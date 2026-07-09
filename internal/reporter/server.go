/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package reporter

import (
	"context"
	"errors"
	"net/http"
	"time"

	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/grantbarry29/scrutineer/internal/tracing"
)

// Options configures the runtime reporter HTTP server.
//
// Read-consistency policy: every hot-path read in this package goes through the
// uncached APIReader (mgr.GetAPIReader()), never the cached manager client. This
// is deliberate, not an oversight (issue #47):
//
//   - Consistency: the status-merge path (PatchRuntimePolicyReport) does
//     optimistic-concurrency Update inside a retry loop. A cached read returns a
//     stale resourceVersion, so the Update would conflict, retry, re-read the same
//     stale version, and exhaust its retries — turning every report under
//     contention into a spurious 409. It needs read-after-write consistency.
//   - Least privilege / footprint: the reporter ships a dedicated get-only
//     reporter-role and the standalone --reporter-only Deployment runs under a
//     128Mi budget (issue #34). A cached client is informer-backed, so it would
//     require list;watch on AgentSessions/Pods/Jobs/ApprovalRequests and hold
//     namespace-wide informer caches in memory — undermining exactly the
//     least-privilege, low-footprint design the standalone reporter exists for.
//
// Reads are instead bounded by per-session rate limiting (see ratelimit.go). If
// the standalone reporter's RBAC/footprint constraints are ever relaxed, the
// non-consistency-critical reads (session existence pre-read, identity pod/Job
// lookups, countOutstandingHolds) could move to the cached client; the
// status-merge read must remain uncached regardless.
type Options struct {
	BindAddress string
	Client      client.Client
	// APIReader is the uncached reader used for ALL reporter reads — see the
	// read-consistency policy on Options above.
	APIReader client.Reader
	Recorder  record.EventRecorder
	Audience  string
	Verifier  IdentityVerifier
}

// +kubebuilder:rbac:groups=authentication.k8s.io,resources=tokenreviews,verbs=create
// +kubebuilder:rbac:groups=scrutineer.sh,resources=agentsessions,verbs=get
// +kubebuilder:rbac:groups=scrutineer.sh,resources=agentsessions/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=scrutineer.sh,resources=approvalrequests,verbs=get;list;create
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get
// +kubebuilder:rbac:groups="",resources=pods,verbs=get

// NewRunnable returns a manager.Runnable that serves POST /v1/report.
func NewRunnable(opts Options) manager.Runnable {
	bind := opts.BindAddress
	if bind == "" {
		bind = DefaultBindAddress
	}

	verifier := opts.Verifier
	if verifier == nil {
		verifier = &KubeIdentityVerifier{
			Client:   opts.Client,
			Reader:   opts.APIReader,
			Audience: opts.Audience,
		}
	}

	handler := &Handler{
		Writer:    opts.Client.Status(),
		Reader:    opts.APIReader,
		Verifier:  verifier,
		Recorder:  opts.Recorder,
		Limiter:   newSessionRateLimiter(time.Second, DefaultReportRateBurst),
		ReportIDs: newReportIDCache(DefaultReportIDCacheTTL),
	}

	approvals := &ApprovalHandler{
		Client:   opts.Client,
		Reader:   opts.APIReader,
		Verifier: verifier,
		// Burst 1: hold registration is a human-approval control point, not a
		// batched evidence pipeline — strict spacing is the point.
		Limiter:        newSessionRateLimiter(DefaultApprovalRegisterInterval, 1),
		MaxOutstanding: DefaultMaxOutstandingApprovals,
	}

	mux := http.NewServeMux()
	mux.Handle(reportPath, tracing.HTTPMiddleware(tracing.ReporterTracer(), "runtime.report", handler))
	mux.Handle(approvalsPath, tracing.HTTPMiddleware(tracing.ReporterTracer(), "runtime.approval", approvals))
	mux.Handle(approvalsPrefix, tracing.HTTPMiddleware(tracing.ReporterTracer(), "runtime.approval", approvals))

	return &httpServer{
		addr: bind,
		mux:  mux,
	}
}

type httpServer struct {
	addr string
	mux  http.Handler
	srv  *http.Server
}

func (s *httpServer) Start(ctx context.Context) error {
	s.srv = &http.Server{
		Addr:    s.addr,
		Handler: s.mux,
	}

	errCh := make(chan error, 1)
	go func() {
		if err := s.srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		close(errCh)
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.srv.Shutdown(shutdownCtx)
		return nil
	case err, ok := <-errCh:
		if !ok {
			return nil
		}
		return err
	}
}
