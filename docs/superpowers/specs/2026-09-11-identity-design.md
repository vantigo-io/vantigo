# Vantigo Go port — sub-project 3: Identity

Date: 2026-09-11
Status: scope decided with the user in conversation. Identity is **one sub-project** with one spec, one plan and one PR. It is **backend-only**, proven on the Go side; the SPA stays on .NET until sub-projects 6 and 7. Access rules are enforced **from the contract at runtime**. The user delegated every other decision; they are recorded here and listed in the PR.
Parent: `docs/superpowers/specs/2026-09-10-go-backend-port-design.md` (§3.3–3.7, §3.9, §3.11, §7, §10 step 3) and `docs/superpowers/specs/2026-09-10-api-contract-design.md` (the contract and its open items). The parent spec is the binding authority; this document settles what it leaves open and corrects the four statements in §3.7 that the code contradicts (see *Corrections to the parent spec*).

## Goal

The Go server serves all 101 operations of `openapi/identity.yaml` and behaves like the .NET identity module. Tenancy is removed. The .NET code and its 106 non-tenant xunit tests are the behavioural reference. The contract fixes the wire shapes.

Sub-project 3 also builds the platform that sub-projects 4 and 5 will use:
- module composition
- contract-driven access enforcement
- error mapping
- sqlc and transactions
- secrets
- mail
- the peer-CIDR proxy trust

**Definition of done:**
- every identity operation is implemented
- the ported tests pass against real PostgreSQL
- every HTTP exchange in the identity tests validates against `identity.yaml`
- every operation is exercised at least once
- the architecture tests pass: import boundaries and cross-schema SQL
- the image smoke test still passes

## What the .NET module does (summary of the behavioural inventory)

The full inventory (policies, sessions, sign-in, bootstrap, users, invitations, recovery, account settings, RBAC, system, OIDC, SCIM, data model, rate limits, configuration, tests) was taken from the .NET code with file references. Points that matter for the design:

- **Sessions.** There is no server-side session store. Revocation rotates a per-user security stamp, which ends all of that user's other sessions. Logout only clears the cookie.
- **Roles and policies.** The roles are `SystemAdmin`, `Owner` and `User`. The policies are:
  - `ActiveAccount`: signed in and not disabled.
  - `Owner`, `OwnerManagement`: role, active, and MFA if required.
  - `SystemAdmin`: role, active, and MFA if required.
  - `AuthorizationManagement`: active, MFA, and either Owner or an active delegation.
  - `permission:<key>`: active, the `Business` check, not locked out, and either Owner or an effective permission. Effective permissions come from direct roles plus roles mapped to active access groups.
- **Error envelopes.** There are three:
  - `{"error":{"code","message","fields"}}` (`AuthErrorResponse`) for most endpoints
  - a flat `{code, message}` (`CodeMessageError`) under `/access/*` and for the maintenance 400
  - SCIM error bodies

  OIDC failures are redirects to `/sign-in?error=<code>`.
- **Unenforced or surprising behaviour.** Maintenance mode is advisory: only the SPA enforces it. `/access/me` computes permissions from direct roles only. Password-reset tokens are ASP.NET Data Protection tokens. SCIM has 14 endpoints, with no Group PUT.

## Decisions

### Platform (reused by sub-projects 4–5)

- **`internal/contracts`** holds the cross-module types:
  - `Permission{Key, Display, Description, Category, Sensitive, Delegable}`, with `Category` required
  - `Principal`, the resolved caller: user id, roles, MFA state, flags
  - the `Access` interface: `Check(r, rule) (Principal, error)`, which answers `ErrUnauthenticated` or `ErrForbidden` for a refusal, and `Reject(w, r, rule, err)`, which writes that refusal

  Identity implements `Access`. No other module imports identity.
- **`internal/module`** holds the `Module` value from parent §3.6:
  - `Name`
  - `Permissions`
  - `Mount(Deps) (http.Handler, error)`

  `Workers` is not built yet: no module has a worker, and it arrives with the first that does.

  `Deps` carries the pool, config, logger, clock, `mail.Sender`, `secrets.Box`, `contracts.Access`, the rate limiter, the composed permission catalog (`Catalog`) and the module's own contract (`Doc`), the last two set per module by `Compose`.

  `cmd/vantigo` composes the enabled modules into `server.Options.API`. It mounts each module at `/api/v1/<name>/`, serves `GET /api/openapi.json` (the combined contract of the enabled modules, session-only), and falls back to the existing `/api` 404 catch-all. Identity is always enabled. `MODULES` parsing arrives with the first optional module (sub-project 4).
