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
//   - suite_test.go     — cluster preflight, in-process manager (proc 1 only, #156)
//   - fixtures_test.go  — namespaces, session builders, options
//   - assertions_test.go — phase/job/condition wait helpers
//   - agentsession_test.go — Ginkgo specs only
//   - egress_proxy_test.go — live per-session Envoy egress proxy: agent egress
//     traverses Envoy (access log, incl. CONNECT) + teardown
//   - fqdn_egress_test.go / observed_evidence_test.go — Envoy observed-evidence path
//   - lock_gate_e2e_test.go — verified-or-refused NetworkPolicy lock gate (#70)
//
// Run with:   make test-e2e (parallel via the ginkgo CLI, #156)
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
	"encoding/json"
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
	ctrlconfig "sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/controller/agentsession"
	"github.com/grantbarry29/scrutineer/internal/enforcement/lockverify"
)

// envLockVerify enables the verified-or-refused lock gate (#70) in the in-process
// manager. Set by make test-e2e-net: the gate's behavior is CNI-dependent, so it is
// exercised by the networking suite on both clusters. The standard suite keeps it off
// so its specs stay CNI-independent.
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

// suiteSetupData is the payload proc 1's SynchronizedBeforeSuite half hands every
// parallel process: cluster-wide facts probed once so N processes don't re-probe them.
type suiteSetupData struct {
	// CNIProbed reports whether proc 1 ran the egress-enforcement probe (it does when
	// the lock gate is enabled, i.e. under make test-e2e-net). When false, a process
	// that needs the verdict probes lazily (see egressEnforcingCNI).
	CNIProbed   bool
	CNIEnforces bool
}

// The suites run specs in parallel across Ginkgo processes (#156). Exactly one
// in-process controller manager may reconcile the shared cluster, so proc 1 starts it
// in the first half; every process — including proc 1 — builds its own client
// scaffolding in the second half. Specs that need the manager handle itself
// (restart/downtime, #150) are Serial, which Ginkgo guarantees to run on proc 1 after
// all parallel specs have finished.
var _ = SynchronizedBeforeSuite(func(ctx SpecContext) []byte {
	initSuiteScaffolding()

	By("starting the controller manager in-process (proc 1 only)")
	startControllerManager()

	// The lock gate fails closed until the verifier reaches a conclusive verdict:
	// running enforced specs before the first probe settles would (correctly) see them
	// held. Wait for the settled verdict once, up front, so every spec runs against a
	// stable gate — mirroring a controller that has completed its startup probe.
	waitForLockVerdictIfEnabled()

	// One-time cluster setup for the networking suite, consolidated here so parallel
	// processes don't each deploy the reporter or launch a duplicate probe namespace.
	// Per-spec deployInClusterReporter calls remain as idempotent fast no-ops, and the
	// image-missing case still skips at spec level rather than failing the suite here.
	data := suiteSetupData{}
	if lockVerifyEnabled() {
		if clusterImageRunnable(ctx, scrutineerE2EImage()) {
			deployInClusterReporter(ctx)
		}
		data.CNIProbed = true
		data.CNIEnforces = probeEgressEnforced(ctx)
	}
	payload, err := json.Marshal(data)
	Expect(err).NotTo(HaveOccurred())
	return payload
}, func(ctx SpecContext, payload []byte) {
	initSuiteScaffolding()

	var data suiteSetupData
	Expect(json.Unmarshal(payload, &data)).To(Succeed())
	if data.CNIProbed {
		cniEnforceOnce.Do(func() { cniEnforces = data.CNIEnforces })
	}
})

var _ = SynchronizedAfterSuite(func() {
	// Per-process teardown: nothing — every spec-owned resource is DeferCleanup'd.
}, func() {
	// Proc 1 tears down the shared reporter and the manager it started.
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

// initSuiteScaffolding builds the per-process suite handles (logger, scheme,
// kubeconfig, direct client) and fails fast if the cluster is unusable. Idempotent:
// proc 1 runs it in both SynchronizedBeforeSuite halves.
func initSuiteScaffolding() {
	if k8sClient != nil {
		return
	}
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
}

// waitForLockVerdictIfEnabled blocks until the wired lock verifier reaches a conclusive
// verdict (lock-verify mode only) — the gate fails closed until then.
func waitForLockVerdictIfEnabled() {
	if !lockVerifyEnabled() {
		return
	}
	By("waiting for a conclusive lock-verification verdict")
	Eventually(func() lockverify.Verdict {
		return lockVerifier.Current().Verdict
	}, 4*time.Minute, 3*time.Second).ShouldNot(Equal(lockverify.VerdictUnknown),
		"lock verifier produced no conclusive verdict — probe pods may be unschedulable")
}

// stopControllerManager stops the in-process manager and waits for it to exit — the
// "controller down" half of a restart/downtime spec (#150). Callers MUST bring it back
// with restartControllerManager before their spec ends (guard it with DeferCleanup so a
// failing assertion cannot leave the rest of the suite running against a dead
// controller). Restart specs are Serial for the same reason.
func stopControllerManager() {
	By("stopping the in-process controller manager")
	Expect(managerCancel).NotTo(BeNil(), "controller manager is not running")
	managerCancel()
	select {
	case <-managerDone:
	case <-time.After(10 * time.Second):
		Fail("controller manager did not stop within 10s after cancel")
	}
	managerCancel = nil
}

// controllerManagerRunning reports whether the in-process manager is currently up.
func controllerManagerRunning() bool { return managerCancel != nil }

// restartControllerManager brings the manager back after stopControllerManager: a true
// cold start (fresh manager, fresh caches, and in lock-verify mode a fresh verifier
// whose conclusive verdict is awaited), so the calling spec resumes against a settled
// controller exactly as BeforeSuite provides one.
func restartControllerManager() {
	By("restarting the in-process controller manager")
	startControllerManager()
	waitForLockVerdictIfEnabled()
}

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
		// Restart specs (#150) start a second manager in this same test process;
		// controller-runtime's global name-uniqueness check (metrics hygiene) would
		// reject re-registering "agentsession". Test-process concern only — the
		// production manager never restarts in-process.
		Controller: ctrlconfig.Controller{SkipNameValidation: boolPtr(true)},
	})
	Expect(err).NotTo(HaveOccurred())

	reconciler := &agentsession.AgentSessionReconciler{
		Client:    mgr.GetClient(),
		APIReader: mgr.GetAPIReader(),
		Scheme:    mgr.GetScheme(),
		Recorder:  mgr.GetEventRecorderFor("scrutineer-e2e"),
		// Small rotation threshold (#98) so the rotation spec can drive a real cycle
		// with a few hundred requests. Well above what any other spec's probe traffic
		// generates (~16KiB), so it does not perturb them.
		EgressRotateAfterBytes: 48 << 10,
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
