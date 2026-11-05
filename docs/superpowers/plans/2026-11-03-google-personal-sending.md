# Google Workspace, delivery G — sending from one's own Gmail address: implementation plan

> **For agentic workers:** implement this plan task by task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** in a conversation on any channel, a person whose Google connection holds the
**Gmail** feature checks **Send from my Gmail address**: the message leaves from their own
Gmail address, lands in their own **Sent**, and carries Reply-To the channel, so the
customer's answer arrives in the channel's mailbox and threads onto the conversation. A
person who has not consented to Gmail is offered **Connect Google** for the mail feature
from the composer. When their connection needs reconnecting at send time, the message
fails at once with `connection_needed` — never sent from the channel instead — and they
retry after reconnecting. Communications does not change: delivery 0 already picks the
author's provider and records it on the message.

**Architecture:** `google` fills `contracts.PersonalMailbox` under `Key() = "google"`,
`DisplayName() = "Gmail"` — Entra's `personal_mailbox.go` reshaped for Google, in
`srv/google/personal_mailbox.go`. `Status` reads the switch and the web client
(`personalOffered`), the caller's certain account (G27, the same check `POST /connect`
makes, now a package function both share) and the caller's live row in
`google.connections`, with no network. `Send` takes the person's token for the `mail`
feature from D's `personalTokens` (the check on use, the revision-guarded refresh,
`consent_missing` without moving the row) and uploads the MIME through F's upload send,
which becomes one function both the channel and the person call. The `mail` feature
(`gmail.send`) is D's already, offered by `POST /google/connect` with the union of the
held features' scopes; the composer's `connection.button` slot already asks Google's
button for `mail`.

**Spec:** [`docs/superpowers/specs/2026-11-03-google-workspace-design.md`](../specs/2026-11-03-google-workspace-design.md), G35 (on G3, G5, G26–G28, G34), P4; the Outlook connection design's M20 and Q5–Q7.
**Brief:** [`2026-11-03-google-workspace-briefs/g-personal-sending.md`](2026-11-03-google-workspace-briefs/g-personal-sending.md).

## Plan readings

