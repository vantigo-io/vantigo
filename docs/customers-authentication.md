# Vantigo identity, authentication and deployment

Vantigo uses ASP.NET Core Identity with an application cookie for the browser SPA and
same-origin API. The API returns JSON `401`/`403` responses rather than login
redirects. Mutating browser requests use antiforgery protection: obtain a token from
`GET /api/v1/identity/antiforgery` and send it in `X-XSRF-TOKEN`.

For static workforce OIDC, SCIM 2.0, group mappings, provider setup, and the
operator recovery runbook, see [SSO and SCIM operations](sso-scim-operations.md).

The application cookie and antiforgery cookie are `HttpOnly` and `SameSite=Strict`, and
are Secure outside Development. Run the SPA and API under one public origin in
deployment. A reverse proxy may serve both paths, but it must preserve the host and
scheme and forward exactly one trusted `X-Forwarded-For`/`X-Forwarded-Proto` hop.
Cross-origin SPA/API hosting is not the supported deployment shape.

## First owner and local accounts

There is one deliberate, one-time bootstrap path:

1. Outside Development, the deployment operator **must** set
   `Authentication__Bootstrap__Secret` (Key Vault-backed via an ACA secret reference,
   or an equivalent secret store) before starting a blank install. The configured
   secret is used exactly and is never logged. If it is unset, startup fails
   immediately with a configuration error instead of starting the API: with multiple
   replicas, a per-process generated secret would be unpredictable to reach behind a
   load balancer, and every replica would additionally log its own value.
   In Development only, an unset secret is generated in memory and printed once at
   backend `Warning` log level for the operator to use at `/setup`; it is not
   persisted, and restarting before setup generates a new secret. Never rely on this
   fallback outside Development.
2. Visit `/setup`, enter the bootstrap secret manually, and provide the first Owner's
   email, display name, and password. The secret is never sent to the browser
   automatically; it is submitted only when the operator completes setup.
3. Remove or rotate the bootstrap secret after the Owner account is created.

`GET /api/v1/identity/bootstrap-status` is anonymous and returns `{ "available": true|false }`.
Signed-out visitors are directed to `/setup` only while bootstrap is available. The
generated or configured secret is sensitive and must be treated like any other
bootstrap credential. Setup and bootstrap become unavailable after the first local
Owner is created. The direct `POST /api/v1/identity/bootstrap` endpoint remains available for an
explicitly orchestrated flow; send the secret, Owner details, and password over the
protected deployment path. The operation is transactionally guarded against concurrent
bootstrap requests. It creates the local `Owner` and `User` roles; the bootstrap
account is an ordinary local password account.
Local Owner accounts are the break-glass path even when workforce OIDC is enabled.

Owners can invite `User` or `Owner` accounts from `/settings`. Invitation tokens are
opaque, stored only as hashes, expire after seven days by default, and can be
configured for one to thirty days (`Authentication__Invitations__Lifetime` uses a
standard .NET `TimeSpan`, for example `7.00:00:00`). Only one active invitation is
retained for an email lifecycle; replacement and failed delivery revoke the
previous/new token as appropriate.

Password recovery uses Identity's protected reset tokens (24-hour lifetime) and a
generic request response so it does not disclose whether an email exists. The reset
operation only reports password-policy details after the token has been validated.

Owner MFA uses Identity's authenticator provider (six-digit TOTP) and one-use recovery
codes. It is disabled by default. Set `Authentication__Owners__RequireMfa=true` to
require MFA for Owner business access and invitation management. Assisted Owner-MFA
reset always requires a separately verified local MFA session.
MFA enrollment/status remains available to let a new Owner enroll. Recovery codes are
shown once; an MFA-authenticated Owner can reset another Owner's local MFA, which
invalidates that account's existing session and requires re-enrollment.

## Invitation and recovery URLs

The current frontend routes are:

- Invitation acceptance: `/invitations/accept?token=...` (also supported:
  `/accept-invitation?token=...`).
- Password recovery request: `/forgot-password`.
- Password reset: `/password-reset?email=...&token=...` (also supported:
  `/reset-password?email=...&token=...`).

The backend defaults already match the canonical mailed routes. For a deployed
public origin, set `App__PublicOrigin` (scheme + host only); the mailed links
are then derived automatically from origin + base path (the host defaults to the
root; see `App__BasePath`):

