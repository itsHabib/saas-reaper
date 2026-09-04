# Agent operating guide

This repository contains the SaaS Reaper factory and three customer-owned golden
specimens: the root Go feature-flag service, the independent Go outbound
webhook-delivery module under `specimens/webhook-delivery/`, and the independent
Go notification-routing module under `specimens/notification-routing/`. The
factory still composes feature-flag services only. The proofs are intentionally
bounded; preserve their compatibility rules unless the operator explicitly
expands them.

`AGENTS.md` and `CLAUDE.md` are paired entrypoints. Keep them byte-identical.

## Start here

Read, in order:

1. `WORK.md` for the exact active outcome, subject, proof, and stop boundary.
2. `README.md` for the product boundary and runnable demo.
3. `REAPER.yaml` for selected and excluded capabilities.
4. `DOMAIN.md` for customer vocabulary and targeting-data policy.
5. The nearest package source and tests for the change.
6. The relevant repo skill under `skills/`.

Webhook specimen work also reads `specimens/webhook-delivery/README.md`, and
notification specimen work reads `specimens/notification-routing/README.md`.
Keep each module independent: do not add a root import, `go.work`, or a webhook
or notification capability to the factory as part of specimen maintenance.

`WORK.md` records intent and resumable state; it does not grant authority. Keep
it at or below 120 lines and run `make work` after changing it.

Factory work also reads `internal/factory/recipe.go`, `catalog.go`, and
`validate.go`. Templates are layered under `internal/factory/templates/` as
common knowledge, language packs, database packs, and deployment packs. A new
choice is supported only when it is cataloged, validated, rendered, and exercised
by the product demo.

`skills/` is canonical. `.agents/skills/` and `.claude/skills/` contain relative symlink projections for automatic discovery. Edit only the canonical skill and preserve both projections.

Use these commands:

```sh
make demo
make product-demo
make webhook-demo
make webhook-invariants
make notification-demo
make notification-invariants
make check
```

Do not claim completion unless `make check` passes. Run `make demo` after changes to evaluation, API, storage, examples, dependency versions, or startup.

Run `make product-demo` after changes to recipes, rendering, generated source,
archive delivery, or deployment packs. Generation must refuse existing output
paths and unsafe combinations; it must never apply external infrastructure.

Run both webhook proof commands after changes to webhook policy, signing,
transport, persistence, worker behavior, official verifier pins, or fixtures.
Their traffic must remain on loopback with an injectable retry clock.

Run both notification proof commands after changes to notification policy,
template rendering, either transport, persistence, worker behavior, or sink
fixtures. Their traffic must remain on loopback ports `1940x` with an injectable
retry clock.

## Boundary law

Dependencies flow toward `internal/flags`, never outward from it:

```text
management HTTP ──┐
                  ▼
              internal/flags ◄──── internal/store/*
                  ▲
OFREP HTTP ───────┤
                  │
                  └──── internal/snapshot
```

- `internal/flags` owns policy: valid definitions, domain-approved context, ordered rules, rollout semantics, revision conflicts, and the narrow interfaces it consumes.
- `internal/api` owns transport: authentication, decoding, route shape, OFREP translation, status codes, and encoding. It must not decide flag behavior.
- `internal/store/*` owns persistence mechanisms. A store does not decide whether a flag is valid or what an evaluation means.
- `internal/snapshot` owns the replaceable read projection. It is never the durable authority.
- `cmd/reaper-flags/main.go` is the composition root. Concrete mechanisms meet there, not inside policy.
- Interfaces live with the consumer. Do not create a provider, ports, abstractions, or shared-types package.

SQLite must commit a published definition and its audit entry in the same transaction. The snapshot is updated only after that commit succeeds. Startup reconstructs the snapshot from SQLite.

The webhook specimen keeps policy in `internal/delivery`, HTTP/API translation
in `internal/api`, outbound transport in `internal/transport/httpdelivery`,
persistence in `internal/store/sqlite`, and polling in `internal/worker`.
Signed deliveries use the exact stored payload bytes. Attempt audit insertion
and delivery state advancement are one SQLite transaction, and the audit is
append-only. Retry schedules are bounded; replay keeps the original message ID
and creates a fresh delivery identity.

The notification specimen keeps policy in `internal/routing`, HTTP/API
translation in `internal/api`, one wire protocol per transport package under
`internal/transport/`, persistence in `internal/store/sqlite`, and polling in
`internal/worker`. A transport receives an already-rendered, already-addressed
envelope and classifies its own failures; an error wrapping `ErrPermanent` ends
retries and every other error is transient. Retry schedules stay in policy,
computed outside the worker. One outcome-to-state table in
`routing.TransitionFor` is called by both policy and the store so the two cannot
drift. Every channel variant renders before anything is queued, so a missing
template variable rejects the whole send instead of delivering to some channels.
Attempt audit insertion and delivery state advancement are one SQLite
transaction, and the audit is append-only.

