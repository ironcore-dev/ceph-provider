// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package monitors

import (
	"context"
	"errors"
	"strconv"
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
		ns         = "rook-ceph"
		name       = "rook-ceph-mon-endpoints"
		initial    = `[{"clusterID":"rook-ceph","monitors":["10.0.0.1:6789"]}]`
		updated    = `[{"clusterID":"rook-ceph","monitors":["10.0.0.3:6789","10.0.0.1:6789"]}]`
		initialCSV = "10.0.0.1:6789"
		updatedCSV = "10.0.0.1:6789,10.0.0.3:6789"
	)

	configMap := func(raw string) *corev1.ConfigMap {
		return &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
			Data: map[string]string{
				csiClusterConfigJSONKey: raw,
			},
		}
	}

	newWatcher := func(client *fake.Clientset) *Watcher {
		return &Watcher{
			Client:    client,
			Namespace: ns,
			Name:      name,
			Key:       csiClusterConfigJSONKey,
			ClusterID: "rook-ceph",
			Log:       logr.Discard(),
			Baseline:  initialCSV,
		}
	}

	It("bootstraps a monitor CSV from the ConfigMap", func() {
		w := &Watcher{
			Client:    fake.NewSimpleClientset(configMap(`[{"clusterID":"rook-ceph","monitors":["10.0.0.2:6789","10.0.0.1:6789"]}]`)),
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

	It("returns ErrMonitorListChanged when the store already differs from baseline", func() {
		w := newWatcher(fake.NewSimpleClientset(configMap(updated)))

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		err := w.Start(ctx)
		Expect(err).To(MatchError(ContainSubstring(updatedCSV)))
		Expect(errors.Is(err, ErrMonitorListChanged)).To(BeTrue())
	})

	It("returns ErrMonitorListChanged when the parsed CSV changes", func() {
		client := fake.NewSimpleClientset(configMap(initial))
		w := newWatcher(client)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		errCh := make(chan error, 1)
		go func() {
			errCh <- w.Start(ctx)
		}()

		// The fake client does not bump metadata.resourceVersion. The informer
		// treats an unchanged resourceVersion as a resync and does not call the
		// handler. Set a new resourceVersion so the update is delivered.
		var got error
		rev := 10
		Eventually(func(g Gomega) {
			rev++
			current, err := client.CoreV1().ConfigMaps(ns).Get(context.Background(), name, metav1.GetOptions{})
			g.Expect(err).NotTo(HaveOccurred())
			current = current.DeepCopy()
			current.Data[csiClusterConfigJSONKey] = updated
			current.ResourceVersion = strconv.Itoa(rev)
			_, err = client.CoreV1().ConfigMaps(ns).Update(context.Background(), current, metav1.UpdateOptions{})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(errCh).To(Receive(&got))
		}, 5*time.Second, 200*time.Millisecond).Should(Succeed())
		Expect(got).To(MatchError(ContainSubstring(updatedCSV)))
		Expect(errors.Is(got, ErrMonitorListChanged)).To(BeTrue())
	})

	It("keeps running when an update normalizes to the baseline CSV", func() {
		const (
			baselineJSON = `[{"clusterID":"rook-ceph","monitors":["10.0.0.1:6789","10.0.0.2:6789"]}]`
			reversedJSON = `[{"clusterID":"rook-ceph","monitors":["10.0.0.2:6789","10.0.0.1:6789"]}]`
		)
		client := fake.NewSimpleClientset(configMap(baselineJSON))
		w := newWatcher(client)
		w.Baseline = "10.0.0.1:6789,10.0.0.2:6789"

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		errCh := make(chan error, 1)
		go func() {
			errCh <- w.Start(ctx)
		}()

		// The fake client does not bump metadata.resourceVersion. The informer
		// treats an unchanged resourceVersion as a resync and does not call the
		// handler. Set a new resourceVersion so the update is delivered.
		rev := 10
		Consistently(func(g Gomega) {
			rev++
			current, err := client.CoreV1().ConfigMaps(ns).Get(context.Background(), name, metav1.GetOptions{})
			g.Expect(err).NotTo(HaveOccurred())
			current = current.DeepCopy()
			current.Data[csiClusterConfigJSONKey] = reversedJSON
			current.ResourceVersion = strconv.Itoa(rev)
			_, err = client.CoreV1().ConfigMaps(ns).Update(context.Background(), current, metav1.UpdateOptions{})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(errCh).NotTo(Receive())
		}, time.Second, 200*time.Millisecond).Should(Succeed())
		cancel()
		Eventually(errCh, 5*time.Second).Should(Receive(BeNil()))
	})

	It("keeps running when the ConfigMap is deleted", func() {
		client := fake.NewSimpleClientset(configMap(initial))
		w := newWatcher(client)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		errCh := make(chan error, 1)
		go func() {
			errCh <- w.Start(ctx)
		}()

		Eventually(func() error {
			return client.CoreV1().ConfigMaps(ns).Delete(context.Background(), name, metav1.DeleteOptions{})
		}, 5*time.Second).Should(Succeed())

		Consistently(errCh, 300*time.Millisecond).ShouldNot(Receive())
		cancel()
		Eventually(errCh, 5*time.Second).Should(Receive(BeNil()))
	})

	It("keeps running when a later update cannot be parsed", func() {
		client := fake.NewSimpleClientset(configMap(initial))
		w := newWatcher(client)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		errCh := make(chan error, 1)
		go func() {
			errCh <- w.Start(ctx)
		}()

		rev := 10
		Consistently(func(g Gomega) {
			rev++
			current, err := client.CoreV1().ConfigMaps(ns).Get(context.Background(), name, metav1.GetOptions{})
			g.Expect(err).NotTo(HaveOccurred())
			current = current.DeepCopy()
			current.Data[csiClusterConfigJSONKey] = "{"
			current.ResourceVersion = strconv.Itoa(rev)
			_, err = client.CoreV1().ConfigMaps(ns).Update(context.Background(), current, metav1.UpdateOptions{})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(errCh).NotTo(Receive())
		}, time.Second, 200*time.Millisecond).Should(Succeed())
		cancel()
		Eventually(errCh, 5*time.Second).Should(Receive(BeNil()))
	})
})
