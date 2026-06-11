/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package agentsession

import (
	"time"

	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
	relayjob "github.com/secureai/relay/internal/controller/job"
	"github.com/secureai/relay/internal/enforcement"
	"github.com/secureai/relay/internal/enforcement/workspace"
)

// ApplyFilePolicyRuntimeEvent merges file/workspace runtime evidence into session status.
// FS gateway sidecars (future) call this after observing file access.
func ApplyFilePolicyRuntimeEvent(session *relayv1alpha1.AgentSession, profile *relayv1alpha1.RuntimeProfile, req workspace.FileRequest, now time.Time) {
	if session == nil {
		return
	}
	ctx := enforcement.NewSessionContext(session, profile, relayjob.NameFor(session))
	if session.Status.EffectivePolicy != nil {
		ep := session.Status.EffectivePolicy
		ctx.Mode = ep.Mode
		ctx.Policy = ep.PolicyRules
	}
	auth := workspace.EvaluateFile(ctx, req)
	ApplyRuntimePolicyReport(session, workspace.RuntimeReport(ctx, req, auth, now))
}
