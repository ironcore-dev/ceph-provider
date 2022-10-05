package ceph

import (
	"fmt"
	"github.com/onmetal/onmetal-api/apis/storage"
	"k8s.io/apimachinery/pkg/api/resource"
)

const (
	LabelLimitsPerGB = "cephlet.onmetal.de/limitsPerGB"
)

func DefaultLimits() Limits {
	return map[LimitType]*resource.Quantity{}
}

type Limits map[LimitType]*resource.Quantity

func CalculateLimits(volume *storage.Volume, volumeClass *storage.VolumeClass) (Limits, error) {
	limits := DefaultLimits()

	var scale int64 = 1
	// if not true: absolute limits
	if _, limitsPerGB := volumeClass.Labels[LabelLimitsPerGB]; limitsPerGB {
		size := volume.Spec.Resources.Storage()
		if size == nil {
			return nil, fmt.Errorf("")
		}

		scale = size.ScaledValue(resource.Giga)
	}

	if iops, ok := volumeClass.Capabilities[storage.ResourceIOPS]; ok {
		value := iops.DeepCopy()
		value.Set(scale * iops.Value())

		limits[IOPSlLimit] = &value
		limits[ReadIOPSLimit] = &value
		limits[WriteIOPSLimit] = &value
	}

	if tps, ok := volumeClass.Capabilities[storage.ResourceTPS]; ok {
		value := tps.DeepCopy()
		value.Set(scale * tps.Value())

		limits[BPSLimit] = &value
		limits[ReadBPSLimit] = &value
		limits[WriteBPSLimit] = &value
	}

	return limits, nil
}
