# Static identity deployment and operations

This is the production runbook for Vantigo's static identity integrations. Each
deployment has **at most one** workforce OpenID Connect provider and one
deployment-bound SCIM credential. OIDC and SCIM settings are read when the
process starts; changing a mounted file, environment variable, or secret store
entry has no effect until the application is restarted.

There is no dynamic SSO or SCIM configuration API, and there is no Owner admin UI
for adding providers, editing provider metadata, issuing SCIM tokens, or changing
SCIM scope. Use deployment configuration and the release procedure below instead.

This also holds in multi-tenant deployments: the workforce OIDC provider is
deployment-wide, not per tenant. There is no per-tenant SSO, allowed-email-domain,
or just-in-time provisioning setting, and the tenant control plane
(`/api/v1/identity/admin/tenants`) does not expose one. A per-tenant surface used
to exist but persisted settings that login never consulted, so it was removed
rather than left as a false assurance of a tenant identity boundary. There is
likewise no tenant export or purge API; tenant offboarding is a manual
operational procedure today.

## Configuration sources

Vantigo accepts the normal ASP.NET Core configuration sources. Use either:

1. environment variables, with `__` separating JSON sections; or
2. a mounted `appsettings.Production.json` (or `appsettings.json`), placed in the
   application's content root/working directory. Set `ASPNETCORE_ENVIRONMENT` or
   `DOTNET_ENVIRONMENT` to `Production` when using the environment-specific file.

For example, a published .NET container commonly uses `/app` as its content root:

```bash
docker run --read-only \
  -v /secure/vantigo/appsettings.Production.json:/app/appsettings.Production.json:ro \
  -e ASPNETCORE_ENVIRONMENT=Production \
  ghcr.io/vantigo-io/vantigo:<pinned-release> api
```

Use the actual content root shown by the deployment rather than assuming `/app`
when the image runner changes it. A mounted SCIM `BearerTokenFile` is a separate
secret-file mount; the example Compose file does not create that file for you.

Do not configure the same secret in both sources. In a container, mount the JSON
file read-only and keep its permissions limited to the application user. Prefer a
secret manager or a mounted secret file for credentials rather than putting the
credential itself in JSON or an environment dump.

### Mounted appsettings example

This example contains placeholders, not usable credentials. The OIDC client
secret is shown only to document the option name; use a secret manager or an
environment-level secret injection for the actual value. `ClientSecretFile` is
not a supported option. SCIM supports `BearerTokenFile` and
`PreviousBearerTokenFile`.

```json
{
  "App": {
    "PublicOrigin": "https://vantigo.example.com",
    "BasePath": ""
  },
  "Authentication": {
    "Oidc": {
      "Enabled": true,
      "Provider": "Entra",
      "Authority": "https://login.microsoftonline.com/00000000-0000-0000-0000-000000000000/v2.0",
      "ClientId": "11111111-1111-1111-1111-111111111111",
      "ClientAuthentication": "ClientSecret",
      "ClientSecret": "<inject-from-a-secret-store>",
      "DisplayName": "Workforce SSO"
    },
    "Scim": {
      "Enabled": true,
      "BearerTokenFile": "/run/secrets/vantigo_scim_token"
    }
  }
}
```

For Google, replace the OIDC object with the Google example in the provider
section below. For Entra WorkloadIdentity, remove `ClientSecret`, set
`ClientAuthentication` to `WorkloadIdentity`, and either set
`WorkloadIdentityTokenFile` to an absolute path or let the runtime supply
`AZURE_FEDERATED_TOKEN_FILE`.

### Environment-variable example

```dotenv
Authentication__Oidc__Enabled=true
Authentication__Oidc__Provider=Entra
Authentication__Oidc__Authority=https://login.microsoftonline.com/00000000-0000-0000-0000-000000000000/v2.0
Authentication__Oidc__ClientId=11111111-1111-1111-1111-111111111111
Authentication__Oidc__ClientAuthentication=ClientSecret
Authentication__Oidc__ClientSecret=<inject-from-secret-store-without-leading-or-trailing-whitespace>
Authentication__Oidc__DisplayName=Workforce SSO

Authentication__Scim__Enabled=true
Authentication__Scim__BearerTokenFile=/run/secrets/vantigo_scim_token
```

