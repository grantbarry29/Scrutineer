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

// ApprovalRequirement describes how many distinct approvers a gated action needs.
//
// +kubebuilder:validation:Enum=default;allOf
type ApprovalRequirement string

const (
	// ApprovalRequirementDefault requires a single approver.
	ApprovalRequirementDefault ApprovalRequirement = "default"
	// ApprovalRequirementAllOf requires every listed approver (reserved; gate slice may defer).
	ApprovalRequirementAllOf ApprovalRequirement = "allOf"
)

// ApprovalTimeoutAction is the outcome when no decision arrives before expiry.
//
// +kubebuilder:validation:Enum=deny;allow
type ApprovalTimeoutAction string

const (
	// ApprovalTimeoutDeny denies the gated session on timeout (safe default).
	ApprovalTimeoutDeny ApprovalTimeoutAction = "deny"
	// ApprovalTimeoutAllow allows the gated session on timeout (audit-only escape hatch).
	ApprovalTimeoutAllow ApprovalTimeoutAction = "allow"
)

// ApprovalSubjectKind enumerates the kinds of subject that may approve a request.
//
// +kubebuilder:validation:Enum=User;Group;ServiceAccount
type ApprovalSubjectKind string

const (
	ApprovalSubjectUser           ApprovalSubjectKind = "User"
	ApprovalSubjectGroup          ApprovalSubjectKind = "Group"
	ApprovalSubjectServiceAccount ApprovalSubjectKind = "ServiceAccount"
)

// ApprovalSubject identifies who may grant approvals under an ApprovalPolicy.
// Advisory in this declarative slice; real enforcement (RBAC + webhook) lands
// with the controller gate (Phase 5 slice 3).
type ApprovalSubject struct {
	// Kind of subject.
	Kind ApprovalSubjectKind `json:"kind"`

	// Name of the subject (user name, group name, or ServiceAccount name).
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

// ApprovalPolicySpec defines reusable, scoped human-approval rules for AgentSessions.
// Declarative only in this slice — no controller gate yet (see
// docs/design/phase-5-approval-workflows.md).
type ApprovalPolicySpec struct {
	// Actions lists the action types this policy gates. These match entries in a
	// session's effective policy.requireHumanApproval (e.g. "deploy",
	// "credential-use") and the action types surfaced by runtime decisions.
	// +kubebuilder:validation:MinItems=1
	Actions []string `json:"actions"`

	// Approvers lists subjects allowed to grant requests created under this policy.
	// +optional
	Approvers []ApprovalSubject `json:"approvers,omitempty"`

	// ExpiresAfter is how long a granted approval stays valid before the session
	// must re-request. Unset means a granted approval does not auto-expire.
	// +optional
	ExpiresAfter *metav1.Duration `json:"expiresAfter,omitempty"`

	// Requirement is how many distinct approvers are required. Defaults to a
	// single approver.
	// +kubebuilder:default=default
	// +optional
	Requirement ApprovalRequirement `json:"requirement,omitempty"`

	// OnTimeout is the outcome when no decision arrives before expiry. Defaults
	// to deny (fail closed).
	// +kubebuilder:default=deny
	// +optional
	OnTimeout ApprovalTimeoutAction `json:"onTimeout,omitempty"`
}

// ApprovalPolicyStatus defines the observed state of an ApprovalPolicy.
type ApprovalPolicyStatus struct {
	// ObservedGeneration is reserved for a future ApprovalPolicy controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// ApprovalPolicy is a reusable, scoped human-approval policy that AgentSessions
// can be gated by. Declarative only until the Phase 5 controller gate ships.
//
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,shortName=appol;approvalpol
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Requirement",type=string,JSONPath=`.spec.requirement`
// +kubebuilder:printcolumn:name="OnTimeout",type=string,JSONPath=`.spec.onTimeout`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type ApprovalPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ApprovalPolicySpec   `json:"spec,omitempty"`
	Status ApprovalPolicyStatus `json:"status,omitempty"`
}

// ApprovalPolicyList contains a list of ApprovalPolicy.
//
// +kubebuilder:object:root=true
type ApprovalPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ApprovalPolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ApprovalPolicy{}, &ApprovalPolicyList{})
}
