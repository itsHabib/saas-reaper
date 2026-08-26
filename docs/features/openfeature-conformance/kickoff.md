<!-- kickoff:distill -->
# Kickoff — OpenFeature conformance fixes for generated services

**Audience:** the implementing agent. Rip this and build — it is self-contained and
code-anchored. Trust the anchors below but re-open each file before you build on it (this
repo moves).

**Grounding:** distilled from a completed product/architecture/code review of HEAD
`108eec5`. Every finding here was reproduced live (services booted, real OpenFeature SDK
clients driven against them). Read first, in order: `README.md`, `CLAUDE.md`
(engineering + evaluation invariants), `CONTRIBUTING.md` (the option-extension contract),
`scripts/demo.sh` (the root cross-language proof you will mirror).

## Goal (one line)

Make the **generated** Go/TS/Python feature-flag services behave identically to the root
golden specimen for OpenFeature clients, and add a conformance harness that proves it in CI
so the drift cannot come back.

## In scope

1. **[CRITICAL] Unify the dotted-attribute wire contract to flat** across the root specimen
   and all three generated language packs.
2. **Build a cross-language conformance harness** that boots each generated service and
   drives the **real OpenFeature SDK** through the root demo's scenario table; wire it into
   `make product-demo`.
3. **Replace the nested test/demo fixtures** in the generated packs that currently hide the
   bug.
4. **[MEDIUM] Fix the root bulk-evaluate ETag** so it is context-aware (or remove bulk until
   it is).
5. **Decide list / audit-read / bulk endpoints for generated services** — implement, or
   disclose their absence in the generated README + `REAPER.yaml`. This one is a product
   call; make a recommendation and do the smaller half unless told otherwise.

## Explicitly NOT in scope (preserve these invariants)

- Do **not** broaden supported flag kinds, rule operators, targeting attributes, write
  authority, or excluded capabilities (`CLAUDE.md` "Authentication and authority").