## Engineering principles

Dave Cheney lineage: simplicity, clarity, and line of sight.

1. No `else`. Handle errors and edge cases with guards and early returns; keep the happy path on the left margin.
2. Keep nesting to at most two levels per scope. Extract a named function when logic goes deeper.
3. Separate policy from mechanism. Validation, state transitions, and business rules stay above persistence, transport, and I/O.
4. Compose single-responsibility layers with one-way dependencies. A mechanism must be swappable without changing policy.
5. Keep APIs small and sharp. Export the least a caller needs, accept narrow inputs, return concrete results, and make names reveal intent.
6. Treat errors as values. Handle or wrap them explicitly. Prefer readable copying over premature abstraction.

For Go, accept small interfaces and return concrete implementations. Wrap errors with `%w`. For TypeScript and Python examples, use guard clauses, narrow public surfaces, and strict checking.

Never create vague source buckets. `scripts/check-boundaries.sh` rejects source files named `model`, `models`, `type`, `types`, `util`, `utils`, `helper`, `helpers`, `common`, `misc`, `service`, or `manager`. Name the file after the concept or operation it owns.

## Evaluation invariants

Evaluation order is public behavior:

1. Disabled returns the default variant with reason `DISABLED`.
2. Rules evaluate in declaration order; the first match wins with `TARGETING_MATCH`.
3. Rollout uses SHA-256 over `flag-key + NUL + targeting-value`, reads the first unsigned 64 bits in big-endian order, and maps them into 10,000 buckets.
4. A rollout match returns `SPLIT`; a miss returns the default with `STATIC`.

Do not change the hash input, byte order, bucket count, precedence, or reason mapping without an explicit compatibility decision and new golden migration evidence.

The POC accepts boolean and string variants only. Rule comparisons accept string context values only. `targetingKey` is mandatory under OFREP.

## Domain adaptation

The allowed targeting attributes in `internal/flags/context.go` are a privacy and product boundary, not merely a validation list. Do not add an attribute because it appears in a request. Use `skills/add-targeting-attribute/SKILL.md`.

For every targeting attribute:

- Name the customer concept.
- State why it affects product behavior.
- Define its representation and missing-value behavior.
- Classify whether it contains sensitive or personal data.
- Add positive and rejection tests.
- Update `DOMAIN.md` and `REAPER.yaml`.

Unknown context may travel through the protocol, but definitions cannot depend on it until the domain explicitly allows it.

## Authentication and authority

Management and evaluation tokens are separate. Possession of an evaluation token never grants write access. Do not add demo-token defaults to the server; the demo supplies explicit temporary values.

The management audit actor comes from the authenticated server principal, not request JSON. Preserve that boundary when replacing authentication: identity must be derived from verified credentials.

The webhook specimen likewise separates its management token from its
audit-read token. Neither token selects the audit actor, endpoint secrets are
never returned by the read surface, and disabling an endpoint prevents future
or already-queued sends.

The notification specimen separates the same two authorities. Neither token
selects the audit actor, the audit read never exposes a recipient address, a
webhook URL, or a relay host, and disabling a channel cancels its queued
deliveries in the same transaction that revisions it.

Agents may implement an operator-requested change and run validation. They may not silently broaden supported flag kinds, rule operators, targeting data, write authority, or excluded capabilities.

## Change recipes

- Domain vocabulary or targeting: `skills/onboard-domain/SKILL.md` and `skills/add-targeting-attribute/SKILL.md`.
- Persistence replacement: `skills/swap-storage/SKILL.md`.
- Reconstruct a decision without mutation: `skills/explain-evaluation/SKILL.md`.
- Evaluation policy: change `internal/flags`, add golden and adversarial cases, then run both `make check` and `make demo`.
- HTTP or OFREP translation: change `internal/api`; prove policy tests remain unchanged.
- Storage: change one `internal/store/<mechanism>`; run the store contract, restart, conflict, and atomic-audit tests.
- Webhook delivery: change only `specimens/webhook-delivery/`; run `make check`,
  `make webhook-demo`, and `make webhook-invariants` from the repository root.
- Notification routing: change only `specimens/notification-routing/`; run
  `make check`, `make notification-demo`, and `make notification-invariants`
  from the repository root.

## Done means evidence

A change is complete only when:

- The behavior is exercised by a focused positive test and a rejection, conflict, or failure test.
- `make check` passes, including race and boundary checks.
- `make demo` passes when a runnable surface changed.
- Both webhook proofs pass when the webhook specimen changed.
- Both notification proofs pass when the notification specimen changed.
- `WORK.md`, `AGENTS.md`, `CLAUDE.md`, `DOMAIN.md`, `REAPER.yaml`, and `README.md` remain consistent with the code.
- The diff contains no unrelated cleanup or speculative capability.
