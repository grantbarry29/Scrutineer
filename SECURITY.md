# Security Policy

Scrutineer's entire purpose is enforcement an agent cannot reach around, so reports
that show otherwise are the most valuable input the project can get.

## Reporting a vulnerability

Please report vulnerabilities privately — do not open a public issue.

- **Preferred**: GitHub private vulnerability reporting — [Security →
  Report a vulnerability](https://github.com/grantbarry29/Scrutineer/security/advisories/new).
- **Alternative**: email `grantbarry29@gmail.com` with `[scrutineer security]` in the
  subject.

You will get an acknowledgement within a few days. Scrutineer is pre-1.0 and
maintained by one person, so there is no formal SLA; severity decides priority, and
fixes ship as patch releases on the affected release line. Please allow a fix to
land before disclosing publicly.

## Supported versions

| Version | Supported |
|---------|-----------|
| Latest minor (`release-0.2` line) | ✅ fixes land here |
| Older lines | ❌ upgrade to the latest minor |

## What counts

The interesting class of report is anything that breaks the documented enforcement
guarantees ([docs/reference/egress-guarantees.md](docs/reference/egress-guarantees.md),
[docs/design/untamperable-enforcement.md](docs/design/untamperable-enforcement.md)):

- **Enforcement bypass** — a governed agent achieving egress that does not traverse
  its per-session chokepoint (Envoy proxy + routing-lock NetworkPolicies), or
  escaping a hold (approval gate, lock gate).
- **Evidence forgery or tampering** — making agent-originated data appear as
  `observed` evidence, tampering with the access-log evidence chain, or erasing
  audit state the design says survives.
- **Privilege escalation** — using the controller's or reporter's RBAC, the
  per-session ServiceAccounts, or the projected-token flow to gain access beyond
  what the design grants.

Out of scope: vulnerabilities in upstream components themselves (Kubernetes, Envoy,
kind, CNIs — report those upstream), attacks requiring cluster-admin, and the demo /
e2e / quickstart scaffolding except where it misrepresents an enforcement guarantee.

There is currently no bug bounty; credit is given in release notes unless you ask
otherwise.
