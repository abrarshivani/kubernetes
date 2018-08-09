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
	"fmt"
	"strings"

	log "github.com/sirupsen/logrus"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/vim25/methods"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
)

// Datastore holds Datastore and Datacenter information.
type Datastore struct {
	// Datastore represents the govmomi Datastore instance.
	*object.Datastore
	// Datacenter represents the datacenter on which the Datastore resides.
	Datacenter *Datacenter
}

// stripDatastorePrefix strips the Datastore prefix from the given disk path.
func (ds *Datastore) stripDatastorePrefix(diskPath string) string {
	return strings.Replace(diskPath, fmt.Sprintf("[%s] ", ds.Name()), "", -1)
}

// GetDiskPathFromUUID returns the disk path for a FCD given its ID.
func (ds *Datastore) GetDiskPathFromUUID(ctx context.Context, fcdUUID string) (string, error) {
	req := types.RetrieveVStorageObject{
		This: types.ManagedObjectReference{
			Type:  "VcenterVStorageObjectManager",
			Value: "VStorageObjectManager",
		},
		Id:        types.ID{Id: fcdUUID},
		Datastore: ds.Datastore.Reference(),
	}
	res, err := methods.RetrieveVStorageObject(ctx, ds.Client(), &req)
	if err != nil {
		log.WithFields(log.Fields{"ds": ds, "req": req, "err": err}).Error("Failed to retrieve VStorageObject")
		return "", err
	}

	log.WithFields(log.Fields{"ds": ds, "req": req, "res": res}).Info("Successfully retrieved VStorageObject")
	diskPath := res.Returnval.Config.Backing.(*types.BaseConfigInfoDiskFileBackingInfo).FilePath
	return ds.stripDatastorePrefix(diskPath), nil
}

// GetDatatoreURL returns the URL of datastore
func (ds *Datastore) GetDatatoreUrl(ctx context.Context) (string, error) {
	var dsMo mo.Datastore
	pc := property.DefaultCollector(ds.Client())
	err := pc.RetrieveOne(ctx, ds.Datastore.Reference(), []string{"summary"}, &dsMo)
	if err != nil {
		log.WithFields(log.Fields{"err": err}).Error("Failed to retrieve datastore summary property")
		return "", err
	}
	return dsMo.Summary.Url, nil
}
