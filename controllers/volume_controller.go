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
	"encoding/json"
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	"github.com/google/uuid"
	"github.com/onmetal/cephlet/pkg/ceph"
	"github.com/onmetal/cephlet/pkg/rook"
	storagev1alpha1 "github.com/onmetal/onmetal-api/apis/storage/v1alpha1"
	rookv1 "github.com/rook/rook/pkg/apis/ceph.rook.io/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/pointer"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	userIDKey    = "userID"
	userKeyKey   = "userKey"
	volumeDriver = "ceph"

	pvPoolKey      = "pool"
	pvImageNameKey = "imageName"

	// worldwide number key
	wwnKey string = "WWN"
	// to use WWN Company Identifiers, set wwnPrefix to Private "1100AA"
	wwnPrefix string = ""
)

var (
	volumeFieldOwner = client.FieldOwner("cephlet.onmetal.de/volume")
)

// VolumeReconciler reconciles a Volume object
type VolumeReconciler struct {
	client.Client
	Scheme         *runtime.Scheme
	VolumePoolName string
	RookConfig     *rook.Config
}

//+kubebuilder:rbac:groups=storage.api.onmetal.de,resources=volumes,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=storage.api.onmetal.de,resources=volumes/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=storage.api.onmetal.de,resources=volumes/finalizers,verbs=update

//+kubebuilder:rbac:groups=ceph.rook.io,resources=cephblockpoolradosnamespaces,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=ceph.rook.io,resources=cephclients,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *VolumeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	volume := &storagev1alpha1.Volume{}
	if err := r.Get(ctx, req.NamespacedName, volume); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	return r.reconcileExists(ctx, log, volume)
}

func (r *VolumeReconciler) reconcileExists(ctx context.Context, log logr.Logger, volume *storagev1alpha1.Volume) (ctrl.Result, error) {
	if !volume.DeletionTimestamp.IsZero() {
		return r.delete(ctx, log, volume)
	}
	return r.reconcile(ctx, log, volume)
}

func (r *VolumeReconciler) reconcile(ctx context.Context, log logr.Logger, volume *storagev1alpha1.Volume) (ctrl.Result, error) {
	log.V(1).Info("Reconciling Volume")

	storageClass, requeue, err := r.applyStorageClass(ctx, log, volume)
	switch {
	case requeue:
		return ctrl.Result{Requeue: true}, nil
	case err != nil:
		return ctrl.Result{}, fmt.Errorf("failed to create storage class for volume: %w", err)
	}

	pvc, requeue, err := r.applyPVC(ctx, log, volume, storageClass)
	switch {
	case requeue:
		return ctrl.Result{Requeue: true}, nil
	case err != nil:
		return ctrl.Result{}, fmt.Errorf("failed to apply PVC for volume: %w", err)
	}

	secretName, requeue, err := r.applyCephClient(ctx, log, volume)
	switch {
	case requeue:
		return ctrl.Result{Requeue: true}, nil
	case err != nil:
		return ctrl.Result{}, fmt.Errorf("failed to provide secrets for volume: %w", err)
	}

	if err := r.applySecretAndUpdateVolumeStatus(ctx, log, volume, secretName, pvc); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to apply secret for volume: %w", err)
	}

	return ctrl.Result{}, nil
}

func (r *VolumeReconciler) delete(ctx context.Context, log logr.Logger, volume *storagev1alpha1.Volume) (ctrl.Result, error) {
	return ctrl.Result{}, nil
}

func generateOrGetWWN(volumeStatus storagev1alpha1.VolumeStatus) (string, error) {
	if volumeStatus.Access == nil {
		return generateWWN()
	}

	wwn, found := volumeStatus.Access.VolumeAttributes[wwnKey]
	if !found {
		return generateWWN()
	}

	return wwn, nil
}

// generate WWN as hex string (16 chars)
func generateWWN() (string, error) {
	// prefix is optional, set to 1100AA for private identifier
	wwn := wwnPrefix

	//TODO other random function ?

	// use UUIDv4, because this will generate good random string
	wwnUUID, err := uuid.NewRandom()
	if err != nil {
		return "", fmt.Errorf("failed to generate UUIDv4 for WWN: %w", err)
	}

	// append hex string without "-"
	wwn += strings.Replace(wwnUUID.String(), "-", "", -1)

	// WWN is 64Bit number as hex, so only the first 16 chars are returned
	return wwn[:16], nil
}

