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

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v6/apis/volumesnapshot/v1"
	"github.com/onmetal/controller-utils/clientutils"
	storagev1alpha1 "github.com/onmetal/onmetal-api/api/storage/v1alpha1"
	"github.com/onmetal/onmetal-api/testutils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	. "sigs.k8s.io/controller-runtime/pkg/envtest/komega"
)

var _ = Describe("VolumeReconciler", func() {
	ctx := testutils.SetupContext()
	testNs, rookNs, _ := SetupTest(ctx)

	var (
		volumeClass *storagev1alpha1.VolumeClass
		volumePool  *storagev1alpha1.VolumePool
	)

	const (
		volumeSize    = "1Gi"
		snapshotSize  = "2Gi"
		cephPoolName  = "pool-1"
		cephImageName = "image-1"
	)

	BeforeEach(func() {
		volumeClass = &storagev1alpha1.VolumeClass{
			TypeMeta: metav1.TypeMeta{},
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "sc-",
			},
			Capabilities: map[corev1.ResourceName]resource.Quantity{
				storagev1alpha1.ResourceIOPS: resource.MustParse("100"),
				storagev1alpha1.ResourceTPS:  resource.MustParse("1"),
			},
		}
		Expect(k8sClient.Create(ctx, volumeClass)).To(Succeed())

		By("checking that a VolumePool has been created")
		volumePool = &storagev1alpha1.VolumePool{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "custom-pool-",
				Namespace:    testNs.Name,
			},
			Spec: storagev1alpha1.VolumePoolSpec{
				ProviderID: "custom://custom-pool",
			},
		}
		Expect(k8sClient.Create(ctx, volumePool)).Should(Succeed())

		cephClientSecret := getCephClientSecret(rookNs.Name, GetClusterVolumePoolName(rookConfig.ClusterId, volumePool.Name), cephClientSecretValue)
		Expect(clientutils.IgnoreAlreadyExists(k8sClient.Create(ctx, cephClientSecret))).To(Succeed())

		volumePoolBase := volumePool.DeepCopy()
		if volumePool.Annotations == nil {
			volumePool.Annotations = map[string]string{}
		}
		volumePool.Annotations[volumePoolSecretAnnotation] = cephClientSecret.Name
		Expect(k8sClient.Patch(ctx, volumePool, client.MergeFrom(volumePoolBase))).To(Succeed())

		volumePoolBase = volumePool.DeepCopy()
		volumePool.Status.State = storagev1alpha1.VolumePoolStateAvailable
		Expect(k8sClient.Status().Patch(ctx, volumePool, client.MergeFrom(volumePoolBase))).To(Succeed())

	})

	It("should reconcile volume", func() {
		By("checking that a Volume has been created")
		vol := &storagev1alpha1.Volume{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "volume-",
				Namespace:    testNs.Name,
			},
			Spec: storagev1alpha1.VolumeSpec{
				VolumeClassRef: &corev1.LocalObjectReference{Name: volumeClass.Name},
				VolumePoolRef:  &corev1.LocalObjectReference{Name: volumePool.Name},
				Resources: corev1.ResourceList{
					"storage": resource.MustParse(volumeSize),
				},
			},
		}
		Expect(k8sClient.Create(ctx, vol)).To(Succeed())

		By("checking that the ceph volume is pending")
		Eventually(Object(vol)).Should(
			HaveField("Status.State", Equal(storagev1alpha1.VolumeStatePending)))

		By("checking that the pvc has been created")
		pvc := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      vol.Name,
				Namespace: vol.Namespace,
			},
		}
		Eventually(Get(pvc)).Should(Succeed())

		By("creating the pv for pvc")
		pv := getPVSpec(pvc, resource.MustParse("1Gi"), cephPoolName, cephImageName)
		Expect(k8sClient.Create(ctx, pv)).To(Succeed())

		pvcBase := pvc.DeepCopy()
		pvc.Spec.VolumeName = pv.Name
		Expect(k8sClient.Patch(ctx, pvc, client.MergeFrom(pvcBase)))

		pvcBase = pvc.DeepCopy()
		pvc.Status.Phase = corev1.ClaimBound
		Expect(k8sClient.Status().Patch(ctx, pvc, client.MergeFrom(pvcBase)))

		By("checking that the volume status has been updated")
		Eventually(Object(vol)).
			Should(SatisfyAll(
				HaveField("Status.State", Equal(storagev1alpha1.VolumeStateAvailable)),
				HaveField("Status.Access.SecretRef", Not(BeNil())),
				HaveField("Status.Access.Handle", Not(BeEmpty())),
				HaveField("Status.Access.Driver", Equal("ceph")),
				HaveField("Status.Access.VolumeAttributes", HaveKeyWithValue("image", fmt.Sprintf("%s/%s", cephPoolName, cephImageName))),
				HaveField("Status.Access.VolumeAttributes", HaveKey("monitors")),
			))

		accessSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      vol.Status.Access.SecretRef.Name,
				Namespace: vol.Namespace,
			},
		}
		Eventually(Object(accessSecret)).Should(SatisfyAll(
			HaveField("Data", HaveKeyWithValue("userID", BeEquivalentTo(GetClusterVolumePoolName(rookConfig.ClusterId, volumePool.Name)))),
			HaveField("Data", HaveKeyWithValue("userKey", BeEquivalentTo(cephClientSecretValue))),
		))
	})

	It("should reconcile volumes in the same customer ns", func() {
		By("checking that a Volume has been created")
		vol := &storagev1alpha1.Volume{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "volume-",
				Namespace:    testNs.Name,
			},
			Spec: storagev1alpha1.VolumeSpec{
				VolumeClassRef: &corev1.LocalObjectReference{Name: volumeClass.Name},
				VolumePoolRef:  &corev1.LocalObjectReference{Name: volumePool.Name},
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

		By("checking that the volume status has been updated")
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, volKey, vol)).To(Succeed())
			g.Expect(vol.Status.State).To(BeEquivalentTo(storagev1alpha1.VolumeStateAvailable))

			g.Expect(k8sClient.Get(ctx, volKey2, vol2)).To(Succeed())
			g.Expect(vol2.Status.State).To(BeEquivalentTo(storagev1alpha1.VolumeStateAvailable))
		}).Should(Succeed())
	})

	It("should reconcile volume with image reference and too little storage space", func() {
		vol := &storagev1alpha1.Volume{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "volume-",
				Namespace:    testNs.Name,
			},
			Spec: storagev1alpha1.VolumeSpec{
				VolumeClassRef: &corev1.LocalObjectReference{Name: volumeClass.Name},
				VolumePoolRef:  &corev1.LocalObjectReference{Name: volumePool.Name},
				Resources: corev1.ResourceList{
					"storage": resource.MustParse(volumeSize),
				},
				Image: "example.com/test:latest",
			},
		}

		By("checking that a VolumeSnapshot has been created")
		snapshot := &snapshotv1.VolumeSnapshot{
			ObjectMeta: metav1.ObjectMeta{
				Name:      GetSanitizedImageNameFromVolume(vol),
				Namespace: vol.Namespace,
			},
			Spec: snapshotv1.VolumeSnapshotSpec{
				Source: snapshotv1.VolumeSnapshotSource{
					PersistentVolumeClaimName: pointer.String("some-pvc"),
				},
				VolumeSnapshotClassName: pointer.String("some-snapshot-class"),
			},
		}
		Expect(k8sClient.Create(ctx, snapshot)).To(Succeed())

		snapshotRestoreSize := resource.MustParse(snapshotSize)
		snapshotBase := snapshot.DeepCopy()
		snapshot.Status = &snapshotv1.VolumeSnapshotStatus{
			BoundVolumeSnapshotContentName: pointer.String("snapcontent-2e13a6b8"),
			ReadyToUse:                     pointer.Bool(true),
			RestoreSize:                    &snapshotRestoreSize,
		}
		Expect(k8sClient.Status().Patch(ctx, snapshot, client.MergeFrom(snapshotBase)))

		By("checking that a Volume has been created")
		Expect(k8sClient.Create(ctx, vol)).To(Succeed())

		By("checking that the pvc has been created but not the corresponding pv ")
		pvc := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      vol.Name,
				Namespace: vol.Namespace,
			},
		}
		Eventually(Get(pvc)).Should(Succeed())

		Eventually(volumeEventRecorder.Events).Should(Receive(ContainSubstring("Requested volume size")))

		Eventually(Object(vol)).Should(SatisfyAll(
			HaveField("Status.State", Equal(storagev1alpha1.VolumeStatePending)),
		))
	})

	It("should end reconcile volume if pool ref is not valid", func() {
		By("checking that a Volume has been created")
		vol := &storagev1alpha1.Volume{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "volume-",
				Namespace:    testNs.Name,
			},
			Spec: storagev1alpha1.VolumeSpec{
				VolumeClassRef: &corev1.LocalObjectReference{Name: "not-there"},
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
