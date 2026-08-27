<!-- reaper-work:v1 -->
# Work: Couchbase database pack

Work-ID: couchbase-database-pack
Status: active
Subject: git:a9258ba86b4248aeb4990f3f8c88ca052db566ec
Stop-at: reviewed-change

## Outcome

The factory catalogs a shared `couchbase` database authority with template
packs for Go, TypeScript, and Python that preserve optimistic revisions,
insert-based concurrent creation, atomic definition-plus-audit publication,
and restart durability, proven by the render matrix, the compose validation,
and the black-box invariant and conformance harnesses against a local
Couchbase container.

## Preserve

- SQLite and PostgreSQL packs, evaluation semantics, and API behavior stay
  byte-identical apart from pack-version metadata.
- The conformance and invariant harnesses stay unchanged; Couchbase must pass
  them as-is.
- Root `AGENTS.md` and `CLAUDE.md` remain byte-identical and untouched.

## Change

- `internal/factory/catalog.go`: register the shared `couchbase` pack, add it
  to every deployment carrying postgres, bump docker and aws-ec2 packs.
- `internal/factory/templates/languages/go/couchbase/`: Go store pack with
  gocb v2 and pinned manifests.
- `internal/factory/templates/languages/typescript/couchbase/`: TypeScript
  store pack with the couchbase SDK.
- `internal/factory/templates/languages/python/couchbase/`: Python store pack
  with the couchbase SDK.
- `internal/factory/templates/deployments/docker/`: couchbase compose service
  plus one-shot cluster-init.
- `internal/factory/templates/deployments/aws-ec2/`: shared authorities use
  the database-url secret branch.
- `internal/factory/contract_test.go`: pin the insert/exists-error conflict
  markers for all three couchbase stores.
- `internal/factory/render.go`: bump `FactoryVersion`.
- `REAPER.yaml`: catalog gains couchbase.
- `recipes/`: go, typescript, and python couchbase-docker recipes.
- `scripts/product-demo.sh`: generate, validate, and check couchbase repos.
- `CONTRIBUTING.md`: record the local container-evidence procedure.
- `WORK.md`: this contract.

## Prove

- Green: `make check` passes, including the new concurrent-create contract
  test and the full render matrix with couchbase rows.
- Green: `make product-demo` passes with couchbase compose validation and
  setup-plus-check runs for all three couchbase language packs.
- Green: `scripts/invariants.sh` and `scripts/conformance.sh` pass against a
  generated Go/couchbase service on a local Couchbase container.
- Red: a stale expectedRevision receives 409; the losing concurrent creator
  receives 409 from the insert exists-error, never an upsert; the harness
  fails if any audit row appears for a failed publish or is lost on restart.

## Stop

- Stop before booting Couchbase containers inside product-demo or CI; the
  container evidence stays a documented local procedure.
- Stop at a reviewed pull request; do not merge.

## Evidence

- Verified: `make check` green at this head.
- Verified: `make product-demo` green with couchbase compose validation and
  setup-plus-check for the go, typescript, and python couchbase repos.
- Verified: local Couchbase container runs green — invariants for generated
  go, typescript, and python services, conformance for go, and
  `DATABASE_URL=... make demo` for the generated go repository.

## Handoff

- Last: packs implemented, verification bar green, container evidence
  captured; the procedure is recorded in `CONTRIBUTING.md`.
- Next: open the pull request, gather review, and fold findings.
