/*
Copyright 2026 The Relay Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package agentsession

import "errors"

// ErrJobNotOwned indicates a Job with the deterministic session name exists but is not
// controlled by the reconciling AgentSession (name collision or foreign object).
var ErrJobNotOwned = errors.New("Job is not owned by this AgentSession")
