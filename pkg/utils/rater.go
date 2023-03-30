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
	"fmt"
	"io"
	"time"
)

func NewRater(r io.Reader) *Rater {
	return &Rater{
		r: r,
	}
}

type Rater struct {
	r          io.Reader
	count      int64
	start, end time.Time
}

func (r *Rater) Read(b []byte) (n int, err error) {
	if r.start.IsZero() {
		r.start = time.Now()
	}

	n, err = r.r.Read(b)
	r.count += int64(n)
	if err == io.EOF {
		r.end = time.Now()
	}

	return
}

func (r *Rater) Rate() (n int64, d time.Duration) {
	start := r.start
	end := r.end
	if end.IsZero() {
		end = time.Now()
	}
	if start.IsZero() {
		return r.count, 0
	}
	return r.count, end.Sub(r.start)
}

func (r *Rater) String() string {
	n, d := r.Rate()
	if d.Seconds() == 0 {
		return "0 b/s"
	}

	return fmt.Sprintf("%.0f b/s", float64(n)/(d.Seconds()))
}
