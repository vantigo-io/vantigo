# Communications: Sending from One's Own Address (Delivery F) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver **F** of the Outlook connection and Microsoft 365 mail design (`docs/superpowers/specs/2026-10-29-outlook-connection-and-mail-design.md`, M20): let people grant delegated `Mail.Send` on their Outlook connection and choose **Send from my address (`kari@firma.no`)** on both new conversations and replies in Communications, sending through their own Microsoft 365 mailbox (`From` = the person's directory address and display name, `Reply-To` = the conversation's channel address and display name so customer replies arrive in the shared channel mailbox), failing immediately with `connection_needed` if their connection needs reconnecting at outbox send time, and letting them reconnect and retry.

**Architecture:**
- **Entra incremental consent & `contracts.PersonalMailbox`**:
  - `outlookFeatures` in `apps/server/internal/entra/outlook.go` gains `"mail": {"Mail.Send"}`.
  - When a person who already holds a live `entra.outlook_connections` row (`state <> 'closing'`) starts a connect (`POST /api/v1/entra/outlook/connect`), the requested scopes include their existing row's granted feature scopes alongside the requested feature's scopes, and `POST /api/v1/entra/outlook/complete` merges the existing row's scopes with the new token's granted scopes on `TakeOutlookConnection`, so connecting `mail` after `calendar` (or `calendar` after `mail`, or reconnecting from the *Connections* page) preserves both features.
  - `MailboxCalendar` checks that `calendars.readwrite` is in `scopes` (reporting `not_connected` on `Status` and `*contracts.ConnectionNeededError{Reason: "consent_missing"}` on worker calls when an `active` row holds only `mail.send`), and `PersonalMailbox` checks that `mail.send` is in `scopes`.
  - `contracts.PersonalMailbox` (`Status(ctx)` and `Send(ctx, mime, bcc)`) is wired on `module.Module` and `module.Deps` in both `Compose` and `Workers`, with `modtest.FakePersonalMailbox` for tests. `Entra` implements `PersonalMailbox.Send` using the caller's delegated token (`contracts.UserID(ctx)`) against `/users/{oid}/sendMail` (or the MIME draft + `send` dance on `/users/{oid}/messages` when `bcc` is non-empty), handling CAE claims challenges and refresh refusals by marking the connection `reconnect` and returning `*contracts.ConnectionNeededError`.
- **Mail `Reply-To` display name (`apps/server/internal/mail/outbound.go`)**:
  - `mail.Outbound` gains `ReplyToName string`; when non-empty alongside `ReplyTo`, `buildMessage` writes `msg.ReplyToFormat(o.ReplyToName, o.ReplyTo)` so the `Reply-To` header carries the channel's display name and address.
- **Communications personal sending, outbox failure & retry**:
  - Migration `00095_communications_personal_sending.sql` adds `send_as varchar(20) NOT NULL DEFAULT 'channel'` (`CHECK (send_as IN ('channel', 'personal'))`), `from_address varchar(320)` and `from_display_name varchar(200)` to `communications.conversation_messages`.
  - `GET /api/v1/communications/personal-sender` and `ConversationDetailResponse.personalSender` report the caller's personal sender status (`available`, `reason`, `errorCode`, `address`, `displayName`, `maxMessageBytes`, `maxAttachmentBytes`), reading `PersonalMailbox.Status(ctx)` and `UserDirectory.User(ctx, callerID)` through `contractscalls.go` outside any transaction.
  - `POST /api/v1/communications/conversations` and `POST /api/v1/communications/conversations/{id}/reply` accept `sendFromPersonal: true`. When `true`, they verify `PersonalMailbox.Status(ctx).Available` before the transaction (refusing `409 connection_needed` otherwise), enforce the 3 MB MIME size budget (`422 too_large`), and store `send_as = 'personal'`, `from_address` and `from_display_name` on the message row.
  - In `outbox.go`, a message with `send_as = 'personal'` sets `From` to `from_address` (`FromName` = `from_display_name`) and `ReplyTo` to the channel's `address` (`ReplyToName` = `channel_display_name`), and dispatches via `PersonalMailbox.Send(contracts.AsUser(ctx, authorUserID), raw, env.Bcc)`. On `contracts.ErrConnectionNeeded`, the outbox fails the job immediately after 1 attempt with terminal code `connection_needed` (never falling back to the channel's address); on `contracts.ErrMailboxUnavailable`, it holds the job via `holdJob`.
  - `POST /api/v1/communications/conversations/{id}/messages/{messageId}/retry` lets the author of a failed outbound message re-queue its failed deliveries and outbox job once their connection is available again.
