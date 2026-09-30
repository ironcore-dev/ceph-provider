// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package monitors

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ParseCSIClusterConfigJSON", func() {
	DescribeTable("parses CSI cluster config JSON",
		func(data, clusterID string, want []string, wantErr bool) {
			got, err := ParseCSIClusterConfigJSON([]byte(data), clusterID)
			if wantErr {
				Expect(err).To(HaveOccurred())
				return
			}
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal(want))
		},
		Entry("CSI IPv4",
			`[{"clusterID":"rook-ceph","monitors":["10.0.0.1:6789","10.0.0.2:6789"]}]`,
			"rook-ceph",
			[]string{"10.0.0.1:6789", "10.0.0.2:6789"},
			false,
		),
		Entry("CSI clusterID select",
			`[{"clusterID":"other","monitors":["10.0.0.9:6789"]},{"clusterID":"rook-ceph","monitors":["10.0.0.1:6789"]}]`,
			"rook-ceph",
			[]string{"10.0.0.1:6789"},
			false,
		),
		Entry("CSI empty clusterID",
			`[{"clusterID":"rook-ceph","monitors":["10.0.0.2:6789","10.0.0.1:6789"]}]`,
			"",
			[]string{"10.0.0.1:6789", "10.0.0.2:6789"},
			false,
		),
		Entry("CSI first entry empty monitors",
			`[{"clusterID":"empty","monitors":[]},{"clusterID":"rook-ceph","monitors":["10.0.0.1:6789"]}]`,
			"",
			[]string{"10.0.0.1:6789"},
			false,
		),
		Entry("CSI missing cluster",
			`[{"clusterID":"rook-ceph","monitors":["10.0.0.1:6789"]}]`,
			"unknown",
			nil,
			true,
		),
		Entry("CSI IPv6",
			`[{"clusterID":"rook-ceph","monitors":["[fd00::1]:6789"]}]`,
			"rook-ceph",
			[]string{"[fd00::1]:6789"},
			false,
		),
		Entry("empty JSON array", `[]`, "", nil, true),
		Entry("invalid JSON", `{`, "", nil, true),
	)
})

var _ = Describe("ParseRookDataCSV", func() {
	DescribeTable("parses Rook data CSV",
		func(raw string, want []string, errSub string) {
			got, err := ParseRookDataCSV(raw)
			if errSub != "" {
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(errSub))
				return
			}
			if want == nil {
				Expect(err).To(HaveOccurred())
				return
			}
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal(want))
		},
		Entry("Rook data", "a=10.0.0.2:6789,b=10.0.0.1:6789", []string{"10.0.0.1:6789", "10.0.0.2:6789"}, ""),
		Entry("Rook data nonce", "a=10.0.0.1:6789/0", []string{"10.0.0.1:6789"}, ""),
		Entry("Rook msgr2", "a=[v2:10.0.0.1:3300,v1:10.0.0.1:6789]", nil, "msgr2 vector not supported; use csi-cluster-config-json"),
		Entry("Dedupe", "a=10.0.0.1:6789,b=10.0.0.1:6789", []string{"10.0.0.1:6789"}, ""),
		Entry("Empty", "", nil, ""),
	)
})

var _ = Describe("ParseConfigMapData", func() {
	csiJSON := `[{"clusterID":"rook-ceph","monitors":["10.0.0.2:6789","10.0.0.1:6789"]}]`

	DescribeTable("reads monitor endpoints from ConfigMap data",
		func(data map[string]string, key, clusterID string, want []string, wantErr bool) {
			got, err := ParseConfigMapData(data, key, clusterID)
			if wantErr {
				Expect(err).To(HaveOccurred())
				return
			}
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal(want))
		},
		Entry("ConfigMap JSON key",
			map[string]string{csiClusterConfigJSONKey: csiJSON},
			csiClusterConfigJSONKey,
			"rook-ceph",
			[]string{"10.0.0.1:6789", "10.0.0.2:6789"},
			false,
		),
		Entry("ConfigMap fallback",
			map[string]string{"data": "a=10.0.0.2:6789,b=10.0.0.1:6789"},
			csiClusterConfigJSONKey,
			"",
			[]string{"10.0.0.1:6789", "10.0.0.2:6789"},
			false,
		),
		Entry("empty map",
			map[string]string{},
			csiClusterConfigJSONKey,
			"",
			nil,
			true,
		),
		Entry("JSON in data key",
			map[string]string{"data": csiJSON},
			"missing",
			"rook-ceph",
			[]string{"10.0.0.1:6789", "10.0.0.2:6789"},
			false,
		),
	)
})

var _ = Describe("NormalizeEndpoints", func() {
	It("strips an IPv6 nonce suffix", func() {
		got, err := NormalizeEndpoints([]string{"[fd00::1]:6789/0"})
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal([]string{"[fd00::1]:6789"}))
	})
})
