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

	oriv1alpha1 "github.com/onmetal/cephlet/ori/volume/api/v1alpha1"
	"github.com/onmetal/cephlet/pkg/api"
	"github.com/onmetal/cephlet/pkg/omap"
	metav1alpha1 "github.com/onmetal/onmetal-api/ori/apis/meta/v1alpha1"
	onmetalv1alpha1 "github.com/onmetal/onmetal-api/ori/apis/volume/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Create Volume", func() {
	It("should get the supported volume classes", func(ctx SpecContext) {
		resp, err := volumeClient.Status(ctx, &onmetalv1alpha1.StatusRequest{})
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.VolumeClassStatus).To(Equal([]*onmetalv1alpha1.VolumeClassStatus{{
			VolumeClass: &onmetalv1alpha1.VolumeClass{
				Name: "foo",
				Capabilities: &onmetalv1alpha1.VolumeClassCapabilities{
					Tps:  100,
					Iops: 100,
				},
			},
			// TODO: fix VolumeClass stats gathering
			Quantity: 10177744896,
		}}))
	})

	It("should create a volume", func(ctx SpecContext) {
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

		resp, err := volumeClient.ListVolumes(ctx, &onmetalv1alpha1.ListVolumesRequest{
			Filter: &onmetalv1alpha1.VolumeFilter{
				Id: createResp.Volume.Metadata.Id,
			},
		})
		Expect(resp.Volumes).NotTo(BeEmpty())

		// Ensure the correct image has been created inside the ceph cluster
		omap, err := ioctx.GetOmapValues(omap.OmapNameVolumes, "", createResp.Volume.Metadata.Id, 10)
		Expect(err).NotTo(HaveOccurred())
		Expect(omap).To(HaveKey(createResp.Volume.Metadata.Id))
		image := &api.Image{}
		Expect(json.Unmarshal(omap[createResp.Volume.Metadata.Id], image)).NotTo(HaveOccurred())
		Expect(image).Should(SatisfyAll(
			// TODO: finish comparison
			HaveField("Metadata.ID", Equal(createResp.Volume.Metadata.Id)),
			HaveField("Metadata.Labels", HaveKeyWithValue(oriv1alpha1.ClassLabel, "foo")),
			HaveField("Spec.Image", Equal("")),
			HaveField("Spec.Size", Equal(uint64(1024*1024*1024))),
			HaveField("Status.State", Equal(api.ImageStatePending)),
		))
	})
})
