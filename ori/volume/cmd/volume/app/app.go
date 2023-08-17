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
	"os"
	"sync"
	"time"

	"github.com/onmetal/cephlet/ori/volume/server"
	"github.com/onmetal/cephlet/pkg/api"
	"github.com/onmetal/cephlet/pkg/ceph"
	"github.com/onmetal/cephlet/pkg/controllers"
	"github.com/onmetal/cephlet/pkg/encryption"
	"github.com/onmetal/cephlet/pkg/event"
	"github.com/onmetal/cephlet/pkg/omap"
	"github.com/onmetal/cephlet/pkg/utils"
	"github.com/onmetal/cephlet/pkg/vcr"
	"github.com/onmetal/onmetal-api/broker/common"
	ori "github.com/onmetal/onmetal-api/ori/apis/volume/v1alpha1"
	"github.com/onmetal/onmetal-image/oci/remote"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"google.golang.org/grpc"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

type Options struct {
	Address string

	PathSupportedVolumeClasses string

	Ceph CephOptions
}

type CephOptions struct {
	Monitors    string
	User        string
	KeyFile     string
	KeyringFile string
	Pool        string
	Client      string

	ConnectTimeout time.Duration

	BurstFactor            int64
	BurstDurationInSeconds int64

	PopulatorBufferSize int64

	KeyEncryptionKeyPath string
}

func (o *Options) Defaults() {
	o.Ceph.ConnectTimeout = 10 * time.Second
	o.Ceph.BurstFactor = 10
	o.Ceph.BurstDurationInSeconds = 15
	o.Ceph.PopulatorBufferSize = 5 * 1024 * 1024
}

func (o *Options) AddFlags(fs *pflag.FlagSet) {
	fs.StringVar(&o.Address, "address", "/var/run/cephlet-volume.sock", "Address to listen on.")

	fs.StringVar(&o.PathSupportedVolumeClasses, "supported-volume-classes", o.PathSupportedVolumeClasses, "File containing supported volume classes.")

	fs.Int64Var(&o.Ceph.BurstFactor, "limits-burst-factor", o.Ceph.BurstFactor, "Defines the factor to calculate the burst limits.")
	fs.Int64Var(&o.Ceph.BurstDurationInSeconds, "limits-burst-duration", o.Ceph.BurstDurationInSeconds, "Defines the burst duration in seconds.")

	fs.Int64Var(&o.Ceph.PopulatorBufferSize, "populator-buffer-size", o.Ceph.PopulatorBufferSize, "Defines the buffer size (in bytes) which is used for downloading a image.")

	fs.StringVar(&o.Ceph.Monitors, "ceph-monitors", o.Ceph.Monitors, "Ceph Monitors to connect to.")
	fs.DurationVar(&o.Ceph.ConnectTimeout, "ceph-connect-timeout", o.Ceph.ConnectTimeout, "Connect timeout for establishing a connection to ceph.")
	fs.StringVar(&o.Ceph.User, "ceph-user", o.Ceph.User, "Ceph User.")
	fs.StringVar(&o.Ceph.KeyFile, "ceph-key-file", o.Ceph.KeyFile, "ceph-key-file or ceph-keyring-file must be provided (ceph-key-file has precedence). ceph-key-file contains contains only the ceph key.")
	fs.StringVar(&o.Ceph.KeyringFile, "ceph-keyring-file", o.Ceph.KeyringFile, "ceph-key-file or ceph-keyring-file must be provided (ceph-key-file has precedence)s. ceph-keyring-file contains the ceph key and client information.")
	fs.StringVar(&o.Ceph.Pool, "ceph-pool", o.Ceph.Pool, "Ceph pool which is used to store objects.")
	fs.StringVar(&o.Ceph.Client, "ceph-client", o.Ceph.Client, "Ceph client which grants access to pools/images eg. 'client.volumes'")
	fs.StringVar(&o.Ceph.KeyEncryptionKeyPath, "ceph-kek-path", o.Ceph.KeyEncryptionKeyPath, "path to the key encryption key file (32 Bit - KEK) to encrypt volume keys.")
}

