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

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/ceph/go-ceph/rados"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/onmetal/cephlet/ori/volume/cmd/volume/app"
	"github.com/onmetal/onmetal-api/ori/apis/volume/v1alpha1"
	"github.com/onmetal/onmetal-api/ori/remote/volume"
)

var (
	srvCtx context.Context
	cancel context.CancelFunc

	volumeClient v1alpha1.VolumeRuntimeClient
	ioctx        *rados.IOContext
)

func TestIntegration_GRPCServer(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "GRPC Server Suite", Label("integration"))
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	keyEncryptionKeyFile, err := os.CreateTemp(GinkgoT().TempDir(), "keyencryption")
	Expect(err).NotTo(HaveOccurred())
	defer func() {
		_ = keyEncryptionKeyFile.Close()
	}()
	Expect(os.WriteFile(keyEncryptionKeyFile.Name(), []byte("abcjdkekakakakakakakkadfkkasfdks"), 0666)).To(Succeed())

	volumeClasses := []v1alpha1.VolumeClass{{
		Name: "foo",
		Capabilities: &v1alpha1.VolumeClassCapabilities{
			Tps:  100,
			Iops: 100,
		},
	}}
	volumeClassesData, err := json.Marshal(volumeClasses)
	Expect(err).NotTo(HaveOccurred())

	volumeClassesFile, err := os.CreateTemp(GinkgoT().TempDir(), "volumeclasses")
	Expect(err).NotTo(HaveOccurred())
	defer func() {
		_ = volumeClassesFile.Close()
	}()
	Expect(os.WriteFile(volumeClassesFile.Name(), volumeClassesData, 0666)).To(Succeed())

	opts := app.Options{
		Address:                    fmt.Sprintf("%s/cephlet-volume.sock", os.Getenv("PWD")),
		PathSupportedVolumeClasses: volumeClassesFile.Name(),
		Ceph: app.CephOptions{
			ConnectTimeout:         10 * time.Second,
			Monitors:               os.Getenv("CEPH_MONITORS"),
			User:                   os.Getenv("CEPH_USERNAME"),
			KeyringFile:            os.Getenv("CEPH_KEYRING_FILENAME"),
			Pool:                   os.Getenv("CEPH_POOLNAME"),
			Client:                 os.Getenv("CEPH_CLIENTNAME"),
			KeyEncryptionKeyPath:   keyEncryptionKeyFile.Name(),
			BurstDurationInSeconds: 15,
		},
	}

	srvCtx, cancel = context.WithCancel(context.Background())
	DeferCleanup(cancel)

	go func() {
		defer GinkgoRecover()
		Expect(app.Run(srvCtx, opts)).To(Succeed())
	}()

	Eventually(func() (bool, error) {
		return isSocketAvailable(opts.Address)
	}, "30s", "500ms").Should(BeTrue(), "The UNIX socket file should be available")

	address, err := volume.GetAddressWithTimeout(3*time.Second, fmt.Sprintf("unix://%s", opts.Address))
	Expect(err).NotTo(HaveOccurred())

	gconn, err := grpc.Dial(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	Expect(err).NotTo(HaveOccurred())

	volumeClient = v1alpha1.NewVolumeRuntimeClient(gconn)
	DeferCleanup(gconn.Close)

	conn, err := rados.NewConn()
	Expect(err).NotTo(HaveOccurred())

	Expect(conn.ReadConfigFile(os.Getenv("CEPH_CONFIG_FILE"))).ToNot(HaveOccurred())

	Expect(conn.Connect()).ToNot(HaveOccurred())
	DeferCleanup(conn.Shutdown)

	pools, err := conn.ListPools()
	Expect(err).NotTo(HaveOccurred())
	Expect(pools).To(ContainElement(opts.Ceph.Pool))

	ioctx, err = conn.OpenIOContext(opts.Ceph.Pool)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(ioctx.Destroy)
})

func isSocketAvailable(socketPath string) (bool, error) {
	fileInfo, err := os.Stat(socketPath)
	if err != nil {
		return false, err
	}
	if fileInfo.Mode()&os.ModeSocket != 0 {
		return true, nil
	}
	return false, nil
}
