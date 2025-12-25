// SPDX-FileCopyrightText: 2022 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package volumeserver

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/ironcore-dev/ceph-provider/api"
	"github.com/ironcore-dev/ceph-provider/internal/limits"
	"github.com/ironcore-dev/ceph-provider/internal/utils"
	iriv1alpha1 "github.com/ironcore-dev/ironcore/iri/apis/volume/v1alpha1"
	apiutils "github.com/ironcore-dev/provider-utils/apiutils/api"
)

const (
	EncryptionSecretDataPassphraseKey = "encryptionKey"
)

func (s *Server) createImageFromVolume(ctx context.Context, log logr.Logger, volume *iriv1alpha1.Volume) (*api.Image, error) {
	if volume == nil {
		return nil, fmt.Errorf("got an empty volume")
	}

	var err error
	var imageSize uint64
	var encryptionType = api.EncryptionTypeUnencrypted
	var encryptedPassphrase []byte
	log.V(2).Info("Getting image size and encryption from IRI volume")
	if volume.Spec.Resources != nil {
		if imageSize, err = utils.Int64ToUint64(volume.Spec.Resources.StorageBytes); err != nil {
			return nil, fmt.Errorf("failed to get image size: %w", err)
		}
	}

	if encryption := volume.Spec.Encryption; encryption != nil {
		if encryption.SecretData == nil {
			return nil, fmt.Errorf("encryption enabled but SecretData missing")
		}
		passphrase, found := encryption.SecretData[EncryptionSecretDataPassphraseKey]
		if !found {
			return nil, fmt.Errorf("encryption enabled but secret data with key %q missing", EncryptionSecretDataPassphraseKey)
		}

		if encryptedPassphrase, err = s.keyEncryption.Encrypt(passphrase); err != nil {
			return nil, fmt.Errorf("failed to encrypt passphrase: %w", err)
		}
		encryptionType = api.EncryptionTypeEncrypted
	}

	log.V(2).Info("Getting volume data source")
	volImage := volume.Spec.Image // TODO: Remove this once volume.Spec.Image is deprecated

	var snapshotID *string
	if dataSource := volume.Spec.VolumeDataSource; dataSource != nil {
		switch {
		case dataSource.SnapshotDataSource != nil:
			volImage = "" // TODO: Remove this once volume.Spec.Image is deprecated

			snapshotID = &dataSource.SnapshotDataSource.SnapshotId
			log.V(2).Info("Getting snapshot data source", "snapshotID", snapshotID)
			snapshot, err := s.snapshotStore.Get(ctx, *snapshotID)
			if err != nil {
				return nil, fmt.Errorf("failed to get volume snapshot from store: %w", err)
			}

			if snapshot.Source.VolumeImageID == "" {
				return nil, fmt.Errorf("snapshot doesn't have source volume ID")
			}

			var snapshotSourceVolume *api.Image
			if snapshotSourceVolume, err = s.imageStore.Get(ctx, snapshot.Source.VolumeImageID); err != nil {
				return nil, fmt.Errorf("failed to get snapshot source volume from store: %w", err)
			}

			log.V(2).Info("Getting image size and encryption from snapshot source volume", "snapshotSourceVolumeID", snapshotSourceVolume.ID)
			imageSize = snapshotSourceVolume.Spec.Size
			if snapshotSourceVolume.Spec.Encryption != nil {
				encryptionType = snapshotSourceVolume.Spec.Encryption.Type
				encryptedPassphrase = snapshotSourceVolume.Spec.Encryption.EncryptedPassphrase
			}

		case dataSource.ImageDataSource != nil:
			volImage = dataSource.ImageDataSource.Image
			log.V(2).Info("Getting image data source", "imageID", volImage)
			if volImage == "" {
				return nil, fmt.Errorf("must specify image url in image data source")
			}

		default:
			return nil, fmt.Errorf("unsupported or incomplete volume data source type")
		}
	}

	log.V(2).Info("Getting volume class")
	class, found := s.volumeClasses.Get(volume.Spec.Class)
	if !found {
		return nil, fmt.Errorf("volume class '%s' not supported", volume.Spec.Class)
	}

	log.V(2).Info("Getting volume limits")
	calculatedLimits := limits.Calculate(class.Capabilities.Iops, class.Capabilities.Tps, s.burstFactor, s.burstDurationInSeconds)

	image := &api.Image{
		Metadata: apiutils.Metadata{
			ID: s.idGen.Generate(),
		},
		Spec: api.ImageSpec{
			Size:        imageSize,
			Limits:      calculatedLimits,
			Image:       volImage,
			SnapshotRef: snapshotID,
			Encryption: &api.EncryptionSpec{
				Type:                encryptionType,
				EncryptedPassphrase: encryptedPassphrase,
			},
		},
	}

	log.V(2).Info("Setting volume metadata to image")
	if err := api.SetObjectMetadataFromMetadata(image, volume.Metadata); err != nil {
		return nil, fmt.Errorf("failed to set metadata: %w", err)
	}
	api.SetClassLabelForObject(image, volume.Spec.Class)
	api.SetManagerLabel(image, api.VolumeManager)

	log.V(2).Info("Creating image in store")
	image, err = s.imageStore.Create(ctx, image)
	if err != nil {
		return nil, fmt.Errorf("failed to create image: %w", err)
	}

	log.V(2).Info("Image created", "ImageID", image.ID)
	return image, nil
}

func (s *Server) CreateVolume(ctx context.Context, req *iriv1alpha1.CreateVolumeRequest) (res *iriv1alpha1.CreateVolumeResponse, retErr error) {
	log := s.loggerFrom(ctx)
	log.V(1).Info("Creating volume")

	log.V(1).Info("Creating Ceph image from volume")
	image, err := s.createImageFromVolume(ctx, log, req.Volume)
	if err != nil {
		return nil, utils.ConvertInternalErrorToGRPC(fmt.Errorf("unable to create ceph volume: %w", err))
	}

	log = log.WithValues("ImageID", image.ID)

	log.V(1).Info("Converting image to IRI volume")
	iriVolume, err := s.convertImageToIriVolume(image)
	if err != nil {
		return nil, utils.ConvertInternalErrorToGRPC(fmt.Errorf("unable to create ceph volume: %w", err))
	}

	log.V(1).Info("Volume created", "Volume", iriVolume.Metadata.Id, "State", iriVolume.Status.State)
	return &iriv1alpha1.CreateVolumeResponse{
		Volume: iriVolume,
	}, nil
}