- **Access enforcement from the contract.** Each module passes the generated server a `BaseRouter`. It is built by `module.NewRouter(RouterOptions{Doc, Access, Limiter, Limits, Catalog, MaxBodyBytes, BodyLimits})` and implements the generated `ServeMux` interface.
  - When the generated code registers a pattern, the router looks up that `METHOD path` in the module's embedded contract. It reads the operation's `x-vantigo-access` rule and the module's rate-limit policy for that `operationId`.
  - It registers the pattern on an inner `http.ServeMux`, wrapped in the order: rate limit → `Access.Check` (authentication and authorization in one call; a refusal goes to `Access.Reject`) → the request-body cap → the generated wrapper. This runs before parameter and body decoding, the same order as .NET, where authorization precedes model binding.
  - An unknown pattern, a missing rule, or a permission missing from the composed catalog fails `Mount`, and so fails startup.
  - Rule semantics:
    - `anonymous`: no check
    - `session`: a valid session, else 401
    - `policy:A+B`: every policy
    - `permission:a+b`: every permission
    - `scim`: identity's bearer-token authenticator, which answers with SCIM errors
  - Failures write `AuthErrorResponse` 401 `unauthenticated` or 403 `forbidden`, as the .NET cookie events do for every module. A 401 also clears a session cookie the request presented.
  - Identity has no ServeMux conflicts. Sub-project 4 swaps the inner mux for the literal-before-parameter dispatcher behind the same interface.
- **Request-body cap.** The router caps every operation's request body at 1 MiB (`module.DefaultMaxBodyBytes`). `RouterOptions.MaxBodyBytes` changes a module's default and `BodyLimits` one operation's. The cap is put in place after the rate limit and `Access.Check`, so a refused request's body is never read. A body past it fails the generated decode and answers the operation's documented 400. Identity raises it to 5 MiB + 64 KiB for the two avatar uploads. SCIM reads its own bodies, under its 256 KiB cap, before the router.
- **No runtime request-schema validation middleware.** This deviates from parent §3.5. A middleware 400 cannot produce the per-operation error bodies the frontend parses, such as identity's `invalid_request` codes and field errors. So handlers validate as .NET's handlers do, and the contract is enforced by tests: every test exchange is validated.
- **Generated-server error hooks.** A module-supplied writer handles `RequestErrorHandlerFunc`, `ErrorHandlerFunc` and `ResponseErrorHandlerFunc`:
  - Parameter and body decode failures get the module's documented 400. For identity that is `AuthErrorResponse` with `code: invalid_request` and `message: "The request is invalid."`, the shape its handlers already use.
  - Handler errors go through `httpx.WriteError` (sanitised 500s; 23505 → 409 unless a handler maps it).
  - `err.Error()` is never echoed.

  A missing required header maps the same way.
- **Proxy trust.** `TRUSTED_PROXY_CIDRS` is the peer allowlist promised in parent §3.11. `X-Forwarded-*` is honoured only when the connecting peer is inside it, for up to `TRUSTED_PROXY_HOPS` hops. Every rate-limit partition keys on the resulting client IP. Outside development, `TRUSTED_PROXY_HOPS` > 0 without `TRUSTED_PROXY_CIDRS` is a configuration error; development still accepts it and trusts every peer by hops alone, with a startup warning.
- **sqlc v1.31.1**, pinned in `mise.toml` as `aqua:sqlc-dev/sqlc`; the server-test job installs it.
  - Queries live in `internal/identity/queries/*.sql`, and the code is generated into `internal/identity/store` by a `go:generate` directive.
  - The schema comes from `internal/db/migrations`, and the existing drift check covers the output.
  - `db.WithTx(ctx, pool, opts, fn)` runs `fn` with a `pgx.Tx`. `db.RetrySerializable(ctx, attempts, fn)` retries it on SQLSTATE 40001 (serialization failure) and 40P01 (deadlock), with a short backoff, as .NET's four-attempt loops do.
  - Unique violations are detected by `PgError.Code`.
