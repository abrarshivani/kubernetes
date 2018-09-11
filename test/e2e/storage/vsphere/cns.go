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
	"sync"

	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/soap"
	vimtypes "github.com/vmware/govmomi/vim25/types"
	"gitlab.eng.vmware.com/hatchway/common-csp/cns/methods"
	cnstypes "gitlab.eng.vmware.com/hatchway/common-csp/cns/types"
	"k8s.io/kubernetes/test/e2e/framework"
)

// Namespace and Path constants
const (
	Namespace = "vsan"
	Path      = "/vsanHealth"
)

var (
	clientMutex              sync.Mutex
	CnsVolumeManagerInstance = vimtypes.ManagedObjectReference{
		Type:  "CnsVolumeManager",
		Value: "cns-volume-manager",
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
func ConnectCns(ctx context.Context, vs *VSphere) error {
	var err error
	clientMutex.Lock()
	defer clientMutex.Unlock()
	if vs.CnsClient == nil {
		vs.CnsClient, err = NewCnsClient(ctx, vs.Client.Client)
		if err != nil {
			framework.Logf("Failed to create govmomi client. err: %+v", err)
			return err
		}
	}
	return nil
}

// DisconnectCns destroys the CNS client for the virtual center.
func DisconnectCns(ctx context.Context, vs *VSphere) {
	clientMutex.Lock()
	defer clientMutex.Unlock()
	if vs.CnsClient == nil {
		framework.Logf("CnsClient wasn't connected, ignoring")
	} else {
		vs.CnsClient = nil
	}
}

// QueryVolume calls the CNS query API.
func QueryVolume(ctx context.Context, vs *VSphere, queryFilter cnstypes.CnsQueryFilter) (*cnstypes.CnsQueryResult, error) {
	req := cnstypes.CnsQueryVolume{
		This:   CnsVolumeManagerInstance,
		Filter: queryFilter,
	}
	err := ConnectCns(ctx, vs)
	if err != nil {
		return nil, err
	}
	res, err := methods.CnsQueryVolume(ctx, vs.CnsClient.Client, &req)
	if err != nil {
		return nil, err
	}
	return &res.Returnval, nil
}
