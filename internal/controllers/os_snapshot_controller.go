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
	"github.com/ironcore-dev/ceph-provider/internal/round"
	"github.com/ironcore-dev/ceph-provider/internal/utils"
	ironcoreimage "github.com/ironcore-dev/ironcore-image"
	"github.com/ironcore-dev/ironcore-image/oci/image"
	"github.com/ironcore-dev/provider-utils/eventutils/event"
	"github.com/ironcore-dev/provider-utils/storeutils/store"
	"k8s.io/client-go/util/workqueue"
)

type OSSnapshotReconcilerOptions struct {
	Pool                string
	PopulatorBufferSize int64
}

func NewOSSnapshotReconciler(
	log logr.Logger,
	conn *rados.Conn,
	registry image.Source,
	store store.Store[*providerapi.OSSnapshot],
	events event.Source[*providerapi.OSSnapshot],
	opts OSSnapshotReconcilerOptions,
) (*OSSnapshotReconciler, error) {
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

	return &OSSnapshotReconciler{
		log:                 log,
		conn:                conn,
		registry:            registry,
		queue:               workqueue.NewTypedRateLimitingQueue[string](workqueue.DefaultTypedControllerRateLimiter[string]()),
		store:               store,
		events:              events,
		pool:                opts.Pool,
		populatorBufferSize: opts.PopulatorBufferSize,
	}, nil
}

type OSSnapshotReconciler struct {
	log  logr.Logger
	conn *rados.Conn

	registry image.Source
	queue    workqueue.TypedRateLimitingInterface[string]

	store  store.Store[*providerapi.OSSnapshot]
	events event.Source[*providerapi.OSSnapshot]

	pool                string
	populatorBufferSize int64
}

func (r *OSSnapshotReconciler) openOSImageSource(ctx context.Context, ioCtx *rados.IOContext, imageReference string) (io.ReadCloser, uint64, string, error) {
	img, err := r.registry.Resolve(ctx, imageReference)
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
}

