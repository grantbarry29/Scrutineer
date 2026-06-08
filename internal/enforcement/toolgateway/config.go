/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package toolgateway

import "github.com/secureai/relay/internal/enforcement"

// BuildConfig renders desired gateway configuration for a session, or nil when not applicable.
func BuildConfig(ctx enforcement.SessionContext) *GatewayConfig {
	if !Applicable(ctx) {
		return nil
	}
	return &GatewayConfig{
		SessionNamespace:  ctx.SessionNamespace,
		SessionName:       ctx.SessionName,
		Mode:              ctx.Mode,
		AllowedTools:      append([]string(nil), ctx.Policy.AllowedTools...),
		DeniedTools:       append([]string(nil), ctx.Policy.DeniedTools...),
		MaxToolCalls:      ctx.Policy.MaxToolCalls,
		MaxCallsPerMinute: ctx.Policy.MaxCallsPerMinute,
		ListenAddr:        DefaultListenAddr,
	}
}
