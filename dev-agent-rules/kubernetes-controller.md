
# Kubernetes Controller Rules

You are working on production-grade Kubernetes controller code. Optimize for correctness, safety, idempotency, debuggability, and operational clarity over cleverness.

**Before changing code:**
- Read the relevant existing code paths first.
- Identify the resource, controller, reconcile path, or networking path being changed.
- Prefer small, reviewable changes.
- Do not introduce hidden global behavior, background goroutines, caches, retries, polling loops, or network calls without justification.
- If a requirement is ambiguous, choose the safest Kubernetes-native behavior and document the assumption.
- If you notice an eventual requirement that is out of scope, file a **GitHub Issue** (see `dev-agent-rules/task-management.md`) instead of implementing it silently.

## Reconciliation

1. **Reconcile must be idempotent.** Safe to run repeatedly for the same object. Never assume it runs once, or only after create/update. Compute desired state from spec, compare with actual, apply only the minimal mutation to converge. External side effects must be idempotent or protected by durable state, operation IDs, finalizers, or status checkpoints.

2. **Reconcile must be level-based, not event-based.** Treat watch events as hints only. Do not depend on event ordering or assume every event is delivered. Do not assume cache reads are perfectly fresh. Re-read the object at the start of reconcile.

3. **Use finalizers for external cleanup.** If the controller creates external resources, add a finalizer *before* creating them. On deletion, check `deletionTimestamp`. Cleanup must be idempotent. Remove the finalizer only after cleanup succeeds or is proven unnecessary. Never delete external resources unless ownership is proven. Finalizer names must be domain-qualified (e.g. `scrutineer.sh/finalizer`).

4. **Spec is user-owned desired state; status is controller-owned observed state.** Do not mutate spec from the controller. Enable the status subresource. Update status via `Status().Patch` or `Status().Update`. Do not use status as the source of desired behavior or require users to edit it.

5. **Conditions must be useful.** Every serious CR should have a `Ready` condition using `metav1.Condition`. Set `ObservedGeneration`. Use stable machine-readable Reasons (`Reconciling`, `Provisioned`, `DependencyNotReady`, `InvalidSpec`, `ExternalCreateFailed`, `ExternalDeleteFailed`). Do not report `Ready=True` unless the latest `metadata.generation` has been reconciled. Always surface meaningful failures in status.

6. **Handle conflicts and stale objects safely.** Expect 409 Conflict. Prefer patch over full update for metadata/status. Use `client.MergeFrom` or server-side apply where appropriate. Do not blindly overwrite objects fetched earlier. Re-fetch or retry on conflict. Never ignore update/patch errors.

7. **Use owner references for owned child resources.** Use `controllerutil.SetControllerReference`. Use labels for lookup, owner refs for ownership. Do not set invalid cross-namespace owner refs. Do not adopt unrelated resources unless explicitly designed.

8. **Watches must be intentional.** Watch the primary resource with `For()`, owned children with `Owns()`, referenced resources only if their changes should trigger reconciliation. Use predicates to reduce noisy reconciles. Do not watch huge resource sets without filtering/indexing/justification.

9. **Requeue deliberately.** Return errors for transient failures so controller-runtime rate limiting applies. Use `RequeueAfter` only for known future checks. Do not hot-loop. Do not requeue immediately after every successful reconcile. Document why each explicit requeue exists.

10. **External calls must be bounded.** Every external API call uses `context.Context` and a timeout. Classify errors as retryable or permanent. No unbounded calls inside reconcile. External clients must be injectable interfaces for tests. Never log secrets, tokens, credentials, signed URLs, or full request bodies.

11. **Avoid hidden shared mutable state.** Reconciler fields must be safe for concurrent reconciles. Protect or avoid shared maps. Prefer stateless reconcilers. Do not store per-object progress only in memory — persist to Kubernetes (status, annotations, leases) or external durable storage if it must survive restart.

12. **Production lifecycle safety.** Use leader election with multiple replicas. Do not perform singleton side effects outside leader election. Fail fast on invalid static config. Expose health/readiness checks. Readiness reflects whether the controller can operate, not whether every managed resource is healthy.

## Go Coding

41. **Use interfaces at external boundaries.** Kubernetes/cloud/dataplane/DNS clients and clocks should be injectable. Do not call real cloud APIs in unit tests. Define narrow interfaces near the consumer; avoid giant clients.

42. **Preserve error meaning.** Wrap errors with operation context. Use typed/sentinel errors for retryable, not-found, conflict, invalid-spec, and unauthorized cases. Do not string-match errors unless unavoidable. Do not swallow errors. Avoid duplicate noisy logs at every layer.

