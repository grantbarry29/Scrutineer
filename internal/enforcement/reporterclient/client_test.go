/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package reporterclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/grantbarry29/scrutineer/internal/enforcement"
)

func writeTempToken(t *testing.T, tok string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(p, []byte(tok), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSubmit_tagsBackendAndAuthenticates(t *testing.T) {
	var got ReportRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/report" {
			http.NotFound(w, r)
			return
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-token" {
			t.Fatalf("auth = %q", auth)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Fatalf("content-type = %q", ct)
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	c := New(srv.URL, writeTempToken(t, "test-token"), enforcement.BackendToolGateway, srv.Client())
	err := c.Submit(context.Background(), SessionRef{Namespace: "ns", Name: "s"}, enforcement.RuntimeReport{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Backend != string(enforcement.BackendToolGateway) {
		t.Fatalf("backend = %q, want %q", got.Backend, enforcement.BackendToolGateway)
	}
	if got.Session.Namespace != "ns" || got.Session.Name != "s" {
		t.Fatalf("session = %+v", got.Session)
	}
}

func TestSubmit_nonAcceptedStatusErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := New(srv.URL, writeTempToken(t, "x"), enforcement.BackendEgressProxy, srv.Client())
	if err := c.Submit(context.Background(), SessionRef{Namespace: "ns", Name: "s"}, enforcement.RuntimeReport{}); err == nil {
		t.Fatal("expected error for non-202 response")
	}
}

func TestNew_defaultsHTTPClientAndTrims(t *testing.T) {
	c := New("  http://example/  ", writeTempToken(t, "x"), enforcement.BackendFSGateway, nil)
	if c.HTTPClient == nil {
		t.Fatal("expected default http client")
	}
	if c.BaseURL != "http://example" {
		t.Fatalf("BaseURL = %q, want trimmed http://example", c.BaseURL)
	}
}

func TestNewRequest_missingTokenErrors(t *testing.T) {
	c := New("http://example", filepath.Join(t.TempDir(), "does-not-exist"), enforcement.BackendEgressProxy, http.DefaultClient)
	if _, err := c.NewRequest(context.Background(), http.MethodGet, "http://example/x", nil); err == nil {
		t.Fatal("expected error when token file is missing")
	}
}
