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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
)

const (
	approvalsPath   = "/v1/approvals"
	approvalsPrefix = "/v1/approvals/"
	// MaxApprovalBodyBytes bounds the approval-channel request body.
	MaxApprovalBodyBytes = 16 * 1024
	// DefaultMaxOutstandingApprovals caps undecided runtime holds per session, so a
	// chatty or hostile agent cannot create unbounded ApprovalRequest objects.
	DefaultMaxOutstandingApprovals = 16
	// DefaultApprovalRegisterInterval is the minimum spacing between NEW holds for one
	// session (re-registering an existing hold is exempt — that is the keepalive path).
	DefaultApprovalRegisterInterval = time.Second
)

// ApprovalHandler serves the mid-execution per-tool approval channel:
//
//	POST /v1/approvals        register/lookup a hold (idempotent by requestId)
//	GET  /v1/approvals/{id}    poll a hold's current state
//
// It reuses the reporter's identity model (TokenReview + pod→Job→session
// ownership) so a sidecar can only ever act on its own session. The controller
// remains the sole writer of ApprovalRequest.status; this handler only creates
// the runtime request (idempotently) and reports the controller-observed state.
// See docs/design/phase-5-runtime-tool-approval.md.
type ApprovalHandler struct {
	// Client is a full client used to create runtime ApprovalRequests.
	Client client.Client
	// Reader is the uncached reader for all reads on this handler — fresh
	// get/lookup avoids stale-cache races, and the uncached path keeps the
	// standalone reporter's least-privilege get-only RBAC + low memory footprint
	// (no informer cache). See the read-consistency policy on reporter.Options (#47).
	Reader   client.Reader
	Verifier IdentityVerifier
	// Limiter throttles the creation of NEW holds per session (nil disables).
	Limiter *sessionRateLimiter
	// MaxOutstanding caps undecided runtime holds per session (<=0 disables).
	MaxOutstanding int
	// Now is the clock used for rate limiting (defaults to time.Now).
	Now func() time.Time
}

func (h *ApprovalHandler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

// ServeHTTP dispatches the approval channel by method/path.
func (h *ApprovalHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == approvalsPath:
		h.register(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, approvalsPrefix):
		h.lookup(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// register creates (or looks up) the runtime ApprovalRequest for a held tool call
// and returns its current state. Idempotent per (session, requestId).
func (h *ApprovalHandler) register(w http.ResponseWriter, r *http.Request) {
	var req ApprovalRegisterRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, MaxApprovalBodyBytes)).Decode(&req); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, ErrPayloadTooLarge, http.StatusRequestEntityTooLarge, "")
			return
		}
		writeError(w, ErrBadRequest, http.StatusBadRequest, "invalid JSON")
		return
	}

	if err := validateApprovalRegister(&req); err != nil {
		writeError(w, ErrBadRequest, http.StatusBadRequest, err.Error())
		return
	}

	if _, err := h.Verifier.Verify(r.Context(), r, req.Session); err != nil {
		writeAuthError(w, err)
		return
	}

	sessionKey := types.NamespacedName{Namespace: req.Session.Namespace, Name: req.Session.Name}
	// The session must exist before we hold a tool call for it.
	var session scrutineerv1alpha1.AgentSession
	if err := h.Reader.Get(r.Context(), sessionKey, &session); err != nil {
		if apierrors.IsNotFound(err) {
			writeError(w, ErrNotFound, http.StatusNotFound, "AgentSession not found")
			return
		}
		http.Error(w, "failed to load session", http.StatusInternalServerError)
		return
	}

	name := RuntimeApprovalName(req.Session.Name, req.RequestID)
	key := types.NamespacedName{Namespace: req.Session.Namespace, Name: name}

	existing, err := h.getApproval(r.Context(), key)
	if err != nil {
		http.Error(w, "failed to load approval", http.StatusInternalServerError)
		return
	}
	if existing == nil {
		// Abuse controls apply only to genuinely NEW holds: re-registering an
		// existing requestId (the gateway's keepalive) is idempotent and exempt.
		if h.Limiter != nil && !h.Limiter.allow(sessionKey.String(), h.now()) {
			w.Header().Set("Retry-After", "1")
			writeError(w, ErrRateLimited, http.StatusTooManyRequests, "approval registration rate limit exceeded")
			return
		}
		if h.MaxOutstanding > 0 {
			outstanding, cErr := h.countOutstandingHolds(r.Context(), req.Session.Namespace, req.Session.Name)
			if cErr != nil {
				http.Error(w, "failed to count outstanding holds", http.StatusInternalServerError)
				return
			}
			if outstanding >= h.MaxOutstanding {
				w.Header().Set("Retry-After", "5")
				writeError(w, ErrRateLimited, http.StatusTooManyRequests,
					"too many outstanding approval holds for session")
				return
			}
		}
		created, cErr := h.createApproval(r.Context(), &session, &req, name)
		if cErr != nil {
			http.Error(w, "failed to create approval", http.StatusInternalServerError)
			return
		}
		existing = created
	}

	writeApprovalResponse(w, existing)
}

