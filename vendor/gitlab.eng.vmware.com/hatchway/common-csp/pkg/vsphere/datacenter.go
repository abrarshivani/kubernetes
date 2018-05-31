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
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
)

// Datacenter holds virtual center information along with the Datacenter.
type Datacenter struct {
	// Datacenter represents the govmomi Datacenter.
	*object.Datacenter
	// VirtualCenterHost represents the virtual center host address.
	VirtualCenterHost string
}

func (dc *Datacenter) String() string {
	return fmt.Sprintf("Datacenter [Datacenter: %v, VirtualCenterHost: %v]",
		dc.Datacenter, dc.VirtualCenterHost)
}

// GetDatastoreByName returns the *Datastore instance given its name.
func (dc *Datacenter) GetDatastoreByName(ctx context.Context, name string) (*Datastore, error) {
	finder := find.NewFinder(dc.Datacenter.Client(), false)
	finder.SetDatacenter(dc.Datacenter)
	ds, err := finder.Datastore(ctx, name)
	if err != nil {
		log.WithFields(log.Fields{"name": name, "err": err}).Error("Couldn't find Datastore")
		return nil, err
	}
	return &Datastore{Datastore: ds, Datacenter: dc}, nil
}

// GetVirtualMachineByUUID returns the *VirtualMachine instance given its UUID.
func (dc *Datacenter) GetVirtualMachineByUUID(ctx context.Context, uuid string) (*VirtualMachine, error) {
	uuid = strings.ToLower(strings.TrimSpace(uuid))
	searchIndex := object.NewSearchIndex(dc.Datacenter.Client())
	svm, err := searchIndex.FindByUuid(ctx, dc.Datacenter, uuid, true, nil)
	if err != nil {
		log.WithFields(log.Fields{"uuid": uuid, "err": err}).Error("Failed to find VM")
		return nil, err
	} else if svm == nil {
		log.WithField("uuid", uuid).Error("Couldn't find VM")
		return nil, ErrVMNotFound
	}
	vm := &VirtualMachine{
		VirtualCenterHost: dc.VirtualCenterHost,
		UUID:              uuid,
		VirtualMachine:    object.NewVirtualMachine(dc.Datacenter.Client(), svm.Reference()),
		Datacenter:        dc,
	}
	return vm, nil
}

// asyncGetAllDatacenters returns *Datacenter instances over the given
// channel. If an error occurs, it will be returned via the given error channel.
// If the given context is canceled, the processing will be stopped as soon as
// possible, and the channels will be closed before returning.
func asyncGetAllDatacenters(ctx context.Context, dcsChan chan<- *Datacenter, errChan chan<- error) {
	defer close(dcsChan)
	defer close(errChan)

	for _, vc := range GetVirtualCenterManager().GetAllVirtualCenters() {
		// If the context was canceled, we stop looking for more Datacenters.
		select {
		case <-ctx.Done():
			err := ctx.Err()
			log.WithField("err", err).Info("Context was done, returning")
			errChan <- err
			return
		default:
		}

		if err := vc.Connect(ctx); err != nil {
			log.WithFields(log.Fields{"vc": vc, "err": err}).Error("Failed connecting to VC")
			errChan <- err
			return
		}

		dcs, err := vc.GetDatacenters(ctx)
		if err != nil {
			log.WithFields(log.Fields{"vc": vc, "err": err}).Error("Failed to fetch datacenters")
			errChan <- err
			return
		}

		for _, dc := range dcs {
			// If the context was canceled, we don't return more Datacenters.
			select {
			case <-ctx.Done():
				err := ctx.Err()
				log.WithField("err", err).Info("Context was done, returning")
				errChan <- err
				return
			default:
				log.WithField("dc", dc).Info("Publishing datacenter")
				dcsChan <- dc
			}
		}
	}
}

// AsyncGetAllDatacenters fetches all Datacenters asynchronously. The
// *Datacenter chan returns a *Datacenter on discovering one. The
// error chan returns a single error if one occurs. Both channels are closed
// when nothing more is to be sent.
//
// The buffer size for the *Datacenter chan can be specified via the
// buffSize parameter. For example, buffSize could be 1, in which case, the
// sender will buffer at most 1 *Datacenter instance (and possibly close
// the channel and terminate, if that was the only instance found).
//
// Note that a context.Canceled error would be returned if the context was
// canceled at some point during the execution of this function.
func AsyncGetAllDatacenters(ctx context.Context, buffSize int) (<-chan *Datacenter, <-chan error) {
	dcsChan := make(chan *Datacenter, buffSize)
	errChan := make(chan error, 1)
	go asyncGetAllDatacenters(ctx, dcsChan, errChan)
	return dcsChan, errChan
}

// GetVMMoList gets the VM Managed Objects with the given properties from the VM object
func (dc *Datacenter) GetVMMoList(ctx context.Context, vmObjList []*VirtualMachine, properties []string) ([]mo.VirtualMachine, error) {
	var vmMoList []mo.VirtualMachine
	var vmRefs []types.ManagedObjectReference
	if len(vmObjList) < 1 {
		msg := fmt.Sprintf("VirtualMachine Object list is empty")
		log.WithField("vmObjlist", vmObjList).Error(msg)
		return nil, fmt.Errorf(msg)
	}

	for _, vmObj := range vmObjList {
		vmRefs = append(vmRefs, vmObj.Reference())
	}
	pc := property.DefaultCollector(dc.Client())
	err := pc.Retrieve(ctx, vmRefs, properties, &vmMoList)
	if err != nil {
		log.WithField("vmObjlist", vmObjList).Errorf("Failed to get VM managed objects from VM objects. vmObjList: %+v, properties: %+v, err: %v", vmObjList, properties, err)
		return nil, err
	}
	return vmMoList, nil
}
