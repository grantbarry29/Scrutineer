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

	networkingv1 "k8s.io/api/networking/v1"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	scrutineerjob "github.com/grantbarry29/scrutineer/internal/controller/job"
	"github.com/grantbarry29/scrutineer/internal/enforcement"
)

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
