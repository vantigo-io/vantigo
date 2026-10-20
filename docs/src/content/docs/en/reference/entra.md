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
  marked `deleted`.
- **`events`** — the module's own append-only trail of its writes: `id`, `at`, the
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
audit row or an error. A rotation of `APP_SECRET` leaves both unopenable: the secrets
must be added again and any pass waiting is lost (Staff's **Run now (new pass)** issues
another).

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
Owner sees those steps read-only and is refused 403 on their writes. The four Owner
operations share one rate limit, `entra-owner`: 30 requests a minute per client
address, identity's user-management figures.

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
or quoted). It moves the version on, sends the status back to `unconfigured` and clears
`lastCheckedAt` until the next test, and is audited `entra.connection.changed` with the
ids before and after. **While the module keeps accounts** (`entra.accounts` has rows) a
change of either id is refused `409 connection_in_use` unless the request carries
`confirmChange: true`: the accounts were made in the old tenant, and the module would
now look for them in the new one. A change deletes nothing in the tenant either way.

### The credentials

The connection authenticates to the tenant with a **client secret** of its app
registration, and holds them **in the plural**, so a secret is rotated without a gap:

- **The one used** is the newest unrevoked secret whose `expires_at` is ahead. It is
  opened for each token request and never kept open.
- **A refusal moves on.** A token request refused `invalid_client` or
  `unauthorized_client` marks the secret `last_failed_at`, and the next valid one is
  tried. When none is left the connection is `failing` with `credential_invalid`.
  A success marks `last_used_at`, at most once a minute per secret.
- **`POST /connection/credentials`** (step 2) adds one: `kind` `client_secret` (the only
  kind; a certificate is a later one), `label` (at most 100 characters, the secret's
  description in the portal or anything that tells two apart), `secret` (1 to 1000
  characters) and `expiresAt` (in the future and at most 24 months ahead, Microsoft's
  own limit). It is sealed at once, answered `201` without its value, and audited
  `entra.credential.added` with the label and the dates.
- **`DELETE /connection/credentials/{id}`** revokes one: it is kept, marked
  `revokedAt`, and never used again; nothing changes in the tenant, where the Owner
  deletes the secret themselves. The **only valid** secret is refused `409
  credential_in_use` while the module keeps accounts, since nothing could then disable
  a leaver. An unknown id is a 404 with an empty body. Audited `entra.credential.revoked`.

