<!-- reaper-work:v1 -->
# Work: Non-tunnel review fixes

Work-ID: non-tunnel-review-fixes
Status: active
Subject: git:6517797efde09c323f53c165899caf3bd15c5fbc
Stop-at: reviewed-change

## Outcome

Review the factory, flag service, and the four non-tunnel specimen heads;
fix reproduced defects with regression coverage and open a reviewable PR.
Record executable evidence separately from untested cloud deployment claims.

## Preserve

- Evaluation ordering, rollout hashing, targeting policy, and token separation.
- Independent specimen modules and their existing PR bases.
- Customer-selected deployment and language combinations.
- Preserve the merged tunnel work; do not provision cloud infrastructure.

## Change

- `internal/snapshot/memory.go`: preserve the newest committed revision.
- `internal/flags/`: expose captured-definition evaluation and regressions.
- `internal/api/`: evaluate one bulk snapshot and reject empty targeting keys.
- `internal/factory/`: quote attributes, reject unusable names, repair templates,
  bump receipts, and cover regressions.
- `README.md`: document deployment naming limits.
- `docs/non-tunnel-review.md`: record reviewed heads and proof boundaries.
- `WORK.md`: maintain this work record.

## Prove

- Green: make check, make demo, and make product-demo.
- Green: targeted regression tests pass under the race detector.
- Red: delayed revision 2 cannot overwrite projected revision 3.
- Red: bulk responses cannot reread a newer definition after listing.
- Red: inherited variant names, empty targeting keys, and invalid deployment
  names are rejected; quotes in recipe attributes cannot break Python imports.
- Red: generated Cloud Run configuration never declares a PORT environment
  variable; the container port remains configured.

## Stop

- The operator authorized merging PRs #12 and #13 on 2026-09-08.
- Merge only through the operator-issued grant and Gate-pinned command.
- Do not apply infrastructure or edit another session's checkout.

## Evidence

- Baseline make check and make product-demo passed.
- New regression tests reproduced publication ordering, bulk snapshot,
  empty targeting key, deployment-name, PORT, and attribute-encoding defects.
- Fixed targeted Go race tests and generated TypeScript tests pass.
- Final make check, make demo, and make product-demo pass.
- Detailed specimen heads, proof results, and limitations are recorded in
  docs/non-tunnel-review.md.

## Handoff

- Last: confirmed defects repaired and local proofs green; incident proof fix
  is PR #12, based on the existing incident branch.
- Next: revalidate the reconciliation with merged tunnel PRs #9–#11,
  refresh exact-head review, and merge through Gate.
