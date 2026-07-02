/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package dnsproxy defines the control-plane contract for DNS/egress proxy sidecars.
//
// Phase 3 slice 7: policy evaluation, sidecar configuration env, runtime reporting,
// and the first-party dns-proxy sidecar binary (`cmd/dns-proxy`, `Dockerfile.dns-proxy`).
package dnsproxy

import (
	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/enforcement"
)

// EnforcementType is the RuntimeProfile enforcement entry type for the dns-proxy in-pod sidecar.
const EnforcementType = "dns-proxy"

// DefaultBindAddr is the in-pod listen address for the egress proxy.
const DefaultBindAddr = "127.0.0.1:15053"

// DefaultHTTPProxyURL is the HTTP(S) proxy URL agents should use when a dns-proxy sidecar is injected.
const DefaultHTTPProxyURL = "http://127.0.0.1:15053"

// Env keys propagated to dns-proxy sidecars (AGENT_POLICY_* reuse job builder names).
const (
	EnvBindAddr             = "SCRUTINEER_EGRESS_PROXY_LISTEN"
	EnvHTTPProxyURL         = "SCRUTINEER_EGRESS_PROXY_HTTP"
	EnvPolicyAllowedDomains = "AGENT_POLICY_ALLOWED_DOMAINS"
	EnvPolicyDeniedDomains  = "AGENT_POLICY_DENIED_DOMAINS"
	EnvPolicyAllowedCIDRs   = "AGENT_POLICY_ALLOWED_CIDRS"
	EnvPolicyDeniedCIDRs    = "AGENT_POLICY_DENIED_CIDRS"
	EnvPolicyMode           = "AGENT_POLICY_MODE"
)

// EgressRequest is metadata for a single outbound connection observed at the proxy.
type EgressRequest struct {
	// Host is a domain name or IP address.
	Host string
	// Port is the destination port when known.
	// +optional
	Port int32
}

// EgressAuthorization is the proxy-neutral allow/deny outcome for an egress request.
type EgressAuthorization struct {
	enforcement.Evaluation
	// Reason is a machine-readable code (DeniedDomains, NotInAllowedDomains, etc.).
	Reason string
}

// ProxyConfig is desired egress-proxy configuration derived from session policy.
type ProxyConfig struct {
	SessionNamespace string                        `json:"sessionNamespace"`
	SessionName      string                        `json:"sessionName"`
	Mode             scrutineerv1alpha1.PolicyMode `json:"mode"`
	AllowedDomains   []string                      `json:"allowedDomains,omitempty"`
	DeniedDomains    []string                      `json:"deniedDomains,omitempty"`
	AllowedCIDRs     []string                      `json:"allowedCIDRs,omitempty"`
	DeniedCIDRs      []string                      `json:"deniedCIDRs,omitempty"`
	BindAddr         string                        `json:"bindAddr"`
	HTTPProxyURL     string                        `json:"httpProxyURL"`
}

// RuntimeEvent is the sidecar → control-plane report payload (JSON-serializable).
// Sidecars POST or patch status through a future reporter; controllers call RuntimeReportFromEvent.
type RuntimeEvent struct {
	Host string `json:"host"`
	Port int32  `json:"port,omitempty"`
}
