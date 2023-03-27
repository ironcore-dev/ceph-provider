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
)

func (s *Server) deleteCephImage(ctx context.Context, log logr.Logger, imageId string) (retErr error) {
	log.V(2).Info("Try to acquire lock for volume", "imageId", imageId)
	if err := s.lock(imageId); err != nil {
		return fmt.Errorf("unable to acquire lock: %w", err)
	}
	defer s.release(imageId)

	if err := s.provisioner.DeleteCephImage(ctx, imageId); err != nil {
		return fmt.Errorf("unable to delete ceph image: %w", err)
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
