# Google Workspace, delivery F — Gmail mail channels: implementation plan

> **For agentic workers:** implement this plan task by task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** an Owner turns on **Mail channels** on the Google Workspace page's **Calendar
and Gmail** tab, delegates the two Gmail scopes the page now lists, and tests. In
Communications an admin adds a channel, picks **Google Workspace** as its provider and
types `post@firma.no`, a licensed mailbox. Vantigo sends from it through Gmail, polls its
inbox, imports new mail as conversations threaded by Gmail's thread, and on close marks
the messages read or moves them under a label. A group's address is refused with a
sentence saying why; a person's own mailbox is refused as Microsoft 365 refuses one. The
channels page shows the existing *reaches every mailbox* warning, because domain-wide
delegation does. Communications does not change: delivery 0 made its channels
provider-neutral.

**Architecture:** `google` fills `contracts.ChannelMailbox` under `Key() = "google"`,
Entra's `channel_mailbox*.go` reshaped for Gmail: `srv/google/channel_mailbox.go` (the
status, the footing of a call, `Check`, `Forget`, `Send`, the refusal mapping),
`channel_mailbox_inbound.go` (`Delta` over `messages.list` and `history.list` with the
`g1.` link, `Raw`) and `channel_mailbox_marks.go` (`Mark`, `EnsureFolder` with labels).
Every Gmail call impersonates the channel's mailbox through domain-wide delegation —
`keyring.tokenSource(scope, address)` — with `quotaUser` the mailbox's Directory id; the
Directory reads of `Check` impersonate `admin_subject`. The switch is
`google.connection.mail_channels`, the mailboxes `google.channel_mailboxes` (00129); the
Owner's `PUT /google/connection/mail-channels` turns it on and off, and *Test connection*
probes the Gmail scopes while it is on.

**Spec:** [`docs/superpowers/specs/2026-11-03-google-workspace-design.md`](../specs/2026-11-03-google-workspace-design.md), G33–G34 (on G4, G5, G14, G15), P2, P4; Q6, Q7; the Outlook design's M10–M17.
**Brief:** [`2026-11-03-google-workspace-briefs/f-mail-channels.md`](2026-11-03-google-workspace-briefs/f-mail-channels.md).

## Plan readings

