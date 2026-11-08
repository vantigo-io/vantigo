---
title: "Agents module"
description: "The Agents module (nb Virtuelle ansatte): virtual employees that do part of a person's work — the module and what it requires; the four permissions; the eighteen endpoints with their permission, statuses and refusal codes; the data model (settings, agents, permission_levels, roles, role_channels, events); the gate that applies an agent's levels and role allowlist to the operations dispatcher; the front desk template with its allowlist, needs and hard rules; the customer scope; the two AI tasks; the Owner-only changes; and the audit events."
sidebar:
  order: 135
sources:
  - apps/server/internal/agents
  - openapi/agents.yaml
  - apps/server/internal/openapi/specs/agents.yaml
  - apps/server/internal/db/migrations/00144_agents_baseline.sql
  - apps/agents/frontend/src
---

The Agents module holds **virtual employees**: AI users that do part of a person's work.
The screens are in the [user guide](/en/user/agents/) and the setup in the
[administration guide](/en/admin/agents/). This page describes the module as the code
implements it. Its API reference is generated from `openapi/agents.yaml`.

This delivery holds the agents with their persona, their roles bound to channels, the
levels the Owner gave their permissions and the gate that applies them. The trigger, the
runs and the review queue come after; the tables and endpoints of this page are all that
exist today.

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
| `PUT /agents/{agentId}` | `agents:manage` | 200, 400, 404, 409 | Replaces the persona. `If-Match` carries the version; 409 `version_conflict`. The user is renamed and the model follows. |
| `DELETE /agents/{agentId}` | `agents:manage` | 204, 404 | Marks the agent deleted, frees its channels, ends its grants, disables its user and forgets its model. |
| `GET /agents/{agentId}/avatar` | `agents:view` | 200, 404 | The picture (PNG or JPEG), private and not sniffable. |
| `PUT /agents/{agentId}/avatar` | `agents:manage` | 200, 400, 404 | Sets the picture, multipart field `avatar`, at most 5 MiB. |
| `POST /agents/{agentId}/pause` | `agents:manage` | 200, 404 | Active to paused. Pausing a paused agent is not an error. |
| `POST /agents/{agentId}/resume` | `agents:manage` | 200, 404 | Paused to active. |
| `PUT /agents/{agentId}/permissions/{key}` | `agents:manage` | 200, 400, 404, 409 | Sets the level `off`, `read` or `change`. 403 `owner_required` when raising and not the Owner; 409 `level_locked` for a key in the never list; 404 for a key the catalog lacks. |
| `POST /agents/{agentId}/roles` | `agents:manage` | 201, 400, 404 | Adds a role from a template, in `review` with no channel. Empty instructions take the template's. |
| `PUT /agents/{agentId}/roles/{roleId}` | `agents:manage` | 200, 400, 404, 409 | Replaces the role. `If-Match`; 403 `owner_required` when it loosens the mode or lowers the threshold outside `review`. |
| `DELETE /agents/{agentId}/roles/{roleId}` | `agents:manage` | 204, 404 | Removes the role and its channels. |
| `PUT /agents/{agentId}/roles/{roleId}/channels` | `agents:manage` | 200, 400, 404, 409 | Replaces the role's channels (at most 50). 409 `channel_bound` when another role holds one. |
| `GET /agents/{agentId}/setup` | `agents:view` | 200, 404 | What the agent's roles need against the levels it has, and whether its Identity roles hold each permission. |
| `GET /settings` | `agents:view` | 200 | The company's name, the retention, the version, `configured` and the models the installation allows. |
| `PUT /settings` | `agents:manage` | 200, 400, 409 | Changes the company's name (1 to 120 characters) and the retention (1 to 60 months). `If-Match`. |
| `GET /templates` | `agents:view` | 200 | The role templates: default name and instructions, allowlist, needs and hard rules. |

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
| `ai_unavailable` | 503 | Declared by the contract for a run with no AI provider; no endpoint of this delivery answers it. |

The fields: an agent's `name` 1 to 60 characters; `style` at most 1,500; `languages`
`customer`, `nb` or `en`; `disclosureWording` `assistant`, `talking` or `compact`; `model`
empty or one the installation allows. A role's `name` 1 to 80; `instructions` at most
8,000; `replyMode` `review`, `auto_above` or `auto`; `threshold` 50 to 100;
`capsToolCalls` 1 to 60; `capsTurns` 1 to 16; `maxRepliesPerDay` 1 to 50;
`handOffUserId` an active person. A role's `handOffWarning` answers `none`, `disabled`,
`not_a_person` or `no_permission`; for anything but `none` a hand-off goes to the Inbox,
unassigned.