`Authentication__Oidc__CallbackPath` is not a deployment setting. The callback
path is fixed. If OIDC is disabled, provider settings, client credentials,
workload-token paths, and Google domains must also be absent; partial disabled
configuration fails startup. If SCIM is disabled, all SCIM credential settings
must be absent.

## Workforce OIDC

OIDC uses authorization code plus PKCE, a temporary external cookie, and the
fixed local completion path. Provider access and ID tokens are not saved. The
provider identity is only a login correlation: it does not grant local roles,
prove local MFA, or bypass the local Owner/MFA policy. New identities are
provisioned as ordinary local `User` accounts using the validated issuer and
case-sensitive `sub`; an email collision is rejected rather than auto-linked.

The public callback is:

```text
https://<public-host><base-path>/api/v1/identity/oidc/callback
```

Register that exact HTTPS URL with the provider. With the default empty
`App__BasePath`, it is `/api/v1/identity/oidc/callback`. The browser starts at
`/api/v1/identity/oidc/challenge` and local completion is
`/api/v1/identity/oidc/complete`; neither is a provider callback URI.

The built-in OIDC handler validates provider metadata, issuer, audience,
signature, state, nonce, and correlation. Pushed Authorization Requests (PAR)
are disabled. Static provider policy then applies the provider-specific claim
checks below.

### Microsoft Entra ID: client secret

1. Create or select one Entra app registration for this Vantigo deployment. Use
   a confidential web application and create a client secret in the app
   registration. Store the secret in the deployment secret manager; do not put
   it in Git, an image, or a committed `.env` file.
2. Use the tenant-specific authority exactly in this form, including `/v2.0`:

   ```text
   https://login.microsoftonline.com/<tenant-guid>/v2.0
   ```

   `<tenant-guid>` must be the tenant GUID. Authorities using `common`,
   `organizations`, `consumers`, another host, a query, or a different path do
   not satisfy startup validation.
3. Set `Provider=Entra`, the application (client) ID as `ClientId`, and
   `ClientAuthentication=ClientSecret`. Entra `ClientId` must be a GUID.
4. Register the fixed callback, including the public base path if one is used:

   ```text
   https://vantigo.example.com/api/v1/identity/oidc/callback
   ```

5. Grant only the delegated scopes needed for the sign-in flow (`openid`,
   `profile`, and `email` are requested by Vantigo). Do not treat provider
   group/role claims as Vantigo authorization grants. Do not set
   `AllowedDomains` for Entra; that option is Google-only.

After the built-in token checks, Vantigo requires a `tid` GUID matching the
configured tenant and an `oid` GUID. If the validated token has multiple `aud`
claims, its `azp` must equal the configured client ID. Provider identities remain
local `User` accounts and never become Owners through claims.

### Microsoft Entra ID: WorkloadIdentity

WorkloadIdentity replaces the OIDC **client secret used while redeeming the
authorization code**. It does not replace end-user login, does not make OIDC
claims trusted as local authorization, and does not replace the independent SCIM
bearer token.

The deployment platform must provide all of the following before Vantigo starts:

- A Kubernetes cluster with an OIDC issuer and Azure Workload Identity enabled,
  or the equivalent Azure-hosted workload identity integration.
- The workload identity mutating webhook/sidecar configuration that projects a
  service-account token into the Vantigo pod and sets
  `AZURE_FEDERATED_TOKEN_FILE` (or an explicitly configured absolute
  `WorkloadIdentityTokenFile`). The path must be readable by the Vantigo process.
- An Entra app registration whose client ID is used as Vantigo's `ClientId`.
- A federated identity credential on that app registration with:
  - **Issuer**: the exact cluster OIDC issuer URL;
  - **Subject**: the exact workload subject, normally
    `system:serviceaccount:<namespace>:<service-account>` for Kubernetes; and
  - **Audience**: `api://AzureADTokenExchange`.

The issuer, subject, and audience are compared by Azure exactly. Do not copy a
different namespace, service-account name, trailing path, or audience from
another workload. Bind the pod to the intended service account and use the
standard Azure Workload Identity labels/annotations for that platform.

A minimal Kubernetes shape is:

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

The webhook supplies the projected token volume and environment variable; do not
invent a path that is not present in the running pod. Verify the effective
`AZURE_FEDERATED_TOKEN_FILE` path and file permissions before enabling sign-in.

