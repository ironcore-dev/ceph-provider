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
	"sort"

	"github.com/go-logr/logr"
	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v6/apis/volumesnapshot/v1"
	"github.com/onmetal/cephlet/pkg/rook"
	"github.com/onmetal/controller-utils/clientutils"
	storagev1alpha1 "github.com/onmetal/onmetal-api/api/storage/v1alpha1"
	"github.com/onmetal/onmetal-api/apiutils/equality"
	rookv1 "github.com/rook/rook/pkg/apis/ceph.rook.io/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
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
	volumePoolFinalizer        = "cephlet.onmetal.de/volumepool"
	volumePoolSecretAnnotation = "ceph-client-secret-name"
)

var (
	volumePoolFieldOwner = client.FieldOwner("cephlet.onmetal.de/volumepool")
)

// VolumePoolReconciler reconciles a VolumePool object
type VolumePoolReconciler struct {
	client.Client
	Scheme                *runtime.Scheme
	VolumePoolName        string
	VolumePoolProviderID  string
	VolumePoolLabels      map[string]string
	VolumePoolAnnotations map[string]string
	VolumeClassSelector   client.MatchingLabels
	VolumePoolReplication int
	RookConfig            *rook.Config
}

//+kubebuilder:rbac:groups=storage.api.onmetal.de,resources=volumepools,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=storage.api.onmetal.de,resources=volumepools/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=storage.api.onmetal.de,resources=volumepools/finalizers,verbs=update
//+kubebuilder:rbac:groups=storage.api.onmetal.de,resources=volumeclasses,verbs=get;list;watch

//+kubebuilder:rbac:groups=storage.k8s.io,resources=storageclasses,verbs=get;list;watch;create;update;patch;delete

//+kubebuilder:rbac:groups=snapshot.storage.k8s.io,resources=volumesnapshotclasses,verbs=get;list;watch;create;update;patch;delete

//+kubebuilder:rbac:groups=ceph.rook.io,resources=cephblockpools,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=ceph.rook.io,resources=cephclients,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *VolumePoolReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	pool := &storagev1alpha1.VolumePool{}
	if err := r.Get(ctx, req.NamespacedName, pool); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	return r.reconcileExists(ctx, log, pool)
}

func (r *VolumePoolReconciler) reconcileExists(ctx context.Context, log logr.Logger, pool *storagev1alpha1.VolumePool) (ctrl.Result, error) {
	if !pool.DeletionTimestamp.IsZero() {
		return r.delete(ctx, log, pool)
	}

	if pool.Name != r.VolumePoolName {
		log.V(1).Info("Skipping VolumePool, since it is not owned by us")
		return ctrl.Result{}, nil
	}

	return r.reconcile(ctx, log, pool)
}

func (r *VolumePoolReconciler) reconcile(ctx context.Context, log logr.Logger, pool *storagev1alpha1.VolumePool) (ctrl.Result, error) {
	log.V(1).Info("Reconciling VolumePool")
	if err := clientutils.PatchAddFinalizer(ctx, r.Client, pool, volumePoolFinalizer); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.updateStateToPendingIfEmtpy(ctx, pool); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to patch status for volume pool: %w", err)
	}

	rookPool := &rookv1.CephBlockPool{
		TypeMeta: metav1.TypeMeta{
			Kind:       "CephBlockPool",
			APIVersion: "ceph.rook.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      pool.Name,
			Namespace: r.RookConfig.Namespace,
		},
		Spec: rookv1.NamedBlockPoolSpec{
			PoolSpec: rookv1.PoolSpec{
				Replicated: rookv1.ReplicatedSpec{
					Size: uint(r.VolumePoolReplication),
				},
				EnableRBDStats: r.RookConfig.EnableRBDStats,
			},
		},
	}

	if err := r.Patch(ctx, rookPool, client.Apply, volumePoolFieldOwner, client.ForceOwnership); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to apply ceph volume pool %s: %w", client.ObjectKeyFromObject(rookPool), err)
	}

	if waitUntilReady, err := r.applyCephClient(ctx, log, pool); err != nil || waitUntilReady {
		return ctrl.Result{}, err
	}

	if err := r.applyStorageClass(ctx, log, pool); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to apply StorageClass: %w", err)
	}

	if err := r.applyVolumeSnapshotClass(ctx, log, pool); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to apply VolumeSnapshotClass: %w", err)
	}

	return r.patchVolumePoolStatus(ctx, pool, rookPool)
}

