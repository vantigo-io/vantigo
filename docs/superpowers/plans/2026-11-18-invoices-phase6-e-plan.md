# Invoices phase 6, delivery E — implementation plan

This plan builds delivery E of Invoices phase 6: sending in bulk, the 2028 rule on the
buyer's organisation number, and credit limits. It works from:

- **The spec:** `docs/superpowers/specs/2026-11-17-invoices-phase6-design.md`, §6
  (I36–I38), with I35 (§5) for the channel, §7 to §10, and the revision log (§15).
- **The research:** `docs/superpowers/research/2026-11-17-invoices-competitive-gaps.md`,
  §4.6, §4.8 and §5.
- **The phase plan:** `docs/superpowers/plans/2026-11-17-invoices-phase6-plan.md`. Its
  abbreviations and its *How it lands* hold here.

It is written against `claude/invoices-efaktura-pr`, which is `main` (`529727b15`) plus
delivery D (PR #380). **Where this plan's readings (*E: readings against the code*, at
the end) differ from the spec, the readings win.** Spec revision 6 records them (E10).

**Abbreviations:**

- `srv/` is `apps/server/internal/`;
- `inv/` is `srv/invoices/`;
- `cus/` is `srv/customers/`;
- `ord/` is `srv/orders/`;
- `mig/` is `srv/db/migrations/`;
- `fe/` is `apps/invoices/frontend/src/`. There is no `fe/src/fe` folder: pages,
  components, `assist-targets.json` and `i18n.ts` sit straight under `src/`.

## How E lands — split in two

E is two pieces of work that share nothing but a delivery letter:

- **E-1, send all.** This is Invoices only: a new table, a lease worker, a live topic,
  two screens. It needs D's eFaktura channel, so it waits for PR #380.
- **E-2, the 2028 rule and credit limits.** This spans Customers, Orders and Invoices:
  the registry, the billing profile, the confirm in Orders, and the issue in Invoices. It
  does not depend on D or on E-1.

Each is one PR against `main`. E-2 may open before E-1 merges, since neither touches
the other's files except the docs' *What comes next* lines.

| Piece | Branch | Migration |
| --- | --- | --- |
| E-1 | `claude/invoices-send-all` | next free when its PR opens. Main holds `00199` and `00200`, D holds `00201`–`00202`, C holds `00203` and identity's webhooks `00204`, so this is `00205_invoices_send_jobs` |
| E-2 | `claude/invoices-credit-limits` | next free after E-1's, for example `00205_customers_credit_limit_bookkeeping` |

Recheck both numbers against `main` right before merging (memory: *Migration number
collisions*).

---

## E-1 — Send all

### E1. Migration `00205_invoices_send_jobs.sql`

- **`invoices.send_batches`**, one row per *Send all*:
  - `id bigserial`;
  - `source varchar(12) NOT NULL CHECK (source IN ('selection', 'billing_run'))`;
  - `billing_run_id bigint NULL REFERENCES invoices.billing_runs`;
  - `requested_by uuid NOT NULL`;
  - `created_at timestamptz NOT NULL DEFAULT now()`;
  - `finished_at timestamptz NULL`.
  - `ck_send_batches_run` requires `billing_run_id` exactly when `source = 'billing_run'`.
- **`invoices.send_jobs`**, one row per document (I36):
  - `id bigserial`, `batch_id bigint NOT NULL REFERENCES invoices.send_batches`, and
    `invoice_id bigint NOT NULL REFERENCES invoices.invoices`;
  - `channel varchar(10) NULL CHECK (channel IN ('email', 'ehf', 'efaktura', 'print'))`.
    It is NULL until the worker chooses (reading E3);
  - `state varchar(14) NOT NULL DEFAULT 'queued'`, one of `queued`, `sending`, `sent`,
    `handed_over`, `awaiting_print`, `printed`, `skipped`, `failed` or `uncertain`
    (reading E4);
  - `code varchar(60) NULL`, the refusal or skip code, and `detail varchar(500) NOT NULL
    DEFAULT ''`;
  - `attempts int NOT NULL DEFAULT 0`, `lease_until timestamptz NULL` and
    `started_at timestamptz NULL`, the start of the attempt now in flight;
  - `transmission_id bigint NULL` and `delivery_id bigint NULL`, which say what the job
    produced;
  - `created_at`, `finished_at`.
  - `ux_send_jobs_live ON (invoice_id) WHERE state IN ('queued', 'sending')`. Two
    batches never send one document at once. A second enqueue skips it as
    `send_in_progress` (E3).
  - `ix_send_jobs_due ON (lease_until) WHERE state IN ('queued', 'sending')`, for the
    claim.
- **Triggers** follow the module's rule for history:
  - `refuse_send_job_change` allows only the worker's state moves, by a whitelist of
    from→to transitions written out in the function, and refuses DELETE.
  - The batch refuses DELETE.
  - Neither table holds personal data: the recipient stays in `deliveries`, and an error
    `detail` is the module's own words, never the provider's raw body. So the customer
    slots' erase has nothing to blank, and `mergeKindNouns` is unchanged. Say so in a
    comment beside `inv/customer_slots.go`'s list.
- Append the file to `inv/sqlc.yaml`. `srv/db/schema_test.go` checks the list.

### E2. Server: one send core per channel, callable without a request

None of today's sends has a core a worker can call. Each lives inside its generated
handler and answers `gen.*ResponseObject`:

