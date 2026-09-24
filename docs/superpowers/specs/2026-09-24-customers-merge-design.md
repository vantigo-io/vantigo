# Merging duplicate customers — design (phase 6, delivery B)

Phase 6 of the Customers roadmap ("Data operations and compliance"), delivery **B**: two
customers that are the same real-world entity become one. One customer **absorbs** the
other: the absorbed customer's contacts, addresses, timeline, tags and every reference
another module holds move to the surviving one in a single transaction, and the absorbed
customer is archived with a marker saying where it went. Nothing is deleted; nothing is
merged by guesswork — the surviving customer keeps every field it has, and what it does
not take from the other is written into the merge event so nothing is silently lost.

## Decisions

### D1 — One transaction, one contract, every module re-points inside it

All modules share one PostgreSQL database and one pool, so a cross-module merge can be
atomic without an event bus: the customers module opens the transaction, and every
module that holds customer ids re-points them **inside that transaction, in its own
code, on its own schema**. The schema barrier holds — no customers query names another
schema — and a crash anywhere rolls back everything.

`contracts.CustomerReferenceHolder`:

```go
// A module that stores customer ids in its own schema. RepointCustomer moves
// every reference from `from` to `into` inside tx — the customers module's
// merge transaction, which already holds both customer rows locked — and
// reports what it moved, kind by kind, for the merge's own record. It never
// reads the directory, never opens its own transaction, and tolerates a
// reference that already points at `into` (a junction row that exists for
// both is kept once).
type CustomerReferenceHolder interface {
    RepointCustomer(ctx context.Context, tx pgx.Tx, from, into int32) ([]RepointedReferences, error)
}
type RepointedReferences struct { Kind string; Count int64 } // "energy.supplyPeriods", "projects.projects", …
```

`module.Module` gains `CustomerReferences func(Deps) contracts.CustomerReferenceHolder`
— a **many-provider** slot like `Workers`: Compose collects the holder of every module it
is given — enabled or not, unlike every other slot, since every schema is migrated
whatever `MODULES` says and a module switched off still has rows naming customers —
into `Deps.CustomerReferenceHolders []contracts.CustomerReferenceHolder` before any Mount.
Holders today: **projects** (`projects.projects.customer_id`), **energy**
(`energy.supply_periods.customer_id`), **communications** (`conversations.customer_id`,
`conversations.suggested_customer_id`, `conversation_customer_candidates` with `ON
CONFLICT DO NOTHING`). Time and expenses reach customers only through projects and hold
nothing. The rule that a directory is never called inside a transaction stands: a
holder's re-point is a write on the shared database, not a lookup, and it is the one
sanctioned cross-module write direction — `docs/module-boundaries.md` gains the rule.

### D2 — `POST /customers/{id}/merge`: this customer absorbs another

`POST /customers/{id}/merge` (a new **sensitive** key `customers:merge`, delegable —
merging rewrites other modules' data and archives a customer, which is more than
`customers:delete` does; plus `customers:view`), body `{sourceId, revision?}`: the
customer in the path **survives**, `sourceId` is **absorbed**. `revision` is the
survivor's (optional; stale → the revision-conflict 409); the absorbed customer needs
none — it is going away.

Refusals, in order: 404 for either customer; 409 `merge_self` (same id); 409
`merge_type_mismatch` (a person into a business or the reverse — a merge never changes
what a customer is); 409 `merge_into_archived` (the survivor is archived — restore it
first); 409 `merge_already_merged` (the absorbed customer was merged away before — its
marker says where). An **archived** source may be absorbed (that is the common case: the
duplicate was archived when noticed).

### D3 — What moves, what stays, what is recorded

