/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package agentsession

import (
	"strings"
	"testing"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
	relayjob "github.com/secureai/relay/internal/controller/job"
)

func TestOutputResourceName_truncatesLongSessionNames(t *testing.T) {
	t.Parallel()
	name := outputResourceName("relay-logs-", strings.Repeat("a", 80))
	if len(name) > 63 {
		t.Fatalf("name length = %d", len(name))
	}
	if !strings.HasPrefix(name, "relay-logs-") {
		t.Fatalf("name = %q", name)
	}
}

func TestResolveArtifactPath_defaultsUnderWorkspace(t *testing.T) {
	t.Parallel()
	session := &relayv1alpha1.AgentSession{
		Spec: relayv1alpha1.AgentSessionSpec{
			Workspace: relayv1alpha1.WorkspaceSpec{MountPath: "/data/ws"},
		},
	}
	got, err := resolveArtifactPath(session)
	if err != nil {
		t.Fatal(err)
	}
	if got != "/data/ws/artifacts" {
		t.Fatalf("path = %q", got)
	}
}

func TestResolveArtifactPath_honorsSpec(t *testing.T) {
	t.Parallel()
	session := &relayv1alpha1.AgentSession{
		Spec: relayv1alpha1.AgentSessionSpec{
			Outputs: relayv1alpha1.OutputSpec{ArtifactPath: "/workspace/out"},
		},
	}
	got, err := resolveArtifactPath(session)
	if err != nil {
		t.Fatal(err)
	}
	if got != "/workspace/out" {
		t.Fatalf("path = %q", got)
	}
}

func TestValidateArtifactPath_rejectsRelative(t *testing.T) {
	t.Parallel()
	if err := validateArtifactPath("relative/path"); err == nil {
		t.Fatal("expected error")
	}
}

func TestAppendUniqueArtifacts(t *testing.T) {
	t.Parallel()
	dst := []relayv1alpha1.ArtifactRef{{Name: "agent-logs", URI: "configmap://ns/a"}}
	got := appendUniqueArtifacts(dst, []relayv1alpha1.ArtifactRef{
		{Name: "agent-logs", URI: "configmap://ns/b"},
		{Name: "workspace-artifacts", URI: "secret://ns/b"},
	})
	if len(got) != 2 {
		t.Fatalf("artifacts = %+v", got)
	}
}

func TestHasArtifactNamed(t *testing.T) {
	t.Parallel()
	arts := []relayv1alpha1.ArtifactRef{{Name: artifactNameAgentLogs}}
	if !hasArtifactNamed(arts, artifactNameAgentLogs) {
		t.Fatal("expected agent-logs")
	}
	if hasArtifactNamed(arts, artifactNameWorkspaceBundle) {
		t.Fatal("unexpected workspace artifact")
	}
}

func TestOutputLabels_includeSessionRef(t *testing.T) {
	t.Parallel()
	session := &relayv1alpha1.AgentSession{}
	session.Name = "demo"
	labels := outputLabels(session)
	if labels[relayjob.LabelSessionRef] != "demo" {
		t.Fatalf("labels = %+v", labels)
	}
}
