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
	"github.com/ironcore-dev/ceph-provider/internal/round"
	"github.com/ironcore-dev/provider-utils/storeutils/store"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

const maxPopulateRetries = 10

func (r *SnapshotReconciler) processNextFlattenWorkItem(ctx context.Context, log logr.Logger) bool {
	snapshotID, shutdown := r.flattenQueue.Get()
	if shutdown {
		return false
	}
	defer r.flattenQueue.Done(snapshotID)

	err := r.processFlattenOperation(ctx, snapshotID)
	if err != nil {
		log.Error(err, "flatten operation failed", "snapshotId", snapshotID)
		r.flattenQueue.AddRateLimited(snapshotID)
		return true
	}

	r.flattenQueue.Forget(snapshotID)
	return true
}

// processFlattenOperation processes one flatten operation
func (r *SnapshotReconciler) processFlattenOperation(ctx context.Context, snapshotID string) error {
	log := logr.FromContextOrDiscard(ctx).WithValues("snapshotId", snapshotID)

	snapshot, err := r.store.Get(ctx, snapshotID)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("failed to get snapshot: %w", err)
		}
		return nil
	}
	// Only process snapshots that are actually in "Flattening" (delete path).
	if snapshot.Status.State != providerapi.SnapshotStateFlattening || snapshot.DeletedAt == nil {
		return nil
	}

	ioCtx, err := r.conn.OpenIOContext(r.pool)
	if err != nil {
		return fmt.Errorf("failed to open IO context: %w", err)
	}
	defer ioCtx.Destroy()

	rbdID, snapshotIDName, err := getSnapshotSourceDetails(snapshot)
	if err != nil {
		return fmt.Errorf("failed to get snapshot source details: %w", err)
	}

	img, err := librbd.OpenImage(ioCtx, rbdID, snapshotIDName)
	if err != nil {
		if errors.Is(err, librbd.ErrNotFound) {
			snapshot.Status.State = providerapi.SnapshotStateFlattened
			if _, err := r.store.Update(ctx, snapshot); err != nil {
				return fmt.Errorf("failed to update snapshot state to Flattened: %w", err)
			}
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
		snapshot.Status.State = providerapi.SnapshotStateFlattened
		if _, err := r.store.Update(ctx, snapshot); err != nil {
			return fmt.Errorf("failed to update snapshot state to Flattened: %w", err)
		}
		return nil
	}

	log.V(2).Info("Flattening child image", "child", childImgs[0], "remaining", len(childImgs)-1)
	err = flattenImage(log, r.conn, pools[0], childImgs[0])
	if err != nil {
		return fmt.Errorf("failed to flatten child %s: %w", childImgs[0], err)
	}

	r.flattenQueue.Add(snapshotID)
	return nil
}

func (r *SnapshotReconciler) processNextPopulateWorkItem(ctx context.Context, log logr.Logger) bool {
	snapshotID, shutdown := r.populateQueue.Get()
	if shutdown {
		return false
	}
	defer r.populateQueue.Done(snapshotID)

	err := r.processPopulateOperation(ctx, snapshotID)
	if err != nil {
		log.Error(err, "populate operation failed", "snapshotId", snapshotID)
		if r.populateQueue.NumRequeues(snapshotID) >= maxPopulateRetries {
			log.Error(err, "populate operation reached max retries; marking snapshot as Failed", "snapshotId", snapshotID, "maxRetries", maxPopulateRetries)
			snapshot, getErr := r.store.Get(ctx, snapshotID)
			if getErr == nil {
				snapshot.Status.State = providerapi.SnapshotStateFailed
				if _, updateErr := r.store.Update(ctx, snapshot); updateErr != nil {
					log.Error(updateErr, "failed to persist snapshot Failed state after max retries", "snapshotId", snapshotID)
				}
			} else if !errors.Is(getErr, store.ErrNotFound) {
				log.Error(getErr, "failed to get snapshot to mark Failed after max retries", "snapshotId", snapshotID)
			}

			r.populateQueue.Forget(snapshotID)
			return true
		}

		r.populateQueue.AddRateLimited(snapshotID)
		return true
	}

	r.populateQueue.Forget(snapshotID)
	return true
}

