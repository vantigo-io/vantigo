# Google Workspace, delivery D — the personal Google connection: implementation plan

> **For agentic workers:** implement this plan task by task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** an Owner turns on **Connection for people** on the Google Workspace page's
**Calendar and Gmail** tab. A person whose Staff record names a Google account Vantigo
manages opens **Settings › Connections**, clicks **Connect Google**, consents on Google's
page and comes back *Connected as x@…*. The connection holds a sealed refresh token and
the features consented to (Calendar now; Gmail when delivery G asks). A dead token shows
as **Reconnect Google** to that person alone; a disconnect, a leaver, a suspended or
deleted account or the switch off closes it; `google-sweep` cleans up. Nothing uses the
connection yet — E and G do.

**Architecture:** Entra's Outlook connection (`srv/entra/outlook*.go`, design M3–M8) is
the template, reshaped for Google: `srv/google/personal.go` (the paths, the cookies and
their purposes, the features, `returnTo`, the certain account), `personal_connect.go`
(connect and the anonymous callback), `personal_redeem.go` (the code redeemed through the
guarded client with the web client's secret, the ID token verified with go-oidc against
`https://accounts.google.com`, discovery and JWKS through the same client),
`personal_complete.go` (complete, `GET`/`DELETE /google/connection/me`),
`personal_close.go` (leaver, deleted, suspended, the switch), `personal_token.go` (the
revision-guarded refresh and its cache, the token source E and G will call),
`personal_sweep.go` (`google-sweep`) and `personal_switch.go` (the switch and its status
on `GET /google/connection`). The rows are `google.connections` (00114). The host's
provider list gains Google's card and button from `@vantigo/google-ui`.

**Spec:** [`docs/superpowers/specs/2026-11-03-google-workspace-design.md`](../specs/2026-11-03-google-workspace-design.md), G25–G29 (on G9, G13, G14, G17, G19), P1–P5; the Outlook connection design's M3–M8.
**Brief:** [`2026-11-03-google-workspace-briefs/d-connection.md`](2026-11-03-google-workspace-briefs/d-connection.md).

## Plan readings

Checked against `3653c1f3` (delivery C's branch, `feat/google-link-users`).

1. **The migration is `00114`** (0: 00109–00111, A: 00112, B: 00113, C: none). sqlc's
   schema list in `srv/google/sqlc.yaml` gains it.
2. **A's web client** is `google.connection.oauth_client_id` and the newest unrevoked
   `oauth_client_secret` credential (`liveSecret`, `credentials.go`), sealed under
   `google/oauth-client-secret`. The redirect URI is already answered by
   `redirectURIs` (`connection.go:84`): `APP_URL` + base path +
   `/api/v1/google/connect/callback` (`connectCallbackPath`).
3. **B's accounts** are keyed by the Directory id; `trusted(row)` is created by Vantigo
   or adoption confirmed; `state` is `enabled`, `disabled` (suspended) or `deleted`, as
   the module last wrote it. `Account` reads Google and never writes the row, so a user
   suspended outside Vantigo is not seen in `google.accounts`: the sweep's `users.get`
   finds them (G28).
4. **Staff's `Workforce.DirectoryAccount`** answers `Provider`, `ExternalID` (the
   Directory id for Google, G7) and `Enabled` for the principal in the context. Google
   has no accessor file; the module calls `deps.Workforce` directly, never inside a
   transaction (rule 10).
5. **C's fix** (`0f67963e`): identity refuses an announced link whose object id
   contradicts the token's. D compares the ID token's `sub` with the account's id itself
   (G27); it does not go through identity.
6. **go-oidc v3.21** keeps a `RemoteKeySet` on `context.WithoutCancel` of the context it
   was made with, so a provider discovered on a request's context keeps working after
   it. Its verifier accepts `accounts.google.com` as well as
   `https://accounts.google.com` for a provider at the latter. Discovery
   (`accounts.google.com/.well-known/openid-configuration`) and the JWKS
   (`www.googleapis.com/oauth2/v3/certs`) are both on the client's fixed host list.
7. **Entra's sweep holds one pooled connection** (the advisory lease) across its Graph
   calls; the 00087 lesson forbids it for Google (G28): every step claims rows in one
   statement and calls Google holding nothing.
8. **The leaver** is B's `Deactivate` (`lifecycle_deactivate.go`): its first transaction
   records the suspension and clears the password; the row's close joins that
   transaction. `Delete` and `markDeleted` close it too.
9. **The host's list** (`host/lib/connections.tsx`) takes `{module, Card, Button}`; the
   card props are already neutral (`outcome`, `onReturn`, `onOffered`), the search
   already validates `connection=<module>&outcome=…&code=…`.
10. **The tab** is `GoogleCalendarGmailTab` (`apps/google/frontend/src/pages/coming-tabs.tsx`),
    an empty state since A.

## Decisions taken in planning

- **The table and the switch** are G25's, exactly. `scopes` holds Google's answered
  `scope`, sorted and compacted, as Google spells them (full URLs, and `openid`).
