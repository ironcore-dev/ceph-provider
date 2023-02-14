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
	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v6/apis/volumesnapshot/v1"
	"github.com/onmetal/cephlet/pkg/ceph"
	"github.com/onmetal/cephlet/pkg/rook"
	corev1alpha1 "github.com/onmetal/onmetal-api/api/core/v1alpha1"
	storagev1alpha1 "github.com/onmetal/onmetal-api/api/storage/v1alpha1"
	onmetalimage "github.com/onmetal/onmetal-image"
	"github.com/onmetal/onmetal-image/oci/image"
	"github.com/onmetal/onmetal-image/oci/remote"
	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/pointer"
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
	userIDKey    = "userID"
	userKeyKey   = "userKey"
	volumeDriver = "ceph"

	pvPoolKey           = "pool"
	pvImageNameKey      = "imageName"
	pvRadosNamespaceKey = "radosNamespace"

	// worldwide number key
	// to use WWN Company Identifiers, set wwnPrefix to Private "1100AA"
	wwnPrefix string = ""

	volumePoolRefIndex = ".spec.volumePoolRef.name"
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
	CephClient     ceph.Client

	record.EventRecorder

	PoolUsage *prometheus.GaugeVec
}

//+kubebuilder:rbac:groups=storage.api.onmetal.de,resources=volumes,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=storage.api.onmetal.de,resources=volumes/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=storage.api.onmetal.de,resources=volumes/finalizers,verbs=update
//+kubebuilder:rbac:groups=storage.api.onmetal.de,resources=volumepools,verbs=get;list;watch
//+kubebuilder:rbac:groups=storage.api.onmetal.de,resources=volumepools/status,verbs=get

//+kubebuilder:rbac:groups=snapshot.storage.k8s.io,resources=volumesnapshots,verbs=get;list;watch;create;update;patch;delete

//+kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=persistentvolumeclaims/status,verbs=get
//+kubebuilder:rbac:groups=core,resources=persistentvolumes,verbs=get;list;watch;delete
//+kubebuilder:rbac:groups=core,resources=persistentvolumes/status,verbs=get
//+kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch
//+kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=core,resources=namespaces,verbs=get;list;watch

//+kubebuilder:rbac:groups="",resources=events,verbs=create;patch

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

func GetSanitizedImageNameFromVolume(volume *storagev1alpha1.Volume) string {
	image := volume.Spec.Image
	image = strings.ReplaceAll(image, "/", "-")
	image = strings.ReplaceAll(image, ":", "-")
	return strings.ReplaceAll(image, "@", "-")
}

func (r *VolumeReconciler) reconcile(ctx context.Context, log logr.Logger, volume *storagev1alpha1.Volume) (ctrl.Result, error) {
	log.V(1).Info("Reconciling Volume")

	if volume.Status.State == "" {
		volumeBase := volume.DeepCopy()
		volume.Status.State = storagev1alpha1.VolumeStatePending
		if err := r.Status().Patch(ctx, volume, client.MergeFrom(volumeBase)); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to patch state to pending: %w", err)
		}
	}

	volumePool := &storagev1alpha1.VolumePool{}
	waitUntilRefIsPresent, err := r.checkVolumePoolRef(ctx, log, volume, volumePool)
	if err != nil || waitUntilRefIsPresent {
		return ctrl.Result{}, err
	}

	if err := r.handleImagePopulation(ctx, log, volume); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to initiate image population: %w", err)
	}

	pvc, waitUntilClaimBound, err := r.applyPVC(ctx, log, volume)
	if err != nil || waitUntilClaimBound {
		return ctrl.Result{}, err
	}

	if err := r.applyLimits(ctx, log, volume, pvc); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to limit volume: %w", err)
	}

	if err := r.applySecretAndUpdateVolumeStatus(ctx, log, volume, volumePool, pvc); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to apply secret for volume: %w", err)
	}

	if err := r.updatePoolUsageMetrics(ctx, volumePool); err != nil {
		log.Error(err, "unable to update pool usage metrics")
	}

	return ctrl.Result{}, nil
}