43. **Use context correctly.** Pass `ctx` first to blocking/network functions. Do not store context in structs. Do not use `context.Background` inside reconcile except at process-setup boundaries. Honor cancellation. Always call `cancel` for timeout contexts.

44. **Prevent goroutine leaks.** Every goroutine needs a cancellation path tied to manager lifecycle, context, or explicit stop channel. Do not start goroutines inside reconcile unless absolutely necessary; if required, document ownership and shutdown.

45. **Make output deterministic.** Sort maps before rendering resources/rules/configs/test output. Avoid random names; prefer deterministic names from namespace/name/UID. Avoid time-dependent logic unless using an injectable clock.

## Testing

46. **Every controller change needs tests:** create, update, no-op/idempotent, deletion/finalizer, missing dependency, external API failure, status/condition updates, conflict/retry (where practical), deterministic ordering.

47. **Reconcile tests verify state, not implementation trivia.** Given initial state, run reconcile, verify resulting state, run again, verify no unnecessary duplicate work. Do not overfit to private helper calls.

48. **CRD schema must be tested.** Check in generated CRDs if that is project convention; CI must fail if generated manifests are stale. Example YAMLs must validate against the CRD. Important CEL validation needs positive and negative tests.

49. **Use envtest where API server behavior matters** — CRDs, status subresources, validation, webhooks, finalizers, owner refs. Unit tests suffice for pure functions. Do not rely only on fake clients for behavior that depends on real API server semantics.

## Security and RBAC

50. **Least privilege RBAC.** Avoid wildcard resources/verbs and cluster-wide permissions unless necessary. Separate read, write, status, finalizer, and event permissions. Do not grant secret read unless absolutely required.

51. **Handle secrets safely.** Never log secret values or put them in status, events, errors, or metrics labels. Prefer Secret references over embedding material in CRDs. Avoid copying secrets across namespaces unless explicitly required and documented.

52. **Respect multi-tenancy.** Namespace boundaries matter. Cross-namespace references require explicit design. Do not let one namespace select or mutate another's resources unless intended. Do not assume the controller's permissions match the user's.

## Performance and Scalability

53. **Know the complexity.** For loops over pods/rules/endpoints/labels/flows/FQDNs, state time and space complexity. Avoid `O(all pods × all rules)` in reconcile. Use indexes for reverse lookups. Cache only when staleness behavior is understood. Bound memory.

54. **Avoid API server overload.** Do not list all pods/services/nodes every reconcile unless unavoidable. Use field indexes and label selectors. Watch and enqueue specific owners instead of global rescans. Patch only when content changes. Skip status updates when status is unchanged. Do not create Events every reconcile.

55. **Do not write rapidly changing data to status.** No per-loop timestamps or metrics-like data. Compare old vs new status before patching. Keep high-frequency data in metrics, not status.

## Observability (part of correctness)

- Structured logs for reconcile start/end, major decisions, external operations, and errors. Stable keys: `namespace`, `name`, `uid`, `generation`, `resourceID`, `operation`.
- Metrics for reconcile count, failures, duration, external API latency, queue depth, managed resource counts.
- Kubernetes Events for user-visible state transitions only — do not spam every reconcile.

## Controller/CRD Task Checklist (answer before coding)

1. Primary resource? 2. Desired state? 3. Actual observed state? 4. Owned child K8s resources? 5. Owned external resources? 6. Finalizer needed? 7. Status conditions needed? 8. Immutable fields? 9. Validation/defaulting required? 10. Watches required? 11. Indexes required? 12. Failure modes? 13. Retries/requeues required? 14. Tests proving idempotency? 15. Metrics/events/logs needed?

## Anti-Patterns To Reject (unless explicitly requested + justified)

Reconcile that only works on create events · external resource creation before adding a finalizer · mutating spec from the controller · unbounded lists in status · ignoring `context.Context` · infinite retries · hot-loop requeues · full object updates when patch is safer · logging secrets · relying on Go map iteration order · broad RBAC wildcards · global list-all scans every reconcile · assuming the cache is perfectly fresh · assuming only one controller replica exists · adding goroutines inside reconcile · using status as an event log.

## Highest Priority (on conflict, prioritize)

1. Reconcile idempotent and level-based. 2. Spec is desired, status is observed. 3. Finalizers before creating external resources. 4. Every external op needs timeout + retry classification + idempotency. 5. Every controller change includes idempotency, deletion, failure, and status tests. 6. Never silently fail open for security/network policy. 7. Do not store high-cardinality runtime data in status. 8. Out-of-scope future requirements go to a GitHub Issue (see `task-management.md`), not silent implementation.
