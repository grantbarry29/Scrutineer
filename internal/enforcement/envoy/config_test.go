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

// Structural checks on the generated bootstrap. Semantic validity is verified out-of-band
// with `envoy --mode validate` (Envoy 1.31) and, end to end, by the Slice A e2e (#60).
func TestBootstrapYAML(t *testing.T) {
	cfg := BootstrapYAML(ProxyPort)

	must := []string{
		"port_value: 15001",                        // listens on the proxy port
		"connect_matcher",                          // terminates HTTPS CONNECT
		"upgrade_type: CONNECT",                    // CONNECT upgrade enabled
		"envoy.filters.http.dynamic_forward_proxy", // resolves + forwards by name
		"envoy.clusters.dynamic_forward_proxy",     // dynamic forward proxy cluster
		"envoy.filters.http.router",                // router terminal filter
		"address: 127.0.0.1",                       // admin bound to loopback only
	}
	for _, s := range must {
		if !strings.Contains(cfg, s) {
			t.Fatalf("BootstrapYAML missing %q", s)
		}
	}

	// The listen port is parameterized.
	if !strings.Contains(BootstrapYAML(19000), "port_value: 19000") {
		t.Fatalf("BootstrapYAML did not honor the port argument")
	}
}
