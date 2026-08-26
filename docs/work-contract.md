# Work contract

`WORK.md` is the smallest durable packet a capable model needs to resume one
piece of work safely. It is state, not a tutorial and not an authority grant.

The contract deliberately excludes implementation choreography. Git owns the
change history, tests own executable behavior, repository checks own mechanical
policy, skills own exceptional transformations, and external gates own release
authority. Repeating those systems as prose makes instructions longer and less
reliable.

## Contract

A valid file is at most 120 lines and contains:

- `Work-ID`: stable lowercase identity;
- `Status`: `ready`, `active`, `blocked`, or `done`;
- `Subject`: an exact Git commit or immutable generation-recipe digest;
- `Stop-at`: `local-green`, `pr-ready`, `reviewed-change`, or
  `operator-decision`;
- one observable outcome;
- facts and invariants to preserve;
- exact paths permitted to change;
- at least one green proof and one deterministic red proof;
- conditions where the model must stop rather than reinterpret scope;
- evidence and a two-line last/next handoff.

The seven sections are fixed and ordered: Outcome, Preserve, Change, Prove,
Stop, Evidence, and Handoff. New workflow concepts do not belong in the file;
put enforceable behavior in a check or keep exceptional procedure in a skill.

## Exact subjects

Factory work binds to `git:<full-sha>`. The SHA must exist and remain an
ancestor of the current head, which makes the contract explicit about the code
state from which its decisions were made.

Fresh generated repositories may not have Git history, so their initial
contract binds to `recipe:sha256:<digest>`. The validator compares that digest
with the immutable `REAPER.lock.yaml` origin receipt. Customer work can adopt a
Git subject after the repository is initialized.

## Evidence boundary

`scripts/check-work.sh` validates structure, exact-subject identity, bounded
size, supported states, proof polarity, and resumable handoff fields. It emits
stable `work_contract:<code>` failures and offers JSON output through:

```sh
make work
```

The validator cannot establish that a human-authored evidence sentence is
true. Repository checks must execute the commands named by the contract. A
`done` contract rejects pending evidence and requires a `Verified` record, but
that record remains a claim until its referenced receipt or command result is
inspected.

## Lifecycle

1. Bind the contract to the exact starting subject.
2. Set one outcome and the smallest allowed change surface.
3. Name both the green behavior and the mutant or rejection that proves test
   sensitivity.
4. Work until the selected stop boundary or a Stop condition is reached.
5. Replace Handoff's Last and Next facts as state changes.
6. Mark `done` only after replacing pending evidence with exact verified
   results.

Do not turn `WORK.md` into a diary. Durable details belong in code, tests,
commits, and review artifacts; the contract should remain cheap enough that a
new frontier-model session reads all of it every time.
