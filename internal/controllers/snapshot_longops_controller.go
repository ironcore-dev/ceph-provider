// SPDX-FileCopyrightText: 2023 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"
	"errors"
	"fmt"
	"io"
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

// SnapshotLongOpsReconciler is a dedicated controller that processes long-running snapshot
// operations. It watches Snapshot objects and only acts on in-progress states
// (Populating, Flattening).
type SnapshotLongOpsReconcilerOptions struct {
	Pool                string
	PopulatorBufferSize int64

	// PopulateWorkerSize is the number of concurrent workers processing snapshot populate operations.
	PopulateWorkerSize int

	// FlattenWorkerSize is the number of concurrent workers processing snapshot flatten operations.
	FlattenWorkerSize int
}

func NewSnapshotLongOpsReconciler(
	log logr.Logger,
	conn *rados.Conn,
	store store.Store[*providerapi.Snapshot],
	images store.Store[*providerapi.Image],
	events event.Source[*providerapi.Snapshot],
	opts SnapshotLongOpsReconcilerOptions,
) (*SnapshotLongOpsReconciler, error) {
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

	if opts.PopulateWorkerSize == 0 {
		opts.PopulateWorkerSize = 3
	}
	if opts.FlattenWorkerSize == 0 {
		opts.FlattenWorkerSize = 5
	}

	return &SnapshotLongOpsReconciler{
		log:                 log,
		conn:                conn,
		store:               store,
		images:              images,
		events:              events,
		pool:                opts.Pool,
		populatorBufferSize: opts.PopulatorBufferSize,

		populateQueue:   workqueue.NewTypedRateLimitingQueue[string](workqueue.DefaultTypedControllerRateLimiter[string]()),
		flattenQueue:    workqueue.NewTypedRateLimitingQueue[string](workqueue.DefaultTypedControllerRateLimiter[string]()),
		populateWorkers: opts.PopulateWorkerSize,
		flattenWorkers:  opts.FlattenWorkerSize,
	}, nil
}

type SnapshotLongOpsReconciler struct {
	log    logr.Logger
	conn   *rados.Conn
	store  store.Store[*providerapi.Snapshot]
	images store.Store[*providerapi.Image]
	events event.Source[*providerapi.Snapshot]

	pool                string
	populatorBufferSize int64

	populateQueue workqueue.TypedRateLimitingInterface[string]
	flattenQueue  workqueue.TypedRateLimitingInterface[string]

	populateWorkers int
	flattenWorkers  int
}

func (r *SnapshotLongOpsReconciler) Start(ctx context.Context) error {
	log := r.log

	reg, err := r.events.AddHandler(event.HandlerFunc[*providerapi.Snapshot](func(evt event.Event[*providerapi.Snapshot]) {
		s := evt.Object
		switch s.Status.State {
		case providerapi.SnapshotStatePopulating:
			r.populateQueue.Add(s.ID)
		case providerapi.SnapshotStateFlattening:
			r.flattenQueue.Add(s.ID)
		}
	}))
	if err != nil {
		return err
	}
	defer func() {
		_ = r.events.RemoveHandler(reg)
	}()

	go func() {
		<-ctx.Done()
		r.populateQueue.ShutDown()
		r.flattenQueue.ShutDown()
	}()

	var wg sync.WaitGroup

	for i := 0; i < r.populateWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.processPopulateQueue(ctx, log)
		}()
	}

	for i := 0; i < r.flattenWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.processFlattenQueue(ctx, log)
		}()
	}

	wg.Wait()
	return nil
}

func (r *SnapshotLongOpsReconciler) processPopulateQueue(ctx context.Context, log logr.Logger) {
	for {
		item, shutdown := r.populateQueue.Get()
		if shutdown {
			return
		}
		snapshotID := item

		err := r.processPopulateOperation(ctx, snapshotID)
		if err != nil {
			log.Error(err, "populate operation failed", "snapshotId", snapshotID)
			r.populateQueue.AddRateLimited(snapshotID)
		} else {
			r.populateQueue.Forget(snapshotID)
		}
		r.populateQueue.Done(snapshotID)
	}
}

