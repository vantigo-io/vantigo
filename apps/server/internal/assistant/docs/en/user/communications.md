---
title: Communications
description: Shared mailboxes, conversations with customers, receiving and sending email and suppressions — channels on SMTP or on a Microsoft 365 shared mailbox, mail received into a Microsoft 365 channel, reply and reply all, sending from your own Outlook or Gmail address, switching a channel to Microsoft 365, and the size a Microsoft 365 channel sends — and chat conversations on a Facebook Messenger channel, with answer options and the window in which a reply can be sent.
sidebar:
  order: 80
sources:
  - apps/communications/frontend
  - packages/frontend-shell/src/side-panel.tsx
  - apps/host/frontend/src/routes/communications
  - apps/host/frontend/src/lib/product-messaging.ts
  - apps/host/frontend/src/routes/__root.tsx
---

The Communications app is a shared inbox: every email conversation — and, where a
Facebook Messenger channel is set up, every chat — your organisation has with its
customers, in one place, answered from shared mailboxes called **channels**. Its sidebar has three areas: **Inbox**, where conversations are read and
answered; **Channels**, where the mailboxes are set up; and **Suppressions**, the list
of addresses Vantigo refuses to write to. Opening the app lands you in the Inbox, and
the Home dashboard shows a Communications card with open, new and closed conversations.

