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

	"github.com/grantbarry29/scrutineer/internal/enforcement"
	"github.com/grantbarry29/scrutineer/internal/enforcement/reporterclient"
)

const approvalsPath = "/v1/approvals"

// reportRequest is the shared POST /v1/report wire body; aliased so package tests can
// decode against it. The canonical type lives in the reporterclient package.
type reportRequest = reporterclient.ReportRequest

// approvalRegisterRequest mirrors the reporter's POST /v1/approvals body. It
// carries only a redacted argDigest — never raw argument values.
type approvalRegisterRequest struct {
	Session   reporterclient.SessionRef `json:"session"`
	RequestID string                    `json:"requestId"`
	Action    string                    `json:"action"`
	Target    string                    `json:"target,omitempty"`
	ArgDigest string                    `json:"argDigest,omitempty"`
	PolicyRef string                    `json:"policyRef,omitempty"`
	Window    string                    `json:"window,omitempty"`
}

// approvalResponse mirrors the reporter's approval-channel response.
type approvalResponse struct {
	ApprovalID string `json:"approvalId"`
	State      string `json:"state"`
	ExpiresAt  string `json:"expiresAt,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

// ReporterClient posts tool-gateway runtime evidence to the controller-owned reporter
// and, unlike the other sidecars, also drives the mid-execution approval channel. It
// composes the shared reporterclient.Client for transport + auth.
type ReporterClient struct {
	*reporterclient.Client
}

// NewReporterClient returns a client for POST /v1/report tagged as the tool gateway.
func NewReporterClient(baseURL, tokenPath string, httpClient *http.Client) *ReporterClient {
	return &ReporterClient{reporterclient.New(baseURL, tokenPath, enforcement.BackendToolGateway, httpClient)}
}

// Submit sends a runtime report for the configured session.
func (c *ReporterClient) Submit(ctx context.Context, env RuntimeEnv, report enforcement.RuntimeReport) error {
	if c == nil {
		return fmt.Errorf("reporter client is nil")
	}
	return c.Client.Submit(ctx, reporterclient.SessionRef{
		Namespace: env.SessionNamespace,
		Name:      env.SessionName,
	}, report)
}

// RegisterApproval registers (idempotently) a mid-execution hold for a tool call
// and returns the current approval state. The endpoint dedupes by requestId.
func (c *ReporterClient) RegisterApproval(ctx context.Context, env RuntimeEnv, reg approvalRegisterRequest) (approvalResponse, error) {
	if c == nil {
		return approvalResponse{}, fmt.Errorf("reporter client is nil")
	}
	reg.Session = reporterclient.SessionRef{Namespace: env.SessionNamespace, Name: env.SessionName}
	body, err := json.Marshal(reg)
	if err != nil {
		return approvalResponse{}, fmt.Errorf("marshal approval: %w", err)
	}
	req, err := c.NewRequest(ctx, http.MethodPost, c.BaseURL+approvalsPath, bytes.NewReader(body))
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
	req, err := c.NewRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return approvalResponse{}, err
	}
	return c.doApproval(req)
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
