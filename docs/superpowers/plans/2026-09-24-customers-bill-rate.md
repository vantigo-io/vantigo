# Customer default bill rate (phase 5, delivery B) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A customer that has negotiated an hourly rate gets it applied to every project of theirs that does not price its own hours, without anyone re-typing it per project. The rate is the billing profile's eleventh field, `defaultBillRate`, quoted in the profile's own `currency`; the directory exposes it as `contracts.CustomerBillingProfile.DefaultBillRate`; and Time's rate chain gains a step between the project default and the person card — **billing line → project → customer → person** — with `rateSource: "customer"`. Nothing else about rates changes.

**Architecture:** One migration (`00028`) adds `customers.customers.default_bill_rate numeric(12,2) NULL`, the `00019` ALTER shape. The billing profile's three queries (`GetCustomerBillingProfile`, `UpdateCustomerBillingProfile`, `DirectoryBillingProfile`) select or write it; `billingProfile` carries it as `*float64`, converted at the row boundary through a mirrored `numericFromFloatPtr`/`floatPtrFromNumeric` pair (projects' own, never imported). Validation is `validateDefaultBillRate` (> 0, ≤ 2 decimals, ≤ 9999999999.99) plus the profile's one cross-field rule: a rate without a currency is a 400 on `defaultBillRate`. The event's `before`/`after` pick the field up through its `json` tag, `billingProfileChanges` gains its arm, payload version stays 1. The contract gains one field, own value only — `resolveBillingProfile`'s signature is untouched. Time's `resolveRates` asks the directory at most once, and only when neither the line nor the project priced the hours; `customerRate` is nil-safe on `Deps.Directory` and applies the person card's currency rule verbatim. The customers Billing modal gains a `NumberInput`, the card a row, the catalogs four keys each.

**Tech Stack:** Go 1.27 (pgx, sqlc, oapi-codegen strict server), PostgreSQL 18, React + Mantine 9 + TanStack Query, vitest, bun, mise.

**Spec:** `docs/superpowers/specs/2026-09-24-customers-bill-rate-design.md` (D1–D5 + "Out of scope" + "Testing"). Read it first; it is binding, and every decision below argues from it rather than past it. Research with file:line pointers: `.superpowers/sdd/2026-09-24-customers-bill-rate/context-for-design.md`. The shapes this delivery copies: `apps/server/internal/customers/billing_values.go` (`validatePaymentTermsDays` — a validator writing into the shared `errs` map), `apps/server/internal/projects/values.go` (`validatePositiveAmount`, `maxAmount12`, `numericFromFloatPtr`, `floatPtrFromNumeric` — mirrored, never imported), `apps/server/internal/time/rates.go` (`lineRate` — an optional contract read nil-safely, its failure an error).

## Global Constraints

- Branch `feat/customers-bill-rate`. Never commit to `main`, never merge, never `--no-verify`.
- **Forbidden git commands:** `git add -A`, `git add .`, `git stash`, `git checkout -- .`, `git restore .`, `git clean`, `git reset --hard`. Commit with an explicit pathspec (`git add <files>` then `git commit -F <msgfile> -- <files>`), then check `git show --stat HEAD` and that `git status --short` shows nothing of yours left. The untracked `go.mod`/`go.sum` in the repo root are not ours: never add or delete them.
- Commit messages: Conventional Commits scoped `customers` / `customers-ui` / `contracts` / `projects` / `frontend` / `docs`, subject a plain sentence about behaviour (see `git log`). End every message with `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>`.
- Toolchain only through mise: `mise exec -- go …`, `mise exec -- bun …`. Capture exit codes before any pipe (`${PIPESTATUS[0]}`).
- Go tests need `export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'` (port 55432 belongs to another project — never touch it).
- After any `openapi/*.yaml` change: `cd apps/server && mise exec -- go generate ./...` (a second run must show no new diff) `&& mise exec -- go test -count=1 ./internal/openapi/...`, then from the repo root `mise exec -- bun run gen:client`; commit every generated file (`apps/server/internal/openapi/specs/customers.yaml`, `internal/customers/gen/api.gen.go`, `internal/customers/store/*.go`, each changed `api-schema.d.ts`, `openapi/COVERAGE.md` if it moved).
- **Frozen corpus:** never edit `openapi/testdata/exchanges/*.jsonl`; changes to EXISTING schemas are additive and optional only (never add to an existing `required:` list). New schemas may have required fields. New query/request params are plain optional strings validated in Go with this module's error wording (`… must be one of 'a' or 'b', but was 'x'`), not yaml `enum:`.
- New operations need an `operationId` and an `x-vantigo-access` rule; every operation must be exercised by this module's tests (the module's operation-coverage gate — see `internal/customers/gen/gen_test.go` / `main_test.go` recorder) .
- After any migration: add it to `apps/server/internal/customers/sqlc.yaml`'s `schema:` list, then `go generate`. After any exported Go signature change: `mise exec -- go vet ./...` and grep every caller including tests and fakes.
- Foundation rules that still bind: resolve the timeline actor with `s.actorFor(ctx, generatedFallbackActor)` BEFORE opening a transaction and only when a write will happen — no access or directory call under a lock; every write to the `customers.customers` row bumps `revision`, a no-op writes nothing; a guarded write repeats the revision comparison in the `UPDATE`'s `WHERE`; the revision-conflict 409 is `customerRevisionConflict` (title "Customer revision conflict", no `code`).
- Match the surrounding code: comment density and voice (these files explain *why*), naming, error wording, test style. Check sibling pages/components before inventing a frontend convention: pages read `useSearch({strict:false})`/`useNavigate` themselves and never fetch permissions — the host passes capability props; links use `<Anchor renderRoot={(props) => <Link to={…} {...props} />}>`.
- Every new UI string in both catalogs of `apps/customers/frontend/src/i18n.ts` (en + nb); `mise exec -- bun run translations:check` and `mise exec -- bun run i18n:test` must pass.
- Frontend tests: `mise exec -- bun run --cwd <pkg> test` (not `bun --cwd X run test`). Mantine popovers/modals/selects need `<MantineProvider env="test">`. **Never assert "the last fetch"** — debounced lookups run on their own clock and the CI runner is slow; assert on the specific call (filter `fetchMock.mock.calls` by method/URL, like `lastPut` in `-customer-form-modal.test.tsx`).
- Every new test must be shown able to fail (remove the guard, see red, restore). Say so in the report.

---

- **No test may touch the network or real DNS.** `internal/peppol`'s own tests use in-process UDP/TCP DNS stubs and `httptest`; everything above it uses the `Deps.PeppolLookup` fake.
- **Wire format:** the Go server OMITS unset optional fields (`omitempty`), it never sends `null`. Frontend api files normalise omitted→null at the boundary, and at least one fixture per resource is literally the body the server sends when nothing is set.
- **Frontend data discipline (delivery A):** never seed a form from `invalidateQueries` (it resolves even when the refetch fails) — use `fetchQuery({staleTime: 0})` in try/catch; a save that returns a revision calls `syncCustomerRevision` before invalidating; `src/lib/customer-reload.ts` is the Reload pattern.
- **One implementer commits at a time.** If two agents ever share the tree, the second writes and verifies but does not commit; the controller commits by pathspec.

---

