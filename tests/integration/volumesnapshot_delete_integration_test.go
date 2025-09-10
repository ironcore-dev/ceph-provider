// SPDX-FileCopyrightText: 2023 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package integration

import (
	"encoding/json"

	"github.com/ironcore-dev/ceph-provider/api"
	"github.com/ironcore-dev/ceph-provider/internal/omap"
	metav1alpha1 "github.com/ironcore-dev/ironcore/iri/apis/meta/v1alpha1"
	iriv1alpha1 "github.com/ironcore-dev/ironcore/iri/apis/volume/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = FDescribe("Delete VolumeSnapshot", func() {
	It("should delete a volume snapshot", func(ctx SpecContext) {
		By("creating a volume")
		createResp, err := volumeClient.CreateVolume(ctx, &iriv1alpha1.CreateVolumeRequest{
			Volume: &iriv1alpha1.Volume{
				Metadata: &metav1alpha1.ObjectMetadata{
					Id: "foo",
				},
				Spec: &iriv1alpha1.VolumeSpec{
					Class: "foo",
					Resources: &iriv1alpha1.VolumeResources{
						StorageBytes: 1024 * 1024 * 1024,
					},
				},
			},
		})
		Expect(err).NotTo(HaveOccurred())

		DeferCleanup(volumeClient.DeleteVolume, &iriv1alpha1.DeleteVolumeRequest{
			VolumeId: createResp.Volume.Metadata.Id,
		})

		By("ensuring image has been created in ceph cluster image store")
		image := &api.Image{}
		Eventually(ctx, func() *api.Image {
			oMap, err := ioctx.GetOmapValues(omap.NameVolumes, "", createResp.Volume.Metadata.Id, 10)
			Expect(err).NotTo(HaveOccurred())
			Expect(oMap).To(HaveKey(createResp.Volume.Metadata.Id))
			Expect(json.Unmarshal(oMap[createResp.Volume.Metadata.Id], image)).NotTo(HaveOccurred())
			return image
		}).Should(SatisfyAll(
			HaveField("Metadata.ID", Equal(createResp.Volume.Metadata.Id)),
			HaveField("Status.State", Equal(api.ImageStateAvailable)),
		))

		By("ensuring volume is in available state")
		Eventually(func() *iriv1alpha1.VolumeStatus {
			resp, err := volumeClient.ListVolumes(ctx, &iriv1alpha1.ListVolumesRequest{
				Filter: &iriv1alpha1.VolumeFilter{
					Id: createResp.Volume.Metadata.Id,
				},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.Volumes).NotTo(BeEmpty())
			return resp.Volumes[0].Status
		}).Should(SatisfyAll(
			HaveField("State", Equal(iriv1alpha1.VolumeState_VOLUME_AVAILABLE)),
		))

		By("creating a volume snapshot")
		createSnapshotResp, err := volumeClient.CreateVolumeSnapshot(ctx, &iriv1alpha1.CreateVolumeSnapshotRequest{
			VolumeSnapshot: &iriv1alpha1.VolumeSnapshot{
				Metadata: &metav1alpha1.ObjectMetadata{
					Id: "foo-snap",
				},
				Spec: &iriv1alpha1.VolumeSnapshotSpec{
					VolumeId: createResp.Volume.Metadata.Id,
				},
			},
		})
		Expect(err).NotTo(HaveOccurred())

		snapshotID := createSnapshotResp.VolumeSnapshot.Metadata.Id

		DeferCleanup(volumeClient.DeleteVolumeSnapshot, &iriv1alpha1.DeleteVolumeSnapshotRequest{
			VolumeSnapshotId: snapshotID,
		})

		By("ensuring snapshot has been created in ceph cluster snapshot store")
		snapshot := &api.Snapshot{}
		Eventually(ctx, func() *api.Snapshot {
			oMap, err := ioctx.GetOmapValues(omap.NameSnapshots, "", snapshotID, 10)
			Expect(err).NotTo(HaveOccurred())
			Expect(oMap).To(HaveKey(snapshotID))
			Expect(json.Unmarshal(oMap[snapshotID], snapshot)).NotTo(HaveOccurred())
			return snapshot
		}).Should(SatisfyAll(
			HaveField("Metadata.ID", Equal(snapshotID)),
			HaveField("Status.State", Equal(api.SnapshotStateReady)),
		))

		By("ensuring volume snapshot is in available state and restore size have been updated")
		Eventually(func() *iriv1alpha1.VolumeSnapshotStatus {
			resp, err := volumeClient.ListVolumeSnapshots(ctx, &iriv1alpha1.ListVolumeSnapshotsRequest{
				Filter: &iriv1alpha1.VolumeSnapshotFilter{
					Id: snapshotID,
				},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.VolumeSnapshots).NotTo(BeEmpty())
			Expect(resp.VolumeSnapshots).To(HaveLen(1))
			return resp.VolumeSnapshots[0].Status
		}).Should(SatisfyAll(
			HaveField("State", Equal(iriv1alpha1.VolumeSnapshotState_VOLUME_SNAPSHOT_PENDING)),
			HaveField("RestoreSize", Equal(int64(1024*1024*1024))),
		))

		By("deleting volume snapshot")
		_, err = volumeClient.DeleteVolumeSnapshot(ctx, &iriv1alpha1.DeleteVolumeSnapshotRequest{
			VolumeSnapshotId: snapshotID,
		})
		Expect(err).NotTo(HaveOccurred())

		By("listing volume snapshot with snapshot ID to check volume snapshot is deleted")
		Eventually(func() {
			resp, err := volumeClient.ListVolumeSnapshots(ctx, &iriv1alpha1.ListVolumeSnapshotsRequest{
				Filter: &iriv1alpha1.VolumeSnapshotFilter{
					Id: snapshotID,
				},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.VolumeSnapshots).To(BeEmpty())
		})

		By("ensuring the image has been deleted inside the ceph cluster")
		Eventually(func() {
			oMap, err := ioctx.GetOmapValues(omap.NameSnapshots, "", snapshotID, 10)
			Expect(err).NotTo(HaveOccurred())
			Expect(oMap).NotTo(HaveKey(snapshotID))
		})
	})
})
