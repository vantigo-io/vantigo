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
| [Energy](/en/reference/energy/) | Metering points, meters, supply periods, consumption, the metering-point and consumption imports, and the day-ahead spot prices per price area. |
| [Communications](/en/reference/communications/) | Outbound business email across shared mailboxes, the outbox and retention. |
| [Products](/en/reference/products/) | The catalog of goods and services the company sells, with prices. |
| [Projects](/en/reference/projects/) | Projects per customer, codes, roles, financial shaping and work types. |
| [Time](/en/reference/time/) | Hours on projects, rates and their snapshots, weekly submission and approval. |
| [Expenses](/en/reference/expenses/) | Outlays and mileage, receipts, approval, reimbursement and invoicing. |
| [Invoices](/en/reference/invoices/) | The sales document: numbering, VAT, issue, credit notes, the PDF, payments, sending and export. |
| [Staff](/en/reference/staff/) | The workforce register: people, employments and terms, absence, competence, documents, accounts and groups. |
| [Planning](/en/reference/planning/) | Booking people's time on projects: allocations, tentative or confirmed, against the capacity computed from Staff's facts; the board, the project's plan, the person's timeline, the unbooked list and My planning. |
| [Calendar](/en/reference/calendar/) | Appointments with recurrence and colleagues, the sources other modules project their rows through, the window in the viewer's zone and a colleague's busy and free, the invitations, the calendar feed a phone subscribes to and the calendars subscribed to from elsewhere. |
| [Microsoft Entra ID](/en/reference/entra/) | The connection to one tenant, its client secrets and the connection test, the account rules, the guarded Graph client, and the user lifecycle Staff drives through it: the account prepared, enabled with a Temporary Access Pass, kept in step, disabled and deleted. |

Identity is not a module: accounts, sessions, MFA, RBAC, OIDC and SCIM are always
part of the application and are documented under
[Administration](/en/admin/authentication/).
