# Audit ledger golden specimen

This module is SaaS Reaper's third customer-owned specimen: one Go process,
one SQLite file, and no vendor charging per event to keep an append-only,
tamper-evident record of who did what. It ingests events idempotently, assigns
each tenant a gapless sequence, links every entry to its predecessor with a
SHA-256 hash chain inside one transaction, refuses UPDATE and DELETE at the
database layer, serves paginated reads and a head, and streams an NDJSON export.

The credibility claim is independent verification. `verifier/verify.py` is a
Python program with no third-party dependencies and no shared code with the
Go service. It reads an export, recomputes the whole chain from genesis using
only the canonical encoding written down below, and prints the head hash. The
demo proves that hash equals the service's `/head`, then edits one exported
value and proves the verifier names the exact sequence where the chain breaks.
Two implementations agreeing on every byte is the proof; one implementation
checking itself would not be.

Factory templates for audit ledgers are not part of this specimen; the root
factory still accepts only the `feature-flags` capability.

## Run the proof

From the repository root:

```sh
make audit-demo
make audit-invariants
# or both, after static checks
make audit-proof
```

The demo binds loopback port `19601`, the invariant harness `19602`. Both use a
temporary database and `python3` from `PATH` (standard library only).

The invariant harness independently proves:

- the write token cannot read and the read token cannot append;
- a replayed batch returns the original sequences and hashes without appending;
- a replayed ID with different content is refused with `409` and appends nothing;
- one invalid or conflicting event keeps an entire batch out of the chain;
- an out-of-scope tenant's `head`, `events`, and `export` are byte-identical to an absent tenant's `404`;
- a page walk over fourteen entries visits every sequence exactly once;
- `UPDATE` and `DELETE` on `entries` are rejected by SQLite triggers, not by convention;
- head, sequences, and export bytes are identical across a restart on the same file.

## Canonical encoding (wire contract)

Every entry hashes as `hash_n = hex(SHA-256(canonical(entry_n) || hex(hash_{n-1})))`
where `hex(hash_0)` is sixty-four ASCII `0` characters and `||` is byte
concatenation. `canonical(entry)` is the UTF-8 encoding of a JSON object with
exactly these members and no others, in this order (Unicode code point order
of the keys):

```text
action, actor, id, metadata, occurredAt, recordedAt, sequence, source, target, tenant
```

The canonical JSON rules, applied recursively to `metadata`:

- Objects render their keys sorted by Unicode code point (for valid UTF-8 this
  equals byte order). Duplicate keys are not representable; a decoder that
  keeps the last one wins on both sides.
- No whitespace appears outside strings.
- Strings are raw UTF-8. Only `"` (as `\"`), `\` (as `\\`), and U+0000–U+001F
  (as lowercase `\u00xx`) are escaped. `/`, `<`, `>`, `&`, U+007F, U+2028, and
  every non-ASCII code point are written unescaped. Strings containing U+FFFD
  are rejected at ingest so undecodable input cannot become a valid entry.
- Numbers are integers with magnitude at most 2^53−1, rendered in decimal
  with no leading zeros, exponent, fraction, or negative zero (`-0` is `0`).
  Any other number is rejected at ingest.
- `true`, `false`, and `null` are literals. `null` is a value, never an omitted
  member: an event without `metadata` records `"metadata":null`.
- `sequence` is the integer position; `occurredAt` and `recordedAt` are the
  UTC RFC 3339 strings the service stored (sub-second digits with trailing
  zeros trimmed), treated by the verifier as opaque strings.

The export line encoding is ordinary JSON and carries `hash`; it is not
required to be canonical. Verifiers parse each line, drop `hash`, re-canonicalize
the remaining members, and compare. A line must carry exactly the ten members
above plus `hash` and nothing else, every one of them a string except the
integer `sequence` and the free-form `metadata`. A verifier that silently
ignored an unexpected member, or that accepted a boolean where an integer
belongs, would report a line as verified while authenticating only part of it. `verifier/verify.py` prints
`ok sequence=N head=HEX` on success and `broken sequence=N reason=...` with
exit `1` at the first hash mismatch, gap, or foreign tenant row.

## HTTP surface

The write route requires `Authorization: Bearer $REAPER_AUDIT_WRITE_TOKEN`:

```text
POST /v1/events
```

The body is one event object or an array of 1–500 of them:

```json
{"tenant":"acme","id":"evt-0001","actor":"user:ada","action":"member.invited",
 "target":"member:grace","occurredAt":"2026-08-30T09:15:00Z","metadata":{"role":"admin"}}
