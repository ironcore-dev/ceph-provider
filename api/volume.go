// SPDX-FileCopyrightText: 2023 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package api

import (
	apiutils "github.com/ironcore-dev/provider-utils/apiutils/api"
)

type Volume struct {
	apiutils.Metadata `json:"metadata,omitempty"`

	Spec   VolumeSpec   `json:"spec"`
	Status VolumeStatus `json:"status"`
}

type VolumeState string

const (
	VolumeStatePending   VolumeState = "Pending"
	VolumeStateAvailable VolumeState = "Available"
)

type VolumeEncryptionState string

const (
	VolumeEncryptionStateHeaderSet VolumeEncryptionState = "VolumeEncryptionHeaderSet"
)

type VolumeSpec struct {
	Size             uint64               `json:"size"`
	WWN              string               `json:"wwn"`
	Limits           Limits               `json:"limits"`
	VolumeEncryption VolumeEncryptionSpec `json:"encryption"`

	Source VolumeSource `json:"source"`
}

type VolumeSource struct {
	OSVolume       *OSVolumeSource `json:"osVolume"`
	SnapshotSource *string         `json:"snapshotSource"`
}

type OSVolumeSource struct {
	Name         string  `json:"name"`
	Architecture *string `json:"architecture"`
}

type VolumeEncryptionType string

const (
	VolumeEncryptionTypeEncrypted   VolumeEncryptionType = "Encrypted"
	VolumeEncryptionTypeUnencrypted VolumeEncryptionType = "Unencrypted"
)

type VolumeEncryptionSpec struct {
	Type                VolumeEncryptionType `json:"type"`
	EncryptedPassphrase []byte               `json:"encryptedPassphrase"`
}

type VolumeStatus struct {
	State            VolumeState           `json:"state"`
	VolumeEncryption VolumeEncryptionState `json:"encryption"`
	Access           *VolumeAccess         `json:"access"`
	Size             uint64                `json:"size"`
}

type VolumeAccess struct {
	Monitors string `json:"monitors"`
	Handle   string `json:"handle"`

	User    string `json:"user"`
	UserKey string `json:"userKey"`
}
