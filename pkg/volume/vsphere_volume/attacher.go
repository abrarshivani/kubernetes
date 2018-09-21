/*
Copyright 2016 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package vsphere_volume

import (
	"fmt"
	"os"
	"path"
	"time"

	"github.com/davecgh/go-spew/spew"
	"github.com/golang/glog"
	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/kubernetes/pkg/cloudprovider/providers/vsphere"
	"k8s.io/kubernetes/pkg/util/keymutex"
	"k8s.io/kubernetes/pkg/util/mount"
	"k8s.io/kubernetes/pkg/volume"
	volumeutil "k8s.io/kubernetes/pkg/volume/util"
	"path/filepath"
	"strings"
)

type vsphereVMDKAttacher struct {
	host           volume.VolumeHost
	vsphereVolumes vsphere.CommonVolumes
}

var _ volume.Attacher = &vsphereVMDKAttacher{}

var _ volume.DeviceMounter = &vsphereVMDKAttacher{}

var _ volume.AttachableVolumePlugin = &vsphereVolumePlugin{}

var _ volume.DeviceMountableVolumePlugin = &vsphereVolumePlugin{}

// Singleton key mutex for keeping attach operations for the same host atomic
var attachdetachMutex = keymutex.NewHashed(0)

func (plugin *vsphereVolumePlugin) NewAttacher() (volume.Attacher, error) {
	vsphereCloud, err := getCloudProvider(plugin.host.GetCloudProvider())
	if err != nil {
		return nil, err
	}

	return &vsphereVMDKAttacher{
		host:           plugin.host,
		vsphereVolumes: vsphereCloud,
	}, nil
}

func (plugin *vsphereVolumePlugin) NewDeviceMounter() (volume.DeviceMounter, error) {
	return plugin.NewAttacher()
}

// Attaches the volume specified by the given spec to the given host.
// On success, returns the device path where the device was attached on the
// node.
// Callers are responsible for retryinging on failure.
// Callers are responsible for thread safety between concurrent attach and
// detach operations.
func (attacher *vsphereVMDKAttacher) Attach(spec *volume.Spec, nodeName types.NodeName) (string, error) {
	volumeSource, _, err := getVolumeSource(spec)
	if err != nil {
		return "", err
	}

	glog.V(4).Infof("vSphere: Attach disk called for node %s", nodeName)

	// Keeps concurrent attach operations to same host atomic
	attachdetachMutex.LockKey(string(nodeName))
	defer attachdetachMutex.UnlockKey(string(nodeName))

	// vsphereCloud.AttachDisk checks if disk is already attached to host and
	// succeeds in that case, so no need to do that separately.
	diskUUID, err := attacher.vsphereVolumes.AttachVSphereVolume(&vsphere.AttachVolumeSpec{
		VolID: vsphere.VolumeID{
			ID:           volumeSource.VolumeID,
			DatastoreURL: volumeSource.DatastoreURL,
			VolumePath:   volumeSource.VolumePath,
		},
		StoragePolicyName: volumeSource.StoragePolicyName,
		NodeName:          nodeName,
		PV:                spec.PersistentVolume,
	})
	if err != nil {
		glog.Errorf("Error attaching volume %q to node %q: %+v", volumeSource.VolumePath, nodeName, err)
		return "", err
	}
	diskUUID = strings.ToLower(strings.Replace(diskUUID, "-", "", -1))
	devicePath := path.Join(diskByIDPath, diskSCSIPrefix+diskUUID)
	glog.V(5).Infof("vSphere: Attach disk returning device path %s", devicePath)
	return devicePath, nil
}

func (attacher *vsphereVMDKAttacher) VolumesAreAttached(specs []*volume.Spec, nodeName types.NodeName) (map[*volume.Spec]bool, error) {
	glog.Warningf("Attacher.VolumesAreAttached called for node %q - Please use BulkVerifyVolumes for vSphere", nodeName)
	volumeNodeMap := map[types.NodeName][]*volume.Spec{
		nodeName: specs,
	}
	nodeVolumesResult := make(map[*volume.Spec]bool)
	nodesVerificationMap, err := attacher.BulkVerifyVolumes(volumeNodeMap)
	if err != nil {
		glog.Errorf("Attacher.VolumesAreAttached - error checking volumes for node %q with %v", nodeName, err)
		return nodeVolumesResult, err
	}
	if result, ok := nodesVerificationMap[nodeName]; ok {
		return result, nil
	}
	return nodeVolumesResult, nil
}

func (attacher *vsphereVMDKAttacher) BulkVerifyVolumes(volumeSpecsByNode map[types.NodeName][]*volume.Spec) (map[types.NodeName]map[*volume.Spec]bool, error) {
	glog.V(5).Infof("vSphere: BulkVerifyVolumes Request %s", spew.Sdump(volumeSpecsByNode))
	volumesAttachedCheck := make(map[types.NodeName]map[*volume.Spec]bool)
	volumeIdsByNode := make(map[types.NodeName][]*vsphere.VolumeID)
	volumeSpecMap := make(map[string]*volume.Spec)

	for nodeName, volumeSpecs := range volumeSpecsByNode {
		for _, volumeSpec := range volumeSpecs {
			volumeSource, _, err := getVolumeSource(volumeSpec)
			if err != nil {
				glog.Errorf("Error getting volume (%q) source : %v", volumeSpec.Name(), err)
				continue
			}
			volumeIdsByNode[nodeName] = append(volumeIdsByNode[nodeName], &vsphere.VolumeID{
				ID:           volumeSource.VolumeID,
				DatastoreURL: volumeSource.DatastoreURL,
				VolumePath:   volumeSource.VolumePath,
			})
			nodeVolume, nodeVolumeExists := volumesAttachedCheck[nodeName]
			if !nodeVolumeExists {
				nodeVolume = make(map[*volume.Spec]bool)
			}
			nodeVolume[volumeSpec] = true
			if volumeSource.VolumeID != "" {
				volumeSpecMap[volumeSource.VolumeID] = volumeSpec
			} else {
				volumeSpecMap[volumeSource.VolumePath] = volumeSpec
			}
			volumesAttachedCheck[nodeName] = nodeVolume
		}
	}
	attachedResult, err := attacher.vsphereVolumes.VolumesAreAttached(volumeIdsByNode)
	if err != nil {
		glog.Errorf("Error checking if volumes are attached to nodes: %+v. err: %v", volumeIdsByNode, err)
		return volumesAttachedCheck, err
	}

	for nodeName, nodeVolumes := range attachedResult {
		for volumeID, attached := range nodeVolumes {
			if !attached {
				var spec *volume.Spec
				if volumeID.ID != "" {
					spec = volumeSpecMap[volumeID.ID]
				} else {
					spec = volumeSpecMap[volumeID.VolumePath]
				}
				setNodeVolume(volumesAttachedCheck, spec, nodeName, false)
			}
		}
	}
	glog.V(5).Infof("vSphere: BulkVerifyVolumes Result: %s", spew.Sdump(volumesAttachedCheck))
	return volumesAttachedCheck, nil
}

func (attacher *vsphereVMDKAttacher) WaitForAttach(spec *volume.Spec, devicePath string, _ *v1.Pod, timeout time.Duration) (string, error) {
	volumeSource, _, err := getVolumeSource(spec)
	if err != nil {
		return "", err
	}

	if devicePath == "" {
		return "", fmt.Errorf("WaitForAttach failed for volume %+v: devicePath is empty.", volumeSource)
	}

	ticker := time.NewTicker(checkSleepDuration)
	defer ticker.Stop()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		select {
		case <-ticker.C:
			glog.V(5).Infof("Checking volume %+v is attached", volumeSource)
			path, err := verifyDevicePath(devicePath)
			if err != nil {
				// Log error, if any, and continue checking periodically. See issue #11321
				glog.Warningf("Error verifying volume (%+v) is attached: %v", volumeSource, err)
			} else if path != "" {
				// A device path has successfully been created for the VMDK
				glog.V(5).Infof("device path: %s", path)
				glog.Infof("Successfully found attached volume %+v.", volumeSource)
				return path, nil
			}
		case <-timer.C:
			return "", fmt.Errorf("Could not find attached volume %+v. Timeout waiting for mount paths to be created.", volumeSource)
		}
	}
}

// GetDeviceMountPath returns a path where the device should
// point which should be bind mounted for individual volumes.
func (attacher *vsphereVMDKAttacher) GetDeviceMountPath(spec *volume.Spec) (string, error) {
	volumeSource, _, err := getVolumeSource(spec)
	if err != nil {
		return "", err
	}
	var devPath string
	if volumeSource.VolumeID == "" {
		// When volumeSource.VolumeID is not set, PV is not backed for FCD.
		// For this case devPath would be volumeSource.VolumePath e.g
		// [VSANDatastore] kubevols/kubernetes-dynamic-pvc-83295256-f8e0-11e6-8263-005056b2349c.vmdk
		devPath = volumeSource.VolumePath
	} else {
		// When volumeSource.DatastoreURL=ds:///vmfs/volumes/vsan:5297ff7a8bd7f817-47d91c95e6c1a77a/ and
		// volumeSource.VolumeID = a011f917-0f68-4ac1-a338-9c573bfd7dd5
		// devPath would be [vsan:5297ff7a8bd7f817-47d91c95e6c1a77a] a011f917-0f68-4ac1-a338-9c573bfd7dd5
		devPath = "[" + filepath.Base(volumeSource.DatastoreURL) + "] " + volumeSource.VolumeID
	}
	globalPDPath := makeGlobalPDPath(attacher.host, devPath)
	glog.V(5).Info("Mount path for the volume: %+v is set to: %s", volumeSource, globalPDPath)
	return globalPDPath, nil
}

// GetMountDeviceRefs finds all other references to the device referenced
// by deviceMountPath; returns a list of paths.
func (plugin *vsphereVolumePlugin) GetDeviceMountRefs(deviceMountPath string) ([]string, error) {
	mounter := plugin.host.GetMounter(plugin.GetPluginName())
	return mounter.GetMountRefs(deviceMountPath)
}

// MountDevice mounts device to global mount point.
func (attacher *vsphereVMDKAttacher) MountDevice(spec *volume.Spec, devicePath string, deviceMountPath string) error {
	mounter := attacher.host.GetMounter(vsphereVolumePluginName)
	notMnt, err := mounter.IsLikelyNotMountPoint(deviceMountPath)
	if err != nil {
		if os.IsNotExist(err) {
			if err := os.MkdirAll(deviceMountPath, 0750); err != nil {
				glog.Errorf("Failed to create directory at %#v. err: %+v", deviceMountPath, err)
				return err
			}
			notMnt = true
		} else {
			return err
		}
	}

	volumeSource, _, err := getVolumeSource(spec)
	if err != nil {
		return err
	}

	options := []string{}

	if notMnt {
		diskMounter := volumeutil.NewSafeFormatAndMountFromHost(vsphereVolumePluginName, attacher.host)
		mountOptions := volumeutil.MountOptionFromSpec(spec, options...)
		err = diskMounter.FormatAndMount(devicePath, deviceMountPath, volumeSource.FSType, mountOptions)
		if err != nil {
			glog.Errorf("Failed to format and mount. Device Path: %s, Mount Path: %s. Error: %+v", devicePath, deviceMountPath, err)
			os.Remove(deviceMountPath)
			return err
		}
		glog.V(4).Infof("formatting spec %v devicePath %v deviceMountPath %v fs %v with options %+v", spec.Name(), devicePath, deviceMountPath, volumeSource.FSType, options)
	}
	glog.V(5).Infof("Successfully mounted device: %s at mount path: %s", devicePath, deviceMountPath)
	return nil
}

type vsphereVMDKDetacher struct {
	mounter        mount.Interface
	vsphereVolumes vsphere.CommonVolumes
}

var _ volume.Detacher = &vsphereVMDKDetacher{}

var _ volume.DeviceUnmounter = &vsphereVMDKDetacher{}

func (plugin *vsphereVolumePlugin) NewDetacher() (volume.Detacher, error) {
	vsphereCloud, err := getCloudProvider(plugin.host.GetCloudProvider())
	if err != nil {
		return nil, err
	}

	return &vsphereVMDKDetacher{
		mounter:        plugin.host.GetMounter(plugin.GetPluginName()),
		vsphereVolumes: vsphereCloud,
	}, nil
}

func (plugin *vsphereVolumePlugin) NewDeviceUnmounter() (volume.DeviceUnmounter, error) {
	return plugin.NewDetacher()
}

// Detach the given device from the given node.
func (detacher *vsphereVMDKDetacher) Detach(volumeName string, nodeName types.NodeName) error {
	glog.V(4).Infof("vSphere: Detaching volume: %s from node %s", volumeName, nodeName)
	volIdOrPath := getVolIdOrPathfromMountPath(volumeName)
	glog.V(5).Infof("Volume Id or Path from volumeName %s", volIdOrPath)
	var volumeID vsphere.VolumeID
	volumeId := getVolumeID(volIdOrPath)
	attached, err := detacher.vsphereVolumes.VolumesIsAttached(volumeId, nodeName)
	if err != nil {
		// Log error and continue with detach
		glog.Errorf(
			"Error checking if volume (%q) is already attached to current node (%q). Will continue and try detach anyway. err=%v",
			volIdOrPath, nodeName, err)
	}
	if err == nil && !attached {
		// Volume is already detached from node.
		glog.Infof("detach operation was successful. volume %q is already detached from node %q.", volIdOrPath, nodeName)
		return nil
	}

	attachdetachMutex.LockKey(string(nodeName))
	defer attachdetachMutex.UnlockKey(string(nodeName))
	if err := detacher.vsphereVolumes.DetachVSphereVolume(&vsphere.DetachVolumeSpec{
		VolID:    volumeID,
		NodeName: nodeName,
	}); err != nil {
		glog.Errorf("Error detaching volume %q from node %q: %v", volIdOrPath, nodeName, err)
		return err
	}
	glog.V(4).Infof("vSphere: Successfully detached volume: %s from node: %s", volumeName, nodeName)
	return nil
}

func (detacher *vsphereVMDKDetacher) UnmountDevice(deviceMountPath string) error {
	glog.V(4).Infof("vSphere: un-mounting device: %s", deviceMountPath)
	return volumeutil.UnmountPath(deviceMountPath, detacher.mounter)
}

func setNodeVolume(
	nodeVolumeMap map[types.NodeName]map[*volume.Spec]bool,
	volumeSpec *volume.Spec,
	nodeName types.NodeName,
	check bool) {

	volumeMap := nodeVolumeMap[nodeName]
	if volumeMap == nil {
		volumeMap = make(map[*volume.Spec]bool)
		nodeVolumeMap[nodeName] = volumeMap
	}
	volumeMap[volumeSpec] = check
}
