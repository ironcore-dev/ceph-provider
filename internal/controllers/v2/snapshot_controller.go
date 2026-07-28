// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/ceph/go-ceph/rados"
	librbd "github.com/ceph/go-ceph/rbd"
	"github.com/go-logr/logr"
	providerapi "github.com/ironcore-dev/ceph-provider/api/v2"
	"github.com/ironcore-dev/ceph-provider/internal/utils"
	"github.com/ironcore-dev/provider-utils/eventutils/event"
	"github.com/ironcore-dev/provider-utils/storeutils/store"
	"k8s.io/client-go/util/workqueue"
)

type SnapshotReconcilerOptions struct {
	Pool       string
	WorkerSize int
}

func NewSnapshotReconciler(
	log logr.Logger,
	conn *rados.Conn,
	snapshotStore store.Store[*providerapi.Snapshot],
	imageStore store.Store[*providerapi.Image],
	snapshotEvents event.Source[*providerapi.Snapshot],
	imageEvents event.Source[*providerapi.Image],
	opts SnapshotReconcilerOptions,
) (*SnapshotReconciler, error) {
	if conn == nil {
		return nil, fmt.Errorf("must specify conn")
	}

	if snapshotStore == nil {
		return nil, fmt.Errorf("must specify snapshot store")
	}

	if imageStore == nil {
		return nil, fmt.Errorf("must specify image store")
	}

	if snapshotEvents == nil {
		return nil, fmt.Errorf("must specify snapshot events")
	}

	if imageEvents == nil {
		return nil, fmt.Errorf("must specify image events")
	}

	if opts.Pool == "" {
		return nil, fmt.Errorf("must specify pool")
	}

	if opts.WorkerSize == 0 {
		opts.WorkerSize = 15
	}

	return &SnapshotReconciler{
		log:            log,
		conn:           conn,
		queue:          workqueue.NewTypedRateLimitingQueue[string](workqueue.DefaultTypedControllerRateLimiter[string]()),
		snapshotStore:  snapshotStore,
		imageStore:     imageStore,
		snapshotEvents: snapshotEvents,
		imageEvents:    imageEvents,
		pool:           opts.Pool,
		workerSize:     opts.WorkerSize,
	}, nil
}

type SnapshotReconciler struct {
	log  logr.Logger
	conn *rados.Conn

	queue workqueue.TypedRateLimitingInterface[string]

	snapshotStore store.Store[*providerapi.Snapshot]
	imageStore    store.Store[*providerapi.Image]

	snapshotEvents event.Source[*providerapi.Snapshot]
	imageEvents    event.Source[*providerapi.Image]

	pool string

	workerSize int
}

func (r *SnapshotReconciler) Start(ctx context.Context) error {
	log := r.log

	snapEventReg, err := r.snapshotEvents.AddHandler(event.HandlerFunc[*providerapi.Snapshot](func(evt event.Event[*providerapi.Snapshot]) {
		r.queue.Add(evt.Object.ID)
	}))
	if err != nil {
		return err
	}
	defer func() {
		_ = r.snapshotEvents.RemoveHandler(snapEventReg)
	}()

	imageEventReg, err := r.imageEvents.AddHandler(event.HandlerFunc[*providerapi.Image](func(evt event.Event[*providerapi.Image]) {
		if evt.Type != event.TypeUpdated && evt.Type != event.TypeDeleted {
			return
		}
		if evt.Object.Status.State != providerapi.ImageStateAvailable &&
			evt.Object.DeletedAt == nil {
			return
		}
		r.requeueSnapshotsForImage(ctx, evt.Object.ID)
	}))
	if err != nil {
		return err
	}
	defer func() {
		_ = r.imageEvents.RemoveHandler(imageEventReg)
	}()

	go func() {
		<-ctx.Done()
		r.queue.ShutDown()
	}()

	var wg sync.WaitGroup
	for i := 0; i < r.workerSize; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r.processNextWorkItem(ctx, log) {
			}
		}()
	}

	wg.Wait()
	return nil
}

func (r *SnapshotReconciler) requeueSnapshotsForImage(ctx context.Context, imageID string) {
	snapshots, err := r.snapshotStore.List(ctx, store.MatchingFields{providerapi.SnapshotSpecImageRefField: imageID})
	if err != nil {
		r.log.Error(err, "failed to list snapshots for image event requeue")
		return
	}
	for _, snap := range snapshots {
		r.queue.Add(snap.ID)
	}
}

func (r *SnapshotReconciler) processNextWorkItem(ctx context.Context, log logr.Logger) bool {
	id, shutdown := r.queue.Get()
	if shutdown {
		return false
	}
	defer r.queue.Done(id)

	log = log.WithValues("snapshotId", id)
	ctx = logr.NewContext(ctx, log)

	if err := r.reconcileSnapshot(ctx, id); err != nil {
		log.Error(err, "failed to reconcile snapshot")
		r.queue.AddRateLimited(id)
		return true
	}

	r.queue.Forget(id)
	return true
}

