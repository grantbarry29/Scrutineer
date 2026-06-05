/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package v1alpha1

// PolicyMode describes how policy should be interpreted at runtime.
// Phase 2 records mode in status; Phase 3 enforcement backends act on it.
//
// +kubebuilder:validation:Enum=audit-only;dry-run;enforced
type PolicyMode string

const (
	PolicyModeAuditOnly PolicyMode = "audit-only"
	PolicyModeDryRun    PolicyMode = "dry-run"
	PolicyModeEnforced  PolicyMode = "enforced"
)

// PolicyRules are reusable governance rule fields shared by inline session policy,
// AgentPolicy, and status.effectivePolicy.
type PolicyRules struct {
	// AllowedDomains is an FQDN allowlist for outbound network access.
	// +optional
	AllowedDomains []string `json:"allowedDomains,omitempty"`

	// DeniedDomains is an FQDN denylist for outbound network access.
	// +optional
	DeniedDomains []string `json:"deniedDomains,omitempty"`

	// AllowedCIDRs is an IP/CIDR allowlist for outbound network access.
	// +optional
	AllowedCIDRs []string `json:"allowedCIDRs,omitempty"`

	// DeniedCIDRs is an IP/CIDR denylist for outbound network access.
	// +optional
	DeniedCIDRs []string `json:"deniedCIDRs,omitempty"`

	// AllowedTools lists tool identifiers the agent is permitted to invoke.
	// +optional
	AllowedTools []string `json:"allowedTools,omitempty"`

	// DeniedTools lists tool identifiers the agent must not invoke.
	// +optional
	DeniedTools []string `json:"deniedTools,omitempty"`

	// RequireHumanApproval lists action types that require human approval before execution.
	// +optional
	RequireHumanApproval []string `json:"requireHumanApproval,omitempty"`

	// MaxNetworkRequests caps the total number of network requests the agent may issue.
	// +kubebuilder:validation:Minimum=0
	// +optional
	MaxNetworkRequests *int32 `json:"maxNetworkRequests,omitempty"`

	// MaxToolCalls caps the total number of tool calls the agent may issue.
	// +kubebuilder:validation:Minimum=0
	// +optional
	MaxToolCalls *int32 `json:"maxToolCalls,omitempty"`
}

// PolicyRef references a reusable policy CRD in the same namespace as the AgentSession.
type PolicyRef struct {
	// Kind is the policy resource kind. MVP supports AgentPolicy only.
	// +kubebuilder:validation:Enum=AgentPolicy
	// +kubebuilder:default=AgentPolicy
	// +optional
	Kind string `json:"kind,omitempty"`

	// Name is the policy resource name in the AgentSession namespace.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

// MatchedPolicyRef records a policy CRD that contributed to the effective policy.
type MatchedPolicyRef struct {
	// Kind is the policy resource kind.
	Kind string `json:"kind"`

	// Name is the policy resource name.
	Name string `json:"name"`

	// UID is the policy object UID at resolution time.
	UID string `json:"uid,omitempty"`

	// Generation is the policy generation at resolution time.
	// +optional
	Generation int64 `json:"generation,omitempty"`

	// Mode is the mode declared on the matched policy.
	// +optional
	Mode PolicyMode `json:"mode,omitempty"`
}

// EffectivePolicyStatus is the merged policy the controller propagated to the runtime.
type EffectivePolicyStatus struct {
	// Mode is the strictest mode across matched policies (enforced > dry-run > audit-only).
	// +optional
	Mode PolicyMode `json:"mode,omitempty"`

	// Inline policy rules merged from policyRefs and spec.policy overrides.
	PolicyRules `json:",inline"`
}
