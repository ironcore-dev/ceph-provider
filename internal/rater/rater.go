// SPDX-FileCopyrightText: 2023 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package rater

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
