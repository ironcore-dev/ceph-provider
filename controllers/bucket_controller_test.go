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
	bucketv1alpha1 "github.com/kube-object-storage/lib-bucket-provisioner/pkg/apis/objectbucket.io/v1alpha1"
	"k8s.io/apimachinery/pkg/api/resource"

	storagev1alpha1 "github.com/onmetal/onmetal-api/api/storage/v1alpha1"
	"github.com/onmetal/onmetal-api/testutils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	. "sigs.k8s.io/controller-runtime/pkg/envtest/komega"
)

var _ = Describe("BucketReconciler", func() {
	ctx := testutils.SetupContext()
	testNs, _, _ := SetupTest(ctx)

	var (
		bucketClass *storagev1alpha1.BucketClass
		bucketPool  *storagev1alpha1.BucketPool
	)

	BeforeEach(func() {
		bucketClass = &storagev1alpha1.BucketClass{
			TypeMeta: metav1.TypeMeta{},
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "sc-",
			},
			Capabilities: map[corev1.ResourceName]resource.Quantity{
				storagev1alpha1.ResourceIOPS: resource.MustParse("100"),
				storagev1alpha1.ResourceTPS:  resource.MustParse("1"),
			},
		}
		Expect(k8sClient.Create(ctx, bucketClass)).To(Succeed())

		By("checking that a BucketPool has been created")
		bucketPool = &storagev1alpha1.BucketPool{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "custom-pool-",
				Namespace:    testNs.Name,
			},
			Spec: storagev1alpha1.BucketPoolSpec{
				ProviderID: "custom://custom-pool",
			},
		}
		Expect(k8sClient.Create(ctx, bucketPool)).Should(Succeed())

		bucketPoolBase := bucketPool.DeepCopy()
		bucketPool.Status.State = storagev1alpha1.BucketPoolStateAvailable
		Expect(k8sClient.Status().Patch(ctx, bucketPool, client.MergeFrom(bucketPoolBase))).To(Succeed())

	})

	It("should reconcile bucket", func() {
		By("checking that a Bucket has been created")
		bucket := &storagev1alpha1.Bucket{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "bucket-",
				Namespace:    testNs.Name,
			},
			Spec: storagev1alpha1.BucketSpec{
				BucketClassRef: &corev1.LocalObjectReference{Name: bucketClass.Name},
				BucketPoolRef:  &corev1.LocalObjectReference{Name: bucketPool.Name},
			},
		}
		Expect(k8sClient.Create(ctx, bucket)).To(Succeed())

		By("checking that the pvc has been created")

		obc := &bucketv1alpha1.ObjectBucketClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      bucket.Name,
				Namespace: bucket.Namespace,
			},
		}
		Eventually(Get(obc)).Should(Succeed())

		ob := &bucketv1alpha1.ObjectBucket{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: obc.Name,
				Namespace:    obc.Namespace,
			},
			Spec: bucketv1alpha1.ObjectBucketSpec{
				StorageClassName: obc.Spec.StorageClassName,
				Connection: &bucketv1alpha1.Connection{
					Endpoint: &bucketv1alpha1.Endpoint{
						BucketHost: "rook",
						BucketPort: 00,
						BucketName: "name",
						Region:     "",
						SubRegion:  "",
					},
					AdditionalState: map[string]string{
						"cephUser":             "obc-default-ceph-bucket",
						"objectStoreName":      "test-store",
						"objectStoreNamespace": "rook-ceph",
					},
				},
			},
			Status: bucketv1alpha1.ObjectBucketStatus{
				Phase: bucketv1alpha1.ObjectBucketClaimStatusPhaseBound,
			},
		}
		Expect(k8sClient.Create(ctx, ob)).To(Succeed())

		obcBase := obc.DeepCopy()
		obc.Spec.ObjectBucketName = ob.Name
		Expect(k8sClient.Status().Patch(ctx, obc, client.MergeFrom(obcBase)))

		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      bucket.Name,
				Namespace: bucket.Namespace,
			},
			Type: corev1.SecretTypeOpaque,
		}
		Expect(k8sClient.Create(ctx, secret)).To(Succeed())

		obcBase = obc.DeepCopy()
		obc.Status.Phase = bucketv1alpha1.ObjectBucketClaimStatusPhaseBound
		Expect(k8sClient.Status().Patch(ctx, obc, client.MergeFrom(obcBase)))

		By("checking that the bucket status has been updated")
		Eventually(Object(bucket)).Should(SatisfyAll(
			HaveField("Status.State", Equal(storagev1alpha1.BucketStateAvailable)),
			HaveField("Status.Access.SecretRef", Not(BeNil())),
		))

		accessSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      bucket.Status.Access.SecretRef.Name,
				Namespace: bucket.Namespace,
			},
		}
		Eventually(Get(accessSecret)).Should(Succeed())
	})

	It("should end reconcile bucket if pool ref is not valid", func() {
		By("checking that a Bucket has been created")
		bucket := &storagev1alpha1.Bucket{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "bucket-",
				Namespace:    testNs.Name,
			},
			Spec: storagev1alpha1.BucketSpec{
				BucketClassRef: &corev1.LocalObjectReference{Name: "not-there"},
				BucketPoolRef:  &corev1.LocalObjectReference{Name: "not-there"},
			},
		}
		Expect(k8sClient.Create(ctx, bucket)).To(Succeed())

		By("checking that the bucket status has been updated")
		Eventually(Object(bucket)).Should(SatisfyAll(
			HaveField("Status.State", Not(Equal(storagev1alpha1.BucketStateAvailable))),
		))
	})
})