// countOutstandingHolds returns the number of undecided runtime ApprovalRequests
// gating the given session (the cap denominator).
//
// This namespace-scoped List runs only on the registration of a genuinely NEW
// hold (re-registration/keepalive is exempt) and is further throttled by the
// per-session Limiter, so it is not on the per-tool-call poll path. It uses the
// uncached reader for the same least-privilege reason as the rest of the handler
// (a cached List would need list;watch + an informer cache that the standalone
// reporter deliberately avoids — #47); a slightly stale count is acceptable for
// an abuse cap, but switching to the cache is not free here.
func (h *ApprovalHandler) countOutstandingHolds(ctx context.Context, namespace, sessionName string) (int, error) {
	var list scrutineerv1alpha1.ApprovalRequestList
	if err := h.Reader.List(ctx, &list, client.InNamespace(namespace)); err != nil {
		return 0, err
	}
	n := 0
	for i := range list.Items {
		req := &list.Items[i]
		if req.Spec.SessionRef.Name != sessionName || !req.Spec.IsRuntime() {
			continue
		}
		if !approvalStateFinal(req.Status.State) {
			n++
		}
	}
	return n, nil
}

// approvalStateFinal reports whether a hold has reached a terminal state and no
// longer counts toward the outstanding cap.
func approvalStateFinal(s scrutineerv1alpha1.ApprovalState) bool {
	switch s {
	case scrutineerv1alpha1.ApprovalStateGranted,
		scrutineerv1alpha1.ApprovalStateDenied,
		scrutineerv1alpha1.ApprovalStateExpired:
		return true
	}
	return false
}

// lookup returns the current state of a runtime ApprovalRequest by name, after
// confirming the caller owns the session the request gates.
func (h *ApprovalHandler) lookup(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, approvalsPrefix)
	namespace := strings.TrimSpace(r.URL.Query().Get("namespace"))
	if name == "" || strings.Contains(name, "/") || namespace == "" {
		writeError(w, ErrBadRequest, http.StatusBadRequest, "approval id and ?namespace= are required")
		return
	}

	got, err := h.getApproval(r.Context(), types.NamespacedName{Namespace: namespace, Name: name})
	if err != nil {
		http.Error(w, "failed to load approval", http.StatusInternalServerError)
		return
	}
	if got == nil {
		writeError(w, ErrNotFound, http.StatusNotFound, "ApprovalRequest not found")
		return
	}

	// Authorize against the session the request actually gates (defends against a
	// caller probing another session's holds).
	if _, err := h.Verifier.Verify(r.Context(), r, SessionRef{Namespace: namespace, Name: got.Spec.SessionRef.Name}); err != nil {
		writeAuthError(w, err)
		return
	}

	writeApprovalResponse(w, got)
}

// getApproval fetches a runtime ApprovalRequest, returning (nil, nil) when absent.
func (h *ApprovalHandler) getApproval(ctx context.Context, key types.NamespacedName) (*scrutineerv1alpha1.ApprovalRequest, error) {
	var req scrutineerv1alpha1.ApprovalRequest
	if err := h.Reader.Get(ctx, key, &req); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return &req, nil
}

