package vsphere

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/golang/glog"
	"golang.org/x/net/context"
	"k8s.io/api/core/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	v1helper "k8s.io/kubernetes/pkg/apis/core/v1/helper"
	"k8s.io/client-go/informers"
	"k8s.io/kubernetes/pkg/cloudprovider"
	"k8s.io/kubernetes/pkg/cloudprovider/providers/vsphere/vclib"
	"k8s.io/kubernetes/pkg/cloudprovider/providers/vsphere/vclib/diskmanagers"
	corelisters "k8s.io/client-go/listers/core/v1"
	"net"
	"reflect"
)

type VCP struct {
	*VSphere
	// Responsible for managing discovery of k8s node, their location etc.
	pvLister    corelisters.PersistentVolumeLister
	nodeManager *NodeManager
}

var _ cloudprovider.Interface = &VCP{}
var _ cloudprovider.Instances = &VCP{}
var _ Volumes = &VCP{}
var _ K8sEvents = &VCP{}
var _ NodeEvents = &VCP{}

func (vs *VCP) SetInformers(informerFactory informers.SharedInformerFactory) {
	if vs.cfg == nil {
		return
	}
	SetInformers(vs, informerFactory)
}

func (vs *VCP) NodeEvents() (NodeEvents, bool) {
	return vs, true
}

func (vs *VCP) PVCEvents() (PVCEvents, bool) {
	return vs, true
}

func (vs *VCP) PVEvents() (PVEvents, bool) {
	return vs, true
}

func (vs *VCP) StorePVLister(pvLister corelisters.PersistentVolumeLister) {
	vs.pvLister = pvLister
}

