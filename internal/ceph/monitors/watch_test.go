// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package monitors

import (
	"context"
	"time"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

var _ = Describe("Watcher", func() {
	const (
		ns   = "rook-ceph"
		name = "rook-ceph-mon-endpoints"
	)

	It("bootstraps a monitor CSV from the ConfigMap", func() {
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
			Data: map[string]string{
				csiClusterConfigJSONKey: `[{"clusterID":"rook-ceph","monitors":["10.0.0.2:6789","10.0.0.1:6789"]}]`,
			},
		}
		w := &Watcher{
			Client:    fake.NewSimpleClientset(cm),
			Namespace: ns,
			Name:      name,
			Key:       csiClusterConfigJSONKey,
			ClusterID: "rook-ceph",
			Log:       logr.Discard(),
		}

		got, err := w.Bootstrap(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal("10.0.0.1:6789,10.0.0.2:6789"))
	})

	It("wraps a missing ConfigMap on bootstrap", func() {
		w := &Watcher{
			Client:    fake.NewSimpleClientset(),
			Namespace: ns,
			Name:      name,
			Key:       csiClusterConfigJSONKey,
			Log:       logr.Discard(),
		}
		_, err := w.Bootstrap(context.Background())
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("get configmap rook-ceph/rook-ceph-mon-endpoints"))
	})

	It("calls OnChange when the ConfigMap is updated", func() {
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
			Data: map[string]string{
				csiClusterConfigJSONKey: `[{"clusterID":"rook-ceph","monitors":["10.0.0.1:6789"]}]`,
			},
		}
		client := fake.NewClientset(cm)
		got := make(chan string, 8)
		w := &Watcher{
			Client:    client,
			Namespace: ns,
			Name:      name,
			Key:       csiClusterConfigJSONKey,
			ClusterID: "rook-ceph",
			Log:       logr.Discard(),
			OnChange: func(_ context.Context, csv string) {
				got <- csv
			},
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		errCh := make(chan error, 1)
		go func() {
			errCh <- w.Start(ctx)
		}()

		Eventually(got, 5*time.Second).Should(Receive(Equal("10.0.0.1:6789")))

		updated := cm.DeepCopy()
		updated.Data[csiClusterConfigJSONKey] = `[{"clusterID":"rook-ceph","monitors":["10.0.0.3:6789","10.0.0.1:6789"]}]`
		_, err := client.CoreV1().ConfigMaps(ns).Update(ctx, updated, metav1.UpdateOptions{})
		Expect(err).NotTo(HaveOccurred())

		Eventually(got, 5*time.Second).Should(Receive(Equal("10.0.0.1:6789,10.0.0.3:6789")))

		cancel()
		Eventually(errCh, 5*time.Second).Should(Receive(BeNil()))
	})
})
