package vsphere

import (
	"context"
	"fmt"
	"github.com/golang/glog"
	nodemanager "gitlab.eng.vmware.com/hatchway/common-csp/pkg/node"
	cspvolumes "gitlab.eng.vmware.com/hatchway/common-csp/pkg/volume"
	cspvolumestypes "gitlab.eng.vmware.com/hatchway/common-csp/pkg/volume/types"
	cspvsphere "gitlab.eng.vmware.com/hatchway/common-csp/pkg/vsphere"
	"k8s.io/api/core/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
	v1helper "k8s.io/kubernetes/pkg/apis/core/v1/helper"
	"k8s.io/kubernetes/pkg/cloudprovider"
	"k8s.io/kubernetes/pkg/cloudprovider/providers/vsphere/vclib"
	corelisters "k8s.io/client-go/listers/core/v1"
	"net"
	"strconv"
	"strings"
	"reflect"
)


var (
	PrefixPVCLabel = "vmware#cns#pvc"
)

type CSP struct {
	*VCP
	virtualCenterManager cspvsphere.VirtualCenterManager
	nodeManager          nodemanager.Manager
	volumeManager        cspvolumes.Manager
	PVLister			 corelisters.PersistentVolumeLister
}

var _ cloudprovider.Interface = &CSP{}
var _ cloudprovider.Instances = &CSP{}

func (csp *CSP) SetInformers(informerFactory informers.SharedInformerFactory) {
	if csp.cfg == nil {
		return
	}

	if csp.isSecretInfoProvided {
		secretCredentialManager := &SecretCredentialManager{
			SecretName:      csp.cfg.Global.SecretName,
			SecretNamespace: csp.cfg.Global.SecretNamespace,
			SecretLister:    informerFactory.Core().V1().Secrets().Lister(),
			Cache: &SecretCache{
				VirtualCenter: make(map[string]*Credential),
			},
		}
		cspSecretCredentialManager := &CSPSecretCredentialManager{SecretCredentialManager: secretCredentialManager}
		cspvsphere.GetCredentialManager().SetCredentialStore(cspSecretCredentialManager)
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

	pvcInformer := informerFactory.Core().V1().PersistentVolumeClaims().Informer()
	pvcInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		UpdateFunc: csp.PVCUpdated,
		DeleteFunc: csp.PVCDeleted,
	})
	glog.V(4).Infof("PVC informers in vSphere cloud provider initialized")

	csp.PVLister = informerFactory.Core().V1().PersistentVolumes().Lister()
	glog.V(4).Infof("PVC informers in vSphere cloud provider initialized")

}

// Notification handler when node is added into k8s cluster.
func (csp *CSP) NodeAdded(obj interface{}) {
	node, ok := obj.(*v1.Node)
	if node == nil || !ok {
		glog.Warningf("NodeAdded: unrecognized object %+v", obj)
		return
	}

	glog.V(4).Infof("Node added: %+v", node)
	csp.nodeManager.RegisterNode(node.Name, nil)
}

// Notification handler when node is removed from k8s cluster.
func (csp *CSP) NodeDeleted(obj interface{}) {
	node, ok := obj.(*v1.Node)
	if node == nil || !ok {
		glog.Warningf("NodeDeleted: unrecognized object %+v", obj)
		return
	}

	glog.V(4).Infof("Node deleted: %+v", node)
	csp.nodeManager.UnregisterNode(node.Name)
}

