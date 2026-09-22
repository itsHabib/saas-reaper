# Notification routing golden specimen

This module is SaaS Reaper's third customer-owned specimen: one Go process, one
SQLite file, and no vendor to keep paying per notification. It defines
templates per channel, stores recipients with per-channel addresses and
preferences, fans one send out to every channel a recipient allows, retries
transient transport failures on a bounded schedule, deduplicates re-sends by
idempotency key, and exposes an append-only attempt audit.

The selection rule is the same one that chose webhook delivery. Hosted
notification routers charge per message for template substitution and a fan-out
loop over transports the customer already owns and already pays for. The
essential mechanism is small; the rent is not. What makes the claim testable
rather than rhetorical is that both transports speak open, documented wire
protocols, so the proof can drive a real third-party server implementation
instead of a mock.

Factory templates for notification routing are not part of this specimen; the
root factory still accepts only the `feature-flags` capability.

## Run the proof

From the repository root:

```sh
make notification-demo
make notification-invariants
# static checks plus both runnable proofs
make notification-proof
```

The demo starts the service and both sinks on loopback, defines an email and a
chat variant of one template, registers a recipient who allows both, and sends
one notification. It then asserts that the rendered subject and body arrived at
each transport and that the audit recorded exactly one delivered attempt per
channel. It finishes by proving a send whose payload is missing a template
variable is refused with 400 and reaches no transport at all.

The invariant harness independently proves:

- a missing template variable is rejected at send time, not at delivery time;
- a repeated idempotency key returns the first acceptance and delivers once;
- reusing a key for a different payload is a 409, not a silent redelivery;
- a recipient-disabled binding and an operator-disabled channel both stay silent;
- two 5xx replies are audited exactly once per try before the retry succeeds;
- pending work and prior attempts survive a restart on the same SQLite file;
- the management token cannot read attempts and the audit-read token cannot send.

Dependency installation happens only in `setup`. Demo and invariant execution
make no registry calls and send traffic only to `127.0.0.1` on ports `1940x`.

## What the SMTP proof does and does not prove

