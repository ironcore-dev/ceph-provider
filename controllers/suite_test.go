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

package controllers

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/onmetal/cephlet/pkg/rook"
	"github.com/onmetal/controller-utils/buildutils"
	"github.com/onmetal/controller-utils/modutils"
	storagev1alpha1 "github.com/onmetal/onmetal-api/apis/storage/v1alpha1"
	"github.com/onmetal/onmetal-api/envtestutils"
	"github.com/onmetal/onmetal-api/envtestutils/apiserver"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	rookv1 "github.com/rook/rook/pkg/apis/ceph.rook.io/v1"
	"go.uber.org/zap/zapcore"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
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

	volumePoolName        = "my-pool"
	volumePoolProviderID  = "custom://pool"
	volumePoolReplication = 3
)

var (
	ctx        = context.Background()
	testEnv    *envtest.Environment
	testEnvExt *envtestutils.EnvironmentExtensions
	cfg        *rest.Config
	k8sClient  client.Client

	volumeClassSelector = map[string]string{
		"suitable-for": "testing",
	}
	volumePoolLabels = map[string]string{
		"some": "label",
	}
	volumePoolAnnotations = map[string]string{
		"some": "annotation",
	}
)

func TestAPIs(t *testing.T) {
	_, reporterConfig := GinkgoConfiguration()
	reporterConfig.SlowSpecThreshold = slowSpecThreshold
	SetDefaultConsistentlyPollingInterval(pollingInterval)
	SetDefaultEventuallyPollingInterval(pollingInterval)
	SetDefaultEventuallyTimeout(eventuallyTimeout)
	SetDefaultConsistentlyDuration(consistentlyDuration)

	RegisterFailHandler(Fail)
	RunSpecs(t, "Cephlet Controller Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true), zap.Level(zapcore.InfoLevel)))

	var err error

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			modutils.Dir("github.com/rook/rook", "deploy", "examples", "crds.yaml"),
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

	// Init package-level k8sClient
	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	apiSrv, err := apiserver.New(cfg, apiserver.Options{
		MainPath:     "github.com/onmetal/onmetal-api/cmd/apiserver",
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
})

func SetupTest(ctx context.Context) (*corev1.Namespace, *corev1.Namespace) {
	var (
		cancel context.CancelFunc
	)
	testNamespace := &corev1.Namespace{}
	rookNamespace := &corev1.Namespace{}
	BeforeEach(func() {
		var mgrCtx context.Context
		mgrCtx, cancel = context.WithCancel(ctx)
		*testNamespace = corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "testns-",
			},
		}
		err := k8sClient.Create(ctx, testNamespace)
		Expect(err).To(Succeed(), "failed to create test namespace")

		*rookNamespace = corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "rookns-",
			},
		}
		Expect(k8sClient.Create(ctx, rookNamespace)).To(Succeed(), "failed to create test namespace")

		rookConfigMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      rook.MonitorConfigMapNameDefaultValue,
				Namespace: rookNamespace.Name,
			},
			Data: map[string]string{
				rook.MonitorConfigMapDataKeyDefaultValue: fmt.Sprintf("[{\"clusterID\":\"%s\",\"monitors\":[\"100.64.10.121:6789\"],\"namespace\":\"\"}]", rook.ClusterIdDefaultValue),
			},
		}
		Expect(k8sClient.Create(ctx, rookConfigMap)).To(Succeed(), "failed to create rook configmap")

		k8sManager, err := ctrl.NewManager(cfg, ctrl.Options{
			Scheme:             scheme.Scheme,
			Host:               "127.0.0.1",
			MetricsBindAddress: "0",
		})
		Expect(err).ToNot(HaveOccurred())

		// register reconciler here

		rookConfig := rook.NewConfigWithDefaults()
		rookConfig.Namespace = rookNamespace.Name

		Expect((&VolumeReconciler{
			Client:         k8sManager.GetClient(),
			Scheme:         k8sManager.GetScheme(),
			VolumePoolName: volumePoolName,
			RookConfig:     rookConfig,
		}).SetupWithManager(k8sManager)).To(Succeed())

		Expect((&VolumePoolReconciler{
			Client:                k8sManager.GetClient(),
			Scheme:                k8sManager.GetScheme(),
			VolumePoolName:        volumePoolName,
			VolumePoolProviderID:  volumePoolProviderID,
			VolumePoolLabels:      volumePoolLabels,
			VolumePoolAnnotations: volumePoolAnnotations,
			VolumeClassSelector:   volumeClassSelector,
			VolumePoolReplication: volumePoolReplication,
			RookConfig:            rookConfig,
		}).SetupWithManager(k8sManager)).To(Succeed())

		go func() {
			Expect(k8sManager.Start(mgrCtx)).To(Succeed(), "failed to start manager")
		}()
	})

	AfterEach(func() {
		cancel()
		Expect(k8sClient.Delete(ctx, testNamespace)).To(Succeed(), "failed to delete test namespace")
		Expect(k8sClient.Delete(ctx, rookNamespace)).To(Succeed(), "failed to delete rook namespace")
	})

	return testNamespace, rookNamespace
}
