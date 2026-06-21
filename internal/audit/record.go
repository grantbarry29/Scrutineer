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
	EventApprovalGranted    EventType = "approval.granted"
	EventApprovalDenied     EventType = "approval.denied"
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
	// Assurance is the evidence trust level (controller | self-reported |
	// observed), mirroring api/v1alpha1.EvidenceAssurance. Empty when the record
	// type has no assurance notion (e.g. phase changes).
	Assurance string
}

// PolicyViolation builds a record for a novel policy violation appended to status.
// assurance is the evidence trust level (e.g. self-reported for cooperative
// sidecar violations); callers normalize an empty value.
func PolicyViolation(namespace, session, violationType, target, message, assurance string, at time.Time) Record {
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
		Assurance: assurance,
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

// ApprovalDecision builds a record when a human-approval gate is resolved. granted
// selects the event type (approval.granted vs approval.denied); gatedAction is the
// approved action type (e.g. "deploy"); actor is the approver identity (best-effort
// self-declared, or the joined set for allOf), defaulting to relay-controller.
func ApprovalDecision(namespace, session, gatedAction, actor, reason string, granted bool, at time.Time) Record {
	if at.IsZero() {
		at = time.Now()
	}
	eventType := EventApprovalDenied
	verb := "denied"
	if granted {
		eventType = EventApprovalGranted
		verb = "granted"
	}
	if actor == "" {
		actor = "relay-controller"
	}
	return Record{
		Time:      at,
		EventType: eventType,
		Namespace: namespace,
		Session:   session,
		Actor:     actor,
		Action:    verb,
		Type:      "approval",
		Target:    gatedAction,
		Message:   reason,
	}
}

// RuntimeReport builds a record when runtime evidence is merged from a data-plane
// backend. assurance is the evidence trust level of the report (cooperative
// sidecars are self-reported).
func RuntimeReport(namespace, session, backend string, decisionCount int, assurance string, at time.Time) Record {
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
		Assurance: assurance,
	}
}