const (
	snapshotFinalizer = "snapshot"
)

func (r *SnapshotReconciler) reconcileSnapshot(ctx context.Context, id string) error {
	log := logr.FromContextOrDiscard(ctx)

	log.V(2).Info("Get snapshot from store")
	snapshot, err := r.snapshotStore.Get(ctx, id)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("failed to fetch snapshot from store: %w", err)
		}
		return nil
	}

	if snapshot.DeletedAt != nil {
		if err := r.deleteSnapshot(ctx, log, snapshot); err != nil {
			return fmt.Errorf("failed to delete snapshot: %w", err)
		}
		return nil
	}

	if !slices.Contains(snapshot.Finalizers, snapshotFinalizer) {
		snapshot.Finalizers = append(snapshot.Finalizers, snapshotFinalizer)
		if _, err := r.snapshotStore.Update(ctx, snapshot); err != nil {
			return fmt.Errorf("failed to set finalizers: %w", err)
		}
		return nil
	}

	if snapshot.Status.State == providerapi.SnapshotStateFailed {
		log.V(2).Info("Snapshot is in failed state, not reconciling")
		return nil
	}

	// Verify parent image exists and is available
	parentImage, err := r.imageStore.Get(ctx, snapshot.Spec.ImageRef)
	if err != nil {
		return fmt.Errorf("failed to get parent image %s: %w", snapshot.Spec.ImageRef, err)
	}

	if parentImage.Status.State != providerapi.ImageStateAvailable {
		log.V(1).Info("Parent image not yet available, waiting", "imageRef", snapshot.Spec.ImageRef)
		return nil
	}

	// Put finalizer on parent image to block image deletion until all snapshots have been deleted
	parentImageFinalizer := fmt.Sprintf("%s/%s", snapshotFinalizer, snapshot.ID)
	if !slices.Contains(parentImage.Finalizers, parentImageFinalizer) {
		parentImage.Finalizers = append(parentImage.Finalizers, parentImageFinalizer)
		if _, err := r.imageStore.Update(ctx, parentImage); err != nil {
			return fmt.Errorf("failed to set finalizers on parent image: %w", err)
		}
	}
	log.V(2).Info("Added finalizer to parent image", "parentImageId", parentImage.ID)

	ioCtx, err := r.conn.OpenIOContext(r.pool)
	if err != nil {
		return fmt.Errorf("unable to get rados io context: %w", err)
	}
	defer ioCtx.Destroy()

	rbdParentImage, err := librbd.OpenImage(ioCtx, parentImage.ID, snapshot.ID)
	if err != nil {
		if !errors.Is(err, librbd.ErrNotFound) {
			return fmt.Errorf("failed to open parent RBD image: %w", err)
		}
		log.V(2).Info("RBD snapshot not found, creating")
		if ok, err := r.createSnapshot(ctx, ioCtx, log, parentImage, snapshot); err != nil {
			return fmt.Errorf("failed to create snapshot: %w", err)
		} else if !ok {
			return nil
		}
		rbdParentImage, err = librbd.OpenImage(ioCtx, parentImage.ID, snapshot.ID)
		if err != nil {
			return fmt.Errorf("failed to open parent RBD image after snapshot creation: %w", err)
		}
	}
	defer closeImage(log, rbdParentImage)

	rbdSnapshot := rbdParentImage.GetSnapshot(snapshot.ID)
	isProtected, err := rbdSnapshot.IsProtected()
	if err != nil {
		return fmt.Errorf("failed to check snapshot protection: %w", err)
	}
	switch {
	case snapshot.Spec.Protection == providerapi.SnapshotProtectionNone && isProtected:
		if err := rbdSnapshot.Unprotect(); err != nil {
			return fmt.Errorf("failed to unprotect snapshot: %w", err)
		}
		log.V(1).Info("RBD snapshot unprotected")
	case snapshot.Spec.Protection == providerapi.SnapshotProtectionProtected && !isProtected:
		if err := rbdSnapshot.Protect(); err != nil {
			return fmt.Errorf("failed to protect snapshot: %w", err)
		}
		log.V(1).Info("RBD snapshot protected")
	}

	// Set the newly created snapshot as source of readable data for the image handle (relevant for size and digest)
	if err := rbdParentImage.SetSnapshot(snapshot.ID); err != nil {
		return fmt.Errorf("failed to set snapshot %s: %w", snapshot.ID, err)
	}
	snapSize, err := rbdParentImage.GetSize()
	if err != nil {
		return fmt.Errorf("failed to get snapshot size: %w", err)
	}

	snapshot.Status.State = providerapi.SnapshotStateReady
	snapshot.Status.Size = int64(snapSize)
	if _, err := r.snapshotStore.Update(ctx, snapshot); err != nil {
		return fmt.Errorf("failed to update snapshot status: %w", err)
	}

	return nil
}

