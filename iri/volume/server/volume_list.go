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
	"errors"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/ironcore-dev/ceph-provider/iri/volume/apiutils"
	"github.com/ironcore-dev/ceph-provider/pkg/store"
	iri "github.com/ironcore-dev/ironcore/iri/apis/volume/v1alpha1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/apimachinery/pkg/labels"
)

func (s *Server) getIriVolume(ctx context.Context, log logr.Logger, imageId string) (*iri.Volume, error) {
	cephImage, err := s.imageStore.Get(ctx, imageId)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "image %s not found", imageId)
		}
		return nil, fmt.Errorf("failed to get image: %w", err)
	}

	if !apiutils.IsManagedBy(cephImage, apiutils.VolumeManager) {
		return nil, status.Errorf(codes.NotFound, "image %s not found", imageId)
	}

	return s.convertImageToIriVolume(cephImage)
}

func (s *Server) filterVolumes(volumes []*iri.Volume, filter *iri.VolumeFilter) []*iri.Volume {
	if filter == nil {
		return volumes
	}

	var (
		res []*iri.Volume
		sel = labels.SelectorFromSet(filter.LabelSelector)
	)
	for _, iriVolume := range volumes {
		if !sel.Matches(labels.Set(iriVolume.Metadata.Labels)) {
			continue
		}

		res = append(res, iriVolume)
	}
	return res
}

func (s *Server) listVolumes(ctx context.Context, log logr.Logger) ([]*iri.Volume, error) {
	cephImages, err := s.imageStore.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("error listing volumes: %w", err)
	}

	var res []*iri.Volume
	for _, cephImage := range cephImages {
		if !apiutils.IsManagedBy(cephImage, apiutils.VolumeManager) {
			continue
		}

		iriVolume, err := s.convertImageToIriVolume(cephImage)
		if err != nil {
			return nil, err
		}

		res = append(res, iriVolume)
	}
	return res, nil
}

func (s *Server) ListVolumes(ctx context.Context, req *iri.ListVolumesRequest) (*iri.ListVolumesResponse, error) {
	log := s.loggerFrom(ctx)
	log.V(2).Info("Listing volumes")

	if filter := req.Filter; filter != nil && filter.Id != "" {
		volume, err := s.getIriVolume(ctx, log, filter.Id)
		if err != nil {
			if status.Code(err) != codes.NotFound {
				return nil, err
			}
			return &iri.ListVolumesResponse{
				Volumes: []*iri.Volume{},
			}, nil
		}

		return &iri.ListVolumesResponse{
			Volumes: []*iri.Volume{volume},
		}, nil
	}

	volumes, err := s.listVolumes(ctx, log)
	if err != nil {
		return nil, err
	}

	volumes = s.filterVolumes(volumes, req.Filter)

	log.V(2).Info("Returning volumes list")
	return &iri.ListVolumesResponse{
		Volumes: volumes,
	}, nil
}
