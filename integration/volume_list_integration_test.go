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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/onmetal/cephlet/pkg/api"
	"github.com/onmetal/cephlet/pkg/omap"
	metav1alpha1 "github.com/onmetal/onmetal-api/ori/apis/meta/v1alpha1"
	onmetalv1alpha1 "github.com/onmetal/onmetal-api/ori/apis/volume/v1alpha1"
)

var _ = Describe("List Volume", func() {
	It("should create a volume", func(ctx SpecContext) {
		By("creating a volume")
		createResp, err := volumeClient.CreateVolume(ctx, &onmetalv1alpha1.CreateVolumeRequest{
			Volume: &onmetalv1alpha1.Volume{
				Metadata: &metav1alpha1.ObjectMetadata{
					Id:     "foo",
					Labels: map[string]string{"foo": "bar"},
				},
				Spec: &onmetalv1alpha1.VolumeSpec{
					Class: "foo",
					Resources: &onmetalv1alpha1.VolumeResources{
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

		By("listing volume with volume id")
		Eventually(func() *onmetalv1alpha1.VolumeStatus {
			resp, err := volumeClient.ListVolumes(ctx, &onmetalv1alpha1.ListVolumesRequest{
				Filter: &onmetalv1alpha1.VolumeFilter{
					Id: createResp.Volume.Metadata.Id,
				},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.Volumes).NotTo(BeEmpty())
			return resp.Volumes[0].Status
		}).Should(SatisfyAll(
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

		By("listing volume with correct Label selectors")
		Eventually(func() *onmetalv1alpha1.VolumeStatus {
			resp, err := volumeClient.ListVolumes(ctx, &onmetalv1alpha1.ListVolumesRequest{
				Filter: &onmetalv1alpha1.VolumeFilter{
					LabelSelector: map[string]string{"foo": "bar"},
				},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.Volumes).NotTo(BeEmpty())
			return resp.Volumes[0].Status
		}).Should(SatisfyAll(
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

		By("listing volume with incorrect Labels ")
		Eventually(func() {
			resp, err := volumeClient.ListVolumes(ctx, &onmetalv1alpha1.ListVolumesRequest{
				Filter: &onmetalv1alpha1.VolumeFilter{
					LabelSelector: map[string]string{"foo": "wrong"},
				},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.Volumes).To(BeEmpty())
		})
	})
})
