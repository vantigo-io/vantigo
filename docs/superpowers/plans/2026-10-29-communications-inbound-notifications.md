# Communications Microsoft 365 Inbound Notifications, Outlook State on Close & Import Window (Delivery E) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver **E** of the Outlook connection & Microsoft 365 mail design (`docs/superpowers/specs/2026-10-29-outlook-connection-and-mail-design.md`): Graph mail change notifications (`Watch` / `Unwatch` / `Notified` on `contracts.ChannelMailbox`, sharing Entra's `/api/v1/entra/mailbox-notifications` webhook and widening the safety-net poll interval from 5 minutes to 1 hour when notifications are active), per-channel *Outlook state on close* (`none` | `read` | `move`, drained asynchronously via `communications.mailbox_marks` using `Mark` and `EnsureFolder`), and the per-channel *import window* (`from_now` | `last_30_days` | `last_90_days` writing `channel_sync.import_from`).

**Architecture:**
- **Entra (`internal/entra`, migration `00093`):** `entra.mailbox_subscriptions` gains `resource varchar(40) NOT NULL DEFAULT 'events'` with primary key `(object_id, resource)`. `MailboxCalendar` continues to manage `resource = 'events'` with the person's delegated token; `ChannelMailbox` manages `resource = 'messages'` (`/users/{oid}/mailFolders('inbox')/messages`, `changeType: "created"`) with the **application** token behind the same `recorded` + `ready` gate as `Send`/`Delta`/`Raw`. The existing webhook (`POST /api/v1/entra/mailbox-notifications`) looks up subscriptions by `subscription_id` (`UNIQUE`) and sets `notified_at`, while `Notified(ctx, since)` filters by `resource`. `ChannelMailbox` also implements `EnsureFolder(ctx, mailbox, name)` (`GET /users/{oid}/mailFolders?$filter=displayName eq '...'`, creating via `POST /users/{oid}/mailFolders` on miss or re-reading on 409 `ErrorFolderExists`) and `Mark(ctx, mailbox, messageID, op)` (`PATCH .../messages/{id}` for `read`/`category`, `POST .../messages/{id}/move` for `move`, all with `Prefer: IdType="ImmutableId"` and `404` mapped to `contracts.ErrMailItemGone`).
- **Communications (`internal/communications`, migration `00094`):** `communications-mailbox-sync` drains `ChannelMailbox.Notified` before claiming due channels (30-second overlap cursor, mirroring `calendar-mailbox-sync`), marks matching `channel_sync` rows `notified = true` and `next_sync_at = LEAST(next_sync_at, now)`, calls `Watch(ctx, mailbox, "inbox")` after a finished sync pass (`page.Done`), and sets `next_sync_at` to `now + 1h` when notifications are active (`CALENDAR_MAILBOX_NOTIFICATIONS_ENABLED` + non-empty `PUBLIC_BASE_URL` + `watchErr == nil`) or `now + 5m` otherwise. Switching an `entra` channel back to `smtp` or deactivating it calls `Unwatch(ctx, mailbox, "inbox")`. Closing a conversation (`status -> closed`) on an `entra` channel whose `settings.onClose` is `"read"` or `"move"` enqueues/resets a row in `communications.mailbox_marks` inside the PATCH transaction; `communications-mailbox-sync` drains `mailbox_marks` outside transactions using `EnsureFolder` and `Mark` over the conversation's `inbound_receipts` (`provider_message_id` where `message_id IS NOT NULL`). Channel create and switch-to-Microsoft-365 accept `importWindow` (`from_now`, `last_30_days`, `last_90_days`) to set `channel_sync.import_from`, plus `onClose` and `folder` (also editable via `PATCH /channels/{id}`).

**Tech Stack:** Go 1.26, Chi, Goose migrations (`00093` in `entra`, `00094` in `communications`), sqlc, OpenAPI 3.0 (`oapi-codegen` + `@hey-api/openapi-ts`), React 19 + TanStack Query + Tailwind (`apps/communications/frontend`), Astro Starlight docs (`en` + `nb`).

---

## Design Readings

