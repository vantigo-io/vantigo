# Identity module — behavioural inventory for the Go port

Input to the identity design spec / implementation plan. Describes what the .NET identity module *does*
(beyond `openapi/identity.yaml`) so the Go port (spec §3.7) reproduces it. Tenancy is dropped.

Path abbreviations: `EA/` = `apps/identity/backend/Identity.Module/Endpoints/Auth/`, `SV/` = `…/Identity.Module/Services/`,
`AZ/` = `…/Identity.Module/Authorization/`, `DB/` = `…/Identity.Module/Database/Accounts/`,
`CFG/` = `packages/configuration/Vantigo.Configuration/`, `CT/` = `packages/contracts/Vantigo.Contracts/`,
`TS/` = `apps/identity/backend/Identity.Module.Tests/`, `HOST/` = `apps/host/backend/Vantigo.Host/`.
"UNCONFIRMED" = not provable from repo source (usually ASP.NET framework internals).

Contract access mix (101 ops, `x-vantigo-access`): 26 `policy:ActiveAccount`, 16 `policy:OwnerManagement`, 16 `anonymous`,
14 `scim`, 14 `policy:Owner+OwnerManagement` (the `/owner` group = Owner policy on the group plus OwnerManagement on each
endpoint, `EA/AuthAccountEndpoints.cs:22-27`), 9 `policy:AuthorizationManagement`, 3 `session` (`/logout`, `/session`,
`/access/me` = bare `RequireAuthorization()`), 2 `policy:SystemAdmin`, 1 `policy:Owner` (`/owner/system-status`).
No op uses `permission:` or `policy:Business`. .NET routes absent from the contract: `GET /antiforgery` (removed, spec §3.11),
`POST /session/tenant`, `/admin/tenants/*`, `/tenants/current/capabilities` (tenancy).

## 1. Policies

Registered in `EA/AuthServiceCollectionExtensions.cs:281-304`; names in `CT/Identity/AuthPolicies.cs:5-9`; roles
`SystemAdmin`, `Owner`, `User` (`CT/Identity/AuthRoles.cs:5-7`).

| Policy | Requirements | Handler logic |
|---|---|---|
| `ActiveAccount` | `ActiveAccountRequirement` (`:285`) | authenticated AND fresh DB read `Users.Any(id == uid && !IsDisabled)` (`EA/AuthAuthorization.cs:26-50`, check `:44-45`). **Checks `IsDisabled` only — not lockout, not SCIM-inactive.** |
| `SystemAdmin` | role `SystemAdmin` + Active + MFA (`:292-293`) | |
| `Owner` | role `Owner` + Active + MFA (`:294-295`) | |
| `OwnerManagement` | identical to `Owner` (`:296-298`) | distinct name, same requirements |
| `Business` | Active + `BusinessAccessRequirement` (`:299-300`) | passes if caller is not `Owner`, or `Owners.RequireMfa` is false, else needs MFA claim (`EA/AuthAuthorization.cs:52-78`). Only Owners are ever forced to MFA here. |
| `AuthorizationManagement` | Active + MFA + `AuthorizationManagementRequirement` (`:301-303`) | caller holds `Owner` directly, OR has an active delegation (`RevokedAt==null && (ExpiresAt==null || >now)`) whose roles are all non-system/non-built-in and whose permission keys are all in the catalog and `Delegable` (`EA/AuthAuthorization.cs:104-174`; three batched queries, `:127-172`). |
| MFA-authenticated | `MfaAuthenticatedRequirement` | passes if `!Owners.RequireMfa` OR principal carries MFA claim (`EA/AuthAuthorization.cs:80-97`). Applies to *every* caller of the policy (incl. delegates, SystemAdmins), not only Owners. |

- MFA claim = `amr=mfa` (or `ClaimTypes.AuthenticationMethod=mfa`, value case-insensitive); `Issue()` adds both
  (`AZ/MfaClaims.cs:13-28`). Stamped on the cookie at: TOTP/recovery 2FA login (`EA/AuthEndpoints.cs:410-413`), MFA enable
  (`EA/AuthAccountEndpoints.cs:190-237`), passkey login (`EA/AccountSettingsEndpoints.cs:599-605`). Never on password-only login;
  stale persisted MFA claims are stripped on plain login (`EA/AuthEndpoints.cs:340-344`).
- **`permission:<key>` policies** (`AZ/PermissionAuthorization.cs:22-39`) are generated on demand. Each also adds Active +
  Business (`:36`); an unknown key throws at policy resolution (`:26-38`). Handler (`:41-100`): key must be in catalog (`:48-49`),
  fresh read of `IsDisabled` + `LockoutEnd` (fails if either, `:56-61`), direct roles (`:63-69`). **`Owner` short-circuits to
  allow** (`:70-74`). Otherwise effective roles = direct `user_roles` ∪ roles mapped to *active* groups where membership is
  effective (`Override==ForceMember` or `Override==null && IsUpstreamPresent`) and `mapping.Source == group.Source`
  (`:80-93`); allow iff `role_permissions` has (effective role, key) (`:97-99`). Tenant filters at `:53-54,66,82,92` are dropped.
  **No caching**: several queries on every check.
- Maintenance mode does not interact with any policy (§8).
- **401 vs 403**: unauthenticated → cookie `OnRedirectToLogin` → 401 `{"error":{"code":"unauthenticated",…}}`
  (`EA/AuthServiceCollectionExtensions.cs:114-119`); authenticated but a requirement fails → `OnRedirectToAccessDenied` → 403
  `forbidden` (`:120-125`). Handlers also return explicit 401 `unauthenticated` when the cookie's user row is gone
  (`EA/AuthEndpoints.cs:434-438`, `EA/SessionEndpoints.cs:44,75`) and explicit 403s (`mfa_required`,
  `delegated_admin_mfa_required` `EA/AuthAccountEndpoints.cs:256-264`; `forbidden` `:335-338`; RBAC 403 codes in §7).
- The catalog's only identity permission is `identity:manage` (Sensitive, `Delegable:false`), registered by the host
  (`HOST/Program.cs:203-206`). No production endpoint requires it; it is used only in tests (`TS/Integration/IdentityRbacIntegrationTests.cs:20`).

## 2. Sessions

- **No session table.** A session is a self-contained ASP.NET cookie. Server-side revocation works by rotating
  `users.security_stamp`; validation compares the cookie's stamp claim with the DB (`SV/SessionValidationService.cs:158-192`).
- App cookie `vantigo.identity.auth`: HttpOnly, SameSite=Strict, Secure=`SameAsRequest` in Development else `Always`,
  Path left at the default, `SlidingExpiration=true` (`EA/AuthServiceCollectionExtensions.cs:103-126`). `ExpireTimeSpan` = standard
  `Sessions.IdleTimeout` for everyone (`:346`). Other cookies: external OIDC `vantigo.identity.external` (SameSite=**Lax**, `:142-147`);
  CSRF `vantigo.identity.csrf` + header `X-XSRF-TOKEN` (`:383-394`).
- Lifetimes (`CFG/VantigoAuthenticationOptions.cs:39-65`): idle 8h / privileged idle 2h; absolute 24h / privileged absolute 8h;
  `RevocationCacheDuration` 30s; `PrincipalRefreshInterval` 15m. Privileged = principal in role `Owner` or `SystemAdmin`
  (`SV/SessionValidationService.cs:201-202`), which picks the tighter pair (`:71-73`).
- Validation on every cookie request (`OnValidatePrincipal` chained after Identity's stamp validator,
  `EA/AuthServiceCollectionExtensions.cs:360-376`; `SV/SessionValidationService.cs:56-121`): reject if
  `now − started ≥ absolute` (`:83-87`) or `now − lastSeen ≥ idle` (`:89-93`). Timestamps live in cookie properties
  `vantigo.session.started` / `vantigo.session.seen` (`:38,41`).
- **Sliding**: `lastSeen` is rewritten, and the cookie reissued, only once `now − lastSeen ≥ min(idle/4, 5 min)`
  (`:50,107-111,204-208`). Absolute start is written once (`:100-104`). `BeginFreshSession` (`:155`) resets the start on login,
  2FA, passkey, OIDC and bootstrap. `CarryForwardSessionStart` keeps the original start when the cookie is reissued
  mid-session, e.g. after a password change (`EA/AuthServiceCollectionExtensions.cs:349-357`).
- **Revocation cache** (`SV/SessionStateCache.cs:15-36`): in-memory per process, key = user id, value = `(SecurityStamp, IsEffectivelyDisabled)`,
  TTL `RevocationCacheDuration` (`SV/SessionValidationService.cs:172`). A cached value may *admit* a request (`:164-166`). A
  negative outcome is always re-read from the DB before rejecting (`:169-177`, invariant `:23-28`).
  `IsEffectivelyDisabled` = `IsDisabled` OR (SCIM mapping `UpstreamActive=false` AND not Owner) (`SV/ScimLifecycleService.cs:21-44`);
  lockout is not included. An EF `SaveChanges` interceptor evicts the cache for every touched `ApplicationUser`
  (`SV/SessionStateCache.cs:46-75`); other replicas converge within 30s.
