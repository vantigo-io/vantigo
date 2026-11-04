# Google Workspace, delivery C — connecting existing users: implementation plan

> **For agentic workers:** implement this plan task by task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** on an installation whose single sign-on is Google, an Owner on **Workspace
admin › Users** opens **Find matches** and sees Vantigo users paired with exactly one
Google account each, by address, alias or employee number, and confirms them; or,
connecting one user, types a name and picks the Google account from the directory. Each
becomes an announced link keyed by the account's Google id, and the person's first Google
sign-in lands on their own user. On the Google Workspace page's **Accounts** tab, the
*Sign-in* step opens identity's single sign-on card prefilled with Google, the web
client's id and the organisation's verified domains as the allowed domains.

**Architecture:** `srv/google/directory_search.go` fills `contracts.DirectorySearch`
under `Key() = "google"`, registered as `module.Module.DirectorySearch`, so Compose
collects it beside Entra's and identity's two Owner endpoints
(`/identity/owner/sso/directory/{users,matches}`, delivery 0) ask it while Google is the
configured OIDC provider. Both reads take their footing on the pool (the connection's
row: the admin to impersonate and the verified domains), then call the Directory with
`keyring.tokenSource([admin.directory.user], admin_subject)` on no transaction. What
they need of identity — the users not yet connected and who holds an account — comes
through `contracts.SSOConnections` with the request's context, as Entra's does. Nothing
is stored; nothing new is served by the module. The host's `SsoDefaults` gains
`allowedDomains`; `@vantigo/google-ui`'s Accounts tab takes the host's `signIn` and
`provisioning` renderers, as Entra's does.

**Spec:** [`docs/superpowers/specs/2026-11-03-google-workspace-design.md`](../specs/2026-11-03-google-workspace-design.md), G23–G24 (on G7, G8) and P1–P5.
**Brief:** [`2026-11-03-google-workspace-briefs/c-link-users.md`](2026-11-03-google-workspace-briefs/c-link-users.md).

## Plan readings

Checked against `280cd94a` (delivery B's branch, `feat/google-accounts`).

1. **No migration.** C stores nothing: the announced links are identity's
   (`identity.expected_links`, 00064). Were one needed it would be `00114`.
2. **The contract** (`srv/contracts/directory_search.go`) and identity's endpoints
   (`srv/identity/sso_directory.go`) are delivery 0's: identity strips `"` and `\`,
   refuses a search under two characters or over 100, asks `Users(search, 25)`, answers
   409 `no_directory` while OIDC is off or no provider's `Key()` is the configured one,
   and 503 `provider_unreachable` for an error wrapping `contracts.ErrDirectoryUnavailable`.
   Identity's settings already accept `google` (`sso_settings_owner.go:561`) and need at
   least one allowed domain for it (`:606`).
3. **Entra's implementation** (`srv/entra/directory.go`) is the template: the matching
   rule (`matchCandidates`), the holders read once for the proposals, an account held for
   another user left out, the read bounded by two minutes, one read in flight per Owner
   (`singleflight`), the users read `Unconnected(1001)` and `truncated` past 1000.
4. **The keys are Google's.** Identity keys a Google sign-in by `sub` (G7,
   `oidc_claims.go:278`); `KeyHolders` and the announced link take the 21-digit id as it
   is (`normalizeExternalID` parses only a 32- or 36-character string), and the
   principal name as an address. So `ObjectID` is the Directory id and `PrincipalName`
   the primary email, as G24 says.
5. **Google's addresses are unique within the organisation**: a primary email or an
   alias belongs to one user or group. Employee numbers are not: two accounts may carry
   the same organization external id. Only the number can be ambiguous between accounts.
