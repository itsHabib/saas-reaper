---
name: explain-evaluation
description: Reconstruct why a flag produced a value from its published revision and evaluation context without mutating code, configuration, or state.
---

# Explain one evaluation

This skill is read-only.

## Gather

Obtain the environment, flag key, exact published revision, and evaluation context. Redact secrets and unnecessary personal data from the report.

## Reconstruct

Apply the invariant order in `AGENTS.md`:

1. Disabled state.
2. Rules in declaration order.
3. Rollout bucket using the documented SHA-256 input and byte order.
4. Default variant.

Name the first decisive step, returned variant/value/reason, and revision. For a rollout, report the computed bucket in the range 0–9999 and threshold, not merely “inside” or “outside.”

## Boundaries

Do not publish a flag, edit the database, add logging, or infer missing context. If the exact revision is unavailable, say the decision cannot be reproduced from current state.
