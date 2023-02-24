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

package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-logr/logr"
	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v6/apis/volumesnapshot/v1"
	volumev1alpha1 "github.com/onmetal/cephlet/ori/volume/api/v1alpha1"
	"github.com/onmetal/cephlet/ori/volume/apiutils"
	"github.com/onmetal/controller-utils/metautils"
	storagev1alpha1 "github.com/onmetal/onmetal-api/api/storage/v1alpha1"
	ori "github.com/onmetal/onmetal-api/ori/apis/volume/v1alpha1"
	onmetalimage "github.com/onmetal/onmetal-image"
	"github.com/onmetal/onmetal-image/oci/image"
	"github.com/onmetal/onmetal-image/oci/remote"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func GetSanitizedImageName(image string) string {
	image = strings.ReplaceAll(image, "/", "-")
	image = strings.ReplaceAll(image, ":", "-")
	return strings.ReplaceAll(image, "@", "-")
}

func getImageSize(ctx context.Context, imageName string) (*resource.Quantity, error) {
	reg, err := remote.DockerRegistry(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize registry: %w", err)
	}

	img, err := reg.Resolve(ctx, imageName)
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

func (s *Server) getVolumeConfig(ctx context.Context, volume *ori.Volume) (*AggregateCephVolume, error) {
	volumeClass := &storagev1alpha1.VolumeClass{}
	if err := s.client.Get(ctx, types.NamespacedName{Name: volume.Spec.Class}, volumeClass); err != nil {
		return nil, fmt.Errorf("unable to get VolumeClass: %w", err)
	}

	pvc := &corev1.PersistentVolumeClaim{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PersistentVolumeClaim",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      s.idGen.Generate(),
			Namespace: s.namespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: *resource.NewQuantity(int64(volume.Spec.Resources.StorageBytes), resource.DecimalSI)},
			},
			VolumeMode:       func(m corev1.PersistentVolumeMode) *corev1.PersistentVolumeMode { return &m }(corev1.PersistentVolumeBlock),
			StorageClassName: &s.rookPoolStorageClass,
		},
	}

	var imagePvc *corev1.PersistentVolumeClaim
	if volume.Spec.Image != "" {
		imageName := GetSanitizedImageName(volume.Spec.Image)
		apiutils.SetImageAnnotation(pvc, volume.Spec.Image)

		pvc.Spec.DataSourceRef = &corev1.TypedObjectReference{
			APIGroup: pointer.String("snapshot.storage.k8s.io"),
			Kind:     "VolumeSnapshot",
			Name:     imageName,
		}

		size, err := getImageSize(ctx, volume.Spec.Image)
		if err != nil {
			return nil, fmt.Errorf("unable to get image size: %w", err)
		}

		imagePvc = &corev1.PersistentVolumeClaim{
			TypeMeta: metav1.TypeMeta{
				Kind:       "PersistentVolumeClaim",
				APIVersion: "v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      imageName,
				Namespace: s.namespace,
			},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceStorage: *size},
				},
				VolumeMode:       func(m corev1.PersistentVolumeMode) *corev1.PersistentVolumeMode { return &m }(corev1.PersistentVolumeBlock),
				StorageClassName: pointer.String(s.rookPoolStorageClass),
				//set DataSourceRef that populator picks up the pvc
				DataSourceRef: &corev1.TypedObjectReference{
					APIGroup: pointer.String(storagev1alpha1.SchemeGroupVersion.String()),
					Kind:     "Volume",
					Name:     pvc.Name,
				},
			},
		}
		metautils.SetLabel(imagePvc, volumev1alpha1.ManagerLabel, volumev1alpha1.VolumeCephletManager)

	}

	if err := apiutils.SetObjectMetadata(pvc, volume.Metadata); err != nil {
		return nil, err
	}
	metautils.SetLabel(pvc, volumev1alpha1.ManagerLabel, volumev1alpha1.VolumeCephletManager)
	apiutils.SetVolumeClassLabel(pvc, volume.Spec.Class)

	return &AggregateCephVolume{
		Pvc:         pvc,
		ImagePvc:    imagePvc,
		VolumeClass: volumeClass,
	}, nil
}