- The timeline actor is resolved with `s.actorFor(ctx, ...)` before the transaction and only when a write will happen; no directory call inside a transaction.
- The rate is the ELEVENTH billing-profile field: `customers.customers.default_bill_rate numeric(12,2)` (migration 00028), read with `customers:view`, written only through the full-replace `PUT /customers/{id}/billing-profile` (`customers:billing-manage`), never on `SafeCustomerResponse`; quoted in the profile's own `currency` — a rate without a currency is a 400 keyed `defaultBillRate`; > 0, ≤ 2 decimals, ≤ 9999999999.99 (projects' `validatePositiveAmount` rule mirrored, never imported — depguard).
- `contracts.CustomerBillingProfile.DefaultBillRate *float64` is the customer's OWN value only (no group tier; `resolveBillingProfile`'s signature untouched).
- Time's chain: `sourceCustomer = "customer"` between project and person; the step runs only when billable, no line rate, no usable project default, `Project.CustomerID != nil` and `deps.Directory != nil` (nil-safe — customers may be off); the person card's currency rule verbatim (project without currency takes the customer's; equal currencies apply; otherwise fall through); a directory error is an error; (nil, nil) is "no rate". At most one directory call per resolve.
- `rateSource` stays a plain string on the wire (description gains `customer`); the time frontend's hand-narrowed `RateSource` union gains `"customer"`; time has no frozen corpus.
- Every cross-module fake of `CustomerDirectory` that Time's tests need is written in Time's test package (there is none today); Time's harness gains `modtest.WithDirectory`.
- Contract changes to EXISTING schemas stay additive/optional; new schemas may have required fields; the frozen corpus (`openapi/testdata/exchanges/*.jsonl`, customers' — time has none) is untouched and must still validate.
- After any `queries/*.sql` or migration change: `mise exec -- go generate ./...` from `apps/server`; new migrations go in `sqlc.yaml` and `internal/db/schema_test.go`; new operations need `operationId`, `x-vantigo-access`, module-test coverage, a `KnownServeMuxConflicts` pin where they conflict, and a COVERAGE.md regeneration.
- Run `mise exec -- bunx biome check --write <files>` on touched frontend files before committing.

---

**How this plan reads three of those constraints.** The scope list names no `time`; Task 3 is time's own change (server chain, contract description, the time app's union), so its commit is scoped `feat(time)`, the scope `git log` already uses for that module (`feat(time): report what has been logged…`). "New migrations go in `internal/db/schema_test.go`": that file discovers migrations by globbing and checks `sqlc.yaml` against them (`TestSqlcSchemaListsOnlyTheModulesOwnMigrations`), so there is no list to append to — Task 1 registers `00028` in `sqlc.yaml` and adds a column assertion to `TestCustomersBaseline_AppliesAndIsIdempotent`, whose up-down-up to version 3 also runs `00028`'s Down. No operation is added, so `openapi/COVERAGE.md` (operations only) does not move; Task 5 confirms it.

## File Structure

| File | Responsibility |
| --- | --- |
| `apps/server/internal/db/migrations/00028_customers_default_bill_rate.sql` | the column |
| `apps/server/internal/db/schema_test.go` | the column's type, scale and nullability |
| `apps/server/internal/customers/sqlc.yaml` | `00028` in the schema list |
| `apps/server/internal/customers/queries/customers.sql` (+ generated `store/customers.sql.go`, `store/models.go`) | the three billing queries read/write the column |
| `openapi/customers.yaml` (+ generated `internal/openapi/specs/customers.yaml`, `internal/customers/gen/api.gen.go`, `apps/customers/frontend/src/api-schema.d.ts`) | `defaultBillRate` on `CustomerBillingProfile` and `PutCustomerBillingProfileRequest` |
| `apps/server/internal/customers/billing_values.go`, `billing_values_test.go` | the field, its equality, the numeric conversions, its validator and the currency rule |
| `apps/server/internal/customers/billing_profile.go`, `billing_profile_test.go` | GET/PUT carry it; the HTTP tests |
| `apps/server/internal/customers/timeline_events.go` | `billingProfileChanges`' arm |
| `apps/server/internal/customers/peppol_lookup.go`, `peppol_recheck_worker.go` | `billingProfileFromRow`'s new argument (nil — the participant needs no rate) |
| `apps/server/internal/contracts/directory.go` | `CustomerBillingProfile.DefaultBillRate` |
| `apps/server/internal/customers/directory.go`, `directory_internal_test.go`, `directory_test.go` | the pass-through and its tests |
| `apps/server/internal/time/values.go`, `rates.go`, `module.go` | `sourceCustomer`, the step, the module doc |
| `apps/server/internal/time/rates_internal_test.go`, `harness_test.go`, `entries_test.go` | the table, the fake directory, the end-to-end case |
| `openapi/time.yaml` (+ generated `internal/openapi/specs/time.yaml`, `internal/time/gen/api.gen.go`, `apps/time/frontend/src/api-schema.d.ts`) | `rateSource`'s description |
| `apps/time/frontend/src/api/entries.ts`, `entries.test.ts` | the `RateSource` union |
| `apps/customers/frontend/src/api/billing-profile.ts`, `billing-profile.test.ts`, `api/customers.test.ts` | the type, the boundary, fixtures |
| `apps/customers/frontend/src/pages/-customer-billing-modal.tsx`, `-customer-billing-modal.test.tsx` | the input |
| `apps/customers/frontend/src/pages/-customer-billing-card.tsx`, `-customer-billing-card.test.tsx`, `-customer-peppol-status.test.tsx` | the row; the Use EHF body keeps the rate |
| `apps/customers/frontend/src/i18n.ts` | four keys, en + nb |
| `docs/customers.md`, `docs/time.md`, `docs/projects.md`, `docs/module-boundaries.md`, `ROADMAP.md` | D5 |

---

### Task 1: The eleventh billing-profile field (D1)

The rate is stored, validated, written through the existing full-replace PUT, recorded on the existing event and answered by the existing GET. The directory's query is widened here too, because `billingProfileFromRow` gains an argument every caller must pass; Task 2 then exposes it on the contract.

**Files:**
- Create: `apps/server/internal/db/migrations/00028_customers_default_bill_rate.sql`
- Modify: `apps/server/internal/db/schema_test.go`, `apps/server/internal/customers/sqlc.yaml`, `apps/server/internal/customers/queries/customers.sql`, `openapi/customers.yaml`, `apps/server/internal/customers/billing_values.go`, `billing_values_test.go`, `billing_profile.go`, `billing_profile_test.go`, `timeline_events.go`, `directory.go`, `peppol_lookup.go`, `peppol_recheck_worker.go`
- Generated: `apps/server/internal/customers/store/customers.sql.go`, `store/models.go`, `apps/server/internal/openapi/specs/customers.yaml`, `apps/server/internal/customers/gen/api.gen.go`, `apps/customers/frontend/src/api-schema.d.ts`
- Read first (do not change): `apps/server/internal/projects/values.go:255-290` and `:570-620` (the rules mirrored), `apps/server/internal/customers/contacts_test.go:815-851` (`fetchTimelineEvent` reads the newest event), `apps/server/internal/db/migrations/00019_customers_billing_profile.sql`

**Interfaces:**
- Produces Go: `billingProfile.DefaultBillRate *float64` (json `defaultBillRate`); `billingProfileFromRow(invoiceEmail, reminderEmail *string, paymentTermsDays *int32, currency, language, invoiceDelivery, reminderDelivery, peppolID, gln, buyerReference *string, defaultBillRate *float64) billingProfile`; `floatPtrEqual(a, b *float64) bool`; `validateDefaultBillRate(raw *float64, errs map[string][]string) *float64`; `numericFromFloatPtr(*float64) (pgtype.Numeric, error)`; `floatPtrFromNumeric(pgtype.Numeric) (*float64, error)`; `store.GetCustomerBillingProfileRow.DefaultBillRate`, `store.UpdateCustomerBillingProfileParams.DefaultBillRate`, `store.DirectoryBillingProfileRow.DefaultBillRate` (all `pgtype.Numeric`); `gen.CustomerBillingProfile.DefaultBillRate`, `gen.PutCustomerBillingProfileRequest.DefaultBillRate` (both `*float64`).
- Wire: `defaultBillRate` (`number`, `format: double`, nullable, optional) on both schemas.
- Consumes: nothing new.

- [ ] **Step 1: Write the failing unit tests**

In `apps/server/internal/customers/billing_values_test.go`, directly after `TestValidatePaymentTermsDays_RangeIsZeroTo365`:

```go
// TestValidateDefaultBillRate_PositiveTwoDecimalsWithinTheColumn is the
// customer default bill rate's own rule (customers bill-rate design D1) at
// its edges: the smallest and largest rates numeric(12,2) holds pass, and
// zero, a negative, a third decimal and one cent past the ceiling are each
// refused with a message quoting the number sent.
func TestValidateDefaultBillRate_PositiveTwoDecimalsWithinTheColumn(t *testing.T) {
	for _, ok := range []float64{0.01, 1250, 1250.5, 9999999999.99} {
		errs := map[string][]string{}
		if got := validateDefaultBillRate(&ok, errs); got == nil || *got != ok || len(errs) != 0 {
			t.Errorf("validateDefaultBillRate(%v) = %v, %v, want it kept with no error", ok, got, errs)
		}
	}
	for _, tc := range []struct {
		rate float64
		want string
	}{
		{0, "A default bill rate must be greater than zero, but was 0"},
		{-0.01, "A default bill rate must be greater than zero, but was -0.01"},
		{1250.555, "A default bill rate must have at most two decimals, but was 1250.555"},
		{10000000000, "A default bill rate must be at most 9999999999.99, but was 10000000000"},
	} {
		errs := map[string][]string{}
		if got := validateDefaultBillRate(&tc.rate, errs); got != nil {
			t.Errorf("validateDefaultBillRate(%v) = %v, want nil", tc.rate, *got)
		}
		if msgs := errs["defaultBillRate"]; len(msgs) != 1 || msgs[0] != tc.want {
			t.Errorf("errs[defaultBillRate] = %v, want [%q]", msgs, tc.want)
		}
	}
	errs := map[string][]string{}
	if got := validateDefaultBillRate(nil, errs); got != nil || len(errs) != 0 {
		t.Errorf("validateDefaultBillRate(nil) = %v, %v, want nil and no error: absent clears", got, errs)
	}
}

// TestValidateBillingProfile_DefaultBillRateNeedsTheCurrency is the profile's
// one cross-field rule (design D1): a rate is quoted in the profile's own
// currency, so a rate with none — absent or blank, which clear alike — is
// refused on defaultBillRate. An invalid currency is refused on currency
// alone: blaming the rate as well would say the same thing twice.
func TestValidateBillingProfile_DefaultBillRateNeedsTheCurrency(t *testing.T) {
	rate := 1250.0
	want := "A default bill rate needs the billing profile's currency to be quoted in"
	for name, currency := range map[string]*string{"absent": nil, "blank": billingStrPtr("   ")} {
		_, errs := validateBillingProfile(gen.PutCustomerBillingProfileRequest{DefaultBillRate: &rate, Currency: currency})
		if msgs := errs["defaultBillRate"]; len(msgs) != 1 || msgs[0] != want || len(errs) != 1 {
			t.Errorf("%s currency: errs = %v, want only defaultBillRate [%q]", name, errs, want)
		}
	}

	_, errs := validateBillingProfile(gen.PutCustomerBillingProfileRequest{DefaultBillRate: &rate, Currency: billingStrPtr("US")})
	if len(errs["currency"]) != 1 || errs["defaultBillRate"] != nil {
		t.Errorf("invalid currency: errs = %v, want the currency's own error and none on defaultBillRate", errs)
	}

	got, errs := validateBillingProfile(gen.PutCustomerBillingProfileRequest{DefaultBillRate: &rate, Currency: billingStrPtr("nok")})
	if errs != nil || got.DefaultBillRate == nil || *got.DefaultBillRate != 1250 || got.Currency == nil || *got.Currency != "NOK" {
		t.Errorf("with a currency: got %+v, errs %v, want 1250 in NOK", got, errs)
	}
}
```

In `TestValidateBillingProfile_ValidRequestNormalizesEveryField`, add `rate := 1250.5` under `terms := int32(30)`, add `DefaultBillRate:  &rate,` to the request literal after `BuyerReference`, and after the `BuyerReference` assertion:

```go
	if got.DefaultBillRate == nil || *got.DefaultBillRate != 1250.5 {
		t.Errorf("DefaultBillRate = %v, want 1250.5", got.DefaultBillRate)
	}
```

In `TestValidateBillingProfile_MultipleInvalidValuesReportsEachField`, add `zeroRate := 0.0` under `badTerms`, `DefaultBillRate: &zeroRate,` to the request, `"defaultBillRate"` to the field list, and change the count check to `len(errs) != 5` with the message `"errs has %d keys, want 5: %v"`.

At the end of `TestBillingProfileEqual`:

```go
	rate, sameRate, otherRate := 1250.0, 1250.0, 1300.0
	if !billingProfileEqual(billingProfile{DefaultBillRate: &rate}, billingProfile{DefaultBillRate: &sameRate}) {
		t.Errorf("billingProfileEqual with the same DefaultBillRate = false, want true")
	}
	if billingProfileEqual(billingProfile{DefaultBillRate: &rate}, billingProfile{DefaultBillRate: &otherRate}) ||
		billingProfileEqual(billingProfile{DefaultBillRate: &rate}, billingProfile{}) {
		t.Errorf("billingProfileEqual with a different or cleared DefaultBillRate = true, want false")
	}
```

- [ ] **Step 2: Write the failing HTTP tests**

In `apps/server/internal/customers/billing_profile_test.go`, add to `billingProfileJSON` after `BuyerReference`:

```go
	DefaultBillRate  *float64          `json:"defaultBillRate"`
```

In `TestGetCustomersByIdBillingProfile_FreshCustomer_AllNullWithNoInvoiceAddressWarningOnly`, change the last line of the all-null condition from `got.PeppolId != nil || got.Gln != nil || got.BuyerReference != nil {` to `got.PeppolId != nil || got.Gln != nil || got.BuyerReference != nil || got.DefaultBillRate != nil {`.

In `TestPutCustomersByIdBillingProfile_SetsEveryField_ShowsInGet`, change the body's last line to `"peppolId": "0192:923609016", "gln": "4006381333931", "buyerReference": "PO-42", "defaultBillRate": 1250.5,` and add inside `want` after the `BuyerReference` check:

```go
		if p.DefaultBillRate == nil || *p.DefaultBillRate != 1250.5 {
			t.Errorf("DefaultBillRate = %v, want 1250.5", p.DefaultBillRate)
		}
```

In `TestPutCustomersByIdBillingProfile_BlankAndAbsentFieldsClearToNull`, change the first PUT's body to `{"invoiceEmail": "invoice@clearable.co", "currency": "NOK", "buyerReference": "PO-1", "defaultBillRate": 900}`, the second (the clear with blank/null) to `{"invoiceEmail": "   ", "currency": "", "buyerReference": nil, "defaultBillRate": nil}` — an explicit null clears the rate the way omission does (D1) — and the cleared check to:

```go
	if cleared.InvoiceEmail != nil || cleared.Currency != nil || cleared.BuyerReference != nil || cleared.DefaultBillRate != nil {
		t.Errorf("profile = %+v, want invoiceEmail/currency/buyerReference/defaultBillRate cleared to null", cleared)
	}
```

In `TestGetCustomer_ResponseNeverCarriesBillingFields`, add `"defaultBillRate": 1250,` to the PUT body and `"defaultBillRate"` to both `containsAny` lists.

Directly after `TestPutCustomersByIdBillingProfile_InvalidCurrency_ReturnsBadRequest`:

```go
// TestPutCustomersByIdBillingProfile_InvalidDefaultBillRate_ReturnsBadRequest
// is D1's rule for the rate itself, one case per way to break it, each sent
// with the currency it needs so the rate is the only thing wrong — and none
// of them writes anything.
func TestPutCustomersByIdBillingProfile_InvalidDefaultBillRate_ReturnsBadRequest(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Bad Rate Co")

	for _, tc := range []struct {
		rate any
		want string
	}{
		{0, "A default bill rate must be greater than zero, but was 0"},
		{-100, "A default bill rate must be greater than zero, but was -100"},
		{12.345, "A default bill rate must have at most two decimals, but was 12.345"},
		{10000000000, "A default bill rate must be at most 9999999999.99, but was 10000000000"},
	} {
		r := putBillingProfile(t, c, created.Id, map[string]any{"currency": "NOK", "defaultBillRate": tc.rate})
		if r.Status != http.StatusBadRequest {
			t.Fatalf("rate %v: status %d body %s, want 400", tc.rate, r.Status, r.Body)
		}
		var problem validationProblemJSON
		r.JSON(&problem)
		if msgs := problem.Errors["defaultBillRate"]; len(msgs) != 1 || msgs[0] != tc.want {
			t.Errorf("rate %v: errors[defaultBillRate] = %v, want [%q]", tc.rate, msgs, tc.want)
		}
	}
	if got := fetchBillingProfile(t, c, created.Id); got.DefaultBillRate != nil || got.Currency != nil || got.Revision != 1 {
		t.Errorf("profile = %+v, want nothing written by a refused PUT", got)
	}
}

// TestPutCustomersByIdBillingProfile_DefaultBillRateWithoutCurrency_ReturnsBadRequest
// is D1's currency rule over HTTP: a rate with no currency to be quoted in is
// a 400 on defaultBillRate, and because the PUT is a full replace, clearing
// the currency while sending the rate is the same refusal — leaving the
// stored pair as it was.
func TestPutCustomersByIdBillingProfile_DefaultBillRateWithoutCurrency_ReturnsBadRequest(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Rate Without Currency Co")
	want := "A default bill rate needs the billing profile's currency to be quoted in"

	refused := func(what string) {
		t.Helper()
		r := putBillingProfile(t, c, created.Id, map[string]any{"defaultBillRate": 1250})
		if r.Status != http.StatusBadRequest {
			t.Fatalf("%s: status %d body %s, want 400", what, r.Status, r.Body)
		}
		var problem validationProblemJSON
		r.JSON(&problem)
		if msgs := problem.Errors["defaultBillRate"]; len(msgs) != 1 || msgs[0] != want {
			t.Errorf("%s: errors[defaultBillRate] = %v, want [%q]", what, msgs, want)
		}
	}

	refused("no currency ever set")

	if r := putBillingProfile(t, c, created.Id, map[string]any{"currency": "NOK", "defaultBillRate": 1250}); r.Status != http.StatusOK {
		t.Fatalf("set: status %d body %s, want 200", r.Status, r.Body)
	}
	refused("currency cleared by the full replace")

	got := fetchBillingProfile(t, c, created.Id)
	if got.Currency == nil || *got.Currency != "NOK" || got.DefaultBillRate == nil || *got.DefaultBillRate != 1250 || got.Revision != 2 {
		t.Errorf("profile = %+v, want NOK 1250 at revision 2, untouched by the refusal", got)
	}
}
```

Directly after `TestPutCustomersByIdBillingProfile_ChangesFields_BumpsRevisionAndRecordsEventWithActorAndPayload`:

```go
// TestPutCustomersByIdBillingProfile_DefaultBillRate_ChangesAreWrittenAndRecordedResubmitsAreNot
// proves the rate is a full member of the profile's write path (customers
// bill-rate design D1): a PUT that changes only the rate is a real write — one
// revision, one event whose changes name defaultBillRate alone, payload version
// still 1 — resubmitting the same rate writes nothing, and omitting it clears
// it, recorded the same way.
func TestPutCustomersByIdBillingProfile_DefaultBillRate_ChangesAreWrittenAndRecordedResubmitsAreNot(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Rate Changes Co")
	const eventType = "customer.billing_profile_updated"

	rateChange := func() map[string]any {
		t.Helper()
		event := fetchTimelineEvent(t, h, created.Id, eventType)
		if event.PayloadVersion != 1 {
			t.Errorf("payload_version = %d, want 1: an added optional field is not a new shape", event.PayloadVersion)
		}
		changes, _ := event.Payload["changes"].(map[string]any)
		if len(changes) != 1 {
			t.Errorf("changes = %v, want defaultBillRate alone", changes)
		}
		change, _ := changes["defaultBillRate"].(map[string]any)
		return change
	}

	if r := putBillingProfile(t, c, created.Id, map[string]any{"currency": "NOK", "defaultBillRate": 1250.5}); r.Status != http.StatusOK {
		t.Fatalf("set: status %d body %s, want 200", r.Status, r.Body)
	}
	h.Advance(time.Second)

	r := putBillingProfile(t, c, created.Id, map[string]any{"currency": "NOK", "defaultBillRate": 1300, "revision": 2})
	if r.Status != http.StatusOK {
		t.Fatalf("change: status %d body %s, want 200", r.Status, r.Body)
	}
	var changed billingProfileJSON
	r.JSON(&changed)
	if changed.Revision != 3 || changed.DefaultBillRate == nil || *changed.DefaultBillRate != 1300 {
		t.Errorf("changed = %+v, want 1300 at revision 3", changed)
	}
	if change := rateChange(); change["before"] != 1250.5 || change["after"] != float64(1300) {
		t.Errorf("changes.defaultBillRate = %v, want before 1250.5, after 1300", change)
	}

	if r := putBillingProfile(t, c, created.Id, map[string]any{"currency": "NOK", "defaultBillRate": 1300, "revision": 3}); r.Status != http.StatusOK {
		t.Fatalf("resubmit: status %d body %s, want 200", r.Status, r.Body)
	}
	if got := fetchBillingProfile(t, c, created.Id); got.Revision != 3 {
		t.Errorf("revision after resubmitting the same rate = %d, want 3 (a no-op writes nothing)", got.Revision)
	}
	if n := countTimelineEvents(t, h, created.Id, eventType); n != 2 {
		t.Errorf("%s events = %d, want 2", eventType, n)
	}

	if r := putBillingProfile(t, c, created.Id, map[string]any{"currency": "NOK", "revision": 3}); r.Status != http.StatusOK {
		t.Fatalf("clear: status %d body %s, want 200", r.Status, r.Body)
	}
	if got := fetchBillingProfile(t, c, created.Id); got.DefaultBillRate != nil || got.Currency == nil || *got.Currency != "NOK" {
		t.Errorf("profile = %+v, want the rate cleared by omission and NOK kept", got)
	}
	if change := rateChange(); change["before"] != float64(1300) || change["after"] != nil {
		t.Errorf("changes.defaultBillRate = %v, want before 1300, after nil", change)
	}
}
```

- [ ] **Step 3: Run them and watch them fail**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'
mise exec -- go test -count=1 -run 'DefaultBillRate|BillingProfile' ./internal/customers/
```
Expected: FAIL to compile — `unknown field DefaultBillRate in struct literal of type gen.PutCustomerBillingProfileRequest` and `undefined: validateDefaultBillRate`.

- [ ] **Step 4: The migration and its registration**

Create `apps/server/internal/db/migrations/00028_customers_default_bill_rate.sql`:

```sql
-- +goose Up
-- A customer's default bill rate (customers bill-rate design D1): the
-- billing profile's eleventh column, and this module's first money column.
-- numeric(12,2) is the scale every rate in the chain already has — the
-- project default (00009) and time's bill and cost rates (00010) — so a rate
-- moves from one step of the chain to the next without rounding. It is quoted
-- in the profile's own currency column (00019): one currency per customer,
-- never a pair of its own the way a project carries one. NULL is "no rate
-- decided here", and the chain falls through to the person. No CHECK, the
-- 00019 shape: greater than zero and two decimals are validateDefaultBillRate's
-- (billing_values.go), and unlike a group's default payment term (00027) no
-- other row inherits this value, so a floor under the handler buys nothing.
ALTER TABLE customers.customers
    ADD COLUMN default_bill_rate numeric(12,2);

-- +goose Down
ALTER TABLE customers.customers
    DROP COLUMN default_bill_rate;
```

In `apps/server/internal/customers/sqlc.yaml`, after `      - ../db/migrations/00027_customers_groups.sql` add:

```yaml
      - ../db/migrations/00028_customers_default_bill_rate.sql
```

In `apps/server/internal/db/schema_test.go`, `TestCustomersBaseline_AppliesAndIsIdempotent`, directly after

```go
	if groupTermsChecks != 1 {
		t.Errorf("default_payment_terms_days CHECK constraints = %d, want 1", groupTermsChecks)
	}
```
add:
```go

	// default_bill_rate (00028) is money at the rate chain's own scale —
	// numeric(12,2), the project default's and time's rates' — and nullable,
	// NULL being "no rate decided here". Another scale would round a rate on
	// its way from one step of the chain to the next.
	var billRateColumn string
	if err := pool.QueryRow(ctx, `SELECT coalesce(max(data_type || ':' || numeric_precision || ',' || numeric_scale || ':' || is_nullable), 'MISSING')
	                              FROM information_schema.columns
	                              WHERE table_schema = 'customers' AND table_name = 'customers' AND column_name = 'default_bill_rate'`).Scan(&billRateColumn); err != nil {
		t.Fatalf("read customers.default_bill_rate: %v", err)
	}
	if billRateColumn != "numeric:12,2:YES" {
		t.Errorf("customers.default_bill_rate = %q, want %q", billRateColumn, "numeric:12,2:YES")
	}
```

- [ ] **Step 5: The three queries**

In `apps/server/internal/customers/queries/customers.sql`, replace the `GetCustomerBillingProfile` and `UpdateCustomerBillingProfile` blocks (from `-- name: GetCustomerBillingProfile :one` through the `RETURNING … buyer_reference;` line) with:

```sql
-- name: GetCustomerBillingProfile :one
-- GetCustomerBillingProfile is GET /customers/{id}/billing-profile's read
-- (invoice-ready customer design D1, D4): the eleven billing columns — the
-- default bill rate (customers bill-rate design D1) last — plus everything
-- billingWarnings (billing_profile.go) needs to compute its warnings at read
-- time — the customer's type and legal identity (the ehf_without_recipient
-- check) and its own contact-info email (the email_without_address check) — in
-- one round trip, without ever adding a billing column to
-- GetCustomer/ListCustomers's own SELECT list (the controller ruling: the
-- billing profile must never reach SafeCustomerResponse).
SELECT id, revision, type, legal_country, legal_id, legal_name, legal_source, legal_type, email,
       invoice_email, reminder_email, payment_terms_days, currency, language,
       invoice_delivery, reminder_delivery, peppol_id, gln, buyer_reference, default_bill_rate
