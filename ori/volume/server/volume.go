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
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/onmetal/cephlet/ori/volume/apiutils"
	"github.com/onmetal/cephlet/pkg/ceph"
	storagev1alpha1 "github.com/onmetal/onmetal-api/api/storage/v1alpha1"
	ori "github.com/onmetal/onmetal-api/ori/apis/volume/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	userIDKey  = "userID"
	userKeyKey = "userKey"

	pvPoolKey               = "pool"
	monitorConfigMapDataKey = "csi-cluster-config-json"
	pvImageNameKey          = "imageName"
	pvRadosNamespaceKey     = "radosNamespace"
)

type AggregateCephVolume struct {
	Pvc          *corev1.PersistentVolumeClaim
	ImagePvc     *corev1.PersistentVolumeClaim
	VolumeClass  *storagev1alpha1.VolumeClass
	AccessSecret *corev1.Secret
}

var onmetalVolumeStateToORIState = map[corev1.PersistentVolumeClaimPhase]ori.VolumeState{
	corev1.ClaimPending: ori.VolumeState_VOLUME_PENDING,
	corev1.ClaimBound:   ori.VolumeState_VOLUME_AVAILABLE,
	corev1.ClaimLost:    ori.VolumeState_VOLUME_ERROR,
}

func generateWWN(wwnPrefix string) (string, error) {
	// prefix is optional, set to 1100AA for private identifier
	wwn := wwnPrefix

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

func (s *Server) convertOnmetalVolumeState(state corev1.PersistentVolumeClaimPhase) (ori.VolumeState, error) {
	if state, ok := onmetalVolumeStateToORIState[state]; ok {
		return state, nil
	}
	return 0, fmt.Errorf("unknown onmetal volume state %q", state)
}

func (s *Server) convertOnmetalVolumeAccess(ctx context.Context, volume *AggregateCephVolume) (*ori.VolumeAccess, error) {
	if volume.Pvc.Status.Phase != corev1.ClaimBound {
		return nil, nil
	}

	pv := &corev1.PersistentVolume{}
	if err := s.client.Get(ctx, types.NamespacedName{Name: volume.Pvc.Spec.VolumeName, Namespace: volume.Pvc.Namespace}, pv); err != nil {
		return nil, client.IgnoreNotFound(fmt.Errorf("unable to get pv: %w", err))
	}
	if pv.Status.Phase != corev1.VolumeBound {
		return nil, nil
	}

	monitors, err := s.getMonitorList(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get monitor list for volume: %w", err)
	}

	imageKey, err := s.getImageKeyFromPV(pv)
	if err != nil {
		return nil, fmt.Errorf("failed to provide image name: %w", err)
	}

	wwn, err := generateWWN(s.wwnPrefix)
	if err != nil {
		return nil, fmt.Errorf("unable to generate wwn: %w", err)
	}

	credentials, err := s.getVolumeAccessCredentials(ctx)
	if err != nil {
		return nil, fmt.Errorf("unable to get volume access credentials: %w", err)
	}

	return &ori.VolumeAccess{
		Driver: s.driver,
		Handle: wwn,
		Attributes: map[string]string{
			"monitors": strings.Join(monitors, ","),
			"image":    imageKey,
		},
		SecretData: map[string][]byte{
			userIDKey:  []byte(s.rookPoolName),
			userKeyKey: credentials,
		},
	}, nil
}

func (s *Server) clientGetVolumeClassFunc(ctx context.Context) func(string) (*storagev1alpha1.VolumeClass, error) {

	return func(name string) (*storagev1alpha1.VolumeClass, error) {
		volumeClass := &storagev1alpha1.VolumeClass{}
		if err := s.client.Get(ctx, client.ObjectKey{Namespace: s.namespace, Name: name}, volumeClass); err != nil {
			return nil, err
		}
		return volumeClass, nil
	}
}

func (s *Server) getMonitorList(ctx context.Context) ([]string, error) {
	rookConfigMap := &corev1.ConfigMap{}
	if err := s.client.Get(ctx, types.NamespacedName{Name: s.rookPoolMonitorConfigmap, Namespace: s.rookNamespace}, rookConfigMap); err != nil {
		return nil, fmt.Errorf("failed to get ceph monitors configMap %s: %w", client.ObjectKeyFromObject(rookConfigMap), err)
	}
	var list ceph.ClusterList
	if val, ok := rookConfigMap.Data[monitorConfigMapDataKey]; !ok {
		return nil, fmt.Errorf("unable to find data key %s in rook configMap %s", monitorConfigMapDataKey, client.ObjectKeyFromObject(rookConfigMap))
	} else if err := json.Unmarshal([]byte(val), &list); err != nil {
		return nil, fmt.Errorf("failed to decode ceph cluster list in rook config map %s: %w", client.ObjectKeyFromObject(rookConfigMap), err)
	}
	var monitors []string
	for _, cluster := range list {
		if cluster.ClusterID == s.rookClusterName {
			monitors = cluster.Monitors
			break
		}
	}
	if len(monitors) == 0 {
		return nil, fmt.Errorf("no monitors provided for clusterID %s", s.rookPoolName)
	}
	return monitors, nil
}

func (s *Server) getImageKeyFromPV(pv *corev1.PersistentVolume) (string, error) {
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

	return result, nil
}

func (s *Server) getVolumeAccessCredentials(ctx context.Context) ([]byte, error) {
	cephClientSecret := &corev1.Secret{}
	if err := s.client.Get(ctx, types.NamespacedName{Namespace: s.rookNamespace, Name: s.rookPoolSecretName}, cephClientSecret); err != nil {
		return nil, fmt.Errorf("unable to get secret: %w", err)
	}
	if cephClientSecret.Data == nil {
		return nil, fmt.Errorf("secret %s data empty", client.ObjectKeyFromObject(cephClientSecret))
	}

	// Data key of secret is equivalent to CephClient name
	credentials, ok := cephClientSecret.Data[s.rookClientName]
	if !ok {
		return nil, fmt.Errorf("secret %s does not contain data key %s", client.ObjectKeyFromObject(cephClientSecret), s.rookPoolName)
	}

	return credentials, nil
}

func (s *Server) convertAggregateCephVolume(ctx context.Context, volume *AggregateCephVolume) (*ori.Volume, error) {
	metadata, err := apiutils.GetObjectMetadata(volume.Pvc)
	if err != nil {
		return nil, err
	}

	resources, err := s.convertVolumeResources(volume.Pvc.Spec.Resources.Requests)
	if err != nil {
		return nil, err
	}

	state, err := s.convertOnmetalVolumeState(volume.Pvc.Status.Phase)
	if err != nil {
		return nil, err
	}

	access, err := s.convertOnmetalVolumeAccess(ctx, volume)
	if err != nil {
		return nil, err
	}

	return &ori.Volume{
		Metadata: metadata,
		Spec: &ori.VolumeSpec{
			Image:     apiutils.GetImage(volume.Pvc),
			Class:     volume.VolumeClass.Name,
			Resources: resources,
		},
		Status: &ori.VolumeStatus{
			State:  state,
			Access: access,
		},
	}, nil
}

func (s *Server) convertVolumeResources(resources corev1.ResourceList) (*ori.VolumeResources, error) {
	storage := resources.Storage()
	if storage.IsZero() {
		return nil, fmt.Errorf("volume does not specify storage resource")
	}

	return &ori.VolumeResources{
		StorageBytes: storage.AsDec().UnscaledBig().Uint64(),
	}, nil
}
