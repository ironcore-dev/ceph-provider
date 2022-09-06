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
	"github.com/onmetal/cephlet/pkg/image"
	"github.com/onmetal/onmetal-image/oci/remote"
	"github.com/onmetal/onmetal-image/oci/store"
	flag "github.com/spf13/pflag"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

const (
	devicePath = "/dev/block"
)

func main() {
	var (
		image     string
		storePath string
	)

	// Populate args
	flag.StringVar(&image, "image", "", "Image location which the PVC should be populated with")
	flag.StringVar(&storePath, "storePath", "/tmp", "Location of the local image store")
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(goflag.CommandLine)
	flag.Parse()

	populate(zap.New(zap.UseFlagOptions(&opts)), storePath, image, devicePath)
}

func populate(log logr.Logger, storePath string, ref string, devicePath string) {
	ctx := ctrl.SetupSignalHandler()
	log.Info("Starting image population")

	reg, err := remote.DockerRegistry(nil)
	if err != nil {
		log.Error(err, "Failed to initialize registry")
		os.Exit(1)
	}
	s, err := store.New(storePath)
	if err != nil {
		log.Error(err, "Failed to initialize image store")
		os.Exit(1)
	}
	img, err := image.Pull(ctx, reg, s, ref)
	if err != nil {
		log.Error(err, "Failed to pull image", "Image", ref)
		os.Exit(1)
	}

	src, err := os.Open(img.RootFS.Path)
	if err != nil {
		log.Error(err, "Failed to open rootfs file", "RootFS", img.RootFS.Path)
		os.Exit(1)
	}
	defer src.Close()

	dst, err := os.OpenFile(devicePath, os.O_RDWR, 0755)
	if err != nil {
		log.Error(err, "Failed to open block device", "Device", devicePath)
		os.Exit(1)
	}
	defer dst.Close()

	_, err = io.Copy(dst, src)
	if err != nil {
		log.Error(err, "Failed to copy rootfs to device", "RootFS", img.RootFS.Path, "Device", devicePath)
		os.Exit(1)
	}
	log.Info("Successfully populated device", "Device", devicePath)
}
