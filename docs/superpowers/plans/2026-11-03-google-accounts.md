# Google Workspace, delivery B — accounts, groups, licences and photos: implementation plan

> **For agentic workers:** implement this plan task by task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** HR gives a person the **Google Workspace** account kind in Staff. Staff's
accounts worker then prepares the person's Google account (suspended, found again by the
employee number), activates it on the start day with a generated password HR is shown
once (changed at first sign-in), keeps the profile and the manager in step, assigns the
licences of the person's groups, pushes the photo at 96 px, suspends the account on the
leave day with the leaver's steps, and deletes it when Staff's own *delete after N days*
says so. Staff's groups become Google groups with an address, a visibility and licences.
Staff drives the `google` provisioner through the slot exactly as it drives Entra.

**Architecture:** `srv/google/provisioner.go` fills `contracts.IdentityProvisioner` under
`Key() = "google"`, registered as `module.Module.IdentityProvisioner`, so Compose and
Workers collect it beside Entra's. Every call takes its footing on the pool (`open`): the
connection's row, the admin to impersonate, and token sources from A's
`keyring.tokenSource(scopes, subject)` — Directory users with `admin.directory.user`,
groups with `admin.directory.group`, group settings with `apps.groups.settings`,
licences with `apps.licensing`. Google is called on no transaction; the module's rows
(`google.accounts`, `google.groups`) and its events are written after, in short
transactions. The address rules move from Entra to a platform package,
`srv/directoryaddress`, which both modules call. The guarded client gains request bodies
(JSON, and the photo's), and a shown path that hides an address in a URL from the log.

**Spec:** [`docs/superpowers/specs/2026-11-03-google-workspace-design.md`](../specs/2026-11-03-google-workspace-design.md), G17–G22 and P1–P5.
**Brief:** [`2026-11-03-google-workspace-briefs/b-accounts.md`](2026-11-03-google-workspace-briefs/b-accounts.md).

## Plan readings

Checked against `fb13cc10` (delivery A's branch, `feat/google-foundation`).

1. **Migrations**: `main`'s latest is `00108`; delivery 0 took 00109–00111 and A 00112.
   This delivery's is `00113_google_accounts.sql`, added to `google/sqlc.yaml`.
2. **The token seam.** `keyring.token` answers `errNotConfigured`, `errNoCredential`
   (every key refused, the connection set failing), `errSubjectInvalid` (Google does not
   know the admin — the subject's problem, nothing marked) and `errNotDelegated` (the
   scope is not delegated). Each is the provider unable to act: the provisioner answers
   `contracts.ErrProviderUnavailable` wrapping it. `errGoogleUnreachable` and
   `errThrottled` are failures to retry.
3. **The client sends no body yet** (`client.go`'s `exchange` builds a request with a nil
   body) and logs `host + path`. B adds a body (JSON, or raw bytes with a content type)
   and, for the Licensing calls whose path carries the user's primary email, a shown path
   with the address replaced, so no address reaches a log line.
4. **`neededScopes()`** lists A's two scopes; B adds `admin.directory.user`,
   `admin.directory.group`, `apps.groups.settings` and `apps.licensing`, which the test
   then asks a token for, one each. A's tests that list the needed scopes follow.
5. **Staff's side** (unchanged): the worker reads `Account(externalID)` per link on its
   own cycle (`srv/staff/accounts_worker_provider.go:485`), which is what tells Staff an
   account was suspended or deleted outside Vantigo — so **B has no worker** (G22).
   `deleteExternalAfterDays` (`account_settings.go:27`, read at
   `accounts_worker_provider.go:82`) calls `Delete` for any provider (P1). A reported code
   must match `^[a-z_]{1,40}(:[A-Za-z0-9_.-]{1,64})?$` (`accounts_worker_provider.go:326`):
   a licence's code carries the SKU id (`licence_unavailable:1010020028`), never
   `productId/skuId`, whose slash the pattern refuses. A required trait is refused empty by
   Staff (`groups_traits.go:173`).
6. **Staff's specs**: `ExternalSpec.Fields` holds the switched-on directory fields by
   Entra's names; `ManagerExternalID` is the manager's Google user id;
   `Licences` is the union of the `licences` trait values of the person's groups under
   the provider (`accounts_spec.go:73-84`, `queries/groups.sql:292`).
7. **The attention items** `hireDateNotWritten` and `photoNotWritten` are worded with
   "Microsoft Entra ID" in the host's catalog (`host/catalogs/dashboard.ts:122-123,370-371`),
   and `accountUnconfirmedInDirectory` links to `/workspace/entra`
   (`host/lib/dashboard-helpers.ts:378`). A person's item carries no provider today
   (`staff/attention.go:133`), so the host cannot tell whose it is.
8. **Entra's pass delivery** (`pass_delivery`) is stored and not acted on: HR reveals the
   pass on the Account tab whatever it says (`docs/…/admin/entra.md` *Set the account
   rules*). Google stores it the same way.

## Decisions taken in planning

- **The address package** exports `Fold`, `CleanName`, `Address` with `Attempt`, `For`
  and `TemporaryPassword` over a set of `Rules` (the specials a local part may hold:
  `UPN` for Entra, `Mailbox` — `'._-` — for Google). Entra's choice of a default domain
  (the first verified one not under `.onmicrosoft.com`) stays in Entra; the package takes
  the domain resolved. No behaviour of Entra's changes.
- **Provider's status**: unconfigured while A's status is (no key or no admin);
  `pending_delegation` answers unconfigured with reason `not_delegated`; connected while
  every one of B's four scopes is among the last test's granted ones, else failing with
  `scopes_missing`; A's failing with its reason.
- **Account ids** are Google's numeric user ids (`^[0-9]{1,32}$`); anything else is no
  account (`Account` answers nil; a write answers the account gone).
- **Privileged**: a user with `isAdmin` or `isDelegatedAdmin` is never written to:
  Prepare's find answers `ErrExternalPrivileged` (reported `privileged`), so does every
  write.
- **The password.** Prepare's create sets a 32-character password nobody sees. Activate,
  when it lifts the suspension — or on HR's *Run now* (`Flags.Fresh`) for an account
  never signed in to (`lastLoginTime` at the epoch) — sets a 16-character password from
  an unambiguous alphabet with `changePasswordAtNextLogin`, sealed under `google/pass`
  for `pass_lifetime_days` (1–30, default 7). Never on an account someone has signed in
  to: a new password over a working account is a way into it.
- **Licences** are `productId/skuId`. The module assigns the wanted ones the user does not
  hold (`licenseAssignments.get` 404 → `insert`) and removes only those it assigned
  itself (`accounts.licences_assigned`) that are no longer wanted. Any refusal of an
  insert is `licence_unavailable:<skuId>`. The SKUs are read by
  `GET /google/connection/skus` with `listForProduct` for `Google-Apps`, `101001`,
  `101005`, `101033` and `101034`, a product refused counting as none held, and cached on
  `connection.skus` (at most 200).
- **Leaver** (G19), in order after the suspension, each on its switch: the licences
  removed (the module's own and every cached SKU the user holds), the groups left (`all`:
  every group `groups.list(userKey)` names; `managed`: the module's own), `users.signOut`.
  A refusal of the sign-out is reported `sign_out_refused` and the rest goes on (U19); any
  other step's failure is `leaver_step_failed:<step>`, the rest left to the next call.
- **Groups**: the `email` trait is not *required* in Staff's sense — empty means
  `<group key>@<address domain>` — so HR need not type it; it is immutable once the group
  exists. Visibility `domain` is `ALL_IN_DOMAIN_CAN_VIEW` for the group and its
  membership and `showInGroupDirectory: true`; `members` is `ALL_MEMBERS_CAN_VIEW` and
  `false`. Groups Settings is called on `www.googleapis.com/groups/v1/groups/{email}`,
  the documented host (U12). A group found by its address that Vantigo did not create is
  kept as an unconfirmed adoption (`ErrGroupUnconfirmed`) until a holder of
  `google:manage` confirms it; `DeleteGroup` deletes only the module's own, and forgets
  an adopted one. `google.groups` gains `adoption_confirmed_by_user_id` beside the
  design's columns, as `google.accounts` has.
- **`PUT /google/connection/settings`** is `permission:google:manage` under If-Match, the
  permission A's catalog already describes as setting the account rules (G16), as
  `entra:manage` sets Entra's. The other five operations are `google:manage`; the SKUs'
  read has its own rate limit (30 a minute).
- **The attention items name their provider**: `StaffStatsAttentionItem` gains optional
  `provider` and `providerName` on the items a provider link raises; the host words
  `hireDateNotWritten` and `photoNotWritten` with the name and opens
  `/workspace/<provider>` for `accountUnconfirmedInDirectory`. An item without one keeps
  today's wording and link.

## Tasks

- [ ] **1. Plan** — this file, `docs(google): the plan for accounts and groups (Google B)`.
- [ ] **2. The address package** — `refactor(entra): the address rules move to directoryaddress`.
  `srv/directoryaddress` with its test (moved from `entra/address_internal_test.go`);
  Entra's `address.go` calls it; depguard lets every module import it;
  `C/platform-packages.md` lists it.
- [ ] **3. The provisioner** — `feat(google): accounts, groups, licences and photos through the provisioner (Google B)`.
  `00113_google_accounts.sql` (G17's tables and columns, CHECKs as Entra's), queries,
  sqlc; `openapi/google.yaml`'s six operations and the connection's account rules;
  `client.go`'s bodies and shown path; `provisioner.go`, `lifecycle_*.go`, `licences.go`,
  `groups.go`, `groups_traits.go`, `photo.go`, `accounts.go` (the endpoints),
  `settings.go`, `skus.go`; B's scopes in `neededScopes`; `Module.IdentityProvisioner`.
  `googletest`: users (insert, get, patch, delete, list with the `externalId:` query and
  its client-side filter's edge cases, signOut, photos), groups, members, Groups
  Settings, licence assignments and `listForProduct`. Tests: every method's paths, the
  correlation and adoption, the mapping, the password once, the leaver's steps, the
  traits, the photo, the endpoints' access, no address or Google's words in a log, an
  event or a problem, `contracttest.RequireCoverage`.
- [ ] **4. The attention items' provider** — `feat(staff): the provider's items name their provider (Google B)`.
  `openapi/staff.yaml` `provider`/`providerName`; `accounts_attention.go`; the host's
  `dashboard-helpers.ts` and catalog in en and nb; tests on both sides.
- [ ] **5. The Accounts tab** — `feat(google-ui): the Accounts tab (Google B)`.
  The account rules form, the accounts table with *Confirm*, the groups table with
  *Confirm*, the licences; both languages; the host's route.
- [ ] **6. Documentation** — `docs(google): …`: `admin/google-workspace.md` and
  `user/google-workspace.md` (en, nb); `user/staff.md` and `admin/staff.md` (en, nb) where
  they name providers; `reference/google.md`, `reference/staff.md`,
  `contributing/platform-packages.md`.
- [ ] **7. Embedded docs** — `go generate ./...` in `apps/server`,
  `chore(assistant): regenerate the embedded documentation`.
- [ ] **8. Gates** — `mise run server:check`, `mise run server:test`,
  `mise run frontend:check` (its log read), `mise run docs:check`, serially.

No worker (G22): Staff's accounts worker reads each link's `Account` on its own cycle
(`srv/staff/accounts_worker_provider.go:485`); `users.watch` stays on the Later list.

## Unverified items this delivery depends on

- **U11** (`query=externalId:` matches exactly and across types): every answer is
  filtered client-side to an `organization` external id equal to the number, ignoring
  case; the fake answers prefix and other-type matches to prove it.
- **U12** (Groups Settings' host): `www.googleapis.com/groups/v1`; both hosts are on the
  fixed list.
- **U13** (`users.photos.update` takes a 96 px JPEG in the `*`-padded alphabet): a refusal
  reports `photo_not_written`.
- **U16** (a suspended user's licence is billed; removing it is allowed):
  `leaver_remove_licences` defaults on and can be turned off.
- **U19** (`users.signOut` under the delegated scope): a refusal is reported
  `sign_out_refused`, the suspension already done.
- **Licensing by the primary email**: `licenseAssignments` is addressed by the user's
  primary email, as its reference documents; the path is logged with the address hidden.
