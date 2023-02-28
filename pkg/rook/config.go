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
