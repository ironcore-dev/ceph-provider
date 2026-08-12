// SPDX-FileCopyrightText: 2023 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/distribution/reference"
	"github.com/go-logr/logr"
	providerapi "github.com/ironcore-dev/ceph-provider/api/v2"
	ironcoreimage "github.com/ironcore-dev/ironcore-image"
	"github.com/ironcore-dev/ironcore-image/oci/image"
	apiutils "github.com/ironcore-dev/provider-utils/apiutils/api"
	"github.com/ironcore-dev/provider-utils/eventutils/event"
	"github.com/ironcore-dev/provider-utils/storeutils/store"
	"k8s.io/client-go/util/workqueue"
)

type VolumeReconcilerOptions struct {
	WorkerSize int
}

func NewVolumeReconciler(
	log logr.Logger,
	registry image.Source,
	snapshotStore store.Store[*providerapi.Snapshot],
	imageStore store.Store[*providerapi.Image],
	volumeStore store.Store[*providerapi.Volume],
	events event.Source[*providerapi.Volume],
	imageEvents event.Source[*providerapi.Image],
	snapshotEvents event.Source[*providerapi.Snapshot],
	opts VolumeReconcilerOptions,
) (*VolumeReconciler, error) {
	if registry == nil {
		return nil, fmt.Errorf("must specify registry")
	}

	if snapshotStore == nil {
		return nil, fmt.Errorf("must specify snapshot store")
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

	if imageEvents == nil {
		return nil, fmt.Errorf("must specify image events")
	}

	if snapshotEvents == nil {
		return nil, fmt.Errorf("must specify snapshot events")
	}

	if opts.WorkerSize == 0 {
		opts.WorkerSize = 15
	}

	return &VolumeReconciler{
		log:            log,
		registry:       registry,
		queue:          workqueue.NewTypedRateLimitingQueue[string](workqueue.DefaultTypedControllerRateLimiter[string]()),
		snapshotStore:  snapshotStore,
		imageStore:     imageStore,
		volumeStore:    volumeStore,
		events:         events,
		imageEvents:    imageEvents,
		snapshotEvents: snapshotEvents,
		workerSize:     opts.WorkerSize,
	}, nil
}

type VolumeReconciler struct {
	log logr.Logger

	registry image.Source
	queue    workqueue.TypedRateLimitingInterface[string]

	snapshotStore store.Store[*providerapi.Snapshot]
	imageStore    store.Store[*providerapi.Image]
	volumeStore   store.Store[*providerapi.Volume]

	events         event.Source[*providerapi.Volume]
	imageEvents    event.Source[*providerapi.Image]
	snapshotEvents event.Source[*providerapi.Snapshot]

	workerSize int
}

func (r *VolumeReconciler) Start(ctx context.Context) error {
	log := r.log

	reg, err := r.events.AddHandler(event.HandlerFunc[*providerapi.Volume](func(event event.Event[*providerapi.Volume]) {
		r.queue.Add(event.Object.ID)
	}))
	if err != nil {
		return err
	}
	defer func() {
		_ = r.events.RemoveHandler(reg)
	}()

	imageReg, err := r.imageEvents.AddHandler(event.HandlerFunc[*providerapi.Image](func(evt event.Event[*providerapi.Image]) {
		r.requeueVolumesForImage(ctx, evt.Object.ID)
	}))
	if err != nil {
		return err
	}
	defer func() {
		_ = r.imageEvents.RemoveHandler(imageReg)
	}()

	snapshotReg, err := r.snapshotEvents.AddHandler(event.HandlerFunc[*providerapi.Snapshot](func(evt event.Event[*providerapi.Snapshot]) {
		if evt.Type != event.TypeUpdated && evt.Type != event.TypeDeleted {
			return
		}
		if evt.Object.Status.State != providerapi.SnapshotStateReady &&
			evt.Object.Status.State != providerapi.SnapshotStateFailed &&
			evt.Object.DeletedAt == nil {
			return
		}
		r.requeueVolumesForSnapshot(ctx, evt.Object.ID)
	}))
	if err != nil {
		return err
	}
	defer func() {
		_ = r.snapshotEvents.RemoveHandler(snapshotReg)
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

func (r *VolumeReconciler) requeueVolumesForImage(ctx context.Context, imageID string) {
	// TODO: Requeue OS Volumes when their base image becomes Available
	// OS volumes waiting for a base image have an empty ImageRef,
	// so they are not matched here. We need to connect the base images to the volumes somehow
	volumes, err := r.volumeStore.List(ctx, store.MatchingFields{providerapi.VolumeStatusImageRefField: imageID})
	if err != nil {
		r.log.Error(err, "failed to list volumes for image event requeue")
		return
	}
	for _, vol := range volumes {
		r.queue.Add(vol.ID)
	}
}

func (r *VolumeReconciler) requeueVolumesForSnapshot(ctx context.Context, snapshotID string) {
	// TODO: Requeue OS Volumes when their intermediate snapshot becomes Ready or Failed.
	// OS volumes waiting for a intermediate snapshot have no SnapshotSource,
	// so they are not matched here. We need to connect the snapshots to the volumes somehow
	volumes, err := r.volumeStore.List(ctx, store.MatchingFields{providerapi.VolumeSpecSourceSnapshotSourceField: snapshotID})
	if err != nil {
		r.log.Error(err, "failed to list volumes for snapshot event requeue")
		return
	}
	for _, vol := range volumes {
		r.queue.Add(vol.ID)
	}
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
	VolumeFinalizer     = "volume"
	VolumeImageIDPrefix = "vol-"
	BaseImageIDPrefix   = "os-"
)

func (r *VolumeReconciler) reconcileVolume(ctx context.Context, id string) error {
	log := logr.FromContextOrDiscard(ctx)

	volume, err := r.volumeStore.Get(ctx, id)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("failed to fetch volume from store: %w", err)
		}
		return nil
	}

	if volume.DeletedAt != nil {
		if err := r.deleteVolume(ctx, log, volume); err != nil {
			return fmt.Errorf("failed to delete volume: %w", err)
		}
		return nil
	}

	if !slices.Contains(volume.Finalizers, VolumeFinalizer) {
		var err error
		if _, err = addFinalizer(ctx, r.volumeStore, volume, VolumeFinalizer); err != nil {
			return fmt.Errorf("failed to set finalizers: %w", err)
		}
		return nil
	}

	// If volume already has an ImageRef, check the referenced image's state
	if volume.Status.ImageRef != "" {
		img, err := r.imageStore.Get(ctx, volume.Status.ImageRef)
		if err != nil {
			if !errors.Is(err, store.ErrNotFound) {
				return fmt.Errorf("failed to get image: %w", err)
			}

			// Image was deleted, clear the ref and recreate the image
			log.V(1).Info("Referenced image not found, clearing ImageRef", "imageId", volume.Status.ImageRef)
			volume.Status.ImageRef = ""
			if _, err := r.volumeStore.Update(ctx, volume); err != nil {
				return fmt.Errorf("failed to clear image ref: %w", err)
			}
			return nil
		}

		// If the image was cloned from a snapshot that has since been deleted,
		// clear SnapshotSource so the image proceeds as standalone.
		if img.Spec.SnapshotSource != nil {
			if _, err := r.snapshotStore.Get(ctx, *img.Spec.SnapshotSource); err != nil {
				if !errors.Is(err, store.ErrNotFound) {
					return fmt.Errorf("failed to get snapshot source: %w", err)
				}
				log.V(1).Info("Referenced snapshot not found, clearing SnapshotSource", "imageId", img.ID, "snapshotId", *img.Spec.SnapshotSource)
				img.Spec.SnapshotSource = nil
				if _, err := r.imageStore.Update(ctx, img); err != nil {
					return fmt.Errorf("failed to clear snapshot source on image: %w", err)
				}
				return nil
			}
		}

		// Image exists, reconcile volume state
		switch img.Status.State {
		case providerapi.ImageStatePending:
			log.V(1).Info("Image is pending, waiting until it is available", "imageId", img.ID)
			return nil
		case providerapi.ImageStateAvailable:
			var desiredAccess *providerapi.VolumeAccess
			if img.Status.Access != nil {
				desiredAccess = &providerapi.VolumeAccess{
					Monitors: img.Status.Access.Monitors,
					Handle:   img.Status.Access.Handle,
					User:     img.Status.Access.User,
					UserKey:  img.Status.Access.UserKey,
				}
			}
			changed := volume.Status.Size != img.Status.Size || !volumeAccessEqual(volume.Status.Access, desiredAccess)
			if volume.Status.State == providerapi.VolumeStateAvailable && !changed {
				log.V(2).Info("Image is available, no status changes", "imageId", img.ID)
				return nil
			}
			log.V(1).Info("Image is available, updating volume status", "imageId", img.ID)
			volume.Status.State = providerapi.VolumeStateAvailable
			volume.Status.Size = img.Status.Size
			volume.Status.Access = desiredAccess
			if _, err := r.volumeStore.Update(ctx, volume); err != nil {
				return fmt.Errorf("failed to update volume status: %w", err)
			}
			return nil
		default:
			return fmt.Errorf("image %s in unexpected state: %s", img.ID, img.Status.State)
		}
	}

	// ImageRef is empty, need to create the image based on volume source
	switch {
	case volume.Spec.Source.OSVolume == nil && volume.Spec.Source.SnapshotSource == nil:
		return r.reconcileEmptyVolume(ctx, log, volume)
	case volume.Spec.Source.OSVolume != nil && volume.Spec.Source.SnapshotSource == nil:
		return r.reconcileOSVolume(ctx, log, volume)
	case volume.Spec.Source.OSVolume == nil && volume.Spec.Source.SnapshotSource != nil:
		return r.reconcileRestoredVolume(ctx, log, volume)
	default:
		return fmt.Errorf("invalid volume specification")
	}
}

func (r *VolumeReconciler) setVolumeImageRef(ctx context.Context, volume *providerapi.Volume, imageID string) error {
	if volume.Status.ImageRef == imageID {
		return nil
	}
	volume.Status.ImageRef = imageID
	if _, err := r.volumeStore.Update(ctx, volume); err != nil {
		return fmt.Errorf("failed to update volume with image ref: %w", err)
	}
	return nil
}

func buildVolumeImage(volume *providerapi.Volume, snapshotSource *string) *providerapi.Image {
	return &providerapi.Image{
		Metadata: apiutils.Metadata{
			ID: VolumeImageIDPrefix + volume.ID,
		},
		Spec: providerapi.ImageSpec{
			Size:   volume.Spec.Size,
			WWN:    volume.Spec.WWN,
			Limits: volume.Spec.Limits,
			Encryption: providerapi.EncryptionSpec{
				Type:                providerapi.EncryptionType(volume.Spec.VolumeEncryption.Type),
				EncryptedPassphrase: volume.Spec.VolumeEncryption.EncryptedPassphrase,
			},
			SnapshotSource: snapshotSource,
		},
	}
}

func (r *VolumeReconciler) reconcileEmptyVolume(ctx context.Context, log logr.Logger, volume *providerapi.Volume) error {
	log.V(2).Info("Reconciling empty volume")

	img := buildVolumeImage(volume, nil)

	createdImage, err := createOrGet(ctx, log, r.imageStore, img, "Image created", "Image already exists")
	if err != nil {
		return fmt.Errorf("failed to create or get image: %w", err)
	}

	return r.setVolumeImageRef(ctx, volume, createdImage.ID)
}

func (r *VolumeReconciler) reconcileOSVolume(ctx context.Context, log logr.Logger, volume *providerapi.Volume) error {
	log.V(2).Info("Reconciling OS volume")

	if volume.Spec.Source.OSVolume == nil {
		return fmt.Errorf("OS volume source is nil")
	}
	osVolume := volume.Spec.Source.OSVolume

	// Step 1: Resolve OCI image to get digest
	log.V(2).Info("Resolving OCI image", "ociImageName", osVolume.Name)
	imageSource, err := createImageSource(toPlatform(osVolume.Architecture))
	if err != nil {
		return fmt.Errorf("failed to create image source: %w", err)
	}
	ociImage, err := imageSource.Resolve(ctx, osVolume.Name)
	if err != nil {
		return fmt.Errorf("failed to resolve OCI image %s: %w", osVolume.Name, err)
	}

	// Step 2: Get or create base image
	imageDigest := ociImage.Descriptor().Digest
	baseImageID := BaseImageIDPrefix + imageDigest.Encoded()
	snapshotID := imageDigest.Encoded()
	log.V(2).Info("Using base image", "baseImageId", baseImageID, "snapshotId", snapshotID)
	baseImage, err := r.imageStore.Get(ctx, baseImageID)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("failed to get base image: %w", err)
		}

		ironcoreImage, err := ironcoreimage.ResolveImage(ctx, ociImage)
		if err != nil {
			return fmt.Errorf("failed to resolve ironcore image: %w", err)
		}
		if ironcoreImage.RootFS == nil {
			return fmt.Errorf("ironcore image %s has no rootfs", osVolume.Name)
		}
		imageSize := uint64(ironcoreImage.RootFS.Descriptor().Size)
		rootFSDigest := ironcoreImage.RootFS.Descriptor().Digest

		ref, err := reference.ParseNamed(osVolume.Name)
		if err != nil {
			return fmt.Errorf("failed to parse image reference %s: %w", osVolume.Name, err)
		}
		refDigest, err := reference.WithDigest(ref, rootFSDigest)
		if err != nil {
			return fmt.Errorf("failed to parse image reference %s: %w", osVolume.Name, err)
		}
		refStr := refDigest.String()

		// Create base image
		log.V(1).Info("Creating base image", "baseImageId", baseImageID)
		baseImage = &providerapi.Image{
			Metadata: apiutils.Metadata{
				ID: baseImageID,
			},
			Spec: providerapi.ImageSpec{
				Size:   imageSize,
				WWN:    "", // Base image doesn't need WWN
				Limits: providerapi.Limits{},
				Encryption: providerapi.EncryptionSpec{
					Type: providerapi.EncryptionTypeUnencrypted, // Base images are unencrypted
				},
				Reference:      &refStr,
				SnapshotSource: nil,
			},
		}

		baseImage, err = createOrGet(ctx, log, r.imageStore, baseImage, "Base image created", "Base image already exists, fetching")
		if err != nil {
			return fmt.Errorf("failed to create or get base image: %w", err)
		}
	}

	// Step 3: Check base image state
	switch baseImage.Status.State {
	case providerapi.ImageStatePending:
		log.V(1).Info("Base image is pending, waiting", "baseImageId", baseImage.ID)
		return nil
	case providerapi.ImageStateAvailable:
		log.V(2).Info("Base image is available", "baseImageId", baseImage.ID)
	default:
		return fmt.Errorf("base image %s in unexpected state: %s", baseImage.ID, baseImage.Status.State)
	}

	// Step 4: Get or create snapshot of base image
	snapshot, err := r.snapshotStore.Get(ctx, snapshotID)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("failed to get snapshot %s: %w", snapshotID, err)
		}

		// Create snapshot
		log.V(1).Info("Creating snapshot of base image", "snapshotId", snapshotID, "baseImageId", baseImageID)
		snapshot = &providerapi.Snapshot{
			Metadata: apiutils.Metadata{
				ID: snapshotID,
			},
			Spec: providerapi.SnapshotSpec{
				ImageRef:   baseImageID,
				Protection: providerapi.SnapshotProtectionProtected, // Protection needed for cloning
			},
		}

		snapshot, err = createOrGet(ctx, log, r.snapshotStore, snapshot, "Snapshot created", "Snapshot already exists, fetching")
		if err != nil {
			return fmt.Errorf("failed to create or get snapshot: %w", err)
		}
	}

	// Step 5: Check snapshot state
	switch snapshot.Status.State {
	case providerapi.SnapshotStatePending:
		log.V(1).Info("Snapshot is pending, waiting", "snapshotId", snapshot.ID)
		return nil
	case providerapi.SnapshotStateReady:
		log.V(2).Info("Snapshot is ready", "snapshotId", snapshot.ID)
	case providerapi.SnapshotStateFailed:
		log.V(1).Info("Snapshot failed, recreating", "snapshotId", snapshot.ID)
		if err := r.snapshotStore.Delete(ctx, snapshot.ID); err != nil {
			if !errors.Is(err, store.ErrNotFound) {
				return fmt.Errorf("failed to delete snapshot %s: %w", snapshot.ID, err)
			}
		}
		log.V(2).Info("Deleted failed snapshot", "snapshotId", snapshot.ID)
		return nil
	default:
		return fmt.Errorf("snapshot %s in unexpected state: %s", snapshotID, snapshot.Status.State)
	}

	// Step 6: Create volume's image as clone from snapshot
	volumeImage := buildVolumeImage(volume, &snapshotID)
	log.V(1).Info("Creating volume image from snapshot", "snapshotId", snapshotID, "imageId", volumeImage.ID)

	createdImage, err := createOrGet(ctx, log, r.imageStore, volumeImage, "Volume image created", "Volume image already exists")
	if err != nil {
		return fmt.Errorf("failed to create or get volume image: %w", err)
	}

	// Step 7: Update volume status with ImageRef
	return r.setVolumeImageRef(ctx, volume, createdImage.ID)
}

