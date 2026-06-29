/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package dnsproxy

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

const reportPath = "/v1/report"

type sessionRef struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
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
		Backend:    string(enforcement.BackendEgressProxy),
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
