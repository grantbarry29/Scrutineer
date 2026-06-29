/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package observability projects AgentSession status into UI-oriented views.
package observability

import (
	"fmt"
	"sort"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
)

// TimelineSeverity is a normalized importance level for timeline rendering.
type TimelineSeverity string

const (
	TimelineSeverityInfo     TimelineSeverity = "info"
	TimelineSeverityWarning  TimelineSeverity = "warning"
	TimelineSeverityCritical TimelineSeverity = "critical"
)

// TimelineEntry is a UI-ready projection of one SessionEvent.
type TimelineEntry struct {
	// ID is a stable key for list rendering (prefers eventId when set).
	ID string `json:"id"`

	Time metav1.Time `json:"time"`

	// Category mirrors SessionEvent.Type for filtering.
	Category scrutineerv1alpha1.SessionEventType `json:"category"`

	Severity TimelineSeverity `json:"severity"`

	// Title is a short headline for timeline rows.
	Title string `json:"title"`

	// Detail is operator-facing body text (message or synthesized fallback).
	Detail string `json:"detail,omitempty"`

	Source  string `json:"source,omitempty"`
	Action  string `json:"action,omitempty"`
	Target  string `json:"target,omitempty"`
	EventID string `json:"eventId,omitempty"`
}

// ProjectTimeline maps status.events into a chronologically sorted timeline.
// Events without a type are skipped. Order is ascending by time; ties break on ID.
func ProjectTimeline(events []scrutineerv1alpha1.SessionEvent) []TimelineEntry {
	if len(events) == 0 {
		return nil
	}
	out := make([]TimelineEntry, 0, len(events))
	for _, e := range events {
		if strings.TrimSpace(string(e.Type)) == "" {
			continue
		}
		entry := projectEvent(e)
		out = append(out, entry)
	}
	sort.SliceStable(out, func(i, j int) bool {
		ti, tj := out[i].Time.Time, out[j].Time.Time
		if ti.Equal(tj) {
			return out[i].ID < out[j].ID
		}
		if ti.IsZero() {
			return false
		}
		if tj.IsZero() {
			return true
		}
		return ti.Before(tj)
	})
	return out
}

// FilterTimeline returns entries matching optional category and/or severity filters.
// Nil or empty filter slices mean no restriction on that dimension.
func FilterTimeline(entries []TimelineEntry, categories []scrutineerv1alpha1.SessionEventType, severities []TimelineSeverity) []TimelineEntry {
	if len(entries) == 0 {
		return nil
	}
	catSet := stringSet(len(categories))
	for _, c := range categories {
		catSet[string(c)] = struct{}{}
	}
	sevSet := stringSet(len(severities))
	for _, s := range severities {
		sevSet[string(s)] = struct{}{}
	}
	if len(catSet) == 0 && len(sevSet) == 0 {
		return append([]TimelineEntry(nil), entries...)
	}

	out := make([]TimelineEntry, 0, len(entries))
	for _, e := range entries {
		if len(catSet) > 0 {
			if _, ok := catSet[string(e.Category)]; !ok {
				continue
			}
		}
		if len(sevSet) > 0 {
			if _, ok := sevSet[string(e.Severity)]; !ok {
				continue
			}
		}
		out = append(out, e)
	}
	return out
}

// GroupByCategory buckets timeline entries by category while preserving entry order within each bucket.
func GroupByCategory(entries []TimelineEntry) map[scrutineerv1alpha1.SessionEventType][]TimelineEntry {
	if len(entries) == 0 {
		return nil
	}
	out := make(map[scrutineerv1alpha1.SessionEventType][]TimelineEntry)
	for _, e := range entries {
		out[e.Category] = append(out[e.Category], e)
	}
	return out
}

func projectEvent(e scrutineerv1alpha1.SessionEvent) TimelineEntry {
	action := strings.ToLower(strings.TrimSpace(e.Action))
	return TimelineEntry{
		ID:       timelineEntryID(e),
		Time:     e.Time,
		Category: e.Type,
		Severity: severityForEvent(e.Type, action),
		Title:    titleForEvent(e),
		Detail:   detailForEvent(e),
		Source:   e.Source,
		Action:   e.Action,
		Target:   e.Target,
		EventID:  e.EventID,
	}
}

func severityForEvent(typ scrutineerv1alpha1.SessionEventType, action string) TimelineSeverity {
	switch action {
	case "deny", "block", "blocked":
		return TimelineSeverityCritical
	case "dry-run", "audit", "would-deny":
		return TimelineSeverityWarning
	case "truncate":
		return TimelineSeverityWarning
	}
	if typ == scrutineerv1alpha1.SessionEventTypeSystem {
		return TimelineSeverityWarning
	}
	return TimelineSeverityInfo
}

func titleForEvent(e scrutineerv1alpha1.SessionEvent) string {
	action := strings.ToLower(strings.TrimSpace(e.Action))
	target := strings.TrimSpace(e.Target)
	source := strings.TrimSpace(e.Source)

	switch e.Type {
	case scrutineerv1alpha1.SessionEventTypeNetwork:
		if target != "" {
			return fmt.Sprintf("Network %s: %s", actionLabel(action), target)
		}
		return "Network " + actionLabel(action)
	case scrutineerv1alpha1.SessionEventTypeTool:
		if target != "" {
			return fmt.Sprintf("Tool %s: %s", actionLabel(action), target)
		}
		return "Tool " + actionLabel(action)
	case scrutineerv1alpha1.SessionEventTypePolicy:
		if target != "" {
			return fmt.Sprintf("Policy %s: %s", actionLabel(action), target)
		}
		return "Policy " + actionLabel(action)
	case scrutineerv1alpha1.SessionEventTypeLifecycle:
		if action != "" {
			return "Lifecycle: " + actionLabel(action)
		}
		return "Lifecycle event"
	case scrutineerv1alpha1.SessionEventTypeSystem:
		if action == "truncate" {
			return "Event history truncated"
		}
		if source != "" {
			return "System: " + source
		}
		return "System event"
	default:
		if strings.TrimSpace(e.Message) != "" {
			return e.Message
		}
		return string(e.Type) + " event"
	}
}

func detailForEvent(e scrutineerv1alpha1.SessionEvent) string {
	if msg := strings.TrimSpace(e.Message); msg != "" {
		return msg
	}
	target := strings.TrimSpace(e.Target)
	action := strings.TrimSpace(e.Action)
	source := strings.TrimSpace(e.Source)
	switch {
	case target != "" && action != "":
		return fmt.Sprintf("%s %s", action, target)
	case target != "":
		return target
	case source != "":
		return source
	default:
		return ""
	}
}

func actionLabel(action string) string {
	if action == "" {
		return "event"
	}
	return action
}

func timelineEntryID(e scrutineerv1alpha1.SessionEvent) string {
	if id := strings.TrimSpace(e.EventID); id != "" {
		return id
	}
	return fmt.Sprintf("%s-%s-%s-%s-%d",
		e.Type,
		e.Source,
		e.Action,
		e.Target,
		e.Time.UnixNano(),
	)
}

func stringSet(n int) map[string]struct{} {
	if n == 0 {
		return nil
	}
	return make(map[string]struct{}, n)
}
