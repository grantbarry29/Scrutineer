/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package workspace

import (
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/enforcement"
)

// Env keys for fs-gateway sidecars (mirrors job builder AGENT_POLICY_* names).
const (
	EnvBindAddr                = "SCRUTINEER_FS_GATEWAY_LISTEN"
	EnvPolicyAllowedPaths      = "AGENT_POLICY_ALLOWED_PATHS"
	EnvPolicyDeniedPaths       = "AGENT_POLICY_DENIED_PATHS"
	EnvPolicyMaxWorkspaceBytes = "AGENT_POLICY_MAX_WORKSPACE_BYTES"
	EnvPolicyMode              = "AGENT_POLICY_MODE"
)

// GatewayConfig is desired FS gateway configuration for a session.
type GatewayConfig struct {
	SessionNamespace  string
	SessionName       string
	Mode              scrutineerv1alpha1.PolicyMode
	AllowedPaths      []string
	DeniedPaths       []string
	MaxWorkspaceBytes *int64
	BindAddr          string
	InPodURL          string
}

// BuildConfig renders desired gateway configuration, or nil when not applicable.
func BuildConfig(ctx enforcement.SessionContext) *GatewayConfig {
	if !Applicable(ctx) {
		return nil
	}
	return &GatewayConfig{
		SessionNamespace:  ctx.SessionNamespace,
		SessionName:       ctx.SessionName,
		Mode:              ctx.Mode,
		AllowedPaths:      append([]string(nil), ctx.Policy.AllowedPaths...),
		DeniedPaths:       append([]string(nil), ctx.Policy.DeniedPaths...),
		MaxWorkspaceBytes: ctx.Policy.MaxWorkspaceBytes,
		BindAddr:          DefaultBindAddr,
		InPodURL:          DefaultInPodURL,
	}
}

// EnvForConfig returns env vars for a gateway configuration.
func EnvForConfig(cfg *GatewayConfig) []corev1.EnvVar {
	if cfg == nil {
		return nil
	}
	env := []corev1.EnvVar{
		{Name: EnvBindAddr, Value: cfg.BindAddr},
		{Name: EnvPolicyMode, Value: string(cfg.Mode)},
		{Name: EnvPolicyAllowedPaths, Value: csv(cfg.AllowedPaths)},
		{Name: EnvPolicyDeniedPaths, Value: csv(cfg.DeniedPaths)},
	}
	if cfg.MaxWorkspaceBytes != nil {
		env = append(env, corev1.EnvVar{
			Name:  EnvPolicyMaxWorkspaceBytes,
			Value: strconv.FormatInt(*cfg.MaxWorkspaceBytes, 10),
		})
	}
	return env
}

func csv(in []string) string {
	return strings.Join(in, ",")
}
