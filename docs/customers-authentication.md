# Vantigo identity, authentication and deployment

Vantigo authenticates the browser SPA and the same-origin API with an application
cookie issued by the identity module. The API answers JSON `401`/`403` problems
rather than login redirects.

**There is no antiforgery token endpoint and no `X-XSRF-TOKEN` header.** Cross-site
request forgery is refused by origin, not by a token: the server wraps every `/api`
request in Go's `http.CrossOriginProtection`, which rejects unsafe cross-site browser
requests using `Sec-Fetch-Site` (falling back to `Origin` compared against `Host`).
`APP_URL` is registered as a trusted origin so a proxy that rewrites `Host` does not
turn same-origin requests into rejections, a rejection answers `403` as an RFC 7807
problem, and a request carrying neither header — a non-browser client — passes.
Session cookies are `SameSite=Strict` as the second layer.

Run the SPA and API under one public origin. A reverse proxy may serve both paths,
but it must preserve the `Host` header: `X-Forwarded-Host` is never honoured, and
host filtering is derived from `APP_URL`. Cross-origin SPA/API hosting is not a
supported deployment shape.

For static workforce OIDC, SCIM 2.0, provider setup and the operator recovery
runbook, see [SSO and SCIM operations](sso-scim-operations.md).

Every setting this document names is read by
[`apps/server/internal/config/config.go`](../apps/server/internal/config/config.go),
whose field comments are the authoritative reference. Configuration is parsed once
at startup and **every** problem is reported at once, so a misconfigured container
fails its first boot with the complete list instead of one restart per mistake.

## Cookies

| Cookie | Purpose | Attributes |
| --- | --- | --- |
| `vantigo.session` | The authenticated session token | `HttpOnly`, `SameSite=Strict`, scoped to the base path |
| `vantigo.2fa` | A pending two-factor sign-in's ticket, 5 minutes | `HttpOnly`, `SameSite=Strict` |
| `vantigo.oidc` | OIDC state, nonce and PKCE verifier, sealed | `HttpOnly`, `SameSite=Lax` |
| `vantigo.identity.external` | The validated external identity between callback and completion, sealed | `HttpOnly`, `SameSite=Lax` |

All four are `Secure` exactly when `APP_URL` is an https origin (a `Secure` cookie
never reaches an http one). The two OIDC cookies are `SameSite=Lax` on
purpose: the provider's redirect back is a cross-site top-level navigation, which
`Lax` admits and `Strict` would not. Both are sealed with AES-256-GCM under
distinct purposes, so a state cookie cannot be replayed as an external identity.

A persistent session cookie (`rememberMe` on the 2FA step) carries the standard
absolute lifetime as its `Max-Age`; the server still enforces the tighter
privileged bounds per request, whatever the cookie says.

## First Owner and local accounts

There is one deliberate, one-time bootstrap path, and it needs no secret: on a fresh
installation, visit `/setup` and provide the first Owner's email, display name and
password. That account becomes the installation's full administrator, holding both
`Owner` (every permission in every module) and `SystemAdmin` (`/admin` and
maintenance mode). No configuration names it, and no other path grants `SystemAdmin`.

**Complete setup before the installation is reachable by anyone else.** Whoever
submits `/setup` first owns the installation. While no Owner exists the process logs
a `WARN` at startup saying so; once the Owner exists, setup and bootstrap refuse every
further request.

`GET /api/v1/identity/bootstrap-status` is anonymous and returns
`{"available": true|false}`. `POST /api/v1/identity/bootstrap` remains available for
an orchestrated flow:

```bash
curl -X POST https://vantigo.example.com/api/v1/identity/bootstrap \
  -H 'Content-Type: application/json' \
  -d '{"email":"owner@example.com","displayName":"Owner","password":"a strong password"}'
```

Every change to who holds Owner — bootstrap, Owner creation, Owner invitation
acceptance and Owner demotion — serializes on a transaction advisory lock, so
concurrent bootstrap requests cannot both win. Setup and bootstrap become
unavailable once the first Owner exists. The bootstrap account is an ordinary local
password account, and a local Owner remains the break-glass path even when workforce
OIDC is enabled.

