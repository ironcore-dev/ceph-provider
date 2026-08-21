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
	"github.com/ironcore-dev/provider-utils/storeutils/store"
)

func (s *Server) getIriVolume(ctx context.Context, imageId string) (*iri.Volume, error) {
	cephImage, err := s.imageStore.Get(ctx, imageId)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, fmt.Errorf("failed to get image %s: %w", imageId, store.ErrNotFound)
		}
		return nil, fmt.Errorf("failed to get image: %w", err)
	}

	if !api.IsObjectManagedBy(cephImage, api.VolumeManager) {
		return nil, fmt.Errorf("failed to get image %s: %w", imageId, utils.ErrVolumeIsntManaged)
	}

	return s.convertImageToIriVolume(cephImage)
}

func (s *Server) listVolumes(ctx context.Context, log logr.Logger, filter *iri.VolumeFilter) ([]*iri.Volume, error) {
	matchingLabels := store.MatchingLabels{
		api.ManagerLabel: api.VolumeManager,
	}

	if filter != nil && len(filter.LabelSelector) > 0 {
		for k := range filter.LabelSelector {
			matchingLabels[k] = filter.LabelSelector[k]
		}
	}

	cephImages, err := s.imageStore.List(ctx, matchingLabels)
	if err != nil {
		log.Error(err, "Error listing volumes from image store")
		return nil, fmt.Errorf("error listing volumes: %w", err)
	}

	res := make([]*iri.Volume, 0, len(cephImages))
	for _, cephImage := range cephImages {
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
	filter := req.Filter
	log.V(2).Info("Listing volumes")

	// Fast path for ID filter
	if filter != nil && filter.Id != "" {
		volume, err := s.getIriVolume(ctx, filter.Id)
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

	log.V(1).Info("Listing all volumes (no specific filter or fallback)")
	volumes, err := s.listVolumes(ctx, log, filter) // Lists *all* managed volumes
	if err != nil {
		// listVolumes already logs the store error
		return nil, utils.ConvertInternalErrorToGRPC(err)
	}

	return &iri.ListVolumesResponse{Volumes: volumes}, nil
}
