# SaaS Reaper assurance stack kickoff

Audience: the implementing agent. Rip this and build; it is self-contained.

Status: READY at `ff88bb8` (`FactoryVersion = 0.3.0`). This is a build kickoff,
not a TDD. Verify every anchor against the live head before changing code.

## Goal

Turn SaaS Reaper's structural generation proof into bounded semantic assurance:
one black-box contract across generated Go, TypeScript, and Python services;
stateful persistence checks for SQLite and PostgreSQL; deterministic property
and mutation controls; and executable Quint specifications whose claims remain
strictly narrower than production verification.

## Scope

Build:

- a language-neutral conformance runner over the generated HTTP surface;
- a deterministic cross-language property corpus plus bounded native fuzzing;
- persistence probes for revision conflicts, concurrent creation, atomic audit,
  authenticated actor identity, and restart durability;
- a curated mutant pack with named red controls;
- Quint specifications for revision/audit and artifact-publication state
  machines, including deterministic counterexamples and trace replay;
- bounded PR checks and longer opt-in/nightly checks.

Do not build:

- a general testing platform or runtime plugin system;
- production-only test endpoints, fault-injection flags, or hidden backdoors;
- a generic mutation-score program across every dependency;
- cloud provisioning or claims that Terraform validation proves a live cloud;
- an unbounded proof of the entire generated service;
- Lean work unless a later task identifies a small pure law that bounded model
  checking and executable properties cannot cover;
- new flag kinds, rule operators, targeting attributes, auth capabilities, or
  deployment options.

## Why this is the next seam

The current factory proves registration, compatibility, deterministic
rendering, delivery, compilation, and representative live evaluation. It
explicitly does not claim cross-language semantic conformance
(`CONTRIBUTING.md:73-80`). The product matrix already generates and checks the
six language/database families and validates deployment material
(`scripts/product-demo.sh:28-104`), so the missing layer is reusable behavioral
evidence rather than more generation machinery.

The golden behavior is already bounded:

- evaluation order and rollout hashing are public invariants
  (`AGENTS.md:81-92`);
- management and evaluation authority are separate, and audit identity comes
  from the authenticated principal (`AGENTS.md:109-115`);
- the generated services share health, management-publish, and OFREP-evaluate
  routes (`internal/factory/templates/languages/go/base/internal/api/routes.go.tmpl:63-74`,
  `internal/factory/templates/languages/typescript/base/src/api/routes.ts.tmpl:15-40`,
  `internal/factory/templates/languages/python/base/reaper_flags/api/routes.py.tmpl:16-50`);
- every language pack exposes the same start hook
  (`internal/factory/contract_test.go:147-159`).

## Claim boundary

Use these words precisely:

- **render-verified**: the recipe was validated and generated deterministically;
- **build-verified**: the generated repository passed its native check hook;
- **contract-conformant**: the running service passed a named conformance corpus;
- **state-conformant**: the selected database passed revision, audit, conflict,
  and restart scenarios;
- **bounded-formal**: the named Quint invariants held over the declared finite
  state space;
- **deployment-smoke-tested**: a rendered deployment actually booted and passed
  the runtime corpus in the named environment.

Never collapse those into “bug free.” A bounded Quint check proves its
specification, not Go/TypeScript/Python, PostgreSQL, Docker, AWS, GCP, or
Kubernetes. Trace replay adds implementation evidence for those traces only.

## Locked decisions

1. The root golden specimen and documented invariants define intended behavior;
   majority agreement between generated languages does not.
2. Exercise generated services from outside their process. Test-owned database
   setup and inspection are allowed; production test hooks are not.
3. Keep the scenario format language-neutral and deterministic. A failure must
   print a complete replayable case, not only a random seed.
4. Keep policy in the conformance oracle and cases; startup, HTTP, database, and
   process control remain mechanisms.
5. Interfaces live with consumers. Do not create `model`, `types`, `utils`,
   `helpers`, `common`, `service`, or `manager` source files.
6. Curated semantic mutants are the primary mutation gate. A generic mutation
   percentage may be reported later but cannot replace named red controls.
7. Every assurance stream needs a green baseline and at least one deterministic
   red control that demonstrates sensitivity.
