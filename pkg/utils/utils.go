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
	"errors"

	"golang.org/x/exp/slices"
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
