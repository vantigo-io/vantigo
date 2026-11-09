---
title: Virtual employees
description: Turning Virtual employees on — the modules and the AI provider it needs, its four permissions and which changes stay the Owner's, the company's name and the retention, creating the first agent, the model per agent, giving the agent Identity roles, the levels (Off by default, what the front desk needs, what is locked for every agent), the allowlist and the customer scope, what to keep out of Knowledge, the privacy notice, who reviews, the weekly check of the instructions, retention and erasure, pausing and deleting an agent; how to verify it, and what can go wrong.
sidebar:
  order: 52
sources:
  - apps/server/internal/agents
  - openapi/agents.yaml
  - apps/server/internal/db/migrations/00144_agents_baseline.sql
  - apps/server/internal/db/migrations/00146_agents_runs.sql
  - apps/server/internal/db/migrations/00147_agents_review.sql
  - apps/server/internal/db/migrations/00148_agents_memory.sql
  - apps/server/internal/db/migrations/00149_agents_roles.sql
  - apps/server/internal/config
  - apps/agents/frontend/src
---

The Virtual employees module (**Virtuelle ansatte** in Norwegian) lets the business
put AI agents to work under names of their own. This page covers turning it on and
setting it up; how people use it is in [Virtual employees](/en/user/agents/), and every
rule in the [reference](/en/reference/agents/). An agent that is set up answers the
conversations on its channels; the review queue's screens, with the calibration and the Usage
and Activity screens, come in the next release.

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
3. Set the **Retention**: how many months a run is kept, from 1 to 60. The default is 6. It is never
   longer than Communications keeps its messages (`COMMUNICATIONS_RETENTION_DAYS`, 365 days by default,
   so 12 months): a longer value is lowered to that when you save, and again every night if the
   installation later shortens Communications' own. The field shows the longest you may enter.
4. Save.

What the retention removes is described under [Retention and erasure](#retention-and-erasure).

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
- Every other permission that only an Owner can hand on: Entra and Google administration, Knowledge administration, and Orders' carriers and settings.
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

## How an agent runs

Runs happen in the **API process**, not in the workers, because they call the application's own
operations as the agent. A new message on a bound channel makes a request, and a replica takes it
after a wait of 20 seconds (at most two minutes after the first message of a burst). Check the
following.

- **`AGENTS_CONCURRENCY`** (default `4`, from `1` to `32`) is how many runs one replica carries at the
  same time ([Configuration](/en/admin/configuration/)). A run is one agent on one conversation, for at most
  five minutes, and holds one model call at a time. Raise it when conversations wait; lower it when your
  model server cannot take that many calls at once.
- **A replica that stops** in the middle of a run leaves it claimed; within a few minutes the run is
  taken over once, and the second time the conversation is handed to the hand-off person with a note.
- **Only one run** works on a conversation at a time. A message that arrives during a run makes a new
  request, which runs after it and sees both messages.
- **Without AI** (no provider, or a model that cannot call tools) a run fails as `ai_unavailable` and
  the conversation is handed off with a note; requests that wait more than a day are handed off too.

### The daily token budget

An agent can have a **daily token budget**, set on the agent (`dailyTokenBudget`; 0 removes it, and a
value below 1,000 is refused). It counts the tokens the agent's model and its content check use on the
business day. At the budget no new run starts, every request that waits is handed off with the note
*Daily budget reached.*, and the next day the agent starts again, on new messages only. The budget is
a field on the agent's Persona tab (empty for none); the **Usage** tab shows the last 30 days against it.

### Which channels

The channels the agent answers are the ones you bind to its role. An agent never answers a message from
another channel of your installation, so two agents bound to two channels cannot answer each other; the
module learns a channel's address the first time a run reads a conversation on it. Messenger replies
follow the 24-hour window: when it has closed, a person writes the reply.

### The other roles

- **Sales follow-up.** Needs **Orders** (the quote reads) and a channel of its own, which no other role
  answers: it writes first from that channel, and the customer's reply goes to the Inbox for a person.
  Set **Days after a quote is sent** (the default is 7). The quote must be *sent* in Orders; accepting it
  takes the follow-up away. The agent writes only to a contact address of the quote's customer; Orders'
  quote reads are tied to that customer like the other customer reads.
- **Reminder replies.** The front desk for the invoice mailbox: bind the channel, and give the role
  Invoices' reads.
- **Timesheet reminders.** Needs **Time**. From the Tuesday after a week, in the company's time zone,
  Time announces each person who did not submit that week (a person with draft entries in it, or one who
  submitted any of the eight weeks before it), once. The role reads nothing of the person's hours: the
  notice says the week and the hours registered. It needs the installation's e-mail to be set up, since
  it writes through Vantigo's notifications: where e-mail is not configured, nothing is sent.
- **Project digest.** Needs **Projects**. Set a **schedule** and **Send the note to**; without a person
  the role has no one to write to and hands off. The recipient must hold the project permissions the digest reads
  with, since the note carries what the agent read: a person without them is refused when you save the role and
  when the note is sent. The schedule is in the company's time zone (set under
  the workspace), weekly or monthly, and an occurrence is sent at most 12 hours late.

