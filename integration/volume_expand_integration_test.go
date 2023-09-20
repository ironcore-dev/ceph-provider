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
	"os"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	oriv1alpha1 "github.com/onmetal/cephlet/ori/volume/api/v1alpha1"
	"github.com/onmetal/cephlet/pkg/api"
	"github.com/onmetal/cephlet/pkg/omap"
	metav1alpha1 "github.com/onmetal/onmetal-api/ori/apis/meta/v1alpha1"
	onmetalv1alpha1 "github.com/onmetal/onmetal-api/ori/apis/volume/v1alpha1"
)

var _ = Describe("Expand Volume", func() {
	It("should expand a volume", func(ctx SpecContext) {
		By("creating a volume")
		createResp, err := volumeClient.CreateVolume(ctx, &onmetalv1alpha1.CreateVolumeRequest{
			Volume: &onmetalv1alpha1.Volume{
				Metadata: &metav1alpha1.ObjectMetadata{
					Id: "foo",
				},
				Spec: &onmetalv1alpha1.VolumeSpec{
					Class: "foo",
					Resources: &onmetalv1alpha1.VolumeResources{
						StorageBytes: 1024 * 1024 * 1024,
					},
				},
			},
			XXX_sizecache: 1024,
		})
		Expect(err).NotTo(HaveOccurred())
		// Ensure the correct creation response
		Expect(createResp).Should(SatisfyAll(
			HaveField("Volume.Metadata.Id", Not(BeEmpty())),
			HaveField("Volume.Spec.Image", Equal("")),
			HaveField("Volume.Spec.Class", Equal("foo")),
			HaveField("Volume.Spec.Resources.StorageBytes", Equal(uint64(1024*1024*1024))),
			HaveField("Volume.Spec.Encryption", BeNil()),
			HaveField("Volume.Status.State", Equal(onmetalv1alpha1.VolumeState_VOLUME_PENDING)),
			HaveField("Volume.Status.Access", BeNil()),
		))

		// Ensure the correct image has been created inside the ceph cluster
		oMap, err := ioctx.GetOmapValues(oMap.OmapNameVolumes, "", createResp.Volume.Metadata.Id, 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(oMap).To(HaveKey(createResp.Volume.Metadata.Id))
		image := &api.Image{}
		Expect(json.Unmarshal(oMap[createResp.Volume.Metadata.Id], image)).NotTo(HaveOccurred())
		Expect(image).Should(SatisfyAll(
			HaveField("Metadata.ID", Equal(createResp.Volume.Metadata.Id)),
			HaveField("Metadata.Labels", HaveKeyWithValue(oriv1alpha1.ClassLabel, "foo")),
			HaveField("Spec.Image", Equal("")),
			HaveField("Spec.Size", Equal(uint64(1024*1024*1024))),
			HaveField("Status.State", Equal(api.ImageStatePending)),
		))

		// Wait for image to become Available
		time.Sleep(2 * time.Second)
		oMap, err = ioctx.GetOmapValues(oMap.OmapNameVolumes, "", createResp.Volume.Metadata.Id, 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(oMap).To(HaveKey(createResp.Volume.Metadata.Id))
		Expect(json.Unmarshal(oMap[createResp.Volume.Metadata.Id], image)).NotTo(HaveOccurred())
		if image.Status.State == api.ImageStateAvailable {
			Expect(image.Status).Should(SatisfyAll(
				HaveField("State", Equal(api.ImageStateAvailable)),
				HaveField("Access", SatisfyAll(
					HaveField("Monitors", os.Getenv("CEPH_MONITORS")),
					HaveField("Handle", fmt.Sprintf("%s/%s", os.Getenv("CEPH_POOLNAME"), "img_"+image.ID)),
					HaveField("User", strings.TrimPrefix(os.Getenv("CEPH_CLIENTNAME"), "client.")),
					HaveField("UserKey", image.Status.Access.UserKey),
				)),
				HaveField("Encryption", api.EncryptionState("")),
			))
		}

		// Wait for Volume to become available
		time.Sleep(2 * time.Second)
		resp, err := volumeClient.ListVolumes(ctx, &onmetalv1alpha1.ListVolumesRequest{
			Filter: &onmetalv1alpha1.VolumeFilter{
				Id: createResp.Volume.Metadata.Id,
			},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Volumes).NotTo(BeEmpty())
		if resp.Volumes[0].Status.State == onmetalv1alpha1.VolumeState_VOLUME_AVAILABLE {
			Expect(resp.Volumes[0].Status).Should(SatisfyAll(
				HaveField("State", Equal(onmetalv1alpha1.VolumeState_VOLUME_AVAILABLE)),
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
		}

		By("expanding a volume")
		expandResp, err := volumeClient.ExpandVolume(ctx, &onmetalv1alpha1.ExpandVolumeRequest{
			VolumeId: createResp.Volume.Metadata.Id,
			Resources: &onmetalv1alpha1.VolumeResources{
				StorageBytes: 2048 * 2048 * 2048,
			},
		})
		Expect(err).NotTo(HaveOccurred())
		// Ensure the correct  response
		Expect(err).NotTo(HaveOccurred())
		Expect(expandResp.XXX_sizecache).Should(Equal(1024))

		// Ensure the image size has been updated inside the ceph cluster
		oMap, err := ioctx.GetOmapValues(oMap.OmapNameVolumes, "", createResp.Volume.Metadata.Id, 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(oMap).To(HaveKey(createResp.Volume.Metadata.Id))
		image := &api.Image{}
		Expect(json.Unmarshal(oMap[createResp.Volume.Metadata.Id], image)).NotTo(HaveOccurred())
		Expect(image).Should(SatisfyAll(
			HaveField("Metadata.ID", Equal(createResp.Volume.Metadata.Id)),
			HaveField("Metadata.Labels", HaveKeyWithValue(oriv1alpha1.ClassLabel, "foo")),
			HaveField("Spec.Image", Equal("")),
			HaveField("Spec.Size", Equal(uint64(2048*2048*2048))),
			HaveField("Status.State", Equal(api.ImageStatePending)),
		))

		// Wait for Volume to become available
		time.Sleep(2 * time.Second)
		resp, err := volumeClient.ListVolumes(ctx, &onmetalv1alpha1.ListVolumesRequest{
			Filter: &onmetalv1alpha1.VolumeFilter{
				Id: createResp.Volume.Metadata.Id,
			},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.Volumes).NotTo(BeEmpty())
		if resp.Volumes[0].Status.State == onmetalv1alpha1.VolumeState_VOLUME_AVAILABLE {
			Expect(resp.Volumes[0].Status).Should(SatisfyAll(
				HaveField("State", Equal(onmetalv1alpha1.VolumeState_VOLUME_AVAILABLE)),
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
		}
	})
})
