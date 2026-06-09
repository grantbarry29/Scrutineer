/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package dnsproxy

import (
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/secureai/relay/internal/enforcement"
)

// BuildConfig renders desired proxy configuration for a session, or nil when not applicable.
func BuildConfig(ctx enforcement.SessionContext) *ProxyConfig {
	if !Applicable(ctx) {
		return nil
	}
	return &ProxyConfig{
		SessionNamespace: ctx.SessionNamespace,
		SessionName:      ctx.SessionName,
		Mode:             ctx.Mode,
		AllowedDomains:   append([]string(nil), ctx.Policy.AllowedDomains...),
		DeniedDomains:    append([]string(nil), ctx.Policy.DeniedDomains...),
		AllowedCIDRs:     append([]string(nil), ctx.Policy.AllowedCIDRs...),
		DeniedCIDRs:      append([]string(nil), ctx.Policy.DeniedCIDRs...),
		ListenAddr:       DefaultListenAddr,
		HTTPProxyURL:     DefaultHTTPProxyURL,
	}
}

// EnvForConfig returns sidecar env vars for a proxy configuration.
func EnvForConfig(cfg *ProxyConfig) []corev1.EnvVar {
	if cfg == nil {
		return nil
	}
	return []corev1.EnvVar{
		{Name: EnvListenAddr, Value: cfg.ListenAddr},
		{Name: EnvHTTPProxyURL, Value: cfg.HTTPProxyURL},
		{Name: EnvPolicyMode, Value: string(cfg.Mode)},
		{Name: EnvPolicyAllowedDomains, Value: csv(cfg.AllowedDomains)},
		{Name: EnvPolicyDeniedDomains, Value: csv(cfg.DeniedDomains)},
		{Name: EnvPolicyAllowedCIDRs, Value: csv(cfg.AllowedCIDRs)},
		{Name: EnvPolicyDeniedCIDRs, Value: csv(cfg.DeniedCIDRs)},
	}
}

func csv(in []string) string {
	return strings.Join(in, ",")
}
