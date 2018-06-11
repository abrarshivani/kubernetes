package vsphere

import (
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/kubernetes/pkg/cloudprovider/providers/vsphere/vclib"
)

type VolumeID struct {
	ID string
	DatastoreURL string
}

type AttachVolumeSpec struct {
	VolID  	  VolumeID
	NodeName  k8stypes.NodeName
	StoragePolicyName string
}

type DetachVolumeSpec struct {
	VolID  	  VolumeID
	NodeName  k8stypes.NodeName
}

type DeleteVolumeSpec struct {
	VolID  	  VolumeID
}

type CreateVolumeSpec struct {
	*vclib.VolumeOptions
}

// Manager provides functionality to manage volumes.
type CommonVolumes interface {
	// CreateVolume creates a new volume given its spec.
	CreateVSphereVolume(spec *CreateVolumeSpec) (VolumeID, error)
	// AttachVolume attaches a volume to a virtual machine given the spec.
	AttachVSphereVolume(spec *AttachVolumeSpec) (string, error)
	// DetachVolume detaches a volume from the virtual machine given the spec.
	DetachVSphereVolume(spec *DetachVolumeSpec) error
	// DeleteVolume deletes a volume given its spec.
	DeleteVSphereVolume(spec *DeleteVolumeSpec) error
	// VolumesAreAttached checks if a list disks are attached to the given node.
	// Assumption: If node doesn't exist, disks are not attached to the node.
	VolumesIsAttached(volumeID VolumeID, nodeName k8stypes.NodeName) (bool, error)
	// VolumesAreAttached checks if a list disks are attached to the given node.
	// Assumption: If node doesn't exist, disks are not attached to the node.
	VolumesAreAttached(nodeVolumes map[k8stypes.NodeName][]*VolumeID) (map[k8stypes.NodeName]map[*VolumeID]bool, error)
}


