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

	log "github.com/sirupsen/logrus"
	"github.com/vmware/govmomi/object"
	vimtypes "github.com/vmware/govmomi/vim25/types"
	cnstypes "gitlab.eng.vmware.com/hatchway/common-csp/cns/types"
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