- e-mail: `inv/send.go:268-427`;
- EHF: `inv/sendehf.go:340-529`;
- eFaktura: `inv/sendefaktura.go:346-566`.

Pull each out the way the billing worker's issue already is (`inv/billing_worker.go:548`
→ `issueDraft`, which answers `out.refusal` / `out.unavailable`):

- **`sendEmailCore(ctx, invoiceID, sendInput) (sendOutcome, error)`:**
  - the recipient, the override, then `profile.InvoiceEmail` (`send.go:319-328`);
  - the refusals `customer_anonymised`, `no_invoice_email`, `mail_unavailable` and
    `mail_failed` (`send.go:36-39`), and `invoice_draft` (`send.go:293`);
  - the SMTP send on `WithoutCancel` (`:372-375`), then `InsertDelivery` (`:392`).
- **`sendEhfCore`:**
  - the Peppol lookup (`s.lookupReceiver`, `inv/contractscalls.go:345`, then `canReceive`,
    `sendehf.go:170`) before the lock;
  - then `withLockedTx` (`sendehf.go:459`), with `ActiveTransmissionExists` and
    `InsertTransmission`;
  - the refusals `sendehf.go:40-47`.
- **`sendEfakturaCore`:**
  - `lookupForSend` (`sendefaktura.go:313`) before the lock;
  - then `withLockedTx` (`:466`);
  - the refusals `sendefaktura.go:36-45` and `currency_not_nok` (`:390`).

`sendOutcome` is `{state, code, detail, deliveryID, transmissionID, warnings}`. The
handlers become thin wrappers that map an outcome to today's responses, **byte for byte**:
the existing `send_test.go`, `sendehf_test.go` and `efaktura_test.go` must pass
unchanged. That is the test that nothing moved.

The rule at `inv/server.go:118-128` (module boundaries, rule 10) holds in every core. No
directory read, lookup, SMTP or object-store call is made inside `withLockedTx`, so the
worker never holds a connection while it waits for another.

### E3. Server: the action, the channel and the worker

- **The channel, `chooseChannel`** (new `inv/send_channel.go`), a pure function of the
  billing profile (`contracts/directory.go:90-160`: `Type`, `InvoiceDelivery`,
  `PeppolID`, `InvoiceEmail`), the document (an invoice or a credit note, and its
  currency), today, and what the installation can do (`ehfAvailable`, `mailAvailable`,
  and eFaktura's switch). I35, made concrete:
  1. **The preference wins** (`InvoiceDelivery`):
     - `ehf` → EHF;
     - `efaktura` → eFaktura;
     - `email` → e-mail;
     - `paper` → print.
     When the preferred channel then refuses (no Peppol id, not found, and so on), the
     job **fails with that code**. It does not fall back. The person sees why, and
     sending it another way is their call.
  2. **No preference, a person:** eFaktura if the stored lookup says `found` (or the
     refresh does), else e-mail if there is an invoice e-mail, else print.
  3. **No preference, a business:** EHF if a Peppol id is set and `canReceive`, else
     e-mail.
  4. **The 2027 B2B duty** (`send.go:47-54`, `b2bDutyFrom`): from 2027-01-01, send all
     never e-mails a Norwegian business (`BuyerOrganisationNumber` set), whatever the
     preference. With no EHF, the job is `skipped` with `buyer_norwegian_business_required`
     (reading E5).
  5. **A credit note** is never eFaktura (I31). It takes the next channel in its list.
  6. **eFaktura's own refusals** (`efaktura_due_too_soon`, `efaktura_due_too_far`,
     `currency_not_nok`, `kid_missing`) on the no-preference path fall through to e-mail,
     then print, as "not found" does. On the preference path they fail the job (rule 1).
  - The table test covers every row: person and business, each preference, credit notes,
    before and after 2027-01-01, and installations without mail, EHF or eFaktura.
- **`POST /api/v1/invoices/send-batches`** (`invoices:issue`):
  - Body: `{invoiceIds: int64[1..500]}` or `{billingRunId}`, exactly one of the two.
  - A run's documents are `BillingRunDocuments` (`inv/queries/billing_runs.sql:61`, used at
    `inv/billing_runs.go:419`), taken when the run is `done` or `cancelled`.
  - In one transaction:
    1. insert the batch;
    2. for each document, insert a job **unless** it is skipped. The skips are:
       - a draft, as `invoice_draft`;
       - a live job elsewhere (`ux_send_jobs_live`), as `send_in_progress`;
       - a document already delivered (below), as `already_delivered`.
       Skipped documents are inserted straight away as `state = 'skipped'` rows, so the
       result lists every document asked for.
    3. publish `run.changed` for the batch's topic (E4).
  - **"Already delivered"** is per document, not per channel, at enqueue. It is any of:
    - an e-mail delivery (`DeliveriesOf`, `inv/queries/deliveries.sql:1`);
    - an active EHF or eFaktura transmission (`ActiveTransmissionExists`,
      `inv/queries/transmissions.sql:48`, and `inv/queries/efaktura.sql:206`);
    - a manual delivery that is not removed.
    The single definition already exists: `RuleDeliveries` / `deliveriesOf`
    (`inv/manual_deliveries.go:61`). It is extended to count queued and submitted
    transmissions for this use only. See reading E2 for why per document.
  - It answers 202 with the batch: `{id, counts, jobs[]}`.
  - It refuses with:
    - 400 `send_batch_empty` or `send_batch_too_large`;
    - 404 for an unknown run;
    - 409 `billing_run_not_finished`.