- **Principal refresh** (a separate mechanism): Identity's `SecurityStampValidator` rebuilds the cookie principal (roles/claims) from the DB every
  `PrincipalRefreshInterval` (`EA/AuthServiceCollectionExtensions.cs:72-80`) and copies MFA claims onto the rebuilt principal (`:81-101`).
  So role changes that do not rotate the stamp can appear in the cookie up to 15 min late.
- **What rotates the stamp** (ends every other session):
  - own sessions/revoke (also signs the caller out): `EA/SessionEndpoints.cs:31-59`
  - system-admin revoke for one user: `:61-97`; shared `RevokeAsync` at `:99-124`
  - self password change: `ChangePasswordAsync` then reissue caller cookie, `EA/AccountSettingsEndpoints.cs:133-168`
  - owner-set password: `EA/AuthAccountEndpoints.cs:739-743`; recovery reset: `:1441-1447`
  - MFA disable: `:288-292`; owner MFA reset of another Owner: `:376-380`; MFA setup (`ResetAuthenticatorKeyAsync`): `:175-177`
  - disable and enable: `:793-797`, `:852-856`; owner edit of email/display name/role: `:663-670`
  - **own profile update** (display name/language): `EA/AccountSettingsEndpoints.cs:121-125`
  - SystemAdminBootstrapper granting SystemAdmin: `SV/SystemAdminBootstrapper.cs:69-82`
  - After a self-change, the caller's own cookie is reissued with its MFA claims kept (`EA/AccountSettingsEndpoints.cs:689-697`).
- **Logout** (`EA/AuthEndpoints.cs:421-425`) only clears the cookie. The stamp is not rotated, so a copy of the cookie stays
  valid until it expires. Response `{success:true}`.
- There is no session-list endpoint (not in the contract either).
- `GET /session` (`EA/AuthEndpoints.cs:427-454`, DTO `EA/AuthModels.cs:53-66`):
  - `user{id, displayName, email, roles}`: `email` is null for synthetic `@sso.invalid` addresses (`:587-588`); roles ordered
    SystemAdmin, Owner, User, then alphabetical (`EA/AuthAccountState.cs:112-126`)
  - `twoFactorEnabled`: TOTP enrolled
  - `mfaEnrollmentRequired` = Owner && `RequireMfa` && !enrolled
  - `mfaAuthenticated`: this cookie carries the MFA claim
  - `isSystemAdmin`
  - `tenants[]`, `activeTenantId`: tenancy fields that are **still in the contract schema `AuthSessionResponse`**

## 3. Sign-in

- **Password login** `POST /login` (`EA/AuthEndpoints.cs:270-359`):
  1. missing email or password → 400 `invalid_request`
  2. `LoginAttemptThrottle.IsBlocked` → 429 `rate_limited` (`:301-304`)
  3. if the email exists and the account is effectively disabled → **429 `account_locked`, before the password is checked** (`:306-310`)
  4. `CheckPasswordSignInAsync(lockoutOnFailure:true)`: locked out → 429 `account_locked`; wrong or unknown → record failure, 401
     `invalid_credentials` (the same answer for unknown email and wrong password, tested in `IdentityPasswordAuthIntegrationTests`)
  5. success clears the throttle (`:325`)
  6. if TOTP is enabled: sign in the Identity `TwoFactorUserIdScheme` cookie holding the user id (`:329-336`) and return
     `200 {user:null, requiresTwoFactor:true, twoFactorEnabled:true, mfaEnrollmentRequired:false}`. No app cookie yet.
  7. otherwise: `BeginFreshSession` and a **non-persistent** app cookie (`:348-350`); returns `AuthSuccessResponse`
     (`EA/AuthModels.cs:68-74`: `user, requiresTwoFactor, twoFactorEnabled, mfaEnrollmentRequired, tenants?, activeTenantId?`)
- **Throttles**:
  - `LoginAttemptThrottle`: in-memory, key `UPPER(email)|ip`, 10 failures in 1 min blocks, success clears, 50,000 entries max
    (`EA/LoginAttemptThrottle.cs:18-49`). Its 429 carries **no `Retry-After`**.
  - Identity lockout per account (persisted `lockout_end`): 5 failures → 15 min (`EA/AuthServiceCollectionExtensions.cs:35-37`).
  - Plus the IP rate limit `Login` (§12).
- **2FA** `POST /login/2fa {code, rememberMe}` (`EA/AuthEndpoints.cs:361-419`):
  - The 2FA "ticket" is the framework `TwoFactorUserIdScheme` cookie. Its TTL is not set in the repo (framework default;
    UNCONFIRMED). Missing or expired → 401 `two_factor_session_expired`.
  - disabled or locked → both schemes signed out, 429 `account_locked` (`:381-386`)
  - **exactly 6 digits → TOTP; anything else → recovery code** (`:388-396`). Recovery codes are single-use (framework).
  - failure → 429 `account_locked` if now locked out, else 401 `invalid_two_factor_code`
  - success → fresh session with MFA claim; **`rememberMe` makes the cookie persistent** (`:412`)
- **Passkey login** (`EA/AccountSettingsEndpoints.cs:484-655`):
  - `POST /passkeys/login/begin {email}`: purges expired ceremonies; more than 20 live login ceremonies per client IP → 429
    `rate_limited` (`:497-505`). Unknown, disabled, locked or passkey-less accounts get a placeholder user so the response is
    identical (`:507-522`). `allowCredentials` is stripped (`:723-743`). Stores a `passkey_ceremonies` row (kind `login`,
    TTL 5 min) and returns `{ceremonyId, options}`.
  - `/complete`: atomically consumes the ceremony (conditional update then delete; a replay gets 409 `passkey_ceremony_invalid`,
    `:612-655`). User missing, disabled or locked, an assertion exception, a user mismatch, or no user verification → 401
    `invalid_credentials` (`:565-591`). Success → updates the sign count, fresh session **with MFA claim** (`:593-609`).
  - WebAuthn: RP ID = host of the configured public origin (never `Host`), UV required, resident key required, 5 min timeout,
    strict origin equality (`EA/AuthServiceCollectionExtensions.cs:41-62`).
- **Providers** `GET /providers` → `{oidc: {displayName} | null}` (`EA/WorkforceOidcEndpoints.cs:27-29`).
- **CSRF (today)**: the host middleware validates every non-GET/HEAD/OPTIONS/TRACE request unless marked
  `SkipAntiforgery` (SCIM, health). Failure → 400 `csrf_validation_failed`
  (`HOST/Antiforgery/VantigoAntiforgeryMiddleware.cs:9-50`). The Go port replaces this with `CrossOriginProtection` → 403 (spec §3.11).
- **Error codes on sign-in paths** (all `{"error":{"code","message","fields?"}}`, `CT/Web/AuthError.cs:3-6`; codes are free strings):

| Code | HTTP | Where |
|---|---|---|
| `invalid_request` | 400 | `EA/AuthEndpoints.cs:294,372`; passkey id `EA/AccountSettingsEndpoints.cs` |
| `rate_limited` | 429 | throttle `EA/AuthEndpoints.cs:303`; passkey cap `EA/AccountSettingsEndpoints.cs:504`; middleware §12 |
| `account_locked` | 429 | `EA/AuthEndpoints.cs:309,317,385,401` |
| `invalid_credentials` | 401 | `EA/AuthEndpoints.cs:322`; `EA/AccountSettingsEndpoints.cs:570,585,590` |
| `two_factor_session_expired` / `invalid_two_factor_code` | 401 | `EA/AuthEndpoints.cs:378` / `:400` |
| `identity_validation_failed` | 400 | `IdentityFailure`, `EA/AuthEndpoints.cs:545` |
| `unauthenticated` / `forbidden` | 401 / 403 | §1 |
| `passkey_ceremony_invalid` | 409 | `EA/AccountSettingsEndpoints.cs:562` |
| `passkey_configuration`, `identity_configuration` | 500 | `EA/AccountSettingsEndpoints.cs:526,540`; `EA/AuthEndpoints.cs:194` |
| `invalid_secret` / `bootstrap_unavailable` | 401 / 409 | `EA/AuthEndpoints.cs:177` / `:188,266` |
| `csrf_validation_failed` | 400 | host middleware (above) |

## 4. Bootstrap and setup

- `GET /bootstrap-status` (anonymous) → `{available}` = secret configured && not consumed (`EA/AuthEndpoints.cs:91-104`). It is a
  UI hint only; the POST is authoritative.
