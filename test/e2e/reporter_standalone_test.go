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
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
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
	"sigs.k8s.io/yaml"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
)

// Paths (relative to test/e2e) of the shipped standalone-reporter artifacts this spec
// exercises directly, rather than a Go replica.
const (
	shippedReporterManifest = "../../config/reporter-standalone/reporter.yaml"
	standaloneOverlayDir    = "../../config/reporter-standalone"
	kustomizeBin            = "../../bin/kustomize"
)

const (
	shippedReporterSA          = "scrutineer-reporter"
	shippedReporterDeploy      = "scrutineer-controller-reporter"
	shippedManagerDeploy       = "scrutineer-controller-manager"
	standaloneReporterRoleName = "scrutineer-e2e-standalone-reporter"
)

// shippedReporterPodLabels is the pod label set of the shipped reporter Deployment —
// and the Service selector the overlay patches in, so the two must agree (the render
// spec below asserts the overlay side).
func shippedReporterPodLabels() map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":      "scrutineer",
		"app.kubernetes.io/component": "reporter",
	}
}

// loadShippedReporterObjects parses config/reporter-standalone/reporter.yaml into its
// ServiceAccount and Deployment so the test asserts on and deploys the *shipped* artifact.
func loadShippedReporterObjects() (*corev1.ServiceAccount, *appsv1.Deployment) {
	GinkgoHelper()
	raw, err := os.ReadFile(shippedReporterManifest)
	Expect(err).NotTo(HaveOccurred(), "read %s", shippedReporterManifest)

	var sa *corev1.ServiceAccount
	var deploy *appsv1.Deployment
	for _, doc := range strings.Split(string(raw), "\n---") {
		if strings.TrimSpace(doc) == "" {
			continue
		}
		var tm metav1.TypeMeta
		Expect(yaml.Unmarshal([]byte(doc), &tm)).To(Succeed())
		switch tm.Kind {
		case "ServiceAccount":
			sa = &corev1.ServiceAccount{}
			Expect(yaml.Unmarshal([]byte(doc), sa)).To(Succeed())
		case "Deployment":
			deploy = &appsv1.Deployment{}
			Expect(yaml.Unmarshal([]byte(doc), deploy)).To(Succeed())
		}
	}
	Expect(sa).NotTo(BeNil(), "reporter.yaml must contain a ServiceAccount")
	Expect(deploy).NotTo(BeNil(), "reporter.yaml must contain a Deployment")
	return sa, deploy
}