Read these before touching code:
- `docs/superpowers/specs/2026-10-29-outlook-connection-and-mail-design.md` — **M11**, **M12**, **M14** (step 3), **M17**, **Deliveries › E**, **Testing › E**, and the *as built* notes for **B**, **C**, and **D**.
- `docs/superpowers/plans/2026-10-29-communications-m365-inbound.md` — Delivery D's plan for structure and conventions.
- `apps/server/internal/entra/mailbox_notifications.go` and `apps/server/internal/entra/queries/mailbox_subscriptions.sql` — existing subscription lifecycle and webhook handling.
- `apps/server/internal/calendar/mailbox_worker.go` — existing `Notified` cursor and `Watch` / safety-net cadence pattern.
- `apps/server/internal/communications/mailbox_sync.go`, `outbox.go`, `channels.go`, `conversations_patch.go`, `contractscalls.go`.

---

## Task 1: Save Delivery E's Implementation Plan

**Files:**
- Create: `docs/superpowers/plans/2026-10-29-communications-inbound-notifications.md`

- [ ] **Step 1: Write and commit this plan alone**

Commit with `git commit -S -m "docs(communications, entra): delivery E's plan — mail notifications, on-close marks, import window"`.

---

## Task 2: Foundation — Migrations (`00093`, `00094`), `contracts.ChannelMailbox`, and `modtest.FakeChannelMailbox`

**Files:**
- Create: `apps/server/internal/db/migrations/00093_entra_mailbox_subscriptions_resource.sql`
- Create: `apps/server/internal/db/migrations/00094_communications_mailbox_marks.sql`
- Modify: `apps/server/internal/entra/sqlc.yaml`
- Modify: `apps/server/internal/communications/sqlc.yaml`
- Modify: `apps/server/internal/db/schema_entra_mailbox_subscriptions_test.go`
- Create: `apps/server/internal/db/schema_communications_mailbox_marks_test.go`
- Modify: `apps/server/internal/contracts/channel_mailbox.go`
- Modify: `apps/server/internal/modtest/channel_mailbox.go`
- Modify: `apps/server/internal/modtest/channel_mailbox_test.go`
- Modify: `apps/server/internal/entra/channel_mailbox.go` (temporary compile stubs for the new interface methods, replaced in Task 3)

- [ ] **Step 1: Create `00093_entra_mailbox_subscriptions_resource.sql` (`entra` schema only)**
  - Add `resource varchar(40) NOT NULL DEFAULT 'events'` to `entra.mailbox_subscriptions`.
  - Replace primary key `mailbox_subscriptions_pkey` with `PRIMARY KEY (object_id, resource)`.
  - In `-- +goose Down`, delete rows where `resource <> 'events'`, restore `PRIMARY KEY (object_id)`, and drop column `resource`.
  - Register `00093_entra_mailbox_subscriptions_resource.sql` in `apps/server/internal/entra/sqlc.yaml`.

- [ ] **Step 2: Create `00094_communications_mailbox_marks.sql` (`communications` schema only)**
  - Create `communications.mailbox_marks`:
    - `conversation_id uuid PRIMARY KEY REFERENCES communications.conversations(id) ON DELETE CASCADE`
    - `operation varchar(20) NOT NULL` (`CHECK (operation IN ('read', 'move'))`)
    - `folder varchar(120)`
    - `status varchar(20) NOT NULL DEFAULT 'pending'` (`CHECK (status IN ('pending', 'processing', 'retry', 'completed', 'failed'))`)
    - `attempts integer NOT NULL DEFAULT 0`
    - `next_attempt_at timestamptz NOT NULL`
    - `queued_at timestamptz NOT NULL`
    - `completed_at timestamptz`
    - `last_error varchar(40)`
    - `lease_id varchar(32)`
    - `lease_until timestamptz`
  - Create partial index `ix_mailbox_marks_due ON communications.mailbox_marks (next_attempt_at) WHERE status IN ('pending', 'retry', 'processing')`.
  - Register `00094_communications_mailbox_marks.sql` in `apps/server/internal/communications/sqlc.yaml`.

