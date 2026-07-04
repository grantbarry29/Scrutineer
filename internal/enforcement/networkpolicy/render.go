/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package networkpolicy renders Kubernetes NetworkPolicy objects for coarse CIDR egress
// enforcement on AgentSession runtimes.
//
// Limitations (Phase 3 slice 3):
//   - FQDN fields (allowedDomains/deniedDomains) are not enforced; use DNS/egress proxy (slice 7).
//   - Restrictive policies are applied only when effective policy mode is enforced.
//   - Requires a CNI that enforces NetworkPolicy.
//
// Dual-stack posture (#66): the egress path is IPv4-only, and IPv6 is denied by
// construction rather than by rule. ipBlock CIDRs match a single family, and no rendered
// policy contains an IPv6 allow (no ::/0 block, no v6 excepts) — so on a dual-stack
// cluster any pod selected by these policies has zero permitted IPv6 egress: the routing
// lock is selector-based (family-agnostic, Envoy-only), the backstop and denied-CIDR
// paths allow only 0.0.0.0/0-based traffic, and Envoy itself resolves V4_ONLY. Opening
// IPv6 egress (a ::/0-except companion rule + Envoy dns_lookup_family) is a deliberate
// future posture change, not a config knob.
package networkpolicy

import (
	"strings"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	scrutineerjob "github.com/grantbarry29/scrutineer/internal/controller/job"
	"github.com/grantbarry29/scrutineer/internal/enforcement"
	"github.com/grantbarry29/scrutineer/internal/enforcement/envoy"
)

const (
	NamePrefix            = "scrutineer-netpol-"
	BackstopNamePrefix    = "scrutineer-egress-backstop-"
	LabelEnforcement      = "scrutineer.sh/enforcement"
	EnforcementNetworkPol = "networkpolicy"
)

// DefaultBackstopCIDRs are the egress ranges denied to the Envoy proxy pod even though it
// otherwise egresses freely — a defense in depth that holds if Envoy is compromised. The
// default is the link-local block, which covers the cloud metadata endpoint (169.254.169.254)
// on AWS/GCP/Azure. Cluster/service/API CIDRs are environment-specific (see
// docs/design/evidence-integrity.md §8) and are added by operators via configuration.
// No IPv6 entries (fe80::/10, AWS's fd00:ec2::254) are needed: the backstop policy denies
// ALL IPv6 egress by construction (#66 posture) — v6 metadata endpoints are unreachable
// wholesale, not per-range.
var DefaultBackstopCIDRs = []string{"169.254.0.0/16"}

// Reporter endpoint identity for the backstop's evidence-channel allow rule. Kept in
// sync with job.DefaultReporterURL (scrutineer-controller-reporter.scrutineer-system
// .svc:8088) — the observed-evidence channel (Slice C, #62) must survive any operator
// backstop CIDRs, so the Envoy pod always keeps this explicit allow.
const (
	ReporterNamespace = "scrutineer-system"
	ReporterPort      = 8088
)

// reporterEgressRule allows the egress-proxy pod to reach the reporter namespace on the
// reporter port regardless of backstopped CIDRs (NetworkPolicy rules are additive
// allows; namespace-selector peers are unaffected by ipBlock excepts).
func reporterEgressRule() networkingv1.NetworkPolicyEgressRule {
	port := intstr.FromInt32(ReporterPort)
	tcp := corev1.ProtocolTCP
	return networkingv1.NetworkPolicyEgressRule{
		To: []networkingv1.NetworkPolicyPeer{{
			NamespaceSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"kubernetes.io/metadata.name": ReporterNamespace},
			},
		}},
		Ports: []networkingv1.NetworkPolicyPort{{Protocol: &tcp, Port: &port}},
	}
}

// HasCIDRRules reports whether policy contains CIDR hints this backend can render.
func HasCIDRRules(rules scrutineerv1alpha1.PolicyRules) bool {
	return len(rules.AllowedCIDRs) > 0 || len(rules.DeniedCIDRs) > 0
}

// Applicable reports whether a restrictive NetworkPolicy should be reconciled.
func Applicable(ctx enforcement.SessionContext) bool {
	if ctx.Mode != scrutineerv1alpha1.PolicyModeEnforced {
		return false
	}
	return HasCIDRRules(ctx.Policy)
}

