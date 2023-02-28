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

	volumev1alpha1 "github.com/onmetal/cephlet/ori/volume/api/v1alpha1"
	"github.com/onmetal/cephlet/ori/volume/apiutils"
	"github.com/onmetal/onmetal-api/api/storage/v1alpha1"
	"github.com/onmetal/onmetal-api/broker/common"
	ori "github.com/onmetal/onmetal-api/ori/apis/volume/v1alpha1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (s *Server) aggregateCephVolume(
	pvc *corev1.PersistentVolumeClaim,
	getVolumeClass func(string) (*v1alpha1.VolumeClass, error),
) (*AggregateCephVolume, error) {
	volumeClass, err := getVolumeClass(apiutils.GetVolumeClass(pvc))
	if err != nil {
		return nil, fmt.Errorf("error getting onmetal volume access secret: %w", err)
	}

	return &AggregateCephVolume{
		Pvc:         pvc,
		VolumeClass: volumeClass,
	}, nil
}

func (s *Server) getAggregateCephVolume(ctx context.Context, id string) (*AggregateCephVolume, error) {
	pvc := &corev1.PersistentVolumeClaim{}
	if err := s.getManagedAndCreated(ctx, id, pvc); err != nil {
		if !apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("error getting onmetal volume %s: %w", id, err)
		}
		return nil, status.Errorf(codes.NotFound, "volume %s not found", id)
	}

	return s.aggregateCephVolume(pvc, s.clientGetVolumeClassFunc(ctx))
}

func (s *Server) getVolume(ctx context.Context, id string) (*ori.Volume, error) {
	onmetalVolume, err := s.getAggregateCephVolume(ctx, id)
	if err != nil {
		return nil, err
	}

	return s.convertAggregateCephVolume(ctx, onmetalVolume)
}

func (s *Server) listVolumes(ctx context.Context) ([]*ori.Volume, error) {
	onmetalVolumes, err := s.listAggregateOnmetalVolumes(ctx)
	if err != nil {
		return nil, fmt.Errorf("error listing volumes: %w", err)
	}

	var res []*ori.Volume
	for _, onmetalVolume := range onmetalVolumes {
		volume, err := s.convertAggregateCephVolume(ctx, &onmetalVolume)
		if err != nil {
			return nil, err
		}

		res = append(res, volume)
	}
	return res, nil
}

func (s *Server) listManagedAndCreated(ctx context.Context, list client.ObjectList) error {
	return s.client.List(ctx, list,
		client.InNamespace(s.namespace),
		client.MatchingLabels{
			volumev1alpha1.ManagerLabel: volumev1alpha1.VolumeCephletManager,
			volumev1alpha1.CreatedLabel: "true",
		},
	)
}

func (s *Server) listAggregateOnmetalVolumes(ctx context.Context) ([]AggregateCephVolume, error) {
	pvcVolumeList := &corev1.PersistentVolumeClaimList{}
	if err := s.listManagedAndCreated(ctx, pvcVolumeList); err != nil {
		return nil, fmt.Errorf("error listing onmetal volumes: %w", err)
	}

	volumeClassList := &v1alpha1.VolumeClassList{}
	if err := s.client.List(ctx, volumeClassList); err != nil {
		return nil, fmt.Errorf("error listing secrets: %w", err)
	}

	volumeClassByNameGetter, err := common.NewObjectGetter[string, *v1alpha1.VolumeClass](
		corev1.Resource("volumeclasses"),
		common.ByObjectName[*v1alpha1.VolumeClass](),
		common.ObjectSlice[string](volumeClassList.Items),
	)
	if err != nil {
		return nil, fmt.Errorf("error constructing secret getter: %w", err)
	}

	var res []AggregateCephVolume
	for i := range pvcVolumeList.Items {
		onmetalVolume := &pvcVolumeList.Items[i]
		aggregateCephVolume, err := s.aggregateCephVolume(onmetalVolume, volumeClassByNameGetter.Get)
		if err != nil {
			return nil, fmt.Errorf("error aggregating onmetal volume %s: %w", onmetalVolume.Name, err)
		}

		res = append(res, *aggregateCephVolume)
	}

	return res, nil
}

func (s *Server) filterVolumes(volumes []*ori.Volume, filter *ori.VolumeFilter) []*ori.Volume {
	if filter == nil {
		return volumes
	}

	var (
		res []*ori.Volume
		sel = labels.SelectorFromSet(filter.LabelSelector)
	)
	for _, oriVolume := range volumes {
		if !sel.Matches(labels.Set(oriVolume.Metadata.Labels)) {
			continue
		}

		res = append(res, oriVolume)
	}
	return res
}

func (s *Server) ListVolumes(ctx context.Context, req *ori.ListVolumesRequest) (*ori.ListVolumesResponse, error) {
	if filter := req.Filter; filter != nil && filter.Id != "" {
		volume, err := s.getVolume(ctx, filter.Id)
		if err != nil {
			if status.Code(err) != codes.NotFound {
				return nil, err
			}
			return &ori.ListVolumesResponse{
				Volumes: []*ori.Volume{},
			}, nil
		}

		return &ori.ListVolumesResponse{
			Volumes: []*ori.Volume{volume},
		}, nil
	}

	volumes, err := s.listVolumes(ctx)
	if err != nil {
		return nil, err
	}

	volumes = s.filterVolumes(volumes, req.Filter)

	return &ori.ListVolumesResponse{
		Volumes: volumes,
	}, nil
}
