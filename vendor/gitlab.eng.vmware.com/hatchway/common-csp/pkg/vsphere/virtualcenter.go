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
	neturl "net/url"
	"strconv"
	"sync"

	log "github.com/sirupsen/logrus"
	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/pbm"
	"github.com/vmware/govmomi/session"
	"github.com/vmware/govmomi/vim25"
)

const (
	// DefaultScheme is the default connection scheme.
	DefaultScheme = "https"
	// DefaultRoundTripperCount is the default SOAP round tripper count.
	DefaultRoundTripperCount = 3
)

// VirtualCenter holds details of a virtual center instance.
type VirtualCenter struct {
	// Config represents the virtual center configuration.
	Config *VirtualCenterConfig
	// Client represents the govmomi client instance for the connection.
	Client *govmomi.Client
	// PbmClient represents the govmomi PBM Client instance.
	PbmClient       *pbm.Client
	credentialsLock sync.Mutex
}

func (vc *VirtualCenter) String() string {
	return fmt.Sprintf("VirtualCenter [Config: %v, Client: %v, PbmClient: %v]",
		vc.Config, vc.Client, vc.PbmClient)
}

// VirtualCenterConfig represents virtual center configuration.
type VirtualCenterConfig struct {
	// Scheme represents the connection scheme. (Ex: https)
	Scheme string
	// Host represents the virtual center host address.
	Host string
	// Port represents the virtual center host port.
	Port int
	// Username represents the virtual center username.
	Username string
	// Password represents the virtual center password in clear text.
	Password string
	// Insecure tells if an insecure connection is allowed.
	Insecure bool
	// RoundTripperCount is the SOAP round tripper count. (retries = RoundTripperCount - 1)
	RoundTripperCount int
	// DatacenterPaths represents paths of datacenters on the virtual center.
	DatacenterPaths []string
}

func (vcc *VirtualCenterConfig) String() string {
	return fmt.Sprintf("VirtualCenterConfig [Scheme: %v, Host: %v, Port: %v, "+
		"Username: %v, Password: %v, Insecure: %v, RoundTripperCount: %v, "+
		"DatacenterPaths: %v]", vcc.Scheme, vcc.Host, vcc.Port, vcc.Username,
		vcc.Password, vcc.Insecure, vcc.RoundTripperCount, vcc.DatacenterPaths)
}

// clientMutex is used for exclusive connection creation.
var clientMutex sync.Mutex

// newClient creates a new govmomi Client instance.
func (vc *VirtualCenter) newClient(ctx context.Context) (*govmomi.Client, error) {
	if vc.Config.Scheme == "" {
		vc.Config.Scheme = DefaultScheme
	}
	url, err := neturl.Parse(fmt.Sprintf("%s://%s:%s/sdk", vc.Config.Scheme,
		vc.Config.Host, strconv.Itoa(vc.Config.Port)))
	if err != nil {
		log.WithFields(log.Fields{"url": url, "err": err}).Error("Failed to parse URL")
		return nil, err
	}
	vc.credentialsLock.Lock()
	url.User = neturl.UserPassword(vc.Config.Username, vc.Config.Password)
	vc.credentialsLock.Unlock()

	client, err := govmomi.NewClient(ctx, url, vc.Config.Insecure)
	if err != nil {
		log.WithField("err", err).Error("Failed to create new client")
		return nil, err
	}

	if vc.Config.RoundTripperCount == 0 {
		vc.Config.RoundTripperCount = DefaultRoundTripperCount
	}
	client.RoundTripper = vim25.Retry(client.RoundTripper, vim25.TemporaryNetworkError(vc.Config.RoundTripperCount))
	return client, nil
}

// Connect establishes connection with vSphere with existing credentials if session doesn't exist.
// If credentials are invalid then it fetches latest credential from credential store and connects with it.
func (vc *VirtualCenter) Connect(ctx context.Context) error {
	err := vc.connect(ctx)
	if err == nil {
		return nil
	}
	if !IsInvalidCredentialsError(err) {
		log.Errorf("Cannot connect to vCenter with err: %v", err)
		return err
	}
	log.Infof("Invalid credentials. Cannot connect to server %q. "+
		"Fetching credentials from secrets.", vc.Config.Host)
	store, err := GetCredentialManager().GetCredentialStore()
	if err != nil {
		log.Errorf("Cannot get credential store with err: %v", err)
		return err
	}
	credential, err := store.GetCredential(vc.Config.Host)
	if err != nil {
		log.Errorf("Cannot get credentials from credential store with err: %v", err)
		return err
	}
	vc.UpdateCredentials(credential.User, credential.Password)
	return vc.Connect(ctx)
}

