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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	scrutineerjob "github.com/grantbarry29/scrutineer/internal/controller/job"
	"github.com/grantbarry29/scrutineer/internal/enforcement/envoy"
	"github.com/grantbarry29/scrutineer/internal/reporter"
)

// #152: the security *negatives* of multi-session operation. Every other spec runs one
// session at a time; the demo smoke-tests happy-path coexistence. These prove the
// adversarial boundary live: (1) two concurrent proxies each enforce ONLY their own
// policy and neither session's status absorbs the other's verdicts; (2) a workload that
// is not session B's egress proxy cannot post evidence into B's audit record, even with a
// valid token; (3) the routing lock's pod selector confines only the session's own pods,
// not neighbors. Networking suite (`make test-e2e-net`) — needs an egress-enforcing CNI.
var _ = Describe("Cross-session isolation and evidence attribution", Label(labelNetworking), func() {
	// forgedTarget is the host a forged report tries to smuggle into the victim's status;
	// its absence is how we prove nothing leaked.
	const forgedTarget = "forged.evil.scrutineer.invalid"

	It("keeps two concurrent enforced sessions' policies and evidence isolated", func(ctx SpecContext) {
		requireEgressEnforcingCNI(ctx)
		requireLiveEgressEvidenceImages(ctx)
		deployInClusterReporter(ctx)

		ns := newTestNamespace("scrutineer-e2e-xsession")
		createRuntimeProfileWithEnvoy(ctx, ns, "envoy-egress")

		// Each host is denied by exactly one session and permitted by the other, so the
		// SAME authority yields opposite verdicts across the two proxies — that asymmetry
		// is the isolation proof (a shared or cross-fed pipeline could not produce it).
		const hostA = "a-only.tracker.scrutineer.invalid"
		const hostB = "b-only.tracker.scrutineer.invalid"
		createFQDNDenyPolicy(ctx, ns, "policy-a", scrutineerv1alpha1.PolicyModeEnforced, hostA)
		createFQDNDenyPolicy(ctx, ns, "policy-b", scrutineerv1alpha1.PolicyModeEnforced, hostB)

		sessionA := newAgentSession(ns, "xsession-a",
			withRuntimeProfileRef("envoy-egress"),
			withPolicyRef("AgentPolicy", "policy-a"),
			withEnvoyEgressProbeHosts(hostA, hostB),
		)
		sessionB := newAgentSession(ns, "xsession-b",
			withRuntimeProfileRef("envoy-egress"),
			withPolicyRef("AgentPolicy", "policy-b"),
			withEnvoyEgressProbeHosts(hostA, hostB),
		)
		keyA := createAgentSession(ctx, sessionA)
		keyB := createAgentSession(ctx, sessionB)
		expectJobForSession(ctx, ns, sessionA)
		expectJobForSession(ctx, ns, sessionB)
		waitForPhase(ctx, keyA, []scrutineerv1alpha1.AgentSessionPhase{scrutineerv1alpha1.PhaseRunning}, 90*time.Second, 2*time.Second)
		waitForPhase(ctx, keyB, []scrutineerv1alpha1.AgentSessionPhase{scrutineerv1alpha1.PhaseRunning}, 90*time.Second, 2*time.Second)

		By("each proxy enforcing only its own policy: A denies hostA / permits hostB")
		expectObservedDecision(ctx, keyA, hostA, scrutineerv1alpha1.PolicyDecisionDeny)
		expectObservedDecision(ctx, keyA, hostB, scrutineerv1alpha1.PolicyDecisionAllow)

		By("and B denies hostB / permits hostA — the reverse of A")
		expectObservedDecision(ctx, keyB, hostB, scrutineerv1alpha1.PolicyDecisionDeny)
		expectObservedDecision(ctx, keyB, hostA, scrutineerv1alpha1.PolicyDecisionAllow)

		By("neither session's status absorbing the other proxy's deny verdict")
		expectNoObservedDecision(ctx, keyA, hostB, scrutineerv1alpha1.PolicyDecisionDeny)
		expectNoObservedDecision(ctx, keyB, hostA, scrutineerv1alpha1.PolicyDecisionDeny)

		requestCancellation(ctx, keyA)
		requestCancellation(ctx, keyB)
		waitForTerminalPhase(ctx, keyA, scrutineerv1alpha1.PhaseCancelled)
		waitForTerminalPhase(ctx, keyB, scrutineerv1alpha1.PhaseCancelled)
	})

	It("rejects a forged report from a pod that is not the session's egress proxy", func(ctx SpecContext) {
		requireScrutineerE2EImage(ctx)
		deployInClusterReporter(ctx)

		ns := newTestNamespace("scrutineer-e2e-forge")

		// Victim session B: a real, running session whose audit record must stay clean.
		victim := newAgentSession(ns, "victim", withLongRunningCommand())
		keyB := createAgentSession(ctx, victim)
		expectJobForSession(ctx, ns, victim)
		waitForPhase(ctx, keyB, []scrutineerv1alpha1.AgentSessionPhase{scrutineerv1alpha1.PhaseRunning}, 90*time.Second, 2*time.Second)

		By("an attacker pod with its own SA and a valid reporter-audience token")
		createServiceAccount(ctx, ns, "forger")
		attacker := newForgedReportPod(ns, "forger", "forger", victim.Namespace, victim.Name, forgedTarget)
		Expect(k8sClient.Create(ctx, attacker)).To(Succeed())

		By("the reporter rejecting it 403 — token authenticates, pod-ownership check refuses")
		Eventually(func(g Gomega) {
			g.Expect(podLogTail(ctx, ns, "forger", "forger", 6)).To(ContainSubstring("FORGED_STATUS=403"),
				"forged report was not 403-rejected by the reporter's pod-ownership check")
		}, 120*time.Second, 3*time.Second).Should(Succeed())

		By("nothing from the forged report landing in the victim's status")
		Consistently(func(g Gomega) {
			got := getSession(ctx, keyB)
			for _, d := range got.Status.PolicyDecisions {
				g.Expect(d.Target).NotTo(Equal(forgedTarget), "forged decision leaked into victim status: %+v", d)
			}
			for _, v := range got.Status.Violations {
				g.Expect(v.Target).NotTo(Equal(forgedTarget), "forged violation leaked into victim status: %+v", v)
			}
		}, 12*time.Second, 3*time.Second).Should(Succeed())

		requestCancellation(ctx, keyB)
		waitForTerminalPhase(ctx, keyB, scrutineerv1alpha1.PhaseCancelled)
	})

	It("locks only the session's own pods, leaving a neighboring pod's egress unrestricted", func(ctx SpecContext) {
		requireEgressEnforcingCNI(ctx)
		if !clusterImageRunnable(ctx, envoy.DefaultEnvoyImage) {
			Skip("envoy image not available in cluster — run: make kind-load-envoy")
		}

		ns := newTestNamespace("scrutineer-e2e-lock-scope")
		createRuntimeProfileWithEnvoy(ctx, ns, "envoy-egress")

		// A neighbor must NOT reach this if the lock over-selected; a locked session pod
		// must NOT reach it either. A real pod (not host-network apiserver) keeps the
		// negative CNI-generic.
		targetIP := kubeDNSPodIP(ctx)
		Expect(targetIP).NotTo(BeEmpty(), "need a kube-dns pod IP as a non-Envoy egress target")

		const probeHost = "lockscope.scrutineer.invalid"
		session := newAgentSession(ns, "lock-scope",
			withRuntimeProfileRef("envoy-egress"),
			withNetpolEgressProbe(probeHost),
		)
		session.Spec.Runtime.Env = append(session.Spec.Runtime.Env,
			corev1.EnvVar{Name: "PROBE_TARGET_IP", Value: targetIP})
		key := createAgentSession(ctx, session)
		expectJobForSession(ctx, ns, session)
		waitForPhase(ctx, key, []scrutineerv1alpha1.AgentSessionPhase{scrutineerv1alpha1.PhaseRunning}, 90*time.Second, 2*time.Second)

		neighborLabels := map[string]string{"app": "neighbor"}

		By("the routing lock existing and selecting only the session's own pods")
		Eventually(func(g Gomega) {
			np := getNetworkPolicy(ctx, ns, netpolNameForSession(session))
			g.Expect(np).NotTo(BeNil())
			g.Expect(np.Spec.PodSelector.MatchLabels).To(HaveKeyWithValue(scrutineerjob.LabelSessionRef, session.Name),
				"lock must select by the session label, not the whole namespace")
			// Structural proof (independent of live traffic): the lock's selector does
			// not match a neighbor's labels, so it cannot restrict the neighbor.
			sel, err := metav1.LabelSelectorAsSelector(&np.Spec.PodSelector)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(sel.Matches(labels.Set(neighborLabels))).To(BeFalse(),
				"lock selector must not capture a pod that lacks the session label")
		}, 60*time.Second, 2*time.Second).Should(Succeed())

		By("a neighbor pod WITHOUT the session label keeping unrestricted egress")
		neighbor := newEgressNeighborPod(ns, "neighbor", neighborLabels, targetIP)
		Expect(k8sClient.Create(ctx, neighbor)).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(podLogTail(ctx, ns, "neighbor", "neighbor", 4)).To(ContainSubstring("NEIGHBOR=OPEN"),
				"the lock over-selected — a pod without the session label was restricted")
		}, 90*time.Second, 3*time.Second).Should(Succeed())

		By("while the session's own pod stays locked (direct egress dropped)")
		Eventually(func(g Gomega) {
			logs := agentPodLog(ctx, key)
			g.Expect(logs).To(ContainSubstring("PROBE_ENVOY_TCP=OK"),
				"agent could not reach its Envoy — the negative below would be meaningless")
			g.Expect(logs).To(ContainSubstring("PROBE_DIRECT=BLOCKED"))
		}, 120*time.Second, 3*time.Second).Should(Succeed())

		requestCancellation(ctx, key)
		waitForTerminalPhase(ctx, key, scrutineerv1alpha1.PhaseCancelled)
	})
})