- **`internal/secrets`**: AES-256-GCM under a key derived from `APP_SECRET` with HKDF-SHA256 per purpose. Ciphertexts carry a key-id prefix for future rotation. It encrypts TOTP secrets now and sealed OIDC state cookies. Mailbox credentials come in sub-project 5.
- **`internal/mail`**: the `Sender` port (`Send(ctx, Message{To, Subject, TextBody})`) with three drivers:
  - `smtp` on `wneessen/go-mail` v0.8.1, with implicit TLS or STARTTLS. Plain SMTP is allowed only with insecure transport. It includes the DNS-rebinding guard, which rejects private, loopback, link-local and metadata addresses when resolving and again just before connecting.
  - `log`, development only. It records the recipient and a random id, never the subject, body or link.
  - `fake`, for tests.
- **Architecture tests**:
  - `depguard` in `.golangci.yml`: `internal/<module>` may import only `contracts`, `module` and platform packages, never another module.
  - A Go test scans migrations and queries for `<other_schema>.`.
  - The `x-vantigo-access` and permission-catalog check runs at `Mount`.
- **`/api/openapi.json`** is served behind a session. The Scalar page is deferred to the cutover (sub-project 7), when the SPA build changes; embedding its bundle now would add megabytes for a page nobody uses before cutover.

### Sessions

These follow parent §3.7 (opaque token, SHA-256 stored), refined as follows:

- **Token and table.** An opaque 256-bit token in the `vantigo.session` cookie: HttpOnly, SameSite=Strict, `Secure` unless development or insecure transport, `Path` = base path or `/`. The `identity.sessions` row holds:
  - user id and token hash
  - `created_at` and `last_seen_at` (the absolute bound is measured from `created_at` at each request, so no expiry is stored)
  - `mfa_verified_at`, whether it is `persistent`
  - IP and user agent
  - `revoked_at`
