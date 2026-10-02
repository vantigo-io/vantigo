# Invoices — payments, delivery and the export (phase 1B) Implementation Plan

> **For agentic workers:** implement this plan task by task, one implementer per task and a reviewer after it, never in the same context. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Phase 1B of the Invoices module on the branch `feat/invoices-payments-delivery`: payment registrations with a soft removal and SQL immutability; derived states from one SQL function with a Go mirror pinned to it, on every response and as a list filter; e-mail delivery of the stored PDF with the seller as Reply-To, logged in an immutable delivery table and safe against a racing anonymisation; the accountant's CSV export; a stats summary and the host's dashboard card; the customer page's Invoices tab; the real customers + invoices integration test; the one platform change (`mail.Outbound.ReplyTo`); the Invoices app's pages for all of it in en + nb; and the docs. One migration (`00035_invoices_payments_delivery.sql`), seven new operations, no change to any other module's contract or schema.

**Architecture:** everything lands inside `apps/server/internal/invoices` the way 1A built it — a strict oapi-codegen server over `openapi/invoices.yaml`, sqlc over the module's own migrations, `withLockedTx` for every write that locks, `contractscalls.go` as the one door to the directory, the object store and now the SMTP seam. A registration and a removal lock only the invoice row `FOR UPDATE` (`payments.go`); the state is `invoices.document_state(...)` in SQL and `documentState(...)` in Go (`state.go`), with `today` always the Oslo business day from `Deps.Clock()`. A send (`send.go`) is judged, reads the recipient from the directory, loads the stored PDF through the download's own extracted path, builds a `mail.Outbound` and sends through the seam on an uncancellable context, then writes its delivery row with an `INSERT … SELECT` that blanks the recipient when the customer was erased meanwhile (`invoices.erased_customers`, written by the erase under a lock on the customer's documents). The CSV (`csvexport.go`) duplicates the customers file writer with the guard on text columns only. Stats (`stats.go`) map the period's instants to Oslo days. The frontend adds to `@vantigo/invoices-ui` and the host's registries; the integration test composes both real modules.

**Tech Stack:** Go 1.27 (pgx, sqlc 1.31.1, goose, oapi-codegen v2.8.0 strict server), go-mail v0.8.1, PostgreSQL 18, React + Mantine + TanStack Query/Router, vitest, bun, mise.

**Spec:** `docs/superpowers/specs/2026-10-02-invoices-payments-delivery-design.md` (D1–D11, Readings, Out of scope, Testing) — binding. The 1A design and plan are the shapes this copies: `docs/superpowers/specs/2026-09-26-invoices-foundation-design.md`, `docs/superpowers/plans/2026-09-26-invoices-foundation.md`. `srv/` is `apps/server/internal/`.

## Global Constraints

- Branch `feat/invoices-payments-delivery`, cut from `main` at `0e84ab03`; HEAD carries the spec (`a817d5c2`) and this plan. Never commit to `main`, never merge, never `--no-verify`.
- `export TEST_DATABASE_URL=postgres://vantigo:vantigo@127.0.0.1:55432/vantigo_test?sslmode=disable` for every `go test` (`docker compose -f docker-compose.test.yml up -d --wait` starts it; colima must be running).
- Toolchain only through mise: `mise exec -- go …`, `mise exec -- bun …`, `mise exec -- golangci-lint run` from `apps/server`. Capture exit codes before any pipe.
- After any `openapi/*.yaml`, `queries/*.sql`, `sqlc.yaml` or migration change: `cd apps/server && mise exec -- go generate ./...` — never package-scoped (a package-scoped run skips sqlc); a second run must show no new diff. Then from the root `mise exec -- bun run gen:client`, and regenerate `openapi/COVERAGE.md` with `cd apps/server && mise exec -- go run ./internal/openapi/cmd/contract coverage -corpus ../../openapi/testdata/exchanges -out ../../openapi/COVERAGE.md`. Commit every generated file (`internal/invoices/gen/api.gen.go`, `internal/invoices/store/*.sql.go`, `store/models.go`, `internal/openapi/specs/invoices.yaml`, `apps/invoices/frontend/src/api-schema.d.ts`, `openapi/COVERAGE.md`).
- Never edit `openapi/testdata/exchanges/*.jsonl`. Invoices has no corpus; `contracttest.RequireCoverage` in `internal/invoices/main_test.go` gates every operation — each new operation must answer 2xx in the module's own tests (the integration package counts for nothing).
- Every existing schema changes additively. No other module's migration, contract or Go changes, except the platform `srv/mail` (Task 1) and the test-only `srv/integration` (Task 9).
- **Forbidden git commands:** `git add -A`, `git add .`, `git stash`, `git checkout -- .`, `git restore .`, `git clean`, `git reset --hard`, `git commit --amend`, `--no-verify`. Commit by pathspec; check `git show --stat HEAD` and that `git status --short` shows nothing of yours left. One implementer commits at a time.
- Every commit message ends with exactly `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>`. Conventional Commits, scopes `invoices`, `invoices-ui`, `frontend`, `mail`, `integration`, `docs`.
- **No directory, object-store or SMTP call inside a locked transaction.** Every one goes through `contractscalls.go`; the harness fails any test that makes one under `withLockedTx`.
- **Exact decimal** everywhere money is: `math/big.Rat` / `pgtype.Numeric` / SQL `numeric`; a JSON number through `strconv.FormatFloat(f, 'f', -1, 64)` and `big.Rat.SetString`; two decimals half away from zero. Money is **absent, not null** when it does not apply.
- **The Oslo business day** from `Deps.Clock()` through `businessDay` (`values.go`), never `CURRENT_DATE`.
- **One refusal rule:** 404 bare for what does not exist; 403 the access layer's; 400 `invalid(...)` naming the field; 409 `conflict(code, title, detail)` with the spec's codes; 502/503 in the same conflict shape (`mail_failed`, `mail_unavailable`, `storage_unavailable`); 429 the limiter's.
- **Frontend rules:** at least one fixture per resource is a wire literal; every new string in both catalogs (`en` + `nb`) and `mise exec -- bun run translations:check` passes; a Mantine `Select` is a `combobox` in tests; never assert "the last fetch" — find the call by method and URL; `mise exec -- bunx biome check --write <files>` on touched files before committing; run each package's tests alone (`mise exec -- bun run --cwd <pkg> test`).
- Every new test must be shown able to fail (break the guard it pins, see red, restore) and the report says so.

**Parallelism.** Task 1 (mail) is independent. Tasks 2 → 3 → 4 → 5 → 6 → 7 → 8 run in order on the server, each on the previous one's contract and generated code. Task 9 (integration) follows Task 6 (it needs the erase and the send). Task 10 (docs) follows Task 8 and is checked against the code. Task 11 (invoices-ui) needs the `api-schema.d.ts` Task 8 commits and may run beside Tasks 9–10; Task 12 (host) follows Task 11 (it imports the panel). Task 13 verifies the whole branch. The one-committer rule holds throughout.

---

**How this plan reads the spec where it leaves a choice open**, each for the user's verdict (Task 13 repeats them):

1. The list's `credited` and `paid` come from a `LEFT JOIN LATERAL` (one subquery per aggregate) rather than correlated subqueries repeated inside `document_state(...)`: one text, read once per row.
2. `openAmount` on a list item is `gross − credited − paid` as a plain `numeric` column of the same lateral join, not a second function.
3. `LivePaymentsSum` is one query (`coalesce(sum(amount), 0)::numeric(14,2) … WHERE invoice_id = $1 AND removed_at IS NULL`), used by the registration, the removal, the response and the lateral join's text alike.
4. The delivery row's `INSERT … SELECT CASE WHEN EXISTS (SELECT 1 FROM invoices.erased_customers e WHERE e.customer_id = i.customer_id) THEN '' ELSE @recipient END … FROM invoices.invoices i WHERE i.id = @invoice_id` reads the document's current `customer_id` — a merge between the send's read and its row re-points the document, and the marker is then the survivor's, which is right.
5. The send's `context.WithoutCancel` wraps steps 7 and 8 only; the reads before them stay cancellable.
6. The rate-limit policy is `ratelimit.Policy{Name: "invoices-send", Limit: 60, Window: 10 * time.Minute}` on `postInvoicesByIdSend`, wired through `module.RouterOptions.Limits` in `mount` — identity's `limits` map shape.
7. The stats' Oslo days are computed in Go (`businessDay(periodFrom)` etc.) and passed as `date` parameters; the SQL compares dates only.
8. `sendDefaults.warnings` and the send response's `warnings` are computed by one function `sendWarnings(inv, profile, today)`.
9. `CustomerInvoicesPanel` lives in `@vantigo/invoices-ui` (`components/customer-invoices-panel.tsx`), exported from `index.ts`, and takes `{customerId, canCreate}` as the projects panel does.
10. The dashboard's `InvoicesSummary` KPI value is `outstandingAmount` formatted with the host's currency formatter in NOK; the hint is `t("dashboard.invoicesOverdueHint", {count, amount})` only when `overdueCount > 0`.

## File Structure

| File | Responsibility |
| --- | --- |
| `apps/server/internal/mail/outbound.go`, `outbound_test.go` | `Outbound.ReplyTo` (Task 1) |
| `apps/server/internal/db/migrations/00035_invoices_payments_delivery.sql`, `internal/db/schema_test.go` | the three tables, four triggers, `document_state`, the marker (Task 2) |
| `apps/server/internal/invoices/{state.go,state_test.go,module.go,meta.go}`, `queries/payments.sql`, `queries/deliveries.sql`, `openapi/invoices.yaml` | the mirror, the permission, meta's new fields (Task 2) |
| `apps/server/internal/invoices/{responses.go,list.go}`, `queries/invoices.sql` | `state`, `paidAmount`, `openAmount`, `refundDue`, `payments[]`, the list's lateral join and `state` filter (Task 3) |
| `apps/server/internal/invoices/{payments.go,payments_test.go}` | register and remove (Task 4) |
| `apps/server/internal/invoices/{send.go,send_test.go,mailtext.go,contractscalls.go,pdfstore.go}` | the send, the texts, `loadStoredPDF`, the SMTP accessor, `sendDefaults`, the limit (Task 5) |
| `apps/server/internal/invoices/{customer_slots.go,customer_slots_test.go}`, `queries/customers.sql` | export, erase, the marker (Task 6) |
| `apps/server/internal/invoices/{csvexport.go,csvfile.go,csvexport_test.go}`, `queries/export.sql` | the CSV (Task 7) |
| `apps/server/internal/invoices/{stats.go,stats_test.go}`, `queries/stats.sql` | the summary (Task 8) |
| `apps/server/internal/integration/{harness_test.go,invoices_test.go}` | the real-module test (Task 9) |
| `docs/invoices.md`, `docs/{module-boundaries,customers,customers-authentication,README}.md`, `ROADMAP.md`, `deploy/compose/README.md` | D11 (Task 10) |
| `apps/invoices/frontend/src/**` | the app (Task 11) |
| `apps/host/frontend/src/{routes/dashboard.tsx,routes/customers/*,catalogs/{admin,customer,dashboard}.ts}` | the host (Task 12) |

---

### Task 1: `mail.Outbound.ReplyTo` (D4, the platform change)

**Files:** modify `apps/server/internal/mail/outbound.go`, `apps/server/internal/mail/outbound_test.go`.

**Interfaces:** `Outbound` gains `ReplyTo string` (doc comment: the address replies go to; empty sets no header). `message()` calls `msg.ReplyTo(o.ReplyTo)` when non-empty and wraps its error as `mail: invalid reply-to address: %w`.

- [ ] **Step 1: The test first.** In `TestSendOutbound_RendersTheFullEnvelope`'s shape add `TestSendOutbound_SetsReplyToWhenGiven` (the built message's `Reply-To` header equals the address) and `TestSendOutbound_OmitsReplyToWhenUnset` (no `Reply-To` header). Run `mise exec -- go test ./internal/mail/` from `apps/server`; both fail.
- [ ] **Step 2: Implement** the field and the call in `message()` after `msg.Subject`. Run the package; green. Run `golangci-lint run ./internal/mail/`; 0 issues.
- [ ] **Step 3: Commit** `feat(mail): a Reply-To on the full envelope` with the two files.

