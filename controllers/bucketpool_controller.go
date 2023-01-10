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
	"fmt"
	storagev1 "k8s.io/api/storage/v1"
	"sort"
	"time"

	"github.com/go-logr/logr"
	"github.com/onmetal/cephlet/pkg/rook"
	"github.com/onmetal/controller-utils/clientutils"
	storagev1alpha1 "github.com/onmetal/onmetal-api/api/storage/v1alpha1"
	"github.com/onmetal/onmetal-api/apiutils/equality"
	rookv1 "github.com/rook/rook/pkg/apis/ceph.rook.io/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	bucketPoolFinalizer        = "cephlet.onmetal.de/bucketpool"
	bucketPoolSecretAnnotation = "ceph-client-secret-name"
)

var (
	bucketPoolFieldOwner = client.FieldOwner("cephlet.onmetal.de/bucketpool")
)

// BucketPoolReconciler reconciles a BucketPool object
type BucketPoolReconciler struct {
	client.Client
	Scheme                *runtime.Scheme
	BucketPoolName        string
	BucketPoolProviderID  string
	BucketPoolLabels      map[string]string
	BucketPoolAnnotations map[string]string
	BucketClassSelector   client.MatchingLabels
	BucketPoolReplication int
	RookConfig            *rook.Config
}

//+kubebuilder:rbac:groups=storage.api.onmetal.de,resources=bucketpools,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=storage.api.onmetal.de,resources=bucketpools/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=storage.api.onmetal.de,resources=bucketpools/finalizers,verbs=update
//+kubebuilder:rbac:groups=storage.api.onmetal.de,resources=bucketclasses,verbs=get;list;watch

//+kubebuilder:rbac:groups=ceph.rook.io,resources=cephobjectstores,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *BucketPoolReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	pool := &storagev1alpha1.BucketPool{}
	if err := r.Get(ctx, req.NamespacedName, pool); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	return r.reconcileExists(ctx, log, pool)
}

func (r *BucketPoolReconciler) reconcileExists(ctx context.Context, log logr.Logger, pool *storagev1alpha1.BucketPool) (ctrl.Result, error) {
	if !pool.DeletionTimestamp.IsZero() {
		return r.delete(ctx, log, pool)
	}

	if pool.Name != r.BucketPoolName {
		log.V(1).Info("Skipping BucketPool, since it is not owned by us")
		return ctrl.Result{}, nil
	}

	return r.reconcile(ctx, log, pool)
}

func (r *BucketPoolReconciler) reconcile(ctx context.Context, log logr.Logger, pool *storagev1alpha1.BucketPool) (ctrl.Result, error) {
	log.V(1).Info("Reconciling BucketPool")
	if err := clientutils.PatchAddFinalizer(ctx, r.Client, pool, bucketPoolFinalizer); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.updateStateToPendingIfEmtpy(ctx, pool); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to patch status for bucket pool: %w", err)
	}

	rookPool := &rookv1.CephObjectStore{
		TypeMeta: metav1.TypeMeta{
			Kind:       "CephObjectStore",
			APIVersion: "ceph.rook.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      pool.Name,
			Namespace: r.RookConfig.Namespace,
		},
		Spec: rookv1.ObjectStoreSpec{
			MetadataPool: rookv1.PoolSpec{
				FailureDomain: "host",
				Replicated: rookv1.ReplicatedSpec{
					Size: uint(r.BucketPoolReplication),
				},
			},
			DataPool: rookv1.PoolSpec{
				FailureDomain: "host",
				ErasureCoded: rookv1.ErasureCodedSpec{
					DataChunks:   2,
					CodingChunks: 1,
				},
				Replicated: rookv1.ReplicatedSpec{
					Size: uint(r.BucketPoolReplication),
				},
			},
			PreservePoolsOnDelete: true,
			Gateway: rookv1.GatewaySpec{
				Port:       80,
				SecurePort: 443,
				Instances:  1,
			},
			HealthCheck: rookv1.BucketHealthCheckSpec{
				Bucket: rookv1.HealthCheckSpec{
					Disabled: false,
					Interval: &metav1.Duration{Duration: 60 * time.Second},
				},
			},
		},
	}

	if err := r.Patch(ctx, rookPool, client.Apply, bucketPoolFieldOwner, client.ForceOwnership); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to apply ceph bucket pool %s: %w", client.ObjectKeyFromObject(rookPool), err)
	}

	if err := r.applyStorageClass(ctx, log, pool); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to apply StorageClass: %w", err)
	}

	return r.patchBucketPoolStatus(ctx, pool, rookPool)
}

