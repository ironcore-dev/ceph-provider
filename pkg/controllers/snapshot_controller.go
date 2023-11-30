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
	"github.com/ironcore-dev/ceph-provider/pkg/api"
	"github.com/ironcore-dev/ceph-provider/pkg/event"
	"github.com/ironcore-dev/ceph-provider/pkg/round"
	"github.com/ironcore-dev/ceph-provider/pkg/store"
	"github.com/ironcore-dev/ceph-provider/pkg/utils"
	ironcoreimage "github.com/ironcore-dev/ironcore-image"
	"github.com/ironcore-dev/ironcore-image/oci/image"
	"golang.org/x/exp/slices"
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
	store store.Store[*api.Snapshot],
	events event.Source[*api.Snapshot],
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
		queue:               workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter()),
		store:               store,
		events:              events,
		pool:                opts.Pool,
		populatorBufferSize: opts.PopulatorBufferSize,
	}, nil
}

type SnapshotReconciler struct {
	log  logr.Logger
	conn *rados.Conn

	registry image.Source
	queue    workqueue.RateLimitingInterface

	store  store.Store[*api.Snapshot]
	events event.Source[*api.Snapshot]

	pool                string
	populatorBufferSize int64
}

func (r *SnapshotReconciler) openSnapshotSource(ctx context.Context, src api.SnapshotSource) (io.ReadCloser, uint64, string, error) {
	switch {
	case src.IronCoreImage != "":

		img, err := r.registry.Resolve(ctx, src.IronCoreImage)
		if err != nil {
			return nil, 0, "", fmt.Errorf("failed to resolve image ref in registry: %w", err)
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
	default:
		return nil, 0, "", fmt.Errorf("unrecognized image source %#v", src)
	}
}

func (r *SnapshotReconciler) Start(ctx context.Context) error {
	log := r.log

	//todo make configurable
	workerSize := 15

	reg, err := r.events.AddHandler(event.HandlerFunc[*api.Snapshot](func(event event.Event[*api.Snapshot]) {
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
	item, shutdown := r.queue.Get()
	if shutdown {
		return false
	}
	defer r.queue.Done(item)

	id := item.(string)
	log = log.WithValues("snapshotId", id)
	ctx = logr.NewContext(ctx, log)

	if err := r.reconcileSnapshot(ctx, id); err != nil {
		log.Error(err, "failed to reconcile snapshot")
		r.queue.AddRateLimited(item)
		return true
	}

	r.queue.Forget(item)
	return true
}

const (
	SnapshotFinalizer = "snapshot"
)

func (r *SnapshotReconciler) cleanupSnapshotResources(log logr.Logger, img *librbd.Image) error {
	pools, imgs, err := img.ListChildren()
	if err != nil {
		return fmt.Errorf("unable to list children: %w", err)
	}
	log.V(2).Info("Snapshot references", "pools", len(pools), "rbd-images", len(imgs))

	if len(pools) != 0 || len(imgs) != 0 {
		return fmt.Errorf("unable to delete snapshot: still in use")
	}

	snaps, err := img.GetSnapshotNames()
	if err != nil {
		return fmt.Errorf("unable to list snapshots: %w", err)
	}
	log.V(2).Info("Found snapshots", "count", len(snaps))

	for _, snapInfo := range snaps {
		log.V(2).Info("Start to delete snapshot", "name", snapInfo.Name)

		snap := img.GetSnapshot(snapInfo.Name)
		isProtected, err := snap.IsProtected()
		if err != nil {
			return fmt.Errorf("unable to chek if snapshot is protected: %w", err)
		}

		if isProtected {
			if err := snap.Unprotect(); err != nil {
				return fmt.Errorf("unable to unprotect snapshot: %w", err)
			}
		}

		if err := snap.Remove(); err != nil {
			return fmt.Errorf("unable to remove snapshot snapshot: %w", err)
		}
	}

	return nil
}

func (r *SnapshotReconciler) deleteSnapshot(ctx context.Context, log logr.Logger, ioCtx *rados.IOContext, snapshot *api.Snapshot) error {
	if !slices.Contains(snapshot.Finalizers, SnapshotFinalizer) {
		log.V(1).Info("snapshot has no finalizer: done")
		return nil
	}

	img, err := librbd.OpenImage(ioCtx, SnapshotIDToRBDID(snapshot.ID), librbd.NoSnapshot)
	if err != nil {
		if !errors.Is(err, librbd.ErrNotFound) {
			return fmt.Errorf("failed to fetch snapshot: %w", err)
		}

		snapshot.Finalizers = utils.DeleteSliceElement(snapshot.Finalizers, SnapshotFinalizer)
		if _, err := r.store.Update(ctx, snapshot); store.IgnoreErrNotFound(err) != nil {
			return fmt.Errorf("failed to update snapshot metadata: %w", err)
		}

		log.V(2).Info("Removed snapshot finalizer")
		return nil

	}

	if err := r.cleanupSnapshotResources(log, img); err != nil {
		if closeErr := img.Close(); closeErr != nil {
			return errors.Join(err, fmt.Errorf("unable to close snapshot: %w", closeErr))
		}
		return fmt.Errorf("failed to cleanup snapshot resources: %w", err)
	}

	if err := img.Close(); err != nil {
		return fmt.Errorf("unable to close snapshot: %w", err)
	}

	log.V(2).Info("Remove snapshot")
	if err := img.Remove(); err != nil {
		return fmt.Errorf("unable to remove snapshot: %w", err)
	}
	log.V(2).Info("Snapshot deleted")

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

	if snapshot.Status.State == api.SnapshotStatePopulated {
		log.V(1).Info("Snapshot already populated")
		return nil
	}

	if !slices.Contains(snapshot.Finalizers, SnapshotFinalizer) {
		snapshot.Finalizers = append(snapshot.Finalizers, SnapshotFinalizer)
		if _, err := r.store.Update(ctx, snapshot); err != nil {
			return fmt.Errorf("failed to set finalizers: %w", err)
		}
	}

	rc, snapshotSize, digest, err := r.openSnapshotSource(ctx, snapshot.Source)
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

	roundedSize := round.OffBytes(snapshotSize)
	if err = librbd.CreateImage(ioCtx, SnapshotIDToRBDID(snapshot.ID), roundedSize, options); err != nil {
		return fmt.Errorf("failed to create os rbd image: %w", err)
	}
	log.V(2).Info("Created rbd image", "bytes", roundedSize)

	rbdImg, err := librbd.OpenImage(ioCtx, SnapshotIDToRBDID(snapshot.ID), librbd.NoSnapshot)
	if err != nil {
		return fmt.Errorf("failed to open rbd image: %w", err)
	}

	if err := r.prepareSnapshotContent(log, rbdImg, rc); err != nil {
		if closeErr := rbdImg.Close(); closeErr != nil {
			return errors.Join(err, fmt.Errorf("unable to close snapshot: %w", closeErr))
		}
	}

	if err := rbdImg.Close(); err != nil {
		return fmt.Errorf("unable to close snapshot: %w", err)
	}

	snapshot.Status.Digest = digest
	snapshot.Status.State = api.SnapshotStatePopulated

	if _, err = r.store.Update(ctx, snapshot); err != nil {
		return fmt.Errorf("failed to update snapshot metadate: %w", err)
	}

	return nil
}

func (r *SnapshotReconciler) prepareSnapshotContent(log logr.Logger, rbdImg *librbd.Image, rc io.ReadCloser) error {
	if err := r.populateImage(log, rbdImg, rc); err != nil {
		return fmt.Errorf("failed to populate os image: %w", err)
	}
	log.V(2).Info("Populated os image on rbd image")

	imgSnap, err := rbdImg.CreateSnapshot(ImageSnapshotVersion)
	if err != nil {
		return fmt.Errorf("unable to create snapshot: %w", err)
	}

	if err := imgSnap.Protect(); err != nil {
		return fmt.Errorf("unable to protect snapshot: %w", err)
	}

	return nil
}

func (r *SnapshotReconciler) populateImage(log logr.Logger, dst io.WriteCloser, src io.Reader) error {
	rater := utils.NewRater(src)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	done := make(chan struct{})

	go func() {
		for {
			select {
			case <-ticker.C:
				log.Info("Populating", "rate", rater.String())
			case <-done:
				return
			}
		}
	}()
	defer func() { close(done) }()

	buffer := make([]byte, r.populatorBufferSize)
	_, err := io.CopyBuffer(dst, rater, buffer)
	if err != nil {
		return fmt.Errorf("failed to populate image: %w", err)
	}
	log.Info("Successfully populated image")

	return nil
}
