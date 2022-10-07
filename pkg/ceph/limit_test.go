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

package ceph_test

import (
	"fmt"
	"testing"

	"github.com/onmetal/cephlet/pkg/ceph"
	storagev1alpha1 "github.com/onmetal/onmetal-api/apis/storage/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	iops int64 = 1000
	tps  int64 = 100
	size int64 = 5
)

func TestCalculateRelativeLimits(t *testing.T) {
	volume := &storagev1alpha1.Volume{Spec: storagev1alpha1.VolumeSpec{Resources: map[corev1.ResourceName]resource.Quantity{
		corev1.ResourceStorage: resource.MustParse(fmt.Sprintf("%dG", size)),
	}}}
	volumeClass := &storagev1alpha1.VolumeClass{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				ceph.LabelLimitsPerGB: "true",
			},
		},
		Capabilities: map[corev1.ResourceName]resource.Quantity{
			storagev1alpha1.ResourceIOPS: resource.MustParse(fmt.Sprintf("%d", iops)),
			storagev1alpha1.ResourceTPS:  resource.MustParse(fmt.Sprintf("%d", tps)),
		},
	}

	limits, err := ceph.CalculateLimits(volume, volumeClass)
	if err != nil {
		t.Fail()
	}

	if l, ok := limits[ceph.IOPSlLimit]; !ok || l.Value() != iops*size {
		t.Fail()
	}

	if l, ok := limits[ceph.WriteIOPSLimit]; !ok || l.Value() != iops*size {
		t.Fail()
	}

	if l, ok := limits[ceph.ReadIOPSLimit]; !ok || l.Value() != iops*size {
		t.Fail()
	}

	if l, ok := limits[ceph.BPSLimit]; !ok || l.Value() != tps*size {
		t.Fail()
	}

	if l, ok := limits[ceph.WriteBPSLimit]; !ok || l.Value() != tps*size {
		t.Fail()
	}

	if l, ok := limits[ceph.ReadBPSLimit]; !ok || l.Value() != tps*size {
		t.Fail()
	}
}

func TestCalculateAbsoluteLimits(t *testing.T) {
	volume := &storagev1alpha1.Volume{Spec: storagev1alpha1.VolumeSpec{Resources: map[corev1.ResourceName]resource.Quantity{
		corev1.ResourceStorage: resource.MustParse(fmt.Sprintf("%dG", size)),
	}}}
	volumeClass := &storagev1alpha1.VolumeClass{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{},
		},
		Capabilities: map[corev1.ResourceName]resource.Quantity{
			storagev1alpha1.ResourceIOPS: resource.MustParse(fmt.Sprintf("%d", iops)),
			storagev1alpha1.ResourceTPS:  resource.MustParse(fmt.Sprintf("%d", tps)),
		},
	}

	limits, err := ceph.CalculateLimits(volume, volumeClass)
	if err != nil {
		t.Fail()
	}

	if l, ok := limits[ceph.IOPSlLimit]; !ok || l.Value() != iops {
		t.Fail()
	}

	if l, ok := limits[ceph.WriteIOPSLimit]; !ok || l.Value() != iops {
		t.Fail()
	}

	if l, ok := limits[ceph.ReadIOPSLimit]; !ok || l.Value() != iops {
		t.Fail()
	}

	if l, ok := limits[ceph.BPSLimit]; !ok || l.Value() != tps {
		t.Fail()
	}

	if l, ok := limits[ceph.WriteBPSLimit]; !ok || l.Value() != tps {
		t.Fail()
	}

	if l, ok := limits[ceph.ReadBPSLimit]; !ok || l.Value() != tps {
		t.Fail()
	}
}
