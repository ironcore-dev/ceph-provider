// SPDX-FileCopyrightText: 2023 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package strategy

import (
	"crypto/rand"

	"github.com/ironcore-dev/ceph-provider/api"
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
