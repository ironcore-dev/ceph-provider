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
	"time"

	metav1alpha1 "github.com/onmetal/onmetal-api/ori/apis/meta/v1alpha1"
	onmetalv1alpha1 "github.com/onmetal/onmetal-api/ori/apis/volume/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Delete Volume", func() {

	It("should delete a volume", func(ctx SpecContext) {
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

		// Wait for Volume to be created
		time.Sleep(2 * time.Second)

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

		By("deleting a volume")
		deleteResp, err := volumeClient.DeleteVolume(ctx, &onmetalv1alpha1.DeleteVolumeRequest{
			VolumeId:      createResp.Volume.Metadata.Id,
			XXX_sizecache: 1024,
		})

		// Wait for Volume to be deleted
		time.Sleep(2 * time.Second)

		Expect(err).NotTo(HaveOccurred())
		Expect(deleteResp.XXX_sizecache).Should(Equal(1024))

		listResp, err := volumeClient.ListVolumes(ctx, &onmetalv1alpha1.ListVolumesRequest{
			Filter: &onmetalv1alpha1.VolumeFilter{
				Id: createResp.Volume.Metadata.Id,
			},
		})

		// Wait for Volume list to be updated
		time.Sleep(2 * time.Second)

		Expect(err).NotTo(HaveOccurred())
		for _, vol := range listResp.Volumes {
			Expect(vol.Metadata.Id).NotTo(Equal(createResp.Volume.Metadata.Id))
		}
	})
})
