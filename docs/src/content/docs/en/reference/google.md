---
title: "Google Workspace module"
description: "Google Workspace as a connection to one organisation through a service account's domain-wide delegation — the foundation: the module and why it requires Staff; the schema; the page and its four tabs; the permission and the Owner policy; the connection, the admin to impersonate and the credential kind under If-Match; the key file's rules, the keys in the plural, their try order and a refused key; the Internal web client and its secret; the guarded client, its fixed hosts and its limits; the delegated token, its cache and the one seam; Test connection, the needed and the unexpected scopes, and the statuses and their reasons; the events; the refusals and the endpoints; and the unverified items U2 and U14 — and the accounts (delivery B): the provisioner google and its status, the accounts and groups tables and the account rules, the correlation on the organization external id and the adoption, the address, the mapping, the first password, the licences, the leaver's steps, the delete, the groups' three traits, the photo at 96 px, and the unverified items U11, U12, U13, U16 and U19 — and connecting existing users (delivery C): the directory search identity serves, its two prefix queries, the exact matches by primary email, alias and employee number, their bounds, the announced link keyed by the Directory id, the Sign-in and Provisioning steps, and the unverified items U1 and U4."
sources:
  - apps/server/internal/google
  - apps/google/frontend
  - apps/host/frontend/src/routes/workspace/google
  - apps/host/frontend/src/routes/workspace/google.tsx
  - openapi/google.yaml
  - apps/server/internal/openapi/specs/google.yaml
  - apps/server/internal/db/migrations/00114_google_baseline.sql
  - apps/server/internal/db/migrations/00115_google_accounts.sql
  - apps/server/internal/directoryaddress
