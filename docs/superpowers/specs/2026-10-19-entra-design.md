# Entra — design (phase 1)

A new module, `entra`, researched in
[`../research/2026-10-19-entra-module.md`](../research/2026-10-19-entra-module.md)
("E", cited by section) on the owner's decisions of 2026-10-11
(`../research/2026-10-11-staff-module.md` §10, "R"): Microsoft Entra ID as Vantigo's
first external identity provider. The account Staff's flag creates becomes a user in
the company's tenant — created without licences `createBeforeDays` ahead, enabled on
the start day with a Temporary Access Pass, kept in step as the person moves, disabled
the day after the end with its sessions revoked and its licences and groups stripped,
deleted later or never — and the Vantigo sign-in follows through SSO, correlated on
the employee number. Groups are Staff's, listed or derived from the register, and the
module keeps them in the tenant with the traits an Owner sets. On the way the OIDC and
SCIM configuration moves from the environment into identity's database, and the
wizard on the module's tile carries every portal step.

Like Calendar and Staff the module **depends on nobody but identity** for what it
reads; what it writes outside its own schema goes nowhere — Staff calls it, through a
new many-provider slot, and identity's accounts stay identity's (R §10.10). Code
references are against `main` at `a529e1af`; `srv/` abbreviates
`apps/server/internal/`, `mig/` `srv/db/migrations/`, `S` the staff foundation
design's decisions and readings, `boundaries.md` the module-boundaries page.

Phase 1 is five deliveries, each a pull request with one migration per schema it
touches (`srv/db/schema_test.go` forbids one file touching two), each number assigned
when its first task lands. **A** moves the settings. **B** gives identity the
enterprise extension and the announced account, Staff the account kind and the
provisioner slot, and both a fake provider to prove it. **C** is the module: the
connection, the Graph client, the user lifecycle, the wizard. **D** is the groups and
the traits. **E** is the erase's automation and the operator's leftovers. A, B and C
are specified in full here; D and E take their decisions here and the rest with their
plans.

## Decisions

### D1 — `identity.sso_settings`: one row, read through a short cache, the environment imported once

Migration `00058_identity_sso_settings.sql`:

```sql
CREATE TABLE identity.sso_settings (
    id                          smallint     PRIMARY KEY CHECK (id = 1),
    version                     integer      NOT NULL DEFAULT 0,   -- 0: never written
    oidc_enabled                boolean      NOT NULL DEFAULT false,
    oidc_provider               varchar(10)  CHECK (oidc_provider IN ('entra', 'google')),
    oidc_authority              varchar(200),
    oidc_client_id              varchar(200),
    oidc_client_secret_sealed   bytea,                             -- identity/oidc-client-secret
    oidc_client_secret_set_at   timestamptz,
    oidc_workload_token_file    varchar(500),
    oidc_allowed_domains        text[]       NOT NULL DEFAULT '{}',
    oidc_display_name           varchar(100),
    scim_enabled                boolean      NOT NULL DEFAULT false,
    scim_token_sha256           bytea,
    scim_token_set_at           timestamptz,
    scim_previous_token_sha256  bytea,
    scim_previous_expires_at    timestamptz,
    imported_from_environment   boolean      NOT NULL DEFAULT false,
    updated_at                  timestamptz  NOT NULL,
    updated_by_user_id          uuid
);
```

The row is its own table, not columns on `system_settings`: the session query reads
"is SCIM on" on every request, and a sealed secret and two digests should not travel
with the maintenance flag and the time zone. The module's validation — not a CHECK —
holds `config.go:1258-1475`'s rules as they stand: the authority's exact form per
provider, a GUID client id for Entra and a `.apps.googleusercontent.com` one for
Google, the secret and the token file mutually exclusive and one required while OIDC
is on, allowed domains Google-only and at least one, the previous SCIM digest and its
expiry together or neither, the expiry at most 24 h ahead.

