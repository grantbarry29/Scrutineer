/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package sidecarenv

import (
	"reflect"
	"testing"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
)

func TestLoadBase_defaultsAndValidates(t *testing.T) {
	t.Run("loads and defaults empty mode to audit-only", func(t *testing.T) {
		t.Setenv(EnvSessionNamespace, "ns")
		t.Setenv(EnvSessionName, "sess")
		t.Setenv(EnvReporterURL, "http://reporter")
		t.Setenv(EnvReporterToken, "/var/run/token")

		b, err := LoadBase("")
		if err != nil {
			t.Fatal(err)
		}
		if b.SessionNamespace != "ns" || b.SessionName != "sess" {
			t.Fatalf("session = %+v", b)
		}
		if b.Mode != scrutineerv1alpha1.PolicyModeAuditOnly {
			t.Fatalf("mode = %q, want default audit-only", b.Mode)
		}
	})

	t.Run("keeps explicit mode", func(t *testing.T) {
		t.Setenv(EnvSessionNamespace, "ns")
		t.Setenv(EnvSessionName, "sess")
		t.Setenv(EnvReporterURL, "http://reporter")
		t.Setenv(EnvReporterToken, "/t")
		b, err := LoadBase(string(scrutineerv1alpha1.PolicyModeEnforced))
		if err != nil {
			t.Fatal(err)
		}
		if b.Mode != scrutineerv1alpha1.PolicyModeEnforced {
			t.Fatalf("mode = %q", b.Mode)
		}
	})

	t.Run("errors when session missing", func(t *testing.T) {
		t.Setenv(EnvSessionNamespace, "")
		t.Setenv(EnvSessionName, "")
		t.Setenv(EnvReporterURL, "http://reporter")
		t.Setenv(EnvReporterToken, "/t")
		if _, err := LoadBase(""); err == nil {
			t.Fatal("expected error for missing session")
		}
	})

	t.Run("errors when reporter missing", func(t *testing.T) {
		t.Setenv(EnvSessionNamespace, "ns")
		t.Setenv(EnvSessionName, "sess")
		t.Setenv(EnvReporterURL, "")
		t.Setenv(EnvReporterToken, "")
		if _, err := LoadBase(""); err == nil {
			t.Fatal("expected error for missing reporter config")
		}
	})
}

func TestSplitCSV(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"   ", nil},
		{"a", []string{"a"}},
		{" a , b ,, c ", []string{"a", "b", "c"}},
	}
	for _, tc := range cases {
		if got := SplitCSV(tc.in); !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("SplitCSV(%q) = %#v, want %#v", tc.in, got, tc.want)
		}
	}
}
