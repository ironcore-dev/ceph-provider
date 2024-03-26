// SPDX-FileCopyrightText: 2022 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package rook

const (
	ClusterIdDefaultValue                 = "rook-ceph"
	MonitorConfigMapNameDefaultValue      = "rook-ceph-mon-endpoints"
	MonitorConfigMapDataKeyDefaultValue   = "csi-cluster-config-json"
	StorageClassReclaimPolicyDefaultValue = "Delete"
	NamespaceDefaultValue                 = "rook-ceph"
	BucketProvisionerDefaultValue         = "rook-ceph.ceph.rook.io/bucket"
)

var (
	StorageClassMountOptionsDefaultValue = []string{"discard"}
)

type Config struct {
	ClusterId string
	Namespace string

	DashboardInsecureSkipVerify    bool
	DashboardUser                  string
	DashboardSecretName            string
	DashboardEndpoint              string
	DashboardTokenRefreshInMinutes int

	StorageClassReclaimPolicy string
	BucketProvisioner         string
}

func NewConfigWithDefaults() *Config {
	return &Config{
		ClusterId: ClusterIdDefaultValue,
		Namespace: NamespaceDefaultValue,

		StorageClassReclaimPolicy: StorageClassReclaimPolicyDefaultValue,
		BucketProvisioner:         BucketProvisionerDefaultValue,
	}
}
