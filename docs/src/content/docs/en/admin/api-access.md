---
title: API access
description: Letting another application use Vantigo's API without a person's session — integrations, their roles, API keys with an expiry, a narrowing, a read-only switch and networks, rolling and revoking a key, what is never available to a key, how a client sends it, the answers it can get, and what to do when a key leaks.
sidebar:
  order: 22
sources:
  - apps/server/internal/identity/integrations.go
  - apps/server/internal/identity/access_apikey.go
  - apps/server/internal/identity/apikey.go
  - apps/server/internal/identity/workers.go
  - apps/host/frontend/src/routes/workspace/api-access.tsx
  - apps/host/frontend/src/routes/workspace/api-access_.$integrationId.tsx
  - apps/host/frontend/src/routes/workspace/-api-access-integration.tsx
  - apps/host/frontend/src/routes/workspace/-api-key-dialog.tsx
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
600). The rate limit is kept with the integration but not yet enforced. Saving replaces the description and the contact with what the form holds, so clearing a
field clears it. If someone else changed the integration since you opened it, the save is
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
`401 unauthenticated`, as an unknown key is, so the caller learns nothing about the key, and
its **Last used** is not updated. The address is the client's as the server sees it through
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

A key can be rolled once: rolling it again is refused `409 api_key_already_rolled`, and a
revoked or expired key cannot be rolled (`409 api_key_revoked`). Making and rolling keys is
limited to 30 a minute from one address.

## Revoke a key

In the key's row, choose **Revoke** and confirm. The key is refused `401 unauthenticated`
from its next request, on every replica at once: nothing caches a key. It cannot be undone;
make a new key if the application needs one.

## Disable, enable or archive an integration

Under **Actions**:

- **Disable** refuses every key of the integration at once, until you choose **Enable**. Use
  it to stop an application while you find out what it is doing.
- **Archive** revokes every key, frees the name and keeps the integration, read-only, for
  what it did. It cannot be undone.

## When a key leaks

If a key may have been seen by someone who should not have it — committed to a repository,
pasted in a ticket, left on a laptop:

1. **Revoke** it at once. If you do not know which key leaked, **Disable** the integration
   first: that stops every key while you look.
2. Make a new key, or **Roll** the others the application uses, and put them in the
   application. Enable the integration again if you disabled it.
3. Check what the integration did: what it made and changed is attributed to it, and
   identity's audit trail records every change to the integration and its keys (below). The
   key's **Last used** says when, and from which address, it was used last.
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

## The answers a key can get

| Status | Code | When |
| --- | --- | --- |
| `401` | `unauthenticated` | The key is malformed, unknown, revoked, or its integration is disabled or archived; the request came from outside its networks; or it carried several `Authorization` headers |
| `401` | `api_key_expired` | The key matched but has expired |
| `403` | `forbidden` | A person's or an Owner's operation, or a permission the key does not reach: not in its roles, never available to API keys, or outside its narrowing |
| `403` | `api_not_available` | The operation is never available to API keys |
| `403` | `api_key_read_only` | A read-only key on an operation that is not a read |

A `401` carries `WWW-Authenticate: Bearer realm="vantigo"`. The body is the same JSON error
the API answers people with: `{"error": {"code": "…", "message": "…"}}`.

## What is audited

Identity's authorization audit trail records, with the Owner who did it and whether they used MFA:
`integration.created`, `integration.updated`, `integration.roles_changed`,
`integration.disabled`, `integration.enabled`, `integration.archived`, `api_key.created`,
`api_key.rolled` and `api_key.revoked`. The `identity.api-keys` worker records
`api_key.expired` once for each key that passes its expiry, as `system:api-keys`, and deletes
a key's row 90 days after it expired; the audit rows stay
([Configuration](/en/admin/configuration/#workers)). Names, roles, the narrowing, read-only,
the networks and the expiry are recorded; a key's secret and its hash never are.

## What can go wrong

- **The application gets `401 unauthenticated` with a new key.** Check that it sends
  `Authorization: Bearer` and the whole key, that the integration is *Active*, and that its
  address is within the key's **Networks** as the server sees it behind your proxy.
- **It gets `403 forbidden`.** The roles do not give the permission, the narrowing leaves it
  out, or the permission is never available to API keys. The integration's page shows which.
- **Making a key answers that the expiry is wrong.** The date must be in the future and at most
  365 days ahead.
- **A key was lost.** It cannot be shown again: roll it.
