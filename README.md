# SaaS Reaper

SaaS Reaper composes customer choices into a complete, customer-owned SaaS
capability repository. This proof of concept now contains both sides of that
product: the configurator/generator and the original Go feature-flags golden
specimen used to establish the behavior and engineering boundary.

## Generate a product

Use the interactive configurator:

```sh
go run ./cmd/reaper new
```

Or open the local browser product and download the composed ZIP:

```sh
go run ./cmd/reaper serve
# open http://127.0.0.1:8090
```

Or generate deterministically from a recipe:

```sh
go run ./cmd/reaper catalog
go run ./cmd/reaper generate \
  --recipe recipes/typescript-sqlite-docker.yaml \
  --out /tmp/acme-flags
```

The customer chooses TypeScript or Python, SQLite or PostgreSQL, a deployment
pack, and directory/ZIP delivery. Reaper validates compatibility, renders only
the selected mechanisms, and returns an independent repository containing the
service, infrastructure, `AGENTS.md`, `CLAUDE.md`, `DOMAIN.md`, skills, receipt,
checks, and demo. Generation never provisions infrastructure and generated
services do not call home.

Supported deployment packs are Docker Compose, AWS ECS/Fargate, AWS EC2, GCP
Cloud Run, and Kubernetes/Kustomize. Managed and multi-replica targets require
PostgreSQL; unsafe SQLite combinations are rejected before rendering.

Run the factory proof:

```sh
make product-demo
```

## Golden specimen

The root Go service remains the first customer-owned specimen. It asks whether
a small SaaS capability can arrive runnable, understandable, and safe for an
agent to adapt to a customer's domain.

Prerequisites: Go 1.25 or newer, Node.js 24 or newer with npm, Python 3.11 or
newer, Bash, `curl`, `jq`, and `rsync`. The product demo also uses Docker Compose,
Terraform 1.8 or newer, and `unzip`. The golden demo binds loopback port `18080`
and uses a temporary database.

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

The factory currently offers a CLI and a local browser configurator, not a
hosted multi-user control plane. It generates TypeScript and Python service
packs; the Go implementation remains the golden specimen rather than a
selectable language pack. Cloud templates are delivered
for review but are never applied by Reaper. Managed PostgreSQL instances,
domains, certificates, container registries, and secret values remain explicit
customer inputs.

The generated capability does not yet include an admin UI, client-side/local
evaluation, approvals, analytics, an identity provider, per-environment
credential scopes, token rotation, or a general rule language. Those are
post-proof product questions, not hidden promises.
