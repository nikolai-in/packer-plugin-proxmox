// Copyright IBM Corp. 2019, 2025
// SPDX-License-Identifier: MPL-2.0

package proxmoxlxc

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"
	"time"

	proxmoxapi "github.com/Telmate/proxmox-api-go/proxmox"
	"github.com/hashicorp/hcl/v2/hcldec"
	"github.com/hashicorp/packer-plugin-sdk/communicator"
	"github.com/hashicorp/packer-plugin-sdk/communicator/ssh"
	"github.com/hashicorp/packer-plugin-sdk/multistep"
	"github.com/hashicorp/packer-plugin-sdk/multistep/commonsteps"
	packersdk "github.com/hashicorp/packer-plugin-sdk/packer"
)

// The unique id for the builder.
const BuilderID = "proxmox.lxc"

type Builder struct {
	config Config
}

// Builder implements packersdk.Builder.
var _ packersdk.Builder = &Builder{}

func (b *Builder) ConfigSpec() hcldec.ObjectSpec { return b.config.FlatMapstructure().HCL2Spec() }

func (b *Builder) Prepare(raws ...interface{}) ([]string, []string, error) {
	return b.config.Prepare(raws...)
}

func (b *Builder) Run(ctx context.Context, ui packersdk.Ui, hook packersdk.Hook) (packersdk.Artifact, error) {
	client, err := newProxmoxClient(b.config)
	if err != nil {
		return nil, err
	}

	state := new(multistep.BasicStateBag)
	state.Put("config", &b.config)
	state.Put("proxmoxClient", client)
	state.Put("hook", hook)
	state.Put("ui", ui)

	comm := communicator.Config{
		Type: b.config.Type,
		SSH: communicator.SSH{
			SSHHost:     b.config.SSHHost,
			SSHPort:     b.config.SSHPort,
			SSHUsername: b.config.SSHUsername,
			SSHPassword: b.config.SSHPassword,
			SSHTimeout:  b.config.SSHTimeout,
		},
	}

	if errs := comm.Prepare(&b.config.Ctx); len(errs) > 0 {
		var merr *packersdk.MultiError
		for _, err := range errs {
			merr = packersdk.MultiErrorAppend(merr, err)
		}
		return nil, merr
	}

	steps := []multistep.Step{
		&stepPrepareSSHKeyPair{Comm: &comm},
		&stepStartLXC{},
		&communicator.StepConnect{
			Config:    &comm,
			Host:      commHost(comm.SSHHost),
			SSHConfig: comm.SSHConfigFunc(),
		},
		&commonsteps.StepProvision{},
		&commonsteps.StepCleanupTempKeys{
			Comm: &comm,
		},
		&stepStopLXC{},
		&stepConvertLXC{},
		&stepDeleteLXC{},
		&stepSuccess{},
	}

	runner := commonsteps.NewRunner(steps, b.config.PackerConfig, ui)
	runner.Run(ctx, state)

	if rawErr, ok := state.GetOk("error"); ok {
		return nil, rawErr.(error)
	}
	if _, ok := state.GetOk(multistep.StateCancelled); ok {
		return nil, errors.New("build was cancelled")
	}

	vmRef, ok := state.Get("vmRef").(*proxmoxapi.VmRef)
	if !ok {
		return nil, fmt.Errorf("LXC VM reference could not be determined")
	}
	containerID, ok := state.Get("instance_id").(int)
	if !ok {
		return nil, fmt.Errorf("LXC container ID could not be determined")
	}

	return &Artifact{
		builderID:     BuilderID,
		containerID:   containerID,
		proxmoxClient: client,
		vmRef:         vmRef,
		StateData:     map[string]interface{}{},
	}, nil
}

func commHost(host string) func(state multistep.StateBag) (string, error) {
	if host != "" {
		return func(state multistep.StateBag) (string, error) {
			return host, nil
		}
	}
	return getLXCIP
}

func getLXCIP(state multistep.StateBag) (string, error) {
	client := state.Get("proxmoxClient").(*proxmoxapi.Client)
	vmRef := state.Get("vmRef").(*proxmoxapi.VmRef)
	c := state.Get("config").(*Config)

	sshPort := c.SSHPort
	if sshPort == 0 {
		sshPort = 22
	}

	deadline := time.Now().Add(5 * time.Minute)
	var lastErr error

	for time.Now().Before(deadline) {
		ips, err := fetchLXCIPs(client, vmRef)
		if err == nil && len(ips) > 0 {
			for _, ip := range ips {
				if sshReachable(ip, sshPort) {
					return ip, nil
				}
				lastErr = fmt.Errorf("ssh not reachable at %s:%d", ip, sshPort)
			}
		} else if err != nil {
			lastErr = err
		}
		time.Sleep(5 * time.Second)
	}

	if lastErr != nil {
		return "", lastErr
	}
	return "", fmt.Errorf("no IP address found for LXC container")
}

