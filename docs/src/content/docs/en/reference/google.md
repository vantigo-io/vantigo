---
title: "Google Workspace module"
description: "Google Workspace as a connection to one organisation through a service account's domain-wide delegation — the foundation: the module and why it requires Staff; the schema; the page and its four tabs; the permission and the Owner policy; the connection, the admin to impersonate and the credential kind under If-Match; the key file's rules, the keys in the plural, their try order and a refused key; the Internal web client and its secret; the guarded client, its fixed hosts and its limits; the delegated token, its cache and the one seam; Test connection, the needed and the unexpected scopes, and the statuses and their reasons; the events; the refusals and the endpoints; and the unverified items U2 and U14."
sources:
  - apps/server/internal/google
  - apps/google/frontend
  - apps/host/frontend/src/routes/workspace/google
  - apps/host/frontend/src/routes/workspace/google.tsx
  - openapi/google.yaml
  - apps/server/internal/openapi/specs/google.yaml
  - apps/server/internal/db/migrations/00114_google_baseline.sql
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

**This is the foundation (delivery A).** The module holds the connection — the service
account's keys, the admin to impersonate, the Internal web client — tests it, and reads
the organisation and its verified domains. It fills no slot yet: the accounts
(`contracts.IdentityProvisioner`), the person's own Google connection, the calendar and
Gmail come in later deliveries, each taking its token from the one seam
[`tokenSource`](#the-delegated-token). `MODULES` refuses `google` without `staff`
(`google requires staff`): Staff's accounts worker will be the provisioner's only caller,
and without it the module would hold an organisation-wide credential nothing uses.

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

## The page

The page is the host's layout route `/workspace/google`, with four child routes rendered
as tabs under one header, each tab a link (`googleTabs` in `@vantigo/google-ui`):

| Route | Tab | Component | Holds |
| --- | --- | --- | --- |
| `/workspace/google` | Status | `GoogleStatusTab` | A *Not connected yet* notice while unconfigured, the **Status** card — status and reason, organisation, customer id, primary and verified domains, last test, the scopes granted, missing and unexpected — and **Recent events** |
| `/workspace/google/connection` | Connection | `GoogleConnectionTab` | The six steps: the project, the service account and its key; upload the key; delegate the scopes; the admin to impersonate; **Test connection**; the Internal web client |
| `/workspace/google/accounts` | Accounts | a placeholder | One sentence: accounts are coming |
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
  fake comes in through `Deps.HTTPTransport`, never as a base URL. This delivery calls the
  first two.
- **The transport** dials through netguard, so every address is checked against the one
  table of forbidden addresses; no proxy, and no redirect is followed.
- **Budgets.** 30 seconds per call; a 429 waits `Retry-After` once, at most 10 seconds
  (a longer one fails at once); a 5xx is retried once after a second; a 401 drops the
  token and retries once with a fresh one. Every answer is read through a 1 MiB cap.
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

An event names a user or a system, exactly one. It never carries an address, a key, a
secret, a token or Google's words.

## Refusals

A refusal is an RFC 9457 problem, `GoogleProblem`, whose `code` names the rule:

| Code | Status | When |
| --- | --- | --- |
| field errors under `errors` | 400 | A field broke its rule, keyed by the request's camelCase field (`json`, `adminSubject`, `credentialKind`, `clientId`, `clientSecret`, `limit`, `If-Match`). |
| `client_mismatch` | 422 | `POST /connection/credentials`: a key of another service account while one of the current one's is live. |
| `credential_kind_unsupported` | 422 | `PUT /connection`: `credentialKind` `vantigo_hosted`. |
| `version_mismatch` | 412 | `PUT /connection`: the connection was written since the `If-Match` version; the detail names both. |

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

## Testing

The module's tests run against `googletest`, a fake Google: its token endpoint checks the
JWT — the RS256 signature against the registered key, the issuer, the audience, the expiry,
the subject and the scope — and issues a token only for a scope the test delegated; the Directory's `customers.get` and
`domains.list` answer from its state. It comes in through `Deps.HTTPTransport`, so the
host guard runs in every test.
