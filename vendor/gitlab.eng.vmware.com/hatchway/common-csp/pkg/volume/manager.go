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

package volume

import (
	"context"
	"fmt"
	"sync"

	log "github.com/sirupsen/logrus"

	vimtypes "github.com/vmware/govmomi/vim25/types"
	cnstypes "gitlab.eng.vmware.com/hatchway/common-csp/cns/types"
	node "gitlab.eng.vmware.com/hatchway/common-csp/pkg/node"
	"gitlab.eng.vmware.com/hatchway/common-csp/pkg/volume/types"
	cnsvsphere "gitlab.eng.vmware.com/hatchway/common-csp/pkg/vsphere"
)

// Manager provides functionality to manage volumes.
type Manager interface {
	// CreateVolume creates a new volume given its spec.
	CreateVolume(spec *types.CreateSpec) (*types.VolumeID, error)
	// AttachVolume attaches a volume to a virtual machine given the spec.
	AttachVolume(spec *types.AttachDetachSpec) (string, error)
	// DetachVolume detaches a volume from the virtual machine given the spec.
	DetachVolume(spec *types.AttachDetachSpec) error
	// DeleteVolume deletes a volume given its spec.
	DeleteVolume(spec *types.DeleteSpec) error
	// UpdateVolume updates a volume given its spec.
	UpdateVolume(spec *types.UpdateSpec) error
	// GetVolumeInfo gets a volume given its spec.
	GetVolumeInfo(spec *types.QuerySpec) (bool, error)
	// VolumesAreAttached checks if a list disks are attached to the given node.
	// Assumption: If node doesn't exist, disks are not attached to the node.
	VolumesAreAttached(nodeMgr node.Manager, nodeVolumes map[string][]*types.VolumeID) (map[string]map[*types.VolumeID]bool, error)
}

var (
	// managerInstance is a Manager singleton.
	managerInstance *defaultManager
	// onceForManager is used for initializing the Manager singleton.
	onceForManager sync.Once
)

// GetManager returns the Manager singleton.
func GetManager(vc *cnsvsphere.VirtualCenter) Manager {
	onceForManager.Do(func() {
		log.Info("Initializing volume.defaultManager...")
		managerInstance = &defaultManager{
			virtualCenter: vc,
		}
		log.Info("volume.defaultManager initialized")
	})
	return managerInstance
}

// DefaultManager provides functionality to manage volumes.
type defaultManager struct {
	virtualCenter *cnsvsphere.VirtualCenter
}

// CreateVolume creates a new volume given its spec.
func (m *defaultManager) CreateVolume(spec *types.CreateSpec) (*types.VolumeID, error) {
	err := validateManager(m)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Set up the VC connection
	err = m.virtualCenter.Connect(ctx)
	if err != nil {
		log.WithFields(log.Fields{
			"err": err,
		}).Error("Failed to connect to Virtual Center")
		return nil, err
	}
	// Construct the CNS VolumeCreateSpec list
	var cnsCreateSpecList []cnstypes.CnsVolumeCreateSpec
	cnsCreateSpec := constructCnsCreateSpecList(spec)
	cnsCreateSpec.ContainerCluster.VSphereUser = m.virtualCenter.Config.Username
	cnsCreateSpecList = append(cnsCreateSpecList, cnsCreateSpec)
	// Call the CNS CreateVolume
	task, err := m.virtualCenter.CreateVolume(ctx, cnsCreateSpecList)
	if err != nil {
		log.WithFields(log.Fields{
			"host": m.virtualCenter.Config.Host, "err": err,
		}).Error("CNS CreateVolume failed")
		return nil, err
	}
	// Get the taskInfo
	taskInfo, err := GetTaskInfo(ctx, task)
	if err != nil {
		log.WithFields(log.Fields{
			"host": m.virtualCenter.Config.Host, "err": err,
		}).Error("Failed to get taskInfo for CreateVolume task")
		return nil, err
	}
	// Get the task results for the given task
	createResults, err := GetTaskInfoResult(ctx, m.virtualCenter, taskInfo)
	if err != nil {
		log.WithFields(log.Fields{
			"host": m.virtualCenter.Config.Host, "err": err,
		}).Error("Failed to get task result for CreateVolume task")
		return nil, err
	}
	for _, res := range createResults {
		if res.Key == taskInfo.Task.Value {
			if res.Value == nil {
				log.WithFields(log.Fields{
					"host": m.virtualCenter.Config.Host, "taskID": taskInfo.Task.Value, "createResults": createResults,
				}).Error("nil returned for CnsVolumeCreateResult")
				break
			}
			createRes := res.Value.(cnstypes.CnsVolumeCreateResult)
			log.WithFields(log.Fields{
				"host": m.virtualCenter.Config.Host, "taskID": taskInfo.Task.Value,
				"volumeID": createRes.VolumeId.Id, "DatastoreURL": createRes.VolumeId.DatastoreUrl,
			}).Info("Successfully retrieved the create Result")
			return &types.VolumeID{
				ID:           createRes.VolumeId.Id,
				DatastoreURL: createRes.VolumeId.DatastoreUrl,
			}, nil
		}
	}
	log.WithFields(log.Fields{
		"host": m.virtualCenter.Config.Host, "taskID": taskInfo.Task.Value, "createResults": createResults,
	}).Error("unable to find the task result for CreateVolume task")
	return nil, fmt.Errorf("Unable to find the taskresult for task: %s on vc: %s",
		taskInfo.Task.Value, m.virtualCenter.Config.Host)
}

