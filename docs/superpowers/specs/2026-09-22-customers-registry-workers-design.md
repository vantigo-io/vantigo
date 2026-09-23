# Registry workers — design

Phase 3 of the Customers roadmap, delivery **B**: the two background workers the
Brreg-in-full design (`2026-09-22-customers-brreg-full-design.md`) left for later. Delivery
A gave a Norwegian business customer its registry record, fetched on a pick and re-read on
a click. This delivery keeps that record current without anyone clicking — Brreg's
incremental update feed says which entities changed, and each of ours is re-read through
the refresh path delivery A built — and re-asks the Peppol network, on a schedule, the
question a person's click asks today. Nothing new is shown to a user beyond one line on
the Registry card; what changes is that the record, the timeline, the attention list and
the billing warnings stop going stale.

## What the feed looks like (verified against the live API 2026-09-22)

- `GET /enhetsregisteret/api/oppdateringer/enheter`, `Accept: application/json`, no auth,
  `cache-control: no-store`. Parameters: `oppdateringsid` (**inclusive**: "from and
  including"; the docs say the next request may safely use `updateid+1`), `dato`
  (ISO-8601 `yyyy-MM-dd'T'HH:mm:ss.SSS'Z'`, "from"), `size` (max **10000**; 20000 is a
  400), `page`, `organisasjonsnummer` (a comma list — accepted for 2000 numbers but not
  used here, see D1), `includeChanges` (a JSON-patch list — not used here).
- Body: `_embedded.oppdaterteEnheter[]` of `{oppdateringsid, dato, organisasjonsnummer,
  endringstype, _links}`; `page{size,totalElements,totalPages,number}`; `_links` with
  `next` while more pages exist. **An empty result has no `_embedded` at all** and
  `page.totalElements: 0`. An `oppdateringsid` beyond `int32` is a 400.
- `oppdateringsid` is monotonic ascending across the feed, with gaps (~1 in 5 ids absent).
- `endringstype`: `Ny`, `Endring`, `Sletting` (struck from the register), `Fjernet`
  (removed from open data — "copies must delete"), `Ukjent` (older entries). One day of
  the whole register: ~5 600 entries (4 167 changes, 963 new, 424 deleted, 24 removed);
  a week: ~21 000. ~200 bytes each.

## Decisions

### D1 — One unfiltered scan, one exact cursor

The worker `customers-registry-feed` (named as communications' workers are, `<module>-<worker>`) reads the whole feed from a stored cursor, in pages,
and intersects each page with this installation's customers locally. It does **not** use
the `organisasjonsnummer` filter: chunked filtered requests have no safe cursor (an update
for chunk A's numbers published between A's request and chunk B's would sit below the id
B advanced to), while the unfiltered scan's cursor is exact — the last id of the page
just processed, plus one. The cost is the whole register's churn, ~5 600 entries a day at
~200 bytes: one small request per poll, a handful when catching up.

Cursor row: new table `customers.registry_feed_cursor` (migration `00023`), one row
(`id smallint PRIMARY KEY CHECK (id = 1)`): `next_update_id bigint NULL`, `started_at
timestamptz NOT NULL`, `last_polled_at timestamptz NULL`, `last_update_at timestamptz
NULL` (the `dato` of the last processed entry), `backfill_after_id integer NOT NULL
DEFAULT 0` (the sweep's own position, D3). A cycle whose page is empty records the
poll (`last_polled_at`) without moving the position; the two timestamps are for an
operator's `psql`, nothing reads them. With `next_update_id` NULL the request is
`?dato=<started_at>` (the feed is joined from the moment the worker first ran — never
from the beginning of time; the sweep in D3 covers what came before); once a page has
been processed it is `?oppdateringsid=<next_update_id>`. `size` is 1000; a cycle reads at
most 20 pages, so a week's backlog clears in two cycles; the body cap is 4 MiB.

A cycle: take the lease (D5) or skip; run the sweep (D3); read pages until a page comes
back short or the page budget is spent; per page, match `organisasjonsnummer` against
non-archived Norwegian business customers (`legal_country = 'no'`, `type = 'business'`,
`legal_id = ANY(...)`) — every `endringstype` counts, `Ukjent` included — then per matched
customer (once, even when the page holds several of its updates): write the hint (D2),
refresh (D4), and only after the whole page is handled store `next_update_id = last id +
1`, `last_update_at`, `last_polled_at`. A feed request that fails ends the cycle with the
cursor untouched (the next cycle re-reads the same page); a refresh that fails does not —
the hint records that the registry has something newer, and D3 retries it.

### D2 — `registry_updated_hint` means "the feed said so"

The column delivery A reserved. The worker writes `registry_updated_hint = dato` (the
newest of the customer's entries on the page, never moving it backwards) on the
customer's record row **before** refreshing, in its own statement. A successful refresh
then leaves `fetched_at ≥ registry_updated_hint`; a failed one leaves `hint > fetched_at`,
which is the single definition of *stale*. A customer with no record row has nowhere to
keep a hint: the refresh is attempted, and if it fails the entity is picked up again by
the sweep's backfill (D3), not by the hint.

The record's API shape gains `registryUpdatedHint` (`date-time`, optional — omitted
while NULL); a refresh carries the stored hint onto the record it answers with, since
`UpsertCustomerRegistryRecord` never writes the column and the card must not lose the
line between a click and the next GET. The Registry card shows one line when the hint is newer than `fetchedAt`:
"The registry reported a change on {date}; this record is from {date}." with the existing
Refresh beside it. `CustomerRegistryAddress.countryCode` becomes optional in the same
contract change (delivery A's leftover: it was `required` but could be `""`); the
frontend's normaliser maps an absent value to `""`.

### D3 — The sweep: retries, and the customers delivery A never saw

Before reading the feed, each cycle refreshes up to **50** stale records (`hint >
fetched_at`, oldest hint first) and up to **25** Norwegian business customers that have
**no record at all** (non-archived, valid organisation number, lowest id first). The
second half is the backfill: customers created before delivery A, and picks whose fetch
failed, get their record without anyone clicking — about a hundred an hour at the
default poll, so a few thousand customers are caught up within a day or two. The backfill keeps its own position on the
cursor row (`backfill_after_id`, taking customers with `id >` it and only organisation
numbers that are nine digits), because a customer whose number the register does not
know never gets a record: without a position those 25 rows would be the same 25 rows on
every cycle and the 26th customer would never be read at all. A full batch leaves the
position at the last id attempted and a short one resets it to 0, so an unresolvable
number costs one request per full pass rather than one per cycle. A backfilled record goes through the ordinary
first-fetch diff (name and deletion date against the legal identity), so a hand-typed
name that differs from the registry's raises `registryRenamed` exactly as a click would.
A sweep refresh that fails is logged and left for the next cycle; a sweep never advances
or touches the cursor.

### D4 — The refresh path is delivery A's, with a system actor

Every worker refresh is `refreshRegistryRecord(ctx, customerID, orgnr, legalName,
generatedFallbackActor)` — the same network-then-transaction path the click uses, with
`actor{Kind: "system", Display: "System"}`, bounded per call by `BRREG_TIMEOUT` (the
worker is not a waiting user; it can afford the retry budget). The 60-second click window
lives in the HTTP handler and does not apply: the worker only asks when the feed or the
sweep says there is a reason. The four outcomes keep their meaning: `Fjernet` in the feed
becomes a 410 from the entity endpoint and the row is deleted with one event; `Sletting`
becomes a `SlettetEnhet` body; `unknown` stores nothing. Events land on the timeline with
the system actor and `producer: customers.brreg`, as delivery A's do.

### D5 — Election and pacing, the way retention does it

Each worker takes a session-scoped `pg_try_advisory_lock` on its own recognisable key
(`0x4355535452454731` "CUSTREG1" for the feed, `0x4355535450455031` "CUSTPEP1" for
Peppol), on one acquired connection, released on `context.WithoutCancel` in a defer, the
connection closed rather than returned if the unlock fails — `communications/retention.go`'s
`underLease`, reproduced rather than shared because the two are the only users and a
shared helper would be a third module boundary to design. A held lease means "another
replica is on it": skip the cycle at debug level. Refreshes within a cycle run one at a
time; a cycle stops early when its context is cancelled and never leaves a page half
committed (the cursor is written after the page, atomically with nothing else).

### D6 — Peppol re-checks

The worker `customers-peppol-recheck`, registered only when `PEPPOL_LOOKUP_ENABLED=1` and
its own switch is on ("off" means no scheduled outbound request and a startup log that
names what actually runs — the same for the feed worker's switch), each cycle takes the lease and re-asks the network for up to
**100** customers: non-archived, with a stored lookup whose `checked_at` is older than
the re-check age **and** whose `participant_id` still equals the participant the billing
profile resolves to today (a lookup for a participant that changed is already stale by
identity and is not this worker's to refresh — a rule that lives in Go, `lookupParticipant`,
so the batch is selected in SQL and filtered in Go, and a cycle may re-check fewer than
the batch size), oldest `checked_at` first — **plus**
customers whose `invoice_delivery` is `ehf` and who have no stored lookup at all (the
customer whose invoices are already going to Peppol is the one whose registration must
not be assumed). Each is the handler's own lookup-and-store, extracted into
`lookupAndStorePeppol(ctx, customerID, participant, act) (…, error)` so the click and the
worker share one function: the result is upserted, and `customer.peppol_lookup` is
recorded **only when the answer changed**, with the system actor. A network failure is
logged (kind only, as the handler logs) and leaves `checked_at` alone, so the customer is
first in line next cycle. The billing profile's warnings (`ehf_recipient_not_registered`,
`ehf_available`) already derive from the stored lookup: a lapsed registration surfaces
there with no new code, and nothing is ever switched on the profile (delivery B of phase
2, decision 2, stands: only a person changes `invoiceDelivery`).

### D7 — Configuration and where the workers run

| variable | default | meaning |
| --- | --- | --- |
| `CUSTOMERS_REGISTRY_FEED_ENABLED` | `1` | the feed worker (strict `0`/`1`) |
| `CUSTOMERS_REGISTRY_FEED_POLL` | `15m` | how often a cycle runs |
| `CUSTOMERS_PEPPOL_RECHECK_ENABLED` | `1` | the re-check worker; effective only with `PEPPOL_LOOKUP_ENABLED=1` |
| `CUSTOMERS_PEPPOL_RECHECK_POLL` | `24h` | how often a cycle runs |
| `CUSTOMERS_PEPPOL_RECHECK_AGE` | `720h` | a lookup older than this is asked again |

`BRREG_BASE_URL` and `BRREG_TIMEOUT` are reused. The workers are registered through the
module's `Workers` field, so they run wherever the server already runs workers: in `api`
mode with `WORKERS_IN_PROCESS=1`, and in `worker` mode; never in `server` mode. Page size,
page budget, sweep and batch sizes are constants, not knobs.

## Out of scope

`includeChanges` (the entity is re-read whole); sub-entities (`underenheter`); an operator
"run now" or worker-status endpoint (the management listener reports no worker today —
a cycle's outcome is one log line: pages read, entries seen, customers refreshed, failures);
rate limiting against Brreg beyond one request at a time (the feed is one small request
per poll; refreshes are bounded by what changed); notifying anyone of what the worker
found (the attention list is the notification); a Peppol attention item (the billing
warning is where a lapsed registration belongs).

## Testing

Workers are tested through their cycle methods against the DB harness with a fake
transport (`Deps.HTTPTransport`) serving feed pages recorded from the live API today, and
a fake `Deps.PeppolLookup`. Feed: bootstrap by `dato` then by `oppdateringsid`; the
cursor advances only after a page is processed and not at all when the request fails; a
short page ends the cycle, the page budget ends it too; a matched customer gets its hint
and a refresh, an unmatched entry nothing, a customer with two entries one refresh with
the newest hint; `Fjernet` → 410 → row gone with one event; a refresh failure leaves
`hint > fetched_at` and the next cycle's sweep retries it; the backfill picks a customer
with no record and stops at 25; a held lease skips the cycle and the lease is released
after every cycle; `Run` stops with its context. Peppol: aged rows and `ehf`-without-lookup
are picked, fresh rows and changed participants are not; an event only when the answer
changed, with the system actor; a failure leaves `checked_at`. Config parsing for the five
variables. The contract: the new optional field and the relaxed `countryCode`, with the
frozen corpus untouched. Frontend: the hint line appears only when newer than `fetchedAt`,
and an absent `countryCode` renders as before.
