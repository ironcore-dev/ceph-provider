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
	ClusterIdDefaultValue                        = "rook-ceph"
	MonitorConfigMapNameDefaultValue             = "rook-ceph-mon-endpoints"
	MonitorConfigMapDataKeyDefaultValue          = "csi-cluster-config-json"
	CSIRBDProvisionerSecretNameDefaultValue      = "rook-csi-rbd-provisioner"
	CSIRBDNodeSecretNameDefaultValue             = "rook-csi-rbd-node"
	StorageClassAllowVolumeExpansionDefaultValue = true
	StorageClassFSTypeDefaultValue               = "ext4"
	StorageClassImageFeaturesDefaultValue        = "layering,exclusive-lock,object-map,fast-diff"
	StorageClassReclaimPolicyDefaultValue        = "Delete"
	StorageClassVolumeBindingModeDefaultValue    = "Immediate"
	CSIDriverNameDefaultValue                    = "rook-ceph.rbd.csi.ceph.com"
	NamespaceDefaultValue                        = "rook-ceph"
	EnableRBDStatsDefaultValue                   = false
	BurstFactorDefaultValue                      = 10
	BurstDurationInSecondsDefaultValue           = 15
)

var (
	StorageClassMountOptionsDefaultValue = []string{"discard"}
)

type Config struct {
	ClusterId                        string
	Namespace                        string
	StorageClassMountOptions         []string
	MonitorConfigMapName             string
	MonitorConfigMapDataKey          string
	CSIRBDProvisionerSecretName      string
	CSIRBDNodeSecretName             string
	StorageClassAllowVolumeExpansion bool
	StorageClassFSType               string
	StorageClassImageFeatures        string
	StorageClassReclaimPolicy        string
	StorageClassVolumeBindingMode    string
	CSIDriverName                    string
	EnableRBDStats                   bool

	DashboardInsecureSkipVerify    bool
	DashboardUser                  string
	DashboardSecretName            string
	DashboardEndpoint              string
	DashboardTokenRefreshInMinutes int

	BurstFactor            int64
	BurstDurationInSeconds int64
}

func NewConfigWithDefaults() *Config {
	return &Config{
		ClusterId:                        ClusterIdDefaultValue,
		Namespace:                        NamespaceDefaultValue,
		StorageClassMountOptions:         StorageClassMountOptionsDefaultValue,
		MonitorConfigMapName:             MonitorConfigMapNameDefaultValue,
		MonitorConfigMapDataKey:          MonitorConfigMapDataKeyDefaultValue,
		CSIRBDProvisionerSecretName:      CSIRBDProvisionerSecretNameDefaultValue,
		CSIRBDNodeSecretName:             CSIRBDNodeSecretNameDefaultValue,
		StorageClassAllowVolumeExpansion: StorageClassAllowVolumeExpansionDefaultValue,
		StorageClassFSType:               StorageClassFSTypeDefaultValue,
		StorageClassImageFeatures:        StorageClassImageFeaturesDefaultValue,
		StorageClassReclaimPolicy:        StorageClassReclaimPolicyDefaultValue,
		StorageClassVolumeBindingMode:    StorageClassVolumeBindingModeDefaultValue,
		CSIDriverName:                    CSIDriverNameDefaultValue,
		EnableRBDStats:                   EnableRBDStatsDefaultValue,
		BurstFactor:                      BurstFactorDefaultValue,
		BurstDurationInSeconds:           BurstDurationInSecondsDefaultValue,
	}
}
