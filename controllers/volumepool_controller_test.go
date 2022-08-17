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

package controllers

import (
	storagev1alpha1 "github.com/onmetal/onmetal-api/apis/storage/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("VolumePoolReconciler", func() {
	_ = SetupTest(ctx)

	It("should announce volumepool", func() {
		By("checking that a VolumePool has been created")
		volumePool := &storagev1alpha1.VolumePool{}
		volumePoolKey := types.NamespacedName{Name: volumePoolName}
		Expect(k8sClient.Get(ctx, volumePoolKey, volumePool)).Should(Succeed())
	})
})