**Rotation** is *Add a secret* with the new value, then *Revoke* on the old one. Thirty
days before the newest valid secret ends, the status reads `secret_expiring`; once
every unrevoked secret has ended, `failing` with `secret_expired`. Staff raises an
attention item for each ([below](#the-status-and-its-reasons)).

Each secret in the answer carries `id`, `kind`, `label`, `expiresAt`, `createdAt`,
`lastUsedAt`, `lastFailedAt`, `revokedAt` and `inUse` — true for the one the module
uses now. They are listed newest first, revoked ones included.

### The connection test

**`POST /connection/test`** (step 3) takes a token with the secret in use, reads `GET
/organization` — the `displayName` and the `verifiedDomains[].name` that are verified —
and compares the token's `roles` claim with the [required
permissions](#the-required-permissions). It writes what it found to the connection —
`status`, `status_reason`, `permissions_present`, `verified_domains`,
`organisation_name`, `last_checked_at` — and answers it, **200 whatever it found**, a
failure included:

| Found | `status` | `reason` |
| --- | --- | --- |
| A token, and the organisation answered | `connected` | — |
| No secret fetched a token (`invalid_client`, or none valid) | `failing` | `credential_invalid` |
| The token or Graph host did not answer, or answered 5xx | `failing` | `tenant_unreachable` |
| A token with no `roles` at all: admin consent was never granted | `failing` | `consent_missing` |

The answer is `{ status, organisationName?, verifiedDomains, permissionsPresent,
permissionsMissing, roleKnown, reason? }`. **`roleKnown` is always false**: whether the
application holds the User Administrator role cannot be read without a further
permission, so the wizard asks the Owner to check it in the portal, and the module
learns it from the first [sensitive action](#the-sensitive-actions-and-role_missing).
The test does not move the connection's `version`. Audited `entra.connection.tested`
with the status and the names of the missing permissions.

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
connection's row and nothing else** — no network call — because Staff asks it on every
`GET /meta` and every cycle. The status is written by the connection test and by the
lifecycle's refusals; the reason is derived as it is read:

| Status | Reason | When |
| --- | --- | --- |
| `unconfigured` | — | The tenant, the client or a valid secret is missing. |
| `failing` | `secret_expired` | No unrevoked secret's expiry is ahead. |
| `failing` | `credential_invalid` | No secret fetched a token, at the test or since. |
| `failing` | `tenant_unreachable` | The tenant did not answer the test. |
| `failing` | `consent_missing` | The token carried no permission at all. |
| `connected` | `secret_expiring` | The newest valid secret ends within 30 days. |
| `connected` | `role_missing` | Three sensitive writes in a row were refused, none succeeding since. |
| `connected` | — | Nothing to say. |

Every reason is a code within Staff's bound (`^[a-z_]{1,40}…$`), never the directory's
words. Staff turns the status into its installation items: `unconfigured` is
`entraNotConnected`, `failing` with `secret_expired` is `secretExpired`, `failing` with
any other reason is `entraFailing`, and `secret_expiring` is `secretExpiring`
([Staff's attention items](/en/reference/staff/#attention-items)). `role_missing`
raises no installation item: each account it stops reports `privileged` and shows as
`accountPrivilegedInEntra`, and the module's **Status** card names the reason.

While the status is not `connected`, Staff writes nothing to the module but a
`Deactivate`, and every other write answers `contracts.ErrProviderUnavailable`
([below](#the-user-lifecycle)).

## The account rules

**`PUT /connection/settings`** (step 4, `entra:manage`) sets how accounts are made and
unmade, under `If-Match` on the connection's `version`. Every field is optional and one
left out keeps its value; each refusal is a field error under its own name. It moves
the version on, answers the whole connection and is audited `entra.settings.changed`.

| Field | Rule | Default |
| --- | --- | --- |
| `upnDomain` | One of `verifiedDomains` once a test has read them; before that, any lower-case domain name, at most 253 characters | unset |
| `upnPattern` | `given.family`, `g.family` or `given` — the [address](#the-address) for a person whose work email cannot be the UPN | `given.family` |
| `defaultUsageLocation` | Two letters, ISO 3166-1 alpha-2: the `usageLocation` written when Staff sends no work country. A licence cannot be assigned without one. | `NO` |
| `writeHireDate` | Write the first day as `employeeHireDate` | on |
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
seats bought, `prepaidUnits.enabled`) and `consumed`. It answers `503
credential_invalid` when no secret fetched a token and `503 tenant_unreachable` when the
tenant did not answer.

## The Graph client

Every call to the tenant goes through one client, built once per process and shared by
the API and the workers:

- **Two fixed hosts**, never a setting: `https://login.microsoftonline.com/{tenant}/oauth2/v2.0/token`
  for the token (the `client_credentials` grant, scope
  `https://graph.microsoft.com/.default`) and `https://graph.microsoft.com/v1.0` for
  everything else. A test's fake Graph comes in through the transport, not a base URL.
- **The guard.** The transport dials through `internal/netguard`, the platform's guard on
  outbound addresses, uses **no proxy** and follows **no redirect**; keep-alives are
  on, since the same host is called many times in a cycle. Dial 10 s, TLS handshake
  10 s, response headers 15 s, response headers at most 64 KiB, and each call under a
  30-second budget.
- **The token** is cached in the process per secret, under a mutex, until five minutes
  before its `expires_in`; its `roles` claim is read for the test (its signature is not
  checked: it is the token the tenant just gave). A token is never cached across
  secrets.
- **Throttling.** A 429 waits its `Retry-After`, at most 60 seconds, once; a second 429
  fails the call, and Staff's worker tries again at its next poll.
- **A 5xx is retried once**, after a second.
- **Every response body is read through a 1 MiB cap**; a larger one is refused.
- **Graph's errors** (`{ error: { code, message } }`) are mapped to a sentinel or a
  reported code by status and code. The message is logged at debug level with the
  tenant's id, and **the body is never stored** — not in a row, an event or anything a
  person sees.

## The user lifecycle

The module's `IdentityProvisioner` executes Staff's steps. Every write checks its actor
first (`contracts.ErrInvalidActor` for one that is neither a user nor a system), takes a
working secret — `contracts.ErrProviderUnavailable` when none works or the connection
is `unconfigured`, **`Deactivate` excepted**, which runs with any working secret
whatever the status — and makes its Graph calls on no transaction, writing its own rows
in short transactions after each. Every write is idempotent and records an
[event](#the-events). What it could not do is answered as `Reported` codes, which Staff
shows on the person's Account tab:

| Code | Means |
| --- | --- |
| `hire_date_not_written` | Graph refused `employeeHireDate`; the account was enabled without it. |
| `licence_unavailable:<part number>` | That licence could not be assigned (no seat left, or another assignment error); the others were. |
| `pass_not_allowed` | The tenant's policy refused the Temporary Access Pass. |
| `manager_not_found` | The manager's account was not found in the tenant. |
| `privileged` | A sensitive action was refused ([below](#the-sensitive-actions-and-role_missing)). |

The refusals that stop a step are the contract's sentinels, which Staff records on the
link ([the provider link](/en/reference/staff/#the-provider-link)):

| Sentinel | When |
| --- | --- |
| `ErrExternalAmbiguous` | Two users in the tenant hold the employee number. |
| `ErrExternalAddressTaken` | The work email, or every suffix up to 99, is another user's UPN. |
| `ErrExternalSyncedOnPremises` | The user is synchronised from on-premises Active Directory (`onPremisesSyncEnabled`), which Graph will not write. |
| `ErrExternalPrivileged` | A sensitive action was refused 403. |
| `ErrExternalEnabled` | A delete of an account that is still enabled. |
| `ErrPassGone` | No pass waits, or it has expired. |
| `ErrProviderUnavailable` | The connection is not set, or no secret works. |

Anything else — a throttled call, a 5xx after its retry, a network failure — is a plain
error, which Staff counts and retries.

### Prepare

`GET /users?$filter=employeeId eq '<number>'&$select=id,userPrincipalName,mail,accountEnabled,onPremisesSyncEnabled,employeeId`,
the number's `'` escaped as `''`:

- **Two rows** is `ErrExternalAmbiguous`.
- **One row** is linked — `created_by_us` false — and brought to the spec by an
  [Update](#update); `onPremisesSyncEnabled` on it is `ErrExternalSyncedOnPremises`.
- **None** is a create, `POST /users`, with `accountEnabled: false`, `displayName`,
  `givenName`, `surname`, `mailNickname` and `userPrincipalName` from [the
  address](#the-address), a `passwordProfile` with 32 random characters (from
  `A-Za-z0-9!#%+-`, at least one of each class) and
  `forceChangePasswordNextSignIn: true` — never stored, never logged, never shown: the
  pass replaces it —, `employeeId`, `usageLocation` (Staff's work country, else the
  default), `preferredLanguage` (`nb-NO` or `en-GB`), and the fields Staff's directory
  profile switches on under Graph's names, `costCenter` and `division` written together
  as `employeeOrgData`, since Graph replaces the object whole. Then the manager, by
  `PUT /users/{id}/manager/$ref`, when Staff names one; a 404 there reports
  `manager_not_found`. A 400 `ObjectConflict` on the UPN is `ErrExternalAddressTaken`
  when the work email was the UPN, and a retry with the next suffix when the pattern
  made it.

The account is answered with `Created` true only on the call that created it, and its
row upserted. Audited `entra.user.prepared`. Nothing is licensed and nobody can sign in
yet.

### Activate

On the first day:

1. `PATCH /users/{id}` with `accountEnabled: true` and, when `write_hire_date` is on,
   `employeeHireDate` as the hire day's 01:00 in the business time zone. A 400 or 403
   naming that property is retried without it and reports `hire_date_not_written` —
   whether Graph accepts the property from an application is one of the facts [the
   tagged test](#testing) settles.
2. `POST /users/{id}/assignLicense` with the licences Staff names that the user does not
   hold yet, their part numbers resolved to SKU ids through `subscribedSkus`. A
   `licenseAssignmentStates[].error` (such as `CountViolation`, no seat left) reports
   `licence_unavailable:<part number>`; the rest are assigned.
3. The pass, when the account has no unrevealed, unexpired pass or Staff's step is
   **Run now (new pass)** (`Flags.Fresh`): `POST
   /users/{id}/authentication/temporaryAccessPassMethods` with `lifetimeInMinutes` and
   `isUsableOnce` from the connection. The pass is sealed under `entra/pass` with its
   expiry and the account answers `HasPass` true; audited `entra.pass.issued`. A 400 on
   the create — the tenant's authentication methods policy does not enable the pass for
   the user — reports `pass_not_allowed`, and there is no pass.

Audited `entra.user.activated`.

### Deactivate

The day after the end, in this order, each step on by the connection unless named
always. **A step that fails leaves the rest to the next cycle**, and the account is
disabled first whatever else fails:

1. `PATCH accountEnabled: false` — **always, first**: it is what continuous access
   evaluation reacts to.
2. `POST /users/{id}/revokeSignInSessions` (`leaver_revoke_sessions`): every refresh
   token and session cookie stops working within minutes. An access token already
   issued lives out its hour.
3. `PATCH employeeLeaveDateTime` as the end day's 23:00 in the business time zone
   (`leaver_write_leave_date`) — in the past by the time Staff disables, which is what a
   Lifecycle Workflows leaver trigger wants.
4. `POST /users/{id}/assignLicense` removing every **direct** licence — those whose
   `assignedByGroup` is null (`leaver_strip_licences`). A licence assigned through a
   group goes with the group.
5. The groups, by `leaver_remove_groups`: **`all`** removes the person from every group
   `memberOf` lists that is not dynamic (`membershipRule` set), role-assignable or
   mail-enabled only (Exchange's), groups the module never made included, each by
   `DELETE /groups/{id}/members/{userId}/$ref`; **`managed`** from the module's own
   groups only — none until groups land; **`none`** leaves every membership.

Any waiting pass is cleared. Audited `entra.user.deactivated` with what each step
reported.

### Update

Writes the attributes that changed and the manager, with the same names as the create.
The employee id is **never rewritten**: it is how the account is found.
Audited `entra.user.updated`.

### Delete

Refuses an account that is still enabled (`ErrExternalEnabled`), else `DELETE
/users/{id}` — a soft delete: the user sits in the tenant's deleted users for 30 days
and can be restored there, after which it is gone with its mailbox and OneDrive. The
row is marked `deleted` and kept. A rehire after a delete is a new account. Audited
`entra.user.deleted`.

### Account

`GET /users/{id}`, answered as the account; a 404 answers nil, which Staff records as
`user_gone` and never re-creates. The row is left as it was, for the events' sake.

### RevealPass

Takes the pass in one statement — the sealed value cleared and `pass_revealed_at` set
in the same `UPDATE … RETURNING`, so two callers cannot both have it — opens it, and
answers it with its expiry. A pass past `pass_expires_at` is cleared and answers
`ErrPassGone`, as does an account with none. Audited `entra.pass.revealed` with the
actor and the object id, never the pass.

## The address

The UPN — and `mailNickname`, its local part, at most 64 characters — is made once, at
the create:

1. **The person's work email**, lower-cased, when its domain is one of the tenant's
   verified domains and its local part is in the UPN's alphabet after folding.
2. **Else the pattern** over the folded names at `upn_domain`: `given.family`
   (`kari.nordmann`), `g.family` (`k.nordmann`) or `given` (`kari`), with `2`, `3`, …
   up to `99` appended on `ObjectConflict`, then `ErrExternalAddressTaken`.

**Folding**: the names are decomposed (NFD) and their marks dropped (`é` → `e`), the
three Norwegian letters replaced by table (`Æ` → `AE`, `Ø` → `O`, `Å` → `A`, `æ` →
`ae`, `ø` → `o`, `å` → `a`), and anything outside the UPN's alphabet before the `@` —
`A-Za-z0-9'.-_!#^~` — dropped. `Ørjan Åsmundsen` becomes
`orjan.asmundsen@<upn_domain>`. `mail` is never written (it is Exchange's). The address
made is answered as the account's `Email`, which Staff writes to the person when they
had none.

## The pass

A **Temporary Access Pass** is the person's first credential: a time-limited code they
type at their first sign-in to register their own methods. The module issues it on the
first day ([Activate](#activate)), with the lifetime and the single use the connection
sets; the tenant's authentication methods policy must enable the pass for the user,
or Graph refuses it (`pass_not_allowed`). It is **sealed** under `entra/pass` until it
is shown, **shown once** — through `RevealPass`, which Staff calls when HR chooses
**Show the pass** on the person's Account tab — and never mailed, logged or answered by
any of the module's endpoints. `GET /accounts` says only `hasPass`. A new pass issued
by Graph replaces the last one in the tenant; one that expired unseen is cleared on the
next read.

## The sensitive actions and role_missing

Graph's sensitive actions — `accountEnabled`, `passwordProfile`, the UPN, the phones,
the delete and the pass — need the **User Administrator** role on the application's
service principal as well as the permission, and even with it an application cannot
act on a user who holds an administrator role, is in a role-assignable group or a
restricted management administrative unit. A 403 on one is
`ErrExternalPrivileged`: the account reports `privileged`, its `last_error` is set, and
the connection's `sensitive_denied_streak` goes up by one. A sensitive action that
succeeds sets the streak back to 0.

**A 403 cannot say which of the two it is** — a privileged person, or the role not
assigned. So the connection reads `role_missing` once three sensitive actions in a row
were refused with none succeeding since, which is what "every account fails it" means
in practice. Assigning the role and running the person's step again clears it.

## Users synchronised from on-premises

A user whose `onPremisesSyncEnabled` is true is mastered by on-premises Active Directory
and Graph refuses to write it. The module reads the flag on the user it finds and stops
with `ErrExternalSyncedOnPremises`; a hybrid tenant is not supported in this release.

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

Each answers `id`, `at`, `actor` — the user's id, or the system's name
(`system:staff-accounts` for Staff's worker) — `action`, `codes`, and for a user's event
`objectId` and `upn`. Nothing else: never an address beyond the UPN, a secret or a pass.

`GET /accounts` (`entra:manage`) lists every account the module keeps — `objectId`,
`employeeNumber`, `userPrincipalName`, `state`, `createdByUs`, `hasPass`, `reported`,
`lastSyncedAt` and `createdAt` — read-only, never the pass.

## Refusals

A refusal is an RFC 9457 problem, `EntraProblem`, whose `code` names the rule and is
what a client keys off, never the words:

| Code | Status | When |
| --- | --- | --- |
| field errors under `errors` | 400 | A form field broke its rule, keyed by the request's camelCase field. |
| `connection_in_use` | 409 | The tenant or the client changes while the module keeps accounts, without `confirmChange`. |
| `credential_in_use` | 409 | Revoking the only valid secret while the module keeps accounts. |
| `version_mismatch` | 412 | The connection was written since the `If-Match` version. |
| `credential_invalid` | 503 | `GET /connection/skus`: no secret fetched a token. |
| `tenant_unreachable` | 503 | `GET /connection/skus`: the tenant did not answer. |

A request the server could not decode is a bare 400 with neither.

## Testing

The module's tests run against a **fake Graph**, `internal/entra/graphtest`: an
`http.RoundTripper` that answers both hosts — the token endpoint, the organisation, the
domains, the SKUs with their seats, users (create, filter by employee id, patch, soft
delete, the manager, sessions, licences, the pass), groups and their members — and can
be told to throttle, fail the next call, refuse a sensitive action for a privileged user
or a missing role, or answer a synchronised user. It is wired through
`modtest.WithTransport`, so the module's real client runs against it. It lives under the
module's own package and only the module's tests import it.

A **tagged test** settles against a real tenant what the fake cannot know — whether an
application may write `employeeHireDate`, the pass's permission, and which properties a
synchronised user refuses: `go test -tags entra -run TestEntraTenant ./internal/entra/`
with `ENTRA_TEST_TENANT_ID`, `ENTRA_TEST_CLIENT_ID`, `ENTRA_TEST_CLIENT_SECRET` and
`ENTRA_TEST_DOMAIN` set (and `ENTRA_TEST_SYNCED_USER_ID` for the third), skipped without
them. What the tenant needs is in [the administration guide](/en/admin/entra/#the-tagged-test).

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
