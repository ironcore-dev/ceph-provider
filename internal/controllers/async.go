// SPDX-FileCopyrightText: 2023 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"
	"errors"
	"fmt"
	"io"

	librbd "github.com/ceph/go-ceph/rbd"
	"github.com/go-logr/logr"
	providerapi "github.com/ironcore-dev/ceph-provider/api"
	"github.com/ironcore-dev/ceph-provider/internal/utils"
	"github.com/ironcore-dev/provider-utils/storeutils/store"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// processFlattenQueue processes items from the flatten queue
func (r *SnapshotReconciler) processFlattenQueue(ctx context.Context) {
	for {
		item, shutdown := r.flattenQueue.Get()
		if shutdown {
			return
		}

		snapshotID := item

		select {
		case r.flattenSemaphore <- struct{}{}:
		case <-ctx.Done():
			r.flattenQueue.Done(item)
			return
		}

		go func(id string) {
			defer func() {
				<-r.flattenSemaphore // Release semaphore
				r.flattenQueue.Done(id)
			}()

			r.processFlattenOperation(ctx, id)
		}(snapshotID)
	}
}

// processFlattenOperation processes one flatten operation
func (r *SnapshotReconciler) processFlattenOperation(ctx context.Context, snapshotID string) {
	log := logr.FromContextOrDiscard(ctx).WithValues("snapshotId", snapshotID)

	snapshot, err := r.store.Get(ctx, snapshotID)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			log.Error(err, "Failed to get snapshot")
		}
		return
	}

	ioCtx, err := r.conn.OpenIOContext(r.pool)
	if err != nil {
		log.Error(err, "Failed to open IO context")
		r.flattenQueue.AddRateLimited(snapshotID)
		return
	}
	defer ioCtx.Destroy()

	rbdID, snapshotIDName, err := getSnapshotSourceDetails(snapshot)
	if err != nil {
		log.Error(err, "Failed to get snapshot source details")
		r.flattenQueue.AddRateLimited(snapshotID)
		return
	}

	img, err := librbd.OpenImage(ioCtx, rbdID, snapshotIDName)
	if err != nil {
		if !errors.Is(err, librbd.ErrNotFound) {
			log.Error(err, "Failed to open image")
			r.flattenQueue.AddRateLimited(snapshotID)
		}
		return
	}
	defer closeImage(log, img)

	pools, childImgs, err := img.ListChildren()
	if err != nil {
		log.Error(err, "Failed to list children")
		r.flattenQueue.AddRateLimited(snapshotID)
		return
	}

	if len(childImgs) == 0 {
		if err := img.Close(); err != nil {
			log.Error(err, "Failed to close image")
		}

		// Remove RBD snapshot
		cleanupIoCtx, err := r.conn.OpenIOContext(r.pool)
		if err != nil {
			log.Error(err, "Failed to open IO context for cleanup")
			r.flattenQueue.AddRateLimited(snapshotID)
			return
		}
		defer cleanupIoCtx.Destroy()

		cleanupImg, err := librbd.OpenImage(cleanupIoCtx, rbdID, snapshotIDName)
		if err != nil {
			if !errors.Is(err, librbd.ErrNotFound) {
				log.Error(err, "Failed to open image for cleanup")
				r.flattenQueue.AddRateLimited(snapshotID)
			}
		} else {
			defer closeImage(log, cleanupImg)

			rbdSnapshot := cleanupImg.GetSnapshot(snapshotIDName)
			if err := removeSnapshot(rbdSnapshot); err != nil {
				log.Error(err, "Failed to remove snapshot")
				r.flattenQueue.AddRateLimited(snapshotID)
				return
			}
			log.V(2).Info("Removed RBD snapshot")

			if snapshot.Source.IronCoreImage != "" {
				log.V(2).Info("Remove ironcore os-image")
				if err := cleanupImg.Close(); err != nil {
					log.Error(err, "Failed to close image")
				}

				if err := librbd.RemoveImage(cleanupIoCtx, rbdID); err != nil {
					log.Error(err, "Failed to remove ironcore os-image")
					r.flattenQueue.AddRateLimited(snapshotID)
					return
				}
				log.V(2).Info("Ironcore os-image removed")
			}
		}

		snapshot.Finalizers = utils.DeleteSliceElement(snapshot.Finalizers, SnapshotFinalizer)
		if _, err := r.store.Update(ctx, snapshot); err != nil {
			log.Error(err, "Failed to update snapshot")
			r.flattenQueue.AddRateLimited(snapshotID)
			return
		}
		log.V(2).Info("Removed snapshot finalizer")

		if rbdID == ImageIDToRBDID(snapshot.ID) {
			log.V(2).Info("Remove parent rbd image")
			if err := r.images.Delete(ctx, snapshot.ID); store.IgnoreErrNotFound(err) != nil {
				log.Error(err, "Failed to remove parent rbd image")
				return
			}
			log.V(2).Info("Removed parent rbd image")
		}

		return
	}

	err = flattenImage(log, r.conn, pools[0], childImgs[0])
	if err != nil {
		log.Error(err, "Failed to flatten child", "child", childImgs[0])
		r.flattenQueue.AddRateLimited(snapshotID)
		return
	}

	if _, err := r.store.Update(ctx, snapshot); err != nil {
		log.Error(err, "Failed to update snapshot")
	}

	r.flattenQueue.Add(snapshotID)
}

