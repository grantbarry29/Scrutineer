/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package agentsession

// AgentSessionFinalizer blocks AgentSession deletion until the owned Job is removed.
const AgentSessionFinalizer = "scrutineer.sh/finalizer"

// Condition types used on AgentSession.status.conditions.
const (
	ConditionValidated              = "Validated"
	ConditionPolicyResolved         = "PolicyResolved"
	ConditionPolicyPropagated       = "PolicyPropagated"
	ConditionRuntimeProfileResolved = "RuntimeProfileResolved"
	ConditionRuntimeCreated         = "RuntimeCreated"
	ConditionApprovalRequired       = "ApprovalRequired"
	ConditionCompleted              = "Completed"
	ConditionReady                  = "Ready"
	// ConditionEgressLockVerified — whether the differential probe has proven the
	// CNI enforces NetworkPolicy (the routing lock's substrate). False holds
	// enforced-mode sessions before runtime creation (verified-or-refused, #70).
	ConditionEgressLockVerified = "EgressLockVerified"
	// ConditionEgressProxyHealthy — whether the session's egress-proxy pod is
	// provisioned and not Failed (#99). "Healthy" here means "not kubelet-failed";
	// it is not a liveness probe. False surfaces a dead chokepoint: the routing
	// lock keeps the agent fail-closed (no egress), but silent-and-permanent is
	// not acceptable (dev-agent-rules/distributed-systems-networking.md rule 35).
	ConditionEgressProxyHealthy = "EgressProxyHealthy"
)

// Reasons used on ConditionEgressProxyHealthy.
const (
	ReasonProxyProvisioned = "ProxyProvisioned"
	// ReasonProxyEvidenceOverflow — the kubelet evicted the proxy pod because the
	// access-log emptyDir exceeded its size limit. Deliberately NOT auto-recreated:
	// a fresh evidence volume after a flood is exactly what a hostile agent wants
	// (#98/#99); the session stays fail-closed until a human intervenes.
	ReasonProxyEvidenceOverflow = "EvidenceVolumeOverflow"
	// ReasonProxyPodFailed — the proxy pod failed for a cause unrelated to the
	// evidence volume (node pressure, preemption); the controller replaces it.
	ReasonProxyPodFailed = "ProxyPodFailed"
)

// Reasons used on ConditionEgressLockVerified.
const (
	ReasonLockVerified          = "LockVerified"
	ReasonCNINotEnforcing       = "CNIDoesNotEnforceNetworkPolicy"
	ReasonLockProbeInconclusive = "ProbeInconclusive"
)

// Event reasons emitted by the controller (Kubernetes Events on AgentSession).
const (
	// EventReasonValidationFailed — spec failed validateSpec; session denied.
	EventReasonValidationFailed = "ValidationFailed"
	// EventReasonJobCreated — owned Job was created for this session.
	EventReasonJobCreated = "JobCreated"
	// EventReasonJobRunning — Job has active pods; session phase Running.
	EventReasonJobRunning = "JobRunning"
	// EventReasonJobSucceeded — Job completed successfully.
	EventReasonJobSucceeded = "JobSucceeded"
	// EventReasonJobFailed — Job failed or timed out (warning).
	EventReasonJobFailed = "JobFailed"
	// EventReasonSessionDenied — session denied (validation, task resolution, Job conflict).
	EventReasonSessionDenied = "SessionDenied"
	// EventReasonSessionCancelled — user set cancelRequested; Job stopped.
	EventReasonSessionCancelled = "SessionCancelled"
	// EventReasonApprovalNotEnforced — requireHumanApproval declared but no ApprovalPolicy gates it.
	EventReasonApprovalNotEnforced = "ApprovalNotEnforced"
	// EventReasonApprovalRequested — a human approval gate is open; session is AwaitingApproval.
	EventReasonApprovalRequested = "ApprovalRequested"
	// EventReasonApprovalGranted — approval was granted; session may proceed.
	EventReasonApprovalGranted = "ApprovalGranted"
	// EventReasonApprovalDenied — approval was denied or expired; session denied.
	EventReasonApprovalDenied = "ApprovalDenied"
	// EventReasonApprovalNotified — approvers were notified of an open gate.
	EventReasonApprovalNotified = "ApprovalNotified"
	// EventReasonEgressLockUnverified — enforced session held: the NetworkPolicy
	// routing lock's enforcement is unverified/refused on this cluster (warning).
	EventReasonEgressLockUnverified = "EgressLockUnverified"
	// EventReasonApprovalNotifyFailed — notification delivery failed (warning; will retry).
	EventReasonApprovalNotifyFailed = "ApprovalNotifyFailed"
	// EventReasonApprovalUnauthorized — a grant was set by a subject not in the policy's approvers (warning; not honored).
	EventReasonApprovalUnauthorized = "ApprovalUnauthorized"
	// EventReasonApprovalPartial — an allOf gate received a valid grant but still needs more approvers.
	EventReasonApprovalPartial = "ApprovalPartiallyApproved"
	// EventReasonPolicyResolved — referenced policies merged into effective policy.
	EventReasonPolicyResolved         = "PolicyResolved"
	EventReasonRuntimeProfileResolved = "RuntimeProfileResolved"
	EventReasonPolicyEnvDrift         = "PolicyEnvDrift"
	EventReasonPolicyEnvSynced        = "PolicyEnvSynced"
	// EventReasonNetworkPolicySynced — owned NetworkPolicy was created or updated for CIDR enforcement.
	EventReasonNetworkPolicySynced = "NetworkPolicySynced"
	// EventReasonEgressProxySynced — a per-session Envoy egress proxy object was created.
	EventReasonEgressProxySynced = "EgressProxySynced"
	// EventReasonEgressProxyFailed — the egress-proxy pod failed in place (warning);
	// the message names the kubelet's eviction reason and whether it is replaced (#99).
	EventReasonEgressProxyFailed = "EgressProxyFailed"
	// EventReasonOutputsCollected — terminal session outputs were retained (logs/artifacts).
	EventReasonOutputsCollected = "OutputsCollected"
	// EventReasonOutputsCollectionFailed — output collection was requested but failed (warning).
	EventReasonOutputsCollectionFailed = "OutputsCollectionFailed"
)

// Orchestrator values supported by Scrutineer.
const (
	OrchestratorKubernetesJob = "kubernetes-job"
	OrchestratorKubernetesPod = "kubernetes-pod"
)
