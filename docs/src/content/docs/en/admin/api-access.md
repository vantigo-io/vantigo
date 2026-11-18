---
title: API access
description: Letting another application use Vantigo's API without a person's session — integrations, their roles, API keys with an expiry, a narrowing, a read-only switch and networks, rolling and revoking a key, what is never available to a key, how a client sends it, the rate limit and its headers, the answers it can get, the request log, the expiry notices, webhook endpoints and their signing secrets, and what to do when a key leaks.
sidebar:
  order: 22
sources:
  - apps/server/internal/identity/integrations.go
  - apps/server/internal/identity/access_apikey.go
  - apps/server/internal/identity/apikey.go
  - apps/server/internal/identity/workers.go
  - apps/server/internal/identity/apirequests.go
  - apps/server/internal/identity/apikey_notices.go
  - apps/server/internal/identity/integration_requests.go
  - apps/host/frontend/src/routes/workspace/api-access.tsx
  - apps/host/frontend/src/routes/workspace/api-access_.$integrationId.tsx
  - apps/host/frontend/src/routes/workspace/-api-access-integration.tsx
  - apps/host/frontend/src/routes/workspace/-api-key-dialog.tsx
  - apps/host/frontend/src/routes/workspace/-api-access-requests.tsx
  - apps/host/frontend/src/routes/workspace/-api-access-webhooks.tsx
  - apps/host/frontend/src/routes/workspace/-webhook-secret-dialog.tsx
  - apps/server/internal/identity/webhooks.go
  - apps/server/internal/identity/webhooks_url.go
  - apps/server/internal/identity/webhooks_secret.go
  - apps/server/internal/identity/webhooks_signer.go
  - apps/server/internal/identity/webhooks_client.go
  - apps/server/internal/identity/webhooks_handler.go
  - apps/server/internal/identity/webhooks_worker.go
  - apps/server/internal/contracts/webhook_events.go
  - apps/server/internal/apiref
  - apps/apiref/frontend/src
---

An **integration** is the account another application — a web shop, a reporting tool, a
script — uses Vantigo's API as. It holds roles as a person does, it never signs in, and it
acts only with one of its **API keys** on the request. What it makes and changes is
attributed to it, by name, with an **API** badge on its avatar.