**`BOOTSTRAP_OWNER_EMAIL` replaces `/setup` with an emailed invitation**, for a
deployment that is reachable before an operator can sign in. While it is set and no
Owner exists, `/setup` and `POST /api/v1/identity/bootstrap` answer the same 409
`bootstrap_unavailable` a consumed bootstrap gets, and every start mails that
address an Owner invitation — once: a pending invitation is not re-sent before it
expires, and an expired one is re-issued on the next start. The invitation follows
the configured address, not a fixed one: changing `BOOTSTRAP_OWNER_EMAIL` while no
Owner exists revokes the invitation already issued to the previous address and
mails the new one on the next start, so a mistyped address does not keep the
installation's only live Owner token. An address some account already holds is
never invited — a warning is logged instead. A failed send revokes the invitation
and logs the failure without stopping startup; the next start retries. Whichever
account accepts the invitation becomes the installation's Owner and SystemAdmin,
exactly as `/setup` would create it.

The installation's first Owner holds `Owner` and `SystemAdmin`, and the bootstrap
marker is written, **however that account is created** — `/setup`'s anonymous form
and an accepted Owner invitation reach the same outcome.

Owners invite `User` or `Owner` accounts from `/settings`. Invitation tokens are
opaque and stored only as hashes. `INVITATION_LIFETIME` defaults to `168h` (seven
days) and is accepted from `24h` to `720h`; a value outside that range fails
startup.

Password recovery answers generically so it never discloses whether an address
exists, and password-policy detail is reported only after the token validates.

## MFA

MFA is a six-digit TOTP authenticator plus one-use recovery codes, and passkeys
(WebAuthn) are a first-class second factor: a sign-in that verified a TOTP code, a
recovery code **or** a passkey is recorded as MFA-verified for that session.

`OWNERS_REQUIRE_MFA` requires MFA for the Owner and SystemAdmin policies. It
defaults **on outside development and off in development**. Outside development the
process refuses to start unless it is on or `OWNERS_ALLOW_INSECURE_NO_MFA=1`
deliberately accepts running without it — configuration reports:

```text
OWNERS_REQUIRE_MFA: must not be 0 outside development unless OWNERS_ALLOW_INSECURE_NO_MFA=1
(accepts that a compromised Owner or SystemAdmin password alone reaches every identity
and tenant control-plane endpoint)
```

The opt-out exists for demo deployments; set it deliberately, not by default. It takes
both keys: `OWNERS_REQUIRE_MFA=0` turns the requirement off, and
`OWNERS_ALLOW_INSECURE_NO_MFA=1` lets the process start with it off. The acknowledgement
alone changes nothing — the requirement stays on, and an administrator without an
enrolled authenticator is refused by every module and control-plane endpoint.
`MFA_ISSUER` (default `Vantigo`) is the label authenticator apps show.

MFA enrollment and status stay reachable to a session that has not enrolled yet, so
turning the requirement on never locks out the account that must enable it. Such a
session carries `mfaEnrollmentRequired: true` (GET `/session`, the sign-in and
invitation responses, GET `/account/mfa`) whenever the account holds Owner or
SystemAdmin, the requirement is on, and no authenticator is enrolled. The frontend
holds that session at `/settings/security`: every other signed-in page redirects
there, the page explains what to do, and enabling the authenticator marks the
session MFA-verified so access returns without a new sign-in.
Recovery codes are shown once. An MFA-authenticated Owner can reset another Owner's
MFA, which ends that account's sessions and requires re-enrollment.

## Session lifetime and revocation

Four bounds are enforced on every request. A privileged bound may not exceed its
standard counterpart, or startup fails.

| Key | Default | Meaning |
| --- | --- | --- |
| `SESSION_IDLE_TIMEOUT` | `8h` | Idle window for a standard session |
| `SESSION_PRIVILEGED_IDLE_TIMEOUT` | `2h` | Idle window for an Owner or SystemAdmin session |
| `SESSION_ABSOLUTE_LIFETIME` | `24h` | Hard cap on a standard session, from sign-in |
| `SESSION_PRIVILEGED_ABSOLUTE_LIFETIME` | `8h` | Hard cap on an Owner or SystemAdmin session |

The idle window slides, but the write is throttled: `last_seen_at` is rewritten only
once it is older than `min(idle/4, 5 minutes)`, not on every request. Presenting
credentials again is the only thing that starts a new absolute lifetime.

**Revocation takes effect on the very next request.** Every request resolves the
session cookie to a row in `identity.sessions` and evaluates the operation's access
rule against that row and the user's current roles — there is no cached revocation
state and no security-stamp convergence window, so a revocation, a disable or a
role change applies immediately across every replica.

