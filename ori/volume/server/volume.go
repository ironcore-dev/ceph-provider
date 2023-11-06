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
	"fmt"

	"github.com/onmetal/cephlet/ori/volume/apiutils"
	"github.com/onmetal/cephlet/pkg/api"
	"github.com/onmetal/cephlet/pkg/utils"
	ori "github.com/onmetal/onmetal-api/ori/apis/volume/v1alpha1"
)

const (
	MonitorsKey = "monitors"
	ImageKey    = "image"
	UserIDKey   = "userID"
	UserKeyKey  = "userKey"
	DriverName  = "ceph"
)

func (s *Server) convertImageToOriVolume(image *api.Image) (*ori.Volume, error) {
	metadata, err := apiutils.GetObjectMetadata(image.Metadata)
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
		access, err = s.getOriVolumeAccess(image)
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

func (s *Server) getOriVolumeAccess(image *api.Image) (*ori.VolumeAccess, error) {
	access := image.Status.Access
	if access == nil {
		return nil, fmt.Errorf("image access not present")
	}

	return &ori.VolumeAccess{
		Driver: DriverName,
		Handle: image.Spec.WWN,
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
	storageBytes, err := utils.Uint64ToInt64(image.Spec.Size)
	if err != nil {
		return nil, err
	}

	spec := &ori.VolumeSpec{
		Image: image.Spec.Image,
		Resources: &ori.VolumeResources{
			StorageBytes: storageBytes,
		},
	}

	class, ok := apiutils.GetClassLabel(image)
	if !ok {
		return nil, fmt.Errorf("failed to get volume class")
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
