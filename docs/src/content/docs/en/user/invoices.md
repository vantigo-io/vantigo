---
title: Invoices
description: Drafting, issuing and sending invoices, credit notes, payments, the journal and the export.
sidebar:
  order: 50
sources:
  - apps/invoices/frontend
---

The Invoices app issues the sales documents of your bookkeeping: a draft becomes a
numbered invoice or credit note the moment it is issued, gets a PDF, and from then on
never changes. The app has three areas in its sidebar: **Invoices**, **Invoice
journal** and **Invoice settings**. Every amount is in NOK in this phase. An invoice is
handed over as a PDF — by download or by e-mail — or as an EHF e-invoice over the Peppol
network, once your installation is set up for it
([what the law asks](/en/reference/invoices/#the-law-in-one-page)).

Opening the app at all needs `invoices:access`; the other permissions are named where
they apply and summed up under [Permissions](#permissions).

## Before the first invoice

Nothing can be issued until the company itself is described. Until then the list shows
**The seller is not complete** to anyone who may issue.

### The seller record and the number series

Open **Invoice settings** in the sidebar (shown only with `invoices:manage`; without it
the page says *Changing the invoice settings needs invoices:manage*). Under **The
seller**, the list **What issuing needs** ticks off what is filled in: **Legal name**,
**Organisation number**, **Address**, **Postal code**, **City** and **Bank account**. A
last, informative line says whether e-mail is set up — *Mail is configured (SMTP)* or
*Mail is not configured (SMTP), so documents cannot be sent by e-mail. Issuing does
not need it.*

Fill in the fields and click **Save**:

- **Legal name**, **Organisation number** (nine digits with a valid check digit),
  **Address**, **Address line 2**, **Postal code**, **City**, **Country** (a two-letter
  code such as NO) and **E-mail** — the address replies to sent invoices go to.
- **Bank account** (eleven digits with a valid check digit), and **IBAN** and **BIC**
  for buyers abroad.
- **Default payment terms (days)**, 0 to 365: the terms a new draft starts with when
  the customer's billing profile names none.
- **Registered for VAT (prints MVA after the number)** and **Registered in
  Foretaksregisteret** — both print on the document as the regulation asks.
- **Footer text**, printed at the bottom of every document.
- **The number series starts at** — the number the first document gets. It locks the
  moment anything is issued, and the field then reads *Locked: documents are issued
  from this series*.

Below **The seller** the page has two more cards, **E-invoicing** and **KID**, described
next. The **Save** button at the bottom of the page saves the seller, the Peppol id and
the KID agreement together; the access point has a save button of its own.

If a colleague saved the settings while you were editing, the form says **The settings
changed** and offers **Reload**; your unsaved edits are dropped, never merged.

### Set up e-invoicing

EHF is the Norwegian e-invoice: the document as data your customer's system reads, with
the PDF inside, delivered over the Peppol network. Vantigo hands it to an **access
point**, a provider that carries it onto the network; Storecove is the one Vantigo
supports. Getting a Storecove account and its key is an administrator's job
([E-invoicing administration](/en/admin/e-invoicing/)); entering them here needs
`invoices:manage`, like the rest of the page.

The card **E-invoicing** — *Send documents as EHF over the Peppol network, through an
access point* — starts with **What e-invoicing needs**:

- **Peppol id 0192:974760673** with a tick when the seller's Peppol id is set, or *The
  seller's Peppol id is missing: enter it, or the organisation number it is derived
  from.* This line is about e-invoicing only: issuing never waits for it.
- An informative line: *Sending as EHF is available*, or *Sending as EHF is not
  available. It needs e-invoicing switched on by the operator, the Peppol lookup
  enabled, the access point's credentials and the seller's Peppol id.* The first two
  are the operator's settings, not this page's.

**Peppol id** is your address on the Peppol network, the one EHF invoices are sent from.
Left empty, it is `0192:` and the organisation number — the field shows that as its
placeholder, and saving fills it in. A Norwegian business needs nothing else here; a
`0192` id must be your own organisation number. It is saved with the page's **Save**,
and a change applies to every EHF sent from then on, documents issued earlier included.

Under **Access point** the card shows what is stored:

- **Provider** is **Storecove**, the one provider so far.
- **Legal entity id** is the Storecove legal entity the documents are sent as, a whole
  number the administrator gets from Storecove; once saved, the field shows it.
- **API key** is Storecove's key. It is stored encrypted and never shown again: once one
  is stored the card shows **Key stored**, the field stays empty, and typing a new key
  replaces it. Leave the field empty to keep the stored key.

Click **Save access point**; the message says *The access point is saved*. **Verify**
and **Remove the credentials** stay greyed out until a key is stored. Click **Verify**:
Vantigo asks Storecove with the stored key and answers one of three ways —

- *The access point accepted the key.* You are done.
- *The access point refused the key. Check it and save it again.*
- *The access point could not be reached, or the key does not reach this legal entity.
  Check the legal entity id, or try again later.*

If Vantigo cannot read the stored key at all — the installation's secret was changed —
Verify says *No key is stored, or the stored key can no longer be read here. Enter the
key again.*: enter the key again and save it.

**Remove the credentials** asks first — **Remove the access point's credentials?** —
because the stored key is deleted and cannot be shown again, and nothing can be sent as
EHF until a key is saved again; confirm with **Remove the credentials**. The removal is
refused while a document is still on its way — *A document is still on its way through
this access point. Wait until it is delivered or failed before removing or switching the
credentials.* Replacing the key with a new one is never refused.

If Storecove refuses the stored key while documents are being sent, the **Access point**
part of the card shows a red **The access point refused the key**, with the date it
happened when that is known: *The provider refused the stored API key. Documents wait in
the queue until a valid key is saved.* Save the right key soon — the alert goes as soon
as you do, and each waiting document goes out when it is next due, within the hour —
because a document still queued 48 hours after it was sent is taken out of the queue as
**Failed** (or **Unconfirmed**, when Storecove may have it) and needs a person ([the
states](#following-it-on-the-e-invoice-card)).

### Agree a KID with the bank

A KID is the payment reference your bank matches an incoming payment by. To use one, ask
your bank for a KID agreement (OCR giro) on the account in the settings: the bank
registers a **length** and a **check digit** method for it. Enter exactly what the bank
registered on the card **KID** — *The customer identification your bank agreed for
incoming payments* — and click the page's **Save**:

- **KID length**: 4 to 25 characters, the check digit included.
- **Check digit**: **MOD10 (recommended)** or **MOD11**. Under MOD11 a number whose check
  would be 10 gets a `-` as its check digit, which some payers find odd; that is why
  MOD10 is the common choice.

Give both or neither (*Choose both the length and the check digit, or neither.*). Without
an agreement the card says *No KID agreement: invoices carry their number as the payment
reference.*

With an agreement the card previews the next one, from the next invoice number the
server reports, such as *Next KID: 0010017 (invoice 1001)*: the invoice number
zero-padded to the length less one, then the check digit. Two
warnings can appear:

- In red, *Invoice 1042, the next to be issued, does not fit in 4 characters with its
  check digit. Choose a longer KID.* — the save is refused until it fits.
- In yellow, whenever the saved agreement leaves too little room, *The next invoice
  number leaves fewer than two digits of room in the KID's length. Ask the bank for a
  longer KID before the numbers outgrow it.*

What changes: every invoice issued from then on gets a KID. It is printed as **KID** in
the PDF's payment block, the e-mail asks the buyer to pay quoting it instead of the
invoice number, and an EHF carries it as the payment reference. A credit note never gets
one, and an invoice issued before the agreement keeps none — an EHF without a KID carries
no payment reference at all, so the buyer's system never mistakes the invoice number
for one.

Changing or clearing the agreement later is allowed. Once documents are issued, the card
reminds you: *Issued invoices keep the KIDs computed under the agreement they were issued
with. Ask the bank to keep the old length valid until they are paid.* A length the
next number does not fit in is never saved (the red warning above). Issuing stops only
when the invoice numbers outgrow a length that fitted when it was saved — *The next
invoice number no longer fits the KID agreement's length. Change the KID agreement in
the settings.* The yellow headroom warning is there so that never comes as a surprise.

### VAT codes

Lower on the same page, **VAT codes** lists every code with its **Code**, **Name**,
**Category**, **SAF-T code** and **Rate today**. Vantigo seeds the Norwegian codes
(3 at 25 %, 31 at 15 %, 32 at 11.11 %, 33 at 12 %, and the 0 % codes 5, 51, 52, 6
and 7), so most businesses need to change nothing here.

- **Add a VAT code** asks for the code, name, SAF-T code, category and an exemption
  reason, and for the first **Rate %** and the day it is **Valid from**.
- The pencil (**Edit VAT code …**) edits the code, its name, its exemption reason and —
  until lines carry it — its category and SAF-T code; the box **Offered for new lines**
  takes a code out of the editor's list without touching the lines that already carry
  it. Once lines carry a code, *its category and SAF-T code are fixed*: deactivate it
  and create a new one instead.
- **Rate periods** shows a code's rates as dated periods. Give a **New rate %** and the
  day it is **Valid from** and click **Change the rate from this date**; the previous
  period closes the day before. Only the latest period can be removed, and only while
  it still lies in the future. A rate cannot be changed from a day on or before an
  issued document's date ([the rules](/en/reference/invoices/#endpoints)).

## Finding a document

**Invoices** in the sidebar lists every draft and issued document: drafts first, then by
number, newest first. Each row shows the **Number** (or *Draft*), the **Kind**, the
**State**, the **Customer**, the **Issue date**, the **Due date**, the **Total**, what
is still **Open** on an issued invoice, and under **EHF** where an issued document
stands as an e-invoice: *Not sent*, *Queued*, *Submitted*, *Delivered*, *Failed*,
*Unconfirmed* or *Cancelled* ([what each means](#following-it-on-the-e-invoice-card));
a draft's is blank. Click a row's number to open it.

Narrow the list with the chips and fields above it:

- **Status**: *Any status*, *Draft* or *Issued*.
- **Kind**: *Any kind*, *Invoice* or *Credit note*.
- **State**: *Any state*, *Open*, *Partially paid*, *Overdue*, *Paid* or *Credited*.
  Only an issued invoice has one of these states; drafts and credit notes never match
  the filter. On a phone the chips become a drop-down.
- **Customer** — a search of the customers, archived ones included, since their
  documents are kept. The field is shown when you also hold `customers:view`.
- **Search** by *Number or buyer*, and **Issued from** / **Issued to** for a range of
  issue dates.

The states are judged on the day you look, not stored: *Open* until the due date,
*Overdue* from the day after it, *Partially paid* once something is registered,
*Paid* when nothing is open, *Credited* when credit notes cover the whole invoice
([how the state is derived](/en/reference/invoices/#payments-and-the-state-of-an-invoice)).

## Creating a draft

Click **New invoice** at the top of the list. The button needs `invoices:create` and
`customers:view`, because the buyer is picked from the Customers app. Pick the
**Customer** — only active customers are offered — and click **Create the draft**. The
draft opens in the editor at once.

What the draft starts with comes from the customer's billing profile in Customers
([the Customers guide](/en/user/customers/)): **Your reference** is the profile's buyer
reference and the payment terms are the profile's, or the settings' default when the
profile has none. **Our reference** is prefilled with your own name, and the delivery
is set to today until you change it.

The draft can be refused when the customer was merged into another (*invoice that one
instead*), is archived, is blocked for invoicing, or no longer exists — the same checks
run again on every save and at issue ([the customer gates](/en/reference/invoices/#drafts)).

## Editing and deleting a draft

The editor shows the draft under the heading **Invoice — Draft** with its buyer, its
delivery, its references, its lines and its notes. Changes are kept in the browser until
you click **Save**; while there are unsaved changes a line says *Save the changes before
previewing or issuing*.

- **Customer** can be changed to another active customer, never cleared.
- **Delivery** is **A day** with a **Delivery date**, or a **Delivery period** with
  **Delivered from** and **Delivered to** — both ends, or Save waits. Without a delivery
  the draft can be saved but not issued (*A delivery date or period is needed before the
  document can be issued*). Tick **Delivered somewhere other than the buyer's address**
  to give a place of delivery, which prints on the document only then.
- **Your reference** (the buyer's; while empty the field notes that an EHF e-invoice
  will need it), **Our reference**, **Order reference** and **Payment terms (days)**,
  0 to 365. The due date is the issue date plus the terms, fixed at issue. When the
  customer is invoiced by EHF — their billing profile prefers it, or they have a Peppol
  id — and both **Your reference** and **Order reference** are empty, a yellow warning
  beside the references says *This customer is invoiced by EHF, which needs your
  customer's reference or an order reference. Add one before issuing: neither can change
  afterwards.* Take it seriously: an issued document without either can never be sent
  as EHF, only credited and issued again.
- **Lines**: **Add a line**, then its **Description**, **Quantity** (up to three
  decimals), **Unit**, **Unit price** (up to four decimals), **Discount %** and **VAT
  code** from the codes offered for new lines; the **Amount** is the line's net. The
  arrows move a line, the bin removes it. A line whose code has since been taken out of
  the list still shows it, marked *(no longer offered)*.
- Below the lines, the totals per VAT rate, **Total excluding VAT**, **VAT** and
  **Total**. While you edit they are *an estimate*; the saved draft's totals are the
  server's, computed with the rates in force today.
- **Note** is printed on the document; **Internal note** never is.

A box named **Worth a look** lists the draft's warnings: the customer is invoiced in
another currency, the delivery ended more than a month ago, a line's VAT code has no
rate today. A warning never stops a save, but the last one would stop the issue
([the warnings](/en/reference/invoices/#drafts)).

**Preview** opens the draft as a PDF in a new tab, watermarked *UTKAST — ikke et
salgsdokument* and without a number; it is held back while there are unsaved changes.
**Delete** asks *Delete the draft?* and reminds you that nothing was issued, so no
number is lost. Both need `invoices:create`; without it the editor is read-only.

If someone else saved the draft meanwhile, the editor says **The draft changed** and
offers **Reload**, which drops your unsaved edits for the latest version.

**A draft made from work.** A draft can bill uninvoiced work — hours, expenses and
billing milestones from the projects — and then keeps that work on its lines: each line
knows which entries it bills, and while the draft holds them no other draft can take
them. Saving keeps the work with its line, even when you edit the line's text or amount;
a line whose amount no longer matches its work says so under **Worth a look** (*A line's
amount differs from the work it bills*), which is allowed — a write-down, a rounding.
Removing the line, or changing the draft's customer, releases its work, which is then
uninvoiced again; the save says *The save released work from this draft*. When work has
changed since it was added, or can no longer be invoiced — an entry unapproved, a
milestone moved back — the draft warns about it, and refreshing the work takes its
current figures and drops what can no longer be invoiced; the issue would refuse either.
Work arrives on a draft only through the uninvoiced view of a customer or a project,
which comes with the screens for it; until then no draft holds any.

## Issuing

Click **Issue** in the draft's header — offered with `invoices:issue`, after a save, and
only while the installation has a document store (*This installation has no document
store, so nothing can be issued*). The dialog **Issue the invoice** warns that *this
assigns the next number and cannot be undone* and shows the **Issue date**: today, or a
choice between today and the last day of the previous month when the regulation allows
backdating ([the issue date](/en/reference/invoices/#the-law-in-one-page)). Click
**Issue**.

The draft becomes **Invoice 1001** — the next number in the one series invoices and
credit notes share — and a message says *Issued as number 1001*. Everything on it is now
fixed: the lines, the VAT per rate at the issue date's rates, the due date, and a copy
of the buyer as the customer was at that moment and of the seller as the settings were.
Later changes to the customer or the settings never reach an issued document. If the
delivery ended more than a month earlier the issue still succeeds and reminds you to
*mind the one-month deadline next time*.

An issue is refused, and the number given back, when the seller record is incomplete,
the draft has no lines or no delivery, the buyer has neither a complete address nor an
organisation number, a line's VAT code is no longer offered or has no rate on the issue
date, the lines do not match the seller's VAT registration, a reverse-charge line lacks
the buyer's organisation number, or the chosen date is not allowed that day. Each
refusal is said in the dialog ([every refusal](/en/reference/invoices/#issuing)).

**Issuing a draft made from work** also marks that work invoiced — the hours, expenses
and milestones its lines bill — in the same step, so they leave the uninvoiced work for
good. The issue is refused, naming the line, when a piece of work on it can no longer be
invoiced (*Work on line 2 can no longer be invoiced*), has changed since it was added,
has already been marked invoiced, belongs to a project that no longer bills this customer
or that is now fixed-price or non-billable — and also when the Projects module is switched
off, since the work then cannot be checked, or when the draft was saved by someone else
while you issued. Nothing is issued and no number is used: refresh the work or edit the
line, and issue again ([the write-back](/en/reference/invoices/#the-write-back)).

## Downloading the PDF

An issued document's page offers **Download PDF**. The file is named after the document
and the buyer's language — `faktura-1001.pdf` or `invoice-1001.pdf`, `kreditnota-1002.pdf`
or `credit-note-1002.pdf` — and is the one PDF rendered and stored at issue, served
exactly as stored every time ([the PDF](/en/reference/invoices/#the-pdf)). Anyone with
`invoices:access` can download it.

An invoice with a KID shows it in the payment block, as **KID**, and asks the buyer to
pay with it instead of the invoice number ([the KID agreement](#agree-a-kid-with-the-bank)).

If the store could not be reached at issue, the page says *The PDF could not be stored
when the document was issued. It is stored the first time it is downloaded* — the first
download, or the first send, stores it.

## Crediting an invoice

To reverse an issued invoice, in full or in part, open it and click **Credit** — offered
with `invoices:issue` while the card **Credit notes** still shows something *Left to
credit*. Vantigo makes a **Credit note — Draft** that copies the invoice: its buyer as
the invoice named it, its delivery and references, and every line.

In a credit-note draft you may only take away: remove lines, lower a **Quantity** or a
**Unit price**, and edit descriptions and notes. The customer, the delivery, the
references and the VAT codes stay as the invoice had them, and a credit note has no
payment terms. Keep every line for a full reversal; remove or lower lines for a partial
one. Then **Issue the credit note** as you issue an invoice.

The credit note takes the next number in the same series and is listed under **Credit
notes** on the invoice, which links back with *Credit note for invoice 1001*. The
invoice's **Open** amount falls by the credit note's total; when credit notes cover the
whole invoice its state becomes **Credited**. A credit note after a payment is allowed:
the invoice then shows **Refund due**, what is owed back. The draft warns, and the issue
refuses, a credit note larger than what the invoice or a line has left; a credit note
cannot itself be credited ([credit notes](/en/reference/invoices/#credit-notes)).

## Registering payments

Vantigo does not read bank files: money received is registered by hand, on the issued
invoice's page under **Payments**. Each registration needs `invoices:payments`.

Click **Register payment** — offered while something is left to pay — and fill in:

- **Paid on**: the day the money arrived, prefilled with today; on or after the issue
  date and not after today.
- **Amount**: prefilled with the open amount; more than 0 and at most the open amount.
  An overpayment is refused, with the open amount named.
- **Reference** (the bank's or the payer's) and **Note**, both optional.

Click **Register**. The table lists each payment's **Paid on**, **Amount**,
**Reference**, **Note** and when it was **Registered**, and the totals gain **Paid** and
**Open** — the total less what is credited and paid. A payment of exactly the open
amount makes the invoice **Paid**; a smaller one makes it **Partially paid**, or leaves
it **Overdue** when the due date has passed. A credit note takes no payment
([payments and the state](/en/reference/invoices/#payments-and-the-state-of-an-invoice)).

A wrong registration is never edited: click **Remove** beside it, give a **Reason** in
the dialog **Remove the payment**, and confirm. *The payment stays on the invoice,
struck through with the reason. A removal cannot be undone; register the payment again
if it was right after all.*

## Sending a document by e-mail

Open an issued invoice or credit note and click **Send**. The button needs
`invoices:issue` and an installation with e-mail set up; without SMTP the button is not
there, and the settings page says so.

The dialog **Send by e-mail** opens with the **Recipient** prefilled from the
customer's invoice e-mail in their billing profile — the address as it is today, not as
it was at issue. Change it to send the document elsewhere; only *a bare e-mail address,
such as faktura@example.no* is accepted. When the customer has no invoice e-mail, or the
address could not be read, the field starts empty and you enter one.

Above the field the dialog shows what you should know before sending. Two are red and
cannot be missed: **The customer expects EHF** and, from 1 January 2027 for a Norwegian
business, **An e-invoice is required** — an e-mailed PDF does not meet the e-invoicing
duty; when the document could go as EHF instead, the first says so
([e-mail or EHF](#when-the-customer-expects-ehf-but-you-e-mail)). The rest are notes: the
buyer is a Norwegian business (before 2027), or the customer prefers eFaktura or paper.
None of them stops the send. On an invoice that is
partly paid or credited a note says the e-mail asks only for the outstanding amount; on
a settled one, that it says nothing is due.

Click **Send**. The mail goes at once, with the stored PDF attached and a short plain
text — *Faktura 1001 fra <seller>* or *Invoice 1001 from <seller>* in the buyer's
language — asking for the open amount to the seller's account, marked with the invoice's KID when
it has one and with its number otherwise; replies go to the e-mail
in the settings ([the texts](/en/reference/invoices/#sending-a-document)). A message
confirms *Sent to …*, and the card **Sent by e-mail** on the document gains a row with
**Sent**, **To** and **Subject**. The **To** column is shown only to people with
`invoices:issue`; others see when and under which subject each send went.

A send is refused when the customer has been anonymised (*The customer has been
anonymised and is not contacted again*), when there is no address, when the document
store is unavailable, or after more than 60 sends in ten minutes from one place. If the
mail server does not confirm the send, the dialog says *The mail server did not confirm
the e-mail. Nothing was recorded; it may still have arrived. Check with the customer
before sending again.* A row under **Sent by e-mail** means the mail server accepted the
mail, not that it arrived: a bounce goes to the installation's sender address and is not
recorded here. Sending the same document again is allowed and logged again.

## Sending as EHF

Sending as EHF hands an issued invoice or credit note to the customer's own system over
the Peppol network, with its PDF inside. It needs `invoices:issue` and an installation
set up for it — the card **E-invoicing** in the settings says *Sending as EHF is
available* ([Set up e-invoicing](#set-up-e-invoicing)) — and a document issued to a
customer who had a Peppol id at the time.

### Send an invoice as EHF

Open the issued document. Which button comes first depends on the customer:

- When the customer's billing profile prefers EHF — or the customer has a Peppol id and
  no preference — and the document can be sent, **Send as EHF** is the page's main
  button, beside **Download PDF**, and **Send** (by e-mail) stands beside it as the
  second choice.
- Otherwise **Send as EHF** sits on the card **E-invoice (EHF)**, offered while the
  document can be sent.

When it cannot be sent, the card says why, in the same words a refused send would use
(below) — or, when the installation is not set up, *This installation cannot send EHF
yet: see E-invoicing in the invoice settings.*

Click **Send as EHF**. The dialog names where the document goes — *To Peppol id
0192:…*, the id the document was issued with — and the document and its amount, and
explains what happens: *Before the document is queued, the Peppol network is asked
whether the receiver accepts this kind of document. An access point then delivers it,
and the E-invoice card follows it.* If the document has already gone by e-mail, a note
says *This document has already been sent by e-mail*; sending it as EHF as well is
allowed. Click **Send as EHF** in the dialog. The message *Queued for sending as EHF*
confirms it, and the card shows **Queued**.

A send can be refused; the dialog then says *Could not send as EHF* and why:

- *The document was issued to a buyer without a Peppol id, so it cannot be sent as EHF.
  Send it by e-mail, or credit it and issue it again once the customer has one.* The
  document keeps the customer as they were at issue, so adding the Peppol id to the
  customer afterwards does not help this document.
- *EHF needs the buyer's reference or an order reference, and the document has neither.
  Credit it and issue it again with one.* This is the trap the draft warns about.
- *The document is already on its way as EHF, or delivered. Cancel or resolve that
  transmission first.*
- *The document's EHF breaks a Peppol rule, so it cannot be sent as EHF*, followed by
  *The rules it breaks:* — a line in VAT category K is said in words (*A line is in VAT
  category K (intra-community supply), which is not sent as EHF*), any other rule by its
  official id. Send such a document by e-mail.
- *The receiver does not accept this document as EHF on the Peppol network. Send it by
  e-mail instead*, with what the network answered: *The receiver is not registered on
  the Peppol network*, or *The receiver is registered on the Peppol network, but does
  not accept this kind of document* — some receivers take invoices but not credit
  notes.
- *The Peppol network could not be asked whether the receiver accepts EHF. Try again.*
- That e-invoicing is unavailable on this installation: the operator's switch or the
  Peppol lookup is off, or the access point's credentials or the seller's Peppol id are
  missing or can no longer be read. Someone with `invoices:manage` looks at
  **E-invoicing** in the settings.
- *The document store is unavailable*, *The customer has been anonymised and is not
  contacted again*, or more than 60 sends as EHF in ten minutes from one place.

### Following it on the E-invoice card

Every issued document has the card **E-invoice (EHF)** beside **Sent by e-mail**. It
shows the latest state in words, with when it was queued, submitted, delivered or
failed, and — to people with `invoices:issue` — the provider's reference and the reason
a transmission failed. Below, **Transmissions** lists every attempt, newest first, with
when it was **Queued**, its **Status** and the **Receiver**.

| The card says | What it means for you |
| --- | --- |
| **Not sent** | *Not sent as EHF yet.* |
| **Queued** | Vantigo holds it and hands it to the access point within seconds. Until Vantigo first tries to hand it over, it can be cancelled. |
| **Submitted** | The access point has it and is delivering it. Nothing to do: Vantigo hears from the provider and checks with it, after five minutes, again after fifteen, then hourly. |
| **Delivered to the receiver's access point** | The receiver's access point confirmed it got the document. That is the strongest proof Peppol gives — not that someone has read or approved it. The document cannot be sent as EHF again. |
| **Failed** | It was not delivered: the access point refused it, the receiver has left the network, or it sat in the queue for two days without reaching the provider. Read the reason, fix what it names, and send again — **Send as EHF** is offered once more — or send by e-mail. |
| **Unconfirmed – needs a check with the provider** | Vantigo cannot tell whether it arrived: the provider took it but has not confirmed delivery in seven days, or the hand-over was cut off and the provider may or may not have it. A new send waits until a person resolves it. |
| **Cancelled** | Someone cancelled it before it was handed over. It can be sent again. |

**Cancel the transmission** is offered on a queued row to people with `invoices:issue`,
until Vantigo has tried to hand the document to the access point; then the button goes,
and the outcome decides. If the hand-over starts while you click, the answer is *The
transmission may already have reached the access point, so it can no longer be
cancelled* — wait for its outcome instead.

**Resolve** is offered on an unconfirmed row. First check with the provider: look the
submission up in Storecove by the provider reference on the card, or by when it was
queued — or ask whoever in your company holds the Storecove account — and see whether it
was delivered to the receiver's access point or failed. If the provider cannot tell, ask
the customer whether they received the invoice. Then, in **Resolve the unconfirmed
transmission**, choose the **Outcome**, **Delivered** or **Failed**, write **What the
provider said** (1 to 500 characters) and click **Resolve**: *The transmission is
resolved*, and the row shows the note as *Resolved: …*. **Delivered** closes it.
**Failed** lets you send the document again, and that send carries the very same EHF, so
that if the first one did arrive after all, the customer has two copies of one document,
never two different ones. Vantigo also keeps asking the provider about an unconfirmed
transmission once a day for thirty days, and resolves it on its own — delivered, or
failed if Storecove's event says so — if the provider finally answers; the row's note
then says the provider resolved it, and a send after such a failure carries a fresh EHF.

**Download EHF (XML)** on each row downloads the EHF exactly as Vantigo stored it when
it was queued, named like the PDF with the transmission's number added —
`invoice-1001-1001.xml` or `faktura-1001-1001.xml` — to anyone with `invoices:access`. The
access point rebuilds the EHF it delivers from this file; the copy it actually delivered
is kept with its receipt in the installation's document store.

### When the customer expects EHF but you e-mail

E-mail stays possible for every document, and neither channel stops the other. When the
customer's billing profile prefers EHF and the document can be sent as EHF, the dialog
**Send by e-mail** shows the red **The customer expects EHF**: *This customer expects
EHF, and this document can be sent as EHF. An e-mailed PDF does not meet the
e-invoicing duty.* Close it and use **Send as EHF** instead, unless you have agreed
otherwise with the customer. When the document is already on its way as EHF, or
delivered, the e-mail dialog notes *This document is already on its way as EHF, or
delivered*; an e-mail then sends the customer a second copy.

## Checking the journal

**Invoice journal** in the sidebar is the proof the bookkeeping regulation asks for:
every issued document in number order, and the check that the series has no gaps. It
needs `invoices:access`. The range **Issued from** / **Issued to** starts as the current
month; change either date.

At the top, the check in words: *No documents were issued in this range, so there is
nothing to check*, or *No gaps between 1001 and 1042*, or the red **The number series is
broken** with the *Missing numbers*. A second red alert, **The counter and the documents
disagree**, means a number was taken without a document. Neither happens in normal use —
a refused issue gives its number back and an issued document is never deleted — so
either means the data was changed outside Vantigo: find the cause and document it
([the journal](/en/reference/invoices/#the-journal)).

Below it, **Totals per VAT code** over the whole range (**SAF-T code**, **Category**,
**Rate %**, **Basis**, **VAT**) with *Net · VAT · Total*, and the documents a page at a
time: **Number**, **Kind** (a credit note reads *Credit note for 1001*), **Issue date**,
**Customer**, **Total excluding VAT**, **VAT** and **Total**. A credit note is shown
negative, so the totals are the period's net sales.

## Exporting the period for the accountant

On the journal, **Export CSV** downloads the range shown as `invoices-<from>-<to>.csv`.
The file has one row per document and VAT rate — a credit note's amounts negative —
with fixed English columns: Number, Kind, Issue date, Delivery, Due, Customer number,
Buyer, Buyer org no, Currency, SAF-T code, Rate, Base, VAT, Base NOK, VAT NOK, Credits
number and KID — import the KID column as text, or the spreadsheet drops its leading
zeros. It opens in a spreadsheet as Norwegian systems expect: `;` between cells, the
decimal comma, UTF-8 ([the CSV export](/en/reference/invoices/#the-csv-export)).

A range of more than 5000 rows is refused — *The export would hold more than 5000 rows;
narrow the period* — before anything is written, never cut short.

## The dashboard card

**Home** shows an **Invoices** card to everyone with `invoices:access`. Its figure is
**Outstanding**: the open amount, right now, of every invoice that is open, partially
paid or overdue. Under it, *N overdue (amount)* appears only while something is overdue,
and the percentage *issued vs previous period* compares the total invoiced in the
dashboard's selected period (7, 30 or 90 days, 12 months or a custom range) with the
period of the same length before it ([the stats](/en/reference/invoices/#stats)). The
card has no chart; clicking it opens the list.

## A customer's invoices

A customer's page in the Customers app has an **Invoices** tab (shown when the Invoices
module is on and you hold `invoices:access`): the same list, filtered to that customer,
with each document's state and open amount. Its **New invoice** button makes the draft
for that customer — *delivered today until you change it* — and opens the editor; it is
offered with `invoices:create` and `customers:view`, and only on an active customer,
never an archived, disabled, merged or anonymised one.

## Retention and anonymised customers

Nothing issued is ever deleted, by anyone: an issued document, its payments, its
delivery log and its EHF transmissions are bookkeeping material kept five years after the
end of the financial year, and the PDFs and EHF files live in the installation's document
store, whose backups are part of that retention ([retention](/en/reference/invoices/#retention-and-personal-data)). When
two customers are merged, their documents follow the surviving customer but keep the
buyer printed on them. When a person is anonymised in Customers, their drafts are
deleted, the recipient of every send is blanked — the **To** column then reads
*(anonymised)* — the notes on their payments are emptied, and an EHF still waiting in
the queue that Vantigo never tried to hand to the access point is cancelled; the issued documents, with the
buyer they name, stay. No document is sent to an anonymised customer again, though a
credit note can still be issued, naming the buyer the original named.

## Permissions

No built-in role holds these; an Owner holds everything
([permissions](/en/reference/invoices/#permissions)).

| You want to | You need |
| --- | --- |
| Open the app, read every document, download PDFs and EHF files, see payments, sends and EHF states, read the journal, export the CSV, see the dashboard card | `invoices:access` |
| Create, edit, preview and delete drafts | `invoices:create`, and `customers:view` to pick the buyer |
| Issue a draft, make a credit note, send a document by e-mail or as EHF, see where each send went, cancel or resolve an EHF transmission | `invoices:issue` |
| Register a payment or remove one with a reason | `invoices:payments` |
| Edit the seller record, the number series, the Peppol id, the access point, the KID agreement and the VAT codes | `invoices:manage` |
