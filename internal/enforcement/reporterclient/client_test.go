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
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

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

	c := New(srv.URL, writeTempToken(t, "test-token"), enforcement.BackendEgressProxy, srv.Client())
	err := c.Submit(context.Background(), SessionRef{Namespace: "ns", Name: "s"}, enforcement.RuntimeReport{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Backend != string(enforcement.BackendEgressProxy) {
		t.Fatalf("backend = %q, want %q", got.Backend, enforcement.BackendEgressProxy)
	}
	if got.Session.Namespace != "ns" || got.Session.Name != "s" {
		t.Fatalf("session = %+v", got.Session)
	}
}

// Non-202 responses surface as a typed StatusError so callers can classify permanent
// rejections (413/404/…) vs transient failures per the reporter contract §4.4 (#96).
func TestSubmit_nonAcceptedStatusErrors(t *testing.T) {
	for _, status := range []int{http.StatusInternalServerError, http.StatusRequestEntityTooLarge, http.StatusNotFound} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
		}))

		c := New(srv.URL, writeTempToken(t, "x"), enforcement.BackendEgressProxy, srv.Client())
		err := c.Submit(context.Background(), SessionRef{Namespace: "ns", Name: "s"}, enforcement.RuntimeReport{})
		if err == nil {
			t.Fatalf("expected error for %d response", status)
		}
		var se *StatusError
		if !errors.As(err, &se) || se.StatusCode != status {
			t.Fatalf("err = %v, want StatusError with code %d", err, status)
		}
		srv.Close()
	}
}

// 429 responses carry the server's Retry-After hint (contract §4.4: "back off using
// Retry-After") so the tailer can pace batch submission at the reporter's rate instead
// of treating flow control as a failure (#100).
func TestSubmit_exposesRetryAfterHint(t *testing.T) {
	cases := []struct {
		name   string
		header string
		want   time.Duration
	}{
		{"integer seconds", "2", 2 * time.Second},
		{"absent", "", 0},
		{"unparseable", "soon", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tc.header != "" {
					w.Header().Set("Retry-After", tc.header)
				}
				w.WriteHeader(http.StatusTooManyRequests)
			}))
			defer srv.Close()

			c := New(srv.URL, writeTempToken(t, "x"), enforcement.BackendEgressProxy, srv.Client())
			err := c.Submit(context.Background(), SessionRef{Namespace: "ns", Name: "s"}, enforcement.RuntimeReport{})
			var se *StatusError
			if !errors.As(err, &se) || se.StatusCode != http.StatusTooManyRequests {
				t.Fatalf("err = %v, want StatusError with code 429", err)
			}
			if se.RetryAfter != tc.want {
				t.Fatalf("RetryAfter = %v, want %v", se.RetryAfter, tc.want)
			}
		})
	}

	// The RFC 7231 HTTP-date form is honored too (a proxy may rewrite the header).
	t.Run("http date", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Retry-After", time.Now().Add(5*time.Second).UTC().Format(http.TimeFormat))
			w.WriteHeader(http.StatusTooManyRequests)
		}))
		defer srv.Close()

		c := New(srv.URL, writeTempToken(t, "x"), enforcement.BackendEgressProxy, srv.Client())
		err := c.Submit(context.Background(), SessionRef{Namespace: "ns", Name: "s"}, enforcement.RuntimeReport{})
		var se *StatusError
		if !errors.As(err, &se) {
			t.Fatalf("err = %v, want StatusError", err)
		}
		if se.RetryAfter <= 0 || se.RetryAfter > 5*time.Second {
			t.Fatalf("RetryAfter = %v, want within (0s, 5s]", se.RetryAfter)
		}
	})
}

func TestNew_defaultsHTTPClientAndTrims(t *testing.T) {
	c := New("  http://example/  ", writeTempToken(t, "x"), enforcement.BackendEgressProxy, nil)
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
