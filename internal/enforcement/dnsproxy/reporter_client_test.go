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
	"net/http"
	"net/http/httptest"
	"testing"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/enforcement"
	"github.com/grantbarry29/scrutineer/internal/enforcement/sidecarenv"
)

func TestReporterClient_Submit_success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/report" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("auth = %q", got)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	client := NewReporterClient(srv.URL, writeTempToken(t, "test-token"), srv.Client())
	env := RuntimeEnv{Base: sidecarenv.Base{SessionNamespace: "ns", SessionName: "s"}}
	report := enforcement.RuntimeReport{
		Decisions: []scrutineerv1alpha1.PolicyDecision{{Type: "network", Action: scrutineerv1alpha1.PolicyDecisionDeny}},
	}
	if err := client.Submit(context.Background(), env, report); err != nil {
		t.Fatal(err)
	}
}

func TestReporterClient_Submit_badStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := NewReporterClient(srv.URL, writeTempToken(t, "x"), srv.Client())
	err := client.Submit(context.Background(), RuntimeEnv{Base: sidecarenv.Base{SessionNamespace: "ns", SessionName: "s"}}, enforcement.RuntimeReport{})
	if err == nil {
		t.Fatal("expected error for non-202 response")
	}
}

func TestNewReporterClient_defaultsHTTPClient(t *testing.T) {
	c := NewReporterClient("http://example", writeTempToken(t, "x"), nil)
	if c == nil || c.HTTPClient == nil {
		t.Fatal("expected default http client")
	}
}
