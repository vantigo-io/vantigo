# Agents A3: the review queue, calibration, activity, usage, retention and erasure

Design: `docs/superpowers/specs/2026-11-08-virtual-employees-design.md` §5 (V20, V21, V24's view, V25's settings
audit, V26, and screens 4 to 6) and the A2 plan. Stacked on A2 (`agents/a2-agent-runs`). A2 stored the
review items, the runs, the actions and the usage; A3 decides, shows and sweeps them.

## Tasks

1. **Identity and the platform** (no migration): `contracts.Access.UserSatisfies(ctx, userID, rule)`, filled
   by identity: a permission rule is `UserHolds`; a policy rule is decided for the user's roles with no
   second factor assumed (Business and ActiveAccount are met by anyone who may act; Owner, SystemAdmin and
   the stewardship of roles need their role). `operations.Dispatcher.AccessRule(id)` hands the module an
   operation's rule.
2. **Schema** (`00147`): `review_items.channel_id` (so unbinding a channel withdraws what waits on it),
   `runs.verdict` (the mode's decision, also for a reply that went out by itself), `conversation_links`
   (the customer and contacts the module saw a conversation linked to, which erasure needs), and an index
   on `runs.started_at`.
3. **The review queue** (`review.go`, `review_reply.go`, `review_change.go`, `notify.go`): list, count, get,
   approve, edit and send, discard, all `If-Match` on the item's version. Seeing needs `agents:review`
   and `communications:conversations-view` and the read permissions of the template's needs. Deciding
   needs the reviewer to satisfy the rule of the operation the item would call (`UserSatisfies`). An
   approval issues an agent grant with `approved_by`, reads the conversation as the agent, re-judges the
   taken and moved checks, S6 and S1/S2/C1 on the text about to go, sends the stored full text (or the
   reviewer's under the same disclosure) with the A2 idempotency key, and records the day's counts and the
   action with the reviewer. A change is pinned again, checked against the gate as it stands now (a
   now-never operation is withdrawn) and made as the agent. Discarding a reply hands off. A new held item
   notes the conversation (A2) and mails the role's hand-off person.
4. **The worker `agents-reviews`** (`worker_reviews.go`): items expire after 24 hours, a Messenger reply
   15 minutes before its window closes, and hand off. Withdrawals on deleting an agent or a role and on
   unbinding a channel hand off as well. The runner finishes every hand-off (the worker has no dispatcher).
5. **Calibration** (`calibration.go`): `GET /agents/roles/{id}/calibration` over the last 200 labelled replies.
6. **Usage, activity, audit** (`activity.go`): 30 days of usage, the runs of an agent, a run with its
   timeline, verdict, items and transcript (the same seeing rule), and the list of the Owner's settings
   changes (what A1 already writes to `agents.events`: agent, role, mode, level, model, settings).
7. **Retention and erasure** (`retention.go`, `personal_data.go`): `retention_months` capped by Communications'
   message retention and lowered at save and nightly; the `agents-retention` worker; `CustomerPersonalData`
   and `ContactDataEraser`.
8. **Frontend**, English and Norwegian: `@vantigo/agents-ui` screens 4 to 6 (the queue and its item page, the
   run page), the agent page's Usage and Activity tabs, the role page's calibration panel, complaint tags,
   the daily token budget and the retention cap; the host's routes, sidebar entry and the count badge on
   the agents tile; in Communications, the hand-off banner with **Give back to the agent** and the link in
   the agent's note.
9. **Tests**: API permissions including the laundering checks; approve, edit and discard with every re-check;
   idempotency on a double approval (four reviewers at once send once); expiry including the Messenger
   window; calibration from fixed labels; the retention cap; erasure by customer, by a conversation linked
   after its run and by contact; `UserSatisfies`; an integration test through the real Communications
   (Review, approve, sent as the agent with the disclosure and the header, the grant and the action log name
   the reviewer); frontend unit tests per house pattern.
10. **Documentation**, both languages where the section is bilingual: `user/agents.md`, `admin/agents.md`,
    `user/communications.md`; `reference/agents.md`, `reference/communications.md` (the hand-off read),
    `admin/authentication.md` (`UserSatisfies`); then `go generate` and the regenerated embedded
    documentation.

## Row rules on the front desk's allowlist

V20 asks for every allowlisted operation with a row rule inside its handler to be listed and left off. The
handlers of the allowlist were read for conditional permission checks (`HasPermission`, `s.has`). Those
found only **narrow what is shown** to a caller who lacks a further permission (Invoices hide recipients
and issuer fields without `invoices:payments` and `invoices:issue`; Customers hide the legal identity
without its permission), and both of those permissions are in `agentNever`, so the agent sees the narrowed
form. None grants a caller more than the access rule, so nothing is taken off the allowlist. A reviewer
approves only the reply, and the status of the run's own conversation, whose rules are `conversations-reply`
and `conversations-manage`, which `UserSatisfies` judges.

## Deviations

- **Retention's cap is read from the configuration**, not through a contract call. Communications' message
  retention is the installation's `COMMUNICATIONS_RETENTION_DAYS`, which every module already reads from
  `Deps.Config`; a contract would only have repeated it.
- **Erasure finds a conversation by what the module saw.** Communications cannot be asked inside the
  Customers transaction and the module may not read its schema, so `conversation_links` keeps the customer
  and contacts of each conversation as of the last time a run, an approval or a hand-off read it. A
  conversation linked to a customer after the module's last look is reached by the customer's retention
  sweep, not by the erasure.
- **An approved change is `sent`.** The state set has no `approved`; the item's `outcome` says what the
  operation answered.
- **Calibration counts replies only.** A change is always held (`CHANGE`), so it would only skew the curve.
  `held` counts the hard rules and the check, not the machinery's own `SEND`, `TAKEN` and `MOVED`.
- **A reviewer's approval of an item a rule already held is not held again by that rule.** S1 and S2 hold an
  item in the queue so that a person decides; they hold an approval only when they are new. C1 always holds.
- **Communications records no `Via.ApprovedBy`.** The reviewer is named by the agents action log and by the
  grant (`identity.agent_grants.approved_by_user_id`); a module's own audit reading `Via` is a later change.
- **The count badge is on the dashboard's module card** (the agents tile), and the sidebar offers the queue
  to `agents:review` alone.
