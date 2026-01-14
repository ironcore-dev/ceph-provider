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
	"github.com/ironcore-dev/ironcore-image/oci/image"
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

	if opts.WorkerSize == 0 {
		opts.WorkerSize = 15
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
		workerSize:          opts.WorkerSize,
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

func (r *SnapshotReconciler) deleteSnapshot(ctx context.Context, log logr.Logger, ioCtx *rados.IOContext, snapshot *providerapi.Snapshot) error {

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

	img, err := r.images.Get(ctx, snapshot.Spec.ImageRef)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("failed to fetch image from store: %w", err)
		}
		return nil
	}

	rbdImg, err := librbd.OpenImage(ioCtx, ImageIDToRBDID(img.ID), librbd.NoSnapshot)
	if err != nil {
		return fmt.Errorf("failed to open rbd image: %w", err)
	}
	defer closeImage(log, rbdImg)

	snapshotName := snapshot.ID
	imgSnap, err := rbdImg.CreateSnapshot(snapshotName)
	if err != nil {
		return fmt.Errorf("unable to create snapshot: %w", err)
	}

	//TODO implement protection
	switch snapshot.Spec.Protection {
	case providerapi.SnapshotProtectionNone:
		if err := imgSnap.Unprotect(); err != nil {
			return fmt.Errorf("unable to protect snapshot: %w", err)
		}
	case providerapi.SnapshotProtectionProtected:
		if err := imgSnap.Protect(); err != nil {
			return fmt.Errorf("unable to protect snapshot: %w", err)
		}
	}

	if err := rbdImg.SetSnapshot(snapshotName); err != nil {
		return fmt.Errorf("failed to set snapshot %s for image %s: %w", snapshotName, img.ID, err)
	}
	log.V(2).Info("Created volume image snapshot", "ImageID", rbdImg.GetName())

	// Get image size
	size, err := rbdImg.GetSize()
	if err != nil {
		return fmt.Errorf("failed to get snapshot image size %s: %w", snapshotName, err)
	}
	roundedSize := round.OffBytes(size)

	snapshot.Status.Size = int64(roundedSize)

	if err != nil {
		snapshot.Status.State = providerapi.SnapshotStateFailed
		if _, updateErr := r.store.Update(ctx, snapshot); updateErr != nil {
			return errors.Join(err, fmt.Errorf("failed to update snapshot state: %w", updateErr))
		}
		return fmt.Errorf("failed to reconcile snapshot: %w", err)
	}

	snapshot.Status.State = providerapi.SnapshotStateReady
	if _, err = r.store.Update(ctx, snapshot); err != nil {
		return fmt.Errorf("failed to update snapshot: %w", err)
	}

	return nil
}