// processPopulateQueue processes items from the populate queue
func (r *SnapshotReconciler) processPopulateQueue(ctx context.Context) {
	for {
		item, shutdown := r.populateQueue.Get()
		if shutdown {
			return
		}

		snapshotID := item

		select {
		case r.populateSemaphore <- struct{}{}:
		case <-ctx.Done():
			r.populateQueue.Done(item)
			return
		}

		go func(id string) {
			defer func() {
				<-r.populateSemaphore
				r.populateQueue.Done(id)
			}()

			r.processPopulateOperation(ctx, id)
		}(snapshotID)
	}
}

// processPopulateOperation processes one populate operation
func (r *SnapshotReconciler) processPopulateOperation(ctx context.Context, snapshotID string) {
	log := logr.FromContextOrDiscard(ctx).WithValues("snapshotId", snapshotID)

	snapshot, err := r.store.Get(ctx, snapshotID)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			log.Error(err, "Failed to get snapshot")
		}
		return
	}

	// Only process snapshots that are actually in "Populating".
	if snapshot.Status.State != providerapi.SnapshotStatePopulating {
		return
	}

	ioCtx, err := r.conn.OpenIOContext(r.pool)
	if err != nil {
		log.Error(err, "Failed to open IO context")
		r.populateQueue.AddRateLimited(snapshotID)
		return
	}
	defer ioCtx.Destroy()

	rc, err := r.getImageSourceForSnapshot(ctx, snapshot)
	if err != nil {
		log.Error(err, "Failed to get image source")
		r.populateQueue.AddRateLimited(snapshotID)
		return
	}
	defer rc.Close()

	imageName := SnapshotIDToRBDID(snapshot.ID)
	err = r.prepareSnapshotContent(log, ioCtx, imageName, rc)
	if err != nil {
		log.Error(err, "Failed to populate image")
		snapshot.Status.State = providerapi.SnapshotStateFailed
		if _, err := r.store.Update(ctx, snapshot); err != nil {
			log.Error(err, "Failed to update snapshot after population failure")
		}
		return
	}

	snapshot.Status.State = providerapi.SnapshotStateReady
	if _, err := r.store.Update(ctx, snapshot); err != nil {
		log.Error(err, "Failed to update snapshot")
	}
}

// reconcileFlattening enqueues snapshot for flattening operation
func (r *SnapshotReconciler) reconcileFlattening(ctx context.Context, log logr.Logger, snapshot *providerapi.Snapshot) error {
	ioCtx, err := r.conn.OpenIOContext(r.pool)
	if err != nil {
		return fmt.Errorf("unable to get io context: %w", err)
	}
	defer ioCtx.Destroy()

	rbdID, snapshotID, err := getSnapshotSourceDetails(snapshot)
	if err != nil {
		return fmt.Errorf("failed to get snapshot source details: %w", err)
	}

	img, err := librbd.OpenImage(ioCtx, rbdID, snapshotID)
	if err != nil {
		if !errors.Is(err, librbd.ErrNotFound) {
			return fmt.Errorf("failed to open rbd image: %w", err)
		}
		snapshot.Finalizers = utils.DeleteSliceElement(snapshot.Finalizers, SnapshotFinalizer)
		if _, err := r.store.Update(ctx, snapshot); store.IgnoreErrNotFound(err) != nil {
			return fmt.Errorf("failed to update snapshot metadata: %w", err)
		}
		log.V(2).Info("Removed snapshot finalizer")
		return nil
	}
	defer closeImage(log, img)

	_, childImgs, err := img.ListChildren()
	if err != nil {
		return fmt.Errorf("unable to list children: %w", err)
	}

	if len(childImgs) == 0 {
		snapshot.Finalizers = utils.DeleteSliceElement(snapshot.Finalizers, SnapshotFinalizer)
		if _, err := r.store.Update(ctx, snapshot); store.IgnoreErrNotFound(err) != nil {
			return fmt.Errorf("failed to update snapshot metadata: %w", err)
		}
		log.V(2).Info("Removed snapshot finalizer")
		return nil
	}

	if snapshot.Status.State != providerapi.SnapshotStateFlattening {
		snapshot.Status.State = providerapi.SnapshotStateFlattening
		if _, err := r.store.Update(ctx, snapshot); err != nil {
			return fmt.Errorf("failed to update snapshot: %w", err)
		}
	}

	r.flattenQueue.Add(snapshot.ID)

	return nil
}

// reconcilePopulation enqueues snapshot for population operation
func (r *SnapshotReconciler) reconcilePopulation(ctx context.Context, snapshot *providerapi.Snapshot) error {
	if snapshot.Status.State == providerapi.SnapshotStatePopulating {
		r.populateQueue.Add(snapshot.ID)
		return nil
	}

	snapshot.Status.State = providerapi.SnapshotStatePopulating
	if _, err := r.store.Update(ctx, snapshot); err != nil {
		return fmt.Errorf("failed to update snapshot: %w", err)
	}

	r.populateQueue.Add(snapshot.ID)

	return nil
}

// getImageSourceForSnapshot gets the image source for a snapshot (helper for populate operation)
func (r *SnapshotReconciler) getImageSourceForSnapshot(ctx context.Context, snapshot *providerapi.Snapshot) (io.ReadCloser, error) {
	var platform *ocispec.Platform
	if snapshot.Labels != nil {
		if arch, found := snapshot.Labels[providerapi.MachineArchitectureLabel]; found {
			platform = toPlatform(&arch)
		}
	}

	rc, _, _, err := r.openIroncoreImageSource(ctx, snapshot.Source.IronCoreImage, platform)
	if err != nil {
		return nil, fmt.Errorf("failed to open snapshot source: %w", err)
	}

	return rc, nil
}
