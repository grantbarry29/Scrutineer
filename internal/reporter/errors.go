/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package reporter

import "errors"

var (
	// ErrUnauthorized indicates missing or invalid bearer token authentication.
	ErrUnauthorized = errors.New("unauthorized")
	// ErrForbidden indicates the caller may not report for the claimed session.
	ErrForbidden = errors.New("forbidden")
	// ErrBadRequest indicates a malformed or invalid report payload.
	ErrBadRequest = errors.New("bad request")
	// ErrNotFound indicates the target AgentSession does not exist.
	ErrNotFound = errors.New("not found")
	// ErrConflict indicates status could not be updated after internal retries.
	ErrConflict = errors.New("conflict")
	// ErrPayloadTooLarge indicates the request body exceeds MaxReportBytes.
	ErrPayloadTooLarge = errors.New("payload too large")
	// ErrRateLimited indicates per-session rate limit exceeded.
	ErrRateLimited = errors.New("rate limited")
	// ErrVerifyThrottled indicates the global identity-verification budget is spent
	// (#104): the request was rejected before any TokenReview. Maps to 503 +
	// Retry-After — transient for well-behaved clients.
	ErrVerifyThrottled = errors.New("identity verification throttled")
)