- **`GET /api/v1/invoices/send-batches/{id}`** (`invoices:access`) returns the batch, its
  counts by state, and every job with its document number, its customer name, its
  channel, its state, its code and its detail.
- **`GET /api/v1/invoices/send-batches?billingRunId=`** (`invoices:access`) lists a run's
  batches.
- **The worker `invoices-send`** (new `inv/send_worker.go`) follows the billing worker
  (`inv/billing_worker.go:109-184`):
  - **Claim:** `ClaimSendJob` is one UPDATE with a sub-select `FOR UPDATE OF j SKIP
    LOCKED`, `WHERE state IN ('queued','sending') AND (lease_until IS NULL OR lease_until
    <= now)`, ordered by id. It sets `state = 'sending'`, `attempts + 1`,
    `started_at = now`, and `lease_until = now + 2 min`. It commits before any send.
  - **A job reclaimed in `sending`** with no outcome is handled by channel:
    - **e-mail:** look for a delivery of the document made after `started_at`. If there
      is one, the job is `sent`. If there is none, it is `uncertain`, never sent a second
      time (reading E4).
    - **EHF and eFaktura:** the core's own `ActiveTransmissionExists` makes the retry
      safe. If a transmission exists, the job is `handed_over`.
  - **Send:** `chooseChannel`, then the core. The outcome is written in a short
    transaction of its own, with the lock order invoice, then job. It publishes
    `invoices.send_job.finished` (new in `srv/contracts/events_invoices.go`, beside
    `AvtaleGiroClaimChanged` `:122-135`) and notifies the topic (throttled, E4).
  - The last job of a batch sets `finished_at` in the same transaction (`UPDATE ... WHERE
    NOT EXISTS (live jobs)`).
  - **Failures:**
    - a refusal is final, `failed` with its code;
    - an unavailability (`mail_unavailable`, `peppol_lookup_failed`, a 503 or a 502) is
      retried with backoff up to 5 attempts, then `failed`.
  - **Throughput:** at most 50 jobs a cycle, at an interval of 5 s. SMTP pacing is the
    mail driver's.
  - **Registration:** in `workers()` (`inv/module.go:190-209`), always on. Without mail
    it still fails its jobs honestly.

### E4. Server: print, live updates and the result

- **Print.** There is no print batch for invoices. `inv/print_batches.go` is reminder
  letters only (`:27-44`).
  - A `print` job ends as `awaiting_print`.
  - `GET /api/v1/invoices/send-batches/{id}/print.zip` (`invoices:issue`) answers a ZIP
    (`archive/zip`) of the **stored** PDFs (`inv/pdfstore.go`) of the batch's
    `awaiting_print` jobs, named `{number}.pdf`. Nothing is re-rendered, so what is
    posted is byte for byte what was issued.
  - `POST /api/v1/invoices/send-batches/{id}/posted` (`invoices:issue`, body
    `{postedOn}`) does this, for each `awaiting_print` job, under the invoice lock:
    - for an invoice, it records a manual delivery of kind `posted` as
      `PostInvoicesByIdManualDeliveries` does (`inv/manual_deliveries.go:207-235`), with
      its validation of the date, and the job becomes `printed`;
    - for a credit note, which records no manual delivery
      (`codeCreditNoteNoReminders`), the job becomes `printed` and nothing else.
- **Live.** A second topic in `inv/live.go:15-40`:
  - `invoices.send-batches` (keyless, `invoices:access`), event `batch.changed` with the
    batch id;
  - the worker notifies through a `runNotifier` throttle (`:72`), at most once a second
    per batch, and always on `finished_at`;
  - the internal test mirrors `live_progress_internal_test.go`.
- **Attention** (`inv/attention.go:42`): a batch with `uncertain` or `failed` jobs in
  the last 7 days is an item, "*N documents not sent*", linked to the batch.

### E5. OpenAPI and generation

- `openapi/invoices.yaml`:
  - the four operations;
  - the schemas `InvoicesSendBatch`, `InvoicesSendJob` and `InvoicesSendBatchInput`;
  - the job state and code enums;
  - the attention item type.
- Run `go generate ./...` in `apps/server` and `bun run gen:client`. Commit
  `fe/api-schema.d.ts`.

### E6. Frontend

- **Selection on the list** (`fe/pages/invoices.tsx`, `fe/components/invoice-table.tsx:32-40`):
  - A checkbox column and a header checkbox for the page, following
    `fe/components/uninvoiced-work-panel.tsx:306,336`. They are shown only to holders of
    `invoices:issue`.
  - A selection bar above the table: *N selected · Send all · Clear*.
  - Drafts can be chosen but are reported as skipped. The bar says so.
- **The billing run** (`fe/pages/billing-run.tsx`): a **Send all** button beside Cancel
  (`:92`), when the run is done or cancelled and has issued documents. Below the items
  table (`:150-166`) sits a *Sending* card that lists the run's batches.
