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
	"errors"
	"fmt"
	"sync"

	log "github.com/sirupsen/logrus"
	"github.com/vmware/govmomi/object"
	vimtypes "github.com/vmware/govmomi/vim25/types"
	cnstypes "gitlab.eng.vmware.com/hatchway/common-csp/cns/types"
	node "gitlab.eng.vmware.com/hatchway/common-csp/pkg/node"
	"gitlab.eng.vmware.com/hatchway/common-csp/pkg/volume/types"
	"gitlab.eng.vmware.com/hatchway/common-csp/pkg/vsphere"
)

// GetTaskInfoResult gets the task result using CNS API given the task object
func GetTaskInfoResult(ctx context.Context, vc *vsphere.VirtualCenter, taskInfo *vimtypes.TaskInfo) ([]vimtypes.KeyAnyValue, error) {
	if taskInfo.State == vimtypes.TaskInfoStateError {
		log.WithFields(log.Fields{
			"host": vc.Config.Host, "fault": taskInfo.Error,
		}).Error("CNS task failed with fault")
		return nil, fmt.Errorf("CNS task failed with fault for task: %s on vc: %s",
			taskInfo.Task.Value, vc.Config.Host)
	}
	taskResults, err := vc.GetTaskResult(ctx, []string{taskInfo.Task.Value})
	if err != nil {
		log.WithFields(log.Fields{
			"host": vc.Config.Host, "taskID": taskInfo.Task.Value, "err": err,
		}).Error("Failed to get task result for task")
		return nil, err
	}
	return taskResults, nil
}

// GetTaskInfo gets the taskID given a task
func GetTaskInfo(ctx context.Context, task *object.Task) (*vimtypes.TaskInfo, error) {
	taskInfo, err := task.WaitForResult(ctx, nil)
	if err != nil {
		log.WithField("err", err).Error("Failed to complete task. Task failed on wait.")
		return nil, err
	}
	return taskInfo, nil
}

func validateManager(m *defaultManager) error {
	if m.virtualCenter == nil {
		log.Error(
			"Virtual Center connection not established")
		return errors.New("Virtual Center connection not established")
	}
	return nil
}

func getCnsBackingObjectDetails(spec *types.CreateSpec) cnstypes.BaseCnsBackingObjectDetails {
	switch spec.BackingInfo.(type) {
	// Check if backingInfo is BlockBackingInfo
	case *types.BlockBackingInfo:
		blockBackingInfo := spec.BackingInfo.(*types.BlockBackingInfo)
		cnsBackingObjectDetails := &cnstypes.CnsBackingObjectDetails{}
		if blockBackingInfo.BackingObjectInfo.StoragePolicyID != "" {
			cnsBackingObjectDetails.StoragePolicyId = blockBackingInfo.BackingObjectInfo.StoragePolicyID
		}
		if blockBackingInfo.BackingObjectInfo.Capacity != 0 {
			cnsBackingObjectDetails.CapacityInMb = int64(blockBackingInfo.BackingObjectInfo.Capacity)
		}
		cnsBlockBackingDetails := &cnstypes.CnsBlockBackingDetails{}
		cnsBlockBackingDetails.CnsBackingObjectDetails = *cnsBackingObjectDetails
		if blockBackingInfo.BackingDiskID != "" {
			cnsBlockBackingDetails.BackingDiskId = blockBackingInfo.BackingDiskID
		}
		return cnsBlockBackingDetails
	}
	return nil
}

func constructCnsCreateSpecList(spec *types.CreateSpec) cnstypes.CnsVolumeCreateSpec {
	cnsVolumeBaseSpec := cnstypes.CnsVolumeBaseSpec{}
	cnsVolumeBaseSpec.BackingObjectDetails = getCnsBackingObjectDetails(spec)
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
	cnsCreateSpec := cnstypes.CnsVolumeCreateSpec{
		Name: spec.Name,
		ContainerCluster: cnstypes.CnsContainerCluster{
			ClusterType: spec.ContainerCluster.ClusterID,
			ClusterId:   string(spec.ContainerCluster.ClusterType),
		},
		DatastoreUrls:     spec.DatastoreURLs,
		CnsVolumeBaseSpec: cnsVolumeBaseSpec,
	}
	return cnsCreateSpec
}

