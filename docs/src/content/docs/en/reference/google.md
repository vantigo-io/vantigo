---
title: "Google Workspace module"
description: "Google Workspace as a connection to one organisation through a service account's domain-wide delegation — the foundation: the module and why it requires Staff; the schema; the page and its four tabs; the permission and the Owner policy; the connection, the admin to impersonate and the credential kind under If-Match; the key file's rules, the keys in the plural, their try order and a refused key; the Internal web client and its secret; the guarded client, its fixed hosts and its limits; the delegated token, its cache and the one seam; Test connection, the needed and the unexpected scopes, and the statuses and their reasons; the events; the refusals and the endpoints; and the unverified items U2 and U14 — and the accounts (delivery B): the provisioner google and its status, the accounts and groups tables and the account rules, the correlation on the organization external id and the adoption, the address, the mapping, the first password, the licences, the leaver's steps, the delete, the groups' three traits, the photo at 96 px, and the unverified items U11, U12, U13, U16 and U19 — and connecting existing users (delivery C): the directory search identity serves, its two prefix queries, the exact matches by primary email, alias and employee number, their bounds, the announced link keyed by the Directory id, the Sign-in and Provisioning steps, and the unverified items U1 and U4 — and the person's own Google connection (delivery D): the connections table and the switch, the flow with its two cookies, PKCE and the ID token's checks, the features and the scopes as answered, the certain account checked three times, the states, the refresh and its failures, the closes, google-sweep, the codes, the endpoints and their limits, and the unverified items U1 and U9 — and calendar sync on the person's connection (delivery E): the MailboxCalendar google, its status and footing, the marker, the calendars the module makes, the batch endpoint, the delta with its sync token and link, ShowAs, the write-back's reads, the push channels and the anonymous notifications endpoint, and the unverified items U5, U7, U8 and U18."
sources:
  - apps/server/internal/google
  - apps/google/frontend
  - apps/host/frontend/src/routes/workspace/google
  - apps/host/frontend/src/routes/workspace/google.tsx
  - openapi/google.yaml
  - apps/server/internal/openapi/specs/google.yaml
  - apps/server/internal/db/migrations/00114_google_baseline.sql
  - apps/server/internal/db/migrations/00115_google_accounts.sql
  - apps/server/internal/db/migrations/00116_google_connections.sql
  - apps/server/internal/db/migrations/00117_google_calendar.sql
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