- **Lifetimes and sliding.** Idle and absolute lifetimes come from `SESSION_IDLE_TIMEOUT` (8h), `SESSION_PRIVILEGED_IDLE_TIMEOUT` (2h), `SESSION_ABSOLUTE_LIFETIME` (24h) and `SESSION_PRIVILEGED_ABSOLUTE_LIFETIME` (8h). "Privileged" means the user currently holds `Owner` or `SystemAdmin`; it is evaluated per request, so a promotion tightens the bounds at once. `last_seen_at` slides only once `now − last_seen ≥ min(idle/4, 5 min)`, as .NET does, to limit writes.
- **Validation.** One query per request joins the session, the user and the user's roles. It rejects a session that is revoked or expired, a disabled user, or, while SCIM is configured, a SCIM-inactive user who is not an Owner. There is no revocation cache: revocation is immediate, where .NET allowed up to 30 s.
- **New sessions and cookies.** Every sign-in creates a new session, so a fixation token never survives. Password login creates a non-persistent cookie. `rememberMe` on `/login/2fa` creates a persistent one (`Max-Age` = the absolute lifetime). TOTP 2FA, passkey login and MFA enable set `mfa_verified_at` on the caller's session; MFA setup and disable clear it. Password-only and OIDC sign-in never set it.
- **Revocation.** Every event that rotated the .NET security stamp revokes the affected user's other sessions, and keeps the caller's own session where .NET reissued the caller's cookie. Every one also deletes the user's password-reset tokens, as a rotated stamp invalidated .NET's. That covers:
  - password change: self, owner-set, and recovery reset (all sessions)
  - MFA enable, disable, setup and owner reset
  - disable and enable
  - owner edits of email, display name or role
  - role assignment
  - delegation revoke (the grantee; beyond .NET, which rotated only the delegation's own stamp)
  - the SystemAdmin grant
  - `sessions/revoke`: all sessions, including the caller's, as in .NET
  - the system-admin revoke of another user
- **Cleanup.** Every sign-in deletes the user's dead sessions in its own transaction: revoked ones, and ones past the standard absolute lifetime. The standard bound is the looser one, so a session only a demotion could make valid again is kept. The `user_id` index is not partial.
- **Divergences, both deliberate.**
  - Logout revokes the session row; .NET only cleared the cookie.
  - A user's own profile or language edit does *not* revoke their other sessions. .NET rotated the stamp there only to refresh cookie claims, and Go reads the principal per request.
- **The 2FA step.** `POST /login` with TOTP enrolled answers `requiresTwoFactor: true` and sets a `vantigo.2fa` cookie (HttpOnly, SameSite=Strict). The cookie holds an opaque ticket whose hash is stored in `identity.login_tickets`. The ticket lasts 5 minutes (the framework default the .NET flow relied on) and is single-use. `/login/2fa` consumes it. The contract's request body has no ticket field, so a cookie it must stay.

### Credentials and sign-in

- **Passwords.** Argon2id (`golang.org/x/crypto/argon2`), PHC string, parameters m=19 MiB, t=2, p=1 (OWASP). The policy is .NET's: at least 12 characters with a digit, an upper-case and a lower-case letter. Development relaxes it to at least 1 character.
- **Lockout.** 5 failures lock the account for 15 minutes (`failed_login_count`, `lockout_end`). The per-email-and-IP throttle allows 10 failures per minute, sits in the DB-backed limiter, is cleared by a success, and answers 429 with no `Retry-After`. The nine named IP policies keep .NET's windows and limits. The rate limit runs before authentication.
- **Login responses.** The order and codes follow .NET exactly:
  1. `invalid_request`
  2. throttle 429
  3. a disabled or SCIM-inactive account returns 429 `account_locked` before the password is checked
  4. a locked account returns 429 `account_locked`
  5. an unknown email or wrong password returns the same 401 `invalid_credentials`
- **The 2FA code.** Exactly 6 digits is a TOTP code; anything else is a recovery code.
  - **TOTP** uses `pquerna/otp` v1.5.0: HMAC-SHA1, 6 digits, 30 s, ±1 step. A code's step is remembered per user and cannot be replayed. .NET did not guard against replay, so this is an addition.
  - **Secrets** are encrypted with `internal/secrets`. The otpauth issuer is `MFA_ISSUER` (default "Vantigo").
  - **Recovery codes.** 10 codes of the form `XXXXX-XXXXX` from an unambiguous base32 alphabet. Only their SHA-256 is stored. Comparison is case-insensitive and ignores the dash. Each code is single-use.
- **Owner MFA.** `OWNERS_REQUIRE_MFA` defaults to on in production and off in development. In production, turning it off also requires `OWNERS_ALLOW_INSECURE_NO_MFA=1`, else configuration fails. That is .NET's `RequireMfa` switch and startup guard with Go-style defaults. Where it is on, it binds Owners, SystemAdmins and delegated administrators, as in .NET.
- **Passkeys** use `go-webauthn/webauthn` v0.18.1:
  - RP ID and origin come from `APP_URL`; user verification and resident keys are required.
  - Ceremonies are rows in `identity.passkey_ceremonies` (5-minute TTL, consumed atomically, a replay gets 409).
  - Caps: 10 passkeys per user, 3 live enrolment ceremonies per user, 20 live login ceremonies per IP.
  - Login begin answers identically for unknown, disabled and locked accounts.
  - Passkey login counts as MFA.
  - Passkeys are unavailable while `APP_URL`'s host is an IP address, which WebAuthn forbids as an RP ID: startup logs a warning and the passkey endpoints answer 500 `passkey_configuration`.
- **Password-reset tokens.** 256-bit random, only the SHA-256 stored in `identity.password_reset_tokens`, valid for 24 h, single-use. Any password change deletes all of a user's tokens, so the replay-after-reset test holds. Recovery requests always answer `{accepted: true}` and send only to existing users with a password and a confirmed email. Send failures are swallowed.
- **Invitations.** As .NET: a 256-bit token with SHA-256 stored, one active invitation per email (partial unique index), default lifetime 7 days (`INVITATION_LIFETIME`, 1–30 days). Resend mints a new row and token. A send failure revokes the invitation. Accepting an Owner invite takes the owner lock.
  - The accept and reset URLs come from `INVITATION_ACCEPT_URL` / `PASSWORD_RESET_URL` templates (with `{token}`, and for reset `{email}`), else `APP_URL` + base path + `/invitations/accept?token={token}` or `/password-reset?email={email}&token={token}` (both values URL-escaped; the SPA serves `/password-reset` as an alias of `/reset-password`), as .NET builds them.
  - Both emails are .NET's English plain-text templates, verbatim.
- **Bootstrap.**
  - `/bootstrap` and `/bootstrap-status`, with the secret from `BOOTSTRAP_SECRET`. It is required outside development. In development a random secret is generated once at start and logged as a warning.
  - The endpoint closes once the bootstrap marker row exists or any Owner does.
  - The work runs in a serializable transaction under the owner advisory lock (key `0x56414e54`).
  - `SYSTEM_ADMIN_EMAIL` reproduces `SystemAdminBootstrapper` at startup:
    - a matching user is granted SystemAdmin, idempotently
    - on a fresh installation it waits for bootstrap
    - a missing user on an installation that is already bootstrapped fails startup
    - an account with an unconfirmed email and no local password (in practice one OIDC provisioned from an unverified email claim) is refused: startup fails and nothing is granted, beyond .NET
    - the startup error and the log name the variable, never the address
    - it runs in the `api` and `server` modes before listening; `worker` neither serves the API nor runs it
  - A bootstrap Owner whose email matches `SYSTEM_ADMIN_EMAIL` also gets SystemAdmin.
  - As in .NET, the bootstrap Owner's email is not confirmed.

### Authorization management, system, OIDC, SCIM

- **RBAC.** Ported as it is:
  - built-in roles `SystemAdmin`, `Owner` and `User`, inserted by the baseline migration with fixed ids
  - role metadata with a version for optimistic concurrency, and role permissions (a full replace on update)
  - access groups with memberships (`source`, `is_upstream_present`, `override`) and role mappings (same-source rule)
  - delegations: scopes are evaluated per delegation, disjoint scopes never combine, and dangling references fail closed
  - the per-role transaction advisory lock (the first 8 bytes of SHA-256 of the role id); role mutations run READ COMMITTED under it, not SERIALIZABLE, and group role mappings take the same lock
  - the audit trail, written inside the caller's transaction, so an audit failure rolls the change back
  - `GET /access/audit` returns the latest 500 rows
  - error codes and both 409 codes as .NET: `authorization_conflict` under roles, users and delegations, `concurrency_conflict` under groups, and `role_mapped` as a 403; `self_change` is 400
  - `/access/me` stays direct-roles-only, as .NET serves it; the live permission check includes group roles

  The catalog is composed from modules' `Permissions` plus `identity:manage` (sensitive, not delegable), and validated at startup. Other modules' keys arrive with their modules. RBAC tests use their own catalog fixtures.
- **Maintenance mode** is a singleton `system_settings` row read through a 20 s in-process cache, and stays advisory: nothing in the backend blocks on it, as in .NET. `/system/status` is anonymous; `PUT /system/maintenance` is SystemAdmin-only with the flat 400 `invalid_message`. `/owner/system-status` reports the same counts and heartbeats as .NET. Operational events (last OIDC sign-in, last SCIM request) are best-effort upserts outside the caller's transaction.
- **Workforce OIDC** uses `coreos/go-oidc` v3.21.0 and `golang.org/x/oauth2` v0.37.0:
  - one provider from `OIDC_PROVIDER` (`entra` | `google`), `OIDC_AUTHORITY`, `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET` or `OIDC_WORKLOAD_IDENTITY_TOKEN_FILE` (falling back to `AZURE_FEDERATED_TOKEN_FILE`), `OIDC_ALLOWED_DOMAINS` and `OIDC_DISPLAY_NAME`, all-or-nothing, validated as `WorkforceOidcOptions` does
  - authorization code with PKCE and nonce, requested with `response_mode=query` so the SameSite=Lax state cookie reaches the callback. `POST /oidc/callback` is still served, as the contract documents, but same-origin only: cross-origin protection and the Lax cookie refuse a provider's cross-site form post.
  - the `id_token`'s `exp` and `nbf` allow 5 minutes of clock skew, as .NET's validator did
  - state, nonce and the PKCE verifier travel in a sealed `vantigo.oidc` cookie (`internal/secrets`, 10-minute lifetime). The callback then sets the sealed external-identity cookie (SameSite=Lax), which `/oidc/complete` consumes, as .NET does.
  - the Entra and Google claim policies are .NET's
  - an OIDC session never counts as MFA, and there is no "remember this browser": a TOTP-enrolled account signing in through OIDC always completes its local second factor
  - the federated key is the (normalized issuer, subject) pair in `identity.oidc_links`; there is never automatic linking by email
  - JIT provisioning gives the `User` role only, with the synthetic `@sso.invalid` email when the provider sends none
  - every failure is a 302 to `/sign-in?error=<code>`
  - workload identity reads the token file on every redemption
- **SCIM 2.0** is hand-written for the 14 operations.
  - **Token and ingress:**
    - the static bearer comes from `SCIM_TOKEN` (and `SCIM_PREVIOUS_TOKEN` with `SCIM_PREVIOUS_TOKEN_EXPIRES_AT`, at most 24 h ahead), compared in constant time
    - 120 requests per minute: authenticated requests share one bucket for the single connection, unauthenticated ones are partitioned by IP (429 `tooMany`)
    - bodies must be `application/scim+json` (else 415) and at most 256 KiB
  - **Protocol:**
    - correlation by `externalId`; re-POSTing an existing `externalId` answers 409, and only two creates racing for one `externalId` end in a replacement
    - `externalId` is at most 512 characters
    - Owners are immune, and `externalId` is immutable
    - soft delete and `active:false` make the user inactive, which ends their sessions at the next request; `active:false` keeps the user's SCIM group memberships, and only DELETE marks them not present
    - writes run READ COMMITTED under one SCIM advisory lock
    - SCIM groups set only `is_upstream_present`, so local overrides always win
    - the filter grammar is .NET's
    - paging: `startIndex` ≥ 1, `count` from 0 to 100
    - PATCH supports `add`, `replace` and `remove` on .NET's paths
    - users carry a random ETag, groups use their version, and `If-Match` / `X-SCIM-Meta-Version` answer 412
  - **Schema:** the single static connection collapses to `source = 'scim'` on groups and mappings, and the `scim_connections` table is dropped.

### Data model

There is one goose baseline, `00002_identity_baseline.sql` (`CREATE SCHEMA identity`), designed fresh rather than copied from the ASP.NET Identity tables, since no data is migrated. Its tables:
- `users`: unique normalized email, password hash, email confirmed, disabled, lockout, failed count, the TOTP secret (encrypted) and its last step, preferred language, and a `version` uuid for `/access/me` and conflicts
- `roles`, carrying the metadata columns
- `user_roles` and `role_permissions`
- `access_groups`, `access_group_memberships` and `access_group_role_mappings`
- `authorization_delegations`, with its permissions and roles
- `authorization_audit_events`
- `sessions` and `login_tickets`
- `recovery_codes` and `password_reset_tokens`
- `invitations`
- `passkeys` and `passkey_ceremonies`
- `profile_avatars`
- `oidc_links` and `scim_user_mappings`
- `system_settings`, `bootstrap_state` and `operational_events`

Dropped: the ASP.NET plumbing (claims, role claims, user tokens, security and concurrency stamps, user names), the tenant tables, and `scim_connections`. The baseline is the whole schema in one file. Sub-project 3 is the only writer before the cutover, so there is no reason to keep migrations per feature.

### Contract leftovers

`AuthSessionResponse` and `AuthSuccessResponse` still carry `tenants` and `activeTenantId`. Go emits an empty list and `null` where the schema allows. Where it does not, Go emits one synthetic tenant: id `00000000-0000-0000-0000-000000000001`, slug `default`, name `APP_TITLE`. Sub-project 6 removes the fields from the contract along with the SPA's tenant routing.

### Configuration added

- `APP_SECRET` (≥ 32 bytes)
- `BOOTSTRAP_SECRET`
- `SYSTEM_ADMIN_EMAIL`
- `SESSION_IDLE_TIMEOUT`, `SESSION_PRIVILEGED_IDLE_TIMEOUT`, `SESSION_ABSOLUTE_LIFETIME`, `SESSION_PRIVILEGED_ABSOLUTE_LIFETIME`
- `OWNERS_REQUIRE_MFA`, `OWNERS_ALLOW_INSECURE_NO_MFA`, `MFA_ISSUER`
- `INVITATION_LIFETIME`, `INVITATION_ACCEPT_URL`, `PASSWORD_RESET_URL`
- `MAIL_DRIVER` (`smtp` | `log`; `log` only in development)
- `SMTP_HOST`, `SMTP_PORT`, `SMTP_USERNAME`, `SMTP_PASSWORD`, `SMTP_FROM`, `SMTP_TLS` (`implicit` | `starttls` | `none`; `none` only in development or with insecure transport)
- `OIDC_*` (as above)
- `SCIM_TOKEN`, `SCIM_PREVIOUS_TOKEN`, `SCIM_PREVIOUS_TOKEN_EXPIRES_AT`
- `TRUSTED_PROXY_CIDRS` (required outside development whenever `TRUSTED_PROXY_HOPS` > 0)

Every one is validated in `internal/config` with the existing collect-all-problems style. A formatted or logged configuration redacts every secret, any database password and `SYSTEM_ADMIN_EMAIL`.

## Testing

- **Porting tests.** The 106 non-tenant .NET tests are ported first for each area (TDD), each with the original class and method name in a comment, so coverage can be audited. The CSRF tests become `CrossOriginProtection` tests: a cross-site unsafe request gets 403, and a same-origin one passes.
- **Setup.** Identity tests run in-process against real PostgreSQL through `internal/testdb`. They use the fake mail sender, a fixed clock where time matters, a TOTP generator, and a fake OIDC provider built on `httptest`.
- **A contract-validating test client.** An `internal/openapi` helper wraps the test HTTP client and validates every request and response against `identity.yaml` with the same rules as the recorded-exchange test. Headers are included here, because tests have them.
- **Coverage.** A package-level check fails if any identity operation was never exercised.
- **The rest.** Architecture tests as above, the existing drift checks (now including sqlc), and the existing image smoke test.

## Corrections to the parent spec (applied to §3.7 in this change)

- SCIM has 14 endpoints (Users CRUD with PUT, Groups CRUD without PUT, and the three discovery endpoints), not 13.
- The federated identity key is the (normalized issuer, subject) pair. .NET used SHA-256(issuer "\0" subject) only for the derived username and synthetic email.
- Password-reset tokens were ASP.NET Data Protection tokens, not stored hashes. Go stores their SHA-256, as it does for invitations.
- Owner MFA follows .NET's `RequireMfa` switch with a production guard (`OWNERS_REQUIRE_MFA` / `OWNERS_ALLOW_INSECURE_NO_MFA`), not an unconditional rule.

## Changes during implementation

These changed while the plan was carried out, or in the final review's fix wave. The sections above are updated to match; each bullet says why.

- **`contracts.Access` is `Check`/`Reject`,** not `Authenticate`/`Authorize`. One call decides a rule from the request, since a SCIM token and a session are both request state, and the module that knows its error shapes writes the refusal.
- **`contracts.Permission` gains a required `Category`.** The permission-catalog response carries it, and `Compose` refuses a permission without one, as .NET did. Sub-projects 4 and 5 must set it.
- **`module.Deps` also carries `Doc` and `Catalog`.** The router needs the module's own contract and the composed catalog; `Compose` sets both per module. `Module.Workers` is not built, since no module has a worker yet.
- **Request-body cap.** Every operation's body is capped at 1 MiB by default, with per-operation overrides (the avatar uploads get 5 MiB + 64 KiB), after the rate limit and the access check. Without it an anonymous caller could make a JSON decoder buffer any body. Four identity operations take a body but document no 400; there an oversized body, like any undecodable one, answers the decode 400 off-contract, as .NET's model binding did.
- **Proxy trust.** `TRUSTED_PROXY_HOPS` > 0 requires `TRUSTED_PROXY_CIDRS` outside development. An empty list trusted `X-Forwarded-For` from every peer, which let any client choose the address the rate limits key on.
- **Session cleanup.** Every sign-in purges the user's revoked and past-absolute sessions, and the `user_id` index is not partial. Rows were otherwise never deleted, and each revocation touched a user's whole history.
- **No stored `absolute_expires_at`.** The absolute bound is measured from `created_at` at each request, because which bound applies, standard or privileged, depends on the roles the user holds then.
- **Stamp rotation.** The list gains MFA enable (ASP.NET's `SetTwoFactorEnabledAsync` rotates the stamp), both `sessions/revoke` endpoints (.NET revoked through `UpdateSecurityStampAsync`), role assignment (`AuthorizationManagementEndpoints.cs:397`) and the delegation-revoke grantee (hardening beyond .NET, which rotated only the delegation's own stamp). Every rotation also deletes the user's password-reset tokens, because .NET's stamp-bound reset tokens died with the stamp.
- **A 401 from the access layer clears a presented session cookie,** as .NET signed the caller out, so a browser stops sending a dead token.
- **The SCIM-inactive session rejection applies only while SCIM is configured,** as .NET's `ScimLifecycleService` checked `options.Enabled`. Otherwise turning SCIM off would lock directory-deactivated accounts out with no way back.
- **MFA setup and disable clear the caller's `mfa_verified_at`,** as .NET reissued the caller's cookie without the MFA claim there.
- **`self_change` is 400,** not the inventory's 403: .NET's handler answers it first (`AuthorizationManagementEndpoints.cs:359`), and .NET's test expects 400.
- **Role mutations run READ COMMITTED under the per-role lock,** not SERIALIZABLE: a SERIALIZABLE waiter checks against its pre-wait snapshot, which let a waited edit give a group-mapped role `identity:manage`. Group role mappings take the same lock, or that guarantee would not hold.
- **Deleting a user who created a delegation answers 409 `account_conflict`,** not .NET's 500. The delegation's `created_by_user_id` is `ON DELETE RESTRICT`, so it keeps naming its creator. The deletion backstops now match both codes PostgreSQL raises for such a refusal (23001 for `RESTRICT`, 23503 for `NO ACTION`); the SCIM-mapping backstop had matched only 23503, which a `RESTRICT` key never raises.
- **SCIM rate limit.** Authenticated requests share one bucket for the single connection, not one "by the token": .NET partitioned by connection, and keying by token let a rotation double the budget.
- **SCIM re-POST of an existing `externalId` answers 409,** .NET's code, instead of replacing the user. A replacement happens only when two creates race.
- **SCIM `active:false` keeps the user's group memberships;** only DELETE marks them not present, as in .NET, so an Entra disable and re-enable does not drop the user's groups until the next group push.
- **SCIM writes run READ COMMITTED under one SCIM advisory lock,** so a check never interleaves with another write, and two writes with one ETag succeed once.
- **The SCIM `external_id` bound is 512,** .NET's bound.
- **OIDC `id_token` `exp` and `nbf` allow 5 minutes of skew,** as .NET's validator did, so a server clock slightly ahead of the IdP does not refuse valid tokens.
- **OIDC sessions are not MFA, and there is no "remember this browser".** .NET's MFA claims came only from local factors, and the remembered-browser cookie was not ported, so a TOTP-enrolled account signing in through OIDC always completes its local second factor.
- **The OIDC POST callback is served same-origin only.** Cross-origin protection and the Lax state cookie refuse a provider's cross-site form post, which is why sign-in requests `response_mode=query`.
- **`OIDC_ALLOWED_DOMAINS` must be bare DNS names,** trailing dots stripped, as `WorkforceOidcOptions.NormalizeDomains` required. Anything else is now a configuration problem instead of a domain that can never match.
- **`RunStartup` refuses to grant SystemAdmin to an unverified, passwordless account.** Such an account is in practice one OIDC provisioned from an unverified email claim, which anyone in the tenant could assert. Beyond .NET.
- **`RunStartup` runs in the `api` and `server` modes,** before listening. It is idempotent, and .NET ran its bootstrapper on every host start.
- **`SYSTEM_ADMIN_EMAIL`'s value is never logged.** The startup error and the notice name the variable, not the address.
- **A formatted or logged configuration redacts its secrets.** `Config`, `MailConfig`, `OIDCConfig` and `SCIMConfig` implement `Format` and `LogValue`, as `secrets.Box` does, so `%v` or a log line never prints the application or bootstrap secret, the SMTP password, the OIDC client secret, the SCIM tokens, a database password or `SYSTEM_ADMIN_EMAIL`.
- **Bootstrap's display-name bound counts UTF-16 code units,** as .NET's `string.Length` and every other bound here do; it had counted runes.
- **Passkeys are unavailable when `APP_URL` has an IP host.** WebAuthn forbids an IP RP ID; startup warns, and the passkey endpoints answer 500 `passkey_configuration`. Left as is.
- **`SMTP_TLS=none` is allowed in development** without `ALLOW_INSECURE_TRANSPORT`, as development relaxes every other transport rule.
- **The owner advisory lock is `0x56414e54` (1447120468),** .NET's value. The plan's decimal `1447121492` was a typo and is corrected.

## Out of scope

- The frontend: de-tenanting, dropping the antiforgery fetch and pointing the SPA at Go belong to sub-projects 6 and 7.
- The Scalar page (sub-project 7).
- Other modules' handlers and permissions (sub-projects 4 and 5).
- `MODULES` parsing (sub-project 4).
- Migrating .NET data: none, by decision.

## Risks

- **Size.** 101 operations and about 11,000 lines of behaviour. The plan splits the work by area, and every task ports its tests first.
- **Session and revocation semantics** differ in mechanism from .NET: rows instead of stamps. The revocation list above is checked against the ported revocation tests.
- **WebAuthn and OIDC** are exercised through fakes: a software authenticator and an `httptest` OIDC provider. No real IdP is tested, the same as in .NET.