// volumesAreAttached checks if the volumes are attached to VM's
// This is done as follows:
// 1. Seggregate the VM's per VC
// 2. Check volumes are attached for all the VM's in a VC
// 3. If no assocations, identify if the VM is present on VC by querying VM information from VC
// 4. If not found, this VM is added to retry list and is a potential candidate for retrial on other VC's
func volumesAreAttached(ctx context.Context, nodeMgr node.Manager, nodeVolumes map[string][]*types.VolumeID, disksAttached map[string]map[*types.VolumeID]bool, retry bool) ([]string, error) {
	// vmNodeMap maps VM ManagedObjectReference value to node name
	vmNodeMap := make(map[string]string)
	// vmNodeMap maps VM ManagedObjectReference value to virtual machine object
	vmIDObjMap := make(map[string]*vsphere.VirtualMachine)
	// Segregate nodeVolumes according to VC
	vcNodes := make(map[string]map[string][]*types.VolumeID)
	for node, volumeList := range nodeVolumes {
		// Get VM instance from node name
		vm, err := nodeMgr.GetNode(node)
		if err != nil {
			log.WithField("err", err).Info("Failed to get node information")
			return nil, err
		}
		vmIDObjMap[vm.Reference().Value] = vm
		vmNodeMap[vm.Reference().Value] = node
		vmVolumesMap := vcNodes[vm.VirtualCenterHost]
		if vmVolumesMap == nil {
			vmVolumesMap = make(map[string][]*types.VolumeID)
			vcNodes[vm.VirtualCenterHost] = vmVolumesMap
		}
		vmVolumesMap[vm.Reference().Value] = volumeList
	}
	// localAttachedMaps Contains vmID-Volumes-IsAttached mapping.
	var localAttachedMaps []map[string]map[*types.VolumeID]bool
	var vmsToRetry []string
	var wg sync.WaitGroup
	var globalErr error
	globalErr = nil
	globalErrMutex := &sync.Mutex{}
	vmToRetryMutex := &sync.Mutex{}
	for vcHost, vmVolumeList := range vcNodes {
		localAttachedMap := make(map[string]map[*types.VolumeID]bool)
		localAttachedMaps = append(localAttachedMaps, localAttachedMap)
		vc, err := vsphere.GetVirtualCenterManager().GetVirtualCenter(vcHost)
		if err != nil {
			log.WithFields(log.Fields{"vcHost": vcHost, "err": err}).
				Error("Failed to get virtualcenter information from hostname")
			return nil, err
		}
		// Start go routines per VC-DC to check disks are attached
		go func() {
			err = checkVolumesAttached(ctx, vc, vmVolumeList, localAttachedMap)
			if err != nil {
				globalErrMutex.Lock()
				globalErr = err
				globalErrMutex.Unlock()
				log.WithFields(log.Fields{"vmVolumeList": vmVolumeList,
					"vcHost": vcHost, "err": err}).
					Error("Failed to check volumes attached for nodes")
			}
			if globalErr != nil {
				wg.Done()
				return
			}
			vmIDList := getVMsWithNoVolumesAttached(localAttachedMap)
			for _, vmID := range vmIDList {
				if vmObj, ok := vmIDObjMap[vmID]; ok {
					// Make a call on VM to check if VM is present on the VC
					// If not, add the VM to retry list
					_, err = vmObj.IsActive(ctx)
					if err != nil {
						if !vsphere.IsManagedObjectNotFoundError(err) && !retry {
							globalErrMutex.Lock()
							globalErr = err
							globalErrMutex.Unlock()
							log.WithFields(log.Fields{"vmID": vmID,
								"vcHost": vcHost, "err": err}).
								Error("Unable to verify the status of VM on VirtualCenter host")
						} else {
							log.WithFields(log.Fields{"vmID": vmID, "vcHost": vcHost}).
								Info("VM not found in VirtualCenter host. Adding to retry list")
							vmToRetryMutex.Lock()
							vmsToRetry = append(vmsToRetry, vmID)
							vmToRetryMutex.Unlock()
						}
					}
				}
			}
			wg.Done()
		}()
		wg.Add(1)
	}
	wg.Wait()
	if globalErr != nil {
		return nil, globalErr
	}
	for _, localAttachedMap := range localAttachedMaps {
		for vmID, volumeAttachedStatus := range localAttachedMap {
			if nodeName, ok := vmNodeMap[vmID]; ok {
				disksAttached[nodeName] = volumeAttachedStatus
			}
		}
	}
	var nodesToRetry []string
	for _, vmID := range vmsToRetry {
		if nodeName, ok := vmNodeMap[vmID]; ok {
			nodesToRetry = append(nodesToRetry, nodeName)
		}
	}
	return nodesToRetry, nil
}

