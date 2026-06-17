/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package workspace

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
	"github.com/secureai/relay/internal/enforcement"
)

// Sidecar env keys for reporter wiring (mirrors job builder / dns-proxy).
const (
	EnvSessionName      = "RELAY_SESSION_NAME"
	EnvSessionNamespace = "RELAY_SESSION_NAMESPACE"
	EnvReporterURL      = "RELAY_REPORTER_URL"
	EnvReporterToken    = "RELAY_REPORTER_TOKEN_PATH"
)

// RuntimeEnv is configuration loaded from the sidecar container environment.
type RuntimeEnv struct {
	SessionNamespace string
	SessionName      string
	ListenHost       string
	ReporterURL      string
	ReporterToken    string
	Mode             relayv1alpha1.PolicyMode
	Policy           relayv1alpha1.PolicyRules
}

// LoadRuntimeEnv reads fs-gateway configuration from the process environment.
func LoadRuntimeEnv() (RuntimeEnv, error) {
	env := RuntimeEnv{
		SessionNamespace: strings.TrimSpace(os.Getenv(EnvSessionNamespace)),
		SessionName:      strings.TrimSpace(os.Getenv(EnvSessionName)),
		ListenHost:       strings.TrimSpace(os.Getenv(EnvListenAddr)),
		ReporterURL:      strings.TrimSpace(os.Getenv(EnvReporterURL)),
		ReporterToken:    strings.TrimSpace(os.Getenv(EnvReporterToken)),
		Mode:             relayv1alpha1.PolicyMode(strings.TrimSpace(os.Getenv(EnvPolicyMode))),
		Policy: relayv1alpha1.PolicyRules{
			AllowedPaths:      splitCSV(os.Getenv(EnvPolicyAllowedPaths)),
			DeniedPaths:       splitCSV(os.Getenv(EnvPolicyDeniedPaths)),
			MaxWorkspaceBytes: int64Env(os.Getenv(EnvPolicyMaxWorkspaceBytes)),
		},
	}
	if env.ListenHost == "" {
		env.ListenHost = DefaultListenHost
	}
	if env.SessionNamespace == "" || env.SessionName == "" {
		return RuntimeEnv{}, fmt.Errorf("RELAY_SESSION_NAMESPACE and RELAY_SESSION_NAME are required")
	}
	if env.ReporterURL == "" || env.ReporterToken == "" {
		return RuntimeEnv{}, fmt.Errorf("RELAY_REPORTER_URL and RELAY_REPORTER_TOKEN_PATH are required")
	}
	if env.Mode == "" {
		env.Mode = relayv1alpha1.PolicyModeAuditOnly
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

func int64Env(raw string) *int64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return nil
	}
	return &n
}
