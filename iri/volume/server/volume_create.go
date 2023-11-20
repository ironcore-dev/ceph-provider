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
	"github.com/ironcore-dev/ceph-provider/iri/volume/apiutils"
	"github.com/ironcore-dev/ceph-provider/pkg/api"
	"github.com/ironcore-dev/ceph-provider/pkg/limits"
	"github.com/ironcore-dev/ceph-provider/pkg/utils"
	iri "github.com/ironcore-dev/ironcore/iri/apis/volume/v1alpha1"
)

const (
	EncryptionSecretDataPassphraseKey = "encryptionKey"
)

func (s *Server) createImageFromVolume(ctx context.Context, log logr.Logger, volume *iri.Volume) (*api.Image, error) {
	if volume == nil {
		return nil, fmt.Errorf("got an empty volume")
	}

	log.V(2).Info("Getting volume class")
	class, found := s.volumeClasses.Get(volume.Spec.Class)
	if !found {
		return nil, fmt.Errorf("volume class '%s' not supported", volume.Spec.Class)
	}

	calculatedLimits := limits.Calculate(class.Capabilities.Iops, class.Capabilities.Tps, s.burstFactor, s.burstDurationInSeconds)

	imageSize, err := utils.Int64ToUint64(volume.Spec.Resources.StorageBytes)
	if err != nil {
		return nil, err
	}

	image := &api.Image{
		Metadata: api.Metadata{
			ID: s.idGen.Generate(),
		},
		Spec: api.ImageSpec{
			Size:   imageSize,
			Limits: calculatedLimits,
			Image:  volume.Spec.Image,
			Encryption: api.EncryptionSpec{
				Type: api.EncryptionTypeUnencrypted,
			},
		},
	}

	log.V(2).Info("Checking volume encryption")
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

	log.V(2).Info("Setting volume metadata to image")
	if err := apiutils.SetObjectMetadata(image, volume.Metadata); err != nil {
		return nil, fmt.Errorf("failed to set metadata: %w", err)
	}
	apiutils.SetClassLabel(image, volume.Spec.Class)
	apiutils.SetManagerLabel(image, apiutils.VolumeManager)

	log.V(2).Info("Creating image in store")
	image, err = s.imageStore.Create(ctx, image)
	if err != nil {
		return nil, fmt.Errorf("failed to create image: %w", err)
	}

	log.V(2).Info("Image created", "ImageID", image.Metadata.ID)
	return image, nil
}

func (s *Server) CreateVolume(ctx context.Context, req *iri.CreateVolumeRequest) (res *iri.CreateVolumeResponse, retErr error) {
	log := s.loggerFrom(ctx)
	log.V(1).Info("Creating volume")

	log.V(1).Info("Creating Ceph image from volume")
	image, err := s.createImageFromVolume(ctx, log, req.Volume)
	if err != nil {
		return nil, fmt.Errorf("unable to create ceph volume: %w", err)
	}

	log = log.WithValues("ImageID", image.Metadata.ID)

	log.V(1).Info("Converting image to IRI volume")
	iriVolume, err := s.convertImageToIriVolume(image)
	if err != nil {
		return nil, fmt.Errorf("unable to create ceph volume: %w", err)
	}

	log.V(1).Info("Volume created", "Volume", iriVolume.Metadata.Id, "State", iriVolume.Status.State)
	return &iri.CreateVolumeResponse{
		Volume: iriVolume,
	}, nil
}