- **Features → scopes** (G26): `calendar` → `…/auth/calendar.events` and
  `…/auth/calendar.app.created`; `mail` → `…/auth/gmail.send`; base
  `openid email profile`. Only `calendar` is offered by a caller in D; the map and the
  contract's enum hold both, as Entra's do.
- **The authorize URL**: `accounts.google.com/o/oauth2/v2/auth` with `response_type=code`,
  `client_id`, `redirect_uri`, `scope` (base, the feature's scopes and the scopes of
  every feature the person's live row already holds), `state`, `nonce`, PKCE S256,
  `access_type=offline`, `prompt=consent`, `include_granted_scopes=true`, `login_hint`
  (the account's primary email) and `hd` (its domain, a hint only).
- **The cookies** follow Entra's: `vantigo.google.connect` (purpose
  `google/connect-state`, ten minutes) and `vantigo.google.connected` (purpose
  `google/connected`, five minutes), HttpOnly, SameSite=Lax. G26 names
  `POST /google/connect`, `GET /google/connect/callback` and `POST /google/complete`, so
  the cookie path is `/api/v1/google`, the narrowest covering the three. The session MAC
  is under `google/connect-session`.
- **The callback's order**: the state cookie and `state` (constant time) before anything;
  then Google's `error` (`access_denied`, else `failed`); the switch and the web client;
  the redemption; the ID token (signature, `aud`, expiry by go-oidc, issuer, nonce in
  constant time); `hd` among `domains` (`domain_mismatch`, a consumer account carrying
  no `hd` included); the asked feature among the answered scopes (`consent_missing`,
  recorded as `personal_last_error`); then the connected cookie. Codes: `access_denied`,
  `domain_mismatch`, `expired` (no cookie, a stale state, a code Google calls expired),
  `consent_missing`, `failed`. Every answer is a 302 to
  `/settings/connections?connection=google&outcome=complete|failed[&code=…]` and clears
  the state cookie; nothing is written without a valid state.
- **U9 is not settled (P5): the defensive path.** The asked feature must be in the
  answered `scope`, else `consent_missing` and nothing stored. What else the answer holds
  is stored as answered: if incremental consent does not answer the union, a feature
  held before and missing now is dropped from the row, never assumed — the person sees
  it gone and connects it again. A refresh answering a narrower `scope` is stored the
  same way; a refresh whose `scope` keeps no feature at all moves the row to
  `reconnect`, `consent_missing`.
- **U1 is not settled (P5): the defensive path.** Complete compares the token's `sub`
  with the account's Directory id; a mismatch is 409 `account_mismatch` and nothing is
  stored. If U1 turns out false, nobody can connect until the key is mended — visible,
  never wrong.
- **The certain account** (G27): `DirectoryAccount` with `Provider = "google"`,
  enabled, naming a trusted `google.accounts` row in state `enabled`. At connect (409
  `no_account`), at complete again under the account row's share lock in the storing
  transaction (as Entra), and on use (the token source): a mismatch on use closes the row
  with its token forgotten (`disabled` or `deleted`).
- **Refresh failures** (G28): `invalid_grant` → `reconnect`, `expired`;
  `admin_policy_enforced`, `org_internal` → `reconnect`, `blocked`; `invalid_scope` →
  `reconnect`, `consent_missing`; `invalid_client`, `unauthorized_client` → the
  connection's `personal_last_error`, the row untouched, the call failing; a 5xx, a 429,
  `temporarily_unavailable` or no answer → transient; any other refusal → `reconnect`,
  `sign_in_again`. Every write is guarded by `revision` and bumps it; a lost race keeps
  the winner's token. Google answers no new refresh token on most refreshes; the stored
  one stays.
- **The access token cache** is per `(google_user_id, revision, client id, digest of the
  sealed refresh token)`, dropped five minutes before expiry, as Entra's.
- **Revocation** (`POST oauth2.googleapis.com/revoke`, form `token=`): when the sweep
  deletes a closing row that still holds a token. The row is deleted first (claimed by
  the `DELETE … RETURNING`), then the token revoked holding no connection; a failed
  revoke is logged by count and recorded on the event (`revoke_failed`), not retried —
  Google lets an unused refresh token lapse on its own.
- **The sweep** (`google-sweep`, hourly, in API and worker mode, no lease): (1) one
  `DELETE … RETURNING` of the spent closing rows — seven days after closing, or a day
  after their last use and closing (E adds "and holding no calendar channel" with its
  table) —, then each token revoked; (2) up to 100 `reconnect` rows not read for a day,
  claimed by stamping `account_checked_at` in one `UPDATE … FOR UPDATE SKIP LOCKED`,
  each read in Vantigo's rows first and then with `users.get` as the admin; suspended →
  closed `suspended`, 404 → `deleted`, the close guarded by the revision claimed.
- **The switch** is `PUT /google/connection/personal` `{enabled}` under
  `policy:Owner+OwnerManagement`, in the Owner bucket. Turning it off closes every live
  row, tokens kept for removals, one `google.personal.closed` event each (`switch_off`);
  turning it on stamps `personal_connection_at`. The page asks before turning it off
  while anyone is connected, naming the count. `GET /google/connection` gains
  `personalConnection {enabled, status, reason?, connected, reconnect, redirectUri}`:
  `off`; `unavailable` with `no_web_client` (no client id or no live secret),
  `not_tested` (no domains read yet) or `client_refused` (Google refused the web client
  on a redemption or a refresh); `consent_missing`; `ready`. A new web client clears
  `personal_last_error`.
- **`GET /google/connection/me`** (session, always 200): `{offered, reason?, state,
  features, account?, connectedAt?, errorCode?}`; `reason` `switch_off` (the switch off
  or the client not ready) or `no_account`. **`DELETE /google/connection/me`** →
  `closing`, 204 always.
- **Rate limits** as Entra's: connect and complete share `google-connect` (30 a minute),
  the callback `google-connect-callback` (60 a minute).
- **Events** (`google.events`, subject the Google id, codes only): `google.personal.switched`
  (`on`/`off`), `google.personal.connected` (`feature:<f>`), `google.personal.disconnected`,
  `google.personal.reconnect_needed` (the code), `google.personal.closed` (`leaver`,
  `deleted`, `disabled`, `suspended`, `switch_off`, `repointed`),
  `google.personal.deleted` (`revoke_failed` when it did).
- **The changes reader for E**: `connectionChanges(ctx, since)` over
  `ix_google_connections_changed`, answering `[]contracts.ConnectionChange` (mailbox =
  the Google id), at most `contracts.ConnectionChangesMax`. E's `Connections` calls it.
- **`googletest`**: `IssueCode` (the authorize step as a helper), the
  `authorization_code` grant (client and secret, redirect URI, PKCE, single use, a
  minute's life), the `refresh_token` grant (granular: the answer's `scope` what the test
  granted; `RefuseRefresh`), the ID token signed RS256 by a fake key and served as a
  JWKS at `www.googleapis.com/oauth2/v3/certs` with discovery at
  `accounts.google.com/.well-known/openid-configuration`, `hd` and the issuer settable,
  and `/revoke`.
- **The frontend**: `GoogleConnectionCard` and `GoogleConnectButton` in
  `@vantigo/google-ui` (Entra's card's three states, Google's reason sentences, the
  failures' sentences), joined to `connectionProviders`; the **Calendar and Gmail** tab
  shows the switch (Owner only, confirm before off), its status and reason, the counts
  and the redirect URI to copy. Both languages.

## Tasks

- [ ] **1. Plan** — this file, `docs(google): the plan for the personal connection (Google D)`.
- [ ] **2. The table and the contract** — `feat(google): the personal connection's table and contract (Google D)`.
  `00114_google_connections.sql`, `queries/connections.sql`, sqlc; `openapi/google.yaml`:
  the six operations, `GooglePersonalConnection*` schemas, `personalConnection` on the
  connection; `go generate`; the frontend schema.
- [ ] **3. The flow** — `feat(google): connect, the callback and complete for a person's own Google account (Google D)`.
  `personal*.go` but the sweep; `googletest/authorize.go`; tests: the authorize URL and
  the cookie, the callback's every code, nothing written without a state, the ID token's
  issuer, audience, nonce, signature and `hd`, granular consent, complete's session and
  person binding, `account_mismatch`, `account_taken`, a closing row taken over, a
  re-pointed account, `GET`/`DELETE /me`, no token or Google words in logs or events.
- [ ] **4. Tokens, closes and the sweep** — `feat(google): the personal connection's tokens, closes and google-sweep (Google D)`.
  The refresh and its failure mapping, the cache, the check on use, the leaver and the
  delete, the switch, `google-sweep` (revoke, the account check, a pool of one), the
  changes reader.
- [ ] **5. The card, the button and the tab** — `feat(google-ui): the Google connection card, button and the Calendar and Gmail tab (Google D)`.
- [ ] **6. The host** — `feat(host): Google joins the connections list (Google D)`.
- [ ] **7. Documentation** — `docs(google): the personal Google connection (Google D)`:
  `user/connections.md`, `admin/google-workspace.md`, `user/google-workspace.md`,
  `admin/installation.md` in `en` and `nb`; `reference/google.md`.
- [ ] **8. Embedded docs** — `go generate ./...`, `chore(assistant): regenerate the embedded documentation`.
- [ ] **9. Gates** — `mise run server:check`, `mise run server:test`,
  `mise run frontend:check` (its log read), `mise run docs:check`,
  `bun run tools/docs/check-coverage.ts --base 3653c1f3 --head HEAD`, serially.

## Unverified items this delivery depends on

- **U1** (`sub` equals the Directory id): not settled; the mismatch refuses (above).
- **U9** (incremental consent answers the union): not settled; only the answered
  `scope` is stored and a missing feature is never assumed (above).
- **The refresh's `scope`**: Google is documented to answer `scope` on a refresh; when
  it answers none, the stored scopes stand.
- **`admin_policy_enforced` and `org_internal`** as the refresh refusals of a blocked
  app or a user outside the organisation: mapped to `blocked`; any other shape is
  `sign_in_again`, never a loop.

All four are checked in the test tenant before the merge (P5); `R/google.md` lists them.
