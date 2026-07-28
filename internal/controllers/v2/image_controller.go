// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/ceph/go-ceph/rados"
	librbd "github.com/ceph/go-ceph/rbd"
	"github.com/go-logr/logr"
	providerapi "github.com/ironcore-dev/ceph-provider/api/v2"
	"github.com/ironcore-dev/ceph-provider/internal/encryption"
	"github.com/ironcore-dev/ceph-provider/internal/rater"
	"github.com/ironcore-dev/ceph-provider/internal/round"
	"github.com/ironcore-dev/ceph-provider/internal/utils"
	"github.com/ironcore-dev/provider-utils/eventutils/event"
	"github.com/ironcore-dev/provider-utils/storeutils/store"
	"k8s.io/client-go/util/workqueue"
	"oras.land/oras-go/v2/registry"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/credentials"
)

const (
	LimitMetadataPrefix = "conf_"
	WWNKey              = "wwn"
)

type ImageReconcilerOptions struct {
	Monitors            string
	Client              string
	Pool                string
	PopulatorBufferSize int64
	WorkerSize          int
}

func NewImageReconciler(
	log logr.Logger,
	conn *rados.Conn,
	imageStore store.Store[*providerapi.Image],
	snapshotStore store.Store[*providerapi.Snapshot],
	imageEvents event.Source[*providerapi.Image],
	snapshotEvents event.Source[*providerapi.Snapshot],
	keyEncryption encryption.Encryptor,
	opts ImageReconcilerOptions,
) (*ImageReconciler, error) {
	if conn == nil {
		return nil, fmt.Errorf("must specify conn")
	}

	if imageStore == nil {
		return nil, fmt.Errorf("must specify image store")
	}

	if snapshotStore == nil {
		return nil, fmt.Errorf("must specify snapshot store")
	}

	if imageEvents == nil {
		return nil, fmt.Errorf("must specify image events")
	}

	if snapshotEvents == nil {
		return nil, fmt.Errorf("must specify snapshot events")
	}

	if keyEncryption == nil {
		return nil, fmt.Errorf("must specify key encryption")
	}

	if opts.Pool == "" {
		return nil, fmt.Errorf("must specify pool")
	}

	if opts.Monitors == "" {
		return nil, fmt.Errorf("must specify monitors")
	}

	if opts.Client == "" {
		return nil, fmt.Errorf("must specify ceph client")
	}

	if opts.PopulatorBufferSize == 0 {
		opts.PopulatorBufferSize = 5 * 1024 * 1024
	}

	if opts.WorkerSize == 0 {
		opts.WorkerSize = 15
	}

	return &ImageReconciler{
		log:                 log,
		conn:                conn,
		queue:               workqueue.NewTypedRateLimitingQueue[string](workqueue.DefaultTypedControllerRateLimiter[string]()),
		imageStore:          imageStore,
		snapshotStore:       snapshotStore,
		imageEvents:         imageEvents,
		snapshotEvents:      snapshotEvents,
		monitors:            opts.Monitors,
		client:              opts.Client,
		pool:                opts.Pool,
		keyEncryption:       keyEncryption,
		populatorBufferSize: opts.PopulatorBufferSize,
		workerSize:          opts.WorkerSize,
	}, nil
}

type ImageReconciler struct {
	log  logr.Logger
	conn *rados.Conn

	queue workqueue.TypedRateLimitingInterface[string]

	imageStore    store.Store[*providerapi.Image]
	snapshotStore store.Store[*providerapi.Snapshot]

	imageEvents    event.Source[*providerapi.Image]
	snapshotEvents event.Source[*providerapi.Snapshot]

	monitors string
	client   string
	pool     string

	keyEncryption       encryption.Encryptor
	populatorBufferSize int64

	workerSize int
}

