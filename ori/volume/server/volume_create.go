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
	volumev1alpha1 "github.com/onmetal/cephlet/ori/volume/api/v1alpha1"
	"github.com/onmetal/cephlet/ori/volume/apiutils"
	"github.com/onmetal/cephlet/pkg/api"
	"github.com/onmetal/cephlet/pkg/limits"
	ori "github.com/onmetal/onmetal-api/ori/apis/volume/v1alpha1"
)

const (
	EncryptionSecretDataPassphraseKey = "encryptionKey"
)

type CephVolumeConfig struct {
	image *api.Image
}

type AggregatedImage struct {
	image *api.Image
}

func (s *Server) getCephVolumeConfig(ctx context.Context, log logr.Logger, volume *ori.Volume) (*CephVolumeConfig, error) {
	log.V(2).Info("Getting ceph volume config")

	if volume == nil {
		return nil, fmt.Errorf("volume is nil")
	}

	class, found := s.volumeClasses.Get(volume.Spec.Class)
	if !found {
		return nil, fmt.Errorf("volume class '%s' not supported", volume.Spec.Class)
	}
	log.V(2).Info("Validated class")

	calculatedLimits := limits.Calculate(class.Capabilities.Iops, class.Capabilities.Tps, s.burstFactor, s.burstDurationInSeconds)

	image := &api.Image{
		Metadata: api.Metadata{
			ID: s.idGen.Generate(),
		},
		Spec: api.ImageSpec{
			Size:   volume.Spec.Resources.StorageBytes,
			Limits: calculatedLimits,
			Image:  volume.Spec.Image,
			Encryption: api.EncryptionSpec{
				Type: api.EncryptionTypeUnencrypted,
			},
		},
	}

	if encryption := volume.Spec.Encryption; encryption != nil {
		if encryption.SecretData == nil {
			return nil, fmt.Errorf("encryption enabled but SecretData missing")
		}
		passphrase, found := encryption.SecretData[EncryptionSecretDataPassphraseKey]
		if !found {
			return nil, fmt.Errorf("encryption enabled but secret data with key %q missing", EncryptionSecretDataPassphraseKey)
		}

		encryptedPassphrase, err := s.keyEncryption.Encrypt(passphrase)
		if err != nil {
			return nil, fmt.Errorf("failed to encrypt passphrase: %w", err)
		}

		image.Spec.Encryption.Type = api.EncryptionTypeEncrypted
		image.Spec.Encryption.EncryptedPassphrase = encryptedPassphrase
	}

	if err := apiutils.SetObjectMetadata(image, volume.Metadata); err != nil {
		return nil, fmt.Errorf("failed to set metadata: %w", err)
	}
	apiutils.SetClassLabel(image, volume.Spec.Class)
	apiutils.SetManagerLabel(image, volumev1alpha1.VolumeManager)

	return &CephVolumeConfig{
		image: image,
	}, nil

}

func (s *Server) createImage(ctx context.Context, log logr.Logger, cfg *CephVolumeConfig) (*AggregatedImage, error) {
	image, err := s.imageStore.Create(ctx, cfg.image)
	if err != nil {
		return nil, fmt.Errorf("failed to create image: %w", err)
	}

	return &AggregatedImage{image: image}, nil
}

func (s *Server) CreateVolume(ctx context.Context, req *ori.CreateVolumeRequest) (res *ori.CreateVolumeResponse, retErr error) {
	log := s.loggerFrom(ctx)
	log.V(1).Info("Validating volume request")

	cfg, err := s.getCephVolumeConfig(ctx, log, req.Volume)
	if err != nil {
		return nil, fmt.Errorf("unable to get ceph volume config: %w", err)
	}

	volume, err := s.createImage(ctx, log, cfg)
	if err != nil {
		return nil, fmt.Errorf("unable to create ceph volume: %w", err)
	}

	oriVolume, err := s.convertImageToOriVolume(ctx, log, volume.image)
	if err != nil {
		return nil, fmt.Errorf("unable to create ceph volume: %w", err)
	}

	return &ori.CreateVolumeResponse{
		Volume: oriVolume,
	}, nil
}
