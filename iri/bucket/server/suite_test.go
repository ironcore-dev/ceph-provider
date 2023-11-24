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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	bucketv1alpha1 "github.com/kube-object-storage/lib-bucket-provisioner/pkg/apis/objectbucket.io/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	rookv1 "github.com/rook/rook/pkg/apis/ceph.rook.io/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/ironcore-dev/ceph-provider/iri/bucket/cmd/bucket/app"
	iriv1alpha1 "github.com/ironcore-dev/ironcore/iri/apis/bucket/v1alpha1"
	"github.com/ironcore-dev/ironcore/iri/remote/bucket"
	"github.com/onmetal/controller-utils/modutils"
	//+kubebuilder:scaffold:imports
)

const (
	pollingInterval      = 250 * time.Millisecond
	eventuallyTimeout    = 20 * time.Second
	consistentlyDuration = 1 * time.Second
	bucketBaseURL        = "example.com"
)

var (
	bucketClient  iriv1alpha1.BucketRuntimeClient
	testEnv       *envtest.Environment
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

var _ = BeforeSuite(func() {
	logf.SetLogger(GinkgoLogr)

	var err error

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join(modutils.Dir("github.com/rook/rook", "deploy", "examples", "crds.yaml")),
		},
		ErrorIfCRDPathMissing: true,
	}

	bucketClasses := []iriv1alpha1.BucketClass{
		{
			Name: "foo",
			Capabilities: &iriv1alpha1.BucketClassCapabilities{
				Tps:  1,
				Iops: 100,
			},
		},
		{
			Name: "bar",
			Capabilities: &iriv1alpha1.BucketClassCapabilities{
				Tps:  2,
				Iops: 200,
			},
		}}

	bucketClassesData, err := json.Marshal(bucketClasses)
	Expect(err).NotTo(HaveOccurred())

	bucketClassesFile, err := os.CreateTemp(GinkgoT().TempDir(), "bucketClasses")
	Expect(err).NotTo(HaveOccurred())
	Expect(os.WriteFile(bucketClassesFile.Name(), bucketClassesData, 0666)).To(Succeed())
	DeferCleanup(bucketClassesFile.Close)

	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	DeferCleanup(testEnv.Stop)

	Expect(rookv1.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(bucketv1alpha1.AddToScheme(scheme.Scheme)).To(Succeed())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	komega.SetClient(k8sClient)

	ctx, cancel := context.WithCancel(context.Background())
	DeferCleanup(cancel)

	By("creating the rook namespace")
	rookNamespace = &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "test-ns-",
		},
	}
	Expect(k8sClient.Create(ctx, rookNamespace)).To(Succeed(), "failed to create rook namespace")
	DeferCleanup(k8sClient.Delete, rookNamespace)

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
		Address:                    fmt.Sprintf("%s/ceph-bucket-provider.sock", os.Getenv("PWD")),
		Kubeconfig:                 kubeConfigFile.Name(),
		Namespace:                  rookNamespace.Name,
		BucketEndpoint:             bucketBaseURL,
		BucketPoolStorageClassName: "foo",
		PathSupportedBucketClasses: bucketClassesFile.Name(),
	}

	go func() {
		defer GinkgoRecover()
		Expect(app.Run(ctx, opts)).To(Succeed())
	}()

	Eventually(func() (bool, error) {
		return isSocketAvailable(opts.Address)
	}, "30s", "500ms").Should(BeTrue(), "The UNIX socket file should be available")

	address, err := bucket.GetAddressWithTimeout(3*time.Second, fmt.Sprintf("unix://%s", opts.Address))
	Expect(err).NotTo(HaveOccurred())

	gconn, err := grpc.Dial(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	Expect(err).NotTo(HaveOccurred())

	bucketClient = iriv1alpha1.NewBucketRuntimeClient(gconn)
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
