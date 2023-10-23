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

package server_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	corev1alpha1 "github.com/onmetal/onmetal-api/api/core/v1alpha1"
	"github.com/onmetal/onmetal-api/utils/envtest/apiserver"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	bucketv1alpha1 "github.com/kube-object-storage/lib-bucket-provisioner/pkg/apis/objectbucket.io/v1alpha1"
	"github.com/onmetal/cephlet/ori/bucket/cmd/bucket/app"
	"github.com/onmetal/controller-utils/buildutils"
	"github.com/onmetal/controller-utils/modutils"
	storagev1alpha1 "github.com/onmetal/onmetal-api/api/storage/v1alpha1"
	"github.com/onmetal/onmetal-api/ori/apis/bucket/v1alpha1"
	"github.com/onmetal/onmetal-api/ori/remote/bucket"
	envtestutils "github.com/onmetal/onmetal-api/utils/envtest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	rookv1 "github.com/rook/rook/pkg/apis/ceph.rook.io/v1"
	"go.uber.org/zap/zapcore"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	. "sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	//+kubebuilder:scaffold:imports
)

const (
	eventuallyTimeout    = 5 * time.Second
	pollingInterval      = 250 * time.Millisecond
	consistentlyDuration = 1 * time.Second
	apiServiceTimeout    = 5 * time.Minute
	bucketBaseURL        = "example.com"
)

var (
	srvCtx context.Context
	cancel context.CancelFunc

	bucketClient v1alpha1.BucketRuntimeClient

	testEnv       *envtest.Environment
	testEnvExt    *envtestutils.EnvironmentExtensions
	cfg           *rest.Config
	k8sClient     client.Client
	rookNamespace *corev1.Namespace
)

func TestAPIs(t *testing.T) {
	SetDefaultConsistentlyPollingInterval(pollingInterval)
	SetDefaultEventuallyPollingInterval(pollingInterval)
	SetDefaultEventuallyTimeout(eventuallyTimeout)
	SetDefaultConsistentlyDuration(consistentlyDuration)

	RegisterFailHandler(Fail)
	RunSpecs(t, "Bucket GRPC Server Suite")
}

var _ = BeforeSuite(func(ctx SpecContext) {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true), zap.Level(zapcore.InfoLevel)))

	var err error

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join(modutils.Dir("github.com/rook/rook", "deploy", "examples", "crds.yaml")),
		},
		ErrorIfCRDPathMissing: true,
	}

	testEnvExt = &envtestutils.EnvironmentExtensions{
		APIServiceDirectoryPaths: []string{
			modutils.Dir("github.com/onmetal/onmetal-api", "config", "apiserver", "apiservice", "bases"),
		},
		ErrorIfAPIServicePathIsMissing: true,
	}

	cfg, err = envtestutils.StartWithExtensions(testEnv, testEnvExt)
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	DeferCleanup(envtestutils.StopWithExtensions, testEnv, testEnvExt)

	Expect(rookv1.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(storagev1alpha1.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(bucketv1alpha1.AddToScheme(scheme.Scheme)).To(Succeed())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())
	SetClient(k8sClient)

	apiSrv, err := apiserver.New(cfg, apiserver.Options{
		MainPath:     "github.com/onmetal/onmetal-api/cmd/onmetal-apiserver",
		BuildOptions: []buildutils.BuildOption{buildutils.ModModeMod},
		ETCDServers:  []string{testEnv.ControlPlane.Etcd.URL.String()},
		Host:         testEnvExt.APIServiceInstallOptions.LocalServingHost,
		Port:         testEnvExt.APIServiceInstallOptions.LocalServingPort,
		CertDir:      testEnvExt.APIServiceInstallOptions.LocalServingCertDir,
	})
	Expect(err).NotTo(HaveOccurred())

	By("starting the onmetal-api aggregated api server")
	Expect(apiSrv.Start()).To(Succeed())
	DeferCleanup(apiSrv.Stop)

	Expect(envtestutils.WaitUntilAPIServicesReadyWithTimeout(apiServiceTimeout, testEnvExt, k8sClient, scheme.Scheme)).To(Succeed())

	By("creating the rook namespace")
	rookNamespace = &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "test-ns-",
		},
	}
	Expect(k8sClient.Create(ctx, rookNamespace)).To(Succeed(), "failed to create rook namespace")
	DeferCleanup(k8sClient.Delete, rookNamespace)

	By("creating a bucket class")
	bucketClass := &storagev1alpha1.BucketClass{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name: "foo",
		},
		Capabilities: corev1alpha1.ResourceList{
			corev1alpha1.ResourceIOPS: resource.MustParse("100"),
			corev1alpha1.ResourceTPS:  resource.MustParse("1"),
		},
	}
	Expect(k8sClient.Create(ctx, bucketClass)).To(Succeed())
	DeferCleanup(k8sClient.Delete, bucketClass)

	By("starting the app")
	user, err := testEnv.AddUser(envtest.User{
		Name:   "dummy",
		Groups: []string{"system:authenticated", "system:masters"},
	}, cfg)
	Expect(err).NotTo(HaveOccurred())

	kubeconfig, err := user.KubeConfig()
	Expect(err).NotTo(HaveOccurred())

	kubeConfigFile, err := os.CreateTemp(GinkgoT().TempDir(), "kubeconfig")
	Expect(err).NotTo(HaveOccurred())
	defer os.Remove(kubeConfigFile.Name())

	Expect(os.WriteFile(kubeConfigFile.Name(), kubeconfig, 0600)).To(Succeed())

	opts := app.Options{
		Address:                    fmt.Sprintf("%s/cephlet-bucket.sock", os.Getenv("PWD")),
		Kubeconfig:                 kubeConfigFile.Name(),
		Namespace:                  rookNamespace.Name,
		BucketEndpoint:             bucketBaseURL,
		BucketPoolStorageClassName: "foo",
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

	address, err := bucket.GetAddressWithTimeout(3*time.Second, fmt.Sprintf("unix://%s", opts.Address))
	Expect(err).NotTo(HaveOccurred())

	gconn, err := grpc.Dial(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	Expect(err).NotTo(HaveOccurred())

	bucketClient = v1alpha1.NewBucketRuntimeClient(gconn)
	DeferCleanup(gconn.Close)
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
