// SPDX-FileCopyrightText: 2023 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"time"

	"github.com/ceph/go-ceph/rados"
	librbd "github.com/ceph/go-ceph/rbd"
	"github.com/go-logr/logr"
	providerapi "github.com/ironcore-dev/ceph-provider/api"
	"github.com/ironcore-dev/ceph-provider/internal/async"
	"github.com/ironcore-dev/ceph-provider/internal/rater"
	"github.com/ironcore-dev/ceph-provider/internal/utils"
	"github.com/ironcore-dev/provider-utils/eventutils/event"
	"github.com/ironcore-dev/provider-utils/storeutils/store"
	"k8s.io/client-go/util/workqueue"
)

type SnapshotReconcilerOptions struct {
	Pool                string
	PopulatorBufferSize int64
	WorkerSize          int
	AsyncRunner         *async.Runner
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

	if opts.AsyncRunner == nil {
		return nil, fmt.Errorf("must specify async runner")
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
		asyncRunner:         opts.AsyncRunner,
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

	asyncRunner *async.Runner
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

	// Requeue the affected snapshot whenever an async operation finishes so reconciliation
	// can advance the state machine (e.g. Populating -> Ready, Flattening -> Flattened).
	r.asyncRunner.AddListener(async.ListenerFuncs{
		HandleDoneFunc: func(evt async.DoneEvent) {
			snapshotID, ok := parseSnapshotAsyncKey(evt.Key)
			if !ok {
				return
			}
			if evt.Err != nil {
				r.queue.AddRateLimited(snapshotID)
				return
			}
			r.queue.Add(snapshotID)
		},
	})

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

// deleteSnapshot starts the async deletion of a snapshot. Flattening child images can be
// long-running, so instead of doing it inline it moves the snapshot into the Flattening
// state and submits the work to the async runner. Once flattening completes the runner
// notifies the reconciler, which advances the snapshot to Flattened and runs
// cleanupFlattenedSnapshot to remove the rbd snapshot/images and the finalizer.
func (r *SnapshotReconciler) deleteSnapshot(ctx context.Context, log logr.Logger, snapshot *providerapi.Snapshot) error {
	if !slices.Contains(snapshot.Finalizers, SnapshotFinalizer) {
		log.V(1).Info("snapshot has no finalizer: done")
		return nil
	}

	if snapshot.Status.State != providerapi.SnapshotStateFlattening {
		snapshot.Status.State = providerapi.SnapshotStateFlattening
		if _, err := r.store.Update(ctx, snapshot); err != nil {
			return fmt.Errorf("failed to update snapshot to Flattening state: %w", err)
		}
	}

	// Delegate flattening to the async runner. processFlattenOperation sets the snapshot
	// to Flattened when done; the done listener then requeues it for cleanup.
	r.submitFlatten(ctx, log, snapshot.ID)
	return nil
}

// cleanupFlattenedSnapshot performs the fast, terminal steps of snapshot deletion after the
// async flatten finished: it removes the rbd snapshot, deletes the backing os/clone image when
// the snapshot owns it, and finally clears the finalizer.
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
		defer func() {
			if img != nil {
				closeImage(log, img)
			}
		}()

		rbdSnapshot := img.GetSnapshot(snapName)
		if err := removeSnapshot(rbdSnapshot); err != nil && !errors.Is(err, librbd.ErrNotFound) {
			return fmt.Errorf("failed to remove snapshot: %w", err)
		}
	}

	// deletes os-image if not referenced by any volume
	if snapshot.Source.IronCoreImage != "" {
		log.V(2).Info("Remove ironcore os-image")
		if img != nil {
			closeImage(log, img)
			img = nil
		}
		if err := librbd.RemoveImage(ioCtx, rbdID); err != nil && !errors.Is(err, librbd.ErrNotFound) {
			return fmt.Errorf("failed to remove ironcore os-image: %w", err)
		}
		log.V(2).Info("Ironcore os-image removed")
	}

	// deletes parent rbd image of snapshot which is created during source volume deletion
	// and has no any other reference except snapshot.
	if rbdID == ImageIDToRBDID(snapshot.ID) {
		log.V(2).Info("Remove parent rbd image")
		if err := r.images.Delete(ctx, snapshot.ID); store.IgnoreErrNotFound(err) != nil {
			return fmt.Errorf("unable to remove parent rbd image: %w", err)
		}
		log.V(2).Info("Removed parent rbd image")
	}

	snapshot.Finalizers = utils.DeleteSliceElement(snapshot.Finalizers, SnapshotFinalizer)
	if _, err := r.store.Update(ctx, snapshot); store.IgnoreErrNotFound(err) != nil {
		return fmt.Errorf("failed to update snapshot metadata: %w", err)
	}
	log.V(2).Info("Removed snapshot finalizer")
	return nil
}

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
		// Phase 1: kick off (or continue) async child flattening until the snapshot is Flattened.
		if snapshot.Status.State != providerapi.SnapshotStateFlattened {
			if err := r.deleteSnapshot(ctx, log, snapshot); err != nil {
				return fmt.Errorf("failed to delete snapshot: %w", err)
			}
		}

		// Phase 2: flattening done; run the terminal cleanup (rbd snapshot/image + finalizer).
		if snapshot.Status.State == providerapi.SnapshotStateFlattened {
			if err := r.cleanupFlattenedSnapshot(ctx, log, ioCtx, snapshot); err != nil {
				return fmt.Errorf("failed to cleanup flattened snapshot: %w", err)
			}
			log.V(1).Info("Successfully deleted snapshot")
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

	if snapshot.Status.State == providerapi.SnapshotStateFailed {
		log.V(1).Info("Rbd snapshot does not exist, so snapshot in store is marked as failed")
		return nil
	}

	// Restart recovery: re-submit snapshots already parked in an async state so work resumes
	// after a controller restart (the runner de-duplicates if it is already in flight).
	if snapshot.Status.State == providerapi.SnapshotStateFlattening {
		r.submitFlatten(ctx, log, snapshot.ID)
		return nil
	}
	if snapshot.Status.State == providerapi.SnapshotStatePopulating {
		r.submitPopulate(ctx, log, snapshot.ID)
		return nil
	}

	log.V(1).Info("Rbd snapshot does not exist, start reconciliation")
	switch {
	case snapshot.Source.IronCoreImage != "":
		// IronCore image snapshots are populated asynchronously by the async runner.
		snapshot.Status.State = providerapi.SnapshotStatePopulating
		if _, err := r.store.Update(ctx, snapshot); err != nil {
			return fmt.Errorf("failed to persist Populating snapshot state: %w", err)
		}
		r.submitPopulate(ctx, log, snapshot.ID)
		return nil
	case snapshot.Source.VolumeImageID != "":
		if err := r.reconcileVolumeImageSnapshot(ctx, log, ioCtx, snapshot); err != nil {
			snapshot.Status.State = providerapi.SnapshotStateFailed
			if _, updateErr := r.store.Update(ctx, snapshot); updateErr != nil {
				return errors.Join(err, fmt.Errorf("failed to update snapshot state: %w", updateErr))
			}
			return fmt.Errorf("failed to reconcile snapshot: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("snapshot source not found")
	}
}

func (r *SnapshotReconciler) submitPopulate(ctx context.Context, log logr.Logger, snapshotID string) {
	err := r.asyncRunner.Submit(ctx, snapshotAsyncKey(snapshotID), func(opCtx context.Context) error {
		return r.processPopulateOperation(opCtx, snapshotID)
	})
	switch {
	case err == nil, errors.Is(err, async.ErrInProgress):
		return
	case errors.Is(err, async.ErrAtCapacity), errors.Is(err, async.ErrNotRunning):
		log.V(2).Info("Async runner unavailable for populate, requeueing", "reason", err.Error())
		r.queue.AddRateLimited(snapshotID)
	default:
		log.Error(err, "failed to submit populate operation, requeueing")
		r.queue.AddRateLimited(snapshotID)
	}
}

func (r *SnapshotReconciler) submitFlatten(ctx context.Context, log logr.Logger, snapshotID string) {
	err := r.asyncRunner.Submit(ctx, snapshotAsyncKey(snapshotID), func(opCtx context.Context) error {
		return r.processFlattenOperation(opCtx, snapshotID)
	})
	switch {
	case err == nil, errors.Is(err, async.ErrInProgress):
		return
	case errors.Is(err, async.ErrAtCapacity), errors.Is(err, async.ErrNotRunning):
		log.V(2).Info("Async runner unavailable for flatten, requeueing", "reason", err.Error())
		r.queue.AddRateLimited(snapshotID)
	default:
		log.Error(err, "failed to submit flatten operation, requeueing")
		r.queue.AddRateLimited(snapshotID)
	}
}
func (r *SnapshotReconciler) reconcileVolumeImageSnapshot(ctx context.Context, log logr.Logger, ioCtx *rados.IOContext, snapshot *providerapi.Snapshot) error {
	img, err := r.images.Get(ctx, snapshot.Source.VolumeImageID)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("failed to fetch image from store: %w", err)
		}
		return nil
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

func (r *SnapshotReconciler) prepareSnapshotContent(log logr.Logger, ioCtx *rados.IOContext, imageName string, rc io.ReadCloser) error {
	rbdImg, err := openImage(ioCtx, imageName)
	if err != nil {
		return err
	}
	defer closeImage(log, rbdImg)

	if err := r.populateImage(log, rbdImg, rc); err != nil {
		return fmt.Errorf("failed to populate os image: %w", err)
	}
	log.V(2).Info("Populated os image on rbd image")

	return nil
}

func (r *SnapshotReconciler) populateImage(log logr.Logger, dst io.WriteCloser, src io.Reader) error {
	throughputReader := rater.NewRater(src)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	done := make(chan struct{})

	go func() {
		for {
			select {
			case <-ticker.C:
				log.Info("Populating", "rate", throughputReader.String())
			case <-done:
				return
			}
		}
	}()
	defer func() { close(done) }()

	buffer := make([]byte, r.populatorBufferSize)
	_, err := io.CopyBuffer(dst, throughputReader, buffer)
	if err != nil {
		return fmt.Errorf("failed to populate image: %w", err)
	}
	log.Info("Successfully populated image")

	return nil
}
