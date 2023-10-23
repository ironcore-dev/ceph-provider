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
	"fmt"
	objectbucketv1alpha1 "github.com/kube-object-storage/lib-bucket-provisioner/pkg/apis/objectbucket.io/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	. "sigs.k8s.io/controller-runtime/pkg/envtest/komega"

	ori "github.com/onmetal/onmetal-api/ori/apis/bucket/v1alpha1"
	"github.com/onmetal/onmetal-api/ori/apis/meta/v1alpha1"
)

var _ = Describe("DeleteBucket test", func() {
	It("Should delete a bucket", func(ctx SpecContext) {
		By("Creating a bucket")
		createResp, err := bucketClient.CreateBucket(ctx, &ori.CreateBucketRequest{
			Bucket: &ori.Bucket{
				Metadata: &v1alpha1.ObjectMetadata{
					Labels: map[string]string{"foo": "bar"},
				},
				Spec: &ori.BucketSpec{
					Class: "foo",
				},
			},
		})
		Expect(err).NotTo(HaveOccurred())

		By("Ensuring the correct creation response")
		Expect(createResp).Should(SatisfyAll(
			HaveField("Bucket.Metadata.Id", Equal(createResp.Bucket.Metadata.Id)),
			HaveField("Bucket.Spec.Class", Equal("foo")),
			HaveField("Bucket.Status.State", Equal(ori.BucketState_BUCKET_PENDING)),
		))

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

		By("Patching the BucketClaim status phase to be bound")
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

		By("Ensuring bucket is in available state and Access fields have been updated")
		Eventually(func() *ori.BucketStatus {
			resp, err := bucketClient.ListBuckets(ctx, &ori.ListBucketsRequest{
				Filter: &ori.BucketFilter{
					Id: createResp.Bucket.Metadata.Id,
				},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.Buckets).NotTo(BeEmpty())
			return resp.Buckets[0].Status
		}).Should(SatisfyAll(
			HaveField("State", Equal(ori.BucketState_BUCKET_AVAILABLE)),
			HaveField("Access", SatisfyAll(
				HaveField("Endpoint", Equal(fmt.Sprintf("%s.%s", bucketClaim.Name, bucketBaseURL))),
				HaveField("SecretData", SatisfyAll(
					HaveKeyWithValue("AccessKeyID", []byte("foo")),
					HaveKeyWithValue("SecretAccessKey", []byte("bar")),
				)),
			)),
		))

		By("Deleting the bucket")
		_, err = bucketClient.DeleteBucket(ctx, &ori.DeleteBucketRequest{
			BucketId: createResp.Bucket.Metadata.Id,
		})
		Expect(err).NotTo(HaveOccurred())

		By("Ensuring the bucket is deleted")
		Eventually(func() {
			resp, err := bucketClient.ListBuckets(ctx, &ori.ListBucketsRequest{
				Filter: &ori.BucketFilter{
					Id: createResp.Bucket.Metadata.Id,
				},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.Buckets).To(BeEmpty())
		})
		Eventually(Get(bucketClaim)).Should(Satisfy(apierrors.IsNotFound))
	})
})