func (csp *CSP) PVCUpdated(oldObj, newObj interface{}) {
	oldPvc, ok := oldObj.(*v1.PersistentVolumeClaim)

	if oldPvc == nil || !ok {
		return
	}

	newPvc, ok := newObj.(*v1.PersistentVolumeClaim)

	if newPvc == nil || !ok {
		return
	}

	if newPvc.Status.Phase != v1.ClaimBound {
		return
	}

	pv, err := getPersistentVolume(newPvc, csp.PVLister)
	if err != nil {
		glog.Errorf("Error getting Persistent Volume for pvc %q : %v", newPvc.UID, err)
		return
	}

	if pv.Spec.PersistentVolumeSource.VsphereVolume == nil {
		return
	}

	newLabels := newPvc.GetLabels()
	oldLabels := oldPvc.GetLabels()
	labelsEqual := reflect.DeepEqual(newLabels, oldLabels)

	if !labelsEqual {
		glog.V(4).Infof("Updating %#v labels to %#v in cns for volume %s", newLabels, pv.Spec.PersistentVolumeSource.VsphereVolume.VolumePath)
		vc, err := csp.virtualCenterManager.GetVirtualCenter(csp.cfg.Workspace.VCenterIP)
		if err != nil {
			glog.Errorf("Cannot get virtual center object for server %s with error %+v", csp.cfg.Workspace.VCenterIP, err)
			return
		}
		prefixedLabels := AddPrefixToLabels(PrefixPVCLabel, newLabels)
		volID, datastoreURL := GetVolumeIDAndDatastoreURL(pv.Spec.PersistentVolumeSource.VsphereVolume.VolumePath)
		updateSpec := &cspvolumestypes.UpdateSpec{
			VolumeID: &cspvolumestypes.VolumeID{
				ID: volID,
				DatastoreURL: datastoreURL,
			},
			Labels: prefixedLabels,
		}
		cspvolumes.GetManager(vc).UpdateVolume(updateSpec)
	}
}

func (csp *CSP) PVCDeleted(obj interface{}) {
	pvc, ok := obj.(*v1.PersistentVolumeClaim)
	if pvc == nil || !ok {
		glog.Warningf("PVCDeleted: unrecognized object %+v", obj)
		return
	}
	glog.V(4).Infof("PVC deleted: %+v", pvc)
	pv, err := getPersistentVolume(pvc, csp.PVLister)
	if err != nil {
		glog.Errorf("Error getting Persistent Volume for pvc %q : %v", pvc.UID, err)
		return
	}
	if pv.Spec.PersistentVolumeSource.VsphereVolume == nil {
		return
	}
	// If the PV is retain we need to delete PVC labels
	volID, datastoreURL := GetVolumeIDAndDatastoreURL(pv.Spec.PersistentVolumeSource.VsphereVolume.VolumePath)
	updateSpec := &cspvolumestypes.UpdateSpec{
		VolumeID: &cspvolumestypes.VolumeID{
			ID: volID,
			DatastoreURL: datastoreURL,
		},
	}
	vc, err := csp.virtualCenterManager.GetVirtualCenter(csp.cfg.Workspace.VCenterIP)
	if err != nil {
		glog.Errorf("Cannot get virtual center object for server %s with error %+v", csp.cfg.Workspace.VCenterIP, err)
		return
	}
	cspvolumes.GetManager(vc).UpdateVolume(updateSpec)
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
	instanceIDInternal := func() (string, error) {
		if csp.vmUUID == convertToString(nodeName) {
			return csp.vmUUID, nil
		}

		// Below logic can be performed only on master node where VC details are preset.
		if csp.cfg == nil {
			return "", fmt.Errorf("The current node can't detremine InstanceID for %q", convertToString(nodeName))
		}

		// Create context
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		vm, err := csp.nodeManager.GetNode(convertToString(nodeName))
		if err != nil {
			if err == cspvsphere.ErrVMNotFound {
				return "", cloudprovider.InstanceNotFound
			}
			glog.Errorf("Failed to get VM object for node: %q. err: +%v", convertToString(nodeName), err)
			return "", err
		}

		isActive, err := vm.IsActive(ctx)
		if err != nil {
			glog.Errorf("Failed to check whether node %q is active. err: %+v.", convertToString(nodeName), err)
			return "", err
		}
		if isActive {
			return csp.vmUUID, nil
		}
		glog.Warningf("The VM: %s is not in %s state", convertToString(nodeName), vclib.ActivePowerState)
		return "", cloudprovider.InstanceNotFound
	}
	instanceID, err := instanceIDInternal()
	return instanceID, err
}

