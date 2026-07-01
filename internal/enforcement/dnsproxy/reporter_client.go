/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package dnsproxy

import (
	"context"
	"fmt"
	"net/http"

	"github.com/grantbarry29/scrutineer/internal/enforcement"
	"github.com/grantbarry29/scrutineer/internal/enforcement/reporterclient"
)

// reportRequest is the shared POST /v1/report wire body; aliased so package tests can
// decode against it. The canonical type lives in the reporterclient package.
type reportRequest = reporterclient.ReportRequest

// ReporterClient posts egress-proxy runtime evidence to the controller-owned reporter.
type ReporterClient struct {
	*reporterclient.Client
}

// NewReporterClient returns a client for POST /v1/report tagged as the egress proxy.
func NewReporterClient(baseURL, tokenPath string, httpClient *http.Client) *ReporterClient {
	return &ReporterClient{reporterclient.New(baseURL, tokenPath, enforcement.BackendEgressProxy, httpClient)}
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
