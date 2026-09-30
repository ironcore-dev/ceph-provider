// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package monitors

import (
	"context"
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

// Watcher bootstraps and watches a single ConfigMap for Ceph monitor endpoints.
type Watcher struct {
	Client    kubernetes.Interface
	Namespace string
	Name      string
	Key       string
	ClusterID string
	Log       logr.Logger
	OnChange  func(ctx context.Context, csv string)
}

// Bootstrap GETs the ConfigMap once and returns a normalized monitor CSV.
func (w *Watcher) Bootstrap(ctx context.Context) (string, error) {
	if w.Client == nil {
		return "", fmt.Errorf("client must be set")
	}
	if w.Namespace == "" || w.Name == "" {
		return "", fmt.Errorf("namespace and name must be set")
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

// Start runs a namespaced ConfigMap informer until ctx is done.
func (w *Watcher) Start(ctx context.Context) error {
	if w.Client == nil {
		return fmt.Errorf("client must be set")
	}
	if w.Namespace == "" || w.Name == "" {
		return fmt.Errorf("namespace and name must be set")
	}
	if w.OnChange == nil {
		return fmt.Errorf("OnChange must be set")
	}
	if w.Log.GetSink() == nil {
		w.Log = logr.Discard()
	}

	factory := informers.NewSharedInformerFactoryWithOptions(
		w.Client,
		informerResyncPeriod,
		informers.WithNamespace(w.Namespace),
		informers.WithTweakListOptions(func(opts *metav1.ListOptions) {
			opts.FieldSelector = fields.OneTermEqualSelector(metav1.ObjectNameField, w.Name).String()
		}),
	)
	informer := factory.Core().V1().ConfigMaps().Informer()

	handler := func(obj interface{}) {
		w.handleAddOrUpdate(ctx, obj)
	}
	_, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: handler,
		UpdateFunc: func(_, newObj interface{}) {
			handler(newObj)
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

	factory.Start(ctx.Done())
	defer factory.Shutdown()

	if !cache.WaitForCacheSync(ctx.Done(), informer.HasSynced) {
		// stopCh is ctx.Done(); false means the process is shutting down.
		return nil
	}

	<-ctx.Done()
	return nil
}

func (w *Watcher) handleAddOrUpdate(ctx context.Context, obj interface{}) {
	cm, ok := obj.(*corev1.ConfigMap)
	if !ok {
		return
	}
	if cm.Name != w.Name {
		return
	}

	endpoints, err := ParseConfigMapData(cm.Data, w.Key, w.ClusterID)
	if err != nil {
		w.Log.Error(err, "failed to parse monitor configmap", "namespace", w.Namespace, "name", w.Name)
		return
	}

	csv := Join(endpoints)
	onChange := w.OnChange
	go onChange(ctx, csv)
}
