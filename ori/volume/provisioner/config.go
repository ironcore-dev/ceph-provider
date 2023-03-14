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
	OmapNameVolumesDefault  = "onmetal.csi.volume"
	OmapNameMappingsDefault = "onmetal.csi.mappings"
	ClientDefault           = "client.volumes-ceph"

	OmapImageIdKeyDefault        = "imageId"
	OmapImageNameKeyDefault      = "imageName"
	OmapVolumeNameKeyDefault     = "volumeName"
	OmapWwnKeyDefault            = "wwn"
	OmapClassKeyDefault          = "class"
	OmapPopulatedImageKeyDefault = "populatedImage"

	LimitMetadataPrefixDefault = "conf_"

	OsImageSnapshotVersionDefault = "v1"
)

type CephConfig struct {
	Pool   string
	Client string

	//Limits
	BurstFactor            int64
	BurstDurationInSeconds int64

	OmapNameVolumes  string
	OmapNameMappings string

	OmapImageIdKey        string
	OmapImageNameKey      string
	OmapVolumeNameKey     string
	OmapWwnKey            string
	OmapClassKey          string
	OmapPopulatedImageKey string

	LimitMetadataPrefix string

	OsImageSnapshotVersion string
}

func (c *CephConfig) Defaults() {
	if c.OmapNameVolumes == "" {
		c.OmapNameVolumes = OmapNameVolumesDefault
	}
	if c.OmapNameMappings == "" {
		c.OmapNameMappings = OmapNameMappingsDefault
	}
	if c.Client == "" {
		c.Client = ClientDefault
	}

	if c.OmapImageIdKey == "" {
		c.OmapImageIdKey = OmapImageIdKeyDefault
	}

	if c.OmapImageNameKey == "" {
		c.OmapImageNameKey = OmapImageNameKeyDefault
	}

	if c.OmapVolumeNameKey == "" {
		c.OmapVolumeNameKey = OmapVolumeNameKeyDefault
	}

	if c.OmapWwnKey == "" {
		c.OmapWwnKey = OmapWwnKeyDefault
	}

	if c.OmapClassKey == "" {
		c.OmapClassKey = OmapClassKeyDefault
	}

	if c.OmapPopulatedImageKey == "" {
		c.OmapPopulatedImageKey = OmapPopulatedImageKeyDefault
	}

	if c.LimitMetadataPrefix == "" {
		c.LimitMetadataPrefix = LimitMetadataPrefixDefault
	}

	if c.OsImageSnapshotVersion == "" {
		c.OsImageSnapshotVersion = OsImageSnapshotVersionDefault
	}
}

func (c *CephConfig) OmapVolumeAttributesKey(volumeName string) string {
	return fmt.Sprintf("%s.%s", c.OmapNameVolumes, volumeName)
}
