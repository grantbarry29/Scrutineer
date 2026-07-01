/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package dnsproxy

import (
	"os"
	"strings"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/enforcement"
	"github.com/grantbarry29/scrutineer/internal/enforcement/sidecarenv"
)

// Sidecar env keys for the dns-proxy runtime binary. Session/reporter keys are owned by
// the shared sidecarenv package; aliased here for local (and test) reference.
const (
	EnvSessionName      = sidecarenv.EnvSessionName
	EnvSessionNamespace = sidecarenv.EnvSessionNamespace
	EnvReporterURL      = sidecarenv.EnvReporterURL
	EnvReporterToken    = sidecarenv.EnvReporterToken
)

// DefaultDNSProxyImage is the first-party dns-proxy container image reference.
const DefaultDNSProxyImage = "ghcr.io/grantbarry29/scrutineer-dns-proxy:latest"

// RuntimeEnv is configuration loaded from the sidecar container environment.
type RuntimeEnv struct {
	sidecarenv.Base
	ListenAddr string
	Policy     scrutineerv1alpha1.PolicyRules
}

// LoadRuntimeEnv reads dns-proxy configuration from the process environment.
func LoadRuntimeEnv() (RuntimeEnv, error) {
	base, err := sidecarenv.LoadBase(os.Getenv(EnvPolicyMode))
	if err != nil {
		return RuntimeEnv{}, err
	}
	env := RuntimeEnv{
		Base:       base,
		ListenAddr: strings.TrimSpace(os.Getenv(EnvListenAddr)),
		Policy: scrutineerv1alpha1.PolicyRules{
			AllowedDomains: sidecarenv.SplitCSV(os.Getenv(EnvPolicyAllowedDomains)),
			DeniedDomains:  sidecarenv.SplitCSV(os.Getenv(EnvPolicyDeniedDomains)),
			AllowedCIDRs:   sidecarenv.SplitCSV(os.Getenv(EnvPolicyAllowedCIDRs)),
			DeniedCIDRs:    sidecarenv.SplitCSV(os.Getenv(EnvPolicyDeniedCIDRs)),
		},
	}
	if env.ListenAddr == "" {
		env.ListenAddr = DefaultListenAddr
	}
	return env, nil
}

// SessionContext returns enforcement input for policy evaluation and reporting.
func (e RuntimeEnv) SessionContext() enforcement.SessionContext {
	return e.Base.SessionContext(e.Policy)
}
