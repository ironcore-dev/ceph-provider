// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package monitors

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

const informerResyncPeriod = 10 * time.Minute

// ErrMonitorListChanged is returned by Watcher.Start when the ConfigMap parses
// to a CSV other than Baseline. The process exits so the next start connects
// with the new list.
var ErrMonitorListChanged = errors.New("ceph monitor list changed")

// Watcher bootstraps and watches a single ConfigMap for Ceph monitor endpoints.
type Watcher struct {
	Client    kubernetes.Interface
	Namespace string
	Name      string
	Key       string
	ClusterID string
	Log       logr.Logger
	// Baseline is the CSV already passed to ConnectToRados.
	Baseline string

	changed chan string
}

// Bootstrap GETs the ConfigMap once and returns a normalized monitor CSV.
func (w *Watcher) Bootstrap(ctx context.Context) (string, error) {
	if err := w.validate(); err != nil {
		return "", err
	}

	cm, err := w.Client.CoreV1().ConfigMaps(w.Namespace).Get(ctx, w.Name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get configmap %s/%s: %w", w.Namespace, w.Name, err)
	}

	endpoints, err := ParseConfigMapData(cm.Data, w.Key, w.ClusterID)
	if err != nil {
		return "", fmt.Errorf("parse configmap %s/%s: %w", w.Namespace, w.Name, err)
	}
	return Join(endpoints), nil
}

// Start watches the ConfigMap until ctx is done or the parsed CSV differs from Baseline.
// A delete or a later parse error keeps Baseline in place and does not return an error.
func (w *Watcher) Start(ctx context.Context) error {
	if err := w.validate(); err != nil {
		return err
	}
	if w.Log.GetSink() == nil {
		w.Log = logr.Discard()
	}
	w.changed = make(chan string, 1)

	factory := informers.NewSharedInformerFactoryWithOptions(
		w.Client,
		informerResyncPeriod,
		informers.WithNamespace(w.Namespace),
		informers.WithTweakListOptions(func(opts *metav1.ListOptions) {
			opts.FieldSelector = fields.OneTermEqualSelector(metav1.ObjectNameField, w.Name).String()
		}),
	)
	informer := factory.Core().V1().ConfigMaps().Informer()
	_, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			w.handle(obj)
		},
		UpdateFunc: func(_, newObj interface{}) {
			w.handle(newObj)
		},
		DeleteFunc: func(obj interface{}) {
			if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
				obj = tombstone.Obj
			}
			name := w.Name
			if cm, ok := obj.(*corev1.ConfigMap); ok {
				name = cm.Name
			}
			w.Log.Info("monitor configmap deleted; keeping last known endpoints", "namespace", w.Namespace, "name", name)
		},
	})
	if err != nil {
		return fmt.Errorf("add configmap event handler: %w", err)
	}

	// The informer runs until this context is canceled. Cancel it before
	// Shutdown, or Shutdown waits for a watch that only stops with ctx.
	watchCtx, cancelWatch := context.WithCancel(ctx)
	defer cancelWatch()

	factory.Start(watchCtx.Done())
	defer factory.Shutdown()

	if !cache.WaitForCacheSync(ctx.Done(), informer.HasSynced) {
		return nil
	}
	// An update between Bootstrap and this watch is already in the store.
	if obj, exists, err := informer.GetStore().GetByKey(w.Namespace + "/" + w.Name); err == nil && exists {
		if csv, ok := w.csvFrom(obj); ok && csv != w.Baseline {
			cancelWatch()
			return fmt.Errorf("%w: %s", ErrMonitorListChanged, csv)
		}
	}

	select {
	case <-ctx.Done():
		return nil
	case csv := <-w.changed:
		cancelWatch()
		return fmt.Errorf("%w: %s", ErrMonitorListChanged, csv)
	}
}

func (w *Watcher) validate() error {
	if w.Client == nil {
		return fmt.Errorf("client must be set")
	}
	if w.Namespace == "" || w.Name == "" {
		return fmt.Errorf("namespace and name must be set")
	}
	return nil
}

func (w *Watcher) handle(obj interface{}) {
	csv, ok := w.csvFrom(obj)
	if !ok || csv == w.Baseline {
		return
	}
	w.Log.Info("ceph monitor list changed", "monitors", csv)
	select {
	case w.changed <- csv:
	default:
	}
}

func (w *Watcher) csvFrom(obj interface{}) (string, bool) {
	cm, ok := obj.(*corev1.ConfigMap)
	if !ok || cm.Name != w.Name {
		return "", false
	}

	endpoints, err := ParseConfigMapData(cm.Data, w.Key, w.ClusterID)
	if err != nil {
		w.Log.Error(err, "failed to parse monitor configmap", "namespace", w.Namespace, "name", w.Name)
		return "", false
	}
	return Join(endpoints), true
}
