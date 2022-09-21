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
	"strings"

	"github.com/go-logr/logr"
	"github.com/onmetal/cephlet/pkg/rook"
	"github.com/onmetal/controller-utils/clientutils"
	storagev1alpha1 "github.com/onmetal/onmetal-api/apis/storage/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	k8svolume "k8s.io/component-helpers/storage/volume"
	"k8s.io/utils/pointer"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	populatorPodPrefix      = "populate-"
	populatorPvcPrefix      = "prime-"
	pvcFinalizer            = "cephlet.onmetal.de/populate-target-protection"
	populatorContainerName  = "populate"
	populatorPodVolumeName  = "target"
	populatedFromAnnoSuffix = "populated-from"

	metadataUIDFieldName                         = ".metadata.uid"
	provisionerDeletionSecretNameAnnotation      = "volume.kubernetes.io/provisioner-deletion-secret-name"
	provisionerDeletionSecretNamespaceAnnotation = "volume.kubernetes.io/provisioner-deletion-secret-namespace"
)

var (
	pvcFieldOwner = client.FieldOwner("cephlet.onmetal.de/pvc")
)

type ImagePopulatorReconciler struct {
	client.Client
	Scheme                 *runtime.Scheme
	PopulatorImageName     string
	PopulatorPodDevicePath string
	PopulatorNamespace     string
	Prefix                 string
	RookConfig             *rook.Config
}

//+kubebuilder:rbac:groups=storage.api.onmetal.de,resources=volumes,verbs=get;list;watch
//+kubebuilder:rbac:groups=storage.api.onmetal.de,resources=volumes/status,verbs=get

//+kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;create;update;patch;delete

//+kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=persistentvolumeclaims/status,verbs=get
//+kubebuilder:rbac:groups=core,resources=persistentvolumes,verbs=get;list;watch;patch;delete
//+kubebuilder:rbac:groups=core,resources=persistentvolumes/status,verbs=get

//+kubebuilder:rbac:groups=storage.k8s.io,resources=storageclasses,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// The main reconciliation logic is derived from the Kubernetes populator machinery and adapted to use the
// controller-runtime controller flow.
// ref: https://github.com/kubernetes-csi/lib-volume-populator/blob/master/populator-machinery/controller.go
func (r *ImagePopulatorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	pvc := &corev1.PersistentVolumeClaim{}
	if err := r.Get(ctx, req.NamespacedName, pvc); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	return r.reconcileExists(ctx, log, pvc)
}

func (r *ImagePopulatorReconciler) reconcileExists(ctx context.Context, log logr.Logger, pvc *corev1.PersistentVolumeClaim) (ctrl.Result, error) {
	if !pvc.DeletionTimestamp.IsZero() {
		return r.delete(ctx, log, pvc)
	}
	return r.reconcile(ctx, log, pvc)
}

func (r *ImagePopulatorReconciler) delete(ctx context.Context, log logr.Logger, pvc *corev1.PersistentVolumeClaim) (ctrl.Result, error) {
	return ctrl.Result{}, nil

}

