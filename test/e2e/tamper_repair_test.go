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
	"context"
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/enforcement/envoy"
)

// Tamper repair (#147): the untamperable-enforcement doctrine's adversary is an
// in-cluster actor weakening a per-session chokepoint mid-run — exactly what the
// controller's Owns() watches were built to repair. These specs delete and mutate the
// enforcement objects of LIVE sessions and prove (a) every object is restored to its
// rendered state, (b) the agent stays fail-closed wherever the surviving objects
// guarantee it, and (c) where a tamper genuinely opens egress (routing lock removed or
// weakened), the exposure is bounded and measured.
//
// Exposure accounting, deliberately: watch-driven repair typically lands within a
// second, while a probe cycle takes several — asserting "no OK marker EVER" would fail
// a meaningful fraction of runs purely on probe/window overlap. Instead each lock
// tamper asserts a hard post-repair guarantee (two full probe cycles after the repair
// report only BLOCKED) plus a measured whole-run exposure bound (at most 2 OK markers
// per probe type — i.e. the window stays shorter than two probe cycles), with the
// observed counts printed to the spec log. The proxy-pod tamper never touches the lock,
// so there the strict never-OK assertion holds for the entire run: losing the
// chokepoint fails closed, it does not fail open.
var _ = Describe("Live tamper repair of enforcement objects", Label(labelNetworking), func() {
	BeforeEach(func(ctx SpecContext) {
		requireEgressEnforcingCNI(ctx)
		if !clusterImageRunnable(ctx, envoy.DefaultEnvoyImage) {
			Skip("envoy image not available in cluster — run: make kind-load-envoy")
		}
	})

	It("reverts a weakened routing lock and recreates a deleted one, with bounded exposure", func(ctx SpecContext) {
		ns := newTestNamespace("scrutineer-e2e-tamper")
		createRuntimeProfileWithEnvoy(ctx, ns, "envoy-egress")

		targetIP := kubeDNSPodIP(ctx)
		Expect(targetIP).NotTo(BeEmpty(), "need a kube-dns pod IP as a non-Envoy egress target")

		const probeHost = "tamper-lock.scrutineer.invalid"
		session := newAgentSession(ns, "tamper-lock",
			withRuntimeProfileRef("envoy-egress"),
			withTamperEgressProbe(probeHost),
		)
		session.Spec.Runtime.Env = append(session.Spec.Runtime.Env,
			corev1.EnvVar{Name: "PROBE_TARGET_IP", Value: targetIP})
		key := createAgentSession(ctx, session)

		expectJobForSession(ctx, ns, session)
		waitForPhase(ctx, key, []scrutineerv1alpha1.AgentSessionPhase{scrutineerv1alpha1.PhaseRunning}, 90*time.Second, 2*time.Second)
		waitForEnvoyPodReady(ctx, types.NamespacedName{Namespace: ns, Name: envoy.ResourceName(session.Name)})

		By("baseline: the lock enforcing and the probe healthy")
		expectLockBaselineMarkers(ctx, key)

		lockKey := client.ObjectKey{Namespace: ns, Name: netpolNameForSession(session)}
		var lock networkingv1.NetworkPolicy
		Expect(k8sClient.Get(ctx, lockKey, &lock)).To(Succeed())
		renderedEgress := lock.Spec.DeepCopy().Egress
		lockUID := lock.UID

		By("tamper 1: injecting an allow-all egress rule into the lock")
		// Retry the whole get-mutate-update on conflict: the controller may write the
		// object between our read and write.
		Eventually(func(g Gomega) {
			var np networkingv1.NetworkPolicy
			g.Expect(k8sClient.Get(ctx, lockKey, &np)).To(Succeed())
			np.Spec.Egress = append(np.Spec.Egress, networkingv1.NetworkPolicyEgressRule{})
			g.Expect(k8sClient.Update(ctx, &np)).To(Succeed())
		}, 15*time.Second, time.Second).Should(Succeed())
		mutated := time.Now()

		By("the controller reverting the lock to its rendered spec")
		Eventually(func(g Gomega) {
			var np networkingv1.NetworkPolicy
			g.Expect(k8sClient.Get(ctx, lockKey, &np)).To(Succeed())
			g.Expect(np.Spec.Egress).To(Equal(renderedEgress), "allow-all rule not reverted")
		}, 30*time.Second, 200*time.Millisecond).Should(Succeed())
		GinkgoWriter.Printf("routing lock reverted %v after mutation\n", time.Since(mutated).Round(100*time.Millisecond))

		By("direct egress staying blocked once the revert lands")
		expectProbesBlockedAfterRepair(ctx, key)

		By("tamper 2: deleting the routing lock outright")
		Expect(k8sClient.Get(ctx, lockKey, &lock)).To(Succeed())
		Expect(k8sClient.Delete(ctx, &lock)).To(Succeed())
		deleted := time.Now()

		By("the controller recreating the lock with the rendered spec")
		Eventually(func(g Gomega) {
			var np networkingv1.NetworkPolicy
			g.Expect(k8sClient.Get(ctx, lockKey, &np)).To(Succeed())
			g.Expect(np.UID).NotTo(Equal(lockUID), "old lock still present, not a recreation")
			g.Expect(np.Spec.Egress).To(Equal(renderedEgress))
		}, 30*time.Second, 200*time.Millisecond).Should(Succeed())
		GinkgoWriter.Printf("routing lock recreated %v after deletion\n", time.Since(deleted).Round(100*time.Millisecond))

		By("direct egress staying blocked once the recreation lands")
		expectProbesBlockedAfterRepair(ctx, key)

		By("measuring whole-run exposure across both tamper windows")
		finalLog := agentPodLog(ctx, key)
		okDNS := strings.Count(finalLog, "PROBE_DNS=OK")
		okDirect := strings.Count(finalLog, "PROBE_DIRECT=OK")
		GinkgoWriter.Printf("exposure markers across both tampers: PROBE_DNS=OK ×%d, PROBE_DIRECT=OK ×%d\n", okDNS, okDirect)
		Expect(okDNS).To(BeNumerically("<=", 2),
			"repair window exceeded two probe cycles — DNS egress was open too long:\n%s", finalLog)
		Expect(okDirect).To(BeNumerically("<=", 2),
			"repair window exceeded two probe cycles — direct egress was open too long:\n%s", finalLog)

		requestCancellation(ctx, key)
		waitForTerminalPhase(ctx, key, scrutineerv1alpha1.PhaseCancelled)
	})

	It("re-provisions a deleted egress ConfigMap and proxy pod; direct egress never opens", func(ctx SpecContext) {
		ns := newTestNamespace("scrutineer-e2e-tamper")
		createRuntimeProfileWithEnvoy(ctx, ns, "envoy-egress")

		targetIP := kubeDNSPodIP(ctx)
		Expect(targetIP).NotTo(BeEmpty(), "need a kube-dns pod IP as a non-Envoy egress target")

		const probeHost = "tamper-proxy.scrutineer.invalid"
		session := newAgentSession(ns, "tamper-proxy",
			withRuntimeProfileRef("envoy-egress"),
			withTamperEgressProbe(probeHost),
		)
		session.Spec.Runtime.Env = append(session.Spec.Runtime.Env,
			corev1.EnvVar{Name: "PROBE_TARGET_IP", Value: targetIP})
		key := createAgentSession(ctx, session)

		expectJobForSession(ctx, ns, session)
		waitForPhase(ctx, key, []scrutineerv1alpha1.AgentSessionPhase{scrutineerv1alpha1.PhaseRunning}, 90*time.Second, 2*time.Second)
		egressKey := types.NamespacedName{Namespace: ns, Name: envoy.ResourceName(session.Name)}
		waitForEnvoyPodReady(ctx, egressKey)

		By("baseline: the lock enforcing and the probe healthy")
		expectLockBaselineMarkers(ctx, key)

		var cm corev1.ConfigMap
		Expect(k8sClient.Get(ctx, egressKey, &cm)).To(Succeed())
		cmUID := cm.UID
		renderedHash := cm.Annotations[envoy.ConfigHashAnnotation]
		Expect(renderedHash).NotTo(BeEmpty(), "egress ConfigMap missing its config-hash annotation")

		var pod corev1.Pod
		Expect(k8sClient.Get(ctx, egressKey, &pod)).To(Succeed())
		podUID := pod.UID

		By("tamper 1: deleting the egress ConfigMap")
		Expect(k8sClient.Delete(ctx, &cm)).To(Succeed())

		By("the controller recreating the ConfigMap with the identical config hash")
		Eventually(func(g Gomega) {
			var got corev1.ConfigMap
			g.Expect(k8sClient.Get(ctx, egressKey, &got)).To(Succeed())
			g.Expect(got.UID).NotTo(Equal(cmUID), "old ConfigMap still present, not a recreation")
			g.Expect(got.Annotations[envoy.ConfigHashAnnotation]).To(Equal(renderedHash),
				"recreated ConfigMap carries a different config hash")
		}, 30*time.Second, 500*time.Millisecond).Should(Succeed())

		By("the proxy pod NOT being recycled for an unchanged config hash")
		Consistently(func(g Gomega) {
			var got corev1.Pod
			g.Expect(k8sClient.Get(ctx, egressKey, &got)).To(Succeed())
			g.Expect(got.UID).To(Equal(podUID), "proxy pod recycled although the config hash did not change")
		}, 10*time.Second, 2*time.Second).Should(Succeed())

		By("tamper 2: deleting the egress proxy pod")
		Expect(k8sClient.Delete(ctx, &pod, client.GracePeriodSeconds(0))).To(Succeed())

		By("the controller re-provisioning the proxy pod")
		Eventually(func(g Gomega) {
			var got corev1.Pod
			g.Expect(k8sClient.Get(ctx, egressKey, &got)).To(Succeed())
			g.Expect(got.UID).NotTo(Equal(podUID), "old proxy pod still present, not a re-provision")
		}, 90*time.Second, time.Second).Should(Succeed())
		waitForEnvoyPodReady(ctx, egressKey)

		By("agent egress resuming through the NEW chokepoint (fresh access log)")
		// The replacement pod's log starts empty, so any probe-host entry is post-repair
		// traffic by construction.
		Eventually(func(g Gomega) {
			g.Expect(envoyAccessLog(ctx, egressKey)).To(ContainSubstring(probeHost),
				"agent egress did not resume through the recreated Envoy")
		}, 120*time.Second, 3*time.Second).Should(Succeed())

		By("the agent-side proxy probe recovering")
		recovered := agentPodLog(ctx, key)
		Eventually(func(g Gomega) {
			l := agentPodLog(ctx, key)
			g.Expect(strings.Count(l, "PROBE_ENVOY_TCP=OK")).To(
				BeNumerically(">", strings.Count(recovered, "PROBE_ENVOY_TCP=OK")),
				"agent probe has not reached the recreated Envoy")
		}, 90*time.Second, 3*time.Second).Should(Succeed())

		By("direct egress NEVER having opened — losing the chokepoint fails closed")
		// The routing lock was never touched, so this holds strictly across the entire
		// run, including the window with no proxy pod at all.
		finalLog := agentPodLog(ctx, key)
		Expect(finalLog).To(ContainSubstring("PROBE_DNS=BLOCKED"))
		Expect(finalLog).To(ContainSubstring("PROBE_DIRECT=BLOCKED"))
		Expect(finalLog).NotTo(ContainSubstring("PROBE_DNS=OK"),
			"direct DNS opened during the proxy outage — fail-closed violated")
		Expect(finalLog).NotTo(ContainSubstring("PROBE_DIRECT=OK"),
			"direct egress opened during the proxy outage — fail-closed violated")

		requestCancellation(ctx, key)
		waitForTerminalPhase(ctx, key, scrutineerv1alpha1.PhaseCancelled)
	})

	It("preserves observed evidence across a proxy-pod tamper and records post-repair egress", func(ctx SpecContext) {
		requireScrutineerE2EImage(ctx)
		if !clusterImageRunnable(ctx, envoy.DefaultEgressReporterImage()) {
			Skip("egress-reporter image not available in cluster — run: make kind-load-egress-reporter")
		}
		deployInClusterReporter(ctx)

		ns := newTestNamespace("scrutineer-e2e-tamper")
		createRuntimeProfileWithEnvoy(ctx, ns, "envoy-egress")

		targetIP := kubeDNSPodIP(ctx)
		Expect(targetIP).NotTo(BeEmpty(), "need a kube-dns pod IP as a non-Envoy egress target")

		const probeHost = "tamper-evidence.scrutineer.invalid"
		// The standard probe (not the tamper fixture): one wget per cycle at a slower
		// cadence keeps the session's total decision volume well under the
		// MaxPolicyDecisions cap, so the pre-tamper snapshot cannot be truncated away.
		session := newAgentSession(ns, "tamper-evidence",
			withRuntimeProfileRef("envoy-egress"),
			withNetpolEgressProbe(probeHost),
		)
		session.Spec.Runtime.Env = append(session.Spec.Runtime.Env,
			corev1.EnvVar{Name: "PROBE_TARGET_IP", Value: targetIP})
		key := createAgentSession(ctx, session)

		expectJobForSession(ctx, ns, session)
		waitForPhase(ctx, key, []scrutineerv1alpha1.AgentSessionPhase{scrutineerv1alpha1.PhaseRunning}, 90*time.Second, 2*time.Second)
		egressKey := types.NamespacedName{Namespace: ns, Name: envoy.ResourceName(session.Name)}
		waitForEnvoyPodReady(ctx, egressKey)

		By("pre-tamper observed decisions for the probe host appearing in status")
		preKeys := map[string]bool{}
		Eventually(func(g Gomega) {
			got := getSession(ctx, key)
			decisions := observedEgressDecisions(got, probeHost)
			g.Expect(decisions).NotTo(BeEmpty(),
				"no observed egress decisions before the tamper; decisions=%+v", got.Status.PolicyDecisions)
			for _, d := range decisions {
				preKeys[tamperDecisionKey(d)] = true
			}
		}, 180*time.Second, 3*time.Second).Should(Succeed())

		By("tampering: deleting the egress proxy pod (reporter dies with it)")
		var pod corev1.Pod
		Expect(k8sClient.Get(ctx, egressKey, &pod)).To(Succeed())
		podUID := pod.UID
		Expect(k8sClient.Delete(ctx, &pod, client.GracePeriodSeconds(0))).To(Succeed())

		Eventually(func(g Gomega) {
			var got corev1.Pod
			g.Expect(k8sClient.Get(ctx, egressKey, &got)).To(Succeed())
			g.Expect(got.UID).NotTo(Equal(podUID), "old proxy pod still present, not a re-provision")
		}, 90*time.Second, time.Second).Should(Succeed())
		waitForEnvoyPodReady(ctx, egressKey)

		By("pre-tamper decisions surviving and NEW observed decisions arriving post-repair")
		Eventually(func(g Gomega) {
			got := getSession(ctx, key)
			decisions := observedEgressDecisions(got, probeHost)
			keys := map[string]bool{}
			for _, d := range decisions {
				// Identity-derived assurance must survive the pod swap: every decision
				// from the (new) proxy identity is still stamped observed.
				g.Expect(d.AssuranceLevel).To(Equal(scrutineerv1alpha1.EvidenceObserved),
					"egress-proxy decision not stamped observed: %+v", d)
				keys[tamperDecisionKey(d)] = true
			}
			for k := range preKeys {
				g.Expect(keys).To(HaveKey(k), "pre-tamper decision lost from status: %s", k)
			}
			novel := 0
			for k := range keys {
				if !preKeys[k] {
					novel++
				}
			}
			g.Expect(novel).To(BeNumerically(">=", 1),
				"no new observed decision after the proxy was re-provisioned")
		}, 240*time.Second, 5*time.Second).Should(Succeed())

		requestCancellation(ctx, key)
		waitForTerminalPhase(ctx, key, scrutineerv1alpha1.PhaseCancelled)
	})
})

