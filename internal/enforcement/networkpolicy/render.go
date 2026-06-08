/*
Copyright 2026 The Relay Authors.

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
package networkpolicy

import (
	"strings"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
	relayjob "github.com/secureai/relay/internal/controller/job"
	"github.com/secureai/relay/internal/enforcement"
)

const (
	NamePrefix            = "relay-netpol-"
	LabelEnforcement      = "relay.secureai.dev/enforcement"
	EnforcementNetworkPol = "networkpolicy"
)

// HasCIDRRules reports whether policy contains CIDR hints this backend can render.
func HasCIDRRules(rules relayv1alpha1.PolicyRules) bool {
	return len(rules.AllowedCIDRs) > 0 || len(rules.DeniedCIDRs) > 0
}

// Applicable reports whether a restrictive NetworkPolicy should be reconciled.
func Applicable(ctx enforcement.SessionContext) bool {
	if ctx.Mode != relayv1alpha1.PolicyModeEnforced {
		return false
	}
	return HasCIDRRules(ctx.Policy)
}

// NameFor returns the deterministic NetworkPolicy name for a session.
func NameFor(sessionNamespace, sessionName string) string {
	return NamePrefix + sessionName
}

// Build renders the desired NetworkPolicy for a session context, or nil when not applicable.
func Build(ctx enforcement.SessionContext) *networkingv1.NetworkPolicy {
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
					relayjob.LabelSessionRef: ctx.SessionName,
				},
			},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
			Egress:      egress,
		},
	}
}

func labelsFor(sessionName string) map[string]string {
	return map[string]string{
		relayjob.LabelAppName:      relayjob.AppNameRelay,
		relayjob.LabelAppComponent: relayjob.ComponentSession,
		relayjob.LabelSessionRef:   sessionName,
		LabelEnforcement:           EnforcementNetworkPol,
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

func cidrEgressRules(rules relayv1alpha1.PolicyRules) []networkingv1.NetworkPolicyEgressRule {
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

	except := make([]string, 0, len(rules.DeniedCIDRs))
	for _, cidr := range rules.DeniedCIDRs {
		except = append(except, normalizeCIDR(cidr))
	}
	return []networkingv1.NetworkPolicyEgressRule{{
		To: []networkingv1.NetworkPolicyPeer{{
			IPBlock: &networkingv1.IPBlock{CIDR: "0.0.0.0/0", Except: except},
		}},
	}}
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