---

### Task 2: The migration, the state function and its mirror, the permission, meta (D1, D2 schema, D3 function, D4 schema, D6 marker)

**Files:**
- Create: `apps/server/internal/db/migrations/00035_invoices_payments_delivery.sql`, `apps/server/internal/invoices/state.go`, `apps/server/internal/invoices/state_test.go`, `apps/server/internal/invoices/queries/payments.sql`, `apps/server/internal/invoices/queries/deliveries.sql`.
- Modify: `apps/server/internal/db/schema_test.go`, `apps/server/internal/invoices/module.go`, `apps/server/internal/invoices/meta.go`, `apps/server/internal/invoices/meta_test.go`, `apps/server/internal/invoices/module_internal_test.go`, `openapi/invoices.yaml`.
- Generated: the store and gen files, `internal/openapi/specs/invoices.yaml`, `api-schema.d.ts`, `openapi/COVERAGE.md`.

**The migration**, in order, each plpgsql block between `-- +goose StatementBegin/End`, and a `Down` that drops everything in reverse (`document_state` last):

```sql
CREATE TABLE invoices.payments (
    id                  bigint        GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    invoice_id          bigint        NOT NULL REFERENCES invoices.invoices (id) ON DELETE RESTRICT,
    paid_on             date          NOT NULL,
    amount              numeric(14,2) NOT NULL,
    currency            char(3)       NOT NULL,
    reference           varchar(100)  NOT NULL DEFAULT '',
    note                varchar(500)  NOT NULL DEFAULT '',
    registered_by_user_id uuid        NOT NULL,
    registered_at       timestamptz   NOT NULL,
    removed_at          timestamptz,
    removed_by_user_id  uuid,
    removal_reason      varchar(200),
    CONSTRAINT ck_payments_amount CHECK (amount > 0),
    CONSTRAINT ck_payments_removal CHECK (
        (removed_at IS NULL AND removed_by_user_id IS NULL AND removal_reason IS NULL)
        OR (removed_at IS NOT NULL AND removed_by_user_id IS NOT NULL AND removal_reason IS NOT NULL AND removal_reason <> ''))
);
CREATE INDEX ix_payments_invoice ON invoices.payments (invoice_id);
CREATE INDEX ix_payments_invoice_live ON invoices.payments (invoice_id) WHERE removed_at IS NULL;

CREATE TABLE invoices.deliveries (
    id              bigint       GENERATED ALWAYS AS IDENTITY (START WITH 1001) PRIMARY KEY,
    invoice_id      bigint       NOT NULL REFERENCES invoices.invoices (id) ON DELETE RESTRICT,
    recipient       varchar(254) NOT NULL,
    subject         varchar(300) NOT NULL,
    message_id      varchar(200) NOT NULL,
    pdf_sha256      char(64)     NOT NULL,
    sent_at         timestamptz  NOT NULL,
    sent_by_user_id uuid         NOT NULL
);
CREATE INDEX ix_deliveries_invoice ON invoices.deliveries (invoice_id);

CREATE TABLE invoices.erased_customers (
    customer_id integer     PRIMARY KEY,
    erased_at   timestamptz NOT NULL
);
```

