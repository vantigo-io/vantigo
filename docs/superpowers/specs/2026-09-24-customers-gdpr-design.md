# GDPR for person customers — design (phase 6, delivery C)

Phase 6 of the Customers roadmap ("Data operations and compliance"), delivery **C**, the
last of the roadmap: a **person** customer's data can be handed to them in one file,
and it can be **anonymised on a chosen date** — the row stays (its number, its place in
projects and supply periods, the shape of its history), the person disappears from it.
Nothing is deleted from bookkeeping's point of view; nothing personal is kept past the
date. And the promise the module has made since phase 1 — never a fødselsnummer field —
becomes a rule the validator enforces.

## Decisions

### D1 — A national identity number is refused, not merely unvalidated

`validateLegalID` refuses, for `country = "no"` and `type = "person"`, any value that
is an eleven-digit Norwegian national identity number (fødselsnummer or D-number:
the two mod-11 check digits pass) — field error `legalId`: "A Norwegian national
identity number is never stored here". The same refusal applies through the CSV import
and the identity PUT, because it is the one validator. Other person identifiers
(passport numbers, foreign ids, a customer reference) stay free text. Docs: the
"deliberately not validated" paragraph becomes "deliberately refused".

### D2 — One contract for what other modules hold about a person

`contracts.CustomerPersonalData` — a second sanctioned cross-module direction with its
own design (`docs/module-boundaries.md` rule 9), not a method on the merge holder:

```go
// What a module holds about one customer as a person: a read for the
// export, a write for the anonymisation. Export runs outside any transaction
// and answers a JSON-serialisable section (nil when the module holds nothing
// for the id). Erase runs INSIDE the customers module's anonymisation
// transaction on the module's own schema, in its own code — the merge
// holder's rules — and reports what it removed or blanked, kind by kind.
type CustomerPersonalData interface {
    ExportCustomerData(ctx context.Context, customerID int32) (any, error)
    EraseCustomerData(ctx context.Context, tx pgx.Tx, customerID int32) ([]ErasedData, error)
}
type ErasedData struct { Kind string; Count int64 }
```

`module.Module.CustomerPersonalData func(Deps) contracts.CustomerPersonalData`, a
many-provider slot collected from every module Compose is given (the merge holders'
rule: schemas are migrated whatever `MODULES` says) into `Deps.CustomerPersonalData`.
Implementations: **communications** (export: the person's conversations — subject,
dates, direction, each message's text body, attachment names; erase: the conversations
and their messages, attachments through the retention worker's cleanup ledger, the
suggestion and candidate rows), **energy** (export: supply periods with their metering
point's address; erase: nothing — a supply period is the metering point's history and
its address is the point's, not the person's; the row keeps pointing at the anonymised
customer), **projects** (export: the person's projects — code, name, status, dates;
erase: nothing — invoiced work stays, no customer name is stored there).

### D3 — Export: everything, in one file

`GET /customers/{id}/personal-data` (a new **sensitive** key `customers:personal-data`,
plus `customers:view`; 409 `personal_data_not_a_person` for a business — a company is
not a data subject, its contacts are handled through their own customers if they are),
`application/json` as a download (`customer-<number>-personal-data.json`):
`{exportedAt, customer: {number, name, type, status, identity, contactInfo, addresses,
billingProfile, owner (display name), group, tags, mergedInto, anonymisation}, contacts:
[{contact fields as stored, association title/phone/email, roles}], timeline: [{every
entry: type, occurredOn, summary, note, payload, actor display, follow-up; revisions
omitted}], modules: {communications: …, energy: …, projects: …}}`. Shaped by nothing
else: the key means "may hand this person their data". Streamed from the request (the
CSV export's shape), no object store.

### D4 — Anonymisation is scheduled on a date and run by a worker

`PUT /customers/{id}/anonymisation` `{anonymiseOn: yyyy-MM-dd}` (`customers:personal-data`
+ view): the customer must be a **person** and **archived** (409
`personal_data_customer_active` — an active relationship is not anonymised out from
under it; archive first); the date may be today or later; `DELETE …/anonymisation`
cancels; both record events (`customer.anonymisation_scheduled` / `_cancelled`, the
actor). Migration `00030`: `anonymise_on date NULL`, `anonymised_at timestamptz NULL`, a
partial index on `anonymise_on WHERE anonymised_at IS NULL`. The response carries
`anonymisation?: {anonymiseOn, anonymisedAt?}`. **No default date**: Norwegian
bookkeeping rules keep accounting material for years after the fiscal year, and this
system invoices nothing yet — the caller chooses, the docs say why the field exists and
point at bokføringsloven without encoding a number.

The worker `customers-anonymisation` (`CUSTOMERS_ANONYMISATION_ENABLED`, default on;
`_POLL`, default 24 h; the advisory-lease shape of the registry feed) runs every due
customer (`anonymise_on <= today (UTC)`, `anonymised_at IS NULL`, archived) in **one
transaction each**, the row locked, at most 50 per cycle:

- **The row**: `name` → "Anonymised person" (the customer number stays — it is the
  bookkeeping reference), legal identity cleared, contact info cleared, the billing
  profile's identifiers cleared (`invoiceEmail`, `reminderEmail`, `peppolId`, `gln`,
  `buyerReference`; terms, currency, language and delivery methods stay — they are not
  personal), `website` cleared; owner, group and tags stay (staff and vocabulary).
