//go:build e2e

/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package e2e

import (
	"context"
	"fmt"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/secureai/relay/internal/enforcement/dnsproxy"
	"github.com/secureai/relay/internal/enforcement/toolgateway"
	"github.com/secureai/relay/internal/enforcement/workspace"
)

const (
	relaySystemNamespace = "relay-system"
	e2eReporterLabel     = "relay.secureai.dev/e2e-component"
	e2eReporterLabelVal  = "runtime-reporter"
	e2eReporterDeploy    = "relay-e2e-runtime-reporter"
	e2eReporterService   = "relay-controller-reporter"
	e2eReporterSA        = "relay-e2e-runtime-reporter"
	e2eReporterClusterRole = "relay-e2e-runtime-reporter"
)

// relayE2EImage is the controller image loaded into kind for the in-cluster reporter.
func relayE2EImage() string {
	if img := os.Getenv("RELAY_E2E_IMG"); img != "" {
		return img
	}
	return "ghcr.io/secureai/relay:latest"
}

// requireLiveEvidenceImages skips the spec when dns-proxy e2e images are not present in kind.
func requireLiveEvidenceImages(ctx SpecContext) {
	GinkgoHelper()
	requireRelayE2EImage(ctx)
	if !clusterImageRunnable(ctx, dnsproxy.DefaultDNSProxyImage) {
		Skip("dns-proxy image not available in cluster — run: make kind-load-dns-proxy")
	}
}

// requireLiveToolEvidenceImages skips the spec when tool-gateway e2e images are not present in kind.
func requireLiveToolEvidenceImages(ctx SpecContext) {
	GinkgoHelper()
	requireRelayE2EImage(ctx)
	if !clusterImageRunnable(ctx, toolgateway.DefaultToolGatewayImage) {
		Skip("tool-gateway image not available in cluster — run: make kind-load-tool-gateway")
	}
}

// requireLiveFileEvidenceImages skips the spec when fs-gateway e2e images are not present in kind.
func requireLiveFileEvidenceImages(ctx SpecContext) {
	GinkgoHelper()
	requireRelayE2EImage(ctx)
	if !clusterImageRunnable(ctx, workspace.DefaultFSGatewayImage) {
		Skip("fs-gateway image not available in cluster — run: make kind-load-fs-gateway")
	}
}

func requireRelayE2EImage(ctx SpecContext) {
	GinkgoHelper()
	if !clusterImageRunnable(ctx, relayE2EImage()) {
		Skip("relay image not available in cluster — run: make kind-load")
	}
}

func clusterImageRunnable(ctx context.Context, image string) bool {
	GinkgoHelper()
	probe := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("img-probe-%d", time.Now().UnixNano()),
			Namespace: "default",
			Labels:    map[string]string{e2eReporterLabel: "image-probe"},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{{
				Name:            "probe",
				Image:           image,
				ImagePullPolicy: corev1.PullIfNotPresent,
				Command:         []string{"sh", "-c", "sleep 1"},
			}},
		},
	}
	if err := k8sClient.Create(ctx, probe); err != nil {
		return false
	}
	defer func() { _ = k8sClient.Delete(ctx, probe) }()

	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		var got corev1.Pod
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(probe), &got); err != nil {
			time.Sleep(time.Second)
			continue
		}
		switch got.Status.Phase {
		case corev1.PodRunning, corev1.PodSucceeded:
			return true
		case corev1.PodFailed:
			return false
		}
		for _, cs := range got.Status.ContainerStatuses {
			if w := cs.State.Waiting; w != nil && (w.Reason == "ErrImagePull" || w.Reason == "ImagePullBackOff") {
				return false
			}
		}
		time.Sleep(time.Second)
	}
	return false
}

func deployInClusterReporter(ctx SpecContext) {
	GinkgoHelper()
	ensureRelaySystemNamespace(ctx)

	labels := map[string]string{
		e2eReporterLabel:           e2eReporterLabelVal,
		"app.kubernetes.io/name":   "relay",
		"app.kubernetes.io/component": "runtime-reporter",
	}

	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      e2eReporterSA,
			Namespace: relaySystemNamespace,
			Labels:    labels,
		},
	}
	Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, sa))).To(Succeed())

	cr := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: e2eReporterClusterRole, Labels: labels},
		Rules: []rbacv1.PolicyRule{
			{APIGroups: []string{"authentication.k8s.io"}, Resources: []string{"tokenreviews"}, Verbs: []string{"create"}},
			{APIGroups: []string{"relay.secureai.dev"}, Resources: []string{"agentsessions"}, Verbs: []string{"get"}},
			{APIGroups: []string{"relay.secureai.dev"}, Resources: []string{"agentsessions/status"}, Verbs: []string{"get", "update", "patch"}},
			{APIGroups: []string{"batch"}, Resources: []string{"jobs"}, Verbs: []string{"get"}},
			{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get"}},
		},
	}
	Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, cr))).To(Succeed())

	crb := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: e2eReporterClusterRole, Labels: labels},
		Subjects: []rbacv1.Subject{{
			Kind:      "ServiceAccount",
			Name:      e2eReporterSA,
			Namespace: relaySystemNamespace,
		}},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     e2eReporterClusterRole,
		},
	}
	Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, crb))).To(Succeed())

	replicas := int32(1)
	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      e2eReporterDeploy,
			Namespace: relaySystemNamespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					ServiceAccountName: e2eReporterSA,
					Containers: []corev1.Container{{
						Name:            "reporter",
						Image:           relayE2EImage(),
						ImagePullPolicy: corev1.PullIfNotPresent,
						Args: []string{
							"--reporter-only",
							"--reporter-bind-address=:8088",
							"--metrics-bind-address=0",
							"--health-probe-bind-address=0",
						},
					}},
				},
			},
		},
	}
	Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, deploy))).To(Succeed())

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      e2eReporterService,
			Namespace: relaySystemNamespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Ports: []corev1.ServicePort{{
				Name:       "reporter",
				Port:       8088,
				TargetPort: intstr.FromInt32(8088),
			}},
		},
	}
	Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, svc))).To(Succeed())

	Eventually(func(g Gomega) {
		var got appsv1.Deployment
		g.Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: relaySystemNamespace, Name: e2eReporterDeploy}, &got)).To(Succeed())
		g.Expect(got.Status.ReadyReplicas).To(Equal(int32(1)))
	}, 90*time.Second, time.Second).Should(Succeed())
}

func ensureRelaySystemNamespace(ctx context.Context) {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: relaySystemNamespace}}
	Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, ns))).To(Succeed())
}

func cleanupInClusterReporter(ctx context.Context) {
	_ = k8sClient.Delete(ctx, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: e2eReporterDeploy, Namespace: relaySystemNamespace},
	})
	_ = k8sClient.Delete(ctx, &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: e2eReporterService, Namespace: relaySystemNamespace},
	})
	_ = k8sClient.Delete(ctx, &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: e2eReporterSA, Namespace: relaySystemNamespace},
	})
	_ = k8sClient.Delete(ctx, &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: e2eReporterClusterRole},
	})
	_ = k8sClient.Delete(ctx, &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: e2eReporterClusterRole},
	})
	Eventually(func() bool {
		err := k8sClient.Get(ctx, client.ObjectKey{Namespace: relaySystemNamespace, Name: e2eReporterDeploy}, &appsv1.Deployment{})
		return apierrors.IsNotFound(err)
	}, 30*time.Second, time.Second).Should(BeTrue())
}