func (r *ImagePopulatorReconciler) reconcile(ctx context.Context, log logr.Logger, pvc *corev1.PersistentVolumeClaim) (ctrl.Result, error) {
	if r.PopulatorNamespace == pvc.Namespace {
		// Ignore PVCs in our own working namespace
		return ctrl.Result{}, nil
	}

	log.Info("Reconciling PVC")

	dataSourceRef := pvc.Spec.DataSourceRef
	if dataSourceRef == nil {
		// Ignore PVCs without a datasource
		return ctrl.Result{}, nil
	}
	log.Info("Found datasource ref for PVC", "DataSourceRef", dataSourceRef)

	if pvc.Spec.StorageClassName == nil {
		// Ignore PVCs without a StorageClassName
		return ctrl.Result{}, nil
	}

	if storagev1alpha1.SchemeGroupVersion.String() != *dataSourceRef.APIGroup || "Volume" != dataSourceRef.Kind || "" == dataSourceRef.Name {
		// Ignore PVCs if the datasourceRef is not a Volume
		return ctrl.Result{}, nil
	}

	volume := &storagev1alpha1.Volume{}
	volumeKey := types.NamespacedName{Name: dataSourceRef.Name, Namespace: pvc.Namespace}
	if err := r.Get(ctx, volumeKey, volume); err != nil {
		if !errors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("failed to get datasource ref %s for PVC: %w", volumeKey, err)
		}
		// We'll get called again later when the data source exists
		return ctrl.Result{}, fmt.Errorf("the datasource %s ref for PVC could not be found: %w", volumeKey, err)
	}

	log.Info("Found volume as datasource ref for PVC", "Volume", client.ObjectKeyFromObject(volume))

	storageClass := &storagev1.StorageClass{}
	storageClassKey := types.NamespacedName{Name: *pvc.Spec.StorageClassName}
	if err := r.Get(ctx, storageClassKey, storageClass); err != nil {
		if !errors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("failed to get storageclass %s for PVC: %w", storageClassKey, err)
		}
		// We'll get called again later when the storage class exists
		return ctrl.Result{}, fmt.Errorf("storageclass %s for PVC does not exist: %w", storageClassKey, err)
	}
	log.Info("Found StorageClass for PVC", "StorageClass", storageClassKey)

	if isStorageClassWaitingForConsumer(storageClass) {
		if getPvcNodeName(pvc) == "" {
			// Wait for the PVC to get a node name before continuing
			return ctrl.Result{}, fmt.Errorf("PVC has not been assigned to a node yet")
		}
	}

	// Look for the populator pod
	pod := &corev1.Pod{}
	podKey := types.NamespacedName{Name: generateNameFromPrefixAndUID(populatorPodPrefix, pvc.UID), Namespace: r.PopulatorNamespace}
	if err := r.Get(ctx, podKey, pod); client.IgnoreNotFound(err) != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get populator pod %s: %w", podKey, err)
	} else if errors.IsNotFound(err) {
		// TODO: optimize not found case -> handling of nil in code below
		pod = nil
	}

	// Look for PVC'
	pvcPrime := &corev1.PersistentVolumeClaim{}
	pvcPrimeKey := types.NamespacedName{Name: generateNameFromPrefixAndUID(populatorPvcPrefix, pvc.UID), Namespace: r.PopulatorNamespace}
	if err := r.Get(ctx, pvcPrimeKey, pvcPrime); client.IgnoreNotFound(err) != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get shadow PVC %s: %w", pvcPrimeKey, err)
	} else if errors.IsNotFound(err) {
		pvcPrime = nil
	}

	// *** Here is the first place we start to create/modify objects ***

	// If the PVC is unbound, we need to perform the population
	if pvc.Spec.VolumeName == "" {
		// Ensure the PVC has a finalizer on it, so we can clean up the stuff we create
		if _, err := clientutils.PatchEnsureFinalizer(ctx, r.Client, pvc, pvcFinalizer); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to ensure finalizer on PVC: %w", err)
		}

		// If the pod doesn't exist yet, create it
		if pod == nil {
			if nil != pvc.Spec.VolumeMode && corev1.PersistentVolumeBlock != *pvc.Spec.VolumeMode {
				log.Info(fmt.Sprintf("skipped pvc %s: volumeMode mode is not block", client.ObjectKeyFromObject(pvc)))
				// ignore non block volumes
				return ctrl.Result{}, nil
			}

			pod, err := r.createPopulatorPod(ctx, pvc, volume, isStorageClassWaitingForConsumer(storageClass))
			if err != nil {
				return ctrl.Result{}, err
			}
			log.Info("Created populator Pod", "Pod", client.ObjectKeyFromObject(pod))

			// If PVC doesn't exist yet, create it
			reconciled, err := r.createPvcPrimeIfNotExisting(ctx, log, pvc, pvcPrime, isStorageClassWaitingForConsumer(storageClass))
			if err != nil || reconciled {
				return ctrl.Result{}, err
			}
		}

		if corev1.PodSucceeded != pod.Status.Phase {
			if corev1.PodFailed == pod.Status.Phase {
				// Delete failed pods so we can try again
				if err := r.Delete(ctx, pod); err != nil {
					return ctrl.Result{}, fmt.Errorf("faild to remove failed populator pod %s: %w", podKey, err)
				}
			}
			// We'll get called again later when the pod succeeds
			// throw error in order to increase backoff
			return ctrl.Result{}, fmt.Errorf("populator pod %s failed", podKey)
		}

		// This would be bad
		if pvcPrime == nil {
			return ctrl.Result{}, fmt.Errorf("failed to find PVC for populator pod")
		}

		// Get PV
		pv := &corev1.PersistentVolume{}
		if err := r.Get(ctx, types.NamespacedName{Name: pvcPrime.Spec.VolumeName}, pv); client.IgnoreNotFound(err) != nil {
			return ctrl.Result{}, fmt.Errorf("failed to get pv for prime pvc %s: %w", pvcPrimeKey, err)
		} else if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}

		// Examine the claimref for the PV and see if it's bound to the correct PVC
		pvBase := pv.DeepCopy()
		claimRef := pv.Spec.ClaimRef
		if claimRef.Name != pvc.Name || claimRef.Namespace != pvc.Namespace || claimRef.UID != pvc.UID {
			// Make new PV with strategic patch values to perform the PV rebind
			pv.Spec.ClaimRef = &corev1.ObjectReference{
				Namespace:       pvc.Namespace,
				Name:            pvc.Name,
				UID:             pvc.UID,
				ResourceVersion: pvc.ResourceVersion,
			}
			populatedFromAnno := r.Prefix + "/" + populatedFromAnnoSuffix
			pv.Annotations = map[string]string{
				populatedFromAnno:                            pvc.Namespace + "/" + dataSourceRef.Name,
				k8svolume.AnnDynamicallyProvisioned:          r.RookConfig.CSIDriverName,
				provisionerDeletionSecretNameAnnotation:      r.RookConfig.CSIRBDProvisionerSecretName,
				provisionerDeletionSecretNamespaceAnnotation: r.RookConfig.Namespace,
			}

			if err := r.Patch(ctx, pv, client.MergeFrom(pvBase)); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to patch claimref of pv %s: %w", client.ObjectKeyFromObject(pv), err)
			}

			log.Info("Patched claimref of PVC to use the populated PV")

			// Don't start cleaning up yet -- we need to bind controller to acknowledge
			// the switch
			return ctrl.Result{}, nil
		}

		// We'll get called again later when the pod exists
		return ctrl.Result{}, nil
	}

	// Wait for the bind controller to rebind the PV
	if pvcPrime != nil {
		if corev1.ClaimLost != pvcPrime.Status.Phase {
			return ctrl.Result{}, nil
		}
	}

	// *** At this point the volume population is done, and we're just cleaning up ***

	// If the pod still exists, delete it
	if pod != nil {
		log.Info("Deleting populator pod", "Pod", client.ObjectKeyFromObject(pod))
		if err := r.Delete(ctx, pod); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to clean up populator pod %s: %w", podKey, err)
		}
	}

	// If PVC' still exists, delete it
	if pvcPrime != nil {
		log.Info("Deleting populator PVC", "PVC", client.ObjectKeyFromObject(pvcPrime))
		if err := r.Delete(ctx, pvcPrime); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to delete prime pvc %s: %w", pvcPrimeKey, err)
		}
	}

	// Make sure the PVC finalizer is gone
	if _, err := clientutils.PatchEnsureNoFinalizer(ctx, r.Client, pvc, pvcFinalizer); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to remove finalizer from pvc: %w", err)
	}

	log.Info("Successfully populated PVC")

	return ctrl.Result{}, nil
}

