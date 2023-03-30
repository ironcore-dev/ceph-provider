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
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/onmetal/cephlet/pkg/api"
	"github.com/onmetal/cephlet/pkg/limits"
	"github.com/onmetal/cephlet/pkg/round"
	"github.com/onmetal/cephlet/pkg/store"
	ori "github.com/onmetal/onmetal-api/ori/apis/volume/v1alpha1"
	"github.com/opencontainers/go-digest"
	"k8s.io/utils/pointer"
)

type CephVolumeConfig struct {
	image    *api.Image
	snapshot *api.Snapshot
}

func (s *Server) getCephVolumeConfig(ctx context.Context, log logr.Logger, volume *ori.Volume) (*CephVolumeConfig, error) {
	log.V(2).Info("Starting volume validation")
	defer log.V(2).Info("Finished volume validation")

	if volume == nil {
		return nil, fmt.Errorf("volume is nil")
	}

	class, found := s.volumeClasses.Get(volume.Spec.Class)
	if !found {
		return nil, fmt.Errorf("volume class '%s' not supported", volume.Spec.Class)
	}
	log.V(2).Info("Validated class")

	calculatedLimits := limits.Calculate(class.Capabilities.Iops, class.Capabilities.Tps, s.burstFactor, s.burstDurationInSeconds)

	labels, annotations := map[string]string{}, map[string]string{}
	for key, value := range volume.Metadata.Labels {
		labels[oriMetadataKey(key)] = value
	}

	for key, value := range volume.Metadata.Annotations {
		annotations[oriMetadataKey(key)] = value
	}

	labels[volumeClassLabel] = volume.Spec.Class

	var snapshotRef *string
	var snapshot *api.Snapshot
	if image := volume.Spec.Image; image != "" {
		hash := sha256.New()
		hash.Write([]byte(image))
		digest := digest.NewDigest(digest.SHA256, hash)

		snapshotID := digest.String()
		snapshotRef = pointer.String(snapshotID)

		snapshot = &api.Snapshot{
			Metadata: api.Metadata{
				ID: snapshotID,
			},
			Source: api.SnapshotSource{
				OnmetalImage: image,
			},
		}
		labels[snapshotNameLabel] = image
	}

	return &CephVolumeConfig{
		image: &api.Image{
			Metadata: api.Metadata{
				ID:          s.idGen.Generate(),
				Annotations: annotations,
				Labels:      labels,
			},
			Spec: api.ImageSpec{
				Size:        int64(round.OffBytes(volume.Spec.Resources.StorageBytes)),
				Limits:      calculatedLimits,
				SnapshotRef: snapshotRef,
			},
		},
		snapshot: snapshot,
	}, nil

}

func (s *Server) createImage(ctx context.Context, log logr.Logger, cfg *CephVolumeConfig) (retErr error) {
	if cfg.snapshot != nil {
		_, err := s.snapshotStore.Get(ctx, cfg.snapshot.ID)
		if err != nil {
			if !errors.Is(err, store.ErrNotFound) {
				return fmt.Errorf("failed to get snapshot: %w", err)
			}

			if _, err := s.snapshotStore.Create(ctx, cfg.snapshot); store.IgnoreAlreadyExists(err) != nil {
				return fmt.Errorf("failed to create snapshot: %w", err)
			}
		}
	}

	image, err := s.imageStore.Create(ctx, cfg.image)
	if err != nil {
		return fmt.Errorf("failed to create image: %w", err)
	}

	cfg.image = image

	return nil
}

func (s *Server) CreateVolume(ctx context.Context, req *ori.CreateVolumeRequest) (res *ori.CreateVolumeResponse, retErr error) {
	log := s.loggerFrom(ctx)
	log.V(1).Info("Validating volume request")

	cfg, err := s.getCephVolumeConfig(ctx, log, req.Volume)
	if err != nil {
		return nil, fmt.Errorf("unable to get ceph volume config: %w", err)
	}

	if err := s.createImage(ctx, log, cfg); err != nil {
		return nil, fmt.Errorf("unable to create ceph volume: %w", err)
	}

	oriVolume, err := s.convertImageToOriVolume(ctx, log, cfg.image)
	if err != nil {
		return nil, fmt.Errorf("unable to create ceph volume: %w", err)
	}

	return &ori.CreateVolumeResponse{
		Volume: oriVolume,
	}, nil
}
