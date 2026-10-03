# Invoices — EHF over Peppol, and KID (phase 2) Implementation Plan

> **For agentic workers:** implement this plan task by task, one implementer per task and a reviewer after it, never in the same context. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Phase 2 of the Invoices module on the branch `feat/invoices-ehf-peppol-kid`: a deterministic Peppol BIS Billing 3.0 UBL 2.1 invoice and credit note rendered from the issued document's snapshot with the PDF embedded, validated by a Go pre-check and by the official XSD and Schematron artefacts as the CI oracle; the seller's Peppol id on the settings; KID under the seller's bank agreement, computed at issue with its algorithm stored, on the PDF, in the e-mail and in the EHF's payment id; an access-point port with a Storecove adapter and credentials sealed in their own table; a send that re-checks the receiver against the Peppol network and queues a transmission under the document's lock; an outbox-shaped worker that submits once per claim, polls, records the receipt, and leaves an unknown outcome to a person; channel precedence by the billing profile's preference; the slots, the contract, the app, and the documentation. One migration (`00036_invoices_ehf_kid.sql`), seven new operations.

**Architecture:** everything inside `apps/server/internal/invoices` as 1A/1B built it, plus two sub-packages: `ehf/` (the writer, the unit table, the pre-check, the goldens) and `accesspoint/` (the port, the Storecove adapter, error classes). The Peppol client (`srv/peppol`) is reused through `contractscalls.go`; the secrets box seals the API key; the worker copies the communications outbox's claim-and-lease shape and builds its own object store from the configuration. `withLockedTx` holds every write that locks; no contract, store, lookup or provider call ever runs inside it. The oracle lives in `tools/ehf` and a CI job, never in the server.

**Tech Stack:** Go 1.27 (pgx, sqlc 1.31.1, goose, oapi-codegen v2.8.0 strict server, `encoding/xml`), Temurin 21 + Saxon-HE 12.7 (CI oracle only), PostgreSQL 18, React + Mantine + TanStack, vitest, bun, mise, Astro Starlight (docs).

