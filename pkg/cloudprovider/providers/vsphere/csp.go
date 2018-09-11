package vsphere

import (
	"context"
	"fmt"
	"net"
	"reflect"
	"strconv"
	"strings"

	"github.com/golang/glog"
	nodemanager "gitlab.eng.vmware.com/hatchway/common-csp/pkg/node"
	cspvolumes "gitlab.eng.vmware.com/hatchway/common-csp/pkg/volume"
	cspvolumestypes "gitlab.eng.vmware.com/hatchway/common-csp/pkg/volume/types"
	cspvsphere "gitlab.eng.vmware.com/hatchway/common-csp/pkg/vsphere"
	"k8s.io/api/core/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/informers"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	v1helper "k8s.io/kubernetes/pkg/apis/core/v1/helper"
	"k8s.io/kubernetes/pkg/cloudprovider"
	"k8s.io/kubernetes/pkg/cloudprovider/providers/vsphere/vclib"
)

var (
	PrefixPVCLabel = "vmware#cns#pvc"
)

type CSP struct {
	*VCP
	virtualCenterManager cspvsphere.VirtualCenterManager
	nodeManager          nodemanager.Manager
	volumeManager        cspvolumes.Manager
	PVLister             corelisters.PersistentVolumeLister
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
	glog.V(4).Infof("PV lister in vSphere cloud provider initialized")

}

