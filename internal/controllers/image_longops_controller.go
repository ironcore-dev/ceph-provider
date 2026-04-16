// SPDX-FileCopyrightText: 2023 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/ceph/go-ceph/rados"
	librbd "github.com/ceph/go-ceph/rbd"
	"github.com/go-logr/logr"
	providerapi "github.com/ironcore-dev/ceph-provider/api"
	"github.com/ironcore-dev/provider-utils/eventutils/event"
	"github.com/ironcore-dev/provider-utils/storeutils/store"
	"k8s.io/client-go/util/workqueue"
)

// ImageLongOpsReconciler processes long-running image operations. It is responsible
// for deletion-time flattening of children so that the fast ImageReconciler does not block.
type ImageLongOpsReconcilerOptions struct {
	Pool string

	// FlattenWorkerSize is the number of concurrent workers processing image flatten operations.
	FlattenWorkerSize int
}

func NewImageLongOpsReconciler(
	log logr.Logger,
	conn *rados.Conn,
	images store.Store[*providerapi.Image],
	events event.Source[*providerapi.Image],
	opts ImageLongOpsReconcilerOptions,
) (*ImageLongOpsReconciler, error) {
	if conn == nil {
		return nil, fmt.Errorf("must specify conn")
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
	if opts.FlattenWorkerSize == 0 {
		opts.FlattenWorkerSize = 5
	}

	return &ImageLongOpsReconciler{
		log:            log,
		conn:           conn,
		images:         images,
		events:         events,
		pool:           opts.Pool,
		flattenQueue:   workqueue.NewTypedRateLimitingQueue[string](workqueue.DefaultTypedControllerRateLimiter[string]()),
		flattenWorkers: opts.FlattenWorkerSize,
	}, nil
}

type ImageLongOpsReconciler struct {
	log    logr.Logger
	conn   *rados.Conn
	images store.Store[*providerapi.Image]
	events event.Source[*providerapi.Image]

	pool string

	flattenQueue   workqueue.TypedRateLimitingInterface[string]
	flattenWorkers int
}

func (r *ImageLongOpsReconciler) Start(ctx context.Context) error {
	log := r.log

	reg, err := r.events.AddHandler(event.HandlerFunc[*providerapi.Image](func(evt event.Event[*providerapi.Image]) {
		img := evt.Object
		if img.DeletedAt == nil {
			return
		}
		if img.Status.DeletionPhase == nil {
			return
		}
		if *img.Status.DeletionPhase != providerapi.ImageDeletionPhaseFlatteningChildren {
			return
		}
		r.flattenQueue.Add(img.ID)
	}))
	if err != nil {
		return err
	}
	defer func() {
		_ = r.events.RemoveHandler(reg)
	}()

	go func() {
		<-ctx.Done()
		r.flattenQueue.ShutDown()
	}()

	var wg sync.WaitGroup
	for i := 0; i < r.flattenWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.processFlattenQueue(ctx, log)
		}()
	}

	wg.Wait()
	return nil
}

func (r *ImageLongOpsReconciler) processFlattenQueue(ctx context.Context, log logr.Logger) {
	for {
		item, shutdown := r.flattenQueue.Get()
		if shutdown {
			return
		}
		imageID := item

		err := r.processFlattenOperation(ctx, imageID)
		if err != nil {
			log.Error(err, "image flatten operation failed", "imageId", imageID)
			r.flattenQueue.AddRateLimited(imageID)
		} else {
			r.flattenQueue.Forget(imageID)
		}
		r.flattenQueue.Done(imageID)
	}
}

func (r *ImageLongOpsReconciler) processFlattenOperation(ctx context.Context, imageID string) error {
	log := logr.FromContextOrDiscard(ctx).WithValues("imageId", imageID)
	ctx = logr.NewContext(ctx, log)

	imgObj, err := r.images.Get(ctx, imageID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("failed to get image: %w", err)
	}

	if imgObj.DeletedAt == nil || imgObj.Status.DeletionPhase == nil || *imgObj.Status.DeletionPhase != providerapi.ImageDeletionPhaseFlatteningChildren {
		return nil
	}

	ioCtx, err := r.conn.OpenIOContext(r.pool)
	if err != nil {
		return fmt.Errorf("failed to open IO context: %w", err)
	}
	defer ioCtx.Destroy()

	rbdImg, err := openImage(ioCtx, ImageIDToRBDID(imgObj.ID))
	if err != nil {
		if errors.Is(err, librbd.ErrNotFound) {
			// Parent already gone; clear phase so image delete can complete.
			imgObj.Status.DeletionPhase = nil
			if _, err := r.images.Update(ctx, imgObj); store.IgnoreErrNotFound(err) != nil {
				return fmt.Errorf("failed to clear deletion phase: %w", err)
			}
			return nil
		}
		return fmt.Errorf("failed to open image: %w", err)
	}
	defer closeImage(log, rbdImg)

	pools, childImgs, err := rbdImg.ListChildren()
	if err != nil {
		return fmt.Errorf("failed to list children: %w", err)
	}

	if len(childImgs) != 0 {
		log.V(2).Info("Flattening child image", "child", childImgs[0], "remaining", len(childImgs)-1)
		if err := flattenImage(log, r.conn, pools[0], childImgs[0]); err != nil {
			return fmt.Errorf("failed to flatten child %s: %w", childImgs[0], err)
		}
		r.flattenQueue.Add(imageID)
		return nil
	}

	// Done: clear phase and let normal image reconciler continue deletion steps.
	imgObj.Status.DeletionPhase = nil
	if _, err := r.images.Update(ctx, imgObj); store.IgnoreErrNotFound(err) != nil {
		return fmt.Errorf("failed to clear deletion phase: %w", err)
	}

	return nil
}
