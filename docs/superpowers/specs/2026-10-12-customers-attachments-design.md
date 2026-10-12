# Customer attachments — design (phase 4, delivery E)

Phase 4 of the Customers roadmap ("Light CRM"), delivery **E**, its last leftover: a
customer carries documents — a contract, an NDA, the correspondence behind a timeline
entry — the way a person in Staff carries a document archive and an expense carries a
receipt. It waited for "the storage module to have a model for it"; the object-store
port (`internal/storage`) now has three users — Expenses' receipts
(`expenses/attachments.go`), Invoices' PDFs and Staff's document archive
(`staff/objects.go`, `documents.go`, delivered in PR #136) — and Staff's is the model
this delivery follows: the bounds, the sniffing, the object-before-row order and the
**deletion ledger** that makes an erase finish. What it adds is this module's own: a
new kind of row the merge moves, the export names and the anonymisation takes out, and
two generated timeline events that record a file coming and going.

## Decisions

### D1 — One table, `customers.customer_attachments`, keyed by `attachments/<uuid>`

Migration `00044_customers_attachments.sql`:

```sql
CREATE TABLE customers.customer_attachments (
    id                  integer      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    customer_id         integer      NOT NULL REFERENCES customers.customers (id),
    timeline_entry_id   integer      REFERENCES customers.customers_timeline_entries (id) ON DELETE SET NULL,
    object_key          varchar(200) NOT NULL,
    file_name           varchar(255) NOT NULL,
    content_type        varchar(100) NOT NULL,
    size_bytes          bigint       NOT NULL,
    uploaded_by_user_id uuid,
    created_at          timestamptz  NOT NULL
);
CREATE UNIQUE INDEX ux_customer_attachments_object_key ON customers.customer_attachments (object_key);
CREATE INDEX ix_customer_attachments_customer ON customers.customer_attachments (customer_id, created_at DESC, id DESC);
CREATE INDEX ix_customer_attachments_entry ON customers.customer_attachments (timeline_entry_id) WHERE timeline_entry_id IS NOT NULL;

CREATE TABLE customers.object_deletions (…Staff's ledger, column for column…);
```

The bytes live in the object store under this module's own scope, **`customers`**, and
the relative key is **`attachments/<uuid>`** — a fresh random name, never derived from
the file name, so no caller chooses a key, collides with another's or reads anything
out of one. Deliberately **not** `customers/<customerId>/<uuid>` the way Staff keys by
person and Expenses by entry: a customer attachment is the one kind of object in this
installation that changes owner — a merge moves it to the survivor (D5) — and the port
has no rename, so a key that named the customer would either lie after a merge or
force a Get-Put-Delete outside any transaction, which a crash half-way through turns
into a lost file. With the customer kept out of the key, a merge rewrites one column
and the store is not touched. The physical key is `customers/attachments/<uuid>`;
[Object storage](/en/admin/object-storage/) gains the scope.

`uploaded_by_user_id` is identity's user, opaque here (module boundaries rule 4), and
nullable like `actor_user_id` on a timeline entry: every HTTP path carries a principal
today, and a write without one records none rather than inventing one. It is not named
on the response — the `customer.attachment_added` event beside every upload names the
actor, snapshotted, and one place is better than two that can disagree. The ledger is
Staff's `object_deletions`, column for column, in this module's schema: a delete or an
erase removes the row and queues the key in the same transaction, and the object is
finished after the commit (D4, D6).

### D2 — An attachment hangs off the customer, and may belong to a timeline entry

Both, through one table: `timeline_entry_id` is **null** for a document of the
relationship itself (a signed contract, an NDA) and set for a document of a moment (the
email behind an `interaction.email` entry, the minutes of a meeting). An attachment on
an entry is still the customer's: it moves with the customer in a merge, it is listed
on the customer, it is counted against the customer's cap, and it is erased with the
customer. Two tables — one per customer, one per entry — would have been two cap rules,
two merge statements, two erase kinds and two cards for one idea.

The rules of the link:

- Only a **manual, active** entry of the same customer takes an attachment: a generated
  entry is immutable (nothing is added to what the system recorded) and a deleted one
  is history. `timelineEntryId` naming anything else is a field error on
  `timelineEntryId` (400), judged under the customer's lock so a delete that committed
  since wins.
- The link is set on upload and never moved: an attachment is deleted and uploaded
  again, which is what a person would do with a file put on the wrong entry, and it
  keeps the two events honest.
- An entry's soft delete leaves its attachments where they are, still naming the entry
  — the entry row stays, as its revisions do, and a file is not history to be voided
  with a note. The customer's list still carries them, with the entry; the UI shows the
  entry's type and day beside each, deleted or not. A hard delete never happens to an
  entry, so `ON DELETE SET NULL` is a belt for a buckle this module does not have.

**Bounds**, Staff's values, there being no reason to differ: **PDF, JPEG, PNG or HEIC**,
decided by the bytes (`http.DetectContentType`, HEIC by its ISO base-media major brand)
and never contradicted by the declared type or the extension; at most **10 MiB** a
file; at most **200** attachments a customer, counted on the customer whatever entry
they are on; the file name kept without any path, control characters stripped, 255
characters, `attachment` when nothing usable was sent. A Word or Excel file is not
taken: a zip-based office format cannot be sniffed as what it claims and can carry
macros, and a contract that is to be kept is exported to PDF first — the same line
Staff drew for its archive. The multipart part is `file`; one file per request, a
second one refused rather than dropped. The type's refusal is one message on `file` for every way
the bytes and the name disagree, the size's another — this module's 400s are
validation problems with field messages and no code vocabulary (`errors.go`) — the
count's a 409 `attachment_count_exceeded` on `CustomerConflictProblem`, whose codes
the merge and GDPR deliveries established; a store that cannot write or read is a 503
problem. Uploads are rate-limited at
**600 an hour per client address** — Expenses' and Staff's figure, an office behind one
address shares it — the module's first rate-limited operation (`limits`, module.go).

### D3 — Endpoints, on the timeline's two keys; no new permission

| Operation | Access |
| --- | --- |
| `GET /customers/{id}/attachments` — the customer's attachments, newest first, each with the entry it belongs to when it belongs to one | `customers:timeline-view` |
| `POST /customers/{id}/attachments` — multipart `file`, optional `timelineEntryId` | `customers:timeline-manage` + `customers:timeline-view` |
| `GET /customers/{id}/attachments/{attachmentId}` — the bytes, streamed | `customers:timeline-view` |
| `DELETE /customers/{id}/attachments/{attachmentId}` | `customers:timeline-manage` + `customers:timeline-view` |

Why the timeline's keys and not a new pair: an attachment is a record of the
relationship's history — a contract is as much "what happened between us" as a note
of a call is — and the timeline keys are already the sensitive pair that guards the
customer's correspondence (a manual `interaction.email` entry carries the email's
text). Every attachment added or removed is a timeline event (D4), so whoever can
read the event can read the file, and whoever can write the entry can attach to it.
`customers:view` was the other candidate and is wrong: it is the one **non-sensitive**
read key, meant for names, numbers and a sanitized summary, and a signed contract is
none of those. A key of its own (`customers:attachments-manage`) would be a fourth
key every installation remembers to grant for a thing that, at every point of use, sits
inside the timeline's door — the receivables delivery's reasoning against a new key,
and the follow-ups delivery's for riding on the timeline pair. The permission
descriptions of the two keys gain the word.