**The SCIM token is generated, shown once, and only its digest kept.** `scim.go:166-175`
compares digests already; the clear token existed only because an environment had to
hold it. The server makes it (32 random bytes, `base64.RawURLEncoding`, the feed
token's shape), answers it once, and stores the SHA-256. A lost token is replaced.
**The client secret is sealed** under `identity/oidc-client-secret` by the secrets
box, never answered; the page shows when it was set. The workload token file is a
path and stays one.

**Reading.** `identity.Access` gains `settings *ssoSettingsCache`, the maintenance
cache's shape (`system.go:28-62`): a `get(now)` that re-reads the row at most every 20 s
per process and an `evict()` the writing process calls after its commit. Every reader
of `cfg.SCIM` and `cfg.OIDC` in §E 1.1's table — the session query's and
`UserMayAct`'s `ScimEnabled`, sign-in's, passkeys', two-factor's, the bearer check,
the system status, the user directory's `scimEnabled`, the provisioning provider's,
the mode's derivation — reads the cached row. The directory and the provider read it
**per call** rather than copying at construction (`userdirectory.go:46`), so an Owner's
change reaches them within the 20 seconds too.

**The relying party is versioned.** `server.oidc` (`server.go:34-63`) becomes
`server.oidcRP(ctx) (*oidcRelyingParty, error)`: under the relying party's mutex it
answers the party built for the settings `version` it last saw and rebuilds when the
version differs — the normalised issuer, the redirect, the OAuth2 config; discovery is
still lazy. A save that changes `oidc_authority` while `oidc_links` holds rows under
the old issuer is refused `oidc_links_exist` (409) unless the body carries
`confirmIssuerChange: true`, since the links are keyed on it and every linked person
would provision anew.

**The import, once.** At identity's mount, where `recordAuthorityMode` runs today, an
API process that finds `version = 0` and `OIDC_PROVIDER` or `SCIM_TOKEN` set (the loader's own two switches) imports the two blocks
in one transaction holding the row (`SELECT … FOR UPDATE`): the secret sealed, the
token and the previous token digested with the expiry, `imported_from_environment`
true, `version` 1, `updated_by_user_id` null; audits `sso-settings.imported` with the
**names** of the variables and nothing else; logs a warning that the variables are now
ignored. A process that finds `version > 0` imports nothing and logs each variable
still set as a leftover, by name, once per start. Two replicas starting together import
once: the second finds the row written. **This release reads the row only**; the
`config` loader keeps parsing the variables for the import alone, and the next release
refuses them at startup with the leftover sentence `config.go:1261` already has and
drops `OIDCConfig` and `SCIMConfig`. Workers never import.

**The authority mode is no longer a guard.** `deriveAuthorityMode` reads the row; the
API and the workers read the same row, so the recorded mode and a process's derivation
cannot disagree. `recordAuthorityMode` keeps writing `system_settings.authority_mode`
when the row's derivation changes — at every API start, in the import's own transaction, and at every save — with the audit
`authority-mode.changed` as today, as a trail. `authorityMode` answers the derivation
and never `ErrModeMismatch`; the sentinel, `accountModeMismatch` and Staff's
`mode_mismatch` reason stay one release so a worker on the old binary reports sanely
during a rolling upgrade, then go (E §6.3).

### D2 — The Owner's SSO endpoints, one card, and the module's tile

Under `/api/v1/identity`, `policy:OwnerManagement` (Owner plus MFA, the workspace's
rule), rate-limited as `policyUserManagement`:

| Operation | Does |
| --- | --- |
| `GET /owner/sso` | the row with the secret and the digests withheld: `oidc { enabled, provider, authority, clientId, clientSecretSetAt?, workloadTokenFile?, allowedDomains, displayName, callbackUrl }`, `scim { enabled, tokenSetAt?, previousExpiresAt?, endpointUrl }`, `importedFromEnvironment`, `lastOidcSignInAt?`, `lastScimRequestAt?`, `version` |
| `PUT /owner/sso/oidc` | `{ provider, authority, clientId, clientSecret?, workloadTokenFile?, allowedDomains, displayName, confirmIssuerChange? }` with `If-Match` on `version`; a `clientSecret` absent keeps the sealed one, present replaces it; the token file's readability checked by the saving process, with the sentence that every replica must see the path |
| `DELETE /owner/sso/oidc` | turns OIDC off and clears the block, the secret included; refused `oidc_links_exist` without `?confirm=true` |
| `POST /owner/sso/scim/token` | turns SCIM on if off and issues a token, answered **once** as `{ token, endpointUrl }`; when a token exists, the old digest moves to the previous slot with `previousExpiresAt` from the body (at most 24 h, default 24 h), so a rotation overlaps as today |
| `DELETE /owner/sso/scim/previous` | ends the overlap early |
| `DELETE /owner/sso/scim` | turns SCIM off; the mappings and groups stay, as they do today when the variable is removed |

Every write is one transaction holding the row; one that changes something bumps `version`, clears `imported_from_environment` (the row is the Owner's from then on) and audits
`sso-settings.changed` with before and after **minus** the secret and the digests, and
evicts the cache. `GET /owner/system-status` keeps its three SSO fields, now from the
row. The problems are identity's `CodeMessageError` shape with field errors as
`config.go` words them; a refused authority says what form it must have.

The host renders one **Single sign-on** card under Workspace administration for every
installation — the provider, the callback to copy, the SCIM endpoint and the token
actions — and the Entra tile's wizard (D15) embeds the same two forms as its steps 5
and 6, calling the same endpoints: one place for the operator, two owners in the
backend (R §10.17). `admin/sso-scim.md` is rewritten around the card; its environment
section becomes "Before 2026-10: the environment variables", with the import.

### D3 — The SCIM enterprise extension, inbound

Identity's `Schemas` and `ResourceTypes` advertise
`urn:ietf:params:scim:schemas:extension:enterprise:2.0:User` with `employeeNumber`,
`costCenter`, `organization`, `division`, `department` and `manager { value, $ref,
displayName }` (RFC 7643 §4.3). A POST or PUT carrying the block, and a PATCH on any of
its paths (`urn:…:User:department`, `urn:…:User:manager.value`, …), are **accepted**
and stored in `scim_user_mappings.source_profile` under an `enterprise` key beside the
name parts; GET answers them back; `manager.value` is kept as the SCIM id Entra sent
and answered with `$ref` to this server's path, resolved to nothing else. Nothing in
`identity.users` changes for them except what D4 says of `employeeNumber`. A filter on
`employeeNumber` (`urn:…:User:employeeNumber eq "…"`) is supported, since Entra's
matching needs the target to filter on the attribute it matches by (E §3.7). The
refusal `invalidPath` stays for every path outside the two schemas.

### D4 — The announced account: `Expect`, matched on arrival

`contracts.AccountProvisioning` gains:

```go
// ExpectedAccount is a Vantigo user the caller knows will arrive from the
// identity provider for a person it registers: Staff has had the provider
// create the external account, and announces it so that identity adopts the
// user the provider's SCIM push or the person's first sign-in creates, with
// no Owner in between. Matched by EmployeeNumber (the enterprise extension's
// employeeNumber), by UserPrincipalName (SCIM's userName), or by ObjectID
// (OIDC's oid; SCIM's externalId when the tenant maps it so).
type ExpectedAccount struct {
    EmployeeNumber    string
    DisplayName       string
    PreferredLanguage string
    Issuer            string   // the OIDC issuer the user will sign in under
    ObjectID          string   // the provider's immutable id of the user
    UserPrincipalName string
    // ReplaceUserID names a pending, credential-less managed user of the
    // same employee number (the person's kind changed from local): the
    // arrival completes that user instead of creating one.
    ReplaceUserID     *uuid.UUID
}

type AccountProvisioning interface {
    // … as today …
    // Expect records or replaces the announcement for spec.EmployeeNumber.
    // ErrEmployeeNumberTaken when an unmanaged user holds the number,
    // ErrAccountPrivileged when the managed one is an Owner.
    Expect(ctx context.Context, actor Actor, spec ExpectedAccount) error
    // Unexpect withdraws an announcement nobody fulfilled.
    Unexpect(ctx context.Context, actor Actor, employeeNumber string) error
    // Erase deletes a managed, disabled user whose person was erased (D16).
    Erase(ctx context.Context, actor Actor, userID uuid.UUID) error
}
```

`identity.expected_accounts (employee_number_key PRIMARY KEY, employee_number,
display_name, preferred_language, issuer, object_id, user_principal_name,
replace_user_id, created_at, created_by)` in `00059_identity_expected_accounts.sql`,
delivery B's. Two arrivals consult it, inside
their own transactions, under the owner lock:

- **SCIM's create** (`createScimUser`): a match on the enterprise `employeeNumber`,
  else on `userName` equal to `user_principal_name` (normalised as emails are), else on
  `externalId` equal to `object_id`, makes the new user **managed by Staff**
  (`managed_by = 'staff'`, the employee number and its key, `pending_since` null —
  the credential is the provider's), with the announced display name only when SCIM
  sent none; a `ReplaceUserID` is completed instead of inserted: the mapping is
  attached to that user, `pending_since` cleared, its open invitations revoked
  `replaced`, its address overwritten by `userName` (the person is being signed in
  from the tenant, and D1's digest rule means no mail reached anyone with it). The
  announcement is consumed and audited `account.adopted-from-provider` naming the
  match. No match: today's plain SCIM user.
- **OIDC's completion** (`provisionOIDCAccount`): a match on `oid` with the issuer
  provisions the user managed and numbered the same way, or completes the named one,
  then links `(issuer, sub)` as today. The email claim's collision rule stands
  (`oidc_email_conflict`), except that the colliding account **is** the one being
  completed, which is the completion, not a conflict.

An announcement is consumed on fulfilment and withdrawn by `Unexpect`; one older than
90 days unfulfilled is what Staff's attention reports (`accountNotArrived`, D7). The
Owner's manual adoption stays for the `external` kind and for anyone the module did
not create. `identity.users.employee_number` is written by these two paths and by
`Prepare` and `Update` as today, by nobody else: SCIM's `employeeNumber` on an
unmanaged user is kept in the profile, never promoted to the column, because the
column is Staff's claim on a user and an administrator's mapping must not make one.

### D5 — The account kind, per person

`staff.people.account_kind varchar(10)` — `local`, `external` or `entra` — and
`staff.settings.default_account_kind` (`00060_staff_account_kind.sql`, delivery B's,
with D7's link columns and D8's setting), the per-person authority S open question 3 asked for and the
owner's answer 6 deferred here:

| Kind | Identity link (`provider = 'identity'`) | Provider link (`provider = 'entra'`) |
| --- | --- | --- |
| `local` | today's local path whole: `Prepare` creates, `Activate` invites and assigns the roles, `Deactivate` | none |
| `external` | today's federated path: `Prepare` finds only, `Activate` re-enables what Staff disabled, `Deactivate`; `accountNotFound` until an Owner adopts | none |
| `entra` | link-only like `external`, plus `Expect` after the provider's `Prepare` answered, so no adoption is needed; `Activate` assigns the provisioning roles on the first activation of the adopted user (the one thing the local path did that an SSO user still needs — S26's set, which is identity's to give) | D7's whole table |

The migration seeds every person's `account_kind` from the recorded authority mode
(`local` → `local`, `federated` → `external`) and the setting the same way; the Entra
wizard's last step offers to set the default to `entra` and says that existing people
keep theirs. HR changes a person's kind on the Account tab (`staff:manage`); a change
from `local` to `entra` while the identity link is pending is what `ReplaceUserID`
carries (D4), and from `entra` to `local` is refused `account_kind_locked` while an
Entra link exists that is not disabled — a person leaves Entra by HR's *Disable now*
or by the end of their employment, never by a kind change. `GET /meta`'s `accountMode`
becomes `defaultAccountKind` plus `providers: [{ key, name, status }]` from the slot.
`Mode()` stays on the contract for the `external` kind's rule; Staff's `decide` takes
the person's kind where it took the mode.

### D6 — `contracts.IdentityProvisioner`, a many-provider slot Staff writes through

```go
// contracts.IdentityProvisioner is an external identity provider's accounts
// and groups as a module offers them to Staff: Entra today, another
// directory later. Staff is the authority — it decides what exists and when,
// from the register — and the provider executes against its directory and
// answers what it did. Every write names its Actor; every write is
// idempotent; every call runs on the provider's own pool and reaches the
// network, so a caller never makes one inside a transaction or holding a
// connection, as for AccountProvisioning (rule 11).
type IdentityProvisioner interface {
    // Provider names the provider: its key (the identity_links.provider
    // value, "entra"), its display name, and its status — unconfigured
    // (no connection yet), connected, or failing (the last check failed:
    // a credential expired, consent withdrawn), with the reason code.
    Provider(ctx context.Context) (ProviderInfo, error)

    // Prepare finds the account holding spec.EmployeeNumber, or creates it
    // disabled with no licence and the mapped attributes. Idempotent: an
    // existing account is updated to the spec and answered.
    // ErrExternalAmbiguous (two accounts hold the number),
    // ErrExternalAddressTaken (the address is another account's),
    // ErrExternalSyncedOnPremises, ErrExternalPrivileged, ErrProviderUnavailable.
    Prepare(ctx context.Context, actor Actor, spec ExternalSpec) (ExternalAccount, error)
    // Activate enables the account, writes the hire date, assigns the
    // licences of the groups the person is in (spec.Licences) and, when the
    // account had no pass yet in this activation, issues a Temporary Access
    // Pass the provider keeps sealed (RevealPass). Idempotent.
    Activate(ctx context.Context, actor Actor, externalID string, spec ExternalSpec) (ExternalAccount, error)
    // Deactivate disables the account, revokes its sessions, writes the
    // leave date, strips the direct licences, and removes it from the
    // groups the provider manages and, when the connection says so, every
    // other group it may. Idempotent; runs whatever Provider's status says,
    // as long as a credential works.
    Deactivate(ctx context.Context, actor Actor, externalID string, spec ExternalSpec) (ExternalAccount, error)
    // Update writes the mapped attributes and the manager (the mover).
    Update(ctx context.Context, actor Actor, externalID string, spec ExternalSpec) (ExternalAccount, error)
    // Delete soft-deletes the account (restorable by an administrator for
    // thirty days). ErrExternalEnabled when it is not disabled.
    Delete(ctx context.Context, actor Actor, externalID string) error
    // Account reads the account, or (nil, nil) when the directory has none.
    Account(ctx context.Context, externalID string) (*ExternalAccount, error)
    // RevealPass answers the pass issued for the account once — the second
    // call, and a call after its lifetime, answer ErrPassGone — and audits it.
    RevealPass(ctx context.Context, actor Actor, externalID string) (Pass, error)

    // Groups (delivery D). GroupTraits declares the traits a group may carry
    // for this provider; EnsureGroup creates or updates the external group
    // for the caller's group id with its trait values and owners;
    // SetMembers reconciles its members to exactly these; DeleteGroup
    // deletes it.
    GroupTraits() []GroupTrait
    EnsureGroup(ctx context.Context, actor Actor, spec GroupSpec) (ExternalGroup, error)
    SetMembers(ctx context.Context, actor Actor, groupID string, memberExternalIDs []string) (MembersResult, error)
    DeleteGroup(ctx context.Context, actor Actor, groupID string) error
}

type ExternalSpec struct {
    EmployeeNumber, GivenName, FamilyName, DisplayName string
    WorkEmail         string          // "" lets the provider make the address
    PreferredLanguage string          // "nb" | "en"
    WorkCountry       string          // ISO 3166-1 alpha-2, the usage location
    HireDate, LeaveDate *time.Time    // civil days; the provider makes the instants
    Fields            map[string]string // the mapped fields the installation turned on (D8): "jobTitle", "department", …
    ManagerExternalID string          // "" when the manager has no account here
    Licences          []string        // SKU part numbers from the person's groups (D10)
    Flags             SpecFlags       // Fresh: HR's Run now (a new pass)
}

type ExternalAccount struct {
    ExternalID        string // the directory's immutable id
    UserPrincipalName string
    Email             string // the address the provider made or found
    State             string // "disabled" | "enabled" | "deleted"
    Created           bool   // true only on the Prepare that created it
    HasPass           bool   // a pass exists and was not yet revealed
    Reported          []string // facts the provider could not act on: "hire_date_not_written", "licence_count", "privileged", …
}
```

The slot is `IdentityProvisioners []contracts.IdentityProvisioner` on `module.Deps`,
filled from `IdentityProvisioner func(Deps) contracts.IdentityProvisioner` on
`module.Module`, collected by `Compose` **and** `Workers` from **enabled modules only**
as `CalendarSources` is (`module.go:150-162`, `workers.go`), after `Users` and
`Accounts` so a provider may read the directory while it is built. Staff reaches it
through `contractscalls.go` accessors that refuse a call inside a transaction, keyed by
provider; two modules claiming one key fail composition. The sentinel errors are the
contract's (`ErrExternalAmbiguous`, `ErrExternalAddressTaken`,
`ErrExternalSyncedOnPremises`, `ErrExternalPrivileged`, `ErrExternalEnabled`,
`ErrPassGone`, `ErrProviderUnavailable`, `ErrGroupNotManageable`); anything else is a
failure to retry. `boundaries.md` gains rule 13: **Staff writes a provider's directory**
— the fifth sanctioned direction, Staff → a provider module, the write made for the
accounts and groups of the people it registers, under rule 11's lock discipline, with
the provider answering only facts about the external object and never an address
Staff did not give it except the one it made.

### D7 — Staff drives the provider link: the decision table's third column

Staff's worker (`accounts_worker.go`) claims links of every provider; `decide`
(`accounts_decide.go`) takes the link's provider and the person's kind and answers one
step as today. For a person of kind `entra` with `T` today, `F` the effective
`provisionFrom`, `E` the last end, `P` the provider link, `L` the identity link, with
the provider `connected`:

| State | Provider link `P` | Identity link `L` |
| --- | --- | --- |
| provider `unconfigured` or `failing` | report `provider_unavailable` (installation item), nothing | nothing |
| flag off, no P | nothing | nothing |
| flag on, no employment | `no_employment` | same |
| flag on, `T < F − createBeforeDays` | nothing | nothing |
| flag on, `F − createBeforeDays ≤ T < F`, no P | `Prepare` → P disabled; then `Expect` | `Prepare` (find) → L pending or nothing yet |
| flag on, `T ≥ F`, `T ≤ E` or no E | `Prepare` if no P; `Activate` → P enabled, `HasPass` | `Prepare` (find); L active when the adopted user exists; `Activate` assigns the roles once |
| flag on, `T > E`, no later employment | `Deactivate` → P disabled; after `delete_external_after_days` and no legal hold, `Delete` → P deleted | `Deactivate` → L disabled; after the person's erase, `Erase` (D16) |
| rehire: P disabled, flag on, `T ≥ F` | `Activate` (a new pass) | `Activate` re-enables |
| P enabled, spec changed (`last_spec_hash`) | `Update` | `Update` of language and number |
| P any, `Account` answers nil | `failed: user_gone`, never re-created (S reading 18) | — |
| HR's *Disable now* | the flag off, then `Deactivate` on P, then on L | — |

The order in a cycle is **provider first, identity second**, so the announcement
precedes the arrival; each step is one call under the claim's lease as today, at most
three per claim. The provider's `Reported` facts land on the link as
`blocked_reason`-shaped codes and become attention items for `staff:manage`:
`entraNotConnected` (installation), `entraFailing`, `accountNotArrived` (announced,
nothing came within 90 days), `accountAmbiguousInEntra`, `accountAddressTakenInEntra`,
`accountSyncedOnPremises`, `accountPrivilegedInEntra`, `hireDateNotWritten`,
`licenceUnavailable` (`CountViolation` and its siblings, with the SKU), `passUnrevealed`
(a pass issued and not shown within its lifetime), `secretExpiring` (installation; the
newest valid secret ends within 30 days), `secretExpired`. Every reason is a code,
never an address or Graph's words (plan I5).

`staff.identity_links` needs no migration for the second provider (S reading 12) but
gains `external_upn varchar(254)` and `external_email varchar(254)` in `00060` for the
Account tab, and `provider_state` reuses `state`. A person's **Account tab** shows the
two links side by side — "Microsoft Entra ID: enabled since …, UPN …" and "Vantigo:
active" — with *Run now*, *Disable now* (both links), *Show the pass* and *New pass*
(`staff:manage`, audited as `account.pass_revealed`), and the directory profile (D8).
The work email a provider made for a person who had none is written to the person
(`work_email`, event `work_email_set_by_provider`) the first time the link answers it.

### D8 — The directory profile: a fixed mapping, toggled per installation

`staff.settings.directory_fields jsonb` — `{ "jobTitle": true, "department": true, …
}` over the switchable rows of E §7.4's table, seeded with its defaults — edited on
Staff's Settings under **Directory profile** (`staff:manage`), and shown read-only on the
person's Account tab as the table with the value each field would carry today. The
non-switchable rows (names, employee number, work email, work country) are listed and
greyed. `ExternalSpec.Fields` carries exactly the switched-on fields with their values
("" to clear), so the provider writes what the installation chose and nulls what it
turned off; `employeeOrgData` is written whole (E §3.1). The manager is the terms'
today, else the department's chain, the same rule `reportsWithoutManager` reads, and
travels as the manager's **external id** from their own provider link — a manager
without an account here is no manager in Entra, reported `managerNotInEntra` once.

### D9 — Groups in Staff: listed or derived, owners, members materialised daily

`00062_staff_groups.sql`, delivery D's, adds:

```sql
CREATE TABLE staff.groups (
    id           bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    key          varchar(60)  NOT NULL,            -- stable, lower-case, the provider's reference
    name         varchar(120) NOT NULL,
    description  varchar(500),
    kind         varchar(10)  NOT NULL,            -- 'listed' | 'derived'
    rule         jsonb,                            -- derived: { "kind": "employed" | "department" | "businessUnit" | "location" | "employeeType" | "reportsTo", "id"?: …, "includeBelow"?: bool }
    archived_at  timestamptz,
    revision     integer      NOT NULL DEFAULT 1,
    created_at   timestamptz  NOT NULL,
    updated_at   timestamptz  NOT NULL
);
CREATE UNIQUE INDEX ux_groups_key ON staff.groups (key);

CREATE TABLE staff.group_members (
    group_id   bigint      NOT NULL REFERENCES staff.groups (id) ON DELETE CASCADE,
    person_id  bigint      NOT NULL REFERENCES staff.people (id),
    source     varchar(10) NOT NULL,               -- 'listed' | 'derived'
    since      date        NOT NULL,
    PRIMARY KEY (group_id, person_id)
);

CREATE TABLE staff.group_owners (
    group_id   bigint NOT NULL REFERENCES staff.groups (id) ON DELETE CASCADE,
    person_id  bigint NOT NULL REFERENCES staff.people (id),
    PRIMARY KEY (group_id, person_id)
);

CREATE TABLE staff.group_traits (
    group_id   bigint      NOT NULL REFERENCES staff.groups (id) ON DELETE CASCADE,
    provider   varchar(20) NOT NULL,
    values     jsonb       NOT NULL,
    external_id varchar(200),                      -- what the provider answered
    state      varchar(20) NOT NULL,               -- 'pending' | 'synced' | 'failed' | 'deleted'
    last_members_hash bytea,
    last_error varchar(200),
    attempts   integer     NOT NULL DEFAULT 0,
    next_attempt_at timestamptz,
    lease_id   uuid,
    lease_until timestamptz,
    PRIMARY KEY (group_id, provider)
);
```

A **rule** is one predicate, evaluated on the business day against the employments
and terms in force: `employed` (everyone with an employment in force), `department`
(the terms name it, or one beneath it with `includeBelow`), `businessUnit`, `location`,
`employeeType`, `reportsTo` (the manager rule's chain, one level). The accounts
worker's cycle **materialises** derived membership before it claims links: per group,
the set the rule yields today, written as the difference (rows added with `since`
today, rows removed), each change an event on the person (`group_joined`,
`group_left`); a listed group's members are HR's rows. A person erased leaves every
group. Groups are `staff:manage`'s: `GET|POST /groups`, `GET|PUT|DELETE /groups/{id}`
(delete only when never synced; else archive), `PUT /groups/{id}/members` (listed
only), `PUT /groups/{id}/owners`, `GET /groups/{id}/traits`, `PUT
/groups/{id}/traits/{provider}`; a person's groups on their Account tab; My page shows
a person their own groups by name.

### D10 — Traits: declared by the provider, stored and rendered by Staff

```go
type GroupTrait struct {
    Key         string            // "type", "team", "licences"
    Type        string            // "bool" | "string" | "choice" | "list"
    Label       map[string]string // "en", "nb"
    Description map[string]string
    Choices     []TraitChoice     // for "choice" and "list": { Value, Label }
    DocURL      string            // the vendor's page
    Default     any
    Required    bool
}
```

Staff's group page renders, per provider the slot answers, the provider's traits as a
form from this description alone — no trait by name in Staff's code — and stores the
values as `group_traits.values`; a provider's `EnsureGroup` validates them and answers
field errors keyed by trait, which the page shows beside the field. A group gains a
provider section when HR turns the provider on for the group (`PUT
/groups/{id}/traits/{provider}` with `{ enabled: true, values }`), which the worker
then creates; off deletes or keeps per D14. Entra's traits in C and D: `type` (choice:
`security`, `microsoft365`; required; immutable once synced), `mailNickname` (string;
Microsoft 365; defaults from the key), `visibility` (choice: `private`, `public`),
`team` (bool), `licences` (list of the tenant's SKU part numbers from `subscribedSkus`,
with the available count in the label), `description`.

The **licences a person gets** are the union of the `licences` traits of the groups
they are in, computed by Staff into `ExternalSpec.Licences` on every activation and
update; the provider assigns the missing and removes the direct ones not in the list
(a licence assigned by the tenant through group-based licensing reads `assignedByGroup`
and is left alone). Group-based licensing on the module's groups is the P1 road the
admin page describes; the trait is the road without it.

### D11 — The module: schema `entra`, the connection, the credentials, one permission

Migration `00061_entra_baseline.sql` (delivery C; `entra.groups` is `00063_entra_groups.sql`, delivery D's):

```sql
CREATE SCHEMA entra;

CREATE TABLE entra.connection (
    id                    smallint     PRIMARY KEY CHECK (id = 1),
    tenant_id             uuid,
    client_id             uuid,
    upn_domain            varchar(253),            -- a verified domain
    upn_pattern           varchar(40)  NOT NULL DEFAULT 'given.family',
    default_usage_location char(2)     NOT NULL DEFAULT 'NO',
    leaver_strip_licences boolean      NOT NULL DEFAULT true,
    leaver_remove_groups  varchar(10)  NOT NULL DEFAULT 'all',  -- 'all' | 'managed' | 'none'
    leaver_revoke_sessions boolean     NOT NULL DEFAULT true,
    leaver_write_leave_date boolean    NOT NULL DEFAULT true,
    write_hire_date       boolean      NOT NULL DEFAULT true,
    pass_lifetime_minutes integer      NOT NULL DEFAULT 480,
    pass_usable_once      boolean      NOT NULL DEFAULT true,
    pass_delivery         varchar(10)  NOT NULL DEFAULT 'hr',   -- 'hr' | 'manager' | 'both'
    status                varchar(12)  NOT NULL DEFAULT 'unconfigured', -- 'unconfigured' | 'connected' | 'failing'
    status_reason         varchar(60),
    permissions_present   text[]       NOT NULL DEFAULT '{}',   -- the token's roles at the last check
    verified_domains      text[]       NOT NULL DEFAULT '{}',
    organisation_name     varchar(200),
    last_checked_at       timestamptz,
    version               integer      NOT NULL DEFAULT 1,
    updated_at            timestamptz  NOT NULL,
    updated_by_user_id    uuid
);

CREATE TABLE entra.credentials (
    id            bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    kind          varchar(20)  NOT NULL,            -- 'client_secret' (a certificate later)
    label         varchar(100),
    sealed        bytea        NOT NULL,            -- entra/client-secret
    expires_at    timestamptz  NOT NULL,
    created_at    timestamptz  NOT NULL,
    created_by_user_id uuid,
    last_used_at  timestamptz,
    last_failed_at timestamptz,
    revoked_at    timestamptz
);

CREATE TABLE entra.accounts (
    object_id       uuid         PRIMARY KEY,
    employee_number varchar(16)  NOT NULL,
    user_principal_name varchar(254) NOT NULL,
    state           varchar(10)  NOT NULL,          -- 'disabled' | 'enabled' | 'deleted'
    pass_sealed     bytea,                          -- entra/pass, until revealed or expired
    pass_expires_at timestamptz,
    pass_revealed_at timestamptz,
    created_by_us   boolean      NOT NULL,
    reported        text[]       NOT NULL DEFAULT '{}',
    last_synced_at  timestamptz,
    created_at      timestamptz  NOT NULL,
    updated_at      timestamptz  NOT NULL
);
CREATE UNIQUE INDEX ux_entra_accounts_number ON entra.accounts (upper(employee_number));

-- 00063_entra_groups.sql, delivery D's:
CREATE TABLE entra.groups (
    staff_group_key varchar(60)  PRIMARY KEY,       -- opaque, Staff's
    object_id       uuid         NOT NULL,
    type            varchar(12)  NOT NULL,
    team_created_at timestamptz,
    created_at      timestamptz  NOT NULL,
    updated_at      timestamptz  NOT NULL
);
CREATE UNIQUE INDEX ux_entra_groups_object ON entra.groups (object_id);
```

**Credentials in the plural** (R §10.15): the newest unrevoked secret whose
`expires_at` is ahead is the one used; a token request refused `invalid_client` marks
it `last_failed_at` and the next cycle tries the next; none left is `status = failing,
reason = credential_invalid`. Thirty days before the newest one ends the installation
item `secretExpiring` is raised; after, `secretExpired`. Rotation is *Add a secret*
then *Revoke* the old. The sealed value is answered to nobody; the list shows the
label, the dates and "in use".

**One permission**, `entra:manage` (category *Administration*, sensitive, **not
delegable**): the module's status and settings, the pass settings, the groups' Entra
sections' documentation. The connection itself — tenant, client, credentials, the
wizard's test — is `policy:OwnerManagement`, as identity's SSO settings are (D2): a
tenant-wide application credential is an Owner's to hold. `staff:manage` never reaches
the module directly; everything HR does with accounts goes through Staff and the slot.

### D12 — The Graph client: guarded, cached token, `Retry-After`, batches

`srv/entra/graph.go` is the module's client, the calendar fetch's shape copied
(`subscriptions_fetch.go:60-125`, never imported): a transport built once per process
whose dialler goes through `netguard.DialContext`, no proxy, no redirects, keep-alives
**on** (unlike a subscription's stranger, Graph is one host called many times a
cycle), dial 10 s, handshake 10 s, response headers 15 s, each call under a 30-second
budget, `Deps.HTTPTransport` the test seam. Two fixed hosts — `login.microsoftonline.com`
for the token (`/{tenant}/oauth2/v2.0/token`, `client_credentials`,
`https://graph.microsoft.com/.default`) and `graph.microsoft.com/v1.0` — with no setting
to change them; the fake Graph in tests comes through the transport, not a base URL.
The token is cached in the process until five minutes before `expires_in`, under a
mutex, per credential; its `roles` claim is read for the connection test and stored
on `connection.permissions_present`. A 429 waits `Retry-After` (at most 60 s, once
per call, then the call fails and the link retries at the next poll); a 5xx is
retried once after a second. `$batch` of twenty is used for members only, each item's
status read on its own. Every Graph error is mapped to a sentinel or a reported code;
the response body is logged at debug level with the tenant's ids and never written to
a row or shown to a person (plan I5).

### D13 — The user lifecycle in the tenant

**Prepare.** `GET /users?$filter=employeeId eq '<number>'&$select=id,userPrincipalName,
mail,accountEnabled,onPremisesSyncEnabled,employeeId`: two rows is
`ErrExternalAmbiguous`; one row is linked (`entra.accounts`, `created_by_us false`) and
brought to the spec by an `Update`; none is a **create** — `accountEnabled: false`,
`displayName`, `givenName`, `surname`, `mailNickname` (the UPN's local part), the UPN
(D13's address rule), `passwordProfile` with a 32-character random password and
`forceChangePasswordNextSignIn true` (the pass replaces it; it is never stored or
shown), `employeeId`, `usageLocation`, `preferredLanguage`, the `Fields`, then the
manager by `PUT …/manager/$ref` when `ManagerExternalID` is set. A 400 `ObjectConflict`
on the UPN is `ErrExternalAddressTaken` when the person's work email was the UPN, and a
retry with the next suffix when the pattern made it. `onPremisesSyncEnabled true` on a
found user is `ErrExternalSyncedOnPremises`. A 403 on a sensitive action is
`ErrExternalPrivileged` — the 403 itself is the evidence, the account reported
`privileged` — and `role_missing` on the connection when every account fails it, which
is the User Administrator role not yet assigned (E §3.1).

**The address.** The person's `work_email`, lower-cased, when its domain is one of the
tenant's verified domains and its local part is in the UPN's alphabet after folding —
else the pattern (`given.family`, `g.family`, `given`) over the folded names at
`upn_domain`, with `2`, `3`, … appended on conflict — becomes the UPN and
`mailNickname`; `mail` is not written (Exchange's). The address made is answered in
`ExternalAccount.Email` and Staff writes it to the person when they had none (D7).

**Activate.** `PATCH accountEnabled true`; `employeeHireDate` as the hire day's 01:00
in the business zone when `write_hire_date` — a 400 or 403 on that property alone is
retried without it and reported `hire_date_not_written` (E §3.1's **UNCERTAIN**);
`assignLicense` with the missing SKUs (a `licenseAssignmentState.error` reported with
the SKU, the rest assigned); then, when the account has no unrevealed pass or
`Flags.Fresh`, `POST …/authentication/temporaryAccessPassMethods` with
`lifetimeInMinutes` and `isUsableOnce` from the connection, the pass sealed under
`entra/pass` with its expiry; `HasPass` true. A tenant whose policy refuses the pass
(400 on the create) reports `pass_not_allowed`, with the policy's path in the item.

**Deactivate**, in this order, each step on by the connection unless named always:
`PATCH accountEnabled false` (always, first — it is what continuous access evaluation
reacts to), `revokeSignInSessions` (`leaver_revoke_sessions`), `employeeLeaveDateTime`
as the end day's 23:00 in the business zone (`leaver_write_leave_date`; the day Staff
disables is the day after, so the instant is in the past by then, which is what a
Lifecycle Workflow's leaver trigger wants), `assignLicense` removing every direct SKU
(`leaver_strip_licences`), and the groups: `managed` removes the person from the
module's groups, `all` from every group `memberOf` lists that is not dynamic,
role-assignable or mail-enabled-only-by-Exchange (the ones Lifecycle Workflows'
"remove from all groups" skips too), `none` leaves them. A step that fails leaves the
rest to the next cycle; the account is disabled first whatever else fails.

**Update** writes the changed attributes and the manager; the employee id is never
rewritten. **Delete** refuses an enabled account (`ErrExternalEnabled`), soft-deletes,
marks `deleted` and keeps the row for the audit; a rehire after a delete is a new
account (the Workday rule with a deleted old one). **Account** reads the user by id and
answers nil on 404, which Staff records `user_gone`. **RevealPass** opens the sealed
pass, answers it, clears it and audits; after `pass_expires_at` it answers
`ErrPassGone` and clears. The `manager` delivery (`pass_delivery`) is Staff's: on an
activation answering `HasPass`, Staff mails the manager's Vantigo user through
`Users.Notify` a notice saying a pass awaits on the person's Account tab — the pass
itself travels only through `RevealPass`, so no mail ever carries it; `both` adds the
notice to `hr`.

Every write audits in the module's own `entra.events` (actor, action, object id, the
reported codes, at) — a small append-only table in `00061`, read on the module's
status page, never carrying an address beyond the UPN the module made.

### D14 — Groups in the tenant

`EnsureGroup` creates the group on the first call: `displayName` Staff's name,
`mailNickname` from the trait or the key, `securityEnabled`/`mailEnabled`/`groupTypes`
by `type`, `visibility`, `description`, and **`owners@odata.bind` naming the module's own
service principal and the owners' accounts** (E §3.3: a group created app-only without
owners is unmodifiable); later calls `PATCH` the name, description and visibility, and
`type` is immutable once synced (a changed value is a field error `trait_immutable`).
`team: true` is honoured on a call at least fifteen minutes after the group's creation
and with at least one **person** owner (a field error otherwise), by `PUT
/groups/{id}/team`, three tries ten seconds apart; `team_created_at` recorded. A group
the module finds by `mailNickname` or key that has dynamic membership or is
role-assignable is `ErrGroupNotManageable`. `SetMembers` reads `/groups/{id}/members?
$select=id` (paged), adds the missing in `PATCH … members@odata.bind` batches of twenty,
removes the extra by `DELETE …/members/{id}/$ref` (never without `$ref`, E §3.3's trap)
in `$batch`, and answers `{ Added, Removed, Failed: [{ id, code }] }`. `DeleteGroup`
soft-deletes; an archived Staff group is deleted after `delete_external_after_days`
and **kept and renamed `… (archived)` when the setting is never** — the research's open
question 2, taken this way so the setting's one meaning holds for users and groups
alike; the owner may reverse it.

Staff's worker syncs groups in its cycle after the links: per `group_traits` row
claimed under its lease, `EnsureGroup` when `external_id` is null or the values' hash
changed, `SetMembers` when the members' hash changed, with the members' external ids
read from the provider links of the group's members that are enabled; the result on
the row. A Staff group with a provider section is "Staff's in Entra too", which the
group page says.

### D15 — The wizard, the tile, the endpoints

`apps/entra/frontend` (`@vantigo/entra-ui`), the tile **Microsoft Entra ID** /
**Microsoft Entra ID**, `entra:manage` for the tile, with the wizard as its one page and
a **Status** card above it once connected: the organisation, the permissions present
and missing, the secrets with their dates, the last check, the accounts and groups the
module keeps, the recent events. The six steps are E §7.5's, each a card with the
portal path, what to copy, and a *Done* check the server answers:

| Step | Writes to | Endpoint |
| --- | --- | --- |
| 1 Register | `entra.connection` tenant and client | `PUT /api/v1/entra/connection` (`policy:OwnerManagement`) |
| 2 A secret | `entra.credentials` | `POST /connection/credentials` `{ kind, label, secret, expiresAt }`; `DELETE /connection/credentials/{id}` |
| 3 Permissions and the role | nothing; the test reads | `POST /connection/test` → `{ status, organisation, verifiedDomains, permissionsPresent, permissionsMissing, roleKnown: false }` |
| 4 Accounts | the connection's settings | `PUT /connection/settings` (`entra:manage`) with the SKUs from `GET /connection/skus` |
| 5 Sign-in | identity's row (D2) | the host embeds identity's form, pre-filled with the tenant's authority and the client id |
| 6 Provisioning | identity's row (D2) | the host embeds identity's token card |

`GET /api/v1/entra/connection` (`entra:manage`) answers the status card; `GET /events`
the recent events; `GET /accounts` and `GET /groups` the module's rows as a read-only
list with the UPNs and the states. The problems are the module's `EntraProblem`: field
errors on the forms, `credential_in_use` on deleting the only valid secret while
accounts exist, `connection_in_use` on changing the tenant while `entra.accounts` has
rows (409, `?confirm=true` to proceed, which deletes nothing in the tenant). The last
step offers Staff's default kind (D5) through the host navigating to Staff's settings,
never by the module writing Staff's row.

### D16 — Identity's erase, on Staff's call

`AccountProvisioning.Erase(ctx, actor, userID)` — the automation
`admin/authentication.md` left to this module — runs after Staff's retention worker has
erased the person: it refuses a user not managed by Staff (`ErrAccountNotManaged`),
not disabled (`ErrAccountEnabled`) or privileged; deletes the user with their
invitations, OIDC links, passkeys, TOTP, sessions and SCIM mapping (the mapping's
`RESTRICT` lifted for this path, since the directory has long since deleted theirs or
will answer 404 to its next push, which SCIM handles today); and **pseudonymises the
audit rows' snapshots** that name them — the `before`/`after` details' name, email and
employee number replaced by `"[erased]"`, the rows kept with their actions and times
— audited `account.erased` with the user id alone. Staff's erase run calls it through
the accessor after its own commit, once per erased person with a managed link, and
records `identity_erased` on the person's events; a failure is retried daily and
`erasureIncomplete` is the item. The module's own rows go with the Entra account's
`Delete` (D13) or, when deletion is never, keep the UPN and the object id only, the
employee number blanked — the person is erased, the directory object an
administrator's.

### D17 — Docs and roadmap

- `docs/src/content/docs/en/reference/entra.md`, new: the schema, the permission, the
  connection and the credentials, the Graph client and its limits, the user lifecycle
  per step with the Graph calls and the codes, the address rule, the pass, the groups
  and the traits, the events, the endpoints; `sources: [apps/server/internal/entra,
  apps/entra/frontend, openapi/entra.yaml]`. `reference/index.md`'s table gains the row.
- `reference/staff.md`: the account kind, the provider link and its table, the
  directory profile, the groups and the traits, the new attention items;
  `reference/identity`-equivalent pages (`admin/authentication.md`) for the SSO
  settings, the enterprise extension, the announced account and `Erase`.
- `docs/src/content/docs/{en,nb}/admin/entra.md`, new: the procedure — the tenant, the
  app registration, the permissions **and the User Administrator role**, the secret,
  the TAP policy, the sign-in redirect, the provisioning page; what the leaver steps
  do and what deleting loses; the tagged test's variables for a contributor;
  `admin/index.md`'s module list; `admin/sso-scim.md` rewritten around the card and
  the import; `admin/installation.md`'s workers section (the Staff worker now
  reaches the tenant) and configuration table (`ENTRA_*` none: everything is in the
  database; the variables of A's release note).
- `docs/src/content/docs/{en,nb}/user/staff.md`: the Account tab's two accounts, the
  pass, the kind, the directory profile, the groups; a new `user/entra.md` for the
  wizard's screens as the interface names them; `user/workspace-administration.md`'s
  Single sign-on card.
- `contributing/module-boundaries.md`: rule 13, the slot under the many-provider
  slots, the checklist; `contributing/platform-packages.md` unchanged (nothing new is
  shared).
- `openapi/entra.yaml`, `identity.yaml`'s SSO operations and the SCIM schema, and
  `staff.yaml`'s groups, kind and profile; `docs/astro.config.mjs`'s `contracts`.
- `ROADMAP.md`: the Staff section's *Later* paragraph becomes `## Entra` after Staff
  with phase 1's five deliveries, and a *Later* paragraph (a certificate credential,
  mailbox and OneDrive, hybrid tenants, Lifecycle Workflows switches, Google
  Workspace, group-based licensing, delegated authorisation); Staff's *Later* keeps
  phase 2 and Staffing.

## Deliveries

| | Branch | Migrations | Holds |
| --- | --- | --- | --- |
| **A** — The settings move | `feat/identity-sso-settings` | `00058` (identity) | D1, D2 without the tile; the Single sign-on card; the import and its audit; `ErrModeMismatch` kept one release; docs |
| **B** — The announced account and the slot | `feat/staff-provisioner-slot` | `00059` (identity), `00060` (staff) | D3, D4, D5, D6 (the contract and slot, no provider), D7 (the table, with a **fake provider** in `modtest`), D8; Staff's Account tab; docs |
| **C** — The module | `feat/entra-module` | `00061` (entra: `connection`, `credentials`, `accounts`, `events`) | D11, D12, D13, D15 (the wizard and the tile); the fake Graph; the tagged test; docs |
| **D** — Groups and traits | `feat/entra-groups` | `00062` (staff), `00063` (entra) | D9, D10, D14; the group pages; docs |
| **E** — The erase and the leftovers | `feat/identity-erase` | none expected | D16; `admin/authentication.md`'s paragraph replaced |

A is independent of everything and first, since C's wizard embeds its forms. B is
independent of C and lands before it; C's provider is dropped into B's slot. D needs C.
E needs B. Each branch stacks on the previous when it must (`stacked-prs-per-phase`).

## Out of scope

A certificate credential and workload identity federation for Graph (E §3.5);
mailbox conversion to shared and OneDrive transfer (not in Graph); users synced from
on-premises Active Directory, beyond the report; administrative-unit scoping, national
clouds, delegated authorisation; a claims-mapping policy for an employee id claim; Entra
dynamic groups and role-assignable groups, ever; group-based licensing as a trait;
reading the tenant as a source of truth for anything but the account's own state;
Lifecycle Workflows switches per step; Google Workspace; photos; the SCIM `timezone`
attribute; a second OIDC provider; restoring a deleted account from Vantigo (the
portal does it within 30 days); Teams channels, apps and settings beyond the Team's
creation; Owners in the tenant (the module manages nobody who is an Owner in Vantigo,
S reading 20, and nobody who holds an Entra directory role).

## Testing

**A** (`identity`): the migration applies, rolls back and re-applies; a fresh row with
`OIDC_*` set imports once, audits the names, logs the warning, and a second mount
imports nothing; a written row with a variable set logs the leftover and keeps the
row; the session query, `UserMayAct`, sign-in and the bearer check follow a change
within the cache's 20 s on a second `Access` (two instances over one pool); the token
is answered once and its digest verifies a request, the previous one until its expiry
and not after; the secret is sealed, never in `GET`, and `PUT` without it keeps it; a
changed authority with links is 409 and 200 with the confirmation; the relying party
is rebuilt on the version and the sign-in flow completes against the fake provider
after a change; every write is audited without the secret or the digests; every
operation is 403 for a non-Owner and 401 for an Owner without MFA.

**B** (`identity`, `staff`, `contracts`, `module`): a SCIM create with the enterprise
block is 201 and answers it back, a PATCH on `…:department` 200, a filter on
`employeeNumber` finds it; an expected account is fulfilled by SCIM on each of the
three keys and by OIDC on `oid`, producing a managed, numbered, non-pending user;
`ReplaceUserID` completes the pending user, revokes its invitation `replaced` and
inserts no second user; no match is a plain user; `Expect` refuses an unmanaged
holder of the number; the kind migrates from the mode; `decide`'s third column for
every row of D7 with the fake provider, including provider `unconfigured`, the
announce after `Prepare`, the roles assigned once, `Disable now` on both links, the
rehire's pass, the deletion after the setting and never under a hold; the worker's
chain is provider then identity; the slot is collected from enabled modules only and
two providers with one key fail `Compose`; the accessor refuses a call inside a
transaction; every reported code becomes its item and none carries an address; the
directory profile's toggles shape `Fields`.

**C** (`entra`, against a fake Graph in `modtest` holding users, groups, members,
licences, passes, `$batch`, 429 with `Retry-After`, 400 `ObjectConflict`, 403 on a
role holder, 400 on a synced user): the token is fetched once and reused until five
minutes before expiry, and the next secret is tried on `invalid_client`; the test
reports the missing roles; `Prepare` finds by employee id, refuses two, creates
disabled with the UPN from the work email, folds `Ørjan Åsmundsen` to a legal UPN,
suffixes on conflict, writes the manager; `Activate` enables, writes the hire date and
reports when it cannot, assigns the SKUs and reports `CountViolation`, issues the pass
sealed, re-issues on `Fresh` only; `Deactivate` runs the steps in order, stops at
nothing, disables first; `Update` never rewrites the employee id; `Delete` refuses
enabled; `RevealPass` answers once; a 429 waits the header; every event row carries
codes and the UPN only; the wizard's forms validate as the connection's rules say;
the tile is absent without `entra:manage`; the tagged test against the tenant runs
the whole lifecycle on a prefixed user and group and removes them.

**D**: the rule materialises each kind's set and the difference is written with
events; a person erased leaves every group; the traits form renders from the fake
provider's declaration; `EnsureGroup` names the service principal and the owners,
refuses a type change, teamifies only after fifteen minutes with a person owner,
refuses a dynamic group; `SetMembers` adds in twenties, removes with `$ref`, answers
the failures; licences follow the union of the groups' traits; an archived group is
deleted after the setting or renamed.

**E**: `Erase` refuses enabled, unmanaged and privileged users, deletes the user with
their rows, pseudonymises the audit snapshots and keeps the rows, and Staff's run calls
it after its commit once per person.

**Frontend** (`apps/entra/frontend`, `apps/staff/frontend`, `apps/host/frontend`,
vitest): the wizard's six steps and their *Done* states from a fake `/connection`; the
status card's missing permissions; the secrets list; Staff's Account tab with two
links, the pass dialog shown once, the kind select and its refusal; the directory
profile table; the group page's trait form from a declaration; the Single sign-on
card; `apps.test.ts` counts the module.

**Gates**: `mise run server:check`, `server:test`, `frontend:check`, `docs:check`.

## Readings on the record

1. **The row wins over the environment** once written; the environment imports once
   and is refused a release later (E §6.3).
2. **The SCIM token is generated by the server** and only its digest kept; an Owner
   who wants to choose the token's value cannot.
3. **The kind is per person**, the installation's default a Staff setting; `Mode()`
   survives for the `external` kind only.
4. **The announced account is matched on three keys** in order — employee number,
   UPN, object id — because Entra's default SCIM mapping sends the mail nickname as
   `externalId`.
5. **The pass is shown to HR once by default** and never mailed; the manager gets a
   notice, not the pass.
6. **The leaver is disabled the day after the end** (Staff's rule, S reading 15), and
   the leave date written is the end day's 23:00 so a tenant's own workflow sees a
   past trigger.
7. **An archived group is renamed, not deleted, when deletion is never**, so the one
   setting means one thing.
8. **`employeeHireDate` is written and its refusal reported**, until the tagged test
   says it is never writable app-only, when `write_hire_date` defaults to off.
9. **Direct licences only**: the module assigns and strips what it assigned; a
   licence a group gave is the tenant's.
10. **The service principal owns every group it makes**, beside the people, since an
    ownerless app-made group is unmodifiable.
11. **One app registration** is what the wizard proposes; two work.

## Open questions

1. The pass: shown to HR, a notice to the manager, or both by default; usable once?
2. An archived group: renamed (as read 7) or deleted after the setting?
3. Drop `employeeHireDate` from the first version, or keep it as reading 8?
4. The default kind once the module is connected: `entra` for every new person, or
   `local` with HR opting in?
5. Should `Erase` pseudonymise the audit snapshots, or leave the audit untouched as it
   is for every deleted user today?
