// SPDX-FileCopyrightText: 2023 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package server_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	iriv1alpha1 "github.com/ironcore-dev/ironcore/iri/apis/bucket/v1alpha1"
)

var _ = Describe("ListBucketClasses test", func() {
	It("Should check BucketClasses list", func(ctx SpecContext) {
		By("Listing the available BucketClasses")
		listBuckClasses, err := bucketClient.ListBucketClasses(ctx, &iriv1alpha1.ListBucketClassesRequest{})
		Expect(err).NotTo(HaveOccurred())
		Expect(listBuckClasses.BucketClasses).NotTo(BeEmpty())
		Expect(listBuckClasses.BucketClasses).To(ContainElements(
			&iriv1alpha1.BucketClass{
				Name: "foo",
				Capabilities: &iriv1alpha1.BucketClassCapabilities{
					Tps:  1,
					Iops: 100,
				},
			},
			&iriv1alpha1.BucketClass{
				Name: "bar",
				Capabilities: &iriv1alpha1.BucketClassCapabilities{
					Tps:  2,
					Iops: 200,
				},
			},
		))
	})
})
