/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ApprovalDecision is the human-set decision on an ApprovalRequest. It is the
// only spec field approvers mutate; the controller never writes it.
//
// +kubebuilder:validation:Enum="";granted;denied
type ApprovalDecision string

const (
	// ApprovalDecisionPending is the unset/initial decision (awaiting a human).
	ApprovalDecisionPending ApprovalDecision = ""
	// ApprovalDecisionGranted allows the gated session to proceed.
	ApprovalDecisionGranted ApprovalDecision = "granted"
	// ApprovalDecisionDenied terminally denies the gated session.
	ApprovalDecisionDenied ApprovalDecision = "denied"
)

// ApprovalState is the controller-observed lifecycle state of an ApprovalRequest.
//
// +kubebuilder:validation:Enum=Pending;Granted;Denied;Expired
type ApprovalState string

const (
	ApprovalStatePending ApprovalState = "Pending"
	ApprovalStateGranted ApprovalState = "Granted"
	ApprovalStateDenied  ApprovalState = "Denied"
	ApprovalStateExpired ApprovalState = "Expired"
)

// ApprovalTrigger distinguishes a pre-execution session gate from a
// mid-execution per-tool-call hold. See docs/design/phase-5-runtime-tool-approval.md.
//
// +kubebuilder:validation:Enum=session;runtime
type ApprovalTrigger string

const (
	// ApprovalTriggerSession gates whether the AgentSession may start at all
	// (pre-execution). This is the default and the original behavior.
	ApprovalTriggerSession ApprovalTrigger = "session"
	// ApprovalTriggerRuntime holds a specific tool/MCP call mid-execution until a
	// scoped, time-bounded human grant. The controller resolves its lifecycle
	// without gating the session phase.
	ApprovalTriggerRuntime ApprovalTrigger = "runtime"
)

// ApprovalScope bounds what is being approved so a grant is not an open-ended
// blanket allowance.
type ApprovalScope struct {
	// Target is the entity being approved (deploy target, domain, tool, path).
	// +optional
	Target string `json:"target,omitempty"`

	// Window is how long the approval remains valid once granted. Unset means
	// it does not auto-expire after grant. For runtime holds with no matching
	// ApprovalPolicy, this supplies the post-grant validity window.
	// +optional
	Window *metav1.Duration `json:"window,omitempty"`

	// ArgDigest is a redacted fingerprint (e.g. sha256) of the tool-call
	// arguments a runtime approval is scoped to. It NEVER carries raw argument
	// values — only a digest — so evidence stays redaction-safe. Empty for
	// session gates.
	// +optional
	ArgDigest string `json:"argDigest,omitempty"`
}

// ApprovalSessionRef references the AgentSession this request gates (same namespace).
type ApprovalSessionRef struct {
	// Name of the AgentSession.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

// ApprovalRequestSpec is created by the controller when a gated AgentSession
// needs human approval. Approvers act on it by setting spec.decision.
type ApprovalRequestSpec struct {
	// SessionRef is the AgentSession this request gates.
	SessionRef ApprovalSessionRef `json:"sessionRef"`

	// Trigger distinguishes a pre-execution session gate (default) from a
	// mid-execution per-tool-call hold. Empty is treated as "session" for
	// backward compatibility.
	// +kubebuilder:default=session
	// +optional
	Trigger ApprovalTrigger `json:"trigger,omitempty"`

	// RequestID correlates a runtime hold with the data-plane call that raised it
	// and is the idempotency key the reporter uses to avoid creating duplicate
	// requests for the same tool call. Empty for session gates.
	// +optional
	RequestID string `json:"requestId,omitempty"`

	// PolicyRef is the ApprovalPolicy that triggered this request, if any.
	// +optional
	PolicyRef string `json:"policyRef,omitempty"`

	// Action is the gated action type (e.g. "deploy").
	// +kubebuilder:validation:MinLength=1
	Action string `json:"action"`

	// Scope bounds what is being approved.
	// +optional
	Scope ApprovalScope `json:"scope,omitempty"`

	// Decision is the human decision. Empty means pending. This is the
	// primary field approvers mutate (RBAC + future webhook enforce it).
	// +kubebuilder:default=""
	// +optional
	Decision ApprovalDecision `json:"decision,omitempty"`

	// DecidedBy is the approver's self-declared identity, set alongside Decision.
	// It is best-effort and NOT authenticated — the real gate is RBAC on who may
	// patch this object; authenticated capture needs a future validating webhook.
	// When the matching ApprovalPolicy lists approvers, a grant is only honored if
	// DecidedBy matches a listed approver name.
	// +optional
	DecidedBy string `json:"decidedBy,omitempty"`
}

// ApprovalRequestStatus is owned by the controller.
type ApprovalRequestStatus struct {
	// State is the observed lifecycle state.
	// +optional
	State ApprovalState `json:"state,omitempty"`

	// DecidedBy records the subject that set the decision (best-effort until a
	// validating webhook captures userInfo).
	// +optional
	DecidedBy string `json:"decidedBy,omitempty"`

	// ApprovedBy is the set of distinct approver identities that have granted this
	// request. It is only populated for allOf policies (multi-approver): the gate
	// opens once it covers every listed approver. Controller-owned and best-effort
	// (entries come from the self-declared spec.decidedBy of each grant).
	// +optional
	// +listType=set
	ApprovedBy []string `json:"approvedBy,omitempty"`

	// DecidedAt is when the decision was observed.
	// +optional
	DecidedAt *metav1.Time `json:"decidedAt,omitempty"`

	// ExpiresAt is when a granted approval stops being valid (DecidedAt + window).
	// +optional
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty"`

	// ObservedGeneration is the last generation reconciled.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Reason is a short human-readable explanation of the current state.
	// +optional
	Reason string `json:"reason,omitempty"`
}

// ApprovalRequest is a per-decision, controller-owned object a human grants or
// denies to gate an AgentSession. See docs/design/phase-5-approval-workflows.md.
//
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,shortName=appreq;approvalreq
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Session",type=string,JSONPath=`.spec.sessionRef.name`
// +kubebuilder:printcolumn:name="Action",type=string,JSONPath=`.spec.action`
// +kubebuilder:printcolumn:name="State",type=string,JSONPath=`.status.state`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type ApprovalRequest struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ApprovalRequestSpec   `json:"spec,omitempty"`
	Status ApprovalRequestStatus `json:"status,omitempty"`
}

// IsRuntime reports whether this request holds a single mid-execution tool call
// (vs. gating session start). Empty trigger means session for backward compat.
func (s ApprovalRequestSpec) IsRuntime() bool {
	return s.Trigger == ApprovalTriggerRuntime
}

// ApprovalRequestList contains a list of ApprovalRequest.
//
// +kubebuilder:object:root=true
type ApprovalRequestList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ApprovalRequest `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ApprovalRequest{}, &ApprovalRequestList{})
}