8. Formal specifications need source maps, bounded claims, and executable red
   counterexamples. They grant no release, merge, or runtime authority.
9. Pin new tool versions. Do not use `latest` in CI or generated receipts.
10. Do not mutate generated output without incrementing the affected pack
    version and `FactoryVersion` as required by `CONTRIBUTING.md:86-88`.

## Existing proof to preserve

- Catalog/template agreement and stable language hooks:
  `internal/factory/contract_test.go:25-99`.
- Current PostgreSQL first-create mutant guard:
  `internal/factory/contract_test.go:101-121`.
- Compatibility and single-authority replica laws:
  `internal/factory/contract_test.go:170-251`.
- Delivery behavior and collision refusal:
  `internal/factory/contract_test.go:253-281`.
- Concurrent generation and deterministic directory/ZIP output:
  `internal/factory/factory_test.go:91-168`.
- Complete compatible render matrix:
  `internal/factory/factory_test.go:171-176`.
- Golden SQLite conflict, restart, definition, and audit evidence:
  `internal/store/sqlite/sqlite_test.go:14-66`.
- Atomic staging and publication path:
  `internal/factory/render.go:39-75` and
  `internal/factory/render.go:141-191`.
- Current PR verification jobs:
  `.github/workflows/ci.yml:15-90`.

Do not replace these with the new harness. Compose the new proof on top.

## Work graph

```text
A. black-box contract
├── B. cross-language properties
└── C. persistence state proof
    ├── D. curated mutants        (also depends on B)
    └── E. Quint laws + replay
```

Land in the order `A -> B -> C -> D -> E`. B and C are conceptually
independent after A, but both will touch root commands and CI; sequential landing
keeps the diff and failure attribution clean.

## A. Black-box contract runner

### Outcome

Create one Go command that replays canonical HTTP scenarios against any running
generated service. Start by capturing the current Go/TypeScript/Python behavior
for the common route subset; where implementations differ, resolve against the
golden specimen and documented invariants rather than weakening the case.

### Planned files

- `cmd/reaper-conformance/main.go`
- `internal/conformance/scenario.go`
- `internal/conformance/http_step.go`
- `internal/conformance/report.go`
- `assurance/cases/evaluation.jsonl`
- `scripts/run-conformance.sh`
- `Makefile`
- `README.md`

### Contract shape

Each JSONL record has a stable `id`, setup steps, one or more HTTP actions, and
exact observable expectations. Normalize JSON before comparison; never compare
map serialization order. Reports sort by implementation, database, and case ID
and include `FactoryVersion`, pack versions, recipe digest, case-corpus digest,
and verdict. Do not include wall-clock time or temporary paths in semantic
identity.

The first corpus covers:

- health and unknown route;
- missing, malformed, wrong-role, and correct bearer tokens;
- create at expected revision zero;
- update at the current revision;
- stale update conflict;
- invalid path segments, definitions, and request JSON;
- disabled, first-rule, rollout-hit, rollout-miss, and missing-targeting-key
  evaluation;
- missing flag and revision metadata.

### Green and red controls

- Green: generated Go/SQLite passes the complete fixed corpus.
- Red: change one expected decision reason in a temporary corpus copy; the
  runner fails and names the case plus actual/expected normalized response.
- Red: invoke management with the evaluation token; the runner requires an
  unauthorized response.

### Acceptance

```sh
make conformance
make check
make product-demo
```

`make conformance` must be rerunnable, leave no processes or artifacts behind,
and fail if a case is skipped. Existing generated demos continue to pass.

### Out of scope

Database inspection, generated property cases, fuzzing, mutation patches, and
formal specifications belong to later units.

## B. Cross-language properties and differential corpus

### Outcome

Generate deterministic valid and invalid flag/context cases, evaluate them
against an assurance-only oracle derived from `AGENTS.md:81-92`, and replay the
same cases against generated Go, TypeScript, and Python SQLite services.

### Planned files

- `internal/conformance/oracle.go`
- `internal/conformance/property_case.go`
- `internal/conformance/property_case_test.go`
- `internal/conformance/fuzz_test.go`
- `assurance/cases/property-seeds.jsonl`
- `scripts/check-properties.sh`
- `Makefile`
- `.github/workflows/ci.yml`

