# Customer import from a file — design (phase 6, delivery A2)

Phase 6 of the Customers roadmap ("Data operations and compliance") delivered a CSV
import in September
([`2026-09-24-customers-import-export-design.md`](2026-09-24-customers-import-export-design.md)):
one request, one canonical header, five thousand rows, a dry run and the failed rows
handed back. It was built for a round trip with the export. It is not what a company
arriving from Tripletex, Fiken, PowerOffice or a spreadsheet of their own needs on day
one: their file has their headers, their separator, their encoding and however many
rows their history holds, and the request-sized import answers that with "rename the
columns, split the file, try again". This delivery replaces it with an **import that is
a record**: the file is uploaded once and kept, its columns are mapped to customer
fields on screen with the file's own rows as the preview, a background worker checks
and then imports it with progress, and a permission of its own guards the door. The
export and the template are unchanged.

## Decisions

*Settled during implementation, and reflected below:* the uploader's display name is
snapshotted on the import (`created_by_name`), because the worker runs without a user
directory; in `createOrUpdate` by customer number a number nobody has is the row's
error, not a create; a check may be asked again on a `checked` import; a run asked
for keeps the check's counts until the worker takes it up; the lease key is
`CUSTIMP1`; and the three settings are listed in the administration guide's settings
table as well as on the installation page.

### D1 — An import is a record, parsed once, kept in the database

Migration `00045_customers_imports.sql`:

```sql
CREATE TABLE customers.imports (
    id                  integer      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    state               varchar(20)  NOT NULL,
    file_name           varchar(255) NOT NULL,
    size_bytes          bigint       NOT NULL,
    encoding            varchar(20)  NOT NULL,
    separator           char(1)      NOT NULL,
    header              text[]       NOT NULL,
    row_count           integer      NOT NULL,
    mapping             jsonb,
    processed           integer      NOT NULL DEFAULT 0,
    created             integer      NOT NULL DEFAULT 0,
    updated             integer      NOT NULL DEFAULT 0,
    failed              integer      NOT NULL DEFAULT 0,
    stop_requested      boolean      NOT NULL DEFAULT false,
    error               text,
    created_by_user_id  uuid,
    created_at          timestamptz  NOT NULL,
    updated_at          timestamptz  NOT NULL,
    requested_at        timestamptz,
    started_at          timestamptz,
    finished_at         timestamptz,
    CONSTRAINT ck_imports_state CHECK (state IN ('draft', 'checking', 'checked', 'importing', 'done')),
    CONSTRAINT ck_imports_counts CHECK (processed >= 0 AND created >= 0 AND updated >= 0 AND failed >= 0)
);
CREATE INDEX ix_imports_queue ON customers.imports (requested_at) WHERE state IN ('checking', 'importing');
CREATE INDEX ix_imports_recent ON customers.imports (updated_at DESC);

CREATE TABLE customers.import_rows (
    import_id   integer  NOT NULL REFERENCES customers.imports (id) ON DELETE CASCADE,
    row_no      integer  NOT NULL,
    cells       text[]   NOT NULL,
    outcome     varchar(10),
    customer_id integer,
    errors      jsonb,
    PRIMARY KEY (import_id, row_no),
    CONSTRAINT ck_import_rows_outcome CHECK (outcome IS NULL OR outcome IN ('created', 'updated', 'failed'))
);
```

The upload parses the file **once**, on the request, and writes every data row as its
cells (`text[]`, the formula guard already taken off) with pgx's `CopyFrom`: a hundred
thousand rows land in seconds. From then on nothing reads the file again — the mapping
step reads the header and the first rows, the worker streams rows in chunks, the
failed-rows download reads the rows an error names. The bytes themselves are not kept:
the rows *are* the file, and a parsed row is the only form every later step wants.

Why the database and not the object store: an import is a few days of personal data
in flight, not a document to keep; the worker needs to read it by row, resume by row
and write a result beside each row, which is a table's job and not a blob's; and an
installation with no `STORAGE_PROVIDER` — every installation without attachments — must
still import customers. A hundred thousand rows of forty cells is on the order of a
hundred megabytes, well inside what one table takes without notice, and the retention
sweep (D6) takes it out again.

