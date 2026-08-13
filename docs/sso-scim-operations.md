# SSO and SCIM operations

This is the operator guide for enterprise identity integrations. It covers the
persisted, owner-managed federation connections and their SCIM connections. The
older, single-provider environment configuration is documented separately in
[Identity, authentication and deployment](customers-authentication.md).

## Choose the OIDC operating model

Vantigo has two deliberately different OIDC paths:

| Path | Configuration and lifecycle | Sign-in routes |
| --- | --- | --- |
| **Persisted multi-provider SSO** | Owners create provider connections through the identity control plane. The database stores provider configuration and a client-secret *reference*, never client-secret material. Each connection must pass discovery validation before it can be enabled. | `GET /api/v1/identity/federation/providers`, `GET /api/v1/identity/federation/{connectionId}/challenge`, and `GET`/`POST /api/v1/identity/federation/callback` |
| **Legacy static OIDC** | One optional workforce provider is configured at process startup with `Authentication__Oidc__Authority`, `Authentication__Oidc__ClientId`, and `Authentication__Oidc__ClientSecret`. It is not a persisted connection and does not use the `VANTIGO_SSO_*_CLIENT_SECRET` reference namespace. | `GET /api/v1/identity/providers`, `GET /api/v1/identity/oidc/challenge`, the configured callback (default `/api/v1/identity/oidc/callback`), and `GET /api/v1/identity/oidc/complete` |

Do not mix the two configuration models. Persisted connections use the dynamic
routes and a `clientSecretReference`; the legacy path uses the static
`Authentication__Oidc__*` values and supports only one provider. For legacy OIDC
only, `Authentication__Oidc__ClientSecret` contains the actual direct runtime
secret (injected through the deployment secret mechanism); it is not a
`VANTIGO_SSO_*_CLIENT_SECRET` reference. Never put that direct secret into a
persisted connection, and never put a persisted connection's reference into the
legacy `Authentication__Oidc__ClientSecret` setting. Both paths use authorization
code flow and validate issuer, audience, nonce, state, and token signatures. Neither
path turns upstream claims into Owner or local MFA authority.

## Persisted SSO setup

Persisted federation management is owner-authorized. The control-plane routes are:

```text
GET    /api/v1/identity/access/federation-connections
GET    /api/v1/identity/access/federation-connections/{id}
POST   /api/v1/identity/access/federation-connections
PUT    /api/v1/identity/access/federation-connections/{id}
POST   /api/v1/identity/access/federation-connections/{id}/validate
POST   /api/v1/identity/access/federation-connections/{id}/enable
POST   /api/v1/identity/access/federation-connections/{id}/disable
DELETE /api/v1/identity/access/federation-connections/{id}
```

Create or update a connection with provider type `Entra`, `Google`, or `Generic`,
an HTTPS `authority`, a client ID, `allowedDomains` (required for Google), and a
`jitCreationMode` of `Disabled` or `CreateUser`. Updates invalidate validation and
disable the connection; validate the new configuration and enable it again. Use
the returned `concurrencyStamp` for subsequent mutations. A connection must be
validated before it can be enabled or made the default.

The public provider list is `GET /api/v1/identity/federation/providers`. A sign-in
button starts at
`GET /api/v1/identity/federation/{connectionId}/challenge`; completion always uses
`GET /api/v1/identity/federation/callback` or
`POST /api/v1/identity/federation/callback`. The callback is shared by all
persisted connections and is not the legacy static callback. Register the exact
public HTTPS URL, including `App__BasePath` when one is configured:

```text
https://vantigo.example.com/api/v1/identity/federation/callback
```

The challenge accepts only the server-controlled return paths `/` and `/sign-in`.
Do not construct provider authorization, token, or JWKS URLs in a browser or put a
return URL supplied by an end user into a provider registration.

### Client-secret references

The persisted connection field `clientSecretReference` must name an exact process
environment variable in the `VANTIGO_SSO_*_CLIENT_SECRET` namespace. The value is
injected by the deployment secret manager, for example:

```dotenv
VANTIGO_SSO_ENTRA_CLIENT_SECRET=<secret-injected-by-the-deployment>
VANTIGO_SSO_GOOGLE_CLIENT_SECRET=<secret-injected-by-the-deployment>
```

The connection stores only the reference, such as
`VANTIGO_SSO_ENTRA_CLIENT_SECRET`. Vantigo resolves that exact environment
variable when enabling or running a flow; it does not resolve arbitrary
configuration aliases. The API and audit projections do not return the secret.
Keep the reference and its value out of source control and logs.

