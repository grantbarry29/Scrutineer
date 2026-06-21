/*
Copyright 2026 The Relay Authors.

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

// ApprovalScope bounds what is being approved so a grant is not an open-ended
// blanket allowance.
type ApprovalScope struct {
	// Target is the entity being approved (deploy target, domain, tool, path).
	// +optional
	Target string `json:"target,omitempty"`

	// Window is how long the approval remains valid once granted. Unset means
	// it does not auto-expire after grant.
	// +optional
	Window *metav1.Duration `json:"window,omitempty"`
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

	// PolicyRef is the ApprovalPolicy that triggered this request, if any.
	// +optional
	PolicyRef string `json:"policyRef,omitempty"`

	// Action is the gated action type (e.g. "deploy").
	// +kubebuilder:validation:MinLength=1
	Action string `json:"action"`

	// Scope bounds what is being approved.
	// +optional
	Scope ApprovalScope `json:"scope,omitempty"`

	// Decision is the human decision. Empty means pending. This is the only
	// field approvers are expected to mutate (RBAC + future webhook enforce it).
	// +kubebuilder:default=""
	// +optional
	Decision ApprovalDecision `json:"decision,omitempty"`
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
