//go:build e2e

/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package e2e runs the Scrutineer controller against a real kind cluster (whatever
// ~/.kube/config currently points at) and exercises end-to-end behavior of the
// AgentSession CRD, its admission validation, and the controller's reconciliation.
//
// Test layout (same package, build tag `e2e`):
//   - suite_test.go     — cluster preflight, in-process manager
//   - fixtures_test.go  — namespaces, session builders, options
//   - assertions_test.go — phase/job/condition wait helpers
//   - agentsession_test.go — Ginkgo specs only
//   - egress_proxy_test.go — live per-session Envoy egress proxy: agent egress
//     traverses Envoy (access log, incl. CONNECT) + teardown
//   - fqdn_egress_test.go / observed_evidence_test.go — Envoy observed-evidence path
//   - lock_gate_e2e_test.go — verified-or-refused NetworkPolicy lock gate (#70)
//
// Run with:   make test-e2e
// Skipped by: `go test ./...` (build tag `e2e`).
//
// Preconditions enforced by the suite:
//   - A reachable Kubernetes cluster (via current kubeconfig).
//   - The AgentSession CRD installed (run `make install` or `make dev-up` first).
//   - No other scrutineer-controller-manager running against the same cluster
//     (the suite starts its own in-process manager).
package e2e

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/controller/agentsession"
	"github.com/grantbarry29/scrutineer/internal/enforcement/lockverify"
)

// envLockVerify enables the verified-or-refused lock gate (#70) in the in-process
// manager. Set by make test-e2e-net: the gate's behavior is CNI-dependent, so it is
// exercised by the networking suite on both clusters. The standard suite keeps it off
// until pivot Phase 2 retires the cooperative-era specs that predate the gate.
const envLockVerify = "SCRUTINEER_E2E_LOCK_VERIFY"

func lockVerifyEnabled() bool { return os.Getenv(envLockVerify) == "1" }

// Suite-wide handles set up in BeforeSuite and used by every It block.
var (
	cfg       *rest.Config
	k8sClient client.Client
	scheme    = runtime.NewScheme()

	// managerCancel stops the in-process controller manager goroutine.
	managerCancel context.CancelFunc
	managerDone   chan struct{}

	// lockVerifier is the wired verifier when lockVerifyEnabled(); the suite waits for
	// its first conclusive verdict before running enforced specs (see BeforeSuite).
	lockVerifier *lockverify.Verifier
)

// Default deadlines used by helpers. Generous enough for kind on a laptop;
// short enough that a regression is obvious.
const (
	terminalPhaseTimeout = 90 * time.Second
	terminalPhasePoll    = 2 * time.Second
	deniedPhaseTimeout   = 20 * time.Second
	deniedPhasePoll      = 500 * time.Millisecond
)

func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Scrutineer e2e Suite")
}

var _ = BeforeSuite(func() {
	ctrl.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	By("registering core + scrutineer types on the scheme")
	Expect(clientgoscheme.AddToScheme(scheme)).To(Succeed())
	Expect(appsv1.AddToScheme(scheme)).To(Succeed())
	Expect(rbacv1.AddToScheme(scheme)).To(Succeed())
	Expect(scrutineerv1alpha1.AddToScheme(scheme)).To(Succeed())

	By("loading kubeconfig from the current environment")
	var err error
	cfg, err = config.GetConfig()
	Expect(err).NotTo(HaveOccurred(),
		"could not load a kubeconfig — is the dev container attached to kind? "+
			"(run 'make dev-up' first)")

	By("constructing a direct client")
	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())

	By("verifying the AgentSession CRD is installed")
	verifyAgentSessionCRDInstalled()

	By("starting the controller manager in-process")
	startControllerManager()

	// The lock gate fails closed until the verifier reaches a conclusive verdict:
	// running enforced specs before the first probe settles would (correctly) see them
	// held. Wait for the settled verdict once, up front, so every spec runs against a
	// stable gate — mirroring a controller that has completed its startup probe.
	if lockVerifyEnabled() {
		By("waiting for the first conclusive lock-verification verdict")
		Eventually(func() lockverify.Verdict {
			return lockVerifier.Current().Verdict
		}, 4*time.Minute, 3*time.Second).ShouldNot(Equal(lockverify.VerdictUnknown),
			"lock verifier produced no conclusive verdict — probe pods may be unschedulable")
	}
})

