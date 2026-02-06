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
		snapshot.Finalizers = utils.DeleteSliceElement(snapshot.Finalizers, SnapshotFinalizer)
		if _, err := r.store.Update(ctx, snapshot); store.IgnoreErrNotFound(err) != nil {
			return fmt.Errorf("failed to update snapshot metadata: %w", err)
		}
		log.V(2).Info("Removed snapshot finalizer")
		return nil
	}
	shouldClose := true
	defer func() {
		if shouldClose {
			closeImage(log, img)
		}
	}()

	if err := flattenChildImages(log, r.conn, img); err != nil {
		return fmt.Errorf("failed to flatten snapshot child images: %w", err)
	}

	log.V(2).Info("Remove snapshot")
	rbdSnapshot := img.GetSnapshot(snapshotID)
	if err := removeSnapshot(rbdSnapshot); err != nil {
		return fmt.Errorf("failed to remove snapshot: %w", err)
	}

	snapshot.Finalizers = utils.DeleteSliceElement(snapshot.Finalizers, SnapshotFinalizer)
	if _, err := r.store.Update(ctx, snapshot); store.IgnoreErrNotFound(err) != nil {
		return fmt.Errorf("failed to update snapshot metadata: %w", err)
	}
	log.V(2).Info("Removed snapshot finalizer")

	// deletes os-image if not referenced by any volume
	if snapshot.Source.IronCoreImage != "" {
		log.V(2).Info("Remove ironcore os-image")
		shouldClose = false
		if err := img.Close(); err != nil {
			return fmt.Errorf("unable to close ironcore os-image: %w", err)
		}

		if err := librbd.RemoveImage(ioCtx, rbdID); err != nil {
			return fmt.Errorf("unable to remove ironcore os-image: %w", err)
		}
		log.V(2).Info("Ironcore os-image removed")
	}

	// deletes parent rbd image of snapshot which is created during source volume deletion
	// and has no any other reference except snapshot
	if rbdID == ImageIDToRBDID(snapshotID) {
		log.V(2).Info("Remove parent rbd image")
		if err := r.images.Delete(ctx, snapshotID); store.IgnoreErrNotFound(err) != nil {
			return fmt.Errorf("unable to remove parent rbd image: %w", err)
		}
		log.V(2).Info("Removed parent rbd image")
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
		if err := r.deleteSnapshot(ctx, log, ioCtx, snapshot); err != nil {
			return fmt.Errorf("failed to delete snapshot: %w", err)
		}
		log.V(1).Info("Successfully deleted snapshot")
		return nil
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

	imageName := SnapshotIDToRBDID(snapshot.ID)
	roundedSize := round.OffBytes(snapshotSize)

	if err = librbd.CreateImage(ioCtx, imageName, roundedSize, options); err != nil {
		if errors.Is(err, librbd.ErrExist) {
			log.V(2).Info("RBD image already exists, skipping creation", "ImageID", imageName)
		} else {
			return fmt.Errorf("failed to create os rbd image: %w", err)
		}
	}
	log.V(2).Info("Created rbd image", "bytes", roundedSize)

	if err := r.prepareSnapshotContent(log, ioCtx, imageName, rc); err != nil {
		return fmt.Errorf("failed to prepare snapshot content: %w", err)
	}

	log.V(2).Info("Create ironcore image snapshot", "ImageID", imageName)
	if err := createSnapshot(log, ioCtx, ImageSnapshotVersion, imageName); err != nil {
		return fmt.Errorf("failed to create ironcore image snapshot: %w", err)
	}

	snapshot.Status.Digest = digest
	snapshot.Status.Size = int64(roundedSize)
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
