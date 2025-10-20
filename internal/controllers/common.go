// SPDX-FileCopyrightText: 2023 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"errors"
	"fmt"

	"github.com/ceph/go-ceph/rados"
	librbd "github.com/ceph/go-ceph/rbd"
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

func getSnapshotSourceDetails(snapshot *providerapi.Snapshot) (string, string, error) {
	var parentName, snapName string
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

func listRbdImageChildren(ioCtx *rados.IOContext, imageID string) (int, int, error) {
	img, err := librbd.OpenImage(ioCtx, imageID, librbd.NoSnapshot)
	if err != nil {
		if !errors.Is(err, librbd.ErrNotFound) {
			return 0, 0, fmt.Errorf("failed to open image: %w", err)
		}
		return -1, -1, nil
	}

	pools, imgs, err := img.ListChildren()
	if err != nil {
		return 0, 0, fmt.Errorf("unable to list image children: %w", err)
	}

	if err := img.Close(); err != nil {
		return 0, 0, fmt.Errorf("unable to close image: %w", err)
	}

	return len(pools), len(imgs), nil
}
