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
	storagev1alpha1 "github.com/onmetal/onmetal-api/api/storage/v1alpha1"
	"github.com/onmetal/onmetal-api/testutils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	rookv1 "github.com/rook/rook/pkg/apis/ceph.rook.io/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("BucketPoolReconciler", func() {
	ctx := testutils.SetupContext()
	testNs, rookNs, _ := SetupTest(ctx)

	When("is started", func() {
		It("should announce a BucketPool", func() {
			By("checking that a BucketPool has been created")
			bucketPool := &storagev1alpha1.BucketPool{}
			bucketPoolKey := types.NamespacedName{Name: bucketPoolName}
			Eventually(func() error { return k8sClient.Get(ctx, bucketPoolKey, bucketPool) }).Should(Succeed())

			By("checking that a CephObjectStore has been created")
			rookPool := &rookv1.CephObjectStore{}
			rookPoolKey := types.NamespacedName{Name: bucketPoolName, Namespace: rookNs.Name}
			Eventually(func() error { return k8sClient.Get(ctx, rookPoolKey, rookPool) }).Should(Succeed())

			Expect(rookPool.Spec.DataPool.Replicated.Size).To(HaveValue(Equal(uint(bucketPoolReplication))))
			Expect(rookPool.Spec.MetadataPool.Replicated.Size).To(HaveValue(Equal(uint(bucketPoolReplication))))

			By("checking that a BucketPool reflect the rook status")
			rookPoolBase := rookPool.DeepCopy()
			rookPool.Status = &rookv1.ObjectStoreStatus{
				Phase: rookv1.ConditionProgressing,
			}
			Expect(k8sClient.Status().Patch(ctx, rookPool, client.MergeFrom(rookPoolBase))).To(Succeed())

			Eventually(func(g Gomega) error {
				if err := k8sClient.Get(ctx, bucketPoolKey, bucketPool); err != nil {
					return err
				}
				g.Expect(bucketPool.Status.State).To(BeEquivalentTo(storagev1alpha1.BucketPoolStatePending))
				return nil
			}).Should(Succeed())

			By("checking that a BucketPool reflect the rook status")
			rookPoolBase = rookPool.DeepCopy()
			rookPool.Status.Phase = rookv1.ConditionFailure
			Expect(k8sClient.Status().Patch(ctx, rookPool, client.MergeFrom(rookPoolBase))).To(Succeed())

			Eventually(func(g Gomega) error {
				if err := k8sClient.Get(ctx, bucketPoolKey, bucketPool); err != nil {
					return err
				}
				g.Expect(bucketPool.Status.State).To(BeEquivalentTo(storagev1alpha1.BucketPoolStateUnavailable))
				return nil
			}).Should(Succeed())

			rookPoolBase = rookPool.DeepCopy()
			rookPool.Status.Phase = rookv1.ConditionConnected
			Expect(k8sClient.Status().Patch(ctx, rookPool, client.MergeFrom(rookPoolBase))).To(Succeed())

			Eventually(func(g Gomega) error {
				if err := k8sClient.Get(ctx, bucketPoolKey, bucketPool); err != nil {
					return err
				}
				g.Expect(bucketPool.Status.State).To(BeEquivalentTo(storagev1alpha1.BucketPoolStateAvailable))
				g.Expect(bucketPool.Status.AvailableBucketClasses).To(BeNil())
				return nil
			}).Should(Succeed())

			By("creating a BucketClass")
			volumeClass := &storagev1alpha1.BucketClass{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "sc-",
					Labels:       volumeClassSelector,
				},
				Capabilities: map[corev1.ResourceName]resource.Quantity{
					storagev1alpha1.ResourceIOPS: resource.MustParse("100"),
					storagev1alpha1.ResourceTPS:  resource.MustParse("1"),
				},
			}
			Expect(k8sClient.Create(ctx, volumeClass)).To(Succeed())

			By("creating a second BucketClass")
			Expect(k8sClient.Create(ctx, &storagev1alpha1.BucketClass{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "sc-",
					Labels: map[string]string{
						"suitable-for": "production",
					},
				},
				Capabilities: map[corev1.ResourceName]resource.Quantity{
					storagev1alpha1.ResourceIOPS: resource.MustParse("100"),
					storagev1alpha1.ResourceTPS:  resource.MustParse("1"),
				},
			})).To(Succeed())

			By("checking that the BucketPool status includes the correct BucketClass")
			Eventually(func(g Gomega) error {
				if err := k8sClient.Get(ctx, bucketPoolKey, bucketPool); err != nil {
					return err
				}
				g.Expect(bucketPool.Status.State).To(BeEquivalentTo(storagev1alpha1.BucketPoolStateAvailable))
				g.Expect(bucketPool.Status.AvailableBucketClasses).To(HaveLen(1))
				g.Expect(bucketPool.Status.AvailableBucketClasses).To(ContainElement(corev1.LocalObjectReference{Name: volumeClass.Name}))
				return nil
			}).Should(Succeed())
		})
	})

	When("should reconcile", func() {
		It("a valid custom created pool", func() {
			bucketPool := &storagev1alpha1.BucketPool{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "custom-pool-",
					Namespace:    testNs.Name,
				},
				Spec: storagev1alpha1.BucketPoolSpec{
					ProviderID: "custom://custom-pool",
				},
			}
			Expect(k8sClient.Create(ctx, bucketPool)).Should(Succeed())

			By("checking that a BucketPool has not been created")
			rookPool := &rookv1.CephObjectStore{}
			rookPoolKey := types.NamespacedName{Name: bucketPool.Name, Namespace: rookNs.Name}
			Eventually(func() bool { return errors.IsNotFound(k8sClient.Get(ctx, rookPoolKey, rookPool)) }).Should(BeTrue())
		})
	})
})