A hand-off of a role that has no conversation (the quote, the week, the schedule) mails the role's
hand-off person a link to the run, not what the agent found, since the mail leaves Vantigo.

### Reply modes, in short

Start every role on **Review**. Move a role to **Auto above X %** or **Auto** only when its replies have
been right for a while. The rules that protect a customer's data and the opening line that says the
agent is an AI hold in **every** mode, Auto too ([the reference](/en/reference/agents/#the-pipeline)).

## Who reviews

A reply or a change an agent leaves for a person waits in the **Review queue** (the *Review virtual
employees' work* permission, `agents:review`) for 24 hours; a Messenger reply for no longer than 15
minutes before its window closes. Choose the **hand-off person** of each role carefully: when an item
is left for review that person gets an e-mail (through the installation's mail driver; nothing is sent
where none is configured) and a note appears in the conversation, and a role with no hand-off person
notifies nobody, so someone has to look at the queue, whose count is on the dashboard's
*Virtual employees* tile.

Give `agents:review` to people you would trust with the agent's work. An approval is made **as the agent,
on the reviewer's authority**: the reviewer has to be allowed to do what the item does (to send a reply,
`communications:conversations-reply`; to approve a change, the permission of its operation, with
Owner-tier policies checked as well), so a reviewer cannot give the agent's access to a person who
lacks it. Reading an item or a run also needs `communications:conversations-view` and the read
permissions the Front desk needs, because they show what the agent read: customers, contacts, orders,
invoices, products and Knowledge. Give them to the reviewers; withholding one hides the queue from
that reviewer, and nothing else.

Reading an agent's runs and transcripts is a separate, sensitive permission
(`agents:activity`), because a transcript holds the customers' messages.

## Memory and the weekly check

Two things are kept beyond a run, and both go to the model.

- **Notes.** An agent writes short notes about a customer (none when the conversation has no customer) and reads
  the newest 20 at the start of the next run. They reach the model as data with the conversation. A note is
  what a model wrote after reading a customer's message, so it can be wrong or planted; it is shown to people
  on the customer's page, where someone with `agents:manage` can delete it, and it is never an instruction
  or a source for a reply. An agent keeps at most 50 per customer; when it is full it refuses new notes and a person
  deletes one, the customer page says so.
- **The weekly check.** Once a week an API process asks the model of the AI task **Agent content check**
  (`agents.check`) to read what people corrected for each role: the reasons they discarded replies, and the text
  before and after the edits beyond 20 %. It is sent no conversation and no customer's id, but **the texts of the
  replies and the notes reviewers wrote when they discarded one are sent to the model**, and they can mention a
  customer, so the privacy notice above applies to it too. It proposes at most three
  changes to the role's instructions, which an Owner or someone with `agents:manage` applies or dismisses
  on the role page. With no AI provider the check is skipped. You can change the model on the AI models page.

## Retention and erasure

- **Runs.** A run, with its transcript, its action log, its review items and the hand-off markers of its
  conversation, is deleted after the retention months, and never kept longer than Communications keeps
  its messages, because a transcript copies message bodies. The worker `agents-retention` does it every
  hour, in batches. The day's **usage** (counts only) is kept for 24 months.
- **Notes and suggestions.** The notes an agent remembers and the suggestions of the weekly check expire with
  the runs, after the same retention. Deleting an agent deletes its notes.
- **Items that wait** are never deleted by retention; they expire first (24 hours), and an expired item
  hands the conversation off. The worker `agents-reviews` does this every minute.
- **A customer's erasure.** When a private person's customer is anonymised in Customers, the module
  deletes that customer's notes, the suggestions that rest on their replies, and their runs, transcripts and review items, by the customer and by **every conversation
  of theirs it has seen linked to them**, so a conversation linked after its run began is covered; and it
  blanks the customer's id in the action log. A deleted **contact** takes the conversations they took part
  in with them, and the notes written in them. The export of a customer's personal data lists the runs and replies held for them. A
  conversation that was linked to the customer only after the module last looked at it is not known to the
  module: it is removed by retention instead.
- **A person's erasure** (a reviewer or an Owner) keeps the id of their decisions as an opaque id, so the
  history still says that a person decided; nothing about the person is kept with it.

## Pause and delete an agent

- **Pause** stops new runs; roles, channels and levels stay. **Resume** starts them
  again.
- **Delete** withdraws what waits for review and hands those conversations off, stops the roles, frees
  the channels for another role, ends the agent's grants and disables its user. The user is kept so earlier history still reads true.
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
| An agent does not answer a conversation | The conversation is assigned to a person, the agent is paused, the conversation was handed off, the message was automatic, or the permissions the role needs are still Off. | Assign it to the agent, or give it back ([Virtual employees](/en/user/agents/#hand-off)); resume the agent; raise the Needs. |
| Every run ends in a hand-off with *The AI model did not answer* | The provider is down, or the agent's model cannot call tools. | Check `AI_PROVIDER` and choose another model on the Persona tab. |
| *Daily budget reached.* | The agent used its daily token budget. | Raise the budget, or wait for the next business day. |
| The review queue says you may not see an item | Reading an item needs `agents:review` and also `communications:conversations-view` and the read permissions the Front desk needs. | Give the reviewer the missing permission. |
| `reviewer_not_permitted` on Send or Approve | The reviewer may not do what the item does (for a reply, `communications:conversations-reply`). | Give the permission, or let a colleague decide. |
| An item is *withdrawn* and the conversation handed off | A colleague took the conversation, a newer message arrived, the Messenger window closed, or the agent's level was lowered since. | Nothing is wrong; a person answers it, or a new run answers the newer message. |
| An approval says a rule holds it | The text now says its writer is a person, or customer data would go to someone who is not the customer's contact. | Edit the text, or discard it. |
| A retention lower than you saved | Communications keeps its messages for a shorter time. | None: the retention follows it. |
| A Sales follow-up did nothing | The quote was only saved (not *sent*), the wait has not passed, the agent is paused, the role has no channel of its own, or the customer has no contact with an e-mail address. | Check the run on the agent's Activity tab. When the role is in Review, the conversation it would have started is in the review queue. |
| A schedule did not wake the role | The worker looks every 30 seconds. A schedule set after its time has passed waits for the next occurrence, an occurrence more than 12 hours late is skipped, and the company's time zone decides the weekday and the hour. | Wait for the next occurrence, or check the time zone under the workspace. |
| A reminder or a digest did not arrive | E-mail for notifications is not configured, the person is not active, there is no person on the Project digest, or the message was stopped by the rule that it must not claim to be a person. | Set up e-mail, choose the person, and read the run. |
