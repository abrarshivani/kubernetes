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

package vsphere

import (
	"errors"
	"fmt"
	"gopkg.in/gcfg.v1"
	"io"
	"net"
	"os"
	"path"
	"runtime"
	"strings"
	"sync"

	"github.com/golang/glog"
	"golang.org/x/net/context"
	"k8s.io/api/core/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
	v1helper "k8s.io/kubernetes/pkg/apis/core/v1/helper"
	"k8s.io/kubernetes/pkg/cloudprovider"
	"k8s.io/kubernetes/pkg/cloudprovider/providers/vsphere/vclib"
	"k8s.io/kubernetes/pkg/controller"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/record"
	v1core "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
)

// VSphere Cloud Provider constants
const (
	ProviderName                  = "vsphere"
	VolDir                        = "kubevols"
	RoundTripperDefaultCount      = 3
	DummyVMPrefixName             = "vsphere-k8s"
	MacOuiVC                      = "00:50:56"
	MacOuiEsx                     = "00:0c:29"
	CleanUpDummyVMRoutineInterval = 5
)

var cleanUpRoutineInitialized = false
var datastoreFolderIDMap = make(map[string]map[string]string)

var cleanUpRoutineInitLock sync.Mutex
var cleanUpDummyVMLock sync.RWMutex

// VSphere is an implementation of cloud provider Interface for VSphere.
type VSphere struct {
	cfg      *VSphereConfig
	hostName string
	// Maps the VSphere IP address to VSphereInstance
	vsphereInstanceMap map[string]*VSphereInstance
	vmUUID             string
	clientBuilder      controller.ControllerClientBuilder
	kubeClient         clientset.Interface
	eventBroadcaster   record.EventBroadcaster
	eventRecorder      record.EventRecorder
}

// Represents a vSphere instance where one or more kubernetes nodes are running.
type VSphereInstance struct {
	conn *vclib.VSphereConnection
	cfg  *VirtualCenterConfig
}

// Structure that represents Virtual Center configuration
type VirtualCenterConfig struct {
	// vCenter username.
	User string `gcfg:"user"`
	// vCenter password in clear text.
	Password string `gcfg:"password"`
	// vCenter port.
	VCenterPort string `gcfg:"port"`
	// Datacenter in which VMs are located.
	Datacenters string `gcfg:"datacenters"`
	// Soap round tripper count (retries = RoundTripper - 1)
	RoundTripperCount uint `gcfg:"soap-roundtrip-count"`
}

// Structure that represents the content of vsphere.conf file.
// Users specify the configuration of one or more Virtual Centers in vsphere.conf where
// the Kubernetes master and worker nodes are running.
type VSphereConfig struct {
	Global struct {
		// vCenter username.
		User string `gcfg:"user"`
		// vCenter password in clear text.
		Password string `gcfg:"password"`
		// Deprecated. Use VirtualCenter to specify multiple vCenter Servers.
		// vCenter IP.
		VCenterIP string `gcfg:"server"`
		// vCenter port.
		VCenterPort string `gcfg:"port"`
		// True if vCenter uses self-signed cert.
		InsecureFlag bool `gcfg:"insecure-flag"`
		// Datacenter in which VMs are located.
		// Deprecated. Use "datacenters" instead.
		Datacenter string `gcfg:"datacenter"`
		// Datacenter in which VMs are located.
		Datacenters string `gcfg:"datacenters"`
		// Datastore in which vmdks are stored.
		// Deprecated. See Workspace.DefaultDatastore
		DefaultDatastore string `gcfg:"datastore"`
		// WorkingDir is path where VMs can be found. Also used to create dummy VMs.
		// Deprecated.
		WorkingDir string `gcfg:"working-dir"`
		// Soap round tripper count (retries = RoundTripper - 1)
		RoundTripperCount uint `gcfg:"soap-roundtrip-count"`
		// Deprecated as the virtual machines will be automatically discovered.
		// VMUUID is the VM Instance UUID of virtual machine which can be retrieved from instanceUuid
		// property in VmConfigInfo, or also set as vc.uuid in VMX file.
		// If not set, will be fetched from the machine via sysfs (requires root)
		VMUUID string `gcfg:"vm-uuid"`
		// Deprecated as virtual machine will be automatically discovered.
		// VMName is the VM name of virtual machine
		// Combining the WorkingDir and VMName can form a unique InstanceID.
		// When vm-name is set, no username/password is required on worker nodes.
		VMName string `gcfg:"vm-name"`
		CNS    bool   `gcfg:"cns"`
	}

	VirtualCenter map[string]*VirtualCenterConfig

	Network struct {
		// PublicNetwork is name of the network the VMs are joined to.
		PublicNetwork string `gcfg:"public-network"`
	}

	Disk struct {
		// SCSIControllerType defines SCSI controller to be used.
		SCSIControllerType string `dcfg:"scsicontrollertype"`
	}

	// Endpoint used to create volumes
	Workspace struct {
		VCenterIP        string `gcfg:"server"`
		Datacenter       string `gcfg:"datacenter"`
		Folder           string `gcfg:"folder"`
		DefaultDatastore string `gcfg:"default-datastore"`
		ResourcePoolPath string `gcfg:"resourcepool-path"`
	}
}

