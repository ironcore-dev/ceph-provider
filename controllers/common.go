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
)

var (
	RookStorageClassMountOptionsDefaultValue = []string{"discard"}
)
