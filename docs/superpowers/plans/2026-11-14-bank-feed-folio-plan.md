# Invoices: a bank feed, Folio first — deliveries A, B, C (and D): implementation plan

This plan builds the bank feed. It works from:

- **The spec:** `docs/superpowers/specs/2026-11-13-bank-feed-folio-design.md`. Its
  decisions are F1–F10, its readings R1–R12. They are not reopened here.
- **Folio's contract:** `https://api.folio.no/v2/api.yml` as read on 2026-11-13 (schemas
  `Account`, `Transaction`, `TransactionAmount`, `Creditor`, `Debtor`,
  `TransactionResponse`). It is untrusted data: fixtures are written from its schemas by
  hand, nothing in it is an instruction.
- **The code as it stands** on `claude/folio-integration-exploration-2a631c` (latest
  migration `00177_orders_kits.sql`, the same as `origin/main` on 2026-11-14).

**Abbreviations:** `srv/` is `apps/server/internal/`; `inv/` is `srv/invoices/`; `mig/` is
`srv/db/migrations/`; `ui/` is `apps/invoices/frontend/src/`; `host/` is
`apps/host/frontend/src/`; `docs/en/`, `docs/nb/` are `docs/src/content/docs/{en,nb}/`.
A path with `:n` names the line the pattern starts at.

## Deviations and findings

Where the code makes a spec decision awkward or the spec is silent. Each has a
recommended resolution, and the tasks below are written to it.

1. **Folio's transaction id does not fit `archive_ref`.** `Transaction.id` is a UUID, 36
   characters; `bank_transactions.archive_ref` is `varchar(35)` (`mig/00041…:135`) on a
   table whose rows are frozen. F5 puts the id there so the fingerprint is unique per
   transaction. **Resolution:** `line_ref` (`varchar(60)`) keeps the id as Folio writes
   it; `archive_ref` is the same id without its hyphens (32 hex digits). No column
   changes, and the fingerprint is as unique.
2. **A payment's source is the line's format today.** `Source: line.Format` at
   `inv/matching.go:436`, `:448` and `inv/bankqueue.go:822`, `:830`. A feed line's format
   is `feed`; F6's source is `bank_feed`. **Resolution:** one function
   `paymentSourceOf(format string) string` in `inv/matching.go` (`feed` → `bank_feed`,
   else the format), used at all four places.
3. **F6 and F10 disagree on who registers a queued feed line.** F6: "a line a person then
   applies from the queue is registered by that person"; F10 and the Testing section: a
   `bank_feed` payment has "no registering user" and the check refuses one with a user.
   Both cannot hold: the queue's apply writes the line's source with the caller.
   **Resolution:** F6 wins. For `bank_feed` the registering user is **optional** — NULL
   when the feed matched it, set when a person applied it from the queue or ran
   `POST /bank-files/{id}/match` on a feed batch. The checks still require a bank line and
   no payment attempt for `bank_feed`, and still require a user for `ocr` and `camt054`.
   The spec's test "refuse a `bank_feed` payment with a user" is replaced by "refuse a
   `bank_feed` payment without a bank line".
4. **F10 names the wrong constraint and misses three columns.** The user rule is in
   `ck_payments_origin` (`mig/00046…:361`), not `ck_payments_source`.
   `charge_payments.registered_by_user_id` is `NOT NULL` (`mig/00041…:500`) and
   `ck_charge_payments_origin` (`:507`) has no user rule.
   `charge_waivers.waived_by_user_id` is `NOT NULL` (`:919`), and F6's `deadline_met`
   waiver by no user needs it nullable; F10 does not list it. **Resolution:** the
   migration replaces both `…_source` and both `…_origin` checks, drops the two
   `NOT NULL`s, and adds `ck_charge_waivers_by` (`waived_by_user_id IS NOT NULL OR reason =
   'deadline_met'`).