func (r *VolumeReconciler) getImageKeyFromPV(ctx context.Context, log logr.Logger, volume *storagev1alpha1.Volume, pvc *corev1.PersistentVolumeClaim) (string, error) {
	pv := &corev1.PersistentVolume{}
	if err := r.Get(ctx, types.NamespacedName{Name: pvc.Spec.VolumeName, Namespace: volume.Namespace}, pv); err != nil {
		return "", fmt.Errorf("unable to get pv: %w", err)
	}

	pool, ok := pv.Spec.CSI.VolumeAttributes[pvPoolKey]
	if !ok {
		return "", fmt.Errorf("missing PV volumeAttribute: %s", pvPoolKey)
	}
	imageName, ok := pv.Spec.CSI.VolumeAttributes[pvImageNameKey]
	if !ok {
		return "", fmt.Errorf("missing PV volumeAttribute: %s", pvImageNameKey)
	}

	return fmt.Sprintf("%s/%s", pool, imageName), nil
}

func (r *VolumeReconciler) applyPVC(ctx context.Context, log logr.Logger, volume *storagev1alpha1.Volume, storageClass string) (*corev1.PersistentVolumeClaim, bool, error) {
	pvc := &corev1.PersistentVolumeClaim{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PersistentVolumeClaim",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      volume.Name,
			Namespace: volume.Namespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: volume.Spec.Resources[corev1.ResourceStorage]},
			},
			VolumeMode:       func(m corev1.PersistentVolumeMode) *corev1.PersistentVolumeMode { return &m }(corev1.PersistentVolumeBlock),
			StorageClassName: &storageClass,
		},
	}

	if volume.Spec.Image != "" {
		pvc.Spec.DataSourceRef = &corev1.TypedLocalObjectReference{
			APIGroup: pointer.StringPtr(storagev1alpha1.SchemeGroupVersion.String()),
			Kind:     "Volume",
			Name:     volume.Name,
		}
	}

	if err := ctrl.SetControllerReference(volume, pvc, r.Scheme); err != nil {
		return nil, false, err
	}

	if err := r.Patch(ctx, pvc, client.Apply, volumeFieldOwner, client.ForceOwnership); err != nil {
		return nil, false, err
	}

	if pvc.Status.Phase != corev1.ClaimBound {
		log.V(1).Info("pvc is not yet in ClaimBound state")
		return nil, true, nil
	}

	// TODO: do proper status reporting
	volumeBase := volume.DeepCopy()
	volume.Status.State = storagev1alpha1.VolumeStateAvailable
	if err := r.Status().Patch(ctx, volume, client.MergeFrom(volumeBase)); err != nil {
		return nil, false, fmt.Errorf("failed to patch volume state: %w", err)
	}

	log.V(3).Info("volume provided.")
	return pvc, false, nil
}

func (r *VolumeReconciler) applyCephClient(ctx context.Context, log logr.Logger, volume *storagev1alpha1.Volume) (string, bool, error) {
	ns := &corev1.Namespace{}
	err := r.Client.Get(ctx, client.ObjectKey{Name: volume.Namespace}, ns)
	if err != nil {
		return "", false, fmt.Errorf("failed to get namespace for volume: %w", err)
	}

	cephClient := &rookv1.CephClient{
		TypeMeta: metav1.TypeMeta{
			Kind:       "CephClient",
			APIVersion: "ceph.rook.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      volume.Namespace,
			Namespace: r.RookConfig.Namespace,
		},
		Spec: rookv1.ClientSpec{
			Name: "",
			Caps: map[string]string{
				"mgr": fmt.Sprintf("profile rbd pool=%s namespace=%s", r.VolumePoolName, volume.Namespace),
				"mon": "profile rbd",
				"osd": fmt.Sprintf("profile rbd pool=%s namespace=%s", r.VolumePoolName, volume.Namespace),
			},
		},
	}

	if err := ctrl.SetControllerReference(ns, cephClient, r.Scheme); err != nil {
		return "", false, fmt.Errorf("failed to set ownerreference for ceph client %s: %w", client.ObjectKeyFromObject(cephClient), err)
	}

	if err := r.Patch(ctx, cephClient, client.Apply, volumeFieldOwner, client.ForceOwnership); err != nil {
		return "", false, fmt.Errorf("failed to patch ceph client %s: %w", client.ObjectKeyFromObject(cephClient), err)
	}

	if cephClient.Status == nil || cephClient.Status.Phase != rookv1.ConditionReady {
		log.V(1).Info("ceph client is not ready yet", "client", client.ObjectKeyFromObject(cephClient))
		return "", true, nil
	}

	secretName, found := cephClient.Status.Info["secretName"]
	if !found {
		return "", false, fmt.Errorf("failed to get secret name from ceph client %s status: %w", client.ObjectKeyFromObject(cephClient), err)
	}

	return secretName, false, nil
}

