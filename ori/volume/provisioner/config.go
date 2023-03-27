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

package provisioner

import "fmt"

const (
	ClientDefault                 = "client.volumes-ceph"
	ClientNamePrefixDefault       = "client."
	LimitMetadataPrefixDefault    = "conf_"
	OsImageSnapshotVersionDefault = "v1"
)

const (
	OmapNameVolumes          = "onmetal.csi.volumes"
	OmapNameVolumeAttributes = "onmetal.csi.volume"
	OmapNameOsImages         = "onmetal.csi.os-images"

	OmapImageAnnotationsKey    = "annotations"
	OmapImageLabelsKey         = "labels"
	OmapImageWwnKey            = "wwn"
	OmapImageClassKey          = "class"
	OmapImagePopulatedImageKey = "populatedImage"
	OmapImageGenerationKey     = "generation"
)

type CephConfig struct {
	Pool   string
	Client string

	//Limits
	LimitingEnabled        bool
	BurstFactor            int64
	BurstDurationInSeconds int64

	PopulatorBufferSize int64

	LimitMetadataPrefix string
	ClientNamePrefix    string

	OsImageSnapshotVersion string
}

func (c *CephConfig) Defaults() {
	if c.Client == "" {
		c.Client = ClientDefault
	}

	if c.ClientNamePrefix == "" {
		c.ClientNamePrefix = ClientNamePrefixDefault
	}

	if c.LimitMetadataPrefix == "" {
		c.LimitMetadataPrefix = LimitMetadataPrefixDefault
	}

	if c.OsImageSnapshotVersion == "" {
		c.OsImageSnapshotVersion = OsImageSnapshotVersionDefault
	}
}

func (c *CephConfig) OmapVolumeAttributesKey(volumeName string) string {
	return fmt.Sprintf("%s.%s", OmapNameVolumeAttributes, volumeName)
}
