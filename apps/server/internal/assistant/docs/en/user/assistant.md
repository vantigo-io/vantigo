---
title: Assistant
description: Ask the assistant how to do something, about your data, or to change something — opening it from the header, with Cmd/Ctrl+J or from search, the docked panel and the context chip, the Assistant app with every conversation, the changes it proposes and you confirm, a change to many records at once (preview, the first five, the rest in the background, the failed items), what is private and kept, what is sent to the AI provider, and why it might not be there.
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
you press it; the assistant never moves the screen by itself. For the main create flows it
also adds a **Show me** button that starts a guided tour ([below](#show-me-how)).

**Read your data.** Ask "which invoices are overdue?" or "show the metering points of
Ola Nordmann" and it reads what you may read and answers with a table, each record a link
to its screen. It sees exactly what you would see: a record or a whole area you
have no permission for is not available to it either, and an Owner may narrow it further
([What the assistant may use](/en/user/workspace-administration/#what-the-assistant-may-use)).
If it is not allowed to read something it says so; it does not try to work round it.

**Be wrong.** The assistant is a language model, and it can misread a question or a table.
Check what matters against the screen: every record in an answer links to it.

## Show me how

Under the written steps of a how-to answer the assistant may add a **Show me** button. It
starts a guided tour: the screen dims, one control is lit at a time, and a short note in your
language says what to do with it. "How do I add a metering point?" lights **New metering
point**, then the fields that matter, then **Create metering point**.

- **It starts only when you press the button.** Never on its own, and the written steps
  always come first.
- **It opens the right screen.** If the first control is on another screen, the tour opens
  that screen and the panel stays beside it. A control on one record's screen (a customer, a
  metering point, an invoice) can only be pointed at while that record is open, so ask from the
  record.
- **Next, Back and Escape.** **Next** and **Back**, or the arrow keys, move between steps;
  **Close** or Escape ends the tour, and the focus returns to the **Show me** button. On a narrow
  screen, where the panel covers the page, the panel closes when the tour starts.
- **Forms.** A field in a form is on screen only once the form is open. The note says so:
  press the lit button yourself, then **Next**. The lit control can be clicked through the
  dimmed layer. The tour never clicks, types or saves anything for you.
- **A control that is not there.** If you lack the permission for it, or it has not appeared
  within three seconds, its step is shown in the middle of the screen with the same note.
- **Where it works.** The main create flows: customers and contacts, metering points and
  supply periods, invoices, time entries, expenses and travel claims, and projects. Elsewhere
  you get the written steps only.

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

## Bulk changes

Ask "give every metering point in price area NO5 the product *Spot Bergen*" or "set the payment
terms of all customers tagged *Wholesale* to 30 days" and the assistant proposes **the same change to
all of them at once**. It does not make it. It reads the list you described, as you, and a **Bulk
change** card appears in the conversation. A bulk change has four steps, and you decide each one.

1. **The preview.** The card says **how many items were found** and shows **the first 10**, each
   with the value it has now and the new one. It says how many will be **skipped**, and why: an item
   the list gave without the value the change needs, or one that appears twice. It also says that
   Vantigo checks each record when its change runs, so some may still be refused. Nothing has been
   changed. **Start** and **Cancel** are the only buttons.
2. **Start** changes **the first 5 items**, for real, as you. The card shows how each one went.
   If there were no more than 5, that is all.
3. **Continue** changes the rest **in the background, 100 at a time**. The card shows a progress
   bar and how many have succeeded, failed and been skipped, and a **Stop** button. You can close the
   panel and do something else; it goes on.
4. **The end.** The card says how many were changed, and lists **the items that failed**, each with the
   refusal Vantigo gave (the status and its title). **Download failed items (CSV)** saves the whole list
   as a file you can open in a spreadsheet. The screen you are on refreshes, and the assistant sums the
   outcome up in one short message.

- **It is a preview of the change, not a rehearsal.** Vantigo's screens write directly, and some changes
  also do something outside the database, such as sending a message, so a change cannot be tried and
  taken back. The card never calls itself a trial: what **Start** changes is changed, and stopping
  does not undo it. That is why only 5 items come first, and why you decide whether to go on.
- **Up to 2,000 items.** If more match, the assistant tells you how many and asks you to narrow the
  filter, for instance by a price area or a tag.
- **The items are fixed when you see them.** The job changes exactly the items you saw counted. A
  record that is added or starts to match afterwards is not touched.
- **Stopping.** **Stop** takes effect **between chunks of 100**, so up to 100 items more may be changed
  after you press it. The card then says *Stopped after … of …*. Nothing already changed is undone.
- **Failed items.** A record can be refused on its own rules (you may not change that customer, it was
  edited by someone else meanwhile). That item fails and the others go on. The card lists the first 50
  and the CSV has all of them.
- **Why a long job asks you to Continue.** The assistant acts with your permission, which belongs
  to your session and lasts an hour at most. A job that would outlive it **pauses**, and the card says *Confirm to
  go on*: **Continue** gives it a fresh hour. **If you sign out, the job stops** at the next item,
  because the assistant no longer has your permission.
- **What the Owner decides applies to every item.** If an Owner narrows what the assistant may do
  while a job runs, the job stops at the end of the chunk it is in
  ([What the assistant may use](/en/user/workspace-administration/#what-the-assistant-may-use)).
- **Waiting.** A plan nobody starts, and a job that waits for **Continue**, expire after 24 hours. While
  a bulk change waits or runs, the message box is off, as for a single change; **Stop** on the card, or
  the stop button beside the message box, stops the job.

## What it cannot do

**It does not fill in the form on your screen.** It creates or changes the record through
Vantigo's own interface behind the screens, with your confirmation, and then offers to
open it.

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