var _ = AfterSuite(func() {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cleanupInClusterReporter(ctx)

	if managerCancel != nil {
		By("stopping the controller manager")
		managerCancel()
		select {
		case <-managerDone:
		case <-time.After(10 * time.Second):
			Fail("controller manager did not stop within 10s after cancel")
		}
	}
})

// verifyAgentSessionCRDInstalled fails the suite fast with a clear message if
// the CRD is missing, since otherwise every It block would fail with a confusing
// "no kind 'AgentSession' is registered" error.
func verifyAgentSessionCRDInstalled() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var list scrutineerv1alpha1.AgentSessionList
	err := k8sClient.List(ctx, &list, client.InNamespace("default"), client.Limit(1))
	if err == nil {
		return
	}
	if apierrors.IsNotFound(err) {
		Fail("AgentSession CRD is not installed. Run 'make install' or 'make dev-up' first.")
	}
	// Other errors (network, RBAC, etc.) — surface with full context.
	Fail(fmt.Sprintf("failed to list AgentSessions during suite preflight: %v", err))
}

// startControllerManager spins up an in-process controller-runtime manager that
// reconciles AgentSessions against the live cluster. We disable leader election,
// disable the metrics server, and disable health probes so this can coexist with
// nothing-special running on standard ports.
func startControllerManager() {
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
		LeaderElection:         false,
	})
	Expect(err).NotTo(HaveOccurred())

	reconciler := &agentsession.AgentSessionReconciler{
		Client:    mgr.GetClient(),
		APIReader: mgr.GetAPIReader(),
		Scheme:    mgr.GetScheme(),
		Recorder:  mgr.GetEventRecorderFor("scrutineer-e2e"),
	}
	if lockVerifyEnabled() {
		By("wiring the lock-verification gate (probe pods use the kind-loaded controller image)")
		ensureScrutineerSystemNamespace(context.Background())
		verifier := &lockverify.Verifier{
			Client:    mgr.GetClient(),
			Reader:    mgr.GetAPIReader(),
			Namespace: scrutineerSystemNamespace,
			Image:     scrutineerE2EImage(),
			Interval:  30 * time.Second,
			PodWait:   60 * time.Second,
		}
		Expect(mgr.Add(verifier)).To(Succeed())
		reconciler.LockVerifier = verifier
		lockVerifier = verifier
	}
	Expect(reconciler.SetupWithManager(mgr)).To(Succeed())

	ctx, cancel := context.WithCancel(context.Background())
	managerCancel = cancel
	managerDone = make(chan struct{})

	go func() {
		defer GinkgoRecover()
		defer close(managerDone)
		if err := mgr.Start(ctx); err != nil && ctx.Err() == nil {
			Fail(fmt.Sprintf("controller manager terminated unexpectedly: %v", err))
		}
	}()

	By("waiting for manager caches to sync")
	Eventually(func() bool {
		// A direct LIST against the cluster confirms the apiserver is reachable
		// even if the manager hasn't fully synced caches yet — the manager's
		// cached client below would block on cache sync.
		var ns metav1.PartialObjectMetadataList
		ns.SetGroupVersionKind(metav1.SchemeGroupVersion.WithKind("NamespaceList"))
		return mgr.GetCache().WaitForCacheSync(ctx)
	}, 30*time.Second, 500*time.Millisecond).Should(BeTrue(),
		"manager caches did not sync within 30s")
}
