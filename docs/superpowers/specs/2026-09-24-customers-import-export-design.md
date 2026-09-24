# CSV import and export — design (phase 6, delivery A)

Phase 6 of the Customers roadmap ("Data operations and compliance"), delivery **A**:
customers leave and arrive as a CSV file — an export of the list as the caller sees it,
and an import that creates and updates customers from the same columns, with a dry run
first and the failed rows handed back for a re-run. Onboarding from Tripletex, Fiken or
PowerOffice is "export there, rename the columns, import here"; the canonical columns
are this module's own JSON names, because no competitor layout is documented anywhere
and one honest format beats three guessed ones. Delivery B (merge) and C (GDPR) are
their own specs.

## Decisions

### D1 — One canonical file

Semicolon-separated, UTF-8 with BOM, CRLF, a header row, RFC 4180 quoting, decimal
comma for money, ISO dates, and every cell starting with `=`, `+`, `-`, `@`, tab or CR
prefixed with an apostrophe — the expenses reimbursement export's format verbatim
(Norwegian Excel opens it right). The header names are the API's JSON names, so the
docs' field tables describe the file too:

| Group | Columns | Import writes it through |
| --- | --- | --- |
| Row | `customerNumber` (blank = create), `name`, `type`, `status` | create / `PUT /customers/{id}` / `PUT …/type` |
| Legal identity | `legalCountry`, `legalType`, `legalId`, `legalName` (source is always `manual`) | `PUT …/legal-identity`'s rules |
| Contact info | `email`, `phone`, `website` | `PUT …/contact-info`'s rules |
| Postal address | `postalLine1`, `postalLine2`, `postalPostalCode`, `postalCity`, `postalRegion`, `postalCountry` | the primary `postal` address |
| Invoice address | `invoiceLine1` … `invoiceCountry` | the primary `invoice` address |
| Billing profile | `invoiceEmail`, `reminderEmail`, `paymentTermsDays`, `currency`, `language`, `invoiceDelivery`, `reminderDelivery`, `peppolId`, `gln`, `buyerReference`, `defaultBillRate` | `PUT …/billing-profile`'s rules |
| Relationship | `group` (name), `tags` (names joined by `\|`) | `PUT …/group`, `PUT …/tags` |
| Export only | `id`, `ownerName`, `createdAt`, `updatedAt` | ignored on import |

Delivery and visiting addresses, contacts, the timeline and the owner are not in the
file (an owner is a user of *this* installation, named nowhere a file can reference
safely; the four export-only columns are accepted and ignored so an export re-imports
without editing). Unknown columns are a 400 naming them — a misspelt header must never
become a silently ignored column.

### D2 — Export: the list, as the caller sees it

