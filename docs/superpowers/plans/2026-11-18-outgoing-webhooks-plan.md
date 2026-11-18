# Outgoing webhooks — implementation plan

2026-11-18. Implements `specs/2026-11-18-outgoing-webhooks-design.md` (W1–W39; §13 wins).
Research: `research/2026-11-18-outgoing-webhooks.md`. `srv/` is `apps/server/internal/`,
`host/` is `apps/host/frontend/src/`.

## Order and process

Docs PR (research, spec, this plan) → **A** → **B** → **C** → landing PR. One PR each, each
landed on `main` through the merge queue before the next is pushed (branches are never
rebased once pushed; merge `main` into a branch if it must catch up). Per delivery:

1. Build in a worktree off the latest `origin/main`, tests first where a rule is testable.
2. A **code review** and a **security review**, in separate passes by separate agents; fix their
   findings; re-review a fix that changed logic.
3. Gates: `mise run server:check`, `mise run server:test` (re-run failing packages with
   `-p 1` if SQLSTATE 53300), `mise run frontend:check`, `mise run docs:check`; `go generate`
   in `apps/server` after any docs edit.
4. Open the PR, wait for **green CI** (the merge queue does not gate: `[[merge-queue-does-not-gate]]`),
   check the migration number against `main` and open PRs right before enqueueing
   (`[[migration-number-collisions]]`), merge via GraphQL `enqueuePullRequest`.
5. Commits are Conventional, SSH-signed, with the attribution trailer.

Migration: **`00201`** holds every table (A); B and C add none. Renumber in-history before
the first push if `main` moves.

## Delivery A — schema, catalogue, signer, endpoints

Code:
- `srv/db/migrations/00201_identity_webhooks.sql`: `webhook_endpoints`, `webhook_deliveries`
  (with `resend_count`, `resend_window_start`, `completed_at`), `webhook_attempts`; indexes for
  the claim `(state, next_attempt_at)` partial, `(endpoint_id, event_id)` unique (event_id may
  be null), `(created_at)`, `(integration_id)`; checks on state and reason; the per-endpoint
  `events` array check.
- `srv/contracts/webhook_events.go`: the catalogue (W4, W26) and `WebhookEvents()`.
  `srv/integration/webhook_events_test.go`: the catalogue-to-code test (W26).
- `srv/identity/webhooks_signer.go` (+ Svix-vector test first), `webhooks_secret.go`
  (`secrets.Box`, purpose `identity/webhook-secret`), `webhooks_url.go` (W11, W28),
  `webhooks_client.go` (W20, no sending yet beyond a `Send` used by B), `webhooks.go`
  (service: create, list, read, patch, delete, enable, disable, roll; W1–W3, W9, W10, W24,
  W31, W36), `queries/webhooks.sql` and sqlc output.
- `openapi/identity.yaml` and `srv/openapi/specs/identity.yaml`: endpoints, schemas, marks
  (`policy:Owner+OwnerManagement`, `x-vantigo-api: never`, `x-vantigo-assistant: never`);
  `srv/openapi/testdata/api-marks.txt` regenerated; `limits` entries.
- Archive path (`integrations.go`) disables endpoints in its transaction (W2, W31).
- `host/api/webhooks.ts` (+ regenerated `api-schema.d.ts`), `-api-access-webhooks.tsx`
  (endpoints tab, create/edit form with the grouped event picker, enable/disable/delete, roll),
  the one-time secret dialog, route `validateSearch` and `IntegrationTab` (W39), catalogue
  keys `apiAccess.webhook*` in en and nb.

Docs (en **and** nb): new `reference/webhooks.md` (message, headers, verification with
prose and a signed example, events table generated-by-test-checked, endpoints API, limits,
errors, tables) listed in `reference/index.md`; `admin/api-access.md` Webhooks section (add an
endpoint, pick events, the secret shown once, rolling it, URL rules); `admin/configuration.md`
only in B (the switches arrive there). `sources` updated per W39. `reference/` is English
only; `admin/api-access.md` has an `nb/` twin.

Tests: spec §9 items for signer, URL, catalogue, API, audit, frontend.

## Delivery B — fan-out and sending

Code:
- `srv/identity/webhooks_handler.go`: `identity.webhooks` outbox handler (W14, W31), added as
  `EventHandlers` on `identity.Module`.
- `srv/identity/webhooks_worker.go`: `identity.webhook-sender` (W15, W16, W17, W29, W30, W33,
  W37, W38): claim, send, record, retry schedule with jitter and an injected clock,
  `Retry-After`, 410, expiry, prune, W18/W32 auto-disable and notice (en/nb texts), ping
  deliveries and the `POST …/test` operation (enabled in A's contract, implemented here).
- `srv/config` `IDENTITY_WEBHOOKS_ENABLED`, `IDENTITY_WEBHOOKS_POLL`; `workers()` gates it.
- Create and enable queue a ping (W19).
- `host/`: the Send test event button, status badges, the disabled banner with its reason.

Docs: `reference/webhooks.md` (retries, ordering, disabling, the notice), `admin/api-access.md`
en+nb (what a disabled endpoint means, re-enable), `admin/configuration.md` en+nb (the two
variables, and that a stopped worker queues rows that expire).

Tests: handler on a pool of one; sender against an `httptest` TLS server via
`Deps.HTTPTransport`; the claim under two concurrent workers (4-per-endpoint cap); fairness
with two timing-out endpoints; notice once per disable and again after re-enable.

## Delivery C — the delivery log and resend

Code:
- Operations list deliveries (cursor, `state`), read one with its attempts, resend (W22,
  W35); `webhooks_log.go`, queries, OpenAPI, marks.
- `host/`: `-api-access-webhook-deliveries.tsx` (modelled on `-api-access-requests.tsx`),
  detail drawer with attempts, resend button, filters, empty states.
- Pruning is in B's worker; C asserts and documents it.

Docs: `reference/webhooks.md` (log, retention, resend), `admin/api-access.md` en+nb (reading
the log, resending), `user/` nothing (Owners only).

## Landing

`../vantigo-landing` (`/Users/anders/projects/vantigo-io/vantigo-landing`): follow its
`AGENTS.md`. The integrations / API access page gains the webhooks capability in English and
Norwegian from `admin/api-access.md`, and `mise run apps:check` there passes. Opened as a
separate PR once C is on `main`, linked to C's PR.

## After the deliveries

Record the state in project memory; the manual browser check (a real receiver or tunnel) and
the owner's overturns (spec §12) are listed as open.
