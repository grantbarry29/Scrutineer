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
	// EventReasonApprovalNotEnforced — requireHumanApproval declared but MVP does not gate execution.
	EventReasonApprovalNotEnforced = "ApprovalNotEnforced"
	// EventReasonPolicyResolved — referenced policies merged into effective policy.
	EventReasonPolicyResolved         = "PolicyResolved"
	EventReasonRuntimeProfileResolved = "RuntimeProfileResolved"
	EventReasonPolicyEnvDrift         = "PolicyEnvDrift"
	EventReasonPolicyEnvSynced        = "PolicyEnvSynced"
	// EventReasonNetworkPolicySynced — owned NetworkPolicy was created or updated for CIDR enforcement.
	EventReasonNetworkPolicySynced = "NetworkPolicySynced"
)

// Orchestrator values supported by Relay.
const (
	OrchestratorKubernetesJob = "kubernetes-job"
)