The oracle is assurance code only. Production packages and generated templates
must not import it.

### Required properties

- repeated evaluation is deterministic;
- JSON object key order does not affect a decision;
- adding irrelevant or unapproved context cannot change a decision;
- first matching rule dominates later matching rules;
- increasing a single-variant rollout threshold cannot exclude a subject that
  previously matched;
- encode/decode preserves accepted flag semantics;
- malformed flags and contexts are rejected without panics;
- Go, TypeScript, Python, and the oracle agree on value, variant, reason, key,
  and revision metadata;
- identical property seeds produce byte-identical case corpora.

Keep bounded deterministic seeds in PR CI. Run longer coverage-guided fuzzing
outside the ordinary PR path. Any discovered failure must be minimized into a
committed seed before the fix is considered complete.

### Green and red controls

- Green: every language agrees with the oracle for the committed seed corpus.
- Red: reverse rule precedence in a temporary generated template; the
  first-rule property fails.
- Red: change rollout byte order or omit the NUL separator; stable rollout
  fixtures fail with a replayable record.

### Acceptance

```sh
make properties
go test ./internal/conformance/...
make verify
```

The bounded PR run has a fixed case count and seed. A separate documented
command accepts a duration for local/nightly fuzzing.

### Out of scope

Do not add more rule operators or attempt statistical validation of hash
uniformity. The contract is deterministic bucketing, not experimental-quality
randomness.

## C. Persistence state proof

### Outcome

Run the same stateful publication scenarios against all six generated
language/database families. Use ordinary generated startup hooks, a temporary
SQLite file, and a disposable PostgreSQL instance. Observe audit state directly
through test-owned SQL because generated services do not expose an audit API.

### Planned files

- `internal/conformance/persistence.go`
- `internal/conformance/database_probe.go`
- `internal/conformance/process.go`
- `assurance/cases/persistence.jsonl`
- `scripts/check-persistence.sh`
- `scripts/conformance-matrix.sh`
- `Makefile`
- `.github/workflows/ci.yml`

Start hooks already exist for all three languages; use them rather than adding
language branches to shared generated templates. The current hook entrypoints
are:

- Go: `internal/factory/templates/languages/go/base/scripts/start-language.sh.tmpl:4-6`
- TypeScript: `internal/factory/templates/languages/typescript/base/scripts/start-language.sh.tmpl:4-5`
- Python: `internal/factory/templates/languages/python/base/scripts/start-language.sh.tmpl:4-5`

### Required scenarios

- create at revision zero, then update at revision one;
- stale create and stale update leave definition and audit unchanged;
- simultaneous first creates produce exactly one success and one conflict;
- simultaneous same-revision updates produce exactly one success;
- one successful revision creates exactly one audit event with the configured
  principal as actor;
- a database-owned trigger that rejects audit insertion causes publication to
  fail and rolls back the definition;
- restart against the same authority preserves the latest definition and
  revision;
- SQLite trials never run multiple service replicas;
- PostgreSQL trials use an isolated disposable database per implementation.

The PostgreSQL templates already separate first `INSERT` from update and map
unique creation conflicts:

- Go: `internal/factory/templates/languages/go/postgres/internal/store/postgres/postgres.go.tmpl:133-194`
- TypeScript: `internal/factory/templates/languages/typescript/postgres/src/store/postgres.ts.tmpl:56-82`
- Python: `internal/factory/templates/languages/python/postgres/reaper_flags/store/postgres.py.tmpl:64-114`

The new proof must exercise that behavior live; source-string inspection remains
only a fast structural guard.

### Green and red controls

- Green: six language/database families pass every applicable state scenario.
- Red: restore `ON CONFLICT DO UPDATE` for first creation in a temporary
  PostgreSQL pack; the concurrent-create scenario fails.
- Red: move audit insertion outside the transaction; the forced-audit-failure
  scenario finds a definition without a matching audit event.

### Acceptance

```sh
make persistence
make conformance
make verify
```

The run prints exact implementation/database/case failures and always removes
containers, processes, temporary databases, and generated repositories.