// connect creates a connection to the virtual center host.
func (vc *VirtualCenter) connect(ctx context.Context) error {
	clientMutex.Lock()
	defer clientMutex.Unlock()

	// If client was never initialized, initialize one.
	var err error
	if vc.Client == nil {
		if vc.Client, err = vc.newClient(ctx); err != nil {
			log.WithField("err", err).Error("Failed to create govmomi client")
			return err
		}
		return nil
	}

	// If session hasn't expired, nothing to do.
	sessionMgr := session.NewManager(vc.Client.Client)
	if userSession, err := sessionMgr.UserSession(ctx); err != nil {
		log.WithField("err", err).Error("Failed to obtain user session")
		return err
	} else if userSession != nil {
		return nil
	}

	// If session has expired, create a new instance.
	log.Warn("Creating a new client session as the existing session isn't valid or not authenticated")
	if err = vc.Disconnect(ctx); err != nil {
		log.WithField("err", err).Error("Failed to disconnect")
		return err
	}
	if vc.Client, err = vc.newClient(ctx); err != nil {
		log.WithField("err", err).Error("Failed to create govmomi client")
		return err
	}
	return nil
}

// listDatacenters returns all Datacenters.
func (vc *VirtualCenter) listDatacenters(ctx context.Context) ([]*Datacenter, error) {
	finder := find.NewFinder(vc.Client.Client, false)
	dcList, err := finder.DatacenterList(ctx, "*")
	if err != nil {
		log.WithField("err", err).Error("Failed to list datacenters")
		return nil, err
	}

	var dcs []*Datacenter
	for _, dcObj := range dcList {
		dc := &Datacenter{Datacenter: dcObj, VirtualCenterHost: vc.Config.Host}
		dcs = append(dcs, dc)
	}
	return dcs, nil
}

// getDatacenters returns Datacenter instances given their paths.
func (vc *VirtualCenter) getDatacenters(ctx context.Context, dcPaths []string) ([]*Datacenter, error) {
	finder := find.NewFinder(vc.Client.Client, false)
	var dcs []*Datacenter
	for _, dcPath := range dcPaths {
		dcObj, err := finder.Datacenter(ctx, dcPath)
		if err != nil {
			log.WithFields(log.Fields{"dcPath": dcPath, "err": err}).Error("Failed to fetch datacenter")
			return nil, err
		}
		dc := &Datacenter{Datacenter: dcObj, VirtualCenterHost: vc.Config.Host}
		dcs = append(dcs, dc)
	}
	return dcs, nil
}

// GetDatacenters returns Datacenters found on the VirtualCenter. If no
// datacenters are mentioned in the VirtualCenterConfig during registration, all
// Datacenters for the given VirtualCenter will be returned. If DatacenterPaths
// is configured in VirtualCenterConfig during registration, only the listed
// Datacenters are returned.
func (vc *VirtualCenter) GetDatacenters(ctx context.Context) ([]*Datacenter, error) {
	if len(vc.Config.DatacenterPaths) != 0 {
		return vc.getDatacenters(ctx, vc.Config.DatacenterPaths)
	}
	return vc.listDatacenters(ctx)
}

// Disconnect disconnects the virtual center host connection if connected.
func (vc *VirtualCenter) Disconnect(ctx context.Context) error {
	if vc.Client == nil {
		log.Info("Client wasn't connected, ignoring")
		return nil
	}
	if err := vc.Client.Logout(ctx); err != nil {
		log.WithField("err", err).Error("Failed to logout")
		return err
	}
	vc.Client = nil
	return nil
}

func (vc *VirtualCenter) UpdateCredentials(username, password string) {
	vc.credentialsLock.Lock()
	defer vc.credentialsLock.Unlock()
	vc.Config.Username = username
	vc.Config.Password = password
}
