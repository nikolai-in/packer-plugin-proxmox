// Copyright IBM Corp. 2019, 2025
// SPDX-License-Identifier: MPL-2.0

package proxmoxlxc

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"strings"

	proxmoxapi "github.com/Telmate/proxmox-api-go/proxmox"
	"github.com/hashicorp/hcl/v2/hcldec"
	packersdk "github.com/hashicorp/packer-plugin-sdk/packer"
)

// The unique id for the builder.
const BuilderID = "proxmox.lxc"

type Builder struct {
	config Config
}

// Builder implements packersdk.Builder.
var _ packersdk.Builder = &Builder{}

func (b *Builder) ConfigSpec() hcldec.ObjectSpec { return nil }

func (b *Builder) Prepare(raws ...interface{}) ([]string, []string, error) {
	return b.config.Prepare(raws...)
}

func (b *Builder) Run(_ context.Context, ui packersdk.Ui, _ packersdk.Hook) (packersdk.Artifact, error) {
	client, err := newProxmoxClient(b.config)
	if err != nil {
		return nil, err
	}

	vmid := b.config.VMID
	if vmid == 0 {
		ui.Say("No VM ID given, getting next free from Proxmox")
		vmid, err = client.GetNextID(0)
		if err != nil {
			return nil, err
		}
	}

	ui.Say("Creating LXC container")
	exitStatus, err := client.CreateLxcContainer(b.config.Node, b.config.createAPIParams(vmid))
	if err != nil {
		return nil, fmt.Errorf("error creating LXC container: %w (status: %s)", err, exitStatus)
	}

	vmRef := proxmoxapi.NewVmRef(vmid)
	vmRef.SetNode(b.config.Node)
	vmRef.SetVmType("lxc")
	if b.config.Pool != "" {
		vmRef.SetPool(b.config.Pool)
	}

	return &Artifact{
		builderID:     BuilderID,
		containerID:   vmid,
		proxmoxClient: client,
		vmRef:         vmRef,
		StateData:     map[string]interface{}{},
	}, nil
}

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
