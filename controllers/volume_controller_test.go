package controllers

import (
	storagev1alpha1 "github.com/onmetal/onmetal-api/apis/storage/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	rookv1 "github.com/rook/rook/pkg/apis/ceph.rook.io/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

const (
	volSize = "1Gi"
)

var _ = Describe("VolumeReconciler", func() {
	testNs, rookNs := SetupTest(ctx)

	var (
		volumeClass *storagev1alpha1.VolumeClass
	)

	BeforeEach(func() {
		volumeClass = &storagev1alpha1.VolumeClass{
			TypeMeta: metav1.TypeMeta{},
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "sc-",
			},
		}
		Expect(k8sClient.Create(ctx, volumeClass)).To(Succeed())
	})

	FIt("should announce volumepool", func() {
		By("checking that a VolumePool has been created")
		vol := &storagev1alpha1.Volume{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "volume-",
				Namespace:    testNs.Name,
			},
			Spec: storagev1alpha1.VolumeSpec{
				VolumeClassRef: corev1.LocalObjectReference{Name: volumeClass.Name},
				VolumePoolRef:  &corev1.LocalObjectReference{Name: volumePoolName},
				Resources: corev1.ResourceList{
					"storage": quantity(volSize),
				},
			},
		}
		Expect(k8sClient.Create(ctx, vol)).To(Succeed())

		By("ceph namespace created but not ready")
		cephNs := rookv1.CephBlockPoolRadosNamespace{}
		Eventually(func() error {
			return k8sClient.Get(ctx, types.NamespacedName{
				Namespace: rookNs.Name,
				//namespace represents customer
				Name: testNs.Name,
			}, &cephNs)
		}).Should(Succeed())

	})

})

func quantity(s string) resource.Quantity {
	return resource.MustParse(s)
}
