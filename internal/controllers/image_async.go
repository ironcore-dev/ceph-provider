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

func (r *ImageReconciler) processNextFlattenWorkItem(ctx context.Context, log logr.Logger) bool {
	imageID, shutdown := r.flattenQueue.Get()
	if shutdown {
		return false
	}
	defer r.flattenQueue.Done(imageID)

	log = log.WithValues("imageId", imageID)
	ctx = logr.NewContext(ctx, log)

	err := r.processFlattenOperation(ctx, imageID)
	if err != nil {
		log.Error(err, "flatten operation failed")
		r.flattenQueue.AddRateLimited(imageID)
		return true
	}

	r.flattenQueue.Forget(imageID)
	return true
}

// processFlattenOperation flattens at most one child referencing the image and re-enqueues
// until no children remain. When flattening completes, it re-triggers image reconciliation
// so deletion can proceed (snapshot removal, reparenting, RemoveImage, finalizer removal).
func (r *ImageReconciler) processFlattenOperation(ctx context.Context, imageID string) error {
	log := logr.FromContextOrDiscard(ctx)

	image, err := r.images.Get(ctx, imageID)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("failed to get image: %w", err)
		}
		return nil
	}
	// Only do async flattening during deletion while finalizer is present.
	if image.DeletedAt == nil {
		return nil
	}
	// Only flatten when deletion is in the flattening-children phase.
	if image.Status.State != providerapi.ImageStateFlatteningChildren {
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
			// Parent image already gone; re-trigger reconcile to let finalizers clear.
			r.queue.Add(imageID)
			return nil
		}
		return fmt.Errorf("failed to open image: %w", err)
	}
	defer closeImage(log, img)

	pools, childImgs, err := img.ListChildren()
	if err != nil {
		return fmt.Errorf("failed to list children: %w", err)
	}

	if len(childImgs) == 0 {
		// Flattening complete; let the normal delete flow proceed.
		log.V(2).Info("No children remain; resuming image deletion")
		r.queue.Add(imageID)
		return nil
	}

	// Flatten one child per run for fairness and bounded work per item.
	log.V(2).Info("Flattening child image", "child", childImgs[0], "remaining", len(childImgs)-1)
	if err := flattenImage(log, r.conn, pools[0], childImgs[0]); err != nil {
		return fmt.Errorf("failed to flatten child %s: %w", childImgs[0], err)
	}

	// Re-enqueue to continue flattening the remaining children.
	r.flattenQueue.Add(imageID)
	return nil
}
