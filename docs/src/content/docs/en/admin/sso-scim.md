---
title: "SSO and SCIM operations"
description: "The Single sign-on card: the workforce OIDC provider and the SCIM token, per provider, rotation, the environment import, lifecycle and recovery."
sidebar:
  order: 21
sources:
  - apps/server/internal/identity
  - apps/server/internal/db/migrations/00058_identity_sso_settings.sql
  - apps/server/internal/db/migrations/00059_identity_expected_accounts.sql
---
This is the production runbook for Vantigo's workforce single sign-on and SCIM
provisioning. Each installation has **at most one** workforce OpenID Connect provider
and one SCIM credential. Both live in one row of the database,
`identity.sso_settings`, which an Owner edits on the **Single sign-on** card under
Workspace administration. Every process — each API replica and each worker — reads
that row through a cache of 20 seconds, so a save is in effect everywhere within 20
seconds, with no restart and nothing to change in the deployment.

Vantigo is a single-tenant application. There is no tenant control plane, no
per-tenant SSO or provisioning setting, and nothing to scope a provider to: the
workforce OIDC provider and the SCIM credential are installation-wide.

## Preconditions

- An **Owner** account, signed in with a second factor while `OWNERS_REQUIRE_MFA` is
  on (the default outside development). The card and every request behind it are an
  Owner's alone; a person with delegated role administration does not see it.
- `APP_URL` (and `APP_BASE_PATH`, when one is used) set to the public origin the
  provider redirects to and the directory calls. The callback URL and the SCIM
  endpoint the card shows are derived from them.
