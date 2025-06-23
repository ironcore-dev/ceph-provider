// SPDX-FileCopyrightText: 2022 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package bucketserver

import (
	"context"
	"fmt"

	"github.com/ironcore-dev/ceph-provider/internal/utils"
	iriv1alpha1 "github.com/ironcore-dev/ironcore/iri/apis/bucket/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

func (s *Server) DeleteBucket(ctx context.Context, req *iriv1alpha1.DeleteBucketRequest) (*iriv1alpha1.DeleteBucketResponse, error) {
	log := s.loggerFrom(ctx, "BucketID", req.BucketId)

	bucketClaim, err := s.getBucketClaimForID(ctx, req.BucketId)
	if err != nil {
		return nil, utils.ConvertInternalErrorToGRPC(err)
	}

	log.V(1).Info("Deleting bucket")
	if err := s.client.Delete(ctx, bucketClaim); err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("error deleting bucket claim: %w", err)
		}
		return nil, utils.ConvertInternalErrorToGRPC(fmt.Errorf("failed to delete bucket claim %s: %w", req.BucketId, utils.ErrBucketNotFound))
	}

	log.V(1).Info("Bucket deleted")
	return &iriv1alpha1.DeleteBucketResponse{}, nil
}
