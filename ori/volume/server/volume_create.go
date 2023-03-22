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
	"github.com/onmetal/cephlet/pkg/round"
	ori "github.com/onmetal/onmetal-api/ori/apis/volume/v1alpha1"
	onmetalimage "github.com/onmetal/onmetal-image"
	"github.com/onmetal/onmetal-image/oci/image"
	"github.com/onmetal/onmetal-image/oci/remote"
)

func (s *Server) validateName(volume *ori.Volume) (string, error) {
	if volume.Metadata == nil {
		return "", fmt.Errorf("metadata not defined")
	}

	name, ok := volume.Metadata.Labels[s.VolumeNameLabelName]
	if !ok {
		return "", fmt.Errorf("no name label '%s' found", s.VolumeNameLabelName)
	}

	return name, nil
}

func (s *Server) validateVolumeClass(volume *ori.Volume) (*ori.VolumeClass, error) {
	class, found := s.AvailableVolumeClasses[volume.Spec.Class]
	if !found {
		return nil, fmt.Errorf("volume class '%s' not supported", volume.Spec.Class)
	}

	return &class, nil
}

func (s *Server) validateSize(volume *ori.Volume, image *PopulationImage) (uint64, error) {
	if image != nil && volume.Spec.Resources.StorageBytes <= image.Bytes {
		return 0, fmt.Errorf("volume size smaller than image")
	}

	return volume.Spec.Resources.StorageBytes, nil
}

func (s *Server) validateImage(ctx context.Context, volume *ori.Volume) (*PopulationImage, error) {
	imageName := volume.GetSpec().GetImage()
	if imageName == "" {
		return nil, nil
	}

	reg, err := remote.DockerRegistry(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize registry: %w", err)
	}

	img, err := reg.Resolve(ctx, imageName)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve image ref in registry: %w", err)
	}

	layers, err := img.Layers(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get layers for image: %w", err)
	}

	var rootFSLayer image.Layer
	for _, l := range layers {
		if l.Descriptor().MediaType == onmetalimage.RootFSLayerMediaType {
			rootFSLayer = l
			break
		}
	}
	if rootFSLayer == nil {
		return nil, fmt.Errorf("failed to get rootFS layer")
	}

	return &PopulationImage{
		Name:  imageName,
		Bytes: uint64(rootFSLayer.Descriptor().Size),
	}, nil
}

func (s *Server) getAggregateVolume(ctx context.Context, log logr.Logger, oriVolume *ori.Volume) (*AggregateVolume, error) {
	log.V(2).Info("Starting volume validation")
	defer log.V(2).Info("Finished volume validation")

	if oriVolume == nil {
		return nil, fmt.Errorf("volume is nil")
	}

	validatedName, err := s.validateName(oriVolume)
	if err != nil {
		return nil, fmt.Errorf("invalid volume name: %w", err)
	}
	log.V(2).Info("Validated name")

	validatedClass, err := s.validateVolumeClass(oriVolume)
	if err != nil {
		return nil, fmt.Errorf("invalid volume class: %w", err)
	}
	log.V(2).Info("Validated class")

	validatedImage, err := s.validateImage(ctx, oriVolume)
	if err != nil {
		return nil, fmt.Errorf("invalid volume image: %w", err)
	}
	log.V(2).Info("Validated image")

	validatedSize, err := s.validateSize(oriVolume, validatedImage)
	if err != nil {
		return nil, fmt.Errorf("invalid volume size: %w", err)
	}
	log.V(2).Info("Validated size")

	return &AggregateVolume{
		Requested: Volume{
			Name:  validatedName,
			Image: validatedImage,
			Size:  validatedSize,
			Class: validatedClass.Name,
			IOPS:  validatedClass.GetCapabilities().GetIops(),
			TPS:   validatedClass.GetCapabilities().GetTps(),
		},
	}, nil

}

func (s *Server) prepareOSImage(ctx context.Context, log logr.Logger, aggregateVolume *AggregateVolume) (retErr error) {
	c, cleanup := setupCleaner(ctx, log, &retErr)
	defer cleanup()

	osImageName := aggregateVolume.Requested.Image.Name

	log.V(2).Info("Try to acquire lock for volume", "osImageName", osImageName)
	if err := s.lock(osImageName); err != nil {
		return fmt.Errorf("unable to acquire lock: %w", err)
	}
	defer s.release(osImageName)

	log.V(2).Info("Check if mapping exists")
	osImageId, foundMapping, err := s.provisioner.GetMapping(ctx, osImageName, OsImage)
	if err != nil {
		return fmt.Errorf("unable to fetch os volume mapping: %w", err)
	}

	if foundMapping {
		foundImage, err := s.provisioner.GetOsImage(ctx, osImageId)
		if err != nil {
			return fmt.Errorf("unable to get os image: %w", err)
		}

		if !foundImage {
			if err := s.provisioner.DeleteOsImage(ctx, osImageId); err != nil {
				return fmt.Errorf("unable to delete os image: %w", err)
			}
			if err := s.provisioner.DeleteMapping(ctx, osImageName, OsImage); err != nil {
				return fmt.Errorf("unable to delete os image mapping: %w", err)
			}

			return fmt.Errorf("corrupted os image stated: deleted image & mapping")
		}

		aggregateVolume.Provisioned.PopulatedImageId = osImageId
		aggregateVolume.Provisioned.PopulatedImageName = osImageName
		return nil
	}

	osImageId = s.idGen.Generate()
	aggregateVolume.Provisioned.PopulatedImageId = osImageId
	log.V(2).Info("Create os image")
	if err := s.provisioner.CreateOsImage(ctx, aggregateVolume); err != nil {
		return fmt.Errorf("failed to create os image: %w", err)
	}

	c.Add(func(ctx context.Context) error {
		log.V(2).Info("Delete os image")
		return s.provisioner.DeleteOsImage(ctx, osImageId)
	})

	log.V(2).Info("Create os image mapping")
	if err := s.provisioner.CreateMapping(ctx, osImageName, aggregateVolume.Provisioned.PopulatedImageId, OsImage); err != nil {
		return fmt.Errorf("unable to write mapping: %w", err)
	}
	c.Add(func(ctx context.Context) error {
		return s.provisioner.DeleteMapping(ctx, osImageName, OsImage)
	})
	c.Reset()

	aggregateVolume.Provisioned.PopulatedImageName = osImageName

	return
}

