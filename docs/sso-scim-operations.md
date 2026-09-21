# Static identity deployment and operations

This is the production runbook for Vantigo's static identity integrations. Each
deployment has **at most one** workforce OpenID Connect provider and one
deployment-bound SCIM credential. Both are read from the environment when the process
starts; changing a variable, a mounted file or a secret-store entry has no effect
until the application is restarted.

There is no dynamic SSO or SCIM configuration API, and no Owner admin UI for adding
providers, editing provider metadata, issuing SCIM tokens or changing SCIM scope. Use
deployment configuration and the release procedure below instead.

Vantigo is a single-tenant application. There is no tenant control plane, no
per-tenant SSO or provisioning setting, and nothing to scope a provider to: the
workforce OIDC provider and the SCIM credential are installation-wide.

## Configuration

Configuration comes from **environment variables only**. There is no
`appsettings.json`, no configuration-file search path and no environment-name
variable; `APP_ENV` (`production` by default, or `development`) is the only
environment switch, and only development relaxes anything.

Every setting is validated in one pass at startup and **every** problem is reported
at once, so a misconfigured container fails its first boot with the complete list.
[`apps/server/internal/config/config.go`](../apps/server/internal/config/config.go)'s
field comments are the authoritative reference.

```bash
docker run --read-only \
  --env-file /secure/vantigo/vantigo.env \
  ghcr.io/vantigo-io/vantigo:<pinned-release> api
```

Prefer a platform secret manager or an injected environment secret for every
credential. Secrets do not belong in the image, in Compose YAML, in Git or in an
environment dump that gets logged.

### Environment example

```dotenv
APP_URL=https://vantigo.example.com

OIDC_PROVIDER=entra
OIDC_AUTHORITY=https://login.microsoftonline.com/00000000-0000-0000-0000-000000000000/v2.0
OIDC_CLIENT_ID=11111111-1111-1111-1111-111111111111
OIDC_CLIENT_SECRET=<inject-from-secret-store>
OIDC_DISPLAY_NAME=Workforce SSO

SCIM_TOKEN=<inject-from-secret-store>
```

`OIDC_PROVIDER` and `SCIM_TOKEN` are the on/off switches: unset disables that
integration. **With `OIDC_PROVIDER` unset, leaving any other `OIDC_*` variable set
fails startup**, naming the leftovers — a half-removed provider cannot look disabled
while still carrying live credentials. The callback path is fixed and is not a
setting.

## Workforce OIDC

OIDC uses the authorization code flow with PKCE and a nonce, a sealed state cookie,
and a fixed local completion path. Provider access and ID tokens are never saved. The
provider identity is only a login correlation: it does not grant local roles, prove
local MFA, or bypass the local Owner/MFA policy. New identities are provisioned just
in time as ordinary local `User` accounts keyed by the validated issuer and
case-sensitive `sub`; an email collision is rejected rather than auto-linked.

The public callback is:

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

There is no `ClientAuthentication` setting. Vantigo decides from **which credential
is present**:

| Credential set | Mode |
| --- | --- |
| `OIDC_CLIENT_SECRET` | Client secret |
| `OIDC_WORKLOAD_IDENTITY_TOKEN_FILE`, or `AZURE_FEDERATED_TOKEN_FILE` | Workload identity (Entra only) |
| Both | **Startup fails** — they are mutually exclusive |
| Neither | **Startup fails** — one is required |

### Microsoft Entra ID: client secret

1. Create or select one Entra app registration for this deployment. Use a
   confidential web application, create a client secret, and store it in the
   deployment secret manager.
2. Use the tenant-specific authority exactly in this form, including `/v2.0`:

   ```text
   https://login.microsoftonline.com/<tenant-guid>/v2.0
   ```

   `<tenant-guid>` must be the tenant GUID. Authorities using `common`,
   `organizations`, `consumers`, another host, a query or a different path do not
   satisfy startup validation.
3. Set `OIDC_PROVIDER=entra`, the application (client) ID as `OIDC_CLIENT_ID` (it
   must be a GUID), and `OIDC_CLIENT_SECRET`.