- `POST /bootstrap {secret, email, displayName, password}` (`EA/AuthEndpoints.cs:155-268`):
  1. validate: all fields required, email must contain `@` → 400 `invalid_request` with per-field errors (`:506-530`)
  2. secret compare: UTF-8 bytes, equal length, `FixedTimeEquals` (`:532-538`) → 401 `invalid_secret`
  3. **Serializable** transaction plus `pg_advisory_xact_lock(0x56414e54)`, the shared owner-mutation lock (`EA/AuthAccountState.cs:9,20-25`)
  4. consumed (a `bootstrap_states` id=1 row exists, **or any user holds `Owner`**, `:491-504`) → 409 `bootstrap_unavailable`
  5. ensure built-in roles; create the user (username = email) with the password policy; assign `Owner`; also assign
     `SystemAdmin` if the email case-insensitively equals `Authentication:SystemAdmin:Email` (`:214-225`). **`EmailConfirmed`
     is left false** — since password-recovery only emails confirmed accounts (§5), the bootstrap Owner cannot self-recover a
     lost password by email until an admin confirms it some other way.
  6. insert the `bootstrap_states` row; audit `bootstrap.owner-created` (`:234-247`); commit
  7. only after commit: sign in with a fresh session → **201** at `/api/v1/identity/session`
  - A race loser gets 409, but only for serialization failure, deadlock, or a unique violation on `pk_bootstrap_states`,
    `ux_roles_normalized_name`, `ux_users_normalized_user_name` or `pk_user_roles` (`:548-565`).
- `BootstrapSecretProvider` (`SV/BootstrapSecretProvider.cs:19-48`):
  - a configured secret is used verbatim (no trim) and never logged
  - missing outside Development → startup failure (also `CFG/VantigoAuthenticationOptions.cs:177-189`)
  - missing in Development → random 32 bytes base64url, logged once as a warning, held in memory only
  - The secret stays valid forever. "Consumed" state, not the secret, closes the endpoint.
- `SystemAdminBootstrapper` (startup, `HOST/Program.cs:229`; `SV/SystemAdminBootstrapper.cs:28-107`), driven by config
  `Authentication:SystemAdmin:Email`:
  - ensures built-in roles first
  - email unset → no-op
  - matching user exists → grant `SystemAdmin` if missing and rotate security and concurrency stamps (idempotent)
  - no matching user on a bootstrapped installation (any user, or a bootstrap row) → **startup fails**
  - no matching user on a fresh installation → deferred to `/bootstrap`
  - Serializable transaction, up to 4 attempts with 25 ms × n backoff on unique/serialization/deadlock errors
  - a user-created role named `SystemAdmin` with non-system metadata → fails closed
- Startup order (`HOST/Program.cs:224-239`): TenantBootstrapper (drop) → SystemAdminBootstrapper → eager resolution of
  BootstrapSecretProvider, WorkforceOidcOptions, StaticScimOptions and AppPublicUrls (fail fast) → StaticScimStateInitializer.

## 5. Users, invitations, password recovery

- **Owner user management** (`/owner/users*`, `EA/AuthAccountEndpoints.cs:74-91, 401-961`). Filter: serialization, deadlock or
  unique violation → 409 `account_conflict` (`:28-39`). An Owner can never target itself (`RejectSelfManagement`, `:938-961`).
  - list (`:401-457`): Owner first, then by name; includes disabled, lockout, 2FA, avatar URL, `ssoEnabled` (has a `user_logins` row for the OIDC provider, `:425`)
  - create (`:480-555`, request `EA/AuthModels.cs:32-37` incl. password): Serializable; the owner lock when the role is Owner;
    409 `account_exists`; **revokes active invitations for that email**; audit `user.created-with-role`
  - update (`:557-684`): demoting the **last active Owner** (not disabled, not locked, `EA/AuthAccountState.cs:27-62`) → 409
    `last_active_owner`; an email change revokes invitations for the old and new email; rotates the stamp
  - `password-reset` (`:686-709`): emails a reset link when the user has a password and a confirmed email; always `{true}`
  - `password` (`:711-747`): Owner sets the password directly
  - disable and enable (`:749-871`): last-Owner guard on disable; audits `user.disabled` / `user.enabled`
  - delete (`:873-936`): last-Owner guard; SCIM-mapped user → 409 `provenance_conflict` (`EA/AuthAccountState.cs:64-84`,
    backed by a Restrict FK); audit `user.deleted`
  - Roles assignable here: `User` or `Owner`.
- **Invitations** (`DB/Invitation.cs`, `SV/InvitationTokenService.cs`):
  - token = 32 random bytes as base64url; stored as hex SHA-256 in `token_hash` (unique) (`:22-44`)
  - lifetime = config 1–30 days, else **7 days** (`:12-20`)
  - URL, first match wins: explicit `Invitations:AcceptUrl` template (must contain `{token}`) → `PublicOrigin` + `BasePath` +
    `/invitations/accept?token=` → dev fallback `http://localhost:5173/…` (`:46-78`)
  - create (`EA/AuthAccountEndpoints.cs:1076-1146`): role User or Owner; 409 `account_exists`; Serializable; revokes other active
    invites for the email; emails **after** commit; if the send throws, the invitation is revoked and the error rethrown
  - list (`:1148-1174`): every invitation, newest first, no filter
  - revoke (`:1176-1201`): idempotent; 404 if unknown
  - resend (`:1203-1265`): accepted → 409 `invitation_not_active`; otherwise a **new row with a new token**, old ones revoked
  - validate (anonymous, `:1267-1276`) → `{valid, email?, role?, expiresAt?}`
  - accept (anonymous, `:1278-1368`): `SELECT … FOR UPDATE` on the invitation inside Serializable (a serialization failure →
    400 `invitation_invalid`); rejects revoked, accepted or expired; the owner lock for Owner invites; creates the user
    (display name = request, else invite, else email); marks accepted; audit `invitation.accepted-with-role`; signs in → **201**
- **Password recovery**:
  - `request` (`:1370-1403`): sends only if the user exists, has a password and a confirmed email; send failures are swallowed;
    **always `200 {true}`**
  - `reset {email, token, newPassword}` (`:1405-1449`): token verified with the Identity DataProtection provider
    (`ResetPassword` purpose, **24 h** lifespan, `EA/AuthServiceCollectionExtensions.cs:68-71`). Any failure → the same 400
    `invalid_reset_token` (`:1451-1452`). Success rotates the stamp, which also invalidates the reset token itself.
  - The reset URL template must contain `{email}` (`SV/InvitationTokenService.cs:56-67`).
- **Email** (`CT/Email/ApplicationEmail.cs:3`: `(To, Subject, TextBody)`, plain text):
  - Sender: `Smtp` or `Logging` by `EmailOptions.Provider` (`SV/IdentityServiceCollectionExtensions.cs:11-25`).
  - The Logging sender records only the recipient and a random id (`SV/ApplicationEmail.cs:19-31`).
  - SMTP: implicit TLS on the implicit port, else STARTTLS, else plaintext only if explicitly allowed, else throw (`:58-65`).
  - Only two templates, inline English literals with **no localisation**: invite "You are invited to Vantigo" / "Use this link to create your Vantigo
    account: {url}" (`EA/AuthAccountEndpoints.cs:1454-1467`); reset "Reset your Vantigo password" / "Use this link to reset
    your password: {url}" (`:1042-1064`, `:1389-1392`).

## 6. Account settings

- `/account` group: `ActiveAccount` (`EA/AccountSettingsEndpoints.cs:38-73`).
- `GET /account` → `{id, displayName, email, preferredLanguage, avatarUrl}`; `avatarUrl` is `/api/v1/identity/account/avatar` or null (`:699-701,943`).
- Profile `PUT /account`, `PUT|PATCH /account/profile` (`:93-131`):
  - `displayName` required, at most 200 characters
  - `preferredLanguage`: null, blank or `automatic` → null; otherwise lowercased and must be `en` or `nb` (`:900-917`)
  - rotates the stamp (see §2)
- **Password change** `POST /account/password` (`:133-168`): needs the current password. No local password → 409
  `local_password_unavailable`; wrong password → 400 `reauthentication_required` (`:669-687`). Other sessions die; the caller's cookie is reissued.
- **Avatar** (`:170-289`):
  - `PUT` or `POST` upload: at most 5 MiB of image and 5 MiB + 64 KiB of request (`:27-28`)
  - `image/png` or `image/jpeg` only; the declared type must equal the magic-byte sniffed type; max 4096×4096 (`:35,776-898`) → 400 `invalid_avatar`
  - stored in Postgres `profile_avatars` (bytea, with a `version`)
  - served with `Cache-Control: private, no-store`, `nosniff`, inline (`:267-270`); `DELETE` hard-deletes
  - Owners read others' avatars via `/owner/users/{id}/avatar`
