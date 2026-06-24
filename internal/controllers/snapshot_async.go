// SPDX-FileCopyrightText: 2023 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	librbd "github.com/ceph/go-ceph/rbd"
	"github.com/go-logr/logr"
	providerapi "github.com/ironcore-dev/ceph-provider/api"
	"github.com/ironcore-dev/ceph-provider/internal/round"
	ironcoreimage "github.com/ironcore-dev/ironcore-image"
	"github.com/ironcore-dev/provider-utils/storeutils/store"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

const (
	// maxPopulateRetries bounds how many times a single populate operation retries internally
	// before the snapshot is marked Failed. The retry lives inside the operation because the
	// reconcile loop returns (and Forgets the key) right after submitting the work.
	maxPopulateRetries = 10
	// populateRetryBaseDelay / populateRetryMaxDelay define the in-operation backoff bounds.
	populateRetryBaseDelay = 5 * time.Second
	populateRetryMaxDelay  = 2 * time.Minute
)

// processPopulateOperation is the async operation submitted to the runner for IronCore image
// snapshots. It retries the populate work with bounded backoff and, on exhaustion, marks the
// snapshot Failed. It returns nil for all terminal outcomes (Ready or Failed) so the done
// listener simply requeues for a final reconcile.
func (r *SnapshotReconciler) processPopulateOperation(ctx context.Context, snapshotID string) error {
	log := logr.FromContextOrDiscard(ctx)

	backoff := populateRetryBaseDelay
	var lastErr error
	for attempt := 1; attempt <= maxPopulateRetries; attempt++ {
		err := r.populateSnapshotOnce(ctx, snapshotID)
		if err == nil {
			return nil
		}
		lastErr = err
		log.Error(err, "populate attempt failed", "attempt", attempt, "maxRetries", maxPopulateRetries)
		if attempt == maxPopulateRetries {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < populateRetryMaxDelay {
			if backoff *= 2; backoff > populateRetryMaxDelay {
				backoff = populateRetryMaxDelay
			}
		}
	}

	// Retries exhausted: mark the snapshot Failed if it is still being populated.
	snapshot, err := r.store.Get(ctx, snapshotID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("failed to get snapshot to mark Failed after max retries: %w", err)
	}
	if snapshot.DeletedAt == nil && snapshot.Status.State == providerapi.SnapshotStatePopulating {
		snapshot.Status.State = providerapi.SnapshotStateFailed
		if _, err := r.store.Update(ctx, snapshot); err != nil {
			return fmt.Errorf("failed to persist Failed state after max retries: %w", err)
		}
	}
	log.Error(lastErr, "populate operation reached max retries; marked snapshot as Failed", "maxRetries", maxPopulateRetries)
	return nil
}

// populateSnapshotOnce performs a single populate attempt. It returns nil on success or when
// the work is no longer applicable (snapshot deleted / no longer Populating), and a non-nil
// error for transient failures that should be retried.
func (r *SnapshotReconciler) populateSnapshotOnce(ctx context.Context, snapshotID string) error {
	log := logr.FromContextOrDiscard(ctx)

	snapshot, err := r.store.Get(ctx, snapshotID)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("failed to get snapshot: %w", err)
		}
		return nil
	}

	// Only process snapshots that are actually in Populating with an IronCore image source.
	if snapshot.Status.State != providerapi.SnapshotStatePopulating || snapshot.Source.IronCoreImage == "" {
		return nil
	}

	ioCtx, err := r.conn.OpenIOContext(r.pool)
	if err != nil {
		return fmt.Errorf("failed to open IO context: %w", err)
	}
	defer ioCtx.Destroy()

	var platform *ocispec.Platform
	if snapshot.Labels != nil {
		if arch, found := snapshot.Labels[providerapi.MachineArchitectureLabel]; found {
			platform = toPlatform(&arch)
		}
	}

	// If the Ceph snapshot already exists, population completed previously (e.g. controller restart
	// or a status-update failure). Skip re-populating and just advance status to Ready.
	imageName := SnapshotIDToRBDID(snapshot.ID)
	exists, _, err := snapshotExistsAndProtected(log, ioCtx, imageName, ImageSnapshotVersion)
	if err != nil {
		return fmt.Errorf("failed to check snapshot existence: %w", err)
	}
	if exists {
		rbdImg, err := openImage(ioCtx, imageName)
		if err != nil {
			return fmt.Errorf("failed to open rbd image for snapshot metadata: %w", err)
		}
		defer closeImage(log, rbdImg)
		size, err := rbdImg.GetSize()
		if err != nil {
			return fmt.Errorf("failed to get rbd image size: %w", err)
		}
		var digest string
		if snapshot.Status.Digest == "" {
			digest, _, _, err = resolveIroncoreImageReference(ctx, snapshot.Source.IronCoreImage, platform)
			if err != nil {
				return fmt.Errorf("failed to resolve ironcore image digest: %w", err)
			}
		}
		snapshot, stillPopulating, err := r.syncSnapshotFromStore(ctx, snapshot.ID)
		if err != nil {
			return err
		}
		if !stillPopulating {
			return nil
		}
		snapshot.Status.Size = int64(size)
		if snapshot.Status.Digest == "" {
			snapshot.Status.Digest = digest
		}
		snapshot.Status.State = providerapi.SnapshotStateReady
		if _, err := r.store.Update(ctx, snapshot); err != nil {
			return fmt.Errorf("failed to update snapshot state to Ready: %w", err)
		}
		return nil
	}

	rc, rootFSSizeBytes, digest, err := openIroncoreImageSource(ctx, snapshot.Source.IronCoreImage, platform)
	if err != nil {
		return fmt.Errorf("failed to open ironcore image source: %w", err)
	}
	defer func() {
		if err := rc.Close(); err != nil {
			log.Error(err, "failed to close snapshot source")
		}
	}()

	snapshot, stillPopulating, err := r.syncSnapshotFromStore(ctx, snapshot.ID)
	if err != nil {
		return err
	}
	if !stillPopulating {
		return nil
	}

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

		roundedSize := round.OffBytes(rootFSSizeBytes)
		if err := librbd.CreateImage(ioCtx, imageName, roundedSize, options); err != nil {
			return fmt.Errorf("failed to create os rbd image: %w", err)
		}
		snapshot.Status.Digest = digest
		snapshot.Status.Size = int64(roundedSize)
		if _, err := r.store.Update(ctx, snapshot); err != nil {
			return fmt.Errorf("failed to persist snapshot metadata after image create: %w", err)
		}
	} else {
		size, err := rbdImg.GetSize()
		if err != nil {
			_ = rbdImg.Close()
			return fmt.Errorf("failed to get existing RBD image size: %w", err)
		}
		metadataChanged := false
		if snapshot.Status.Digest == "" {
			snapshot.Status.Digest = digest
			metadataChanged = true
		}
		if snapshot.Status.Size == 0 {
			snapshot.Status.Size = int64(size)
			metadataChanged = true
		}
		_ = rbdImg.Close()
		if metadataChanged {
			if _, err := r.store.Update(ctx, snapshot); err != nil {
				return fmt.Errorf("failed to persist snapshot metadata for existing image: %w", err)
			}
		}
	}

	if err := r.prepareSnapshotContent(log, ioCtx, imageName, rc); err != nil {
		return fmt.Errorf("failed to populate snapshot: %w", err)
	}

	if err := createSnapshot(log, ioCtx, ImageSnapshotVersion, imageName); err != nil {
		// Treat "already exists" as success to keep this step idempotent across retries/restarts.
		exists, _, existsErr := snapshotExistsAndProtected(log, ioCtx, imageName, ImageSnapshotVersion)
		if existsErr != nil {
			log.Error(existsErr, "failed to check snapshot existence after createSnapshot error")
		}
		if !exists {
			return fmt.Errorf("failed to create ironcore image snapshot after population: %w", err)
		}
	}

	snapshot, stillPopulating, err = r.syncSnapshotFromStore(ctx, snapshot.ID)
	if err != nil {
		return err
	}
	if !stillPopulating {
		return nil
	}

	snapshot.Status.State = providerapi.SnapshotStateReady
	if _, err := r.store.Update(ctx, snapshot); err != nil {
		return fmt.Errorf("failed to update snapshot: %w", err)
	}
	return nil
}

