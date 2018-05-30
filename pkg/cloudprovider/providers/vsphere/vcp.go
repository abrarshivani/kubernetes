package vsphere

import (
	"context"
	"errors"
	"github.com/golang/glog"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/kubernetes/pkg/cloudprovider"
	"k8s.io/kubernetes/pkg/controller"
)

type VCP struct {
	cfg      *VSphereConfig
	hostName string
	// Maps the VSphere IP address to VSphereInstance
	vsphereInstanceMap map[string]*VSphereInstance
	// Responsible for managing discovery of k8s node, their location etc.
	vmUUID               string
	isSecretInfoProvided bool
}

func (vs *VCP) Initialize(clientBuilder controller.ControllerClientBuilder) {
}

func (vs *VCP) Clusters() (cloudprovider.Clusters, bool) {
	return nil, true
}

// ProviderName returns the cloud provider ID.
func (vs *VCP) ProviderName() string {
	return ProviderName
}

// LoadBalancer returns an implementation of LoadBalancer for vSphere.
func (vs *VCP) LoadBalancer() (cloudprovider.LoadBalancer, bool) {
	return nil, false
}

// Zones returns an implementation of Zones for Google vSphere.
func (vs *VCP) Zones() (cloudprovider.Zones, bool) {
	glog.V(1).Info("The vSphere cloud provider does not support zones")
	return nil, false
}

// Routes returns a false since the interface is not supported for vSphere.
func (vs *VCP) Routes() (cloudprovider.Routes, bool) {
	return nil, false
}

// HasClusterID returns true if the cluster has a clusterID
func (vs *VCP) HasClusterID() bool {
	return true
}

// AddSSHKeyToAllInstances add SSH key to all instances
func (vs *VCP) AddSSHKeyToAllInstances(ctx context.Context, user string, keyData []byte) error {
	return cloudprovider.NotImplemented
}

func (vs *VCP) InstanceShutdownByProviderID(ctx context.Context, providerID string) (bool, error) {
	return false, cloudprovider.NotImplemented
}

// InstanceTypeByProviderID returns the cloudprovider instance type of the node with the specified unique providerID
// This method will not be called from the node that is requesting this ID. i.e. metadata service
// and other local methods cannot be used here
func (vs *VCP) InstanceTypeByProviderID(ctx context.Context, providerID string) (string, error) {
	return "", nil
}

func (vs *VCP) InstanceType(ctx context.Context, name k8stypes.NodeName) (string, error) {
	return "", nil
}

func GetVSphereCloud(cloud cloudprovider.Interface) (*VSphere, bool) {
	vs, ok := cloud.(*VSphere)
	return vs, ok
}

func GetCSPCloud(cloud cloudprovider.Interface) (*CSP, bool) {
	csp, ok := cloud.(*CSP)
	return csp, ok
}

func GetVCP(cloud cloudprovider.Interface) (*VCP, error) {
	var vcp *VCP
	switch cloud.(type) {
	case *VSphere:
		vcp = cloud.(*VSphere).VCP
	case *CSP:
		vcp = cloud.(*CSP).VCP
	default:
		return nil, errors.New("Invalid cloud provider: expected vSphere")
	}
	return vcp, nil
}