- **Sessions**: only the revoke-all endpoint (§2).
- **MFA (TOTP)**: same handlers at `/owner/mfa/*` and `/account/mfa/*`, both `ActiveAccount` so an unenrolled Owner can reach
  them (`EA/AuthAccountEndpoints.cs:56-70`).
  - status → `{twoFactorEnabled, required-but-missing}` (`:108-125`)
  - `GET setup` never returns the secret (`:127-142`)
  - `POST setup` (password required) resets the key and returns `otpauth://totp/{issuer}:{account}?secret=…&issuer=…&digits=6`;
    issuer = `Owners.MfaIssuer`, default "Vantigo" (`:144-188`)
  - `enable {code, password}`: verifies the code, **10 recovery codes**, cookie reissued with the MFA claim (`:190-237`)
  - `disable`: 403 while `RequireMfa` and the caller is an Owner or active delegate; else password check, stamp rotation, MFA claim removed (`:239-299`)
  - `recovery-codes`: password + current TOTP → 10 new codes (`:301-326`)
  - `POST /owner/mfa/reset/{userId}` (OwnerManagement): caller must carry the MFA claim; target must be another Owner;
    resets the key, turns 2FA off, 10 codes, stamp rotation (`:328-383`)
  - TOTP algorithm is framework-default. The test generator (HMAC-SHA1, 30 s, 6 digits,
    `TS/Integration/IdentityApiFactory.cs:234-250`) authenticates against it, which confirms these parameters indirectly.
    Code format, storage and hashing of recovery codes are framework internals (UNCONFIRMED; they live in `user_tokens`, see §11).
- **Passkeys** (`EA/AccountSettingsEndpoints.cs:291-483`):
  - list → `{credentialId (b64url), name, createdAt, transports, isUserVerified, isBackupEligible, isBackedUp}`
  - begin and complete enrollment need the current password
  - at most **10 per user**, checked at begin and again at complete → 409 `passkey_limit_reached` (`:29,349-352,434-437`)
  - at most **3 live enrollment ceremonies per user** → 429 `rate_limited` "Too many passkey ceremonies are active." (`:32,355-361`)
  - ceremony TTL **5 min** (`:34`); name up to 100 characters; credential id up to 1364 characters / 1023 bytes (`:30-31,757-774`)
  - the credential must be user-verified → else 400 `invalid_passkey`
  - `DELETE /account/passkeys/{credentialId}` requires the password and removes only the caller's credential (404 otherwise)
  - `passkeys_unavailable` 501 if the store lacks support (`:302-305`)
- **Owner MFA enforcement**:
  - `Owners.RequireMfa` (default **false**) drives the request-time checks (§1).
  - `Owners.AllowInsecureNoMfa` (default false) is read only by the startup validator. Outside Development, startup fails unless
    `RequireMfa` or `AllowInsecureNoMfa` is true (`CFG/VantigoAuthenticationOptions.cs:77-95,198-212`).

## 7. Authorization management

- **Catalog**:
  - Built once at composition from `IPermissionCatalogContributor`s; a second `AddPermissionCatalog` throws
    (`AZ/IdentityPermissionCatalog.cs:10-24`, `CT/Authorization/PermissionCatalog.cs:57-97`).
  - Keys are `module:verb`: lowercase module, `_` and `-` allowed in the verb; display and description required; no duplicates;
    the key prefix must equal its module (`CT/Authorization/PermissionDescriptor.cs:17-30`).
  - Startup fails if an endpoint's `RequirePermission` key is not in the catalog
    (`packages/contracts/Vantigo.Contracts.AspNetCore/Authorization/PermissionEndpointConventionExtensions.cs:29-46`).
  - `GET /access/catalog` returns the descriptors (`EA/AuthorizationManagementEndpoints.cs:59`).
  - DB `role_permissions` rows are **not** re-validated against the catalog; an orphaned key simply never matches (cleanup UNCONFIRMED).
- **Roles**:
  - `SystemAdmin`, `Owner` and `User` are seeded at startup with `is_system=is_built_in=true`. Seeding runs Serializable with up
    to 4 retries and repairs case drift in role names (`AZ/AuthorizationCatalogExtensions.cs:47-187`).
  - Protected roles: update or delete → 409 `system_role` (`EA/AuthorizationManagementEndpoints.cs:218-219,301-302`).
  - `GET /access/roles` hides SystemAdmin (`:68-69`).
  - Delete: role still assigned → 409 `role_assigned`; role mapped to a group → **403** `role_mapped` (re-checked after the lock) (`:311-318`).
  - Update and `PUT …/permissions` **replace** the whole permission set; the `concurrencyStamp` must match or 409 `role_conflict`.
    Every key must be in the catalog (`:220-254,331-344,750-756`). Duplicate name → 409 `role_exists`.
  - A role mapped to an access group may not be named Owner and may not carry a non-delegable key → 403 `role_group_protected_permission`
    (`AZ/AuthorizationMutationService.cs:67-74`, `EA/AuthorizationManagementEndpoints.cs:233-240`).
  - `role_metadata.steward_user_id` records the delegate who created a custom role.
- **Access groups** (`/access/groups*`, OwnerManagement, `EA/IdentityControlPlaneEndpoints.cs:14-35`; `SV/AccessGroupManagementService.cs`):
  - `display_name` unique → 409 `group_exists`; stale stamp → 409 `group_conflict`
  - member `POST|PUT` sets `Source=Local, IsUpstreamPresent=false, Override=ForceMember`; `DELETE` removes the row (`:96-156`)
  - role mapping `POST|PUT` / `DELETE` takes the per-role lock; system, built-in, Owner or non-delegable-key roles → `protected_role` (`:158-237`)
  - **SCIM-sourced groups** are protocol-owned: update, delete or member changes → an audit `access_group.*_rejected` row is written,
    then 409 `scim_group_managed` (`:320-340`)
  - `GET /access/groups/{id}` returns **404 for SCIM groups**, while the list includes them (`EA/IdentityControlPlaneEndpoints.cs:41-51`)
  - Every mutation checks the group `concurrencyStamp`, runs in its own transaction and writes an audit row.
- **Delegations** (OwnerManagement: only Owners create, update or revoke; `EA/AuthorizationManagementEndpoints.cs:595-739`):
  - grantee ≠ actor, grantee not an Owner (400 `invalid_delegation_target`)
  - keys must be in the catalog and `Delegable` (400 `invalid_delegation_permissions`)
  - stewarded roles must be custom (400 `invalid_delegation_roles`)
  - `ExpiresAt` in the future; `CanCreateRoles` flag; Serializable
  - active = `revoked_at IS NULL AND (expires_at IS NULL OR > now)`; revoke sets `revoked_at` and rotates the stamp
  - Scopes (`AZ/AuthorizationMutationService.cs:168-267`) are evaluated one delegation at a time; **disjoint scopes never combine**.
    Assignable roles = stewarded roles whose *current* permissions lie inside the scope.
  - A delegation that references missing or now-system roles or keys is dropped silently, i.e. it **fails closed**
    (`TS/Integration/IdentityDelegationMalformedMetadataTests.cs:17-42`).
  - Non-owners must pass a `delegationId` covering the request; Owners may not pass one (403 `delegation_not_allowed`).
  - Edits check current ∪ requested keys against the scope.
  - Assignment (`:105-148`): not to self (403 `self_change`); delegates may not target Owners or other delegates (403
    `assignment_target_denied`); removing a role outside every scope leaves it in place.
  - `GET /access/users/{id}` is allowed for Owners, and for delegates when the target's custom roles are all within one scope; otherwise 404 (`:249-260`).
- **Per-role lock**: `pg_advisory_xact_lock(first 8 bytes big-endian of SHA-256(roleId.ToByteArray()))` (`AZ/AuthorizationMutationService.cs:20-31`).
  - Taken by role edit and delete and by group role-mapping add and remove.
  - Purpose: a role's protected-permission check and a group mapping cannot interleave.
  - Note `ToByteArray()` uses .NET's mixed-endian GUID layout; the Go port's lock key need not match it (the locks are transient).
  - Separate global owner lock `0x56414e54`, used by bootstrap, Owner create/accept and demote (§4).
