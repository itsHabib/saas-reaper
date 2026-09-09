# Non-tunnel review — 2026-09-08

This was a source review and executable proof pass over the factory, the root
feature-flag service, and the four open non-tunnel specimens. It reproduced
bugs that the existing green suites did not cover. It was not an exhaustive
security audit, a production soak, or a cloud deployment.

## Subjects

| Surface | Reviewed base | Scope |
| --- | --- | --- |
| Factory and root flags | `6517797efde09c323f53c165899caf3bd15c5fbc` (`main`) | Recipe validation/rendering, generated policy/API/storage/deployment material, publication/projection, OFREP bulk evaluation |
| Webhook delivery, PR #5 | `bb8061ec1eaf1184170900eb684aa875a3f1e6a8` | Dispatch/retry/cancellation boundaries and complete specimen proof |
| Audit ledger, PR #6 | `6bd25850e858fe11b4fddf843e0a88b7aeaeecdb` | Canonical encoding/export and complete specimen proof |
| Notification routing, PR #7 | `87a0eb941cbef87e564c39d31f8f58b2230bad88` | Dispatch, preference/cancellation boundaries and complete specimen proof |
| Incident escalation, PR #8 | `32e1bd66476b87670d4660c93796fa6fe9129a23` | Timer/dispatch/on-call boundaries, checks and real Alertmanager proof |

Each specimen was tested from a separate copy of its remote branch, preserving
other sessions' checkouts. The fixes in this PR are based on `main`. The incident
demo repair is [PR #12](https://github.com/itsHabib/saas-reaper/pull/12), a follow-up to PR #8 because that module is not on `main`.
Tunnel PRs #9–#11 were excluded from this review.

## Confirmed defects and regressions

| Defect | Reproduction on the old code | Fix and regression |
| --- | --- | --- |
| P1: delayed publication regresses evaluations | Commit revision 2, delay its return, publish/project revision 3, then release revision 2: evaluation returns revision 2 until another publish or restart | Projection ignores older or equal revisions. `TestPublicationCompletionOrderCannotRegressEvaluation` uses a controlled interleaving, without sleeps. |
| P2: bulk response and ETag describe different snapshots | Advance the projection after `List`: the old handler tags revision 1 but returns revision 2 | Evaluate captured definitions without rereading the projection. `TestBulkEvaluationUsesTheListedSnapshot`. |
| P2: empty targeting key bypasses bulk validation | An empty environment with `targetingKey:""` returns 200 | Reject empty keys before cache handling or iteration. `TestBulkEvaluationRejectsEmptyTargetingKey`. |
| P2: generated TypeScript accepts nonexistent inherited variants | `constructor`, `toString`, and `__proto__` pass default/rule/rollout validation without declarations; an explicitly declared `__proto__` value is lost during copying | Use own-property checks and a dictionary without a prototype. Generated positive and rejection tests both fail on the old template and pass on the fixed one. |
| P1: recipe attributes become source syntax or change identity | A quote in an approved attribute produces a Python `SyntaxError`; Go's `\a` quoting is not portable to TypeScript | JSON string encoding shared by the language templates, including Python. A real generated Python import proves exact attribute round-trip; portable literal regression covers control characters and Unicode. |
| P1: Cloud Run template sets reserved `PORT` | Every generated Cloud Run pack explicitly sets the reserved variable; Terraform syntax validation does not establish API acceptance | Remove the environment override and keep `container_port = 8080`. `TestCloudRunLeavesPortToRuntime`. |
| P2: recipes accept unusable resource names | Trailing hyphens, ECS names over 32 characters or beginning `internal-`, and Cloud Run service-account names outside 6–30 characters pass validation | Reject before generation. Boundary tests preserve valid names and the complete language/database/deployment render matrix. |
| P2: incident paging proofs fail on alternating weeks | On 2026-09-09 UTC the fixed weekly schedule selects Grace; both demo and invariant harness wait for Ada's webhook, then time out | Separate incident follow-up anchors both paging rotations to their runs and asserts Ada is selected. Historical rotations/overrides remain covered by the invariant suite. |

The deployment constraints are checked against the providers' own contracts:
[AWS load-balancer names](https://docs.aws.amazon.com/elasticloadbalancing/latest/APIReference/API_CreateLoadBalancer.html),
[GCP service-account IDs](https://docs.cloud.google.com/iam/docs/service-accounts-create),
and [Cloud Run reserved variables](https://docs.cloud.google.com/run/docs/configuring/services/environment-variables#reserved_environment_variables).
These references support template corrections, not a claim that cloud apply succeeded.

## Executable evidence

The baseline `make check` and `make product-demo` passed before the new
regressions. The new regressions failed on the old behavior, then passed after
repair. Factory receipts advance to `0.8.1`; the affected language packs and
Cloud Run pack also advance their versions.

| Proof | Result |
| --- | --- |
| Main fixes: `make check` | Passed: strict lint, race tests, work contract, boundaries, skill projections and domain-adaptation control |
| Main fixes: `make demo` | Passed: root Go service evaluated by official Go, TypeScript and Python clients |
| Main fixes: `make product-demo` | Passed: generated services, cross-language conformance, SQLite invariants, shared-database checks, Terraform validation, Compose and Kustomize, CLI/browser ZIP delivery |
| Targeted Go race regressions | Passed |
| Generated TypeScript variant regressions | Passed; both new tests fail on the old template |
| Webhook `make setup verify` | Passed: three official language verifiers, signature tampering, retry, disable, replay, restart, token separation |
| Audit `make verify` | Passed: independent Python verifier, tampering/removal, replay/conflict atomicity, tenant scope, pagination, SQLite append-only triggers, restart |
| Notification `make verify` | Passed: real SMTP and local webhook payload receiver, render rejection, idempotency, preferences, retry, restart, token separation |
| Incident baseline static/race checks | Passed |
| Incident repaired `make incident-demo` | Passed: real Alertmanager 0.28.1, official signature verifier, Mailpit SMTP and resolve |
| Incident repaired `make incident-invariants` | Passed: all lifecycle, schedule, retry/audit, restart, redaction and authority probes |
| Incident follow-up root `make check` | Passed, including both nested modules |

Two environmental failures were corrected before assessing behavior: simultaneous
specimen linters contended on golangci-lint's shared lock (rerun sequentially),
and the local Colima daemon could not read a bind mount under `/tmp` (rerun under
`/Users/mh/dev`). The alternating-week incident failure persisted after fixing
the mount, then passed with the schedule repair.

## Remaining evidence limits

- Shared database packs were compiled/type-checked by the product matrix. No new
  live PostgreSQL, MongoDB, or Couchbase workload was run in this pass. Earlier
  documented container proofs are historical evidence, not refreshed results.
- Terraform init/validate, Compose configuration, and Kustomize rendering are
  local configuration checks. No AWS/GCP infrastructure was applied and no
  cloud IAM, private networking, billing, or production operation was proven.
- Notification webhook delivery used a local protocol receiver. It does not
  prove a hosted Slack workspace integration. SMTP and Alertmanager tests used
  real local implementations.
- The four independent specimen proofs do not prove an integrated product,
  multi-process operation beyond each documented boundary, long-running load,
  disaster recovery, or every failure interleaving.
- No merge or Gate invocation was performed. PR #5–#8 branch dependencies remain
  as they were; this review does not integrate their separate capabilities.
