// SPDX-FileCopyrightText: 2022 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package volumeserver

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/ironcore-dev/ceph-provider/api"
	"github.com/ironcore-dev/ceph-provider/internal/utils"
	iriv1alpha1 "github.com/ironcore-dev/ironcore/iri/apis/volume/v1alpha1"
	apiutils "github.com/ironcore-dev/provider-utils/apiutils/api"
	"github.com/pkg/errors"
)

func (s *Server) createVolumeSnapshot(ctx context.Context, log logr.Logger, volumeSnapshot *iriv1alpha1.VolumeSnapshot) (*api.Snapshot, error) {
	log.V(2).Info("Check if volume snapshot's source volume exists")
	volumeID := volumeSnapshot.Spec.VolumeId
	volume, err := s.imageStore.Get(ctx, volumeID)
	if err != nil {
		if errors.Is(err, utils.ErrVolumeNotFound) {
			return nil, fmt.Errorf("failed to get source volume %s: %w", volumeID, utils.ErrVolumeNotFound)
		}
		return nil, fmt.Errorf("failed to get source volume %s: %w", volumeID, err)
	}
	if volume.Status.State != api.ImageStateAvailable {
		return nil, fmt.Errorf("source volume %s is not available, current state is: %s", volumeID, volume.Status.State)
	}

	snapshot := &api.Snapshot{
		Metadata: apiutils.Metadata{
			ID: s.idGen.Generate(),
		},
		Source: api.SnapshotSource{
			VolumeImageID: volumeID,
		},
	}

	log.V(2).Info("Setting volume snapshot metadata")
	if err := api.SetObjectMetadataFromMetadata(snapshot, volumeSnapshot.Metadata); err != nil {
		return nil, fmt.Errorf("failed to set volume snapshot metadata: %w", err)
	}
	api.SetManagerLabel(snapshot, api.VolumeManager)

	log.V(2).Info("Creating volume snapshot in store")
	snapshot, err = s.snapshotStore.Create(ctx, snapshot)
	if err != nil {
		return nil, fmt.Errorf("failed to create volume snapshot: %w", err)
	}

	return snapshot, nil
}

func (s *Server) CreateVolumeSnapshot(ctx context.Context, req *iriv1alpha1.CreateVolumeSnapshotRequest) (*iriv1alpha1.CreateVolumeSnapshotResponse, error) {
	log := s.loggerFrom(ctx)
	log.V(1).Info("Creating volume snapshot")

	log.V(1).Info("Creating Ceph volume snapshot from IRI volume snapshot")
	volumeSnapshot, err := s.createVolumeSnapshot(ctx, log, req.VolumeSnapshot)
	if err != nil {
		return nil, utils.ConvertInternalErrorToGRPC(fmt.Errorf("unable to create ceph snapshot: %w", err))
	}

	log = log.WithValues("VolumeSnapshotID", volumeSnapshot.ID, "State", volumeSnapshot.Status.State)

	log.V(1).Info("Converting volume snapshot to IRI volume snapshot")
	iriVolumeSnapshot, err := s.convertSnapshotToIriVolumeSnapshot(volumeSnapshot)
	if err != nil {
		return nil, utils.ConvertInternalErrorToGRPC(fmt.Errorf("unable to convert volume snapshot: %w", err))
	}

	log.V(1).Info("Volume snapshot created successfully")
	return &iriv1alpha1.CreateVolumeSnapshotResponse{
		VolumeSnapshot: iriVolumeSnapshot,
	}, nil
}
