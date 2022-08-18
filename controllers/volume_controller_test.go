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

package controllers

import (
	"fmt"

	storagev1alpha1 "github.com/onmetal/onmetal-api/apis/storage/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	rookv1 "github.com/rook/rook/pkg/apis/ceph.rook.io/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("VolumeReconciler", func() {
	testNs, rookNs := SetupTest(ctx)

	var (
		volumeClass *storagev1alpha1.VolumeClass
	)

	BeforeEach(func() {
		volumeClass = &storagev1alpha1.VolumeClass{
			TypeMeta: metav1.TypeMeta{},
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "sc-",
			},
		}
		Expect(k8sClient.Create(ctx, volumeClass)).To(Succeed())
	})

	It("should reconcile volume", func() {
		volumeSize := "1Gi"
		cephPoolName := "pool-1"
		cephImageName := "image-1"

		By("checking that a Volume has been created")
		vol := &storagev1alpha1.Volume{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "volume-",
				Namespace:    testNs.Name,
			},
			Spec: storagev1alpha1.VolumeSpec{
				VolumeClassRef: corev1.LocalObjectReference{Name: volumeClass.Name},
				VolumePoolRef:  &corev1.LocalObjectReference{Name: volumePoolName},
				Resources: corev1.ResourceList{
					"storage": resource.MustParse(volumeSize),
				},
			},
		}
		Expect(k8sClient.Create(ctx, vol)).To(Succeed())
		volKey := types.NamespacedName{Namespace: vol.Namespace, Name: vol.Name}

		By("checking that the ceph namespace has been created")
		cephNs := &rookv1.CephBlockPoolRadosNamespace{}
		cephNsKey := types.NamespacedName{Namespace: rookNs.Name, Name: testNs.Name}
		Eventually(func() error { return k8sClient.Get(ctx, cephNsKey, cephNs) }).Should(Succeed())

		By("updating the ceph namespace status to ready")
		cephNsBase := cephNs.DeepCopy()
		cephNs.Status = &rookv1.CephBlockPoolRadosNamespaceStatus{
			Phase: rookv1.ConditionReady,
			Info: map[string]string{
				"clusterID": "test-cluster-id",
			},
		}
		Expect(k8sClient.Status().Patch(ctx, cephNs, client.MergeFrom(cephNsBase))).To(Succeed())

		By("checking that the pvc has been created")
		pvc := &corev1.PersistentVolumeClaim{}
		pvcKey := types.NamespacedName{Namespace: vol.Namespace, Name: vol.Name}
		Eventually(func() error { return k8sClient.Get(ctx, pvcKey, pvc) }).Should(Succeed())

		By("creating the pv for pvc")
		pv := &corev1.PersistentVolume{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "pv-",
				Namespace:    pvc.Namespace,
			},
			Spec: corev1.PersistentVolumeSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{
					corev1.ReadWriteOnce,
				},
				Capacity: map[corev1.ResourceName]resource.Quantity{
					corev1.ResourceStorage: resource.MustParse("1Gi"),
				},
				PersistentVolumeSource: corev1.PersistentVolumeSource{
					CSI: &corev1.CSIPersistentVolumeSource{
						Driver:       "rook-ceph.rbd.csi.ceph.com",
						VolumeHandle: "handle",
						VolumeAttributes: map[string]string{
							"pool":      cephPoolName,
							"imageName": cephImageName,
						},
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, pv)).To(Succeed())

		pvcBase := pvc.DeepCopy()
		pvc.Spec.VolumeName = pv.Name
		Expect(k8sClient.Patch(ctx, pvc, client.MergeFrom(pvcBase)))

		By("checking that the ceph client has been created")
		cephClient := &rookv1.CephClient{}
		cephClientKey := types.NamespacedName{Namespace: rookNs.Name, Name: testNs.Name}
		Eventually(func() error { return k8sClient.Get(ctx, cephClientKey, cephClient) }).Should(Succeed())

		rookVolumeSecret := "ceph secret"
		By("creating the ceph client secret")
		cephClientSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "secret-",
				Namespace:    rookNs.Name,
			},
			Data: map[string][]byte{
				testNs.Name: []byte(rookVolumeSecret),
			},
		}
		Expect(k8sClient.Create(ctx, cephClientSecret)).To(Succeed())

		By("updating the ceph client status to ready")
		cephClientBase := cephClient.DeepCopy()
		cephClient.Status = &rookv1.CephClientStatus{
			Phase: rookv1.ConditionReady,
			Info: map[string]string{
				"secretName": cephClientSecret.Name,
			},
		}
		Expect(k8sClient.Status().Patch(ctx, cephClient, client.MergeFrom(cephClientBase))).To(Succeed())

		By("checking that the volume status has been updated")
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, volKey, vol)).To(Succeed())
			g.Expect(vol.Status.Access).NotTo(BeNil())
			g.Expect(vol.Status.Access.SecretRef).NotTo(BeNil())
		}).Should(Succeed())

		By("checking that the volume access attributes are correct")
		Expect(vol.Status.Access.Driver).To(BeEquivalentTo("ceph"))
		Expect(vol.Status.Access.VolumeAttributes).NotTo(BeNil())
		Expect(vol.Status.Access.VolumeAttributes["image"]).To(BeEquivalentTo(fmt.Sprintf("%s/%s", cephPoolName, cephImageName)))
		Expect(vol.Status.Access.VolumeAttributes["monitors"]).NotTo(BeEmpty())
		Expect(vol.Status.Access.VolumeAttributes["WWN"]).NotTo(BeEmpty())

		accessSecret := &corev1.Secret{}
		accessSecretKey := types.NamespacedName{Namespace: vol.Namespace, Name: vol.Status.Access.SecretRef.Name}
		Expect(k8sClient.Get(ctx, accessSecretKey, accessSecret)).To(Succeed())

		Expect(accessSecret.Data).NotTo(BeNil())
		Expect(accessSecret.Data["userID"]).To(BeEquivalentTo(testNs.Name))
		Expect(accessSecret.Data["userKey"]).To(BeEquivalentTo(rookVolumeSecret))
	})

})
