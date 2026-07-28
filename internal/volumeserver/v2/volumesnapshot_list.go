// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package volumeserver

import (
	"context"
	"errors"
	"fmt"

	api "github.com/ironcore-dev/ceph-provider/api/v2"
	"github.com/ironcore-dev/ceph-provider/internal/utils"
	iri "github.com/ironcore-dev/ironcore/iri/apis/volume/v1alpha1"
	"k8s.io/apimachinery/pkg/labels"
)

func (s *Server) getIriVolumeSnapshot(ctx context.Context, snapshotId string) (*iri.VolumeSnapshot, error) {
	snapshot, err := s.snapshotStore.Get(ctx, snapshotId)
	if err != nil {
		return nil, fmt.Errorf("failed to get snapshot %s: %w", snapshotId, err)
	}

	if !api.IsObjectManagedBy(snapshot, api.VolumeManager) {
		return nil, fmt.Errorf("failed to get snapshot %s: %w", snapshotId, utils.ErrSnapshotIsntManaged)
	}

	return s.convertSnapshotToIRI(snapshot)
}

func (s *Server) filterSnapshots(snapshots []*iri.VolumeSnapshot, filter *iri.VolumeSnapshotFilter) []*iri.VolumeSnapshot {
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

func (s *Server) listSnapshots(ctx context.Context) ([]*iri.VolumeSnapshot, error) {
	snapshots, err := s.snapshotStore.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("error listing snapshots: %w", err)
	}

	var res []*iri.VolumeSnapshot
	for _, snapshot := range snapshots {
		if !api.IsObjectManagedBy(snapshot, api.VolumeManager) {
			continue
		}

		iriSnapshot, err := s.convertSnapshotToIRI(snapshot)
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
		volumeSnapshot, err := s.getIriVolumeSnapshot(ctx, filter.Id)
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

	snapshots, err := s.listSnapshots(ctx)
	if err != nil {
		return nil, utils.ConvertInternalErrorToGRPC(err)
	}

	snapshots = s.filterSnapshots(snapshots, req.Filter)

	log.V(2).Info("Returning volume snapshot list")
	return &iri.ListVolumeSnapshotsResponse{
		VolumeSnapshots: snapshots,
	}, nil
}