// Notification handler when node is added into k8s cluster.
func (csp *CSP) NodeAdded(obj interface{}) {
	node, ok := obj.(*v1.Node)
	if node == nil || !ok {
		glog.Warningf("NodeAdded: unrecognized object %+v", obj)
		return
	}

	glog.V(4).Infof("Node added: %+v", node)
	nodeUUID, err := GetNodeUUID(node)
	if err != nil {
		glog.Errorf("Failed to get node uuid for node %s with error: %+v", node.Name, err)
		return
	}
	csp.nodeManager.RegisterNode(nodeUUID, node.Name, nil)
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
		glog.V(4).Infof("Updating %#v labels to %#v in cns for volume %s", oldLabels, newLabels, pv.Spec.PersistentVolumeSource.VsphereVolume.VolumePath)
		vc, err := csp.virtualCenterManager.GetVirtualCenter(csp.cfg.Workspace.VCenterIP)
		if err != nil {
			glog.Errorf("Cannot get virtual center object for server %s with error %+v", csp.cfg.Workspace.VCenterIP, err)
			return
		}
		prefixedLabels := AddPrefixToLabels(PrefixPVCLabel, newLabels)
		glog.V(4).Infof("Prefixed Labels are %+v", prefixedLabels)
		// TODO: Replace it with GetVolumeInfo CNS API once available
		if v1helper.GetPersistentVolumeClass(pv) == "" {
			glog.V(4).Infof("Volume %v is provisioned statically", pv.Spec.PersistentVolumeSource.VsphereVolume.VolumeID)
			createSpec := &cspvolumestypes.CreateSpec{
				Name: pv.Name,
				ContainerCluster: cspvolumestypes.ContainerCluster{
					ClusterID:   csp.cfg.Global.ClusterID,
					ClusterType: cspvolumestypes.ClusterTypeKUBERNETES,
				},
				DatastoreURLs: []string{pv.Spec.PersistentVolumeSource.VsphereVolume.DatastoreURL},
				BackingInfo:   &cspvolumestypes.BlockBackingInfo{BackingDiskID: pv.Spec.PersistentVolumeSource.VsphereVolume.VolumeID},
				Labels:        prefixedLabels,
			}
			csp.volumeManager.CreateVolume(createSpec)
			return
		}
		updateSpec := &cspvolumestypes.UpdateSpec{
			VolumeID: &cspvolumestypes.VolumeID{
				ID:           pv.Spec.PersistentVolumeSource.VsphereVolume.VolumeID,
				DatastoreURL: pv.Spec.PersistentVolumeSource.VsphereVolume.DatastoreURL,
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
	updateSpec := &cspvolumestypes.UpdateSpec{
		VolumeID: &cspvolumestypes.VolumeID{
			ID:           pv.Spec.PersistentVolumeSource.VsphereVolume.VolumeID,
			DatastoreURL: pv.Spec.PersistentVolumeSource.VsphereVolume.DatastoreURL,
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
		if csp.hostName == convertToString(nodeName) {
			return csp.vmUUID, nil
		}

		// Below logic can be performed only on master node where VC details are preset.
		if csp.cfg == nil {
			return "", fmt.Errorf("The current node can't detremine InstanceID for %q", convertToString(nodeName))
		}

		// Create context
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		vm, err := csp.nodeManager.GetNodeByName(convertToString(nodeName))
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

	vm, err := csp.nodeManager.GetNodeByName(convertToString(nodeName))
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
	return convertToK8sType(csp.hostName), nil
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
	var err error
	glog.V(3).Infof("vSphere Cloud Provider creating disk %+v", spec)
	vc, err := csp.virtualCenterManager.GetVirtualCenter(csp.cfg.Workspace.VCenterIP)
	if err != nil {
		glog.Errorf("Cannot get virtual center %s from vitualcentermanager for creating disk %+v with error %+v",
			csp.cfg.Workspace.VCenterIP, spec.Name, err)
		return VolumeID{}, err
	}

	var datastore string
	var datastoreUrl string
	// If datastore not specified in the storage class, then use default datastore
	if spec.Datastore == "" {
		datastore = csp.cfg.Workspace.DefaultDatastore
	} else {
		datastore = spec.Datastore
	}
	datastore = strings.TrimSpace(datastore)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err = vc.Connect(ctx)
	if err != nil {
		glog.Errorf("Failed to connect to Virtual Center: %s", csp.cfg.Workspace.VCenterIP)
		return VolumeID{}, err
	}

	if spec.StoragePolicyName != "" {
		// Get Storage Policy ID from Storage Policy Name
		err := vc.ConnectPbm(ctx)
		if err != nil {
			glog.Errorf("Error occurred while connecting to PBM, err: %+v", err)
			return VolumeID{}, err
		}
		spec.StoragePolicyID, err = vc.GetStoragePolicyIDByName(ctx, spec.StoragePolicyName)
		if err != nil {
			glog.Errorf("Error occurred while getting Profile Id from Profile Name: %s, err: %+v", spec.StoragePolicyName, err)
			return VolumeID{}, err
		}
	}
	var sharedDatastoreURLs []string
	sharedDatastoreURLs, err = GetSharedDatastoresInK8SCluster(ctx, csp.nodeManager)
	if err != nil {
		glog.Errorf("Failed to get shared datastores In K8S Cluster. Error: %+v", err)
		return VolumeID{}, err
	}

	var datastoreURLs []string
	if spec.StoragePolicyID != "" && spec.Datastore == "" || datastore == "" {
		// Pass list of all shared datastore URLs for following cases.
		// 1. SPBM Policy is specified but datastore is not specified in
		// the storage class.
		// 2. Datastore is not specified in the storage class as well as
		// in the cloud provider configuration file.
		datastoreURLs = sharedDatastoreURLs
	} else {
		datacenter, err := vc.GetDatacenter(ctx, csp.cfg.Workspace.Datacenter)
		if err != nil {
			glog.Errorf("Failed to find Datacenter:%+v from VC: %+v, Error: %+v", csp.cfg.Workspace.Datacenter, csp.cfg.Workspace.VCenterIP, err)
			return VolumeID{}, err
		}
		datastoreObj, err := datacenter.GetDatastoreByName(ctx, datastore)
		if err != nil {
			glog.Errorf("Failed to find Datastore:%+v in Datacenter:%+v from VC:%+v, Error: %+v", datastore, csp.cfg.Workspace.Datacenter, csp.cfg.Workspace.VCenterIP, err)
			return VolumeID{}, err
		}
		datastoreUrl, err = datastoreObj.GetDatatoreUrl(ctx)
		if err != nil {
			glog.Errorf("Failed to get URL for the datastore:%+v , Error: %+v", datastore, err)
			return VolumeID{}, err
		}
		isSharedDatastoreURL := false
		for _, url := range sharedDatastoreURLs {
			if url == datastoreUrl {
				isSharedDatastoreURL = true
				break
			}
		}
		if isSharedDatastoreURL {
			datastoreURLs = append(datastoreURLs, datastoreUrl)
		} else {
			errMsg := fmt.Sprintf("Datastore: %v is not accessible to all nodes.", datastore)
			glog.Errorf(errMsg)
			return VolumeID{}, err
		}
	}

	createSpec := &cspvolumestypes.CreateSpec{
		Name:          spec.Name,
		DatastoreURLs: datastoreURLs,
		BackingInfo: &cspvolumestypes.BlockBackingInfo{
			BackingObjectInfo: cspvolumestypes.BackingObjectInfo{
				StoragePolicyID: spec.StoragePolicyID,
				Capacity:        uint64(spec.CapacityKB) / 1024,
			},
		},
		ContainerCluster: cspvolumestypes.ContainerCluster{
			ClusterID:   csp.cfg.Global.ClusterID,
			ClusterType: cspvolumestypes.ClusterTypeKUBERNETES,
		},
		Labels: AddPrefixToLabels(PrefixPVCLabel, spec.PVC.Labels),
	}
	glog.V(5).Infof("vSphere Cloud Provider creating volume %s with create spec %+v", spec.Name, createSpec)
	volumeID, err := cspvolumes.GetManager(vc).CreateVolume(createSpec)
	if err != nil {
		glog.Errorf("Failed to create disk %s with error %+v", spec.Name, err)
		return VolumeID{}, err
	}
	return VolumeID{ID: volumeID.ID, DatastoreURL: volumeID.DatastoreURL, VolumePath: ""}, nil
}

// AttachVolume attaches a volume to a virtual machine given the spec.
func (csp *CSP) AttachVSphereVolume(spec *AttachVolumeSpec) (diskUUID string, err error) {
	nodeName := spec.NodeName
	volID := spec.VolID
	glog.V(3).Infof("vSphere Cloud Provider attaching disk %+v on node %s", volID, nodeName)
	node, err := csp.nodeManager.GetNodeByName(convertToString(nodeName))
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
	// Statically provisioned volume
	// TODO: Replace it with GetVolumeInfo CNS API once available
	if v1helper.GetPersistentVolumeClass(spec.PV) == "" {
		glog.V(4).Infof("Volume %v is provisioned statically", volID)
		createSpec := &cspvolumestypes.CreateSpec{
			Name: spec.PV.Name,
			ContainerCluster: cspvolumestypes.ContainerCluster{
				ClusterID:   csp.cfg.Global.ClusterID,
				ClusterType: cspvolumestypes.ClusterTypeKUBERNETES,
			},
			DatastoreURLs: []string{volID.DatastoreURL},
			BackingInfo:   &cspvolumestypes.BlockBackingInfo{BackingDiskID: volID.ID},
		}
		csp.volumeManager.CreateVolume(createSpec)
	}
	attachSpec := &cspvolumestypes.AttachDetachSpec{
		VolumeID: &cspvolumestypes.VolumeID{
			ID:           volID.ID,
			DatastoreURL: volID.DatastoreURL,
		},
		VirtualMachine: node,
	}
	glog.V(5).Infof("vSphere Cloud Provider attaching volume %s with attach spec %+v", volID.ID, attachSpec)
	diskUUID, err = cspvolumes.GetManager(vc).AttachVolume(attachSpec)
	if err != nil {
		glog.Errorf("Failed to attach disk %+v with err %+v", volID, err)
		return "", err
	}
	glog.V(5).Infof("Attached DiskUUID %s to node %s", diskUUID, nodeName)
	return diskUUID, nil
}

// DetachVolume detaches a volume from the virtual machine given the spec.
func (csp *CSP) DetachVSphereVolume(spec *DetachVolumeSpec) error {
	nodeName := spec.NodeName
	// TODO: Modify vmdiskPath to all input options in the log next line.
	glog.V(3).Infof("vSphere Cloud Provider detaching disk %+v from node %s", spec.VolID, nodeName)
	node, err := csp.nodeManager.GetNodeByName(convertToString(nodeName))
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

	detachSpec := &cspvolumestypes.AttachDetachSpec{
		VolumeID: &cspvolumestypes.VolumeID{
			ID:           spec.VolID.ID,
			DatastoreURL: spec.VolID.DatastoreURL,
		},
		VirtualMachine: node,
	}
	glog.V(5).Infof("vSphere Cloud Provider detaching volume %s with detach spec %+v", spec.VolID.ID, detachSpec)
	err = cspvolumes.GetManager(vc).DetachVolume(detachSpec)
	if err != nil {
		glog.Errorf("Failed to detach disk %s with err %+v", spec.VolID.ID, err)
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
	// TODO: Replace vmdiskPath to VolumeID in this function wherever required.
	deleteSpec := &cspvolumestypes.DeleteSpec{
		VolumeID: &cspvolumestypes.VolumeID{
			ID:           spec.VolID.ID,
			DatastoreURL: spec.VolID.DatastoreURL,
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
			volumeID := &cspvolumestypes.VolumeID{
				ID:           volume.ID,
				DatastoreURL: volume.DatastoreURL,
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

func AddPrefixToLabels(prefix string, labels map[string]string) map[string]string {
	prefixedLabels := make(map[string]string)
	for labelKey, labelValue := range labels {
		prefixedKey := strings.Join([]string{prefix, labelKey}, "#")
		prefixedLabels[prefixedKey] = labelValue
	}
	return prefixedLabels
}

func GetSharedDatastoresInK8SCluster(ctx context.Context, nodeManager nodemanager.Manager) ([]string, error) {
	var datastoreURLs []string
	nodeVMs, err := nodeManager.GetAllNodes()
	if err != nil {
		glog.Errorf("Failed to get Nodes from nodeManager with err %+v", err)
		return nil, err
	}
	if len(nodeVMs) == 0 {
		errMsg := fmt.Sprintf("Empty List of Node VMs returned from nodeManager")
		glog.Errorf(errMsg)
		return make([]string, 0), fmt.Errorf(errMsg)
	}
	var sharedDatastores []*cspvsphere.DatastoreInfo
	for _, nodeVm := range nodeVMs {
		glog.V(5).Infof("Getting accessible datastores for node %s", nodeVm.VirtualMachine.InventoryPath)
		accessibleDatastores, err := nodeVm.GetAllAccessibleDatastores(ctx)
		if err != nil {
			return nil, err
		}
		if len(sharedDatastores) == 0 {
			sharedDatastores = accessibleDatastores
		} else {
			var sharedAccessibleDatastores []*cspvsphere.DatastoreInfo
			for _, sharedDs := range sharedDatastores {
				// Check if sharedDatastores is found in accessibleDatastores
				for _, accessibleDs := range accessibleDatastores {
					// Intersection is performed based on the datastoreUrl as this uniquely identifies the datastore.
					if sharedDs.Info.Url == accessibleDs.Info.Url {
						sharedAccessibleDatastores = append(sharedAccessibleDatastores, sharedDs)
						break
					}
				}
			}
			sharedDatastores = sharedAccessibleDatastores
		}
		if len(sharedDatastores) == 0 {
			return nil, fmt.Errorf("No shared datastores found in the Kubernetes cluster for nodeVm: %+v", nodeVm)
		}
	}
	glog.V(5).Infof("sharedDatastores : %+v", sharedDatastores)
	for _, sharedDs := range sharedDatastores {
		datastoreURLs = append(datastoreURLs, sharedDs.Info.Url)
	}
	return datastoreURLs, nil
}
