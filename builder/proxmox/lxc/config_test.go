// Copyright IBM Corp. 2019, 2025
// SPDX-License-Identifier: MPL-2.0

package proxmoxlxc

import (
	"testing"
)

func TestCreateAPIParams(t *testing.T) {
	c := &Config{
		VMName:       "ct-test",
		Ostemplate:   "local:vztmpl/debian-12-standard_12.7-1_amd64.tar.zst",
		Memory:       1024,
		Cores:        2,
		RootFS:       "local-lvm:8",
		Start:        true,
		Onboot:       true,
		Unprivileged: true,
		NetworkAdapters: []NetworkAdapterConfig{
			{
				Name:     "myct0",
				Bridge:   "vmbr0",
				Firewall: true,
				IP:       "dhcp",
				VLANTag:  "10",
			},
		},
		LXCConfig: map[string]interface{}{
			"password": "secret",
			"swap":     1024,
		},
	}

	params := c.createAPIParams(601)

	if got, ok := params["vmid"].(int); !ok || got != 601 {
		t.Fatalf("expected vmid=601, got %v", params["vmid"])
	}
	if got := params["ostemplate"]; got != c.Ostemplate {
		t.Fatalf("expected ostemplate to be copied, got %v", got)
	}
	if got := params["net0"]; got != "name=myct0,bridge=vmbr0,firewall=1,ip=dhcp,tag=10" {
		t.Fatalf("unexpected net0 value: %v", got)
	}
	if got := params["password"]; got != "secret" {
		t.Fatalf("expected lxc_config to be merged, got %v", got)
	}
	if got := params["swap"]; got != 1024 {
		t.Fatalf("expected typed lxc_config integer to be merged, got %v", got)
	}
	if got := params["start"]; got != true {
		t.Fatalf("expected start=true, got %v", got)
	}
	if got := params["onboot"]; got != true {
		t.Fatalf("expected onboot=true, got %v", got)
	}
	if got := params["unprivileged"]; got != true {
		t.Fatalf("expected unprivileged=true, got %v", got)
	}
}

func TestNetworkAdapterParamDefaults(t *testing.T) {
	nic := &NetworkAdapterConfig{
		Bridge: "vmbr1",
	}

	got := nic.apiParam(3)
	want := "name=eth3,bridge=vmbr1"
	if got != want {
		t.Fatalf("unexpected network param, got %q want %q", got, want)
	}
}

func TestCreateAPIParamsIncludesFalseBooleans(t *testing.T) {
	c := &Config{
		VMName:     "ct-test",
		Ostemplate: "local:vztmpl/debian-12-standard_12.7-1_amd64.tar.zst",
		Memory:     512,
		Cores:      1,
	}

	params := c.createAPIParams(602)

	if got := params["start"]; got != false {
		t.Fatalf("expected start=false, got %v", got)
	}
	if got := params["onboot"]; got != false {
		t.Fatalf("expected onboot=false, got %v", got)
	}
	if got := params["unprivileged"]; got != false {
		t.Fatalf("expected unprivileged=false, got %v", got)
	}
}
