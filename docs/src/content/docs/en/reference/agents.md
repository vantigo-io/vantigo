---
title: "Agents module"
description: "The Agents module (nb Virtuelle ansatte): virtual employees that do part of a person's work — the module and what it requires; the four permissions; the endpoints with their permission, statuses and refusal codes; the data model (settings, agents, permission_levels, roles, role_channels, events, runs, review_items, usage_days and the rest); the review queue and how a decision is made; calibration; memory (V27) and the weekly feedback check (V28); retention and erasure; the workers and runners; the gate that applies an agent's levels and role allowlist to the operations dispatcher; the front desk template with its allowlist, needs and hard rules; the customer scope; the two AI tasks; the Owner-only changes; and the audit events."
sidebar:
  order: 135
sources:
  - apps/server/internal/agents
  - openapi/agents.yaml
  - apps/server/internal/openapi/specs/agents.yaml
  - apps/server/internal/db/migrations/00144_agents_baseline.sql
  - apps/server/internal/db/migrations/00146_agents_runs.sql
  - apps/server/internal/db/migrations/00147_agents_review.sql
  - apps/server/internal/db/migrations/00148_agents_memory.sql
  - apps/server/internal/db/migrations/00149_agents_roles.sql
  - apps/server/internal/contracts/agent_memory.go
  - apps/server/internal/contracts/agent_colleagues.go
  - apps/server/internal/identity/access.go
  - apps/server/internal/config
  - apps/agents/frontend/src
---

The Agents module holds **virtual employees**: AI users that do part of a person's work.
The screens are in the [user guide](/en/user/agents/) and the setup in the
[administration guide](/en/admin/agents/). This page describes the module as the code
implements it. Its API reference is generated from `openapi/agents.yaml`.

The module holds the agents with their persona, their roles bound to channels, the levels
the Owner gave their permissions and the gate that applies them, and the agent **at work**:
the trigger that wakes it, its runs, its tools, the pipeline every reply goes through and the
hand-off; the **review queue** where a person approves, edits and sends, or discards what a run
leaves; the calibration of a role's score; **memory** the agent keeps about a customer and **feedback**, the weekly proposals for a role's instructions; the usage and activity views; and retention and erasure.

## The module

- **Name and mount.** `agents`, mounted at `/api/v1/agents` when `agents` is in `MODULES`.
- **Requires.** `communications`: `MODULES` refuses `agents` without it
  (`agents requires communications`). It needs an AI provider for its runs; the settings
  answer `configured: false` and an empty `models` list without one.
- **Boundaries.** It imports no other module. An agent's user is Identity's, made through
  `contracts.AgentAccounts` and granted through `contracts.AgentGrants`; a channel, a
  customer and a hand-off person are other modules', kept as opaque ids with no foreign
  key. It is the second module that receives the platform's Operations dispatcher
  ([module boundaries rule 18](/en/contributing/module-boundaries/)) and installs the gate
  for agent grants on it.
- **An agent is a user.** It has a user of kind `agent`, holds Identity roles like a
  person (never Owner or SystemAdmin, and no delegation), cannot sign in and is listed
  apart from the people.

## Permissions

| Permission | Sensitive | Delegable | What it opens |
| --- | --- | --- | --- |
| `agents:view` | no | yes | See the agents, their personas, roles and settings. |
| `agents:manage` | yes | no | Create agents, edit personas and instructions, pause and delete them. |
| `agents:review` | no | yes | Approve or discard what an agent proposes in the review queue. |
| `agents:activity` | yes | no | Read an agent's runs and transcripts, which hold customers' data. |

Three changes need `agents:manage` **and** the Owner (`owner_required`, 403): raising an
agent's level for a permission; loosening a role's reply mode (to `auto_above` or `auto`),
or lowering the threshold of a role that is not in `review`; and binding a channel the role
did not hold. Lowering a level, tightening the mode and unbinding are anyone's with
`agents:manage`, as are the role's caps (tool calls, turns and replies per day).

## Endpoints

All paths are under `/api/v1/agents`. Every endpoint answers 401 without a session and 403
without its permission. `x-vantigo-assistant: never` is set on all of them: the Assistant
cannot call this module.