type Volumes interface {
	// AttachDisk attaches given disk to given node. Current node
	// is used when nodeName is empty string.
	AttachDisk(vmDiskPath string, storagePolicyName string, nodeName k8stypes.NodeName) (diskUUID string, err error)

	// DetachDisk detaches given disk to given node. Current node
	// is used when nodeName is empty string.
	// Assumption: If node doesn't exist, disk is already detached from node.
	DetachDisk(volPath string, nodeName k8stypes.NodeName) error

	// DiskIsAttached checks if a disk is attached to the given node.
	// Assumption: If node doesn't exist, disk is not attached to the node.
	DiskIsAttached(volPath string, nodeName k8stypes.NodeName) (bool, error)

	// DisksAreAttached checks if a list disks are attached to the given node.
	// Assumption: If node doesn't exist, disks are not attached to the node.
	DisksAreAttached(nodeVolumes map[k8stypes.NodeName][]string) (map[k8stypes.NodeName]map[string]bool, error)

	// CreateVolume creates a new vmdk with specified parameters.
	CreateVolume(volumeOptions *vclib.VolumeOptions) (volumePath string, err error)

	// DeleteVolume deletes vmdk.
	DeleteVolume(vmDiskPath string) error

	CheckVolumeCompliance(volume *v1.PersistentVolume) error
}

// Parses vSphere cloud config file and stores it into VSphereConfig.
func readConfig(config io.Reader) (VSphereConfig, error) {
	if config == nil {
		err := fmt.Errorf("no vSphere cloud provider config file given")
		return VSphereConfig{}, err
	}

	var cfg VSphereConfig
	err := gcfg.ReadInto(&cfg, config)
	return cfg, err
}

func init() {
	vclib.RegisterMetrics()
	cloudprovider.RegisterCloudProvider(ProviderName, func(config io.Reader) (cloudprovider.Interface, error) {
		// If vSphere.conf file is not present then it is worker node.
		if config == nil {
			return newWorkerNode()
		}
		cfg, err := readConfig(config)
		if err != nil {
			return nil, err
		}
		return newControllerNode(cfg)
	})
}

// Initialize passes a Kubernetes clientBuilder interface to the cloud provider
func (vs *VSphere) Initialize(clientBuilder controller.ControllerClientBuilder) {
	vs.clientBuilder = clientBuilder
	vs.kubeClient = clientBuilder.ClientOrDie("vsphere-cloud-provider")
	vs.eventBroadcaster = record.NewBroadcaster()
	vs.eventBroadcaster.StartRecordingToSink(&v1core.EventSinkImpl{Interface: v1core.New(vs.kubeClient.CoreV1().RESTClient()).Events("")})
	vs.eventRecorder = vs.eventBroadcaster.NewRecorder(scheme.Scheme, v1.EventSource{Component: "vsphere-cloud-provider"})
}

type NodeEvents interface {
	NodeAdded(obj interface{})
	NodeDeleted(obj interface{})
}

