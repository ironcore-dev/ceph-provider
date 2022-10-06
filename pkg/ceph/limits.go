// Copyright 2022 OnMetal authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package ceph

import (
	"fmt"

	storagev1alpha1 "github.com/onmetal/onmetal-api/apis/storage/v1alpha1"
	"k8s.io/apimachinery/pkg/api/resource"
)

const (
	LabelLimitsPerGB = "cephlet.onmetal.de/limitsPerGB"
)

func DefaultLimits() Limits {
	return map[LimitType]*resource.Quantity{}
}

type Limits map[LimitType]*resource.Quantity

func CalculateLimits(volume *storagev1alpha1.Volume, volumeClass *storagev1alpha1.VolumeClass) (Limits, error) {
	limits := DefaultLimits()

	var scale int64 = 1
	// if not true: absolute limits
	if _, limitsPerGB := volumeClass.Labels[LabelLimitsPerGB]; limitsPerGB {
		size := volume.Spec.Resources.Storage()
		if size == nil {
			return nil, fmt.Errorf("unable to calculate scale factor because storage size is not set")
		}

		scale = size.ScaledValue(resource.Giga)
	}

	if iops, ok := volumeClass.Capabilities[storagev1alpha1.ResourceIOPS]; ok {
		value := iops.DeepCopy()
		value.Set(scale * iops.Value())

		limits[IOPSlLimit] = &value
		limits[ReadIOPSLimit] = &value
		limits[WriteIOPSLimit] = &value
	}

	if tps, ok := volumeClass.Capabilities[storagev1alpha1.ResourceTPS]; ok {
		value := tps.DeepCopy()
		value.Set(scale * tps.Value())

		limits[BPSLimit] = &value
		limits[ReadBPSLimit] = &value
		limits[WriteBPSLimit] = &value
	}

	return limits, nil
}