5. **"Plain strings, not enums" is true of `format` only.** In `openapi/invoices.yaml` a
   payment's `source` (`:3614`) and a charge payment's are enums, as are the event name
   and the attention item type (`:5050`). **Resolution:** `bank_feed` is added to both
   source enums and the attention types to theirs; `format` and the new `provider` and
   statuses stay plain strings. Four fields required today become optional (absent, the
   contract's style, not `nullable: true`): `InvoicesBankFile.uploadedBy`,
   `InvoicesBankTransactionEvent.by`, `InvoicesChargePayment.registeredByUserId`,
   `InvoicesChargeWaiver.waivedBy`. `InvoicesBankIgnored` gains `internalTransfer`.
6. **Vipps is not the template for verify-on-save or the last four.**
   `PUT /settings/vipps` stores without calling Vipps (`inv/vipps_settings.go:243`,
   `:282`); verification is its own operation (`:413`) and the screen shows the MSN.
   **Resolution:** `PUT /invoices/bank-feed` calls `Verify` **before** and outside its
   transaction and maps the outcome as `PostInvoicesSettingsVippsVerify` does (`:427`);
   the last four characters are their own plain column, written at the seal.
7. **The Integrations page's template is Shipping, not Vipps.** Vipps' settings are a
   card in the Invoices app (`ui/pages/-vipps-settings.tsx`). The one module page under
   Workspace admin → Integrations is Orders' Shipping: the page lives in the module's UI
   package and the host route only gates and mounts it
   (`host/routes/workspace/shipping.tsx`). **Resolution:** the same shape —
   `ui/pages/bank-feed-settings.tsx`, `host/routes/workspace/bank.tsx`.
8. **`GET /invoices/bank-feed` behind `invoices:payments` locks out a manager.** Access
   rules are conjunctions only (`permission:a+b`, `srv/openapi/lint.go:13`), so F8's rule
   becomes `invoices:access+invoices:payments`, and a person with `invoices:manage` but
   not `invoices:payments` could save a key and never see the page. **Resolution:** the
   GET is `permission:invoices:access` — it answers no secret, as `GET /settings/payments`
   does — and `capabilities.canManageBankFeed` and `canImportBankFiles` decide what each
   screen shows. Every write keeps F8's permission.
9. **The format control must know `feed`.** `PUT /bank-accounts/{account}/format`
   accepts `ocr` and `camt054` only (`inv/bankaccounts.go:46`), and the spec does not say
   what a change away from `feed` does while the feed is on. **Resolution:** `feed` is
   never accepted by the format PUT — only the switch sets it; a change away from `feed`
   is refused `409 bank_feed_enabled` while the account's feed is on, and allowed once it
   is off (the existing cutover then holds back file lines the feed already registered).
   A cycle that finds its account's format is not `feed` stores nothing and marks the
   account `refused`, reason `format_mismatch`.
10. **A sync cannot use the import's account lock as it is.** `lockImportAccounts`
    (`inv/locks.go:71`) upserts the account with a user (`set_by_user_id NOT NULL`).
    **Resolution:** the switch (a person) creates or changes the row; the sync only reads
    it `FOR SHARE` through a new helper `shareImportAccounts`.
11. **Direction needs account normalising, and its fields are optional.** Folio's
    `creditor.accountNumber` is "BBAN or IBAN" and both parties and their fields may be
    absent. The adapter cannot import `invoices/bankfile` (a platform package imports no
    module). **Resolution:** the adapter normalises on its own — spaces removed, `NO` +
    2 digits + 11 digits read as the 11 — before comparing; neither or both matching is
    `unknown`, which refuses the batch (F5 step 2). One transaction Folio writes
    differently therefore stops an account until the adapter changes; see the open items.
12. **Field lengths.** `kid` is `varchar(25)` and Folio's is unbounded. **Resolution:**
    the camt.054 rule (`inv/bankfile/camt054.go:781`): a KID longer than 25 is not a KID
    — the line is stored without one and queued `no_kid`. `description` is cut to 1 000
    characters and the debtor's name to 140, on rune boundaries; a debtor account longer
    than 34 is stored empty.
13. **`PUT /invoices/bank-feed` carries a secret past the assistant.** No Invoices
    operation sets `x-vantigo-assistant` today. **Resolution:** this one is marked
    `never`, so an API key is never typed into a conversation. (Vipps' and the access
    point's PUTs have the same exposure and are left for the owner.)
14. **The docs lists of B and C are short.** `INVOICES_VIPPS_*` are also documented in
    `admin/authentication.md` (the table of every variable, `:1397`) and
    `admin/installation.md` (`:722`, and the list of workers at `:285`), and a stored
    batch is an object in the object store (`admin/object-storage.md`). The host's
    attention files (`host/lib/dashboard-helpers.ts`, `host/catalogs/dashboard.ts`) are
    sources of `user/staff.md` alone, so changing them fails `docs:check` unless that
    page changes. **Resolution:** the pages are added to each delivery below, and C adds
    the two host files to `user/invoices.md`'s `sources`.
15. **A has no recorded response.** The spec's first task of A is replaced by the
    decision already made: fixtures by hand from the schemas, the fail-closed `unknown`,
    and "verify against a real account" as an open item below.
16. **`INVOICES_BANK_FEED_INTERVAL` is a third instance setting** (F5), beside F3's two.
    It lands in C with the worker that reads it.

## Open items (not blocking)

- **Verify against a real Folio account** with an incoming payment: that
  `creditor.accountNumber` is the account asked for on a credit (R4), in which form
  (BBAN or IBAN), and whether `kid` arrives on a payment made with one (R8). Until then
  the hand-written fixtures are the only evidence; a mismatch shows as accounts going
  `refused` with reason `direction_unknown`, never as a wrong payment.
- The spec's questions 5–9 for Folio, and 1–3 for the owner.
- A browser check of the Integrations page and *Fetch now*.

## How it lands: one stack

Stacked PRs (memory: stacked-prs-per-phase), `gh stack`, non-interactive flags only.

| # | Branch | Delivery | Migration |
| --- | --- | --- | --- |
| 1 | `claude/folio-integration-exploration-2a631c` | The spec and this plan (docs only, no `Docs-Impact` trailer) | — |
| 2 | `claude/bank-feed-a-port` | A: `internal/bankfeed`, `bankfeed/folio`, `foliotest`, purity, depguard | — |
| 3 | `claude/bank-feed-b-connection` | B: the schema, the two settings, the connection, the accounts, the Integrations page | `00178_invoices_bank_feed.sql` |
| 4 | `claude/bank-feed-c-sync` | C: the worker, the batch, matching by no user, attention, *Fetch now* | — |

**The migration number.** `00178` is the next free number today. Right before merging B,
run `git fetch && git ls-tree --name-only origin/main apps/server/internal/db/migrations/ | tail -3`;
if `main` took `00178`, renumber the file **in B's own history**, change its name in
`inv/sqlc.yaml` and in the test's version constant, then rebase (memories:
migration-number-collisions, prs-land-by-rebase).

## How each delivery is run

The routine of the earlier stacks.

1. One executor per layer; it writes code, frontend and docs itself.
2. **The seam first**, one compiling commit: the migration, the contract, `go generate`,
   the skeleton handlers.
3. **Codegen.** After any change to `openapi/*.yaml`, a migration, `inv/queries/*.sql` or
   a docs page under `user/` or `admin/`: `cd apps/server && mise exec -- go generate ./...`
   (it copies the contracts to `srv/openapi/specs`, runs oapi-codegen into `inv/gen`,
   sqlc from `inv/sqlc.yaml` into `inv/store`, the assistant's embedded docs and the
   assist targets; `apps/server/generate.go`). Then `bun run gen:client` for every
   `api-schema.d.ts`. CI fails when either is stale (`.github/workflows/ci.yml:121`,
   `:366`). The regenerated assistant docs are their own commit, `chore(assistant):
   regenerate the embedded documentation`.
4. **Docs**, English first, then Norwegian bokmål page for page for `user/` and `admin/`.
   Check coverage against the layer below: `bun run tools/docs/check-coverage.ts --base
   <parent branch>`.
5. **Review:** an opus code review of the layer's diff; B and C also get an opus
   security review (a bank credential, an outbound call, money registered by nobody).
6. **Gates**, in order: `bun install --frozen-lockfile` once (memory:
   worktree-needs-bun-install); `mise run server:check`; `mise run server:test` (on
   SQLSTATE 53300 rerun the failing packages with `-p 1`); `mise run frontend:check`
   (read the log, the wrapper exits 0); `mise run docs:check`.
7. **Commits:** signed Conventional Commits — `feat(bankfeed)`, `feat(invoices)`,
   `feat(invoices-ui)`, `feat(host)`, `docs(invoices)`, `docs(contributing)`,
   `chore(assistant)`.

---

## Delivery A — the port and Folio's adapter (F1)

No behaviour a user can notice; nothing imports the packages yet.

### A1. The port — `srv/bankfeed/`

Copy the shape of `srv/payments/` (`payments.go`, `errors.go`, `money.go`).

- `bankfeed.go`: the package comment (a platform package, no SQL, no module, the standard
  library and `internal/buildinfo` only); `Feed` exactly as F1 writes it; `Account{Number,
  Name string; Kind AccountKind}` with kinds `operational`, `tax`, `savings`, `earmarks`,
  `card`, `other`; `Direction` (`credit`, `debit`, `unknown`); `Kind` (`transfer`,
  `card`, `internal`); `Transaction{ID string; BookedOn time.Time (UTC midnight);
  AmountMinor int64; Currency string; Direction; Kind; KID, Text, DebtorName,
  DebtorAccount string; Raw json.RawMessage}`.
- `errors.go`: `ErrUnauthorized` (a sentinel, as `payments/errors.go:16`) and
  `ErrRateLimited{RetryAfter time.Duration}` (as `payments.ErrThrottled`, `:37`).
  Anything else is a plain error.
- Tests: `errors_test.go` (`errors.Is`, `errors.AsType`, the message with and without a
  wait), as `payments/errors_test.go`.

### A2. The adapter — `srv/bankfeed/folio/`

Copy the client's construction from `srv/payments/vipps/vipps.go:60–118`.

- `folio.go`: `Options{BaseURL, APIKey string; Transport http.RoundTripper; Now func()
  time.Time}`; `New` refuses a base URL that is not absolute http(s) or an empty key;
  `http.Client` with `CheckRedirect` answering `http.ErrUseLastResponse` (`vipps.go:112`);
  one 30-second deadline per method call; every answer read through
  `io.LimitReader(body, 10<<20+1)` and refused past 10 MiB; `Authorization: Bearer`;
  `User-Agent: vantigo/<buildinfo.Version>`. No retry, no token cache.
- `Verify` → `GET /accounts`, the body discarded.
- `Accounts` → `GET /accounts`, `accounts[]` mapped (`type` `Card` → `card`, an unknown
  type → `other`).
- `Transactions(account, from, to)` → `GET /accounts/{accountNumber}/transactions?
  startDate=&endDate=` (both `2006-01-02`); `transactions.booked[]` decoded twice per
  element: once into the struct, once kept as `json.RawMessage` for `Raw` (decode the
  array as `[]json.RawMessage` first). Mapping per F1 and findings 1, 11, 12:
  - amount: `transactionAmount.amount` must match `^\d+\.\d{2}$`, parsed to øre without a
    float; anything else is a plain error for the whole call (the module refuses the
    batch); `currencyAmount` is never read;
  - direction: `credit` when the normalised `creditor.accountNumber` equals the
    normalised account asked for and the debtor's does not; `debit` the other way;
    `unknown` when neither or both;
  - kind: `internal` when `creditorEventId` is present, else `card` when
    `cardAuthorization` is, else `transfer`;
  - `BookedOn` from `bookingDate`; `KID` from `kid` trimmed; `Text` from `description`;
    the debtor's name and account as written.
  The adapter cuts nothing: lengths are the module's (finding 12).
- Status mapping: 401 and 403 → `bankfeed.ErrUnauthorized`; 429 →
  `*bankfeed.ErrRateLimited` with `Retry-After` (seconds or an HTTP date, against `Now`);
  a 3xx, any other status, a transport error, a timeout, a body that does not decode →
  a plain error naming the status and never the key.
- `folio/testdata/`: `accounts.json`, `transactions.json` written by hand from the
  schemas — one incoming transfer with a KID, one without, one outgoing, one card
  purchase, one internal transfer (both sides), one with an IBAN creditor, one with no
  creditor account. A comment in each says it is hand-written, not recorded.

### A3. The fake — `srv/bankfeed/folio/foliotest/`

Copy `srv/payments/vipps/vippstest/server.go:183–300`: an ordinary package (not a
`_test` file), `New(t testing.TB) *Server` on an `httptest` TLS listener, `URL()`,
`Transport()`, `APIKey` constant, `SetAccounts(...)`, `AddTransaction(account, tx)`,
`Requests()`, and `Fail(op Op, f Failure)` with **`FailOn`** semantics per operation
(memory: google-fake-failnext-token-cache) — a status, a `Retry-After`, a redirect, a
reset connection, an oversized body. It serves `GET /accounts` and
`GET /accounts/{n}/transactions` with the date filter applied, and 401 for another key.

### A4. Tests

- `srv/bankfeed/purity_test.go`, a copy of `srv/payments/purity_test.go`: dirs `.`,
  `folio`, `folio/foliotest`; allowed: the standard library, `internal/buildinfo`,
  `internal/bankfeed` and below. No third-party exception.
- `srv/bankfeed/folio/folio_test.go` against the fake and the fixtures: every field of
  R3; each direction and kind, BBAN and IBAN; 401, 403, 429 with and without
  `Retry-After`, 500, a reset; a body of 10 MiB + 1; a redirect not followed (the fake
  records no second request); an amount with three decimals; the key absent from every
  error string.

### A5. Registries

- `apps/server/.golangci.yml`, two sets, nothing else (a module's own set only denies
  other modules):
  - `test-fakes` (`:1789`): add `…/internal/bankfeed/folio/foliotest` with the desc
    "foliotest is a test fake: only _test.go files may import it";
  - `platform` `files` (`:1845`): add `"**/internal/bankfeed/**"`, in alphabetical place
    after `apicommon`.
- No env var, no module registry, no migration in A.

### A6. Documentation (English only — `contributing/`)

- `docs/en/contributing/platform-packages.md`: add `apps/server/internal/bankfeed` to
  `sources`; a new section **The bank feed port** after *The payments port* (`:323`):
  the interface, direction and kind and who decides them, the two typed errors and "an
  adapter never retries", the fake.
- New `docs/en/contributing/bank-feeds.md` (*Adding a bank*), `sources:
  [apps/server/internal/bankfeed]`, `sidebar.order` after Shipping carriers' 6 — written
  on the model of `contributing/shipping-carriers.md`: the port, Folio's mapping table,
  the fake, the hand-written fixtures and the open verification, and the checklist for a
  second bank (adapter, fake, purity dirs, depguard `test-fakes`, the provider value in
  the schema's check and the contract's description).
- `docs/en/contributing/index.md` (`:31`): list *Bank feeds* after *Shipping carriers*.
- `docs/en/contributing/module-boundaries.md` (`:64–79`, rule 1's list of platform
  packages): add `internal/bankfeed` with `internal/bankfeed/folio`, and `foliotest`
  to the sentence about fakes. This page is `.golangci.yml`'s source, so A5 needs it.

### A7. Gates

`mise run server:check`, `mise run server:test`, `mise run docs:check`. No frontend
change; run `mise run frontend:check` once anyway for the pre-commit hook.

---

## Delivery B — the connection (F3, F4, F8 but the sync, F9's admin page, F10)

### B1. The seam

**The migration — `mig/00178_invoices_bank_feed.sql`** (schema `invoices` only). Up:

- `invoices.bank_feed_connections`: the single-row shape of
  `payment_provider_credentials` (`mig/00046…:31`) — `id integer PRIMARY KEY CHECK (id =
  1)`, `provider varchar(20)` with `CHECK (provider IN ('folio'))`, `secret_ciphertext
  text NOT NULL`, `key_last4 varchar(4) NOT NULL`, `status varchar(12) NOT NULL DEFAULT
  'ok'` with `CHECK (status IN ('ok', 'unauthorized'))`, `rejected_at timestamptz`,
  `connected_by_user_id uuid NOT NULL`, `connected_at timestamptz NOT NULL`,
  `updated_at timestamptz NOT NULL`.
- `invoices.bank_feed_accounts`: `account varchar(11) PRIMARY KEY`, `enabled boolean NOT
  NULL`, `start_day date NOT NULL`, `status varchar(12) NOT NULL DEFAULT 'ok'` with
  `CHECK (status IN ('ok', 'failing', 'refused', 'unauthorized'))`, `reason varchar(40)`,
  `detail varchar(500) NOT NULL DEFAULT ''`, `failures integer NOT NULL DEFAULT 0`,
  `last_synced_at timestamptz`, `last_bank_file_id bigint REFERENCES
  invoices.bank_files (id)`, `next_sync_at timestamptz`, `lease_id varchar(32)`,
  `lease_until timestamptz`, `enabled_by_user_id uuid NOT NULL`, `updated_at timestamptz
  NOT NULL`; `CHECK ((lease_id IS NULL) = (lease_until IS NULL))`; `CREATE INDEX
  ix_bank_feed_accounts_due ON invoices.bank_feed_accounts (next_sync_at) WHERE enabled`.
  Neither table is immutable; neither gets a trigger.
- `bank_files`: `ADD COLUMN provider varchar(20)`; `ALTER COLUMN uploaded_by_user_id DROP
  NOT NULL`; replace `ck_bank_files_format` with `('ocr', 'camt054', 'feed')`; add
  `ck_bank_files_origin CHECK ((format = 'feed') = (uploaded_by_user_id IS NULL) AND
  (format = 'feed') = (provider IS NOT NULL))`. `tr_bank_files_immutable` compares the
  row as jsonb and needs no change.
- `bank_transactions`: replace `ck_bank_transactions_format` likewise.
- `bank_import_accounts`: replace `ck_bank_import_accounts_format` and
  `…_previous_format` likewise.
- `payments`: replace `ck_payments_source` (add `bank_feed`) and `ck_payments_origin`
  (`mig/00046…:361`) with a fourth branch: `source = 'bank_feed' AND bank_transaction_id
  IS NOT NULL AND payment_attempt_id IS NULL` — the user either (finding 3).
- `charge_payments`: `ALTER COLUMN registered_by_user_id DROP NOT NULL`; replace
  `ck_charge_payments_source` (add `bank_feed`) and `ck_charge_payments_origin` with
  `(source = 'manual') = (bank_transaction_id IS NULL) AND (registered_by_user_id IS NOT
  NULL OR source = 'bank_feed')`.
- `charge_waivers`: `ALTER COLUMN waived_by_user_id DROP NOT NULL`; add
  `ck_charge_waivers_by` (finding 4).
- `bank_transaction_events`: `ALTER COLUMN by_user_id DROP NOT NULL`.

Down: the reverse, as `mig/00046…:469–536` does it — drop the two tables, restore every
check and `NOT NULL`, drop `provider`. It fails on a database that holds feed rows, as
00046's fails on one that holds Vipps payments; say so in the file's comment.

**Wire the migration:** add it to `inv/sqlc.yaml`'s `schema:` list
(`srv/db/schema_test.go:215` fails otherwise) and the two table names to the list at
`srv/db/schema_test.go:2476`.

**The contract — `openapi/invoices.yaml`.** Model the operations on
`/api/v1/invoices/settings/vipps` (`:6047–6151`): each with `operationId`, `summary`,
`tags: [Invoices]`, `x-vantigo-access`, the 401 and 403 answers.

| Operation | `operationId` | Access | Answers |
| --- | --- | --- | --- |
| `GET /invoices/bank-feed` | `getInvoicesBankFeed` | `permission:invoices:access` (finding 8) | 200 `InvoicesBankFeed` |
| `PUT /invoices/bank-feed` | `putInvoicesBankFeed` | `…access+invoices:manage`; `x-vantigo-assistant: never` | 200; 400; 409 `bank_feed_disabled`; 422 `bank_feed_credential_rejected`; 503 when the bank cannot be reached |
| `DELETE /invoices/bank-feed` | `deleteInvoicesBankFeed` | `…access+invoices:manage` | 204 |
| `GET /invoices/bank-feed/accounts` | `getInvoicesBankFeedAccounts` | `…access+invoices:manage` | 200 `InvoicesBankFeedBankAccountsResponse`; 409 `bank_feed_disabled`, `bank_feed_not_connected`; 422 `bank_feed_credential_rejected`; 503 |
| `PUT /invoices/bank-feed/accounts/{account}` | `putInvoicesBankFeedAccountsByAccount` | `…access+invoices:manage` | 200 `InvoicesBankFeedAccount`; 400; 409 `bank_account_unknown`, `bank_feed_not_connected`, `bank_feed_account_not_offered` |

Schemas, statuses and `provider` as plain strings: `InvoicesBankFeed{connected,
enabledOnInstance, provider?, keyLastFour?, status?, rejectedAt?, connectedBy?,
connectedAt?, accounts: InvoicesBankFeedAccount[]}`; `InvoicesBankFeedAccount{account,
enabled, startDay, status, reason?, detail?, lastSyncedAt?, lastBankFileId?,
nextSyncAt?}`; `InvoicesBankFeedRequest{provider, apiKey}`;
`InvoicesBankFeedBankAccount{account, name, kind, canEnable, whyNot?}` (`whyNot`:
`card`, `not_sellers_account`); `InvoicesBankFeedAccountRequest{enabled, startDay?}`.
Also in B: `capabilities.canManageBankFeed` (required, `:3`);
`InvoicesBankAccountFormatRequest`'s and `InvoicesBankAccount`'s descriptions name
`feed`; the format PUT gains 409 `bank_feed_enabled`. `POST /invoices/bank-feed/sync`
and the changes to files, lines and payments are C's.

Then `go generate ./...` and `bun run gen:client`.

### B2. The instance settings — `srv/config/config.go`

Copy `invoicesVipps` (`:1160–1171`) and its fields (`:355–367`):

- `InvoicesBankFeedEnabled bool` — `boolean(p, env, "INVOICES_BANK_FEED_ENABLED", true)`;
- `InvoicesFolioBaseURL string` — `httpBaseURL(p, env, "INVOICES_FOLIO_BASE_URL",
  "https://api.folio.no/v2")`; call the new `invoicesBankFeed` beside `invoicesVipps`
  (`:694`).
- Tests: copy the Vipps test (`srv/config/config_test.go:2045–2070`).
- `deploy/compose/vantigo.env.example` (`:281–288`): the two variables, commented, with
  the same kind of sentence.

### B3. The server — `inv/`

New `inv/bank_feed.go` (the connection and the accounts), `inv/queries/bank_feed.sql`
(→ `inv/store/bank_feed.sql.go`), and edits:

- **The mechanical consequence of the migration.** sqlc now types four columns
  `*uuid.UUID`. Thread a pointer through every writer, each existing caller passing its
  user as before — no behaviour changes in B:
  - `bank_files.uploaded_by_user_id`: `inv/queries/bankfiles.sql` (`BankFileBySHA`,
    `BankFileByIdentity`, `InsertBankFile` gains `provider`); `inv/bankimport.go:149–163`
    (`earlierImport`), `:137` (`bankFileDuplicate` — `by *uuid.UUID`), `:299`, `:547`
    (`bankFileResponse`; the wire field stays required until C, so B derefs with a
    guard that cannot fire);
  - `bank_transaction_events.by_user_id`: `inv/queries/matching.sql:72`;
    `inv/matching.go:282` (`queueLine`), `:295`, `:462`; `inv/bankqueue.go:573`
    (`lineEvent`), `:359`;
  - `charge_payments.registered_by_user_id`: `inv/queries/charges.sql:28`;
    `inv/matching.go:449`, `inv/bankqueue.go:831`, `inv/charges.go:326`, `:452`;
  - `charge_waivers.waived_by_user_id`: `inv/queries/charges.sql:62`; `inv/charges.go:211`
    (`waiveCharges`), `:289`, `:340`; its callers `inv/matching.go:480`, `inv/holds.go:287`
    and the posted re-judge;
  - `payments.registered_by_user_id` is nullable already; `InsertImportedPayment`
    (`inv/queries/matching.sql:42`) casts `@registered_by_user_id::uuid` — make it
    `sqlc.narg`.
- **The credential.** `bankFeedCredentialPurpose = "invoices/bank-feed-credential"`,
  sealed and opened with `s.deps.Secrets.SealString` / `OpenString` as
  `inv/vipps_settings.go:341` and `:161`.
- **The feed constructor**, in `inv/contractscalls.go` beside `paymentProvider`
  (`:412–448`): `func (s *server) bankFeed(ctx) (bankfeed.Feed, error)` — a typed
  "disabled" error when `!Config.InvoicesBankFeedEnabled`, "not connected" on no row, the
  key opened, `folio.New(folio.Options{BaseURL: Config.InvoicesFolioBaseURL, APIKey: key,
  Transport: s.deps.HTTPTransport, Now: s.deps.Clock})`, wrapped in a `notedFeed` that
  reports each call to the contract-call seam (`noteContractCall`, `:53`) and, on
  `bankfeed.ErrUnauthorized`, sets the connection `unauthorized` with `rejected_at` (as
  `notedProvider.flagged`, `:460`). `bankFeedWith(key)` builds one for a key not stored
  yet.
- **`GET /invoices/bank-feed`**: the row (never the ciphertext), `enabledOnInstance`
  from the switch, the accounts in account order.
- **`PUT /invoices/bank-feed`**: 409 `bank_feed_disabled` when the switch is off; 400 on
  a provider other than `folio` or a key empty, blank or over 1 000 characters; `Verify`
  with the new key **outside any transaction** — `ErrUnauthorized` → 422
  `bank_feed_credential_rejected`, anything else → 503, nothing stored either way; then
  one transaction: the row locked or inserted (`lockPaymentCredentials`' shape), the key
  sealed, `key_last4` the last four characters, status `ok`, `rejected_at` cleared, every
  `unauthorized` account back to `ok` and due now.
- **`DELETE /invoices/bank-feed`**: one transaction — the row deleted, every account
  `enabled = false`, its lease and `next_sync_at` cleared. Files, lines, payments and
  each account's format stay.
- **`GET /invoices/bank-feed/accounts`**: `Accounts` on the feed; each number normalised
  with `bankfile.NormaliseAccount` (`inv/bankfile/account.go:16`); `canEnable` when the
  kind is not `card` and the number is in `KnownSellerAccounts`
  (`inv/queries/bankfiles.sql:7`) — the rule the import applies at `inv/bankimport.go:205`.
- **`PUT /invoices/bank-feed/accounts/{account}`**: 409 `bank_feed_not_connected` with no
  row. Switching **on**: the account must be one `Accounts` answers now and not a card
  (409 `bank_feed_account_not_offered`) and a known seller account (409
  `bank_account_unknown`, the import's title and words); `startDay` defaults to today in
  the business zone (`holidays.DayIn(now, loc)`, `inv/bankimport.go:183`) and may not be
  after it; the bank is called before the transaction. Then one transaction:
  `lockImportAccount` (`inv/locks.go:88`) — no row: insert it with format `feed` by the
  caller (a single-account `UpsertImportAccounts`, `inv/queries/locks.sql:15`); a row in
  another format: `LatestBookedInFormat` + `ChangeImportFormat` exactly as
  `inv/bankaccounts.go:65–73`; already `feed`: nothing — then upsert
  `bank_feed_accounts` (`enabled`, `start_day`, `status ok`, `failures 0`, `next_sync_at
  = now`). Switching **off**: `enabled = false`, the lease and `next_sync_at` cleared;
  the format is left.
- **The format PUT** (`inv/bankaccounts.go:44`): still only `ocr` and `camt054`; under
  the account's lock, when the current format is `feed` and the account's feed is
  enabled, refuse 409 `bank_feed_enabled` (finding 9).
- **`bankfile`**: add `FormatFeed Format = "feed"` (`inv/bankfile/bankfile.go:22`) so the
  constants name all three; nothing parses it.
- **`GET /meta`** (`inv/meta.go:99`): `CanManageBankFeed: s.has(ctx, "invoices:manage")`.
- **`inv/module.go`**: name the bank feed in the `invoices:manage` and
  `invoices:payments` descriptions (`:81`, `:91`) and the comment above `permissions`.
  No new permission key.
- **The harness** (`inv/harness_test.go:139–152`): a `foliotest.Server` per harness,
  `modtest.WithEnv("INVOICES_FOLIO_BASE_URL", folio.URL())` and its host in the
  `hostTransport` map, beside Vipps'.

### B4. Server tests

- `srv/db/schema_invoices_bank_feed_test.go`, on the model of
  `srv/db/schema_invoices_payments_port_test.go:103` (`applyUpDownUp(t, url, version)`,
  then idempotence) and its check tests (`:466`): each new check, both ways — a `feed`
  file with an uploader and one without a provider refused; an `ocr` file without an
  uploader refused; a `bank_feed` payment without a bank line refused, with and without
  a user accepted; an `ocr` payment without a user refused; a charge payment likewise; a
  waiver without a user refused unless `deadline_met`; an event without a user accepted;
  the two tables' checks.
- `inv/bank_feed_test.go` (model: `inv/vipps_settings_test.go`): PUT stores nothing when
  `Verify` answers 401 (422) or 500 (503); the key is never answered and the last four
  are; the ciphertext opens only under the purpose; 409 with the switch off; DELETE
  switches every account off and keeps files; the accounts list with a card, an unknown
  account and a known one; the switch with each prior state of `bank_import_accounts`
  (none, `camt054` with lines → `previous_format` and `cutover_through`, already
  `feed`); `startDay` after today refused; the format PUT refused while enabled and
  allowed after; a file uploaded for a `feed` account refused
  `bank_import_format_mismatch` (the existing rule, pinned).
- `inv/bank_feed_races_test.go` (model: `inv/vipps_settings_races_test.go`): two PUTs at
  once store one row; a switch against a format PUT on the same account.
- `inv/meta_test.go`: the capability. `inv/locks_test.go`: the switch's lock order
  (the account row only).

### B5. The frontends

- **Invoices UI.** `ui/api/bank-feed.ts` (model: `ui/api/vipps.ts`): the five calls and
  their query keys. `ui/pages/bank-feed-settings.tsx`, exported as `BankFeedSettingsPage`
  (the package exports `./pages/*`): the connect form (bank, a password input for the
  key, the two sentences of F3 — a key reads the whole account; it cannot move money
  without BankID), the last four and the status, the bank's accounts with a switch and a
  start-day picker each and why one cannot be switched on, each enabled account's
  status, and *Disconnect* behind a confirmation. Model the form and its error mapping on
  `ui/pages/-vipps-settings.tsx`. `ui/pages/payments.tsx`: `formatLabel` (`:363`) and the
  accounts table show `feed`; the format dialog (`:467–508`) offers only `BANK_FORMATS`
  (`ui/api/bank.ts:35`, unchanged) and shows the 409. `ui/lib/errors.ts`: the new codes
  (its test reads the codes out of the Go source, `ui/lib/errors.test.ts:23`).
  `ui/i18n.ts`: every new key in `en` and `nb` (`bank.format.feed`: *Bank feed* /
  *Bankintegrasjon*). Tests: `ui/pages/bank-feed-settings.test.tsx` (model:
  `ui/pages/settings-vipps.test.tsx`), with handlers added to `ui/test/bank-server.ts`.
- **The host**, each a copy of Shipping's entry:
  - `host/routes/workspace/bank.tsx` from `host/routes/workspace/shipping.tsx`: module
    `invoices`, permissions `["invoices:access", "invoices:manage"]`, `labelKey
    "navigation.bank"`; `host/routeTree.gen.ts` regenerates on the next Vite run — commit
    it;
  - `host/routes/workspace.tsx:30`: admit holders of `invoices:manage` while Invoices is
    on, and name them in the comment;
  - `host/apps.ts:840` (the Integrations section) and `host/account-menu.ts:55`: the
    item, icon `IconBuildingBank`;
  - `host/catalogs/navigation.ts:97`, `:228`: `navigation.bank` — *Bank* in both;
  - tests: `host/routes/route-guards.test.ts:716` (the Shipping block copied),
    `host/apps.test.ts`, `host/navigation.test.ts`, and `host/catalogs/parity.test.ts`
    passes by itself.
- `bun run translations:check` is part of `frontend:check`.

### B6. Documentation

| Page | Change |
| --- | --- |
| `docs/en/admin/payments.md` + `docs/nb/admin/payments.md` | A new section **The bank feed** after *What can go wrong* (`:117`): what it is, the two instance settings, creating the key in Folio, connecting, choosing accounts and the start day, the cutover from files, what a leaked key can and cannot do, disconnecting. Add `apps/server/internal/bankfeed`, `apps/server/internal/invoices/bank_feed.go` and `apps/host/frontend/src/routes/workspace/bank.tsx` to `sources`. *The format of each account* (`:84`) names `feed` |
| `docs/en/admin/configuration.md` + `nb` | The two variables in the table at `:139` |
| `docs/en/admin/authentication.md` + `nb` | The two rows in the table of every variable (`:1397`) |
| `docs/en/admin/installation.md` + `nb` | `:722`: the Folio host among what the server reaches |
| `docs/en/admin/index.md` + `nb` | A **Bank** line among the Integrations items (`:97`) |
| `docs/en/user/workspace-administration.md` + `nb` | **Bank** in the Integrations list (`:16`) and section (`:578`), with who sees it |
| `docs/en/reference/invoices.md` | The connection and the accounts: the two tables, the five operations with status codes and permissions, the format rule, `canManageBankFeed`. Add `mig/00178_invoices_bank_feed.sql` to `sources` |
| `docs/en/contributing/platform-packages.md` | The purposes table (`:54`): `invoices/bank-feed-credential` |

Then `go generate ./...` in `apps/server` and the `chore(assistant)` commit.

### B7. Gates

All four. Before merging: the migration-number check above.

---

## Delivery C — the sync (F5, F6, F7, the rest of F8 and F9)

### C1. The seam

- **The contract.** `POST /invoices/bank-feed/sync` (`postInvoicesBankFeedSync`,
  `…access+invoices:payments`, body `{account?}`, 202 with the accounts made due; 409
  `bank_feed_disabled`, `bank_feed_not_connected`). Finding 5's changes:
  `InvoicesBankFile` gains `provider`, `uploadedBy` leaves `required`, its description
  names `feed`; `InvoicesBankTransactionFile` gains `provider`;
  `InvoicesBankTransactionEvent.by`, `InvoicesChargePayment.registeredByUserId` and
  `InvoicesChargeWaiver.waivedBy` leave `required`; both `source` enums gain `bank_feed`;
  `InvoicesBankIgnored` gains `internalTransfer` (required); the attention type enum
  (`:5050`) gains `bankFeedUnauthorized`, `bankFeedFailing`, `bankFeedRefused`, described
  in the operation's text (`:5016–5034`); the 409 `bank_file_duplicate` problem's
  `uploadedBy` is described as absent for a feed batch.
- **The setting.** `InvoicesBankFeedInterval time.Duration`, default 30 minutes, 5
  minutes to 24 hours, parsed as `STAFF_WORKERS_POLL` is (`srv/config/config.go:1078–1086`)
  with both bounds; test, `vantigo.env.example`.
- `go generate ./...`, `bun run gen:client`.

### C2. The batch — `inv/bankfile/` and `inv/bank_feed_sync.go`

- `inv/bankfile/bankfile.go`: `IgnoredInternalTransfer IgnoredKind = "internal_transfer"`;
  `newFile` (`:107`) counts it at zero (an older file's `ignored_kinds` lacks the key and
  reads as 0 — `ignoredResponse`, `inv/bankimport.go:523`, gains the field). New exported
  `FeedFile(identity, account string, txs []Transaction, ignored map[IgnoredKind]int)
  *File` that sets `Format: FormatFeed` and calls `finish()` (`:116`) — the fingerprints
  are `Fingerprints` (`inv/bankfile/fingerprint.go:27`), unchanged for good. The purity
  test stays as it is: `bankfile` does not import `bankfeed`. Tests in
  `inv/bankfile/fingerprint_test.go`: two feed lines of one day, amount and KID with
  different ids have different fingerprints and ordinal 1.
- `inv/bank_feed_sync.go`, `func (s *server) syncAccount(ctx, claim) error` — F5's seven
  steps, the bank called outside any transaction:
  1. `from` = the later of `start_day` and `LatestBookedInFormat(account, 'feed')`
     (`inv/queries/bankfiles.sql:165`) less 10 days; `to` = today in the business zone.
  2. Refuse the whole batch, storing nothing but the account's status `refused`: a
     booking day after today (`bankfile`'s rule, `inv/bankfile/bankfile.go:146`) —
     `booked_in_future`; a currency other than `NOK` — `currency_not_nok`; a direction
     `unknown` — `direction_unknown`; the account's format not `feed` —
     `format_mismatch` (finding 9). `detail` names the transaction's id, never a name or
     a text.
  3. Keep `Kind == transfer && Direction == credit` with an amount above zero. Count the
     rest: `debit`, `card_information`, `zero_amount`, `internal_transfer`.
  4. Build each kept `bankfile.Transaction` (findings 1 and 12): `LineRef` the id,
     `ArchiveRef` the id without hyphens, no value or ordering day, `BankCode` empty;
     `bankfile.FeedFile(...)`; then drop every line whose fingerprint a new query
     `StoredFingerprints(account, fingerprints[])` finds on a live line. Nothing left:
     `last_synced_at` set, the status `ok`, no batch.
  5. The document: the `Raw` of the lines left, in the order received, as one JSON
     array; its SHA-256; `bankFileKey` (`inv/bankimport.go:125`) gains `.json` for
     `feed`; `objectExists` then `objectPut` exactly as `inv/bankimport.go:224–233`.
     Without an object store (`!s.storageConfigured`) the cycle fails as "any other
     error".
  6. The import's transaction: `importBankFile` (`inv/bankimport.go:262`) takes
     `by *uuid.UUID`, a `provider *string` and a flag for how it takes the accounts —
     an upload keeps `lockImportAccounts`; a sync uses the new `shareImportAccounts`
     (`ShareImportAccounts` alone, finding 10) and treats a missing row or another
     format as `format_mismatch`. Identity `folio|<account>|<claim time, RFC 3339 UTC>`.
     The rest — the file row, `InsertBankTransactions`, the duplicates,
     `SetBankFileDuplicates` — is the same code.
  7. `s.matchFile(ctx, fileID, nil, now, loc)` after the commit.
- New in `inv/queries/bank_feed.sql`: `StoredFingerprints`, and the worker's statements
  (C4).

### C3. Matching by no user — `inv/matching.go`, `inv/bankqueue.go`

- `matchFile` (`:100`), `queueOnPool` (`:268`), `matchLine` (`:310`), `registerMatch`
  (`:417`), `waiveDeadlineMet` (`:480`) take `caller *uuid.UUID`. The upload
  (`inv/bankimport.go:244`) and `POST …/match` (`inv/matching.go:517`) pass the caller;
  the sync passes nil.
- `paymentSourceOf(line.Format)` at the four places of finding 2.
- The answers: `bankFileResponse` (`inv/bankimport.go:534`) answers `provider` and an
  absent `uploadedBy`; `BankFileRefs` (`inv/queries/bankqueue.sql:74`) and
  `inv/bankqueue.go:329` answer the file's `provider`; the event's `by`
  (`inv/bankqueue.go:359`), the charge payment's `registeredByUserId`
  (`inv/charges.go:326`) and the waiver's `waivedBy` (`:340`) may be absent.
- `inv/customer_slots.go:218`, `:300`: the personal-data export's `source` comment names
  `bank_feed`; its tests' expected JSON where they list sources.
- Nothing else in classification, the lock order, the allocation or the queue changes.

### C4. The worker — `inv/bank_feed_worker.go`

Copy `inv/payment_worker.go:159–233` (the struct over `newServer`, `Name`, `Interval`,
`Run`, `RunCycle`, `ProcessOne`) and its claim (`inv/queries/payment_worker.sql:11–31`).

- Name `invoices-bank-feed`; the tick is one minute; an account is due by `next_sync_at`.
- `ClaimBankFeedAccount`: one statement, `FOR NO KEY UPDATE SKIP LOCKED`, `enabled AND
  next_sync_at <= @now AND (lease_until IS NULL OR lease_until <= @now)`, and only while
  the connection's status is `ok`; lease 5 minutes (longer than the 30-second call, the
  object write and the import together). Every later write names the lease and clears it
  (`ReschedulePaymentAttempt`'s shape, `:33`); a write that finds the lease gone ends the
  claim (`errClaimLost`, `inv/payment_worker.go:142`).
- The claim holds **no connection** while it works: the claim is one statement on the
  pool, then the bank, the object store and the import's own transaction each take and
  release one. That is the pool rule every lease worker keeps (memory:
  lease-worker-pool-fix).
- Outcomes, F7's table: success → `ok`, `failures 0`, `last_synced_at`,
  `last_bank_file_id` when a batch was stored, `next_sync_at = now + interval`;
  `ErrUnauthorized` → the connection `unauthorized` (through `notedFeed`), every account
  `unauthorized`, no next try; `ErrRateLimited` → status unchanged, `detail` the wait,
  next try after `max(RetryAfter, interval)`; any other error → `failures + 1`, `failing`
  from the third in a row, next try `min(interval << failures, 6h)`; a refusal →
  `refused` with its reason, next try the interval. A failed cycle writes nothing else.
  Matching stopping early is not a failure: the batch stands and its lines stay pending.
- **Registration** — `inv/module.go:166` (`workers`): append `NewBankFeedWorker(d)` only
  when `d.Config.InvoicesBankFeedEnabled`, and say so in the comment above it and in
  `Module`'s.
- **`POST /invoices/bank-feed/sync`** (in `inv/bank_feed.go`): sets `next_sync_at = now`
  for the account, or every enabled one, where it is later than now — never the lease, so
  a cycle in flight finishes and the next claim takes it; 202. It does not clear
  `refused` or `unauthorized`.

### C5. Attention — `inv/attention.go`

- A new `attentionBankFeed(ctx, q)` called for a caller holding `invoices:manage`
  (`s.has`, as `:60` does for `invoices:payments`), before the early return: one item per
  account whose status is `unauthorized`, `failing` or `refused` — types
  `bankFeedUnauthorized`, `bankFeedFailing`, `bankFeedRefused`, id `<type>/<account>`,
  `entityId` the account, title its last four digits, `occurredAt` the row's
  `updated_at`. Query `AttentionBankFeedAccounts` in `inv/queries/attention.sql`.
- The host: `host/lib/dashboard-helpers.ts` — the link (`:407–424`) is `/workspace/bank`
  for the three types, the title keys join `invoicesAttentionTitleKeys` (`:532`);
  `host/catalogs/dashboard.ts`: three titles in `en` and `nb`;
  `host/routes/dashboard.test.ts:1299`: the three types.

### C6. Server tests

- `inv/bank_feed_sync_test.go`: each refusal of step 2 stores nothing and sets the
  reason; an overlap re-read stores no batch; two identical payments on one day are two
  lines; outgoing, card and internal transactions are counted and absent from the stored
  document (read it back from the fake object store); a KID of 26 digits is queued
  `no_kid`; no object store fails the cycle; the cutover from `camt054` holds back lines
  on or before it as `possible_duplicate`; a second payment of the same day, amount and
  KID in a later batch is held back.
- `inv/matching_test.go`: a feed line matched has a payment with source `bank_feed` and
  no user, `matched` by no user, and a `deadline_met` waiver by no user; a queued feed
  line applied by a person has source `bank_feed` and that person (finding 3); the wire
  answers each without the field.
- `inv/bank_feed_worker_test.go` (model: `inv/payment_worker_test.go`): each row of F7,
  the backoff's doubling and its 6-hour cap, three failures to `failing`, `Retry-After`
  honoured, an unauthorized key stopping every account until a PUT; two workers never
  take one account; a manual sync and the worker claiming the same account run once;
  **the pool of one** — `TestBankFeedWorker_RunsTheCycleOnAPoolOfOne`, a copy of
  `TestEhfEventsWorker_RunsTheCycleOnTheOneConnectionItHolds`
  (`inv/ehf_events_worker_test.go:318`: `db.Open(…, db.WithMaxConns(1))`, `d.Pool = one`,
  a deadline) over a cycle with nothing due, one that stores a batch and matches it, and
  one whose key is refused. Build the worker through `invoices.Module().Workers(d)`.
- `inv/bank_feed_races_test.go` (model: `inv/matching_races_test.go`,
  `inv/bankqueue_races_test.go:40`): a sync's match against an apply from the queue on
  the same invoice.
- The worker lists: `inv/ehf_worker_test.go:867`, `:875` and
  `inv/reminder_worker_test.go:711–714` gain `invoices-bank-feed`, and a case with
  `INVOICES_BANK_FEED_ENABLED=0`.
- **The boot on a pool of four**: `TestServe_APIIsReadyOnAPoolOfFourWithEveryWorker`
  (`apps/server/cmd/vantigo/main_test.go:611`) starts every default-on worker, this one
  included, with no change; run it and say so in the PR.
- `inv/attention_test.go`: the three types, and none for a caller without
  `invoices:manage`. `inv/customer_slots_test.go`: the export of a `bank_feed` payment.

### C7. The frontends

- `ui/api/bank.ts`: `uploadedBy` and the event's `by` optional; `provider`; `postSync`.
- `ui/pages/payments.tsx`: a **Fetch now** button beside **Import** (`:192–199`) for
  `canImportBankFiles`, shown while `GET /invoices/bank-feed` answers an enabled account,
  with each account's last sync and status under it; the files list (`:554–575`) shows
  *Bank feed · Folio* in the format column and *Bank feed* where the uploader's name is
  (`:575`); the duplicate notice (`:212`) copes without an uploader.
- `ui/pages/bank-file.tsx:55–57`: the same for one batch.
- `ui/components/bank-transaction-table.tsx:396`: an event without `by` reads *Bank
  feed*. `ui/components/payments-card.tsx:93`, `charges-card.tsx:120`, `:191`: the source
  label and a missing registrar or waiver.
- `ui/i18n.ts`, `en` and `nb`: `paymentSource.bank_feed` (`:1135`), `bank.fetchNow`,
  `bank.byFeed`, `bank.format.feedProvider`, the ignored kind, the statuses and reasons.
- Tests: `ui/pages/payments.test.tsx`, `ui/components/bank-transaction-table.test.tsx`,
  `ui/pages/invoice-payments.test.tsx`; handlers in `ui/test/bank-server.ts`.
- `ui/pages/bank-feed-settings.tsx`: each account's reason and detail, and *Fetch now*.

### C8. Documentation

| Page | Change |
| --- | --- |
| `docs/en/user/invoices.md` + `docs/nb/user/invoices.md` | A new section **Payments fetched from the bank** after *Importing payments from the bank* (`:1113`): what arrives by itself, *Fetch now*, a batch in the files list, *Bank feed* as who registered, a payment without a KID in the queue, and that a returned payment stays registered until someone removes it (F6). *The exception queue* (`:1179`) names feed lines. Add `apps/host/frontend/src/lib/dashboard-helpers.ts` and `apps/host/frontend/src/catalogs/dashboard.ts` to `sources` (finding 14) and name the three attention items |
| `docs/en/admin/payments.md` + `nb` | *The bank feed* gains: the worker and `INVOICES_BANK_FEED_INTERVAL`, the 10-day overlap, the status table and what to do for each (`unauthorized`: save a new key; `refused`: the reason; `failing`: reachability), that no mail is sent. Add `apps/server/internal/invoices/bank_feed_worker.go` and `bank_feed_sync.go` to `sources` |
| `docs/en/admin/configuration.md`, `authentication.md` + `nb` | The interval |
| `docs/en/admin/installation.md` + `nb` | The worker in the list at `:285` |
| `docs/en/admin/object-storage.md` + `nb` | A feed batch is stored as `bank-files/<sha256>.json` and is needed for a sync |
| `docs/en/reference/invoices.md` | The sync's seven steps, the line's fields, the fingerprint, source `bank_feed` and the optional registrar, the worker and its backoff, the attention types, `POST …/sync`, the contract's changed fields |
| `ROADMAP.md` | *Invoices → Later* (`:1636`): "a bank API for payments" leaves the list, and `:1643`'s "bank APIs and direct file delivery" becomes "direct file delivery"; a done entry for the bank feed above *Later*, naming the Later items of the spec |

Then `go generate ./...` and the `chore(assistant)` commit.

### C9. Gates

All four, and the boot test named above.

---

## Delivery D — the public site (another repository)

In `vantigo-io/vantigo-landing` (usually `../vantigo-landing`), its own pull request,
linked to C's, opened once C's user guide pages are written:

1. In `web/src/apps/invoices.ts`, in English and Norwegian, add what
   `docs/{en,nb}/user/invoices.md` now documents and nothing more: incoming payments on a
   Folio account are fetched, matched on their KID and registered without a file.
2. Follow that repository's `AGENTS.md`; run `mise run apps:check` and its other checks.
3. If the repository is not available to the executor, say so in C's hand-over, naming
   the Invoices page and the sentence above.

## What is left open after C

The open items at the top; a second bank's adapter; everything under the spec's *Out of
scope* and *Later*. And, from C's review:

- **Which account-number form Folio's transactions path wants.** The sync asks
  `/accounts/{accountNumber}/transactions` with the account's 11 digits (BBAN); were
  Folio to want the form it writes the account in (an IBAN, say), every read of that
  account would fail. To be settled against a real account with the open item above.
- **A day's slack on the cutover from files to the feed.** An account switched from
  `camt054` or `ocr` to `feed` holds back feed lines booked on or before the cutover —
  the latest day the files brought. A file of that day may not have carried every
  payment of it (an intraday notification), so the first feed read holds back a payment
  the files never registered, and a person applies it from the queue. A day of slack, or
  matching on the files' lines, was left for later.