func (r *VolumeReconciler) checkVolumePoolRef(ctx context.Context, log logr.Logger, volume *storagev1alpha1.Volume, pool *storagev1alpha1.VolumePool) (bool, error) {
	if volume.Spec.VolumePoolRef == nil {
		log.V(1).Info("Skipped reconcile: VolumePoolRef not present")
		return true, nil
	}

	if err := r.Get(ctx, types.NamespacedName{Name: volume.Spec.VolumePoolRef.Name}, pool); client.IgnoreNotFound(err) != nil {
		return false, fmt.Errorf("failed to get volume pool %s : %w", volume.Spec.VolumePoolRef.Name, err)
	} else if errors.IsNotFound(err) {
		log.V(1).Info("Skipped reconcile: VolumePool does not exist", "pool", volume.Spec.VolumePoolRef.Name)
		return true, nil
	}

	if pool.Status.State != storagev1alpha1.VolumePoolStateAvailable {
		log.V(1).Info("Skipped reconcile: VolumePool is not ready")
		return true, nil
	}

	return false, nil
}

func (r *VolumeReconciler) applyLimits(ctx context.Context, log logr.Logger, volume *storagev1alpha1.Volume, pvc *corev1.PersistentVolumeClaim) error {
	volumeClass := &storagev1alpha1.VolumeClass{}
	if err := r.Get(ctx, types.NamespacedName{Name: volume.Spec.VolumeClassRef.Name}, volumeClass); err != nil {
		return fmt.Errorf("unable to get VolumeClass: %w", err)
	}

	limits, err := ceph.CalculateLimits(volume, volumeClass, r.RookConfig.BurstFactor, r.RookConfig.BurstDurationInSeconds)
	if err != nil {
		return fmt.Errorf("unable to calculate volume limits: %w", err)
	}

	if len(limits) == 0 {
		log.Info("No limits to apply.")
		return nil
	}

	pv := &corev1.PersistentVolume{}
	if err := r.Get(ctx, client.ObjectKey{Name: pvc.Spec.VolumeName}, pv); err != nil {
		return fmt.Errorf("unable to get pv: %w", err)
	}

	imageName, ok := pv.Spec.CSI.VolumeAttributes["imageName"]
	if !ok || imageName == "" {
		return fmt.Errorf("csi volume attribute 'imageName' is missing")
	}

	if err := r.CephClient.SetVolumeLimit(ctx, volume.Spec.VolumePoolRef.Name, imageName, "", limits); err != nil {
		return fmt.Errorf("unable to apply limits (%+v): %w", limits, err)
	}

	log.Info("Successfully applied limits")
	return nil
}

func (r *VolumeReconciler) delete(ctx context.Context, log logr.Logger, volume *storagev1alpha1.Volume) (ctrl.Result, error) {
	volumePool := &storagev1alpha1.VolumePool{}
	if waitUntilRefIsPresent, err := r.checkVolumePoolRef(ctx, log, volume, volumePool); err != nil || waitUntilRefIsPresent {
		return ctrl.Result{}, err
	}

	if err := r.updatePoolUsageMetrics(ctx, volumePool); err != nil {
		log.Error(err, "unable to update pool usage metrics")
	}

	return ctrl.Result{}, nil
}

func generateOrGetWWN(volumeStatus storagev1alpha1.VolumeStatus) (string, error) {
	if volumeStatus.Access == nil || volumeStatus.Access.Handle == "" {
		return generateWWN()
	}

	return volumeStatus.Access.Handle, nil
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

	var parts []string
	parts = append(parts, pool)

	radosNamespace, ok := pv.Spec.CSI.VolumeAttributes[pvRadosNamespaceKey]
	if ok {
		parts = append(parts, radosNamespace)
	}

	imageName, ok := pv.Spec.CSI.VolumeAttributes[pvImageNameKey]
	if !ok {
		return "", fmt.Errorf("missing PV volumeAttribute: %s", pvImageNameKey)
	}

	parts = append(parts, imageName)

	result := strings.Join(parts, "/")
	log.V(3).Info(fmt.Sprintf("Get image key %s from pv %s", result, client.ObjectKeyFromObject(pv)))

	return result, nil
}