`customer_id` on a row is the customer the row created or updated, so a finished import
can link each row to its customer. `errors` is the row's `[{column, message}]`,
`column` the file's **column index** (0-based) rather than its header, since a mapped
file may well have two columns with the same header. `created_by_user_id` is
identity's user, opaque here, nullable like every actor column; the response names the
person through the user directory, as the export's `ownerName` is named.

### D2 — Reading the file: the person's file, not ours

The upload takes **one multipart part `file`** of at most **50 MiB**, at most
**100 000 data rows** and at most **200 columns**; a tenth of that is a large customer
base in this product's market, and the caps keep one import inside what the worker
walks in minutes (D6), what the browser uploads in one request through a hosted
installation's proxy, and what the row table carries. The header is the first row
that is not blank; blank rows are skipped and never numbered, as today. Then:

- **Encoding.** UTF-8, with or without a byte order mark. A file that is not valid
  UTF-8 is read as **Windows-1252** — the encoding of every CSV a Norwegian Excel saved
  without "UTF-8" in the name, and of most legacy exports; æ, ø and å are the
  difference, and the preview shows them, so a wrong guess is visible before a row is
  written. The import records which (`encoding`), and the page says it.
- **Separator.** Detected from the header line among `;`, `,` and tab — the one that
  occurs most, a tie going to the semicolon (the export's own) — never asked for. A
  file whose columns come out wrong is seen in the mapping table at once (one column
  holding the whole header); the page says which separator was read and that saving
  the file with semicolons is the way past it. A file is not re-parsed with another
  separator: the rows are stored parsed, and a second upload is one click.
- **Quoting** is RFC 4180 with the detected separator, CRLF or LF, the formula guard's
  apostrophe taken off a cell that begins with one of the guarded characters
  (`csvUnguard`, unchanged — an export re-imports as the values it was written from).
- **Nothing about the header is refused.** An unknown column is simply a column that
  is not imported, a repeated one two columns to map or leave, a nameless one is
  "Column 7". The old file-level refusals existed because a header was the mapping;
  now the mapping is its own step, shown in full, and a column nobody mapped is a
  column the person saw go unmapped.

What is refused at upload, each a 400 on `file` in a sentence a person can act on:
no part or two, an empty file, over 50 MiB (the router's body cap, as today), no header
row, more than 200 columns, more than 100 000 rows, a line the CSV reader cannot parse
(an unbalanced quote). Refused before any row is written.

### D3 — The mapping: which column becomes which field, and what the rows do

The mapping is one JSON document on the import, `PUT /customers/imports/{id}/mapping`:

```
CustomerImportMapping {
  mode:    "create" | "update" | "createOrUpdate",
  matchBy: "customerNumber" | "legalId",          // required unless mode is create
  allowDuplicateIdentity: boolean,
  columns: [{ field: "email", column: 3 },        // a column at most once, a field at most once
            { field: "status", value: "active" }, …] // or a fixed value for every row
}
```

An entry names a field and **either** a column of the file **or** a `value`: the same
text for every row, read exactly as a cell would be — `status: active`, `currency:
NOK`, `legalCountry: NO`, `group: Imported 2026`, `tags: Tripletex` — so a file that
lacks a column every customer needs still imports, and a batch can be marked on the
way in. A fixed value is a mapped field like any other: it is written on every row,
validated by the field's own validator (an unknown group name is every row's error,
found by the preview before anything runs), and it needs the same permission its
column would. `customerNumber` takes no fixed value (a match key is per row). The
page offers it as the third choice of each field's Select, **Same value for every
row**, with a text box beside it.

The **fields** are the import columns the file format already has — `name`, `type`,
`status`, the legal identity's four, contact info's three, the two primary addresses'
six each, the billing profile's eleven, `group` and `tags` — plus `customerNumber`,
which is now only ever a match key, never a value written. Thirty-seven targets, each
written through the function its endpoint calls, behind its validator, followed by
its event, the importer as actor, a row a transaction: nothing of `import.go`'s
write side changes, and a row that succeeds is still indistinguishable from the same
edits made by hand.

What changes is the unit. The request-sized import wrote a **group** whole or not at
all, because a header could not say "email only"; a mapping can, and a person who
maps the email column alone means "set the email". So:

**A mapped field is written; an unmapped field is left as it is.** On an update, the
row's group is the customer's current group with the mapped fields laid over it, then
validated and written as a whole through the same function — so mapping `email` alone
keeps the phone and the website, and mapping `postalCity` alone onto a customer with no
postal address is that row's error on that column (an address needs a line and a
country). On a create, an unmapped field is blank. A mapped cell that **is blank still
clears** its field — the file said blank — with the two rules the old import had:
a blank `name` cell on an update is an error (a name is never cleared), and all of an
address's mapped cells blank remove the primary address of that type only when it is the
only one of that type, as before. A blank `tags` cell clears the tags.

