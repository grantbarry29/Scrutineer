/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package dnsproxy

import (
	"testing"

	corev1 "k8s.io/api/core/v1"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/enforcement"
)

func baseCtx(mode scrutineerv1alpha1.PolicyMode, rules scrutineerv1alpha1.PolicyRules) enforcement.SessionContext {
	return enforcement.SessionContext{
		SessionNamespace: "team-a",
		SessionName:      "demo",
		Mode:             mode,
		Policy:           rules,
	}
}

func TestEvaluateEgress_enforcedDeniedDomain(t *testing.T) {
	ctx := baseCtx(scrutineerv1alpha1.PolicyModeEnforced, scrutineerv1alpha1.PolicyRules{
		DeniedDomains: []string{"evil.example"},
	})
	auth := EvaluateEgress(ctx, EgressRequest{Host: "evil.example"})
	if auth.Allowed || !auth.Blocked || auth.Reason != ReasonDeniedDomains {
		t.Fatalf("got %+v", auth)
	}
}

func TestEvaluateEgress_dryRunDeniedDomain(t *testing.T) {
	ctx := baseCtx(scrutineerv1alpha1.PolicyModeDryRun, scrutineerv1alpha1.PolicyRules{
		DeniedDomains: []string{"evil.example"},
	})
	auth := EvaluateEgress(ctx, EgressRequest{Host: "evil.example"})
	if !auth.Allowed || !auth.WouldDeny || auth.Action != scrutineerv1alpha1.PolicyDecisionDryRun {
		t.Fatalf("got %+v", auth)
	}
}

// Shared wildcard semantics (#32): the dns-proxy now honors "*." patterns too, so it
// agrees with the Envoy path. Exact entries still do not cover subdomains.
func TestEvaluateEgress_wildcardDomains(t *testing.T) {
	denyCtx := baseCtx(scrutineerv1alpha1.PolicyModeEnforced, scrutineerv1alpha1.PolicyRules{
		DeniedDomains: []string{"*.evil.example"},
	})
	if auth := EvaluateEgress(denyCtx, EgressRequest{Host: "c2.evil.example"}); auth.Allowed || auth.Reason != ReasonDeniedDomains {
		t.Fatalf("wildcard deny subdomain: got %+v", auth)
	}
	if auth := EvaluateEgress(denyCtx, EgressRequest{Host: "evil.example"}); !auth.Allowed {
		t.Fatalf("wildcard must not match apex: got %+v", auth)
	}

	allowCtx := baseCtx(scrutineerv1alpha1.PolicyModeEnforced, scrutineerv1alpha1.PolicyRules{
		AllowedDomains: []string{"*.github.com"},
	})
	if auth := EvaluateEgress(allowCtx, EgressRequest{Host: "api.github.com"}); !auth.Allowed {
		t.Fatalf("wildcard allow subdomain: got %+v", auth)
	}
	if auth := EvaluateEgress(allowCtx, EgressRequest{Host: "api.gitlab.com"}); auth.Allowed || auth.Reason != ReasonNotInAllowedDomains {
		t.Fatalf("wildcard allowlist must deny others: got %+v", auth)
	}
	// Exact entry still does not cover subdomains.
	exactCtx := baseCtx(scrutineerv1alpha1.PolicyModeEnforced, scrutineerv1alpha1.PolicyRules{
		AllowedDomains: []string{"github.com"},
	})
	if auth := EvaluateEgress(exactCtx, EgressRequest{Host: "api.github.com"}); auth.Allowed {
		t.Fatalf("exact entry must not cover subdomain: got %+v", auth)
	}
}

// #41 headline case, deny side: an exact deny entry covers only itself. Subdomain
// coverage is opt-in via "*.evil.example" (add both entries to cover apex + subdomains) —
// implicit suffix matching was rejected in the #32 semantics decision.
func TestEvaluateEgress_exactDenyDoesNotCoverSubdomain(t *testing.T) {
	ctx := baseCtx(scrutineerv1alpha1.PolicyModeEnforced, scrutineerv1alpha1.PolicyRules{
		DeniedDomains: []string{"evil.example"},
	})
	if auth := EvaluateEgress(ctx, EgressRequest{Host: "api.evil.example"}); !auth.Allowed {
		t.Fatalf("exact deny entry must not cover subdomain: got %+v", auth)
	}

	bothCtx := baseCtx(scrutineerv1alpha1.PolicyModeEnforced, scrutineerv1alpha1.PolicyRules{
		DeniedDomains: []string{"evil.example", "*.evil.example"},
	})
	for _, host := range []string{"evil.example", "api.evil.example"} {
		if auth := EvaluateEgress(bothCtx, EgressRequest{Host: host}); auth.Allowed || auth.Reason != ReasonDeniedDomains {
			t.Fatalf("apex+wildcard pair must deny %q: got %+v", host, auth)
		}
	}
}