### Bail point

If an invariant cannot be observed without a production test hook, stop. Add a
test-owned database probe or narrow the claim; do not add a hidden endpoint or
environment switch to generated services.

## D. Curated semantic mutant pack

### Outcome

Turn the known failure modes into a deterministic adversarial gate. Every mutant
has one owner proof and one expected failure signature. The baseline runs first;
a surviving mutant is a hard failure.

### Planned files

- `assurance/mutants/manifest.json`
- `assurance/mutants/M01-postgres-upsert.patch`
- `assurance/mutants/M02-rule-order.patch`
- `assurance/mutants/M03-rollout-input.patch`
- `assurance/mutants/M04-token-role.patch`
- `assurance/mutants/M05-audit-actor.patch`
- `assurance/mutants/M06-split-audit-transaction.patch`
- `assurance/mutants/M07-sqlite-replicas.patch`
- `assurance/mutants/M08-delete-winning-artifacts.patch`
- `assurance/mutants/M09-compose-port-owner.patch`
- `scripts/run-mutants.sh`
- `Makefile`
- `.github/workflows/ci.yml`

Apply mutants only inside a validated temporary copy of the exact current head.
Never patch the operator's checkout. Each manifest record names the patch,
proof command, expected failing case, and bounded timeout. Reject unknown fields,
duplicate IDs, missing patches, unexpected baseline failures, timeouts, and
surviving mutants.

### Required mutant ownership

| Mutant | Proof that must kill it |
| --- | --- |
| PostgreSQL first-create upsert | persistence concurrent-create case |
| Reverse declaration-order rules | first-rule property |
| Change rollout hash input or byte order | stable rollout property |
| Accept evaluation token for management | auth-role conformance case |
| Take audit actor from request JSON | principal-identity persistence case |
| Commit audit separately | forced-audit-failure case |
| Permit SQLite replicas greater than one | catalog/validation contract test |
| Losing generator deletes winner | concurrent generation test |
| Expose port 8080 on PostgreSQL | Compose ownership assertion |

### Acceptance

```sh
make mutants
make verify
```

The report shows baseline green, each mutant red, the owning proof, and no
survivors. Keep critical fast mutants in PR CI; run the complete pack in a
separate bounded job if total CI time would exceed the existing 20-minute
product-proof budget.

### Out of scope

A generic source mutation engine may be evaluated later. Do not optimize for a
mutation percentage before the named semantic mutants are reliable.

## E. Quint laws and implementation trace replay

### Outcome

Write two small executable specifications: one for revision/audit state and one
for concurrent artifact publication. Check their bounded invariants, retain
deterministic red counterexamples for broken variants, and replay selected traces
through the executable conformance layer.

### Planned files

- `assurance/formal/revision-audit.qnt`
- `assurance/formal/artifact-publication.qnt`
- `assurance/formal/revision-upsert.qnt`
- `assurance/formal/publication-delete-winner.qnt`
- `assurance/formal/claims.md`
- `assurance/formal/source-map.md`
- `assurance/formal/traces/`
- `scripts/check-formal.sh`
- `scripts/replay-formal-traces.sh`
- `Makefile`
- `.github/workflows/ci.yml`

Pin the Quint toolchain after checking its current supported CLI. Do not use an
unpinned global install or `latest` in the verification path.

### Revision/audit state

Represent committed flags, revisions, audit events, authenticated principals,
and competing writers. Include create, update, stale write, audit failure,
commit, and abort transitions.

Required invariants:

- at most one create from expected revision zero commits;
- every committed update advances exactly one revision;
- stale writes do not change durable state;
- every committed `(environment, key, revision)` has exactly one matching audit
  event;
- no audit exists for an uncommitted revision;
- audit actor equals the authenticated principal for that transition.

### Artifact-publication state

Represent absent, staged, archive-published, directory-published, failed, and
complete states for two competing generators with distinct recipe digests.

Required invariants:

- existing destination artifacts are never overwritten;
- exactly one competing generator may report success for one destination;
- a losing generator never deletes a winner's directory or archive;
- successful `both` output has one recipe digest across directory and ZIP;
- failure never reports a complete result.

### Red controls and trace bridge