// processPopulateOperation processes one populate operation
func (r *SnapshotReconciler) processPopulateOperation(ctx context.Context, snapshotID string) error {
	log := logr.FromContextOrDiscard(ctx).WithValues("snapshotId", snapshotID)

	snapshot, err := r.store.Get(ctx, snapshotID)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("failed to get snapshot: %w", err)
		}
		return nil
	}

	// Only process snapshots that are actually in "Populating".
	if snapshot.Status.State != providerapi.SnapshotStatePopulating {
		return nil
	}
	if snapshot.Source.IronCoreImage == "" {
		return nil
	}

	ioCtx, err := r.conn.OpenIOContext(r.pool)
	if err != nil {
		return fmt.Errorf("failed to open IO context: %w", err)
	}
	defer ioCtx.Destroy()

	// If the Ceph snapshot already exists, population completed previously (e.g. controller restart
	// or status update failure). Skip re-populating and just try to advance status to Ready.
	imageName := SnapshotIDToRBDID(snapshot.ID)
	exists, _, err := snapshotExistsAndProtected(log, ioCtx, imageName, ImageSnapshotVersion)
	if err != nil {
		return fmt.Errorf("failed to check snapshot existence: %w", err)
	}
	if exists {
		snapshot.Status.State = providerapi.SnapshotStateReady
		if _, err := r.store.Update(ctx, snapshot); err != nil {
			return fmt.Errorf("failed to update snapshot state to Ready: %w", err)
		}
		return nil
	}

	var platform *ocispec.Platform
	if snapshot.Labels != nil {
		if arch, found := snapshot.Labels[providerapi.MachineArchitectureLabel]; found {
			platform = toPlatform(&arch)
		}
	}

	rc, snapshotSize, digest, err := r.openIroncoreImageSource(ctx, snapshot.Source.IronCoreImage, platform)
	if err != nil {
		snapshot.Status.State = providerapi.SnapshotStateFailed
		if _, updateErr := r.store.Update(ctx, snapshot); updateErr != nil {
			log.Error(updateErr, "Failed to update snapshot after source resolution failure")
		}
		// Terminal error: don't retry.
		return nil
	}
	defer rc.Close()

	// Create the backing RBD image if needed.
	rbdImg, err := openImage(ioCtx, imageName)
	if err != nil {
		if !errors.Is(err, librbd.ErrNotFound) {
			return fmt.Errorf("failed to open RBD image: %w", err)
		}

		options := librbd.NewRbdImageOptions()
		defer options.Destroy()
		if err := options.SetString(librbd.RbdImageOptionDataPool, r.pool); err != nil {
			return fmt.Errorf("failed to set data pool: %w", err)
		}

		roundedSize := round.OffBytes(snapshotSize)
		if err := librbd.CreateImage(ioCtx, imageName, roundedSize, options); err != nil {
			return fmt.Errorf("failed to create os rbd image: %w", err)
		}
		snapshot.Status.Digest = digest
		snapshot.Status.Size = int64(roundedSize)
		if _, err := r.store.Update(ctx, snapshot); err != nil {
			return fmt.Errorf("failed to persist snapshot metadata after image create: %w", err)
		}
	} else {
		_ = rbdImg.Close()
	}

	if err := r.prepareSnapshotContent(log, ioCtx, imageName, rc); err != nil {
		return fmt.Errorf("populate failed: %w", err)
	}

	if err := createSnapshot(log, ioCtx, ImageSnapshotVersion, imageName); err != nil {
		// Treat "already exists" as success to keep this step idempotent across retries/restarts.
		exists, _, existsErr := snapshotExistsAndProtected(log, ioCtx, imageName, ImageSnapshotVersion)
		if existsErr != nil {
			log.Error(existsErr, "Failed to check snapshot existence after createSnapshot error")
		}
		if !exists {
			return fmt.Errorf("failed to create ironcore image snapshot after population: %w", err)
		}
	}

	snapshot.Status.State = providerapi.SnapshotStateReady
	if _, err := r.store.Update(ctx, snapshot); err != nil {
		return fmt.Errorf("failed to update snapshot: %w", err)
	}
	return nil
}
