// SPDX-FileCopyrightText: 2023 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package omap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"sort"
	"sync"
	"time"

	"github.com/ceph/go-ceph/rados"
	"github.com/go-logr/logr"
	utilssync "github.com/ironcore-dev/ceph-provider/internal/sync"
	"github.com/ironcore-dev/ceph-provider/internal/utils"
	apiutils "github.com/ironcore-dev/provider-utils/apiutils/api"
	"github.com/ironcore-dev/provider-utils/storeutils/store"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
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
	IteratorSize   int64
	FieldIndexers  map[string]store.IndexerFunc[E]
}

func New[E apiutils.Object](log logr.Logger, conn *rados.Conn, pool string, opts Options[E]) (*Store[E], error) {
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

	if opts.IteratorSize <= 0 {
		return nil, fmt.Errorf("must specify opts.IteratorSize (must be > 0)")
	}

	indexers := make(map[string]store.IndexerFunc[E], len(opts.FieldIndexers))
	maps.Copy(indexers, opts.FieldIndexers)

	store := &Store[E]{
		idMu: utilssync.NewMutexMap[string](),

		log: log,

		conn:     conn,
		pool:     pool,
		omapName: opts.OmapName,

		iteratorSize: opts.IteratorSize,
		watches:      sets.New[*watch[E]](),

		newFunc:        opts.NewFunc,
		createStrategy: opts.CreateStrategy,

		indexers:   indexers,
		labelIndex: make(map[string]sets.Set[string]),
		fieldIndex: make(map[string]sets.Set[string]),
	}
	if err := store.initializeLabelIndex(); err != nil {
		return nil, fmt.Errorf("failed to initialize label index: %w", err)
	}
	if err := store.initializeFieldIndex(); err != nil {
		return nil, fmt.Errorf("failed to initialize field index: %w", err)
	}
	return store, nil
}

type Store[E apiutils.Object] struct {
	idMu *utilssync.MutexMap[string]

	log logr.Logger

	conn         *rados.Conn
	pool         string
	omapName     string
	iteratorSize int64

	newFunc        func() E
	createStrategy CreateStrategy[E]

	watchesMu sync.RWMutex
	watches   sets.Set[*watch[E]]

	indexers map[string]store.IndexerFunc[E]

	labelIndexMu sync.RWMutex
	labelIndex   map[string]sets.Set[string] // Add label index field (labelKey=labelValue -> Set[objectID])
	fieldIndexMu sync.RWMutex
	fieldIndex   map[string]sets.Set[string] // Add field index field (fieldName=fieldValue -> Set[objectID])

}

// --- Internal Index Helpers ---
func formatIndexKey(key, value string) string {
	return key + "\x00" + value
}

func formatLabel(key, value string) string {
	return formatIndexKey(key, value)
}

func formatField(key, value string) string {
	return formatIndexKey(key, value)
}

// updateLabelIndex updates the index for a single object based on its labels.
func applyIndexDelta(index map[string]sets.Set[string], objID string, oldEntries, newEntries map[string]string) {
	oldSet := sets.New[string]()
	for key := range oldEntries {
		oldSet.Insert(key)
	}

	newSet := sets.New[string]()
	for key := range newEntries {
		newSet.Insert(key)
	}

	for key := range oldSet.Difference(newSet) {
		if ids, ok := index[key]; ok {
			ids.Delete(objID)
			if ids.Len() == 0 {
				delete(index, key)
			}
		}
	}

	for key := range newSet.Difference(oldSet) {
		if _, ok := index[key]; !ok {
			index[key] = sets.New[string]()
		}
		index[key].Insert(objID)
	}
}

func removeFromIndex(index map[string]sets.Set[string], objID string, entries map[string]string) {
	for key := range entries {
		if ids, ok := index[key]; ok {
			ids.Delete(objID)
			if ids.Len() == 0 {
				delete(index, key)
			}
		}
	}
}

func (s *Store[E]) updateLabelIndex(objID string, oldLabels, newLabels map[string]string) {
	oldEntries := make(map[string]string, len(oldLabels))
	for k, v := range oldLabels {
		oldEntries[formatLabel(k, v)] = ""
	}
	newEntries := make(map[string]string, len(newLabels))
	for k, v := range newLabels {
		newEntries[formatLabel(k, v)] = ""
	}

	s.labelIndexMu.Lock()
	defer s.labelIndexMu.Unlock()
	applyIndexDelta(s.labelIndex, objID, oldEntries, newEntries)
}

