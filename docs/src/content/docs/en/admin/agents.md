---
title: Virtual employees
description: Turning Virtual employees on — the modules and the AI provider it needs, its four permissions and which changes stay the Owner's, the company's name and the retention, creating the first agent, the model per agent, giving the agent Identity roles, the levels (Off by default, what the front desk needs, what is locked for every agent), the allowlist and the customer scope, what to keep out of Knowledge, the privacy notice, pausing and deleting an agent; how to verify it, and what can go wrong.
sidebar:
  order: 52
sources:
  - apps/server/internal/agents
  - openapi/agents.yaml
  - apps/server/internal/db/migrations/00144_agents_baseline.sql
  - apps/agents/frontend/src
---

The Virtual employees module (**Virtuelle ansatte** in Norwegian) lets the business
put AI agents to work under names of their own. This page covers turning it on and
setting it up; how people use it is in [Virtual employees](/en/user/agents/), and every
rule in the [reference](/en/reference/agents/). In this release an agent can be set up
and prepared. The trigger that makes it answer a message, the record of its runs and the
review queue come in a later release.

## Before you start

- **`agents` in `MODULES`, with `communications`.** `MODULES` refuses `agents` without
  `communications` at startup (`agents requires communications`): an agent answers
  conversations. When `MODULES` is unset every module is on.
