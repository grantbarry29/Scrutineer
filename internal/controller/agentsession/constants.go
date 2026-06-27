/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package agentsession

// AgentSessionFinalizer blocks AgentSession deletion until the owned Job is removed.
const AgentSessionFinalizer = "relay.secureai.dev/finalizer"

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
	// EventReasonOutputsCollected — terminal session outputs were retained (logs/artifacts).
	EventReasonOutputsCollected = "OutputsCollected"
	// EventReasonOutputsCollectionFailed — output collection was requested but failed (warning).
	EventReasonOutputsCollectionFailed = "OutputsCollectionFailed"
)

// Orchestrator values supported by Relay.
const (
	OrchestratorKubernetesJob = "kubernetes-job"
	OrchestratorKubernetesPod = "kubernetes-pod"
)
