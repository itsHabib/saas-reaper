<!-- reaper-work:v1 -->
# Work: Customer-owned audit ledger specimen

Work-ID: audit-ledger-specimen
Status: active
Subject: git:bb8061ec1eaf1184170900eb684aa875a3f1e6a8
Stop-at: reviewed-change

## Outcome

An in-repository, customer-owned Go audit-ledger specimen ingests events
idempotently, assigns gapless per-tenant sequences, links entries with a
SHA-256 hash chain inside one SQLite transaction, refuses mutation at the
database layer, scopes reads per tenant, and streams an export that an
independent Python verifier recomputes to the same head hash and in which it
locates any tampered row.

## Preserve

- The feature-flags and webhook-delivery specimens and factory output stay behaviorally unchanged.
- `internal/factory/validate.go` continues to reject every capability other than `feature-flags`.
- Ledger policy, HTTP transport, and SQLite persistence remain one-way layers with consumer-owned interfaces.
- Write and read credentials stay separate; the configured write principal supplies every entry's source.
- Demo and CI traffic stays on loopback ports 19601 and 19602 with no external infrastructure.

## Change

- `specimens/audit-ledger/`: add the independent Go module, Python verifier, fixtures, local demo, invariant probes, module lint config, and scoped checks.
- `scripts/setup.sh`: download the nested module's pinned dependencies.
- `scripts/check.sh`: include the nested module in the root verification floor.
- `.gitignore`: keep verifier bytecode out of the repository proof.
- `Makefile`: expose audit demo, invariant, and complete specimen proof targets.
- `.github/workflows/ci.yml`: cache the nested module and run the loopback-only audit proof job.
- `REAPER.yaml`: declare the third specimen in the capability manifest.
- `README.md`: describe the third specimen, its verifier proof, and its honest boundary.
- `AGENTS.md`: add the audit ledger paragraph to the paired agent guide.
- `CLAUDE.md`: add the byte-identical audit ledger paragraph.
- `WORK.md`: bind this exact task, proof, and stop boundary.

## Prove

- Green: `make check` passes with nested Go race tests, lint, Python unit tests, shell, and boundary checks.
- Green: `make audit-demo` shows the Python verifier's recomputed head equals the service head for two tenants.
- Green: `make audit-invariants` proves token separation, idempotent replay, atomic batches, tenant isolation, exact pagination, trigger-enforced append-only, and restart durability.
- Red: one edited export value breaks verification at exactly its sequence, a removed row surfaces as a gap, a replay with different content is refused, and `UPDATE` or `DELETE` on `entries` aborts.

## Stop

- Stop if the nested module requires root import coupling, a `go.work`, or factory capability changes.
- Stop before any non-loopback demo dependency, signing keys, notarization, Gate invocation, or merge.
- Stop after two review-fix rounds even if a broader architectural finding remains.

## Evidence

- Verified: nested `make check`, `make demo`, and `make invariants` pass locally.
- Verified: Go known-answer tests match hashes produced by the Python verifier without shared code.
- Verified: the nested check passes after rebasing onto the current webhook base.
- Pending: root and CI checks on the pushed rebased head.
- Reviewed: the first Codex round's exact-schema and integer-sequence findings
  and the second round's U+FFFD-domain and unreadable-input findings were each
  reproduced, fixed, and covered by regression tests.

## Handoff

- Last: rebased onto webhook head `bb8061e`, retained the audit-ledger work
  contract, and declared the specimen in `REAPER.yaml`.
- Next: push the rebased exact head, resolve the answered Codex threads after
  verification, and stop at the CI-green head without Gate or merge.
