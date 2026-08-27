<!-- reaper-work:v1 -->
# Work: MongoDB database pack

Work-ID: mongodb-database-pack
Status: active
Subject: git:a9258ba86b4248aeb4990f3f8c88ca052db566ec
Stop-at: reviewed-change

## Outcome

The catalog offers MongoDB as a shared database authority: every language pack
gains a mongodb store that preserves optimistic revisions, insert-only
concurrent creation, and one-transaction definition-plus-audit publishes, and
the docker deployment runs it as a single-node replica set.

## Preserve

- Evaluation order, rollout hashing, reason mapping, and the OFREP surface.
- SQLite and PostgreSQL packs and their harness results, byte for byte.
- Root `AGENTS.md` and `CLAUDE.md` byte-identical and unchanged.
- Deterministic rendering and refusal to overwrite existing destinations.

## Change

- `internal/factory/catalog.go`: mongodb databasePacks row (shared, v1) and
  deployment compatibility everywhere postgres is accepted.
- `internal/factory/templates/languages/go/mongodb/`: store, go.mod, go.sum.
- `internal/factory/templates/languages/typescript/mongodb/`: store, package.json.
- `internal/factory/templates/languages/python/mongodb/`: store, requirements.
- `internal/factory/templates/deployments/docker/deploy/docker/compose.yaml.tmpl`: replica-set mongodb service branch.
- `internal/factory/contract_test.go`: mongodb concurrent-create contract test.
- `internal/factory/render.go`: FactoryVersion 0.7.0.
- `scripts/product-demo.sh`: mongodb catalog assertion plus compile/check run.
- `recipes/go-mongodb-docker.yaml`: representative recipe.
- `docs/mongodb-live-proof.md`: container-backed harness recipe.
- `REAPER.yaml`: factory databases gain mongodb.

## Prove

- Green: `make check` passes at this head.
- Green: `make product-demo` passes, including the mongodb compile/check run.
- Green: `scripts/invariants.sh` and `scripts/conformance.sh` pass for the
  generated go, typescript, and python mongodb services against a disposable
  single-node replica set.
- Red: a stale expectedRevision receives 409; the losing concurrent creator
  receives 409; the contract test rejects upsert-shaped creates.

## Stop

- Stop before booting mongodb containers inside product-demo or CI.
- Stop before adding endpoints, flag kinds, or non-mongodb template changes.
- Stop at a reviewed pull request; merging is an operator decision.

## Evidence

- Verified: generated go, typescript, and python mongodb repositories pass
  `make setup check`.
- Verified: both harnesses green for all three languages against mongo:8.0
  running `--replSet rs0`; `make demo` green against the same container.

## Handoff

- Last: the mongodb pack is implemented with live container evidence recorded
  in `docs/mongodb-live-proof.md`.
- Next: fold review findings, then hand the merge decision to the operator.
