// SPDX-FileCopyrightText: 2023 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"
	"errors"
	"fmt"

	librbd "github.com/ceph/go-ceph/rbd"
	"github.com/go-logr/logr"
	providerapi "github.com/ironcore-dev/ceph-provider/api"
	"github.com/ironcore-dev/provider-utils/storeutils/store"
)

// processImageFlattenOperation is the async operation submitted to the runner during image
// deletion. It flattens all child images that still reference the image being deleted. When it
// finishes (or if the parent image is already gone) it returns nil; the done listener then
// requeues the image so deleteImageSnapshots runs again and deletion proceeds.
func (r *ImageReconciler) processImageFlattenOperation(ctx context.Context, imageID string) error {
	log := logr.FromContextOrDiscard(ctx)

	image, err := r.images.Get(ctx, imageID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("failed to get image: %w", err)
	}
	// Only flatten during deletion while in the flattening-children phase.
	if image.DeletedAt == nil || image.Status.State != providerapi.ImageStateFlatteningChildren {
		return nil
	}

	ioCtx, err := r.conn.OpenIOContext(r.pool)
	if err != nil {
		return fmt.Errorf("failed to open IO context: %w", err)
	}
	defer ioCtx.Destroy()

	img, err := openImage(ioCtx, ImageIDToRBDID(image.ID))
	if err != nil {
		if errors.Is(err, librbd.ErrNotFound) {
			// Parent image already gone; nothing to flatten. The done listener requeues the
			// image so the finalizer can be cleared.
			return nil
		}
		return fmt.Errorf("failed to open image: %w", err)
	}
	defer closeImage(log, img)

	pools, childImgs, err := img.ListChildren()
	if err != nil {
		return fmt.Errorf("failed to list children: %w", err)
	}

	for i, child := range childImgs {
		log.V(2).Info("Flattening child image", "child", child, "remaining", len(childImgs)-i)
		if err := flattenImage(log, r.conn, pools[i], child); err != nil {
			return fmt.Errorf("failed to flatten child %s: %w", child, err)
		}
	}

	return nil
}
