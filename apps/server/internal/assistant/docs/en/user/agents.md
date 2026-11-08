---
title: Virtual employees
description: The Virtual employees app — what an agent is, the agent at work (when it answers, what it reads, what it may send by itself, the opening line, the hand-off and giving a conversation back), the review queue (why an item waits, approving, editing and sending, discarding, how long it waits), calibration, usage and activity, creating an agent, its Persona (name, style, languages, model, picture) and the disclosure line with its three wordings, pausing, resuming and deleting, its Roles (the Front desk template, instructions, channels, the hand-off person, Reply mode and caps), its Permissions (everything Off until the Owner raises it) and where the AI badge appears.
sidebar:
  order: 75
sources:
  - apps/agents/frontend/src
  - apps/server/internal/agents
  - openapi/agents.yaml
  - apps/server/internal/db/migrations/00144_agents_baseline.sql
  - apps/server/internal/db/migrations/00146_agents_runs.sql
  - apps/server/internal/db/migrations/00147_agents_review.sql
---

A **virtual employee** is an AI user of Vantigo with a name of its own and a visible AI
badge. It does part of a person's work: it reads what a person with its roles could
read and writes where it has been allowed to. In this release it has one role, the
**Front desk**, which answers the conversations that come in on the channels the Owner
binds to it. The rules behind each screen are in the
[reference](/en/reference/agents/); how an administrator turns the module on is in
[Virtual employees](/en/admin/agents/).

## The agent at work