// removeFromLabelIndex removes an object entirely from the label index.
func (s *Store[E]) removeFromLabelIndex(objID string, labels map[string]string) {
	s.labelIndexMu.Lock()
	defer s.labelIndexMu.Unlock()

	entries := make(map[string]string, len(labels))
	for k, v := range labels {
		entries[formatLabel(k, v)] = ""
	}
	removeFromIndex(s.labelIndex, objID, entries)
}

func (s *Store[E]) updateFieldIndex(objID string, oldFields, newFields map[string]string) {
	oldEntries := make(map[string]string, len(oldFields))
	for k, v := range oldFields {
		oldEntries[formatField(k, v)] = ""
	}
	newEntries := make(map[string]string, len(newFields))
	for k, v := range newFields {
		newEntries[formatField(k, v)] = ""
	}

	s.fieldIndexMu.Lock()
	defer s.fieldIndexMu.Unlock()
	applyIndexDelta(s.fieldIndex, objID, oldEntries, newEntries)
}

func (s *Store[E]) removeFromFieldIndex(objID string, fields map[string]string) {
	s.fieldIndexMu.Lock()
	defer s.fieldIndexMu.Unlock()

	entries := make(map[string]string, len(fields))
	for k, v := range fields {
		entries[formatField(k, v)] = ""
	}
	removeFromIndex(s.fieldIndex, objID, entries)
}

func (s *Store[E]) initializeIndex(indexName string, index map[string]sets.Set[string], indexBuilder func(E) map[string]string) error {
	ioCtx, err := s.conn.OpenIOContext(s.pool)
	if err != nil {
		return fmt.Errorf("failed to open IO context for %s index initialization: %w", indexName, err)
	}
	defer ioCtx.Destroy()

	omapValues, err := ioCtx.GetAllOmapValues(s.omapName, "", "", s.iteratorSize)
	if err != nil && !errors.Is(err, rados.ErrNotFound) {
		if errors.Is(err, rados.ErrNotFound) {
			s.log.V(1).Info("OMAP not found during initial index initialization", "index", indexName)
			return nil
		}
		s.log.Error(err, "Index initialization failed", "index", indexName)
		return fmt.Errorf("failed to get all omap values for %s index initialization: %w", indexName, err)
	}

	for key := range index {
		delete(index, key)
	}
	for k, v := range omapValues {
		obj := s.newFunc()
		if err := json.Unmarshal(v, &obj); err != nil {
			s.log.Error(err, "Failed to unmarshal object during init for index update", "id", k, "index", indexName)
			continue
		}
		for entryKey := range indexBuilder(obj) {
			if _, ok := index[entryKey]; !ok {
				index[entryKey] = sets.New[string]()
			}
			index[entryKey].Insert(obj.GetID())
		}
	}
	s.log.V(1).Info("OMAP index initialized successfully", "index", indexName, "entries", len(index))
	return nil
}

func (s *Store[E]) initializeLabelIndex() error {
	s.labelIndexMu.Lock()
	defer s.labelIndexMu.Unlock()
	return s.initializeIndex("label", s.labelIndex, func(obj E) map[string]string {
		entries := make(map[string]string, len(obj.GetLabels()))
		for k, v := range obj.GetLabels() {
			entries[formatLabel(k, v)] = ""
		}
		return entries
	})
}

func (s *Store[E]) initializeFieldIndex() error {
	s.fieldIndexMu.Lock()
	defer s.fieldIndexMu.Unlock()
	return s.initializeIndex("field", s.fieldIndex, func(obj E) map[string]string {
		entries := make(map[string]string, len(s.indexers))
		for key, idx := range s.indexers {
			value := idx(obj)
			if value == "" {
				continue
			}
			entries[formatField(key, value)] = ""
		}
		return entries
	})
}

