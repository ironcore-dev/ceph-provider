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

package server_test

import (
	corev1alpha1 "github.com/onmetal/onmetal-api/api/core/v1alpha1"
	storagev1alpha1 "github.com/onmetal/onmetal-api/api/storage/v1alpha1"
	"github.com/onmetal/onmetal-api/ori/apis/bucket/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("List bucketclass", func() {

	It("should check empty bucket classes list", func(ctx SpecContext) {
		By("creating a bucket classes")
		bucketClass1 := &storagev1alpha1.BucketClass{
			TypeMeta: metav1.TypeMeta{},
			ObjectMeta: metav1.ObjectMeta{
				Name: "bar",
			},
			Capabilities: corev1alpha1.ResourceList{
				corev1alpha1.ResourceIOPS: resource.MustParse("200"),
				corev1alpha1.ResourceTPS:  resource.MustParse("2"),
			},
		}
		Expect(k8sClient.Create(ctx, bucketClass1)).To(Succeed())

		bucketClass2 := &storagev1alpha1.BucketClass{
			TypeMeta: metav1.TypeMeta{},
			ObjectMeta: metav1.ObjectMeta{
				Name: "foo",
			},
			Capabilities: corev1alpha1.ResourceList{
				corev1alpha1.ResourceIOPS: resource.MustParse("100"),
				corev1alpha1.ResourceTPS:  resource.MustParse("1"),
			},
		}
		Expect(k8sClient.Create(ctx, bucketClass2)).To(Succeed())

		By("listing bucket class list")
		listBuckClasses, err := bucketClient.ListBucketClasses(ctx, &v1alpha1.ListBucketClassesRequest{})
		Expect(err).NotTo(HaveOccurred())
		Expect(listBuckClasses.BucketClasses).NotTo(BeEmpty())

		for _, class := range listBuckClasses.BucketClasses {
			if class.Name == "foo" {
				Expect(class.Capabilities.Iops).Should(Equal(int64(100)))
				Expect(class.Capabilities.Tps).Should(Equal(int64(1)))
			}
			if class.Name == "bar" {
				Expect(class.Capabilities.Iops).Should(Equal(int64(200)))
				Expect(class.Capabilities.Tps).Should(Equal(int64(2)))
			}
		}

		By("deleting bucket classes")
		Expect(k8sClient.Delete(ctx, bucketClass1)).To(Succeed())
		Expect(k8sClient.Delete(ctx, bucketClass2)).To(Succeed())

		By("listing bucket class to check empty list")
		Eventually(ctx, func() {
			listBuckClasses, err = bucketClient.ListBucketClasses(ctx, &v1alpha1.ListBucketClassesRequest{})
			Expect(err).NotTo(HaveOccurred())
			Expect(listBuckClasses.BucketClasses).To(BeEmpty())
		})
	})

})
