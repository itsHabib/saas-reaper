# Incident escalation golden specimen

This module is SaaS Reaper's third customer-owned specimen: one Go process, one
SQLite file, and no vendor to keep paying per seat for an escalation timer and a
calendar. It accepts alerts on the PagerDuty Events API v2 wire shape,
deduplicates them into incidents, climbs a declared escalation ladder when
nobody acknowledges, resolves who is on call from a declared rotation, pages the
responders that level names, and keeps an append-only journal and notification
audit.

The selection rule is the same one that picked webhook delivery. Incident
paging is sold per seat as though the hard part were the pager; the essential
mechanism is a durable timer, a rotation calculation, and a bounded retry loop.
The claim is testable rather than rhetorical because the ingest surface is an
open wire contract that a real, unmodified **Prometheus Alertmanager** speaks:
the demo points Alertmanager's own `pagerduty_configs` at this service and lets
it open, deduplicate, and resolve an incident.

## Run the proof

From the repository root:

```sh
make incident-demo        # real Alertmanager, real SMTP, official verifier
make incident-invariants  # bounded lifecycle probes, no container runtime
```

Or from this directory: `make verify` (checks, then both proofs).

The demo needs a working Docker daemon; the invariant probes do not.

### What the demo actually proves

`scripts/demo.sh` starts an unmodified `prom/alertmanager:v0.28.1` container
whose configuration is the ordinary receiver an operator writes to point
Alertmanager at a hosted vendor — only the routing key and the URL are
substituted:

```yaml
receivers:
  - name: reaper-incidents
    pagerduty_configs:
      - routing_key: <the key this service minted>
        url: http://<this service>/v2/enqueue
        send_resolved: true
```

It then fires an alert through Alertmanager's own `/api/v2/alerts` API and
asserts, end to end, that

- exactly one incident opens, carrying Alertmanager's payload (summary, source,
  `severity`, `client`);
- the incident's dedup key is Alertmanager's own identity for the alert group —
  the hex SHA-256 of its aggregation group key;
- a second identical notification opens no second incident and is journaled as a
  repeat;
- the responder's signed webhook page is accepted by the **official Standard
  Webhooks Go verifier**, and the same library rejects the same page after one
  Base64 character of the signature is changed;
- a real SMTP server (`axllent/mailpit`) receives the email page with the
  incident's severity, service, and summary;
- every notification attempt appears exactly once in the append-only audit;
- Alertmanager's resolve notification closes that same incident identity.

Alertmanager, this service, the responder sink, the SMTP server, and the probe
that drives the assertions share one private container network. No port is
published to the host, and no traffic leaves the machine.

### What the invariant probes prove

`scripts/invariants.sh` runs host processes on `127.0.0.1` with an injected
clock offset, so a 30-second escalation timeout is proven in seconds and no
container runtime is required:

- three triggers with one dedup key hold exactly one open incident, and the
  repeats are journaled;
- a schedule override wins inside its window and the rotation resumes after it;
- an unacknowledged incident climbs to level two exactly at its timeout and
  pages the responder that level names, attributed to the timer principal;
- **the escalation timer survives a process restart**: the service is killed and
  rebooted on the same database with the clock advanced, and the escalation
  still fires;
- acknowledging stops escalation permanently, even ten minutes past the timeout;
- a resolve from ingest closes the incident and disarms its timer, and a later
  trigger opens a fresh identity;
- the management token cannot read and the audit-read token cannot mutate;
- a page to an unreachable destination is audited as a classification, and the
  destination, its path, and its port appear nowhere in the audit;
- the audit holds exactly one row per attempt, exposes no signing secret or
  destination, and every row written before a restart survives it unchanged.

## Timer durability

The escalation timer is not a `time.Timer` and is never held only in memory. It
is a column: `incidents.escalate_at`, written inside the same transaction as the
lifecycle transition that armed it. A poller asks the store for triggered
incidents whose `escalate_at` has passed and applies one revision-checked
transition per incident. A restart therefore reconstructs every pending
escalation from SQLite by definition — there is nothing to rebuild — and a lost
race is skipped rather than failing the batch.

`REAPER_INCIDENT_CLOCK_OFFSET` advances the one clock every policy decision
reads. It exists so the proofs can cross a timeout deliberately instead of
sleeping, and it is the only way the specimen's notion of "now" can be moved.

