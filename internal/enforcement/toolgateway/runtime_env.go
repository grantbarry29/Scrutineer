/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package toolgateway

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/enforcement"
)

// Sidecar env keys for reporter wiring (mirrors job builder / dns-proxy).
const (
	EnvSessionName      = "SCRUTINEER_SESSION_NAME"
	EnvSessionNamespace = "SCRUTINEER_SESSION_NAMESPACE"
	EnvReporterURL      = "SCRUTINEER_REPORTER_URL"
	EnvReporterToken    = "SCRUTINEER_REPORTER_TOKEN_PATH"
)

// DefaultToolGatewayImage is the first-party tool-gateway container image reference.
const DefaultToolGatewayImage = "ghcr.io/grantbarry29/scrutineer-tool-gateway:latest"

// RuntimeEnv is configuration loaded from the sidecar container environment.
type RuntimeEnv struct {
	SessionNamespace string
	SessionName      string
	ListenHost       string
	ReporterURL      string
	ReporterToken    string
	Mode             scrutineerv1alpha1.PolicyMode
	Policy           scrutineerv1alpha1.PolicyRules
}

// LoadRuntimeEnv reads tool-gateway configuration from the process environment.
func LoadRuntimeEnv() (RuntimeEnv, error) {
	env := RuntimeEnv{
		SessionNamespace: strings.TrimSpace(os.Getenv(EnvSessionNamespace)),
		SessionName:      strings.TrimSpace(os.Getenv(EnvSessionName)),
		ListenHost:       strings.TrimSpace(os.Getenv(EnvListenAddr)),
		ReporterURL:      strings.TrimSpace(os.Getenv(EnvReporterURL)),
		ReporterToken:    strings.TrimSpace(os.Getenv(EnvReporterToken)),
		Mode:             scrutineerv1alpha1.PolicyMode(strings.TrimSpace(os.Getenv(EnvPolicyMode))),
		Policy: scrutineerv1alpha1.PolicyRules{
			AllowedTools:         splitCSV(os.Getenv(EnvPolicyAllowedTools)),
			DeniedTools:          splitCSV(os.Getenv(EnvPolicyDeniedTools)),
			RequireHumanApproval: splitCSV(os.Getenv(EnvPolicyRequireApproval)),
			MaxToolCalls:         int32Env(os.Getenv(EnvPolicyMaxToolCalls)),
			MaxCallsPerMinute:    int32Env(os.Getenv(EnvPolicyMaxToolCallsPerMinute)),
			ArgumentRules:        argumentRulesEnv(os.Getenv(EnvPolicyArgumentRules)),
		},
	}
	if env.ListenHost == "" {
		env.ListenHost = DefaultListenHost
	}
	if env.SessionNamespace == "" || env.SessionName == "" {
		return RuntimeEnv{}, fmt.Errorf("SCRUTINEER_SESSION_NAMESPACE and SCRUTINEER_SESSION_NAME are required")
	}
	if env.ReporterURL == "" || env.ReporterToken == "" {
		return RuntimeEnv{}, fmt.Errorf("SCRUTINEER_REPORTER_URL and SCRUTINEER_REPORTER_TOKEN_PATH are required")
	}
	if env.Mode == "" {
		env.Mode = scrutineerv1alpha1.PolicyModeAuditOnly
	}
	return env, nil
}

// SessionContext returns enforcement input for policy evaluation and reporting.
func (e RuntimeEnv) SessionContext() enforcement.SessionContext {
	return enforcement.SessionContext{
		SessionNamespace: e.SessionNamespace,
		SessionName:      e.SessionName,
		Mode:             e.Mode,
		Policy:           e.Policy,
	}
}

func splitCSV(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func argumentRulesEnv(raw string) []scrutineerv1alpha1.ToolArgumentRule {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var rules []scrutineerv1alpha1.ToolArgumentRule
	if err := json.Unmarshal([]byte(raw), &rules); err != nil {
		return nil
	}
	return rules
}

func int32Env(raw string) *int32 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	n, err := strconv.ParseInt(raw, 10, 32)
	if err != nil {
		return nil
	}
	v := int32(n)
	return &v
}
