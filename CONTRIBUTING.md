# Contributing to SaaS Reaper

SaaS Reaper should be easy to extend without making it easy to advertise a
half-built option. A choice is supported only when it is discoverable,
compatible, renderable, independently runnable, documented, and covered by the
product proof.

This is a compile-time factory, not a runtime plugin system. Contributors add
small, reviewable template packs to the repository. Generated customer services
never download third-party code from SaaS Reaper or call the factory at runtime.

## Start with a clean proof

Prerequisites are listed in [README.md](README.md). From a fresh clone, run:

```sh
make check
make product-demo
# or run both
make verify
```

`make check` is the fast correctness and policy floor. It runs formatting,
static analysis, strict Go linting, race-enabled Go tests, TypeScript checking,
Python compilation, package-boundary checks, skill-projection checks, and the
bounded domain-adaptation control.

`make product-demo` is the extension proof. It generates representative
repositories, compares deterministic output, tests ZIP delivery, compiles every
language/database family, validates deployment material, and runs live local
publish/evaluate demos.

Pull requests run those floors independently, plus a complete-history Gitleaks
scan. GitHub Actions are pinned to exact revisions and the workflow has
read-only repository permissions.

Keep both commands green. Do not weaken a check to land an option; fix the
option or make an explicit contract change.

## Know which product you are changing

The repository has two related surfaces:

- `cmd/reaper` and `internal/factory` are the factory customers use to select
  and generate a repository.
- The root `cmd/reaper-flags`, `internal/flags`, `internal/api`, and
  `internal/store` packages are the Go golden specimen used to establish the
  capability's behavioral boundary.

A generated language pack does not have to copy the golden specimen's internal
shape. It must preserve the public evaluation, revision, authentication, audit,
domain, and ownership invariants recorded in `README.md`, `REAPER.yaml`, and
`AGENTS.md`.

## The option-extension contract

The catalog is the public promise. Template directories are the implementation.
Tests require the two to agree exactly. `CatalogSchema` versions the JSON
contract used by the browser and external configurators; bump it for a breaking
shape or meaning change, not when merely adding a compatible choice.

| Dimension | Registration | Required implementation | Proof |
| --- | --- | --- | --- |
| Language | `languagePacks` in `catalog.go` | `templates/languages/<language>/base` plus one pack for every cataloged database | Catalog/template agreement, complete render matrix, generated checks, representative recipe |
| Database | `databasePacks` in `catalog.go` | `templates/languages/<language>/<database>` for every cataloged language | Complete render matrix and generated checks, plus pack-specific revision/durability evidence |
| Deployment | `deploymentPacks` in `catalog.go` | `templates/deployments/<deployment>` | Derived compatibility metadata, complete render matrix, provider formatter/schema or manifest validation |
| Delivery | `deliveryPacks` in `catalog.go` | Declared output artifacts plus one concrete publisher in `render.go` or `archive.go` | Every cataloged format must produce exactly its promised artifacts |

The executable contract lives in
`internal/factory/contract_test.go` and `internal/factory/factory_test.go`.
Adding only a catalog entry or only a directory fails locally and in CI.

The v0 automation proves registration, compatibility, deterministic rendering,
compilation, delivery, and representative live evaluation. It does not yet
provide one cross-language behavioral harness for every store/API invariant. A
new language or database must therefore add pack-specific evidence for stale
revisions, concurrent creation, restart durability, atomic audit writes,
authentication separation, and rollout hashing. Building that shared
conformance harness is the next extension milestone; do not describe the
current structural matrix as semantic proof.

Each rendered path has exactly one owning layer. The renderer rejects collisions
instead of letting a later database or deployment pack silently overwrite a
common or language file.

Increment a pack's version whenever its rendered content or compatibility
contract changes. Increment `FactoryVersion` whenever any generated output
changes, including common templates. The pair makes origin receipts meaningful.

Compatibility is part of registration. Database packs declare whether their
authority is shared, and deployment packs explicitly name the database
authorities they implement, whether they require a shared authority, plus their
replica defaults and ceilings. The factory derives validation, CLI choices, and
browser metadata from that one policy. A new shared database is not silently
assumed to work with PostgreSQL-shaped infrastructure. Do not add a second
compatibility table to a UI or template. Cross-field rules that do not fit those
declarations still belong in `internal/factory/validate.go`.

A deployment that supports any non-shared authority must declare both its
default and maximum as one replica. Validation independently enforces the same
database safety rule, so a mistaken deployment registration cannot advertise
unsafe horizontal replicas.

### Add a language

Use a lowercase path-safe value such as `ruby` or `rust`.

1. Add one labeled, described, versioned row to `languagePacks`.
2. Add `internal/factory/templates/languages/<language>/base/` containing the
   service, exact direct-dependency declarations, container build, and these
   stable hooks:
   `scripts/setup-language.sh`, `scripts/check-language.sh`, and
   `scripts/start-language.sh`. Shared setup, check, and demo scripts call those
   hooks; do not add another language branch to the common templates.
