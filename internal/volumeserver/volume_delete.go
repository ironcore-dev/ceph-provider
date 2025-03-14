// SPDX-FileCopyrightText: 2022 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package volumeserver

import (
	"context"
	"fmt"

	"github.com/ironcore-dev/ceph-provider/internal/store"
	iri "github.com/ironcore-dev/ironcore/iri/apis/volume/v1alpha1"
	"github.com/pkg/errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *Server) DeleteVolume(ctx context.Context, req *iri.DeleteVolumeRequest) (*iri.DeleteVolumeResponse, error) {
	log := s.loggerFrom(ctx, "VolumeID", req.GetVolumeId())

	log.V(1).Info("Deleting volume")
	if err := s.imageStore.Delete(ctx, req.VolumeId); err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			return nil, fmt.Errorf("error deleting volume: %w", err)
		}
		return nil, status.Errorf(codes.NotFound, "volume %s not found", req.VolumeId)
	}

	log.V(1).Info("Volume deleted")
	return &iri.DeleteVolumeResponse{}, nil
}
