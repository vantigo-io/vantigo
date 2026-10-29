---
title: Communications
description: Shared mailboxes, conversations with customers, receiving and sending email and suppressions — channels on SMTP or on a Microsoft 365 shared mailbox, mail received into a Microsoft 365 channel, reply and reply all, switching a channel to Microsoft 365, and the size a Microsoft 365 channel sends.
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

Mail that arrives in a **Microsoft 365** channel's shared mailbox comes into the Inbox
by itself, as a conversation you can read and answer. An **SMTP** channel only sends: no
mail arrives through it, so its conversations hold only what was sent from Vantigo and the
notes written in them, and they cannot be replied to. How receiving works is described
under [Receiving](/en/reference/communications/#receiving) in the reference.

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
colleague wrote, and the sender's name for a message from the customer. The time on each
message, and on each conversation in the list, is in your own time zone
([Choose your time zone](/en/user/getting-started/#choose-your-time-zone)).

**Mail from customers.** An email sent to a Microsoft 365 channel's shared mailbox appears
in the Inbox within about five minutes. A reply to an earlier message joins that
message's conversation; anything else starts a new conversation. A closed conversation
the customer writes to again is opened again; an archived one is left as it is, and the
email starts a new conversation. The message also stays in the shared
mailbox in Outlook, as it was.

- A message written as HTML is shown formatted, in a sandbox that blocks scripts, forms
  and network access. Pictures from the internet are removed; pictures the message itself
  carries are shown. **Show as text** shows the message's plain text instead, and **Show
  formatted** goes back.
- An automatic answer — an out-of-office reply, say — is marked **Auto-reply**, and does
  not open a closed conversation again.
- A message Vantigo could not read says *Vantigo could not read this message.*; open it in
  your mail program with **Download original**. **Message actions** on any message from a
  customer also offers **Download original**, the email exactly as it arrived, as an
  `.eml` file.

An attachment appears as a link you can download. One that came with a customer's email
is marked *Not scanned. Open it only if you trust the sender.* — Vantigo does not check
files for viruses — and is always downloaded as a file, never opened in the browser. An
attachment waiting to be checked is shown as **Scanning**, and one that cannot be served
as **Attachment unavailable**.

From the header you can:

- change the **Status** (`open`, `closed` or `archived`);
- open **Actions** → **Mark read**, which clears the unread marking, or **Archive**;
- add a tag from the **Add tag** selector, or remove one with the × on it. Only tags
  that already exist are offered; the Inbox does not create new ones.

Reading needs `communications:conversations-view`; changing the status and the tags
needs `communications:conversations-manage` as well.

## Link a conversation to a customer

When a customer's email arrives, Vantigo looks up the sender's address among the
customers' contacts. If it belongs to **one customer**, the conversation is linked to that
customer by itself; if it belongs to **several**, they are listed as candidates for you to
choose between. A conversation that is already linked — by hand or by an earlier email —
keeps its customer.

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
the reply goes to ("Replying to …"): the sender of the customer's latest email. When that
email was also sent to others in copy (Cc), a **Reply mode** selector offers **Reply**,
to the sender alone, or **Reply all · N**, which copies them too — the shared mailbox
itself left out. The reply is sent from the channel the conversation belongs to; you do
not choose a sender. It is sent as an answer to the customer's email, so it lands in the
same thread in their mail program.

1. Write the message in the editor; bold, italic and bullet lists are available.
2. **Attach file** stages a file. It appears as a badge with its name: green when it
   is ready, **Scanning** while it is being checked, **Unavailable** when it was
   refused. A file that could not be staged leaves your draft unchanged. The size
   limit is set by the administrator (see
   [Attachments](/en/reference/communications/#attachments)). On a **Microsoft 365**
   channel the composer also says *Attachments up to about 2.1 MB in total on this
   channel*: Microsoft 365 takes a message only up to a fixed size, attachments and text
   together. A larger file is refused at once with *The attachment is larger than this
   channel can send (about 2.1 MB).*
3. **Send reply**. The button is disabled until there is text, and reads **Waiting for
   attachment scan** while an attachment is not ready yet. In a conversation with no email
   from the customer it stays disabled, with *There is no message from the customer to
   reply to yet.*

Sending needs `communications:conversations-reply`. The reply is queued rather than
sent on the spot — see [After you send](#after-you-send) — and it is refused when:

- the conversation's channel has been deactivated under Channels;
- the conversation has no customer message to answer — a conversation started from
  Vantigo that the customer has not answered yet, or one on an SMTP channel;
- an attachment is not ready;
- a recipient is on the suppression list;
- on a Microsoft 365 channel, the message and its attachments together are larger than
  the channel sends: *The message and its attachments are larger than this channel can
  send.* Remove an attachment, or send it another way.

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

A sent reply is placed in an outbox and delivered by a background worker — through the
channel's SMTP server, or, on a Microsoft 365 channel, from the shared mailbox through
Microsoft 365 — so it may take a moment to leave. A Microsoft 365 channel's sent mail is
also in the shared mailbox's **Sent Items**, where your team sees it in Outlook. A failed attempt is retried
with growing waits, up to eight attempts in total; after that the message is given up
and counted on a metric the administrator can alert on. On a Microsoft 365 channel, a
message the mailbox can never send — the mailbox outside what Vantigo may reach, or gone,
the message too large, or a blind copy (Bcc) that would be lost — is given up after the
first attempt. While Microsoft 365 is unavailable to Vantigo (**Mail channels** turned
off, or the Entra connection failing), a message waits without using up its attempts,
and is given up only 24 hours after it was sent. Immediately before each
attempt the worker checks the suppression list again and cancels delivery to any
address on it. In rare cases a message can be delivered twice after a crash; the rules
are under [Delivery semantics](/en/reference/communications/#delivery-semantics).

The Inbox does not yet show a delivery status per message; what it shows is that the
message was sent from Vantigo.

## Set up a channel

A channel is a shared mailbox: the address your replies are sent from. Open **Channels**
for the table of channels with **Address** (the display name, with the address beneath
it), **Provider**, **Status** (**Active** or **Inactive**) and **Actions**. The channel
marked **Default** is the one a conversation is created on when none is named; the first
channel you create becomes the default.

The **Receive status** column says whether a channel receives mail: **Receiving**, with
*Last synced …*; **Waiting for first sync** just after the channel was added or switched;
**Failed: …** with the reason when Vantigo cannot read the mailbox — *The application can
no longer read the mailbox. Give it access in Exchange Online.*, *The mailbox no longer
exists in Microsoft 365.* or *The mailbox is not known for this channel. Verify the
channel.* — or **Last sync failed: …** when one attempt failed and Vantigo tries again
(*Microsoft 365 mail channels are not available.* while **Mail channels** is off or the
Entra connection is failing; the mail waits in the mailbox).
An SMTP channel shows **Sends only**.

A channel sends one of two ways, its **Provider**:

- **SMTP** — through a mail server, with an SMTP account. The administrator must have the
  application secret in place and know the SMTP host, port and account; the password is
  stored encrypted and never shown again, and the secret it is encrypted under is
  explained under [SMTP](/en/reference/communications/#smtp). The mail server settings an
  administrator configures for Vantigo's own invitations and password resets are a
  different thing and are not used here.
- **Microsoft 365** — from an Exchange Online **shared mailbox**, through Microsoft 365,
  with no password to enter. It also **receives**: mail arriving in the shared mailbox's
  Inbox from the day the channel is added comes into the Inbox — not the mail that was
  there before. It is offered only once someone has turned on **Mail
  channels** in the Microsoft Entra ID app and an Exchange administrator has given
  Vantigo access to the mailbox ([the administration
  guide](/en/admin/entra/#mail-channels)). When the application's access reaches every
  mailbox in the company, an orange notice above the table, *Mail access reaches every
  mailbox*, says *Mail access is granted to every mailbox in the tenant. Limit it with
  RBAC for Applications and remove the tenant-wide consent. While this shows, anyone who
  manages channels in Communications can send as any shared mailbox in the tenant.* The
  channels still work.

### Add an SMTP channel

Click **Add channel**. The **Add email channel** dialog asks for:

- **Email address** — the sender address, required.
- **Display name** — the name recipients see; shown in the table in place of the
  address when set.
- **Provider** — **SMTP**.
- **SMTP host** and **SMTP port** (587 is filled in), both required, and the
  account's **Username** and **Password**.

**Create channel** closes the dialog and confirms with "Channel created — Your email
channel is ready." An address that already has a channel is refused.

### Add a Microsoft 365 channel

Click **Add channel**, and choose **Microsoft 365** as the **Provider**. The dialog then
asks only for the **Email address** of the shared mailbox and a **Display name**, and
says *The mailbox must be a shared mailbox in the application's scope.* **Create
channel** checks the mailbox with Microsoft 365 first; the channel is created active, with
the mailbox's own primary address, and can send at once. The check can refuse it:

| The dialog says | Means | Who fixes it |
| --- | --- | --- |
| *This is a personal mailbox. Only a shared mailbox can be a channel.* | The address is a person's mailbox. | Use a shared mailbox; an Exchange administrator can convert one. |
| *The mailbox is outside the application's scope. Give the application access to it in Exchange Online.* | Vantigo has not been given access to this mailbox, or the access has not taken effect yet — it can take up to 2 hours. | An Exchange administrator, by [How to give the application access](/en/admin/entra/#mail-channels). |
| *Microsoft 365 has no mailbox with this address.* | No mailbox has this address — or it is an alias. | Use the mailbox's primary address. |
| *Microsoft 365 mail channels are not available. An Entra administrator turns on Mail channels in the Entra settings.* | **Mail channels** is off, or the Entra connection is not working. | Whoever manages the Microsoft Entra ID app. |
| *Another channel already uses this mailbox.* | Another channel uses the same mailbox, perhaps by another of its addresses. | Use that channel. |
| *A channel with this address already exists.* | Another channel has this address. | Use that channel. |
| *Microsoft 365 did not answer. Try again in a moment.* | Microsoft 365 was busy or unreachable. | Try again. |

Adding, switching and verifying channels are limited to 30 a minute; past that,
*Too many attempts. Wait a minute and try again.*

### Switch an SMTP channel to Microsoft 365

When the mailbox an SMTP channel sends from is a shared mailbox in Exchange Online, the
channel can send through Microsoft 365 instead, keeping its conversations, its address
and its **Default** mark. On its row, choose **Switch to Microsoft 365**; the dialog
warns *The SMTP settings are deleted. To go back you enter them again.* — choose
**Switch**. Vantigo checks the mailbox as when adding one, with the same messages, and
the row's provider reads **Microsoft 365**: "Channel switched — The channel now sends and
receives through Microsoft 365." From then on the mailbox's new mail comes into the Inbox;
mail that was already there does not. The action is shown only while Microsoft 365 is
available.

The channel keeps the address it had. If that address is an alias of the shared mailbox
rather than its primary address, check that the first message goes out as expected:
how Microsoft 365 sends from an alias has not been verified yet. Adding the channel anew
by the mailbox's primary address avoids the question.

**There is no button to go back.** The channels page cannot switch a channel back to
SMTP; an administrator can, through the API, by sending the channel's SMTP settings
again with the password ([Switching provider](/en/reference/communications/#switching-provider)).

### Verify and deactivate

Per channel, **Verify** checks that the channel can send: for SMTP it connects to the
server with the stored credentials, for Microsoft 365 it checks the mailbox again, and
it reports "Channel verified — The channel connection is valid." When it fails,
*Channel not verified* says why — for Microsoft 365 with the messages above, or *The
address now belongs to another mailbox in Microsoft 365.* **Deactivate** keeps the
channel but stops sending: a reply in a conversation on an inactive channel is refused
until **Activate** switches it back on.

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
attempt. Vantigo does not add suppressions on its own: a bounce that reaches a Microsoft
365 channel arrives in the Inbox like any other email, and the address has to be added
here by hand.
Suppressions need `communications:suppressions-manage`.

## Permissions

| To | You need |
| --- | --- |
| Open the Inbox, read conversations, mark them read, download attachments and originals | `communications:conversations-view` |
| Reply, attach files, draft with AI | `communications:conversations-reply`, with view |
| Change status, tags and customer link, add notes, ask for a customer suggestion | `communications:conversations-manage`, with view |
| See and manage Channels | `communications:channels-manage` |
| See and manage Suppressions | `communications:suppressions-manage` |

An area whose permission you lack is not shown in the sidebar. Permissions are granted
through roles under Workspace administration — see
[Finding your way around](/en/user/).
