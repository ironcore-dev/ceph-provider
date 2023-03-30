// Copyright 2023 OnMetal authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
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
	"strings"

	"github.com/go-logr/logr"
	"github.com/onmetal/cephlet/pkg/api"
	orimeta "github.com/onmetal/onmetal-api/ori/apis/meta/v1alpha1"
	ori "github.com/onmetal/onmetal-api/ori/apis/volume/v1alpha1"
)

const (
	MonitorsKey = "monitors"
	ImageKey    = "image"
	UserIDKey   = "userID"
	UserKeyKey  = "userKey"
)

const (
	volumeClassLabel  = "volume-class"
	snapshotNameLabel = "snapshot-name"
)

const (
	oriMetadataPrefix = "ori_"
)

func oriMetadataKey(key string) string {
	return oriMetadataPrefix + key
}

func isORIMetadataKey(key string) (string, bool) {
	return strings.TrimPrefix(key, oriMetadataPrefix), strings.HasPrefix(key, oriMetadataPrefix)
}

func (s *Server) convertImageToOriVolume(ctx context.Context, log logr.Logger, image *api.Image) (*ori.Volume, error) {
	metadata, err := s.getOriObjectMetadata(&image.Metadata)
	if err != nil {
		return nil, fmt.Errorf("error getting ori metadata: %w", err)
	}

	spec, err := s.getOriVolumeSpec(image)
	if err != nil {
		return nil, fmt.Errorf("error getting ori resources: %w", err)
	}

	state, err := s.getOriState(image.Status.State)
	if err != nil {
		return nil, fmt.Errorf("error getting ori state: %w", err)
	}

	var access *ori.VolumeAccess
	if state == ori.VolumeState_VOLUME_AVAILABLE {
		access, err = s.getOriVolumeAccess(ctx, log, image)
		if err != nil {
			return nil, fmt.Errorf("error getting ori volume access: %w", err)
		}
	}

	return &ori.Volume{
		Metadata: metadata,
		Spec:     spec,
		Status: &ori.VolumeStatus{
			State:  state,
			Access: access,
		},
	}, nil
}

func (s *Server) generateWWN() string {
	wwn := strings.Replace(s.idGen.Generate(), "-", "", -1)
	return wwn[:16]
}

func (s *Server) getOriVolumeAccess(ctx context.Context, log logr.Logger, image *api.Image) (*ori.VolumeAccess, error) {
	access := image.Status.Access
	if access == nil {
		return nil, fmt.Errorf("image access not present")
	}

	return &ori.VolumeAccess{
		Driver: "ceph",
		Handle: s.generateWWN(),
		Attributes: map[string]string{
			MonitorsKey: access.Monitors,
			ImageKey:    access.Handle,
		},
		SecretData: map[string][]byte{
			UserIDKey:  []byte(access.User),
			UserKeyKey: []byte(access.UserKey),
		},
	}, nil
}

func (s *Server) getOriVolumeSpec(image *api.Image) (*ori.VolumeSpec, error) {
	spec := &ori.VolumeSpec{
		Image: image.Labels[snapshotNameLabel],
		Resources: &ori.VolumeResources{
			StorageBytes: uint64(image.Spec.Size),
		},
	}

	class, ok := image.Labels[volumeClassLabel]
	if !ok {
		return nil, fmt.Errorf("failed to get volume class: label %s missing", volumeClassLabel)
	}
	spec.Class = class

	return spec, nil
}

func (s *Server) getOriState(state api.ImageState) (ori.VolumeState, error) {
	switch state {
	case api.ImageStateAvailable:
		return ori.VolumeState_VOLUME_AVAILABLE, nil
	case api.ImageStatePending:
		return ori.VolumeState_VOLUME_PENDING, nil
	default:
		return 0, fmt.Errorf("unknown volume state '%q'", state)
	}
}

func (s *Server) getOriObjectMetadata(meta *api.Metadata) (*orimeta.ObjectMetadata, error) {
	labels, annotations := map[string]string{}, map[string]string{}
	for key, value := range meta.Labels {
		if oriKey, isORIKey := isORIMetadataKey(key); isORIKey {
			labels[oriKey] = value
		}
	}

	for key, value := range meta.Annotations {
		if oriKey, isORIKey := isORIMetadataKey(key); isORIKey {
			annotations[oriKey] = value
		}
	}

	result := &orimeta.ObjectMetadata{
		Id:          meta.ID,
		Annotations: annotations,
		Labels:      labels,
		Generation:  meta.Generation,
		CreatedAt:   meta.CreatedAt.UnixNano(),
	}

	if meta.DeletedAt != nil {
		result.DeletedAt = meta.DeletedAt.UnixNano()
	}

	return result, nil
}
