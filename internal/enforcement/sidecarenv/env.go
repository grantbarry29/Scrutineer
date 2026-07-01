/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package sidecarenv holds the session/reporter/mode configuration every enforcement
// sidecar (dns-proxy, tool-gateway, fs-gateway) loads from its container environment.
// Each sidecar embeds Base in its own RuntimeEnv and adds its listen-address and policy
// specifics, so a new shared env var is added in one place rather than three.
package sidecarenv

import (
	"fmt"
	"os"
	"strings"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/enforcement"
)

// Shared sidecar env keys (mirrors the job builder's propagation).
const (
	EnvSessionName      = "SCRUTINEER_SESSION_NAME"
	EnvSessionNamespace = "SCRUTINEER_SESSION_NAMESPACE"
	EnvReporterURL      = "SCRUTINEER_REPORTER_URL"
	EnvReporterToken    = "SCRUTINEER_REPORTER_TOKEN_PATH"
)

// Base is the configuration common to every sidecar: which session it enforces and how
// to reach the reporter, plus the effective policy mode.
type Base struct {
	SessionNamespace string
	SessionName      string
	ReporterURL      string
	ReporterToken    string
	Mode             scrutineerv1alpha1.PolicyMode
}

// LoadBase reads and validates the shared fields from the process environment. rawMode is
// the raw policy-mode env value (the key lives with each sidecar's policy env vars);
// an empty mode defaults to audit-only.
func LoadBase(rawMode string) (Base, error) {
	b := Base{
		SessionNamespace: strings.TrimSpace(os.Getenv(EnvSessionNamespace)),
		SessionName:      strings.TrimSpace(os.Getenv(EnvSessionName)),
		ReporterURL:      strings.TrimSpace(os.Getenv(EnvReporterURL)),
		ReporterToken:    strings.TrimSpace(os.Getenv(EnvReporterToken)),
		Mode:             scrutineerv1alpha1.PolicyMode(strings.TrimSpace(rawMode)),
	}
	if b.SessionNamespace == "" || b.SessionName == "" {
		return Base{}, fmt.Errorf("SCRUTINEER_SESSION_NAMESPACE and SCRUTINEER_SESSION_NAME are required")
	}
	if b.ReporterURL == "" || b.ReporterToken == "" {
		return Base{}, fmt.Errorf("SCRUTINEER_REPORTER_URL and SCRUTINEER_REPORTER_TOKEN_PATH are required")
	}
	if b.Mode == "" {
		b.Mode = scrutineerv1alpha1.PolicyModeAuditOnly
	}
	return b, nil
}

// SessionContext returns enforcement input for policy evaluation and reporting, combining
// the shared session/mode fields with a sidecar's resolved policy.
func (b Base) SessionContext(policy scrutineerv1alpha1.PolicyRules) enforcement.SessionContext {
	return enforcement.SessionContext{
		SessionNamespace: b.SessionNamespace,
		SessionName:      b.SessionName,
		Mode:             b.Mode,
		Policy:           policy,
	}
}

// SplitCSV parses a comma-separated env value into a trimmed, non-empty slice.
func SplitCSV(raw string) []string {
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
