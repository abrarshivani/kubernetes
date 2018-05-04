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
	"log"
	"os"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/net/context"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/kubernetes/pkg/cloudprovider"
	"k8s.io/kubernetes/pkg/cloudprovider/providers/vsphere/vclib"
)

func configFromEnv() (cfg VSphereConfig, ok bool) {
	var InsecureFlag bool
	var err error
	cfg.Global.VCenterIP = os.Getenv("VSPHERE_VCENTER")
	cfg.Global.VCenterPort = os.Getenv("VSPHERE_VCENTER_PORT")
	cfg.Global.User = os.Getenv("VSPHERE_USER")
	cfg.Global.Password = os.Getenv("VSPHERE_PASSWORD")
	cfg.Global.Datacenter = os.Getenv("VSPHERE_DATACENTER")
	cfg.Network.PublicNetwork = os.Getenv("VSPHERE_PUBLIC_NETWORK")
	cfg.Global.DefaultDatastore = os.Getenv("VSPHERE_DATASTORE")
	cfg.Disk.SCSIControllerType = os.Getenv("VSPHERE_SCSICONTROLLER_TYPE")
	cfg.Global.WorkingDir = os.Getenv("VSPHERE_WORKING_DIR")
	cfg.Global.VMName = os.Getenv("VSPHERE_VM_NAME")
	if os.Getenv("VSPHERE_INSECURE") != "" {
		InsecureFlag, err = strconv.ParseBool(os.Getenv("VSPHERE_INSECURE"))
	} else {
		InsecureFlag = false
	}
	if err != nil {
		log.Fatal(err)
	}
	cfg.Global.InsecureFlag = InsecureFlag

	ok = (cfg.Global.VCenterIP != "" &&
		cfg.Global.User != "")

	return
}

func TestReadConfig(t *testing.T) {
	_, err := readConfig(nil)
	if err == nil {
		t.Errorf("Should fail when no config is provided: %s", err)
	}

	cfg, err := readConfig(strings.NewReader(`
[Global]
server = 0.0.0.0
port = 443
user = user
password = password
insecure-flag = true
datacenter = us-west
vm-uuid = 1234
vm-name = vmname
`))
	if err != nil {
		t.Fatalf("Should succeed when a valid config is provided: %s", err)
	}

	if cfg.Global.VCenterIP != "0.0.0.0" {
		t.Errorf("incorrect vcenter ip: %s", cfg.Global.VCenterIP)
	}

	if cfg.Global.Datacenter != "us-west" {
		t.Errorf("incorrect datacenter: %s", cfg.Global.Datacenter)
	}

	if cfg.Global.VMUUID != "1234" {
		t.Errorf("incorrect vm-uuid: %s", cfg.Global.VMUUID)
	}

	if cfg.Global.VMName != "vmname" {
		t.Errorf("incorrect vm-name: %s", cfg.Global.VMName)
	}
}

func TestNewVSphere(t *testing.T) {
	cfg, ok := configFromEnv()
	if !ok {
		t.Skipf("No config found in environment")
	}

	_, err := newControllerNode(cfg)
	if err != nil {
		t.Fatalf("Failed to construct/authenticate vSphere: %s", err)
	}
}

func TestVSphereLogin(t *testing.T) {
	cfg, ok := configFromEnv()
	if !ok {
		t.Skipf("No config found in environment")
	}

	// Create vSphere configuration object
	vs, err := newControllerNode(cfg)
	if err != nil {
		t.Fatalf("Failed to construct/authenticate vSphere: %s", err)
	}

	// Create context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create vSphere client
	var vcInstance *VSphereInstance
	if vcInstance, ok = vs.vsphereInstanceMap[cfg.Global.VCenterIP]; !ok {
		t.Fatalf("Couldn't get vSphere instance: %s", cfg.Global.VCenterIP)
	}

	err = vcInstance.conn.Connect(ctx)
	if err != nil {
		t.Errorf("Failed to connect to vSphere: %s", err)
	}
	defer vcInstance.conn.GoVmomiClient.Logout(ctx)
}

func TestZones(t *testing.T) {
	cfg := VSphereConfig{}
	cfg.Global.Datacenter = "myDatacenter"

	// Create vSphere configuration object
	vs := VSphere{
		cfg: &cfg,
	}

	_, ok := vs.Zones()
	if ok {
		t.Fatalf("Zones() returned true")
	}
}

