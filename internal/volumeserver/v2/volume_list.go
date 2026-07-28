// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package volumeserver

import (
	"context"
	"errors"
	"fmt"

	api "github.com/ironcore-dev/ceph-provider/api/v2"
	"github.com/ironcore-dev/ceph-provider/internal/utils"
	iri "github.com/ironcore-dev/ironcore/iri/apis/volume/v1alpha1"
	"k8s.io/apimachinery/pkg/labels"
)

func (s *Server) getIriVolume(ctx context.Context, volumeID string) (*iri.Volume, error) {
	volume, err := s.volumeStore.Get(ctx, volumeID)
	if err != nil {
		if errors.Is(err, utils.ErrVolumeNotFound) {
			return nil, fmt.Errorf("failed to get volume %s: %w", volumeID, utils.ErrVolumeNotFound)
		}
		return nil, fmt.Errorf("failed to get volume: %w", err)
	}

	if !api.IsObjectManagedBy(volume, api.VolumeManager) {
		return nil, fmt.Errorf("failed to get volume %s: %w", volumeID, utils.ErrVolumeIsntManaged)
	}

	return s.convertVolumeToIRI(volume)
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

func (s *Server) listVolumes(ctx context.Context) ([]*iri.Volume, error) {
	volumes, err := s.volumeStore.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("error listing volumes: %w", err)
	}

	var res []*iri.Volume
	for _, volume := range volumes {
		if !api.IsObjectManagedBy(volume, api.VolumeManager) {
			continue
		}

		iriVolume, err := s.convertVolumeToIRI(volume)
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

	volumes, err := s.listVolumes(ctx)
	if err != nil {
		return nil, utils.ConvertInternalErrorToGRPC(err)
	}

	volumes = s.filterVolumes(volumes, req.Filter)

	log.V(2).Info("Returning volumes list")
	return &iri.ListVolumesResponse{
		Volumes: volumes,
	}, nil
}