- **Frontend (`@vantigo/entra-ui`, `@vantigo/communications-ui`, `host`)**:
  - `@vantigo/entra-ui` adds `"mail"` feature labels (`Mail` / `E-post`), updates the *Outlook connection* description to name `Mail.Send` alongside `Calendars.ReadWrite`, and updates `OutlookConnectButton` so a connection that is `active` for another feature but lacks `feature` in `features` offers **Connect Outlook** to grant the missing feature.
  - `@vantigo/communications-ui` accepts `connectionButton` on `InboxPage` (wired in `apps/host/frontend/src/routes/communications/inbox.tsx` with `feature="mail"` and `returnTo="/communications/inbox"`), adds a **New conversation** modal and updates `ConversationComposer` so both offer **Send from my address (`…`)** (and the inline `connection.button` when not connected or needing reconnect), and shows personal sender attribution (`From` / `Reply-To`) and a **Retry send** action on `connection_needed` failures in `ConversationThread`.

**Tech Stack:** Go 1.26, PostgreSQL + Goose migrations + sqlc, oapi-codegen strict server, React 19 + Mantine + TanStack Query/Router + i18next (`en` + `nb`), Vitest + Testing Library.

---

### Task 1: Research & Plan

**Files:**
- Modify: `docs/superpowers/specs/2026-10-29-outlook-connection-and-mail-design.md`
- Create: `docs/superpowers/plans/2026-10-30-communications-personal-sending.md`

- [ ] **Step 1: Update design spec with Delivery F (M20) and 2026-10-30 owner decisions**
- [ ] **Step 2: Write implementation plan and commit signed (`git commit -S`)**

---

### Task 2: Contracts, OpenAPI, DB Migration & Modtest

**Files:**
- Create: `apps/server/internal/contracts/personal_mailbox.go`
- Modify: `apps/server/internal/module/module.go`
- Modify: `apps/server/internal/module/compose.go`
- Modify: `apps/server/internal/module/workers.go`
- Create: `apps/server/internal/modtest/personal_mailbox.go`
- Modify: `apps/server/internal/modtest/modtest.go`
- Modify: `openapi/entra.yaml`
- Modify: `openapi/communications.yaml`
- Modify: `apps/server/internal/openapi/specs/entra.yaml`
- Modify: `apps/server/internal/openapi/specs/communications.yaml`
- Create: `apps/server/internal/db/migrations/00095_communications_personal_sending.sql`
- Create: `apps/server/internal/db/schema_communications_personal_sending_test.go`
- Modify: `apps/server/internal/communications/queries/conversations.sql`
- Modify: `apps/server/internal/communications/queries/outbox.sql`

- [ ] **Step 1: Define `contracts.PersonalMailbox` (`Status`, `Send`, `PersonalMailStatus`) and wire in `module` and `modtest`**
- [ ] **Step 2: Update `openapi/entra.yaml` (`feature` enum `[calendar, mail]`) and `openapi/communications.yaml` (`PersonalSenderResponse`, `GET /personal-sender`, `sendFromPersonal` on create/reply, message sender fields, `POST .../messages/{messageId}/retry`)**
- [ ] **Step 3: Add migration `00095_communications_personal_sending.sql`, schema test, and sqlc queries; run `sqlc generate` and `go generate ./...`**
- [ ] **Step 4: Run `go test ./internal/db/... ./internal/contracts/...` and commit signed**

---

### Task 3: Entra Implementation (Incremental Consent & `PersonalMailbox`)

**Files:**
- Modify: `apps/server/internal/entra/outlook.go`
- Modify: `apps/server/internal/entra/outlook_connect.go`
- Modify: `apps/server/internal/entra/outlook_complete.go`
- Modify: `apps/server/internal/entra/outlook_token.go`
- Modify: `apps/server/internal/entra/mailbox.go`
- Modify: `apps/server/internal/entra/channel_mailbox.go`
- Create: `apps/server/internal/entra/personal_mailbox.go`
- Modify: `apps/server/internal/entra/module.go`
- Modify: `apps/server/internal/entra/outlook_connect_test.go`
- Create: `apps/server/internal/entra/personal_mailbox_test.go`

- [ ] **Step 1: Add `"mail": {"Mail.Send"}` to `outlookFeatures` and preserve existing granted scopes on incremental connect and complete**
- [ ] **Step 2: Gate `mailboxCalendar` and `personalMailbox` on their respective feature scopes**
- [ ] **Step 3: Implement `personalMailbox` (`Status` and `Send` with delegated token, CAE claims challenge handling, MIME `sendMail` and Bcc draft dance)**
- [ ] **Step 4: Add unit and integration tests in `apps/server/internal/entra/` and commit signed**

