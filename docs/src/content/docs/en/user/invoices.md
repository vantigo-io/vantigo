---
title: Invoices
description: Invoicing work, drafting, issuing and sending invoices, credit notes, payments, the journal and the export.
sidebar:
  order: 50
sources:
  - apps/invoices/frontend
  - apps/host/frontend/src/routes/customers/-customer-invoices-tab.tsx
  - apps/host/frontend/src/lib/invoice-access.ts
  - apps/host/frontend/src/routes/invoices
---

The Invoices app issues the sales documents of your bookkeeping: a draft becomes a
numbered invoice or credit note the moment it is issued, gets a PDF, and from then on
never changes. The app has four areas in its sidebar: **Invoices**, **Invoice
journal**, **Payments** — the bank's files and the exception queue, shown only with
`invoices:payments` — and **Invoice settings**. Every amount is in NOK in this phase. An invoice is
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

### Work to invoice

The card **Work to invoice** on the same page — *The VAT code each kind of work is
invoiced at, and whether new invoices carry a timesheet* — holds what a draft made from
work starts with ([Invoicing work](#invoicing-work)). It is saved with the page's
**Save**.

- **VAT code for hours**, **VAT code for expenses** and **VAT code for milestones**: the
  code each kind of work's lines get. All three are code 3 (25 %) until you change them.
  The lists offer the codes offered for new lines, and the one saved when it has since
  been taken out of the list, marked *(no longer offered)*; choose another before you
  invoice work of that kind. While the seller is not registered for VAT, every kind of
  work is invoiced at code 7 instead, whatever the card says.
- **Attach a timesheet to new invoices**: whether a new invoice draft carries a
  timesheet in its PDF unless you choose otherwise. Off until you turn it on.
- **How the timesheet names each person**: **Initials (KN)** — the default; a second
  "KN" becomes "KN2" — **Person 1, Person 2**, numbered in the order they appear on the
  timesheet, or **Full name**. *A timesheet shows the customer your employees' work.
  Initials say the least; as their employer, tell them.* The label is applied when a
  timesheet's rows are written, and an issued invoice's timesheet never changes
  ([the timesheet](/en/reference/invoices/#the-timesheet)).

## Finding a document

**Invoices** in the sidebar lists every draft and issued document: drafts first, then by
number, newest first. Each row shows the **Number** (or *Draft*), the **Kind**, the
**State**, the **Customer**, the **Issue date**, the **Due date**, the **Total**, what
is still **Open** on an issued invoice, under **EHF** where an issued document
stands as an e-invoice: *Not sent*, *Queued*, *Submitted*, *Delivered*, *Failed*,
*Unconfirmed* or *Cancelled* ([what each means](#following-it-on-the-e-invoice-card)) —
a draft's is blank — and under **Project** the code of the project the document's work
belongs to, when all of it belongs to one. Click a row's number to open it, or a
project's code to list only that project's documents: the list then says *Project
P-41*, and the cross beside it (*Clear the project filter*) shows them all again.

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

To invoice hours, expenses or milestones, start from the work instead: the draft is made
with its lines ([Invoicing work](#invoicing-work)).

## Invoicing work

The hours approved in Time, the expenses ready to invoice in Expenses and the billing
milestones ready to invoice in Projects become invoice lines here, without retyping:
you choose the work, Vantigo writes the lines, and the issue marks that work invoiced in
the app it came from ([how it works](/en/reference/invoices/#invoicing-work)).

### Where the uninvoiced work is

- On a customer's page, the **Invoices** tab starts with the card **Uninvoiced work** —
  *Approved hours, billable expenses and ready milestones not yet on an invoice. Choose
  what to invoice.* — listing the work of every project billed to that customer, above
  the customer's documents.
- On a project's page, the **Invoicing** tab — the last one, after **Time** and
  **Expenses** — shows the same card for that one project. A project that bills no
  customer says *This project bills no customer, so its work cannot be invoiced from
  here.* On the **Economy** tab, the invoice plan links to it: **Invoice the work**.

Both need `invoices:access` and `invoices:create`, with the Invoices module on. When no
module that records work — Time, Expenses or Projects — is switched on, the customer's
card is not shown at all, and the project's tab says *No module that records billable
work — Time, Expenses or Projects — is switched on.* When nothing waits, the card says
*Nothing to invoice*.

### Reading the card

The work is grouped per project — a heading such as *P-41 · Apollo*, with the badge
**Fixed price** or **Not billable** when the project is one — and within a project in up
to three tables:

- **Hours**: **Date**, **Person**, **Work type**, **Hours** with the rate they are billed
  at (*4 h at NOK 1,200.00*) and **Amount**. A work type with a multiplier is billed at
  its own rate.
- **Expenses**: **Date**, **Kind** — **Outlay**, **Mileage** or **Supplier invoice** —
  **Description** (a supplier invoice's starts with the supplier and its invoice number),
  **Distance** for mileage, and **Amount**, the price for the customer with any markup.
- **Milestones**: **Date**, the day it became ready to invoice, **Milestone**,
  **Description** and **Amount**.

A row that cannot be chosen is greyed, and **Why not** says why:

| Why not | What it means |
| --- | --- |
| *Fixed-price project: the hours are shown, not invoiced* | A fixed-price project invoices its milestones; its hours are there to compare against the plan. |
| *The project is not billable* | The project bills nobody — it may have changed since the work was approved. |
| *Not in NOK* | Vantigo invoices in NOK only. |
| *The project bills no customer* | On a project's tab: the project has no customer to invoice. |
| **On draft 12** or **On invoice 1042** | A draft already holds it, or an invoice has invoiced it. The link opens that document. |

Below the tables, *Ready to invoice: NOK 48,500.00* is what can be chosen, per currency.
The card also warns, without stopping anything:

- on a project, *Some of this project's work was delivered more than a month ago; the
  law asks for the invoice within a month of delivery*;
- on a supplier invoice, *A supplier invoice with this supplier and number is already
  invoiced, or appears twice here. Check it is not billed twice*;
- at the top, *Not all the work is listed: there is more than one read can show. Invoice
  some of it, or list up to an earlier date, to see the rest.*

### Make a draft of it

Tick the rows to invoice, or the box at the head of a table to choose all of its rows.
The line beside the button counts what you chose —
*3 chosen: NOK 14,500.00*. Click **Invoice the chosen work**. On a customer's tab the
button is offered only for an active customer, as **New invoice** is: never for one that
is archived, blocked for invoicing, merged or anonymised.

The dialog **Invoice the work** repeats what is chosen and asks:

- **Lines** — how the work is grouped into lines, each choice with the number of lines it
  would make (*— 3 lines*): **One line per project** (the default), **Per work type**,
  **Per person**, **Per day** or **Itemised**, one line per entry. *Hours at different
  rates are always separate lines, and every milestone is a line of its own*; expenses are
  one line per kind of expense, except itemised. The lines are written in the customer's
  language — "Konsulenttimer, Apollo, september 2026" or "Consulting hours, Apollo,
  September 2026" — and can be edited afterwards like any line
  ([the line texts](/en/reference/invoices/#from-work-to-a-draft)).
- **Attach a timesheet to the PDF** — shown when hours are chosen: *One row per hour
  entry: the date, the person, the work type and the hours — never the entry's note.* It
  starts as the settings say for a new draft, and as the draft has it when you add to one
  ([the timesheet](#the-timesheet)).
- **VAT code for hours**, **VAT code for expenses**, **VAT code for milestones** — one for
  each kind chosen, starting at the codes on the card **Work to invoice** in the
  settings, or code 7 for every kind while the seller is not registered for VAT. A
  re-billed expense takes the code chosen here, never the VAT on its receipt. Vantigo has
  no "utlegg": a cost passed on to the customer is a sale like any other.
- **Delivered from** and **Delivered to** — *Left empty, the delivery runs from the work's
  first day to its last.*
- **Note** — printed on the invoice. Left empty, when some of the work was given back by a
  credit note before, the draft gets a note naming what it replaces — *Erstatter faktura
  1001, kreditert med kreditnota 1002* or *Replaces invoice 1001, credited by credit note
  1002*, in the customer's language — which you can change on the draft.
- **Put the work on** — **A new draft**, or one of the customer's invoice drafts, listed
  as *Draft 12 — NOK 9,000.00*. When the customer has more than 100 drafts, the field
  says *Only 100 of the customer's drafts are listed; the others are not offered here.*

Click **Create draft**, or **Add to draft 12**. The draft opens. A new draft takes the
customer's reference and payment terms as **New invoice** does. Added to a draft, the
work comes after the lines the draft already has; the draft keeps its own header and
note, its delivery period stretches to cover the new work unless you gave one, and the
timesheet is as the box said.

### When the wizard refuses

Nothing is made, and the dialog says *Could not invoice the work* and why:

- *Some of this work is already on another draft or an issued invoice*, with a link to
  that document — someone took it meanwhile.
- *Some of the chosen work changed since it was listed. Read the work again and choose.*
- *Some of the chosen work can no longer be invoiced: it was unapproved, invoiced
  elsewhere, or its module is off. Read the work again.*
- *Some of the chosen work is on a fixed-price or non-billable project, so it cannot be
  invoiced.*
- *Some of the chosen work belongs to a project that does not bill this customer.*
- *The chosen work is in more than one currency, and one invoice is in one currency*, or
  *The chosen work is not in NOK, the only currency this module invoices in.*
- *Work is invoiced per project, and the Projects module is switched off.*
- *One document holds at most 5 000 pieces of work. Choose fewer, or make another draft.*
- *Grouped this way the work makes more than 500 lines. Choose a coarser grouping* — and
  the dialog switches to the finest grouping that fits, saying, for instance, *“Per day”
  fits in 500 lines, so the lines are now grouped that way.*
- The customer is merged, archived, blocked for invoicing or gone, as for **New
  invoice**; or the draft you were adding to was saved by someone else meanwhile.

When the refusal is about the work, the alert names it — *The work refused: …* — and
the list is read again, so work that is no longer there to invoice drops out of what you
chose. Check the choice and click the button once more.

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

### The work on a draft

A draft made from work keeps that work on its lines: under each line the editor lists
what it bills — *Hour entry 4211 · 3 Sep 2026 · 7.5 · NOK 9,000.00*, *Expense …* or
*Milestone …* — and, on an issued invoice, whether each is *Invoiced* or *Released*. While
the draft holds the work, no other draft can take it. When all the work belongs to one
project, the draft says *Project: P-41* under the customer: the code is printed on the
PDF as *Prosjekt* / *Project*, carried in the EHF, kept by the issued invoice and its
credit notes, shown in the list's **Project** column, and the last column of the
accountant's export ([the project](/en/reference/invoices/#the-project)).

- **Edit a line's text, quantity or price** as on any draft: the work stays with the
  line. A line whose amount no longer matches its work says so in orange under the line
  and under **Worth a look** — *A line's amount differs from the work it bills* — which is
  allowed: a write-down, a rounding.
- **Remove a line** to give its work back: after **Save**, the work is uninvoiced again,
  and **Worth a look** says *The save released work from this draft* and *Released:*
  with the work named. Deleting the draft gives all its work back.
- **Change the customer** only knowing that it gives all the work back: the editor says
  *Changing the customer releases the work this draft holds: it becomes uninvoiced again
  when you save.*
- **Refresh work**, above the lines, reads the work again from Time, Expenses and
  Projects: new figures are taken, and work that can no longer be invoiced is dropped
  and named as released. It is greyed while you have unsaved changes (*Save the changes
  before refreshing the work*); when it is done, *The work is refreshed*. If someone else
  saved the draft in between, the refresh is refused (*The invoice changed; try again*):
  try again.
- When work has changed since it was added, or can no longer be invoiced — an entry
  unapproved, a milestone moved back — the line and **Worth a look** say so: *Work on this
  draft has changed since it was added; refresh the work, or the issue will refuse it*,
  or *Work on this draft can no longer be invoiced; refresh the work to drop it, or the
  issue will refuse it.*

Work arrives on a draft only from the card **Uninvoiced work**, never by editing a line.

### The timesheet

Every invoice draft has the card **Timesheet**, with the box **Attach a timesheet to the
PDF**. Ticked, the invoice's PDF carries, after the invoice itself and on pages of its
own, every hour entry the draft bills — **Date**, **Person**, **Work type**,
**Description** (the task, or else the project's name, or else the work type) and
**Hours** — with a total per person and one in all. It never shows the note a person
wrote on an entry. The rows are written when the draft is saved, and the card lists them
then; until you save it says *The timesheet's rows are written when the draft is saved*,
and on a draft with no hours, *The draft holds no hours for the timesheet*. Each save keeps
the timesheet to the hours the draft still bills, **Refresh work** writes it again, and
unticking the box removes it. **Preview** shows it; once the invoice is issued, the
timesheet is part of it and never changes, whatever later happens to a user. A credit
note has no timesheet.

A timesheet tells the customer who worked on what. Telling your employees that their
hours are shown to customers is the employer's job — the privacy notice of GDPR art. 13 —
which is why initials are the default and the full name is a choice made on the card
**Work to invoice** ([the timesheet](/en/reference/invoices/#the-timesheet)).

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

**Crediting an invoice made from work** gives the work back — the hours, expenses and
milestones a line billed become uninvoiced again, ready for a new invoice — only when the
credit note returns that line **in full**: its whole quantity, at the invoice's own unit
price and discount, counting the credit notes issued before it. A line credited in part,
or at a lower price, keeps its work invoiced until the rest of the line is returned; a
milestone comes back whole or not at all. The credit-note draft's card **Work this credit
note gives back** lists, as the draft stands, the work its issue would give back — *When
it is issued, this work becomes uninvoiced again and can be invoiced anew* — or says *As
it stands, issuing it gives no work back: only a line credited in full, at its own price,
releases its work.* Once it is issued, the invoice's lines show that work as *Released*,
and it is back on the card **Uninvoiced work**. A new invoice may bill the released work
again; it does not have to name the credit note, and the wizard suggests a note saying
what it replaces ([release on credit](/en/reference/invoices/#release-on-credit)).

## Final settlement

A large job is often invoiced **on account** (*a konto*) as it goes — an ordinary invoice
for each instalment, typically a milestone — and closed with a **final settlement**: an
invoice for the whole that deducts what the a-konto invoices already billed. A settlement
is an ordinary draft with one **deduction line** per earlier invoice and VAT code:

- the line names the earlier invoice it deducts, an issued invoice of the same customer
  in the same currency — never a draft, a credit note or another customer's;
- its **Quantity** is **-1** and its **Unit price** the amount deducted, above 0, with
  no discount; its amount prints with a minus, "-125 000,00", and lowers the total;
- its **VAT code** is one the earlier invoice has a line at, and it is taxed at the rate
  that invoice was issued with — so a rate change since, or a code no longer offered,
  does not change it. The text proposed is *Tidligere fakturert a konto, faktura 985* /
  *Previously invoiced on account, invoice 985*. A settlement can itself be deducted
  later, but not at a VAT code where it deducts earlier invoices.

To add them, click **Deduct earlier invoices** above the lines of an invoice draft. The
dialog lists, per VAT code, what each of the customer's issued invoices has left to
deduct — **Invoice**, **Issue date**, **VAT code**, **VAT %** and **Left to deduct** —
*A deduction is taxed at the earlier invoice's rate.* Tick the rows to deduct and give
each an amount under **Deduct**: more than 0 and at most what is left, which is also what
it starts at. A pair the draft already deducts says *Already on this draft*. Click **Add
the deduction lines**: each becomes a line *Previously invoiced on account, invoice 985*
or *Tidligere fakturert a konto, faktura 985* — in the customer's language from their
billing profile, else yours —
quantity -1 at the amount, with a link **Deducts an earlier invoice** under it. The
quantity, the discount and the VAT code of a deduction line cannot be edited; its price
and text can. **Save** the draft to keep them. When no earlier invoice has anything left,
the dialog says *No earlier invoice of this customer has anything left to deduct.*

A deduction may take no more than the earlier invoice has **left** at its VAT code: what
it billed there, less what credit notes gave back of it and what earlier settlements
deducted. A draft past that says so under **Worth a look** (*A deduction takes more than
the earlier invoice has left at its VAT code*), and the issue is refused naming the line.
A settlement must come to **more than zero**: one that deducts as much as it bills, or
more, is warned about and cannot be issued — a fixed price billed in full on account ends
with its last a-konto invoice. Two deduction lines for the same invoice and VAT code are
refused when you save; keep one.

The PDF lists the invoices deducted under the references (*Fratrukket a konto: Faktura
985 av 01.08.2026*), and the EHF names each of them as a preceding invoice. **Crediting a
settlement** reverses its deductions too — the credit note copies them as minus lines —
and gives the earlier invoices back what was deducted; a credit note of the deductions
alone, below zero, is refused. **Crediting an a-konto invoice that a settlement
deducted** is refused beyond what the settlement left of it: credit the settlement first
([a-konto and the final settlement](/en/reference/invoices/#a-konto-and-the-final-settlement)).

## Registering payments

Money received is registered by hand, on the issued invoice's page under **Payments**.
Each registration needs `invoices:payments`. Vantigo can also take in the bank's own
files of incoming payments — OCR giro and camt.054, imported by someone with
`invoices:payments` ([bank files](/en/reference/invoices/#bank-files-and-the-exception-queue);
the bank agreement and the download are on [Payments from the bank](/en/admin/payments/)).
An imported file's payments are matched to your invoices on their KID: a payment that
carries the KID of an issued invoice, paid to the account that invoice printed, is
registered against it — the open amount first, any reminder charges with the rest — paid
on the day the bank booked it, with the KID as its reference and the person who imported
the file as the one who registered it. Every payment records where it came from:
registered by hand, or taken from a line of an OCR giro or camt.054 file. A payment
matching cannot place — no KID, a KID no invoice carries, more than is left to pay, or
one that may repeat a payment already registered — is kept for a person and not
registered. Someone with `invoices:payments` deals with it in the exception queue on the
**Payments** page ([importing payments from the bank](#importing-payments-from-the-bank),
[the exception queue](#the-exception-queue)): apply it to one or more invoices and their reminder charges, with suggestions — an invoice whose number is in
the payment's text, one whose open amount it equals, one of the customer who paid from the
same account before; dismiss it as not a customer payment, with a note; for a reversal,
remove the payment the bank took back; confirm a duplicate or keep it as a payment of its
own; or reopen it. What a payment leaves unapplied stays visible on its line — Vantigo
keeps no credit balance and makes no refund. A payment dismissed because its invoice was
credited or already paid, or because it was more than was owed, stays unapplied too: the
money is owed back, and the refund is made outside Vantigo. Once a reversal has taken a
payment back, the line it came from is never applied or reopened again.
Nor is the same payment in another file of the bank's, or a copy of the line: those are
held back for a person, never registered by themselves. A reversal that named the wrong
payment cannot be undone; register that payment again by hand.

Click **Register payment** — offered while something is left to pay — and fill in:

- **Paid on**: the day the money arrived, prefilled with today; on or after the issue
  date and not after today.
- **Amount**: prefilled with the open amount; more than 0 and at most the open amount.
  An overpayment is refused, with the open amount named.
- **Reference** (the bank's or the payer's) and **Note**, both optional.

Click **Register**. The table lists each payment's **Paid on**, **Amount**, its
**Source** — **By hand**, **OCR giro** or **camt.054**, a bank's payment naming its
**Bank line**, linked to **Payments** for someone with `invoices:payments` — its
**Reference**, **Note** and when it was **Registered**, and the totals gain **Paid** and
**Open** — the total less what is credited and paid. A payment of exactly the open
amount makes the invoice **Paid**; a smaller one makes it **Partially paid**, or leaves
it **Overdue** when the due date has passed. A credit note takes no payment
([payments and the state](/en/reference/invoices/#payments-and-the-state-of-an-invoice)).

A wrong registration is never edited: click **Remove** beside it, give a **Reason** in
the dialog **Remove the payment**, and confirm. *The payment stays on the invoice,
struck through with the reason. A removal cannot be undone; register the payment again
if it was right after all.*

## Importing payments from the bank

Open **Payments** in the sidebar. It is shown only to someone with `invoices:payments`
(a reader with `invoices:access` alone does not see it), and it has four parts:
**Import a bank file**, **Bank accounts**, **Imported files** and **The exception
queue**.

**Where the files come from.** The bank gives you OCR giro files under an OCR/KID
agreement, or camt.054 notifications of incoming payments; download them from the online
bank ([Payments from the bank](/en/admin/payments/) says which agreement and where). Never
edit a file by hand.

**Import a file.** Under **Import a bank file**, choose the file in **Bank file** and click
**Import**. The whole file is checked first, and nothing of it is imported if any of it is
wrong. The page then says why, under *The bank file was not imported*: the file is not an
OCR giro or camt.054 file, is too large or breaks its own rules (with where in the file);
an account it names is neither your **Bank account** nor one an issued invoice printed;
the account's files are imported in the other format; or there is no document store to
keep the file in. A file imported before is refused too, naming the earlier import — its
file number, when it was uploaded, and whether by you or another user — with a link, **Open file**, to it.

**What the result says.** After an import, **The import's result** counts the file's
**Payments in the file**: those **Matched to invoices**, with the amount registered as
payments; those **In the exception queue**, with the amount waiting there; those
**Already imported, kept as duplicates** — payments an earlier or overlapping file had
brought; those **Not yet matched**; and those **Ignored**, by kind — card information in
an OCR file, debits that are not reversals, entries not booked, amounts of zero. Each
matched payment is registered against its invoice — the open amount first, any reminder
charges with the rest — on the day the bank booked it, with the KID as its reference and
you as the one who registered it. **Open the file** shows the file's own page, with every
payment it brought.

**Match the rest.** Matching runs right after the import, one payment at a time. If it
stops early, the file stays imported and some of its payments are **Not yet matched**:
click **Match the rest** — in the result, on the file's row under **Imported files**, or
on the file's page — and you are the one registering them. The result then shows what
that matching registered and queued.

**Imported files** lists every file, newest first: its format, when it was uploaded and
whether by you or another user, the booking days it covers, and its payments counted — matched, in the queue,
duplicates and not yet matched.

**The format of each account.** **Bank accounts** lists every account a file was imported
for: its format — **OCR giro** or **camt.054**, set by the account's first file — its latest
file and latest booking day, and, after a change, the earlier format with its cutover. A
file of the other format for an account is refused. To switch an account — say from OCR
giro to camt.054 — someone with `invoices:manage` clicks **Change the format** beside it,
chooses the **New format** and clicks **Change the format**. The dialog explains the
**cutover** first: Vantigo records the latest booking day of the account's payments in the
old format, and holds back a payment of the new format booked on or before that day as a
possible duplicate, since the old format may already have brought it in. Make the change
once the last file in the old format is imported. Without `invoices:manage` the formats
are shown, and the page says that changing one needs it.

**A genuine second payment that looks like a repeat.** A payment is held back as **A
possible duplicate**, and not registered, when a payment from **another file** to the
same account, booked on the same day, of the same amount and with the same KID is
already registered — because that is what one payment looks like in two of the bank's
files: an intraday and an end-of-day notification, or an OCR giro and a camt.054 file of
the same day. So a customer who really does pay the same amount twice on one day with
the same KID, the two payments arriving in different files, has the second one queued.
Check the account statement; if both payments are real, find the line in the exception
queue and **Apply** it — to the invoice, its reminder charges or another invoice — as you
would any other payment. Two such payments in the same file are both registered.

## The exception queue

**The exception queue**, at the foot of **Payments**, holds the payments matching could
not place, and the duplicates — what waits for a person. It opens on **In the queue**;
the duplicates are under the status **Duplicate**. Filter it by **Status**, **Reason**
and **File** (type to find a file; it offers the latest 500 files and says so when there
are more), or tick **Only payments with an
unapplied rest** to see the payments applied in part, matched and since removed, or
dismissed as money owed back. Each line shows the day the bank booked it, its KID or else
its text, the debtor and their account, the amount, what is applied — each payment linked
to its invoice, a removed one struck through — what is unapplied, and its state and reason.
**Details** shows what happened to the line, each step with when, and whether by you or
another user, the
invoices it may pay, and, for a possible duplicate, the line it may repeat: that line's
file, booking day and payments, and whether the bank reversed a payment of it.

Each reason in plain words, and what to do:

| Reason | What it means | What to do |
| --- | --- | --- |
| **The KID is not valid** | the KID's check digit is wrong | **Apply** it by hand, or dismiss it |
| **No invoice has this KID** | another system's or another agreement's KID | **Apply** it, or dismiss it |
| **The invoice is credited** | the KID's invoice is credited in full | **Not a customer payment**, with a note: the money is owed back, and refunded outside Vantigo |
| **Nothing is left to pay** | the invoice is paid, and the payment is more than its reminder charges | the same |
| **More than is owed** | more than the open amount and the charges | **Apply** what is owed to the invoice and its charges; the rest stays unapplied |
| **No KID** | the payment carries no KID | **Apply** it to one or more invoices from the suggestions |
| **A negative amount** | an OCR line with a minus sign | **Not a customer payment**, with a note |
| **A reversal** | the bank took a payment back | **Handle reversal** |
| **A Vipps payout** | a payout from Vipps, not a customer's payment | **Not a customer payment** |
| **Paid before the invoice was issued** | booked before the KID's invoice was issued | **Apply** it after checking, or dismiss it |
| **Paid into another account** | not into the account the invoice printed | **Apply** it after checking, or dismiss it |
| **A possible duplicate** | the account's cutover, or the same payment already registered from another file | **Confirm duplicate**, or **Apply** it as a payment of its own |
| **Its payment was removed** | a matched payment whose registrations were all removed, reopened | **Apply** it again, or dismiss it |
| status **Duplicate** | an earlier or overlapping file brought the same line | **Confirm duplicate**, or **Treat as distinct** |

**Apply.** The dialog **Apply line …** opens with the invoices the payment may pay — the
one its KID named, and the suggestions: an invoice whose number is in the payment's text,
one whose open amount is the payment's amount, an open invoice of a customer who paid from
the same account before — filled in order, each with up to its open amount, until the
payment runs out. Add another with **Add an invoice by its number**, or remove one. For
each invoice enter the **Principal** and the **Charges** — the reminder charges — it pays;
a payment can pay the charges alone of an invoice that is already paid. The total below
says how much is applied and how much stays unapplied, and allocations that add up to more
than is left of the payment are refused in the dialog before anything is sent. Click
**Apply**. A refusal is said in words and nothing is applied: an amount over an invoice's
open amount names the invoice and that amount, a charge payment over the charges
outstanding names what is outstanding, and an invoice issued after the bank booked the
payment cannot be paid by it. What is not applied stays visible on the line: Vantigo keeps
no credit balance and makes no refund.

**Not a customer payment.** For a Vipps payout, a payment refunded outside Vantigo or
another system's KID: say what it is, or what was done about it, in the **Note**, and
click **Dismiss**.

**Handle reversal.** The dialog offers the payments the reversal may take back — those
registered from bank lines of the same account and amount, booked on or before it; it
reads the latest 500 such lines of each kind and says so when there are more — an older
payment is then not offered — and says so when they could not be read, rather than that
there are none. Tick
the one it reverses, or choose **No payment is removed; the note says why** and write the
note, then click **Handle the reversal**. Each chosen payment is removed with the reason
"Reversed by the bank", and the line it came from is never applied again. Without a
payment or a note the reversal is refused, in words. A reversal that names the wrong
payment cannot be undone: register that payment again by hand.

**Duplicates.** **Confirm duplicate**, with an optional note, keeps the line and registers
nothing. **Treat as distinct** makes a duplicate row a payment of its own, back in the
queue as a possible duplicate, ready to **Apply**; it is refused when the bank reversed a
payment of the line it repeats, since that money went back.

**Reopen.** A resolved line — or a matched one whose payments were all removed — goes back
to the queue with **Reopen**, with its reason. It is refused while a payment registered from
the line still stands (remove it on the invoice first); a line the bank reversed a payment
of is never offered it.

Every action is checked again as it is made: a line someone else dealt with meanwhile is
refused, in words, and nothing changes.

## Reminders

Reminders are on their way to Vantigo: letters for overdue invoices, with the fee, the
compensation and the late interest the law allows. Three things they rest on can already
be set — through the API for now; the screens come with the reminders themselves
([reminders](/en/reference/invoices/#reminders)):

- **Collection rates**: the statutory late interest rate, the business compensation and
  the inkassosats, as dated rows that Vantigo's releases fill in. Someone with
  `invoices:manage` can add a rate that takes effect after today — and after the date of
  the latest printed or sent letter — ahead of a release, and delete one that is not yet in force and that no letter has used.
- **Reminder settings**: whether reminders are offered at all, how long after the due
  date the first letter comes, each letter's deadline, the charges for people and for
  businesses, and the day the new inkasso law takes effect, with its review
  (`invoices:manage`).
- **A customer's reminder policy**: normal, no charges, or no reminders at all, with a
  note. Someone with `invoices:payments` sets it, for a customer with an invoice or a
  draft here. When two customers are merged the stricter policy wins.

**Reminder charges and deliveries recorded by hand.** An issued invoice now also carries
what its reminders claim — reminder fees, the compensation and late interest, kept apart
from what the invoice itself is for — with the payments and waivers of those charges,
and the deliveries recorded by hand that a charge needs when the invoice was handed over
or posted rather than e-mailed or sent as EHF. Such a record cannot be removed while a
reminder — sent, printed or on its way — claims a charge that rests on it alone. Their
screens come with reminders; until then they are in the API ([charges](/en/reference/invoices/#charges),
[the delivery fact](/en/reference/invoices/#the-delivery-fact)).

**The overdue list and reminder runs.** Vantigo now judges every overdue invoice: what
comes next — a reminder, a debt collection notice, a suggested hand-off to a collection
agency, or why it waits or is blocked — with the letter as it would go today, its fee,
compensation, interest and deadline, and whether the bank data is recent enough to trust.
Someone with `invoices:payments` previews a run and then makes it: the letters are
created, by e-mail to the customer's reminder address or on paper, and their figures are
fixed when they are sent. An invoice shows its letters and what comes next. The screens
come with the letters' sending; until then the list and the runs are in the API
([the overdue list](/en/reference/invoices/#the-overdue-list),
[runs](/en/reference/invoices/#runs)).

**Paid on the deadline.** A payment ordered on a letter's deadline is on time, and the
bank may book it days later. An OCR giro file says when a payment was ordered; a camt.054
file does not, so a payment from one is judged by the day it was booked. The default
grace of 3 days after a deadline, before the next letter, covers a payment ordered on the
deadline and booked after an ordinary long weekend; Easter can take longer, and the
confirmation a run asks for when the latest bank file is old is the guard then. With a
grace of 1 day such a payment could draw a second fee that is never waived: keep the
grace at 3 days or more when your bank files are camt.054.

**Previewing part of the list.** A preview can be narrowed to one customer or to invoices
due before a day, as the overdue list can — and must be, when more than 5 000 invoices are
overdue.

**Disputed invoices and the hand-off to a collection agency.** Someone with
`invoices:payments` can put an invoice the customer disputes on hold: no reminder goes
while it is held, the late interest keeps running, and payments are still registered.
When the hold is lifted, Vantigo asks whether the objection was obviously groundless; if
it was not, every reminder fee and compensation already claimed on the invoice is waived
and none is claimed on it again. An invoice handed to a collection agency is recorded the
same way — with the day, the agency and its case number — and Vantigo then sends it no
more letters; a payment you receive directly is still registered, and you tell the agency
about it. Vantigo refuses to record the hand-off of an invoice it has no delivery of by
the due date until you confirm it; if the invoice was in fact delivered, record the
delivery first. Holding or handing off an invoice withdraws its letters that have not gone
yet, but not a printed letter, which may already be in the post, nor one being e-mailed at
that moment: those are named so you can pull them. The collection file — one row per
invoice, the amount owed apart from the fees and interest, and what was waived in a column
of its own — is exported for the agency. The screens come with the invoice page's
reminder cards; until then this is in the API
([holds and the hand-off](/en/reference/invoices/#holds-and-the-hand-off-to-collection)).

**How a letter by e-mail is sent.** Vantigo sends the e-mailed letters itself, in the
background, one at a time. Each is judged again on the day it goes: its date, its deadline
— at least 14 days on — its fee and its interest are that day's, not the run's, and a
letter that would leave after midnight is judged again on the new day; an invoice
paid, put on hold or handed off meanwhile gets no letter, and the letter is withdrawn with
the reason. The letter is a PDF, attached to a short e-mail in the customer's language,
with replies going to your invoicing e-mail. A letter whose rates or regime review are
missing waits, and goes once they are in place. A letter the mail server keeps refusing
fails after 48 hours; it can then be sent again or withdrawn, and any letter not yet sent
can be withdrawn, with your reason — but not while it is being e-mailed. A printed or sent
letter's PDF can be downloaded. The screens come with the invoice page's reminder cards;
until then this is in the API ([letters](/en/reference/invoices/#letters),
[the worker](/en/reference/invoices/#the-worker)).

**Letters on paper.** A paper letter goes when it is posted. Someone with
`invoices:payments` prints the letters awaiting print for the day they will be posted —
today or up to a week ahead — and each is judged for that day: its date, its deadline and
its fee are the posting day's. A letter that cannot go that day is left out and named:
withdrawn meanwhile, waiting for a rate or the regime review, or no longer due — the
invoice paid, put on hold or handed off. The batch's letters come as one PDF, which can be
downloaded again. Once the batch is in the post, confirm it was posted that day, and its
letters are sent; a fee the day no longer supports — the invoice paid, put on hold or
handed off since printing — is waived. Posted on another day, the batch must be printed
again for the day it goes. A batch confirmed posted, or reprinted, while it is still being
printed keeps the letters already printed in it; the rest are named and wait for another
batch. The screens come with the Overdue area; until then this is in
the API ([paper and posting](/en/reference/invoices/#paper-and-posting)).

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
number, KID and Project — import the KID column as text, or the spreadsheet drops its
leading zeros. It opens in a spreadsheet as Norwegian systems expect: `;` between cells, the
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
never an archived, disabled, merged or anonymised one. Above the list, the card
**Uninvoiced work** shows the customer's work not yet invoiced to whoever holds
`invoices:create` as well ([Invoicing work](#invoicing-work)).

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
buyer they name and their timesheets, stay. No document is sent to an anonymised customer again, though a
credit note can still be issued, naming the buyer the original named.

## Permissions

No built-in role holds these; an Owner holds everything
([permissions](/en/reference/invoices/#permissions)).

| You want to | You need |
| --- | --- |
| Open the app, read every document, download PDFs and EHF files, see payments, sends and EHF states, read the journal, export the CSV, see the dashboard card, read the overdue list | `invoices:access` |
| Create, edit, preview and delete drafts | `invoices:create`, and `customers:view` to pick the buyer |
| See the uninvoiced work — its hours, people and rates — on a customer's Invoices tab or a project's Invoicing tab, make a draft of it or add it to one, refresh a draft's work, turn its timesheet on or off, deduct earlier invoices | `invoices:create` |
| Issue a draft — which marks its work invoiced in Time, Expenses and Projects, without asking for their permissions — make a credit note, send a document by e-mail or as EHF, see where each send went, cancel or resolve an EHF transmission | `invoices:issue` |
| Register a payment or remove one with a reason; import bank files, and use **Payments** and its exception queue; set a customer's reminder policy; preview and make reminder runs; put an invoice on hold and lift the hold, record a hand-off to a collection agency and withdraw it, export the collection file | `invoices:payments` |
| Edit the seller record, the number series, the Peppol id, the access point, the KID agreement, the VAT codes, the card **Work to invoice**, the collection rates and the reminder settings; change a bank account's file format under **Payments** | `invoices:manage` |
