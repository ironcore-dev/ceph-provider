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

	storagev1alpha1 "github.com/onmetal/onmetal-api/api/storage/v1alpha1"
	ori "github.com/onmetal/onmetal-api/ori/apis/volume/v1alpha1"
)

func (s *Server) convertOnmetalVolumeClass(volumeClass *storagev1alpha1.VolumeClass) (*ori.VolumeClass, error) {
	tps := volumeClass.Capabilities.TPS()
	iops := volumeClass.Capabilities.IOPS()

	return &ori.VolumeClass{
		Name: volumeClass.Name,
		Capabilities: &ori.VolumeClassCapabilities{
			Tps:  tps.Value(),
			Iops: iops.Value(),
		},
	}, nil
}

func (s *Server) ListVolumeClasses(ctx context.Context, req *ori.ListVolumeClassesRequest) (*ori.ListVolumeClassesResponse, error) {
	log := s.loggerFrom(ctx)

	log.V(1).Info("Listing onmetal volume classes")
	onmetalVolumeClassList := &storagev1alpha1.VolumeClassList{}
	if err := s.client.List(ctx, onmetalVolumeClassList, s.volumeClassSelector); err != nil {
		return nil, fmt.Errorf("error listing volume classes: %w", err)
	}

	var volumeClasses []*ori.VolumeClass
	for _, onmetalVolumeClass := range onmetalVolumeClassList.Items {
		volumeClass, err := s.convertOnmetalVolumeClass(&onmetalVolumeClass)
		if err != nil {
			return nil, fmt.Errorf("error converting onmetal volume class %s: %w", onmetalVolumeClass.Name, err)
		}

		volumeClasses = append(volumeClasses, volumeClass)
	}

	log.V(1).Info("Returning volume classes")
	return &ori.ListVolumeClassesResponse{
		VolumeClasses: volumeClasses,
	}, nil
}