func TestInstances(t *testing.T) {
	cfg, ok := configFromEnv()
	if !ok {
		t.Skipf("No config found in environment")
	}

	vs, err := newControllerNode(cfg)
	if err != nil {
		t.Fatalf("Failed to construct/authenticate vSphere: %s", err)
	}

	i, ok := vs.Instances()
	if !ok {
		t.Fatalf("Instances() returned false")
	}

	nodeName, err := vs.CurrentNodeName(context.TODO(), "")
	if err != nil {
		t.Fatalf("CurrentNodeName() failed: %s", err)
	}

	externalID, err := i.ExternalID(context.TODO(), nodeName)
	if err != nil {
		t.Fatalf("Instances.ExternalID(%s) failed: %s", nodeName, err)
	}
	t.Logf("Found ExternalID(%s) = %s\n", nodeName, externalID)

	nonExistingVM := types.NodeName(rand.String(15))
	externalID, err = i.ExternalID(context.TODO(), nonExistingVM)
	if err == cloudprovider.InstanceNotFound {
		t.Logf("VM %s was not found as expected\n", nonExistingVM)
	} else if err == nil {
		t.Fatalf("Instances.ExternalID did not fail as expected, VM %s was found", nonExistingVM)
	} else {
		t.Fatalf("Instances.ExternalID did not fail as expected, err: %v", err)
	}

	instanceID, err := i.InstanceID(context.TODO(), nodeName)
	if err != nil {
		t.Fatalf("Instances.InstanceID(%s) failed: %s", nodeName, err)
	}
	t.Logf("Found InstanceID(%s) = %s\n", nodeName, instanceID)

	instanceID, err = i.InstanceID(context.TODO(), nonExistingVM)
	if err == cloudprovider.InstanceNotFound {
		t.Logf("VM %s was not found as expected\n", nonExistingVM)
	} else if err == nil {
		t.Fatalf("Instances.InstanceID did not fail as expected, VM %s was found", nonExistingVM)
	} else {
		t.Fatalf("Instances.InstanceID did not fail as expected, err: %v", err)
	}

	addrs, err := i.NodeAddresses(context.TODO(), nodeName)
	if err != nil {
		t.Fatalf("Instances.NodeAddresses(%s) failed: %s", nodeName, err)
	}
	t.Logf("Found NodeAddresses(%s) = %s\n", nodeName, addrs)
}

func TestVolumes(t *testing.T) {
	cfg, ok := configFromEnv()
	if !ok {
		t.Skipf("No config found in environment")
	}

	vs, err := newControllerNode(cfg)
	if err != nil {
		t.Fatalf("Failed to construct/authenticate vSphere: %s", err)
	}

	nodeName, err := vs.CurrentNodeName(context.TODO(), "")
	if err != nil {
		t.Fatalf("CurrentNodeName() failed: %s", err)
	}

	volumeOptions := &vclib.VolumeOptions{
		CapacityKB: 1 * 1024 * 1024,
		Tags:       nil,
		Name:       "kubernetes-test-volume-" + rand.String(10),
		DiskFormat: "thin"}

	volPath, err := vs.CreateVolume(volumeOptions)
	if err != nil {
		t.Fatalf("Cannot create a new VMDK volume: %v", err)
	}

	_, err = vs.AttachDisk(volPath, "", "")
	if err != nil {
		t.Fatalf("Cannot attach volume(%s) to VM(%s): %v", volPath, nodeName, err)
	}

	err = vs.DetachDisk(volPath, "")
	if err != nil {
		t.Fatalf("Cannot detach disk(%s) from VM(%s): %v", volPath, nodeName, err)
	}

	// todo: Deleting a volume after detach currently not working through API or UI (vSphere)
	// err = vs.DeleteVolume(volPath)
	// if err != nil {
	// 	t.Fatalf("Cannot delete VMDK volume %s: %v", volPath, err)
	// }
}

