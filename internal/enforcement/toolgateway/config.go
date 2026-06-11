/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package toolgateway

import (
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/secureai/relay/internal/enforcement"
)

// Env keys propagated to tool-gateway sidecars (AGENT_POLICY_* reuse job builder names).
const (
	EnvListenAddr                  = "RELAY_TOOL_GATEWAY_LISTEN"
	EnvPolicyAllowedTools          = "AGENT_POLICY_ALLOWED_TOOLS"
	EnvPolicyDeniedTools           = "AGENT_POLICY_DENIED_TOOLS"
	EnvPolicyRequireApproval       = "AGENT_POLICY_REQUIRE_HUMAN_APPROVAL"
	EnvPolicyMaxToolCalls          = "AGENT_POLICY_MAX_TOOL_CALLS"
	EnvPolicyMaxToolCallsPerMinute = "AGENT_POLICY_MAX_TOOL_CALLS_PER_MINUTE"
	EnvPolicyMode                  = "AGENT_POLICY_MODE"
)

// BuildConfig renders desired gateway configuration for a session, or nil when not applicable.
func BuildConfig(ctx enforcement.SessionContext) *GatewayConfig {
	if !Applicable(ctx) {
		return nil
	}
	return &GatewayConfig{
		SessionNamespace:  ctx.SessionNamespace,
		SessionName:       ctx.SessionName,
		Mode:              ctx.Mode,
		AllowedTools:      append([]string(nil), ctx.Policy.AllowedTools...),
		DeniedTools:       append([]string(nil), ctx.Policy.DeniedTools...),
		RequireApproval:   append([]string(nil), ctx.Policy.RequireHumanApproval...),
		MaxToolCalls:      ctx.Policy.MaxToolCalls,
		MaxCallsPerMinute: ctx.Policy.MaxCallsPerMinute,
		ListenHost:        DefaultListenHost,
		ListenAddr:        DefaultListenAddr,
	}
}

// EnvForConfig returns sidecar env vars for a gateway configuration.
func EnvForConfig(cfg *GatewayConfig) []corev1.EnvVar {
	if cfg == nil {
		return nil
	}
	env := []corev1.EnvVar{
		{Name: EnvListenAddr, Value: cfg.ListenHost},
		{Name: EnvPolicyMode, Value: string(cfg.Mode)},
		{Name: EnvPolicyAllowedTools, Value: csv(cfg.AllowedTools)},
		{Name: EnvPolicyDeniedTools, Value: csv(cfg.DeniedTools)},
		{Name: EnvPolicyRequireApproval, Value: csv(cfg.RequireApproval)},
	}
	if cfg.MaxToolCalls != nil {
		env = append(env, corev1.EnvVar{Name: EnvPolicyMaxToolCalls, Value: strconv.FormatInt(int64(*cfg.MaxToolCalls), 10)})
	}
	if cfg.MaxCallsPerMinute != nil {
		env = append(env, corev1.EnvVar{Name: EnvPolicyMaxToolCallsPerMinute, Value: strconv.FormatInt(int64(*cfg.MaxCallsPerMinute), 10)})
	}
	return env
}

func csv(in []string) string {
	return strings.Join(in, ",")
}