3. Add one concrete persistence pack under
   `languages/<language>/<database>/` for every database currently in the
   catalog. The v0 contract deliberately requires the full language/database
   matrix.
4. Keep policy in the flag package and mechanisms in API/store packages.
   Interfaces belong to the consumer. Avoid shared `types`, `model`, `utils`,
   or `service` buckets.
5. Add a recipe under `recipes/` and extend `scripts/product-demo.sh` so at
   least one local configuration runs end to end. Compile the other database
   family even when its live authority is unavailable locally.
6. Update the root README and `REAPER.yaml`, then bump `FactoryVersion` because
   generated receipts identify the template set.

Containers run as UID/GID `65532:65532` and expose the service on port `8080`.
Deployment packs own external health checks such as the ALB `/healthz` probe;
they must not switch on language-specific container tooling.

The hook contract is deliberately shell-sized:

| Hook | Invocation | Contract |
| --- | --- | --- |
| `setup-language.sh` | No arguments, repository root as working directory | Install the selected language/database dependencies; safe to rerun |
| `check-language.sh` | No arguments, repository root as working directory | Run the selected implementation's configured static analysis and tests, plus a non-mutating format check when the pack configures one; no source mutations |
| `start-language.sh` | First argument is a disposable work directory; repository root as working directory | Read `PORT`, token, and database environment; `exec` or block on the service until signaled |

All hooks use Bash with `set -euo pipefail`. They are invoked with `bash`, so
generated ZIPs do not depend on preserving executable mode bits.

The expected shape is conventional rather than framework-specific:

```text
templates/languages/<language>/
├── base/                  # API + flag policy + composition + build + hooks
├── sqlite/                # only the SQLite mechanism
└── postgres/              # only the PostgreSQL mechanism
```

### Add a database

1. Register it in `databasePacks`, including whether it is a shared authority.
2. Add one implementation pack for every cataloged language.
3. Give each adapter the language's stable composition entry point:
   `OpenFromEnvironment` in Go, `openAuthority` in TypeScript, and
   `open_authority` in Python. Keep that database's dependency manifest in the
   database layer; shared composition and setup templates must not branch on a
   database name.
4. Use deployment registration metadata for shared-authority and replica
   constraints. Add bespoke validation only for policy that cannot be expressed
   there.
5. Preserve optimistic revision checks and atomic definition-plus-audit writes.
   Prove that simultaneous first writes with expected revision zero produce one
   success and one conflict; a missing row cannot be protected by `FOR UPDATE`.
6. Add at least one representative recipe and proof path.

Do not introduce a universal storage abstraction owned by providers. Each
language should keep the smallest contract beside the code that consumes it.

### Add a deployment

1. Register it in `deploymentPacks` with its shared-authority requirement,
   exact supported databases, default replicas, maximum replicas, and pack
   version.
2. Add one language-neutral pack under `templates/deployments/`.
3. Add operational compatibility rules to `validate.go`.
4. Validate the rendered result with the provider's real formatter, schema
   tooling, or manifest decoder.
5. Add a representative recipe. Generation may emit infrastructure source but
   must never provision it.

### Add a delivery format

Delivery is not a template layer. Add a versioned `deliveryPacks` row that
declares whether it owns the directory and its exact archive suffix, then wire
it to a concrete publisher in `render.go` or `archive.go`. Add its exact behavior to
`assertDeliveryResult`. A cataloged delivery with no explicit assertion is a
test failure.

## Engineering bar

- Preserve line of sight: guards and early returns, no `else`, and nesting no
  deeper than two levels per scope.
- Keep policy separate from mechanisms and dependencies flowing inward.
- Use concrete names. Source files named `model`, `types`, `utils`, `helpers`,
  `common`, `service`, or `manager` are rejected.
- Wrap errors with useful operation context. Never discard security,
  persistence, archive, or network errors.
- Keep generated output deterministic. Time, random IDs, machine paths, and
  network lookups do not belong in rendering.
- Preserve customer ownership: no telemetry, callbacks, hosted dependency, or
  hidden provisioning.
- Add tests after the implementation. This project does not require a TDD
  ceremony, but it does require executable evidence for the completed option.

## Pull request shape

Keep one option or one contract change per pull request. In the description,
include:

- what customer choice changed;
- which compatibility decisions changed;
- one generated output path reviewers can inspect;
- the exact `make check` and `make product-demo` results;
- any infrastructure validation that could not run locally;
- whether `FactoryVersion`, README, recipes, and agent guidance changed.

Before requesting review, confirm:

- [ ] Catalog and template packs agree.
- [ ] Unsafe combinations fail before rendering.
- [ ] Generated repositories contain only selected mechanisms.
- [ ] Generated checks and the representative live demo pass.
- [ ] Output is deterministic and existing destinations are never overwritten.
- [ ] `AGENTS.md` and `CLAUDE.md` remain byte-identical where required.
- [ ] No external infrastructure was applied.
