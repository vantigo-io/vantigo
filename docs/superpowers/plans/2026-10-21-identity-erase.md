# Entra phase 1, delivery E — identity erase: implementation plan

> **For agentic workers:** implement this plan task by task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** when Staff's retention run erases a person (D-erase), it also erases their managed Vantigo account (`identity.users`) and retires or finishes their external account lifecycle — hard-deleting the `identity.users` row with its invitations, SCIM mapping, expected account, and delegations; pseudonymising `before_json` and `after_json` snapshots in `identity.authorization_audit_events` to `"[erased]"`; recording `account.erased` with the user id alone; recording `identity_erased` on the person in Staff; withdrawing any unfulfilled external-account announcement (`Unexpect`); letting an erased leaver's provider account finish deactivating and deleting; and raising `erasureIncomplete` on the dashboard when the identity erase cannot complete.

**Architecture:** no new module and no database migration. `contracts.AccountProvisioning` gains `Erase(ctx, actor, userID)` alongside `ErrAccountEnabled` and `SystemActorStaffRetention`. `identity` implements `Erase` inside its serializable owner-locked transaction (`writeIn`), refusing unmanaged, privileged, last-owner, or still-enabled accounts, deleting the user's dependent rows (`expected_accounts`, `invitations`, `scim_user_mappings`, `authorization_delegations`, and `users`), pseudonymising PII keys in `authorization_audit_events` snapshots to `"[erased]"`, and writing `account.erased`. `staff`'s retention run calls `Erase` (and `Deactivate` first when the account is still enabled, or `Unexpect` when an announcement was never fulfilled) after releasing the retention lease connection so 1-connection pools never deadlock, records `identity_erased` on success, or stores the failure on the `identity` link (`next_attempt_at` tomorrow's midnight) so the next retention run retries and `AccountLinkAttention` surfaces `erasureIncomplete`. `decideProvider` and `ClaimAccountLink` continue driving an erased leaver's provider link through deactivation and `Delete`. Branch `feat/identity-erase`, cut from `main` at `8472d164` (D merged).

**Spec:** [`docs/superpowers/specs/2026-10-19-entra-design.md`](../specs/2026-10-19-entra-design.md) D4 (`Erase`), D13 (leaver lifecycle after erase), D16 (erasure across the three modules, `identity_erased`, `erasureIncomplete`), the deliveries table (E), testing (E).

## Tasks

- [x] **1. This plan.**
- [x] **2. Contracts & module composition (`internal/contracts`, `internal/module`).**
  - `apps/server/internal/contracts/accounts.go`: add `SystemActorStaffRetention = "system:staff-retention"` and accept it in `Actor.Valid()`; add `ErrAccountEnabled = errors.New("contracts: the account is not disabled")`; add `Erase(ctx context.Context, actor Actor, userID uuid.UUID) error` to `AccountProvisioning`.
  - `apps/server/internal/contracts/accounts_test.go` and `identity_provisioner_test.go`: pin `SystemActorStaffRetention` and `ErrAccountEnabled`.
  - `apps/server/internal/module/compose_test.go`: add `Erase` to `fakeAccounts`.
- [x] **3. Identity `AccountProvisioning.Erase` (`internal/identity`).**
  - `apps/server/internal/identity/queries/provisioning.sql` and `audit.sql`: add `DeleteUserExpectedAccounts`, `DeleteUserInvitations`, `DeleteUserScimMapping`, `DeleteUserCreatedDelegations`, `ListUserAuditEventsForErase`, and `UpdateAuditEventSnapshots`; regenerate `internal/identity/db`.
  - `apps/server/internal/identity/provisioning.go`: implement `(*provisioning).Erase(ctx, actor, userID)` under `p.writeIn(ctx, actor, false, ...)`, checking `ErrUserGone`, `ErrAccountNotManaged`, `ErrLastOwner`, `ErrAccountPrivileged`, and `ErrAccountEnabled`, deleting the user and dependent rows, pseudonymising PII in `actor_snapshot` and `target_snapshot` (`displayName: "Erased user"`, `email: ""`, `employeeNumber: ""`), and writing `user.staff-erased` with no target snapshot.
  - `apps/server/internal/identity/provisioning_test.go`: test happy path (hard delete, cascade cleanup, snapshot pseudonymisation, `user.staff-erased` audit event) and every refusal (`ErrUserGone`, `ErrAccountNotManaged`, `ErrAccountPrivileged`, `ErrLastOwner`, `ErrAccountEnabled`) plus actor validation.
- [x] **4. Staff retention erase, `identity_erased`, `erasureIncomplete`, and erased-leaver provider lifecycle (`internal/staff`, `openapi/staff.yaml`).**
  - `openapi/staff.yaml`: add `erasureIncomplete` to `StaffStatsAttentionItem.type` and `identity_erased` to `StaffEvent`; run `go generate ./...` in `apps/server`.
  - `apps/server/internal/staff/contractscalls.go`, `contractscalls_internal_test.go`, `accounts_fake_test.go`: add `accountsErase` and wire fakes/tests.
  - `apps/server/internal/staff/account_flag.go`: add `eventIdentityErased = "identity_erased"` to `accountEventKinds`.
  - `apps/server/internal/staff/queries/retention.sql` and `accounts.sql` (+ `sqlc generate`): add queries to list erased people needing account erasure / unexpect, delete or clear the `identity` link after erasure, record `erasure_incomplete` on the `identity` link, include incomplete identity erasures in `AccountLinkAttention`, and let `ClaimAccountLink` / `CountDueAccountLinks` claim an erased leaver's non-deleted provider link.
  - `apps/server/internal/staff/retention.go` and `retention_worker.go`: run `eraseAccounts(ctx, who, zone)` after releasing the lease connection in `eraseUnderLease`; withdraw unfulfilled announcements via `accountsUnexpect`, erase via `accountsErase`, record `identity_erased`, or record `erasure_incomplete` for retry and `erasureIncomplete`.
  - `apps/server/internal/staff/accounts_decide_provider.go` and `accounts_attention.go`: allow erased leavers' provider links to delete after `deleteExternalAfterDays`; emit `erasureIncomplete` (with `erasedPersonTitle`) when an erased person's managed `identity` link has `blocked_reason = erasure_incomplete`.
  - `apps/server/internal/staff/retention_test.go`, `accounts_attention_test.go`, `accounts_decide_provider_internal_test.go`: pin all new flows.
- [x] **5. Frontend (`apps/staff/frontend`, `apps/host/frontend`).**
  - Run `bun run gen:client`.
  - `apps/staff/frontend/src/lib/events.ts`, `i18n.ts`: wire `identity_erased` and `accountReason_erasure_incomplete` in `en` and `nb`.
  - `apps/host/frontend/src/lib/dashboard-helpers.ts`, `catalogs/dashboard.ts`, `routes/dashboard.test.ts`: wire `erasureIncomplete` in `en` and `nb`.
- [x] **6. Bilingual documentation (`docs/src/content/docs/{en,nb}`) & `ROADMAP.md`.**
  - Update `admin/authentication.md`, `user/staff.md`, `admin/staff.md`, `reference/staff.md` in `en` and `nb`, and mark Delivery E done in `ROADMAP.md`.
- [x] **7. Verification.**
  - Run `server:check`, `server:test`, frontend typecheck and unit tests, and `docs:check`.
