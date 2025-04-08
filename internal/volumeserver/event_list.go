// SPDX-FileCopyrightText: 2023 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package volumeserver

import (
	"context"
	"github.com/go-logr/logr"
	"github.com/ironcore-dev/ceph-provider/api"
	apiutils "github.com/ironcore-dev/provider-utils/apiutils/api"
	"github.com/ironcore-dev/provider-utils/eventutils/recorder"

	irievent "github.com/ironcore-dev/ironcore/iri/apis/event/v1alpha1"
	iri "github.com/ironcore-dev/ironcore/iri/apis/volume/v1alpha1"
	"k8s.io/apimachinery/pkg/labels"
)

func (s *Server) filterEvents(log logr.Logger, events []*recorder.Event, filter *iri.EventFilter) []*recorder.Event {
	if filter == nil {
		return events
	}

	var (
		res []*recorder.Event
		sel = labels.SelectorFromSet(filter.LabelSelector)
	)
	for _, iriEvent := range events {
		originLabel, err := apiutils.GetLabelsAnnotation(iriEvent.InvolvedObjectMeta, api.LabelsAnnotation)
		if err != nil {
			log.V(1).Info("Failed to get Labels from iriEvent")
			continue
		}

		if !sel.Matches(labels.Set(originLabel)) {
			continue
		}

		if filter.EventsFromTime > 0 && filter.EventsToTime > 0 {
			if iriEvent.EventTime < filter.EventsFromTime || iriEvent.EventTime > filter.EventsToTime {
				continue
			}
		}

		res = append(res, iriEvent)
	}
	return res
}

func (s *Server) convertEventToIriEvent(events []*recorder.Event) []*irievent.Event {
	var (
		res []*irievent.Event
	)

	for _, event := range events {
		metadata, error := api.GetObjectMetadata(event.InvolvedObjectMeta)
		res = append(res, &irievent.Event{
			Spec: &irievent.EventSpec{
				InvolvedObjectMeta: metadata,
				Reason:             event.Reason,
				Message:            event.Message,
				Type:               event.Type,
				EventTime:          event.EventTime,
			},
		})
	}
}

func (s *Server) ListEvents(ctx context.Context, req *iri.ListEventsRequest) (*iri.ListEventsResponse, error) {
	events := s.volumeEventStore.ListEvents()
	log := s.loggerFrom(ctx)
	filteredEvents := s.filterEvents(log, events, req.Filter)
	iriEvents := s.convertEventToIriEvent(filteredEvents)

	return &iri.ListEventsResponse{
		Events: iriEvents,
	}, nil
}