func (o *Options) MarkFlagsRequired(cmd *cobra.Command) {
	_ = cmd.MarkFlagRequired("available-volume-classes")
	_ = cmd.MarkFlagRequired("ceph-monitors")
	_ = cmd.MarkFlagRequired("ceph-pool")
	_ = cmd.MarkFlagRequired("ceph-kek-path")
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

func configureCephAuth(opts *CephOptions) (func() error, error) {
	noOpCleanup := func() error { return nil }
	if opts.KeyFile == "" && opts.KeyringFile == "" {
		return noOpCleanup, fmt.Errorf("ceph-key-file or ceph-keyring-file needs to be defined")
	}

	if opts.KeyFile != "" {
		return noOpCleanup, nil
	}

	key, err := ceph.GetKeyFromKeyring(opts.KeyringFile)
	if err != nil {
		return noOpCleanup, fmt.Errorf("failed to get key from keyring: %w", err)
	}

	file, err := os.CreateTemp("", "key")
	if err != nil {
		return noOpCleanup, fmt.Errorf("failed to create temp file: %w", err)
	}
	cleanup := func() error {
		return os.Remove(file.Name())
	}

	_, err = file.WriteString(key)
	if err != nil {
		return cleanup, fmt.Errorf("failed to write key to temp file: %w", err)
	}

	opts.KeyFile = file.Name()

	return cleanup, nil
}

func Run(ctx context.Context, opts Options) error {
	log := ctrl.LoggerFrom(ctx)
	setupLog := log.WithName("setup")
	var wg sync.WaitGroup

	cleanup, err := configureCephAuth(&opts.Ceph)
	if err != nil {
		return fmt.Errorf("failed to configure ceph auth: %w", err)
	}
	defer func() {
		err := cleanup()
		if err != nil {
			setupLog.Error(err, "failed to cleanup")
		}
	}()

	setupLog.Info("Initializing key encryptor")
	encryptor, err := encryption.NewAesGcmEncryptor(opts.Ceph.KeyEncryptionKeyPath)
	if err != nil {
		return fmt.Errorf("failed to init encryptor: %w", err)
	}

	setupLog.Info("Establishing ceph connection", "Monitors", opts.Ceph.Monitors, "User", opts.Ceph.User, "Timeout", opts.Ceph.ConnectTimeout)
	connectCtx, cancelConnect := context.WithTimeout(ctx, opts.Ceph.ConnectTimeout)
	defer cancelConnect()
	conn, err := ceph.ConnectToRados(connectCtx, ceph.Credentials{
		Monitors: opts.Ceph.Monitors,
		User:     opts.Ceph.User,
		Keyfile:  opts.Ceph.KeyFile,
	})
	if err != nil {
		return fmt.Errorf("failed to establish rados connection: %w", err)
	}

	if err := ceph.CheckIfPoolExists(conn, opts.Ceph.Pool); err != nil {
		return fmt.Errorf("configuration invalid: %w", err)
	}

	setupLog.Info("Configuring image store", "OmapName", omap.OmapNameVolumes)
	imageStore, err := omap.New(conn, opts.Ceph.Pool, omap.Options[*api.Image]{
		OmapName:       omap.OmapNameVolumes,
		NewFunc:        func() *api.Image { return &api.Image{} },
		CreateStrategy: utils.ImageStrategy,
	})
	if err != nil {
		return fmt.Errorf("failed to initialize image store: %w", err)
	}

	imageEvents, err := event.NewListWatchSource[*api.Image](
		imageStore.List,
		imageStore.Watch,
		event.ListWatchSourceOptions{},
	)
	if err != nil {
		return fmt.Errorf("failed to initialize image events: %w", err)
	}

	setupLog.Info("Configuring snapshot store", "OmapName", omap.OmapNameOsImages)
	snapshotStore, err := omap.New(conn, opts.Ceph.Pool, omap.Options[*api.Snapshot]{
		OmapName:       omap.OmapNameOsImages,
		NewFunc:        func() *api.Snapshot { return &api.Snapshot{} },
		CreateStrategy: utils.SnapshotStrategy,
	})
	if err != nil {
		return fmt.Errorf("failed to initialize snapshot store: %w", err)
	}

	snapshotEvents, err := event.NewListWatchSource[*api.Snapshot](
		snapshotStore.List,
		snapshotStore.Watch,
		event.ListWatchSourceOptions{},
	)
	if err != nil {
		return fmt.Errorf("failed to initialize snapshot events: %w", err)
	}

	reg, err := remote.DockerRegistry(nil)
	if err != nil {
		return fmt.Errorf("failed to initialize docker registry: %w", err)
	}

	imageReconciler, err := controllers.NewImageReconciler(
		log.WithName("image-reconciler"),
		conn,
		reg,
		imageStore, snapshotStore,
		imageEvents,
		snapshotEvents,
		encryptor,
		controllers.ImageReconcilerOptions{
			Monitors: opts.Ceph.Monitors,
			Client:   opts.Ceph.Client,
			Pool:     opts.Ceph.Pool,
		},
	)
	if err != nil {
		return fmt.Errorf("failed to initialize image reconciler: %w", err)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		setupLog.Info("Starting image reconciler")
		if err := imageReconciler.Start(ctx); err != nil {
			log.Error(err, "failed to start image reconciler")
		}
	}()

	snapshotReconciler, err := controllers.NewSnapshotReconciler(
		log.WithName("snapshot-reconciler"),
		conn,
		reg,
		snapshotStore,
		snapshotEvents,
		controllers.SnapshotReconcilerOptions{
			Pool:                opts.Ceph.Pool,
			PopulatorBufferSize: opts.Ceph.PopulatorBufferSize,
		},
	)
	if err != nil {
		return fmt.Errorf("failed to initialize snapshot reconciler: %w", err)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		setupLog.Info("Starting snapshot reconciler")
		if err := snapshotReconciler.Start(ctx); err != nil {
			log.Error(err, "failed to start snapshot reconciler")
		}

	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		setupLog.Info("Starting image events")
		if err := imageEvents.Start(ctx); err != nil {
			log.Error(err, "failed to start image events")
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		setupLog.Info("Starting snapshot events")
		if err := snapshotEvents.Start(ctx); err != nil {
			log.Error(err, "failed to start snapshot events")
		}
	}()

	supportedClasses, err := vcr.LoadVolumeClassesFile(opts.PathSupportedVolumeClasses)
	if err != nil {
		return fmt.Errorf("failed to load supported volume classes: %w", err)
	}

	classRegistry, err := vcr.NewVolumeClassRegistry(supportedClasses)
	if err != nil {
		return fmt.Errorf("failed to initialize volume class registry : %w", err)
	}

	srv, err := server.New(
		imageStore,
		snapshotStore,
		classRegistry,
		encryptor,
		server.Options{
			BurstFactor:            opts.Ceph.BurstFactor,
			BurstDurationInSeconds: opts.Ceph.BurstDurationInSeconds,
		},
	)
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
