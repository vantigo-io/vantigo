# Brief E — Calendar sync with Google Calendar

You are implementing delivery **E** of the design
`docs/superpowers/specs/2026-11-03-google-workspace-design.md` (decisions **G30–G32**).
Read that file first, in full, then this brief, then the calendar Outlook sync design's
O4, O8–O11 and the Outlook design's M9. You work alone, from a cold start, in this
repository. Model: Claude Opus. Deliveries 0–D are on `main`.

## The outcome

A person with a Google account in Staff and a connected Google connection turns on **Sync
with Google Calendar** in the calendar's settings. Their appointments and the items they
chose appear in their Google Calendar (or a *Vantigo* calendar), their busy and
out-of-office time in Google shows in Vantigo as hatched blocks, edits to the copies in
Google come back under the calendar's write-back rules, and a change in Google makes the
sync due at once through a push channel when `PUBLIC_BASE_URL` is set. The calendar
module itself does not change: it already picks the provider by the person's account
(delivery 0).

## Read before you plan

- The calendar Outlook sync design (`docs/superpowers/specs/2026-10-27-calendar-outlook-sync-design.md`)
  O4, O8, O9, O10, O11, with their *as built* notes; the Outlook design's M9.
- `apps/server/internal/contracts/mailbox_calendar.go` — every method's comment is the
  specification you implement.
- Entra's implementation: `entra/{mailbox.go,mailbox_delta.go,mailbox_writeback.go,mailbox_notifications.go,mailbox_notifications_endpoint.go}`
  and their tests; `mig/00085`, `00093`.
- The calendar's consumer: `calendar/{mailbox_sync.go,mailbox_push.go,mailbox_busy.go,mailbox_writeback.go,mailbox_worker.go}`
  — read, do not change.
- Delivery D's connection and token code; `internal/security/publichost.go:66-78`.
- Docs: `en/user/calendar.md` (*Sync with …*), `en/admin/calendar.md`,
  `en/admin/entra.md` (*Calendar sync*, *Change notifications*),
  `en/admin/installation.md`.

## Build

1. **Migration `00115_google_calendar.sql`**: `google.calendars(google_user_id,
   calendar_id, created_at)`, `google.calendar_channels(google_user_id PRIMARY KEY,
   channel_id, resource_id, token_hash bytea, expires_at, granted_seconds, notified_at,
   created_at)`, `connection.marker varchar(32) NOT NULL` filled once with random hex.
2. **`MailboxCalendar` (G30)** under `"google"`, `DisplayName() "Google Calendar"`,
   every call through the person's connection with the `calendar` feature
   (`ConnectionNeededError` with `consent_missing` when the feature is absent, without
   moving the row); the call table in G30; the batch endpoint for `Apply`, `Marked` and
   `Events` (50 a batch); the marker in `extendedProperties.private`; `etag` as the change
   key; `ShowAs` as G30 reads it; the error mapping; `quotaUser`.
3. **The delta (G31)**: the windowed first list with `singleEvents=true` and
   `showDeleted=true`, the `g1.` link with the mailbox and window, the sync token, 410 →
   `ErrMailboxDeltaExpired`, the window enforced by the provider, and the no-token
   fallback (each continuation expired, logged once per mailbox).
4. **Notifications (G32)**: `Watch` (token hashed, `PUBLIC_BASE_URL` and
   `CALENDAR_MAILBOX_NOTIFICATIONS_ENABLED` honoured, renewal at min(3 days, half the
   granted life)), `Unwatch` (`channels.stop`), `Notified`; the anonymous
   `POST /google/calendar-notifications` (headers only, constant-time hash compare,
   204 always, 600 a minute), allowlisted in `publichost.go`; `Connections` from
   `google.connections.changed_at`.
5. **Cleanup and closing**: the removals served by a closing row while it holds a token;
   `ErrMailboxGone` once none can; the sweep (D) deletes a closing row only once it holds
   no channel row.
6. **`googletest`**: calendars insert/delete, events insert/patch/delete/get/list with
   sync tokens, pages, 410, `privateExtendedProperty`, the batch endpoint, `events.watch`
   with an expiry the test sets, `channels.stop`.
7. **Tests** through the calendar's own worker with Google's provider and a fake tenant:
   a push, a busy read, a write-back taken and one put back, the delta restarted on 410,
   the fallback without a token, a notification making the sync due, a forged token
   ignored, a disconnect cleaning up, a leaver's copies left.
8. **Docs**, then `go generate ./...` and the regenerated assistant docs.

## The documentation (same commit series)

- `en/` and `nb/`: `user/calendar.md` (syncing with Google Calendar, the busy kinds as
  they read for Google, the *own* calendar's availability), `admin/calendar.md` (Google
  as a provider; notifications need `PUBLIC_BASE_URL`), `admin/google-workspace.md`
  (*Calendar sync*: on each person's connection, no domain-wide calendar access,
  verifying with a connected person, change notifications, a leaver's copies left in
  the suspended account), `admin/installation.md` (`PUBLIC_BASE_URL` serves Google's
  notifications).
- `reference/calendar.md` (Google as a `MailboxCalendar`), `reference/google.md` (the
  call table, the link, the marker, the channels, the endpoint).

## Do not

Change the calendar module's behaviour · use DWD for any calendar call · ask for
`calendar` (full) scope · read an event's content anywhere but `Events` · log an id, a
title or Google's words · treat a notification as more than a nudge.

## How to work

- Worktree on `feat/google-calendar` cut from the previous delivery's branch (the README's stack);
  `bun install --frozen-lockfile` first.
- **Plan first**: `docs/superpowers/plans/2026-11-03-google-calendar.md`, committed alone
  as `docs(google): the plan for calendar sync (Google E)`.
- The migration takes the next free number (expected 00115).
- Commits: `feat(google): … (Google E)`, `docs(…): …`, `chore(assistant): regenerate the
  embedded documentation`. Signed.
- **No `Docs-Impact: none` trailer** on any commit.
- Gates: `mise run server:check`, `mise run server:test`, `mise run frontend:check`,
  `mise run docs:check`. No merge of `main`; the starting session pushes and opens the pull request.

## Report

Branch and `git log --oneline main..`; the gates' output; the plan's path; the migration
number; departures from G30–G32; U5, U7, U8 and U18 as the tenant answered; and the
manual check: sync on, an appointment in Google Calendar, an out-of-office in Google shown
in Vantigo, a copy moved in Google and taken back, a notification received.
