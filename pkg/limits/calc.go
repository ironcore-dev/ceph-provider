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

package limits

import (
	"github.com/onmetal/cephlet/pkg/api"
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
