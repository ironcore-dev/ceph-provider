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
	"github.com/go-logr/logr"
	providerapi "github.com/ironcore-dev/ceph-provider/api"
	"github.com/ironcore-dev/ceph-provider/internal/rater"
	"github.com/ironcore-dev/ceph-provider/internal/utils"
	"github.com/ironcore-dev/ironcore-image/oci/image"
	"github.com/ironcore-dev/provider-utils/eventutils/event"
	"github.com/ironcore-dev/provider-utils/storeutils/store"
	"k8s.io/client-go/util/workqueue"
)

type SnapshoVolumelerOptions struct {
	Pool                string
	PopulatorBufferSize int64
	WorkerSize          int
}

func NewVolumeReconciler(
	log logr.Logger,
	conn *rados.Conn,
	registry image.Source,
	snapshotStore store.Store[*providerapi.Snapshot],
	imageStore store.Store[*providerapi.Image],
	volumeStore store.Store[*providerapi.Volume],
	events event.Source[*providerapi.Snapshot],
	opts SnapshoVolumelerOptions,
) (*VolumeReconciler, error) {
	if conn == nil {
		return nil, fmt.Errorf("must specify conn")
	}

	if registry == nil {
		return nil, fmt.Errorf("must specify registry")
	}

	if snapshotStore == nil {
		return nil, fmt.Errorf("must specify store")
	}

	if imageStore == nil {
		return nil, fmt.Errorf("must specify image store")
	}

	if volumeStore == nil {
		return nil, fmt.Errorf("must specify volume store")
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

	return &VolumeReconciler{
		log:                 log,
		conn:                conn,
		registry:            registry,
		queue:               workqueue.NewTypedRateLimitingQueue[string](workqueue.DefaultTypedControllerRateLimiter[string]()),
		snapshotStore:       snapshotStore,
		imageStore:          imageStore,
		volumeStore:         volumeStore,
		events:              events,
		pool:                opts.Pool,
		populatorBufferSize: opts.PopulatorBufferSize,
		workerSize:          opts.WorkerSize,
	}, nil
}

type VolumeReconciler struct {
	log  logr.Logger
	conn *rados.Conn

	registry image.Source
	queue    workqueue.TypedRateLimitingInterface[string]

	snapshotStore store.Store[*providerapi.Snapshot]
	imageStore    store.Store[*providerapi.Image]
	volumeStore   store.Store[*providerapi.Volume]

	events event.Source[*providerapi.Snapshot]

	pool                string
	populatorBufferSize int64

	workerSize int
}

func (r *VolumeReconciler) Start(ctx context.Context) error {
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

func (r *VolumeReconciler) processNextWorkItem(ctx context.Context, log logr.Logger) bool {
	id, shutdown := r.queue.Get()
	if shutdown {
		return false
	}
	defer r.queue.Done(id)

	log = log.WithValues("volumeId", id)
	ctx = logr.NewContext(ctx, log)

	if err := r.reconcileVolume(ctx, id); err != nil {
		log.Error(err, "failed to reconcile volume")
		r.queue.AddRateLimited(id)
		return true
	}

	r.queue.Forget(id)
	return true
}

const (
	VolumeFinalizer = "volume"
)

func (r *VolumeReconciler) deleteVolume(ctx context.Context, log logr.Logger, ioCtx *rados.IOContext, volume *providerapi.Volume) error {
	if !slices.Contains(volume.Finalizers, VolumeFinalizer) {
		log.V(1).Info("volume has no finalizer: done")
		return nil
	}

	//TODO implement me

	volume.Finalizers = utils.DeleteSliceElement(volume.Finalizers, VolumeFinalizer)
	if _, err := r.volumeStore.Update(ctx, volume); store.IgnoreErrNotFound(err) != nil {
		return fmt.Errorf("failed to update volume metadata: %w", err)
	}
	log.V(2).Info("Removed volume finalizer")
	return nil
}

func (r *VolumeReconciler) reconcileVolume(ctx context.Context, id string) error {
	log := logr.FromContextOrDiscard(ctx)
	ioCtx, err := r.conn.OpenIOContext(r.pool)
	if err != nil {
		return fmt.Errorf("unable to get io context: %w", err)
	}
	defer ioCtx.Destroy()

	log.V(2).Info("Get volume from store")
	volume, err := r.volumeStore.Get(ctx, id)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("failed to fetch volume from store: %w", err)
		}
		return nil
	}

	if volume.DeletedAt != nil {
		if err := r.deleteVolume(ctx, log, ioCtx, volume); err != nil {
			return fmt.Errorf("failed to delete volume: %w", err)
		}
	}

	if volume.Status.State == providerapi.VolumeStateAvailable {
		log.V(1).Info("Snapshot already populated")
		return nil
	}

	if !slices.Contains(volume.Finalizers, VolumeFinalizer) {
		volume.Finalizers = append(volume.Finalizers, VolumeFinalizer)
		if _, err := r.volumeStore.Update(ctx, volume); err != nil {
			return fmt.Errorf("failed to set finalizers: %w", err)
		}
	}

	switch {
	case volume.Spec.Source.OSVolume == nil && volume.Spec.Source.SnapshotSource == nil:
		err = r.reconcileEmptyVolume(ctx, log, ioCtx, volume)
	case volume.Spec.Source.OSVolume != nil && volume.Spec.Source.SnapshotSource == nil:
		err = r.reconcileOSVolume(ctx, log, ioCtx, volume)
	case volume.Spec.Source.OSVolume == nil && volume.Spec.Source.SnapshotSource != nil:
		err = r.reconcileRestoredVolume(ctx, log, ioCtx, volume)
	default:
		err = fmt.Errorf("invalid volume specification")
	}
	if err != nil {
		//TODO
	}

	return nil
}

func (r *VolumeReconciler) populateImage(log logr.Logger, dst io.WriteCloser, src io.Reader) error {
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

func (r *VolumeReconciler) reconcileEmptyVolume(ctx context.Context, log logr.Logger, ctx2 *rados.IOContext, volume *providerapi.Volume) error {
	//Case 1: Empty Volume -> create img
	return nil
}

func (r *VolumeReconciler) reconcileOSVolume(ctx context.Context, log logr.Logger, ctx2 *rados.IOContext, volume *providerapi.Volume) error {
	//Case 2: OS Volume -> create img, dump os on img, snapshot, create img with snap ref
	// r.populateImage
	return nil
}

func (r *VolumeReconciler) reconcileRestoredVolume(ctx context.Context, log logr.Logger, ctx2 *rados.IOContext, volume *providerapi.Volume) error {
	//Case 3: Restore volume -> create img with snap ref
	return nil
}
