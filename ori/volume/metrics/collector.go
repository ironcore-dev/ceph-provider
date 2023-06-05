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

package metrics

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"github.com/onmetal/cephlet/pkg/api"
	orimetrics "github.com/onmetal/onmetal-api/ori/apis/metrics/v1alpha1"
)

const (
	metricName    = "volume_pool_usage"
	requestedSize = "requested_size"
	requestedTps  = "requested_tps"
	requestedIops = "requested_iops"
)

func New(
	listImages listImagesFunc,
	listSnapshots listSnapshotsFunc,
	refreshInterval time.Duration,
) *Collector {
	return &Collector{
		listImages:      listImages,
		listSnapshots:   listSnapshots,
		refreshInterval: refreshInterval,
		descriptors: []*orimetrics.MetricDescriptor{
			{
				Name: metricName,
				Help: "Current pool usage, partitioned by pool and resource.",
				LabelKeys: []string{
					"pool",
					"resource",
				},
			},
		},
	}
}

type listImagesFunc func(ctx context.Context) ([]*api.Image, error)
type listSnapshotsFunc func(ctx context.Context) ([]*api.Snapshot, error)

type Collector struct {
	cephPool        string
	log             logr.Logger
	refreshInterval time.Duration

	listImages    listImagesFunc
	listSnapshots listSnapshotsFunc

	mutex       sync.Mutex
	lastMetrics []*orimetrics.Metric

	descriptors []*orimetrics.MetricDescriptor
}

func (c *Collector) Start(ctx context.Context) {
	if err := c.calcMetrics(ctx); err != nil {
		c.log.Error(err, "failed to calc metrics")
	}

	ticker := time.NewTicker(c.refreshInterval)

	for {
		select {
		case <-ticker.C:
			if err := c.calcMetrics(ctx); err != nil {
				c.log.Error(err, "failed to calc metrics")
			}
		case <-ctx.Done():
		}
	}
}

func (c *Collector) calcMetrics(ctx context.Context) error {
	images, err := c.listImages(ctx)
	if err != err {
		return fmt.Errorf("failed to list images: %w", err)
	}

	var (
		size, tps, iops uint64
		currentMetrics  []*orimetrics.Metric
		currentTime     = time.Now()
	)

	for _, image := range images {
		size += image.Spec.Size
		tps += uint64(image.Spec.Limits[api.BPSLimit])
		iops += uint64(image.Spec.Limits[api.IOPSlLimit])
	}

	currentMetrics = append(currentMetrics, &orimetrics.Metric{
		Name:       metricName,
		Timestamp:  currentTime.UnixNano(),
		MetricType: orimetrics.MetricType_GAUGE,
		LabelValues: []string{
			c.cephPool,
			requestedSize,
		},
		Value: size,
	})
	currentMetrics = append(currentMetrics, &orimetrics.Metric{
		Name:       metricName,
		Timestamp:  currentTime.UnixNano(),
		MetricType: orimetrics.MetricType_GAUGE,
		LabelValues: []string{
			c.cephPool,
			requestedIops,
		},
		Value: iops,
	})
	currentMetrics = append(currentMetrics, &orimetrics.Metric{
		Name:       metricName,
		Timestamp:  currentTime.UnixNano(),
		MetricType: orimetrics.MetricType_GAUGE,
		LabelValues: []string{
			c.cephPool,
			requestedTps,
		},
		Value: tps,
	})

	c.mutex.Lock()
	defer c.mutex.Unlock()

	c.lastMetrics = currentMetrics

	return nil
}
func (c *Collector) GetMetricDescriptors() []*orimetrics.MetricDescriptor {
	return c.descriptors
}

func (c *Collector) GetMetrics() []*orimetrics.Metric {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	return c.lastMetrics
}
