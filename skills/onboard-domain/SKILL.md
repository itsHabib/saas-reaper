---
name: onboard-domain
description: Ground the reaped feature-flag appliance in a customer's tenant vocabulary, authority rules, and approved evaluation context without moving policy into transport or storage.
---

# Onboard a customer domain

Use this when the operator asks to adapt the generic specimen to a real product domain.

## Read first

Read `AGENTS.md`, `REAPER.yaml`, `DOMAIN.md`, `internal/flags/context.go`, and the evaluation tests completely.

## Elicit only missing facts

Determine:

1. The tenant boundary: organization, account, workspace, project, or another explicit concept.
2. The stable targeting subject and whether it is pseudonymous or personal.
3. The domain attributes that are genuinely allowed to change product behavior.
4. Who may publish to each environment.
5. Required missing-value and outage behavior.

Do not invent a customer concept, targeting attribute, sensitivity classification, or authorization rule.

## Apply

- Update `DOMAIN.md` first with concrete vocabulary and privacy boundaries.
- Add only approved targeting attributes through the `add-targeting-attribute` skill.
- Keep evaluation decisions in `internal/flags`.
- Keep authentication mechanics in `internal/api`; inject authorization policy into `internal/flags` if the domain requires more than token separation.
- Update `REAPER.yaml` to reflect the resulting package, including exclusions.

## Prove

Add one positive domain case and one missing, malformed, or unauthorized case. Run `make check` and `make demo`. Report unresolved domain questions rather than encoding guesses.
