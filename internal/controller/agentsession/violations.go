/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package agentsession

import (
	relayv1alpha1 "github.com/secureai/relay/api/v1alpha1"
	"github.com/secureai/relay/internal/enforcement"
)

// AppendRuntimeViolations appends policy violations onto session status without
// exceeding the shared cap.
func AppendRuntimeViolations(session *relayv1alpha1.AgentSession, incoming []relayv1alpha1.PolicyViolation) {
	if session == nil || len(incoming) == 0 {
		return
	}
	session.Status.Violations = enforcement.AppendViolations(
		session.Status.Violations,
		incoming,
		enforcement.MaxViolations,
	)
}

func violationKey(v relayv1alpha1.PolicyViolation) string {
	return v.Time.String() + "\x00" + v.Type + "\x00" + v.Target + "\x00" + v.Message
}

// mergeViolationsInPlace appends violations from preserve that are absent from dst.
func mergeViolationsInPlace(dst *[]relayv1alpha1.PolicyViolation, preserve []relayv1alpha1.PolicyViolation) {
	if dst == nil || len(preserve) == 0 {
		return
	}
	keys := make(map[string]struct{}, len(*dst))
	for _, v := range *dst {
		keys[violationKey(v)] = struct{}{}
	}
	var missing []relayv1alpha1.PolicyViolation
	for _, v := range preserve {
		if _, ok := keys[violationKey(v)]; !ok {
			missing = append(missing, v)
		}
	}
	if len(missing) == 0 {
		return
	}
	*dst = enforcement.AppendViolations(*dst, missing, enforcement.MaxViolations)
}
