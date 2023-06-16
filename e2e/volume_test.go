package e2e

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
)

var _ = Describe("cephlet-volume", func() {
	It("should create a basic volume", func(ctx SpecContext) {

		//todo
		podList := corev1.PodList{}
		Expect(k8sClient.List(ctx, &podList)).To(Succeed())
	})
})
