// Copyright 2022 OnMetal authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package image

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/containerd/containerd/remotes"
	onmetalimage "github.com/onmetal/onmetal-image"
	"github.com/onmetal/onmetal-image/oci/image"
	"github.com/onmetal/onmetal-image/oci/store"
	ocispecv1 "github.com/opencontainers/image-spec/specs-go/v1"
)

// TODO: cleanup unneeded code

type Image struct {
	Config    onmetalimage.Config
	RootFS    *FileLayer
	InitRAMFs *FileLayer
	Kernel    *FileLayer
}

type FileLayer struct {
	Descriptor ocispecv1.Descriptor
	Path       string
}

func Pull(ctx context.Context, source image.Source, store *store.Store, ref string) (*Image, error) {
	ctx = setupMediaTypeKeyPrefixes(ctx)
	img, err := image.Copy(ctx, store, source, ref)
	if err != nil {
		return nil, err
	}
	return resolveImage(ctx, store, img)
}

func readImageConfig(ctx context.Context, img image.Image) (*onmetalimage.Config, error) {
	configLayer, err := img.Config(ctx)
	if err != nil {
		return nil, fmt.Errorf("error getting config layer: %w", err)
	}

	rc, err := configLayer.Content(ctx)
	if err != nil {
		return nil, fmt.Errorf("error getting config content: %w", err)
	}
	defer func() { _ = rc.Close() }()

	config := &onmetalimage.Config{}
	if err := json.NewDecoder(rc).Decode(config); err != nil {
		return nil, fmt.Errorf("error decoding config: %w", err)
	}
	return config, nil
}

func resolveImage(ctx context.Context, s *store.Store, ociImg image.Image) (*Image, error) {
	config, err := readImageConfig(ctx, ociImg)
	if err != nil {
		return nil, err
	}

	layers, err := ociImg.Layers(ctx)
	if err != nil {
		return nil, fmt.Errorf("error getting image layers: %w", err)
	}

	var (
		localStore = s.Layout().Store()
		img        = Image{Config: *config}
	)
	for _, layer := range layers {
		switch layer.Descriptor().MediaType {
		case onmetalimage.InitRAMFSLayerMediaType:
			initRAMFSPath, err := localStore.BlobPath(layer.Descriptor().Digest)
			if err != nil {
				return nil, fmt.Errorf("error getting path to initramfs: %w", err)
			}
			img.InitRAMFs = &FileLayer{
				Descriptor: layer.Descriptor(),
				Path:       initRAMFSPath,
			}
		case onmetalimage.KernelLayerMediaType:
			kernelPath, err := localStore.BlobPath(layer.Descriptor().Digest)
			if err != nil {
				return nil, fmt.Errorf("error getting path to kernel: %w", err)
			}
			img.Kernel = &FileLayer{
				Descriptor: layer.Descriptor(),
				Path:       kernelPath,
			}
		case onmetalimage.RootFSLayerMediaType:
			rootFSPath, err := localStore.BlobPath(layer.Descriptor().Digest)
			if err != nil {
				return nil, fmt.Errorf("error getting path to rootfs: %w", err)
			}
			img.RootFS = &FileLayer{
				Descriptor: layer.Descriptor(),
				Path:       rootFSPath,
			}

		}
	}
	var missing []string
	if img.RootFS.Path == "" {
		missing = append(missing, "rootfs")
	}
	if img.Kernel.Path == "" {
		missing = append(missing, "kernel")
	}
	if img.InitRAMFs.Path == "" {
		missing = append(missing, "initramfs")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("incomplete image: components are missing: %v", missing)
	}

	return &img, nil
}

func setupMediaTypeKeyPrefixes(ctx context.Context) context.Context {
	mediaTypeToPrefix := map[string]string{
		onmetalimage.ConfigMediaType:         "config",
		onmetalimage.InitRAMFSLayerMediaType: "layer",
		onmetalimage.KernelLayerMediaType:    "layer",
		onmetalimage.RootFSLayerMediaType:    "layer",
	}
	for mediaType, prefix := range mediaTypeToPrefix {
		ctx = remotes.WithMediaTypeKeyPrefix(ctx, mediaType, prefix)
	}
	return ctx
}