**Deliveries A, B, C, D and E.** The module holds the connection — the service account's keys,
the admin to impersonate, the Internal web client — tests it, and reads the organisation
and its verified domains (A). It fills `contracts.IdentityProvisioner` under the key
`google` ([the provisioner](#the-provisioner)): Staff's accounts worker drives a person's
account and Staff's groups in the organisation through it (B). It fills
`contracts.DirectorySearch` under the same key ([the directory
search](#the-directory-search)): identity's Owner endpoints search the organisation's
users and propose exact matches for connecting existing users to Google sign-in (C). It
keeps each person's own Google connection, behind the Owner's Connection for people
switch ([the personal connection](#the-personal-connection)) (D). It fills
`contracts.MailboxCalendar` under the same key ([the mailbox
calendar](#the-mailbox-calendar)): the calendar syncs a person's copies with their
Google Calendar on that connection (E). Gmail comes in later deliveries, each taking its
token from the one seam
[`tokenSource`](#the-delegated-token) — the person's own token through the same
interface. `MODULES` refuses `google` without
`staff` (`google requires staff`): Staff's accounts worker is the provisioner's only
caller.

Nothing in the module imports another module — depguard enforces it, tests included —
and no SQL of it crosses a schema. Its one background worker is
[`google-sweep`](#google-sweep). It reaches the internet
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
| `/workspace/google/calendar-gmail` | Calendar and Gmail | `GoogleCalendarGmailTab` | **Connection for people**: the switch (the Owner's; confirmed before it turns off while anyone is connected), its status and reason, the counts of people connected and waiting to reconnect, and the redirect URI to copy |

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
refused 403. They share one rate limit, `google-owner`: 30 requests a minute per
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

## The personal connection

Each person may connect their own Google account, once, through the organisation's
Internal web client ([the web client](#the-credentials)) — the authorization code flow
with PKCE and a nonce, Entra's Outlook connection reshaped. The service account plays no
part: no calendar or Gmail scope is ever delegated. Design G25–G29;
the files are `personal*.go`.

### The table and the switch

`00116_google_connections.sql` creates **`connections`**, one row per Google account:

- `google_user_id` (32, the primary key) — the account's Directory id, which the ID
  token's `sub` carries (U1);
- `user_id` — identity's user, no foreign key;
- `scopes` — what Google answered in the token response's `scope`, sorted, spelled as
  Google spells them (full URLs, `openid`); never what was asked;
- `refresh_sealed` — the refresh token sealed under `google/connection-refresh`; `NULL`
  once forgotten;
- `state` — `active`, `reconnect` or `closing` (`CHECK`ed), with `error_code` (40) for
  `reconnect`;
- `connected_at`, `refreshed_at`, `used_at` (written at most hourly),
  `account_checked_at` (the sweep's daily read), `changed_at` (the last move between
  states, indexed: what the calendar's `Connections` will read), `revision`,
  `created_at`, `updated_at`.

A partial unique index keeps **one live row per person** (`user_id WHERE state <>
'closing'`); any number of closing rows may stand beside it, so a person re-pointed to
another account does not overwrite the token the old account's removals still need.
Every write that moves a row bumps `revision`, and every write decided on an earlier read
is guarded by the revision read: a refresh that loses a race keeps the winner's token; a
refusal of a token a newer connect replaced changes nothing.

`connection` gains `personal_connection` (the switch, off by default),
`personal_connection_at` (when it was last turned on), and `personal_last_error` with
its instant — a failure of the web client itself an Owner must act on:
`consent_missing`, `invalid_client` or `unauthorized_client`. A connect that works, or a
new web client or secret, clears it.

`GET /connection` answers `personalConnection`: `enabled`; `status` `off` while the
switch is, `unavailable` with `reason` `no_web_client` (no client id or no live secret),
`not_tested` (no test has read the verified domains) or `client_refused`
(`personal_last_error` is the client's), `consent_missing`, else `ready`; `connected`
and `reconnect`, the rows `active` and in `reconnect`; and `redirectUri`, `APP_URL` +
the base path + `/api/v1/google/connect/callback`. **`PUT /connection/personal`**
`{enabled}` is the Owner's (`policy:Owner+OwnerManagement`, `google-owner`); turning it
off closes every live row, tokens kept, one `google.personal.closed` (`switch_off`)
each; the same value again writes nothing. The page asks before turning it off while
anyone is connected.

### The features

A connect names a **feature**, never a scope:

| Feature | Scopes asked |
| --- | --- |
| `calendar` | `https://www.googleapis.com/auth/calendar.events`, `https://www.googleapis.com/auth/calendar.app.created` |
| `mail` | `https://www.googleapis.com/auth/gmail.send` |

Every connect also asks `openid email profile`, and the scopes of every feature the
person's live row already holds, so the answer never depends on Google returning the
union of an incremental grant. A row's features are those whose every scope is in
`scopes`. In this release the host's card and button ask for `calendar`; `mail` is in the
contract for delivery G.

### The flow

1. **`POST /connect`** `{feature, returnTo?}` (`session`): `feature_unknown` (422) for
   another feature; `switch_off` (409) while the switch is off or the web client is not
   ready; `no_account` (409) without a [certain account](#the-certain-account);
   `account_taken` (409) when another person's live row holds it. Otherwise it seals a
   fresh `state` and `nonce` (256 random bits each), a PKCE verifier, the user, a MAC of
   the session id under `google/connect-session`, the feature, the scopes and `returnTo`
   into **`vantigo.google.connect`** (purpose `google/connect-state`, ten minutes,
   HttpOnly, SameSite=Lax, path `/api/v1/google` under the base path), clears a
   connected cookie an abandoned connect left, and answers `authorizeUrl`:
   `https://accounts.google.com/o/oauth2/v2/auth` with `client_id`, `redirect_uri`,
   `response_type=code`, `scope`, `state`, `nonce`, `code_challenge` (S256),
   `access_type=offline`, **`prompt=consent`** (Google issues a refresh token only on a
   consent), `include_granted_scopes=true`, `login_hint` (the account's primary email)
   and `hd` (its domain — a hint; the check is the ID token's).
2. **`GET /connect/callback`** (`anonymous`, on `APP_URL`, `Cache-Control: no-store`):
   the state cookie and `state` (constant time) first — without a valid pair nothing is
   redeemed or written, anywhere, and the answer is `expired`. Then Google's `error`
   (`access_denied`, else `failed`); the switch and the web client again; the code
   redeemed at `oauth2.googleapis.com/token` through the guarded client with the client
   secret and the verifier (`invalid_grant` → `expired`; the client's own refusal is
   recorded as `personal_last_error`); the ID token verified with go-oidc against
   `https://accounts.google.com`, its discovery document and keys fetched through the
   same client — signature (RS256), audience the client id, expiry, the issuer — then
   the nonce in constant time, then `hd` among the connection's `domains`, ignoring case
   (`domain_mismatch`, a consumer account carrying no `hd` included); then the asked
   feature's scopes among the answered `scope` (`consent_missing`, recorded as
   `personal_last_error`). Only then is **`vantigo.google.connected`** sealed (purpose
   `google/connected`, five minutes): the user and session MAC, `sub`, the address, the
   scopes, the refresh token, the feature and `returnTo`. Every answer is a 302 to
   `/settings/connections?connection=google&outcome=complete`, or
   `&outcome=failed&code=` one of `expired`, `access_denied`, `domain_mismatch`,
   `consent_missing`, `failed`; every answer clears the state cookie. Google's
   `error_description` is never read; `scope`, `authuser`, `hd`, `prompt` and `iss` on
   the redirect are ignored.
3. **`POST /complete`** (`session`): the connected cookie must open, be unexpired, name
   the caller and carry the MAC of the caller's session, else 410 `expired`; the cookie
   is cleared on every answer. The switch and the certain account again (409
   `switch_off`, `no_account`); the token's `sub` must equal the account's Directory id
   (409 `account_mismatch`, whose detail names no address). Then, in one transaction,
   the connection row `FOR UPDATE` (a switch-off runs wholly before or after) and the
   account row `FOR SHARE` (so does a leaver's suspension), checked again; the account's
   row, when another person's and live, is `account_taken`; the caller's live row on
   another account is closed (`repointed`), its token kept; the row is inserted, or a
   closing one taken over, `active`, with the answered scopes and the refresh token
   sealed. A connect submitted twice is the caller's own row both times. It answers the
   connection and `returnTo`.

**`returnTo`** is kept only as a path on this installation — at most 512 bytes, a
single leading `/`, never `//` or `/\`, no backslash or control character, no scheme or
host once parsed — else `/settings/connections`; the browser checks it again before it
navigates.

**`GET /connection/me`** (`session`, always 200) answers `{offered, reason?, state,
features, account?, connectedAt?, errorCode?}`: `offered` false with `reason`
`switch_off` (the switch off or the web client not ready) or `no_account`; `account` the
primary email Vantigo knows the caller by; `state` `not_connected`, `active`,
`reconnect` (with `errorCode`) or `closing`. **`DELETE /connection/me`** (`session`)
closes the caller's live row, its token kept for removals; 204 whether or not there was
one.

### The certain account

A person's certain account is Staff's `Workforce.DirectoryAccount` with `Provider`
`google`, enabled, whose `ExternalID` names a row in `accounts` that Vantigo created or
whose adoption is confirmed, in state `enabled`. It is checked:

- **at connect** (`no_account`);
- **at complete** — again, and the token's `sub` against its id (`account_mismatch`);
- **on use** — every token asked: a row `active` whose account is no longer such closes
  with its token forgotten (`deleted` or `disabled`), and the call needs the person.

### The token and its failures

The access token is cached in the process per `(google_user_id, revision, client id,
digest of the sealed refresh token)` until five minutes before it expires; a cache hit
records `used_at`, at most hourly. A miss refreshes at the token endpoint with the
client secret; Google usually answers no new refresh token, and the stored one stays.
The answered `scope` replaces `scopes` (none answered keeps them); a refresh that keeps
no feature the row held moves it to `reconnect`, `consent_missing`. A call for a feature
the row's scopes lack needs the person (`consent_missing`) without moving the row.

| Google answers | The row | The call |
| --- | --- | --- |
| `invalid_grant` (revoked, expired, unused six months, a password change) | `reconnect`, `expired` | `ErrConnectionNeeded` |
| `admin_policy_enforced`, `org_internal` | `reconnect`, `blocked` | `ErrConnectionNeeded` |
| `invalid_scope` | `reconnect`, `consent_missing` | `ErrConnectionNeeded` |
| another refusal | `reconnect`, `sign_in_again` | `ErrConnectionNeeded` |
| `invalid_client`, `unauthorized_client` | untouched; `personal_last_error` set | fails, not the person's |
| a 5xx, 429, `temporarily_unavailable`, no answer | untouched | fails, transient |

A row in `reconnect` is never refreshed again until the person connects; the call
answers its code. A closing row, or any row while the switch is off, serves removals
alone while it holds its token; its token refused is forgotten. Every move to
`reconnect` is a `google.personal.reconnect_needed` event. Nothing in this delivery
calls it: the calendar (E) and personal sending (G) will.

### Closes

| Cause | Token | Code |
| --- | --- | --- |
| The person's **Disconnect** | kept for removals | event `google.personal.disconnected` |
| The switch off | kept | `switch_off` |
| Re-pointed to another account at complete | kept | `repointed` |
| The leaver's `Deactivate`, in the transaction that records the suspension | forgotten | `leaver` |
| `Delete`, or the user gone at Google | forgotten | `deleted` |
| On use, the account not usable | forgotten | `deleted`, `disabled` |
| The sweep finds the account suspended or gone | forgotten | `suspended`, `deleted` |

### google-sweep

The module's one worker, hourly, in the API and in worker mode. It takes no advisory
lease and holds no pooled connection across a call to Google:

1. One `DELETE … RETURNING` takes the spent closing rows — seven days after closing, or
   once a day has passed since their last use and their closing while they hold no
   calendar push channel (delivery E) — then each token they
   held is revoked at `oauth2.googleapis.com/revoke` (a token Google no longer knows is
   revoked already), and each is a `google.personal.deleted` event with `revoked` or
   `revoke_failed`. A failed revocation is not retried: the row is gone, and Google lets
   an unused token lapse.
2. One `UPDATE … FOR UPDATE SKIP LOCKED` claims at most 100 rows in `reconnect` not read
   for a day, stamping `account_checked_at`; each account is read in `accounts` first
   (gone, deleted, untrusted or not enabled closes it), then by `users.get` as the admin
   to impersonate — 404 closes it `deleted`, `suspended` closes it `suspended` — each
   close guarded by the revision claimed. A failure at Google ends the step; the rest
   wait for their next claim.

### The changes reader

`connectionChanges(since)` answers the rows whose state moved after `since`, oldest
first, at most `contracts.ConnectionChangesMax`, each as a `contracts.ConnectionChange`
whose mailbox is the Google id: what the calendar's `MailboxCalendar.Connections`
answers ([the mailbox calendar](#the-mailbox-calendar)).

## The mailbox calendar

Delivery E (G30–G32, [`docs/superpowers/plans/2026-11-03-google-calendar.md`](https://github.com/vantigo-io/vantigo/blob/main/docs/superpowers/plans/2026-11-03-google-calendar.md))
fills `contracts.MailboxCalendar` under the key `google`, `DisplayName()` *Google
Calendar* (`mailbox*.go`). The calendar picks it for a person whose directory account's
`Provider` is `google`; the mailbox is the account's Directory id, and the calendar id
`""` is the person's `primary` calendar. Every call is made **on the person's own
connection** with the `calendar` feature ([the token](#the-token-and-its-failures)),
never on the service account: no calendar scope is delegated (Q3).

### The footing of a call

- **The status** (`Status`, no network): `available` while Connection for people is on
  and the web client ready; `cleanup` (`switch_off`) while the switch is off, the web
  client set, and a closing connection still holds its token; else `unavailable` with
  `switch_off` or the web client's reason (`no_web_client`, `not_tested`).
- **The removals** — `DeleteCalendar`, `Find`, `Unwatch` and an `Apply` of deletes alone
  — run under `cleanup` and on a closing connection while it holds its token; every
  other call needs `available` and an active connection. A removal whose connection is
  closing with no token, or gone, is `ErrMailboxGone`.
- **The account**: a Directory id that is a row of `accounts` the module made or whose
  adoption is confirmed, and not deleted; otherwise `ErrMailboxUnknown`, with no
  network.
- **The connection's answers**: `ErrConnectionNeeded` with `not_connected`, `closing`,
  `consent_missing` (the calendar's scopes not granted — the row is not moved) or the
  reconnect's code. A **401** after the client's one fresh token moves the row to
  `reconnect`, `sign_in_again`, at the revision the token came from (a row moved on
  since is a failure to retry). A **403** that is no usage limit is `ErrMailboxDenied`.
  The web client not set up or refused is `ErrMailboxUnavailable`.
- **`quotaUser`** is the Google id on every call — the batch's and each part's.

### The tables

`00117_google_calendar.sql` creates **`calendars`** — `(google_user_id, calendar_id)`,
the secondary calendars the module made, with `created_at` — and **`calendar_channels`**
— `google_user_id` (the primary key: one channel a person), `channel_id` (a UUID,
unique), `resource_id`, `token_hash` (32 bytes, `CHECK`ed), `address`, `expires_at`,
`granted_seconds`, `notified_at` (indexed where set) and `created_at` — and gives
**`connection`** its `marker` (32 lower-case hex digits, `CHECK`ed, drawn with
`gen_random_uuid()`). The down step drops them; the copies and calendars stay in the
people's Google Calendars, and the channels lapse.

### Copies and calendars

- **The marker.** `connection.marker`, 32 hex digits the migration draws once. A copy
  carries `extendedProperties.private = {"vantigo": <marker>, "vantigoKey": <key>}`,
  written on a create and on an update naming a key. An event whose `vantigo` is another
  value is another installation's (`Foreign`), and one without it the person's own: never
  answered by `Find`, `Marked` or the default calendar's `Versions`.
- **`Apply`** sends creates (`POST …/events`), updates (`PATCH …/events/{id}`) and deletes
  (`DELETE …/events/{id}`) through the batch endpoint
  (`www.googleapis.com/batch/calendar/v3`, `multipart/mixed`), fifty a batch, one batch
  after another, and asks Google to answer `id,etag,status` alone. The `etag` is the change
  key. Each part is judged alone: 2xx done (an update answered `cancelled` is `Gone`), 404
  or 410 on an update or a delete `Gone`, a 403 `ErrMailboxDenied`, a usage limit (429, or
  403 `rateLimitExceeded`/`userRateLimitExceeded`) sent again once after a second and then
  a failure. Once one op is refused for the mailbox, the rest answer the same, unsent. A
  batch holding a create is sent once.
- **The event written**: `summary`, `description`, `location` (an update always writes
  it), a timed event's `start.dateTime`/`end.dateTime` in RFC 3339 with the event's IANA
  zone (else UTC), an all-day event's `start.date`/`end.date` (the end the day after the
  last), the other kind's field `null`; `transparency` `transparent` for free and `opaque`
  otherwise — tentative, out of office and working elsewhere are written opaque, Google
  keeping no such state on a plain event —; `visibility` `private` or `default`;
  `reminders {useDefault: false}`.
- **`Find`** is `events.list(privateExtendedProperty=vantigo=<marker>, timeMin=from)`,
  pages of 250, an event kept only when it starts at or after `from` (`timeMin` bounds
  the end), at most 5000; `Meeting` when it has an attendee other than the person. A
  secondary calendar that is gone answers none.
- **The calendars.** `calendar.app.created` reaches only the calendars this client made,
  and Google keeps no property on a calendar, so a calendar is the module's when
  **`calendars`** holds `(google_user_id, calendar_id)`. `EnsureCalendar` keeps `known`
  when its row and `calendars.get` both answer, else adopts another row's calendar that
  still exists, else `calendars.insert {summary: name}` and writes the row; a row whose
  calendar is gone (404, 410) is dropped. A 403 on the insert is `ErrMailboxDenied` (U8):
  the calendar's *own* target is then unavailable. `DeleteCalendar` of `primary`, or of a
  calendar no row names that still exists, is `ErrCalendarNotOurs`; one gone is done.

### Reads

- **`Delta`** (G31). `link ""` lists `primary` with `singleEvents=true`,
  `showDeleted=true`, `timeMin` and `timeMax`, pages of 250; the last page's
  `nextSyncToken` is the link answered `Done`. A continuing link lists with `syncToken`.
  At most 40 pages a call; a cut answer's link carries the next page's token. The link is
  `g1.` and the base64url of `{m, f, t, s, p, w}` — the mailbox, the window, the sync
  token, the page token and whether the listing paged is the windowed one; a link whose
  `m` is not the mailbox asked, or not the provider's at all, is refused with a failure,
  unsent. **410 is `ErrMailboxDeltaExpired`.** Increments are not windowed (U7), so the
  provider enforces the window: an event outside it, deleted or cancelled is `Removed`.
  A windowed list that answers no `nextSyncToken` leaves a link with no token, whose next
  read is `ErrMailboxDeltaExpired` at once — every sync then re-reads the window, logged
  once per mailbox in the process. The fields asked are `id`, `status`, `start`, `end`,
  `transparency`, `eventType`, `recurringEventId` and `attendees(self,responseStatus)`;
  `Series` is an instance of a recurring event.
- **`ShowAs`**: `transparency=transparent` → `free`; `eventType=outOfOffice` →
  `out_of_office`; `eventType=workingLocation` → `working_elsewhere` (opaque only, a
  transparent one being free); the person's own `declined` → `free`; `tentative` →
  `tentative`; otherwise `busy`.
- **`Marked`** and **`Events`** read `events.get` per id through the batch endpoint, fifty
  a batch. `Marked` answers the ids carrying the marker; `Events` reads the event whole —
  `TextBody` is a description with no HTML tag (Google's editor writes HTML), `Private` a
  `private` or `confidential` visibility — and answers 404, 410 and a cancelled event in
  `Gone`, skipping a 403 of one event. A usage limit is waited once, then a failure.
- **`Versions`**: the default calendar is `Find`'s list with the `etag`; a secondary
  calendar is `events.list(timeMin, timeMax, singleEvents=true)` with
  `fields=items(id,etag,status,recurringEventId)`; at most 5000, `Complete` false past
  it.
- **Ids.** An event id is `[A-Za-z0-9_-]`, at most 1024; a calendar id
  `[A-Za-z0-9_.@+-]`, at most 255, path-escaped. Anything else is refused before a call,
  and a delta's event with one is left out, counted.

### Change notifications

- **`Watch`** opens `events.watch` on `primary` — `id` a fresh UUID, `token` 32 random
  bytes in base64url, `address` `PUBLIC_BASE_URL/api/v1/google/calendar-notifications`,
  `params.ttl` 30 days — and records **`calendar_channels`**: the Google id (one channel a
  person), the channel and resource ids, the token's **SHA-256** (the token itself is
  never stored), the address, the expiry Google answered and the seconds it granted.
  Nothing is asked while a channel for the present address has more than **min(3 days,
  half its granted life)** left (U5); otherwise a new channel is opened, the row
  replaced, and the old one stopped (a refused stop is logged and left to lapse; its
  messages name no row). No `PUBLIC_BASE_URL`, or `CALENDAR_MAILBOX_NOTIFICATIONS_ENABLED`
  off, answers nil: the poll carries the sync. Refused under `cleanup`.
- **`Unwatch`** is `channels.stop {id, resourceId}` on the removal token: 2xx and 404
  forget the row, and so does a connection holding no token for it; anything else keeps
  the row and answers the error, the stop owed. No row, or one past its expiry, is nil
  with no network.
- **`Notified`** answers the rows notified after `since`, oldest first, at most
  `MailboxNoticesMax`. **`Connections`** is [the changes reader](#the-changes-reader).
- **`POST /calendar-notifications`** (anonymous, on the public host's allowlist, 600 a
  minute per client address, `google-calendar-notifications`) reads headers alone and no
  body: `X-Goog-Channel-ID` a UUID naming a row, the SHA-256 of `X-Goog-Channel-Token`
  equal to the row's (constant time), `X-Goog-Resource-ID` the row's; `exists` and
  `not_exists` set `notified_at`, `sync` and anything else nothing. **204 always**, a
  database failure included, so the endpoint tells nothing of which channels exist. The
  log names no channel, token, resource or Google id.
- `google-sweep` deletes a closing connection unused for a day only once it holds no
  channel row; after seven days, whatever it holds.

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
| `google.personal.switched` | `PUT /connection/personal` | `on`, `off` |
| `google.personal.connected` | `POST /complete` | `feature:<feature>`; subject the Google id |
| `google.personal.disconnected` | `DELETE /connection/me` | — |
| `google.personal.reconnect_needed` | a refused refresh, by `system:google` | the error code |
| `google.personal.closed` | the closes | `leaver`, `deleted`, `disabled`, `suspended`, `switch_off`, `repointed` |
| `google.personal.deleted` | `google-sweep`, by `system:google` | `revoked`, `revoke_failed` |
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
| `feature_unknown` | 422 | `POST /connect`: a feature Vantigo does not offer. |
| `switch_off` | 409 | `POST /connect`, `POST /complete`: Connection for people is off, or the web client is not ready. |
| `no_account` | 409 | `POST /connect`, `POST /complete`: no certain account. |
| `account_taken` | 409 | `POST /connect`, `POST /complete`: another person's live connection holds the account. |
| `account_mismatch` | 409 | `POST /complete`: the token's `sub` is not the account's Directory id. |
| `expired` | 410 | `POST /complete`: the connected cookie is missing, expired, or another person's or session's. |

An unknown credential is a bare 404. The 401, the 403 and the 429 are the router's.

## Endpoints

All under `/api/v1/google`.

| Operation | Access |
| --- | --- |
| `GET /connection` | `google:manage` |
| `PUT /connection` | `policy:Owner+OwnerManagement`; `If-Match`; `google-owner` (30 a minute per client address, shared by the Owner operations) |
| `POST /connection/credentials` | `policy:Owner+OwnerManagement`; `google-owner` |
| `DELETE /connection/credentials/{id}` | `policy:Owner+OwnerManagement`; `google-owner` |
| `PUT /connection/oauth-client` | `policy:Owner+OwnerManagement`; `google-owner` |
| `POST /connection/test` | `policy:Owner+OwnerManagement`; `google-owner` |
| `GET /events` | `google:manage` |
| `PUT /connection/settings` | `google:manage`; `If-Match` |
| `GET /connection/skus` | `google:manage`; `google-skus` (30 a minute per client address) |
| `GET /accounts`, `POST /accounts/{userId}/confirm` | `google:manage` |
| `GET /groups`, `POST /groups/{groupId}/confirm` | `google:manage` |
| `PUT /connection/personal` | `policy:Owner+OwnerManagement`; `google-owner` |
| `POST /connect`, `POST /complete` | `session`; `google-connect` (30 a minute per client address, shared) |
| `GET /connect/callback` | `anonymous`; `google-connect-callback` (60 a minute per client address) |
| `GET /connection/me`, `DELETE /connection/me` | `session` |
| `POST /calendar-notifications` | `anonymous`; `google-calendar-notifications` (600 a minute per client address); on the public host's allowlist |

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

Two concern delivery D, built on the design's defensive path before they are settled
(P5); the check in a test organisation comes before its merge:

- **U1** again: complete compares the ID token's `sub` with the Directory id. Should they
  differ, every connect ends `account_mismatch` and nothing is stored — visible, never
  wrong.
- **U9 — incremental consent with `include_granted_scopes` and `prompt=consent`
  answers the union in `scope`.** Only the answered `scope` is stored, and every connect
  asks again for the features the row holds; should Google answer less, a feature held
  before is dropped from the row, never assumed, and the asked one missing ends
  `consent_missing`.
- Besides: Google answering `scope` on a refresh (none keeps the stored scopes), and
  `admin_policy_enforced` and `org_internal` as the refusals of a blocked web client
  (any other refusal is `sign_in_again`, never a loop).

Four concern delivery E, built on the design's defensive path before they are settled
(P5); the check in a test organisation comes before its merge:

- **U5 — a push channel's lifetime.** Google does not document what `events.watch`
  grants; the channel asks 30 days and is renewed at min(3 days, half the life granted),
  and the poll runs regardless.
- **U7 — a windowed first list answers a sync token, and increments are unwindowed.** The
  provider enforces the window on every change; with no token answered, every sync
  re-reads the window (correct, costlier, logged once per mailbox).
- **U8 — `calendar.app.created` lets the person's token make and use a secondary
  calendar.** A refusal is `ErrMailboxDenied`: the *own* target is unavailable, and the
  main calendar is unaffected.
- **U18 — push needs HTTPS alone, no domain verification.** Without notifications the
  poll carries the sync.

Besides: `licenseAssignments` is addressed by the user's primary email, as its
reference documents; and the language codes written are `no` and `en-GB`.

## Testing

The module's tests run against `googletest`, a fake Google: its token endpoint checks the
JWT — the RS256 signature against the registered key, the issuer, the audience, the expiry,
the subject and the scope — and issues a token only for a scope the test delegated; the Directory's `customers.get` and
`domains.list` answer from its state. For the personal connection it serves Google's
discovery document and a JWK set, stands in for the authorization endpoint with
`IssueCode` (the person's consent, granular: the test says what is granted), checks the
authorization code grant (the web client and secret, the redirect URI, PKCE, single
use, a minute's life), signs the ID token (its claims changeable to prove each check),
answers refreshes as Google does (no new refresh token; `RefuseRefresh`,
`NarrowRefresh`) and revokes. For the calendar it serves Calendar v3 — calendars,
events with `privateExtendedProperty`, windows, pages and sync tokens
(`ExpireSyncTokens` for 410, `SetNoSyncToken` for U7), the batch endpoint, `events.watch`
and `channels.stop` — to the person's own access token. `internal/integration` composes
the calendar and this module together and drives the calendar's own worker against it. The sweep's tests run on a pool of one connection. It comes in through `Deps.HTTPTransport`, so the
host guard runs in every test.
