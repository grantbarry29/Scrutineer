/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package workspace

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
)

const accessPath = "/v1/files/access"

// Gateway is a minimal HTTP file governance endpoint for in-pod agents.
type Gateway struct {
	Env      RuntimeEnv
	Reporter *ReporterClient
	Now      func() time.Time
}

type accessRequest struct {
	Path      string `json:"path"`
	Operation string `json:"operation,omitempty"`
}

type accessResponse struct {
	Status string `json:"status"`
	Path   string `json:"path"`
}

// ServeHTTP implements http.Handler.
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && r.URL.Path == accessPath:
		g.handleAccess(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (g *Gateway) handleAccess(w http.ResponseWriter, r *http.Request) {
	var req accessRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	fileReq := FileRequest{
		Path:      strings.TrimSpace(req.Path),
		Operation: strings.TrimSpace(req.Operation),
	}

	ctx := g.Env.SessionContext()
	auth := EvaluateFile(ctx, fileReq)

	if shouldReport(auth) {
		report := RuntimeReport(ctx, fileReq, auth, g.now())
		if g.Reporter != nil {
			_ = g.Reporter.Submit(r.Context(), g.Env, report)
		}
	}

	if auth.Blocked {
		target := normalizePath(fileReq.Path)
		if target == "" {
			target = "unknown"
		}
		http.Error(w, fmt.Sprintf("path %q denied by policy (%s)", target, auth.Reason), http.StatusForbidden)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(accessResponse{
		Status: "ok",
		Path:   normalizePath(fileReq.Path),
	})
}

func (g *Gateway) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now()
}

func shouldReport(auth FileAuthorization) bool {
	return auth.Reason != ReasonAllowed || auth.Action != scrutineerv1alpha1.PolicyDecisionAllow
}
