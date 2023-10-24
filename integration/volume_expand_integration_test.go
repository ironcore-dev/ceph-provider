// Copyright 2023 OnMetal authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package integration

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/onmetal/cephlet/ori/volume/apiutils"
	"github.com/onmetal/cephlet/pkg/api"
	"github.com/onmetal/cephlet/pkg/omap"
	metav1alpha1 "github.com/onmetal/onmetal-api/ori/apis/meta/v1alpha1"
	oriv1alpha1 "github.com/onmetal/onmetal-api/ori/apis/volume/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Expand Volume", func() {
	It("should expand a volume", func(ctx SpecContext) {
		By("creating a volume")
		createResp, err := volumeClient.CreateVolume(ctx, &oriv1alpha1.CreateVolumeRequest{
			Volume: &oriv1alpha1.Volume{
				Metadata: &metav1alpha1.ObjectMetadata{
					Id: "foo",
				},
				Spec: &oriv1alpha1.VolumeSpec{
					Class: "foo",
					Resources: &oriv1alpha1.VolumeResources{
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
			HaveField("Volume.Status.State", Equal(oriv1alpha1.VolumeState_VOLUME_PENDING)),
			HaveField("Volume.Status.Access", BeNil()),
		))

		DeferCleanup(volumeClient.DeleteVolume, &oriv1alpha1.DeleteVolumeRequest{
			VolumeId: createResp.Volume.Metadata.Id,
		})

		By("ensuring the correct image has been created inside the ceph cluster")
		image := &api.Image{}
		Eventually(func() *api.Image {
			oMap, err := ioctx.GetOmapValues(omap.OmapNameVolumes, "", createResp.Volume.Metadata.Id, 10)
			Expect(err).NotTo(HaveOccurred())
			Expect(oMap).To(HaveKey(createResp.Volume.Metadata.Id))
			Expect(json.Unmarshal(oMap[createResp.Volume.Metadata.Id], image)).NotTo(HaveOccurred())
			return image
		}).Should(SatisfyAll(
			HaveField("Metadata.ID", Equal(createResp.Volume.Metadata.Id)),
			HaveField("Metadata.Labels", HaveKeyWithValue(apiutils.ClassLabel, "foo")),
			HaveField("Spec.Image", Equal("")),
			HaveField("Spec.Size", Equal(uint64(1024*1024*1024))),
			HaveField("Spec.Limits", SatisfyAll(
				HaveKeyWithValue(api.IOPSBurstDurationLimit, int64(15)),
				HaveKeyWithValue(api.WriteIOPSLimit, int64(100)),
				HaveKeyWithValue(api.ReadBPSLimit, int64(100)),
				HaveKeyWithValue(api.BPSLimit, int64(100)),
				HaveKeyWithValue(api.ReadIOPSLimit, int64(100)),
				HaveKeyWithValue(api.WriteBPSLimit, int64(100)),
				HaveKeyWithValue(api.BPSBurstDurationLimit, int64(15)),
				HaveKeyWithValue(api.IOPSlLimit, int64(100)),
			)),
			HaveField("Spec.SnapshotRef", BeNil()),
			HaveField("Spec.Encryption", SatisfyAll(
				HaveField("Type", api.EncryptionTypeUnencrypted),
			)),
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
		Eventually(func() *oriv1alpha1.VolumeStatus {
			resp, err := volumeClient.ListVolumes(ctx, &oriv1alpha1.ListVolumesRequest{
				Filter: &oriv1alpha1.VolumeFilter{
					Id: createResp.Volume.Metadata.Id,
				},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.Volumes).NotTo(BeEmpty())
			return resp.Volumes[0].Status
		}).Should(SatisfyAll(
			HaveField("State", Equal(oriv1alpha1.VolumeState_VOLUME_AVAILABLE)),
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

		By("expanding a volume")
		_, err = volumeClient.ExpandVolume(ctx, &oriv1alpha1.ExpandVolumeRequest{
			VolumeId: createResp.Volume.Metadata.Id,
			Resources: &oriv1alpha1.VolumeResources{
				StorageBytes: 2048 * 2048 * 2048,
			},
		})
		Expect(err).NotTo(HaveOccurred())

		By("ensuring image size has been updated inside the ceph cluster after expand")
		Eventually(func() *api.Image {
			oMap, err := ioctx.GetOmapValues(omap.OmapNameVolumes, "", createResp.Volume.Metadata.Id, 10)
			Expect(err).NotTo(HaveOccurred())
			Expect(oMap).To(HaveKey(createResp.Volume.Metadata.Id))
			Expect(json.Unmarshal(oMap[createResp.Volume.Metadata.Id], image)).NotTo(HaveOccurred())
			return image
		}).Should(SatisfyAll(
			HaveField("Metadata.ID", Equal(createResp.Volume.Metadata.Id)),
			HaveField("Metadata.Labels", HaveKeyWithValue(apiutils.ClassLabel, "foo")),
			HaveField("Spec.Image", Equal("")),
			HaveField("Spec.Size", Equal(uint64(2048*2048*2048))),
			HaveField("Spec.Limits", SatisfyAll(
				HaveKeyWithValue(api.IOPSBurstDurationLimit, int64(15)),
				HaveKeyWithValue(api.WriteIOPSLimit, int64(100)),
				HaveKeyWithValue(api.ReadBPSLimit, int64(100)),
				HaveKeyWithValue(api.BPSLimit, int64(100)),
				HaveKeyWithValue(api.ReadIOPSLimit, int64(100)),
				HaveKeyWithValue(api.WriteBPSLimit, int64(100)),
				HaveKeyWithValue(api.BPSBurstDurationLimit, int64(15)),
				HaveKeyWithValue(api.IOPSlLimit, int64(100)),
			)),
			HaveField("Spec.SnapshotRef", BeNil()),
			HaveField("Spec.Encryption", SatisfyAll(
				HaveField("Type", api.EncryptionTypeUnencrypted),
			)),
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
		Eventually(func() *oriv1alpha1.VolumeStatus {
			resp, err := volumeClient.ListVolumes(ctx, &oriv1alpha1.ListVolumesRequest{
				Filter: &oriv1alpha1.VolumeFilter{
					Id: createResp.Volume.Metadata.Id,
				},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.Volumes).NotTo(BeEmpty())
			return resp.Volumes[0].Status
		}).Should(SatisfyAll(
			HaveField("State", Equal(oriv1alpha1.VolumeState_VOLUME_AVAILABLE)),
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
})