**Defaults that make the common file work.** A Norwegian file has an organisation
number and nothing else about the identity. So, for a row whose `legalId` is mapped and
not blank: an unmapped or blank `legalCountry` is `NO`; an unmapped or blank `legalType`
is the row's customer type (business unless the row says person); an unmapped or blank
`legalName` is the row's `name`. The identity is validated as a whole as before (the
organisation number's check digit included); its source is `manual`, or kept when the row
repeats the identity on file field for field. `type` accepts, besides `business` and
`person`, the words a Norwegian file uses — `bedrift`, `firma`, `selskap`, `company` →
business; `privat`, `privatperson`, `private` → person — folded in the import alone,
never in the validator. A row with no `type` mapped is business, as before.

**Mode and match.** `create` makes every row a new customer; `matchBy` is ignored.
`update` and `createOrUpdate` find the customer by the mapped match column:
`customerNumber` (the column must be mapped; a cell that is not a whole number is the
row's error) or `legalId` (the identity's country and id, defaults applied — the
duplicate guard's own comparison, `customers:legal-identity-view` not needed, since
the row only learns "found" or "not found" and the number of what it found, which
every update tells it anyway). No match is a create in `createOrUpdate` and the row's
error in `update`; **more than one** match (an installation that allowed the
duplicate) is the row's error naming the numbers, in both modes. Two rows of the file
with the same identity are refused on the second in every mode, as today
(`inFileDuplicates`): a file does not list one customer twice, and in `createOrUpdate`
the second row would otherwise update what the first created in the real run and
create it again in the dry run. A matched customer that is merged away or anonymised
is refused as today. Type may not change by import, as today.

**Suggestions.** The upload answers a `suggested` mapping the page starts from, in
three tiers, the first that fits a column winning, a field taken by the first column
that claims it: the mapping of the **most recent import with the same header** (so a
failed-rows file, or next month's export from the same system, maps itself — nothing
new is stored, the header is on every import); the field's own JSON name (the export
and the template); and an alias table of the words these files use — `navn`,
`kundenavn`, `firmanavn`, `company` for `name`; `kundenummer`, `kundenr`,
`customerno` for `customerNumber`; `orgnr`, `organisasjonsnummer`, `org.nr`,
`organisationnumber` for `legalId`; `epost`, `e-post`, `mail` for `email`; `telefon`,
`tlf`, `mobil`, `mobile` for `phone`; `adresse`, `gateadresse`, `address`,
`addressline1` for `postalLine1`; `postnummer`, `postnr`, `zip` for
`postalPostalCode`; `poststed`, `city`, `by` for `postalCity`; `land`, `country` for
`postalCountry`; `fakturaadresse`, `fakturapostnummer`, `fakturapoststed` for the
invoice address; `fakturaepost`, `billingemail` for `invoiceEmail`;
`betalingsbetingelser`, `kredittdager`, `paymentterms` for `paymentTermsDays`;
`valuta` for `currency`; `kundegruppe`, `gruppe`, `category` for `group`; `etiketter`,
`stikkord`, `labels` for `tags`; and so on, matched after lowercasing and dropping
everything but letters and digits (`Org.nr` and `org nr` are `orgnr`). A suggestion is
only a suggestion: the mapping the person saves is the mapping. The default mode is
`createOrUpdate` by `customerNumber` when a customer-number column was suggested,
otherwise `create` — the day-one case.