- **Audit** (`DB/RoleMetadata.cs:44-57`, writer `AZ/AuthorizationAuditWriter.cs:16-127`):
  - Row fields: actor, target user, target role, action (up to 100), details `{"action":…}` (up to 4000), before and after JSON
    (up to 10000 each), correlation id (trace id, else the request id), `mfa_authenticated`, `occurred_at`.
  - Written **inside the caller's transaction**; an audit failure rolls back the mutation (`TS/Integration/IdentityRbacIntegrationTests.cs:577-614`).
  - Owner snapshots record permissions as `["*"]`.
  - Actions: `role.created|updated|deleted`, `user.roles-replaced`, `delegation.created|updated|revoked`, `access_group.created|updated|
    deleted|member_added|member_removed|role_mapped|role_unmapped|*_rejected`, `bootstrap.owner-created`, `user.sessions-revoked`,
    `user.created-with-role|disabled|enabled|deleted`, `invitation.accepted-with-role`, and `scim.*` (§10).
  - `GET /access/audit` (OwnerManagement) returns the latest 500 rows, newest first, with no paging (`EA/AuthorizationManagementEndpoints.cs:552-554`).
- **Effective access**:
  - `GET /access/me` (session): `{roles, roleIds, permissions, version, canManageAuthorization, administrationScope}`.
  - `permissions` is `["*"]` for an Owner, else the keys from **direct roles only** (`:422-459,529-543`).
  - **It omits group-derived roles, which the live permission check does grant** (§1). Whether that is intended is UNCONFIRMED.
  - `version` is the user's concurrency stamp.
- **409 behaviour**:
  - `/access` roles, users and delegations: filter maps concurrency, unique, serialization and deadlock errors → 409
    `authorization_conflict` (`:20-34`). Explicit 409s: `role_exists`, `role_conflict`, `system_role`, `role_assigned`,
    `user_conflict`, `delegation_conflict`.
  - `/access/groups`: separate route group whose filter answers 409 **`concurrency_conflict`** (`EA/IdentityControlPlaneEndpoints.cs:17-22`).
  - Both groups use the **flat** body `{code, message}` (contract `CodeMessageError`) (`EA/AuthorizationManagementEndpoints.cs:759-760`,
    `EA/IdentityControlPlaneEndpoints.cs:137`); 401/403 from policies remain wrapped `AuthErrorResponse`.

## 8. System

- **Maintenance mode** (`EA/SystemMaintenanceEndpoints.cs`):
  - Stored in a singleton `system_settings` row: enabled, message, updated at, updated by.
  - `GET /system/status` (anonymous) → `{maintenance, message}` from a **20 s in-memory cache**; defaults to off when no row exists (`:16-17,33-47`).
  - `PUT /system/maintenance` (SystemAdmin): message up to 500 characters, else **flat** 400 `invalid_message` (`:49-90`); upserts and evicts the cache.
  - **Nothing in the backend enforces it.** The SPA blocks non-SystemAdmins: `shouldShowMaintenance = maintenance && !isSystemAdmin`
    (`apps/host/frontend/src/api/system-status.ts:40-41`, used at `apps/host/frontend/src/routes/__root.tsx:167`).
- **Owner system status** `GET /owner/system-status` (Owner policy, `EA/AuthEndpoints.cs:106-153`):
  - `total`
  - `active`: excludes disabled, locked and SCIM-inactive, except an inactive Owner still counts
  - `disabled`
  - `pendingInvitations`: not accepted, not revoked, not expired
  - OIDC enabled flag and provider
  - `staticScimEnabled`
  - timestamps of the last authenticated SCIM request and the last OIDC sign-in
  - No secrets are included (`TS/Integration/IdentitySystemStatusIntegrationTests.cs:44-46`).
- **System-admin revoke** `POST /system/users/{userId}/sessions/revoke`: all sessions of one user (§2); 404 for an unknown user.
- **Operational events** (`SV/OperationalEventService.cs`):
  - Two kinds: `static-oidc.sign-in-succeeded` and `scim.authenticated-request` (`:8-12`).
  - One row per kind: `INSERT … ON CONFLICT (kind) DO UPDATE SET occurred_at`, in a **separate scope and connection, outside the caller's transaction**.
  - Best effort: failures are swallowed (`:18-37`; callers `EA/WorkforceOidcEndpoints.cs:344-353`, `SV/ScimProtocolService.cs:574-581`).
  - It is a last-seen heartbeat, not an audit log.

## 9. OIDC

- **Handler setup**: one ASP.NET OpenIdConnect scheme (`EA/AuthServiceCollectionExtensions.cs:155-279`):
  - code flow with PKCE; HTTPS metadata except in Development
  - `SaveTokens=false`, no UserInfo call, `MapInboundClaims=false`, PAR disabled
  - scopes exactly `openid profile email`
  - `ResponseMode` is not set in the repo, so the framework default applies (UNCONFIRMED; the contract accepts GET and POST `/oidc/callback`)
- **Challenge** `GET /oidc/challenge`: 404 when disabled. `RedirectUri` is always `/api/v1/identity/oidc/complete`; the browser
  cannot supply a return URL (`EA/WorkforceOidcEndpoints.cs:31-43`). The authorize request carries state, nonce, S256 challenge
  and the fixed callback path (`TS/Integration/IdentityOidcIntegrationTests.cs:57-70`).
- **Callback** `/oidc/callback`: handled by the framework (state, nonce, PKCE, code redemption, id_token). Hooks:
  - `OnRemoteFailure` → `/sign-in?error=oidc_remote_failure`
  - `OnAuthenticationFailed` → `…oidc_authentication_failed` (`:194-205`)
  - `OnTokenValidated` re-normalises and compares the token issuer with `Authority` (`WorkforceOidcOptions.TryNormalizeIssuer`:
    lowercases scheme and host, keeps path case, trims a trailing `/`, rejects userinfo, query and fragment, at most 2048 chars;
    `CFG/WorkforceOidcOptions.cs:160-170`)
  - it then runs the claim policy and stamps the private claim `vantigo:oidc:validated-issuer` (`:233-275`)
  - Workload identity: `OnAuthorizationCodeReceived` → `client_assertion` (`:206-232`).
- **Claim policy** (`EA/StaticOidcClaimValidation.cs`):
  - Entra: `tid` equals the configured tenant GUID; `oid` is a GUID; with more than one `aud`, `azp` must equal the client id (`:29-49`).
  - Google: `email_verified=="true"`; the email is strictly valid; `hd` is non-empty, equals the email domain and is in `AllowedDomains`
    (ordinal) (`:51-87`). This is the **only domain restriction**; Entra forbids `AllowedDomains`.
- **Complete** `GET /oidc/complete` (`EA/WorkforceOidcEndpoints.cs:45-175`):
  1. read the external cookie (`oidc_external_identity_missing`)
  2. re-run the claim policy (`oidc_identity_invalid`)
  3. re-check the stamped issuer against `Authority` and validate `sub`: non-empty, no whitespace, at most 512 chars (`:253-278`)
  4. `BeginFreshSession`
  5. existing link `FindByLoginAsync(normalizedIssuer, sub)`:
     - unavailable account → `account_locked`
     - otherwise `ExternalLoginSignInAsync(bypassTwoFactor:false)`: local TOTP is still required → `local_mfa_required`;
       other failures → `oidc_local_sign_in_failed`, `account_locked` (`:85-111`)
  6. no link but the email belongs to an existing user → `oidc_email_conflict`, or `account_locked` if that user is unavailable.
     There is **never automatic linking by email** (`:114-125`).
  7. JIT provisioning, Serializable (`:177-251`):
     - username `oidc-` + hex(SHA-256(issuer + "\0" + sub))
     - email = the claim, else the opaque `oidc-{same hex}@sso.invalid` (`CFG/WorkforceOidcOptions.cs:172-181`)
     - `EmailConfirmed` = the verified flag; role `User` only
     - link row `user_logins(login_provider=normalized issuer, provider_key=sub)`; no password
  8. a lost provisioning race re-reads the link; otherwise `oidc_sign_in_unavailable` (`:135-174,365-384`)
  9. success → operational event and 302 to `/`
- **Federated key** = (normalized issuer, case-sensitive `sub`) in `user_logins`. SHA-256 is used only for the username and the
  opaque email, with a **NUL separator**. Spec §3.7's "SHA-256(issuer+subject)" describes those derived values, not the link key.
- **Failures**: every error is a 302 to `/sign-in?error=<code>` after clearing the external cookie (`:359-363`); there are no JSON errors.
- **External cookie** `vantigo.identity.external`: HttpOnly, **SameSite=Lax**, carries the validated principal from callback to
  complete, and is cleared on both success and failure. Its lifetime is not set in the repo (UNCONFIRMED).
- **Workload identity** (`EA/WorkforceOidcClientAssertion.cs:9-37`): Entra only. `client_secret` is nulled and
  `client_assertion_type = urn:ietf:params:oauth:client-assertion-type:jwt-bearer`. `client_assertion` is the token file's content,
  **re-read on every redemption** and never cached; empty, whitespace or unreadable → throw. The file path comes from config, else
  `AZURE_FEDERATED_TOKEN_FILE`; it must be absolute and readable at startup (`CFG/WorkforceOidcOptions.cs:107-127`).

## 10. SCIM

