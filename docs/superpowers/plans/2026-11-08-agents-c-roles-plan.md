# Virtual employees, delivery C: more roles (V29, V30)

Design: `docs/superpowers/specs/2026-11-08-virtual-employees-design.md` §7. Branch `agents/c-roles`, stacked on
`agents/b-memory`. Reuses A1–A3 and B: the template mechanism, the gate and allowlist, the run queue and the runner,
the pipeline, the review items and memory.

## Tasks, in order

1. **Migration 00149** (`agents_roles`): `wakeups`; role fields `start_mode`, `change_mode`, `wait_days`,
   `recipient_user_id`, the schedule and `schedule_fired_at`; the template check; a subject on `run_queue` and `runs`
   (`subject_type`, `subject_id`, the nil uuid for "no conversation"), `customer_id` and `note` on the request; the
   review kind `start`. Unique indexes become per `(role, conversation, subject)`.
2. **Templates** (`template.go`): `Safe` on an allowlisted operation, `Tools`, `Fields`, `Subject`, `Recipient`; the four
   new templates; `GET /templates` answers them.
3. **Wakeups** (`wakeups.go`, `subject.go`): the `agents-wakeups` worker (schedules in the business time zone, due wakeups
   to run requests, paused and handed-off honoured); the `follow_up` tool (30 days, the run's own subject).
4. **Runs about a subject** (`run.go`, `subject.go`): prepare, seed and prompt without a conversation; hand-off by mail to
   the hand-off person; memory about a customer only; the `notify` tool through `UserDirectory.Notify`.
5. **`start_conversation`** (`start.go`, `review_start.go`): the channel is the role's, the address a contact of the run's
   customer (S1), the verdict, the rules and the disclosure of a reply, the role's `start_mode`; the review item of kind
   `start` and its approval. Communications marks a conversation started through an agent grant (`agent_id`, the header).
6. **Modes for changes** (`tools.go`, `pipeline.go`): only an operation a template marks safe follows `change_mode`.
7. **Role API**: the new fields, validation, Owner-tier loosening; lists hold the templates the caller may see.
8. **Triggers**: `orders.quote.sent` / `accepted` and `time.week.unsubmitted` in `agents.trigger`.
9. **Publishers**: `invoices.invoice.overdue` (worker `invoices-overdue-events`, migration 00150),
   `invoices.payment.received` (trigger on `invoices.payments`), `time.week.unsubmitted` (worker `time-weeks`,
   migration 00151); DTOs in `contracts`.
10. **Orders scope marks** on `listQuotes` and `getQuote`.
11. **Frontend**: the role page shows a template's fields; the review card shows a start.
12. **Documentation**, English and Norwegian where the section is bilingual:
    - `user/agents.md` (en, nb): the templates, schedules, follow-ups, notices, the new settings, new conversations in the queue;
    - `admin/agents.md` (en, nb): setting up each template, what can go wrong;
    - `reference/agents.md`: templates, data model, tools, wakeups, runs about a subject, the review queue;
    - `reference/invoices.md`, `reference/time.md`: the events; `reference/orders.md`: the scope marks;
    - `reference/communications.md`: a conversation an agent starts.
    Then `go generate ./...` in `apps/server` (the Assistant embeds the docs).

## Tests

Wakeups and schedules in the business time zone (edges: the zone's weekday, the hour the clocks skip and repeat, a month's
28th, a schedule just set, an occurrence slept through); `follow_up` limits; `start_conversation` against S1, the channel,
the modes and approval; change mode only for safe operations; each publisher on its transaction with no personal data;
each template's trigger end to end with a scripted model; the scope marks.

## Deviations

- `invoices.payment.received` is published by a database trigger, not by Go: four write paths register payments and two of
  them have only the queries, not the transaction.
- Timesheet reminders are woken by `time.week.unsubmitted`, not by a role schedule; the Project digest is the role with a
  schedule. The reminders' allowlist is empty: the event carries the week and the hours.
- `notify` (timesheet reminders, project digest) is not left for review: it goes to one internal person, carries the
  disclosure, and passes C1. The project digest goes to one chosen person per role, not to each project's manager.
- A run about a subject uses the nil uuid for its conversation instead of a nullable column, to leave the conversation-run
  queries and their tests as they are.
- Lists of review items and runs hold the templates the caller may see, instead of requiring the reads of every template.
