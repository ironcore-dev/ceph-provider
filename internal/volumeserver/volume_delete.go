// SPDX-FileCopyrightText: 2022 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package volumeserver

import (
	"context"
	"fmt"

	"github.com/ironcore-dev/ceph-provider/internal/utils"
	iri "github.com/ironcore-dev/ironcore/iri/apis/volume/v1alpha1"
	"github.com/pkg/errors"
)

func (s *Server) DeleteVolume(ctx context.Context, req *iri.DeleteVolumeRequest) (*iri.DeleteVolumeResponse, error) {
	log := s.loggerFrom(ctx, "VolumeID", req.GetVolumeId())

	log.V(1).Info("Deleting volume")
	if err := s.imageStore.Delete(ctx, req.VolumeId); err != nil {
		if !errors.Is(err, utils.ErrVolumeNotFound) {
			return nil, fmt.Errorf("error deleting volume: %w", err)
		}
		return nil, utils.ConvertInternalErrorToGRPC(fmt.Errorf("failed to get volume %s: %w", req.VolumeId, utils.ErrVolumeNotFound))
	}

	log.V(1).Info("Volume deleted")
	return &iri.DeleteVolumeResponse{}, nil
}
