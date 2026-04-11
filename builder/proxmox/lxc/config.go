// Copyright IBM Corp. 2019, 2025
// SPDX-License-Identifier: MPL-2.0

package proxmoxlxc

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/packer-plugin-sdk/common"
	packersdk "github.com/hashicorp/packer-plugin-sdk/packer"
	"github.com/hashicorp/packer-plugin-sdk/template/config"
	"github.com/hashicorp/packer-plugin-sdk/template/interpolate"
	"github.com/hashicorp/packer-plugin-sdk/uuid"
	"github.com/mitchellh/mapstructure"
)

type Config struct {
	common.PackerConfig `mapstructure:",squash"`

	ProxmoxURLRaw string `mapstructure:"proxmox_url"`
	proxmoxURL    *url.URL

	SkipCertValidation bool          `mapstructure:"insecure_skip_tls_verify"`
	Username           string        `mapstructure:"username"`
	Password           string        `mapstructure:"password"`
	Token              string        `mapstructure:"token"`
	Node               string        `mapstructure:"node"`
	Pool               string        `mapstructure:"pool"`
	TaskTimeout        time.Duration `mapstructure:"task_timeout"`

	VMName string `mapstructure:"vm_name"`
	VMID   int    `mapstructure:"vm_id"`

	Ostemplate   string `mapstructure:"ostemplate"`
	RootFS       string `mapstructure:"rootfs"`
	Memory       int    `mapstructure:"memory"`
	Cores        int    `mapstructure:"cores"`
	Onboot       bool   `mapstructure:"onboot"`
	Start        bool   `mapstructure:"start"`
	Tags         string `mapstructure:"tags"`
	Nameserver   string `mapstructure:"nameserver"`
	SearchDomain string `mapstructure:"searchdomain"`
	Unprivileged bool   `mapstructure:"unprivileged"`

	NetworkAdapters []NetworkAdapterConfig `mapstructure:"network_adapters"`
	LXCConfig       map[string]string      `mapstructure:"lxc_config"`

	Ctx interpolate.Context `mapstructure-to-hcl2:",skip"`
}

type NetworkAdapterConfig struct {
	Name        string `mapstructure:"name"`
	Bridge      string `mapstructure:"bridge"`
	Firewall    bool   `mapstructure:"firewall"`
	Gateway     string `mapstructure:"gateway"`
	GatewayIPv6 string `mapstructure:"gateway_ipv6"`
	IP          string `mapstructure:"ip"`
	IPv6        string `mapstructure:"ipv6"`
	MACAddress  string `mapstructure:"mac_address"`
	MTU         uint16 `mapstructure:"mtu"`
	Rate        string `mapstructure:"rate"`
	VLANTag     string `mapstructure:"vlan_tag"`
	Type        string `mapstructure:"type"`
}

