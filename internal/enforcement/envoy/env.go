/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package envoy

import (
	"os"

	corev1 "k8s.io/api/core/v1"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/enforcement/sidecarenv"
)

// Policy env keys the egress-reporter reads. Values mirror the agent/sidecar convention
// (AGENT_POLICY_*) so a session's effective FQDN policy is expressed the same way
// everywhere. Duplicated here (rather than imported from internal/controller/job) because
// the job package imports this one.
const (
	EnvPolicyMode           = "AGENT_POLICY_MODE"
	EnvPolicyAllowedDomains = "AGENT_POLICY_ALLOWED_DOMAINS"
	EnvPolicyDeniedDomains  = "AGENT_POLICY_DENIED_DOMAINS"
)

// PolicyFromEnv loads the effective FQDN policy the egress-reporter classifies observed
// authorities against (#32).
func PolicyFromEnv() EgressPolicy {
	return EgressPolicy{
		Enforce:        os.Getenv(EnvPolicyMode) == string(scrutineerv1alpha1.PolicyModeEnforced),
		AllowedDomains: sidecarenv.SplitCSV(os.Getenv(EnvPolicyAllowedDomains)),
		DeniedDomains:  sidecarenv.SplitCSV(os.Getenv(EnvPolicyDeniedDomains)),
	}
}

// policyEnv renders the FQDN-policy env vars for the egress-reporter container from a
// BootstrapConfig (the same source that drives the Envoy RBAC), so enforcement and
// evidence classification always see the same policy.
func policyEnv(cfg BootstrapConfig) []corev1.EnvVar {
	mode := string(scrutineerv1alpha1.PolicyModeAuditOnly)
	if cfg.Enforce {
		mode = string(scrutineerv1alpha1.PolicyModeEnforced)
	}
	return []corev1.EnvVar{
		{Name: EnvPolicyMode, Value: mode},
		{Name: EnvPolicyAllowedDomains, Value: csvJoin(cfg.AllowedDomains)},
		{Name: EnvPolicyDeniedDomains, Value: csvJoin(cfg.DeniedDomains)},
	}
}

func csvJoin(in []string) string {
	out := ""
	for i, s := range in {
		if i > 0 {
			out += ","
		}
		out += s
	}
	return out
}