func (r *ImageReconciler) Start(ctx context.Context) error {
	log := r.log

	imgEventReg, err := r.imageEvents.AddHandler(event.HandlerFunc[*providerapi.Image](func(evt event.Event[*providerapi.Image]) {
		r.queue.Add(evt.Object.ID)
	}))
	if err != nil {
		return err
	}
	defer func() {
		_ = r.imageEvents.RemoveHandler(imgEventReg)
	}()

	snapEventReg, err := r.snapshotEvents.AddHandler(event.HandlerFunc[*providerapi.Snapshot](func(evt event.Event[*providerapi.Snapshot]) {
		if evt.Type != event.TypeUpdated && evt.Type != event.TypeDeleted {
			return
		}
		if evt.Object.Status.State != providerapi.SnapshotStateReady &&
			evt.Object.Status.State != providerapi.SnapshotStateFailed &&
			evt.Object.DeletedAt == nil {
			return
		}
		r.requeueImagesForSnapshot(ctx, evt.Object.ID)
	}))
	if err != nil {
		return err
	}
	defer func() {
		_ = r.snapshotEvents.RemoveHandler(snapEventReg)
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

func (r *ImageReconciler) requeueImagesForSnapshot(ctx context.Context, snapshotID string) {
	images, err := r.imageStore.List(ctx, store.MatchingFields{providerapi.ImageSpecSnapshotSourceField: snapshotID})
	if err != nil {
		r.log.Error(err, "failed to list images for snapshot event requeue")
		return
	}
	for _, img := range images {
		r.queue.Add(img.ID)
	}
}

func (r *ImageReconciler) processNextWorkItem(ctx context.Context, log logr.Logger) bool {
	id, shutdown := r.queue.Get()
	if shutdown {
		return false
	}
	defer r.queue.Done(id)

	log = log.WithValues("imageId", id)
	ctx = logr.NewContext(ctx, log)

	if err := r.reconcileImage(ctx, id); err != nil {
		log.Error(err, "failed to reconcile image")
		r.queue.AddRateLimited(id)
		return true
	}

	r.queue.Forget(id)
	return true
}

const (
	imageFinalizer = "image"
)

func (r *ImageReconciler) reconcileImage(ctx context.Context, id string) error {
	log := logr.FromContextOrDiscard(ctx)

	log.V(2).Info("Get image from store")
	image, err := r.imageStore.Get(ctx, id)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("failed to fetch image from store: %w", err)
		}
		return nil
	}

	if image.DeletedAt != nil {
		if err := r.deleteImage(ctx, log, image); err != nil {
			return fmt.Errorf("failed to delete image: %w", err)
		}
		return nil
	}

	if !slices.Contains(image.Finalizers, imageFinalizer) {
		image.Finalizers = append(image.Finalizers, imageFinalizer)
		if _, err := r.imageStore.Update(ctx, image); err != nil {
			return fmt.Errorf("failed to set finalizers: %w", err)
		}
		return nil
	}

	ioCtx, err := r.conn.OpenIOContext(r.pool)
	if err != nil {
		return fmt.Errorf("unable to get rados io context: %w", err)
	}
	defer ioCtx.Destroy()

	rbdImage, err := librbd.OpenImage(ioCtx, image.ID, librbd.NoSnapshot)
	if err != nil {
		if !errors.Is(err, librbd.ErrNotFound) {
			return fmt.Errorf("failed to open RBD image: %w", err)
		}
		log.V(2).Info("RBD image not found, creating")
		if ok, err := r.createImage(ctx, ioCtx, log, image); err != nil {
			return fmt.Errorf("failed to create RBD image: %w", err)
		} else if !ok {
			// Image creation is not yet complete
			return nil
		}
		rbdImage, err = librbd.OpenImage(ioCtx, image.ID, librbd.NoSnapshot)
		if err != nil {
			return fmt.Errorf("failed to open RBD image after creation: %w", err)
		}
	}
	defer closeImage(log, rbdImage)

	// TODO: The if condition here is not a sufficient check for population.
	// Multiple workers could run into this and start populating in parallel
	if image.Status.State == providerapi.ImageStatePending && image.Spec.Reference != nil {
		// TODO: Run populate asynchronously
		if err := populateImage(ctx, *image.Spec.Reference, rbdImage, r.populatorBufferSize); err != nil {
			return fmt.Errorf("failed to populate image: %w", err)
		}
	}

	if image.Spec.SnapshotSource != nil {
		snapshotID := *image.Spec.SnapshotSource
		snapshot, err := r.snapshotStore.Get(ctx, snapshotID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("failed to get source snapshot from store: %w", err)
		}
		// Flatten images if their snapshot source is deleted
		if snapshot == nil || snapshot.DeletedAt != nil {
			if _, err := rbdImage.GetParent(); err != nil {
				if !errors.Is(err, librbd.ErrNotFound) {
					return fmt.Errorf("failed to get RBD parent snapshot: %w", err)
				}
				log.V(2).Info("RBD image is flattened", "snapshotId", snapshotID)
			} else {
				log.V(1).Info("Flattening RBD image", "snapshotId", snapshotID)
				// TODO: Run flatten asynchronously
				if err := rbdImage.Flatten(); err != nil {
					return fmt.Errorf("failed to flatten RBD image: %w", err)
				}
				log.V(1).Info("RBD image successfully flattened", "snapshotId", snapshotID)
			}

			// Remove finalizer from source snapshot to signal the image has been flattened,
			// so the image is no longer connected to the snapshot
			if snapshot != nil {
				sourceSnapshotFinalizer := fmt.Sprintf("%s/%s", imageFinalizer, image.ID)
				if slices.Contains(snapshot.Finalizers, sourceSnapshotFinalizer) {
					snapshot.Finalizers = utils.DeleteSliceElement(snapshot.Finalizers, sourceSnapshotFinalizer)
					if _, err := r.snapshotStore.Update(ctx, snapshot); err != nil {
						return fmt.Errorf("failed to set finalizers on source snapshot: %w", err)
					}
					log.V(2).Info("Removed finalizer from source snapshot", "snapshotId", snapshot.ID)
				}
			}
		}
	} else {
		if parent, err := rbdImage.GetParent(); err != nil {
			if !errors.Is(err, librbd.ErrNotFound) {
				return fmt.Errorf("failed to get RBD image parent info: %w", err)
			}
		} else {
			log.V(1).Info("Image has no snapshot source configured, but has a parent in Ceph. Flattening image", "snapshotId", parent.Snap.SnapName)
			// TODO: Run flatten asynchronously
			if err := rbdImage.Flatten(); err != nil {
				return fmt.Errorf("failed to flatten image: %w", err)
			}
			log.V(1).Info("Image successfully flattened", "snapshotId", parent.Snap.SnapName)
		}
	}

	if image.Status.Encryption != providerapi.EncryptionStateHeaderSet && image.Spec.Encryption.Type == providerapi.EncryptionTypeEncrypted {
		passphrase, err := r.keyEncryption.Decrypt(image.Spec.Encryption.EncryptedPassphrase)
		if err != nil {
			return fmt.Errorf("failed to decrypt passphrase: %w", err)
		}

		if err := rbdImage.EncryptionFormat(librbd.EncryptionOptionsLUKS2{
			Alg:        librbd.EncryptionAlgorithmAES256,
			Passphrase: passphrase,
		}); err != nil {
			return fmt.Errorf("failed to set encryption format: %w", err)
		}
		image.Status.Encryption = providerapi.EncryptionStateHeaderSet
		if _, err = r.imageStore.Update(ctx, image); err != nil {
			return fmt.Errorf("failed to update image metadata: %w", err)
		}
		return nil
	}

	metadata, err := rbdImage.ListMetadata()
	if err != nil {
		return fmt.Errorf("failed to list image metadata: %w", err)
	}

	if wwn, ok := metadata[WWNKey]; !ok || wwn != image.Spec.WWN {
		if err := rbdImage.SetMetadata(WWNKey, image.Spec.WWN); err != nil {
			return fmt.Errorf("failed to set wwn (%s): %w", image.Spec.WWN, err)
		}
	}

	for limit, value := range image.Spec.Limits {
		metaKey := fmt.Sprintf("%s%s", LimitMetadataPrefix, limit)
		metaVal := strconv.FormatInt(value, 10)

		if actualVal, ok := metadata[metaKey]; !ok || actualVal != metaVal {
			if err := rbdImage.SetMetadata(metaKey, metaVal); err != nil {
				return fmt.Errorf("failed to set limit (%s): %w", metaKey, err)
			}
		}
	}

	// TODO: Think about caching the ceph auth key instead of re-fetching it on every reconcile loop.
	user, key, err := fetchAuth(log, r.client, r.conn)
	if err != nil {
		return fmt.Errorf("failed to fetch credentials: %w", err)
	}

	newAccess := &providerapi.ImageAccess{
		Monitors: r.monitors,
		Handle:   fmt.Sprintf("%s/%s", r.pool, image.ID),
		User:     user,
		UserKey:  key,
	}
	newSize := round.OffBytes(image.Spec.Size)

	needsUpdate := image.Status.State != providerapi.ImageStateAvailable ||
		image.Status.Size != newSize ||
		!equalAccess(image.Status.Access, newAccess)

	if !needsUpdate {
		return nil
	}

	image.Status.State = providerapi.ImageStateAvailable
	image.Status.Access = newAccess
	image.Status.Size = newSize

	if _, err = r.imageStore.Update(ctx, image); err != nil {
		return fmt.Errorf("failed to update image metadata: %w", err)
	}
	return nil
}

