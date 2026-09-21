# Management listener

A control plane that runs many Vantigo instances needs to ask each one three
things without holding a user session: which version is running, whether its
first Owner has been seated, and how much it is being used. The management
listener answers exactly that, and nothing else.

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
  "usage": { "users": 12, "activeUsers": 9, "databaseBytes": 734003200 }
}
```

| Field | Meaning |
| --- | --- |
| `version` | The build version, the same string `/health/ready` reports |
| `bootstrap` | `pending`: no Owner and no invitation that can be accepted. `invited`: an Owner invitation is waiting. `completed`: an Owner exists |
| `usage.users` | Every account |
| `usage.activeUsers` | Accounts that can sign in: not disabled and not locked out, and, while SCIM is enabled, not deprovisioned upstream — except an Owner, who stays counted as the break-glass account even then. It says nothing about recency |
| `usage.databaseBytes` | `pg_database_size` of the instance's database |

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

## Keep it private

The public listener refuses any request whose `Host` is not `APP_URL`'s. The
management listener deliberately has **no host filter**, so that it can be
reached by a service name or a pod address. The bearer token is therefore its
only protection: never publish the port, never route it from the internet, and
restrict it at the network layer (in Kubernetes, a NetworkPolicy admitting only
the control plane's namespace).

Rotating the token means restarting the process with a new value.

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
already holds, no invitation is sent — a warning is logged instead, since
that account is not necessarily the Owner. If the mail send itself fails,
the invitation is revoked and the failure is logged without startup
stopping; the next start retries. A pending invitation cannot be forced to
resend before it expires other than by correcting the address, since
`/setup` and the owner-invitation management endpoints are both closed
while `BOOTSTRAP_OWNER_EMAIL` is set and no Owner exists.

Whichever path seats it — `/setup` or an accepted invitation — the
installation's first Owner always receives both `Owner` and `SystemAdmin`,
and the same bootstrap marker is written either way. This also closes a
previous gap where an installation whose only (invited) Owner was later
removed counted as un-bootstrapped again.