FROM customers.customers
WHERE id = @id;

-- name: UpdateCustomerBillingProfile :one
-- UpdateCustomerBillingProfile is PUT /customers/{id}/billing-profile's
-- write (invoice-ready customer design D1, D4): a full replace of the eleven
-- billing columns only — name, status, type and the legal identity are
-- untouched, since this sub-resource never writes them. Guarded and
-- revision-bumping exactly like UpdateCustomerContactInfo above; RETURNING
-- mirrors GetCustomerBillingProfile's own column list, so the handler can
-- recompute warnings from the same row shape after the write.
UPDATE customers.customers
SET invoice_email = @invoice_email,
    reminder_email = @reminder_email,
    payment_terms_days = @payment_terms_days,
    currency = @currency,
    language = @language,
    invoice_delivery = @invoice_delivery,
    reminder_delivery = @reminder_delivery,
    peppol_id = @peppol_id,
    gln = @gln,
    buyer_reference = @buyer_reference,
    default_bill_rate = @default_bill_rate,
    updated_at = @updated_at::timestamptz,
    revision = revision + 1
WHERE id = @id
  AND (sqlc.narg(expected_revision)::int IS NULL OR revision = sqlc.narg(expected_revision)::int)
RETURNING id, revision, type, legal_country, legal_id, legal_name, legal_source, legal_type, email,
          invoice_email, reminder_email, payment_terms_days, currency, language,
          invoice_delivery, reminder_delivery, peppol_id, gln, buyer_reference, default_bill_rate;
```

In the `DirectoryBillingProfile` block, change `-- does everywhere else this module reads it), contact email and the ten` to `-- does everywhere else this module reads it), contact email and the eleven` (leave the next line alone), and replace

```sql
       c.invoice_delivery, c.reminder_delivery, c.peppol_id, c.gln, c.buyer_reference,
       g.default_payment_terms_days AS group_default_payment_terms_days
```
with
```sql
       c.invoice_delivery, c.reminder_delivery, c.peppol_id, c.gln, c.buyer_reference, c.default_bill_rate,
       g.default_payment_terms_days AS group_default_payment_terms_days
```

- [ ] **Step 6: The contract**

In `openapi/customers.yaml`, `CustomerBillingProfile`: append to the end of its `description` string, before the closing `"`:

```
 defaultBillRate is the customer's default hourly bill rate (customers bill-rate design D1), quoted in this profile's own currency: the rate chain's customer step, which Time applies to this customer's projects that price their hours neither by a billing line nor by a default of their own. It is never set without a currency.
```

and between its `currency:` and `gln:` properties add:

```yaml
                defaultBillRate:
                    description: The customer's default hourly bill rate, in this profile's currency (customers bill-rate design D1). Greater than zero, at most two decimals; absent when the customer has none.
                    format: double
                    nullable: true
                    type: number
```

In `PutCustomerBillingProfileRequest`, between its `currency:` and `gln:` properties add:

```yaml
                defaultBillRate:
                    description: The customer's default hourly bill rate (customers bill-rate design D1). Greater than zero, at most two decimals, and only with a currency to be quoted in — a rate without one is a 400 on defaultBillRate. Absent or null clears it.
                    format: double
                    nullable: true
                    type: number
```

Nothing is added to either `required:` list.

- [ ] **Step 7: Generate**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- go generate ./... && mise exec -- go generate ./... && git -C ../.. status --short
grep -n "DefaultBillRate" internal/customers/store/customers.sql.go internal/customers/gen/api.gen.go | head -20
```
Expected: `store.GetCustomerBillingProfileRow`, `store.UpdateCustomerBillingProfileRow`, `store.UpdateCustomerBillingProfileParams` and `store.DirectoryBillingProfileRow` each carry `DefaultBillRate pgtype.Numeric`; `gen.CustomerBillingProfile` and `gen.PutCustomerBillingProfileRequest` each carry `DefaultBillRate *float64`; the second `go generate` adds no diff. The build is still red — Step 8 fixes it. If sqlc named anything differently, use what it printed.

- [ ] **Step 8: The value object**

In `apps/server/internal/customers/billing_values.go`:

Change the imports to:

```go
import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
)
```

In `billingProfile`'s doc comment change `exactly the shape customers.customers' ten` to `exactly the shape customers.customers' eleven`, and add the field after `BuyerReference`:

```go
	// DefaultBillRate is the customer's default hourly bill rate (customers
	// bill-rate design D1), quoted in Currency — never set without one — and
	// read by Time's rate chain through the directory. *float64 like every
	// money field a contract carries; the column is numeric(12,2).
	DefaultBillRate *float64 `json:"defaultBillRate"`
```

In `billingProfileEqual`, change the last line `stringPtrEqual(a.BuyerReference, b.BuyerReference)` to

```go
		stringPtrEqual(a.BuyerReference, b.BuyerReference) &&
		floatPtrEqual(a.DefaultBillRate, b.DefaultBillRate)
```

Change `int32PtrEqual`'s doc comment to `// int32PtrEqual is stringPtrEqual's *int32 twin, needed for` / `// paymentTermsDays alone among this profile's eleven fields.`, and after it add:

```go
// floatPtrEqual is stringPtrEqual's *float64 twin, for defaultBillRate. Exact
// equality, not a tolerance, is right here: both sides are at most two
// decimals — one read back from numeric(12,2)'s text, the other validated to
// that precision — and the same decimal always parses to the same float64.
func floatPtrEqual(a, b *float64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
```

Replace `billingProfileFromRow` (doc comment and body) with:

```go
// billingProfileFromRow is a persisted customer row's billing profile —
// store.GetCustomerBillingProfileRow and store.UpdateCustomerBillingProfileRow
// share this shape field for field, so billing_profile.go calls this with
// either. defaultBillRate arrives already read off its numeric column
// (floatPtrFromNumeric), because that read can fail and this cannot; a caller
// whose query does not select the rate — the Peppol lookups, which only derive
// a participant from the profile — passes nil.
func billingProfileFromRow(invoiceEmail, reminderEmail *string, paymentTermsDays *int32, currency, language, invoiceDelivery, reminderDelivery, peppolID, gln, buyerReference *string, defaultBillRate *float64) billingProfile {
	return billingProfile{
		InvoiceEmail: invoiceEmail, ReminderEmail: reminderEmail, PaymentTermsDays: paymentTermsDays,
		Currency: currency, Language: language, InvoiceDelivery: invoiceDelivery, ReminderDelivery: reminderDelivery,
		PeppolID: peppolID, Gln: gln, BuyerReference: buyerReference, DefaultBillRate: defaultBillRate,
	}
}

// numericFromFloatPtr is a default bill rate on its way into numeric(12,2):
// nil stays an invalid (SQL NULL) pgtype.Numeric, and a value is scanned from
// its shortest decimal text — the digits the caller sent, never the binary
// fraction a float64 happens to hold. internal/projects/values.go's own rule
// for its money columns, duplicated because depguard forbids the import. Scan's
// error is returned, not dropped: it can only fire for an infinity, which JSON
// cannot carry, and discarding it would store a NULL for a number the caller
// actually sent.
func numericFromFloatPtr(v *float64) (pgtype.Numeric, error) {
	if v == nil {
		return pgtype.Numeric{}, nil
	}
	var n pgtype.Numeric
	if err := n.Scan(strconv.FormatFloat(*v, 'f', -1, 64)); err != nil {
		return pgtype.Numeric{}, fmt.Errorf("customers: %v is not a storable decimal: %w", *v, err)
	}
	return n, nil
}

// floatPtrFromNumeric reads a default bill rate back off its column: nil for a
// SQL NULL, never 0, which would be a rate somebody set. A number the column
// holds but Go cannot read is an error rather than a nil — rendering it absent
// would tell the caller the rate was never set — though numeric(12,2) cannot
// hold one, so nothing reachable produces it today. Projects' rule again.
func floatPtrFromNumeric(n pgtype.Numeric) (*float64, error) {
	if !n.Valid {
		return nil, nil
	}
	f, err := n.Float64Value()
	if err != nil {
		return nil, fmt.Errorf("customers: read a stored decimal: %w", err)
	}
	if !f.Valid {
		return nil, nil
	}
	return &f.Float64, nil
}
```

Directly after `validatePaymentTermsDays`:

```go
// maxBillRate is numeric(12,2)'s ceiling, default_bill_rate's column (migration
// 00028) — the width, and the number, internal/projects' maxAmount12 gives its
// own default bill rate. A rate past it is refused here rather than left to
// Postgres, whose 22003 the handler could only answer as a 500.
const maxBillRate = 9999999999.99

// validateDefaultBillRate is the customer default bill rate's own rule
// (customers bill-rate design D1): absent left nil — clearing it — and a value
// greater than zero, at most maxBillRate and at most two decimals. The
// precision half is projects' argument for its own money: the column is scale
// 2, so a third decimal would be rounded away silently, and a customer billed
// at a rate nobody typed is worse than a refusal. Projects' validatePositiveAmount
// rule, mirrored in this module's wording ("…, but was …") since depguard
// forbids sharing it; the number is quoted as its shortest decimal text, the
// digits the caller sent. Keyed like validatePaymentTermsDays, into the same
// errs map, so it is reported beside every other field's problem.
func validateDefaultBillRate(raw *float64, errs map[string][]string) *float64 {
	if raw == nil {
		return nil
	}
	v := *raw
	text := strconv.FormatFloat(v, 'f', -1, 64)
	switch {
	case v <= 0:
		errs["defaultBillRate"] = []string{fmt.Sprintf("A default bill rate must be greater than zero, but was %s", text)}
	case v > maxBillRate:
		errs["defaultBillRate"] = []string{fmt.Sprintf("A default bill rate must be at most %.2f, but was %s", maxBillRate, text)}
	case decimalPlaces(text) > 2:
		errs["defaultBillRate"] = []string{fmt.Sprintf("A default bill rate must have at most two decimals, but was %s", text)}
	default:
		return &v
	}
	return nil
}

// decimalPlaces is how many digits follow the point in text, a number's
// shortest 'f' formatting (never an exponent) — internal/projects'
// decimalPlaces, taking the text the message quotes anyway.
func decimalPlaces(text string) int {
	point := strings.IndexByte(text, '.')
	if point < 0 {
		return 0
	}
	return len(text) - point - 1
}
```

In `validateBillingProfile`, add `DefaultBillRate:  validateDefaultBillRate(req.DefaultBillRate, errs),` as the last line of the `profile` literal, and between the literal's closing `}` and `if len(errs) > 0 {` add:

```go
	// The one cross-field rule on this profile (customers bill-rate design D1):
	// a rate is quoted in the profile's own currency, so a rate with none — the
	// currency absent or blank, which clear alike, including a full replace that
	// clears it while sending the rate — is refused on the rate. Not when the
	// currency was sent and is invalid: that field already carries its own error,
	// and a second on the rate would say the same thing twice.
	if profile.DefaultBillRate != nil && profile.Currency == nil && len(errs["currency"]) == 0 {
		errs["defaultBillRate"] = []string{"A default bill rate needs the billing profile's currency to be quoted in"}
	}
```

- [ ] **Step 9: The handlers and every caller**

In `apps/server/internal/customers/billing_profile.go`:

`billingProfileResponse`'s doc comment: change `p's ten` to `p's eleven`. In its literal, change `PeppolId: p.PeppolID, Gln: p.Gln, BuyerReference: p.BuyerReference,` to `PeppolId: p.PeppolID, Gln: p.Gln, BuyerReference: p.BuyerReference, DefaultBillRate: p.DefaultBillRate,`.

In `GetCustomersByIdBillingProfile`, replace

```go
	profile := billingProfileFromRow(row.InvoiceEmail, row.ReminderEmail, row.PaymentTermsDays,
		row.Currency, row.Language, row.InvoiceDelivery, row.ReminderDelivery, row.PeppolID, row.Gln, row.BuyerReference)
```
with
```go
	defaultBillRate, err := floatPtrFromNumeric(row.DefaultBillRate)
	if err != nil {
		return nil, err
	}
	profile := billingProfileFromRow(row.InvoiceEmail, row.ReminderEmail, row.PaymentTermsDays,
		row.Currency, row.Language, row.InvoiceDelivery, row.ReminderDelivery, row.PeppolID, row.Gln, row.BuyerReference, defaultBillRate)
```

In `PutCustomersByIdBillingProfile`'s doc comment change `identical in all` / `// ten fields writes nothing` to `identical in all` / `// eleven fields writes nothing`. Replace

```go
	before := billingProfileFromRow(existing.InvoiceEmail, existing.ReminderEmail, existing.PaymentTermsDays,
		existing.Currency, existing.Language, existing.InvoiceDelivery, existing.ReminderDelivery, existing.PeppolID, existing.Gln, existing.BuyerReference)
```
with
```go
	existingRate, err := floatPtrFromNumeric(existing.DefaultBillRate)
	if err != nil {
		return nil, err
	}
	before := billingProfileFromRow(existing.InvoiceEmail, existing.ReminderEmail, existing.PaymentTermsDays,
		existing.Currency, existing.Language, existing.InvoiceDelivery, existing.ReminderDelivery, existing.PeppolID, existing.Gln, existing.BuyerReference, existingRate)
```