func (r *ImageReconciler) createImage(ctx context.Context, ioCtx *rados.IOContext, log logr.Logger, image *providerapi.Image) (bool, error) {
	options := librbd.NewRbdImageOptions()
	defer options.Destroy()
	if err := options.SetString(librbd.ImageOptionDataPool, r.pool); err != nil {
		return false, fmt.Errorf("failed to set data pool: %w", err)
	}

	// Create empty image if no snapshot source is given
	if image.Spec.SnapshotSource == nil {
		if err := librbd.CreateImage(ioCtx, image.ID, round.OffBytes(image.Spec.Size), options); err != nil {
			if !errors.Is(err, librbd.ErrExist) {
				return false, fmt.Errorf("failed to create image: %w", err)
			}
			log.V(2).Info("RBD image already exists")
		} else {
			log.V(1).Info("Created RBD image")
		}
		return true, nil
	}

	snapshotRef := *image.Spec.SnapshotSource
	snapshot, err := r.snapshotStore.Get(ctx, snapshotRef)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return false, fmt.Errorf("source snapshot '%s' not found: %w", snapshotRef, err)
		}
		return false, fmt.Errorf("failed to get source snapshot: %w", err)
	}

	if snapshot.Status.Size > int64(image.Spec.Size) {
		return false, fmt.Errorf("image size is smaller than snapshot size: (%d < %d)", image.Spec.Size, snapshot.Status.Size)
	}

	switch snapshot.Status.State {
	case providerapi.SnapshotStatePending:
		log.V(1).Info("Snapshot is pending, waiting", "snapshotId", snapshot.ID)
		return false, nil
	case providerapi.SnapshotStateReady:
		log.V(2).Info("Source snapshot is available", "snapshotId", snapshot.ID)
	case providerapi.SnapshotStateFailed:
		return false, fmt.Errorf("snapshot %s is failed", snapshotRef)
	default:
		return false, fmt.Errorf("snapshot %s is in unknown state '%s'", snapshotRef, snapshot.Status.State)
	}

	parentImage, err := r.imageStore.Get(ctx, snapshot.Spec.ImageRef)
	if err != nil {
		return false, fmt.Errorf("failed to get parent image of snapshot %s: %w", snapshot.ID, err)
	}

	// Put finalizer on source snapshot to allow the image to be flattened before snapshot deletion
	sourceSnapshotFinalizer := fmt.Sprintf("%s/%s", imageFinalizer, image.ID)
	if !slices.Contains(snapshot.Finalizers, sourceSnapshotFinalizer) {
		snapshot.Finalizers = append(snapshot.Finalizers, sourceSnapshotFinalizer)
		if _, err := r.snapshotStore.Update(ctx, snapshot); err != nil {
			return false, fmt.Errorf("failed to set finalizers on source snapshot: %w", err)
		}
	}
	log.V(2).Info("Added finalizer to source snapshot", "snapshotId", snapshot.ID)

	if err := librbd.CloneImage(ioCtx, parentImage.ID, snapshot.ID, ioCtx, image.ID, options); err != nil {
		if !errors.Is(err, librbd.ErrExist) {
			return false, fmt.Errorf("failed to clone image: %w", err)
		}
		log.V(2).Info("RBD clone already exists", "snapshotId", snapshot.ID)
	} else {
		log.V(1).Info("Cloned RBD image from snapshot", "snapshotId", snapshot.ID)
	}

	return true, nil
}

