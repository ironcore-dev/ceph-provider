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

	objectbucketv1alpha1 "github.com/kube-object-storage/lib-bucket-provisioner/pkg/apis/objectbucket.io/v1alpha1"
	"github.com/onmetal/cephlet/ori/bucket/apiutils"
	oriv1alpha1 "github.com/onmetal/onmetal-api/ori/apis/bucket/v1alpha1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

var bucketClaimStateToORIState = map[objectbucketv1alpha1.ObjectBucketClaimStatusPhase]oriv1alpha1.BucketState{
	objectbucketv1alpha1.ObjectBucketClaimStatusPhasePending:  oriv1alpha1.BucketState_BUCKET_PENDING,
	objectbucketv1alpha1.ObjectBucketClaimStatusPhaseBound:    oriv1alpha1.BucketState_BUCKET_AVAILABLE,
	objectbucketv1alpha1.ObjectBucketClaimStatusPhaseFailed:   oriv1alpha1.BucketState_BUCKET_ERROR,
	objectbucketv1alpha1.ObjectBucketClaimStatusPhaseReleased: oriv1alpha1.BucketState_BUCKET_PENDING,
}

func (s *Server) convertBucketClaimAndAccessSecretToBucket(
	bucketClaim *objectbucketv1alpha1.ObjectBucketClaim,
	accessSecret *corev1.Secret,
) (*oriv1alpha1.Bucket, error) {
	metadata, err := apiutils.GetObjectMetadata(bucketClaim)
	if err != nil {
		return nil, fmt.Errorf("failed to get bucket claim object metadata: %w", err)
	}

	state, err := s.convertBucketClaimStateToBucketState(bucketClaim.Status.Phase)
	if err != nil {
		return nil, fmt.Errorf("failed to convert bucket claim state to bucket state: %w", err)
	}

	class, ok := apiutils.GetClassLabel(bucketClaim)
	if !ok {
		return nil, fmt.Errorf("failed to get bucket class")
	}

	access, err := s.convertAccessSecretToBucketAccess(bucketClaim, accessSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to convert access secret to bucket access: %w", err)
	}

	return &oriv1alpha1.Bucket{
		Metadata: metadata,
		Spec: &oriv1alpha1.BucketSpec{
			Class: class,
		},
		Status: &oriv1alpha1.BucketStatus{
			State:  state,
			Access: access,
		},
	}, nil
}

func (s *Server) convertBucketClaimStateToBucketState(state objectbucketv1alpha1.ObjectBucketClaimStatusPhase) (oriv1alpha1.BucketState, error) {
	if state == "" {
		return oriv1alpha1.BucketState_BUCKET_PENDING, nil
	}

	if state, ok := bucketClaimStateToORIState[state]; ok {
		return state, nil
	}
	return 0, fmt.Errorf("unknown bucket state %q", state)
}

func (s *Server) convertAccessSecretToBucketAccess(
	bucketClaim *objectbucketv1alpha1.ObjectBucketClaim,
	accessSecret *corev1.Secret,
) (*oriv1alpha1.BucketAccess, error) {
	if bucketClaim.Status.Phase != objectbucketv1alpha1.ObjectBucketClaimStatusPhaseBound {
		return nil, nil
	}

	if accessSecret == nil {
		return nil, fmt.Errorf("access secret not contained in aggregate bucket")
	}

	return &oriv1alpha1.BucketAccess{
		Endpoint:   fmt.Sprintf("%s.%s", bucketClaim.Spec.BucketName, s.bucketEndpoint),
		SecretData: accessSecret.Data,
	}, nil
}

func (s *Server) getBucketClaimForID(ctx context.Context, id string) (*objectbucketv1alpha1.ObjectBucketClaim, error) {
	bucketClaim := &objectbucketv1alpha1.ObjectBucketClaim{}
	if err := s.getManagedAndCreated(ctx, id, bucketClaim); err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("error getting bucket claim with ID %s: %w", id, err)
		}
		return nil, status.Errorf(codes.NotFound, "bucket for ID %s not found", id)
	}

	return bucketClaim, nil
}
