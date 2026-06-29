/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package workspace

import (
	"testing"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/enforcement"
)

func TestBackend_metadata(t *testing.T) {
	b := Backend{}
	if b.Kind() != enforcement.BackendFSGateway {
		t.Fatalf("kind = %q", b.Kind())
	}
	if !b.Capabilities().FileAccess {
		t.Fatal("expected file access capability")
	}
}

func TestBackendDesiredState(t *testing.T) {
	raw, err := (Backend{}).DesiredState(enforcement.SessionContext{
		SessionNamespace: "ns",
		SessionName:      "s",
	})
	if err != nil || raw != nil {
		t.Fatalf("got %v err=%v", raw, err)
	}

	raw, err = (Backend{}).DesiredState(enforcement.SessionContext{
		SessionNamespace: "ns",
		SessionName:      "s",
		Policy: scrutineerv1alpha1.PolicyRules{
			DeniedPaths: []string{"/etc/**"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if raw == nil {
		t.Fatal("expected config for file policy")
	}
}
