# Brief F — Gmail mail channels

You are implementing delivery **F** of the design
`docs/superpowers/specs/2026-11-03-google-workspace-design.md` (decisions **G33–G34**,
on G4 and G5 from delivery 0). Read that file first, in full, then this brief, then the
Outlook design's M10–M17. You work alone, from a cold start, in this repository. Model:
Claude Opus. Deliveries 0–D are on `main` (E need not be).

## The outcome

An Owner turns on **Mail channels** on the Google Workspace page's **Calendar and Gmail**
tab, delegates the two Gmail scopes the page now lists, and tests. In Communications, an
admin adds a channel, picks **Google Workspace** as the provider and types `post@firma.no`,
a licensed mailbox. Vantigo sends from it through Gmail, polls its inbox, imports new
mail as conversations threaded by Gmail's thread, and on close marks the messages read or
moves them under a label. A group's address is refused with a sentence saying why; a
person's own mailbox is refused as Microsoft 365 refuses one. The channels page shows the
existing *reaches every mailbox* warning, because domain-wide delegation does.

## Read before you plan

- The Outlook design's M10–M17 and Q4, with C–E's *as built* notes.
- `apps/server/internal/contracts/channel_mailbox.go` — the specification you implement —
  and delivery 0's `SentMessage`.
- Entra's implementation: `entra/{channel_mailbox.go,channel_mailbox_inbound.go,channel_mailbox_marks.go,mail_send.go}`
  and their tests, `mig/00090`.
- Communications' consumer — read, do not change: `channel_providers.go`, `channels.go`,
  `mailbox_sync.go`, `inbound_import.go`, `inbound_threading.go`, `mailbox_marks.go`,
  `outbox.go`.
- Delivery A's client (the upload host path), the test's scope list (G15).
- Docs: `en/user/communications.md` (*Set up a channel*), `en/admin/entra.md` (*Mail
  channels*), `en/reference/communications.md`.

## Build

1. **Migration `00116_google_mail_channels.sql`**: `connection.mail_channels boolean NOT
   NULL DEFAULT false`, `google.channel_mailboxes(google_user_id PRIMARY KEY, address,
   checked_at)`.
2. **The switch and scopes (G33)**: `PUT /google/connection/mail-channels` (Owner
   policy); `gmail.modify` and `gmail.send` join the scopes to delegate while on; the
   test checks them with a token for a channel mailbox when one exists, else for
   `admin_subject`; a Gmail scope granted while off is unexpected.
3. **`ChannelMailbox` (G34)** under `"google"`, `DisplayName() "Google Workspace"`:
   `Status` with `MailboxTenantWide`; `Check` (group → `group_address`, unknown,
   suspended or unlicensed → `ErrMailboxGone`, a Staff-managed person's account →
   `Shared` false, an alias resolved); `Forget`; `MaxMessage` 25,000,000; `Send` through
   `/upload/gmail/v1/users/{id}/messages/send?uploadType=media` answering `SentMessage`
   with the Message-ID read back; `Delta` with the profile's history id, the first
   `messages.list`, `history.list` continuations, the metadata read per new item, the
   `g1.` link, 404/400 → `ErrMailboxDeltaExpired`; `Raw` (`format=raw`, capped);
   `Mark` and `EnsureFolder` with labels; `Watch`/`Unwatch` nil, `Notified` nothing.
   Every call impersonating the mailbox, `quotaUser` set.
4. **Errors**: every shape a suspended or unlicensed mailbox answers → `ErrMailboxGone`
   (U17); 403 on the scope → `ErrMailboxDenied`; 429 retried once.
5. **`googletest`**: Gmail `getProfile`, `messages.list`, `messages.get` (metadata,
   raw), `history.list` with stale-id 404, `messages.modify`, `labels.list/create`, the
   upload send answering an id, a thread id and — switchable — a replaced Message-ID;
   Directory `groups.get` and `users.get` for `Check`.
6. **Tests through Communications' worker and outbox** with Google's provider and a fake
   tenant: a channel created (and refused for a group, a person's mailbox, an unknown
   address), a send whose replacement Message-ID is recorded, an inbound reply threading
   onto that conversation by `sent_message_id` and by the thread, a too-large message, a
   delta restarted, read and move marks.
7. **The tab**: the switch with its status and the two scopes to add.
8. **Docs**, then `go generate ./...` and the regenerated assistant docs.

## The documentation (same commit series)

- `en/` and `nb/`: `user/communications.md` (adding a Google Workspace channel: a
  licensed mailbox; why a group is refused and Collaborative Inbox is not supported;
  the 25 MB budget; marks as Gmail labels; mail is fetched every poll, not pushed),
  `admin/google-workspace.md` (*Mail channels*: the switch, the scopes and that they
  reach every mailbox in the domain, the licensed mailbox and its cost, verifying with a
  channel, turning it off, what can go wrong), `user/google-workspace.md` (the tab).
- `reference/communications.md` (Google as a mailbox provider), `reference/google.md`
  (the call table, the link, the labels, the limits).

## Do not

Change Communications · add Pub/Sub or any push (Q7) · ask for `https://mail.google.com/`
or `gmail.readonly` · read a mailbox that is not a channel · accept a group's address ·
log an address, a subject or Google's words.

## How to work

- Worktree on `feat/google-mail-channels` cut from the previous delivery's branch (the README's stack);
  `bun install --frozen-lockfile` first.
- **Plan first**: `docs/superpowers/plans/2026-11-03-google-mail-channels.md`, committed
  alone as `docs(google): the plan for Gmail channels (Google F)`.
- The migration takes the next free number (expected 00116; renumber if E took it).
- Commits: `feat(google): … (Google F)`, `feat(google-ui): …`, `docs(…): …`,
  `chore(assistant): regenerate the embedded documentation`. Signed.
- **No `Docs-Impact: none` trailer** on any commit.
- Gates: `mise run server:check`, `mise run server:test`, `mise run frontend:check`,
  `mise run docs:check`. No merge of `main`; the starting session pushes and opens the pull request.

## Report

Branch and `git log --oneline main..`; the gates' output; the plan's path; the migration
number; departures from G33–G34; U6, U10, U15, U17 as the tenant answered; and the
manual check: a channel on `post@`, a message sent and seen in its Sent, a reply received
threading onto the conversation, a close moving it under the label, a group refused.
