// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	providerapi "github.com/ironcore-dev/ceph-provider/api"
	apiutils "github.com/ironcore-dev/provider-utils/apiutils/api"
	"github.com/ironcore-dev/provider-utils/storeutils/store"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type fakeImageStore struct {
	updates int
}

func (f *fakeImageStore) Create(context.Context, *providerapi.Image) (*providerapi.Image, error) {
	return nil, fmt.Errorf("unexpected create")
}

func (f *fakeImageStore) Get(context.Context, string) (*providerapi.Image, error) {
	return nil, fmt.Errorf("unexpected get")
}

func (f *fakeImageStore) Update(_ context.Context, img *providerapi.Image) (*providerapi.Image, error) {
	f.updates++
	return img, nil
}

func (f *fakeImageStore) Delete(context.Context, string) error {
	return fmt.Errorf("unexpected delete")
}

func (f *fakeImageStore) List(context.Context, ...store.ListOption) ([]*providerapi.Image, error) {
	return nil, fmt.Errorf("unexpected list")
}

func (f *fakeImageStore) Watch(context.Context, ...store.ListOption) (store.Watch[*providerapi.Image], error) {
	return nil, fmt.Errorf("unexpected watch")
}

func availableImage(monitors string) *providerapi.Image {
	return &providerapi.Image{
		Metadata: apiutils.Metadata{ID: "img"},
		Status: providerapi.ImageStatus{
			State: providerapi.ImageStateAvailable,
			Access: &providerapi.ImageAccess{
				Monitors: monitors,
				Handle:   "devpool/img",
			},
		},
	}
}

var _ = Describe("syncAccessMonitors", func() {
	It("writes the reconciler list when an address was added", func() {
		images := &fakeImageStore{}
		r := &ImageReconciler{
			images:   images,
			monitors: "10.0.0.1:6789,10.0.0.2:6789",
		}
		img := availableImage("10.0.0.1:6789")

		Expect(r.syncAccessMonitors(context.Background(), logr.Discard(), img)).To(Succeed())
		Expect(images.updates).To(Equal(1))
		Expect(img.Status.Access.Monitors).To(Equal("10.0.0.1:6789,10.0.0.2:6789"))
	})

	It("does not write when the stored list is the same addresses in another order", func() {
		images := &fakeImageStore{}
		r := &ImageReconciler{
			images:   images,
			monitors: "10.0.0.1:6789,10.0.0.2:6789",
		}
		img := availableImage("10.0.0.2:6789,10.0.0.1:6789")

		Expect(r.syncAccessMonitors(context.Background(), logr.Discard(), img)).To(Succeed())
		Expect(images.updates).To(Equal(0))
		Expect(img.Status.Access.Monitors).To(Equal("10.0.0.2:6789,10.0.0.1:6789"))
	})

	It("returns an error and does not write when access is missing", func() {
		images := &fakeImageStore{}
		r := &ImageReconciler{
			images:   images,
			monitors: "10.0.0.1:6789",
		}
		img := availableImage("10.0.0.1:6789")
		img.Status.Access = nil

		Expect(r.syncAccessMonitors(context.Background(), logr.Discard(), img)).To(MatchError(ContainSubstring("image access is nil")))
		Expect(images.updates).To(Equal(0))
	})
})
