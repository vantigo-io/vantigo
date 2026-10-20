---
title: "Microsoft Entra ID module"
description: "Microsoft Entra ID as Staff's identity provider: the connection to one tenant, its client secrets in the plural and the connection test; the status and its reasons, read by Staff with no network call; the account rules; the guarded Graph client and its limits; the user lifecycle per step — prepared disabled, enabled on the day with the hire date, the licences and a Temporary Access Pass, kept in step, disabled with the leaver's steps, soft-deleted — with the Graph calls and the codes; the address rule; the pass; the sensitive actions and role_missing; the events, the permission, the refusals and the endpoints."
sources:
  - apps/server/internal/entra
  - apps/entra/frontend
  - openapi/entra.yaml
  - apps/server/internal/openapi/specs/entra.yaml
  - apps/server/internal/db/migrations/00061_entra_baseline.sql
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

It depends on nobody but identity, and in this release reads nothing of identity's
either: the Owner's check and `entra:manage`'s are the router's. It writes nothing
outside its own schema, and nothing in it imports another module. It reaches the
internet in one place, the [Graph client](#the-graph-client), and only two fixed hosts
there.

Groups — Staff's groups kept in the tenant, with their traits — are a later delivery's:
until it lands, the group calls of the contract answer
`contracts.ErrGroupNotManageable`, and `GroupTraits` declares the two traits a group's
Entra section will carry, `type` (a choice, `security` or `microsoft365`, required) and
`mailNickname` (a string).

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
    `verified_domains`, `organisation_name` (200) and `last_checked_at`;
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
  write), `last_error` (60, the last refusal's code), `last_synced_at` and the two
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
| `entra:manage` | Manage Microsoft Entra ID | The tile and its page; the connection's status, secrets (without their values), accounts and events; the [account rules](#the-account-rules) and the tenant's licences. | yes | **no** |

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
the last check, the account rules, every secret without its value, and the accounts'
counts by state (`count`, `enabled`, `disabled`, `deleted`) — whether or not anything is
set, so the wizard can show every step before the first is done. It reads the module's
own rows only; nothing reaches the tenant. `permissionsMissing` is the
[required permissions](#the-required-permissions) less those the last test's token
carried; all of them before the first test.

**`PUT /connection`** (step 1) sets `tenantId` and `clientId`, both GUIDs as the
portal's **Overview** shows them, under `If-Match` on the connection's `version` (bare
or quoted, or weak). A change of either id moves the version on, sends the status back
to `unconfigured`, forgets what the last test found — `lastCheckedAt`, the permissions
present, the verified domains and the organisation's name — and the sensitive streak,
and is recorded as the event `entra.connection.changed` with the codes
`tenant_before:`, `tenant_after:`, `client_before:` and `client_after:` (`none` for an id
not set before). **The same pair again writes nothing**: no version, no event; the
answer is the connection as it is. **While the module keeps accounts**
(`entra.accounts` has rows) a change of either id is refused `409 connection_in_use`
unless the request carries
`confirmChange: true`: the accounts were made in the old tenant, and the module would
now look for them in the new one. A change deletes nothing in the tenant either way.

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
The test does not move the connection's `version`. Recorded as the event
`entra.connection.tested`, with the status, the reason and a `missing:<permission>` code
for each permission missing.

### The required permissions

The application permissions (Microsoft Graph, *Application* type, admin consented) the
module needs, which the test compares with the token's `roles`:

| Permission | For |
| --- | --- |
| `User.ReadWrite.All` | Finding a user by employee id, creating, updating, enabling and disabling, the manager, deleting |
| `User-LifeCycleInfo.ReadWrite.All` | `employeeHireDate` and `employeeLeaveDateTime` |
| `User.RevokeSessions.All` | Revoking a leaver's sessions |
| `UserAuthMethod-TAP.ReadWrite.All` | The Temporary Access Pass |
| `LicenseAssignment.ReadWrite.All` | Assigning and removing licences, and `subscribedSkus` |
| `Group.ReadWrite.All` | A leaver's group memberships, and the groups of the later delivery |
| `Domain.Read.All` | The verified domains |
| `Organization.Read.All` | The organisation, read by the test |

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

**`passDelivery` is stored and offered, and nothing acts on it yet**: the pass is
revealed only on the person's Account tab in Staff, to HR, and no notice is sent to the
manager in this release; a later one gives Staff the setting and the notice together.

`GET /connection/skus` (`entra:manage`) answers the licences the tenant subscribes to,
read from `GET /subscribedSkus` through the connection: `skuId`, `skuPartNumber` (the
part number a Staff group's licences name, such as `ENTERPRISEPACK`), `enabled` (the
seats bought, `prepaidUnits.enabled`) and `consumed`, sorted by part number. It
answers `503 credential_invalid` when no secret fetched a token (or no tenant or client
is saved) and `503 tenant_unreachable` when the tenant did not answer. The wizard shows
the list under step 4 while the connection is `connected`.

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
- **A 5xx is retried once**, after a second.
- **A 401 drops the cached token** and the call is retried once with a fresh one.
- **Every response body is read through a 1 MiB cap**; a larger one is refused.
- **Graph's errors** (`{ error: { code, message, details } }`) are mapped to a
  sentinel or a reported code by the status, the code and the first detail's code (a
  taken UPN is `ObjectConflict` there). The message is logged at debug level with the
  tenant's id, the method and the path — never the query, which may carry an employee
  number — and **the body is never stored**: not in a row, an event or anything a
  person sees.

## The user lifecycle

The module's `IdentityProvisioner` executes Staff's steps. Every write checks its actor
first (`contracts.ErrInvalidActor` for one that is neither a user nor a system).
**`Prepare`, `Activate`, `Update` and `Delete` need the status `connected`**, as
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
| `pass_not_allowed` | The tenant's policy refused the Temporary Access Pass. |
| `manager_not_found` | The manager's account was not found in the tenant, or Staff named no valid object id. |
| `privileged` | A sensitive action was refused ([below](#the-sensitive-actions-and-role_missing)). |
| `leaver_step_failed:<step>` | A leaver's step after the disable failed — `revoke_sessions`, `leave_date`, `strip_licences` or `remove_groups` — and the steps after it were not tried ([Deactivate](#deactivate)). |

The refusals that stop a step are the contract's sentinels, which Staff records on the
link ([the provider link](/en/reference/staff/#the-provider-link)):

| Sentinel | When |
| --- | --- |
| `ErrExternalAmbiguous` | Two users in the tenant hold the employee number, or the module's row for the number names another user the tenant still holds. |
| `ErrExternalAddressTaken` | The work email, or every suffix up to 99, is another user's UPN. |
| `ErrExternalSyncedOnPremises` | The user is synchronised from on-premises Active Directory (`onPremisesSyncEnabled`), which Graph will not write. Checked on every write to a user. |
| `ErrExternalPrivileged` | A sensitive action was refused 403. |
| `ErrExternalEnabled` | A delete of an account that is still enabled. |
| `ErrPassGone` | No pass waits, or it has expired. |
| `ErrProviderUnavailable` | The connection is not set, or no secret works. |

Each refusal sets the account's `last_error` and is recorded under the write's own
event with the code `refused:<code>` — `ambiguous`, `address_taken`,
`synced_on_premises`, `privileged` or `enabled`. Anything else — a throttled call, a 5xx
after its retry, a network failure, no domain to make an address under — is a plain
error, which Staff counts and retries.

### Prepare

`GET /users?$filter=employeeId eq '<number>'&$select=id,userPrincipalName,mail,accountEnabled,onPremisesSyncEnabled,employeeId`,
the number's `'` escaped as `''`:

- **Two rows** is `ErrExternalAmbiguous`.
- **One row** is read whole and linked — `created_by_us` false, unless the module made
  it — and brought to the spec by the [Update](#update) path; `onPremisesSyncEnabled`
  on it is `ErrExternalSyncedOnPremises`.
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
  suffix when the pattern made it; a 403 is `ErrExternalPrivileged`. Then the manager,
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
2. `POST /users/{id}/assignLicense` with the licences Staff names that the user does not
   hold yet, their part numbers resolved (ignoring case) to SKU ids through
   `subscribedSkus`. A part number the tenant does not subscribe to, or a
   `licenseAssignmentStates[].error` (such as `CountViolation`, no seat left), reports
   `licence_unavailable:<part number>`; the rest are assigned. A 4xx on the whole call
   reports every licence it carried.
3. The pass, **when Staff's step asks for a fresh one** — **Run now (new pass)**,
   `Flags.Fresh` — **or when this call enabled the account and no unrevealed, unexpired
   pass waits**; an `Activate` of an account already enabled issues none otherwise.
   `POST /users/{id}/authentication/temporaryAccessPassMethods` with
   `lifetimeInMinutes` and `isUsableOnce` from the connection. The pass is sealed under
   `entra/pass` with its expiry — Graph's `startDateTime` plus its lifetime — and the
   account answers `HasPass` true; recorded as the event `entra.pass.issued` with
   `expires_at:<instant>`. A 400 on the create — the tenant's authentication methods
   policy does not enable the pass for the user — reports `pass_not_allowed`, and there
   is no pass; a 403 is `ErrExternalPrivileged`.

An enabled user with nothing missing is answered as it is. Recorded as the event
`entra.user.activated` with `enabled`, `hire_date_written`, `licences_assigned:<n>`
and `pass_issued` as each happened, and the reported codes.

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
   `assignedByGroup` is null (`leaver_strip_licences`). A licence assigned through a
   group goes with the group.
5. The groups, by `leaver_remove_groups`: **`all`** removes the person from every group
   `memberOf` lists (at most 50 pages) that is a group — not a directory role or an
   administrative unit — and not dynamic (`membershipRule` set), role-assignable or a
   distribution list (mail-enabled, neither security-enabled nor Microsoft 365, which
   Exchange keeps), groups the module never made included, each by `DELETE
   /groups/{id}/members/{userId}/$ref`, a membership already gone counting as done;
   **`managed`** from the module's own groups only — none until groups land;
   **`none`** leaves every membership.

**A step that fails stops the steps after it**: it is logged, reported as
`leaver_step_failed:<step>` (`revoke_sessions`, `leave_date`, `strip_licences` or
`remove_groups`), and the call still answers the account disabled. A later
`Deactivate` runs the steps again from the first — each is safe to repeat — and
finishes the rest; Staff, though, calls `Deactivate` only on an account that is still
enabled, so it makes no such call by itself. Recorded as the event `entra.user.deactivated` with `disabled`,
`sessions_revoked`, `leave_date_written`, `licences_stripped` and `groups_removed` as
each happened, and the step that failed.

### Update

Writes the attributes that changed and the manager, with the same names as the create.
The employee id and the UPN are **never rewritten**: the id is how the account is
found. A phone (`mobilePhone`, `businessPhones`) is a sensitive property: a 403 on a
`PATCH` carrying one is `ErrExternalPrivileged`. Recorded as the event
`entra.user.updated` with a `changed:<property>` code for each, `changed:manager`, and
the reported codes.

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
disable or a delete clears any pass waiting.

## The sensitive actions and role_missing

Graph's sensitive actions — `accountEnabled`, `passwordProfile`, the UPN, the phones,
the delete and the pass — need the **User Administrator** role on the application's
service principal as well as the permission, and even with it an application cannot
act on a user who holds an administrator role, is in a role-assignable group or a
restricted management administrative unit. The module treats a 403 on the create, the
enable, the disable, a phone's update, the delete or the pass as one: it is
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
changing anything; a hybrid tenant is not supported in this release.

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
| `entra.pass.issued`, `entra.pass.revealed` | the pass |

A lifecycle write the tenant refused is recorded under its own action with
`refused:<code>`. The codes carry the details each section names — `tenant_before:`,
`label:`, `expires_at:`, `missing:<permission>`, a changed field's name, `created` or
`found`, `changed:<property>`, `enabled`, `hire_date_written`, `licences_assigned:<n>`,
`pass_issued`, `disabled`, `sessions_revoked`, `leave_date_written`,
`licences_stripped`, `groups_removed`, `gone` — and the reported codes. The trail is
the module's only record of its writes: nothing goes to the platform's audit log.

Each answers `id`, `at`, `actor` — the user's id, or the system's name
(`system:staff-accounts` for Staff's worker) — `action`, `codes`, and for a user's event
`objectId` and `upn`. Nothing else: never an address beyond the UPN, a secret or a pass.

`GET /accounts` (`entra:manage`) lists every account the module keeps, by UPN —
`objectId`, `employeeNumber`, `userPrincipalName`, `state`, `createdByUs`, `hasPass`,
`reported`, `lastSyncedAt` and `createdAt` — read-only, never the pass. The **Status**
card lists them once the module keeps any.

## Refusals

A refusal is an RFC 9457 problem, `EntraProblem`, whose `code` names the rule and is
what a client keys off, never the words:

| Code | Status | When |
| --- | --- | --- |
| field errors under `errors` | 400 | A form field broke its rule, keyed by the request's camelCase field. |
| `connection_in_use` | 409 | The tenant or the client changes while the module keeps accounts, without `confirmChange`. |
| `credential_in_use` | 409 | Revoking the only valid secret while the module keeps accounts. |
| `version_mismatch` | 412 | The connection was written since the `If-Match` version. |
| `credential_invalid` | 503 | `GET /connection/skus`: no secret fetched a token, or no tenant or client is saved. |
| `tenant_unreachable` | 503 | `GET /connection/skus`: the tenant did not answer. |

A request the server could not decode is a bare 400 with neither. The 401 (no session),
the 403 (the permission, or the Owner policy — an Owner without a second factor
included) and the 429 (`entra-owner`) are the router's.

## Testing

The module's tests run against a **fake Graph**, `internal/entra/graphtest`: an
`http.RoundTripper` that answers both hosts — the token endpoint, the organisation, the
domains, the SKUs with their seats, users (create, filter by employee id, patch, soft
delete, the manager, sessions, licences, the pass), groups and their members — and can
be told to throttle, fail the next call, refuse a sensitive action for a privileged user
or a missing role, or answer a synchronised user. It is wired through
`modtest.WithTransport`, so the module's real client runs against it. It lives under the
module's own package and only the module's tests import it.

A **tagged test**, `tenant_test.go`, settles against a real tenant what the fake cannot
know — whether an application may write `employeeHireDate`, whether the pass is issued
with its permission, and what the module answers for a user synchronised from
on-premises: `go test -tags entra -run TestEntraTenant -v ./internal/entra/` with
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
| `GET /connection/skus` | `entra:manage` |
| `GET /events` | `entra:manage` |
| `GET /accounts` | `entra:manage` |

The request and response shapes are in the contract,
[`openapi/entra.yaml`](https://github.com/vantigo-io/vantigo/blob/main/openapi/entra.yaml),
and in the API reference generated from it.
