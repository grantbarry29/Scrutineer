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
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// The networking suite is a CNI-generic set of Envoy egress / routing-lock / DNS
// enforcement specs. They assert enforcement *behavior* (not any CNI's internals), so the
// same tests validate any NetworkPolicy-enforcing CNI. Select them with the Ginkgo label
// below; `make test-e2e-net` runs them against the current cluster, and
// `make test-e2e-net-kindnet` / `make test-e2e-net-calico` run the identical suite on each
// CNI. The standard suite (`make test-e2e`) excludes this label.
const labelNetworking = "networking"

var (
	cniEnforceOnce sync.Once
	cniEnforces    bool
)

// requireEgressEnforcingCNI skips the spec unless the target cluster's CNI actually enforces
// egress NetworkPolicy. Probed once per suite so the networking tests stay portable: pointed
// at a non-enforcing CNI they skip with a clear message instead of emitting confusing
// enforcement-assertion failures.
func requireEgressEnforcingCNI(ctx SpecContext) {
	GinkgoHelper()
	cniEnforceOnce.Do(func() { cniEnforces = probeEgressEnforced(ctx) })
	if !cniEnforces {
		Skip("target CNI does not enforce egress NetworkPolicy — the networking suite needs one that does (kindnet, Calico, …)")
	}
}

// probeEgressEnforced returns whether a default-deny egress NetworkPolicy actually drops
// traffic on this cluster: a busybox pod polls a raw TCP connect to a kube-dns POD IP (open
// before the policy), then a deny-all egress policy is applied and the probe must flip to
// BLOCKED. No exec required — the verdict is read from the pod's own log. The target is a
// real pod (not the apiserver, which is host-network and exempt from pod egress policy on
// many CNIs), so this reflects genuine pod-to-pod egress enforcement.
func probeEgressEnforced(ctx context.Context) bool {
	GinkgoHelper()
	targetIP := kubeDNSPodIP(ctx)
	if targetIP == "" {
		return false
	}

	ns := "scrutineer-cni-probe-" + rand.String(5)
	Expect(k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}})).To(Succeed())
	defer func() {
		_ = k8sClient.Delete(context.Background(), &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}})
	}()

	probeCmd := fmt.Sprintf(
		`while true; do if nc -w 3 %s 53 </dev/null >/dev/null 2>&1; then echo PROBE=OPEN; else echo PROBE=BLOCKED; fi; sleep 2; done`,
		targetIP)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "probe", Namespace: ns, Labels: map[string]string{"app": "probe"}},
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

	// Baseline: without a policy the probe reaches the apiserver.
	Eventually(func(g Gomega) {
		g.Expect(podLogTail(ctx, ns, "probe", "probe", 3)).To(ContainSubstring("PROBE=OPEN"))
	}, 60*time.Second, 2*time.Second).Should(Succeed())

	Expect(k8sClient.Create(ctx, &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "deny-egress", Namespace: ns},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
		},
	})).To(Succeed())

	// Enforcement = recent probe lines flip to BLOCKED within the window. A non-enforcing
	// CNI keeps reporting OPEN, so we return false after the deadline (not a failure).
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(podLogTail(ctx, ns, "probe", "probe", 3), "PROBE=BLOCKED") {
			return true
		}
		time.Sleep(2 * time.Second)
	}
	return false
}

// kubeDNSPodIP returns the IP of a running kube-dns/CoreDNS pod, or "" if none is found.
func kubeDNSPodIP(ctx context.Context) string {
	var pods corev1.PodList
	if err := k8sClient.List(ctx, &pods, client.InNamespace("kube-system"), client.MatchingLabels{"k8s-app": "kube-dns"}); err != nil {
		return ""
	}
	for i := range pods.Items {
		if ip := pods.Items[i].Status.PodIP; ip != "" && pods.Items[i].Status.Phase == corev1.PodRunning {
			return ip
		}
	}
	return ""
}

// podLogTail returns the last tail lines of a pod container's log, or "" if unavailable.
func podLogTail(ctx context.Context, namespace, pod, container string, tail int64) string {
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return ""
	}
	raw, err := cs.CoreV1().Pods(namespace).
		GetLogs(pod, &corev1.PodLogOptions{Container: container, TailLines: &tail}).
		DoRaw(ctx)
	if err != nil {
		return ""
	}
	return string(raw)
}