func (r *VolumeReconciler) applyPVC(ctx context.Context, log logr.Logger, volume *storagev1alpha1.Volume) (*corev1.PersistentVolumeClaim, bool, error) {
	storageClass := GetClusterVolumePoolName(r.RookConfig.ClusterId, volume.Spec.VolumePoolRef.Name)
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
				Requests: corev1.ResourceList{corev1.ResourceStorage: volume.Spec.Resources[corev1alpha1.ResourceStorage]},
			},
			VolumeMode:       func(m corev1.PersistentVolumeMode) *corev1.PersistentVolumeMode { return &m }(corev1.PersistentVolumeBlock),
			StorageClassName: &storageClass,
		},
	}

	if volume.Spec.Image != "" {
		pvc.Spec.DataSourceRef = &corev1.TypedObjectReference{
			APIGroup: pointer.String("snapshot.storage.k8s.io"),
			Kind:     "VolumeSnapshot",
			Name:     GetSanitizedImageNameFromVolume(volume),
		}
	}

	if err := ctrl.SetControllerReference(volume, pvc, r.Scheme); err != nil {
		return nil, false, fmt.Errorf("failed to set ownerref for volume pvc %s: %w", client.ObjectKeyFromObject(pvc), err)
	}

	if _, err := controllerutil.CreateOrPatch(ctx, r.Client, pvc, func() error { return nil }); err != nil {
		return nil, false, fmt.Errorf("failed to apply volume pvc %s: %w", client.ObjectKeyFromObject(pvc), err)
	}

	if pvc.Status.Phase != corev1.ClaimBound {
		log.V(1).Info("Pvc is not yet in ClaimBound state")
		return nil, true, nil
	}

	log.V(3).Info("Volume provided.")
	return pvc, false, nil
}
func (r *VolumeReconciler) handleImagePopulation(ctx context.Context, log logr.Logger, volume *storagev1alpha1.Volume) error {
	if volume.Spec.Image == "" {
		return nil
	}

	snapshot := &snapshotv1.VolumeSnapshot{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: volume.Namespace, Name: GetSanitizedImageNameFromVolume(volume)}, snapshot); err != nil {
		if !errors.IsNotFound(err) {
			return fmt.Errorf("unable to get snapshot: %w", err)
		}
		log.V(1).Info("Requested snapshot not found: create image pvc and snapshot it.")
		return r.createSnapshot(ctx, log, volume)
	}

	volumeCapacity := volume.Spec.Resources[corev1alpha1.ResourceStorage]
	if !pointer.BoolDeref(snapshot.Status.ReadyToUse, false) || snapshot.Status.RestoreSize == nil {
		log.Info("Referenced snapshot not ready or RestoreSize not defined", "snapshotName", snapshot.Name)
		return nil
	}

	volumeSizeBytes := volumeCapacity.Value()
	if volumeSizeBytes < snapshot.Status.RestoreSize.Value() {
		log.Info(fmt.Sprintf("Requested volume size %d is less than the size %d for the source snapshot", volumeSizeBytes, snapshot.Status.RestoreSize.Value()), "snapshotName", snapshot.Name)
		r.Eventf(volume, corev1.EventTypeWarning, ReasonVolumeSizeToSmall, "Requested volume size %d is less than the size %d for the source snapshot %s", volumeSizeBytes, snapshot.Status.RestoreSize.Value(), snapshot.Name)
	}

	return nil
}

func getImageSize(ctx context.Context, volume *storagev1alpha1.Volume) (*resource.Quantity, error) {
	reg, err := remote.DockerRegistry(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize registry: %w", err)
	}

	img, err := reg.Resolve(ctx, volume.Spec.Image)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve image ref in registry: %w", err)
	}

	layers, err := img.Layers(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get layers for image: %w", err)
	}

	var rootFSLayer image.Layer
	for _, l := range layers {
		if l.Descriptor().MediaType == onmetalimage.RootFSLayerMediaType {
			rootFSLayer = l
			break
		}
	}
	if rootFSLayer == nil {
		return nil, fmt.Errorf("failed to get rootFS layer")
	}

	size := resource.NewQuantity(rootFSLayer.Descriptor().Size, resource.BinarySI)
	if size == nil {
		return nil, fmt.Errorf("failed to get size of rootFS layer")
	}

	return size, nil
}