- **Addresses**: deleted. **Peppol lookup**: deleted. **Registry record**: none for a
  person.
- **Contacts**: every association detached; a contact linked to no other customer
  afterwards is deleted (it existed only for this person).
- **Timeline**: every entry stays, dated and typed, with its content anonymised —
  manual entries' `summary`/`note` → "[anonymised]"; generated payloads rewritten: the
  keys that carry the person (`name`, `identity`, `contactInfo`, `billingProfile`,
  `before`/`after`/`changes` snapshots, `absorbed`, contact names) replaced with
  `"[anonymised]"`, the rest (`customerId`, ids, dates, counts) kept; revisions the
  same; follow-up notes go with their entries, dates stay. **Merge chains are one
  person**: customers merged into this one are anonymised in the same run, and the
  `customer.merged` payload on this customer's timeline that describes them is
  rewritten; if this customer was merged away, the `absorbed` block on its survivor's
  timeline is rewritten too.
- **Other modules**: each `EraseCustomerData` inside the transaction; the counts join
  the event.
- Then `anonymised_at` is set, `revision` bumps, and `customer.anonymised` is recorded
  **after** the rewrite (`{customerId, erased: [{kind, count}]}`, the generated fallback
  actor) — the one event that keeps its words. A failing module rolls the customer's run
  back; the worker logs and moves on, and retries next cycle.

An anonymised customer is read-only like a merged-away one (409 `customer_anonymised`
on every write through the same lock-time check), stays archived, and its export still
works (it answers what is left). Scheduling again after anonymisation is a 409.

### D5 — The frontend

On a **person** customer's page, a **Personal data** menu behind `canManagePersonalData`
(`customers:personal-data`): **Export** (a JSON download, the CSV download's pattern)
and **Schedule anonymisation** (a date picker, enabled only when archived with the
reason shown otherwise; when scheduled, the date and **Cancel**). A banner once
anonymised ("Anonymised on …", every edit hidden — the merged-away banner's shape) and,
while scheduled, a warning banner with the date. The timeline renders "[anonymised]"
content plainly. The identity form's `legalId` shows the new refusal. Host prop +
catalog label. en + nb.

### D6 — Docs

`docs/customers.md`: a **Personal data and anonymisation** section (the export's
contents, the scheduling rules, exactly what the worker clears/keeps/rewrites and why
the number and dates stay, the merge-chain rule, the read-only rule, the worker's config),
the identity section's refusal, the permission key, the statuses section, the API list,
the frontend bullets, a phase 6 delivery C paragraph and "the roadmap's last delivery";
`docs/module-boundaries.md` rule 9; `docs/communications.md`, `docs/projects.md` and
the energy row in customers' holder table; `ROADMAP.md` phase 6 complete (attachments
and the outbox-dependent timeline writers remain deferred).

## Out of scope

Business customers' data (not a data subject); deleting the customer row; a
law-derived default date; anonymising staff users (identity's own concern); a
bulk "anonymise everyone archived before X" (one customer at a time, scheduled by a
person); rewriting other modules' free text (a project named after the person);
encryption at rest.

## Testing

Customers through `modtest`: the fødselsnummer refusal (valid fnr and D-number
refused; an org number on a business untouched; an 11-digit non-fnr accepted;
through the identity PUT and the CSV import); the export (person only; every section
present with real data; the module sections through fakes; the download headers);
scheduling (person + archived only; date validation; cancel; the events; the response
field; a merged-away customer); the worker (`RunCycle` with `h.Advance`: due vs not due,
the row cleared/kept exactly as D4 lists, addresses/peppol gone, contacts detached and
orphans deleted, every timeline entry's content anonymised and its shape kept, revisions,
the merge chain and the survivor's `absorbed` block, the module erasers called inside the
tx and a failing one rolling that customer back while the next still runs, the
`customer.anonymised` event last and intact, `anonymised_at` + revision, read-only
afterwards, the export afterwards, idempotency across cycles, the lease). Each module's
personal-data implementation in its own package (communications: export shape and erase
through the cleanup ledger; energy/projects: export shape, erase no-op). Integration:
customers + projects composed for real. Frontend: the menu, the download, the scheduling
form and its gating, cancel, the banners, the timeline's anonymised rendering, the
identity refusal, `canManagePersonalData`, both catalogs. Docs against the code.
