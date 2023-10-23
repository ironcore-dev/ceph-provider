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
	bucketv1alpha1 "github.com/onmetal/cephlet/ori/bucket/api/v1alpha1"
	"github.com/onmetal/onmetal-api/broker/common"
	ori "github.com/onmetal/onmetal-api/ori/apis/bucket/v1alpha1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (s *Server) listManagedAndCreated(ctx context.Context, list client.ObjectList) error {
	return s.client.List(ctx, list,
		client.InNamespace(s.namespace),
		client.MatchingLabels{
			bucketv1alpha1.ManagerLabel: bucketv1alpha1.BucketManager,
		},
	)
}

func (s *Server) clientGetSecretFunc(ctx context.Context) func(string) (*corev1.Secret, error) {
	return func(name string) (*corev1.Secret, error) {
		secret := &corev1.Secret{}
		if err := s.client.Get(ctx, client.ObjectKey{Namespace: s.namespace, Name: name}, secret); err != nil {
			return nil, err
		}
		return secret, nil
	}
}

func (s *Server) getBucketAccessSecretIfRequired(
	bucketClaim *objectbucketv1alpha1.ObjectBucketClaim,
	getSecret func(string) (*corev1.Secret, error),
) (*corev1.Secret, error) {
	if bucketClaim.Status.Phase != objectbucketv1alpha1.ObjectBucketClaimStatusPhaseBound {
		return nil, nil
	}

	return getSecret(bucketClaim.Name)
}

func (s *Server) getAccessSecretForBucketClaim(
	bucketClaim *objectbucketv1alpha1.ObjectBucketClaim,
	getSecret func(string) (*corev1.Secret, error),
) (*corev1.Secret, error) {
	accessSecret, err := s.getBucketAccessSecretIfRequired(bucketClaim, getSecret)
	if err != nil {
		return nil, fmt.Errorf("error getting bucket access secret: %w", err)
	}
	return accessSecret, nil
}

func (s *Server) getAllManagedBuckets(ctx context.Context) ([]*ori.Bucket, error) {
	bucketClaimList := &objectbucketv1alpha1.ObjectBucketClaimList{}
	if err := s.listManagedAndCreated(ctx, bucketClaimList); err != nil {
		return nil, fmt.Errorf("error listing buckets: %w", err)
	}

	secretList := &corev1.SecretList{}
	if err := s.client.List(ctx, secretList,
		client.InNamespace(s.namespace),
	); err != nil {
		return nil, fmt.Errorf("error listing secrets: %w", err)
	}

	secretByNameGetter, err := common.NewObjectGetter[string, *corev1.Secret](
		corev1.Resource("secrets"),
		common.ByObjectName[*corev1.Secret](),
		common.ObjectSlice[string](secretList.Items),
	)
	if err != nil {
		return nil, fmt.Errorf("error constructing secret getter: %w", err)
	}

	var res []*ori.Bucket
	for i := range bucketClaimList.Items {
		bucketClaim := &bucketClaimList.Items[i]
		accessSecret, err := s.getAccessSecretForBucketClaim(bucketClaim, secretByNameGetter.Get)
		if err != nil {
			return nil, fmt.Errorf("error aggregating bucket %s: %w", bucketClaim.Name, err)
		}

		bucket, err := s.convertBucketClaimAndAccessSecretToBucket(bucketClaim, accessSecret)
		if err != nil {
			return nil, err
		}

		res = append(res, bucket)
	}

	return res, nil
}

func (s *Server) filterBuckets(buckets []*ori.Bucket, filter *ori.BucketFilter) []*ori.Bucket {
	if filter == nil {
		return buckets
	}

	var (
		res []*ori.Bucket
		sel = labels.SelectorFromSet(filter.LabelSelector)
	)
	for _, oriBucket := range buckets {
		if !sel.Matches(labels.Set(oriBucket.Metadata.Labels)) {
			continue
		}

		res = append(res, oriBucket)
	}
	return res
}

func (s *Server) getBucketForID(ctx context.Context, id string) (*ori.Bucket, error) {
	bucketClaim := &objectbucketv1alpha1.ObjectBucketClaim{}
	if err := s.getManagedAndCreated(ctx, id, bucketClaim); err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("error getting bucket %s: %w", id, err)
		}
		return nil, status.Errorf(codes.NotFound, "bucket %s not found", id)
	}

	accessSecret, err := s.getAccessSecretForBucketClaim(bucketClaim, s.clientGetSecretFunc(ctx))
	if err != nil {
		return nil, fmt.Errorf("failed to get access secret for bucket: %w", err)
	}

	return s.convertBucketClaimAndAccessSecretToBucket(bucketClaim, accessSecret)
}

func (s *Server) ListBuckets(ctx context.Context, req *ori.ListBucketsRequest) (*ori.ListBucketsResponse, error) {
	if filter := req.Filter; filter != nil && filter.Id != "" {
		bucket, err := s.getBucketForID(ctx, filter.Id)
		if err != nil {
			if status.Code(err) != codes.NotFound {
				return nil, err
			}
			return &ori.ListBucketsResponse{
				Buckets: []*ori.Bucket{},
			}, nil
		}

		return &ori.ListBucketsResponse{
			Buckets: []*ori.Bucket{bucket},
		}, nil
	}

	buckets, err := s.getAllManagedBuckets(ctx)
	if err != nil {
		return nil, err
	}

	buckets = s.filterBuckets(buckets, req.Filter)

	return &ori.ListBucketsResponse{
		Buckets: buckets,
	}, nil
}
