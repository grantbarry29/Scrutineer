---
type: Agent Rule
title: Test-Driven Development
description: "Build features test-first and environment-first: the matching test level (unit/envtest/e2e) must be runnable before feature code, and correctness only a running artifact can prove needs an e2e test."
status: live
read_when: "Always — before writing feature code."
always_load: true
---

# Test-Driven Development — E2E Environment First

**When building a feature, secure the test environment before the code, and let tests
drive the build.** This is how we get correctness on non-trivial (especially networking
and controller) work, where "it compiles" and "it unit-passes" are weak signals.

## The loop

1. **Ensure the right test environment exists first.** Before writing feature code,
   confirm you can actually *run* the level of test the feature needs:
   - Pure logic → unit tests suffice.
   - Controller/reconciler behavior → **envtest** must be runnable.
   - Data-plane / networking / deployed-workload behavior (proxies, sidecars, egress,
     webhooks, overlays) → a **live `make test-e2e` against kind** must be runnable.
   If that environment isn't available, **set it up (or fix it) before building the
   feature** — don't build against a level of test you can't execute.
2. **Write the test first** — a failing test that encodes the acceptance criterion.
3. **Build the feature** until the test passes.
4. **Run the tests in the devcontainer** (see [`devcontainer.md`](devcontainer.md)) and
   **iterate** until green.

## Rules

- **Match the test level to the risk.** Correctness that can only be proven by running
  the real thing (e.g. an Envoy config, an iptables/redirect path, a deployed overlay)
  **must** have an e2e test — unit tests that only assert "the string we generated equals
  the string we expected" do not prove the artifact works. Do not claim such a feature
  done on unit tests alone.
- **Do not merge feature code whose correctness is unverified** because its test
  environment wasn't available. Land it on a branch and finish the environment first.
- **Fix the harness as part of the feature.** If the e2e/envtest environment is missing
  or broken, wiring it up is in-scope prep for the feature that needs it — not a reason to
  skip verification.
- Keep tests deterministic and hermetic; prefer failure-injection and boundary cases over
  happy-path-only.

## Why

Scrutineer is a governance/security control plane; its value is that enforcement and
evidence actually hold. Shipping enforcement code that merely compiles is worse than
shipping nothing — it implies a guarantee that hasn't been demonstrated. Test-first,
environment-first development keeps the demonstrated guarantee and the claimed guarantee
in sync. See also [[devcontainer]] (where tests run) and the design docs for what each
feature must demonstrate.