func makePopulatePodSpec(pvcName string) corev1.PodSpec {
	return corev1.PodSpec{
		//TODO: check appropriate SecurityContext settings
		SecurityContext: &corev1.PodSecurityContext{
			RunAsGroup: pointer.Int64(0),
			RunAsUser:  pointer.Int64(0),
			FSGroup:    pointer.Int64(0),
		},
		Containers: []corev1.Container{
			{
				Name:            populatorContainerName,
				ImagePullPolicy: corev1.PullIfNotPresent,
			},
		},
		RestartPolicy: corev1.RestartPolicyNever,
		Volumes: []corev1.Volume{
			{
				Name: populatorPodVolumeName,
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: pvcName,
					},
				},
			},
		},
	}
}

func (r *ImagePopulatorReconciler) enqueuePVCsFromPV() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(obj client.Object) []ctrl.Request {
		pv := obj.(*corev1.PersistentVolume)

		if pv.Spec.ClaimRef == nil {
			return nil
		}

		return []ctrl.Request{{NamespacedName: types.NamespacedName{
			Name:      pv.Spec.ClaimRef.Name,
			Namespace: pv.Spec.ClaimRef.Namespace}}}
	})
}

func (r *ImagePopulatorReconciler) enqueuePVCFromPrimePVC(ctx context.Context, log logr.Logger) handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(obj client.Object) []ctrl.Request {
		pvc := obj.(*corev1.PersistentVolumeClaim)
		log = log.WithValues("PVC", client.ObjectKeyFromObject(pvc))

		pvcUID := r.getUIDFromPrefixedName(pvc.Name, populatorPvcPrefix)

		pvcList := &corev1.PersistentVolumeClaimList{}
		if err := r.List(ctx, pvcList, &client.MatchingFields{metadataUIDFieldName: pvcUID}); err != nil {
			log.Error(err, "failed to list PVCs")
			return nil
		}

		res := make([]ctrl.Request, 0, len(pvcList.Items))
		for _, pvc := range pvcList.Items {
			res = append(res, ctrl.Request{NamespacedName: types.NamespacedName{
				Namespace: pvc.Namespace,
				Name:      pvc.Name,
			}})
		}
		return res
	})
}