- [ ] **Step 3: Add DB schema tests**
  - Update `apps/server/internal/db/schema_entra_mailbox_subscriptions_test.go` so the HEAD table inspection expects `resource` and PK `(object_id, resource)`, and add `TestEntraMailboxSubscriptionsResource_AppliesAndIsIdempotent` testing version `93`.
  - Create `apps/server/internal/db/schema_communications_mailbox_marks_test.go` testing version `94`, its columns, PK, FK cascade on `conversations(id)`, `CHECK` constraints (`operation`, `status`), and `ix_mailbox_marks_due`.

- [ ] **Step 4: Extend `contracts.ChannelMailbox` and `modtest.FakeChannelMailbox`**
  - In `apps/server/internal/contracts/channel_mailbox.go`, add:
    - `Watch(ctx context.Context, mailbox, folder string) error`
    - `Unwatch(ctx context.Context, mailbox, folder string) error`
    - `Notified(ctx context.Context, since time.Time) ([]MailboxNotice, error)`
    - `Mark(ctx context.Context, mailbox, messageID string, op MailMark) error`
    - `EnsureFolder(ctx context.Context, mailbox, name string) (string, error)`
    - `MailMark` and `MailMarkKind` (`MailMarkRead`, `MailMarkCategory`, `MailMarkMove`).
  - In `apps/server/internal/modtest/channel_mailbox.go` and `channel_mailbox_test.go`, implement `Watch`, `Unwatch`, `Notified`, `Notify`, `Watched`, `SetWatchError`, `Mark`, `EnsureFolder`, `MessageState`, `Folders`, and `SetMarkError`, with unit tests.

- [ ] **Step 5: Run `sqlc generate` and tests, then commit**
  - Run `go test ./internal/db ./internal/modtest`.
  - Commit signed (`Docs-Impact: none — the pages land in task 6's commit of this delivery`).

---

## Task 3: Entra — Mail Subscriptions (`Watch`, `Unwatch`, `Notified`) and Message State (`Mark`, `EnsureFolder`)

**Files:**
- Modify: `apps/server/internal/entra/queries/mailbox_subscriptions.sql`
- Modify: `apps/server/internal/entra/gen/...` (via `sqlc generate`)
- Modify: `apps/server/internal/entra/mailbox_notifications.go`
- Create: `apps/server/internal/entra/channel_mailbox_notifications.go`
- Create: `apps/server/internal/entra/channel_mailbox_marks.go`
- Modify: `apps/server/internal/entra/channel_mailbox.go`
- Modify: `apps/server/internal/entra/graphtest/graph.go`
- Modify: `apps/server/internal/entra/graphtest/subscriptions.go`
- Modify: `apps/server/internal/entra/graphtest/mail.go`
- Modify: `apps/server/internal/entra/graphtest/mail_inbox.go`
- Modify: `apps/server/internal/entra/graphtest/graph_test.go`
- Create: `apps/server/internal/entra/channel_mailbox_notifications_test.go`
- Create: `apps/server/internal/entra/channel_mailbox_marks_test.go`

- [ ] **Step 1: Extend `graphtest` for application-token mail subscriptions, folders, and message marks**
  - Allow `/subscriptions` on the application token (`g.caller == nil`) for `users/{id}/mailFolders('inbox')/messages` with `changeType == "created"`, and keep delegated token enforcement for `users/{id}/events`.
  - Add `IsRead`, `Categories`, and `FolderID` state to `graphtest.InboxMessage`; support `PATCH /users/{id}/messages/{mid}` (`isRead`, `categories`), `POST /users/{id}/messages/{mid}/move` (`destinationId`, keeping the immutable ID and moving the message out of Inbox when destination is another folder), and `GET`/`POST /users/{id}/mailFolders` (including `$filter=displayName eq '...'` and `409 ErrorFolderExists` on duplicate display name).
  - Test the new `graphtest` routes in `apps/server/internal/entra/graphtest/graph_test.go`.

- [ ] **Step 2: Key `entra.mailbox_subscriptions` queries by `(object_id, resource)`**
  - Update `apps/server/internal/entra/queries/mailbox_subscriptions.sql` so `GetMailboxSubscription`, `UpsertMailboxSubscription`, `MarkMailboxSubscriptionConflict`, `RenewMailboxSubscription`, `MarkMailboxSubscriptionDeleting`, `ForgetMailboxSubscription`, and `ListMailboxNotified` filter on `resource`.
  - Keep `GetMailboxSubscriptionByGraphID`, `RecordMailboxNotification`, `MarkMailboxSubscriptionReauthorize`, `DeleteMailboxSubscriptionByGraphID`, and `PruneStaleMailboxSubscriptions` keyed by `subscription_id` / `updated_at` so the single `/api/v1/entra/mailbox-notifications` webhook and sweep serve both `'events'` and `'messages'`.
  - Update `MailboxCalendar` in `mailbox_notifications.go` to pass `resource = "events"`.