- `POST /api/v1/identity/account/sessions/revoke` signs the calling account out
  everywhere, including the browser that made the request.
- `POST /api/v1/identity/system/users/{userId}/sessions/revoke` does the same for
  any account and requires SystemAdmin.

Each sign-in also purges that user's dead sessions (revoked, or past the standard
absolute lifetime), so the table stays bounded by sessions that could still be valid.

## Rate limits

Authentication endpoints are rate limited by a fixed-window limiter whose counters
live in PostgreSQL (`platform.rate_limit`), so a limit holds **across replicas and
restarts** rather than per process. The limit is applied before the access check.

| Policy | Limit | Applies to |
| --- | --- | --- |
| `Login` | 100 / minute | `POST /login` |
| `login-attempts` | 10 / minute | Failed passwords for one email from one address, keyed `EMAIL\|ip`; answers without `Retry-After` |
| `Bootstrap` | 20 / minute | `POST /bootstrap` |
| `Mfa` | 20 / 5 minutes | The 2FA step, MFA management and passkey registration |
| `PasskeyLogin` | 30 / 5 minutes | Passkey sign-in |
| `PasswordRecovery` | 10 / 15 minutes | Recovery request, reset and account password change |
| `Invitations` | 30 / minute | Owner invitation management |
| `InvitationAcceptance` | 20 / minute | Invitation validate and accept |
| `UserManagement` | 30 / minute | Owner user management |
| `OwnerAvatarRead` | 300 / minute | Owner avatar reads |

Every policy except `login-attempts` keys on the client address, which is why
`TRUSTED_PROXY_HOPS` and `TRUSTED_PROXY_CIDRS` matter behind a proxy: get them
wrong and every request shares one bucket, or a client picks its own.

## Invitation and recovery URLs

The frontend routes are `/invitations/accept?token=…`, `/forgot-password` and
`/password-reset?email=…&token=…`.

Mailed links are derived from `APP_URL` plus `APP_BASE_PATH` by default, so setting
the public origin is usually all that is needed:

```text
APP_URL=https://vantigo.example.com
# yields https://vantigo.example.com/invitations/accept?token=...
# and    https://vantigo.example.com/password-reset?email=...&token=...
```

When the mailed links must differ, set the templates explicitly. Each must contain
its placeholders literally or startup fails:

```text
INVITATION_ACCEPT_URL=https://vantigo.example.com/invitations/accept?token={token}
PASSWORD_RESET_URL=https://vantigo.example.com/password-reset?email={email}&token={token}
```

`APP_URL` must be an origin only — scheme, host and optional port, no path, query or
credentials; a path prefix belongs in `APP_BASE_PATH`. `{email}` and `{token}` are
URL-encoded by the server. Do not put tokens in source control.

## Email delivery

`MAIL_DRIVER=log` writes mail to the application log instead of sending it. It
defaults to `log` in development and `smtp` everywhere else, and is **rejected
outside development**: invitation and password-reset mails carry bearer links, so
logging them anywhere else is a leak, not a feature.

For production:

```text
MAIL_DRIVER=smtp
SMTP_HOST=smtp.example.com
SMTP_PORT=587
SMTP_FROM=no-reply@vantigo.example.com
SMTP_USERNAME=<from-secret-store>
SMTP_PASSWORD=<from-secret-store>
SMTP_TLS=starttls
```

`SMTP_HOST` and a valid `SMTP_FROM` address are required for the SMTP driver.
`SMTP_TLS` is `starttls` (the default), `implicit`, or `none` — the operator's
choice; see [transport security](transport-security.md) for what `none` costs.
STARTTLS is mandatory rather than opportunistic — a server that offers no TLS
produces an error, not a plaintext delivery. `SMTP_USERNAME` is optional for servers
that need no authentication.

SMTP destinations are resolved and checked before the socket opens: private,
loopback, link-local, carrier-grade-NAT and cloud-metadata addresses are refused,
and the connection is made to the address that was checked rather than a fresh
lookup. See [transport security](transport-security.md) for the rule and its escape
hatch. A destination reached only through DNS64 on the local-use prefix
`64:ff9b:1::/48` is refused outright (there is no single fixed offset to decode
an embedded address from); a resolver on the well-known `64:ff9b::/96` prefix
works as normal.

This is identity's application mail. Communications' per-channel mailbox credentials
are a different thing entirely — configured through the Communications API and
sealed at rest, never environment variables. See
[Communications](communications.md).