func (csp *CSP) NodeAddresses(ctx context.Context, nodeName k8stypes.NodeName) ([]v1.NodeAddress, error) {
	// Get local IP addresses if node is local node
	if csp.vmUUID == convertToString(nodeName) {
		return getLocalIP()
	}

	if csp.cfg == nil {
		return nil, cloudprovider.InstanceNotFound
	}

	vm, err := csp.nodeManager.GetNode(convertToString(nodeName))
	if err != nil {
		glog.Errorf("Failed to get VM object for node: %q. err: +%v", convertToString(nodeName), err)
		return nil, err
	}

	vmMoList, err := vm.Datacenter.GetVMMoList(ctx, []*cspvsphere.VirtualMachine{vm}, []string{"guest.net"})
	if err != nil {
		glog.Errorf("Failed to get VM Managed object with property guest.net for node: %q. err: +%v", convertToString(nodeName), err)
		return nil, err
	}

	// Below logic can be executed only on master as VC details are present.
	addrs := []v1.NodeAddress{}

	// retrieve VM's ip(s)
	for _, v := range vmMoList[0].Guest.Net {
		if csp.cfg.Network.PublicNetwork == v.Network {
			for _, ip := range v.IpAddress {
				if net.ParseIP(ip).To4() != nil {
					v1helper.AddToNodeAddresses(&addrs,
						v1.NodeAddress{
							Type:    v1.NodeExternalIP,
							Address: ip,
						}, v1.NodeAddress{
							Type:    v1.NodeInternalIP,
							Address: ip,
						},
					)
				}
			}
		}
	}
	return addrs, nil
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
	return convertToK8sType(csp.vmUUID), nil
}

func RegisterVirtualCenters(vsphereInstanceMap map[string]*VSphereInstance,
	virtualCenterManager cspvsphere.VirtualCenterManager) error {
	for _, vsi := range vsphereInstanceMap {
		datacenters := strings.Split(vsi.cfg.Datacenters, ",")
		for dci, dc := range datacenters {
			dc = strings.TrimSpace(dc)
			datacenters[dci] = dc
		}
		port, err := strconv.Atoi(vsi.conn.Port)
		if err != nil {
			return err
		}
		roundTripCount := int(vsi.conn.RoundTripperCount)

		virtualCenterManager.RegisterVirtualCenter(&cspvsphere.VirtualCenterConfig{
			Host:              vsi.conn.Hostname,
			Port:              port,
			Username:          vsi.conn.Username,
			Password:          vsi.conn.Password,
			RoundTripperCount: roundTripCount,
			DatacenterPaths:   datacenters,
			Insecure:          vsi.conn.Insecure,
		})
	}
	return nil
}

var _ CommonVolumes = &CSP{}

// CreateVolume creates a new volume given its spec.
func (csp *CSP) CreateVSphereVolume(spec *CreateVolumeSpec) (VolumeID, error) {
	glog.V(3).Infof("vSphere Cloud Provider creating disk %+v", spec)
	vc, err := csp.virtualCenterManager.GetVirtualCenter(csp.cfg.Workspace.VCenterIP)
	if err != nil {
		glog.Errorf("Cannot get virtual center %s from vitualcentermanager for creating disk %+v with error %+v",
			csp.cfg.Workspace.VCenterIP, spec.Name, err)
		return VolumeID{}, err
	}

	// TODO: Add labels and compute storagepolicyID from storagepolicyName
	createSpec := &cspvolumestypes.CreateSpec{
		Name:          spec.Name,
		DatastoreURLs: []string{csp.cfg.Workspace.DefaultDatastore},
		BackingInfo: &cspvolumestypes.BackingObjectInfo{
			StoragePolicyID: spec.StoragePolicyID,
			Capacity:        uint64(spec.CapacityKB),
		},
		ContainerCluster: cspvolumestypes.ContainerCluster{
			ClusterID:   csp.cfg.Global.ClusterID,
			ClusterType: cspvolumestypes.ClusterTypeKUBERNETES,
		},
	}
	glog.V(5).Infof("vSphere Cloud Provider creating volume %s with create spec %+v", spec.Name, createSpec)
	volumeID, err := cspvolumes.GetManager(vc).CreateVolume(createSpec)
	if err != nil {
		glog.Errorf("Failed to create disk %s with error %+v", spec.Name, err)
		return VolumeID{}, err
	}
	// TODO: Return VolumeID
	volPath := GetVolPathFromVolumeID(volumeID)
	return VolumeID{ID: volPath}, nil
}