func (r *VolumeReconciler) reconcileRestoredVolume(ctx context.Context, log logr.Logger, volume *providerapi.Volume) error {
	log.V(2).Info("Reconciling restored volume from snapshot")

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
		log.V(1).Info("Snapshot is pending, waiting", "snapshotId", snapshotID)
		return nil
	case providerapi.SnapshotStateReady:
		log.V(2).Info("Snapshot is ready", "snapshotId", snapshotID)
	case providerapi.SnapshotStateFailed:
		return fmt.Errorf("snapshot %s failed", snapshotID)
	default:
		return fmt.Errorf("snapshot %s in unexpected state: %s", snapshotID, snapshot.Status.State)
	}

	// Create new Image from snapshot
	img := buildVolumeImage(volume, &snapshotID)
	imageID := img.ID
	log.V(1).Info("Creating image from snapshot", "snapshotId", snapshotID, "imageId", imageID)

	createdImage, err := createOrGet(ctx, log, r.imageStore, img, "Image created from snapshot", "Image already exists")
	if err != nil {
		return fmt.Errorf("failed to create or get image: %w", err)
	}

	// Update volume status with ImageRef
	return r.setVolumeImageRef(ctx, volume, createdImage.ID)
}

func (r *VolumeReconciler) deleteVolume(ctx context.Context, log logr.Logger, volume *providerapi.Volume) error {
	if !slices.Contains(volume.Finalizers, VolumeFinalizer) {
		log.V(1).Info("volume has no finalizer: done")
		return nil
	}

	// Delete the volume's Image if it exists
	imageRef := volume.Status.ImageRef
	if imageRef != "" {
		if err := r.imageStore.Delete(ctx, imageRef); err != nil {
			if !errors.Is(err, store.ErrNotFound) {
				return fmt.Errorf("failed to delete image %s: %w", imageRef, err)
			}
			log.V(2).Info("Image already deleted", "imageId", imageRef)
		} else {
			log.V(1).Info("Image deleted", "imageId", imageRef)
		}
	}

	// Note: We do NOT delete base images or snapshots for OS volumes
	// as they may be shared by multiple volumes (golden image pattern).
	// Clean up of unused base images and snapshots can be added later.

	if _, err := removeFinalizer(ctx, r.volumeStore, volume, VolumeFinalizer); store.IgnoreErrNotFound(err) != nil {
		return fmt.Errorf("failed to update volume metadata: %w", err)
	}
	log.V(2).Info("Removed volume finalizer")
	return nil
}

func volumeAccessEqual(a, b *providerapi.VolumeAccess) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.Monitors == b.Monitors && a.Handle == b.Handle && a.User == b.User && a.UserKey == b.UserKey
}
