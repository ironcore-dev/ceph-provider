// SPDX-FileCopyrightText: 2023 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package volumeserver

import (
	"encoding/json"

	"github.com/ironcore-dev/ceph-provider/api"
	apiutils "github.com/ironcore-dev/provider-utils/apiutils/api"
	"github.com/ironcore-dev/provider-utils/eventutils/recorder"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
)

// newEventMetadata builds an apiutils.Metadata carrying the labels and annotations
// that api.GetObjectMetadata expects, so convertEventToIriEvent does not error out
// on a bare metadata value.
func newEventMetadata(id string) apiutils.Metadata {
	labelsJSON, err := json.Marshal(map[string]string{"foo": "bar"})
	Expect(err).NotTo(HaveOccurred())
	annotationsJSON, err := json.Marshal(map[string]string{})
	Expect(err).NotTo(HaveOccurred())

	return apiutils.Metadata{
		ID: id,
		Annotations: map[string]string{
			api.LabelsAnnotation:      string(labelsJSON),
			api.AnnotationsAnnotation: string(annotationsJSON),
		},
	}
}

var _ = Describe("convertEventToIriEvent", func() {
	var s *Server

	BeforeEach(func() {
		s = &Server{}
	})

	DescribeTable("propagates the recorded event fields to the IRI event",
		func(action string) {
			By("recording an event with the given action")
			event := &recorder.Event{
				InvolvedObjectMeta: newEventMetadata("img-1"),
				Type:               corev1.EventTypeNormal,
				Reason:             "EmptyImageCreationSucceeded",
				Action:             action,
				Message:            "Created empty image. bytes: 123",
				EventTime:          42,
			}

			By("converting the recorded event to an IRI event")
			iriEvents, err := s.convertEventToIriEvent([]*recorder.Event{event})
			Expect(err).NotTo(HaveOccurred())
			Expect(iriEvents).To(HaveLen(1))

			By("asserting the action and the remaining fields are propagated")
			Expect(iriEvents[0].Spec).To(SatisfyAll(
				HaveField("Action", Equal(action)),
				HaveField("Reason", Equal("EmptyImageCreationSucceeded")),
				HaveField("Message", Equal("Created empty image. bytes: 123")),
				HaveField("Type", Equal(corev1.EventTypeNormal)),
				HaveField("EventTime", Equal(int64(42))),
				HaveField("InvolvedObjectMeta.Id", Equal("img-1")),
			))
		},
		Entry("when the action is set", "CreateImage"),
		Entry("when the action is empty", ""),
	)
})