Configure:

```dotenv
Authentication__Oidc__Enabled=true
Authentication__Oidc__Provider=Entra
Authentication__Oidc__Authority=https://login.microsoftonline.com/<tenant-guid>/v2.0
Authentication__Oidc__ClientId=<entra-application-client-guid>
Authentication__Oidc__ClientAuthentication=WorkloadIdentity
# Optional when the platform does not set AZURE_FEDERATED_TOKEN_FILE:
# Authentication__Oidc__WorkloadIdentityTokenFile=/var/run/secrets/azure/tokens/azure-identity-token
```

Do not set `Authentication__Oidc__ClientSecret` in this mode. Vantigo checks the
file at startup, reads it fresh for every authorization-code redemption, rejects
empty/whitespace-containing assertions, clears `ClientSecret`, and sends the
assertion with type
`urn:ietf:params:oauth:client-assertion-type:jwt-bearer`. Rotate the projected
token through the workload identity platform; Vantigo does not cache it.

### Google Workspace

1. Create a Google OAuth web client and keep its client secret in the deployment
   secret manager. Google uses client-secret authentication only; set
   `ClientAuthentication=ClientSecret`.
2. Use the exact issuer:

   ```text
   https://accounts.google.com
   ```

3. Set a client ID ending in `.apps.googleusercontent.com` and register the
   fixed callback, for example:

   ```dotenv
   Authentication__Oidc__Enabled=true
   Authentication__Oidc__Provider=Google
   Authentication__Oidc__Authority=https://accounts.google.com
   Authentication__Oidc__ClientId=<client-id>.apps.googleusercontent.com
   Authentication__Oidc__ClientAuthentication=ClientSecret
   Authentication__Oidc__ClientSecret=<inject-from-secret-store>
   Authentication__Oidc__AllowedDomains__0=example.com
   ```

4. Add every permitted Workspace domain as a separate
   `Authentication__Oidc__AllowedDomains__N` value, or as entries in the JSON
   `AllowedDomains` array. Values are bare DNS names, normalized to lowercase;
   do not use `https://`, `@`, paths, or wildcards.

Vantigo requires `email_verified=true`, a syntactically valid email, a non-empty
`hd` claim matching the email domain, and an `hd` value in `AllowedDomains`.
Personal Gmail accounts and unverified or mismatched-domain identities are
rejected. WorkloadIdentity is not accepted for Google.

## Static SCIM

Enable static SCIM with `Authentication__Scim__Enabled=true`. The fixed protocol
endpoint is:

```text
/api/v1/identity/scim/v2
```

SCIM requests use `Authorization: Bearer <token>` and request bodies use
`application/scim+json`. The supported protocol resources are `Users` and
`Groups`, along with the standard service discovery resources. The current token
may be supplied directly with `BearerToken` or through an absolute readable
`BearerTokenFile`; configure one, not both. Tokens are held in startup
configuration and are never stored in the identity database.

SCIM state is persistent even though the credential is static. Vantigo retains
the deterministic static connection, user mappings, SCIM groups/memberships,
lifecycle state, ETags, and audit records in PostgreSQL. SCIM-created users are
unprivileged; Owner accounts are protected from SCIM mutation. Upstream inactive
users are made unavailable according to the static lifecycle rules, while local
access-group role mappings and local membership overrides remain local policy.

### Token rotation with overlap

The previous-token overlap is optional and is limited to 24 hours from startup.
Use this sequence:

1. Generate a new high-entropy current token and store it in the secret manager
   or mounted token file.
2. Keep the old token as `PreviousBearerToken` or
   `PreviousBearerTokenFile`, set `PreviousBearerTokenExpiresAtUtc` to a future
   UTC time no more than 24 hours after the new process starts, and restart
   Vantigo. Both tokens are accepted until the deadline.
3. Change the provisioning client's credential to the new current token. Verify
   a discovery or harmless read request and inspect the status endpoint.
4. After every client has switched, remove the previous-token setting **and its
   expiry**, then restart again. An expiry without a previous token is invalid.

The current and previous token values cannot be the same, and direct values
cannot be combined with their corresponding `*File` options. A failed startup
validation is safer than silently accepting an invalid rotation configuration.

### Status and operational evidence

An authenticated Owner can read:

```text
GET /api/v1/identity/owner/system-status
```

