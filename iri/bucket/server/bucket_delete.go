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

	iriv1alpha1 "github.com/ironcore-dev/ironcore/iri/apis/bucket/v1alpha1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

func (s *Server) DeleteBucket(ctx context.Context, req *iriv1alpha1.DeleteBucketRequest) (*iriv1alpha1.DeleteBucketResponse, error) {
	log := s.loggerFrom(ctx, "BucketID", req.BucketId)

	bucketClaim, err := s.getBucketClaimForID(ctx, req.BucketId)
	if err != nil {
		return nil, err
	}

	log.V(1).Info("Deleting bucket")
	if err := s.client.Delete(ctx, bucketClaim); err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("error deleting bucket claim: %w", err)
		}
		return nil, status.Errorf(codes.NotFound, "bucket claim %s not found", req.BucketId)
	}

	log.V(1).Info("Bucket deleted")
	return &iriv1alpha1.DeleteBucketResponse{}, nil
}
