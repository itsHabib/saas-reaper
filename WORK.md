<!-- reaper-work:v1 -->
# Work: Customer-owned ingress tunnel specimen

Work-ID: ingress-tunnel-specimen
Status: active
Subject: git:6517797efde09c323f53c165899caf3bd15c5fbc
Stop-at: reviewed-change

## Outcome

An in-repository, customer-owned Go ingress-tunnel specimen claims subdomains
under one domain, issues one-time agent credentials, routes public requests by
host down a multiplexed WebSocket link to a developer's local port, supersedes
duplicate agents, revokes claims with immediate link closure, persists claims
and an append-only lifecycle audit in SQLite, and ships an AWS Terraform pack
that stands the whole prerequisite up in one apply, all proven by loopback-only
runnable proofs and a validated deployment pack.

## Preserve

- The feature-flags specimen and the factory stay behaviorally unchanged.
- `internal/factory/validate.go` continues to reject every capability other than `feature-flags`.
- Policy, link, edge, agent, HTTP transport, and persistence remain one-way layers with consumer-owned interfaces; only `internal/link` speaks WebSocket or yamux.
- Management, read, and agent credentials stay separate; the configured principal or the agent identity supplies the audit actor.
- Demo and CI tunnel traffic stays on loopback ports `1950x`; the AWS pack is validated and never applied.

## Change

- `specimens/ingress-tunnel/`: add the independent Go module with server, agent, proof fixtures, local demo, invariant probes, scoped checks, and the AWS pack.
- `Makefile`: expose tunnel demo, invariant, deploy-check, and complete specimen proof targets.
- `scripts/setup.sh`: install the nested specimen's pinned dependencies.
- `scripts/check.sh`: include the nested module in the root verification floor.
- `scripts/check-boundaries.sh`: exclude every nested dependency directory, not only the root examples.
- `.github/workflows/ci.yml`: run the loopback tunnel proofs and the deployment pack validation, and cache the new module sums.
- `REAPER.yaml`: declare the specimen in the capability manifest.
- `README.md`: describe the specimen, its selection principle, its runnable proof, and what the pack does not do.
- `AGENTS.md`: record the specimen's layering, lifecycle table, credential boundary, and proof obligations.
- `CLAUDE.md`: keep the paired agent guide byte-identical.
- `docs/features/ingress-tunnel/kickoff.md`: record the design decisions the specimen was built against.
- `WORK.md`: bind this exact task, proof, and stop boundary.

## Prove

- Green: `make check` passes with nested Go race tests, strict lint, shell format, shellcheck, and the specimen's own boundary script.
- Green: `make tunnel-demo` shows two hosts reaching only their own origins, a mebibyte crossing unchanged, a chunked response streaming, and a WebSocket upgrade surviving every hop.
- Green: `make tunnel-invariants` proves isolation, credential gating, authority separation, supersession, restart durability, revocation, and audit integrity.
- Green: `make tunnel-deploy-check` formats, validates, and cross-compiles the AWS pack without an account.
- Green: `make product-demo` remains unaffected by the nested module.
- Red: an unknown host is 404, an unclaimed or offline subdomain is 502, a malformed or revoked credential is 401 and the agent exits, a stale or repeated revoke is 409, and a spoofed forwarded header never reaches the origin.

## Stop

- Stop if the nested module requires root import coupling, a `go.work`, or factory capability changes.
- Stop before any non-loopback proof dependency, cloud account, applied infrastructure, Gate invocation, or merge.
- Stop after two review-fix rounds even if a broader architectural finding remains.

## Evidence

- Verified: nested `make check`, `make demo`, `make invariants`, and `make deploy-check` pass locally.
- Verified: the exhaustive lifecycle walk pins 3 reachable statuses and 9 edges, and a revoked claim never carries a live link.
- Verified: the superseded agent exits on WebSocket status 4001 instead of reconnecting, and the revoked one on 4003.
- Reviewed: a ten-finding local review found two ordering races in policy, an unreachable shutdown path, a vacuous harness assertion, and an instance-replacing digest in the pack; all were fixed with regression tests or harness assertions.
- Reviewed: a second local round found a supersession flap when the close frame is missed, a token rotation that never reached the host, a systemd-expanded updater loop, a cross-compiled xcaddy, and a fresh-checkout apply failure; all were fixed, with the cooldown pinned by tests.

## Handoff

- Last: both allowed review rounds folded; residual notes are recorded in the pull request.
- Next: CI green on the reviewed head, then stop without Gate or merge; Codex re-review when its quota returns is the operator's call.
