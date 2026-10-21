// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package integration

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
	tclog "github.com/testcontainers/testcontainers-go/log"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	// cephTestImage is a single-node Ceph cluster, tagged by Ceph version: https://github.com/arttor/ceph-test
	cephTestImage = "ghcr.io/arttor/ceph-test"
	cephTestPool  = "devpool"
	// cephTestDiskSize is the size of the cluster's only OSD, a sparse file.
	cephTestDiskSize = 10 << 30
)

// startCephTestCluster starts a ceph-test container and points the suite's Ceph settings at it.
func startCephTestCluster(ctx context.Context) {
	version := os.Getenv("CEPH_TEST_CLUSTER_VERSION")
	Expect(version).NotTo(BeEmpty(), "set CEPH_TEST_CLUSTER_VERSION to a ceph-test tag, or run the tests with make")

	By(fmt.Sprintf("starting Ceph cluster %s:%s", cephTestImage, version))
	tclog.SetDefault(log.New(GinkgoWriter, "", log.LstdFlags))
	container, err := testcontainers.Run(ctx, cephTestImage+":"+version,
		testcontainers.WithEnv(map[string]string{
			"CEPH_RBD_POOL":   cephTestPool,
			"CEPH_RGW":        "false",
			"CEPH_EXTRA_CONF": fmt.Sprintf("bluestore_block_size = %d", cephTestDiskSize),
		}),
		testcontainers.WithWaitStrategy(
			wait.ForExec([]string{"ceph-test-health", "rbd"}).WithStartupTimeout(time.Minute),
		),
	)
	DeferCleanup(func() error {
		return testcontainers.TerminateContainer(container, testcontainers.StopTimeout(0))
	})
	Expect(err).NotTo(HaveOccurred())

	confDir := GinkgoT().TempDir()
	keyringFile := filepath.Join(confDir, "ceph.client.admin.keyring")
	exitCode, keyring, err := container.Exec(ctx, []string{"cat", "/etc/ceph/ceph.client.admin.keyring"}, tcexec.Multiplexed())
	Expect(err).NotTo(HaveOccurred())
	keyringData, err := io.ReadAll(keyring)
	Expect(err).NotTo(HaveOccurred())
	Expect(exitCode).To(BeZero(), string(keyringData))
	Expect(os.WriteFile(keyringFile, keyringData, 0600)).To(Succeed())

	ip, err := container.ContainerIP(ctx)
	Expect(err).NotTo(HaveOccurred())
	monitor := fmt.Sprintf("%s:3300", ip)
	configFile := filepath.Join(confDir, "ceph.conf")
	config := fmt.Sprintf("[global]\nmon host = v2:%s/0\nkeyring = %s\n", monitor, keyringFile)
	Expect(os.WriteFile(configFile, []byte(config), 0600)).To(Succeed())

	cephMonitors = monitor
	cephUsername = "admin"
	cephClientname = "client.admin"
	cephKeyringFilename = keyringFile
	cephConfigFile = configFile
	cephPoolname = cephTestPool
	cephDiskSize = strconv.Itoa(cephTestDiskSize)
}