// NameFor returns the deterministic NetworkPolicy name for a session.
func NameFor(sessionNamespace, sessionName string) string {
	return NamePrefix + sessionName
}

// BackstopNameFor returns the deterministic name of the Envoy-pod egress backstop policy.
func BackstopNameFor(sessionNamespace, sessionName string) string {
	return BackstopNamePrefix + sessionName
}

// BuildEgressProxyBackstop renders the egress policy for a session's Envoy proxy pod: it may
// resolve DNS and reach the internet, but the backstopCIDRs (cloud metadata by default, plus
// any operator-added cluster/service/API ranges) are denied — even a compromised Envoy can't
// reach the metadata endpoint or pivot into those ranges. Returns nil when there is nothing
// to deny (no backstops), leaving the proxy pod's egress unrestricted.
//
// Dual-stack posture (#66): the egress path is IPv4-only. This policy deliberately contains
// no ::/0 (or any IPv6) allow rule, so on a dual-stack cluster its existence default-denies
// ALL IPv6 egress from the Envoy pod. IPv6 backstop entries are therefore satisfied
// wholesale and must never be placed in the IPv4 block's except list — the apiserver
// rejects cross-family excepts, which would break enforcement provisioning entirely.
func BuildEgressProxyBackstop(ctx enforcement.SessionContext, backstopCIDRs []string) *networkingv1.NetworkPolicy {
	cidrs := make([]string, 0, len(backstopCIDRs))
	for _, c := range backstopCIDRs {
		if c = strings.TrimSpace(c); c != "" {
			cidrs = append(cidrs, normalizeCIDR(c))
		}
	}
	if len(cidrs) == 0 {
		return nil
	}
	// An IPv6-only backstop list still renders: dropping the policy would leave the proxy
	// pod unrestricted in BOTH families; rendering it denies all v6 and leaves v4 free.
	except := ipv4Only(cidrs)
	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      BackstopNameFor(ctx.SessionNamespace, ctx.SessionName),
			Namespace: ctx.SessionNamespace,
			Labels:    labelsFor(ctx.SessionName),
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: envoy.Labels(ctx.SessionName)},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
			Egress: []networkingv1.NetworkPolicyEgressRule{
				// Keep DNS reachable even if cluster ranges are backstopped, so Envoy can
				// still resolve upstreams.
				dnsEgressRule(),
				// Keep the observed-evidence channel to the reporter open regardless of
				// backstopped CIDRs (Slice C, #62).
				reporterEgressRule(),
				{To: []networkingv1.NetworkPolicyPeer{{
					IPBlock: &networkingv1.IPBlock{CIDR: "0.0.0.0/0", Except: except},
				}}},
			},
		},
	}
}

// Build renders the desired NetworkPolicy for a session context, or nil when not applicable.
//
// When the per-session Envoy egress proxy is enabled, the agent pod gets the mandatory
// routing lock (buildEgressLock) — the Envoy chokepoint is the only reachable egress, so
// the lock takes precedence over the legacy coarse CIDR policy and applies regardless of
// policy mode (the chokepoint must hold even in audit mode so egress is observable).
func Build(ctx enforcement.SessionContext) *networkingv1.NetworkPolicy {
	if egressProxyEnabled(ctx) {
		return buildEgressLock(ctx)
	}

	if !Applicable(ctx) {
		return nil
	}

	egress := []networkingv1.NetworkPolicyEgressRule{dnsEgressRule()}
	egress = append(egress, cidrEgressRules(ctx.Policy)...)

	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      NameFor(ctx.SessionNamespace, ctx.SessionName),
			Namespace: ctx.SessionNamespace,
			Labels:    labelsFor(ctx.SessionName),
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					scrutineerjob.LabelSessionRef: ctx.SessionName,
				},
			},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
			Egress:      egress,
		},
	}
}

// egressProxyEnabled reports whether the session's RuntimeProfile enables the out-of-pod
// Envoy egress proxy (the EnforcementTypeEnvoy toggle; see internal/controller/agentsession/egress_envoy.go).
func egressProxyEnabled(ctx enforcement.SessionContext) bool {
	for _, sc := range ctx.Enforcement {
		if sc.Type == scrutineerjob.EnforcementTypeEnvoy && (sc.Enabled == nil || *sc.Enabled) {
			return true
		}
	}
	return false
}

