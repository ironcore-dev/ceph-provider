package rook

const (
	ClusterIdDefaultValue                        = "rook-ceph"
	MonitorConfigMapNameDefaultValue             = "rook-ceph-mon-endpoints"
	MonitorConfigMapDataKeyDefaultValue          = "csi-cluster-config-json"
	CSIRBDProvisionerSecretNameDefaultValue      = "rook-csi-rbd-provisioner"
	CSIRBDNodeSecretNameDefaultValue             = "rook-csi-rbd-node"
	StorageClassAllowVolumeExpansionDefaultValue = true
	StorageClassFSTypeDefaultValue               = "ext4"
	StorageClassImageFeaturesDefaultValue        = "layering"
	StorageClassReclaimPolicyDefaultValue        = "Delete"
	StorageClassVolumeBindingModeDefaultValue    = "Immediate"
	CSIDriverNameDefaultValue                    = "rook-ceph.rbd.csi.ceph.com"
	NamespaceDefaultValue                        = "rook-ceph"
	EnableRBDStatsDefaultValue                   = false
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
	}
}
