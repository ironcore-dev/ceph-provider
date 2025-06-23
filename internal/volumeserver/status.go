// SPDX-FileCopyrightText: 2023 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package volumeserver

import (
	"context"
	"fmt"

	"github.com/ironcore-dev/ceph-provider/internal/utils"
	iri "github.com/ironcore-dev/ironcore/iri/apis/volume/v1alpha1"
)

func (s *Server) Status(ctx context.Context, req *iri.StatusRequest) (*iri.StatusResponse, error) {
	log := s.loggerFrom(ctx)
	log.V(1).Info("Volume Status called")

	log.V(1).Info("Listing ironcore volume classes")
	volumeClassList := s.volumeClasses.List()

	log.V(1).Info("Getting ceph pool stats")
	poolStats, err := s.cephCommandClient.PoolStats()
	if err != nil {
		return nil, utils.ConvertInternalErrorToGRPC(fmt.Errorf("failed to get ceph pool stats: %w", err))
	}

	var volumeClassStatus []*iri.VolumeClassStatus
	for _, volumeClass := range volumeClassList {
		volumeClassStatus = append(volumeClassStatus, &iri.VolumeClassStatus{
			VolumeClass: volumeClass,
			Quantity:    poolStats.MaxAvail,
		})
	}

	log.V(1).Info("Returning status with volume classes")
	return &iri.StatusResponse{
		VolumeClassStatus: volumeClassStatus,
	}, nil
}
