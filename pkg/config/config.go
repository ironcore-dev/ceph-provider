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

package config

import (
	"time"
)

type Config struct {
	ControllerManager
	DisableCapacityCheck bool

	Rook RookConfig
	Pool PoolConfig
}

type ControllerManager struct {
	MetricsAddr     string
	HealthProbeAddr string
	LeaderElection  bool
}

type RookConfig struct {
	Namespace                   string
	ClusterID                   string
	ToolboxPodLabel             string
	MonEndpointConfigMapName    string
	MonEndpointConfigMapDataKey string
	CSIRBDNodeSecretName        string
	CSIRBDProvisionerSecretName string
	CSIDriverName               string
	StorageClass                CephStorageClass

	DashboardEndpoint           string
	DashboardInsecureSkipVerify bool
	DashboardUser               string
	DashboardSecretName         string
}

type CephStorageClass struct {
	AllowVolumeExpansion bool
	FSType               string
	ImageFeatures        string
	MountOptions         []string
	ReclaimPolicy        string
	VolumeBindingMode    string
}

type PoolConfig struct {
	EnableRBDStats      bool
	WatermarkPercentage int
}

type InventoryConfig struct {
	PollInterval time.Duration
	Namespace    string
	MachineClass string
}
