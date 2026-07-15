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
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/grantbarry29/scrutineer/internal/enforcement"
	"github.com/grantbarry29/scrutineer/internal/enforcement/envoy"
	"github.com/grantbarry29/scrutineer/internal/enforcement/networkpolicy"
)

// Egress-backstop IPv4 deny-list (#153): the backstop's principal job on IPv4 is to deny the
// configured CIDRs (cloud metadata, operator ranges) from the Envoy proxy pod even though the
// pod egresses freely (0.0.0.0/0). That mechanism — an ipBlock 0.0.0.0/0 whose `except` list
// carves out the denied ranges — was covered only by IPv6 posture (V6=BLOCKED by construction)
// and apiserver-acceptance; nothing ever probed a v4 target the except-list should block, so a
// rendering regression that silently dropped the except entries kept every test green while the
// backstop stopped backstopping.
//
// 169.254.169.254 is unroutable in kind, so "blocked by policy" and "unreachable anyway" are
// indistinguishable there — an assertion against it would be unfalsifiable. This spec proves the
// mechanism with a routable differential instead: two in-cluster v4 sink pods, one whose /32 is
// placed in the backstop CIDR list and one that is not. The real BuildEgressProxyBackstop output
// (carrying the true metadata CIDRs alongside the listed /32) must flip the listed target to
// BLOCKED while the unlisted target stays OPEN. Deliberately dropping the except list from the
// renderer turns this spec red while the IPv6/apiserver specs stay green — that asymmetry is the
// gap the issue closes. IPv4-only, so it runs in the per-push networking leg (no dual-stack need).
var _ = Describe("Egress backstop IPv4 deny-list", Label(labelNetworking), func() {
	BeforeEach(func(ctx SpecContext) {
		requireEgressEnforcingCNI(ctx)
	})

	It("denies a backstopped v4 /32 from the Envoy pod while leaving unlisted v4 targets open", func(ctx SpecContext) {
		ns := newTestNamespace("scrutineer-e2e-bs4")

		By("standing up two routable in-cluster v4 sink pods (the except-list differential targets)")
		listedIP := startHTTPSink(ctx, ns, "sink-listed")
		unlistedIP := startHTTPSink(ctx, ns, "sink-unlisted")
		Expect(listedIP).NotTo(Equal(unlistedIP), "the two sink pods must have distinct IPs")

		By("baselining both sink pods OPEN from an unconfined Envoy-labeled probe pod")
		probe := startBackstopProbe(ctx, ns, "backstop-probe", envoy.Labels("bs4"), listedIP, unlistedIP)
		Eventually(func(g Gomega) {
			g.Expect(podLogTail(ctx, ns, probe, "probe", 2)).To(
				ContainSubstring("LISTED=OPEN UNLISTED=OPEN"),
				"before any policy, the Envoy pod reaches both in-cluster v4 targets")
		}, 90*time.Second, 2*time.Second).Should(Succeed())

		By("applying the real backstop with the metadata CIDRs plus the listed target's /32")
		// DefaultBackstopCIDRs (the cloud-metadata range) is kept so apiserver-acceptance
		// coverage is preserved; the listed sink's /32 is the routable differential entry.
		backstop := networkpolicy.BuildEgressProxyBackstop(
			enforcement.SessionContext{SessionNamespace: ns, SessionName: "bs4"},
			append(append([]string{}, networkpolicy.DefaultBackstopCIDRs...), listedIP+"/32"),
		)
		Expect(backstop).NotTo(BeNil())
		Expect(k8sClient.Create(ctx, backstop)).To(Succeed(),
			"a backstop carrying real metadata CIDRs must render an apiserver-valid policy")

		By("the listed /32 flipping to BLOCKED while the unlisted v4 target stays OPEN")
		Eventually(func(g Gomega) {
			g.Expect(podLogTail(ctx, ns, probe, "probe", 2)).To(
				ContainSubstring("LISTED=BLOCKED UNLISTED=OPEN"),
				"the except list must deny the listed /32 while 0.0.0.0/0 keeps the unlisted target reachable")
		}, 60*time.Second, 2*time.Second).Should(Succeed())

		By("the differential holding steady (not a one-off flap during policy propagation)")
		Consistently(func(g Gomega) {
			g.Expect(podLogTail(ctx, ns, probe, "probe", 2)).To(
				ContainSubstring("LISTED=BLOCKED UNLISTED=OPEN"))
		}, 10*time.Second, 2*time.Second).Should(Succeed())
	})
})

// backstopSinkPort is the TCP port the sink pods listen on. Deliberately neither 53 (the
// backstop's DNS allow) nor the reporter port, so only the 0.0.0.0/0 except rule governs
// reachability to these targets.
const backstopSinkPort = 9999

// startHTTPSink runs a persistent busybox httpd on backstopSinkPort and returns the pod's IP
// once it is Running. httpd is a long-lived TCP listener (unlike one-shot `nc -l`), so the
// probe's repeated connects see a stable OPEN baseline. The pod carries no Envoy labels, so the
// egress backstop never selects it — it is purely an ingress target.
func startHTTPSink(ctx context.Context, namespace, name string) string {
	GinkgoHelper()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    map[string]string{"role": "backstop-sink"},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{{
				Name:    "sink",
				Image:   "busybox:latest",
				Command: []string{"httpd", "-f", "-p", fmt.Sprintf("%d", backstopSinkPort), "-h", "/"},
			}},
		},
	}
	Expect(k8sClient.Create(ctx, pod)).To(Succeed())

	var ip string
	Eventually(func(g Gomega) {
		var got corev1.Pod
		g.Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &got)).To(Succeed())
		g.Expect(got.Status.Phase).To(Equal(corev1.PodRunning), "sink pod %s not Running yet", name)
		g.Expect(got.Status.PodIP).NotTo(BeEmpty(), "sink pod %s has no IP yet", name)
		ip = got.Status.PodIP
	}, 90*time.Second, 2*time.Second).Should(Succeed())
	return ip
}

// startBackstopProbe runs a busybox pod (carrying the given labels — the Envoy labels the
// backstop selects on) that polls a raw TCP connect to both sink targets each tick and prints
// one atomic verdict line "LISTED=… UNLISTED=…". The verdict is read from the pod log (no exec),
// like startFamilyProbe / probeEgressEnforced.
func startBackstopProbe(ctx context.Context, namespace, name string, labels map[string]string, listedIP, unlistedIP string) string {
	GinkgoHelper()
	probeCmd := fmt.Sprintf(`while true; do
l=BLOCKED; nc -w 3 %[1]s %[3]d </dev/null >/dev/null 2>&1 && l=OPEN
u=BLOCKED; nc -w 3 %[2]s %[3]d </dev/null >/dev/null 2>&1 && u=OPEN
echo "LISTED=$l UNLISTED=$u"
sleep 2
done`, listedIP, unlistedIP, backstopSinkPort)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: labels},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{{
				Name:    "probe",
				Image:   "busybox:latest",
				Command: []string{"sh", "-c", probeCmd},
			}},
		},
	}
	Expect(k8sClient.Create(ctx, pod)).To(Succeed())
	return name
}