- **Token** (`SV/ScimTokenService.cs:16-36`): static values from config or a file; nothing stored in the DB.
  - Empty or whitespace-containing → reject. Constant-time comparison against the current token, or against the previous token
    only while `PreviousBearerTokenExpiresAtUtc > now`.
  - Either token maps to the fixed connection `ScimConnection.StaticId` = `7f6b7f8a-7c7e-4c19-8f06-6c64d3b7f4d2` (`DB/ScimConnection.cs:5`).
  - Startup rules: current token required; token and file are mutually exclusive; previous token needs an expiry that is in the
    future and **at most 24 h** away, and must differ from current (`CFG/ScimOptions.cs:15-60`).
- **`StaticScimStateInitializer`** (`SV/StaticScimStateInitializer.cs:26-75`): creates the single `scim_connections` row if missing;
  4 attempts with backoff on races.
- **Ingress** (`EA/ScimProtocolEndpoints.cs:13-109`): antiforgery skipped.
  - A filter authenticates and rate-limits before the body is read: 120 requests/min fixed window, partition = connection id when
    authenticated, else `ip:<addr>` (`SV/ScimProtocolService.cs:558-584`). Over the limit → 429 with SCIM error `scimType:"tooMany"`
    (`:569`).
  - Each handler authenticates again (`:539-551`).
  - Requests require `Content-Type: application/scim+json` (415) and at most 256 KiB (`:36-66`).
  - 401 is a SCIM error `{schemas, status:"401", scimType:"invalidValue", detail}`.
- **Endpoints: 14, not the 13 in spec §3.7**; the contract also lists 14 `scim` ops.
  - Discovery: `GET ServiceProviderConfig`, `Schemas`, `ResourceTypes`.
  - Users: `POST`, `GET {id}`, `GET` list, `PUT {id}`, `PATCH {id}`, `DELETE {id}`.
  - Groups: `POST`, `GET {id}`, `GET` list, `PATCH {id}`, `DELETE {id}`; **there is no PUT for Groups** (`:20-33`).
  - ServiceProviderConfig: filter supported with `maxResults` 100, `changePassword` unsupported (`SV/ScimProtocolService.cs:62-63`).
- **Users → `ApplicationUser` + `scim_user_mappings`** (`:111-360`):
  - The correlation key is `(connection, externalId)`.
  - A new user gets `EmailConfirmed`, is not disabled, has **no role or group**, and gets no password.
  - Re-POSTing the same externalId replaces the existing user.
  - An Owner can never be changed via SCIM → 400 `mutability` (`SV/ScimLifecycleService.cs:46-54`).
  - Changing `externalId` → 409 `mutability`.
  - Serializable transaction; audit `scim.user.*` written in the same transaction.
- **Groups → `access_groups` (Source=Scim) + memberships**:
  - Duplicate display name or externalId → error.
  - At most 100 members per mutation.
  - `type` must be `User`.
  - SCIM only toggles `IsUpstreamPresent`; **a local Force override is never overwritten** (`:362-537,647-673`).
- **Filter** (`:1059-1079`): regex `attr eq "json-string"` joined by `and`; attributes: Users `userName`/`externalId`, Groups
  `displayName`/`externalId`; at most 512 chars; anything else → 400 `invalidFilter`. The regex only captures the **last** `and`
  clause (`:1064`), so a third clause is ignored in effect (UNCONFIRMED how extra clauses are treated).
- **Paging**: `startIndex` ≥ 1 (default 1), `count` 0–100 (default 100); a non-integer → 400 `invalidValue` (`:1049-1055`).
- **PATCH**: ops `add`, `replace`, `remove` only.
  - User paths: `emails[…].value` or `emails.value` → email; `userName`, `externalId`, `active`, `displayName`, `name.givenName`,
    `name.familyName`, `name`; Entra-style pathless objects are expanded. `userName` and `externalId` cannot be removed (`:775-920`).
  - Group paths: `members`, `members[value eq "…"]`, `displayName`, `active`.
- **ETags**:
  - A user's ETag is a random GUID (N format) in the mapping, regenerated on each change (`version++`); sent as `ETag: "…"` and in
    `meta.version` (`:960-971,1040-1047,1192`).
  - A group's ETag is `access_groups.concurrency_stamp`.
  - `If-Match` (or `*`) is checked on User PUT, PATCH and DELETE and on Group DELETE → 412 `invalidVers` (`:1171-1177`).
  - Group PATCH also checks body `meta.version`; Group DELETE also checks header `X-SCIM-Meta-Version` (`:1178-1188`).
  - EF concurrency errors → 412.
- **Deactivation**:
  - `DELETE /Users/{id}` is a soft delete: `upstream_active=false`, and the user's SCIM memberships get `IsUpstreamPresent=false`;
    a later GET still returns 200 with `active:false` (`:327-360`).
  - `active:false` behaves the same way. The stamp is not rotated; sessions end through `IsEffectivelyDisabled` within
    ≤30 s (§2). Owners are exempt (break-glass).
  - Group DELETE sets `is_active=false` and marks its SCIM memberships not present.
- **Division of work**: `ScimProtocolService` handles the wire protocol and CRUD and writes SCIM state; `ScimLifecycleService` is
  the read side (Owner protection, and the effective-disabled session state used on every request).

## 11. Data model

Schema `identity` (`DB/AccountsDbContext.cs:56`); final shape from `DB/Migrations/AccountsDbContextModelSnapshot.cs`. Earlier
`federated_identities`, `federation_oidc_states`, `federation_connections` and `scim_bearer_tokens` were dropped
(`DB/Migrations/20260813130639_StaticIdentitySchemaCleanup.cs:20,42-44`).

