# Customers authentication and deployment

The Customers application uses ASP.NET Core Identity with an application cookie for
the browser SPA and same-origin API. The API returns JSON `401`/`403` responses rather
than login redirects. Mutating browser requests use antiforgery protection: obtain a
token from `GET /auth/antiforgery` and send it in `X-XSRF-TOKEN`.

The application cookie and antiforgery cookie are `HttpOnly` and `SameSite=Strict`, and
are Secure outside Development. Run the SPA and API under one public origin in
deployment. A reverse proxy may serve both paths, but it must preserve the host and
scheme and forward exactly one trusted `X-Forwarded-For`/`X-Forwarded-Proto` hop.
Cross-origin SPA/API hosting is not the supported deployment shape.

## First owner and local accounts

There is one deliberate, one-time bootstrap path:

1. The deployment operator may set `Authentication__Bootstrap__Secret` in the
   environment or a secret store before starting a blank install. When configured,
   that secret is used and is never logged.
   If it is unset, the app generates a temporary high-entropy startup-only secret and
   prints it once at backend `Warning` log level for the operator to use at `/setup`.
   It is not persisted; restarting before setup generates a new secret.
2. Visit `/setup`, enter the bootstrap secret manually, and provide the first Owner's
   email, display name, and password. The secret is never sent to the browser
   automatically; it is submitted only when the operator completes setup.
3. Remove or rotate the bootstrap secret after the Owner account is created.

`GET /auth/bootstrap-status` is anonymous and returns `{ "available": true|false }`.
Signed-out visitors are directed to `/setup` only while bootstrap is available. The
generated or configured secret is sensitive and must be treated like any other
bootstrap credential. Setup and bootstrap become unavailable after the first local
Owner is created. The direct `POST /auth/bootstrap` endpoint remains available for an
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
are then derived automatically from origin + base path (default `/customers`,
see `App__BasePath`):

```text
App__PublicOrigin=https://vantigo.example.com
# yields https://vantigo.example.com/customers/invitations/accept?token=...
# and    https://vantigo.example.com/customers/password-reset?email=...&token=...
```

When the mailed links must differ from the derived defaults, set the explicit
templates instead — they always take precedence and must retain the
placeholders:

