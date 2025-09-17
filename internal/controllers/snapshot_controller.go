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
	"github.com/ironcore-dev/ceph-provider/internal/round"
	"github.com/ironcore-dev/ceph-provider/internal/utils"
	"github.com/ironcore-dev/ironcore-image/oci/image"
	"github.com/ironcore-dev/provider-utils/eventutils/event"
	"github.com/ironcore-dev/provider-utils/storeutils/store"
	"k8s.io/client-go/util/workqueue"
)

type SnapshotReconcilerOptions struct {
	Pool                string
	PopulatorBufferSize int64
}

func NewSnapshotReconciler(
	log logr.Logger,
	conn *rados.Conn,
	registry image.Source,
	store store.Store[*providerapi.Snapshot],
	images store.Store[*providerapi.Image],
	events event.Source[*providerapi.Snapshot],
	opts SnapshotReconcilerOptions,
) (*SnapshotReconciler, error) {
	if conn == nil {
		return nil, fmt.Errorf("must specify conn")
	}

	if registry == nil {
		return nil, fmt.Errorf("must specify registry")
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

	return &SnapshotReconciler{
		log:                 log,
		conn:                conn,
		registry:            registry,
		queue:               workqueue.NewTypedRateLimitingQueue[string](workqueue.DefaultTypedControllerRateLimiter[string]()),
		store:               store,
		images:              images,
		events:              events,
		pool:                opts.Pool,
		populatorBufferSize: opts.PopulatorBufferSize,
	}, nil
}

type SnapshotReconciler struct {
	log  logr.Logger
	conn *rados.Conn

	registry image.Source
	queue    workqueue.TypedRateLimitingInterface[string]

	store  store.Store[*providerapi.Snapshot]
	images store.Store[*providerapi.Image]
	events event.Source[*providerapi.Snapshot]

	pool                string
	populatorBufferSize int64
}

func (r *SnapshotReconciler) Start(ctx context.Context) error {
	log := r.log

	//todo make configurable
	workerSize := 15

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
	for i := 0; i < workerSize; i++ {
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

func (r *SnapshotReconciler) cleanupSnapshotResources(log logr.Logger, img *librbd.Image, snapshotName string) error {
	pools, imgs, err := img.ListChildren()
	if err != nil {
		return fmt.Errorf("unable to list children: %w", err)
	}
	log.V(2).Info("Snapshot references", "pools", len(pools), "rbd-images", len(imgs))

	if len(pools) != 0 || len(imgs) != 0 {
		return fmt.Errorf("unable to delete snapshot: still in use")
	}

	log.V(2).Info("Start to delete snapshot", "name", snapshotName)

	snap := img.GetSnapshot(snapshotName)
	isProtected, err := snap.IsProtected()
	if err != nil {
		return fmt.Errorf("unable to chek if snapshot %s is protected: %w", snapshotName, err)
	}

	if isProtected {
		if err := snap.Unprotect(); err != nil {
			return fmt.Errorf("unable to unprotect snapshot: %w", err)
		}
	}

	if err := snap.Remove(); err != nil {
		return fmt.Errorf("unable to remove snapshot snapshot: %w", err)
	}

	return nil
}

func (r *SnapshotReconciler) deleteSnapshot(ctx context.Context, log logr.Logger, ioCtx *rados.IOContext, snapshot *providerapi.Snapshot) error {
	if !slices.Contains(snapshot.Finalizers, SnapshotFinalizer) {
		log.V(1).Info("snapshot has no finalizer: done")
		return nil
	}

	img, err := librbd.OpenImage(ioCtx, ImageIDToRBDID(snapshot.Spec.Source.VolumeImageID), librbd.NoSnapshot)
	if err != nil {
		if !errors.Is(err, librbd.ErrNotFound) {
			return fmt.Errorf("failed to open image: %w", err)
		}

		snapshot.Finalizers = utils.DeleteSliceElement(snapshot.Finalizers, SnapshotFinalizer)
		if _, err := r.store.Update(ctx, snapshot); store.IgnoreErrNotFound(err) != nil {
			return fmt.Errorf("failed to update snapshot metadata: %w", err)
		}

		log.V(2).Info("Removed snapshot finalizer")
		return nil

	}

	if err := r.cleanupSnapshotResources(log, img, SnapshotIDToRBDID(snapshot.ID)); err != nil {
		if closeErr := img.Close(); closeErr != nil {
			return errors.Join(err, fmt.Errorf("unable to close snapshot: %w", closeErr))
		}
		return fmt.Errorf("failed to cleanup snapshot resources: %w", err)
	}

	if err := img.Close(); err != nil {
		return fmt.Errorf("unable to close snapshot: %w", err)
	}

	snapshot.Finalizers = utils.DeleteSliceElement(snapshot.Finalizers, SnapshotFinalizer)
	if _, err := r.store.Update(ctx, snapshot); store.IgnoreErrNotFound(err) != nil {
		return fmt.Errorf("failed to update snapshot metadata: %w", err)
	}

	log.V(2).Info("Deleted snapshot")
	return nil
}

func (r *SnapshotReconciler) reconcileSnapshot(ctx context.Context, id string) error {
	log := logr.FromContextOrDiscard(ctx)
	ioCtx, err := r.conn.OpenIOContext(r.pool)
	if err != nil {
		return fmt.Errorf("unable to get io context: %w", err)
	}
	defer ioCtx.Destroy()

	snapshot, err := r.store.Get(ctx, id)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("failed to fetch snapshot from store: %w", err)
		}

		return nil
	}

	if snapshot.DeletedAt != nil {
		if err := r.deleteSnapshot(ctx, log, ioCtx, snapshot); err != nil {
			return fmt.Errorf("failed to delete snapshot: %w", err)
		}
	}

	if snapshot.Status.State == providerapi.SnapshotStateReady {
		log.V(1).Info("Snapshot already populated")
		return nil
	}

	if !slices.Contains(snapshot.Finalizers, SnapshotFinalizer) {
		snapshot.Finalizers = append(snapshot.Finalizers, SnapshotFinalizer)
		if _, err := r.store.Update(ctx, snapshot); err != nil {
			return fmt.Errorf("failed to set finalizers: %w", err)
		}
	}

	image, err := r.images.Get(ctx, snapshot.Spec.Source.VolumeImageID)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("failed to fetch image from store: %w", err)
		}
		return err
	}
	rbdImg, err := librbd.OpenImage(ioCtx, ImageIDToRBDID(image.ID), librbd.NoSnapshot)
	if err != nil {
		return fmt.Errorf("failed to open rbd image: %w", err)
	}

	snapshotName := SnapshotIDToRBDID(snapshot.ID)
	imgSnap, err := rbdImg.CreateSnapshot(snapshotName)
	if err != nil {
		return fmt.Errorf("unable to create snapshot: %w", err)
	}

	if err := imgSnap.Protect(); err != nil {
		return fmt.Errorf("unable to protect snapshot: %w", err)
	}

	if err := rbdImg.SetSnapshot(snapshotName); err != nil {
		return fmt.Errorf("failed to set snapshot %s for image %s: %v", snapshotName, image.ID, err)
	}

	// Get image information (including size of the snapshot)
	stat, err := rbdImg.Stat()
	if err != nil {
		return fmt.Errorf("failed to get image stats for snapshot %s: %v", snapshotName, err)
	}

	snapshot.Status.Size = int64(round.OffBytes(stat.Size))
	snapshot.Status.State = providerapi.SnapshotStateReady

	if _, err = r.store.Update(ctx, snapshot); err != nil {
		return fmt.Errorf("failed to update snapshot metadate: %w", err)
	}

	return nil
}
