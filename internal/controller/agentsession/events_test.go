/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package agentsession

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
	"github.com/secureai/relay/internal/enforcement"
)

func TestAppendSessionEvents_idempotentByEventID(t *testing.T) {
	ts := metav1.NewTime(time.Unix(1, 0))
	session := &relayv1alpha1.AgentSession{}
	ev := relayv1alpha1.SessionEvent{
		Time:    ts,
		Type:    relayv1alpha1.SessionEventTypeNetwork,
		Source:  "egress-proxy",
		Action:  "deny",
		Target:  "evil.example.com",
		Message: "blocked",
		EventID: "evt-1",
	}
	AppendSessionEvents(session, []relayv1alpha1.SessionEvent{ev})
	AppendSessionEvents(session, []relayv1alpha1.SessionEvent{ev})
	if len(session.Status.Events) != 1 {
		t.Fatalf("events = %d, want 1", len(session.Status.Events))
	}
}

func TestApplyRuntimePolicyReport_appendsEvents(t *testing.T) {
	ts := metav1.NewTime(time.Unix(2, 0))
	session := &relayv1alpha1.AgentSession{}
	ApplyRuntimePolicyReport(session, enforcement.RuntimeReport{
		Events: []relayv1alpha1.SessionEvent{{
			Time:    ts,
			Type:    relayv1alpha1.SessionEventTypeNetwork,
			Source:  "egress-proxy",
			Action:  "deny",
			Target:  "evil.example.com",
			Message: "egress blocked",
			EventID: "net-1",
		}},
	})
	if len(session.Status.Events) != 1 {
		t.Fatalf("events = %d", len(session.Status.Events))
	}
	if session.Status.Events[0].Type != relayv1alpha1.SessionEventTypeNetwork {
		t.Fatalf("type = %s", session.Status.Events[0].Type)
	}
}

func TestAppendSessionEvents_truncatesWithSummary(t *testing.T) {
	session := &relayv1alpha1.AgentSession{}
	incoming := make([]relayv1alpha1.SessionEvent, MaxSessionEvents+10)
	for i := range incoming {
		incoming[i] = relayv1alpha1.SessionEvent{
			Time:    metav1.NewTime(time.Unix(int64(i), 0)),
			Type:    relayv1alpha1.SessionEventTypeNetwork,
			Source:  "egress-proxy",
			Action:  "deny",
			Target:  "host",
			Message: "blocked",
			EventID: "evt-" + itoaEvents(i),
		}
	}
	AppendSessionEvents(session, incoming)

	if len(session.Status.Events) != MaxSessionEvents {
		t.Fatalf("events = %d, want %d", len(session.Status.Events), MaxSessionEvents)
	}
	last := session.Status.Events[len(session.Status.Events)-1]
	if last.Type != relayv1alpha1.SessionEventTypeSystem || last.Action != "truncate" {
		t.Fatalf("expected truncation summary, got %+v", last)
	}
}

func TestMergeEventsInPlace_preservesReporterEvents(t *testing.T) {
	ts := metav1.NewTime(time.Unix(3, 0))
	dst := []relayv1alpha1.SessionEvent{}
	preserve := []relayv1alpha1.SessionEvent{{
		Time: ts, Type: relayv1alpha1.SessionEventTypeTool, Source: "tool-gateway", EventID: "tool-1",
	}}
	mergeEventsInPlace(&dst, preserve)
	if len(dst) != 1 {
		t.Fatalf("dst = %d", len(dst))
	}
}
