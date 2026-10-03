---
title: Communications
description: Shared mailboxes, conversations with customers, sending email and suppressions.
sidebar:
  order: 80
sources:
  - apps/communications/frontend
---

The Communications app is a shared inbox: every email conversation your organisation
has with its customers, in one place, answered from shared mailboxes called
**channels**. Its sidebar has three areas: **Inbox**, where conversations are read and
answered; **Channels**, where the mailboxes are set up; and **Suppressions**, the list
of addresses Vantigo refuses to write to. Opening the app lands you in the Inbox, and
the Home dashboard shows a Communications card with open, new and closed conversations.

Vantigo sends email but does not yet receive it: no mail arrives through Vantigo, so a
conversation only ever holds what was sent from it and the notes written in it. The
consequences for replying are described under
[The outbound-only consequence](/en/reference/communications/#the-outbound-only-consequence)
in the reference, and noted below where you will meet them.

## Find a conversation

Open **Inbox**. The left pane lists the conversations, newest activity first, with a
count at the top. Each row shows the other party (their name, or their address when
no name is known), when something last happened, the subject, a preview of the latest
text, the conversation's tags and its status. An unread conversation is shown in bold.

The toolbar filters the list:

- **All / Open / Closed / Archived** picks a status; All is the default.
- **Unread** keeps only conversations with something you have not read.
- The tag selector (placeholder **All tags**) keeps conversations carrying one tag.

Changing a filter drops the selected conversation, since it may no longer be in the
list, and the filters are kept in the address bar so a filtered view can be bookmarked
or shared with a colleague. When nothing matches you see "No conversations match these
filters." The **Search conversations** box in the toolbar is not yet in use. The tick
icon next to the count reloads the list.

Opening the Inbox selects the first conversation in the list; click any row to read
another one. The Inbox needs `communications:conversations-view`.

## Read a conversation

A conversation groups everything exchanged with one customer about one subject: the
participants and the subject in the header, then the messages in order. Each message
is marked **You** for an email sent from Vantigo, **Internal note** for a note a
colleague wrote, and the sender's name for a message from the customer. A message
written as HTML is shown in a sandbox that blocks scripts, forms and network access.

An attachment appears as a link you can download; while it is waiting to be checked it
is shown as **Scanning**, and one that cannot be served as **Attachment unavailable**.

From the header you can:

- change the **Status** (`open`, `closed` or `archived`);
- open **Actions** → **Mark read**, which clears the unread marking, or **Archive**;
- add a tag from the **Add tag** selector, or remove one with the × on it. Only tags
  that already exist are offered; the Inbox does not create new ones.

Reading needs `communications:conversations-view`; changing the status and the tags
needs `communications:conversations-manage` as well.

## Link a conversation to a customer

The header tells you how the conversation relates to a customer:

- **Customer #id** — the conversation is linked, and the badge says how the link was
  made.
- **Suggested customer #id**, with a confidence percentage — the AI assistant has a
  suggestion, shown with its reasoning. **Confirm customer** makes it the link.
- **N customer candidates** — several customers could match. **Suggest customer**
  asks the AI assistant to pick one, which then appears as a suggestion to confirm.
- **No customer association** — none of the above.

Suggesting and confirming need `communications:conversations-manage`. When the
assistant is not configured you see "AI customer suggestions are currently
unavailable." — the administrator's side of that is under
[AI draft and customer suggestion](/en/reference/communications/#ai-draft-and-customer-suggestion).

## Reply to a customer

The composer at the bottom of the conversation is headed **Reply**. It tells you who
the reply goes to ("Replying to …"), and when the conversation has more recipients a
**Reply mode** selector offers **Reply** or **Reply all · N**. The reply is sent from
the channel the conversation belongs to; you do not choose a sender.

1. Write the message in the editor; bold, italic and bullet lists are available.
2. **Attach file** stages a file. It appears as a badge with its name: green when it
   is ready, **Scanning** while it is being checked, **Unavailable** when it was
   refused. A file that could not be staged leaves your draft unchanged. The size
   limit is set by the administrator (see
   [Attachments](/en/reference/communications/#attachments)).
3. **Send reply**. The button is disabled until there is text, and reads **Waiting for
   attachment scan** while an attachment is not ready yet.

Sending needs `communications:conversations-reply`. The reply is queued rather than
sent on the spot — see [After you send](#after-you-send) — and it is refused when:

- the conversation's channel has been deactivated under Channels;
- the conversation has no customer message to answer. Because Vantigo does not
  receive mail yet, this is the case for every conversation today, so a reply cannot
  currently be sent; the composer is complete and starts working once inbound mail
  exists;
- an attachment is not ready;
- a recipient is on the suppression list.

## Draft a reply with AI

Above the composer, **Draft with AI** writes a first draft for you. Pick a tone
(`concise`, `friendly` or `formal`), say what the reply should cover in the text box
— this is required — and click **Draft with AI**. The draft lands in the editor,
headed "AI draft — review before sending. Nothing has been sent." Read it, edit it, and
send it like any other reply; **Regenerate draft** asks for another one. When the
assistant is not configured the panel says so and you write the reply yourself.

## Add an internal note

**Add note** switches the composer to **Internal note**. A note is kept in the
conversation for your colleagues and is never sent to the customer. Write it and click
**Add note**; the button **Reply** switches back. Notes need
`communications:conversations-manage`.

## After you send

A sent reply is placed in an outbox and delivered by a background worker through the
channel's SMTP server, so it may take a moment to leave. A failed attempt is retried
with growing waits, up to eight attempts in total; after that the message is given up
and counted on a metric the administrator can alert on. Immediately before each
attempt the worker checks the suppression list again and cancels delivery to any
address on it. In rare cases a message can be delivered twice after a crash; the rules
are under [Delivery semantics](/en/reference/communications/#delivery-semantics).

The Inbox does not yet show a delivery status per message; what it shows is that the
message was sent from Vantigo.

## Set up a channel

A channel is a shared mailbox: the address your replies are sent from, with the SMTP
account that sends them. Open **Channels** for the table of channels with **Address**
(the display name, with the address beneath it), **Provider**, **Status** (**Active**
or **Inactive**) and **Actions**. The channel marked **Default** is the one a
conversation is created on when none is named; the first channel you create becomes
the default.

Before creating a channel, the administrator must have the application secret in place
and know the SMTP host, port and account; the password is stored encrypted and never
shown again, and the secret it is encrypted under is explained under
[SMTP](/en/reference/communications/#smtp). Note that the mail server settings an
administrator configures for Vantigo's own invitations and password resets are a
different thing and are not used here.

To create one, click **Add channel**. The **Add email channel** dialog asks for:

- **Email address** — the sender address, required.
- **Display name** — the name recipients see; shown in the table in place of the
  address when set.
- **Provider** — leave it at **SMTP**. **Mailgun** is listed but the server only
  accepts SMTP channels and refuses the request; the channel is then not created.
- **SMTP host** and **SMTP port** (587 is filled in), both required, and the
  account's **Username** and **Password**.

**Create channel** closes the dialog and confirms with "Channel created — Your email
channel is ready." An address that already has a channel is refused.

Per channel, **Verify** connects to the SMTP server with the stored credentials and
reports "Channel verified — The channel connection is valid."; when the connection
fails, no confirmation appears. **Deactivate** keeps the channel but stops sending: a reply in a
conversation on an inactive channel is refused until **Activate** switches it back on.

There is no per-user restriction on a channel. Anyone who can reply in the Inbox
replies from the channel the conversation belongs to, so who can use a channel is
decided by who holds `communications:conversations-reply`. Managing channels needs
`communications:channels-manage`.

## Block an address

A suppression is an address Vantigo will not send to — a person who has asked not to
be written to, or an address that is known to be dead. Open **Suppressions** for the
list, with each address's **Email**, **Reason** and **Created** date, and a **Search**
box that matches the email or the reason.

To add one, fill in **Email address** (an invalid address is refused with "Enter a
valid email address") and optionally a **Reason**, then click **Add suppression**. You
see "Suppression added — The address will not receive messages." Adding an address
that is already on the list keeps the existing entry, with its original reason.

To remove one, click **Delete** on its row and confirm with **Delete suppression** in
the dialog that asks "Allow this address to receive messages again?" The confirmation
reads "Suppression deleted — The address can receive messages again."

Suppressions are matched without regard to case. A reply to a suppressed address is
refused when it is sent, and the outbox checks the list once more before every delivery
attempt. Vantigo does not add suppressions on its own: since no mail is received, a
bounce never reaches it, so an address that bounces has to be added here by hand.
Suppressions need `communications:suppressions-manage`.

## Permissions

| To | You need |
| --- | --- |
| Open the Inbox, read conversations, mark them read, download attachments | `communications:conversations-view` |
| Reply, attach files, draft with AI | `communications:conversations-reply`, with view |
| Change status, tags and customer link, add notes, ask for a customer suggestion | `communications:conversations-manage`, with view |
| See and manage Channels | `communications:channels-manage` |
| See and manage Suppressions | `communications:suppressions-manage` |

An area whose permission you lack is not shown in the sidebar. Permissions are granted
through roles under Workspace administration — see
[Finding your way around](/en/user/).
