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
	storagev1alpha1 "github.com/onmetal/onmetal-api/apis/storage/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("ImagePopulatorReconciler", func() {
	testNs, _ := SetupTest(ctx)

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

	It("should create a populator pod for a given PVC", func() {
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
					"storage": resource.MustParse(volumeSize),
				},
			},
		}
		Expect(k8sClient.Create(ctx, vol)).To(Succeed())

		By("creating a storageclass")
		storageClass := &storagev1.StorageClass{
			TypeMeta: metav1.TypeMeta{
				Kind:       "StorageClass",
				APIVersion: "storage.k8s.io/v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "sc-",
			},
			Provisioner: "my-driver",
		}
		Expect(k8sClient.Create(ctx, storageClass)).To(Succeed())

		By("creating a pvc")
		mode := corev1.PersistentVolumeBlock
		apiGroup := storagev1alpha1.SchemeGroupVersion.String()
		pvc := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "pvc-",
				Namespace:    testNs.Name,
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
	})
})
