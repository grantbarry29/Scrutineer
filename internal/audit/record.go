/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package audit exports structured governance audit records to a configurable sink.
package audit

import "time"

// EventType categorizes audit records for downstream SIEM/timeline routing.
type EventType string

const (
	EventPolicyViolation    EventType = "policy.violation"
	EventSessionPhaseChange EventType = "session.phase_change"
	EventRuntimeReport      EventType = "runtime.report"
)

// Record is a structured audit entry emitted by the Relay control plane.
type Record struct {
	Time      time.Time
	EventType EventType
	Namespace string
	Session   string
	Actor     string
	Phase     string
	FromPhase string
	Action    string
	Target    string
	Type      string
	Message   string
	Backend   string
	Count     int
}

// PolicyViolation builds a record for a novel policy violation appended to status.
func PolicyViolation(namespace, session, violationType, target, message string, at time.Time) Record {
	if at.IsZero() {
		at = time.Now()
	}
	return Record{
		Time:      at,
		EventType: EventPolicyViolation,
		Namespace: namespace,
		Session:   session,
		Actor:     "relay-controller",
		Action:    "violation",
		Type:      violationType,
		Target:    target,
		Message:   message,
	}
}

// SessionPhaseChange builds a record when an AgentSession lifecycle phase changes.
func SessionPhaseChange(namespace, session, fromPhase, toPhase string, at time.Time) Record {
	if at.IsZero() {
		at = time.Now()
	}
	return Record{
		Time:      at,
		EventType: EventSessionPhaseChange,
		Namespace: namespace,
		Session:   session,
		Actor:     "relay-controller",
		FromPhase: fromPhase,
		Phase:     toPhase,
		Message:   "session phase changed",
	}
}

// RuntimeReport builds a record when runtime evidence is merged from a data-plane backend.
func RuntimeReport(namespace, session, backend string, decisionCount int, at time.Time) Record {
	if at.IsZero() {
		at = time.Now()
	}
	return Record{
		Time:      at,
		EventType: EventRuntimeReport,
		Namespace: namespace,
		Session:   session,
		Actor:     backend,
		Backend:   backend,
		Count:     decisionCount,
		Action:    "accepted",
		Message:   "runtime evidence report merged",
	}
}
