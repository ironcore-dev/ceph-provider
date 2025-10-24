// SPDX-FileCopyrightText: 2022 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package volumeserver

import (
	"context"
	"errors"
	"fmt"

	"github.com/ironcore-dev/ceph-provider/internal/utils"
	iri "github.com/ironcore-dev/ironcore/iri/apis/volume/v1alpha1"
)

func (s *Server) DeleteVolumeSnapshot(ctx context.Context, req *iri.DeleteVolumeSnapshotRequest) (*iri.DeleteVolumeSnapshotResponse, error) {
	log := s.loggerFrom(ctx, "SnapshotID", req.GetVolumeSnapshotId())

	log.V(1).Info("Deleting volume snapshot")
	if err := s.snapshotStore.Delete(ctx, req.VolumeSnapshotId); err != nil {
		if !errors.Is(err, utils.ErrSnapshotNotFound) {
			return nil, fmt.Errorf("error deleting volume snapshot: %w", err)
		}
		return nil, utils.ConvertInternalErrorToGRPC(fmt.Errorf("failed to delete volume snapshot %s: %w", req.VolumeSnapshotId, utils.ErrSnapshotNotFound))
	}

	log.V(1).Info("Volume snapshot deleted")
	return &iri.DeleteVolumeSnapshotResponse{}, nil
}