## Key material

`APP_SECRET` is the process-wide key material: at least 32 bytes, from which every
key the process uses is derived with HKDF-SHA256 — one derived key per purpose —
and used as AES-256-GCM (CSRF and cookie sealing, OIDC state, TOTP secret
encryption, Communications channel passwords). The purpose string is bound in as
additional authenticated data, so a value sealed for one purpose cannot be opened
under another, and a key-id byte leaves room for a future rotation scheme.

**There is no persisted key ring, no key-wrapping service and no external key
vault.** Nothing needs provisioning beyond the variable itself. Two consequences
matter operationally:

- **All replicas must share the same `APP_SECRET`** (and the same database), or
  sessions issued by one replica are unreadable by another.
- **Losing or rotating it is equivalent to losing a signing key**: every open
  session, every OIDC flow in progress, every stored TOTP secret and every sealed
  channel credential becomes unrecoverable. Rotate only with a migration plan.

## Configuration reference

`config.go`'s field comments remain authoritative; this table is the operator-facing
summary. Booleans are strict `0`/`1` switches — anything else fails startup.

### Application

| Variable | Purpose | Default |
| --- | --- | --- |
| `APP_ENV` | `production` or `development`; only development relaxes anything | `production` |
| `APP_URL` | Public origin (scheme + host [+ port]); no path | **required** |
| `APP_BASE_PATH` | Path prefix on a shared domain, e.g. `/vantigo` | empty (domain root) |
| `PORT` | Listen port | `8080` |
| `DATABASE_URL` | Runtime connection, the least-privilege role | **required** |
| `MIGRATIONS_DATABASE_URL` | Connection `migrate` uses (the owner role) | `DATABASE_URL` |
| `SHUTDOWN_TIMEOUT` | Drain budget for in-flight requests and workers | `30s` |
| `LOG_LEVEL` | `debug`, `info`, `warn`, `error` | `info` |
| `MODULES` | Comma list of business modules to serve | `customers,products,energy,communications,projects,time,expenses` |
| `WORKERS_IN_PROCESS` | Whether `api` also runs background workers in-process | `1` |

### Secrets and identity

| Variable | Purpose | Default |
| --- | --- | --- |
| `APP_SECRET` | Process-wide key material, ≥ 32 bytes | **required** |
| `OWNERS_REQUIRE_MFA` | Require MFA for Owner and SystemAdmin | on outside development |
| `OWNERS_ALLOW_INSECURE_NO_MFA` | Lets the process start with the above set to `0`; does not turn it off by itself | `0` |
| `MFA_ISSUER` | Authenticator app label | `Vantigo` |
| `SESSION_IDLE_TIMEOUT` | Standard idle window | `8h` |
| `SESSION_PRIVILEGED_IDLE_TIMEOUT` | Privileged idle window | `2h` |
| `SESSION_ABSOLUTE_LIFETIME` | Standard absolute cap | `24h` |
| `SESSION_PRIVILEGED_ABSOLUTE_LIFETIME` | Privileged absolute cap | `8h` |
| `INVITATION_LIFETIME` | Invitation validity, `24h`–`720h` | `168h` |
| `INVITATION_ACCEPT_URL` | Template containing `{token}` | derived from `APP_URL` + `APP_BASE_PATH` |
| `PASSWORD_RESET_URL` | Template containing `{email}` and `{token}` | derived from `APP_URL` + `APP_BASE_PATH` |
| `BOOTSTRAP_OWNER_EMAIL` | Closes `/setup`; seats the first Owner by emailed invitation instead | unset (`/setup` open) |

### Mail

| Variable | Purpose | Default |
| --- | --- | --- |
| `MAIL_DRIVER` | `smtp`, or `log` (development only) | `log` in development, else `smtp` |
| `SMTP_HOST` | SMTP server | required for `smtp` |
| `SMTP_PORT` | SMTP port | `587` |
| `SMTP_FROM` | Sender mailbox | required for `smtp` |
| `SMTP_USERNAME` / `SMTP_PASSWORD` | Optional credentials | unset |
| `SMTP_TLS` | `starttls`, `implicit` or `none` | `starttls` |

### Workforce OIDC and SCIM

