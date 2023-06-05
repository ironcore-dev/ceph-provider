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

package server

import (
	"context"

	"github.com/go-logr/logr"
	cephapi "github.com/onmetal/cephlet/pkg/api"
	"github.com/onmetal/cephlet/pkg/encryption"
	"github.com/onmetal/cephlet/pkg/store"
	"github.com/onmetal/onmetal-api/broker/common/idgen"
	orimetrics "github.com/onmetal/onmetal-api/ori/apis/metrics/v1alpha1"
	ori "github.com/onmetal/onmetal-api/ori/apis/volume/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
)

type VolumeClassRegistry interface {
	Get(volumeClassName string) (*ori.VolumeClass, bool)
	List() []*ori.VolumeClass
}

type MetricCollector interface {
	GetMetrics() []*orimetrics.Metric
	GetMetricDescriptors() []*orimetrics.MetricDescriptor
}

type Server struct {
	idGen idgen.IDGen

	imageStore    store.Store[*cephapi.Image]
	snapshotStore store.Store[*cephapi.Snapshot]

	volumeClasses    VolumeClassRegistry
	metricsCollector MetricCollector

	burstFactor            int64
	burstDurationInSeconds int64

	keyEncryption encryption.Encryptor
}

func (s *Server) loggerFrom(ctx context.Context, keysWithValues ...interface{}) logr.Logger {
	return ctrl.LoggerFrom(ctx, keysWithValues...)
}

type Options struct {
	IDGen idgen.IDGen

	BurstFactor            int64
	BurstDurationInSeconds int64
}

func setOptionsDefaults(o *Options) {
	if o.IDGen == nil {
		o.IDGen = idgen.Default
	}
}

var _ ori.VolumeRuntimeServer = (*Server)(nil)

//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=storage.api.onmetal.de,resources=volumes,verbs=get;list;watch;create;update;patch;delete

func New(
	imageStore store.Store[*cephapi.Image],
	snapshotStore store.Store[*cephapi.Snapshot],
	volumeClassRegistry VolumeClassRegistry,
	metricsCollector MetricCollector,
	keyEncryption encryption.Encryptor,
	opts Options,
) (*Server, error) {

	setOptionsDefaults(&opts)

	return &Server{
		idGen:         opts.IDGen,
		imageStore:    imageStore,
		snapshotStore: snapshotStore,

		volumeClasses:    volumeClassRegistry,
		metricsCollector: metricsCollector,

		keyEncryption: keyEncryption,

		burstFactor:            opts.BurstFactor,
		burstDurationInSeconds: opts.BurstDurationInSeconds,
	}, nil
}
