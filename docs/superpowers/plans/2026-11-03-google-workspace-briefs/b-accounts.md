# Brief B — Accounts, groups, licences and photos in Google Workspace

You are implementing delivery **B** of the design
`docs/superpowers/specs/2026-11-03-google-workspace-design.md` (decisions **G17–G22**,
with G6 from delivery 0 already on `main`). Read that file first, in full, then this
brief. You work alone, from a cold start, in this repository. Model: Claude Opus.
Deliveries 0 and A are on `main`.

## The outcome

HR sets a person's account kind to **Google Workspace** in Staff. Staff's accounts worker
then prepares the person's Google account (suspended, correlated by the employee number),
activates it on the start day with a generated password revealed once to HR (changed at
first sign-in), keeps the profile and the manager in step, assigns licences from the
person's groups, pushes the photo at 96 px, suspends the account on the leave day with the
leaver steps, and deletes it when Staff's existing *delete after N days* says so. Staff's
groups become Google groups with an address, a visibility and licences. Nothing in Staff
changes: it drives the `google` provisioner through the slot exactly as it drives Entra.

## Read before you plan

- `AGENTS.md`, `en/contributing/module-boundaries.md` (rule 13), the Entra design's
  D5–D14 (`docs/superpowers/specs/2026-10-19-entra-design.md`).
- `apps/server/internal/contracts/identity_provisioner.go` (every method, `ExternalSpec`,
  `ExternalAccount`, the reported codes, `Pass`, `GroupTrait`, the errors) and
  `internal/modtest/identity_provisioner.go`.
- Entra's provider as the template: `entra/{provisioner.go,lifecycle_prepare.go,lifecycle_activate.go,lifecycle_deactivate.go,lifecycle_user.go,address.go,groups.go,groups_traits.go,groups_endpoints.go,photo.go}`,
  `mig/00061`, `00063`, `00070`.
- Staff's side, to know what it expects and must not change: `staff/accounts_worker.go`,
  `accounts_worker_provider.go`, `accounts_decide_provider.go`, `accounts_provider.go`,
  `account_settings.go` (`deleteExternalAfterDays`), `groups_traits.go`,
  `accounts_worker_photo.go`, `person_photo_sync.go`, `meta.go`.
- Delivery A's `internal/google` (client, token source, connection, events, googletest).
- Docs: `en/admin/entra.md` (*Set the account rules*, *What a leaver's steps do*, *Staff's
  groups in the tenant*, *Accounts Vantigo did not create*), `en/user/staff.md` and
  `en/admin/staff.md` where they name providers, `en/contributing/platform-packages.md`.

## Build

1. **The address package (G18)**, its own commit: move `entra/address.go`'s pure
   functions (and their internal test) to `internal/directoryaddress`, Entra calling
   it; nothing in Entra's behaviour changes (`refactor(entra): …`). List it in
   `platform-packages.md` in the same commit.
2. **Migration `00113_google_accounts.sql`** as G17 (accounts, groups, the account-rule
   columns with CHECKs mirroring Entra's).
3. **The provisioner (G17)**: `Key() "google"`, `Provider` from the connection (status,
   reason), `Prepare`, `Activate`, `Update`, `Deactivate`, `Delete`, `Account`,
   `RevealPass`, with the correlation and adoption rules, the field mapping (Google's
   attributes only; the unsupported fields ignored and not reported), the password
   generated and sealed under `google/pass` with `pass_lifetime_days`, the licences via
   `licensing.licenseAssignments`. Register it as `Module.IdentityProvisioner`.
   Directory calls as `admin_subject` with `admin.directory.user`; licences with
   `apps.licensing`. Add both, with `admin.directory.group` and `apps.groups.settings`,
   to the scopes the test asks for.
4. **Leavers (G19)**: suspend; licences removed when `leaver_remove_licences`; groups by
   `leaver_remove_groups`; `users.signOut` when `leaver_sign_out` (a refusal reported,
   U19). No delete setting of Google's own: Staff's `deleteExternalAfterDays` calls
   `Delete`.
5. **Groups and licences (G20)**: the three traits with their labels in en and nb;
   `EnsureGroup` with Groups Settings for visibility (call the host the test tenant
   confirms, U12); `SetMembers`, `DeleteGroup`; `GET /google/connection/skus` caching
   `connection.skus`.
6. **Photos (G21)**: 96×96 centre crop, JPEG, Google's base64 alphabet;
   `photo_not_written` on refusal.
7. **Endpoints**: `GET /google/accounts`, `POST /google/accounts/{userId}/confirm`,
   `GET /google/groups`, `POST /google/groups/{groupId}/confirm` (`google:manage`),
   `PUT /google/connection/settings` (Owner policy, `If-Match`), `GET /google/connection/skus`.
8. **`googletest`**: users (insert, get, patch, delete, list with `externalId:` query
   and the client-side filter's edge cases, signOut, photos), groups, members, Groups
   Settings, licence assignments and `listForProduct`.
9. **The Accounts tab**: the account rules form, the accounts table with *Confirm*, the
   groups table with *Confirm*, the licences.
10. **Docs**, then `go generate ./...` and the regenerated assistant docs.

No worker (G22): say so in the plan, citing `staff/accounts_worker_provider.go:485`.

## The documentation (same commit series)

- `en/` and `nb/` `admin/google-workspace.md`: the account rules (address pattern and
  domain, the organisational unit), the password revealed once and its lifetime and who
  receives it, licences and groups (the three traits), photos at 96 px, what a leaver's
  steps do, delete after N days (Staff's setting, under `staff:manage`), confirming an
  account or a group Vantigo did not create, the reason codes.
- `en/` and `nb/` `user/google-workspace.md` (the Accounts tab), `user/staff.md` (the
  Google Workspace kind; a password, not a Temporary Access Pass, is shown once),
  `admin/staff.md` where it names providers.
- `reference/google.md` (the provisioner, the tables, the correlation, the mapping, the
  leaver steps, the traits, the photo, the endpoints), `reference/staff.md`
  (`deleteExternalAfterDays` for Google), `contributing/platform-packages.md`.

## Do not

Change Staff's worker, its decisions or its OpenAPI · add a delete setting to Google ·
write the hire or leave date anywhere · rename a primary email · add a reconcile worker
or `users.watch` · call the Calendar or Gmail APIs · log an address or Google's words.

## How to work

- Worktree on `feat/google-accounts` cut from the previous delivery's branch (the README's stack); `bun install
  --frozen-lockfile` first.
- **Plan first**: `docs/superpowers/plans/2026-11-03-google-accounts.md`, committed alone
  as `docs(google): the plan for accounts and groups (Google B)`.
- The migration takes the next free number (expected 00113).
- Commits: `refactor(entra): …` (the address package), `feat(google): … (Google B)`,
  `feat(google-ui): … (Google B)`, `docs(google): …`, `chore(assistant): regenerate the
  embedded documentation`. Signed.
- **No `Docs-Impact: none` trailer** — not even on the refactor commit: a trailer on one
  commit waives the coverage check for the whole pull request.
- Gates: `mise run server:check`, `mise run server:test`, `mise run frontend:check`,
  `mise run docs:check`. No merge of `main`; the starting session pushes and opens the pull request.

## Report

Branch and `git log --oneline main..`; the gates' output; the plan's path; the migration
number; departures from G17–G22; U11, U12, U13, U16, U19 as the test tenant answered
them; and the manual check: a person of the Google kind prepared (suspended), activated
(the password revealed once, then gone), updated (title, manager), a group with a licence,
a photo, a leaver suspended with licences removed.
