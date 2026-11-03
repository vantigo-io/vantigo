# Brief C — Connect existing users to their Google accounts

You are implementing delivery **C** of the design
`docs/superpowers/specs/2026-11-03-google-workspace-design.md` (decisions **G23–G24**,
on G7 and G8 from delivery 0). Read that file first, in full, then this brief. You work
alone, from a cold start, in this repository. Model: Claude Sonnet. Deliveries 0, A and
B are on `main`.

**Precondition: U1 is settled.** The owner has confirmed in the test tenant that an ID
token's `sub` equals the Directory user id. If the brief's starting message does not
say so, stop and report before you write code.

## The outcome

On an installation whose single sign-on is Google, an Owner on **Workspace admin ›
Users** opens **Find matches** and sees Vantigo users paired with exactly one Google
account each, by address, alias or employee number, and confirms them; or, connecting
one user, types a name and picks the Google account from the directory. Each becomes an
announced link keyed by the account's Google id, and the person's first Google sign-in
lands on their own user. On the Google Workspace page's **Accounts** tab, the *Sign-in*
step opens identity's SSO card prefilled with Google, the web client's id and the
organisation's verified domains as the allowed domains.

## Read before you plan

- `AGENTS.md`; the design's G7, G8, G23, G24; the Entra phase 2 design's matching rules
  (`docs/superpowers/specs/` — the Entra phase 2 design, delivery B).
- `apps/server/internal/contracts/{sso_connections.go,directory_search.go}` (the latter
  from delivery 0), identity's `directory` endpoints and `sso_connections.go`,
  `oidc_claims.go:264-284` (delivery 0's `sub`), `expected_links.go`.
- Entra's `DirectorySearch` implementation (moved in delivery 0) as the template for
  matching, ambiguity and truncation.
- `apps/host/frontend/src/components/directory/` (delivery 0), `routes/workspace/users.tsx`,
  `routes/workspace/-sso-settings.tsx` (`SsoDefaults`, `draftWithDefaults`),
  `routes/workspace/entra/accounts.tsx:12` (how Entra hands the card its defaults).
- Docs: `en/admin/sso-scim.md`, `en/admin/authentication.md`,
  `en/user/workspace-administration.md`, `en/user/entra.md` (*Connect existing users*).

## Build

1. **`DirectorySearch` for Google (G23)**: `Users` with the prefix queries (two queries
   merged by id if Google refuses the `OR`), 20 answers, holders from
   `SSOConnections.KeyHolders`; `Matches` reading `Unconnected(1001)` and proposing exact,
   unambiguous matches by primary email, alias or the organization external id, users
   paged at 500 and matched in memory, `truncated` past 10,000. Register it as the
   module's `DirectorySearch`. Calls as `admin_subject`.
2. **`googletest`**: `users.list` with `name:`/`email:` prefix queries and aliases.
3. **The SSO prefill (G24)**: `SsoDefaults.allowedDomains`; the Accounts tab's *Sign-in*
   step renders identity's `SsoSettingsCard` with `provider: "google"`, authority
   `https://accounts.google.com`, the connection's `oauth_client_id` and `domains`; the
   *Provisioning* step as Entra's. Hidden while the connection has no customer.
4. **Tests**: identity's directory endpoints through Google's provider with a fake
   tenant (matches, ambiguity, a holder shown, truncated); a Google sign-in matching an
   announced link made from a pick; the card's defaults applied only to an empty form.
5. **Docs**, then `go generate ./...` and the regenerated assistant docs.

## The documentation (same commit series)

- `en/` and `nb/`: `admin/sso-scim.md` and `admin/authentication.md` (Google as the
  provider, the domains from the connection, `sub` as the key, a user on a secondary
  domain), `user/google-workspace.md` (*Sign-in*, *Provisioning*, connecting existing
  users and the matches), `user/workspace-administration.md` (the picker answers from
  Google when Google is the provider).
- `reference/google.md` (`DirectorySearch`: the queries, the matching rules, the
  bounds).

## Do not

Add an endpoint to `openapi/google.yaml` for searching (identity serves it) · send a
client secret across modules · change identity's claim checks · propose a fuzzy match.

## How to work

- Worktree on `feat/google-link-users` cut from the previous delivery's branch (the README's stack);
  `bun install --frozen-lockfile` first.
- **Plan first**: `docs/superpowers/plans/2026-11-03-google-link-users.md`, committed
  alone as `docs(google): the plan for connecting existing users (Google C)`.
- No migration.
- Commits: `feat(google): … (Google C)`, `feat(google-ui): …`, `feat(host): …`,
  `docs(…): …`, `chore(assistant): regenerate the embedded documentation`. Signed.
- **No `Docs-Impact: none` trailer** on any commit.
- Gates: `mise run server:check`, `mise run server:test`, `mise run frontend:check`,
  `mise run docs:check`. No merge of `main`; the starting session pushes and opens the pull request.

## Report

Branch and `git log --oneline main..`; the gates' output; the plan's path; departures
from G23–G24; how U1 and U4 were settled; and the manual check: SSO set to Google from
the prefilled card, a match confirmed, the user signing in with Google and landing on
their own account.
