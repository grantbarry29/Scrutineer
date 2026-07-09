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
	"strings"

	corev1 "k8s.io/api/core/v1"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/enforcement/containerenv"
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
		AllowedDomains: containerenv.SplitCSV(os.Getenv(EnvPolicyAllowedDomains)),
		DeniedDomains:  containerenv.SplitCSV(os.Getenv(EnvPolicyDeniedDomains)),
	}
}

// policyEnv renders the FQDN-policy env vars for the egress-reporter container from a
// BootstrapConfig (the same source that drives the Envoy RBAC), so enforcement and
// evidence classification always see the same policy.
//
// Precondition (#103): patterns passed the shared enforcement.ValidateDomainPattern at
// reconcile time. The comma join round-trips through containerenv.SplitCSV — an
// unvalidated pattern containing a comma would silently split into two different
// patterns on the evidence side only, making evidence disagree with enforcement.
func policyEnv(cfg BootstrapConfig) []corev1.EnvVar {
	mode := string(scrutineerv1alpha1.PolicyModeAuditOnly)
	if cfg.Enforce {
		mode = string(scrutineerv1alpha1.PolicyModeEnforced)
	}
	return []corev1.EnvVar{
		{Name: EnvPolicyMode, Value: mode},
		{Name: EnvPolicyAllowedDomains, Value: strings.Join(cfg.AllowedDomains, ",")},
		{Name: EnvPolicyDeniedDomains, Value: strings.Join(cfg.DeniedDomains, ",")},
	}
}