// AttachVolume attaches a volume to a virtual machine given the spec.
func (csp *CSP) AttachVSphereVolume(spec *AttachVolumeSpec) (diskUUID string, err error) {
	nodeName := spec.NodeName
	volID := spec.VolID
	glog.V(3).Infof("vSphere Cloud Provider attaching disk %+v on node %s", volID, nodeName)
	node, err := csp.nodeManager.GetNode(convertToString(nodeName))
	if err != nil {
		glog.Errorf("Cannot get node %s from nodemanager for attaching disk %+v with error %+v",
			nodeName, volID, err)
		return "", err
	}
	vc, err := csp.virtualCenterManager.GetVirtualCenter(node.VirtualCenterHost)
	if err != nil {
		glog.Errorf("Cannot get virtual center %s from vitualcentermanager for attaching disk %+v with error %+v", node.VirtualCenterHost, volID.ID, err)
		return "", err
	}
	volumeID, datastoreURL := GetVolumeIDAndDatastoreURL(volID.ID)
	attachSpec := &cspvolumestypes.AttachDetachSpec{
		VolumeID: &cspvolumestypes.VolumeID{
			ID:           volumeID,
			DatastoreURL: datastoreURL,
		},
		VirtualMachine: node,
	}
	glog.V(5).Infof("vSphere Cloud Provider attaching volume %s with attach spec %+v", volID.ID, attachSpec)
	diskUUID, err = cspvolumes.GetManager(vc).AttachVolume(attachSpec)
	if err != nil {
		glog.Errorf("Failed to attach disk %s with err %+v", volumeID, err)
		return "", err
	}
	return diskUUID, nil
}

// DetachVolume detaches a volume from the virtual machine given the spec.
func (csp *CSP) DetachVSphereVolume(spec *DetachVolumeSpec) error {
	nodeName := spec.NodeName
	// TODO: Modify vmdiskPath to all input options in the log next line.
	glog.V(3).Infof("vSphere Cloud Provider detaching disk %+v from node %s", spec.VolID, nodeName)
	node, err := csp.nodeManager.GetNode(convertToString(nodeName))
	if err != nil {
		glog.Errorf("Cannot get node %s from nodemanager for detaching disk %+v with error %+v",
			nodeName, spec.VolID, err)
		return err
	}
	vc, err := csp.virtualCenterManager.GetVirtualCenter(node.VirtualCenterHost)
	if err != nil {
		glog.Errorf("Cannot get virtual center %s from vitualcentermanager for detaching disk %+v with error %+v", node.VirtualCenterHost, spec.VolID.ID, err)
		return err
	}

	volID, datastoreURL := GetVolumeIDAndDatastoreURL(spec.VolID.ID)
	detachSpec := &cspvolumestypes.AttachDetachSpec{
		VolumeID: &cspvolumestypes.VolumeID{
			ID:           volID,
			DatastoreURL: datastoreURL,
		},
		VirtualMachine: node,
	}
	glog.V(5).Infof("vSphere Cloud Provider detaching volume %s with detach spec %+v", volID, detachSpec)
	err = cspvolumes.GetManager(vc).DetachVolume(detachSpec)
	if err != nil {
		glog.Errorf("Failed to dettach disk %s with err %+v", volID, err)
		return err
	}
	return nil
}

// DeleteVolume deletes a volume given its spec.
func (csp *CSP) DeleteVSphereVolume(spec *DeleteVolumeSpec) error {
	// TODO: Modify vmdiskPath to all input options in the log next line.
	glog.V(3).Infof("vSphere Cloud Provider deleting volume %+v", spec.VolID.ID)
	vc, err := csp.virtualCenterManager.GetVirtualCenter(csp.cfg.Workspace.VCenterIP)
	if err != nil {
		glog.Errorf("Cannot get virtual center %s from vitualcentermanager for delete disk %+v with error %+v",
			csp.cfg.Workspace.VCenterIP, spec.VolID, err)
		return err
	}
	volID, datastoreURL := GetVolumeIDAndDatastoreURL(spec.VolID.ID)
	// TODO: Replace vmdiskPath to VolumeID in this function wherever required.
	deleteSpec := &cspvolumestypes.DeleteSpec{
		VolumeID: &cspvolumestypes.VolumeID{
			ID:           volID,
			DatastoreURL: datastoreURL,
		},
	}

	glog.V(5).Infof("vSphere Cloud Provider deleting volume %s with delete spec %+v", spec.VolID.ID, deleteSpec)
	err = cspvolumes.GetManager(vc).DeleteVolume(deleteSpec)
	if err != nil {
		glog.Errorf("Failed to delete disk %s with error %+v", spec.VolID.ID, err)
		return err
	}
	return nil
}