The responses:

```
CustomerAttachment {
  id, customerId, fileName, contentType, sizeBytes, uploadedAt,
  timelineEntry?: { id, eventType, occurredOn, summary, state }   // absent for a customer-level attachment
}
```

The object key is never on the wire. The list is one query with a left join on the
entry — the card needs the entry's type and day beside a file, and the timeline is
paged, so the entry may not be loaded. There is no per-entry list endpoint: a customer
has at most 200, the card loads them once, and the timeline groups them by
`timelineEntry.id` client-side.

The download streams the bytes with the type they were sniffed as, the file name RFC
5987 encoded, inline for PDF, JPEG and PNG and a file to save for HEIC, `Cache-Control:
private, no-store`, `X-Content-Type-Options: nosniff` and `Content-Security-Policy:
default-src 'none'; sandbox` — on the 404 and the 503 as on the bytes, Staff's and
Expenses' headers. The row is read on the pool and the object after it; a row whose
object cannot be read is 503, a fault to be seen, not a file to be denied.

The order of refusals on the upload: the file (400, before any database access — a
10 MiB body is not read twice), the customer (404), under the customer's lock the
read-only refusal (409 `customer_merged` / `customer_anonymised`, the same
`lockWritableCustomer` every write takes), the entry (400 on `timelineEntryId`), the
count (409). The object is written **before** the transaction and removed again by
whatever refuses or fails after it, Expenses' order, so a failure leaves an object
nothing points at, never a row pointing at nothing. The delete: the customer's lock, the
read-only refusal, the row deleted and the key queued on the ledger in one transaction,
then one attempt at the object on a context detached from the request and bounded by
thirty seconds; a store that refuses leaves the ledger row for the worker (D6). A
merged-away or anonymised customer's attachments are still **listed and downloaded** —
they are its history, and an anonymised customer has none left — and refuse the upload
and the delete as every write does. No object-store call is ever made inside a
transaction of this module, by construction: the store is reached only through three
accessors in `objects.go`, and none of them is called from inside `db.WithTx`.