func (s *Server) handleImagePopulation(ctx context.Context, log logr.Logger, volume *AggregateCephVolume) error {
	if volume.ImagePvc == nil {
		return nil
	}

	snapshot := &snapshotv1.VolumeSnapshot{}
	if err := s.client.Get(ctx, client.ObjectKeyFromObject(volume.ImagePvc), snapshot); err != nil {
		if !errors.IsNotFound(err) {
			return fmt.Errorf("unable to get snapshot: %w", err)
		}
		log.V(1).Info("Requested snapshot not found: create image pvc and snapshot it.")
		return s.createSnapshot(ctx, log, volume)
	}

	if !pointer.BoolDeref(snapshot.Status.ReadyToUse, false) || snapshot.Status.RestoreSize == nil {
		log.Info("Referenced snapshot not ready or RestoreSize not defined", "snapshotName", snapshot.Name)
		return nil
	}

	volumeSizeBytes := volume.Pvc.Spec.Resources.Requests.Storage().Value()
	if volumeSizeBytes < snapshot.Status.RestoreSize.Value() {
		log.Info(fmt.Sprintf("Requested volume size %d is less than the size %d for the source snapshot", volumeSizeBytes, snapshot.Status.RestoreSize.Value()), "snapshotName", snapshot.Name)
		//r.Eventf(volume, corev1.EventTypeWarning, ReasonVolumeSizeToSmall, "Requested volume size %d is less than the size %d for the source snapshot %s", volumeSizeBytes, snapshot.Status.RestoreSize.Value(), snapshot.Name)
	}

	return nil
}

func (s *Server) createSnapshot(ctx context.Context, log logr.Logger, volume *AggregateCephVolume) error {
	if err := s.client.Create(ctx, volume.ImagePvc); err != nil {
		return fmt.Errorf("error creating pvc: %w", err)
	}
	log.V(1).Info(fmt.Sprintf("created image pvc %s", client.ObjectKeyFromObject(volume.ImagePvc)))

	snapshot := &snapshotv1.VolumeSnapshot{
		TypeMeta: metav1.TypeMeta{
			Kind:       "VolumeSnapshot",
			APIVersion: snapshotv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      volume.ImagePvc.Name,
			Namespace: volume.ImagePvc.Namespace,
		},
		Spec: snapshotv1.VolumeSnapshotSpec{
			Source: snapshotv1.VolumeSnapshotSource{
				PersistentVolumeClaimName: &volume.ImagePvc.Name,
			},
			VolumeSnapshotClassName: pointer.String(s.rookPoolStorageClass),
		},
	}
	metautils.SetLabel(snapshot, volumev1alpha1.ManagerLabel, volumev1alpha1.VolumeCephletManager)

	if err := s.client.Create(ctx, snapshot); err != nil {
		return fmt.Errorf("error creating snapshot: %w", err)
	}
	log.V(1).Info(fmt.Sprintf("created snapshot %s for image pvc %s", client.ObjectKeyFromObject(snapshot), client.ObjectKeyFromObject(volume.ImagePvc)))

	return nil
}

func (s *Server) createVolume(ctx context.Context, log logr.Logger, volume *AggregateCephVolume) (retErr error) {

	if err := s.handleImagePopulation(ctx, log, volume); err != nil {
		return fmt.Errorf("error populate image: %w", err)
	}

	c, cleanup := s.setupCleaner(ctx, log, &retErr)
	defer cleanup()

	log.V(1).Info("Creating pvc")
	if err := s.client.Create(ctx, volume.Pvc); err != nil {
		return fmt.Errorf("error creating pvc: %w", err)
	}
	c.Add(func(ctx context.Context) error {
		if err := s.client.Delete(ctx, volume.Pvc); client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("error deleting pvc: %w", err)
		}
		return nil
	})

	log.V(1).Info("Patching pvc as created")
	if err := apiutils.PatchCreated(ctx, s.client, volume.Pvc); err != nil {
		return fmt.Errorf("error patching pvc as created: %w", err)
	}

	// Reset cleaner since everything from now on operates on a consistent volume
	c.Reset()

	return nil
}

func (s *Server) CreateVolume(ctx context.Context, req *ori.CreateVolumeRequest) (res *ori.CreateVolumeResponse, retErr error) {
	log := s.loggerFrom(ctx)

	log.V(1).Info("Getting volume configuration")
	cfg, err := s.getVolumeConfig(ctx, req.Volume)
	if err != nil {
		return nil, fmt.Errorf("error getting volume config: %w", err)
	}

	if err := s.createVolume(ctx, log, cfg); err != nil {
		return nil, fmt.Errorf("error creating pvc: %w", err)
	}

	v, err := s.convertAggregateCephVolume(ctx, cfg)
	if err != nil {
		return nil, err
	}

	return &ori.CreateVolumeResponse{
		Volume: v,
	}, nil
}
