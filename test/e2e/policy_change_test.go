//go:build e2e

/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package e2e

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/controller/agentsession"
	"github.com/grantbarry29/scrutineer/internal/enforcement/envoy"
)

// #148 live proof: "operator tightens the allowlist while the agent is running" — the
// core mid-session governance scenario. The chain under test: AgentPolicy update →
// policy watch requeues the session → egress ConfigMap updated in place → proxy pod
// deleted and recreated with the new rules (Envoy reads its bootstrap once; no hot
// reload) → the live Envoy denies the newly-denied host → observed decisions flip
// allow→deny — while the agent runtime itself is NOT restarted (Job pod templates are
// immutable with pods active; the drift is surfaced via PolicyPropagated=False), and
// the churn window stays fail-closed (the routing lock admits only Envoy, so a proxy
// being replaced means no egress, never open egress).
//
// #116 migration guard: when pod-recreate propagation is replaced by xDS hot-reload,
// this spec must stay green — it asserts propagation outcomes (new config active,
// decisions flip, fail-closed window), not the recreate mechanism itself, except for
// the pod-identity assertion which documents today's mechanism.
var _ = Describe("Mid-session policy change propagation", Label(labelNetworking), func() {
	BeforeEach(func(ctx SpecContext) {
		requireScrutineerE2EImage(ctx)
		if !clusterImageRunnable(ctx, envoy.DefaultEnvoyImage) {
			Skip("envoy image not available — run: make kind-load-envoy")
		}
		if !clusterImageRunnable(ctx, envoy.DefaultEgressReporterImage()) {
			Skip("egress-reporter image not available — run: make kind-load-egress-reporter")
		}
		deployInClusterReporter(ctx)
	})

	const probeHost = "live.scrutineer.invalid"

	It("propagates a mid-run tightening: proxy recreated, observed decisions flip allow→deny, env drift surfaced, churn window fail-closed", func(ctx SpecContext) {
		// Enforced-mode egress sessions require a verified routing lock (#70).
		requireEgressEnforcingCNI(ctx)
		ns := newTestNamespace("scrutineer-e2e-polchange")
		createRuntimeProfileWithEnvoy(ctx, ns, "envoy-egress")
		// Enforced from the start but denying only an unrelated host, so probeHost is
		// allowed and the mid-run change is a pure rule tightening under constant mode.
		createFQDNDenyPolicy(ctx, ns, "tighten", scrutineerv1alpha1.PolicyModeEnforced, "unrelated.scrutineer.invalid")

		targetIP := kubeDNSPodIP(ctx)
		Expect(targetIP).NotTo(BeEmpty(), "need a kube-dns pod IP as a non-Envoy egress target")

		session := newAgentSession(ns, "policy-tighten",
			withRuntimeProfileRef("envoy-egress"),
			withPolicyRef("AgentPolicy", "tighten"),
			withContinuousEgressProbe(probeHost),
		)
		session.Spec.Runtime.Env = append(session.Spec.Runtime.Env,
			corev1.EnvVar{Name: "PROBE_TARGET_IP", Value: targetIP})
		key := createAgentSession(ctx, session)

		expectJobForSession(ctx, ns, session)
		waitForPhase(ctx, key, []scrutineerv1alpha1.AgentSessionPhase{scrutineerv1alpha1.PhaseRunning},
			90*time.Second, 2*time.Second)
		egressKey := envoyKey(ns, session.Name)
		waitForEnvoyPodReady(ctx, egressKey)

		By("observed ALLOW decisions flowing for the still-allowed host")
		expectObservedDecision(ctx, key, probeHost, scrutineerv1alpha1.PolicyDecisionAllow)

		pre := getSession(ctx, key)
		Expect(pre.Status.PodName).NotTo(BeEmpty(), "running session should expose its agent pod")
		agentPod := pre.Status.PodName
		var oldProxy corev1.Pod
		Expect(k8sClient.Get(ctx, egressKey, &oldProxy)).To(Succeed())
		oldUID := oldProxy.UID
		oldHash := oldProxy.Annotations[envoy.ConfigHashAnnotation]
		Expect(oldHash).NotTo(BeEmpty(), "proxy pod should carry the egress-config hash annotation")

		By("tightening the policy mid-run to deny the host")
		updateAgentPolicy(ctx, client.ObjectKey{Namespace: ns, Name: "tighten"}, func(p *scrutineerv1alpha1.AgentPolicy) {
			p.Spec.PolicyRules.DeniedDomains = append(p.Spec.PolicyRules.DeniedDomains, probeHost)
		})

		By("the proxy pod being recreated with the new config (new UID, new hash annotation)")
		Eventually(func(g Gomega) {
			var pod corev1.Pod
			g.Expect(k8sClient.Get(ctx, egressKey, &pod)).To(Succeed())
			g.Expect(pod.UID).NotTo(Equal(oldUID), "proxy pod was not recreated on policy change")
			g.Expect(pod.Annotations[envoy.ConfigHashAnnotation]).NotTo(Equal(oldHash),
				"recreated proxy pod does not carry the updated config hash")
		}, 90*time.Second, 2*time.Second).Should(Succeed())
		waitForEnvoyPodReady(ctx, egressKey)

		By("the running agent surfacing policy-env drift without being restarted")
		Eventually(func(g Gomega) {
			got := getSession(ctx, key)
			cond := getCondition(&got, agentsession.ConditionPolicyPropagated)
			g.Expect(cond).NotTo(BeNil(), "PolicyPropagated condition missing")
			g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(cond.Reason).To(Equal("PolicyEnvDrift"))
			g.Expect(got.Status.PodName).To(Equal(agentPod),
				"a policy change must not restart the running agent")
		}, 60*time.Second, 2*time.Second).Should(Succeed())

		By("observed decisions flipping to DENY under the new rules")
		expectObservedDecision(ctx, key, probeHost, scrutineerv1alpha1.PolicyDecisionDeny)

		By("pre-change ALLOW evidence surviving the proxy churn")
		post := getSession(ctx, key)
		var allows, denies int
		for _, d := range post.Status.PolicyDecisions {
			if d.Type != "network" || d.Actor != envoy.AccessLogActor {
				continue
			}
			if d.Target != probeHost && d.Target != probeHost+":443" {
				continue
			}
			Expect(d.AssuranceLevel).To(Equal(scrutineerv1alpha1.EvidenceObserved))
			switch d.Action {
			case scrutineerv1alpha1.PolicyDecisionAllow:
				allows++
			case scrutineerv1alpha1.PolicyDecisionDeny:
				denies++
			}
		}
		Expect(allows).To(BeNumerically(">", 0), "pre-change allow decisions lost across proxy churn")
		Expect(denies).To(BeNumerically(">", 0), "no post-change deny decisions recorded")

		By("no direct egress having escaped at any point, including the churn window (probe markers)")
		Eventually(func(g Gomega) {
			logs := agentPodLog(ctx, key)
			// Positive control: the probe reached Envoy at least once, so the BLOCKED
			// negatives below are real denies, not a broken probe.
			g.Expect(logs).To(ContainSubstring("PROBE_ENVOY_TCP=OK"),
				"agent never reached its Envoy — probe broken, negatives meaningless")
			g.Expect(logs).To(ContainSubstring("PROBE_DNS=BLOCKED"))
			g.Expect(logs).NotTo(ContainSubstring("PROBE_DNS=OK"),
				"direct DNS escaped — churn window not fail-closed")
			g.Expect(logs).To(ContainSubstring("PROBE_DIRECT=BLOCKED"))
			g.Expect(logs).NotTo(ContainSubstring("PROBE_DIRECT=OK"),
				"direct egress escaped — churn window not fail-closed")
		}, 60*time.Second, 2*time.Second).Should(Succeed())

		requestCancellation(ctx, key)
		waitForTerminalPhase(ctx, key, scrutineerv1alpha1.PhaseCancelled)
	})
})
