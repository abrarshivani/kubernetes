// Copyright 2018 VMware, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package node

import (
	"errors"
	"sync"

	log "github.com/sirupsen/logrus"
	"gitlab.eng.vmware.com/hatchway/common-csp/pkg/vsphere"
)

var (
	// ErrNodeNotFound is returned when a node isn't found.
	ErrNodeNotFound = errors.New("node wasn't found")
	// ErrNodeAlreadyRegistered is returned when registration is attempted for
	// a previously registered node.
	ErrNodeAlreadyRegistered = errors.New("node was already registered")
)

// Manager provides functionality to manage nodes.
type Manager interface {
	// RegisterNode registers a node given its UUID and Metadata.
	RegisterNode(nodeUUID string, metadata Metadata) error
	// DiscoverNode discovers a registered node given its UUID. This method
	// scans all virtual centers registered on the VirtualCenterManager for a
	// virtual machine with the given UUID.
	DiscoverNode(nodeUUID string) error
	// GetNodeMetadata returns Metadata for a registered node given its UUID.
	GetNodeMetadata(nodeUUID string) (Metadata, error)
	// GetAllNodeMetadata returns Metadata for all registered nodes.
	GetAllNodeMetadata() []Metadata
	// GetNode refreshes and returns the VirtualMachine for a registered node
	// given its UUID.
	GetNode(nodeUUID string) (*vsphere.VirtualMachine, error)
	// GetAllNodes refreshes and returns VirtualMachine for all registered
	// nodes. If nodes are added or removed concurrently, they may or may not be
	// reflected in the result of a call to this method.
	GetAllNodes() ([]*vsphere.VirtualMachine, error)
	// UnregisterNode unregisters a registered node given its UUID.
	UnregisterNode(nodeUUID string) error
}

// Metadata represents node metadata.
type Metadata interface{}

var (
	// managerInstance is a Manager singleton.
	managerInstance *defaultManager
	// onceForManager is used for initializing the Manager singleton.
	onceForManager sync.Once
)

// GetManager returns the Manager singleton.
func GetManager() Manager {
	onceForManager.Do(func() {
		log.Info("Initializing node.defaultManager...")
		managerInstance = &defaultManager{
			nodeVMs:      sync.Map{},
			nodeMetadata: sync.Map{},
		}
		log.Info("node.defaultManager initialized")
	})
	return managerInstance
}

// defaultManager holds node information and provides functionality around it.
type defaultManager struct {
	// nodeVMs maps node UUIDs to VirtualMachine objects.
	nodeVMs sync.Map
	// nodeMetadata maps node UUIDs to generic metadata.
	nodeMetadata sync.Map
}

func (m *defaultManager) RegisterNode(nodeUUID string, metadata Metadata) error {
	if _, exists := m.nodeMetadata.Load(nodeUUID); exists {
		log.WithFields(log.Fields{
			"nodeUUID": nodeUUID, "metadata": metadata,
		}).Error("Node already exists, failed to register")
		return ErrNodeAlreadyRegistered
	}

	m.nodeMetadata.Store(nodeUUID, metadata)
	log.WithFields(log.Fields{
		"nodeUUID": nodeUUID, "metadata": metadata,
	}).Error("Successfully registered node")

	return m.DiscoverNode(nodeUUID)
}

func (m *defaultManager) DiscoverNode(nodeUUID string) error {
	if _, err := m.GetNodeMetadata(nodeUUID); err != nil {
		log.WithFields(log.Fields{
			"nodeUUID": nodeUUID, "err": err,
		}).Error("Node wasn't found, failed to discover")
		return err
	}

	vm, err := vsphere.GetVirtualMachineByUUID(nodeUUID)
	if err != nil {
		log.WithFields(log.Fields{
			"nodeUUID": nodeUUID, "err": err,
		}).Error("Couldn't find VM instance, failed to discover")
		return err
	}

	m.nodeVMs.Store(nodeUUID, vm)
	log.WithFields(log.Fields{
		"nodeUUID": nodeUUID, "vm": vm,
	}).Info("Successfully discovered node")
	return nil
}

