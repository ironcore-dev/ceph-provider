// SPDX-FileCopyrightText: 2022 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"

	iriv1alpha1 "github.com/ironcore-dev/ironcore/iri/apis/bucket/v1alpha1"
)

func (s *Server) ListBucketClasses(ctx context.Context, req *iriv1alpha1.ListBucketClassesRequest) (*iriv1alpha1.ListBucketClassesResponse, error) {
	log := s.loggerFrom(ctx)
	log.V(1).Info("Listing bucket classes")

	classes := s.bucketClassess.List()

	log.V(1).Info("Returning bucket classes")
	return &iriv1alpha1.ListBucketClassesResponse{
		BucketClasses: classes,
	}, nil
}