// checkVolumesAttached checks if the volumes are attached to VM's in a given Virtual Center
// This is done by querying the FCD wrapper API RetrieveFCDAssociations and aggregating the results for each VM
func checkVolumesAttached(ctx context.Context, vc *vsphere.VirtualCenter, vmVolumesMap map[string][]*types.VolumeID, attached map[string]map[*types.VolumeID]bool) error {
	var datastoreURLs []string
	volumeIDMap := make(map[string]*types.VolumeID)
	// Construct datastoreURLs list by traversing through the vmVolumesList
	for vmID, volumes := range vmVolumesMap {
		for _, volume := range volumes {
			setNodeVolumeMap(attached, volume, vmID, false)
			datastoreURLs = append(datastoreURLs, volume.DatastoreURL)
			volumeIDMap[volume.ID] = volume
		}
	}
	// Get datastore refernces for above constructed datastore URL
	// using a single VC query
	datastoreURLObjMap, err := vc.GetDatastoresByURL(ctx, datastoreURLs)
	if err != nil {
		log.WithFields(log.Fields{"datastoreURL": datastoreURLs, "err": err}).
			Error("Failed to get datastores by URL")
	}
	// Populate the volumeSpec list to query FCD
	var volSpecList []*vsphere.VolumeSpec
	for _, volumes := range vmVolumesMap {
		for _, volume := range volumes {
			volSpec := &vsphere.VolumeSpec{
				ID:        volume.ID,
				Datastore: datastoreURLObjMap[volume.DatastoreURL],
			}
			volSpecList = append(volSpecList, volSpec)
		}
	}
	vmVolumeAssociations, err := vc.RetrieveFCDAssociations(ctx, volSpecList)
	if err != nil {
		log.WithFields(log.Fields{"volSpecList": volSpecList, "err": err}).
			Error("Failed to retrieve FCD associations")
		return err
	}
	for _, vmVolumeAssociation := range vmVolumeAssociations {
		for _, vmID := range vmVolumeAssociation.VMIdList {
			if _, ok1 := vmVolumesMap[vmID]; ok1 {
				if volumeID, ok2 := volumeIDMap[vmID]; ok2 {
					setNodeVolumeMap(attached, volumeID, vmVolumeAssociation.ID, true)
				}
			}
		}
	}
	return nil
}

// getVMsWithNoVolumesAttached identied VM's with no volume attached based on volumesAttached Map
func getVMsWithNoVolumesAttached(volumesAttached map[string]map[*types.VolumeID]bool) []string {
	var vmIDList []string
	for vmID, volumeAttachMap := range volumesAttached {
		isAttachedFound := false
		for _, isAttached := range volumeAttachMap {
			if isAttached {
				isAttachedFound = true
				break
			}
		}
		if !isAttachedFound {
			vmIDList = append(vmIDList, vmID)
		}
	}
	return vmIDList
}

func setNodeVolumeMap(
	nodeVolumeMap map[string]map[*types.VolumeID]bool,
	volumeID *types.VolumeID,
	nodeName string,
	check bool) {
	volumeMap := nodeVolumeMap[nodeName]
	if volumeMap == nil {
		volumeMap = make(map[*types.VolumeID]bool)
		nodeVolumeMap[nodeName] = volumeMap
	}
	volumeMap[volumeID] = check
}
