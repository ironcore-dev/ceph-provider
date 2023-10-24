// Copyright 2023 OnMetal authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package api

type Image struct {
	Metadata `json:"metadata,omitempty"`

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
	Size        uint64         `json:"size"`
	WWN         string         `json:"wwn"`
	Limits      Limits         `json:"limits"`
	Image       string         `json:"image"`
	SnapshotRef *string        `json:"snapshotRef"`
	Encryption  EncryptionSpec `json:"encryption"`
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
}

type ImageAccess struct {
	Monitors string `json:"monitors"`
	Handle   string `json:"handle"`

	User    string `json:"user"`
	UserKey string `json:"userKey"`
}

type Limits map[LimitType]int64

const (
	IOPSlLimit                  LimitType = "rbd_qos_iops_limit"
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
