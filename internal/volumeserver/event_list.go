// SPDX-FileCopyrightText: 2023 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package volumeserver

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	"github.com/ironcore-dev/ceph-provider/api"
	"github.com/ironcore-dev/ceph-provider/internal/utils"
	irievent "github.com/ironcore-dev/ironcore/iri/apis/event/v1alpha1"
	iri "github.com/ironcore-dev/ironcore/iri/apis/volume/v1alpha1"
	apiutils "github.com/ironcore-dev/provider-utils/apiutils/api"
	"github.com/ironcore-dev/provider-utils/eventutils/recorder"
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

func (s *Server) convertEventToIriEvent(events []*recorder.Event) ([]*irievent.Event, error) {
	var (
		res []*irievent.Event
	)

	for _, event := range events {
		metadata, err := api.GetObjectMetadata(event.InvolvedObjectMeta)
		if err != nil {
			return nil, fmt.Errorf("failed to get object metadata: %w", err)
		}
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
	return res, nil
}

func (s *Server) ListEvents(ctx context.Context, req *iri.ListEventsRequest) (*iri.ListEventsResponse, error) {
	log := s.loggerFrom(ctx)

	events := s.volumeEventStore.ListEvents()
	filteredEvents := s.filterEvents(log, events, req.Filter)
	iriEvents, err := s.convertEventToIriEvent(filteredEvents)
	if err != nil {
		return nil, utils.ConvertInternalErrorToGRPC(err)
	}

	return &iri.ListEventsResponse{
		Events: iriEvents,
	}, nil
}