func (r *ImagePopulatorReconciler) enqueuePVCfromPlaceholderPod(ctx context.Context, log logr.Logger) handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(obj client.Object) []ctrl.Request {
		pod := obj.(*corev1.Pod)
		log = log.WithValues("Pod", client.ObjectKeyFromObject(pod))

		pvcUID := r.getUIDFromPrefixedName(pod.Name, populatorPodPrefix)

		pvcList := &corev1.PersistentVolumeClaimList{}
		if err := r.List(ctx, pvcList, &client.MatchingFields{metadataUIDFieldName: pvcUID}); err != nil {
			log.Error(err, "failed to list PVCs")
			return nil
		}

		res := make([]ctrl.Request, 0, len(pvcList.Items))
		for _, pvc := range pvcList.Items {
			res = append(res, ctrl.Request{NamespacedName: types.NamespacedName{
				Namespace: pvc.Namespace,
				Name:      pvc.Name,
			}})
		}
		return res
	})
}

func (r *ImagePopulatorReconciler) getUIDFromPrefixedName(name, prefix string) string {
	return strings.TrimPrefix(name, prefix)
}

func generateNameFromPrefixAndUID(prefix string, uid types.UID) string {
	return fmt.Sprintf("%s%s", prefix, uid)
}

func (r *ImagePopulatorReconciler) createPopulatorPod(ctx context.Context, pvc *corev1.PersistentVolumeClaim, volume *storagev1alpha1.Volume, waitForFirstConsumer bool) (*corev1.Pod, error) {
	// Calculate the args for the populator pod
	var args []string
	args = append(args, "--image="+volume.Spec.Image)

	// Make the pod
	pod := &corev1.Pod{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Pod",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      generateNameFromPrefixAndUID(populatorPodPrefix, pvc.UID),
			Namespace: r.PopulatorNamespace,
		},
		Spec: makePopulatePodSpec(pvc.Name),
	}
	pod.Spec.Volumes[0].VolumeSource.PersistentVolumeClaim.ClaimName = generateNameFromPrefixAndUID(populatorPvcPrefix, pvc.UID)
	con := &pod.Spec.Containers[0]
	con.Image = r.PopulatorImageName
	con.Args = args
	con.VolumeDevices = []corev1.VolumeDevice{
		{
			Name:       populatorPodVolumeName,
			DevicePath: r.PopulatorPodDevicePath,
		},
	}

	if waitForFirstConsumer {
		pod.Spec.NodeName = getPvcNodeName(pvc)
	}

	if err := r.Patch(ctx, pod, client.Apply, pvcFieldOwner, client.ForceOwnership); err != nil {
		return nil, fmt.Errorf("could not create populator pod: %w", err)
	}
	return pod, nil
}