---
The Google Workspace module connects one installation to one Google Workspace
organisation through a **service account with domain-wide delegation**: a JWT signed with
the service account's key, naming the admin it acts as and the scopes it asks for, is
exchanged at Google's token endpoint for a token that acts as that admin. It is a
vertical-slice module inside the single Vantigo binary (`apps/server/internal/google`),
owns the `google` schema in the shared PostgreSQL database, and serves
`openapi/google.yaml` under `/api/v1/google`. Its frontend package is
`apps/google/frontend` (`@vantigo/google-ui`); it owns no app and no tile in the switcher,
and its page is **Google Workspace** under **Integrations** in Workspace admin
([the page](#the-page)).

**Deliveries A, B and C.** The module holds the connection — the service account's keys,
the admin to impersonate, the Internal web client — tests it, and reads the organisation
and its verified domains (A). It fills `contracts.IdentityProvisioner` under the key
`google` ([the provisioner](#the-provisioner)): Staff's accounts worker drives a person's
account and Staff's groups in the organisation through it (B). It fills
`contracts.DirectorySearch` under the same key ([the directory
search](#the-directory-search)): identity's Owner endpoints search the organisation's
users and propose exact matches for connecting existing users to Google sign-in (C). The
person's own Google
connection, the calendar and Gmail come in later deliveries, each taking its token from
the one seam [`tokenSource`](#the-delegated-token). `MODULES` refuses `google` without
`staff` (`google requires staff`): Staff's accounts worker is the provisioner's only
caller.

Nothing in the module imports another module — depguard enforces it, tests included —
and no SQL of it crosses a schema. It has no background worker. It reaches the internet
in one place, the [guarded client](#the-guarded-client), and only fixed hosts there. The
design is [`docs/superpowers/specs/2026-11-03-google-workspace-design.md`](https://github.com/vantigo-io/vantigo/blob/main/docs/superpowers/specs/2026-11-03-google-workspace-design.md),
G12 to G16.

## The schema

One migration, `00114_google_baseline.sql`, creates the `google` schema and its three
tables. No table references another schema; the user who acted is an opaque id.

- **`connection`** — the installation's one connection, a single row seeded by the
  migration (`id` 1, `CHECK (id = 1)`):
  - `credential_kind` (`service_account_key` by default; `service_account_key` or
    `vantigo_hosted`, `CHECK`ed — the second is reserved and refused by the API);
  - `admin_subject` (254), the admin the Admin SDK calls impersonate;
  - `oauth_client_id` (200), the Internal web client's id;
  - what the last [test](#test-connection) found: `customer_id` (40), `primary_domain`
    (253), `domains` (the verified domains), `organisation_name` (256),
    `scopes_granted`, `scopes_missing`, `scopes_unexpected`, `status` (`unconfigured`,
    `pending_delegation`, `connected` or `failing`, `CHECK`ed; `unconfigured` by
    default), `status_reason` (60, a code) and `last_checked_at`;
  - `version` (from 1), the `If-Match` of `PUT /connection`, and `updated_at` and
    `updated_by_user_id`.
- **`credentials`** — the service account's keys and the web client's secret: `id` (a
  `bigint` identity from 1001), `kind` (`service_account_key` or `oauth_client_secret`,
  `CHECK`ed), a key's four clear fields `client_email` (254), `client_id` (40, the
  numeric id delegation is granted to), `private_key_id` (64) and `project_id` (64),
  `sealed` (the private key or the secret, sealed), `created_at`, `created_by_user_id`,
  `last_used_at`, `last_failed_at` and `revoked_at`. A row is revoked, never deleted.
- **`events`** — the module's append-only trail ([the events](#the-events)): `at`, exactly
  one of `actor_user_id` and `actor_system` (`CHECK`ed), `action` (40), `subject_id` (64,
  a Google id, never an address) and `codes`; indexed on `at`.

The migration's down step drops the schema whole; nothing at Google is touched.

A second migration, `00115_google_accounts.sql` (delivery B), adds:

- **`accounts`** — the users the module made or found for Staff's people, keyed by
  `user_id` (32, the Directory id): `employee_number` (16, unique case-folded),
  `primary_email` (254, never renamed), `state` (`disabled`, `enabled` or `deleted`,
  `CHECK`ed), the first password `pass_sealed` (under `google/pass`) with
  `pass_expires_at` and `pass_revealed_at` (`CHECK`: no password without its expiry),
  `created_by_us`, `licences_assigned` (the `productId/skuId` the module assigned),
  `reported` (codes), `last_error` (60), `last_synced_at`, `adoption_confirmed_at` and
  `adoption_confirmed_by_user_id`, `created_at`, `updated_at`.
- **`groups`** — the groups the module made or found for Staff's groups, keyed by
  `group_id` (32, the Directory id): `staff_group_id` (64, unique), `email` (254, unique
  case-folded), `created_by_us`, `adoption_confirmed_at`, `adoption_confirmed_by_user_id`,
  `created_at`, `updated_at`.
- **`connection`** gains the account rules: `address_domain` (253), `address_pattern`
  (`given.family` by default, or `g.family` or `given`), `org_unit_path` (400, `/` by
  default, starting with `/`), `pass_lifetime_days` (7; 1 to 30), `pass_delivery` (`hr`;
  `hr`, `manager` or `both`), `leaver_remove_licences` (true), `leaver_remove_groups`
  (`managed`; `all`, `managed` or `none`), `leaver_sign_out` (true), each `CHECK`ed as
  Entra's are, and `skus`, the licences as last read (at most 200 of `productId`, `skuId`,
  `name`, `assigned`).

## The page

The page is the host's layout route `/workspace/google`, with four child routes rendered
as tabs under one header, each tab a link (`googleTabs` in `@vantigo/google-ui`):

| Route | Tab | Component | Holds |
| --- | --- | --- | --- |
| `/workspace/google` | Status | `GoogleStatusTab` | A *Not connected yet* notice while unconfigured, the **Status** card — status and reason, organisation, customer id, primary and verified domains, last test, the scopes granted, missing and unexpected — and **Recent events** |
| `/workspace/google/connection` | Connection | `GoogleConnectionTab` | The six steps: the project, the service account and its key; upload the key; delegate the scopes; the admin to impersonate; **Test connection**; the Internal web client |
| `/workspace/google/accounts` | Accounts | `GoogleAccountsTab` | The account rules; once a test has read the organisation, **Sign-in** and **Provisioning**, identity's single sign-on card handed in by the host ([the prefill](#the-sign-in-prefill)); the licences; the accounts and the groups |
| `/workspace/google/calendar-gmail` | Calendar and Gmail | a placeholder | One sentence: Calendar and Gmail are coming |

- **The gate.** The workspace layout admits a holder of `google:manage` while `google` is
  in `MODULES`; the layout route asks again: the module on, otherwise the not-enabled
  page in place, and `google:manage`, otherwise a redirect home. The Owner's steps stay
  the Owner's inside the tabs; the tab bar hides nothing by role. The sidebar item is
  `module: "google"`, `requiredPermissions: ["google:manage"]`, under *Integrations*
  beside Microsoft Entra ID's; the avatar menu lists it under the same flags.
- **The way in.** The index route redirects to `/workspace/google/connection` while the
  connection is `unconfigured` and no key is live (`opensOnConnection`), and opens
  *Status* otherwise.
- **The marks** (`stepsDone`): the key — a live key; the admin — an admin named; the
  test — the last test `connected` with no scope missing; the web client — an id and a
  live secret. Steps 1 and 3 happen at Google and carry no mark.
- **The key file** is read in the browser (at most 8 KiB, the server's own cap) and sent
  as text; it never leaves the request.

## Permissions

| Key | Name | Grants | Sensitive | Delegable |
| --- | --- | --- | --- | --- |
| `google:manage` | Manage Google Workspace | The page under **Integrations** in Workspace admin; the connection's status and credentials (without their values), and the events. Later deliveries add the accounts and the account rules. | yes | **no** |

It is in the category "Administration", Entra's `entra:manage` twin. The Owner holds it
through the wildcard; anyone else needs a role that grants it, and because it is not
delegable, only an Owner can grant it.

**The connection itself is the Owner's.** Every write — the admin, the keys, the web
client — and the test are under `policy:Owner+OwnerManagement`, as identity's single
sign-on settings and Entra's connection are: an Owner, signed in with a second factor
while `OWNERS_REQUIRE_MFA` is on; anyone else, an Owner without the factor included, is
refused 403. The five share one rate limit, `google-owner`: 30 requests a minute per
client address, identity's user-management figures. The page decides what to show from
identity's session: anyone but an Owner sees the steps read-only.

## The connection

`GET /connection` answers the whole connection from the module's rows; nothing reaches
Google. Beside the row's fields it answers:

- `status` and `statusReason`: **`unconfigured` while no key is live or no admin is
  named**, whatever the last test wrote; the row's own otherwise;
- `credentials`: every key and secret, newest first, without its value, the ones in use
  marked `inUse`;
- `delegationClientId`: the numeric client id of the key in use — what the Admin console's
  domain-wide delegation takes;
- `scopesToDelegate`: the [needed scopes](#test-connection), written in full; the page
  joins them with commas, ready to paste;
- `redirectUris`: the two URIs to register on the Internal web client, `APP_URL` +
  `APP_BASE_PATH` + `/api/v1/google/connect/callback` (the person's connection, a later
  delivery) and + `/api/v1/identity/oidc/callback` (identity's single sign-on with
  Google as the provider); none without `APP_URL`;
- `oauthClientSecretAddedAt`: when the live client secret was added.

**`PUT /connection`** sets `adminSubject` and `credentialKind` under `If-Match` on the
connection's `version` (bare, quoted or weak); a field left out keeps its value. The admin
is an address (at most 254 characters, a local part, an `@` and a domain of two labels or
more), lower-cased. `credentialKind` is `service_account_key`; `vantigo_hosted` is
refused `422 credential_kind_unsupported`, anything else 400. A stale version is
`412 version_mismatch`; an `If-Match` that is not a version is 400. **A changed admin**
sends the status back to `unconfigured` and forgets the scopes and the time of the last
test, which were another admin's. The same values again write nothing; otherwise the
version moves on and the event `google.connection.changed` names what changed —
`admin_subject_changed`, `credential_kind:<kind>` — never the address.

### The credentials

**The upload.** `POST /connection/credentials` takes `{json}`, the key file's text. The
server parses it under these rules, each failure a sentence on the `json` field (400)
that never repeats the file:

- at most 8 KiB (Google's are about 2.4 KiB);
- JSON, with `type` `service_account` and `token_uri` `https://oauth2.googleapis.com/token`;
- `client_email` an address, `client_id` 1 to 40 digits, `private_key_id` hex,
  `project_id` present;
- `private_key` one PEM block, `PRIVATE KEY`, a PKCS #8 RSA key.

The private key is sealed at once under the secrets purpose
**`google/service-account-key`**; `client_email`, `client_id`, `private_key_id` and
`project_id` are kept in the clear; nothing else of the file is kept. The key is never
answered, logged or audited. The answer is 201 with the credential.

**Keys in the plural.** Several keys may be live, so a key is rotated without a gap.
Under the connection's lock, **a key of another service account is refused
`422 client_mismatch` while any key of the current one is live**: delegation is granted
to a client id, so changing the service account means revoking every key first. Keys
carry no expiry in the file and none is stored.

**The try order.** The live keys — not revoked — are tried newest first, except that a
key Google refused since it last fetched a token (or that never fetched one) goes after
every other. A key is opened for each token request and never held open. When Google
refuses a key (`invalid_grant` or `invalid_client` on its JWT, or the sealed key does not
open under this `APP_SECRET`), it is marked `last_failed_at`, recorded as
`google.credential.refused` by the system actor `system:google` — once: a key already
refused since it last worked is not recorded again — and the next is tried.
**When every key is refused** the connection is set `failing`, `credential_invalid`. A
refused scope is not a refused key, and neither is a refused subject (below): the next
key is the same service account's and is not tried, nothing is marked and the
connection's status is left alone. `last_used_at` is written when a key fetches a token,
at most once a minute — and at once when the key was refused since it was last used.

**`DELETE /connection/credentials/{id}`** revokes a key or a client secret: kept, marked
`revoked_at`, never used again, a key's cached tokens forgotten. Nothing changes at
Google. One already revoked answers as it is; an unknown id is 404. Revoking the last
live key leaves the connection `unconfigured` on its next read.

**The Internal web client.** `PUT /connection/oauth-client` takes `{clientId,
clientSecret?}`. The id is the project number, a hyphen, a random part and
`.apps.googleusercontent.com` (at most 200 characters); the secret at most 1 000. A
changed id needs its secret (400 otherwise); a secret alone rotates the current
client's, sealed under **`google/oauth-client-secret`** as a new `oauth_client_secret`
credential, the older ones revoked. Either moves the connection's version on; the same
id without a secret writes nothing. The secret is never answered. Google shows a client
secret only at creation, and the page says so. The event is `google.oauth_client.changed`.
Nothing in this delivery uses the web client.

## The guarded client

Every call to Google — the token exchange included — goes through one client
(`client.go`):

- **Fixed hosts.** A request is refused before it is sent unless it is `https`, with no
  user, no port and no dot segment, to one of: `oauth2.googleapis.com` (the token
  endpoint), `admin.googleapis.com` (the Directory), `licensing.googleapis.com`,
  `www.googleapis.com`, `gmail.googleapis.com`, `groupssettings.googleapis.com` and
  `accounts.google.com`. The hosts are constants: no setting reaches them, and a test's
  fake comes in through `Deps.HTTPTransport`, never as a base URL. Deliveries A and B call
  the first four; Groups Settings is called on `www.googleapis.com/groups/v1/groups`
  ([U12](#unverified-items)).
- **The transport** dials through netguard, so every address is checked against the one
  table of forbidden addresses; no proxy, and no redirect is followed.
- **Budgets.** 30 seconds per call; a 429 waits `Retry-After` once, at most 10 seconds
  (a longer one fails at once); a 5xx is retried once after a second; a 401 drops the
  token and retries once with a fresh one. Every answer is read through a 1 MiB cap. A
  create (`users.insert`, `groups.insert`, a licence's insert) is made once: its 5xx is
  the answer, never a second create.
- **Bodies** are JSON. A Licensing path carries the user's primary email; the log names
  it as `{user}`, and a Groups Settings or group-by-address path as `{group}`, so no
  address reaches a log line.
- **Google's words are never read.** A refusal is the HTTP status and the reason code of
  Google's error body (`error.errors[0].reason`, else `error.status`, else the OAuth
  error); its message is never logged, stored or answered. A 403 reads as the admin
  refused, a 404 as not found, a 429 as throttled, a 5xx as Google unreachable.
- **No `google.golang.org/api` dependency.** The calls are few and JSON.

## The delegated token

`tokenSource(scopes, subject)` is the one seam every call takes its bearer token from.
The token is fetched by `golang.org/x/oauth2/jwt` — the service account's address, the
key and its id, the scopes, the subject — at `https://oauth2.googleapis.com/token`, with
the guarded client handed in the context as `oauth2.HTTPClient`, so the exchange goes
through the host guard and netguard too. Tokens are cached in the process per (key — its
row and a digest of its sealed value —, subject, scope set) until five minutes before
they expire; the test always asks a fresh one, and keeps none of its probes' tokens for
scopes Vantigo never uses. A later credential kind, `vantigo_hosted`,
fills the same seam without a key.

The token endpoint's answers map to: `invalid_grant` whose description names the subject
(*Invalid email or User ID* and the like: an address Google does not know, or a suspended
user) — the subject refused, the admin's problem in a test and that person's in a later
delivery's call; the description is read for this alone and never logged or kept; any other
`invalid_grant`, or `invalid_client` — the key refused; `unauthorized_client` or
`access_denied` — the scope not delegated to the client id ([U14](#unverified-items));
a 429 or a 5xx retried once; anything else Google unreachable.

## Test connection

`POST /connection/test` asks Google, as the admin to impersonate:

1. **One fresh token per needed scope.** In this delivery:
   `https://www.googleapis.com/auth/admin.directory.customer.readonly` and
   `https://www.googleapis.com/auth/admin.directory.domain.readonly`. Each later
   delivery adds its rows; the Gmail scopes join only while its Mail channels switch is on.
2. **One token per unexpected scope** — scopes Vantigo never uses that an over-wide
   delegation is likely to carry; a token is the only way to learn what is delegated:
   `https://mail.google.com/`, and under `https://www.googleapis.com/auth/`
   `gmail.readonly`, `gmail.modify`, `gmail.send`, `calendar`, `calendar.events`,
   `admin.directory.user.security`, `drive` and `cloud-platform`. The Gmail scopes are
   unexpected while there is no Mail channels switch. No calendar scope is ever delegated:
   calendar sync will run on each person's own connection. A probe's other failure is not
   reported.
3. **The organisation**: `customers.get(my_customer)` and `domains.list` on the
   Directory, each with the token of its scope when it was granted. It stores
   `customer_id`, `organisation_name` (cut to 256 characters), the verified domains
   (lower-cased, sorted) and the primary one (the customer's domain when none is marked
   primary).

What it found is written beside the connection's settings and leaves the version alone,
so an open form is not refused for it — and only over the version the test read, in one
transaction with its event: when the admin changed during the test, the test is answered
but neither written nor audited. It answers 200 whatever it found
(`{status, reason?, granted, missing, unexpected, customer?, domains}`). Without a live
key or an admin it is `unconfigured` and asks Google nothing.

### The statuses and their reasons

| Status | Reason | When |
| --- | --- | --- |
| `unconfigured` | — | No live key, or no admin named; also right after the admin changes. |
| `pending_delegation` | `not_delegated` | Every needed scope was refused: the delegation is not entered, or not applied — it can take up to 24 hours, and with multi-party approval a second super admin must approve it. |
| `connected` | — | At least one needed scope granted; the refused ones are listed in `scopesMissing`, and what needs them is unavailable. Unexpected scopes do not change it. |
| `failing` | `credential_invalid` | No key fetched a token: every key refused. Also set by any call that finds every key refused. |
| `failing` | `admin_refused` | Google does not know the admin (the subject refused), or the Directory answered 403: the admin may not read the organisation, or the Admin SDK API is off in the project. |
| `failing` | `google_unreachable` | Google did not answer, answered nonsense, or a 5xx twice. |

The event is `google.connection.tested`, with the customer id as its subject.

## The provisioner

`provisioner.go` fills `contracts.IdentityProvisioner` under `Key() = "google"`, display
name *Google Workspace*, registered as `module.Module.IdentityProvisioner`; Compose and
Workers collect it into `Deps.IdentityProvisioners` beside Entra's, and Staff drives it
exactly as it drives Entra (boundaries rule 13). Every call to the Directory is made as
`admin_subject` with `admin.directory.user` (users), `admin.directory.group` (groups and
members), `apps.groups.settings` (Groups Settings) or `apps.licensing` (licences), on no
transaction; the rows and the event follow in short transactions. A token Google will
not issue — no key, every key refused, the admin unknown, the scope not delegated — is
`ErrProviderUnavailable`, nothing written. The module has **no worker**: Staff's accounts
worker reads every link's `Account` on its own cycle, which is what tells Staff an
account was suspended or deleted outside Vantigo.

**`Provider`** reads the rows, never the network: `unconfigured` while A's status is
(no live key or no admin) and, with the reason `not_delegated`, while it is
`pending_delegation`; `failing` with A's reason when the last test failed, or
`scopes_missing` while the last test's granted scopes lack one of the four above;
`connected` otherwise. Every write but `Deactivate` needs `connected`.

**Correlation.** A person's account is the user holding their employee number as an
external id of the type `organization`: `users.list(customer=my_customer,
query=externalId:<number>)`, each answer filtered to an `organization` id equal to the
number, ignoring case ([U11](#unverified-items)). Two holders are
`ErrExternalAmbiguous`; a holder with `isAdmin` or `isDelegatedAdmin` is
`ErrExternalPrivileged` (reported `privileged`), and so is every later write to one, as
is a 403 on a write to a user. One the module did not create is linked
`created_by_us false` and adopted untrusted: `adoption_unconfirmed` reported, nothing
written, every write `ErrExternalUnconfirmed`, until `POST /accounts/{userId}/confirm`.
A row naming a user the organisation no longer holds, or a deleted one, is forgotten so
a rehire gets a new user.

**Prepare** creates the user with `users.insert`: `primaryEmail` by the shared address
rule ([`directoryaddress`](/en/contributing/platform-packages/#the-directory-address-rule),
the mailbox alphabet: the work email on a verified domain, else the pattern at
`address_domain` or the primary domain, 2 to 99 on a 409 — a taken work email is
`ErrExternalAddressTaken`), a random 32-character password nobody sees,
`changePasswordAtNextLogin: true`, `suspended: true`, `orgUnitPath`, `externalIds`
`[{type: organization, value: <number>}]`, and the mapping below. The create is made
once; when its answer is lost, and on every 409 before the next address is tried, the
user at the address asked for is read back with `users.get` — strongly consistent, where
the number's search may lag a create — and taken as this call's (`created_by_us`) when it
holds the number as its organization id, is suspended and its `lastLoginTime` is still
the epoch: a lost answer never makes a second user.

**The mapping** writes only what Google has: the names (`name.givenName`,
`name.familyName`, at most 60 each), and of the spec's fields `jobTitle`, `department`
and `costCenter` (the primary entry of `organizations`), `businessPhone` and
`mobilePhone` (`phones` of type `work` and `mobile`), `officeLocation` (`locations` of
type `desk`, as `area`), the manager (`relations` of type `manager`, the manager's primary
email — `manager_not_found` when the organisation holds no such user), and
`preferredLanguage` (`languages`, `no` or `en-GB`). Entries of other types are written
back as they were. `employeeHireDate`, `employeeLeaveDateTime`, `employeeType`,
`companyName` and the division are ignored and **not reported**. **Update** patches the
attributes that differ, never the external id or the primary email, and on an active
user reconciles the licences.

**Activate** lifts the suspension (`suspended: false`). A password is wanted with it —
when no password waits unrevealed, or on HR's *Run now* (`Flags.Fresh`) — and on *Run
now* for an active user; it is set **only when the user's `lastLoginTime` is still the
epoch**: a fresh 16-character password (`directoryaddress.ReadablePassword`) with
`changePasswordAtNextLogin: true`, sealed under **`google/pass`** with
`pass_expires_at = now + pass_lifetime_days`. On a user someone has signed in to — an
administrator's suspension, a rehire's old account, a confirmed adoption — none is set,
whatever Staff asked: the suspension is lifted alone and `pass_not_set_used` reported
(Activate judges it, so the next Activate that wants no password clears it), for HR or an
administrator to reset the password by hand. The password is sealed and stored **before**
the `users.patch` that sets it; when Google does not set it the row is put back — the
earlier unrevealed password kept, or the new one cleared — and `google.pass.issued` is
recorded only after Google accepted it. A 400 on the password is `pass_not_allowed`, the
suspension lifted without it. **`RevealPass`** answers it once, taken and cleared in one statement,
audited `google.pass.revealed` with no value; a second reveal, or one after the lifetime
(every expired password is cleared first), is `ErrPassGone`.

**Licences** are `productId/skuId`. For each one the spec names that the module has not
assigned, `licenseAssignments.get` (404 not held) then `insert` with the user's primary
email; any refusal of the insert is `licence_unavailable:<skuId>`. A licence the module
assigned that no group names any more is deleted; an administrator's is left.
`GET /connection/skus` reads `listForProduct` for `Google-Apps`, `101001`, `101005`,
`101033` and `101034` with the customer id, counts the assignments by SKU, counts a
product Google refuses as none held, and caches at most 200 on `connection.skus`.

**Deactivate** runs whatever the status says, on any working key: `suspended: true`
always and first, the waiting password cleared, then in order as the account rules turn
them on — `remove_licences` (every licence the module assigned and every cached SKU the
user holds; U16), `remove_groups` (`groups.list(userKey)`'s groups, all or the module's
own; a membership gone is done) and `sign_out` (`users.signOut`; a 4xx is reported
`sign_out_refused` and the rest goes on, U19). A step that fails is
`leaver_step_failed:<step>`, the rest left to the next call. **Delete** is
`users.delete` of a suspended user (`ErrExternalEnabled` otherwise), the row marked
`deleted`; Google keeps it restorable for 20 days. It is called by Staff's
`deleteExternalAfterDays`; the module has no delete setting of its own. **`Account`**
is `users.get`: `suspended` is `disabled`, a 404 is `nil`.

**Groups.** `GroupTraits` declares, from the rows alone:

| Trait | Type | Rule |
| --- | --- | --- |
| `email` | string (254) | the group's address; empty for `<group key>@<address domain>`; its domain one of `domains` (`trait_invalid`); `trait_immutable` once the group exists. Not *required* in Staff's sense, since empty has its default. |
| `visibility` | choice | `domain` (default): `whoCanViewGroup` and `whoCanViewMembership` `ALL_IN_DOMAIN_CAN_VIEW`, `showInGroupDirectory` true; `members`: `ALL_MEMBERS_CAN_VIEW`, false. |
| `licences` | list | the cached SKUs, `productId/skuId`, labelled with their name and the count assigned. |

`EnsureGroup` updates the group its row names (`groups.patch` for the name and the
description, Groups Settings, the owners), or adopts the group Google holds at the
address — `ErrGroupUnconfirmed`, nothing written, until `POST /groups/{groupId}/confirm`,
after which it is reconciled like the module's own (the name, description and visibility
overwritten, unnamed owners lowered, unlisted members removed);
`ErrGroupNotManageable` when another Staff group's row names it — or creates it with
`groups.insert` (a 409 is `ErrGroupNotManageable`). Owners are members with the role
`OWNER`: one missing is added or raised, one no longer named lowered to `MEMBER`.
`SetMembers` diffs `members.list` against the ids, adds the missing as `MEMBER`
(`not_found`, `refused` or `other` per member) and removes the users not named — never an
owner, never a member that is not a user. `DeleteGroup` deletes a group the module made
(`groups.delete`) and forgets one it found (`kept_not_ours`).

**The photo.** `SetPhoto` decodes the JPEG or PNG (at most 4096 px a side), crops its
centre square, scales it to **96 × 96** with Catmull-Rom over white, encodes a JPEG and
writes `users.photos.update` with `photoData` in Google's web-safe base64 (`+` as `-`, `/`
as `_`, `=` as `*`), `mimeType` `JPEG`. A 400, 403, 404 or 413, or a picture that cannot
be decoded, reports `photo_not_written` and answers nil ([U13](#unverified-items));
`ClearPhoto` is `users.photos.delete`, a 404 done. Both refuse an administrator
(`ErrExternalPrivileged`, nothing written), as every write does.

## The directory search

`directory_search.go` fills `contracts.DirectorySearch` under `Key() = "google"`,
`DisplayName()` "Google Workspace", registered as the module's `DirectorySearch`; Compose
collects it into `Deps.DirectorySearches` (Workers does not). Identity serves it — the
module adds no endpoint — while `google` is the configured OIDC provider:
`GET /api/v1/identity/owner/sso/directory/users?search=` and
`GET /api/v1/identity/owner/sso/directory/matches`, under identity's rules (an Owner,
60 searches and 6 reviews a minute, 409 `no_directory`, 503 `provider_unreachable`;
[the announced link](/en/admin/sso-scim/#the-announced-link)).

**The footing.** Each read takes the connection's row on the pool: the admin to
impersonate and the verified domains. A connection naming no admin or holding no live key
fails `contracts.ErrDirectoryUnavailable`, as does a token Google will not issue (the key,
the admin, or `admin.directory.user` not delegated), a timeout, throttling or a 5xx;
identity answers it 503. The calls go as the admin with `admin.directory.user`, on no
transaction. What the reads need of identity — the users not yet connected and who holds
an account — comes through `contracts.SSOConnections` with the request's context, so
identity's own gate applies. A search, an address or an employee number never reaches a
log line (the client logs host and path, never the query), an event or an error; the reads
write no event.

**The keys.** An account answers `ObjectID` its Directory id (digits), `PrincipalName`
and `Mail` its primary email, `DisplayName` its full name (the primary email when it has
none), `EmployeeNumber` its first `organization`-typed external id, `Enabled` not
suspended, and `Guest` false — Google's directory has no guests. A pick fills identity's
announced link with the Directory id as `objectId` and the primary email as
`userPrincipalName`; identity keys a Google sign-in by the ID token's `sub`
([U1](#unverified-items)), and a sign-in whose `sub` is another id does not fulfil the
link by its verified email.

**`Users`** strips `'`, `"`, `\` and `*` from the search — so `O'Brien` is searched as
`OBrien` — and asks nothing when nothing is
left. It then asks `users.list(customer=my_customer, projection=basic, viewType=admin_view,
maxResults=20)` twice, with `query=name:'<s>*'` and with `query=email:'<s>*'` — the full
name and the primary email or an alias starting with the search. Google documents no `OR`
between clauses, so the two are always asked apart. The answers are merged by id,
ordered by full name then id, cut to the limit (20 at most; identity asks 25), and each
marked with its holder from `SSOConnections.KeyHolders`.

**`Matches`** reads `SSOConnections.Unconnected(1001)` — `truncated` past 1000, as
Entra's — then the organisation's users once, `users.list(customer=my_customer,
projection=basic)` in pages of 500, at most 20 pages (10,000 accounts). An account's
addresses are its primary email, its `aliases` and its `nonEditableAliases`; the
`emails[]` entries — addresses an administrator typed for the person, on any domain (a
home address, or a work address the account does not receive) — are not the account's
own and match nobody. A candidate's key matches an account when:

| Key | Rule | `matchedBy` |
| --- | --- | --- |
| The address | equals the primary email, trimmed, ignoring case | `mail`, `principalName` |
| The address | equals an alias, the same way | `mail` |
| The employee number | equals the organization external id, trimmed, ignoring case | `employeeNumber` |

The rule is Entra's, exact only: a candidate is proposed when their keys hit exactly one
account that no other candidate's key hit and that nobody else holds (the candidate's own
SCIM mapping does not block it); two accounts, or one account two candidates hit, is
ambiguous and counted. An address identity made up and an empty number match nothing.
Past 10,000 accounts the answer is `truncated` and **the employee number is not used**:
an address belongs to one user in a Google organisation, so an address match stays exact
over a partial read, while a second account holding the same number could be in the pages
not read. The whole read is bounded by two minutes, past which it fails
`ErrDirectoryUnavailable`; a second request of the same Owner while the first reads
shares its answer.

### The sign-in prefill

The Accounts tab's **Sign-in** step, an Owner's and shown once the connection has a
customer id, renders the host's `SsoSettingsCard` (`sections: ["oidc"]`) with
`defaults: {provider: "google", authority: "https://accounts.google.com", clientId:
oauthClientId, allowedDomains: domains}`; without a web client id it asks for one on the
Connection tab. `SsoDefaults.allowedDomains` is applied with the rest, and only to a form
with OIDC off and no authority or client id saved — never over a saved value. The Owner
types the web client's secret into identity's card; identity seals its own copy, and no
secret crosses modules. **Provisioning** renders the card's SCIM half, says Google sends
no users to Vantigo, and links to Staff's default account kind.

## The events

Every write the module makes is a row in `google.events`, read newest first by
`GET /events?limit=` (1 to 200, 50 by default; outside that a 400) on the **Status** tab:

| Action | Written by | Codes |
| --- | --- | --- |
| `google.connection.changed` | `PUT /connection` | `admin_subject_changed`, `credential_kind:<kind>` |
| `google.connection.tested` | `POST /connection/test` | the status, the reason, `missing:<scope>`, `unexpected:<scope>`; subject the customer id |
| `google.oauth_client.changed` | `PUT /connection/oauth-client` | `client_id:<id>`, `client_changed`, `secret:<row id>` |
| `google.credential.added`, `google.credential.revoked` | the upload and the revoke | `credential:<row id>`, `kind:<kind>`, `key_id:<id>`, `client_id:<numeric id>`; subject the numeric client id |
| `google.credential.refused` | a call that found a key refused, by `system:google` | as above |
| `google.settings.changed` | `PUT /connection/settings` | the names of the rules that changed |
| `google.user.prepared`, `.activated`, `.updated`, `.deactivated`, `.deleted` | the provisioner, by Staff's actor | `created`, `found`, `enabled`, `suspended`, `pass_issued`, `changed:<attribute>`, `licences_assigned:<n>`, `licences_removed:<n>`, `groups_left`, `signed_out`, `gone`, the codes reported, `refused:<code>`; subject the user id |
| `google.user.photo_set`, `google.user.photo_cleared` | `SetPhoto`, `ClearPhoto` | `photo_written`, `photo_cleared`, `refused:photo_not_written` |
| `google.pass.issued`, `google.pass.revealed` | `Activate`, `RevealPass` | `expires_at:<instant>` — never the password |
| `google.account.adoption_confirmed` | `POST /accounts/{userId}/confirm` | — |
| `google.group.created`, `.adopted`, `.updated`, `.members_set`, `.deleted`, `.adoption_confirmed` | the groups' calls and `POST /groups/{groupId}/confirm` | `visibility:<v>`, `owners_added:<n>`, `owners_removed:<n>`, `added:<n>`, `removed:<n>`, `failed:<n>`, `kept_not_ours`, `gone`, `refused:<code>`; subject the group id |

An event names a user or a system, exactly one. It never carries an address, a key, a
secret, a token or Google's words.

## Refusals

A refusal is an RFC 9457 problem, `GoogleProblem`, whose `code` names the rule:

| Code | Status | When |
| --- | --- | --- |
| field errors under `errors` | 400 | A field broke its rule, keyed by the request's camelCase field (`json`, `adminSubject`, `credentialKind`, `clientId`, `clientSecret`, `limit`, `If-Match`). |
| `client_mismatch` | 422 | `POST /connection/credentials`: a key of another service account while one of the current one's is live. |
| `credential_kind_unsupported` | 422 | `PUT /connection`: `credentialKind` `vantigo_hosted`. |
| `version_mismatch` | 412 | `PUT /connection` and `PUT /connection/settings`: the connection was written since the `If-Match` version; the detail names both. |
| `adoption_confirmed` | 409 | `POST /accounts/{userId}/confirm`: the adoption is confirmed already, or the module created the user. |
| `credential_invalid`, `google_unreachable` | 503 | `GET /connection/skus`: no token for the licences' scope, or Google did not answer. |

An unknown credential is a bare 404. The 401, the 403 and the 429 are the router's.

## Endpoints

All under `/api/v1/google`.

| Operation | Access |
| --- | --- |
| `GET /connection` | `google:manage` |
| `PUT /connection` | `policy:Owner+OwnerManagement`; `If-Match`; `google-owner` (30 a minute per client address, shared by the five Owner operations) |
| `POST /connection/credentials` | `policy:Owner+OwnerManagement`; `google-owner` |
| `DELETE /connection/credentials/{id}` | `policy:Owner+OwnerManagement`; `google-owner` |
| `PUT /connection/oauth-client` | `policy:Owner+OwnerManagement`; `google-owner` |
| `POST /connection/test` | `policy:Owner+OwnerManagement`; `google-owner` |
| `GET /events` | `google:manage` |
| `PUT /connection/settings` | `google:manage`; `If-Match` |
| `GET /connection/skus` | `google:manage`; `google-skus` (30 a minute per client address) |
| `GET /accounts`, `POST /accounts/{userId}/confirm` | `google:manage` |
| `GET /groups`, `POST /groups/{groupId}/confirm` | `google:manage` |

The directory search adds none: identity serves it ([the directory
search](#the-directory-search)).

The request and response shapes are in the contract,
[`openapi/google.yaml`](https://github.com/vantigo-io/vantigo/blob/main/openapi/google.yaml),
and in the API reference generated from it.

## Unverified items

Two of the design's unverified items concern this delivery; each is safe before it is
settled in a test organisation:

- **U2 — new organisations enforce `iam.disableServiceAccountKeyCreation`.** If so, the
  Cloud console refuses to create the key. Nothing in the code depends on it: the
  [administration guide](/en/admin/google-workspace/#create-the-service-account-and-its-key)
  covers lifting the constraint for the project, and the Connection tab's first step
  warns of it.
- **U14 — a scope not delegated answers `unauthorized_client`.** The token endpoint's
  refusal of a scope is read as not delegated whether it says `unauthorized_client` or
  `access_denied`, so either shape gives `pending_delegation` or a missing scope, never a
  refused key.

Five more concern delivery B, each taken on the design's defensive path; none is
settled in a test organisation yet:

- **U11 — `query=externalId:` matches exactly and across types.** Whatever it matches,
  every answer is filtered to an `organization` external id equal to the number,
  ignoring case; the fake answers prefixes and other types to prove it.
- **U12 — Groups Settings' host.** Called on `www.googleapis.com/groups/v1/groups`, the
  documented host; `groupssettings.googleapis.com` is on the fixed list too.
- **U13 — `users.photos.update` takes a 96 px JPEG in the `*`-padded alphabet.** A
  refusal reports `photo_not_written`.
- **U16 — a suspended user's licence is billed, and removing it is allowed.**
  `leaver_remove_licences` defaults on and can be turned off.
- **U19 — `users.signOut` under the delegated scope.** A refusal is reported
  `sign_out_refused`; the suspension is already done.

Three concern delivery C, built on the design's defensive path before they are settled
(P5); the check in a test organisation comes before its merge:

- **U1 — the ID token's `sub` equals the Directory user id.** Announced links name the
  Directory id and identity compares it with `sub`. Should they differ, the object id
  never matches, and the link's other key does not stand in for it: a primary email named
  under another object id is no match, since the address may since have been given to
  another account (identity's test pins it, for Google and for Entra). A link announced
  from a pick would then never be fulfilled, and the sign-in would go as it would have
  without it; links would have to be announced with the primary email alone, as the
  user principal name, until the key is mended.
- **U4 — `hd` for a user on a secondary domain.** Every verified domain is prefilled as
  allowed. Identity also requires `hd` to equal the address's domain: should Google answer
  the primary domain for a user whose address is on a secondary one, that user's sign-in
  is refused, never linked wrongly.
- **The search's prefixes.** `name:'<s>*'` and `email:'<s>*'` are taken as prefix
  matches over the full name and the addresses; a query that finds less shows fewer
  accounts, never a wrong one.

Besides: `licenseAssignments` is addressed by the user's primary email, as its
reference documents; and the language codes written are `no` and `en-GB`.

## Testing

The module's tests run against `googletest`, a fake Google: its token endpoint checks the
JWT — the RS256 signature against the registered key, the issuer, the audience, the expiry,
the subject and the scope — and issues a token only for a scope the test delegated; the Directory's `customers.get` and
`domains.list` answer from its state. It comes in through `Deps.HTTPTransport`, so the
host guard runs in every test.