The response contains total, active, and disabled user counts; whether static
OIDC and static SCIM are enabled; the configured static OIDC provider name; and
best-effort timestamps for the last successful static OIDC sign-in and
authenticated SCIM request:

- `totalUsers`
- `activeUsers`
- `disabledUsers`
- `staticOidcEnabled`
- `staticOidcProvider`
- `staticScimEnabled`
- `lastStaticOidcSignInAtUtc`
- `lastAuthenticatedScimRequestAtUtc`

The timestamp writes are operational projections, not authentication-critical
state. A telemetry/database write failure must not turn a successful OIDC or
SCIM operation into a failed operation. The endpoint never returns bearer tokens,
client secrets, token-file contents, or secret-reference names. Treat null
timestamps as “no successful use has been recorded yet.”

## Local break-glass Owner and MFA

Keep at least one local Owner account as the break-glass path even when OIDC is
enabled. Create the first Owner through `/setup` with the one-time
`Authentication__Bootstrap__Secret`, then remove or rotate that bootstrap secret.
Local password login and local MFA are independent of the external provider.

If `Authentication__Owners__RequireMfa=true`, Owner business and management
operations require local authenticator MFA. Store recovery codes offline in the
organization's break-glass process. OIDC claims never satisfy this local MFA
requirement and never grant Owner or other local roles.

## Release, backup, and migration constraint

The Phase 2/3 static-only release removes the former database-managed federation
and SCIM control-plane schema. The forward cleanup migration intentionally
destructively removes old provider configuration/state, old SCIM token rows, and
non-static federation/SCIM connection data. It retains local identity data and
the deterministic static SCIM state. This release is suitable only for a database
that has **no real use of the removed dynamic federation/SCIM configuration or
data**. Do not treat the migration as a conversion or recovery mechanism.

The cleanup is forward-only and must be treated as irreversible; do not plan to
roll back the application binary across the schema cleanup. Before upgrading:

1. Confirm that no removed provider/control-plane configuration or non-static
   SCIM connection is needed. If there is any uncertainty, stop and take an
   application/database owner decision before proceeding.
2. Pin the exact Vantigo image release, read its release notes, and take a tested
   PostgreSQL backup. For example, use `pg_dump --format=custom` with database
   credentials supplied by the secret manager, then verify it with
   `pg_restore --list`. Restore-test the dump in a safe environment and keep the
   backup outside the database volume; do not put the password in shell history.
3. Run a terminating `migrate` job with the new image and the same database
   configuration. The host `api` command does not apply migrations. Migrators
   serialize on an installation-wide PostgreSQL advisory lock, so an
   accidentally concurrent migrator waits for the first and then re-runs
   idempotently rather than corrupting the schema; still prefer running exactly
   one job, and for destructive cleanup releases (like this one) stop or drain
   the API first so no old binary serves requests against the new schema.
4. Wait for the migration job to exit successfully before starting the API. Do
   not run the Development-only `seed` command in production.
5. Start the new API, verify local Owner login, the fixed OIDC callback (if
   enabled), the SCIM endpoint (if enabled), and the Owner system-status
   response. Keep the backup and migration logs under the release retention
   policy.

For Compose, `vantigo-migrate` is the one-shot migration service and `vantigo` is
the long-running `api` service. `docker compose up -d` honors that dependency;
for a controlled release, run `docker compose up vantigo-migrate`, confirm the
service exits successfully, then run `docker compose up -d vantigo` as described
in the [Compose runbook](../deploy/compose/README.md). Do not run more than one
migrator concurrently.

## Secret hygiene checklist

- Never commit client secrets, SCIM tokens, bootstrap secrets, token files, or
  real tenant identifiers with credentials.
- Do not put secrets in `vantigo.env.example`, Compose YAML, container images,
  mounted appsettings checked into source control, logs, tickets, or shell
  history. The committed examples use placeholders only.
- Prefer the platform secret manager or a read-only mounted secret file. Restrict
  file ownership and permissions and ensure the migration job receives only the
  secrets it needs.
- Rotate OIDC client secrets and SCIM tokens through the provider/secret manager,
  then restart Vantigo. Use the bounded previous-token overlap only for SCIM.
- If a secret is exposed, revoke it immediately, replace it, restart all
  replicas, and inspect logs and the status endpoint for unexpected use.
