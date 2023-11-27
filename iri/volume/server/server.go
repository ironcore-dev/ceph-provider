// SPDX-FileCopyrightText: 2022 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"

	"github.com/go-logr/logr"
	cephapi "github.com/ironcore-dev/ceph-provider/pkg/api"
	"github.com/ironcore-dev/ceph-provider/pkg/ceph"
	"github.com/ironcore-dev/ceph-provider/pkg/encryption"
	"github.com/ironcore-dev/ceph-provider/pkg/store"
	"github.com/ironcore-dev/ironcore/broker/common/idgen"
	iri "github.com/ironcore-dev/ironcore/iri/apis/volume/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
)

type VolumeClassRegistry interface {
	Get(volumeClassName string) (*iri.VolumeClass, bool)
	List() []*iri.VolumeClass
}

type Server struct {
	idGen idgen.IDGen

	imageStore    store.Store[*cephapi.Image]
	snapshotStore store.Store[*cephapi.Snapshot]

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
}

func setOptionsDefaults(o *Options) {
	if o.IDGen == nil {
		o.IDGen = idgen.Default
	}
}

var _ iri.VolumeRuntimeServer = (*Server)(nil)

func New(imageStore store.Store[*cephapi.Image],
	snapshotStore store.Store[*cephapi.Snapshot],
	volumeClassRegistry VolumeClassRegistry,
	keyEncryption encryption.Encryptor,
	cephCommandClient ceph.Command,
	opts Options,
) (*Server, error) {

	setOptionsDefaults(&opts)

	return &Server{
		idGen:         opts.IDGen,
		imageStore:    imageStore,
		snapshotStore: snapshotStore,
		volumeClasses: volumeClassRegistry,

		keyEncryption:     keyEncryption,
		cephCommandClient: cephCommandClient,

		burstFactor:            opts.BurstFactor,
		burstDurationInSeconds: opts.BurstDurationInSeconds,
	}, nil
}
