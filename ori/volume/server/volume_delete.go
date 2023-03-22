// Copyright 2022 OnMetal authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *Server) findNameFromId(ctx context.Context, volumeId string) (string, bool, error) {
	mappings, err := s.provisioner.GetAllMappings(ctx, RbdImage)
	if err != nil {
		return "", false, fmt.Errorf("unable to fetch all volume mapping: %w", err)
	}

	for volumeName, imageId := range mappings {
		if volumeId == imageId {
			return volumeName, true, nil
		}
	}

	return "", false, nil
}

func (s *Server) deleteCephImage(ctx context.Context, log logr.Logger, volumeId string) (retErr error) {

	volumeName, found, err := s.findNameFromId(ctx, volumeId)
	if err != nil {
		return fmt.Errorf("unable to find volumeName from id: %w", err)
	}
	if !found {
		log.V(1).Info("No mapping found: Already gone.", "volumeId", volumeId)
		return status.Errorf(codes.NotFound, "volumeId %s not found", volumeId)
	}

	log.V(2).Info("Try to acquire lock for volume", "volumeName", volumeName)
	if err := s.lock(volumeName); err != nil {
		return fmt.Errorf("unable to acquire lock: %w", err)
	}
	defer s.release(volumeName)

	if err := s.provisioner.DeleteCephImage(ctx, volumeId); err != nil {
		return fmt.Errorf("unable to delete ceph image: %w", err)
	}

	if err := s.provisioner.DeleteMapping(ctx, volumeName, RbdImage); err != nil {
		return fmt.Errorf("unable to delete mapping: %w", err)
	}

	return nil
}

func (s *Server) DeleteVolume(ctx context.Context, req *ori.DeleteVolumeRequest) (*ori.DeleteVolumeResponse, error) {
	log := s.loggerFrom(ctx)

	if err := s.deleteCephImage(ctx, log, req.VolumeId); err != nil {
		return nil, fmt.Errorf("unable to delete ceph volume: %w", err)
	}

	return &ori.DeleteVolumeResponse{}, nil
}