`GET /customers/export` (`customers:view`), `text/csv`, taking the list endpoint's own
filters (`search`, `status`, `type`, `ownerId`, `tagId`, `groupId`, `includeArchived`)
and its sort, **capped at 5000 rows** — more is a 400 asking for a narrower filter (the
expenses precedent; a portfolio that size is exported in slices). Columns are **shaped
by permission** the way responses are: the legal-identity columns are present only for
`customers:legal-identity-view` (absent, not blank — a file without them re-imports
without touching identities); the billing columns are present for every viewer, as the
billing profile's own GET is. Filename `customers-YYYY-MM-DD.csv`. `GET
/customers/import/template` (`customers:view`) answers the header row alone, with every
importable column.

### D3 — Import: a file may only say what its sender could say by hand

`POST /customers/import` (`customers:create`, `customers:update` **and** `customers:view` —
view because every write it stands in for needs view by hand; multipart
`file`, body limit 5 MiB, at most **5000 data rows** — the export's cap, so a round
trip always fits) with `?dryRun=true|false` (default **true**) and
`?allowDuplicateIdentity=true|false` (default false, the create endpoint's own flag
applied to every row). **No new permission key.** Instead the file is checked against
the caller before any row is touched: the legal-identity columns need
`customers:legal-identity-manage`, the billing columns `customers:billing-manage`; a
file carrying a column its sender may not write is refused whole (400 naming the column
and the key). The importer can never write more than they could through the UI.

Per row, in order: a non-blank `customerNumber` selects the customer to **update**
(unknown → row error; the row `revision` is not sent, so the change applies regardless —
the module's own rule for an omitted revision); a blank one **creates**. A column group
is applied only when at least one of its columns is in the header, and then as the
group's endpoint applies it — a **full replace of that group**: a present-but-blank
cell clears (a billing profile column blank clears that field; all postal columns blank
removes the primary postal address when it is the only postal address, is a no-op when
there is none, and is a row error on `postalLine1` when the customer has others of the
type — removing the primary promotes the next, so anything else would remove one more
address each time the same file ran; `tags` blank clears the tags). A group whose columns
are absent from the header is left untouched. `group` and `tags` name existing
vocabulary entries (case-insensitive); an unknown name is a row error, never a
silently created word. Type may not change on an update through the row column
(`PUT …/type` is a deliberate act) — a differing `type` is a row error.

Each row is its own transaction through the **existing write paths** — the same
validation functions, the same guarded queries, the same timeline events with the
importer as actor — so a row that fails leaves nothing half-written and a row that
succeeds is indistinguishable from the same edits made by hand. Rows are processed in
file order, sequentially; a failing row does not stop the file. No new event type and
no batch marker: the granular events are the audit trail, and one onboarding writes a
handful of them per customer once.

The answer is JSON, `CustomerImportResult`: `{dryRun, rows, created, updated, failed,
errors: [{row, column?, message}]}` — `row` is the 1-based data row (the header is row
0), `column` the offending header when the error is a field error, `message` the
module's own validation wording. A dry run runs every row exactly as the real run
does — each in its own transaction — and rolls each back instead of committing it:
nothing is kept (no customer, no number, no event) and no lock outlives its row.
Because each row is then checked on its own, the importer also checks the file against
itself before any row runs: of two rows that create customers with the same legal
identity (country and id), the second is a row error in both runs unless
`allowDuplicateIdentity`. The difference that remains is stated plainly: a dry run
cannot see any other effect of an earlier row on a later one, and where that matters
the real run still refuses the later row cleanly, as that row's error. File-level refusals (unknown column, forbidden column, too
many rows, not a CSV, no `file` part) are a 400 problem, not a result.

### D4 — The frontend

The list page's header gains **Export** (downloads the current filter as the file, the
reimbursements download pattern) behind `canExport` (`customers:view` — always true on
the page, so effectively always shown) and **Import** behind `canImport`
(`customers:create` + `customers:update`; a new capability prop). The Import modal:
step 1 pick a file (the receipt dropzone's shape; a "Download template" link), step 2
**Check** (dry run) → the counts and an errors table (row, column, message), step 3
**Import** (enabled only when the check passed with at least one row) → the counts and,
when rows failed, **Download failed rows**: the original rows that failed, with an
`error` column appended, built client-side from the file the browser still holds — fix
them, re-import only those. The list invalidates on completion. en + nb.

### D5 — Docs

`docs/customers.md`: a **CSV import and export** section (the format, the column
table, the permission rule, matching, the group-replace rule, the dry run, the caps,
what the failed-rows file is), the API list, the permission section's note that import
needs no key, the frontend bullets, a phase 6 delivery A paragraph; `ROADMAP.md` phase 6
(A delivered; B merge and C GDPR ahead).

## Out of scope

Owner import; delivery/visiting addresses; contacts; a background job with progress
(5000 rows in one request is the cap and the precedent — measure it in the tests and
say what it takes); competitor column mappings; Excel (`.xlsx`); a batch id or
`customer.imported` event; changing a customer's type or number by import; merging
(delivery B).

## Testing

Backend through `modtest` (`modtest.RawBody` for multipart): the export's format bytes
(BOM, separator, CRLF, quoting, the formula guard), the column set with and without
`legal-identity-view`, every list filter honoured, the sort, the cap → 400, the
template; the import's file-level 400s (no part, not CSV, unknown column, forbidden
column per key, > 5000 rows, > 5 MiB), create and update by `customerNumber`, each
group's full-replace and untouched-when-absent behaviour, `group`/`tags` by name
(case-insensitive, unknown → row error), the duplicate-identity 409 as a row error and
`allowDuplicateIdentity`, a type change refused, a failing row not stopping the file, a
dry run writing nothing (no rows, no events) and reporting what a real run reports, the
events carrying the importer, a round trip (export → import → identical customers), and
a timing test at the cap. Frontend: the Export download (headers → filename), the Import
modal's three steps with a wire-shaped result fixture, the failed-rows CSV built from a
fixture file, the capability props, both catalogs. Docs against the code.
