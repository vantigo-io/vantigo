---
title: Customers
description: Customer cards, contacts, follow-ups and the customer 360 view.
sidebar:
  order: 10
sources:
  - apps/customers/frontend
---

The **Customers** app holds the companies and people you do business with, the
people you talk to at each of them, and what has happened between you. Its sidebar
has three areas: **Customers** (needs `customers:view`), **Contacts** (needs
`customers:contacts-view` or `customers:associations-view`) and **Follow-ups** (needs
`customers:timeline-view`). What each screen lets you change depends on further
permissions, named with the task they gate and gathered under [Permissions](#permissions).

## Finding a customer

**Customers** lists every customer in a table, with cards above it: **Total
customers**, **Active** and **New last 30 days**, and — with
`customers:legal-identity-view` — **Business**, **Private**, **Missing legal
identity** and **Countries**.

- **Search** matches the name, the customer number and the customer's own email and
  phone. With `customers:legal-identity-view` it also matches the legal name and
  organisation number; with both `customers:contacts-view` and
  `customers:associations-view` it also matches a linked contact's name and email.
  See [the list endpoint and search](/en/reference/customers/#the-list-endpoint-and-search).
- **Status** is **All open** by default: every active and disabled customer. Pick
  **Archived** to see archived customers; they are hidden otherwise.
- **Type** narrows to **Business** or **Private**; **Owner** to **Mine** or
  **Unassigned**; **Tag** to one tag; **Group** to one group or **No group**.
- Click the **Number**, **Name** or **Created** header to sort by it: once ascending,
  again descending, a third time to drop the sort.

Every filter and the sort live in the page address, so a filtered list can be
bookmarked or sent to a colleague. Click a row to open the customer; the pencil at
the end of the row opens the edit form directly. **No customers found.** means nothing
matched the filters.

## Creating a customer

Press **Create new customer** (`customers:create`; also a quick action in the search
spotlight). The form asks for:

- **Customer type**: **Business** or **Private**. It is not editable later in this
  form — see [Changing the type](#changing-the-type).
- **Name**. For a business, typing searches Brønnøysundregistrene by company name or
  organisation number; pick a hit and the customer gets that legal name and
  organisation number as its legal identity, attributed to Brønnøysundregistrene. Keep
  typing instead and the name is saved on its own, without an identity. The lookup
  needs `customers:lookup-view` and saving an identity `customers:legal-identity-manage`;
  the form says so under the field. For a private person it is the person's full name.
- **Email** and **Phone**, the customer's own; edited afterwards from the customer page.
- **Status**: **Active**, **Disabled** or **Archived**.

While you type, **Existing customers with a similar name** lists up to three customers
whose name contains what you typed — a nudge to look before creating a duplicate,
never a block.

If the organisation number already belongs to another customer, the save is refused
with **Legal identity already in use**, naming the customers that carry it (when you
hold `customers:view`). Open one of them instead, or press **Create anyway** when two
customers really do share an identity — two departments of one company, say. With
`customers:merge` the warning also points to **Merge…** as the way to bring two
records together. The rule is
[the duplicate-identity guard](/en/reference/customers/#the-duplicate-identity-guard).

## The customer page

The header shows the name, the legal identity as badges (legal name, organisation
number, country, type and source — or *Legal identity is not available to this
account* without `customers:legal-identity-view`), the status, the type and the
customer number. Beside them are the actions **Edit customer**, **Change type**,
**Merge…**, **Archive customer** or **Restore customer**, **Personal data** (private
persons only) and **Open in inbox**, each shown only with the permission it needs.

A row of tabs — **Overview**, **Energy**, **Projects** and **Invoices** — appears when
more than one is available to you. The three module tabs show that module's own panel
for this customer and need the module on and a permission in it
(`energy:metering-points-view`, `projects:access`, `invoices:access`).

**Overview** opens with the **Customer 360** panel: **Open projects**, **Unbilled
hours**, **Expenses ready to invoice** and **Last activity** — each tile present only
when its module is on and for you — plus a table of open projects (**Code**, **Name**,
**Status**, **Last work**) and **See all … open projects** when there are more. The
rule is [Customer 360](/en/reference/customers/#customer-360). Under it come the cards
of the next sections: **Relationship**, **Contact & addresses**, **Registry**,
**Billing**, **Contacts** and **Timeline**.

A banner at the top says when the customer is archived, merged into another, scheduled
for anonymisation or anonymised. A merged-away or anonymised customer is read-only, and
every editing action disappears from its page.

## Editing, archiving and restoring

**Edit customer** (`customers:update`) opens the creation form reduced to **Name** and
**Status**. **Disabled** keeps the customer in the lists but blocks it for invoicing —
see [Statuses](/en/reference/customers/#statuses). The duplicate-identity warning
applies here too, with **Save anyway** as the override.

If somebody else saved the customer while your form was open, the save is refused with
**Customer changed**: press **Reload** to load their version — your typed changes are
discarded — and save again. The same alert appears in every form that edits the
customer row (contact details, owner, group, billing).

**Archive customer** (`customers:delete`) asks *Archive {name}? It is kept, but hidden
from most lists until restored.* An archived customer is still reachable from other
modules and refused a new invoice. **Restore customer** (`customers:update`) sets it
active again without a confirmation.

## Changing the type

**Change type** (`customers:update`) turns a business into a private person or the
reverse, through a confirmation that spells out what it does: contacts, the timeline
and everything linked to the customer are kept; a legal identity is removed, since it
belongs to the old type; forms and figures treat the customer as the new type from
then on. Confirm with **Change to private** or **Change to business**. The rule is
[Customer type vs. legal identity type](/en/reference/customers/#customer-type-vs-legal-identity-type).

## Owner, group and tags

The **Relationship** card shows who owns the relationship, which group the customer is
in and how it is tagged. With `customers:update` each has a control:

- **Owner**: search for a colleague and pick them, or **Clear owner**.
- **Group**: one of the installation's groups, or **No group**. A group can carry a
  default payment term, which the Billing card shows the customer inheriting.
- **Tags**: pick existing tags, or type a new name and choose **Create "…"** to create
  and attach it in one go.

The vocabularies are managed from the customer list (`customers:update`). **Manage
tags** beside the Tag filter renames a tag, gives it a **Colour** or deletes it, which
removes it from every customer that carried it. **Manage groups** beside the Group
filter creates a group with a **Group name** and an optional **Default payment terms
(days)**, edits it, or deletes it — refused while any customer belongs to it. The rules are
[Owner and tags](/en/reference/customers/#owner-and-tags) and
[Groups](/en/reference/customers/#groups).

## Contact details and addresses

**Contact & addresses** shows the customer's own **Email**, **Phone** and **Website**;
**Edit contact details** (`customers:update`) changes them.

Addresses are listed below by type — **Invoice**, **Postal**, **Delivery**,
**Visiting** — and the primary one of each type carries a **Primary** badge. **Add
address** asks for **Address type**, an optional **Label**, **Address line 1** and
**2**, **Postal code**, **City**, **Region** and **Country**; a
Norwegian address needs a four-digit postal code and a city. Tick **Primary … address**
to make it the primary of its type. The first address of a type is primary whatever
you tick, and you cannot untick the only or primary one — press **Make primary** on
another row instead. **Delete address** asks first and cannot be undone.

When the Registry card has a record and the customer has no address of that type yet,
**Use the registry's business address** and **Use the registry's postal address**
open the form pre-filled from Brønnøysundregistrene, as a visiting or postal address;
nothing is saved without a click. See [Addresses](/en/reference/customers/#addresses).

## The registry card

For a business customer whose legal identity is a Norwegian organisation number, and
with `customers:legal-identity-view`, the **Registry** card shows what
Brønnøysundregistrene says: **Organisation form**, **Industry**, **Employees**, **VAT
registered**, **Founded**, contact details, **Parent organisation**, **Business
address** and **Postal address**, and when the record was fetched. Red badges flag a
company that is **Bankrupt**, **Under liquidation**, **Under compulsory liquidation**
or **Deleted**.

**Refresh** (`customers:legal-identity-manage`) reads the register again; any
difference is written to the timeline as a **Registry change** event, and the card
says how many changes to look for. If the register calls the company something else,
**The registry has another name** offers **Update legal name**. See
[Registry record](/en/reference/customers/#registry-record).

## Billing

The **Billing** card shows the billing profile: **Invoice email**, **Reminder email**,
**Payment terms**, **Currency**, **Default bill rate**, **Document language**, **Invoice
delivery**, **Reminder delivery**, **Peppol ID**, **GLN** and **Buyer reference**. An
empty field reads *Not set — the invoicing default applies*, with a hint saying what
is used instead (the customer's own email, the group's payment terms, an EHF recipient
derived from the organisation number). **Before you invoice this customer** lists what
would stop an invoice: EHF without a Peppol ID or organisation number, email without
any address, eFaktura on a business, no invoice address.

Editing needs `customers:billing-manage`. Delivery methods are **Email**, **EHF**,
**eFaktura** (private customers only) and **Paper**. **Check EHF** asks the Peppol network whether
the customer can receive EHF invoices and keeps the answer with its date; when it can
and EHF is not yet chosen, **This customer can receive EHF invoices** offers **Use
EHF**. See [Billing profile](/en/reference/customers/#billing-profile) and
[Peppol lookup](/en/reference/customers/#peppol-lookup).

## Contacts and their roles

A contact is a person. A customer's **Contacts** card lists the people linked to it
with their **Roles**, **Email** and **Phone** — a dimmed value is the contact's own,
inherited because none is set for this customer.

**Add contact** searches existing contacts by name, phone or email. Pick one, or choose
**No contact found — create "…" as a new contact** and fill in **First name** and
**Last name** (plus **Middle name**, **Prefix**, **Suffix**, **Phone**, **Email**).
Then describe the connection:

- **Title**, such as *CEO*.
- **Roles**: **Billing** (who gets the invoice and the reminder), **Project** (who is
  spoken to day to day) and **Decision maker** (who approves), each with a **Primary**
  switch. The first contact given a role is its primary; switching **Primary** on for
  another contact moves it. It cannot be switched off on the primary holder — make
  another contact primary instead, and the form says so.
- **Phone at this customer** and **Email at this customer**, used for this customer
  instead of the contact's own.

A connection needs a title or at least one role. The pencil on a row opens **Edit
connection — {name}**; the remove icon detaches the contact after a confirmation,
keeping both. See [Contacts and associations](/en/reference/customers/#contacts-and-associations).

**Contacts** in the sidebar lists every contact with **Name**, **Phone**, **Email** and
**Customers**, searchable by name, phone or email; **Create new contact** creates one
without a customer, the pencil edits it, the bin deletes it outright. A contact's page
has **Edit contact** and a **Customers** card mirroring the customer's Contacts card,
with **Add customer**. Contacts need `customers:contacts-view` to see and
`customers:contacts-manage` to change; the links `customers:associations-view` and
`customers:associations-manage`.

## The timeline

The **Timeline** card is the customer's history, newest first: automatic events the
system writes (customer created, owner changed, contact linked, registry change,
merged and so on) and manual entries you add. Each entry shows its type, a **Manual**
or **Automatic** badge, when it happened, who recorded it, and the details; **Load
more** fetches older entries. Reading needs `customers:timeline-view`. Narrow it with
the **Source** switch (**All**, **Manual**, **Automatic**), **Event types** and an
**Occurred on** range; **Reset** clears them.

**Add event** (`customers:timeline-manage`) records a moment: **Type** (**Call**,
**Meeting**, **Email**, **Note**, **Other** or **Registry change**), **Date** (not in
the future), an optional **Time (UTC)**, a **Description**, an optional **Source URL**
shown as **Open source** on the entry, and a follow-up (next section). The menu on a
manual entry offers **Edit**, **Revision history** and **Delete**. Every saved version
is kept, and **Revision history** lists them as **Revision 1**, **Revision 2** and so
on with who changed it and when. If an entry changed under you, the save is refused
with **This event changed**. See [The timeline](/en/reference/customers/#the-timeline).

## Follow-ups

A follow-up is a date on a manual timeline entry: what you promised to do next, and
optionally who will do it. In the entry form, set **Follow up on** — it may be in the
future — and then **Assigned to**; the assignee cannot be chosen before the date, and
clearing the date clears the assignee.

The entry then shows **Follow up {date}**, the assignee or **Unassigned**, and
**overdue** in red once the date has passed. **Mark done** ticks it off, after which it
reads **Followed up {date}**; **Reopen** takes that back. Both need
`customers:timeline-manage`.

**Follow-ups** in the sidebar is what is on your plate across every customer, oldest
due date first, with **Due**, **Customer**, **Description** and **Assigned to**.
**Assigned to** filters to **Me** or **Unassigned**; **State** to **Open**,
**Overdue**, **Done** or **All**; archived customers' follow-ups appear only under
**Done** and **All**. With `customers:timeline-manage` each open row has **Mark
done**. The page needs `customers:view` as well as `customers:timeline-view`, because
every row names a customer. See [Follow-ups](/en/reference/customers/#follow-ups).

## Export and import

**Export** (`customers:view`) downloads the list as you see it — the current search,
filters and sort, every page — as a semicolon-separated CSV. The legal identity's
columns are included only with `customers:legal-identity-view`, and more than 5000
customers is refused with a request to narrow the filter.

**Import** needs `customers:create`, `customers:update` and `customers:view` together,
and opens **Import customers**:

1. **Download template** gives you the header your import may write. A file of at
   most 5 MB, in UTF-8, with the template's columns — or an export's, if you may write
   every column it carries. A row with a customer number updates that customer; a row
   without one creates a new customer.
2. Drop the file or **Choose CSV file**. Tick **Allow a customer to share a legal
   identity with another customer** only if that is intended.
3. **Check** runs the import without saving anything and reports **Rows**, **Would be
   created**, **Would be updated** and **Have errors**, each problem by **Row** and
   **Column**. Fix the file and check again until something can be imported.
4. **Import** saves the rows and reports **Created**, **Updated** and **Failed**. When
   rows failed, **Download failed rows** gives you exactly those rows with an error
   column in front, to fix and import on their own.

One import runs at a time; a second is refused with **An import is already running**.
The file format and every rule are in
[CSV import and export](/en/reference/customers/#csv-import-and-export).

## Merging duplicates

When two records are the same customer, open the one to keep and press **Merge…**
(`customers:merge`). In **Merge a duplicate into {name}**, search for the duplicate by
name or number. The dialog explains what happens: the duplicate's contacts, addresses,
timeline and tags — and what Projects, Energy and Communications hold for it — move to
this customer; its own details stay readable in this customer's timeline; and it is
archived. This customer keeps every detail of its own.

**Merge** is held back, and the dialog says why, when the two are of different types
(change one first) or this customer is archived (restore it first); the server also
refuses a duplicate already merged away. Afterwards the dialog lists what moved. The
merged-away customer stays reachable under its old number with a banner, **Merged into
#… …**, linking to this customer; it can no longer be edited or restored. See
[Merging duplicates](/en/reference/customers/#merging-duplicates).

## Personal data

For a private person, **Personal data** (`customers:personal-data`) gathers the actions
the GDPR asks for:

- **Export personal data** downloads everything held about the person as one JSON
  file.
- **Schedule anonymisation…** chooses the day on which the person's name, legal
  identity, contact details, addresses, contacts, correspondence and the content of
  every timeline entry are removed; the customer number, the dates and what happened
  when are kept for bookkeeping. There is no default day: choose one after every
  retention period under Norwegian bookkeeping rules has passed. The customer must be
  archived first — the item is greyed out with *Archive the customer first* until it
  is — and a merged-away person is scheduled on the customer that absorbed them.
- Once scheduled, the page shows **Anonymisation scheduled for {date}** and the menu
  offers **Change anonymisation date…** and **Cancel anonymisation**; restoring the
  customer cancels it too.

An anonymisation cannot be undone. Afterwards the page shows **Anonymised on {date}**,
nothing on it can be changed, and only the export remains. See
[Personal data and anonymisation](/en/reference/customers/#personal-data-and-anonymisation).

## Permissions

| To | You need |
| --- | --- |
| See the list, a customer page, its billing profile and the 360 panel; export | `customers:view` |
| Create a customer | `customers:create` |
| Edit, restore, change the type, set owner, group and tags, manage tags and groups, edit contact details and addresses | `customers:update` |
| Archive a customer | `customers:delete` |
| See the legal identity and the Registry card | `customers:legal-identity-view` |
| Save a legal identity, refresh the registry record, update the legal name | `customers:legal-identity-manage` |
| Look up a company in Brønnøysundregistrene | `customers:lookup-view` |
| See contacts / create, edit and delete them | `customers:contacts-view` / `customers:contacts-manage` |
| See a customer's contacts / link, edit and unlink them | `customers:associations-view` / `customers:associations-manage` |
| Read the timeline and follow-ups | `customers:timeline-view` |
| Add, edit and delete timeline entries; set and tick follow-ups | `customers:timeline-manage` |
| Edit the billing profile, check and use EHF | `customers:billing-manage` |
| Import customers | `customers:create`, `customers:update` and `customers:view` |
| Merge a duplicate | `customers:merge` |
| Export personal data and schedule an anonymisation | `customers:personal-data` |

The full list, with what each key guards on the server, is
[Permissions](/en/reference/customers/#permissions).
