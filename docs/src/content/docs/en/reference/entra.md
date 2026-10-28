---
title: "Microsoft Entra ID module"
description: "Microsoft Entra ID as Staff's identity provider: the connection to one tenant, its client secrets in the plural and the connection test; the status and its reasons, read by Staff with no network call; the account rules; the guarded Graph client and its limits; the user lifecycle per step — prepared disabled, enabled on the day with the hire date, the licences and a Temporary Access Pass, kept in step, disabled with the leaver's steps, soft-deleted — with the Graph calls and the codes; the account's photo, converted to Graph's JPEG and written or cleared; the address rule; the pass; the sensitive actions and role_missing; Staff's groups in the tenant — the six traits and the cached licences, the create, or the adoption an entra:manage holder confirms, with the service principal as an owner, the team, the members by $ref, the delete or the retirement; connecting existing users — the directory search and the exact matches, read through contracts.SSOConnections; the calendar sync switch and its status, Calendars.ReadWrite asked for only while it is on, and the mailbox calendar the module fills for the calendar's Outlook sync — the status rule, the account check, the Graph calls and $batch, the marker, the all-day zones, the reads of the default calendar's delta and the marker, the write-back's reads of the copies' versions and of events whole, and the refusals; the events, the permission, the refusals and the endpoints."
sources:
  - apps/server/internal/entra
  - apps/entra/frontend
  - openapi/entra.yaml
  - apps/server/internal/openapi/specs/entra.yaml
  - apps/server/internal/db/migrations/00061_entra_baseline.sql
  - apps/server/internal/db/migrations/00063_entra_groups.sql
  - apps/server/internal/db/migrations/00082_entra_calendar_sync.sql
---
The Microsoft Entra ID module connects one installation to one Microsoft Entra ID tenant
and keeps the accounts of the people Staff registers in it: created disabled ahead of
the start, enabled on the first day with a Temporary Access Pass, kept in step as the
person moves, disabled the day after the end, and deleted when Staff asks. It is a
vertical-slice module inside the single Vantigo binary (`apps/server/internal/entra`),
owns the `entra` schema in the shared PostgreSQL database, and serves
`openapi/entra.yaml` under `/api/v1/entra`. Its app is `apps/entra/frontend`
(`@vantigo/entra-ui`), the tile **Microsoft Entra ID**.