### D4 — The timeline records it: `customer.attachment_added` and `customer.attachment_removed`

Two generated events, producer `customers.api`, written in the upload's and the
delete's transaction, the actor the caller's (`actorFor`, foundation design D1):

- `customer.attachment_added` — summary "Attachment added: avtale.pdf", payload
  `{customerId, attachmentId, fileName, contentType, sizeBytes, timelineEntryId?}`.
- `customer.attachment_removed` — the same shape, summary "Attachment removed: avtale.pdf".

Both summaries are truncated to the column's 500 UTF-16 units the way
`addressSummary` is. `fileName` joins `personalPayloadKeys`: a file is called "Kari
Nordmann – kontrakt.pdf" more often than not, so an anonymisation replaces it with
"[anonymised]" in every payload, and neither type is in `keptSummaryEventTypes`, so
the summaries go too — the allow-list does its job without a change. Attaching to an
entry writes **no revision of the entry**: the entry's own words did not change, and a
revision is a snapshot of them; the event's `timelineEntryId` says which entry the
file went on. The frontend's `typeKey` gains the two types ("Attachment added" /
"Vedlegg lagt til", "Attachment removed" / "Vedlegg fjernet"), so the card labels them
and the Event types filter can pick them.

### D5 — The merge moves them; nothing moves in the store

`moveOwnRecords` gains one statement, `UPDATE customers.customer_attachments SET
customer_id = survivor WHERE customer_id = absorbed`, after the tags, and this
module's kinds become **five**: `customers.attachments` after `customers.tags`,
counted in the answer and in `customer.merged` ("…, 3 attachments"; `mergeKindNouns`
gains the pair). An attachment on a timeline entry follows its entry — the entry moved a
statement earlier, so the link needs no rewrite. The object keys name no customer (D1),
so the store is not touched and the merge stays one transaction with no call out of the
database; the 200-cap guards a write, not a merge, as the address cap does. The
absorbed customer keeps nothing: its page lists no attachments afterwards, and the
survivor's lists them all, each still naming its entry.

### D6 — The export names them, the anonymisation takes them out