func TestSecretVSphereConfig(t *testing.T) {
	var vs *VSphere
	var (
		username = "user"
		password = "password"
	)
	var testcases = []struct {
		testName                 string
		conf                     string
		expectedIsSecretProvided bool
		expectedUsername         string
		expectedPassword         string
		expectedError            error
	}{
		{
			testName: "Username and password with old configuration",
			conf: `[Global]
			server = 0.0.0.0
			user = user
			password = password
			datacenter = us-west
			working-dir = kubernetes
			`,
			expectedUsername: username,
			expectedPassword: password,
			expectedError:    nil,
		},
		{
			testName: "SecretName and SecretNamespace in old configuration",
			conf: `[Global]
			server = 0.0.0.0
			datacenter = us-west
			secret-name = "vccreds"
			secret-namespace = "kube-system"
			working-dir = kubernetes
			`,
			expectedIsSecretProvided: true,
			expectedError:            nil,
		},
		{
			testName: "SecretName and SecretNamespace with Username and Password in old configuration",
			conf: `[Global]
			server = 0.0.0.0
			user = user
			password = password
			datacenter = us-west
			secret-name = "vccreds"
			secret-namespace = "kube-system"
			working-dir = kubernetes
			`,
			expectedIsSecretProvided: true,
			expectedError:            nil,
		},
		{
			testName: "SecretName and SecretNamespace with Username missing in old configuration",
			conf: `[Global]
			server = 0.0.0.0
			password = password
			datacenter = us-west
			secret-name = "vccreds"
			secret-namespace = "kube-system"
			working-dir = kubernetes
			`,
			expectedIsSecretProvided: true,
			expectedError:            nil,
		},
		{
			testName: "SecretNamespace missing with Username and Password in old configuration",
			conf: `[Global]
			server = 0.0.0.0
			user = user
			password = password
			datacenter = us-west
			secret-name = "vccreds"
			working-dir = kubernetes
			`,
			expectedUsername: username,
			expectedPassword: password,
			expectedError:    nil,
		},
		{
			testName: "SecretNamespace and Username missing in old configuration",
			conf: `[Global]
			server = 0.0.0.0
			password = password
			datacenter = us-west
			secret-name = "vccreds"
			working-dir = kubernetes
			`,
			expectedError: ErrUsernameMissing,
		},
		{
			testName: "SecretNamespace and Password missing in old configuration",
			conf: `[Global]
			server = 0.0.0.0
			user = user
			datacenter = us-west
			secret-name = "vccreds"
			working-dir = kubernetes
			`,
			expectedError: ErrPasswordMissing,
		},
		{
			testName: "SecretNamespace, Username and Password missing in old configuration",
			conf: `[Global]
			server = 0.0.0.0
			datacenter = us-west
			secret-name = "vccreds"
			working-dir = kubernetes
			`,
			expectedError: ErrUsernameMissing,
		},
		{
			testName: "Username and password with new configuration but username and password in global section",
			conf: `[Global]
			user = user
			password = password
			datacenter = us-west
			[VirtualCenter "0.0.0.0"]
			[Workspace]
			server = 0.0.0.0
			datacenter = us-west
			folder = kubernetes
			`,
			expectedUsername: username,
			expectedPassword: password,
			expectedError:    nil,
		},
		{
			testName: "Username and password with new configuration, username and password in virtualcenter section",
			conf: `[Global]
			server = 0.0.0.0
			port = 443
			insecure-flag = true
			datacenter = us-west
			[VirtualCenter "0.0.0.0"]
			user = user
			password = password
			[Workspace]
			server = 0.0.0.0
			datacenter = us-west
			folder = kubernetes
			`,
			expectedUsername: username,
			expectedPassword: password,
			expectedError:    nil,
		},
		{
			testName: "SecretName and SecretNamespace with new configuration",
			conf: `[Global]
			server = 0.0.0.0
			secret-name = "vccreds"
			secret-namespace = "kube-system"
			datacenter = us-west
			[VirtualCenter "0.0.0.0"]
			[Workspace]
			server = 0.0.0.0
			datacenter = us-west
			folder = kubernetes
			`,
			expectedIsSecretProvided: true,
			expectedError:            nil,
		},
		{
			testName: "SecretName and SecretNamespace with Username missing in new configuration",
			conf: `[Global]
			server = 0.0.0.0
			port = 443
			insecure-flag = true
			datacenter = us-west
			secret-name = "vccreds"
			secret-namespace = "kube-system"
			[VirtualCenter "0.0.0.0"]
			password = password
			[Workspace]
			server = 0.0.0.0
			datacenter = us-west
			folder = kubernetes
			`,
			expectedIsSecretProvided: true,
			expectedError:            nil,
		},
	}

	for _, testcase := range testcases {
		t.Logf("Executing Testcase: %s", testcase.testName)
		cfg, err := readConfig(strings.NewReader(testcase.conf))
		if err != nil {
			t.Fatalf("Should succeed when a valid config is provided: %s", err)
		}
		vs, err = buildVSphereFromConfig(cfg)
		if err != testcase.expectedError {
			t.Fatalf("Should succeed when a valid config is provided: %s", err)
		}
		if err != nil {
			continue
		}
		if vs.isSecretInfoProvided != testcase.expectedIsSecretProvided {
			t.Fatalf("SecretName and SecretNamespace was expected in config %s. error: %s",
				testcase.conf, err)
		}
		if !testcase.expectedIsSecretProvided {
			for _, vsInstance := range vs.vsphereInstanceMap {
				if vsInstance.conn.Username != testcase.expectedUsername {
					t.Fatalf("Expected username doesn't match actual username in config %s. error: %s",
						testcase.expectedUsername, vsInstance.conn.Username, testcase.conf, err)
				}
				if vsInstance.conn.Password != testcase.expectedPassword {
					t.Fatalf("Expected password doesn't match actual password in config %s. error: %s",
						testcase.expectedPassword, vsInstance.conn.Password, testcase.conf, err)
				}

			}
		}

	}
}
