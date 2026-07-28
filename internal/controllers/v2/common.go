// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"errors"

	librbd "github.com/ceph/go-ceph/rbd"
	"github.com/go-logr/logr"
	"github.com/ironcore-dev/ironcore-image/oci/image"
	"github.com/ironcore-dev/ironcore-image/oci/remote"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"k8s.io/utils/ptr"
)

func closeImage(log logr.Logger, img *librbd.Image) {
	if img == nil {
		log.Error(errors.New("image is nil"), "failed to close image")
		return
	}
	if err := img.Close(); err != nil && !errors.Is(err, librbd.ErrImageNotOpen) {
		log.Error(err, "failed to close image")
	}
}

func createImageSource(platform *ocispec.Platform) (image.Source, error) {
	if platform == nil {
		return remote.DockerRegistry()
	}

	return remote.DockerRegistryWithPlatform(platform)
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
