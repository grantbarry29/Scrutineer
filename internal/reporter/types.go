/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package reporter implements the controller-owned runtime evidence ingestion endpoint.
// See docs/design/phase-3-runtime-reporter-contract.md.
package reporter

import (
	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
)

const (
	// DefaultBindAddress is the HTTP listen address for POST /v1/report.
	DefaultBindAddress = ":8088"
	// TokenAudience is the projected service account token audience for sidecar auth.
	TokenAudience = "scrutineer-reporter"
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
	Session    SessionRef                           `json:"session"`
	Backend    string                               `json:"backend"`
	ReportID   string                               `json:"reportId,omitempty"`
	Decisions  []scrutineerv1alpha1.PolicyDecision  `json:"decisions"`
	Violations []scrutineerv1alpha1.PolicyViolation `json:"violations,omitempty"`
	Events     []scrutineerv1alpha1.SessionEvent    `json:"events,omitempty"`
	Usage      *scrutineerv1alpha1.SessionUsage     `json:"usage,omitempty"`
}

// CallerClass distinguishes where an authenticated reporter caller runs, which decides
// the assurance stamped on its evidence (see docs/design/evidence-integrity.md §4).
type CallerClass string

const (
	// CallerAgentSidecar is a cooperative in-agent-pod sidecar (dns-proxy, tool-gateway,
	// fs-gateway): it shares the agent's pod, so its evidence is self-reported. The zero
	// value of CallerClass maps here so an unset class can never over-claim.
	CallerAgentSidecar CallerClass = "agent-sidecar"
	// CallerEgressProxy is the session's out-of-pod Envoy egress-proxy pod (dedicated
	// per-session identity, controller owner-ref) — outside the agent's trust domain,
	// so its evidence is stamped observed (Slice C, #62).
	CallerEgressProxy CallerClass = "egress-proxy"
)

// CallerIdentity is an authenticated pod authorized to report evidence for a session.
type CallerIdentity struct {
	Namespace string
	PodName   string
	// Class is how the caller was authorized (agent-sidecar vs egress-proxy). Empty
	// means agent-sidecar.
	Class CallerClass
}

// Assurance maps the caller's class to the evidence assurance the reporter stamps.
// Identity — never the payload — is the sole source of assurance.
func (c CallerIdentity) Assurance() scrutineerv1alpha1.EvidenceAssurance {
	if c.Class == CallerEgressProxy {
		return scrutineerv1alpha1.EvidenceObserved
	}
	return scrutineerv1alpha1.EvidenceSelfReported
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
