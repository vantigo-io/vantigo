# Follow-ups — design

Phase 4 of the Customers roadmap ("Light CRM"), delivery **C**: what happens next. A
manual timeline entry can carry a **follow-up** — a due date and, optionally, an assignee —
and be marked done; due and overdue follow-ups reach `/stats/attention` and a
**Follow-ups** page answers "what is on my plate". No task engine beyond that: no
priorities, no recurrence, no reminders by mail. Delivery D (own spec) is customer groups.

The same PR carries two things the user asked for after #117: the deprecated `role`
alias on the contact-association contract is **removed** (the API is not live; `title` is
the field), and the five open code-scanning alerts are fixed. Both are their own tasks.

## Decisions

### D1 — A follow-up is part of a manual timeline entry

`customers.customers_timeline_entries` (and its revisions table, so history stays
point-in-time) gain `follow_up_on date NULL`, `follow_up_assignee_user_id uuid NULL`,
`follow_up_done_at timestamptz NULL` (migration `00026`). Only a **manual, active** entry
carries one — an interaction ("called about the renewal") or a note is exactly where "call
back on Friday" belongs, and a generated event never asks anyone to do anything.

- `POST /customers/{id}/timeline` and `PUT …/timeline/{entryId}` gain
  `followUp?: {dueOn, assigneeUserId?} | null` — set, replace or clear (`null`) with the
  entry, under the entry's own `expectedRevision` as today. `dueOn` is a strict
  `yyyy-MM-dd` and **may be in the future** (unlike `occurredOn`); the assignee must exist
  and be active to be assigned (field error on `followUp.assigneeUserId`, worded as the
  owner's), and an assignee disabled afterwards keeps the follow-up (shown inactive).
  Clearing a follow-up also clears its done state.
- `POST …/timeline/{entryId}/follow-up/done` and `DELETE …/follow-up/done` mark it done /
  reopen it. Idempotent, **no expected revision** (a tick from a list must not conflict
  with an edit of the note), but each is a revision of the entry: `current_revision`
  bumps and a revision row records who ticked it. 409 "Timeline entry is immutable" when
  the entry is not manual/active; 404 when it has no follow-up.
- `TimelineResponse` (and the revision response) gain `followUp?: {dueOn,
  assignee?: {userId, displayName, active}, doneAt?}` — omitted when the entry carries
  none. Assignee names come from one batched directory call per page, outside any
  transaction, `Unknown user` for a vanished id (the owner's precedent).
- Permissions: the timeline's own — `customers:timeline-manage` to set, tick and reopen,
  `customers:timeline-view` to read. `GET /customers/assignable-users` is relaxed from
  `customers:update` to **`customers:view`**: it answers display names of active users,
  which every timeline reader already sees as entry authors, and a timeline writer must
  be able to pick an assignee without the customer-edit permission.

### D2 — Due and overdue follow-ups are attention

`/stats/attention` gains two types, computed from state each time it is asked, for open
follow-ups on non-archived customers **assigned to the caller or unassigned** (an
unassigned follow-up is everyone's until someone takes it): `followUpOverdue` (`dueOn` before
today, UTC) and `followUpDue` (`dueOn` = today). Id `<type>/<entryId>`, `entityId` = the
customer id (the host already links a customers item to `/customers/{entityId}`),
`title` = the customer's name, `occurredAt` = `dueOn` at midnight UTC — so an overdue
follow-up sorts by how overdue it is, not by when it was noticed. The host catalog gets
the two sentences (en + nb): "Follow-up overdue for {{name}}" / "Follow-up due today for
{{name}}". This is the first customers attention item that depends on the caller; the
endpoint reads the principal from the context as time's and expenses' items do.

### D3 — A Follow-ups page

`GET /customers/follow-ups` (`customers:timeline-view` + `customers:view`), paginated
(`page`, `pageSize`), filters `assignee=me|none|<uuid>` (default `me`) and
`state=open|overdue|done|all` (default `open`; `overdue` ⊂ `open`), optional `customerId`;
sorted `dueOn` ascending then entry id; each row `{entryId, customerId, customerName,
eventType, occurredOn, note (first 200 UTF-16 units), followUp}`. Archived customers'
follow-ups are excluded unless `state=done`.

Host: a **Follow-ups** nav entry under Customers (`/customers/follow-ups`, requires
`customers:timeline-view`), a route rendering the package's `FollowUpsPage` with the two
filters as URL search params, rows linking to the customer's timeline, and a **Done** tick
per row with `canManageTimeline`. The host passes `canManageTimeline`
(`customers:timeline-manage`) — a new capability prop that the customer page's timeline
also receives, so its Add/Edit/Delete controls and the new follow-up controls hide for a
reader (today the server alone enforced it and a reader saw buttons that 403'd).

### D4 — The timeline card

The entry form gains a **Follow-up** section: a due date (`DateInput`, may be future) and
an assignee picker — the owner picker generalised into a `UserPicker` fed by the same
assignable-users search, the current assignee kept in the options. Entries with a
follow-up show a line "Follow up {date} · {assignee}" (red and "overdue" when past, grey
and struck through when done, with who ticked it and when) and a **Done** / **Reopen**
control with `canManageTimeline`. The revisions view shows the follow-up fields per
revision.

### D5 — The contract break: `title` only

`role` is removed from `AttachCustomerContactRequest`, `CustomerContactRequest`,
`CustomerContactResponse` and `GetContactCustomersContactCustomerResponse`; `title` is the
field (request: optional; the title-or-role rule becomes **title-or-roles**; response:
nullable). `validateContactRole` and the alias handling go; generated contact events keep
`title` and drop `role` from new payloads (payload version bumps; old entries keep
theirs). The frontend's `titleOf` fallback goes. The frozen corpus is **not edited**: no
schema sets `additionalProperties: false`, so its recorded requests and responses that
still carry `role` validate against the new schemas (verified by the corpus test staying
green); `docs/customers.md`'s "why `role` is still on the wire" section is replaced by
one sentence saying it was removed while nothing was live.

### D6 — The code-scanning alerts

Five open CodeQL alerts, none in customers: the session cookie's `Secure` attribute
(`identity/cookies.go`), three integer conversions in the Argon2 PHC parser
(`identity/passwords.go:129`), and an allocation sized from `len(plaintext)`
(`secrets/secrets.go:124`). Fixed in their own commit with tests; the cookie fix keeps the
existing design honest (Secure always, documented) or reports a dismissal if no honest
code change exists.

## Out of scope

Recurring follow-ups, priorities, reminders by e-mail or notification (the attention list
and the page are the reminder), follow-ups on generated events, bulk reassignment, a
follow-up without a timeline entry, customer groups (delivery D).

## Testing

Backend through `modtest`: create/update with a follow-up (strict date, future allowed,
assignee validation, clear with `null` also clears done, revision conflict as today), done
and reopen (idempotent, revision bump, revision row's actor, 404 without a follow-up, 409
on a generated entry), attention items for the caller's and unassigned open follow-ups
only (overdue vs due today by UTC, none for done, none for archived customers, none for
another user's), the follow-ups list (each filter, paging, ordering, archived exclusion,
`assignee=me` from the principal), the assignable-users relaxation. Contract: the corpus
still validates without `role`; every contact test speaks `title`. GHAS: each fix has a
test or a documented reason. Frontend: the timeline form round trip with a follow-up
(wire-shaped fixtures, one with no `followUp`), the entry line and Done/Reopen, the
Follow-ups page filters ↔ URL ↔ request (never "the last fetch"), the host nav entry and
capability prop tests, both catalogs.
