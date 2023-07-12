package e2e

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
//	"sigs.k8s.io/controller-runtime/pkg/client"

	storagev1alpha1 "github.com/onmetal/onmetal-api/api/storage/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	. "github.com/onsi/gomega"

)



var _ = Describe("cephlet-volumepool", func() {
		var (
		volumePool  *storagev1alpha1.VolumePool
		)

		It("VolumePool Creation", func(ctx SpecContext) {
			By("checking that a VolumePool has been created")
			volumePool = &storagev1alpha1.VolumePool{
				ObjectMeta: metav1.ObjectMeta{
					Name: "volumepool-testcase",
				},
				Spec: storagev1alpha1.VolumePoolSpec{
					ProviderID: "cephlet",
				},
			}
		Expect(k8sClient.Create(ctx, volumePool)).Should(Succeed())
		fmt.Println("Here the Volumepool is getting created********", volumePool.Name)
	
	

		By("issuing a delete request for the volume pool")
		Expect(k8sClient.Delete(ctx, volumePool)).Should(Succeed())
		fmt.Println("Here the VolumePool is getting deleted********", volumePool.Name)
	})

})
