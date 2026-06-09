/*
Copyright 2026 The Relay Authors.

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

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
	"github.com/secureai/relay/internal/controller/agentsession"
)

const reportPath = "/v1/report"

// Handler serves POST /v1/report.
type Handler struct {
	Writer   client.StatusWriter
	Reader   client.Reader
	Verifier IdentityVerifier
	Recorder record.EventRecorder
	Limiter  *sessionRateLimiter
	Now      func() time.Time
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path != reportPath {
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
			writeError(w, ErrPayloadTooLarge, http.StatusRequestEntityTooLarge, "")
			return
		}
		writeError(w, ErrBadRequest, http.StatusBadRequest, "invalid JSON")
		return
	}

	if _, err := h.Verifier.Verify(r.Context(), r, req.Session); err != nil {
		status := http.StatusUnauthorized
		if errors.Is(err, ErrForbidden) {
			status = http.StatusForbidden
		} else if !errors.Is(err, ErrUnauthorized) {
			status = http.StatusInternalServerError
		}
		writeError(w, err, status, err.Error())
		return
	}

	sessionKey := types.NamespacedName{Namespace: req.Session.Namespace, Name: req.Session.Name}
	if h.Limiter != nil && !h.Limiter.allow(sessionKey.String(), receivedAt) {
		w.Header().Set("Retry-After", "1")
		writeError(w, ErrRateLimited, http.StatusTooManyRequests, "rate limit exceeded")
		return
	}

	var session relayv1alpha1.AgentSession
	if err := h.Reader.Get(r.Context(), sessionKey, &session); err != nil {
		if apierrors.IsNotFound(err) {
			writeError(w, ErrNotFound, http.StatusNotFound, "AgentSession not found")
			return
		}
		http.Error(w, "failed to load session", http.StatusInternalServerError)
		return
	}

	var effectiveMode relayv1alpha1.PolicyMode
	if session.Status.EffectivePolicy != nil {
		effectiveMode = session.Status.EffectivePolicy.Mode
	}

	report, err := ValidateAndNormalizeReport(req, receivedAt, effectiveMode)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, ErrBadRequest) {
			status = http.StatusBadRequest
		}
		writeError(w, err, status, err.Error())
		return
	}

	hadViolations := len(session.Status.Violations) == 0 && len(report.Decisions) > 0
	if err := agentsession.PatchRuntimePolicyReport(r.Context(), h.Writer, h.Reader, sessionKey, report); err != nil {
		if apierrors.IsNotFound(err) {
			writeError(w, ErrNotFound, http.StatusNotFound, "AgentSession not found")
			return
		}
		if strings.Contains(err.Error(), "exhausted retries") || strings.Contains(err.Error(), "conflict") {
			writeError(w, ErrConflict, http.StatusConflict, "status update conflict")
			return
		}
		http.Error(w, "failed to merge report", http.StatusInternalServerError)
		return
	}

	if hadViolations && h.Recorder != nil {
		for _, d := range report.Decisions {
			if d.Action == relayv1alpha1.PolicyDecisionDeny || d.Action == relayv1alpha1.PolicyDecisionDryRun {
				h.Recorder.Event(&session, "Warning", "RuntimeViolation", d.Message)
				break
			}
		}
	}

	w.WriteHeader(http.StatusAccepted)
}

func writeError(w http.ResponseWriter, sentinel error, status int, msg string) {
	if msg == "" {
		msg = sentinel.Error()
	}
	http.Error(w, msg, status)
}
