# Customer groups — design

Phase 4 of the Customers roadmap ("Light CRM"), delivery **D**: customer groups that
carry defaults. A group is a named bucket an installation defines — "Retail", "Key
accounts", "Public sector" — that a customer belongs to at most one of, and that
carries a **default payment term** every member inherits unless its own billing
profile says otherwise. Later, Products phase 4 hangs customer-group prices off the
same id. No hierarchy, no group-level prices yet, no bulk moves.

## Decisions

### D1 — A group is a vocabulary the module owns, like tags; a membership is a column, like the owner

Tags and the owner are opposite precedents. Tags are many-to-many, off the row,
last-wins through the row lock and carry no revision. The owner is **one nullable
column on `customers.customers` that shares the row's `revision`**. "At most one
group" is the owner's shape, and that is the one this delivery copies for the
membership — while the vocabulary itself copies the tags'.

Migration `00027`:

- `customers.customer_groups(id uuid PK, name varchar(100) NOT NULL,
  default_payment_terms_days integer NULL CHECK (0..365), created_at, updated_at)`,
  unique on `lower(name)` (the tags' index, NFC-normalised names). No colour, no
  description: a group is a policy object, not a label.
- `customers.customers.group_id uuid NULL REFERENCES customers.customer_groups(id)
  ON DELETE RESTRICT` — an in-module foreign key (unlike `owner_user_id`, which
  points across a module boundary and therefore has none), and RESTRICT because D2
  says a group in use is not deleted. Partial index `ix_customers_group (group_id)
  WHERE group_id IS NOT NULL`; `groupId=none` is a scan, the owner's own bet.

### D2 — The vocabulary: `GET/POST /customers/groups`, `PUT/DELETE /customers/groups/{groupId}`

- `GET` (`customers:view`) answers every group, **unpaged** (the tag vocabulary's
  bet, stated again: tens, not thousands), name-ascending, each with `customerCount`
  — the delete confirmation and the picker both read the one list.
- `POST` (`customers:update`) takes `{name, defaultPaymentTermsDays?}`; a name another
  group holds ignoring case is **409 `group_exists`** (the tags' `tag_exists`
  precedent); the term is validated with the billing profile's own
  `validatePaymentTermsDays` (0–365), field error keyed `defaultPaymentTermsDays`.
- `PUT` (`customers:update`) is a **full replace** of the same two fields — an omitted
  or null term clears the default, so the request says what the group is, not what
  changed.
- `DELETE` (`customers:update`) answers **409 `group_in_use`** with the member count
  when any customer belongs to the group; the members are moved first (the list
  page's `groupId` filter finds them). A silent detach would change every member's
  effective payment term with no record on any customer — the tags' cascade is
  right for a label and wrong for a default. 204 for an empty group, 404 for an
  unknown id.
- The vocabulary's own writes record **no timeline event** (the tags' rule: the
  group is the vocabulary, not the customer). Changing a group's default changes
  every member's effective term at once, by design; the docs say so.

**Permissions: no new key.** A group's name and default are installation policy —
the same reasoning that put the tag vocabulary on `customers:update` — and a
customer's own override stays where it is, behind `customers:billing-manage`. A
`customers:view` holder therefore learns a member's *inherited* term from the
vocabulary list, which is a policy fact, not a customer's negotiated one. The
alternative — gating the default field behind `billing-manage` inside a
`customers:update` endpoint — is a per-field permission this module has never
had, and the field is the least sensitive of the ten billing fields.

The write side is the consequence to state plainly (whole-branch review, accepted
as a trade-off): **`customers:update` decides a customer's *effective* payment
term whenever the customer's own profile leaves it unset.** Creating a group with
a 90-day default and moving a customer into it are both `customers:update`
writes, and one edit to a group's default moves the effective term of every such
member at once — `CustomerDirectory.BillingProfile` answers the new value.
`customers:billing-manage` guards only the customer's own override. This is
deliberate: a group's default is installation policy. If it is ever unwanted, the
change is to require `customers:billing-manage` as well on a `POST`/`PUT` that
sets or changes `defaultPaymentTermsDays`, and on a membership `PUT` whose before
or after group carries a default — a per-request check, not a per-field one, with
no new key, and cheap only while nothing is live.

### D3 — Membership: `PUT /customers/{id}/group`, `group` on the response, `groupId` filter

- `PUT /customers/{id}/group` (`customers:update` + `customers:view`) takes
  `{groupId: uuid|null, revision?}` and answers the `SafeCustomerResponse` — the owner
  endpoint's own body, ordering and guards: (1) customer 404; (2) stale `revision`
  409; (3) no-op (same id, nil-safe) → 200, nothing written, no actor resolved;
  (4) an unknown `groupId` → 400 field error `groupId`; (5) guarded write and the
  event `customer.group_changed` (`{customerId, before: {groupId, name}|null,
  after: {groupId, name}|null}`, name snapshotted so a rename never rewrites
  history; summary "Moved to group Retail" / "Removed from group Retail" / "Moved
  from Retail to Key accounts") in one transaction. Only a sub-resource sets the
  group; `POST /customers` and `PUT /customers/{id}` do not learn a `groupId` (the
  owner's rule).
- `SafeCustomerResponse.group?: {id, name}` — absent when none, never null, beside
  `owner`; resolved by `customerDecoration` in one batched query per page.
- `GET /customers?groupId=<uuid>|none` — an equality on `c.group_id` (`none` →
  `IS NULL`) in **both** `CountCustomers` and `ListCustomers` (their WHEREs are kept
  identical by hand). An unknown uuid simply matches nothing.

### D4 — The default is the third resolution tier, in the one place resolution lives

`resolveBillingProfile` (`customers/directory.go`) resolves `PaymentTermsDays` as
**the billing profile's own value, else the customer's group's default, else
`nil`** — the first field with a third tier, in the same function and the same
docs table every other rule lives in. `DirectoryBillingProfile` LEFT JOINs the
group for it. Nothing else inherits: currency, language and delivery methods
stay "not decided here" until a default is asked for.

`GET /customers/{id}/billing-profile` (unchanged permissions) gains
`groupDefault?: {group: {id, name}, paymentTermsDays?}` — present whenever the
customer belongs to a group, so the card can say "inherits 30 days from Retail"
when the profile's own `paymentTermsDays` is absent and "group default 30 days,
overridden" when it is present. The profile's own `paymentTermsDays` keeps
meaning "decided here"; the client derives the effective value the same way the
directory does, and the docs state the rule once. `warnings` learn nothing new.

`contracts.CustomerEntry` gains `Group *CustomerGroupEntry{ID uuid, Name string}`
(nil when none) from `Customer` and `Customers` — the seam Products phase 4 needs
for group prices, added now because it is one LEFT JOIN and one field, and a
consumer that resolves a price by group must not read the billing profile for it.

### D5 — The frontend

- **Relationship card** (`-customer-relationship-card.tsx`): a **Group** `Select`
  beside the owner — the vocabulary's names with a "No group" row, saved through
  `PUT /customers/{id}/group` with the card's revision handling (`syncCustomerRevision`
  + `setQueryData` of the answered customer), behind `canEdit`.
- **Manage groups modal** (the Manage tags modal's shape): name, default payment
  terms (days, blank = none), member count; create, rename/re-default, delete — the
  delete control disabled with the reason when `customerCount > 0`; 409
  `group_exists` recovered as the tags' `tag_exists` is. Opened from the list
  page's new **Group** filter (`groupId` ↔ URL, "All" (the other filters' word) / "No group" /
  each group) behind `canEdit`.
- **Billing card**: under the payment terms field, one line from `groupDefault` —
  "Inherits {n} days from {group}" when own is unset, "Group default {n} days —
  overridden here" when set, nothing when the group carries no default.
- No new host props, nav entries or routes: the host already passes `canEdit`.
- Catalogs en + nb for every new key.

### D6 — Docs

`docs/customers.md`: a **Groups** section after Owner and tags (vocabulary,
membership, the `group_in_use` rule, the inheritance rule and where it is
implemented), the billing profile section's `paymentTermsDays` row and the
directory's resolution table (`PaymentTermsDays` now has a rule), the permission
table's "no new key" note, the frontend bullets, the API list;
`ROADMAP.md` phase 4: groups delivered, leftovers named.

## Out of scope

Group prices (Products phase 4 reads `CustomerEntry.Group` when it comes), any
default beyond payment terms, bulk move / move-on-delete, a group on the create
form, hierarchy, paging the vocabulary, a group column in the list table (the
filter and the card carry it), attachments (still waiting on storage).

## Testing

Backend through `modtest`: vocabulary CRUD (409 `group_exists` ignoring case, term
0–365 field errors, PUT full replace clearing the default, 409 `group_in_use` with
the count, 204 when empty, 404 unknown); membership (owner's matrix: 404, stale
409, no-op writes nothing, unknown group 400 keyed `groupId`, event with snapshotted
names, response carries `group`, concurrent PUTs on one customer); the list filter
(`groupId=<uuid>`, `none`, count and rows agree); `resolveBillingProfile` unit
cases (own wins, group fills, neither → nil) and the directory integration
(`BillingProfile` and `Customers` see the group); `groupDefault` on the profile
GET for member / non-member / group without default. Contract: corpus still
validates. Frontend: the card's Select round trip (wire-shaped fixtures, one with
no `group`), the modal (create, rename, delete disabled with reason, 409
recovery), the list filter ↔ URL ↔ request (filter fetches by method+URL, never
"the last fetch"), the Billing card's three sentences, both catalogs.