6. **The users' read** is `users.list(customer=my_customer, projection=basic,
   maxResults=500)` paged by `pageToken`; `basic` answers `primaryEmail`, `aliases`,
   `nonEditableAliases`, `emails[]`, `externalIds`, `name.fullName` and `suspended`.
   `emails[]` holds addresses an administrator typed for the person (`type: home`, an
   outside address, or a work address on any domain), which are not the account's own.
7. **Identity's Google claim check** (`checkGoogleClaims`, `oidc_claims.go:198`)
   requires `hd` equal to the email's domain and listed among the allowed domains. The
   brief forbids changing it; U4 decides whether a user on a secondary domain passes it.
8. **The SSO card** (`host/routes/workspace/-sso-settings.tsx`): `draftWithDefaults`
   applies `SsoDefaults` only to a form with OIDC off and no authority or client id
   saved; Google's domains are a text area, one per line.
9. **The Accounts tab** (`apps/google/frontend/src/pages/accounts-tab.tsx`) has no
   sign-in or provisioning step yet; the host's route renders it bare. Entra's tab takes
   `signIn(defaults)` and `provisioning()` from the host
   (`host/routes/workspace/entra/accounts.tsx`).

## Decisions taken in planning

- **The search is two queries, always.** `users.list` with
  `query=name:'<s>*'` and with `query=email:'<s>*'`, 20 answers each, merged by id,
  ordered by full name then id, cut to the limit (at most 20). Google documents no `OR`
  between clauses; a query Google did not refuse but read differently would answer
  wrongly without a sign, so the design's fallback is taken as the only path. The search
  loses `'`, `"`, `\` and `*` before it is quoted, and nothing is asked when nothing is
  left. Each account answers `ObjectID` the id, `PrincipalName` and `Mail` the primary
  email, `DisplayName` the full name (the primary email when Google has none),
  `EmployeeNumber` the first organization external id, `Enabled` not suspended, `Guest`
  false, and its holder from `KeyHolders`. An id that is not digits is left out.
- **An account's addresses** for the matches are its primary email, its `aliases` and
  `nonEditableAliases` — never an `emails[]` entry, which an administrator types, on any
  domain (changed in review: a typed work address on a verified domain matched too).
- **The matching rule is Entra's, exact only.** A candidate's address equal to an
  account's primary email matches `mail` and `principalName` (the primary email is both
  on Google); equal to an alias, `mail`; their employee number equal to the account's
  organization external id (trimmed, ignoring case), `employeeNumber`. A candidate is
  proposed when their keys hit exactly one account that no other candidate's key hit,
  and nobody else holds it; two accounts, or one account two candidates hit, is
  ambiguous and counted. An address identity made up (`Email ""`) matches nothing.
- **The bounds.** Candidates: `Unconnected(1001)`, `truncated` past 1000, as Entra. The
  directory: pages of 500, at most 20 (10,000 accounts); a page token left after that
  sets `truncated`, and **the employee number is then not used**: an address is unique
  in Google, so an address match stays exact over a partial read, while a number match
  could hide a second account in the pages not read. The whole read is bounded by two
  minutes; one read in flight per Owner.
- **The footing.** The connection must name an admin and hold a live key; otherwise, or
  when Google refuses the token (key, subject, scope not delegated), times out,
  throttles or answers 5xx, the call fails `contracts.ErrDirectoryUnavailable` (identity
  answers 503 `provider_unreachable`). No status gate beyond that: a test that has not
  run yet does not stop a search that works.
- **No address leaves.** A search, an address or a filter never reaches a log line (the
  client logs host and path only), an event or an error. The module writes no event for
  a read, as Entra writes none.
- **The prefill (G24).** `SsoDefaults.allowedDomains?: string[]`, applied with the rest
  and only to an empty form. The Accounts tab gains two steps, Owner-only as Entra's:
  **Sign-in** renders `signIn({provider: "google", authority: "https://accounts.google.com",
  clientId: oauthClientId, allowedDomains: domains})`, or says to enter the web client on
  the Connection tab when there is none; **Provisioning** renders the host's SCIM card
  and the link to Staff's default account kind, with a sentence that Google does not
  push users to Vantigo, so the token is for a SCIM tool of the Owner's own and people
  otherwise arrive at their first sign-in. Both steps are hidden while the connection has
  no customer (no test has read the organisation).
- **U1 is not settled (P5).** The links are keyed by the Directory id, compared with the
  ID token's `sub`. Should they differ, the link is **not** fulfilled by its other key:
  a principal name announced under an object id the token does not carry is no match,
  for every provider, since the address may have been given to another account since
  the announcement (changed in review: the fallback by address alone let a reassigned
  address claim the link). If U1 turns out false, Google links must be announced
  without the object id — the primary email alone, as the principal name — until the
  key is mended. The pull request says the check in the test tenant comes before the
  merge; `R/google.md` records it as unverified.

