// SPDX-FileCopyrightText: 2023 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"fmt"

	librbd "github.com/ceph/go-ceph/rbd"
	"github.com/go-logr/logr"
	providerapi "github.com/ironcore-dev/ceph-provider/api"
	"github.com/ironcore-dev/ironcore-image/oci/image"
	"github.com/ironcore-dev/ironcore-image/oci/remote"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"k8s.io/utils/ptr"
)

const (
	ImageRBDIDPrefix    = "img_"
	SnapshotRBDIDPrefix = "snap_"

	ImageSnapshotVersion = "v1"
)

func ImageIDToRBDID(imageID string) string {
	return ImageRBDIDPrefix + imageID
}

func SnapshotIDToRBDID(snapshotID string) string {
	return SnapshotRBDIDPrefix + snapshotID
}

func getSnapshotSourceDetails(snapshot *providerapi.Snapshot) (parentName string, snapName string, err error) {
	switch {
	case snapshot.Source.IronCoreImage != "":
		parentName = SnapshotIDToRBDID(snapshot.ID)
		snapName = ImageSnapshotVersion
	case snapshot.Source.VolumeImageID != "":
		parentName = ImageIDToRBDID(snapshot.Source.VolumeImageID)
		snapName = snapshot.ID
	default:
		return "", "", fmt.Errorf("snapshot source is not present")
	}
	return parentName, snapName, nil
}

func closeImage(log logr.Logger, img *librbd.Image) {
	if closeErr := img.Close(); closeErr != nil {
		log.Error(closeErr, "failed to close image")
	}
}

func createOsImageSource(platform *ocispec.Platform) (image.Source, error) {
	if platform == nil {
		return remote.DockerRegistry(nil)
	}

	return remote.DockerRegistryWithPlatform(nil, platform)
}

func toPlatform(arch *string) *ocispec.Platform {
	if arch == nil {
		return nil
	}

	return &ocispec.Platform{
		Architecture: ptr.Deref(arch, ""),
		OS:           "linux",
	}
}