func (r *VolumeReconciler) applySecretAndUpdateVolumeStatus(ctx context.Context, log logr.Logger, volume *storagev1alpha1.Volume, secretName string, pvc *corev1.PersistentVolumeClaim) error {
	defer log.V(2).Info("applySecretAndUpdateVolumeStatus done")
	cephClientSecret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: r.RookConfig.Namespace, Name: secretName}, cephClientSecret); err != nil {
		return fmt.Errorf("unable to get secret: %w", err)
	}
	if cephClientSecret.Data == nil {
		return fmt.Errorf("secret %s data empty", client.ObjectKeyFromObject(cephClientSecret))
	}

	credentials, ok := cephClientSecret.Data[volume.Namespace]
	if !ok {
		return fmt.Errorf("secret %s does not contain data key %s", client.ObjectKeyFromObject(cephClientSecret), volume.Namespace)
	}

	volumeSecret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      volume.Name,
			Namespace: volume.Namespace,
		},
		Data: map[string][]byte{
			userIDKey:  []byte(volume.Namespace),
			userKeyKey: credentials,
		},
		Type: corev1.SecretTypeOpaque,
	}

	if err := ctrl.SetControllerReference(volume, volumeSecret, r.Scheme); err != nil {
		return fmt.Errorf("failed to set ownerref for volume secret %s: %w", client.ObjectKeyFromObject(volumeSecret), err)
	}

	if err := r.Patch(ctx, volumeSecret, client.Apply, volumeFieldOwner, client.ForceOwnership); err != nil {
		return fmt.Errorf("failed to apply volume secret %s: %w", client.ObjectKeyFromObject(volumeSecret), err)
	}

	monitors, err := r.getMonitorListForVolume(ctx, volume)
	if err != nil {
		return fmt.Errorf("failed to get monitor list for volume: %w", err)
	}

	imageKey, err := r.getImageKeyFromPV(ctx, log, volume, pvc)
	if err != nil {
		return fmt.Errorf("failed to provide image name: %w", err)
	}

	// TODO:
	// Currently we only create a WWN and add it to the volume status. Ideally we want to have it as
	// metadata attached to the real Ceph block device.
	wwn, err := generateOrGetWWN(volume.Status)
	if err != nil {
		return fmt.Errorf("error creating WWN: %w", err)
	}

	volumeBase := volume.DeepCopy()
	volume.Status.Access = &storagev1alpha1.VolumeAccess{
		SecretRef: &corev1.LocalObjectReference{
			Name: volume.Name,
		},
		Driver: volumeDriver,
		VolumeAttributes: map[string]string{
			"monitors": strings.Join(monitors, ","),
			"image":    imageKey,
			wwnKey:     wwn,
		},
	}
	return r.Status().Patch(ctx, volume, client.MergeFrom(volumeBase))
}