The functions and triggers (messages exactly as the spec): `invoices.refuse_payment_change()` — on DELETE raise; on UPDATE allow only when `OLD.removed_at IS NULL AND NEW.removed_at IS NOT NULL` and `to_jsonb(OLD) - 'removed_at' - 'removed_by_user_id' - 'removal_reason' = to_jsonb(NEW) - 'removed_at' - 'removed_by_user_id' - 'removal_reason'`, else raise `'invoices: a payment registration is immutable' USING ERRCODE = 'P0001'`; `tr_payments_immutable BEFORE UPDATE OR DELETE`. `invoices.refuse_payment_on_unissued()` BEFORE INSERT: `SELECT kind, status INTO … FROM invoices.invoices WHERE id = NEW.invoice_id FOR SHARE`; raise `'invoices: a payment needs an issued invoice'` unless `kind = 'invoice' AND status = 'issued'`; `tr_payments_parent`. `invoices.refuse_delivery_change()` — DELETE raises; UPDATE allowed only when `NEW.recipient = ''` and the rows less `recipient` are equal; message `'invoices: a delivery is immutable'`; `tr_deliveries_immutable`. `invoices.refuse_delivery_on_draft()` BEFORE INSERT, `FOR SHARE`, raises `'invoices: a delivery needs an issued document'` unless `status = 'issued'`; `tr_deliveries_parent`. Then:

```sql
CREATE FUNCTION invoices.document_state(kind text, status text, gross numeric, credited numeric, paid numeric, due_date date, today date)
RETURNS text LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT CASE
        WHEN status = 'draft' THEN 'draft'
        WHEN kind = 'credit_note' THEN 'issued'
        WHEN credited > 0 AND credited >= gross THEN 'credited'
        WHEN gross - credited - paid <= 0 THEN 'paid'
        WHEN due_date < today THEN 'overdue'
        WHEN paid > 0 THEN 'partially_paid'
        ELSE 'open'
    END
$$;
```

**Interfaces (Go):** `state.go`: `const stateDraft, stateIssued, stateCredited, statePaid, stateOverdue, statePartiallyPaid, stateOpen`; `var invoiceStates = []string{open, partially_paid, overdue, paid, credited}`; `func documentState(kind, status string, gross, credited, paid *big.Rat, dueDate *time.Time, today time.Time) string`. `module.go`: the fifth permission `invoices:payments` (Display "Register payments", Description "Register payments against issued invoices, and remove a registration with a reason.", Sensitive true). `meta.go`: `MailAvailable: s.mailAvailable()` (`s.deps.Config != nil && s.deps.Config.Mail.Driver == "smtp"`), `CanRegisterPayments`, `CanSend: s.has("invoices:issue") && mailAvailable`.

**Queries** (`payments.sql`): `LivePaymentsSum :one` (reading 3), `PaymentsOf :many` (`WHERE invoice_id = $1 ORDER BY paid_on, id`), `InsertPayment :one`, `GetPaymentForUpdate :one` (`WHERE id = @id AND invoice_id = @invoice_id`), `RemovePayment :execrows` (`SET removed_at, removed_by_user_id, removal_reason WHERE id = @id AND removed_at IS NULL`). `deliveries.sql`: `DeliveriesOf :many` (`ORDER BY sent_at, id`), `InsertDelivery :one` (reading 4), `CustomerErased :one` (`SELECT EXISTS (SELECT 1 FROM invoices.erased_customers WHERE customer_id = $1)`).

