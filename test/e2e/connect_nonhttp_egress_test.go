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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/enforcement/envoy"
)

// #162 (splits #123 AC#4 — the "may be split into its own issue" live e2e; egress-steering
// option A4): the CONNECT-tunnel escape hatch for NON-HTTP L4 egress, proven live on a NON-443
// port. fqdn_egress_test.go already proves FQDN RBAC over HTTPS (= a CONNECT tunnel to :443);
// TestAuthorityRegex unit-locks the non-443 claim. Until this spec the port-5432 claim only
// rode on that mechanism-inheritance, never adversarially exercised end to end.
//
// Here an Envoy-governed agent raw-CONNECTs (no HTTP request, just the CONNECT preamble) to an
// in-cluster TCP sink on :5432 through its per-session proxy. The tunnel is L7-opaque once the
// CONNECT is established — Envoy forwards raw bytes and applies NO HTTP semantics to the
// payload — so the sink's own protocol is irrelevant to what is proven; a persistent busybox
// listener is simply a reliable "accepts TCP on a non-443 port" upstream (and, being always
// listening, makes the routing-lock negative unambiguous: a BLOCKED direct dial means the lock
// dropped it, not a closed port). What the spec proves is that FQDN policy governs a non-443
// CONNECT authority exactly as it governs :443:
//   - an allow-listed host tunnels (Envoy answers HTTP 200 to the CONNECT),
//   - an unlisted host is refused at the chokepoint (403), never reaching any upstream,
//   - each is recorded as observed evidence stamped from the proxy pod's identity, and
//   - the default-deny routing lock still drops a direct (non-proxied) dial to the sink.
//
// The allow/deny differential is what makes the spec non-vacuous: if the FQDN RBAC did not
// apply to a non-443 CONNECT authority, the unlisted host would NOT be 403-blocked and the
// deny assertions would fail. Enforced-mode egress needs a verified routing lock (#70), so the
// spec is meaningful only on an enforcing CNI and skips otherwise — it runs across kindnet and
// Calico in the networking suite (`make test-e2e-net`). IPv4 path only; no dual-stack need.
var _ = Describe("Live non-HTTP CONNECT-tunnel egress at Envoy", Label(labelNetworking), func() {
	const sinkPort = 5432
	// Never allow-listed, so under the allow-list it is default-denied. RBAC refuses it before
	// any DNS/upstream dial, so it deliberately does not resolve — the assertion is the 403.
	const deniedHost = "denied.raw.scrutineer.invalid"

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

	It("tunnels an allow-listed non-443 host, refuses an unlisted one, and holds the lock", func(ctx SpecContext) {
		requireEgressEnforcingCNI(ctx)
		ns := newTestNamespace("scrutineer-e2e-connect")
		createRuntimeProfileWithEnvoy(ctx, ns, "envoy-egress")

		By("standing up a persistent in-cluster TCP sink on a non-443 port (Service + pod)")
		sinkFQDN, sinkPodIP := startTunnelSink(ctx, ns, "raw-sink", sinkPort)

		By("allowing only the sink's FQDN — every other authority is default-denied")
		createEnforcedAllowedDomainPolicy(ctx, ns, "raw-allow", sinkFQDN)

		session := newAgentSession(ns, "connect-nonhttp",
			withRuntimeProfileRef("envoy-egress"),
			withPolicyRef("AgentPolicy", "raw-allow"),
			withRawConnectProbe(sinkFQDN, deniedHost, sinkPort),
		)
		// The routing-lock negative dials the sink pod directly (bypassing Envoy); it needs the
		// IP because the lock denies DNS. A real, persistently-listening pod IP keeps the
		// negative CNI-generic and unambiguous.
		session.Spec.Runtime.Env = append(session.Spec.Runtime.Env,
			corev1.EnvVar{Name: "SINK_IP", Value: sinkPodIP})
		key := createAgentSession(ctx, session)

		expectJobForSession(ctx, ns, session)
		waitForPhase(ctx, key, []scrutineerv1alpha1.AgentSessionPhase{scrutineerv1alpha1.PhaseRunning}, 90*time.Second, 2*time.Second)
		egressKey := envoyKey(ns, session.Name)
		waitForEnvoyPodReady(ctx, egressKey)

		By("observed decisions: ALLOW for the allow-listed non-443 authority, DENY for the unlisted one")
		expectObservedDecision(ctx, key, sinkFQDN, scrutineerv1alpha1.PolicyDecisionAllow)
		expectObservedDecision(ctx, key, deniedHost, scrutineerv1alpha1.PolicyDecisionDeny)

		By("Envoy's access log corroborating: both authorities logged, the unlisted one 403-blocked")
		Eventually(func(g Gomega) {
			logs := envoyAccessLog(ctx, egressKey)
			g.Expect(logs).To(ContainSubstring(sinkFQDN), "allow-listed sink authority should appear in the access log")
			g.Expect(logs).To(ContainSubstring(deniedHost), "denied authority should appear in the access log")
			g.Expect(logs).To(ContainSubstring("403"), "the unlisted host must be RBAC-denied (403); log:\n%s", logs)
		}, 150*time.Second, 3*time.Second).Should(Succeed())

		By("the agent's own view: raw tunnel established to the allow-listed sink, refused for the unlisted host, direct dial dropped by the lock")
		Eventually(func(g Gomega) {
			logs := agentPodLog(ctx, key)
			// Positive: a raw CONNECT to the allow-listed sink on :5432 established a tunnel
			// (Envoy answered HTTP 200). This is the byte-level proof the escape hatch works.
			g.Expect(logs).To(ContainSubstring("TUNNEL_ALLOWED=OPEN"),
				"raw CONNECT to the allow-listed non-443 sink should tunnel (Envoy answers HTTP 200)")
			// Negative: the unlisted host is refused before any upstream dial (403), never OPEN.
			g.Expect(logs).To(ContainSubstring("TUNNEL_DENIED=BLOCKED"))
			g.Expect(logs).NotTo(ContainSubstring("TUNNEL_DENIED=OPEN"))
			// Routing lock: a direct dial to the (persistently-listening) sink pod, bypassing
			// Envoy, is dropped — BLOCKED here means the lock, not a closed port.
			g.Expect(logs).To(ContainSubstring("LOCK_DIRECT=BLOCKED"))
			g.Expect(logs).NotTo(ContainSubstring("LOCK_DIRECT=OPEN"))
		}, 150*time.Second, 3*time.Second).Should(Succeed())

		requestCancellation(ctx, key)
		waitForTerminalPhase(ctx, key, scrutineerv1alpha1.PhaseCancelled)
	})
})

