/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package reporterclient is the shared data-plane transport enforcement components use
// to POST runtime evidence to the controller-owned reporter endpoint. Today its one
// caller is the egress-reporter beside Envoy in the per-session egress-proxy pod; each
// caller wraps a *Client parameterized by its backend kind. Keeping this on the
// data-plane side of the control-plane/data-plane split — see
// docs/design/architecture.md.
package reporterclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/enforcement"
)

const reportPath = "/v1/report"

// StatusError is a non-202 reporter response. It exposes the HTTP status so callers can
// classify per the contract's §4.4 response table (docs/design/
// phase-3-runtime-reporter-contract.md): permanent rejections (400/403/404/413) must not
// be retried verbatim, transient ones (5xx, 429, 401, 409) keep at-least-once retry (#96).
type StatusError struct {
	StatusCode int
	// RetryAfter is the server's Retry-After hint, zero when absent or unparseable.
	// On 429 it is flow control, not failure: callers pace and retry (§4.4, #100).
	RetryAfter time.Duration
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("reporter returned %d", e.StatusCode)
}

// SessionRef identifies the AgentSession a report or approval is scoped to.
type SessionRef struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

// ReportRequest is the wire body of POST /v1/report shared by every sidecar backend.
type ReportRequest struct {
	Session    SessionRef                           `json:"session"`
	Backend    string                               `json:"backend"`
	Decisions  []scrutineerv1alpha1.PolicyDecision  `json:"decisions"`
	Violations []scrutineerv1alpha1.PolicyViolation `json:"violations,omitempty"`
	Events     []scrutineerv1alpha1.SessionEvent    `json:"events,omitempty"`
}

// Client posts runtime evidence to the controller-owned reporter endpoint on behalf of
// a single sidecar backend.
type Client struct {
	BaseURL    string
	TokenPath  string
	Backend    string
	HTTPClient *http.Client
}

// New returns a Client for POST /v1/report tagged with the given backend.
func New(baseURL, tokenPath string, backend enforcement.BackendKind, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &Client{
		BaseURL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		TokenPath:  strings.TrimSpace(tokenPath),
		Backend:    string(backend),
		HTTPClient: httpClient,
	}
}

// Submit sends a runtime report for the given session.
func (c *Client) Submit(ctx context.Context, session SessionRef, report enforcement.RuntimeReport) error {
	if c == nil {
		return fmt.Errorf("reporter client is nil")
	}
	body, err := json.Marshal(ReportRequest{
		Session:    session,
		Backend:    c.Backend,
		Decisions:  report.Decisions,
		Violations: report.Violations,
		Events:     report.Events,
	})
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}

	req, err := c.NewRequest(ctx, http.MethodPost, c.BaseURL+reportPath, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("post report: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusAccepted {
		return &StatusError{StatusCode: resp.StatusCode, RetryAfter: retryAfterHint(resp)}
	}
	return nil
}

// retryAfterHint parses the response's Retry-After header — integer seconds or the
// RFC 7231 HTTP-date form — returning zero when absent, unparseable, or in the past.
func retryAfterHint(resp *http.Response) time.Duration {
	v := strings.TrimSpace(resp.Header.Get("Retry-After"))
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs < 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if at, err := http.ParseTime(v); err == nil {
		if d := time.Until(at); d > 0 {
			return d
		}
	}
	return 0
}

// NewRequest builds a request authenticated with the projected reporter token. Exported
// so composed clients (e.g. a future approval channel at an out-of-pod chokepoint)
// reuse the same transport and auth without re-reading the token themselves.
func (c *Client) NewRequest(ctx context.Context, method, url string, body io.Reader) (*http.Request, error) {
	if c == nil {
		return nil, fmt.Errorf("reporter client is nil")
	}
	token, err := os.ReadFile(c.TokenPath)
	if err != nil {
		return nil, fmt.Errorf("read reporter token: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	return req, nil
}
