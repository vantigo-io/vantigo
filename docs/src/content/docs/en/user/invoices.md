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
journal** and **Invoice settings**. Every amount is in NOK in this phase, and an
invoice is handed over as a PDF — by download or by e-mail — not as an EHF e-invoice
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

Saving keeps two things the page has no fields for yet: the seller's **Peppol id** —
the address EHF invoices are sent from, filled in as `0192:` and the organisation number
when you have one, and following the number when you change it — and the **KID agreement** with your bank, its length and its check
digit (MOD10 or MOD11). Until they get their own fields, they are set through the API
([the Peppol id and the KID agreement](/en/reference/invoices/#the-peppol-id-and-the-kid-agreement)).
With a KID agreement every invoice issued from then on gets a KID, the payment
reference the bank matches the payment by; a credit note never does.
Sending as EHF is not in the app yet; the server already answers whether this
installation could — the operator's EHF switch and the Peppol lookup on, the access
point's credentials stored and the Peppol id set
([the switches](/en/reference/invoices/#permissions)). The access point — the provider
that carries an EHF invoice onto the network — is configured by an administrator through
the API for now; its screen comes later
([the access point's credentials](/en/reference/invoices/#the-access-points-credentials)).

If a colleague saved the settings while you were editing, the form says **The settings
changed** and offers **Reload**; your unsaved edits are dropped, never merged.

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
**State**, the **Customer**, the **Issue date**, the **Due date**, the **Total** and —
for an issued invoice — what is still **Open**. Click a row's number to open it.

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
  0 to 365. The due date is the issue date plus the terms, fixed at issue.
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

## Downloading the PDF

An issued document's page offers **Download PDF**. The file is named after the document
and the buyer's language — `faktura-1001.pdf` or `invoice-1001.pdf`, `kreditnota-1002.pdf`
or `credit-note-1002.pdf` — and is the one PDF rendered and stored at issue, served
exactly as stored every time ([the PDF](/en/reference/invoices/#the-pdf)). Anyone with
`invoices:access` can download it.

An invoice with a KID shows it in the payment block, as **KID**, and asks the buyer to
pay with it instead of the invoice number.

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
duty. The rest are notes: the buyer is a Norwegian business (before 2027), or the
customer prefers eFaktura or paper. None of them stops the send. On an invoice that is
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

The app gets its **Send as EHF** button in a later release; until then the server's API
carries it, for people with `invoices:issue` on an installation that can send as EHF.
Sending as EHF checks the issued document, makes its EHF — the e-invoice, with the PDF
inside — asks the Peppol network whether the customer accepts it, and puts it in a queue
the server works through; the document then shows whether it is queued, submitted,
delivered, failed or awaiting confirmation. It is refused when the document was issued
to a customer without a Peppol id, when it has neither the customer's reference nor an
order reference, when it is already on its way or delivered, when its EHF breaks a
Peppol rule, when the customer is not on the Peppol network or does not take this kind
of document there, or when the network cannot be asked. A transmission still waiting in
the queue can be cancelled, and one the provider never confirmed is resolved by a person
after checking with the provider. Because the references cannot change after issuing, a
draft for a customer who is invoiced by EHF warns while it has neither — *This customer
is invoiced by EHF, which needs your customer's reference or an order reference* — so
add one before you issue
([the rules](/en/reference/invoices/#sending-as-ehf)).

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

Nothing issued is ever deleted, by anyone: an issued document, its payments and its
delivery log are bookkeeping material kept five years after the end of the financial
year, and the PDFs live in the installation's document store, whose backups are part of
that retention ([retention](/en/reference/invoices/#retention-and-personal-data)). When
two customers are merged, their documents follow the surviving customer but keep the
buyer printed on them. When a person is anonymised in Customers, their drafts are
deleted, the recipient of every send is blanked — the **To** column then reads
*(anonymised)* — and the notes on their payments are emptied; the issued documents, with
the buyer they name, stay. No document is sent to an anonymised customer again, though a
credit note can still be issued, naming the buyer the original named.

## Permissions

No built-in role holds these; an Owner holds everything
([permissions](/en/reference/invoices/#permissions)).

| You want to | You need |
| --- | --- |
| Open the app, read every document, download PDFs, see payments and sends, read the journal, export the CSV, see the dashboard card | `invoices:access` |
| Create, edit, preview and delete drafts | `invoices:create`, and `customers:view` to pick the buyer |
| Issue a draft, make a credit note, send a document, see where each send went | `invoices:issue` |
| Register a payment or remove one with a reason | `invoices:payments` |
| Edit the seller record, the number series and the VAT codes | `invoices:manage` |
