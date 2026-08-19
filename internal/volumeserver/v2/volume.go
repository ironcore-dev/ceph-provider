// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package volumeserver

import (
	"fmt"

	api "github.com/ironcore-dev/ceph-provider/api/v2"
	"github.com/ironcore-dev/ceph-provider/internal/utils"
	iri "github.com/ironcore-dev/ironcore/iri/apis/volume/v1alpha1"
)

const (
	MonitorsKey = "monitors"
	ImageKey    = "image"
	UserIDKey   = "userID"
	UserKeyKey  = "userKey"
	DriverName  = "ceph"
)

func (s *Server) convertVolumeToIRI(volume *api.Volume) (*iri.Volume, error) {
	metadata, err := api.GetObjectMetadataFromObjectID(volume.Metadata)
	if err != nil {
		return nil, fmt.Errorf("error getting iri metadata: %w", err)
	}

	spec, err := s.getIriVolumeSpec(volume)
	if err != nil {
		return nil, fmt.Errorf("error getting iri spec: %w", err)
	}

	state, err := s.getIriState(volume.Status.State)
	if err != nil {
		return nil, fmt.Errorf("error getting iri state: %w", err)
	}

	var access *iri.VolumeAccess
	if state == iri.VolumeState_VOLUME_AVAILABLE && volume.Status.Access != nil {
		access = &iri.VolumeAccess{
			Driver: DriverName,
			Handle: volume.Spec.WWN,
			Attributes: map[string]string{
				MonitorsKey: volume.Status.Access.Monitors,
				ImageKey:    volume.Status.Access.Handle,
			},
			SecretData: map[string][]byte{
				UserIDKey:  []byte(volume.Status.Access.User),
				UserKeyKey: []byte(volume.Status.Access.UserKey),
			},
		}
	}

	volumeSize, err := utils.Uint64ToInt64(volume.Status.Size)
	if err != nil {
		return nil, err
	}

	return &iri.Volume{
		Metadata: metadata,
		Spec:     spec,
		Status: &iri.VolumeStatus{
			State:  state,
			Access: access,
			Resources: &iri.VolumeResources{
				StorageBytes: volumeSize,
			},
		},
	}, nil
}

func (s *Server) getIriVolumeSpec(volume *api.Volume) (*iri.VolumeSpec, error) {
	storageBytes, err := utils.Uint64ToInt64(volume.Spec.Size)
	if err != nil {
		return nil, err
	}

	spec := &iri.VolumeSpec{
		Resources: &iri.VolumeResources{
			StorageBytes: storageBytes,
		},
	}

	class, ok := api.GetClassLabelFromObject(volume)
	if !ok {
		return nil, fmt.Errorf("failed to get volume class for volume: %s", volume.ID)
	}
	spec.Class = class

	return spec, nil
}

func (s *Server) getIriState(state api.VolumeState) (iri.VolumeState, error) {
	switch state {
	case api.VolumeStateAvailable:
		return iri.VolumeState_VOLUME_AVAILABLE, nil
	case api.VolumeStatePending:
		return iri.VolumeState_VOLUME_PENDING, nil
	default:
		return 0, fmt.Errorf("unknown volume state '%q'", state)
	}
}