## Ingest contract

```text
POST /v2/enqueue        (no bearer token: the routing key is the credential)
```

```json
{
  "routing_key": "...",
  "event_action": "trigger | acknowledge | resolve",
  "dedup_key": "optional; generated when absent",
  "client": "Alertmanager",
  "payload": {"summary": "...", "source": "...", "severity": "critical|error|warning|info"}
}
```

Accepted events answer `202` with exactly:

```json
{"status": "success", "message": "Event processed", "dedup_key": "..."}
```

Invalid events answer `400` with `{"status": "invalid event", "message": "Event
object is invalid", "errors": ["..."]}`. Unknown fields are accepted and ignored,
because real senders add `client_url`, `images`, `links`, `payload.class`,
`payload.component`, `payload.group`, and `payload.custom_details`. Only 2xx is
success to Alertmanager, so every rejection is deliberate.

Concurrent events for one dedup key serialize through the store, and a lost
optimistic race is re-read and re-applied a bounded number of times. If the
bound is exhausted the answer is `503`, never `409`: the upstream retrier
retries 5xx and 429 and drops every other status, so reporting a conflict as `409`
would silently discard a resolve and leave the incident escalating.

`trigger` opens an incident unless one is already open for that service and
dedup key, in which case it is journaled as a repeat. `acknowledge` and
`resolve` act on the open incident and are accepted silently when none exists,
matching the upstream contract. A resolved incident is terminal: a later trigger
with the same dedup key opens a new incident with a new identity.

## Lifecycle and escalation

```text
triggered ──acknowledge──► acknowledged ──resolve──► resolved
    │  ▲                        │                       ▲
    │  └── trigger (journaled)  └── resolve ────────────┘
    └── timeout ──► next level, or repeat the ladder, or exhausted
```

One transition function owns this table; ingest, management, and the timer all
go through it. Acknowledging disarms the timer. Timeout climbs one level, then
loops the ladder while repeats remain, then journals exhaustion and disarms —
the incident stays open but stops paging. An exhaustive state walk in
`internal/incident/state_walk_test.go` drives every signal from every reachable
durable state and pins the reachable-state count at 13.

## HTTP surface

Management routes require `Authorization: Bearer $REAPER_INCIDENT_ADMIN_TOKEN`:

```text
POST /v1/responders
POST /v1/schedules
POST /v1/escalation-policies
POST /v1/services
POST /v1/incidents/{incident}/acknowledge
POST /v1/incidents/{incident}/resolve
```

Read routes require `Authorization: Bearer $REAPER_INCIDENT_READ_TOKEN`:

```text
GET /v1/incidents?serviceId=&state=&limit=
GET /v1/incidents/{incident}
GET /v1/incidents/{incident}/events
GET /v1/incidents/{incident}/notifications
GET /v1/attempts?incidentId=&notificationId=&limit=
GET /v1/schedules/{schedule}/on-call?at=RFC3339
```

Registering a service mints its routing key once; registering a responder with a
`webhookUrl` mints its signing secret once. Neither is ever returned again, and
the read surface exposes no secret and no destination URL. The audit actor comes
from the configured server principal for management actions, `service:<id>` for
anything the wire drove, and `system:escalation-timer` for the timer — never
from request JSON.

## On-call schedules

A schedule is a small declared data format, not a UI:

```json
{
  "id": "payments-primary",
  "name": "Payments primary rotation",
  "layers": [
    {"name": "weekly-primary", "start": "2026-01-05T09:00:00Z",
     "rotation": "168h", "responders": ["ada", "grace"]}
  ],
  "overrides": [
    {"responder": "linus", "start": "2026-01-10T09:00:00Z", "end": "2026-01-12T09:00:00Z"}
  ]
}
```

Resolution at an instant is total and deterministic: an override covering the
instant wins; otherwise the highest-index layer whose window covers the instant
wins, rotating through its responders from that layer's start. Windows are
half-open, overrides may not overlap, and nobody is on call before a layer
starts. `internal/oncall` depends on nothing else in the module.

## Notification

