---
name: swap-storage
description: Add or replace the feature-definition authority while preserving flag policy, optimistic revisions, restart recovery, and atomic audit behavior.
---

# Swap the storage mechanism

## Read first

Read `AGENTS.md`, the `flags.Store` interface beside `internal/flags/publish.go`, the SQLite implementation, the memory implementation, and their tests.

## Rules

- Add one concrete package under `internal/store/<mechanism>`.
- Implement exactly the interface consumed by `internal/flags`; do not widen it for backend convenience.
- Keep validation and revision policy out of the mechanism. The store atomically compares the supplied expected revision, writes the next revision, and appends its audit record.
- Return a concrete store from its constructor.
- Wire the selection only in the composition root.
- Do not change API or evaluation behavior to fit the backend.

## Evidence

Prove create, update, stale-write conflict, restart/load, chronological audit, and no audit entry on failed publication. Run `make check` and `make demo`.
