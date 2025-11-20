// SPDX-FileCopyrightText: 2023 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"fmt"

	librbd "github.com/ceph/go-ceph/rbd"
	"github.com/go-logr/logr"
	providerapi "github.com/ironcore-dev/ceph-provider/api"
)

const (
	ImageRBDIDPrefix    = "img_"
	SnapshotRBDIDPrefix = "snap_"

	ImageSnapshotVersion = "v1"
)

func ImageIDToRBDID(imageID string) string {
	return ImageRBDIDPrefix + imageID
}

func SnapshotIDToRBDID(snapshotID string) string {
	return SnapshotRBDIDPrefix + snapshotID
}

func getSnapshotSourceDetails(snapshot *providerapi.Snapshot) (parentName string, snapName string, err error) {
	switch {
	case snapshot.Source.IronCoreImage != "":
		parentName = SnapshotIDToRBDID(snapshot.ID)
		snapName = ImageSnapshotVersion
	case snapshot.Source.VolumeImageID != "":
		parentName = ImageIDToRBDID(snapshot.Source.VolumeImageID)
		snapName = snapshot.ID
	default:
		return "", "", fmt.Errorf("snapshot source is not present")
	}
	return parentName, snapName, nil
}

func closeImage(log logr.Logger, img *librbd.Image) {
	if closeErr := img.Close(); closeErr != nil && closeErr != librbd.ErrImageNotOpen {
		log.Error(closeErr, "failed to close image")
	}
}