// startTunnelSink stands up a persistent TCP listener (busybox httpd on port) fronted by a
// ClusterIP Service, and returns the Service FQDN (the CONNECT authority Envoy resolves) and
// the pod IP (a persistently-listening, non-Envoy target for the routing-lock negative). httpd
// is used only because it is a reliable, always-on TCP listener — the tunnel is L7-opaque, so
// what it speaks post-accept is irrelevant to this spec.
func startTunnelSink(ctx context.Context, namespace, name string, port int) (fqdn, podIP string) {
	GinkgoHelper()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    map[string]string{"app": name, "role": "tunnel-sink"},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{{
				Name:    "sink",
				Image:   "busybox:latest",
				Command: []string{"httpd", "-f", "-p", fmt.Sprintf("%d", port), "-h", "/"},
			}},
		},
	}
	Expect(k8sClient.Create(ctx, pod)).To(Succeed())

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{"app": name},
			Ports: []corev1.ServicePort{{
				Port:       int32(port),
				TargetPort: intstr.FromInt32(int32(port)),
			}},
		},
	}
	Expect(k8sClient.Create(ctx, svc)).To(Succeed())

	Eventually(func(g Gomega) {
		var got corev1.Pod
		g.Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &got)).To(Succeed())
		g.Expect(got.Status.Phase).To(Equal(corev1.PodRunning), "sink pod %s not Running yet", name)
		g.Expect(got.Status.PodIP).NotTo(BeEmpty(), "sink pod %s has no IP yet", name)
		podIP = got.Status.PodIP
	}, 90*time.Second, 2*time.Second).Should(Succeed())

	fqdn = fmt.Sprintf("%s.%s.svc.cluster.local", name, namespace)
	return fqdn, podIP
}

// withRawConnectProbe makes the busybox agent, after startup, repeatedly send a raw HTTP
// CONNECT (the preamble only — no HTTP request follows, so nothing HTTP-level is spoken to the
// upstream) at its per-session Envoy for two authorities on a non-443 port, plus a direct
// non-proxied dial to the sink pod. Envoy is targeted by ClusterIP derived from the injected
// proxy env (the lock denies DNS, so a name would not resolve). It prints atomic markers the
// spec asserts on:
//   - TUNNEL_ALLOWED — OPEN if Envoy answered HTTP 200 (CONNECT established) for the allow-listed host.
//   - TUNNEL_DENIED  — BLOCKED unless Envoy tunneled the unlisted host (it must not: RBAC 403).
//   - LOCK_DIRECT    — BLOCKED unless a direct dial to $SINK_IP:port succeeds (the lock must drop it).
func withRawConnectProbe(allowedHost, deniedHost string, port int) agentSessionOption {
	return func(s *scrutineerv1alpha1.AgentSession) {
		script := fmt.Sprintf(`sleep 12
ENVOY_IP=$(printf '%%s' "${http_proxy:-$HTTP_PROXY}" | sed 's|^http://||; s|:.*$||')
for i in $(seq 1 40); do
  a=$({ printf 'CONNECT %[1]s:%[3]d HTTP/1.1\r\nHost: %[1]s:%[3]d\r\n\r\n'; sleep 4; } | nc -w 6 "$ENVOY_IP" 15001 2>/dev/null)
  case "$a" in *"HTTP/1.1 200"*) echo "TUNNEL_ALLOWED=OPEN" ;; *) echo "TUNNEL_ALLOWED=BLOCKED" ;; esac
  d=$({ printf 'CONNECT %[2]s:%[3]d HTTP/1.1\r\nHost: %[2]s:%[3]d\r\n\r\n'; sleep 2; } | nc -w 6 "$ENVOY_IP" 15001 2>/dev/null)
  case "$d" in *"HTTP/1.1 200"*) echo "TUNNEL_DENIED=OPEN" ;; *) echo "TUNNEL_DENIED=BLOCKED" ;; esac
  if timeout 6 nc -w 4 "$SINK_IP" %[3]d </dev/null >/dev/null 2>&1; then echo "LOCK_DIRECT=OPEN"; else echo "LOCK_DIRECT=BLOCKED"; fi
  sleep 2
done
sleep 120`, allowedHost, deniedHost, port)
		s.Spec.Runtime.Command = []string{"sh", "-c", script}
	}
}
