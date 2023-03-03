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

func (s *Server) validateSize(volume *ori.Volume, image *Image) (uint64, error) {
	if image != nil && volume.Spec.Resources.StorageBytes <= image.Bytes {
		return 0, fmt.Errorf("volume size smaller than image")
	}

	return round.OffBytes(volume.Spec.Resources.StorageBytes), nil
}

func (s *Server) validateImage(ctx context.Context, volume *ori.Volume) (*Image, error) {
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

	return &Image{
		Name:  imageName,
		Bytes: uint64(rootFSLayer.Descriptor().Size),
	}, nil
}

func (s *Server) getCephVolume(ctx context.Context, oriVolume *ori.Volume) (*CephVolume, error) {
	log := s.loggerFrom(ctx)
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
	log.V(2).Info("Validated class")

	validatedSize, err := s.validateSize(oriVolume, validatedImage)
	if err != nil {
		return nil, fmt.Errorf("invalid volume size: %w", err)
	}
	log.V(2).Info("Validated class")

	return &CephVolume{
		Requested: Volume{
			Name:  validatedName,
			Image: validatedImage,
			Bytes: validatedSize,
			IOPS:  validatedClass.GetCapabilities().GetIops(),
			TPS:   validatedClass.GetCapabilities().GetIops(),
		},
	}, nil

}

func (s *Server) createCephVolume(ctx context.Context, log logr.Logger, volume *CephVolume) (retErr error) {
	c, cleanup := setupCleaner(ctx, log, &retErr)
	defer cleanup()

	if err := s.provisioner.Lock(); err != nil {
		//TODO
		return err
	}
	defer s.provisioner.Release()

	found, err := s.provisioner.MappingExists(ctx, volume)
	if err != nil {
		//TODO
		return err
	}

	if found {
		s.provisioner.UpdateCephImage(ctx)
		return nil
	}

	if err := s.provisioner.PutMapping(ctx, volume); err != nil {
		//TODO
		return err
	}
	c.Add(func(ctx context.Context) error {
		return s.provisioner.DeleteMapping(ctx, volume)
	})

	if err := s.provisioner.CreateCephImage(ctx); err != nil {
		//TODO
		return err
	}
	c.Add(func(ctx context.Context) error {
		return s.provisioner.DeleteCephImage(ctx)
	})

	c.Reset()

	return nil
}

func (s *Server) CreateVolume(ctx context.Context, req *ori.CreateVolumeRequest) (res *ori.CreateVolumeResponse, retErr error) {
	log := s.loggerFrom(ctx)
	log.V(1).Info("Validating volume request")

	cephVolume, err := s.getCephVolume(ctx, req.Volume)
	if err != nil {
		return nil, fmt.Errorf("error validating ceph volume config: %w", err)
	}

	_ = cephVolume

	return &ori.CreateVolumeResponse{}, nil
}
