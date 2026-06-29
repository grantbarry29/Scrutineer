/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package toolgateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/enforcement"
)

const (
	reportPath    = "/v1/report"
	approvalsPath = "/v1/approvals"
)

type sessionRef struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

// approvalRegisterRequest mirrors the reporter's POST /v1/approvals body. It
// carries only a redacted argDigest — never raw argument values.
type approvalRegisterRequest struct {
	Session   sessionRef `json:"session"`
	RequestID string     `json:"requestId"`
	Action    string     `json:"action"`
	Target    string     `json:"target,omitempty"`
	ArgDigest string     `json:"argDigest,omitempty"`
	PolicyRef string     `json:"policyRef,omitempty"`
	Window    string     `json:"window,omitempty"`
}

// approvalResponse mirrors the reporter's approval-channel response.
type approvalResponse struct {
	ApprovalID string `json:"approvalId"`
	State      string `json:"state"`
	ExpiresAt  string `json:"expiresAt,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

type reportRequest struct {
	Session    sessionRef                           `json:"session"`
	Backend    string                               `json:"backend"`
	Decisions  []scrutineerv1alpha1.PolicyDecision  `json:"decisions"`
	Violations []scrutineerv1alpha1.PolicyViolation `json:"violations,omitempty"`
	Events     []scrutineerv1alpha1.SessionEvent    `json:"events,omitempty"`
}

// ReporterClient posts runtime evidence to the controller-owned reporter endpoint.
type ReporterClient struct {
	BaseURL    string
	TokenPath  string
	HTTPClient *http.Client
}

// NewReporterClient returns a client for POST /v1/report.
func NewReporterClient(baseURL, tokenPath string, httpClient *http.Client) *ReporterClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &ReporterClient{
		BaseURL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		TokenPath:  strings.TrimSpace(tokenPath),
		HTTPClient: httpClient,
	}
}

// Submit sends a runtime report for the configured session.
func (c *ReporterClient) Submit(ctx context.Context, env RuntimeEnv, report enforcement.RuntimeReport) error {
	if c == nil {
		return fmt.Errorf("reporter client is nil")
	}
	body, err := json.Marshal(reportRequest{
		Session: sessionRef{
			Namespace: env.SessionNamespace,
			Name:      env.SessionName,
		},
		Backend:    string(enforcement.BackendToolGateway),
		Decisions:  report.Decisions,
		Violations: report.Violations,
		Events:     report.Events,
	})
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}

	token, err := os.ReadFile(c.TokenPath)
	if err != nil {
		return fmt.Errorf("read reporter token: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+reportPath, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("post report: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("reporter returned %d", resp.StatusCode)
	}
	return nil
}

// RegisterApproval registers (idempotently) a mid-execution hold for a tool call
// and returns the current approval state. The endpoint dedupes by requestId.
func (c *ReporterClient) RegisterApproval(ctx context.Context, env RuntimeEnv, reg approvalRegisterRequest) (approvalResponse, error) {
	if c == nil {
		return approvalResponse{}, fmt.Errorf("reporter client is nil")
	}
	reg.Session = sessionRef{Namespace: env.SessionNamespace, Name: env.SessionName}
	body, err := json.Marshal(reg)
	if err != nil {
		return approvalResponse{}, fmt.Errorf("marshal approval: %w", err)
	}
	req, err := c.newRequest(ctx, http.MethodPost, c.BaseURL+approvalsPath, bytes.NewReader(body))
	if err != nil {
		return approvalResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	return c.doApproval(req)
}

// GetApproval polls the current state of a hold by approval id.
func (c *ReporterClient) GetApproval(ctx context.Context, env RuntimeEnv, approvalID string) (approvalResponse, error) {
	if c == nil {
		return approvalResponse{}, fmt.Errorf("reporter client is nil")
	}
	url := fmt.Sprintf("%s%s/%s?namespace=%s", c.BaseURL, approvalsPath, approvalID, env.SessionNamespace)
	req, err := c.newRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return approvalResponse{}, err
	}
	return c.doApproval(req)
}

// newRequest builds an authenticated request using the projected reporter token.
func (c *ReporterClient) newRequest(ctx context.Context, method, url string, body io.Reader) (*http.Request, error) {
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

// doApproval executes an approval-channel request and decodes the JSON response.
func (c *ReporterClient) doApproval(req *http.Request) (approvalResponse, error) {
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return approvalResponse{}, fmt.Errorf("approval request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return approvalResponse{}, fmt.Errorf("approval endpoint returned %d", resp.StatusCode)
	}
	var out approvalResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return approvalResponse{}, fmt.Errorf("decode approval response: %w", err)
	}
	return out, nil
}