var _ = Describe("standalone reporter overlay", func() {
	// These specs exercise the committed config/reporter-standalone artifacts end to
	// end. The full overlay layers on config/default (manager included), which would
	// collide with the suite's in-process manager, so the live specs deploy the shipped
	// reporter artifact specifically and the render spec covers the manager-side patch.

	// Catches drift in the shipped Deployment spec (args, ports, securityContext,
	// probes) that the Go-constructed reporter in reporter_infra_test.go cannot.
	It("boots the shipped reporter Deployment in --reporter-only mode", func(ctx SpecContext) {
		requireScrutineerE2EImage(ctx)
		DeferCleanup(func(ctx context.Context) {
			cleanupStandaloneReporter(ctx)
		})
		deployShippedReporter(ctx)
	})

	// Full observed-evidence path against the shipped artifact (#45 AC): the reporter
	// Service is retargeted at the shipped pods exactly as the overlay's Service patch
	// does, so the per-session Envoy's egress-reporter POST /v1/report lands on the
	// shipped reporter, which must merge the observed runtime decision into the session
	// status. Post-pivot the evidence source is the out-of-pod Envoy egress path (#71).
	It("merges observed runtime evidence posted through the shipped reporter into session status", func(ctx SpecContext) {
		requireEgressEnforcingCNI(ctx)
		requireLiveEgressEvidenceImages(ctx)

		DeferCleanup(func(ctx context.Context) {
			// Delete the retargeted Service too; any later spec's
			// deployInClusterReporter recreates its own.
			_ = k8sClient.Delete(ctx, &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: shippedReporterDeploy, Namespace: scrutineerSystemNamespace},
			})
			cleanupStandaloneReporter(ctx)
		})
		deployShippedReporter(ctx)
		pointReporterServiceAtShippedPods(ctx)

		const deniedHost = "standalone.tracker.scrutineer.invalid"
		ns := newTestNamespace("scrutineer-e2e-standalone")
		createRuntimeProfileWithEnvoy(ctx, ns, "envoy-egress")
		createFQDNDenyPolicy(ctx, ns, "deny-tracker", scrutineerv1alpha1.PolicyModeEnforced, deniedHost)

		session := newAgentSession(ns, "standalone-evidence",
			withRuntimeProfileRef("envoy-egress"),
			withPolicyRef("AgentPolicy", "deny-tracker"),
			withEnvoyEgressProbe(deniedHost),
		)
		key := createAgentSession(ctx, session)

		expectJobForSession(ctx, ns, session)
		waitForPhase(ctx, key, []scrutineerv1alpha1.AgentSessionPhase{scrutineerv1alpha1.PhaseRunning}, 60*time.Second, time.Second)

		Eventually(func(g Gomega) {
			got := getSession(ctx, key)
			var runtime []scrutineerv1alpha1.PolicyDecision
			for _, d := range got.Status.PolicyDecisions {
				if d.Phase == scrutineerv1alpha1.PolicyDecisionPhaseRuntime && d.Target == deniedHost {
					runtime = append(runtime, d)
				}
			}
			g.Expect(runtime).NotTo(BeEmpty(),
				"expected an observed runtime decision for %q merged via the shipped reporter; decisions=%+v",
				deniedHost, got.Status.PolicyDecisions)
		}, 90*time.Second, 2*time.Second).Should(Succeed())

		requestCancellation(ctx, key)
		waitForTerminalPhase(ctx, key, scrutineerv1alpha1.PhaseCancelled)
	})

	// The manager-side half of the overlay cannot be applied over the suite's
	// in-process manager, so assert on the rendered output instead (#45 AC): the
	// manager Deployment gains --enable-reporter=false and the reporter Service is
	// retargeted at the reporter pods.
	It("renders the manager with the in-process reporter disabled", func(ctx SpecContext) {
		if _, err := os.Stat(kustomizeBin); err != nil {
			Skip("kustomize binary not present — run: make kustomize")
		}

		cmd := exec.CommandContext(ctx, kustomizeBin, "build", standaloneOverlayDir)
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		Expect(cmd.Run()).To(Succeed(), "kustomize build %s: %s", standaloneOverlayDir, stderr.String())

		var managerDeploy, reporterDeploy *appsv1.Deployment
		var reporterSvc *corev1.Service
		for _, doc := range strings.Split(stdout.String(), "\n---") {
			if strings.TrimSpace(doc) == "" {
				continue
			}
			var tm metav1.TypeMeta
			Expect(yaml.Unmarshal([]byte(doc), &tm)).To(Succeed())
			switch tm.Kind {
			case "Deployment":
				d := &appsv1.Deployment{}
				Expect(yaml.Unmarshal([]byte(doc), d)).To(Succeed())
				switch d.Name {
				case shippedManagerDeploy:
					managerDeploy = d
				case shippedReporterDeploy:
					reporterDeploy = d
				}
			case "Service":
				s := &corev1.Service{}
				Expect(yaml.Unmarshal([]byte(doc), s)).To(Succeed())
				if s.Name == shippedReporterDeploy {
					reporterSvc = s
				}
			}
		}

		By("asserting the manager runs with the in-process reporter disabled")
		Expect(managerDeploy).NotTo(BeNil(), "overlay must render the manager Deployment")
		manager := containerByName(managerDeploy.Spec.Template.Spec.Containers, "manager")
		Expect(manager).NotTo(BeNil(), "manager Deployment must have a manager container")
		Expect(manager.Args).To(ContainElement("--enable-reporter=false"))

		By("asserting the reporter Service routes to the standalone reporter pods")
		Expect(reporterSvc).NotTo(BeNil(), "overlay must render the reporter Service")
		Expect(reporterSvc.Spec.Selector).To(Equal(shippedReporterPodLabels()),
			"overlay Service selector must match the shipped reporter pod labels the live spec wires up")

		By("asserting the rendered reporter Deployment is the pinned --reporter-only artifact")
		Expect(reporterDeploy).NotTo(BeNil(), "overlay must render the reporter Deployment")
		Expect(reporterDeploy.Spec.Template.Spec.Containers).To(HaveLen(1))
		Expect(reporterDeploy.Spec.Template.Spec.Containers[0].Args).To(ContainElement("--reporter-only"))
		Expect(reporterDeploy.Spec.Template.Spec.Containers[0].Image).To(HavePrefix("ghcr.io/grantbarry29/scrutineer:"),
			"images transform must replace the controller:latest placeholder")
	})
})

func containerByName(containers []corev1.Container, name string) *corev1.Container {
	for i := range containers {
		if containers[i].Name == name {
			return &containers[i]
		}
	}
	return nil
}