func (s *Store[E]) enqueue(evt store.WatchEvent[E]) {
	id := evt.Object.GetID()
	for _, handler := range s.watchHandlers() {
		var toSend store.WatchEvent[E]

		handler.membersMu.Lock()
		switch {
		case evt.Type == store.WatchEventTypeDeleted:
			// Object was deleted; forward only if we were tracking it.
			if handler.members.Has(id) {
				handler.members.Delete(id)
				toSend = evt
			}
		case handler.matches(evt.Object):
			// Object matches the filter; track it and forward the event.
			handler.members.Insert(id)
			toSend = evt
		case handler.members.Has(id):
			// Object no longer matches the filter. Send deleted event.
			handler.members.Delete(id)
			toSend = store.WatchEvent[E]{Type: store.WatchEventTypeDeleted, Object: evt.Object}
		}
		handler.membersMu.Unlock()

		if toSend.Type != "" {
			select {
			case handler.events <- toSend:
			default:
				// TODO: switch to `ctx` to backpressure, if channel size is not enough
				s.log.Info("Dropping watch event, due to full channel", "event", evt)
			}
		}
	}
}

func (s *Store[E]) watchHandlers() []*watch[E] {
	s.watchesMu.RLock()
	defer s.watchesMu.RUnlock()

	return s.watches.UnsortedList()
}

func getOmapValuesByKeys(ioCtx *rados.IOContext, omapName string, keys []string) (map[string][]byte, error) {
	op := rados.CreateReadOp()
	defer op.Release()

	step := op.GetOmapValuesByKeys(keys)

	if err := op.Operate(ioCtx, omapName, rados.OperationNoFlag); err != nil {
		return nil, fmt.Errorf("ceph read operation failed: %w", err)
	}

	result := make(map[string][]byte, len(keys))
	for {
		kv, err := step.Next()
		if err != nil {
			return nil, fmt.Errorf("failed to iterate over ceph read results: %w", err)
		}
		if kv == nil {
			return result, nil
		}
		result[kv.Key] = kv.Value
	}
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

	s.updateLabelIndex(obj.GetID(), nil, obj.GetLabels())
	s.updateFieldIndex(obj.GetID(), nil, s.fieldValues(obj))

	s.enqueue(store.WatchEvent[E]{
		Type:   store.WatchEventTypeCreated,
		Object: obj,
	})

	return obj, nil
}

func (s *Store[E]) fieldValues(obj E) map[string]string {
	if len(s.indexers) == 0 {
		return nil
	}

	values := make(map[string]string, len(s.indexers))
	for key, indexer := range s.indexers {
		value := indexer(obj)
		if value == "" {
			continue
		}
		values[key] = value
	}
	return values
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
		if err := s.delete(ioCtx, id); err != nil {
			return err
		}
		s.removeFromLabelIndex(id, obj.GetLabels())
		s.removeFromFieldIndex(id, s.fieldValues(obj))
		return nil
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

	s.removeFromLabelIndex(id, obj.GetLabels())
	s.removeFromFieldIndex(id, s.fieldValues(obj))
	s.enqueue(store.WatchEvent[E]{
		Type:   store.WatchEventTypeDeleted,
		Object: obj,
	})

	return nil
}

