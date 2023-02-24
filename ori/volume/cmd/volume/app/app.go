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

	"github.com/onmetal/cephlet/ori/volume/server"
	"github.com/onmetal/controller-utils/configutils"
	"github.com/onmetal/onmetal-api/broker/common"
	ori "github.com/onmetal/onmetal-api/ori/apis/volume/v1alpha1"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"google.golang.org/grpc"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

type Options struct {
	Kubeconfig string
	Address    string

	Namespace string

	RookNamespace            string
	RookClusterName          string
	RookPoolName             string
	RookClientName           string
	RookPoolSecretName       string
	RookPoolMonitorConfigmap string
	RookPoolStorageClass     string

	Driver    string
	WwnPrefix string

	VolumeClassSelector map[string]string
}

//TODO: redo flags once csi dependency is removed

func (o *Options) AddFlags(fs *pflag.FlagSet) {
	fs.StringVar(&o.Kubeconfig, "kubeconfig", o.Kubeconfig, "Path pointing to a kubeconfig file to use.")
	fs.StringVar(&o.Address, "address", "/var/run/ori-volume.sock", "Address to listen on.")

	fs.StringVar(&o.Namespace, "namespace", o.Namespace, "Target Kubernetes namespace to use.")

	fs.StringVar(&o.RookNamespace, "rook-namespace", o.RookNamespace, "TODO.")
	fs.StringVar(&o.RookClusterName, "rook-cluster-name", o.RookClusterName, "TODO.")
	fs.StringVar(&o.RookPoolName, "rook-pool-name", o.RookPoolName, "TODO.")
	fs.StringVar(&o.RookClientName, "rook-client-name", o.RookClientName, "TODO.")
	fs.StringVar(&o.RookPoolSecretName, "rook-pool-secret-name", o.RookPoolSecretName, "TODO.")
	fs.StringVar(&o.RookPoolMonitorConfigmap, "rook-pool-monitor-configmap", o.RookPoolMonitorConfigmap, "TODO.")
	fs.StringVar(&o.RookPoolStorageClass, "rook-pool-storage-class", "standard", "TODO.")

	fs.StringVar(&o.Driver, "driver", "ceph", "driver.")
	fs.StringVar(&o.WwnPrefix, "wwn-prefix", "", "wwn-prefix.")

	fs.StringToStringVar(&o.VolumeClassSelector, "volume-class-selector", nil, "Selector for volume classes to report as available.")
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

	opts.AddFlags(cmd.Flags())

	return cmd
}

func Run(ctx context.Context, opts Options) error {
	log := ctrl.LoggerFrom(ctx)
	setupLog := log.WithName("setup")

	cfg, err := configutils.GetConfig(configutils.Kubeconfig(opts.Kubeconfig))
	if err != nil {
		return err
	}

	srv, err := server.New(cfg, server.Options{
		Namespace: opts.Namespace,

		RookNamespace:            opts.RookNamespace,
		RookClusterName:          opts.RookClusterName,
		RookPoolName:             opts.RookPoolName,
		RookClientName:           opts.RookClientName,
		RookPoolSecretName:       opts.RookPoolSecretName,
		RookPoolMonitorConfigmap: opts.RookPoolMonitorConfigmap,
		RookPoolStorageClass:     opts.RookPoolStorageClass,

		Driver:    opts.Driver,
		WwnPrefix: opts.WwnPrefix,

		VolumeClassSelector: opts.VolumeClassSelector,
	})
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