// deployShippedReporter applies the shipped ServiceAccount + --reporter-only Deployment
// (with the kind-loaded image substituted for the manifest's controller:latest
// placeholder) plus reporter-role-equivalent RBAC, and waits for the pod to pass its
// readiness probe.
func deployShippedReporter(ctx context.Context) {
	GinkgoHelper()
	sa, deploy := loadShippedReporterObjects()

	By("asserting the shipped Deployment runs the standalone reporter")
	Expect(sa.Name).To(Equal(shippedReporterSA))
	Expect(deploy.Name).To(Equal(shippedReporterDeploy))
	Expect(deploy.Spec.Template.Spec.ServiceAccountName).To(Equal(shippedReporterSA))
	Expect(deploy.Spec.Template.Labels).To(Equal(shippedReporterPodLabels()),
		"shipped pod labels must match the overlay's Service selector")
	Expect(deploy.Spec.Template.Spec.Containers).To(HaveLen(1))
	Expect(deploy.Spec.Template.Spec.Containers[0].Args).To(ContainElement("--reporter-only"))

	By("substituting the kind-loaded image for the manifest's controller:latest placeholder")
	deploy.Spec.Template.Spec.Containers[0].Image = scrutineerE2EImage()
	deploy.Spec.Template.Spec.Containers[0].ImagePullPolicy = corev1.PullIfNotPresent

	ensureScrutineerSystemNamespace(ctx)
	deployStandaloneReporterRBAC(ctx)

	By("applying the shipped ServiceAccount and Deployment")
	Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, sa))).To(Succeed())
	Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, deploy))).To(Succeed())

	By("waiting for the reporter pod to become ready (readiness probe passes)")
	Eventually(func(g Gomega) {
		var got appsv1.Deployment
		g.Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: scrutineerSystemNamespace, Name: shippedReporterDeploy}, &got)).To(Succeed())
		g.Expect(got.Status.ReadyReplicas).To(Equal(int32(1)))
	}, 90*time.Second, time.Second).Should(Succeed())
}

// pointReporterServiceAtShippedPods creates (replacing if needed) the
// scrutineer-controller-reporter Service with the selector the overlay patches in, so
// the sidecars' fixed reporter URL (job.DefaultReporterURL) resolves to the shipped
// reporter pods. An earlier spec's deployInClusterReporter may have created this
// Service routing to its Go-built reporter — replace it; later specs recreate theirs.
func pointReporterServiceAtShippedPods(ctx context.Context) {
	GinkgoHelper()
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      shippedReporterDeploy,
			Namespace: scrutineerSystemNamespace,
			Labels:    shippedReporterPodLabels(),
		},
		Spec: corev1.ServiceSpec{
			Selector: shippedReporterPodLabels(),
			Ports: []corev1.ServicePort{{
				Name:       "reporter",
				Port:       8088,
				TargetPort: intstr.FromString("reporter"),
				Protocol:   corev1.ProtocolTCP,
			}},
		},
	}
	_ = k8sClient.Delete(ctx, &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: svc.Name, Namespace: svc.Namespace},
	})
	Eventually(func() error {
		return k8sClient.Create(ctx, svc.DeepCopy())
	}, 15*time.Second, 500*time.Millisecond).Should(Succeed())
}

// deployStandaloneReporterRBAC grants the shipped scrutineer-reporter ServiceAccount the
// reporter-role permissions it needs (the overlay retargets reporter-role to this SA; here
// we bind an equivalent ClusterRole so the standalone reporter can read sessions and merge
// status). Mirrors the rules in deployInClusterReporter.
func deployStandaloneReporterRBAC(ctx context.Context) {
	GinkgoHelper()
	cr := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: standaloneReporterRoleName},
		Rules: []rbacv1.PolicyRule{
			{APIGroups: []string{"authentication.k8s.io"}, Resources: []string{"tokenreviews"}, Verbs: []string{"create"}},
			{APIGroups: []string{"scrutineer.sh"}, Resources: []string{"agentsessions"}, Verbs: []string{"get"}},
			{APIGroups: []string{"scrutineer.sh"}, Resources: []string{"agentsessions/status"}, Verbs: []string{"get", "update", "patch"}},
			{APIGroups: []string{"scrutineer.sh"}, Resources: []string{"approvalrequests"}, Verbs: []string{"get", "list", "create"}},
			{APIGroups: []string{"batch"}, Resources: []string{"jobs"}, Verbs: []string{"get"}},
			{APIGroups: []string{""}, Resources: []string{"pods"}, Verbs: []string{"get"}},
		},
	}
	Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, cr))).To(Succeed())

	crb := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: standaloneReporterRoleName},
		Subjects: []rbacv1.Subject{{
			Kind:      "ServiceAccount",
			Name:      shippedReporterSA,
			Namespace: scrutineerSystemNamespace,
		}},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     standaloneReporterRoleName,
		},
	}
	Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, crb))).To(Succeed())
}

func cleanupStandaloneReporter(ctx context.Context) {
	_ = k8sClient.Delete(ctx, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: shippedReporterDeploy, Namespace: scrutineerSystemNamespace},
	})
	_ = k8sClient.Delete(ctx, &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: shippedReporterSA, Namespace: scrutineerSystemNamespace},
	})
	_ = k8sClient.Delete(ctx, &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: standaloneReporterRoleName}})
	_ = k8sClient.Delete(ctx, &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: standaloneReporterRoleName}})
	Eventually(func() bool {
		err := k8sClient.Get(ctx, client.ObjectKey{Namespace: scrutineerSystemNamespace, Name: shippedReporterDeploy}, &appsv1.Deployment{})
		return apierrors.IsNotFound(err)
	}, 30*time.Second, time.Second).Should(BeTrue())
}