func (s *Store[E]) delete(ioCtx *rados.IOContext, id string) error {
	if err := s.deleteOmapValue(ioCtx, s.omapName, id); err != nil {
		if errors.Is(err, rados.ErrNotFound) {
			return store.ErrNotFound
		}
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

	// Begin OMAP Update Logic
	deleted := false
	oldLabels := oldObj.GetLabels()
	newLabels := obj.GetLabels()

	if obj.GetDeletedAt() != nil && len(obj.GetFinalizers()) == 0 {
		if oldObj.GetResourceVersion() != obj.GetResourceVersion() {
			return utils.Zero[E](), fmt.Errorf("failed to delete object during update: %w", ErrResourceVersionNotLatest)
		}

		if err := s.delete(ioCtx, obj.GetID()); err != nil {
			if !errors.Is(err, store.ErrNotFound) {
				return utils.Zero[E](), fmt.Errorf("failed to delete object from omap during update: %w", err)
			}
		}
		deleted = true
	} else {
		if oldObj.GetResourceVersion() != obj.GetResourceVersion() {
			return utils.Zero[E](), fmt.Errorf("failed to update object: %w", ErrResourceVersionNotLatest)
		}
		obj.IncrementResourceVersion()

		obj, err = s.set(ioCtx, obj)
		if err != nil {
			return utils.Zero[E](), err
		}
	}

	oldFields := s.fieldValues(oldObj)
	newFields := s.fieldValues(obj)

	var eventType store.WatchEventType
	if deleted {
		s.removeFromLabelIndex(obj.GetID(), oldLabels)
		s.removeFromFieldIndex(obj.GetID(), oldFields)
		eventType = store.WatchEventTypeDeleted
	} else {
		s.updateLabelIndex(obj.GetID(), oldLabels, newLabels)
		s.updateFieldIndex(obj.GetID(), oldFields, newFields)
		eventType = store.WatchEventTypeUpdated
	}

	s.enqueue(store.WatchEvent[E]{
		Type:   eventType,
		Object: obj,
	})

	return obj, nil
}

type watch[E apiutils.Object] struct {
	store *Store[E]

	events    chan store.WatchEvent[E]
	opts      store.ListOptions
	membersMu sync.Mutex
	members   sets.Set[string]
}

func (w *watch[E]) matches(obj E) bool {
	return w.store.matchesOptions(obj, w.opts)
}

func (w *watch[E]) Stop() {
	w.store.watchesMu.Lock()
	defer w.store.watchesMu.Unlock()

	w.store.watches.Delete(w)
}

func (w *watch[E]) Events() <-chan store.WatchEvent[E] {
	return w.events
}

func (s *Store[E]) validateFieldSelector(opts store.ListOptions) error {
	if opts.FieldSelector == nil {
		return nil
	}
	for _, req := range opts.FieldSelector.Requirements() {
		if _, ok := s.indexers[req.Field]; !ok {
			return fmt.Errorf("field selector references unindexed field %q", req.Field)
		}
	}
	return nil
}

func (s *Store[E]) matchesOptions(obj E, opts store.ListOptions) bool {
	if opts.LabelSelector != nil && !opts.LabelSelector.Matches(labels.Set(obj.GetLabels())) {
		return false
	}
	if opts.FieldSelector != nil {
		merged := fields.Set{}
		for _, req := range opts.FieldSelector.Requirements() {
			if fn, ok := s.indexers[req.Field]; ok {
				merged[req.Field] = fn(obj)
			}
		}
		if !opts.FieldSelector.Matches(merged) {
			return false
		}
	}
	return true
}

func (s *Store[E]) Watch(ctx context.Context, opts ...store.ListOption) (store.Watch[E], error) {
	listOpts := &store.ListOptions{}
	for _, opt := range opts {
		opt.ApplyToList(listOpts)
	}

	if err := s.validateFieldSelector(*listOpts); err != nil {
		return nil, err
	}

	// List runs before acquiring watchesMu to avoid a deadlock: List→OpenIOContext
	// may interact with idMu, while mutations hold idMu before calling enqueue→watchesMu.RLock.
	// This means a mutation between List and watches.Insert may be missed in
	// members, but we accept that narrow gap.
	existing, err := s.List(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to list existing objects for watch: %w", err)
	}

	members := sets.New[string]()
	for _, obj := range existing {
		members.Insert(obj.GetID())
	}

	s.watchesMu.Lock()
	defer s.watchesMu.Unlock()

	w := &watch[E]{
		store:   s,
		events:  make(chan store.WatchEvent[E], 1024),
		opts:    *listOpts,
		members: members,
	}

	s.watches.Insert(w)

	return w, nil
}

func (s *Store[E]) List(ctx context.Context, opts ...store.ListOption) ([]E, error) {
	listOpts := &store.ListOptions{}
	for _, opt := range opts {
		opt.ApplyToList(listOpts)
	}

	if err := s.validateFieldSelector(*listOpts); err != nil {
		return nil, err
	}

	var labelsMap map[string]string
	if listOpts.LabelSelector != nil {
		reqs, isSelectable := listOpts.LabelSelector.Requirements()
		if !isSelectable {
			return []E{}, nil
		}
		labelsMap = make(map[string]string, len(reqs))
		for _, req := range reqs {
			if req.Operator() != selection.Equals {
				return []E{}, nil
			}
			key := req.Key()
			value, hasValue := listOpts.LabelSelector.RequiresExactMatch(key)
			if !hasValue {
				return []E{}, nil
			}
			labelsMap[key] = value
		}
	}

	var fieldMap map[string]string
	if listOpts.FieldSelector != nil {
		reqs := listOpts.FieldSelector.Requirements()
		fieldMap = make(map[string]string, len(reqs))
		for _, req := range reqs {
			if req.Operator != selection.Equals {
				return []E{}, nil
			}
			value, hasValue := listOpts.FieldSelector.RequiresExactMatch(req.Field)
			if !hasValue {
				return []E{}, nil
			}
			fieldMap[req.Field] = value
		}
	}

	if len(labelsMap) > 0 && len(fieldMap) > 0 {
		labelObjs, err := s.listByLabels(ctx, labelsMap)
		if err != nil {
			return nil, err
		}
		fieldObjs, err := s.listByFieldSelector(ctx, fieldMap)
		if err != nil {
			return nil, err
		}
		objIDs := make(map[string]struct{}, len(fieldObjs))
		for _, obj := range fieldObjs {
			objIDs[obj.GetID()] = struct{}{}
		}
		filtered := make([]E, 0, len(labelObjs))
		for _, obj := range labelObjs {
			if _, ok := objIDs[obj.GetID()]; ok {
				filtered = append(filtered, obj)
			}
		}
		return filtered, nil
	}
	if len(labelsMap) > 0 {
		return s.listByLabels(ctx, labelsMap)
	}
	if len(fieldMap) > 0 {
		return s.listByFieldSelector(ctx, fieldMap)
	}

	ioCtx, err := s.conn.OpenIOContext(s.pool)
	if err != nil {
		return nil, fmt.Errorf("unable to get io context: %w", err)
	}
	defer ioCtx.Destroy()

	omap, err := ioCtx.GetAllOmapValues(s.omapName, "", "", s.iteratorSize)
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
	data, err := getOmapValuesByKeys(ioCtx, s.omapName, []string{id})
	if err != nil {
		if !errors.Is(err, rados.ErrNotFound) {
			return utils.Zero[E](), fmt.Errorf("failed to fetch omap value: %w", err)
		}
		return utils.Zero[E](), fmt.Errorf("object with id %q: %w", id, store.ErrNotFound)
	}
	if data[id] == nil {
		return utils.Zero[E](), fmt.Errorf("object with id %q: %w", id, store.ErrNotFound)
	}

	obj := s.newFunc()
	if err := json.Unmarshal(data[id], obj); err != nil {
		return utils.Zero[E](), fmt.Errorf("failed to unmarshal object: %w", err)
	}

	return obj, nil
}

type sizedIndex struct {
	ids  sets.Set[string]
	size int
}

func (s *Store[E]) listByIndex(ctx context.Context, selector map[string]string, index map[string]sets.Set[string], mu *sync.RWMutex, formatKey func(string, string) string) ([]E, error) {
	selects := make([]sizedIndex, 0, len(selector))
	var intersection sets.Set[string]

	if mu != nil {
		mu.RLock()
		defer mu.RUnlock()
	}
	for key, value := range selector {
		idxKey := formatKey(key, value)
		ids, found := index[idxKey]
		if !found {
			return []E{}, nil
		}
		selects = append(selects, sizedIndex{
			ids:  ids.Clone(),
			size: ids.Len(),
		})
	}

	if len(selects) > 1 {
		sort.Slice(selects, func(i, j int) bool {
			return selects[i].size < selects[j].size
		})
	}

	var isFirst = true
	for _, info := range selects {
		ids := info.ids
		if isFirst {
			intersection = ids
			isFirst = false
		} else {
			intersection = intersection.Intersection(ids)
		}
		if intersection.Len() == 0 {
			return []E{}, nil
		}
	}

	objs := make([]E, 0, intersection.Len())
	for id := range intersection {
		obj, err := s.Get(ctx, id)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			return nil, err
		}
		objs = append(objs, obj)
	}

	return objs, nil
}

func (s *Store[E]) listByLabels(ctx context.Context, labelSelector map[string]string) ([]E, error) {
	return s.listByIndex(ctx, labelSelector, s.labelIndex, &s.labelIndexMu, formatLabel)
}

func (s *Store[E]) listByFieldSelector(ctx context.Context, fieldSelector map[string]string) ([]E, error) {
	return s.listByIndex(ctx, fieldSelector, s.fieldIndex, &s.fieldIndexMu, formatField)
}
