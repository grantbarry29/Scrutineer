/*
Copyright 2026 The Scrutineer Authors.

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

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/enforcement"
)

func TestAppendSessionEvents_idempotentByEventID(t *testing.T) {
	ts := metav1.NewTime(time.Unix(1, 0))
	session := &scrutineerv1alpha1.AgentSession{}
	ev := scrutineerv1alpha1.SessionEvent{
		Time:    ts,
		Type:    scrutineerv1alpha1.SessionEventTypeNetwork,
		Source:  "egress-proxy",
		Action:  "deny",
		Target:  "evil.example.com",
		Message: "blocked",
		EventID: "evt-1",
	}
	AppendSessionEvents(session, []scrutineerv1alpha1.SessionEvent{ev})
	AppendSessionEvents(session, []scrutineerv1alpha1.SessionEvent{ev})
	if len(session.Status.Events) != 1 {
		t.Fatalf("events = %d, want 1", len(session.Status.Events))
	}
}

func TestApplyRuntimePolicyReport_appendsEvents(t *testing.T) {
	ts := metav1.NewTime(time.Unix(2, 0))
	session := &scrutineerv1alpha1.AgentSession{}
	ApplyRuntimePolicyReport(session, enforcement.RuntimeReport{
		Events: []scrutineerv1alpha1.SessionEvent{{
			Time:    ts,
			Type:    scrutineerv1alpha1.SessionEventTypeNetwork,
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
	if session.Status.Events[0].Type != scrutineerv1alpha1.SessionEventTypeNetwork {
		t.Fatalf("type = %s", session.Status.Events[0].Type)
	}
}

func TestAppendSessionEvents_truncatesWithSummary(t *testing.T) {
	session := &scrutineerv1alpha1.AgentSession{}
	incoming := make([]scrutineerv1alpha1.SessionEvent, MaxSessionEvents+10)
	for i := range incoming {
		incoming[i] = scrutineerv1alpha1.SessionEvent{
			Time:    metav1.NewTime(time.Unix(int64(i), 0)),
			Type:    scrutineerv1alpha1.SessionEventTypeNetwork,
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
	if last.Type != scrutineerv1alpha1.SessionEventTypeSystem || last.Action != "truncate" {
		t.Fatalf("expected truncation summary, got %+v", last)
	}
}

func TestMergeEventsInPlace_preservesReporterEvents(t *testing.T) {
	ts := metav1.NewTime(time.Unix(3, 0))
	dst := []scrutineerv1alpha1.SessionEvent{}
	preserve := []scrutineerv1alpha1.SessionEvent{{
		Time: ts, Type: scrutineerv1alpha1.SessionEventTypeTool, Source: "tools-pod", EventID: "tool-1",
	}}
	mergeEventsInPlace(&dst, preserve)
	if len(dst) != 1 {
		t.Fatalf("dst = %d", len(dst))
	}
}
