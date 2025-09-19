// SPDX-FileCopyrightText: 2023 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package api

import (
	apiutils "github.com/ironcore-dev/provider-utils/apiutils/api"
)

type OSSnapshot struct {
	apiutils.Metadata `json:"metadata,omitempty"`

	Source OSSnapshotSource `json:"source"`

	Status OSSnapshotStatus `json:"status"`
}

type OSSnapshotState string

const (
	OSSnapshotStatePending   OSSnapshotState = "Pending"
	OSSnapshotStatePopulated OSSnapshotState = "Populated"
)

type OSSnapshotStatus struct {
	State  OSSnapshotState `json:"state"`
	Digest string          `json:"digest"`
}

type OSSnapshotSource struct {
	IronCoreOSImage string `json:"ironcoreOSImage"`
}
