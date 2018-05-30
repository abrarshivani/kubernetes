package vsphere

import (
	"context"
	"github.com/golang/glog"
	"k8s.io/api/core/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
	"k8s.io/kubernetes/pkg/cloudprovider"
	"k8s.io/kubernetes/pkg/cloudprovider/providers/vsphere/vclib"
)

type CSP struct {
	*VCP
}

var _ cloudprovider.Interface = &CSP{}
var _ cloudprovider.Instances = &CSP{}
var _ Volumes = &CSP{}

func (csp *CSP) SetInformers(informerFactory informers.SharedInformerFactory) {
	if csp.cfg == nil {
		return
	}

	// Only on controller node it is required to register listeners.
	// Register callbacks for node updates
	glog.V(4).Infof("Setting up node informers for vSphere Cloud Provider")
	nodeInformer := informerFactory.Core().V1().Nodes().Informer()
	nodeInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    csp.NodeAdded,
		DeleteFunc: csp.NodeDeleted,
	})
	glog.V(4).Infof("Node informers in vSphere cloud provider initialized")
}

// Notification handler when node is added into k8s cluster.
func (csp *CSP) NodeAdded(obj interface{}) {
	node, ok := obj.(*v1.Node)
	if node == nil || !ok {
		glog.Warningf("NodeAdded: unrecognized object %+v", obj)
		return
	}

	glog.V(4).Infof("Node added: %+v", node)
}

// Notification handler when node is removed from k8s cluster.
func (csp *CSP) NodeDeleted(obj interface{}) {
	node, ok := obj.(*v1.Node)
	if node == nil || !ok {
		glog.Warningf("NodeDeleted: unrecognized object %+v", obj)
		return
	}

	glog.V(4).Infof("Node deleted: %+v", node)
}

// AttachDisk attaches given virtual disk volume to the compute running kubelet.
func (csp *CSP) AttachDisk(vmDiskPath string, storagePolicyName string, nodeName k8stypes.NodeName) (diskUUID string, err error) {
	return diskUUID, nil
}

func (csp *CSP) DetachDisk(volPath string, nodeName k8stypes.NodeName) error {
	return nil
}

// DiskIsAttached checks if a disk is attached to the given node.
// Assumption: If node doesn't exist, disk is not attached to the node.
func (csp *CSP) DiskIsAttached(volPath string, nodeName k8stypes.NodeName) (bool, error) {
	return false, nil
}

// DisksAreAttached checks if a list disks are attached to the given node.
// Assumption: If node doesn't exist, disks are not attached to the node.
func (csp *CSP) DisksAreAttached(nodeVolumes map[k8stypes.NodeName][]string) (map[k8stypes.NodeName]map[string]bool, error) {
	return nil, nil
}

// CreateVolume creates a new vmdk with specified parameters.
func (csp *CSP) CreateVolume(volumeOptions *vclib.VolumeOptions) (volumePath string, err error) {
	return "", nil
}

// DeleteVolume deletes vmdk.
func (csp *CSP) DeleteVolume(vmDiskPath string) error {
	return nil
}

// Instances returns an implementation of Instances for vSphere.
func (csp *CSP) Instances() (cloudprovider.Instances, bool) {
	return csp, true
}

func (csp *CSP) ExternalID(ctx context.Context, nodeName k8stypes.NodeName) (string, error) {
	return csp.InstanceID(ctx, nodeName)
}

func (csp *CSP) InstanceExistsByProviderID(ctx context.Context, providerID string) (bool, error) {
	return false, nil
}

// InstanceID returns the cloud provider ID of the node with the specified Name.
func (csp *CSP) InstanceID(ctx context.Context, nodeName k8stypes.NodeName) (string, error) {
	return "", nil
}

func (csp *CSP) NodeAddresses(ctx context.Context, nodeName k8stypes.NodeName) ([]v1.NodeAddress, error) {
	return nil, nil
}

// NodeAddressesByProviderID returns the node addresses of an instances with the specified unique providerID
// This method will not be called from the node that is requesting this ID. i.e. metadata service
// and other local methods cannot be used here
// NodeAddressesByProviderID returns the node addresses of an instances with the specified unique providerID
// This method will not be called from the node that is requesting this ID. i.e. metadata service
// and other local methods cannot be used here
func (csp *CSP) NodeAddressesByProviderID(ctx context.Context, providerID string) ([]v1.NodeAddress, error) {
	return csp.NodeAddresses(ctx, convertToK8sType(providerID))
}

// CurrentNodeName gives the current node name
func (csp *CSP) CurrentNodeName(ctx context.Context, hostname string) (k8stypes.NodeName, error) {
	return convertToK8sType(csp.hostName), nil
}
