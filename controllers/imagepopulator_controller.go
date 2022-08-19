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
)

const (
	annSelectedNode         = "volume.kubernetes.io/selected-node"
	populatorPodPrefix      = "populate"
	pvcFinalizer            = "cephlet.onmetal.de/populate-target-protection"
	populatorContainerName  = "populate"
	populatorPodVolumeName  = "target"
	populatorPvcPrefix      = "prime"
	populatedFromAnnoSuffix = "populated-from"
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

	dataSourceRef := pvc.Spec.DataSourceRef
	if dataSourceRef == nil {
		// Ignore PVCs without a datasource
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
	var pod *corev1.Pod
	podKey := types.NamespacedName{Name: podName, Namespace: r.PopulatorNamespace}
	if err := r.Get(ctx, podKey, pod); !errors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("failed to get populator pod %s: %w", podKey, err)
	}

	// Look for PVC'
	pvcPrimeName := fmt.Sprintf("%s-%s", populatorPvcPrefix, pvc.UID)
	var pvcPrime *corev1.PersistentVolumeClaim
	pvcPrimeKey := types.NamespacedName{Name: pvcPrimeName, Namespace: r.PopulatorNamespace}
	if err := r.Get(ctx, pvcPrimeKey, pvcPrime); !errors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("failed to get shadow PVC %s: %w", pvcPrimeKey, err)
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
				ObjectMeta: metav1.ObjectMeta{
					Name:      podName,
					Namespace: r.PopulatorNamespace,
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

			if err := ctrl.SetControllerReference(pvc, pod, r.Scheme); err != nil {
				return ctrl.Result{}, fmt.Errorf("error owning populator pod %s: %w", podKey, err)
			}

			if err := r.Patch(ctx, pod, client.Apply, pvcFieldOwner, client.ForceOwnership); err != nil {
				return ctrl.Result{}, fmt.Errorf("could not apply populator pod: %w", err)
			}

			// If PVC' doesn't exist yet, create it
			if pvcPrime == nil {
				pvcPrime = &corev1.PersistentVolumeClaim{
					ObjectMeta: metav1.ObjectMeta{
						Name:      pvcPrimeName,
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
						annSelectedNode: nodeName,
					}
				}

				if err := r.Patch(ctx, pvcPrime, client.Apply, pvcFieldOwner, client.ForceOwnership); err != nil {
					return ctrl.Result{}, fmt.Errorf("could not apply prime pvc %s: %w", pvcPrimeKey, err)
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
		if err := r.Get(ctx, types.NamespacedName{Name: pvcPrime.Spec.VolumeName}, pv); !errors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("failed to get pv for prime pvc %s: %w", pvcPrimeKey, err)
		}

		// Examine the claimref for the PV and see if it's bound to the correct PVC
		pvBase := pv.DeepCopy()
		claimRef := pv.Spec.ClaimRef
		if claimRef.Name != pvc.Name || claimRef.Namespace != pvc.Namespace || claimRef.UID != pvc.UID {
			// Make new PV with strategic patch values to perform the PV rebind
			patchPv := corev1.PersistentVolume{
				ObjectMeta: metav1.ObjectMeta{
					Name:        pv.Name,
					Annotations: map[string]string{},
				},
				Spec: corev1.PersistentVolumeSpec{
					ClaimRef: &corev1.ObjectReference{
						Namespace:       pvc.Namespace,
						Name:            pvc.Name,
						UID:             pvc.UID,
						ResourceVersion: pvc.ResourceVersion,
					},
				},
			}
			populatedFromAnno := r.Prefix + "/" + populatedFromAnnoSuffix
			patchPv.Annotations[populatedFromAnno] = pvc.Namespace + "/" + dataSourceRef.Name

			if err := r.Patch(ctx, pv, client.MergeFrom(pvBase)); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to patch pv %s: %w", client.ObjectKeyFromObject(pv), err)
			}

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
		if err := r.Delete(ctx, pod); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to clean up populator pod %s: %w", podKey, err)
		}
	}

	// If PVC' still exists, delete it
	if pvcPrime != nil {
		if err := r.Delete(ctx, pvcPrime); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to delete prime pvc %s: %w", pvcPrimeKey, err)
		}
	}

	// Make sure the PVC finalizer is gone
	if _, err := clientutils.PatchEnsureNoFinalizer(ctx, r.Client, pvc, pvcFinalizer); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to remove finalizer from pvc: %w", err)
	}

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

func (r *ImagePopulatorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.PersistentVolumeClaim{}).
		Owns(&corev1.Pod{}).
		Complete(r)
}
