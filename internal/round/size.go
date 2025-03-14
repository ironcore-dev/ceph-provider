// SPDX-FileCopyrightText: 2023 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package round

import "math"

const (
	// GB - GigaByte size
	GB = 1000 * 1000 * 1000
	// GiB - GibiByte size
	GiB = 1024 * 1024 * 1024

	// MB - MegaByte size
	MB = 1000 * 1000
	// MiB - MebiByte size
	MiB = 1024 * 1024

	// KB - KiloByte size
	KB = 1000
	// KiB - KibiByte size
	KiB = 1024
)

// OffBytes converts roundoff the size
// 1.1Mib will be round off to 2Mib same for GiB
// size less than 1MiB will be round off to 1MiB.
func OffBytes(bytes uint64) uint64 {
	var num uint64
	// round off the value if its in decimal
	if floatBytes := float64(bytes); floatBytes < GiB {
		num = uint64(math.Ceil(floatBytes / MiB))
		num *= MiB
	} else {
		num = uint64(math.Ceil(floatBytes / GiB))
		num *= GiB
	}

	return num
}
