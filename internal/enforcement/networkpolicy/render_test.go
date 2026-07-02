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

// The Envoy pod gets a backstop egress policy: it may reach the internet + DNS, but NOT the
// hard-denied CIDRs (cloud metadata, and any operator-added cluster ranges) — a defense in
// depth that holds even if Envoy itself is compromised (Slice B, #61 B3).
func TestBuildEgressProxyBackstop(t *testing.T) {
	ctx := enforcement.SessionContext{SessionNamespace: "team-a", SessionName: "demo"}
	np := BuildEgressProxyBackstop(ctx, []string{"169.254.0.0/16", "10.0.0.0/8"})
	if np == nil {
		t.Fatal("expected a backstop NetworkPolicy")
	}
	if np.Name != BackstopNameFor("team-a", "demo") {
		t.Fatalf("name = %q", np.Name)
	}
	// It must select the session's Envoy pod, not the agent pod.
	for k, v := range envoy.Labels("demo") {
		if np.Spec.PodSelector.MatchLabels[k] != v {
			t.Fatalf("backstop must select the Envoy pod; missing %s=%s: %#v", k, v, np.Spec.PodSelector.MatchLabels)
		}
	}
	if np.Spec.PodSelector.MatchLabels[scrutineerjob.LabelSessionRef] == "demo" {
		t.Fatal("backstop must select the Envoy pod, not the agent (LabelSessionRef)")
	}
	if len(np.Spec.PolicyTypes) != 1 || np.Spec.PolicyTypes[0] != networkingv1.PolicyTypeEgress {
		t.Fatalf("policyTypes = %#v", np.Spec.PolicyTypes)
	}

	// Find the allow-all-except rule and assert every backstop CIDR is excepted.
	var ipRule *networkingv1.NetworkPolicyEgressRule
	hasDNS := false
	for i := range np.Spec.Egress {
		r := &np.Spec.Egress[i]
		for _, p := range r.Ports {
			if p.Port != nil && p.Port.IntValue() == 53 {
				hasDNS = true
			}
		}
		for _, to := range r.To {
			if to.IPBlock != nil && to.IPBlock.CIDR == "0.0.0.0/0" {
				ipRule = r
			}
		}
	}
	if !hasDNS {
		t.Fatal("backstop must still allow DNS so Envoy can resolve upstreams")
	}
	if ipRule == nil {
		t.Fatal("backstop must allow 0.0.0.0/0 (minus the excepted ranges)")
	}
	except := ipRule.To[0].IPBlock.Except
	for _, want := range []string{"169.254.0.0/16", "10.0.0.0/8"} {
		found := false
		for _, e := range except {
			if e == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("backstop except missing %q: %#v", want, except)
		}
	}
}

// The backstop must never sever the evidence channel: even when operators backstop
// cluster/service CIDRs, the Envoy pod keeps an explicit allow to the reporter namespace
// on the reporter port so observed evidence (Slice C, #62) still flows.
func TestBuildEgressProxyBackstop_alwaysAllowsReporter(t *testing.T) {
	ctx := enforcement.SessionContext{SessionNamespace: "team-a", SessionName: "demo"}
	// Backstop the entire service+pod space — the worst case for the evidence channel.
	np := BuildEgressProxyBackstop(ctx, []string{"10.0.0.0/8", "192.168.0.0/16"})
	if np == nil {
		t.Fatal("expected a backstop NetworkPolicy")
	}
	found := false
	for _, rule := range np.Spec.Egress {
		for _, to := range rule.To {
			if to.NamespaceSelector == nil {
				continue
			}
			if to.NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"] != ReporterNamespace {
				continue
			}
			for _, p := range rule.Ports {
				if p.Port != nil && p.Port.IntValue() == ReporterPort {
					found = true
				}
			}
		}
	}
	if !found {
		t.Fatalf("backstop must allow the reporter namespace on port %d: %+v", ReporterPort, np.Spec.Egress)
	}
}

func TestDefaultBackstopCIDRs_denyMetadata(t *testing.T) {
	ctx := enforcement.SessionContext{SessionNamespace: "ns", SessionName: "s"}
	np := BuildEgressProxyBackstop(ctx, DefaultBackstopCIDRs)
	found := false
	for _, r := range np.Spec.Egress {
		for _, to := range r.To {
			if to.IPBlock != nil {
				for _, e := range to.IPBlock.Except {
					if e == "169.254.169.254/32" || e == "169.254.0.0/16" {
						found = true
					}
				}
			}
		}
	}
	if !found {
		t.Fatalf("default backstops must deny the cloud metadata IP; got %#v", DefaultBackstopCIDRs)
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