func (vs *VCP) PVCUpdated(oldObj, newObj interface{}) {
	glog.V(1).Infof("PVC Update called from VCP")
	oldPvc, ok := oldObj.(*v1.PersistentVolumeClaim)

	if oldPvc == nil || !ok {
		return
	}

	newPvc, ok := newObj.(*v1.PersistentVolumeClaim)

	if newPvc == nil || !ok {
		return
	}

	glog.V(4).Infof("OldPVC: %+v", oldPvc)
	glog.V(4).Infof("NewPVC: %+v", newPvc)

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

// AttachDisk attaches given virtual disk volume to the compute running kubelet.
func (vs *VCP) AttachDisk(vmDiskPath string, storagePolicyName string, nodeName k8stypes.NodeName) (diskUUID string, err error) {
	attachDiskInternal := func(vmDiskPath string, storagePolicyName string, nodeName k8stypes.NodeName) (diskUUID string, err error) {
		if nodeName == "" {
			nodeName = convertToK8sType(vs.hostName)
		}
		// Create context
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		vsi, err := vs.getVSphereInstance(nodeName)
		if err != nil {
			return "", err
		}
		// Ensure client is logged in and session is valid
		err = vsi.conn.Connect(ctx)
		if err != nil {
			return "", err
		}

		vm, err := vs.getVMFromNodeName(ctx, nodeName)
		if err != nil {
			glog.Errorf("Failed to get VM object for node: %q. err: +%v", convertToString(nodeName), err)
			return "", err
		}

		diskUUID, err = vm.AttachDisk(ctx, vmDiskPath, &vclib.VolumeOptions{SCSIControllerType: vclib.PVSCSIControllerType, StoragePolicyName: storagePolicyName})
		if err != nil {
			glog.Errorf("Failed to attach disk: %s for node: %s. err: +%v", vmDiskPath, convertToString(nodeName), err)
			return "", err
		}
		return diskUUID, nil
	}
	requestTime := time.Now()
	diskUUID, err = attachDiskInternal(vmDiskPath, storagePolicyName, nodeName)
	if err != nil {
		var isManagedObjectNotFoundError bool
		isManagedObjectNotFoundError, err = vs.retry(nodeName, err)
		if isManagedObjectNotFoundError {
			if err == nil {
				glog.V(4).Infof("AttachDisk: Found node %q", convertToString(nodeName))
				diskUUID, err = attachDiskInternal(vmDiskPath, storagePolicyName, nodeName)
				glog.V(4).Infof("AttachDisk: Retry: diskUUID %s, err +%v", convertToString(nodeName), diskUUID, err)
			}
		}
	}
	glog.V(4).Infof("AttachDisk executed for node %s and volume %s with diskUUID %s. Err: %s", convertToString(nodeName), vmDiskPath, diskUUID, err)
	vclib.RecordvSphereMetric(vclib.OperationAttachVolume, requestTime, err)
	return diskUUID, err
}

func (vs *VCP) retry(nodeName k8stypes.NodeName, err error) (bool, error) {
	isManagedObjectNotFoundError := false
	if err != nil {
		if vclib.IsManagedObjectNotFoundError(err) {
			isManagedObjectNotFoundError = true
			glog.V(4).Infof("error %q ManagedObjectNotFound for node %q", err, convertToString(nodeName))
			err = vs.nodeManager.RediscoverNode(nodeName)
		}
	}
	return isManagedObjectNotFoundError, err
}

// DetachDisk detaches given virtual disk volume from the compute running kubelet.
func (vs *VCP) DetachDisk(volPath string, nodeName k8stypes.NodeName) error {
	detachDiskInternal := func(volPath string, nodeName k8stypes.NodeName) error {
		if nodeName == "" {
			nodeName = convertToK8sType(vs.hostName)
		}
		// Create context
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		vsi, err := vs.getVSphereInstance(nodeName)
		if err != nil {
			return err
		}
		// Ensure client is logged in and session is valid
		err = vsi.conn.Connect(ctx)
		if err != nil {
			return err
		}
		vm, err := vs.getVMFromNodeName(ctx, nodeName)
		if err != nil {
			// If node doesn't exist, disk is already detached from node.
			if err == vclib.ErrNoVMFound {
				glog.Infof("Node %q does not exist, disk %s is already detached from node.", convertToString(nodeName), volPath)
				return nil
			}

			glog.Errorf("Failed to get VM object for node: %q. err: +%v", convertToString(nodeName), err)
			return err
		}
		err = vm.DetachDisk(ctx, volPath)
		if err != nil {
			glog.Errorf("Failed to detach disk: %s for node: %s. err: +%v", volPath, convertToString(nodeName), err)
			return err
		}
		return nil
	}
	requestTime := time.Now()
	err := detachDiskInternal(volPath, nodeName)
	if err != nil {
		var isManagedObjectNotFoundError bool
		isManagedObjectNotFoundError, err = vs.retry(nodeName, err)
		if isManagedObjectNotFoundError {
			if err == nil {
				err = detachDiskInternal(volPath, nodeName)
			}
		}
	}
	vclib.RecordvSphereMetric(vclib.OperationDetachVolume, requestTime, err)
	return err
}

// DiskIsAttached returns if disk is attached to the VM using controllers supported by the plugin.
func (vs *VCP) DiskIsAttached(volPath string, nodeName k8stypes.NodeName) (bool, error) {
	diskIsAttachedInternal := func(volPath string, nodeName k8stypes.NodeName) (bool, error) {
		var vSphereInstance string
		if nodeName == "" {
			vSphereInstance = vs.hostName
			nodeName = convertToK8sType(vSphereInstance)
		} else {
			vSphereInstance = convertToString(nodeName)
		}
		// Create context
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		vsi, err := vs.getVSphereInstance(nodeName)
		if err != nil {
			return false, err
		}
		// Ensure client is logged in and session is valid
		err = vsi.conn.Connect(ctx)
		if err != nil {
			return false, err
		}
		vm, err := vs.getVMFromNodeName(ctx, nodeName)
		if err != nil {
			if err == vclib.ErrNoVMFound {
				glog.Warningf("Node %q does not exist, vsphere CP will assume disk %v is not attached to it.", nodeName, volPath)
				// make the disk as detached and return false without error.
				return false, nil
			}
			glog.Errorf("Failed to get VM object for node: %q. err: +%v", vSphereInstance, err)
			return false, err
		}

		volPath = vclib.RemoveStorageClusterORFolderNameFromVDiskPath(volPath)
		attached, err := vm.IsDiskAttached(ctx, volPath)
		if err != nil {
			glog.Errorf("DiskIsAttached failed to determine whether disk %q is still attached on node %q",
				volPath,
				vSphereInstance)
		}
		glog.V(4).Infof("DiskIsAttached result: %q and error: %q, for volume: %q", attached, err, volPath)
		return attached, err
	}
	requestTime := time.Now()
	isAttached, err := diskIsAttachedInternal(volPath, nodeName)
	if err != nil {
		var isManagedObjectNotFoundError bool
		isManagedObjectNotFoundError, err = vs.retry(nodeName, err)
		if isManagedObjectNotFoundError {
			if err == vclib.ErrNoVMFound {
				isAttached, err = false, nil
			} else if err == nil {
				isAttached, err = diskIsAttachedInternal(volPath, nodeName)
			}
		}
	}
	vclib.RecordvSphereMetric(vclib.OperationDiskIsAttached, requestTime, err)
	return isAttached, err
}

// DisksAreAttached returns if disks are attached to the VM using controllers supported by the plugin.
// 1. Converts volPaths into canonical form so that it can be compared with the VM device path.
// 2. Segregates nodes by vCenter and Datacenter they are present in. This reduces calls to VC.
// 3. Creates go routines per VC-DC to find whether disks are attached to the nodes.
// 4. If the some of the VMs are not found or migrated then they are added to a list.
// 5. After successful execution of goroutines,
// 5a. If there are any VMs which needs to be retried, they are rediscovered and the whole operation is initiated again for only rediscovered VMs.
// 5b. If VMs are removed from vSphere inventory they are ignored.
func (vs *VCP) DisksAreAttached(nodeVolumes map[k8stypes.NodeName][]string) (map[k8stypes.NodeName]map[string]bool, error) {
	disksAreAttachedInternal := func(nodeVolumes map[k8stypes.NodeName][]string) (map[k8stypes.NodeName]map[string]bool, error) {

		// disksAreAttach checks whether disks are attached to the nodes.
		// Returns nodes that need to be retried if retry is true
		// Segregates nodes per VC and DC
		// Creates go routines per VC-DC to find whether disks are attached to the nodes.
		disksAreAttach := func(ctx context.Context, nodeVolumes map[k8stypes.NodeName][]string, attached map[string]map[string]bool, retry bool) ([]k8stypes.NodeName, error) {

			var wg sync.WaitGroup
			var localAttachedMaps []map[string]map[string]bool
			var nodesToRetry []k8stypes.NodeName
			var globalErr error
			globalErr = nil
			globalErrMutex := &sync.Mutex{}
			nodesToRetryMutex := &sync.Mutex{}

			// Segregate nodes according to VC-DC
			dcNodes := make(map[string][]k8stypes.NodeName)
			for nodeName := range nodeVolumes {
				nodeInfo, err := vs.nodeManager.GetNodeInfo(nodeName)
				if err != nil {
					glog.Errorf("Failed to get node info: %+v. err: %+v", nodeInfo.vm, err)
					return nodesToRetry, err
				}
				VC_DC := nodeInfo.vcServer + nodeInfo.dataCenter.String()
				dcNodes[VC_DC] = append(dcNodes[VC_DC], nodeName)
			}

			for _, nodes := range dcNodes {
				localAttachedMap := make(map[string]map[string]bool)
				localAttachedMaps = append(localAttachedMaps, localAttachedMap)
				// Start go routines per VC-DC to check disks are attached
				go func() {
					nodesToRetryLocal, err := vs.checkDiskAttached(ctx, nodes, nodeVolumes, localAttachedMap, retry)
					if err != nil {
						if !vclib.IsManagedObjectNotFoundError(err) {
							globalErrMutex.Lock()
							globalErr = err
							globalErrMutex.Unlock()
							glog.Errorf("Failed to check disk attached for nodes: %+v. err: %+v", nodes, err)
						}
					}
					nodesToRetryMutex.Lock()
					nodesToRetry = append(nodesToRetry, nodesToRetryLocal...)
					nodesToRetryMutex.Unlock()
					wg.Done()
				}()
				wg.Add(1)
			}
			wg.Wait()
			if globalErr != nil {
				return nodesToRetry, globalErr
			}
			for _, localAttachedMap := range localAttachedMaps {
				for key, value := range localAttachedMap {
					attached[key] = value
				}
			}

			return nodesToRetry, nil
		}

		glog.V(4).Info("Starting DisksAreAttached API for vSphere with nodeVolumes: %+v", nodeVolumes)
		// Create context
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		disksAttached := make(map[k8stypes.NodeName]map[string]bool)
		if len(nodeVolumes) == 0 {
			return disksAttached, nil
		}

		// Convert VolPaths into canonical form so that it can be compared with the VM device path.
		vmVolumes, err := vs.convertVolPathsToDevicePaths(ctx, nodeVolumes)
		if err != nil {
			glog.Errorf("Failed to convert volPaths to devicePaths: %+v. err: %+v", nodeVolumes, err)
			return nil, err
		}
		attached := make(map[string]map[string]bool)
		nodesToRetry, err := disksAreAttach(ctx, vmVolumes, attached, false)
		if err != nil {
			return nil, err
		}

		if len(nodesToRetry) != 0 {
			// Rediscover nodes which are need to be retried
			remainingNodesVolumes := make(map[k8stypes.NodeName][]string)
			for _, nodeName := range nodesToRetry {
				err = vs.nodeManager.RediscoverNode(nodeName)
				if err != nil {
					if err == vclib.ErrNoVMFound {
						glog.V(4).Infof("node %s not found. err: %+v", nodeName, err)
						continue
					}
					glog.Errorf("Failed to rediscover node %s. err: %+v", nodeName, err)
					return nil, err
				}
				remainingNodesVolumes[nodeName] = nodeVolumes[nodeName]
			}

			// If some remaining nodes are still registered
			if len(remainingNodesVolumes) != 0 {
				nodesToRetry, err = disksAreAttach(ctx, remainingNodesVolumes, attached, true)
				if err != nil || len(nodesToRetry) != 0 {
					glog.Errorf("Failed to retry disksAreAttach  for nodes %+v. err: %+v", remainingNodesVolumes, err)
					return nil, err
				}
			}

			for nodeName, volPaths := range attached {
				disksAttached[convertToK8sType(nodeName)] = volPaths
			}
		}
		glog.V(4).Infof("DisksAreAttach successfully executed. result: %+v", attached)
		return disksAttached, nil
	}
	requestTime := time.Now()
	attached, err := disksAreAttachedInternal(nodeVolumes)
	vclib.RecordvSphereMetric(vclib.OperationDisksAreAttached, requestTime, err)
	return attached, err
}

// CreateVolume creates a volume of given size (in KiB) and return the volume path.
// If the volumeOptions.Datastore is part of datastore cluster for example - [DatastoreCluster/sharedVmfs-0] then
// return value will be [DatastoreCluster/sharedVmfs-0] kubevols/<volume-name>.vmdk
// else return value will be [sharedVmfs-0] kubevols/<volume-name>.vmdk
func (vs *VCP) CreateVolume(volumeOptions *vclib.VolumeOptions) (canonicalVolumePath string, err error) {
	glog.V(1).Infof("Starting to create a vSphere volume with volumeOptions: %+v", volumeOptions)
	createVolumeInternal := func(volumeOptions *vclib.VolumeOptions) (canonicalVolumePath string, err error) {
		var datastore string
		// If datastore not specified, then use default datastore
		if volumeOptions.Datastore == "" {
			datastore = vs.cfg.Workspace.DefaultDatastore
		} else {
			datastore = volumeOptions.Datastore
		}
		datastore = strings.TrimSpace(datastore)
		// Create context
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		vsi, err := vs.getVSphereInstanceForServer(vs.cfg.Workspace.VCenterIP, ctx)
		if err != nil {
			return "", err
		}
		dc, err := vclib.GetDatacenter(ctx, vsi.conn, vs.cfg.Workspace.Datacenter)
		if err != nil {
			return "", err
		}
		var vmOptions *vclib.VMOptions
		if volumeOptions.VSANStorageProfileData != "" || volumeOptions.StoragePolicyName != "" {
			// Acquire a read lock to ensure multiple PVC requests can be processed simultaneously.
			cleanUpDummyVMLock.RLock()
			defer cleanUpDummyVMLock.RUnlock()
			// Create a new background routine that will delete any dummy VM's that are left stale.
			// This routine will get executed for every 5 minutes and gets initiated only once in its entire lifetime.
			cleanUpRoutineInitLock.Lock()
			if !cleanUpRoutineInitialized {
				glog.V(1).Infof("Starting a clean up routine to remove stale dummy VM's")
				go vs.cleanUpDummyVMs(DummyVMPrefixName)
				cleanUpRoutineInitialized = true
			}
			cleanUpRoutineInitLock.Unlock()
			vmOptions, err = vs.setVMOptions(ctx, dc, vs.cfg.Workspace.ResourcePoolPath)
			if err != nil {
				glog.Errorf("Failed to set VM options requires to create a vsphere volume. err: %+v", err)
				return "", err
			}
		}
		if volumeOptions.StoragePolicyName != "" && volumeOptions.Datastore == "" {
			datastore, err = getPbmCompatibleDatastore(ctx, dc, volumeOptions.StoragePolicyName, vs.nodeManager)
			if err != nil {
				glog.Errorf("Failed to get pbm compatible datastore with storagePolicy: %s. err: %+v", volumeOptions.StoragePolicyName, err)
				return "", err
			}
		} else {
			// Since no storage policy is specified but datastore is specified, check
			// if the given datastore is a shared datastore across all node VMs.
			sharedDsList, err := getSharedDatastoresInK8SCluster(ctx, dc, vs.nodeManager)
			if err != nil {
				glog.Errorf("Failed to get shared datastore: %+v", err)
				return "", err
			}
			found := false
			for _, sharedDs := range sharedDsList {
				if datastore == sharedDs.Info.Name {
					found = true
					break
				}
			}
			if !found {
				msg := fmt.Sprintf("The specified datastore %s is not a shared datastore across node VMs", datastore)
				return "", errors.New(msg)
			}
		}
		ds, err := dc.GetDatastoreByName(ctx, datastore)
		if err != nil {
			return "", err
		}
		volumeOptions.Datastore = datastore
		kubeVolsPath := filepath.Clean(ds.Path(VolDir)) + "/"
		err = ds.CreateDirectory(ctx, kubeVolsPath, false)
		if err != nil && err != vclib.ErrFileAlreadyExist {
			glog.Errorf("Cannot create dir %#v. err %s", kubeVolsPath, err)
			return "", err
		}
		volumePath := kubeVolsPath + volumeOptions.Name + ".vmdk"
		disk := diskmanagers.VirtualDisk{
			DiskPath:      volumePath,
			VolumeOptions: volumeOptions,
			VMOptions:     vmOptions,
		}
		volumePath, err = disk.Create(ctx, ds)
		if err != nil {
			glog.Errorf("Failed to create a vsphere volume with volumeOptions: %+v on datastore: %s. err: %+v", volumeOptions, datastore, err)
			return "", err
		}
		// Get the canonical path for the volume path.
		canonicalVolumePath, err = getcanonicalVolumePath(ctx, dc, volumePath)
		if err != nil {
			glog.Errorf("Failed to get canonical vsphere volume path for volume: %s with volumeOptions: %+v on datastore: %s. err: %+v", volumePath, volumeOptions, datastore, err)
			return "", err
		}
		if filepath.Base(datastore) != datastore {
			// If datastore is within cluster, add cluster path to the volumePath
			canonicalVolumePath = strings.Replace(canonicalVolumePath, filepath.Base(datastore), datastore, 1)
		}
		return canonicalVolumePath, nil
	}
	requestTime := time.Now()
	canonicalVolumePath, err = createVolumeInternal(volumeOptions)
	vclib.RecordCreateVolumeMetric(volumeOptions, requestTime, err)
	glog.V(4).Infof("The canonical volume path for the newly created vSphere volume is %q", canonicalVolumePath)
	return canonicalVolumePath, err
}

// DeleteVolume deletes a volume given volume name.
func (vs *VCP) DeleteVolume(vmDiskPath string) error {
	glog.V(1).Infof("Starting to delete vSphere volume with vmDiskPath: %s", vmDiskPath)
	deleteVolumeInternal := func(vmDiskPath string) error {
		// Create context
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		vsi, err := vs.getVSphereInstanceForServer(vs.cfg.Workspace.VCenterIP, ctx)
		if err != nil {
			return err
		}
		dc, err := vclib.GetDatacenter(ctx, vsi.conn, vs.cfg.Workspace.Datacenter)
		if err != nil {
			return err
		}
		disk := diskmanagers.VirtualDisk{
			DiskPath:      vmDiskPath,
			VolumeOptions: &vclib.VolumeOptions{},
			VMOptions:     &vclib.VMOptions{},
		}
		err = disk.Delete(ctx, dc)
		if err != nil {
			glog.Errorf("Failed to delete vsphere volume with vmDiskPath: %s. err: %+v", vmDiskPath, err)
		}
		return err
	}
	requestTime := time.Now()
	err := deleteVolumeInternal(vmDiskPath)
	vclib.RecordvSphereMetric(vclib.OperationDeleteVolume, requestTime, err)
	return err
}

// Get the VM Managed Object instance by from the node
func (vs *VCP) getVMFromNodeName(ctx context.Context, nodeName k8stypes.NodeName) (*vclib.VirtualMachine, error) {
	nodeInfo, err := vs.nodeManager.GetNodeInfo(nodeName)
	if err != nil {
		return nil, err
	}
	return nodeInfo.vm, nil
}

func (vs *VCP) getVSphereInstanceForServer(vcServer string, ctx context.Context) (*VSphereInstance, error) {
	vsphereIns, ok := vs.vsphereInstanceMap[vcServer]
	if !ok {
		glog.Errorf("cannot find vcServer %q in cache. VC not found!!!", vcServer)
		return nil, errors.New(fmt.Sprintf("Cannot find node %q in vsphere configuration map", vcServer))
	}
	// Ensure client is logged in and session is valid
	err := vsphereIns.conn.Connect(ctx)
	if err != nil {
		glog.Errorf("failed connecting to vcServer %q with error %+v", vcServer, err)
		return nil, err
	}

	return vsphereIns, nil
}

func (vs *VCP) getVSphereInstance(nodeName k8stypes.NodeName) (*VSphereInstance, error) {
	vsphereIns, err := vs.nodeManager.GetVSphereInstance(nodeName)
	if err != nil {
		glog.Errorf("Cannot find node %q in cache. Node not found!!!", nodeName)
		return nil, err
	}
	return &vsphereIns, nil
}

// Notification handler when node is added into k8s cluster.
func (vs *VCP) NodeAdded(obj interface{}) {
	node, ok := obj.(*v1.Node)
	if node == nil || !ok {
		glog.Warningf("NodeAdded: unrecognized object %+v", obj)
		return
	}

	glog.V(4).Infof("Node added: %+v", node)
	vs.nodeManager.RegisterNode(node)
}

// Notification handler when node is removed from k8s cluster.
func (vs *VCP) NodeDeleted(obj interface{}) {
	node, ok := obj.(*v1.Node)
	if node == nil || !ok {
		glog.Warningf("NodeDeleted: unrecognized object %+v", obj)
		return
	}

	glog.V(4).Infof("Node deleted: %+v", node)
	vs.nodeManager.UnRegisterNode(node)
}

// Instances returns an implementation of Instances for vSphere.
func (vs *VCP) Instances() (cloudprovider.Instances, bool) {
	return vs, true
}

func (vs *VCP) ExternalID(ctx context.Context, nodeName k8stypes.NodeName) (string, error) {
	return vs.InstanceID(ctx, nodeName)
}

// InstanceExistsByProviderID returns true if the instance with the given provider id still exists and is running.
// If false is returned with no error, the instance will be immediately deleted by the cloud controller manager.
func (vs *VCP) InstanceExistsByProviderID(ctx context.Context, providerID string) (bool, error) {
	var nodeName string
	nodes, err := vs.nodeManager.GetNodeDetails()
	if err != nil {
		glog.Errorf("Error while obtaining Kubernetes node nodeVmDetail details. error : %+v", err)
		return false, err
	}
	for _, node := range nodes {
		if node.VMUUID == GetUUIDFromProviderID(providerID) {
			nodeName = node.NodeName
			break
		}
	}
	if nodeName == "" {
		msg := fmt.Sprintf("Error while obtaining Kubernetes nodename for providerID %s.", providerID)
		return false, errors.New(msg)
	}
	_, err = vs.InstanceID(ctx, convertToK8sType(nodeName))
	if err == nil {
		return true, nil
	}

	return false, err
}

// InstanceID returns the cloud provider ID of the node with the specified Name.
func (vs *VCP) InstanceID(ctx context.Context, nodeName k8stypes.NodeName) (string, error) {

	instanceIDInternal := func() (string, error) {
		if vs.hostName == convertToString(nodeName) {
			return vs.vmUUID, nil
		}

		// Below logic can be performed only on master node where VC details are preset.
		if vs.cfg == nil {
			return "", fmt.Errorf("The current node can't detremine InstanceID for %q", convertToString(nodeName))
		}

		// Create context
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		vsi, err := vs.getVSphereInstance(nodeName)
		if err != nil {
			return "", err
		}
		// Ensure client is logged in and session is valid
		err = vsi.conn.Connect(ctx)
		if err != nil {
			return "", err
		}
		vm, err := vs.getVMFromNodeName(ctx, nodeName)
		if err != nil {
			if err == vclib.ErrNoVMFound {
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
			return vs.vmUUID, nil
		}
		glog.Warningf("The VM: %s is not in %s state", convertToString(nodeName), vclib.ActivePowerState)
		return "", cloudprovider.InstanceNotFound
	}

	instanceID, err := instanceIDInternal()
	if err != nil {
		var isManagedObjectNotFoundError bool
		isManagedObjectNotFoundError, err = vs.retry(nodeName, err)
		if isManagedObjectNotFoundError {
			if err == nil {
				glog.V(4).Infof("InstanceID: Found node %q", convertToString(nodeName))
				instanceID, err = instanceIDInternal()
			} else if err == vclib.ErrNoVMFound {
				return "", cloudprovider.InstanceNotFound
			}
		}
	}

	return instanceID, err
}

// NodeAddresses is an implementation of Instances.NodeAddresses.
func (vs *VCP) NodeAddresses(ctx context.Context, nodeName k8stypes.NodeName) ([]v1.NodeAddress, error) {
	// Get local IP addresses if node is local node
	if vs.hostName == convertToString(nodeName) {
		return getLocalIP()
	}

	if vs.cfg == nil {
		return nil, cloudprovider.InstanceNotFound
	}

	// Below logic can be executed only on master as VC details are present.
	addrs := []v1.NodeAddress{}
	// Create context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	vsi, err := vs.getVSphereInstance(nodeName)
	if err != nil {
		return nil, err
	}
	// Ensure client is logged in and session is valid
	err = vsi.conn.Connect(ctx)
	if err != nil {
		return nil, err
	}

	vm, err := vs.getVMFromNodeName(ctx, nodeName)
	if err != nil {
		glog.Errorf("Failed to get VM object for node: %q. err: +%v", convertToString(nodeName), err)
		return nil, err
	}
	vmMoList, err := vm.Datacenter.GetVMMoList(ctx, []*vclib.VirtualMachine{vm}, []string{"guest.net"})
	if err != nil {
		glog.Errorf("Failed to get VM Managed object with property guest.net for node: %q. err: +%v", convertToString(nodeName), err)
		return nil, err
	}
	// retrieve VM's ip(s)
	for _, v := range vmMoList[0].Guest.Net {
		if vs.cfg.Network.PublicNetwork == v.Network {
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
func (vs *VCP) NodeAddressesByProviderID(ctx context.Context, providerID string) ([]v1.NodeAddress, error) {
	return vs.NodeAddresses(ctx, convertToK8sType(providerID))
}

func (vs *VCP) CheckVolumeCompliance(pvNodeMap map[k8stypes.NodeName][]*v1.PersistentVolume) error {
	return nil
}