func (s *Server) createCephImage(ctx context.Context, log logr.Logger, aggregateVolume *AggregateVolume) (retErr error) {
	c, cleanup := setupCleaner(ctx, log, &retErr)
	defer cleanup()

	volumeName := aggregateVolume.Requested.Name
	log.V(2).Info("Try to acquire lock for volume", "volumeName", volumeName)
	if err := s.lock(volumeName); err != nil {
		return fmt.Errorf("unable to acquire lock: %w", err)
	}
	defer s.release(volumeName)

	if aggregateVolume.Requested.Image != nil {
		if err := s.prepareOSImage(ctx, log, aggregateVolume); err != nil {
			return fmt.Errorf("err: %w", err)
		}
	}

	log.V(2).Info("Check if mapping exists")
	imageName, foundMapping, err := s.provisioner.GetMapping(ctx, volumeName, RbdImage)
	if err != nil {
		return fmt.Errorf("unable to fetch volume mapping: %w", err)
	}

	if foundMapping {
		aggregateVolume.Provisioned.Name = imageName

		foundImage, err := s.provisioner.GetCephImage(ctx, imageName, &aggregateVolume.Provisioned)
		if err != nil {
			return fmt.Errorf("unable to get image: %w", err)
		}

		if !foundImage {
			if err := s.provisioner.DeleteCephImage(ctx, imageName); err != nil {
				return fmt.Errorf("unable to delete image: %w", err)
			}

			if err := s.provisioner.DeleteMapping(ctx, volumeName, RbdImage); err != nil {
				return fmt.Errorf("unable to delete mapping: %w", err)
			}

			return fmt.Errorf("corrupted state since image is missing: deleted mapping")
		}

		log.V(2).Info("Nothing updated since update is not supported: Returning found ceph image.")
		return nil
	}

	imageName = s.idGen.Generate()
	aggregateVolume.Provisioned.Name = imageName
	log.V(2).Info("Set image id.", "volumeName", volumeName, "volumeId", aggregateVolume.Provisioned.Name)

	aggregateVolume.Provisioned.Size = round.OffBytes(aggregateVolume.Requested.Size)
	log.V(2).Info("Set image size.", "volumeName", volumeName, "requested", aggregateVolume.Requested.Size, "configured", aggregateVolume.Provisioned.Size)

	aggregateVolume.Provisioned.Wwn, err = generateWWN()
	if err != nil {
		return fmt.Errorf("unable to generate wwn: %w", err)
	}

	aggregateVolume.Provisioned.Class = aggregateVolume.Requested.Class

	if err := s.provisioner.CreateMapping(ctx, volumeName, imageName, RbdImage); err != nil {
		return fmt.Errorf("unable to write volume mapping: %w", err)
	}
	c.Add(func(ctx context.Context) error {
		return s.provisioner.DeleteMapping(ctx, volumeName, RbdImage)
	})

	if err := s.provisioner.CreateCephImage(ctx, aggregateVolume); err != nil {
		return fmt.Errorf("unable to create ceph image: %w", err)
	}
	c.Add(func(ctx context.Context) error {
		return s.provisioner.DeleteCephImage(ctx, imageName)
	})

	c.Reset()

	return nil
}

func (s *Server) CreateVolume(ctx context.Context, req *ori.CreateVolumeRequest) (res *ori.CreateVolumeResponse, retErr error) {
	log := s.loggerFrom(ctx)
	log.V(1).Info("Validating volume request")

	aggregateVolume, err := s.getAggregateVolume(ctx, log, req.Volume)
	if err != nil {
		return nil, fmt.Errorf("anable to get ceph volume config: %w", err)
	}

	if err := s.createCephImage(ctx, log, aggregateVolume); err != nil {
		return nil, fmt.Errorf("unable to create ceph volume: %w", err)
	}

	oriVolume, err := s.createOriVolume(ctx, log, &aggregateVolume.Provisioned)
	if err != nil {
		return nil, fmt.Errorf("unable to create ceph volume: %w", err)
	}

	return &ori.CreateVolumeResponse{
		Volume: oriVolume,
	}, nil
}
