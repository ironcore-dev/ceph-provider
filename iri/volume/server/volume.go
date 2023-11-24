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

	"github.com/ironcore-dev/ceph-provider/iri/volume/apiutils"
	"github.com/ironcore-dev/ceph-provider/pkg/api"
	"github.com/ironcore-dev/ceph-provider/pkg/utils"
	iri "github.com/ironcore-dev/ironcore/iri/apis/volume/v1alpha1"
)

const (
	MonitorsKey = "monitors"
	ImageKey    = "image"
	UserIDKey   = "userID"
	UserKeyKey  = "userKey"
	DriverName  = "ceph"
)

func (s *Server) convertImageToIriVolume(image *api.Image) (*iri.Volume, error) {
	metadata, err := apiutils.GetObjectMetadata(image.Metadata)
	if err != nil {
		return nil, fmt.Errorf("error getting iri metadata: %w", err)
	}

	spec, err := s.getIriVolumeSpec(image)
	if err != nil {
		return nil, fmt.Errorf("error getting iri resources: %w", err)
	}

	state, err := s.getIriState(image.Status.State)
	if err != nil {
		return nil, fmt.Errorf("error getting iri state: %w", err)
	}

	var access *iri.VolumeAccess
	if state == iri.VolumeState_VOLUME_AVAILABLE {
		access, err = s.getIriVolumeAccess(image)
		if err != nil {
			return nil, fmt.Errorf("error getting iri volume access: %w", err)
		}
	}

	return &iri.Volume{
		Metadata: metadata,
		Spec:     spec,
		Status: &iri.VolumeStatus{
			State:  state,
			Access: access,
		},
	}, nil
}

func (s *Server) getIriVolumeAccess(image *api.Image) (*iri.VolumeAccess, error) {
	access := image.Status.Access
	if access == nil {
		return nil, fmt.Errorf("image access not present")
	}

	return &iri.VolumeAccess{
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

func (s *Server) getIriVolumeSpec(image *api.Image) (*iri.VolumeSpec, error) {
	storageBytes, err := utils.Uint64ToInt64(image.Spec.Size)
	if err != nil {
		return nil, err
	}

	spec := &iri.VolumeSpec{
		Image: image.Spec.Image,
		Resources: &iri.VolumeResources{
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

func (s *Server) getIriState(state api.ImageState) (iri.VolumeState, error) {
	switch state {
	case api.ImageStateAvailable:
		return iri.VolumeState_VOLUME_AVAILABLE, nil
	case api.ImageStatePending:
		return iri.VolumeState_VOLUME_PENDING, nil
	default:
		return 0, fmt.Errorf("unknown volume state '%q'", state)
	}
}
