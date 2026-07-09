/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package version carries the build-time identity of a Scrutineer binary. It is the
// single source the controller uses to reference its *own* images (lock-probe pods,
// injected egress-reporter container), so a dev-built controller points at dev-tagged
// images and a release-built controller points at the matching release tag — the two
// can never silently shadow each other (#112, the failure mode behind #109).
package version

// Version is the image tag this binary was built as. Injected at build time via
//
//	-ldflags "-X github.com/grantbarry29/scrutineer/internal/version.Version=<tag>"
//
// The Makefile passes its dev tag (dev-<git describe>); the release workflow passes
// the release tag (vX.Y.Z). The fallback marks non-image builds (go run, test
// binaries): it deliberately resolves to an image reference that does not exist, so
// a misassembled build fails loudly (ImagePullBackOff) instead of silently running a
// stale release image.
var Version = "v0.0.0-dev"
