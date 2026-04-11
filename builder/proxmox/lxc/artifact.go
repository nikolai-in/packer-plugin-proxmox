// Copyright IBM Corp. 2019, 2025
// SPDX-License-Identifier: MPL-2.0

package proxmoxlxc

import (
	"fmt"
	"log"
	"strconv"

	proxmoxapi "github.com/Telmate/proxmox-api-go/proxmox"
	packersdk "github.com/hashicorp/packer-plugin-sdk/packer"
)

type Artifact struct {
	builderID     string
	containerID   int
	proxmoxClient *proxmoxapi.Client
	vmRef         *proxmoxapi.VmRef
	StateData     map[string]interface{}
}

// Artifact implements packersdk.Artifact.
var _ packersdk.Artifact = &Artifact{}

func (a *Artifact) BuilderId() string {
	return a.builderID
}

func (*Artifact) Files() []string {
	return nil
}

func (a *Artifact) Id() string {
	return strconv.Itoa(a.containerID)
}

func (a *Artifact) String() string {
	return fmt.Sprintf("An LXC container was created: %d", a.containerID)
}

func (a *Artifact) State(name string) interface{} {
	return a.StateData[name]
}

func (a *Artifact) Destroy() error {
	log.Printf("Destroying LXC container: %d", a.containerID)
	if a.vmRef == nil {
		a.vmRef = proxmoxapi.NewVmRef(a.containerID)
	}
	_, err := a.proxmoxClient.DeleteVm(a.vmRef)
	return err
}
