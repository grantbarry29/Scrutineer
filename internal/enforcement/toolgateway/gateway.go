/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package toolgateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/enforcement"
)

const invokePath = "/v1/tools/invoke"

// Default mid-execution approval hold tuning. A bounded long-poll keeps the
// common case a single synchronous call while capping held connections; on
// timeout the gateway returns 202 so a cooperating agent re-invokes (idempotent
// by requestId).
const (
	defaultApprovalHoldTimeout  = 25 * time.Second
	defaultApprovalPollInterval = 1 * time.Second
)

// Gateway is a minimal HTTP tool governance endpoint for in-pod agents.
type Gateway struct {
	Env      RuntimeEnv
	Reporter *ReporterClient
	Now      func() time.Time
	// ApprovalHoldTimeout bounds how long a single invoke holds for a human decision
	// before returning 202 (re-invoke to keep waiting). Zero uses the default.
	ApprovalHoldTimeout time.Duration
	// ApprovalPollInterval is how often the gateway re-polls the approval state while
	// holding. Zero uses the default.
	ApprovalPollInterval time.Duration
}

type invokeRequest struct {
	Tool      string         `json:"tool"`
	Server    string         `json:"server,omitempty"`
	Method    string         `json:"method,omitempty"`
	RequestID string         `json:"requestId,omitempty"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

type invokeResponse struct {
	Status string `json:"status"`
	Tool   string `json:"tool"`
	// ApprovalID is set on a 202 hold so the agent can poll/re-invoke.
	ApprovalID string `json:"approvalId,omitempty"`
}

// ServeHTTP implements http.Handler.
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == invokePath:
		g.handleInvoke(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (g *Gateway) handleInvoke(w http.ResponseWriter, r *http.Request) {
	var req invokeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	toolReq := ToolRequest{
		Tool:      strings.TrimSpace(req.Tool),
		Server:    strings.TrimSpace(req.Server),
		Method:    strings.TrimSpace(req.Method),
		RequestID: strings.TrimSpace(req.RequestID),
		Arguments: req.Arguments,
	}

	ctx := g.Env.SessionContext()
	auth := EvaluateTool(ctx, toolReq)

	// Mid-execution human gate: a blocked approval-required outcome (enforced mode)
	// means we must hold this specific call for a scoped human decision.
	if auth.Blocked && auth.Reason == ReasonApprovalRequired {
		g.handleApprovalHold(w, r, ctx, toolReq)
		return
	}

	if shouldReport(auth) {
		report := RuntimeReport(ctx, toolReq, auth, g.now())
		if g.Reporter != nil {
			_ = g.Reporter.Submit(r.Context(), g.Env, report)
		}
	}

	if auth.Blocked {
		http.Error(w, fmt.Sprintf("tool %q denied by policy (%s)", toolReq.Tool, auth.Reason), http.StatusForbidden)
		return
	}

	writeInvokeOK(w, toolReq.Tool)
}

// handleApprovalHold registers a mid-execution hold with the controller (via the
// reporter approval channel) and bounded-long-polls for the human decision:
// granted -> allow (200), denied/expired -> deny (403), still pending at the hold
// deadline -> 202 so the agent re-invokes the same call (idempotent by requestId).
// Fails closed (403) when no approval channel is configured.
func (g *Gateway) handleApprovalHold(w http.ResponseWriter, r *http.Request, ctx enforcement.SessionContext, toolReq ToolRequest) {
	if g.Reporter == nil {
		http.Error(w, fmt.Sprintf("tool %q requires human approval but no approval channel is configured", toolReq.Tool), http.StatusForbidden)
		return
	}

	digest := argumentsDigest(toolReq.Arguments)
	requestID := toolReq.RequestID
	if requestID == "" {
		requestID = deriveApprovalRequestID(toolReq, digest)
	}

	reg := approvalRegisterRequest{
		RequestID: requestID,
		Action:    toolReq.Tool,
		Target:    approvalTarget(toolReq),
		ArgDigest: digest,
	}
	resp, err := g.Reporter.RegisterApproval(r.Context(), g.Env, reg)
	if err != nil {
		// Cannot reach the gate: fail closed under enforced mode.
		http.Error(w, fmt.Sprintf("tool %q approval registration failed: %v", toolReq.Tool, err), http.StatusServiceUnavailable)
		return
	}

	deadline := time.Now().Add(g.holdTimeout())
	state := resp.State
	for {
		switch state {
		case string(scrutineerv1alpha1.ApprovalStateGranted):
			g.reportApprovalResolved(r.Context(), ctx, toolReq, digest, true)
			writeInvokeOK(w, toolReq.Tool)
			return
		case string(scrutineerv1alpha1.ApprovalStateDenied), string(scrutineerv1alpha1.ApprovalStateExpired):
			g.reportApprovalResolved(r.Context(), ctx, toolReq, digest, false)
			http.Error(w, fmt.Sprintf("tool %q denied by human approval (%s)", toolReq.Tool, state), http.StatusForbidden)
			return
		}
		if !time.Now().Before(deadline) {
			break
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(g.pollInterval()):
		}
		if polled, pErr := g.Reporter.GetApproval(r.Context(), g.Env, resp.ApprovalID); pErr == nil {
			state = polled.State
		}
	}

	// Undecided within the hold window: tell the agent to re-invoke to keep waiting.
	w.Header().Set("Retry-After", retryAfterSeconds(g.pollInterval()))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(invokeResponse{Status: "pending", Tool: toolReq.Tool, ApprovalID: resp.ApprovalID})
}

// reportApprovalResolved submits self-reported runtime evidence for a resolved hold.
func (g *Gateway) reportApprovalResolved(ctx context.Context, sctx enforcement.SessionContext, toolReq ToolRequest, digest string, granted bool) {
	if g.Reporter == nil {
		return
	}
	_ = g.Reporter.Submit(ctx, g.Env, ApprovalResolvedReport(sctx, toolReq, digest, granted, g.now()))
}

func (g *Gateway) holdTimeout() time.Duration {
	if g.ApprovalHoldTimeout > 0 {
		return g.ApprovalHoldTimeout
	}
	return defaultApprovalHoldTimeout
}

func (g *Gateway) pollInterval() time.Duration {
	if g.ApprovalPollInterval > 0 {
		return g.ApprovalPollInterval
	}
	return defaultApprovalPollInterval
}

func writeInvokeOK(w http.ResponseWriter, tool string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(invokeResponse{Status: "ok", Tool: tool})
}

// argumentsDigest returns a redacted, deterministic fingerprint of the tool-call
// arguments (sha256 over canonical JSON; encoding/json sorts map keys). It never
// exposes raw values — only the digest crosses the control-plane boundary.
func argumentsDigest(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// deriveApprovalRequestID produces a stable idempotency key for a tool call when
// the agent did not supply one, so identical re-invocations map to one hold.
func deriveApprovalRequestID(req ToolRequest, digest string) string {
	seed := req.Tool + "|" + req.Server + "|" + digest
	sum := sha256.Sum256([]byte(seed))
	return "tg-" + hex.EncodeToString(sum[:])[:16]
}

// approvalTarget is the human-readable subject of a hold: tool, or tool@server.
func approvalTarget(req ToolRequest) string {
	if req.Server != "" {
		return req.Tool + "@" + req.Server
	}
	return req.Tool
}

func retryAfterSeconds(d time.Duration) string {
	secs := int(d.Seconds())
	if secs < 1 {
		secs = 1
	}
	return fmt.Sprintf("%d", secs)
}

func (g *Gateway) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now()
}

func shouldReport(auth ToolAuthorization) bool {
	return auth.Reason != ReasonAllowed || auth.Action != scrutineerv1alpha1.PolicyDecisionAllow
}