// AttachVolume attaches a volume to a virtual machine given the spec.
func (m *defaultManager) AttachVolume(spec *types.AttachDetachSpec) (string, error) {
	err := validateManager(m)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Set up the VC connection
	err = m.virtualCenter.Connect(ctx)
	if err != nil {
		log.WithFields(log.Fields{
			"err": err,
		}).Error("Failed to connect to Virtual Center")
		return "", err
	}
	// Construct the CNS AttachSpec list
	var cnsAttachSpecList []cnstypes.CnsVolumeAttachDetachSpec
	cnsAttachSpec := cnstypes.CnsVolumeAttachDetachSpec{
		VolumeId: cnstypes.CnsVolumeId{
			Id:           spec.VolumeID.ID,
			DatastoreUrl: spec.VolumeID.DatastoreURL,
		},
		Vm: spec.VirtualMachine.Reference(),
	}
	cnsAttachSpecList = append(cnsAttachSpecList, cnsAttachSpec)
	// Call the CNS AttachVolume
	task, err := m.virtualCenter.AttachVolume(ctx, cnsAttachSpecList)
	if err != nil {
		log.WithFields(log.Fields{
			"host": m.virtualCenter.Config.Host, "err": err,
		}).Error("CNS AttachVolume failed")
		return "", err
	}
	// Get the taskInfo
	taskInfo, err := GetTaskInfo(ctx, task)
	if err != nil {
		log.WithFields(log.Fields{
			"host": m.virtualCenter.Config.Host, "err": err,
		}).Error("Failed to get taskInfo for AttachVolume task")
		return "", err
	}
	// Get the task results for the given task
	attachResults, err := GetTaskInfoResult(ctx, m.virtualCenter, taskInfo)
	if err != nil {
		log.WithFields(log.Fields{
			"host": m.virtualCenter.Config.Host, "err": err,
		}).Error("Failed to get task result for AttachVolume task")
		return "", err
	}
	for _, res := range attachResults {
		if res.Key == taskInfo.Task.Value {
			if res.Value == nil {
				log.WithFields(log.Fields{
					"host": m.virtualCenter.Config.Host, "taskID": taskInfo.Task.Value, "attachResults": attachResults,
				}).Error("nil returned for CnsVolumeAttachResult")
				break
			}
			attachRes := res.Value.(cnstypes.CnsVolumeAttachResult)
			log.WithFields(log.Fields{
				"host": m.virtualCenter.Config.Host, "taskID": taskInfo.Task.Value,
				"volumeID": attachRes.DiskUUID,
			}).Info("Successfully retrieved the attach Result")
			return attachRes.DiskUUID, nil
		}
	}
	log.WithFields(log.Fields{
		"host": m.virtualCenter.Config.Host, "taskID": taskInfo.Task.Value, "attachResults": attachResults,
	}).Error("unable to find the task result for AttachVolume task")
	return "", fmt.Errorf("Unable to find the taskresult for AttachVolume task: %s on vc: %s",
		taskInfo.Task.Value, m.virtualCenter.Config.Host)
}

