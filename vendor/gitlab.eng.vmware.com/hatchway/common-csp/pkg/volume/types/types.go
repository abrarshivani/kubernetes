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

package types

import (
	"gitlab.eng.vmware.com/hatchway/common-csp/pkg/vsphere"
)

// BackingObjectInfo is the base for defining a volume backing option.
type BackingObjectInfo struct {
	// StoragePolicyID is the ID of the storage policy.
	StoragePolicyID string
	// Capacity is the volume capacity specified in MBs.
	Capacity uint64
}

// BlockBackingInfo represents a block volume backing info.
type BlockBackingInfo struct {
	// BackingObjectInfo is the base for BlockVolumeOption.
	BackingObjectInfo
	// BackingDiskID represents the ID of the disk to be used.
	BackingDiskID string
}

// ContainerCluster uniquely identifies a container orchestrator cluster.
type ContainerCluster struct {
	// ClusterID represents the cluster ID.
	ClusterID string
	// ClusterType represents the cluster type.
	ClusterType ClusterType
	// VSphereUser represents the vSphere user corresponding to the cluster user.
	VSphereUser string
}

// VolumeID uniquely identifies a volume.
type VolumeID struct {
	// ID represents the volume ID.
	ID string
	// DatastoreURL is the datastore path where the volume resides.
	DatastoreURL string
}

// CreateSpec holds the specification for creating a volume.
type CreateSpec struct {
	// Name represents the volume name.
	Name string
	// ContainerCluster is the container cluster for which the volume is to be created.
	ContainerCluster ContainerCluster
	// DatastoreURLs represent the datastores to be considered for volume placement.
	DatastoreURLs []string
	// Labels represent the labels for the volume.
	Labels map[string]string
	// BackingInfo represents the backing object information for volume creation.
	BackingInfo BaseBackingObjectInfo
}

// AttachDetachSpec holds the specification for attaching/detaching a volume.
type AttachDetachSpec struct {
	// VolumeID uniquely identifies a volume.
	VolumeID *VolumeID
	// VirtualMachine is the virtual machine instance to which the volume needs
	// to be attached.
	VirtualMachine *vsphere.VirtualMachine
}

// UpdateSpec holds the specification for updating a volume.
type UpdateSpec struct {
	// VolumeID uniquely identifies a volume.
	VolumeID *VolumeID
	// Labels represent the labels for the volume.
	Labels map[string]string
	// Capacity is the new volume capacity specified in MBs.
	Capacity uint64
}

// DeleteSpec holds the specification for deleting a volume.
type DeleteSpec struct {
	// VolumeID uniquely identifies a volume.
	VolumeID *VolumeID
}

// QuerySpec holds the specification for querying a container volume.
type QuerySpec struct {
	// VolumeID uniquely identifies a volume.
	VolumeID *VolumeID
	// ClusterID represents the cluster ID.
	ClusterID string
}