// Initialize Node Informers
func SetInformers(nodeEvents NodeEvents, informerFactory informers.SharedInformerFactory) {

	// Only on controller node it is required to register listeners.
	// Register callbacks for node updates
	glog.V(4).Infof("Setting up node informers for vSphere Cloud Provider")
	nodeInformer := informerFactory.Core().V1().Nodes().Informer()
	nodeInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    nodeEvents.NodeAdded,
		DeleteFunc: nodeEvents.NodeDeleted,
	})
	glog.V(4).Infof("Node informers in vSphere cloud provider initialized")
}

// Creates new worker node interface and returns
func newWorkerNode() (cloudprovider.Interface, error) {
	var err error
	vsphere := VSphere{}
	vsphere.hostName, err = os.Hostname()
	if err != nil {
		glog.Errorf("Failed to get hostname. err: %+v", err)
		return nil, err
	}
	vsphere.vmUUID, err = GetVMUUID()
	if err != nil {
		glog.Errorf("Failed to get uuid. err: %+v", err)
		return nil, err
	}
	vs := VCP{VSphere: &vsphere}
	return &vs, nil
}

func populateVsphereInstanceMap(cfg *VSphereConfig) (map[string]*VSphereInstance, error) {
	vsphereInstanceMap := make(map[string]*VSphereInstance)

	// Check if the vsphere.conf is in old format. In this
	// format the cfg.VirtualCenter will be nil or empty.
	if cfg.VirtualCenter == nil || len(cfg.VirtualCenter) == 0 {
		glog.V(4).Infof("Config is not per virtual center and is in old format.")
		if cfg.Global.User == "" {
			glog.Error("Global.User is empty!")
			return nil, errors.New("Global.User is empty!")
		}
		if cfg.Global.Password == "" {
			glog.Error("Global.Password is empty!")
			return nil, errors.New("Global.Password is empty!")
		}
		if cfg.Global.WorkingDir == "" {
			glog.Error("Global.WorkingDir is empty!")
			return nil, errors.New("Global.WorkingDir is empty!")
		}
		if cfg.Global.VCenterIP == "" {
			glog.Error("Global.VCenterIP is empty!")
			return nil, errors.New("Global.VCenterIP is empty!")
		}
		if cfg.Global.Datacenter == "" {
			glog.Error("Global.Datacenter is empty!")
			return nil, errors.New("Global.Datacenter is empty!")
		}
		cfg.Workspace.VCenterIP = cfg.Global.VCenterIP
		cfg.Workspace.Datacenter = cfg.Global.Datacenter
		cfg.Workspace.Folder = cfg.Global.WorkingDir
		cfg.Workspace.DefaultDatastore = cfg.Global.DefaultDatastore

		vcConfig := VirtualCenterConfig{
			User:              cfg.Global.User,
			Password:          cfg.Global.Password,
			VCenterPort:       cfg.Global.VCenterPort,
			Datacenters:       cfg.Global.Datacenter,
			RoundTripperCount: cfg.Global.RoundTripperCount,
		}

		vSphereConn := vclib.VSphereConnection{
			Username:          vcConfig.User,
			Password:          vcConfig.Password,
			Hostname:          cfg.Global.VCenterIP,
			Insecure:          cfg.Global.InsecureFlag,
			RoundTripperCount: vcConfig.RoundTripperCount,
			Port:              vcConfig.VCenterPort,
		}
		vsphereIns := VSphereInstance{
			conn: &vSphereConn,
			cfg:  &vcConfig,
		}
		vsphereInstanceMap[cfg.Global.VCenterIP] = &vsphereIns
	} else {
		if cfg.Workspace.VCenterIP == "" || cfg.Workspace.Folder == "" || cfg.Workspace.Datacenter == "" {
			msg := fmt.Sprintf("All fields in workspace are mandatory."+
				" vsphere.conf does not have the workspace specified correctly. cfg.Workspace: %+v", cfg.Workspace)
			glog.Error(msg)
			return nil, errors.New(msg)
		}
		for vcServer, vcConfig := range cfg.VirtualCenter {
			glog.V(4).Infof("Initializing vc server %s", vcServer)
			if vcServer == "" {
				glog.Error("vsphere.conf does not have the VirtualCenter IP address specified")
				return nil, errors.New("vsphere.conf does not have the VirtualCenter IP address specified")
			}
			if vcConfig.User == "" {
				vcConfig.User = cfg.Global.User
			}
			if vcConfig.Password == "" {
				vcConfig.Password = cfg.Global.Password
			}
			if vcConfig.User == "" {
				msg := fmt.Sprintf("vcConfig.User is empty for vc %s!", vcServer)
				glog.Error(msg)
				return nil, errors.New(msg)
			}
			if vcConfig.Password == "" {
				msg := fmt.Sprintf("vcConfig.Password is empty for vc %s!", vcServer)
				glog.Error(msg)
				return nil, errors.New(msg)
			}
			if vcConfig.VCenterPort == "" {
				vcConfig.VCenterPort = cfg.Global.VCenterPort
			}
			if vcConfig.Datacenters == "" {
				if cfg.Global.Datacenters != "" {
					vcConfig.Datacenters = cfg.Global.Datacenters
				} else {
					// cfg.Global.Datacenter is deprecated, so giving it the last preference.
					vcConfig.Datacenters = cfg.Global.Datacenter
				}
			}
			if vcConfig.RoundTripperCount == 0 {
				vcConfig.RoundTripperCount = cfg.Global.RoundTripperCount
			}

			vSphereConn := vclib.VSphereConnection{
				Username:          vcConfig.User,
				Password:          vcConfig.Password,
				Hostname:          vcServer,
				Insecure:          cfg.Global.InsecureFlag,
				RoundTripperCount: vcConfig.RoundTripperCount,
				Port:              vcConfig.VCenterPort,
			}
			vsphereIns := VSphereInstance{
				conn: &vSphereConn,
				cfg:  vcConfig,
			}
			vsphereInstanceMap[vcServer] = &vsphereIns
		}
	}
	return vsphereInstanceMap, nil
}

