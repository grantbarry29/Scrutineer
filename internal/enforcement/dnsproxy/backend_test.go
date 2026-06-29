/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package dnsproxy

import (
	"testing"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/enforcement"
)

func TestBackend_metadata(t *testing.T) {
	b := Backend{}
	if b.Kind() != enforcement.BackendEgressProxy {
		t.Fatalf("kind = %q", b.Kind())
	}
	caps := b.Capabilities()
	if !caps.NetworkCIDR || !caps.NetworkFQDN {
		t.Fatalf("caps = %+v", caps)
	}
}

func TestBackendDesiredState(t *testing.T) {
	empty, err := (Backend{}).DesiredState(enforcement.SessionContext{
		SessionNamespace: "ns",
		SessionName:      "s",
	})
	if err != nil || empty != nil {
		t.Fatalf("got %v err=%v", empty, err)
	}

	raw, err := (Backend{}).DesiredState(baseCtx(scrutineerv1alpha1.PolicyModeEnforced, scrutineerv1alpha1.PolicyRules{
		DeniedDomains: []string{"evil.example"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	cfg, ok := raw.(*ProxyConfig)
	if !ok || cfg == nil || cfg.SessionName != "demo" {
		t.Fatalf("got %T %+v", raw, cfg)
	}
}
