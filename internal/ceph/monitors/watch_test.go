// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package monitors

import (
	"context"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"
)

var _ = Describe("mounted ConfigMap", func() {
	var dir string

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
	})

	It("reads csi-cluster-config-json", func() {
		writeKey(dir, csiClusterConfigJSONKey, `[{"clusterID":"rook-ceph","monitors":["10.0.0.2:6789","10.0.0.1:6789"]}]`)
		w := &Watcher{Dir: dir, Key: csiClusterConfigJSONKey, ClusterID: "rook-ceph"}
		got, err := w.Bootstrap(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal("10.0.0.1:6789,10.0.0.2:6789"))
	})

	It("falls back to the data file when the preferred key is missing", func() {
		writeKey(dir, rookDataKey, "c=10.0.0.1:6789")
		w := &Watcher{Dir: dir, Key: csiClusterConfigJSONKey, ClusterID: "rook-ceph"}
		got, err := w.Bootstrap(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal("10.0.0.1:6789"))
	})

	It("does not fall back when the preferred key is present and invalid", func() {
		writeKey(dir, csiClusterConfigJSONKey, "{")
		writeKey(dir, rookDataKey, "c=10.0.0.1:6789")
		w := &Watcher{Dir: dir, Key: csiClusterConfigJSONKey, ClusterID: "rook-ceph"}
		_, err := w.Bootstrap(context.Background())
		Expect(err).To(HaveOccurred())
	})

	It("exits when a mounted address is added", func() {
		writeKey(dir, csiClusterConfigJSONKey, `[{"clusterID":"rook-ceph","monitors":["10.0.0.1:6789"]}]`)
		w := &Watcher{Dir: dir, Key: csiClusterConfigJSONKey, ClusterID: "rook-ceph", Baseline: "10.0.0.1:6789", added: make(chan struct{})}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		errCh := startWatcher(ctx, w)
		Eventually(w.added).Should(BeClosed())

		writeKey(dir, csiClusterConfigJSONKey, `[{"clusterID":"rook-ceph","monitors":["10.0.0.1:6789","10.0.0.2:6789"]}]`)
		expectWatch(errCh, MatchError(ContainSubstring("ceph monitor list changed")))
	})

	It("stays up when the same addresses are reordered", func() {
		writeKey(dir, csiClusterConfigJSONKey, `[{"clusterID":"rook-ceph","monitors":["10.0.0.1:6789","10.0.0.2:6789"]}]`)
		w := &Watcher{
			Dir:       dir,
			Key:       csiClusterConfigJSONKey,
			ClusterID: "rook-ceph",
			Baseline:  "10.0.0.1:6789,10.0.0.2:6789",
			added:     make(chan struct{}),
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		errCh := startWatcher(ctx, w)
		Eventually(w.added).Should(BeClosed())

		writeKey(dir, csiClusterConfigJSONKey, `[{"clusterID":"rook-ceph","monitors":["10.0.0.2:6789","10.0.0.1:6789"]}]`)
		Consistently(errCh, time.Second, 100*time.Millisecond).ShouldNot(Receive())
		cancel()
		expectWatch(errCh, BeNil())
	})

	It("stays up when a later write does not parse", func() {
		writeKey(dir, csiClusterConfigJSONKey, `[{"clusterID":"rook-ceph","monitors":["10.0.0.1:6789"]}]`)
		w := &Watcher{Dir: dir, Key: csiClusterConfigJSONKey, ClusterID: "rook-ceph", Baseline: "10.0.0.1:6789", added: make(chan struct{})}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		errCh := startWatcher(ctx, w)
		Eventually(w.added).Should(BeClosed())

		writeKey(dir, csiClusterConfigJSONKey, "{")
		Consistently(errCh, time.Second, 100*time.Millisecond).ShouldNot(Receive())
		cancel()
		expectWatch(errCh, BeNil())
	})
})

func writeKey(dir, name, content string) {
	Expect(os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644)).To(Succeed())
}

func startWatcher(ctx context.Context, w *Watcher) <-chan error {
	errCh := make(chan error, 1)
	go func() {
		errCh <- w.Start(ctx)
	}()
	return errCh
}

func expectWatch(errCh <-chan error, matcher types.GomegaMatcher) {
	var err error
	Eventually(errCh, 2*time.Second, 50*time.Millisecond).Should(Receive(&err))
	Expect(err).To(matcher)
}
