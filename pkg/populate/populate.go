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
	"time"

	"github.com/go-logr/logr"
	onmetalimage "github.com/onmetal/onmetal-image"
	"github.com/onmetal/onmetal-image/oci/remote"
)

func Image(ctx context.Context, log logr.Logger, imageName string, destination io.Writer, bufferSize int64) error {

	reg, err := remote.DockerRegistry(nil)
	if err != nil {
		return fmt.Errorf("failed to initialize registry: %w", err)
	}

	img, err := reg.Resolve(ctx, imageName)
	if err != nil {
		return fmt.Errorf("failed to resolve image ref in registry: %w", err)
	}

	onmetalImage, err := onmetalimage.ResolveImage(ctx, img)
	if err != nil {
		return fmt.Errorf("failed to resolve onmetal image: %w", err)
	}

	src, err := onmetalImage.RootFS.Content(ctx)
	if err != nil {
		return fmt.Errorf("failed to get content reader for layer: %w", err)
	}
	defer src.Close()

	log.Info("Start to download image", "image buffer size", bufferSize)

	rater := NewRater(src)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	done := make(chan struct{})

	go func() {
		for {
			select {
			case <-ticker.C:
				log.Info("Populating", "rate", rater.String())
			case <-done:
				return
			}
		}
	}()
	defer func() { close(done) }()

	buffer := make([]byte, bufferSize)
	_, err = io.CopyBuffer(destination, rater, buffer)
	if err != nil {
		return fmt.Errorf("failed to copy rootfs to device (%s): %w", destination, err)
	}
	log.Info("Successfully populated device")

	return nil
}
