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

	storagev1alpha1 "github.com/onmetal/onmetal-api/api/storage/v1alpha1"
	oriv1alpha1 "github.com/onmetal/onmetal-api/ori/apis/bucket/v1alpha1"
)

func (s *Server) convertBucketClass(bucketClass *storagev1alpha1.BucketClass) (*oriv1alpha1.BucketClass, error) {
	tps := bucketClass.Capabilities.TPS()
	iops := bucketClass.Capabilities.IOPS()

	return &oriv1alpha1.BucketClass{
		Name: bucketClass.Name,
		Capabilities: &oriv1alpha1.BucketClassCapabilities{
			Tps:  tps.Value(),
			Iops: iops.Value(),
		},
	}, nil
}

func (s *Server) ListBucketClasses(ctx context.Context, req *oriv1alpha1.ListBucketClassesRequest) (*oriv1alpha1.ListBucketClassesResponse, error) {
	log := s.loggerFrom(ctx)
	log.V(1).Info("Listing bucket classes")

	list := &storagev1alpha1.BucketClassList{}
	if err := s.client.List(ctx, list, s.bucketClassSelector); err != nil {
		return nil, fmt.Errorf("error listing bucket classes: %w", err)
	}

	var classes []*oriv1alpha1.BucketClass
	for _, class := range list.Items {
		bucketClass, err := s.convertBucketClass(&class)
		if err != nil {
			return nil, fmt.Errorf("error converting bucket class %s: %w", class.Name, err)
		}
		classes = append(classes, bucketClass)
	}

	log.V(1).Info("Returning bucket classes")
	return &oriv1alpha1.ListBucketClassesResponse{
		BucketClasses: classes,
	}, nil
}