- **The batch page** (new `fe/pages/send-batch.tsx`, route `/invoices/send-batches/$id`
  in `fe/lib/routes.ts`):
  - counts by state, and a progress bar while it runs;
  - a table of each document's number, customer, channel, state, and code in words;
  - for `awaiting_print`: **Download for printing** (the ZIP) and **Mark as posted** (a
    date);
  - live through `useLiveTopic("invoices.send-batches", …)`
    (`packages/frontend-shell/src/live/provider.tsx:68`), polling only while not live, as
    `fe/api/billing-runs.ts:37-75` does.
- **The confirm dialog** before sending: *Send N documents? Each goes by the customer's
  chosen channel, or by eFaktura, EHF, e-mail or print as I35 picks.* It shows no
  per-document preview (reading E6).
- **`fe/api/send-batches.ts`**, with a live test as `fe/api/billing-runs.live.test.tsx`
  has.
- **i18n:** every string in `en` and `nb` (`fe/i18n.ts`). Every job code gets words.
  `bun run translations:check` passes.
- **Tests:** select and send, the skipped drafts, the run's button, the print actions, and
  the live invalidation.

### E7. Tests (server)

- `send_channel_internal_test.go`: the table of E3.
- `send_batches_test.go` (Postgres):
  - every refusal code;
  - the skips (`invoice_draft`, `send_in_progress`, `already_delivered`);
  - a billing run's batch;
  - the triggers: a forbidden transition, and DELETE.
- `send_worker_test.go`:
  - e-mail sent, EHF and eFaktura handed over, and print, then posted;
  - a preferred channel's refusal fails the job;
  - the 2027 skip;
  - a credit note that falls through.
- **A pool-of-one test**, `TestSendWorker_RunsTheCycleOnAPoolOfOne`, after
  `inv/efaktura_test.go:324` (`db.WithMaxConns(1)`): a batch of one of each channel
  reaches its end states.
- `send_worker_races_test.go`:
  - two workers on one batch: every job is sent once;
  - two batches enqueued at once for one document: one job, one skip;
  - a lease that expires mid-e-mail: `uncertain` without a second mail, and `sent` when
    the delivery row exists.
- The fakes for the access point and the consumer provider use `FailOn`, not `FailNext`
  (memory: *Google fake FailNext vs token cache*).
- The cores (E2): the existing handler tests pass unchanged.
- The integration test that enables every module needs no change: no new module.

### E8. Seed, documentation, assistant and site

- **Seed** (`apps/server/cmd/vantigo/seed.go`, a new `seed_send_batches.go` called after
  `seedInvoicesReceivables` `:1547`): one finished batch over three seed invoices, one
  `sent`, one `awaiting_print` and one `failed` `no_invoice_email`, so the batch page and
  the attention item have something to show. It writes the rows directly, with no worker.
- **`en/admin/installation.md`:**
  - the seed sentence (`:115`) names the send batch;
  - the worker list (`:298-313`, *Background workers and scaling*) gains `invoices-send`,
    always on.
