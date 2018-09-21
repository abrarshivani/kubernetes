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
	"github.com/vmware/govmomi/vim25/methods"
	"github.com/vmware/govmomi/vim25/types"
)

// VolumeSpec uniquely identifies a FCD volume.
type VolumeSpec struct {
	// ID represents the ID of the volume.
	ID string
	// Datastore is the datastore path where the volume resides.
	Datastore *Datastore
}

// VMVolumeAssociation represents VM volume associations
type VMVolumeAssociation struct {
	// ID represents the ID of the volume.
	ID string
	// VMId represents the list of the virtual machine ID's.
	VMIdList []string
}

// RetrieveFCDAssociations retrieves the VM associations for each FCD object in query.
func (vc *VirtualCenter) RetrieveFCDAssociations(ctx context.Context, volSpecList []*VolumeSpec) ([]*VMVolumeAssociation, error) {
	var vStorageObjectSpecList []types.RetrieveVStorageObjSpec
	for _, volSpec := range volSpecList {
		vStorageObjectSpec := types.RetrieveVStorageObjSpec{
			Id: types.ID{
				Id: volSpec.ID,
			},
			Datastore: volSpec.Datastore.Reference(),
		}
		vStorageObjectSpecList = append(vStorageObjectSpecList, vStorageObjectSpec)
	}
	err := vc.Connect(ctx)
	if err != nil {
		log.WithFields(log.Fields{
			"err": err, "vcenter": vc.Config.Host,
		}).Error("Failed to connect to Virtual Center")
		return nil, err
	}
	req := types.RetrieveVStorageObjectAssociations{
		This: *vc.Client.Client.ServiceContent.VStorageObjectManager,
		Ids:  vStorageObjectSpecList,
	}
	res, err := methods.RetrieveVStorageObjectAssociations(ctx, vc.Client.Client, &req)
	if err != nil {
		log.WithFields(log.Fields{
			"err": err, "vcenter": vc.Config.Host,
		}).Error("RetrieveVStorageObjectAssociations call failed")
		return nil, err
	}
	var vmVolumeAssociations []*VMVolumeAssociation
	for _, volAssocation := range res.Returnval {
		vmVolumeAssociation := VMVolumeAssociation{
			ID: volAssocation.Id.Id,
		}
		for _, vmAssociation := range volAssocation.VmDiskAssociations {
			vmVolumeAssociation.VMIdList = append(vmVolumeAssociation.VMIdList, vmAssociation.VmId)
		}
		vmVolumeAssociations = append(vmVolumeAssociations, &vmVolumeAssociation)
	}
	return vmVolumeAssociations, nil
}
