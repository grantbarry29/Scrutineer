/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package networkpolicy

import "github.com/secureai/relay/internal/enforcement"

// Backend renders Kubernetes NetworkPolicy desired state for AgentSession runtimes.
type Backend struct{}

func (Backend) Kind() enforcement.BackendKind {
	return enforcement.BackendNetworkPolicy
}

func (Backend) Capabilities() enforcement.Capabilities {
	return enforcement.Capabilities{NetworkCIDR: true}
}

// DesiredState returns a *networkingv1.NetworkPolicy when applicable, otherwise nil.
func (Backend) DesiredState(ctx enforcement.SessionContext) (any, error) {
	np := Build(ctx)
	if np == nil {
		return nil, nil
	}
	return np, nil
}
