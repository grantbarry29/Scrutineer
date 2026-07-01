/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package networkpolicy

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	scrutineerjob "github.com/grantbarry29/scrutineer/internal/controller/job"
	"github.com/grantbarry29/scrutineer/internal/enforcement"
	"github.com/grantbarry29/scrutineer/internal/enforcement/envoy"
)

func enabledEnvoySidecar() scrutineerv1alpha1.RuntimeProfileSidecar {
	on := true
	return scrutineerv1alpha1.RuntimeProfileSidecar{Name: "envoy", Type: scrutineerjob.SidecarTypeEnvoy, Enabled: &on}
}

// When the per-session Envoy egress proxy is enabled, the agent pod gets a mandatory
// default-deny egress lock: the ONLY allowed egress is to the session's Envoy pod on the
// proxy port. No DNS rule — the agent resolves nothing itself (Envoy does), which closes
// the DNS-exfil path (Slice B, #61).
func TestBuild_egressLockWhenEnvoyEnabled(t *testing.T) {
	ctx := enforcement.SessionContext{
		SessionNamespace: "team-a",
		SessionName:      "demo",
		Mode:             scrutineerv1alpha1.PolicyModeAuditOnly, // lock is mandatory regardless of mode
		Sidecars:         []scrutineerv1alpha1.RuntimeProfileSidecar{enabledEnvoySidecar()},
	}
	np := Build(ctx)
	if np == nil {
		t.Fatal("expected an egress-lock NetworkPolicy when envoy egress is enabled")
	}
	if np.Spec.PodSelector.MatchLabels[scrutineerjob.LabelSessionRef] != "demo" {
		t.Fatalf("policy must select the agent pod; selector=%#v", np.Spec.PodSelector.MatchLabels)
	}
	if len(np.Spec.PolicyTypes) != 1 || np.Spec.PolicyTypes[0] != networkingv1.PolicyTypeEgress {
		t.Fatalf("policyTypes = %#v", np.Spec.PolicyTypes)
	}
	if len(np.Spec.Egress) != 1 {
		t.Fatalf("expected exactly one allow rule (Envoy only), got %d: %#v", len(np.Spec.Egress), np.Spec.Egress)
	}
	rule := np.Spec.Egress[0]
	// Peer must be the session's Envoy pod (by label), and the only port the proxy port.
	if len(rule.To) != 1 || rule.To[0].PodSelector == nil {
		t.Fatalf("egress peer must be a podSelector for the Envoy pod: %#v", rule.To)
	}
	for k, v := range envoy.Labels("demo") {
		if rule.To[0].PodSelector.MatchLabels[k] != v {
			t.Fatalf("egress peer selector missing %s=%s: %#v", k, v, rule.To[0].PodSelector.MatchLabels)
		}
	}
	if len(rule.Ports) != 1 || rule.Ports[0].Port.IntValue() != envoy.ProxyPort {
		t.Fatalf("egress must allow only the proxy port %d: %#v", envoy.ProxyPort, rule.Ports)
	}
	if p := rule.Ports[0].Protocol; p == nil || *p != corev1.ProtocolTCP {
		t.Fatalf("proxy port must be TCP: %#v", rule.Ports[0])
	}
	// No DNS: there must be no rule opening UDP/TCP 53.
	for _, r := range np.Spec.Egress {
		for _, port := range r.Ports {
			if port.Port != nil && port.Port.IntValue() == 53 {
				t.Fatalf("egress lock must not allow DNS (port 53): %#v", r)
			}
		}
	}
}

// A disabled envoy sidecar must not trigger the lock (falls back to CIDR behavior → nil here).
func TestBuild_noEgressLockWhenEnvoyDisabled(t *testing.T) {
	off := false
	ctx := enforcement.SessionContext{
		SessionNamespace: "team-a",
		SessionName:      "demo",
		Mode:             scrutineerv1alpha1.PolicyModeEnforced,
		Sidecars: []scrutineerv1alpha1.RuntimeProfileSidecar{
			{Name: "envoy", Type: scrutineerjob.SidecarTypeEnvoy, Enabled: &off},
		},
	}
	if got := Build(ctx); got != nil {
		t.Fatalf("expected nil when envoy is disabled and no CIDR rules, got %#v", got)
	}
}