4. Register the fixed callback, including the public base path if one is used.
5. Grant only the delegated scopes the sign-in flow needs (`openid`, `profile`,
   `email`). Do not treat provider group or role claims as Vantigo authorization
   grants. Do not set `OIDC_ALLOWED_DOMAINS` for Entra — it is Google-only and is
   rejected here.

After the built-in token checks, Vantigo requires a `tid` GUID matching the tenant in
the configured authority and an `oid` GUID. If the validated token carries multiple
`aud` claims, its `azp` must equal the configured client ID. Provider identities
remain local `User` accounts and never become Owners through claims.

### Microsoft Entra ID: workload identity

Workload identity replaces the **client secret used while redeeming the authorization
code**. It does not replace end-user login, does not make OIDC claims trusted as
local authorization, and does not replace the independent SCIM bearer token.

The deployment platform must provide all of the following before Vantigo starts:

- A Kubernetes cluster with an OIDC issuer and Azure Workload Identity enabled, or
  the equivalent Azure-hosted integration.
- The mutating webhook/sidecar configuration that projects a service-account token
  into the pod and sets `AZURE_FEDERATED_TOKEN_FILE` (or an explicitly configured
  absolute `OIDC_WORKLOAD_IDENTITY_TOKEN_FILE`). The path must be readable by the
  Vantigo process — **startup checks that it is an absolute path and a readable
  file**, and fails otherwise.
- An Entra app registration whose client ID is used as `OIDC_CLIENT_ID`.
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

Configure:

```dotenv
OIDC_PROVIDER=entra
OIDC_AUTHORITY=https://login.microsoftonline.com/<tenant-guid>/v2.0
OIDC_CLIENT_ID=<entra-application-client-guid>
# Optional when the platform does not set AZURE_FEDERATED_TOKEN_FILE:
# OIDC_WORKLOAD_IDENTITY_TOKEN_FILE=/var/run/secrets/azure/tokens/azure-identity-token
```

Do not set `OIDC_CLIENT_SECRET` in this mode; the combination fails startup. The
assertion is read **fresh from the file for every authorization-code redemption** and
sent with
`client_assertion_type=urn:ietf:params:oauth:client-assertion-type:jwt-bearer`.
Rotate the projected token through the platform; Vantigo does not cache it.

### Google Workspace

1. Create a Google OAuth web client and keep its secret in the deployment secret
   manager. Google is client-secret only — workload identity is not accepted.
2. Use the exact issuer `https://accounts.google.com`.
3. Use a client ID ending in `.apps.googleusercontent.com` and register the fixed
   callback.
4. List every permitted Workspace domain in `OIDC_ALLOWED_DOMAINS` as a comma list of
   bare DNS names. **At least one is required for Google.** Values are lower-cased
   and de-duplicated; a URL, an address, a port, whitespace or a single label is
   refused.

```dotenv
OIDC_PROVIDER=google
OIDC_AUTHORITY=https://accounts.google.com
OIDC_CLIENT_ID=<client-id>.apps.googleusercontent.com
OIDC_CLIENT_SECRET=<inject-from-secret-store>
OIDC_ALLOWED_DOMAINS=example.com,example.org
```

Vantigo requires `email_verified`, a syntactically valid email, and a non-empty `hd`
claim that matches the email domain and appears in `OIDC_ALLOWED_DOMAINS`. Personal
Gmail accounts and unverified or mismatched-domain identities are rejected.

## SCIM

Enable SCIM by setting `SCIM_TOKEN`. The fixed protocol endpoint is:

```text
/api/v1/identity/scim/v2
```

Requests authenticate with `Authorization: Bearer <token>` and use
`application/scim+json`. The supported resources are `Users` and `Groups`, plus the
standard `ServiceProviderConfig`, `ResourceTypes` and `Schemas` discovery endpoints.
The token must not contain whitespace, and it is compared by digest rather than
directly.

**There is no file-based token variant.** `SCIM_TOKEN` and `SCIM_PREVIOUS_TOKEN` are
the only inputs — there is no `BearerTokenFile` or `PreviousBearerTokenFile`. Inject
the value from a secret manager.

