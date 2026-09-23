# Typed contact roles — design

Phase 4 of the Customers roadmap ("Light CRM"), delivery **B**: who at the customer does
what. Today a contact's association carries one free-text `role`, and the recorded corpus
shows what people put there: `CEO`, `CTO` — a job title, not a role. It answers "who is
this person" and never "who gets the invoice" or "who approves". This delivery keeps the
title and adds **typed roles** with a **primary contact per role**, the way Business
Central's bill-to contact and Salesforce's contact roles do. Delivery C (own spec) is
follow-ups; D is customer groups.

## Decisions

### D1 — The free text is a title, and stays one

`customers.customers_contacts.role` becomes `title varchar(255) NULL` (migration
`00025`; existing values are kept — they already are titles). On the wire the contract is
frozen: `AttachCustomerContactRequest`/`CustomerContactRequest` keep `role` (now
optional, a deprecated alias of the new optional `title`; `title` wins when both are
sent) and `CustomerContactResponse`/`ContactCustomerResponse` keep `role` **required**,
answering the title or `""` when there is none, beside the new nullable `title`. A
request with neither a title nor a role is refused (400, field `title`: `A contact needs
a title or at least one role`) — an association that says nothing about the person is not
worth having, and the corpus never sends one. Validation of the title is today's
(non-blank when given, ≤ 255 UTF-16 units, trimmed).

### D2 — Three roles, one primary each

New table `customers.customer_contact_roles(customer_id, contact_id, role varchar(30),
is_primary boolean NOT NULL, created_at timestamptz NOT NULL, PRIMARY KEY (customer_id,
contact_id, role), FOREIGN KEY (customer_id, contact_id) REFERENCES customers_contacts ON
DELETE CASCADE)` with the partial unique index `(customer_id, role) WHERE is_primary` —
the addresses' invariant, one table over. The vocabulary is code-defined:

| role | answers |
| --- | --- |
| `billing` | who gets the invoice (and the reminder) |
| `project` | who is spoken to day to day |
| `decision_maker` | who approves |

(`A contact role must be one of 'billing', 'project' or 'decision_maker', but was '…'`.)
A wider list (technical, executive sponsor…) is a value change for later; the free-text
title carries everything else today.

The primary rule is the addresses' rule, verbatim: the **first** contact given a role is
its primary whatever the request says; `primary: true` on another contact demotes the
current one in the same transaction; `primary: false` on the one that is the only or the
primary holder is refused (400, field `roles`); a contact losing a role it was primary
for promotes the **longest-standing** remaining holder (`created_at`, then contact id),
so there is always a primary while anyone holds the role. Detaching a contact (or
deleting it — the cascade) runs the same promotion for each role it held.

### D3 — Roles ride on the association's own endpoints

No new paths. `AttachCustomerContactRequest` and `CustomerContactRequest` gain
`roles?: [{role, primary?}]`: on attach, the roles to give (none when omitted); on
update, the **complete** set to hold (omitted = unchanged; `[]` = none). Duplicated
roles in one request are a 400 (field `roles`). `CustomerContactResponse` and
`ContactCustomerResponse` gain `roles: [{role, primary}]` (always present), sorted
`billing, project, decision_maker`. `GET /customers/{id}/contacts` keeps its order
(contact name) — a "primary billing contact" is found by scanning the list, which is what
the UI does; a directory accessor (`CustomerDirectory.PrimaryContact(customerID, role)`)
is one added method when Invoices asks for it, not before.

Permissions: the association endpoints' existing rules (`customers:associations-manage`
to write, `customers:associations-view` to read) cover roles — a role is part of the
association. The customer row is locked (`LockCustomer`, `FOR NO KEY UPDATE`) for every
write that touches roles, so two writers giving `primary: true` to different contacts
serialize and the partial unique index is the backstop, never the mechanism.

### D4 — Timeline

`customer.contact_attached` and `customer.contact_relationship_updated` payloads gain
`title` and `roles: [{role, primary}]` beside the existing `role` (= title); the update
event is recorded only when title, phone, email, the role set or a primary flag changed,
and its summary names what changed ("Roles updated" / "Now the primary billing contact").
A promotion caused by another contact's change or detachment is recorded on the promoted
contact as `customer.contact_relationship_updated` with the acting user — the person who
caused it — and a summary that says so. `customer.contact_detached`/`_removed` are
unchanged beyond carrying `title`/`roles` in the snapshot.

### D5 — What the user sees

- **Contacts card** (customer page): each row shows the title under the name and role
  badges — `Billing`, `Project`, `Decision maker` — with a star on the primary one; a
  row that is primary for a role reads "Primary billing contact" in the badge's tooltip.
  The attach and edit modals rename the Role input to **Title** and add a **Roles**
  group: a checkbox per role, each with a **Primary** switch enabled while checked;
  the switch is disabled with "Already the only holder" when the server would refuse
  `primary: false`. Saving sends `title` and the complete `roles`.
- **Contact page** (global contacts): the per-customer list shows the title and badges.
- Both catalogs (en + nb). No dashboard or attention change.

## Out of scope

A wider role vocabulary; roles on the customer's own contact info; a directory accessor
for the primary billing contact (when Invoices asks); using the billing contact for
invoice or reminder e-mail resolution (the billing profile's `invoiceEmail` stays the
answer until Invoices decides the precedence); contact-level permissions; follow-ups
(delivery C).

## Testing

Backend through `modtest`: attach with roles (first holder primary whatever was sent;
`primary: true` demotes; `primary: false` on the only holder refused; duplicates 400;
unknown role 400; title-or-role rule), update replacing the set (promotion of the
longest-standing holder when the primary loses a role; `[]` clears; omitted keeps), detach
and contact deletion promote, the corpus's `role`-only requests still work and answer
`role` = title, `title` and `roles` on both list shapes, events only on change with the
right summaries and actor, a forced race of two `primary: true` writers (one wins, the
other demotes it — never two primaries), the partial index as the backstop. Frontend:
modal round trips against wire-shaped fixtures (one literally the body with no roles),
badge rendering, the disabled Primary switch, both catalogs.