Checked against `6f3c3581` (delivery F's branch, `feat/google-mail-channels`).

1. **The `mail` feature is D's.** `personalFeatures["mail"] = {gmail.send}`
   (`personal.go`), `POST /google/connect` accepts `feature: "mail"` (the contract's enum
   holds it), `connectScopes` asks the held features again beside it (U9), the callback
   refuses `consent_missing` when the answer lacks it, and `GET /google/connection/me`
   answers `features` from the stored scopes. `GoogleConnectButton` shows *Connect
   Google* while `mail` is not among the features. Nothing changes there; G tests the
   whole flow for `mail`.
2. **The composer** (`ConversationComposer.tsx`, `ConversationThread.tsx`,
   `pages/inbox.tsx`) already renders `connection.button` with `feature: "mail"`, and its
   label is `sendFromPersonalVia` with `PersonalMailStatus.ProviderDisplayName` — *Send
   from my Gmail address (…)* once Google answers `Gmail`. Nothing changes in
   Communications.
3. **D's token** — `personalTokens.issue(gid, "mail", false)` — answers
   `*ConnectionNeededError` for a missing row (`not_connected`), a closing one or the
   switch off (`closing`), a reconnect (its code), the account no longer usable (the row
   closed, `closing`) and a row without `gmail.send` (`consent_missing`, the row
   untouched); `errWebClient` for the web client not set up or refused; the token
   endpoint's 5xx, 429 or no answer as `errGoogleUnreachable`.
4. **F's send** (`channelMailbox.Send`) posts to `gmailSendURL` with `noRetry`,
   `sizedBudget`, `shownAs`, then reads the Message-ID back with
   `messages.get(format=metadata)` on its own context. The read-back is made with the
   channel's **`gmail.modify`** token. Google's reference for `users.messages.get` lists
   `mail.google.com`, `gmail.modify`, `gmail.readonly` and `gmail.metadata` — not
   `gmail.send` — and the fake answers 403 `insufficientPermissions` without
   `gmail.modify`, as Google does.
5. **The fake's Gmail** knows a token's mailbox only by an address (a delegated token's
   subject); a person's token (D) has the account's Directory id as its subject. The
   fake learns to map a Directory id to the user's primary address.
6. **The outbox** records `ThreadID` as `providerThreadId` and a differing `MessageID` as
   `sent_message_id` for a personal send too (`complete`, `sentMessageRecord`). A personal
   send's thread is the *person's* Gmail thread, which the channel's mailbox never sees;
   threading looks it up within the channel only (`findThread`), so it is inert, as
   Entra's zero value is.
7. **Errors the outbox reads**: `ErrConnectionNeeded` → terminal `connection_needed`;
   `ErrMailboxDenied`, `ErrMailTooLarge`, `ErrBccUnsupported` terminal;
   `ErrMailboxUnavailable` held, not spending an attempt; anything else retried on the
   backoff.

## Decisions taken in planning

- **One upload send.** F's upload becomes `gmailUpload(ctx, c, src, gid, mime)` in
  `channel_mailbox.go`, answering Gmail's `{id, threadId}`; `channelMailbox.Send` and the
  personal `Send` both call it. The read-back stays F's for the channel.
- **The personal read-back is made only when the token can read.** The `mail` feature asks
  `gmail.send` alone (G26), which Google's reference does not admit for `messages.get`
  (reading 4). A call refused on every send costs quota and a refusal in the log for
  nothing, so `Send` reads the Message-ID back only when the connection's granted scopes
  hold one that reads metadata (`gmail.metadata`, `gmail.readonly`, `gmail.modify`,
  `https://mail.google.com/`) — never with the `mail` feature as designed — and otherwise
  answers `SentMessage{ThreadID}`. **This departs from the brief's "try it, record the
  refusal"**: the refusal is recorded from Google's reference rather than from the tenant
  (U6's finding for G). The consequence, in `R/google.md` and `U/communications.md`:
  if Gmail replaces the Message-ID on `messages.send` (U6), a customer's answer to a
  **new** conversation sent from a person's Gmail does not thread by its In-Reply-To and
  starts a new conversation; an answer to a personal **reply** still threads by its
  References, which carry the customer's own earlier Message-ID. If the tenant shows Gmail
  keeps ours, both thread. Asking `gmail.metadata` with the `mail` feature would close the
  gap at the price of reading every message's headers in the person's mailbox — the
  owner's call, not this delivery's.
- **Status** answers, in order: `unavailable` without a principal or while
  `personalOffered` is false (the switch off, or the web client not ready);
  `no_account` without a certain account; then, with `Mailbox` the Directory id and
  `Address` the primary email, `not_connected` for no live row, a row of another account
  or a closing one; `reconnect` with the row's code; `not_connected` when the row lacks
  `gmail.send` (M20: a calendar-only connection); else available.
- **Send** checks with no call what the token cannot: a principal, the size
  (`MaxPersonalMessageBytes`, P4), the footing (`personalOffered`, else
  `ConnectionNeeded{closing}` as Entra answers the switch off), the certain account (else
  `closing`), the caller's own live row being that account's (else `not_connected`), and
  that every blind copy is in the MIME's Bcc header (Gmail delivers the header's, F) —
  else `ErrBccUnsupported`. Then the token for `mail` and the upload.
- **Send's refusals**: a `*ConnectionNeededError` as it is; `errWebClient` →
  `ErrMailboxUnavailable`; a 401 after the client's fresh token → the row to `reconnect`
  (`sign_in_again`) at the token's revision through D's guarded `reconnect`, which
  answers `ConnectionNeeded` (the calendar's `unauthorized`, moved onto `personalTokens`
  so both use it); a 400 (Gmail refusing a recipient or the message) or a 403 that is not
  a usage limit → `ErrMailboxDenied`; a 413 → `ErrMailTooLarge`; `errThrottled`,
  `errGoogleUnreachable` and a 5xx → `ErrMailboxUnavailable`. Never a fallback, never a
  second provider.
- **Logs**: the person's Directory id and a refusal's status and reason, never an
  address, a token or Google's words (the client already logs so).
- **The tab**: the *Sending from Gmail is coming* card goes; Connection for people's intro
  says Calendar sync and sending from Gmail **use** the connection.
- **Mixed installation test**: Communications, customers and the real `google` module
  with a `FakePersonalMailbox` under `entra` — Entra's own `Send` is its package's
  (`entra/personal_mailbox_test.go`); composing the real Entra beside Google needs a
  second fake tenant behind one transport for no new evidence.
- No migration, no OpenAPI change.

## Tasks

- [ ] **1. Plan** — this file, `docs(google): the plan for personal sending (Google G)`.
- [ ] **2. The provider** — `feat(google): sending from one's own Gmail address (Google G)`:
  `personal_mailbox.go`; `gmailUpload` shared with the channel; `certainAccountOf` shared
  with the connect; `personalTokens.unauthorized` shared with the calendar;
  `PersonalMailbox: newPersonalMailbox` in `module.go`; the fake's Gmail answering a
  person's token; `personal_mailbox_test.go` (status reasons, the mail feature connected
  through the flow, send and Sent, Reply-To kept, no read-back, read-back when the scopes
  allow, size, Bcc, `consent_missing` without moving the row, reconnect, 401 → reconnect,
  400/403/413/5xx/429 mapped, nothing logged, a pool of one).
