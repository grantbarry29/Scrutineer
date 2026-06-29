/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package agentsession

import (
	"time"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	scrutineerjob "github.com/grantbarry29/scrutineer/internal/controller/job"
	"github.com/grantbarry29/scrutineer/internal/enforcement"
	"github.com/grantbarry29/scrutineer/internal/enforcement/dnsproxy"
)

// ApplyEgressProxyRuntimeEvent merges dns-proxy runtime evidence into session status.
// Sidecars (or a future reporter controller) call this after observing egress.
func ApplyEgressProxyRuntimeEvent(session *scrutineerv1alpha1.AgentSession, profile *scrutineerv1alpha1.RuntimeProfile, ev dnsproxy.RuntimeEvent, now time.Time) {
	if session == nil {
		return
	}
	ctx := enforcement.NewSessionContext(session, profile, scrutineerjob.NameFor(session))
	if session.Status.EffectivePolicy != nil {
		ep := session.Status.EffectivePolicy
		ctx.Mode = ep.Mode
		ctx.Policy = ep.PolicyRules
	}
	ApplyRuntimePolicyReport(session, dnsproxy.RuntimeReportFromEvent(ctx, ev, now))
}