// VolumesAreAttached checks if a list disks are attached to the given node.
// Assumption: If node doesn't exist, disks are not attached to the node.
func (csp *CSP) VolumesIsAttached(volumeID VolumeID, nodeName k8stypes.NodeName) (bool, error) {
	glog.V(3).Infof("vSphere Cloud Provider checking disk is attached %s to node %s", volumeID.ID, nodeName)
	nodeVolumes := make(map[k8stypes.NodeName][]*VolumeID)
	nodeVolumes[nodeName] = append(nodeVolumes[nodeName], &volumeID)
	volumesAttached, err := csp.VolumesAreAttached(nodeVolumes)
	if err != nil {
		glog.Errorf("Failed to check disk is attached %s to node %s with error %+v", volumeID.ID, nodeName, err)
		return false, err
	}
	return volumesAttached[nodeName][&volumeID], nil
}

// VolumesAreAttached checks if a list disks are attached to the given node.
// Assumption: If node doesn't exist, disks are not attached to the node.
func (csp *CSP) VolumesAreAttached(nodeVolumes map[k8stypes.NodeName][]*VolumeID) (map[k8stypes.NodeName]map[*VolumeID]bool, error) {
	glog.V(3).Infof("vSphere Cloud Provider checking disks are attached %+v", nodeVolumes)
	vc, err := csp.virtualCenterManager.GetVirtualCenter(csp.cfg.Workspace.VCenterIP)
	if err != nil {
		glog.Errorf("Cannot get virtual center %s from vitualcentermanager for checking disk are attach %+v with error %+v",
			csp.cfg.Workspace.VCenterIP, nodeVolumes, err)
		return nil, err
	}
	cspNodeVolumes := make(map[string][]*cspvolumestypes.VolumeID)
	for k8snode, volumes := range nodeVolumes {
		node := convertToString(k8snode)
		for _, volume := range volumes {
			volID, datastoreURL := GetVolumeIDAndDatastoreURL(volume.ID)
			volumeID := &cspvolumestypes.VolumeID{
				ID:           volID,
				DatastoreURL: datastoreURL,
			}
			cspNodeVolumes[node] = append(cspNodeVolumes[node], volumeID)
		}
	}
	volumesAreAttached, err := cspvolumes.GetManager(vc).VolumesAreAttached(csp.nodeManager, cspNodeVolumes)
	if err != nil {
		glog.Errorf("Failed to check disk are attach %s with error %+v", volumesAreAttached, err)
		return nil, err
	}
	attachedVolumes := make(map[k8stypes.NodeName]map[*VolumeID]bool)
	for node, volumes := range volumesAreAttached {
		k8snode := convertToK8sType(node)
		if _, ok := attachedVolumes[k8snode]; !ok {
			attachedVolumes[k8snode] = make(map[*VolumeID]bool)
		}
		for volumeID, attached := range volumes {
			attachedVolumes[k8snode][&VolumeID{ID: GetVolPathFromVolumeID(volumeID)}] = attached
		}
	}
	return attachedVolumes, nil
}

// TODO: Remove below functions after adding DatastoreURL and VolumeID variables in volume source structure
func GetVolumeIDAndDatastoreURL(id string) (string, string) {
	dsPathObj, _ := vclib.GetDatastorePathObjFromVMDiskPath(id)
	return dsPathObj.Path, dsPathObj.Datastore
}

func GetVolPathFromVolumeID(volumeID *cspvolumestypes.VolumeID) string {
	return fmt.Sprintf("[%s] %s", volumeID.DatastoreURL, volumeID.ID)
}

func getPersistentVolume(pvc *v1.PersistentVolumeClaim, pvLister corelisters.PersistentVolumeLister) (*v1.PersistentVolume, error) {
	volumeName := pvc.Spec.VolumeName
	pv, err := pvLister.Get(volumeName)

	if err != nil {
		return nil, fmt.Errorf("failed to find PV %q in PV informer cache with error : %v", volumeName, err)
	}

	return pv.DeepCopy(), nil
}

func AddPrefixToLabels(prefix string, labels map[string]string) (map[string]string){
	prefixedLabels := make(map[string]string)
	for labelKey, labelValue := range labels {
		prefixedKey := strings.Join([]string{prefix, labelKey}, "#")
		prefixedLabels[prefixedKey] = labelValue
	}
	return prefixedLabels
}