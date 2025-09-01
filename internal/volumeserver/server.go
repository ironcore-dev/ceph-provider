// SPDX-FileCopyrightText: 2022 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package volumeserver

import (
	"context"

	"github.com/go-logr/logr"
	"github.com/ironcore-dev/ceph-provider/api"
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

	imageStore       store.Store[*api.Image]
	snapshotStore    store.Store[*api.Snapshot]
	volumeEventStore recorder.EventStore

	volumeClasses     VolumeClassRegistry
	cephCommandClient ceph.Command

	burstFactor            int64
	burstDurationInSeconds int64

	keyEncryption encryption.Encryptor
}

// DeleteVolumeSnapshotContent implements v1alpha1.VolumeRuntimeServer.
func (s *Server) DeleteVolumeSnapshotContent(context.Context, *iri.DeleteVolumeSnapshotContentRequest) (*iri.DeleteVolumeSnapshotContentResponse, error) {
	panic("unimplemented")
}

// ListVolumeSnapshotContents implements v1alpha1.VolumeRuntimeServer.
func (s *Server) ListVolumeSnapshotContents(context.Context, *iri.ListVolumeSnapshotContentsRequest) (*iri.ListVolumeSnapshotContentsResponse, error) {
	panic("unimplemented")
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

func New(imageStore store.Store[*api.Image],
	snapshotStore store.Store[*api.Snapshot],
	volumeClassRegistry VolumeClassRegistry,
	keyEncryption encryption.Encryptor,
	cephCommandClient ceph.Command,
	opts Options,
) (*Server, error) {

	setOptionsDefaults(&opts)

	return &Server{
		idGen:            opts.IDGen,
		imageStore:       imageStore,
		snapshotStore:    snapshotStore,
		volumeEventStore: opts.VolumeEventStore,
		volumeClasses:    volumeClassRegistry,

		keyEncryption:     keyEncryption,
		cephCommandClient: cephCommandClient,

		burstFactor:            opts.BurstFactor,
		burstDurationInSeconds: opts.BurstDurationInSeconds,
	}, nil
}
