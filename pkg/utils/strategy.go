// Copyright 2023 OnMetal authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package utils

import (
	"crypto/rand"

	"github.com/ironcore-dev/ceph-provider/pkg/api"
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
