package controllers

import (
	storagev1alpha1 "github.com/onmetal/onmetal-api/apis/storage/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("VolumePoolReconciler", func() {
	_ = SetupTest(ctx)

	It("should announce volumepool", func() {
		By("checking that a VolumePool has been created")
		volumePool := &storagev1alpha1.VolumePool{}
		volumePoolKey := types.NamespacedName{Name: volumePoolName}
		Expect(k8sClient.Get(ctx, volumePoolKey, volumePool)).Should(Succeed())
	})
})