// buildEgressLock renders the mandatory default-deny egress policy for the agent pod: the
// ONLY permitted egress is to the session's Envoy pod on the proxy port. There is no DNS
// allowance — the agent reaches Envoy by ClusterIP and Envoy performs all name resolution,
// which closes the direct-DNS exfil path (Slice B, #61). Backstops on the Envoy pod's own
// egress are added separately (Slice B B3).
func buildEgressLock(ctx enforcement.SessionContext) *networkingv1.NetworkPolicy {
	tcp := corev1.ProtocolTCP
	proxyPort := intstr.FromInt32(int32(envoy.ProxyPort))
	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      NameFor(ctx.SessionNamespace, ctx.SessionName),
			Namespace: ctx.SessionNamespace,
			Labels:    labelsFor(ctx.SessionName),
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					scrutineerjob.LabelSessionRef: ctx.SessionName,
				},
			},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
			Egress: []networkingv1.NetworkPolicyEgressRule{{
				To: []networkingv1.NetworkPolicyPeer{{
					PodSelector: &metav1.LabelSelector{MatchLabels: envoy.Labels(ctx.SessionName)},
				}},
				Ports: []networkingv1.NetworkPolicyPort{{Protocol: &tcp, Port: &proxyPort}},
			}},
		},
	}
}

func labelsFor(sessionName string) map[string]string {
	return map[string]string{
		scrutineerjob.LabelAppName:      scrutineerjob.AppNameScrutineer,
		scrutineerjob.LabelAppComponent: scrutineerjob.ComponentSession,
		scrutineerjob.LabelSessionRef:   sessionName,
		LabelEnforcement:                EnforcementNetworkPol,
	}
}

func dnsEgressRule() networkingv1.NetworkPolicyEgressRule {
	port53 := intstr.FromInt32(53)
	udp := corev1.ProtocolUDP
	tcp := corev1.ProtocolTCP
	return networkingv1.NetworkPolicyEgressRule{
		To: []networkingv1.NetworkPolicyPeer{{NamespaceSelector: &metav1.LabelSelector{}}},
		Ports: []networkingv1.NetworkPolicyPort{
			{Protocol: &udp, Port: &port53},
			{Protocol: &tcp, Port: &port53},
		},
	}
}

func cidrEgressRules(rules scrutineerv1alpha1.PolicyRules) []networkingv1.NetworkPolicyEgressRule {
	if len(rules.AllowedCIDRs) > 0 {
		out := make([]networkingv1.NetworkPolicyEgressRule, 0, len(rules.AllowedCIDRs))
		for _, cidr := range rules.AllowedCIDRs {
			out = append(out, networkingv1.NetworkPolicyEgressRule{
				To: []networkingv1.NetworkPolicyPeer{{
					IPBlock: &networkingv1.IPBlock{CIDR: normalizeCIDR(cidr)},
				}},
			})
		}
		return out
	}

	// IPv6 denies never enter the IPv4 block's except list (apiserver-invalid); they are
	// satisfied by this policy's default-deny of all v6 — no rule allows v6 destinations
	// (#66 posture: IPv4-only egress).
	denied := make([]string, 0, len(rules.DeniedCIDRs))
	for _, cidr := range rules.DeniedCIDRs {
		denied = append(denied, normalizeCIDR(cidr))
	}
	return []networkingv1.NetworkPolicyEgressRule{{
		To: []networkingv1.NetworkPolicyPeer{{
			IPBlock: &networkingv1.IPBlock{CIDR: "0.0.0.0/0", Except: ipv4Only(denied)},
		}},
	}}
}

// ipv4Only filters normalized CIDRs down to the IPv4 family. IPv6 entries are handled by
// omission: every rendered policy is IPv4-only, so selected pods have no allowed IPv6
// egress at all (see the package doc and BuildEgressProxyBackstop).
func ipv4Only(cidrs []string) []string {
	out := make([]string, 0, len(cidrs))
	for _, c := range cidrs {
		if !strings.Contains(c, ":") {
			out = append(out, c)
		}
	}
	return out
}

func normalizeCIDR(cidr string) string {
	cidr = strings.TrimSpace(cidr)
	if cidr == "" {
		return cidr
	}
	if strings.Contains(cidr, "/") {
		return cidr
	}
	if strings.Contains(cidr, ":") {
		return cidr + "/128"
	}
	return cidr + "/32"
}
