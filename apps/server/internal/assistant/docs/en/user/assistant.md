---
title: Assistant
description: Ask the assistant how to do something, about your data, or to change something — opening it from the header, with Cmd/Ctrl+J or from search, the docked panel and the context chip, the Assistant app with every conversation, the changes it proposes and you confirm, what is private and kept, what is sent to the AI provider, and why it might not be there.
sidebar:
  order: 3
sources:
  - apps/assistant/frontend
  - apps/host/frontend/src/routes/assistant
  - apps/host/frontend/src/routes/assistant.tsx
---

The **Assistant** answers "how do I…" from Vantigo's documentation and shows you the
way, and it reads the data you may read and answers with a table you can click
through. It never does anything you could not do yourself in the interface, and it never
changes anything on its own: ask it to create or change something and it shows you the
change on a card first, and **nothing is written until you press Confirm**. Where the rules
behind it are exact — who has it, what it may
use, what it keeps — they are in the [Assistant reference](/en/reference/assistant/)
and, for an Owner, in [Workspace administration](/en/user/workspace-administration/#assistant).

## Open it

There are three ways, and the first two work on every screen:

- **The Assistant button** in the header (its label is *Open the assistant*). A badge on
  the button tells you the assistant is working on an answer, is **waiting for you to
  confirm a change**, or has answered while the panel was closed. When it proposes a
  change while the panel is closed, a notification tells you too.
- **Cmd+J** on a Mac, **Ctrl+J** elsewhere. The key is ignored while you are typing in an
  editor that uses it itself.
- **Search.** Type a question in the search box in the top bar. When nothing matches
  well, the list ends with **Ask the assistant: "…"**; choose it and the panel opens with
  your question already sent.

The button and the app are shown only to people who have the assistant
([why it might not be there](#when-it-is-not-there)).

## The panel

The panel opens on the right and **pushes the page** aside rather than covering it, so you can read a
screen while you ask about it. Drag its edge to make it wider or narrower; the width is
remembered in this browser. On a narrow screen it fills the screen instead. It starts
closed every time you open Vantigo, and it **stays open as you move between apps**.

- **It opens on your last conversation**, on any device: the one you had open last.
  **New conversation** is the only way to start a fresh one.
- **The header** holds the conversation's title, **Recent conversations**, **New
  conversation** and **Open in Assistant**, which opens the same conversation in the
  full app.
- **The context chip** above the message box says what you are looking at — *Viewing:
  Customer Ola Nordmann* — so "this customer" and "here" mean something. The chip
  follows you as you navigate. The assistant is told the app, the screen and the
  record's name, never what is on the screen. Press the cross on the chip,
  **Do not send what I am viewing**, and the next message goes without it.
- **Stop** ends an answer that is on its way. You can also close the panel or leave the
  page: the assistant finishes its answer on the server, and it is in the conversation
  when you come back.

## The Assistant app

Choose **Assistant** in the app switcher, or **Open in Assistant** in the panel, for
the app at `/assistant`. It is laid out like a chat: a sidebar of your conversations and
the open conversation beside it.

- **Every conversation you have had, from anywhere in Vantigo.** They are grouped
  **Pinned**, **Today**, **Yesterday**, **Last 7 days** and **Older**, and each says
  where it began — *Started in Energy*.
- **Search conversations** finds a conversation by its title or by words in it.
- **Rename**, **Pin** / **Unpin**, **Archive** and **Delete** are in the actions menu of
  each conversation. Archived conversations leave the list; the **Archived** switch shows
  them, and **Restore from archive** brings one back. **Delete** asks first and removes the
  messages for good.
- **Resume any conversation** by opening it and writing: it carries on from what was said.
  The assistant answers one message at a time; while it is still answering you cannot
  send another.

A conversation is named after your first question until you rename it.

## What it can do

**Answer how-to questions.** Ask "how do I add a metering point?" and the assistant
searches the documentation, in your language, and answers with numbered steps named as
the interface names them, a link to the documentation page it used and, when a screen is
the answer, a **Take me there** button that opens it. The button opens the screen when
you press it; the assistant never moves the screen by itself.

**Read your data.** Ask "which invoices are overdue?" or "show the metering points of
Ola Nordmann" and it reads what you may read and answers with a table, each record a link
to its screen. It sees exactly what you would see: a record or a whole area you
have no permission for is not available to it either, and an Owner may narrow it further
([What the assistant may use](/en/user/workspace-administration/#what-the-assistant-may-use)).
If it is not allowed to read something it says so; it does not try to work round it.

**Be wrong.** The assistant is a language model, and it can misread a question or a table.
Check what matters against the screen: every record in an answer links to it.

## Changes, with your confirmation

Ask "set Ola Nordmann's payment terms to 30 days" or "create a project called Rollout for
Acme" and the assistant finds the right operation and **proposes** the change. It does
not make it. A **confirmation card** appears in the conversation:

- **What will happen**, in a sentence, and the record it is about as a link.
- **The fields** it will set, each with the value now and the new one — *14 → 30*. The
  current value is shown when a read of the same record could say what is there; for a new
  record, or one you may not read, only the new values are shown.
- **Confirm** and **Cancel**. Nothing has been written yet. The focus lands on the card,
  not on **Confirm**, so pressing Enter does not confirm by accident.

Press **Confirm** and the change is made **as you**, by the same rules as if you had done
it yourself on its own screen: if you are not allowed, it is refused. The assistant then
reads the result and tells you what happened, and **the screen you are on refreshes** so it
shows the change. Press **Cancel** and nothing is changed; the assistant answers in one
line.

- **Confirming can wait.** The card stays in the conversation. Reload the page, come back
  from another device, or confirm half an hour later: it is still there and still works.
  The assistant's button shows that a change is waiting, and the message box is off until
  you decide it. **After 24 hours** the card expires: it is marked *Expired* and nothing
  is changed.
- **One change at a time.** If you ask for several, the assistant proposes the first and
  offers the next once you have decided it.
- **If the record changed in the meantime**, because somebody else edited it after the
  card was made, the change is not made over their work. The card says *Failed*, the
  assistant tells you the record changed, and you can ask again.
- **Once decided, the card shows how it ended** — *Changed*, *Cancelled*, *Expired* or
  *Failed*, with the reason — and the names of the fields. The values you confirmed are
  not kept in the conversation; the module's own history shows the change as made by you.
- **An Owner can switch changes off** for the whole workspace, or for parts of it, under
  **Workspace admin** › **Assistant**; a card that is waiting when that happens can no
  longer be confirmed.

Some operations are never available to the assistant, such as an Owner's settings, signing
in and passwords, and file uploads, whatever the Owner sets.

## What it cannot do

**It does not fill in the form on your screen.** It creates or changes the record through
Vantigo's own interface behind the screens, with your confirmation, and then offers to
open it. It does not yet make a change to many records at once.

## Links in answers

A link in an answer opens only a screen in Vantigo or this installation's documentation.
Any other address — a web page, `mailto:`, `tel:` — is not a link: it is shown as text with
the address visible, so you can see where it would lead before you decide to copy it.

## Privacy

- **Your conversations are yours alone.** Nobody else can open, search or export them —
  not another user, not a colleague with the same role, not an Owner or an administrator.
  There is no screen or API for it.
- **How long they are kept.** A conversation is deleted, whole, when its last activity
  is older than the workspace's retention: **12 months** unless an Owner has set another
  time (1 to 60 months). You can delete one yourself at any time. If your account is
  erased, your conversations go with it within a day.
- **What is sent to the AI provider.** To answer, the assistant sends the provider your
  questions and the conversation so far, the documentation excerpts it finds, the data it
  reads for you, the screen your chip names, and your name and language. Nothing is sent
  until you write a message. When the model runs on your organisation's own network, none
  of it leaves it; otherwise an Owner has been told, under **Workspace admin** ›
  **Assistant**, where it goes (*Questions and the data the assistant reads are sent to
  …*). Ask your administrator which applies to you.
- **What it does for you is limited to what you may do yourself, and is logged.** Every
  time the assistant uses a tool, reads from an operation or makes a change you confirmed,
  the conversation records which one, in what mode (for a change, that you confirmed it)
  and what status came back. That record holds no data. What the assistant read, shortened, is kept in the conversation itself so it
  can carry on from it, and goes when the conversation does.
- **What the Owner sees.** An Owner can see, per person and day, how many conversations,
  runs, model tokens and confirmed changes there were — a count for cost and for abuse.
  Never what was said, and never what was changed.
- **A customer's data and the assistant.** If you asked it about a customer, that
  answer is in your conversation until the retention deletes it. Erasing a customer
  under GDPR does not search conversations; the retention is the answer.

## When it is not there

The Assistant button and app are shown only to a person who has the assistant. If you do
not see them, one of these is the reason:

- **The installation has no AI provider.** Nobody has the assistant until the
  administrator who runs the installation has set one up
  ([Configuration](/en/admin/configuration/#ai)).
- **An Owner turned it off.** Under **Workspace admin** › **Assistant** the setting can
  be *Disabled for all*.
- **It is per person, and your switch is off.** With *Per person*, only people whose own
  switch is on have it. Ask an Owner to turn it on under **Workspace admin** ›
  **Users**.
- **Your own access to Vantigo has ended.** A disabled or locked account has nothing.

If the assistant is there but an answer fails with *The AI service did not answer*, the
model behind it is busy or down; try again in a moment. If it says *You have used the
assistant's token budget for today*, an Owner has set a daily limit per person and you have
reached it; it is available again the next business day. A long answer from a model on
modest hardware can take minutes.