## Provider setup

### Microsoft Entra ID

Use a tenant-specific issuer. Do not use `common`, `organizations`, or
`consumers`:

```text
authority=https://login.microsoftonline.com/<tenant-id>/v2.0
clientSecretReference=VANTIGO_SSO_ENTRA_CLIENT_SECRET
```

Operator steps:

1. In the Microsoft Entra admin center, register an application for the Vantigo
   web sign-in. Add the exact dynamic callback URL as a **Web** redirect URI:
   `https://vantigo.example.com/api/v1/identity/federation/callback` (include the
   configured `App__BasePath` in the public URL). Create a client secret and inject
   its value as `VANTIGO_SSO_ENTRA_CLIENT_SECRET`; set the persisted connection's
   `clientSecretReference` to that exact variable name.
2. Configure the tenant-specific issuer
   `https://login.microsoftonline.com/<tenant-id>/v2.0` and the application client
   ID. Do not use `common`, `organizations`, or `consumers`. Validate discovery,
   then enable the connection only after validation succeeds.
3. Create the Entra enterprise application provisioning configuration with the
   Vantigo SCIM base URL from the [SCIM section](#scim-20). Put the generated SCIM
   bearer token in the enterprise application's **Secret Token** / bearer-token
   field; send it as `Authorization: Bearer <token>` to Vantigo. Do not put the
   bearer token in a query string or in the OIDC client-secret reference.
4. In **Users and groups**, assign only the users and groups that should be
   provisioned to the enterprise application. Keep the assignment scope narrow
   during rollout. Provisioning sends Entra `userPrincipalName` as SCIM
   `userName`, and sends the directory `objectId` as SCIM `externalId`.
5. Test the Entra enterprise application connection and run a controlled user and
   group provisioning test. Confirm the SCIM user has `userName` equal to
   `userPrincipalName`, `externalId` equal to the object ID GUID, and that group
   membership arrives as expected. Test OIDC sign-in for an assigned user. Disable
   the federation connection, verify that sign-in stops, then re-enable it and
   repeat the sign-in smoke test. Treat a failed disable/re-enable test as a
   rollout blocker.

Vantigo validates that the `tid` claim matches the tenant in the issuer and records
the directory object identity from `oid`. For Entra SCIM, `externalId` must be the
object ID GUID (the `oid` value), normalized as a GUID and immutable after mapping.
This `(tenant, object-id)` correlation is used to join SCIM-provisioned users to
their OIDC sign-in; email is not the correlation key. For provider-side details,
use Microsoft's official [Configure Microsoft Entra ID for SCIM provisioning]
(https://learn.microsoft.com/entra/identity/app-provisioning/use-scim-to-provision-users-and-groups)
and [Add an enterprise application]
(https://learn.microsoft.com/entra/identity/enterprise-apps/add-application-portal),
and verify the current Entra admin-center labels for the release in use.

### Google Workspace

Use exactly the Google issuer below; the persisted provider validator rejects an
authority with another host or a path. In Google Cloud Console, create an OAuth
client for a web application, configure the consent screen for the Workspace
organization, and add the exact dynamic callback URL as an authorized redirect URI:
`https://vantigo.example.com/api/v1/identity/federation/callback` (including
`App__BasePath` when configured). Inject the client secret as
`VANTIGO_SSO_GOOGLE_CLIENT_SECRET` and use that name as `clientSecretReference`.

```text
authority=https://accounts.google.com
clientSecretReference=VANTIGO_SSO_GOOGLE_CLIENT_SECRET
allowedDomains=["example.com"]
```

Set the Google Workspace domain in `allowedDomains` and ensure the consent screen
and Workspace app access policy cover that organization. At sign-in, the email must
be verified, the `hd` claim must be present, `hd` must match the email domain, and
both must be in the configured allowed-domain list. This is an organization
restriction, not merely a cosmetic sign-in hint. Test sign-in with an assigned
Workspace user and with a user outside the allowed domain; the latter must be
rejected.

Google Workspace OIDC does not imply that a tenant has an outbound SCIM client
available. Generic SCIM client capability is customer/edition dependent. If the
directory cannot act as a standards-compliant SCIM client, use OIDC alone or an
approved provisioning bridge; do not assume that Google Workspace will push to the
SCIM endpoint.

### Generic OIDC

Generic connections use the issuer returned by OIDC discovery. Discovery and the
authorization, token, and JWKS endpoints must be HTTPS, must not redirect, and
must resolve to public addresses. The application rejects private, loopback,
link-local, and other non-public targets during validation and runtime checks.

That application check is not a replacement for platform egress policy. Restrict
the API's outbound HTTPS egress to approved issuer and identity-provider hosts,
and use an approved issuer-host allowlist where possible. Account for DNS
rebinding between validation and later metadata, token, and JWKS requests. The
same controls should cover SMTP and any configured OTLP collector without allowing
unnecessary internet egress.

## SCIM 2.0

SCIM is separate from browser authentication. Every SCIM request uses a bearer
token issued for one SCIM connection; browser cookies and antiforgery tokens are
not accepted. SCIM request bodies must use `application/scim+json`, and SCIM
responses and errors use the SCIM media type. Configure the directory's base URL
to the exact path below. When the application is mounted below a prefix, compose
the prefix before `/api`; for example, with `App__BasePath=/vantigo` and
`App__PublicOrigin=https://example.com`, use:

```text
https://example.com/vantigo/api/v1/identity/scim/v2
```

With the default empty base path, the equivalent shape is
`https://vantigo.example.com/api/v1/identity/scim/v2`.

The protocol resources are:

```text
GET    /api/v1/identity/scim/v2/ServiceProviderConfig
GET    /api/v1/identity/scim/v2/Schemas
GET    /api/v1/identity/scim/v2/ResourceTypes

POST   /api/v1/identity/scim/v2/Users
GET    /api/v1/identity/scim/v2/Users
GET    /api/v1/identity/scim/v2/Users/{id}
PUT    /api/v1/identity/scim/v2/Users/{id}
PATCH  /api/v1/identity/scim/v2/Users/{id}
DELETE /api/v1/identity/scim/v2/Users/{id}

POST   /api/v1/identity/scim/v2/Groups
GET    /api/v1/identity/scim/v2/Groups
GET    /api/v1/identity/scim/v2/Groups/{id}
PATCH  /api/v1/identity/scim/v2/Groups/{id}
DELETE /api/v1/identity/scim/v2/Groups/{id}
```

The implementation supports SCIM Users and Groups, user filtering by `userName`
or `externalId`, group filtering by `displayName` or `externalId`, pagination,
PATCH, ETags, and SCIM lifecycle deactivation. Bulk and sort are not supported;
the service advertises a maximum page size of 100 and rejects request bodies over
256 KiB. Send `Authorization: Bearer <token>` on every request.

### Pepper and token lifecycle

Configure the pepper by reference, not by putting it in the database or in a
connection record:

```dotenv
Authentication__Scim__TokenPepperReference=VANTIGO_SCIM_DIRECTORY_TOKEN_PEPPER
VANTIGO_SCIM_DIRECTORY_TOKEN_PEPPER=<secret-injected-by-the-deployment>
```

`Authentication__Scim__TokenPepperReference` must point to a matching
`VANTIGO_SCIM_*_TOKEN_PEPPER` environment variable. SCIM tokens are stored only
as peppered hashes. The raw token is shown once when a SCIM connection is created
or rotated; it cannot be retrieved later.

SCIM connection control is owner-authorized:

```text
GET    /api/v1/identity/access/scim
POST   /api/v1/identity/access/scim
POST   /api/v1/identity/access/scim/{id}/enable
POST   /api/v1/identity/access/scim/{id}/disable
POST   /api/v1/identity/access/scim/{id}/rotate
POST   /api/v1/identity/access/scim/{id}/revoke
DELETE /api/v1/identity/access/scim/{id}
GET    /api/v1/identity/access/scim/{id}/users
PUT    /api/v1/identity/access/scim/{id}/users/{userId}/override
```

Create the SCIM connection against a validated federation connection and choose
`Authoritative` or `Additive` mode. Copy the returned token directly into the
directory configuration. Rotation creates a new current token and keeps the
previous current token valid for a ten-minute overlap, allowing the directory
configuration to be updated. Revoke invalidates all active tokens **and disables
the SCIM connection**. Disable stops processing without deleting the retained
provenance. The SCIM delete route is intentionally non-destructive and returns a
provenance conflict for retained connections.

Changing the configured pepper invalidates every existing token immediately: the
old hashes can no longer be verified. Plan a pepper change as an outage-sensitive
operation. The exact sequence is: (1) revoke the SCIM connection, which disables
it; (2) update the deployment's pepper reference/value without exposing the value;
(3) rotate or create a new SCIM token as the API permits; (4) update the directory
bearer-token field; (5) explicitly enable the SCIM connection; and (6) smoke-test
SCIM provisioning, including a harmless read and a controlled user/group check.
The ten-minute rotation overlap applies only when the pepper remains unchanged.

## Lifecycle, groups, and operator controls

- OIDC JIT accounts are keyed by the validated issuer and case-sensitive `sub`.
  Provider email never links to an existing local account. Dynamic JIT accounts
  receive no application roles or permissions until an Owner explicitly assigns
  them or maps a SCIM group to them; SCIM-created users likewise receive no roles
  automatically.
- A SCIM connection is **Authoritative** by default. An authoritative mapping
  whose upstream `active` value is false is effectively disabled. **Additive** mode
  provisions and updates users but does not make upstream inactivity disable the
  local account. SCIM deprovisioning disables rather than deletes users so history
  and provenance remain available.
- SCIM groups own their synchronized membership facts. Owners can map an eligible
  SCIM group to an ordinary local role through the group role-mapping routes. Owner,
  system, built-in, and roles carrying non-delegable authorization-management
  permissions cannot be mapped from a group.
- Local groups use the same access-group API, but local member changes are not a
  way to edit SCIM-owned membership. Keep provider membership and local operator
  decisions separate.

The access-group routes used for operator mapping are:

```text
GET    /api/v1/identity/access/groups
GET    /api/v1/identity/access/groups/{id}
POST   /api/v1/identity/access/groups
PUT    /api/v1/identity/access/groups/{id}
DELETE /api/v1/identity/access/groups/{id}
POST   /api/v1/identity/access/groups/{groupId}/role-mappings/{roleId}
PUT    /api/v1/identity/access/groups/{groupId}/role-mappings/{roleId}
DELETE /api/v1/identity/access/groups/{groupId}/role-mappings/{roleId}
```

For a SCIM group, include its `scimConnectionId` in the role-mapping mutation and
use the current group `concurrencyStamp`. Review effective access after changing a
mapping; group membership and role mapping are separate durable facts.

An Owner can apply a documented lifecycle override to a mapped non-Owner user:

```text
PUT /api/v1/identity/access/scim/{id}/users/{userId}/override
```

The body contains the current mapping `etag`, a reason, and `override` set to
`ForceEnable`, `ForceDisable`, or `null` to clear the override. Overrides are
separate from the upstream fact and are concurrency checked. They cannot be used
to control an Owner account. Treat `ForceEnable` as a temporary exception and
record the owner, reason, expiry/review date, and upstream ticket in the change
record.

## Ingress, egress, and secrets

- Serve the SPA, API, OIDC callbacks, and SCIM endpoint through one public HTTPS
  origin. Set `App__PublicOrigin` to the scheme and host only and set
  `App__BasePath` separately when mounting below a path prefix.
- Terminate TLS at a trusted reverse proxy, preserve the public host and HTTPS
  scheme, and configure `ForwardedHeaders__KnownProxies` or
  `ForwardedHeaders__KnownNetworks__0` for exactly the proxy/network that can
  connect to the API. Vantigo consumes one forwarded hop; do not trust arbitrary
  client-supplied forwarded headers.
- Restrict inbound SCIM traffic at the proxy or network boundary to the directory
  where practical, while retaining bearer authentication and the application rate
  limit. Never expose a raw SCIM token in a URL, log, ticket, or monitoring label.
- Permit outbound HTTPS only to the approved OIDC discovery/authorization/token/JWKS
  hosts, SMTP, and explicitly configured telemetry destinations. Generic OIDC
  validation cannot replace an egress firewall.

## Migration, backup, and recovery runbook

The host has one migration command for all enabled modules, Identity, and the
PostgreSQL-backed Data Protection key context. Use exactly one migrator at a time.
Do not run `migrate` concurrently with another migration job or start the API
before it succeeds. `seed` is Development-only.

For Compose, back up PostgreSQL before an upgrade or identity change, then let the
one-shot migration service complete before serving traffic:

```bash
docker compose exec -T postgres pg_dump -U "${POSTGRES_USER:-vantigo}" -d vantigo > vantigo-backup.sql
docker compose pull
docker compose up -d
docker compose logs vantigo-migrate
```

Confirm the migration job completed successfully, then check the API and a local
Owner sign-in before changing provider or directory settings. Keep backups of the
whole `vantigo` database, including the `identity` and `dataprotection` schemas;
restoring only application tables can invalidate cookies and protected account
tokens.

For a non-Compose deployment, run the terminating migrator once and wait for a
successful exit before starting the long-running API. The migrator needs only the
PostgreSQL database connection and the matching Data Protection PostgreSQL
configuration/application name: the same database,
`DataProtection__PostgreSql__ConnectionString` or
`DataProtection__PostgreSql__ConnectionStringName`, schema, table, and
`DataProtection__PostgreSql__ApplicationName` that the API will use. The migrator
does **not** need persisted SSO client-secret references, SCIM pepper settings, or
the legacy static OIDC client secret. Supply those runtime authentication secrets
only to the long-running `api` process, without exposing values in command history
or logs. Do not start `api` with a different database or Data Protection
configuration/application name:

```bash
docker run --rm \
  -e ConnectionStrings__vantigo="Host=your-postgres;Database=vantigo;Username=...;Password=..." \
  -e DataProtection__PostgreSql__Schema=dataprotection \
  -e DataProtection__PostgreSql__TableName=Keys \
  -e DataProtection__PostgreSql__ApplicationName=Vantigo \
  ghcr.io/vantigo-io/vantigo migrate

docker run -d \
  --name vantigo \
  -p 8080:8080 \
  -e ConnectionStrings__vantigo="Host=your-postgres;Database=vantigo;Username=...;Password=..." \
  -e DataProtection__PostgreSql__Schema=dataprotection \
  -e DataProtection__PostgreSql__TableName=Keys \
  -e DataProtection__PostgreSql__ApplicationName=Vantigo \
  -e VANTIGO_SSO_<NAME>_CLIENT_SECRET=<injected-secret> \
  -e Authentication__Scim__TokenPepperReference=VANTIGO_SCIM_<NAME>_TOKEN_PEPPER \
  -e VANTIGO_SCIM_<NAME>_TOKEN_PEPPER=<injected-pepper> \
  ghcr.io/vantigo-io/vantigo api
```

The API example's placeholder secret references are an inventory, not literal
values to copy. Supply only the references and injected values required by the
enabled connections; keep direct secrets in the deployment secret manager rather
than in shell history. Do not add those authentication secrets to the migration
job.

### Break-glass and incident recovery

1. Keep at least one tested local Owner account with a known recovery path. Local
   Owner accounts are the break-glass path when every OIDC provider is unavailable;
   OIDC and SCIM cannot grant Owner or protected administration authority.
2. Require local Owner MFA in production with
   `Authentication__Owners__RequireMfa=true`, store recovery codes offline, and
   test a local `/login` sign-in before enabling a new provider. Do not remove the
   last local Owner while relying on federation.
3. If SSO is failing, use the local Owner to disable the affected persisted
   connection at
   `POST /api/v1/identity/access/federation-connections/{id}/disable`, or use the
   local account if the static provider is unavailable. Validate the corrected
   connection before enabling it again.
4. If a SCIM token is exposed, follow this exact sequence: call
   `POST /api/v1/identity/access/scim/{id}/revoke` (this disables the connection),
   then call `POST /api/v1/identity/access/scim/{id}/rotate` to issue a replacement
   token, update the directory's bearer token, call
   `POST /api/v1/identity/access/scim/{id}/enable`, and smoke-test provisioning.
   If the pepper is exposed, replace it and perform the explicit pepper-rotation
   sequence above, including enable and verification.
5. If data is corrupted, stop the API and migrator, preserve logs and audit data,
   restore the complete PostgreSQL backup to a controlled instance, verify the
   `identity` and `dataprotection` schemas, run one `migrate` job for the target
   release if required, and only then restart `api`.

## Data Protection key storage

ASP.NET Core Data Protection keys are persisted in the shared PostgreSQL database
in the `dataprotection."Keys"` table by default. All replicas must use the same
database and `DataProtection__PostgreSql__ApplicationName` (or the same default
application name) so cookies, antiforgery tokens, and protected Identity tokens
remain compatible across restarts and replicas. The key ring is managed through the
PostgreSQL-backed Data Protection context rather than an application file volume.

For higher-assurance deployments, external at-rest wrapping of the database or
key material is recommended. Vantigo does not currently provide a product-level
configuration for that wrapping; it must be implemented and operated by the
deployment or an extension. Do not represent external wrapping as a built-in
Vantigo feature.
