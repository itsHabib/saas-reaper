<!-- reaper-work:v1 -->
# Work: Store invariant harness

Work-ID: store-invariant-harness
Status: done
Subject: git:761df689a09d634ae3cc7f60f8397def887eb117
Stop-at: reviewed-change

## Outcome

Every generated SQLite service is proven black-box, in the product demo, to
hold the store and API invariants the contribution contract lists: stale
revisions rejected, exactly one winner under concurrent creation, tokens
separated in both directions, one audit row per successful publish, and
definitions plus audit surviving a service restart on the same database.

## Preserve

- Templates and generated output unchanged; no factory or pack version moves.
- The conformance harness and its scenario table stay as they are.
- PostgreSQL packs remain compile-only in the demo with their white-box
  concurrent-create contract test.

## Change

- `scripts/invariants.sh`: boots one generated service, probes the revision,
  authority, and audit invariants, then restarts it on the same database and
  re-verifies.
- `scripts/product-demo.sh`: runs the invariant harness against every
  generated SQLite service after conformance.
- `CONTRIBUTING.md`: records the two harnesses and narrows the remaining
  milestone to container-backed stores.
- `README.md`: honest boundary describes both harnesses.

## Prove

- Green: `make check` passes lint, race tests, boundaries, and domain controls.
- Green: `make product-demo` passes with three conformance and three
  invariant-harness runs.
- Red: a stale expectedRevision receives 409; the losing concurrent creator
  receives 409; an evaluation token cannot publish and a management token
  cannot evaluate; the harness fails the run if any of these succeed.
- Red: a restart that loses the published definition or any audit row fails
  the harness.

## Stop

- Stop before booting docker-backed stores in the demo; that is the next
  milestone, not this one.
- Stop before adding new endpoints, flag kinds, or template changes.

## Evidence

- Verified: `make check` green at this head.
- Verified: `make product-demo` green, including invariant runs for the Go,
  TypeScript, and Python SQLite services.
- Verified: each invariant probe was exercised standalone against freshly
  generated services before wiring into the demo.

## Handoff

- Last: the product demo now proves evaluation semantics and store/API
  invariants for the live-runnable family.
- Next: choose the first NoSQL authority (DynamoDB via LocalStack, MongoDB,
  or Couchbase in docker) and run these same probes against it; Aurora
  PostgreSQL already works through the existing postgres pack.