func (r *ImageReconciler) deleteImage(ctx context.Context, log logr.Logger, image *providerapi.Image) error {
	if !slices.Contains(image.Finalizers, imageFinalizer) {
		log.V(2).Info("Image has no finalizer: done")
		return nil
	}

	if len(image.Finalizers) > 1 {
		log.V(2).Info("Image has too many finalizers: waiting")
		return nil
	}

	ioCtx, err := r.conn.OpenIOContext(r.pool)
	if err != nil {
		return fmt.Errorf("unable to get rados io context: %w", err)
	}
	defer ioCtx.Destroy()

	if err := librbd.RemoveImage(ioCtx, image.ID); err != nil && !errors.Is(err, librbd.ErrNotFound) {
		return fmt.Errorf("failed to remove image %s: %w", image.ID, err)
	}
	log.V(1).Info("RBD image removed")

	// Remove finalizer from source snapshot. The RBD child image has been deleted,
	// so it should not block snapshot deletion anymore
	if image.Spec.SnapshotSource != nil {
		snapshot, err := r.snapshotStore.Get(ctx, *image.Spec.SnapshotSource)
		if err != nil {
			if !errors.Is(err, store.ErrNotFound) {
				return fmt.Errorf("failed to get source snapshot from store: %w", err)
			}
			log.V(2).Info("Source snapshot already deleted", "snapshotId", *image.Spec.SnapshotSource)
		} else {
			sourceSnapshotFinalizer := fmt.Sprintf("%s/%s", imageFinalizer, image.ID)
			if slices.Contains(snapshot.Finalizers, sourceSnapshotFinalizer) {
				snapshot.Finalizers = utils.DeleteSliceElement(snapshot.Finalizers, sourceSnapshotFinalizer)
				if _, err := r.snapshotStore.Update(ctx, snapshot); err != nil {
					return fmt.Errorf("failed to set finalizers on source snapshot: %w", err)
				}
				log.V(2).Info("Removed finalizer from source snapshot", "snapshotId", snapshot.ID)
			}
		}
	}

	image.Finalizers = utils.DeleteSliceElement(image.Finalizers, imageFinalizer)
	if _, err := r.imageStore.Update(ctx, image); store.IgnoreErrNotFound(err) != nil {
		return fmt.Errorf("failed to update image metadata: %w", err)
	}
	log.V(2).Info("Removed image finalizer")
	return nil
}

