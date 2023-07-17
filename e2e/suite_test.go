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

package e2e

import (
	"flag"
	"testing"
	"time"

	"github.com/ceph/go-ceph/rados"
	"github.com/onmetal/controller-utils/configutils"
	rookv1 "github.com/rook/rook/pkg/apis/ceph.rook.io/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	storagev1alpha1 "github.com/onmetal/onmetal-api/api/storage/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	. "sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var (
	cfg       *rest.Config
	k8sClient client.Client
)

var (
	cephCluster *rookv1.CephCluster
	cephPool    *rookv1.CephBlockPool
	cephClient  *rookv1.CephClient
	conn        *rados.Conn
	cephOptions CephOptions
)

const (
	eventuallyTimeout    = 3 * time.Second
	pollingInterval      = 50 * time.Millisecond
	consistentlyDuration = 1 * time.Second
)

/*
type Options struct {
	Address string

	PathSupportedVolumeClasses string

	Ceph CephOptions
}
*/

type CephOptions struct {
	Monitors    string
	User        string
	KeyFile     string
	KeyringFile string
	Pool        string
	Client      string

	/*
		BurstFactor            int64
		BurstDurationInSeconds int64

		PopulatorBufferSize int64

		KeyEncryptionKeyPath string
	*/
}

func TestControllers(t *testing.T) {
	SetDefaultConsistentlyPollingInterval(pollingInterval)
	SetDefaultEventuallyPollingInterval(pollingInterval)
	SetDefaultEventuallyTimeout(eventuallyTimeout)
	SetDefaultConsistentlyDuration(consistentlyDuration)

	RegisterFailHandler(Fail)
	RunSpecs(t, "Controllers Suite")
}

var _ = BeforeSuite(func(ctx SpecContext) {
	InitFlags()
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	var err error
	By("bootstrapping test environment")

	cfg, err = configutils.GetConfig()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	Expect(storagev1alpha1.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(rookv1.AddToScheme(scheme.Scheme)).Should(Succeed())

	// Init package-level k8sClient
	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())
	SetClient(k8sClient)

	cephCluster = &rookv1.CephCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cluster",
			Namespace: "rook-ceph",
		},
	}
	Eventually(Object(cephCluster)).Should(SatisfyAll(
		HaveField("Status.Phase", rookv1.ConditionReady),
	))

	cephPool = &rookv1.CephBlockPool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cephlet-pool",
			Namespace: "rook-ceph",
		},
	}
	Eventually(Object(cephPool)).Should(SatisfyAll(
		HaveField("Status.Phase", rookv1.ConditionReady),
	))

	cephClient = &rookv1.CephClient{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cephlet-pool",
			Namespace: "rook-ceph",
		},
	}
	Eventually(Object(cephClient)).Should(SatisfyAll(
		HaveField("Status.Phase", rookv1.ConditionReady),
	))

	//goFlags := goflag.NewFlagSet("", 0)

	//opts.Defaults()
	//opts.AddFlags(cmd.Flags())
	//opts.MarkFlagsRequired()

	/*
			conn, err := ceph.ConnectToRados(ctx, ceph.Credentials{
				Monitors: opts.Ceph.Monitors,
				User:     opts.Ceph.User,
				Keyfile:  opts.Ceph.KeyFile,
			})

		Expect(err).NotTo(HaveOccurred())

		err = ceph.CheckIfPoolExists(conn, opts.Ceph.Pool)
		Expect(err).NotTo(HaveOccurred())
	*/

})

/*
func (o *Options) AddFlags(fs *pflag.FlagSet) {

		fs.StringVar(&o.Ceph.Monitors, "ceph-monitors", o.Ceph.Monitors, "Ceph Monitors to connect to.")
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
*/
//var myFlag string

func InitFlags() {
	cephOptions := CephOptions{}
	flag.StringVar(&cephOptions.Pool, "ceph-pool", "ceph11", "Ceph pool which is used to store objects.")
}
