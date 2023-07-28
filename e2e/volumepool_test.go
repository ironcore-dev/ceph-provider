/*
 * Copyright (c) 2021 by the OnMetal authors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */
 
package e2e

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	//	"sigs.k8s.io/controller-runtime/pkg/client"

	storagev1alpha1 "github.com/onmetal/onmetal-api/api/storage/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	. "github.com/onsi/gomega"
)

var _ = Describe("cephlet-volumepool", func() {
	var (
		volumePool *storagev1alpha1.VolumePool
	)

	It("VolumePool Creation", func(ctx SpecContext) {
		By("checking that a VolumePool has been created")
		volumePool = &storagev1alpha1.VolumePool{
			ObjectMeta: metav1.ObjectMeta{
				Name: "volumepool-testcase",
			},
			Spec: storagev1alpha1.VolumePoolSpec{
				ProviderID: "cephlet",
			},
		}
		Expect(k8sClient.Create(ctx, volumePool)).Should(Succeed())
		fmt.Println("Here the Volumepool is getting created********", volumePool.Name)

		By("issuing a delete request for the volume pool")
		Expect(k8sClient.Delete(ctx, volumePool)).Should(Succeed())
		fmt.Println("Here the VolumePool is getting deleted********", volumePool.Name)
	})

})
