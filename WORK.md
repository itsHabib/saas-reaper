<!-- reaper-work:v1 -->
# Work: Customer-owned incident escalation specimen

Work-ID: incident-escalation-specimen
Status: active
Subject: git:9334a0bbc99df69dc19abd834249ee89c23cb57e
Stop-at: reviewed-change

## Outcome

An in-repository, customer-owned Go incident-escalation specimen accepts
PagerDuty Events API v2 events, deduplicates them into incidents, climbs a
declared escalation ladder on a durable timer that survives restarts, resolves
on-call responders from declared rotations and overrides, pages them over signed
webhooks and SMTP with a bounded retry schedule and append-only audit, and
proves its compatibility against a real, unmodified Prometheus Alertmanager.

## Preserve

- The feature-flags golden specimen, the webhook-delivery specimen, and factory output stay behaviorally unchanged.
- `internal/factory/validate.go` continues to reject every capability other than `feature-flags`.
- Policy, on-call resolution, HTTP transport, persistence, and worker mechanisms remain one-way layers with consumer-owned interfaces.
- Management and incident-read credentials stay separate; the audit actor comes from the server principal, the routing key, or the timer, never request JSON.
- Proof traffic stays on the local machine: invariants on loopback, the demo on one private container network with no published port.

## Change

- `specimens/incident-escalation/`: add the independent Go module, on-call schedule format, durable escalation timer, notification transports, container-native Alertmanager demo, invariant probes, module lint config, and scoped checks.
- `scripts/setup.sh`: install the new specimen's pinned dependencies.
- `scripts/check.sh`: include the new module in the root verification floor.
- `Makefile`: expose incident demo, invariant, and complete specimen proof targets.
- `.gitignore`: keep the demo's container scratch directory out of the repository proof.
- `.github/workflows/ci.yml`: run the Docker-backed compatibility proof and the loopback invariants in a dedicated job.
- `README.md`: describe the third specimen, its Alertmanager proof, and its documented gaps.
- `REAPER.yaml`: declare the third specimen in the capability manifest.
- `AGENTS.md`: record the third specimen's boundary law, authority split, proof commands, and change recipe.
- `CLAUDE.md`: keep the paired agent guide byte-identical with `AGENTS.md`.
- `WORK.md`: bind this exact task, proof, and stop boundary.

## Prove

- Green: `make check` passes with the new module's race tests, strict lint, shell format, shellcheck, and boundary checks.
- Green: `make incident-demo` shows an unmodified Alertmanager opening, deduplicating, and resolving an incident, with the official Standard Webhooks verifier accepting the signed page and a real SMTP server receiving the email.
- Green: `make incident-invariants` proves dedup, schedule overrides, timed escalation, timer survival across a restart, acknowledge, resolve, token separation, and one audit row per attempt.
- Red: a tampered signature is rejected by the official verifier, a duplicate dedup key opens no second incident, an acknowledged incident never escalates, each token is refused the other's route, and a failed audit write neither replays a page nor starves the queue.

## Stop

- Stop if the new module requires root import coupling, a `go.work`, or factory capability changes.
- Stop before adding voice, SMS, mobile, UI, or notification-routing capability to this specimen.
- Stop before any proof dependency that leaves the machine, external infrastructure, Gate invocation, or merge.
- Stop after two review-fix rounds even if a broader architectural finding remains.

## Evidence

- Verified: the new module's `go test -race ./...`, `golangci-lint`, `gofmt`, `shfmt`, `shellcheck`, and boundary checks pass.
- Verified: `make incident-demo` passed locally against `prom/alertmanager:v0.28.1`, `axllent/mailpit:v1.30.1`, and `alpine:3.22` on linux/arm64.
- Verified: `make incident-invariants` passed all ten probes, including escalation firing after a kill and reboot on the same database with the clock advanced.
- Verified: an exhaustive state walk drives every signal from every reachable durable state and pins the reachable-state count at 13.
- Reviewed: the first `@codex review` round found two P1s, both reproduced and fixed. A lost ingest race could surface as HTTP 409, which the upstream retrier drops, silently discarding a resolve; the re-apply is now bounded and a persistent conflict answers 503. Raw transport errors reached the audit carrying destination URLs and SMTP reply text; a transport now returns one classification from its own vocabulary and policy persists nothing else.
- Verified: a new invariant probe pages an unreachable destination and proves the audit records a classification with no host, path, or port.
- Reviewed: the second `@codex review` found no major issues at `25de9a6`.
- Pending: root and CI checks on the pushed rebased head.

## Handoff

- Last: rebased the reviewed commits onto the current webhook base and retained
  the base manifest shape while declaring incident escalation.
- Next: push the rebased exact head, resolve the answered Codex threads after
  verification, and stop at the CI-green head without Gate or merge.
