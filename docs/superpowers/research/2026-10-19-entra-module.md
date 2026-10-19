# Entra module — research before a design

Research for Vantigo's first external identity provider: a module of its own, `entra`,
that makes the account Staff's flag creates a user in the company's Microsoft Entra ID
tenant — created without licences ahead of the start, enabled on the day, disabled
with its licences, groups and sessions stripped at the end — with the Vantigo sign-in
following through SSO, correlated on the employee number; the groups Staff owns
translated into the tenant's groups; and the OIDC and SCIM configuration moved from
environment variables into the database on the way. The outline is the roadmap's
*Later* paragraph under Staff and the owner's decisions of 2026-10-11
(`2026-10-11-staff-module.md` §10, "R" below, cited by item: R §10.10, R §10.12–15,
R §10.17–19). Three inputs: what the repository holds now that Staff phase 1 is built,
what Microsoft Graph and Entra's own provisioning do and demand, and how comparable
products run joiner, mover and leaver. Uncertain items are marked **UNCERTAIN**; every
Microsoft fact was checked against learn.microsoft.com on 2026-10-19 and the page is
named.

`srv/` abbreviates `apps/server/internal/`, `mig/` `srv/db/migrations/`,
`boundaries.md` the page `docs/src/content/docs/en/contributing/module-boundaries.md`,
`S` the staff foundation design's decisions and readings.

## 1. Where the repository stands (2026-10-19, `main` at `a529e1af`)

### 1.1 What identity holds, and from where

**Two integrations, both bound at startup.** `config.OIDCConfig` (provider `entra` or
`google`, authority, client id, a client secret **or** a workload-identity token file,
Google's allowed domains, a display name) and `config.SCIMConfig` (a token, an optional
previous token with an expiry of at most 24 h) are read from the environment once
(`srv/config/config.go:1258-1475`); a nil pointer means off. Everything that depends on
them reads the pointer held on `identity.Access` or `Deps.Config` **per request**, never
a copy:

| Reader | What it reads | Where |
| --- | --- | --- |
| the session query | `ScimEnabled` as a parameter: a directory-deactivated non-Owner has no session while SCIM is on | `access.go:233`, `queries/sessions.sql:35` |
| `UserMayAct` (the calendar feed's `AsUser` read) | the same flag, so a token outlives nothing a session would not | `access.go:183`, `sessions.sql:64` |
| sign-in, passkeys, two-factor | the same flag on the user they resolve | `signin.go:93,352`, `passkeys.go:471,587` |
| the SCIM bearer check | the two tokens, compared by SHA-256 in constant time | `scim.go:166-175` |
| the OIDC relying party | the whole `OIDCConfig`, **built once per process** in `newServer` (`server.go:63`): the normalised issuer every `oidc_links` row is keyed on, the discovery cached under a mutex, the client secret or the assertion file read fresh at every redemption | `oidc.go:113-200,699` |
| the sign-in page | `GET /identity/providers` answers the display name while OIDC is on | `oidc.go:241` |
| the Owner's status | `staticOidcEnabled`, `staticOidcProvider`, `staticScimEnabled` | `system.go:136-185` |
| the user directory | `scimEnabled` copied at construction into `userDirectory` | `userdirectory.go:46` |
| the provisioning provider | `scimEnabled()` for `DisabledByDirectory` | `provisioning.go:156` |
| the authority mode | `cfg.OIDC != nil \|\| cfg.SCIM != nil` ⇒ `federated`, recorded at the API's mount and re-derived by every provisioning write | `authority_mode.go:30-115` |

That is the whole surface a move into the database has to cover: one flag read in six
places, two tokens in one, one relying party built once, two status fields, and the
derivation of the mode. Nothing else in the server knows the variables exist.
`system_settings` (`mig/00002:248`, one row, `id = 1`) is the installation's table —
maintenance mode, the recorded authority mode, the business time zone — and is read
either per request (the business zone, time zones design D2) or through a 20-second
in-process cache (maintenance, `system.go:28-62`).

**What the Entra OIDC sign-in is today** (`admin/sso-scim.md`): the authorization code
flow with PKCE and a nonce, scopes `openid profile email`, the fixed callback
`/api/v1/identity/oidc/callback`, the claim policy requiring `tid` equal to the
authority's tenant and an `oid` (`oidc_claims.go`), just-in-time provisioning keyed on
`(issuer, sub)` into `identity.oidc_links` with the `User` role and nothing more
(`oidc.go:660-697`), an email collision refused (`oidc_email_conflict`), a pending
Staff user refused (`account_locked`) so an invitation is the only way in for them.
Provider claims never become roles. The claims read are `iss sub aud azp tid oid email
email_verified hd name given_name family_name preferred_username` — **no employee id,
no groups**. Nothing links an OIDC identity to a Staff person except the email that
Staff's `Prepare` refuses to match on (S24).

**What the SCIM server is today**: `Users` and `Groups` under one static bearer token,
matched on `externalId` (`scim_users.go:390`), `userName` unique, `upstream_active` on
`identity.scim_user_mappings`, `source_profile` holding only the name parts, and
**still no enterprise extension** — the research's finding 7 (R §1.2) stands
unchanged: `employeeNumber`, `department` and `manager` in a POST or PUT are dropped,
and a PATCH on their path answers 400 `invalidPath`. SCIM groups become
`identity.access_groups` of source `scim` whose **role mappings** an Owner sets
(`groups.go`), which is the one place today where an Entra group decides a Vantigo
permission.

### 1.2 What Staff built for the provider that is not there yet

Delivery C (`docs/superpowers/plans/2026-10-17-staff-accounts.md`, delivered
2026-10-18) left exactly the seams the roadmap's paragraph expects:

- `contracts.AccountProvisioning` (`srv/contracts/accounts.go`) is the one write
  direction staff → identity, in the single-provider slot `Accounts`, resolved by
  `Compose` and `Workers`; identity's provider is stateless and answers `Mode`
  `local` or `federated`. The decision table (`accounts_decide.go`, pure) and the
  worker (`accounts_worker.go`: per-link claims under a five-minute lease, no cycle
  lock, every call outside any transaction, at most three chained steps per claim)
  are written against the contract, not against identity.
- `staff.identity_links` is **provider-keyed** (`provider varchar(20)`, `identity`
  today; S reading 12: "the Entra module adds a value, no migration"), unique per
  `(person_id, provider)` and `(provider, external_id)`, with the lease, attempt and
  `last_spec_hash` columns the worker already drives.
- `staff.settings.create_before_days` (default 14) is live;
  `delete_external_after_days` (null = never, 1–3650) is stored, validated and
  **inert**: "read by the Entra module" (`account_settings.go`).
- The person carries the directory fields S7 promised a provider: work email, work and
  mobile phone, work country, preferred language on `people`; employee type,
  department, manager, cost centre and division on `employment_terms`; the
  department's own cost centre and division; the business unit under its legal
  entity. The job title is the terms' `title`; the office location the terms'
  `location_id`. **No groups table exists in Staff** (`CREATE TABLE staff.*` lists
  none).
- In **federated** mode the local path is link-only: `Prepare` finds a managed user by
  employee number and never creates, `Activate` re-enables only what Staff disabled,
  `Deactivate` always runs (a leaver loses access whatever the provider does), and a
  person without an account waits on attention `accountNotFound` "until the provider
  has created the account and an Owner has adopted it" (`admin/sso-scim.md`, "Staff in
  a federated installation"). The Entra module is that provider.
- S reading 30 names the migration case: pending, credential-less users created in
  local mode before federation was turned on are reported `mode_changed`, and "the
  Entra module's adoption into the provider is where they are resolved".
- The owner's answer 6 (plan, 2026-10-17): the authority mode "stays derived and
  recorded, **the Entra module revisits it in detail** (a setting beside the derived
  mode)".
- What identity keeps after Staff's erase (`admin/authentication.md`, "After Staff
  erases a person"): the managed user with name, address and employee number, the
  invitations, and the audit rows naming them; the Owner removes the user by hand;
  "an automatic removal is a later release's" — the roadmap assigns it here.

### 1.3 Patterns to reuse

- **A guarded outbound client.** `calendar/subscriptions_fetch.go:60-125` is the
  shape: a transport built once per process whose dialler goes through
  `netguard.DialContext` so every resolved address is checked and the checked address
  is the one dialled, no proxy, no keep-alives, no redirects, bounded dial, handshake
  and header timeouts, `Deps.HTTPTransport` as the test seam. Boundaries rule 1 notes
  that Brreg, OIDC and the AI client still use `http.DefaultTransport`; the Graph
  client is the second module client after the calendar's to take the guard, copied
  rather than imported "because no module imports another's code". Graph answers from
  two fixed hosts (`login.microsoftonline.com`, `graph.microsoft.com`), so the guard
  costs nothing and the fetch client's "no redirects" rule holds.
- **A sealed credential in a row of its own.** Invoices' `access_point_credentials`
  (`invoices/credentials.go`: provider and legal entity in clear, the key sealed under
  its own `secrets.Box` purpose, never answered, a *test* action answering `ok`,
  `unauthorized` or `unreachable`) and Invoices' Vipps settings; Communications'
  channel passwords; Staff's national id sealed and MAC'd. The box's purposes are
  caller-owned constants (`"invoices/access-point-credential"`), and the box refuses
  a value opened under another purpose.
- **A tagged test against the real thing.** `payments/vipps/vipps_test_env_test.go`:
  `//go:build vipps`, five `VIPPS_TEST_*` variables, skipped without them, a captured
  fixture pinned in every ordinary run. R §11 already names the Entra variables.
- **A worker under per-row claims.** Staff's accounts worker, not the retention
  worker's cycle lease (PR #124's pool starvation).
