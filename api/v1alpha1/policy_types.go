/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// PolicyDecisionPhase distinguishes when a decision was recorded.
//
// +kubebuilder:validation:Enum=merge;runtime
type PolicyDecisionPhase string

const (
	PolicyDecisionPhaseMerge   PolicyDecisionPhase = "merge"
	PolicyDecisionPhaseRuntime PolicyDecisionPhase = "runtime"
)

// PolicyDecisionAction is the outcome of a policy evaluation.
//
// +kubebuilder:validation:Enum=allow;deny;audit;dry-run
type PolicyDecisionAction string

const (
	PolicyDecisionAllow  PolicyDecisionAction = "allow"
	PolicyDecisionDeny   PolicyDecisionAction = "deny"
	PolicyDecisionAudit  PolicyDecisionAction = "audit"
	PolicyDecisionDryRun PolicyDecisionAction = "dry-run"
)

// EvidenceAssurance describes how trustworthy an evidence record is, based on
// who produced it relative to the agent being governed. It lets audit and UI
// consumers tell authoritative or independently-observed evidence apart from
// cooperative, self-reported evidence that a compromised agent could forge.
//
// +kubebuilder:validation:Enum=controller;self-reported;observed
type EvidenceAssurance string

const (
	// EvidenceControllerComputed is authoritative evidence the control plane
	// computed itself (e.g. merge-time policy decisions). The agent cannot
	// influence it.
	EvidenceControllerComputed EvidenceAssurance = "controller"
	// EvidenceSelfReported is cooperative evidence reported by a data-plane
	// component that shares a pod and ServiceAccount with the agent (today's
	// sidecars). A fully compromised agent could forge or suppress it, so the
	// controller-side reporter always stamps incoming runtime evidence with
	// this level rather than trusting a client-supplied value.
	EvidenceSelfReported EvidenceAssurance = "self-reported"
	// EvidenceObserved is independently-observed evidence from a source the
	// agent cannot tamper with (e.g. kernel/eBPF or out-of-pod enforcement).
	// Reserved: the cooperative reporter never assigns it.
	EvidenceObserved EvidenceAssurance = "observed"
)

// PolicyDecision records a structured policy evaluation for audit and future UI timelines.
// Phase 2 populates merge-time decisions; Phase 3 enforcement backends append runtime entries.
type PolicyDecision struct {
	// Time is when the decision was recorded.
	Time metav1.Time `json:"time"`

	// Phase is merge (control-plane resolution) or runtime (enforcement observation).
	Phase PolicyDecisionPhase `json:"phase"`

	// Type categorizes the decision: mode, policy, network, tool, approval, cap.
	Type string `json:"type"`

	// Action is allow, deny, audit, or dry-run (would-deny under dry-run mode).
	Action PolicyDecisionAction `json:"action"`

	// Actor is who or what produced the decision. Merge-time uses "scrutineer-controller".
	// +optional
	Actor string `json:"actor,omitempty"`

	// Target is the entity evaluated (domain, tool, approval reason, cap name).
	// +optional
	Target string `json:"target,omitempty"`

	// Reason is a short machine-readable code for filtering (e.g. DeniedTools, StrictestMode).
	Reason string `json:"reason"`

	// Message is a human-readable description.
	Message string `json:"message"`

	// Mode is the effective policy mode when the decision was made.
	// +optional
	Mode PolicyMode `json:"mode,omitempty"`

	// PolicyRef identifies the matched policy CRD when applicable.
	// +optional
	PolicyRef *MatchedPolicyRef `json:"policyRef,omitempty"`

	// Rule is the policy rule field that produced the decision (e.g. deniedDomains).
	// +optional
	Rule string `json:"rule,omitempty"`

	// AssuranceLevel indicates how trustworthy this decision record is.
	// Merge-phase decisions are "controller" (authoritative). Runtime decisions
	// reported by cooperative sidecars are stamped "self-reported" by the
	// controller. An empty value on a runtime decision should be treated as
	// "self-reported" by consumers (legacy records predate this field).
	// +optional
	AssuranceLevel EvidenceAssurance `json:"assuranceLevel,omitempty"`
}

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

	// RequireHumanApproval lists action types that require human approval before execution.
	// +optional
	RequireHumanApproval []string `json:"requireHumanApproval,omitempty"`
}

// PolicyRef references a reusable policy CRD in the same namespace as the AgentSession.
type PolicyRef struct {
	// Kind is the policy resource kind. AgentPolicy is the only kind today; tool/file
	// policy kinds return with their out-of-pod chokepoints (the untamperable pivot,
	// docs/design/untamperable-pivot.md).
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
