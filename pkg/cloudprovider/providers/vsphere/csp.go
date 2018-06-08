package vsphere

import (
	"context"
	"fmt"
	"github.com/golang/glog"
	nodemanager "gitlab.eng.vmware.com/hatchway/common-csp/pkg/node"
	cspvsphere "gitlab.eng.vmware.com/hatchway/common-csp/pkg/vsphere"
	cspvolumes "gitlab.eng.vmware.com/hatchway/common-csp/pkg/volume"
	cspvolumestypes "gitlab.eng.vmware.com/hatchway/common-csp/pkg/volume/types"
	"k8s.io/api/core/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
	v1helper "k8s.io/kubernetes/pkg/apis/core/v1/helper"
	"k8s.io/kubernetes/pkg/cloudprovider"
	"k8s.io/kubernetes/pkg/cloudprovider/providers/vsphere/vclib"
	"net"
	"strconv"
	"strings"
)

type CSP struct {
	*VCP
	virtualCenterManager cspvsphere.VirtualCenterManager
	nodeManager          nodemanager.Manager
	volumeManager        cspvolumes.Manager
}

var _ cloudprovider.Interface = &CSP{}
var _ cloudprovider.Instances = &CSP{}
var _ Volumes = &CSP{}

type CSPID struct {
	clusterType string
	clusterID   string
}

func (csp *CSP) GetCSPID() *CSPID {
	return &CSPID{
		clusterType: "KUBERNETES",
		clusterID:   csp.cfg.Global.ClusterID,
	}
}

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

// AttachDisk attaches given virtual disk volume to the compute running kubelet.
func (csp *CSP) AttachDisk(vmDiskPath string, storagePolicyName string, nodeName k8stypes.NodeName) (diskUUID string, err error) {
	volumeID := &cspvolumestypes.VolumeID {
		ID: vmDiskPath,
		DatastoreURL: storagePolicyName,
	}
	node, err := csp.nodeManager.GetNode(convertToString(nodeName))
	if err != nil {
		return "", err
	}
	attachSpec := &cspvolumestypes.AttachDetachSpec{
		VolumeID: volumeID,
		VirtualMachine: node,
	}
	vc, err := csp.virtualCenterManager.GetVirtualCenter(csp.cfg.Workspace.VCenterIP)
	if err != nil {
		return "", err
	}
	diskUUID, err = cspvolumes.GetManager(vc).AttachVolume(attachSpec)
	if err != nil {
		glog.V(1).Infof("Failed to attach disk %s with err %+v", diskUUID, err)
		return "", err
	}
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
	cspID := csp.GetCSPID()
	glog.V(4).Infof("cspID: %+v", cspID)
	createSpec := &cspvolumestypes.CreateSpec{
		Name: volumeOptions.Name,
		DatastoreURLs: []string{csp.cfg.Workspace.DefaultDatastore},
		BackingInfo: &cspvolumestypes.BackingObjectInfo{
			StoragePolicyID: volumeOptions.StoragePolicyID,
			Capacity: uint64(volumeOptions.CapacityKB),
		},
	}
	vc, err := csp.virtualCenterManager.GetVirtualCenter(csp.cfg.Workspace.VCenterIP)
	if err != nil {
		return "", err
	}
	volumeID, err :=cspvolumes.GetManager(vc).CreateVolume(createSpec)
	if err != nil {
		glog.V(1).Infof("Failed to create disk %s with error %+v", volumeOptions.Name, err)
		return "", err
	}
	return volumeID.ID, nil
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
