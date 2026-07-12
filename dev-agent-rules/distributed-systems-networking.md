---
type: Agent Rule
title: Distributed Systems & Networking
description: "Strict distributed-systems and networking standards — partial failure, timeouts, idempotency, fail-closed policy, deterministic rules."
status: live
read_when: "Distributed-systems / networking code (internal/**, cmd/**)."
applies_to: ["internal/**/*.go", "cmd/**/*.go"]
always_load: false
---

# Distributed Systems and Networking Rules

You are working on distributed-systems and networking code. Assume partial failure everywhere. Optimize for convergence, bounded side effects, deterministic output, and fail-closed safety.

**Before changing code:**
- Identify the reconcile path or networking path being changed and the failure modes it introduces.
- Do not add hidden retries, polling loops, caches, goroutines, or network calls without justification.
- Choose the safest behavior under ambiguity and document the assumption.
- Out-of-scope future requirements go to a **GitHub Issue** (see `dev-agent-rules/task-management.md`), not silent implementation.

## Distributed Systems

24. **Assume partial failure.** Every remote call can fail, timeout, be slow, or partially succeed. Every write can succeed while the response is lost. Every retry can duplicate side effects unless idempotent. Every cache can be stale. Every process can restart between two lines of code. Every network path can drop, delay, duplicate, or reorder packets.

25. **Timeouts are mandatory.** All network calls need explicit timeouts. Blocking operations accept `context.Context`. No infinite waits in reconcile. Log timeout errors with operation name and target — never secrets.

26. **Retries must be bounded and safe.** Retry only transient errors. Use exponential backoff with jitter for external services. Bound retry count or total duration. Retried operations must be idempotent or protected by idempotency keys. Do not retry validation errors, authz errors, malformed requests, or unsupported specs. Do not layer aggressive retry loops on top of controller-runtime retries.

27. **Rate limit anything that can stampede.** External API calls need client-side rate limits. Reconcile concurrency must respect API quotas and cluster size. Error paths must not hot-loop. Fan-out operations must be bounded. Requeues after shared-dependency failures should include jitter.

28. **Prefer convergence over transactions.** Do not pursue distributed transactional consistency unless required. Store enough durable state to resume. Make every step safe to repeat. Be explicit about which invariants are strongly vs eventually consistent.

29. **Use idempotency keys for external resources.** Derive deterministic external names or idempotency tokens from the Kubernetes object UID. Do not use only object name if delete/recreate semantics matter. Store external resource IDs in status after creation. On retry, check whether the resource already exists. On deletion, tolerate not-found.

30. **Make ownership explicit.** Every external resource the controller creates must carry tags/labels identifying owner namespace, name, UID, controller name, and cluster identity if available. Cleanup must use durable ownership markers, not fuzzy name matching. Do not delete external resources unless ownership is proven.

31. **Avoid split brain.** Use leader election for active/passive controllers. Use leases or external locks when coordinating singleton external resources. Design side effects to tolerate duplicate actors when possible. Never assume only one controller pod exists unless enforced.

32. **Observability is part of correctness.** Structured logs for reconcile start/end, major decisions, external operations, and errors with stable keys (`namespace`, `name`, `uid`, `generation`, `resourceID`, `operation`). Metrics for reconcile count, failures, duration, external latency, queue depth, managed counts. Kubernetes Events for user-visible transitions only — do not spam. Wrap errors with actionable context.

## Networking

33. **Be explicit about packet direction and identity.** For every networking change, identify direction (ingress, egress, return path, east-west, north-south) and the policy identity (pod IP, node IP, service IP, original/translated source IP, FQDN, SNI, HTTP Host, label identity, connection tuple). Document where NAT/SNAT/DNAT/masquerade/proxying/load balancing changes identity. Do not assume source IP is preserved.

34. **Rule behavior must be deterministic.** Rule ordering must be explicit. Do not depend on Go map iteration order. Sort generated rules by stable keys before rendering/applying. If priorities are assigned, document the algorithm. Test rule ordering.

35. **Fail closed for security policy.** Security/network policy defaults must fail closed unless the product explicitly promises fail open. Do not silently skip invalid rules — invalid policy produces a clear condition. Be careful with wildcards. Require explicit user intent for broad allow rules. Surface when policy could not be fully programmed.

36. **Handle DNS and FQDNs carefully.** DNS names are not stable identities by default. Respect TTLs when using DNS-derived IPs. Be explicit about wildcard semantics. Avoid regex rules unless documented and tested. Normalize FQDN casing and trailing dots. Decide whether matching occurs on DNS query name, SNI, HTTP Host, cert SAN, or resolved IP.

37. **Account for connection tracking and long-lived flows.** Any traffic-affecting change must consider existing connections. Decide whether changes apply to new connections only or existing flows too. Document conntrack behavior. For draining, distinguish "stop accepting new connections" from "terminate existing connections". Do not delete state needed for return traffic before flows drain.

38. **Data plane updates must be atomic or staged.** Avoid partial firewall/proxy state that can drop traffic. Prefer build-new-then-swap over mutate-in-place when supported. If atomic swap is unavailable, order operations to preserve safety. Validate generated config before applying. Keep rollback / last-known-good where practical.

39. **Be careful with high-cardinality state.** Per-pod/per-flow/per-FQDN/per-connection state can explode at scale. Estimate memory/CPU complexity before implementing. Do not store high-cardinality data in CR status. Use indexes, maps, sets, tries, prefix structures, or ipsets. Bound queues and caches. Garbage-collect stale entries. Add cardinality metrics.

40. **Do not assume IPv4 only.** Validate IP family where needed. Parse CIDRs with standard libraries. Do not compare IPs as raw strings. Normalize CIDRs before storing/comparing. Add IPv6 tests if dual-stack is supported. Be explicit if a feature is IPv4-only.

## Networking Task Checklist (answer before coding)

1. Traffic direction affected? 2. Identity policy matches on? 3. Where can NAT/proxying change identity? 4. Existing connections affected? 5. Is the update atomic? 6. Fail-open vs fail-closed behavior? 7. How are rules ordered? 8. IPv6/dual-stack behavior? 9. Scale/cardinality risk? 10. What packet/flow tests are needed?

## Distributed Systems Task Checklist (answer before coding)

1. What can fail partially? 2. What if the process restarts halfway? 3. Is the operation idempotent? 4. What durable state records progress? 5. What timeout applies? 6. What retry policy applies? 7. What rate limit applies? 8. What is the consistency model? 9. What happens under duplicate events? 10. What observability proves it is working?

## Anti-Patterns To Reject (unless explicitly requested + justified)

Ignoring `context.Context` · infinite retries · hot-loop requeues · relying on Go map iteration order · treating DNS names as permanent identities · silently skipping invalid user policy · fuzzy name matching for external cleanup · assuming source IP is preserved · partial/mutate-in-place dataplane writes that can drop traffic · unbounded high-cardinality state · assuming IPv4 only · assuming only one controller replica exists.

## Highest Priority (on conflict, prioritize)

1. Assume partial failure; every external op needs timeout + bounded retry classification + idempotency. 2. Make every step safe to repeat (convergence over transactions). 3. Generated rules/config must be deterministic. 4. Never silently fail open for security/network policy. 5. Do not store high-cardinality runtime data in CR status. 6. Out-of-scope future requirements go to a GitHub Issue (see `task-management.md`), not silent implementation.
