/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package workspace

import (
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

// RuntimeEnv is configuration loaded from the sidecar container environment.
type RuntimeEnv struct {
	sidecarenv.Base
	ListenHost string
	Policy     scrutineerv1alpha1.PolicyRules
}

// LoadRuntimeEnv reads fs-gateway configuration from the process environment.
func LoadRuntimeEnv() (RuntimeEnv, error) {
	base, err := sidecarenv.LoadBase(os.Getenv(EnvPolicyMode))
	if err != nil {
		return RuntimeEnv{}, err
	}
	env := RuntimeEnv{
		Base:       base,
		ListenHost: strings.TrimSpace(os.Getenv(EnvListenAddr)),
		Policy: scrutineerv1alpha1.PolicyRules{
			AllowedPaths:      sidecarenv.SplitCSV(os.Getenv(EnvPolicyAllowedPaths)),
			DeniedPaths:       sidecarenv.SplitCSV(os.Getenv(EnvPolicyDeniedPaths)),
			MaxWorkspaceBytes: int64Env(os.Getenv(EnvPolicyMaxWorkspaceBytes)),
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
