# Domain adaptation record

This specimen assumes a multi-tenant product whose customer boundary is an organization. The record is deliberately small: it names only concepts that currently affect flag behavior. Dotted attribute names such as `organization.id` are flat evaluation-context keys on the wire, never nested objects.

## Vocabulary

| Concept | Representation | Purpose |
|---|---|---|
| Environment | URL-safe string such as `production` | Separates independently published flag revisions. |
| Targeting subject | `targetingKey`, non-empty string | Stable subject for deterministic rollout assignment. |
| Organization | `organization.id`, string | Allows an organization-wide flag decision. |

## Targeting-data policy

- `targetingKey` is required for every OFREP evaluation. It is treated as a pseudonymous identifier; examples use synthetic values.
- `organization.id` may select product behavior for a tenant. It must be an opaque stable identifier, not a display name, email address, or secret.
- Definitions may use only attributes explicitly allowed in `internal/flags/context.go`.
- Unknown request context is ignored by policy. It does not become an allowed targeting dimension by observation.
- No email address, IP address, geographic location, plan, health data, or other personal/sensitive attribute is approved in this specimen.

## Missing values

- A missing, empty, or non-string `targetingKey` rejects the evaluation.
- A missing optional rule attribute makes that rule fail to match; evaluation continues to the next rule or rollout/default.
- A missing rollout attribute makes the rollout miss and returns the default variant.

## Authority

- One configured management principal may publish every environment in this POC. Its verified server-side label becomes the audit actor.
- One evaluation credential may read every environment.
- Per-environment roles, an identity provider, approvals, token rotation, and TLS termination remain explicitly outside the specimen.

## Adaptation record

Add a dated entry when the domain vocabulary or approved context changes. Each entry must name the operator decision, code/test changes, and any privacy consequence.