// DetachVolume detaches a volume from the virtual machine given the spec.
func (m *defaultManager) DetachVolume(spec *types.AttachDetachSpec) error {
	err := validateManager(m)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Set up the VC connection
	err = m.virtualCenter.Connect(ctx)
	if err != nil {
		log.WithFields(log.Fields{
			"err": err,
		}).Error("Failed to connect to Virtual Center")
		return err
	}
	// Construct the CNS DetachSpec list
	var cnsDetachSpecList []cnstypes.CnsVolumeAttachDetachSpec
	cnsDetachSpec := cnstypes.CnsVolumeAttachDetachSpec{
		VolumeId: cnstypes.CnsVolumeId{
			Id:           spec.VolumeID.ID,
			DatastoreUrl: spec.VolumeID.DatastoreURL,
		},
		Vm: spec.VirtualMachine.Reference(),
	}
	cnsDetachSpecList = append(cnsDetachSpecList, cnsDetachSpec)
	// Call the CNS DetachVolume
	task, err := m.virtualCenter.DetachVolume(ctx, cnsDetachSpecList)
	if err != nil {
		log.WithFields(log.Fields{
			"host": m.virtualCenter.Config.Host, "err": err,
		}).Error("CNS DetachVolume failed")
		return err
	}
	// Get the taskInfo
	taskInfo, err := GetTaskInfo(ctx, task)
	if err != nil {
		log.WithFields(log.Fields{
			"host": m.virtualCenter.Config.Host, "err": err,
		}).Error("Failed to get taskInfo for DetachVolume task")
		return err
	}
	// Get the task results for the given task
	_, err = GetTaskInfoResult(ctx, m.virtualCenter, taskInfo)
	if err != nil {
		log.WithFields(log.Fields{
			"host": m.virtualCenter.Config.Host, "err": err,
		}).Error("Failed to get task result for DetachVolume task")
		return err
	}
	return nil
}

// DeleteVolume deletes a volume given its spec.
func (m *defaultManager) DeleteVolume(spec *types.DeleteSpec) error {
	err := validateManager(m)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Set up the VC connection
	err = m.virtualCenter.Connect(ctx)
	if err != nil {
		log.WithFields(log.Fields{
			"err": err,
		}).Error("Failed to connect to Virtual Center")
		return err
	}
	// Construct the CNS VolumeId list
	var cnsVolumeIDList []cnstypes.CnsVolumeId
	cnsVolumeID := cnstypes.CnsVolumeId{
		Id:           spec.VolumeID.ID,
		DatastoreUrl: spec.VolumeID.DatastoreURL,
	}
	cnsVolumeIDList = append(cnsVolumeIDList, cnsVolumeID)
	// Call the CNS DeleteVolume
	task, err := m.virtualCenter.DeleteVolume(ctx, cnsVolumeIDList)
	if err != nil {
		log.WithFields(log.Fields{
			"host": m.virtualCenter.Config.Host, "err": err,
		}).Error("CNS DeleteVolume failed")
		return err
	}
	// Get the taskInfo
	taskInfo, err := GetTaskInfo(ctx, task)
	if err != nil {
		log.WithFields(log.Fields{
			"host": m.virtualCenter.Config.Host, "err": err,
		}).Error("Failed to get taskInfo for DeleteVolume task")
		return err
	}
	// Get the task results for the given task
	_, err = GetTaskInfoResult(ctx, m.virtualCenter, taskInfo)
	if err != nil {
		log.WithFields(log.Fields{
			"host": m.virtualCenter.Config.Host, "err": err,
		}).Error("Failed to get task result for DeleteVolume task")
		return err
	}
	return nil
}