- **A settings row read at request time.** `BusinessCalendar` over `system_settings`
  (one row by primary key per request); the maintenance status's 20-second cache for
  something read on every request.
- **An Owner's setting, audited.** `PutIdentityOwnerWorkspace` (`workspace.go`):
  validated in Go then in Postgres before the transaction, the singleton row ensured
  and locked, an audit row with before and after, the same value answering 200 with
  nothing written.
- **A many-provider slot collected from enabled modules only.** `CalendarSources`
  (`module.go:150-162`): the provider is the gatekeeper (rule 12), the consumer calls
  through one accessor in its `contractscalls.go` that refuses a call inside a
  transaction.
- **A colleague mailed without their address crossing the seam.**
  `contracts.UserDirectory.Notify(ctx, userID, Notice)` (calendar design D9), the
  shape a pass handed to a manager can use.
- **Scaffolding.** Copy Calendar (the latest module): `srv/entra/module.go`,
  `openapi/entra.yaml`, the sqlc and oapi-codegen configs, the migrations numbered after `00057`, the
  highest today, the depguard rule set, `moduleSchemas` in
  `srv/db/schema_test.go:28`, `businessModules` in `cmd/vantigo/main.go:290`,
  `apps/entra/frontend` as `@vantigo/entra-ui`, the host tile in `apps.ts`, the
  reference page with `sources:`, the user and admin pages in both languages.

## 2. What Entra's own SSO and SCIM already give, and what the module adds

A tenant with the enterprise application configured gives Vantigo today, without the
module:

| Through | What arrives | What it does not do |
| --- | --- | --- |
| **OIDC sign-in** | a Vantigo user at the person's first sign-in, keyed on `(issuer, sub)`, with the `User` role; a disabled Entra user cannot sign in (the provider refuses the authorization) | nothing before the first sign-in; no employee number, no end of access until the next session check; no roles |
| **SCIM provisioning** (every 40 minutes; [how provisioning works](https://learn.microsoft.com/en-us/entra/identity/app-provisioning/how-provisioning-works)) | a Vantigo user for every Entra user in the app's scope, with `userName`, `externalId`, the name parts and `active`; `active=false` when the user is disabled, soft-deleted or scoped out, which ends their sessions (`upstream_active`); `DELETE` thirty days after Entra's hard delete; Entra groups in scope as access groups an Owner maps to roles | the enterprise extension, which Vantigo drops (§1.1); **a user disabled in Entra is never provisioned** ("Provisioning a user that is disabled in Microsoft Entra ID isn't supported"), so an account created disabled ahead of the start reaches Vantigo only once it is enabled |
| **Entra itself** | the user's existence, password, MFA, Conditional Access, licences, mailbox | any knowledge of who is employed from when to when, unless an HR system tells it |

What Entra does **not** have without a paid tier or a second system is the joiner,
mover and leaver: the account ahead of the start, the enabling on the day, the
attributes kept in step, the disabling with licences and groups stripped at the end,
the deletion later. Microsoft's own layer for that is **Lifecycle Workflows** — Entra
ID Governance or the Entra Suite, evaluated every three hours, triggered at offsets
from `employeeHireDate` and `employeeLeaveDateTime`, with built-in tasks for a Temporary
Access Pass mailed to the manager, enable, add to groups, disable, remove from all
groups and Teams, remove licences, revoke refresh tokens and delete
([templates](https://learn.microsoft.com/en-us/entra/id-governance/lifecycle-workflow-templates),
[tasks](https://learn.microsoft.com/en-us/entra/id-governance/lifecycle-workflow-tasks)).
Its inputs are the two dates, which **an HR system must write**.

So the module adds, in order of weight:

1. **Existence and status in the tenant, driven by the register**: the Entra user
   created disabled `createBeforeDays` ahead, enabled on the day, disabled the day
   after the end, deleted `deleteExternalAfterDays` later or never — Staff's decision
   table (S §4) applied to a second provider.
2. **The leaver's whole sequence** without Governance: disable, revoke sessions, strip
   direct licences, remove from the groups, and the two dates written so a tenant that
   does have Governance can run its own workflows on top.
3. **The mover's attributes**: title, department, manager, cost centre, division,
   company, office, phones, language, from the fields Staff already keeps (S7).
4. **The first credential**: a Temporary Access Pass created on the start day
   (§3.4), the passwordless pattern Microsoft recommends, since the random password
   the create requires is never shown to anyone.
5. **Groups** the company defines in Staff, listed or by rule, created and kept in
   Entra as security or Microsoft 365 groups, with a Team when asked and the licences
   their members get (§7.3).
6. **Correlation that needs no Owner**: the Vantigo user Entra provisions or the first
   sign-in creates is adopted as Staff's automatically because Staff announced it
   (§5.3), replacing the Owner's manual adoption for every person the module created.
7. **The enterprise extension inbound**, so an Entra tenant with the default mappings
   (`employeeId → employeeNumber`, `department`, `manager`) is no longer refused.
8. **The settings in the database**, one tile, one wizard (§6, §7.5).

What stays Entra's and the Owner's: who is an Owner in Vantigo; Conditional Access,
MFA and password policy; the mailbox and OneDrive (§8); Entra administrators, whom the
module never touches (§3.1).

## 3. Microsoft Graph: the user, licence and group lifecycle, and the permissions model

Every statement here is from Graph v1.0's reference unless a page is named; the
"higher privileged" permissions are listed where the least privileged one would make
the wizard's list longer without making it safer.

### 3.1 Users

**Create** — `POST /users` ([create user](https://learn.microsoft.com/en-us/graph/api/user-post-users?view=graph-rest-1.0)):
required `accountEnabled`, `displayName`, `mailNickname` (at most 64 ASCII characters),
`userPrincipalName` and `passwordProfile`. The UPN "cannot contain accent characters";
only `A–Z a–z 0–9 ' . - _ ! # ^ ~` are allowed before the `@`, and the domain must be
one the tenant has verified — so a Nordic name is folded (`Ø`→`O`, `æ`→`ae`) before it
becomes a UPN. The password is the caller's to supply, strong, with
`forceChangePasswordNextSignIn` recommended true; Graph generates none. A UPN already
in use answers 400 `ObjectConflict`. Least privileged application permission
`User.Create`; `User.ReadWrite.All` covers it.

**The properties the module writes** ([user resource](https://learn.microsoft.com/en-us/graph/api/resources/user?view=graph-rest-1.0)):
`employeeId` (**at most 16 characters**, filterable by `eq` without advanced-query
headers — [advanced queries](https://learn.microsoft.com/en-us/graph/aad-advanced-queries)),
`employeeHireDate` and `employeeLeaveDateTime` (UTC instants; writing the leave date
needs `User-LifeCycleInfo.ReadWrite.All`, R §10.18's "extra permission"),
`employeeType` (free text), `employeeOrgData` (`costCenter` and `division`, **both
written on every update** — omitting one nulls it), `jobTitle`, `department`,
`companyName`, `officeLocation`, `streetAddress`/`postalCode`/`city`/`country`,
`businessPhones`, `mobilePhone`, `preferredLanguage`, `usageLocation` (two-letter
country, **required before any licence**), and `manager` through
`PUT /users/{id}/manager/$ref` (`User.ReadWrite.All`). `id` is the opaque, immutable
key to store; the UPN and `mail` are mutable and reusable.

**UNCERTAIN — `employeeHireDate` under application permissions.** The update page says
`employeeHireDate` is among the properties that "cannot be updated by an app with only
application permissions", while `employeeLeaveDateTime` can be with its permission;
Microsoft's own lifecycle tutorial sets the hire date under a delegated User
Administrator sign-in. The users overview lists `hireDate` (a SharePoint property)
rather than `employeeHireDate` among the properties stored outside the directory, so
the note may conflate the two. The tagged test (§9) settles it on the first run; the
design writes it and treats a refusal as a reported fact, not a failure (spec).

**Update** — `PATCH /users/{id}` ([update user](https://learn.microsoft.com/en-us/graph/api/user-update?view=graph-rest-1.0)):
least privileged `User.ReadUpdate.All`; `accountEnabled` needs
`User.EnableDisableAccount.All` with `User.Read.All`, **or** `User.ReadWrite.All`,
which covers every property the module writes. Two walls stand behind the permission:

- **Sensitive actions need a directory role.** `accountEnabled`, `passwordProfile`,
  `userPrincipalName`, the phones, `otherMails`, delete and restore are "sensitive
  actions": "In app-only scenarios in addition to Microsoft Graph permissions, the app
  must be assigned a higher privileged administrator role"
  ([who can perform sensitive actions](https://learn.microsoft.com/en-us/graph/api/resources/users?view=graph-rest-1.0)).
  **User Administrator** on the service principal lets the app act on users holding
  no role or a low one; a user holding Global Administrator, Privileged Role
  Administrator, a membership of a role-assignable group or a place in a restricted
  management administrative unit is beyond it (and beyond `User.ReadWrite.All` for
  deletion too). The wizard therefore has a step that no comparable product documents
  (§4): assign the User Administrator role to the app, and the module reports an
  Entra administrator as `account_privileged_in_entra` instead of trying.
- **Users synced from on-premises Active Directory cannot be written in the cloud**:
  `onPremisesSyncEnabled = true` users answer 400 `Request_BadRequest` "Unable to
  update the specified properties for on-premises mastered Directory Sync objects"
  ([breaking change, 2020](https://devblogs.microsoft.com/microsoft365dev/breaking-change-to-microsoft-graph-users-api-updates-to-on-premises-sync-enabled-user-contact-numbers-are-no-longer-allowed/);
  **UNCERTAIN** which exact properties are blocked beyond the contact numbers). A
  hybrid tenant is out of the first version: the module reads the flag and reports
  `account_synced_on_premises`.

**Sessions** — `POST /users/{id}/revokeSignInSessions`
([revoke](https://learn.microsoft.com/en-us/graph/api/user-revokesigninsessions?view=graph-rest-1.0)),
`User.RevokeSessions.All`: resets `signInSessionsValidFromDateTime`, which invalidates
every refresh token and browser session cookie, "with a small delay of a few
minutes". **Access tokens are not revoked**: they live their lifetime, "a random value
ranging between 60–90 minutes", unless the client supports continuous access
evaluation, which revokes "in near real time in response to critical events such as
account disablement". Disabling first, then revoking, is the order that ends access
soonest.

**Delete** — `DELETE /users/{id}` (`User.ReadWrite.All`) is a **soft delete**: the user
sits in deleted items for 30 days and can be restored (`POST
/directory/deletedItems/{id}/restore`, `User.DeleteRestore.All`), after which
everything — mailbox and OneDrive included — is gone
([delete](https://learn.microsoft.com/en-us/graph/api/user-delete?view=graph-rest-1.0)).
Microsoft's own leaver guidance keeps the account as the **anchor of a shared
mailbox** and warns that deletion loses the mailbox after the 30 days
([remove a former employee](https://learn.microsoft.com/en-us/microsoft-365/admin/add-users/remove-former-employee)).

### 3.2 Licences

`POST /users/{id}/assignLicense` with `addLicenses[{skuId, disabledPlans}]` and
`removeLicenses[skuId]` ([assignLicense](https://learn.microsoft.com/en-us/graph/api/user-assignlicense?view=graph-rest-1.0)),
`LicenseAssignment.ReadWrite.All`; `GET /subscribedSkus` (`LicenseAssignment.Read.All`
or `Organization.Read.All`) lists each SKU's `prepaidUnits.enabled` and
`consumedUnits`, with no `$filter`. `usageLocation` must be set first — direct
assignment fails without it, where group-based licensing silently takes the tenant's
location. The failures a user's `licenseAssignmentStates[].error` names:
`CountViolation` (no seats), `MutuallyExclusiveViolation`, `DependencyViolation`,
`ProhibitedInUsageLocationViolation`, `UniquenessViolation`. A licence assigned
through a group reads `assignedByGroup` set; one assigned directly reads it null.
**Group-based licensing needs Entra ID P1** for every member who benefits (Microsoft
365 Business Premium includes P1); it is configured in the admin centre, not by the
module, and the module **strips only direct assignments** at the end, as Lifecycle
Workflows' "remove all licenses" does. The first version assigns licences directly
from a group's trait (§7.3) and removes them the same way; an installation on P1 may
use group-based licensing on the groups the module creates instead, and the leaver
loses those with the group membership.

### 3.3 Groups and Teams

`POST /groups` ([create group](https://learn.microsoft.com/en-us/graph/api/group-post-groups?view=graph-rest-1.0)),
least privileged `Group.Create`, `Group.ReadWrite.All` for the rest: `displayName`
(≤ 256), `mailEnabled`, `mailNickname` (≤ 64 ASCII), `securityEnabled`; a **security
group** is `groupTypes: []`, `mailEnabled: false`, `securityEnabled: true`; a
**Microsoft 365 group** is `groupTypes: ["Unified"]`, `mailEnabled: true`,
`securityEnabled: false`, with `visibility`. The fact that bites: "Creating a group
using the `Group.Create` application permission without specifying owners creates the
group anonymously and the group isn't modifiable"; a Microsoft 365 group created
app-only without owners may get no SharePoint site. **The app is not made an owner
automatically** and "can assign itself as the group's owner or member" — so every
group the module creates names **the service principal as an owner** in the create
request, plus any person owners (§7.3), at most 20 relationships at creation.

Members: `POST /groups/{id}/members/$ref`, `DELETE /groups/{id}/members/{id}/$ref`
(`GroupMember.ReadWrite.All`, covered by `Group.ReadWrite.All`), or up to 20 in one
`PATCH` with `members@odata.bind`, all-or-nothing. A freshly created group may answer
400 "the source resource object … doesn't exist" for a few seconds; retry. The
documented trap: a `DELETE` of a member **without `/$ref`** deletes the user object
when the app also holds `User.ReadWrite.All`. **Dynamic membership** needs P1 per
member and refuses manual changes; R §10.14 excluded it and the module reads
`membershipRuleProcessingState` to refuse such a group rather than fight it.
**Role-assignable groups** (`isAssignableToRole`) need Privileged Role Administrator
and `RoleManagement.ReadWrite.Directory`; the module never creates one and refuses
to manage one it finds.

**Teams**: `PUT /groups/{id}/team` ([create team from group](https://learn.microsoft.com/en-us/graph/api/team-put-teams?view=graph-rest-1.0))
under `Group.ReadWrite.All` (not `Team.Create`, which is `POST /teams`'); "the group
must have at least one owner"; a group **less than 15 minutes old** may answer 404,
Microsoft's advice being three retries ten seconds apart and then fifteen minutes.
Microsoft recommends "two or more owners … real user accounts and not service
accounts" for a Team, and the group-to-Teams membership sync "typically takes 24
hours". The worker's hourly cycle makes the fifteen minutes a non-event: the group is
created in one cycle and teamified in the next. Teams throttle at 6 `PUT /team`
requests per second per app per tenant and 4 member additions a minute per team.

### 3.4 The first credential: a Temporary Access Pass

`POST /users/{id}/authentication/temporaryAccessPassMethods`
([create TAP](https://learn.microsoft.com/en-us/graph/api/authentication-post-temporaryaccesspassmethods?view=graph-rest-1.0))
**is supported with application permissions** — the table lists `UserAuthMethod-TAP.ReadWrite.All`
and `UserAuthenticationMethod.ReadWrite.All` (the least-privileged column oddly names
a `.Read.All`; **UNCERTAIN**, the tagged test tells). `lifetimeInMinutes` 10–43,200,
`isUsableOnce`; one pass per user, a new one replacing the old; none for a guest. The
pass works only once the tenant's **authentication methods policy** enables TAP for
the user or a group they are in ("Although you can create a TAP for any user, only
users included in the policy can sign in with it" —
[TAP](https://learn.microsoft.com/en-us/entra/identity/authentication/howto-authentication-temporary-access-pass));
the policy's defaults are one hour, a maximum of eight, eight characters. Lifecycle
Workflows' pre-hire template creates the pass seven days before the hire date and
mails it to the manager, with the hire date as its start; Microsoft's sync guidance
suggests a 24-hour lifetime for that pattern.

The alternatives are weaker: the create's random password shown to someone is a
password in a mail or on a screen; self-service password reset needs the person to
have registered a method, which a new hire has not. The pass is the right first
credential, and whom it is shown to is a design decision (§10).

### 3.5 Throttling, batching, tokens, credentials

- **Limits** ([throttling limits](https://learn.microsoft.com/en-us/graph/throttling-limits)):
  per app per tenant 3,500–8,000 resource units per 10 s by tenant size, 3,000 writes
  per 2 min 30 s, 130,000 requests per 10 s per app globally. A company of a few hundred
  people moves a handful of accounts a day; a first synchronisation of fifty groups
  with their members is the only burst, and it fits in one cycle. 429 carries
  `Retry-After`, which the client honours; without it, exponential backoff.
- **`$batch`** ([JSON batching](https://learn.microsoft.com/en-us/graph/json-batching)):
  20 requests, each throttled on its own (a 429 inside a 200), `dependsOn` for order.
  Member reconciliation is the one place it pays.
- **Tokens** ([client credentials](https://learn.microsoft.com/en-us/entra/identity-platform/v2-oauth2-client-creds-grant-flow)):
  `POST https://login.microsoftonline.com/{tenant}/oauth2/v2.0/token`, scope
  `https://graph.microsoft.com/.default`, `expires_in` about an hour, no refresh token;
  the token's `roles` claim lists the application permissions consented, which is how
  the wizard's connection test reports what is still missing. Admin consent is the
  tenant's `/adminconsent` page or the portal's button.
- **Credentials** ([add credentials](https://learn.microsoft.com/en-us/entra/identity-platform/how-to-add-credentials)):
  a client secret lives at most 24 months, Microsoft recommends under 12 and says
  secrets "should not be used in production environments" — certificates preferred.
  R §10.15 chose secrets first, in the plural; a certificate (a JWT assertion the
  server signs with a key it holds) is the natural second kind, and **workload
  identity federation** is not for a self-hosted server: it needs an OpenID issuer
  with a reachable JWKS that can prove the workload, which Kubernetes clusters have and
  a plain server does not. The same app registration can hold the sign-in's web
  redirect and the Graph's application permissions, so one registration serves both.
- **Scoping**: application permissions are tenant-wide; restricted management
  administrative units ignore them entirely. The first version is tenant-wide and says
  so.

### 3.6 The permissions the wizard asks for

| Permission (application) | For | Could be narrower |
| --- | --- | --- |
| `User.ReadWrite.All` | create, read by employee id, update, `accountEnabled`, manager, delete | `User.Create` + `User.ReadUpdate.All` + `User.EnableDisableAccount.All` + `User.Read.All`, but the manager and the delete need the wide one anyway |
| `User-LifeCycleInfo.ReadWrite.All` | `employeeLeaveDateTime` (R §10.18) | — |
| `User.RevokeSessions.All` | the leaver's sessions | — |
| `UserAuthMethod-TAP.ReadWrite.All` | the pass | — |
| `LicenseAssignment.ReadWrite.All` | assign and strip licences; `subscribedSkus` | — |
| `Group.ReadWrite.All` | groups, members, owners, the Team | `Group.Create` + `GroupMember.ReadWrite.All` lose the Team |
| `Domain.Read.All` | the verified domains in the wizard | — |
| `Organization.Read.All` | the connection test (`/organization`) | — |
| **the User Administrator directory role** on the service principal | the sensitive actions (§3.1) | — |

Lifecycle Workflows and API-driven inbound provisioning (`/bulkUpload`, 50 operations,
`SynchronizationData-User.Upload`, P1 or P2, 2,000 calls a day, **never deletes**) are
Microsoft's alternatives for the push itself. Direct Graph is chosen because the
bulkUpload path needs P1, puts a 40-minute cycle and Entra's own attribute mappings
between Staff's decision and the account, cannot delete, cannot add to groups or set a
pass, and leaves the UPN's uniqueness unhandled ("the user create operation fails";
[FAQ](https://learn.microsoft.com/en-us/entra/identity/app-provisioning/inbound-provisioning-api-faqs)).
The module's reconciliation is the same shape as bulkUpload's — send the whole record,
let the receiver diff — done by the module against Graph's current state.

### 3.7 What Entra's provisioning service and sign-in send back

- **SCIM's default mappings** for a custom app
  ([attribute mappings](https://learn.microsoft.com/en-us/entra/identity/app-provisioning/customize-application-attributes)):
  `userPrincipalName → userName`, **`mailNickname → externalId`** for users (`objectId`
  for groups), `employeeId → …enterprise:2.0:User:employeeNumber`, `department`,
  `manager`, `IsSoftDeleted → active`; matching on one attribute at a time in a
  declared order, the target needing to filter on it. So an Entra user's `externalId`
  in Vantigo is the mail nickname unless the Owner changes the mapping — the
  correlation in §5.3 must not depend on it.
- **The id token** ([claims](https://learn.microsoft.com/en-us/entra/identity-platform/id-token-claims-reference)):
  `oid` is "an immutable identifier … Microsoft Graph returns this ID as the `id`
  property"; `sub` is pairwise per application; `email` is mutable and may be absent
  for a member user; `upn` is "not a durable identifier". **There is no `employeeid`
  optional claim** ([optional claims](https://learn.microsoft.com/en-us/entra/identity-platform/optional-claims-reference));
  emitting the employee id needs a claims-mapping policy, which is a tenant-side step
  the wizard would have to explain. The `groups` claim overflows at 200 groups in a
  JWT. Identity already requires `oid` and reads it (`oidc_claims.go`), which is all
  §5.3 needs.

## 4. How comparable products do joiner, mover and leaver

The eleven looked at fall into three kinds. Vendor pages are named; **UNCERTAIN**
marks what a page did not say.

**HR systems pushing through Graph with the customer's own app registration.**
*Huma* ([Entra ID integration](https://humahr.com/knowledge/entra-id-integration)):
`User.ReadWrite.All` and a client secret the customer recreates when it expires (or a
delegated sign-in that stops when that user loses User Administrator); correlation on
the **email**, which "must always be identical in both systems"; `employeeId` written
once and never updated; mail, names, mobile, address, hire date and job title kept in
step; a user created when the person is added if the option is on, which fails for a
private address; a deactivated person is "not deactivated in Entra, but locked for
updates"; no groups, licences or sessions. *Simployer One*
([Entra ID user provisioning](https://mysupport.simployer.com/en/support/solutions/articles/201000131890-entra-id-azure-ad-user-provisioning)):
delegated consent to Simployer's app, the work-email domain must be the tenant's,
`accountEnabled` set true at creation only and never changed, the leaver handled by
writing `employeeLeaveDateTime` as **the end of that day** (`23:59:59`) and leaving the
disabling to Lifecycle Workflows. Simployer's older HRM connector
([Azure AD integration](https://mysupport.simployer.com/en/support/solutions/articles/201000113112-azure-ad-integration))
is the closest to the roadmap: `employeeId` from the ansattnummer, the user created
**ten days before the start**, a generated password never used "since an invitation
will be sent", deactivation "at 23:59 the end date", a security group per company,
temporary employees without IT access excluded, email and UPN mastered by Entra and
imported back. *Personio* (support pages reachable only as search excerpts): one-way
push by a Personio-hosted app needing Privileged Role Administrator to connect,
correlation on email = UPN and then the Employee ID written to the profile, disable on
Inactive, never delete, no way to turn creation off.

**HR systems pushing through Entra's own inbound provisioning.** *Rippling* and
*HiBob* ([Rippling tutorial](https://learn.microsoft.com/en-us/entra/identity/saas-apps/rippling-hcm-microsoft-entra-id-integration-tutorial),
[HiBob tutorial](https://learn.microsoft.com/en-us/entra/identity/saas-apps/hibob-to-active-directory-user-provisioning-tutorial))
consent to an app that creates an API-driven provisioning job in the tenant
(`SynchronizationData-User.Upload.OwnedBy`, `ProvisioningLog.Read.All`), send SCIM bulk
payloads, and read the provisioning logs for status; both must map the start and
termination dates for Lifecycle Workflows to do the rest; HiBob offers an
approval-required mode for movers; Rippling's policy decides "who gets an account and
when access starts" and disables "on their last day". *BambooHR* has no Microsoft
connector and goes through a third party.

**Microsoft's own HR connectors**, which define the pattern
([plan cloud HR provisioning](https://learn.microsoft.com/en-us/entra/identity/app-provisioning/plan-cloud-hr-provision),
[Workday reference](https://learn.microsoft.com/en-us/entra/identity/app-provisioning/workday-integration-reference)):
matching on `employeeId`, "written on create only", which must be unique and
pre-populated; a rehire with the old id **re-enables the existing account**, with a
new id gets a new one; an employee-to-contractor conversion creates a new account and
disables the old on the effective date; a future hire is created as soon as it is
visible and `Active` is read as of the hire date, so delaying the enabling needs an
expression; termination is read from the start of the day in UTC and disabled "in the
provisioning cycle that runs after end of day" — with a documented expression to pick
a local hour because the connector runs in Pacific time; `mail` cannot be set by
provisioning ("managed by Exchange Online") and `extensionAttribute1–15` cannot match.
Microsoft's sketch of the full flow: X days before, mail the manager, add to groups,
generate a pass; on the last day, remove groups and licences; X days after, delete.
Its guidance on the two dates: a hire date **at the start of the day** (01:00–05:00)
and a leave date **at the end** (21:00–23:00) in the tenant's own terms
([sync attributes](https://learn.microsoft.com/en-us/entra/id-governance/how-to-lifecycle-workflow-sync-attributes)).

**Identity platforms as the HR consumer.** *Okta*
([Workday provisioning](https://help.okta.com/en-us/content/topics/provisioning/workday/workday-provisioning.htm),
[Office 365 deprovisioning](https://help.okta.com/en-us/content/topics/apps/office365/references/deprovision-options.htm)):
a pre-start interval in days that imports **and activates**; "deactivation occurs the
day after" the last day, time-zone-aware by the worker's zone; rehire re-links and
reactivates; Office 365 licences chosen per assignment; deprovisioning as a ladder —
block sign-in; block and remove licences, at once or after a grace period Okta
recommends be three days; block, remove and delete — with **no mailbox conversion**.
*Google Workspace* as a target (Personio, HiBob): suspend keeps data and keeps billing;
delete transfers Drive and Calendar at deletion and is restorable twenty days.

**What every one of them settles the same way**, and this design keeps: one direction,
HR to the directory; the employee number as the key, written once; disable, never
delete, by default; the directory owns the sign-in and the mailbox. **What they leave
to Microsoft** — the pass, the groups, the licences, the deletion — is what Lifecycle
Workflows sell, and what a Nordic SMB on Business Premium does not have. **What none
of them has** is correlation of the Vantigo-side user back to the person without an
administrator (§5.3), and groups defined in HR terms with the tenant's shape as a
trait (§7.3). Simployer's legacy connector — ten days before, 23:59, a group per
company, invitation instead of password — is the nearest ancestor of what Staff
phase 1 already does locally.

## 5. Correlation on the employee number

Three objects stand for one person: Staff's person (`employee_number`, at most 16
characters, S3), the Entra user (`employeeId`, at most 16 — the same bound, chosen for
it), and identity's user (`employee_number` with its unique `employee_number_key`,
S24). The employee number is the key of all three, and the object id is the join in
the middle.

### 5.1 Person → Entra user

`GET /users?$filter=employeeId eq '<number>'` finds the Entra account on the first
`Prepare`; found, it is linked (`identity_links.provider = 'entra'`, `external_id` the
object id) and brought to the state the table wants — the Workday rule, a rehire with
the old number re-enables the old account; not found, it is created with the number.
Entra does not enforce `employeeId`'s uniqueness; two users with the number is a
refusal (`employee_number_ambiguous`) for an Owner to resolve in the portal. The
number is **written on create and never changed by a mover** (Microsoft's "written on
create only"); a renumbered person is the `accountMismatch` case Staff already reports.
The UPN is the person's **work email** when its domain is one the tenant has verified
(Simployer's rule), lower-cased and folded to the UPN's alphabet; a person without a
work email gets the wizard's pattern (`given.family@domain`, folded, a numeric suffix on
conflict) and Staff records the address the provider answered as the work email (an
event), so the register and the directory agree from the first day.

### 5.2 Entra user → identity user, today

OIDC's `oid` claim is the Entra object id, and SCIM's `externalId` is whatever the
Owner mapped — the mail nickname by default (§3.7). Today identity knows none of this
and the Owner adopts by hand.

### 5.3 Entra user → identity user, with the module: the announced account

Staff knows, the moment the module answers `Prepare`, that a Vantigo user will arrive
for this person — by SCIM within a cycle of the enabling, or at the first sign-in —
and knows the object id, the UPN and the employee number it will carry. The design
proposed: Staff **announces** it to identity through a new `AccountProvisioning` method,
`Expect(ctx, actor, ExpectedAccount{EmployeeNumber, DisplayName, PreferredLanguage,
Issuer, Subject?, ObjectID, UserPrincipalName})`, and identity keeps
`identity.expected_accounts` keyed by employee number. Then:

- **SCIM `POST /Users`** matching an expected account — by the enterprise extension's
  `employeeNumber`, else by `userName` equal to the expected UPN, else by `externalId`
  equal to the object id — creates the user **managed by Staff** with the employee
  number, not pending (the credential is the provider's), and consumes the
  announcement; the accounts worker's next `Prepare` for the identity link finds it
  by number, as it does for an adopted user today.
- **OIDC's first sign-in** with `oid` equal to an expected object id provisions the
  user the same way, managed and numbered, instead of as a plain `User`; the email
  claim's collision rule stands.
- An arrival that matches nothing is what it is today: a plain SCIM or OIDC user, for
  the Owner to adopt if they are Staff's.
- An announcement nobody fulfils within a bound (the Owner's SCIM scope does not
  include the user; the person never signs in) is an attention item, not an error.

This is what "correlated on the employee number" means end to end, and why the
enterprise extension is a prerequisite rather than a nicety: Entra's default mapping
sends `employeeId` as `employeeNumber`, and the module wrote `employeeId`. Reading 30's
pending local users are handled by the same table: a person whose kind HR changes
from `local` to `entra` (§7.2) has their pending, credential-less identity user
**replaced** — the announcement names the existing user id, and the SCIM create or
first sign-in completes that user (clears `pending_since`, sets the link) rather than
inserting a second, which is `PostIdentityInvitationsAccept`'s "complete the named
user" shape (S25) applied to a provider's arrival. The address collision S23 feared
cannot arise, because the user being completed is the one holding the address.

### 5.4 What is never a key

The email: mutable on both sides, absent from a member's id token at times, and
refused as a match by S24 for the reason B3 recorded (HR could redirect an account).
The UPN: reusable. The SCIM `externalId`: an Owner's mapping choice. The object id is
a key only between Entra and identity, never between Staff and anything — Staff keeps
it on the link because the module answered it, and compares numbers.

## 6. Moving OIDC and SCIM from the environment into a settings table

R §10.17 decided the move and its split: identity owns an SSO-and-SCIM settings table
and the endpoints that edit it, read at request time through a short cache the way
maintenance mode is; the Entra module owns the Graph connection; the host renders both
on one tile. The brief adds the constraint the decision did not have to meet then:
**a running installation must not break**. §1.1 lists the whole surface the move
touches; what follows is how each piece moves.

### 6.1 The table, and what is a secret in it

One singleton row, `identity.sso_settings`, beside `system_settings` rather than on it
(the row is read on every request by the session query's `scim_enabled`, and a wide
row of sealed values should not ride along with the maintenance flag and the time
zone): the OIDC block (`enabled`, `provider`, `authority`, `client_id`, the client
secret **sealed** under a purpose of its own, `workload_token_file`, `allowed_domains`,
`display_name`) and the SCIM block (`enabled`, the **SHA-256 of the token**, the
previous token's digest and its `expires_at`), with `version`, `updated_at` and
`updated_by_user_id`.

Two things are different from the environment, deliberately:

- **The SCIM token is never stored, only its digest.** The bearer check compares
  digests already (`scim.go:166-175`); the environment held the clear token only
  because it had to be typed somewhere. In the table, a token is **generated** by the
  server (32 random bytes, base64url, the feed token's shape), shown **once** to the
  Owner to paste into Entra's provisioning page, and its digest kept. A lost token is
  replaced, not recovered. Rotation keeps the 24-hour overlap: *New token* writes the
  old digest into the previous slot with an expiry the Owner picks within 24 h, and
  the status shows which of the two the last authenticated request used.
- **The client secret is sealed by the secrets box** (`identity/oidc-client-secret`),
  as Invoices seals its access-point key and Communications its channel passwords,
  and is never answered; the Owner sees "set on <date>" and *Replace*. The
  workload-identity file path stays a path: it names a file the platform projects,
  not a secret.

### 6.2 Reading it: a short cache, and a relying party rebuilt on change

`identity.Access` holds `cfg *config.Config` today. It gains a reader over the row with
the maintenance cache's shape (`system.go:28-62`: fetched at most every 20 s per
process, evicted on write in the writing process). The six `ScimEnabled` parameters,
the bearer check, the status endpoint and the mode derivation read the cached row
instead of `cfg.SCIM` and `cfg.OIDC`; the per-request cost is a map read, and a change
an Owner saves is in effect on every replica within 20 seconds, with no restart.

The relying party is the one thing built once (`server.go:63`): the normalised issuer
keys `oidc_links`, the discovery is cached under a mutex, and the OAuth2 config carries
the client id and secret. It becomes a **versioned** value: the server answers the
party built for the row's `version` it last saw and rebuilds when the version differs,
under the same mutex; discovery is still fetched lazily, so a provider that is down at
the time of the save fails its first sign-in, never the save. The callback and the
completion path do not change. A change of `authority` changes the issuer the links
are keyed on, which is a change of provider: the Owner's page says so, and the save
refuses it while `oidc_links` has rows under the old issuer unless the Owner confirms
(`oidc_links_exist`, the same shape as Invoices' `transmissions_active`).

### 6.3 Not breaking a running installation

Nothing is in production yet (R §10.17), but the repository's own rule is that an
upgrade is a migration and a restart, never a procedure an operator must get right in
the middle. The move is therefore three releases of behaviour in one design, each
release explicit:

1. **This release imports.** The migration creates the row empty. At identity's
   mount — where the authority mode is recorded today — an API process that finds the
   row **never written** (`version = 0`) and `OIDC_*` or `SCIM_*` set **imports** them
   into the row in one transaction (the secret sealed, the token digested, the previous
   token and its expiry carried over, `updated_by_user_id` null), audits
   `sso-settings.imported` naming the variables (never their values), and logs a
   warning that the variables are now ignored and should be removed. A process that
   finds the row written ignores the environment entirely: **the row wins**, and a
   variable still set is logged once per start as a leftover, by name. Workers never
   import (they do not mount identity) and read the row like everyone else. Two API
   replicas starting at once import once: the row is locked, and the second finds it
   written.
2. **The next release refuses.** `OIDC_*` and `SCIM_*` set at all fail startup with
   the leftover sentence `config.go` already has for a half-removed provider, pointing
   at the Owner's page. The `config` loader's OIDC and SCIM functions go, with
   `OIDCConfig` and `SCIMConfig`; `admin/sso-scim.md`'s environment section becomes a
   history note.
3. **Never:** a release that reads both and lets them disagree.

The authority mode's recording (`authority_mode.go`) exists because a worker process
could be configured differently from the API process (pre-flight I6). With the row the
source for both, that cannot happen: `deriveAuthorityMode` reads the row, the recorded
value becomes the audit trail of changes rather than a guard, and
`contracts.ErrModeMismatch` has no sender. The design retires it (the sentinel stays
one release for Staff's `accountModeMismatch` item to drain, then goes).

### 6.4 The Owner's page, and the one tile

Identity's endpoints are the Owner's, under `OwnerManagement` as the workspace's are:
`GET /owner/sso` (the row with the secret and the digests withheld, the callback URL,
the SCIM endpoint URL, the last sign-in and the last SCIM request from the status
events), `PUT /owner/sso/oidc`, `DELETE /owner/sso/oidc`, `POST /owner/sso/scim/token`
(generates, answers the token once), `POST /owner/sso/scim/token/rotate`,
`DELETE /owner/sso/scim`. Every write is audited with before and after, the secret and
the digests never in either. Validation is `config.go`'s, moved: the authority's exact
form per provider, the client id's, the domains', the mutual exclusion of the secret
and the token file, the file's readability checked by the **API process that
saves** — with a sentence that every replica must see the same path.

The host renders these on the Entra tile's **Sign-in** and **Provisioning** steps (the
module's wizard, §7.5) and, for an installation that uses Google or no module, on a
plain **Single sign-on** card under Workspace administration. The Entra tile shows one
place; the backend keeps identity's table identity's and the module's connection the
module's.

## 7. The `IdentityProvisioner` slot, and the group-and-traits model

### 7.1 The shape the slot must have

The roadmap's sentence fixes the direction: the Entra module "fills an
`IdentityProvisioner` slot Staff consumes, so identity stays the owner of local
accounts only". Three facts from §1 shape the contract:

- Staff's worker and its decision table already exist and are written against a
  contract; the Entra provider is a second contract the same worker drives, keyed by
  the link's `provider`. The worker's discipline — a claim per link, no connection held
  across a call, a bounded chain of steps, the result written under the person's lock —
  is exactly what a Graph call needs, and a second worker in the Entra module would
  copy it and then need Staff's facts, which it cannot read.
- The provider cannot read Staff's tables and Staff cannot read the provider's, so
  everything the provider needs about the person travels in the call (the spec), and
  everything Staff needs to remember about the external account travels back (the
  object id, the UPN, the state), kept on Staff's link. The provider keeps its own
  rows for what Staff has no business knowing: the Graph object ids of groups, the
  credentials, the throttling state, the attempts.
- A many-provider slot collected from **enabled modules only**, like `CalendarSources`,
  because a disabled module's permissions are not in the catalog and its tables may be
  empty; Staff reads the slot through one accessor in `contractscalls.go` that refuses
  a call inside a transaction (rule 12's discipline, applied to a write).

A provider that is enabled but **not connected** (no credentials yet) must say so
rather than fail: `Status` answers `unconfigured`, `connected` or `failing`, and Staff's
decision table treats an unconfigured provider as "nothing to do" with an attention
item, not as an error to retry.

### 7.2 Who is the authority, per person

S23 derived one authority per installation from identity's configuration and the owner
asked the Entra module to revisit it (plan answer 6). The federated mode's reason was
a collision: a locally invited pending user and an SSO sign-in with the same address
lock each other out. The collision is per person, and so is the right unit of the
decision. The research proposes a **per-person account kind** on the flag, replacing
the installation-wide mode in Staff's decision table:

| Kind | Who creates the Vantigo user | Who creates the external account | Today's name |
| --- | --- | --- | --- |
| `local` | identity, invited by Staff | nobody | local mode |
| `external` | the identity provider, by SCIM or first sign-in; Staff links by employee number after an Owner's adoption | the tenant's own administrators | federated mode |
| `entra` | the identity provider, by SCIM or first sign-in; identity adopts automatically because Staff announced the account (§5.3) | the Entra module, from Staff's record | the roadmap's paragraph |

An installation's **default kind** for a new person is a Staff setting (`entra` once
the module is connected, else what the recorded mode says today), and HR changes it on
a person: the shift worker without a Microsoft licence gets `local` in an
Entra-connected company, which is what S open question 3 wanted a setting for. The
derived mode keeps one job, which is to seed that default on upgrade; `Mode()` stays on
the identity contract for the `external` kind's link-only rule and for nothing else.

### 7.3 The groups, and what a trait is

R §10.14 decided the model: Staff owns the group and its members, listed or derived
from a rule; a provider declares the traits a group can carry for it; Staff stores the
trait values and renders them; the provider creates and maintains the external group
and its membership; membership is explicit, never Entra's dynamic membership. The
research adds what the code and §3.3 need of it.

**Staff's side.** `staff.groups` (`key`, `name`, `description`, `kind` `listed` or
`derived`, `rule` for a derived one, `archived_at`) and `staff.group_members`
(`group_id`, `person_id`, `source` `listed` or `derived`, `since`). A **rule** is one
predicate over the register, evaluated on the business day: everyone with an
employment in force today, everyone whose terms today name a department (or one
beneath it), a business unit, a location or an employee type, or everyone a given
person manages. Membership is **materialised** daily by the accounts worker's cycle —
the same cycle that moves accounts, so a person's group memberships change the day
their terms do — and a change is an event on the person. A provider is handed the
members as **external ids** Staff already holds on the person's link for that
provider; a member with no account on the provider yet is simply not in the external
group until they have one, and Staff's UI says "3 of 12 members have a Microsoft
account". A group's **owners** are people too, named on the group, handed over the
same way; the provider adds its own service principal as an owner always (§3.3).

**Traits.** A trait is a provider's typed, labelled, documented field on a group —
`contracts.GroupTrait{Key, Type (bool | string | choice | list), Label{en, nb},
Description{en, nb}, Choices, DocURL, Default, Required}` — answered by the provider's
`GroupTraits()`, stored by Staff as `staff.group_traits (group_id, provider, values
jsonb)` and rendered by Staff's group page as a section per connected provider without
Staff's UI knowing a single trait by name. For Entra the first set is: `type`
(`security` or `microsoft365`), `mailNickname` (a Microsoft 365 group's), `team`
(create a Team on the group — needs a person owner, §3.3), `visibility` (`private` or
`public`, Microsoft 365 only), `licences` (SKU part numbers the members are assigned
directly when activated and stripped when they leave; the wizard lists the tenant's
SKUs from `subscribedSkus`) and `description` (defaulting to Staff's). The provider
validates the values on `EnsureGroup` and answers field errors Staff shows beside the
trait.

**The provider's side.** `entra.groups (staff_group_id opaque, object_id, type, team,
state, last_members_hash, last_synced_at, last_error, attempts, next_attempt_at)`; the
external group is created on the first `EnsureGroup` with the service principal and
the person owners in the request; members are reconciled by `SetMembers(groupID,
externalIDs)` — the provider reads the group's current members from Graph, adds the
missing and removes the extra in `$batch` requests of twenty. The group is **Staff's**,
so the provider reconciles to Staff's list exactly, and the wizard says that a group
Staff manages is Staff's in Entra too; a group the module finds with dynamic
membership or `isAssignableToRole` is refused, never reconciled. A group Staff
archives is deleted in Entra after `delete_external_after_days` or kept — the owner's
question (§10).

**The loop worth naming.** A group the module creates can be assigned to the enterprise
application's provisioning scope by the Owner; Entra's SCIM then pushes it to Vantigo
as an access group of source `scim`, and the Owner maps roles to it (§1.1). That is
"Staff group membership grants Vantigo roles" with no new code: Staff → Entra → SCIM →
identity. The module does not short-cut it, because the role mapping must stay the
Owner's (S26's reason).

### 7.4 What the Entra provider needs from the person

The spec a call carries is the explicit, visible mapping R §10.19 asked for, as a
fixed table shown on the person's **Directory profile** section with a per-installation
toggle per field. Every field is already a column in Staff (S7; §1.2), so the Entra
phase adds the mapping and a provider, not columns:

| Staff field | Entra attribute | Default |
| --- | --- | --- |
| given name, family name, display name | `givenName`, `surname`, `displayName` | on, not switchable |
| employee number | `employeeId` | on, not switchable; written on create only |
| the earliest open employment's start | `employeeHireDate` (the start of that day in the business zone — Microsoft's 01:00–05:00 advice) | on; **UNCERTAIN** whether app-only may write it (§3.1) |
| the last employment's end | `employeeLeaveDateTime` (the end of that day in the business zone, Microsoft's 21:00–23:00 advice; Simployer's 23:59:59) | on |
| work email | the UPN and `mail` (§5.1; `mail` is Exchange's once a mailbox exists) | on, not switchable |
| employee type (the a-melding form) | `employeeType` | on |
| job title (the terms') | `jobTitle` | on |
| department (the terms', else none) | `department` | on |
| business unit (the legal entity's name) | `companyName` | on |
| cost centre, division (the terms', else the department's) | `employeeOrgData.costCenter`, `employeeOrgData.division`, both always | on |
| office location (the terms' location name) | `officeLocation` | on |
| work address (the location's) | `streetAddress`, `postalCode`, `city`, `country` | off |
| work phone, mobile | `businessPhones[0]`, `mobilePhone` | off |
| work country | `usageLocation` (needed before any licence) | on, not switchable |
| preferred language | `preferredLanguage` (`nb-NO`, `en-GB`) | on |
| manager (the terms', else the department's) | `manager` by the manager's Entra object id, when the manager has an Entra link | on |

Private contact details are never mapped. The spec is hashed as Staff hashes it today
(`last_spec_hash`), so a mover is an `Update` on the day the terms change and nothing on
other days.

### 7.5 The wizard, and who owns which step

The tile is the module's; the wizard's steps write to two owners. Each step carries
the portal path and what to copy, the way R §10.5 asked ("deliberately simple and
carries its instructions in the UI"):

1. **Register** — "Entra admin centre › App registrations › New": the name, *Accounts
   in this organizational directory only*, no redirect yet. Copy the **tenant id** and
   the **application (client) id** into the module (`entra.connection`).
2. **A secret** — "Certificates & secrets › New client secret", expiry at most what
   the tenant allows: paste the value, the module seals it with its expiry; a second
   one later for rotation (R §10.15).
3. **Permissions** — "API permissions › Add › Microsoft Graph › Application
   permissions": the list in §3.6, then *Grant admin consent*; and "Roles and
   administrators › User Administrator › Add assignment" for the app's service
   principal (§3.1), the step no comparable product documents and the one a
   half-done setup fails on at the first enabling. **Test connection** fetches a token
   and reads the organisation and the verified domains; the token's `roles` claim is
   compared with the list, and the page names each permission still missing, so a
   half-consented app is visible before the first person is due. The directory role
   cannot be read without a further permission; the first sensitive action reports
   `role_missing` instead.
4. **Accounts** — the verified domain user principal names are created under, the
   pattern for a person without a work email, the default `usageLocation`, the leaver
   steps (licences, groups, sessions, the leave date; each on by default), the
   Temporary Access Pass handover and its lifetime — with the sentence that the
   tenant's authentication methods policy must enable the pass for the users (§3.4),
   and the path to it.
5. **Sign-in** — identity's OIDC settings (§6.4) with the same tenant and client id
   pre-filled: "Authentication › Add a platform › Web" with the fixed callback shown to
   copy, `openid profile email`. One app registration serves both the sign-in and the
   Graph connection; the Owner may use two.
6. **Provisioning** — identity's SCIM token generated and shown once, with the
   provisioning page's path ("Enterprise applications › the app › Provisioning ›
   Automatic", the tenant URL and the token), and the sentence that Vantigo now accepts
   the enterprise extension's attributes Entra maps by default.

Steps 5 and 6 are optional and say what happens without them: without sign-in, Staff
creates Microsoft accounts and nobody signs in to Vantigo with them; without
provisioning, the Vantigo user appears at the person's first sign-in rather than
within Entra's provisioning cycle after the enabling.

## 8. What the first version should hold, and what should wait

**The first version:**

- The settings move (§6), as its own delivery, independent of the module: it is a
  prerequisite for the wizard's last two steps and worth having in a Google
  installation that will never run Entra.
- The enterprise extension inbound, the announced account (§5.3) and the per-person
  account kind (§7.2): identity and Staff changes a fake provider can exercise before
  a line of Graph exists, and the correlation the roadmap's sentence promises.
- The provisioner contract and slot, Staff's worker driving a second provider per
  link, Staff's groups with listed and derived membership, the traits (§7.1, §7.3).
- The module: the connection and its secrets in the plural with expiry warnings; a
  Graph client under the guard with a token cache, `Retry-After`, batches; the user
  lifecycle — find by employee id, create disabled, enable on the day with a pass,
  update the mapped attributes and the manager, disable with sessions revoked and
  direct licences and group memberships stripped, the two dates, delete after the
  setting with the legal hold respected, re-enable on rehire; groups as security or
  Microsoft 365 groups with a Team on request, owners, members, licences by trait;
  the wizard; the Account tab's Entra section and the pass shown once; the attention
  items; the docs in both languages.
- Identity's removal of what it keeps after Staff's erase (§1.2): a managed, disabled
  user deleted on Staff's call once the person is erased, the audit rows kept as for
  any deleted user, their snapshots pseudonymised — the automation the roadmap left to
  this module.
- A fake Graph in the test harness (users, groups, members, licences, the pass,
  `$batch`, 429) and the tagged test against the dedicated tenant (§9).

**Later, named so the design keeps the door open:**

- A **certificate credential** (a JWT assertion the server signs); workload identity
  federation only where a platform issues tokens.
- **Mailbox conversion to shared and OneDrive transfer** at leaving: not in Graph (the
  conversion is Exchange Online's; Microsoft's own guidance does it in the admin
  centre), and the delete must then wait — the setting's help text says what deleting
  loses.
- **Hybrid tenants** (users synced from on-premises AD), which Graph refuses to write;
  the module reports and stops.
- **Lifecycle Workflows alignment**: a tenant with Governance may run its own
  workflows on the dates the module writes; nothing more is needed from the module,
  and a per-step "leave this to the tenant" switch is a later refinement.
- **Google Workspace** as a second provider — the traits model and the kind column are
  shaped for it; the research of R §5.3 stands.
- **Delegated (signed-in) authorisation** to Graph, administrative-unit scoping,
  national clouds, a claims-mapping policy for an employee id claim, Entra dynamic
  groups (never), reading Entra as a source of truth for anything but the account's
  own state, photos, the SCIM `timezone` attribute.
- **Group-based licensing** as a trait (a P1 feature the admin centre configures); the
  first version assigns directly.

## 9. What the Entra test tenant needs

R §11 stands, corrected by §3: a dedicated tenant (Entra ID Free covers users and
groups through Graph; a licence is needed only for a Team's members to use it and for
the licence trait); an app registration with the application permissions of §3.6, admin
consented, **and the User Administrator role assigned to its service principal**;
TAP enabled in the authentication methods policy for all users; two client secrets
with their expiry dates; the tenant id, the client id and a verified domain
(`onmicrosoft.com` is fine); for sign-in and provisioning, an enterprise application
with the web redirect to a reachable Vantigo (a tunnel locally) and automatic
provisioning against Vantigo's SCIM endpoint with the token the wizard generates. The
tagged test is `//go:build entra`, with `ENTRA_TEST_TENANT_ID`, `ENTRA_TEST_CLIENT_ID`,
`ENTRA_TEST_CLIENT_SECRET` and `ENTRA_TEST_DOMAIN`, skipped without them, the Vipps
shape; it creates, enables, updates, disables and deletes one user and one group with a
test prefix and removes what it made, and it settles the three **UNCERTAIN** facts
(`employeeHireDate` app-only, the TAP permission's name, which properties a hybrid
user refuses) — the plan's contributing page repeats this with the portal paths.
**UNCERTAIN** whether the Microsoft 365 Developer Program sandbox is still available
without a Visual Studio Enterprise subscription; a trial or a pay-as-you-go tenant is
the fallback.

## 10. Decisions a design must take before a plan

1. **The contract's shape and the kind column** (§7.1, §7.2): a per-person account
   kind replacing the installation-wide mode in Staff's table, with the installation
   default as a Staff setting; `Mode()` kept for the `external` kind only.
2. **The announced account** (§5.3): `Expect` on `AccountProvisioning`, matched by
   employee number, UPN or object id on SCIM and by `oid` on sign-in; a pending local
   user completed rather than duplicated.
3. **The settings table and the import** (§6): the row wins, the environment imports
   once, the next release refuses the variables; `ErrModeMismatch` retired.
4. **Whom the pass is shown to** (§3.4): HR on the Account tab once, the manager by
   `Notify`, or both — and whether usable once.
5. **The leaver's order and the day's hour**: Staff's "day after the end" at the
   worker's first cycle, or a leave hour on the end day (Microsoft's and Simployer's
   end of day); the design keeps Staff's rule and writes the end-of-day instant to the
   leave date.
6. **An archived group's fate in Entra**: deleted after the setting, or kept.
7. **The credentials' next kind**: a certificate, and when.
8. **Delete after N days** with a mailbox: the setting stays per installation, null by
   default; whether the Owner must confirm a non-null value with the sentence about
   the mailbox.

## 11. Open questions for the owner

1. The pass: shown to HR, mailed to the manager through `Notify`, or both? Usable once
   by default?
2. Should a group Staff archives be deleted in Entra, or kept and renamed?
3. Should the module **write `employeeHireDate`** even if the tagged test shows it
   needs a delegated permission (then as a reported fact), or drop it from the first
   version?
4. Is one app registration for the sign-in and the Graph connection the default the
   wizard proposes, or two?
5. The per-person kind's default when the module is connected: `entra` for every new
   person, or `local` with HR opting in?