- For Entra workload identity: the platform projecting the token file into every
  replica that serves the API, at the same path (see
  [workload identity](#microsoft-entra-id-workload-identity)).

## The Single sign-on card

Open the avatar menu, choose **Workspace admin**, and stay on **Overview**: the
**Single sign-on** card runs the full width below the overview's other cards, the
**Workspace** card among them. It holds:

- **Provider** — **Off**, **Microsoft Entra ID** or **Google Workspace**.
- **Authority** — filled in as `https://accounts.google.com` when Google is chosen —
  **Client ID**, and the credential: for Entra a **Credential** choice between
  **Client secret** and **Token file**; for Google always the **Client secret**. The
  secret field is blank on every visit. Once a secret is stored it reads "Set on
  _date_. Leave it blank to keep it." for as long as the provider and the **Client
  ID** are the ones it was saved for; otherwise "Sealed when saved and never shown
  again.", and a secret or a token file must be given.
- **Domains** (Google only) and the **Display name** of the sign-in button,
  `Workforce SSO` when left empty.
- The **Callback URL**, with a copy button, shown before anything is set.
- **Save**, **Turn off** for the provider, and **Last sign-in**.
- The SCIM block: the **Endpoint URL** to copy into the directory, **Token issued**,
  **New token**, **End overlap** while a rotation overlaps, **Turn off**, and **Last
  request**.
- A notice, **Imported from the environment**, when the settings were imported from
  the environment, to remove the variables (see
  [Before the card](#before-the-card-the-environment-variables)). It stays until
  the next change made on the card — a save, a token issued, a part turned off — and
  a turn-off with nothing to turn off changes nothing.

**With the Microsoft Entra ID module** the card's two parts are also the last two
steps of the module's wizard: step 5, **Sign-in**, is the provider form, filled in
with the tenant's authority and the client id the wizard registered (on an empty form
only), and step 6, **Provisioning**, is the SCIM block. Both are an Owner's alone. They are the same forms over
the same row and the same operations; save in either place
([Microsoft Entra ID](/en/admin/entra/#5-sign-in)).

Behind the card are six operations under `/api/v1/identity/owner/sso`, in the
contract [`openapi/identity.yaml`](https://github.com/vantigo-io/vantigo/blob/main/openapi/identity.yaml):

| On the card | Request |
| --- | --- |
| Opening it | `GET /owner/sso` — the row without the client secret or any token, the callback URL, the SCIM endpoint, the last OIDC sign-in and the last SCIM request |
| **Save** | `PUT /owner/sso/oidc`, with `If-Match` carrying the version the card read |
| **Turn off** for the provider, or **Off** and **Save** | `DELETE /owner/sso/oidc` |
| **New token** | `POST /owner/sso/scim/token` |
| **End overlap** | `DELETE /owner/sso/scim/previous` |
| **Turn off** in the SCIM block | `DELETE /owner/sso/scim` |

Every write holds the row in one transaction, moves its `version` on, answers the
whole row with the new version, and is audited `sso-settings.changed` with the
settings before and after — never the client secret and never a token or its digest.
A **Turn off** or **End overlap** with nothing to clear writes nothing: it answers the
row as it is, with the same version and no audit. **Save** alone carries the version:
one from a card opened before someone else's write is refused `412
version_mismatch`, and the card says "The settings have changed": choose **Reload**
and save again. The answer that carries a new SCIM token is sent with
`Cache-Control: no-store`. The operations are limited to 30 requests a minute, like
the rest of user management.

## Workforce OIDC

OIDC uses the authorization code flow with PKCE and a nonce, a sealed state cookie,
and a fixed local completion path. Provider access and ID tokens are never saved. The
provider identity is only a login correlation: it does not grant local roles, prove
local MFA, or bypass the local Owner/MFA policy. New identities are provisioned just
in time as ordinary local `User` accounts keyed by the validated issuer and
case-sensitive `sub`; an email collision is rejected rather than auto-linked.

The public callback, which the card shows as **Callback URL**, is:

```text
https://<public-host><base-path>/api/v1/identity/oidc/callback
```

Register that exact HTTPS URL with the provider. With the default empty
`APP_BASE_PATH` it is `/api/v1/identity/oidc/callback`. The browser starts at
`/api/v1/identity/oidc/challenge` and local completion is
`/api/v1/identity/oidc/complete`; neither is a provider callback URI. Every failure
redirects to `/sign-in?error=<code>`, and no redirect target ever comes from the
request.

### The client-authentication mode is inferred

There is no client-authentication setting. Vantigo decides from **which credential
the card holds**:

| Credential saved | Mode |
| --- | --- |
| **Client secret** | Client secret |
| **Token file** | Workload identity (Entra only) |
| Both in one save | **Refused** — they are mutually exclusive |
| Neither, with a secret stored for the same provider and client ID | Client secret — the stored one is kept |
| Neither, otherwise | **Refused** — one is required |

Saving one clears the other: a token file clears the stored secret, a secret clears
the token file. Only a secret is kept by leaving it out; a token file is part of every
save, and the card sends the path it shows each time. A save that changes the provider or the client ID must carry a
credential, since the old application's secret is never carried over to a new one.
The secret is sealed at rest under `APP_SECRET` and never shown again.

### Microsoft Entra ID: client secret

With the Microsoft Entra ID module, follow its wizard instead: the registration, the
tenant and the client id are steps 1 to 3, and this form is step 5
([Microsoft Entra ID](/en/admin/entra/)). By hand:

1. Create or select one Entra app registration for this installation. Use a
   confidential web application and create a client secret. Copy its value: you
   need it once, for the card.
2. Note the tenant-specific authority, exactly in this form, including `/v2.0`:

   ```text
   https://login.microsoftonline.com/<tenant-guid>/v2.0
   ```

   `<tenant-guid>` must be the tenant GUID. Authorities using `common`,
   `organizations`, `consumers`, another host, a query or a different path are
   refused on **Authority** with the form it must have.
3. Register the card's **Callback URL** as the registration's web redirect URI.
4. Grant only the delegated scopes the sign-in flow needs (`openid`, `profile`,
   `email`). Do not treat provider group or role claims as Vantigo authorization
   grants.
5. On the **Single sign-on** card, choose **Microsoft Entra ID** as the provider, and
   fill in the **Authority** and the application (client) ID as **Client ID** (it
   must be a GUID). Choose **Client secret** as the **Credential** and paste it. Give
   the button a **Display name** if `Workforce SSO` is not what people should see (at
   most 100 characters, trimmed). Choose **Save**: the card says "OIDC settings
   saved", or marks each field the save refused with the sentence saying why.

Verify: within 20 seconds the sign-in page shows **Continue with** and the display
name. Sign in with an ordinary test account, and check that the card's **Last
sign-in** moves.

After the built-in token checks, Vantigo requires a `tid` GUID matching the tenant in
the configured authority and an `oid` GUID. If the validated token carries multiple
`aud` claims, its `azp` must equal the configured client ID. Provider identities
remain local `User` accounts and never become Owners through claims.

To rotate the secret, create a new one on the registration, paste it into **Client
secret** and choose **Save**; once the card says it was set today, delete the old one
in Entra. No restart is needed.

### Microsoft Entra ID: workload identity

Workload identity replaces the **client secret used while redeeming the authorization
code**. It does not replace end-user login, does not make OIDC claims trusted as
local authorization, and does not replace the independent SCIM bearer token.

The deployment platform must provide all of the following before the card is saved:

- A Kubernetes cluster with an OIDC issuer and Azure Workload Identity enabled, or
  the equivalent Azure-hosted integration.
- The mutating webhook/sidecar configuration that projects a service-account token
  into every pod that serves the API, at one absolute path (the webhook sets
  `AZURE_FEDERATED_TOKEN_FILE` to it, normally
  `/var/run/secrets/azure/tokens/azure-identity-token`).
- An Entra app registration whose client ID is the card's **Client ID**.
- A federated identity credential on that registration with:
  - **Issuer**: the exact cluster OIDC issuer URL;
  - **Subject**: the exact workload subject, normally
    `system:serviceaccount:<namespace>:<service-account>`; and
  - **Audience**: `api://AzureADTokenExchange`.

Azure compares issuer, subject and audience exactly. Do not copy a different
namespace, service-account name, trailing path or audience from another workload.

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: vantigo
  namespace: production
  annotations:
    azure.workload.identity/client-id: <entra-application-client-guid>
    # Optional when the cluster's tenant is not the app's tenant:
    # azure.workload.identity/tenant-id: <tenant-guid>
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: vantigo
  namespace: production
spec:
  template:
    metadata:
      labels:
        azure.workload.identity/use: "true"
    spec:
      serviceAccountName: vantigo
      containers:
        - name: vantigo
          image: ghcr.io/vantigo-io/vantigo:<pinned-release>
```

Then, on the card: **Microsoft Entra ID**, the **Authority** and the **Client ID**;
choose **Token file** as the **Credential** and enter the projected file's path.
Choose **Save**. The process that saves checks, before it touches the row, that the
path is absolute and names a regular file of at most 16 KiB that it can read, and
refuses it on **Token file** otherwise; saving it clears any stored client secret.

**Every replica must see the same file at the same path.** The save is checked by one
process only; a replica that cannot read the file fails the sign-ins it completes with
`/sign-in?error=oidc_remote_failure`. The assertion is read **fresh from the file for
every authorization-code redemption** — only from a regular file, and no more than
16 KiB of it — and sent with
`client_assertion_type=urn:ietf:params:oauth:client-assertion-type:jwt-bearer`.
Rotate the projected token through the platform; Vantigo does not cache it.

### Google Workspace

1. Create a Google OAuth web client and copy its secret. Google is client-secret only
   — workload identity is not accepted.
2. Register the card's **Callback URL** as an authorised redirect URI.
3. On the card, choose **Google Workspace**: **Authority** is filled in with the exact
   issuer `https://accounts.google.com`. Enter a client ID ending in
   `.apps.googleusercontent.com` as **Client ID**, and the **Client secret**.
4. List every permitted Workspace domain in **Domains** as a bare DNS name, such as
   `example.com`. **At least one is required for Google.** Each is trimmed,
   lower-cased and stripped of trailing dots, and duplicates are dropped; a URL, an
   address, a port or a single label is refused.
5. Choose **Save**, and verify as for Entra.

Vantigo requires `email_verified`, a syntactically valid email, and a non-empty `hd`
claim that matches the email domain and appears in **Domains**. Personal Gmail
accounts and unverified or mismatched-domain identities are rejected.

### Change the authority, or turn the provider off

People who have signed in through the provider are linked to it by the issuer — the
authority — and their subject. A save that changes the **Authority** while anyone is
linked under the current one asks for confirmation first — "Change the provider's
authority?" — and is sent again with `confirmIssuerChange` only when you choose
**Change authority** (the request is refused `409 oidc_links_exist` without it). Confirm only when you mean to
move to another tenant or provider: nobody linked under the old authority is
recognised under the new one. Their next SSO sign-in finds no link, and because an
address never links a new identity to an existing account, it is refused
(`oidc_email_conflict`) while their user exists.

**Turn off** in the provider's part of the card — or **Off** as the provider and
**Save**, which does the same — asks first, "Turn off SSO?", and on **Turn off**
turns OIDC off and clears its settings, the stored secret included. While people are
linked to the provider a second dialog says "People are linked to this provider",
and only its **Turn off** sends `?confirm=true` (without it the request is refused
`409 oidc_links_exist`). Within 20 seconds the sign-in page offers no provider button
and every process stops accepting the provider's callback.

## SCIM

SCIM is on while a token exists. The fixed protocol endpoint, which the card shows as
**Endpoint URL**, is:

```text
https://<public-host><base-path>/api/v1/identity/scim/v2
```

Requests authenticate with `Authorization: Bearer <token>` and use
`application/scim+json`. The supported resources are `Users` and `Groups`, plus the
standard `ServiceProviderConfig`, `ResourceTypes` and `Schemas` discovery endpoints.

**The token is generated by the server and shown once.** Vantigo makes it (32 random
bytes, base64url without padding), shows it in a dialog with **Copy token** and "It
will not be shown again.", and keeps only its SHA-256, so a token is never shown again: a lost one is replaced with a new
token, never recovered.

To turn SCIM on:

1. On the **Single sign-on** card, copy the **Endpoint URL**.
2. Choose **New token**. The dialog shows the token; copy it straight into the
   directory's provisioning settings (in Entra, the enterprise application's
   **Provisioning** page: **Tenant URL** is the endpoint URL, **Secret Token** the
   token) and close the dialog with **Done**. It does not close on a click beside it.
3. Test the connection from the directory, and check that the SCIM block's **Last
   request** moves.

SCIM state is persistent: user mappings, SCIM groups and memberships, lifecycle
state, ETags and audit records live in PostgreSQL. SCIM-created users are
unprivileged, and **Owner accounts are protected from SCIM mutation** — an update,
patch or delete targeting an Owner is refused, which keeps the break-glass account
outside the provisioning system's reach.

### The enterprise extension and the employee number

The User resource accepts the SCIM **enterprise extension**,
`urn:ietf:params:scim:schemas:extension:enterprise:2.0:User` — `employeeNumber`,
`costCenter`, `organization`, `division`, `department` and `manager` — kept in the
user's SCIM profile, answered back on `GET`, advertised by `Schemas` and
`ResourceTypes`, and patchable on its own paths (`urn:…:User:department`) or by a
PatchOp without a path whose value holds the block — the form Entra sends, its manager
as a bare string accepted too. Each value is at most 256 characters. It changes nothing in the user's own record: in particular the
directory's `employeeNumber` never becomes the user's employee number in Vantigo — only
Staff's provisioning or an Owner's adoption make that claim
([details](/en/admin/authentication/#scim)).

Entra's default attribute mapping for a custom application already sends it:
**`employeeId → employeeNumber`** in the extension, and `userPrincipalName → userName`.
Note that the same default sends **`mailNickname → externalId`** — the mail nickname,
not the object id. Leave both as they are. Entra matches a user it provisions against
the target by one attribute at a time, and the target must filter on it: Vantigo
answers `GET /Users?filter=urn:ietf:params:scim:schemas:extension:enterprise:2.0:User:employeeNumber eq "1042"`
as well as `userName eq` and `externalId eq`.

**Accounts Staff announces.** For a person whose account kind in Staff is `entra`,
Staff has the provider create the account and announces it to identity
([the announced account](/en/admin/authentication/#the-announced-account)). The SCIM
create that brings the user is matched, in order, on the extension's
`employeeNumber`, then on `userName` against the announced principal name, then on
`externalId` against the announced object id — which matches only if you map
`objectId` to `externalId`, since the default sends the mail nickname. A match makes
the user Staff-managed and numbered on arrival, with no adoption; the audit records
`account.adopted-from-provider`. A create that matches nothing makes a plain user, as
before.

To verify, after the provider has created a person's account and the provisioning
cycle has run: the user appears on the **Users** page managed by Staff with the
person's employee number and not pending, and the person's Account tab in Staff shows
the Vantigo account active.

### Token rotation with overlap

1. Choose **New token**. Since a token exists, the card asks first, "Issue a new
   token?", how long to "Accept the current token for (hours)": 1 to 24, 24 by
   default (the request's `previousExpiresAt`; 24 sends none, so the server's own
   default applies). Choose **Issue token**. The new token is then shown once, with
   "The previous token is accepted until _date_."
2. Put the new token into the directory and test the connection there.
3. When every client has switched, choose **End overlap**: the old token stops being
   accepted now rather than at the expiry. Or let the overlap run out.

Only the token just replaced overlaps. **New token** during an overlap ends the
running one at once: the token before last stops being accepted immediately. Nothing
needs a restart and nothing in the deployment changes. An expiry in the past or more
than 24 hours ahead is refused on `previousExpiresAt`.

### Turn SCIM off

Choose **Turn off** in the SCIM block and confirm "Turn off SCIM?" with **Turn off**.
The tokens are forgotten and, within 20 seconds, the endpoint refuses every request. The users, mappings and groups SCIM provisioned stay as they
are, and a directory's deactivation of a user stops counting for who holds a
permission. To turn it on again, issue a **New token** and give it to the directory.

### Status and operational evidence

The card shows the last OIDC sign-in (**Last sign-in**) and the last authenticated SCIM
request (**Last request**). An
authenticated Owner can also read:

```text
GET /api/v1/identity/owner/system-status
```

The response carries, read from the same row:

- `total`, `active`, `disabled` — user counts
- `pendingInvitations`
- `staticOidcEnabled`, `staticOidcProvider`
- `staticScimEnabled`
- `lastStaticOidcSignInAtUtc`, `lastAuthenticatedScimRequestAtUtc`

The two timestamps are operational projections, not authentication-critical state: a
telemetry or database write failure must not turn a successful OIDC or SCIM operation
into a failed one. Neither endpoint ever returns bearer tokens, token digests or
client secrets. Treat null timestamps as "no successful use has been recorded yet".

## Before the card: the environment variables

Until the release that brought the card, the provider and the SCIM token were the
`OIDC_*` and `SCIM_*` environment variables, read at every start. That release moves
them into the row once, by itself:

- **The import.** The first process serving the API (`api` or `server`) that finds the
  row never written, while the environment configures a provider (`OIDC_PROVIDER`) or
  SCIM (`SCIM_TOKEN`) — the two switches the loader reads the other variables by —
  imports them in one transaction holding the row: the client secret sealed,
  `SCIM_TOKEN` and `SCIM_PREVIOUS_TOKEN` kept as their digests with the overlap's
  expiry, no user behind it. The same transaction records the
  [authority mode](/en/admin/authentication/#the-authority-mode) the row derives, at
  this start and every later one. The directory and the
  provider keep working with the credentials they already have. Replicas starting
  together import once: the second finds the row written. A worker never imports.
- **The trail.** The import is audited `sso-settings.imported`, with no actor and the
  **names** of the variables it found, never their values — of `OIDC_PROVIDER`,
  `OIDC_AUTHORITY`, `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET`,
  `OIDC_WORKLOAD_IDENTITY_TOKEN_FILE` (also for a token file taken from
  `AZURE_FEDERATED_TOKEN_FILE`), `OIDC_ALLOWED_DOMAINS`, `OIDC_DISPLAY_NAME` (unless it
  is `Workforce SSO`), `SCIM_TOKEN`, `SCIM_PREVIOUS_TOKEN` and
  `SCIM_PREVIOUS_TOKEN_EXPIRES_AT`. The log says, as a warning, with the same names
  under `variables`:
  `identity: the OIDC and SCIM settings were imported from the environment into the database; the variables are now ignored and should be removed`.
  The card shows the notice to remove them.
- **A value too long for the row.** The import never fails on one. A display name
  over 100 characters is imported cut to 100, with a warning naming
  `OIDC_DISPLAY_NAME`. An `OIDC_AUTHORITY` or `OIDC_CLIENT_ID` over 200 characters,
  or an `OIDC_WORKLOAD_IDENTITY_TOKEN_FILE` over 500, cannot be cut: the provider is
  imported **off**, its secret with it, and a warning names the variable —
  `identity: the OIDC provider was imported off, since <variable> is longer than the single sign-on settings hold; set the provider on the Single sign-on card`.
  SCIM is imported as usual. Set the provider on the card.
- **The row wins afterwards.** A process that finds the row written imports nothing,
  whatever the variables say — a later change to one has no effect — and names each
  still set in a warning at every start:
  `identity: these OIDC and SCIM variables are ignored, since the single sign-on settings are kept in the database; remove them`.
  An Owner who turns the provider or SCIM off on the card is not overridden by them.
- **They are still checked at startup in this release.** The loader still validates
  them, so a malformed value, a half-removed provider (`OIDC_PROVIDER` unset with
  another `OIDC_*` left) or a `SCIM_PREVIOUS_TOKEN_EXPIRES_AT` that has passed still
  fails the start. **The next release refuses them** at startup, naming each one.

The procedure, once, on the upgrade to that release:

1. Upgrade as the [release procedure](#release-and-upgrade-procedure) says, with the
   variables still in place for the first start.
2. Check the log for the import warning, or the audit trail for
   `sso-settings.imported`, and open the **Single sign-on** card: the provider, the
   authority, the client ID, "Set on" for the secret and **Token issued** for SCIM are
   there, with the notice. It stays until the next change made on the card.
3. Remove every `OIDC_*` and `SCIM_*` variable from every API and worker container —
   `vantigo.env`, the secret store, the platform's environment — all together, and
   restart when convenient. `AZURE_FEDERATED_TOKEN_FILE`, which the workload identity
   webhook sets, may stay.
4. Verify: the next start logs no warning about the variables, local Owner sign-in,
   an SSO sign-in and a SCIM test connection still work.

An installation with none of the variables imports nothing; its row stays unwritten
until an Owner saves the card.

**Rolling back across the release** drops the row, and with it the sealed secret and
the token digests: the binary before it reads the variables again, so set them before
starting it. A SCIM token issued on the card cannot be recovered; set a new
`SCIM_TOKEN` and give it to the directory.

## Staff in a federated installation

Staff decides **per person**, by the person's account kind; on the upgrade that
brought the kinds, every person and the default were set from the
[authority mode](/en/admin/authentication/#the-authority-mode) once — `external` in a
federated installation, where SSO or SCIM is on. This section is that `external` kind;
HR may give a person `local` or, once a provider module is installed, `entra`
([Staff's account kinds](/en/user/staff/#account)). For a person of the `external`
kind the identity provider owns the account, and Staff's account provisioning only
links to it. It never creates a user and never sends an
invitation. For each person whose account Staff manages, it finds the Staff-managed
user holding the person's employee number — a user the identity provider created and an
Owner [adopted](/en/admin/authentication/#adopt-an-existing-user-for-staff) with that
number — re-enables that user when Staff disabled them (a rehire), disables them after
the employment ends, and keeps their language and employee number in step. It changes
neither their name nor their address, and gives no role: those are the provider's and
the Owner's. A user an Owner disabled stays disabled. A leaver is disabled in identity
even when the directory already deactivated them, so a later reactivation in the
directory, or SCIM turned off, does not let them back in; a rehire re-enables them in
identity, and while the directory still reports them inactive it keeps them out. Until
the provider has created the
account and an Owner has adopted it, Staff reports the person's account as not found.
For the `entra` kind no adoption is needed: the account is
[announced](#the-enterprise-extension-and-the-employee-number) and adopted on arrival.

A pending user — one Staff created for the `local` kind, before SSO or SCIM was turned on,
who has not accepted their invitation — never becomes the identity provider's account
by their address alone: OIDC's first sign-in with that address answers
`account_locked` and links nothing, and SCIM's create of a User with that address is
refused `DuplicateEmail`, with no second user. Let the pending user accept their
invitation, or have an Owner unmanage and delete them, before the provider's account
takes the address.

## Local break-glass Owner and MFA

Keep at least one local Owner account as the break-glass path even when SSO is
on. Create the first Owner through `/setup` on the fresh installation, before
anyone else can reach it; that account is also the SystemAdmin. Local password login,
passkeys and local MFA are independent of the external provider, and the card is
reached with them: a local Owner can turn a broken provider off, or issue a new SCIM
token, while the provider is down.

If the installation is reachable before an operator can complete `/setup`, set
`BOOTSTRAP_OWNER_EMAIL` instead: the same break-glass Owner is then seated by an
emailed invitation issued at startup rather than by whoever reaches `/setup`
first. See [the management listener](/en/admin/management-listener/#seating-the-first-owner-without-setup).

With `OWNERS_REQUIRE_MFA=1` (the default outside development), Owner and SystemAdmin
operations — the **Single sign-on** card among them — require a second factor: a TOTP
code, a recovery code or a passkey. Store recovery codes offline in the
organization's break-glass process. OIDC claims never satisfy this requirement and
never grant local roles.

## Release and upgrade procedure

Migrations are plain SQL files embedded in the binary and applied in order under a
PostgreSQL advisory lock. They are **forward-only**: do not plan to roll the
application binary back across a schema change.

1. Pin the exact image release, read its release notes, and take a tested PostgreSQL
   backup — for example `pg_dump --format=custom`, verified with `pg_restore --list`.
   Restore-test it somewhere safe, keep it outside the database volume, and keep the
   password out of shell history.
2. Run a terminating `migrate` job with the new image and the same database
   configuration, using `MIGRATIONS_DATABASE_URL` (the owner role) where the
   deployment separates it from the runtime role.
3. Wait for that job to exit successfully before starting the application. Migrators
   serialize on the advisory lock, so an accidentally concurrent migrator waits
   rather than corrupting the schema — but still prefer exactly one job.
4. Start `api` (or `server` replicas). **`api` applies pending migrations itself
   before it serves**, so the migration job is about seeing a failure *before* any
   serving replica starts, not about `api` leaving the schema behind. `server` mode
   never migrates.
5. Verify local Owner login, the fixed OIDC callback (if enabled), the SCIM endpoint
   (if enabled) and the Owner system-status response. Keep the backup and migration
   logs under the release retention policy.

Do not run the development-only `seed` command in production; it exits 2 outside
development.

For Compose, `vantigo-migrate` is the one-shot migration service and `vantigo` is the
long-running `api` service. `docker compose up -d` honours that dependency; for a
controlled release, run `docker compose up vantigo-migrate`, confirm it exits
successfully, then `docker compose up -d vantigo` as described in the
[Compose runbook](https://github.com/vantigo-io/vantigo/blob/main/deploy/compose/README.md).

## Secret hygiene checklist

- Never commit client secrets, SCIM tokens, `APP_SECRET`, token
  files or real tenant identifiers.
- Do not put secrets in `vantigo.env.example`, Compose YAML, container images, logs,
  tickets or shell history. The committed examples use placeholders only. Copy a new
  SCIM token from its dialog straight into the directory, nowhere else.
- Prefer the platform secret manager or an injected environment secret for what stays
  in the environment. Restrict file ownership and permissions, and give the migration
  job only the secrets it needs.
- Rotate the OIDC client secret by saving a new one on the card, and the SCIM token
  with **New token**; neither needs a restart. The bounded previous-token overlap
  exists for SCIM only.
- `APP_SECRET` is not rotatable in place: it derives session, cookie, TOTP and
  Communications channel-credential keys and the key the OIDC client secret is sealed
  under, so changing it invalidates all of them. After such a change, save the client
  secret on the card again.
- If a client secret or a SCIM token is exposed, replace it on the card at once — for
  SCIM, **New token** then **End overlap** — and every replica stops accepting the old
  one within 20 seconds; revoke the secret at the provider too. If `APP_SECRET` is
  exposed, replace it and restart all replicas. Then inspect the logs, the audit trail
  and the status endpoint for unexpected use.
