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

	log "github.com/sirupsen/logrus"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/soap"
	vimtypes "github.com/vmware/govmomi/vim25/types"
	"gitlab.eng.vmware.com/hatchway/common-csp/cns/methods"
	cnstypes "gitlab.eng.vmware.com/hatchway/common-csp/cns/types"
)

// Namespace and Path constants
const (
	Namespace = "vsan"
	Path      = "/vsanHealth"
)

var (
	CnsVolumeManagerInstance = vimtypes.ManagedObjectReference{
		Type:  "CnsVolumeManager",
		Value: "cns-volume-manager",
	}
	CnsCnsTaskResultManagerInstance = vimtypes.ManagedObjectReference{
		Type:  "CnsTaskResultManager",
		Value: "cns-task-result-manager",
	}
)

type CNSClient struct {
	*soap.Client
}

// NewCnsClient creates a new CNS client
func NewCnsClient(ctx context.Context, c *vim25.Client) (*CNSClient, error) {
	sc := c.Client.NewServiceClient(Path, Namespace)
	return &CNSClient{sc}, nil
}

// ConnectCns creates a CNS client for the virtual center.
func (vc *VirtualCenter) ConnectCns(ctx context.Context) error {
	clientMutex.Lock()
	defer clientMutex.Unlock()

	if vc.CnsClient == nil {
		var err error
		if vc.CnsClient, err = NewCnsClient(ctx, vc.Client.Client); err != nil {
			log.WithFields(log.Fields{
				"host": vc.Config.Host, "err": err,
			}).Error("Failed to create CNS client on vCenter host")
			return err
		}
	}
	return nil
}

// DisconnectCns destroys the CNS client for the virtual center.
func (vc *VirtualCenter) DisconnectCns(ctx context.Context) {
	clientMutex.Lock()
	defer clientMutex.Unlock()
	if vc.CnsClient == nil {
		log.Info("CnsClient wasn't connected, ignoring")
	} else {
		vc.CnsClient = nil
	}
}

// CreateVolume calls the CNS create API.
func (vc *VirtualCenter) CreateVolume(ctx context.Context, createSpecList []cnstypes.CnsVolumeCreateSpec) (*object.Task, error) {
	req := cnstypes.CnsCreateVolume{
		This:        CnsVolumeManagerInstance,
		CreateSpecs: createSpecList,
	}
	err := vc.ConnectCns(ctx)
	if err != nil {
		return nil, err
	}
	res, err := methods.CnsCreateVolume(ctx, vc.CnsClient.Client, &req)
	if err != nil {
		return nil, err
	}
	return object.NewTask(vc.Client.Client, res.Returnval), nil
}

// UpdateVolume calls the CNS update API.
func (vc *VirtualCenter) UpdateVolume(ctx context.Context, updateSpecList []cnstypes.CnsVolumeUpdateSpec) (*object.Task, error) {
	req := cnstypes.CnsUpdateVolume{
		This:        CnsVolumeManagerInstance,
		UpdateSpecs: updateSpecList,
	}
	err := vc.ConnectCns(ctx)
	if err != nil {
		return nil, err
	}
	res, err := methods.CnsUpdateVolume(ctx, vc.CnsClient.Client, &req)
	if err != nil {
		return nil, err
	}
	return object.NewTask(vc.Client.Client, res.Returnval), nil
}

// DeleteVolume calls the CNS delete API.
func (vc *VirtualCenter) DeleteVolume(ctx context.Context, volumeIDList []cnstypes.CnsVolumeId) (*object.Task, error) {
	req := cnstypes.CnsDeleteVolume{
		This:      CnsVolumeManagerInstance,
		VolumeIds: volumeIDList,
	}
	err := vc.ConnectCns(ctx)
	if err != nil {
		return nil, err
	}
	res, err := methods.CnsDeleteVolume(ctx, vc.CnsClient.Client, &req)
	if err != nil {
		return nil, err
	}
	return object.NewTask(vc.Client.Client, res.Returnval), nil
}

// AttachVolume calls the CNS Attach API.
func (vc *VirtualCenter) AttachVolume(ctx context.Context, attachSpecList []cnstypes.CnsVolumeAttachDetachSpec) (*object.Task, error) {
	req := cnstypes.CnsAttachVolume{
		This:        CnsVolumeManagerInstance,
		AttachSpecs: attachSpecList,
	}
	err := vc.ConnectCns(ctx)
	if err != nil {
		return nil, err
	}
	res, err := methods.CnsAttachVolume(ctx, vc.CnsClient.Client, &req)
	if err != nil {
		return nil, err
	}
	return object.NewTask(vc.Client.Client, res.Returnval), nil
}

// DetachVolume calls the CNS Detach API.
func (vc *VirtualCenter) DetachVolume(ctx context.Context, detachSpecList []cnstypes.CnsVolumeAttachDetachSpec) (*object.Task, error) {
	req := cnstypes.CnsDetachVolume{
		This:        CnsVolumeManagerInstance,
		DetachSpecs: detachSpecList,
	}
	err := vc.ConnectCns(ctx)
	if err != nil {
		return nil, err
	}
	res, err := methods.CnsDetachVolume(ctx, vc.CnsClient.Client, &req)
	if err != nil {
		return nil, err
	}
	return object.NewTask(vc.Client.Client, res.Returnval), nil
}

// GetTaskResult calls the CNS GetTaskResult API.
func (vc *VirtualCenter) GetTaskResult(ctx context.Context, taskIDList []string) ([]vimtypes.KeyAnyValue, error) {
	req := cnstypes.CnsGetTaskResult{
		This:    CnsCnsTaskResultManagerInstance,
		TaskIds: taskIDList,
	}
	err := vc.ConnectCns(ctx)
	if err != nil {
		return nil, err
	}
	res, err := methods.CnsGetTaskResult(ctx, vc.CnsClient.Client, &req)
	if err != nil {
		return nil, err
	}
	return res.Returnval, nil
}
