// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package volumeserver

import (
	"context"

	"github.com/go-logr/logr"
	api "github.com/ironcore-dev/ceph-provider/api/v2"
	"github.com/ironcore-dev/ceph-provider/internal/ceph"
	"github.com/ironcore-dev/ceph-provider/internal/encryption"
	"github.com/ironcore-dev/ironcore/broker/common/idgen"
	iri "github.com/ironcore-dev/ironcore/iri/apis/volume/v1alpha1"
	"github.com/ironcore-dev/provider-utils/eventutils/recorder"
	"github.com/ironcore-dev/provider-utils/storeutils/store"
	ctrl "sigs.k8s.io/controller-runtime"
)

type VolumeClassRegistry interface {
	Get(volumeClassName string) (*iri.VolumeClass, bool)
	List() []*iri.VolumeClass
}

type Server struct {
	iri.UnimplementedVolumeRuntimeServer
	idGen idgen.IDGen

	volumeStore      store.Store[*api.Volume]
	snapshotStore    store.Store[*api.Snapshot]
	volumeEventStore recorder.EventStore

	volumeClasses     VolumeClassRegistry
	cephCommandClient ceph.Command

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

	VolumeEventStore recorder.EventStore
}

func setOptionsDefaults(o *Options) {
	if o.IDGen == nil {
		o.IDGen = idgen.Default
	}
}

var _ iri.VolumeRuntimeServer = (*Server)(nil)

func New(
	volumeStore store.Store[*api.Volume],
	snapshotStore store.Store[*api.Snapshot],
	volumeClassRegistry VolumeClassRegistry,
	keyEncryption encryption.Encryptor,
	cephCommandClient ceph.Command,
	opts Options,
) (*Server, error) {
	setOptionsDefaults(&opts)

	return &Server{
		idGen:            opts.IDGen,
		volumeStore:      volumeStore,
		snapshotStore:    snapshotStore,
		volumeEventStore: opts.VolumeEventStore,
		volumeClasses:    volumeClassRegistry,

		keyEncryption:     keyEncryption,
		cephCommandClient: cephCommandClient,

		burstFactor:            opts.BurstFactor,
		burstDurationInSeconds: opts.BurstDurationInSeconds,
	}, nil
}