- [ ] **Step 3: Implement `Watch`, `Unwatch`, `Notified` on `channelMailbox`**
  - In `channel_mailbox_notifications.go`:
    - `Watch(ctx, mailbox, folder)` validates `strings.EqualFold(folder, "inbox")`, checks `m.recorded(ctx, mailbox)` and `m.ready(ctx)`, and manages a subscription for `resource = "messages"` at `users/{oid}/mailFolders('inbox')/messages` with `changeType: "created"` using the **application** token (`m.graph.post`, `m.graph.patch`, `m.graph.delete`, `m.graph.get`).
    - `Unwatch(ctx, mailbox, folder)` deletes any existing `'messages'` subscription on the application token and removes the row.
    - `Notified(ctx, since)` checks `m.ready(ctx)` and queries `ListMailboxNotified` for `resource = "messages"`.

- [ ] **Step 4: Implement `Mark` and `EnsureFolder` on `channelMailbox`**
  - In `channel_mailbox_marks.go`:
    - `EnsureFolder(ctx, mailbox, name)` checks `m.recorded` + `m.ready`, validates `name` (trimmed 1..120 runes, no control chars), queries `GET /users/{oid}/mailFolders?$filter=displayName eq '{escaped}'&$select=id,displayName&$top=10`, creates with `POST /users/{oid}/mailFolders` (`noRetry`) if absent, and re-reads on `409 ErrorFolderExists`.
    - `Mark(ctx, mailbox, messageID, op)` checks `m.recorded` + `m.ready`, validates `messageID`, sends `Prefer: IdType="ImmutableId"`, handles `MailMarkRead` (`PATCH .../messages/{id}` `{"isRead": true}`), `MailMarkCategory` (`PATCH .../messages/{id}` `{"categories": ...}`), and `MailMarkMove` (`POST .../messages/{id}/move` `{"destinationId": op.FolderID}` with `noRetry`), mapping `404 ErrorItemNotFound` to `contracts.ErrMailItemGone`.

- [ ] **Step 5: Write Entra tests and commit**
  - Add `channel_mailbox_notifications_test.go` and `channel_mailbox_marks_test.go` covering `Watch`/`Renew`/`Unwatch`, shared webhook delivery isolated between `'events'` and `'messages'`, `EnsureFolder` idempotence + 409 race, and `Mark` (`read`, `category`, `move`, `404 -> ErrMailItemGone`, unrecorded refusal).
  - Run `go test ./internal/entra/...` and commit signed.

---

## Task 4: Communications Server — OpenAPI, Notifications, Import Window, and On-Close `mailbox_marks`

**Files:**
- Modify: `openapi/communications.yaml`
- Modify: `apps/server/internal/openapi/specs/communications.yaml`
- Modify: `apps/server/internal/communications/gen/openapi.gen.go`
- Modify: `apps/server/internal/communications/contractscalls.go`
- Modify: `apps/server/internal/communications/contractscalls_test.go`
- Modify: `apps/server/internal/communications/channels.go`
- Modify: `apps/server/internal/communications/queries/mailbox_sync.sql`
- Modify: `apps/server/internal/communications/gen/...` (via `sqlc generate`)
- Modify: `apps/server/internal/communications/mailbox_sync.go`
- Create: `apps/server/internal/communications/mailbox_marks.go`
- Modify: `apps/server/internal/communications/conversations_patch.go`
- Modify: `apps/server/internal/communications/channels_entra_test.go`
- Create: `apps/server/internal/communications/mailbox_notifications_test.go`
- Create: `apps/server/internal/communications/mailbox_marks_test.go`

