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
	"github.com/onmetal/cephlet/pkg/populate"
	ori "github.com/onmetal/onmetal-api/ori/apis/volume/v1alpha1"
)

func (s *Server) validateVolumeClass(volume *ori.Volume) error {
	_, found := s.AvailableVolumeClasses[volume.Spec.Class]
	if !found {
		return fmt.Errorf("volume class '%s' not supported", volume.Spec.Class)
	}

	return nil
}

func (s *Server) validateSize(volume *ori.Volume, image *PopulationImage) error {
	if image != nil && volume.Spec.Resources.StorageBytes <= image.Bytes {
		return fmt.Errorf("volume size smaller than image")
	}

	return nil
}

func (s *Server) validateImage(ctx context.Context, log logr.Logger, volume *ori.Volume) (*PopulationImage, error) {
	imageName := volume.GetSpec().GetImage()
	if imageName == "" {
		return nil, nil
	}

	img, err := populate.ResolveImage(ctx, log, imageName)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve image: %w", err)
	}

	if img.RootFS == nil {
		return nil, fmt.Errorf("failed to get rootFS layer")
	}

	return &PopulationImage{
		Name:  imageName,
		Bytes: uint64(img.RootFS.Descriptor().Size),
	}, nil
}

func (s *Server) validateVolume(ctx context.Context, log logr.Logger, oriVolume *ori.Volume) error {
	log.V(2).Info("Starting volume validation")
	defer log.V(2).Info("Finished volume validation")

	if oriVolume == nil {
		return fmt.Errorf("volume is nil")
	}

	if err := s.validateVolumeClass(oriVolume); err != nil {
		return fmt.Errorf("invalid volume class: %w", err)
	}
	log.V(2).Info("Validated class")

	validatedImage, err := s.validateImage(ctx, log, oriVolume)
	if err != nil {
		return fmt.Errorf("invalid volume image: %w", err)
	}
	log.V(2).Info("Validated image")

	if err := s.validateSize(oriVolume, validatedImage); err != nil {
		return fmt.Errorf("invalid volume size: %w", err)
	}
	log.V(2).Info("Validated size")

	return nil

}

func (s *Server) prepareOSImage(ctx context.Context, log logr.Logger, osImageName string) (osImageId string, retErr error) {
	c, cleanup := setupCleaner(ctx, log, &retErr)
	defer cleanup()

	osImageId, found, err := s.provisioner.GetOsImage(ctx, osImageName)
	if err != nil {
		return "", fmt.Errorf("unable to get os image: %w", err)
	}

	if found {
		return osImageId, nil
	}

	osImageId = s.idGen.Generate()
	if err := s.provisioner.CreateOsImage(ctx, osImageName, osImageId); err != nil {
		return "", fmt.Errorf("unable to create os image: %w", err)
	}
	c.Add(func(ctx context.Context) error {
		log.V(2).Info("Delete os image")
		return s.provisioner.DeleteOsImage(ctx, osImageName, osImageId)
	})
	c.Reset()

	return osImageId, nil
}

func (s *Server) createCephImage(ctx context.Context, log logr.Logger, volume *ori.Volume) (cephImage *CephImage, retErr error) {
	c, cleanup := setupCleaner(ctx, log, &retErr)
	defer cleanup()

	imageId := s.idGen.Generate()

	log.V(2).Info("Try to acquire lock", "imageId", imageId)
	if err := s.lock(imageId); err != nil {
		return nil, fmt.Errorf("unable to acquire lock: %w", err)
	}
	defer s.release(imageId)

	populationImageName := volume.Spec.Image
	var populationImageId string
	if populationImageName != "" {
		osImageId, err, _ := s.syncPopulation.Do(populationImageName, func() (interface{}, error) {
			return s.prepareOSImage(ctx, log, populationImageName)
		})

		if err != nil {
			return nil, fmt.Errorf("failed to create os image: %w", err)
		}

		populationImageId = osImageId.(string)
	}

	class, found := s.AvailableVolumeClasses[volume.Spec.Class]
	if !found {
		return nil, fmt.Errorf("not supported volume class: %s", volume.Spec.Class)
	}

	log.V(2).Info("Create ceph image")
	cephImage, err := s.provisioner.CreateCephImage(ctx, imageId, volume, &class, populationImageId)
	if err != nil {
		return nil, fmt.Errorf("unable to create ceph image: %w", err)
	}
	c.Add(func(ctx context.Context) error {
		return s.provisioner.DeleteCephImage(ctx, imageId)
	})
	c.Reset()

	return cephImage, nil
}

func (s *Server) CreateVolume(ctx context.Context, req *ori.CreateVolumeRequest) (res *ori.CreateVolumeResponse, retErr error) {
	log := s.loggerFrom(ctx)
	log.V(1).Info("Validating volume request")

	if err := s.validateVolume(ctx, log, req.Volume); err != nil {
		return nil, fmt.Errorf("unable to get ceph volume config: %w", err)
	}

	cephImage, err := s.createCephImage(ctx, log, req.Volume)
	if err != nil {
		return nil, fmt.Errorf("unable to create ceph volume: %w", err)
	}

	oriVolume, err := s.createOriVolume(ctx, log, cephImage)
	if err != nil {
		return nil, fmt.Errorf("unable to create ceph volume: %w", err)
	}

	return &ori.CreateVolumeResponse{
		Volume: oriVolume,
	}, nil
}
