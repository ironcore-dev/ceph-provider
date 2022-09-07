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

	if err := populate(zap.New(zap.UseFlagOptions(&opts)), image, devicePath); err != nil {
		os.Exit(1)
	}
}

func populate(log logr.Logger, ref string, devicePath string) error {
	ctx := ctrl.SetupSignalHandler()
	log.Info("Starting image population")

	reg, err := remote.DockerRegistry(nil)
	if err != nil {
		log.Error(err, "Failed to initialize registry")
		return err
	}

	img, err := reg.Resolve(ctx, ref)
	if err != nil {
		log.Error(err, "Failed to resolve image ref in registry")
		return err
	}

	layers, err := img.Layers(ctx)
	if err != nil {
		log.Error(err, "Failed to get layers for image")
		return err
	}

	var rootFSLayer image.Layer
	for _, l := range layers {
		if l.Descriptor().MediaType == onmetalimage.RootFSLayerMediaType {
			rootFSLayer = l
			break
		}
	}
	if rootFSLayer == nil {
		log.Error(err, "Failed to get rootFS layer for image")
		return err
	}

	src, err := rootFSLayer.Content(ctx)
	if err != nil {
		log.Error(err, "Failed to get content reader for layer")
		return err
	}
	defer src.Close()

	dst, err := os.OpenFile(devicePath, os.O_RDWR, 0755)
	if err != nil {
		log.Error(err, "Failed to open block device", "Device", devicePath)
		return err
	}
	defer dst.Close()

	_, err = io.Copy(dst, src)
	if err != nil {
		log.Error(err, "Failed to copy rootfs to device", "Device", devicePath)
		return err
	}
	log.Info("Successfully populated device", "Device", devicePath)
	return nil
}
