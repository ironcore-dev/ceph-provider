// SPDX-FileCopyrightText: 2023 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package api

import (
	apiutils "github.com/ironcore-dev/provider-utils/apiutils/api"
)

type Image struct {
	apiutils.Metadata `json:"metadata,omitempty"`

	Spec   ImageSpec   `json:"spec"`
	Status ImageStatus `json:"status"`
}

type ImageState string

const (
	ImageStatePending   ImageState = "Pending"
	ImageStateAvailable ImageState = "Available"
)

type EncryptionState string

const (
	EncryptionStateHeaderSet EncryptionState = "EncryptionHeaderSet"
)

type ImageSpec struct {
	Size              uint64         `json:"size"`
	WWN               string         `json:"wwn"`
	Limits            Limits         `json:"limits"`
	Image             string         `json:"image"`
	ImageArchitecture *string        `json:"imageArchitecture"`
	SnapshotRef       *string        `json:"snapshotRef"`
	Encryption        *EncryptionSpec `json:"encryption"`
}

type EncryptionType string

const (
	EncryptionTypeEncrypted   EncryptionType = "Encrypted"
	EncryptionTypeUnencrypted EncryptionType = "Unencrypted"
)

type EncryptionSpec struct {
	Type                EncryptionType `json:"type"`
	EncryptedPassphrase []byte         `json:"encryptedPassphrase"`
}

type ImageStatus struct {
	State      ImageState      `json:"state"`
	Encryption EncryptionState `json:"encryption"`
	Access     *ImageAccess    `json:"access"`
	Size       uint64          `json:"size"`
}

type ImageAccess struct {
	Monitors string `json:"monitors"`
	Handle   string `json:"handle"`

	User    string `json:"user"`
	UserKey string `json:"userKey"`
}

type Limits map[LimitType]int64

const (
	IOPSLimit                   LimitType = "rbd_qos_iops_limit"
	IOPSBurstLimit              LimitType = "rbd_qos_iops_burst"
	IOPSBurstDurationLimit      LimitType = "rbd_qos_iops_burst_seconds"
	ReadIOPSLimit               LimitType = "rbd_qos_read_iops_limit"
	ReadIOPSBurstLimit          LimitType = "rbd_qos_read_iops_burst"
	ReadIOPSBurstDurationLimit  LimitType = "rbd_qos_read_iops_burst_seconds"
	WriteIOPSLimit              LimitType = "rbd_qos_write_iops_limit"
	WriteIOPSBurstLimit         LimitType = "rbd_qos_write_iops_burst"
	WriteIOPSBurstDurationLimit LimitType = "rbd_qos_write_iops_burst_seconds"
	BPSLimit                    LimitType = "rbd_qos_bps_limit"
	BPSBurstLimit               LimitType = "rbd_qos_bps_burst"
	BPSBurstDurationLimit       LimitType = "rbd_qos_bps_burst_seconds"
	ReadBPSLimit                LimitType = "rbd_qos_read_bps_limit"
	ReadBPSBurstLimit           LimitType = "rbd_qos_read_bps_burst"
	ReadBPSBurstDurationLimit   LimitType = "rbd_qos_read_bps_burst_seconds"
	WriteBPSLimit               LimitType = "rbd_qos_write_bps_limit"
	WriteBPSBurstLimit          LimitType = "rbd_qos_write_bps_burst"
	WriteBPSBurstDurationLimit  LimitType = "rbd_qos_write_bps_burst_seconds"
)

type LimitType string
