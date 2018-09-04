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
	"fmt"
	"sync"

	log "github.com/sirupsen/logrus"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/property"
	"github.com/vmware/govmomi/vim25/mo"
	"github.com/vmware/govmomi/vim25/types"
)

// ErrVMNotFound is returned when a virtual machine isn't found.
var ErrVMNotFound = errors.New("virtual machine wasn't found")

// VirtualMachine holds details of a virtual machine instance.
type VirtualMachine struct {
	// VirtualCenterHost represents the virtual machine's vCenter host.
	VirtualCenterHost string
	// UUID represents the virtual machine's UUID.
	UUID string
	// VirtualMachine represents the virtual machine.
	*object.VirtualMachine
	// Datacenter represents the datacenter to which the virtual machine belongs.
	Datacenter *Datacenter
}

func (vm *VirtualMachine) String() string {
	return fmt.Sprintf("%v [VirtualCenterHost: %v, UUID: %v, Datacenter: %v]",
		vm.VirtualMachine, vm.VirtualCenterHost, vm.UUID, vm.Datacenter)
}

func (vm *VirtualMachine) IsActive(ctx context.Context) (bool, error) {
	vmMoList, err := vm.Datacenter.GetVMMoList(ctx, []*VirtualMachine{vm}, []string{"summary"})
	if err != nil {
		log.WithField("err", err).Errorf("Failed to get VM Managed object with property summary. err: +%v", err)
		return false, err
	}
	if vmMoList[0].Summary.Runtime.PowerState == types.VirtualMachinePowerStatePoweredOn {
		return true, nil
	}
	return false, nil
}

// renew renews the virtual machine and datacenter objects given its virtual center.
func (vm *VirtualMachine) renew(vc *VirtualCenter) {
	vm.VirtualMachine = object.NewVirtualMachine(vc.Client.Client, vm.VirtualMachine.Reference())
	vm.Datacenter.Datacenter = object.NewDatacenter(vc.Client.Client, vm.Datacenter.Reference())
}

// GetAllAccessibleDatastores gets the list of accessible Datastores for the given Virtual Machine
func (vm *VirtualMachine) GetAllAccessibleDatastores(ctx context.Context) ([]*DatastoreInfo, error) {
	host, err := vm.HostSystem(ctx)
	if err != nil {
		log.WithFields(log.Fields{"vm": vm.InventoryPath, "err": err}).Error("Failed to get host system for VM")
		return nil, err
	}
	var hostSystemMo mo.HostSystem
	s := object.NewSearchIndex(vm.Client())
	err = s.Properties(ctx, host.Reference(), []string{"datastore"}, &hostSystemMo)
	if err != nil {
		log.WithFields(log.Fields{"host": host, "err": err}).Error("Failed to retrieve datastores for host")
		return nil, err
	}
	var dsRefList []types.ManagedObjectReference
	for _, dsRef := range hostSystemMo.Datastore {
		dsRefList = append(dsRefList, dsRef)
	}

	var dsMoList []mo.Datastore
	pc := property.DefaultCollector(vm.Client())
	properties := []string{"info"}
	err = pc.Retrieve(ctx, dsRefList, properties, &dsMoList)
	if err != nil {
		log.WithFields(log.Fields{"dsObjList": dsRefList, "properties": properties, "err": err}).Error("Failed to get Datastore managed objects from datastore objects")
		return nil, err
	}
	var dsObjList []*DatastoreInfo
	for _, dsMo := range dsMoList {
		dsObjList = append(dsObjList,
			&DatastoreInfo{
				&Datastore{object.NewDatastore(vm.Client(), dsMo.Reference()),
					vm.Datacenter},
				dsMo.Info.GetDatastoreInfo()})
	}
	return dsObjList, nil
}

// Renew renews the virtual machine and datacenter information. If reconnect is
// set to true, the virtual center connection is also renewed.
func (vm *VirtualMachine) Renew(reconnect bool) error {
	vc, err := GetVirtualCenterManager().GetVirtualCenter(vm.VirtualCenterHost)
	if err != nil {
		log.WithFields(log.Fields{"vm": vm, "err": err}).Error("Failed to get VC while renewing VM")
		return err
	}

	if reconnect {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if err := vc.Connect(ctx); err != nil {
			log.WithFields(log.Fields{"vm": vm, "vc": vc, "err": err}).
				Error("Failed reconnecting to VC while renewing VM")
			return err
		}
	}

	vm.renew(vc)
	return nil
}

const (
	// poolSize is the number of goroutines to run while trying to find a
	// virtual machine.
	poolSize = 8
	// dcBufferSize is the buffer size for the channel that is used to
	// asynchronously receive *Datacenter instances.
	dcBufferSize = poolSize * 10
)

// GetVirtualMachineByUUID returns VirtualMachine for a virtual machine given its UUID.
func GetVirtualMachineByUUID(uuid string) (*VirtualMachine, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	log.WithField("uuid", uuid).Info("Initiating asynchronous datacenter listing")
	dcsChan, errChan := AsyncGetAllDatacenters(ctx, dcBufferSize)

	var wg sync.WaitGroup
	var vm *VirtualMachine
	var poolErr error

	for i := 0; i < poolSize; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case err, ok := <-errChan:
					if !ok {
						// Async function finished.
						log.WithField("uuid", uuid).Info("AsyncGetAllDatacenters finished")
						return
					} else if err == context.Canceled {
						// Canceled by another instance of this goroutine.
						log.WithField("uuid", uuid).Info("AsyncGetAllDatacenters ctx was canceled")
						return
					} else {
						// Some error occurred.
						log.WithFields(log.Fields{"uuid": uuid, "err": err}).
							Error("AsyncGetAllDatacenters sent an error")
						poolErr = err
						return
					}

				case dc, ok := <-dcsChan:
					if !ok {
						// Async function finished.
						log.WithField("uuid", uuid).Info("AsyncGetAllDatacenters finished")
						return
					}

					// Found some Datacenter object.
					log.WithFields(log.Fields{"uuid": uuid, "dc": dc}).
						Info("AsyncGetAllDatacenters sent a dc")

					var err error
					if vm, err = dc.GetVirtualMachineByUUID(context.Background(), uuid); err != nil {
						if err == ErrVMNotFound {
							// Didn't find VM on this DC, so, continue searching on other DCs.
							log.WithFields(log.Fields{"uuid": uuid, "dc": dc, "err": err}).
								Info("Couldn't find VM on DC, continuing search")
							continue
						} else {
							// Some serious error occurred, so stop the async function.
							log.WithFields(log.Fields{"uuid": uuid, "dc": dc, "err": err}).
								Error("Failed finding VM on DC, canceling context")
							cancel()
							poolErr = err
							return
						}
					} else {
						// Virtual machine was found, so stop the async function.
						log.WithFields(log.Fields{"uuid": uuid, "dc": dc, "vm": vm}).
							Info("Found VM on DC, canceling context")
						cancel()
						return
					}
				}
			}
		}()
	}
	wg.Wait()

	if vm != nil {
		log.WithFields(log.Fields{"uuid": uuid, "vm": vm}).Info("Returning VM for UUID")
		return vm, nil
	} else if poolErr != nil {
		log.WithFields(log.Fields{"uuid": uuid, "poolErr": poolErr}).Error("Returning err for UUID")
		return nil, poolErr
	} else {
		log.WithField("uuid", uuid).Error("Returning VM not found err for UUID")
		return nil, ErrVMNotFound
	}
}
