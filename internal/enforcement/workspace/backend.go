/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package workspace

import "github.com/grantbarry29/scrutineer/internal/enforcement"

// Backend describes file/workspace desired configuration for AgentSession runtimes.
type Backend struct{}

func (Backend) Kind() enforcement.BackendKind {
	return enforcement.BackendFSGateway
}

func (Backend) Capabilities() enforcement.Capabilities {
	return enforcement.Capabilities{FileAccess: true}
}

// DesiredState returns *GatewayConfig when file policy applies.
func (Backend) DesiredState(ctx enforcement.SessionContext) (any, error) {
	cfg := BuildConfig(ctx)
	if cfg == nil {
		return nil, nil
	}
	return cfg, nil
}