```text
Authentication__Invitations__AcceptUrl=https://vantigo.example.com/customers/invitations/accept?token={token}
Authentication__PasswordReset__ResetUrl=https://vantigo.example.com/customers/password-reset?email={email}&token={token}
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
Email__From=no-reply@customers.example.com
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

## Optional workforce OIDC

At most one workforce OpenID Connect provider can be configured. OIDC is disabled
when all three required values are absent. Once any required value is supplied,
`Authority`, `ClientId`, and `ClientSecret` are all required and invalid partial
configuration fails startup. The optional display name defaults to `Workforce SSO`;
the callback path defaults to `/auth/oidc/callback`.

The flow is authorization code plus PKCE. ASP.NET Core's built-in handler validates
provider metadata, issuer, audience, signature, state, nonce, and correlation. The
validated principal is held in Identity's temporary external cookie and consumed by
the fixed local completion route `/auth/oidc/complete`; provider tokens are not saved.
The browser starts the flow at `/auth/oidc/challenge`.

New OIDC identities are provisioned just in time as local `User` accounts, keyed by
the validated issuer and case-sensitive `sub`. Provider email is informational: it
is never used to link to an existing local account. An email collision fails rather
than auto-linking. A provider identity without an email receives a reserved,
non-deliverable local address. OIDC claims never grant local roles or prove local
MFA; an existing local account's local MFA policy still applies. Keep the local Owner
bootstrap account as the break-glass path.

All OIDC settings use the same neutral environment-variable names regardless of
provider:

```text
Authentication__Oidc__Authority=https://issuer.example.com
Authentication__Oidc__ClientId=<client-id>
Authentication__Oidc__ClientSecret=<client-secret-from-secret-store>
Authentication__Oidc__DisplayName=Workforce SSO
Authentication__Oidc__CallbackPath=/auth/oidc/callback
```

Minimal configuration shapes (not provider test claims):

**Microsoft Entra ID**

```text
Authentication__Oidc__Authority=https://login.microsoftonline.com/<tenant-id>/v2.0
Authentication__Oidc__ClientId=<application-client-id>
Authentication__Oidc__ClientSecret=<client-secret-from-secret-store>
```

**Okta**

```text
Authentication__Oidc__Authority=https://<okta-org>.okta.com/oauth2/default
Authentication__Oidc__ClientId=<application-client-id>
Authentication__Oidc__ClientSecret=<client-secret-from-secret-store>
```

These are generic configuration examples only; no real provider has been tested or
is implied by this documentation.

### Required callback URI

Register the exact public HTTPS callback URI with the provider. The application is
served under its base path (default `/customers`, see `App__BasePath`), so the
public callback URI includes that prefix:

```text
https://vantigo.example.com/customers/auth/oidc/callback
```

When `App__PublicOrigin` is configured, the API logs the exact callback URI to
register at startup. When `App__BasePath` is set to an empty value (root
serving), omit the prefix.

If `Authentication__Oidc__CallbackPath` is changed, register the same public origin
plus that exact path. It must be an absolute path below `/auth/oidc/`, without a
query, fragment, traversal, or trailing slash, and it must not be
`/auth/oidc/challenge` or `/auth/oidc/complete`. The public origin, forwarded scheme,
and provider registration must agree; the application does not accept a browser-
supplied return URL.

## Deployment configuration reference

Environment variables use ASP.NET Core's standard double-underscore mapping:

| Variable | Purpose | Default |
| --- | --- | --- |
| `App__BasePath` | Path prefix the app is served under on a shared domain (empty value serves from the root) | `/customers` |
| `App__PublicOrigin` | Public scheme + host used to derive mailed links and the logged OIDC callback URI (no path; invalid values fail startup) | unset |
| `Authentication__Bootstrap__Secret` | One-time Owner bootstrap secret | unset; generated once at startup and logged at Warning |
| `Authentication__Owners__RequireMfa` | Require local MFA for Owner access/management | `false` |
| `Authentication__Owners__MfaIssuer` | Issuer label in authenticator apps | `Vantigo` |
| `Authentication__Invitations__Lifetime` | Invitation lifetime as a .NET `TimeSpan` (`1`–`30` days) | `7.00:00:00` |
| `Authentication__Invitations__AcceptUrl` | Invitation URL template with `{token}` (override) | derived from `App__PublicOrigin` + `App__BasePath`; dev fallback `http://localhost:5173/invitations/accept?token={token}` |
| `Authentication__PasswordReset__ResetUrl` | Reset URL template with `{email}` and `{token}` (override) | derived from `App__PublicOrigin` + `App__BasePath`; dev fallback `http://localhost:5173/password-reset?email={email}&token={token}` |
| `Authentication__Oidc__Authority` | OIDC issuer/authority; required to enable OIDC | unset |
| `Authentication__Oidc__ClientId` | OIDC client ID; required to enable OIDC | unset |
| `Authentication__Oidc__ClientSecret` | OIDC client secret; required to enable OIDC | unset |
| `Authentication__Oidc__DisplayName` | Sign-in button/provider label | `Workforce SSO` |
| `Authentication__Oidc__CallbackPath` | OIDC callback path | `/auth/oidc/callback` |
| `DataProtection__KeysPath` | Persistent Data Protection key directory; relative paths are under the content root | unset |
| `DataProtection__ApplicationName` | Shared Data Protection application discriminator | framework default |
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

Set a writable, persistent `DataProtection__KeysPath` in every replica. A relative
path is resolved below the API content root; an absolute path is used as-is. Set the
same `DataProtection__ApplicationName` for replicas of this application. Shared,
persistent keys are required for cookie validation and protected Identity tokens
across restarts and replicas. Protect the directory with filesystem/secret-store
controls and do not commit its contents.

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

The Customers API keeps `CustomersDbContext` and `AccountsDbContext` in the same
assembly and PostgreSQL database. Customers tables and configurations live under
`Database/Customers/`, with the default `public.__EFMigrationsHistory`. Identity and
account entities live under `Database/Accounts/`, with the separate
`accounts.__EFMigrationsHistory`. The contexts share the NpgsqlDataSource's ADO.NET
connection pool, not EF DbContext pooling; they do not share tracking or transactions.
See the [contributor migration commands](../CONTRIBUTING.md#database-migrations) for
context-specific add, list, script, update, and pending-model checks.

Migrations run only through the `migrate` command, which applies both contexts and exits.
In production, run it as a terminating job, wait for it to succeed, and then start the
API with the explicit `api` command. The API does not apply migrations at startup. The
platform must not start the API until the migration job has completed successfully.

Development Aspire explicitly selects `migrate`, then the Development-only `seed`, and
then `api`. Do not run `seed` in production. Verify the database is reachable and the
persistent Data Protection directory is ready before accepting browser traffic.
