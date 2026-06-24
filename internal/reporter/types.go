/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package reporter implements the controller-owned runtime evidence ingestion endpoint.
// See docs/design/phase-3-runtime-reporter-contract.md.
package reporter

import (
	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
)

const (
	// DefaultBindAddress is the HTTP listen address for POST /v1/report.
	DefaultBindAddress = ":8088"
	// TokenAudience is the projected service account token audience for sidecar auth.
	TokenAudience = "relay-reporter"
	// MaxReportBytes bounds the request body size.
	MaxReportBytes = 64 * 1024
	// MaxDecisionsPerReport caps decisions in a single report payload.
	MaxDecisionsPerReport = 128
	// MaxEventsPerReport caps events in a single report payload.
	MaxEventsPerReport = 64
)

// SessionRef identifies the AgentSession a report targets.
type SessionRef struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

// ReportRequest is the JSON body for POST /v1/report.
type ReportRequest struct {
	Session    SessionRef                      `json:"session"`
	Backend    string                          `json:"backend"`
	ReportID   string                          `json:"reportId,omitempty"`
	Decisions  []relayv1alpha1.PolicyDecision  `json:"decisions"`
	Violations []relayv1alpha1.PolicyViolation `json:"violations,omitempty"`
	Events     []relayv1alpha1.SessionEvent    `json:"events,omitempty"`
	Usage      *relayv1alpha1.SessionUsage     `json:"usage,omitempty"`
}

// CallerIdentity is an authenticated sidecar pod authorized to report evidence.
type CallerIdentity struct {
	Namespace string
	PodName   string
}

// ApprovalRegisterRequest is the JSON body for POST /v1/approvals. A tool-gateway
// sidecar posts it to register (or look up) a mid-execution hold for a single
// tool call. It is idempotent per (session, requestId): repeated posts return the
// same approval without creating duplicates. It carries NO raw argument values —
// only a redacted argDigest — so the control plane never ingests sensitive args.
type ApprovalRegisterRequest struct {
	Session   SessionRef `json:"session"`
	RequestID string     `json:"requestId"`
	Action    string     `json:"action"`
	Target    string     `json:"target,omitempty"`
	ArgDigest string     `json:"argDigest,omitempty"`
	PolicyRef string     `json:"policyRef,omitempty"`
	// Window is an optional post-grant validity duration (Go duration string, e.g.
	// "15m"). Used only when no matching ApprovalPolicy supplies expiresAfter.
	Window string `json:"window,omitempty"`
}

// ApprovalResponse is the JSON response for the approval channel. State mirrors
// ApprovalRequest.status.state (Pending until a human decides).
type ApprovalResponse struct {
	ApprovalID string `json:"approvalId"`
	State      string `json:"state"`
	ExpiresAt  string `json:"expiresAt,omitempty"`
	Reason     string `json:"reason,omitempty"`
}
