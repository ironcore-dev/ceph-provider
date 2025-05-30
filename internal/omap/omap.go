// SPDX-FileCopyrightText: 2023 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package omap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ceph/go-ceph/rados"
	utilssync "github.com/ironcore-dev/ceph-provider/internal/sync"
	"github.com/ironcore-dev/ceph-provider/internal/utils"
	apiutils "github.com/ironcore-dev/provider-utils/apiutils/api"
	"github.com/ironcore-dev/provider-utils/storeutils/store"
	"k8s.io/apimachinery/pkg/util/sets"
)

type CreateStrategy[E apiutils.Object] interface {
	PrepareForCreate(obj E)
}

var ErrResourceVersionNotLatest = errors.New("resourceVersion is not latest")

type Options[E apiutils.Object] struct {
	OmapName       string
	NewFunc        func() E
	CreateStrategy CreateStrategy[E]
}

func New[E apiutils.Object](conn *rados.Conn, pool string, opts Options[E]) (*Store[E], error) {
	if conn == nil {
		return nil, fmt.Errorf("must specify conn")
	}

	if pool == "" {
		return nil, fmt.Errorf("must specify pool")
	}

	if opts.OmapName == "" {
		return nil, fmt.Errorf("must specify opts.OmapName")
	}

	if opts.NewFunc == nil {
		return nil, fmt.Errorf("must specify opts.NewFunc")
	}

	return &Store[E]{
		idMu: utilssync.NewMutexMap[string](),

		conn:     conn,
		pool:     pool,
		omapName: opts.OmapName,

		watches: sets.New[*watch[E]](),

		newFunc:        opts.NewFunc,
		createStrategy: opts.CreateStrategy,
	}, nil
}

type Store[E apiutils.Object] struct {
	idMu *utilssync.MutexMap[string]

	conn     *rados.Conn
	pool     string
	omapName string

	newFunc        func() E
	createStrategy CreateStrategy[E]

	watchesMu sync.RWMutex
	watches   sets.Set[*watch[E]]
}

func (s *Store[E]) enqueue(evt store.WatchEvent[E]) {
	for _, handler := range s.watchHandlers() {
		select {
		case handler.events <- evt:
		default:
		}
	}
}

func (s *Store[E]) watchHandlers() []*watch[E] {
	s.watchesMu.RLock()
	defer s.watchesMu.RUnlock()

	return s.watches.UnsortedList()
}

func (s *Store[E]) getSingleOmapValue(ioCtx *rados.IOContext, omapName, key string) ([]byte, error) {
	omap, err := ioCtx.GetAllOmapValues(omapName, "", key, 10)
	if err != nil {
		return nil, err
	}

	value, ok := omap[key]
	if !ok {
		return nil, rados.ErrNotFound
	}

	return value, nil
}

func (s *Store[E]) deleteOmapValue(ioCtx *rados.IOContext, omapName, key string) error {
	if err := ioCtx.RmOmapKeys(omapName, []string{key}); err != nil {
		return fmt.Errorf("unable to delete mapping omap value: %w", err)
	}

	return nil
}

func (s *Store[E]) setOmapValue(ioCtx *rados.IOContext, omapName, key string, value []byte) error {
	if err := ioCtx.SetOmap(omapName, map[string][]byte{
		key: value,
	}); err != nil {
		return fmt.Errorf("unable to set omap values: %w", err)
	}

	return nil
}

func (s *Store[E]) Create(ctx context.Context, obj E) (E, error) {
	s.idMu.Lock(obj.GetID())
	defer s.idMu.Unlock(obj.GetID())

	ioCtx, err := s.conn.OpenIOContext(s.pool)
	if err != nil {
		return utils.Zero[E](), fmt.Errorf("unable to get io context: %w", err)
	}
	defer ioCtx.Destroy()

	_, err = s.get(ioCtx, obj.GetID())
	switch {
	case err == nil:
		return utils.Zero[E](), fmt.Errorf("object with id %q %w", obj.GetID(), store.ErrAlreadyExists)
	case errors.Is(err, store.ErrNotFound):
	default:
		return utils.Zero[E](), fmt.Errorf("failed to get object with id %q %w", obj.GetID(), err)
	}

	if s.createStrategy != nil {
		s.createStrategy.PrepareForCreate(obj)
	}

	obj.SetCreatedAt(time.Now())
	obj.IncrementResourceVersion()

	obj, err = s.set(ioCtx, obj)
	if err != nil {
		return utils.Zero[E](), err
	}

	s.enqueue(store.WatchEvent[E]{
		Type:   store.WatchEventTypeCreated,
		Object: obj,
	})

	return obj, nil
}

