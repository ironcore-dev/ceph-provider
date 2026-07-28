// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package volumeserver

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	api "github.com/ironcore-dev/ceph-provider/api/v2"
	"github.com/ironcore-dev/ceph-provider/internal/limits"
	"github.com/ironcore-dev/ceph-provider/internal/utils"
	iriv1alpha1 "github.com/ironcore-dev/ironcore/iri/apis/volume/v1alpha1"
	apiutils "github.com/ironcore-dev/provider-utils/apiutils/api"
)

const (
	EncryptionSecretDataPassphraseKey = "encryptionKey"
)

func (s *Server) createVolumeFromIRI(ctx context.Context, log logr.Logger, iriVolume *iriv1alpha1.Volume) (*api.Volume, error) {
	if iriVolume == nil {
		return nil, fmt.Errorf("got an empty volume")
	}

	var imageSize uint64
	var err error
	if iriVolume.Spec.Resources != nil {
		if imageSize, err = utils.Int64ToUint64(iriVolume.Spec.Resources.StorageBytes); err != nil {
			return nil, fmt.Errorf("failed to get volume size: %w", err)
		}
	}

	var encryptionSpec api.VolumeEncryptionSpec
	if encryption := iriVolume.Spec.Encryption; encryption != nil {
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
		encryptionSpec = api.VolumeEncryptionSpec{
			Type:                api.VolumeEncryptionTypeEncrypted,
			EncryptedPassphrase: encryptedPassphrase,
		}
	}

	log.V(2).Info("Getting volume class")
	class, found := s.volumeClasses.Get(iriVolume.Spec.Class)
	if !found {
		return nil, fmt.Errorf("volume class '%s' not supported", iriVolume.Spec.Class)
	}

	log.V(2).Info("Getting volume limits")
	calculatedLimits := limits.CalculateV2(class.Capabilities.Iops, class.Capabilities.Tps, s.burstFactor, s.burstDurationInSeconds)

	var source api.VolumeSource
	if dataSource := iriVolume.Spec.VolumeDataSource; dataSource != nil {
		switch {
		case dataSource.SnapshotDataSource != nil:
			snapshotID := dataSource.SnapshotDataSource.SnapshotId
			source.SnapshotSource = &snapshotID
		case dataSource.ImageDataSource != nil:
			if dataSource.ImageDataSource.Image == "" {
				return nil, fmt.Errorf("must specify image url in image data source")
			}
			if imageSize == 0 {
				return nil, fmt.Errorf("must specify size when creating volume from image data source")
			}
			var arch *string
			if a := dataSource.ImageDataSource.Architecture; a != "" {
				arch = &a
			} else if iriVolume.Metadata != nil {
				if a, ok := iriVolume.Metadata.Labels[api.MachineArchitectureLabel]; ok {
					arch = &a
				}
			}
			source.OSVolume = &api.OSVolumeSource{
				Name:         dataSource.ImageDataSource.Image,
				Architecture: arch,
			}
		default:
			return nil, fmt.Errorf("unsupported or incomplete volume data source type")
		}
	}

	volume := &api.Volume{
		Metadata: apiutils.Metadata{
			ID: s.idGen.Generate(),
		},
		Spec: api.VolumeSpec{
			Size:             imageSize,
			Limits:           calculatedLimits,
			VolumeEncryption: encryptionSpec,
			Source:           source,
		},
	}

	log.V(2).Info("Setting volume metadata")
	if err := api.SetObjectMetadataFromMetadata(volume, iriVolume.Metadata); err != nil {
		return nil, fmt.Errorf("failed to set metadata: %w", err)
	}
	api.SetClassLabelForObject(volume, iriVolume.Spec.Class)
	api.SetManagerLabel(volume, api.VolumeManager)

	log.V(2).Info("Creating volume in store")
	volume, err = s.volumeStore.Create(ctx, volume)
	if err != nil {
		return nil, fmt.Errorf("failed to create volume: %w", err)
	}

	log.V(2).Info("Volume created", "VolumeID", volume.ID)
	return volume, nil
}

func (s *Server) CreateVolume(ctx context.Context, req *iriv1alpha1.CreateVolumeRequest) (*iriv1alpha1.CreateVolumeResponse, error) {
	log := s.loggerFrom(ctx)
	log.V(1).Info("Creating volume")

	volume, err := s.createVolumeFromIRI(ctx, log, req.Volume)
	if err != nil {
		return nil, utils.ConvertInternalErrorToGRPC(fmt.Errorf("unable to create volume: %w", err))
	}

	log = log.WithValues("VolumeID", volume.ID)

	log.V(1).Info("Converting volume to IRI volume")
	iriVolume, err := s.convertVolumeToIRI(volume)
	if err != nil {
		return nil, utils.ConvertInternalErrorToGRPC(fmt.Errorf("unable to convert volume: %w", err))
	}

	log.V(1).Info("Volume created", "Volume", iriVolume.Metadata.Id, "State", iriVolume.Status.State)
	return &iriv1alpha1.CreateVolumeResponse{
		Volume: iriVolume,
	}, nil
}