func (r *VolumeReconciler) getMonitorListForVolume(ctx context.Context, volume *storagev1alpha1.Volume) ([]string, error) {
	rookConfigMap := &corev1.ConfigMap{}
	if err := r.Get(ctx, types.NamespacedName{Name: r.RookConfig.MonitorConfigMapName, Namespace: r.RookConfig.Namespace}, rookConfigMap); err != nil {
		return nil, fmt.Errorf("failed to get ceph monitors configMap %s: %w", client.ObjectKeyFromObject(rookConfigMap), err)
	}
	dataKey := r.RookConfig.MonitorConfigMapDataKey
	var list ceph.ClusterList
	if val, ok := rookConfigMap.Data[dataKey]; !ok {
		return nil, fmt.Errorf("unable to find data key %s in rook configMap %s", dataKey, client.ObjectKeyFromObject(rookConfigMap))
	} else if err := json.Unmarshal([]byte(val), &list); err != nil {
		return nil, fmt.Errorf("failed to decode ceph cluster list in rook config map %s: %w", client.ObjectKeyFromObject(rookConfigMap), err)
	}
	var monitors []string
	for _, cluster := range list {
		if cluster.ClusterID == r.RookConfig.ClusterId {
			monitors = cluster.Monitors
			break
		}
	}
	if len(monitors) == 0 {
		return nil, fmt.Errorf("no monitors provided for clusterID %s in configMap %s", r.RookConfig.ClusterId, client.ObjectKeyFromObject(rookConfigMap))
	}
	return monitors, nil
}

func (r *VolumeReconciler) applyStorageClass(ctx context.Context, log logr.Logger, volume *storagev1alpha1.Volume) (string, bool, error) {
	ns := &corev1.Namespace{}
	if err := r.Client.Get(ctx, client.ObjectKey{Name: volume.Namespace}, ns); err != nil {
		return "", false, err
	}

	cephNs := &rookv1.CephBlockPoolRadosNamespace{
		TypeMeta: metav1.TypeMeta{
			Kind:       "CephBlockPoolRadosNamespace",
			APIVersion: "ceph.rook.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      volume.Namespace,
			Namespace: r.RookConfig.Namespace,
		},
		Spec: rookv1.CephBlockPoolRadosNamespaceSpec{
			BlockPoolName: volume.Spec.VolumePoolRef.Name,
		},
	}

	if err := ctrl.SetControllerReference(ns, cephNs, r.Scheme); err != nil {
		return "", false, fmt.Errorf("failed to set controller reference for volume: %w", err)
	}

	if err := r.Patch(ctx, cephNs, client.Apply, volumeFieldOwner, client.ForceOwnership); err != nil {
		return "", false, fmt.Errorf("failed to patch cephNs for volume: %w", err)
	}

	if cephNs.Status == nil || cephNs.Status.Phase != rookv1.ConditionReady {
		log.V(1).Info("empty cephNS status found", "cephNS", client.ObjectKeyFromObject(cephNs))
		return "", true, nil
	}

	clusterID, found := cephNs.Status.Info["clusterID"]
	if !found {
		return "", false, fmt.Errorf("no clusterId in status for cephNS %s for volume %s", client.ObjectKeyFromObject(cephNs), client.ObjectKeyFromObject(volume))
	}

	storageClass := &storagev1.StorageClass{
		TypeMeta: metav1.TypeMeta{
			Kind:       "StorageClass",
			APIVersion: "storage.k8s.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: clusterID + "--" + volume.Spec.VolumePoolRef.Name,
		},
		Provisioner: r.RookConfig.CSIDriverName,
		Parameters: map[string]string{
			"clusterID":     clusterID,
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

	if err := ctrl.SetControllerReference(ns, storageClass, r.Scheme); err != nil {
		return "", false, fmt.Errorf("failed to set controller reference for storageClass %s: %w", client.ObjectKeyFromObject(storageClass), err)
	}

	if err := r.Patch(ctx, storageClass, client.Apply, volumeFieldOwner, client.ForceOwnership); err != nil {
		return "", false, fmt.Errorf("failed to patch storageClass %s for volume %s: %w", client.ObjectKeyFromObject(storageClass), client.ObjectKeyFromObject(volume), err)
	}

	log.V(1).Info("Applied StorageClass")

	return storageClass.Name, false, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *VolumeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&storagev1alpha1.Volume{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&rookv1.CephBlockPoolRadosNamespace{}).
		Owns(&rookv1.CephClient{}).
		Complete(r)
}