// UpdateVolume updates a volume given its spec.
func (m *defaultManager) UpdateVolume(spec *types.UpdateSpec) error {
	err := validateManager(m)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Set up the VC connection
	err = m.virtualCenter.Connect(ctx)
	if err != nil {
		log.WithFields(log.Fields{
			"err": err,
		}).Error("Failed to connect to Virtual Center")
		return err
	}
	// Construct the CNS UpdateSpec list
	var cnsUpdateSpecList []cnstypes.CnsVolumeUpdateSpec
	cnsVolumeBaseSpec := cnstypes.CnsVolumeBaseSpec{}
	cnsBackingObjectDetails := &cnstypes.CnsBackingObjectDetails{}
	if spec.Capacity != 0 {
		cnsBackingObjectDetails.CapacityInMb = int64(spec.Capacity)
	}
	cnsBlockBackingDetails := &cnstypes.CnsBlockBackingDetails{}
	cnsBlockBackingDetails.CnsBackingObjectDetails = *cnsBackingObjectDetails
	cnsVolumeBaseSpec.BackingObjectDetails = cnsBlockBackingDetails
	// Create VIM label key-value pairs
	var vimLabels []vimtypes.KeyValue
	for labelKey, labelVal := range spec.Labels {
		vimLabels = append(vimLabels, vimtypes.KeyValue{
			Key:   labelKey,
			Value: labelVal,
		})
	}
	if vimLabels != nil {
		cnsVolumeBaseSpec.Labels = vimLabels
	}
	cnsUpdateSpec := cnstypes.CnsVolumeUpdateSpec{
		VolumeId: cnstypes.CnsVolumeId{
			Id:           spec.VolumeID.ID,
			DatastoreUrl: spec.VolumeID.DatastoreURL,
		},
		CnsVolumeBaseSpec: cnsVolumeBaseSpec,
	}
	cnsUpdateSpecList = append(cnsUpdateSpecList, cnsUpdateSpec)
	task, err := m.virtualCenter.UpdateVolume(ctx, cnsUpdateSpecList)
	if err != nil {
		log.WithFields(log.Fields{
			"host": m.virtualCenter.Config.Host, "err": err,
		}).Error("CNS UpdateVolume failed")
		return err
	}
	// Get the taskInfo
	taskInfo, err := GetTaskInfo(ctx, task)
	if err != nil {
		log.WithFields(log.Fields{
			"host": m.virtualCenter.Config.Host, "err": err,
		}).Error("Failed to get taskInfo for UpdateVolume task")
		return err
	}
	// Get the task results for the given task
	_, err = GetTaskInfoResult(ctx, m.virtualCenter, taskInfo)
	if err != nil {
		log.WithFields(log.Fields{
			"host": m.virtualCenter.Config.Host, "err": err,
		}).Error("Failed to get task result for UpdateVolume task")
		return err
	}
	return nil
}

// GetVolumeInfo queries a volume given its spec.
func (m *defaultManager) GetVolumeInfo(spec *types.QuerySpec) (bool, error) {
	err := validateManager(m)
	if err != nil {
		return false, err
	}
	// TODO: Python VMODL API is not created yet.
	// Implement this method when the VMODL API is available.
	return false, nil
}

// VolumesAreAttached checks if a list disks are attached to the given node.
func (m *defaultManager) VolumesAreAttached(nodeMgr node.Manager, nodeVolumes map[string][]*types.VolumeID) (map[string]map[*types.VolumeID]bool, error) {
	err := validateManager(m)
	if err != nil {
		return nil, err
	}
	// Create context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Contains node-Volumes-IsAttached mapping
	disksAttached := make(map[string]map[*types.VolumeID]bool)
	// Return empty list if nodeVolumes is empty
	if len(nodeVolumes) == 0 {
		log.Error("Node Volumes are empty")
		return disksAttached, nil
	}
	nodesToRetry, err := volumesAreAttached(ctx, nodeMgr, nodeVolumes, disksAttached, false)
	if err != nil {
		log.WithFields(log.Fields{"node-volumes map": nodeVolumes, "err": err}).Error("failed to checks list of disks attached to the given node")
		return nil, err
	}
	if len(nodesToRetry) != 0 {
		remainingNodesVolumes := make(map[string][]*types.VolumeID)
		// Rediscover nodes which are need to be retried
		for _, nodeName := range nodesToRetry {
			err = nodeMgr.DiscoverNode(nodeName)
			if err != nil {
				if err == node.ErrNodeNotFound {
					log.WithFields(log.Fields{"node": nodeName, "err": err}).Error("Node not found")
					continue
				}
				log.WithFields(log.Fields{"node": nodeName, "err": err}).Error("Failed to rediscover node")
				return nil, err
			}
			remainingNodesVolumes[nodeName] = nodeVolumes[nodeName]
		}
		// Check if the volumes are attached for rediscovered nodes
		if len(remainingNodesVolumes) != 0 {
			log.WithFields(log.Fields{"remainingNodesVolumes": remainingNodesVolumes}).
				Info("Nodes that needs to be retried after rediscovery")
			nodesToRetry, err = volumesAreAttached(ctx, nodeMgr, remainingNodesVolumes, disksAttached, true)
			if err != nil || len(nodesToRetry) != 0 {
				log.WithFields(log.Fields{"remainingNodesVolumes": remainingNodesVolumes, "err": err}).
					Error("Failed to retry volumesAreAttached for rediscovered nodes")
				return nil, err
			}
		}
		log.WithFields(log.Fields{"nodeVolumes": nodeVolumes,
			"disksAttached": disksAttached, "err": err}).
			Info("DisksAreAttach successfully executed")
	}
	return disksAttached, nil
}
