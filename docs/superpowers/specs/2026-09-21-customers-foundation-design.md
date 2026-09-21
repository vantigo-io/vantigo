# Customers foundation — design

First delivery of the Customers roadmap drawn up in
`docs/superpowers/research/2026-09-21-customers-module-next.md` (its "P0"). It adds no new
customer data. It closes the gaps that would otherwise be built upon: a timeline that
cannot say who wrote an entry, a list that finds a customer by name only, a customer row
two people can silently overwrite, duplicate customers nobody warns about, and legal
identities nobody validates.

Next deliveries (own specs): P1 "the invoice-ready customer" (addresses, customer-level
contact info, billing profile, Peppol lookup, a wider `CustomerDirectory`), then P2
(Brreg in full, with refresh).

## Constraints

- **The recorded exchange corpus is frozen** (`openapi/testdata/exchanges/customers.jsonl`,
  CONTRIBUTING "Contract first"). Every contract change here is therefore additive and
  optional: new response properties are not `required`, new request properties are
  optional, new query parameters are optional, new status codes are additions.
- Module boundaries: customers reads users only through `contracts.UserDirectory`
  (`deps.Users`), never identity's schema.
- Permissions are unchanged: no new key. What a caller may *search by* follows what they
  may *see* (D4).
- English and Norwegian catalogs both get every new string.

## Decisions

### D1 — Timeline entries name their author

`customers_timeline_entries` and `customers_timeline_entries_revisions` gain
`actor_user_id uuid NULL`. Every write made on behalf of a signed-in user — a manual
entry's create, update and delete, *and* the generated `customer.*` events a user's action
causes — is stored with `actor_kind = 'user'`, `actor_user_id` = the principal's user id and
`actor_display` = that user's display name at the time of writing (from
`contracts.UserDirectory`; `'Unknown user'` if the directory has no such user). The name is
a snapshot, like every other timeline column: a later rename does not rewrite history.

