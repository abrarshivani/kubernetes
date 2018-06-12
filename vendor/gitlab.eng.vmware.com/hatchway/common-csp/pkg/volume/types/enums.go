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

// ProvisioningType is the type of block volume.
type ProvisioningType string

const (
	// ProvisioningTypeTHIN represents a disk for which space is allocated and
	// zeroed on demand.
	ProvisioningTypeTHIN = ProvisioningType("THIN")
	// ProvisioningTypeEAGERZEROEDTHICK represents a disk for which space is
	// allocated and zeroed during provisioning.
	ProvisioningTypeEAGERZEROEDTHICK = ProvisioningType("EAGER_ZEROED_THICK")
	// ProvisioningTypeLAZYZEROEDTHICK represents a disk for which space is
	// allocated during provisioning but not fully wiped.
	ProvisioningTypeLAZYZEROEDTHICK = ProvisioningType("LAZY_ZEROED_THICK")
)

// ClusterType represents a container orchestrator cluster type.
type ClusterType string

const (
	// ClusterTypeKUBERNETES represents a Kubernetes cluster type.
	ClusterTypeKUBERNETES = ClusterType("KUBERNETES")
)