**Spec:** `docs/superpowers/specs/2026-10-03-invoices-ehf-peppol-kid-design.md` (D1–D16, Readings, Testing) — binding. Research: `docs/superpowers/research/2026-10-03-invoices-ehf-peppol-kid.md`. The shapes this copies: 1B's `send.go`, `payments.go`, `customer_slots.go`, migration `00035`; `communications/outbox.go` and `queries/outbox.sql`; `communications/channels.go` (credentials); `customers/server.go` (the Peppol client's construction), `customers/brreg.go` (the HTTP client); `peppol/` (its tests' `httptest` and stub-DNS shape); `tools/docs/check-coverage.ts` (a bun tool with tests).

## Global Constraints

- Branch `feat/invoices-ehf-peppol-kid`, cut from `main` at `00daa9f5`; HEAD carries the research, the spec and this plan. Never commit to `main`, never merge, never `--no-verify`.
- `export TEST_DATABASE_URL=postgres://vantigo:vantigo@127.0.0.1:55432/vantigo_test?sslmode=disable` for every `go test` (`docker compose -f docker-compose.test.yml up -d --wait`; colima must be running).
- Toolchain through mise only. After any `openapi/*.yaml`, `queries/*.sql`, `sqlc.yaml` or migration change: `cd apps/server && mise exec -- go generate ./...` (never package-scoped), twice; then from the root `mise exec -- bun run gen:client`; commit every generated file. `openapi/COVERAGE.md` is regenerated when an operation is added.
- Invoices has no corpus; `RequireCoverage` gates every operation in the module's own suite.
- **The documentation changes with the code (AGENTS.md).** Every task that alters a behaviour, a setting, an endpoint, a permission, a worker or a screen updates its pages in the same commit — `reference/invoices.md` for a rule or endpoint, `user/invoices.md` in `en/` and `nb/` for a screen, the admin pages for a setting or procedure — and runs `mise run docs:check` before committing. A task that changes nothing documented carries `Docs-Impact: none — <reason>`. Task 10 writes the new pages and the long sections; earlier server tasks add their rows to the reference's tables and a sentence where a page already speaks of the behaviour, so no commit leaves the docs false.
- No other module's Go changes, except: `srv/peppol` (three exports, Task 3), `srv/config` (two settings, Task 3), `mise.toml` and `.github/workflows/ci.yml` (Task 5), `srv/integration` (Task 9).
- **No directory, store, lookup or provider call inside a locked transaction**; every one goes through `contractscalls.go` (the harness's hook fails any made under `withLockedTx`).
- **Exact decimal**: `big.Rat` and `numeric`; the XML writes amounts with two decimals, quantities and prices with their stored scale, never exponent form.
- **The Oslo business day** from `Deps.Clock()`; never `CURRENT_DATE`.
- **One refusal rule** (1B's), with the spec's new codes; the provider's wording never on the wire for a reader.
- **Forbidden git commands:** `git add -A`, `git add .`, `git stash`, `git checkout -- .`, `git restore .`, `git clean`, `git reset --hard`, `git commit --amend`, `--no-verify`. Commit by pathspec; one committer at a time; unstage your files if you must yield the index; the pre-commit hook runs biome over the whole repository, so keep every touched TS file formatted continuously.
- Every commit message ends with `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>`; a docs-only or test-only commit carries `Docs-Impact: none — <reason>` above it. Scopes `invoices`, `invoices-ui`, `peppol`, `config`, `ci`, `tools`, `integration`, `docs`.
- Every new test is shown able to fail; the report says so.

**Parallelism.** Task 1 (the spike's record) first. Tasks 2 → 3 → 4 → 6 → 7 → 8 → 9 run in order on the server. Task 5 (the oracle toolchain) needs Task 4's goldens and may run beside Task 6. Task 10 (docs) follows Task 8 and is checked against the code. Task 11 (the app) needs the `api-schema.d.ts` Task 7 commits and may run beside Tasks 8–10. Task 12 verifies the whole branch. The one-committer rule holds throughout.

---

**How this plan reads the spec where it leaves a choice open**, each for the user's verdict (Task 12 repeats them):

1. `kid_algorithm` on the document is also set to NULL when there is no KID; the pair is all-or-none (a CHECK).
2. The send's `FOR UPDATE` reuses 1A's `LockInvoice`; the partial unique index's violation is caught by SQLSTATE `23505` on `ux_transmissions_active` and mapped to `ehf_already_sent`.
3. The worker's "one provider call per claim" is literal: a claim does exactly one of Submit, Status, Evidence or the 24-hour lookup refresh, then releases; the next claim continues.
4. The UBL writer is a hand writer over `xml.Encoder` with explicit `cac:`/`cbc:` names and the three namespace declarations on the root; the goldens are the readability test.
5. The XML read-back in tests uses `encoding/xml` with an `xml.Name` that ignores prefixes (namespace-qualified matching), never string matching.
6. The oracle compiles the Peppol Schematron with the ISO Schematron XSLT 2.0 skeleton (`schxslt` or the official `iso-schematron-xslt2` three-step pipeline, pinned by checksum) and caches the compiled XSLT under `tools/ehf/.cache`, git-ignored.
7. The e-mail cover text's KID sentence replaces the "quoting invoice number" clause only; the account sentence stays.
8. The CSV's `KID` column is the seventeenth, after `Credits number`.
9. `blockedBy` is the first of D8's codes in order, computed without the network.
10. The Storecove status read is whichever the spike confirms (Task 1); the plan's Task 8 carries both shapes and the implementer deletes the one not chosen.

## File Structure

| File | Responsibility |
| --- | --- |
| `docs/superpowers/plans/…` §Spike findings (this file, Task 1) | the confirmed Storecove contract |
| `srv/db/migrations/00036_invoices_ehf_kid.sql`, `srv/db/schema_test.go` | settings columns, `kid`/`kid_algorithm`, credentials table, transmissions table, triggers, indexes (Task 2) |
| `srv/invoices/{kid.go,kid_test.go,settings.go,issue.go,pdf.go,mailtext.go,csvexport.go}`, `queries/{settings,invoices,transmissions,credentials}.sql`, `openapi/invoices.yaml` | KID (Task 2) |
| `srv/peppol/{smp.go,doc.go}`, `srv/config/config.go`, `srv/invoices/{server.go,contractscalls.go,meta.go,module.go}` | the client, the settings, meta (Task 3) |
| `srv/invoices/ehf/{units.go,writer.go,render.go,precheck.go,eas.go,*_test.go,testdata/golden,testdata/invalid}` | the document (Task 4) |
| `tools/ehf/{validate.sh,Validate.java,manifest.json,README.md}`, `mise.toml`, `.github/workflows/ci.yml`, `docs/src/content/docs/en/contributing/e-invoice-validation.md` | the oracle (Task 5) |
| `srv/invoices/accesspoint/{accesspoint.go,storecove.go,storecove_test.go,fake_test.go}`, `srv/invoices/{credentials.go,credentials_test.go}` | the port, the adapter, the credentials (Task 6) |
| `srv/invoices/{sendehf.go,sendehf_test.go,transmissions.go,responses.go,list.go,drafts.go}` | the send, cancel, resolve, the UBL download, the `ehf` block (Task 7) |
| `srv/invoices/{ehf_worker.go,ehf_worker_test.go}` | the worker (Task 8) |
| `srv/invoices/customer_slots.go`, `srv/integration/{harness_test.go,ehf_test.go}` | the slots and the integration test (Task 9) |
| `docs/src/content/docs/{en,nb}/…`, `ROADMAP.md` | Task 10 |
| `apps/invoices/frontend/src/**` | Task 11 |

---

### Task 1: Record the Storecove contract spike

The spike ran as research (`scratchpad/spike-storecove.md`); this task writes its answers into this plan's §Spike findings and into `research §5.3`, and settles reading 10. Deliverable: a table — the raw-UBL field and encoding; `eIdentifiers` scheme format for `0192`; the attachment path and which PDF wins; the submission response; the 422 bodies for a duplicate key vs validation; 401/403; 429 and `Retry-After`; the status read (per-submission GET, or the pull queue and its acknowledgement); the evidence response; the dedupe window; whether the UBL is Schematron-validated on submission and how rule ids come back; the sandbox base URL and how keys are obtained; legal-entity registration. Each with its source URL and a confidence. Commit `docs(invoices): the Storecove contract, as the spike found it` with `Docs-Impact: none — a plan note`.

### Task 2: The migration, KID, and the settings' new fields (D2, D3, D9 tables)

**Files:** create the migration, `kid.go`, `kid_test.go`, `queries/transmissions.sql`, `queries/credentials.sql`; modify `schema_test.go`, `sqlc.yaml`, `settings.go`, `settings_test.go`, `issue.go`, `issue_test.go`, `pdf.go`, `pdf_internal_test.go`, `mailtext.go`, `mailtext_internal_test.go`, `csvexport.go`, `csvexport_test.go`, `queries/{settings,invoices}.sql`, `openapi/invoices.yaml`, the reference page's model/settings rows and the user page's settings and KID sentences (en + nb); generated files.

**The migration**, each plpgsql block in goose markers, a complete Down:

```sql
ALTER TABLE invoices.settings
    ADD COLUMN peppol_id     varchar(60),
    ADD COLUMN kid_length    smallint,
    ADD COLUMN kid_algorithm varchar(5),
    ADD CONSTRAINT ck_settings_kid CHECK (
        (kid_length IS NULL AND kid_algorithm IS NULL)
        OR (kid_length BETWEEN 4 AND 25 AND kid_algorithm IN ('mod10','mod11')));
UPDATE invoices.settings SET peppol_id = '0192:' || organisation_number
    WHERE organisation_number ~ '^[0-9]{9}$' AND peppol_id IS NULL;   -- the backfill (D2)

ALTER TABLE invoices.invoices
    ADD COLUMN kid           varchar(25),
    ADD COLUMN kid_algorithm varchar(5),
    ADD CONSTRAINT ck_invoices_kid CHECK ((kid IS NULL) = (kid_algorithm IS NULL)),
    ADD CONSTRAINT ck_invoices_kid_kind CHECK (kid IS NULL OR kind = 'invoice');

CREATE TABLE invoices.access_point_credentials (
    id                integer     PRIMARY KEY,
    provider          varchar(20) NOT NULL,
    settings_json     text        NOT NULL DEFAULT '{}',
    secret_ciphertext text        NOT NULL,
    updated_at        timestamptz NOT NULL,
    CONSTRAINT ck_access_point_single_row CHECK (id = 1),
    CONSTRAINT ck_access_point_provider CHECK (provider IN ('storecove'))
);

CREATE TABLE invoices.transmissions (
    id                   bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    invoice_id           bigint       NOT NULL REFERENCES invoices.invoices (id) ON DELETE RESTRICT,
    provider             varchar(20)  NOT NULL,
    idempotency_key      uuid         NOT NULL UNIQUE,
    sender_participant   varchar(60)  NOT NULL,
    receiver_participant varchar(60)  NOT NULL,
    document_type        varchar(300) NOT NULL,
    process_id           varchar(100) NOT NULL,
    ubl_object_key       varchar(300) NOT NULL,
    ubl_sha256           char(64)     NOT NULL,
    pdf_sha256           char(64)     NOT NULL,
    status               varchar(20)  NOT NULL DEFAULT 'queued',
    provider_ref         varchar(200),
    evidence_object_key  varchar(300),
    submit_attempts      integer      NOT NULL DEFAULT 0,
    poll_attempts        integer      NOT NULL DEFAULT 0,
    next_attempt_at      timestamptz  NOT NULL,
    submit_attempted_at  timestamptz,
    lease_id             varchar(100),
    lease_until          timestamptz,
    last_error           varchar(500),
    lookup_registered    boolean      NOT NULL,
    lookup_can_receive   boolean      NOT NULL,
    lookup_at            timestamptz  NOT NULL,
    queued_at            timestamptz  NOT NULL,
    submitted_at         timestamptz,
    delivered_at         timestamptz,
    failed_at            timestamptz,
    cancelled_at         timestamptz,
    resolved_by_user_id  uuid,
    resolution_note      varchar(500),
    created_by_user_id   uuid         NOT NULL,
    CONSTRAINT ck_transmissions_status CHECK (status IN ('queued','submitted','delivered','failed','unconfirmed','cancelled'))
);
CREATE UNIQUE INDEX ux_transmissions_active ON invoices.transmissions (invoice_id)
    WHERE status IN ('queued','submitted','delivered','unconfirmed');
CREATE INDEX ix_transmissions_due ON invoices.transmissions (status, next_attempt_at)
    WHERE status IN ('queued','submitted','unconfirmed');
```

Triggers: `invoices.guard_transmission_insert()` BEFORE INSERT — `SELECT status, customer_id … FOR SHARE`; refuse unless issued (`'invoices: a transmission needs an issued document'`); refuse when `invoices.erased_customers` holds the customer (`'invoices: the customer is anonymised'`) — the marker checked after the wait; `tr_transmissions_parent`. `invoices.refuse_transmission_change()` BEFORE UPDATE OR DELETE — DELETE raises `'invoices: a transmission is never deleted'`; UPDATE allowed only when the row as jsonb less the state columns (`status, provider_ref, evidence_object_key, submit_attempts, poll_attempts, next_attempt_at, submit_attempted_at, lease_id, lease_until, last_error, submitted_at, delivered_at, failed_at, cancelled_at, resolved_by_user_id, resolution_note`) is unchanged, and `OLD.status` is not `failed`/`cancelled`, and when `OLD.status = 'delivered'` only `evidence_object_key` from NULL may change; else `'invoices: a transmission is immutable'`; `tr_transmissions_immutable`. The 1A document trigger freezes `kid`/`kid_algorithm` automatically (they are set in the draft→issued UPDATE, which it allows).

**Interfaces:** `kid.go`: `const kidMod10, kidMod11`; `func kidCheckMod10(digits string) byte`, `func kidCheckMod11(digits string) byte` (`'-'` on remainder 1); `func computeKID(number int64, length int, algorithm string) (string, error)` (`errKIDLengthExceeded`); `func verifyKID(kid, algorithm string, number int64) bool`; `func kidFits(next int64, length int) (fits, headroomLow bool)`. `settings.go`: `peppolId` validation (the customers pattern; a `0192` value equal to the seller's number), the default when empty, the KID pair's rules against `nextNumber(q)` (the counter, or `series_start`), the `kid_headroom_low` warning on the settings response. `issue.go`: after `AllocateNumber`, when the agreement is set and kind is invoice, `computeKID` → `kid_length_exceeded` (a `cannotIssue` conflict; the number rolls back); `IssueDocument` gains the two columns. `pdf.go`: the "KID" line in the payment block; `mailtext.go`: the KID sentence; `csvexport.go`: the `KID` column last. Queries: `queries/transmissions.sql` and `queries/credentials.sql` with the Task 7/8 statements written now (sqlc needs them): `InsertTransmission`, `TransmissionsOf`, `LatestTransmission`, `ClaimTransmission`, `GetTransmissionForLease`, `MarkSubmitAttempted`, `MarkSubmitted`, `MarkUnconfirmed`, `MarkFailed`, `MarkDelivered`, `SetEvidence`, `RescheduleTransmission`, `CancelUnattemptedTransmission`, `ResolveTransmission`, `ActiveTransmissionsCount`, `CancelCustomerUnattemptedTransmissions`, `TransmissionsOfDocuments`; `GetAccessPointCredentials`, `UpsertAccessPointCredentials`, `DeleteAccessPointCredentials`.

**Contract:** settings request/response `peppolId`, `kidLength`, `kidAlgorithm`, `warnings` (`kid_headroom_low`); the document response `kid`, `kidAlgorithm` (optional); `InvoicesConflictProblem.code` gains `kid_length_exceeded`.

- [ ] **Step 1: Schema tests first** — `TestInvoicesEhfKid_AppliesAndIsIdempotent` (`applyUpDownUp(t, url, 36)`: the three new columns' CHECKs, both tables, the two triggers with their messages — an insert under a draft, under an erased customer, a DELETE, an UPDATE of `ubl_sha256`, an UPDATE of a `failed` row, `evidence_object_key` set once on `delivered` — the two indexes incl. the partial unique's predicate, the backfill of `peppol_id` from a planted settings row; Down removes all); widen the 1A/1B baseline tests' table and trigger counts; the reserved-word test at 36. Red.
- [ ] **Step 2: The migration and sqlc.yaml**; generate twice.
- [ ] **Step 3: KID tests first** — `TestKID_TheSpecificationsWorkedExamples` (`12345678` → `123456782` MOD10 and `123456785` MOD11), `TestKID_Mod11RemainderOneIsAHyphen`, `TestKID_ComputeAndVerify` (padding, length exceeded), `TestKID_Fits`; then `kid.go`. Red then green; shown able to fail by swapping the weights.
- [ ] **Step 4: Settings** — `TestSettings_PeppolIdDefaultsAndValidates`, `TestSettings_TheKidAgreement` (bounds; the fit rule against the counter and against `series_start`; the headroom warning; clearing allowed); `TestSettings_PeppolIdIsBackfilled` (plant 1A settings, migrate, read). Implement.
- [ ] **Step 5: The KID at issue and where it goes** — `TestIssue_AKIDUnderTheAgreement` (both algorithms; none on a credit note; none before the agreement; `kid_length_exceeded` rolls the number back — shorten the agreement by SQL after issuing to the limit), `TestPDF_TheKIDLine`, `TestCoverMail_TheKIDSentence`, `TestExportCSV_TheKIDColumnLast`, `TestDocument_CarriesItsKID`. Implement.
- [ ] **Step 6: Docs rows** — the reference's model and settings tables gain the columns and the KID paragraph stub pointing to Task 10's section; the user pages' settings section gains one sentence each (en + nb). `mise run docs:check`.
- [ ] **Step 7:** lint, the suites, generate idempotent, `gen:client`, fixtures patched, typecheck; **commit** `feat(invoices): KID under the bank agreement, the seller's Peppol id, and the tables for credentials and transmissions`.

### Task 3: The Peppol client in Invoices, the settings, and meta (D1, D6)

**Files:** modify `srv/peppol/{smp.go,doc.go}` (export `InvoiceDocumentType`, `CreditNoteDocumentType`; add `BillingProcessID`; a test pinning the three), `srv/config/config.go` + `config_test.go` (`InvoicesEhfEnabled` from `INVOICES_EHF_ENABLED` via `boolean(…, true)`; `InvoicesStorecoveBaseURL` from `INVOICES_STORECOVE_BASE_URL`, default `https://api.storecove.com/api/v2/`, validated like `brregBaseURL`), `srv/invoices/{server.go,contractscalls.go,meta.go,meta_test.go,module.go}`, `openapi/invoices.yaml`, `docs/…/admin/authentication.md` (en + nb: the two rows in the configuration reference), the reference's meta description; generated files.

**Interfaces:** `server.peppolLookup func(ctx, participant string) (peppol.Result, error)` built as customers does (nil when the lookup is disabled); `contractscalls.go`: `func (s *server) lookupReceiver(ctx, participant string) (peppol.Result, error)` with `noteContractCall(ctx, "Peppol.Lookup")`, logging a failure by `peppolErrorKind`; `meta.go`: `ehfAvailable` (switch, lookup enabled, credentials row present, `peppol_id` set), `accessPointCredentialsRejected` (Task 8 sets a flag the meta reads: a row in `access_point_credentials`? — no: a column `rejected_at timestamptz` on the credentials row, set by the worker on `ErrUnauthorized`, cleared on a successful call or a new key; add it in Task 2's migration), `capabilities.canSendEhf`.

- [ ] **Step 1:** tests — `TestPeppol_TheBillingIdentifiersArePinned`; `TestLoad_InvoicesEhfSettings`; `TestMeta_EhfAvailabilityAndCanSendEhf` (each precondition toggled). Red; implement; green.
- [ ] **Step 2:** docs rows; `docs:check`; **commit** `feat(invoices): the Peppol client as the send's re-check, and the e-invoicing switches on meta`.

### Task 4: The EHF document: the writer, the units, the pre-check, the goldens (D4, D5, D11's pre-check)

**Files:** create `srv/invoices/ehf/{doc.go,units.go,units_test.go,eas.go,eas_test.go,writer.go,render.go,render_test.go,precheck.go,precheck_test.go,testdata/golden/*.xml,testdata/invalid/*.xml,testdata/invalid/manifest.json}`; modify nothing outside the package except `srv/invoices/responses.go` (a `renderEHF` accessor on the server that reads the settings' Peppol id) and the reference page's "E-invoicing" mapping-table stub.

**Interfaces:** `ehf.Document` — a plain struct built from `store.InvoicesInvoice`, its lines, its VAT summaries, the original (for a credit note), the seller's Peppol id and the PDF bytes (`func DocumentOf(inv, lines, sums, original *…, sellerPeppolID string, pdf []byte, pdfName string) (Document, error)` in `doc.go`, in the invoices package's terms); `func Render(d Document) ([]byte, error)` (the writer; deterministic); `func UnitCode(unit string) string` (the table; `C62` fallback; punctuation stripped; `t` unmapped); `func EASScheme(scheme string) bool` (the vendored EAS list, version-stamped); `func Precheck(xml []byte, d Document) (Report, error)` — `Report{Refusals []Rule, Internal []Rule}` where `Refusals` are the user-facing rule ids (R003, the EAS scheme, `vat_category_k_unsupported`, NO-R-001's shape) and `Internal` the module-only invariants (BR-CO-10/13/15 re-summed, the KID against its algorithm, the attachment, a unit outside the table) — the send maps `Refusals` to 409 `ehf_invalid` and `Internal` to a 500.

The mapping is the spec's D4 table, row for row; the category rules (no `Percent` on O; the exemption code on AE/G/O; the text on E only; nothing on Z/S; K refused) are each a named test. `render_test.go`: `TestEHF_Goldens` renders the fixtures of D11 (two VAT fixtures — the registered seller with S at every seeded rate plus Z, E, AE and G; the non-registered seller with O — a discount, a foreign buyer with IBAN/BIC, a person, a credit note, a partial credit note with the squaring row, a KID under each algorithm, a delivery period and a place with and without a country) and compares byte-for-byte with `testdata/golden`, `-update` rewriting; `TestEHF_ReadBack_*` one per mapping row through `encoding/xml` name-qualified decoding; `TestEHF_IsPure`; `TestEHF_DiffersWhenTheSellerPeppolIdChanges`.

- [ ] **Step 1:** units and EAS tests first (every table entry; the fallback; the strip; `t` unmapped; `0192`, `0088`, `0208` true, `9999` false). Implement.
- [ ] **Step 2:** the read-back tests first (red: no writer), then `doc.go`, `writer.go`, `render.go`; green; the goldens written with `-update` and committed; `TestEHF_Goldens` green without the flag.
- [ ] **Step 3:** the pre-check tests first (each refusal rule and each internal rule on a tampered document), then `precheck.go`; the invalid fixtures and their manifest written (no buyer reference; a malformed endpoint scheme; tampered totals; a reason on Z; a percent on O; a K line).
- [ ] **Step 4:** the reference page's mapping table and units section (English); `docs:check`; **commit** `feat(invoices): the EHF document — a deterministic UBL 2.1 render with the PDF embedded, the unit table, and the pre-check`.

### Task 5: The oracle: Java, Saxon, the artefacts, the CI job, the contributing page (D11)

**Files:** create `tools/ehf/{validate.sh,Validate.java,artefacts.lock,README.md}`, `docs/src/content/docs/en/contributing/e-invoice-validation.md`; modify `mise.toml` (`java = "temurin-21"` in `[tools]`; `[tasks."ehf:validate"]`), `.github/workflows/ci.yml` (a job `🧾 Validate EHF goldens` with `install_args: "java"`, on every pull request), `.gitignore` (`tools/ehf/.cache`), the contributing overview page.

**`validate.sh`**: downloads into `tools/ehf/.cache` and verifies against `artefacts.lock` (SHA-256 each): Saxon-HE 12.7 from Maven Central; `en16931-ubl-1.3.16.zip` from the CEN release `validation-1.3.16`; the Peppol repository at tag `v3.0.20` (zipball) and the ISO Schematron XSLT 2.0 skeleton (pinned); the UBL 2.1 XSD zip from OASIS. Compiles `PEPPOL-EN16931-UBL.sch` to XSLT once (cached by the lock's hash). For every `srv/invoices/ehf/testdata/golden/*.xml`: XSD (`java Validate.java <xsd> <xml>`), then the CEN XSLT, then the Peppol XSLT; parses SVRL, fails on any `flag="fatal"`, prints warnings. For `testdata/invalid/*.xml`: expects exactly the manifest's rule ids among the fatals and fails otherwise. Exit codes and a summary line. `README.md` says how to bump a pin.

- [ ] **Step 1:** write the script and `Validate.java`; run locally (`mise install` pulls Java); every golden green, every invalid fixture trips its manifest; fix the writer where the oracle disagrees (expected on the first run — report what it found).
- [ ] **Step 2:** the mise task, the CI job, the contributing page (what it checks, how to run it, how to bump the artefacts, where the fixtures live), the overview entry; `docs:check`.
- [ ] **Step 3: commit** `ci(invoices): the EHF oracle — the official XSD and Schematron artefacts over the goldens, in CI and on demand`.

### Task 6: The access-point port, the Storecove adapter, and the credentials (D7)

**Files:** create `srv/invoices/accesspoint/{accesspoint.go,storecove.go,storecove_test.go,fake_test.go}`, `srv/invoices/{credentials.go,credentials_test.go}`; modify `srv/invoices/{server.go,contractscalls.go,harness_test.go}`, `openapi/invoices.yaml`, the reference page's endpoints/permissions rows, the user and admin page sentences; generated files.

**Interfaces:** the port exactly as spec D7 (the interface, `Submission`, `SubmissionRef`, `SubmissionStatus`, the error classes `ErrAlreadySubmitted{Ref}`, `ErrRejected{Reason}`, `ErrUnauthorized`, `ErrThrottled{RetryAfter}`); `storecove.New(baseURL, apiKey, legalEntityID string, transport http.RoundTripper) *Client` implementing it with the contract Task 1 recorded; no retries inside; 30 s per request; `Verify` = the cheapest authenticated read; the fake in `fake_test.go` (`httptest`) speaking the same contract for every status path. `contractscalls.go`: `accessPoint(ctx) (accesspoint.AccessPoint, error)` opening the key (`noteContractCall("AccessPoint.…")` on each method through a wrapper). `credentials.go`: `PUT/DELETE /invoices/settings/access-point`, `POST …/verify`, `transmissions_active` (409) while any transmission is active, `hasCredentials`, `rejectedAt` on the response; the key sealed under `invoices/access-point-credential`.

- [ ] **Step 1:** adapter tests first against the fake: a submission's body shape (the UBL, `legalEntityId`, `eIdentifiers`, `idempotencyGuid`), each response class, status mapping to the four states, evidence, verify. Red; implement; green.
- [ ] **Step 2:** credentials tests first: never returned, kept when omitted, DELETE and switch refused while active, verify's answers, `invoices:manage`, a failed `Open` → 503 and an error log. Implement.
- [ ] **Step 3:** docs rows; `docs:check`; **commit** `feat(invoices): the access-point port with a Storecove adapter, and credentials sealed in their own table`.

### Task 7: The send, cancel, resolve, the UBL download, and the `ehf` block (D8, D10, D14)

**Files:** create `sendehf.go`, `sendehf_test.go`, `transmissions.go`, `transmissions_test.go`; modify `responses.go`, `list.go`, `drafts.go`, `queries/invoices.sql`, `openapi/invoices.yaml`, `export_test.go` (`SetBeforeTransmissionInsert`), the reference's endpoints table and send-order section, the user page's "Sending as EHF" stub (en + nb); generated files.

**The send** in D8's ten steps; the lock `LockInvoice` (`FOR UPDATE`); the index violation mapped. The draft warning `ehf_buyer_reference_missing` in `renderInvoice` when the profile prefers `ehf` or has a Peppol id (the profile is read for drafts already). `transmissions.go`: cancel (the conditional update), resolve (`unconfirmed` only; `transmission_not_resolvable`), the UBL download (`application/xml`, hash-verified, 500 when broken). `responses.go`: the `ehf` block with `canSend`/`blockedBy` computed without the network; issuer-only fields; `list.go`: `ehfStatus`.

- [ ] **Step 1:** tests first, named for each refusal in order (`TestSendEhf_UnavailableIsJudgedFirst`, `…RefusesADraftAndAnAnonymisedCustomer`, `…NoPeppolId`, `…BuyerReferenceMissing`, `…AlreadySent`, `…PrecheckBeforeTheLookup`, `…TheReceiverRecheck` (not registered; cannot receive a credit note; the lookup error 502 logged by kind), `…StoresTheUblOnce`, `…RendersFreshAfterAFailure`, `…TwoRacingSendsOneQueued` (held on the seam after the lookup), `…TheRateLimit`, `…NeedsIssue`), `TestDrafts_WarnsEhfBuyerReferenceMissing`, `TestTransmissions_Cancel`, `TestTransmissions_Resolve`, `TestTransmissions_UblDownload`, `TestDocument_TheEhfBlock`, `TestList_EhfStatus`. Red; implement; green; the index shown to catch a race with the lock removed.
- [ ] **Step 2:** docs; `docs:check`; **commit** `feat(invoices): send as EHF — re-checked against the network, queued under the document's lock, with cancel, resolve and the UBL download`.

### Task 8: The `invoices-ehf` worker (D9)

**Files:** create `ehf_worker.go`, `ehf_worker_test.go`; modify `module.go` (`Workers`), `queries/transmissions.sql` if a statement is missing, the reference's worker section stub and `admin/installation.md`'s workers list (en + nb).

**The worker** as D9: the claim query (`status IN ('queued','submitted','unconfirmed') AND next_attempt_at <= now AND (lease_until IS NULL OR lease_until < now)`), one call per claim, the crash marker, the error classes → the transitions, the caps (8 submit attempts / 48 h → `unconfirmed` when attempted, else `failed`), the poll cadence by `poll_attempts`, `delivered` committed before evidence, the seven days → `unconfirmed`, the daily poll of `unconfirmed` rows for thirty days, the 24-hour lookup refresh (through the seam), the lease-checked completion, its own object store from the configuration, the switches. **Status read**: per the spike — a per-submission GET polled by the row's cadence, **or** the leased drain of the account's pull queue (a second worker `invoices-ehf-events` under an advisory lease, `0x494E56454846" "INVEHF`, matching events by provider guid, acknowledging them) with `Evidence` as the proxy; the implementer keeps the one Task 1 chose.

- [ ] **Step 1:** tests first against the fake access point and a fake lookup: claim and lease (two workers, one submission); the marker before Submit; each error class's transition; the caps by the marker; the cadence under the fixed clock; delivered-then-evidence with a failing fetch retried; the seven days; the daily unconfirmed poll resolving; the lookup refresh; a failed `Open`; the lease-changed no-op; the switch off → no worker; worker-mode Deps. Red; implement; green; shown able to fail by removing the lease check (a double completion).
- [ ] **Step 2:** docs; `docs:check`; **commit** `feat(invoices): the invoices-ehf worker — one provider call per claim, polled status, the receipt stored once, and an unconfirmed outcome left to a person`.

### Task 9: The slots and the integration test (D12)

**Files:** modify `customer_slots.go`, `customer_slots_test.go`, `queries/customers.sql`; `srv/integration/{harness_test.go,ehf_test.go}`; the customers reference's anonymisation list.

- [ ] **Step 1:** export carries transmissions; erase cancels never-attempted queued rows (count) and keeps the rest; run twice zero. Red; implement.
- [ ] **Step 2:** `TestEhf_ARealCustomerIsInvoicedAsEhfEndToEnd` composing customers + invoices with the fake lookup and the `httptest` provider: profile with `peppolId` and `invoiceDelivery: ehf` → issue (a KID under an agreement) → send-ehf → the worker once → `submitted`, again → `delivered` with evidence; the `ehf` block and the UBL download.
- [ ] **Step 3:** `docs:check`; **commit** `feat(invoices): transmissions in the export and the erase, and the customers + invoices EHF integration test`.

### Task 10: The documentation (D16)

**Files:** `docs/src/content/docs/en/reference/invoices.md` (the two long sections and every table), `en/user/invoices.md` + `nb/user/invoices.md`, **new** `en/admin/e-invoicing.md` + `nb/admin/e-invoicing.md` (its `sources`), `en/admin/index.md` + `nb/admin/index.md`, `en/admin/object-storage.md` + `nb/…`, `en/admin/installation.md` + `nb/…` (verify the Task 8 sentence), `en/reference/customers.md` (verify Task 9), `en/contributing/index.md` (verify Task 5), `ROADMAP.md`.

- [ ] **Step 1:** write every section D16 lists in the house voice, English first, then the Norwegian pages; the admin page is honest about Storecove's sales-contact onboarding and names Qvalia as the next adapter.
- [ ] **Step 2:** the docs check against the code as 1B's Task 10 did (every code, warning and access rule in the reference; nothing missing); `mise run docs:check` (the build, the coverage check, the bilingual parity).
- [ ] **Step 3: commit** `docs(invoices): e-invoicing over Peppol and KID — the reference, the user guide, and the administration page`.

### Task 11: The Invoices app (D15)

**Files:** `apps/invoices/frontend/src/**` — `pages/settings.tsx` (the E-invoicing card, the KID card), `api/{settings,access-point,ehf}.ts`, `pages/-send-ehf-dialog.tsx`, `components/ehf-card.tsx`, `pages/invoice.tsx` (the primary action by precedence, the card, the draft warning), `pages/-send-dialog.tsx` (`ehf_preferred`, the cross-channel note), `components/invoice-table.tsx` (the EHF column), `lib/errors.ts`, `i18n.ts`, fixtures and tests.

- [ ] **Step 1:** tests first per the spec's Testing "Frontend"; red; implement; `biome --write`; tests, typecheck, lint, `translations:check`.
- [ ] **Step 2: commit** `feat(invoices-ui): e-invoicing — the settings cards, send as EHF, the transmission card, KID, and channel precedence`.

### Task 12: Verify the whole branch and open the PR

- [ ] **Step 1:** generate and gen:client → no drift; gofmt; vet; lint; `go test -count=1 ./...`; race on invoices and integration; govulncheck; every frontend package's checks; `mise run docs:check`; `mise run ehf:validate`; the greps (no `CURRENT_DATE`, no `SetFloat64`, every outside call through `contractscalls.go`, no module import).
- [ ] **Step 2:** the PR in PR #129's shape: What, Decisions made without asking (the spec's fourteen readings and this plan's ten), Things to know (the Java/Saxon CI job; the Storecove onboarding; the KID agreement; the switch defaults), How it was built, Verification. `gh pr create --base main --head feat/invoices-ehf-peppol-kid`. Watch CI; do not merge.
- [ ] **Step 3: Report.**

## Spike findings (Task 1 fills this in)

_Pending the spike's report._

## Self-review

**Spec coverage.** D1 → Task 3; D2, D3 → Task 2; D4, D5 → Task 4; D6 → Task 3; D7 → Task 6; D8, D10, D14 → Task 7; D9 → Task 8; D11 → Tasks 4 (pre-check) and 5 (oracle); D12 → Task 9; D13 → nothing adds it; D15 → Task 11; D16 → Task 10 and each task's docs step. Every Testing bullet names a test above.

**Name consistency.** SQL: `invoices.access_point_credentials`, `invoices.transmissions`, `guard_transmission_insert`, `refuse_transmission_change`, `ux_transmissions_active`, `ix_transmissions_due`, `ck_settings_kid`, `ck_invoices_kid`. Go: `computeKID`, `verifyKID`, `kidFits`, `lookupReceiver`, `accessPoint`, `ehf.DocumentOf`, `ehf.Render`, `ehf.UnitCode`, `ehf.EASScheme`, `ehf.Precheck`, `accesspoint.AccessPoint`, `storecove.New`. Wire: `ehf`, `ehfStatus`, `kid`, `kidAlgorithm`, `peppolId`, `kidLength`, `ehfAvailable`, `accessPointCredentialsRejected`, `canSendEhf`; the codes and warnings of D14.