func (r *OSSnapshotReconciler) Start(ctx context.Context) error {
	log := r.log

	//todo make configurable
	workerSize := 15

	reg, err := r.events.AddHandler(event.HandlerFunc[*providerapi.OSSnapshot](func(event event.Event[*providerapi.OSSnapshot]) {
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

func (r *OSSnapshotReconciler) processNextWorkItem(ctx context.Context, log logr.Logger) bool {
	id, shutdown := r.queue.Get()
	if shutdown {
		return false
	}
	defer r.queue.Done(id)

	log = log.WithValues("osSnapshotId", id)
	ctx = logr.NewContext(ctx, log)

	if err := r.reconcileOSSnapshot(ctx, id); err != nil {
		log.Error(err, "failed to reconcile OS snapshot")
		r.queue.AddRateLimited(id)
		return true
	}

	r.queue.Forget(id)
	return true
}

const (
	OSSnapshotFinalizer = "os-snapshot"
)

func (r *OSSnapshotReconciler) cleanupSnapshotResources(log logr.Logger, img *librbd.Image) error {
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

func (r *OSSnapshotReconciler) deleteOSSnapshot(ctx context.Context, log logr.Logger, ioCtx *rados.IOContext, osSnapshot *providerapi.OSSnapshot) error {
	if !slices.Contains(osSnapshot.Finalizers, OSSnapshotFinalizer) {
		log.V(1).Info("snapshot has no finalizer: done")
		return nil
	}

	img, err := librbd.OpenImage(ioCtx, SnapshotIDToRBDID(osSnapshot.ID), librbd.NoSnapshot)
	if err != nil {
		if !errors.Is(err, librbd.ErrNotFound) {
			return fmt.Errorf("failed to fetch snapshot: %w", err)
		}

		osSnapshot.Finalizers = utils.DeleteSliceElement(osSnapshot.Finalizers, SnapshotFinalizer)
		if _, err := r.store.Update(ctx, osSnapshot); store.IgnoreErrNotFound(err) != nil {
			return fmt.Errorf("failed to update snapshot metadata: %w", err)
		}

		log.V(2).Info("Removed snapshot finalizer")
		return nil

	}

	if err := r.cleanupSnapshotResources(log, img); err != nil {
		if closeErr := img.Close(); closeErr != nil {
			return errors.Join(err, fmt.Errorf("unable to close snapshot: %w", closeErr))
		}
		return fmt.Errorf("failed to cleanup os snapshot resources: %w", err)
	}

	if err := img.Close(); err != nil {
		return fmt.Errorf("unable to close snapshot: %w", err)
	}

	log.V(2).Info("Remove snapshot")
	if err := img.Remove(); err != nil {
		return fmt.Errorf("unable to remove os snapshot: %w", err)
	}
	log.V(2).Info("Snapshot deleted")

	osSnapshot.Finalizers = utils.DeleteSliceElement(osSnapshot.Finalizers, SnapshotFinalizer)
	if _, err := r.store.Update(ctx, osSnapshot); store.IgnoreErrNotFound(err) != nil {
		return fmt.Errorf("failed to update os snapshot metadata: %w", err)
	}

	log.V(2).Info("Deleted os snapshot")
	return nil
}

func (r *OSSnapshotReconciler) reconcileOSSnapshot(ctx context.Context, id string) error {
	log := logr.FromContextOrDiscard(ctx)
	ioCtx, err := r.conn.OpenIOContext(r.pool)
	if err != nil {
		return fmt.Errorf("unable to get io context: %w", err)
	}
	defer ioCtx.Destroy()

	osSnapshot, err := r.store.Get(ctx, id)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("failed to fetch os snapshot from store: %w", err)
		}

		return nil
	}

	if osSnapshot.DeletedAt != nil {
		if err := r.deleteOSSnapshot(ctx, log, ioCtx, osSnapshot); err != nil {
			return fmt.Errorf("failed to delete os snapshot: %w", err)
		}
	}

	if osSnapshot.Status.State == providerapi.OSSnapshotStatePopulated {
		log.V(1).Info("OSSnapshot already populated")
		return nil
	}

	if !slices.Contains(osSnapshot.Finalizers, OSSnapshotFinalizer) {
		osSnapshot.Finalizers = append(osSnapshot.Finalizers, OSSnapshotFinalizer)
		if _, err := r.store.Update(ctx, osSnapshot); err != nil {
			return fmt.Errorf("failed to set finalizers: %w", err)
		}
	}

	if osSnapshot.Source.IronCoreOSImage != "" {
		rc, snapshotSize, digest, err := r.openOSImageSource(ctx, ioCtx, osSnapshot.Source.IronCoreOSImage)
		if err != nil {
			return fmt.Errorf("failed to open os snapshot source: %w", err)
		}
		defer func() {
			if err := rc.Close(); err != nil {
				log.Error(err, "failed to close os snapshot source")
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
		if err = librbd.CreateImage(ioCtx, SnapshotIDToRBDID(osSnapshot.ID), roundedSize, options); err != nil {
			return fmt.Errorf("failed to create os rbd image: %w", err)
		}
		log.V(2).Info("Created rbd image", "bytes", roundedSize)

		rbdImg, err := librbd.OpenImage(ioCtx, SnapshotIDToRBDID(osSnapshot.ID), librbd.NoSnapshot)
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

		osSnapshot.Status.Digest = digest
		osSnapshot.Status.State = providerapi.OSSnapshotStatePopulated

		if _, err = r.store.Update(ctx, osSnapshot); err != nil {
			return fmt.Errorf("failed to update snapshot metadate: %w", err)
		}
	}

	return nil
}

func (r *OSSnapshotReconciler) prepareSnapshotContent(log logr.Logger, rbdImg *librbd.Image, rc io.ReadCloser) error {
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

func (r *OSSnapshotReconciler) populateImage(log logr.Logger, dst io.WriteCloser, src io.Reader) error {
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
