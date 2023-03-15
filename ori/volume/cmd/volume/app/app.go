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

package app

import (
	"context"
	goflag "flag"
	"fmt"
	"net"

	"github.com/onmetal/cephlet/ori/volume/provisioner"
	"github.com/onmetal/cephlet/ori/volume/server"
	"github.com/onmetal/onmetal-api/broker/common"
	ori "github.com/onmetal/onmetal-api/ori/apis/volume/v1alpha1"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"google.golang.org/grpc"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

type Options struct {
	Address string

	VolumeNameLabelName        string
	PathAvailableVolumeClasses string

	Ceph CephOptions
}

type CephOptions struct {
	Monitors string
	User     string
	KeyFile  string
	Pool     string
	Client   string

	BurstFactor            int64
	BurstDurationInSeconds int64

	PopulatorBufferSize int64
	LimitingEnabled     bool
}

func (o *Options) Defaults() {
	o.Ceph.LimitingEnabled = true
	o.Ceph.BurstFactor = 10
	o.Ceph.BurstDurationInSeconds = 15
	o.Ceph.PopulatorBufferSize = 5 * 1024 * 1024
}

func (o *Options) AddFlags(fs *pflag.FlagSet) {
	fs.StringVar(&o.Address, "address", "/var/run/ori-volume.sock", "Address to listen on.")

	fs.StringVar(&o.VolumeNameLabelName, "volume-name-label-name", o.VolumeNameLabelName, "Label name to fetch VolumeName.")
	fs.StringVar(&o.PathAvailableVolumeClasses, "available-volume-classes", o.PathAvailableVolumeClasses, "JSON File path of available volume classes.")

	fs.Int64Var(&o.Ceph.BurstFactor, "limits-burst-factor", o.Ceph.BurstFactor, "Defines the factor to calculate the burst limits.")
	fs.Int64Var(&o.Ceph.BurstDurationInSeconds, "limits-burst-duration", o.Ceph.BurstDurationInSeconds, "Defines the burst duration in seconds.")

	fs.Int64Var(&o.Ceph.PopulatorBufferSize, "populator-buffer-size", o.Ceph.PopulatorBufferSize, "Defines the buffer size (in bytes) which is used for downloading a image.")

	fs.StringVar(&o.Ceph.Monitors, "ceph-monitors", o.Ceph.Monitors, "Ceph Monitors to connect to.")
	fs.StringVar(&o.Ceph.User, "ceph-user", o.Ceph.User, "Ceph User.")
	fs.StringVar(&o.Ceph.KeyFile, "ceph-key-file", o.Ceph.KeyFile, "CephKeyFile.")
	fs.StringVar(&o.Ceph.Pool, "ceph-pool", o.Ceph.Pool, "Ceph pool which is used to store objects.")
	fs.StringVar(&o.Ceph.Client, "ceph-client", o.Ceph.Client, "Ceph client which grants access to pools/images eg. 'client.volumes'")
	fs.BoolVar(&o.Ceph.LimitingEnabled, "ceph-limiting-enabled", o.Ceph.LimitingEnabled, "Enable limiting of ceph images according VolumeClass capabilities'")
}

func (o *Options) MarkFlagsRequired(cmd *cobra.Command) {
	_ = cmd.MarkFlagRequired("available-volume-classes")
	_ = cmd.MarkFlagRequired("ceph-monitors")
	_ = cmd.MarkFlagRequired("ceph-key-file")
	_ = cmd.MarkFlagRequired("ceph-pool")
}

func Command() *cobra.Command {
	var (
		zapOpts = zap.Options{Development: true}
		opts    Options
	)

	cmd := &cobra.Command{
		Use: "volume",
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			logger := zap.New(zap.UseFlagOptions(&zapOpts))
			ctrl.SetLogger(logger)
			cmd.SetContext(ctrl.LoggerInto(cmd.Context(), ctrl.Log))
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return Run(cmd.Context(), opts)
		},
	}

	goFlags := goflag.NewFlagSet("", 0)
	zapOpts.BindFlags(goFlags)
	cmd.PersistentFlags().AddGoFlagSet(goFlags)

	opts.Defaults()
	opts.AddFlags(cmd.Flags())
	opts.MarkFlagsRequired(cmd)

	return cmd
}

func Run(ctx context.Context, opts Options) error {
	log := ctrl.LoggerFrom(ctx)
	setupLog := log.WithName("setup")

	provisioner := provisioner.New(log.WithName("provisioner"), &provisioner.Credentials{
		Monitors: opts.Ceph.Monitors,
		User:     opts.Ceph.User,
		Keyfile:  opts.Ceph.KeyFile,
	}, &provisioner.CephConfig{
		Client:                 opts.Ceph.Client,
		Pool:                   opts.Ceph.Pool,
		BurstFactor:            opts.Ceph.BurstFactor,
		BurstDurationInSeconds: opts.Ceph.BurstDurationInSeconds,
		PopulatorBufferSize:    opts.Ceph.PopulatorBufferSize,
		LimitingEnabled:        opts.Ceph.LimitingEnabled,
	})

	srv, err := server.New(server.Options{
		PathAvailableVolumeClasses: opts.PathAvailableVolumeClasses,
		VolumeNameLabelName:        opts.VolumeNameLabelName,
	}, provisioner)
	if err != nil {
		return fmt.Errorf("error creating server: %w", err)
	}

	log.V(1).Info("Cleaning up any previous socket")
	if err := common.CleanupSocketIfExists(opts.Address); err != nil {
		return fmt.Errorf("error cleaning up socket: %w", err)
	}

	log.V(1).Info("Start listening on unix socket", "Address", opts.Address)
	l, err := net.Listen("unix", opts.Address)
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}
	defer func() {
		if err := l.Close(); err != nil {
			log.Error(err, "Error closing socket")
		}
	}()

	grpcSrv := grpc.NewServer(
		grpc.UnaryInterceptor(func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp interface{}, err error) {
			log := log.WithName(info.FullMethod)
			ctx = ctrl.LoggerInto(ctx, log)
			log.V(1).Info("Request")
			resp, err = handler(ctx, req)
			if err != nil {
				log.Error(err, "Error handling request")
			}
			return resp, err
		}),
	)
	ori.RegisterVolumeRuntimeServer(grpcSrv, srv)

	setupLog.Info("Starting server", "Address", l.Addr().String())
	go func() {
		defer func() {
			setupLog.Info("Shutting down server")
			grpcSrv.Stop()
			setupLog.Info("Shut down server")
		}()
		<-ctx.Done()
	}()
	if err := grpcSrv.Serve(l); err != nil {
		return fmt.Errorf("error serving: %w", err)
	}
	return nil
}