- [ ] **3. Through Communications** — `test(integration): personal sending through Gmail (Google G)`:
  a new conversation and a reply sent personally, Reply-To the channel, in the person's
  Sent, the customer's answer threading back (Gmail keeping the Message-ID; and a reply's
  answer threading by References when Gmail replaces it); a reconnect failing the job at
  once with `connection_needed` and the retry after reconnecting; an `entra` person and a
  Google person in one installation each through their own provider.
- [ ] **4. The tab** — `feat(google-ui): sending from Gmail is here (Google G)`, both
  languages, the tab's test.
- [ ] **5. Documentation** — `docs(google): sending from one's own Gmail address (Google G)`:
  `en`/`nb` `user/communications.md` (Send from my Gmail address, Reply-To the channel,
  reconnecting, the threading caveat), `user/connections.md` (the Gmail feature,
  connecting it from the composer), `user/google-workspace.md` (the tab),
  `admin/google-workspace.md` (personal sending: what it needs, the scope, what the person
  sees, verifying); `reference/google.md` (`PersonalMailbox`, U6 and U9 for G),
  `reference/communications.md` (Google as a personal mailbox provider).
- [ ] **6. Embedded docs** — `go generate ./...`, `chore(assistant): regenerate the embedded documentation`.
- [ ] **7. Gates** — `mise run server:check`, `mise run server:test`,
  `mise run frontend:check`, `mise run docs:check`,
  `bun run tools/docs/check-coverage.ts --base feat/google-mail-channels --head HEAD`.

## Unverified items this delivery depends on

| # | Item | How G is safe before it is settled |
| --- | --- | --- |
| U6 | Gmail replaces a supplied Message-ID on `messages.send` | The personal send cannot read it back with `gmail.send`; if Gmail keeps ours, threading is unchanged; if it replaces it, an answer to a new personal conversation starts a new conversation, and replies still thread by References. Documented, never a wrong thread |
| U9 | Incremental consent answers the union in `scope` | The `mail` connect asks the held features again; only the answered scope is stored; a missing `gmail.send` is `consent_missing`, never assumed |

## Manual check in the tenant

Connect **Gmail** from the composer's *Connect Google*; send a reply with **Send from my
Gmail address**; see it in the person's Gmail **Sent** with Reply-To the channel; answer it
as the customer and see the answer arrive in the channel and thread onto the
conversation; note whether Gmail kept the Message-ID (U6) and record it in `R/google.md`.
