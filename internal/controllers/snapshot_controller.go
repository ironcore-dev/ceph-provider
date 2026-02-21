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
	"github.com/ironcore-dev/ceph-provider/internal/rater"
	"github.com/ironcore-dev/ceph-provider/internal/utils"
	ironcoreimage "github.com/ironcore-dev/ironcore-image"
	"github.com/ironcore-dev/provider-utils/eventutils/event"
	"github.com/ironcore-dev/provider-utils/storeutils/store"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"k8s.io/client-go/util/workqueue"
)

type SnapshotReconcilerOptions struct {
	Pool                string
	PopulatorBufferSize int64
	WorkerSize          int
	FlattenWorkerSize   int
	PopulateWorkerSize  int
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

	// Set defaults for async operations
	if opts.FlattenWorkerSize == 0 {
		opts.FlattenWorkerSize = 5
	}
	if opts.PopulateWorkerSize == 0 {
		opts.PopulateWorkerSize = 3
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
		flattenQueue:        workqueue.NewTypedRateLimitingQueue[string](workqueue.DefaultTypedControllerRateLimiter[string]()),
		populateQueue:       workqueue.NewTypedRateLimitingQueue[string](workqueue.DefaultTypedControllerRateLimiter[string]()),
		flattenWorkers:      opts.FlattenWorkerSize,
		populateWorkers:     opts.PopulateWorkerSize,
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

	// Async operation queues
	flattenQueue  workqueue.TypedRateLimitingInterface[string]
	populateQueue workqueue.TypedRateLimitingInterface[string]

	// Configuration
	flattenWorkers  int
	populateWorkers int
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
		r.flattenQueue.ShutDown()
		r.populateQueue.ShutDown()
	}()

	var wg sync.WaitGroup
	// Start fast workers (existing)
	for i := 0; i < r.workerSize; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r.processNextWorkItem(ctx, log) {
			}
		}()
	}

	// Start flatten workers
	for i := 0; i < r.flattenWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r.processNextFlattenWorkItem(ctx, log) {
			}
		}()
	}

	// Start populate workers
	for i := 0; i < r.populateWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r.processNextPopulateWorkItem(ctx, log) {
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

func (r *SnapshotReconciler) deleteSnapshot(ctx context.Context, log logr.Logger, ioCtx *rados.IOContext, snapshot *providerapi.Snapshot) error {
	if !slices.Contains(snapshot.Finalizers, SnapshotFinalizer) {
		log.V(1).Info("snapshot has no finalizer: done")
		return nil
	}

	rbdID, snapshotID, err := getSnapshotSourceDetails(snapshot)
	if err != nil {
		return fmt.Errorf("failed to get snapshot source details: %w", err)
	}

	img, err := librbd.OpenImage(ioCtx, rbdID, snapshotID)
	if err != nil {
		if !errors.Is(err, librbd.ErrNotFound) {
			return fmt.Errorf("failed to open rbd image: %w", err)
		}
		// Parent image or snapshot is gone; treat flattening as complete and let cleanup handle the rest.
		snapshot.Status.State = providerapi.SnapshotStateFlattened
		if _, err := r.store.Update(ctx, snapshot); err != nil {
			return fmt.Errorf("failed to update snapshot state to Flattened: %w", err)
		}
		return nil
	}
	defer closeImage(log, img)

	_, childImgs, err := img.ListChildren()
	if err != nil {
		return fmt.Errorf("unable to list children: %w", err)
	}

	if snapshot.Status.State != providerapi.SnapshotStateFlattening {
		snapshot.Status.State = providerapi.SnapshotStateFlattening
		if _, err := r.store.Update(ctx, snapshot); err != nil {
			return fmt.Errorf("failed to update snapshot: %w", err)
		}
	}

	// Delegate flattening to the async flatten queue. The worker will set state to Flattened
	// when flattening completes (or no children remain)
	if len(childImgs) == 0 {
		r.flattenQueue.Add(snapshot.ID)
		return nil
	}

	r.flattenQueue.Add(snapshot.ID)
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
		if snapshot.Status.State != providerapi.SnapshotStateFlattened {
			if err := r.deleteSnapshot(ctx, log, ioCtx, snapshot); err != nil {
				return fmt.Errorf("failed to delete snapshot: %w", err)
			}
		}

		if snapshot.Status.State == providerapi.SnapshotStateFlattened {
			return r.cleanupFlattenedSnapshot(ctx, log, ioCtx, snapshot)
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

	if snapshot.Status.State == providerapi.SnapshotStateFlattening {
		r.flattenQueue.Add(snapshot.ID)
		return nil
	}
	if snapshot.Status.State == providerapi.SnapshotStatePopulating {
		r.populateQueue.Add(snapshot.ID)
		return nil
	}

	if !slices.Contains(snapshot.Finalizers, SnapshotFinalizer) {
		snapshot.Finalizers = append(snapshot.Finalizers, SnapshotFinalizer)
		if _, err := r.store.Update(ctx, snapshot); err != nil {
			return fmt.Errorf("failed to set finalizers: %w", err)
		}
	}

	switch {
	case snapshot.Source.IronCoreImage != "":
		// Ironcore-image snapshots are populated asynchronously by the populate workers.
		snapshot.Status.State = providerapi.SnapshotStatePopulating
		if _, err := r.store.Update(ctx, snapshot); err != nil {
			return fmt.Errorf("failed to persist populating snapshot state: %w", err)
		}
		r.populateQueue.Add(snapshot.ID)
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

func (r *SnapshotReconciler) openIroncoreImageSource(ctx context.Context, imageReference string, platform *ocispec.Platform) (io.ReadCloser, uint64, string, error) {
	osImgSrc, err := createOsImageSource(platform)
	if err != nil {
		return nil, 0, "", fmt.Errorf("failed to create os image source: %w", err)
	}

	img, err := osImgSrc.Resolve(ctx, imageReference)
	if err != nil {
		return nil, 0, "", fmt.Errorf("failed to resolve image ref in os image source: %w", err)
	}

	ironcoreImage, err := ironcoreimage.ResolveImage(ctx, img)
	if err != nil {
		return nil, 0, "", fmt.Errorf("failed to resolve ironcore image: %w", err)
	}

	rootFS := ironcoreImage.RootFS
	if rootFS == nil {
		return nil, 0, "", fmt.Errorf("image has no root fs")
	}

	content, err := rootFS.Content(ctx)
	if err != nil {
		return nil, 0, "", fmt.Errorf("failed to get root fs content: %w", err)
	}

	return content, uint64(rootFS.Descriptor().Size), img.Descriptor().Digest.String(), nil
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
