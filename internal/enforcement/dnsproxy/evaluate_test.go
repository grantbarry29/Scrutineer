/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package dnsproxy

import (
	"testing"

	corev1 "k8s.io/api/core/v1"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
	"github.com/secureai/relay/internal/enforcement"
)

func baseCtx(mode relayv1alpha1.PolicyMode, rules relayv1alpha1.PolicyRules) enforcement.SessionContext {
	return enforcement.SessionContext{
		SessionNamespace: "team-a",
		SessionName:      "demo",
		Mode:             mode,
		Policy:           rules,
	}
}

func TestEvaluateEgress_enforcedDeniedDomain(t *testing.T) {
	ctx := baseCtx(relayv1alpha1.PolicyModeEnforced, relayv1alpha1.PolicyRules{
		DeniedDomains: []string{"evil.example"},
	})
	auth := EvaluateEgress(ctx, EgressRequest{Host: "evil.example"})
	if auth.Allowed || !auth.Blocked || auth.Reason != ReasonDeniedDomains {
		t.Fatalf("got %+v", auth)
	}
}

func TestEvaluateEgress_dryRunDeniedDomain(t *testing.T) {
	ctx := baseCtx(relayv1alpha1.PolicyModeDryRun, relayv1alpha1.PolicyRules{
		DeniedDomains: []string{"evil.example"},
	})
	auth := EvaluateEgress(ctx, EgressRequest{Host: "evil.example"})
	if !auth.Allowed || !auth.WouldDeny || auth.Action != relayv1alpha1.PolicyDecisionDryRun {
		t.Fatalf("got %+v", auth)
	}
}

func TestEvaluateEgress_allowlistDomain(t *testing.T) {
	ctx := baseCtx(relayv1alpha1.PolicyModeEnforced, relayv1alpha1.PolicyRules{
		AllowedDomains: []string{"github.com"},
	})
	auth := EvaluateEgress(ctx, EgressRequest{Host: "dropbox.com"})
	if auth.Allowed || auth.Reason != ReasonNotInAllowedDomains {
		t.Fatalf("got %+v", auth)
	}
}

func TestEvaluateEgress_deniedCIDR(t *testing.T) {
	ctx := baseCtx(relayv1alpha1.PolicyModeEnforced, relayv1alpha1.PolicyRules{
		DeniedCIDRs: []string{"10.0.0.0/8"},
	})
	auth := EvaluateEgress(ctx, EgressRequest{Host: "10.1.2.3"})
	if auth.Allowed || auth.Reason != ReasonDeniedCIDRs {
		t.Fatalf("got %+v", auth)
	}
}

func TestEnvForConfig_includesPolicyLists(t *testing.T) {
	cfg := BuildConfig(baseCtx(relayv1alpha1.PolicyModeEnforced, relayv1alpha1.PolicyRules{
		DeniedDomains: []string{"evil.example"},
		AllowedCIDRs:  []string{"203.0.113.0/24"},
	}))
	env := envMap(EnvForConfig(cfg))
	if env[EnvPolicyDeniedDomains] != "evil.example" {
		t.Fatalf("denied domains = %q", env[EnvPolicyDeniedDomains])
	}
	if env[EnvListenAddr] != DefaultListenAddr {
		t.Fatalf("listen = %q", env[EnvListenAddr])
	}
}

func envMap(vars []corev1.EnvVar) map[string]string {
	out := make(map[string]string, len(vars))
	for _, e := range vars {
		out[e.Name] = e.Value
	}
	return out
}