type fetchAuthResponse struct {
	Key string `json:"key"`
}

func fetchAuth(log logr.Logger, client string, conn *rados.Conn) (string, string, error) {
	cmd, err := json.Marshal(map[string]string{
		"prefix": "auth get-key",
		"entity": client,
		"format": "json",
	})
	if err != nil {
		return "", "", fmt.Errorf("unable to marshal mon command: %w", err)
	}

	data, _, err := conn.MonCommand(cmd)
	if err != nil {
		return "", "", fmt.Errorf("failed to execute mon command: %w", err)
	}

	response := fetchAuthResponse{}
	if err := json.Unmarshal(data, &response); err != nil {
		return "", "", fmt.Errorf("unable to unmarshal mon response: %w", err)
	}

	return strings.TrimPrefix(client, "client."), response.Key, nil
}

func equalAccess(a, b *providerapi.ImageAccess) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Monitors == b.Monitors &&
		a.Handle == b.Handle &&
		a.User == b.User &&
		a.UserKey == b.UserKey
}

func populateImage(ctx context.Context, imageRef string, rbdImage *librbd.Image, bufferSize int64) error {
	parsed, err := registry.ParseReference(imageRef)
	if err != nil {
		return fmt.Errorf("could not parse image reference %q: %w", imageRef, err)
	}

	dockerStore, err := credentials.NewStoreFromDocker(credentials.StoreOptions{})
	if err != nil {
		return fmt.Errorf("could not create credential store: %w", err)
	}

	client := auth.DefaultClient
	client.Credential = credentials.Credential(dockerStore)
	repo := &remote.Repository{
		Reference: parsed,
		Client:    client,
	}

	_, content, err := repo.Blobs().FetchReference(ctx, parsed.Reference)
	if err != nil {
		return fmt.Errorf("could not fetch blob %q: %w", imageRef, err)
	}
	throughputReader := rater.NewRater(content)
	buffer := make([]byte, bufferSize)
	if _, err := io.CopyBuffer(rbdImage, throughputReader, buffer); err != nil {
		return fmt.Errorf("failed to copy image contents onto RBD image: %w", err)
	}

	return nil
}
