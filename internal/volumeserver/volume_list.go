// SPDX-FileCopyrightText: 2022 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package volumeserver

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/ironcore-dev/ceph-provider/api"
	"github.com/ironcore-dev/ceph-provider/internal/utils"
	iri "github.com/ironcore-dev/ironcore/iri/apis/volume/v1alpha1"
	"k8s.io/apimachinery/pkg/labels"
)

func (s *Server) getIriVolume(ctx context.Context, log logr.Logger, imageId string) (*iri.Volume, error) {
	cephImage, err := s.imageStore.Get(ctx, imageId)
	if err != nil {
		if errors.Is(err, utils.ErrVolumeNotFound) {
			return nil, fmt.Errorf("failed to get image %s: %w", imageId, utils.ErrVolumeNotFound)
		}
		return nil, fmt.Errorf("failed to get image: %w", err)
	}

	if !api.IsObjectManagedBy(cephImage, api.VolumeManager) {
		return nil, fmt.Errorf("failed to get image %s: %w", imageId, utils.ErrVolumeIsntManaged)
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
		if !api.IsObjectManagedBy(cephImage, api.VolumeManager) {
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
			if !errors.Is(err, utils.ErrVolumeNotFound) && !errors.Is(err, utils.ErrVolumeIsntManaged) {
				return nil, utils.ConvertInternalErrorToGRPC(err)
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
		return nil, utils.ConvertInternalErrorToGRPC(err)
	}

	volumes = s.filterVolumes(volumes, req.Filter)

	log.V(2).Info("Returning volumes list")
	return &iri.ListVolumesResponse{
		Volumes: volumes,
	}, nil
}
