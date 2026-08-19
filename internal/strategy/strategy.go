// SPDX-FileCopyrightText: 2023 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package strategy

import (
	"crypto/rand"

	"github.com/ironcore-dev/ceph-provider/api"
	apiv2 "github.com/ironcore-dev/ceph-provider/api/v2"
	"github.com/ironcore-dev/ironcore/broker/common/idgen"
)

var SnapshotStrategy = snapshotStrategy{}

type snapshotStrategy struct{}

func (snapshotStrategy) PrepareForCreate(obj *api.Snapshot) {
	obj.Status = api.SnapshotStatus{State: api.SnapshotStatePending}
}

var ImageStrategy = imageStrategy{
	WWNGen: idgen.NewIDGen(rand.Reader, 16),
}

type imageStrategy struct {
	WWNGen idgen.IDGen
}

func (i imageStrategy) PrepareForCreate(obj *api.Image) {
	obj.Spec.WWN = i.WWNGen.Generate()
	obj.Status = api.ImageStatus{State: api.ImageStatePending}
}

var VolumeStrategy = volumeStrategy{}

type volumeStrategy struct{}

func (volumeStrategy) PrepareForCreate(obj *apiv2.Volume) {
	obj.Status = apiv2.VolumeStatus{State: apiv2.VolumeStatePending}
}

var ImageV2Strategy = imageV2Strategy{
	WWNGen: idgen.NewIDGen(rand.Reader, 16),
}

type imageV2Strategy struct {
	WWNGen idgen.IDGen
}

func (i imageV2Strategy) PrepareForCreate(obj *apiv2.Image) {
	obj.Spec.WWN = i.WWNGen.Generate()
	obj.Status = apiv2.ImageStatus{State: apiv2.ImageStatePending}
}

var SnapshotV2Strategy = snapshotV2Strategy{}

type snapshotV2Strategy struct{}

func (snapshotV2Strategy) PrepareForCreate(obj *apiv2.Snapshot) {
	obj.Status = apiv2.SnapshotStatus{State: apiv2.SnapshotStatePending}
}