Directly before `now := s.deps.Clock()` add:

```go
	// The rate's column value, built before the transaction like everything
	// else this write needs: a failed conversion is an error, never a NULL.
	defaultBillRate, err := numericFromFloatPtr(after.DefaultBillRate)
	if err != nil {
		return nil, err
	}

```

and in `store.UpdateCustomerBillingProfileParams{…}` change `PeppolID: after.PeppolID, Gln: after.Gln, BuyerReference: after.BuyerReference,` to `PeppolID: after.PeppolID, Gln: after.Gln, BuyerReference: after.BuyerReference, DefaultBillRate: defaultBillRate,`.

In `apps/server/internal/customers/directory.go`, `BillingProfile`, replace

```go
	profile := billingProfileFromRow(row.InvoiceEmail, row.ReminderEmail, row.PaymentTermsDays,
		row.Currency, row.Language, row.InvoiceDelivery, row.ReminderDelivery, row.PeppolID, row.Gln, row.BuyerReference)
```
with
```go
	defaultBillRate, err := floatPtrFromNumeric(row.DefaultBillRate)
	if err != nil {
		return nil, err
	}
	profile := billingProfileFromRow(row.InvoiceEmail, row.ReminderEmail, row.PaymentTermsDays,
		row.Currency, row.Language, row.InvoiceDelivery, row.ReminderDelivery, row.PeppolID, row.Gln, row.BuyerReference, defaultBillRate)
```

In `apps/server/internal/customers/peppol_lookup.go` (`PostCustomersByIdPeppolLookup`) and at **both** sites in `apps/server/internal/customers/peppol_recheck_worker.go`, change the call's second line from `…, row.PeppolID, row.Gln, row.BuyerReference)` to `…, row.PeppolID, row.Gln, row.BuyerReference, nil)` and put this comment on the line above each call:

```go
			// No rate: the profile is read here for its Peppol participant alone.
```
(at the call's own indentation — one tab in `peppol_lookup.go`, three inside the worker's loops).

In `apps/server/internal/customers/timeline_events.go`, `billingProfileChanges`, directly after the `currency` arm:

```go
	if !floatPtrEqual(before.DefaultBillRate, after.DefaultBillRate) {
		changes["defaultBillRate"] = map[string]any{"before": before.DefaultBillRate, "after": after.DefaultBillRate}
	}
```

Prove the caller list was complete:

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
grep -rn "billingProfileFromRow(" --include=*.go internal/customers | grep -v "^internal/customers/billing_values.go"
mise exec -- go build ./... && mise exec -- go vet ./internal/customers/...
```
Expected: six calls (billing_profile.go ×2, directory.go, peppol_lookup.go, peppol_recheck_worker.go ×2), all with eleven arguments; build and vet clean.

- [ ] **Step 10: Run, regenerate the client, show the tests can fail, commit**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'
mise exec -- gofmt -w internal/customers/billing_values.go internal/customers/billing_values_test.go \
  internal/customers/billing_profile.go internal/customers/billing_profile_test.go internal/customers/timeline_events.go \
  internal/customers/directory.go internal/customers/peppol_lookup.go internal/customers/peppol_recheck_worker.go internal/db/schema_test.go
mise exec -- gofmt -l internal
mise exec -- go test -count=1 ./internal/customers/ ./internal/openapi/...
mise exec -- go test -count=1 -run 'TestCustomersBaseline|TestSqlcSchema' ./internal/db/
cd /home/anders/projects/vantigo/vantigo && mise exec -- bun run gen:client && git status --short
```
Expected: PASS (the frozen customers corpus still validates — the field is optional and absent from every recorded exchange); `gen:client` changes `apps/customers/frontend/src/api-schema.d.ts` only.

Prove each new test can fail, one mutation at a time, restoring after each: delete the `profile.DefaultBillRate != nil && … ` block in `validateBillingProfile` — `DefaultBillRateNeedsTheCurrency` and `DefaultBillRateWithoutCurrency_ReturnsBadRequest` go red (the second on its 200); change `case decimalPlaces(text) > 2:` to `> 3` — the `1250.555` and `12.345` cases go red; delete the `floatPtrEqual(…)` line from `billingProfileEqual` (and the `&&` before it) — `TestBillingProfileEqual` and `ChangesAreWrittenAndRecorded…` go red (the rate-only change is taken for a no-op: revision 2, not 3); add the line `existingRate = nil` directly after the `existingRate` conversion's error check in the PUT — `ChangesAreWrittenAndRecorded…` goes red at `changes.defaultBillRate = map[after:1300 before:<nil>]` (and, past it, on the resubmit's revision); delete the `defaultBillRate` arm of `billingProfileChanges` — red on `len(changes)`; change the migration's `numeric(12,2)` to `numeric(14,2)` and re-run `TestCustomersBaseline` — red on `numeric:14,2:YES`. Say what each printed.

```bash
cd /home/anders/projects/vantigo/vantigo
cat > /tmp/claude-1000/msg-bill-rate-1.txt <<'EOF'
feat(customers): the billing profile carries a default bill rate

A customer's billing profile gains its eleventh field, defaultBillRate, the
module's first money column: numeric(12,2) like every rate in the chain,
quoted in the profile's own currency. It is written through the existing
full-replace PUT under customers:billing-manage, read with customers:view,
never on the customer response, and recorded on the billing profile event —
payload version unchanged, since an added optional field is not a new shape.
Greater than zero, at most two decimals, within the column; and the profile's
one cross-field rule: a rate without a currency to be quoted in is refused on
the rate.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
PATHS="apps/server/internal/db/migrations/00028_customers_default_bill_rate.sql apps/server/internal/db/schema_test.go \
 apps/server/internal/customers/sqlc.yaml apps/server/internal/customers/queries/customers.sql \
 apps/server/internal/customers/store/customers.sql.go apps/server/internal/customers/store/models.go \
 openapi/customers.yaml apps/server/internal/openapi/specs/customers.yaml apps/server/internal/customers/gen/api.gen.go \
 apps/customers/frontend/src/api-schema.d.ts \
 apps/server/internal/customers/billing_values.go apps/server/internal/customers/billing_values_test.go \
 apps/server/internal/customers/billing_profile.go apps/server/internal/customers/billing_profile_test.go \
 apps/server/internal/customers/timeline_events.go apps/server/internal/customers/directory.go \
 apps/server/internal/customers/peppol_lookup.go apps/server/internal/customers/peppol_recheck_worker.go"
git add $PATHS && git commit -F /tmp/claude-1000/msg-bill-rate-1.txt -- $PATHS
git show --stat HEAD && git status --short
```
If `go generate` touched a generated file outside `PATHS` (`store/models.go` may not move if sqlc emits no table model for this schema), look at why before adding or dropping it; a path in `PATHS` that does not exist makes `git add` fail — drop it and say so (an unchanged tracked path is a no-op for both commands).

---

### Task 2: The directory exposes the customer's own rate (D2)

One field on the contract, passed through like `Currency`. No group tier, so `resolveBillingProfile` keeps its signature and no caller of it moves.

**Files:**
- Modify: `apps/server/internal/contracts/directory.go`, `apps/server/internal/customers/directory.go`, `apps/server/internal/customers/directory_internal_test.go`, `apps/server/internal/customers/directory_test.go`, `docs/customers.md`
- Read first (do not change) — every `CustomerDirectory` implementation, none of which needs an edit because the struct only gains a field and every literal of it is keyed: `apps/server/internal/projects/projects_list_internal_test.go` (`countingDirectory`), `apps/server/internal/projects/harness_test.go` (`fakeDirectory`), `apps/server/internal/integration/harness_test.go` (`fakeCustomers` — keyed `{ID: id, Name: …}`, no rate, so the composed time module in the integration suite prices exactly as before), `apps/server/internal/module/compose_test.go` (`fakeDirectory`), `apps/server/internal/communications/harness_test.go` and `apps/server/internal/energy/harness_test.go` (`fakeDirectory`, keyed)

**Interfaces:**
- Produces Go: `contracts.CustomerBillingProfile.DefaultBillRate *float64` — the customer's own value, nil when never set, quoted in `Currency` (never `""` when set).
- Consumes: Task 1's `billingProfile.DefaultBillRate` and `DirectoryBillingProfile`'s column.

- [ ] **Step 1: Write the failing tests**

In `apps/server/internal/customers/directory_internal_test.go`, in `TestResolveBillingProfile_PassThroughFields`, add `rate := 1250.5` above `profile := billingProfile{`, add `DefaultBillRate: &rate,` to the literal's last line (`Gln: …, BuyerReference: …, DefaultBillRate: &rate,`), and after the `BuyerReference` check:

```go
	if got.DefaultBillRate == nil || *got.DefaultBillRate != 1250.5 {
		t.Errorf("DefaultBillRate = %v, want 1250.5", got.DefaultBillRate)
	}
```

In that test's doc comment, replace the last line `// from the billingProfile's *string, "" when unset.` with the two lines `// from the billingProfile's *string, "" when unset — and DefaultBillRate,` and `// carried across as the pointer it is.`

At the end of the file:

```go
// TestResolveBillingProfile_DefaultBillRate_TheCustomersOwnAndNoGroupTier pins
// the rate's resolution (customers bill-rate design D2): the profile's own value
// passes through, nil stays nil — never a 0, which would be a rate somebody set
// — and a group's default payment term, the one thing a group resolves, never
// becomes a rate: there is no group tier here.
func TestResolveBillingProfile_DefaultBillRate_TheCustomersOwnAndNoGroupTier(t *testing.T) {
	groupDefault := int32(30)
	got := resolveBillingProfile(1, 1, "No Rate AS", "business", false, nil, nil, billingProfile{}, &groupDefault, nil)
	if got.DefaultBillRate != nil {
		t.Errorf("DefaultBillRate = %v, want nil: no own rate, and a group gives none", *got.DefaultBillRate)
	}

	rate := 980.75
	got = resolveBillingProfile(1, 1, "Own Rate AS", "business", false, nil, nil,
		billingProfile{Currency: billingStrPtr("NOK"), DefaultBillRate: &rate}, &groupDefault, nil)
	if got.DefaultBillRate == nil || *got.DefaultBillRate != 980.75 || got.Currency != "NOK" {
		t.Errorf("DefaultBillRate/Currency = %v/%q, want 980.75 in NOK", got.DefaultBillRate, got.Currency)
	}
}
```

In `apps/server/internal/customers/directory_test.go`, directly after `TestDirectory_BillingProfile_ArchivedResolves`:

```go
// TestDirectory_BillingProfile_DefaultBillRate_IsTheCustomersOwn proves the
// directory reads the stored rate through to the contract (customers bill-rate
// design D2) — the value Time's rate chain prices hours with — in the profile's
// currency, and nil for a customer that set none.
func TestDirectory_BillingProfile_DefaultBillRate_IsTheCustomersOwn(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	dir := newDirectory(t, h)
	priced := insertCustomer(t, h, "Timepris AS", "active")
	h.Exec(t, `UPDATE customers.customers SET currency = 'NOK', default_bill_rate = 1250.50 WHERE id = $1`, priced)
	unpriced := insertCustomer(t, h, "Uten Pris AS", "active")

	got, err := dir.BillingProfile(context.Background(), priced)
	if err != nil {
		t.Fatalf("BillingProfile(priced): %v", err)
	}
	if got.DefaultBillRate == nil || *got.DefaultBillRate != 1250.5 || got.Currency != "NOK" {
		t.Errorf("DefaultBillRate/Currency = %v/%q, want 1250.5 in NOK", got.DefaultBillRate, got.Currency)
	}

	got, err = dir.BillingProfile(context.Background(), unpriced)
	if err != nil {
		t.Fatalf("BillingProfile(unpriced): %v", err)
	}
	if got.DefaultBillRate != nil {
		t.Errorf("DefaultBillRate = %v, want nil for a customer that set none", *got.DefaultBillRate)
	}
}
```

- [ ] **Step 2: Run them and watch them fail**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'
mise exec -- go test -count=1 -run 'TestResolveBillingProfile|TestDirectory_BillingProfile' ./internal/customers/
```
Expected: FAIL to compile — `got.DefaultBillRate undefined (type *contracts.CustomerBillingProfile has no field or method DefaultBillRate)`.

- [ ] **Step 3: The contract**

In `apps/server/internal/contracts/directory.go`, `CustomerBillingProfile`, directly after `BuyerReference string`:

```go
	// DefaultBillRate is the customer's own default hourly bill rate
	// (customers bill-rate design D2), quoted in Currency — which is never ""
	// when this is set, since the billing profile refuses a rate without a
	// currency — and nil when the customer set none. Own value only: no group
	// tier, so nil means this customer decided nothing, and a consumer pricing
	// hours (Time's rate chain) falls through to its next step.
	DefaultBillRate *float64
```

- [ ] **Step 4: The pass-through**

In `apps/server/internal/customers/directory.go`, `resolveBillingProfile`'s doc comment, after the `PaymentTermsDays` bullet (ending `…a group with no default of its own and no group at all are the same answer.`) add:

```go
//   - DefaultBillRate: the billing profile's own defaultBillRate, else nil
//     (customers bill-rate design D2) — no group tier, so it passes through
//     like Currency, the currency it is quoted in. Time's rate chain reads it.
```

and in the returned literal, after `BuyerReference:   deref(profile.BuyerReference),` add `DefaultBillRate:  profile.DefaultBillRate,`.

Then prove every implementation and fake still compiles:

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
grep -rln ') BillingProfile(' --include=*.go internal | sort
mise exec -- go build ./... && mise exec -- go vet ./...
```
Expected: seven files (`communications/harness_test.go`, `customers/directory.go`, `energy/harness_test.go`, `integration/harness_test.go`, `module/compose_test.go`, `projects/harness_test.go`, `projects/projects_list_internal_test.go`), none edited; `go vet` compiles every test package.

- [ ] **Step 5: The docs' resolution table**

In `docs/customers.md`, "`BillingProfile` — what an invoice needs, already resolved", directly after the `| `PaymentTermsDays` | … |` row add:

```
| `DefaultBillRate` | The billing profile's own `defaultBillRate`, else `nil`, quoted in `Currency` (never `""` when a rate is set). **Own value only** — no group tier; a group default bill rate would slot in beside `defaultPaymentTermsDays` if it is ever wanted. [Time's rate chain](time.md#the-rate-chain) reads it: the customer step, between the project default and the person card. |
```

and change

```
**Every consumer treats `""` (a string field) or `nil` (`PaymentTermsDays`,
`InvoiceAddress`) as "not decided — use your own default"**, never as an error or
```
to
```
**Every consumer treats `""` (a string field) or `nil` (`PaymentTermsDays`,
`DefaultBillRate`, `InvoiceAddress`) as "not decided — use your own default"**, never as an error or
```

Replace the paragraph from `Today's consumers: Energy (naming the customer on a metering point/supply period),` through `of its caller: Products phase 4's customer-group prices are the intended reader.` with:

```
Today's consumers: Energy (naming the customer on a metering point/supply period),
Projects (resolving a project's `customerId`, and now naming a whole list page's
customers through `Customers` in one call), Communications (matching an
inbound email to a customer's contact), and Time (`BillingProfile`'s
`DefaultBillRate` and `Currency`, for the rate chain's customer step — asked at most
once per entry save, and only when neither a billing line nor the project priced the
hours). `ContactsByEmail` still has **no production
caller** — it exists for Communications' future customer-suggestion feature. The rest
of `BillingProfile` is ready for Invoices to read once that module exists — the same
"built ahead of its caller" position `ContactsByEmail` has been in since the
foundation. `CustomerEntry.Group` is the other seam built ahead
of its caller: Products phase 4's customer-group prices are the intended reader.
```

