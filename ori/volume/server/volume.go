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
	"time"

	"github.com/go-logr/logr"
	orimeta "github.com/onmetal/onmetal-api/ori/apis/meta/v1alpha1"
	ori "github.com/onmetal/onmetal-api/ori/apis/volume/v1alpha1"
)

const (
	MonitorsKey = "monitors"
	ImageKey    = "image"
	UserIDKey   = "userID"
	UserKeyKey  = "userKey"
)

type PopulationImage struct {
	Name  string
	Bytes uint64
}

type CephImage struct {
	Id          string
	Annotations map[string]string
	Labels      map[string]string
	Generation  int64
	CreatedAt   time.Time
	DeletedAt   time.Time

	Wwn                string
	Pool               string
	Size               uint64
	PopulatedImageName string
	Class              string
	State              ori.VolumeState
}

func (s *Server) createOriVolume(ctx context.Context, log logr.Logger, image *CephImage) (*ori.Volume, error) {
	metadata, err := s.getOriObjectMetadata(image)
	if err != nil {
		return nil, fmt.Errorf("unable to create ori metadata: %w", err)
	}

	resources, err := s.getOriResources(image)
	if err != nil {
		return nil, fmt.Errorf("unable to create ori resources: %w", err)
	}

	var access *ori.VolumeAccess
	if image.State == ori.VolumeState_VOLUME_AVAILABLE {
		access, err = s.getOriVolumeAccess(ctx, log, image)
		if err != nil {
			return nil, fmt.Errorf("unable to create ori volume access: %w", err)
		}
	}

	return &ori.Volume{
		Metadata: metadata,
		Spec: &ori.VolumeSpec{
			Class:     image.Class,
			Resources: resources,
			Image:     image.PopulatedImageName,
		},
		Status: &ori.VolumeStatus{
			State:  image.State,
			Access: access,
		},
	}, nil
}
func (s *Server) getOriVolumeAccess(ctx context.Context, log logr.Logger, image *CephImage) (*ori.VolumeAccess, error) {
	user, key, err := s.provisioner.FetchAuth(ctx)
	if err != nil {
		return nil, fmt.Errorf("unable to fetch volume credentials: %w", err)
	}
	log.V(3).Info("Fetched volume access credentials", "userId", user)

	return &ori.VolumeAccess{
		Driver: "ceph",
		Handle: image.Wwn,
		Attributes: map[string]string{
			MonitorsKey: s.provisioner.Monitors(),
			ImageKey:    fmt.Sprintf("%s/%s", image.Pool, image.Id),
		},
		SecretData: map[string][]byte{
			UserIDKey:  []byte(user),
			UserKeyKey: []byte(key),
		},
	}, nil
}

func (s *Server) getOriResources(image *CephImage) (*ori.VolumeResources, error) {
	return &ori.VolumeResources{
		StorageBytes: image.Size,
	}, nil
}

func (s *Server) getOriObjectMetadata(image *CephImage) (*orimeta.ObjectMetadata, error) {
	return &orimeta.ObjectMetadata{
		Id:          image.Id,
		Annotations: image.Annotations,
		Labels:      image.Annotations,
		Generation:  image.Generation,
		CreatedAt:   image.CreatedAt.UnixNano(),
	}, nil
}
