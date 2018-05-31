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

package vsphere

import (
	"context"
	"errors"
	"sync"

	log "github.com/sirupsen/logrus"
)

var (
	// ErrVCAlreadyRegistered is returned when registration for a previously
	// registered virtual center is attempted.
	ErrVCAlreadyRegistered = errors.New("virtual center was already registered")
	// ErrVCNotFound is returned when a virtual center instance isn't found.
	ErrVCNotFound = errors.New("virtual center wasn't found in registry")
)

// VirtualCenterManager provides functionality to manage virtual centers.
type VirtualCenterManager interface {
	// GetVirtualCenter returns the VirtualCenter instance given the host.
	GetVirtualCenter(host string) (*VirtualCenter, error)
	// GetAllVirtualCenters returns all VirtualCenter instances. If virtual
	// centers are added or removed concurrently, they may or may not be
	// reflected in the result of a call to this method.
	GetAllVirtualCenters() []*VirtualCenter
	// RegisterVirtualCenter registers a virtual center, but doesn't initiate
	// the connection to the host.
	RegisterVirtualCenter(config *VirtualCenterConfig) (*VirtualCenter, error)
	// UnregisterVirtualCenter disconnects and unregisters the virtual center
	// given it's host.
	UnregisterVirtualCenter(host string) error
	// UnregisterAllVirtualCenters disconnects and unregisters all virtual centers.
	UnregisterAllVirtualCenters() error
}

var (
	// vcManagerInst is a VirtualCenterManager singleton.
	vcManagerInst *defaultVirtualCenterManager
	// onceForVCManager is used for initializing the VirtualCenterManager singleton.
	onceForVCManager sync.Once
)

// GetVirtualCenterManager returns the VirtualCenterManager singleton.
func GetVirtualCenterManager() VirtualCenterManager {
	onceForVCManager.Do(func() {
		log.Info("Initializing defaultVirtualCenterManager...")
		vcManagerInst = &defaultVirtualCenterManager{virtualCenters: sync.Map{}}
		log.Info("Successfully initialized defaultVirtualCenterManager")
	})
	return vcManagerInst
}

// defaultVirtualCenterManager holds virtual center information and provides
// functionality around it.
type defaultVirtualCenterManager struct {
	// virtualCenters map hosts to *VirtualCenter instances.
	virtualCenters sync.Map
}

func (m *defaultVirtualCenterManager) GetVirtualCenter(host string) (*VirtualCenter, error) {
	if vc, exists := m.virtualCenters.Load(host); exists {
		return vc.(*VirtualCenter), nil
	}
	log.WithField("host", host).Error("Couldn't find VC in registry")
	return nil, ErrVCNotFound
}

func (m *defaultVirtualCenterManager) GetAllVirtualCenters() []*VirtualCenter {
	var vcs []*VirtualCenter
	m.virtualCenters.Range(func(_, vcInf interface{}) bool {
		// If an entry was concurrently deleted from virtualCenters, Range could
		// possibly return a nil value for that key.
		// See https://golang.org/pkg/sync/#Map.Range for more info.
		if vcInf != nil {
			vcs = append(vcs, vcInf.(*VirtualCenter))
		}
		return true
	})
	return vcs
}

func (m *defaultVirtualCenterManager) RegisterVirtualCenter(config *VirtualCenterConfig) (*VirtualCenter, error) {
	if _, exists := m.virtualCenters.Load(config.Host); exists {
		log.WithField("config", config).Error("VC was already found in registry, failed to register")
		return nil, ErrVCAlreadyRegistered
	}

	vc := &VirtualCenter{Config: config} // Note that the Client isn't initialized here.
	m.virtualCenters.Store(config.Host, vc)
	log.WithField("vc", vc).Info("Successfully registered VC")
	return vc, nil
}

func (m *defaultVirtualCenterManager) UnregisterVirtualCenter(host string) error {
	vc, err := m.GetVirtualCenter(host)
	if err != nil {
		log.WithField("host", host).Error("Failed to find VC, couldn't unregister")
		return err
	}
	if err := vc.DisconnectPbm(context.Background()); err != nil {
		log.WithField("host", host).Error("Failed to disconnect VC pbm, couldn't unregister")
		return err
	}
	if err := vc.Disconnect(context.Background()); err != nil {
		log.WithField("host", host).Error("Failed to disconnect VC, couldn't unregister")
		return err
	}

	m.virtualCenters.Delete(host)
	log.WithField("host", host).Info("Successfully unregistered VC")
	return nil
}

func (m *defaultVirtualCenterManager) UnregisterAllVirtualCenters() error {
	var err error
	m.virtualCenters.Range(func(hostInf, _ interface{}) bool {
		if err = m.UnregisterVirtualCenter(hostInf.(string)); err != nil {
			log.WithField("host", hostInf).Error("Failed to unregister VC")
			return false
		}
		return true
	})
	return err
}
