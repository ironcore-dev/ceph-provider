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

func (r *SnapshotReconciler) removeSnapshot(log logr.Logger, snapshotID string, img *librbd.Image) error {
	log.V(2).Info("Remove snapshot")

	pools, imgs, err := img.ListChildren()
	if err != nil {
		return fmt.Errorf("unable to list children: %w", err)
	}
	log.V(2).Info("Snapshot references", "pools", len(pools), "rbd-images", len(imgs))

	if len(pools) != 0 || len(imgs) != 0 {
		return fmt.Errorf("unable to delete snapshot: still in use")
	}

	snapshot := img.GetSnapshot(snapshotID)
	isProtected, err := snapshot.IsProtected()
	if err != nil {
		return fmt.Errorf("unable to check if snapshot is protected: %w", err)
	}

	if isProtected {
		if err := snapshot.Unprotect(); err != nil {
			return fmt.Errorf("unable to unprotect snapshot: %w", err)
		}
	}

	if err := snapshot.Remove(); err != nil {
		return fmt.Errorf("unable to remove snapshot: %w", err)
	}
	log.V(2).Info("Snapshot Removed")

	if err := img.Close(); err != nil {
		return fmt.Errorf("unable to close snapshot: %w", err)
	}
	return nil
}

func (r *SnapshotReconciler) deleteSnapshot(log logr.Logger, ioCtx *rados.IOContext, snapshot *providerapi.Snapshot) error {
	if !slices.Contains(snapshot.Finalizers, SnapshotFinalizer) {
		log.V(1).Info("snapshot has no finalizer: done")
		return nil
	}

	rbdID, snapshotID, err := getSnapshotSourceDetails(snapshot)
	if err != nil {
		return fmt.Errorf("failed to get snapshot source details: %w", err)
	}

	img, err := librbd.OpenImage(ioCtx, rbdID, librbd.NoSnapshot)
	if err != nil {
		if !errors.Is(err, librbd.ErrNotFound) {
			return fmt.Errorf("failed to open image: %w", err)
		}
		return nil
	}

	if err = r.removeSnapshot(log, snapshotID, img); err != nil {
		if closeErr := img.Close(); closeErr != nil {
			return errors.Join(err, fmt.Errorf("unable to close snapshot: %w", closeErr))
		}
		return fmt.Errorf("failed to remove snapshot: %w", err)
	}

	if snapshot.Source.IronCoreImage != "" {
		log.V(2).Info("Remove ironcore os-image")
		if err := img.Remove(); err != nil {
			return fmt.Errorf("unable to remove snapshot: %w", err)
		}
		log.V(2).Info("Ironcore os-image removed")
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
		if err := r.deleteSnapshot(log, ioCtx, snapshot); err != nil {
			return fmt.Errorf("failed to delete snapshot: %w", err)
		} else if slices.Contains(snapshot.Finalizers, SnapshotFinalizer) {
			snapshot.Finalizers = utils.DeleteSliceElement(snapshot.Finalizers, SnapshotFinalizer)
			if _, err := r.store.Update(ctx, snapshot); store.IgnoreErrNotFound(err) != nil {
				return fmt.Errorf("failed to update snapshot metadata: %w", err)
			}
			log.V(2).Info("Removed snapshot finalizer")
			return nil
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

	switch {
	case snapshot.Source.IronCoreImage != "":
		err = r.reconcileIroncoreImageSnapshot(ctx, log, ioCtx, snapshot)
	case snapshot.Source.VolumeImageID != "":
		err = r.reconcileVolumeImageSnapshot(ctx, ioCtx, snapshot)
	default:
		return fmt.Errorf("snapshot source not found")
	}
	if err != nil {
		snapshot.Status.State = providerapi.SnapshotStateFailed
		if _, updateErr := r.store.Update(ctx, snapshot); updateErr != nil {
			return fmt.Errorf("failed to update snapshot state: %w", updateErr)
		}
		return fmt.Errorf("failed to reconcile snapshot: %w", err)
	}

	snapshot.Status.State = providerapi.SnapshotStateReady
	if _, err = r.store.Update(ctx, snapshot); err != nil {
		return fmt.Errorf("failed to update snapshot: %w", err)
	}

	return nil
}
func (r *SnapshotReconciler) reconcileIroncoreImageSnapshot(ctx context.Context, log logr.Logger, ioCtx *rados.IOContext, snapshot *providerapi.Snapshot) error {
	rc, snapshotSize, digest, err := r.openIroncoreImageSource(ctx, snapshot.Source.IronCoreImage)
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

	snapshotName := SnapshotIDToRBDID(snapshot.ID)
	roundedSize := round.OffBytes(snapshotSize)
	if err = librbd.CreateImage(ioCtx, snapshotName, roundedSize, options); err != nil {
		return fmt.Errorf("failed to create os rbd image: %w", err)
	}
	log.V(2).Info("Created rbd image", "bytes", roundedSize)

	rbdImg, err := librbd.OpenImage(ioCtx, snapshotName, librbd.NoSnapshot)
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
	snapshot.Status.Size = int64(roundedSize)
	return nil
}

func (r *SnapshotReconciler) reconcileVolumeImageSnapshot(ctx context.Context, ioCtx *rados.IOContext, snapshot *providerapi.Snapshot) error {
	img, err := r.images.Get(ctx, snapshot.Source.VolumeImageID)
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

	snapshotName := snapshot.ID
	imgSnap, err := rbdImg.CreateSnapshot(snapshotName)
	if err != nil {
		return fmt.Errorf("unable to create snapshot: %w", err)
	}

	if err := imgSnap.Protect(); err != nil {
		return fmt.Errorf("unable to protect snapshot: %w", err)
	}

	if err := rbdImg.SetSnapshot(snapshotName); err != nil {
		return fmt.Errorf("failed to set snapshot %s for image %s: %w", snapshotName, img.ID, err)
	}

	// Get image size
	size, err := rbdImg.GetSize()
	if err != nil {
		return fmt.Errorf("failed to get snapshot image size %s: %w", snapshotName, err)
	}
	roundedSize := round.OffBytes(size)

	if err := rbdImg.Close(); err != nil {
		return fmt.Errorf("unable to close snapshot: %w", err)
	}

	snapshot.Status.Size = int64(roundedSize)
	return nil
}

func (r *SnapshotReconciler) openIroncoreImageSource(ctx context.Context, imageReference string) (io.ReadCloser, uint64, string, error) {
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

func (r *SnapshotReconciler) prepareSnapshotContent(log logr.Logger, rbdImg *librbd.Image, rc io.ReadCloser) error {
	if err := r.populateImage(log, rbdImg, rc); err != nil {
		return fmt.Errorf("failed to populate os image: %w", err)
	}
	log.V(2).Info("Populated os image on rbd image")

	imgSnap, err := rbdImg.CreateSnapshot(ImageSnapshotVersion)
	if err != nil {
		return fmt.Errorf("unable to create snapshot: %w", err)
	}
	log.V(2).Info("Created snapshot")

	if err := imgSnap.Protect(); err != nil {
		return fmt.Errorf("unable to protect snapshot: %w", err)
	}
	log.V(2).Info("Protected snapshot")

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