**Contract:** `InvoicesMetaResponse` gains `mailAvailable` (required); `InvoicesMetaCapabilities` gains `canRegisterPayments`, `canSend` (required). The description of `InvoicesConflictProblem.code` gains every new code: `credit_note_no_payments`, `invoice_settled`, `payment_exceeds_open`, `payment_removed`, `customer_anonymised`, `no_invoice_email`, `mail_unavailable`, `mail_failed`; the problem gains `openAmount` (double, on `payment_exceeds_open`).

- [ ] **Step 1: The schema tests first.** In `internal/db/schema_test.go` add `TestInvoicesPaymentsDelivery_AppliesAndIsIdempotent` in `TestInvoicesBaseline_AppliesAndIsIdempotent`'s shape with `applyUpDownUp(t, url, 35)`: after up, the three tables exist, the four triggers exist with their functions, `invoices.document_state` exists; after down none do; after up again all do. Move `TestInvoicesSchema_NamesNoColumnWithAReservedWord` to `migrateTo(t, url, 35)`. Run `go test ./internal/db/ -run 'Invoices'`; the new test fails.
- [ ] **Step 2: The migration.** Write it; run the schema tests; green. Run `go generate ./...` from `apps/server` (sqlc needs the queries of Step 4 to exist for the new tables to have Go types — write Step 4's queries now, then generate).
- [ ] **Step 3: The mirror and its test.** `state.go`; `state_test.go` `TestDocumentState_TheGoMirrorAgreesWithTheSQLFunction` in `expenses/owes_employee_test.go`'s shape over every combination of kind (invoice, credit_note) × status (draft, issued) × gross (0, 1000) × credited (0, 400, 1000, 1200) × paid (0, 300, 600, 1000) × due (yesterday, today, tomorrow), comparing `documentState` with `modtest.One[string](t, h.Harness, "SELECT invoices.document_state($1,$2,$3::numeric,$4::numeric,$5::numeric,$6::date,$7::date)", …)`. Shown able to fail by swapping the Go `paid` and `overdue` branches.
- [ ] **Step 4: Queries, contract, meta, permission.** The queries above; the contract changes; `module.go`'s permission (pin it in `module_internal_test.go`'s catalog test: five keys); `meta.go`; `meta_test.go` gains `TestMeta_MailAvailabilityAndTheNewCapabilities` (with `WithEnv("MAIL_DRIVER","smtp")`, `("SMTP_HOST","smtp.example.invalid")`, `("SMTP_FROM","faktura@example.invalid")`: `mailAvailable` true, `canSend` true for a caller with `invoices:issue`; without the env false and false; `canRegisterPayments` by permission). Generate (both runs), `gen:client`, coverage.
- [ ] **Step 5: Lint and the whole invoices package**, then `git show --stat` and **commit** `feat(invoices): the payments, deliveries and erased-customer tables, the state function and its Go mirror, and the fifth permission` with every created, modified and generated path.

---

### Task 3: The state on the wire: the document response and the list (D3)

**Files:** modify `apps/server/internal/invoices/responses.go`, `list.go`, `list_test.go`, `queries/invoices.sql`, `openapi/invoices.yaml`; generated files.

**Interfaces:**
- `InvoicesInvoiceResponse` gains `state` (required; description naming the seven values), `paidAmount`, `openAmount`, `refundDue` (double, optional; "on an issued invoice"), `payments` (array of `InvoicesPayment`, on an issued invoice). New schema `InvoicesPayment {id, paidOn, amount, currency, reference, note, registeredAt, registeredByUserId, removedAt?, removedByUserId?, removalReason?}` (required: the first eight).
- `InvoicesInvoiceListItem` gains `state` (required), `openAmount` (optional). `GET /invoices` gains the query parameter `state`.
- `queries/invoices.sql`: `ListInvoices`/`CountInvoices` rewritten with `sqlc.embed(i)` and `LEFT JOIN LATERAL (SELECT coalesce(sum(c.gross_total),0)::numeric(14,2) AS credited FROM invoices.invoices c WHERE c.credits_invoice_id = i.id AND c.status = 'issued') cr ON true LEFT JOIN LATERAL (SELECT coalesce(sum(p.amount),0)::numeric(14,2) AS paid FROM invoices.payments p WHERE p.invoice_id = i.id AND p.removed_at IS NULL) pd ON true`, selecting `cr.credited`, `pd.paid`, `(i.gross_total - cr.credited - pd.paid)::numeric(14,2) AS open_amount`, `invoices.document_state(i.kind, i.status, i.gross_total, cr.credited, pd.paid, i.due_date, @today::date)::text AS state`, and the predicate `(sqlc.narg(state)::text IS NULL OR invoices.document_state(...) = sqlc.narg(state)::text)`; `Count` the same predicate.
- `responses.go`: `renderInvoice` computes, for an issued invoice, `credited` (the existing `CreditedGross`), `paid` (`LivePaymentsSum`), `state` (the mirror), `paidAmount`, `openAmount`, `refundDue` when open < 0, `payments` (`PaymentsOf`); for a draft `state = draft`; for an issued credit note `state = issued`.
- `list.go`: validate `state` ∈ `invoiceStates` (400 `'state' must be one of open, partially_paid, overdue, paid or credited, but was '…'.`); pass `Today: pgDate(businessDay(s.deps.Clock()))`; map `state` and `openAmount` (issued invoices only) onto the item.

- [ ] **Step 1: Tests first** in `list_test.go` and a new `state_wire_test.go`: `TestList_TheStateFilterAndTheOpenAmount` (plant open, overdue — due yesterday under the fixed clock — a credited invoice through a credit note, a draft, a credit note; each `state=` value returns exactly its rows; `state=bogus` is a 400; a draft and a credit note never match; `openAmount` is on issued invoices only), `TestDocument_CarriesItsState` (a draft `draft`, an issued credit note `issued`, an issued invoice `open` with `paidAmount` 0 and `openAmount` = gross, no `refundDue`). Red.
- [ ] **Step 2: Implement**; generate; green; lint.
- [ ] **Step 3: Commit** `feat(invoices): the derived state on every document and in the list, with the open amount`.