func (r *BucketPoolReconciler) applyStorageClass(ctx context.Context, log logr.Logger, pool *storagev1alpha1.BucketPool) error {
	storageClass := &storagev1.StorageClass{}
	storageClassKey := types.NamespacedName{Name: GetClusterBucketPoolName(r.RookConfig.ClusterId, pool.Name)}
	err := r.Get(ctx, storageClassKey, storageClass)
	if err == nil {
		return nil
	}
	if client.IgnoreNotFound(err) != nil {
		return fmt.Errorf("failed to to get storageClass: %w", err)
	}

	storageClass = &storagev1.StorageClass{
		TypeMeta: metav1.TypeMeta{
			Kind:       "StorageClass",
			APIVersion: "storage.k8s.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: storageClassKey.Name,
		},
		Provisioner: r.RookConfig.BucketProvisioner,
		Parameters: map[string]string{
			"objectStoreName":      pool.Name,
			"objectStoreNamespace": r.RookConfig.Namespace,
		},
		ReclaimPolicy: (*corev1.PersistentVolumeReclaimPolicy)(&r.RookConfig.StorageClassReclaimPolicy),
	}

	if err := ctrl.SetControllerReference(pool, storageClass, r.Scheme); err != nil {
		return fmt.Errorf("failed to set ownerreference for storage class %s: %w", client.ObjectKeyFromObject(storageClass), err)
	}

	if err := r.Patch(ctx, storageClass, client.Apply, volumeFieldOwner, client.ForceOwnership); err != nil {
		return fmt.Errorf("failed to patch storageClass %s for volumepool %s: %w", client.ObjectKeyFromObject(storageClass), client.ObjectKeyFromObject(pool), err)
	}

	log.V(1).Info("Applied StorageClass")
	return nil
}

func (r *BucketPoolReconciler) delete(ctx context.Context, log logr.Logger, pool *storagev1alpha1.BucketPool) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(pool, bucketPoolFinalizer) {
		return ctrl.Result{}, nil
	}

	cephPoolExisted, err := clientutils.DeleteIfExists(ctx, r.Client, &rookv1.CephBlockPool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pool.Name,
			Namespace: r.RookConfig.Namespace,
		}})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error deleting %s: %w", pool.Name, err)
	}

	if cephPoolExisted {
		return ctrl.Result{Requeue: true}, nil
	}

	log.V(1).Info("Ceph pool gone, removing finalizer")
	if err := clientutils.PatchRemoveFinalizer(ctx, r.Client, pool, bucketPoolFinalizer); err != nil {
		return ctrl.Result{}, fmt.Errorf("error removing finalizer: %w", err)
	}

	log.V(1).Info("Successfully released finalizer")
	return ctrl.Result{}, nil
}

func (r *BucketPoolReconciler) gatherBucketClasses(ctx context.Context) ([]corev1.LocalObjectReference, error) {
	list := &storagev1alpha1.BucketClassList{}
	if err := r.List(ctx, list, r.BucketClassSelector); err != nil {
		return nil, fmt.Errorf("error listing bucket classes: %w", err)
	}

	var availableBucketClasses []corev1.LocalObjectReference
	for _, bucketClass := range list.Items {
		availableBucketClasses = append(availableBucketClasses, corev1.LocalObjectReference{Name: bucketClass.Name})
	}
	sort.Slice(availableBucketClasses, func(i, j int) bool {
		return availableBucketClasses[i].Name < availableBucketClasses[j].Name
	})
	return availableBucketClasses, nil
}