// withTamperEgressProbe is withNetpolEgressProbe on a faster clock: shorter probe
// timeouts and sleep give a ~7–13s cycle (vs ~18s), which tightens the exposure
// measurement around a tamper window, and 150 iterations keep markers flowing through
// two sequential tamper/repair rounds in one session.
func withTamperEgressProbe(host string) agentSessionOption {
	return func(s *scrutineerv1alpha1.AgentSession) {
		script := fmt.Sprintf(`sleep 12
ENVOY_IP=$(printf '%%s' "${http_proxy:-$HTTP_PROXY}" | sed 's|^http://||; s|:.*$||')
i=0
while [ $i -lt 150 ]; do
  i=$((i+1))
  wget -q -O /dev/null -T 2 "http://%[1]s/" 2>/dev/null || true
  if timeout 3 nc -w 2 "$ENVOY_IP" 15001 </dev/null >/dev/null 2>&1; then echo "PROBE_ENVOY_TCP=OK"; else echo "PROBE_ENVOY_TCP=FAIL"; fi
  if timeout 3 nslookup kubernetes.default.svc.cluster.local >/dev/null 2>&1; then echo "PROBE_DNS=OK"; else echo "PROBE_DNS=BLOCKED"; fi
  if timeout 3 nc -w 2 "$PROBE_TARGET_IP" 53 </dev/null >/dev/null 2>&1; then echo "PROBE_DIRECT=OK"; else echo "PROBE_DIRECT=BLOCKED"; fi
  sleep 2
done
sleep 300`, host)
		s.Spec.Runtime.Command = []string{"sh", "-c", script}
	}
}