// createApproval builds and creates the runtime ApprovalRequest owned by the
// session. On an AlreadyExists race it re-reads and returns the winner.
func (h *ApprovalHandler) createApproval(ctx context.Context, session *scrutineerv1alpha1.AgentSession, req *ApprovalRegisterRequest, name string) (*scrutineerv1alpha1.ApprovalRequest, error) {
	ar := &scrutineerv1alpha1.ApprovalRequest{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: req.Session.Namespace},
		Spec: scrutineerv1alpha1.ApprovalRequestSpec{
			SessionRef: scrutineerv1alpha1.ApprovalSessionRef{Name: req.Session.Name},
			Trigger:    scrutineerv1alpha1.ApprovalTriggerRuntime,
			RequestID:  req.RequestID,
			PolicyRef:  req.PolicyRef,
			Action:     req.Action,
			Scope: scrutineerv1alpha1.ApprovalScope{
				Target:    req.Target,
				ArgDigest: req.ArgDigest,
				Window:    approvalWindowFromString(req.Window),
			},
			Decision: scrutineerv1alpha1.ApprovalDecisionPending,
		},
	}
	// Owner ref (non-controller) so the hold is GC'd with its session, mirroring
	// the pre-exec request's GC behavior.
	if err := controllerutil.SetOwnerReference(session, ar, h.Client.Scheme()); err == nil {
		setApprovalBlockOwnerDeletion(ar, false)
	}

	if err := h.Client.Create(ctx, ar); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return h.getApproval(ctx, types.NamespacedName{Namespace: ar.Namespace, Name: ar.Name})
		}
		return nil, err
	}
	return ar, nil
}

func validateApprovalRegister(req *ApprovalRegisterRequest) error {
	if strings.TrimSpace(req.Session.Namespace) == "" || strings.TrimSpace(req.Session.Name) == "" {
		return fmt.Errorf("%w: session namespace and name are required", ErrBadRequest)
	}
	if strings.TrimSpace(req.RequestID) == "" {
		return fmt.Errorf("%w: requestId is required", ErrBadRequest)
	}
	if strings.TrimSpace(req.Action) == "" {
		return fmt.Errorf("%w: action is required", ErrBadRequest)
	}
	if req.Window != "" {
		if _, err := time.ParseDuration(req.Window); err != nil {
			return fmt.Errorf("%w: invalid window duration", ErrBadRequest)
		}
	}
	return nil
}

func approvalWindowFromString(s string) *metav1.Duration {
	if s == "" {
		return nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return nil
	}
	return &metav1.Duration{Duration: d}
}

// RuntimeApprovalName is the deterministic, idempotent ApprovalRequest name for a
// held tool call, derived from the session name and the gateway's requestId.
func RuntimeApprovalName(sessionName, requestID string) string {
	sum := sha256.Sum256([]byte(requestID))
	suffix := hex.EncodeToString(sum[:])[:12]
	base := sessionName
	// Keep the whole name within the DNS-1123 253-char object-name limit, leaving
	// room for the "-rt-" + 12-char digest suffix.
	if maxLen := 253 - len("-rt-") - len(suffix); len(base) > maxLen {
		base = base[:maxLen]
	}
	return fmt.Sprintf("%s-rt-%s", base, suffix)
}

func setApprovalBlockOwnerDeletion(obj metav1.Object, block bool) {
	refs := obj.GetOwnerReferences()
	for i := range refs {
		refs[i].BlockOwnerDeletion = &block
	}
	obj.SetOwnerReferences(refs)
}

func writeApprovalResponse(w http.ResponseWriter, ar *scrutineerv1alpha1.ApprovalRequest) {
	state := string(ar.Status.State)
	if state == "" {
		state = string(scrutineerv1alpha1.ApprovalStatePending)
	}
	resp := ApprovalResponse{
		ApprovalID: ar.Name,
		State:      state,
		Reason:     ar.Status.Reason,
	}
	if ar.Status.ExpiresAt != nil {
		resp.ExpiresAt = ar.Status.ExpiresAt.UTC().Format(time.RFC3339)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// writeAuthError maps verifier errors to HTTP status codes (mirrors the report handler).
func writeAuthError(w http.ResponseWriter, err error) {
	status := http.StatusUnauthorized
	switch {
	case errors.Is(err, ErrForbidden):
		status = http.StatusForbidden
	case errors.Is(err, ErrUnauthorized):
		status = http.StatusUnauthorized
	case errors.Is(err, ErrVerifyThrottled):
		// Global verification budget spent (#104): transient for the caller.
		w.Header().Set("Retry-After", "1")
		status = http.StatusServiceUnavailable
	default:
		status = http.StatusInternalServerError
	}
	writeError(w, err, status, err.Error())
}