| Table | Key / notable columns | Uniques, indexes, FKs, concurrency | Port |
|---|---|---|---|
| `users` | uuid id; display_name(200), is_disabled, preferred_language(10); Identity columns: user_name, email, email_confirmed, password_hash, security_stamp, concurrency_stamp, two_factor_enabled, lockout_end, lockout_enabled, access_failed_count, phone_* | unique `normalized_user_name`; **non-unique** `normalized_email` index (uniqueness enforced in code: `RequireUniqueEmail`) | keep; drop `active_tenant_id` (tenant), stamps/phone (plumbing) |
| `roles` | uuid id, name, normalized_name, concurrency_stamp | unique normalized_name | keep (fold into role table) |
| `user_roles` | PK (user_id, role_id), tenant_id | unique (user, role, tenant); cascade | keep, drop tenant_id |
| `role_metadata` | PK role_id (1:1), display_name(200), description(2000), is_system, is_built_in, steward_user_id | **concurrency token** concurrency_stamp | keep |
| `role_permissions` | PK (role_id, permission_key(200)) | index on key; cascade | keep |
| `authorization_delegations` | uuid; grantee (cascade), created_by (restrict), expires_at, revoked_at, can_create_roles | **concurrency** stamp; index on grantee | keep |
| `authorization_delegation_permissions` / `_roles` | PK (delegation, key) / (delegation, role) | cascade | keep |
| `authorization_audit_events` | bigint id, actor/target ids (no FK), action(100), details(4000), before/after_json(10000), correlation_id(200), mfa_authenticated, occurred_at | index on occurred_at | keep |
| `access_groups` | uuid; scim_connection_id (restrict), display_name(200), source(20), external_id(256), is_active | **unique display_name**; unique (scim_connection, external_id) where not null; **concurrency** stamp | keep |
| `access_group_memberships` | PK (group, user); source, is_upstream_present, membership_override(32), tenant_id | cascade; index on user | keep, drop tenant_id |
| `access_group_role_mappings` | PK (group, role); source, tenant_id | cascade | keep, drop tenant_id |
| `invitations` | uuid; email, normalized_email, role(32), display_name, token_hash(64), created/expires/revoked/accepted_at, invited_by, tenant_id | unique token_hash; **partial unique active invitation per normalized_email** (accepted_at and revoked_at null); no FKs | keep, drop tenant_id |
| `bootstrap_states` | int id (singleton 1), completed_at | PK | keep |
| `system_settings` | int id=1, maintenance_enabled, maintenance_message(500), updated_at, updated_by | PK | keep |
| `operational_events` | bigint identity, kind(64) unique, occurred_at | unique kind | keep |
| `passkey_ceremonies` | uuid; user_id (nullable, cascade), kind(32), state(20000), credential_name, client_address(64), expires_at, consumed | index (expires_at, consumed) | keep (spec's short-TTL table) |
| `profile_avatars` | PK user_id; data bytea, content_type(32), updated_at, version | cascade | keep |
| `scim_connections` | uuid (static id), created/updated_at | | keep (or collapse to a constant) |
| `scim_user_mappings` | uuid; connection & user (**restrict** → blocks user delete), resource_id, external_id, user_name, upstream_active, source_profile_json, version, **etag (concurrency token)** | unique (conn, resource_id), (conn, external_id), (conn, user_id) | keep |
| `user_logins` | PK (login_provider, provider_key), user_id | cascade | **needed**: the OIDC link (§9); not unused plumbing |
| `user_passkeys` | PK (user_id, credential_id bytea), data = JSON `IdentityPasskeyData` | unique credential_id | **needed** content; shape is ASP.NET's |
| `user_tokens` | PK (user, provider, name) | | **needed today**: the framework keeps the TOTP authenticator key and recovery codes here (UNCONFIRMED; no repo code touches it directly) |
| `user_claims` | Identity claims | | plumbing: only read, to purge legacy `amr=mfa` claims (`EA/AuthEndpoints.cs:571`) → drop |
| `role_claims` | Identity role claims | | plumbing, no usage → drop |
| `tenants`, `tenant_memberships` | slug, status, enabled_modules[] | | **tenant-only → drop** |

Also dropped: `security_stamp` becomes the revocation of `identity.sessions` rows (spec §3.7), and `concurrency_stamp` on
users and roles. Keep a user-version token, though: `/access/me` `version` and `user_conflict` depend on the user
concurrency stamp.

## 12. Rate limits

- Definitions: `EA/AuthRateLimitingServiceCollectionExtensions.cs`; names in `EA/AuthModels.cs:5-16`.
- All nine are fixed windows with `QueueLimit=0`, partitioned **per client IP** only: `RemoteIpAddress`, or `"unknown"` (`:74-75`).

| Policy | Window | Permits | Applied to |
|---|---|---|---|
| `Login` | 1 min | 100 | `POST /login` |
| `Bootstrap` | 1 min | 20 | `POST /bootstrap` |
| `Invitations` | 1 min | 30 | `/owner/invitations*` (list, create, revoke, resend) |
| `InvitationAcceptance` | 1 min | 20 | `GET /invitations/validate`, `POST /invitations/accept` |
| `PasswordRecovery` | **15 min** | 10 | `/password-recovery/request|reset` **and `POST /account/password`** |
| `Mfa` | 5 min | 20 | `/login/2fa`, `/owner|account/mfa/*`, `/owner/mfa/reset`, account passkey begin, complete and delete |
| `PasskeyLogin` | 5 min | 30 | `/passkeys/login/begin|complete` |
| `UserManagement` | 1 min | 30 | `/owner/users*` except the avatar |
| `OwnerAvatarRead` | 1 min | 300 | `GET /owner/users/{id}/avatar` |

- **Rejection**: 429, `Retry-After` = lease retry-after rounded up to whole seconds, body `{"error":{"code":"rate_limited","message":
  "Too many authentication attempts. Please try again later."}}` (`:15-27`).
- **Other limiters**: `LoginAttemptThrottle` (10 per minute per email|IP, no `Retry-After`); passkey ceremony caps (3 per user,
  20 per IP → 429 `rate_limited`); SCIM ingress 120/min (§10).
- In the pipeline the rate limiter runs **before authentication** (`HOST/Program.cs:120-136`).
- The client IP comes from `UseForwardedHeaders`: XFF and XFP, `ForwardLimit` 1, known proxies and networks from config, which
  otherwise defaults to trusting loopback (`CFG/ForwardedHeadersOptions.cs:17-80`).

## 13. Configuration

`VantigoAuthenticationOptions` (section `Authentication`, `CFG/VantigoAuthenticationOptions.cs`). Go env names follow spec §3.3.
Items marked **(no Go var yet)** are missing from §3.3.

| Setting | Default | Notes / Go |
|---|---|---|
| `SystemAdmin:Email` | null | break-glass SystemAdmin grant (§4) **(no Go var yet)** |
| `Owners:RequireMfa` | **false** | request-time MFA switch **(no Go var yet)**; spec says "Owners must have MFA unless `OWNERS_ALLOW_INSECURE_NO_MFA`" |
| `Owners:MfaIssuer` | "Vantigo" | otpauth issuer **(no Go var yet)** |
| `Owners:AllowInsecureNoMfa` | false | startup-only escape hatch → `OWNERS_ALLOW_INSECURE_NO_MFA` |
| `Bootstrap:Secret` | null (required outside Dev) | `BOOTSTRAP_SECRET` |
| `Invitations:Lifetime` | null → 7 d (valid 1–30 d) | **(no Go var yet)** |
| `Invitations:AcceptUrl`, `PasswordReset:ResetUrl` | null | templates with `{token}` / `{email}`; else derived from `APP_URL` + base path **(no Go var yet)** |
| `Oidc:Enabled, Provider (Entra\|Google), Authority, ClientId, ClientAuthentication (ClientSecret\|WorkloadIdentity), ClientSecret, WorkloadIdentityTokenFile, AllowedDomains[], DisplayName ("Workforce SSO"), CallbackPath (fixed)` | off | `OIDC_*`. Validation: Entra authority exactly `https://login.microsoftonline.com/<guid>/v2.0` with a GUID client id; Google authority exactly `https://accounts.google.com`, client id ending `.apps.googleusercontent.com`, ≥1 allowed domain (`CFG/WorkforceOidcOptions.cs:42-149`). Provider and AllowedDomains are **(no Go var yet)** |
| `Scim:Enabled, BearerToken[File], PreviousBearerToken[File], PreviousBearerTokenExpiresAtUtc` | off | `SCIM_TOKEN`, `SCIM_PREVIOUS_TOKEN`, `SCIM_PREVIOUS_TOKEN_EXPIRES_AT`; overlap ≤24 h |
| `Sessions:IdleTimeout / PrivilegedIdleTimeout / AbsoluteLifetime / PrivilegedAbsoluteLifetime` | 8h / 2h / 24h / 8h | `SESSION_*`; must be > 0 |
| `Sessions:RevocationCacheDuration / PrincipalRefreshInterval` | 30s / 15m | ≥ 0; largely moot with per-request DB validation |
| `App:PublicOrigin` (+ `App:BasePath`) | null | `APP_URL` / `APP_BASE_PATH`; must be https outside Dev unless insecure transport is allowed (`CFG/AppPublicOriginOptions.cs:12-103`) |
| `ForwardedHeaders:KnownProxies/KnownNetworks/ForwardLimit` | []/[]/1 | → `TRUSTED_PROXY_HOPS` |
| `Email:Provider` + SMTP options | Logging (Dev only) | `SMTP_*` (spec §3.9) |
| `Tenancy:Mode`, `AllowUnsafeMultiTenant` | single / false | **drop** (`CFG/TenancyOptions.cs:12-48`) |

Hard-coded, not configurable (`EA/AuthServiceCollectionExtensions.cs:26-71`):
- Password: length 12 with digit, upper and lower case required, no symbol required (Development: length 1, nothing required).
- Lockout: 5 failures / 15 min. Unique email. DataProtection token lifetime 24 h.
- Avatar: 5 MiB, PNG or JPEG, 4096 px. Passkeys: 10 per user, 3 enrollment and 20 login ceremonies, 5 min TTL.
- Maintenance status cache 20 s. SCIM: 120/min, 256 KiB, page size 100, 100 members per mutation.
- Recovery codes: 10. Login throttle: 10/min.

## 14. Tests

121 methods = 120 `[Fact]` + 1 `[Theory]` (2 cases). **15 are tenancy-only** (14 in whole files, plus 1 in a mixed file);
106 Facts + 1 Theory carry over. Infrastructure: `TS/Integration/IdentityApiFactory.cs`, a WebApplicationFactory on Testcontainers
Postgres with a least-privilege role, owner bootstrap, a synthetic IP per client, a TOTP generator, a fake OIDC backchannel,
a capturing email sender, and factory variants (StaticScim, Oidc, Mfa, MultiTenant, Production, ShortSession).

| Class (file under `TS/`) | F | T | Covers | Drop? |
|---|---|---|---|---|
| `Authorization/AuthorizationCatalogTests` | 5 | 0 | contributor order, duplicate/malformed keys, endpoint keys must exist in the catalog at startup | |
| `Endpoints/AuthAccountStateTests` | 3 | 0 | disabled vs lockout independence; role ordering and managed-role selection | |
| `Endpoints/Auth/StaticOidcSecurityTests` | 3 | 0 | Entra tid/oid/azp; Google verified email + hosted domain; workload assertion re-read, no secret sent | |
| `Integration/AccessGroupQueryCountTests` | 1 | 0 | group list and details have no N+1 queries | |
| `Integration/IdentityAccountEndpointsTests` | 10 | 0 | owner user CRUD 401/403, CSRF, self-mutation block, role validation and order, disable keeps lockout and kills sessions, concurrent owner-demotion race, profile and language, password change, avatar | |
| `Integration/IdentityControlPlaneIntegrationTests` | 2 | 0 | local access-group admin is owner-only (keep); `/admin/tenants` 409 `multi_tenant_required` (**drop this one**) | partial |
| `Integration/IdentityDelegationMalformedMetadataTests` | 1 | 0 | missing role metadata → delegation fails closed (403) | |
| `Integration/IdentityMaintenanceModeIntegrationTests` | 4 | 0 | status default and anonymous read, non-admin PUT denied, enable/disable round trip, message length | |
| `Integration/IdentityMfaIntegrationTests` | 12 | 0 | MFA required on Owner/OwnerMgmt/AuthzMgmt/SystemAdmin; enrollment reachable pre-MFA; cross-user reset denied; password on MFA alias; passkey begin: lockout and enumeration; ceremony cleanup and replay; TOTP claim is cookie-only; passkey removal bound to credential id | |
| `Integration/IdentityOidcIntegrationTests` (2 classes) | 4 | 0 | challenge 404 when disabled; full code+PKCE with JIT; failed redemption creates nothing; disabled linked account → `account_locked` | |
| `Integration/IdentityPasswordAuthIntegrationTests` | 5 | 0 | login/logout lifecycle; unknown email ≡ wrong password; recovery request indistinguishable; CSRF on unsafe endpoints; recovery round trip (sessions invalidated, token replay rejected) | |
| `Integration/IdentityRbacIntegrationTests` | 18 | 0 | built-in roles; owner implicit allow; immediate revocation; versioned mutations and conflict codes; `/access/me` projection; delegation scoping (disjoint scopes don't combine), stewardship, self and owner boundaries; audit rollback | |
| `Integration/IdentitySessionLifetimeIntegrationTests` | 4 | 0 | privileged idle shorter; standard survives; activity renews; absolute cap | |
| `Integration/IdentitySessionRevocationIntegrationTests` | 7 | 0 | self revoke-all; auth required; admin revokes another user; re-login works; non-admin denied; unknown user 404; stamp rotation spares the caller | |
| `Integration/IdentitySystemAdminBootstrapperTests` | 5 | 0 | email normalisation; missing account fails post-bootstrap and is allowed pre-bootstrap; idempotent; conflicting role fails closed | |
| `Integration/IdentitySystemStatusIntegrationTests` (2 classes) | 3 | 0 | owner-only, redacted, coherent counts; SCIM-inactive, locked and disabled excluded; last SCIM request reported | |
| `Integration/IdentityTenantBootstrapProductionIntegrationTests` | 1 | 0 | default tenant has all modules | **drop** |
| `Integration/IdentityTenantCapabilitiesIntegrationTests` (2 classes) | 3 | 0 | tenant capability flags / `no_active_tenant` | **drop** |
| `Integration/IdentityTenantControlPlaneIntegrationTests` | 6 | 0 | tenant CRUD authz and lifecycle; removed tenant routes | **drop** |
| `Integration/LoginThrottlingIntegrationTests` | 2 | 0 | per-account 429 without affecting others; success clears | |
| `Integration/StaticScimProtocolIntegrationTests` | 1 | 0 | end-to-end: 401, create, filter, stale If-Match 412, PATCH new ETag, group, deactivate-on-delete, `scim.*` audit | |
| `Integration/StaticScimSafetyIntegrationTests` | 2 | 0 | deterministic connection id; legacy `/access/scim` route removed | |
| `Integration/TenantBootstrapperTests` | 4 | 0 | seeding the default tenant's modules | **drop** |
| `Services/ApplicationEmailSenderTests` | 4 | 0 | logging sender never logs subject, body or link; provider selection | |
| `Services/BootstrapSecretProviderTests` | 2 | 1(2) | dev generation and warning; prod throws without logging; configured secret used verbatim | |
| `Services/InvitationTokenServiceUrlTests` | 8 | 0 | invite and reset URL building, encoding, fail-fast on bad origin | |

CSRF tests (`IdentityAccountEndpointsTests`, `IdentityPasswordAuthIntegrationTests`) assert the antiforgery 400. They must be
rewritten for the Go `CrossOriginProtection` 403.

## 15. Surprises (likely port mistakes)

1. **No session store.** Revocation rotates the security stamp. Logout does not revoke server-side. **Profile edits end other
   sessions.** Decide deliberately which of these to keep with `identity.sessions` rows.
2. **Maintenance mode is advisory.** No backend gate; the SystemAdmin bypass exists only in the SPA
   (`system-status.ts:40-41`). Do not add a server-side block without deciding to.
3. `ActiveAccount` ignores lockout and SCIM-inactive state; `permission:*` checks lockout; session validation checks disabled and
   SCIM-inactive (non-Owner) but not lockout. Three different notions of "active".
4. `Owner` bypasses the permission table (`*`). Every `permission:*` policy also carries `Business`, so an Owner calling a
   permission route needs the MFA claim when `RequireMfa` is on. `Business` as a named policy has no users.
5. `/access/me` and `/access/users/{id}` compute permissions from **direct roles only**; the live check adds group-mapped roles.
6. Three error envelopes: `{"error":{…}}` (most), flat `{code,message}` (`/access/*`, maintenance 400), SCIM errors; OIDC failures
   are redirects. There are two different 409 codes under `/access` (`authorization_conflict` vs `concurrency_conflict`), and
   `role_mapped` is a 403, not a 409.
7. A disabled or SCIM-inactive account returns **429 `account_locked` before the password is checked** (`EA/AuthEndpoints.cs:306-310`),
   while an unknown email returns 401. There are three 429 paths on `/login`; only middleware 429s carry `Retry-After`.
8. The 2FA code type is decided by shape (6 digits → TOTP). `rememberMe` on `/login/2fa` makes the cookie persistent; plain
   login never is. Passkey login counts as MFA.
9. **Password-reset tokens are not stored today.** They are ASP.NET DataProtection tokens (24 h, invalidated by stamp rotation).
   Spec §3.7 says reset tokens are "256-bit random, SHA-256 stored, as today", which is only true of invitations. A Go table-based
   token must be single-use and die on password change (the replay-rejection test depends on it). The bootstrap secret is a config
   value, not a stored token.
10. TOTP secrets and recovery codes live in framework-managed `user_tokens` (format and storage UNCONFIRMED). Migrating existing
    installations requires reading them out; spec §3.7 wants recovery codes hashed.
11. Federated key = (normalized issuer, raw `sub`) in `user_logins`. The SHA-256 uses a `"\0"` separator and feeds only the
    username and the opaque `@sso.invalid` email, which `/session` then hides. There is no linking by email; an email collision is a hard failure.
12. The external OIDC cookie must be **SameSite=Lax**, not Strict, to survive the provider redirect back to `/complete`.
    OIDC `ResponseMode` is the framework default (GET and POST callback both in the contract).
13. SCIM: 14 endpoints (no Group PUT); a different precondition mechanism for Users vs Groups; the filter regex keeps only the
    last `and` clause; deletes are soft; the Owner is immune to SCIM; SCIM deactivation ends sessions without a stamp rotation.
14. `scim_user_mappings` → `users` is ON DELETE RESTRICT, so deleting a SCIM-provisioned user gives 409 `provenance_conflict`.
15. Bootstrap counts as "consumed" if *any* Owner exists, not only the marker row. A matching `SystemAdmin:Email` gets SystemAdmin
    at bootstrap. SystemAdminBootstrapper fails startup when the configured user is missing on a used installation.
16. The owner lock `0x56414e54` serialises bootstrap, Owner creation, invitation acceptance for Owner role, and Owner demotion.
    The last-active-Owner guard counts non-disabled, non-locked Owners.
17. An invitation send failure revokes the invitation. Resend mints a new row and token. Creating a user revokes that email's
    pending invites. There is one active invite per email (partial unique index).
18. Audit rows are transactional (their failure rolls back the change); operational events are deliberately non-transactional
    and best effort.
19. Delegation scopes are per delegation (disjoint scopes never combine) and are re-validated on every request. Dangling
    references fail closed.
20. The contract still has `tenants[]` and `activeTenantId` on `AuthSessionResponse` (and optional on `AuthSuccessResponse`).
    The Go port must emit something coherent, such as an empty list or a single synthetic tenant, until the contract changes.
21. The .NET MFA rule is `RequireMfa` (default false) plus a startup guard. Spec §3.7's "Owners must have MFA unless
    `OWNERS_ALLOW_INSECURE_NO_MFA=1`" changes the default in dev. The MFA requirement also binds SystemAdmins and delegated
    administrators, not only Owners.
22. Every rate-limit partition is keyed on the post-forwarded-headers IP. With `TRUSTED_PROXY_HOPS=0` behind a proxy, all
    users share one bucket.
23. `identity:manage` exists in the catalog (added by the host, non-delegable) but no endpoint requires it. Identity itself
    contributes no permissions.