func (s *Store[E]) Delete(ctx context.Context, id string) error {
	s.idMu.Lock(id)
	defer s.idMu.Unlock(id)

	ioCtx, err := s.conn.OpenIOContext(s.pool)
	if err != nil {
		return fmt.Errorf("unable to get io context: %w", err)
	}
	defer ioCtx.Destroy()

	obj, err := s.get(ioCtx, id)
	if err != nil {
		return err
	}

	if len(obj.GetFinalizers()) == 0 {
		return s.delete(ioCtx, id)
	}

	if obj.GetDeletedAt() != nil {
		return nil
	}

	now := time.Now()
	obj.SetDeletedAt(&now)
	obj.IncrementResourceVersion()

	if _, err := s.set(ioCtx, obj); err != nil {
		return fmt.Errorf("failed to set object metadata: %w", err)
	}

	s.enqueue(store.WatchEvent[E]{
		Type:   store.WatchEventTypeDeleted,
		Object: obj,
	})

	return nil
}

func (s *Store[E]) delete(ioCtx *rados.IOContext, id string) error {
	if err := s.deleteOmapValue(ioCtx, s.omapName, id); err != nil {
		return fmt.Errorf("failed to delete object from omap: %w", err)
	}
	return nil
}

func (s *Store[E]) Get(ctx context.Context, id string) (E, error) {
	ioCtx, err := s.conn.OpenIOContext(s.pool)
	if err != nil {
		return utils.Zero[E](), fmt.Errorf("unable to get io context: %w", err)
	}
	defer ioCtx.Destroy()

	return s.get(ioCtx, id)
}

func (s *Store[E]) Update(ctx context.Context, obj E) (E, error) {
	s.idMu.Lock(obj.GetID())
	defer s.idMu.Unlock(obj.GetID())

	ioCtx, err := s.conn.OpenIOContext(s.pool)
	if err != nil {
		return utils.Zero[E](), fmt.Errorf("unable to get io context: %w", err)
	}
	defer ioCtx.Destroy()

	oldObj, err := s.get(ioCtx, obj.GetID())
	if err != nil {
		return utils.Zero[E](), err
	}

	if obj.GetDeletedAt() != nil && len(obj.GetFinalizers()) == 0 {
		if err := s.delete(ioCtx, obj.GetID()); err != nil {
			return utils.Zero[E](), fmt.Errorf("failed to delete object metadata: %w", err)
		}
		return obj, nil
	}

	if oldObj.GetResourceVersion() != obj.GetResourceVersion() {
		return utils.Zero[E](), fmt.Errorf("failed to update object: %w", ErrResourceVersionNotLatest)
	}
	obj.IncrementResourceVersion()

	obj, err = s.set(ioCtx, obj)
	if err != nil {
		return utils.Zero[E](), err
	}

	s.enqueue(store.WatchEvent[E]{
		Type:   store.WatchEventTypeUpdated,
		Object: obj,
	})

	return obj, nil
}

type watch[E apiutils.Object] struct {
	store *Store[E]

	events chan store.WatchEvent[E]
}

func (w *watch[E]) Stop() {
	w.store.watchesMu.Lock()
	defer w.store.watchesMu.Unlock()

	w.store.watches.Delete(w)
}

func (w *watch[E]) Events() <-chan store.WatchEvent[E] {
	return w.events
}

func (s *Store[E]) Watch(ctx context.Context) (store.Watch[E], error) {
	s.watchesMu.Lock()
	defer s.watchesMu.Unlock()

	w := &watch[E]{
		store:  s,
		events: make(chan store.WatchEvent[E]),
	}

	s.watches.Insert(w)

	return w, nil
}

func (s *Store[E]) List(ctx context.Context) ([]E, error) {
	ioCtx, err := s.conn.OpenIOContext(s.pool)
	if err != nil {
		return nil, fmt.Errorf("unable to get io context: %w", err)
	}
	defer ioCtx.Destroy()

	omap, err := ioCtx.GetAllOmapValues(s.omapName, "", "", 10)
	if err != nil {
		if errors.Is(err, rados.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}

	var objs []E
	for _, v := range omap {
		obj := s.newFunc()
		if err := json.Unmarshal(v, &obj); err != nil {
			return nil, fmt.Errorf("failed to unmarshal object: %w", err)
		}

		objs = append(objs, obj)
	}

	return objs, nil
}

func (s *Store[E]) set(ioCtx *rados.IOContext, obj E) (E, error) {
	data, err := json.Marshal(obj)
	if err != nil {
		return utils.Zero[E](), fmt.Errorf("failed to marshal obj: %w", err)
	}

	if err := s.setOmapValue(ioCtx, s.omapName, obj.GetID(), data); err != nil {
		return utils.Zero[E](), fmt.Errorf("failed to put os object mapping: %w", err)
	}

	return obj, nil
}

func (s *Store[E]) get(ioCtx *rados.IOContext, id string) (E, error) {
	data, err := s.getSingleOmapValue(ioCtx, s.omapName, id)
	if err != nil {
		if !errors.Is(err, rados.ErrNotFound) {
			return utils.Zero[E](), fmt.Errorf("failed to fetch omap value: %w", err)
		}

		return utils.Zero[E](), fmt.Errorf("object with id %q %w", id, store.ErrNotFound)
	}

	obj := s.newFunc()
	if err := json.Unmarshal(data, obj); err != nil {
		return utils.Zero[E](), fmt.Errorf("failed to unmarshal object: %w", err)
	}

	return obj, nil
}
