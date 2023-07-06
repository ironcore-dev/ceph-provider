package e2e

import (
	//"github.com/onmetal/onmetal-api/testutils"
	//"flag"
	"fmt"

	. "github.com/onsi/ginkgo/v2"

	//. "github.com/onsi/gomega"

	corev1alpha1 "github.com/onmetal/onmetal-api/api/core/v1alpha1"
	storagev1alpha1 "github.com/onmetal/onmetal-api/api/storage/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	//"k8s.io/apimachinery/pkg/types

	//testutils "github.com/onmetal/onmetal-api/utils/testing"

	//. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	//. "sigs.k8s.io/controller-runtime/pkg/envtest/komega"
)

var _ = Describe("cephlet-volume", func() {

	// var (
	// 	volumeClass *storagev1alpha1.VolumeClass
	// 	volumePool  *storagev1alpha1.VolumePool
	// )

	const (
		volumeSize = "1Gi"
	//	snapshotSize  = "2Gi"
	//	cephPoolName  = "ceph"
	//	cephImageName = "image-1"
	)

	It("should create volume", func(ctx SpecContext) {
		By("checking that a Volume has been created")
		vol := &storagev1alpha1.Volume{
			ObjectMeta: metav1.ObjectMeta{
				Name: "tsi",
				//GenerateName: "volume-",
				Namespace: "rook-ceph",
			},
			Spec: storagev1alpha1.VolumeSpec{
				VolumeClassRef: &corev1.LocalObjectReference{Name: "fast"},
				VolumePoolRef:  &corev1.LocalObjectReference{Name: "ceph"},
				Resources: corev1alpha1.ResourceList{
					corev1alpha1.ResourceStorage: resource.MustParse(volumeSize),
				},
			},
		}
		Expect(k8sClient.Create(ctx, vol)).To(Succeed())
	})

	It("Should get the volume", func(ctx SpecContext) {
		fmt.Println("Gingo working...........")

		volume := &storagev1alpha1.Volume{}
		ns := types.NamespacedName{Namespace: "rook-ceph", Name: "tsi"}
		Expect(k8sClient.Get(ctx, ns, volume)).To(Succeed())
		fmt.Println(volume.Name)

		// Todo use matcher
		//Expect(volume.Name).To(Succeed())

		//Expect(k8sClient.List(ctx, volumeList, client.InNamespace("rook-ceph"))).To(Succeed())
	})

	It("Should delete volume", func(ctx SpecContext) {
		fmt.Println("Gingo working...........")

		volume := &storagev1alpha1.Volume{}
		//volumeList := &storagev1alpha1.vol
		ns := types.NamespacedName{Namespace: "rook-ceph", Name: "tsi"}
		err := k8sClient.Get(ctx, ns, volume)
		if err != nil {

		}
		deleteResult := k8sClient.Delete(ctx, volume)
		fmt.Println(deleteResult)
		Expect(deleteResult).To(Succeed())
		fmt.Println(volume.Name)
		// Expect(deleteResult).Should(compareDeleteResult)

		// func () compareDeleteResult(a error) {
		// 	if a != nil {
		// 		return false
		// 	 }
		// 	 return true
		// }

	})

})