- The **entry** row keeps its original author. Each **revision** row carries the actor of
  *that* revision, so an edit or a delete by somebody else shows up in the history under
  their name. (Today a revision copies the entry's actor.)
- A write with no user principal (none exists today; SCIM never reaches these operations)
  keeps today's values: `unattributed`/`Unattributed` for manual, `system`/`System` for
  generated.
- Existing rows are left as they are; nothing can be said about who wrote them.
- Contract: `actorKind` is already a free string and `actorDisplay`/`actorDisplayName`
  already exist, so no schema change. The frontend shows the author on each entry, not only
  in the revision history.

### D2 — Legal identities are validated where a rule exists

- `country` must be an assigned ISO 3166-1 alpha-2 code (case-insensitive, stored
  lower-case as today). The list is embedded in Go; no dependency.
- When `country = "no"` and `type = "business"`, `id` must be a Norwegian
  organisasjonsnummer: whitespace is stripped, then exactly nine digits whose last is the
  mod-11 check digit (weights 3 2 7 6 5 4 3 2; a remainder that gives check digit 10 is
  invalid). It is stored as the nine digits. This holds for `source` `brreg` and `manual` alike.
- Other countries and the `person` type keep today's rule (non-blank, ≤ 50). A Norwegian
  *person's* legal id is deliberately not validated as a fødselsnummer — P5's GDPR work
  decides whether that field should exist at all; this delivery must not make it look blessed.
- Validation applies to writes only. Rows already stored are not re-validated, and a PUT
  that leaves the identity unchanged (identity omitted) never trips over an old value.

### D3 — `disabled` stays a label for now

`disabled` keeps being accepted, stored, shown and — new — filterable, and the docs say
plainly that it blocks nothing yet. Projects deliberately accepts even *archived* customers
("a project can outlive the relationship that started it"), so inventing a blocking rule now
would contradict a standing decision for a status nobody can invoice against yet. Invoices
gives it its meaning (Business Central's "blocked for invoicing").

### D4 — The list finds customers the way people look for them

`GET /api/v1/customers` gains:

- `search` matches, case-insensitively and as a substring: the name; the **customer
  number**; and, *only when the caller holds* `customers:legal-identity-view`, the legal
  name and legal id; and, *only when the caller holds both* `customers:contacts-view` and
  `customers:associations-view`, the first/last name and email (canonical or
  association-specific) of any linked contact. A caller without those permissions gets
  exactly today's name-and-number behaviour — search must never be an oracle for data the
  response would withhold.
  The customer number and legal id are also matched against the search term with all
  whitespace removed, so `923 609 016` finds `923609016`.
- `status`: `active`, `disabled` or `archived`. Naming a status shows exactly that status
  (so `status=archived` shows archived customers without `includeArchived`). Absent, today's
  rule holds: archived hidden unless `includeArchived=true`.
- `type`: `business` or `person`.
- `sortBy` adds `customerNumber`, `createdAt` and `updatedAt` to `id` and `name`; `id` is
  the tie-break throughout.
- `SafeCustomerResponse` already carries `customerNumber`; the frontend starts showing it
  (the list shows the database id today).

The two list queries (by id, by name) become one `ListCustomers` with a `sort_by`
parameter, so a filter is written once.

### D5 — The customer row has a revision

`customers.customers` gains `revision integer NOT NULL DEFAULT 1`. **Every** write to the row
adds one: update, type change, legal identity put/delete, archive.

- `SafeCustomerResponse` gains `revision` (optional in the schema — corpus).
- `PUT /customers/{id}` and `PUT /customers/{id}/type` accept an optional `revision`. When
  present and not equal to the row's, the answer is **409** `Customer revision conflict` and
  nothing is written; the comparison is repeated in the `UPDATE`'s `WHERE` so two writers who
  both read the same revision cannot both win. When absent the write goes through as today
  (corpus compatibility; the frontend always sends it).
- The legal-identity sub-resource and archive stay unconditional writes: each replaces one
  self-contained thing, and archive is idempotent.
- This follows the `revision` convention of `time` and `projects`, not the timeline's
  `expectedRevision` naming.

### D6 — A legal identity already in use is a conflict you can overrule

Wherever a legal identity is written (`POST /customers`, `PUT /customers/{id}`,
`PUT /customers/{id}/legal-identity`), if another customer — of any status, archived
included, since the right move is usually to restore it — already has the same
`(legal_country, legal_id)`, the answer is **409** with a problem body carrying
`code: "duplicate_legal_identity"` and `duplicates: [{id, customerNumber, name, status}]`.
The request may carry `allowDuplicateIdentity: true` to go ahead anyway (two departments of
one company kept as separate customers is legitimate), so there is no unique index. An
identity that is unchanged by the request is never checked. The caller holds
`legal-identity-manage` by the time this runs, so naming the other customer leaks nothing.

Similar *names* are a frontend hint only: while a name is typed in the create form, the
form asks the list endpoint for that name and shows up to three existing customers with a
link. No new endpoint.

### D7 — Frontend

- **List**: customer number column (replacing the id column), status and type filters,
  sortable headers (number, name, created), all in the URL (`status`, `type`, `sortBy`,
  `sortDirection`) and validated by the host route.
- **Detail**: Archive (confirmation) and Restore actions, shown from the host's
  permissions (`customers:delete` to archive; `customers:update` to restore, which is a PUT
  with `status: "active"`). An archived customer shows a banner.
- **Form**: sends `revision`; a 409 revision conflict tells the user the customer changed
  and reloads it; a 409 duplicate shows who already has the identity, with links and a
  "Create anyway"/"Save anyway" action; the similar-names hint.
- **Timeline**: each entry shows its author.

## Out of scope

Addresses, contact details, billing profile (P1); Brreg enrichment and refresh (P2);
merge (P5); renaming `docs/customers-authentication.md`; rate limits; error-code
vocabulary beyond the one `code` above; un-attributing or back-filling old timeline rows.

## Testing

Go: handler tests through `modtest` for every decision, each proven able to fail (remove
the guard, watch it go red); a concurrency test for D5 in the style of
`timeline_concurrency_test.go`; permission tests proving D4's search is no oracle.
Frontend: vitest for the api layer, list page, form modal and detail actions.
