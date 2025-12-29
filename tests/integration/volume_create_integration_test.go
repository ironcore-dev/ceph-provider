// SPDX-FileCopyrightText: 2023 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package integration

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ironcore-dev/ceph-provider/api"
	"github.com/ironcore-dev/ceph-provider/internal/omap"
	metav1alpha1 "github.com/ironcore-dev/ironcore/iri/apis/meta/v1alpha1"
	iriv1alpha1 "github.com/ironcore-dev/ironcore/iri/apis/volume/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/resource"
)

var _ = Describe("Create Volume", func() {
	It("should create a volume", func(ctx SpecContext) {
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

		By("ensuring the correct creation response")
		Expect(createResp).Should(SatisfyAll(
			HaveField("Volume.Metadata.Id", Not(BeEmpty())),
			HaveField("Volume.Spec.Image", Equal("")),
			HaveField("Volume.Spec.Class", Equal("foo")),
			HaveField("Volume.Spec.Resources.StorageBytes", Equal(int64(1024*1024*1024))),
			HaveField("Volume.Spec.Encryption", BeNil()),
			HaveField("Volume.Status.State", Equal(iriv1alpha1.VolumeState_VOLUME_PENDING)),
			HaveField("Volume.Status.Access", BeNil()),
		))

		DeferCleanup(volumeClient.DeleteVolume, &iriv1alpha1.DeleteVolumeRequest{
			VolumeId: createResp.Volume.Metadata.Id,
		})

		By("ensuring the correct image has been created inside the ceph cluster")
		image := &api.Image{}
		Eventually(ctx, func() *api.Image {
			oMap, err := ioctx.GetOmapValues(omap.NameVolumes, "", createResp.Volume.Metadata.Id, 10)
			Expect(err).NotTo(HaveOccurred())
			Expect(oMap).To(HaveKey(createResp.Volume.Metadata.Id))
			Expect(json.Unmarshal(oMap[createResp.Volume.Metadata.Id], image)).NotTo(HaveOccurred())
			return image
		}).Should(SatisfyAll(
			HaveField("Metadata.ID", Equal(createResp.Volume.Metadata.Id)),
			HaveField("Metadata.Labels", HaveKeyWithValue(api.ClassLabel, "foo")),
			HaveField("Spec.Image", Equal("")),
			HaveField("Spec.Size", Equal(uint64(1024*1024*1024))),
			HaveField("Spec.Limits", SatisfyAll(
				HaveKeyWithValue(api.IOPSBurstDurationLimit, int64(15)),
				HaveKeyWithValue(api.WriteIOPSLimit, int64(15000)),
				HaveKeyWithValue(api.ReadBPSLimit, int64(262144000)),
				HaveKeyWithValue(api.BPSLimit, int64(262144000)),
				HaveKeyWithValue(api.ReadIOPSLimit, int64(15000)),
				HaveKeyWithValue(api.WriteBPSLimit, int64(262144000)),
				HaveKeyWithValue(api.BPSBurstDurationLimit, int64(15)),
				HaveKeyWithValue(api.IOPSLimit, int64(15000)),
			)),
			HaveField("Spec.SnapshotRef", BeNil()),
			HaveField("Spec.Encryption", BeNil()),
			HaveField("Status.State", Equal(api.ImageStateAvailable)),
			HaveField("Status.Access", SatisfyAll(
				HaveField("Monitors", cephMonitors),
				HaveField("Handle", fmt.Sprintf("%s/%s", cephPoolname, "img_"+createResp.Volume.Metadata.Id)),
				HaveField("User", strings.TrimPrefix(cephClientname, "client.")),
				HaveField("UserKey", Not(BeEmpty())),
			)),
			HaveField("Status.Encryption", api.EncryptionState("")),
		))

		By("ensuring volume is in available state and other state fields have been updated")
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
			HaveField("Resources.StorageBytes", Equal(resource.NewQuantity(1024*1024*1024, resource.BinarySI).Value())),
			HaveField("Access", SatisfyAll(
				HaveField("Driver", "ceph"),
				HaveField("Handle", image.Spec.WWN),
				HaveField("Attributes", SatisfyAll(
					HaveKeyWithValue("monitors", image.Status.Access.Monitors),
					HaveKeyWithValue("image", image.Status.Access.Handle),
				)),
				HaveField("SecretData", SatisfyAll(
					HaveKeyWithValue("userID", []byte(image.Status.Access.User)),
					HaveKeyWithValue("userKey", []byte(image.Status.Access.UserKey)),
				)),
			)),
		))
	})

	It("should create an encrypted volume", func(ctx SpecContext) {
		By("creating a volume with encryption key")
		createResp, err := volumeClient.CreateVolume(ctx, &iriv1alpha1.CreateVolumeRequest{
			Volume: &iriv1alpha1.Volume{
				Metadata: &metav1alpha1.ObjectMetadata{
					Id: "foo-enc",
				},
				Spec: &iriv1alpha1.VolumeSpec{
					Class: "foo",
					Resources: &iriv1alpha1.VolumeResources{
						StorageBytes: 1024 * 1024 * 1024,
					},
					Encryption: &iriv1alpha1.EncryptionSpec{
						SecretData: map[string][]byte{
							"encryptionKey": []byte("abcjdkekakakakakakakkadfkkasfdks"),
						},
					},
				},
			},
		})
		Expect(err).NotTo(HaveOccurred())
		By("ensuring the correct creation response")
		Expect(createResp).Should(SatisfyAll(
			HaveField("Volume.Metadata.Id", Not(BeEmpty())),
			HaveField("Volume.Spec.Image", Equal("")),
			HaveField("Volume.Spec.Class", Equal("foo")),
			HaveField("Volume.Spec.Resources.StorageBytes", Equal(int64(1024*1024*1024))),
			HaveField("Volume.Spec.Encryption", BeNil()),
			HaveField("Volume.Status.State", Equal(iriv1alpha1.VolumeState_VOLUME_PENDING)),
			HaveField("Volume.Status.Access", BeNil()),
		))

		DeferCleanup(volumeClient.DeleteVolume, &iriv1alpha1.DeleteVolumeRequest{
			VolumeId: createResp.Volume.Metadata.Id,
		})

		By("ensuring the correct image has been created inside the ceph cluster with encryption header")
		image := &api.Image{}
		Eventually(ctx, func() *api.Image {
			oMap, err := ioctx.GetOmapValues(omap.NameVolumes, "", createResp.Volume.Metadata.Id, 10)
			Expect(err).NotTo(HaveOccurred())
			Expect(oMap).To(HaveKey(createResp.Volume.Metadata.Id))
			Expect(json.Unmarshal(oMap[createResp.Volume.Metadata.Id], image)).NotTo(HaveOccurred())
			return image
		}).Should(SatisfyAll(
			HaveField("Metadata.ID", Equal(createResp.Volume.Metadata.Id)),
			HaveField("Metadata.Labels", HaveKeyWithValue(api.ClassLabel, "foo")),
			HaveField("Spec.Image", Equal("")),
			HaveField("Spec.Size", Equal(uint64(1024*1024*1024))),
			HaveField("Spec.Limits", SatisfyAll(
				HaveKeyWithValue(api.IOPSBurstDurationLimit, int64(15)),
				HaveKeyWithValue(api.WriteIOPSLimit, int64(15000)),
				HaveKeyWithValue(api.ReadBPSLimit, int64(262144000)),
				HaveKeyWithValue(api.BPSLimit, int64(262144000)),
				HaveKeyWithValue(api.ReadIOPSLimit, int64(15000)),
				HaveKeyWithValue(api.WriteBPSLimit, int64(262144000)),
				HaveKeyWithValue(api.BPSBurstDurationLimit, int64(15)),
				HaveKeyWithValue(api.IOPSLimit, int64(15000)),
			)),
			HaveField("Spec.SnapshotRef", BeNil()),
			HaveField("Spec.Encryption.Type", api.EncryptionTypeEncrypted),
			HaveField("Spec.Encryption.EncryptedPassphrase", Not(BeEmpty())),
			HaveField("Status.State", Equal(api.ImageStateAvailable)),
			HaveField("Status.Access", SatisfyAll(
				HaveField("Monitors", cephMonitors),
				HaveField("Handle", fmt.Sprintf("%s/%s", cephPoolname, "img_"+createResp.Volume.Metadata.Id)),
				HaveField("User", strings.TrimPrefix(cephClientname, "client.")),
				HaveField("UserKey", Not(BeEmpty())),
			)),
			HaveField("Status.Encryption", api.EncryptionStateHeaderSet),
		))

		By("ensuring volume is in available state and other state fields have been updated")
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
			HaveField("Access", SatisfyAll(
				HaveField("Driver", "ceph"),
				HaveField("Handle", image.Spec.WWN),
				HaveField("Attributes", SatisfyAll(
					HaveKeyWithValue("monitors", image.Status.Access.Monitors),
					HaveKeyWithValue("image", image.Status.Access.Handle),
				)),
				HaveField("SecretData", SatisfyAll(
					HaveKeyWithValue("userID", []byte(image.Status.Access.User)),
					HaveKeyWithValue("userKey", []byte(image.Status.Access.UserKey)),
				)),
			)),
		))
	})

	It("should create a volume with snapshot data source", func(ctx SpecContext) {
		By("creating a volume with image data source")
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

		By("ensuring image has been created inside the ceph cluster")
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

		By("ensuring snapshot has been created in snapshot store")
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
			HaveField("State", Equal(iriv1alpha1.VolumeSnapshotState_VOLUME_SNAPSHOT_READY)),
			HaveField("Size", Equal(int64(1024*1024*1024))),
		))

		By("creating a volume with image data source")
		volCreateResp, err := volumeClient.CreateVolume(ctx, &iriv1alpha1.CreateVolumeRequest{
			Volume: &iriv1alpha1.Volume{
				Metadata: &metav1alpha1.ObjectMetadata{
					Id: "foo",
				},
				Spec: &iriv1alpha1.VolumeSpec{
					Class: "foo",
					Resources: &iriv1alpha1.VolumeResources{
						StorageBytes: 1024 * 1024 * 1024,
					},
					VolumeDataSource: &iriv1alpha1.VolumeDataSource{
						SnapshotDataSource: &iriv1alpha1.SnapshotDataSource{
							SnapshotId: snapshotID,
						},
					},
				},
			},
		})
		Expect(err).NotTo(HaveOccurred())

		DeferCleanup(volumeClient.DeleteVolume, &iriv1alpha1.DeleteVolumeRequest{
			VolumeId: volCreateResp.Volume.Metadata.Id,
		})

		By("ensuring image has been created inside the ceph cluster")
		volImage := &api.Image{}
		Eventually(ctx, func() *api.Image {
			oMap, err := ioctx.GetOmapValues(omap.NameVolumes, "", volCreateResp.Volume.Metadata.Id, 10)
			Expect(err).NotTo(HaveOccurred())
			Expect(oMap).To(HaveKey(volCreateResp.Volume.Metadata.Id))
			Expect(json.Unmarshal(oMap[volCreateResp.Volume.Metadata.Id], volImage)).NotTo(HaveOccurred())
			return volImage
		}).Should(SatisfyAll(
			HaveField("Metadata.ID", Equal(volCreateResp.Volume.Metadata.Id)),
			HaveField("Status.State", Equal(api.ImageStateAvailable)),
		))

		By("ensuring volume is in available state")
		Eventually(func() *iriv1alpha1.VolumeStatus {
			resp, err := volumeClient.ListVolumes(ctx, &iriv1alpha1.ListVolumesRequest{
				Filter: &iriv1alpha1.VolumeFilter{
					Id: volCreateResp.Volume.Metadata.Id,
				},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.Volumes).NotTo(BeEmpty())
			return resp.Volumes[0].Status
		}).Should(SatisfyAll(
			HaveField("State", Equal(iriv1alpha1.VolumeState_VOLUME_AVAILABLE)),
		))
	})
})