- [ ] **Step 6: Run, show the tests can fail, commit**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'
mise exec -- gofmt -w internal/contracts/directory.go internal/customers/directory.go \
  internal/customers/directory_internal_test.go internal/customers/directory_test.go
mise exec -- gofmt -l internal
mise exec -- go test -count=1 ./internal/customers/ ./internal/projects/ ./internal/energy/ ./internal/communications/ ./internal/module/
```
Expected: PASS. Prove the tests can fail: replace `DefaultBillRate:  profile.DefaultBillRate,` with `DefaultBillRate:  nil,` — both new tests and the pass-through test go red; restore. Add the line `defaultBillRate = nil` directly after the `defaultBillRate` conversion's error check in `directory.go`'s `BillingProfile` — only `TestDirectory_BillingProfile_DefaultBillRate_IsTheCustomersOwn` goes red (the SQL-backed one is the only test that sees the query); restore. Say what each printed.

```bash
cd /home/anders/projects/vantigo/vantigo
cat > /tmp/claude-1000/msg-bill-rate-2.txt <<'EOF'
feat(contracts): the customer directory answers a customer's default bill rate

CustomerBillingProfile.DefaultBillRate is the customer's own rate, quoted in
the profile's currency and nil when none was set. There is no group tier:
the rate passes through the directory's resolution like the currency does,
so resolveBillingProfile keeps its signature and every fake of the interface
compiles unchanged.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
PATHS="apps/server/internal/contracts/directory.go apps/server/internal/customers/directory.go \
 apps/server/internal/customers/directory_internal_test.go apps/server/internal/customers/directory_test.go docs/customers.md"
git add $PATHS && git commit -F /tmp/claude-1000/msg-bill-rate-2.txt -- $PATHS
git show --stat HEAD && git status --short
```

---

### Task 3: Time's chain gains the customer step (D3)

The directory is Time's to read for the first time. It is optional — customers may be off — so `Deps.Directory` is checked where it is used, not at mount, the way `lineRate` treats `Deps.Products`. The step is asked only once the line and the project have both priced nothing, so an entry that never gets there costs no cross-module call.

**Files:**
- Modify: `apps/server/internal/time/values.go`, `rates.go`, `module.go`, `rates_internal_test.go`, `harness_test.go`, `entries_test.go`, `openapi/time.yaml`, `apps/time/frontend/src/api/entries.ts`, `apps/time/frontend/src/api/entries.test.ts`, `docs/time.md`, `docs/projects.md`, `docs/module-boundaries.md`
- Generated: `apps/server/internal/openapi/specs/time.yaml`, `apps/server/internal/time/gen/api.gen.go`, `apps/time/frontend/src/api-schema.d.ts`
- Read first (do not change): `apps/server/internal/time/entries.go:120-140` and `:395-405` (both callers resolve rates on the pool, before `withLockedTx` — the directory call lands outside every lock), `apps/server/internal/modtest/modtest.go:167-179` (`WithDirectory` survives `Compose`: identity and time declare no `Module.Directory`)

**Interfaces:**
- Produces Go: `sourceCustomer = "customer"`; `(*server).customerRate(ctx, project contracts.ProjectEntry) (*float64, *string, error)`.
- Wire: `rateSource` may be `"customer"`; description only.
- Produces TS: `RateSource = "line" | "project" | "customer" | "person" | "none"`.
- Consumes: Task 2's `contracts.CustomerBillingProfile.DefaultBillRate`, `module.Deps.Directory`.

- [ ] **Step 1: Write the failing table**

In `apps/server/internal/time/rates_internal_test.go`, add `"errors"` to the imports (between `"context"` and `"sync"`), and after `priceList`'s `ListPrice`:

```go
// customerRates is a contracts.CustomerDirectory answering one billing
// profile per customer id and recording every id BillingProfile was asked
// about, so a test can prove the chain asks at most once and only when it
// reaches the customer step. fail makes every lookup an error. The other four
// methods are what time never asks.
type customerRates struct {
	mu       sync.Mutex
	profiles map[int32]contracts.CustomerBillingProfile
	fail     bool
	asked    []int32
}

var _ contracts.CustomerDirectory = (*customerRates)(nil)

func (c *customerRates) Customer(context.Context, int32) (*contracts.CustomerEntry, error) {
	return nil, nil
}

func (c *customerRates) Customers(context.Context, []int32) ([]contracts.CustomerEntry, error) {
	return []contracts.CustomerEntry{}, nil
}

func (c *customerRates) Contact(context.Context, int32) (*contracts.ContactEntry, error) {
	return nil, nil
}

func (c *customerRates) ContactsByEmail(context.Context, string) ([]contracts.ContactMatch, error) {
	return []contracts.ContactMatch{}, nil
}

func (c *customerRates) BillingProfile(_ context.Context, id int32) (*contracts.CustomerBillingProfile, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.asked = append(c.asked, id)
	if c.fail {
		return nil, errors.New("customer directory unavailable")
	}
	p, ok := c.profiles[id]
	if !ok {
		return nil, nil
	}
	return &p, nil
}
```

After `TestResolveRates_PricesAListLineAtNoonOnTheEntryDate`:

```go
// TestResolveRates_CustomerStep is the chain's third step as a table (customers
// bill-rate design D3): the customer's default bill rate prices what neither
// the line nor the project did, under the person card's currency rule, and
// falls through to the person whenever it cannot — customers off, no customer,
// a customer the directory does not know, one without a rate, one in another
// currency. An archived customer's rate still applies: the directory resolves
// archived customers on purpose, and a project of theirs may still be worked
// on. The directory is asked at most once, and never by an entry the chain
// priced before the step (or that is not billable at all).
func TestResolveRates_CustomerStep(t *testing.T) {
	t.Parallel()
	pool, _ := testdb.Migrated(t)
	ctx := context.Background()
	q := store.New(pool)

	nokCard := uuid.New()  // bill 1100 / cost 650 NOK
	euroCard := uuid.New() // bill 1000 / cost 500 EUR
	for _, row := range []struct {
		user       uuid.UUID
		bill, cost float64
		currency   string
	}{
		{nokCard, 1100, 650, "NOK"},
		{euroCard, 1000, 500, "EUR"},
	} {
		if _, err := pool.Exec(ctx, `INSERT INTO time.person_rates (user_id, valid_from, bill_rate, cost_rate, currency, created_at, updated_at)
		                             VALUES ($1, '2026-01-01', $2::numeric, $3::numeric, $4, now(), now())`,
			row.user, row.bill, row.cost, row.currency); err != nil {
			t.Fatal(err)
		}
	}

	const (
		nokCustomer     int32 = 7  // 1250 NOK
		sekCustomer     int32 = 8  // 1400 SEK
		noRateCustomer  int32 = 9  // NOK, no rate
		archivedCustomer int32 = 10 // 1250 NOK, archived
		unknownCustomer int32 = 99 // the directory answers (nil, nil)
	)
	directory := func() *customerRates {
		return &customerRates{profiles: map[int32]contracts.CustomerBillingProfile{
			nokCustomer:    {ID: nokCustomer, Currency: "NOK", DefaultBillRate: float(1250)},
			sekCustomer:    {ID: sekCustomer, Currency: "SEK", DefaultBillRate: float(1400)},
			noRateCustomer: {ID: noRateCustomer, Currency: "NOK"},
			// Archived customers still resolve (contracts.CustomerDirectory's own
			// rule), and a project of theirs may still be worked on: its rate applies.
			archivedCustomer: {ID: archivedCustomer, Archived: true, Currency: "NOK", DefaultBillRate: float(1250)},
		}}
	}
	project := func(currency *string, defaultRate *float64, customer *int32) contracts.ProjectEntry {
		return contracts.ProjectEntry{ID: 1, Code: "P1", BillingType: "time-and-materials", Currency: currency, DefaultBillRate: defaultRate, CustomerID: customer}
	}
	id := func(v int32) *int32 { return &v }
	fixed := &contracts.BillingLineEntry{ID: 10, PricingMode: "fixed", FixedAmount: float(1500), VariantID: 7, Active: true}

	for name, tc := range map[string]struct {
		directory   *customerRates // nil: the customers module is off
		user        uuid.UUID
		billable    bool
		project     contracts.ProjectEntry
		line        *contracts.BillingLineEntry
		wantSource  string
		wantBill    *float64
		wantBillCur *string
		wantAsked   int
	}{
		"customer default when the project has none": {
			directory: directory(), user: nokCard, billable: true, project: project(text("NOK"), nil, id(nokCustomer)),
			wantSource: "customer", wantBill: float(1250), wantBillCur: text("NOK"), wantAsked: 1,
		},
		"a project default wins over the customer's": {
			directory: directory(), user: nokCard, billable: true, project: project(text("NOK"), float(900), id(nokCustomer)),
			wantSource: "project", wantBill: float(900), wantBillCur: text("NOK"), wantAsked: 0,
		},
		"a line wins over the customer's": {
			directory: directory(), user: nokCard, billable: true, project: project(text("NOK"), nil, id(nokCustomer)), line: fixed,
			wantSource: "line", wantBill: float(1500), wantBillCur: text("NOK"), wantAsked: 0,
		},
		"a project default without a currency is no default, and the customer prices it": {
			directory: directory(), user: euroCard, billable: true, project: project(nil, float(900), id(nokCustomer)),
			wantSource: "customer", wantBill: float(1250), wantBillCur: text("NOK"), wantAsked: 1,
		},
		"a currency-less project takes the customer's currency": {
			directory: directory(), user: euroCard, billable: true, project: project(nil, nil, id(sekCustomer)),
			wantSource: "customer", wantBill: float(1400), wantBillCur: text("SEK"), wantAsked: 1,
		},
		"a customer in another currency falls through to the person": {
			directory: directory(), user: euroCard, billable: true, project: project(text("EUR"), nil, id(nokCustomer)),
			wantSource: "person", wantBill: float(1000), wantBillCur: text("EUR"), wantAsked: 1,
		},
		"a customer in another currency and a card in another is no rate": {
			directory: directory(), user: nokCard, billable: true, project: project(text("EUR"), nil, id(nokCustomer)),
			wantSource: "none", wantAsked: 1,
		},
		"customers module off falls through to the person": {
			user: nokCard, billable: true, project: project(text("NOK"), nil, id(nokCustomer)),
			wantSource: "person", wantBill: float(1100), wantBillCur: text("NOK"),
		},
		"a project without a customer falls through to the person": {
			directory: directory(), user: nokCard, billable: true, project: project(text("NOK"), nil, nil),
			wantSource: "person", wantBill: float(1100), wantBillCur: text("NOK"), wantAsked: 0,
		},
		"a customer the directory does not know falls through to the person": {
			directory: directory(), user: nokCard, billable: true, project: project(text("NOK"), nil, id(unknownCustomer)),
			wantSource: "person", wantBill: float(1100), wantBillCur: text("NOK"), wantAsked: 1,
		},
		"an archived customer's rate still applies": {
			directory: directory(), user: nokCard, billable: true, project: project(text("NOK"), nil, id(archivedCustomer)),
			wantSource: "customer", wantBill: float(1250), wantBillCur: text("NOK"), wantAsked: 1,
		},
		"a customer without a rate falls through to the person": {
			directory: directory(), user: nokCard, billable: true, project: project(text("NOK"), nil, id(noRateCustomer)),
			wantSource: "person", wantBill: float(1100), wantBillCur: text("NOK"), wantAsked: 1,
		},
		"not billable never asks": {
			directory: directory(), user: nokCard, billable: false, project: project(text("NOK"), nil, id(nokCustomer)),
			wantSource: "none", wantAsked: 0,
		},
	} {
		deps := module.Deps{}
		if tc.directory != nil {
			// Only a real directory: a nil *customerRates in the interface would
			// be a non-nil Directory, which is not what "customers off" is.
			deps.Directory = tc.directory
		}
		got, err := newServer(deps).resolveRates(ctx, q, rateRequest{
			UserID: tc.user, Billable: tc.billable, Project: tc.project, Line: tc.line, Date: date(t, "2026-09-14"),
		})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got.Source != tc.wantSource {
			t.Errorf("%s: source = %q, want %q", name, got.Source, tc.wantSource)
		}
		if !equalPtr(got.BillRate, tc.wantBill) || !equalPtr(got.BillCurrency, tc.wantBillCur) {
			t.Errorf("%s: bill = %v %v, want %v %v", name, show(got.BillRate), show(got.BillCurrency), show(tc.wantBill), show(tc.wantBillCur))
		}
		if tc.directory != nil && len(tc.directory.asked) != tc.wantAsked {
			t.Errorf("%s: directory asked %v, want %d call(s)", name, tc.directory.asked, tc.wantAsked)
		}
	}
}

// TestResolveRates_CustomerDirectoryErrorIsAnError proves a failing directory
// fails the resolve, the way a failing list price does (customers bill-rate
// design D3): a lookup silently skipped would store the person's rate on hours
// the customer's should have priced.
func TestResolveRates_CustomerDirectoryErrorIsAnError(t *testing.T) {
	t.Parallel()
	pool, _ := testdb.Migrated(t)
	customer := int32(7)
	s := newServer(module.Deps{Directory: &customerRates{fail: true}})

	_, err := s.resolveRates(context.Background(), store.New(pool), rateRequest{
		UserID: uuid.New(), Billable: true,
		Project: contracts.ProjectEntry{Currency: text("NOK"), CustomerID: &customer},
		Date:    date(t, "2026-09-14"),
	})
	if err == nil {
		t.Fatal("resolveRates with a failing directory = nil error, want the directory's error")
	}
}
```

- [ ] **Step 2: Write the failing end-to-end case**

In `apps/server/internal/time/harness_test.go`:

`harness` gains the fake — replace

```go
type harness struct {
	*modtest.Harness
	projects *fakeProjects
}
```
with
```go
type harness struct {
	*modtest.Harness
	projects  *fakeProjects
	customers *fakeCustomers
}
```

and in the doc comment above it change `// Deps.Projects and Deps.Products are fakes built directly against` / `// internal/contracts — depguard forbids internal/time/** from importing` / `// internal/projects or internal/products, even in tests.` to `// Deps.Projects, Deps.Products and Deps.Directory are fakes built directly` / `// against internal/contracts — depguard forbids internal/time/** from` / `// importing internal/projects, internal/products or internal/customers, even` / `// in tests.`

In `newTimeHarness`, replace

```go
	projects := newFakeProjects()
	base := []modtest.Option{
		modtest.WithRecorder(recorder),
		modtest.WithModule(timetracking.Module()),
		modtest.WithProjects(projects),
	}
```
with
```go
	projects := newFakeProjects()
	customers := newFakeCustomers()
	base := []modtest.Option{
		modtest.WithRecorder(recorder),
		modtest.WithModule(timetracking.Module()),
		modtest.WithProjects(projects),
		modtest.WithDirectory(customers),
	}
```
replace `h := &harness{Harness: modtest.New(t, append(base, opts...)...), projects: projects}` with `h := &harness{Harness: modtest.New(t, append(base, opts...)...), projects: projects, customers: customers}`, and inside the `t.Cleanup` func, after the projects check:

```go
		if calls := customers.locked.all(); len(calls) > 0 {
			t.Errorf("customer directory called inside a locked transaction: %v", calls)
		}
```

Directly before `// fakeCatalog is contracts.ProductCatalog over the one variant the billing`:

```go
// customerKraftVerket is the customer every fixture project but the internal
// one bills to (newFakeProjects' customer) — the id a test gives a default
// bill rate through h.customers.setBillRate.
const customerKraftVerket = 1001

// fakeCustomers is contracts.CustomerDirectory as the rate chain reads it
// (customers bill-rate design D3): a billing profile for each customer a test
// gave a default bill rate, (nil, nil) for every other id — so a harness
// nobody set a rate on prices exactly as it did before the customer step
// existed. The other four methods answer nothing; time never asks them. It
// records calls made inside a locked transaction, like the other fakes.
type fakeCustomers struct {
	mu       sync.Mutex
	profiles map[int32]contracts.CustomerBillingProfile
	locked   lockedCalls
}

var _ contracts.CustomerDirectory = (*fakeCustomers)(nil)

func newFakeCustomers() *fakeCustomers {
	return &fakeCustomers{profiles: map[int32]contracts.CustomerBillingProfile{}}
}

// setBillRate gives customerID a default bill rate quoted in currency, as a
// PUT of its billing profile would.
func (f *fakeCustomers) setBillRate(customerID int32, rate float64, currency string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.profiles[customerID] = contracts.CustomerBillingProfile{
		ID: customerID, Name: "Kraft-Verket AS", Type: "business", Currency: currency, DefaultBillRate: &rate,
	}
}

func (f *fakeCustomers) Customer(ctx context.Context, _ int32) (*contracts.CustomerEntry, error) {
	f.locked.note(ctx, "Customer")
	return nil, nil
}

func (f *fakeCustomers) Customers(ctx context.Context, _ []int32) ([]contracts.CustomerEntry, error) {
	f.locked.note(ctx, "Customers")
	return []contracts.CustomerEntry{}, nil
}

func (f *fakeCustomers) Contact(ctx context.Context, _ int32) (*contracts.ContactEntry, error) {
	f.locked.note(ctx, "Contact")
	return nil, nil
}

func (f *fakeCustomers) ContactsByEmail(ctx context.Context, _ string) ([]contracts.ContactMatch, error) {
	f.locked.note(ctx, "ContactsByEmail")
	return []contracts.ContactMatch{}, nil
}

func (f *fakeCustomers) BillingProfile(ctx context.Context, id int32) (*contracts.CustomerBillingProfile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.locked.note(ctx, "BillingProfile")
	p, ok := f.profiles[id]
	if !ok {
		return nil, nil
	}
	return &p, nil
}
```

In `apps/server/internal/time/entries_test.go`, directly after `TestPostTimeEntries_ProjectWithoutCurrency_TakesThePersonCardsCurrency`:

```go
// The customer's default bill rate is the chain's third step (customers
// bill-rate design D3): a project that prices nothing itself bills at it —
// snapshotted with rateSource "customer", in the customer's currency when the
// project has none — a customer quoted in another currency than the project's
// is passed over for the person, and a project default still wins. A draft
// re-resolves at its next save, so a changed customer rate reaches it.
func TestPostTimeEntries_NoProjectDefault_UsesTheCustomerDefaultRate(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.customers.setBillRate(customerKraftVerket, 1250, "NOK")
	c, userID := signInAs(t, h, projectNoCurrency, roleMember)
	h.projects.addRole(projectEuro, userID, roleMember)
	h.projects.addRole(projectKraftVerket, userID, roleMember)
	seedRate(t, h, userID, "2026-01-01", 1100.0, 650.0, "EUR")

	priced := createEntry(t, c, map[string]any{"projectId": projectNoCurrency})
	wantRate(t, priced, 1250, "NOK", "customer")
	wantRate(t, getEntry(t, c, priced.Id), 1250, "NOK", "customer")

	// A EUR project, a NOK customer: nothing converts, so the EUR card prices it.
	wantRate(t, createEntry(t, c, map[string]any{"projectId": projectEuro}), 1100, "EUR", "person")
	// A project with its own default never reaches the customer.
	wantRate(t, createEntry(t, c, nil), 900, "NOK", "project")

	h.customers.setBillRate(customerKraftVerket, 1300, "NOK")
	wantRate(t, updateEntry(t, c, priced, map[string]any{"hours": 3}), 1300, "NOK", "customer")
}
```

- [ ] **Step 3: Run them and watch them fail**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'
mise exec -- go test -count=1 -run 'TestResolveRates|TestPostTimeEntries_NoProjectDefault' ./internal/time/
```
Expected: FAIL — `TestResolveRates_CustomerStep` red on every `customer` case (`source = "person", want "customer"` and so on), `CustomerDirectoryErrorIsAnError` red (nil error), and the end-to-end case red on `rateSource = "person", want "customer"` (the EUR card; on `projectNoCurrency` the person card takes its own currency).

- [ ] **Step 4: The step**

In `apps/server/internal/time/values.go`, replace the sources block with:

```go
// The steps of the rate chain an entry's bill rate can come from (D3, and
// the customer's since customers bill-rate design D3), as rate_source records
// them.
const (
	sourceLine     = "line"
	sourceProject  = "project"
	sourceCustomer = "customer"
	sourcePerson   = "person"
	sourceNone     = "none"
)
```

In `apps/server/internal/time/rates.go`, replace the file's header comment (from `// This file is D3's rate chain.` through `// say, a NOK rate on a EUR project.`) with:

```go
// This file is D3's rate chain. It runs at every save while an entry is a
// draft or rejected, and what it answers is snapshotted onto the entry: from
// submitted on, a rate card, a project's default, a customer's default or a
// price list changing underneath never moves an entry's money. No currency is
// ever converted — the bill rate is in the project's currency when a line or
// the project priced it, in the customer's when the customer did (which is the
// project's whenever the project has one), and in the person card's when the
// person did, the cost rate always in the card's. A project bills in one
// currency: a customer or a card in another cannot price its hours, so that
// step answers nothing rather than storing, say, a NOK rate on a EUR project.
```

Replace `resolveRates`' doc comment and body (from `// resolveRates runs the chain (D3):` to the function's closing brace) with:

```go
// resolveRates runs the chain (D3):
//
//   - bill, only when billable: the billing line's rule (a fixed amount, or
//     the variant's list price in the project's currency on the entry date,
//     discounted when the line says so) → the project's default bill rate →
//     the project's customer's default bill rate, when customers is enabled
//     and the customer's currency is the project's (or the project has none,
//     and the customer's is taken; customers bill-rate design D3) → the
//     person's bill rate in effect on the date, when their card is in the
//     project's currency (or the project has none, and the card's is taken)
//     → none;
//   - cost, always: the person's cost rate in effect on the date → none.
//
// A step that cannot answer falls through to the next rather than ending the
// chain: a list line with products disabled, or with no price in the
// project's currency, is priced by the project's default, the customer or the
// person, the same as an entry with no line at all.
func (s *server) resolveRates(ctx context.Context, q *store.Queries, req rateRequest) (rateSnapshot, error) {
	card, err := personRate(ctx, q, req.UserID, req.Date)
	if err != nil {
		return rateSnapshot{}, err
	}
	snap := rateSnapshot{Source: sourceNone}
	var cardBill *float64
	if card != nil {
		cost, err := floatPtrFromNumeric(card.CostRate)
		if err != nil {
			return rateSnapshot{}, err
		}
		if cost != nil {
			currency := card.Currency
			snap.CostRate, snap.CostCurrency = cost, &currency
		}
		if cardBill, err = floatPtrFromNumeric(card.BillRate); err != nil {
			return rateSnapshot{}, err
		}
	}
	if !req.Billable {
		return snap, nil
	}

	lineRate, err := s.lineRate(ctx, req)
	if err != nil {
		return rateSnapshot{}, err
	}
	switch {
	case lineRate != nil:
		snap.Source, snap.BillRate, snap.BillCurrency = sourceLine, lineRate, req.Project.Currency
		return snap, nil
	case req.Project.DefaultBillRate != nil && req.Project.Currency != nil:
		rate := *req.Project.DefaultBillRate
		snap.Source, snap.BillRate, snap.BillCurrency = sourceProject, &rate, req.Project.Currency
		return snap, nil
	}

	// The customer's step asks another module, so it is asked only here, once
	// the line and the project have both priced nothing — and at most once.
	customerRate, customerCurrency, err := s.customerRate(ctx, req.Project)
	if err != nil {
		return rateSnapshot{}, err
	}
	switch {
	case customerRate != nil:
		snap.Source, snap.BillRate, snap.BillCurrency = sourceCustomer, customerRate, customerCurrency
	case cardBill != nil && (req.Project.Currency == nil || *req.Project.Currency == card.Currency):
		currency := card.Currency
		snap.Source, snap.BillRate, snap.BillCurrency = sourcePerson, cardBill, &currency
	}
	return snap, nil
}

// customerRate is the chain's third step (customers bill-rate design D3): the
// default bill rate on the billing profile of the project's customer, and the
// currency it is quoted in — both nil when the step prices nothing: a project
// with no customer, the customers module off (Deps.Directory is nil then, a
// real installation and never an error), a customer the directory does not
// know ((nil, nil)), one with no rate, or one quoted in another currency than
// the project bills in. That last is the person card's rule verbatim — nothing
// converts — and a project with no currency of its own takes the customer's.
// An archived customer still answers: a project of theirs can still be worked
// on. A directory that fails is an error, as a failing list price is: a lookup
// silently skipped would store the person's rate on hours the customer's
// should have priced.
func (s *server) customerRate(ctx context.Context, project contracts.ProjectEntry) (*float64, *string, error) {
	if project.CustomerID == nil || s.deps.Directory == nil {
		return nil, nil, nil
	}
	profile, err := s.deps.Directory.BillingProfile(ctx, *project.CustomerID)
	if err != nil {
		return nil, nil, fmt.Errorf("time: look up the customer's default bill rate: %w", err)
	}
	if profile == nil || profile.DefaultBillRate == nil || profile.Currency == "" {
		return nil, nil, nil
	}
	if project.Currency != nil && *project.Currency != profile.Currency {
		return nil, nil, nil
	}
	rate, currency := *profile.DefaultBillRate, profile.Currency
	return &rate, &currency, nil
}
```

In `apps/server/internal/time/module.go`, replace

```go
// Time reads projects only through contracts.ProjectDirectory (required:
// config refuses "time" without "projects"), names people through
// contracts.UserDirectory, and prices billing lines through
// contracts.ProductCatalog when products is enabled (D4). It provides one
```
with
```go
// Time reads projects only through contracts.ProjectDirectory (required:
// config refuses "time" without "projects"), names people through
// contracts.UserDirectory, prices billing lines through
// contracts.ProductCatalog when products is enabled (D4), and reads a
// customer's default bill rate through contracts.CustomerDirectory when
// customers is enabled (customers bill-rate design D3) — optional, so checked
// where it is read, never at mount. It provides one
```

- [ ] **Step 5: The contract's description and the time app's union**

In `openapi/time.yaml`, `TimeEntryResponse.rateSource`, change the description to:

```yaml
                    description: "The step of the rate chain the bill rate came from: 'line', 'project', 'customer', 'person' or 'none'."
```

and `TimeRateRequest.billRate`'s description — which would otherwise say the card prices whatever no line or project default did — to:

```yaml
                    description: The hourly bill rate, greater than zero. Used for an entry only when no billing line, project default or customer default prices it, and the card's currency is the project's (or the project has none).
```

In `apps/time/frontend/src/api/entries.ts`, replace

```ts
/** The rate chain step a billable entry's bill rate came from (design D3). */
export type RateSource = "line" | "project" | "person" | "none";
```
with
```ts
/**
 * The rate chain step a billable entry's bill rate came from (design D3; the
 * customer's default since the customers bill-rate design, D3).
 */
export type RateSource = "line" | "project" | "customer" | "person" | "none";
```

In `apps/time/frontend/src/api/entries.test.ts`, add `type TimeEntry,` as the first name of the existing `import { … } from "./entries";` list, and inside `describe("timeEntryQueryOptions", …)` after its `it`:

```ts
  it("carries a customer-priced entry's rate source through as the chain names it", async () => {
    // rateSource is a plain string on the wire; the union is this file's own
    // narrowing, so a step it forgets would be a type error on this fixture.
    stubFetch(() => Promise.resolve(jsonResponse(200, entry({ rateSource: "customer" }))));

    const result = (await runQuery(timeEntryQueryOptions(501))) as TimeEntry;

    expect(result.rateSource).toBe("customer");
  });
```

- [ ] **Step 6: Generate**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- go generate ./... && mise exec -- go generate ./... && mise exec -- go test -count=1 ./internal/openapi/...
cd /home/anders/projects/vantigo/vantigo && mise exec -- bun run gen:client && git status --short
```
Expected: `internal/openapi/specs/time.yaml`, `internal/time/gen/api.gen.go` (the field's comment) and `apps/time/frontend/src/api-schema.d.ts` change; nothing under customers moves.

- [ ] **Step 7: The docs**

In `docs/time.md`, "The rate chain", replace the text from `2. **The project's `defaultBillRate`**, when the project has a currency to quote it` through `the person.` (the end of the "Each step falls through…" paragraph) with:

```
2. **The project's `defaultBillRate`**, when the project has a currency to quote it
   in.
3. **The customer's default bill rate** — the `defaultBillRate` on the
   [billing profile](customers.md#billing-profile) of the project's customer, read
   through `contracts.CustomerDirectory.BillingProfile` — under the person card's
   currency rule: the project has no currency of its own (the entry takes the
   customer's), or it is the customer's. The directory is asked **at most once per
   save**, and only when the chain gets this far — never for a non-billable entry, a
   line-priced one or a project with its own default. With the customers module off,
   a project without a customer, a customer the directory does not know or one that
   set no rate, the step simply answers nothing; an archived customer still answers,
   since a project of theirs can still be worked on. A directory that *fails* fails
   the save, the way a failing list price does.
4. **The person's rate card** effective on the entry date — but **only when its
   currency is one the project bills in**: the project has no currency of its own, or
   it is the card's. A card quoted in SEK is no use to a project billed in NOK, and
   nothing here converts, so the chain ends at step 5 instead.
5. **None.** No rate is not an error; the entry is stored with `rateSource: "none"`
   and the hours simply have no amount.

Each step falls through when it has nothing to offer: a `list` line with products
disabled, or a variant with no price in the project's currency on that date, falls
through to the project default the same way a project without one falls through to
the customer, and a customer without one to the person.
```

Replace

```
**Currency is never converted.** A bill rate from a line or the project carries the
**project's** currency; a bill rate from a rate card carries the **card's**, as does
every cost rate. Three consequences follow, and all three are the same rule:
```
with
```
**Currency is never converted.** A bill rate from a line or the project carries the
**project's** currency; one from the customer carries the **customer's** (the
billing profile's `currency`, which is the project's whenever the project has one);
one from a rate card carries the **card's**, as does every cost rate. Four
consequences follow, and all four are the same rule:
```

and directly after the bullet ending `own number, in the card's currency, whatever the project bills in.` add:

```
- A customer whose currency is **not the project's** gives no bill rate either — a
  customer quoted in SEK is no use to a project billed in NOK, and the chain falls
  through to the person's card. (A project with no currency takes the customer's,
  for the card's reason.)
```

Replace `` `rateSource` is one of `line`, `project`, `person` or `none`, and it is part of the `` with `` `rateSource` is one of `line`, `project`, `customer`, `person` or `none`, and it is part of the ``.

In "No directory call inside a locked transaction", replace `contract call.** Projects, users and product prices are resolved *before* the` with `contract call.** Projects, users, product prices and customers' default bill rates are resolved *before* the`.

In "Enabling and disabling", replace

```
Two startup rules to know:
```
with
```
Three startup rules to know:
```
and