func (c *Config) Prepare(raws ...interface{}) ([]string, []string, error) {
	var md mapstructure.Metadata
	err := config.Decode(c, &config.DecodeOpts{
		Metadata:           &md,
		Interpolate:        true,
		InterpolateContext: &c.Ctx,
	}, raws...)
	if err != nil {
		return nil, nil, err
	}

	var errs *packersdk.MultiError

	packersdk.LogSecretFilter.Set(c.Password)

	if c.ProxmoxURLRaw == "" {
		c.ProxmoxURLRaw = os.Getenv("PROXMOX_URL")
	}
	if c.Username == "" {
		c.Username = os.Getenv("PROXMOX_USERNAME")
	}
	if c.Password == "" {
		c.Password = os.Getenv("PROXMOX_PASSWORD")
	}
	if c.Token == "" {
		c.Token = os.Getenv("PROXMOX_TOKEN")
	}
	if c.TaskTimeout == 0 {
		c.TaskTimeout = 60 * time.Second
	}
	if c.VMName == "" {
		c.VMName = fmt.Sprintf("packer-%s", uuid.TimeOrderedUUID())
	}
	if c.Memory < 1 {
		c.Memory = 512
	}
	if c.Cores < 1 {
		c.Cores = 1
	}

	if c.ProxmoxURLRaw == "" {
		errs = packersdk.MultiErrorAppend(errs, errors.New("proxmox_url must be specified"))
	} else if c.proxmoxURL, err = url.Parse(c.ProxmoxURLRaw); err != nil {
		errs = packersdk.MultiErrorAppend(errs, fmt.Errorf("could not parse proxmox_url: %s", err))
	}
	if c.Username == "" {
		errs = packersdk.MultiErrorAppend(errs, errors.New("username must be specified"))
	}
	if c.Password == "" && c.Token == "" {
		errs = packersdk.MultiErrorAppend(errs, errors.New("password or token must be specified"))
	}
	if c.Node == "" {
		errs = packersdk.MultiErrorAppend(errs, errors.New("node must be specified"))
	}
	if c.Ostemplate == "" {
		errs = packersdk.MultiErrorAppend(errs, errors.New("ostemplate must be specified"))
	}
	if c.VMID != 0 && (c.VMID < 100 || c.VMID > 999999999) {
		errs = packersdk.MultiErrorAppend(errs, errors.New("vm_id must be in range 100-999999999"))
	}

	if errs != nil && len(errs.Errors) > 0 {
		return nil, nil, errs
	}
	return nil, nil, nil
}

func (c *Config) createAPIParams(vmid int) map[string]interface{} {
	params := map[string]interface{}{
		"vmid":       vmid,
		"ostemplate": c.Ostemplate,
		"hostname":   c.VMName,
		"memory":     c.Memory,
		"cores":      c.Cores,
	}

	if c.Pool != "" {
		params["pool"] = c.Pool
	}
	if c.RootFS != "" {
		params["rootfs"] = c.RootFS
	}
	if c.Onboot {
		params["onboot"] = c.Onboot
	}
	if c.Start {
		params["start"] = c.Start
	}
	if c.Tags != "" {
		params["tags"] = c.Tags
	}
	if c.Nameserver != "" {
		params["nameserver"] = c.Nameserver
	}
	if c.SearchDomain != "" {
		params["searchdomain"] = c.SearchDomain
	}
	if c.Unprivileged {
		params["unprivileged"] = c.Unprivileged
	}

	for idx, nic := range c.NetworkAdapters {
		params[fmt.Sprintf("net%d", idx)] = nic.apiParam(idx)
	}

	for key, value := range c.LXCConfig {
		params[key] = value
	}

	return params
}

func (n *NetworkAdapterConfig) apiParam(idx int) string {
	parts := make([]string, 0, 12)
	name := n.Name
	if name == "" {
		name = fmt.Sprintf("eth%d", idx)
	}
	parts = append(parts, "name="+name)

	if n.Bridge != "" {
		parts = append(parts, "bridge="+n.Bridge)
	}
	if n.Firewall {
		parts = append(parts, "firewall=1")
	}
	if n.Gateway != "" {
		parts = append(parts, "gw="+n.Gateway)
	}
	if n.GatewayIPv6 != "" {
		parts = append(parts, "gw6="+n.GatewayIPv6)
	}
	if n.IP != "" {
		parts = append(parts, "ip="+n.IP)
	}
	if n.IPv6 != "" {
		parts = append(parts, "ip6="+n.IPv6)
	}
	if n.MACAddress != "" {
		parts = append(parts, "hwaddr="+n.MACAddress)
	}
	if n.MTU > 0 {
		parts = append(parts, "mtu="+strconv.Itoa(int(n.MTU)))
	}
	if n.Rate != "" {
		parts = append(parts, "rate="+n.Rate)
	}
	if n.VLANTag != "" {
		parts = append(parts, "tag="+n.VLANTag)
	}
	if n.Type != "" {
		parts = append(parts, "type="+n.Type)
	}

	return strings.Join(parts, ",")
}