---

### Task 4: Payments: register and remove, under the invoice's lock (D2)

**Files:** create `payments.go`, `payments_test.go`; modify `openapi/invoices.yaml`; generated files.

**Contract:** `POST /api/v1/invoices/{id}/payments` (`operationId: postInvoicesByIdPayments`, `x-vantigo-access: permission:invoices:access+invoices:payments`), body `InvoicesPaymentRequest {paidOn (date, required), amount (double, required), reference?, note?}`, 200 the document, 400 `HttpValidationProblemDetails`, 404, 409 `InvoicesConflictProblem`. `POST /api/v1/invoices/{id}/payments/{paymentId}/remove` (`postInvoicesByIdPaymentsByPaymentIdRemove`, same access), body `InvoicesPaymentRemovalRequest {reason (required)}`, 200 the document, 400, 404, 409.

**Interfaces:** `payments.go`: `const codeCreditNoteNoPayments, codeInvoiceSettled, codePaymentExceedsOpen, codePaymentRemoved`; `func parsePayment(req gen.InvoicesPaymentRequest, issueDate, today time.Time) (store.InsertPaymentParams, map[string][]string)` (the 400s of D2 step 2, amount through `decimal.go`'s reader, two decimals, the bound); `PostInvoicesByIdPayments`: read the document (404; `invoice_draft`; `credit_note_no_payments`), validate, then `withLockedTx`: `GetInvoiceForUpdate` (exists from 1A's issue; reuse), `CreditedGross`, `LivePaymentsSum`, open ≤ 0 → `invoice_settled`, amount > open → `payment_exceeds_open` with `OpenAmount`, `InsertPayment` with `Currency: inv.Currency`, `RegisteredAt: s.deps.Clock()`, `RegisteredByUserID: callerID(ctx)`; after commit `invoiceResponse`. `PostInvoicesByIdPaymentsByPaymentIdRemove`: validate the reason first; `withLockedTx`: `GetInvoiceForUpdate` (404), `GetPaymentForUpdate` (404), `removed_at != nil` → `payment_removed`, `RemovePayment`; answer the document.

- [ ] **Step 1: Tests first** (`payments_test.go`), each from the spec's Testing "Payments" bullet, named: `TestPayments_RegisterAgainstAnIssuedInvoice` (state `partially_paid`, then `paid` with the exact open amount, `paidAmount`, `openAmount` 0), `TestPayments_RefusesWhatIsNotAnIssuedInvoice` (404, `invoice_draft`, `credit_note_no_payments`), `TestPayments_TheFieldRules` (every 400), `TestPayments_SettledAndExceeding` (`invoice_settled`, `payment_exceeds_open` carrying `openAmount`), `TestPayments_RemovalReopensAndIsRecorded` (the struck-through row with its reason in `payments[]`, `payment_removed` on a second removal, 400 on an empty reason), `TestPayments_ImmutableInSQL` (direct `UPDATE amount` fails `P0001`; `DELETE` fails; the removal columns set once pass; a second removal fails; an insert under a draft and under a credit note fails), `TestPayments_ARegistrationRacingACreditNoteIssue` (a 1000 invoice; a credit note of 300 held with `SetIssueAfterAllocation` while a registration of 400 runs; both commit; `openAmount` is exactly 300), `TestPayments_TwoRegistrationsOfTheWholeOpenAmount` (exactly one 200, one `invoice_settled` or `payment_exceeds_open`), `TestPayments_TwoRemovalsOfOnePayment` (one 200, one `payment_removed`), `TestPayments_AnAnonymisedCustomersInvoiceStillTakesAPayment`, `TestPayments_BothWritesNeedThePermission` (403 without `invoices:payments`). Red.
- [ ] **Step 2: Implement**, generate, green, lint, coverage.
- [ ] **Step 3: Commit** `feat(invoices): payments — registered under the invoice's lock, refused once settled or over the open amount, removed only with a reason`.

---

### Task 5: Sending the document (D4)

**Files:** create `send.go`, `send_test.go`, `mailtext.go`, `mailtext_internal_test.go`; modify `contractscalls.go`, `pdfstore.go`, `responses.go`, `drafts.go` (`GetInvoicesById`), `module.go` (the limit), `openapi/invoices.yaml`; generated files.

**Contract:** `POST /api/v1/invoices/{id}/send` (`postInvoicesByIdSend`, `permission:invoices:access+invoices:issue`), body `InvoicesSendRequest {recipient?}`, 200 the document (its `warnings` holding the send warnings), 400, 404, 409 (`invoice_draft`, `customer_anonymised`, `no_invoice_email`), 429 (`ProblemDetails`), 502 `mail_failed` and 503 (`mail_unavailable`, `storage_unavailable`) in the conflict shape, 500. `InvoicesInvoiceResponse` gains `deliveries` (array of `InvoicesDelivery {id, recipient, subject, sentAt, sentByUserId}`) and `sendDefaults` (`InvoicesSendDefaults {recipient?, preference?, warnings[] (required)}`).