---

### Task 4: Communications Backend Implementation

**Files:**
- Modify: `apps/server/internal/mail/outbound.go`
- Modify: `apps/server/internal/mail/mail_test.go`
- Modify: `apps/server/internal/communications/contractscalls.go`
- Modify: `apps/server/internal/communications/conversations.go`
- Modify: `apps/server/internal/communications/conversations_create.go`
- Modify: `apps/server/internal/communications/conversations_reply.go`
- Create: `apps/server/internal/communications/conversations_retry.go`
- Modify: `apps/server/internal/communications/outbox.go`
- Create: `apps/server/internal/communications/personal_sending_test.go`

- [ ] **Step 1: Support `ReplyToName` on `mail.Outbound` and test RFC 5322 `Reply-To` formatting**
- [ ] **Step 2: Add `personalMailStatus` and `personalMailboxSend` in `contractscalls.go` (enforcing the no-tx lock rule)**
- [ ] **Step 3: Implement `GetCommunicationsPersonalSender`, `sendFromPersonal` on `PostCommunicationsConversations` and `PostCommunicationsConversationsByIdReply`, message sender/delivery error fields on `GetCommunicationsConversationsById`, and `PostCommunicationsConversationsByIdMessagesByMessageIdRetry`**
- [ ] **Step 4: Update `outbox.go` to set `From` (personal) + `Reply-To` (channel), dispatch via `PersonalMailbox.Send`, fail immediately with `connection_needed` on `ErrConnectionNeeded`, and hold on `ErrMailboxUnavailable`**
- [ ] **Step 5: Run `go test ./internal/mail/... ./internal/communications/...` and commit signed**

---

### Task 5: Frontend Implementation (`entra-ui`, `communications-ui`, `host`)

**Files:**
- Modify: `apps/entra/frontend/src/i18n.ts`
- Modify: `apps/entra/frontend/src/components/outlook-connection.tsx`
- Modify: `apps/entra/frontend/src/components/outlook-connection.test.tsx`
- Modify: `apps/communications/frontend/src/catalog.ts`
- Modify: `apps/communications/frontend/src/api/conversations.ts`
- Modify: `apps/communications/frontend/src/pages/inbox.tsx`
- Modify: `apps/communications/frontend/src/components/ConversationThread.tsx`
- Modify: `apps/communications/frontend/src/components/ConversationComposer.tsx`
- Modify: `apps/communications/frontend/src/pages/inbox.test.tsx`
- Modify: `apps/host/frontend/src/routes/communications/inbox.tsx`
- Modify: `apps/*/frontend/src/api-schema.d.ts` (via `bun run gen:client`)

- [ ] **Step 1: Run `bun run gen:client` to regenerate OpenAPI TypeScript schemas**
- [ ] **Step 2: Update `@vantigo/entra-ui` (`outlook.feature_mail`, `outlookConnectDescription`, `OutlookConnectButton` incremental feature check, tests in `en` and `nb`)**
- [ ] **Step 3: Update `@vantigo/communications-ui` and `host` (`connectionButton` slot on `InboxPage`, new conversation modal + reply composer personal sending toggle, thread personal sender display + `connection_needed` retry, tests in `en` and `nb`)**
- [ ] **Step 4: Run `bun run test` and `bun run typecheck` and commit signed**

---

### Task 6: Documentation & Verification

**Files:**
- Modify: `docs/reference/entra.md`
- Modify: `docs/reference/communications.md`
- Modify: `docs/reference/module-boundaries.md`
- Modify: `docs/site/docs/en/admin/entra.md`
- Modify: `docs/site/docs/nb/admin/entra.md`
- Modify: `docs/site/docs/en/user/connections.md`
- Modify: `docs/site/docs/nb/user/connections.md`
- Modify: `docs/site/docs/en/user/communications.md`
- Modify: `docs/site/docs/nb/user/communications.md`
- Modify: `ROADMAP.md`
- Modify: `docs/superpowers/specs/2026-10-29-outlook-connection-and-mail-design.md`

- [ ] **Step 1: Update reference docs, admin and user guides (`en` and `nb`), `ROADMAP.md`, and the Delivery F as-built section in the design spec**
- [ ] **Step 2: Run `bun run tools/docs/check-coverage.ts`, `gofmt -l apps/server`, `git diff --exit-code -- '**/api-schema.d.ts'`, and full Go + frontend test suites**
- [ ] **Step 3: Commit signed and report summary to parent agent**
