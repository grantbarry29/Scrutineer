/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package job builds and compares Kubernetes Job objects for AgentSession runtimes.
package job

// Labels applied to Jobs and Pods owned by an AgentSession.
const (
	LabelAppName      = "app.kubernetes.io/name"
	LabelAppComponent = "app.kubernetes.io/component"
	LabelSessionRef   = "scrutineer.sh/session"

	AppNameScrutineer  = "scrutineer"
	ComponentSession   = "agent-session"
	AgentContainerName = "agent"

	NamePrefix = "scrutineer-session-"

	DefaultWorkspaceMountPath = "/workspace"
)

// Environment variable keys injected into the agent container.
const (
	EnvScrutineerSessionName      = "SCRUTINEER_SESSION_NAME"
	EnvScrutineerSessionNamespace = "SCRUTINEER_SESSION_NAMESPACE"
	EnvTaskDescription            = "AGENT_TASK_DESCRIPTION"
	EnvTaskPrompt                 = "AGENT_TASK_PROMPT"
	EnvModelProvider              = "AGENT_MODEL_PROVIDER"
	EnvModelName                  = "AGENT_MODEL_NAME"
	EnvModelBaseURL               = "AGENT_MODEL_BASE_URL"
	EnvPolicyAllowedDomains       = "AGENT_POLICY_ALLOWED_DOMAINS"
	EnvPolicyDeniedDomains        = "AGENT_POLICY_DENIED_DOMAINS"
	EnvPolicyAllowedCIDRs         = "AGENT_POLICY_ALLOWED_CIDRS"
	EnvPolicyDeniedCIDRs          = "AGENT_POLICY_DENIED_CIDRS"
	EnvPolicyRequireApproval      = "AGENT_POLICY_REQUIRE_HUMAN_APPROVAL"
	EnvPolicyMode                 = "AGENT_POLICY_MODE"
)

// Runtime reporter wiring. Audience must match internal/reporter.TokenAudience.
const (
	ReporterTokenAudience = "scrutineer-reporter"
)