Integrations and their keys are made under **Workspace admin › API access**. How a key is
judged on each request is under
[Identity, authentication and deployment](/en/admin/authentication/#integration-users).

## Before you start

- You are an **Owner**. Every operation behind the screen is the Owner's, and when the
  workspace requires Owners to use MFA, it is refused until you have it. An administrator
  to whom role administration is delegated does not see the screen.
- The roles the integration is to hold exist as **custom roles** under **Roles & access**.
  An integration can hold only custom roles: never **Owner**, **SystemAdmin** or **User**.
  Make a role for each application with exactly what it needs.
- You know where the application will keep its key — a secret store or its configuration —
  because the key is shown once.

## Create an integration

1. Open the avatar menu, choose **Workspace admin**, then **API access**. The list shows
   each integration's **Status** (*Active*, *Disabled* or *Archived*), its **Roles**, its
   **Keys in force**, the **Nearest expiry** among them and when a key was **Last used**.
2. Choose **New integration**. Give it a **Name** (1 to 100 characters, unique among the
   integrations that are not archived), a **Description** if it helps, a **Contact** — the
   person responsible for it, who must be a person with an account — and its **Roles**.
3. Choose **Create integration**. Its page opens.

To change it later, choose **Actions › Edit details**: the name, the description, the
contact and the **Rate limit** (60 to 6,000 requests a minute across its keys; the default is
600; see [The rate limit](#the-rate-limit)). The contact gets the keys'
[expiry notices](#expiry-notices). Saving replaces the description and the contact with what the form
holds, so clearing a field clears it. If someone else changed the integration since you opened it, the save is
refused and the page shows the latest; make the change again.

## Give it roles

On the integration's page, **Roles** is the same picker **Roles & access** uses, offering the
custom roles. Choose **Save roles** to replace its roles. The next request of every key sees
the new roles.

Below, **What its roles allow** lists every permission the roles give the integration:

- **Never available to API keys** marks a permission no key ever holds, whatever the roles
  say: every `identity:` and `agents:` permission, and every permission that cannot be
  delegated. A request that needs one is refused with a `403`.
- **Sensitive** marks a permission that reaches personal or financial data. A key holding it
  can read or change that data in bulk, so give it only to an integration that needs it.

Only an Owner can change an integration's roles, and only on this page: an integration is
not listed under **Roles & access**, and the database refuses a role given to it any other
way. A delegated administrator who stewards a role an integration holds cannot change that
role either (`403 role_edit_denied`), because adding a permission to it would widen every
key of the integration.

## Make a key

1. Under **API keys**, choose **New API key**.
2. Give it a **Name**, such as *Production*.
3. Choose its **Expiry**: 30, 90, 180 or 365 days, or **On a date** at most 365 days ahead.
   Every key expires; the default is 90 days.
4. Optionally **Narrow the key** (below), turn on **Read only** (below) and list its
   **Networks** (below).
5. Choose **Create key**. The key appears **once**, with **Copy** and a `curl` example
   against this installation's address. Copy it into the application's secret store and
   choose **I have copied the key**. Vantigo keeps only a hash: the key cannot be shown
   again, and closing the dialog forgets it. If it is lost, roll the key.

The keys table shows each key's **Name**, its recognisable form (`vtg_4f2a…9XyZ`), its
status (*Active*, *Expired* or *Revoked*), its **Expiry**, its **Narrowing**, whether it is
**Read only**, its **Networks** and when and from which address it was **Last used** (to the
minute).

### Narrow a key

A key that is not narrowed may use everything the roles allow. **Narrow the key** limits it
to the permissions you tick, of those the roles grant, grouped by module as in the role
editor. A permission that is never available to API keys is shown and cannot be ticked.

**Show the permissions the roles do not grant** lists the rest of the catalog. A key may name
one: it does nothing until the roles grant it, and the keys table marks it **Not granted by
the roles**. A permission a later release of Vantigo removes is kept in the narrowing and
ignored, and the table strikes it through.

### Make a key read-only

**Read only** lets the key call only the operations that read. Which operations those are is
decided by each operation in Vantigo's API contract, not by the name of the permission it
needs. A read-only key that calls anything else is refused `403 api_key_read_only`.

### Restrict a key's networks

**Networks** lists the addresses or CIDR ranges the key may be used from, one per line —
IPv4 or IPv6, at most 20. Empty means anywhere. A request from outside them is refused
`401 unauthenticated`, as an unknown key is, even when the key has also expired or been
revoked, so the caller learns nothing about the key. **Last used** records only a request the
key was let through for, never a refused one. The address is the client's as the server sees it through
`TRUSTED_PROXY_HOPS` and `TRUSTED_PROXY_CIDRS`
([Forwarded headers and HTTPS](/en/admin/authentication/#forwarded-headers-and-https)):
behind a proxy that is not trusted, every request has the proxy's address.

## Roll a key

Rolling replaces a key without a moment in which the application has none.

1. In the key's row, choose **Roll**.
2. Choose the **Overlap** — how long the old key keeps working: none, 1 hour, 24 hours (the
   default), 3 days or 7 days — and the new key's **Expiry**.
3. Choose **Roll**. The new key appears once, as when it was made. It has the old key's name,
   narrowing, read-only setting and networks.
4. Put the new key in the application before the overlap ends. The old key then expires on
   its own; it is marked **Rolled** until then.

A roll keeps the old key working through the overlap. If the key has leaked, revoke it
instead ([When a key leaks](#when-a-key-leaks)): the dialog says so too.

A key can be rolled once: rolling it again is refused `409 api_key_already_rolled`, and a
revoked or expired key cannot be rolled (`409 api_key_revoked`). Making and rolling keys
share identity's user-management limit (UserManagement) of 30 requests a minute from one
address, with the other user-management operations.

## Revoke a key

In the key's row, choose **Revoke** and confirm. The key is refused `401 unauthenticated`
from its next request, on every replica at once: nothing caches a key. It cannot be undone;
make a new key if the application needs one.

## Disable, enable or archive an integration

Under **Actions**:

- **Disable** refuses every key of the integration at once, until you choose **Enable**. Use
  it to stop an application while you find out what it is doing.
- **Archive** revokes every key, disables every [webhook endpoint](#webhooks), frees the name
  and keeps the integration, read-only, for what it did. It cannot be undone.

## When a key leaks

If a key may have been seen by someone who should not have it — committed to a repository,
pasted in a ticket, left on a laptop:

1. **Revoke** it at once. If you do not know which key leaked, **Disable** the integration
   first: that stops every key while you look.
2. Make a new key, or **Roll** the others the application uses, and put them in the
   application. Enable the integration again if you disabled it.
3. Check what the integration did: what it made and changed is attributed to it, and
   identity's audit trail records every change to the integration and its keys (below). The
   **Requests** tab ([The request log](#the-request-log)) lists what each key called over the
   last 30 days, from which address, and what was refused; filter it by the key. The key's
   **Last used** says when, and from which address, it was used last.
4. If the key held sensitive permissions, treat the data they reach as exposed.

## How a client sends the key

The application sends the key in the `Authorization` header of every request, with the
`Bearer` scheme:

```sh
curl -H "Authorization: Bearer vtg_…" https://vantigo.example.com/api/v1/customers
```

- A key is read only from that header — never from a query string or a cookie — and a
  request must carry one `Authorization` header: several are refused `401 unauthenticated`.
- A request with a key is judged as the key's alone, even when the browser also sends a
  session cookie, and a refused key never signs a person out.
- A key starts with `vtg_` and has 53 characters, with a checksum, so a secret scanner can
  recognise one and a mistyped key is refused without a database read.
- A key reaches only operations behind a permission, or open to anyone; never a person's own
  account, an Owner's administration, SSO, SCIM or the keys themselves. Every operation that is
  never available to a key says so in the generated API reference (`/en/reference/api/`).

## The rate limit

Every request an integration's keys make is counted against the integration's **Rate limit**,
per minute, across all its keys: adding keys does not raise it. A request is counted once its
key has been let through, so a refused key never uses up the integration's allowance. The
limits identity and the modules set per address on single operations — signing in, making
keys — still apply as well.

Every answer to a request a key was let through for carries three headers:

| Header | Says |
| --- | --- |
| `RateLimit-Limit` | The integration's limit a minute |
| `RateLimit-Remaining` | How many more requests this minute allows |
| `RateLimit-Reset` | The seconds until the minute ends and the count starts again |

A request over the limit is refused `429 rate_limited` with `Retry-After`, the seconds to wait.
An application should slow down when `RateLimit-Remaining` nears zero and wait out
`Retry-After` when it gets a `429`.

The count is kept in the database, so every replica sees the same one. Each replica adds its
own requests to it every second, whether or not more arrive, and once more as it shuts down, so
it is at most about a second of its own requests behind. A workspace with several replicas may
therefore let through up to a second's worth of requests more than the limit before they all
refuse.

## Trying the API at /api

Vantigo serves an interactive reference of its API at `/api` on the application's own
address, for example `https://vantigo.example.com/api`. Open it in a browser:

1. **Signed in, without a key,** it lists every operation an API key can call, with the
   permission each requires. A call you try there with **Test Request** goes with your own
   session and is judged as your call, as the page says above the list.
2. **To see what a key reaches,** paste it into **API key** and choose **Use key**. The list
   is cut down to the operations that key can call — a read-only key's to its reads — and
   **Test Request** sends the key, so the call is judged, refused and logged as the key's.
3. **Clear** (or emptying the field) puts the page back to your session.
4. **Neither signed in nor with a key,** the page says how to get a key and links the
   [introduction to the API](/en/reference/api-introduction/).

The key stays in the page: it is not stored in the browser, not sent anywhere but this
installation, and gone when you leave or clear it. A key refused at **Use key** is named
with its code (see below), and a refused key never signs you out. Nothing on the page is
fetched from outside the installation. The page is not served on the public host.
**Test Request** works only when the page is opened at the installation's own address,
`APP_URL`: the document names that address as its server, and the page's content security
policy refuses requests to any other origin.

A program can load the same document itself: `GET /api/openapi.json` with the key in the
`Authorization` header answers the OpenAPI document of what that key reaches, ready for a
client generator; a session asking `?view=api` gets every operation a key can call
([The API contract document](/en/admin/transport-security/#the-api-contract-document)).
Loading it with a key counts against the integration's rate limit and shows in its request
log, as any other call does.

## The answers a key can get

| Status | Code | When |
| --- | --- | --- |
| `401` | `unauthenticated` | The key is malformed, unknown, revoked, or its integration is disabled or archived; the request came from outside its networks; or it carried several `Authorization` headers |
| `401` | `api_key_expired` | The key matched, from within its networks, but has expired |
| `403` | `forbidden` | A person's or an Owner's operation, or a permission the key does not reach: not in its roles, never available to API keys, or outside its narrowing |
| `403` | `api_not_available` | The operation is never available to API keys |
| `403` | `api_key_read_only` | A read-only key on an operation that is not a read |
| `429` | `rate_limited` | The integration has made its **Rate limit** of requests this minute; `Retry-After` says how long to wait |

A `401` carries `WWW-Authenticate: Bearer realm="vantigo"`. The body is the same JSON error
the API answers people with: `{"error": {"code": "…", "message": "…"}}`.

## The request log

The integration's page has a **Requests** tab: what its keys called over the last 30 days.
Every request a key made that Vantigo could tell was the key's is there, the refused ones too;
a request with a key that matched no key is not, because there is nothing to tie it to.

- **Requests per day** counts them per day and status class — *2xx success*, *3xx redirect*,
  *4xx refused* and *5xx server error* — as a chart and a table. Days are UTC days.
- **Called most** lists the ten operations called most.
- **Latest requests** lists the 200 newest: when, in UTC, the key, the method and the operation, the
  status — with why, for a refusal — how long it took, the client's address and the trace id.
  A key whose row has been deleted, 90 days after it expired, shows as *Deleted key*.
- **Key**, **Status**, **Refused because** and **Day** filter all three.

A refusal is recorded with its reason:

| Reason | The request was refused because |
| --- | --- |
| *Expired* (`expired`) | the key had expired |
| *Revoked* (`revoked`) | the key was revoked |
| *Integration disabled* (`disabled`) | the integration was disabled |
| *Integration archived* (`archived`) | the integration was archived |
| *Outside its networks* (`network`) | the request came from outside the key's **Networks** |
| *Read-only key* (`read_only`) | a read-only key called an operation that is not a read |
| *Not available to API keys* (`never`) | the operation is never available to API keys |
| *Not allowed by the roles* (`forbidden`) | the roles, or the narrowing, do not give what the operation needs |
| *Over the rate limit* (`rate_limited`) | the integration had made its **Rate limit** of requests that minute |

A revoked key, a disabled or archived integration and a request from outside the networks are
all answered `401 unauthenticated`, as an unknown key is, so the client learns nothing about the
key; the log says which it was. The others get the code in
[The answers a key can get](#the-answers-a-key-can-get).

The log holds the operation, never the path, a query string or a body, because those can
carry personal data. The trace id finds the request in the server's own traces and log lines.
Rows are written in batches every two seconds, so a request shows on the tab a few seconds
after it was made; a server that stops abruptly may lose its last two seconds. When the
database cannot keep up, rows are dropped rather than slowing requests down, and counted in
the `vantigo.identity.api_requests.dropped` metric. The log is an operational record, not the
audit trail.

The log keeps 30 days, today included: older rows are deleted by the `identity.api-keys` worker
([Configuration](/en/admin/configuration/#workers)). With the worker switched off, nothing is
ever deleted from the log.

## Expiry notices

Vantigo mails a notice about a key 14 days before it expires, 3 days before, and on the day
(UTC days). Each goes to the integration's **Contact**, if that person's account is active, and
to every active Owner, in each recipient's language, from the address invitations come from.
A contact whose account is disabled stays on the integration and is skipped; the Owners still
get the notice.

- Each notice is sent once. A key made with less than 14 days left gets only the notices
  still ahead of it.
- A key that has been rolled or revoked gets none: rolling a key is what the notice asks for.
- The notice names the key and the integration, never the key itself, and says to roll the
  key under **Workspace admin › API access** and give the new one to the application.
- A notice that reached no one — the mail failed, or no recipient is active with an address mail
  reaches — is tried again on the next run. One that reached someone is not sent again to those
  it missed. If the server stops in the moment between recording a notice and mailing it, that
  notice is not sent: each goes at most once.

The notices are sent by the `identity.api-keys` worker; with it switched off, none are sent.

## Webhooks

A **webhook endpoint** is an HTTPS address of the application to which Vantigo is to post a
short, signed message when something happens — a sales order confirmed, an invoice overdue.
The message names the resource and nothing else; the application fetches it with its API key.
What the message holds, how it is signed and how the application checks it are in the
reference, [Webhooks](/en/reference/webhooks/).

Messages are sent by the `identity.webhook-sender` worker, within seconds of the event, and a
failed one is retried for about two days ([Configuration](/en/admin/configuration/#workers)).
The delivery log, with resend, comes in the next release.

### Add an endpoint

1. On the integration's page, open the **Webhooks** tab and choose **New endpoint**.
2. Give its **URL**: an `https://` address on port 443 with a host name — not an IP address,
   `localhost` or a name under `.local`, `.localhost` or `.internal` — with no user name,
   password or `#` part. A query string is allowed. Add a **Description** if it helps.
3. Tick its **Events**. They are grouped by module, and only those the integration's roles let
   it receive are offered: an event about a sales order needs a role with access to Orders, for
   example. If none is offered, give the integration a role first.
4. Choose **Create endpoint**. The **signing secret** (`whsec_…`) appears **once**, with
   **Copy**. Put it where the application checks messages and choose **I have copied the
   secret**. Vantigo keeps it sealed and never shows it again; if it is lost, roll it.
5. Vantigo sends the new endpoint a **test event** at once, a `webhook.ping` message. Check that
   the application received it and accepted its signature.

Vantigo looks the host name up when you save. An address that resolves to a private, loopback
or otherwise reserved address is refused (*The address resolves to an address Vantigo never
sends to.*), as is one that does not resolve (*The host name does not resolve.*). Messages only ever go to public addresses,
checked again every time one is sent, so an application on your own network needs a public
address or a tunnel.

An integration has at most 5 endpoints, and the workspace 50. The tab shows each endpoint's
full URL, query included — the only place it is shown — its events and its status.

### Send a test event

In an enabled endpoint's row, choose **Actions › Send test event**. *Test event queued* says a
`webhook.ping` message is on its way; it is sent once, within seconds, with no retry, and it
never counts toward disabling the endpoint. Use it to check the URL, the secret and the
application's signature check. If the application answers `410 Gone`, the endpoint is
disabled as for any message.

### Change, disable or delete an endpoint

In the endpoint's row, under **Actions**:

- **Edit endpoint** changes its URL, description and events, by the same rules. Changing the
  URL keeps the secret. If someone else changed the endpoint since you opened it, the save is
  refused and the tab shows the latest; make the change again.
- **Disable** stops it, after you confirm: nothing more is sent to it, and the messages
  waiting for it are dropped. **Enable** starts it again with the events that happen from then
  on, and sends it a test event.
- **Delete**, after you confirm, deletes the endpoint and its deliveries. It cannot be undone.

The status says why an endpoint is disabled: by an Owner, because it kept failing for 5 days,
because it answered `410 Gone`, or because the integration was archived. Archiving an
integration disables its endpoints; they can then only be deleted. Disabling the integration
itself pauses its endpoints: nothing is sent while it is disabled, and when you enable it
again a message that has been due for more than 24 hours is dropped and the rest is sent.

### When Vantigo disables an endpoint

A message that fails — any answer but `2xx`, no answer within 15 seconds, an address that
cannot be reached — is tried again after 30 seconds, 5 minutes, 30 minutes, 2 hours, 6 hours,
12 hours and 24 hours, then given up. While an enabled endpoint's messages fail, its status
says **Failing**, with since when and when it last received one. Vantigo disables it:

- when it has failed for **5 days** and at least **5 messages** were given up after every
  retry — the status *Disabled: it failed for 5 days*;
- at once, when it answers **`410 Gone`**, which says the address is no longer in use — the
  status *Disabled: it answered 410 Gone*.

The messages waiting for it are dropped, and the integration's contact and every Owner get
**one mail**, in their own language, naming the endpoint by its address's host and saying
why. The tab shows a banner, *Endpoint disabled: …*, with the reason.

To start it again:

1. Fix the application, or the endpoint's URL with **Edit endpoint**.
2. Choose **Enable** in the banner, or **Actions › Enable**. The endpoint starts afresh with no
   failure counted and is sent a test event at once.
3. Check that the application received the test event. If it fails again, the endpoint is
   disabled again and a new mail is sent.

The events that happened while it was disabled are not sent: the application fetches what it
missed through the API.

### Roll a secret

Rolling gives an endpoint a new secret without a moment in which the application cannot check a
message.

1. In the endpoint's row, choose **Actions › Roll secret**.
2. Choose the **Overlap** — how long the old secret keeps signing beside the new one: 1 hour,
   24 hours (the default), 3 days or 7 days.
3. Choose **Roll secret**. The new secret appears once, as when the endpoint was made.
4. Put it in the application before the overlap ends. Until then every message carries a
   signature by each secret, so the application accepts it with either; the row says until
   when the previous secret signs.

Rolling again while an overlap runs ends the older secret at once, and the dialog warns of it.
If a secret may have leaked, roll it with the shortest overlap.

## What is audited

Identity's authorization audit trail records, with the Owner who did it and whether they used MFA:
`integration.created`, `integration.updated`, `integration.roles_changed`,
`integration.disabled`, `integration.enabled`, `integration.archived`, `api_key.created`,
`api_key.rolled` and `api_key.revoked`. The `identity.api-keys` worker records
`api_key.expired` once for each key that passes its expiry, as `system:api-keys`, and deletes
a key's row 90 days after it expired; the audit rows stay
([Configuration](/en/admin/configuration/#workers)). Names, roles, the narrowing, read-only,
the networks and the expiry are recorded; a key's secret and its hash never are.

For webhook endpoints it records `webhook.created`, `webhook.updated`, `webhook.deleted`,
`webhook.enabled`, `webhook.disabled` (also once for each endpoint an archive disables, and as
`system:webhooks` when Vantigo disables one), `webhook.secret_rolled` and `webhook.test_sent`.
Of an endpoint's URL it keeps only the scheme and host, with a short
digest that shows when the rest changed: services such as Zapier and Slack carry their token
in the path. The signing secret is never recorded.

## What can go wrong

- **The application gets `401 unauthenticated` with a new key.** Check that it sends
  `Authorization: Bearer` and the whole key, that the integration is *Active*, and that its
  address is within the key's **Networks** as the server sees it behind your proxy.
- **It gets `403 forbidden`.** The roles do not give the permission, the narrowing leaves it
  out, or the permission is never available to API keys. The integration's page shows which.
- **It gets `429 rate_limited`.** The integration made more requests this minute than its
  **Rate limit**, across all its keys. Have the application wait out `Retry-After` and watch
  `RateLimit-Remaining`, or raise the limit under **Actions › Edit details**. **Called most** on
  the **Requests** tab shows what it spends its requests on.
- **A request is not on the Requests tab.** Wait a few seconds: rows are written in batches.
  A request with a key that matched no key is never recorded; check the key the application
  sends.
- **Making a key answers that the expiry is wrong.** The date must be in the future and at most
  365 days ahead.
- **A key was lost.** It cannot be shown again: roll it.
- **An endpoint's URL is refused.** It must be `https://` on port 443 with a host name that
  resolves to public addresses only; an application on your own network needs a public address
  or a tunnel.
- **An event is not offered.** The integration's roles do not give the permission the event's
  resource is read with; the reference lists each event's permission.
- **A webhook secret was lost.** It cannot be shown again: roll it.
- **The application gets no messages.** Send a test event. If it does not arrive, check that the
  URL is reachable from the internet and answers `2xx` within 15 seconds without a redirect,
  and that the integration and the endpoint are enabled. An operator can check that the
  `identity.webhook-sender` worker runs ([Configuration](/en/admin/configuration/#workers)).
- **The application rejects the signature.** It must check the raw body before parsing it,
  with the secret's bytes after `whsec_` decoded from base64; during a roll's overlap it gets
  two signatures and should accept either.
- **An endpoint was disabled.** See [When Vantigo disables an endpoint](#when-vantigo-disables-an-endpoint).
