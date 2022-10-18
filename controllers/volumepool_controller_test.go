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
	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v6/apis/volumesnapshot/v1"
	"github.com/onmetal/cephlet/pkg/rook"
	storagev1alpha1 "github.com/onmetal/onmetal-api/apis/storage/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	rookv1 "github.com/rook/rook/pkg/apis/ceph.rook.io/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("VolumePoolReconciler", func() {
	testNs, rookNs, _ := SetupTest(ctx)

	When("is started", func() {
		It("should announce a VolumePool", func() {
			By("checking that a VolumePool has been created")
			volumePool := &storagev1alpha1.VolumePool{}
			volumePoolKey := types.NamespacedName{Name: volumePoolName}
			Eventually(func() error { return k8sClient.Get(ctx, volumePoolKey, volumePool) }).Should(Succeed())

			By("checking that a CephBlockPool has been created")
			rookPool := &rookv1.CephBlockPool{}
			rookPoolKey := types.NamespacedName{Name: volumePoolName, Namespace: rookNs.Name}
			Eventually(func() error { return k8sClient.Get(ctx, rookPoolKey, rookPool) }).Should(Succeed())

			Expect(rookPool.Spec.PoolSpec.Replicated.Size).To(HaveValue(Equal(uint(volumePoolReplication))))
			Expect(rookPool.Spec.PoolSpec.EnableRBDStats).To(Equal(rook.EnableRBDStatsDefaultValue))

			By("checking that a VolumePool reflect the rook status")
			rookPoolBase := rookPool.DeepCopy()
			rookPool.Status = &rookv1.CephBlockPoolStatus{
				Phase: rookv1.ConditionProgressing,
			}
			Expect(k8sClient.Status().Patch(ctx, rookPool, client.MergeFrom(rookPoolBase))).To(Succeed())

			Eventually(func(g Gomega) error {
				if err := k8sClient.Get(ctx, volumePoolKey, volumePool); err != nil {
					return err
				}
				g.Expect(volumePool.Status.State).To(BeEquivalentTo(storagev1alpha1.VolumePoolStatePending))
				return nil
			}).Should(Succeed())

			By("checking that a CephClient has been created")
			cephClient := &rookv1.CephClient{}
			cephClientKey := types.NamespacedName{Name: GetClusterPoolName(rookConfig.ClusterId, volumePoolName), Namespace: rookNs.Name}
			Eventually(func() error { return k8sClient.Get(ctx, cephClientKey, cephClient) }).Should(Succeed())

			cephClientBase := cephClient.DeepCopy()
			cephClient.Status = &rookv1.CephClientStatus{
				Phase: rookv1.ConditionReady,
			}
			Expect(k8sClient.Status().Patch(ctx, cephClient, client.MergeFrom(cephClientBase))).To(Succeed())

			By("checking that a StorageClass has been created")
			storageClass := &storagev1.StorageClass{}
			storageClassKey := types.NamespacedName{Name: GetClusterPoolName(rookConfig.ClusterId, volumePoolName)}
			Eventually(func() error { return k8sClient.Get(ctx, storageClassKey, storageClass) }).Should(Succeed())

			Expect(storageClass.Provisioner).To(BeEquivalentTo(rookConfig.CSIDriverName))

			By("checking that a VolumeSnapshotClass has been created")
			volumeSnapshotClass := &snapshotv1.VolumeSnapshotClass{}
			volumeSnapshotClassKey := types.NamespacedName{Name: GetClusterPoolName(rookConfig.ClusterId, volumePoolName)}
			Eventually(func() error { return k8sClient.Get(ctx, volumeSnapshotClassKey, volumeSnapshotClass) }).Should(Succeed())

			Expect(volumeSnapshotClass.Driver).To(BeEquivalentTo(rookConfig.CSIDriverName))

			By("checking that a VolumePool reflect the rook status")
			rookPoolBase = rookPool.DeepCopy()
			rookPool.Status.Phase = rookv1.ConditionFailure
			Expect(k8sClient.Status().Patch(ctx, rookPool, client.MergeFrom(rookPoolBase))).To(Succeed())

			Eventually(func(g Gomega) error {
				if err := k8sClient.Get(ctx, volumePoolKey, volumePool); err != nil {
					return err
				}
				g.Expect(volumePool.Status.State).To(BeEquivalentTo(storagev1alpha1.VolumePoolStateNotAvailable))
				return nil
			}).Should(Succeed())

			rookPoolBase = rookPool.DeepCopy()
			rookPool.Status.Phase = rookv1.ConditionReady
			Expect(k8sClient.Status().Patch(ctx, rookPool, client.MergeFrom(rookPoolBase))).To(Succeed())

			Eventually(func(g Gomega) error {
				if err := k8sClient.Get(ctx, volumePoolKey, volumePool); err != nil {
					return err
				}
				g.Expect(volumePool.Status.State).To(BeEquivalentTo(storagev1alpha1.VolumePoolStateAvailable))
				return nil
			}).Should(Succeed())
		})
	})

	When("should reconcile", func() {
		It("a valid custom created pool", func() {
			volumePool := &storagev1alpha1.VolumePool{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "custom-pool-",
					Namespace:    testNs.Name,
				},
				Spec: storagev1alpha1.VolumePoolSpec{
					ProviderID: "custom://custom-pool",
				},
			}
			Expect(k8sClient.Create(ctx, volumePool)).Should(Succeed())

			By("checking that a VolumePool has not been created")
			rookPool := &rookv1.CephBlockPool{}
			rookPoolKey := types.NamespacedName{Name: volumePool.Name, Namespace: rookNs.Name}
			Eventually(func() bool { return errors.IsNotFound(k8sClient.Get(ctx, rookPoolKey, rookPool)) }).Should(BeTrue())
		})
	})
})
