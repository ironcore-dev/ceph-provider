// SPDX-FileCopyrightText: 2023 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package api

import (
	apiutils "github.com/ironcore-dev/provider-utils/apiutils/api"
)

type Snapshot struct {
	apiutils.Metadata `json:"metadata,omitempty"`

	Spec   SnapshotSpec   `json:"spec"`
	Status SnapshotStatus `json:"status"`
}

type SnapshotProtection string

const (
	SnapshotProtectionNone      SnapshotProtection = "none"
	SnapshotProtectionProtected SnapshotProtection = "protected"
)

type SnapshotSpec struct {
	ImageRef   string             `json:"imageRef"`
	Protection SnapshotProtection `json:"protection"`
}

type SnapshotState string

const (
	SnapshotStatePending SnapshotState = "Pending"
	SnapshotStateReady   SnapshotState = "Ready"
	SnapshotStateFailed  SnapshotState = "Failed"
)

type SnapshotStatus struct {
	State  SnapshotState `json:"state"`
	Digest string        `json:"digest"`
	Size   int64         `json:"size"`
}

type SnapshotSource struct {
	IronCoreImage string `json:"ironcoreImage"`
}