func (r *VolumeReconciler) createSnapshot(ctx context.Context, log logr.Logger, volume *storagev1alpha1.Volume) error {
	size, err := getImageSize(ctx, volume)
	if err != nil {
		return fmt.Errorf("unable to get image size: %w", err)
	}

	imagePvc := &corev1.PersistentVolumeClaim{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PersistentVolumeClaim",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      GetSanitizedImageNameFromVolume(volume),
			Namespace: volume.Namespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: *size},
			},
			VolumeMode:       func(m corev1.PersistentVolumeMode) *corev1.PersistentVolumeMode { return &m }(corev1.PersistentVolumeBlock),
			StorageClassName: pointer.String(GetClusterVolumePoolName(r.RookConfig.ClusterId, volume.Spec.VolumePoolRef.Name)),
			//set DataSourceRef that populator picks up the pvc
			DataSourceRef: &corev1.TypedObjectReference{
				APIGroup: pointer.String(storagev1alpha1.SchemeGroupVersion.String()),
				Kind:     "Volume",
				Name:     volume.Name,
			},
		},
	}

	if err := r.Patch(ctx, imagePvc, client.Apply, volumeFieldOwner, client.ForceOwnership); err != nil {
		return fmt.Errorf("unable to patch image pvc: %w", err)
	}
	log.V(1).Info(fmt.Sprintf("created image pvc %s", client.ObjectKeyFromObject(imagePvc)))

	snapshot := &snapshotv1.VolumeSnapshot{
		TypeMeta: metav1.TypeMeta{
			Kind:       "VolumeSnapshot",
			APIVersion: snapshotv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      GetSanitizedImageNameFromVolume(volume),
			Namespace: volume.Namespace,
		},
		Spec: snapshotv1.VolumeSnapshotSpec{
			Source: snapshotv1.VolumeSnapshotSource{
				PersistentVolumeClaimName: &imagePvc.Name,
			},
			VolumeSnapshotClassName: pointer.String(GetClusterVolumePoolName(r.RookConfig.ClusterId, volume.Spec.VolumePoolRef.Name)),
		},
	}
	if err := r.Patch(ctx, snapshot, client.Apply, volumeFieldOwner, client.ForceOwnership); err != nil {
		return fmt.Errorf("unable to patch snapshot: %w", err)
	}
	log.V(1).Info(fmt.Sprintf("created snapshot %s for image pvc %s", client.ObjectKeyFromObject(snapshot), client.ObjectKeyFromObject(imagePvc)))

	return nil
}

func (r *VolumeReconciler) applySecretAndUpdateVolumeStatus(ctx context.Context, log logr.Logger, volume *storagev1alpha1.Volume, pool *storagev1alpha1.VolumePool, pvc *corev1.PersistentVolumeClaim) error {
	defer log.V(2).Info("applySecretAndUpdateVolumeStatus done")

	secretName, ok := pool.Annotations[volumePoolSecretAnnotation]
	if !ok {
		return fmt.Errorf("volume pool %s does not contain '%s' annotation", client.ObjectKeyFromObject(pool), volumePoolSecretAnnotation)
	}

	cephClientSecret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Namespace: r.RookConfig.Namespace, Name: secretName}, cephClientSecret); err != nil {
		return fmt.Errorf("unable to get secret: %w", err)
	}
	if cephClientSecret.Data == nil {
		return fmt.Errorf("secret %s data empty", client.ObjectKeyFromObject(cephClientSecret))
	}

	// Data key of secret is equivalent to CephClient name
	credentials, ok := cephClientSecret.Data[GetClusterVolumePoolName(r.RookConfig.ClusterId, pool.Name)]
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
			userIDKey:  []byte(GetClusterVolumePoolName(r.RookConfig.ClusterId, pool.Name)),
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

	monitors, err := r.getMonitorList(ctx)
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
	volume.Status.State = storagev1alpha1.VolumeStateAvailable

	volume.Status.Access = &storagev1alpha1.VolumeAccess{
		SecretRef: &corev1.LocalObjectReference{
			Name: volume.Name,
		},
		Driver: volumeDriver,
		Handle: wwn,
		VolumeAttributes: map[string]string{
			"monitors": strings.Join(monitors, ","),
			"image":    imageKey,
		},
	}
	return r.Status().Patch(ctx, volume, client.MergeFrom(volumeBase))
}

