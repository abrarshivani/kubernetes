/*
Copyright 2017 The Kubernetes Authors.
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
	"fmt"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"k8s.io/api/core/v1"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/kubernetes/test/e2e/framework"
	"k8s.io/kubernetes/test/e2e/storage/utils"
)

/*
   Test to verify diskformat specified in storage-class is being honored while volume creation.
   Valid and supported options are eagerzeroedthick, zeroedthick and thin

   Steps
   1. Create StorageClass with diskformat set to valid type
   2. Create PVC which uses the StorageClass created in step 1.
   3. Wait for PV to be provisioned.
   4. Wait for PVC's status to become Bound
*/

var _ = utils.SIGDescribe("Volume Create [Feature:csp]", func() {
	f := framework.NewDefaultFramework("volume-disk-create")

	var (
		client    clientset.Interface
		namespace string
		nodeInfo  *NodeInfo
	)

	BeforeEach(func() {
		framework.SkipUnlessProviderIs("vsphere")
		Bootstrap(f)
		client = f.ClientSet
		namespace = f.Namespace.Name
		nodes := framework.GetReadySchedulableNodesOrDie(client)
		nodeInfo = TestContext.NodeMapper.GetNodeInfo(nodes.Items[0].Name)
	})

	It("Basic csp create volume validation", func() {
		By("Invoking Test for verifying volume create")
		verifyDiskCreate(f, client, namespace, nodeInfo)
	})
})

func verifyDiskCreate(f *framework.Framework, client clientset.Interface, namespace string, nodeInfo *NodeInfo) {
	scParameters := make(map[string]string)
	scParameters["diskformat"] = "thin"

	By("Creating Storage Class")
	storageClassSpec := getVSphereStorageClassSpec("testsc", scParameters)
	storageclass, err := client.StorageV1().StorageClasses().Create(storageClassSpec)
	Expect(err).NotTo(HaveOccurred())
	defer client.StorageV1().StorageClasses().Delete(storageclass.Name, nil)
	By("Creating PVC using the Storage Class")
	pvclaimSpec := getVSphereClaimSpecWithStorageClass(namespace, "2Gi", storageclass)
	pvclaim, err := client.CoreV1().PersistentVolumeClaims(namespace).Create(pvclaimSpec)
	Expect(err).NotTo(HaveOccurred())

	defer func() {
		client.CoreV1().PersistentVolumeClaims(namespace).Delete(pvclaimSpec.Name, nil)
	}()

	var pvclaims []*v1.PersistentVolumeClaim
	pvclaims = append(pvclaims, pvclaim)
	By("Waiting for claim to be in bound phase")
	persistentvolumes, err := framework.WaitForPVClaimBoundPhase(client, pvclaims, framework.ClaimProvisionTimeout)
	Expect(err).NotTo(HaveOccurred())
	By("Calling CNS Query Api")
	By(fmt.Sprintf("Invoking QueryCNSVolume with VolumeID: %s, and DatastoreURL: %s", persistentvolumes[0].Spec.VsphereVolume.VolumeID, persistentvolumes[0].Spec.VsphereVolume.DatastoreURL))
	err = nodeInfo.VSphere.QueryCNSVolume(persistentvolumes[0].Spec.VsphereVolume.VolumeID, persistentvolumes[0].Spec.VsphereVolume.DatastoreURL)
	Expect(err).NotTo(HaveOccurred())
}