func (r *ImagePopulatorReconciler) createPvcPrimeIfNotExisting(ctx context.Context, log logr.Logger, pvc, pvcPrime *corev1.PersistentVolumeClaim, waitForFirstConsumer bool) (bool, error) {
	if pvcPrime != nil {
		log.Info("pvc is already there, skip creation.")
		return false, nil
	}

	pvcPrime = &corev1.PersistentVolumeClaim{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PersistentVolumeClaim",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      generateNameFromPrefixAndUID(populatorPvcPrefix, pvc.UID),
			Namespace: r.PopulatorNamespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources:        pvc.Spec.Resources,
			StorageClassName: pvc.Spec.StorageClassName,
			VolumeMode:       pvc.Spec.VolumeMode,
		},
	}
	if waitForFirstConsumer {
		pvcPrime.Annotations = map[string]string{
			k8svolume.AnnSelectedNode: getPvcNodeName(pvc),
		}
	}

	if err := r.Patch(ctx, pvcPrime, client.Apply, pvcFieldOwner, client.ForceOwnership); err != nil {
		return false, fmt.Errorf("could not create prime pvc %s: %w", client.ObjectKeyFromObject(pvcPrime), err)
	}

	// We'll get called again later when the pod exists
	return true, nil
}

func isStorageClassWaitingForConsumer(storageClass *storagev1.StorageClass) bool {
	if storageClass == nil {
		return false
	}

	return storageClass.VolumeBindingMode != nil && storagev1.VolumeBindingWaitForFirstConsumer == *storageClass.VolumeBindingMode
}

func getPvcNodeName(pvc *corev1.PersistentVolumeClaim) string {
	if pvc == nil {
		return ""
	}

	return pvc.Annotations[k8svolume.AnnSelectedNode]
}

func (r *ImagePopulatorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	ctx := context.Background()
	log := ctrl.Log.WithName("imagepopulator").WithName("setup")

	if err := mgr.GetCache().IndexField(ctx, &corev1.PersistentVolumeClaim{}, metadataUIDFieldName, func(obj client.Object) []string {
		return []string{string(obj.GetUID())}
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.PersistentVolumeClaim{}).
		Watches(
			&source.Kind{Type: &corev1.Pod{}},
			r.enqueuePVCfromPlaceholderPod(ctx, log)).
		Watches(
			&source.Kind{Type: &corev1.PersistentVolume{}},
			r.enqueuePVCsFromPV(),
		).
		Watches(
			&source.Kind{Type: &corev1.PersistentVolumeClaim{}},
			r.enqueuePVCFromPrimePVC(ctx, log),
		).
		Complete(r)
}
