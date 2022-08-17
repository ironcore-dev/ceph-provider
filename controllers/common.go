package controllers

const (
	RookMonitorConfigMapNameDefaultValue             = "rook-ceph-mon-endpoints"
	RookMonitorConfigMapDataKeyDefaultValue          = "csi-cluster-config-json"
	RookCSIRBDProvisionerSecretNameDefaultValue      = "rook-csi-rbd-provisioner"
	RookCSIRBDNodeSecretNameDefaultValue             = "rook-csi-rbd-node"
	RookStorageClassAllowVolumeExpansionDefaultValue = true
	RookStorageClassFSTypeDefaultValue               = "ext4"
	RookStorageClassImageFeaturesDefaultValue        = "layering"
	RookStorageClassReclaimPolicyDefaultValue        = "Delete"
	RookStorageClassVolumeBindingModeDefaultValue    = "Immediate"
	RookClusterIdDefaultValue                        = "rook-ceph"
	RookCSIDriverNameDefaultValue                    = "rook-ceph.rbd.csi.ceph.com"
	EnableRBDStatsDefaultValue                       = false
)

var (
	RookStorageClassMountOptionsDefaultValue = []string{"discard"}
)
