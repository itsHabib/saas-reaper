---
name: add-targeting-attribute
description: Add one explicitly approved feature-flag targeting attribute with domain documentation, privacy classification, validation, and positive/rejection evidence.
---

# Add one targeting attribute

Use only after the operator has named the customer concept and authorized it as evaluation input.

## Read first

Read `AGENTS.md`, `DOMAIN.md`, `REAPER.yaml`, `internal/flags/context.go`, and `internal/flags/evaluate_test.go`.

## Change boundary

1. Add the exact external attribute name to `allowedAttributes` in `internal/flags/context.go`.
2. Record its representation, purpose, sensitivity, and missing-value behavior in `DOMAIN.md`.
3. Add it to `domain.targetingAttributes` in `REAPER.yaml`.
4. Add a positive rule test using the attribute.
5. Preserve or add a rejection test proving an unapproved neighboring attribute remains invalid.

Do not change `internal/api`, `internal/store`, or rollout hashing. Do not allow an entire namespace or accept arbitrary context keys.

## Verify

Run `make check`. Run `make demo` only if the fixture or runnable examples now use the attribute. Show the exact domain decision and files changed.