func (r *BucketPoolReconciler) updateStateToPendingIfEmtpy(ctx context.Context, pool *storagev1alpha1.BucketPool) error {
	if pool.Status.State != "" {
		return nil
	}
	poolBase := pool.DeepCopy()
	pool.Status.State = storagev1alpha1.BucketPoolStatePending
	return r.Status().Patch(ctx, pool, client.MergeFrom(poolBase))
}

func (r *BucketPoolReconciler) patchBucketPoolStatus(ctx context.Context, pool *storagev1alpha1.BucketPool, rookPool *rookv1.CephObjectStore) (ctrl.Result, error) {
	availableBucketClasses, err := r.gatherBucketClasses(ctx)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to gather available bucket classes for bucket pool: %w", err)
	}

	var requeue bool
	poolBase := pool.DeepCopy()
	switch {
	case rookPool.Status == nil:
		pool.Status.State = storagev1alpha1.BucketPoolStatePending
		requeue = true
	case rookPool.Status.Phase == rookv1.ConditionConnected:
		pool.Status.State = storagev1alpha1.BucketPoolStateAvailable
	case rookPool.Status.Phase == rookv1.ConditionFailure:
		pool.Status.State = storagev1alpha1.BucketPoolStateUnavailable
		requeue = true
	default:
		pool.Status.State = storagev1alpha1.BucketPoolStatePending
		requeue = true
	}

	pool.Status.AvailableBucketClasses = availableBucketClasses

	if err := r.Status().Patch(ctx, pool, client.MergeFrom(poolBase)); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to patch status for bucket pool: %w", err)
	}

	return ctrl.Result{Requeue: requeue}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *BucketPoolReconciler) SetupWithManager(mgr ctrl.Manager) error {
	ctx := context.Background()
	ctx = ctrl.LoggerInto(ctx, ctrl.Log.WithName("bucket-pool").WithName("setup"))

	if err := r.birthCry(ctx); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		//TODO: remove once API Server is fixed
		For(&storagev1alpha1.BucketPool{}, builder.WithPredicates(predicate.Funcs{
			UpdateFunc: func(updateEvent event.UpdateEvent) bool {
				old := updateEvent.ObjectOld.(*storagev1alpha1.BucketPool)
				new := updateEvent.ObjectNew.(*storagev1alpha1.BucketPool)

				return !equality.Semantic.DeepEqual(old.Spec, new.Spec)
			},
		})).
		Owns(&rookv1.CephBlockPool{}).
		Watches(
			&source.Kind{Type: &storagev1alpha1.BucketClass{}},
			handler.EnqueueRequestsFromMapFunc(func(object client.Object) []reconcile.Request {
				return []reconcile.Request{{NamespacedName: types.NamespacedName{
					Name: r.BucketPoolName,
				}}}
			}),
		).
		Complete(r)
}

func (r *BucketPoolReconciler) birthCry(ctx context.Context) error {
	log := ctrl.LoggerFrom(ctx)
	log.V(1).Info("applying bucket pool", "bucketpool", r.BucketPoolName)
	if err := r.Patch(ctx, &storagev1alpha1.BucketPool{
		TypeMeta: metav1.TypeMeta{
			APIVersion: storagev1alpha1.SchemeGroupVersion.String(),
			Kind:       "BucketPool",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        r.BucketPoolName,
			Labels:      r.BucketPoolLabels,
			Annotations: r.BucketPoolAnnotations,
		},
		Spec: storagev1alpha1.BucketPoolSpec{
			ProviderID: r.BucketPoolProviderID,
		},
	}, client.Apply, bucketPoolFieldOwner, client.ForceOwnership); err != nil {
		return fmt.Errorf("error applying bucket pool: %w", err)
	}
	return nil
}