| Method and path | Permission | Statuses | What it does |
| --- | --- | --- | --- |
| `GET /agents` | `agents:view` | 200 | The agents, not deleted, each with its roles. |
| `POST /agents` | `agents:manage` | 201, 400, 409 | Creates an agent and its user. 409 `company_name_required` while the company's name is empty. |
| `GET /agents/{agentId}` | `agents:view` | 200, 404 | One agent. |
| `PUT /agents/{agentId}` | `agents:manage` | 200, 400, 404, 409 | Replaces the persona. `If-Match` carries the version; 409 `version_conflict`. The user is renamed and the model follows. `dailyTokenBudget` (0 or at least 1,000; 0 removes it; absent keeps it) is the day's tokens ([budget](#the-budget)). |
| `DELETE /agents/{agentId}` | `agents:manage` | 204, 404 | Marks the agent deleted, frees its channels, ends its grants, disables its user and forgets its model. |
| `GET /agents/{agentId}/avatar` | `agents:view` | 200, 404 | The picture (PNG or JPEG), private and not sniffable. |
| `PUT /agents/{agentId}/avatar` | `agents:manage` | 200, 400, 404 | Sets the picture, multipart field `avatar`, at most 5 MiB. |
| `POST /agents/{agentId}/pause` | `agents:manage` | 200, 404 | Active to paused. Pausing a paused agent is not an error. |
| `POST /agents/{agentId}/resume` | `agents:manage` | 200, 404 | Paused to active. |
| `PUT /agents/{agentId}/permissions/{key}` | `agents:manage` | 200, 400, 404, 409 | Sets the level `off`, `read` or `change`. 403 `owner_required` when raising and not the Owner; 409 `level_locked` for a key in the never list; 404 for a key the catalog lacks. |
| `POST /agents/{agentId}/roles` | `agents:manage` | 201, 400, 404 | Adds a role from a template (any of the five), in `review` with no channel. Empty instructions take the template's. |
| `PUT /agents/{agentId}/roles/{roleId}` | `agents:manage` | 200, 400, 404, 409 | Replaces the role. `If-Match`; 403 `owner_required` when it loosens the reply, start or change mode, or lowers the threshold while any of the three is not `review`. `startMode`, `changeMode`, `waitDays` (1 to 60), `recipientUserId` (an active person; the nil uuid clears it) and `schedule` (`kind` `none`, `weekly` or `monthly`, with `day` and `time`) are optional and an absent one keeps the role's; a schedule on a template with none is 400. |
| `DELETE /agents/{agentId}/roles/{roleId}` | `agents:manage` | 204, 404 | Removes the role and its channels. |
| `PUT /agents/{agentId}/roles/{roleId}/channels` | `agents:manage` | 200, 400, 404, 409 | Replaces the role's channels (at most 50). 409 `channel_bound` when another role holds one. |
| `GET /agents/{agentId}/setup` | `agents:view` | 200, 404 | What the agent's roles need against the levels it has, and whether its Identity roles hold each permission. |
| `GET /settings` | `agents:view` | 200 | The company's name, the retention, `retentionCapMonths`, the version, `configured` and the models the installation allows. |
| `PUT /settings` | `agents:manage` | 200, 400, 409 | Changes the company's name (1 to 120 characters) and the retention (1 to 60 months, lowered to `retentionCapMonths` when longer: [retention](#retention-and-erasure)). `If-Match`. |
| `GET /agents/{agentId}/usage?days=` | `agents:view` | 200, 404 | The agent's usage for the last `days` (30 by default, at most 90) on the business day, oldest first, with zeros for a day with nothing. |
| `GET /agents/{agentId}/runs?state=&limit=&offset=` | `agents:activity` + [seeing](#the-review-queue) | 200, 400, 403, 404 | The agent's runs, newest first. |
| `GET /runs/{runId}` | `agents:activity` + [seeing](#the-review-queue) | 200, 403, 404 | A run with its actions (the timeline), its `verdict`, its review items and its transcript. |
| `GET /roles/{roleId}/calibration` | `agents:view` | 200, 404 | The [calibration](#calibration) of the role. |
| `GET /audit?limit=&offset=` | `agents:view` | 200 | The Owner's changes ([audit](#audit)), newest first. |
| `GET /agents/{agentId}/memory` | `agents:view` | 200, 404 | How many [notes](#memory) the agent keeps: `customerNotes`, `conversationNotes` and `customers`. |
| `DELETE /memory/{noteId}` | `agents:manage` | 204, 404 | Deletes one note. The audit (`memory.deleted`) names the note, the agent and the subject, never the text. |
| `GET /roles/{roleId}/suggestions?state=` | `agents:manage` | 200, 404 | The role's [suggestions](#feedback) newest first, at most 20, narrowed by `state` (`proposed`, `applied`, `dismissed`). |
| `POST /suggestions/{suggestionId}/apply` | `agents:manage` | 200, 400, 404, 409 | Applies the suggestion as an ordinary role edit. `If-Match` is the **role's** version (409 `version_conflict`); 409 `suggestion_decided`; 409 `suggestion_stale` when the passage it replaces is not in the instructions exactly once; 400 when the result is over 8,000 characters. Answers the role. |
| `POST /suggestions/{suggestionId}/dismiss` | `agents:manage` | 200, 404, 409 | Dismisses it; 409 `suggestion_decided`. |
| `GET /review?agentId=&state=&limit=&offset=` (`state` may be `decided`: every state but waiting) | `agents:review` + [seeing](#the-review-queue) | 200, 400, 403 | The queue: waiting items first, those that lapse soonest first, then the rest, newest first. |
| `GET /review/count` | `agents:review` | 200 | `{waiting}`: the badge on the agents tile. Holds no content. |
| `GET /review/{itemId}` | `agents:review` + [seeing](#the-review-queue) | 200, 403, 404 | One item. |
| `POST /review/{itemId}/approve` | `agents:review` + the item's own rule | 200, 400, 403, 404, 409, 422, 502 | [Sends the reply or makes the change](#how-a-decision-is-made). `If-Match`. |
| `POST /review/{itemId}/send` | `agents:review` + the reply's rule | 200, 400, 403, 404, 409, 422, 502 | Sends the reviewer's `text` (and optional `components`) under the reply's disclosure. Only a reply. `If-Match`. |
| `POST /review/{itemId}/discard` | `agents:review` | 200, 400, 403, 404, 409 | Discards with a `reason` (`wrong_facts`, `wrong_tone`, `should_not_answer`, `other`) and an optional `text`. Discarding a reply hands off. `If-Match`. |
| `GET /templates` | `agents:view` | 200 | The five role templates: default name and instructions, allowlist, the operations marked safe to automate, needs, hard rules, tools, the role fields the template uses and its trigger. |
| `GET /conversations/{conversationId}/hand-off` | `communications:conversations-view` | 200 | Whether the conversation is handed off (`handedOff`, `at`, `reason`, `roleId`). |
| `DELETE /conversations/{conversationId}/hand-off` | `communications:conversations-manage` | 204 | Gives the conversation back to the agent: the marker is cleared. Nothing happens when there is none. |

### Refusals

A refusal is `application/problem+json` with a stable `code`; the interface keys off the
code, never the words. `errors` carries the field errors of a 400.

| Code | Status | When |
| --- | --- | --- |
| `not_found` | 404 | The agent, role or permission key does not exist, or the agent is deleted. |
| `invalid` | 400 | A field is out of range, or `If-Match` is not a version. |
| `version_conflict` | 409 | The row changed since it was read. Judged under the row's lock. |
| `company_name_required` | 409 | An agent is created before the company's name is set. |
| `channel_bound` | 409 | A channel is already answered by another role. |
| `owner_required` | 403 | The change is the Owner's alone. |
| `level_locked` | 409 | A level is set for a permission in the never list. |
| `ai_unavailable` | 503 | Declared by the contract for a run with no AI provider; no endpoint answers it. |
| `reviewer_not_permitted` | 403 | The caller lacks a permission the item or run shows (`communications:conversations-view` or a read of the template), or may not do what the item would do (the operation's access rule, keys and policies, through `UserSatisfies`). |
| `item_decided` | 409 | The item is no longer waiting, or a reply to this message was already sent with other text. |
| `held` | 422 | A rule that holds in every mode holds the text now; `rules` names them, and the item stays waiting with them added to `held_by`. |
| `channel_refused` | 422 | The channel refused the message (too long, components unsupported …); edit it. |
| `change_refused` | 422 | The operation answered a 4xx; discard it or fix the record. |
| `reply_key_reused` | 409 | A different reply to this message was queued before (an edit after a send that failed) and the agent's reply is not in the conversation. When it is, the item is finished as sent instead. |
| `send_unknown` | 502 | It is not known whether the reply was sent. Sending again is safe: the reply is idempotent. |
| `suggestion_decided` | 409 | The suggestion was applied or dismissed already. |
| `suggestion_stale` | 409 | The passage a suggestion replaces is gone from the instructions, or occurs twice. |

The fields: an agent's `name` 1 to 60 characters; `style` at most 1,500; `languages`
`customer`, `nb` or `en`; `disclosureWording` `assistant`, `talking` or `compact`; `model`
empty or one the installation allows. A role's `name` 1 to 80; `instructions` at most
8,000; `replyMode` `review`, `auto_above` or `auto`; `threshold` 50 to 100;
`capsToolCalls` 1 to 60; `capsTurns` 1 to 16; `maxRepliesPerDay` 1 to 50;
`handOffUserId` an active person; `complaintTagIds` at most 20 Communications tag ids (absent keeps them). A role's `handOffWarning` answers `none`, `disabled`,
`not_a_person` or `no_permission`; for anything but `none` a hand-off goes to the Inbox,
unassigned.

## Data model

The `agents` schema. A user, a channel and a hand-off person are opaque ids.

| Table | What it holds and its constraints |
| --- | --- |
| `settings` | One row (unique index on `(true)`). `company_name` varchar(120), empty until set; `retention_months` 1 to 60, default 6, never above the cap of [retention](#retention-and-erasure); `version`, `updated_at`, `updated_by_user_id`. |
| `agents` | `id`, `user_id` (unique), `name` 1 to 60, `style` at most 1,500, `languages` (`customer`, `nb`, `en`), `disclosure_wording` (`assistant`, `talking`, `compact`), `state` (`active`, `paused`, `deleted`), `daily_token_budget` (null for none, else above 0), `version`, timestamps and `created_by_user_id`. A deleted row stays so the audit's references stay true. |
| `permission_levels` | `(agent_id, permission_key)` key, `level` (`off`, `read`, `change`). Only what was given is stored; **off deletes the row**. Cascades with the agent. |
| `roles` | `template` (`front_desk`, `reminder_replies`, `sales_followup`, `timesheet_reminders`, `project_digest`), `start_mode` and `change_mode` (as `reply_mode`, default `review`; the threshold is shared), `wait_days` 1 to 60 (default 7), `recipient_user_id`, the schedule (`schedule_kind` `weekly` or `monthly`, `schedule_day` 1 to 28, `schedule_time` `HH:MM`, all three or none, and `schedule_fired_at`), `name` 1 to 80, `instructions` at most 8,000, `reply_mode` (default `review`), `threshold` 50 to 100 (default 85), `caps_tool_calls` (default 30), `caps_turns` (default 8), `max_replies_per_day` (default 5), `hand_off_user_id`, `complaint_tag_ids` (the tags that mark a complaint, S5), `version`. Cascades with the agent. |
| `role_channels` | `(role_id, channel_id)` key with `channel_id` unique: a channel belongs to at most one role. |
| `events` | The Owner's changes: `at`, `actor_user_id`, `action`, `target`, `before` and `after` as jsonb. |
| `run_queue` | A request for a run: `role_id`, `conversation_id`, `subject_type` and `subject_id` (see [runs about a subject](#runs-about-a-subject)), `customer_id`, `reason` (`message`, `assigned`, `retry`, `wakeup`, `event`), `note` (what the wakeup or the event said), `attempt`, `not_before`, `due_by`, `taken_at`, `run_id`. A partial unique index on `(role_id, conversation_id, subject_id)` where `taken_at` is null holds one request that waits. |
| `handoff_notices` | `(role_id, day)` key, `n`: the hand-offs of a role about a subject on a business day, counted atomically, so the hand-off person is mailed for the first and every tenth. |
| `wakeups` | A moment a role is to look at a subject again: `role_id` (cascade), `subject_type` (`conversation`, `quote`, `schedule`, `person_week`), `subject_id`, `customer_id` (from the event that made it, never from a message), `at`, `reason` (at most 200), `taken_at`. A partial unique index on `(role_id, subject_type, subject_id)` where `taken_at` is null holds one pending wakeup, which a second moves. |
| `handed_off` | `conversation_id` (key), `role_id`, `at`, `reason`: the marker that keeps the trigger away. |
| `runs` | One run: `agent_id`, `role_id`, `conversation_id`, `customer_id` (the run's scope), `reason`, `attempt`, `state` (`queued`, `running`, `done`, `waiting_review`, `handed_off`, `failed`, `stopped`), `outcome` (`sent`, `review`, `handed_off`, `no_reply`), `error_code`, `summary`, `claimed_by`, `claimed_until`, the counts of tool calls, turns and tokens, `started_at`, `ended_at`, `sent_at`. `verdict` (jsonb, what the pipeline decided: `mode`, `threshold`, `score`, `modelScore`, `sources`, `held`, `refused`, `sent`). `subject_type` and `subject_id`. A partial unique index on `(role_id, conversation_id, subject_id)` for `queued` and `running` keeps one run at a time. |
| `run_messages` | The transcript: `run_id`, `role`, `text`, `tool_calls`, `tool_call_id`. Copies of message bodies, so retention deletes them with the run. |
| `actions` | The action log, one row per tool call: `operation_id`, `ref` (the operation and the ids it named), `scoped` (it returned a customer's data), `mode`, `status`, `http_status`, `duration_ms`, `approved_by`. Never a body. |
| `review_items` | What a run leaves for a person: `kind` (`reply`, `change` or `start`), `item` (jsonb), `score`, `sources`, `model_score`, `held_by` (rule codes), `state` (`waiting`, `deciding` while a reviewer acts on it, `sent`, `edited_sent`, `discarded`, `withdrawn`, `expired`), `claimed_at`, `version`, the decision's columns, `expires_at` (24 hours, or a Messenger window's close less 15 minutes), `channel_id` (so unbinding the channel withdraws what waits on it), `discard_reason`, `discard_text`, `edit_ratio`, `outcome`. A reply's `item` holds `text`, `disclosure`, `components`, `inReplyToMessageId` and `windowClosesAt`; a start's holds `text`, `disclosure`, `channelId`, `to` and `subject`; a change's holds `operationId`, `summary`, `explain`, `pathParams`, `query` and `body`. |
| `usage_days` | `(agent_id, day)`: runs, input and output tokens, sent, held and discarded. |
| `channel_addresses`, `seen_senders` | The addresses of the channels the module has read (V23), and the senders it has met (S4). |
| `conversation_links` | `conversation_id` (key), `customer_id` and `contact_ids` as the module last saw the conversation linked, `seen_at`: what [erasure](#retention-and-erasure) finds a customer's or a contact's conversations by. |

| `memory` | A note (V27): `agent_id` (cascade), `subject_type` (`customer` or `conversation`), `subject_id` (the customer's number or the conversation's id, opaque text), `text` 1 to 500 characters, `created_at`, `run_id` (no foreign key: the note outlives the run's deletion by its own age). |
| `instruction_suggestions` | A suggestion (V28): `role_id` (cascade), `old_text` (the passage to replace, null to append), `new_text` (at most 1,000), `rationale` (at most 500), `item_ids` (the review items it rests on, ids only), `state` (`proposed`, `applied`, `dismissed`), `created_at`, `decided_by`, `decided_at`. |

`roles.feedback_checked_at` says when the weekly check last looked at the role and `feedback_since` the end of the corrections it last read; `runs.answered_over_at` is set when a person answered over the run's Auto reply ([calibration](#calibration)).

The agent's `version` moves on with a change of the persona, a level or a role, so the gate
can cache by it. The model of an agent is not a column: it is the per-subject choice for
the task `agents.run`, subject being the agent's id.

## The gate

The dispatcher calls the gate for every operation an agent's run asks for. It starts
from the contract's mode and narrows it, and it **fails closed**:

1. The run must resolve and the operation must name a permission. An operation whose
   access rule names none (a policy or a session rule) is `never`, as is one the contract
   marks `never`.
2. The agent must exist and not be deleted, and the operation must be on the allowlist of
   a template one of its roles has. The grant names the agent and the run, not the role,
   so the allowlist is the **union** of the agent's roles' templates.
3. None of the permissions the operation names may be in the **never list**, whatever the
   level.
4. Each permission it names narrows the mode by the agent's level: **off** (the default,
   and a key with no row) makes it `never`; **read** turns `confirm` into `never` and
   leaves the rest; **change** leaves it.

The agent still needs the permission through its Identity roles, and the router still
checks every call: the gate only narrows.

The table of an agent is cached by the agent's version. A change on this replica
invalidates it at once; the gate asks the database for the version at most once a second,
so a change made on another replica takes effect at the next tool call after that second.

### The never list

No level can be set for these; a request is 409 `level_locked`, and the setup answers them
as `locked`:

`accounting:close`, `agents:activity`, `agents:manage`, `agents:review`, `agents:view`,
`customers:personal-data`, `entra:manage`, `expenses:approve`, `google:manage`,
`identity:manage`, `invoices:issue`, `invoices:payments`, `knowledge:manage`,
`manufacturing:costs-view`, `orders:carriers-manage`, `orders:settings`, `staff:approve`,
`staff:manage`, `staff:personal-data`, `time:approve`.

A test in `internal/integration` holds the list to the composed catalog by key patterns
(`identity:*`, `agents:*`, `*:approve`, `*:personal-data`, `invoices:issue` and
`payments`, `staff:manage`): a new permission that matches one and is not listed fails it.
A second test requires every permission that cannot be delegated to be listed or waived
with a reason.
The Assistant's API is not on the list because it names no permission, and the dispatcher
refuses it by its path.

## Templates

Five templates, in code. Each declares its trigger, the levels it needs, its allowlist with the
operations it marks **safe to automate**, its default instructions, the tools it offers and the role fields it uses.

| Key | Trigger | Tools | Role fields |
| --- | --- | --- | --- |
| `front_desk` | `communications.conversation.message_received` and `assigned`, on a bound channel | `reply`, `note`, `tag`, `remember`, `follow_up`, `hand_off` | channels, reply mode, change mode, complaint tags |
| `reminder_replies` | the same; the allowlist is the front desk's, which holds Invoices' reads | the same | the same |
| `sales_followup` | `orders.quote.sent`, `wait_days` later | `start_conversation`, `remember`, `follow_up`, `hand_off` | channels, start mode, wait days |
| `timesheet_reminders` | `time.week.unsubmitted` | `notify`, `hand_off` | none |
| `project_digest` | the role's schedule | `notify`, `hand_off` | schedule, recipient |

Only a template that answers conversations is woken by a message: a reply to a conversation the sales follow-up
started waits in the Inbox. The timesheet reminders' allowlist is empty (the event names the week and the hours), the project
digest's holds Projects' `getProjects`, `getProjectsById` and `getProjectsByIdMilestones`, and the sales follow-up's holds
`getQuote`, `getCustomer`, `getCustomersByIdContacts`, `getCommunicationsConversations` (all four customer-scoped:
`getQuote` by `customer.customerId`, the list by `customerId`), `postCommunicationsConversations` (reserved for `start_conversation`: refused
through `call_operation`), and Knowledge's two reads. `GET /templates` answers the five with `fields`, `tools`, `trigger` and
`safeOperations`.

### Safe to automate

A template marks an allowlisted operation as safe. Only such a change follows the role's `change_mode` (decided by
S3 and the threshold, as [a reply is](#the-pipeline)); every other change is a review item of kind `change`. A change
that is not made, because the mode holds it, a score is under the threshold or the call was refused before dispatch,
becomes a review item as before; one whose outcome is unknown is not retried or queued, and hands the conversation
off. The front desk and the reminder replies mark `patchCommunicationsConversationsById` (its body pinned to `status`).
Raising `start_mode` or `change_mode`, or lowering the threshold while any mode is not Review, is the Owner's alone.

### The front desk template

`front_desk` (default name *Front desk*). It answers
the conversations that come in on the channels its role is bound to.

### The allowlist

The operations a Front desk role may call, whatever its levels. The second column marks an operation that returns a customer's data and so carries
`x-vantigo-customer-scope`; the third one whose path names a conversation, which a run's
tools must pin to its own.

| operationId | Customer data | Own conversation |
| --- | --- | --- |
| `getCommunicationsConversations` | yes | |
| `getCommunicationsConversationsById` | yes | yes |
| `postCommunicationsConversationsByIdReply` | | yes |
| `postCommunicationsConversationsByIdNotes` | | yes |
| `patchCommunicationsConversationsById` | | yes |
| `putCommunicationsConversationsByIdTagsByTagId` | | yes |
| `deleteCommunicationsConversationsByIdTagsByTagId` | | yes |
| `getCommunicationsTags` | | |
| `getCustomer` | yes | |
| `getCustomersByIdContacts` | yes | |
| `listSalesOrders` | yes | |
| `getSalesOrder` | yes | |
| `listOrderShipments`, `getShipment`, `listDeliveries` | yes | |
| `getInvoices` | yes | |
| `getInvoicesById` | yes | |
| `getProducts`, `getProduct`, `getProductsByIdVariants`, `getProductsByIdVariantsByVariantIdPrices` | | |
| `searchKnowledge`, `getKnowledgeArticleText` | | |

The shipment reads answer "where is my parcel?": a shipment names its order's customer, and the lists
take it as a filter (see [Orders](/en/reference/orders/#agents-and-the-customer-scope)). The tools drop the
business's own freight figures from a shipment before the agent reads it: `bookedCost`, `bookedCostCurrency`,
`bookedCostEstimate`, `carrierAccountName` and `bookedBy`. The Orders API still answers them.

### The needs

What a Front desk role needs, shown by the setup. When several roles need the same
permission the higher level wins; a permission whose module is not enabled is left out.
Conversations are **change** because the assistant's gate turns a confirm-mode operation
into `never` when any permission it names is only read.

| Permission | Level |
| --- | --- |
| `communications:conversations-view`, `conversations-reply`, `conversations-manage` | change |
| `customers:view`, `customers:contacts-view`, `customers:associations-view` | read |
| `orders:access`, `invoices:access` | read |
| `products:products-view`, `variants-view`, `pricing-view`, `categories-view`, `tax-categories-view` | read |
| `knowledge:articles` | read |

The setup is `ready` when the agent has a role and, for every need, a level at least as
high, not locked, and held by the agent's Identity roles.

### The hard rules

Rules the pipeline applies after the model has written. The template lists them with
whether they are **locked**: a locked rule holds in every reply mode, Auto too. The
unlocked ones hold in Review and Auto above X %.

| Rule | Locked | What it checks |
| --- | --- | --- |
| S1 Recipient | yes | A reply goes only to the latest inbound sender's own address; with a customer's data in it, only to a contact of the run's customer. |
| S2 Another customer's data | yes | An attachment must belong to the run's customer; a run with no customer attaches nothing. |
| S3 No source | no | A reply with no source for what it says is held. |
| S4 First contact | no | The conversation's first inbound message from a new participant is held. |
| S5 Complaint | no | A conversation carrying a complaint tag is held. |
| S6 Reply window | yes | A conversation whose window is closed is refused, then handed off. |
| S7 Loop guard | yes | The loop limits are enforced, then handed off. |
| C1 Claims to be a person | yes | A draft that says it is a person is held. |
| C2 A promise the instructions do not allow | no | A date, discount, refund or price not read from Products is held. |

The rules are applied by [the pipeline](#the-pipeline).

## Runs about a subject

A run is about a conversation, or about a **subject**: a `quote`, a `person_week` (`<user id>|<Monday>`) or a
`schedule` (the occurrence's date in the business time zone). Such a run has the nil uuid for its conversation
(`runs.conversation_id`), names the subject in `subject_type` and `subject_id`, and has the customer the event named
(`run_queue.customer_id`, carried to `runs.customer_id`) or none. It marks and hands off no conversation: a hand-off
mails the role's hand-off person a link to the run, once for the role's first such hand-off of a business day and then for every tenth, with the count (`handoff_notices`). A run that ends having done nothing (the quote was accepted) is `done` with outcome
`no_reply`. Memory is kept only about a customer. S7's hourly run count and the reply count are the conversation runs'.

## Wakeups

The `agents-wakeups` worker (every 30 seconds, needing only the database):

1. **Schedules.** For each role of an active agent with a schedule, the latest occurrence at or before now in the business
   time zone (`weekly` on an ISO weekday 1 to 7, `monthly` on a day 1 to 28, at `HH:MM`; a local time the clocks skip is
   the moment after the jump, one they repeat the later) is written as a `schedule` wakeup, once, and `schedule_fired_at`
   moved to it, in one transaction. An occurrence more than 12 hours old is not written, only marked. Setting or
   changing a schedule sets `schedule_fired_at` to now.
2. **Due wakeups.** Wakeups that came due, of active agents' roles (a paused agent's wait), become run requests: a
   `conversation` wakeup an ordinary request with reason `wakeup` unless the conversation is handed off; any other subject
   a request with the subject, the customer and the wakeup's reason as `note`. A wakeup more than 7 days late is dropped. Each is marked taken.

`agents.trigger` also takes `orders.quote.sent` (a `quote` wakeup at the sent time plus each sales follow-up role's `wait_days`,
replacing the quote's pending one), `orders.quote.accepted` (removes the quote's pending wakeups) and `time.week.unsubmitted`
(a request, reason `event`, for each timesheet reminders role). Roles of paused agents are woken too; their requests lapse after a day.

## The trigger

`agents.trigger` is a handler of the [domain-events outbox](/en/contributing/platform-packages/#the-domain-events-outbox)
(module boundaries rule 23). It runs whatever `MODULES` says and does nothing when `agents` is not
enabled; it writes only this module's schema, on the delivery's own transaction, and reads no
contract. It takes four events of [Communications](/en/reference/communications/#events).

| Event | What it does |
| --- | --- |
| `message_received` | Writes a **run request** when the channel is bound to a role of an **active** agent, the conversation is unassigned or assigned to that agent, it is not handed off, and the message is not an auto-reply. An unreadable message is handed off at once, with a note. |
| `assigned` | An assignment made by an agent (its own hand-off) is ignored. A person's assignment clears the hand-off marker; an assignment **to the agent** also writes a request. |
| `replied` | A person's reply (`byAgent` false) clears the hand-off marker. |
| `note_mentioned` | [An agent mentioned in an internal note](#mentions) is woken. |

A request is keyed by `(role, conversation)`: a second message while one waits moves `not_before` to
now plus **20 seconds**, and never past `due_by`, the first message plus **2 minutes**, so three short
messages make one run and a chatty customer cannot postpone it for ever. A paused agent gets no request;
the ones already queued stay.

## Mentions

An internal note that mentions an agent wakes it (virtual employees design V32). `communications.conversation.note_mentioned`
carries the conversation, the note's message id, the channel, the author and the users mentioned, and no text. The trigger
does nothing when the author is an agent (two agents never wake each other), when the conversation is
[handed off](#the-hand-off), or when the channel's role does not belong to a mentioned agent: the agent is woken **under
its role on the conversation's channel**, and one with no role there, or a paused one, is left alone. A person
mentioned is Communications' to mail ([Events](/en/reference/communications/#events)).

The request is the conversation's ordinary one — one per `(role, conversation)`, reason `message` — but due **now**, with no
debounce, and its `note` is `mention:<message id>[,<message id>…]`: a second mention before the run adds its id, so the run reads every unanswered note since the last run, oldest first. A request waiting in its wait for a customer's message becomes due
at once, and the run sees both. The run reads the note from the conversation (the event holds no text), puts them in its
first message as `colleagueNotes`, **data** like the rest, and the first message and the system prompt say a colleague
asked: the agent answers the colleague with `note`, and writes to the customer only if the colleague asks. Everything
else holds: the run's role, allowlist, customer scope, the pipeline's hard rules and the role's modes. Unlike a
message's run, a mention's goes on when the conversation has no inbound message to answer. When the conversation is assigned to a
person other than the agent, the run is **note-only**: it is offered the `note` tool alone (no reply, tag, remember, follow-up or
hand-off), every change `call_operation` would queue is refused, and it ends without a hand-off.

## Talking to an agent in the panel

`contracts.AgentColleagues` (`Module.AgentColleagues`, `agents/colleagues.go`) is what the
[Assistant](/en/reference/assistant/#talking-to-a-virtual-employee) needs to let a person talk to an agent (design V31). It is
consumer-gated: the Assistant asks for `agents:view` before it lists, starts, sends or confirms. It reads this module's
schema and the dispatcher's descriptions only; Compose resolves it once the dispatcher exists and gives the provider
the dispatcher.

| Method | Answers |
| --- | --- |
| `Agents` | The active agents, ordered by name, with their style and languages. |
| `Agent` | One active agent, or `ErrColleagueUnavailable` for an unknown, paused or deleted one. |
| `Names` | The names of the agents asked for, whatever their state. |
| `Operations` | What the agent may do in the panel, by operation id, with whether it may *change* with it: the union of its roles' allowlists less the operations that return a customer's data or write to a customer (`reply`, `start`), each narrowed by the levels the Owner gave and the permissions the agent's Identity roles hold, exactly as [the gate](#the-gate) narrows a run. A change (an operation whose contract mode is confirm) needs the level `change` for every permission it names. |

The panel's run is the **person's** (an assistant grant); nothing here issues an agent grant or starts a run, and the
agent's modes do not apply.

## Runs

The **runner** (`Module.APIRunners`, in the API process only: it needs the dispatcher) polls every
**5 seconds**, hands off the requests of an agent over its budget, completes the hand-offs that wait
for their note, and claims due requests with `FOR UPDATE SKIP LOCKED` in a transaction that **commits
before the run starts**. It carries at most `AGENTS_CONCURRENCY` runs (default 4) on a replica, each
a goroutine with a **5-minute ceiling**. No transaction, advisory lock or connection is held across
a dispatcher call or a model call; a test on a pool of one proves it.

- **The claim.** `claimed_by` is the replica's id and `claimed_until` two minutes ahead, renewed every
  30 seconds, each renewal its own statement. A request is claimed only when its agent is active and
  its conversation has no run in progress.
- **The `agents-runs` worker** (every 30 seconds, needing no dispatcher) takes a run whose claim lapsed:
  the first time it is stopped (`lapsed`) and re-queued once; the second time it is handed off. It also
  hands off a request that waited more than a day (`stale`).
- **The grants.** A run is given an agent grant at the claim and it is revoked at the end. The run's
  **customer** is the conversation's when a person linked it, or when the customer linker did and the
  sender is one of that customer's contacts (an e-mail address, or the Messenger participant linked to a
  contact); otherwise none. The conversation is read first under a grant with no customer, and the
  contacts under one for the candidate; both are revoked. The grant's run names the run's **role**, and
  the gate narrows the run to that role's template.
- **Before the model.** An agent over its budget, a conversation with six runs in the last hour and a
  sender that is a channel of this installation are handed off; a conversation a person has, an
  auto-reply and one with no sender are left alone and the run ends with outcome `no_reply`.
- **The end.** `done` with outcome `sent`; `waiting_review` with `review` (a reply or a change waits);
  `handed_off`; or `failed`. A run whose model cannot be reached, or cannot call tools, fails as
  `ai_unavailable` and hands off. A final text with no `reply` is a hand-off with the text in the note, and
  so is a run that reached its caps.

## The agent's tools

From `agentloop`, as the agent and limited to the run's role's allowlist by the gate:

| Tool | What it does |
| --- | --- |
| `search_operations`, `describe_operation` | Find and describe the operations the run may call. |
| `call_operation` | Calls one. A read runs at once. A call to a change (a `confirm` operation) never runs: it is checked against the contract and becomes a **review item** of kind `change`, always, in every mode. It takes `sources` and `score` beside `explain`. |
| `reply` | `text`, `components?`, `attachments?`, `sources[]`, `score`. Proposes the one reply of the run through [the pipeline](#the-pipeline). It takes no recipient. `attachments` are refused (`attachments_unavailable`): fetching a Vantigo document under the customer's scope needs an operation that does not exist yet. |
| `note` | An internal note on the conversation. |
| `tag` | Tags the conversation. |
| `remember` | `text` (at most 500 characters). Keeps a [note](#memory) about the run's customer. Refused with `no_customer` when the run has none, `note_too_long`, `too_many_notes` (3 a run) and `memory_full` (50 for the customer, nothing evicted). A note the agent already holds answers `already_remembered`. It writes nothing to Communications and needs no level. |
| `hand_off` | A note with the summary, the conversation assigned to the role's hand-off person (or the assignee cleared), the marker written, the run ended. In a run about a subject: the hand-off person is mailed a link, the run ended. |
| `follow_up` | `at` (RFC 3339, or a date taken as 08:00 in the business time zone), `reason` (at most 200). Writes the one pending wakeup of the run's own subject (the conversation, or the quote): refused `out_of_range` unless `at` is in the future and at most 30 days ahead, `no_subject` for a scheduled run, `handed_off` once the run handed the conversation off. |
| `start_conversation` | `channelId`, `to`, `subject`, `text`, `sources[]`, `score`. Starts one conversation a run through Communications' `POST /conversations` as the agent, with the customer linked and `Idempotency-Key` per role, subject and address (so a second wakeup about the same subject starts nothing more). The item keeps the contact's id, so that [erasing the contact](#retention-and-erasure) deletes it and the run that wrote it. Refused in code: `channel_not_yours` (the channel is not one of the role's), `no_customer`, `not_a_contact` (the address is not an e-mail of the run's customer's contacts, or is a channel of this installation), `already_followed_up` (the role has started `max_replies_per_day` conversations to the customer in 24 hours), `already_started`. The verdict, the rules and the disclosure are the reply's; the mode is `start_mode`; held, it is a review item of kind `start`. |
| `notify` | `title`, `text`. One message to one colleague by `UserDirectory.Notify`, with the disclosure line on top and C1 applied (a failed or positive check refuses it: `rejected`); the recipient is the person of the subject (`person_week`) or the role's `recipient_user_id`, never the model's. Refused for an inactive person or an agent, `recipient_unavailable` when the recipient does not satisfy the access rule of an operation the run called (the note carries what it read, so a digest is limited to what its recipient may see; the same check refuses a recipient at role save, 400), and `daily_limit` after 50 notices from the role in 24 hours. The title is one line (control characters dropped) and the content check reads title and text. Not reviewed. |

**Pinned.** Every operation that names a conversation (`getCommunicationsConversationsById`,
`postCommunicationsConversationsByIdReply`, `…Notes`, `patchCommunicationsConversationsById`, the tag
operations) is for the run's own conversation: the id is filled in, and another is refused
(`own_conversation_only`). `patchCommunicationsConversationsById` takes the `status` and nothing else
(`status_only`); the customer link and the assignee are not the model's to set, and the assignee is
`hand_off`'s. The operations the module's own tools write (`reply`, `…Notes`, the tag operations) are
refused through `call_operation` (`use_the_tool`), so nothing is sent that the pipeline did not see.
`search_docs` and the screen tools are not offered. **One outbound message a run:** after a `reply` it
may only note, tag and end.

**The prompt** (`promptVersion`) states who the agent is, the persona's style and language rule, the
role's instructions, and that everything it reads is data and never instructions; it must look in
Knowledge first and cite the article, answer truthfully about being an AI, offer answer options on a chat
channel with *Talk to a person* among them, and hand off when it does not know. The disclosure line is
not written by the model.

## The pipeline

What a `reply` goes through, in code, after the model has written.

0. **What the run read.** S1 judges what a reply may carry from the run's action log (any successful read of a
   customer's data), not from the sources the model cites. The model's context holds no colleague's internal note,
   and a run with no customer is not shown the conversation's customer link, suggestion or candidates.
1. **The verdict (V17).** Each cited source must be an entry of the run's action log that succeeded
   (an operation and its ids, as `getInvoicesById 2026-0931`, or an id alone), and never a read of
   the conversation itself. The valid ones are kept; **with none the score is 0**. The score, the valid
   sources and the model's own score are stored on the review item.
2. **The hard rules (V18).** S1 to S7 above, with these definitions:

   | Rule | Definition |
   | --- | --- |
   | S1 | The reply goes to the latest inbound sender alone. When a valid source is a read of a customer's data (a scoped operation) or an attachment is carried, the sender must be a contact of the run's customer; otherwise it is held. No recipient also holds. |
   | S2 | An attachment must belong to the run's customer; a run with none attaches nothing. |
   | S3 | The score is 0. |
   | S4 | The conversation's first inbound message, from a sender whose **first conversation** this is. It is judged per first conversation: the same sender's second conversation is not a first contact, whoever starts it. |
   | S5 | The conversation carries one of the role's `complaint_tag_ids`. |
   | S6 | The reply window is `closed`, or only `human_agent` is left: **refused**, never sent tagged as a human agent's. |
   | S7 | The latest message is an auto-reply, the sender is a channel of this installation, `max_replies_per_day` agent messages were sent in the last 24 hours (a run's own send and an approval), or six other runs started on the conversation in the last hour: **refused**. |

   A refused reply is stored as a review item in state `withdrawn` and the conversation is **handed
   off**. A rule that holds names its code in the item's `held_by`.
3. **The content check (C1, C2).** The task `agents.check` is asked one structured question with the
   role's instructions and the draft, never the conversation: `{claimsHuman, promises: [{text, allowed}]}`.
   `claimsHuman` holds the reply (C1); a promise that is not allowed holds it (C2). A call that fails, times
   out or does not answer the rubric holds it (`CHECK`).
4. **The mode (V19).** *Review*: every reply is queued. *Auto above X %*: sent when the score is at
   least the threshold and **nothing** held it. *Auto*: sent unless S1, S2, C1 or a failed check holds it;
   S3, S4, S5 and C2 do not apply. **Changes are always queued in this delivery.**
5. **The disclosure (V11).** The line, in the persona's wording and language (`nb` or `en`; for a persona
   that follows the customer, the language the reply was written in), is put at the top of the text. The
   review item stores the line and the text apart. The text with the line must fit the channel's limit
   (`text_too_long` is the model's to shorten).
6. **The send.** Just before it, the run checks that its agent is still active and that this replica still
   holds the claim; a paused agent or a claim taken over by the worker stops the run, and the model's
   next turn is not taken. `POST …/reply` as the agent names `inReplyToMessageId`, the message the run
   answered, with the idempotency key `agents-<agent>-<conversation>-<message>`, so a retry and the original can
   never both send. A `201` ends the run `done`. `409 conversation_taken` withdraws the item and ends the run
   (`no_reply`) without a hand-off. `409 conversation_moved` withdraws it, ends the run `no_reply` and queues a new
   run. A dispatcher failure or a `5xx` after the message may have been queued is **unknown**: no review item is made
   (it could send twice); the conversation is handed off with a note that says so (`send_unknown`).
   `422 reply_window_closed` is S6. A refusal that is the model's to fix (`components_invalid`,
   `text_too_long`, `too_large`, `400`) is answered to the model, which may reply again. Anything else holds the
   reply for a person (`SEND`).

A held reply is a `waiting` review item and an internal note, *«name» has a reply waiting for review*,
with a link.

## The hand-off

A hand-off writes the marker in `handed_off`, drops the waiting requests, writes an internal note
with the summary and assigns the conversation to the role's hand-off person, or clears the assignee when
it is the agent's. It is done by the tool, a refused reply, a cap, a discarded or expired item (next
reviewer, an expiry or a withdrawal), the budget, a guard, an unreadable message, a lapsed run or a model that cannot be reached.
The marker is cleared when a person replies in the conversation (the `replied` event) or assigns it,
or by `DELETE …/hand-off`. A hand-off made by the trigger, the budget, a decision or a worker has no dispatcher
to write its note: the run is recorded as `handed_off` with no outcome, and the runner completes it
within five seconds.

## The budget

`daily_token_budget` counts the input and output tokens of the agent's model turns and of its content
checks, on the business day, in `usage_days`. A run is judged before every model turn: at the budget it
ends handed off with the note *Daily budget reached.* The runner also hands off every request that
waits for an agent at its budget. The next day the agent starts again, and only on new messages.

## The customer scope

An operation that returns a customer's data declares `x-vantigo-customer-scope` in its
contract (a `param`, naming the path or query parameter that holds the customer, or a
`resource` naming a field of the answer). The dispatcher then ties every call under an
agent's run to the **run's customer**: a list is filtered to it, and a read of another
customer's record is refused. A run with no customer reads none of them. The scope is what
makes S2 certain: an agent never reads another customer's data.

## AI tasks

| Task | Display | Subject | What it does |
| --- | --- | --- | --- |
| `agents.run` | Agent at work | per agent | Runs an agent on a conversation. Sent the conversation's messages, the persona and instructions, and what the allowed operations answer for the run's customer. Needs a model that supports tool calls. |
| `agents.check` | Agent content check | none | Checks a draft against a fixed rubric before it is sent. Sent the instructions and the draft, never the conversation. Uses the workspace's choice. It is also the model of the [weekly check](#feedback), which is sent the role's instructions and the reasons and texts of the corrections. |

The model for an agent is chosen on its Persona and stored as the per-subject choice for
`agents.run`; it wins over the task's own ([Configuration](/en/admin/configuration/#the-assistants-model)).

## Audit

Every Owner-tier and configuration change is written to `agents.events` in the same
transaction, with the actor and the before and after: `settings.changed`, `agent.created`,
`agent.changed`, `agent.paused`, `agent.active` (resume), `agent.deleted`,
`permission.changed` (target `agentId key`), `role.created`, `role.changed`,
`role.mode.changed` (the reply mode or threshold moved), `role.channels.changed`,
`role.deleted`, `role.instructions.suggestion_applied` (the role's snapshot before and after, with the instructions' length and the suggestion's id, never their text), `role.instructions.suggestion_dismissed`, `memory.deleted` and `settings.retention_lowered` (the nightly pass lowered the retention to
Communications' own; the actor is the nil user). `agent.changed` carries the agent's model. A
change of a mode, a level, a model, a role or the settings is therefore audited with the user who made
it, and `GET /audit` lists them. Identity's audit is Identity's alone; a module may not write
another's schema.

A call made under an approval is in the action log with `approved_by` set to the reviewer, and the
grant it was made under carries `approved_by_user_id` (`Via.ApprovedBy`). A module's own audit that
reads `Via` is not built; the message Communications stores names the agent as its author.

## The review queue

An item is a reply, a change or a **start** that a run left for a person (`review_items`). A reply carries the
agent's text and its disclosure line apart; a change carries the operation with its arguments and the
preview; a start (`kind` `start`, from `start_conversation`) carries the text and the disclosure line apart and the
channel, the address and the subject, with `conversationId` the nil uuid since no conversation exists yet. Approving a start
(or editing its text and sending) judges again that the role may still start conversations on the channel and that the
address is still a contact of the customer, applies S1, S2 and C1, and sends through `POST /conversations` with the item's
idempotency key; discarding one hands nothing off.

**Seeing.** Listing or opening an item, a run and its transcript need `agents:review` (`agents:activity`
for a run) **and** `communications:conversations-view` **and** every read permission of the template's
needs, because an item shows what the agent read. A **list** holds the items and runs of the templates the caller
may see, and is 403 `reviewer_not_permitted` only when there are none; opening one of a template the caller
may not see is 403. The count needs `agents:review` alone. The handler-level conditions found on the front desk's
allowlisted operations (Invoices without `invoices:payments` or `invoices:issue`, Customers without the
legal identity) only narrow what is shown, and both of those permissions are in the never list, so the
agent saw the narrowed form.

**Deciding.** `If-Match` carries the item's version. A decision on an item that is no longer waiting is
409 `item_decided`; at a version that moved on, 409 `version_conflict`. The reviewer must also satisfy the
access rule of the operation the item would call, permission keys and `policy:` rules alike
(`contracts.Access.UserSatisfies`): `postCommunicationsConversationsByIdReply` for a reply, the change's own
operation for a change. `UserSatisfies` is `UserHolds` for a permission rule; for a policy it decides the
user's roles as they stand and assumes no second factor, so `Owner` is met by an Owner only while the
installation does not require one for owners.

### How a decision is made

**Claim.** Before anything is called or sent the decision claims the item (`waiting` to `deciding`, by the version read). Only one claim wins; the loser is 409 `item_decided`. A refusal or a hold gives the claim back; a claim never finished goes back to waiting after ten minutes, and an item being decided is never expired. A change that takes an `Idempotency-Key` is called with `agents-item-<id>`.

**Approve and edit and send** run as the agent: a grant for the agent and the item's run, with the item's
customer and `approved_by` the reviewer (ten minutes, revoked after). Then:

1. The conversation is read as the agent. One the agent cannot read, or the agent deleted or its role gone,
   withdraws the item.
2. A conversation assigned to someone else is `conversation_taken`; a newest inbound message that is not the
   one the reply answers is `conversation_moved`, and a new run is asked for. Both withdraw the item.
3. The text sent is the stored one, or the reviewer's, always under the stored disclosure; the channel's
   limits (`maxText`, components) apply to the whole.
4. S6 refuses a closed window or one where only `human_agent` is left: the item is withdrawn and handed off.
   S1, S2 and C1 are judged again on the text about to go. **A rule that already held the item is the
   reviewer's to decide** and does not hold the approval; one that is new does, and C1 always does. The
   content check is asked again on the full text. A hold is 422 `held` and the rules are added to
   `held_by`.
5. `postCommunicationsConversationsByIdReply` is called with `inReplyToMessageId` and the `Idempotency-Key`
   `agents-<agent>-<conversation>-<message>`, the key a run's own send uses, so concurrent approvals, a retry
   after `send_unknown` and the run itself send one message. The call is logged in `actions` with
   `approved_by`.
6. Communications' own refusals (`conversation_taken`, `conversation_moved`, `reply_window_closed`) withdraw
   the item as above; a 4xx the model could fix is 422 `channel_refused`; a 5xx or no answer is 502
   `send_unknown` with the item left waiting.

The item is then `sent`, or `edited_sent` with the character-level `edit_ratio` of the change (the
Levenshtein distance over the longer text, cut at 4,000 characters). The day's `sent` count moves.

**A change** is checked again before it is made: the operation must still be on the role's allowlist,
must not be one of the module's own tools, its path is pinned to the run's conversation, and the
conversation patch takes `status` alone. The dispatcher's gate is asked for the mode now: *never* (a level
was lowered, the role lost it) withdraws the item and hands off. Then it is called as the agent with the
confirmation the reviewer's approval is. A 2xx makes the item `sent` with the status as its `outcome`; a
4xx is 422 `change_refused`; a 5xx or an error is not retried (a change is not idempotent): the item is
withdrawn and a colleague is told to check.

**Discard** records the reason and text, counts `discarded` and, for a reply, writes the hand-off.

**A newer reply supersedes** the one that waits: queuing a reply withdraws the conversation's other
waiting replies.

A hand-off note never quotes the draft: it says why and links `/agents/review/<id>`, which is gated. A hand-off never takes a conversation from a person who has it (it changes the assignee only when it is empty or the agent's own), and the note then says the agent stopped working on it.

Expiry, withdrawals and discards hand off by recording the marker and a run handed off with no outcome,
which the runner completes with the note and the assignee ([the hand-off](#the-hand-off)).

A new item writes an internal note on the conversation (*Hanna has a reply waiting for review:
/agents/review/&lt;id&gt;*) and mails the role's hand-off person (`UserDirectory.Notify`, in the person's
language, with a link when the installation has an origin).

## Memory

V27. The tool `remember(text)` writes a note for the **run's customer** (`subject_type` `customer`, the
customer's number). A run with no customer keeps none (`no_customer`): a note about a stranger's conversation
would be a way to plant text for the next run, so no `conversation` note is written (the subject type and the
reading and erasure of one stay in the schema). The caps are checked and the note written in one transaction
under advisory locks per run and per customer, so concurrent calls cannot pass them together: at most 3 notes a
run (`too_many_notes`), 500 characters (`note_too_long`), 50 for the customer (`memory_full`, the tool answers
that a person must delete a note; nothing is evicted), and a note already held is `already_remembered`.

At the start of a run, after the run's customer is settled (V9b) and before the first model turn, the run
reads the agent's own notes: those about **the run's customer only** and those about the run's own
conversation, 20 at most, newest first, and puts them in the conversation's data block under `memory`. A
customer's notes are therefore never read by a run for another customer or for none, which is what keeps S2
true; the read is logged as an action `memory:customer` with `scoped` set, so S1 treats a reply of that
run as carrying a customer's data, and the operation is no source a reply may cite. The prompt tells the model
the notes are data. The `promptVersion` moved to `2026-11-08.2`.

`contracts.AgentMemory` (`apps/server/internal/contracts/agent_memory.go`, the single-provider slot
`Deps.AgentMemory`, provided by this module and nil when it is off) answers `AboutCustomer`: one entry per
agent, with its notes newest first and `Full` when it holds 50. It performs no authorization; Customers consumes it for
`GET /customers/{id}/agent-memory` ([Customers](/en/reference/customers/)). Deleting a note is
`DELETE /memory/{noteId}`.

## Feedback

V28. The API process's runner `agents-feedback` (a `module.Runner`, because it asks the AI client, which only
the API process has) runs hourly and checks each role of an active agent once a week: from Monday 06:00 in
the business time zone, `roles.feedback_checked_at` is claimed with a conditional update so that replicas
share the work. It reads the role's corrections since the last check (28 days at the first): replies
`discarded` and replies `edited_sent` with an `edit_ratio` above 0.20, whose original text the item keeps in
`item.agentText`. The role is claimed first (`feedback_checked_at`), whatever there is to read, so a role with fewer than three corrections is looked at once a week and not counted again every hour; the corrections it left stay for the next week, because `feedback_since` moves only when they were read. Otherwise it asks the
`agents.check` model with the role's instructions and at most 40 corrections (each: the item id, the outcome,
the reason and note of a discard, or the text before and after of an edit, cut at 1,200 characters), never a
conversation, customer or person id. The answer is one JSON object; a suggestion is kept when its text fits,
its `oldText` occurs in the instructions exactly once (or is null), and it rests on at least two of the
items shown. At most three are proposed, and none while three wait. A failed check gives the claim back.

Applying a suggestion locks the agent and the role, checks the role's `If-Match` version, replaces the passage
(or appends a paragraph), saves the role through the same update as `PUT` on the role (the version moves
on) and records `role.instructions.suggestion_applied`. Nothing changes the instructions by itself.

## Calibration

A decided reply is a label for its score: `sent` is good, `edited_sent` good with edits (bad when its
`edit_ratio` is above 0.20), `discarded` bad. A reply the agent **sent by itself** is a label too: when a
person's reply in the same conversation (`conversation.replied` with `byAgent` false) arrives within 24
hours of its `sent_at`, the run gets `answered_over_at` and counts as bad; one that has stood 24 hours
unanswered counts as good (weak evidence: silence is not approval), a younger one as nothing. Only the newest Auto reply of a conversation is marked: the ones before it were not what the person answered. `GET /roles/{roleId}/calibration` reads the role's last 200
labelled replies (changes, waiting, withdrawn and expired items are no labels) and answers, for each X from
50 to 100, `autoCount` and `autoShare` (score at least X and no rule in `held_by`), `badCount` and
`badShare` (the share of those that are bad) and, once, `heldCount` (replies a rule held, whatever the score;
`SEND`, `TAKEN` and `MOVED` are not rules). With fewer than 50 labelled replies it answers
`notEnoughHistory: true` and the count, and no points.

## Retention and erasure

- **Retention.** `settings.retention_months` is the age at which the `agents-retention` worker (hourly)
  deletes runs (never one with an item still waiting or being decided), which cascade to their transcripts, action logs and review items, and the hand-off markers,
  taken run requests, senders and conversation links of that age. The cutoff is the later of the months
  and Communications' `COMMUNICATIONS_RETENTION_DAYS`. The setting itself is lowered, on save and by the
  worker (audited as `settings.retention_lowered`), to `retentionCapMonths`: that value divided by 30,
  between 1 and 60. `usage_days` is deleted after 24 months.
- **A customer's erasure.** The module implements `contracts.CustomerPersonalData` and
  `contracts.ContactDataEraser`. `EraseCustomerData` deletes, in the Customers transaction, the review items
  and runs by `customer_id` and by every conversation in `conversation_links` for the customer (and so
  the transcripts and actions), the markers, requests and senders of those conversations and their links,
  and sets the customer's id in `actions` to null. `EraseContactData` does the same for the conversations
  whose `contact_ids` hold a deleted contact. Both report counts by kind (`agents.runs`,
  `agents.runMessages`, `agents.reviewItems`, `agents.actions`) and report zeros when run again.
  `ExportCustomerData` hands over the runs and the replies. A conversation the module never saw linked is
  found by retention instead.
- **Memory and suggestions.** `memory` is deleted with the cutoff above, whatever the note's age relative to
  the run, and with an agent that is deleted. An erasure deletes the customer's notes, the notes about the
  conversations it erases, any note a run of those wrote, and the suggestions whose `item_ids` hold an item it
  erases; it reports `agents.memory` and `agents.instructionSuggestions`. `ExportCustomerData` includes the
  customer's notes.
- **A person's erasure** needs nothing here: `decided_by_user_id` and `approved_by` are opaque ids.

## The workers and the runners

| Worker | Interval | What it does |
| --- | --- | --- |
| `agents-runs` | 30 seconds | Re-queues a run whose claim lapsed once and hands it off the second time; hands off requests that waited more than a day. |
| `agents-reviews` | 1 minute | Expires waiting items past `expires_at` and hands their conversations off. Skips locked rows, so replicas share the work. |
| `agents-retention` | 1 hour | Lowers a retention above the cap, deletes what is older ([retention](#retention-and-erasure)), including taken wakeups. |
| `agents-wakeups` | 30 seconds | Writes the schedules' occurrences and turns due [wakeups](#wakeups) into run requests. |

The runner (an API-process runner, not a worker) finishes the hand-offs the workers record. A second API-process runner, `agents-feedback` (hourly), makes the [weekly check](#feedback).

## Not yet

Sending a Vantigo document as an attachment is not built. Communications records no `Via.ApprovedBy`
itself: the reviewer is named by the agents action log and the grant.
