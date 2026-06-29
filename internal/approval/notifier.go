/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package approval contains pluggable notification adapters used to alert
// approvers when an AgentSession opens a human-approval gate. Notification is a
// best-effort side channel: failures never block the controller's approval gate,
// which is enforced entirely through ApprovalRequest state.
package approval

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Notification is the payload describing an open approval gate. It is provider
// neutral; concrete adapters (webhook, future Slack/PagerDuty) shape it for their
// transport.
type Notification struct {
	// Namespace/Name identify the ApprovalRequest object.
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	// Session is the AgentSession awaiting approval.
	Session string `json:"session"`
	// Action is the gated action type (e.g. "deploy").
	Action string `json:"action"`
	// PolicyRef is the ApprovalPolicy that opened the gate, if any.
	PolicyRef string `json:"policyRef,omitempty"`
	// Target is the bounded scope of the request (e.g. "cluster/prod"), if any.
	Target string `json:"target,omitempty"`
	// Message is a short human-readable summary.
	Message string `json:"message"`
}

// Notifier delivers an approval Notification to an external channel. Implementations
// must be safe for concurrent use and must honor the provided context deadline.
type Notifier interface {
	Notify(ctx context.Context, n Notification) error
}

// NoopNotifier is the default when no channel is configured; it drops notifications.
type NoopNotifier struct{}

// Notify implements Notifier and does nothing.
func (NoopNotifier) Notify(context.Context, Notification) error { return nil }

// defaultWebhookTimeout bounds a single webhook delivery attempt so a slow or
// unreachable endpoint cannot stall reconciliation.
const defaultWebhookTimeout = 5 * time.Second

// WebhookNotifier POSTs the Notification as JSON to a configured URL. Any non-2xx
// response (or transport error) is reported as an error so the caller can retry.
type WebhookNotifier struct {
	URL    string
	Client *http.Client
}

// NewWebhookNotifier builds a WebhookNotifier with a bounded-timeout HTTP client.
func NewWebhookNotifier(url string) *WebhookNotifier {
	return &WebhookNotifier{
		URL:    url,
		Client: &http.Client{Timeout: defaultWebhookTimeout},
	}
}

// Notify delivers the notification via HTTP POST. It is idempotent from the
// caller's perspective: callers gate re-delivery on ApprovalRequest state.
func (w *WebhookNotifier) Notify(ctx context.Context, n Notification) error {
	body, err := json.Marshal(n)
	if err != nil {
		return fmt.Errorf("marshal notification: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.URL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := w.Client
	if client == nil {
		client = &http.Client{Timeout: defaultWebhookTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("post approval webhook: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("approval webhook returned status %d", resp.StatusCode)
	}
	return nil
}