func (r *VolumeReconciler) getMonitorList(ctx context.Context) ([]string, error) {
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

func (r *VolumeReconciler) findObjectsForVolumePool(pool client.Object) []reconcile.Request {
	volumes := &storagev1alpha1.VolumeList{}
	if err := r.List(context.TODO(), volumes, &client.ListOptions{
		FieldSelector: fields.OneTermEqualSelector(volumePoolRefIndex, pool.GetName()),
	}); err != nil {
		return nil
	}

	var requests []reconcile.Request
	for _, volume := range volumes.Items {
		requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{
			Namespace: volume.Namespace,
			Name:      volume.Name,
		}})
	}
	return requests
}

// SetupWithManager sets up the controller with the Manager.
func (r *VolumeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	var err error
	if r.CephClient == nil {
		r.CephClient, err = ceph.NewClient(mgr.GetClient(), r.RookConfig)
		if err != nil {
			return err
		}
	}

	if err := mgr.GetFieldIndexer().IndexField(context.TODO(), &storagev1alpha1.Volume{}, volumePoolRefIndex, func(rawObj client.Object) []string {
		configDeployment := rawObj.(*storagev1alpha1.Volume)
		if configDeployment.Spec.VolumePoolRef == nil || configDeployment.Spec.VolumePoolRef.Name == "" {
			return nil
		}
		return []string{configDeployment.Spec.VolumePoolRef.Name}
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&storagev1alpha1.Volume{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&snapshotv1.VolumeSnapshot{}).
		Watches(&source.Kind{Type: &storagev1alpha1.VolumePool{}}, handler.EnqueueRequestsFromMapFunc(r.findObjectsForVolumePool), builder.WithPredicates(predicate.Funcs{
			UpdateFunc: func(event event.UpdateEvent) bool {
				oldPool := event.ObjectOld.(*storagev1alpha1.VolumePool)
				newPool := event.ObjectNew.(*storagev1alpha1.VolumePool)
				if oldPool.Status.State == newPool.Status.State {
					return false
				}
				return newPool.Status.State == storagev1alpha1.VolumePoolStateAvailable
			},
		})).
		Complete(r)
}

func (r *VolumeReconciler) updatePoolUsageMetrics(ctx context.Context, pool *storagev1alpha1.VolumePool) error {
	volumeClassList := &storagev1alpha1.VolumeClassList{}
	if err := r.List(ctx, volumeClassList); err != nil {
		return fmt.Errorf("error listing volume classes: %w", err)
	}

	volumeList := &storagev1alpha1.VolumeList{}
	if err := r.List(ctx, volumeList, &client.ListOptions{
		FieldSelector: fields.OneTermEqualSelector(volumePoolRefIndex, pool.GetName()),
	}); err != nil {
		return fmt.Errorf("error listing volume classes: %w", err)
	}

	limits, err := ceph.CalculateUsage(volumeList, volumeClassList, r.RookConfig.BurstFactor, r.RookConfig.BurstDurationInSeconds)
	if err != nil {
		return err
	}

	for k, v := range limits {
		switch k {
		case ceph.IOPSlLimit, ceph.BPSLimit:
			r.PoolUsage.WithLabelValues(pool.Name, string(k)).Set(v.AsApproximateFloat64())
		}
	}

	return nil
}