SCIM state is persistent even though the credential is static: user mappings, SCIM
groups and memberships, lifecycle state, ETags and audit records live in PostgreSQL.
SCIM-created users are unprivileged, and **Owner accounts are protected from SCIM
mutation** — an update, patch or delete targeting an Owner is refused, which keeps the
break-glass account outside the provisioning system's reach.

### Token rotation with overlap

The previous-token overlap is optional and bounded to 24 hours from startup:

1. Generate a new high-entropy token and store it in the secret manager.
2. Set the new value as `SCIM_TOKEN`, keep the old one as `SCIM_PREVIOUS_TOKEN`, set
   `SCIM_PREVIOUS_TOKEN_EXPIRES_AT` to an RFC 3339 timestamp in the future and no
   more than 24 hours ahead, and restart. Both tokens are accepted until the
   deadline.
3. Change the provisioning client's credential to the new token. Verify a discovery
   or harmless read request and check the Owner status endpoint.
4. After every client has switched, remove **both** `SCIM_PREVIOUS_TOKEN` and its
   expiry, then restart again.

The rules are enforced at startup, and each is a boot failure rather than a silently
accepted rotation:

- `SCIM_PREVIOUS_TOKEN` must differ from `SCIM_TOKEN`.
- `SCIM_PREVIOUS_TOKEN` requires `SCIM_PREVIOUS_TOKEN_EXPIRES_AT`, and the expiry
  requires the token — neither is valid alone.
- The expiry must be in the future **at every start** and at most 24 hours ahead. A
  restart after the window has passed therefore fails configuration: remove both
  variables once the overlap is over.

### Status and operational evidence

An authenticated Owner can read:

```text
GET /api/v1/identity/owner/system-status
```

The response carries:

- `total`, `active`, `disabled` — user counts
- `pendingInvitations`
- `staticOidcEnabled`, `staticOidcProvider`
- `staticScimEnabled`
- `lastStaticOidcSignInAtUtc`, `lastAuthenticatedScimRequestAtUtc`

The two timestamps are operational projections, not authentication-critical state: a
telemetry or database write failure must not turn a successful OIDC or SCIM operation
into a failed one. The endpoint never returns bearer tokens, client secrets or
secret-reference names. Treat null timestamps as "no successful use has been recorded
yet".

## Local break-glass Owner and MFA

Keep at least one local Owner account as the break-glass path even when OIDC is
enabled. Create the first Owner through `/setup` on the fresh installation, before
anyone else can reach it; that account is also the SystemAdmin. Local password login,
passkeys and local MFA are independent of the external provider.

If the installation is reachable before an operator can complete `/setup`, set
`BOOTSTRAP_OWNER_EMAIL` instead: the same break-glass Owner is then seated by an
emailed invitation issued at startup rather than by whoever reaches `/setup`
first. See [the management listener](management.md#seating-the-first-owner-without-setup).

With `OWNERS_REQUIRE_MFA=1` (the default outside development), Owner and SystemAdmin
operations require a second factor — a TOTP code, a recovery code or a passkey. Store
recovery codes offline in the organization's break-glass process. OIDC claims never
satisfy this requirement and never grant local roles.

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
[Compose runbook](../deploy/compose/README.md).

## Secret hygiene checklist

- Never commit client secrets, SCIM tokens, `APP_SECRET`, token
  files or real tenant identifiers.
- Do not put secrets in `vantigo.env.example`, Compose YAML, container images, logs,
  tickets or shell history. The committed examples use placeholders only.
- Prefer the platform secret manager or an injected environment secret. Restrict file
  ownership and permissions, and give the migration job only the secrets it needs.
- Rotate OIDC client secrets and SCIM tokens through the provider or secret manager,
  then restart. The bounded previous-token overlap exists for SCIM only.
- `APP_SECRET` is not rotatable in place: it derives session, cookie, TOTP and
  Communications channel-credential keys, so changing it invalidates all of them.
- If a secret is exposed, revoke it immediately, replace it, restart all replicas, and
  inspect logs and the status endpoint for unexpected use.
