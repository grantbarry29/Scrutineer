/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package reporter

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/audit"
	"github.com/grantbarry29/scrutineer/internal/controller/agentsession"
	"github.com/grantbarry29/scrutineer/internal/metrics"
	"github.com/grantbarry29/scrutineer/internal/tracing"
)

const reportPath = "/v1/report"

// Handler serves POST /v1/report.
type Handler struct {
	Writer client.StatusWriter
	// Reader is the uncached reader (see reporter.Options read-consistency policy):
	// used for the session pre-read here and passed to PatchRuntimePolicyReport,
	// whose optimistic-concurrency Update requires read-after-write consistency.
	Reader    client.Reader
	Verifier  IdentityVerifier
	Recorder  record.EventRecorder
	Limiter   *sessionRateLimiter
	ReportIDs *reportIDCache
	Now       func() time.Time
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	result := "internal_error"
	defer func() {
		metrics.ObserveRuntimeReport(result, time.Since(start))
	}()

	ctx := r.Context()
	sessionNamespace := ""
	sessionName := ""
	backend := ""
	decisionCount := -1
	defer func() {
		tracing.SetReportSpanAttributes(ctx, sessionNamespace, sessionName, backend, result, decisionCount)
	}()

	if r.Method != http.MethodPost {
		result = "method_not_allowed"
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path != reportPath {
		result = "not_found"
		http.NotFound(w, r)
		return
	}

	now := time.Now
	if h.Now != nil {
		now = h.Now
	}
	receivedAt := now()

	var req ReportRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, MaxReportBytes)).Decode(&req); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			result = "payload_too_large"
			writeError(w, ErrPayloadTooLarge, http.StatusRequestEntityTooLarge, "")
			return
		}
		result = "bad_request"
		writeError(w, ErrBadRequest, http.StatusBadRequest, "invalid JSON")
		return
	}

	if _, err := h.Verifier.Verify(r.Context(), r, req.Session); err != nil {
		status := http.StatusUnauthorized
		result = "unauthorized"
		if errors.Is(err, ErrForbidden) {
			status = http.StatusForbidden
			result = "forbidden"
		} else if !errors.Is(err, ErrUnauthorized) {
			status = http.StatusInternalServerError
			result = "internal_error"
		}
		writeError(w, err, status, err.Error())
		return
	}

	sessionKey := types.NamespacedName{Namespace: req.Session.Namespace, Name: req.Session.Name}
	sessionNamespace = sessionKey.Namespace
	sessionName = sessionKey.Name
	backend = req.Backend
	decisionCount = len(req.Decisions)
	if h.Limiter != nil && !h.Limiter.allow(sessionKey.String(), receivedAt) {
		result = "rate_limited"
		w.Header().Set("Retry-After", "1")
		writeError(w, ErrRateLimited, http.StatusTooManyRequests, "rate limit exceeded")
		return
	}

	// Uncached read by design — see the read-consistency policy on reporter.Options.
	// This pre-read supplies the effective policy mode for normalization and the
	// object for the RuntimeViolation event; the authoritative read happens inside
	// PatchRuntimePolicyReport below.
	var session scrutineerv1alpha1.AgentSession
	if err := h.Reader.Get(r.Context(), sessionKey, &session); err != nil {
		if apierrors.IsNotFound(err) {
			result = "not_found"
			writeError(w, ErrNotFound, http.StatusNotFound, "AgentSession not found")
			return
		}
		result = "internal_error"
		http.Error(w, "failed to load session", http.StatusInternalServerError)
		return
	}

	var effectiveMode scrutineerv1alpha1.PolicyMode
	if session.Status.EffectivePolicy != nil {
		effectiveMode = session.Status.EffectivePolicy.Mode
	}

	report, err := ValidateAndNormalizeReport(req, receivedAt, effectiveMode)
	if err != nil {
		result = "bad_request"
		status := http.StatusBadRequest
		if errors.Is(err, ErrBadRequest) {
			status = http.StatusBadRequest
		}
		writeError(w, err, status, err.Error())
		return
	}

	reportKey := ""
	if reportID := normalizeReportID(req.ReportID); reportID != "" && h.ReportIDs != nil {
		reportKey = reportIDCacheKey(sessionKey, reportID)
		// Atomically reserve the reportId so concurrent identical reports can't both
		// pass the dedup check before either records it. Released below if the status
		// patch fails, so a failed report can still be retried.
		if !h.ReportIDs.reserve(reportKey, receivedAt) {
			result = "duplicate"
			w.WriteHeader(http.StatusAccepted)
			return
		}
	}

	if err := agentsession.PatchRuntimePolicyReport(r.Context(), h.Writer, h.Reader, sessionKey, report); err != nil {
		h.ReportIDs.release(reportKey)
		if apierrors.IsNotFound(err) {
			result = "not_found"
			writeError(w, ErrNotFound, http.StatusNotFound, "AgentSession not found")
			return
		}
		if strings.Contains(err.Error(), "exhausted retries") || strings.Contains(err.Error(), "conflict") {
			result = "conflict"
			writeError(w, ErrConflict, http.StatusConflict, "status update conflict")
			return
		}
		result = "internal_error"
		http.Error(w, "failed to merge report", http.StatusInternalServerError)
		return
	}

	// Emit a RuntimeViolation event whenever *this* report carries a violating
	// (Deny/DryRun) decision — independent of how many violations the session has
	// already accumulated. One event per violating report.
	if h.Recorder != nil {
		for _, d := range report.Decisions {
			if d.Action == scrutineerv1alpha1.PolicyDecisionDeny || d.Action == scrutineerv1alpha1.PolicyDecisionDryRun {
				h.Recorder.Event(&session, "Warning", "RuntimeViolation", d.Message)
				break
			}
		}
	}

	result = "accepted"
	// Runtime reports come from cooperative data-plane sidecars: self-reported assurance.
	audit.Emit(ctx, audit.RuntimeReport(sessionNamespace, sessionName, backend, len(report.Decisions),
		string(scrutineerv1alpha1.EvidenceSelfReported), receivedAt))
	w.WriteHeader(http.StatusAccepted)
}

func writeError(w http.ResponseWriter, sentinel error, status int, msg string) {
	if msg == "" {
		msg = sentinel.Error()
	}
	http.Error(w, msg, status)
}