Mail that arrives in a **Microsoft 365** channel's shared mailbox comes into the Inbox
by itself, as a conversation you can read and answer. An **SMTP** channel only sends: no
mail arrives through it, so its conversations hold only what was sent from Vantigo and the
notes written in them, and they cannot be replied to. How receiving works is described
under [Receiving](/en/reference/communications/#receiving) in the reference.

A **Facebook Messenger** channel is a Facebook Page your business owns, and its
conversations are chats: they look and are answered differently from email, as described
under [Messenger conversations](#messenger-conversations).

## Find a conversation

Open **Inbox**. The left pane lists the conversations, newest activity first, with a
count at the top. Each row shows the other party (their name, or their address when
no name is known) beside a small icon for the channel — an envelope for email, the
Messenger logo for a chat —, when something last happened, the subject (a chat has none,
and shows **Facebook Messenger** in its place), a preview of the latest
text, the conversation's tags and its status, and a small avatar with the initials of the person
it is assigned to, when there is one (hover it for the name). An unread conversation is shown in bold.

The toolbar filters the list:

- **All / Open / Closed / Archived** picks a status; All is the default.
- **Unread** keeps only conversations with something you have not read.
- The assignee selector (placeholder **Anyone**) keeps the conversations **Assigned to me**, the
  ones that are **Unassigned**, or those assigned to one person you find by typing their name.
- The tag selector (placeholder **All tags**) keeps conversations carrying one tag.

Changing a filter drops the selected conversation, since it may no longer be in the
list, and the filters are kept in the address bar so a filtered view can be bookmarked
or shared with a colleague. When nothing matches you see "No conversations match these
filters." The **Search conversations** box in the toolbar is not yet in use. The tick
icon next to the count reloads the list.

Opening the Inbox selects the first conversation in the list; click any row to read
another one. The Inbox needs `communications:conversations-view`.

### The Inbox updates by itself

You do not need to reload. When mail or a chat message arrives, or a colleague answers,
closes, tags or assigns a conversation, the list and the conversation you have open change
within a moment. If your administrator has not made live updates available, or the
connection is lost for a while, the list refreshes by itself every minute instead, and the
tick icon next to the count still reloads it at once.

## The Conversations panel

You do not have to be in the Inbox to answer a customer. The **Conversations** button in the
header (its label is *Open conversations*) opens the Conversations panel in the side panel on
the right, on any screen, beside the Assistant: one at a time under tabs, or both stacked, as
described under [The panel](/en/user/assistant/#the-panel) in the Assistant guide. **Cmd+Shift+U** on
a Mac, **Ctrl+Shift+U** elsewhere, opens and closes it too; the key is left alone while you are
typing in a code editor. (Microsoft Edge on Windows uses Ctrl+Shift+U for *Read aloud* and may keep the
key for itself; use the button there.) The button and the panel need `communications:conversations-view`
and are not shown on the Inbox itself, which is the same view.

**The badge** on the button counts the open conversations assigned to you that you have not read.
When none of those is unread but other open conversations are, it shows a dot. A screen reader hears both numbers when
there are two: the ones assigned to you, then all unread. The counts change within a moment of mail arriving or a colleague changing a conversation, and
when you come back to the browser tab. Reading a conversation in another browser tab is only seen then.

**The list** is compact. Its filters are **Assigned to me**, **Unread** and **All open**; it starts on
Assigned to me, or on All open when nothing is assigned to you. On a customer's page it also offers
**This customer**, which shows every conversation linked to that customer, open or not; it is offered,
never chosen for you. The search box matches the subject, the preview and the names and addresses of the
conversations that are already loaded in the list (the newest 30 of the filter) — it does not search the
rest of the mail.

**A conversation** opens in the panel with the same thread and composer as the Inbox, Messenger chats
included, with their answer options and reply window. At the top, **Back** returns to the list, **Open in
Inbox** opens it in the Inbox on the main page, and the customer's link (*Customer #42*) opens the
customer on the main page while the panel stays as it is. Opening a conversation in the panel marks it read, which is
how the badge clears; the Inbox page does not mark a conversation read just because you opened it (use **Mark read**). An
e-mail wider than the panel is shown scaled down to fit. A reply you have half written is still there when you go to another app and
come back, or close the panel and open it again. What you may do follows your permissions, as in the
Inbox: without `communications:conversations-reply` the composer is read-only; without
`communications:conversations-manage` the status and assignee controls are disabled and the tag, archive and customer controls are not shown.

**It stays current.** Like the Inbox, the panel is live (see [The Inbox updates by itself](#the-inbox-updates-by-itself)):
while it is in front, the list and the open conversation change within a moment of a new message or a
colleague's change, and the conversation shows who else has it open and who is replying. Nothing is
listened to while the panel is closed or on another tab, and it reloads once when it is shown again. Where live
updates are not available, the list and the conversation refresh every minute instead. If a reload fails, the thread you have open stays as it was, with a note that it
could not be refreshed, so a reply you are writing is not lost.

## Read a conversation

A conversation groups everything exchanged with one customer about one subject: the
participants and the subject in the header, then the messages in order. Each message
is marked **You** for an email sent from Vantigo, **Internal note** for a note a
colleague wrote, and the sender's name for a message from the customer. The time on each
message, and on each conversation in the list, is in your own time zone
([Choose your time zone](/en/user/getting-started/#choose-your-time-zone)).

**Mail from customers.** An email sent to a Microsoft 365 channel's shared mailbox appears
in the Inbox within about a minute when change notifications are on, or within about five
minutes otherwise. A reply to an earlier message joins that
message's conversation; anything else starts a new conversation. A closed conversation
the customer writes to again is opened again; an archived one is left as it is, and the
email starts a new conversation. While a conversation is open, the message stays in the
shared mailbox in Outlook as it was; when you close the conversation, Vantigo leaves it
as-is, marks it as read, or moves it to a folder in Outlook according to the channel's
**When a conversation is closed** setting.

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
- choose the **Assignee**, the colleague who has the conversation, beside the status: type a name
  to search the active users, pick one, and the conversation is assigned to them with their avatar
  shown. Anyone who may manage conversations may assign one to any active colleague, themselves
  included. Clear the selector (**Unassign**) to leave the conversation with no one. A colleague who
  has since been disabled is not offered, and cannot be assigned;
- open **Actions** → **Mark read**, which clears the unread marking, or **Archive**;
- add a tag from the **Add tag** selector, or remove one with the × on it. Only tags
  that already exist are offered; the Inbox does not create new ones.

Reading needs `communications:conversations-view`; changing the status, the assignee and the
tags needs `communications:conversations-manage` as well.

### See who else is in a conversation

Under the conversation's header you see the colleagues who have the same conversation open,
as small round avatars. Beside them:

- **Also here: Kari** lists those who are only looking.
- **Kari is replying …** appears, highlighted, when a colleague has clicked into the reply
  box and typed something. With several, it reads **Kari, Ola are replying …**.

Use it to avoid answering the same customer twice: when you see someone replying, agree with
them before you send. It never stops you from sending, and it says nothing about people who
have the conversation closed. You do not appear to yourself, and a colleague with the
conversation open in two tabs appears once.

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

## Start a conversation

Choose **New conversation** in the toolbar to begin a new customer conversation: enter the
recipient's email address in **To**, the **Subject**, and write your message.

If you have connected your own mailbox under **Settings → Connections** — Outlook with
the Mail permission granted, or Google with **Gmail** among its features — you can check
**Send from my Outlook address (`you@yourcompany.com`)** or **Send from my Gmail address
(`you@yourcompany.com`)**: the checkbox names the service your own address is sent through.
The email is sent through your own mailbox, so it appears in your own **Sent Items** in
Microsoft 365 or **Sent** in Gmail. Customer replies are still directed to the channel
mailbox (`Reply-To`), keeping the conversation threaded in the shared inbox for your team.
This works on any channel, whichever service the channel itself uses.

## Reply to a customer

The composer at the bottom of the conversation is headed **Reply**. It tells you who
the reply goes to ("Replying to …"): the sender of the customer's latest email. When that
email was also sent to others in copy (Cc), a **Reply mode** selector offers **Reply**,
to the sender alone, or **Reply all · N**, which copies them too — the shared mailbox
itself left out. By default, the reply is sent from the channel the conversation belongs to.
If you have connected your own Outlook with the Mail permission, or your own Google
account with Gmail, you can also check **Send from my Outlook address
(`you@yourcompany.com`)** or **Send from my Gmail address (`you@yourcompany.com`)** to send
through your own mailbox instead, with customer replies arriving back in the shared channel. It is sent as an answer
to the customer's email, so it lands in the same thread in their mail program.

### Personal sending and retrying

When **Send from my … address** is checked:
- The `From` address is your directory email and display name, and the email lands in your
  own Sent Items in Microsoft 365, or your own **Sent** in Gmail.
- The `Reply-To` address is the channel's address and display name, so replies from the
  customer arrive in the shared channel and thread into the conversation.
- The message is never sent from the channel instead: if your own mailbox cannot send it,
  it fails, and you choose what to do.
- If your connection does not allow sending yet, or needs reconnecting, the composer offers
  an inline button: **Connect Outlook** or **Reconnect Outlook**, or **Connect Google** or
  **Reconnect Google**. A Google connection made for the calendar alone shows **Connect
  Google** here too: it asks Google for your consent to send as you (Gmail), keeping the
  calendar. Once Microsoft or Google answers, you are back in the same conversation, with
  the filters you had.
- If sending fails because your connection expired, was refused, needs multi-factor
  authentication or no longer allows sending, the message card displays:
  *Sending failed because your mail connection needs reconnecting.* with a **Reconnect
  Outlook** or **Reconnect Google** button and a **Retry send** button. Once you have
  reconnected, choose **Retry send** to queue the message again.
- A message sent from your own address can be about 3 MB in all, attachments included,
  whichever service sends it.
- **Gmail and threading.** Gmail may give a message it sends its own Message-ID, and
  Vantigo cannot read it back from your mailbox — it is only allowed to send. A customer's
  answer to a **reply** you sent from Gmail still threads, by the earlier messages it
  names. An answer to a **new conversation** you started from Gmail threads only if Gmail
  kept Vantigo's Message-ID; otherwise it arrives as a new conversation in the channel.

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
assistant is not configured the panel says so and you write the reply yourself. Which AI
model writes the draft, and which one suggests customers, is an Owner's choice under
**Workspace admin** › **Integrations** › **AI models**
([AI models](/en/user/workspace-administration/#ai-models)); the choice changes nothing
you do here.

## Messenger conversations

A conversation on a Messenger channel starts when a person writes to your Facebook Page.
**Start a conversation** only ever uses an email channel. The one way to write first is from
a customer's contact, while the person's window is open: see
[Write on Messenger](/en/user/customers/#messenger-on-a-contact). The
person's messages come into the Inbox by themselves, a few seconds after they are sent,
and the conversation's title is the person's name — Messenger gives no subject and no
email address. A person's new message joins their open conversation; once you have
closed the conversation, their next message starts a new one.

The thread is a chat. Each message is a bubble, the customer's on the left and yours on
the right. Pictures the customer sends are shown in the bubble and other files are listed
below it, as downloads. A message you sent says where it stands — **Sending**, **Sent**,
**Delivered**, **Read** — or **Not sent**, with the reason when there is one: *the
customer cannot be reached* (they blocked the Page), *the customer has to write again
first*, *Human Agent is not granted to the app* or *Messenger refused the message*. A
message sent from Meta Business Suite or another app on the Page is shown too, marked
**Sent from Meta**, so the thread is whole. Link the conversation to a customer with the
same buttons as an email conversation; Vantigo cannot do it by itself, since Messenger
does not give an email address. You can also link the person to a contact of that
customer: see [Link the person to a contact](#link-the-person-to-a-contact).

### Answer a customer in a chat

The composer is a chat composer: plain text, with no subject, no Cc and no formatting, and
**Attach file**. A reply can be a file alone, with no text; a file larger than Messenger takes
(25 MB) is refused. A counter under the box shows the characters against Messenger's limit of
2000; past it, **Send reply** is disabled. Sending needs `communications:conversations-reply`.

**The reply window.** Messenger only lets you answer someone who wrote to you in the last
24 hours. The composer shows where the window stands:

- *You can answer until …* while it is open.
- **More than 24 hours since the customer wrote**, when the messaging provider has been
  granted Meta's **Human Agent** permission: the reply is sent with Messenger's Human Agent
  tag, and you can answer for up to 7 days.
- **The conversation is closed for replies**, with *The customer must write again before
  you can answer here*, once the window has passed. **Send reply** is disabled. You can
  still write an internal note. When the customer writes again, the window opens again.
  A message sent from Meta Business Suite does not open it.

### Add answer options

A message can carry answer options the customer taps instead of typing. Under the text
box, **Answer options** has:

- **Add Yes / No** adds two options, **Yes** and **No** in your language. Rename them if you
  like.
- **Add option** adds one empty option. A message takes at most 13.
- Each option has a field for its title, a counter against its limit of 20 characters, and
  a **×** to remove it. The options are sent in the order they were added in. An option
  with no title, or one longer than 20 characters, stops the message from being sent.
- **Ask for e-mail** adds a button that lets the customer share their email address with
  one tap. It is one of Messenger's 13 quick replies, so with it on you can add 12 options.

When the customer taps an option, their answer arrives as a message shown as a chip with a
check — **✓ Yes** — and the question marks the option they chose. An email address the
customer shares arrives as a message saying **Shared an e-mail address**; if a contact in
Customers has that address, its customers are offered as candidates for the conversation,
and you decide which one to link. If exactly one contact has the address, the header also
offers **Link to** that contact (see below). A tapped option Vantigo cannot place — it belongs to
another conversation — is shown as plain text.

### Attach a product

**Attach product** is in the Conversations panel's composer too, under the same conditions.

**Attach product** in the chat composer shows a product to the customer as a row of cards
they can swipe through. It is offered when Products is on and you may see products
(`products:products-view`); without them the button is not there.

1. Choose **Attach product** and search for the product by name. Each product in the list
   says how many of its images can be shown. A product that has no image marked **Show in
   Communications** on its page cannot be attached, and the list says so: open the product
   and mark an image first (see [Images](/en/user/products/#images)).
2. Choose the product. Its images marked for Communications become the cards, in the order
   they have on the product, at most 10. Each card has the product's name as its title, the
   image, and a **subtitle** — with `products:pricing-view` it starts as the product's price
   excluding VAT, as the product page shows it, and without that permission it starts empty.
   Edit the subtitle or clear it; it is text of at most 80 characters, and Vantigo does not
   work out a price itself.
3. Remove a card with its **×** to leave that picture out; removing the last one removes the
   product. **Change product** replaces the product, since a message shows one product.
4. Every card carries a **Buy button**, titled **Kjøp** in Norwegian and **Buy** otherwise.
   You can rename it and add a **Second button** of your own, such as *Tell me more*; a
   button's title is 1 to 20 characters.
5. Write the message text as usual and choose **Send reply**. Answer options can be added
   to the same message.

The message keeps the pictures as they were when it was sent: changing or deleting the
product's images afterwards changes nothing in the conversation. If an image has been
removed or unmarked since you picked the product, sending says so and you attach the product
again. Messenger fetches the pictures from the installation's public address, so sending
cards needs one to be set up ([The public host](/en/admin/payments/#the-public-host));
without it, sending is refused with *The instance has no public host*.

When the customer taps **Buy** (or **Kjøp**) on a card, their answer arrives as **Wants to
buy: &lt;product&gt;** with the card's picture. If Orders is on and you may create orders
(`orders:access` and `orders:sales-manage`), the answer carries a **Create order** link.
It opens the new-order page with the conversation's customer, when one is linked, and with
the product: its only variant becomes the order's first line, and when the product has
several variants you pick which one. Nothing is created until you choose **Create the
order**. A tapped second button arrives as an ordinary answer chip.

### Link the person to a contact

A person who writes to your Page is known to Messenger only by an id that belongs to the
Page, so Vantigo cannot tell who they are. You can tell it, by linking the person to a
contact in Customers. A linked person shows in the customer's contact list with a Messenger
mark, and you can write to them from there ([Messenger on a contact](/en/user/customers/#messenger-on-a-contact)).
Linking, and unlinking, need `communications:conversations-manage`; without it the header names the contact and offers nothing to change.

In the header of a Messenger conversation:

- **Link to contact** — once the conversation is linked to a customer, a list of that
  customer's contacts and a button to link the person to the one you pick. Only a contact of
  the conversation's customer is accepted. Without a customer on the conversation there is no
  list: link the customer first.
- **Link to *name*** — shown when the email address the person shared belongs to exactly one
  contact. It is a suggestion only: Vantigo never links anyone by itself, so nothing is
  linked until you click it.
- **Linked to *name***, with **Unlink**, once the person is linked. One contact can be
  linked on several Pages, and a person is linked to one contact at most.

A contact's **Messenger link** is the other way. When someone opens it and writes, Vantigo
links them to the contact it was made for. If the person is linked to another contact
already, the link is left as it was and the header says **This person used *name*'s Messenger
link**, so you can see that the link may have been passed on. Linking again, or unlinking,
clears that notice. Where to get the link and why it should be sent to its contact only:
[Messenger on a contact](/en/user/customers/#messenger-on-a-contact).

### Set up a Messenger channel

A channel on Messenger needs a messaging provider that is available on the installation,
with a Facebook Page connected to it. Only then does **Add channel** open with a choice
first: **E-mail** or **Facebook Messenger**; without one, it is the email dialog alone.
**Facebook Messenger** is offered once Meta is configured. The dialog asks for the **Facebook
Page** — picked from the Pages connected under **Integrations** › **Meta** (an Owner
connects them; see [Meta](/en/admin/meta/)), one that cannot be used now listed disabled
with its reason, a Page whose token Meta rejected showing as **Unavailable** — and an optional **Display name**, which defaults to the Page's name. **Create
channel** adds the channel. Adding it needs `communications:channels-manage`, as an email
channel does. A Messenger channel has no credentials of its own and no mailbox: its row in
**Channels** says **Receiving** or why it is not, and a failing Page or provider shows
**Unavailable**. Messenger channels have their own default: the first Messenger channel is
the default Messenger channel, and making another channel default never changes the
default of the other type.

## An agent in the inbox

When a [virtual employee](/en/user/agents/) answers a channel, you see it in the Inbox like a
colleague, with the **AI** badge on its avatar:

- **Its messages** carry a line at the top that says who wrote them and that it is an AI, such as
  *«Hanna» er «Hansen Rør» sin virtuelle assistent (KI).* The mail also carries the header
  `X-Vantigo-Agent`, so a mail rule can tell it from a person's.
- **An internal note** says when the agent has a reply **waiting for review**, with an **Open in review**
  link to it, and when it has **handed the conversation to a colleague**, with what the customer wants.
- **A conversation assigned to you is yours.** The agent does not answer it, and if it was writing when you
  took it, its reply is not sent. Assign a conversation to the agent, or to nobody, to let it answer.
- **After a hand-off** the agent leaves the conversation alone until a person replies in it or assigns it,
  or it is given back to the agent.

### Handed-off conversations

A conversation the agent handed off shows a yellow banner above the thread: *An agent handed this
conversation to the people*, with the reason (the agent asked for a colleague, a reviewer discarded its
reply, its reply waited too long, the daily budget was reached …). **Give back to the agent** removes the
hand-off, so the agent answers the customer's next message; it needs `communications:conversations-manage`. Without a
click the agent stays away until a person replies in the conversation or assigns it.

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
channel you create becomes the default — of its type: email channels and Messenger channels
each have a default of their own.

The **Receive status** column says whether a channel receives mail: **Receiving**, with
*Last synced …*; **Waiting for first sync** just after the channel was added or switched;
**Failed: …** with the reason when Vantigo cannot read the mailbox — *The application can
no longer read the mailbox. Give it access in Exchange Online.*, *The mailbox no longer
exists in Microsoft 365.* or *The mailbox is not known for this channel. Verify the
channel.* — or **Last sync failed: …** when one attempt failed and Vantigo tries again
(*Mail channels through Microsoft 365 are not available.* while **Mail channels** is off or the
Entra connection is failing; the mail waits in the mailbox). Each reason names the
channel's own provider.
An SMTP channel shows **Sends only**.

A channel that cannot send shows an orange **Unavailable** badge in the **Status** column,
with the reason beneath: *This channel has no stored credentials, so it sends nothing.* for
an SMTP channel whose credentials are missing, or *Mail channels through Microsoft 365
are not available, so this channel sends nothing.* for a Microsoft 365 channel while its
provider is off. **An SMTP channel without credentials sends nothing**: Vantigo never falls back on
the mail server settings the administrator configured for its own mail. A message queued on
such a channel fails at once, and a channel that is unavailable is not offered to Invoices
or Calendar as a place to send from.

A channel sends through its **Provider**: SMTP, or a mailbox service Vantigo is connected
to. The **Provider** list in the **Add email channel** dialog shows **SMTP** first, then
every mailbox service the administrator has turned on, by its name — **Microsoft 365**
and **Google Workspace** — and a service that is turned on but cannot be used right now is
greyed, with *Not available now* and the reason beside it:

- **SMTP** — through a mail server, with an SMTP account. The administrator must have the
  application secret in place and know the SMTP host, port and account; the password is
  stored encrypted and never shown again, and the secret it is encrypted under is
  explained under [SMTP](/en/reference/communications/#smtp). The mail server settings an
  administrator configures for Vantigo's own invitations and password resets are a
  different thing: they write to your own employees, and no channel, invoice or
  invitation ever uses them.
- **Microsoft 365** — from an Exchange Online **shared mailbox**, through Microsoft 365,
  with no password to enter. It also **receives**: mail arriving in the shared mailbox's
  Inbox from the day the channel is added comes into the Inbox — not the mail that was
  there before. It is listed once the Microsoft Entra ID app is on, and can be chosen once
  someone has turned on **Mail
  channels** in the Microsoft Entra ID app and an Exchange administrator has given
  Vantigo access to the mailbox ([the administration
  guide](/en/admin/entra/#mail-channels)). When the application's access reaches every
  mailbox in the company, an orange notice above the table, *Mail access reaches every
  mailbox*, says *Mail access is granted to every mailbox in the tenant. Limit it with
  RBAC for Applications and remove the tenant-wide consent. While this shows, anyone who
  manages channels in Communications can send as any shared mailbox in the tenant.* The
  channels still work.
- **Google Workspace** — from a licensed Gmail mailbox of your organisation, such as
  `post@`, through Google Workspace, with no password to enter. It also **receives**, as
  Microsoft 365 does. It is listed once the Google Workspace app is on, and can be chosen
  once an Owner has turned on **Mail channels** on the Google Workspace page and a Google
  super admin has delegated the Gmail scopes ([the administration
  guide](/en/admin/google-workspace/#mail-channels)). Google cannot limit Vantigo's access
  to some mailboxes, so an orange notice *Mail access reaches every mailbox* shows above
  the table whenever Google Workspace can be used: *Mail access through Google Workspace
  reaches every mailbox in the domain: domain-wide delegation cannot be limited to some
  mailboxes. While this shows, anyone who manages channels in Communications can send as
  any mailbox in the domain that is no person's own.* Vantigo uses only the mailboxes that
  are channels, and refuses a person's own. When both services reach every mailbox, each
  has its own notice.

The provider list in the **Add email channel** dialog ends with **Vantigo hosted mail**,
greyed, with *Not yet available*: it is not offered yet, and cannot be chosen.

Invoices and Calendar send to your customers **through a channel you choose**: Invoice
settings has a **Sending** card and Calendar's settings an **Invitations to people outside
the company** card, each listing the channels here with their provider, or **Disabled**
([Sending a document by e-mail](/en/user/invoices/#sending-a-document-by-e-mail),
[Invite people outside the company](/en/user/calendar/#invite-people-outside-the-company)).
Deactivating a channel they use makes them say the channel no longer exists, and they
send nothing until someone chooses another.

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
asks for the **Email address** of the shared mailbox, a **Display name**, and two settings
for the mailbox, and says *The mailbox must be a shared mailbox in the application's
scope.*:

- **Import history** — **From now on** (the default), **Last 30 days** or **Last 90 days**.
- **When a conversation is closed** — **Leave as-is in the mailbox** (the default),
  **Mark as read in the mailbox**, or **Move to a folder in the mailbox** (which asks for
  the **Mailbox folder**, filled in as `Handled in Vantigo`, created in the shared mailbox
  if it does not exist yet).

**Create
channel** checks the mailbox with Microsoft 365 first; the channel is created active, with
the mailbox's own primary address, and can send at once. The check can refuse it:

| The dialog says | Means | Who fixes it |
| --- | --- | --- |
| *This is a person's own mailbox. Only a shared mailbox can be a channel.* | The address is a person's mailbox. | Use a shared mailbox; an Exchange administrator can convert one. |
| *The address is a group. A group has no mailbox Vantigo can read; use a licensed mailbox.* | The address belongs to a group, not a mailbox (a mail service that has group addresses says so; Microsoft 365 does not). | Use a mailbox's address. |
| *The mailbox is outside the application's scope. Give the application access to it in Exchange Online.* | Vantigo has not been given access to this mailbox, or the access has not taken effect yet — it can take up to 2 hours. | An Exchange administrator, by [How to give the application access](/en/admin/entra/#mail-channels). |
| *Microsoft 365 has no mailbox with this address.* | No mailbox has this address — or it is an alias. | Use the mailbox's primary address. |
| *Mail channels through Microsoft 365 are not available. An administrator turns them on under Integrations.* | **Mail channels** is off, or the Entra connection is not working. | Whoever manages the Microsoft Entra ID app. |
| *Another channel already uses this mailbox.* | Another channel uses the same mailbox, perhaps by another of its addresses. | Use that channel. |
| *A channel with this address already exists.* | Another channel has this address. | Use that channel. |
| *Microsoft 365 did not answer. Try again in a moment.* | Microsoft 365 was busy or unreachable. | Try again. |

Each message names the provider you chose. Adding, switching and verifying channels are limited to 30 a minute; past that,
*Too many attempts. Wait a minute and try again.*

### Add a Google Workspace channel

Click **Add channel**, and choose **Google Workspace** as the **Provider**. The dialog
asks for the same as for Microsoft 365: the **Email address** of the mailbox, a **Display
name**, **Import history** and **When a conversation is closed**. Its note says *The
mailbox must be a licensed user of the organisation in Google Workspace that is no
person's own, such as post@. A group cannot be a channel.*: Google Workspace has no shared
mailbox of its own, so the mailbox is a user made for the purpose, and takes a licence.

**Create channel** checks the address with Google Workspace first. An alias is taken as
its mailbox, and the channel is created active with the mailbox's primary address. The
check refuses, with the messages in the table above:

- **A group's address** — *The address is a group. A group has no mailbox Vantigo can
  read; use a licensed mailbox.* A Google Group, a Collaborative Inbox included, receives
  mail for its members but has no mailbox of its own. Use a licensed user's address, and
  let the group forward to it if you need both.
- **A person's own mailbox** — *This is a person's own mailbox in Google Workspace. Only a
  mailbox that is no person's own, such as post@, can be a channel.* when the address
  belongs to someone whose Google account Vantigo keeps through Staff, or who connected
  their own Google account to Vantigo. A channel must never read or send from a
  colleague's own mail.
- **An address Google Workspace does not know** — *Google Workspace has no mailbox with
  this address.*: no user has it, the user is suspended, archived, or has no Gmail
  licence, or the address is not in one of your organisation's verified domains.
- **A mailbox Google refuses** — *Google Workspace refused the mailbox for another reason
  than the delegation, such as a policy on the user. Check the user in the Google Admin
  console.*, with a link, **Mail channels in Google Workspace**, to [the administration
  guide](/en/admin/google-workspace/#mail-channels).
- *Mail channels through Google Workspace are not available…* while **Mail channels** is
  off on the Google Workspace page, or the Gmail scopes are not delegated yet.

What differs from Microsoft 365, once the channel works:

- **Size.** A message on the channel can be up to 25 MB — Gmail's limit — so the composer
  takes attachments up to about **18.2 MB** in total.
- **Receiving** is fetched, not pushed: Vantigo looks for new mail about **every five
  minutes**, and a message arrives within that.
- **Sent mail** is in the mailbox's **Sent** in Gmail. Gmail may give a sent message its
  own Message-ID; Vantigo keeps both, and Gmail's thread, so a customer's answer still
  joins its conversation.
- **When a conversation is closed**: Gmail has labels, not folders. **Mark as read in the
  mailbox** takes the message's *Unread* off; **Move to a folder in the mailbox** gives it
  the label named in **Mailbox folder** (`Handled in Vantigo` unless you change it),
  created if it does not exist, and takes it out of the **Inbox**.
- **Archiving or deleting** a message in Gmail after Vantigo read it changes nothing in
  Vantigo; mail a Gmail filter keeps out of the Inbox is never read.
- **A mailbox that becomes a person's own** stops: if Staff later keeps a Google account
  for the mailbox's user, the user connects their own Google account, or the mailbox's
  address is renamed or given to someone else, the channel fails with *The mailbox no
  longer exists in Google Workspace.* and sends and reads nothing more. A refused mailbox
  reads *Failed: Google Workspace refuses the mailbox. Check the user in the Google Admin
  console.*
- **A folder or a category named as a Gmail system label** — *Inbox*, *Sent*, *Trash*,
  *Spam*, *Drafts*, *Starred*, *Important*, *Unread*, *Chat* — is refused: choose another
  **Mailbox folder** name.

### Switch an SMTP channel to a mailbox service

When the mailbox an SMTP channel sends from is a shared mailbox in Exchange Online, the
channel can send through Microsoft 365 instead, keeping its conversations, its address
and its **Default** mark. On its row, choose **Switch to Microsoft 365** — there is one
**Switch to …** for each mailbox service that can be used now, **Switch to Google
Workspace** for a Gmail mailbox; the dialog
warns *The SMTP settings are deleted. To go back you enter them again.*, and offers the
same **Import history** and **When a conversation is closed** choices as when adding a
Microsoft 365 channel — choose
**Switch**. Vantigo checks the mailbox as when adding one, with the same messages, and
the row's provider reads **Microsoft 365**: "Channel switched — The channel now sends and
receives through Microsoft 365." From then on the mailbox's mail from the chosen import
window comes into the Inbox. The action is shown only while Microsoft 365 is
available. A channel already on one mailbox service cannot be switched to another
directly: *The channel uses another mailbox provider. Switch it back to SMTP first, then
to the new provider.*

On any Microsoft 365 or Google Workspace channel's row, **Mailbox on close** opens a dialog to change **When a
conversation is closed** (*Leave as-is in the mailbox*, *Mark as read in the mailbox*, or
*Move to a folder in the mailbox* with its **Mailbox folder**).

The channel keeps the address it had. If that address is an alias of the shared mailbox
rather than its primary address, check that the first message goes out as expected:
how Microsoft 365 sends from an alias has not been verified yet. Adding the channel anew
by the mailbox's primary address avoids the question.

**There is no button to go back.** The channels page cannot switch a channel back to
SMTP; an administrator can, through the API, by sending the channel's SMTP settings
again with the password ([Switching provider](/en/reference/communications/#switching-provider)).

### Verify and deactivate

Per channel, **Verify** checks that the channel can send: for SMTP it connects to the
server with the stored credentials, for Microsoft 365 and Google Workspace it checks the mailbox again, and
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
| Change status, assignee, tags and customer link, add notes, ask for a customer suggestion, link a Messenger person to a contact and get a contact's Messenger link | `communications:conversations-manage`, with view |
| See and manage Channels | `communications:channels-manage` |
| See and manage Suppressions | `communications:suppressions-manage` |

An area whose permission you lack is not shown in the sidebar. Permissions are granted
through roles under Workspace administration — see
[Finding your way around](/en/user/).