**Interfaces:**
- `contractscalls.go`: `func (s *server) smtpSend(ctx, cfg config.MailConfig, out mail.Outbound) error` — `noteContractCall(ctx, "SMTPSend")`; `s.deps.SMTPSend` else `mail.SendOutbound`.
- `pdfstore.go`: `func (s *server) loadStoredPDF(ctx, q, inv) (storedPDF, pdfProblem)` extracted from `GetInvoicesByIdPdf` lines "found := storedPDF{}" through the hash check; `pdfProblem` carries which of 503/500 and the problem body; the download calls it and maps the problem.
- `mailtext.go`: `type mailText struct{ subject, body string }`; `func coverMail(inv store.InvoicesInvoice, original *store.InvoicesInvoice, open *big.Rat) mailText` — the spec's four texts with the three payment paragraphs and the IBAN form, formatted with `pdf.go`'s money and date formatters for `buyer_language`.
- `send.go`: `const codeCustomerAnonymised, codeNoInvoiceEmail, codeMailUnavailable, codeMailFailed, warningDeliveryPreferenceEHF = "delivery_preference_ehf", warningDeliveryPreferenceOther = "delivery_preference_other", warningBuyerNorwegianBusiness = "buyer_norwegian_business"`; `var b2bDutyFrom = time.Date(2027, 1, 1, …)`; `func sendWarnings(inv, profile *contracts.CustomerBillingProfile, today time.Time) []string`; `func validRecipient(s string) (string, bool)` (the settings rule); `PostInvoicesByIdSend` in the spec's nine steps, with `sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)` around the send and the row.
- `responses.go` / `drafts.go`: on an issued document for a caller with `canSend`, read the profile (best effort: a warn log and no `sendDefaults` on error) and set `SendDefaults{Recipient, Preference, Warnings: sendWarnings(...)}`; `deliveries` from `DeliveriesOf`.
- `module.go`: `var limits = map[string]ratelimit.Policy{"postInvoicesByIdSend": {Name: "invoices-send", Limit: 60, Window: 10 * time.Minute}}`, passed as `Limits`.

The test harness: `newHarness(t, modtest.WithSMTPSend(fake.send), modtest.WithEnv("MAIL_DRIVER","smtp"), modtest.WithEnv("SMTP_HOST","smtp.example.invalid"), modtest.WithEnv("SMTP_FROM","faktura@example.invalid"))`; the fake records every `mail.Outbound` and can fail on demand.