func TestEvaluateEgress_allowlistDomain(t *testing.T) {
	ctx := baseCtx(scrutineerv1alpha1.PolicyModeEnforced, scrutineerv1alpha1.PolicyRules{
		AllowedDomains: []string{"github.com"},
	})
	auth := EvaluateEgress(ctx, EgressRequest{Host: "dropbox.com"})
	if auth.Allowed || auth.Reason != ReasonNotInAllowedDomains {
		t.Fatalf("got %+v", auth)
	}
}

func TestEvaluateEgress_deniedCIDR(t *testing.T) {
	ctx := baseCtx(scrutineerv1alpha1.PolicyModeEnforced, scrutineerv1alpha1.PolicyRules{
		DeniedCIDRs: []string{"10.0.0.0/8"},
	})
	auth := EvaluateEgress(ctx, EgressRequest{Host: "10.1.2.3"})
	if auth.Allowed || auth.Reason != ReasonDeniedCIDRs {
		t.Fatalf("got %+v", auth)
	}
}

func TestEnvForConfig_includesPolicyLists(t *testing.T) {
	cfg := BuildConfig(baseCtx(scrutineerv1alpha1.PolicyModeEnforced, scrutineerv1alpha1.PolicyRules{
		DeniedDomains: []string{"evil.example"},
		AllowedCIDRs:  []string{"203.0.113.0/24"},
	}))
	env := envMap(EnvForConfig(cfg))
	if env[EnvPolicyDeniedDomains] != "evil.example" {
		t.Fatalf("denied domains = %q", env[EnvPolicyDeniedDomains])
	}
	if env[EnvBindAddr] != DefaultBindAddr {
		t.Fatalf("listen = %q", env[EnvBindAddr])
	}
}

func TestEvaluateEgress_allowedDomain(t *testing.T) {
	ctx := baseCtx(scrutineerv1alpha1.PolicyModeEnforced, scrutineerv1alpha1.PolicyRules{
		AllowedDomains: []string{"github.com"},
	})
	auth := EvaluateEgress(ctx, EgressRequest{Host: "github.com"})
	if !auth.Allowed || auth.Reason != ReasonAllowed {
		t.Fatalf("got %+v", auth)
	}
}

func TestEvaluateEgress_allowedCIDR(t *testing.T) {
	ctx := baseCtx(scrutineerv1alpha1.PolicyModeEnforced, scrutineerv1alpha1.PolicyRules{
		AllowedCIDRs: []string{"203.0.113.0/24"},
	})
	auth := EvaluateEgress(ctx, EgressRequest{Host: "203.0.113.50"})
	if !auth.Allowed {
		t.Fatalf("got %+v", auth)
	}
}

func TestEvaluateEgress_notInAllowedCIDR(t *testing.T) {
	ctx := baseCtx(scrutineerv1alpha1.PolicyModeEnforced, scrutineerv1alpha1.PolicyRules{
		AllowedCIDRs: []string{"203.0.113.0/24"},
	})
	auth := EvaluateEgress(ctx, EgressRequest{Host: "198.51.100.1"})
	if auth.Allowed || auth.Reason != ReasonNotInAllowedCIDRs {
		t.Fatalf("got %+v", auth)
	}
}

func TestEvaluateEgress_emptyHost(t *testing.T) {
	ctx := baseCtx(scrutineerv1alpha1.PolicyModeEnforced, scrutineerv1alpha1.PolicyRules{})
	auth := EvaluateEgress(ctx, EgressRequest{})
	if auth.Allowed || auth.Reason != ReasonEmptyHost {
		t.Fatalf("got %+v", auth)
	}
}

func TestHasEnabledSidecar(t *testing.T) {
	disabled := false
	ctx := enforcement.SessionContext{
		Enforcement: []scrutineerv1alpha1.RuntimeProfileEnforcement{
			{Type: EnforcementType, Enabled: &disabled},
		},
	}
	if HasEnabledSidecar(ctx) {
		t.Fatal("disabled sidecar should not count")
	}
	ctx.Enforcement[0].Enabled = nil
	if !HasEnabledSidecar(ctx) {
		t.Fatal("nil enabled should default to enabled")
	}
}

func TestBuildConfig_nilWhenNotApplicable(t *testing.T) {
	if BuildConfig(enforcement.SessionContext{}) != nil {
		t.Fatal("expected nil config")
	}
}

func TestEnvForConfig_nil(t *testing.T) {
	if EnvForConfig(nil) != nil {
		t.Fatal("expected nil env")
	}
}

func envMap(vars []corev1.EnvVar) map[string]string {
	out := make(map[string]string, len(vars))
	for _, e := range vars {
		out[e.Name] = e.Value
	}
	return out
}
