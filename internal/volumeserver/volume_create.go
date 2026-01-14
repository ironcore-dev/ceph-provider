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
	"k8s.io/utils/ptr"
)

const (
	EncryptionSecretDataPassphraseKey = "encryptionKey"
)

func getArchitectureFromVolume(volume *iriv1alpha1.Volume) *string {
	if volume != nil && volume.Metadata != nil {
		if arch, found := volume.Metadata.Labels[api.MachineArchitectureLabel]; found {
			return ptr.To(arch)
		}
	}

	return nil
}

func (s *Server) createImageFromVolume(ctx context.Context, log logr.Logger, volume *iriv1alpha1.Volume) (*api.Image, error) {
	if volume == nil {
		return nil, fmt.Errorf("got an empty volume")
	}

	log.V(2).Info("Getting volume data source")
	volImage := volume.Spec.Image // TODO: Remove this once volume.Spec.Image is deprecated
	var snapshotID *string
	if dataSource := volume.Spec.VolumeDataSource; dataSource != nil {
		switch {
		case dataSource.SnapshotDataSource != nil:
			snapshotID = &dataSource.SnapshotDataSource.SnapshotId
			volImage = "" // TODO: Remove this once volume.Spec.Image is deprecated
		case dataSource.ImageDataSource != nil:
			volImage = dataSource.ImageDataSource.Image
		}
	}

	var snapshotSourceVolume *api.Image
	if snapshotID != nil {
		snapshot, err := s.snapshotStore.Get(ctx, *snapshotID)
		if err != nil {
			return nil, fmt.Errorf("failed to get volume snapshot from store: %w", err)
		}
		var snapshotSourceVolumeID string
		if snapshot.Source.VolumeImageID != "" {
			snapshotSourceVolumeID = snapshot.Source.VolumeImageID
		}
		if snapshotSourceVolumeID != "" {
			snapshotSourceVolume, err = s.imageStore.Get(ctx, snapshotSourceVolumeID)
			if err != nil {
				return nil, fmt.Errorf("failed to get snapshot source volume from store: %w", err)
			}
		}
	}

	var imageSize uint64
	var err error
	var encryptionType = api.EncryptionTypeUnencrypted
	var encryptedPassphrase []byte
	log.V(2).Info("Getting image size and volume encryption")
	if snapshotSourceVolume != nil {
		imageSize = snapshotSourceVolume.Status.Size
		if snapshotSourceVolume.Status.Encryption == api.EncryptionStateHeaderSet {
			encryptionType = snapshotSourceVolume.Spec.Encryption.Type
			encryptedPassphrase = snapshotSourceVolume.Spec.Encryption.EncryptedPassphrase
		}
	} else {
		if imageSize, err = utils.Int64ToUint64(volume.Spec.Resources.StorageBytes); err != nil {
			return nil, err
		}
		if encryption := volume.Spec.Encryption; encryption != nil {
			if encryption.SecretData == nil {
				return nil, fmt.Errorf("encryption enabled but SecretData missing")
			}
			passphrase, found := encryption.SecretData[EncryptionSecretDataPassphraseKey]
			if !found {
				return nil, fmt.Errorf("encryption enabled but secret data with key %q missing", EncryptionSecretDataPassphraseKey)
			}

			encryptedPassphrase, err = s.keyEncryption.Encrypt(passphrase)
			if err != nil {
				return nil, fmt.Errorf("failed to encrypt passphrase: %w", err)
			}
			encryptionType = api.EncryptionTypeEncrypted
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
			Size:              imageSize,
			Limits:            calculatedLimits,
			Image:             volImage,
			ImageArchitecture: getArchitectureFromVolume(volume),
			SnapshotRef:       snapshotID,
			Encryption: api.EncryptionSpec{
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