Checked against `5eb44be4` (`feat/google-calendar`, delivery E on `main`'s 0–D).

1. **The migration is `00129`** (first `00118`, renumbered after `main`'s 00117–00128). `main` holds 00114–00116 for Google and E took 00128;
   the design's and the brief's `00116` is stale. `srv/google/sqlc.yaml` follows.
2. **The slot is many-provider already** (delivery 0): `module.Module.ChannelMailbox`
   builds the module's, Compose and Workers collect it into `Deps.ChannelMailboxes`, and
   Communications picks `channelMailboxFor(channel.provider)`. `GET /channel-providers`
   lists every filled provider with its `DisplayName`, `MaxMessage`, status and the
   `tenant_wide` warning; `resolveSharedMailbox` answers `group_address` for
   `ErrMailboxGroup` and `personal_mailbox` for `Shared` false; the outbox records
   `SentMessage.ThreadID` as `providerThreadId` and a differing `MessageID` as
   `sent_message_id`, and threading matches both (G5). Nothing in Communications changes.
3. **Communications' calls**: the outbox renders the MIME with the `Bcc` header when there
   are blind copies (`mail.WithBcc`) and sends it under a 30-second timeout; the mailbox
   sync asks `Delta(mailbox, "inbox", link, since)`, imports each item not removed through
   `Raw(…, max)` (`COMMUNICATIONS_INBOUND_MAX_BYTES`, 25 MiB by default), restarts with
   `""` on `ErrMailboxDeltaExpired`, and calls `Watch` only while its own notifications are
   on; the marks run `EnsureFolder` once per move and `Mark` per message. `giveUp` logs a
   failed import's error: no error the provider answers may carry an address.
4. **The guarded client** (`client.go`) already lists `gmail.googleapis.com`; it reads an
   answer through a limit `do` takes, so `Raw` passes its own (the base64url of `max`
   plus room). It sends JSON or raw bytes (`requestBody`), so the upload's
   `message/rfc822` body needs no new path. It logs the URL's path: every Gmail path is
   `users/me/…` — the token's subject names the mailbox — and the Directory paths that
   carry the typed address are logged through `shownAs`.
5. **The keyring** (`token.go`) fetches a delegated token for any subject; an unknown or
   suspended subject is `errSubjectInvalid` and marks nothing, an undelegated scope
   `errNotDelegated`. The subject a Gmail call impersonates is the mailbox's primary
   address, recorded by `Check`: the JWT's `sub` is an address in Google's documentation.
6. **Test connection** (`test.go`) probes `neededScopes()` as `admin_subject` and the
   `unexpectedProbes`, which list `gmail.modify` and `gmail.send`. `neededScopes` becomes a
   function of the connection; `connectionBody`'s `scopesToDelegate` follows.
7. **The tab** (`calendar-gmail-tab.tsx`) holds Connection for people and a "coming"
   card; the Mail channels card replaces the coming card, and the coming sentence moves
   to what is still to come (sending from a person's Gmail, G).
8. **E's integration test** composes `calendar` and `google` in `internal/integration`;
   F's composes `communications` and `google` the same way, with Communications' own
   workers (`NewOutboxWorker`, `NewMailboxSyncWorker`) and Google's real provider over
   `googletest`.

## Decisions taken in planning

- **`Status`** (G34): `unavailable switch_off` while the switch is off; else the
  connection's status (`unconfigured`, `pending_delegation`, `failing` with its reason)
  as `unavailable`; else `not_tested` while the last test neither granted nor refused
  the two Gmail scopes (the switch turned on since), `not_delegated` while it refused
  either; else **available with `tenant_wide`**, always. No network.
- **The footing of a call**: `Status` available (`ErrMailboxUnavailable` otherwise), the
  mailbox a row of `google.channel_mailboxes` (`ErrMailboxUnknown` otherwise, no call),
  then the token for the scope as the row's address. Read on the pool; nothing is held
  across a Google call.
- **`Check`** (G34, P2): a bare addr-spec only (Entra's rule; `ErrMailboxUnknown` with no
  call). As `admin_subject`: `groups.get(address)` — found → `ErrMailboxGroup`; 404 (and
  400, which Google answers for a key that names no group) → on —; `users.get(address)` —
  404 → `ErrMailboxUnknown`, `suspended` or `archived` → `ErrMailboxGone`; an alias
  resolves to its user. A user `google.accounts` holds (an account Staff manages for a
  person) answers `Shared` false and nothing more: no call reaches a person's mailbox.
  Then `users.getProfile` as the mailbox, which proves the Gmail scope reaches it and the
  mailbox has Gmail; recorded `(google_user_id, address, checked_at)`. `Mailbox` is the
  Directory id; `Address` the primary email, lower case.
- **Refusals** (U17): the token endpoint refusing the mailbox as a subject, a 400
  `failedPrecondition` (Gmail not enabled for the user: no licence) and a 404 on the
  profile → `ErrMailboxGone`; a scope not delegated → `ErrMailboxUnavailable`
  (`not_delegated`, held, not a refusal: the delegation is the Owner's to mend); a 403
  that is not a usage limit → `ErrMailboxDenied`; a 413 → `ErrMailTooLarge`; the
  client's one wait on a 429 or a usage-limit 403, then a failure to retry. A 404 on a
  message → `ErrMailItemGone`.
- **`MaxMessage`** is 25,000,000 bytes (P4, U10).
- **`Send`**: refused unsent above `MaxMessage`, and when a blind copy is not in the
  MIME's `Bcc` header (`ErrBccUnsupported`: Gmail delivers the header's blind copies and
  strips it, so the header is the only way they travel). `POST
  https://gmail.googleapis.com/upload/gmail/v1/users/me/messages/send?uploadType=media`,
  `message/rfc822`, made once (no 5xx retry). The answer's `id` and `threadId`; then
  `messages.get(format=metadata, metadataHeaders=Message-ID)` for the Message-ID Gmail
  sent it with (U6). A failed read-back answers the thread alone and is logged by count:
  the message is sent, and a retry would send it twice.
- **`Delta`** (G34): `""` reads `getProfile`'s `historyId`, then `messages.list(labelIds=INBOX,
  q=after:<unix since>, maxResults=50)`, paged; its last page answers the history link,
  `Done`. A continuing link is `history.list(startHistoryId, labelId=INBOX,
  historyTypes=messageAdded,labelAdded,labelRemoved,messageDeleted, maxResults=50)`,
  paged under one start; the last page's `historyId` is the next start. A message added
  with `INBOX` or given it is an item; `INBOX` taken off or the message deleted is
  `Removed`, the last word per message winning. Each new item is completed by
  `messages.get(format=metadata, metadataHeaders=Message-ID)`: `MessageID`, `internalDate`
  as `ReceivedAt`, `threadId` as `ConversationID`; a 404 there is `Removed`. More than
  `MailDeltaPageMax` items carry over in the link's queue (`q`), answered before the next
  history page. The link is `g1.<base64url(json{m, h, p, l, a, q})>` (mailbox, start,
  page token, listing or history, the listing's `after`, the queue), at most 64 KiB; one
  not `g1.` or naming another mailbox is a failure, unsent. A 404, or a 400, on
  `history.list` (and a 400 on a listing's page token) → `ErrMailboxDeltaExpired` (U15).
- **`Raw`**: `messages.get(format=raw)` read through `4·⌈max/3⌉ + 64 KiB`; above it, or
  decoded above `max`, `ErrMailTooLarge`.
- **`Mark`**: read → `messages.modify(removeLabelIds=[UNREAD])`; category → the labels of
  those names (each found or created as `EnsureFolder` does) added; move → the label
  `EnsureFolder` answered added and `INBOX` removed.
- **`EnsureFolder(name)`**: a user label of that name (`labels.list`, compared ignoring
  case), else `labels.create {name, labelListVisibility: labelShow, messageListVisibility:
  show}`; a 409 re-reads. A Gmail label id is `Label_<digits>`; a system label is never
  answered.
- **`Watch`, `Unwatch`** nil and **`Notified`** none: v1 polls (Q7). *As built:* `Watch`
  answers the new `contracts.ErrWatchUnsupported` (the coordinator's decision, the as-built note
  under task 3), since a nil `Watch` made Communications wait its one-hour safety interval.
- **The switch** `PUT /google/connection/mail-channels {enabled}` (Owner policy, the
  Owner's rate limit), audited `google.mail_channels.switched`; answers the connection,
  which gains `mailChannels {enabled, status (off | ready | unavailable), reason?,
  mailboxes}`. Turning it off leaves the rows: a channel goes unavailable and Communications
  holds it; turning it on again needs no new Check.
- **The scopes** (G33): `neededScopes(conn)` adds `gmail.modify` and `gmail.send` while
  the switch is on; *Test connection* probes those two as the first channel mailbox's
  address when one is recorded, else as `admin_subject`, and stops probing them as
  unexpected; while off they stay among the unexpected probes.
- **Never words**: Check logs the mailbox's Directory id and whether it is shared; no
  address, subject or Google text reaches a log, an error or an event.

## Tasks

- [x] **1. Plan** — this file, `docs(google): the plan for Gmail channels (Google F)`.
- [x] **2. The switch** — `feat(google): the Mail channels switch and the Gmail scopes (Google F)`.
  `00129_google_mail_channels.sql` (`connection.mail_channels`, `google.channel_mailboxes`),
  `queries/channel_mailboxes.sql`, sqlc; `openapi/google.yaml` (the operation,
  `GoogleMailChannels`); `go generate`; the scopes in the test and the connection; tests:
  the switch, the scopes listed, the probe's subject, unexpected while off.
- [x] **3. The provider** — `feat(google): Gmail mail channels (Google F)`.
  `channel_mailbox.go`, `channel_mailbox_inbound.go`, `channel_mailbox_marks.go`;
  `ChannelMailbox` on the module; `googletest/gmail.go` (profile, list, get metadata and
  raw, history with a stale start, modify, labels, the upload send with a replaceable
  Message-ID; subjects impersonated by address); tests per call, the refusals, never
  words, a pool of one.
*As built besides:* `feat(communications): a mailbox provider with no push keeps the normal
poll (Google F)` — the docs pass found that a nil `Watch` puts a channel on
`mailboxSyncSafetyEvery` (an hour) while Communications' notifications are active.
`contracts.ErrWatchUnsupported` says a provider has no push; Communications keeps the
five-minute poll and logs nothing; Google answers it, Entra is unchanged. The one change to
Communications in this delivery, against the brief's "do not change Communications", on
the coordinator's decision.
- [x] **4. Through Communications** — `test(integration): Gmail channels through Communications' outbox and mailbox sync (Google F)`.
  A channel created (and refused for a group, a person's mailbox, an unknown address), a
  send whose replaced Message-ID is recorded, a reply threading by `sent_message_id` and
  by the thread, a too-large message, a delta restarted, read and move marks.
- [x] **5. The tab** — `feat(google-ui): the Mail channels card (Google F)`, both languages, tests.
- [x] **6. Documentation** — `docs(google): Gmail mail channels (Google F)`:
  `user/communications.md`, `admin/google-workspace.md`, `user/google-workspace.md` in
  `en` and `nb`; `reference/communications.md`, `reference/google.md`.
- [x] **7. Embedded docs** — `go generate ./...`, `chore(assistant): regenerate the embedded documentation`.
- [x] **8. Gates** — `mise run server:check`, `mise run server:test`,
  `mise run frontend:check` (its log read), `mise run docs:check`,
  `bun run tools/docs/check-coverage.ts --base feat/google-calendar --head HEAD`, serially.

## Unverified items this delivery depends on

- **U6** (Gmail replaces a supplied Message-ID): read back after the send; threading
  matches ours and Gmail's.
- **U10** (25 MB a message, 35 MB through the upload): `MaxMessage` is 25,000,000; a 413 is
  `ErrMailTooLarge`, terminal.
- **U15** (`history.list` with a stale id answers 404): 404 and 400 both restart.
- **U17** (what a suspended or unlicensed mailbox answers): every shape named above is
  `ErrMailboxGone`, never a retry loop.
- Besides: that the JWT's `sub` may be any licensed user's address (not an admin's) for
  the Gmail scopes, and that `groups.get` of a user's address answers 404 (400 handled the
  same).

All are checked in the test tenant before the merge (P5); `R/google.md` lists them.
