// SPDX-FileCopyrightText: 2023 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package api

import (
	apiutils "github.com/ironcore-dev/provider-utils/apiutils/api"
)

type Snapshot struct {
	apiutils.Metadata `json:"metadata,omitempty"`

	Spec SnapshotSpec `json:"spec"`

	Status SnapshotStatus `json:"status"`
}

type SnapshotSpec struct {
	Source SnapshotSource `json:"source"`
}

type SnapshotState string

const (
	SnapshotStatePending SnapshotState = "Pending"
	SnapshotStateReady   SnapshotState = "Ready"
	SnapshotStateFailed  SnapshotState = "Failed"
)

type SnapshotStatus struct {
	State SnapshotState `json:"state"`
	Size  int64         `json:"size"`
}

type SnapshotSource struct {
	VolumeImageID string `json:"volumeImageID"`
}
