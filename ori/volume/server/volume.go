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
	"time"

	"github.com/go-logr/logr"
	"github.com/google/uuid"
	orimeta "github.com/onmetal/onmetal-api/ori/apis/meta/v1alpha1"
	ori "github.com/onmetal/onmetal-api/ori/apis/volume/v1alpha1"
)

const (
	// worldwide number key
	// to use WWN Company Identifiers, set wwnPrefix to Private "1100AA"
	wwnPrefix string = ""

	MonitorsKey = "monitors"
	ImageKey    = "image"
	UserIDKey   = "userID"
	UserKeyKey  = "userKey"
)

type AggregateVolume struct {
	Requested   Volume
	Provisioned Image
}

type Volume struct {
	Name string

	Bytes uint64

	Class string
	IOPS  int64
	TPS   int64

	Image *PopulationImage
}

type PopulationImage struct {
	Name  string
	Bytes uint64
}

type Image struct {
	Name           string
	Id             string
	Wwn            string
	Pool           string
	Bytes          uint64
	PopulatedImage string
	Class          string
	Created        time.Time
}

func (s *Server) createOriVolume(ctx context.Context, log logr.Logger, image *Image) (*ori.Volume, error) {
	metadata, err := s.createOriObjectMetadata(image)
	if err != nil {
		return nil, fmt.Errorf("unable to create ori metadata: %w", err)
	}

	resources, err := s.createOriResources(image)
	if err != nil {
		return nil, fmt.Errorf("unable to create ori resources: %w", err)
	}

	state := ori.VolumeState_VOLUME_AVAILABLE
	log.V(2).Info("Set volume state", "state", state)

	access, err := s.createOriVolumeAccess(ctx, log, image)
	if err != nil {
		return nil, fmt.Errorf("unable to create ori volume access: %w", err)
	}

	return &ori.Volume{
		Metadata: metadata,
		Spec: &ori.VolumeSpec{
			Class:     image.Class,
			Resources: resources,
			Image:     image.PopulatedImage,
		},
		Status: &ori.VolumeStatus{
			State:  state,
			Access: access,
		},
	}, nil
}
func (s *Server) createOriVolumeAccess(ctx context.Context, log logr.Logger, image *Image) (*ori.VolumeAccess, error) {
	user, key, err := s.provisioner.FetchAuth(ctx, image)
	if err != nil {
		return nil, fmt.Errorf("unable to fetch volume credentials: %w", err)
	}
	log.V(2).Info("Fetched volume access credentials", "userId", user)

	return &ori.VolumeAccess{
		Driver: "ceph",
		Handle: image.Wwn,
		Attributes: map[string]string{
			MonitorsKey: s.provisioner.Monitors(),
			ImageKey:    fmt.Sprintf("%s/%s", image.Pool, image.Name),
		},
		SecretData: map[string][]byte{
			UserIDKey:  []byte(user),
			UserKeyKey: []byte(key),
		},
	}, nil
}

func (s *Server) createOriResources(provisioned *Image) (*ori.VolumeResources, error) {
	return &ori.VolumeResources{
		StorageBytes: provisioned.Bytes,
	}, nil
}

func (s *Server) createOriObjectMetadata(image *Image) (*orimeta.ObjectMetadata, error) {
	return &orimeta.ObjectMetadata{
		Id:         image.Name,
		Generation: 0,
		CreatedAt:  image.Created.UnixNano(),
		DeletedAt:  0,
	}, nil
}

// generate WWN as hex string (16 chars)
func generateWWN() (string, error) {
	// prefix is optional, set to 1100AA for private identifier
	wwn := wwnPrefix

	// use UUIDv4, because this will generate good random string
	wwnUUID, err := uuid.NewRandom()
	if err != nil {
		return "", fmt.Errorf("failed to generate UUIDv4 for WWN: %w", err)
	}

	// append hex string without "-"
	wwn += strings.Replace(wwnUUID.String(), "-", "", -1)

	// WWN is 64Bit number as hex, so only the first 16 chars are returned
	return wwn[:16], nil
}
