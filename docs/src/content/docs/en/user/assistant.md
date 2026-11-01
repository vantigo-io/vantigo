---
title: Assistant
description: Ask the assistant how to do something or about your data — opening it from the header, with Cmd/Ctrl+J or from search, the docked panel and the context chip, the Assistant app with every conversation, what it can and cannot do yet, what is private and kept, what is sent to the AI provider, and why it might not be there.
sidebar:
  order: 3
sources:
  - apps/assistant/frontend
  - apps/host/frontend/src/routes/assistant
  - apps/host/frontend/src/routes/assistant.tsx
---

The **Assistant** answers "how do I…" from Vantigo's documentation and shows you the
way, and it reads the data you may read and answers with a table you can click
through. It never does anything you could not do yourself in the interface, and in this
version it makes no changes at all: ask it to create or change something and it says so
and gives you the steps. Where the rules behind it are exact — who has it, what it may
use, what it keeps — they are in the [Assistant reference](/en/reference/assistant/)
and, for an Owner, in [Workspace administration](/en/user/workspace-administration/#assistant).

## Open it

There are three ways, and the first two work on every screen:

- **The Assistant button** in the header (its label is *Open the assistant*). A badge on
  the button tells you the assistant is working on an answer, or has answered while the
  panel was closed.
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

## What it cannot do yet

**It makes no changes.** Ask it to create an invoice, approve hours or change a customer
and it says plainly that it cannot change anything yet, and gives you the steps to do it
yourself. Changes — each one asked of you first — come in a later version. It does not
fill in the form on your screen.

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
  time the assistant uses a tool or reads from an operation on your behalf, the
  conversation records which one, in what mode and what status came back. That record
  holds no data. What the assistant read, shortened, is kept in the conversation itself so it
  can carry on from it, and goes when the conversation does.
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
model behind it is busy or down; try again in a moment. A long answer from a model on
modest hardware can take minutes.
