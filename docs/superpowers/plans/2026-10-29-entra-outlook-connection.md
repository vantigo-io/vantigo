# Outlook connection, delivery A — the person's delegated connection: implementation plan

> **For agentic workers:** implement this plan task by task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** a person whose Staff record names an Entra account the module manages opens
**Settings → Connections**, clicks **Connect Outlook** once, and, after one redirect that
SSO usually answers without a page, is *Connected as y@…*. Entra holds their sealed
refresh token, answers *Reconnect Outlook* to that person alone when Microsoft refuses
it, and closes the connection when they disconnect, leave, or the Owner turns the switch
off. Nothing uses the connection yet: the calendar keeps running on the application
permission until delivery B, and A and B ship in one release (spec M2).

**Architecture:** Entra gains a confidential-client authorization code flow with PKCE,
a nonce and `cp1`, hand-written on its guarded Graph client (identity's two-cookie
pattern: a sealed Lax state cookie to an anonymous callback, then a same-origin `complete`
under the Strict session, bound by a MAC of the session id). The person's row lives in
`entra.outlook_connections` (00089), every write guarded by `revision`. The Owner's
switch `calendar_sync` is renamed `outlook_connect` (00088) and `GET /connection` answers
`outlookConnect` in place of `calendarSync`. A token source for the delegated token —
the refresh, its failures mapped to `reconnect`, the CAE claims challenge — is built and
tested here and wired into the mailbox calls by B. Entra reads Staff's
`Workforce.DirectoryAccount` (rule 12) for the certain account, and gains its first
worker, `entra-outlook-sweep`. The host composes the new page from `@vantigo/entra-ui`
and fills the calendar card's new `connectionButton` slot with the same package's
button. Branch `feat/entra-outlook-connection`, cut from `main` at `c3cfbc78` with the
research and spec (`75557214`) on it.

