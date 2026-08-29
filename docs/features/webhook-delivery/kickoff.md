<!-- kickoff:distill -->
# Kickoff — webhook delivery, the factory's second reaped capability

**Audience:** the implementing agent. Rip this and build — it is self-contained and
code-anchored. Trust the anchors but re-open each file before building on it.

**Grounding:** distilled from the session that shipped factory 0.8.0 (four database packs,
both root harnesses, HEAD `5dda570`). Read first, in order: `README.md`, `CLAUDE.md`
(binding law for every capability), `CONTRIBUTING.md`, `scripts/demo.sh` and
`scripts/invariants.sh` (the proof patterns you will mirror), `WORK.md` format via
`scripts/check-work.sh`.

## Why this capability (the selection principle)

The Reaper goes after products that are priced like infrastructure but built like a weekend
project — trivially self-hostable capabilities sold at per-unit rents. Outbound webhook
delivery is the poster child: hosted senders charge per message for an HTTP POST, an HMAC
signature, and a retry loop. It is also the best-proofed candidate: **Standard Webhooks**
(github.com/standard-webhooks/standard-webhooks) is an open spec with official verifier
libraries in Go, JavaScript, and Python — so conformance can be proven with *real
third-party clients*, exactly the OpenFeature play that made feature flags credible.
Record this principle in the doc you write; it is the capability roadmap's filter.

## Goal (one line)

A customer-owned outbound webhook-delivery golden specimen — endpoints, signed deliveries,
bounded retries, replay, append-only attempt audit — proven by a demo in which the official
Standard Webhooks Go/JS/Python verifier libraries validate real deliveries.

## In scope

1. The **specimen service** (Go, mirroring the flags specimen's shape): management API to
   register/disable endpoints and publish messages; a delivery engine that signs and POSTs
   to registered endpoints with a bounded retry schedule; replay of a message to one
   endpoint; delivery-attempt audit read.
2. **Standard Webhooks compliance**: `webhook-id`, `webhook-timestamp`, `webhook-signature`
   headers; signed content `{id}.{timestamp}.{payload}`; HMAC-SHA256, `v1,`-prefixed
   base64 signature; per-endpoint secret with the `whsec_` encoding. Verify every detail
   against the spec repo — do not trust this paragraph's memory of it.
3. **A demo in the flags-demo shape** ([scripts/demo.sh:51](../../../scripts/demo.sh) run_go/
   run_typescript/run_python + [:76](../../../scripts/demo.sh) assert_result): boot the
   specimen, register three local receiver fixtures (one per language, each using the
   official Standard Webhooks verifier library), publish a message, assert all three
   receivers verified the signature and payload; then a tampered-signature rejection.
4. **Invariant probes in the invariants-harness shape** ([scripts/invariants.sh:35,57](../../../scripts/invariants.sh)
   boot/stop lifecycle): at-least-once (receiver 500s twice → delivery succeeds on retry
   and attempts are audited), disabled endpoint receives nothing, replay redelivers with
   the same `webhook-id`, restart durability (pending deliveries and audit survive a
   service restart), management/read token separation.
5. **WORK.md contract** for the work item (`make work` must pass; ≤120 lines).

## Explicitly NOT in scope

- **Factory packs for this capability.** [validate.go:36](../../../internal/factory/validate.go)
  pins `Capability != "feature-flags"` → reject; leave that pin. Factory-izing webhooks
  (a capability template axis, recipes, TS/Python service ports) is the follow-up work
  item, not this one.
- Inbound webhook receiving, transformations, fan-in, a UI, multi-tenant billing anything.
- Message-broker backends. SQLite first, same as flags; the store contract mirrors
  [internal/store/sqlite](../../../internal/store/sqlite/sqlite.go) (atomic write + audit
  in one transaction, optimistic revision where mutation exists).

## Phase-0 spike (decide before building)

Where the specimen lives. Recommendation: `specimens/webhook-delivery/` as its own Go
module inside this repo (the factory must eventually consume it as template source, so
adjacency wins over a sibling repo; the flags specimen stays at root untouched — moving it
to `specimens/flags/` for symmetry is a separate future migration). Confirm the root
`make check` can include the new module without disturbing the existing boundary scripts
([scripts/check-boundaries.sh:40](../../../scripts/check-boundaries.sh) greps module-pathed
imports — the new module needs its own equivalent checks, not a widened root grep). If the
multi-module friction turns out ugly, bail to a sibling-repo recommendation and stop for
an operator decision.

## Locked constraints (carried from repo law — not renegotiable)

1. Boundary law: policy package owns delivery decisions (which endpoints, retry schedule,
   signature input); transport owns HTTP; store owns persistence; composition root wires
   them ([CLAUDE.md](../../../CLAUDE.md) "Boundary law", mirrored at
   [internal/api/routes.go:53-59](../../../internal/api/routes.go) for the auth split).
2. No `else`, no vague filenames, guard clauses — enforced by root checks on any `.go` you
   add.
3. Separate management and read tokens; the audit actor comes from the configured server
   principal, never request JSON.
4. Append-only attempt audit written atomically with delivery state changes.
5. No call-home, no external network in demo or CI — receiver fixtures are local
   processes; the retry clock must be injectable so probes run in seconds, not minutes.
6. Every behavior ships with a positive test and a rejection/failure test; done means
   `make check` green and the demo green.

## Sharp edges / bail-points

- **The delivery engine is a background loop** — the first long-running concurrent
  component in the codebase. Keep it policy-driven (schedule computed in the policy
  package, executed by a mechanism worker); pin its states with an exhaustive-walk test
  (portfolio lesson: BFS state walks catch what coverage misses).
- **Signature bytes are the contract.** Get the signed-content bytes exactly right
  (`{id}.{timestamp}.{payload}` — raw payload bytes, not re-serialized JSON) or every
  verifier library rejects you. The demo's three real verifiers are the referee.
- **Timestamp tolerance and replay**: verifiers reject old timestamps; replayed messages
  need fresh timestamps but the same `webhook-id`. Read the spec's replay guidance.
- **Retry schedule is public behavior** — document it like the flags rollout hash
  (bounded attempts, capped backoff, terminal dead state visible in the audit).
- **Receiver fixtures** must be tiny servers using the *official* verifier libraries —
  that is the whole credibility claim. Pin library versions like `examples/` does.

## Definition of done

- Specimen boots via env config; management + delivery-read tokens separated; demo green
  with all three official verifiers accepting real deliveries and rejecting a tampered one.
- Invariant probes green: retry-after-failure, disabled-endpoint silence, replay identity,
  restart durability, token separation.
- Root `make check` green including the new module; WORK.md contract valid; README gains
  an honest one-paragraph mention of the second specimen and its boundary.
- No factory changes beyond (at most) documentation of the follow-up.

## Handoff prompt

See the paste-ready prompt in the session that produced this doc, or reconstruct: point at
this file, build the phase-0 decision first, then the specimen, then the demo, then the
invariant probes; stop at reviewed green PR (Codex via `@codex review`, two fix-rounds
max); gate/merge is the operator's session.