**Export** (`GET /customers/{id}/personal-data`): the file gains
`customer.attachments: [{id, timelineEntryId?, fileName, contentType, sizeBytes,
uploadedAt}]`, read in the same snapshot as the rest. The bytes are **not** in the
file: it is JSON built in memory and streamed from the request (GDPR design D3's
"no object store, no job"), and two hundred ten-megabyte files do not fit in a
response a browser saves; each is downloaded through its own endpoint, which the
export's row names by id, for as long as the customer is not anonymised. The docs say
so where they describe the export.

**Anonymisation** (`anonymiseOne`): every attachment row of the customer — on the
customer and on its entries alike — is deleted and its key queued on the ledger in the
run's transaction, before the modules' erasers, and the count is this module's
**fifth kind** in `customer.anonymised`, `customers.attachments`, after
`customers.timelineEntries`. A merge chain's members are anonymised one by one through
the same function, so each member's attachments go with it. After the commit the
worker makes one pass over the ledger, so the objects follow within the cycle; a store
that refuses leaves the rows for the next cycle, retried with Staff's backoff (two
seconds doubling to an hour), **without a ceiling**, so an erase is never best-effort
— the reason this module takes a ledger rather than Expenses' one compensating delete.
The ledger pass belongs to the **anonymisation worker** (`customers-anonymisation`): it
runs after each cycle's customers, on the pool rather than the lease's connection, as
Staff's runs after its erase, since it is the one worker of this module that erases.
With `CUSTOMERS_ANONYMISATION_ENABLED=0` the ledger's rows wait as the scheduled days
do; a hand delete still makes its one attempt. Nothing is added to `GET /settings`:
this module has none, and the rows a store outage leaves are visible in the worker's
log line ("an object could not be removed from the store; the ledger retries it") and
in the table.

### D7 — The customer page: an Attachments card, and files on an entry

In `apps/customers/frontend`, two things:

- A new **Attachments** card (`-customer-attachments-card.tsx`) between **Contacts**
  and **Timeline**, rendered the way the Timeline card is (the list's 403 is the card's
  error state). It lists the customer's attachments newest first — an icon by type, the
  name as a download (fetched with the session and handed to the browser, the
  personal-data export's shape: a bare link would land the person on a problem page
  when the read is refused), the size, the day, and for one on an entry "On: Email ·
  12 Oct 2026" — with **Add attachment** (a `FileButton` taking
  `.pdf,.jpg,.jpeg,.png,.heic,.heif`) uploading to the customer, and a **Delete** per
  row with a confirmation. The type's and size's refusals are shown as the server
  words them; the count's 409 too.
- On the **Timeline** card, a manual entry lists its attachments under its text, each a
  download with a delete, and its actions menu gains **Attach file…** — the same upload,
  with `timelineEntryId`. The page reads the list once and hands it to the timeline as
  a prop, grouped by entry there; an upload or delete from either place invalidates
  the one query key.

Both take `canManageTimeline` — the page already passes it, and
`CustomerOverview` ANDs it with `editable`, so a merged-away or anonymised customer
shows the files and no Add, Attach or Delete, as its contacts card shows the list and
nothing else. The host passes no new prop. Strings in `i18n.ts`, en + nb.

### D8 — Docs and roadmap

- `docs/src/content/docs/en/reference/customers.md`: a new **Attachments** section after
  **The timeline** (the table, the key scheme, the bounds and the sniffing, the
  endpoints, the refusals in order, the headers, the ledger, the two events); the
  generated event list gains the two types; **What moves, what stays, what is
  recorded** gains an Attachments row and the kinds become five; **What the worker
  does** gains an Attachments row and the kinds list `customers.attachments`; the
  export's table gains the row; **Permissions** says why no key was added; **API**
  gains the four operations and the count; **What comes next** drops the leftover.
- `docs/src/content/docs/{en,nb}/user/customers.md`: the customer page's card list
  gains **Attachments** / **Vedlegg**; a new **Attachments** section (what to upload,
  where, the cap, the permission); the timeline section names **Attach file…**; the
  merge and personal-data sections name attachments; the permissions table's two
  timeline rows gain them.