**Validating the mapping** (400 on the field that is wrong): a field or a column
twice; a column index outside the header; a mode other than `create` without its match
column mapped; an unknown field; and the permissions below. A mapping is saved on a
`draft` or `checked` import; saving on a `checked` one puts it back to `draft` and
clears every row's result, since the check was about another mapping. The response is
the import with its **preview**: the first **twenty** rows, each with the errors its
cells would get under this mapping — the row planned through the endpoints' validators
with no database, `importLayout.plan`'s own pass — so a wrong column (a phone number
mapped as an email) is seen on the page before anything is checked, and the page can
show a sample row's cells under the fields they would become.

### D4 — A permission of its own: `customers:import`

The module gains its seventeenth key:

| Key | Display | Description | Sensitive |
| --- | --- | --- | --- |
| `customers:import` | Import customers | Upload a file of customers, map its columns, check it and import it; each field written needs the permission it needs by hand. | yes |

Every import operation (D7) is `permission:customers:import+customers:view` — view
because every answer names customers by number and name, the old import's own
reasoning. **The key is the door, not the content.** A file may still only say what
its sender could say by hand, and the mapping is where that is checked, before anything
runs (400 on `mapping.mode` or `mapping.columns` naming the key): a mode that creates
needs `customers:create`, one that updates needs `customers:update`, `createOrUpdate`
both; a legal-identity field mapped needs `customers:legal-identity-manage`; a billing
field mapped needs `customers:billing-manage`. The import operation's old rule
(create + update + view) is therefore not lost but moved to the thing it is about, and
an administrator grants `customers:import` to the one person doing the onboarding
without granting it to everyone who may create a customer. Sensitive, because one
import writes thousands of customers under one name; delegable like the rest. The
page hides what the person may not do (D8) and the server refuses it regardless. The
template keeps `customers:view`, the export too.

### D5 — The lifecycle: a check that is the run, then the run, each resumable

```
draft ──check──▶ checking ──(worker)──▶ checked ──run──▶ importing ──(worker)──▶ done
  ▲                  │                     │                 │
  └── mapping saved ─┴─── stop ────────────┘                 └── stop ──▶ done (stopped)
```

- **`draft`**: uploaded, mapping being made. `POST …/check` needs a mapping and queues
  the check (`requested_at`).
- **`checking`**: the worker runs every row exactly as the real run would, each in its
  own transaction rolled back at its end — the old dry run, unchanged in what it proves
  and in what it cannot see (an earlier row's effect on a later one; the in-file
  duplicate check closes the one likely case). Each row's outcome and errors are written
  to `import_rows` after its transaction, the import's `processed` and counts every
  chunk. **Stop** puts it back to `draft` with the results cleared.
- **`checked`**: the counts and the failed rows are on the import. `POST …/run` is
  allowed only here, and only when `created + updated > 0`; it clears the rows'
  outcomes and queues the run. Saving the mapping goes back to `draft`.
- **`importing`**: the real run. A row's outcome is written **inside the row's
  transaction** — `UPDATE import_rows SET outcome, customer_id, errors` beside the
  customer's own statements — so a row is either committed with its outcome or neither:
  a process that dies mid-import loses nothing and double-writes nothing, and the
  next cycle resumes at the first row with no outcome. (A dry-run outcome is written
  after its rollback; losing one costs a re-check of that row, nothing more.) **Stop**
  sets `stop_requested`; the worker reads it between chunks and finishes as `done` with
  `finished_at` set and `rows − processed` rows never attempted — which the result says.
