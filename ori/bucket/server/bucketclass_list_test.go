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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	oriv1alpha1 "github.com/onmetal/onmetal-api/ori/apis/bucket/v1alpha1"
)

var _ = Describe("ListBucketClasses test", func() {
	It("Should check empty BucketClasses list", func(ctx SpecContext) {
		By("Listing the available BucketClasses")
		listBuckClasses, err := bucketClient.ListBucketClasses(ctx, &oriv1alpha1.ListBucketClassesRequest{})
		Expect(err).NotTo(HaveOccurred())
		Expect(listBuckClasses.BucketClasses).NotTo(BeEmpty())
		Expect(listBuckClasses.BucketClasses).To(ContainElements(
			&oriv1alpha1.BucketClass{
				Name: "foo",
				Capabilities: &oriv1alpha1.BucketClassCapabilities{
					Tps:  1,
					Iops: 100,
				},
			},
			&oriv1alpha1.BucketClass{
				Name: "bar",
				Capabilities: &oriv1alpha1.BucketClassCapabilities{
					Tps:  2,
					Iops: 200,
				},
			},
		))
	})
})
