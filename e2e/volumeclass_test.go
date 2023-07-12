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
	corev1alpha1 "github.com/onmetal/onmetal-api/api/core/v1alpha1"
	. "github.com/onmetal/onmetal-api/utils/testing"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	//corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	//apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	storagev1alpha1 "github.com/onmetal/onmetal-api/api/storage/v1alpha1"
)

var _ = Describe("VolumeClass controller", func() {
	ctx := SetupContext()
	//ns, _ := SetupTest(ctx)

	It("should finalize the volume class if no volume is using it", func() {
		By("creating the volume class consumed by the volume")
		volumeClass := &storagev1alpha1.VolumeClass{
			ObjectMeta: metav1.ObjectMeta{
				Name: "volumeclass-test",
			},
			Capabilities: corev1alpha1.ResourceList{
				corev1alpha1.ResourceTPS:  resource.MustParse("100Mi"),
				corev1alpha1.ResourceIOPS: resource.MustParse("100"),
			},
		}
		Expect(k8sClient.Create(ctx, volumeClass)).Should(Succeed())



		By("checking the finalizer is present")
		fmt.Println(client.ObjectKeyFromObject(volumeClass))
		By("issuing a delete request for the volume class")
		Expect(k8sClient.Delete(ctx, volumeClass)).Should(Succeed())

	})
})
