# Invoices — EHF over Peppol, and KID (phase 2) Implementation Plan

> **For agentic workers:** implement this plan task by task, one implementer per task and a reviewer after it, never in the same context. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Phase 2 of the Invoices module on the branch `feat/invoices-ehf-peppol-kid`: a deterministic Peppol BIS Billing 3.0 UBL 2.1 invoice and credit note rendered from the issued document's snapshot with the PDF embedded, validated by a Go pre-check and by the official XSD and Schematron artefacts as the CI oracle; the seller's Peppol id on the settings; KID under the seller's bank agreement, computed at issue with its algorithm stored, on the PDF, in the e-mail and in the EHF's payment id; an access-point port with a Storecove adapter and credentials sealed in their own table; a send that re-checks the receiver against the Peppol network and queues a transmission under the document's lock; two workers — one that submits once per claim and probes evidence, one that drains the provider's event queue — leaving an unknown outcome to a person; channel precedence by the billing profile's preference; the slots, the contract, the app, and the documentation. One migration (`00036_invoices_ehf_kid.sql`), seven new operations.

**Architecture:** everything inside `apps/server/internal/invoices` as 1A/1B built it, plus three packages: `invoices/kid` (a leaf: the check digits, imported by `invoices` and `ehf`), `invoices/ehf` (the `Document` model of `big.Rat` and strings, the writer, the unit table, the pre-check, the goldens) and `invoices/accesspoint` (the port, the Storecove adapter, the error classes) with `accesspoint/storecovetest` (an importable `httptest` Storecove for every test package). The conversion from store rows to `ehf.Document` lives in package `invoices` (`ehfdoc.go`). The Peppol client (`srv/peppol`) is reused through `contractscalls.go`; the secrets box seals the API key; both workers hold a `*server` from `newServer`, so every outside call goes through `contractscalls.go` and the scoped object store is built from the configuration as in worker mode. `withLockedTx` holds every write that locks; no contract, store, lookup or provider call ever runs inside it. The oracle lives in `tools/ehf` and a CI job, never in the server.

**Tech Stack:** Go 1.27 (pgx, sqlc 1.31.1, goose, oapi-codegen v2.8.0 strict server, `encoding/xml`), Temurin 21 + Saxon-HE 12.7 + SchXslt (CI oracle only), PostgreSQL 18, React + Mantine + TanStack, vitest, bun, mise, Astro Starlight (docs).

**Spec:** `docs/superpowers/specs/2026-10-03-invoices-ehf-peppol-kid-design.md` (D1–D16, Readings, Testing) — binding; this plan's pre-flight review amended D9 (delivered rows stay claimable for evidence; the lookup refresh in the submit claim; the crash marker's rule; the cap by age; the UBL reuse rule) and the spec carries the amendments. Research: `docs/superpowers/research/2026-10-03-invoices-ehf-peppol-kid.md`. The shapes this copies: 1B's `send.go`, `payments.go`, `customer_slots.go`, migration `00035`; `communications/outbox.go` and `queries/outbox.sql`; `communications/channels.go`; `customers/server.go` and `customers/peppol_lookup.go` (`peppolErrorKind`, `peppolDNSServers` — copied, since depguard forbids the import); `customers/peppol_recheck_worker.go` (the advisory lease); `customers/brreg.go`; `peppol/`'s tests; `tools/docs/check-coverage.ts`.

## Global Constraints