// withEnvoyEgressProbeHosts is withEnvoyEgressProbe generalized to several hosts: it
// exercises each host through the per-session Envoy every loop (an HTTP GET via the proxy
// env plus a raw CONNECT straight at the proxy), so a concurrent-session spec can drive
// both a denied and a permitted authority through each session's proxy in one run.
func withEnvoyEgressProbeHosts(hosts ...string) agentSessionOption {
	list := ""
	for _, h := range hosts {
		list += h + " "
	}
	return func(s *scrutineerv1alpha1.AgentSession) {
		script := fmt.Sprintf(`sleep 12
ENVOY_IP=$(printf '%%s' "${http_proxy:-$HTTP_PROXY}" | sed 's|^http://||; s|:.*$||')
HOSTS="%s"
for i in $(seq 1 60); do
  for h in $HOSTS; do
    wget -q -O /dev/null "http://$h/" 2>/dev/null || true
    printf 'CONNECT %%s:443 HTTP/1.1\r\nHost: %%s:443\r\n\r\n' "$h" "$h" | nc -w 3 "$ENVOY_IP" 15001 2>/dev/null || true
  done
  sleep 2
done
sleep 120`, list)
		s.Spec.Runtime.Command = []string{"sh", "-c", script}
	}
}

// expectNoObservedDecision asserts the session's status never carries an egress-proxy
// decision for host with the given action across a short window — the negative half of
// isolation (session A must not show B's proxy verdict, and vice versa).
func expectNoObservedDecision(ctx context.Context, key client.ObjectKey, host string, action scrutineerv1alpha1.PolicyDecisionAction) {
	GinkgoHelper()
	Consistently(func(g Gomega) {
		got := getSession(ctx, key)
		for i := range got.Status.PolicyDecisions {
			d := &got.Status.PolicyDecisions[i]
			if d.Actor != envoy.AccessLogActor {
				continue
			}
			if (d.Target == host || d.Target == host+":443") && d.Action == action {
				g.Expect(d).To(BeNil(), "session %s absorbed a foreign %s verdict for %q: %+v", key.Name, action, host, d)
			}
		}
	}, 8*time.Second, 2*time.Second).Should(Succeed())
}