func (r *VolumePoolReconciler) delete(ctx context.Context, log logr.Logger, pool *storagev1alpha1.VolumePool) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(pool, volumePoolFinalizer) {
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
	if err := clientutils.PatchRemoveFinalizer(ctx, r.Client, pool, volumePoolFinalizer); err != nil {
		return ctrl.Result{}, fmt.Errorf("error removing finalizer: %w", err)
	}

	log.V(1).Info("Successfully released finalizer")
	return ctrl.Result{}, nil
}

func (r *VolumePoolReconciler) applyStorageClass(ctx context.Context, log logr.Logger, pool *storagev1alpha1.VolumePool) error {
	storageClass := &storagev1.StorageClass{}
	storageClassKey := types.NamespacedName{Name: GetClusterVolumePoolName(r.RookConfig.ClusterId, pool.Name)}
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
		Provisioner: r.RookConfig.CSIDriverName,
		Parameters: map[string]string{
			"clusterID":     r.RookConfig.ClusterId,
			"pool":          r.VolumePoolName,
			"imageFeatures": r.RookConfig.StorageClassImageFeatures,
			"csi.storage.k8s.io/provisioner-secret-name":            r.RookConfig.CSIRBDProvisionerSecretName,
			"csi.storage.k8s.io/provisioner-secret-namespace":       r.RookConfig.Namespace,
			"csi.storage.k8s.io/controller-expand-secret-name":      r.RookConfig.CSIRBDProvisionerSecretName,
			"csi.storage.k8s.io/controller-expand-secret-namespace": r.RookConfig.Namespace,
			"csi.storage.k8s.io/node-stage-secret-name":             r.RookConfig.CSIRBDNodeSecretName,
			"csi.storage.k8s.io/node-stage-secret-namespace":        r.RookConfig.Namespace,
			"csi.storage.k8s.io/fstype":                             r.RookConfig.StorageClassFSType,
		},
		ReclaimPolicy:        (*corev1.PersistentVolumeReclaimPolicy)(&r.RookConfig.StorageClassReclaimPolicy),
		MountOptions:         r.RookConfig.StorageClassMountOptions,
		AllowVolumeExpansion: &r.RookConfig.StorageClassAllowVolumeExpansion,
		VolumeBindingMode:    (*storagev1.VolumeBindingMode)(&r.RookConfig.StorageClassVolumeBindingMode),
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

func (r *VolumePoolReconciler) applyVolumeSnapshotClass(ctx context.Context, log logr.Logger, pool *storagev1alpha1.VolumePool) error {
	volumeSnapshotClass := &snapshotv1.VolumeSnapshotClass{}
	volumeSnapshotClassKey := types.NamespacedName{Name: GetClusterVolumePoolName(r.RookConfig.ClusterId, pool.Name)}
	err := r.Get(ctx, volumeSnapshotClassKey, volumeSnapshotClass)
	if err == nil {
		return nil
	}
	if client.IgnoreNotFound(err) != nil {
		return fmt.Errorf("failed to to get volumeSnapshotClass: %w", err)
	}

	volumeSnapshotClass = &snapshotv1.VolumeSnapshotClass{
		TypeMeta: metav1.TypeMeta{
			Kind:       "VolumeSnapshotClass",
			APIVersion: snapshotv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: volumeSnapshotClassKey.Name,
		},
		Driver: r.RookConfig.CSIDriverName,
		Parameters: map[string]string{
			"clusterID": r.RookConfig.ClusterId,
			"csi.storage.k8s.io/snapshotter-secret-name":      r.RookConfig.CSIRBDProvisionerSecretName,
			"csi.storage.k8s.io/snapshotter-secret-namespace": r.RookConfig.Namespace,
		},
		DeletionPolicy: snapshotv1.VolumeSnapshotContentDelete,
	}

	if err := ctrl.SetControllerReference(pool, volumeSnapshotClass, r.Scheme); err != nil {
		return fmt.Errorf("failed to set controller reference for volumeSnapshotClass %s: %w", client.ObjectKeyFromObject(volumeSnapshotClass), err)
	}

	if err := r.Patch(ctx, volumeSnapshotClass, client.Apply, volumeFieldOwner, client.ForceOwnership); err != nil {
		return fmt.Errorf("failed to patch volumeSnapshotClass %s for volumepool %s: %w", client.ObjectKeyFromObject(volumeSnapshotClass), client.ObjectKeyFromObject(pool), err)
	}

	log.V(1).Info("Applied VolumeSnapshotClass")
	return nil
}

func (r *VolumePoolReconciler) applyCephClient(ctx context.Context, log logr.Logger, pool *storagev1alpha1.VolumePool) (bool, error) {
	cephClient := &rookv1.CephClient{
		TypeMeta: metav1.TypeMeta{
			Kind:       "CephClient",
			APIVersion: "ceph.rook.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: r.RookConfig.Namespace,
			Name:      GetClusterVolumePoolName(r.RookConfig.ClusterId, pool.Name),
		},
		Spec: rookv1.ClientSpec{
			Name: "",
			Caps: map[string]string{
				"mgr": fmt.Sprintf("profile rbd pool=%s", r.VolumePoolName),
				"mon": "profile rbd",
				"osd": fmt.Sprintf("profile rbd pool=%s", r.VolumePoolName),
			},
		},
	}

	if err := ctrl.SetControllerReference(pool, cephClient, r.Scheme); err != nil {
		return false, fmt.Errorf("failed to set ownerreference for ceph client %s: %w", client.ObjectKeyFromObject(cephClient), err)
	}

	if err := r.Patch(ctx, cephClient, client.Apply, volumeFieldOwner, client.ForceOwnership); err != nil {
		return false, fmt.Errorf("failed to patch ceph client %s: %w", client.ObjectKeyFromObject(cephClient), err)
	}

	if cephClient.Status == nil || cephClient.Status.Phase != rookv1.ConditionReady {
		log.V(1).Info("ceph client is not ready yet", "client", client.ObjectKeyFromObject(cephClient))
		return true, nil
	}

	secretName, found := cephClient.Status.Info[cephClientSecretKey]
	if !found {
		return false, fmt.Errorf("failed to get secret name from ceph client %s", client.ObjectKeyFromObject(cephClient))
	}

	poolBase := pool.DeepCopy()
	if pool.Annotations == nil {
		pool.Annotations = map[string]string{}
	}
	pool.Annotations[volumePoolSecretAnnotation] = secretName
	if err := r.Patch(ctx, pool, client.MergeFrom(poolBase)); err != nil {
		return false, fmt.Errorf("failed to patch volume pool %s annotations: %w", client.ObjectKeyFromObject(pool), err)
	}

	return false, nil
}

func (r *VolumePoolReconciler) gatherVolumeClasses(ctx context.Context) ([]corev1.LocalObjectReference, error) {
	list := &storagev1alpha1.VolumeClassList{}
	if err := r.List(ctx, list, r.VolumeClassSelector); err != nil {
		return nil, fmt.Errorf("error listing volume classes: %w", err)
	}

	var availableVolumeClasses []corev1.LocalObjectReference
	for _, volumeClass := range list.Items {
		availableVolumeClasses = append(availableVolumeClasses, corev1.LocalObjectReference{Name: volumeClass.Name})
	}
	sort.Slice(availableVolumeClasses, func(i, j int) bool {
		return availableVolumeClasses[i].Name < availableVolumeClasses[j].Name
	})
	return availableVolumeClasses, nil
}

func (r *VolumePoolReconciler) updateStateToPendingIfEmtpy(ctx context.Context, pool *storagev1alpha1.VolumePool) error {
	if pool.Status.State != "" {
		return nil
	}
	poolBase := pool.DeepCopy()
	pool.Status.State = storagev1alpha1.VolumePoolStatePending
	return r.Status().Patch(ctx, pool, client.MergeFrom(poolBase))
}

func (r *VolumePoolReconciler) patchVolumePoolStatus(ctx context.Context, pool *storagev1alpha1.VolumePool, rookPool *rookv1.CephBlockPool) (ctrl.Result, error) {
	availableVolumeClasses, err := r.gatherVolumeClasses(ctx)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to gather available volume classes for volume pool: %w", err)
	}

	var requeue bool
	poolBase := pool.DeepCopy()
	switch {
	case rookPool.Status == nil:
		pool.Status.State = storagev1alpha1.VolumePoolStatePending
		requeue = true
	case rookPool.Status.Phase == rookv1.ConditionReady:
		pool.Status.State = storagev1alpha1.VolumePoolStateAvailable
	case rookPool.Status.Phase == rookv1.ConditionFailure:
		pool.Status.State = storagev1alpha1.VolumePoolStateUnavailable
		requeue = true
	default:
		pool.Status.State = storagev1alpha1.VolumePoolStatePending
		requeue = true
	}

	pool.Status.AvailableVolumeClasses = availableVolumeClasses

	if err := r.Status().Patch(ctx, pool, client.MergeFrom(poolBase)); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to patch status for volume pool: %w", err)
	}

	return ctrl.Result{Requeue: requeue}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *VolumePoolReconciler) SetupWithManager(mgr ctrl.Manager) error {
	ctx := context.Background()
	ctx = ctrl.LoggerInto(ctx, ctrl.Log.WithName("volume-pool").WithName("setup"))

	if err := r.birthCry(ctx); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		//TODO: remove once API Server is fixed
		For(&storagev1alpha1.VolumePool{}, builder.WithPredicates(predicate.Funcs{
			UpdateFunc: func(updateEvent event.UpdateEvent) bool {
				old := updateEvent.ObjectOld.(*storagev1alpha1.VolumePool)
				new := updateEvent.ObjectNew.(*storagev1alpha1.VolumePool)

				return !equality.Semantic.DeepEqual(old.Spec, new.Spec)
			},
		})).
		Owns(&rookv1.CephBlockPool{}).
		Owns(&rookv1.CephClient{}).
		Watches(
			&source.Kind{Type: &storagev1alpha1.VolumeClass{}},
			handler.EnqueueRequestsFromMapFunc(func(object client.Object) []reconcile.Request {
				return []reconcile.Request{{NamespacedName: types.NamespacedName{
					Name: r.VolumePoolName,
				}}}
			}),
		).
		Complete(r)
}

func (r *VolumePoolReconciler) birthCry(ctx context.Context) error {
	log := ctrl.LoggerFrom(ctx)
	log.V(1).Info("applying volume pool", "volumepool", r.VolumePoolName)
	if err := r.Patch(ctx, &storagev1alpha1.VolumePool{
		TypeMeta: metav1.TypeMeta{
			APIVersion: storagev1alpha1.SchemeGroupVersion.String(),
			Kind:       "VolumePool",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        r.VolumePoolName,
			Labels:      r.VolumePoolLabels,
			Annotations: r.VolumePoolAnnotations,
		},
		Spec: storagev1alpha1.VolumePoolSpec{
			ProviderID: r.VolumePoolProviderID,
		},
	}, client.Apply, volumePoolFieldOwner, client.ForceOwnership); err != nil {
		return fmt.Errorf("error applying volume pool: %w", err)
	}
	return nil
}
