---
title: Reference
description: The domain model, rules, permissions and endpoints of every module.
sidebar:
  order: 0
  label: Overview
---

The reference describes each module as the code implements it: the domain model, the
rules a request is checked against, the permissions, the background workers and the
endpoints. It is written for an integrator, an operator debugging a refusal, or a
contributor about to change the module, and it is kept in step with the code: a change
to a module's behaviour lands together with the change to its page here.

The reference is maintained in English. The generated **API** reference is built
from the OpenAPI contracts in
[`openapi/`](https://github.com/vantigo-io/vantigo/tree/main/openapi), one document
per module, so it cannot drift from what the server serves. It is part of the public
site at <https://docs.vantigo.io> and left out of the copy a Vantigo installation
serves under `/docs/`.

## Modules

| Module | What it holds |
| --- | --- |
| [Customers](/en/reference/customers/) | Customers, legal identity, contacts and roles, the timeline, addresses, the billing profile, registry lookups, GDPR. |
| [Energy](/en/reference/energy/) | Metering points, meters, supply periods, consumption, the metering-point and consumption imports, the day-ahead spot prices per price area, the power and grid tariffs with the metering point's billing facts, and the settlements that price a supply period's billing periods — handed to invoicing across `contracts.ConsumptionProvider` and stamped by Energy's invoiced-work holder. |
| [Communications](/en/reference/communications/) | Outbound business email across shared mailboxes, the outbox and retention. |
| [Products](/en/reference/products/) | The catalog of goods and services the company sells, with prices. |
| [Projects](/en/reference/projects/) | Projects per customer, codes, roles, financial shaping and work types. |
| [Time](/en/reference/time/) | Hours on projects, rates and their snapshots, weekly submission and approval. |
| [Expenses](/en/reference/expenses/) | Outlays and mileage, receipts, approval, reimbursement and invoicing. |
| [Invoices](/en/reference/invoices/) | The sales document: numbering, VAT, issue, credit notes, the PDF, payments, sending and export. |
| [Staff](/en/reference/staff/) | The workforce register: people, employments and terms, absence, competence, documents, accounts and groups. |
| [Planning](/en/reference/planning/) | Booking people's time on projects: allocations, tentative or confirmed, against the capacity computed from Staff's facts; the board, the project's plan, the person's timeline, the unbooked list and My planning; requests with their competence requirements, the candidates, the fill and the intake of the planned projects; an assignment's terms with the earliest release and the handover start derived from them, and the non-compete as the one hard refusal; the person's wishes, seen and resolved by the planners; and the caller's confirmed bookings on the calendar. |
| [Inventory](/en/reference/inventory/) | Stock of the catalog's goods in warehouses and stores: sites, each a tree of locations; the ledger of immutable movements — adjustments, moves and goods receipts — and the balances it keeps; a product's stock, a location's contents, the lookup by code; the lock order shared with Products, and the variant delete it refuses while stock remains. |
| [Orders](/en/reference/orders/) | Quotes and sales orders for customers: the header and its snapshot, lines and pricing, number series, the quote's states, revisions and accept, the order's life, versions and change log, PDFs, sending, the events, workers and holders, retention and the endpoints. |
| [Manufacturing](/en/reference/manufacturing/) | Work centers with their weekly working pattern and closures, bills of materials with lines and operations, the default and alternative BoMs, the cycle and depth walk and the locks behind it, the BoM overview with its requirements, availability, expected minutes and rolled-up cost, the cost fields shaped by permission, the variant holder, the refusals and the endpoints. |
| [Accounting](/en/reference/accounting/) | The books: Set up the books and the default chart, accounts with their grouping codes and system roles, tax codes and rate periods, vouchers and postings kept balanced and immutable by the database, the posting engine every voucher goes through, manual journals and their VAT lines, reversal, attachments, the lock, the opening balances, open items and settlements, the reports, the refusals and the endpoints. |
| [Knowledge](/en/reference/knowledge/) | The company's handbook as a tree of articles: the access model (the nearest member row, else the nearest default, materialised under one tree lock; Workspace, Shared and Private; what `knowledge:manage` may and may not do), the document allowlist and its plain text, autosave with version conflicts, the lock, the history, the trash and its groups, favourites, search in Norwegian and English, the Assistant's two tools and Hidden from the Assistant, the live topics, the retention worker and the erasure sweep, and the refusals. |
| [Calendar](/en/reference/calendar/) | Appointments with recurrence and colleagues, the sources other modules project their rows through, the window in the viewer's zone and a colleague's busy and free, the invitations, the calendar feed a phone subscribes to and the calendars subscribed to from elsewhere. |
| [Microsoft Entra ID](/en/reference/entra/) | The connection to one tenant, its client secrets and the connection test, the account rules, the guarded Graph client, and the user lifecycle Staff drives through it: the account prepared, enabled with a Temporary Access Pass, kept in step, disabled and deleted. |
| [Google Workspace](/en/reference/google/) | The connection to one Google Workspace organisation through a service account's domain-wide delegation: its keys in the plural and the Internal web client, the guarded client and the delegated token, and the connection test with its needed and unexpected scopes. Accounts, calendar and Gmail come in later deliveries. |
| [Meta](/en/reference/meta/) | The connection to Facebook Messenger through a Facebook Page: the Meta app's settings and sealed secrets, the signed webhook and its status codes, the inbound queue and its splitting worker, Send and the Graph error map, the Pages and the retention of personal data. |
| [Agents](/en/reference/agents/) | Virtual employees: the agents with their persona, disclosure wording and model; their roles bound to channels with a reply mode and caps; the levels the Owner gives each permission and the never list; the gate that applies them to the Operations dispatcher (the template allowlist, the customer scope); the Front desk template with its needs and hard rules; the eighteen endpoints and their refusals; and the audit. |
| [Assistant](/en/reference/assistant/) | Every person's assistant: availability by the instance, the Owner's mode and the person's switch; private conversations; runs that outlive the connection, their caps and the event stream; context compaction and the system prompt; the five tools — documentation search, finding, describing and calling the operations a person may call, and links; the action log; the permission levels an Owner sets and the gate that applies them to the dispatcher; the Owner's audit; retention and the erasure sweep. |

Identity is not a module: accounts, sessions, MFA, RBAC, OIDC and SCIM are always
part of the application and are documented under
[Administration](/en/admin/authentication/).