// Creates new Contreoller node interface and returns
func newControllerNode(cfg VSphereConfig) (cloudprovider.Interface, error) {
	var cloud cloudprovider.Interface
	var err error

	if cfg.Disk.SCSIControllerType == "" {
		cfg.Disk.SCSIControllerType = vclib.PVSCSIControllerType
	} else if !vclib.CheckControllerSupported(cfg.Disk.SCSIControllerType) {
		glog.Errorf("%v is not a supported SCSI Controller type. Please configure 'lsilogic-sas' OR 'pvscsi'", cfg.Disk.SCSIControllerType)
		return nil, errors.New("Controller type not supported. Please configure 'lsilogic-sas' OR 'pvscsi'")
	}
	if cfg.Global.WorkingDir != "" {
		cfg.Global.WorkingDir = path.Clean(cfg.Global.WorkingDir)
	}
	if cfg.Global.RoundTripperCount == 0 {
		cfg.Global.RoundTripperCount = RoundTripperDefaultCount
	}
	if cfg.Global.VCenterPort == "" {
		cfg.Global.VCenterPort = "443"
	}
	vsphereInstanceMap, err := populateVsphereInstanceMap(&cfg)
	if err != nil {
		return nil, err
	}

	vs := VSphere{
		vsphereInstanceMap: vsphereInstanceMap,
		cfg:                &cfg,
	}

	vs.hostName, err = os.Hostname()
	if err != nil {
		glog.Errorf("Failed to get hostname. err: %+v", err)
		return nil, err
	}
	vs.vmUUID, err = GetVMUUID()
	if err != nil {
		glog.Errorf("Failed to get uuid. err: %+v", err)
		return nil, err
	}

	cloud = &VCP{VSphere: &vs,
		nodeManager: &NodeManager{
			vsphereInstanceMap: vsphereInstanceMap,
			nodeInfoMap:        make(map[string]*NodeInfo),
			registeredNodes:    make(map[string]*v1.Node),
		}}

	if cfg.Global.CNS {
		if len(vsphereInstanceMap) > 1 {
			return nil, errors.New("Multiple vCenters is not supported by cns")
		}
		cloud = &CSP{&vs}
	}

	runtime.SetFinalizer(&vs, logout)
	return cloud, nil
}

