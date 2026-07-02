/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package enforcement

import "testing"

func TestMatchDomain(t *testing.T) {
	cases := []struct {
		name     string
		patterns []string
		host     string
		want     bool
	}{
		{"exact match", []string{"example.com"}, "example.com", true},
		{"exact case-insensitive", []string{"Example.COM"}, "example.com", true},
		{"exact host case-insensitive", []string{"example.com"}, "EXAMPLE.com", true},
		{"exact does not cover subdomain", []string{"example.com"}, "api.example.com", false},
		{"exact ignores port", []string{"example.com"}, "example.com:443", true},
		{"no match", []string{"example.com"}, "evil.com", false},
		{"empty patterns", nil, "example.com", false},
		{"empty host", []string{"example.com"}, "", false},

		{"wildcard covers one label", []string{"*.example.com"}, "api.example.com", true},
		{"wildcard covers nested labels", []string{"*.example.com"}, "a.b.example.com", true},
		{"wildcard excludes apex", []string{"*.example.com"}, "example.com", false},
		{"wildcard ignores port", []string{"*.example.com"}, "api.example.com:8443", true},
		{"wildcard case-insensitive", []string{"*.Example.com"}, "API.example.com", true},
		{"wildcard not a suffix-substring", []string{"*.example.com"}, "notexample.com", false},
		{"wildcard rejects evil suffix trick", []string{"*.example.com"}, "example.com.evil.com", false},

		{"mixed list matches exact", []string{"*.example.com", "foo.test"}, "foo.test", true},
		{"mixed list matches wildcard", []string{"foo.test", "*.example.com"}, "x.example.com", true},

		{"pattern whitespace trimmed", []string{"  example.com  "}, "example.com", true},
		{"blank pattern ignored", []string{"", "example.com"}, "example.com", true},
		{"trailing dot on host normalized", []string{"example.com"}, "example.com.", true},
	}
	for _, tc := range cases {
		if got := MatchDomain(tc.patterns, tc.host); got != tc.want {
			t.Errorf("%s: MatchDomain(%v, %q) = %v, want %v", tc.name, tc.patterns, tc.host, got, tc.want)
		}
	}
}
