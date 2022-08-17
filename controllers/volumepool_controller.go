package controllers

import (
	"context"
	"fmt"
	"sort"

	"github.com/go-logr/logr"
	"github.com/onmetal/controller-utils/clientutils"
	storagev1alpha1 "github.com/onmetal/onmetal-api/apis/storage/v1alpha1"
	rookv1 "github.com/rook/rook/pkg/apis/ceph.rook.io/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	volumePoolFinalizer = "cephlet.onmetal.de/volumepool"
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
	RookNamespace         string
	EnableRBDStats        bool
}

//+kubebuilder:rbac:groups=storage.api.onmetal.de,resources=volumepools,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=storage.api.onmetal.de,resources=volumepools/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=storage.api.onmetal.de,resources=volumepools/finalizers,verbs=update

//+kubebuilder:rbac:groups=storage.api.onmetal.de,resources=volumeclasses,verbs=get;list;watch

//+kubebuilder:rbac:groups=ceph.rook.io,resources=cephblockpools,verbs=get;list;watch;create;update;patch;delete

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
	return r.reconcile(ctx, log, pool)
}

func (r *VolumePoolReconciler) reconcile(ctx context.Context, log logr.Logger, pool *storagev1alpha1.VolumePool) (ctrl.Result, error) {
	log.V(1).Info("Reconciling VolumePool")
	if err := clientutils.PatchAddFinalizer(ctx, r.Client, pool, volumePoolFinalizer); err != nil {
		return ctrl.Result{}, err
	}

	rookPool := &rookv1.CephBlockPool{
		TypeMeta: metav1.TypeMeta{
			Kind:       "CephBlockPool",
			APIVersion: "ceph.rook.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      pool.Name,
			Namespace: r.RookNamespace,
		},
		Spec: rookv1.NamedBlockPoolSpec{
			PoolSpec: rookv1.PoolSpec{
				Replicated: rookv1.ReplicatedSpec{
					Size: uint(r.VolumePoolReplication),
				},
				EnableRBDStats: r.EnableRBDStats,
			},
		},
	}

	if err := r.Patch(ctx, rookPool, client.Apply, volumePoolFieldOwner, client.ForceOwnership); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to apply ceph volume pool %s: %w", client.ObjectKeyFromObject(rookPool), err)
	}

	availableVolumeClasses, err := r.gatherVolumeClasses(ctx)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to gather available volume classes for volume pool: %w", err)
	}

	poolBase := pool.DeepCopy()
	poolBase.Status.State = storagev1alpha1.VolumePoolStateAvailable
	poolBase.Status.AvailableVolumeClasses = availableVolumeClasses
	if err := r.Status().Patch(ctx, pool, client.MergeFrom(poolBase)); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to patch status for volume pool: %w", err)
	}

	return ctrl.Result{}, nil
}

func (r *VolumePoolReconciler) delete(ctx context.Context, log logr.Logger, pool *storagev1alpha1.VolumePool) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(pool, volumePoolFinalizer) {
		return ctrl.Result{}, nil
	}

	cephPoolExisted, err := clientutils.DeleteIfExists(ctx, r.Client, &rookv1.CephBlockPool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pool.Name,
			Namespace: r.RookNamespace,
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

func (r *VolumePoolReconciler) gatherVolumeClasses(ctx context.Context) ([]corev1.LocalObjectReference, error) {
	list := &storagev1alpha1.VolumeClassList{}
	if err := r.List(ctx, list, client.MatchingLabels(r.VolumeClassSelector)); err != nil {
		return nil, fmt.Errorf("error listing machine classes: %w", err)
	}

	availableVolumeClasses := make([]corev1.LocalObjectReference, 0, len(list.Items))
	for _, volumeClass := range list.Items {
		availableVolumeClasses = append(availableVolumeClasses, corev1.LocalObjectReference{Name: volumeClass.Name})
	}
	sort.Slice(availableVolumeClasses, func(i, j int) bool {
		return availableVolumeClasses[i].Name < availableVolumeClasses[j].Name
	})
	return availableVolumeClasses, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *VolumePoolReconciler) SetupWithManager(mgr ctrl.Manager) error {
	ctx := context.Background()
	ctx = ctrl.LoggerInto(ctx, ctrl.Log.WithName("volume-pool").WithName("setup"))

	if err := r.birthCry(ctx); err != nil {
		return err
	}

	// TODO: setup watch for VolumeClasses in order to reconcile availableVolumeClasses in Pool Status
	return ctrl.NewControllerManagedBy(mgr).
		For(&storagev1alpha1.VolumePool{}).
		Owns(&rookv1.CephBlockPool{}).
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
