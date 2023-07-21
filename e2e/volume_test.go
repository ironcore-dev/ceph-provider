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

package e2e

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	librbd "github.com/ceph/go-ceph/rbd"
	corev1alpha1 "github.com/onmetal/onmetal-api/api/core/v1alpha1"
	storagev1alpha1 "github.com/onmetal/onmetal-api/api/storage/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("cephlet-volume", func() {

	// var (
	// //volumeClass *storagev1alpha1.VolumeClass
	// //volumePool  *storagev1alpha1.VolumePool
	// //rookConfig                 *rook.Config
	// //volumePoolSecretAnnotation = "ceph-client-secret-name"
	// )

	const (
		volumeSize = "10Gi"

		//TODO get from volumeclass
		fastVolClassTPS                = 100
		fastVolClassIOPS               = 100
		fastVolClassBurstFactor        = 1
		fastVolClassBurstDurationInSec = 10

	//	cephClientSecretValue = "test"
	//	snapshotSize  = "2Gi"
	//	cephPoolName  = "ceph"
	//	cephImageName = "image-1"
	)

	It("should create volume", func(ctx SpecContext) {
		By("checking that a Volume has been created")

		vol := &storagev1alpha1.Volume{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "tsi",
				Namespace: "rook-ceph",
			},
			Spec: storagev1alpha1.VolumeSpec{
				VolumeClassRef: &corev1.LocalObjectReference{Name: cephOptions.VolumeClass},
				VolumePoolRef:  &corev1.LocalObjectReference{Name: cephOptions.Pool},
				Resources: corev1alpha1.ResourceList{
					corev1alpha1.ResourceStorage: resource.MustParse(volumeSize),
				},
				Image: "ghcr.io/onmetal/onmetal-image/gardenlinux:rootfs-dev-20230223",
			},
		}
		Expect(k8sClient.Create(ctx, vol)).To(Succeed())

		time.Sleep(6 * time.Second)
		volume := &storagev1alpha1.Volume{}
		ns := types.NamespacedName{Namespace: "rook-ceph", Name: "tsi"}
		Expect(k8sClient.Get(ctx, ns, volume)).To(Succeed())
		if volume.Status.State == "Available" {
			imageName := strings.Split(volume.Status.Access.VolumeAttributes["image"], "/")[1]

			fmt.Println("RBD Image details", imageName)
			img1, err := librbd.OpenImage(ioCtx, imageName, librbd.NoSnapshot)
			if err != nil {
				fmt.Println("Errrorrrrrrrr", err)

			}
			metaData, err := img1.ListMetadata()
			fmt.Println("Metadata of image is::::::::::::::: :", metaData)
			Expect(err).NotTo(HaveOccurred())
			fmt.Println("Volume Image for TSI::::::::::::", imageName)

			// TPS/BPS Data
			tpsBurstLimit := fastVolClassBurstFactor * fastVolClassTPS
			tpsBurst := tpsBurstLimit * fastVolClassBurstDurationInSec

			setTPSBurstLimit, err := strconv.Atoi(metaData["conf_rbd_qos_bps_limit"])
			if err != nil {
				panic(err)
			}
			setTPSBurst, err := strconv.Atoi(metaData["conf_rbd_qos_read_bps_burst"])
			if err != nil {
				panic(err)
			}
			// IOPS Data
			iopsBurstLimit := fastVolClassBurstFactor * fastVolClassIOPS
			iopsBurst := iopsBurstLimit * fastVolClassBurstDurationInSec

			setIOPSBurst, err := strconv.Atoi(metaData["conf_rbd_qos_iops_burst"])
			if err != nil {
				panic(err)
			}

			setIOPSBurstLimit, err := strconv.Atoi(metaData["conf_rbd_qos_iops_limit"])
			if err != nil {
				panic(err)
			}

			fmt.Println("set isssssssssssssss", setTPSBurst)
			fmt.Println("expected issssssssssssssssss", tpsBurst)

			Expect(setIOPSBurstLimit).To(Equal(iopsBurstLimit))
			Expect(setTPSBurstLimit).To(Equal(tpsBurstLimit))
			Expect(setTPSBurst).To(Equal(tpsBurst))
			Expect(setIOPSBurst).To(Equal(iopsBurst))

			/*
				limits[rbd_qos_iops_burst] = iopsBurstLimit
				limits[rbd_qos_read_iops_burst] = iopsBurstLimit
				limits[rbd_qos_write_iops_burst] = iopsBurstLimit
			*/

		} else {
			fmt.Println("Though the volume created, image is not created because Cephlet and Volumepoollet is not running.")
		}

		// Eventually(Object(volume)).Should(SatisfyAll(
		// 	HaveField(volume.Status.State, "Available"),
		// ))

		fmt.Println("Here the Volume is getting created############", vol.Name)
	})

	It("Should get the volume", func(ctx SpecContext) {
		volume := &storagev1alpha1.Volume{}
		ns := types.NamespacedName{Namespace: "rook-ceph", Name: "tsi"}
		Expect(k8sClient.Get(ctx, ns, volume)).To(Succeed())
		fmt.Println("Here the Volume is getting listed##############", volume.Name)

		// Todo use matcher
		//Expect(volume.Name).To(Succeed())
		//Expect(k8sClient.List(ctx, volumeList, client.InNamespace("rook-ceph"))).To(Succeed())
	})

	It("Should delete volume", func(ctx SpecContext) {

		volume := &storagev1alpha1.Volume{}
		//volumeList := &storagev1alpha1.vol
		ns := types.NamespacedName{Namespace: "rook-ceph", Name: "tsi"}
		err := k8sClient.Get(ctx, ns, volume)
		imageName := strings.Split(volume.Status.Access.VolumeAttributes["image"], "/")[1]
		if err != nil {

		}
		deleteResult := k8sClient.Delete(ctx, volume)
		//fmt.Println(deleteResult)
		time.Sleep(6 * time.Second)
		Expect(deleteResult).To(Succeed())
		fmt.Println("Here the Volume is getting deleted which was ealier created.###########", volume.Name)

		Expect(k8sClient.Get(ctx, ns, volume)).To(Succeed())
		if volume.Status.State != "" {
			fmt.Println("Image is deleted completely which is", imageName)
		} else {
			fmt.Println("Image/Volume not deleted completely, it is there")
		}

	})

})