func (r *SnapshotLongOpsReconciler) processFlattenQueue(ctx context.Context, log logr.Logger) {
	for {
		item, shutdown := r.flattenQueue.Get()
		if shutdown {
			return
		}
		snapshotID := item

		err := r.processFlattenOperation(ctx, snapshotID)
		if err != nil {
			log.Error(err, "flatten operation failed", "snapshotId", snapshotID)
			r.flattenQueue.AddRateLimited(snapshotID)
		} else {
			r.flattenQueue.Forget(snapshotID)
		}
		r.flattenQueue.Done(snapshotID)
	}
}

func (r *SnapshotLongOpsReconciler) processPopulateOperation(ctx context.Context, snapshotID string) error {
	log := logr.FromContextOrDiscard(ctx).WithValues("snapshotId", snapshotID)
	ctx = logr.NewContext(ctx, log)

	snapshot, err := r.store.Get(ctx, snapshotID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("failed to get snapshot: %w", err)
	}
	if snapshot.Status.State != providerapi.SnapshotStatePopulating {
		return nil
	}

	ioCtx, err := r.conn.OpenIOContext(r.pool)
	if err != nil {
		return fmt.Errorf("failed to open IO context: %w", err)
	}
	defer ioCtx.Destroy()

	// If the Ceph snapshot already exists, population likely completed previously but status update didn't.
	rbdImageID := SnapshotIDToRBDID(snapshot.ID)
	exists, _, err := snapshotExistsAndProtected(log, ioCtx, rbdImageID, ImageSnapshotVersion)
	if err == nil && exists {
		snapshot.Status.State = providerapi.SnapshotStateReady
		if _, err := r.store.Update(ctx, snapshot); err != nil {
			return fmt.Errorf("failed to update snapshot state to Ready: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to check snapshot existence: %w", err)
	}

	rc, err := r.getImageSourceForSnapshot(ctx, snapshot)
	if err != nil {
		return fmt.Errorf("failed to get image source: %w", err)
	}
	defer rc.Close()

	if populateErr := r.prepareSnapshotContent(log, ioCtx, rbdImageID, rc); populateErr != nil {
		snapshot.Status.State = providerapi.SnapshotStateFailed
		if _, updateErr := r.store.Update(ctx, snapshot); updateErr != nil {
			log.Error(updateErr, "failed to update snapshot state after population failure")
		}
		return fmt.Errorf("failed to populate image: %w", populateErr)
	}

	if err := createSnapshot(log, ioCtx, ImageSnapshotVersion, rbdImageID); err != nil {
		return fmt.Errorf("failed to create snapshot after population: %w", err)
	}

	snapshot.Status.State = providerapi.SnapshotStateReady
	if _, err := r.store.Update(ctx, snapshot); err != nil {
		return fmt.Errorf("failed to update snapshot: %w", err)
	}

	return nil
}

func (r *SnapshotLongOpsReconciler) processFlattenOperation(ctx context.Context, snapshotID string) error {
	log := logr.FromContextOrDiscard(ctx).WithValues("snapshotId", snapshotID)
	ctx = logr.NewContext(ctx, log)

	snapshot, err := r.store.Get(ctx, snapshotID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("failed to get snapshot: %w", err)
	}
	if snapshot.Status.State != providerapi.SnapshotStateFlattening {
		return nil
	}
	if snapshot.DeletedAt == nil {
		// Flattening is only meaningful for delete path.
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
			// Image already gone; remove finalizer to allow deletion.
			snapshot.Finalizers = utils.DeleteSliceElement(snapshot.Finalizers, SnapshotFinalizer)
			if _, err := r.store.Update(ctx, snapshot); store.IgnoreErrNotFound(err) != nil {
				return fmt.Errorf("failed to update snapshot metadata: %w", err)
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

	if len(childImgs) != 0 {
		// Flatten one child per run for fairness.
		log.V(2).Info("Flattening child image", "child", childImgs[0], "remaining", len(childImgs)-1)
		if err := flattenImage(log, r.conn, pools[0], childImgs[0]); err != nil {
			return fmt.Errorf("failed to flatten child %s: %w", childImgs[0], err)
		}
		r.flattenQueue.Add(snapshotID)
		return nil
	}

	// No children remaining: perform cleanup first, then remove the finalizer.
	rbdSnapshot := img.GetSnapshot(snapName)
	if err := removeSnapshot(rbdSnapshot); err != nil {
		return fmt.Errorf("failed to remove snapshot: %w", err)
	}

	// deletes os-image if not referenced by any volume
	if snapshot.Source.IronCoreImage != "" {
		if err := img.Close(); err != nil && !errors.Is(err, librbd.ErrImageNotOpen) {
			return fmt.Errorf("failed to close ironcore os-image: %w", err)
		}
		if err := librbd.RemoveImage(ioCtx, rbdID); err != nil && !errors.Is(err, librbd.ErrNotFound) {
			return fmt.Errorf("failed to remove ironcore os-image: %w", err)
		}
	}

	// deletes parent rbd image of snapshot which is created during source volume deletion
	// and has no any other reference except snapshot.
	// cloneSnapshot created both the RBD and the store entry; remove RBD before the store to avoid a leaked image.
	if rbdID == ImageIDToRBDID(snapshot.ID) {
		if err := img.Close(); err != nil && !errors.Is(err, librbd.ErrImageNotOpen) {
			return fmt.Errorf("failed to close RBD image before remove: %w", err)
		}
		if err := librbd.RemoveImage(ioCtx, rbdID); err != nil && !errors.Is(err, librbd.ErrNotFound) {
			return fmt.Errorf("failed to remove RBD image for snapshot clone: %w", err)
		}
		if err := r.images.Delete(ctx, snapshot.ID); store.IgnoreErrNotFound(err) != nil {
			return fmt.Errorf("failed to remove image store entry: %w", err)
		}
	}

	// Remove the finalizer last so failed cleanup is retried.
	snapshot.Finalizers = utils.DeleteSliceElement(snapshot.Finalizers, SnapshotFinalizer)
	if _, err := r.store.Update(ctx, snapshot); store.IgnoreErrNotFound(err) != nil {
		return fmt.Errorf("failed to update snapshot metadata: %w", err)
	}

	return nil
}

func (r *SnapshotLongOpsReconciler) prepareSnapshotContent(log logr.Logger, ioCtx *rados.IOContext, imageName string, rc io.ReadCloser) error {
	rbdImg, err := openImage(ioCtx, imageName)
	if err != nil {
		return err
	}
	defer closeImage(log, rbdImg)

	return r.populateImage(log, rbdImg, rc)
}

func (r *SnapshotLongOpsReconciler) populateImage(log logr.Logger, dst io.WriteCloser, src io.Reader) error {
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
	if _, err := io.CopyBuffer(dst, throughputReader, buffer); err != nil {
		return fmt.Errorf("failed to populate image: %w", err)
	}
	log.Info("Successfully populated image")
	return nil
}

// getImageSourceForSnapshot resolves the image content stream for a snapshot being populated.
// In the current model, this is only valid for IronCore-image-backed snapshots.
func (r *SnapshotLongOpsReconciler) getImageSourceForSnapshot(ctx context.Context, snapshot *providerapi.Snapshot) (io.ReadCloser, error) {
	var platform *ocispec.Platform
	if snapshot.Labels != nil {
		if arch, found := snapshot.Labels[providerapi.MachineArchitectureLabel]; found {
			platform = toPlatform(&arch)
		}
	}
	rc, _, _, err := openIroncoreImageSource(ctx, snapshot.Source.IronCoreImage, platform)
	if err != nil {
		return nil, fmt.Errorf("failed to open snapshot source: %w", err)
	}
	return rc, nil
}

func openIroncoreImageSource(ctx context.Context, imageReference string, platform *ocispec.Platform) (io.ReadCloser, uint64, string, error) {
	osImgSrc, err := createOsImageSource(platform)
	if err != nil {
		return nil, 0, "", fmt.Errorf("failed to create os image source: %w", err)
	}

	img, err := osImgSrc.Resolve(ctx, imageReference)
	if err != nil {
		return nil, 0, "", fmt.Errorf("failed to resolve image ref in os image source: %w", err)
	}

	ironcoreImg, err := ironcoreimage.ResolveImage(ctx, img)
	if err != nil {
		return nil, 0, "", fmt.Errorf("failed to resolve ironcore image: %w", err)
	}

	rootFS := ironcoreImg.RootFS
	if rootFS == nil {
		return nil, 0, "", fmt.Errorf("image has no root fs")
	}

	content, err := rootFS.Content(ctx)
	if err != nil {
		return nil, 0, "", fmt.Errorf("failed to get root fs content: %w", err)
	}

	return content, uint64(rootFS.Descriptor().Size), img.Descriptor().Digest.String(), nil
}