// expectLockBaselineMarkers waits until the agent's probe demonstrates both probe
// health (Envoy reachable, so a BLOCKED verdict is a real deny) and live enforcement
// (direct DNS and direct connect both blocked).
func expectLockBaselineMarkers(ctx context.Context, key client.ObjectKey) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		logs := agentPodLog(ctx, key)
		g.Expect(logs).To(ContainSubstring("PROBE_ENVOY_TCP=OK"),
			"agent could not reach its Envoy — probe/connectivity broken, negatives are meaningless")
		g.Expect(logs).To(ContainSubstring("PROBE_DNS=BLOCKED"))
		g.Expect(logs).To(ContainSubstring("PROBE_DIRECT=BLOCKED"))
	}, 120*time.Second, 3*time.Second).Should(Succeed())
}

// probeCycles counts completed probe cycles in an agent log. PROBE_DIRECT is the final
// marker each cycle prints, so its occurrences count whole cycles regardless of verdict.
func probeCycles(log string) int {
	return strings.Count(log, "PROBE_DIRECT=")
}

// expectProbesBlockedAfterRepair asserts enforcement is fully restored after a tamper
// repair. It first waits for one whole probe cycle to complete (draining any cycle that
// was in flight across the tamper window — its verdicts belong to the window, not to
// the repaired state), then requires the two following cycles to report only BLOCKED
// for direct DNS and direct-connect egress. Probe cycles are sequential in the agent
// script, so every marker after the drain boundary comes from a cycle that started
// after the repair landed.
func expectProbesBlockedAfterRepair(ctx context.Context, key client.ObjectKey) {
	GinkgoHelper()

	start := agentPodLog(ctx, key)
	var aligned string
	Eventually(func(g Gomega) {
		l := agentPodLog(ctx, key)
		g.Expect(probeCycles(l)).To(BeNumerically(">=", probeCycles(start)+1),
			"no probe cycle completed after the repair")
		aligned = l
	}, 90*time.Second, 2*time.Second).Should(Succeed())

	var final string
	Eventually(func(g Gomega) {
		l := agentPodLog(ctx, key)
		g.Expect(probeCycles(l)).To(BeNumerically(">=", probeCycles(aligned)+2),
			"post-repair probe cycles did not complete")
		final = l
	}, 90*time.Second, 2*time.Second).Should(Succeed())

	delta := final[len(aligned):]
	Expect(delta).To(ContainSubstring("PROBE_DNS=BLOCKED"))
	Expect(delta).To(ContainSubstring("PROBE_DIRECT=BLOCKED"))
	Expect(delta).NotTo(ContainSubstring("PROBE_DNS=OK"),
		"direct DNS still open after the repair landed:\n%s", delta)
	Expect(delta).NotTo(ContainSubstring("PROBE_DIRECT=OK"),
		"direct egress still open after the repair landed:\n%s", delta)
}

// observedEgressDecisions filters status.policyDecisions down to runtime network
// decisions submitted by the egress-proxy identity for the given probe host.
func observedEgressDecisions(s scrutineerv1alpha1.AgentSession, host string) []scrutineerv1alpha1.PolicyDecision {
	var out []scrutineerv1alpha1.PolicyDecision
	for _, d := range s.Status.PolicyDecisions {
		if d.Phase != scrutineerv1alpha1.PolicyDecisionPhaseRuntime || d.Type != "network" || d.Actor != envoy.AccessLogActor {
			continue
		}
		if strings.HasPrefix(d.Target, host) {
			out = append(out, d)
		}
	}
	return out
}

// tamperDecisionKey identifies one decision across status re-reads, mirroring the
// controller's dedupe key fields (time/target/action/reason).
func tamperDecisionKey(d scrutineerv1alpha1.PolicyDecision) string {
	return d.Time.String() + "|" + d.Target + "|" + string(d.Action) + "|" + d.Reason
}
