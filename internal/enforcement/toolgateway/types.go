/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package toolgateway defines the control-plane contract and first-party tool-gateway
// sidecar (`cmd/tool-gateway`, `Dockerfile.tool-gateway`) for MCP/tool-call governance.
package toolgateway

import (
	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/enforcement"
)

// SidecarType is the RuntimeProfile sidecar type for tool gateways.
const SidecarType = "tool-gateway"

// ToolRequest is metadata for a single tool invocation observed at the gateway.
type ToolRequest struct {
	// Tool is the stable tool identifier (MCP tool name or Scrutineer tool id).
	Tool string
	// Server is the MCP server or upstream tool provider id, when known.
	// +optional
	Server string
	// Method is the MCP method or RPC name, when distinct from Tool.
	// +optional
	Method string
	// RequestID correlates gateway logs with agent/runtime traces.
	// +optional
	RequestID string
	// Arguments is the decoded tool-call argument object, evaluated against argument
	// rules. Values are never copied into status/events/logs (redaction invariant).
	// +optional
	Arguments map[string]any
}

// ArgConstraintMatch describes the argument constraint that produced an argument-level
// decision. It carries only policy-defined fields (path, operator, effect, policy
// operands) — never the request's argument value — so it is safe to record in evidence.
type ArgConstraintMatch struct {
	Arg          string
	Op           scrutineerv1alpha1.ArgumentOperator
	Effect       scrutineerv1alpha1.ConstraintEffect
	PolicyValues []string
}

// ToolAuthorization is the gateway-neutral allow/deny outcome for a ToolRequest.
type ToolAuthorization struct {
	enforcement.Evaluation
	// Reason is a machine-readable code (DeniedTools, NotInAllowedTools, Allowed,
	// ArgumentDenied, ArgumentNotAllowed).
	Reason string
	// ArgMatch is set for argument-level decisions to describe the matched constraint
	// (redacted; no request value).
	// +optional
	ArgMatch *ArgConstraintMatch
}

// GatewayConfig is desired gateway configuration derived from session policy.
// Control plane passes this to an injected tool-gateway sidecar (future slice).
type GatewayConfig struct {
	SessionNamespace  string                        `json:"sessionNamespace"`
	SessionName       string                        `json:"sessionName"`
	Mode              scrutineerv1alpha1.PolicyMode `json:"mode"`
	AllowedTools      []string                      `json:"allowedTools,omitempty"`
	DeniedTools       []string                      `json:"deniedTools,omitempty"`
	MaxToolCalls      *int32                        `json:"maxToolCalls,omitempty"`
	MaxCallsPerMinute *int32                        `json:"maxCallsPerMinute,omitempty"`
	RequireApproval   []string                      `json:"requireHumanApproval,omitempty"`
	// ArgumentRules constrain tool calls by their arguments (evaluated per-call).
	ArgumentRules []scrutineerv1alpha1.ToolArgumentRule `json:"argumentRules,omitempty"`
	// ListenHost is the HTTP bind address (127.0.0.1:19090).
	ListenHost string `json:"listenHost"`
	// ListenAddr is the in-pod URL agents should target (contract default).
	ListenAddr string `json:"listenAddr"`
}
