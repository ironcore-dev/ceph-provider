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
)

func (s *Server) createSnapshot(ctx context.Context, log logr.Logger, volumeID string) (*api.Snapshot, error) {
	if volumeID == "" {
		return nil, fmt.Errorf("got an empty volumeID")
	}

	snapshot := &api.Snapshot{
		Metadata: apiutils.Metadata{
			ID: s.idGen.Generate(),
		},
		Spec: api.SnapshotSpec{
			Source: api.SnapshotSource{
				IronCoreVolumeImage: volumeID,
			},
		},
	}

	api.SetManagerLabel(snapshot, api.VolumeManager)

	log.V(2).Info("Creating snapshot in store")
	snapshot, err := s.snapshotStore.Create(ctx, snapshot)
	if err != nil {
		return nil, fmt.Errorf("failed to create snapshot: %w", err)
	}

	log.V(2).Info("Snapshot created", "SnapshotID", snapshot.ID)
	return snapshot, nil
}

func (s *Server) CreateVolumeSnapshot(ctx context.Context, req *iriv1alpha1.CreateVolumeSnapshotRequest) (*iriv1alpha1.CreateVolumeSnapshotResponse, error) {
	log := s.loggerFrom(ctx)
	log.V(1).Info("Creating snapshot")

	log.V(1).Info("Creating Ceph snapshot from volumeSnapshot")
	snaphot, err := s.createSnapshot(ctx, log, req.VolumeSnapshot.Spec.VolumeId)
	if err != nil {
		return nil, utils.ConvertInternalErrorToGRPC(fmt.Errorf("unable to create ceph snapshot: %w", err))
	}

	log = log.WithValues("snaphotID", snaphot.ID)

	log.V(1).Info("Converting snapshot to IRI snapshot")
	iriVolumeSnapshot, err := s.convertSnapshotToIriVolumeSnapshot(snaphot)
	if err != nil {
		return nil, utils.ConvertInternalErrorToGRPC(fmt.Errorf("unable to create ceph snapshot: %w", err))
	}

	log.V(1).Info("VolumeSnapshot created", "VolumeSnapshot", snaphot.Metadata.ID, "State", snaphot.Status.State)
	return &iriv1alpha1.CreateVolumeSnapshotResponse{
		VolumeSnapshot: iriVolumeSnapshot,
	}, nil
}
