# Agent operating guide

This repository contains the SaaS Reaper factory and two customer-owned golden
specimens: the root Go feature-flag service and the independent Go
ingress-tunnel module under `specimens/ingress-tunnel/`. The factory still
composes feature-flag services only. The proofs are intentionally bounded;
preserve their compatibility rules unless the operator explicitly expands them.

`AGENTS.md` and `CLAUDE.md` are paired entrypoints. Keep them byte-identical.

## Start here

Read, in order:

1. `WORK.md` for the exact active outcome, subject, proof, and stop boundary.
2. `README.md` for the product boundary and runnable demo.
3. `REAPER.yaml` for selected and excluded capabilities.
4. `DOMAIN.md` for customer vocabulary and targeting-data policy.
5. The nearest package source and tests for the change.
6. The relevant repo skill under `skills/`.

Tunnel specimen work also reads `specimens/ingress-tunnel/README.md` and
`specimens/ingress-tunnel/deploy/aws/README.md`. Keep the module independent: do
not add a root import, `go.work`, or a tunnel capability to the factory as part
of specimen maintenance.

`WORK.md` records intent and resumable state; it does not grant authority. Keep
it at or below 120 lines and run `make work` after changing it.

Factory work also reads `internal/factory/recipe.go`, `catalog.go`, and
`validate.go`. Templates are layered under `internal/factory/templates/` as
common knowledge, language packs, database packs, and deployment packs. A new
choice is supported only when it is cataloged, validated, rendered, and exercised
by the product demo.

`skills/` is canonical. `.agents/skills/` and `.claude/skills/` contain relative symlink projections for automatic discovery. Edit only the canonical skill and preserve both projections.

Use these commands:

```sh
make demo
make product-demo
make tunnel-demo
make tunnel-invariants
make tunnel-deploy-check
make check
```

Do not claim completion unless `make check` passes. Run `make demo` after changes to evaluation, API, storage, examples, dependency versions, or startup.

Run `make product-demo` after changes to recipes, rendering, generated source,
archive delivery, or deployment packs. Generation must refuse existing output
paths and unsafe combinations; it must never apply external infrastructure.

Run all three tunnel proof commands after changes to tunnel policy, the link,
the edge, the agent, persistence, proof fixtures, or the AWS pack. Their traffic
must remain on loopback ports `1950x`, and the deployment pack is validated,
never applied.

## Boundary law

Dependencies flow toward `internal/flags`, never outward from it:

```text
management HTTP ──┐
                  ▼
              internal/flags ◄──── internal/store/*
                  ▲
OFREP HTTP ───────┤
                  │
                  └──── internal/snapshot
```

- `internal/flags` owns policy: valid definitions, domain-approved context, ordered rules, rollout semantics, revision conflicts, and the narrow interfaces it consumes.
- `internal/api` owns transport: authentication, decoding, route shape, OFREP translation, status codes, and encoding. It must not decide flag behavior.
- `internal/store/*` owns persistence mechanisms. A store does not decide whether a flag is valid or what an evaluation means.
- `internal/snapshot` owns the replaceable read projection. It is never the durable authority.
- `cmd/reaper-flags/main.go` is the composition root. Concrete mechanisms meet there, not inside policy.
- Interfaces live with the consumer. Do not create a provider, ports, abstractions, or shared-types package.

SQLite must commit a published definition and its audit entry in the same transaction. The snapshot is updated only after that commit succeeds. Startup reconstructs the snapshot from SQLite.

The tunnel specimen keeps policy in `internal/tunnel`, the WebSocket-plus-yamux
control link in `internal/link`, the public reverse-proxy edge in
`internal/edge`, the customer-side forwarder in `internal/agent`, management
and read HTTP in `internal/api`, and persistence in `internal/store/sqlite`.
Only `internal/link` may import the WebSocket or yamux libraries, and
`internal/metrics` may import only the observer contract the edge defines. The
diagnostics listener that serves metrics and gated pprof binds loopback only. One lifecycle
table in `tunnel.Transition` decides every status change and the audit rows
that record it; the exhaustive walk pins three reachable statuses and nine
edges. A claim insert or revoke commits with its audit rows in one SQLite
transaction; presence is in-memory and empties on restart by design. One mutex
in the service sequences every status change and the audit commits before the
routing table moves. Links are hijacked from net/http, so the accept handler
owns their lifetime and ends them all before the store closes. The edge opens
one fresh stream per request with keep-alives disabled so a pooled stream can
never outlive the link that owns it, and it answers an unclaimed and an offline
subdomain identically.

