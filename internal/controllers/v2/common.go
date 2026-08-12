// SPDX-FileCopyrightText: 2026 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"
	"errors"
	"fmt"
	"slices"

	librbd "github.com/ceph/go-ceph/rbd"
	"github.com/go-logr/logr"
	"github.com/ironcore-dev/ceph-provider/internal/utils"
	"github.com/ironcore-dev/ironcore-image/oci/image"
	"github.com/ironcore-dev/ironcore-image/oci/remote"
	apiutils "github.com/ironcore-dev/provider-utils/apiutils/api"
	"github.com/ironcore-dev/provider-utils/storeutils/store"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"k8s.io/utils/ptr"
)

// removeFinalizer removes fin from obj's finalizers and persists it if present.
func removeFinalizer[T apiutils.Object](ctx context.Context, s store.Store[T], obj T, fin string) (T, error) {
	if !slices.Contains(obj.GetFinalizers(), fin) {
		return obj, nil
	}
	obj.SetFinalizers(utils.DeleteSliceElement(obj.GetFinalizers(), fin))
	updated, err := s.Update(ctx, obj)
	if err != nil {
		return obj, fmt.Errorf("failed to remove finalizer %q from %T: %w", fin, obj, err)
	}
	return updated, nil
}

// addFinalizer adds fin to obj's finalizers and persists it if not already present.
func addFinalizer[T apiutils.Object](ctx context.Context, s store.Store[T], obj T, fin string) (T, error) {
	if slices.Contains(obj.GetFinalizers(), fin) {
		return obj, nil
	}
	obj.SetFinalizers(append(obj.GetFinalizers(), fin))
	updated, err := s.Update(ctx, obj)
	if err != nil {
		return obj, fmt.Errorf("failed to set finalizer %q on %T: %w", fin, obj, err)
	}
	return updated, nil
}

// createOrGet creates obj in the store. If the object already exists it fetches and returns it instead.
func createOrGet[T apiutils.Object](ctx context.Context, log logr.Logger, s store.Store[T], obj T, createdMsg, existsMsg string) (T, error) {
	created, err := s.Create(ctx, obj)
	if err != nil {
		if !errors.Is(err, store.ErrAlreadyExists) {
			return obj, fmt.Errorf("failed to create %T: %w", obj, err)
		}
		log.V(2).Info(existsMsg, "id", obj.GetID())
		existing, err := s.Get(ctx, obj.GetID())
		if err != nil {
			return obj, fmt.Errorf("failed to get existing %T: %w", obj, err)
		}
		return existing, nil
	}
	log.V(1).Info(createdMsg, "id", created.GetID())
	return created, nil
}

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
