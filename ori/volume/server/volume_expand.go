// Copyright 2023 OnMetal authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package server

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	ori "github.com/onmetal/onmetal-api/ori/apis/volume/v1alpha1"
)

func (s *Server) expandImage(ctx context.Context, log logr.Logger, imageId string, storageBytes uint64) error {
	log.V(2).Info("Fetching ceph image")
	cephImage, err := s.imageStore.Get(ctx, imageId)
	if err != nil {
		return fmt.Errorf("unable to get ceph image: %w", err)
	}

	if storageBytes <= cephImage.Spec.Size {
		return fmt.Errorf("requested size %q must be greater than current size %q", storageBytes, cephImage.Spec.Size)
	}

	log.V(2).Info("Updating ceph image with new size", "storageBytes", storageBytes)
	cephImage.Spec.Size = storageBytes
	if _, err := s.imageStore.Update(ctx, cephImage); err != nil {
		return fmt.Errorf("failed to update ceph image: %w", err)
	}

	return nil
}

func (s *Server) ExpandVolume(ctx context.Context, req *ori.ExpandVolumeRequest) (*ori.ExpandVolumeResponse, error) {
	volumeID := req.VolumeId
	log := s.loggerFrom(ctx, "VolumeID", volumeID)

	if err := s.expandImage(ctx, log, req.VolumeId, req.Resources.StorageBytes); err != nil {
		return nil, fmt.Errorf("failed to expand volume: %w", err)
	}

	return &ori.ExpandVolumeResponse{}, nil
}