```
- `products` is optional. Without it, billing lines priced `list` or `discount` have
  no price to read and the rate chain falls through to the project default or the
  person's card.
```
with
```
- `products` is optional. Without it, billing lines priced `list` or `discount` have
  no price to read and the rate chain falls through to the project default, the
  customer's default or the person's card.
- `customers` is optional too. Without it (`Deps.Directory` is nil) the rate chain's
  customer step answers nothing, and the chain goes from the project default straight
  to the person's card.
```

In `docs/projects.md`, replace `  (billing-line rule → project default → person default). It is a financial field —` with `  (billing-line rule → project default → customer default → person default). It is a financial field —`.

In `docs/module-boundaries.md`, replace

```
Time is the first module to consume three contracts and provide one:
`contracts.ProjectDirectory` (required — hence the config check),
`contracts.UserDirectory` (always there, for names and for the rate card's user
search), `contracts.ProductCatalog` (optional — with products disabled a `list` or
`discount` billing line simply has no price, and the rate chain falls through to the
project default) — and it provides `contracts.ProjectActuals` (optional for its
```
with
```
Time consumes four contracts and provides one:
`contracts.ProjectDirectory` (required — hence the config check),
`contracts.UserDirectory` (always there, for names and for the rate card's user
search), `contracts.ProductCatalog` (optional — with products disabled a `list` or
`discount` billing line simply has no price, and the rate chain falls through to the
project default), `contracts.CustomerDirectory` (optional — the rate chain's customer
step reads a customer's default bill rate from `BillingProfile`; with customers
disabled the chain goes from the project default to the person) — and it provides
`contracts.ProjectActuals` (optional for its
```

- [ ] **Step 8: Run, show the tests can fail, commit**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'
mise exec -- gofmt -w internal/time/values.go internal/time/rates.go internal/time/module.go \
  internal/time/rates_internal_test.go internal/time/harness_test.go internal/time/entries_test.go