| Variable | Purpose | Default |
| --- | --- | --- |
| `OIDC_PROVIDER` | `entra` or `google`; unset disables OIDC | unset |
| `OIDC_AUTHORITY` | Exact issuer for the provider | unset |
| `OIDC_CLIENT_ID` | Entra GUID, or a `.apps.googleusercontent.com` client ID | unset |
| `OIDC_CLIENT_SECRET` | Client secret | unset |
| `OIDC_WORKLOAD_IDENTITY_TOKEN_FILE` | Absolute, readable assertion file (Entra only) | unset |
| `AZURE_FEDERATED_TOKEN_FILE` | Platform-supplied fallback for the above | unset |
| `OIDC_ALLOWED_DOMAINS` | Comma list of bare DNS names (Google only, required there) | unset |
| `OIDC_DISPLAY_NAME` | Sign-in button label | `Workforce SSO` |
| `SCIM_TOKEN` | Static SCIM bearer token; unset disables SCIM | unset |
| `SCIM_PREVIOUS_TOKEN` | Prior token during a rotation overlap | unset |
| `SCIM_PREVIOUS_TOKEN_EXPIRES_AT` | RFC 3339, in the future, ≤ 24h ahead | unset |

### Transport, storage and modules

| Variable | Purpose | Default |
| --- | --- | --- |
| `CSP_REPORT_ONLY` | Emit the CSP as report-only | `0` |
| `TRUSTED_PROXY_HOPS` | Number of proxies in front (0–10) | `0` |
| `TRUSTED_PROXY_CIDRS` | The proxies' own addresses, as CIDR prefixes | unset |
| `STORAGE_PROVIDER` | `fs`, or unset for fail-closed storage | unset |
| `STORAGE_FS_ROOT` | Absolute root for the `fs` driver | unset |
| `STORAGE_FS_ALLOW_INSECURE_ROOT` | Relax the root permission check (development only) | `0` |
| `BRREG_BASE_URL` | Brønnøysundregisteret origin | `https://data.brreg.no` |
| `BRREG_TIMEOUT` | Budget for one lookup, retries included | `15s` |
| `PEPPOL_LOOKUP_ENABLED` | On/off switch for the Peppol EHF-capability lookup | `1` |
| `PEPPOL_SML_ZONE` | SML zone a participant identifier is hashed into; must be a plain hostname — no scheme, path or whitespace | `participant.sml.prod.tech.peppol.org` |
| `PEPPOL_DNS_SERVER` | `host:port` of a resolver to use instead of `/etc/resolv.conf`; the port is required and must be numeric | unset |
| `PEPPOL_TIMEOUT` | Budget for one lookup end to end (DNS and SMP together) | `10s` |
| `APP_TITLE`, `APP_LOGO_URL`, `APP_SUPPORT_EMAIL`, `APP_SUPPORT_PHONE`, `APP_SUPPORT_URL` | SPA branding | unset |

The Peppol lookup treats NXDOMAIN and NOERROR-with-no-NAPTR-records as the same
definitive "not registered" answer (`docs/customers.md#peppol-lookup`), since
the SML publishes a name only for a registered participant. That makes the
resolver named by `PEPPOL_DNS_SERVER` matter: one that answers NODATA instead
of forwarding the authoritative NXDOMAIN — or that silently drops an
unfamiliar record type such as NAPTR rather than passing it through — would
turn a genuinely registered participant into a silent false negative, with
nothing in the answer to tell the two apart. Point `PEPPOL_DNS_SERVER` at a
plain recursive resolver, not one that synthesizes or filters answers.

### Management listener

| Variable | Purpose | Default |
| --- | --- | --- |
| `MANAGEMENT_PORT` | Port of the private control-plane listener (must differ from `PORT`) | unset (disabled) |
| `MANAGEMENT_TOKEN` | Bearer token for it, ≥ 32 characters, no whitespace | unset (disabled) |

The two are one switch: set both or neither. See
[the management listener](management.md).

Communications' own settings (`COMMUNICATIONS_*`) are documented in
[Communications](communications.md). OpenTelemetry is configured with the standard
`OTEL_EXPORTER_OTLP_*` variables, read by the OTel SDK rather than by `config.go`.

## Workforce OIDC

At most one workforce OpenID Connect provider is configured, at startup, from the
environment; nothing about it is persisted or editable through an API. `OIDC_PROVIDER`
is the on/off switch — with it unset, leaving any other `OIDC_*` variable set fails
startup naming the leftovers, so a half-removed provider cannot look disabled while
carrying live credentials.

