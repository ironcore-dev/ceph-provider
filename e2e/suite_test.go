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
	//"github.com/onmetal/controller-utils/configutils"
	rookv1 "github.com/rook/rook/pkg/apis/ceph.rook.io/v1"
	//metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"testing"
	"time"

	//storagev1alpha1 "github.com/onmetal/onmetal-api/api/storage/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	//"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	//. "sigs.k8s.io/controller-runtime/pkg/envtest/komega"
	//logf "sigs.k8s.io/controller-runtime/pkg/log"
	//"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var (
	cfg       *rest.Config
	k8sClient client.Client
)

var (
	cephCluster *rookv1.CephCluster
	cephPool    *rookv1.CephBlockPool
	cephClient  *rookv1.CephClient
)

const (
	eventuallyTimeout    = 3 * time.Second
	pollingInterval      = 50 * time.Millisecond
	consistentlyDuration = 1 * time.Second
)

func TestControllers(t *testing.T) {
	SetDefaultConsistentlyPollingInterval(pollingInterval)
	SetDefaultEventuallyPollingInterval(pollingInterval)
	SetDefaultEventuallyTimeout(eventuallyTimeout)
	SetDefaultConsistentlyDuration(consistentlyDuration)

	RegisterFailHandler(Fail)
	RunSpecs(t, "Controllers Suite")
}
/*
var _ = BeforeSuite(func(ctx SpecContext) {
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

})*/
