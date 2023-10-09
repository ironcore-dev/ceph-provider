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

package server_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	corev1alpha1 "github.com/onmetal/onmetal-api/api/core/v1alpha1"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	bucketv1alpha1 "github.com/kube-object-storage/lib-bucket-provisioner/pkg/apis/objectbucket.io/v1alpha1"
	"github.com/onmetal/cephlet/ori/bucket/cmd/bucket/app"
	"github.com/onmetal/cephlet/pkg/rook"
	"github.com/onmetal/controller-utils/buildutils"
	"github.com/onmetal/controller-utils/modutils"
	storagev1alpha1 "github.com/onmetal/onmetal-api/api/storage/v1alpha1"
	"github.com/onmetal/onmetal-api/ori/apis/bucket/v1alpha1"
	"github.com/onmetal/onmetal-api/ori/remote/bucket"
	envtestutils "github.com/onmetal/onmetal-api/utils/envtest"
	"github.com/onmetal/onmetal-api/utils/envtest/apiserver"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	rookv1 "github.com/rook/rook/pkg/apis/ceph.rook.io/v1"
	"go.uber.org/zap/zapcore"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	. "sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	//+kubebuilder:scaffold:imports
)

const (
	slowSpecThreshold    = 20 * time.Second
	eventuallyTimeout    = 20 * time.Second
	pollingInterval      = 250 * time.Millisecond
	consistentlyDuration = 1 * time.Second
	apiServiceTimeout    = 5 * time.Minute

	bucketBaseURL = "example.com"
)

var (
	srvCtx context.Context
	cancel context.CancelFunc

	bucketClient v1alpha1.BucketRuntimeClient

	testEnv    *envtest.Environment
	testEnvExt *envtestutils.EnvironmentExtensions
	cfg        *rest.Config
	k8sClient  client.Client
	rookConfig *rook.Config
)

func TestAPIs(t *testing.T) {
	SetDefaultConsistentlyPollingInterval(pollingInterval)
	SetDefaultEventuallyPollingInterval(pollingInterval)
	SetDefaultEventuallyTimeout(eventuallyTimeout)
	SetDefaultConsistentlyDuration(consistentlyDuration)

	RegisterFailHandler(Fail)
	RunSpecs(t, "Bucket GRPC Server Suite")
}

var _ = BeforeSuite(func() {
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

	By("starting the app")
	kubeconfig := createKubeconfigFileFromrRestConfig(*cfg)
	opts := app.Options{
		Address:                    fmt.Sprintf("%s/cephlet-bucket.sock", os.Getenv("PWD")),
		Kubeconfig:                 kubeconfig,
		Namespace:                  "rook-namespace",
		BucketPoolStorageClassName: "test-bucket-pool-storage-class",
		PathSupportedBucketClasses: "",
		BucketClassSelector:        map[string]string{"test-bucket": "test-bucket-class"},
		BucketEndpoint:             bucketBaseURL,
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

func SetupTest() (*corev1.Namespace, *corev1.Namespace) {
	var (
		bucketClass *storagev1alpha1.BucketClass
	)

	testNamespace := &corev1.Namespace{}
	rookNamespace := &corev1.Namespace{}

	BeforeEach(func(ctx SpecContext) {
		*testNamespace = corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-namespace",
			},
		}
		Expect(k8sClient.Create(ctx, testNamespace)).To(Succeed(), "failed to create test namespace")

		*rookNamespace = corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: "rook-namespace",
			},
		}
		Expect(k8sClient.Create(ctx, rookNamespace)).To(Succeed(), "failed to create rook namespace")

		rookConfigMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      rook.MonitorConfigMapNameDefaultValue,
				Namespace: rookNamespace.Name,
			},
			Data: map[string]string{
				rook.MonitorConfigMapDataKeyDefaultValue: fmt.Sprintf("[{\"clusterID\":\"%s\",\"monitors\":[\"10.0.0.1:6789\"],\"namespace\":\"\"}]", rook.ClusterIdDefaultValue),
			},
		}
		Expect(k8sClient.Create(ctx, rookConfigMap)).To(Succeed(), "failed to create rook configmap")

		rookConfig = rook.NewConfigWithDefaults()
		rookConfig.Namespace = rookNamespace.Name

		bucketClass = &storagev1alpha1.BucketClass{
			TypeMeta: metav1.TypeMeta{},
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "sc-",
			},
			Capabilities: corev1alpha1.ResourceList{
				corev1alpha1.ResourceIOPS: resource.MustParse("100"),
				corev1alpha1.ResourceTPS:  resource.MustParse("1"),
			},
		}
		Expect(k8sClient.Create(ctx, bucketClass)).To(Succeed())
	})

	AfterEach(func(ctx SpecContext) {
		Expect(k8sClient.Delete(ctx, testNamespace)).To(Succeed(), "failed to delete test namespace")
		Expect(k8sClient.Delete(ctx, rookNamespace)).To(Succeed(), "failed to delete rook namespace")
	})

	return testNamespace, rookNamespace
}

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

func createKubeconfigFileFromrRestConfig(restConfig rest.Config) string {
	clusters := make(map[string]*clientcmdapi.Cluster)
	clusters["default-cluster"] = &clientcmdapi.Cluster{
		Server:                   restConfig.Host,
		CertificateAuthorityData: restConfig.CAData,
	}
	contexts := make(map[string]*clientcmdapi.Context)
	contexts["default-context"] = &clientcmdapi.Context{
		Cluster:  "default-cluster",
		AuthInfo: "default-user",
	}
	authinfos := make(map[string]*clientcmdapi.AuthInfo)
	authinfos["default-user"] = &clientcmdapi.AuthInfo{
		ClientCertificateData: restConfig.CertData,
		ClientKeyData:         restConfig.KeyData,
	}
	clientConfig := clientcmdapi.Config{
		Kind:           "Config",
		APIVersion:     "v1",
		Clusters:       clusters,
		Contexts:       contexts,
		CurrentContext: "default-context",
		AuthInfos:      authinfos,
	}
	kubeConfigFile, _ := os.CreateTemp("", "kubeconfig")
	_ = clientcmd.WriteToFile(clientConfig, kubeConfigFile.Name())
	return kubeConfigFile.Name()
}