Under both rows locked in ascending id order (`LockCustomer` twice — the contact
delete's lock-order rule) and `RetrySerializable`:

- **The survivor keeps every field of its own row**: name, type, status, legal
  identity, contact info, billing profile, owner, group, customer number. The absorbed
  customer's values for all of these go into the merge event's payload (`absorbed:
  {…the customer as it was…}`), so a person who wants its billing profile or identity
  can still read them. Nothing is filled in from the other by guesswork.
- **Contacts**: every association moves; a contact linked to both keeps the survivor's
  association (its title, phone and email override); roles are unioned; a primary the
  survivor already has stays, a primary for a role the survivor has none of becomes the
  survivor's, every other `is_primary` is demoted — one write under the lock, the
  addresses' demote-then-promote shape.
- **Addresses**: every address moves; the absorbed customer's primary of a type the
  survivor already has a primary for is demoted; labels stay.
- **Timeline**: every entry and its revisions move (`customer_id` rewritten on both
  tables, a revision following its entry's); follow-ups ride along. Payloads are **not** rewritten: `payload.customerId`
  records which customer an event happened to at the time, and the merge event on the
  survivor says the rest.
- **Tags**: unioned (`ON CONFLICT DO NOTHING`).
- **Registry record and Peppol lookup**: the survivor keeps its own; the absorbed
  customer's rows are deleted (they described an identity the survivor either shares or
  does not have).
- **Other modules**: each holder's `RepointCustomer` inside the transaction; the counts
  it returns are part of the answer and the event.
- **The absorbed customer** is set `archived`, gains `merged_into_customer_id`
  (migration `00029`: nullable, an in-module FK, `ON DELETE RESTRICT`, partial index),
  and both rows bump `revision`. It keeps its name, number and identity — history
  stays readable, and its page shows where it went. Markers stay one hop long: a
  customer merged into the absorbed one earlier now names the survivor (chains
  flatten, and each re-pointed row's revision advances). A merged-away customer no
  longer holds its legal identity for the duplicate-identity guard, and it is
  read-only: every write to it answers 409 `customer_merged` naming the survivor
  (final review ruling), the restore included.
- **Events**: `customer.merged` on the survivor (`{customerId, absorbed: {id,
  customerNumber, name, type, status, identity, contactInfo, billingProfile, ownerUserId,
  groupId}, moved: [{kind, count}]}`, summary "Absorbed #1005 Acme Norge AS: 3
  contacts, 2 addresses, 14 timeline entries, 2 projects") and `customer.merged_away`
  on the absorbed customer (`{customerId, into: {id, customerNumber, name}}`, summary
  "Merged into #1002 Acme AS"). No `customer.status_changed` beside it — the merge is
  the reason.
- The answer: `CustomerMergeResult {customer: SafeCustomerResponse (the survivor,
  decorated), moved: [{kind, count}]}`.

`SafeCustomerResponse` gains `mergedInto?: {id, customerNumber, name}` (absent unless
merged away). The directory's `Customer(id)` still answers an absorbed customer (archived,
with `MergedInto *int32`) — a consumer holding a stale id learns where to look, though
after a merge no module holds one.

### D4 — The frontend

On the customer page header, **Merge…** (a new `canMerge` prop, `customers:merge`) opens
a modal: a customer picker (this package's own copy of the projects' search-select
shape, over the list's search, excluding this customer and merged-away ones), a plain
sentence of what will happen ("Everything on #1005 Acme Norge AS — contacts, addresses,
timeline, tags, and its projects, supply periods and conversations — moves here; its
own details stay readable in the timeline; it is archived"), a type-mismatch or archived
warning before the button, and **Merge** → the result's counts, the page refreshed. A
merged-away customer's page shows a banner "Merged into #1002 Acme AS" linking there,
and its edit actions are hidden (it is archived). The duplicate-identity 409 in the
create/edit forms gains a "Merge…" hint naming the duplicate (a link to its page, where
the action lives) — not a merge from the form. en + nb.

### D5 — Docs

`docs/customers.md`: a **Merging duplicates** section (the direction, the refusals,
the table of what moves/stays/is recorded, the contract, the events, the marker), the
permission key, the API list, the frontend bullets, a phase 6 delivery B paragraph;
`docs/module-boundaries.md`: the new rule for the one sanctioned cross-module write
(`CustomerReferenceHolder`: inside the caller's transaction, own schema, own code, no
lookups); one paragraph each in `docs/projects.md`, `docs/energy.md`,
`docs/communications.md` (what the module re-points); `ROADMAP.md` phase 6 (B delivered;
C ahead).

## Out of scope

Un-merging (the event payload is the record; a reverse operation is a later delivery if
ever wanted); merging more than two at once; filling the survivor's blank fields from the
absorbed customer; rewriting historical payloads; a bulk "find duplicates" report (the
duplicate-identity 409 and the list search are the discovery surfaces today); GDPR
(delivery C).

## Testing

Customers through `modtest` with fake holders: the refusal ladder (404s, self, type
mismatch, into-archived, already-merged, stale survivor revision); the full move
(contacts with the shared-contact and both-primaries cases; addresses with the
primary-per-type demotion; timeline entries + revisions + follow-ups; tags union;
registry/peppol rows gone; survivor fields untouched; the absorbed customer archived
with the marker, `mergedInto` on its response; both revisions bumped); the two events
and their payloads; the holders called inside the transaction with the right ids and
their counts in the answer; a holder error rolls everything back (nothing moved, no
marker, no events); concurrency (two merges racing on the same pair — one wins, the
other gets `merge_already_merged`; a merge racing a contact attach — retried). Each
holder in its own package: projects (real rows re-pointed, counts), energy (supply
periods), communications (both columns + the candidates' `ON CONFLICT`). Integration:
customers + projects composed for real — a merge re-points a project and the survivor's
overview shows it. Frontend: the modal (picker, the sentence, warnings, the result),
the banner, the 409 hint, `canMerge`, both catalogs. Docs against the code.
