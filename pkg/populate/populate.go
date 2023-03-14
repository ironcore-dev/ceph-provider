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

package populate

import (
	"context"
	"fmt"
	"io"

	"github.com/go-logr/logr"
	onmetalimage "github.com/onmetal/onmetal-image"
	"github.com/onmetal/onmetal-image/oci/image"
	"github.com/onmetal/onmetal-image/oci/remote"
)

func Image(ctx context.Context, log logr.Logger, imageName string, destination io.Writer) error {

	reg, err := remote.DockerRegistry(nil)
	if err != nil {
		return fmt.Errorf("failed to initialize registry: %w", err)
	}

	img, err := reg.Resolve(ctx, imageName)
	if err != nil {
		return fmt.Errorf("failed to resolve image ref in registry: %w", err)
	}

	layers, err := img.Layers(ctx)
	if err != nil {
		return fmt.Errorf("failed to get layers for image: %w", err)
	}

	var rootFSLayer image.Layer
	for _, l := range layers {
		if l.Descriptor().MediaType == onmetalimage.RootFSLayerMediaType {
			rootFSLayer = l
			break
		}
	}
	if rootFSLayer == nil {
		return fmt.Errorf("failed to get rootFS layer for image: %w", err)
	}

	src, err := rootFSLayer.Content(ctx)
	if err != nil {
		return fmt.Errorf("failed to get content reader for layer: %w", err)
	}
	defer src.Close()

	_, err = io.Copy(destination, src)
	if err != nil {
		return fmt.Errorf("failed to copy rootfs to device (%s): %w", destination, err)
	}
	log.Info("Successfully populated device")

	return nil
}
