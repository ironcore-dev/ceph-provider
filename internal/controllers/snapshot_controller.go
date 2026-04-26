// SPDX-FileCopyrightText: 2023 SAP SE or an SAP affiliate company and IronCore contributors
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
	providerapi "github.com/ironcore-dev/ceph-provider/api"
	"github.com/ironcore-dev/ceph-provider/internal/utils"
	"github.com/ironcore-dev/provider-utils/eventutils/event"
	"github.com/ironcore-dev/provider-utils/storeutils/store"
	"k8s.io/client-go/util/workqueue"
)

type SnapshotReconcilerOptions struct {
	Pool                string
	PopulatorBufferSize int64
	WorkerSize          int
}

func NewSnapshotReconciler(
	log logr.Logger,
	conn *rados.Conn,
	store store.Store[*providerapi.Snapshot],
	images store.Store[*providerapi.Image],
	events event.Source[*providerapi.Snapshot],
	opts SnapshotReconcilerOptions,
) (*SnapshotReconciler, error) {
	if conn == nil {
		return nil, fmt.Errorf("must specify conn")
	}

	if store == nil {
		return nil, fmt.Errorf("must specify store")
	}

	if images == nil {
		return nil, fmt.Errorf("must specify image store")
	}

	if events == nil {
		return nil, fmt.Errorf("must specify events")
	}

	if opts.Pool == "" {
		return nil, fmt.Errorf("must specify pool")
	}

	if opts.PopulatorBufferSize == 0 {
		opts.PopulatorBufferSize = 5 * 1024 * 1024
	}

	if opts.WorkerSize == 0 {
		opts.WorkerSize = 15
	}

	return &SnapshotReconciler{
		log:                 log,
		conn:                conn,
		queue:               workqueue.NewTypedRateLimitingQueue[string](workqueue.DefaultTypedControllerRateLimiter[string]()),
		store:               store,
		images:              images,
		events:              events,
		pool:                opts.Pool,
		populatorBufferSize: opts.PopulatorBufferSize,
		workerSize:          opts.WorkerSize,
	}, nil
}

type SnapshotReconciler struct {
	log   logr.Logger
	conn  *rados.Conn
	queue workqueue.TypedRateLimitingInterface[string]

	store  store.Store[*providerapi.Snapshot]
	images store.Store[*providerapi.Image]
	events event.Source[*providerapi.Snapshot]

	pool                string
	populatorBufferSize int64

	workerSize int
}

func (r *SnapshotReconciler) Start(ctx context.Context) error {
	log := r.log

	reg, err := r.events.AddHandler(event.HandlerFunc[*providerapi.Snapshot](func(event event.Event[*providerapi.Snapshot]) {
		r.queue.Add(event.Object.ID)
	}))
	if err != nil {
		return err
	}
	defer func() {
		_ = r.events.RemoveHandler(reg)
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
	SnapshotFinalizer = "snapshot"
)

func (r *SnapshotReconciler) reconcileSnapshot(ctx context.Context, id string) error {
	log := logr.FromContextOrDiscard(ctx)
	ioCtx, err := r.conn.OpenIOContext(r.pool)
	if err != nil {
		return fmt.Errorf("unable to get io context: %w", err)
	}
	defer ioCtx.Destroy()

	log.V(2).Info("Get snapshot from store")
	snapshot, err := r.store.Get(ctx, id)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("failed to fetch snapshot from store: %w", err)
		}
		return nil
	}

	if snapshot.DeletedAt != nil {
		// If flattening has finished, perform cleanup and remove the finalizer.
		if snapshot.Status.State == providerapi.SnapshotStateFlattened {
			if err := r.cleanupFlattenedSnapshot(ctx, log, ioCtx, snapshot); err != nil {
				return err
			}
			return nil
		}

		// Long-running delete work (flattening) is handled by SnapshotLongOpsReconciler.
		// Here we only transition state and return quickly.
		if snapshot.Status.State != providerapi.SnapshotStateFlattening {
			snapshot.Status.State = providerapi.SnapshotStateFlattening
			if _, err := r.store.Update(ctx, snapshot); err != nil {
				return fmt.Errorf("failed to update snapshot for flattening: %w", err)
			}
		}
		return nil
	}

	if !slices.Contains(snapshot.Finalizers, SnapshotFinalizer) {
		snapshot.Finalizers = append(snapshot.Finalizers, SnapshotFinalizer)
		if _, err := r.store.Update(ctx, snapshot); err != nil {
			return fmt.Errorf("failed to set finalizers: %w", err)
		}
	}

	rbdID, snapshotID, err := getSnapshotSourceDetails(snapshot)
	if err != nil {
		return fmt.Errorf("failed to get snapshot source details: %w", err)
	}

	log.V(2).Info("Check if rbd snapshot exists")
	isSnapshotExist, isSnapshotProtected, err := snapshotExistsAndProtected(log, ioCtx, rbdID, snapshotID)
	if err != nil {
		return fmt.Errorf("failed to check snapshot existence: %w", err)
	}

	if isSnapshotExist && !isSnapshotProtected {
		// Snapshot exists but not protected - just protect it
		if err := protectSnapshot(log, ioCtx, rbdID, snapshotID); err != nil {
			return fmt.Errorf("failed to protect snapshot: %w", err)
		}
	}

	// SnapshotStatePopulated is no longer actively used. It has been replaced by SnapshotStateReady.
	// This block will transition any snapshots that are in SnapshotStatePopulated to SnapshotStateReady.
	if snapshot.Status.State == providerapi.SnapshotStatePopulated {
		log.V(1).Info("Snapshot already populated")
		snapshot.Status.State = providerapi.SnapshotStateReady
		if _, err = r.store.Update(ctx, snapshot); err != nil {
			return fmt.Errorf("failed to update snapshot: %w", err)
		}
		return nil
	}

	if snapshot.Status.State == providerapi.SnapshotStateReady {
		log.V(1).Info("Snapshot is ready")
		return nil
	}

	// In-progress long operations are handled by SnapshotLongOpsReconciler.
	if snapshot.Status.State == providerapi.SnapshotStatePopulating || snapshot.Status.State == providerapi.SnapshotStateFlattening {
		return nil
	}

	if snapshot.Status.State == providerapi.SnapshotStateFailed {
		log.V(1).Info("Rbd snapshot does not exist, so snapshot in store is marked as failed")
		return nil
	}

	log.V(1).Info("Rbd snapshot does not exist, start reconciliation")
	switch {
	case snapshot.Source.IronCoreImage != "":
		// Ironcore-image-backed snapshots are populated asynchronously by SnapshotLongOpsReconciler.
		snapshot.Status.State = providerapi.SnapshotStatePopulating
	case snapshot.Source.VolumeImageID != "":
		err = r.reconcileVolumeImageSnapshot(ctx, log, ioCtx, snapshot)
	default:
		return fmt.Errorf("snapshot source not found")
	}
	if err != nil {
		snapshot.Status.State = providerapi.SnapshotStateFailed
		if _, updateErr := r.store.Update(ctx, snapshot); updateErr != nil {
			return errors.Join(err, fmt.Errorf("failed to update snapshot state: %w", updateErr))
		}
		return fmt.Errorf("failed to reconcile snapshot: %w", err)
	}

	// For ironcore-image-backed snapshots, persist Populating and let SnapshotLongOpsReconciler complete population.
	if snapshot.Status.State == providerapi.SnapshotStatePopulating {
		if _, err = r.store.Update(ctx, snapshot); err != nil {
			return fmt.Errorf("failed to persist populating snapshot state: %w", err)
		}
		return nil
	}

	return nil
}

