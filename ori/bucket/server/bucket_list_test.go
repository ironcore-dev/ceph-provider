// Copyright 2023 OnMetal authors
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

package server_test

import (
	"fmt"

	objectbucketv1alpha1 "github.com/kube-object-storage/lib-bucket-provisioner/pkg/apis/objectbucket.io/v1alpha1"
	oriv1alpha1 "github.com/onmetal/onmetal-api/ori/apis/bucket/v1alpha1"
	orimetav1alpha1 "github.com/onmetal/onmetal-api/ori/apis/meta/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	. "sigs.k8s.io/controller-runtime/pkg/envtest/komega"
)

var _ = Describe("ListBucket test", func() {
	It("Should list buckets", func(ctx SpecContext) {
		By("Creating a bucket")
		createResp, err := bucketClient.CreateBucket(ctx, &oriv1alpha1.CreateBucketRequest{
			Bucket: &oriv1alpha1.Bucket{
				Metadata: &orimetav1alpha1.ObjectMetadata{
					Labels: map[string]string{"foo": "bar"},
				},
				Spec: &oriv1alpha1.BucketSpec{
					Class: "foo",
				},
			},
		})
		Expect(err).NotTo(HaveOccurred())

		By("Ensuring the correct creation response")
		Expect(createResp).Should(SatisfyAll(
			HaveField("Bucket.Metadata.Id", Equal(createResp.Bucket.Metadata.Id)),
			HaveField("Bucket.Spec.Class", Equal("foo")),
			HaveField("Bucket.Status.State", Equal(oriv1alpha1.BucketState_BUCKET_PENDING)),
			HaveField("Bucket.Status.Access", BeNil()),
		))

		DeferCleanup(bucketClient.DeleteBucket, &oriv1alpha1.DeleteBucketRequest{
			BucketId: createResp.Bucket.Metadata.Id,
		})

		By("Ensuring the bucketClaim is created")
		bucketClaim := &objectbucketv1alpha1.ObjectBucketClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      createResp.Bucket.Metadata.Id,
				Namespace: rookNamespace.Name,
			},
		}
		Eventually(Object(bucketClaim)).Should(SatisfyAll(
			HaveField("Spec.StorageClassName", "foo"),
			HaveField("Spec.GenerateBucketName", createResp.Bucket.Metadata.Id),
		))

		By("Patching BucketName in BucketClaim Spec with GenerateBucketName")
		bucketClaimBase := bucketClaim.DeepCopy()
		bucketClaim.Spec.BucketName = createResp.Bucket.Metadata.Id
		Expect(k8sClient.Patch(ctx, bucketClaim, client.MergeFrom(bucketClaimBase))).To(Succeed())

		By("Patching the bucketClaim status phase to be bound")
		updatedBucketClaimBase := bucketClaim.DeepCopy()
		bucketClaim.Status.Phase = objectbucketv1alpha1.ObjectBucketClaimStatusPhaseBound
		bucketClaim.Spec.BucketName = createResp.Bucket.Metadata.Id
		Expect(k8sClient.Status().Patch(ctx, bucketClaim, client.MergeFrom(updatedBucketClaimBase))).To(Succeed())

		By("Creating a bucket access secret")
		secretData := map[string][]byte{
			"AccessKeyID":     []byte("foo"),
			"SecretAccessKey": []byte("bar"),
		}
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      bucketClaim.Name,
				Namespace: rookNamespace.Name,
			},
			Type: corev1.SecretTypeOpaque,
			Data: secretData,
		}
		Expect(k8sClient.Create(ctx, secret)).To(Succeed())

		By("Ensuring the bucket access secret is created")
		accessSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      bucketClaim.Name,
				Namespace: rookNamespace.Name,
			},
		}
		Eventually(Get(accessSecret)).Should(Succeed())

		By("Listing Bucket with Bucket id")
		Eventually(func() *oriv1alpha1.BucketStatus {
			resp, err := bucketClient.ListBuckets(ctx, &oriv1alpha1.ListBucketsRequest{
				Filter: &oriv1alpha1.BucketFilter{
					Id: createResp.Bucket.Metadata.Id,
				},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.Buckets).NotTo(BeEmpty())
			return resp.Buckets[0].Status
		}).Should(SatisfyAll(
			HaveField("State", Equal(oriv1alpha1.BucketState_BUCKET_AVAILABLE)),
			HaveField("Access", SatisfyAll(
				HaveField("Endpoint", Equal(fmt.Sprintf("%s.%s", bucketClaim.Name, bucketBaseURL))),
				HaveField("SecretData", SatisfyAll(
					HaveKeyWithValue("AccessKeyID", []byte("foo")),
					HaveKeyWithValue("SecretAccessKey", []byte("bar")),
				)),
			)),
		))

		By("Listing bucket with correct label selector")
		Eventually(func() *oriv1alpha1.BucketStatus {
			resp, err := bucketClient.ListBuckets(ctx, &oriv1alpha1.ListBucketsRequest{
				Filter: &oriv1alpha1.BucketFilter{
					LabelSelector: map[string]string{"foo": "bar"},
				},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.Buckets).NotTo(BeEmpty())
			return resp.Buckets[0].Status
		}).Should(SatisfyAll(
			HaveField("State", Equal(oriv1alpha1.BucketState_BUCKET_AVAILABLE)),
			HaveField("Access", SatisfyAll(
				HaveField("Endpoint", Equal(fmt.Sprintf("%s.%s", bucketClaim.Name, bucketBaseURL))),
				HaveField("SecretData", SatisfyAll(
					HaveKeyWithValue("AccessKeyID", []byte("foo")),
					HaveKeyWithValue("SecretAccessKey", []byte("bar")),
				)),
			)),
		))

		By("Listing bucket with incorrect label selector ")
		Eventually(func() {
			resp, err := bucketClient.ListBuckets(ctx, &oriv1alpha1.ListBucketsRequest{
				Filter: &oriv1alpha1.BucketFilter{
					LabelSelector: map[string]string{"foo": "wrong"},
				},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.Buckets).To(BeEmpty())
		})
	})
})