The email sink is [`emersion/go-smtp`](https://github.com/emersion/go-smtp), a
genuine third-party SMTP server implementation, not a hand-rolled socket
reader. The specimen's sender speaks to it with the standard library's
`net/smtp` client. The sink parses each accepted message with `net/mail`,
decodes the declared transfer encoding, and records the result, so the harness
compares the subject and body a real mail parser produced.

That proves the specimen completes a real SMTP transaction against an
independent implementation, that its RFC 5322 message parses, that the rendered
subject and body survive transport byte for byte, and that a 4xx reply retries
while a 5xx reply is terminal.

It does not prove deliverability. TLS negotiation with a public relay,
authentication against a specific provider, DKIM/SPF/DMARC alignment, bounce
and complaint handling, reputation, and multipart or attachment-bearing
messages are all outside this proof. The sender does `STARTTLS` when the relay
advertises it and `PLAIN` authentication when credentials are configured, but
neither path is exercised by the local proofs.

The chat sink validates each request against Slack's documented incoming-webhook
payload shape — a JSON object whose top-level keys are all documented fields,
carrying `text`, `blocks`, or `attachments` — and answers with Slack's
documented `ok`, `invalid_payload`, and `no_text` responses. It never contacts
Slack. That proves the specimen emits the documented shape; it does not prove
any particular workspace would accept it.

## The transport seam

Policy owns the decision; a transport owns only its wire protocol:

```go
type Transport interface {
    Deliver(context.Context, Envelope) (Receipt, error)
}
```

An `Envelope` is already rendered and already addressed — a transport never
sees a template, a payload, a recipient's other channels, or the retry
schedule. A `Receipt` carries the protocol's own result code and nothing else:
remote-controlled text, an SMTP reply message or a webhook response body, never
crosses this seam, so redaction has exactly one boundary and a later consumer
cannot reintroduce a leak by reading a field. Classification is the transport's one policy-shaped duty, and it is
expressed in the error rather than in a status enum: an error wrapping
`routing.ErrPermanent` ends retries, and any other error is transient. That
keeps the SMTP reply-code rules in the SMTP package and the HTTP status rules
in the webhook package while the retry schedule stays in policy, computed
outside the worker.

Adding a transport is: implement `Deliver`, add the `ChannelKind`, register it
in the composition root's map, and add its address validation. The dispatcher
selects by kind and treats an unconfigured kind as a permanent rejection rather
than a retry loop.

## Retry and delivery state

The retry schedule is public behavior:

```text
immediate, 10s, 1m, 5m, 30m, 2h, 6h
```

`REAPER_NOTIFY_RETRY_DELAYS` injects a comma-separated schedule for local
proofs. Every attempt appends exactly one audit row and applies its delivery
transition in the same SQLite transaction. One outcome-to-state table lives in
`routing.TransitionFor` and is called by both policy and the store, so the two
cannot drift.

```text
pending ──delivered──► delivered
        ──permanent──► failed
        ──transient──► pending (bounded), then exhausted
        ──disable───► canceled
```

An exhaustive breadth-first walk over that machine pins 16 reachable durable
states under a three-attempt schedule and asserts every terminal state is
reached; changing the schedule shape or the transition table has to change that
count deliberately. The walk models both shapes of cancellation — before a send,
which leaves no attempt row, and racing an active send, which still audits the
completed attempt while the delivery stays canceled. That second shape is the
only durable state where the audit count exceeds the attempt count.

Delivery is at least once. A transport can accept a message immediately before
the process loses the corresponding SQLite commit, and the pending delivery is
then sent again. Every retry of one delivery reuses that delivery's identity —
the same `Message-ID` for email, the same `X-Reaper-Delivery` header for chat —
so a receiver can collapse duplicates.

## HTTP surface

Management routes require `Authorization: Bearer $REAPER_NOTIFY_ADMIN_TOKEN`:

```text
POST /v1/channels
POST /v1/channels/{channel}/disable
POST /v1/templates
POST /v1/recipients
POST /v1/notifications
```

A channel is `{"id":"email","kind":"smtp"}` or `{"id":"chat","kind":"slack-webhook"}`.
Disable accepts `{"expectedRevision":1}` and cancels that channel's pending
deliveries in the same transaction. A template is one channel variant of a key:
`{"key":"invoice-paid","channel":"email","subject":"...","body":"..."}`.
Placeholders are `{{dotted.path}}` over the JSON payload — substitution only,
no logic language, no loops, no partials. A recipient carries one address per
channel plus that channel's preference:
`{"id":"cus_acme","channels":[{"channel":"email","address":"billing@acme.example","enabled":true}]}`.

A send is
`{"template":"...","recipient":"...","payload":{...},"idempotencyKey":"..."}`.
It answers 202 with the queued deliveries, or 200 with the first acceptance
when the key was already used for the same send. Reusing a key for a different
template, recipient, or payload is a 409. A reused key is settled before any
rendering happens, so the answer does not depend on whether the replacement
request would independently validate. Every channel variant is rendered before
anything is queued, so a missing variable rejects the whole send with 400 rather
than delivering to some channels and failing on others.

The read route requires `Authorization: Bearer $REAPER_NOTIFY_READ_TOKEN`:

```text
GET /v1/attempts?notificationId=...&channelId=...&limit=100
```

Attempt rows are newest first. They carry the configured management actor, the
transport's result code, the delivery state, and the next due time — but never
a recipient address, a webhook URL, or a relay host. Transport failures are
classified and redacted before the lower-authority audit reader can see them:
an SMTP reply contributes its code but never its text, because relays routinely
echo the rejected mailbox or their own hostname into that text.

## Configuration

The five required values are:

```text
REAPER_NOTIFY_ADMIN_TOKEN
REAPER_NOTIFY_ADMIN_ACTOR
REAPER_NOTIFY_READ_TOKEN
REAPER_NOTIFY_SMTP_ADDR
REAPER_NOTIFY_SMTP_FROM
```

The two tokens must differ. Optional runtime settings are:

| Variable | Default |
| --- | --- |
| `REAPER_NOTIFY_ADDR` | `:8080` |
| `REAPER_NOTIFY_DB` | `.reaper/notifications.db` |
| `REAPER_NOTIFY_SMTP_USERNAME` | unset (no authentication) |
| `REAPER_NOTIFY_SMTP_PASSWORD` | unset (no authentication) |
| `REAPER_NOTIFY_RETRY_DELAYS` | the schedule above |
| `REAPER_NOTIFY_POLL_INTERVAL` | `250ms` |
| `REAPER_NOTIFY_REQUEST_TIMEOUT` | `20s` |

## Boundaries

```text
management HTTP ──► routing policy ──► SQLite authority
                          │
                          ▼
                   polling mechanism
                          │
                    ┌─────┴─────┐
                    ▼           ▼
              SMTP sender  webhook sender

audit-read HTTP ────────────────────► SQLite authority
```

`internal/routing` owns validation, template rendering, channel selection,
idempotency identity, and retry transitions. `internal/api` translates strict
HTTP and keeps the two bearer authorities separate. `internal/store/sqlite`
owns transactions and the append-only attempt table.
`internal/transport/smtpmail` and `internal/transport/slackwebhook` own one
wire protocol each. `internal/worker` owns waiting and lifecycle only. The
composition root is `cmd/reaper-notifications`.

A failed audit write for one delivery never stops its siblings: the dispatcher
records the error and continues the batch, so no row can starve the queue.

A batch is a snapshot, so every item is rechecked against the store immediately
before its transport call; a channel disabled partway through a batch cannot
have the rest of that batch sent to it. That narrows the window to one in-flight
send, which no lock could close without holding a permit across network I/O.
When disablement does race an active send, the completed attempt is still
audited — the transport call did happen — while the delivery stays canceled
rather than being transitioned back to pending. Disable cannot recall a request
a transport has already accepted.

This specimen has one worker and one SQLite authority; the store transaction is
the only arbiter, and two processes on one database file are not supported. The
database file is created and enforced as owner-only (`0600`). SMTP credentials
are read from the environment and never stored. The authenticated management
principal is trusted to choose destinations; addresses are validated for shape
and the webhook sender rejects non-HTTP(S) URLs, credentials, fragments, and
redirects, but that is not a substitute for an outbound SSRF-filtering proxy in
an untrusted multi-tenant control plane.

Digest and batching, a user-facing preference center, template versioning,
localization, per-channel rate limits, additional transports (an SMS provider's
Messages API is the obvious next one), multi-worker leasing, and factory
generation remain explicit follow-up work.
