<!-- reaper-work:v1 -->
# Work: Generated audit-read endpoint

Work-ID: generated-audit-read
Status: done
Subject: git:9f3df8f7be5f8f8bee8a5d3f9597a98a52c99ce0
Stop-at: reviewed-change

## Outcome

Every generated Go, TypeScript, and Python service serves its append-only
publication audit over `GET /v1/audit` behind the management token, with the
same clamp, ordering, and wire shape as the golden specimen, proven live by the
demo and the conformance harness.

## Preserve

- Publish and its audit row stay one transaction; the read never mutates.
- Management and evaluation tokens stay separate; an evaluation token can
  never read the audit.
- Postgres publish keeps the concurrent-create INSERT/UPDATE split.
- Deterministic generation, byte-identical `AGENTS.md`/`CLAUDE.md`, and the
  flat evaluation-context contract are untouched.

## Change

- `internal/factory/templates/languages/go/base/internal/flags/audit.go.tmpl`: AuditEntry and the limit clamp; TypeScript and Python flags packages mirror it.
- `internal/factory/templates/languages/go/base/internal/api/routes.go.tmpl`: audit route, handler, and Authority extension; TypeScript and Python routes and protocols mirror it.
- `internal/factory/templates/languages/go/sqlite/internal/store/sqlite/sqlite.go.tmpl`: newest-first audit query; the postgres store and both TypeScript and Python stores mirror it.
- `internal/factory/templates/common/scripts/demo.sh.tmpl`: demo reads the audit and proves the evaluation token is rejected.
- `scripts/conformance.sh`: harness asserts the audit entry and the 401 boundary against every generated SQLite service.
- `internal/factory/templates/languages/typescript/base/scripts/start-language.sh.tmpl`: exec node directly so a stop signal reaches the service and the demo port drains.
- `.github/workflows/ci.yml`: full-history checkouts so the work-contract validator resolves its git subject in CI.
- `internal/factory/render.go`: factory version 0.6.0; language packs v4 and database packs v3 in `catalog.go`.
- `README.md`: honest boundary narrows to listing and bulk; `REAPER.yaml`, `CONTRIBUTING.md`, and the generated README template record the served audit read.

## Prove

- Green: `make check` passes lint, race tests, boundaries, and domain controls.
- Green: `make product-demo` passes; every generated SQLite demo and the
  conformance harness read back exactly one audit entry with the configured
  actor and revision 1.
- Red: an evaluation token on `GET /v1/audit` receives 401 in every generated
  service; demo and harness both assert it.
- Red: a non-integer limit receives 400 in every generated service; the
  harness asserts it.

## Stop

- Stop before adding flag listing, bulk evaluation, filtering, or pagination
  beyond the single limit clamp.
- Stop if the read would require schema changes to the audit table.

## Evidence

- Verified: `make check` green at this head.
- Verified: `make product-demo` green, including audit assertions in three
  generated demos and three conformance runs.
- Verified: TypeScript and Python postgres packs type-check the new store
  method through the generated `make check` matrix.

## Handoff

- Last: factory 0.6.0 serves the generated audit read with token separation
  proven across all three language packs.
- Next: extend the conformance harness toward store and API invariants such as
  stale revisions, restart durability, and concurrent creation.
