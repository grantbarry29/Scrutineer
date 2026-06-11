/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package toolgateway defines the control-plane contract and first-party tool-gateway
// sidecar (`cmd/tool-gateway`, `Dockerfile.tool-gateway`) for MCP/tool-call governance.
package toolgateway

import (
	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
	"github.com/secureai/relay/internal/enforcement"
)

// SidecarType is the RuntimeProfile sidecar type for tool gateways.
const SidecarType = "tool-gateway"

// ToolRequest is metadata for a single tool invocation observed at the gateway.
type ToolRequest struct {
	// Tool is the stable tool identifier (MCP tool name or Relay tool id).
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
}

// ToolAuthorization is the gateway-neutral allow/deny outcome for a ToolRequest.
type ToolAuthorization struct {
	enforcement.Evaluation
	// Reason is a machine-readable code (DeniedTools, NotInAllowedTools, Allowed).
	Reason string
}

// GatewayConfig is desired gateway configuration derived from session policy.
// Control plane passes this to an injected tool-gateway sidecar (future slice).
type GatewayConfig struct {
	SessionNamespace  string                   `json:"sessionNamespace"`
	SessionName       string                   `json:"sessionName"`
	Mode              relayv1alpha1.PolicyMode `json:"mode"`
	AllowedTools      []string                 `json:"allowedTools,omitempty"`
	DeniedTools       []string                 `json:"deniedTools,omitempty"`
	MaxToolCalls      *int32                   `json:"maxToolCalls,omitempty"`
	MaxCallsPerMinute *int32                   `json:"maxCallsPerMinute,omitempty"`
	RequireApproval   []string                 `json:"requireHumanApproval,omitempty"`
	// ListenHost is the HTTP bind address (127.0.0.1:19090).
	ListenHost string `json:"listenHost"`
	// ListenAddr is the in-pod URL agents should target (contract default).
	ListenAddr string `json:"listenAddr"`
}