Notification is deliberately narrow, behind a policy-owned `Notifier` seam:
signed outbound webhooks and SMTP email. The signing shape is the Standard
Webhooks shape copied — not imported — from the webhook-delivery specimen:
`webhook-id`, `webhook-timestamp`, `webhook-signature: v1,<base64 HMAC-SHA256>`
over `{id}.{timestamp}.{body}`. **The notification-routing specimen is the
intended future implementation behind this seam**; when it lands, these two
transports become one of its channels rather than the whole story.

Delivery is at least once with a bounded schedule (`10s, 30s, 1m, 5m` by
default). A page is leased before any I/O and its attempt is audited in the same
transaction that advances its state, so a failed audit write neither replays the
page on the next tick nor blocks the pages behind it. A 2xx is delivery; 408,
429, and 5xx retry; every other status and any unusable secret is permanent and
terminal. An unconfigured SMTP relay fails permanently rather than retrying
forever.

A failed page is audited as a **classification, never as a transport error
string**. A Go `url.Error` carries the full destination including any query
credentials, and an SMTP relay echoes the rejected mailbox and its own hostname
in reply text; the audit-read token is lower authority than the principal that
configured those, so a transport returns one short token from its own fixed
vocabulary (`connection_failed`, `timeout`, `http_status_502`,
`smtp_status_550`, `relay_unconfigured`, …) and policy persists only that.
Anything a transport failed to classify is recorded as `notification_failed`,
so an unclassified error cannot leak by omission.

## Configuration

```text
REAPER_INCIDENT_ADMIN_TOKEN
REAPER_INCIDENT_ADMIN_ACTOR
REAPER_INCIDENT_READ_TOKEN
```

The two tokens must differ. Optional settings:

| Variable | Default |
| --- | --- |
| `REAPER_INCIDENT_ADDR` | `:8080` |
| `REAPER_INCIDENT_DB` | `.reaper/incidents.db` |
| `REAPER_INCIDENT_SMTP_ADDR` | unset; email pages then fail permanently |
| `REAPER_INCIDENT_SMTP_FROM` | `pager@reaper.invalid` |
| `REAPER_INCIDENT_NOTIFY_RETRY_DELAYS` | `10s,30s,1m,5m` |
| `REAPER_INCIDENT_POLL_INTERVAL` | `250ms` |
| `REAPER_INCIDENT_REQUEST_TIMEOUT` | `20s` |
| `REAPER_INCIDENT_CLOCK_OFFSET` | `0` |

## Boundaries

```text
Events API v2 ──┐
management HTTP ├──► incident policy ──► SQLite authority
audit-read HTTP ┘         ▲   │
                          │   ▼
                 on-call resolution   escalation + notification pollers
                                              │
                                              ▼
                                  signed webhook / SMTP transports
```

`internal/incident` owns lifecycle transitions, dedup, escalation, planning, and
the `Notifier` and `Store` interfaces it consumes. `internal/oncall` owns
rotation and override resolution and imports nothing else. `internal/api`
translates HTTP and keeps the two bearer authorities separate. `internal/store/sqlite`
owns transactions, the unique open-dedup index, and the append-only journal and
audit triggers. `internal/transport/*` own outbound I/O only. `internal/worker`
owns waiting. The composition root is `cmd/reaper-incidents`.

## Deliberate gaps

These are missing on purpose, and this specimen does not pretend otherwise:

- **No mobile app, no voice calls, no SMS.** Paging a phone is the part real
  responders rely on, and it needs a carrier relationship this specimen has no
  business owning.
- **No UI.** Schedules and ladders are declared data.
- **No notification routing.** The `Notifier` seam is where the
  notification-routing specimen belongs.
- **One process, one SQLite authority.** The notification lease and the
  revision-checked transition make a second process safe against duplicate
  sends, but nothing elects a leader or leases the escalation scan.
- **No alert-level suppression, maintenance windows, or grouping.** Alertmanager
  already does that upstream.
- No factory generation: the root factory still accepts only the `feature-flags`
  capability.

The database file is created and enforced as owner-only (`0600`). Responder
signing secrets live in that customer-owned database without application-layer
encryption. The authenticated management principal is trusted to choose
destinations; the sender rejects non-HTTP(S) URLs, credentials, fragments, and
redirects, but it is not a substitute for an outbound SSRF-filtering proxy in an
untrusted multi-tenant control plane.