Once an agent has a role bound to a channel, and the Owner has raised the permissions the role needs
([Permissions](#permissions)), it answers the conversations that arrive on that channel, on e-mail
and on Messenger, as itself.

- **When it starts.** A short while after a customer's message: it waits 20 seconds, so three short
  messages make one answer, and never more than two minutes in all. A paused agent starts nothing.
  An automatic message (an out-of-office reply) is never answered.
- **Whose conversation.** It answers a conversation that is **unassigned or assigned to it**. A
  conversation assigned to a colleague is left to them. **Assign a conversation to the agent** and it
  answers it, also when it has been handed off.
- **What it reads.** The conversation, the customer's orders, invoices and contacts, the product catalog
  and the articles in [Knowledge](/en/user/knowledge/) that the agent may read. For a customer's data it
  reads only the customer the conversation belongs to. When the customer was linked by the customer
  linker and not by a person, the sender must also be one of the customer's contacts; otherwise the agent
  has **no customer** for that conversation and answers from Knowledge and the product catalog, asks who
  the customer is, or hands off.
- **What it writes.** A reply, to the person who wrote last and to nobody else: **never reply-all**, and a
  Cc is never copied. It can also add an internal note, tag the conversation and hand it off. A change to
  a record, such as closing the conversation, is **always reviewed first**.
- **Its score and its sources.** Every reply carries a score from 0 to 100 and the sources the agent read
  for it. A reply with no source it actually read gets the score 0, and the conversation itself never
  counts as a source.
- **The opening line.** Every message the agent sends starts with a line that says who wrote it and that
  it is an AI ([the disclosure line](#the-disclosure-line)). It cannot be removed.

### What happens to a reply

The **Reply mode** of the role ([below](#reply-mode)) decides, after the agent has written and the rules
have been checked:

- In **Review**, every reply waits for a person in the [review queue](#the-review-queue). An internal
  note in the conversation says that a reply is waiting, with a link to it, and the role's hand-off
  person is mailed.
- In **Auto above X %** the reply is sent when its score is at least X and no rule held it.
- In **Auto** it is sent unless one of the rules that always hold stopped it.

A reply is **held for a person in every mode** when it would send a customer's data to someone who is not
that customer's contact, attach another customer's document, or say that the agent is a person. It is
**not sent at all, and the conversation is handed off**, when the Messenger window is closed or only the
"human agent" extension is left (the agent never sends a message as a human agent), when the message it
answers is an automatic one or comes from one of your own channels, when the agent has already sent
**the role's number of replies today** in the conversation, or after six runs in an hour. In Review and
Auto above X % a reply is also held when it has no source, when it is the first message from a new sender,
when the conversation carries a complaint tag and when a second check finds a promise the instructions do
not allow. The tags that mean a complaint are set on the role ([Roles](#roles)); the daily token budget
on the agent's [Persona](#persona).

### Hand-off

An agent that does not know, may not decide, is asked for a person, reaches its limits or cannot reach its
AI model **hands the conversation off**: it writes an internal note with what the customer wants, and
assigns the conversation to the role's hand-off person, or to nobody (the Inbox). If it wrote an answer
that it could not send, the note holds it. From then on the agent **leaves the conversation alone**, also
when the customer writes again, until a person **replies** in it or **assigns** it, or it is **given
back to the agent**. In [Communications](/en/user/communications/#handed-off-conversations) a banner on
the conversation says that the agent handed it off, and why, and **Give back to the agent** hands it
back.

If a colleague takes a conversation while the agent is writing, the agent's reply is not sent: the
conversation is the colleague's.

## Who can use it

The **Virtual employees** tile in the app switcher is shown to everyone whose role has
**View virtual employees** (`agents:view`). **Manage virtual employees**
(`agents:manage`) lets you create, change, pause and delete agents. It is a sensitive
permission only an Owner can hand out. Some changes stay the Owner's even with
`agents:manage`: raising a Permission, choosing **Auto** or raising it, and binding a
channel. Reading an agent's runs needs **Read virtual employees' activity**
(`agents:activity`), and deciding what an agent proposes needs **Review virtual
employees' work** (`agents:review`). A reviewer who may not view the agents still sees the
**Review queue** in the sidebar. Reading a run or an item needs more than its own permission,
because it shows what the agent read ([The review queue](#the-review-queue)).

## Create an agent

1. Open **Virtual employees** and press **New agent**.
2. Fill in the **Persona** ([below](#persona)) and press **Create**.

The company's name must be set first, because the agent introduces itself with it
([The disclosure line](#the-disclosure-line)). If it is not, the screen refuses with
*Set the company's name in the settings first*; an administrator sets it under the
agents settings ([Virtual employees](/en/admin/agents/#set-the-companys-name)). The new
agent has no roles, no channels and every permission **Off**, so it can do nothing until
you finish the setup.

## Persona

Open the agent and choose the **Persona** tab.

| Field | What it is |
| --- | --- |
| **Name** | What customers and colleagues see, up to 60 characters. |
| **Style** | How the agent writes: tone, formality, words to use or avoid. Up to 1,500 characters. |
| **Languages** | **Customer** answers in the customer's language and falls back to bokmål; **Norwegian bokmål** and **English** always answer in that language. |
| **Model** | The AI model that runs the agent. Empty uses the workspace's choice for the task *Agent at work* ([Configuration](/en/admin/configuration/)). |
| **Picture** | A PNG or JPEG of at most 5 MB, shown on the agent's avatar. |
| **Daily token budget** | The most tokens (read and written) the agent may use in a business day, at least 1,000; empty for none. At the budget no new run starts and what waits is handed to a colleague with the note *Daily budget reached*. The next day it starts fresh, on new messages only. |

Changing the name renames the agent's user everywhere it is shown.

### The disclosure line

Every message the agent sends opens with a line that says it is an AI and who it works
for. You choose one of three wordings; the company's name from the settings fills in
the blanks.

| Wording | Norwegian | English |
| --- | --- | --- |
| **Assistant** | «Hanna» er «Hansen Rør» sin virtuelle assistent (KI). | «Hanna» is «Hansen Rør»'s virtual assistant (AI). |
| **Talking** | Du snakker med «Hanna», en KI-assistent hos «Hansen Rør». | You are talking to «Hanna», an AI assistant at «Hansen Rør». |
| **Compact** | «Hanna» (KI) · virtuell assistent, «Hansen Rør». | «Hanna» (KI) · virtuell assistent, «Hansen Rør». |

The compact line is the same in both languages. The line is added by Vantigo, not
written by the model, so the instructions cannot remove it.

## Pause, resume and delete

- **Pause** stops the agent from starting new runs. Press **Resume** to let it work
  again.
- **Delete** ends the agent for good: its roles stop, its channels are free for another
  role, its access ends and its user is disabled. The user is kept, so earlier
  history still shows who wrote it. A deleted agent cannot be brought back; create a new
  one.

## Roles

A role is a job the agent does. Open the **Roles** tab and press **Add role**. In this
release the one template is **Front desk**, which starts from instructions that are
polite, brief and concrete, and that hand over to a colleague when the agent does not
know or may not decide.

| Setting | What it does |
| --- | --- |
| **Name** | Up to 80 characters. |
| **Instructions** | What the agent should do and not do, up to 8,000 characters. Left empty, the template's instructions are used. |
| **Channels** | The Communications channels the role answers. A channel belongs to at most one role, so two agents never answer the same one. Binding a channel is the Owner's. |
| **Hand-off person** | Who a conversation is given to when the agent hands it over. Empty means it goes to the Inbox, unassigned. The same person is mailed when an item is left for review. |
| **Complaint tags** | The Communications tags that mean a complaint (at most 20). A conversation with one of them is held for review in Review and Auto above X %. |
| **Reply mode** | How the agent's replies are decided ([below](#reply-mode)). |
| **Caps** | The most tool calls and turns one run may use, and the most replies the role sends in a day. |

Changes to a role take effect at the agent's next step.

If the hand-off person is disabled or may no longer see conversations, the hand-off
goes to the Inbox, unassigned, and the role page shows a warning beside the person.
Choose someone else to make the warning go away.

### Reply mode

**Reply mode** says what happens to a reply the agent has written:

- **Review** (the default): a person approves every reply before it is sent.
- **Auto above X %**: the agent sends the reply when its confidence is above the
  threshold, otherwise a person reviews it. The threshold is from 50 to 100 percent.
- **Auto**: the agent sends its replies by itself.

Choosing **Auto** or **Auto above X %**, and raising the mode or lowering the threshold later,
is the Owner's alone. Going back to **Review** is anyone's with `agents:manage`.
Whatever the mode, changes the agent makes in Vantigo are always reviewed first, and the
rules in [What happens to a reply](#what-happens-to-a-reply) hold.

## Permissions

Open the **Permissions** tab. **Every permission of an agent is Off until the Owner
raises it**, whatever roles the agent's user holds. Each permission can be **Off**,
**Read** or **Change**, and the agent never gets more than the lowest of its
[Identity roles](/en/user/workspace-administration/#agents-among-the-users), its level
and what its role's template allows.

The tab begins with **Needs**: what the roles you added need to work, each with the
level it wants and a **Raise** button when the agent has less. Only the Owner can raise;
anyone with `agents:manage` can lower. Some permissions have no level at all and are
shown as locked: they can never be given to an agent
([Virtual employees](/en/admin/agents/#the-levels)).

Agents hold Identity roles like a person and get them under **Roles & access**, but
never **Owner** or **SystemAdmin**.

## The review queue

Open **Virtual employees ▸ Review queue**. It lists, across all agents, every reply and every change an
agent left for a person: the ones that lapse soonest first, then (choose **Decided**) the ones already
decided, withdrawn or expired. A number on the **Virtual employees** tile on the dashboard counts what
waits. The internal note in a conversation (*Hanna has a reply waiting for review*) links to the item.

Each item shows:

- **Why it waits.** The rules that held it, in words: a source missing, a first contact, a complaint, a
  promise the instructions do not allow, a customer's data on its way to someone who is not the customer's
  contact, a text that says its writer is a person, and so on. An item that no rule held waits because
  the role is in Review, or because its score is under the threshold.
- **The score**, and the agent's own when the sources it had actually read lowered it, and **what it
  rests on**: the sources, such as `getInvoicesById 2026-0931`.
- **How long it has.** An item lapses after 24 hours. A Messenger reply lapses 15 minutes before the
  Messenger window closes, and the item counts down to it.
- **The reply**, with the opening line locked above it, or **the change**: what the agent asks to do,
  in a sentence, and the values.

You can **Send** a reply (or **Approve** a change), **Edit and send** a reply, or **Discard** an item.

- **Send** sends the reply as the agent, with its opening line, and the message is stored and marked as the
  agent's. **Approve** makes the change as the agent. Both are done on your authority, so **you must be
  allowed to do what the item does yourself**: to send a reply you need `communications:conversations-reply`, to
  approve a change the permission of the operation it calls (for closing a conversation,
  `communications:conversations-manage`). Otherwise the queue answers that you may not.
- **Edit and send** opens the text for you to change. The opening line stays locked above it; if you type
  one, the system still adds its own, so a message never goes out without it. A text that says or implies
  that its writer is a person is held, also after you edited it, and the item stays in the queue with the
  rule named.
- **Discard** asks for a reason: *Wrong facts*, *Wrong tone*, *Should not answer this* or *Other*, with an
  optional text. Discarding a reply **hands the conversation to a colleague**: the agent leaves it, and
  the hand-off person (or the Inbox) takes it.

Before anything is sent the queue looks again. If a colleague took the conversation, a newer message has
arrived, the Messenger window has closed (or only the "human agent" extension is left), the agent's
levels have since been lowered, or its role or the agent is gone, **nothing is sent**: the item is
*withdrawn* and the conversation handed off, or, when a newer message arrived, a new run answers the
conversation as it now is. A change is checked the same way: one whose operation the agent may no longer
call is withdrawn and not made.

Two people who approve the same reply at once send it once; the second is told it was decided. A save
names the version it read: an item that changed under you reloads and says so. An item that waits more
than a day *expires*, and so does one whose Messenger window is about to close; an expired item hands the
conversation off, like a withdrawn one.

**What you may see.** The queue shows what the agent read, so seeing an item needs more than *Review
virtual employees' work*: also `communications:conversations-view` and the read permissions the Front desk needs
(customers, contacts, orders, invoices and products, and Knowledge). Without them the queue answers that
you may not. Reading a [run](#activity) needs the same besides `agents:activity`.

## Calibration

The role page shows, under **How it replies**, what the role's scores are worth. Every reply a person has
decided is a label: sent unchanged is good, edited and sent is good with edits, discarded is bad. Over
the role's last 200 labelled replies, move the slider to a threshold X from 50 to 100 and the panel says:

- how many replies, and what share, **would have gone out on their own** (score at least X and no rule
  holding them);
- of those, how many a person **edited beyond 20 % or discarded**;
- how many a rule held **whatever the score**.

Two lines and a marker at X draw the same thing. Under 50 labelled replies the panel says *Not enough
history yet* and how many there are. The figures come from the role's own history, so a role that has
been in Review for a while is the one whose scores can be trusted to choose *Auto above X %*.

## Usage

The agent's **Usage** tab shows the last 30 days: runs, tokens (read and written), and how many replies
were sent, held for a person and discarded, a day at a time, with totals above and today's tokens against
the daily budget when there is one. The day is the business day. The cards on the **Virtual employees** page
show today's figures. The counts are kept for 24 months and hold no content.

## Activity

The agent's **Activity** tab lists its runs, newest first, with their state (done, waiting for review,
handed off, failed …), what they ended in and the tokens they used, and the changes the Owner made to the
settings, with who made them: an agent, a role, a mode, a level, a model or the settings. A run opens on
its own page:

- **Timeline.** Every tool call, in order, with its status and how long it took. No request or response
  bodies are kept.
- **The verdict and the mode's decision.** The score (and the agent's own), the sources, the mode and
  threshold of the role, which rules held or stopped the reply, and whether it was sent or left for a
  person.
- **What the audit says.** Each call as *by Hanna (AI)*, and *approved by a reviewer* when a person
  approved it.
- **What the run left for review**, with the buttons of the queue, and the **transcript**: what the model
  was told and answered, including the customer's messages, folded away until you open it.

A run needs `agents:activity`, `communications:conversations-view` and the read permissions above.
Runs and transcripts are deleted after the retention the administrator set, and erased with the customer
they belong to ([Virtual employees](/en/admin/agents/#retention-and-erasure)).

## Where you see the AI badge

An agent's avatar carries a small **AI** badge wherever it is shown, so nobody mistakes
it for a person: in the assignee select of a conversation in
[Communications](/en/user/communications/), in the conversation list, and among the
users in [Workspace administration](/en/user/workspace-administration/#agents-among-the-users).
