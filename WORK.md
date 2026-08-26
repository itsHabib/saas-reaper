<!-- reaper-work:v1 -->
# Work: Frontier work contract

Work-ID: frontier-work-contract
Status: done
Subject: git:fab647fff28e4f4e2a49ce4807b3a283718cad18
Stop-at: local-green

## Outcome

Every SaaS Reaper repository carries a compact, exact-subject work contract
that a frontier model can resume without reloading a procedural manual.

## Preserve

- Generated repositories remain customer-owned and make no calls to Reaper.
- Policy stays separate from transport, persistence, deployment, and tooling.
- `AGENTS.md` and `CLAUDE.md` remain byte-identical stable law.
- Generation stays deterministic and refuses existing destinations.

## Change

- `WORK.md`: dogfood one bounded active work contract in the factory.
- `scripts/check-work.sh`: validate identity, state, proof, and handoff structure.
- `internal/factory/templates/common/`: render the contract and validator once for every stack.
- `internal/factory/`: prove valid output and deterministic red controls.
- `README.md`: publish the customer-facing artifact contract.
- `CONTRIBUTING.md`: preserve the extension and versioning boundary.
- `REAPER.yaml`: advertise work-contract knowledge in the factory manifest.

## Prove

- Green: `make check` accepts this contract and all focused validator cases.
- Green: `make product-demo` accepts generated Go, TypeScript, and Python repositories.
- Red: a changed recipe digest is rejected as `subject_mismatch`.
- Red: completed work with pending evidence is rejected as `done_needs_verified_evidence`.
- Red: a contract without an adversarial proof is rejected as `red_proof_missing`.
- Red: a Change item without one exact path is rejected as `change_path`.

## Stop

- Stop if validation needs a production service hook or language-specific implementation.
- Stop before adding orchestration, task dispatch, review judgment, or merge authority.
- Stop if the useful contract cannot remain at or below 120 lines.

## Evidence

- Verified: `make check` passed strict lint, race tests, boundaries, projections, and domain controls.
- Verified: `make product-demo` passed every representative generated stack and deployment proof.
- Verified: uncached focused tests killed subject, fake-done, missing-red, and broad-scope mutants.
- Verified: validator JSON passed an independent `jq` schema and verdict assertion.

## Handoff

- Last: factory version 0.4.0 now renders and verifies the bounded contract for every stack.
- Next: replace this completed contract when the next bounded work item starts.