- `docs/src/content/docs/{en,nb}/admin/object-storage.md`: the scope `customers`
  with its key, a "Customers' attachments, specifically" paragraph (the ledger, what
  to watch), the download paragraph, and `apps/server/internal/customers/objects.go`
  in `sources`.
- `ROADMAP.md`: phase 4 becomes "five deliveries done" with a **Delivery E (done)**
  paragraph; "Still ahead in this phase" drops attachments; the phase 6 paragraph's
  "still deferred" shrinks to the timeline writers.

## Out of scope

A rename or a move of an attachment between entries (delete and upload again); a
category or a retention class (Staff's document categories answer a legal question
this module does not have — a customer's contract is kept as long as the customer is,
and goes with the anonymisation); a `validTo` or `signedAt` (a contract's dates are a
timeline entry's to record, with the file on it); Word and Excel files (D2); a
thumbnail or an inline preview beyond the browser's own; attachments on a contact or
on an address; a per-entry list endpoint (D3); naming the uploader on the response
(the event does); a `GET /settings` retention block (D6); a sweeper for an object
orphaned by the process dying between the Put and the compensating Delete (Expenses'
and Staff's stated gap, unchanged); a bulk download or a zip; other modules attaching
to a customer (the outbox's day).

## Testing

**Backend** (`attachments_test.go`, through `modtest`, a fake object store in memory
set with `modtest.WithObjectStore`, the harness's recorder validating every exchange):
an upload of each of the four types is 201 with the row's fields and the object under
`attachments/<uuid>` in the `customers` scope; the list is newest first and names the
entry of one attached to an entry; the download streams the bytes with the stored
type, the disposition (inline for a PDF, attachment for a HEIC, a Norwegian name
intact), `nosniff`, the policy and `no-store`, and the same headers on its 404; a
PNG named `.pdf`, a PDF declared `image/png`, a text file, an empty file and no `file`
part are each 400 `attachment_type_unsupported` or a field error on `file`; a file one
byte over 10 MiB is 400 `attachment_too_large` and a ten-megabyte one is 201; two files
in one request are refused; the 201st attachment is 409 `attachment_count_exceeded`
and the object was removed; `timelineEntryId` naming a generated entry, a deleted
entry, another customer's entry and an unknown id each a field error with the object
removed; a store whose Put fails is 503 with no row; a store whose Get fails is 503 on
the download; the upload and the delete each write their event with the actor and the
payload, and no entry revision; the delete removes the row, deletes the object and
clears the ledger; a delete the store refuses leaves the ledger row with one failed
attempt and the next attempt after the backoff, and a later ledger pass that succeeds
clears it; a merged-away and an anonymised customer each answer 409 on the upload and
the delete and 200 on the list and the download; a caller without
`customers:timeline-manage` is 403 on both writes, one without `customers:timeline-view`
403 on both reads; the rate limit refuses the 601st upload in an hour; a merge moves
every attachment, those on entries still naming their entries, reports
`customers.attachments` with the count in the answer and the event, and touches no
object; the export lists the attachments with their metadata; the anonymisation
deletes every attachment of the customer and of each chain member, queues every key,
reports `customers.attachments` with the count, and the worker's ledger pass removes
the objects; the two events' `fileName` and summaries are "[anonymised]" afterwards;
the worker contribution test's names are unchanged (the ledger is part of
`customers-anonymisation`).

**Frontend** (`-customer-attachments-card.test.tsx`, the timeline test extended):
the card lists attachments with name, size, day and the entry reference; the name links
to the download URL with `target="_blank"`; Add attachment sends a multipart `file`
and refreshes the list; a type refusal and a count refusal are shown as the server
words them; Delete confirms, calls the endpoint and refreshes; without
`canManageTimeline` there is no Add and no Delete; a manual entry on the timeline lists
its own attachments and offers **Attach file…**, a generated entry neither; the two
new event types are labelled; the catalog test counts the new keys in both languages.

**Gates**: `mise run server:check`, `server:test`, `frontend:check`, `docs:check`.