func (r *SnapshotReconciler) cleanupFlattenedSnapshot(ctx context.Context, log logr.Logger, ioCtx *rados.IOContext, snapshot *providerapi.Snapshot) error {
	if !slices.Contains(snapshot.Finalizers, SnapshotFinalizer) {
		return nil
	}

	rbdID, snapName, err := getSnapshotSourceDetails(snapshot)
	if err != nil {
		return fmt.Errorf("failed to get snapshot source details: %w", err)
	}

	img, err := openImage(ioCtx, rbdID)
	if err != nil {
		if !errors.Is(err, librbd.ErrNotFound) {
			return fmt.Errorf("failed to open image: %w", err)
		}
	} else {
		defer closeImage(log, img)

		rbdSnapshot := img.GetSnapshot(snapName)
		if err := removeSnapshot(rbdSnapshot); err != nil && !errors.Is(err, librbd.ErrNotFound) {
			return fmt.Errorf("failed to remove snapshot: %w", err)
		}
	}

	// deletes os-image if not referenced by any volume
	if snapshot.Source.IronCoreImage != "" {
		if err := librbd.RemoveImage(ioCtx, rbdID); err != nil && !errors.Is(err, librbd.ErrNotFound) {
			return fmt.Errorf("failed to remove ironcore os-image: %w", err)
		}
	}

	// deletes parent rbd image of snapshot which is created during source volume deletion
	// and has no any other reference except snapshot.
	// cloneSnapshot created both the RBD and the store entry; remove RBD before the store to avoid a leaked image.
	if rbdID == ImageIDToRBDID(snapshot.ID) {
		if err := librbd.RemoveImage(ioCtx, rbdID); err != nil && !errors.Is(err, librbd.ErrNotFound) {
			return fmt.Errorf("failed to remove RBD image for snapshot clone: %w", err)
		}
		if err := r.images.Delete(ctx, snapshot.ID); store.IgnoreErrNotFound(err) != nil {
			return fmt.Errorf("failed to remove image store entry: %w", err)
		}
	}

	snapshot.Finalizers = utils.DeleteSliceElement(snapshot.Finalizers, SnapshotFinalizer)
	if _, err := r.store.Update(ctx, snapshot); store.IgnoreErrNotFound(err) != nil {
		return fmt.Errorf("failed to update snapshot metadata: %w", err)
	}

	return nil
}
func (r *SnapshotReconciler) reconcileVolumeImageSnapshot(ctx context.Context, log logr.Logger, ioCtx *rados.IOContext, snapshot *providerapi.Snapshot) error {
	img, err := r.images.Get(ctx, snapshot.Source.VolumeImageID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("source volume image not found: %w", err)
		}
		return fmt.Errorf("failed to fetch image from store: %w", err)
	}

	log.V(2).Info("Create volume image snapshot", "ImageID", img.ID)
	if err := createSnapshot(log, ioCtx, snapshot.ID, ImageIDToRBDID(img.ID)); err != nil {
		return fmt.Errorf("failed to create volume image snapshot: %w", err)
	}

	snapshot.Status.Size = int64(img.Status.Size)
	snapshot.Status.State = providerapi.SnapshotStateReady
	if _, err := r.store.Update(ctx, snapshot); err != nil {
		return fmt.Errorf("failed to persist snapshot after creating volume image snapshot: %w", err)
	}
	return nil
}