**Spec:** [`docs/superpowers/specs/2026-10-29-outlook-connection-and-mail-design.md`](../specs/2026-10-29-outlook-connection-and-mail-design.md), M2 (less B's removals), M3–M8, M19, the owner's answers Q1–Q4.
**Research:** [`…/research/2026-10-29-outlook-connection-and-mail.md`](../research/2026-10-29-outlook-connection-and-mail.md) §2 (*Identity's sign-in*), §3 (*The authorization code flow*, *Refresh tokens*).

## Plan readings

1. **Base, numbering and the neighbour PR.** `main`'s last migration is
   `00086_calendar_mailbox_sync_mailbox.sql`; vantigo-io/vantigo#194 (open,
   `fix/worker-lease-pool-deadlock`) holds `00087_calendar_mailbox_sync_claim.sql` and
   edits `contributing/module-boundaries.md`, `modtest/mailbox_calendar.go` and
   `db/schema_calendar_mailbox_sync_test.go`. A takes `00088` and `00089`. Goose's
   provider refuses a migration numbered below one already applied, so whichever of #194
   and A lands second renumbers in its own history before rebasing (memory: *PRs land by
   rebase*). Expect a textual conflict in `schema_calendar_mailbox_sync_test.go` and
   `module-boundaries.md` if #194 lands first; neither touches what A changes in them.

2. **The rename is safe in A, and is done whole.** Every reader of the switch, found by
   `grep -ri 'calendar_sync\|calendarSync'`:
   - Entra's own Go: `store/models.go:59`, `store/connection.sql.go` (regenerated),
     `queries/connection.sql:64`, `connection.go:89-104` (`missingPermissions`),
     `:123-142` (`calendarSyncBody`), `:156`, `:174`, `:344`, `:413`, `:519`, `:585`;
     `mailbox.go:160-164` (`mailboxStatusOf`); `status.go:127` (the notifications' body
     type only); the tests `mailbox_test.go`, `mailbox_delta_test.go`,
     `mailbox_writeback_test.go`, `mailbox_notifications_test.go`,
     `mailbox_notifications_endpoint_test.go`, `mailbox_internal_test.go` (they turn the
     switch on through `PUT /connection/settings {"calendarSync": true}` or set the row).
   - The contract: `openapi/entra.yaml:74-95` (`EntraCalendarSync`), `:127-128`,
     `:191`, `:608-611` (`EntraSettingsRequest.calendarSync`), and its embedded copy.
   - The Entra frontend: `api/connection.ts:20,39-46`, `components/settings-form.tsx:20,
     43,58,61-73,88,108,126-132,266-274,317-360`, `components/step-card.tsx:157`,
     `i18n.ts:153,217-224` (en) and `:530,594-601` (nb), `pages/entra.test.tsx`,
     `test/server.ts`.
   - `db/schema_entra_test.go:343-356` and `db/schema_calendar_mailbox_sync_test.go:141-166`.
   - Docs: `reference/entra.md` (and the guides name the switch by its label).

   **The calendar module reads none of them.** It sees the switch only as
   `contracts.MailboxStatus` from `mailboxCalendar.Status` (`mailbox.go:131-173`), whose
   states and reasons (`available`, `cleanup`/`switch_off`, `unavailable`) do not change.
   No other module, no host code and no calendar frontend reads `calendarSync`. So the
   column, the store field (`OutlookConnect`), the JSON field and the settings field are
   renamed together in task 2, and `mailboxStatusOf` reads `conn.OutlookConnect` with the
   same three cases: **calendar behaviour is unchanged** — the switch on and the
   connection connected is still `available` on the application token.
   *Spec correction:* M2 says B removes "the `permission_missing` advisory". It goes in
   A, because A replaces the `calendarSync` object, the only place that status lives,
   by `outlookConnect`, whose status table (M2) has no such value. What survives until B
   is `Calendars.ReadWrite` in `permissionsMissing` while the switch is on
   (`missingPermissions(present, conn.OutlookConnect)`, unchanged logic) and step 3's
   *(for calendar sync)* label; `mailboxStatusOf` never read the role.

3. **Migration `00088_entra_outlook_connect.sql`** (Entra's schema only):
   ```sql
   -- +goose Up
   ALTER TABLE entra.connection RENAME COLUMN calendar_sync TO outlook_connect;
   ALTER TABLE entra.connection
       ADD COLUMN outlook_connect_at    timestamptz,   -- when the switch was last turned on
       ADD COLUMN outlook_last_error    varchar(40),   -- the last validated connect's failure (consent_missing)
       ADD COLUMN outlook_last_error_at timestamptz;
   UPDATE entra.connection SET outlook_connect_at = now() WHERE outlook_connect;
   -- +goose Down
   ALTER TABLE entra.connection
       DROP COLUMN outlook_last_error_at, DROP COLUMN outlook_last_error, DROP COLUMN outlook_connect_at;
   ALTER TABLE entra.connection RENAME COLUMN outlook_connect TO calendar_sync;
   ```
   The `UPDATE` covers an installation that turned the unreleased switch on (the owner
   says none did), so `untested`/`ready` (reading 9) have a start. No CHECK (house style).
   The header comment says why the rename breaks nothing (reading 2).

4. **Migration `00089_entra_outlook_connections.sql`** — the spec's M3 table, with four
   *plan decisions* grounded in the code:
   ```sql
   CREATE TABLE entra.outlook_connections (
       object_id          uuid         PRIMARY KEY,
       user_id            uuid         NOT NULL,      -- identity's user, no FK (rule 4)
       tenant_id          uuid         NOT NULL,
       scopes             text[]       NOT NULL,      -- as granted, lower-cased, sorted
       refresh_sealed     bytea,                      -- entra/outlook-refresh; NULL once revoked
       state              varchar(20)  NOT NULL,      -- 'active' | 'reconnect' | 'closing'
       error_code         varchar(40),
       connected_at       timestamptz  NOT NULL,
       refreshed_at       timestamptz,
       used_at            timestamptz,
       account_checked_at timestamptz,                -- B's daily accountEnabled read (M6)
       changed_at         timestamptz  NOT NULL,
       revision           bigint       NOT NULL DEFAULT 1,
       created_at         timestamptz  NOT NULL,
       updated_at         timestamptz  NOT NULL
   );
   CREATE UNIQUE INDEX ux_entra_outlook_connections_user ON entra.outlook_connections (user_id)
       WHERE state <> 'closing';
   CREATE INDEX ix_entra_outlook_connections_changed ON entra.outlook_connections (changed_at);
   ```
   - `refresh_sealed` is **`bytea`, not the spec's `text`**: every sealed column of the
     module is `bytea` (`accounts.pass_sealed`, `credentials.sealed`, 00061) and
     `secrets.Box.Seal` answers `[]byte`.
   - `account_checked_at` is **added**: M6's "at most once a day" read for a row in
     `reconnect` needs a time to compare, and the spec says B has no migration of its own.
   - `ix_…_changed` is **added**: B's `Connections(since)` (M9) reads by `changed_at`,
     the way `Notified` reads `notified_at` through its index (00085).
   - Index names carry the `entra_` infix (house style: `ix_entra_mailbox_subscriptions_notified`).

   Down drops the table. `internal/entra/sqlc.yaml` lists `00088` and `00089` after
   `00085`; `db/schema_test.go`'s `sqlcSchemaFiles` check keeps that list honest.

5. **Schema tests** (`internal/db`): a new `schema_entra_outlook_test.go` with
   `entraOutlookConnectVersion = 88` and `entraOutlookConnectionsVersion = 89`,
   `applyUpDownUp` for each, the columns pinned (`outlook_connect:boolean:NO:false`, the
   three new ones), the rename undone by the down, the table's columns, the partial
   unique index refusing a second live row for a user and admitting a `closing` one
   beside it. `TestEntraCalendarSync_AppliesAndIsIdempotent`
   (`schema_calendar_mailbox_sync_test.go:141-166`) reads the column at head today; it
   now `migrateTo(t, url, entraCalendarSyncVersion)` before reading `calendar_sync`,
   then checks the down as before. `schema_entra_test.go:343-356`'s tail becomes
   `service_principal_id, skus, outlook_connect` (a rename keeps the ordinal), and its
   filter also drops columns prefixed `outlook_connect_at`, `outlook_last_error`.

6. **Queries.** `queries/connection.sql`:
   - `UpdateConnectionSettings` writes `outlook_connect = @outlook_connect` and
     `outlook_connect_at = CASE WHEN @outlook_connect::boolean AND NOT outlook_connect
     THEN @now::timestamptz ELSE outlook_connect_at END`.
   - `SetOutlookLastError :exec` — `outlook_last_error = @code, outlook_last_error_at =
     @now`; `ClearOutlookLastError :exec` — both NULL `WHERE outlook_last_error IS NOT
     NULL`. Neither moves `version` (like `SetConnectionStatus`).

   A new `queries/outlook_connections.sql` (all leave the connection's `version` alone):
   - `GetOutlookConnection :one` by `object_id`; `GetLiveOutlookConnectionByUser :one`
     (`user_id = @user_id AND state <> 'closing'`).
   - `LockOutlookConnection :one` / `LockLiveOutlookConnectionByUser :one` — the same
     with `FOR UPDATE`, for `complete`.
   - `InsertOutlookConnection :one` — `state 'active'`, `revision 1`, `connected_at =
     refreshed_at = changed_at = created_at = updated_at = @now`.
   - `TakeOutlookConnection :one` — `complete` over an existing row of the same account
     (the caller's, in any state, or anyone's `closing` one): `user_id, tenant_id,
     scopes, refresh_sealed`, `state 'active'`, `error_code NULL`, `connected_at =
     refreshed_at = changed_at = @now`, `revision = revision + 1` `WHERE object_id =
     @object_id AND revision = @revision RETURNING *`.
   - `CloseOutlookConnection :one` — `state 'closing'`, `changed_at`, `revision + 1`,
     `refresh_sealed = CASE WHEN @forget THEN NULL ELSE refresh_sealed END`, `WHERE
     object_id = @object_id AND state <> 'closing' RETURNING user_id, revision`. A
     disconnect keeps the token for removals (M7); a leaver and a deleted account forget
     it. A second close of a closing row with `@forget` still nulls the token:
     `CloseOutlookConnection` is followed, when `@forget`, by `ForgetOutlookRefresh
     :exec` (`refresh_sealed = NULL, revision + 1 WHERE object_id = @object_id AND
     refresh_sealed IS NOT NULL`).
   - `CloseAllOutlookConnections :many` — the switch turned off: every live row to
     `closing`, `RETURNING object_id, user_id`.
   - `StoreOutlookRefresh :execrows` — a refresh's new token: `refresh_sealed, scopes,
     refreshed_at = used_at = @now, revision + 1 WHERE object_id = @object_id AND
     revision = @revision AND refresh_sealed IS NOT NULL`. Zero rows is the lost race
     (M3), not an error.
   - `MarkOutlookReconnect :execrows` — `state 'reconnect', error_code = @code,
     changed_at, revision + 1 WHERE object_id = @object_id AND revision = @revision AND
     state = 'active'`. Zero rows means a newer `complete` or another failure won.
   - `TouchOutlookUsed :exec` — `used_at = @now WHERE object_id = @object_id AND (used_at
     IS NULL OR used_at < @now - interval '1 hour')`.
   - `CountOutlookConnections :one` — `count(*) FILTER (WHERE state = 'active') AS
     connected, count(*) FILTER (WHERE state = 'reconnect') AS reconnect`;
     `OutlookConnectedSince :one` — `EXISTS (… WHERE state <> 'closing' AND
     connected_at >= @since)`.
   - `DeleteSpentOutlookConnections :many` (reading 18).

7. **Secrets, cookies and paths** (`internal/entra/outlook.go`). Purposes, each its own
   key (`secrets.Box`, HKDF by purpose), after `clientSecretPurpose`
   (`credentials.go:33`) and `passPurpose` (`lifecycle_activate.go:25`):
   `outlookStatePurpose = "entra/outlook-connect-state"`,
   `outlookConnectedPurpose = "entra/outlook-connected"`,
   `outlookRefreshPurpose = "entra/outlook-refresh"`, and the MAC purpose
   `outlookSessionPurpose = "entra/outlook-connect-session"` (`Box.MAC`, compared with
   `secrets.MACEqual`). Cookies, mirroring `identity/oidc.go:50-75,918-979`:
   `vantigo.entra.connect` (10 min) and `vantigo.entra.connected` (5 min), both
   HttpOnly, `SameSite=Lax`, `Secure = Config.UsesHTTPS()`, `Path = BasePath +
   "/api/v1/entra/outlook"`, `Max-Age` and an expiry sealed inside checked on the Deps
   clock; every answer of the step that reads one clears it (single use, a 500
   included — `clearOnServerError`'s rule). The redirect URI is `Config.AppOrigin +
   Config.BasePath + "/api/v1/entra/outlook/callback"` (identity's
   `oidc.go:174` shape), on `APP_URL`, never the public host, so
   `security.PayRoutes` is unchanged. The page is `BasePath + "/settings/connections"`.
   Entra cannot reach identity's `Access.cookie`, so it builds the cookie itself with
   the same fields (`identity/cookies.go:93-105`).

   *Plan decision — the refresh token in the `connected` cookie:* as the spec's flow
   has it, sealed. Its sealed, base64url form is bounded: the callback refuses (`failed`,
   logged at warning with the length, never the value) a cookie over 3,800 bytes, so a
   browser never drops it silently. A typical v2 refresh token is well under that; the
   fake's is 1.5 KB.

8. **The contract** (`openapi/entra.yaml`, keys sorted as the file is):
   - `EntraOutlookConnect` replaces `EntraCalendarSync`: `{enabled: bool, status: enum
     off|ready|untested|consent_missing|unavailable, reason?: string, connected: int32,
     reconnect: int32, redirectUri: string, notifications:
     EntraCalendarSyncNotifications}`, required all but `reason`. The notifications
     schema keeps its name and shape (M2: "moved unchanged"), so `status.go:127` changes
     no type. *Plan decision:* `redirectUri` is added — the Entra page shows the exact
     URI to copy, as it shows the client id, since it depends on `APP_URL` and
     `APP_BASE_PATH`.
   - `EntraConnection`: `outlookConnect` and `permissionsUnexpected: string[]` (both
     required) in place of `calendarSync`; `EntraConnectionTest` gains
     `permissionsUnexpected` too (*plan decision*: the test's answer mirrors
     `permissionsMissing` today). `permissionsMissing`'s description keeps "and
     Calendars.ReadWrite while the Outlook connection switch is on".
   - `EntraSettingsRequest.outlookConnect` replaces `calendarSync`; its description says
     turning it off closes every person's connection (M7).
   - `EntraOutlookConnectRequest {feature: enum [calendar], returnTo?: string}`;
     `EntraOutlookAuthorize {authorizeUrl: string}`; `EntraOutlookConnection` (reading
     14); `EntraOutlookComplete {connection: EntraOutlookConnection, returnTo: string}`.
   - Operations, each `tags: [Entra]`:

     | operationId | Path | Access | Answers |
     | --- | --- | --- | --- |
     | `postEntraOutlookConnect` | `POST /api/v1/entra/outlook/connect` | `session` | 200 `EntraOutlookAuthorize` (+ `Set-Cookie`); 409 `switch_off`, `no_account`, `account_taken`; 422 `feature_unknown`; 429 |
     | `getEntraOutlookCallback` | `GET /api/v1/entra/outlook/callback` | `anonymous` | 302 `Location`; query `code`, `state`, `error`, `error_description` (read for nothing), `session_state` (ignored); 429 |
     | `postEntraOutlookComplete` | `POST /api/v1/entra/outlook/complete` | `session` | 200 `EntraOutlookComplete`; 409 `switch_off`, `no_account`, `account_mismatch`, `account_taken`; 410 `expired`; 429 |
     | `getEntraOutlookConnection` | `GET /api/v1/entra/outlook/connection` | `session` | 200 `EntraOutlookConnection` |
     | `deleteEntraOutlookConnection` | `DELETE /api/v1/entra/outlook/connection` | `session` | 204 |

     `session` is in the grammar (`contracts/permission.go:76`) and used by identity's
     `getIdentitySession`. `EntraProblem`'s description lists the new codes.
   - Generation: `go generate ./...` in `apps/server` (copies the contract into
     `internal/openapi/specs/`, which `TestEmbeddedSpecsMatchTheContract` guards, runs
     `oapi-codegen -config internal/openapi/gen/cfg-entra.yaml` into
     `internal/entra/gen/api.gen.go` and `sqlc generate -f internal/entra/sqlc.yaml`);
     `bun run gen:client` for every frontend's `api-schema.d.ts`.

9. **`outlookConnect`'s status** (`connection.go`, `outlookConnectBody(conn, status,
   reason, counts, since bool, notifications, redirectURI)` in place of
   `calendarSyncBody`): `off` while `!conn.OutlookConnect`; `unavailable` with the
   connection's reason while `statusOf` is not `connected`; `consent_missing` while
   `conn.OutlookLastError == "consent_missing"`; `ready` when `OutlookConnectedSince(
   *conn.OutlookConnectAt)`; else `untested`. In that order. `connected` and
   `reconnect` from `CountOutlookConnections`. `readConnection` (`connection.go:185-203`)
   gains the two reads.

10. **M19, `permissionsUnexpected`** (`connection.go`): `unexpectedPermissions(present)`
    — every role in `present` (already sorted and compacted by `claimsOf`,
    `graph_token.go:219-245`) that is neither in `requiredPermissions`
    (`connection.go:35-44`) nor `permissionCalendarsReadWrite` (`:49`), unconditionally
    in A (B drops the exception). *Plan decision:* the `Mail.*` and `full_access_as_app`
    roles count as unexpected in A; C moves them out when `tenant_wide` reports them
    (M10), so they are never reported twice once both exist. `connectionBody` answers
    it; `PostEntraConnectionTest` (`:585-607`) answers it and appends
    `unexpected:<permission>` to the audited codes after the `missing:` ones. A warning,
    never a status: `statusOf` is untouched.

11. **`POST /outlook/connect`** (`outlook_connect.go`): the principal (`requestActor`,
    `events.go:73`; `contracts.PrincipalFrom` for `SessionID`, `contracts/access.go:12-18`);
    `feature` checked against `outlookFeatures = map[string][]string{"calendar":
    {"Calendars.ReadWrite"}}` (422 `feature_unknown`); the switch (409 `switch_off`);
    the certain account (reading 16, 409 `no_account`); a live row of that object id
    held by another user (409 `account_taken`, *plan decision*: refused early as well as
    at `complete`, which still decides). Then a fresh state and nonce (32 random bytes,
    base64url, as `randomOIDCValue`), a PKCE verifier (`oauth2.GenerateVerifier`, S256
    by `oauth2.S256ChallengeFromVerifier`), sealed with `{userId, sessionMac, feature,
    scopes, returnTo: safeReturnTo(in.ReturnTo), expiresAt}` into
    `vantigo.entra.connect`. The URL, built by hand (no `oauth2.Config`, *plan decision*:
    the code redemption must read AADSTS `error_codes` and go through the guarded
    client): `https://login.microsoftonline.com/{tenant}/oauth2/v2.0/authorize` with
    `client_id`, `response_type=code`, `response_mode=query`, `redirect_uri`, `scope=
    "openid profile offline_access https://graph.microsoft.com/Calendars.ReadWrite"`,
    `state`, `nonce`, `code_challenge`, `code_challenge_method=S256`, `login_hint` (the
    account's `user_principal_name` from `entra.accounts`), `domain_hint` (the UPN's
    domain when it is among `conn.VerifiedDomains`, else omitted), `claims=
    {"access_token":{"xms_cc":{"values":["cp1"]}}}`; no `prompt`. 200
    `{authorizeUrl}` through a response type of its own that sets the cookie before the
    JSON (identity's `oidcRedirect` pattern, `oidc.go:879-899`).

12. **`GET /outlook/callback`** (`outlook_connect.go`, `outlook_redeem.go`). Every
    answer is a 302 to `BasePath + "/settings/connections?outlook=failed&code=<code>"`
    or `…?outlook=complete`, and clears `vantigo.entra.connect`. In order:
    1. The cookie opened under `outlookStatePurpose` and unexpired, and `state` equal to
       it (`crypto/subtle.ConstantTimeCompare`) — else `expired`, **and nothing is
       written anywhere**.
    2. `error=access_denied` → `access_denied`; any other `error` → `failed`; no
       `code` → `failed`.
    3. The switch still on and a credential (`credentialFor`, `credentials.go:86`) —
       else `failed`. The client secret is opened from `cred.sealed` for this one request.
    4. The redemption: `POST /{tenant}/oauth2/v2.0/token` (`graphTokenEndpoint`,
       `graph.go:50`) through `c.http` with `grant_type=authorization_code`, `code`,
       `redirect_uri`, `code_verifier`, `client_id`, `client_secret`, the same `scope`
       and `claims`, the answer read through `readCapped(…, graphMaxBody)`. Refusals by
       code only, never `error_description`: `invalid_grant` with `error_codes`
       70008 or 54005 → `expired`; `invalid_client`/`unauthorized_client` → `failed`
       and the credential marked failed (`MarkCredentialFailed`, as `pickCredential`
       does); anything else, a transport error or no `refresh_token` → `failed`.
    5. The id_token verified with go-oidc (`github.com/coreos/go-oidc/v3`, already in
       `go.mod` for identity): `oidc.NewVerifier("https://login.microsoftonline.com/
       {tid}/v2.0", keySet, &oidc.Config{ClientID})` over
       `oidc.NewRemoteKeySet(oidc.ClientContext(ctx, c.http), "https://login.
       microsoftonline.com/{tid}/discovery/v2.0/keys")`, the key set kept per tenant in
       a process-wide `sync.Map` like `processTokens` (`graph_token.go:75`); no discovery
       document. Then `nonce` in constant time, `oid` a GUID (`objectID`,
       `lifecycle_user.go:131`). A failure → `failed`. `tid` not the connection's
       tenant → `tenant_mismatch` (also written by nothing).
    6. The scopes: the token answer's `scope`, each with any
       `https://graph.microsoft.com/` prefix dropped, lower-cased, sorted, compacted.
       When the feature's scopes are not all there, the validated callback records
       `SetOutlookLastError("consent_missing")` and answers `consent_missing`.
       *Plan decision:* the spec stores "the connection without that feature"; with one
       feature in A that is a connection with none, so nothing is stored, and the rule
       becomes the spec's once a second feature exists (a row keeps the features it has).
       `consent_missing` is a callback code the spec's list (M4) lacks.
    7. `vantigo.entra.connected` sealed with `{userId, sessionMac, oid, tid, upn
       (preferred_username), scopes, refreshToken, feature, returnTo, expiresAt}`; 302
       `…?outlook=complete`.

    Logged at info with the code and a reason, never a code, token, state, nonce or
    secret (`identity/oidc.go:909-916`'s rule).

13. **`POST /outlook/complete`** (`outlook_complete.go`): the cookie opened under
    `outlookConnectedPurpose` and unexpired, its `userId` the principal's and its
    `sessionMac` equal to `MAC(principal.SessionID)` — else 410 `expired`
    (*plan decision*: another session's or another user's cookie is answered as if
    there were none; the spec names no code for it). The switch (409 `switch_off`); the
    certain account read again **before** any transaction (the lock rule,
    `contractscalls.go`), 409 `no_account`; `oid` and `tid` against it and the
    connection, else 409 `account_mismatch` with the detail "You signed in as {upn};
    Vantigo knows you as {account's UPN}." Then one transaction:
    `LockOutlookConnection(oid)` and `LockLiveOutlookConnectionByUser(me)`;
    a live row of `oid` held by another user → 409 `account_taken`; the caller's live
    row of another object id (an account re-pointed in Staff) → `CloseOutlookConnection`
    without forgetting, event `entra.outlook.closed` code `repointed`; then
    `TakeOutlookConnection` over a row of `oid` (the caller's, or a `closing` one), else
    `InsertOutlookConnection`. A unique violation (`23505`) from a concurrent `complete`
    → 409 `account_taken`. The refresh token sealed under `outlookRefreshPurpose`.
    `ClearOutlookLastError`. Event `entra.outlook.connected`, the object id, the UPN,
    codes `feature:calendar`. 200 `{connection, returnTo}`. The cookie cleared on every
    answer.

14. **The person's own reads** (`outlook_complete.go`). `GET /outlook/connection` answers
    `EntraOutlookConnection {offered: bool, reason?: enum switch_off|no_account, state:
    enum not_connected|active|reconnect|closing, account?: string, scopes: string[],
    features: string[], connectedAt?: date-time, errorCode?: string}` — `account` the
    certain account's UPN (*Vantigo knows you as*), `features` derived from the scopes
    through `outlookFeatures`. *Spec departure:* the spec answers "the caller's own row
    or 404"; the page and the button must tell "not connected, may connect" from "the
    switch is off or you have no account" (M8: nothing shows then), which a 404 cannot,
    so the read is always 200 and says whether it is offered and why not. A `closing`
    row of the caller's is answered `closing` (the page shows *Not connected*).
    `DELETE /outlook/connection`: the caller's live row → `CloseOutlookConnection`
    keeping the token, event `entra.outlook.disconnected`; 204 whether or not there was
    one. No `revokeSignInSessions`, ever (M7).

15. **`returnTo`** (`safeReturnTo` in `outlook.go`, and `safeReturnTo` in
    `apps/entra/frontend/src/lib/return-to.ts`, the same rule): at most 512 bytes; starts
    with `/`; its second byte is neither `/` nor `\`; no `\` anywhere; no byte below
    0x20 nor 0x7f; `url.Parse` (`new URL(x, "http://h")` in the browser, same origin
    checked) yields no scheme and no host. Anything else, and an empty value, becomes
    `/settings/connections`. Checked when sealed and again in the browser before
    navigating. An internal test table: `//x`, `/\x`, `/%5Cx` (kept: a path),
    `https://x`, `javascript:x`, `/a\nb`, `/a\x7f`, 513 bytes, `/calendar/settings`.

16. **The certain account, M6** (`contractscalls.go`, `outlook.go`): an accessor
    `workforceDirectoryAccount(ctx)` — `noteContractCall(ctx, "Workforce.DirectoryAccount")`,
    then `s.deps.Workforce.DirectoryAccount(ctx)` (`contracts/workforce.go:71-92`),
    `nil, nil` when `Deps.Workforce` is nil — the calendar's own accessor
    (`calendar/contractscalls.go:326-334`) copied. `certainAccount(ctx)` answers the
    account row when `Provider == providerKey` (`provisioner.go:22`), `Enabled`, the
    `ExternalID` an object id, its `entra.accounts` row `trusted` (`lifecycle_user.go:429`)
    and `State == stateEnabled`; otherwise none (`no_account`). The call is made under
    the request's own principal: it answers only the caller's own account. The file's
    header comment and `module.go`'s package doc stop saying the module "depends on
    nobody but identity".

17. **The token source, M3 and M5** (`outlook_token.go`) — built and tested in A,
    called by nothing in production until B:
    - `outlookTokens.token(ctx, q, oid, removal bool) (string, error)`: the row by
      `object_id`; none → `connectionNeeded{reason: "not_connected"}`; `reconnect` →
      `connectionNeeded{reason: row.error_code}`; `closing` without `removal`, or with
      `refresh_sealed` NULL → `connectionNeeded{reason: "closing"}`. A cache hit by
      `delegatedKey{oid, revision, clientID, sha256(refresh_sealed)}` (the digest keeps
      two test databases apart, as `tokenKey` does, `graph_token.go:41-52`), dropped
      `tokenEarlyExpiry` before expiry; `TouchOutlookUsed`.
    - The refresh: `grant_type=refresh_token`, the opened refresh token, `client_id`,
      the client secret (`credentialFor`), `scope` = the row's Graph scopes plus
      `offline_access`, `claims` with `cp1`. Success → `StoreOutlookRefresh` with the
      new token sealed; zero rows (lost race) keeps the winner's token and answers this
      access token all the same (both are valid, research §3).
    - The failure table, by `error` and `error_codes` (the token endpoint's int array),
      as `outlookRefusalCode(answer) (code string, person bool)`: 70008, 700082, 50173
      → `expired`; 70043, 50132 → `sign_in_again`; 50076, 50078, 50079 → `mfa`; 53000,
      53003 → `blocked`; 65001 → `consent_missing`; `interaction_required` or any other
      `invalid_grant` → `sign_in_again`; `invalid_client`/`unauthorized_client` → not the
      person's: `errCredentialInvalid` and the credential marked failed; 5xx, 429,
      timeouts and transport errors → transient, the row stays `active`. A person's
      failure → `MarkOutlookReconnect(oid, revision, code)` and, when it moved the row,
      event `entra.outlook.reconnect_needed` with the code; the answer is
      `connectionNeeded{reason: code}`.
    - **CAE.** `graph.go`'s `send` (`:397-463`) takes its token from a small
      `tokenSource` interface instead of `c.token(ctx, cred)`: `token(ctx)`,
      `dropped()` (today's `c.tokens.drop(cred)` at `:446-448`) and `challenged(ctx,
      header) error`. The application credential implements it exactly as today
      (`challenged` answers nil: drop and retry once, unchanged); the delegated source's
      `challenged` reads a 401 whose `WWW-Authenticate` carries
      `error="insufficient_claims"` and a `claims=` value, moves the row to `reconnect`
      with `sign_in_again` (*plan decision*: the spec names no code; every CAE event —
      disable, delete, password reset, revocation, risk — needs the person to sign in)
      and answers `connectionNeeded` without a retry. The existing `get`/`post`/… keep
      their signatures and wrap `cred` (no call site changes in A); a
      `getAs(ctx, src, path, into)` exists for the delegated test call. `graphCredential`
      and `tokenFor` are unchanged.
    - `connectionNeeded` is Entra's own error type in A. *Plan decision:* the spec's
      `contracts.ErrConnectionNeeded` (M5) is a contract change the spec itself lists
      under B (M9, *Contract changes*); A adds nothing to `internal/contracts`, and B
      wraps `connectionNeeded` in the new sentinel.

18. **Closing, M7** (`outlook_close.go`, `outlook_sweep.go`):
    - **A leaver**: `Deactivate`'s transaction (`lifecycle_deactivate.go:87-100`, beside
      `SetAccountState` and `ClearAccountPass`) closes the account's row with `@forget`
      (state `closing`, `refresh_sealed` NULL), event `entra.outlook.closed` code
      `leaver` under the call's actor (`system:staff-accounts`). Whatever the leaver's
      later steps do, the token is gone first. `Delete` (`:297-330`) and `markDeleted`
      do the same with code `deleted`.
    - **The switch off**: `PutEntraConnectionSettings` (`connection.go:427-476`), in its
      transaction, when the row's `OutlookConnect` was true and the merge sets it false:
      `CloseAllOutlookConnections`, one `entra.outlook.closed` event per row, code
      `switch_off`, under the caller. Turning it on sets `outlook_connect_at` (reading 6).
      `mergeSettings` (`:331-421`) maps `outlookConnect` and marks the field changed
      under that name.
    - **The sweep**: Entra's first worker, `entra-outlook-sweep`, interval one hour, no
      setting (*plan decision*: the module "has no worker" today and M7's deletion needs
      something to run; nothing else in Entra runs on a clock). `Module.Workers`
      returns it; it runs in the API and in worker mode alike. One statement:
      ```sql
      -- name: DeleteSpentOutlookConnections :many
      DELETE FROM entra.outlook_connections c
      WHERE c.state = 'closing'
        AND (c.changed_at < @now::timestamptz - interval '7 days'
             OR (COALESCE(c.used_at, c.changed_at) < @now::timestamptz - interval '24 hours'
                 AND NOT EXISTS (SELECT 1 FROM entra.mailbox_subscriptions s
                                 WHERE s.object_id = c.object_id)))
      RETURNING object_id, user_id;
      ```
      and `entra.outlook.deleted` per row under `system:entra-outlook`. In A a closing
      row of a person the calendar syncs on the application path keeps its
      subscription row, so it waits the seven days; that is harmless and B makes the
      subscription the person's.
    - `recordEvent` (`events.go:47-68`) refuses any system actor but Staff's two
      (`contracts.Actor.Valid`); it gains the module's own `outlookSystemActor =
      "system:entra-outlook"` as a second accepted value, kept in Entra (*plan
      decision*: identity's audit never sees it, so `contracts` is untouched).

19. **Events** (`events.go:28-36`): `actionOutlookConnected = "entra.outlook.connected"`,
    `actionOutlookDisconnected = "entra.outlook.disconnected"`, `actionOutlookReconnect =
    "entra.outlook.reconnect_needed"` (the spec's `outlook.reconnect_needed` with the
    module's prefix), `actionOutlookClosed = "entra.outlook.closed"` (codes `leaver`,
    `deleted`, `switch_off`, `repointed`), `actionOutlookDeleted = "entra.outlook.deleted"`.
    All within `action varchar(40)`; never a token, a code from Microsoft's words, or an
    address beyond the account's UPN.

20. **Access and limits** (`module.go:44-119`): `outlookCallbackLimit = {Name:
    "entra-outlook-callback", Limit: 60, Window: time.Minute}` (M4) on
    `getEntraOutlookCallback`; *plan decision*: `outlookLimit = {Name: "entra-outlook",
    Limit: 30, Window: time.Minute}` shared by `postEntraOutlookConnect` and
    `postEntraOutlookComplete` (each mints or spends a sealed cookie; nobody connects
    thirty times a minute), each with its reason in a comment. The GET and DELETE are
    unlimited, like the module's other reads. CSRF: the two POSTs are under the
    cross-origin protection of every `/api` request; the callback is a safe GET acting
    only on a state it sealed.

21. **The fake authorization server** (`graphtest/authorize.go`, the package doc's list
    extended): `New` generates an RSA key; the fake serves
    `GET /{tid}/discovery/v2.0/keys` (the JWK) and its token endpoint
    (`graph.go:557-614`) learns two grants beside `client_credentials`:
    - `IssueCode(CodeGrant{ClientID, RedirectURI, Challenge, Scopes, Nonce, OID, TID,
      UPN, Granted []string}) string` — what `/authorize` would have answered; the test
      reads `code_challenge`, `nonce` and `scope` from the `authorizeUrl` it was given.
      `authorization_code` checks the secret, `redirect_uri`, S256 of `code_verifier`,
      single use (54005 on reuse), a minute's life on the fake clock (70008), and
      answers `access_token`, an RS256 `id_token` (`iss`, `aud`, `tid`, `oid`, `nonce`,
      `preferred_username`, `iat`, `exp`), `refresh_token` (1.5 KB, random) and `scope`
      from `Granted` (Graph's own style: bare `Calendars.ReadWrite`, plus `User.Read`).
      `IDTokenFor` lets a test tamper (another `tid`, `nonce`, `aud`, a bad signature).
    - `refresh_token`: each use answers a new refresh token and leaves the old one
      valid (both redeemable, research §3); `RefuseRefresh(token, error string, codes
      ...int)` queues a refusal; `RefreshCalls()` counts.
    - Delegated access tokens carry `scp`, `oid`, `tid`; Graph learns `GET /me`
      (`{id, userPrincipalName}`), and `ChallengeNext(oid)` makes that user's next
      Graph call 401 with `WWW-Authenticate: Bearer error="insufficient_claims",
      claims="…"`.
    Its own tests in `graphtest/authorize_test.go`.

22. **The Entra frontend** (`apps/entra/frontend`):
    - `api/connection.ts`: `EntraOutlookConnect` in place of `EntraCalendarSync`;
      `permissionsToAdd` reads `connection.outlookConnect.enabled`. A new
      `api/outlook.ts`: `outlookConnectionQueryOptions` (`[ENTRA_QUERY_KEY, "outlook"]`),
      `startConnect({feature, returnTo})` (then `window.location.assign(authorizeUrl)`),
      `completeConnect()`, `disconnect()`.
    - `components/settings-form.tsx`: the *Calendar sync* block becomes *Outlook
      connection* — the switch (`outlookConnectSwitch`), its description, the status line
      by `status` (`ready` teal, `untested` blue, `consent_missing` orange, `unavailable`
      dimmed with the reason), the counts, the redirect URI with a copy button, and the
      *Change notifications* block reading `outlookConnect.notifications` (unchanged
      otherwise). Turning the switch off while `connected + reconnect > 0` asks first
      (a `modals.openConfirmModal`): "Everyone will have to connect Outlook again."
    - `components/status-card.tsx`: under the permissions, when
      `permissionsUnexpected` is not empty, an orange `Alert` with M19's sentence and the
      list.
    - New `components/outlook-connection.tsx`: `OutlookConnectionCard` (the page's card:
      *Not connected* + **Connect Outlook**; *Connected as {{account}}* since a date, the
      features (*Calendar*), **Disconnect** with a confirm; *Reconnect needed* with the
      reason and **Reconnect Outlook**; nothing but a short *Nothing to connect* line when
      not offered) and `OutlookConnectButton({feature, returnTo})` (the slot's filler:
      the same three states inline, nothing when not offered). The card takes
      `{outcome?: {outlook: "complete" | "failed"; code?: string}, onReturn(to: string)}`:
      `complete` runs `completeConnect` once (a ref guards React's double effect), then
      `onReturn(safeReturnTo(answer.returnTo))`; `failed` shows the code's sentence.
      Exported from `src/index.ts`.
    - i18n (`i18n.ts`, en and nb, same keys): `outlookConnect`, `outlookConnectSwitch`
      ("Outlook connection: let people connect their Outlook to Vantigo" / "Outlook-
      tilkobling: la folk koble Outlook til Vantigo"), `outlookConnectDescription`,
      `outlookConnectStatus_{ready,untested,consent_missing,unavailable}`,
      `outlookConnectCounts`, `outlookRedirectUri`, `outlookConnectOffTitle`,
      `outlookConnectOffBody`; `permissionsUnexpected`, `permissionsUnexpectedHint`;
      `outlook.title`, `outlook.notConnected`, `outlook.connect` ("Connect Outlook" /
      "Koble til Outlook"), `outlook.connectedAs`, `outlook.connectedSince`,
      `outlook.usedBy`, `outlook.feature_calendar`, `outlook.disconnect` /
      `outlook.disconnectConfirm*`, `outlook.reconnectNeeded`, `outlook.reconnect`
      ("Reconnect Outlook" / "Koble til Outlook på nytt"),
      `outlook.reason_{expired,sign_in_again,mfa,blocked,consent_missing}`,
      `outlook.failed_{access_denied,tenant_mismatch,expired,failed,consent_missing,
      account_mismatch,account_taken,no_account,switch_off}`, `outlook.nothingToConnect`.
      The removed `calendarSync*` keys go in both languages (`bun run translations:check`).
    - Tests: `pages/entra.test.tsx` (the switch's rename, the four statuses, the counts,
      the redirect URI copied, the off-confirm, the unexpected-permissions alert),
      `components/outlook-connection.test.tsx` (each state, connect navigating to the
      URL, complete once and returning to a safe `returnTo`, an unsafe one sent to
      `/settings/connections`, each failure sentence, nothing when not offered — in both
      languages), `lib/return-to.test.ts`; `test/server.ts` fixtures.

23. **The host** (`apps/host/frontend`). *Spec correction:* M8 says "the host composes,
    and never imports Entra". The host imports module packages — it composes
    `@vantigo/entra-ui`'s `EntraPage` today (`routes/entra/index.tsx`); rule 7 forbids a
    module package importing another or the host. What must not import Entra is the
    calendar, and it does not (reading 24). So:
    - `routes/settings/connections.tsx`: the page (`PageHeader` "Connections" /
      "Tilkoblinger" from the host's `settings` catalog), `validateSearch` for
      `{outlook?: "complete" | "failed", code?: string}`, rendering
      `OutlookConnectionCard` with the outcome and `onReturn` pushing to the router's
      history (and stripping the query when it returns to itself).
    - `apps.ts:520-531`: a third item `{label: "navigation.connections", to:
      "/settings/connections", icon: IconPlugConnected, module: "entra"}` — hidden when
      the module is off by `visibleNavSections` (`navigation.ts:100`).
    - `catalogs/navigation.ts` (`:63-64`, `:143-144`): `navigation.connections`;
      `catalogs/settings.ts`: `settings.connections`, `settings.connectionsDescription`.
    - `routes/calendar/settings.tsx`: `SettingsPage` given `connectionButton={(p) =>
      <OutlookConnectButton {...p} />}` when `enabledModuleKeys().includes("entra")`.
    - `routeTree.gen.ts` regenerated; tests: the nav item shown and hidden with the
      module, the route's search validated, the calendar route passing the slot.

24. **The calendar's slot** (`apps/calendar/frontend/src/pages/settings.tsx`):
    `SettingsPage` (`:92`) takes `connectionButton?: (props: {feature: "calendar";
    returnTo: string}) => ReactNode` and hands it to `OutlookSection` (`:98`, `:262`),
    which renders `connectionButton?.({feature: "calendar", returnTo:
    "/calendar/settings"})` under the card's description. The spec calls it the host
    slot `connection.button`; in code it is that prop, the way `extraTabs` is
    `PersonPage`'s. The package names nothing that fills it and renders nothing when it
    is absent. No calendar API call changes. `settings.test.tsx`: the slot rendered
    with those props, and nothing without it.

25. **What A leaves to B**: `mailboxSession`'s token source, the markers on the
    connection's client id, `Status` and every mailbox call on the person's token, the
    daily `accountEnabled` read, `contracts.ErrConnectionNeeded`, `Connections`,
    removing `Calendars.ReadWrite` (the ninth permission, its M19 exception, the
    *(for calendar sync)* label and the RBAC procedure), and the calendar's parking and
    waking. A's calendar sync runs as it does on `main`.

## Tasks

- [ ] **1. This plan.** Committed alone, no `Docs-Impact` trailer:
  `docs(entra): delivery A's plan — the Outlook connection: the flow on the guarded client, entra.outlook_connections (00089), the switch renamed (00088), the token source and its reconnects, closing and the sweep, permissionsUnexpected, the Connections page and the calendar's slot`.

- [ ] **2. The foundation (one worker, alone).** Readings 2–8, 19 and 20's table.
  - [ ] Write the schema tests first (reading 5) and see them fail.
  - [ ] Add `00088` and `00089`; list them in `internal/entra/sqlc.yaml`.
  - [ ] Write the queries (reading 6); `sqlc generate -f internal/entra/sqlc.yaml`.
  - [ ] Change the contract (reading 8); `go generate ./...` in `apps/server`;
    `bun run gen:client`.
  - [ ] Carry the rename through Entra's Go and tests (reading 2): `OutlookConnect` in
    `mergeSettings`, `missingPermissions`, `mailboxStatusOf`, `connectionBody`; the
    tests' `{"calendarSync": true}` become `{"outlookConnect": true}`.
    `outlookConnectBody` answers only `off`/`unavailable`/`untested` until task 4;
    `permissionsUnexpected` answers `[]`.
  - [ ] Stubs for the five new operations so everything builds: connect 409
    `switch_off`, callback 302 `failed`, complete 410, GET not offered, DELETE 204.
  - [ ] The Entra frontend's types follow the rename so `frontend:typecheck` passes
    (`calendarSync` → `outlookConnect` in reads only; the form's UI change is task 5).
  - [ ] `go build ./... && go vet ./...`, the full `mise run server:test`.
  - [ ] Commit (no trailer; the docs land in task 6):
    `feat(entra): the Outlook connection's foundation — calendar_sync renamed outlook_connect with when it was turned on and the last connect's failure (00088); entra.outlook_connections, one live row per person, revision-guarded (00089); the queries; outlookConnect in place of calendarSync, permissionsUnexpected and the five /entra/outlook operations in the contract; the stubs`.

- [ ] **3. The flow (`internal/entra`), after 2.** Readings 7, 11–16, 19–21.
  - [ ] The fake first (reading 21) with `graphtest/authorize_test.go`.
  - [ ] Failing tests in `outlook_connect_test.go` against the harness and a
    `fakeWorkforce` (the calendar's `fakes_test.go:245` shape): connect — 422 unknown
    feature, 409 switch off, 409 no account (none, another provider, disabled link,
    untrusted adoption, deleted account), 409 taken; the URL's every parameter, the
    cookie's attributes and path, `login_hint` and `domain_hint`. Callback — no cookie,
    an expired one, a wrong state (all `expired`, **nothing written**: the connection's
    row and the table unchanged), `access_denied`, another `error`, no code, a code
    reused or expired, a PKCE verifier wrong, `invalid_client`, a token for another
    tenant (`tenant_mismatch`), a bad nonce, a bad signature, `oid` not a GUID, a missing
    scope (`consent_missing` recorded), an oversized cookie; success sealing the
    `connected` cookie and clearing the state. Complete — no cookie, another session,
    another user (410), switch turned off meanwhile, `oid` not the person's account
    (`account_mismatch`, both UPNs in the detail), taken by another user, a `closing` row
    taken over, the caller's old live row of another account closed (`repointed`), two
    concurrent completes (one `account_taken`), the last error cleared, the event, the
    answer's `returnTo`. GET in each state; DELETE to `closing` keeping the token, and
    204 again. `outlook_internal_test.go`: `safeReturnTo`'s table, the scope
    normalising, the cookie size guard. `access_test.go`: the three `session`
    operations 401 without a session and reached by a caller holding nothing; the
    callback anonymous; the limits' buckets.
  - [ ] Implement until green; `go vet`, the package's tests.
  - [ ] Commit:
    `feat(entra): the Outlook connection's flow — connect sealing state, nonce and PKCE with the person's certain account read from Staff; the anonymous callback redeeming through the guarded client, the id_token verified with go-oidc, the tenant and the scopes checked, consent_missing recorded only after a valid state; complete under the session's MAC, account_mismatch and account_taken, a closing row taken over; the person's GET and DELETE; returnTo held to a path; graphtest's authorization server`.

- [ ] **4. The token, the status and closing (`internal/entra`), after 3.** Readings 9,
  10, 17, 18.
  - [ ] Failing tests: `outlook_token_internal_test.go` — every M5 code to its
    `error_code` (a table over `outlookRefusalCode`), transient failures leaving the row
    `active`, `invalid_client` marking the credential; `outlook_token_test.go` — a
    refresh storing the new token and moving the revision, the race (two refreshes of
    one token, both access tokens valid, the winner's refresh token kept, neither
    marking reconnect), a failure on an old revision after a `complete` changing
    nothing, the cache keyed by revision, `removal` allowed on `closing` and refused
    without a token, CAE through `GET /me` (`ChallengeNext`) moving the row to
    `reconnect` with `sign_in_again` and not retrying, the application path's 401 retry
    unchanged (the existing tests). `connection_test.go` — `outlookConnect`'s five
    statuses and counts, `outlook_connect_at` set on turning on, the switch off closing
    every row with an event each, `permissionsUnexpected` from a token with extra roles
    and empty from the eight (and with `Calendars.ReadWrite`), the test's
    `unexpected:` codes. `lifecycle_test.go` — Deactivate closing and nulling the token
    even when a later step fails, Delete and `markDeleted` closing. `outlook_sweep_test.go`
    — a closing row kept within 24 h, kept while a subscription row exists, deleted
    after 24 h unused, deleted after seven days regardless, live rows never; the events.
  - [ ] Implement: `tokenSource` in `graph.go` with no behaviour change for the
    application path; `outlook_token.go`; `outlook_close.go`; `outlook_sweep.go` and
    `Module.Workers`; the status and M19 in `connection.go`; `module.go`'s package doc.
  - [ ] `go vet`, the full `mise run server:test`.
  - [ ] Commit:
    `feat(entra): the delegated token and the connection's life — the refresh revision-guarded with the race kept harmless, Microsoft's refusals mapped to reconnect with their codes, a CAE claims challenge read as sign_in_again, cp1 declared; outlookConnect's status and counts; closing on disconnect, on a leaver's disable (the token forgotten first), on a deleted account and on the switch turned off; entra-outlook-sweep deleting what nothing needs; permissionsUnexpected warned of, never judged`.

- [ ] **5. The frontend, after 2 (may run beside 3–4 against the contract).** Readings
  22–24.
  - [ ] Failing tests first (reading 22's, 23's and 24's lists), then the code.
  - [ ] `mise run frontend:check`.
  - [ ] Commits:
    `feat(entra-ui): the Outlook connection — the switch renamed with its status, counts and redirect URI, a warning before it closes everyone's connection, permissions Vantigo does not use on the Status card; the Connections card and the connect button, complete run once and returnTo held to a path; both languages`
    and
    `feat(host-ui, calendar-ui): Settings → Connections from the Entra package, shown while the module is on; the calendar's Outlook card opens the connectionButton slot, which the host fills with the Entra button; both languages`.

- [ ] **6. Documentation, after 3–5.** English first, then Norwegian bokmål page for page.
  - [ ] `reference/entra.md` (en): the schema (00088's rename and columns, 00089's table
    and indexes, the frontmatter's `sources` gaining both migrations); *The Outlook
    connection* — the switch and `outlookConnect`'s status table, the redirect URI, the
    flow step by step with its cookies, purposes and codes, the states and the
    revision guard, the refresh and its failure table, CAE and `cp1`, the certain
    account read through `Workforce.DirectoryAccount`, closing (disconnect, leaver,
    deleted, switch off) and `entra-outlook-sweep`; the endpoints table's five
    operations with access, limits and answers; the events; the sealed purposes (two
    become five) and the MAC purpose; `permissionsUnexpected` under the connection test;
    every *calendar sync switch* renamed.
  - [ ] `contributing/module-boundaries.md` (en): rule 7 — the host's *Connections* page
    composed from `@vantigo/entra-ui`, and the calendar's `connectionButton` slot
    (`connection.button`) filled by the host; the Entra paragraph — it now reads Staff's
    `Workforce.DirectoryAccount` under rule 12 through its accessor, never inside a
    transaction, and has one worker; still two fixed hosts.
  - [ ] `admin/entra.md` (en, nb): *Outlook connection* — preconditions (the connection
    connected), the steps (the Web platform and the redirect URI copied from the Entra
    page, the delegated `openid`, `profile`, `offline_access`, `Calendars.ReadWrite`,
    **Grant admin consent**, the switch), verifying by connecting yourself
    (*untested* → *ready*), turning it off (everyone connects again), what can go wrong
    (AADSTS50011 on Microsoft's page, *Approval required* or a consent prompt without
    admin consent, `consent_missing`, Conditional Access and MFA turning people to
    *Reconnect*, `account_mismatch`); step 1's "leave Redirect URI empty" sentence; the
    Status card's unexpected-permissions warning; the switch's new name where *Calendar
    sync* names it; the description frontmatter.
  - [ ] `admin/installation.md` (en, nb): the redirect URI is on `APP_URL` (with the base
    path), not `PUBLIC_BASE_URL`; `entra-outlook-sweep` in the workers' list.
  - [ ] `user/connections.md` (en, nb; new, `sidebar.order: 2`, `sources`:
    `apps/host/frontend/src/routes/settings/connections.tsx` and
    `apps/entra/frontend/src/components/outlook-connection.tsx`): who sees it, connecting,
    what the redirect looks like, *Connected as*, disconnecting, *Reconnect needed* and
    each reason, each failure's sentence and what to do.
  - [ ] `user/index.md` (en, nb): *Your account* names **Connections**.
  - [ ] `user/entra.md` (en, nb): *Calendar sync* becomes *Outlook connection* for the
    switch, its status lines, the counts, the redirect URI and the warning on turning it
    off; the notifications line under it unchanged; the Status card's warning.
  - [ ] `user/calendar.md` (en, nb): the Outlook card shows the person's Outlook
    connection with **Connect Outlook** or **Reconnect Outlook**. *Plan decision:* the
    spec puts the calendar guide in B, but A changes `apps/calendar/frontend`, which that
    page's `sources` cover; B rewrites the section when the sync uses the connection.
  - [ ] `ROADMAP.md`: the Outlook connection, delivery A.
  - [ ] The spec's *as built* note for A, listing this plan's departures.
  - [ ] `mise run docs:check`.
  - [ ] Commit:
    `docs(entra, calendar): the Outlook connection — the flow, the states, the refresh and its reconnects, closing and the sweep, the endpoints, the events and permissionsUnexpected in the reference; the host's page and the calendar's slot in module boundaries; the Entra administration and installation pages, the new Connections guide, the overview, the Entra and calendar guides in both languages; the roadmap and the spec's as-built note`.

- [ ] **7. Review, then verification.** A security review (read-only) of the flow (state,
  nonce, PKCE, the cookies and their purposes, the MAC, `returnTo`, the callback writing
  nothing without a valid state, the id_token checks, logging), the token store and the
  leaver's path; a docs pass against the code; one fix batch (`fix(entra): the review's
  findings — …`, `docs(entra): the review's fixes — …`). Then `mise run server:check`,
  `mise run server:test`, `mise run frontend:check`, `mise run docs:check` (after
  `bun install --frozen-lockfile` in a fresh worktree); renumber the migrations if #194
  landed first; push; the pull request against `main` with the real-tenant checklist:
  the redirect URI and admin consent added, a person connected silently with SSO, a
  person signing in as another account refused, Conditional Access forcing a
  *Reconnect*, a leaver's row closed, the switch off closing everyone — and a note that
  A must not be released without B.