**The client-authentication mode is inferred, not configured.** There is no
`ClientAuthentication` setting: set `OIDC_CLIENT_SECRET` for client-secret
authentication, or `OIDC_WORKLOAD_IDENTITY_TOKEN_FILE` (or let the platform supply
`AZURE_FEDERATED_TOKEN_FILE`) for workload identity. Setting both is a configuration
error, and so is setting neither. Workload identity is Entra-only; Google is
client-secret-only.

The flow is authorization code plus PKCE with a nonce. Provider metadata, issuer,
audience, signature, state, nonce and correlation are validated, the validated
identity is held in a sealed cookie, and provider tokens are never saved. The
browser starts at `/api/v1/identity/oidc/challenge`, the provider returns to the
fixed `/api/v1/identity/oidc/callback`, and local completion is
`/api/v1/identity/oidc/complete`. Every failure is a redirect to
`/sign-in?error=<code>`; no redirect target ever comes from the request.

Provider-specific checks then apply: Entra requires a `tid` matching the tenant GUID
in the configured authority and an `oid` GUID, and requires `azp` to equal the
client ID when the token carries multiple audiences; Google requires
`email_verified`, a valid email, and a non-empty `hd` that matches the email domain
and appears in `OIDC_ALLOWED_DOMAINS`.

New identities are provisioned just in time as local `User` accounts, keyed by the
validated issuer and case-sensitive `sub`. Provider email is informational: an email
collision **fails rather than auto-linking**. OIDC claims never grant local roles and
never satisfy the local MFA requirement.

`GET /api/v1/identity/providers` is anonymous and reports the configured sign-in
providers to the SPA.

### Required callback URI

Register the exact public HTTPS callback with the provider, including the base path
when one is configured:

```text
https://vantigo.example.com/api/v1/identity/oidc/callback
```

The callback path is fixed and is not a deployment setting. The public origin, the
forwarded scheme and the provider registration must agree.

## SCIM

SCIM 2.0 is served at `/api/v1/identity/scim/v2` and authenticated by the static
bearer token in `SCIM_TOKEN`; unset disables it. The token must not contain
whitespace. **There is no file-based token variant** — inject the value from a
secret manager. Rotation with a bounded overlap is described in
[SSO and SCIM operations](sso-scim-operations.md).

## Forwarded headers and HTTPS

`TRUSTED_PROXY_HOPS` **counts the proxies in front of the server** rather than
listing them: with `N > 0`, each of the N trusted proxies appended one entry to
`X-Forwarded-For`, so the client is the Nth entry from the right and everything to
its left is client-written and untrusted. `X-Forwarded-Proto`'s last entry becomes
the scheme. `X-Forwarded-Host` is never honoured — proxies must preserve `Host`.

Outside development, hops without `TRUSTED_PROXY_CIDRS` fails startup:

```text
TRUSTED_PROXY_HOPS: requires TRUSTED_PROXY_CIDRS outside development:
forwarded headers are honoured only from a peer inside that list
```

With the list set, forwarded headers are honoured only when the direct peer falls
inside one of its prefixes; any other peer is treated as the client itself. Without
it, every peer could choose the client address the rate limits key on.

```text
TRUSTED_PROXY_HOPS=1
TRUSTED_PROXY_CIDRS=10.0.0.0/24
```

Terminate TLS at the proxy, forward the original scheme, and expose the SPA, API and
OIDC callback on that HTTPS origin. Outside development cookies are `Secure` and
`APP_URL` must be `https`.

## Database migrations

One PostgreSQL database holds one schema per module — `identity`, `customers`,
`products`, `energy`, `communications`, `projects` and `time` — migrated by plain SQL files embedded in
the binary. Every schema is migrated regardless of which modules `MODULES` enables,
so enabling a module later needs no migration. See the
[contributor migration guide](../CONTRIBUTING.md#database-migrations) for how
migrations are written and applied.

**Both the `migrate` command and the `api` command apply migrations.** `api`
applies pending migrations under a PostgreSQL advisory lock and only serves once
they succeed; `server` mode never migrates. Migrators serialize on that lock, so an
old and a new binary starting at once wait for each other instead of racing.

Running a terminating `migrate` job first and waiting for it is still the right
deployment shape: the point is to see a migration failure **before** any serving
replica starts, not that `api` would otherwise leave the schema behind. Use
`MIGRATIONS_DATABASE_URL` for the owner role where a deployment separates it from
the runtime role.

Do not run `seed` in production; it is development-only and exits 2 outside it.
