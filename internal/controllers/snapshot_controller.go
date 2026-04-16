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

	"github.com/ceph/go-ceph/rados"
	librbd "github.com/ceph/go-ceph/rbd"
	"github.com/go-logr/logr"
	providerapi "github.com/ironcore-dev/ceph-provider/api"
	"github.com/ironcore-dev/ceph-provider/internal/round"
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
		err = r.reconcileIroncoreImageSnapshot(ctx, log, ioCtx, snapshot)
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

	// For ironcore-image-backed snapshots, population is asynchronous. The long-ops controller
	// will transition Populating -> Ready. Persist the Populating state (plus digest/size set
	// by reconcileIroncoreImageSnapshot) so SnapshotLongOpsReconciler can observe and act on it.
	if snapshot.Status.State == providerapi.SnapshotStatePopulating {
		if _, err = r.store.Update(ctx, snapshot); err != nil {
			return fmt.Errorf("failed to persist populating snapshot state: %w", err)
		}
		return nil
	}

	snapshot.Status.State = providerapi.SnapshotStateReady
	if _, err = r.store.Update(ctx, snapshot); err != nil {
		return fmt.Errorf("failed to update snapshot: %w", err)
	}

	return nil
}
func (r *SnapshotReconciler) reconcileIroncoreImageSnapshot(ctx context.Context, log logr.Logger, ioCtx *rados.IOContext, snapshot *providerapi.Snapshot) error {
	var platform *ocispec.Platform

	if snapshot.Labels != nil {
		if arch, found := snapshot.Labels[providerapi.MachineArchitectureLabel]; found {
			log.V(2).Info("Snapshot architecture", "architecture", arch)
			platform = toPlatform(&arch)
		}
	}

	rc, snapshotSize, digest, err := r.openIroncoreImageSource(ctx, snapshot.Source.IronCoreImage, platform)
	if err != nil {
		return fmt.Errorf("failed to open snapshot source: %w", err)
	}
	defer func() {
		if err := rc.Close(); err != nil {
			log.Error(err, "failed to close snapshot source")
		}
	}()

	options := librbd.NewRbdImageOptions()
	defer options.Destroy()

	//TODO: different pool for OS images?
	if err := options.SetString(librbd.RbdImageOptionDataPool, r.pool); err != nil {
		return fmt.Errorf("failed to set data pool: %w", err)
	}
	log.V(2).Info("Configured pool", "pool", r.pool)

	rbdImageID := SnapshotIDToRBDID(snapshot.ID)
	roundedSize := round.OffBytes(snapshotSize)

	if err = librbd.CreateImage(ioCtx, rbdImageID, roundedSize, options); err != nil {
		return fmt.Errorf("failed to create os rbd image: %w", err)
	}
	log.V(2).Info("Created rbd image", "bytes", roundedSize)

	snapshot.Status.Digest = digest
	snapshot.Status.Size = int64(roundedSize)
	snapshot.Status.State = providerapi.SnapshotStatePopulating
	return nil
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