- [ ] **Step 1: Tests first** (`send_test.go`): `TestSend_MailUnavailableIsJudgedFirst` (the `log` driver; even a draft id answers 503), `TestSend_RefusesADraftAndAnAnonymisedCustomer`, `TestSend_NoInvoiceEmail`, `TestSend_TheOverrideWinsAndIsValidated`, `TestSend_TheEnvelope` (From, DisplayName the snapshot's seller, Reply-To from the current settings and absent without, To, subject, the nb body in full, the attachment's name/type/bytes equal to the stored PDF, a bare uuid Message-ID), `TestSend_TheEnglishBodyAndTheIBANForm`, `TestSend_ThePaymentParagraphFollowsTheOpenAmount` (in full, partly, nothing), `TestSend_ACreditNote`, `TestSend_StoresAnUnstoredPDFFirst` (row hash equals the attached bytes'), `TestSend_StorageUnavailable`, `TestSend_AFailedSendRecordsNothing` (502, no row), `TestSend_ACancelledRequestStillSendsAndLogs` (cancel the request context from the fake; the row exists), `TestSend_IsLogged` (`deliveries[]`), `TestSend_SendDefaultsAndWarnings` (for a sender; absent for a reader; absent on a draft; absent with a warn log when the directory fails; `delivery_preference_ehf`, `delivery_preference_other`, `buyer_norwegian_business` before and after 2027-01-01 under the fixed clock), `TestSend_NeedsIssue` (403), `TestSend_IsRateLimited` (the 61st within the window is a 429); `mailtext_internal_test.go` pins each text. Red.
- [ ] **Step 2: Implement**, generate, green, lint, coverage. The harness's locked-call check must see `SMTPSend` never under a lock.
- [ ] **Step 3: Commit** `feat(invoices): send the stored PDF by e-mail with the seller as Reply-To, logged once and safe against a racing anonymisation`.

---

### Task 6: The slots: export with payments and deliveries, erase with the marker (D6)

**Files:** modify `customer_slots.go`, `customer_slots_test.go`, `queries/customers.sql`; generated files.

**Interfaces:** `queries/customers.sql`: `LockCustomerDocuments` (exists from 1A's merge holder: `SELECT id FROM invoices.invoices WHERE customer_id = ANY(...) ORDER BY id DESC FOR UPDATE` — reuse with one id), `MarkCustomerErased :exec` (`INSERT … ON CONFLICT DO NOTHING`), `BlankCustomerDeliveries :execrows` (`UPDATE invoices.deliveries d SET recipient = '' FROM invoices.invoices i WHERE d.invoice_id = i.id AND i.customer_id = $1 AND d.recipient <> ''`), `PaymentsOfDocuments :many`, `DeliveriesOfDocuments :many`. `customer_slots.go`: `exportedDocument` gains `Payments []exportedPayment`, `Deliveries []exportedDelivery`; `EraseCustomerData` in the spec's order, reporting `invoices.drafts`, `invoices.documents` (0), `invoices.payments` (0), `invoices.deliveries` (n).

- [ ] **Step 1: Tests first**: `TestCustomerPersonalData_ExportCarriesPaymentsAndDeliveries`, `TestCustomerPersonalData_EraseBlanksDeliveriesAndReportsFourKinds` (run twice → zeros; payments untouched; the marker row exists), `TestCustomerPersonalData_ASendRacingTheEraseLeavesNoAddress` (hold the send between its directory read and its row with a seam — add `SetBeforeDeliveryWrite(hook func(ctx, invoiceID))` to `export_test.go` — run the erase in a transaction to commit, release the send; the row's `recipient` is `''`; a second send is `customer_anonymised`). Red.
- [ ] **Step 2: Implement**, generate, green, lint.
- [ ] **Step 3: Commit** `feat(invoices): the export carries payments and deliveries, and the erase blanks the delivery log under a lock and marks the customer erased`.

---

### Task 7: The CSV export (D5)

**Files:** create `csvexport.go`, `csvfile.go`, `csvexport_test.go`, `queries/export.sql`; modify `openapi/invoices.yaml`; generated files.

**Contract:** `GET /api/v1/invoices/export.csv?from&to` (`getInvoicesExportCsv`, `permission:invoices:access`), 200 `text/csv` `{type: string, format: binary}` with the description naming the attachment and no caching, 400 `ProblemDetails`.

**Interfaces:** `csvfile.go`: `csvByteOrderMark`, `csvSeparator`, `csvLineEnd`, `invoicesFileMaxRows = 5000`, `writeCSVRow(b *bytes.Buffer, cells []csvValue)` where `csvValue{text string; guard bool}` — the guard applied only when `guard`; `csvCell`, `csvDecimal(n pgtype.Numeric, negate bool) string` (decimal comma, two places, `-` prefix when negated). `queries/export.sql`: `ExportRows :many` — the issued documents in `[from, to]` joined with their VAT summaries, ordered by `number, vat_category, rate_percent`, with the original's number for a credit note (a self join), limited to `invoicesFileMaxRows + 1`. `csvexport.go`: `GetInvoicesExportCsv` (both dates required and `from ≤ to` → 400 "Invalid query parameters"; over the cap → 400 "Too many rows to export" / "This export would hold more than 5000 rows … narrow the period."), `csvDownload{body, fileName}` implementing `VisitGetInvoicesExportCsvResponse` with the customers headers; the file name `invoices-<from>-<to>.csv`.

- [ ] **Step 1: Tests first**: `TestExportCSV_IsExactlyTheseBytes` (an invoice with two VAT rows, a credit note of it, a buyer named `=cmd`; the expected bytes written by hand in the test, BOM to final CRLF; the negative amounts unguarded; the guard on `=cmd`), `TestExportCSV_IsServedAsAFileNobodyCaches` (headers, file name), `TestExportCSV_ThePeriodAndTheCap` (a document outside the range absent; 5001 rows → 400 — plant via direct SQL rows in `vat_summaries`, the trigger disabled in the test as 1A's journal gap test does), `TestExportCSV_TheDateRules` (missing, `from > to`). Red.
- [ ] **Step 2: Implement**, generate, green, lint, coverage.
- [ ] **Step 3: Commit** `feat(invoices): the accountant's CSV export — one row per document and VAT row, credit notes negative, the guard on text only`.

---

### Task 8: Stats and the summary (D7)

**Files:** create `stats.go`, `stats_test.go`, `queries/stats.sql`; modify `openapi/invoices.yaml`; generated files.

**Contract:** `GET /api/v1/invoices/stats/summary?from&to` (`getInvoicesStatsSummary`, `permission:invoices:access`; `from`/`to` `date-time` as `openapi/projects.yaml`'s), 200 `InvoicesStatsSummaryResponse` with the spec's envelope (all required), 400 `ProblemDetails`.

**Interfaces:** `queries/stats.sql`: `InvoiceStatsNow :one` (over the lateral-join text of Task 3: `count(*) FILTER (WHERE state IN ('open','partially_paid','overdue'))`, `sum(open_amount) FILTER (…)`, the overdue pair with `state = 'overdue'`, `@today`), `InvoiceStatsPeriod :one` (`issued`/`credited` counts and gross over `issue_date >= @from_day AND issue_date < @to_day`, the previous period's issued gross over `[@previous_from_day, @from_day)`), `PaymentsInPeriod :one` (live payments by `paid_on`). `stats.go`: `GetInvoicesStatsSummary` — `NormalizePeriod`, the three instants to Oslo days through `businessDay`, the three queries, `IssuedGrossTotalDelta = issued − previous`.

- [ ] **Step 1: Tests first**: `TestStatsSummary_TheFigures` (the planted set of the spec: open, partially paid, overdue, paid, credited, a credit note excluded from outstanding, a removed payment not counted; the period and the previous period; a document on the boundary day in and the day before out), `TestStatsSummary_TheDefaultPeriodAndAnInvalidOne`. Red.
- [ ] **Step 2: Implement**, generate, green, lint, coverage.
- [ ] **Step 3: Commit** `feat(invoices): the stats summary — outstanding and overdue now, issued, credited and paid in the period`.

---

### Task 9: The customers + invoices integration test (D9)

**Files:** modify `apps/server/internal/integration/harness_test.go` (`modInvoices`, `moduleNamed`, the merged recorder gains `invoices`, `newInstallationWith(t, opts []modtest.Option, names ...string)`); create `apps/server/internal/integration/invoices_test.go`.

- [ ] **Step 1:** The five scenarios of D9 as `TestInvoices_ADraftTakesTheRealProfileAndTheSnapshotIsTheCustomer`, `TestInvoices_ADisabledCustomerIsRefusedANewDraftButCredited`, `TestInvoices_AMergeRepointsTheRealDocuments`, `TestInvoices_ASendReachesTheProfilesAddressWithTheSellersReplyTo`, `TestInvoices_TheExportAndTheAnonymisation` (the anonymisation worker run once through the customers module's worker constructor as `personal_data_test.go` runs it; afterwards a send is `customer_anonymised`). The object store: `storage.NewFS(t.TempDir(), true, true)` through `modtest.WithObjectStore`. Shown able to fail by asserting the wrong Reply-To once.
- [ ] **Step 2: Commit** `test(integration): customers and invoices composed for real — the profile, the gates, the merge, the send and the anonymisation`.

---

### Task 10: The docs (D11)

**Files:** modify `docs/invoices.md`, `docs/module-boundaries.md`, `docs/customers.md`, `docs/customers-authentication.md`, `ROADMAP.md`, `deploy/compose/README.md`, `docs/README.md` if its index describes modules.

- [ ] **Step 1:** Write every section D11 lists, in `docs/invoices.md`'s voice; the endpoints table gains the seven operations with their refusals; the permissions table the fifth key.
- [ ] **Step 2: Check against the code** as 1A's Task 11 Step 3 did: every `codeX` constant and every `x-vantigo-access` of `openapi/invoices.yaml` appears in `docs/invoices.md`, and every code the doc names exists in Go (`grep -o '`[a-z_]*`' docs/invoices.md | sort -u` against `grep -ho 'code[A-Za-z]* *= "[a-z_]*"' apps/server/internal/invoices/*.go`).
- [ ] **Step 3: Commit** `docs(invoices): payments and the state, sending, the export and the stats — what the code does`.

---

### Task 11: The Invoices app (D10, the package)

**Files:** modify `apps/invoices/frontend/src/{api/invoices.ts,api/meta.ts,i18n.ts,index.ts,pages/invoices.tsx,pages/invoice.tsx,pages/journal.tsx,pages/settings.tsx,test/fixtures.ts}`; create `api/payments.ts`, `api/send.ts`, `api/export.ts`, `api/stats.ts`, `components/state-badge.tsx`, `components/payments-card.tsx`, `components/-register-payment-modal.tsx`, `components/-remove-payment-modal.tsx`, `components/send-dialog.tsx`, `components/deliveries-card.tsx`, `components/customer-invoices-panel.tsx`, and their tests.

**Interfaces:** `registerPayment(id, input)`, `removePayment(id, paymentId, reason)`, `sendInvoice(id, recipient?)`, `downloadInvoicesCsv({from, to})` + `saveCsv` (the customers `downloadFile` copied into this package), `invoiceStatsSummaryQueryOptions(range)`; `StateBadge({state})` with the spec's colours; `CustomerInvoicesPanel({customerId, canCreate})` exported from `index.ts`; every refusal code of Tasks 4–8 in `lib/errors.ts`'s `refusalMessage` with a key in both catalogs.

- [ ] **Step 1: Fixtures** — a wire literal of an issued invoice with `state`, `paidAmount`, `openAmount`, `payments` (one live, one removed), `deliveries`, `sendDefaults`; a list item with `state` and `openAmount`; a meta with `mailAvailable` and the two capabilities.
- [ ] **Step 2: Tests first**, per the spec's Testing "Frontend": the list's chips and badges; the payments card (register → the request body, the button disabled while pending, `invoice_settled` and `payment_exceeds_open` shown with the amount; remove → the reason modal, `payment_removed`); the send dialog (recipient prefilled, both alerts, the settled note, "Sent to …", `no_invoice_email`, `mail_unavailable`, `mail_failed`, 429); the deliveries card ("(anonymised)" for `''`); the journal's export button and its error notification; the settings checklist line; the customer panel (filtered by `customerId`, "New invoice" only with `canCreate`). Red.
- [ ] **Step 3: Implement**; `biome check --write`; the package's tests, typecheck and lint; `translations:check`.
- [ ] **Step 4: Commit** `feat(invoices-ui): payments, the state, sending, the export and the customer panel`.

---

### Task 12: The host: the dashboard card, the customer tab, the catalog (D1, D7, D8)

**Files:** modify `apps/host/frontend/src/routes/dashboard.tsx`, `routes/dashboard.test.ts`, `routes/customers/-customer-detail-layout.tsx`, `routes/customers/customer-detail-tabs.test.ts`, `catalogs/{admin,customer,dashboard}.ts`; create `routes/customers/$customerId.invoices.tsx`, `routes/customers/-customer-invoices-tab.tsx`, `routes/customers/customer-invoices-tab.test.tsx`.

- [ ] **Step 1: Tests first**: the tab registry (visible with the module and `invoices:access`, hidden otherwise; the active tab on the invoices route); the tab component (`ModuleNotEnabledPage` without the module; `canCreate` only with `invoices:create`, `customers:view` and an active customer); the dashboard card (gated; the KPI value and the overdue hint only when non-zero; no timeseries query for invoices). Red.
- [ ] **Step 2: Implement** D7's card (`IconFileInvoice`, `InvoicesSummary`, the query, one `KpiCard`), D8's registry edits (the three unions, the entry, the `activeTab` ternary), the route and tab files, the catalog keys (`dashboard.invoices`, `dashboard.manageInvoices`, `dashboard.invoicesOverdueHint`, `customer.invoicesTab`, `admin.permission.invoicesPayments` and its description) in en + nb. The host's tests regenerate `routeTree.gen.ts`; commit it.
- [ ] **Step 3: Commit** `feat(frontend): the Invoices dashboard card, the customer page's Invoices tab and the payments permission in the admin catalog`.

---

### Task 13: Verify the whole branch and open the PR

- [ ] **Step 1:** From `apps/server`: `go generate ./...` and from the root `bun run gen:client` — no diff; `gofmt -l apps/server` empty; `go vet ./...`; `golangci-lint run ./...` 0 issues; `go test -count=1 ./...` every package ok; `go test -race ./internal/invoices/ ./internal/integration/` twice; `go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./...`. From the root: every frontend package's typecheck, lint and tests; `translations:check`; `biome check .`. Task 10's docs check once more.
- [ ] **Step 2:** The greps: no `CURRENT_DATE` in `internal/invoices`; no `SetFloat64`; every `noteContractCall` name in `contractscalls.go` only; no import of another module from `internal/invoices`.
- [ ] **Step 3:** The PR: title `Invoices — payments, delivery and the export (phase 1B)`, body in PR #128's shape (What, Decisions made without asking — the spec's nine readings and this plan's ten, Things to know before merging, How it was built, Verification). `gh pr create --base main --head feat/invoices-payments-delivery`. Watch CI; fix red on the branch with a new commit. Do not merge.
- [ ] **Step 4: Report** the PR, CI's state, each test shown able to fail, and the readings for the user's verdict.

---

## Self-review

**Spec coverage.** D1 → Task 2 (the permission, meta) and Task 12 (the catalog); D2 → Tasks 2, 4; D3 → Tasks 2, 3; D4 → Tasks 1, 5; D5 → Task 7; D6 → Task 6; D7 → Tasks 8, 12; D8 → Tasks 11 (the panel), 12; D9 → Task 9; D10 → Task 11; D11 → Task 10; every Testing bullet names a test above. Out of scope: nothing in Tasks 1–12 adds an outbox, an idempotency key, a timeseries, a KPI strip, a tone prop or a PDF stamp.

**Name consistency.** SQL: `invoices.payments/deliveries/erased_customers`, `document_state`, `refuse_payment_change`, `refuse_payment_on_unissued`, `refuse_delivery_change`, `refuse_delivery_on_draft`, `tr_payments_immutable`, `tr_payments_parent`, `tr_deliveries_immutable`, `tr_deliveries_parent`. Go: `documentState`, `invoiceStates`, `parsePayment`, `loadStoredPDF`, `coverMail`, `sendWarnings`, `validRecipient`, `smtpSend`, `csvValue`, `csvDownload`. Wire: `state`, `paidAmount`, `openAmount`, `refundDue`, `payments`, `deliveries`, `sendDefaults`, `mailAvailable`, `canRegisterPayments`, `canSend`; the codes `credit_note_no_payments`, `invoice_settled`, `payment_exceeds_open`, `payment_removed`, `customer_anonymised`, `no_invoice_email`, `mail_unavailable`, `mail_failed`; the warnings `delivery_preference_ehf`, `delivery_preference_other`, `buyer_norwegian_business`; the kinds `invoices.payments`, `invoices.deliveries`.