// createServiceAccount creates a dedicated SA so a forged-report pod runs under an
// identity that is NOT the session's egress-proxy SA.
func createServiceAccount(ctx context.Context, ns, name string) {
	GinkgoHelper()
	Expect(k8sClient.Create(ctx, &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
	})).To(Succeed())
}

// newForgedReportPod builds a pod that repeatedly POSTs a report claiming targetSession to
// the real in-cluster reporter, carrying a *valid* projected token for the reporter
// audience so authentication succeeds and only the pod-ownership check can reject it. It
// prints FORGED_STATUS=<code> each attempt for the spec to assert on. The pod is neither
// owned by the session's Job nor is it the session's egress proxy, so the honest verdict
// is 403.
func newForgedReportPod(ns, name, sa, targetNamespace, targetSession, forgedTarget string) *corev1.Pod {
	url := fmt.Sprintf("%s/v1/report", scrutineerjob.DefaultReporterURL)
	body := fmt.Sprintf(
		`{"session":{"namespace":%q,"name":%q},"backend":"egress-proxy","decisions":[{"time":"2023-11-14T22:13:20Z","phase":"runtime","type":"network","action":"deny","reason":"Forged","target":%q,"message":"forged cross-session report"}]}`,
		targetNamespace, targetSession, forgedTarget)
	// wget -S writes the status line to stderr even on a 4xx (which sets a non-zero exit,
	// hence `|| true`); we parse the last "HTTP/x.y NNN" from the merged output.
	script := fmt.Sprintf(`TOK=$(cat /var/run/secrets/reporter/token)
i=0
while [ $i -lt 30 ]; do
  i=$((i+1))
  RESP=$(wget -S -O /dev/null --header="Authorization: Bearer $TOK" --header="Content-Type: application/json" --post-data='%s' '%s' 2>&1 || true)
  CODE=$(printf '%%s' "$RESP" | grep -oE 'HTTP/[0-9.]+ [0-9]+' | awk '{print $2}' | tail -1)
  echo "FORGED_STATUS=$CODE"
  sleep 3
done
sleep 120`, body, url)

	expiry := int64(3600)
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Labels: map[string]string{"app": name}},
		Spec: corev1.PodSpec{
			RestartPolicy:      corev1.RestartPolicyNever,
			ServiceAccountName: sa,
			Containers: []corev1.Container{{
				Name:    name,
				Image:   "busybox:latest",
				Command: []string{"sh", "-c", script},
				VolumeMounts: []corev1.VolumeMount{{
					Name:      "reporter-token",
					MountPath: "/var/run/secrets/reporter",
					ReadOnly:  true,
				}},
			}},
			Volumes: []corev1.Volume{{
				Name: "reporter-token",
				VolumeSource: corev1.VolumeSource{
					Projected: &corev1.ProjectedVolumeSource{
						Sources: []corev1.VolumeProjection{{
							ServiceAccountToken: &corev1.ServiceAccountTokenProjection{
								Audience:          reporter.TokenAudience,
								ExpirationSeconds: &expiry,
								Path:              "token",
							},
						}},
					},
				},
			}},
		},
	}
}

// newEgressNeighborPod builds a plain busybox pod (carrying only the given labels, none of
// them the session label) that continuously probes a direct TCP connect to targetIP,
// printing NEIGHBOR=OPEN/BLOCKED. It shares the session's namespace but must stay
// unrestricted — the routing lock selects only the session's own pods.
func newEgressNeighborPod(ns, name string, podLabels map[string]string, targetIP string) *corev1.Pod {
	script := fmt.Sprintf(
		`while true; do if nc -w 3 %s 53 </dev/null >/dev/null 2>&1; then echo NEIGHBOR=OPEN; else echo NEIGHBOR=BLOCKED; fi; sleep 2; done`,
		targetIP)
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Labels: podLabels},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{{
				Name:    name,
				Image:   "busybox:latest",
				Command: []string{"sh", "-c", script},
			}},
		},
	}
}

// getNetworkPolicy returns the named NetworkPolicy or nil if absent.
func getNetworkPolicy(ctx context.Context, ns, name string) *networkingv1.NetworkPolicy {
	var np networkingv1.NetworkPolicy
	if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, &np); err != nil {
		return nil
	}
	return &np
}
