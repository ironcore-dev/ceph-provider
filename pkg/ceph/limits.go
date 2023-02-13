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

	corev1alpha1 "github.com/onmetal/onmetal-api/api/core/v1alpha1"
	storagev1alpha1 "github.com/onmetal/onmetal-api/api/storage/v1alpha1"
	"k8s.io/apimachinery/pkg/api/resource"
)

const (
	LabelLimitsPerGB = "cephlet.onmetal.de/limitsPerGB"
)

func DefaultLimits() Limits {
	return map[LimitType]resource.Quantity{}
}

type Limits map[LimitType]resource.Quantity

func CalculateLimits(volume *storagev1alpha1.Volume, volumeClass *storagev1alpha1.VolumeClass, burstFactor, burstDurationInSeconds int64) (Limits, error) {
	limits := DefaultLimits()
	burstDuration := resource.NewQuantity(burstDurationInSeconds, resource.DecimalSI)

	var scale int64 = 1
	// if not true: absolute limits
	if _, limitsPerGB := volumeClass.Labels[LabelLimitsPerGB]; limitsPerGB {
		size := volume.Spec.Resources.Storage()
		if size == nil {
			return nil, fmt.Errorf("unable to calculate scale factor because storage size is not set")
		}

		scale = size.ScaledValue(resource.Giga)
	}

	if iops, ok := volumeClass.Capabilities[corev1alpha1.ResourceIOPS]; ok {
		limit := iops.DeepCopy()
		limit.Set(scale * iops.Value())

		limits[IOPSlLimit] = limit
		limits[ReadIOPSLimit] = limit
		limits[WriteIOPSLimit] = limit

		burstLimit := limit.DeepCopy()
		burstLimit.Set(burstFactor * limit.Value())
		limits[IOPSBurstLimit] = burstLimit
		limits[ReadIOPSBurstLimit] = burstLimit
		limits[WriteIOPSBurstLimit] = burstLimit

		limits[IOPSBurstDurationLimit] = *burstDuration
	}

	if tps, ok := volumeClass.Capabilities[corev1alpha1.ResourceTPS]; ok {
		limit := tps.DeepCopy()
		limit.Set(scale * tps.Value())

		limits[BPSLimit] = limit
		limits[ReadBPSLimit] = limit
		limits[WriteBPSLimit] = limit

		burstLimit := limit.DeepCopy()
		burstLimit.Set(burstFactor * limit.Value())
		limits[BPSBurstLimit] = burstLimit
		limits[ReadBPSBurstLimit] = burstLimit
		limits[WriteBPSBurstLimit] = burstLimit

		limits[BPSBurstDurationLimit] = *burstDuration
	}

	return limits, nil
}

func CalculateUsage(volumes *storagev1alpha1.VolumeList, volumeClasses *storagev1alpha1.VolumeClassList, burstFactor, burstDurationInSeconds int64) (Limits, error) {
	classes := map[string]*storagev1alpha1.VolumeClass{}

	for i := range volumeClasses.Items {
		volumeClass := volumeClasses.Items[i]
		classes[volumeClass.Name] = &volumeClass
	}

	usage := DefaultLimits()
	for _, volume := range volumes.Items {
		class := classes[volume.Spec.VolumeClassRef.Name]
		limits, err := CalculateLimits(&volume, class, burstFactor, burstDurationInSeconds)
		if err != nil {
			continue
		}

		for k, v := range limits {
			value := usage[k]
			value.Add(v)
			usage[k] = value
		}
	}

	return usage, nil
}
