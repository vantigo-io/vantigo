# Brief G — Sending from one's own Gmail address

You are implementing delivery **G** of the design
`docs/superpowers/specs/2026-11-03-google-workspace-design.md` (decision **G35**, on
G3 and G5 from delivery 0). Read that file first, in full, then this brief, then the
Outlook design's M20 and its Q5–Q7. You work alone, from a cold start, in this
repository. Model: Claude Sonnet. Deliveries 0–D and F are on `main`.

## The outcome

In a conversation on any channel, a person whose Google connection holds the **Gmail**
feature chooses **Send from my address**: the message leaves from their own Gmail
address, lands in their own Sent, and carries Reply-To the channel, so the customer's
answer arrives in the channel's mailbox and threads onto the conversation. A person not
yet consenting to Gmail is offered **Connect Google** for the mail feature from the
composer. When their connection needs reconnecting at send time, the message fails at
once with `connection_needed` — never sent from the channel instead — and they retry
after reconnecting. Communications does not change: delivery 0 already picks the
author's provider and records it on the message.

## Read before you plan

- The Outlook design's M20 and Q5–Q7
  (`docs/superpowers/specs/2026-10-29-outlook-connection-and-mail-design.md`).
- `apps/server/internal/contracts/personal_mailbox.go` (with delivery 0's `Key`,
  `DisplayName`, `SentMessage`).
- Entra's `personal_mailbox.go` and its tests; Entra's `outlookFeatures` for how the
  `mail` feature joins a live connection.
- Delivery D's connect (features, the union of scopes, the stored `scope`) and F's
  upload send and Message-ID read-back.
- Communications' consumer — read, do not change: `contractscalls.go`
  (`personalMailStatus`, `personalMailboxSend`), `conversations_create.go`,
  `conversations_reply.go`, `conversations_retry.go`, `outbox.go`
  (`sendThroughPersonalMailbox`).
- Docs: `en/user/communications.md` (*Send from my address*), `en/user/connections.md`.

## Build

1. **`PersonalMailbox` (G35)** under `"google"`, `DisplayName() "Gmail"`: `Status` for
   the caller (switch, certain account, `active` connection with `gmail.send`, the
   reasons of M20, `Address` the primary email, `Provider` and `ProviderDisplayName`);
   `Send` on the person's token through the upload endpoint, answering `SentMessage`
   with the thread id and the Message-ID read back (needs `gmail.send` only — the
   metadata read uses the same token; if Google refuses it with `gmail.send` alone,
   answer `SentMessage{ThreadID}` and record the refusal as U6's finding);
   `ConnectionNeededError` for every connection case, `consent_missing` without moving
   the row when only Gmail is missing; `ErrMailTooLarge` above
   `MaxPersonalMessageBytes`; `ErrMailboxDenied` on a refused recipient;
   `ErrMailboxUnavailable` on 5xx or throttling past the retry.
2. **The `mail` feature** offered by `POST /google/connect` (`feature: "mail"`),
   requesting the union with the live row's features, storing the answer as D does;
   `GoogleConnectButton` shows the button when `mail` is not among the features.
3. **Tests**: through Communications with Google's provider and a fake tenant — a new
   conversation and a reply sent personally, Reply-To the channel, the Message-ID
   recorded, the customer's reply to the channel threading back by it; a reconnect
   needed failing the job at once and the retry after reconnecting; an Entra and a
   Google person in one installation each sending through their own provider.
4. **Docs**, then `go generate ./...` and the regenerated assistant docs.

## The documentation (same commit series)

- `en/` and `nb/`: `user/communications.md` (Send from my Gmail address, Reply-To the
  channel, what happens when the connection needs reconnecting), `user/connections.md`
  (the Gmail feature, connecting it), `admin/google-workspace.md` (personal sending:
  what it needs, the scope, what the person sees).
- `reference/google.md` (`PersonalMailbox`), `reference/communications.md` (Google as a
  personal mailbox provider).

## Do not

Change Communications · fall back to the channel's address · raise the 3 MB personal
budget · use domain-wide delegation for a person's send · add a migration.

## How to work

- Worktree on `feat/google-personal-sending` cut from the previous delivery's branch (the README's stack);
  `bun install --frozen-lockfile` first.
- **Plan first**: `docs/superpowers/plans/2026-11-03-google-personal-sending.md`,
  committed alone as `docs(google): the plan for personal sending (Google G)`.
- No migration.
- Commits: `feat(google): … (Google G)`, `feat(google-ui): …`, `docs(…): …`,
  `chore(assistant): regenerate the embedded documentation`. Signed.
- **No `Docs-Impact: none` trailer** on any commit.
- Gates: `mise run server:check`, `mise run server:test`, `mise run frontend:check`,
  `mise run docs:check`. No merge of `main`; the starting session pushes and opens the pull request.

## Report

Branch and `git log --oneline main..`; the gates' output; the plan's path; departures
from G35; U6 and U9 as the tenant answered; and the manual check: the Gmail feature
connected from the composer, a reply sent from the person's address and seen in their
Sent, the customer's answer arriving in the channel and threading back.
