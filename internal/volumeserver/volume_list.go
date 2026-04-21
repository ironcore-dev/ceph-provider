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
	providerapi "github.com/ironcore-dev/provider-utils/apiutils/api"
	"github.com/ironcore-dev/provider-utils/storeutils/store"
)

// imageListerWithLabels type parameter E is constrained to be a providerapi.Object.
type imageListerWithLabels[E providerapi.Object] interface {
	store.Store[E]
	ListByLabels(ctx context.Context, labelSelector map[string]string) ([]E, error)
}

func (s *Server) getIriVolume(ctx context.Context, imageId string) (*iri.Volume, error) {
	cephImage, err := s.imageStore.Get(ctx, imageId)
	if err != nil {
		if errors.Is(err, utils.ErrVolumeNotFound) {
			return nil, fmt.Errorf("failed to get image %s: %w", imageId, utils.ErrVolumeNotFound)
		}
	}

	if !api.IsObjectManagedBy(cephImage, api.VolumeManager) {
		return nil, fmt.Errorf("failed to get image %s: %w", imageId, utils.ErrVolumeIsntManaged)
	}

	return s.convertImageToIriVolume(cephImage)
}

func (s *Server) listVolumes(ctx context.Context, log logr.Logger) ([]*iri.Volume, error) {
	cephImages, err := s.imageStore.List(ctx)
	if err != nil {
		log.Error(err, "Error listing volumes from image store")
		return nil, fmt.Errorf("error listing volumes: %w", err)
	}
	log.V(1).Info("Listed all volumes from store", "count", len(cephImages))
	return s.listAndConvert(cephImages)
}

func (s *Server) listAndConvert(cephImages []*api.Image) ([]*iri.Volume, error) {
	res := make([]*iri.Volume, 0, len(cephImages))
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
	filter := req.Filter
	log.V(2).Info("Listing volumes")

	// Fast path for ID filter
	if filter := req.Filter; filter != nil && filter.Id != "" {
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

	// Fast path for LabelSelector filter (using the new index)
	if filter != nil && len(filter.LabelSelector) > 0 {
		log.V(1).Info("Filtering by Label Selector using index", "Selector", filter.LabelSelector)
		if listerWithLabels, ok := s.imageStore.(imageListerWithLabels[*api.Image]); ok {
			cephImages, err := listerWithLabels.ListByLabels(ctx, filter.LabelSelector)
			if err != nil {
				log.Error(err, "Error listing volumes by labels from store", "Selector", filter.LabelSelector)
				return nil, fmt.Errorf("error listing volumes by labels: %w", err)
			}
			log.V(1).Info("Listed volumes using label index", "count", len(cephImages))

			// Convert results
			volumes, err := s.listAndConvert(cephImages)
			if err != nil {
				log.Error(err, "Error converting volumes found by label", "Selector", filter.LabelSelector)
				return nil, fmt.Errorf("error converting listed volumes: %w", err)
			}
			return &iri.ListVolumesResponse{Volumes: volumes}, nil
		}
		log.Error(fmt.Errorf("imageStore does not support ListByLabels"), "Cannot use label index optimization")
	}

	// Slow path: No specific filter or fallback
	log.V(1).Info("Listing all volumes (no specific filter or fallback)")
	volumes, err := s.listVolumes(ctx, log) // Lists *all* managed volumes
	if err != nil {
		// listVolumes already logs the store error
		return nil, utils.ConvertInternalErrorToGRPC(err)
	}

	return &iri.ListVolumesResponse{Volumes: volumes}, nil
}
