/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package agentsession

import (
	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
)

// MaxSessionEvents caps status.events entries per AgentSession.
const MaxSessionEvents = 256

// AppendSessionEvents appends runtime events onto session status without exceeding the cap.
// Duplicate events (same sessionEventKey) are skipped for idempotent reporter delivery.
func AppendSessionEvents(session *relayv1alpha1.AgentSession, incoming []relayv1alpha1.SessionEvent) {
	if session == nil || len(incoming) == 0 {
		return
	}

	keys := make(map[string]struct{}, len(session.Status.Events))
	for _, e := range session.Status.Events {
		keys[sessionEventKey(e)] = struct{}{}
	}

	out := append([]relayv1alpha1.SessionEvent(nil), session.Status.Events...)
	for _, e := range incoming {
		key := sessionEventKey(e)
		if _, ok := keys[key]; ok {
			continue
		}
		out = append(out, e)
		keys[key] = struct{}{}
	}

	if len(out) <= MaxSessionEvents {
		session.Status.Events = out
		return
	}

	omitted := len(out) - MaxSessionEvents
	if MaxSessionEvents <= 1 {
		session.Status.Events = out[:MaxSessionEvents]
		return
	}

	out = out[:MaxSessionEvents-1]
	last := incoming[len(incoming)-1]
	out = append(out, relayv1alpha1.SessionEvent{
		Time:    last.Time,
		Type:    relayv1alpha1.SessionEventTypeSystem,
		Source:  "relay-controller",
		Action:  "truncate",
		Message: formatEventTruncationMessage(omitted, MaxSessionEvents),
	})
	session.Status.Events = out
}

func sessionEventKey(e relayv1alpha1.SessionEvent) string {
	if e.EventID != "" {
		return "id\x00" + e.EventID
	}
	return e.Time.String() + "\x00" + string(e.Type) + "\x00" + e.Source + "\x00" + e.Action + "\x00" + e.Target + "\x00" + e.Message
}

func formatEventTruncationMessage(omitted, maxTotal int) string {
	return "events truncated: omitted " + itoaEvents(omitted) + " entries (max " + itoaEvents(maxTotal) + ")"
}

func itoaEvents(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// mergeEventsInPlace appends events from preserve that are absent from dst.
func mergeEventsInPlace(dst *[]relayv1alpha1.SessionEvent, preserve []relayv1alpha1.SessionEvent) {
	if dst == nil || len(preserve) == 0 {
		return
	}
	keys := make(map[string]struct{}, len(*dst))
	for _, e := range *dst {
		keys[sessionEventKey(e)] = struct{}{}
	}
	var missing []relayv1alpha1.SessionEvent
	for _, e := range preserve {
		if _, ok := keys[sessionEventKey(e)]; !ok {
			missing = append(missing, e)
		}
	}
	if len(missing) == 0 {
		return
	}
	// Reuse append helper for cap enforcement on the combined list.
	tmp := &relayv1alpha1.AgentSession{Status: relayv1alpha1.AgentSessionStatus{Events: *dst}}
	AppendSessionEvents(tmp, missing)
	*dst = tmp.Status.Events
}
