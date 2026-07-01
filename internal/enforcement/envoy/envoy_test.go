/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package envoy

import (
	"strings"
	"testing"
)

func TestServiceName(t *testing.T) {
	if got := ServiceName("sess-a"); got != "sess-a-egress" {
		t.Fatalf("ServiceName = %q, want sess-a-egress", got)
	}

	long := strings.Repeat("x", 80)
	got := ServiceName(long)
	if len(got) > maxServiceNameLen {
		t.Fatalf("ServiceName length = %d, want <= %d", len(got), maxServiceNameLen)
	}
	if !strings.HasSuffix(got, serviceSuffix) {
		t.Fatalf("truncated ServiceName %q lost the %q suffix", got, serviceSuffix)
	}
}

func TestProxyURL(t *testing.T) {
	got := ProxyURL("sess-a", "ns1")
	want := "http://sess-a-egress.ns1.svc:15001"
	if got != want {
		t.Fatalf("ProxyURL = %q, want %q", got, want)
	}
}

func TestExplicitProxyEnv(t *testing.T) {
	url := ProxyURL("sess-a", "ns1")
	env := ExplicitProxyEnv(url)

	byName := map[string]string{}
	for _, e := range env {
		byName[e.Name] = e.Value
	}

	// Both upper- and lower-case proxy vars must point at the Envoy proxy.
	for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
		if byName[k] != url {
			t.Fatalf("%s = %q, want %q", k, byName[k], url)
		}
	}
	// NO_PROXY keeps loopback direct, in both cases.
	for _, k := range []string{"NO_PROXY", "no_proxy"} {
		if !strings.Contains(byName[k], "127.0.0.1") {
			t.Fatalf("%s = %q, want it to keep loopback direct", k, byName[k])
		}
	}
}