- Do **not** change the rollout hash contract: SHA-256 over `flag-key + NUL +
  targeting-value`, first unsigned 64 bits big-endian, mod 10 000 (`CLAUDE.md` "Evaluation
  invariants"). It is already cross-language consistent — leave it alone.
- Do **not** touch the generation-determinism guarantees or the no-call-home / no-provision
  boundary. Both are verified-good.
- Keep `AGENTS.md` and `CLAUDE.md` byte-identical (root and generated).

## The bug, precisely (finding 1)

The one non-trivial approved attribute is `organization.id`. The two halves of the product
disagree on how to read it from an evaluation context:

- **Root specimen: flat key lookup** — [internal/flags/evaluate.go:60](../../../internal/flags/evaluate.go)
  (`context[rule.Attribute]`) and [:68](../../../internal/flags/evaluate.go) (rollout).
- **Generated packs: nested traversal, split on `.`** —
  [go evaluate.go.tmpl:34,51-61](../../../internal/factory/templates/languages/go/base/internal/flags/evaluate.go.tmpl),
  [ts evaluate.ts.tmpl:33,46-55](../../../internal/factory/templates/languages/typescript/base/src/flags/evaluate.ts.tmpl),
  [py evaluate.py.tmpl:35,47-53](../../../internal/factory/templates/languages/python/base/reaper_flags/flags/evaluate.py.tmpl).
- **The shipped OpenFeature clients send flat** —
  [examples/go/main.go:43](../../../examples/go/main.go) and
  [examples/python/client.py:29](../../../examples/python/client.py) both use
  `{"organization.id": "acme"}`.

Captured OpenFeature Go SDK wire body: `{"context":{"organization.id":"acme","targetingKey":"user-2"}}`.

Live result for a flag whose rule is `organization.id == acme → on`, same unmodified client:

| Target | Result |
|---|---|
| Root specimen | `on` / `TARGETING_MATCH` ✓ |
| Generated Go service | `off` / `STATIC` ✗ (silent wrong default) |
| Generated Python service | `off` / `STATIC` ✗ |

**Decision (recommended): unify on FLAT.** Flat is the only option that makes the shipped
OpenFeature clients work *and* matches the specimen; nested is non-standard and would force
rewriting the specimen + all three example clients. In each generated pack, replace the
`contextValue`/`context_value` nested helper with a direct flat lookup
(`context[attribute]` / `context.get(attribute)` / `context[attribute]`). Flat also handles
`targetingKey` (no dot) unchanged. If you believe nested is right, stop and raise it — it
contradicts the specimen and the clients, so it needs an explicit product decision, not a
silent choice.

Where the approved-attribute list is injected into templates (context, not the bug but
useful): [internal/factory/render.go:122-134](../../../internal/factory/render.go)
(`Attributes` is a comma-joined quoted list; `DOMAIN.md` targeting attributes flow in here).

## Why the tests didn't catch it (finding 2 — fix as part of 1)

The generated tests and demo hand-build the **nested** shape no real SDK sends, so they are
green *because* they avoid the real wire format:

- Generated demo publishes a rule on `organization.id` then evaluates with nested context —
  [common/scripts/demo.sh.tmpl:46](../../../internal/factory/templates/common/scripts/demo.sh.tmpl)
  (`"organization":{"id":"acme"}`).
- Generated Go evaluate test uses nested context —
  [go evaluate_test.go.tmpl:17](../../../internal/factory/templates/languages/go/base/internal/flags/evaluate_test.go.tmpl)
  (TS/Python tests mirror it).

After the flat fix, these fixtures must become flat (`{"organization.id":"acme"}`), or they
will fail — which is the point.

## The conformance harness (finding 1's permanent guard — the "feel confident" ask)

The root demo already proves cross-language conformance with **real** OpenFeature SDK
clients — [scripts/demo.sh:51-73](../../../scripts/demo.sh) (`run_go`/`run_typescript`/
`run_python`, each invoking `examples/<lang>`) driven through the scenario table at
[:98-100](../../../scripts/demo.sh). But it only ever runs against the **root** service.
`make product-demo` ([scripts/product-demo.sh](../../../scripts/product-demo.sh)) generates
services and runs their **own** `demo.sh` (raw `curl`, nested) — so no real client ever hits
a generated service.

Build a harness that, for each generated language × sqlite: generates the service, boots it,
publishes `fixtures/publish-checkout-v2.json`, then runs each shipped example client
(`examples/go`, `examples/typescript`, `examples/python`) **pointed at the generated
service's OFREP endpoint**, asserting the same three scenarios the root demo asserts (rule
match → `on`/`TARGETING_MATCH`, rollout match → `SPLIT`, rollout miss → `STATIC`). Wire it
into `product-demo.sh` so CI blocks on it. This is the structural→semantic upgrade
`CONTRIBUTING.md` explicitly says is the next milestone ("do not describe the current
structural matrix as semantic proof").

## Finding 4 — bulk-evaluate ETag is context-blind (root only, MEDIUM)

[internal/api/evaluate.go:60-64](../../../internal/api/evaluate.go) returns `304` when
`If-None-Match` equals `flagETag(listed)`, but `flagETag`
([:129-136](../../../internal/api/evaluate.go)) hashes only `key:revision` — not the context.
Reproduced: bulk-evaluate `organization.id=acme` → `value:true` + ETag; re-request
`organization.id=other` with that ETag → `304`, so a caching client reuses `true` when the
correct answer is `false`. Two tenants can receive each other's decisions. Fix: fold a hash
of the request context (or of the evaluated results) into the ETag. Note the generated packs
have **no** bulk endpoint, so this is specimen-only — but if you add bulk to generated
(finding 5), carry the fixed ETag, not this one. `TestOFREPBulkEvaluationUsesETag` only
covers the same-context happy path; add the cross-context case.

## Finding 5 — generated services drop endpoints the specimen has (product call)

Generated `Handler` registers only `healthz`, `PUT flags/{key}`, single evaluate —
[go routes.go.tmpl:63-75](../../../internal/factory/templates/languages/go/base/internal/api/routes.go.tmpl).
The specimen also has list, audit-read, and bulk —
[internal/api/routes.go:51-67](../../../internal/api/routes.go). Verified live: on a
generated service `GET …/flags`, `GET /v1/audit`, and bulk `POST …/evaluate/flags` all
return `404`. The audit table **is written** every publish but there is no API to read it.
Recommendation: at minimum add **audit-read** (an unreadable audit is a credibility problem
for a flag service) or state the omission in the generated `README.md.tmpl` honest-boundary
section and in `REAPER.yaml`. Do the smaller, honest half unless the operator wants the
endpoints.

## Definition of done

- All four implementations (specimen + 3 packs) resolve `organization.id` the same way, and
  the shipped OpenFeature Go/TS/Python clients return `on`/`TARGETING_MATCH` for
  `organization.id=acme` against **every generated** sqlite service.
- The conformance harness runs in `make product-demo` and fails if any generated service
  diverges from the scenario table under a real client.
- Generated demo + evaluate-test fixtures use the flat wire form.
- Bulk ETag is context-aware (or bulk is removed), with a cross-context regression test.
- Finding 5 resolved (endpoint added or absence disclosed).
- `make check` and `make product-demo` both green. `rg` must be on PATH or the work-contract
  tests fail with `rg: command not found` (`brew install ripgrep`).
- No unrelated capability expansion; `AGENTS.md`/`CLAUDE.md` still byte-identical; generation
  still deterministic (`TestGenerationIsDeterministic`).

## Sharp edges / bail-points

- **Bump `FactoryVersion`** ([render.go:16](../../../internal/factory/render.go)) — any change
  to generated output requires it, and pack versions in `catalog.go` if a pack's rendered
  content changes. `CONTRIBUTING.md` is strict about this; the origin-receipt tests depend on
  it.
- **The `else` ban and vague-filename ban** are enforced on generated code too
  (`scripts/check-boundaries.sh`, and the generated repo's own `check.sh`). Keep the flat-fix
  guard-clause-only.
- **Postgres packs must keep the concurrent-create conflict** (INSERT/UPDATE split, not
  upsert) — `TestPostgresPacksPreserveConcurrentCreateConflict` in
  `internal/factory/contract_test.go` will fail if you touch the store templates carelessly.
- **Harness runtime:** booting three services and running three real SDK clients each adds
  minutes to `product-demo`; scope it to sqlite (the live-runnable family) and keep the
  postgres packs at compile-only, matching the existing demo's split.
- **Secondary drift to optionally fold in:** root accepts an empty `targetingKey`
  ([evaluate.go:38](../../../internal/flags/evaluate.go)) while generated rejects it
  ([go evaluate.go.tmpl:12](../../../internal/factory/templates/languages/go/base/internal/flags/evaluate.go.tmpl)).
  Generated is the more-correct side; align the specimen if you're already touching evaluate.

## Handoff prompt

See the bottom of this session, or paste the block the operator has.
