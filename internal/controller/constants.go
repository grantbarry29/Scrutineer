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
	ConditionValidated      = "Validated"
	ConditionRuntimeCreated = "RuntimeCreated"
	ConditionCompleted      = "Completed"
)

// Event reasons emitted by the controller.
const (
	EventReasonValidationFailed = "ValidationFailed"
	EventReasonJobCreated       = "JobCreated"
	EventReasonJobRunning       = "JobRunning"
	EventReasonJobSucceeded     = "JobSucceeded"
	EventReasonJobFailed        = "JobFailed"
	EventReasonSessionDenied    = "SessionDenied"
	EventReasonSessionCancelled = "SessionCancelled"
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
)