func (m *defaultManager) GetNodeMetadata(nodeUUID string) (Metadata, error) {
	if metadata, ok := m.nodeMetadata.Load(nodeUUID); ok {
		log.WithFields(log.Fields{
			"nodeUUID": nodeUUID, "metadata": metadata,
		}).Info("Node metadata was found")
		return metadata, nil
	}
	log.WithField("nodeUUID", nodeUUID).Error("Node metadata wasn't found")
	return nil, ErrNodeNotFound
}

func (m *defaultManager) GetAllNodeMetadata() []Metadata {
	var nodeMetadata []Metadata
	m.nodeMetadata.Range(func(_, metadataInf interface{}) bool {
		nodeMetadata = append(nodeMetadata, metadataInf.(Metadata))
		return true
	})
	return nodeMetadata
}

func (m *defaultManager) GetNode(nodeUUID string) (*vsphere.VirtualMachine, error) {
	vmInf, discovered := m.nodeVMs.Load(nodeUUID)
	if !discovered {
		log.WithField("nodeUUID", nodeUUID).Info("Node hasn't been discovered yet")

		if err := m.DiscoverNode(nodeUUID); err != nil {
			log.WithFields(log.Fields{
				"nodeUUID": nodeUUID, "err": err,
			}).Error("Failed to discover node")
			return nil, err
		}

		vmInf, _ = m.nodeVMs.Load(nodeUUID)
		log.WithFields(log.Fields{
			"nodeUUID": nodeUUID, "vm": vmInf,
		}).Info("Node was successfully discovered")
		return vmInf.(*vsphere.VirtualMachine), nil
	}

	vm := vmInf.(*vsphere.VirtualMachine)
	log.WithFields(log.Fields{
		"nodeUUID": nodeUUID, "vm": vm,
	}).Info("Renewing virtual machine")

	if err := vm.Renew(true); err != nil {
		log.WithFields(log.Fields{
			"nodeUUID": nodeUUID, "vm": vm, "err": err,
		}).Error("Failed to renew VM")
		return nil, err
	}

	log.WithFields(log.Fields{
		"nodeUUID": nodeUUID, "vm": vm,
	}).Info("VM was successfully renewed")
	return vm, nil
}

func (m *defaultManager) GetAllNodes() ([]*vsphere.VirtualMachine, error) {
	var vms []*vsphere.VirtualMachine
	var err error
	reconnectedHosts := make(map[string]bool)

	m.nodeVMs.Range(func(nodeUUIDInf, vmInf interface{}) bool {
		// If an entry was concurrently deleted from vm, Range could
		// possibly return a nil value for that key.
		// See https://golang.org/pkg/sync/#Map.Range for more info.
		if vmInf == nil {
			log.WithField("nodeUUID", nodeUUIDInf).Warn("VM instance was nil, ignoring")
			return true
		}

		nodeUUID := nodeUUIDInf.(string)
		vm := vmInf.(*vsphere.VirtualMachine)

		if reconnectedHosts[vm.VirtualCenterHost] {
			log.WithFields(log.Fields{
				"nodeUUID": nodeUUID, "vm": vm,
			}).Info("Renewing VM, no new connection needed")
			err = vm.Renew(false)
		} else {
			log.WithFields(log.Fields{
				"nodeUUID": nodeUUID, "vm": vm,
			}).Info("Renewing VM with new connection")
			err = vm.Renew(true)
			reconnectedHosts[vm.VirtualCenterHost] = true
		}

		if err != nil {
			log.WithFields(log.Fields{
				"nodeUUID": nodeUUID, "vm": vm,
			}).Error("Failed to renew VM, aborting get all nodes")
			return false
		}

		log.WithFields(log.Fields{
			"nodeUUID": nodeUUID, "vm": vm,
		}).Info("Updated VM for node")
		vms = append(vms, vm)
		return true
	})

	if err != nil {
		return nil, err
	}
	return vms, nil
}

func (m *defaultManager) UnregisterNode(nodeUUID string) error {
	if _, err := m.GetNodeMetadata(nodeUUID); err != nil {
		log.WithFields(log.Fields{
			"nodeUUID": nodeUUID, "err": err,
		}).Error("Node wasn't found, failed to unregister")
		return err
	}

	m.nodeMetadata.Delete(nodeUUID)
	m.nodeVMs.Delete(nodeUUID)
	log.WithField("nodeUUID", nodeUUID).Info("Successfully unregistered node")
	return nil
}