func (r *SnapshotReconciler) createSnapshot(ctx context.Context, ioCtx *rados.IOContext, log logr.Logger, parentImage *providerapi.Image, snapshot *providerapi.Snapshot) (bool, error) {
	rbdParentImage, err := librbd.OpenImage(ioCtx, parentImage.ID, librbd.NoSnapshot)
	if err != nil {
		return false, fmt.Errorf("failed to open parent image: %w", err)
	}
	defer closeImage(log, rbdParentImage)

	if _, err := rbdParentImage.CreateSnapshot(snapshot.ID); err != nil {
		if !errors.Is(err, librbd.ErrExist) {
			// If there is an RBD error during the snapshot creation, do not treat it as an error and keep reconciling the snapshot until creation succeeds.
			// Instead, log the error, mark the snapshot as failed and stop the reconcile.
			log.V(1).Info("RBD snapshot creation failed", "rbdError", err.Error(), "parentImageId", parentImage.ID)
			snapshot.Status.State = providerapi.SnapshotStateFailed
			if _, err := r.snapshotStore.Update(ctx, snapshot); err != nil {
				return false, fmt.Errorf("failed to update snapshot status: %w", err)
			}
			log.V(1).Info("Set snapshot state to failed")
			return false, nil
		}
		log.V(2).Info("RBD snapshot already exists", "parentImageId", parentImage.ID)
	} else {
		log.V(1).Info("Created RBD snapshot", "parentImageId", parentImage.ID)
	}
	return true, nil
}
func (r *SnapshotReconciler) deleteSnapshot(ctx context.Context, log logr.Logger, snapshot *providerapi.Snapshot) error {
	if !slices.Contains(snapshot.Finalizers, snapshotFinalizer) {
		log.V(2).Info("Snapshot has no finalizer: done")
		return nil
	}

	if len(snapshot.Finalizers) > 1 {
		log.V(2).Info("Snapshot has too many finalizers: waiting")
		return nil
	}

	ioCtx, err := r.conn.OpenIOContext(r.pool)
	if err != nil {
		return fmt.Errorf("unable to get rados io context: %w", err)
	}
	defer ioCtx.Destroy()

	rbdImage, err := librbd.OpenImage(ioCtx, snapshot.Spec.ImageRef, snapshot.ID)
	if err != nil {
		if !errors.Is(err, librbd.ErrNotFound) {
			return fmt.Errorf("failed to open parent RBD image: %w", err)
		}
		log.V(2).Info("RBD snapshot already removed")
	} else {
		defer closeImage(log, rbdImage)

		rbdSnapshot := rbdImage.GetSnapshot(snapshot.ID)

		if protected, err := rbdSnapshot.IsProtected(); err != nil {
			return fmt.Errorf("failed to check if RBD snapshot is protected: %w", err)
		} else if protected {
			if err := rbdSnapshot.Unprotect(); err != nil {
				return fmt.Errorf("failed to unprotect RBD snapshot: %w", err)
			}
			log.V(2).Info("RBD snapshot unprotected before deletion")
		}

		if err := rbdSnapshot.Remove(); err != nil && !errors.Is(err, librbd.ErrNotFound) {
			return fmt.Errorf("failed to remove snapshot: %w", err)
		}
		log.V(1).Info("Snapshot removed")
	}

	parentImage, err := r.imageStore.Get(ctx, snapshot.Spec.ImageRef)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("failed to get parent image %s: %w", snapshot.Spec.ImageRef, err)
		}
		log.V(2).Info("parent image already removed")
	} else {
		// Remove finalizer from parent image to signal this snapshot is no longer blocking deletion
		parentImageFinalizer := fmt.Sprintf("%s/%s", snapshotFinalizer, snapshot.ID)
		if slices.Contains(parentImage.Finalizers, parentImageFinalizer) {
			parentImage.Finalizers = utils.DeleteSliceElement(parentImage.Finalizers, parentImageFinalizer)
			if _, err := r.imageStore.Update(ctx, parentImage); err != nil {
				return fmt.Errorf("failed to set finalizers on parent image: %w", err)
			}
			log.V(2).Info("Removed finalizer from parent image", "parentImageId", parentImage.ID)
		}
	}

	snapshot.Finalizers = utils.DeleteSliceElement(snapshot.Finalizers, snapshotFinalizer)
	if _, err := r.snapshotStore.Update(ctx, snapshot); store.IgnoreErrNotFound(err) != nil {
		return fmt.Errorf("failed to update snapshot metadata: %w", err)
	}
	log.V(2).Info("Removed snapshot finalizer")
	return nil
}
