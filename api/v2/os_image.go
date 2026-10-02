// SPDX-FileCopyrightText: 2023 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package api

import (
	apiutils "github.com/ironcore-dev/provider-utils/apiutils/api"
)

//const (
//	VolumeStatusImageRefField           = "status.imageRef"
//	VolumeSpecSourceSnapshotSourceField = "spec.source.snapshotSource"
//)
//
//func SetupVolumeStatusImageRefFieldIndexer(volume *Volume) string {
//	return volume.Status.ImageRef
//}
//
//func SetupVolumeSpecSourceSnapshotSourceFieldIndexer(volume *Volume) string {
//	if volume.Spec.Source.Snapshot != nil {
//		return *volume.Spec.Source.Snapshot
//	}
//	return ""
//}

type OSImage struct {
	apiutils.Metadata `json:"metadata"`

	Spec   OSImageSpec   `json:"spec"`
	Status OSImageStatus `json:"status"`
}

type OSImageState string

const (
	OSImageStatePending   OSImageState = "Pending"
	OSImageStateAvailable OSImageState = "Available"
)

type OSImageSpec struct {
	Source OSImageSource `json:"source"`
}

type OSImageSource struct {
	Reference string `json:"reference"`
}

type OSImageStatus struct {
	State       OSImageState `json:"state"`
	Size        uint64       `json:"size"`
	SnapshotRef *string      `json:"snapshotRef"`
}
