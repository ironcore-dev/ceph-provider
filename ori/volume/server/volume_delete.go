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

	"github.com/onmetal/cephlet/pkg/store"
	ori "github.com/onmetal/onmetal-api/ori/apis/volume/v1alpha1"
	"github.com/pkg/errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *Server) DeleteVolume(ctx context.Context, req *ori.DeleteVolumeRequest) (*ori.DeleteVolumeResponse, error) {
	log := s.loggerFrom(ctx)

	log.V(1).Info("Deleting volume")
	if err := s.imageStore.Delete(ctx, req.VolumeId); err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			return nil, fmt.Errorf("error deleting volume: %w", err)
		}
		return nil, status.Errorf(codes.NotFound, "volume %s not found", req.VolumeId)
	}
	log.V(1).Info("Volume deleted")

	return &ori.DeleteVolumeResponse{}, nil
}