```text
App__PublicOrigin=https://vantigo.example.com
# yields https://vantigo.example.com/invitations/accept?token=...
# and    https://vantigo.example.com/password-reset?email=...&token=...
```

When the mailed links must differ from the derived defaults, set the explicit
templates instead — they always take precedence and must retain the
placeholders:

```text
Authentication__Invitations__AcceptUrl=https://vantigo.example.com/invitations/accept?token={token}
Authentication__PasswordReset__ResetUrl=https://vantigo.example.com/password-reset?email={email}&token={token}
```

An invalid `App__PublicOrigin` (path, query, missing scheme) fails startup, and
a startup warning is logged when explicit templates disagree with the
configured origin and base path.

Do not put tokens in source control or static configuration. The `{email}` and
`{token}` values are URL-encoded by the backend.

## Email delivery

The temporary Logging sender is a Development/pre-production fallback, not a
production delivery mechanism. By explicit current design, recipient and generated
invitation/reset link contents appear in application logs; anyone with log access can
use those bearer links. Keep this provider confined to non-production environments.

For production, use the direct SMTP sender:

```text
Email__Provider=Smtp
Email__From=no-reply@vantigo.example.com
Email__Smtp__Host=smtp.example.com
Email__Smtp__Port=587
Email__Smtp__UserName=<smtp-user-from-secret-store>
Email__Smtp__Password=<smtp-password-from-secret-store>
Email__Smtp__EnableSsl=true
Email__Smtp__TimeoutSeconds=30
```

`Email__Smtp__Host` is required for SMTP. With TLS enabled, the sender uses STARTTLS;
the username is optional for servers that do not require authentication. Do not use
real credentials in configuration examples.

## Optional static workforce OIDC

This is the startup-configured OIDC path. At most one workforce OpenID
Connect provider can be configured, and its settings are not persisted in the
accounts database. OIDC is disabled
unless `Authentication__Oidc__Enabled=true`. The supported providers are Entra
and Google. `Authority`, `ClientId`, and the selected client-authentication
settings are required and invalid partial configuration fails startup. The
optional display name defaults to `Workforce SSO`; the callback path is fixed at
`/api/v1/identity/oidc/callback`.

`Authentication__Oidc__ClientSecret` is the direct runtime secret injected into the
process for client-secret authentication. Entra may instead use
`ClientAuthentication=WorkloadIdentity`, which reads the projected assertion
fresh for each authorization-code redemption. Use the [static identity operations
guide](sso-scim-operations.md) for deployment-bound SCIM configuration.

The flow is authorization code plus PKCE. ASP.NET Core's built-in handler validates
provider metadata, issuer, audience, signature, state, nonce, and correlation. The
validated principal is held in Identity's temporary external cookie and consumed by
the fixed local completion route `/api/v1/identity/oidc/complete`; provider tokens are not saved.
The browser starts the flow at `/api/v1/identity/oidc/challenge`.

New OIDC identities are provisioned just in time as local `User` accounts, keyed by
the validated issuer and case-sensitive `sub`. Provider email is informational: it
is never used to link to an existing local account. An email collision fails rather
than auto-linking. A provider identity without an email receives a reserved,
non-deliverable local address. OIDC claims never grant local roles or prove local
MFA; an existing local account's local MFA policy still applies. Keep the local Owner
bootstrap account as the break-glass path.

All OIDC settings use the same neutral environment-variable names regardless of
provider. Use one of these supported provider-specific shapes; the examples are
placeholders, not credentials:

**Microsoft Entra ID**

```text
Authentication__Oidc__Enabled=true
Authentication__Oidc__Provider=Entra
Authentication__Oidc__Authority=https://login.microsoftonline.com/<tenant-id>/v2.0
Authentication__Oidc__ClientId=<application-client-id>
Authentication__Oidc__ClientAuthentication=ClientSecret
Authentication__Oidc__ClientSecret=<client-secret-from-secret-store>
```

**Google Workspace**

```text
Authentication__Oidc__Enabled=true
Authentication__Oidc__Provider=Google
Authentication__Oidc__Authority=https://accounts.google.com
Authentication__Oidc__ClientId=<client-id>.apps.googleusercontent.com
Authentication__Oidc__ClientAuthentication=ClientSecret
Authentication__Oidc__ClientSecret=<client-secret-from-secret-store>
Authentication__Oidc__AllowedDomains__0=example.com
```

