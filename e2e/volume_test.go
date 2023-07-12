// Copyright 2022 OnMetal authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package e2e

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	//"sigs.k8s.io/controller-runtime/pkg/client"

	corev1alpha1 "github.com/onmetal/onmetal-api/api/core/v1alpha1"
	storagev1alpha1 "github.com/onmetal/onmetal-api/api/storage/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	. "github.com/onsi/gomega"
	//. "sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	//"github.com/onmetal/cephlet/pkg/rook"
	//"github.com/onmetal/controller-utils/clientutils"


)

var _ = Describe("cephlet-volume", func() {


	/*var (
	 	volumeClass *storagev1alpha1.VolumeClass
	 	//volumePool  *storagev1alpha1.VolumePool
		//rookConfig                 *rook.Config
		//volumePoolSecretAnnotation = "ceph-client-secret-name"
	 )*/

	const (
		volumeSize = "10Gi"
	//	cephClientSecretValue = "test"
	//	snapshotSize  = "2Gi"
	//	cephPoolName  = "ceph"
	//	cephImageName = "image-1"
	)


	It("should create volume", func(ctx SpecContext) {
		By("checking that a Volume has been created")
		vol := &storagev1alpha1.Volume{
			ObjectMeta: metav1.ObjectMeta{
				Name: "tsi",
				Namespace: "rook-ceph",
			},
			Spec: storagev1alpha1.VolumeSpec{
				VolumeClassRef: &corev1.LocalObjectReference{Name: "fast"},
				VolumePoolRef:  &corev1.LocalObjectReference{Name: "ceph"},
				Resources: corev1alpha1.ResourceList{
					corev1alpha1.ResourceStorage: resource.MustParse(volumeSize),
				},
			Image: "ghcr.io/onmetal/onmetal-image/gardenlinux:rootfs-dev-20230223",
			},
		}
		Expect(k8sClient.Create(ctx, vol)).To(Succeed())
		fmt.Println("Here the Volume is getting created############")
		fmt.Println("Volume created name is ", vol.Name)
	})

	It("Should get the volume", func(ctx SpecContext) {		
		volume := &storagev1alpha1.Volume{}
		ns := types.NamespacedName{Namespace: "rook-ceph", Name: "tsi"}
		Expect(k8sClient.Get(ctx, ns, volume)).To(Succeed())
		fmt.Println("Here the Volume is getting listed##############")
		fmt.Println("Volume listed as :", volume.Name)

		// Todo use matcher
		//Expect(volume.Name).To(Succeed())

		//Expect(k8sClient.List(ctx, volumeList, client.InNamespace("rook-ceph"))).To(Succeed())
	})

	It("Should delete volume", func(ctx SpecContext) {

		volume := &storagev1alpha1.Volume{}
		//volumeList := &storagev1alpha1.vol
		ns := types.NamespacedName{Namespace: "rook-ceph", Name: "tsi"}
		err := k8sClient.Get(ctx, ns, volume)
		if err != nil {

		}
		deleteResult := k8sClient.Delete(ctx, volume)
		//fmt.Println(deleteResult)
		Expect(deleteResult).To(Succeed())
		fmt.Println("Here the Volume is getting deleted which was ealier created.###########")
		fmt.Println("Deleted Volume name is ",volume.Name)

	})

})

