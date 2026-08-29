<!-- reaper-work:v1 -->
# Work: Customer-owned webhook delivery specimen

Work-ID: webhook-delivery-specimen
Status: active
Subject: git:6517797efde09c323f53c165899caf3bd15c5fbc
Stop-at: reviewed-change

## Outcome

An in-repository, customer-owned Go webhook-delivery specimen registers and
disables endpoints, publishes exact payload bytes, signs Standard Webhooks
deliveries, retries failures on a bounded schedule, replays messages, and
persists an append-only attempt audit that official Go, JavaScript, and Python
verifiers accept through local runnable proofs.

## Preserve

- The feature-flags golden specimen and factory output stay behaviorally unchanged.
- `internal/factory/validate.go` continues to reject every capability other than `feature-flags`.
- Policy, HTTP transport, persistence, and worker mechanisms remain one-way layers with consumer-owned interfaces.
- Management and audit-read credentials stay separate; the configured principal supplies the audit actor.
- Demo and CI delivery traffic stays on loopback and no external infrastructure is provisioned.

## Change

- `specimens/webhook-delivery/`: add the independent Go module, official verifier fixtures, local demo, invariant probes, and scoped checks.
- `scripts/setup.sh`: install the nested specimen's pinned dependencies.
- `scripts/check.sh`: include the nested module in the root verification floor.
- `scripts/check-boundaries.sh`: exclude installed nested dependencies while continuing to scan authored specimen source.
- `.gitignore`: keep nested verifier installations and bytecode out of the repository proof.
- `Makefile`: expose webhook demo, invariant, and complete specimen proof targets.
- `.github/workflows/ci.yml`: run the local-only webhook interoperability and invariant proof.
- `README.md`: describe the second specimen, selection principle, runnable proof, and factory boundary honestly.
- `WORK.md`: bind this exact task, proof, and stop boundary.

## Prove

- Green: `make check` passes with nested Go race tests, lint, shell, TypeScript, Python, and boundary checks.
- Green: `make webhook-demo` shows all three official verifier libraries accept real deliveries and reject a tampered signature.
- Green: `make webhook-invariants` proves retry-after-failure, disabled silence, replay identity, restart durability, and token separation.
- Red: a changed payload or signature is rejected, failed sends are audited before retry, and a forced audit insert failure cannot advance delivery state.

## Stop

- Stop if the nested module requires root import coupling, a `go.work`, or factory capability changes.
- Stop before any non-loopback demo dependency, external infrastructure, Gate invocation, or merge.
- Stop after two review-fix rounds even if a broader architectural finding remains.

## Evidence

- Verified: root `make check`, `make demo`, and `make product-demo` pass.
- Verified: nested `make check`, `make demo`, and `make invariants` pass; all
  three official verifier libraries accept literal fixture bytes and reject
  same-length signature tampering.
- Verified: race tests cover both disable/send orderings, replay attribution
  across a principal change, owner-only database mode, atomic audit rollback,
  and destination-credential redaction from the read authority.
- Reviewed: five adversarial findings were reproduced and folded before PR.

## Handoff

- Last: implementation, rejection controls, local proofs, and the first
  adversarial fix pass are green without root-module or factory coupling.
- Next: open the PR, request `@codex review`, fold at most two verified fix
  rounds, and stop at the reviewed CI-green head without Gate or merge.
