/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package workspace

import (
	"path"
	"strings"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/enforcement"
)

const (
	ReasonAllowed           = "Allowed"
	ReasonDeniedPaths       = "DeniedPaths"
	ReasonNotInAllowedPaths = "NotInAllowedPaths"
	ReasonEmptyPath         = "EmptyPath"
)

// HasFilePolicy reports whether effective policy contains file/path governance hints.
func HasFilePolicy(rules scrutineerv1alpha1.PolicyRules) bool {
	return len(rules.AllowedPaths) > 0 ||
		len(rules.DeniedPaths) > 0 ||
		rules.MaxWorkspaceBytes != nil
}

// Applicable reports whether file policy evaluation or fs-gateway config should run.
func Applicable(ctx enforcement.SessionContext) bool {
	return HasFilePolicy(ctx.Policy) || HasEnabledSidecar(ctx)
}

// HasEnabledSidecar reports whether the session context includes an enabled fs-gateway sidecar.
func HasEnabledSidecar(ctx enforcement.SessionContext) bool {
	for _, s := range ctx.Enforcement {
		if s.Type != EnforcementType {
			continue
		}
		if s.Enabled == nil || *s.Enabled {
			return true
		}
	}
	return false
}

// EvaluateFile applies effective file policy and mode semantics to a file access request.
func EvaluateFile(ctx enforcement.SessionContext, req FileRequest) FileAuthorization {
	p := normalizePath(req.Path)
	if p == "" {
		return FileAuthorization{
			Evaluation: enforcement.Evaluation{
				Allowed: false,
				Action:  scrutineerv1alpha1.PolicyDecisionDeny,
				Blocked: ctx.Mode == scrutineerv1alpha1.PolicyModeEnforced,
			},
			Reason: ReasonEmptyPath,
		}
	}

	rules := ctx.Policy
	if matchesAnyPath(rules.DeniedPaths, p) {
		return authorize(ctx.Mode, true, ReasonDeniedPaths)
	}
	if len(rules.AllowedPaths) > 0 && !matchesAnyPath(rules.AllowedPaths, p) {
		return authorize(ctx.Mode, true, ReasonNotInAllowedPaths)
	}
	return FileAuthorization{
		Evaluation: enforcement.Evaluation{
			Allowed: true,
			Action:  scrutineerv1alpha1.PolicyDecisionAllow,
		},
		Reason: ReasonAllowed,
	}
}

func authorize(mode scrutineerv1alpha1.PolicyMode, ruleWouldDeny bool, reason string) FileAuthorization {
	return FileAuthorization{
		Evaluation: enforcement.EvaluateRestrictive(mode, ruleWouldDeny),
		Reason:     reason,
	}
}

func normalizePath(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	return path.Clean(raw)
}

func matchesAnyPath(patterns []string, p string) bool {
	for _, pattern := range patterns {
		if pathMatches(pattern, p) {
			return true
		}
	}
	return false
}

// pathMatches reports whether an absolute path matches a policy pattern.
// Supports exact paths, shell globs via path.Match, and trailing /** directory prefixes.
func pathMatches(pattern, p string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return false
	}
	if pattern == p {
		return true
	}
	if strings.HasSuffix(pattern, "/**") {
		prefix := strings.TrimSuffix(pattern, "/**")
		if p == prefix || strings.HasPrefix(p, prefix+"/") {
			return true
		}
	}
	ok, err := path.Match(pattern, p)
	return err == nil && ok
}
