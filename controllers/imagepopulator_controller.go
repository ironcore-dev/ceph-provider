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

	"github.com/go-logr/logr"
	"github.com/onmetal/controller-utils/clientutils"
	storagev1alpha1 "github.com/onmetal/onmetal-api/apis/storage/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	annSelectedNode         = "volume.kubernetes.io/selected-node"
	populatorPodPrefix      = "populate"
	pvcFinalizer            = "cephlet.onmetal.de/populate-target-protection"
	populatorContainerName  = "populate"
	populatorPodVolumeName  = "target"
	populatorPvcPrefix      = "prime"
	populatedFromAnnoSuffix = "populated-from"

	pvcOwnerName      = "ceplhlet.onmetal.de/ownerPVCName"
	pvcOwnerNamespace = "cephlet.onmetal.de/ownerPVCNamespace"
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
}

// Reconcile
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

	var waitForFirstConsumer bool
	var nodeName string
	if pvc.Spec.StorageClassName != nil {
		storageClassName := *pvc.Spec.StorageClassName
		storageClass := &storagev1.StorageClass{}
		storageClassKey := types.NamespacedName{Name: storageClassName}
		if err := r.Get(ctx, storageClassKey, storageClass); err != nil {
			if !errors.IsNotFound(err) {
				return ctrl.Result{}, fmt.Errorf("failed to get storageclass %s for PVC: %w", storageClassKey, err)
			}
			// We'll get called again later when the storage class exists
			return ctrl.Result{}, fmt.Errorf("storageclass %s for PVC does not exist: %w", storageClassKey, err)
		}

		log.Info("Found StorageClass for PVC", "StorageClass", storageClassKey)

		if storageClass.VolumeBindingMode != nil && storagev1.VolumeBindingWaitForFirstConsumer == *storageClass.VolumeBindingMode {
			waitForFirstConsumer = true
			nodeName = pvc.Annotations[annSelectedNode]
			if nodeName == "" {
				// Wait for the PVC to get a node name before continuing
				return ctrl.Result{}, fmt.Errorf("PVC has not been assigned to a node yet")
			}
		}
	}

	// Look for the populator pod
	podName := fmt.Sprintf("%s-%s", populatorPodPrefix, pvc.UID)
	pod := &corev1.Pod{}
	podKey := types.NamespacedName{Name: podName, Namespace: r.PopulatorNamespace}
	if err := r.Get(ctx, podKey, pod); err != nil && !errors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("failed to get populator pod %s: %w", podKey, err)
	} else if errors.IsNotFound(err) {
		// TODO: optimize not found case -> handling of nil in code below
		pod = nil
	}

	// Look for PVC'
	pvcPrimeName := fmt.Sprintf("%s-%s", populatorPvcPrefix, pvc.UID)
	pvcPrime := &corev1.PersistentVolumeClaim{}
	pvcPrimeKey := types.NamespacedName{Name: pvcPrimeName, Namespace: r.PopulatorNamespace}
	if err := r.Get(ctx, pvcPrimeKey, pvcPrime); err != nil && !errors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("failed to get shadow PVC %s: %w", pvcPrimeKey, err)
	} else if errors.IsNotFound(err) {
		pvcPrime = nil
	}

	// *** Here is the first place we start to create/modify objects ***

	// If the PVC is unbound, we need to perform the population
	if "" == pvc.Spec.VolumeName {
		// Ensure the PVC has a finalizer on it, so we can clean up the stuff we create
		if _, err := clientutils.PatchEnsureFinalizer(ctx, r.Client, pvc, pvcFinalizer); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to ensure finalizer on PVC: %w", err)
		}

		// If the pod doesn't exist yet, create it
		if pod == nil {
			if nil != pvc.Spec.VolumeMode && corev1.PersistentVolumeBlock != *pvc.Spec.VolumeMode {
				// ignore non block volumes
				return ctrl.Result{}, nil
			}

			// Calculate the args for the populator pod
			var args []string
			args = append(args, "--mode=populate")
			args = append(args, "--image="+volume.Spec.Image)

			// Make the pod
			pod = &corev1.Pod{
				TypeMeta: metav1.TypeMeta{
					Kind:       "Pod",
					APIVersion: corev1.SchemeGroupVersion.String(),
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      podName,
					Namespace: r.PopulatorNamespace,
					Annotations: map[string]string{
						pvcOwnerName:      pvc.Name,
						pvcOwnerNamespace: pvc.Namespace,
					},
				},
				Spec: makePopulatePodSpec(pvc.Name),
			}
			pod.Spec.Volumes[0].VolumeSource.PersistentVolumeClaim.ClaimName = pvcPrimeName
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
				pod.Spec.NodeName = nodeName
			}

			if err := r.Patch(ctx, pod, client.Apply, pvcFieldOwner, client.ForceOwnership); err != nil {
				return ctrl.Result{}, fmt.Errorf("could not create populator pod: %w", err)
			}

			log.Info("Created populator Pod", "Pod", podKey)

			// If PVC' doesn't exist yet, create it
			if pvcPrime == nil {

				pvcPrime = &corev1.PersistentVolumeClaim{
					TypeMeta: metav1.TypeMeta{
						Kind:       "PersistentVolumeClaim",
						APIVersion: corev1.SchemeGroupVersion.String(),
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      pvcPrimeName,
						Namespace: r.PopulatorNamespace,
						Annotations: map[string]string{
							pvcOwnerName:      pvc.Name,
							pvcOwnerNamespace: pvc.Namespace,
						},
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
						annSelectedNode: nodeName,
					}
				}

				if err := r.Patch(ctx, pvcPrime, client.Apply, pvcFieldOwner, client.ForceOwnership); err != nil {
					return ctrl.Result{}, fmt.Errorf("could not create prime pvc %s: %w", pvcPrimeKey, err)
				}

				// We'll get called again later when the pod exists
				return ctrl.Result{}, nil
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
			return ctrl.Result{}, nil
		}

		// This would be bad
		if pvcPrime == nil {
			return ctrl.Result{}, fmt.Errorf("failed to find PVC for populator pod")
		}

		// Get PV
		pv := &corev1.PersistentVolume{}
		if err := r.Get(ctx, types.NamespacedName{Name: pvcPrime.Spec.VolumeName}, pv); err != nil && !errors.IsNotFound(err) {
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
			pv.Annotations = map[string]string{populatedFromAnno: pvc.Namespace + "/" + dataSourceRef.Name}

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

func (r *ImagePopulatorReconciler) enqueuePVCFromPrimePVC() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(obj client.Object) []ctrl.Request {
		pvc := obj.(*corev1.PersistentVolumeClaim)

		pvcName, ok := pvc.Annotations[pvcOwnerName]
		if !ok {
			return nil
		}
		pvcNamespace, ok := pvc.Annotations[pvcOwnerNamespace]
		if !ok {
			return nil
		}
		return []ctrl.Request{{NamespacedName: types.NamespacedName{
			Namespace: pvcNamespace,
			Name:      pvcName,
		}}}
	})
}

func (r *ImagePopulatorReconciler) enqueuePVCfromPlaceholderPod() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(obj client.Object) []ctrl.Request {
		pod := obj.(*corev1.Pod)

		pvcName, ok := pod.Annotations[pvcOwnerName]
		if !ok {
			return nil
		}
		pvcNamespace, ok := pod.Annotations[pvcOwnerNamespace]
		if !ok {
			return nil
		}
		return []ctrl.Request{{NamespacedName: types.NamespacedName{
			Namespace: pvcNamespace,
			Name:      pvcName,
		}}}
	})
}

func (r *ImagePopulatorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.PersistentVolumeClaim{}).
		Watches(
			&source.Kind{Type: &corev1.Pod{}},
			r.enqueuePVCfromPlaceholderPod()).
		Watches(
			&source.Kind{Type: &corev1.PersistentVolume{}},
			r.enqueuePVCsFromPV(),
		).
		Watches(
			&source.Kind{Type: &corev1.PersistentVolumeClaim{}},
			r.enqueuePVCFromPrimePVC(),
		).
		Complete(r)
}
