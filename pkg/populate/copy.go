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

package populate

import (
	"fmt"
	"io"
	"time"
)

func NewRater(r io.Reader) *rate {
	return &rate{
		r: r,
	}
}

type rate struct {
	r          io.Reader
	count      int64
	start, end time.Time
}

func (r *rate) Read(b []byte) (n int, err error) {
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

func (r *rate) Rate() (n int64, d time.Duration) {
	end := r.end
	if end.IsZero() {
		end = time.Now()
	}
	return r.count, end.Sub(r.start)
}

func (r *rate) String() string {
	n, d := r.Rate()
	return fmt.Sprintf("%.0f b/s", float64(n)/(d.Seconds()))
}
