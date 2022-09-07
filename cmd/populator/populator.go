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

package main

import (
	goflag "flag"
	"fmt"
	"io"
	"os"

	"github.com/go-logr/logr"
	onmetalimage "github.com/onmetal/onmetal-image"
	"github.com/onmetal/onmetal-image/oci/image"
	"github.com/onmetal/onmetal-image/oci/remote"
	flag "github.com/spf13/pflag"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

const (
	devicePath = "/dev/block"
)

func main() {
	var (
		image string
	)

	// Populate args
	flag.StringVar(&image, "image", "", "Image location which the PVC should be populated with")
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(goflag.CommandLine)
	flag.Parse()

	log := zap.New(zap.UseFlagOptions(&opts))
	if err := populate(log, image); err != nil {
		log.Error(err, "Image population failed")
		os.Exit(1)
	}
}

func populate(log logr.Logger, ref string) error {
	ctx := ctrl.SetupSignalHandler()
	log.Info("Starting image population")

	reg, err := remote.DockerRegistry(nil)
	if err != nil {
		return fmt.Errorf("failed to initialize registry: %w", err)
	}

	img, err := reg.Resolve(ctx, ref)
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

	dst, err := os.OpenFile(devicePath, os.O_RDWR, 0755)
	if err != nil {
		return fmt.Errorf("failed to open block device (%s): %w", devicePath, err)
	}
	defer dst.Close()

	_, err = io.Copy(dst, src)
	if err != nil {
		return fmt.Errorf("failed to copy rootfs to device (%s): %w", devicePath, err)
	}
	log.Info("Successfully populated device", "Device", devicePath)
	return nil
}