- [ ] **Step 1: Update `openapi/communications.yaml` and regenerate server code**
  - Add `ChannelImportWindow` (`from_now`, `last_30_days`, `last_90_days`) and `ChannelOnClose` (`none`, `read`, `move`).
  - Add optional `importWindow`, `onClose`, `folder` to `CreateChannelRequest` and `SwitchChannelProviderRequest`.
  - Add optional `onClose` and `folder` to `UpdateChannelRequest`.
  - Add `onClose` and `folder` (`nullable: true`) to `ChannelMailboxInfo` (`ChannelResponse.mailbox`).
  - Copy to `apps/server/internal/openapi/specs/communications.yaml` and regenerate `apps/server/internal/communications/gen/openapi.gen.go`.

- [ ] **Step 2: Add contract call wrappers in `contractscalls.go`**
  - Add `mailboxWatch`, `mailboxUnwatch`, `mailboxNotified`, `mailboxMark`, and `mailboxEnsureFolder` with the `errContractCallInTx` guard, and test them in `contractscalls_test.go`.

- [ ] **Step 3: Update channel handlers (`channels.go`) for `importWindow`, `onClose`, `folder`, and `Unwatch`**
  - Extend `entraChannelSettings` to `{Mailbox, OnClose, Folder}` (`onClose` default `"none"`, `folder` default `"Handled in Vantigo"` when `onClose == "move"`).
  - Compute `import_from` from `importWindow`: `from_now` (default) -> `now`, `last_30_days` -> `now - 30d`, `last_90_days` -> `now - 90d`.
  - Allow `PATCH /channels/{id}` on an `entra` channel to update `onClose` and `folder` in `channels.settings`.
  - When switching an `entra` channel back to `smtp` (`SwitchChannelProvider`) or deactivating an active `entra` channel (`UpdateChannel` with `isActive = false`), call `mailboxUnwatch(ctx, m.mailbox, oldMailbox, mailboxSyncFolder)` best-effort outside any transaction.

- [ ] **Step 4: Wire mail notifications and `Watch` into `MailboxSyncWorker` (`mailbox_sync.go`)**
  - Add `MarkChannelSyncNotified` query in `queries/mailbox_sync.sql`: updates active `entra` channels matching `LOWER(settings->>'mailbox') = LOWER(@mailbox)` to set `notified = true, next_sync_at = LEAST(next_sync_at, @now), updated_at = @now`.
  - At the start of `MailboxSyncWorker.RunCycle`, call `drainNotices(ctx, now)` with a 30-second overlap cursor (`notifiedSince`), marking matching channels due.
  - When a channel sync pass finishes (`page.Done`), call `watchErr := mailboxWatch(ctx, c.provider, c.mailbox, mailboxSyncFolder)`. Set `next_sync_at` to `now + 1h` (`mailboxSyncSafetyInterval`) when `w.notificationsActive() && watchErr == nil` (`cfg.CalendarMailboxNotificationsEnabled && strings.TrimSpace(cfg.PublicBaseURL) != ""`), and `now + 5m` (`mailboxSyncInterval`) otherwise.

- [ ] **Step 5: Enqueue and drain `communications.mailbox_marks` on conversation close**
  - Add sqlc queries in `queries/mailbox_sync.sql` for `UpsertMailboxMark`, `ClaimDueMailboxMarks`, `ListConversationInboundProviderIDs`, `CompleteMailboxMark`, `RetryMailboxMark`, `FailMailboxMark`, and `HoldMailboxMark`.
  - In `conversations_patch.go`, inside the transaction when `existing.Status != "closed" && nextStatus == "closed"`, if the channel has `provider == "entra"` and `onClose` is `"read"` or `"move"`, call `UpsertMailboxMark` (`status = 'pending'`, `attempts = 0`, `next_attempt_at = now`).
  - In `mailbox_marks.go`, implement `drainMarks(ctx, now)` called by `MailboxSyncWorker.RunCycle`:
    - Claims due `mailbox_marks` rows with a 2-minute lease.
    - Reads the channel's mailbox and the conversation's `inbound_receipts` (`provider_message_id` where `message_id IS NOT NULL`).
    - For `move`, resolves the folder ID once via `mailboxEnsureFolder(ctx, provider, mailbox, folderName)`.
    - Calls `mailboxMark` for each message (`ErrMailItemGone` is treated as already handled).
    - Completes on success, holds for 5 minutes on `ErrChannelMailboxUnavailable` / `ErrChannelMailboxUnrecorded`, or retries with exponential backoff up to `maxMailboxMarkAttempts = 10` before marking `failed`.