## Tasks

- [ ] **1. Plan** — this file, `docs(google): the plan for connecting existing users (Google C)`.
- [ ] **2. The directory search** — `feat(google): directory search and exact matches for connecting existing users (Google C)`.
  `directory_search.go` (`Users`, `Matches`, the rule, the bounds, the holders),
  `Module.DirectorySearch`; `googletest`: `users.list` with `name:`/`email:` prefix
  queries, `projection=basic`, paging by `pageToken` and `maxResults`, aliases,
  `nonEditableAliases` and `emails[]`, alongside B's `externalId:`. Tests through
  identity's endpoints: the two queries and their merge, a holder shown, the search's
  stripping, the email prefix ignoring case, the matches by primary email, alias and
  number, an `emails[]` address (outside or on a verified domain) matching nothing, ambiguity both ways, an account held by another left out,
  candidates truncated, the directory truncated with the number unused, no key and a
  refused token answering 503, no address in the log; a pick announced keeps the
  Google id and the primary email.
- [ ] **3. A Google sign-in fulfils a pick** — `test(identity): a Google sign-in lands on the user announced from the directory (Google C)`.
  Identity's harness with the Google OIDC fake and a `FakeDirectorySearch` keyed
  `google`: search, announce the pick, sign in with `sub` the picked id, land on the
  user; and a `sub` that differs not landing on the announced user, though the primary
  email is its principal name (U1 false; Entra's equivalent beside it).
- [ ] **4. The SSO prefill** — `feat(host): the single sign-on card takes allowed domains as a default (Google C)`.
  `SsoDefaults.allowedDomains`; tests: applied to an empty form only, never over a saved
  value.
- [ ] **5. The Sign-in and Provisioning steps** — `feat(google-ui): the Sign-in and Provisioning steps on the Accounts tab (Google C)`.
  `GoogleAccountsTab({signIn, provisioning})`, both languages; the host's route hands
  `SsoSettingsCard` in; tests: the defaults handed, Owner only, hidden without a
  customer, no client id.
- [ ] **6. Documentation** — `docs(…): …`: `admin/sso-scim.md` and
  `admin/authentication.md` (Google as the provider, the domains from the connection,
  `sub` as the key, a user on a secondary domain), `user/google-workspace.md` (*Sign-in*,
  *Provisioning*, connecting existing users and the matches),
  `user/workspace-administration.md` (the picker answers from Google), each in `en` and
  `nb`; `reference/google.md` (`DirectorySearch`: the queries, the rule, the bounds; U1
  and U4 among the unverified items).
- [ ] **7. Embedded docs** — `go generate ./...` in `apps/server`,
  `chore(assistant): regenerate the embedded documentation`.
- [ ] **8. Gates** — `mise run server:check`, `mise run server:test`,
  `mise run frontend:check` (its log read), `mise run docs:check`, serially.

No endpoint is added to `openapi/google.yaml` (identity serves the search); no client
secret crosses modules (the Owner types it into identity's card); identity's claim
checks are unchanged; no match is fuzzy.

## Unverified items this delivery depends on

- **U1** (the ID token's `sub` equals the Directory user id): not settled. A mismatch
  fulfils no link announced from a pick — the primary email does not stand in for the
  object id — so nothing is linked wrongly, but if U1 is false Google links must be
  announced without the object id. Settled in the test tenant before the merge (P5).
- **U4** (`hd` for a user on a secondary domain): every verified domain is prefilled as
  allowed. Identity's check also needs `hd` to equal the email's domain (reading 7): if
  Google answers the primary domain for a user whose address is on a secondary one, that
  user's sign-in is refused, never linked wrongly; the guide says so, and the check is
  identity's to revisit.
- **The search's prefix** (`name:'<s>*'` and `email:'<s>*'` as prefix matches, the
  `name` clause over the full name): Google documents the `*` suffix; a query that finds
  less shows fewer accounts, never a wrong one. Settled in the test tenant.
