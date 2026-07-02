/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package enforcement

import (
	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
)

// NewSessionContext builds normalized enforcement input from a reconciled AgentSession.
// jobName is the deterministic runtime Job name (scrutineer-session-<session>).
// profile may be nil when no runtimeProfileRef is set.
func NewSessionContext(session *scrutineerv1alpha1.AgentSession, profile *scrutineerv1alpha1.RuntimeProfile, jobName string) SessionContext {
	ctx := SessionContext{JobName: jobName}
	if session == nil {
		return ctx
	}

	ctx.SessionNamespace = session.Namespace
	ctx.SessionName = session.Name
	ctx.PodName = session.Status.PodName

	if session.Status.EffectivePolicy != nil {
		ep := session.Status.EffectivePolicy
		ctx.Mode = ep.Mode
		ctx.Policy = ep.PolicyRules
	}

	if profile != nil && len(profile.Spec.Enforcement) > 0 {
		ctx.Enforcement = append([]scrutineerv1alpha1.RuntimeProfileEnforcement(nil), profile.Spec.Enforcement...)
	}

	return ctx
}