- [ ] **Step 6: Test and commit**
  - Add unit and integration tests in `channels_entra_test.go`, `mailbox_notifications_test.go`, and `mailbox_marks_test.go`.
  - Run `go test ./internal/communications/...` and commit signed.

---

## Task 5: Communications Frontend — Import Window and Outlook State on Close

**Files:**
- Modify: `apps/communications/frontend/src/api/gen/...` (via `bun run gen:client`)
- Modify: `apps/communications/frontend/src/catalog.ts`
- Modify: `apps/communications/frontend/src/components/ChannelForm.tsx`
- Modify: `apps/communications/frontend/src/pages/admin.channels.tsx`
- Modify: `apps/communications/frontend/src/pages/admin.channels.test.tsx`

- [ ] **Step 1: Regenerate TypeScript client and add `en` + `nb` copy**
  - Run `bun run gen:client` in `apps/communications/frontend`.
  - Add catalog strings in `src/catalog.ts` (`en` and `nb`) for the import window selector (*From now on*, *Last 30 days*, *Last 90 days*) and the *When a conversation is closed* setting (*Leave as-is in Outlook*, *Mark as read in Outlook*, *Move to folder in Outlook*, plus folder name label/default `"Handled in Vantigo"`).

- [ ] **Step 2: Update `ChannelForm.tsx` and `admin.channels.tsx`**
  - On Microsoft 365 channel create and switch-to-Microsoft-365: render the import window selector (default `from_now`) and the on-close selector (default `none`, revealing the folder input defaulted to `"Handled in Vantigo"` when `move` is selected).
  - On Microsoft 365 channel edit (`admin.channels.tsx`): allow viewing and updating the on-close behavior and folder name via `PATCH /channels/{id}`, and display the active on-close rule in the Microsoft 365 connection card.

- [ ] **Step 3: Add frontend tests and commit**
  - Extend `apps/communications/frontend/src/pages/admin.channels.test.tsx` to cover creating/switching with `importWindow` and `onClose: "move"`, and editing `onClose` on an existing Microsoft 365 channel.
  - Run `mise run frontend:check` and commit signed.

---

## Task 6: Documentation & Roadmap Updates

**Files:**
- Modify: `docs/src/content/docs/en/reference/entra.md`
- Modify: `docs/src/content/docs/en/reference/communications.md`
- Modify: `docs/src/content/docs/en/contributing/module-boundaries.md`
- Modify: `docs/src/content/docs/en/user/communications.md`
- Modify: `docs/src/content/docs/nb/user/communications.md`
- Modify: `docs/src/content/docs/en/admin/entra.md`
- Modify: `docs/src/content/docs/nb/admin/entra.md`
- Modify: `docs/superpowers/specs/2026-10-29-outlook-connection-and-mail-design.md`
- Modify: `ROADMAP.md`

- [ ] **Step 1: Update reference, contributor, user, and admin docs + spec & roadmap**
  - Document `(object_id, resource)` on `entra.mailbox_subscriptions`, application-token mail subscriptions, and `Watch`/`Unwatch`/`Notified`/`Mark`/`EnsureFolder` in `reference/entra.md` and `contributing/module-boundaries.md`.
  - Document mail change notifications, the 1-hour safety-net interval, `communications.mailbox_marks`, `onClose`/`folder`, and `importWindow` in `reference/communications.md`.
  - Update `en` and `nb` `user/communications.md` and `admin/entra.md`.
  - Record the **E — as built** note in `docs/superpowers/specs/2026-10-29-outlook-connection-and-mail-design.md` and mark Delivery E complete in `ROADMAP.md`.

- [ ] **Step 2: Verify docs build and commit**
  - Run `mise run docs:check` and commit signed.

---

## Task 7: Final Verification & Cleanup

- [ ] **Step 1: Strip temporary `Docs-Impact` trailers from intermediate commits**
- [ ] **Step 2: Run all project verification gates**
  - `mise run server:check`
  - `mise run server:test`
  - `mise run frontend:check`
  - `mise run docs:check`