mise exec -- gofmt -l internal
mise exec -- go vet ./internal/time/... ./internal/integration/...
mise exec -- go test -count=1 ./internal/time/ ./internal/integration/
cd /home/anders/projects/vantigo/vantigo
mise exec -- bunx biome check --write apps/time/frontend/src/api/entries.ts apps/time/frontend/src/api/entries.test.ts
mise exec -- bun run --cwd apps/time/frontend test
mise exec -- bun run --cwd apps/time/frontend typecheck
```
Expected: PASS, every existing time test included — the harness's directory knows no rate until a test sets one, so nothing prices differently. The integration suite composes time with its own `fakeCustomers` (no rate) and still passes.

Prove the tests can fail, restoring after each: replace `if project.CustomerID == nil || s.deps.Directory == nil {` with `if project.CustomerID == nil {` — "customers module off" panics on the nil interface (a red, and exactly the installation the guard is for); drop `if project.Currency != nil && *project.Currency != profile.Currency { return nil, nil, nil }` — "another currency" cases go red with `source = "customer"`; move the `customerRate` call above the first `switch` — "a project default wins" and "a line wins" go red on `directory asked [7], want 0 call(s)`; replace the error return with `return nil, nil, nil` — `CustomerDirectoryErrorIsAnError` goes red; remove `"customer"` from the `RateSource` union — `typecheck` fails on `entry({ rateSource: "customer" })`. Say what each printed.

```bash
cd /home/anders/projects/vantigo/vantigo
cat > /tmp/claude-1000/msg-bill-rate-3.txt <<'EOF'
feat(time): the rate chain bills at the customer's default rate

A billable entry whose line and project price nothing now takes the default
bill rate on its project's customer's billing profile, before falling back to
the person's card, and records rateSource "customer". The currency rule is
the card's: the rate applies when the project has no currency (the entry takes
the customer's) or bills in the customer's; nothing converts, so a customer
quoted in another currency falls through. The customer directory is asked at
most once per save and only when the chain gets that far; customers off, no
customer or no rate is simply no rate, a failing directory an error. Drafts
pick a changed rate up at their next save; submitted entries keep theirs.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
PATHS="apps/server/internal/time/values.go apps/server/internal/time/rates.go apps/server/internal/time/module.go \
 apps/server/internal/time/rates_internal_test.go apps/server/internal/time/harness_test.go apps/server/internal/time/entries_test.go \
 openapi/time.yaml apps/server/internal/openapi/specs/time.yaml apps/server/internal/time/gen/api.gen.go \
 apps/time/frontend/src/api-schema.d.ts apps/time/frontend/src/api/entries.ts apps/time/frontend/src/api/entries.test.ts \
 docs/time.md docs/projects.md docs/module-boundaries.md"
git add $PATHS && git commit -F /tmp/claude-1000/msg-bill-rate-3.txt -- $PATHS
git show --stat HEAD && git status --short
```

---

### Task 4: The Billing modal and card (D4) and the remaining docs (D5)

The modal gains a two-decimal `NumberInput` after the currency, the card a row after the currency. The server's field errors already land by key (`form.setErrors(error.fieldErrors)`), so the currency-required error reaches the rate field with no new mapping. `valuesFromProfile`/`toInput` are shared with the Use EHF offer, which is why the rate must travel through both: that button's PUT is a full replace too, and forgetting the field there would clear a customer's rate on a click about EHF.

**Files:**
- Modify: `apps/customers/frontend/src/api/billing-profile.ts`, `api/billing-profile.test.ts`, `api/customers.test.ts`, `pages/-customer-billing-modal.tsx`, `pages/-customer-billing-modal.test.tsx`, `pages/-customer-billing-card.tsx`, `pages/-customer-billing-card.test.tsx`, `pages/-customer-peppol-status.test.tsx`, `src/i18n.ts`, `docs/customers.md`, `ROADMAP.md`
- Read first (do not change): `apps/customers/frontend/src/pages/-customer-peppol-status.tsx:256-261` (the Use EHF body is `toInput(valuesFromProfile(profile), …)`), `packages/frontend-shell/src/i18n/format.ts` (`formatters.formatNumber`)

**Interfaces:**
- Produces TS: `CustomerBillingProfile.defaultBillRate: number | null`; `CustomerBillingProfileInput.defaultBillRate: number | null`; catalog keys `billingDefaultBillRate`, `billingDefaultBillRateHint`, `billingDefaultBillRateValue`, `billingDefaultBillRateNotSet` (en + nb).
- Consumes: Task 1's wire field.

- [ ] **Step 1: Write the failing tests**

**`api/billing-profile.test.ts`:** add `defaultBillRate: null,` after `buyerReference: null,` in `profile`, in the `input` of "PUTs the full ten-field profile plus revision", and in `emptyInput`; rename that test to `"PUTs the full eleven-field profile plus revision"` and set its input's `defaultBillRate` to `1250.5` (the line becomes `defaultBillRate: 1250.5,`). After "fills in every field the server leaves out with null":

```ts
  it("carries the default bill rate through as the number the server sent", async () => {
    stubFetch(
      vi.fn().mockResolvedValue(jsonResponse(200, { revision: 3, warnings: [], currency: "NOK", defaultBillRate: 1250.5 })),
    );

    const options = customerBillingProfileQueryOptions(1001);
    const result = await (options.queryFn as (context: unknown) => Promise<unknown>)({ signal: undefined });

    expect(result).toEqual({ ...profile, currency: "NOK", defaultBillRate: 1250.5 });
  });
```

**`api/customers.test.ts`:** in `cachedProfile`, add `defaultBillRate: null,` after `buyerReference: null,`.

**`pages/-customer-peppol-status.test.tsx`:** add `defaultBillRate: null,` after `buyerReference: null,` in `emptyProfile`. In "explains the ehf_available offer with a Use EHF action, and hides it once EHF is switched on", the billing-profile GET answer becomes `{ ...emptyProfile, currency: "NOK", defaultBillRate: 1250.5, warnings: ["ehf_available"], peppolLookup: registeredWithInvoice }` and the PUT answer gains `currency: "NOK", defaultBillRate: 1250.5,` after `...emptyProfile,`; in its `sentBody` `toEqual` change `currency: null,` to `currency: "NOK",` and add `defaultBillRate: 1250.5,` after `buyerReference: null,` — the offer's PUT is a full replace, so a click about EHF must keep the stored rate. (Mutation to show it can fail: `defaultBillRate: ""` in `valuesFromProfile`, as below, turns this red too.)

**`pages/-customer-billing-modal.test.tsx`:** add `defaultBillRate: null,` after `buyerReference: null,` in `emptyProfile`. In "sends the full ten-field profile plus revision on save" — renamed `"sends the full eleven-field profile plus revision on save"` — add after the currency `type` line:

```tsx
    await userEvent.type(within(dialog).getByLabelText(/default bill rate/i), "1250.5");
```
and `defaultBillRate: 1250.5,` after `buyerReference: null,` in its `toEqual`. After "upper-cases the currency as it is typed":

```tsx
  it("keeps a stored default bill rate through a save that never touched it, and sends null once it is cleared", async () => {
    // The PUT is a full replace, and the Use EHF offer builds its body through
    // the same valuesFromProfile/toInput: a rate the form did not carry would be
    // cleared by a save about something else.
    const stored = { ...emptyProfile, currency: "NOK", defaultBillRate: 1250.5 };
    const fetchMock = renderModal(stored, {
      billingPut: () => jsonResponse(200, { ...stored, revision: 4 }),
    });
    const puts = () =>
      fetchMock.mock.calls.filter(
        ([url, init]) => String(url).endsWith("/billing-profile") && (init as RequestInit | undefined)?.method === "PUT",
      );

    const dialog = await screen.findByRole("dialog");
    const rate = within(dialog).getByLabelText(/default bill rate/i);
    expect(rate).toHaveValue("1250.5");
    await userEvent.click(within(dialog).getByRole("button", { name: /save changes/i }));

    await waitFor(() => expect(puts()).toHaveLength(1));
    expect(JSON.parse(String((lastBillingPut(fetchMock) as [string, RequestInit])[1].body)).defaultBillRate).toBe(1250.5);

    await userEvent.clear(rate);
    await userEvent.click(within(dialog).getByRole("button", { name: /save changes/i }));

    await waitFor(() => expect(puts()).toHaveLength(2));
    expect(JSON.parse(String((lastBillingPut(fetchMock) as [string, RequestInit])[1].body)).defaultBillRate).toBeNull();
  });

  it("shows the server's rate-needs-a-currency error on the rate field", async () => {
    const message = "A default bill rate needs the billing profile's currency to be quoted in";
    renderModal(emptyProfile, {
      billingPut: () =>
        jsonResponse(400, { title: "Invalid billing profile", status: 400, errors: { defaultBillRate: [message] } }),
    });

    const dialog = await screen.findByRole("dialog");
    const rate = within(dialog).getByLabelText(/default bill rate/i);
    await userEvent.type(rate, "1250");
    await userEvent.click(within(dialog).getByRole("button", { name: /save changes/i }));

    expect(await within(dialog).findByText(message)).toBeInTheDocument();
    expect(rate).toHaveAttribute("aria-invalid", "true");
  });
```

**`pages/-customer-billing-card.test.tsx`:** add `defaultBillRate: null,` after `buyerReference: null,` in `emptyProfile` and in the `toEqual` of "keeps the modal's selects controlled…". In "shows 'Not set — the invoicing default applies' for every null field", replace the comment `// Ten billing fields, all null in emptyProfile.` with the two lines `// Eleven billing fields, all null in emptyProfile — ten say the invoicing default` and `// applies; the rate has its own words (asserted below).`, and after its `expect` add:

```tsx
    // The rate's own words: when it is not set, the chain goes on to the
    // project's or the person's rate — not to an invoicing default.
    expect(screen.getByText("Not set — the project's or the person's rate applies")).toBeInTheDocument();
```
and in "reads a profile whose unset fields the server left out entirely", after its first `expect`:

```tsx
    expect(screen.getByText("Not set — the project's or the person's rate applies")).toBeInTheDocument();
```
In "renders each set field through its own human label", add `defaultBillRate: 1250,` after `buyerReference: "PO-42",` and after the `PO-42` assertion:

```tsx
    expect(screen.getByText("1,250.00 NOK per hour")).toBeInTheDocument();
    expect(screen.getByText("Default bill rate")).toBeInTheDocument();
```

- [ ] **Step 2: Run them and watch them fail**

```bash
cd /home/anders/projects/vantigo/vantigo
mise exec -- bun run --cwd apps/customers/frontend test
```
Expected: FAIL — the api tests on `defaultBillRate` (absent from the normalised profile), the modal tests on `Unable to find a label with the text of: /default bill rate/i`, the card tests on the not-set text and `1,250.00 NOK per hour`, the peppol and card body `toEqual`s on the missing `defaultBillRate: null`.

- [ ] **Step 3: The api type and boundary**

In `apps/customers/frontend/src/api/billing-profile.ts`, `CustomerBillingProfile`, after `buyerReference: string | null;`:

```ts
  /**
   * The customer's default hourly bill rate (customers bill-rate design D1),
   * quoted in `currency` — the server never stores one without it. Time's rate
   * chain prices this customer's projects at it when they price nothing
   * themselves.
   */
  defaultBillRate: number | null;
```

In `normalizeBillingProfile`, after `buyerReference: raw.buyerReference ?? null,` add `defaultBillRate: raw.defaultBillRate ?? null,`. In `CustomerBillingProfileInput`, after `buyerReference: string | null;` add `defaultBillRate: number | null;`.

- [ ] **Step 4: The modal**

In `apps/customers/frontend/src/pages/-customer-billing-modal.tsx`:

`BillingFormValues`, after `currency: string;`: `defaultBillRate: number | string;`

Change the `valuesFromProfile` doc comment's `PUT body the same way this modal's own save does — the ten fields go` to `PUT body the same way this modal's own save does — the eleven fields go`, and in its literal after `currency: profile.currency ?? "",` add `defaultBillRate: profile.defaultBillRate ?? "",`.

In `toInput`, after `currency: values.currency.trim() || null,` add `defaultBillRate: numeric(values.defaultBillRate),`.

After the currency `TextInput` (the one ending `onChange={(event) => form.setFieldValue("currency", event.currentTarget.value.toUpperCase())}` `/>`):

```tsx
          <NumberInput
            label={t("billingDefaultBillRate")}
            description={t("billingDefaultBillRateHint")}
            // A rate is money at the column's own scale — two decimals, never
            // zero or negative — which the server refuses otherwise, so the
            // input does not offer it. Empty is "cleared", like payment terms.
            decimalScale={2}
            allowNegative={false}
            min={0}
            {...form.getInputProps("defaultBillRate")}
          />
```

- [ ] **Step 5: The card**

In `apps/customers/frontend/src/pages/-customer-billing-card.tsx`, change `* The ten billing fields, in the same order the contract lists them.` to `* The eleven billing fields, in the same order the contract lists them.`. In `BillingFields`, replace

```tsx
  const { t } = useI18n("customers");
  const notSet = (
```
with
```tsx
  const { t, formatters } = useI18n("customers");
  const notSet = (
```

and directly after `<BillingRow label={t("billingCurrency")} value={value(profile.currency)} />`:

```tsx
      <BillingRow
        label={t("billingDefaultBillRate")}
        value={
          profile.defaultBillRate === null ? (
            // Not "the invoicing default": an unset rate sends the chain on to
            // the project's or the person's rate, and the words say which.
            <Text size="sm" c="dimmed">
              {t("billingDefaultBillRateNotSet")}
            </Text>
          ) : (
            <Text size="sm">
              {t("billingDefaultBillRateValue", {
                amount: formatters.formatNumber(profile.defaultBillRate, {
                  minimumFractionDigits: 2,
                  maximumFractionDigits: 2,
                }),
                // Never empty for a profile the server answered: it refuses a
                // rate without a currency.
                currency: profile.currency ?? "",
              })}
            </Text>
          )
        }
      />
```

- [ ] **Step 6: The catalogs**

In `apps/customers/frontend/src/i18n.ts`, `en`, directly after `  billingCurrency: "Currency",`:

```ts
  billingDefaultBillRate: "Default bill rate",
  billingDefaultBillRateHint: "Per hour, in the currency above — used on this customer's projects that set no rate of their own.",
  billingDefaultBillRateValue: "{{amount}} {{currency}} per hour",
  billingDefaultBillRateNotSet: "Not set — the project's or the person's rate applies",
```

and in `nb`, directly after `  billingCurrency: "Valuta",`:

```ts
  billingDefaultBillRate: "Standard timepris",
  billingDefaultBillRateHint: "Per time, i valutaen over — brukes på kundens prosjekter som ikke har egen timepris.",
  billingDefaultBillRateValue: "{{amount}} {{currency}} per time",
  billingDefaultBillRateNotSet: "Ikke satt — prosjektets eller personens timepris gjelder",
```

- [ ] **Step 7: The docs**

In `docs/customers.md`:

Replace

```
- **Billing profile** — ten nullable columns on `customers.customers` itself
  (`invoice_email`, `reminder_email`, `payment_terms_days`, `currency`, `language`,
  `invoice_delivery`, `reminder_delivery`, `peppol_id`, `gln`, `buyer_reference`):
```
with
```
- **Billing profile** — eleven nullable columns on `customers.customers` itself
  (`invoice_email`, `reminder_email`, `payment_terms_days`, `currency`, `language`,
  `invoice_delivery`, `reminder_delivery`, `peppol_id`, `gln`, `buyer_reference`,
  `default_bill_rate`):
```

Replace

```
Ten nullable columns on `customers.customers` (migration `00019`), every one
meaning "not decided here — whoever invoices uses its own default" when NULL:
```
with
```
Eleven nullable columns on `customers.customers` — ten from migration `00019`,
`default_bill_rate` from `00028` — every one meaning "not decided here — whoever
invoices (or, for the rate, whatever prices the hours) uses its own default" when
NULL:
```

Directly after the `| `currency` | … |` row of that table:

```
| `defaultBillRate` | The customer's default hourly bill rate, quoted in the profile's own `currency` — one currency per customer, never a pair of its own the way a project has. Greater than zero, at most two decimals, at most 9999999999.99 (`numeric(12,2)`, the scale every rate in the chain has). **A rate needs the currency**: a body with `defaultBillRate` and no `currency` is a 400 on `defaultBillRate`, and since the PUT is a full replace, clearing the currency while sending the rate is the same error. Omitted or null clears it. It is the customer step of [Time's rate chain](time.md#the-rate-chain), between the project default and the person card. |
```

Replace

```
**There are deliberately no cross-field rules** — no "EHF needs a Peppol id", no
"eFaktura only makes sense for a business". A profile is filled in over time, field
```
with
```
**There is one cross-field rule and deliberately no others.** The one is the rate's:
`defaultBillRate` needs `currency`, because a number with no currency to be quoted in
prices nothing. Beyond it, no "EHF needs a Peppol id", no
"eFaktura only makes sense for a business". A profile is filled in over time, field
```

Replace `comparison; a request identical in all ten fields writes nothing — no revision` with `comparison; a request identical in all eleven fields writes nothing — no revision`; `simple (a caller with plain `customers:view` never has ten billing columns handed` with `simple (a caller with plain `customers:view` never has eleven billing columns handed`; and ``customers.customers` itself (contact info and the ten billing columns), so both`` with ``customers.customers` itself (contact info and the eleven billing columns), so both``.

In "The frontend", the **Billing card** bullet, replace

```
  *Inherits 30 days from Retail* — or that the customer's own overrides one,
  from the profile's `groupDefault`.
```
with
```
  *Inherits 30 days from Retail* — or that the customer's own overrides one,
  from the profile's `groupDefault`. After the currency comes the **default bill
  rate** (*1,250.00 NOK per hour*, or *Not set — the project's or the person's rate
  applies*), edited in the modal as a two-decimal number that is cleared by emptying
  it; the server's refusal of a rate without a currency lands on that field.
```

In "What comes next", replace

```
projects module's own keys. Still ahead in phase 5: the customer default bill rate
in the rate chain (delivery B), other modules writing to the customer timeline (on
the outbox deferred until Orders), and invoiced revenue and outstanding once
Invoices exists.
```
with
```
projects module's own keys.

**Phase 5 delivery B** — the customer default bill rate — has landed on top of it,
decided in
[`docs/superpowers/specs/2026-09-24-customers-bill-rate-design.md`](superpowers/specs/2026-09-24-customers-bill-rate-design.md):
the billing profile's eleventh field, `defaultBillRate` (migration `00028`, the
module's first money column), quoted in the profile's own currency and written under
`customers:billing-manage` like every other billing field; the directory's
`BillingProfile.DefaultBillRate`, the customer's own value with no group tier; and
the customer step of [Time's rate chain](time.md#the-rate-chain), between the
project default and the person card, recorded as `rateSource: "customer"`. No
permission key was added. Still ahead in phase 5: other modules writing to the
customer timeline (on the outbox deferred until Orders), and invoiced revenue and
outstanding once Invoices exists.
```

In `ROADMAP.md`, replace `### Phase 5 — Customer 360 (one delivery done)` with `### Phase 5 — Customer 360 (two deliveries done)`, and replace

```
**Still ahead in this phase:** the customer default bill rate in the rate chain
(delivery B); other modules writing to the customer timeline, riding on the outbox
deferred until Orders; invoiced revenue and outstanding, once Invoices exists.
```
with
```
**Delivery B (done)** — decided in
[`docs/superpowers/specs/2026-09-24-customers-bill-rate-design.md`](docs/superpowers/specs/2026-09-24-customers-bill-rate-design.md):
the customer default bill rate, the billing profile's eleventh field, quoted in the
profile's own currency, and the customer step of Time's rate chain between the
project default and the person card — the person card's currency rule, nothing
converted. See [`docs/time.md#the-rate-chain`](docs/time.md#the-rate-chain).

**Still ahead in this phase:** other modules writing to the customer timeline, riding
on the outbox deferred until Orders; invoiced revenue and outstanding, once Invoices
exists.
```
and replace `explicit chain (billing-line rule → project default → person default; cost` with `explicit chain (billing-line rule → project default → customer default → person default; cost`.

- [ ] **Step 8: Run everything, show the tests can fail, commit**

```bash
cd /home/anders/projects/vantigo/vantigo
mise exec -- bunx biome check --write apps/customers/frontend/src/api/billing-profile.ts apps/customers/frontend/src/api/billing-profile.test.ts \
  apps/customers/frontend/src/api/customers.test.ts apps/customers/frontend/src/pages/-customer-billing-modal.tsx \
  apps/customers/frontend/src/pages/-customer-billing-modal.test.tsx apps/customers/frontend/src/pages/-customer-billing-card.tsx \
  apps/customers/frontend/src/pages/-customer-billing-card.test.tsx apps/customers/frontend/src/pages/-customer-peppol-status.test.tsx \
  apps/customers/frontend/src/i18n.ts
mise exec -- bun run --cwd apps/customers/frontend test
mise exec -- bun run --cwd apps/customers/frontend typecheck
mise exec -- bun run --cwd apps/customers/frontend lint
mise exec -- bun run translations:check && mise exec -- bun run i18n:test
```
Expected: PASS. If the card test prints the amount with another separator than `1,250.00` (the test locale's `Intl` output), use exactly what it printed and say so. Prove the tests can fail, restoring after each: replace `defaultBillRate: profile.defaultBillRate ?? "",` in `valuesFromProfile` with `defaultBillRate: "",` — "keeps a stored default bill rate" goes red on `toHaveValue("1250.5")` and on the body; replace `{...form.getInputProps("defaultBillRate")}` with `value={form.values.defaultBillRate} onChange={(v) => form.setFieldValue("defaultBillRate", v)}` — "shows the server's rate-needs-a-currency error" goes red (no error rendered, no `aria-invalid`); replace the not-set branch's key with `billingNotSet` — both card not-set assertions go red and the count of ten becomes eleven; delete one `nb` key — `translations:check` fails. Say what each printed.

```bash
cat > /tmp/claude-1000/msg-bill-rate-4.txt <<'EOF'
feat(customers-ui): the billing profile edits and shows a default bill rate

The Billing modal gains a default bill rate after the currency — two
decimals, never negative, empty meaning cleared — and the card a row with
the rate per hour in the profile's currency, or that the project's or the
person's rate applies when none is set. The server's refusal of a rate
without a currency lands on the rate field. The rate travels through the
same values the Use EHF offer builds its full-replace body from, so a click
about EHF keeps it. The docs and the roadmap record phase 5 delivery B.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
PATHS="apps/customers/frontend/src/api/billing-profile.ts apps/customers/frontend/src/api/billing-profile.test.ts \
 apps/customers/frontend/src/api/customers.test.ts apps/customers/frontend/src/pages/-customer-billing-modal.tsx \
 apps/customers/frontend/src/pages/-customer-billing-modal.test.tsx apps/customers/frontend/src/pages/-customer-billing-card.tsx \
 apps/customers/frontend/src/pages/-customer-billing-card.test.tsx apps/customers/frontend/src/pages/-customer-peppol-status.test.tsx \
 apps/customers/frontend/src/i18n.ts docs/customers.md ROADMAP.md"
git add $PATHS && git commit -F /tmp/claude-1000/msg-bill-rate-4.txt -- $PATHS
git show --stat HEAD && git status --short
```
If biome reformatted a file outside `PATHS`, look at it before adding it; `routeTree.gen.ts` must not move.

---

### Task 5: Verify the whole branch and open the PR

- [ ] **Step 1: The whole suite, as CI runs it**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'
mise exec -- gofmt -l internal && mise exec -- go vet ./... && mise exec -- go build ./...
taskset -c 0-3 mise exec -- go test -count=1 ./... 2>&1 | tail -40; echo "exit ${PIPESTATUS[0]}"
mise exec -- go generate ./... >/dev/null 2>&1; cd /home/anders/projects/vantigo/vantigo && git status --short   # must be clean but for go.mod/go.sum (and the plan file, if the controller has not committed it yet — it is the controller's, not the implementer's)
mise exec -- bun run gen:client && git status --short                                                         # must still be clean
mise exec -- bun run --cwd apps/customers/frontend test && mise exec -- bun run --cwd apps/time/frontend test
mise exec -- bun run --cwd apps/customers/frontend typecheck && mise exec -- bun run --cwd apps/time/frontend typecheck
mise exec -- bun run --cwd apps/customers/frontend lint && mise exec -- bun run --cwd apps/time/frontend lint
mise exec -- bun run translations:check && mise exec -- bun run i18n:test
mise exec -- bunx biome check apps/customers/frontend/src apps/time/frontend/src
cd apps/server && mise exec -- go run ./internal/openapi/cmd/contract coverage -corpus ../../openapi/testdata/exchanges -out ../../openapi/COVERAGE.md
cd /home/anders/projects/vantigo/vantigo && git status --short -- openapi/COVERAGE.md   # no new operation: must print nothing
```
`taskset -c 0-3` because the race detector and 44 CPUs disagree about this database's connection limits (the repo's local-vs-CI notes); drop it if the suite is already green without it. `main` may already be red for reasons that are not ours — if a failure is in a module this branch never touched, check it against `git log origin/main` and say so in the report rather than fixing it here. Both `coverage` flags are required in practice: `runCoverage` (`internal/openapi/cmd/contract/corpus.go`) joins the module name onto `-corpus` and writes to `-out` as given.

- [ ] **Step 2: Read the branch as a reviewer would**

```bash
cd /home/anders/projects/vantigo/vantigo
git log --oneline main..HEAD
git diff --stat main..HEAD
git diff main..HEAD -- openapi/customers.yaml openapi/time.yaml
git diff main..HEAD -- apps/server/internal/contracts
```
Check, by eye: the design and plan commits plus four task commits, each trailer `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>`; nothing under `openapi/testdata/exchanges/`; `go.mod`/`go.sum` still untracked; no `required:` list changed in either yaml; the contracts diff is one field and its comment.

- [ ] **Step 3: Open the PR**

```bash
cd /home/anders/projects/vantigo/vantigo
git push -u origin feat/customers-bill-rate
cat > /tmp/claude-1000/pr-bill-rate.md <<'EOF'
## Customer default bill rate (phase 5, delivery B)

A customer that has negotiated an hourly rate now gets it on every project of
theirs that does not price its own hours — the rate chain is **billing line →
project → customer → person**. Decided in
`docs/superpowers/specs/2026-09-24-customers-bill-rate-design.md` (D1–D5).

- **The billing profile's eleventh field**, `defaultBillRate` (migration `00028`,
  `numeric(12,2)` like every rate in the chain), quoted in the profile's own
  `currency`. Written through the existing full-replace PUT under
  `customers:billing-manage`, read with `customers:view`, never on the customer
  response, recorded on `customer.billing_profile_updated` (payload version 1).
  Greater than zero, at most two decimals; a rate without a currency is a 400 on
  `defaultBillRate` — the profile's one cross-field rule.
- **The directory exposes it**, own value only:
  `CustomerBillingProfile.DefaultBillRate`. No group tier; every fake compiles
  unchanged.
- **Time's chain gains the customer step** between the project default and the
  person card, `rateSource: "customer"`. The person card's currency rule, nothing
  converted. The directory is asked at most once per save and only when the chain
  gets there; customers off, no customer or no rate is simply no rate, a failing
  directory an error. Drafts pick a changed rate up at their next save.
- **The Billing modal and card** edit and show it; en + nb.

Contract: two optional fields on existing customers schemas, one description in
time.yaml; no operation added. The frozen corpus is untouched and still validates.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
gh pr create --base main --head feat/customers-bill-rate \
  --title "Customer default bill rate (phase 5, delivery B)" \
  --body-file /tmp/claude-1000/pr-bill-rate.md
```
`gh pr edit` is broken in this environment: to change the body afterwards use `gh api -X PATCH repos/:owner/:repo/pulls/<n> -f body=@/tmp/claude-1000/pr-bill-rate.md`. Do not merge — the user does that.

- [ ] **Step 4: Report**

Say: the PR's number and URL; each test shown able to fail and what the mutation printed; anything the generated code disagreed with this plan about (sqlc's field names in Task 1 Step 7, the card's number format in Task 4); and whether `main` was already red. Plus the places this branch decides what the spec left open, each of which should be on the record for the user's verdict:

- the currency rule fires only when the currency is **absent or blank** — an invalid currency is refused on `currency` alone, not on the rate as well;
- the three Peppol call sites pass `nil` for the rate to `billingProfileFromRow` (they derive a participant and nothing else; the recheck worker's queries do not select the column) rather than widening two more queries;
- the customer step is a second `switch` after an early return for line/project, so the directory is provably asked only on reaching step 3 — the table asserts the call count;
- the card's unset text is its own, *Not set — the project's or the person's rate applies*, rather than the card's generic *the invoicing default applies* (spec: "—"), because for a rate the fallback is the chain, not invoicing;
- an archived customer's rate still applies (the directory resolves archived customers deliberately, and a project of theirs may still be worked on); the table pins it, and D3's sentence in the spec now says so plainly;
- the customer-step table is a new keyed test (`TestResolveRates_CustomerStep`) beside `TestResolveRates`, whose positional rows would all have had to grow a directory column.