func fetchLXCIPs(client *proxmoxapi.Client, vmRef *proxmoxapi.VmRef) ([]string, error) {
	url := fmt.Sprintf("/nodes/%s/lxc/%d/interfaces", vmRef.Node(), vmRef.VmId())
	data := map[string]interface{}{}

	if err := client.GetJsonRetryable(url, &data, 3); err != nil {
		return nil, err
	}

	raw, ok := data["data"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("unexpected LXC interface response")
	}

	var ips []string

	for _, item := range raw {
		iface, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		ipsRaw, ok := iface["ip-addresses"].([]interface{})
		if !ok {
			continue
		}
		for _, ipItem := range ipsRaw {
			ipMap, ok := ipItem.(map[string]interface{})
			if !ok {
				continue
			}
			for _, key := range []string{"ip-address", "inet", "address"} {
				if val, ok := ipMap[key].(string); ok {
					ip := net.ParseIP(val)
					if ip != nil && ip.To4() != nil && !ip.IsLoopback() {
						ips = append(ips, val)
					}
				}
			}
		}
	}

	if len(ips) == 0 {
		return nil, fmt.Errorf("no IP address found for LXC container")
	}
	return ips, nil
}

func sshReachable(host string, port int) bool {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

type stepPrepareSSHKeyPair struct {
	Comm *communicator.Config
}

func (s *stepPrepareSSHKeyPair) Run(ctx context.Context, state multistep.StateBag) multistep.StepAction {
	ui := state.Get("ui").(packersdk.Ui)
	if s.Comm.SSHPassword != "" {
		return multistep.ActionContinue
	}

	ui.Say("Creating ephemeral key pair for SSH communicator...")

	kp, err := ssh.NewKeyPair(ssh.CreateKeyPairConfig{
		Comment: fmt.Sprintf("packer-%d", time.Now().UnixNano()),
	})
	if err != nil {
		state.Put("error", fmt.Errorf("error creating temporary keypair: %s", err))
		return multistep.ActionHalt
	}

	s.Comm.SSHKeyPairName = kp.Comment
	s.Comm.SSHTemporaryKeyPairName = kp.Comment
	s.Comm.SSHPrivateKey = kp.PrivateKeyPemBlock
	s.Comm.SSHPublicKey = kp.PublicKeyAuthorizedKeysLine
	s.Comm.SSHClearAuthorizedKeys = true

	state.Put("ssh_public_key", string(kp.PublicKeyAuthorizedKeysLine))

	ui.Say("Created ephemeral SSH key pair for communicator")

	return multistep.ActionContinue
}

func (s *stepPrepareSSHKeyPair) Cleanup(state multistep.StateBag) {}

type stepStartLXC struct{}

func (s *stepStartLXC) Run(ctx context.Context, state multistep.StateBag) multistep.StepAction {
	ui := state.Get("ui").(packersdk.Ui)
	client := state.Get("proxmoxClient").(*proxmoxapi.Client)
	c := state.Get("config").(*Config)

	vmid := c.VMID
	if vmid == 0 {
		ui.Say("No VM ID given, getting next free from Proxmox")
		var err error
		vmid, err = client.GetNextID(0)
		if err != nil {
			state.Put("error", err)
			ui.Error(err.Error())
			return multistep.ActionHalt
		}
	}

	ui.Say("Creating LXC container")
	params := c.createAPIParams(vmid)
	if pubKeyRaw, ok := state.GetOk("ssh_public_key"); ok {
		if pubKey, ok := pubKeyRaw.(string); ok && strings.TrimSpace(pubKey) != "" {
			params["ssh-public-keys"] = pubKey
		}
	}
	exitStatus, err := client.CreateLxcContainer(c.Node, params)
	if err != nil {
		err := fmt.Errorf("error creating LXC container: %w (status: %s)", err, exitStatus)
		state.Put("error", err)
		ui.Error(err.Error())
		return multistep.ActionHalt
	}

	vmRef := proxmoxapi.NewVmRef(vmid)
	vmRef.SetNode(c.Node)
	vmRef.SetVmType("lxc")
	if c.Pool != "" {
		vmRef.SetPool(c.Pool)
	}

	state.Put("vmRef", vmRef)
	state.Put("instance_id", vmid)

	if c.Start {
		ui.Say("LXC container already started by Proxmox")
		return multistep.ActionContinue
	}

	ui.Say("Starting LXC container")
	if _, err := client.StartVm(vmRef); err != nil {
		if strings.Contains(err.Error(), "already running") {
			ui.Say("LXC container is already running")
			return multistep.ActionContinue
		}
		err := fmt.Errorf("error starting LXC container: %w", err)
		state.Put("error", err)
		ui.Error(err.Error())
		return multistep.ActionHalt
	}

	return multistep.ActionContinue
}

func (s *stepStartLXC) Cleanup(state multistep.StateBag) {
	vmRefUntyped, ok := state.GetOk("vmRef")
	if !ok {
		return
	}
	if _, ok := state.GetOk("success"); ok {
		return
	}

	client := state.Get("proxmoxClient").(*proxmoxapi.Client)
	vmRef := vmRefUntyped.(*proxmoxapi.VmRef)

	_, _ = client.StopVm(vmRef)
	_, _ = client.DeleteVm(vmRef)
}

type stepStopLXC struct{}

func (s *stepStopLXC) Run(ctx context.Context, state multistep.StateBag) multistep.StepAction {
	ui := state.Get("ui").(packersdk.Ui)
	client := state.Get("proxmoxClient").(*proxmoxapi.Client)
	vmRef := state.Get("vmRef").(*proxmoxapi.VmRef)

	ui.Say("Stopping LXC container")
	if _, err := client.StopVm(vmRef); err != nil {
		err := fmt.Errorf("error stopping LXC container: %w", err)
		state.Put("error", err)
		ui.Error(err.Error())
		return multistep.ActionHalt
	}

	return multistep.ActionContinue
}

func (s *stepStopLXC) Cleanup(state multistep.StateBag) {}

type stepConvertLXC struct{}

func (s *stepConvertLXC) Run(ctx context.Context, state multistep.StateBag) multistep.StepAction {
	ui := state.Get("ui").(packersdk.Ui)
	client := state.Get("proxmoxClient").(*proxmoxapi.Client)
	vmRef := state.Get("vmRef").(*proxmoxapi.VmRef)
	c := state.Get("config").(*Config)

	if !c.Template {
		ui.Say("Skipping LXC template conversion (template=false)")
		return multistep.ActionContinue
	}

	if c.TemplateName != "" || c.TemplateDescription != "" {
		ui.Say("Updating LXC template metadata")
		params := map[string]interface{}{}
		if c.TemplateName != "" {
			params["hostname"] = c.TemplateName
		}
		if c.TemplateDescription != "" {
			params["description"] = c.TemplateDescription
		}
		if _, err := client.SetLxcConfig(vmRef, params); err != nil {
			err := fmt.Errorf("error updating LXC template metadata: %w", err)
			state.Put("error", err)
			ui.Error(err.Error())
			return multistep.ActionHalt
		}
	}

	ui.Say("Converting LXC container to template")
	url := fmt.Sprintf("/nodes/%s/lxc/%d/template", vmRef.Node(), vmRef.VmId())
	if _, err := client.PostWithTask(map[string]interface{}{}, url); err != nil {
		err := fmt.Errorf("error converting LXC container to template: %w", err)
		state.Put("error", err)
		ui.Error(err.Error())
		return multistep.ActionHalt
	}

	return multistep.ActionContinue
}

func (s *stepConvertLXC) Cleanup(state multistep.StateBag) {}

type stepDeleteLXC struct{}

func (s *stepDeleteLXC) Run(ctx context.Context, state multistep.StateBag) multistep.StepAction {
	ui := state.Get("ui").(packersdk.Ui)
	client := state.Get("proxmoxClient").(*proxmoxapi.Client)
	vmRef := state.Get("vmRef").(*proxmoxapi.VmRef)
	c := state.Get("config").(*Config)

	if c.Template {
		ui.Say("Skipping LXC deletion (template=true)")
		return multistep.ActionContinue
	}
	if !c.DeleteAfterBuild {
		ui.Say("Skipping LXC deletion (delete_after_build=false)")
		return multistep.ActionContinue
	}

	ui.Say("Deleting LXC container")
	if _, err := client.DeleteVm(vmRef); err != nil {
		err := fmt.Errorf("error deleting LXC container: %w", err)
		state.Put("error", err)
		ui.Error(err.Error())
		return multistep.ActionHalt
	}

	return multistep.ActionContinue
}

func (s *stepDeleteLXC) Cleanup(state multistep.StateBag) {}

type stepSuccess struct{}

func (s *stepSuccess) Run(ctx context.Context, state multistep.StateBag) multistep.StepAction {
	state.Put("success", true)
	return multistep.ActionContinue
}

func (s *stepSuccess) Cleanup(state multistep.StateBag) {}

func newProxmoxClient(config Config) (*proxmoxapi.Client, error) {
	tlsConfig := &tls.Config{
		InsecureSkipVerify: config.SkipCertValidation,
	}

	client, err := proxmoxapi.NewClient(strings.TrimSuffix(config.proxmoxURL.String(), "/"), nil, "", tlsConfig, "", int(config.TaskTimeout.Seconds()))
	if err != nil {
		return nil, err
	}

	*proxmoxapi.Debug = config.PackerDebug

	if config.Token != "" {
		log.Print("using token auth")
		client.SetAPIToken(config.Username, config.Token)
	} else {
		log.Print("using password auth")
		err = client.Login(config.Username, config.Password, "")
		if err != nil {
			return nil, err
		}
	}

	return client, nil
}