For Entra WorkloadIdentity, replace `ClientAuthentication=ClientSecret` with
`ClientAuthentication=WorkloadIdentity`, omit `ClientSecret`, and configure an
absolute `Authentication__Oidc__WorkloadIdentityTokenFile` or provide
`AZURE_FEDERATED_TOKEN_FILE`. See the [static identity operations guide](sso-scim-operations.md)
for the required Azure Workload Identity federation setup. WorkloadIdentity is
Entra-only; Google is client-secret-only.

These examples describe the supported static provider shapes; use test tenants and
secret stores appropriate to the deployment.

### Required callback URI

Register the exact public HTTPS callback URI with the provider. The application is
served under its base path (the host defaults to the root; see `App__BasePath`), so the
public callback URI includes that prefix:

```text
https://vantigo.example.com/api/v1/identity/oidc/callback
```

When `App__PublicOrigin` is configured, the API logs the exact callback URI to
register at startup. When `App__BasePath` is set to an empty value (root
serving), omit the prefix.

The callback path is fixed at `/api/v1/identity/oidc/callback`; it is not a
deployment setting. The public origin, forwarded scheme, and provider registration
must agree; the application does not accept a browser-supplied return URL.

Environment variables use ASP.NET Core's standard double-underscore mapping:

| Variable | Purpose | Default |
| --- | --- | --- |
| `App__BasePath` | Path prefix the app is served under on a shared domain (empty value serves from the root) | empty |
| `App__PublicOrigin` | Public scheme + host used to derive mailed links and the logged OIDC callback URI (no path; invalid values fail startup) | unset |
| `Authentication__Bootstrap__Secret` | One-time Owner bootstrap secret | **required outside Development** (startup fails if unset); in Development only, generated once at startup and logged at Warning |
| `Authentication__Owners__RequireMfa` | Require local MFA for Owner access/management | `false` |
| `Authentication__Owners__MfaIssuer` | Issuer label in authenticator apps | `Vantigo` |
| `Authentication__Invitations__Lifetime` | Invitation lifetime as a .NET `TimeSpan` (`1`–`30` days) | `7.00:00:00` |
| `Authentication__Invitations__AcceptUrl` | Invitation URL template with `{token}` (override) | derived from `App__PublicOrigin` + `App__BasePath`; dev fallback `http://localhost:5173/invitations/accept?token={token}` |
| `Authentication__PasswordReset__ResetUrl` | Reset URL template with `{email}` and `{token}` (override) | derived from `App__PublicOrigin` + `App__BasePath`; dev fallback `http://localhost:5173/password-reset?email={email}&token={token}` |
| `Authentication__Oidc__Enabled` | Enable the one startup-configured OIDC provider | `false` |
| `Authentication__Oidc__Provider` | Supported provider: `Entra` or `Google` | unset |
| `Authentication__Oidc__Authority` | Static OIDC issuer/authority | unset |
| `Authentication__Oidc__ClientId` | Static OIDC client ID | unset |
| `Authentication__Oidc__ClientAuthentication` | `ClientSecret` or Entra-only `WorkloadIdentity` | unset |
| `Authentication__Oidc__ClientSecret` | OIDC client secret when using `ClientSecret` | unset |
| `Authentication__Oidc__WorkloadIdentityTokenFile` | Absolute projected Entra assertion file for `WorkloadIdentity` | unset |
| `Authentication__Oidc__AllowedDomains__0` | Allowed Google Workspace hosted domain | unset |
| `Authentication__Oidc__DisplayName` | Sign-in button/provider label | `Workforce SSO` |
| `Authentication__Oidc__CallbackPath` | Retained only to reject non-fixed callback configuration; do not set it | `/api/v1/identity/oidc/callback` |
| `Authentication__Scim__Enabled` | Enable the fixed static SCIM protocol | `false` |
| `Authentication__Scim__BearerToken` | Static SCIM bearer token; configure this or `BearerTokenFile`, not both | unset |
| `Authentication__Scim__BearerTokenFile` | Absolute readable file containing the current static SCIM token | unset |
| `Authentication__Scim__PreviousBearerToken` | Optional prior token during rotation; requires a bounded expiry | unset |
| `Authentication__Scim__PreviousBearerTokenFile` | Optional absolute file containing the prior token; mutually exclusive with the direct value | unset |
| `Authentication__Scim__PreviousBearerTokenExpiresAtUtc` | Future UTC expiry for the prior token, no more than 24 hours after startup | unset |
| `DataProtection__PostgreSql__ConnectionString` | Optional override for the PostgreSQL connection used for Data Protection keys | `ConnectionStrings__Vantigo` |
| `DataProtection__PostgreSql__ConnectionStringName` | Named connection-string lookup used when the override is unset | `Vantigo` |
| `DataProtection__PostgreSql__Schema` | PostgreSQL schema for the Data Protection key table | `dataprotection` |
| `DataProtection__PostgreSql__TableName` | PostgreSQL Data Protection key table | `Keys` |
| `DataProtection__PostgreSql__ApplicationName` | Shared Data Protection application discriminator | host application name |
| `ForwardedHeaders__KnownProxies` | Trusted proxy IP(s), comma-separated or indexed | ASP.NET Core safe defaults |
| `ForwardedHeaders__KnownNetworks__0` | Trusted proxy network in IPv4/IPv6 CIDR form | ASP.NET Core safe defaults |
| `Email__Provider` | `Smtp` selects SMTP; any other value selects logging | `Logging` |
| `Email__From` | Sender mailbox | `no-reply@localhost` |
| `Email__Smtp__Host` | SMTP server | unset |
| `Email__Smtp__Port` | SMTP port | `587` |
| `Email__Smtp__UserName` | Optional SMTP username | unset |
| `Email__Smtp__Password` | SMTP password | unset |
| `Email__Smtp__EnableSsl` | Use STARTTLS when true | `true` |
| `Email__Smtp__TimeoutSeconds` | SMTP timeout, clamped to 1–300 seconds | `30` |

