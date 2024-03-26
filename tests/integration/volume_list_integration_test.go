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
)

var _ = Describe("List Volume", func() {
	It("should create a volume", func(ctx SpecContext) {
		By("creating a volume")
		createResp, err := volumeClient.CreateVolume(ctx, &iriv1alpha1.CreateVolumeRequest{
			Volume: &iriv1alpha1.Volume{
				Metadata: &metav1alpha1.ObjectMetadata{
					Id:     "foo",
					Labels: map[string]string{"foo": "bar"},
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
			HaveField("Status.State", Equal(api.ImageStateAvailable)),
			HaveField("Status.Access", SatisfyAll(
				HaveField("Monitors", cephMonitors),
				HaveField("Handle", fmt.Sprintf("%s/%s", cephPoolname, "img_"+createResp.Volume.Metadata.Id)),
				HaveField("User", strings.TrimPrefix(cephClientname, "client.")),
				HaveField("UserKey", Not(BeEmpty())),
			)),
			HaveField("Status.Encryption", api.EncryptionState("")),
		))

		DeferCleanup(volumeClient.DeleteVolume, &iriv1alpha1.DeleteVolumeRequest{
			VolumeId: createResp.Volume.Metadata.Id,
		})

		By("listing volume with volume id")
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

		By("listing volume with correct Label selectors")
		Eventually(func() *iriv1alpha1.VolumeStatus {
			resp, err := volumeClient.ListVolumes(ctx, &iriv1alpha1.ListVolumesRequest{
				Filter: &iriv1alpha1.VolumeFilter{
					LabelSelector: map[string]string{"foo": "bar"},
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

		By("listing volume with incorrect Labels ")
		Eventually(func() {
			resp, err := volumeClient.ListVolumes(ctx, &iriv1alpha1.ListVolumesRequest{
				Filter: &iriv1alpha1.VolumeFilter{
					LabelSelector: map[string]string{"foo": "wrong"},
				},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.Volumes).To(BeEmpty())
		})
	})
})
