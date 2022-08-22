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
	"k8s.io/apimachinery/pkg/api/errors"

	popv1beta1 "github.com/kubernetes-csi/volume-data-source-validator/client/apis/volumepopulator/v1beta1"
	storagev1alpha1 "github.com/onmetal/onmetal-api/apis/storage/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("ImagePopulatorReconciler", func() {
	testNs, _, populatorNS := SetupTest(ctx)

	var (
		volumeClass *storagev1alpha1.VolumeClass
		apiGroup    = storagev1alpha1.SchemeGroupVersion.String()
	)

	BeforeEach(func() {
		volumeClass = &storagev1alpha1.VolumeClass{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "sc-",
			},
		}
		Expect(k8sClient.Create(ctx, volumeClass)).To(Succeed())

		volumePopulator := &popv1beta1.VolumePopulator{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "pop-",
			},
			SourceKind: metav1.GroupKind{
				Group: apiGroup,
				Kind:  "Volume",
			},
		}
		Expect(k8sClient.Create(ctx, volumePopulator)).To(Succeed())
	})

	It("should create a populator pod for a given PVC, create a shaddow PVC, mock a successful run and reassign the PV claim", func() {
		By("creating a volume")
		volumeSize := "1Gi"
		vol := &storagev1alpha1.Volume{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "volume-",
				Namespace:    testNs.Name,
			},
			Spec: storagev1alpha1.VolumeSpec{
				VolumeClassRef: corev1.LocalObjectReference{Name: volumeClass.Name},
				VolumePoolRef:  &corev1.LocalObjectReference{Name: volumePoolName},
				Resources: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse(volumeSize),
				},
				Image: "my-image",
			},
		}
		Expect(k8sClient.Create(ctx, vol)).To(Succeed())

		By("creating a storageclass")
		bindingMode := storagev1.VolumeBindingWaitForFirstConsumer
		storageClass := &storagev1.StorageClass{
			TypeMeta: metav1.TypeMeta{
				Kind:       "StorageClass",
				APIVersion: "storage.k8s.io/v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "sc-",
			},
			Provisioner:       "my-driver",
			VolumeBindingMode: &bindingMode,
		}
		Expect(k8sClient.Create(ctx, storageClass)).To(Succeed())

		By("creating a pvc")
		mode := corev1.PersistentVolumeBlock
		pvc := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "pvc-",
				Namespace:    testNs.Name,
				Annotations:  map[string]string{annSelectedNode: "node"},
			},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceStorage: vol.Spec.Resources[corev1.ResourceStorage]},
				},
				StorageClassName: &storageClass.Name,
				VolumeMode:       &mode,
				DataSourceRef: &corev1.TypedLocalObjectReference{
					APIGroup: &apiGroup,
					Kind:     "Volume",
					Name:     vol.Name,
				},
			},
		}
		Expect(k8sClient.Create(ctx, pvc)).To(Succeed())

		By("ensuring that the pvc has a correct datasource ref")
		pvcKey := types.NamespacedName{Name: pvc.Name, Namespace: testNs.Name}
		Eventually(func(g Gomega) {
			err := k8sClient.Get(ctx, pvcKey, pvc)
			Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred())
			g.Expect(err).NotTo(HaveOccurred())

			g.Expect(pvc.Spec.DataSourceRef).To(Equal(&corev1.TypedLocalObjectReference{
				APIGroup: &apiGroup,
				Kind:     "Volume",
				Name:     vol.Name,
			}))
		}).Should(Succeed())

		By("ensuring that the pvc has a finalizer set")
		Eventually(func(g Gomega) {
			err := k8sClient.Get(ctx, pvcKey, pvc)
			Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred())
			g.Expect(err).NotTo(HaveOccurred())

			g.Expect(pvc.Finalizers).To(ContainElement(pvcFinalizer))
		}).Should(Succeed())

		By("ensuring that the shadow pvc has been created in the populator namespace")
		pvcPrime := &corev1.PersistentVolumeClaim{}
		pvcPrimeKey := types.NamespacedName{
			Namespace: populatorNS.Name,
			Name:      fmt.Sprintf("%s-%s", populatorPvcPrefix, pvc.UID),
		}
		Eventually(func(g Gomega) {
			err := k8sClient.Get(ctx, pvcPrimeKey, pvcPrime)
			Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred())
			g.Expect(err).NotTo(HaveOccurred())

			// TODO: compare spec
		}).Should(Succeed())

		By("ensuring that the populator pod has been created")
		populatorPod := &corev1.Pod{}
		podName := fmt.Sprintf("%s-%s", populatorPodPrefix, pvc.UID)
		populatorPodKey := types.NamespacedName{Namespace: populatorNS.Name, Name: podName}
		Eventually(func(g Gomega) {
			err := k8sClient.Get(ctx, populatorPodKey, populatorPod)
			Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred())
			g.Expect(err).NotTo(HaveOccurred())

			g.Expect(populatorPod.Spec.Containers[0]).To(Equal(corev1.Container{
				Name:            populatorContainerName,
				Image:           defaultPopulatorImage,
				Args:            []string{"--mode=populate", "--image=" + vol.Spec.Image},
				ImagePullPolicy: corev1.PullIfNotPresent,
				VolumeDevices: []corev1.VolumeDevice{
					{
						Name:       populatorPodVolumeName,
						DevicePath: defaultDevicePath,
					},
				},
				TerminationMessagePath:   "/dev/termination-log",
				TerminationMessagePolicy: "File",
			}))
			g.Expect(populatorPod.Spec.RestartPolicy).To(Equal(corev1.RestartPolicyNever))
			g.Expect(populatorPod.Spec.Volumes[0]).To(Equal(corev1.Volume{
				Name: populatorPodVolumeName,
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: pvcPrime.Name,
					},
				},
			}))
		}).Should(Succeed())

		By("creating a PV for the shaddow PVC")
		pvPrime := &corev1.PersistentVolume{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "pv-",
			},
			Spec: corev1.PersistentVolumeSpec{
				PersistentVolumeSource: corev1.PersistentVolumeSource{
					CSI: &corev1.CSIPersistentVolumeSource{
						Driver:       "rook-ceph.rbd.csi.ceph.com",
						VolumeHandle: "handle",
						VolumeAttributes: map[string]string{
							"pool":      "my-pool",
							"imageName": "my-image",
						},
					},
				},
				StorageClassName: storageClass.Name,
				AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Capacity: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse(volumeSize),
				},
				ClaimRef: &corev1.ObjectReference{
					Namespace:       pvcPrime.Namespace,
					Name:            pvcPrime.Name,
					UID:             pvcPrime.UID,
					ResourceVersion: pvcPrime.ResourceVersion,
				},
			},
		}
		Expect(k8sClient.Create(ctx, pvPrime)).Should(Succeed())

		By("patching the pv name into the shadow pvc")
		pvcPrimeBase := pvcPrime.DeepCopy()
		pvcPrime.Spec.VolumeName = pvPrime.Name
		Expect(k8sClient.Patch(ctx, pvcPrime, client.MergeFrom(pvcPrimeBase))).To(Succeed())

		By("patching the populator pod status to succeeded")
		populatorPodBase := populatorPod.DeepCopy()
		populatorPod.Status.Phase = corev1.PodSucceeded
		Expect(k8sClient.Status().Patch(ctx, populatorPod, client.MergeFrom(populatorPodBase))).To(Succeed())

		Expect("that PV of shadow PVC has been created and patched")
		pv := &corev1.PersistentVolume{}
		pvKey := types.NamespacedName{Name: pvPrime.Name}
		Eventually(func(g Gomega) {
			err := k8sClient.Get(ctx, pvKey, pv)
			Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred())
			g.Expect(err).NotTo(HaveOccurred())

			g.Expect(pv.Spec.ClaimRef.Namespace).To(Equal(pvc.Namespace))
			g.Expect(pv.Spec.ClaimRef.Name).To(Equal(pvc.Name))
			g.Expect(pv.Spec.ClaimRef.UID).To(Equal(pvc.UID))
		}).Should(Succeed())

		By("patching the shadow pvc claim status to Lost")
		pvcPrimeBase = pvcPrime.DeepCopy()
		pvcPrime.Status.Phase = corev1.ClaimLost
		Expect(k8sClient.Status().Patch(ctx, pvcPrime, client.MergeFrom(pvcPrimeBase))).To(Succeed())

		By("patching the pvc volume name")
		pvcBase := pvc.DeepCopy()
		pvc.Spec.VolumeName = pv.Name
		Expect(k8sClient.Patch(ctx, pvc, client.MergeFrom(pvcBase))).To(Succeed())

		By("ensuring that the populator pod has a deletion timestamp")
		Eventually(func(g Gomega) {
			err := k8sClient.Get(ctx, populatorPodKey, populatorPod)
			Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred())
			g.Expect(errors.IsNotFound(err)).To(BeTrue())
		}).Should(Succeed())

		By("ensuring that the shadow pvc has a deletion timestamp")
		Eventually(func(g Gomega) {
			err := k8sClient.Get(ctx, pvcPrimeKey, pvcPrime)
			Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred())
			g.Expect(err).NotTo(HaveOccurred())

			g.Expect(pvcPrime.DeletionTimestamp.IsZero()).To(BeFalse())
		}).Should(Succeed())

		By("ensuring that the pvc has no finalizer")
		Eventually(func(g Gomega) {
			err := k8sClient.Get(ctx, pvcKey, pvc)
			Expect(client.IgnoreNotFound(err)).NotTo(HaveOccurred())
			g.Expect(err).NotTo(HaveOccurred())

			g.Expect(pvc.Finalizers).To(Not(ContainElement(pvcFinalizer)))
		}).Should(Succeed())
	})
})
