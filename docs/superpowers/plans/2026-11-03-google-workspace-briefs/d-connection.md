# Brief D — The personal Google connection

You are implementing delivery **D** of the design
`docs/superpowers/specs/2026-11-03-google-workspace-design.md` (decisions **G25–G29**).
Read that file first, in full, then this brief, then the Outlook design's M3–M8 it
mirrors. You work alone, from a cold start, in this repository. Model: Claude Opus.
Deliveries 0, A, B and C are on `main`.

**Preconditions: U1 and U9 are settled** (the starting message says how). The code
follows the design's defensive path either way.

## The outcome

An Owner turns on **Connection for people** on the Google Workspace page's **Calendar and
Gmail** tab. A person whose Staff record names a Google account Vantigo manages opens
**Settings › Connections**, clicks **Connect Google**, consents on Google's page, and
comes back *Connected as x@…*. The connection holds a sealed refresh token and the
features consented to (Calendar now; Gmail when G asks). A dead token shows as
**Reconnect Google** to that person alone; a disconnect, a leaver, a suspended account or
the switch off closes it; the sweep cleans up. Nothing uses the connection yet — E and G
do.

## Read before you plan

- The Outlook connection design, M3–M8 in full
  (`docs/superpowers/specs/2026-10-29-outlook-connection-and-mail-design.md`), with
  delivery A's *as built* notes.
- Entra's connection as the template: `entra/{outlook.go,outlook_connect.go,outlook_redeem.go,outlook_complete.go,outlook_close.go,outlook_token.go,outlook_sweep.go}`,
  their tests, `mig/00088`, `00089`, `openapi/entra.yaml`'s `/outlook/*` operations,
  `apps/entra/frontend/src/components/outlook-connection.tsx`.
- `internal/contracts/{workforce.go,mailbox_calendar.go}` (`ConnectionNeededError`,
  `ConnectionChange`), `internal/secrets/secrets.go` (`Seal`, `MAC`),
  `identity`'s OIDC relying party for how go-oidc is used with the guarded client.
- Host: `lib/connections.tsx` and `routes/settings/connections.tsx` (delivery 0).
- Delivery A's client and token source, B's `google.accounts`.
- Docs: `en/user/connections.md`, `en/admin/entra.md` (*Outlook connection*),
  `en/admin/installation.md`.

## Build

1. **Migration `00114_google_connections.sql`** as G25.
2. **The flow (G26)**: `POST /google/connect`, `GET /google/connect/callback`
   (`anonymous`, on `APP_URL`), `POST /google/complete`, with the sealed cookies under
   `google/connect-state`, `google/connected` and the session MAC under
   `google/connect-session`; PKCE; `access_type=offline`, `prompt=consent`,
   `include_granted_scopes=true`, `login_hint`, `hd`; the code redeemed through the
   guarded client with the web client's secret (A); the ID token verified with go-oidc
   (discovery and JWKS through the guarded client) — iss, aud, nonce, `hd` among
   `domains`; the stored scopes from the token response; the callback's codes
   (`access_denied`, `domain_mismatch`, `expired`, `consent_missing`, `failed`);
   `returnTo` checked as M4 says; the redirect to
   `/settings/connections?connection=google&outcome=…`. Features map `calendar` →
   `calendar.events` + `calendar.app.created`, `mail` → `gmail.send`; only `calendar`
   is requested by a caller in D, but the map holds both.
3. **The certain account (G27)** at connect, at complete and on use.
4. **Tokens and failures (G28)**: the revision-guarded refresh, the in-memory cache per
   `(google_user_id, revision)`, the failure mapping, `invalid_client` as the web
   client's (`personal_last_error`), a closed row's token revoked at
   `oauth2.googleapis.com/revoke` when the row is deleted.
5. **Disconnect, leavers, the switch**: `DELETE /google/connection/me` → `closing`;
   B's `Deactivate` closes the person's row with its token nulled (inside its
   transaction); the switch off closes every row; `PUT /google/connection/personal`
   under the Owner policy, asking before it turns off while anyone is connected.
6. **`google-sweep`** (hourly; claims, no pooled connection held across a call; at most
   100 reconnect rows a cycle read with `users.get` once a day).
7. **`GET /google/connection/me`** (always 200, `offered`, `reason`) and a
   `Connections`-ready reader of `changed_at` (E's contract call uses it).
8. **The frontend (G29)**: `GoogleConnectionCard` and `GoogleConnectButton` in
   `@vantigo/google-ui`, joined to the host's list and composite button; the *Calendar
   and Gmail* tab's switch, status, counts and the redirect URI to copy.
9. **`googletest`**: the authorize step (a test helper, not a browser), the token
   endpoint for authorization codes and refresh with granular grants, the ID token
   signed by a fake JWKS, revoke.
10. **Docs**, then `go generate ./...` and the regenerated assistant docs.

## The documentation (same commit series)

- `en/` and `nb/`: `user/connections.md` (connecting Google, Connected as, the features,
  disconnecting, Reconnect needed and its reasons, each failure's message),
  `admin/google-workspace.md` (*Connection for people*: the web client as Internal, the
  redirect URI, the switch, verifying by connecting yourself, turning it off, what
  happens to a person's connection, `google-sweep`, what can go wrong),
  `user/google-workspace.md` (the tab), `admin/installation.md` (the callback is on
  `APP_URL`).
- `reference/google.md` (the table, the flow, the states, the codes, the endpoints and
  their access and limits, the events, the sweep).

## Do not

Fill `MailboxCalendar` or `PersonalMailbox` (E, G) · request a Gmail scope from a
caller · delegate any calendar scope through DWD (Q3) · omit `prompt=consent` · store a
scope Google did not answer · keep a token in a log, an event or an error.

## How to work

- Worktree on `feat/google-connection` cut from the previous delivery's branch (the README's stack);
  `bun install --frozen-lockfile` first.
- **Plan first**: `docs/superpowers/plans/2026-11-03-google-connection.md`, committed
  alone as `docs(google): the plan for the personal connection (Google D)`.
- The migration takes the next free number (expected 00114).
- Commits: `feat(google): … (Google D)`, `feat(google-ui): … (Google D)`,
  `feat(host): …`, `docs(…): …`, `chore(assistant): regenerate the embedded
  documentation`. Signed.
- **No `Docs-Impact: none` trailer** on any commit.
- Gates: `mise run server:check`, `mise run server:test`, `mise run frontend:check`,
  `mise run docs:check`. No merge of `main`; the starting session pushes and opens the pull request.

## Report

Branch and `git log --oneline main..`; the gates' output; the plan's path; the migration
number; departures from G25–G29; U1 and U9 as the tenant answered; and the manual check:
switch on, connect, see *Connected as*, revoke the grant in the Google account's
security page and see *Reconnect needed* after the next refresh, disconnect.