### Data Protection keys

ASP.NET Core Data Protection keys are persisted in the shared PostgreSQL database
in the `dataprotection."Keys"` table by default. All replicas must use the same
database and `DataProtection__PostgreSql__ApplicationName` (or the same default
application name) so cookies, antiforgery tokens, and protected Identity tokens
remain compatible across restarts and replicas. The key ring is managed through the
PostgreSQL-backed Data Protection context rather than an application file volume.

For higher-assurance deployments, external at-rest wrapping of the database or key
material is recommended. Vantigo does not currently provide a product-level
configuration for that wrapping; it must be implemented and operated by the
deployment or an extension. Do not represent external wrapping as a built-in
Vantigo feature.

### Forwarded headers and HTTPS

The API consumes `X-Forwarded-For` and `X-Forwarded-Proto` with a one-hop limit. When
an explicit proxy or network allowlist is supplied, it replaces the framework's
default trusted lists; only configure the actual proxy address or ingress network.
Invalid IP/CIDR values fail startup. Examples:

```text
ForwardedHeaders__KnownProxies=10.0.0.10
ForwardedHeaders__KnownNetworks__0=10.0.0.0/24
```

Do not trust arbitrary client-supplied forwarded headers. Outside Development,
production cookies are always Secure and OIDC authorities must use HTTPS. Terminate
TLS at the trusted proxy, forward the original HTTPS scheme, and expose the SPA/API
and OIDC callback on that HTTPS origin.

### Database migrations

The host keeps the Identity, Customers, Communications and Products contexts in one
PostgreSQL database. Their tables live in the `identity`, `customers`,
`communications` and `products` schemas, each with its own
`__EFMigrationsHistory`. The contexts share the NpgsqlDataSource's ADO.NET
connection pool, not EF DbContext pooling; they do not share tracking or transactions.
See the [contributor migration commands](../CONTRIBUTING.md#database-migrations) for
context-specific add, list, script, update, and pending-model checks.

Migrations run only through the host's `migrate` command, which applies all enabled
module and Identity contexts and exits.
In production, run it as a terminating job, wait for it to succeed, and then start the
API with the explicit `api` command. The API does not apply migrations at startup. The
platform must not start the API until the migration job has completed successfully.

Development Aspire explicitly selects `migrate`, then the Development-only `seed`, and
then `api`. Do not run `seed` in production. Verify the database is reachable and the
PostgreSQL-backed Data Protection key context is available before accepting browser
traffic.