- **`done`**: final. The failed rows and their customers' links are read from the rows;
  the import stays until the sweep (D6) or a delete.

Why the check is required before the run: it is the one moment a wrong mapping is
free to fix, the page's whole reason to exist, and a run on a hundred thousand rows
under one person's name should not start on a mapping nobody tested against the whole
file. The check costs what the run costs (D6 says what that is), and a person in a
hurry on a small file waits seconds.

`DELETE /customers/imports/{id}` removes a `draft`, `checked` or `done` import with
its rows (a running one is a 409: stop it first) — the file is personal data the
person may not want kept thirty days. A whole-request failure inside the worker (a
database gone, a store of vocabulary that cannot be read) is logged, written to
`error` on the import, and the import is retried next cycle from where it stood; it
is never silently abandoned. The import answers `error` so the page can say "an error
stopped the import; it will be retried" rather than show a bar that stopped moving.

### D6 — The worker: `customers-import`

A new worker, the anonymisation worker's shape (an advisory lease, key `CUSTIMP01`,
one cycle at a time per installation; the lease's connection for the queue, the pool
for the rows' transactions, as the anonymisation does for its customers). A cycle:
take the oldest queued import (`requested_at`, among `checking` and `importing`),
resolve the actor (the uploader, through `actorFor`'s fallback when the user is gone)
and the group and tag vocabularies once, run the in-file duplicate pass over the rows'
identities, then stream the rows with no outcome in **chunks of 500** in row order,
plan each chunk in memory, run each row as D5 says, write the chunk's outcomes, update
`processed` and the counts, re-read `stop_requested` and the context, and continue to
the next chunk; at the end set the final state, `finished_at` and the counts from the
rows themselves (`count(*) FILTER (WHERE outcome = …)`, the one source of truth). Then
the next queued import, until none is queued; then the sweep.

**Cadence.** `CUSTOMERS_IMPORT_POLL`, default **5 seconds** — the outbox worker's own
figure, because a person is watching this one — and an **in-process nudge**: the
check and run handlers signal a package-level channel the worker selects on beside
its ticker, so in the default deployment (`WORKERS_IN_PROCESS=1`) a check starts the
moment it is asked for, and in a split deployment within the poll. A nudge is a
non-blocking send on a channel of one; a spurious one finds nothing queued and costs a
query. `CUSTOMERS_IMPORT_ENABLED` (default on) registers the worker at all, as the
other three switches do; with it off an import queues for ever and the page says so
("waiting for the background worker") once `requested_at` is a minute old with no
`started_at`. The installation page's worker paragraph names the worker.

**Pace.** The old timing test measured about 6 ms a row at the cap (a create with
contact info, an address, a billing profile and a tag); a hundred thousand rows is
then about ten minutes a run, check and import each, inside one cycle and inside
nothing's timeout, since nobody's request waits on it. The timing test moves to the
new path and keeps its opt-in (`VANTIGO_IMPORT_TIMING=1`).

**Retention.** After its queue is empty a cycle deletes every import not running whose
`updated_at` is older than `CUSTOMERS_IMPORT_RETENTION` (default **30 days**), rows
with it by cascade. A draft nobody finished, a result nobody looked at again — gone,
with the personal data in it. The customers an import wrote are of course untouched;
the granular events remain the audit trail, as the old design decided, and no
`customer.imported` event is added. The worker's cycle log line names the import,
the state it reached and the counts; nothing personal.

### D7 — Endpoints, all `permission:customers:import+customers:view`

| Operation | What it does |
| --- | --- |
| `POST /customers/imports` — multipart `file` | Parses (D2), stores, answers 201 `CustomerImport` with `suggested` |
| `GET /customers/imports` | The imports, newest first, for the page's start and for finding a running one; paged like every list |
| `GET /customers/imports/{id}` | The import: state, file facts, header, `sample` (twenty rows with their errors under the current mapping), mapping, counts, `processed`, times, `createdByName`, `error` |
| `PUT /customers/imports/{id}/mapping` | D3; `draft`/`checked` only (409 `import_running` otherwise); answers the import with the preview |
| `POST /customers/imports/{id}/check` | `draft` with a mapping → `checking`; 409 otherwise |
| `POST /customers/imports/{id}/run` | `checked` with `created + updated > 0` → `importing`; 409 otherwise |
| `POST /customers/imports/{id}/stop` | `checking` → `draft`, `importing` → stop requested; 409 otherwise |
| `DELETE /customers/imports/{id}` | not running → gone; 409 otherwise |
| `GET /customers/imports/{id}/rows?outcome=failed\|created\|updated&page&pageSize` | The rows with their cells, outcome, errors and the customer's id and number, paged, in row order |
| `GET /customers/imports/{id}/failed-rows` | `text/csv`: the failed rows with the file's **own header** and an `error` column first, the old failed-rows file built on the server from the rows it kept, `customers-import-<id>-failed-rows.csv` |

The 409s are `CustomerConflictProblem` codes in the module's established vocabulary:
`import_running`, `import_not_checked`, `import_nothing_to_import`, `import_finished`.
The upload is rate-limited like the attachment upload (600 an hour per address, the
same policy name pattern) and carries the router's body cap of 50 MiB plus framing.
The old `POST /customers/import` is **removed** (D9).

`CustomerImport` on the wire:

```
{ id, state, fileName, sizeBytes, encoding, separator, rows,
  header: [string], sample: [{ row, cells: [string], errors: [{ column, message }] }],
  mapping?: CustomerImportMapping, suggested: CustomerImportMapping,
  processed, created, updated, failed, stopRequested, error?,
  createdAt, createdByName?, requestedAt?, startedAt?, finishedAt? }
```

`sample`'s errors are under the saved mapping, or none when there is no mapping yet.
`suggested` is answered on every GET as well, so the page can offer "Reset to
suggestions" without keeping state of its own.

### D8 — The frontend: a page with three steps, not a modal

The import leaves the modal. A modal that cannot be closed for ten minutes, that
loses the result when the tab goes, and that has to fit forty columns of mapping in
`size="xl"` is the wrong vessel; a page is what the navigation convention gives a
thing that leaves the list ("a tab never leaves the page; something that does is a
header action"). Two routes in the host, both thin wrappers over `@vantigo/customers-ui`
pages, both under the customers area's prefix (nesting means belonging):

- **`/customers/import`** — `PageHeader` with breadcrumb Customers › Import. A dropzone
  (the old modal's, with "Download template" beside it: a template still maps itself and
  is the honest answer to "what can I import?") and **Recent imports**: a table of the
  imports with file name, who, when, state and counts — a running one with its
  progress — each a link. Uploading navigates to the import's page.
- **`/customers/import/$importId`** — the one import, a Mantine `Stepper` across the
  top — **Map columns · Check · Import** — whose active step is **derived from the
  state** (`draft` → 1, `checking`/`checked` → 2, `importing`/`done` → 3): the URL names
  the import, the state names the step, a refresh lands where the person was, and a
  link to a running import shows its bar. The page polls `GET /imports/{id}` every two
  seconds while the state is `checking` or `importing` and otherwise not at all.

**Map columns.** Above the table: **What to do** as a segmented control (Create new
customers · Update existing customers · Create and update), **Match by** (Customer
number · Organisation number) shown unless creating, and the duplicate-identity
checkbox. Then one row per **file column**: the header as the file spells it (or
"Column 7"), its first three values dimmed beside it, and **Imported as**: a `Select`
grouped by the field groups the reference names, "Not imported" first, a field already
taken by another column greyed with "mapped from <that column>". The identity group
is absent from the Select without `customers:legal-identity-manage`, the billing group
without `customers:billing-manage`, and the modes a person may not run are disabled
with the key named — four capability props from the host (`canImportCreate`,
`canImportUpdate`, `canMapLegalIdentity`, `canMapBilling`), beside `canImport`
(`customers:import` + `customers:view`), which puts **Import** on the list page and
guards the two routes. Under the table, the **preview**: the first twenty rows as the
customer would be read — one column per mapped field, in the groups' order — a cell
with an error marked and its message in a tooltip and listed under the table, and a
sentence: "20 of 20 sample rows read cleanly" or "3 of 20 sample rows have a problem".
The mapping is saved as it changes (a debounced `PUT`; the preview is its answer), so
there is no Save and nothing to lose. **Check file** is the step's one action, enabled
once the mapping has a field.

**Check.** While `checking`: a `Progress` bar with "Checking row 12 400 of 98 000", the
counts so far, and **Stop**. Once `checked`: Rows · Would be created · Would be updated
· Have errors, the **errors table** (row, column as the file spells it, the message)
read from `/rows?outcome=failed` a page at a time, **Download failed rows**, **Change
mapping** (back to step 1, the server's `draft`), and **Import** — the step's action,
enabled when something would be created or updated, with a confirmation that says
the counts in words.

**Import.** While `importing`: the same bar, **Stop** with a confirmation that says
what stopping leaves. Once `done`: Created · Updated · Failed, and "Not attempted" when
it was stopped; the errors table; **Download failed rows**; and a table of the first
rows that succeeded with each customer as a link (`/rows?outcome=created`) — the
onboarding's "did it work?" answered by opening one. **Start another import** returns
to `/customers/import`. A finished import's `error` renders as an alert that says it is
retried, with the time it stopped.

The list page's **Import** button becomes a link to `/customers/import`. The old
modal, its tests, `lib/csv.ts`'s failed-rows builder and the client-side file holding
go; `api/import-export.ts` keeps the export and the template and gains the imports
client (`api/imports.ts`). Strings in `i18n.ts`, en + nb; the Spotlight gains "Import
customers" for those who may.

### D9 — What is removed

`POST /customers/import` (the request-sized import) leaves the contract: one way to
import is one thing to know, document and secure, and a script that drove the old
endpoint drives the new one in four calls (upload, mapping, check, run), gaining
progress and a result that survives the connection. `GET /customers/import/template`
stays as it is. `GET /customers/export` stays as it is, and its file maps itself on
upload. `import.go`'s row side (`importPlan`, `importRow`, `importCreate`,
`importUpdate`, `importRelations`, `importPrimaryAddress`, `inFileDuplicates`) is kept
and fed by the mapping instead of the header; its request side (`importFilePart`,
`importLayoutFor`, the group refusals, the `importing` mutex) goes. The old tests move
to the new paths where their rules survive (every write rule does) and go where the
rule went (the header refusals).

### D10 — Docs and roadmap

- `docs/src/content/docs/en/reference/customers.md`: **CSV import and export** becomes
  **Export, and import from a file** — the export as it is; the import's record, the
  reading rules, the mapping and its defaults, mode and match, the permission rule,
  the lifecycle, the worker and its settings, retention, the endpoints and the
  conflict codes, the failed-rows file; **Permissions** gains `customers:import` and
  the content rule; **The frontend** describes the page; **API** lists the ten
  operations less the one removed; the old delivery A paragraph says it was replaced
  and by what.
- `docs/src/content/docs/{en,nb}/user/customers.md`: **Export and import** rewritten
  as the three steps, the permission table's row changed to `customers:import` with
  the content keys, and a note on what the page shows while an import runs.
- `docs/src/content/docs/{en,nb}/admin/installation.md`: the worker paragraph gains
  `customers-import`, and the settings table `CUSTOMERS_IMPORT_ENABLED`,
  `CUSTOMERS_IMPORT_POLL` and `CUSTOMERS_IMPORT_RETENTION`.
- `ROADMAP.md`: phase 6 gains **Delivery A2 (done)** — the import wizard — and says
  delivery A's endpoint was replaced.

## Out of scope

Excel (`.xlsx`) and other formats (a spreadsheet saves as CSV); choosing the
separator or the encoding by hand (upload again); a mapping saved by name for reuse
(the last mapping with the same header covers the recurring case); importing contacts,
delivery and visiting addresses, the owner, the timeline or attachments; setting a
customer's number or changing its type by import; an "undo this import" (the rows
link each customer; the granular events say what changed; a merge or an archive is
the tool); a `customer.imported` event or a batch marker on the customer; running
two imports at once; an import from a URL or a scheduled import; email when an import
finishes; a per-row "skip" or in-page editing of a row (download the failed rows, fix,
upload).

## Testing

**Backend** (`imports_test.go`, `import_worker_test.go`, `import_read_test.go`, through
`modtest`; the worker driven by `RunCycle` as the anonymisation worker's tests are):

- Reading: a semicolon, a comma and a tab file each detected; a UTF-8 file with and
  without a BOM; a Windows-1252 file with æøå read right and `encoding` saying so; a
  blank header line skipped; blank rows skipped and unnumbered; a quoted cell with the
  separator and a line break in it; the formula guard taken off; the caps (201 at each
  cap, 400 one past: 50 MiB, 100 000 rows, 200 columns); no part, two parts, an empty
  file, an unbalanced quote each a 400 on `file`; the rows stored with `CopyFrom` and
  the header on the import; the rate limit.
- Suggestions: each alias family; the JSON names; the previous import's mapping with
  the same header winning over the aliases; a field claimed once; the default mode.
- The mapping: every 400 (a field twice, a column twice, an index out of range, update
  with no match column, an unknown field, each permission by key); the preview's
  errors under a mapping (a phone mapped as email); saving on `checked` returns to
  `draft` and clears the rows; saving while running is 409.
- The check and the run, each rule the old tests proved on the new path: create and
  update by customer number and by organisation number; no match in `update` an error
  and a create in `createOrUpdate`; two holders an error; a mapped field written and
  an unmapped field kept (email alone keeps the phone); a blank mapped cell clearing; a
  blank name refused on an update; the address rules; the identity defaults (an orgnr
  column alone makes a Norwegian business identity named as the customer); the type
  synonyms; the type change refused; group and tags by name; the duplicate guard, the
  in-file duplicate and `allowDuplicateIdentity`; the events with the uploader as
  actor; the dry run keeping nothing (rows, numbers, events) and reporting what the
  real run reports; a merged-away and an anonymised match refused.
- The lifecycle: check from `draft` only, run from `checked` only and only with
  something to do, each other transition 409 with its code; progress written per
  chunk; stop during a check returns to `draft` with the results cleared; stop during a
  run finishes as `done` with the rest unattempted and says so; a run interrupted
  between chunks (the context cancelled) and resumed by the next cycle writes every row
  exactly once; the outcome inside the row's transaction (a row whose customer write
  fails has no outcome); a worker error written to `error` and the import retried;
  delete of a draft and of a done import removes the rows, of a running one 409; the
  sweep removes an import past retention and leaves a running one; the lease held by
  another replica skips the cycle; the nudge starts a check without a tick; the worker
  contribution test names `customers-import` and its switch; the rows endpoint's paging
  and filters with the customer's number on each; the failed-rows file with the file's
  own header and the error column first; `createdByName` through the directory; the
  old timing test on the new path.

**Frontend** (`imports.test.ts`, `-import-start.test.tsx`, `-import-page.test.tsx`,
the host route tests): the dropzone uploads and navigates; recent imports listed with
state and a running one's progress; the step derived from each state; the mapping
table lists every file column with its samples and the suggested field; a field taken
is greyed; the identity and billing groups hidden and the modes disabled by the
capability props with the key named; a change saves and the preview re-renders with
its errors marked; Check file posts and the bar polls; Stop in each state; the counts
and the errors table paged; Download failed rows fetches the CSV; Import confirms with
the counts and posts; the done view's created links; the "waiting for the worker"
hint; the `error` alert; the list page's Import link behind `canImport`; both
catalogs' key counts.

**Gates**: `mise run server:check`, `server:test`, `frontend:check`, `docs:check`.