```

`tenant` is 1–64 lowercase letters, digits, or interior hyphens. `id`,
`actor`, `action`, and `target` are non-empty, at most 512 bytes, without
surrounding whitespace or control characters. `occurredAt` is RFC 3339.
`metadata` is any JSON value obeying the canonical rules, at most 64 KiB
canonical and 32 levels deep. The response is `{"receipts":[...]}` with one
`{tenant, id, sequence, hash, replayed}` per event, `201` when anything was
appended and `200` when every event was a replay. A batch is all-or-nothing.

Idempotency is keyed by `(tenant, id)`. A replay with identical `actor`,
`action`, `target`, `occurredAt`, and canonical `metadata` returns the original
receipt; a replay with different content fails the whole request with `409`.

Read routes require `Authorization: Bearer $REAPER_AUDIT_READ_TOKEN` and a
tenant listed in `REAPER_AUDIT_READ_TENANTS`:

```text
GET /v1/tenants/{tenant}/head
GET /v1/tenants/{tenant}/events?after=0&limit=100
GET /v1/tenants/{tenant}/export
```

`head` returns `{tenant, sequence, hash}`; an empty scoped tenant reports
sequence `0` and the genesis hash. `events` returns `{events, next}` in
ascending sequence with `limit` 1–1000; pass `next` back as `after` until
`events` is empty. `export` streams `application/x-ndjson`, one entry per line
in sequence order. Any tenant outside the configured scope returns the same
`404` body whether or not it has entries.

## Configuration

Required:

```text
REAPER_AUDIT_WRITE_TOKEN
REAPER_AUDIT_WRITE_PRINCIPAL
REAPER_AUDIT_READ_TOKEN
REAPER_AUDIT_READ_TENANTS   comma-separated tenant names the read token may see
```

The tokens must differ. `REAPER_AUDIT_WRITE_PRINCIPAL` is recorded as every
entry's `source`: the event's `actor` is customer data reported by the caller,
while `source` is the authenticated ingest principal the server configured.
Optional: `REAPER_AUDIT_ADDR` (default `:8080`) and `REAPER_AUDIT_DB`
(default `.reaper/audit.db`).

## Boundaries

```text
write HTTP ──► ledger policy ──► SQLite authority
read HTTP ─────────────────────► SQLite authority
```

`internal/ledger` owns validation, canonical bytes, hash linking, idempotency,
and the append algorithm, which it runs inside a transaction callback the store
provides. `internal/api` translates strict HTTP, keeps the two bearer
authorities separate, and enforces the tenant scope. `internal/store/sqlite`
owns the immediate write transaction, the `entries` table, and the append-only
triggers; it decides nothing about chain content. The composition root is
`cmd/reaper-audit`. There is no background worker.

Appends are serialized by one SQLite connection with immediate transactions,
so per-tenant sequences are gapless and the head read inside the transaction is
the head the new entry links to. That guarantee holds for one process on one
database file; two processes sharing the file would rely on SQLite's file lock
and `busy_timeout` alone, which this specimen does not prove.

What the hash chain proves: any change, removal, or reordering of an entry in
an export or in the database is detected by anyone holding an earlier head
hash or the full export, unless the party can rewrite every later entry.
Anyone with write access to the SQLite file can drop the triggers and rebuild
the whole chain, and the verifier would accept the result; only a head hash
recorded outside their reach (the service's `/head` at an earlier time, a
copy of an export, a printed digest) exposes it. The chain is not a notarized
timestamp, carries no signature, and does not prove when an entry was made.
Search, retention, a UI, signing keys, notarization, multi-process writers,
and factory generation remain explicit follow-up work.
