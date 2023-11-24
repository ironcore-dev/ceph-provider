// Copyright 2022 OnMetal authors
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

package server

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/ironcore-dev/ceph-provider/iri/bucket/apiutils"
	iriv1alpha1 "github.com/ironcore-dev/ironcore/iri/apis/bucket/v1alpha1"
	objectbucketv1alpha1 "github.com/kube-object-storage/lib-bucket-provisioner/pkg/apis/objectbucket.io/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (s *Server) createBucketClaimAndAccessSecretFromBucket(
	ctx context.Context,
	log logr.Logger,
	bucket *iriv1alpha1.Bucket,
) (*objectbucketv1alpha1.ObjectBucketClaim, *corev1.Secret, error) {
	generateBucketName := s.idGen.Generate()
	bucketClaim := &objectbucketv1alpha1.ObjectBucketClaim{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ObjectBucketClaim",
			APIVersion: "objectbucket.io/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      generateBucketName,
			Namespace: s.namespace,
		},
		Spec: objectbucketv1alpha1.ObjectBucketClaimSpec{
			StorageClassName:   s.bucketPoolStorageClassName,
			GenerateBucketName: generateBucketName,
		},
	}

	if err := apiutils.SetObjectMetadata(bucketClaim, bucket.Metadata); err != nil {
		return nil, nil, err
	}
	apiutils.SetClassLabel(bucketClaim, bucket.Spec.Class)
	apiutils.SetBucketManagerLabel(bucketClaim, apiutils.BucketManager)

	log.V(2).Info("Creating bucket claim")
	if err := s.client.Create(ctx, bucketClaim); err != nil {
		return nil, nil, fmt.Errorf("failed to create bucket claim: %w", err)
	}

	log.V(2).Info("Getting bucket access secret")
	accessSecret, err := s.getBucketAccessSecretIfRequired(bucketClaim, s.clientGetSecretFunc(ctx))
	if err != nil {
		return nil, nil, err
	}

	return bucketClaim, accessSecret, nil
}

func (s *Server) CreateBucket(
	ctx context.Context,
	req *iriv1alpha1.CreateBucketRequest,
) (res *iriv1alpha1.CreateBucketResponse, retErr error) {
	log := s.loggerFrom(ctx)
	log.V(1).Info("Creating bucket")

	log.V(1).Info("Creating bucket claim and bucket access secret")
	bucketClaim, accessSecret, err := s.createBucketClaimAndAccessSecretFromBucket(ctx, log, req.Bucket)
	if err != nil {
		return nil, fmt.Errorf("error getting bucket config: %w", err)
	}

	log = log.WithValues("BucketClaimName", bucketClaim.Name)

	log.V(1).Info("Getting IRI bucket object")
	iriBucket, err := s.convertBucketClaimAndAccessSecretToBucket(bucketClaim, accessSecret)
	if err != nil {
		return nil, err
	}

	log.V(1).Info("Bucket created", "Bucket", iriBucket.Metadata.Id, "State", iriBucket.Status.State)
	return &iriv1alpha1.CreateBucketResponse{
		Bucket: iriBucket,
	}, nil
}
