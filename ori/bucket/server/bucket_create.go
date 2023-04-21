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
	objectbucketv1alpha1 "github.com/kube-object-storage/lib-bucket-provisioner/pkg/apis/objectbucket.io/v1alpha1"
	bucketv1alpha1 "github.com/onmetal/cephlet/ori/bucket/api/v1alpha1"
	"github.com/onmetal/cephlet/ori/bucket/apiutils"
	ori "github.com/onmetal/onmetal-api/ori/apis/bucket/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type AggregateBucket struct {
	BucketClaim  *objectbucketv1alpha1.ObjectBucketClaim
	AccessSecret *corev1.Secret
}

type BucketConfig struct {
	BucketClaim *objectbucketv1alpha1.ObjectBucketClaim
}

func (s *Server) getBucketConfig(_ context.Context, bucket *ori.Bucket) (*BucketConfig, error) {
	bucketClaim := &objectbucketv1alpha1.ObjectBucketClaim{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ObjectBucketClaim",
			APIVersion: "objectbucket.io/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      s.idGen.Generate(),
			Namespace: s.namespace,
		},
		Spec: objectbucketv1alpha1.ObjectBucketClaimSpec{
			StorageClassName:   s.bucketPoolStorageClassName,
			GenerateBucketName: s.idGen.Generate(),
		},
	}

	if err := apiutils.SetObjectMetadata(bucketClaim, bucket.Metadata); err != nil {
		return nil, err
	}
	apiutils.SetClassLabel(bucketClaim, bucket.Spec.Class)
	apiutils.SetBucketManagerLabel(bucketClaim, bucketv1alpha1.BucketManager)

	return &BucketConfig{
		BucketClaim: bucketClaim,
	}, nil
}

func (s *Server) createBucket(ctx context.Context, log logr.Logger, cfg *BucketConfig) (res *AggregateBucket, retErr error) {
	log.V(1).Info("Creating bucket claim")
	if err := s.client.Create(ctx, cfg.BucketClaim); err != nil {
		return nil, fmt.Errorf("error creating bucket: %w", err)
	}

	accessSecret, err := s.getBucketAccessSecretIfRequired(cfg.BucketClaim, s.clientGetSecretFunc(ctx))
	if err != nil {
		return nil, err
	}

	return &AggregateBucket{
		BucketClaim:  cfg.BucketClaim,
		AccessSecret: accessSecret,
	}, nil
}

func (s *Server) CreateBucket(ctx context.Context, req *ori.CreateBucketRequest) (res *ori.CreateBucketResponse, retErr error) {
	log := s.loggerFrom(ctx)

	log.V(1).Info("Getting bucket configuration")
	cfg, err := s.getBucketConfig(ctx, req.Bucket)
	if err != nil {
		return nil, fmt.Errorf("error getting bucket config: %w", err)
	}

	aggregateBucket, err := s.createBucket(ctx, log, cfg)
	if err != nil {
		return nil, fmt.Errorf("error creating bucket: %w", err)
	}

	v, err := s.convertAggregateBucket(aggregateBucket)
	if err != nil {
		return nil, err
	}

	return &ori.CreateBucketResponse{
		Bucket: v,
	}, nil
}