func TestBuild_nilWhenAuditOnly(t *testing.T) {
	ctx := enforcement.SessionContext{
		SessionNamespace: "team-a",
		SessionName:      "demo",
		Mode:             scrutineerv1alpha1.PolicyModeAuditOnly,
		Policy: scrutineerv1alpha1.PolicyRules{
			AllowedCIDRs: []string{"203.0.113.0/24"},
		},
	}
	if got := Build(ctx); got != nil {
		t.Fatalf("expected nil for audit-only, got %#v", got)
	}
}

func TestBuild_nilForDomainsOnly(t *testing.T) {
	ctx := enforcement.SessionContext{
		SessionNamespace: "team-a",
		SessionName:      "demo",
		Mode:             scrutineerv1alpha1.PolicyModeEnforced,
		Policy: scrutineerv1alpha1.PolicyRules{
			DeniedDomains: []string{"evil.example"},
		},
	}
	if got := Build(ctx); got != nil {
		t.Fatal("expected nil for domain-only policy")
	}
}

func TestBuild_allowedCIDRs(t *testing.T) {
	ctx := enforcement.SessionContext{
		SessionNamespace: "team-a",
		SessionName:      "demo",
		Mode:             scrutineerv1alpha1.PolicyModeEnforced,
		Policy: scrutineerv1alpha1.PolicyRules{
			AllowedCIDRs: []string{"203.0.113.5", "198.51.100.0/24"},
		},
	}
	np := Build(ctx)
	if np == nil {
		t.Fatal("expected NetworkPolicy")
	}
	if np.Name != NameFor("team-a", "demo") {
		t.Fatalf("name = %q", np.Name)
	}
	if np.Spec.PodSelector.MatchLabels[scrutineerjob.LabelSessionRef] != "demo" {
		t.Fatalf("selector = %#v", np.Spec.PodSelector.MatchLabels)
	}
	if len(np.Spec.Egress) != 3 { // DNS + 2 CIDR rules
		t.Fatalf("egress rules = %d", len(np.Spec.Egress))
	}
	if np.Spec.Egress[1].To[0].IPBlock.CIDR != "203.0.113.5/32" {
		t.Fatalf("first cidr = %q", np.Spec.Egress[1].To[0].IPBlock.CIDR)
	}
}

func TestBuild_deniedCIDRs(t *testing.T) {
	ctx := enforcement.SessionContext{
		SessionNamespace: "team-a",
		SessionName:      "demo",
		Mode:             scrutineerv1alpha1.PolicyModeEnforced,
		Policy: scrutineerv1alpha1.PolicyRules{
			DeniedCIDRs: []string{"10.0.0.0/8"},
		},
	}
	np := Build(ctx)
	if np == nil {
		t.Fatal("expected NetworkPolicy")
	}
	if len(np.Spec.Egress) != 2 { // DNS + default route except
		t.Fatalf("egress rules = %d", len(np.Spec.Egress))
	}
	block := np.Spec.Egress[1].To[0].IPBlock
	if block.CIDR != "0.0.0.0/0" || len(block.Except) != 1 || block.Except[0] != "10.0.0.0/8" {
		t.Fatalf("ipBlock = %#v", block)
	}
}

func TestBackendDesiredState(t *testing.T) {
	b := Backend{}
	if b.Kind() != enforcement.BackendNetworkPolicy {
		t.Fatalf("kind = %q", b.Kind())
	}
	caps := b.Capabilities()
	if !caps.NetworkCIDR || caps.NetworkFQDN {
		t.Fatalf("caps = %#v", caps)
	}
	raw, err := b.DesiredState(enforcement.SessionContext{
		SessionNamespace: "ns",
		SessionName:      "s",
		Mode:             scrutineerv1alpha1.PolicyModeEnforced,
		Policy:           scrutineerv1alpha1.PolicyRules{AllowedCIDRs: []string{"1.2.3.4/32"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := raw.(*networkingv1.NetworkPolicy); !ok {
		t.Fatalf("type = %T", raw)
	}
}