**Staff decides and the module executes.** The module is the first provider in the
many-provider slot `IdentityProvisioners`: it fills `contracts.IdentityProvisioner`
under the key `entra`, and Staff's accounts worker is the only caller of the slot
([module boundaries](/en/contributing/module-boundaries/) rule 13; [the provider
link](/en/reference/staff/#the-provider-link)). Which person gets an account, when it is
enabled and when it goes is Staff's decision from the register; the module answers what
it did in the tenant. It has no background worker of its own. That is why `MODULES`
refuses `entra` without `staff` (`entra requires staff`): without Staff the module would
hold a tenant-wide credential that nothing uses.

It depends on nobody but identity, and reads of identity only what
`contracts.SSOConnections` answers an Owner — the users not yet connected to the
workforce provider and who already holds a directory account's keys — for
[connecting existing users](#connecting-existing-users); the Owner's check and
`entra:manage`'s are the router's. It writes nothing
outside its own schema, and nothing in it imports another module. It reaches the
internet in one place, the [Graph client](#the-graph-client), and only two fixed hosts
there.

It also keeps **Staff's groups** in the tenant ([groups in the
tenant](#groups-in-the-tenant)): Staff decides which groups exist, who is in them and
what [traits](#the-traits) they carry; the module makes the group, with its own service
principal and the group's owners as owners, and keeps its name, its members and its
team in line.

And it fills **the mailbox calendar** for the calendar's Outlook sync ([the mailbox
calendar](#the-mailbox-calendar)): behind the Owner's **Calendar sync** switch and the
permission `Calendars.ReadWrite`, it writes the calendar's copies of a person's
appointments to the mailbox of an account it manages, in the single-provider slot
`MailboxCalendar` ([module boundaries](/en/contributing/module-boundaries/) rule 14).

## The schema

One migration, `00061_entra_baseline.sql`, creates the `entra` schema and its four
tables. No table references another schema; every foreign identifier — the user who
acted, the user who added a secret — is opaque. The CHECK constraints listed are the
schema's own; the module's validation reports the same rules per field before a write
reaches them.

- **`connection`** — the installation's one connection, a single row seeded by the
  migration (`id` 1, `CHECK (id = 1)`):
  - the registration: `tenant_id` and `client_id`, null until step 1 is saved;
  - the [account rules](#the-account-rules): `upn_domain` (253), `upn_pattern`
    (`given.family` by default; `given.family`, `g.family` or `given`),
    `default_usage_location` (two letters, `NO` by default),
    `leaver_strip_licences`, `leaver_revoke_sessions`, `leaver_write_leave_date` and
    `write_hire_date` (each true by default), `leaver_remove_groups` (`all` by default;
    `all`, `managed` or `none`), `pass_lifetime_minutes` (480 by default; 10 to
    43 200), `pass_usable_once` (true by default) and `pass_delivery` (`hr` by default;
    `hr`, `manager` or `both`);
  - what the last [connection test](#the-connection-test) — or a refusal since — found:
    `status` (`unconfigured`, `connected` or `failing`; `unconfigured` by default),
    `status_reason` (60, a code), `permissions_present` (the token's roles),
    `verified_domains`, `organisation_name` (256) and `last_checked_at`;
  - `sensitive_denied_streak` (0 by default, never negative), the sensitive writes
    refused in a row with none succeeding since ([role_missing](#the-sensitive-actions-and-role_missing));
  - `version` (from 1), the `If-Match` of both of the connection's `PUT`s, and
    `updated_at` and `updated_by_user_id`.
- **`credentials`** — the application's client secrets, in the plural: `id` (a `bigint`
  identity from 1001), `kind` (`client_secret` only, `CHECK`ed), `label` (100),
  `sealed` (the secret, sealed under the purpose `entra/client-secret`), `expires_at`,
  `created_at` and `created_by_user_id`, `last_used_at`, `last_failed_at` and
  `revoked_at`. A revoked secret is kept, never deleted.
- **`accounts`** — the users the module found or created in the tenant: `object_id`
  (the directory's immutable id, the primary key), `employee_number` (16, the
  directory's `employeeId` limit), `user_principal_name` (254), `state` (`disabled`,
  `enabled` or `deleted`), `pass_sealed` (a Temporary Access Pass sealed under the
  purpose `entra/pass`, until revealed or expired) with `pass_expires_at` — a sealed
  pass always has its expiry (`CHECK`) — and `pass_revealed_at`, `created_by_us`
  (false for a user the module found and linked), `reported` (the codes of the last
  write), `last_error` (60, the last refusal's code), `last_synced_at`,
  `adoption_confirmed_at` and `adoption_confirmed_by_user_id` (who confirmed a found
  user's [adoption](#adopted-accounts), null for one the module created) and the two
  stamps. `ux_entra_accounts_number` on `upper(employee_number)` holds one account per
  employee number, case-folded as Staff's numbers are. A deleted account stays,
  marked `deleted`, until the number is prepared again: a rehire after a delete drops
  the deleted row so the number can name the new user ([Prepare](#prepare)), and the
  events keep the old object id and its UPN.
- **`events`** — the module's own append-only trail of its writes, and its only one: it
  writes no row to the platform's audit log. `id`, `at`, the
  actor — exactly one of `actor_user_id` and `actor_system` (40), `CHECK`ed — `action`
  (40), `object_id`, `upn` (254) and `codes`. `ix_entra_events_at` serves the newest
  first. A row carries the UPN the module made or found and codes, never another
  address, a secret or a pass.

Delivery D's migration, `00063_entra_groups.sql`, adds a table and three columns:

- **`groups`** — the groups the module made or adopted for Staff's: `object_id` (the
  primary key), `group_id` (Staff's group id, 60, unique — `ux_entra_groups_group_id`),
  `key` (60), `display_name` (256) and `mail_nickname` (64) as the module last wrote
  them, `type` (`security` or `microsoft365`, `CHECK`ed, never changed), `team_created_at`,
  `created_by_us` (false for a group found by its nickname and adopted), `state`
  (`present`, `deleted` or `retired`, `CHECK`ed — a deleted group's row is kept, and so is
  a retired one's, a security group emptied and kept: [DeleteGroup](#deletegroup)),
  `last_members_at`, the two stamps, and `adoption_confirmed_at` and
  `adoption_confirmed_by_user_id` — when and by whom a holder of `entra:manage` confirmed
  an adopted group ([adopted groups](#adopted-groups)), null for one not confirmed and
  for one the module made.
- **`connection.service_principal_id`** — the application's service principal in the
  tenant, read from its token's `oid` claim, which every group the module makes names as
  an owner; and **`connection.skus`** (`jsonb`, `[]` by default) — the tenant's
  licences as the last connection test or licences read found them, from which the
  [licences trait](#the-traits) is declared.
- **`accounts.licences_assigned`** (`text[]`, `{}` by default) — the SKU ids, lower-case,
  of the direct licences the module itself assigned the user: the only ones
  [Activate](#activate) and [Update](#update) remove (design reading 9).

Its down drops the table and the three columns; the groups the module made stay in the
tenant, the next connection test reads the service principal and the licences again,
and the licences the module assigned are forgotten.

`00082_entra_calendar_sync.sql` adds **`connection.calendar_sync`** (`boolean`, false by
default), the [calendar sync switch](#the-calendar-sync-switch). Its down drops the
column; the copies already in people's mailboxes stay there.

The down migration drops the schema with everything in it — the connection, the sealed
secrets, any pass not yet revealed and the trail. Nothing in the tenant is touched: the
users the module made stay there, and a later up finds them again by employee number.

**The two sealed purposes** are `entra/client-secret` and `entra/pass`, each its own key
derived from `APP_SECRET` (see [platform packages](/en/contributing/platform-packages/#rotating-app_secret)).
Neither value is ever answered by an endpoint, written to a log line, an event, an
audit row or an error. A rotation of `APP_SECRET` leaves both unopenable: a secret
that cannot be opened counts as one the tenant refused (marked failed, the next tried,
then `credential_invalid`), so the secrets must be added again, and any pass waiting is
lost (Staff's **Run now (new pass)** issues another).

## Permissions

| Key | Name | Grants | Sensitive | Delegable |
| --- | --- | --- | --- | --- |
| `entra:manage` | Manage Microsoft Entra ID | The tile and its page; the connection's status, secrets (without their values), accounts and events; confirming an [adopted account](#adopted-accounts) or an [adopted group](#adopted-groups); the [account rules](#the-account-rules), the tenant's licences and the module's [groups](#groups-in-the-tenant). | yes | **no** |

It is in the category "Administration". The Owner holds it through the wildcard;
anyone else needs a role that grants it, and because it is not delegable, only an Owner
can grant it. What it sets decides what happens to every account in the tenant.

**The connection itself is the Owner's**, not `entra:manage`'s: the tenant and the
client (step 1), the secrets (step 2) and the connection test (step 3) are under
`policy:Owner+OwnerManagement`, as identity's single sign-on settings are — an Owner,
signed in with a second factor while `OWNERS_REQUIRE_MFA` is on. A tenant-wide
application credential is an Owner's to hold. A holder of `entra:manage` who is not an
Owner is refused 403 on their writes, and so is an Owner whose session verified no
second factor while `OWNERS_REQUIRE_MFA` is on — the policy's MFA requirement refuses
as a 403, as identity's own Owner endpoints do; 401 is only for a request without a
session. The app decides what to show from identity's session (`GET
/api/v1/identity/session`, the caller's roles): anyone but an Owner sees steps 1 to 3
read-only, and steps 5 and 6 — identity's own Owner forms — without their forms. The
four Owner operations share one rate limit, `entra-owner`: 30 requests a minute per
client address, identity's user-management figures.

`staff:manage` never reaches the module: everything HR does with an account goes
through Staff and the slot.

## The connection

`GET /connection` answers the whole connection — the registration, the status and its
reason, the organisation, the verified domains, the permissions present and missing,
the last check, the account rules, every secret without its value, the accounts'
counts by state (`count`, `enabled`, `disabled`, `deleted`) and the groups' (`groups.count`,
the groups present in the tenant) — whether or not anything is
set, so the wizard can show every step before the first is done. It reads the module's
own rows only; nothing reaches the tenant. `permissionsMissing` is the
[required permissions](#the-required-permissions) less those the last test's token
carried — with `Calendars.ReadWrite` last while the calendar sync switch is on and the
token lacks it; all of them before the first test. `calendarSync` is the
[switch and its status](#the-calendar-sync-switch).

**`PUT /connection`** (step 1) sets `tenantId` and `clientId`, both GUIDs as the
portal's **Overview** shows them, under `If-Match` on the connection's `version` (bare
or quoted, or weak). A change of either id moves the version on, sends the status back
to `unconfigured`, forgets what the last test found — `lastCheckedAt`, the permissions
present, the verified domains and the organisation's name — and the sensitive streak,
and is recorded as the event `entra.connection.changed` with the codes
`tenant_before:`, `tenant_after:`, `client_before:` and `client_after:` (`none` for an id
not set before). **A change of the tenant also clears every pass not yet revealed**,
in the same transaction — they are the old tenant's users' — while a change of the
client alone keeps them. **The same pair again writes nothing**: no version, no event;
the answer is the connection as it is. **While the module keeps accounts**
(`entra.accounts` has rows) a change of either id is refused `409 connection_in_use`
unless the request carries `confirmChange: true`: the accounts were made in the old
tenant, the module would now look for them in the new one, and — as the refusal's
detail says — the current tenant's accounts would no longer be disabled or deleted by
Vantigo when their people leave. A change deletes nothing in the tenant either way.

### The credentials

The connection authenticates to the tenant with a **client secret** of its app
registration, and holds them **in the plural**, so a secret is rotated without a gap:

- **The try order.** The valid secrets — unrevoked, with `expires_at` ahead — are
  tried newest first, except that a secret the tenant refused since it last fetched a
  token (or that never fetched one) goes after every other. The first that has a
  cached token, or fetches one, is used. A secret is opened for each token request and
  never kept open.
- **A refusal moves on.** A token request refused `invalid_client` or
  `unauthorized_client`, or a sealed value that cannot be opened on this server, marks
  the secret `last_failed_at`, and the next is tried. When none is left the connection
  is written `failing` with `credential_invalid`. A token endpoint that does not answer
  marks nothing: the call fails as `tenant_unreachable`.
- **`last_used_at`** is when the secret last *fetched* a token — a cached token reused
  writes nothing — written at most once a minute per secret.
- **`POST /connection/credentials`** (step 2) adds one: `kind` `client_secret` (the only
  kind; a certificate is a later one), an optional `label` (at most 100 characters,
  trimmed, a blank one none — the secret's description in the portal or anything that
  tells two apart), `secret` (1 to 1000 characters once trimmed) and `expiresAt` (in
  the future and at most 24 months ahead, Microsoft's own limit). The wizard sends the
  expiry the Owner picks as that day's midnight in the browser's time zone. It is
  sealed at once, answered `201` without its value, and recorded as the event
  `entra.credential.added` with the codes `credential:<id>`, `expires_at:<instant>` and
  `label:<label>` — never the value.
- **`DELETE /connection/credentials/{id}`** revokes one: it is kept, marked
  `revokedAt`, its cached token forgotten, and never used again; nothing changes in the
  tenant, where the Owner deletes the secret themselves. The answer is `200` with the
  secret as revoked; one revoked already is answered as it is, and nothing is written.
  The **only valid** secret is refused `409 credential_in_use` while the module keeps
  accounts, since nothing could then disable a leaver. An unknown id is a 404 with an
  empty body. Recorded as the event `entra.credential.revoked`, with the same codes.

**Rotation** is *Add the secret* with the new value, then *Revoke* on the old one.
While the connection is `connected`, the status reads `secret_expiring` once the valid
secret that ends last ends within 30 days; once every unrevoked secret has ended,
`failing` with `secret_expired`. Staff raises an attention item for each
([below](#the-status-and-its-reasons)).

Each secret in the answer carries `id`, `kind`, `label`, `expiresAt`, `createdAt`,
`lastUsedAt`, `lastFailedAt`, `revokedAt` and `inUse` — true for the first secret in
the try order, the one the next token request starts with. They are listed newest
first, revoked ones included.

### The connection test

**`POST /connection/test`** (step 3) fetches a fresh token — never the cached one, so a
consent granted since is read — trying the secrets in [their order](#the-credentials),
reads `GET /organization` — the `displayName` and the `verifiedDomains[].name` that are
verified, lower-cased — and compares the token's `roles` claim with the [required
permissions](#the-required-permissions). It writes what it found to the connection —
`status`, `status_reason`, `permissions_present`, `last_checked_at`, and the
`verified_domains` and `organisation_name` when the organisation answered — and
answers it, **200 whatever it found**, a failure included:

| Found | `status` | `reason` |
| --- | --- | --- |
| A token with roles, and the organisation answered | `connected` | — |
| No secret fetched a token (`invalid_client`, none valid, or none can be opened) | `failing` | `credential_invalid` |
| The token or Graph host did not answer, or answered 5xx (Graph's after its one retry) | `failing` | `tenant_unreachable` |
| A token with no `roles` at all — admin consent was never granted — or Graph refused the organisation 401 or 403 | `failing` | `consent_missing` |

A failed test clears the stored permissions (none is present until a test reads them
again) but keeps the verified domains and the organisation's name the last good test
read, so the account rules' domain stays a choice. With no tenant or client saved, the
test answers `unconfigured` with every permission missing and writes nothing.

The answer is `{ status, organisationName?, verifiedDomains, permissionsPresent,
permissionsMissing, roleKnown, reason? }`. **`roleKnown` is always false**: whether the
application holds the User Administrator role cannot be read without a further
permission, so the wizard asks the Owner to check it in the portal, and the module
learns it from the first [sensitive action](#the-sensitive-actions-and-role_missing).
When the test connects, it also writes the service principal's object id — the
token's `oid` claim, when it carries one — to `service_principal_id`, and reads
`subscribedSkus`, writing the tenant's licences to `skus` when it answers
([the traits](#the-traits)); a read that fails leaves the cache, and the status the
test found, as they were. The test does not move the connection's `version`. Recorded as the event
`entra.connection.tested`, with the status, the reason and a `missing:<permission>` code
for each permission missing.

### The required permissions

The application permissions (Microsoft Graph, *Application* type, admin consented) the
module needs, which the test compares with the token's `roles`:

| Permission | For |
| --- | --- |
| `User.ReadWrite.All` | Finding a user by employee id, creating, updating, enabling and disabling, the manager, the photo, deleting |
| `User-LifeCycleInfo.ReadWrite.All` | `employeeHireDate` and `employeeLeaveDateTime` |
| `User.RevokeSessions.All` | Revoking a leaver's sessions |
| `UserAuthMethod-TAP.ReadWrite.All` | The Temporary Access Pass |
| `LicenseAssignment.ReadWrite.All` | Assigning and removing licences, and `subscribedSkus` |
| `Group.ReadWrite.All` | A leaver's group memberships; Staff's groups, their owners and members, and a group's team |
| `Domain.Read.All` | The verified domains |
| `Organization.Read.All` | The organisation, read by the test |

**`Calendars.ReadWrite`** is the ninth, and conditional: asked for only while the
[calendar sync switch](#the-calendar-sync-switch) is on — it is then in
`permissionsMissing`, after the eight, and in the test's `missing:` codes, until a test's
token carries it — and never judging the connection itself: the status the test writes
is decided by the eight alone, and a token without it is still `connected`. It is the
[mailbox calendar's](#the-mailbox-calendar) permission, and an administrator may narrow
it to a group of mailboxes with Exchange's RBAC for Applications ([the administration
guide](/en/admin/entra/#calendar-sync)). A grant made that way never shows in the
token, so the permission listed missing is **advisory**: the sync runs either way, and a
mailbox outside the grant is refused for that person alone.

Beside them, **the User Administrator directory role** must be assigned to the
application's service principal: Graph treats enabling and disabling an account,
setting its password, changing its principal name or phones, and deleting it as
*sensitive actions*, which an application may take only with an administrator role as
well as the permission. The test cannot see the role; see
[role_missing](#the-sensitive-actions-and-role_missing).

## The status and its reasons

The status is what Staff reads before it writes. `Provider(ctx)` answers the key
`entra`, the name **Microsoft Entra ID**, the status and a reason, and **reads the
connection's row and the secrets and nothing else** — no network call — because Staff
asks it on every `GET /meta` and every cycle. The row's status is written by the
connection test, and as `failing` with `credential_invalid` by any call that finds no
secret fetching a token; the rest is derived as it is read, in this order:

| Status | Reason | When |
| --- | --- | --- |
| `unconfigured` | — | The tenant or the client is not saved, no unrevoked secret has been added, or the connection has never been tested (or not since step 1 changed). |
| `failing` | `secret_expired` | Secrets are there, but no unrevoked one's expiry is ahead — whatever the last test found. |
| `failing` | `credential_invalid` | No secret fetched a token, at the test or since. |
| `failing` | `tenant_unreachable` | The tenant did not answer the test. |
| `failing` | `consent_missing` | The test's token carried no permission at all, or Graph refused the organisation. |
| `connected` | `secret_expiring` | The valid secret that ends last ends within 30 days. It wins over `role_missing`. |
| `connected` | `role_missing` | Three sensitive writes in a row were refused, none succeeding since. It wins over the row's own reason. |
| `connected` | — | Nothing to say. |

Every reason is a code within Staff's bound (`^[a-z_]{1,40}…$`), never the directory's
words. Staff turns the status into its installation items: `unconfigured` is
`entraNotConnected`, `failing` with `secret_expired` is `secretExpired`, `failing` with
any other reason is `entraFailing`, and `secret_expiring` is `secretExpiring`
([Staff's attention items](/en/reference/staff/#attention-items)). `role_missing`
raises no installation item: each account it stops reports `privileged` and shows as
`accountPrivilegedInEntra`, and the module's **Status** card names the reason.

While the status is not `connected`, Staff writes nothing to the module but a
`Deactivate`: `Prepare`, `Activate`, `Update` and `Delete` answer
`contracts.ErrProviderUnavailable`, while `Deactivate` and the `Account` read run on any
secret that fetches a token, and `RevealPass` needs no connection at all
([below](#the-user-lifecycle)).

## The account rules

**`PUT /connection/settings`** (step 4, `entra:manage`) sets how accounts are made and
unmade, under `If-Match` on the connection's `version`. Every field is optional and one
left out keeps its value; each refusal is a field error under its own name. It moves
the version on, answers the whole connection and is recorded as the event
`entra.settings.changed`, whose codes are the names of the fields that changed.

| Field | Rule | Default |
| --- | --- | --- |
| `upnDomain` | One of `verifiedDomains` once a test has read them; before that, any lower-case domain name, at most 253 characters. Unset, the [address](#the-address) falls back to a verified domain; the wizard asks for it all the same | unset |
| `upnPattern` | `given.family`, `g.family` or `given` — the [address](#the-address) for a person whose work email cannot be the UPN | `given.family` |
| `defaultUsageLocation` | Two letters, ISO 3166-1 alpha-2: the `usageLocation` written when Staff sends no work country, or one that is not such a code. A licence cannot be assigned without one. | `NO` |
| `writeHireDate` | Write the first day as `employeeHireDate`, 01:00 in the business time zone | on |
| `leaverRevokeSessions` | Revoke a leaver's sign-in sessions | on |
| `leaverWriteLeaveDate` | Write the last day as `employeeLeaveDateTime` | on |
| `leaverStripLicences` | Remove a leaver's directly assigned licences | on |
| `leaverRemoveGroups` | `all`, `managed` or `none` ([Deactivate](#deactivate)) | `all` |
| `passLifetimeMinutes` | 10 to 43 200; the tenant's own policy may cap it lower | 480 |
| `passUsableOnce` | The pass works for one sign-in | on |
| `passDelivery` | `hr`, `manager` or `both` | `hr` |
| `calendarSync` | Lets people sync their Vantigo calendar to Outlook ([the switch](#the-calendar-sync-switch)); on, it asks for `Calendars.ReadWrite`; off, the calendar's worker removes the copies already written | off |

**`passDelivery` is stored and offered, and nothing acts on it yet**: the pass is
revealed only on the person's Account tab in Staff, to HR, and no notice is sent to the
manager in this release; a later one gives Staff the setting and the notice together.

`GET /connection/skus` (`entra:manage`) answers the licences the tenant subscribes to,
read from `GET /subscribedSkus` through the connection: `skuId`, `skuPartNumber` (the
part number a Staff group's licences name, such as `ENTERPRISEPACK`), `enabled` (the
seats bought, `prepaidUnits.enabled`) and `consumed`, sorted by part number — a licence with no part number or a `skuId` that is
not a GUID left out — and on success writes them to `connection.skus`, the cache the
[licences trait](#the-traits) is declared from. It
answers `503 credential_invalid` when no secret fetched a token (or no tenant or client
is saved) and `503 tenant_unreachable` when the tenant did not answer. The wizard shows
the list under step 4 while the connection is `connected`. It is rate-limited in a
bucket of its own, `entra-skus`, 30 requests a minute per client address (429 beyond),
since every request reaches the tenant.

## The Graph client

Every call to the tenant goes through one client. Its transport and its token cache
are built once per process and shared by the API's operations and the provisioner
Staff's worker calls:

- **Two fixed hosts**, never a setting: `https://login.microsoftonline.com/{tenant}/oauth2/v2.0/token`
  for the token (the `client_credentials` grant, scope
  `https://graph.microsoft.com/.default`) and `https://graph.microsoft.com/v1.0` for
  everything else. A test's fake Graph comes in through the transport, not a base URL.
- **The guard.** The transport dials through `internal/netguard`, the platform's guard on
  outbound addresses, uses **no proxy** and follows **no redirect**; keep-alives are
  on, since the same host is called many times in a cycle, and an idle connection is
  closed after 90 s. Dial 10 s, TLS handshake 10 s, response headers 15 s, response
  headers at most 64 KiB, and each call under a 30-second budget.
- **The token** is cached in the process per secret — keyed by the secret's row, the
  tenant, the client and a digest of the sealed value — under a mutex, until five
  minutes before its `expires_in`; its `roles` claim is read for the test (its
  signature is not checked: it is the token the tenant just gave). A token is never
  cached across secrets.
- **Throttling.** A 429 waits its `Retry-After` once (a second when it names none); a
  second 429, or a `Retry-After` over 60 seconds, fails the call, and Staff's worker
  tries again at its next poll.
- **A 5xx is retried once**, after a second — except on the three creates that must
  not happen twice, `POST /users`, the pass's create and `POST /groups`: their 5xx is
  the answer, and the caller reads back instead ([Prepare](#prepare),
  [EnsureGroup](#ensuregroup)). A 429 and a 401, which the tenant answers before it
  acts, are retried on them as on any call.
- **A 401 drops the cached token** and the call is retried once with a fresh one.
- **Every response body is read through a 1 MiB cap**, but the [mailbox
  calendar's](#the-mailbox-calendar) `$batch`, read through 8 MiB, since twenty events
  each answered whole may outgrow 1 MiB, and a page of its [delta](#the-reads), read
  through 16 MiB, since fifty events are answered whole — subject, an HTML body,
  attendees — which the module must read past; a larger one is refused.
- **Bodies are JSON** (`get`, `post`, `patch`, `put`, `del`) but for the photo's, which
  `putRaw` sends as it is with its own media type (`image/jpeg`), under the same rules —
  the throttle, the one retry of a 5xx, the 401's fresh token.
- **Graph's errors** (`{ error: { code, message, details } }`) are mapped to a
  sentinel or a reported code by the status, the code and the first detail's code (a
  taken UPN is `ObjectConflict` there); of the message the client reads one fact
  alone, whether it says a reference added "already exist[s]", which Graph names by no
  code of its own. The message is logged at debug level with the
  tenant's id, the method and the path — never the query, which may carry an employee
  number — except for the three creates, whose refusal is logged by its status and
  codes alone, since the message could echo the password or the pass the call carried,
  and the calls that add or remove a group's member or owner by its reference — the
  members' `PATCH`, the `$ref` posts and the `$batch` — whose message could echo a
  user's id; the token endpoint's refusal is likewise logged by its status and
  OAuth code, never its `error_description`. **The body is never stored**: not in a
  row, an event or anything a person sees.

## The user lifecycle

The module's `IdentityProvisioner` executes Staff's steps. Every write checks its actor
first (`contracts.ErrInvalidActor` for one that is neither a user nor a system).
**An account the module did not create is written to only once its adoption is
confirmed** ([adopted accounts](#adopted-accounts)).
**`Prepare`, `Activate`, `Update`, `Delete`, `SetPhoto` and `ClearPhoto` need the status `connected`**, as
`Provider` answers it, and answer `contracts.ErrProviderUnavailable` otherwise.
**`Deactivate` and `Account` run whatever the status**, on any secret that fetches a
token. For all of them a connection with no tenant or client, or no secret that fetches
a token, is `ErrProviderUnavailable`, and a tenant that does not answer is a plain
error. `RevealPass` reads the module's own row only and needs no connection. The calls
to Graph are made on no transaction, the module's own rows written in short
transactions after each. Every write is idempotent and records an
[event](#the-events). What it could not do is answered as `Reported` codes, which Staff
shows on the person's Account tab:

| Code | Means |
| --- | --- |
| `hire_date_not_written` | Graph refused the `PATCH` carrying `employeeHireDate` (400 or 403); the account was enabled without it. |
| `licence_unavailable:<part number>` | That licence could not be assigned — the tenant does not subscribe to the part number, no seat was left, or the assignment was refused; the others were. |
| `pass_not_allowed` | The tenant's policy refused the Temporary Access Pass (a 400). |
| `pass_forbidden` | The tenant refused the pass's create alone with a 403; the account is enabled without a pass. Not a privileged refusal, and not counted in the sensitive streak. |
| `adoption_unconfirmed` | The account is one the module found, not created, and nobody has confirmed its adoption; nothing is written to it ([adopted accounts](#adopted-accounts)). |
| `manager_not_found` | The manager's account was not found in the tenant, or Staff named no valid object id. |
| `photo_not_written` | Graph refused the photo or its removal (400, 403 or 413; a 404 on the `PUT`), or the picture could not be decoded or brought under 4 MB; reported until a photo is written or cleared ([SetPhoto](#setphoto-and-clearphoto)). |
| `privileged` | A sensitive action was refused ([below](#the-sensitive-actions-and-role_missing)). |
| `leaver_step_failed:<step>` | A leaver's step after the disable failed — `revoke_sessions`, `leave_date`, `strip_licences` or `remove_groups` — and the steps after it were not tried ([Deactivate](#deactivate)). |

The refusals that stop a step are the contract's sentinels, which Staff records on the
link ([the provider link](/en/reference/staff/#the-provider-link)):

| Sentinel | When |
| --- | --- |
| `ErrExternalAmbiguous` | Two users in the tenant hold the employee number, or the module's row for the number names another user the tenant still holds. |
| `ErrExternalAddressTaken` | The work email, or every suffix up to 99, is another user's UPN. |
| `ErrExternalSyncedOnPremises` | The user is synchronised from on-premises Active Directory (`onPremisesSyncEnabled`), which Graph will not write. Checked on every write to a user but the photo's. |
| `ErrExternalPrivileged` | A sensitive action was refused 403. |
| `ErrExternalEnabled` | A delete of an account that is still enabled. |
| `ErrExternalUnconfirmed` | The account is one the module found, not created, and its adoption is not confirmed; nothing was written. |
| `ErrPassGone` | No pass waits, or it has expired. |
| `ErrProviderUnavailable` | The connection is not set, or no secret works. |

Each refusal sets the account's `last_error` and is recorded under the write's own
event with the code `refused:<code>` — `ambiguous`, `address_taken`,
`synced_on_premises`, `privileged` or `enabled`; an unconfirmed adoption records
`refused:adoption_unconfirmed` and leaves `last_error` as it was. Anything else — a throttled call, a 5xx
after its retry, a network failure, no domain to make an address under — is a plain
error, which Staff counts and retries.

### Prepare

`GET /users?$filter=employeeId eq '<number>'&$select=id,userPrincipalName,mail,accountEnabled,onPremisesSyncEnabled,employeeId`,
the number's `'` escaped as `''`:

- **Two rows** is `ErrExternalAmbiguous`.
- **One row** is read whole and linked — `created_by_us` false, unless the module made
  it; `onPremisesSyncEnabled` on it is `ErrExternalSyncedOnPremises`. A user the module
  made, or one whose adoption was confirmed, is brought to the spec by the
  [Update](#update) path. **Any other is adopted untrusted**: its row is written with
  `adoption_unconfirmed` among its codes, nothing is written to the user, and the call
  answers `contracts.ErrExternalUnconfirmed` (event codes `found` and
  `refused:adoption_unconfirmed`) until a holder of `entra:manage` confirms it; the
  next `Prepare` then brings it to the spec and answers it. This supersedes the letter
  of the design's D13, under which `Prepare` adopted and wrote the user holding the
  number; [the plan's reading 8](https://github.com/vantigo-io/vantigo/blob/main/docs/superpowers/plans/2026-10-20-entra-module.md)
  is the record of why: a `staff:manage` holder registering someone else's employee
  number could otherwise take over that person's account.
- **None, while the module's row for the number names a user the tenant still holds**
  (the filter may lag a create by seconds): that user, as above.
- **The module's row names a user deleted, or one the tenant answers 404 for**: the row
  is dropped, so the number can name a new user — a rehire after a delete is a new
  account. A row naming a user the tenant still holds under another number is
  `ErrExternalAmbiguous`.
- **None** is a create, `POST /users`, with `accountEnabled: false`, `displayName`
  (the spec's, else the given and family names, else the mail nickname), `givenName`,
  `surname`, `mailNickname` and `userPrincipalName` from [the address](#the-address), a
  `passwordProfile` with 32 random characters (from `A-Za-z0-9!#%+-`, at least one of
  each class) and `forceChangePasswordNextSignIn: true` — never stored, never logged,
  never shown: the pass replaces it —, `employeeId`, `usageLocation` (Staff's work
  country when it is an ISO 3166-1 code, else the default), `preferredLanguage`
  (`nb-NO` for `nb`, `no` or `nn`; `en-GB` for `en`), and the fields Staff's directory
  profile switches on under Graph's names — `jobTitle`, `department`, `companyName`,
  `employeeType`, `officeLocation`, `streetAddress`, `postalCode`, `city`, `country`,
  `mobilePhone`, `businessPhone` as `businessPhones`, and `costCenter` and `division`
  written together as `employeeOrgData`, since Graph replaces the object whole — each
  cut to the length Graph keeps. A conflict (`ObjectConflict`) on the UPN is
  `ErrExternalAddressTaken` when the work email was the UPN, and a retry with the next
  suffix when the pattern made it; a 403 is `ErrExternalPrivileged`. **The create is
  made once**: when its answer is lost — a 5xx, or no answer — `Prepare` reads the
  number back and takes the user as its own only when exactly one holds it, with the
  UPN it asked for, disabled and not synchronised; otherwise the call fails, and a
  user it may have made is found by the next `Prepare` and adopted untrusted. **The
  row is recorded under the connection's share lock** (`FOR SHARE`), after checking
  that the tenant, the client and a usable secret are still the ones the create was
  made with; when one moved — step 1 changed, or the last valid secret was revoked,
  meanwhile — nothing is recorded, the user is left disabled in the tenant, and the
  call answers `ErrProviderUnavailable`. Then the manager,
  by `PUT /users/{id}/manager/$ref`, when Staff names one; a 404 there, or a manager id
  that is not an object id, reports `manager_not_found`.

The account is answered with `Created` true only on the call that created it, and its
row upserted — at once after the create, so a failure after it finds the user next
time. Recorded as the event `entra.user.prepared` with `created` or `found`, a
`changed:<property>` code for each property a found user had brought in line
(`changed:manager` for the manager), and the reported codes. Nothing is licensed and
nobody can sign in yet.

### Activate

On the first day:

1. `PATCH /users/{id}` with `accountEnabled: true` when the user is disabled and, when
   `write_hire_date` is on and Staff sends a hire day the user does not hold yet,
   `employeeHireDate` as that day's 01:00 in the business time zone (UTC without a
   business calendar). A 400 or 403 on a `PATCH` carrying the hire date is retried
   without it and reports `hire_date_not_written` — whether Graph accepts the property
   from an application is one of the facts [the tagged test](#testing) settles. A 403
   on the enable is `ErrExternalPrivileged`.
2. The licences reconciled (`reconcileLicences`, which [Update](#update) uses too):
   `POST /users/{id}/assignLicense` with the licences Staff names that the user does not
   hold yet, their part numbers resolved (ignoring case) to SKU ids through
   `subscribedSkus`. A part number the tenant does not subscribe to, or a
   `licenseAssignmentStates[].error` (such as `CountViolation`, no seat left), reports
   `licence_unavailable:<part number>`; the rest are assigned. A 4xx on the whole call
   reports every licence it carried. Then **the licences the module itself assigned** —
   those in `accounts.licences_assigned` the user still holds directly
   (`assignedByGroup` null) — whose part number Staff no longer names are removed in one
   `assignLicense` with `removeLicenses`, every one of them when Staff names none. Only
   the module's own (design reading 9, plan reading 12): a licence an administrator
   assigned directly, one the user held before Staff named it, and one the tenant
   assigns through a group are the tenant's and are left alone, and so is one whose SKU
   `subscribedSkus` no longer lists. A 4xx on the removal records
   `licences_not_removed` in the event and leaves the user as it is; the removal records
   `licences_removed:<n>`. The module's list is then written when it changed: those it
   held still and those it assigned now — a SKU the tenant reported an error for not
   among them — less those removed, so a licence the user no longer holds directly
   drops out of it. An account the module enabled before it kept the list starts with
   an empty one, so nothing assigned to it earlier is ever removed here. With no licence
   named and no direct licence held, nothing is asked of the tenant.
3. The pass, **only when this call enabled the account**, and then when Staff's step
   asks for a fresh one — **Run now (new pass)**, `Flags.Fresh` — or no unrevealed,
   unexpired pass waits. An `Activate` of an account already enabled issues none,
   `Fresh` included: a pass over a working account would be a way into it. The create
   is made once, never retried on a 5xx.
   `POST /users/{id}/authentication/temporaryAccessPassMethods` with
   `lifetimeInMinutes` and `isUsableOnce` from the connection. The pass is sealed under
   `entra/pass` with its expiry — Graph's `startDateTime` plus its lifetime — and the
   account answers `HasPass` true; recorded as the event `entra.pass.issued` with
   `expires_at:<instant>`. A 400 on the create — the tenant's authentication methods
   policy does not enable the pass for the user — reports `pass_not_allowed`, and a 403
   reports `pass_forbidden`; either way there is no pass and the enable stands.

An enabled user with nothing missing is answered as it is. Recorded as the event
`entra.user.activated` with `enabled`, `hire_date_written`, `licences_assigned:<n>`,
`licences_removed:<n>` or `licences_not_removed`, and `pass_issued` as each happened,
and the reported codes.

### Deactivate

The day after the end, whatever the connection's status, in this order, each step on
by the connection unless named always:

1. `PATCH accountEnabled: false` — **always, first**, when the user is enabled: it is
   what continuous access evaluation reacts to. A 403 is `ErrExternalPrivileged`, and
   nothing else is tried. Any waiting pass is cleared at once, before another step can
   fail.
2. `POST /users/{id}/revokeSignInSessions` (`leaver_revoke_sessions`): every refresh
   token and session cookie stops working within minutes. An access token already
   issued lives out its hour.
3. `PATCH employeeLeaveDateTime` as the end day's 23:00 in the business time zone
   (`leaver_write_leave_date`, when Staff sends an end day the user does not hold yet) —
   in the past by the time Staff disables, which is what a Lifecycle Workflows leaver
   trigger wants.
4. `POST /users/{id}/assignLicense` removing every **direct** licence — those whose
   `assignedByGroup` is null (`leaver_strip_licences`), the module's or not. A licence
   assigned through a group goes with the group. The module's list
   (`licences_assigned`) is emptied.
5. The groups, by `leaver_remove_groups`: **`all`** removes the person from every group
   `memberOf` lists (at most 50 pages) that is a group — not a directory role or an
   administrative unit — and not dynamic (`membershipRule` set), role-assignable or a
   distribution list (mail-enabled, neither security-enabled nor Microsoft 365, which
   Exchange keeps), groups the module never made included, each by `DELETE
   /groups/{id}/members/{userId}/$ref` inside `$batch` requests of twenty, so the user's
   id never stands in a request line the client logs — a membership already gone (404)
   counting as done, and any other answer failing the step;
   **`managed`** from the module's own groups only — the `memberOf` pages intersected
   with the groups the module made or adopted and holds present in the tenant
   (`entra.groups`, `state` `present`) before the same `$ref` deletes, so a group the
   module never made keeps the person; with no such group, nothing is read or removed
   and the step records nothing;
   **`none`** leaves every membership.

**A step that fails stops the steps after it**: it is logged, reported as
`leaver_step_failed:<step>` (`revoke_sessions`, `leave_date`, `strip_licences` or
`remove_groups`), and the call still answers the account disabled. A later
`Deactivate` runs the steps again from the first — each is safe to repeat — and
finishes the rest, the code clearing once they succeed. Staff makes that call: it
deactivates again a disabled account whose codes hold `leaver_step_failed:<step>`,
once a day — a call already made in a claim's chain is not made again in it, so a
step that fails again waits for the link's next claim
([the provider link](/en/reference/staff/#the-provider-link)). Recorded as the event `entra.user.deactivated` with `disabled`,
`sessions_revoked`, `leave_date_written`, `licences_stripped` and `groups_removed` as
each happened, and the step that failed.

### Update

Writes the attributes that changed and the manager, with the same names as the create.
The employee id and the UPN are **never rewritten**: the id is how the account is
found. A phone (`mobilePhone`, `businessPhones`) is a sensitive property: a 403 on a
`PATCH` carrying one is `ErrExternalPrivileged`. **On an enabled user the licences are
reconciled** as on [Activate](#activate) (Entra design D10, plan reading 6): the
licences Staff names that the user lacks are assigned — `licence_unavailable:<part
number>` when one cannot be, `CountViolation` included — and those the module assigned
itself that Staff no longer names are removed, every one of them when Staff names none;
a licence an administrator assigned directly, and one a group gave, are left alone.
Staff's list is the union of the person's groups' licences, so a membership's change
reaches the user here: a licence the module assigned for a group the person has left is
removed at their next `Update`, while one an administrator assigned by hand stays. On a
disabled user the licences are not touched. Recorded as the event `entra.user.updated`
with a `changed:<property>` code for each, `changed:manager`, `licences_assigned:<n>`,
`licences_removed:<n>` or `licences_not_removed`, and the reported codes.

### Delete

Refuses an account that is still enabled (`ErrExternalEnabled`) or synchronised from
on-premises, else `DELETE /users/{id}` — a soft delete: the user sits in the tenant's
deleted users for 30 days and can be restored there, after which it is gone with its
mailbox and OneDrive. A 403 is `ErrExternalPrivileged`. The row is marked `deleted`,
its pass cleared, and kept. A user the tenant answers 404 for is deleted already: its
row is marked `deleted` once, with the code `gone`. A rehire after a delete is a new
account ([Prepare](#prepare)). Recorded as the event `entra.user.deleted`.

### Account

`GET /users/{id}`, on any secret that fetches a token whatever the status, answered as
the account — `deleted` when the module's row says so, its `Email` the user's `mail`,
else the UPN; a 404 answers nil, which Staff records as `user_gone` and never
re-creates. Nothing is written: the row is left as it was, for the events' sake.

### RevealPass

Takes the pass in one statement — the sealed value cleared and `pass_revealed_at` set
in the same `UPDATE … RETURNING`, so two callers cannot both have it — opens it, and
answers it with its expiry. A pass past `pass_expires_at` is cleared and answers
`ErrPassGone`, as does an account with none. It reads the module's own row alone —
no call to the tenant, so it needs no connection. Recorded as the event
`entra.pass.revealed` with the actor, the object id and `expires_at:<instant>`, never
the pass.

### SetPhoto and ClearPhoto

The account's photo (`photo.go`; Staff photo sync design §4, reading 4): the picture
Staff's worker pushes — HR's photo, or the user's own Vantigo avatar
([the directory's photo](/en/reference/staff/#the-directorys-photo)) — written as Graph
takes it. Both need the status `connected`, find the user by its object id
(`GET /users/{id}`) and write nothing to an adoption nobody confirmed
(`ErrExternalUnconfirmed`, recorded `refused:adoption_unconfirmed`). A user synchronised
from on-premises is not refused up front: Graph's own answer decides.

- **The conversion.** Whatever its content type says, the bytes are decoded as a JPEG or
  a PNG — the header first, and a picture over 4096 px a side, more than Staff and
  identity admit, is not decoded at all — scaled with Catmull-Rom so the longer side is
  at most **1024 px** (a smaller one keeps its size), drawn over white (a PNG's
  transparency has no place in a JPEG) and encoded as a **JPEG** at quality 85, then 70,
  then 55, the first under **4,000,000 bytes** (Graph's "4 MB", read as decimal). Every
  photo is re-encoded, so nothing of the original's metadata reaches the tenant, even
  had Staff's stripping missed something.
- **SetPhoto** then sends `PUT /users/{id}/photo/$value` as `image/jpeg`, under the
  [Graph client's](#the-graph-client) rules. Each call writes: Staff's stamp decides
  when a push is due. A photo written clears `photo_not_written` and is recorded as
  `entra.user.photo_set` with the code `photo_written`. **A refusal of the picture
  itself** — Graph's 400, 403 or 413, a 404 (the user was read a moment before, so it is
  the photo's or the mailbox's: a user without an Exchange mailbox), a picture that does
  not decode, or one still over the limit at quality 55 — answers nil: nothing (more) is
  written, the reason is logged at Info, the account reports `photo_not_written` and the
  event carries `refused:photo_not_written`. Anything else — a throttled call, a 5xx
  after its retry, the network — is a plain error, which Staff counts and retries.
- **ClearPhoto** sends `DELETE /users/{id}/photo/$value`; a 404 — no photo — is done.
  It clears `photo_not_written` and is recorded as `entra.user.photo_cleared` with the
  code `photo_cleared`. A 400, 403 or 413 on the delete is a refusal as on the `PUT`:
  nil, `photo_not_written` reported, `refused:photo_not_written` on the event. Anything
  else is a plain error Staff retries.
- **The state** is left as it was: the account's row keeps `enabled`, `disabled` or
  `deleted` as Graph and the row say, and neither call touches anything but the photo.

The permission is `User.ReadWrite.All`, among the eight already consented: no new
consent.

## The address

The UPN — and `mailNickname`, its local part, at most 64 characters — is made once, at
the create:

1. **The person's work email**, lower-cased with its local part folded, when its domain
   is one of the tenant's verified domains and the folded local part is in the UPN's
   alphabet — at most 64 characters, no dot at either end or two in a row. It is tried
   once.
2. **Else the pattern** over the folded names at `upn_domain` — or, unset, the first
   verified domain not ending in `.onmicrosoft.com`, else the first verified domain;
   with none at all the create fails until a domain is set or a test reads them:
   `given.family` (`kari.nordmann`), `g.family` (`k.nordmann`) or `given` (`kari`),
   cut to 62 characters (`user` when the names fold to nothing), with `2`, `3`, … up to
   `99` appended on `ObjectConflict`, then `ErrExternalAddressTaken`.

**Folding**: letters NFD leaves whole are replaced by table first — the three
Norwegian ones (`Æ` → `AE`, `Ø` → `O`, `Å` → `A`, `æ` → `ae`, `ø` → `o`, `å` → `a`)
and `ß` → `ss`, `Ł` → `L`, `Đ` → `D`, `Þ` → `Th`, `Œ` → `OE` and their lower-case
forms — then the rest are decomposed (NFD) and their marks dropped (`é` → `e`), and in
a name anything outside the UPN's alphabet before the `@` — `A-Za-z0-9'.-_!#^~` — is
dropped, dots and spaces included. `Ørjan Åsmundsen` becomes
`orjan.asmundsen@<upn_domain>`. `mail` is never written (it is Exchange's). The
account's `Email` is the user's `mail` when Graph has one, else the UPN made, which
Staff writes to the person when they had none.

## The pass

A **Temporary Access Pass** is the person's first credential: a time-limited code they
type at their first sign-in to register their own methods. The module issues it on the
first day ([Activate](#activate)), with the lifetime and the single use the connection
sets; the tenant's authentication methods policy must enable the pass for the user,
or Graph refuses it (`pass_not_allowed`). It is **sealed** under `entra/pass` until it
is shown, **shown once** — through `RevealPass`, which Staff calls when HR chooses
**Show the pass** on the person's Account tab — and never mailed, logged or answered by
any of the module's endpoints. `GET /accounts` says only `hasPass`, false once the
expiry is past. A new pass issued by Graph replaces the last one in the tenant and on
the row; one that expired unseen is cleared when a reveal finds it, and a leaver's
disable or a delete clears any pass waiting, as a change of the tenant clears them
all.

## Adopted accounts

An account is **trusted** — written to by the lifecycle — when the module created it
(`created_by_us`) or a holder of `entra:manage` confirmed its adoption
(`adoption_confirmed_at` set). Any other user Staff names — one `Prepare` found holding
the employee number, or one the module keeps no row for — is **adopted untrusted**:
`Activate`, `Update`, `Deactivate` and `Delete` answer `contracts.ErrExternalUnconfirmed`
and make no write to Graph, the row keeps `adoption_unconfirmed` among its codes with
its `last_error` untouched, and the write's event carries
`refused:adoption_unconfirmed`. A leaver's disable is refused too: the module never
owned the account, and its safety is not the module's to give until someone confirms
the account is the person's ([the plan's reading 8](https://github.com/vantigo-io/vantigo/blob/main/docs/superpowers/plans/2026-10-20-entra-module.md)).

**`POST /accounts/{objectId}/confirm`** (`entra:manage`) confirms one: it sets
`adoption_confirmed_at` and `adoption_confirmed_by_user_id`, takes `adoption_unconfirmed`
out of the codes, answers `200` with the account, and is recorded as the event
`entra.account.adoption_confirmed`. An account the module does not keep is a 404 with
an empty body; one confirmed already, or one the module created, is `409
adoption_confirmed`. The next `Prepare` or `Update` Staff makes brings the account to
the person's spec. On the **Status** card such an account reads *Adopted — confirm*
with a **Confirm** button, which asks *Confirm the adoption of …?* first;
`GET /accounts` answers `adoptionConfirmed` beside `createdByUs`.

Staff records the sentinel as the blocked reason `external_unconfirmed` and raises the
attention item `accountUnconfirmedInEntra`, whose dashboard sentence links to the
module's page. A refused `Deactivate` is tried again at every poll, so a leaver's
account is disabled within one poll of the confirmation; the other refused steps wait
for the link's daily claim (or **Run now**).

## The sensitive actions and role_missing

Graph's sensitive actions — `accountEnabled`, `passwordProfile`, the UPN, the phones,
the delete and the pass — need the **User Administrator** role on the application's
service principal as well as the permission, and even with it an application cannot
act on a user who holds an administrator role, is in a role-assignable group or a
restricted management administrative unit. The module treats a 403 on the create, the
enable, the disable, a phone's update or the delete as one — a 403 on the pass alone
is `pass_forbidden` instead, the enable before it having been allowed: it is
`ErrExternalPrivileged`, the account reports `privileged`, its `last_error` is set, and
the connection's `sensitive_denied_streak` goes up by one. A sensitive action that
succeeds sets the streak back to 0, as does a change of the tenant or the client.

**A 403 cannot say which of the two it is** — a privileged person, or the role not
assigned. So the connection reads `role_missing` once three sensitive actions in a row
were refused with none succeeding since, which is what "every account fails it" means
in practice. Assigning the role and running the person's step again clears it.

## Users synchronised from on-premises

A user whose `onPremisesSyncEnabled` is true is mastered by on-premises Active Directory
and Graph refuses to write it. The module reads the flag on the user before every
write to it — the user `Prepare` finds, and the user of an `Activate`, an `Update`, a
`Deactivate` or a `Delete` — and stops with `ErrExternalSyncedOnPremises` before
changing anything; a hybrid tenant is not supported in this release. The photo is the
exception: `SetPhoto` and `ClearPhoto` do not read the flag, and Graph's answer decides —
a refusal of the picture is `photo_not_written`.

## Groups in the tenant

Staff's [groups](/en/reference/staff/#groups) reach the tenant through the slot's group
calls (Entra design D14). Staff decides which groups exist and holds their names,
owners, members and trait values; the module makes or adopts each group in the tenant,
keeps it in line, and records it in `entra.groups` by its object id, one row per Staff
group id. Staff calls `EnsureGroup` when a group's values changed, `SetMembers` when its
members did, and `DeleteGroup` to retire it ([Staff's sync](/en/reference/staff/#the-sync)).
Each write checks its actor, needs the status `connected` — `ErrProviderUnavailable`
otherwise, but for a `DeleteGroup` with nothing to delete or on a group the module did
not make — makes its Graph calls on no
transaction and writes the row after, and records an [event](#the-events). A user's
object id is never logged, and a member or an owner is only ever removed by its
reference (`/$ref`): without it, Graph deletes the user.

**A group the module may not write** is `contracts.ErrGroupNotManageable`, which Staff
shows as `group_not_manageable`: a group that is dynamic (`membershipRule` set, or
`DynamicMembership` among its `groupTypes`), role-assignable (`isAssignableToRole`), or
mail-enabled without being a Microsoft 365 group — a distribution list or a
mail-enabled security group, which Exchange keeps; a group found by the nickname that
another of the module's rows holds — judged again inside the transaction that records
the row, so two Staff groups racing for one group cannot both take it — or two groups
holding it; and, at `SetMembers`, a group the module keeps no row for, or one gone from
the tenant. A refusal judged on a group the module read is recorded under the write's
action with `refused:group_not_manageable`.

**An adopted group nobody has confirmed** is `contracts.ErrGroupUnconfirmed`, which
Staff shows as `group_unconfirmed` and does not count as a failure: the module found
the group rather than made it, and writes nothing to it until a holder of
`entra:manage` confirms the adoption ([adopted groups](#adopted-groups)).

### The traits

`GroupTraits(ctx)` declares the six traits a group's Entra section carries, reading the
connection's row alone — never the network (plan reading 1) — each with its label and
description in English and Norwegian and Microsoft's page about it as `DocURL`, in this
order:

| Key | Label (`en` / `nb`) | Type | Default | Rule |
| --- | --- | --- | --- | --- |
| `type` | Group type / Gruppetype | choice: `security` (Security / Sikkerhet), `microsoft365` (Microsoft 365) | `security` | Required: none is `trait_required`, another value `trait_invalid`. **Immutable**: a value other than the one the group was made or adopted with is `trait_immutable`. |
| `mailNickname` | Mail nickname / E-postalias | string, at most 64 | none | The group's mail nickname. A value is folded and lower-cased — a–z, 0–9 and hyphens kept, every run of other characters one hyphen — and cut at 64; one that folds to nothing is `trait_invalid`. Empty, it is made from the key the same way: the key as it is. **Immutable too**: once the group exists, a value other than the nickname it was made or adopted with is `trait_immutable`; left empty, the group keeps its own. |
| `visibility` | Visibility / Synlighet | choice: `private` (Private / Privat), `public` (Public / Offentlig) | none | Microsoft 365 groups only: a value on a security group is `trait_invalid`. Left empty, nothing is written and the tenant's default stands. |
| `description` | Description in the tenant / Beskrivelse i leietakeren | string, at most 1024 | none | The description written to the group; empty, the Staff group's own description, cut at 1024. |
| `team` | Team in Microsoft Teams / Team i Microsoft Teams | bool | `false` | Microsoft 365 groups only: `true` on a security group is `trait_invalid`. The group [becomes a team](#the-team). |
| `licences` | Licences / Lisenser | list | none | The licences the group's members get: each a part number (`[A-Za-z0-9_.-]`, at most 64), else `trait_invalid`. Whether the tenant subscribes to it is judged when it is assigned (`licence_unavailable`). |

`EnsureGroup` checks the values against the same rules, in that order, and answers the
first refusal; a key no trait declares is ignored, and a string is trimmed.

**The licences' choices are the tenant's SKUs as the module last read them**: the
connection test, when it connects, and `GET /connection/skus` write what
`subscribedSkus` answered to `connection.skus` — sorted by part number, at most 200,
each `{skuId, skuPartNumber, enabled, consumed}` — and the trait lists them, the part
number as the value and the seats left, never below 0, as the label:
`<part number> (<enabled − consumed> of <enabled> available)` in English and
`<part number> (<enabled − consumed> av <enabled> ledige)` in Norwegian. While the row
holds none, the trait has no choices. What the licences do is Staff's: a person is sent
the union of their groups' ([the licences](/en/reference/staff/#the-licences)), and
[Activate](#activate) and [Update](#update) reconcile them.

### EnsureGroup

**The spec.** A spec with no group id or key, or one over 60 characters, is a plain
error. The name is Staff's, trimmed and cut at 256 — the key when empty; the
description is the `description` trait's, else Staff's, cut at 1024. A value the
traits refuse is a `contracts.GroupTraitError` naming the trait and its code, recorded
as `refused:<code>` under `entra.group.updated` when the module has the group, else
`entra.group.created`; nothing is written to the tenant.

**The service principal owns every group the module makes** (design reading 10): a
group an application creates with no owner cannot be changed by it later. Its object
id is the token's `oid` claim — an application's token names its service principal in
the tenant, so no further permission is needed (plan reading 2). Every group write
takes it from the token it was opened with and writes it to
`connection.service_principal_id` when the row has none or names another — **the
token's `oid` wins over the row** — as the connection test does; a token without one
falls back to the row, and with neither the write fails.

**With no row for the Staff group** — or only a deleted or retired one — the module looks for a
group holding the mail nickname, `GET /groups?$filter=mailNickname eq '<nickname>'`,
the nickname's `'` escaped as `''`:

- **Two or more** is `ErrGroupNotManageable`: the module cannot tell which is Staff's
  (a security group's nickname need not be unique).
- **One** is adopted, unless the module [may not write it](#groups-in-the-tenant) or
  another of its rows holds its object id or, present, its nickname
  (`ErrGroupNotManageable`), or its type is not the `type` trait's (`GroupTraitError
  {type, trait_immutable}`) — each recorded under `entra.group.adopted`. **An adopted
  group is untrusted** (plan reading 11): its row is written at once, `created_by_us`
  false, and nothing is written to the tenant — no name, no description, no owner — the
  call answering `ErrGroupUnconfirmed` and recording `entra.group.adopted` with
  `type:<type>` and `refused:adoption_unconfirmed`, until a holder of `entra:manage`
  confirms it ([adopted groups](#adopted-groups)). A group the Staff group's own row
  names, found again — a security group the module [retired](#deletegroup) and Staff
  turned on anew, say — comes back present with who made it and its confirmation kept,
  and one the module made or whose adoption was confirmed needs no confirmation: Staff's
  name, description and visibility are written where they differ (`PATCH
  /groups/{id}`) and the owners it lacks are added by `POST /groups/{id}/owners/$ref` —
  the service principal among them — none removed, recorded as `entra.group.adopted`
  with `type:<type>`, a `changed:<property>` code for each property written and
  `owners_added:<n>`.
- **None** is a create, `POST /groups`, with `displayName`, `mailNickname`, by `type` —
  `security`: `mailEnabled` false, `securityEnabled` true, `groupTypes: []`;
  `microsoft365`: `mailEnabled` true, `securityEnabled` true, `groupTypes: ["Unified"]`
  and `visibility` (`Private` or `Public`) when the trait has one — `description` when
  there is one, and **`owners@odata.bind` naming the module's own service principal
  and the first nineteen owners' accounts**
  (`https://graph.microsoft.com/v1.0/servicePrincipals/<id>` and
  `https://graph.microsoft.com/v1.0/users/<id>`), the rest added by `$ref` after. A
  create carrying owners that is refused 400 or 404 — an owner the tenant does not
  hold — is made again with the service principal alone, and the owners added one by
  one, a person the tenant does not hold (a 404) skipped. **The create is made once**,
  never retried on a 5xx: when its answer is lost, the module reads the nickname back
  and takes the group as its own only when exactly one holds it, one it may write, of
  the type asked; otherwise the call fails. It answers `Created` true, writes the row
  `created_by_us` true, and records `entra.group.created` with `type:<type>`,
  `owners_added:<n>` for the owners added after the create, and `owners:<n>`, the
  people owning the group.

**The row is recorded under the connection's share lock**, at once after the create or
the adoption, so a failure after it finds the group next time: when the tenant or the
client moved since the call began, nothing is recorded, the group is left in the
tenant, and the call answers `ErrProviderUnavailable`. A deleted or retired row for the
same Staff group gives way to the new one (`ForgetDeletedGroup`) — but for the row of
the group found again, which is brought back; the old group's events keep its object
id. The module's rows are read again inside that transaction: a group another Staff
group's row holds is `ErrGroupNotManageable` and nothing is recorded — `InsertGroup`
never re-points another Staff group's row.

**With the row of an adopted group nobody has confirmed**, the call is
`ErrGroupUnconfirmed` before anything else is judged, nothing written — no row, no
event: the refusal is recorded once, on the adoption (`entra.group.adopted` with
`refused:adoption_unconfirmed`), not at each of Staff's polls until the group is
confirmed. **With any other
row**, a `type` other than the row's, or a `mailNickname` other than the row's, is
`GroupTraitError{…, trait_immutable}`, and nothing is written. The module reads the
group (`GET /groups/{id}`): **a group gone from the tenant** — a 404, deleted by
someone else — has its row marked `deleted` and is looked for by its nickname and
made again as above; a group it [may not write](#groups-in-the-tenant) any more is
`ErrGroupNotManageable`. Otherwise it `PATCH`es what differs of `displayName`,
`description` (an empty one cleared) and a Microsoft 365 group's `visibility`, and
reconciles the owners — `GET /groups/{id}/owners?$select=id`, at most 10 pages: the
service principal added back when it is missing, the people missing added by `$ref` (a
person the tenant does not hold skipped), and the users among the owners Staff no
longer names removed by `DELETE /groups/{id}/owners/{id}/$ref` in `$batch` requests of
twenty — **never the service principal**, nor an owner that is not a user. Recorded as
`entra.group.updated` with `changed:displayName`, `changed:description` and
`changed:visibility` as each was written, `owners_added:<n>` and `owners_removed:<n>`.
Staff's archive arrives here too: when a group is kept rather than deleted, Staff sends
its name with ` (archived)` after it (plan reading 5).

A write that fails after it changed something records its event with the codes so far
and `failed`. A trait refused here is kept beside the field by Staff, which does not
call `EnsureGroup` again until HR changes the group, but goes on sending the members of
a group the module holds ([Staff's sync](/en/reference/staff/#the-sync)).

### Adopted groups

A Staff group's mail nickname is named by a holder of `staff:manage`, so a group
adopted on its nickname alone would let HR take over any group in the tenant the module
may write — its name, its owners, its members and, through Staff's archive, its
existence. An adopted group is therefore untrusted, as an [adopted
account](#adopted-accounts) is (plan reading 11): until it is confirmed, `EnsureGroup`
and `SetMembers` answer `ErrGroupUnconfirmed` and write nothing — the refusal recorded
once, on the adoption, as `entra.group.adopted` with `refused:adoption_unconfirmed` —
and Staff's section waits `pending`
with `group_unconfirmed`, asked again at every poll with no attempt counted ([the
sync](/en/reference/staff/#the-sync)).

`POST /groups/{objectId}/confirm` (`entra:manage`) confirms it, in one transaction:
`adoption_confirmed_at` and `adoption_confirmed_by_user_id` are written and
`entra.group.adoption_confirmed` recorded with the caller as its actor. It answers 200
with the group. Idempotent: a group confirmed already, or one the module made, is
answered as it is and records nothing — never a 409, unlike an account's — and an
object id the module keeps no row for is a bare 404. Staff's next cycle then calls
`EnsureGroup`, which takes the row's path above: Staff's name, description and
visibility are written, and the owners made exactly Staff's people and the service
principal — **the group's own owners are removed** — and `SetMembers` makes the members
exactly Staff's. The module's page offers **Confirm** in its **Groups** table on a
present group the module neither made nor has had confirmed.

**The module never deletes a group it did not make**, confirmed or not:
[DeleteGroup](#deletegroup) leaves it to the tenant.

### The team

`team: true` on a Microsoft 365 group makes it a team in Microsoft Teams, on a call
that finds the row without `team_created_at`, after the group's own writes:

- **The group younger than fifteen minutes** (its `createdDateTime`) is not attempted:
  the answer carries `ExternalGroup.Pending: ["team"]` and the event the code
  `team_pending`, and Staff asks again at its next cycle (plan reading 7). A team
  cannot be made on a group the directory has not finished creating.
- **No person among the owners** is `GroupTraitError{team, trait_needs_owner}`, the
  event carrying `refused:trait_needs_owner`: the service principal alone cannot own a
  team.
- **Otherwise** `PUT /groups/{id}/team` with
  `{"template@odata.bind": "https://graph.microsoft.com/v1.0/teamsTemplates('standard')"}`,
  tried three times ten seconds apart while it answers 404, a 409 saying the group has
  a team already read as done. `team_created_at` is written and
  `entra.group.team_created` recorded after the write's own event. Still 404 after the
  third try, the answer is `Pending` as above; any other refusal fails the call after
  the row and the event are written.

`team: false` on a group that has a team changes nothing: the module never deletes a
team.

### SetMembers

Reconciles the group's members to exactly the accounts Staff names. A group the module
keeps no row for, or one marked deleted or retired, is `ErrGroupNotManageable`; an
adopted group nobody has confirmed is `ErrGroupUnconfirmed`, nothing read or written
and no event (the refusal was recorded once, on the adoption); an id
that is not a GUID is a failure `not_found`. It reads the group: one gone from the tenant (a 404)
has its row marked `deleted` and is `ErrGroupNotManageable`; one that became
role-assignable or Exchange's is refused `group_not_manageable`; **one that became
dynamic** changes nothing, each member to add or remove answered failed `dynamic`. It
reads the current members, `GET /groups/{id}/members?$select=id&$top=999`, page by
page, at most 100 pages — a longer list fails the call, never a partial reconcile — and
leaves a member that is not a user (a nested group, a device) alone:

- **Adds** in `PATCH /groups/{id}` with `members@odata.bind`, twenty to a request. A
  `PATCH` refused 400 or 404 falls back, for that batch, to one `POST
  /groups/{id}/members/$ref` per account: one already a member is no failure, a 404 is
  `not_found`, a 400 or 403 `refused`, another 4xx `other`, and a 5xx fails the call.
- **Removes** in `$batch` requests of twenty, each `DELETE
  /groups/{id}/members/{userId}/$ref` — **always with `/$ref`**, which removes the
  membership: the same path without it would delete the user. Each response is read on
  its own: 204 (or 200) is removed, 404 is removed already, 400 or 403 is `refused`,
  and anything else — a throttled one included — is `other`.

It answers `MembersResult{Added, Removed, Failed}`, each failure the account's id and a
code — `not_found`, `dynamic`, `refused` or `other` — and never an error for one; Staff
tries the failed ones again at its next cycle. **`last_members_at` is written when the
call finished** — failures of single members included — and not when a request failed.
`entra.group.members_set` is recorded with `added:<n>`, `removed:<n>` and `failed:<n>`,
and `failed` when a request failed after something changed.

### DeleteGroup

Lets go of the Staff group's group, its row kept, by who made it and by its kind (plan
readings 11 and 13). A group the module keeps no row for, or one marked deleted or
retired, answers nil with no connection and no call: there is nothing to let go of.

- **A group the module did not make** — adopted, confirmed or not — is the tenant's: no
  connection and no call. The row is marked `deleted` and `entra.group.deleted` recorded
  with the code `kept_not_ours`; the group stays in the tenant as it is.
- **A Microsoft 365 group the module made**, needing the status `connected`: `DELETE
  /groups/{id}` — a soft delete: it stays restorable in the tenant's deleted groups for
  30 days. A 404 is deleted already, recorded with the code `gone`. The row is marked
  `deleted` and `entra.group.deleted` recorded.
- **A security group the module made is never deleted**: Graph deletes a security group
  for good, with no deleted-groups container to restore it from. Needing the status
  `connected`, the module reads it — one gone from the tenant (a 404) is marked
  `deleted` with `gone`; one it [may not write](#groups-in-the-tenant) any more is
  `ErrGroupNotManageable`, recorded under `entra.group.retired` — then removes every
  user among its members (`GET /groups/{id}/members`, at most 100 pages) and every user
  among its owners by their reference, `DELETE …/$ref` in `$batch` requests of twenty —
  **never the service principal**, nor a member or an owner that is not a user — and
  renames it with ` (archived)` after its name, once (`PATCH`, the name cut so the whole
  fits 256). The row is marked `retired` with that name, and `entra.group.retired`
  recorded with `security_kept`, `removed:<n>`, `owners_removed:<n>` and
  `changed:displayName` when the name was written. A member or an owner the tenant would
  not remove fails the call, the row as it was, for Staff's next call to try again.

A group Staff keeps whole — its archive while `deleteExternalAfterDays` is never — is
renamed through `EnsureGroup`, not here.

## Connecting existing users

An installation that turns single sign-on on with users already in it connects each of
them through identity's announced link
([`POST /api/v1/identity/owner/users/{id}/sso-link`](/en/admin/authentication/#connect-an-existing-user-to-the-provider)).
The module offers two **Owner reads** of the tenant so that the Owner picks an account
rather than types it: the **directory search**, behind the picker in the connect
dialog, and the **exact matches**, behind the review that connects a whole workspace.
Neither writes anything: connecting stays identity's endpoint, which the host calls
once per chosen pair, and identity's refusals (`sso_link_taken`, `account_linked`,
`account_pending`) still decide what may be connected — `sso_link_taken` also when
another user's SCIM mapping names either key. No migration.

Both are under `policy:Owner+OwnerManagement` — an Owner, with a second factor when
Owners must use one (`OWNERS_REQUIRE_MFA`) — like the connection's writes, since they read the whole
tenant and identity's addresses; a holder of `entra:manage` alone, or a SystemAdmin who
is not an Owner, is refused 403 by the router. Each has a rate limit of its own per
client address: the search `entra-directory`, 60 a minute (a debounced picker asks once
per pause in typing), the matches `entra-matches`, 6 a minute (a review an Owner opens,
not a list they refresh); 429 beyond.

### What the module reads of identity

The users to connect and who holds an account belong to identity, so the module reads
them through **`contracts.SSOConnections`**, a single-provider port identity provides
(`Deps.SSOConnections`), never identity's tables
([module boundaries](/en/contributing/module-boundaries/) rules 5 and 12), and only
through the accessors in `internal/entra/contractscalls.go`, which refuse a call made
inside one of the module's transactions:

- `Unconnected` — every user who is not pending and has neither an OIDC link nor an
  announced link under the configured issuer, the caller excluded (an Owner connects
  themselves by signing in), in the order the Owner's user list shows them: Owners
  first, then by display name — at most the limit the caller asks for (the matches ask
  for 1001, to learn whether more than 1000 wait). Each is the user's id, address, employee number and
  whether they are disabled; an address identity made up for an account the provider
  created is answered empty. While OIDC is off it answers `ErrSSONotConfigured`.
- `KeyHolders` — for each account's object id and principal name, who already holds
  it under the configured issuer, the first of: `expected`, an Owner's announced link
  for an existing user; `mapped`, a SCIM mapping whose `userName` is the principal
  name or whose `externalId` is the object id; `account`, an account Staff announced
  for a person yet to arrive, with no user. While OIDC is off it answers nothing.

The port is **provider-gated**: identity checks the request in the context against
`policy:Owner+OwnerManagement` itself — a second factor required only when Owners must
use one, as on every Owner endpoint — and answers nothing, not an error, to anyone
else and to a context with no request. **An OIDC link cannot be attributed to a
directory account**: it holds the token's `sub`, which Graph never returns, so an
account whose person has already signed in is not marked as held. That user is linked,
so never a candidate, and the matches never propose them; the search may show their
account unmarked, and identity's `sso_link_taken` and `account_linked` refuse what must
be refused.

**A user's address never leaves the module.** It is compared with the tenant's
accounts and goes nowhere else: not on the wire (neither read answers a Vantigo user's
address), not in a log, an event or an error. The Graph client logs a refused call by
its path, never its query, and both reads' calls are made without Graph's error words
in the log (they may echo the search or the filter), so neither a search nor a filter
of addresses reaches the log either.

### The directory search

`GET /api/v1/entra/directory/users?q=` searches the tenant's accounts by display name,
mail and principal name. `q` loses every `"` and `\` (it is quoted into the search) and
must then be **2 to 100 characters** once trimmed, else 400 `invalid_request` with the
field `q`. The module asks Graph:

```http
GET /users?$search="displayName:<q>" OR "mail:<q>" OR "userPrincipalName:<q>"
    &$select=id,displayName,mail,userPrincipalName,employeeId,accountEnabled,userType
    &$orderby=displayName&$top=25&$count=true
ConsistencyLevel: eventual
```

— an advanced query, which Graph answers only with the `ConsistencyLevel` header. The
answer, `EntraDirectoryUsers`, is **at most 25** accounts by display name, each its
`objectId`, `displayName`, `userPrincipalName`, `mail` and `employeeId` when set,
`accountEnabled`, `guest: true` for a guest of the tenant (`userType` `Guest`, invited
from another organisation; absent for a member), and `holder` (`kind` `expected`, `mapped` or `account`, with the
`userId` for the first two) when `KeyHolders` says somebody holds it. When 25 come back
there may be more, and the picker asks the Owner to narrow the search. An account
another user or Staff holds is shown but cannot be picked; one the user being connected
holds already can. A pick hands the host both keys as one unit: in the connect dialog,
editing the principal name away from the picked account's clears the object id, and
changing the search after a pick clears both.

### The exact matches

`GET /api/v1/entra/directory/matches` reads `Unconnected` — **at most 1000** users, in
identity's order, asking identity for 1001; `truncated` is true when more were
waiting — and asks the tenant for
every account any of their keys could match: `mail in (…)` and
`userPrincipalName in (…)` by the users' addresses (trimmed, lower-cased, each once),
and `employeeId in (…)` by the employee numbers of those who have one, in filters of
**15 values** each (a `'` doubled, as OData quotes), `$top=999`, `$count=true` and
`ConsistencyLevel: eventual`. It then applies the rule, **exact only**:

- A **guest** account (`userType` `Guest`) matches nobody: it is set aside before
  matching, so it is never proposed and never makes a user ambiguous.
- A user's key matches an account when the user's address equals the account's
  `mail` (by `mail`) or its `userPrincipalName` (by `userPrincipalName`), ignoring case
  and surrounding space, or the user's employee number equals the account's
  `employeeId` (by `employeeId`), trimmed and ignoring case. An empty address — one
  identity made up — and an empty number match nothing.
- A user whose keys matched no account is neither proposed nor counted.
- A user is **proposed** when their keys matched **exactly one** account and no other
  user's key matched that account; `matchedBy` lists every key that matched it, in the
  order `mail`, `userPrincipalName`, `employeeId`.
- A user is **ambiguous** — left out and counted — when their keys matched two
  accounts or more, or one account that another user's key matched too (whether or not
  that user matched further accounts). Nobody is proposed by a guess between two, and
  there is no fuzzy match.
- A proposed account **held for somebody else** (`KeyHolders`: another user's announced
  link or SCIM mapping, or an account Staff announced, which has no user) is then left
  out, and not counted as ambiguous. The user's own SCIM mapping of the account does not
  block their proposal.

The answer, `EntraDirectoryMatches`, is the `matches` — each `{userId, account,
matchedBy}`, the account as the search answers it without a holder, in identity's
order — with `candidates`, how many users were read, `ambiguous`, how many were left
out as above, and `truncated`.

The whole read — identity's candidates, the tenant's filters and the holders — is
bounded by **two minutes**, past which it answers 503 `tenant_unreachable`. An Owner's
second request while their first is still reading waits for it and shares its answer
(one read in flight per caller, keyed by the caller, since what identity answers
depends on who asks); a waiting request whose shared read was cancelled by the other
client leaving reads again.

The review proposes each with a checkbox, ticked only when the account is enabled,
the principal name matched (`userPrincipalName` in `matchedBy`) and the user is not an
Owner; any other row starts unticked and is marked *Check this match*, with the reason
— matched by mail or employee ID only, or the user is an Owner, whose access whoever
holds the account would sign in with. **Connect selected** announces the chosen pairs one at a time
through identity, and each is fulfilled at the person's next sign-in or the provider's
next push.

The search's and the matches' refusals:

| Code | Status | When |
| --- | --- | --- |
| `invalid_request` | 400 | The search: `q` is not 2 to 100 characters once trimmed, under `errors.q`. |
| `sso_not_configured` | 409 | The matches: single sign-on is off, so there is no issuer to connect anybody under. |
| `credential_invalid` | 503 | No secret fetched a token, or no tenant or client is saved — as the licences' read answers it. |
| `tenant_unreachable` | 503 | The tenant did not answer; for the matches, also when the read took more than two minutes. |
| `throttled` | 503 | Graph throttled the read twice in a row. |

## The mailbox calendar

The module fills the single-provider slot `MailboxCalendar` (`contracts.MailboxCalendar`,
`mailbox.go`) for the calendar's [Outlook sync](/en/reference/calendar/#outlook-sync): it
writes the calendar's copies of a person's appointments and items to their Exchange
Online mailbox, [reads the busy time](#the-reads) of their default calendar back, and
[reads the copies](#the-write-backs-reads) and the *Vantigo* calendar's events for the
calendar's write-back, through the [Graph client](#the-graph-client) and its token, with the application
permission `Calendars.ReadWrite`, which covers the reads. The calendar decides what the copies are;
the module writes them and answers what it did, per operation ([module
boundaries](/en/contributing/module-boundaries/) rule 14). `Key()` is `entra`, the
provider-link key Staff's `DirectoryAccount` names, so the calendar can tell a person's
account is this module's before it writes. The writes are not recorded in
[`entra.events`](#the-events): they are the calendar's, and its worker keeps their
record.

### The calendar sync switch

`entra.connection.calendar_sync` (migration `00082_entra_calendar_sync.sql`, false by
default) is the Owner's switch, **Calendar sync** on [the account
rules](#the-account-rules) — `calendarSync` in `PUT /connection/settings`. It is not a
[required permission](#the-required-permissions) of the connection: provisioning works
with it off, and with the permission missing. `GET /connection` answers it as
`calendarSync: {enabled, status, reason?}`:

| `status` | When |
| --- | --- |
| `off` | The switch is off. |
| `ready` | On, the connection `connected`, and the last test's token carried `Calendars.ReadWrite`. |
| `permission_missing` | On and `connected`, but the last test's token did not carry `Calendars.ReadWrite`. **Advisory**: expected when the permission was granted with Exchange RBAC for Applications, and otherwise a sign to add it; the sync runs either way. |
| `unavailable` | On, but the connection is not `connected`; `reason` is the connection's `statusReason`, when it has one. |

### The status

`Status` reads the connection's row and the secrets and nothing else — no network, as
`Provider` — and answers, in this order:

| State | Reason | When |
| --- | --- | --- |
| `available` | — | The switch is on and the connection `connected` (with or without a reason such as `secret_expiring`), whether or not the last test's token carried `Calendars.ReadWrite`: a grant made with RBAC for Applications never shows there, and a mailbox outside the grant answers its own 403, `ErrMailboxDenied`. |
| `cleanup` | `switch_off` | The switch is off, and a credential still works: the tenant and the client saved, a secret unrevoked and unexpired, and the connection not `failing` with `credential_invalid`. |
| `unavailable` | `switch_off` | The switch is off, and no credential works. |
| `unavailable` | the connection's reason, else its status | The switch is on and the connection is not `connected`. |

In `cleanup` a person's copies can still be removed: `Apply` takes deletes only, and
`DeleteCalendar` and `Find` work; `Delta`, `Marked`, `Versions` and `Events` do not,
since a read is never needed to remove copies. Every reason is a code, never the directory's words.

### The account check

Every call but `Key` and `Status` checks the status first — `available` for
`EnsureCalendar`, `Delta`, `Marked`, `Versions` and `Events`; `available` or `cleanup` for `DeleteCalendar`, `Apply` and `Find`;
otherwise `ErrMailboxUnavailable` — and then the mailbox. **The module writes only to an
account it manages**: the `mailbox` must be an object id that is a row of
`entra.accounts` the module created, or whose [adoption](#adopted-accounts) is confirmed,
and not `deleted` — otherwise `ErrMailboxUnknown`, and nothing is sent. So the calendar
can never point it at a mailbox the module does not own. A connection with no tenant,
client or usable secret is `ErrMailboxUnavailable`. A calendar or event id the caller
hands back must be at most 512 printable characters with no space, and never a path
segment of its own — `.` or `..`, bare or percent-encoded, judged through every decoding
— which would move a `$batch` request's URL off the item it names; a calendar's name is
1 to 251 characters, room left for ` (5)`.

**The markers name the installation.** Every event the module writes carries the
single-value extended property `String {7b1c5e3a-6f0d-4d5e-9a40-3c7a5e2b9d11} Name
VantigoKey`, its value `vantigo:<client id>:<key>`; every calendar it makes carries
`String {7b1c5e3a-6f0d-4d5e-9a40-3c7a5e2b9d11} Name VantigoCalendar`, its value
`vantigo:<client id>` — the client id the Entra application's, a lower-case GUID, which
tells two installations sharing a tenant apart. A calendar or an event without this
installation's marker is never the module's: never adopted, deleted or answered by
`Find` or `Marked`.

### The Graph calls

| Call | Graph |
| --- | --- |
| `EnsureCalendar` | `GET /users/{oid}/calendars/{known}?$select=id,name&$expand=singleValueExtendedProperties($filter=id eq '<calendar marker>')` while `known` is set — found carrying this installation's marker, it is the answer; a 404 for the item, or a calendar without the marker, goes on —, then `POST /users/{oid}/calendars` `{name, singleValueExtendedProperties: [the marker]}`, sent once (no retry of a 5xx). A 409 or `ErrorFolderExists` reads `GET /users/{oid}/calendars?$filter=name eq '<name>'&$select=id,name&$expand=…` and adopts the calendar of that name only when it carries the marker — a create whose answer was lost; otherwise it tries the next name, `<name> (2)` up to `<name> (5)`, then fails. A calendar of the person's own is never adopted. |
| `DeleteCalendar` | `GET /users/{oid}/calendars/{id}` with the marker's `$expand` first: a calendar without this installation's marker is `ErrCalendarNotOurs` and is never deleted; then `DELETE /users/{oid}/calendars/{id}`, which deletes its events with it. A 404 for the item is done. |
| `Apply` | `POST /$batch` of at most 20 requests, the batches one after another — Outlook allows four concurrent requests per mailbox. A create is `POST /users/{oid}/events` (the default calendar) or `POST /users/{oid}/calendars/{id}/events`; an update `PATCH /users/{oid}/events/{id}`; a delete `DELETE /users/{oid}/events/{id}`. Each request's body carries `Content-Type: application/json`. |
| `Find` | `GET /users/{oid}/events` (the default calendar) or `GET /users/{oid}/calendars/{id}/events`, with `$filter=singleValueExtendedProperties/Any(ep: ep/id eq '<event marker>' and startswith(ep/value, 'vantigo:<client id>:'))` — and `and start/dateTime ge '<from, UTC>'` when `from` is set — `&$expand=singleValueExtendedProperties($filter=id eq '<event marker>')&$select=id,attendees&$top=100`, in either calendar: only this installation's events are answered, each with its key and `Meeting` when it has attendees — decoded as empty objects, their count alone, never an address. It follows `@odata.nextLink` — refused when the link leaves Graph's base — and stops at 5000. A secondary calendar whose list answers 404 for the item on the first page — the calendar gone, not the mailbox — answers no events. |

**The event** as Graph takes it: `subject`; `body` as `text`; `location.displayName`
when there is one — on an update always, so a location dropped is cleared; `isAllDay`;
`start` and `end`; `showAs` (`busy` when none was given; anything outside the five
values refuses the op); `sensitivity` `private` or `normal`; `isReminderOn: false`; and
the event marker carrying `vantigo:<client id>:` and the calendar's key — on a create
always, and on an update when the op names a `Key`, the same value a copy already
carries, so an event the calendar [took in](/en/reference/calendar/#new-events-in-the-vantigo-calendar)
from the *Vantigo* calendar is tagged by the push that rewrites it; an update with no
`Key` leaves the marker as it is. A timed event is laid in `UTC`; an
**all-day** event runs from midnight of its first day to midnight of the day after its
last, in the **Windows name of its zone** (`Europe/Oslo` is `W. Europe Standard Time`):
`windowszones.go` mirrors CLDR's `windowsZones.xml`, which the calendar holds for the
other direction, inverted, with the territory entries for the zones a territory names
besides its golden one — and `UTC` for a zone it does not know. An event that ends
before it starts, a create with no key and an update or delete with no id refuse that op
alone.

**Each answer is judged on its own:**

| Answer | Result |
| --- | --- |
| 2xx | Done: a create's id and every write's `changeKey` read from the answer (a create answered with no id is a failure). |
| A mailbox gone — `MailboxNotEnabledForRESTAPI`, `ErrorInvalidUser`, `ErrorNonExistentMailbox` or `Request_ResourceNotFound`, as the code or the first detail's code | `ErrMailboxGone` |
| 403 | `ErrMailboxDenied` — the mailbox is outside the application's scope (an RBAC for Applications scope) |
| 404 on an update or a delete | `Gone` (a delete is then done) |
| 429 | Sent again once, with the others of its batch answered 429, after the longest `Retry-After` among them — only when it is at most 30 seconds, so one mailbox never holds the calendar's worker; a second 429, or a longer wait, is a throttled failure |
| Anything else | Graph's refusal, a failure to retry |

Graph's bare `ResourceNotFound` and `ErrorItemNotFound` are never read as the mailbox
gone, so an event or a calendar gone is never mistaken for its mailbox. Once an op is
refused for its mailbox (denied or gone), the ops in the batches not yet sent answer the
same, unsent. Under `cleanup` an op other than a delete answers `ErrMailboxUnavailable`
unsent. A `$batch` that fails whole — Graph unreachable, or a 5xx after its one retry —
fails the call, and no result is answered; **a batch holding a create is sent once**,
with no retry of a 5xx, since the tenant may have made the event — the calendar's
[reconcile](/en/reference/calendar/#the-mailbox-sync-worker) finds it by its key. A
response the `$batch` did not answer is a failure. A refusal is logged at debug level by
the tenant, the op's kind, the status and the codes alone — never Graph's words, which
may echo a subject or a body, nor any subject, body or address.

### The reads

The calendar's [busy read](/en/reference/calendar/#the-busy-read) asks two things of the
person's mailbox. Neither decodes, logs or answers anything of what an event is about.

**`Delta`** reads the changes to the **default calendar**, a page at a time:

- `link` `""` starts a delta afresh: `GET /users/{oid}/calendarView/delta?startDateTime=
  <from, UTC>&endDateTime=<to, UTC>`. A window that ends before it starts is refused
  unsent.
- Any other link is followed only when it is **this mailbox's delta**: it holds no `#`,
  `\` or space, and parses as `https` on `graph.microsoft.com` exactly, with no user, no
  fragment, no percent-encoding in its path, a query, and the path
  `/v1.0/users/{oid}/calendarView/delta` of the account checked above, compared
  whatever its case (Graph answers `calendarview` and the id in either). Else the call
  is a failure, unsent. The path sent is the module's own, built from the `oid`, with
  only the link's query after it, so a stored link can never point the client at
  another mailbox or off Graph.
  Every `@odata.nextLink` and `@odata.deltaLink` Graph answers is checked the same way,
  and one that fails is a failure.
- Each page is asked with `Prefer: outlook.timezone="UTC", odata.maxpagesize=50` and
  read through the 16 MiB cap. The call follows `@odata.nextLink` for at most **40
  pages**: at the `@odata.deltaLink` it answers `Done` with it; at the 40th page it
  answers what it read with the next link, to be continued. A page with neither link is
  a failure.
- Of each event it decodes `id`, `@removed`, `type`, `isAllDay`, `isCancelled`, `showAs`,
  `start` and `end` — **nothing else**; the subject, body, location, organizer and
  attendees Graph answers are read past. An event with `@removed` is its id alone,
  `Removed`. `type` `occurrence` or `exception` is `Series`. `showAs` is matched to the
  five values whatever its case, `""` for another. An event whose id breaks the id
  rule above is left out before anything else, so neither the calendar's rows nor
  `Marked` ever see one. A timed event's `start` and `end`
  are instants (UTC as asked; a zone that is not is read as an IANA name, and one that
  is neither does not read — never taken as UTC and so placed hours off); an all-day
  event's are rounded to the nearest midnight, the end made inclusive. An event with no
  id, or whose times do not read, is left out — counted at debug level with the tenant,
  never named — rather than failing a page the delta would otherwise stop at for ever.
- **Expired**: a 410, or a 4xx whose code or first detail's code is `syncStateNotFound`,
  `syncStateInvalid`, `resyncRequired` or `ErrorInvalidSyncStateData` (whatever the
  case), is `ErrMailboxDeltaExpired`. The other refusals are judged as `Apply`'s: a
  mailbox gone `ErrMailboxGone`, 403 `ErrMailboxDenied`, anything else a failure.

**`Marked`** answers, among at most 100 ids — each held to the id rule above — the
events that carry this installation's event marker: `POST /$batch` of at most 20
`GET /users/{oid}/events/{id}?$select=id&$expand=singleValueExtendedProperties($filter=id
eq '<event marker>')`, the batches one after another, read through the 8 MiB cap. A 200
whose marker's value starts `vantigo:<client id>:` is answered with the key after it, in
the order asked; a 200 without it, or with another installation's, is not; nor is a 404
for the item. A 429 is sent again once, alone, after a `Retry-After` of at most 30
seconds, else a throttled failure; a mailbox gone, a 403 or any other refusal fails the
whole call, as `Apply`'s would.

Both log a refusal at debug level by the tenant, the status and the codes alone — never
Graph's words, nor an event's id, subject or body.

### The write-back's reads

The calendar's [write-back](/en/reference/calendar/#the-write-back) asks two more things
of the mailbox (`mailbox_writeback.go`), both refused under `cleanup`. Neither logs,
nor wraps in an error, a word of what an event says.

**`Versions`** answers the calendar's events' ids and change keys a page at a time:

- `calendarID` `""` is the **default calendar**: `GET /users/{oid}/events` with `Find`'s
  filter — `$filter=singleValueExtendedProperties/Any(ep: ep/id eq '<event marker>' and
  startswith(ep/value, 'vantigo:<client id>:'))`, and `and start/dateTime ge '<from,
  UTC>'` when `from` is set — `&$expand=singleValueExtendedProperties($filter=id eq
  '<event marker>')&$select=id,changeKey,type&$top=500`. Each event's `Key` is cut from
  its marker; one carrying no key of this installation's is left out. `to` is not used.
- A **secondary calendar** — the *Vantigo* calendar — is
  `GET /users/{oid}/calendars/{id}/calendarView?startDateTime=<from, UTC>&endDateTime=<to,
  UTC>&$select=id,changeKey,type&$top=500`: every event of the view, with no `Key`. A
  calendar id that breaks the id rule, or a window that ends before it starts, is
  refused unsent.
- Each page is read through the 8 MiB cap. Of each event it decodes `id`, `changeKey`,
  `type` and the marker and nothing else; `type` `occurrence`, `exception` or
  `seriesMaster`, whatever its case, is `Series`. An event whose id breaks the id rule
  is left out, counted at debug level, never failing the page.
- It follows `@odata.nextLink` only when the link is **a page of the very list sent**:
  no `#`, `\` or space, `https` on `graph.microsoft.com` exactly, no user and no
  fragment, a query, and the path `/v1.0` plus the path sent, compared unescaped and
  whatever its case — percent-encoding in the path only when the path sent carries it
  (a secondary calendar's id escaped), and then only as an encoding of the same path.
  The path sent next is the module's own, with the link's query alone after it. Any
  other link is a failure, unsent.
- At most **5000** events (`MailboxVersionsMax`): past them the list is cut and
  `Complete` is false.
- A refusal is judged as `Apply`'s: a mailbox gone `ErrMailboxGone`, 403
  `ErrMailboxDenied`, anything else a failure.

**`Events`** reads whole, among at most 100 ids — each held to the id rule, else
refused unsent — the events of the calendar, in the order asked:

- `POST /$batch` of at most 20 `GET`s, the batches one after another, each **through
  the calendar's own path** — `/users/{oid}/calendar/events/{id}` for the default
  calendar, `/users/{oid}/calendars/{cid}/events/{id}` for a secondary one — so an
  event moved to another calendar or folder is not found there. Each asks
  `$select=id,changeKey,type,subject,body,location,start,end,isAllDay,originalStartTimeZone,showAs,sensitivity,isCancelled,attendees`
  with the marker's `$expand`, and carries its own `Prefer: outlook.timezone="UTC",
  outlook.body-content-type="text"`. The answer is read through the **16 MiB** cap, as
  a delta page's: a body asked as text may still come as HTML, and twenty of those
  outgrow the 8 MiB one.
- **200** is answered: `Key` this installation's key, `Foreign` when the event carries
  a marker that is not this installation's, `Series` as above, `Cancelled`
  `isCancelled`, `Meeting` when it has attendees — decoded as empty objects, their
  count alone, never an address —, `TextBody` when the body's `contentType` is `text`,
  and the subject, the body's content, `location.displayName`, the times (a timed
  event's instants in UTC; an all-day event's the dates its UTC instants fall on in the
  zone it was laid in, `originalStartTimeZone` — a Windows name mapped through CLDR's
  table, whatever its case, or an IANA name — the end inclusive, and, when that zone
  maps to no location, rounded to the nearest midnight as `Delta`'s are), `showAs`
  matched to the five values, and `Private` for a `private` or `confidential`
  sensitivity. A 200 for another id than the one asked, whose body is not the JSON
  expected, or whose times do not read, is **skipped**, counted at debug level.
- **404** for the item — not a mailbox gone — is answered in `Gone`: the event is not
  in that calendar any more.
- **403** for one event is skipped too: a refusal of that event alone, never
  `ErrMailboxDenied`, which is the mailbox's own refusal — of the `$batch` itself, or
  of `Versions`.
- **429** is sent again once, alone, after the longest `Retry-After` among the
  throttled, at most 30 seconds; else a throttled failure.
- A `$batch` whose answer is larger than the 16 MiB cap, or is not the JSON expected,
  is sent again **one id at a time**; an id whose read alone still is is skipped,
  counted. A skipped id is neither answered nor `Gone`, so the calendar infers nothing
  from it and asks again at the next sync.
- A refusal of the `$batch` POST itself goes as `Apply`'s would — a 403 there is
  `ErrMailboxDenied` (`mailbox_denied`) —, and so does any other refusal of one event —
  a mailbox gone, say —, failing the whole call; so does a response the `$batch` did
  not answer. A refusal is logged at debug level by the tenant, the status and the
  codes alone.

## The events

Every write the module makes, in the tenant or to its own connection, is a row in
`entra.events`, read newest first by `GET /events?limit=` (1 to 200, 50 by default; a
limit outside that is a 400) on the **Status** card:

| Action | Written by |
| --- | --- |
| `entra.connection.changed` | `PUT /connection` |
| `entra.connection.tested` | `POST /connection/test` |
| `entra.settings.changed` | `PUT /connection/settings` |
| `entra.credential.added`, `entra.credential.revoked` | the two secret operations |
| `entra.user.prepared`, `entra.user.activated`, `entra.user.deactivated`, `entra.user.updated`, `entra.user.deleted` | the lifecycle |
| `entra.user.photo_set`, `entra.user.photo_cleared` | [the photo](#setphoto-and-clearphoto) |
| `entra.pass.issued`, `entra.pass.revealed` | the pass |
| `entra.account.adoption_confirmed` | `POST /accounts/{objectId}/confirm` |
| `entra.group.created`, `entra.group.adopted`, `entra.group.updated`, `entra.group.team_created`, `entra.group.members_set`, `entra.group.deleted`, `entra.group.retired` | [the groups](#groups-in-the-tenant), with the group's `objectId` — none on a refusal before the module had one — and no UPN |
| `entra.group.adoption_confirmed` | `POST /groups/{objectId}/confirm`, with the group's `objectId` |

A lifecycle write the tenant refused is recorded under its own action with
`refused:<code>`. The codes carry the details each section names — `tenant_before:`,
`label:`, `expires_at:`, `missing:<permission>`, a changed field's name, `created` or
`found`, `changed:<property>`, `enabled`, `hire_date_written`, `licences_assigned:<n>`,
`licences_removed:<n>`, `licences_not_removed`, `pass_issued`, `disabled`,
`sessions_revoked`, `leave_date_written`, `licences_stripped`, `groups_removed`, `gone`,
`photo_written`, `photo_cleared`, and for a group `type:<type>`, `changed:<property>`, `owners:<n>`, `owners_added:<n>`,
`owners_removed:<n>`, `team_pending`, `added:<n>`, `removed:<n>`, `failed:<n>`, `failed`
(a write that failed after it changed something), `gone`, `kept_not_ours`,
`security_kept` and `refused:<code>` (`refused:adoption_unconfirmed` among them) — and
the reported codes. The module's page words each action and each code it knows, in
English and Norwegian, and shows one it does not know as it is. The trail is
the module's only record of its writes: nothing goes to the platform's audit log.

Each answers `id`, `at`, `actor` — the user's id, or the system's name
(`system:staff-accounts` for Staff's worker) — `action`, `codes`, and for a user's event
`objectId` and `upn`. Nothing else: never an address beyond the UPN, a secret or a pass.

`GET /accounts` (`entra:manage`) lists every account the module keeps, by UPN —
`objectId`, `employeeNumber`, `userPrincipalName`, `state`, `createdByUs`,
`adoptionConfirmed`, `hasPass`, `reported`, `lastSyncedAt` and `createdAt` — never the
pass. The **Status**
card lists them once the module keeps any.

`GET /groups` (`entra:manage`) lists every group the module keeps for Staff's, deleted
and retired ones included, by display name — `objectId`, `groupId`, `key`,
`displayName`, `mailNickname`, `type`, `team` (true once it became a team), `state`,
`createdByUs`, `adoptionConfirmedAt`, `lastMembersAt` and `createdAt`. Staff decides
what exists; the one write on the groups here is the [confirmation](#adopted-groups).
The module's page shows them in its **Groups** table.

## Refusals

A refusal is an RFC 9457 problem, `EntraProblem`, whose `code` names the rule and is
what a client keys off, never the words:

| Code | Status | When |
| --- | --- | --- |
| field errors under `errors` | 400 | A form field broke its rule, keyed by the request's camelCase field. |
| `connection_in_use` | 409 | The tenant or the client changes while the module keeps accounts, without `confirmChange`. |
| `credential_in_use` | 409 | Revoking the only valid secret while the module keeps accounts. |
| `adoption_confirmed` | 409 | Confirming an account confirmed already, or one the module created. |
| `version_mismatch` | 412 | The connection was written since the `If-Match` version. |
| `credential_invalid` | 503 | `GET /connection/skus`, `GET /directory/users`, `GET /directory/matches`: no secret fetched a token, or no tenant or client is saved. |
| `tenant_unreachable` | 503 | `GET /connection/skus`, `GET /directory/users`, `GET /directory/matches`: the tenant did not answer. |
| `throttled` | 503 | `GET /directory/users`, `GET /directory/matches`: Graph throttled the read twice in a row. |
| `sso_not_configured` | 409 | `GET /directory/matches` while single sign-on is off. |
| `invalid_request` | 400 | `GET /directory/users`: `q` is not 2 to 100 characters once trimmed, under `errors.q`. |

A request the server could not decode is a bare 400 with neither. The 401 (no session),
the 403 (the permission, or the Owner policy — an Owner without a second factor
included) and the 429 (`entra-owner`, `entra-skus`, `entra-directory`, `entra-matches`) are the router's.

## Testing

The module's tests run against a **fake Graph**, `internal/entra/graphtest`: an
`http.RoundTripper` that answers both hosts — the token endpoint, the organisation, the
domains, the SKUs with their seats, users (create, filter by employee id, patch, soft
delete, the manager, sessions, licences, the pass, the photo — a JPEG of at most 4 MB,
413 beyond, 400 for another type, 403 when told the photo is denied), groups — the create with
`owners@odata.bind`, the nickname filter, owners and members by `$ref`, paged members,
`$batch` of twenty, the team after a delay, and a trap that fails a test when a member's
removal lacks `/$ref`; mailboxes — calendars made, read, listed by name and deleted, events
made in the default or a secondary calendar with the extended property, patched,
deleted and listed with the marker filter, the `$expand` and paging, each with a
`changeKey` that moves with every write, a secondary calendar's `calendarView`, an
event read through its calendar's own path (404 for one of another calendar), and the
person's own edits to an event in Outlook, a mailbox denied
(403 `ErrorAccessDenied`) or absent (404 `MailboxNotEnabledForRESTAPI`), and requests
inside a `$batch` throttled one by one; the directory's `$search` over display name, mail and principal name (a `$search` or a `$count` without `ConsistencyLevel: eventual` is 400 `Request_UnsupportedQuery`, as Graph answers it), `$orderby=displayName`, `$top`, and `in` filters on `mail`, `userPrincipalName` and `employeeId` — and can be told to throttle, fail the next call, refuse a sensitive action for a privileged user
or a missing role, or answer a synchronised user. It is wired through
`modtest.WithTransport`, so the module's real client runs against it. It lives under the
module's own package and only the module's tests import it.

A **tagged test**, `tenant_test.go`, settles against a real tenant what the fake cannot
know — whether an application may write `employeeHireDate`, whether the pass is issued
with its permission, and what the module answers for a user synchronised from
on-premises — and runs a group through the tenant: made with the service principal and
an owner (a security group whose nickname starts with `vt-test-`), ensured again to
find the same group, the user added as a member and removed by `$ref` in a `$batch`,
then let go by `DeleteGroup` — a security group the module made, so emptied, renamed
` (archived)` and kept in the tenant, where each run leaves one (the team is not
attempted, the group being younger than fifteen minutes): `go test -tags entra -run TestEntraTenant -v ./internal/entra/` with
`ENTRA_TEST_TENANT_ID`, `ENTRA_TEST_CLIENT_ID`, `ENTRA_TEST_CLIENT_SECRET` and
`ENTRA_TEST_DOMAIN` set (and `ENTRA_TEST_SYNCED_USER_ID` with
`ENTRA_TEST_SYNCED_EMPLOYEE_NUMBER` for the third), skipped without them. It runs the
real guarded transport. What the tenant needs is in [the administration
guide](/en/admin/entra/#the-tagged-test).

## Endpoints

All under `/api/v1/entra`.

| Operation | Access |
| --- | --- |
| `GET /connection` | `entra:manage` |
| `PUT /connection` | `policy:Owner+OwnerManagement`; `If-Match`; `entra-owner` (30 a minute per client address, shared by the four Owner operations) |
| `POST /connection/credentials` | `policy:Owner+OwnerManagement`; `entra-owner` |
| `DELETE /connection/credentials/{id}` | `policy:Owner+OwnerManagement`; `entra-owner` |
| `POST /connection/test` | `policy:Owner+OwnerManagement`; `entra-owner` |
| `PUT /connection/settings` | `entra:manage`; `If-Match` |
| `GET /connection/skus` | `entra:manage`; `entra-skus` (30 a minute per client address, its own bucket) |
| `GET /events` | `entra:manage` |
| `GET /accounts` | `entra:manage` |
| `POST /accounts/{objectId}/confirm` | `entra:manage` |
| `GET /groups` | `entra:manage` |
| `POST /groups/{objectId}/confirm` | `entra:manage` |
| `GET /directory/users?q=` | `policy:Owner+OwnerManagement`; `entra-directory` (60 a minute per client address, its own bucket) |
| `GET /directory/matches` | `policy:Owner+OwnerManagement`; `entra-matches` (6 a minute per client address, its own bucket) |

The request and response shapes are in the contract,
[`openapi/entra.yaml`](https://github.com/vantigo-io/vantigo/blob/main/openapi/entra.yaml),
and in the API reference generated from it.
