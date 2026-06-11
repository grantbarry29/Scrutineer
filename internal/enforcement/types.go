/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package enforcement defines the control-plane contract between Relay and
// replaceable data-plane enforcement backends (NetworkPolicy, egress proxy, tool gateway).
//
// Phase 3 slice 1: types, mode semantics, and reporting helpers only. Concrete backends
// and reconciler wiring arrive in later slices. See docs/design/phase-3-enforcement-architecture.md.
package enforcement

import relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"

// BackendKind identifies an enforcement backend implementation.
type BackendKind string

const (
	BackendNetworkPolicy BackendKind = "networkpolicy"
	BackendEgressProxy   BackendKind = "egress-proxy"
	BackendToolGateway   BackendKind = "tool-gateway"
	BackendFSGateway     BackendKind = "fs-gateway"
)

// Capabilities reports what policy dimensions a backend can enforce.
type Capabilities struct {
	NetworkCIDR bool
	NetworkFQDN bool
	Tools       bool
	FileAccess  bool
}

// SessionContext is normalized control-plane input passed to enforcement backends.
// It is derived from AgentSession status and optional RuntimeProfile — not from env vars.
type SessionContext struct {
	SessionNamespace string
	SessionName      string
	JobName          string
	PodName          string
	Mode             relayv1alpha1.PolicyMode
	Policy           relayv1alpha1.PolicyRules
	Sidecars         []relayv1alpha1.RuntimeProfileSidecar
}

// RuntimeReport is evidence produced by a data-plane backend for controller aggregation.
type RuntimeReport struct {
	Decisions  []relayv1alpha1.PolicyDecision
	Violations []relayv1alpha1.PolicyViolation
	Events     []relayv1alpha1.SessionEvent
}
