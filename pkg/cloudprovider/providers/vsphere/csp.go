package vsphere

import (
	"github.com/golang/glog"
	"k8s.io/api/core/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/informers"
	"k8s.io/kubernetes/pkg/cloudprovider"
	"golang.org/x/net/context"
	"k8s.io/kubernetes/pkg/cloudprovider/providers/vsphere/vclib"
	corelisters "k8s.io/client-go/listers/core/v1"
	"fmt"
	"reflect"
)

type CSP struct {
	*VSphere
	pvLister corelisters.PersistentVolumeLister
}

var _ cloudprovider.Interface = &CSP{}
var _ cloudprovider.Instances = &CSP{}
var _ Volumes = &CSP{}
var _ K8sEvents = &CSP{}
var _ PVCEvents = &CSP{}
var _ PVEvents = &CSP{}
var _ NodeEvents = &CSP{}

func (vs *CSP) SetInformers(informerFactory informers.SharedInformerFactory) {
	if vs.cfg == nil {
		return
	}
	SetInformers(vs, informerFactory)
}

func (vs *CSP) NodeEvents() (NodeEvents, bool) {
	return vs, true
}

func (vs *CSP) PVCEvents() (PVCEvents, bool) {
	return vs, true
}

func (vs *CSP) PVEvents() (PVEvents, bool) {
	return vs, true
}

func (vs *CSP) StorePVLister(pvLister corelisters.PersistentVolumeLister) {
	vs.pvLister = pvLister
}

func (vs *CSP) PVCUpdated(oldObj, newObj interface{}) {
	oldPvc, ok := oldObj.(*v1.PersistentVolumeClaim)

	if oldPvc == nil || !ok {
		return
	}

	newPvc, ok := newObj.(*v1.PersistentVolumeClaim)

	if newPvc == nil || !ok {
		return
	}

	pv, err := getPersistentVolume(newPvc, vs.pvLister)
	if err != nil {
		glog.V(5).Infof("Error getting Persistent Volume for pvc %q : %v", newPvc.UID, err)
		return
	}

	if pv.Spec.PersistentVolumeSource.VsphereVolume == nil {
		return
	}

	newLabels := newPvc.GetLabels()
	oldLabels := oldPvc.GetLabels()
	labelsUpdated := reflect.DeepEqual(newLabels, oldLabels)

	if labelsUpdated {
		// Call update on cns
		glog.V(1).Infof("Labels Updated to %#v", newLabels)
	}
}

// Notification handler when node is added into k8s cluster.
func (vs *CSP) NodeAdded(obj interface{}) {
	node, ok := obj.(*v1.Node)
	if node == nil || !ok {
		glog.Warningf("NodeAdded: unrecognized object %+v", obj)
		return
	}

	glog.V(4).Infof("Node added: %+v", node)
}

// Notification handler when node is removed from k8s cluster.
func (vs *CSP) NodeDeleted(obj interface{}) {
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
func (vs *CSP) Instances() (cloudprovider.Instances, bool) {
	return vs, true
}

func (vs *CSP) ExternalID(ctx context.Context, nodeName k8stypes.NodeName) (string, error) {
	return vs.InstanceID(ctx, nodeName)
}

func (vs *CSP) InstanceExistsByProviderID(ctx context.Context, providerID string) (bool, error) {
	return false, nil
}

// InstanceID returns the cloud provider ID of the node with the specified Name.
func (vs *CSP) InstanceID(ctx context.Context, nodeName k8stypes.NodeName) (string, error) {
	return "", nil
}

func (vs *CSP) NodeAddresses(ctx context.Context, nodeName k8stypes.NodeName) ([]v1.NodeAddress, error) {
	return nil, nil
}

// NodeAddressesByProviderID returns the node addresses of an instances with the specified unique providerID
// This method will not be called from the node that is requesting this ID. i.e. metadata service
// and other local methods cannot be used here
// NodeAddressesByProviderID returns the node addresses of an instances with the specified unique providerID
// This method will not be called from the node that is requesting this ID. i.e. metadata service
// and other local methods cannot be used here
func (vs *CSP) NodeAddressesByProviderID(ctx context.Context, providerID string) ([]v1.NodeAddress, error) {
	return vs.NodeAddresses(ctx, convertToK8sType(providerID))
}

func (vs *CSP) CheckVolumeCompliance(pvNodeMap map[k8stypes.NodeName][]*v1.PersistentVolume) error {
	for nodeName, volumes := range pvNodeMap {
		for _, pv := range volumes {
			policyName := pv.Spec.VsphereVolume.StoragePolicyName
			if policyName == "" {
				continue
			}
			volumePath := pv.Spec.VsphereVolume.VolumePath
			msg := fmt.Sprintf("Compliance change for volume %s with policy %s attached to node %s", nodeName, policyName, volumePath)
			//Check Complilance
			glog.V(4).Info(msg)
			vs.eventRecorder.Event(pv.Spec.ClaimRef, v1.EventTypeWarning, ComplianceChange, msg)
		}
	}
	return nil
}