func logout(vs *VSphere) {
	for _, vsphereIns := range vs.vsphereInstanceMap {
		if vsphereIns.conn.GoVmomiClient != nil {
			vsphereIns.conn.GoVmomiClient.Logout(context.TODO())
		}
	}

}

func getLocalIP() ([]v1.NodeAddress, error) {
	addrs := []v1.NodeAddress{}
	ifaces, err := net.Interfaces()
	if err != nil {
		glog.Errorf("net.Interfaces() failed for NodeAddresses - %v", err)
		return nil, err
	}
	for _, i := range ifaces {
		localAddrs, err := i.Addrs()
		if err != nil {
			glog.Warningf("Failed to extract addresses for NodeAddresses - %v", err)
		} else {
			for _, addr := range localAddrs {
				if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
					if ipnet.IP.To4() != nil {
						// Filter external IP by MAC address OUIs from vCenter and from ESX
						var addressType v1.NodeAddressType
						if strings.HasPrefix(i.HardwareAddr.String(), MacOuiVC) ||
							strings.HasPrefix(i.HardwareAddr.String(), MacOuiEsx) {
							v1helper.AddToNodeAddresses(&addrs,
								v1.NodeAddress{
									Type:    v1.NodeExternalIP,
									Address: ipnet.IP.String(),
								},
								v1.NodeAddress{
									Type:    v1.NodeInternalIP,
									Address: ipnet.IP.String(),
								},
							)
						}
						glog.V(4).Infof("Find local IP address %v and set type to %v", ipnet.IP.String(), addressType)
					}
				}
			}
		}
	}
	return addrs, nil
}

// AddSSHKeyToAllInstances add SSH key to all instances
func (vs *VSphere) AddSSHKeyToAllInstances(ctx context.Context, user string, keyData []byte) error {
	return cloudprovider.NotImplemented
}

// CurrentNodeName gives the current node name
func (vs *VSphere) CurrentNodeName(ctx context.Context, hostname string) (k8stypes.NodeName, error) {
	return convertToK8sType(vs.hostName), nil
}

func convertToString(nodeName k8stypes.NodeName) string {
	return string(nodeName)
}

func convertToK8sType(vmName string) k8stypes.NodeName {
	return k8stypes.NodeName(vmName)
}

// InstanceTypeByProviderID returns the cloudprovider instance type of the node with the specified unique providerID
// This method will not be called from the node that is requesting this ID. i.e. metadata service
// and other local methods cannot be used here
func (vs *VSphere) InstanceTypeByProviderID(ctx context.Context, providerID string) (string, error) {
	return "", nil
}

func (vs *VSphere) InstanceType(ctx context.Context, name k8stypes.NodeName) (string, error) {
	return "", nil
}

func (vs *VSphere) Clusters() (cloudprovider.Clusters, bool) {
	return nil, true
}

// ProviderName returns the cloud provider ID.
func (vs *VSphere) ProviderName() string {
	return ProviderName
}

// LoadBalancer returns an implementation of LoadBalancer for vSphere.
func (vs *VSphere) LoadBalancer() (cloudprovider.LoadBalancer, bool) {
	return nil, false
}

// Zones returns an implementation of Zones for Google vSphere.
func (vs *VSphere) Zones() (cloudprovider.Zones, bool) {
	glog.V(1).Info("The vSphere cloud provider does not support zones")
	return nil, false
}

// Routes returns a false since the interface is not supported for vSphere.
func (vs *VSphere) Routes() (cloudprovider.Routes, bool) {
	return nil, false
}

// HasClusterID returns true if the cluster has a clusterID
func (vs *VSphere) HasClusterID() bool {
	return true
}

func (vs *VSphere) CheckVolumeCompliance(volume *v1.PersistentVolume) error {
	vs.eventRecorder.Event(volume.Spec.ClaimRef, v1.EventTypeWarning, "ComplianceChange", volume.Name + "compliance check")
	return nil
}