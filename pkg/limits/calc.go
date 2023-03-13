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

import "fmt"

const (
	IOPSlLimit                  LimitType = "rbd_qos_iops_limit"
	IOPSBurstLimit              LimitType = "rbd_qos_iops_burst"
	IOPSBurstDurationLimit      LimitType = "rbd_qos_iops_burst_seconds"
	ReadIOPSLimit               LimitType = "rbd_qos_read_iops_limit"
	ReadIOPSBurstLimit          LimitType = "rbd_qos_read_iops_burst"
	ReadIOPSBurstDurationLimit  LimitType = "rbd_qos_read_iops_burst_seconds"
	WriteIOPSLimit              LimitType = "rbd_qos_write_iops_limit"
	WriteIOPSBurstLimit         LimitType = "rbd_qos_write_iops_burst"
	WriteIOPSBurstDurationLimit LimitType = "rbd_qos_write_iops_burst_seconds"
	BPSLimit                    LimitType = "rbd_qos_bps_limit"
	BPSBurstLimit               LimitType = "rbd_qos_bps_burst"
	BPSBurstDurationLimit       LimitType = "rbd_qos_bps_burst_seconds"
	ReadBPSLimit                LimitType = "rbd_qos_read_bps_limit"
	ReadBPSBurstLimit           LimitType = "rbd_qos_read_bps_burst"
	ReadBPSBurstDurationLimit   LimitType = "rbd_qos_read_bps_burst_seconds"
	WriteBPSLimit               LimitType = "rbd_qos_write_bps_limit"
	WriteBPSBurstLimit          LimitType = "rbd_qos_write_bps_burst"
	WriteBPSBurstDurationLimit  LimitType = "rbd_qos_write_bps_burst_seconds"
)

type LimitType string

func DefaultLimits() Limits {
	return map[LimitType]int64{}
}

type Limits map[LimitType]int64

func (l Limits) String() map[string]string {
	limitData := map[string]string{}
	for limit, limitValue := range l {
		limitData[(string(limit))] = fmt.Sprintf("%d", limitValue)
	}

	return limitData
}

func Calculate(iops, tps int64, burstFactor, burstDurationInSeconds int64) Limits {
	limits := DefaultLimits()

	//TODO: scaling dependent on size
	var scale int64 = 1

	//IOPS
	iops = iops * scale
	limits[IOPSlLimit] = iops
	limits[ReadIOPSLimit] = iops
	limits[WriteIOPSLimit] = iops

	iopsBurstLimit := burstFactor * iops
	limits[IOPSBurstLimit] = iopsBurstLimit
	limits[ReadIOPSBurstLimit] = iopsBurstLimit
	limits[WriteIOPSBurstLimit] = iopsBurstLimit

	limits[IOPSBurstDurationLimit] = burstDurationInSeconds

	//TPS
	tps = tps * scale
	limits[BPSLimit] = tps
	limits[ReadBPSLimit] = tps
	limits[WriteBPSLimit] = tps

	tpsBurstLimit := burstFactor * tps
	limits[BPSBurstLimit] = tpsBurstLimit
	limits[ReadBPSBurstLimit] = tpsBurstLimit
	limits[WriteBPSBurstLimit] = tpsBurstLimit

	limits[BPSBurstDurationLimit] = burstDurationInSeconds

	return limits
}
