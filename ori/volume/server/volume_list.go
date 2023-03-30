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
	ori "github.com/onmetal/onmetal-api/ori/apis/volume/v1alpha1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/apimachinery/pkg/labels"
)

func (s *Server) getOriVolume(ctx context.Context, log logr.Logger, imageId string) (*ori.Volume, error) {
	cephImage, err := s.imageStore.Get(ctx, imageId)
	if err != nil {
		return nil, fmt.Errorf("failed to get image: %w", err)
	}

	return s.convertImageToOriVolume(ctx, log, cephImage)
}

func (s *Server) filterVolumes(volumes []*ori.Volume, filter *ori.VolumeFilter) []*ori.Volume {
	if filter == nil {
		return volumes
	}

	var (
		res []*ori.Volume
		sel = labels.SelectorFromSet(filter.LabelSelector)
	)
	for _, oriVolume := range volumes {
		if !sel.Matches(labels.Set(oriVolume.Metadata.Labels)) {
			continue
		}

		res = append(res, oriVolume)
	}
	return res
}

func (s *Server) listVolumes(ctx context.Context, log logr.Logger) ([]*ori.Volume, error) {
	cephImages, err := s.imageStore.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("error listing volumes: %w", err)
	}

	var res []*ori.Volume
	for _, cephImage := range cephImages {
		oriVolume, err := s.convertImageToOriVolume(ctx, log, cephImage)
		if err != nil {
			return nil, err
		}

		res = append(res, oriVolume)
	}
	return res, nil
}

func (s *Server) ListVolumes(ctx context.Context, req *ori.ListVolumesRequest) (*ori.ListVolumesResponse, error) {
	log := s.loggerFrom(ctx)

	if filter := req.Filter; filter != nil && filter.Id != "" {
		volume, err := s.getOriVolume(ctx, log, filter.Id)
		if err != nil {
			if status.Code(err) != codes.NotFound {
				return nil, err
			}
			return &ori.ListVolumesResponse{
				Volumes: []*ori.Volume{},
			}, nil
		}

		return &ori.ListVolumesResponse{
			Volumes: []*ori.Volume{volume},
		}, nil
	}

	volumes, err := s.listVolumes(ctx, log)
	if err != nil {
		return nil, err
	}

	volumes = s.filterVolumes(volumes, req.Filter)

	return &ori.ListVolumesResponse{
		Volumes: volumes,
	}, nil
}
