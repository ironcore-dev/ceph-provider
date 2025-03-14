// SPDX-FileCopyrightText: 2023 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package utils

import (
	"errors"
	"slices"
)

func DeleteSliceElement[E comparable](s []E, elem E) []E {
	idx := slices.Index(s, elem)
	if idx < 0 {
		return s
	}

	return slices.Delete(s, idx, idx+1)
}

func Zero[E any]() E {
	var zero E
	return zero
}

// Uint64ToInt64 converts a uint64 to an int64 and returns an error if the value is out of range.
func Uint64ToInt64(u uint64) (int64, error) {
	if u > 1<<63-1 {
		return 0, errors.New("uint64 value is out of int64 range")
	}
	return int64(u), nil
}

// Int64ToUint64 converts an int64 to an uint64 and returns an error if the value is negative.
func Int64ToUint64(i int64) (uint64, error) {
	if i < 0 {
		return 0, errors.New("failed to convert a negative int64 to uint64")
	}
	return uint64(i), nil
}
