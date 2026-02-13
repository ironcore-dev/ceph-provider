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
	apiutils "github.com/ironcore-dev/provider-utils/apiutils/api"
	"github.com/ironcore-dev/provider-utils/eventutils/event"
	"github.com/ironcore-dev/provider-utils/storeutils/store"
	"k8s.io/client-go/util/workqueue"
)

type VolumeReconcilerOptions struct {
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
	events event.Source[*providerapi.Volume],
	opts VolumeReconcilerOptions,
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

	events event.Source[*providerapi.Volume]

	pool                string
	populatorBufferSize int64

	workerSize int
}

func (r *VolumeReconciler) Start(ctx context.Context) error {
	log := r.log

	// TODO: Handlers for snapshot and image events needed in cases were reconcile of the volume is needed
	reg, err := r.events.AddHandler(event.HandlerFunc[*providerapi.Volume](func(event event.Event[*providerapi.Volume]) {
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

	// TODO: can this be removed to allow reconcile after volume has become available?
	if volume.Status.State == providerapi.VolumeStateAvailable {
		log.V(1).Info("Volume already available")
		return nil
	}

	if !slices.Contains(volume.Finalizers, VolumeFinalizer) {
		volume.Finalizers = append(volume.Finalizers, VolumeFinalizer)
		if _, err := r.volumeStore.Update(ctx, volume); err != nil {
			return fmt.Errorf("failed to set finalizers: %w", err)
		}
	}

	// If volume already has an ImageRef, check the referenced image's state
	if volume.Status.ImageRef != "" {
		img, err := r.imageStore.Get(ctx, volume.Status.ImageRef)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("failed to get image: %w", err)
		}

		if img != nil {
			// Image exists, check its state
			switch img.Status.State {
			case providerapi.ImageStatePending:
				log.V(1).Info("Image is pending, requeuing")
				r.queue.AddAfter(volume.ID, time.Second*60)
				return nil
			case providerapi.ImageStateAvailable:
				if volume.Status.State == providerapi.VolumeStateAvailable {
					// Volume already marked as available, no need to update
					return nil
				}
				log.V(1).Info("Image is available, updating volume status")
				volume.Status.State = providerapi.VolumeStateAvailable
				volume.Status.Size = img.Status.Size
				if img.Status.Access != nil {
					volume.Status.Access = &providerapi.VolumeAccess{
						Monitors: img.Status.Access.Monitors,
						Handle:   img.Status.Access.Handle,
						User:     img.Status.Access.User,
						UserKey:  img.Status.Access.UserKey,
					}
				}
				if _, err := r.volumeStore.Update(ctx, volume); err != nil {
					return fmt.Errorf("failed to update volume status: %w", err)
				}
				return nil
			default:
				return fmt.Errorf("image %s in unexpected state: %s", img.ID, img.Status.State)
			}
		}

		// Image was deleted, clear the ref and proceed to recreate
		log.V(1).Info("Referenced image not found, clearing ImageRef")
		volume.Status.ImageRef = ""
		if _, err := r.volumeStore.Update(ctx, volume); err != nil {
			return fmt.Errorf("failed to clear image ref: %w", err)
		}
	}

	// ImageRef is empty, need to create the image based on volume source
	switch {
	case volume.Spec.Source.OSVolume == nil && volume.Spec.Source.SnapshotSource == nil:
		return r.reconcileEmptyVolume(ctx, log, ioCtx, volume)
	case volume.Spec.Source.OSVolume != nil && volume.Spec.Source.SnapshotSource == nil:
		return r.reconcileOSVolume(ctx, log, ioCtx, volume)
	case volume.Spec.Source.OSVolume == nil && volume.Spec.Source.SnapshotSource != nil:
		return r.reconcileRestoredVolume(ctx, log, ioCtx, volume)
	default:
		return fmt.Errorf("invalid volume specification")
	}
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

func (r *VolumeReconciler) reconcileEmptyVolume(ctx context.Context, log logr.Logger, ioCtx *rados.IOContext, volume *providerapi.Volume) error {
	log.V(1).Info("Reconciling empty volume")

	// Create new Image
	log.V(1).Info("Creating image for empty volume")
	img := &providerapi.Image{
		Metadata: apiutils.Metadata{
			ID: volume.ID,
		},
		Spec: providerapi.ImageSpec{
			Size:   volume.Spec.Size,
			WWN:    volume.Spec.WWN,
			Limits: volume.Spec.Limits,
			Encryption: providerapi.EncryptionSpec{
				Type:                providerapi.EncryptionType(volume.Spec.VolumeEncryption.Type),
				EncryptedPassphrase: volume.Spec.VolumeEncryption.EncryptedPassphrase,
			},
			SnapshotSource: nil,
		},
	}

	createdImage, err := r.imageStore.Create(ctx, img)
	if err != nil {
		return fmt.Errorf("failed to create image: %w", err)
	}

	log.V(1).Info("Image created", "ImageID", createdImage.ID)

	// Update volume status with ImageRef
	volume.Status.ImageRef = createdImage.ID
	if _, err := r.volumeStore.Update(ctx, volume); err != nil {
		return fmt.Errorf("failed to update volume with image ref: %w", err)
	}

	return nil
}

func (r *VolumeReconciler) reconcileOSVolume(ctx context.Context, log logr.Logger, ioCtx *rados.IOContext, volume *providerapi.Volume) error {
	log.V(1).Info("Reconciling OS volume")

	if volume.Spec.Source.OSVolume == nil {
		return fmt.Errorf("OSVolume source is nil")
	}
	osVolume := volume.Spec.Source.OSVolume

	// Step 1: Resolve OCI image to get digest
	// TODO: Consider architecture from osVolume.Architecture
	log.V(1).Info("Resolving OCI image", "imageName", osVolume.Name)
	ociImage, err := r.registry.Resolve(ctx, osVolume.Name)
	if err != nil {
		return fmt.Errorf("failed to resolve OCI image %s: %w", osVolume.Name, err)
	}

	// Step 2: Get or create base image
	baseImageID := fmt.Sprintf("os-%s", osVolume.Name)
	snapshotID := ociImage.Descriptor().Digest.Encoded()
	log.V(1).Info("Using base image", "baseImageID", baseImageID, "snapshotID", snapshotID)
	baseImage, err := r.imageStore.Get(ctx, baseImageID)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("failed to get base image: %w", err)
		}

		// Create base image
		log.V(1).Info("Creating base image", "baseImageID", baseImageID)
		baseImage = &providerapi.Image{
			Metadata: apiutils.Metadata{
				ID: baseImageID,
			},
			Spec: providerapi.ImageSpec{
				Size:   volume.Spec.Size, // TODO: Should we get size from OCI manifest?
				WWN:    "",               // Base image doesn't need WWN
				Limits: providerapi.Limits{},
				Encryption: providerapi.EncryptionSpec{
					Type: providerapi.EncryptionTypeUnencrypted, // Base images are unencrypted
				},
				SnapshotSource: nil,
			},
		}

		baseImage, err = r.imageStore.Create(ctx, baseImage)
		if err != nil && !errors.Is(err, store.ErrAlreadyExists) {
			return fmt.Errorf("failed to create base image: %w", err)
		}
		if errors.Is(err, store.ErrAlreadyExists) {
			// Another controller created it, fetch it
			log.V(1).Info("Base image already exists, fetching", "baseImageID", baseImageID)
			baseImage, err = r.imageStore.Get(ctx, baseImageID)
			if err != nil {
				return fmt.Errorf("failed to get existing base image: %w", err)
			}
		}
	}

	// Step 3: Check base image state
	switch baseImage.Status.State {
	case providerapi.ImageStatePending:
		log.V(1).Info("Base image is pending, waiting", "baseImageID", baseImageID)
		// TODO: Populate the base image here or let Image controller handle it?
		// For now, requeue and wait
		// TODO: avoid static wait duration.
		r.queue.AddAfter(volume.ID, time.Second*60)
		return nil
	case providerapi.ImageStateAvailable:
		log.V(1).Info("Base image is available", "baseImageID", baseImageID)
	default:
		return fmt.Errorf("base image %s in unexpected state: %s", baseImageID, baseImage.Status.State)
	}

	// Step 4: Get or create snapshot of base image
	snapshot, err := r.snapshotStore.Get(ctx, snapshotID)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("failed to get snapshot: %w", err)
		}

		// Create snapshot
		log.V(1).Info("Creating snapshot of base image", "snapshotID", snapshotID, "baseImageID", baseImageID)
		snapshot = &providerapi.Snapshot{
			Metadata: apiutils.Metadata{
				ID: snapshotID,
			},
			Spec: providerapi.SnapshotSpec{
				ImageRef:   baseImageID,
				Protection: providerapi.SnapshotProtectionProtected, // Protection needed for cloning
			},
		}

		snapshot, err = r.snapshotStore.Create(ctx, snapshot)
		if err != nil && !errors.Is(err, store.ErrAlreadyExists) {
			return fmt.Errorf("failed to create snapshot: %w", err)
		}
		if errors.Is(err, store.ErrAlreadyExists) {
			// Another controller created it, fetch it
			log.V(1).Info("Snapshot already exists, fetching", "snapshotID", snapshotID)
			snapshot, err = r.snapshotStore.Get(ctx, snapshotID)
			if err != nil {
				return fmt.Errorf("failed to get existing snapshot: %w", err)
			}
		}
	}

	// Step 5: Check snapshot state
	switch snapshot.Status.State {
	case providerapi.SnapshotStatePending:
		log.V(1).Info("Snapshot is pending, waiting", "snapshotID", snapshotID)
		r.queue.AddAfter(volume.ID, time.Second*60)
		return nil
	case providerapi.SnapshotStateReady:
		log.V(1).Info("Snapshot is ready", "snapshotID", snapshotID)
	case providerapi.SnapshotStateFailed:
		// TODO: Do we need a failed state for volumes? or should we trigger recreation of the snapshot?
		return fmt.Errorf("snapshot %s failed", snapshotID)
	default:
		return fmt.Errorf("snapshot %s in unexpected state: %s", snapshotID, snapshot.Status.State)
	}

	// Step 6: Create volume's image as clone from snapshot
	log.V(1).Info("Creating volume image from snapshot", "snapshotID", snapshotID)
	volumeImage := &providerapi.Image{
		Metadata: apiutils.Metadata{
			ID: volume.ID,
		},
		Spec: providerapi.ImageSpec{
			Size:   volume.Spec.Size,
			WWN:    volume.Spec.WWN,
			Limits: volume.Spec.Limits,
			Encryption: providerapi.EncryptionSpec{
				Type:                providerapi.EncryptionType(volume.Spec.VolumeEncryption.Type),
				EncryptedPassphrase: volume.Spec.VolumeEncryption.EncryptedPassphrase,
			},
			SnapshotSource: &snapshotID,
		},
	}

	createdImage, err := r.imageStore.Create(ctx, volumeImage)
	if err != nil {
		return fmt.Errorf("failed to create volume image: %w", err)
	}

	log.V(1).Info("Volume image created", "ImageID", createdImage.ID, "SnapshotID", snapshotID)

	// Step 7: Update volume status with ImageRef
	volume.Status.ImageRef = createdImage.ID
	if _, err := r.volumeStore.Update(ctx, volume); err != nil {
		return fmt.Errorf("failed to update volume with image ref: %w", err)
	}

	return nil
}

