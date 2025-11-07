// SPDX-FileCopyrightText: 2022 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package volumeserver

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/ironcore-dev/ceph-provider/api"
	"github.com/ironcore-dev/ceph-provider/internal/utils"
	iri "github.com/ironcore-dev/ironcore/iri/apis/volume/v1alpha1"
	"k8s.io/apimachinery/pkg/labels"
)

func (s *Server) getIriVolumeSnapshot(ctx context.Context, log logr.Logger, snapshotId string) (*iri.VolumeSnapshot, error) {
	log.V(2).Info("Get volume snapshot %s", snapshotId)
	cephSnapshot, err := s.snapshotStore.Get(ctx, snapshotId)
	if err != nil {
		return nil, fmt.Errorf("failed to get snapshot %s: %w", snapshotId, err)
	}

	if !api.IsObjectManagedBy(cephSnapshot, api.VolumeManager) {
		return nil, fmt.Errorf("failed to get snapshot %s: %w", snapshotId, utils.ErrSnapshotIsntManaged)
	}

	return s.convertSnapshotToIriVolumeSnapshot(cephSnapshot)
}

func (s *Server) filterSnapshot(snapshots []*iri.VolumeSnapshot, filter *iri.VolumeSnapshotFilter) []*iri.VolumeSnapshot {
	if filter == nil {
		return snapshots
	}

	var (
		res []*iri.VolumeSnapshot
		sel = labels.SelectorFromSet(filter.LabelSelector)
	)
	for _, iriSnapshot := range snapshots {
		if !sel.Matches(labels.Set(iriSnapshot.Metadata.Labels)) {
			continue
		}

		res = append(res, iriSnapshot)
	}
	return res
}

func (s *Server) listSnapshots(ctx context.Context, log logr.Logger) ([]*iri.VolumeSnapshot, error) {
	cephSnapshots, err := s.snapshotStore.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("error listing snapshots: %w", err)
	}

	var res []*iri.VolumeSnapshot
	for _, cephSnapshot := range cephSnapshots {
		if !api.IsObjectManagedBy(cephSnapshot, api.VolumeManager) {
			continue
		}

		iriSnapshot, err := s.convertSnapshotToIriVolumeSnapshot(cephSnapshot)
		if err != nil {
			return nil, err
		}

		res = append(res, iriSnapshot)
	}
	return res, nil
}

func (s *Server) ListVolumeSnapshots(ctx context.Context, req *iri.ListVolumeSnapshotsRequest) (*iri.ListVolumeSnapshotsResponse, error) {
	log := s.loggerFrom(ctx)
	log.V(2).Info("Listing volume snapshots")

	if filter := req.Filter; filter != nil && filter.Id != "" {
		volumeSnapshot, err := s.getIriVolumeSnapshot(ctx, log, filter.Id)
		if err != nil {
			if !errors.Is(err, utils.ErrSnapshotNotFound) && !errors.Is(err, utils.ErrSnapshotIsntManaged) {
				return nil, utils.ConvertInternalErrorToGRPC(err)
			}
			return &iri.ListVolumeSnapshotsResponse{
				VolumeSnapshots: []*iri.VolumeSnapshot{},
			}, nil
		}

		return &iri.ListVolumeSnapshotsResponse{
			VolumeSnapshots: []*iri.VolumeSnapshot{volumeSnapshot},
		}, nil
	}

	snapshots, err := s.listSnapshots(ctx, log)
	if err != nil {
		return nil, utils.ConvertInternalErrorToGRPC(err)
	}

	snapshots = s.filterSnapshot(snapshots, req.Filter)

	log.V(2).Info("Returning volume snapshot list")
	return &iri.ListVolumeSnapshotsResponse{
		VolumeSnapshots: snapshots,
	}, nil
}