- Branch `feat/invoices-ehf-peppol-kid`, cut from `main` at `00daa9f5`; HEAD carries the research, the spec and this plan. Never commit to `main`, never merge, never `--no-verify`.
- `export TEST_DATABASE_URL=postgres://vantigo:vantigo@127.0.0.1:55432/vantigo_test?sslmode=disable` for every `go test` (`docker compose -f docker-compose.test.yml up -d --wait`; colima must be running).
- Toolchain through mise only. After any `openapi/*.yaml`, `queries/*.sql`, `sqlc.yaml` or migration change: `cd apps/server && mise exec -- go generate ./...` (never package-scoped), twice; from the root `mise exec -- bun run gen:client`; when an operation is added, `cd apps/server && mise exec -- go run ./internal/openapi/cmd/contract coverage -corpus ../../openapi/testdata/exchanges -out ../../openapi/COVERAGE.md`. Commit every generated file.
- **Every server task that changes the wire patches `apps/invoices/frontend/src/test/fixtures.ts` (and any page test that spells a whole object) in the same commit and runs `mise exec -- bun run --cwd apps/invoices/frontend typecheck`.**
- Invoices has no corpus; `RequireCoverage` gates every operation in the module's own suite.
- **The documentation changes with the code (AGENTS.md).** Each task updates the pages its change touches in the same commit — `reference/invoices.md` for a rule, a column or an endpoint; `user/invoices.md` in `en/` and `nb/` for anything the generated client type or a screen changes (every task that edits `openapi/invoices.yaml` touches `api-schema.d.ts`, which the user page covers — so such a task adds at least its sentence to both user pages); the admin pages for a setting, a worker or a procedure. **The gate is per commit:** the branch carries `Docs-Impact: none` trailers, and the checker waives everything in `mergeBase..HEAD` when it sees one, so each task runs `mise exec -- bun run tools/docs/check-coverage.ts --base HEAD~1` right after its commit (and `mise run docs:check` for the build and parity); a failure is a follow-up commit, never an amend. Reviewers re-run it. A commit that changes nothing documented carries `Docs-Impact: none — <reason>`.
- No other module's Go changes, except: `srv/peppol` (three exports, Task 3), `srv/config` (two settings, Task 3), `mise.toml`, `.github/workflows/ci.yml`, `CONTRIBUTING.md`'s `mise install` line (Task 5), `srv/modtest` (nothing expected; say so if needed), `srv/integration` (Task 9).
- **No directory, store, lookup or provider call inside a locked transaction**; every one goes through `contractscalls.go` (the harness's hook fails any made under `withLockedTx`).
- **Every transmissions query takes `@now` from `Deps.Clock()`** — never SQL `now()`: the harness clock sits weeks before the database's, and a lease judged by `now()` would always look expired. The 1A rule "never `CURRENT_DATE`" covers it.
- **Exact decimal**: `big.Rat` and `numeric`; the XML writes amounts with two decimals, quantities and prices with their stored scale, never exponent form.
- **One refusal rule** (1B's), with the spec's new codes; the provider's wording never on the wire for a reader; `last_error` redacted (e-mail addresses and `NNNN:` identifiers replaced) and truncated rune-safely to 500.
- **Forbidden git commands:** `git add -A`, `git add .`, `git stash`, `git checkout -- .`, `git restore .`, `git clean`, `git reset --hard`, `git commit --amend`, `--no-verify`. Commit by pathspec; one committer at a time; unstage your files if you must yield the index; keep every touched TS file formatted continuously (the hook runs biome over the repository).
- Every commit message ends with `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>`; a docs-only or test-only commit carries `Docs-Impact: none — <reason>` above it. Scopes `invoices`, `invoices-ui`, `peppol`, `config`, `ci`, `tools`, `integration`, `docs`.
- Every new test is shown able to fail; the report says so.

**Parallelism.** Task 1 is done. Tasks 2 → 3 → 4 → 6 → 7 → 8 → 9 run in order on the server. Task 5 (the oracle toolchain) needs Task 4's goldens and may run beside Task 6. Task 10 (the app) needs the `api-schema.d.ts` Task 8 commits and may run beside Task 9. Task 11 (the docs) follows Task 10 — the user guide describes screens that must exist. Task 12 verifies the whole branch. The one-committer rule holds throughout.

---

**How this plan reads the spec where it leaves a choice open**, each for the user's verdict (Task 12 repeats them):

1. `kid` and `kid_algorithm` on the document are all-or-none (a CHECK); the pair is NULL without an agreement.
2. The send's `FOR UPDATE` reuses 1A's `LockInvoice`; `customer_anonymised` is judged again under the lock; the partial unique index's violation (SQLSTATE `23505` on `ux_transmissions_active`) maps to 409 `ehf_already_sent`.
3. A claim does one provider call: the submit claim refreshes the lookup first when it is older than 24 h **and then** submits in the same claim (the refresh is a lookup, not a provider call); the probe claim does one `Evidence`; the evidence claim does one `Evidence` and stores.
4. The UBL writer is a hand writer over `xml.Encoder` with explicit `cac:`/`cbc:` names and the three namespace declarations on the root; the goldens are the readability test.
5. The XML read-back in tests uses `encoding/xml` with namespace-qualified matching, never string matching.
6. The oracle compiles the Peppol Schematron with **SchXslt** (pinned on Maven Central) unless the OpenPEPPOL release already ships compiled XSLT, which Task 5 checks first; the compiled XSLT is cached under `tools/ehf/.cache`, git-ignored.
7. The e-mail cover text's KID sentence replaces the "quoting invoice number" clause only.
8. The CSV's `KID` column is the seventeenth, after `Credits number`.
9. `blockedBy` is the first of D8's steps 1–5 plus a K-category check from the lines, computed without the network and without a render; `ehf_invalid` is judged at send only.
10. Status is the pull queue drained by a second leased worker; evidence is the proof probe; the delivered copy from the evidence is stored as the transmitted record.
11. **The cap is by age only**: a `queued` row older than 48 hours since `queued_at` becomes `unconfirmed` when the crash marker is set, else `failed`; attempts are not capped (backoff reaches its 3600 s ceiling), so a nine-minute provider outage does not strand every queued invoice on a person's desk. `ErrUnauthorized` and `ErrThrottled` do not count as attempts.
12. **The crash marker** `submit_attempted_at` is stamped immediately before the `POST` and **never touched by a claim**; a completion for 401, 403, 429 or a pre-call refusal (an unmapped scheme) restores it to its pre-claim value — those prove the provider did not take the document.
13. **The UBL reuse rule**: a new send reuses the bytes of the newest transmission a person resolved as failed, among all of the document's (`failed` with `resolved_by_user_id` set) — it was `unconfirmed`, and its bytes may have reached the receiver, so a reused send that is later cancelled still leaves a third send carrying them (amended after the Task 7 review: not only the latest row); without one, after any other `failed` or `cancelled` the send renders fresh. (While a row is `queued`, `submitted`, `delivered` or `unconfirmed`, no new send exists.)
14. `ehf_preferred` is server-emitted: `sendWarnings` emits it **instead of** `delivery_preference_ehf` when `canSendEhf` **and the document's `ehf.blockedBy` is empty** (amended after the Task 7 review: a document that cannot go as EHF keeps the old warning); the e-mail dialog treats it as loud.
15. The `ehf` block carries `preference` (the profile's `InvoiceDelivery`) and `buyerPeppolId` for `invoices:issue` holders, read in `GET /invoices/{id}` beside `sendDefaults` but **not gated on mail being available** — an EHF-only installation has no SMTP.
16. The settings' `peppolId`, `kidLength` and `kidAlgorithm` are required-nullable on `PUT /invoices/settings` (every caller sends them; NULL clears), and the KID fit is judged against the request's own `seriesStart`.
17. One Storecove account or key per installation: the drain acknowledges every event it reads, and a shared account would lose another system's events; the admin page says so.
18. The Peppol artefact is pinned at `v3.0.20`, the newest tag; 3.0.21 is adopted the day it is tagged (a pin bump).

## File Structure

| File | Responsibility |
| --- | --- |
| `srv/db/migrations/00036_invoices_ehf_kid.sql`, `srv/db/schema_test.go` | the schema (Task 2) |
| `srv/invoices/kid/{kid.go,kid_test.go}`, `srv/invoices/{settings.go,issue.go,pdf.go,mailtext.go,csvexport.go}`, `queries/{settings,invoices,export,transmissions,credentials}.sql`, `openapi/invoices.yaml` | KID and the settings fields (Task 2) |
| `srv/peppol/{smp.go,doc.go}`, `srv/config/config.go`, `srv/invoices/{server.go,contractscalls.go,meta.go,peppolerr.go}` | the client, the switches, meta (Task 3) |
| `srv/invoices/ehf/{doc.go,units.go,eas.go,writer.go,render.go,precheck.go,*_test.go,testdata/golden,testdata/invalid/{*.xml,manifest.json}}`, `srv/invoices/ehfdoc.go` | the document (Task 4) |
| `tools/ehf/{validate.sh,Validate.java,artefacts.lock,README.md}`, `mise.toml`, `.github/workflows/ci.yml`, `CONTRIBUTING.md`, `docs/…/en/contributing/e-invoice-validation.md` | the oracle (Task 5) |
| `srv/invoices/accesspoint/{accesspoint.go,storecove.go,storecove_test.go}`, `srv/invoices/accesspoint/storecovetest/server.go`, `srv/invoices/{credentials.go,credentials_test.go}` | the port, the adapter, the credentials (Task 6) |
| `srv/invoices/{sendehf.go,transmissions.go,responses.go,list.go,drafts.go,send.go,module.go}` | the send, cancel, resolve, the UBL download, the `ehf` block, the warning (Task 7) |
| `srv/invoices/{ehf_worker.go,ehf_events_worker.go,*_test.go}` | the workers (Task 8) |
| `srv/invoices/customer_slots.go`, `srv/integration/{harness_test.go,ehf_test.go}` | the slots and the integration test (Task 9) |
| `apps/invoices/frontend/src/**` | the app (Task 10) |
| `docs/src/content/docs/{en,nb}/…`, `ROADMAP.md` | the docs (Task 11) |

---

### Task 1: The Storecove contract spike — done

Its record is §Spike findings at the end of this plan; spec D7/D9 were revised on it. **Storecove's test account is a sales-contact form with a thirty-day period** — the user requests it on day one; every task runs against `storecovetest`; the tagged sandbox test (Task 6) runs when `STORECOVE_SANDBOX_API_KEY` is set. Nothing to commit.

### Task 2: The migration, KID, and the settings' new fields (D2, D3, D7's table, D9's table)

**Files:** create the migration, `srv/invoices/kid/{kid.go,kid_test.go}`, `queries/transmissions.sql`, `queries/credentials.sql`; modify `schema_test.go`, `sqlc.yaml`, `settings.go`, `settings_test.go`, `issue.go`, `issue_test.go`, `pdf.go`, `pdf_internal_test.go`, `mailtext.go`, `mailtext_internal_test.go`, `csvexport.go`, `csvexport_test.go`, `queries/{settings,invoices,export}.sql` (`ExportRows` lists its columns: add `i.kid`), `openapi/invoices.yaml`, `apps/invoices/frontend/src/pages/settings.tsx` (`SellerForm` sends the three new fields, round-tripping what it read) and `test/fixtures.ts`, the reference page's model and settings rows, both user pages' settings sentence; generated files.

**The migration**, each plpgsql block in goose markers, with a Down written out as `00035`'s is:

```sql
ALTER TABLE invoices.settings
    ADD COLUMN peppol_id     varchar(60),
    ADD COLUMN kid_length    smallint,
    ADD COLUMN kid_algorithm varchar(5),
    ADD CONSTRAINT ck_settings_kid CHECK (
        (kid_length IS NULL AND kid_algorithm IS NULL)
        OR (kid_length BETWEEN 4 AND 25 AND kid_algorithm IN ('mod10','mod11')));
UPDATE invoices.settings SET peppol_id = '0192:' || organisation_number
    WHERE organisation_number ~ '^[0-9]{9}$' AND peppol_id IS NULL;

ALTER TABLE invoices.invoices
    ADD COLUMN kid           varchar(25),
    ADD COLUMN kid_algorithm varchar(5),
    ADD CONSTRAINT ck_invoices_kid CHECK ((kid IS NULL) = (kid_algorithm IS NULL)),
    ADD CONSTRAINT ck_invoices_kid_algorithm CHECK (kid_algorithm IS NULL OR kid_algorithm IN ('mod10','mod11')),
    ADD CONSTRAINT ck_invoices_kid_kind CHECK (kid IS NULL OR kind = 'invoice');

CREATE TABLE invoices.access_point_credentials (
    id                integer     PRIMARY KEY,
    provider          varchar(20) NOT NULL,
    settings_json     text        NOT NULL DEFAULT '{}',
    secret_ciphertext text        NOT NULL,
    rejected_at       timestamptz,
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
    receiver_participant varchar(100) NOT NULL,
    document_type        varchar(300) NOT NULL,
    process_id           varchar(100) NOT NULL,
    ubl_object_key       varchar(300) NOT NULL,
    ubl_sha256           char(64)     NOT NULL,
    pdf_sha256           char(64)     NOT NULL,
    status               varchar(20)  NOT NULL DEFAULT 'queued',
    provider_ref         varchar(200),
    evidence_object_key  varchar(300),
    evidence_sha256      char(64),
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
    CONSTRAINT ck_transmissions_provider CHECK (provider IN ('storecove')),
    CONSTRAINT ck_transmissions_status CHECK (status IN ('queued','submitted','delivered','failed','unconfirmed','cancelled')),
    CONSTRAINT ck_transmissions_stamps CHECK (
        (status <> 'submitted' OR submitted_at IS NOT NULL) AND
        (status <> 'delivered' OR delivered_at IS NOT NULL) AND
        (status <> 'failed' OR failed_at IS NOT NULL) AND
        (status <> 'cancelled' OR cancelled_at IS NOT NULL)),
    CONSTRAINT ck_transmissions_resolution CHECK ((resolved_by_user_id IS NULL) = (resolution_note IS NULL)),
    CONSTRAINT ck_transmissions_evidence CHECK ((evidence_object_key IS NULL) = (evidence_sha256 IS NULL))
);
CREATE UNIQUE INDEX ux_transmissions_active ON invoices.transmissions (invoice_id)
    WHERE status IN ('queued','submitted','delivered','unconfirmed');
CREATE INDEX ix_transmissions_due ON invoices.transmissions (status, next_attempt_at)
    WHERE status IN ('queued','submitted','unconfirmed')
       OR (status = 'delivered' AND evidence_object_key IS NULL AND provider_ref IS NOT NULL);
```

Triggers: `invoices.guard_transmission_insert()` BEFORE INSERT — `SELECT status, customer_id … FOR SHARE`; refuse unless issued (`'invoices: a transmission needs an issued document'`); refuse when the customer is in `erased_customers` (`'invoices: the customer is anonymised'`), checked after the wait; `tr_transmissions_parent`. `invoices.refuse_transmission_change()` BEFORE UPDATE OR DELETE — DELETE raises `'invoices: a transmission is never deleted'`; UPDATE allowed only when the row as jsonb less **the state columns** — `status, provider_ref, evidence_object_key, evidence_sha256, submit_attempts, poll_attempts, next_attempt_at, submit_attempted_at, lease_id, lease_until, last_error, lookup_registered, lookup_can_receive, lookup_at, submitted_at, delivered_at, failed_at, cancelled_at, resolved_by_user_id, resolution_note` — is unchanged, **and**: `OLD.status IN ('failed','cancelled')` → refuse; `OLD.status = 'delivered'` → allowed only while `OLD.evidence_object_key IS NULL`, changing nothing but `evidence_object_key`/`evidence_sha256` (once), the lease, `next_attempt_at`, `poll_attempts`, `last_error` (status and `delivered_at` frozen); else raise `'invoices: a transmission is immutable'`; `tr_transmissions_immutable`. The 1A document trigger freezes `kid`/`kid_algorithm` (set in the draft→issued UPDATE).

**Interfaces:** `kid` package: `const Mod10, Mod11`; `func CheckMod10(digits string) byte`, `func CheckMod11(digits string) byte` (`'-'` on remainder 1); `func Compute(number int64, length int, algorithm string) (string, error)` (`ErrLengthExceeded`); `func Verify(kid, algorithm string, number int64) bool`; `func Fits(next int64, length int) (fits, headroomLow bool)`. `settings.go`: `peppolId` validation and default, the KID pair's rules against `nextNumber(q)` or the request's `seriesStart`, the `kid_headroom_low` warning; the three fields required-nullable in the request. `issue.go`: after `AllocateNumber`, when the agreement is set and the kind is invoice, `kid.Compute` → `kid_length_exceeded` (a `cannotIssue`; the number rolls back); `IssueDocument` gains the two columns. `pdf.go`: the "KID" line, the stored KID verified with `kid.Verify` against `kid_algorithm` (a mismatch is an error → 500); `mailtext.go`: the KID sentence; `csvexport.go`: the `KID` column last. **Queries written now** (sqlc needs them): `transmissions.sql` — `InsertTransmission`, `TransmissionsOf`, `LatestTransmission`, `ActiveTransmissionExists`, `ClaimTransmission` (`@now`, the due predicate incl. the delivered-without-evidence case, `lease_until < @now`), `GetTransmission`, `MarkSubmitAttempted`, `RestoreSubmitAttempted`, `MarkSubmitted` (`AND status = 'queued' AND lease_id = @lease`), `MarkSubmittedWithoutRef`, `MarkFailedLeased`, `MarkUnconfirmedLeased`, `RescheduleLeased`, `RefreshTransmissionLookup`, `MarkDeliveredLeased`, `SetEvidenceLeased`, `ApplyEventDelivered` (`WHERE (id = @id OR idempotency_key = @key) AND status IN ('queued','submitted','unconfirmed')`), `ApplyEventFailed`, `AnyAwaitingEvents`, `CancelUnattemptedTransmission` (`@now`), `ResolveTransmission`, `ActiveTransmissionsCount`, `CancelCustomerUnattemptedTransmissions`, `TransmissionsOfDocuments`; `credentials.sql` — `GetAccessPointCredentials`, `UpsertAccessPointCredentials`, `DeleteAccessPointCredentials`, `MarkAccessPointRejected`, `ClearAccessPointRejected`.

**Contract:** `InvoicesSettingsRequest` gains `peppolId`, `kidLength`, `kidAlgorithm` (nullable, required); the response the same plus `warnings` (`kid_headroom_low`); the document response `kid`, `kidAlgorithm` (optional); `InvoicesConflictProblem.code` gains `kid_length_exceeded`.

- [ ] **Step 1: Schema tests first** — `TestInvoicesEhfKid_AppliesAndIsIdempotent` (`applyUpDownUp(t, url, 36)` plus custom `DownTo(35)` steps where the helper cannot assert: the columns and CHECKs; both tables; the two triggers with their messages — an insert under a draft, under an erased customer, a DELETE, an UPDATE of `ubl_sha256`, an UPDATE of a `failed` row, a `delivered` row's lease and evidence set once, a `delivered` row's status refused, the `lookup_*` columns updatable; `ux_transmissions_active`'s predicate; `ix_transmissions_due`'s predicate incl. the delivered case; the backfill from a planted settings row; Down removes all); widen the 1A/1B baseline tests' table and trigger counts; the reserved-word test at 36. Red.
- [ ] **Step 2:** the migration and `sqlc.yaml`; the queries; generate twice.
- [ ] **Step 3: KID** — `TestKID_TheSpecificationsWorkedExamples` (`12345678` → `123456782` MOD10, `123456785` MOD11), `TestKID_Mod11RemainderOneIsAHyphen`, `TestKID_ComputeVerifyAndFits`. Red; `kid.go`; green; shown able to fail by swapping the weights.
- [ ] **Step 4: Settings** — `TestSettings_PeppolIdDefaultsAndValidates`, `TestSettings_TheKidAgreement` (bounds; the fit rule against the counter, against `series_start`, and against the request's own `seriesStart`; the headroom warning; clearing), `TestSettings_PeppolIdIsBackfilled`, `TestSettings_TheThreeFieldsAreRequiredNullable`. Implement; patch `SellerForm` to round-trip them; fixtures; typecheck.
- [ ] **Step 5: The KID at issue and where it goes** — `TestIssue_AKIDUnderTheAgreement` (both algorithms; none on a credit note; none before the agreement; `kid_length_exceeded` rolls the number back after shortening the agreement by SQL), `TestPDF_TheKIDLineAndTheStoredAlgorithm` (render after the agreement changes; a tampered KID — `ALTER TABLE invoices.invoices DISABLE TRIGGER tr_invoices_immutable` in the test — is a 500), `TestCoverMail_TheKIDSentence`, `TestExportCSV_TheKIDColumnLast`, `TestDocument_CarriesItsKID`. Implement.
- [ ] **Step 6: Docs** — the reference's model table (the columns, the two tables) and settings section; both user pages' settings sentence (en + nb). `mise run docs:check`.
- [ ] **Step 7:** lint, the suites, generate idempotent, `gen:client`, typecheck; **commit** `feat(invoices): KID under the bank agreement, the seller's Peppol id, and the tables for credentials and transmissions`; then `check-coverage.ts --base HEAD~1`.

### Task 3: The Peppol client in Invoices, the switches, and meta (D1, D6)

**Files:** modify `srv/peppol/{smp.go,doc.go}` (export `InvoiceDocumentType`, `CreditNoteDocumentType`; add `BillingProcessID`; a test pinning the three), `srv/config/{config.go,config_test.go}` (`InvoicesEhfEnabled`, `boolean(…, true)`; `InvoicesStorecoveBaseURL`, default `https://api.storecove.com/api/v2/`, `http` or `https` as `brregBaseURL` accepts, trailing slash trimmed), `srv/invoices/{server.go,contractscalls.go,meta.go,meta_test.go}`, create `srv/invoices/peppolerr.go` (`peppolErrorKind`, `peppolDNSServers` copied from customers with their tests), `openapi/invoices.yaml`, `docs/…/admin/authentication.md` (en + nb: the configuration rows), both user pages (one sentence: EHF availability on the settings page), the reference's meta row; fixtures; generated files.

**Interfaces:** `server.peppolLookup` built as customers does (nil when the lookup is disabled); `contractscalls.go`: `lookupReceiver(ctx, participant) (peppol.Result, error)` with `noteContractCall(ctx, "Peppol.Lookup")`, failures logged by kind; `meta.go`: `ehfAvailable` (the switch, the lookup enabled, the credentials row, `peppol_id`), `accessPointCredentialsRejected` (`rejected_at IS NOT NULL`), `capabilities.canSendEhf`.

- [ ] **Step 1:** `TestPeppol_TheBillingIdentifiersArePinned`; `TestLoad_InvoicesEhfSettings`; `TestMeta_EhfAvailabilityAndCanSendEhf` (each precondition toggled; the rejected flag). Red; implement; green.
- [ ] **Step 2:** docs; fixtures; typecheck; **commit** `feat(invoices): the Peppol client as the send's re-check, and the e-invoicing switches on meta`; the per-commit check.

### Task 4: The EHF document: the model, the writer, the units, the pre-check, the goldens (D4, D5, D11's pre-check)

**Files:** create `srv/invoices/ehf/{doc.go,units.go,units_test.go,eas.go,eas_test.go,writer.go,render.go,render_test.go,precheck.go,precheck_test.go,testdata/golden/*.xml,testdata/invalid/*.xml,testdata/invalid/manifest.json}`, `srv/invoices/ehfdoc.go` (+ test); modify the reference page (the mapping table, the units section).

**Interfaces:** `ehf.Document` (`doc.go`) holds `big.Rat` and strings only — parties, lines, VAT rows, totals, the KID and its algorithm, the PDF bytes and name, the seller Peppol id, the original's number and date for a credit note; `invoices/ehfdoc.go`: `func ehfDocumentOf(inv store.InvoicesInvoice, lines, sums, original *store.InvoicesInvoice, sellerPeppolID string, pdf []byte) (ehf.Document, error)`. `ehf.Render(d) ([]byte, error)`; `ehf.UnitCode(unit) string`; `ehf.EASScheme(scheme) bool` (the full EAS list, version-stamped); `ehf.Precheck(xml []byte) (Refusals []Rule, err error)` — the user-facing rules read **from the XML alone** (R003; the buyer endpoint scheme; a K category; NO-R-001's shape) so it runs on the invalid fixtures too; `ehf.Invariants(xml []byte, d Document) ([]Rule, error)` — the module-only checks (BR-CO-10/13/15 re-summed; `kid.Verify` on the document's KID; the attachment present; every unit code in the table) → a 500 at send.

- [ ] **Step 1:** units and EAS tests first (every table entry; the fallback; the punctuation strip; `t` unmapped; `0192`, `0088`, `0208` true, `9999` false). Implement.
- [ ] **Step 2:** the read-back tests first (one per mapping row of D4, incl. the category rules and the credit-note rows), then `doc.go`, `writer.go`, `render.go`, `ehfdoc.go`; green; the goldens (D11's fixtures, with **a fixed small PDF blob**, not the renderer) written with `-update` and committed; `TestEHF_Goldens` green without the flag; `TestEHF_IsPure`; `TestEHF_DiffersWhenTheSellerPeppolIdChanges`.
- [ ] **Step 3:** the pre-check tests first — each refusal rule on a tampered XML, each invariant on a tampered Document — then `precheck.go`; the invalid fixtures (no buyer reference; a malformed endpoint scheme; tampered totals; a reason on Z; a percent on O; a K line) and `manifest.json`; `TestPrecheck_AgreesWithTheManifest` (every rule the manifest lists that the pre-check knows is reported; `-update` does **not** rewrite the invalid fixtures).
- [ ] **Step 4:** the reference's mapping table and units section; `docs:check`; **commit** `feat(invoices): the EHF document — a deterministic UBL 2.1 render with the PDF embedded, the unit table, and the pre-check`; the per-commit check.

### Task 5: The oracle: Java, Saxon, the artefacts, the CI job, the contributing page (D11)

**Files:** create `tools/ehf/{validate.sh,Validate.java,artefacts.lock,README.md}`, `docs/src/content/docs/en/contributing/e-invoice-validation.md`; modify `mise.toml` (`java = "temurin-21.0.12+8.0.LTS"` in `[tools]` — an exact pin like the others; `[tasks."ehf:validate"]`), `.github/workflows/ci.yml` (a job `🧾 Validate EHF goldens` with `install_args: "java"`, the others' `needs`/`permissions`/`persist-credentials: false` shape, `actions/cache` pinned by SHA keyed on `artefacts.lock`; `install_args: "bun"` added to the three jobs that today install everything, so no JDK downloads there), `CONTRIBUTING.md` (the `mise install` note), `.gitignore` (`tools/ehf/.cache`), the contributing overview.

**`validate.sh`**: downloads into `tools/ehf/.cache` and verifies against `artefacts.lock` (SHA-256 each): `Saxon-HE-12.7.jar` **and its `org.xmlresolver:xmlresolver` (+ `xmlresolver-data`) dependency** from Maven Central; SchXslt (pinned) — unless the OpenPEPPOL release ships compiled XSLT, which Step 1 checks first and records; `en16931-ubl-1.3.16.zip` (compiled XSLT inside); the Peppol repository zipball at `v3.0.20`; the UBL 2.1 XSD zip from OASIS. Compiles `PEPPOL-EN16931-UBL.sch` once (SchXslt handles its foreign `xsl:function`s — `u:mod11`, `u:gln`, …) and caches by the lock's hash. `Validate.java` (one file, run with `java Validate.java …`): picks the Invoice or CreditNote XSD by the root element, validates with `javax.xml.validation`, runs the two XSLTs through Saxon, **parses the SVRL itself** and fails on any `flag="fatal"`, prints warnings, and for `testdata/invalid/` compares the fatals against `manifest.json`. The script loops the goldens and the invalid fixtures and prints one summary line. `README.md` says how to bump a pin.

- [ ] **Step 1:** write and run locally (`mise install` pulls Java; cache the artefacts); every golden green, every invalid fixture tripping its manifest — **correct the manifest from the oracle's real output where the research guessed a rule id**, and fix the writer where the oracle disagrees; report both.
- [ ] **Step 2:** the mise task, the CI job and the `install_args` edits, the contributing page (what it checks, how to run it, how to bump the artefacts, where the fixtures live), CONTRIBUTING's line; `docs:check`.
- [ ] **Step 3: commit** `ci(invoices): the EHF oracle — the official XSD and Schematron artefacts over the goldens, in CI and on demand`; the per-commit check.

### Task 6: The access-point port, the Storecove adapter, the test server, and the credentials (D7)

**Files:** create `srv/invoices/accesspoint/{accesspoint.go,storecove.go,storecove_test.go}`, `srv/invoices/accesspoint/storecovetest/server.go` (a non-test package: `storecovetest.New(t) *Server` — an `httptest` Storecove speaking the OpenAPI shapes; `Server.Enqueue(event)`, `Server.Submissions()`, `Server.Fail(mode)`, `Server.Evidence(guid, …)`; documented test-only), `srv/invoices/{credentials.go,credentials_test.go}`; modify `srv/invoices/{server.go,contractscalls.go,harness_test.go}` (the harness points `INVOICES_STORECOVE_BASE_URL` at the test server through `WithEnv`), `openapi/invoices.yaml`, the reference's endpoints and permissions rows, both user pages (the settings' access-point card sentence), fixtures; generated files.

**Interfaces:** `accesspoint.AccessPoint` — `Submit(ctx, Submission) (SubmissionRef, error)`, `NextEvent(ctx) (Event, bool, error)`, `AckEvent(ctx, eventID string) error`, `Evidence(ctx, ref) (Evidence, error)`, `Verify(ctx) error`; `Event{ID, SubmissionRef, IdempotencyKey uuid.UUID, State, At, Reason}` (`succeeded → delivered`, `failed`/`no_action_taken → failed`, else `submitted`; the event `body` is a JSON string inside the JSON — decoded twice); `Evidence{ReceiptJSON []byte, Delivered []byte, DeliveredMIME string, MessageID, ReceivingAP string}` (every document URL fetched at once, `https` only, `io.LimitReader` 20 MiB, under the call's one 30-s deadline) or `ErrNotYetAvailable`; errors `ErrUnprocessable{Messages}` (422), `ErrUnauthorized`, `ErrThrottled{RetryAfter}`, `ErrUnmappedScheme` (pre-call), and transport/5xx. `accesspoint.NewStorecove(baseURL, apiKey string, legalEntityID int, transport http.RoundTripper) AccessPoint`: `POST document_submissions` `{legalEntityId, idempotencyGuid, routing: {eIdentifiers: [{scheme: "NO:ORG", id}]}, document: {documentType: "invoice", rawDocumentData: {document: <base64>, parseStrategy: "ubl"}}}`; `{guid}`; `GET webhook_instances/`, `DELETE webhook_instances/{guid}`; `GET document_submissions/{guid}/evidence/sending`; `Verify` = `GET legal_entities/{id}`; no retries; 30 s per call. `contractscalls.go`: `accessPoint(ctx) (accesspoint.AccessPoint, error)` opening the key, every method wrapped with `noteContractCall("AccessPoint.<Method>")`. `credentials.go`: the three endpoints of D7 and the rejected flag on the response.

**Contract:** `putInvoicesSettingsAccessPoint` (`invoices:access+invoices:manage`; body `InvoicesAccessPointRequest {provider, legalEntityId, apiKey?}`; 200 `InvoicesAccessPointResponse {provider, legalEntityId, hasCredentials, rejectedAt?}`; 400; 409 `transmissions_active`), `deleteInvoicesSettingsAccessPoint` (204; 409), `postInvoicesSettingsAccessPointVerify` (200 `{result: ok|unauthorized|unreachable}`; 409 `ehf_unavailable` without credentials).

- [ ] **Step 1:** adapter tests first against `storecovetest`: `TestStorecove_SubmitsTheDocumentShape`, `TestStorecove_MapsTheSchemes`, `TestStorecove_ErrorClasses` (422, 401, 403, 429 with and without Retry-After, 5xx, timeout), `TestStorecove_EventsAndAck` (the double-decoded body; 204), `TestStorecove_EvidenceFetchesTheDocuments` (https only; the limit; 404 → `ErrNotYetAvailable`), `TestStorecove_Verify`. Red; implement; green. A tagged `TestStorecoveSandbox_*` (`//go:build storecove`) submits one invoice and one credit note to the Norwegian test receiver, drains its events and asserts the embedded PDF is in the delivered copy — run only with `STORECOVE_SANDBOX_API_KEY`.
- [ ] **Step 2:** credentials tests first: `TestAccessPoint_TheKeyIsNeverReturned`, `…KeptWhenOmitted`, `…RefusedWhileTransmissionsAreActive`, `…Verify`, `…NeedsManage`, `…AFailedOpenIsA503AndFlags`. Implement.
- [ ] **Step 3:** docs rows; fixtures; typecheck; **commit** `feat(invoices): the access-point port with a Storecove adapter and its test server, and credentials sealed in their own table`; the per-commit check.

### Task 7: The send, cancel, resolve, the UBL download, the `ehf` block, and the warnings (D8, D10, D14)

**Files:** create `sendehf.go`, `sendehf_test.go`, `transmissions.go`, `transmissions_test.go`; modify `responses.go`, `list.go`, `drafts.go`, `send.go` (`ehf_preferred`), `module.go` (`invoices-send-ehf` in `limits`), `export_test.go` (`SetBeforeTransmissionInsert`, inside the transaction after the under-lock judgment), `queries/invoices.sql`, `openapi/invoices.yaml`, the reference's endpoints table and send-order section, both user pages ("Sending as EHF" and the draft warning, one paragraph each); fixtures; generated files.

**Contract:** `postInvoicesByIdSendEhf` (`invoices:access+invoices:issue`; no body; 200 the document without `sendDefaults` — the app invalidates; 404; 409 `invoice_draft` / `customer_anonymised` / `no_peppol_id` / `buyer_reference_missing` / `ehf_already_sent` / `peppol_not_receivable` (+ `peppolRegistered`, `peppolCanReceive`) / `ehf_invalid` (+ `rules[]`); 429 as identity declares it; 502 `peppol_lookup_failed`; 503 `ehf_unavailable` / `storage_unavailable`; 500), `postInvoicesByIdTransmissionsByTransmissionIdCancel` (200 the document; 404; 409 `transmission_not_cancellable`), `…Resolve` (body `InvoicesTransmissionResolution {outcome, note}`; 200; 400; 404; 409 `transmission_not_resolvable`), `getInvoicesByIdTransmissionsByTransmissionIdUbl` (`invoices:access`; 200 `application/xml`; 404; 500 a missing or altered object; 503). Schemas `InvoicesEhfState {status, queuedAt?, submittedAt?, deliveredAt?, failedAt?, providerRef?, reason?, canSend, blockedBy?, preference?, buyerPeppolId?, transmissions[]}`, `InvoicesTransmission {id, status, provider, idempotencyKey, receiverParticipant, ublSha256, queuedAt, submittedAt?, deliveredAt?, failedAt?, cancelledAt?, providerRef?, reason?, resolvedByUserId?, resolutionNote?, ublUrl}`; the document gains `ehf`, the list item `ehfStatus`; warnings `ehf_buyer_reference_missing`, `ehf_preferred`.

- [ ] **Step 1:** tests first — `TestSendEhf_UnavailableIsJudgedFirst`, `…RefusesADraftAndAnAnonymisedCustomer` (and again under the lock: the erase committing between the read and the lock → 409, never a 500), `…NoPeppolId`, `…BuyerReferenceMissing`, `…AlreadySent`, `…PrecheckBeforeTheLookup`, `…TheReceiverRecheck` (not registered; cannot receive a credit note; the lookup error 502 logged by kind), `…StoresTheUblOnce`, `…ReusesTheBytesAfterAResolvedUnconfirmed`, `…RendersFreshAfterAFailure`, `…TwoRacingSendsOneQueued` (held on the seam; the second blocks on `LockInvoice` and is refused; with the lock removed the index refuses it), `…TheRateLimit`, `…NeedsIssue`, `TestDrafts_WarnsEhfBuyerReferenceMissing`, `TestSend_EhfPreferredReplacesThePreferenceWarning`, `TestTransmissions_CancelOnlyNeverAttempted` (incl. losing to a claim), `TestTransmissions_ResolveOnlyUnconfirmed`, `TestTransmissions_UblDownload`, `TestDocument_TheEhfBlockAndBlockedBy`, `TestList_EhfStatus`. Red; implement; green.
- [ ] **Step 2:** docs; fixtures; typecheck; **commit** `feat(invoices): send as EHF — re-checked against the network, queued under the document's lock, with cancel, resolve and the UBL download`; the per-commit check.

### Task 8: The two workers (D9)

**Files:** create `ehf_worker.go`, `ehf_events_worker.go`, `ehf_worker_test.go`, `ehf_events_worker_test.go`; modify `module.go` (`Workers`), `export_test.go` (constructors exported for the external test package: `NewEhfWorkerForTest`, or the workers exported outright with `ProcessOne`/`RunCycle`), `docs/…/en/reference/invoices.md` (the worker section), `admin/installation.md` (en + nb: the two workers in the workers list).

**The submit/probe worker** (`invoices-ehf`, `Interval` 5 s): `ClaimTransmission(@now)` with the due predicate; per claim exactly one of —
- `queued`: when `lookup_at` older than 24 h, `lookupReceiver` first (`RefreshTransmissionLookup`; not receivable → `MarkFailedLeased` with `receiver_not_receivable`, the marker untouched, and stop); then `MarkSubmitAttempted(@now)` immediately before `Submit`; on success `MarkSubmitted` (with `submitted_at`); `ErrUnprocessable` with the marker previously NULL → `MarkFailedLeased` with the messages; with the marker already set → `MarkSubmittedWithoutRef` (`submitted_at = @now`); transport/5xx → `RescheduleLeased` (`submit_attempts + 1`, backoff `min(3600, 2^n)` s); `ErrThrottled` → reschedule at its retry-after, `RestoreSubmitAttempted`, no attempt counted; `ErrUnauthorized` → reschedule at 3600 s, `RestoreSubmitAttempted`, `MarkAccessPointRejected`, an error log, no attempt counted; `ErrUnmappedScheme` → `RestoreSubmitAttempted` then `MarkFailedLeased`; a failed `Open` → reschedule 3600 s, `MarkAccessPointRejected`, error log; **the age cap**: `queued_at` older than 48 h → `MarkUnconfirmedLeased` when the marker is set, `MarkFailedLeased` otherwise; a successful call clears `rejected_at`.
- `submitted` with a reference: `Evidence` → 200 → `MarkDeliveredLeased` (`delivered_at = @now`, `poll_attempts` reset); `ErrNotYetAvailable` → reschedule by the cadence (5 min, 15 min, then hourly by `poll_attempts`); seven days after `submitted_at` → `MarkUnconfirmedLeased`. `submitted` without a reference: no probe; reschedule hourly; seven days → `unconfirmed`.
- `delivered` without evidence: `Evidence` → stored once (`Exists` before `Put`; keys `documents/<id>/<number>-<transmission>-receipt.json` and `…-delivered.xml`; `evidence_sha256` of the receipt) → `SetEvidenceLeased`; a failure → reschedule by the cadence.
- `unconfirmed` with a reference: `Evidence` once a day for thirty days → 200 resolves it to `delivered` (`resolved_by_user_id` NULL, a note "resolved by the provider's evidence"); after thirty days `next_attempt_at = 'infinity'`. Without a reference: `next_attempt_at = 'infinity'` at once.
Every completion is `… WHERE id = @id AND lease_id = @lease AND status = @claimed`; 0 rows means another actor moved the row — log at debug, nothing else. The adapter is built per claim from the credentials row (`accessPoint(ctx)`); a lookup-disabled installation still probes and drains but never submits: a `queued` row is left alone and logged once per cycle (the send already refused when the lookup was disabled).

**The events worker** (`invoices-ehf-events`, `Interval` 30 s): under an advisory lease (`pg_try_advisory_lock(0x494E5645484631)`, "INVEHF1", the customers shape), when `AnyAwaitingEvents` (any `submitted`, `unconfirmed`, or `queued` with the marker set): `NextEvent` until 204 — each applied **idempotently and without a row lease**: `delivered` → `ApplyEventDelivered` (`WHERE … AND status IN ('queued','submitted','unconfirmed')`, setting `provider_ref` when missing); `failed` → `ApplyEventFailed` likewise; `submitted` → nothing; 0 rows (already terminal, or no row of ours) → log by guid; **always `AckEvent`**. Exported `RunCycle` for tests.

- [ ] **Step 1:** tests first against `storecovetest` and the fake lookup: claim and lease (two workers, one submission); the submit claim's lookup refresh and `receiver_not_receivable`; the marker stamped before the POST and restored on 401/429/unmapped scheme; the 422 rule's two branches; backoff on 5xx; `ErrThrottled`'s retry-after; `ErrUnauthorized` flags and does not count; the age cap by the marker; the probe cadence; `delivered` then evidence (a failing fetch retried; the lease-changed no-op; `Exists` before `Put`); the seven days; the daily unconfirmed probe resolving, and `'infinity'` after thirty days; a failed `Open`; worker-mode `Deps`; the switch off → no worker. Events: match by ref and by key; the duplicate event; an event after the probe delivered; an event for a failed row; an event for a leased row; an unknown event acked; stop at 204; two workers, one drain. Red; implement; green; shown able to fail by removing the lease check.
- [ ] **Step 2:** docs; **commit** `feat(invoices): the invoices-ehf workers — one provider call per claim, the event queue drained under a lease, evidence stored as the record, and an unconfirmed outcome left to a person`; the per-commit check.

### Task 9: The slots and the integration test (D12)

**Files:** modify `customer_slots.go`, `customer_slots_test.go`, `queries/customers.sql` (only what `transmissions.sql` does not already hold); `srv/integration/{harness_test.go,ehf_test.go}`; the reference's retention and anonymisation paragraphs and `reference/customers.md`'s anonymisation list.

- [ ] **Step 1:** export carries transmissions; erase cancels never-attempted queued rows (count) and keeps the rest; run twice zero. Red; implement.
- [ ] **Step 2:** `TestEhf_ARealCustomerIsInvoicedAsEhfEndToEnd` composing customers + invoices with the fake lookup and `storecovetest`: a profile with `peppolId` and `invoiceDelivery: ehf` → issue (a KID) → send-ehf → the submit worker once → `submitted`; an event enqueued → the events worker once → `delivered`; the probe worker once → evidence stored; the `ehf` block and the UBL download.
- [ ] **Step 3:** `docs:check`; **commit** `feat(invoices): transmissions in the export and the erase, and the customers + invoices EHF integration test`; the per-commit check.

### Task 10: The Invoices app (D15)

**Files:** `apps/invoices/frontend/src/**` — `api/settings.ts` (the three fields), `api/access-point.ts` (`putAccessPoint`, `deleteAccessPoint`, `verifyAccessPoint`), `api/ehf.ts` (`sendEhf`, `cancelTransmission`, `resolveTransmission`, `ublUrl`; every mutation invalidates `[INVOICES_QUERY_KEY]`), `pages/settings.tsx` (the **E-invoicing** card with its own readiness line for the Peppol id — never in the "What issuing needs" list — and the access point; the **KID** card with the preview and both warnings), `pages/-send-ehf-dialog.tsx`, `pages/-resolve-transmission-modal.tsx`, `components/ehf-card.tsx`, `pages/invoice.tsx` (the primary action by `ehf.preference`/`buyerPeppolId`; the card; the draft warning), `pages/-send-dialog.tsx` (`ehf_preferred` loud; the cross-channel note), `components/invoice-table.tsx` (the EHF column), `lib/errors.ts` (every D14 code), `i18n.ts`, `test/fixtures.ts` (wire literals: a document with an `ehf` block in each state, transmissions, the access-point response, meta), and tests.

- [ ] **Step 1:** fixtures; tests first — the settings cards (readiness, the key never shown, verify, remove refused, the KID preview and warnings); the primary action by precedence; the send-EHF dialog and each refusal; the E-invoice card through every state incl. cancel, resolve and the download; the draft warning; the e-mail dialog's `ehf_preferred`; the list column; both catalogs. Red; implement; `biome --write`; tests, typecheck, lint, `translations:check`.
- [ ] **Step 2: commit** `feat(invoices-ui): e-invoicing — the settings cards, send as EHF, the transmission card, KID, and channel precedence` with `Docs-Impact: none — the user guide for these screens lands in the next commit`; then Task 11 immediately.

### Task 11: The documentation (D16)

**Files:** `docs/src/content/docs/en/reference/invoices.md` (the two long sections and every table), `en/user/invoices.md` + `nb/user/invoices.md` (the screens of Task 10), **new** `en/admin/e-invoicing.md` + `nb/admin/e-invoicing.md` (`sources`: `apps/server/internal/invoices/accesspoint`, `apps/server/internal/invoices/ehf`; one Storecove account per installation; the sales-contact onboarding; keep the settings' Peppol id equal to Storecove's legal-entity identifier), `en/admin/index.md` + `nb/…`, `en/admin/object-storage.md` + `nb/…` (the `.xml`, receipt and delivered keys), `en/admin/installation.md` + `nb/…` (verify Task 8), `en/reference/customers.md` (verify Task 9), `en/contributing/index.md` (verify Task 5), `ROADMAP.md`; `module.go`'s `invoices:issue` and `invoices:manage` descriptions and the host catalog's en/nb (they now cover EHF and the e-invoicing settings) — a small code edit this task owns, with its tests.

- [ ] **Step 1:** every section D16 lists, English first, then Norwegian; the permission descriptions.
- [ ] **Step 2:** the docs check against the code as 1B's did; `mise run docs:check`; the per-commit check.
- [ ] **Step 3: commit** `docs(invoices): e-invoicing over Peppol and KID — the reference, the user guide, and the administration page`.

### Task 12: Verify the whole branch and open the PR

- [ ] **Step 1:** generate and gen:client → no drift; gofmt; vet; lint; `go test -count=1 ./...`; race on invoices and integration; govulncheck; every frontend package's checks; `mise run docs:check`; `check-coverage.ts --base 00daa9f5` with the trailers' waivers in mind (re-run per commit as each task did); `mise run ehf:validate`; the greps.
- [ ] **Step 2:** the PR in PR #129's shape (What; Decisions — the spec's nineteen readings and this plan's eighteen; Things to know — the Java/Saxon CI job, the Storecove onboarding and one-account rule, the KID agreement, the switch defaults, the artefact pin; How it was built; Verification). `gh pr create --base main --head feat/invoices-ehf-peppol-kid`. Watch CI; do not merge.
- [ ] **Step 3: Report.**

## Spike findings (Task 1)

From Storecove's OpenAPI 2.0 document (`https://api.storecove.com/api/v2/openapi.json`) and its documentation, read verbatim on 2026-10-04:

| Item | Answer | Confidence |
| --- | --- | --- |
| Raw UBL | `document.rawDocumentData.document` (base64) + `parseStrategy: "ubl"`; `document.documentType: "invoice"`; `parse` is deprecated | High |
| Pass-through | **No**: parsed into Storecove's model, outbound UBL regenerated; `ubl_sha256` is what we sent, the evidence's delivered copy is what arrived | High |
| Sender | `legalEntityId` (integer), sibling field; one API key spans several legal entities (`legalEntityId` per call) | High |
| Receiver | `routing.eIdentifiers: [{scheme, id}]`; Norway's scheme is **`NO:ORG`** (tax `NO:VAT`); test receiver `NO:ORG` / `010101018` | High |
| Idempotency | `idempotencyGuid` (36 chars), sibling field; a duplicate is a 422; window and scope **undocumented** | High / UNCERTAIN |
| Attachments | none on the raw-UBL path; whether an embedded PDF survives the regeneration is **unstated** — the sandbox test asserts it | Medium |
| Response | 200 `{guid}`; 401/403 without a body; 422 `[{source, details}]` for duplicate and validation alike; **no 429, no 5xx, no rate limit documented** | High |
| Status | **no `GET document_submissions/{guid}`**; webhooks pushed (5-day retry) or pulled: `GET webhook_instances/` → 200 `{guid, body}` (one event; `body` a JSON **string** holding the webhook body, so it is decoded twice) or 204; `DELETE webhook_instances/{guid}` acknowledges; FIFO in prose; ordering/retention/visibility **undocumented**; every event body carries the submission `guid` and `idempotencyGuid` | High / UNCERTAIN |
| States | `no_action_taken` (no routable receiver), `failed` (final), `succeeded` (corner-3 AS4 receipt — the terminal state for a sender without Invoice Response); the rest gated on CTC/Invoice Response | High |
| Evidence | `GET document_submissions/{guid}/evidence/sending` → JSON `{guid, sender, receiver, network, documents: [{document: <expiring url>, expires_at, mime_type}], evidence: {xml, message_id, receiving_accesspoint, …}}`; 404 before `succeeded` | High |
| Validation | synchronous rule ids documented for France's CTC only; for Peppol/Norway **unstated** | UNCERTAIN |
| Sandbox | same host, a sandbox key; obtained through a **sales-contact form**, thirty days; pricing by contact | Medium |
| Legal entity | `POST legal_entities` `{party_name, line1, city, zip, country, …}` then a `PeppolIdentifier {scheme: "NO:ORG", superscheme: "iso6523-actorid-upis", identifier}` | High |
| Open | whether a UBL `CreditNote` goes through `documentType: "invoice"`; whether the regenerated UBL's supplier `EndpointID` is the legal entity's own identifier (the admin page says to keep them equal) — both for the sandbox test | UNCERTAIN |
| Qvalia | no OpenAPI, no idempotency, no status/evidence API — fewer answers, not more | High |

## Self-review

**Spec coverage.** D1 → Task 3; D2, D3 → Task 2; D4, D5 → Task 4; D6 → Task 3; D7 → Task 6; D8, D10, D14 → Task 7; D9 → Task 8 (and Task 2's schema); D11 → Tasks 4 and 5; D12 → Task 9; D13 → nothing adds it; D15 → Task 10; D16 → Task 11 and each task's docs step. Every Testing bullet names a test above; the spec's "`ErrAlreadySubmitted` with and without a reference" cannot exist against Storecove and is replaced by the 422 rule's two tests.

**Name consistency.** SQL: `invoices.access_point_credentials` (+ `rejected_at`), `invoices.transmissions` (+ `evidence_sha256`), `guard_transmission_insert`, `refuse_transmission_change`, `ux_transmissions_active`, `ix_transmissions_due`, `ck_settings_kid`, `ck_invoices_kid`, `ck_invoices_kid_algorithm`, `ck_transmissions_*`. Go: `kid.Compute`, `kid.Verify`, `kid.Fits`, `lookupReceiver`, `accessPoint`, `ehfDocumentOf`, `ehf.Render`, `ehf.UnitCode`, `ehf.EASScheme`, `ehf.Precheck`, `ehf.Invariants`, `accesspoint.AccessPoint`, `accesspoint.NewStorecove`, `storecovetest.New`. Wire: `ehf`, `ehfStatus`, `kid`, `kidAlgorithm`, `peppolId`, `kidLength`, `kidAlgorithm`, `ehfAvailable`, `accessPointCredentialsRejected`, `canSendEhf`; the codes and warnings of D14 plus `receiver_not_receivable` as a transmission reason.
