// SPDX-FileCopyrightText: 2022 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package limits

import (
	"github.com/ironcore-dev/ceph-provider/pkg/api"
)

func Calculate(iops, tps int64, burstFactor, burstDurationInSeconds int64) api.Limits {
	limits := map[api.LimitType]int64{}

	//TODO: scaling dependent on size
	var scale int64 = 1

	//IOPS
	iops = iops * scale
	limits[api.IOPSlLimit] = iops
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