## Engineering principles

Dave Cheney lineage: simplicity, clarity, and line of sight.

1. No `else`. Handle errors and edge cases with guards and early returns; keep the happy path on the left margin.
2. Keep nesting to at most two levels per scope. Extract a named function when logic goes deeper.
3. Separate policy from mechanism. Validation, state transitions, and business rules stay above persistence, transport, and I/O.
4. Compose single-responsibility layers with one-way dependencies. A mechanism must be swappable without changing policy.
5. Keep APIs small and sharp. Export the least a caller needs, accept narrow inputs, return concrete results, and make names reveal intent.
6. Treat errors as values. Handle or wrap them explicitly. Prefer readable copying over premature abstraction.

For Go, accept small interfaces and return concrete implementations. Wrap errors with `%w`. For TypeScript and Python examples, use guard clauses, narrow public surfaces, and strict checking.

Never create vague source buckets. `scripts/check-boundaries.sh` rejects source files named `model`, `models`, `type`, `types`, `util`, `utils`, `helper`, `helpers`, `common`, `misc`, `service`, or `manager`. Name the file after the concept or operation it owns.

## Evaluation invariants

Evaluation order is public behavior:

1. Disabled returns the default variant with reason `DISABLED`.
2. Rules evaluate in declaration order; the first match wins with `TARGETING_MATCH`.
3. Rollout uses SHA-256 over `flag-key + NUL + targeting-value`, reads the first unsigned 64 bits in big-endian order, and maps them into 10,000 buckets.
4. A rollout match returns `SPLIT`; a miss returns the default with `STATIC`.

Do not change the hash input, byte order, bucket count, precedence, or reason mapping without an explicit compatibility decision and new golden migration evidence.

The POC accepts boolean and string variants only. Rule comparisons accept string context values only. `targetingKey` is mandatory under OFREP.

## Domain adaptation

The allowed targeting attributes in `internal/flags/context.go` are a privacy and product boundary, not merely a validation list. Do not add an attribute because it appears in a request. Use `skills/add-targeting-attribute/SKILL.md`.

For every targeting attribute:

- Name the customer concept.
- State why it affects product behavior.
- Define its representation and missing-value behavior.
- Classify whether it contains sensitive or personal data.
- Add positive and rejection tests.
- Update `DOMAIN.md` and `REAPER.yaml`.

Unknown context may travel through the protocol, but definitions cannot depend on it until the domain explicitly allows it.

## Authentication and authority

Management and evaluation tokens are separate. Possession of an evaluation token never grants write access. Do not add demo-token defaults to the server; the demo supplies explicit temporary values.

The management audit actor comes from the authenticated server principal, not request JSON. Preserve that boundary when replacing authentication: identity must be derived from verified credentials.

The tunnel specimen separates a management token, a read token, and per-claim
agent tokens. The agent token is shown once at claim time and only its hash is
stored; the read plane never returns credential material. A second agent with
the same credential supersedes the first, which is closed with WebSocket status
`4001` and must exit; a revoked claim closes its link with `4003` and its
credential never authenticates again.

Agents may implement an operator-requested change and run validation. They may not silently broaden supported flag kinds, rule operators, targeting data, write authority, or excluded capabilities.

## Change recipes

- Domain vocabulary or targeting: `skills/onboard-domain/SKILL.md` and `skills/add-targeting-attribute/SKILL.md`.
- Persistence replacement: `skills/swap-storage/SKILL.md`.
- Reconstruct a decision without mutation: `skills/explain-evaluation/SKILL.md`.
- Evaluation policy: change `internal/flags`, add golden and adversarial cases, then run both `make check` and `make demo`.
- HTTP or OFREP translation: change `internal/api`; prove policy tests remain unchanged.
- Storage: change one `internal/store/<mechanism>`; run the store contract, restart, conflict, and atomic-audit tests.
- Ingress tunnel: change only `specimens/ingress-tunnel/`; run `make check`,
  `make tunnel-demo`, `make tunnel-invariants`, and `make tunnel-deploy-check`
  from the repository root.

## Done means evidence

A change is complete only when:

- The behavior is exercised by a focused positive test and a rejection, conflict, or failure test.
- `make check` passes, including race and boundary checks.
- `make demo` passes when a runnable surface changed.
- All three tunnel proofs pass when the tunnel specimen changed.
- `WORK.md`, `AGENTS.md`, `CLAUDE.md`, `DOMAIN.md`, `REAPER.yaml`, and `README.md` remain consistent with the code.
- The diff contains no unrelated cleanup or speculative capability.