## Data model

The `agents` schema. A user, a channel and a hand-off person are opaque ids.

| Table | What it holds and its constraints |
| --- | --- |
| `settings` | One row (unique index on `(true)`). `company_name` varchar(120), empty until set; `retention_months` 1 to 60, default 6; `version`, `updated_at`, `updated_by_user_id`. |
| `agents` | `id`, `user_id` (unique), `name` 1 to 60, `style` at most 1,500, `languages` (`customer`, `nb`, `en`), `disclosure_wording` (`assistant`, `talking`, `compact`), `state` (`active`, `paused`, `deleted`), `daily_token_budget` (null for none, else above 0), `version`, timestamps and `created_by_user_id`. A deleted row stays so the audit's references stay true. |
| `permission_levels` | `(agent_id, permission_key)` key, `level` (`off`, `read`, `change`). Only what was given is stored; **off deletes the row**. Cascades with the agent. |
| `roles` | `template` (`front_desk`), `name` 1 to 80, `instructions` at most 8,000, `reply_mode` (default `review`), `threshold` 50 to 100 (default 85), `caps_tool_calls` (default 30), `caps_turns` (default 8), `max_replies_per_day` (default 5), `hand_off_user_id`, `version`. Cascades with the agent. |
| `role_channels` | `(role_id, channel_id)` key with `channel_id` unique: a channel belongs to at most one role. |
| `events` | The Owner's changes: `at`, `actor_user_id`, `action`, `target`, `before` and `after` as jsonb. |

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

`agents:activity`, `agents:manage`, `agents:review`, `agents:view`,
`customers:personal-data`, `entra:manage`, `expenses:approve`, `google:manage`,
`identity:manage`, `invoices:issue`, `invoices:payments`, `knowledge:manage`,
`orders:carriers-manage`, `orders:settings`, `staff:approve`, `staff:manage`,
`staff:personal-data`, `time:approve`.

A test in `internal/integration` holds the list to the composed catalog by key patterns
(`identity:*`, `agents:*`, `*:approve`, `*:personal-data`, `invoices:issue` and
`payments`, `staff:manage`): a new permission that matches one and is not listed fails it.
A second test requires every permission that cannot be delegated to be listed or waived
with a reason.
The Assistant's API is not on the list because it names no permission, and the dispatcher
refuses it by its path.

## The front desk template

The one template of this delivery, `front_desk` (default name *Front desk*). It answers
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
| `getInvoices` | yes | |
| `getInvoicesById` | yes | |
| `getProducts`, `getProduct`, `getProductsByIdVariants`, `getProductsByIdVariantsByVariantIdPrices` | | |
| `searchKnowledge`, `getKnowledgeArticleText` | | |

Shipments are not on it: Orders' shipments name no customer, so they cannot be tied to
one.

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

The rules are listed on the template now and applied by the pipeline that comes with the
runs.

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
| `agents.check` | Agent content check | none | Checks a draft against a fixed rubric before it is sent. Sent the instructions and the draft, never the conversation. Uses the workspace's choice. |

The model for an agent is chosen on its Persona and stored as the per-subject choice for
`agents.run`; it wins over the task's own ([Configuration](/en/admin/configuration/#the-assistants-model)).

## Audit

Every Owner-tier and configuration change is written to `agents.events` in the same
transaction, with the actor and the before and after: `settings.changed`, `agent.created`,
`agent.changed`, `agent.paused`, `agent.active` (resume), `agent.deleted`,
`permission.changed` (target `agentId key`), `role.created`, `role.changed`,
`role.mode.changed` (the reply mode or threshold moved), `role.channels.changed` and
`role.deleted`. Identity's audit is Identity's alone; a module may not write another's
schema.

## Not yet

The trigger on a conversation, the runs and their transcripts, the review queue, the token
budget (`daily_token_budget` exists and is unused), the Usage and Activity tabs'
contents and the retention sweep come in later deliveries. `agents:review` and
`agents:activity` are declared now so roles can be built ahead of them.