func (r *VolumeReconciler) reconcileRestoredVolume(ctx context.Context, log logr.Logger, ioCtx *rados.IOContext, volume *providerapi.Volume) error {
	log.V(1).Info("Reconciling restored volume from snapshot")

	// Verify snapshot exists
	if volume.Spec.Source.SnapshotSource == nil {
		return fmt.Errorf("snapshot source is nil")
	}
	snapshotID := *volume.Spec.Source.SnapshotSource

	snapshot, err := r.snapshotStore.Get(ctx, snapshotID)
	if err != nil {
		return fmt.Errorf("failed to get snapshot %s: %w", snapshotID, err)
	}

	switch snapshot.Status.State {
	case providerapi.SnapshotStatePending:
		log.V(1).Info("Snapshot is pending, waiting", "snapshotID", snapshotID)
		r.queue.AddAfter(volume.ID, time.Second*60)
		return nil
	case providerapi.SnapshotStateReady:
		log.V(1).Info("Snapshot is ready", "snapshotID", snapshotID)
	case providerapi.SnapshotStateFailed:
		// TODO: Do we need a failed state for volumes? or should we trigger recreation of the snapshot?
		return fmt.Errorf("snapshot %s failed", snapshotID)
	default:
		return fmt.Errorf("snapshot %s in unexpected state: %s", snapshotID, snapshot.Status.State)
	}

	// Create new Image from snapshot
	log.V(1).Info("Creating image from snapshot", "snapshotID", snapshotID)
	img := &providerapi.Image{
		Metadata: apiutils.Metadata{
			ID: volume.ID,
		},
		Spec: providerapi.ImageSpec{
			Size:   volume.Spec.Size,
			WWN:    volume.Spec.WWN,
			Limits: volume.Spec.Limits,
			Encryption: providerapi.EncryptionSpec{
				Type:                providerapi.EncryptionType(volume.Spec.VolumeEncryption.Type),
				EncryptedPassphrase: volume.Spec.VolumeEncryption.EncryptedPassphrase,
			},
			SnapshotSource: &snapshotID,
		},
	}

	createdImage, err := r.imageStore.Create(ctx, img)
	if err != nil {
		return fmt.Errorf("failed to create image: %w", err)
	}

	log.V(1).Info("Image created from snapshot", "ImageID", createdImage.ID, "SnapshotID", snapshotID)

	// Update volume status with ImageRef
	volume.Status.ImageRef = createdImage.ID
	if _, err := r.volumeStore.Update(ctx, volume); err != nil {
		return fmt.Errorf("failed to update volume with image ref: %w", err)
	}

	return nil
}
