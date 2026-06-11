/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package observability

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
)

func TestProjectTimeline_sortsAndProjects(t *testing.T) {
	t1 := metav1.NewTime(time.Unix(10, 0))
	t2 := metav1.NewTime(time.Unix(20, 0))
	entries := ProjectTimeline([]relayv1alpha1.SessionEvent{
		{
			Time:    t2,
			Type:    relayv1alpha1.SessionEventTypeTool,
			Source:  "tool-gateway",
			Action:  "deny",
			Target:  "kubectl",
			Message: "tool denied",
			EventID: "tool-1",
		},
		{
			Time:    t1,
			Type:    relayv1alpha1.SessionEventTypeNetwork,
			Source:  "egress-proxy",
			Action:  "deny",
			Target:  "evil.example.com",
			Message: "egress blocked",
			EventID: "net-1",
		},
		{Time: t1, Message: "skipped without type"},
	})

	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	if entries[0].EventID != "net-1" || entries[1].EventID != "tool-1" {
		t.Fatalf("order = %+v", entries)
	}
	if entries[0].Severity != TimelineSeverityCritical {
		t.Fatalf("network severity = %q", entries[0].Severity)
	}
	if entries[0].Title != "Network deny: evil.example.com" {
		t.Fatalf("title = %q", entries[0].Title)
	}
	if entries[0].Detail != "egress blocked" {
		t.Fatalf("detail = %q", entries[0].Detail)
	}
}

func TestProjectTimeline_zeroTimeSortedLast(t *testing.T) {
	withTime := metav1.NewTime(time.Unix(5, 0))
	entries := ProjectTimeline([]relayv1alpha1.SessionEvent{
		{Type: relayv1alpha1.SessionEventTypeSystem, Action: "truncate", EventID: "z"},
		{Time: withTime, Type: relayv1alpha1.SessionEventTypeLifecycle, Action: "start", EventID: "a"},
	})
	if len(entries) != 2 || entries[0].EventID != "a" {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestProjectTimeline_systemTruncate(t *testing.T) {
	entries := ProjectTimeline([]relayv1alpha1.SessionEvent{{
		Time:    metav1.NewTime(time.Unix(1, 0)),
		Type:    relayv1alpha1.SessionEventTypeSystem,
		Source:  "relay-controller",
		Action:  "truncate",
		Message: "events truncated: omitted 10 entries (max 256)",
		EventID: "trunc-1",
	}})
	if len(entries) != 1 {
		t.Fatal(entries)
	}
	e := entries[0]
	if e.Title != "Event history truncated" || e.Severity != TimelineSeverityWarning {
		t.Fatalf("entry = %+v", e)
	}
}

func TestFilterTimeline_byCategoryAndSeverity(t *testing.T) {
	all := ProjectTimeline([]relayv1alpha1.SessionEvent{
		{Time: metav1.NewTime(time.Unix(1, 0)), Type: relayv1alpha1.SessionEventTypeNetwork, Action: "deny", Target: "a", EventID: "1"},
		{Time: metav1.NewTime(time.Unix(2, 0)), Type: relayv1alpha1.SessionEventTypeTool, Action: "allow", Target: "shell", EventID: "2"},
		{Time: metav1.NewTime(time.Unix(3, 0)), Type: relayv1alpha1.SessionEventTypeSystem, Action: "truncate", EventID: "3"},
	})

	networkOnly := FilterTimeline(all, []relayv1alpha1.SessionEventType{relayv1alpha1.SessionEventTypeNetwork}, nil)
	if len(networkOnly) != 1 || networkOnly[0].EventID != "1" {
		t.Fatalf("network = %+v", networkOnly)
	}

	critical := FilterTimeline(all, nil, []TimelineSeverity{TimelineSeverityCritical})
	if len(critical) != 1 || critical[0].EventID != "1" {
		t.Fatalf("critical = %+v", critical)
	}

	if len(FilterTimeline(all, nil, nil)) != 3 {
		t.Fatal("nil filters should return all")
	}
}

func TestGroupByCategory(t *testing.T) {
	entries := ProjectTimeline([]relayv1alpha1.SessionEvent{
		{Time: metav1.NewTime(time.Unix(1, 0)), Type: relayv1alpha1.SessionEventTypeNetwork, EventID: "n1"},
		{Time: metav1.NewTime(time.Unix(2, 0)), Type: relayv1alpha1.SessionEventTypeTool, EventID: "t1"},
		{Time: metav1.NewTime(time.Unix(3, 0)), Type: relayv1alpha1.SessionEventTypeNetwork, EventID: "n2"},
	})
	groups := GroupByCategory(entries)
	if len(groups[relayv1alpha1.SessionEventTypeNetwork]) != 2 {
		t.Fatalf("network group = %d", len(groups[relayv1alpha1.SessionEventTypeNetwork]))
	}
	if len(groups[relayv1alpha1.SessionEventTypeTool]) != 1 {
		t.Fatalf("tool group = %d", len(groups[relayv1alpha1.SessionEventTypeTool]))
	}
}

func TestTimelineEntryID_withoutEventID(t *testing.T) {
	ts := metav1.NewTime(time.Unix(99, 0))
	id := timelineEntryID(relayv1alpha1.SessionEvent{
		Time:   ts,
		Type:   relayv1alpha1.SessionEventTypePolicy,
		Source: "relay-controller",
		Action: "audit",
		Target: "deploy",
	})
	if id == "" || id == "deploy" {
		t.Fatalf("id = %q", id)
	}
}