- `revision-upsert.qnt` must produce a stable trace where two revision-zero
  creates commit.
- `publication-delete-winner.qnt` must produce a stable trace where loser
  cleanup removes winning output.
- Normalize and commit the smallest counterexample traces.
- Map every state/action/invariant to implementation anchors in
  `source-map.md`.
- Replay selected revision/audit traces through the stateful conformance runner
  and artifact traces through `factory.Generate` tests.

### Acceptance

```sh
make formal
make persistence
make mutants
make verify
```

`claims.md` states the checked bounds, tool version, invariant names, red
controls, trace-replay coverage, and explicit non-claims. The formal job fails
on parse/type errors, invariant violations in the good specifications, missing
counterexamples in the bad specifications, stale source-map anchors, or trace
replay mismatch.

### Bail point

If the desired property requires an unbounded theorem, record the exact law and
park a narrowly scoped Lean follow-up. Do not expand this work unit or translate
the whole service into a prover.

## Aggregate commands and CI shape

Add these compositional targets:

```make
conformance
properties
persistence
mutants
formal
assurance: conformance properties persistence mutants formal
```

Keep `verify` as the existing product floor. Do not silently make an expensive
nightly matrix part of every local `make check` invocation.

PR gate:

- fixed conformance corpus;
- deterministic property seeds;
- persistence state matrix;
- critical named mutants;
- bounded Quint checks and red counterexamples;
- existing `make verify`.

Long/manual or nightly gate:

- duration-based native fuzzing;
- complete mutant pack;
- expanded Quint bounds;
- repeated concurrency stress;
- optional local Kubernetes smoke test.

Live AWS/GCP smoke deployment remains a separately authorized release proof.
Rendering or Terraform validation cannot claim it.

## Global definition of done

The assurance stack is complete when:

1. Every supported Go/TypeScript/Python plus SQLite/PostgreSQL family passes the
   applicable fixed behavioral and state corpus.
2. Cross-language properties agree with the independent assurance oracle.
3. Every named mutant is killed by its owning proof and the baseline stays
   green.
4. Both Quint specifications pass at documented bounds; both broken variants
   produce deterministic red traces; selected traces replay successfully.
5. Reports identify exact factory, pack, recipe, and corpus versions without
   temporary-path or wall-clock identity.
6. `make verify`, `make conformance`, `make properties`, `make persistence`,
   `make mutants`, and `make formal` pass from a clean checkout.
7. `README.md` and `CONTRIBUTING.md` state the new claims without saying “bug
   free” or treating formal evidence as production verification.
8. Any generated template repair increments its owning pack and factory version.
9. No test processes, containers, temporary repositories, or database state
   survive a successful or failed run.

## Stop conditions

- Do not weaken an invariant because one generated language currently fails it.
  Either fix the pack or surface a product-contract decision.
- Do not accept a mutant with “the test fails somewhere.” Require the named
  owning proof and failure signature.
- Do not keep a flaky concurrency test. Add synchronization at the test-owned
  orchestration/database boundary until the red and green controls are stable.
- Do not commit large random corpora, runtime databases, temporary generated
  repositories, or unnormalized formal traces.
- Do not provision external infrastructure or spend cloud credentials without
  a separate operator-authorized task.
- Stop and split the work if any unit exceeds one reviewable PR or needs a
  product capability decision outside the current feature-flag contract.

## Ready-to-paste handoff prompt

Work from `docs/features/assurance-stack/kickoff.md`. Trust its anchors as the
starting map, but verify them against the live head before editing. Build the
SaaS Reaper assurance stack in the documented A→E order, starting with unit A's
black-box contract runner and its explicit green/red controls. Preserve the
existing factory, policy/mechanism boundaries, Dave Cheney line-of-sight rules,
and current product capability; do not add runtime test hooks, cloud
provisioning, generic mutation-score machinery, or broader feature-flag
semantics. Keep each work unit reviewable and run its acceptance commands plus
`make verify`. Completion means the fixed conformance and persistence matrices
pass, every named mutant is killed, the bounded Quint good/bad controls behave
as claimed, selected traces replay, docs state exact claim boundaries, and the
worktree is clean.
