// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package volumeserver

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-logr/logr"
	api "github.com/ironcore-dev/ceph-provider/api/v2"
	"github.com/ironcore-dev/ceph-provider/internal/limits"
	"github.com/ironcore-dev/ceph-provider/internal/utils"
	"github.com/ironcore-dev/controller-utils/metautils"
	iriv1alpha1 "github.com/ironcore-dev/ironcore/iri/apis/volume/v1alpha1"
	apiutils "github.com/ironcore-dev/provider-utils/apiutils/api"
	"github.com/ironcore-dev/provider-utils/storeutils/store"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/registry"
	"oras.land/oras-go/v2/registry/remote"
)

const (
	EncryptionSecretDataPassphraseKey = "encryptionKey"
)

func (s *Server) createOrGetOSImage(ctx context.Context, log logr.Logger, iriVolume *iriv1alpha1.Volume) (*api.OSImage, error) {
	// Skip OSImage creation if no image data source is specified
	dataSource := iriVolume.Spec.VolumeDataSource
	if dataSource == nil {
		return nil, nil
	}
	imageSource := dataSource.ImageDataSource
	if imageSource == nil {
		return nil, nil
	}

	if imageSource.Image == "" {
		return nil, fmt.Errorf("must specify image url in image data source")
	}
	imageRef, err := registry.ParseReference(imageSource.Image)
	if err != nil {
		return nil, fmt.Errorf("failed to parse image source: %w", err)
	}
	imageName := imageRef.String()

	var imageArch string
	if arch := imageSource.Architecture; arch != "" {
		imageArch = arch
	} else if iriVolume.Metadata != nil {
		if a, ok := iriVolume.Metadata.Labels[api.MachineArchitectureLabel]; ok {
			imageArch = a
		}
	}

	// Resolve digest for architecture from image reference, as OS images are architecture-specific.
	imageDigest, err := imageRef.Digest()
	if err != nil {
		// If the image reference does not contain a digest, it must be resolved from the repository.
		repo, err := remote.NewRepository(imageRef.Repository)
		if err != nil {
			return nil, fmt.Errorf("failed to create remote image repository: %w", err)
		}
		log.V(2).Info("Resolve OCI reference for OS image", "reference", imageRef.String(), "architecture", imageArch)
		desc, err := oras.Resolve(ctx, repo, imageRef.Reference, oras.ResolveOptions{
			TargetPlatform: &ocispec.Platform{OS: "linux", Architecture: imageArch},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to resolve image reference: %w", err)
		}
		imageDigest = desc.Digest
	}
	osImageID := imageDigest.Encoded()
	imageRef.Reference = imageDigest.String()

	osImage, err := s.osImageStore.Get(ctx, osImageID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, fmt.Errorf("failed to get OS image from store: %w", err)
	}
	if err == nil {
		log.V(2).Info("OS image already exists in store", "OSImageId", osImage.ID)
		return osImage, nil
	}

	log.V(1).Info("Creating OS image", "OSImageId", osImageID)
	osImage = &api.OSImage{
		Metadata: apiutils.Metadata{
			ID: osImageID,
		},
		Spec: api.OSImageSpec{
			Source: api.OSImageSource{
				Reference: imageRef.String(),
			},
		},
	}

	log.V(2).Info("Setting OS image metadata")
	api.SetManagerLabel(osImage, api.VolumeManager)

	// TODO: Is this the right set of labels to attach to an OS image? Is there something missing? OR should we move some of them to annotations?
	metautils.SetLabels(osImage, map[string]string{
		"architecture": imageArch,
		"os":           "linux",
		"name":         imageName,
		"digest":       imageRef.String(),
	})

	log.V(2).Info("Creating OS image in store")
	osImage, err = s.osImageStore.Create(ctx, osImage)
	if err != nil {
		return nil, fmt.Errorf("failed to create OS image: %w", err)
	}

	log.V(2).Info("OS image created", "OSImageID", osImage.ID)
	return osImage, nil
}

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
			osImage, err := s.createOrGetOSImage(ctx, log, iriVolume)
			if err != nil || osImage == nil {
				return nil, fmt.Errorf("failed to create OS image: %w", err)
			}
			source.OSImage = &osImage.ID
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
