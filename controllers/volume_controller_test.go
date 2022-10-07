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

	"github.com/onmetal/controller-utils/clientutils"
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
	testNs, rookNs, _ := SetupTest(ctx)

	var (
		volumeClass *storagev1alpha1.VolumeClass
	)

	const (
		volumeSize       = "1Gi"
		cephPoolName     = "pool-1"
		cephImageName    = "image-1"
		rookVolumeSecret = "ceph secret"
	)

	BeforeEach(func() {
		volumeClass = &storagev1alpha1.VolumeClass{
			TypeMeta: metav1.TypeMeta{},
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "sc-",
			},
			Capabilities: map[corev1.ResourceName]resource.Quantity{
				storagev1alpha1.ResourceIOPS: resource.MustParse("100"),
			},
		}
		Expect(k8sClient.Create(ctx, volumeClass)).To(Succeed())
	})

	It("should reconcile volume", func() {
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

		By("checking that the ceph volume is pending")
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, volKey, vol)).To(Succeed())
			g.Expect(vol.Status.State).To(BeEquivalentTo(storagev1alpha1.VolumeStatePending))
		}).Should(Succeed())

		By("checking that the pvc has been created")
		pvc := &corev1.PersistentVolumeClaim{}
		pvcKey := types.NamespacedName{Namespace: vol.Namespace, Name: vol.Name}
		Eventually(func() error { return k8sClient.Get(ctx, pvcKey, pvc) }).Should(Succeed())

		By("creating the pv for pvc")
		pv := getPVSpec(pvc, resource.MustParse("1Gi"), cephPoolName, cephImageName)
		Expect(k8sClient.Create(ctx, pv)).To(Succeed())

		pvcBase := pvc.DeepCopy()
		pvc.Spec.VolumeName = pv.Name
		Expect(k8sClient.Patch(ctx, pvc, client.MergeFrom(pvcBase)))

		pvcBase = pvc.DeepCopy()
		pvc.Status.Phase = corev1.ClaimBound
		Expect(k8sClient.Status().Patch(ctx, pvc, client.MergeFrom(pvcBase)))

		By("checking that the ceph client has been created")
		cephClient := &rookv1.CephClient{}
		cephClientKey := types.NamespacedName{Namespace: rookNs.Name, Name: testNs.Name}
		Eventually(func() error { return k8sClient.Get(ctx, cephClientKey, cephClient) }).Should(Succeed())

		By("creating the ceph client secret")
		cephClientSecret := getCephClientSecret(rookNs.Name, testNs.Name, rookVolumeSecret)
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
			g.Expect(vol.Status.State).To(BeEquivalentTo(storagev1alpha1.VolumeStateAvailable))
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

	It("should reconcile volumes in the same customer ns", func() {
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
		vol2 := vol.DeepCopy()
		Expect(k8sClient.Create(ctx, vol)).To(Succeed())
		Expect(k8sClient.Create(ctx, vol2)).To(Succeed())
		volKey := types.NamespacedName{Namespace: vol.Namespace, Name: vol.Name}
		volKey2 := types.NamespacedName{Namespace: vol2.Namespace, Name: vol2.Name}

		By("checking that the pvc1 has been created and creating corresponding pv ")
		pv := &corev1.PersistentVolume{}
		pvc := &corev1.PersistentVolumeClaim{}
		pvcKey := types.NamespacedName{Namespace: vol.Namespace, Name: vol.Name}
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, pvcKey, pvc)).To(Succeed())

			pv = getPVSpec(pvc, resource.MustParse("1Gi"), cephPoolName, cephImageName)
			g.Expect(clientutils.IgnoreAlreadyExists(k8sClient.Create(ctx, pv))).To(Succeed())

			pvcBase := pvc.DeepCopy()
			pvc.Spec.VolumeName = pv.Name
			g.Expect(k8sClient.Patch(ctx, pvc, client.MergeFrom(pvcBase)))

			pvcBase = pvc.DeepCopy()
			pvc.Status.Phase = corev1.ClaimBound
			g.Expect(k8sClient.Status().Patch(ctx, pvc, client.MergeFrom(pvcBase)))
		}).Should(Succeed())

		By("checking that the pvc2 has been created and creating corresponding pv2 ")
		pv2 := &corev1.PersistentVolume{}
		pvc2 := &corev1.PersistentVolumeClaim{}
		pvcKey2 := types.NamespacedName{Namespace: vol2.Namespace, Name: vol2.Name}
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, pvcKey2, pvc2)).To(Succeed())

			pv2 = getPVSpec(pvc2, resource.MustParse("1Gi"), cephPoolName, cephImageName)
			g.Expect(clientutils.IgnoreAlreadyExists(k8sClient.Create(ctx, pv2))).To(Succeed())

			pvcBase2 := pvc2.DeepCopy()
			pvc2.Spec.VolumeName = pv2.Name
			g.Expect(k8sClient.Patch(ctx, pvc2, client.MergeFrom(pvcBase2)))

			pvcBase2 = pvc2.DeepCopy()
			pvc2.Status.Phase = corev1.ClaimBound
			g.Expect(k8sClient.Status().Patch(ctx, pvc2, client.MergeFrom(pvcBase2)))
		}).Should(Succeed())

		By("checking that the ceph client has been created and updating it to ready")
		cephClientSecret := &corev1.Secret{}
		cephClient := &rookv1.CephClient{}
		cephClientKey := types.NamespacedName{Namespace: rookNs.Name, Name: testNs.Name}
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, cephClientKey, cephClient)).To(Succeed())

			cephClientSecret = getCephClientSecret(rookNs.Name, testNs.Name, rookVolumeSecret)
			g.Expect(clientutils.IgnoreAlreadyExists(k8sClient.Create(ctx, cephClientSecret))).To(Succeed())

			cephClientBase := cephClient.DeepCopy()
			cephClient.Status = &rookv1.CephClientStatus{
				Phase: rookv1.ConditionReady,
				Info: map[string]string{
					"secretName": cephClientSecret.Name,
				},
			}
			g.Expect(k8sClient.Status().Patch(ctx, cephClient, client.MergeFrom(cephClientBase))).To(Succeed())
		}).Should(Succeed())

		By("checking that the volume status has been updated")
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, volKey, vol)).To(Succeed())
			g.Expect(vol.Status.State).To(BeEquivalentTo(storagev1alpha1.VolumeStateAvailable))

			g.Expect(k8sClient.Get(ctx, volKey2, vol2)).To(Succeed())
			g.Expect(vol2.Status.State).To(BeEquivalentTo(storagev1alpha1.VolumeStateAvailable))
		}).Should(Succeed())
	})

	It("should end reconcile volume if pool ref is not valid", func() {
		By("checking that a Volume has been created")
		vol := &storagev1alpha1.Volume{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "volume-",
				Namespace:    testNs.Name,
			},
			Spec: storagev1alpha1.VolumeSpec{
				VolumeClassRef: corev1.LocalObjectReference{Name: "not-there"},
				VolumePoolRef:  &corev1.LocalObjectReference{Name: "not-there"},
				Resources: corev1.ResourceList{
					"storage": resource.MustParse(volumeSize),
				},
			},
		}
		Expect(k8sClient.Create(ctx, vol)).To(Succeed())
		volKey := types.NamespacedName{Namespace: vol.Namespace, Name: vol.Name}

		By("checking that the volume status has been updated")
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, volKey, vol)).To(Succeed())
			g.Expect(vol.Status.State).To(BeEquivalentTo(storagev1alpha1.VolumeStatePending))
		}).Should(Succeed())
	})
})

func getCephClientSecret(rookNs, customerNs, secret string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "secret-",
			Namespace:    rookNs,
		},
		Data: map[string][]byte{
			customerNs: []byte(secret),
		},
	}
}

func getPVSpec(pvc *corev1.PersistentVolumeClaim, size resource.Quantity, cephPoolName, cephImageName string) *corev1.PersistentVolume {
	return &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "pv-",
			Namespace:    pvc.Namespace,
		},
		Spec: corev1.PersistentVolumeSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{
				corev1.ReadWriteOnce,
			},
			Capacity: map[corev1.ResourceName]resource.Quantity{
				corev1.ResourceStorage: size,
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
}
