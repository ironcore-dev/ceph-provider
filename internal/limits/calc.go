// SPDX-FileCopyrightText: 2022 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package limits

import (
	"github.com/ironcore-dev/ceph-provider/api"
	apiv2 "github.com/ironcore-dev/ceph-provider/api/v2"
)

func Calculate(iops, tps int64, burstFactor, burstDurationInSeconds int64) api.Limits {
	limits := map[api.LimitType]int64{}

	//TODO: scaling dependent on size
	var scale int64 = 1

	//IOPS
	iops = iops * scale
	limits[api.IOPSLimit] = iops
	limits[api.ReadIOPSLimit] = iops
	limits[api.WriteIOPSLimit] = iops

	iopsBurstLimit := burstFactor * iops
	limits[api.IOPSBurstLimit] = iopsBurstLimit
	limits[api.ReadIOPSBurstLimit] = iopsBurstLimit
	limits[api.WriteIOPSBurstLimit] = iopsBurstLimit

	limits[api.IOPSBurstDurationLimit] = burstDurationInSeconds

	//TPS
	tps = tps * scale
	limits[api.BPSLimit] = tps
	limits[api.ReadBPSLimit] = tps
	limits[api.WriteBPSLimit] = tps

	tpsBurstLimit := burstFactor * tps
	limits[api.BPSBurstLimit] = tpsBurstLimit
	limits[api.ReadBPSBurstLimit] = tpsBurstLimit
	limits[api.WriteBPSBurstLimit] = tpsBurstLimit

	limits[api.BPSBurstDurationLimit] = burstDurationInSeconds

	return limits
}

func CalculateV2(iops, tps int64, burstFactor, burstDurationInSeconds int64) apiv2.Limits {
	limits := map[apiv2.LimitType]int64{}

	var scale int64 = 1

	iops = iops * scale
	limits[apiv2.IOPSLimit] = iops
	limits[apiv2.ReadIOPSLimit] = iops
	limits[apiv2.WriteIOPSLimit] = iops

	iopsBurstLimit := burstFactor * iops
	limits[apiv2.IOPSBurstLimit] = iopsBurstLimit
	limits[apiv2.ReadIOPSBurstLimit] = iopsBurstLimit
	limits[apiv2.WriteIOPSBurstLimit] = iopsBurstLimit

	limits[apiv2.IOPSBurstDurationLimit] = burstDurationInSeconds

	tps = tps * scale
	limits[apiv2.BPSLimit] = tps
	limits[apiv2.ReadBPSLimit] = tps
	limits[apiv2.WriteBPSLimit] = tps

	tpsBurstLimit := burstFactor * tps
	limits[apiv2.BPSBurstLimit] = tpsBurstLimit
	limits[apiv2.ReadBPSBurstLimit] = tpsBurstLimit
	limits[apiv2.WriteBPSBurstLimit] = tpsBurstLimit

	limits[apiv2.BPSBurstDurationLimit] = burstDurationInSeconds

	return limits
}
