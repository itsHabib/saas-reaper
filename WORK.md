<!-- reaper-work:v1 -->
# Work: Customer-owned notification routing specimen

Work-ID: notification-routing-specimen
Status: active
Subject: git:9334a0bbc99df69dc19abd834249ee89c23cb57e
Stop-at: reviewed-change

## Outcome

An in-repository, customer-owned Go notification-routing specimen defines
per-channel templates, stores recipients with per-channel addresses and
preferences, renders and fans one send out to every channel a recipient allows,
deduplicates re-sends by idempotency key, retries transient transport failures
on a bounded schedule, and persists an append-only attempt audit that local
runnable proofs verify against a real third-party SMTP server implementation
and a Slack-shape incoming-webhook receiver.

## Preserve

- The feature-flags and webhook-delivery specimens stay behaviorally unchanged.
- `internal/factory/validate.go` continues to reject every capability other than `feature-flags`.
- Policy, HTTP transport, persistence, and worker mechanisms remain one-way layers with consumer-owned interfaces.
- Management and audit-read credentials stay separate; the configured principal supplies the audit actor.
- Demo and CI notification traffic stays on loopback ports `1940x` and no external infrastructure is provisioned.

## Change

- `specimens/notification-routing/`: add the independent Go module, both transport packages, SMTP and Slack-shape sink fixtures, local demo, invariant probes, and scoped checks.
- `Makefile`: expose notification demo, invariant, and complete specimen proof targets.
- `scripts/setup.sh`: install the nested specimen's pinned dependencies.
- `scripts/check.sh`: include the nested module in the root verification floor.
- `.github/workflows/ci.yml`: run the local-only notification transport and invariant proof, cache the new module sums, and declare the tools the fast floor already used.
- `README.md`: describe the third specimen, its selection principle, its runnable proof, and what that proof does not establish.
- `AGENTS.md`: record the third specimen's layering, transport seam, token boundary, and proof obligations.
- `CLAUDE.md`: keep the paired agent guide byte-identical.
- `WORK.md`: bind this exact task, proof, and stop boundary.

## Prove

- Green: `make check` passes with nested Go race tests, strict lint, shell format, shellcheck, and the specimen's own boundary script.
- Green: `make notification-demo` shows `emersion/go-smtp` parsing the rendered subject and body and the Slack-shape receiver validating the documented payload.
- Green: `make notification-invariants` proves render rejection, idempotency, honored preferences, retry auditing, restart durability, and token separation.
- Green: `make product-demo` remains unaffected by the nested module.
- Red: a send missing a template variable returns 400 and reaches no transport, a reused idempotency key delivers only once, a reused key with a different payload returns 409, and a forced audit insert failure cannot advance delivery state.

## Stop

- Stop if the nested module requires root import coupling, a `go.work`, or factory capability changes.
- Stop before any non-loopback proof dependency, vendor account, external infrastructure, Gate invocation, or merge.
- Stop after two review-fix rounds even if a broader architectural finding remains.

## Evidence

- Verified: nested `make check`, `make demo`, and `make invariants` pass; the real SMTP server received the rendered notification and the Slack-shape receiver accepted the documented payload.
- Verified: the exhaustive delivery state walk pins 13 reachable durable states and reaches every terminal state.
- Verified: race tests cover the dispatcher continuing past a poisoned audit write, an unconfigured transport kind failing permanently, shutdown auditing, atomic audit rollback, and address redaction from the read authority.
- Reviewed: the eight defects recorded in the webhook specimen's review were designed against rather than repeated.

## Handoff

- Last: the specimen, its two proofs, and the root wiring are complete and green on this working tree.
- Next: run the full verification bar on the final tree, push, open the pull request, and stop at the reviewed CI-green head without Gate or merge.
