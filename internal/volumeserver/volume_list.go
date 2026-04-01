// SPDX-FileCopyrightText: 2022 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package volumeserver

import (
	"context"
	"errors"
	"fmt"

	"github.com/ceph/go-ceph/rados"
	"github.com/go-logr/logr"
	"github.com/ironcore-dev/ceph-provider/api"
	"github.com/ironcore-dev/ceph-provider/internal/utils"
	iri "github.com/ironcore-dev/ironcore/iri/apis/volume/v1alpha1"
	providerapi "github.com/ironcore-dev/provider-utils/apiutils/api"
	"github.com/ironcore-dev/provider-utils/storeutils/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// imageListerWithLabels type parameter E is constrained to be a providerapi.Object.
type imageListerWithLabels[E providerapi.Object] interface {
	store.Store[E]
	ListByLabels(ctx context.Context, labelSelector map[string]string) ([]E, error)
}

func (s *Server) getIriVolume(ctx context.Context, log logr.Logger, imageId string) (*iri.Volume, error) {
	_, ok := s.imageStore.(imageListerWithLabels[*api.Image])
	if !ok {
		log.V(0).Info("Warning: imageStore does not implement ListByLabels, falling back to Get")
		// If ListByLabels is crucial, you might return an error here instead.
	}

	cephImage, err := s.imageStore.Get(ctx, imageId)
	if err != nil {
		if errors.Is(err, utils.ErrVolumeNotFound) {
			if errors.Is(err, rados.ErrNotFound) {
				log.V(1).Info("OMAP not found for volume", "imageID", imageId)
				return nil, status.Errorf(codes.NotFound, "volume %s not found (omap)", imageId)
			}
			log.V(1).Info("Volume not found in store", "imageID", imageId)
			return nil, status.Errorf(codes.NotFound, "volume %s not found", imageId)
		}
		log.Error(err, "Failed to get volume from store", "imageID", imageId)
		return nil, fmt.Errorf("failed to get image: %w", err)
	}

	if !api.IsObjectManagedBy(cephImage, api.VolumeManager) {
		log.V(1).Info("Volume is not managed by this manager", "volumeID", imageId, "managerLabel", cephImage.GetLabels()["ceph-volume-manager"])
		return nil, status.Errorf(codes.NotFound, "volume %s not found (not managed)", imageId)
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
	return s.listAndConvert(log, cephImages)
}

func (s *Server) listAndConvert(log logr.Logger, cephImages []*api.Image) ([]*iri.Volume, error) {
	res := make([]*iri.Volume, 0, len(cephImages))
	for _, cephImage := range cephImages {
		if !api.IsObjectManagedBy(cephImage, api.VolumeManager) {
			continue
		}
		iriVolume, err := s.convertImageToIriVolume(cephImage)
		if err != nil {
			log.Error(err, "Failed to convert ceph image to ori volume", "imageID", cephImage.GetID())
			continue
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
		log.V(1).Info("Filtering by Volume ID", "VolumeID", filter.Id)
		volume, err := s.getIriVolume(ctx, log, filter.Id)
		if err != nil {
			if status.Code(err) == codes.NotFound {
				log.V(1).Info("Volume not found by ID", "VolumeID", filter.Id)
				return &iri.ListVolumesResponse{Volumes: []*iri.Volume{}}, nil
			}
			log.Error(err, "Error getting volume by ID", "VolumeID", filter.Id)
			return nil, err
		}
		// Found by ID
		return &iri.ListVolumesResponse{Volumes: []*iri.Volume{volume}}, nil
	}

	// Fast path for LabelSelector filter (using the new index)
	if filter != nil && len(filter.LabelSelector) > 0 {
		log.V(1).Info("Filtering by Label Selector using index", "Selector", filter.LabelSelector)
		listerWithLabels, ok := s.imageStore.(imageListerWithLabels[*api.Image])
		if !ok {
			log.Error(fmt.Errorf("imageStore does not support ListByLabels"), "Cannot use label index optimization")
			goto SlowPath
		}

		cephImages, err := listerWithLabels.ListByLabels(ctx, filter.LabelSelector)
		if err != nil {
			log.Error(err, "Error listing volumes by labels from store", "Selector", filter.LabelSelector)
			return nil, fmt.Errorf("error listing volumes by labels: %w", err)
		}
		log.V(1).Info("Listed volumes using label index", "count", len(cephImages))

		// Convert results
		volumes, err := s.listAndConvert(log, cephImages)
		if err != nil {
			log.Error(err, "Error converting volumes found by label", "Selector", filter.LabelSelector)
			return nil, fmt.Errorf("error converting listed volumes: %w", err)
		}
		return &iri.ListVolumesResponse{Volumes: volumes}, nil
	}

	// Slow path: No specific filter or fallback
SlowPath:
	log.V(1).Info("Listing all volumes (no specific filter or fallback)")
	volumes, err := s.listVolumes(ctx, log) // Lists *all* managed volumes
	if err != nil {
		// listVolumes already logs the store error
		return nil, utils.ConvertInternalErrorToGRPC(err)
	}

	return &iri.ListVolumesResponse{Volumes: volumes}, nil
}