- **`en/user/invoices.md` and `nb/user/invoices.md`:**
  - a new section *Sending many documents at once*, after *Sending as eFaktura*
    (`:2022-2142`). It covers choosing on the list, Send all on a billing run, how the
    channel is chosen (I35 in a person's words, the preference first), the result page,
    printing and marking as posted, what *uncertain* means and what to do about it, and
    the permission.
  - *Run billing for every customer* (`:618`) and *A billing run of orders* (`:763`) say
    the run can now be sent.
- **`en/reference/invoices.md`** (English only):
  - *Sending a document* (`:2995-3225`) gains a sub-section *Send all*, with:
    - the batch and the job, and their states and transitions;
    - the channel rule (E3) as a table;
    - "already delivered";
    - the worker, its claim, its lease, and the `uncertain` rule;
    - the print ZIP, and *posted*;
  - the live topic table (`:1869`) gains `invoices.send-batches`;
  - the events table (`:6917-6925`) gains `invoices.send_job.finished`;
  - the endpoints (`:6645`), the permissions (`:6558`), and the tour targets (`:6952`);
  - *What comes next*: E-1 done;
  - `sources` gains the migration and the new files.
- **`ROADMAP.md`** (`:1763`, Phase 6): send all done. *Scheduled billing runs* (spec §13)
  is now unblocked.
- **Assistant:**
  - `fe/assist-targets.json` gains `invoices.invoices.select`, `invoices.send-all`,
    `invoices.billing-run.send-all` and `invoices.send-batch.print`, in `en` and `nb`;
  - bump `promptVersion` (`srv/assistant/prompt.go:15`, today `2026-11-18.3`) to the
    day's next;
  - update `en/reference/assistant.md:309`, which states the version.
  - Run `go generate ./...` after the docs, since the assistant embeds them (memory:
    *Docs edit needs go generate*).
- **The public site** (`../vantigo-landing`): the Invoices entry is in
  `web/src/i18n/en.ts:160` and `nb.ts`, `{name, body, points[]}`, with `points: []` today.
  The site's AGENTS.md names `web/src/apps/<key>.ts`; if that file exists on its `main`
  by then, write there. Add one point in both languages: *Send a whole billing run, or any
  selection, in one go, each document by eFaktura, EHF, e-mail or post.* Open that PR
  and link it from this one.

### E9. Gates and review

- **Gates:**
  - `mise run server:check`;
  - `mise run server:test`, or the invoices package with `-p 1` if the shared database
    runs out of clients;
  - `mise run frontend:check`, reading its log;
  - `mise run docs:check`.
- **Commit:** `feat(invoices): send all …`, signed, never `--no-verify`.
- **The independent review** checks, in particular:
  - the cores' byte-for-byte equivalence with the old handlers;
  - that no core calls out under `withLockedTx`;
  - the at-most-once rule for e-mail (`uncertain`);
  - `ux_send_jobs_live` and the enqueue race;
  - the transition whitelist in the trigger;
  - the 2027 skip;
  - that the ZIP serves stored bytes only.

---

## E-2 — The 2028 rule and credit limits

### E10. Migration `00205_customers_credit_limit_bookkeeping.sql`

- **`customers.customer_registry_records`** (`mig/00021_customers_registry_records.sql:6`)
  gains `business_register_registered boolean NULL`, from Enhetsregisteret's
  `registrertIForetaksregisteret`. It is NULL until the next refresh.
- **`customers.customers`:**
  - `bookkeeping_obligated_override boolean NULL`. NULL means "as the registry says".
    `true` and `false` are a person's decision;
  - `bookkeeping_obligated_override_reason varchar(500) NOT NULL DEFAULT ''`;
  - `credit_limit numeric(14,2) NULL CHECK (credit_limit >= 0)`, in NOK.
- **Anonymisation** keeps the limit and the override, which are business facts and not
  personal ones, and blanks the reason. Check against `cus/`'s erase list.
- **Merge:** the survivor keeps its own values. Neither is summed.
- Append to `cus/sqlc.yaml`.

### E11. Customers: the registry flag, the derived fact, the limit

- **`srv/brreg/entity.go`:**
  - `entityWire` (`:164`) gains `RegistrertIForetaksregisteret bool
    \`json:"registrertIForetaksregisteret"\``;
  - `Entity` (`:94-97`) gains `BusinessRegisterRegistered`, mapped beside
    `VATRegistered` (`:285`);
  - the fixture JSON in `srv/brreg/testdata` gains the field.
- **`cus/registry.go`:**
  - `registryRecord` (`:107-110`), `registryRecordFrom` (`:156`), `registryRecordFromRow`
    (`:284`) and `registryUpsertParams` (`:322`) carry it;
  - `cus/queries/registry.sql` (upsert `:47-66`) writes it;
  - `cus/registry_diff.go` (`:142`) records a change on the timeline;
  - the feed worker (`cus/registry_feed_worker.go:97`, `refresh` `:618`) fills it on its
    next pass, with no backfill job.
- **`bookkeepingObligated(record, override) (value *bool, source string)`** (new, in
  `cus/billing_values.go`):
  - the override if set, with source `override`;
  - else, with a registry record: `vat_registered OR business_register_registered`, with
    source `registry` (I37);
  - else NULL, with source `unknown`. A person with no record is unknown, never
    obligated.
- **The billing profile:**
  - `contracts.CustomerBillingProfile` (`srv/contracts/directory.go:90-160`) gains:
    - `BookkeepingObligated *bool`;
    - `BookkeepingObligatedSource string`;
    - `CreditLimit *string`, decimal text, NOK, as money crosses contracts
      (`srv/contracts/receivables.go:96`);
  - `cus/directory.go:193` fills them.
  - `cus/billing_profile.go`:
    - the response (`:54`) answers all three;
    - the PUT (`:284`, `customers:billing-manage`, `cus/server.go:150`) takes
      `creditLimit`, and `bookkeepingObligatedOverride` with its reason, which is
      required when the override is set;
    - `validateBillingProfile` (`cus/billing_values.go`) refuses `credit_limit_invalid`
      and `override_reason_required`.
- **`customers.credit_limit.changed`:**
  - a new `srv/contracts/events_customers.go`, in the pattern of
    `contracts/events_orders.go`;
  - published with `events.Publish(ctx, tx, "customers", …)` (`srv/events/events.go:80`)
    in `writeBillingProfile` (`cus/billing_profile.go:244`) when the limit changes;
  - this is **the first outbox event Customers publishes**. Its payload is customer id
    and limit, with no name.
  - The timeline's `recordCustomerBillingProfileUpdated` (`cus/timeline_events.go:271`)
    names the limit and the override.
- **Exposure on the customer page:** see E13.

### E12. Invoices: the 2028 rule at the issue

- **`invoiceIssueChecks`** (`inv/issue.go:190-199`), after `buyerComplete`:
  - `obligated := profile.BookkeepingObligated != nil && *profile.BookkeepingObligated`;
  - when `obligated && plan.buyer.BuyerOrganisationNumber == nil`:
    - from **2028-01-01**, judged on the issue date (`buyerOrgNumberFrom`, a `var` like
      `b2bDutyFrom`, `inv/send.go:54`), it refuses 409 `buyer_org_number_required`:
      "*The buyer is bookkeeping-obligated, and from 1 January 2028 its organisation
      number must be on the invoice (bokføringsforskriften § 5-1-2).*";
    - before that day, it adds the warning `buyer_org_number_missing` to the issue
      response's `Warnings` (`inv/issue.go:275-288`).
  - `buyerComplete` (`:159-170`) is unchanged. The address alternative stays for buyers
    that are not obligated, and the unknown ones (I37).
  - Credit notes take the same check, since a credit note is a sales document.
  - A billing run's issue (`inv/billing_worker.go:548`) fails the item with the code.
    That is today's path for any issue refusal.
- **Why the refusal is rare** (reading E8): `buyerSnapshot` (`inv/issue.go:125-148`)
  already sets the org number for any business with a Norwegian `LegalID`, and only such
  a customer has a registry record. The refusal bites on an override `true` without an
  org number, and on a business whose legal country is wrong.
- **The draft page** shows the same warning or refusal before issue, in the draft's issue
  checks, where `buyer_incomplete` is shown today.
- **Sending to the buyer** (§ 5-2-1 fourth paragraph, from 2028): the attention item of
  E4 gains "*issued and not delivered after 7 days*", counted from 2028-01-01. Send all
  (E-1) is the way to clear it. No refusal: the rule is about sending, and the module
  cannot know about a hand-over it was not told of (reading E9).

### E13. Credit limits: exposure, Orders' warning, Invoices' warning

- **The exposure (I38), one definition:**
  - Invoices' NOK `Outstanding`, from the existing slot `contracts.CustomerReceivables`
    (`srv/contracts/receivables.go:36-100`, `module.Deps.Receivables`,
    `srv/module/module.go:135-141`);
  - plus Orders' confirmed, uninvoiced orders in NOK, **gross**;
  - plus, at confirm, the order being confirmed.
  - Only NOK figures count. The contract forbids a total across currencies
    (`receivables.go:58-61`), and C's `open_nok` is not on this branch (reading E7).
  - Figures in other currencies are reported beside the NOK total, "*not counted*", and
    never added.
- **A new contract, `contracts.OrderBacklog`** (new `srv/contracts/order_backlog.go`),
  which Orders implements:
  - `UninvoicedForCustomer(ctx, customerID int32) ([]CurrencyAmount, error)`: confirmed
    orders' lines, `round(line_gross * (quantity - qty_cancelled - qty_invoiced) /
    quantity, 2)`, summed per currency. It is a new `ord/queries/orders.sql` query beside
    `OrderedNotDeliveredSums` (`:201`) and `DeliveredNotInvoicedSums` (`:219`), which are
    net and split by delivery, so neither can be reused;
  - registered as `Module.OrderBacklog` and set on `module.Deps.OrderBacklog` by compose
    (`srv/module/compose.go:332-337`, beside `Receivables`). It is nil when Orders is
    off.
  - Invoices and Customers read it. Orders reads its own query directly.
- **The arithmetic** is a pure function in a new platform package `srv/creditlimit`
  (no SQL, no module import, allowed by every depguard set as a platform package; confirm
  in `apps/server/.golangci.yml`):
  - `Exposure(receivables CustomerReceivableTotals, backlog []CurrencyAmount, extra
    CurrencyAmount) Result{NOK, Uncounted []CurrencyAmount}`;
  - `Exceeds(limit *string, r Result) bool`.
  - It is unit-tested on its own.
- **Orders, at confirm** (`ord/lifecycle.go:150`):
  - read the profile (already at `:171`) and `deps.Receivables` **before** the
    transaction, since `ord/contractscalls.go:35` refuses a contract call inside one;
  - inside `confirmTx` (`:69`), sum the backlog with the order's own query, excluding
    this order, and add the order's gross;
  - when it exceeds the limit, a computed warning `credit_limit_exceeded` at level
    `block` joins `documentWarnings` (`ord/warnings.go:61`) in `judgeWarnings` (`:122`).
    It has no row: its text is "*Credit limit NOK {limit}, exposure NOK {exposure}*",
    plus the uncounted currencies.
  - **The override is the warnings' own:** `overrideWarnings` + `overrideReason` and
    `orders:settings` (`ord/warnings.go:92-100`).
  - Without the override, the refusal is 409 with code **`credit_limit_exceeded`** when
    it is the only block, and `blocked_by_warning` when a row's block is there too
    (`ord/errors.go:51`). The change log's `warning_overridden` (`:165`) names it.
  - **When the figure cannot be read:**
    - Invoices disabled (`deps.Receivables == nil`): only the backlog counts, and the
      warning says so;
    - a read error: confirm warns "*credit could not be checked*" at level `info` and
      never refuses, because a provider outage must not stop sales (reading E7).
  - `GET` of an order or quote shows the warning at `info` level while it is a draft, so
    the person sees it before confirm.
- **Invoices, the draft and the issue:**
  - the warning `credit_limit_exceeded` (with figures) in the draft's response and the
    issue's `Warnings`. **It never refuses** (I38).
  - Invoices' own NOK outstanding is read in Invoices, plus `deps.OrderBacklog` before the
    transaction, plus the draft's gross.
- **Customers, the customer page:**
  - `cus/overview.go:138` already reads `deps.Receivables`. It adds `deps.OrderBacklog`
    and answers `credit: {limit, exposureNok, uncounted[], exceeded}` when the caller may
    see invoices (`invoicesAccess`, as today) and a limit is set.
- **OpenAPI:**
  - `openapi/customers.yaml`: the billing profile (`:365`, PUT `:1822`) and the overview;
  - `openapi/orders.yaml`: `OrdersProblem` codes (`:2373`), with `credit_limit_exceeded`
    and the 409 description (`:6353`);
  - `openapi/invoices.yaml`: the codes `buyer_org_number_required`,
    `buyer_org_number_missing` and `credit_limit_exceeded`.
- **Frontend:**
  - Customers, `apps/customers/frontend/src/pages/-customer-billing-modal.tsx` and
    `-customer-billing-card.tsx`:
    - **Credit limit (NOK)**;
    - **Bookkeeping-obligated**, as *From the register: yes/no/unknown*, with an
      override and its reason;
    - the card shows limit, exposure and the uncounted currencies;
  - `-customer-registry-fields.tsx`: *Registered in the Register of Business
    Enterprises*;
  - Orders: `components/warning-banners.tsx:28` shows the computed warning, and
    `pages/order-editor.tsx:233` confirms with override as today;
  - Invoices: the draft and issue warnings and the 2028 refusal, in words, in
    `fe/i18n.ts`;
  - i18n in `en` and `nb` in all three apps.

### E14. Tests

- `srv/creditlimit`: the arithmetic, only NOK counted, the uncounted list, the boundary
  (equal is not exceeding), and a nil limit.
- Customers:
  - the registry flag parsed and diffed;
  - the derived fact's table (record and no record, override true and false);
  - the PUT's refusals;
  - the event published once on a change and not on an unchanged PUT;
  - anonymisation.
- Orders, against Postgres:
  - confirm refused `credit_limit_exceeded`;
  - overridden with reason and `orders:settings`;
  - refused override without the permission;
  - Receivables nil;
  - a Receivables fake that errors → info, confirmed;
  - a EUR order uncounted.
- Invoices:
  - the issue before and after `buyerOrgNumberFrom` (set the `var` in the test, as the
    B2B tests do `b2bDutyFrom`);
  - a credit note;
  - a billing run item failing with the code;
  - the credit warning on draft and issue;
  - the attention item after 2028.
- `contracts.OrderBacklog`: Orders' query against fixtures with cancelled, partly
  invoiced and kit lines. Kit children are excluded as at `ord/queries/orders.sql:219`.
- The integration test `srv/integration/assistant_test.go` `everyModule()` needs no
  change. Customers + Orders + Invoices with Invoices off proves the nil slot.

### E15. Seed, documentation, assistant and site

- **Seed:**
  - `seedCustomersAndContacts` (`seed.go:803`, INSERT `:916`): a credit limit on two
    business customers, one of them over its limit with the seed's open invoices;
  - one customer's override `false` with a reason;
  - the registry seed rows gain `business_register_registered`.
  - `seed_test.go` counts stay.
  - `en/admin/installation.md:115` names the credit limit.
- **`en/user/customers.md` and `nb/user/customers.md`:**
  - *Billing* (`:203`): the credit limit, the exposure and what is not counted;
  - *The registry card* (`:187`): the Register of Business Enterprises, and
    *bookkeeping-obligated*, its override, and why it matters from 2028.
- **`en/reference/customers.md`:**
  - the billing profile (`:524`) and its contract (`:3189`);
  - the registry record (`:2135`) and its workers (`:2482`);
  - the event, the codes, and the tour targets (`:3811`).
- **`en/user/orders.md` and `nb/user/orders.md`:** *Warnings on a customer or a product*
  (`:137`) gains the credit-limit warning and its override.
- **`en/reference/orders.md`:** *Sales warnings* (`:1577`), *Refusals* (`:1672`), and
  `OrderBacklog`.
- **`en/user/invoices.md` and `nb/user/invoices.md`:**
  - the issue's checks: the 2028 refusal and the earlier warning;
  - the credit warning;
  - "issued and not delivered".
- **`en/reference/invoices.md`:** the issue's checks, with § 5-1-2 as amended and its
  source; the codes; and the attention item.
- **Assistant:**
  - targets `customers.billing.credit-limit`, `customers.billing.bookkeeping-obligated`
    and `orders.confirm.credit-override`;
  - `promptVersion` bumped again, and `en/reference/assistant.md:309`;
  - `go generate ./...`.
- **`ROADMAP.md`:** E-2 done. The Invoices *Later* item on the 2028 rule is removed.
- **The public site:**
  - Customers' entry: *Credit limits, with the customer's exposure from open invoices and
    orders.*
  - Orders' entry: *Warns at confirm when a customer is over its credit limit.*
  - The 2028 rule is compliance, not something a customer is sold on, so it gets no
    line.

### E16. Gates and review

- **Gates:** as in E9, with the customers and orders packages as well.
- **Commit:** `feat(customers,orders,invoices): credit limits and the 2028 buyer rule`.
- **The independent review** checks, in particular:
  - the contract calls outside transactions in Orders' confirm;
  - that no total across currencies is ever built;
  - the 2028 boundary judged on the issue date;
  - anonymisation of the override reason;
  - Customers' first outbox event, its payload and its transaction;
  - the depguard sets for `srv/creditlimit` and `OrderBacklog`.

---

## E: readings against the code

- **E1 — The 2028 rule, its exact wording.**
  - **The source:** *Forskrift om endring i bokføringsforskriften*,
    FOR-2026-09-29-1933, adopted on 29 September 2026
    (<https://lovdata.no/dokument/LTI/forskrift/2026-09-29-1933>). Skatteetaten
    summarises it at
    <https://www.skatteetaten.no/en/rettskilder/type/uttalelser/uttalelser/forskrifter-til-lovendringer-om-digital-bokforing-og-e-fakturering/>.
    Both were read on 2026-11-18.
  - **§ 5-1-2 first paragraph, first sentence, in force at once:** «Angivelse av kjøper,
    jf. § 5-1-1 nr. 2, skal minst inneholde kjøpers navn, og adresse eller
    organisasjonsnummer som er tildelt i henhold til lov 20. juni 2025 nr. 105 om
    Enhetsregisteret § 6-2.»
  - **The second and third sentences, from 1 January 2028:** «Ved salg til
    bokføringspliktig kjøper skal kjøpers organisasjonsnummer alltid angis. Dersom kjøper
    som nevnt i § 5-1-1 nr. 7 er registrert i Merverdiavgiftsregisteret, skal
    organisasjonsnummer etterfølges av bokstavene MVA.»
  - They replace today's «Ved salg som nevnt i § 5-1-1 nr. 7 skal kjøpers
    organisasjonsnummer alltid angis». § 5-1-1 nr. 7 is reverse charge.
    `reverse_charge_needs_org_number` (`inv/issue.go:54`) therefore stays, and is now a
    special case of the new rule. The MVA suffix is unchanged.
  - **§ 5-2-1 fourth paragraph, from 1 January 2028:** «Salgsdokument skal oversendes
    bokføringspliktig kjøper. Det samme gjelder salgsdokument til andre kjøpere med
    mindre annet fremgår av skriftlig, undertegnet avtale mellom partene eller er bestemt
    i lov eller forskrift.»
  - So "always sent to the buyer" is § 5-2-1, not § 5-1-2. The research §5 cites
    § 5-1-2 for both, and the spec I37 names only the first.
- **E2 — Idempotent per document, not per channel.** I36 says "per document and
  channel". But a document e-mailed by hand and then sent as EHF by send all is delivered
  twice, which is a duplicate a buyer can pay twice. Send all therefore skips any
  document with any live delivery (`already_delivered`). A person who wants a second
  channel sends it from the document, as today.
- **E3 — The channel is chosen when the job runs, not at enqueue.** The eFaktura lookup
  and the Peppol lookup leave the process and may be slow. Five hundred of them in the
  request would break rule 10's spirit and the request's time. So `channel` is NULL until
  claimed.
- **E4 — At most once for e-mail.** SMTP has no idempotency key, and the delivery row is
  written after the send (`send.go:372-392`). A worker that dies between the two would,
  on reclaim, mail twice. A reclaimed e-mail job with no delivery after `started_at` is
  therefore `uncertain`: listed, and shown on attention, and never retried by itself. EHF
  and eFaktura are safe to retry, since their cores check the active transmission under
  the lock.
- **E5 — The 2027 duty beats I35's "else e-mail".** I35 says a business without EHF gets
  e-mail. From 2027-01-01 that is not a lawful e-invoice to a Norwegian business
  (`send.go:50-54`). The single send warns and lets a person decide, but a bulk action
  should not decide for them. The job is `skipped` with
  `buyer_norwegian_business_required`.
- **E6 — No per-document preview before Send all.** Choosing every channel up front needs
  the lookups (E3). The result page is the preview after the fact, and a refusal never
  sends anything wrong. Revisit if people ask.
- **E7 — NOK only, until C.** `open_nok` and `document_nok` are not on this branch.
  `CustomerReceivables` is per currency and forbids a cross-currency total
  (`receivables.go:58-61`), and Orders' sums are in the document's currency
  (`mig/00119_orders_baseline.sql:194,203`). So the exposure counts NOK and lists the
  rest as uncounted.
  - **C follow-up:** when C lands, `CustomerReceivableTotals` gains
    `OutstandingNOK`, the sum of `open_nok`, and Orders' backlog converts at C's rate.
    `srv/creditlimit.Exposure` is then the one place that changes, and the uncounted list
    empties.
  - A provider error warns and never refuses in Orders, because I38 makes the limit a
    warning everywhere but confirm, and an outage must not stop sales.
- **E8 — Org number from the registry, not a new field.** The snapshot already takes the
  org number from `LegalID` for a Norwegian business (`issue.go:136-144`), and
  `bookkeeping_obligated` comes only from a registry record, which only such a customer
  has. The refusal therefore guards the override and broken data. No new buyer field is
  needed.
- **E9 — § 5-2-1 is an attention item, not a refusal.** The module cannot see a document
  handed over outside it, unless it is recorded as a manual delivery. Refusing would block
  nothing useful, so attention plus send all is the remedy.
- **E10 — The spec's revision.** Spec §15 gains revision 6, "delivery E's plan amends
  I35–I38", listing E2 to E9. Do it in E-1's PR, with I37's § 5-2-1 sentence added.
- **E11 — Foretaksregisteret is not fetched today.** `srv/brreg/entity.go` reads
  `registrertIMvaregisteret` (`:172`) but not `registrertIForetaksregisteret`. E-2 adds it
  (E11). Until the feed worker has refreshed a record, the flag is NULL and the VAT flag
  alone decides. The derived fact says `registry` and stays honest.
- **E12 — Customers publishes no outbox event today.** It writes timeline events only
  (`cus/timeline_events.go:77`). `customers.credit_limit.changed` is its first
  `events.Publish`. The platform outbox needs no per-module table, but check that
  Customers' transaction helper exposes the `pgx.Tx` `Publish` needs.
- **E13 — The print fallback is new.** No path puts an invoice into a print batch today,
  so E-1 adds the ZIP and *Mark as posted* (E4) rather than widen reminder print batches,
  whose rows, PDFs and lock order (`print_batches.go:39-44`) are letters'.
