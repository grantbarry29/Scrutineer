/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package toolgateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
)

const invokePath = "/v1/tools/invoke"

// Gateway is a minimal HTTP tool governance endpoint for in-pod agents.
type Gateway struct {
	Env      RuntimeEnv
	Reporter *ReporterClient
	Now      func() time.Time
}

type invokeRequest struct {
	Tool      string `json:"tool"`
	Server    string `json:"server,omitempty"`
	Method    string `json:"method,omitempty"`
	RequestID string `json:"requestId,omitempty"`
}

type invokeResponse struct {
	Status string `json:"status"`
	Tool   string `json:"tool"`
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
	}

	ctx := g.Env.SessionContext()
	auth := EvaluateTool(ctx, toolReq)

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

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(invokeResponse{Status: "ok", Tool: toolReq.Tool})
}

func (g *Gateway) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now()
}

func shouldReport(auth ToolAuthorization) bool {
	return auth.Reason != ReasonAllowed || auth.Action != relayv1alpha1.PolicyDecisionAllow
}