// syncSnapshotFromStore reloads the snapshot from the store between slow populate steps to
// avoid writing stale state. It reports stillPopulating=false when the snapshot is gone, is
// being deleted, has left the Populating state, or has no IronCore image source.
func (r *SnapshotReconciler) syncSnapshotFromStore(ctx context.Context, snapshotID string) (snap *providerapi.Snapshot, stillPopulating bool, err error) {
	s, err := r.store.Get(ctx, snapshotID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("failed to get snapshot: %w", err)
	}
	if s.DeletedAt != nil || s.Status.State != providerapi.SnapshotStatePopulating || s.Source.IronCoreImage == "" {
		return nil, false, nil
	}
	return s, true, nil
}

// processFlattenOperation is the async operation submitted to the runner during snapshot
// deletion. It flattens all child images of the snapshot and then moves the snapshot to the
// Flattened state so reconciliation can run the terminal cleanup.
func (r *SnapshotReconciler) processFlattenOperation(ctx context.Context, snapshotID string) error {
	log := logr.FromContextOrDiscard(ctx)

	snapshot, err := r.store.Get(ctx, snapshotID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("failed to get snapshot: %w", err)
	}
	// Only process snapshots actively being deleted in the Flattening phase.
	if snapshot.DeletedAt == nil || snapshot.Status.State != providerapi.SnapshotStateFlattening {
		return nil
	}

	ioCtx, err := r.conn.OpenIOContext(r.pool)
	if err != nil {
		return fmt.Errorf("failed to open IO context: %w", err)
	}
	defer ioCtx.Destroy()

	rbdID, snapName, err := getSnapshotSourceDetails(snapshot)
	if err != nil {
		return fmt.Errorf("failed to get snapshot source details: %w", err)
	}

	img, err := librbd.OpenImage(ioCtx, rbdID, snapName)
	if err != nil {
		if errors.Is(err, librbd.ErrNotFound) {
			return r.markSnapshotFlattened(ctx, snapshot)
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

	return r.markSnapshotFlattened(ctx, snapshot)
}

func (r *SnapshotReconciler) markSnapshotFlattened(ctx context.Context, snapshot *providerapi.Snapshot) error {
	snapshot.Status.State = providerapi.SnapshotStateFlattened
	if _, err := r.store.Update(ctx, snapshot); err != nil {
		return fmt.Errorf("failed to update snapshot to Flattened state: %w", err)
	}
	return nil
}

// resolveIroncoreImageReference resolves imageReference and returns its digest, root filesystem
// size, and a function to open a fresh stream of the root filesystem content.
func resolveIroncoreImageReference(ctx context.Context, imageReference string, platform *ocispec.Platform) (digest string, rootFSSizeBytes uint64, getSourceStream func() (io.ReadCloser, error), err error) {
	osImgSrc, err := createOsImageSource(platform)
	if err != nil {
		return "", 0, nil, fmt.Errorf("failed to create os image source: %w", err)
	}

	img, err := osImgSrc.Resolve(ctx, imageReference)
	if err != nil {
		return "", 0, nil, fmt.Errorf("failed to resolve image ref in os image source: %w", err)
	}

	ironcoreImage, err := ironcoreimage.ResolveImage(ctx, img)
	if err != nil {
		return "", 0, nil, fmt.Errorf("failed to resolve ironcore image: %w", err)
	}

	rootFS := ironcoreImage.RootFS
	if rootFS == nil {
		return "", 0, nil, fmt.Errorf("image has no root fs")
	}

	digest = img.Descriptor().Digest.String()
	rootFSSizeBytes = uint64(rootFS.Descriptor().Size)
	getSourceStream = func() (io.ReadCloser, error) {
		return rootFS.Content(ctx)
	}
	return digest, rootFSSizeBytes, getSourceStream, nil
}

// openIroncoreImageSource resolves imageReference and opens a stream of its root filesystem
// content, returning the stream, the root filesystem size and the image digest.
func openIroncoreImageSource(ctx context.Context, imageReference string, platform *ocispec.Platform) (io.ReadCloser, uint64, string, error) {
	digest, rootFSSizeBytes, getSourceStream, err := resolveIroncoreImageReference(ctx, imageReference, platform)
	if err != nil {
		return nil, 0, "", err
	}
	rc, err := getSourceStream()
	if err != nil {
		return nil, 0, "", fmt.Errorf("failed to get source stream: %w", err)
	}
	return rc, rootFSSizeBytes, digest, nil
}
