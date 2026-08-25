# SaaS Reaper: feature-flags specimen

This repository is the customer-owned artifact for the first SaaS Reaper proof of concept. It is deliberately the specimen, not a generator: the experiment asks whether a small SaaS capability can arrive runnable, understandable, and safe for an agent to adapt to a customer's domain.

Prerequisites: Go 1.25 or newer, Node.js 20 or newer with npm, Python 3.10 or newer, Bash, `curl`, `jq`, and `rsync`. The demo binds loopback port `18080` and uses a temporary database.

Run the complete demo:

```sh
make demo
```

The command installs pinned Go, TypeScript, and Python dependencies, starts the appliance against a temporary SQLite database, publishes one flag, and proves the same OpenFeature evaluation from all three consumer languages for a rule match, rollout match, and rollout miss.

Run the validation floor:

```sh
make check
```

The validation includes a bounded domain-adaptation control: in a temporary copy, it follows the shipped skill to approve `organization.plan`, updates only domain policy/docs/tests, and proves the new targeting rule without coupling API or storage. The shipped specimen remains unchanged.

Agent skills have one canonical body under `skills/`. Relative symlinks project them into `.agents/skills/` for Codex and `.claude/skills/` for Claude; validation fails if either projection is missing or drifted.

## Boundaries

```text
management HTTP ──┐
                  ▼
              internal/flags ──► store contract ──► SQLite
                  │
OFREP HTTP ───────┤
                  ▼
             snapshot contract ──► memory projection
```

- `internal/flags` owns flag validation, ordered rule evaluation, deterministic rollout policy, revision conflicts, and the narrow interfaces it consumes.
- `internal/api` translates management JSON and OFREP 0.3.0 HTTP. It does not decide flag behavior.
- `internal/store/sqlite` persists authoritative definitions and the append-only audit in one transaction.
- `internal/snapshot` serves copied, already-validated flags from memory.
- `cmd/reaper-flags/main.go` is the composition root.

Packages name the responsibility they own. Files name concrete concepts or operations. `model.go`, `types.go`, `utils.go`, and similar buckets are rejected by `scripts/check-boundaries.sh`, which also rejects `else` and inward dependency leaks.

## Evaluation contract

The OFREP base URL is environment-scoped:

```text
http://host:port/environments/{environment}
```

The standard provider appends `/ofrep/v1/evaluate/flags/{key}`. Evaluation order is fixed:

1. A disabled flag returns its default variant.
2. Rules are checked in declaration order.
3. The optional rollout hashes `flag-key + NUL + targeting-value` with SHA-256 and maps the first unsigned 64 bits into 10,000 buckets.
4. A rollout miss returns the default variant.

The POC supports boolean and string flags, equality rules, `targetingKey`, and `organization.id`. The bounded surface and explicit exclusions are recorded in `REAPER.yaml`.

## Management contract

Publish a flag with optimistic concurrency:

```text
PUT /v1/environments/{environment}/flags/{key}
Authorization: Bearer $REAPER_ADMIN_TOKEN

{
  "expectedRevision": 0,
  "flag": { ... }
}
```

Creating requires revision `0`; updating requires the current revision. SQLite commits the new definition and its audit event atomically. The audit actor comes from the authenticated server principal in `REAPER_ADMIN_ACTOR`, never from request JSON. Evaluation uses a separate `REAPER_EVALUATION_TOKEN`.

## Honest POC boundary

This does not yet include a UI, client-side/local evaluation, multi-instance snapshot propagation, approvals, analytics, an identity provider, per-environment credential scopes, TLS termination, token rotation, a generator, or a general rule language. The POC's configured management principal and evaluation token each span every environment. Those are post-proof product questions, not hidden promises.
