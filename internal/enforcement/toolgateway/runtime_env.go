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
	"os"
	"strconv"
	"strings"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/enforcement"
	"github.com/grantbarry29/scrutineer/internal/enforcement/sidecarenv"
)

// Sidecar env keys for reporter wiring. Session/reporter keys are owned by the shared
// sidecarenv package; aliased here for local (and test) reference.
const (
	EnvSessionName      = sidecarenv.EnvSessionName
	EnvSessionNamespace = sidecarenv.EnvSessionNamespace
	EnvReporterURL      = sidecarenv.EnvReporterURL
	EnvReporterToken    = sidecarenv.EnvReporterToken
)

// DefaultToolGatewayImage is the first-party tool-gateway container image reference.
const DefaultToolGatewayImage = "ghcr.io/grantbarry29/scrutineer-tool-gateway:latest"

// RuntimeEnv is configuration loaded from the sidecar container environment.
type RuntimeEnv struct {
	sidecarenv.Base
	ListenHost string
	Policy     scrutineerv1alpha1.PolicyRules
}

// LoadRuntimeEnv reads tool-gateway configuration from the process environment.
func LoadRuntimeEnv() (RuntimeEnv, error) {
	base, err := sidecarenv.LoadBase(os.Getenv(EnvPolicyMode))
	if err != nil {
		return RuntimeEnv{}, err
	}
	env := RuntimeEnv{
		Base:       base,
		ListenHost: strings.TrimSpace(os.Getenv(EnvListenAddr)),
		Policy: scrutineerv1alpha1.PolicyRules{
			AllowedTools:         sidecarenv.SplitCSV(os.Getenv(EnvPolicyAllowedTools)),
			DeniedTools:          sidecarenv.SplitCSV(os.Getenv(EnvPolicyDeniedTools)),
			RequireHumanApproval: sidecarenv.SplitCSV(os.Getenv(EnvPolicyRequireApproval)),
			MaxToolCalls:         int32Env(os.Getenv(EnvPolicyMaxToolCalls)),
			MaxCallsPerMinute:    int32Env(os.Getenv(EnvPolicyMaxToolCallsPerMinute)),
			ArgumentRules:        argumentRulesEnv(os.Getenv(EnvPolicyArgumentRules)),
		},
	}
	if env.ListenHost == "" {
		env.ListenHost = DefaultListenHost
	}
	return env, nil
}

// SessionContext returns enforcement input for policy evaluation and reporting.
func (e RuntimeEnv) SessionContext() enforcement.SessionContext {
	return e.Base.SessionContext(e.Policy)
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
