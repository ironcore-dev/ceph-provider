// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package volumeserver

import (
	"fmt"

	api "github.com/ironcore-dev/ceph-provider/api/v2"
	iri "github.com/ironcore-dev/ironcore/iri/apis/volume/v1alpha1"
)

func (s *Server) convertSnapshotToIRI(snapshot *api.Snapshot) (*iri.VolumeSnapshot, error) {
	metadata, err := api.GetObjectMetadataFromObjectID(snapshot.Metadata)
	if err != nil {
		return nil, fmt.Errorf("error getting iri metadata: %w", err)
	}

	state, err := s.getIriSnapshotState(snapshot.Status.State)
	if err != nil {
		return nil, fmt.Errorf("error getting iri state: %w", err)
	}

	return &iri.VolumeSnapshot{
		Metadata: metadata,
		Spec: &iri.VolumeSnapshotSpec{
			VolumeId: snapshot.Spec.ImageRef,
		},
		Status: &iri.VolumeSnapshotStatus{
			State: state,
			Size:  snapshot.Status.Size,
		},
	}, nil
}

func (s *Server) getIriSnapshotState(state api.SnapshotState) (iri.VolumeSnapshotState, error) {
	switch state {
	case api.SnapshotStateReady:
		return iri.VolumeSnapshotState_VOLUME_SNAPSHOT_READY, nil
	case api.SnapshotStatePending:
		return iri.VolumeSnapshotState_VOLUME_SNAPSHOT_PENDING, nil
	case api.SnapshotStateFailed:
		return iri.VolumeSnapshotState_VOLUME_SNAPSHOT_FAILED, nil
	default:
		return 0, fmt.Errorf("unknown volume snapshot state '%q'", state)
	}
}