- **An AI provider with a model that supports tool calls.** Set `AI_PROVIDER` and the
  rest as described in [Configuration](/en/admin/configuration/#the-assistants-model). A
  model that cannot return tool calls cannot work the way an agent does. Without a
  provider the app shows *AI is not set up* and no agent can be run.
- **An Owner.** Raising a level, choosing Auto and binding a channel are the Owner's.
- **At least one Communications channel** for the agent to answer, set up as described
  in [Communications](/en/user/communications/).

## Permissions

Give roles the permissions under **Roles & access**. Nobody has them by default.

| Permission | What it opens | Who can hand it out |
| --- | --- | --- |
| `agents:view` | See the agents, their personas, roles and settings. | Anyone who can edit roles. |
| `agents:manage` | Create agents, edit personas and instructions, pause and delete them. | Sensitive: an Owner only. |
| `agents:review` | Approve or discard what an agent proposes in the review queue. | Anyone who can edit roles. |
| `agents:activity` | Read an agent's runs and transcripts, which hold customers' data. | Sensitive: an Owner only. |

Three changes stay the **Owner's** even for someone with `agents:manage`, because a person
with narrow roles of their own could otherwise steer an agent with broad ones: raising
a level on the Permissions tab, choosing **Auto** or **Auto above X %** (or raising it), and
binding a channel. Lowering and unbinding are not.

## Set the company's name

The agent introduces itself with the company's name, so no agent can be created before
it is set.

1. Open **Virtual employees** and its settings.
2. Enter the **Company name** (1 to 120 characters) as customers know the business.
3. Set the **Retention**: how many months a run is kept, from 1 to 60. The default is 6.
4. Save.

Runs are not recorded in this release, so the retention has nothing to remove yet; it
starts to apply with the release that records them.

## Create the first agent

1. Check that the settings above are saved.
2. Press **New agent**, fill in the **Persona** and press **Create**.
3. On **Roles**, press **Add role**, choose the **Front desk** template, write or keep
   the instructions and choose the hand-off person.
4. Bind the channels the role should answer. A channel belongs to one role at a time.
5. Give the agent's user its Identity roles under **Roles & access** ([below](#give-the-agent-identity-roles)).
6. On **Permissions**, press the raise buttons under **Needs** until the list is
   satisfied ([the levels](#the-levels)).
7. Leave the Reply mode on **Review**.

## The model per agent

Under **Workspace admin** › **Integrations** › **AI models** two tasks of this module
appear:

- **Agent at work** (`agents.run`) runs an agent on a conversation. It is chosen
  **per agent**: the task's row there keeps the workspace's choice, and each agent can
  have a model of its own on its **Persona** tab, which wins over it
  ([Configuration](/en/admin/configuration/#the-assistants-model)). It needs a model
  that supports tool calls.
- **Agent content check** (`agents.check`) checks a draft against a fixed rubric before
  it is sent. It uses the workspace's choice and is sent only the instructions and the
  draft, never the conversation.

## Give the agent Identity roles

An agent is a user, and it holds Identity roles like a person. Under **Roles & access**
give its user the roles that hold the permissions it should use, such as reading
customers and replying in Communications. A level cannot give a permission the agent's
roles do not hold. An agent may never hold **Owner**, **SystemAdmin**, a delegation or
stewardship; the screen refuses them
([Workspace administration](/en/user/workspace-administration/#agents-among-the-users)).

## The levels

Every permission of an agent is **Off** until the Owner raises it to **Read** or
**Change**. The **Needs** list of an agent's Permissions tab shows what its roles need.
The **Front desk** needs:

| Area | Permission | Level |
| --- | --- | --- |
| Communications | Conversations view, reply and manage | Change |
| Customers | View, contacts view, associations view | Read |
| Orders | Access | Read |
| Invoices | Access | Read |
| Products | Products, variants, pricing, categories and tax categories view | Read |
| Knowledge | Use Knowledge (articles) | Read |

A write that asks for confirmation is refused when the level is only **Read**, which is
why the conversation permissions are **Change**.

### What is locked for every agent

No level can be given for these; the screen shows them as locked and the server refuses
with `level_locked`:

- Staff: approve, manage and personal data.
- Customers: personal data.
- Identity: manage.
- Every approval in Time, Expenses and Staff.
- Invoices: issue and payments.
- The agents' own permissions.

### The allowlist and the customer scope

A role can call only the operations of its template, whatever its levels. The Front desk
can work its own conversation, read the conversation's customer, orders, invoices and
contacts, read Products and search Knowledge; it cannot reach Staff, Time or the rest
of the workspace. Everything that returns a customer's data is tied to the customer of
the conversation: **an agent never reads another customer's data**. The full list is in
the [reference](/en/reference/agents/#the-front-desk-template).

## What to keep out of the agent's reach

**Knowledge:** an agent can search articles it may read. Do not share articles that hold
customer data with an agent; mark them **Hidden from the Assistant** and an agent will not find them
([Knowledge](/en/user/knowledge/)).

## The privacy notice

Tell customers in the privacy notice that an AI assistant answers on these channels
and that the content of the conversation, the instructions and what the agent reads for
that customer are sent to the AI provider, as for any processor. The agent also says so
itself in the disclosure line of every message. Where the data goes for your installation
is described under [Configuration](/en/admin/configuration/#the-assistants-model); a model on your
own network sends nothing outside it.

## Pause and delete an agent

- **Pause** stops new runs; roles, channels and levels stay. **Resume** starts them
  again.
- **Delete** stops the roles, frees the channels for another role, ends the agent's
  grants and disables its user. The user is kept so earlier history still reads true.
  It cannot be undone.

## Verify

1. The agent is listed on **Virtual employees** as active, with its picture and the AI
   badge.
2. Its **Needs** list shows every row as satisfied and the setup is ready.
3. In Communications the agent appears, with the AI badge, in the assignee select.
4. The role page shows no warning beside the hand-off person.

## What can go wrong

| You see | Why | What to do |
| --- | --- | --- |
| `company_name_required` (*Set the company's name in the settings first*) | You create an agent before the company's name is set. | Save the name in the settings, then create the agent. |
| `channel_bound` | A channel is already answered by another role. | Unbind it there first, or choose another channel. |
| `owner_required` | You raised a level, chose Auto or bound a channel without being an Owner. | Ask an Owner. |
| `level_locked` | The permission is locked for every agent. | There is no level; leave it Off. |
| `version_conflict` | Someone saved the same agent, role or settings since you opened it. | Reload and make the change again. |
| *AI is not set up* | `AI_PROVIDER` is unset or the model cannot be reached. | Configure the provider ([Configuration](/en/admin/configuration/)). |
| A warning beside the hand-off person | The person is disabled, or may no longer see conversations. | Choose someone else. Until then handed-off conversations go to the Inbox, unassigned. |
