/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package approval

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWebhookNotifier_postsJSONPayload(t *testing.T) {
	var got Notification
	var contentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentType = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := Notification{
		Namespace: "team-a", Name: "deploy-session", Session: "deploy-session",
		Action: "deploy", PolicyRef: "prod-deploys", Target: "cluster/prod",
		Message: "approval required",
	}
	if err := NewWebhookNotifier(srv.URL).Notify(context.Background(), n); err != nil {
		t.Fatalf("Notify returned error: %v", err)
	}
	if contentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", contentType)
	}
	if got != n {
		t.Errorf("delivered payload = %+v, want %+v", got, n)
	}
}

func TestWebhookNotifier_non2xxIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	if err := NewWebhookNotifier(srv.URL).Notify(context.Background(), Notification{}); err == nil {
		t.Fatal("expected error on non-2xx response, got nil")
	}
}

func TestWebhookNotifier_transportErrorIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // force a connection error.

	if err := NewWebhookNotifier(url).Notify(context.Background(), Notification{}); err == nil {
		t.Fatal("expected transport error, got nil")
	}
}

func TestNoopNotifier_neverErrors(t *testing.T) {
	if err := (NoopNotifier{}).Notify(context.Background(), Notification{}); err != nil {
		t.Fatalf("NoopNotifier returned error: %v", err)
	}
}
