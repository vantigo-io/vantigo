---
title: "Management listener"
description: "The private, bearer-protected status endpoint a control plane polls, the stuck deliveries of the events outbox and their retry and skip, and seating the first Owner by invitation."
sidebar:
  order: 40
sources:
  - apps/server/internal/management
  - apps/server/internal/events
---
A control plane that runs many Vantigo instances needs to ask each one three
things without holding a user session: which version is running, whether its
first Owner has been seated, and how much it is being used. The management
listener answers exactly that — and lets the operator see and clear the deliveries of
the events outbox that are stuck ([The events outbox](#the-events-outbox)) — and
nothing else.

| Variable | Purpose | Default |
| --- | --- | --- |
| `MANAGEMENT_PORT` | Port of the private listener (must differ from `PORT`) | unset (disabled) |
| `MANAGEMENT_TOKEN` | Bearer token, at least 32 characters, no whitespace | unset (disabled) |

The two are one switch: set both or neither — one alone is a configuration
error. The listener runs in the `api` and `server` commands; `worker` never
opens it, and the public listener never serves `/management/status` either
(an unmatched path there falls through to the SPA shell, exactly as any
other unknown path does).

## The endpoint

`GET /management/status` with `Authorization: Bearer <MANAGEMENT_TOKEN>`:

```json
{
  "version": "1.4.2",
  "bootstrap": "invited",
  "usage": { "users": 12, "activeUsers": 9, "databaseBytes": 734003200 },
  "live": { "listener": "up", "queueUsage": 0.0001, "connections": 14 }
}
```

| Field | Meaning |
| --- | --- |
| `version` | The build version, the same string `/health/ready` reports |
| `bootstrap` | `pending`: no Owner and no invitation that can be accepted. `invited`: an Owner invitation is waiting. `completed`: an Owner exists |
| `usage.users` | Every account |
| `usage.activeUsers` | Accounts that can sign in: not disabled and not locked out, and, while SCIM is enabled, not deprovisioned upstream — except an Owner, who stays counted as the break-glass account even then. It says nothing about recency |
| `usage.databaseBytes` | `pg_database_size` of the instance's database |
| `live.listener` | `up` or `down`: whether this replica's live-updates listener holds its database connection. While `down`, screens refresh about once a minute |
| `live.queueUsage` | `pg_notification_queue_usage()`, from `0` to `1`, or `-1` when it could not be read. Near `1` means a listener is stuck |
| `live.connections` | The live sockets open on this replica |

The `live` block is left out of the answer of a `worker`-only process and when
`LIVE_ENABLED=0`: it describes the replica you ask ([Live updates and your
proxy](/en/admin/installation/#live-updates-and-your-proxy)).

There is no `usage.storageBytes` field — usage is users, active users and
database size only.

A wrong or missing token is `401` with `WWW-Authenticate: Bearer`, for
`/management/status` and for every other path on this listener: the bearer
check wraps the whole handler, ahead of routing, so an unauthenticated
caller cannot learn which routes exist. A failing dependency is `503`; the
cause is logged, never returned. An empty configured token — unreachable
through normal configuration, since `MANAGEMENT_TOKEN` must be at least 32
characters, but the listener does not rely on that upstream check — closes
the endpoint instead of opening it: every request is then rejected, `401`,
regardless of what is presented.

## The events outbox

Modules tell each other what happened through the platform's events outbox: an event is
written with the change that caused it, and the `events.dispatch` worker hands it to
each module that takes it — one **delivery** per event and handler
([The domain-events outbox](/en/contributing/platform-packages/#the-domain-events-outbox)).
A delivery that fails is retried after 1 minute, 5 minutes, 30 minutes, 2 hours and
12 hours, and every 12 hours from then on; after the eighth failure it is **dead** and
is never tried again by itself. A failed or dead delivery also holds back the later
deliveries to the same handler for the same document, so they stay in order. Clearing
it is the operator's job, with three calls on this listener, behind the same bearer
token. No tenant user and no permission reaches them.

The dispatcher runs wherever the workers run — the `worker` command, or `api` with
`WORKERS_IN_PROCESS=1` — and has no setting of its own. It logs what to look for:

| Log line | Level | Meaning |
| --- | --- | --- |
| `events delivery failed` | warn | One attempt failed; it is retried after the backoff |
| `events delivery is dead` | error | The eighth attempt failed; the delivery waits for a retry or a skip |
| `events deliveries wait for handlers that are not composed` | warn | Deliveries name a handler this release no longer has, usually after a downgrade or a removed module; skip them |
| `events dispatch failed` | error | A pass of the dispatcher failed, the database most often; the next pass tries again |

### List the stuck deliveries

`GET /management/events/deliveries?state=dead` (or `state=failed`; with no `state`,
both) answers at most 200 deliveries, the latest changed first, with
`Cache-Control: no-store`. Any other `state` is `400`.

```json
{
  "deliveries": [
    {
      "id": 812,
      "eventId": 5531,
      "event": "orders.sales_order.confirmed",
      "version": 1,
      "module": "orders",
      "aggregate": "orders.sales_order",
      "aggregateId": "1042",
      "handler": "customers.timeline_orders",
      "state": "dead",
      "attempts": 8,
      "lastError": "…",
      "note": null,
      "nextAttemptAt": "2026-11-05T21:14:03Z",
      "occurredAt": "2026-11-02T09:12:44Z",
      "createdAt": "2026-11-02T09:12:47Z",
      "updatedAt": "2026-11-05T09:14:03Z"
    }
  ]
}
```

| Field | Meaning |
| --- | --- |
| `id` | The delivery's id, which the two calls below take |
| `eventId`, `event`, `version`, `module`, `aggregate`, `aggregateId` | The event: its id, its name and payload version, the module that published it, the kind of thing it is about (`orders.sales_order`) and that thing's id |
| `handler` | The handler it is delivered to, `<module>.<what>` |
| `state` | `failed` (retrying) or `dead` |
| `attempts` | Failed attempts so far |
| `lastError` | The last failure, at most 2 000 characters |
| `note` | The operator's note, set by a skip |
| `nextAttemptAt` | When a failed delivery is next tried |
| `occurredAt` | When the event happened |
| `createdAt`, `updatedAt` | When the delivery was written and last changed |

An event's payload is not shown. Payloads carry ids, numbers, amounts and states only,
never a person's name, address, e-mail or phone.

### Retry one

`POST /management/events/deliveries/{id}/retry` sets a `failed` or `dead` delivery back
to pending, due at once: `204`. Its attempts are kept, so a dead delivery that fails
again is dead again at once — fix the cause first, then retry. No such delivery is
`404`; one in another state (pending or done) is `409`.

### Skip one

`POST /management/events/deliveries/{id}/skip` with `{"note": "…"}` marks a delivery
that is not done as done without handling it, keeping the note, and so lets the
deliveries waiting behind it go: `204`. The note is required, at most 500 characters
(`400` otherwise). No such delivery is `404`; one already done is `409`. Skipping means
the handling never happens — whatever the handler would have written (a confirmation
queued, a line on a timeline) is not written.

### Verify

List again: the delivery is gone from `state=dead`. A retried one shows up as `failed`
if it fails again, or `dead` at once if it was dead before; one that succeeded is listed
no more.

Events whose deliveries are all done are deleted after 30 days.

## Keep it private

The public listener refuses any request whose `Host` is not `APP_URL`'s. The
management listener deliberately has **no host filter**, so that it can be
reached by a service name or a pod address. The bearer token is therefore its
only protection: never publish the port, never route it from the internet, and
restrict it at the network layer (in Kubernetes, a NetworkPolicy admitting only
the control plane's namespace).

Rotating the token means restarting the process with a new value.

If the management listener itself fails after startup — the port it bound
stops accepting, for instance — the failure is logged and the instance
keeps serving the public listener regardless: a control plane sees
connection refused against an otherwise healthy pod, not a crash.

## Seating the first Owner without `/setup`

Pair the listener with `BOOTSTRAP_OWNER_EMAIL`. The anonymous `/setup` flow is
then closed, and `bootstrap` moves `pending` → `invited` → `completed` as an
invitation is sent to that address and accepted.

The invitation follows the configured address, not a fixed one: on every
start, while no Owner exists, the process revokes any pending Owner
invitation addressed to a *different* email than the currently configured
one and invites the current address instead. This is what lets an operator
who mistyped the address correct it — restart with the fix, and the
mistyped recipient's token stops working; the next start mails the
corrected address.

A pending invitation to the current address is not sent again while it is
still valid — restarting does not resend it. If it expires before anyone
uses it, the next start issues a fresh one (a new token; the old one is no
longer accepted). If `BOOTSTRAP_OWNER_EMAIL` names an address some account
already holds, no invitation is ever issued: `/setup` stays closed and
`bootstrap` stays `pending`, the warning is logged at every start, not just
the first, and the only recovery is pointing the variable at a different
address. If the mail send itself fails, the invitation is revoked and the
failure is logged without startup stopping; the next start retries. A
pending invitation cannot be forced to resend before it expires other than
by correcting the address, since `/setup` and the owner-invitation
management endpoints are both closed while `BOOTSTRAP_OWNER_EMAIL` is set
and no Owner exists. To get a fresh invitation before that one expires —
mail lost in transit, or a process killed between issuing and sending — set
`BOOTSTRAP_OWNER_EMAIL` to another address you control, restart (this
revokes the pending invitation), set it back to the intended address, and
restart again; a plain restart does not resend.

While no Owner exists, every start attempts the invitation mail before the
process starts answering requests: with an unreachable SMTP relay, that
shows up as a slow start, not as a mail error anywhere in the response
path.

Whichever path seats it — `/setup` or an accepted invitation — the
installation's first Owner always receives both `Owner` and `SystemAdmin`
and writes the same bootstrap marker, so an installation whose first Owner
was seated by invitation is bootstrapped exactly as one set up through
`/setup`.
