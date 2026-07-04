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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	scrutineerjob "github.com/grantbarry29/scrutineer/internal/controller/job"
	"github.com/grantbarry29/scrutineer/internal/enforcement"
	"github.com/grantbarry29/scrutineer/internal/enforcement/envoy"
	"github.com/grantbarry29/scrutineer/internal/enforcement/networkpolicy"
)

// Dual-stack egress posture (#66): the egress path is IPv4-only, and IPv6 must be denied
// BY CONSTRUCTION — no rendered policy contains a v6 allow, so on a dual-stack cluster the
// backstop and the routing lock leave selected pods zero permitted IPv6 egress. That claim
// is about what a real CNI does with our real rendered objects, so it can only be proven
// here: the spec applies the actual BuildEgressProxyBackstop / Build(lock) outputs to probe
// pods on a dual-stack cluster and asserts the per-family differential. Skipped on
// single-stack clusters (kube-dns pod has no IPv6) — run it via `make test-e2e-net-dual`.
//
// The differential target is a CoreDNS pod's metrics port (9153), which dual-listens:
//   - backstop probe: v4:9153 OPEN (0.0.0.0/0 ipBlock) while v6:9153 BLOCKED (no v6 allow)
//     and v6:53 OPEN (the DNS rule is selector-based, hence family-agnostic);
//   - lock probe: everything BLOCKED on both families (default-deny, Envoy-only).
//
// Creating the backstop object with IPv6 backstop entries also regression-covers the
// cross-family except bug: pre-#66, v6 CIDRs landed in the 0.0.0.0/0 except list and the
// apiserver rejected the whole policy.
var _ = Describe("Dual-stack egress posture", Label(labelNetworking), func() {
	BeforeEach(func(ctx SpecContext) {
		requireEgressEnforcingCNI(ctx)
	})

	It("denies all IPv6 egress by construction while IPv4 follows the rendered rules", func(ctx SpecContext) {
		v4, v6 := kubeDNSPodIPFamilies(ctx)
		Expect(v4).NotTo(BeEmpty(), "need a kube-dns pod IPv4 as the differential target")
		if v6 == "" {
			Skip("cluster is not dual-stack (kube-dns pod has no IPv6) — run on the scrutineer-dual cluster (make kind-up-dual)")
		}

		ns := newTestNamespace("scrutineer-e2e-dual")

		By("baselining both families OPEN from an unconfined probe pod")
		backstopProbe := startFamilyProbe(ctx, ns, "backstop-probe", envoy.Labels("dual-bs"), v4, v6)
		lockProbe := startFamilyProbe(ctx, ns, "lock-probe",
			map[string]string{scrutineerjob.LabelSessionRef: "dual-lock"}, v4, v6)
		for _, probe := range []string{backstopProbe, lockProbe} {
			Eventually(func(g Gomega) {
				g.Expect(podLogTail(ctx, ns, probe, "probe", 2)).To(
					ContainSubstring("V4=OPEN V6=OPEN DNS6=OPEN"),
					"dual-stack pod-to-pod baseline must be open on both families")
			}, 90*time.Second, 2*time.Second).Should(Succeed())
		}

		By("creating the real backstop object with IPv6 backstop entries (apiserver must accept it)")
		backstop := networkpolicy.BuildEgressProxyBackstop(
			enforcement.SessionContext{SessionNamespace: ns, SessionName: "dual-bs"},
			[]string{"169.254.0.0/16", "fe80::/10", "fd00:ec2::254"},
		)
		Expect(backstop).NotTo(BeNil())
		Expect(k8sClient.Create(ctx, backstop)).To(Succeed(),
			"IPv6 backstop entries must never render an apiserver-invalid policy")

		By("the backstopped pod losing IPv6 while keeping IPv4 internet + family-agnostic DNS")
		Eventually(func(g Gomega) {
			g.Expect(podLogTail(ctx, ns, backstopProbe, "probe", 2)).To(
				ContainSubstring("V4=OPEN V6=BLOCKED DNS6=OPEN"),
				"backstop must deny ALL IPv6 by construction, keep v4 (0.0.0.0/0) and selector-based DNS")
		}, 60*time.Second, 2*time.Second).Should(Succeed())

		By("creating the real routing lock and losing both families (default-deny is family-agnostic)")
		enabled := true
		lock := networkpolicy.Build(enforcement.SessionContext{
			SessionNamespace: ns,
			SessionName:      "dual-lock",
			Mode:             scrutineerv1alpha1.PolicyModeEnforced,
			Enforcement: []scrutineerv1alpha1.RuntimeProfileEnforcement{{
				Name: "envoy", Type: scrutineerjob.EnforcementTypeEnvoy, Enabled: &enabled,
			}},
		})
		Expect(lock).NotTo(BeNil())
		Expect(k8sClient.Create(ctx, lock)).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(podLogTail(ctx, ns, lockProbe, "probe", 2)).To(
				ContainSubstring("V4=BLOCKED V6=BLOCKED DNS6=BLOCKED"),
				"the routing lock must default-deny both families (Envoy is the only egress)")
		}, 60*time.Second, 2*time.Second).Should(Succeed())
	})
})

// kubeDNSPodIPFamilies returns the IPv4 and IPv6 addresses of a running CoreDNS pod
// ("" for a family the pod does not have — the dual-stack detection signal).
func kubeDNSPodIPFamilies(ctx context.Context) (v4, v6 string) {
	var pods corev1.PodList
	if err := k8sClient.List(ctx, &pods, client.InNamespace("kube-system"), client.MatchingLabels{"k8s-app": "kube-dns"}); err != nil {
		return "", ""
	}
	for i := range pods.Items {
		if pods.Items[i].Status.Phase != corev1.PodRunning {
			continue
		}
		for _, ip := range pods.Items[i].Status.PodIPs {
			if strings.Contains(ip.IP, ":") {
				v6 = ip.IP
			} else {
				v4 = ip.IP
			}
		}
		if v4 != "" && v6 != "" {
			return v4, v6
		}
	}
	return v4, v6
}

// startFamilyProbe runs a busybox pod that polls the differential targets and prints one
// atomic verdict line per tick: "V4=… V6=… DNS6=…" (v4:9153, v6:9153, v6:53 against the
// CoreDNS pod). The verdict is read from the pod log (no exec), like probeEgressEnforced.
func startFamilyProbe(ctx context.Context, namespace, name string, labels map[string]string, v4, v6 string) string {
	GinkgoHelper()
	probeCmd := fmt.Sprintf(`while true; do
v4=BLOCKED; nc -w 3 %[1]s 9153 </dev/null >/dev/null 2>&1 && v4=OPEN
v6=BLOCKED; nc -w 3 %[2]s 9153 </dev/null >/dev/null 2>&1 && v6=OPEN
d6=BLOCKED; nc -w 3 %[2]s 53 </dev/null >/dev/null 2>&1 && d6=OPEN
echo "V4=$v4 V6=$v6 DNS6=$d6"
sleep 2
done`, v4, v6)
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
