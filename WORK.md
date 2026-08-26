<!-- reaper-work:v1 -->
# Work: OpenFeature flat-context conformance

Work-ID: openfeature-flat-context-conformance
Status: done
Subject: git:108eec5bbb183a6f3575b3c6b0f4b2d143c199e0
Stop-at: local-green

## Outcome

Generated Go, TypeScript, and Python services resolve dotted targeting
attributes exactly like the golden specimen — flat evaluation-context keys —
and a conformance harness in the product demo proves it with the real
OpenFeature clients so the drift cannot return.

## Preserve

- Rollout hash contract: SHA-256 over flag-key, NUL, targeting-value; first
  unsigned 64 bits big-endian; 10,000 buckets; unchanged reasons and precedence.
- `AGENTS.md` and `CLAUDE.md` byte-identical, root and generated.
- Deterministic generation that refuses existing output paths.
- No new flag kinds, rule operators, targeting attributes, or write authority.

## Change

- `internal/factory/templates/languages/go/base/internal/flags/evaluate.go.tmpl`: flat lookup, nested traversal removed.
- `internal/factory/templates/languages/typescript/base/src/flags/evaluate.ts.tmpl`: flat lookup, string-only comparison.
- `internal/factory/templates/languages/python/base/reaper_flags/flags/evaluate.py.tmpl`: flat lookup, string-only comparison.
- `internal/factory/templates/languages/go/base/internal/flags/evaluate_test.go.tmpl`: flat fixture plus nested-rejection guard; TypeScript and Python test templates mirror it.
- `internal/factory/templates/common/scripts/demo.sh.tmpl`: demo evaluates with the flat wire body.
- `internal/flags/evaluate.go`: specimen rejects an empty targetingKey, matching generated packs.
- `internal/api/evaluate.go`: bulk-evaluate ETag folds in the evaluation context so caches never cross contexts.
- `scripts/conformance.sh`: boots one generated service and drives the real Go, TypeScript, and Python OpenFeature clients through the specimen scenario table.
- `scripts/product-demo.sh`: runs the conformance harness against every generated SQLite service.
- `internal/factory/render.go`: factory version 0.5.0; language packs v3 in `catalog.go`.
- `README.md`: honest boundary names the generated endpoint gap; `REAPER.yaml`, `DOMAIN.md`, `CONTRIBUTING.md`, and the generated README and DOMAIN templates record the flat contract and the missing list, audit-read, and bulk endpoints.

## Prove

- Green: `make check` passes lint, race tests, boundaries, and domain controls.
- Green: `make product-demo` passes, including conformance runs where each
  generated SQLite service answers the real OpenFeature clients with the
  specimen's rule-match, rollout-match, and rollout-miss results.
- Red: a nested evaluation context no longer matches a dotted-attribute rule in
  the specimen or any generated pack; guard tests pin the miss.
- Red: a bulk-evaluate request reusing another context's ETag receives a fresh
  200 decision, never 304; the cross-context regression test pins it.

## Stop

- Stop before adding flag kinds, rule operators, attributes, or endpoints
  beyond audit-gap disclosure.
- Stop if conformance would need Docker or an external PostgreSQL service;
  SQLite is the live-runnable family.

## Evidence

- Verified: `make check` green at this head.
- Verified: `make product-demo` green, including three conformance harness runs.
- Verified: regenerated Go, TypeScript, and Python repositories pass their own
  `make check` and `make demo` with the flat fixtures.

## Handoff

- Last: factory 0.5.0 unifies the flat wire contract, adds the OpenFeature
  conformance harness, makes the bulk ETag context-aware, and disclosed the
  generated endpoint gap.
- Next: implement the generated audit-read endpoint, then extend conformance
  toward store and API invariants.
