// Copyright 2022 OnMetal authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package controllers

import (
	"context"

	"github.com/go-logr/logr"
	"github.com/onmetal/cephlet/pkg/ceph"
	"github.com/onmetal/cephlet/pkg/rook"
	storagev1alpha1 "github.com/onmetal/onmetal-api/api/storage/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	bucketPoolRefIndex = ".spec.bucketPoolRef.name"
)

// BucketReconciler reconciles a Bucket object
type BucketReconciler struct {
	client.Client
	Scheme         *runtime.Scheme
	BucketPoolName string
	RookConfig     *rook.Config
	CephClient     ceph.Client

	PoolUsage *prometheus.GaugeVec
}

//+kubebuilder:rbac:groups=storage.api.onmetal.de,resources=buckets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=storage.api.onmetal.de,resources=buckets/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=storage.api.onmetal.de,resources=buckets/finalizers,verbs=update
//+kubebuilder:rbac:groups=storage.api.onmetal.de,resources=bucketpools,verbs=get;list;watch
//+kubebuilder:rbac:groups=storage.api.onmetal.de,resources=bucketpools/status,verbs=get

//+kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch
//+kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=namespaces,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *BucketReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	bucket := &storagev1alpha1.Bucket{}
	if err := r.Get(ctx, req.NamespacedName, bucket); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	return r.reconcileExists(ctx, log, bucket)
}

func (r *BucketReconciler) reconcileExists(ctx context.Context, log logr.Logger, bucket *storagev1alpha1.Bucket) (ctrl.Result, error) {
	if !bucket.DeletionTimestamp.IsZero() {
		return r.delete(ctx, log, bucket)
	}
	return r.reconcile(ctx, log, bucket)
}

func (r *BucketReconciler) reconcile(ctx context.Context, log logr.Logger, bucket *storagev1alpha1.Bucket) (ctrl.Result, error) {
	log.V(1).Info("Reconciling Bucket")

	return ctrl.Result{}, nil
}

func (r *BucketReconciler) delete(ctx context.Context, log logr.Logger, bucket *storagev1alpha1.Bucket) (ctrl.Result, error) {
	return ctrl.Result{}, nil
}

func (r *BucketReconciler) findObjectsForBucketPool(pool client.Object) []reconcile.Request {
	buckets := &storagev1alpha1.BucketList{}
	if err := r.List(context.TODO(), buckets, &client.ListOptions{
		FieldSelector: fields.OneTermEqualSelector(bucketPoolRefIndex, pool.GetName()),
	}); err != nil {
		return nil
	}

	var requests []reconcile.Request
	for _, bucket := range buckets.Items {
		requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{
			Namespace: bucket.Namespace,
			Name:      bucket.Name,
		}})
	}
	return requests
}

// SetupWithManager sets up the controller with the Manager.
func (r *BucketReconciler) SetupWithManager(mgr ctrl.Manager) error {
	var err error
	if r.CephClient == nil {
		r.CephClient, err = ceph.NewClient(mgr.GetClient(), r.RookConfig)
		if err != nil {
			return err
		}
	}

	if err := mgr.GetFieldIndexer().IndexField(context.TODO(), &storagev1alpha1.Bucket{}, bucketPoolRefIndex, func(rawObj client.Object) []string {
		configDeployment := rawObj.(*storagev1alpha1.Bucket)
		if configDeployment.Spec.BucketPoolRef == nil || configDeployment.Spec.BucketPoolRef.Name == "" {
			return nil
		}
		return []string{configDeployment.Spec.BucketPoolRef.Name}
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&storagev1alpha1.Bucket{}).
		Watches(&source.Kind{Type: &storagev1alpha1.BucketPool{}}, handler.EnqueueRequestsFromMapFunc(r.findObjectsForBucketPool), builder.WithPredicates(predicate.Funcs{
			UpdateFunc: func(event event.UpdateEvent) bool {
				oldPool := event.ObjectOld.(*storagev1alpha1.BucketPool)
				newPool := event.ObjectNew.(*storagev1alpha1.BucketPool)
				if oldPool.Status.State == newPool.Status.State {
					return false
				}
				return newPool.Status.State == storagev1alpha1.BucketPoolStateAvailable
			},
		})).
		Complete(r)
}
