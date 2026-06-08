/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package controller

// Labels applied to objects owned by an AgentSession.
const (
	LabelAppName      = "app.kubernetes.io/name"
	LabelAppComponent = "app.kubernetes.io/component"
	LabelSessionRef   = "relay.secureai.dev/session"

	AppNameRelay       = "relay"
	ComponentSession   = "agent-session"
	AgentContainerName = "agent"

	JobNamePrefix = "relay-session-"

	DefaultWorkspaceMountPath = "/workspace"

	// AgentSessionFinalizer blocks AgentSession deletion until the owned Job is removed.
	AgentSessionFinalizer = "relay.secureai.dev/finalizer"
)

// Condition types used on AgentSession.status.conditions.
const (
	ConditionValidated        = "Validated"
	ConditionPolicyResolved   = "PolicyResolved"
	ConditionPolicyPropagated = "PolicyPropagated"
	ConditionRuntimeCreated   = "RuntimeCreated"
	ConditionCompleted        = "Completed"
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
	EventReasonPolicyResolved  = "PolicyResolved"
	EventReasonPolicyEnvDrift  = "PolicyEnvDrift"
	EventReasonPolicyEnvSynced = "PolicyEnvSynced"
)

// Orchestrator values supported by Relay.
const (
	OrchestratorKubernetesJob = "kubernetes-job"
	// OrchestratorTekton, OrchestratorArgo, OrchestratorTemporal will be added once
	// dispatcher plumbing is in place. The Reconciler currently rejects anything but
	// kubernetes-job at validation time.
)

// Environment variable keys injected into the agent container.
const (
	EnvRelaySessionName      = "RELAY_SESSION_NAME"
	EnvRelaySessionNamespace = "RELAY_SESSION_NAMESPACE"
	EnvTaskDescription       = "AGENT_TASK_DESCRIPTION"
	EnvTaskPrompt            = "AGENT_TASK_PROMPT"
	EnvModelProvider         = "AGENT_MODEL_PROVIDER"
	EnvModelName             = "AGENT_MODEL_NAME"
	EnvPolicyAllowedDomains  = "AGENT_POLICY_ALLOWED_DOMAINS"
	EnvPolicyDeniedDomains   = "AGENT_POLICY_DENIED_DOMAINS"
	EnvPolicyAllowedCIDRs    = "AGENT_POLICY_ALLOWED_CIDRS"
	EnvPolicyDeniedCIDRs     = "AGENT_POLICY_DENIED_CIDRS"
	EnvPolicyAllowedTools    = "AGENT_POLICY_ALLOWED_TOOLS"
	EnvPolicyDeniedTools     = "AGENT_POLICY_DENIED_TOOLS"
	EnvPolicyRequireApproval = "AGENT_POLICY_REQUIRE_HUMAN_APPROVAL"
	EnvPolicyMaxNetReqs      = "AGENT_POLICY_MAX_NETWORK_REQUESTS"
	EnvPolicyMaxToolCalls    = "AGENT_POLICY_MAX_TOOL_CALLS"
	EnvPolicyMode            = "AGENT_POLICY_MODE"
)
