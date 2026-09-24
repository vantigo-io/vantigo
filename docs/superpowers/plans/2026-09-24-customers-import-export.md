# Customers CSV import and export (phase 6, delivery A) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Customers leave and arrive as one CSV file. `GET /customers/export` answers the list a person is looking at — its filters, its sort, its permission-shaped columns — as the expenses payroll file's format; `GET /customers/import/template` answers the header alone; `POST /customers/import` reads the same columns back, creating a customer for a row without a `customerNumber` and updating the one it names otherwise, every row in its own transaction through the write paths a person's edits take, with a dry run first and the failed rows handed back for a re-run. No permission key, no event type, no migration.

**Architecture:** One new file per concern in `internal/customers`: `csvfile.go` (the format — constants, `csvCell`/`csvUnguard`, the decimal comma, `readCSVFile`, the column table in D1's order), `csvexport.go` (the two GETs: the list's own filter, resolved once by an extracted `customerListFilterFor` that `GetCustomers` now calls too, one `ListCustomers` of at most 5001 rows, the existing `decorate` plus two new batched queries — billing profiles and primary postal/invoice addresses — never a read per row), and `import.go` (the multipart part, the header checked against the table and the caller's keys, the group and tag vocabularies read once, then per row a pure `plan` and an in-transaction `importRow`). The importer writes only through functions the handlers themselves call, extracted in Task 3 with each handler refactored onto its own: `insertNewCustomer`, `writeCustomerCore`, `writeContactInfo`, `writeBillingProfile`, `insertAddress`/`replaceAddress`/`removeAddress`, `writeCustomerGroup`, `replaceCustomerTags`. Every row is one `db.WithTx` (retried on the tag table's deadlock, as the tags PUT is), committed in a real run and rolled back in a dry run; an in-memory check refuses a second row creating an identity the file already created, so both runs agree on it. The frontend gains an Export button (the reimbursements download pattern), an Import modal (the receipt dropzone's shape; Check → Import → Download failed rows, built client-side), `canExport`/`canImport` props from the host, and en + nb strings.

**Tech Stack:** Go 1.27 (pgx, sqlc, oapi-codegen strict server), PostgreSQL 18, React + Mantine 9 + TanStack Query, vitest, bun, mise.

**Spec:** `docs/superpowers/specs/2026-09-24-customers-import-export-design.md` (D1–D5 + "Out of scope" + "Testing"). Read it first; it is binding. Research with file:line pointers: `.superpowers/sdd/2026-09-24-customers-import-export/context-for-design.md`. The shapes this delivery copies: `apps/server/internal/expenses/reimbursements.go` (`csvDownload`, the format constants, `csvCell`, `writeCSVRow`, the 5000-row cap answered as a bare 400), `apps/server/internal/expenses/attachments.go` (`receiptPart`, `receiptBodyLimits`), `apps/server/internal/expenses/attachments_test.go:575-650` (`modtest.RawBody` multipart), `apps/expenses/frontend/src/api/reimbursements.ts` (`fileNameFrom`, the plain-`fetch` download, `saveCsv`), `apps/expenses/frontend/src/components/receipt-dropzone.tsx`, `apps/expenses/frontend/src/api/attachments.ts` (`FormData`, no hand-set `Content-Type`).

## Global Constraints

- Branch `feat/customers-import-export`. Never commit to `main`, never merge, never `--no-verify`.
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
- ONE canonical file: semicolon, UTF-8 BOM, CRLF, header row, RFC 4180 quoting, decimal comma, ISO dates, the formula-injection guard — the expenses reimbursement export's format verbatim (`expenses/reimbursements.go` constants + `csvCell`); header names are the API's JSON names (design D1's table).
- Export `GET /customers/export` (`customers:view`, `text/csv`): the list's own filters + sort, cap 5000 rows → 400; legal-identity columns ABSENT (not blank) without `customers:legal-identity-view`. `GET /customers/import/template` answers the header only.
- Import `POST /customers/import` (`customers:create` + `customers:update`, multipart `file`, body limit 5 MiB via `RouterOptions.BodyLimits`, ≤ 5000 data rows, `dryRun` default TRUE, `allowDuplicateIdentity` default false). NO new permission key: legal-identity columns need `customers:legal-identity-manage`, billing columns `customers:billing-manage`; a file with a column its sender may not write is refused WHOLE (400 naming column + key). Unknown columns → 400; the four export-only columns (`id`, `ownerName`, `createdAt`, `updatedAt`) are ignored.
- Per row: non-blank `customerNumber` → update (unknown → row error, no revision sent), blank → create; a column GROUP is applied only when one of its columns is in the header, and then as a full replace through the EXISTING write paths (same validators, same guarded queries, same timeline events, actor = the importer); `group`/`tags` by name case-insensitively, unknown → row error; a differing `type` on update → row error; each row its own transaction; sequential; a failing row never stops the file; a dry run rolls back and records nothing.
- `CustomerImportResult {dryRun, rows, created, updated, failed, errors[{row (1-based data row), column?, message}]}`; file-level refusals are 400 problems, not results.
- Frontend: Export button (reimbursements download pattern: plain fetch, Content-Disposition filename, createObjectURL), Import modal (dropzone precedent; Check → Import → Download failed rows built client-side with an `error` column) behind a new `canImport` prop (`customers:create` + `update`); the list invalidates on completion.
- Contract changes to EXISTING schemas stay additive/optional; new schemas may have required fields; the frozen corpus (`openapi/testdata/exchanges/customers.jsonl`) is untouched and must still validate.
- After any `queries/*.sql` or migration change: `mise exec -- go generate ./...` from `apps/server`; new migrations go in `sqlc.yaml` and `internal/db/schema_test.go`; new operations need `operationId`, `x-vantigo-access`, module-test coverage, a `KnownServeMuxConflicts` pin where they conflict, and a COVERAGE.md regeneration.
- Run `mise exec -- bunx biome check --write <files>` on touched frontend files before committing.

---

**How this plan reads the spec where it leaves a choice open.** Each of these is on the record for the user's verdict (Task 7 Step 4 repeats them):

1. **A multi-column group comes whole or not at all.** D3 says a group "is applied only when at least one of its columns is in the header, and then … a full replace of that group". A header carrying *some* of a group's columns (`email` without `phone` and `website`) is refused as a file-level 400 naming the missing columns, rather than read as "the absent ones are blank" (which would silently clear `phone`) or "the absent ones keep their value" (which is not a full replace). The export and the template always carry whole groups, so a round trip never meets this. The row's own `name`/`type`/`status` are the exception: each is optional on its own, because each already has the endpoint's meaning when absent — `name` absent keeps the name, while a blank `name` cell on an update is that row's error (a name can never be cleared), `status` absent or blank keeps the status (`PUT /customers/{id}` without `status`), `type` absent or blank checks nothing; a create still needs a name, and a create row in a file with no `name` column is a row error.
2. **A dry run is the real run minus the commit (controller ruling, spec D3 updated):** each row in its own transaction, rolled back — no savepoints, no lock or counter increment held past a row. Because rows are then checked independently, the importer also checks the file against itself before any row runs: of two rows creating customers with the same legal identity (country + id), the second is a row error in both runs unless `allowDuplicateIdentity`. The difference that remains — a dry run cannot see any other effect of an earlier row on a later one — is stated in the docs; the real run still refuses such a row cleanly.
3. **A row repeating the identity on file keeps that identity's source.** D1 says an imported identity's source is always `manual`; a row whose four identity cells equal the stored identity is a no-op instead, so re-importing an export never turns a Brreg pick into a manual entry (and the round trip writes nothing).
4. **The importer also ignores a column named `error`**, beside D1's four export-only ones: it is the column the failed-rows file adds, and D4's "fix them, re-import only those" needs that file to import as it is.
5. **Import is enabled once the check found at least one row that would succeed** (`created + updated ≥ 1`), not only when every row passed — D4's "Download failed rows … re-import only those" is the workflow for a file that partly fails.
6. **A row's column-less problems and unexpected failures:** a row whose cell count differs from the header's is a row error without a `column`; a database error that is not a refusal (a dead connection, say) is a 500 for the whole request, as everywhere in this module — rows already committed by a real run stay committed, and re-running the file updates them (they carry numbers only if exported again; created rows would be created twice, which the 500's rarity is judged to be worth).
7. **The import wants `customers:view` as well** (controller ruling on the pre-flight review): `permission:customers:create+customers:update+customers:view`. Every hand write the import stands in for is update plus view, and without view a row's errors ("No customer has number N", a customer's type) would describe customers the caller may not see. The global constraint's `create + update` wording predates the ruling; the host's `canImport` follows the rule.
8. **The row cap is subject to the timing rule** (Task 4 Step 6): `maxImportRows` starts at the export's 5000 and is lowered if a real run of 5000 rows does not fit in 60 seconds on the 4-CPU run.

Smaller readings, stated where they are implemented: header names match case-insensitively after trimming; an all-blank record is skipped and takes no row number (the browser numbers the same way); the decimal comma is written and either mark is read, digits only; `createdAt`/`updatedAt` are UTC RFC 3339; the export's filename date is UTC; the primary address keeps its label (the file has none); an unchanged update still counts as `updated`.

## File Structure

| File | Responsibility |
| --- | --- |
| `apps/server/internal/customers/csvfile.go`, `csvfile_test.go` | the format, the column table, the reader (Task 1) |
| `apps/server/internal/customers/customers.go` | `customerListFilter` extracted from `GetCustomers` (Task 2); `insertNewCustomer`, `writeCustomerCore` extracted (Task 3) |
| `apps/server/internal/customers/csvexport.go`, `csvexport_test.go` | the export and the template (Task 2) |
| `apps/server/internal/customers/queries/customers.sql`, `queries/addresses.sql`, `queries/groups.sql` (+ generated `store/*.sql.go`) | two batched export reads (Task 2); `CustomerIDByNumber` and a comment (Task 4) |
| `apps/server/internal/customers/legal_identity.go`, `contact_info.go`, `billing_profile.go`, `addresses.go`, `group_membership.go`, `tags.go` | each handler onto its extracted write (Task 3) |
| `apps/server/internal/customers/import.go`, `server.go`, `module.go`, `csvimport_test.go`, `csvimport_cap_test.go` | the import, its key constant, its body limit, its tests (Task 4) |
| `openapi/customers.yaml` (+ generated `internal/openapi/specs/customers.yaml`, `internal/customers/gen/api.gen.go`, `apps/customers/frontend/src/api-schema.d.ts`), `openapi/COVERAGE.md` | three operations, two schemas (Tasks 2, 4) |
| `docs/customers.md`, `ROADMAP.md` | D5 (Task 5) |
| `apps/customers/frontend/src/lib/csv.ts`, `lib/csv.test.ts` | the browser's reader/writer and the failed-rows file (Task 6) |
| `apps/customers/frontend/src/api/customers.ts`, `api/import-export.ts`, `api/import-export.test.ts` | the list's query string, the downloads, the upload (Task 6) |
| `apps/customers/frontend/src/pages/-customer-import-modal.tsx`, `-customer-import-modal.test.tsx`, `customers.index.tsx`, `-customers.index.test.tsx`, `i18n.ts` | the modal, the buttons, the strings (Task 6) |
| `apps/host/frontend/src/routes/customers/-customers-list.tsx`, `customers-list-route.test.tsx` | `canExport`/`canImport` (Task 6) |

---

### Task 1: The customers file's format (D1)

The format and the column table, with no endpoint yet: everything export and import share. Nothing here reads the database.

**Files:**
- Create: `apps/server/internal/customers/csvfile.go`, `apps/server/internal/customers/csvfile_test.go`
- Read first (do not change): `apps/server/internal/expenses/reimbursements.go:580-800` (the constants, `writeCSVRow`, `csvCell`), `apps/server/internal/customers/values.go:500-515` (`stripWhitespace`)

**Interfaces:**
- Produces Go: `csvByteOrderMark`, `csvSeparator`, `csvLineEnd`, `csvTagSeparator`, `customersFileMaxRows = 5000`; `type csvGroup string` with `csvGroupKey`, `csvGroupRow`, `csvGroupIdentity`, `csvGroupContact`, `csvGroupPostal`, `csvGroupInvoice`, `csvGroupBilling`, `csvGroupMembership`, `csvGroupTags`, `csvGroupExportOnly`; `csvImportGroups []csvGroup`; `type csvColumn struct{Name string; Group csvGroup}`; `customerCSVColumns []csvColumn`; `csvColumnsOf(csvGroup) []string`; `csvColumnNames([]csvColumn) []string`; `writeCSVRow(*bytes.Buffer, []string)`; `csvCell(string) string`; `csvUnguard(string) string`; `formatCSVDecimal(*float64) string`; `parseCSVDecimal(string) (float64, bool)`; `type csvRecord struct{Row int; Cells []string}`; `type csvFile struct{Header []string; Rows []csvRecord}`; `readCSVFile([]byte, int) (csvFile, string)`.
- Wire: none.
- Consumes: `stripWhitespace` (values.go).

- [ ] **Step 1: Write the failing unit tests**

Create `apps/server/internal/customers/csvfile_test.go`:

```go
package customers

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

// TestCustomerCSVColumns_AreDesignD1sInOrder pins the file's columns — the
// API's own JSON names, in D1's group order, export-only last. The export's
// header, the template and the importer's layout all read this one table, so a
// column renamed here is renamed in all three at once, and this test is where
// that shows.
func TestCustomerCSVColumns_AreDesignD1sInOrder(t *testing.T) {
	want := []string{
		"customerNumber", "name", "type", "status",
		"legalCountry", "legalType", "legalId", "legalName",
		"email", "phone", "website",
		"postalLine1", "postalLine2", "postalPostalCode", "postalCity", "postalRegion", "postalCountry",
		"invoiceLine1", "invoiceLine2", "invoicePostalCode", "invoiceCity", "invoiceRegion", "invoiceCountry",
		"invoiceEmail", "reminderEmail", "paymentTermsDays", "currency", "language", "invoiceDelivery",
		"reminderDelivery", "peppolId", "gln", "buyerReference", "defaultBillRate",
		"group", "tags",
		"id", "ownerName", "createdAt", "updatedAt",
	}
	if got := csvColumnNames(customerCSVColumns); !reflect.DeepEqual(got, want) {
		t.Errorf("columns =\n%v\nwant\n%v", got, want)
	}
	if got := csvColumnsOf(csvGroupPostal); !reflect.DeepEqual(got, want[11:17]) {
		t.Errorf("postal columns = %v, want %v", got, want[11:17])
	}
}

// TestCSVCell_GuardsFormulasAndQuotesWhatWouldEndTheCell is the payroll
// export's cell rule, verbatim (design D1): a cell a spreadsheet would run as a
// formula gets an apostrophe, and a cell holding the separator, a quote or a
// line break is quoted with its quotes doubled — the guard first, so a quoted
// formula is still guarded.
func TestCSVCell_GuardsFormulasAndQuotesWhatWouldEndTheCell(t *testing.T) {
	for in, want := range map[string]string{
		"Fjord AS":          "Fjord AS",
		"":                  "",
		"=SUM(A1)":          "'=SUM(A1)",
		"+47 22 33 44 55":   "'+47 22 33 44 55",
		"-5":                "'-5",
		"@home":             "'@home",
		"\tindented":        "'\tindented",
		"a;b":               `"a;b"`,
		`say "hi"`:          `"say ""hi"""`,
		"two\nlines":        "\"two\nlines\"",
		"=a;b":              `"'=a;b"`,
		"not-a-formula = 1": "not-a-formula = 1",
	} {
		if got := csvCell(in); got != want {
			t.Errorf("csvCell(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestCSVUnguard_TakesOffOnlyTheApostropheTheGuardPutOn: the importer reads
// back what the export wrote, so "'+47 …" is a phone number again — but an
// apostrophe a person typed in front of anything else is theirs, and stays.
func TestCSVUnguard_TakesOffOnlyTheApostropheTheGuardPutOn(t *testing.T) {
	for in, want := range map[string]string{
		"'+47 22 33 44 55": "+47 22 33 44 55",
		"'=SUM(A1)":        "=SUM(A1)",
		"'-5":              "-5",
		"'@home":           "@home",
		"'\tx":             "\tx",
		"'s-Hertogenbosch": "'s-Hertogenbosch",
		"'":                "'",
		"O'Brien":          "O'Brien",
		"":                 "",
	} {
		if got := csvUnguard(in); got != want {
			t.Errorf("csvUnguard(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestReadCSVFile_ReadsBackWhatTheWriterWrites is the round trip the import
// exists for: a file written the export's way — BOM, semicolons, CRLF, quoting,
// the guard — reads back as the very cells that went in, numbered from 1.
// encoding/csv folds a quoted CRLF to LF, so the line break is written as LF.
func TestReadCSVFile_ReadsBackWhatTheWriterWrites(t *testing.T) {
	var b bytes.Buffer
	b.WriteString(csvByteOrderMark)
	writeCSVRow(&b, []string{"name", "phone"})
	writeCSVRow(&b, []string{`Fjord; "Nord" AS`, "+47 22 33 44 55"})
	writeCSVRow(&b, []string{"to\nlinjer", "=1+1"})

	file, refusal := readCSVFile(b.Bytes(), customersFileMaxRows)
	if refusal != "" {
		t.Fatalf("refusal = %q, want none", refusal)
	}
	if !reflect.DeepEqual(file.Header, []string{"name", "phone"}) {
		t.Errorf("header = %q", file.Header)
	}
	want := []csvRecord{
		{Row: 1, Cells: []string{`Fjord; "Nord" AS`, "+47 22 33 44 55"}},
		{Row: 2, Cells: []string{"to\nlinjer", "=1+1"}},
	}
	if !reflect.DeepEqual(file.Rows, want) {
		t.Errorf("rows = %+v, want %+v", file.Rows, want)
	}
}

// TestReadCSVFile_TakesLFAndNoBOM_AndSkipsBlankRecordsUnnumbered: a file saved
// by something other than the export still reads. A record whose every cell is
// blank — the trailing ";;;" rows a spreadsheet leaves — is not a row: it is
// skipped and takes no number, so row 2 is the second row a person filled in,
// which is also how the browser numbers it (lib/csv.ts).
func TestReadCSVFile_TakesLFAndNoBOM_AndSkipsBlankRecordsUnnumbered(t *testing.T) {
	file, refusal := readCSVFile([]byte("name;email\nA;a@x.no\n\n; \nB;\n"), customersFileMaxRows)
	if refusal != "" {
		t.Fatalf("refusal = %q, want none", refusal)
	}
	want := []csvRecord{{Row: 1, Cells: []string{"A", "a@x.no"}}, {Row: 2, Cells: []string{"B", ""}}}
	if !reflect.DeepEqual(file.Rows, want) {
		t.Errorf("rows = %+v, want %+v", file.Rows, want)
	}
}

// TestReadCSVFile_RefusesWhatIsNotACustomersFile: each way a file is not one
// we can read is a sentence the person can act on — and a file with more rows
// than the cap is refused before a row is looked at.
func TestReadCSVFile_RefusesWhatIsNotACustomersFile(t *testing.T) {
	for _, tc := range []struct {
		name string
		data string
		want string
	}{
		{"empty", "", "The file is empty; it needs a header row naming its columns"},
		{"only a BOM", csvByteOrderMark, "The file is empty; it needs a header row naming its columns"},
		{"not UTF-8", "name\n\xff\xfe\n", "The file is not UTF-8 text; save it from the spreadsheet as CSV UTF-8"},
		{"a bare quote", "name\nA \"b\" c\n", "The file is not a semicolon-separated CSV file: "},
		{"past the cap", "name\nA\nB\nC\n", "The file holds more than 2 rows, which is more than one import takes; import it in slices of at most 2"},
	} {
		_, refusal := readCSVFile([]byte(tc.data), 2)
		if !strings.HasPrefix(refusal, tc.want) {
			t.Errorf("%s: refusal = %q, want it to start %q", tc.name, refusal, tc.want)
		}
	}
	if _, refusal := readCSVFile([]byte("name\nA\nB\n"), 2); refusal != "" {
		t.Errorf("exactly at the cap: refusal = %q, want none", refusal)
	}
}

// TestCSVDecimal_WritesTheDecimalCommaAndReadsEitherMark: money leaves with
// the decimal comma a Norwegian spreadsheet expects and two decimals, and comes
// back from whichever mark the spreadsheet used — but only as plain digits: a
// thousands separator next to a decimal mark is ambiguous and refused, never
// guessed at, and so is a lone point before exactly three digits ("1.250").
func TestCSVDecimal_WritesTheDecimalCommaAndReadsEitherMark(t *testing.T) {
	rate := 1250.5
	if got := formatCSVDecimal(&rate); got != "1250,50" {
		t.Errorf("formatCSVDecimal(1250.5) = %q, want 1250,50", got)
	}
	if got := formatCSVDecimal(nil); got != "" {
		t.Errorf("formatCSVDecimal(nil) = %q, want empty", got)
	}
	for in, want := range map[string]float64{"1250,50": 1250.5, "1250.5": 1250.5, " 1 250,50 ": 1250.5, "1 250": 1250, "-3": -3} {
		if got, ok := parseCSVDecimal(in); !ok || got != want {
			t.Errorf("parseCSVDecimal(%q) = %v, %v, want %v", in, got, ok, want)
		}
	}
	for _, in := range []string{"1.250,50", "1.250", "12.500", "abc", "", "NaN", "Inf", "1e3", "12,5,0"} {
		if got, ok := parseCSVDecimal(in); ok {
			t.Errorf("parseCSVDecimal(%q) = %v, want refused", in, got)
		}
	}
}
```

- [ ] **Step 2: Run them and watch them fail**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'
mise exec -- go test -count=1 -run 'CSV|CustomerCSVColumns' ./internal/customers/
```
Expected: FAIL to compile — `undefined: csvColumnNames`, `undefined: csvCell`, `undefined: readCSVFile`, …

- [ ] **Step 3: The format**

Create `apps/server/internal/customers/csvfile.go`:

```go
package customers

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// This file is the customers file (customers import/export design D1): one
// canonical CSV that GET /customers/export writes, GET /customers/import/template
// starts, and POST /customers/import reads back. Its form is the expenses
// payroll export's, verbatim — semicolons, a byte order mark, CRLF, RFC 4180
// quoting, the decimal comma and the formula guard — because that is the form
// a Norwegian Excel opens without an import dialog, and one form in the product
// is one thing to know. The constants and csvCell are duplicated from
// internal/expenses rather than shared: depguard keeps modules from importing
// one another, and ten lines of formatting are not worth a platform package.
//
// The header names are the API's own JSON names, so docs/customers.md's field
// tables describe the file too. No competitor's layout is documented anywhere
// this module could read it from, and one honest format beats three guessed
// ones: onboarding from Tripletex, Fiken or PowerOffice is "export there,
// rename the columns, import here".
const (
	csvByteOrderMark = "\ufeff"
	csvSeparator     = ';'
	csvLineEnd       = "\r\n"
	// csvTagSeparator joins a customer's tag names in the one tags cell. A tag
	// whose own name holds it cannot be named by a file — the only name no
	// file can reach, and a vocabulary word nobody has had reason to write.
	csvTagSeparator = "|"
	// customersFileMaxRows is the export's cap and the import's (design D2, D3):
	// the expenses precedent's five thousand, and one number for both so that a
	// file the export wrote always fits the import.
	customersFileMaxRows = 5000
)

// csvGroup is which part of a customer a column belongs to — the unit the
// import applies (design D3): a group is written through its own endpoint's
// rules, whole, or not at all. Its value is the words a refusal names it by.
type csvGroup string

const (
	csvGroupKey        csvGroup = "customer number"
	csvGroupRow        csvGroup = "customer"
	csvGroupIdentity   csvGroup = "legal identity"
	csvGroupContact    csvGroup = "contact info"
	csvGroupPostal     csvGroup = "postal address"
	csvGroupInvoice    csvGroup = "invoice address"
	csvGroupBilling    csvGroup = "billing profile"
	csvGroupMembership csvGroup = "group"
	csvGroupTags       csvGroup = "tags"
	csvGroupExportOnly csvGroup = "export only"
)

// csvImportGroups are the groups whose columns come together or not at all
// (import.go's importLayoutFor). The row's own name/type/status are not one of
// them: each already means something when it is absent. The key and the two
// relationship columns are a single column each.
var csvImportGroups = []csvGroup{csvGroupIdentity, csvGroupContact, csvGroupPostal, csvGroupInvoice, csvGroupBilling}

// csvColumn is one column of the file: its header name and its group.
type csvColumn struct {
	Name  string
	Group csvGroup
}

// customerCSVColumns is design D1's table, in its order. The export writes
// these (less the legal identity's four for a caller who may not see them),
// the template writes all but the export-only four, and the importer knows a
// column only if it is here.
var customerCSVColumns = []csvColumn{
	{"customerNumber", csvGroupKey},
	{"name", csvGroupRow}, {"type", csvGroupRow}, {"status", csvGroupRow},
	{"legalCountry", csvGroupIdentity}, {"legalType", csvGroupIdentity}, {"legalId", csvGroupIdentity}, {"legalName", csvGroupIdentity},
	{"email", csvGroupContact}, {"phone", csvGroupContact}, {"website", csvGroupContact},
	{"postalLine1", csvGroupPostal}, {"postalLine2", csvGroupPostal}, {"postalPostalCode", csvGroupPostal},
	{"postalCity", csvGroupPostal}, {"postalRegion", csvGroupPostal}, {"postalCountry", csvGroupPostal},
	{"invoiceLine1", csvGroupInvoice}, {"invoiceLine2", csvGroupInvoice}, {"invoicePostalCode", csvGroupInvoice},
	{"invoiceCity", csvGroupInvoice}, {"invoiceRegion", csvGroupInvoice}, {"invoiceCountry", csvGroupInvoice},
	{"invoiceEmail", csvGroupBilling}, {"reminderEmail", csvGroupBilling}, {"paymentTermsDays", csvGroupBilling},
	{"currency", csvGroupBilling}, {"language", csvGroupBilling}, {"invoiceDelivery", csvGroupBilling},
	{"reminderDelivery", csvGroupBilling}, {"peppolId", csvGroupBilling}, {"gln", csvGroupBilling},
	{"buyerReference", csvGroupBilling}, {"defaultBillRate", csvGroupBilling},
	{"group", csvGroupMembership}, {"tags", csvGroupTags},
	{"id", csvGroupExportOnly}, {"ownerName", csvGroupExportOnly}, {"createdAt", csvGroupExportOnly}, {"updatedAt", csvGroupExportOnly},
}

// csvColumnsOf is one group's column names, in the table's order.
func csvColumnsOf(g csvGroup) []string {
	var names []string
	for _, c := range customerCSVColumns {
		if c.Group == g {
			names = append(names, c.Name)
		}
	}
	return names
}

// csvColumnNames is columns' header row.
func csvColumnNames(columns []csvColumn) []string {
	names := make([]string, len(columns))
	for i, c := range columns {
		names[i] = c.Name
	}
	return names
}

// writeCSVRow writes one row, each cell guarded and quoted as it needs.
func writeCSVRow(b *bytes.Buffer, cells []string) {
	for i, cell := range cells {
		if i > 0 {
			b.WriteRune(csvSeparator)
		}
		b.WriteString(csvCell(cell))
	}
	b.WriteString(csvLineEnd)
}

// csvCell is one cell as it goes into the file: guarded against a spreadsheet
// reading it as a formula, then quoted if it holds anything that would
// otherwise end the cell or the row — internal/expenses' rule, word for word.
// Unlike the payroll file, this one carries values nobody vetted in almost
// every column (a phone number typed "+47 …" is exactly the case), which is why
// the importer takes the apostrophe off again (csvUnguard).
func csvCell(value string) string {
	if strings.IndexAny(value, "=+-@\t\r") == 0 {
		value = "'" + value
	}
	if !strings.ContainsAny(value, ";\"\r\n") {
		return value
	}
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

// csvUnguard is csvCell's guard taken off: an apostrophe in front of one of the
// six characters the guard protects is the guard's, and goes; any other
// apostrophe is the person's own and stays. A file the export wrote therefore
// imports as the values it was written from.
func csvUnguard(value string) string {
	if len(value) >= 2 && value[0] == '\'' && strings.IndexAny(value[1:2], "=+-@\t\r") == 0 {
		return value[1:]
	}
	return value
}

// csvDecimalPattern is the one shape a number cell may have once its spaces are
// gone and its decimal comma is a point: digits, optionally a sign, optionally
// a fraction. No exponent, no NaN, no thousands separator — ParseFloat would
// take all three, and a spreadsheet cell holding any of them is not a price.
var csvDecimalPattern = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?$`)

// csvThousandsPattern is the one shape parseCSVDecimal refuses as ambiguous:
// up to three digits, a point and exactly three digits.
var csvThousandsPattern = regexp.MustCompile(`^-?[0-9]{1,3}\.[0-9]{3}$`)

// formatCSVDecimal is money as the file carries it: two decimals and the
// decimal comma, the payroll export's csvAmount. nil is the empty cell.
func formatCSVDecimal(v *float64) string {
	if v == nil {
		return ""
	}
	return strings.Replace(strconv.FormatFloat(*v, 'f', 2, 64), ".", ",", 1)
}

// parseCSVDecimal reads a number cell back: whitespace dropped (a Norwegian
// spreadsheet groups thousands with a space, often a no-break one), and either
// mark taken as the decimal one — but never both, since "1.250,50" says
// nothing a reader can be sure of. The value's own rules (greater than zero,
// two decimals) are its validator's, not this function's.
func parseCSVDecimal(raw string) (float64, bool) {
	compact := stripWhitespace(raw)
	if strings.Contains(compact, ",") && strings.Contains(compact, ".") {
		return 0, false
	}
	// One point followed by exactly three digits after at most three — "1.250"
	// — is a thousands separator to one reader and a decimal to another, so it
	// is refused rather than read as 1.25. "1250.555" is a number with three
	// decimals, and its validator says so.
	if csvThousandsPattern.MatchString(compact) {
		return 0, false
	}
	compact = strings.Replace(compact, ",", ".", 1)
	if !csvDecimalPattern.MatchString(compact) {
		return 0, false
	}
	v, err := strconv.ParseFloat(compact, 64)
	return v, err == nil
}

// csvRecord is one data row: its number as an import result reports it — the
// first row under the header is 1 — and its cells with the guard taken off.
type csvRecord struct {
	Row   int
	Cells []string
}

// csvFile is a file read: its header as written, and its data rows.
type csvFile struct {
	Header []string
	Rows   []csvRecord
}

// The reader's refusals, each a sentence a person can act on.
const (
	csvEmptyMessage   = "The file is empty; it needs a header row naming its columns"
	csvNotUTF8Message = "The file is not UTF-8 text; save it from the spreadsheet as CSV UTF-8"
)

// readCSVFile reads data as the customers file: a byte order mark taken off if
// there is one, UTF-8 required, semicolons, CRLF or LF, RFC 4180 quoting. The
// refusal is "" on success. Rows keep whatever cell count they have — a row
// that disagrees with the header is that row's problem (import.go), not the
// file's. A record whose every cell is blank is skipped and numbered not at
// all, and more than maxRows rows is refused before the rest is read.
func readCSVFile(data []byte, maxRows int) (csvFile, string) {
	data = bytes.TrimPrefix(data, []byte(csvByteOrderMark))
	if !utf8.Valid(data) {
		return csvFile{}, csvNotUTF8Message
	}
	reader := csv.NewReader(bytes.NewReader(data))
	reader.Comma = csvSeparator
	reader.FieldsPerRecord = -1
	header, err := reader.Read()
	if errors.Is(err, io.EOF) {
		return csvFile{}, csvEmptyMessage
	}
	if err != nil {
		return csvFile{}, fmt.Sprintf("The file is not a semicolon-separated CSV file: %v", err)
	}
	file := csvFile{Header: header}
	for {
		cells, err := reader.Read()
		if errors.Is(err, io.EOF) {
			return file, ""
		}
		if err != nil {
			return csvFile{}, fmt.Sprintf("The file is not a semicolon-separated CSV file: %v", err)
		}
		if allBlank(cells) {
			continue
		}
		if len(file.Rows) == maxRows {
			return csvFile{}, fmt.Sprintf("The file holds more than %d rows, which is more than one import takes; import it in slices of at most %d", maxRows, maxRows)
		}
		for i, cell := range cells {
			cells[i] = csvUnguard(cell)
		}
		file.Rows = append(file.Rows, csvRecord{Row: len(file.Rows) + 1, Cells: cells})
	}
}

// allBlank reports whether every cell is empty or whitespace.
func allBlank(cells []string) bool {
	for _, cell := range cells {
		if strings.TrimSpace(cell) != "" {
			return false
		}
	}
	return true
}
```

- [ ] **Step 4: Run them, show they can fail, commit**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'
mise exec -- gofmt -w internal/customers/csvfile.go internal/customers/csvfile_test.go && mise exec -- gofmt -l internal
mise exec -- go vet ./internal/customers/ && mise exec -- go test -count=1 -run 'CSV|CustomerCSVColumns' ./internal/customers/
```
Expected: PASS. Nothing in production calls `csvfile.go` yet; its only callers until Task 2 are these tests, which the `unused` linter counts.

Prove each test can fail, restoring after each: change `strings.IndexAny(value, "=+-@\t\r") == 0` in `csvCell` to `== 1` — the guard test goes red on `'=SUM(A1)`; delete the `if allBlank(cells) { continue }` block — the blank-record test goes red (four rows); change `len(file.Rows) == maxRows` to `> maxRows` — "past the cap" goes red; delete the `strings.Contains(compact, ",") && strings.Contains(compact, ".")` guard — `1.250,50` is accepted (red); drop the `{"tags", csvGroupTags}` entry — the column test goes red. Say what each printed.

```bash
cd /home/anders/projects/vantigo/vantigo
cat > /tmp/claude-1000/msg-import-export-1.txt <<'EOF'
feat(customers): the customers file has one format and one column table

The format the export will write and the import will read: the expenses
payroll file's form verbatim (semicolons, a byte order mark, CRLF, RFC
4180 quoting, the decimal comma and the formula guard), and design D1's
column table in its order, the API's own JSON names. The reader takes a
byte order mark off, requires UTF-8, numbers data rows from 1, skips a
record whose every cell is blank without numbering it, takes the guard's
apostrophe off again, and refuses a file past the row cap before reading
the rest.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
PATHS="apps/server/internal/customers/csvfile.go apps/server/internal/customers/csvfile_test.go"
git add $PATHS && git commit -F /tmp/claude-1000/msg-import-export-1.txt -- $PATHS
git show --stat HEAD && git status --short
```

---

### Task 2: The export and the template (D1, D2)

The list, as the caller sees it, as a file. `GetCustomers`' parameter handling moves into `customerListFilterFor` so the export resolves exactly the list's filter; everything a row needs beyond the list's own columns is read in bulk.

**Files:**
- Create: `apps/server/internal/customers/csvexport.go`, `apps/server/internal/customers/csvexport_test.go`
- Modify: `apps/server/internal/customers/customers.go`, `queries/customers.sql`, `queries/addresses.sql`, `openapi/customers.yaml`
- Generated: `apps/server/internal/customers/store/customers.sql.go`, `store/addresses.sql.go`, `apps/server/internal/openapi/specs/customers.yaml`, `apps/server/internal/customers/gen/api.gen.go`, `apps/customers/frontend/src/api-schema.d.ts`, `openapi/COVERAGE.md`
- Read first (do not change): `openapi/expenses.yaml:3325-3403` (a `text/csv` operation), `apps/server/internal/customers/owner.go:80-217` (`decorate`), `apps/server/internal/customers/customers_list_test.go` (list helpers), `apps/server/internal/expenses/csvexport_test.go:1-90`

**Interfaces:**
- Produces Go: `type customerListFilter struct{…}`, `(*server).customerListFilterFor(ctx, gen.GetCustomersParams) (customerListFilter, string)`, `customerListFilter.countParams() store.CountCustomersParams`, `customerListFilter.listParams(pageSize, offset int32) store.ListCustomersParams`; `type csvDownload struct{body []byte; fileName string}` with `VisitGetCustomersExportResponse` and `VisitGetCustomersImportTemplateResponse`; `exportColumns(includeIdentity bool) []csvColumn`; `importTemplateColumns() []csvColumn`; `customersCSV(...)`; `tooManyCustomersToExport()`; `(*server).GetCustomersExport`, `(*server).GetCustomersImportTemplate`; `store.CustomerBillingProfilesForCustomers(ctx, ids []int32)`, `store.PrimaryAddressesForCustomers(ctx, customerIds []int32) ([]store.CustomersCustomerAddress, error)`.
- Wire: `GET /api/v1/customers/export` (`getCustomersExport`, the list's filter and sort parameters, `200 text/csv`, `400` problem, `permission:customers:view`); `GET /api/v1/customers/import/template` (`getCustomersImportTemplate`, `200 text/csv`, `permission:customers:view`).
- Consumes: Task 1's format; `decorate`, `validateGetCustomersParams`, `billingProfileFromRow`, `floatPtrFromNumeric`.

- [ ] **Step 1: Write the failing tests**

Create `apps/server/internal/customers/csvexport_test.go`:

```go
package customers_test

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is the customers file on its way out (customers import/export
// design D1, D2): GET /customers/export and GET /customers/import/template.
// The bytes are a contract with a spreadsheet, so the golden test pins them
// exactly; the rest read the file the way a spreadsheet would.

// csvBOM is the UTF-8 byte order mark the file opens with.
const csvBOM = "\xef\xbb\xbf"

// exportProblemJSON is a bare problem's title and detail — the export's 400s
// carry no errors object.
type exportProblemJSON struct {
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

// exportCSV GETs the export with query (no leading '?') and fails on anything
// but 200.
func exportCSV(t *testing.T, c *modtest.Client, query string) *modtest.Response {
	t.Helper()
	r := c.Do(http.MethodGet, "/api/v1/customers/export?"+query, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("GET /customers/export?%s: status %d body %s, want 200", query, r.Status, r.Body)
	}
	return r
}

// exportTable reads a file the way a spreadsheet does: the BOM off, semicolons,
// quotes — header first.
func exportTable(t *testing.T, body []byte) (header []string, rows [][]string) {
	t.Helper()
	reader := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(body, []byte(csvBOM))))
	reader.Comma = ';'
	records, err := reader.ReadAll()
	if err != nil {
		t.Fatalf("read the file: %v\n%s", err, body)
	}
	if len(records) == 0 {
		t.Fatalf("the file has no header row")
	}
	return records[0], records[1:]
}

// exportedNames is the name column of an export, in file order.
func exportedNames(t *testing.T, c *modtest.Client, query string) []string {
	t.Helper()
	header, rows := exportTable(t, exportCSV(t, c, query).Body)
	at := slices.Index(header, "name")
	names := make([]string, 0, len(rows))
	for _, row := range rows {
		names = append(names, row[at])
	}
	return names
}

// importableHeader is every column an import writes, in D1's order.
const importableHeader = "customerNumber;name;type;status;legalCountry;legalType;legalId;legalName;email;phone;website;" +
	"postalLine1;postalLine2;postalPostalCode;postalCity;postalRegion;postalCountry;" +
	"invoiceLine1;invoiceLine2;invoicePostalCode;invoiceCity;invoiceRegion;invoiceCountry;" +
	"invoiceEmail;reminderEmail;paymentTermsDays;currency;language;invoiceDelivery;reminderDelivery;peppolId;gln;buyerReference;defaultBillRate;" +
	"group;tags"

// TestGetCustomersExport_IsExactlyTheseBytes is the golden sample: a customer
// with every group filled in — a name carrying the separator and quotes, a phone
// number a spreadsheet would read as a formula, a rate with the decimal comma —
// and a bare one whose very name starts like a formula.
func TestGetCustomersExport_IsExactlyTheseBytes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, userID := authenticatedClientWithID(t, h)
	setDisplayName(t, h, userID, "Kari Nordmann")

	r := c.Do(http.MethodPost, "/api/v1/customers", map[string]any{
		"name":        `Fjord; "Nord" AS`,
		"identity":    map[string]any{"country": "no", "type": "business", "id": "923609016", "name": "Fjord Nord AS", "source": "manual"},
		"contactInfo": map[string]any{"email": "post@fjord.no", "phone": "+47 22 33 44 55"},
	})
	if r.Status != http.StatusCreated {
		t.Fatalf("create: status %d body %s", r.Status, r.Body)
	}
	var fjord createdCustomerJSON
	r.JSON(&fjord)
	createAddress(t, c, fjord.Id, map[string]any{"type": "postal", "line1": "Storgata 1", "postalCode": "0155", "city": "Oslo", "country": "no"})
	if r := putBillingProfile(t, c, fjord.Id, map[string]any{"paymentTermsDays": 30, "currency": "NOK", "defaultBillRate": 1250.5}); r.Status != http.StatusOK {
		t.Fatalf("billing: status %d body %s", r.Status, r.Body)
	}
	retail := createGroup(t, c, map[string]any{"name": "Retail"})
	if r := putCustomerGroup(t, c, fjord.Id, map[string]any{"groupId": retail.Id}); r.Status != http.StatusOK {
		t.Fatalf("group: status %d body %s", r.Status, r.Body)
	}
	vip := createTag(t, c, map[string]any{"name": "VIP"})
	key := createTag(t, c, map[string]any{"name": "Key"})
	if r := putCustomerTags(t, c, fjord.Id, []string{vip.Id, key.Id}); r.Status != http.StatusOK {
		t.Fatalf("tags: status %d body %s", r.Status, r.Body)
	}
	if r := putOwner(t, c, fjord.Id, map[string]any{"ownerUserId": userID.String()}); r.Status != http.StatusOK {
		t.Fatalf("owner: status %d body %s", r.Status, r.Body)
	}
	bare := createCustomer(t, c, "=Formel AS")
	stamp := h.Now().UTC().Format(time.RFC3339)

	fjordRow := []string{
		fmt.Sprint(fjord.CustomerNumber), `"Fjord; ""Nord"" AS"`, "business", "active",
		"no", "business", "923609016", "Fjord Nord AS",
		"post@fjord.no", "'+47 22 33 44 55", "",
		"Storgata 1", "", "0155", "Oslo", "", "no",
		"", "", "", "", "", "",
		"", "", "30", "NOK", "", "", "", "", "", "", "1250,50",
		"Retail", "Key|VIP",
		fmt.Sprint(fjord.Id), "Kari Nordmann", stamp, stamp,
	}
	bareRow := append([]string{fmt.Sprint(bare.CustomerNumber), "'=Formel AS", "business", "active"}, make([]string, 32)...)
	bareRow = append(bareRow, fmt.Sprint(bare.Id), "", stamp, stamp)
	want := csvBOM + strings.Join([]string{
		importableHeader + ";id;ownerName;createdAt;updatedAt",
		strings.Join(fjordRow, ";"),
		strings.Join(bareRow, ";"),
		"",
	}, "\r\n")

	if got := string(exportCSV(t, c, "").Body); got != want {
		t.Errorf("the export is\n%q\nwant\n%q", got, want)
	}
}

// TestGetCustomersExport_IsServedAsAFileNobodyCaches pins the headers a browser
// needs to save the file rather than show it, and its name: the day it was
// made, UTC.
func TestGetCustomersExport_IsServedAsAFileNobodyCaches(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	createCustomer(t, c, "Fil AS")

	r := exportCSV(t, c, "")
	for header, want := range map[string]string{
		"Content-Type":        "text/csv; charset=utf-8",
		"Content-Disposition": fmt.Sprintf("attachment; filename=%q", "customers-"+h.Now().UTC().Format(time.DateOnly)+".csv"),
		"Cache-Control":       "private, no-store",
	} {
		if got := r.Header(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
}

// TestGetCustomersExport_LegalIdentityColumnsAreAbsentWithoutLegalIdentityView
// is D2's permission shape: without customers:legal-identity-view the four
// columns are not in the file at all — absent, not blank, so the file re-imports
// without touching an identity — while a caller who may see them gets them.
func TestGetCustomersExport_LegalIdentityColumnsAreAbsentWithoutLegalIdentityView(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	owner := authenticatedClient(t, h)
	createCustomerWithIdentity(t, owner, "Fjord AS", "no", "923609016")
	legal := []string{"legalCountry", "legalType", "legalId", "legalName"}

	header, rows := exportTable(t, exportCSV(t, owner, "").Body)
	if len(header) != 40 || rows[0][slices.Index(header, "legalId")] != "923609016" {
		t.Errorf("with legal-identity-view: %d columns, legalId %q; want 40 and 923609016", len(header), rows[0][slices.Index(header, "legalId")])
	}

	viewer := h.SignIn(t, "customers:view")
	header, rows = exportTable(t, exportCSV(t, viewer, "").Body)
	for _, column := range legal {
		if slices.Contains(header, column) {
			t.Errorf("without legal-identity-view the header carries %s", column)
		}
	}
	if len(header) != 36 || len(rows) != 1 || len(rows[0]) != 36 {
		t.Errorf("without legal-identity-view: header %d columns, rows %v; want 36 and one row of 36", len(header), rows)
	}
}

// TestGetCustomersExport_HonoursTheListsFiltersAndSort: every filter the list
// takes narrows the file the same way, and the sort orders it — the file is the
// list a person is looking at. An invalid parameter is the list's own 400.
func TestGetCustomersExport_HonoursTheListsFiltersAndSort(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, userID := authenticatedClientWithID(t, h)
	alpha := createCustomer(t, c, "Alpha AS")
	beta := createCustomerOfType(t, c, "Beta Person", "person")
	gamma := createCustomer(t, c, "Gamma AS")
	if r := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d", gamma.Id), nil); r.Status != http.StatusNoContent {
		t.Fatalf("archive: status %d body %s", r.Status, r.Body)
	}
	vip := createTag(t, c, map[string]any{"name": "VIP"})
	if r := putCustomerTags(t, c, alpha.Id, []string{vip.Id}); r.Status != http.StatusOK {
		t.Fatalf("tags: status %d body %s", r.Status, r.Body)
	}
	retail := createGroup(t, c, map[string]any{"name": "Retail"})
	if r := putCustomerGroup(t, c, beta.Id, map[string]any{"groupId": retail.Id}); r.Status != http.StatusOK {
		t.Fatalf("group: status %d body %s", r.Status, r.Body)
	}
	if r := putOwner(t, c, alpha.Id, map[string]any{"ownerUserId": userID.String()}); r.Status != http.StatusOK {
		t.Fatalf("owner: status %d body %s", r.Status, r.Body)
	}

	for query, want := range map[string][]string{
		"":                                    {"Alpha AS", "Beta Person"},
		"includeArchived=true":                {"Alpha AS", "Beta Person", "Gamma AS"},
		"status=archived":                     {"Gamma AS"},
		"type=person":                         {"Beta Person"},
		"tagId=" + vip.Id:                     {"Alpha AS"},
		"groupId=" + retail.Id:                {"Beta Person"},
		"groupId=none":                        {"Alpha AS"},
		"ownerId=me":                          {"Alpha AS"},
		"search=gam&includeArchived=true":     {"Gamma AS"},
		"sortBy=name&sortDirection=desc":      {"Beta Person", "Alpha AS"},
		"search=" + url.QueryEscape("Nobody"): {},
	} {
		if got := exportedNames(t, c, query); !slices.Equal(got, want) {
			t.Errorf("?%s: names = %q, want %q", query, got, want)
		}
	}

	r := c.Do(http.MethodGet, "/api/v1/customers/export?status=Active", nil)
	var problem exportProblemJSON
	r.JSON(&problem)
	if r.Status != http.StatusBadRequest || problem.Title != "Invalid query parameters" ||
		problem.Detail != "'status' must be one of 'active', 'disabled' or 'archived', but was 'Active'." {
		t.Errorf("status=Active: %d %+v, want the list's own 400", r.Status, problem)
	}
}

// TestGetCustomersExport_MoreThan5000Customers_IsRefusedAskingForANarrowerFilter
// is D2's cap at its edge: 5000 customers are one file, 5001 are a 400 with no
// errors object that asks for a narrower filter — and the narrower filter works.
// The customers are written in one statement: the cap is about the file, not
// about how they came to exist.
func TestGetCustomersExport_MoreThan5000Customers_IsRefusedAskingForANarrowerFilter(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	h.Exec(t, `INSERT INTO customers.customers (customer_number, name, status, created_at, updated_at)
	           SELECT 100000 + n, 'Bulk ' || n, 'active', $1, $1 FROM generate_series(1, 5001) AS n`, h.Now())

	r := c.Do(http.MethodGet, "/api/v1/customers/export", nil)
	var problem exportProblemJSON
	r.JSON(&problem)
	if r.Status != http.StatusBadRequest || problem.Title != "Too many customers to export" || !strings.Contains(problem.Detail, "more than 5000 customers") {
		t.Fatalf("5001 customers: %d %+v, want the cap's 400", r.Status, problem)
	}
	if got := exportedNames(t, c, "search="+url.QueryEscape("Bulk 5001")); !slices.Equal(got, []string{"Bulk 5001"}) {
		t.Errorf("narrowed: names = %q, want [Bulk 5001]", got)
	}

	h.Exec(t, `DELETE FROM customers.customers WHERE customer_number = 105001`)
	if _, rows := exportTable(t, exportCSV(t, c, "").Body); len(rows) != 5000 {
		t.Errorf("5000 customers: %d rows, want 5000", len(rows))
	}
}

// TestGetCustomersImportTemplate_IsTheImportableHeaderAlone: every column an
// import writes — the legal identity's included, whoever asks, since the
// template is the file's shape and not anybody's data — and nothing else.
func TestGetCustomersImportTemplate_IsTheImportableHeaderAlone(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	viewer := h.SignIn(t, "customers:view")

	r := viewer.Do(http.MethodGet, "/api/v1/customers/import/template", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("template: status %d body %s", r.Status, r.Body)
	}
	if got, want := string(r.Body), csvBOM+importableHeader+"\r\n"; got != want {
		t.Errorf("template is\n%q\nwant\n%q", got, want)
	}
	if got, want := r.Header("Content-Disposition"), `attachment; filename="customers-import-template.csv"`; got != want {
		t.Errorf("Content-Disposition = %q, want %q", got, want)
	}
}
```

- [ ] **Step 2: Run them and watch them fail**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'
mise exec -- go test -count=1 -run 'GetCustomersExport|GetCustomersImportTemplate' ./internal/customers/
```
Expected: FAIL — every request answers 404 (no such operation), or `GET /customers/{id}` refuses `export` as an id with a 400; the golden test prints the body it got.

- [ ] **Step 3: The two batched reads**

In `apps/server/internal/customers/queries/customers.sql`, directly after the `UpdateCustomerBillingProfile` block:

```sql
-- name: CustomerBillingProfilesForCustomers :many
-- CustomerBillingProfilesForCustomers is the billing profile of a whole export
-- in ONE query (customers import/export design D2), CustomerTagsForCustomers'
-- shape: the eleven columns GetCustomerBillingProfile selects for one customer,
-- for every id in @ids, never one read per row. Only the export reads it: no
-- response of this module carries a billing column beside the customer's own
-- (invoice-ready customer design D4's ruling), and a file is not a response.
SELECT id, invoice_email, reminder_email, payment_terms_days, currency, language,
       invoice_delivery, reminder_delivery, peppol_id, gln, buyer_reference, default_bill_rate
FROM customers.customers
WHERE id = ANY(@ids::int[]);
```

In `apps/server/internal/customers/queries/addresses.sql`, directly after the `ListCustomerAddresses` block:

```sql
-- name: PrimaryAddressesForCustomers :many
-- PrimaryAddressesForCustomers is the two addresses the customers file carries
-- (customers import/export design D1) — each customer's primary postal and
-- primary invoice address — for a whole export in ONE query. At most two rows
-- per customer: the partial unique index allows one primary per type.
SELECT id, customer_id, type, label, line1, line2, postal_code, city, region, country, is_primary, created_at, updated_at
FROM customers.customer_addresses
WHERE customer_id = ANY(@customer_ids::int[]) AND is_primary AND type IN ('postal', 'invoice')
ORDER BY customer_id, type;
```

- [ ] **Step 4: The contract**

In `openapi/customers.yaml`, `paths:` is alphabetical: insert `/api/v1/customers/export:` between `/api/v1/customers/contacts/{id}/customers:` and `/api/v1/customers/follow-ups:`, and `/api/v1/customers/import/template:` between `/api/v1/customers/groups/{groupId}:` and `/api/v1/customers/lookup/brreg:` (Task 4 puts `/api/v1/customers/import:` right above it):

```yaml
    /api/v1/customers/export:
        get:
            description: |-
                The customer list as the customers file (customers import/export design D1, D2): UTF-8 with a byte order mark, semicolon-separated, decimal comma, ISO dates (createdAt and updatedAt are UTC, RFC 3339), CRLF line ends and a header row — the expenses payroll export's form, which a Norwegian Excel opens without an import dialog. Quoting is RFC 4180's with the semicolon as the separator, and a cell beginning with '=', '+', '-', '@', a tab or a carriage return is prefixed with an apostrophe, so nothing a person typed becomes a formula in somebody's spreadsheet; the import takes that apostrophe off again.

                The columns are the API's own JSON names, in this order: customerNumber, name, type, status; legalCountry, legalType, legalId, legalName; email, phone, website; postalLine1, postalLine2, postalPostalCode, postalCity, postalRegion, postalCountry (the primary postal address); invoiceLine1, invoiceLine2, invoicePostalCode, invoiceCity, invoiceRegion, invoiceCountry (the primary invoice address); invoiceEmail, reminderEmail, paymentTermsDays, currency, language, invoiceDelivery, reminderDelivery, peppolId, gln, buyerReference, defaultBillRate (the billing profile, the customer's own values); group (its name) and tags (their names, joined by '|'); then id, ownerName, createdAt and updatedAt, which an import ignores. The four legal-identity columns are present only for a caller holding customers:legal-identity-view — absent, not blank, so a file without them re-imports without touching an identity. The billing columns are there for every caller, as the billing profile's own GET is.

                It takes the list's own filters and sort, and is deliberately not paged: more than 5000 customers is refused and asks for a narrower filter.
            operationId: getCustomersExport
            parameters:
                - in: query
                  name: sortBy
                  schema:
                    description: "'id', 'name', 'customerNumber', 'createdAt' or 'updatedAt'. Defaults to 'id'."
                    type: string
                - in: query
                  name: sortDirection
                  schema:
                    type: string
                - in: query
                  name: includeArchived
                  schema:
                    type: boolean
                - in: query
                  name: search
                  schema:
                    description: "Matches, case-insensitively, the name, customer number and, only for a caller who may see them, the legal name/id and any linked contact's name/email."
                    type: string
                - in: query
                  name: status
                  schema:
                    description: "'active', 'disabled' or 'archived', case-sensitive. Naming a status shows exactly that status, regardless of includeArchived."
                    type: string
                - in: query
                  name: type
                  schema:
                    description: "'business' or 'person', case-sensitive."
                    type: string
                - in: query
                  name: ownerId
                  schema:
                    description: "A user id, or the literal 'me' (the calling user) or 'none' (customers with no owner). Case-sensitive. 'me' is how the UI's My customers filter is expressed — the caller is resolved from the session, never sent."
                    type: string
                - in: query
                  name: tagId
                  schema:
                    description: "A tag id. A customer matches when it carries that tag. One tag only; multi-tag filtering is not built."
                    type: string
                - in: query
                  name: groupId
                  schema:
                    description: "A group id, or the literal 'none' (customers in no group). Case-sensitive. An id no group holds simply matches nothing."
                    type: string
            responses:
                "200":
                    content:
                        text/csv:
                            schema:
                                format: binary
                                type: string
                    description: OK — served as an attachment named customers-YYYY-MM-DD.csv (the day in UTC), and never cached.
                "400":
                    content:
                        application/problem+json:
                            schema:
                                $ref: common.yaml#/components/schemas/ProblemDetails
                    description: Bad Request — a parameter the list itself would refuse, in the list's words, or more than 5000 customers, which asks for a narrower filter.
                "401":
                    content:
                        application/json:
                            schema:
                                $ref: common.yaml#/components/schemas/AuthErrorResponse
                    description: Unauthorized
                "403":
                    content:
                        application/json:
                            schema:
                                $ref: common.yaml#/components/schemas/AuthErrorResponse
                    description: Forbidden
            summary: Export customers as CSV
            tags:
                - Customers
            x-vantigo-access: permission:customers:view
```

The nine parameters are `getCustomers`' own (`openapi/customers.yaml` 1598–1640), verbatim — the export takes the list's own filter — so `gen.GetCustomersExportParams` gets the same field names and types as `gen.GetCustomersParams` less `Page`/`PageSize`.

```yaml
    /api/v1/customers/import/template:
        get:
            description: The customers file's header row alone (customers import/export design D2), with every column an import writes — the export's columns less id, ownerName, createdAt and updatedAt, the legal identity's four included whoever asks, since the template is the file's shape and nobody's data. The starting point for a file made by hand.
            operationId: getCustomersImportTemplate
            responses:
                "200":
                    content:
                        text/csv:
                            schema:
                                format: binary
                                type: string
                    description: OK — served as an attachment named customers-import-template.csv.
                "401":
                    content:
                        application/json:
                            schema:
                                $ref: common.yaml#/components/schemas/AuthErrorResponse
                    description: Unauthorized
                "403":
                    content:
                        application/json:
                            schema:
                                $ref: common.yaml#/components/schemas/AuthErrorResponse
                    description: Forbidden
            summary: Download the customers import template
            tags:
                - Customers
            x-vantigo-access: permission:customers:view
```

- [ ] **Step 5: Generate**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- go generate ./... && mise exec -- go generate ./... && git -C ../.. status --short
grep -n "GetCustomersExportParams struct\|VisitGetCustomersExportResponse\|VisitGetCustomersImportTemplateResponse\|func (q \*Queries) CustomerBillingProfilesForCustomers\|func (q \*Queries) PrimaryAddressesForCustomers" internal/customers/gen/api.gen.go internal/customers/store/*.go
```
Expected: `gen.GetCustomersExportParams` with `SortBy, SortDirection *string; IncludeArchived *bool; Search, Status, Type, OwnerId, TagId, GroupId *string`; both response interfaces; `CustomerBillingProfilesForCustomers(ctx, ids []int32) ([]CustomerBillingProfilesForCustomersRow, error)` and `PrimaryAddressesForCustomers(ctx, customerIds []int32) ([]CustomersCustomerAddress, error)`. The build is red until Step 7 — the server does not implement the two operations yet. If sqlc named anything differently, use what it printed.

- [ ] **Step 6: The list's filter, extracted**

In `apps/server/internal/customers/customers.go`, directly above `// GetCustomers List all customers`, add:

```go
// customerListFilter is GET /customers' query parameters resolved into what
// CountCustomers and ListCustomers take: the filters, the search with its three
// permission-gated reaches, and the sort. GetCustomers and GetCustomersExport
// (csvexport.go) both build one through customerListFilterFor, so the file a
// person downloads is exactly the list they are looking at — the same filters,
// the same search reach, the same order (customers import/export design D2).
type customerListFilter struct {
	includeArchived       bool
	status, customerType  *string
	search, searchCompact *string
	searchPhone           bool
	// searchIdentity is legalIdentityView: it decides both whether search may
	// reach the legal identity and whether a response (or a file) shows it.
	searchIdentity bool
	searchContacts bool
	ownerNone      bool
	ownerID        *uuid.UUID
	tagID          *uuid.UUID
	groupNone      bool
	groupID        *uuid.UUID
	sortBy         string
	descending     bool
}

// customerListFilterFor resolves p, or answers the one 400 detail the list
// gives for it (every message validateGetCustomersParams collects, joined with
// a space, or the 'me'-without-a-session refusal).
func (s *server) customerListFilterFor(ctx context.Context, p gen.GetCustomersParams) (customerListFilter, string) {
	if msgs := validateGetCustomersParams(p); len(msgs) > 0 {
		return customerListFilter{}, strings.Join(msgs, " ")
	}
	f := customerListFilter{
		includeArchived: p.IncludeArchived != nil && *p.IncludeArchived,
		status:          p.Status,
		customerType:    p.Type,
		sortBy:          "id",
		descending:      p.SortDirection != nil && *p.SortDirection == "desc",
	}
	if p.SortBy != nil {
		f.sortBy = *p.SortBy
	}
	if p.Search != nil {
		if trimmed := strings.TrimSpace(*p.Search); trimmed != "" {
			search := likePattern(trimmed)
			f.search = &search
			// search_compact matches the customer number and legal id the
			// way a person actually types them: "923 609 016" finds a legal
			// id stored, with no spaces, as "923609016" (customers
			// foundation design D4).
			compact := stripWhitespace(trimmed)
			searchCompact := likePattern(compact)
			f.searchCompact = &searchCompact
			// search_phone gates the phone branch on the same compact term
			// (final review fix M2): searchPhoneEligible above.
			f.searchPhone = searchPhoneEligible(compact)
		}
	}

	// ownerId's three forms resolve to the two SQL parameters the list queries
	// take (owner and tags design D1): 'none' is a NULL test, and both a user
	// id and 'me' are an equality — 'me' resolved from the SESSION, never from
	// anything the request says about who the caller is. The router already
	// refused an unauthenticated call (design D4), so the missing-principal
	// branch below is unreachable in production; it is a 400 rather than a
	// silent "everyone's customers", because answering the wrong customers is
	// the one outcome a "my customers" filter must never have.
	if p.OwnerId != nil {
		switch *p.OwnerId {
		case "none":
			f.ownerNone = true
		case "me":
			principal, ok := contracts.PrincipalFrom(ctx)
			if !ok || principal.UserID == uuid.Nil {
				return customerListFilter{}, "'ownerId' cannot be 'me' without a signed-in user."
			}
			id := principal.UserID
			f.ownerID = &id
		default:
			// validateGetCustomersParams already refused anything unparseable,
			// so err is impossible here; the guard means an impossible value
			// filters nothing rather than panicking.
			if id, err := uuid.Parse(*p.OwnerId); err == nil {
				f.ownerID = &id
			}
		}
	}
	if p.TagId != nil {
		if id, err := uuid.Parse(*p.TagId); err == nil {
			f.tagID = &id
		}
	}
	// groupId's two forms resolve to the two SQL parameters the list queries
	// take (design D3). No 'me' here and nothing session-dependent: a group is a
	// bucket the installation defines, not a relationship to the caller.
	if p.GroupId != nil {
		if *p.GroupId == "none" {
			f.groupNone = true
		} else if id, err := uuid.Parse(*p.GroupId); err == nil {
			f.groupID = &id
		}
	}

	// search_identity/search_contacts gate the legal-identity and
	// contact/association branches of search: a caller who cannot see that
	// data through its own endpoints must not be able to use search as an
	// oracle for it either (customers foundation design D4). Computed once
	// here and reused for both queries, so the count and the page never
	// disagree about what search reaches.
	//
	// Each answer costs an access check — a session lookup plus a permission
	// query — so the list asks for as few as it needs: searchIdentity
	// unconditionally, because legalIdentityView also decides whether the
	// identity is shown; searchContacts only when there is a search term at
	// all, since with no term the contact/association branch is unreachable.
	// The two contact permissions travel as one AND-gated check
	// (hasPermissions, server.go) rather than two.
	f.searchIdentity = s.hasPermission(ctx, legalIdentityView)
	f.searchContacts = f.search != nil && s.hasPermissions(ctx, contactsView, associationsView)
	return f, ""
}

// countParams is f as CountCustomers takes it.
func (f customerListFilter) countParams() store.CountCustomersParams {
	return store.CountCustomersParams{
		IncludeArchived: f.includeArchived, Status: f.status, CustomerType: f.customerType,
		Search: f.search, SearchCompact: f.searchCompact, SearchIdentity: f.searchIdentity, SearchContacts: f.searchContacts,
		SearchPhone: f.searchPhone,
		OwnerNone:   f.ownerNone, OwnerID: f.ownerID, TagID: f.tagID,
		GroupNone: f.groupNone, GroupID: f.groupID,
	}
}

// listParams is f as ListCustomers takes it, for one page.
func (f customerListFilter) listParams(pageSize, offset int32) store.ListCustomersParams {
	return store.ListCustomersParams{
		IncludeArchived: f.includeArchived, Status: f.status, CustomerType: f.customerType,
		Search: f.search, SearchCompact: f.searchCompact, SearchIdentity: f.searchIdentity, SearchContacts: f.searchContacts,
		SearchPhone: f.searchPhone,
		OwnerNone:   f.ownerNone, OwnerID: f.ownerID, TagID: f.tagID,
		GroupNone: f.groupNone, GroupID: f.groupID,
		SortBy: f.sortBy, Descending: f.descending, PageSize: pageSize, RowOffset: offset,
	}
}
```

The comments above are GetCustomers' own, moved with their code (cut them from `GetCustomers` in the same edit). Then replace `GetCustomers` itself (doc comment kept) with:

```go
func (s *server) GetCustomers(ctx context.Context, req gen.GetCustomersRequestObject) (gen.GetCustomersResponseObject, error) {
	filter, detail := s.customerListFilterFor(ctx, req.Params)
	if detail != "" {
		return gen.GetCustomers400ApplicationProblemPlusJSONResponse(apicommon.Problem("Invalid query parameters", detail)), nil
	}

	page := int32(1)
	if req.Params.Page != nil {
		page = *req.Params.Page
	}
	pageSize := int32(25)
	if req.Params.PageSize != nil {
		pageSize = *req.Params.PageSize
	}

	q := store.New(s.deps.Pool)
	total, err := q.CountCustomers(ctx, filter.countParams())
	if err != nil {
		return nil, fmt.Errorf("customers: count customers: %w", err)
	}
	list, err := q.ListCustomers(ctx, filter.listParams(pageSize, (page-1)*pageSize))
	if err != nil {
		return nil, fmt.Errorf("customers: list customers: %w", err)
	}
	rows := make([]customerRow, 0, len(list))
	for _, r := range list {
		rows = append(rows, fromListRow(r))
	}

	// One directory call and one tags query for the whole page (owner and tags
	// design D1, D2), never one per row: 25 rows would otherwise be 25
	// out-of-process calls for data that is on the wire either way.
	dec, err := s.decorate(ctx, q, rows...)
	if err != nil {
		return nil, err
	}
	// searchIdentity doubles as includeIdentity here: legalIdentityView
	// answers both "may search reach the legal identity" and "may the
	// response show it", so one hasPermission call serves both.
	data := make([]gen.SafeCustomerResponse, 0, len(rows))
	for _, r := range rows {
		data = append(data, safeCustomerResponse(r, filter.searchIdentity, dec))
	}

	return gen.GetCustomers200JSONResponse{
		Data:       data,
		Pagination: apicommon.Pagination(page, pageSize, int32(total)),
	}, nil
}
```

The list's behaviour is unchanged: `customers_list_test.go`, `customers_test.go` and `owner_test.go`'s filter tests are its proof in Step 8.

- [ ] **Step 7: The two handlers**

Create `apps/server/internal/customers/csvexport.go`:

```go
package customers

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
	"github.com/vantigo-io/vantigo/server/internal/customers/store"
)

// This file is GET /customers/export and GET /customers/import/template
// (customers import/export design D1, D2): the customers file (csvfile.go) on
// its way out. The export is the list a person is looking at — its filters,
// its sort, and its permission shape: the legal identity's four columns are
// absent for a caller without customers:legal-identity-view, as the identity
// is absent from their list rows. Every value a row needs beyond the list's own
// columns is read in bulk before a byte is written — one decoration (owners,
// tags, groups), one billing read, one address read — so the file costs a fixed
// number of reads however many rows it has.

// tooManyCustomersToExportTitle is the cap's 400 (design D2), the expenses
// payroll file's refusal in this module's words: a bare problem, no errors
// object, asking for a narrower filter.
const tooManyCustomersToExportTitle = "Too many customers to export"

func tooManyCustomersToExport() apicommon.ProblemDetails {
	return apicommon.Problem(tooManyCustomersToExportTitle, fmt.Sprintf(
		"This export would hold more than %d customers, which is more than one file should. Narrow it with a filter or a search, and export the rest in slices.",
		customersFileMaxRows))
}

// csvDownload writes a customers file: the generated response types hard-code
// a bare text/csv and name no file, and this answer has to say which encoding
// it is in, what to call it and that nobody may cache it — the payroll
// export's csvDownload, answering both of this module's file operations.
type csvDownload struct {
	body     []byte
	fileName string
}

func (d csvDownload) write(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", d.fileName))
	// A customer file names who the business deals with and on what terms. It
	// belongs in nobody's cache, and least of all in a shared one.
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Length", strconv.Itoa(len(d.body)))
	w.WriteHeader(http.StatusOK)
	_, err := w.Write(d.body)
	return err
}

func (d csvDownload) VisitGetCustomersExportResponse(w http.ResponseWriter) error {
	return d.write(w)
}

func (d csvDownload) VisitGetCustomersImportTemplateResponse(w http.ResponseWriter) error {
	return d.write(w)
}

// exportColumns is the export's header for a caller: every column, less the
// legal identity's four without legal-identity-view — absent, not blank, so
// the file re-imports without touching an identity (design D2).
func exportColumns(includeIdentity bool) []csvColumn {
	out := make([]csvColumn, 0, len(customerCSVColumns))
	for _, c := range customerCSVColumns {
		if c.Group == csvGroupIdentity && !includeIdentity {
			continue
		}
		out = append(out, c)
	}
	return out
}

// importTemplateColumns is every column an import writes: the table less the
// export-only four.
func importTemplateColumns() []csvColumn {
	out := make([]csvColumn, 0, len(customerCSVColumns))
	for _, c := range customerCSVColumns {
		if c.Group != csvGroupExportOnly {
			out = append(out, c)
		}
	}
	return out
}

// GetCustomersExport Export customers as CSV
// (GET /api/v1/customers/export)
func (s *server) GetCustomersExport(ctx context.Context, req gen.GetCustomersExportRequestObject) (gen.GetCustomersExportResponseObject, error) {
	p := req.Params
	filter, detail := s.customerListFilterFor(ctx, gen.GetCustomersParams{
		SortBy: p.SortBy, SortDirection: p.SortDirection, IncludeArchived: p.IncludeArchived,
		Search: p.Search, Status: p.Status, Type: p.Type, OwnerId: p.OwnerId, TagId: p.TagId, GroupId: p.GroupId,
	})
	if detail != "" {
		return gen.GetCustomersExport400ApplicationProblemPlusJSONResponse(apicommon.Problem("Invalid query parameters", detail)), nil
	}

	q := store.New(s.deps.Pool)
	// One row more than the cap, so "the whole file" and "more than a file may
	// hold" are told apart without a count of their own.
	list, err := q.ListCustomers(ctx, filter.listParams(customersFileMaxRows+1, 0))
	if err != nil {
		return nil, fmt.Errorf("customers: list customers for export: %w", err)
	}
	if len(list) > customersFileMaxRows {
		return gen.GetCustomersExport400ApplicationProblemPlusJSONResponse(tooManyCustomersToExport()), nil
	}
	rows := make([]customerRow, 0, len(list))
	ids := make([]int32, 0, len(list))
	for _, r := range list {
		rows = append(rows, fromListRow(r))
		ids = append(ids, r.ID)
	}

	dec, err := s.decorate(ctx, q, rows...)
	if err != nil {
		return nil, err
	}
	profiles := make(map[int32]billingProfile, len(rows))
	addresses := make(map[int32]map[string]store.CustomersCustomerAddress, len(rows))
	if len(ids) > 0 {
		billing, err := q.CustomerBillingProfilesForCustomers(ctx, ids)
		if err != nil {
			return nil, fmt.Errorf("customers: read billing profiles for export: %w", err)
		}
		for _, b := range billing {
			rate, err := floatPtrFromNumeric(b.DefaultBillRate)
			if err != nil {
				return nil, err
			}
			profiles[b.ID] = billingProfileFromRow(b.InvoiceEmail, b.ReminderEmail, b.PaymentTermsDays, b.Currency, b.Language,
				b.InvoiceDelivery, b.ReminderDelivery, b.PeppolID, b.Gln, b.BuyerReference, rate)
		}
		primaries, err := q.PrimaryAddressesForCustomers(ctx, ids)
		if err != nil {
			return nil, fmt.Errorf("customers: read addresses for export: %w", err)
		}
		for _, a := range primaries {
			if addresses[a.CustomerID] == nil {
				addresses[a.CustomerID] = map[string]store.CustomersCustomerAddress{}
			}
			addresses[a.CustomerID][a.Type] = a
		}
	}

	return csvDownload{
		body:     customersCSV(exportColumns(filter.searchIdentity), rows, dec, profiles, addresses),
		fileName: fmt.Sprintf("customers-%s.csv", s.deps.Clock().UTC().Format(time.DateOnly)),
	}, nil
}

// GetCustomersImportTemplate Download the customers import template
// (GET /api/v1/customers/import/template)
func (s *server) GetCustomersImportTemplate(_ context.Context, _ gen.GetCustomersImportTemplateRequestObject) (gen.GetCustomersImportTemplateResponseObject, error) {
	var b bytes.Buffer
	b.WriteString(csvByteOrderMark)
	writeCSVRow(&b, csvColumnNames(importTemplateColumns()))
	return csvDownload{body: b.Bytes(), fileName: "customers-import-template.csv"}, nil
}

// customersCSV is the file: the byte order mark, the header, then one row per
// customer with its cells in columns' order.
func customersCSV(columns []csvColumn, rows []customerRow, dec customerDecoration, profiles map[int32]billingProfile, addresses map[int32]map[string]store.CustomersCustomerAddress) []byte {
	var b bytes.Buffer
	b.WriteString(csvByteOrderMark)
	writeCSVRow(&b, csvColumnNames(columns))
	cells := make([]string, len(columns))
	for _, row := range rows {
		values := exportValues(row, dec, profiles[row.ID], addresses[row.ID])
		for i, c := range columns {
			cells[i] = values[c.Name]
		}
		writeCSVRow(&b, cells)
	}
	return b.Bytes()
}

// exportValues is one customer's cells by column name. A value there is none
// of is the empty cell, never a dash or a zero; money carries the decimal
// comma; the two instants are UTC, RFC 3339.
func exportValues(row customerRow, dec customerDecoration, profile billingProfile, addresses map[string]store.CustomersCustomerAddress) map[string]string {
	values := map[string]string{
		"customerNumber": strconv.FormatInt(row.CustomerNumber, 10),
		"name":           row.Name,
		"type":           row.Type,
		"status":         row.Status,
		"legalCountry":   deref(row.LegalCountry),
		"legalType":      deref(row.LegalType),
		"legalId":        deref(row.LegalID),
		"legalName":      deref(row.LegalName),
		"email":          deref(row.Email),
		"phone":          deref(row.Phone),
		"website":        deref(row.Website),
		"invoiceEmail":   deref(profile.InvoiceEmail),
		"reminderEmail":  deref(profile.ReminderEmail),
		"currency":       deref(profile.Currency),
		"language":       deref(profile.Language),
		"invoiceDelivery": deref(profile.InvoiceDelivery),
		"reminderDelivery": deref(profile.ReminderDelivery),
		"peppolId":       deref(profile.PeppolID),
		"gln":            deref(profile.Gln),
		"buyerReference": deref(profile.BuyerReference),
		"defaultBillRate": formatCSVDecimal(profile.DefaultBillRate),
		"id":             strconv.FormatInt(int64(row.ID), 10),
		"createdAt":      row.CreatedAt.UTC().Format(time.RFC3339),
		"updatedAt":      row.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if profile.PaymentTermsDays != nil {
		values["paymentTermsDays"] = strconv.Itoa(int(*profile.PaymentTermsDays))
	}
	if group := dec.group(row.ID); group != nil {
		values["group"] = group.Name
	}
	tags := dec.tagsFor(row.ID)
	names := make([]string, 0, len(tags))
	for _, tag := range tags {
		names = append(names, tag.Name)
	}
	values["tags"] = strings.Join(names, csvTagSeparator)
	if owner := dec.owner(row.OwnerUserID); owner != nil {
		values["ownerName"] = owner.DisplayName
	}
	for _, prefix := range []string{"postal", "invoice"} {
		address, ok := addresses[prefix]
		if !ok {
			continue
		}
		values[prefix+"Line1"] = address.Line1
		values[prefix+"Line2"] = deref(address.Line2)
		values[prefix+"PostalCode"] = deref(address.PostalCode)
		values[prefix+"City"] = deref(address.City)
		values[prefix+"Region"] = deref(address.Region)
		values[prefix+"Country"] = address.Country
	}
	return values
}
```

(`gofmt` aligns the map literal.) The address map is keyed by the address type, which is also each group's column prefix — `postal`/`invoice` — so one loop fills both.

- [ ] **Step 8: Run, regenerate the client and the coverage, show the tests can fail, commit**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'
mise exec -- gofmt -w internal/customers/csvexport.go internal/customers/customers.go internal/customers/csvexport_test.go && mise exec -- gofmt -l internal
mise exec -- go vet ./internal/customers/... && mise exec -- go test -count=1 ./internal/customers/ ./internal/openapi/...
mise exec -- go run ./internal/openapi/cmd/contract coverage -corpus ../../openapi/testdata/exchanges -out ../../openapi/COVERAGE.md
cd /home/anders/projects/vantigo/vantigo && mise exec -- bun run gen:client && git status --short
```
Expected: PASS, the whole customers package included (the list's own tests prove the extraction); `TestServeMuxConflictsArePinned` needs no new pin — `/customers/export` is a literal against `/customers/{id}` at equal depth, and `/customers/import/template` overlaps no two-segment pattern (the `/customers/groups` precedent); if it does report a pair, add it to `KnownServeMuxConflicts` and to this commit. `COVERAGE.md` gains the two operations under "uncovered, review by hand"; `gen:client` changes `apps/customers/frontend/src/api-schema.d.ts`.

Prove each new test can fail, restoring after each: delete `exportColumns`' `if c.Group == csvGroupIdentity && !includeIdentity { continue }` — the permission test goes red on `legalCountry`; change `customersFileMaxRows+1` to `customersFileMaxRows` in `GetCustomersExport` — the 5001 case goes red (it answers 200 with 5000 rows); in `customerListFilterFor` drop `sortBy`'s assignment — the `sortBy=name&sortDirection=desc` case goes red; delete the `values["tags"] = …` line — the golden test goes red on `Key|VIP`; remove `.UTC()` from the filename — red only if the harness clock is not UTC (say which it is). Say what each printed.

```bash
cd /home/anders/projects/vantigo/vantigo
cat > /tmp/claude-1000/msg-import-export-2.txt <<'EOF'
feat(customers): the customer list downloads as one CSV file, with a template to import from

GET /customers/export answers the list a person is looking at as the
customers file: the list's own filters and sort, resolved by the very
function the list now uses, capped at 5000 customers (more is a 400
asking for a narrower filter), and shaped by permission — the legal
identity's four columns are absent, not blank, without
customers:legal-identity-view. Owners, tags, groups, billing profiles and
the primary postal and invoice addresses are read in bulk, never per row.
GET /customers/import/template answers the header an import writes.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
PATHS="apps/server/internal/customers/csvexport.go apps/server/internal/customers/csvexport_test.go \
 apps/server/internal/customers/customers.go apps/server/internal/customers/queries/customers.sql \
 apps/server/internal/customers/queries/addresses.sql apps/server/internal/customers/store/customers.sql.go \
 apps/server/internal/customers/store/addresses.sql.go openapi/customers.yaml \
 apps/server/internal/openapi/specs/customers.yaml apps/server/internal/customers/gen/api.gen.go \
 apps/customers/frontend/src/api-schema.d.ts openapi/COVERAGE.md"
git add $PATHS && git commit -F /tmp/claude-1000/msg-import-export-2.txt -- $PATHS
git show --stat HEAD && git status --short
```
If `go generate` touched a generated file outside `PATHS`, look at why before adding it; if `openapi/COVERAGE.md` did not move, drop it from `PATHS` and say so.

---

### Task 3: One write path per group, shared (D3's seam)

Every write the importer will make, lifted out of its handler into a function the handler now calls — behaviour unchanged, no contract change, no new test: the handlers' own tests are the proof, and Task 4 is each function's second caller. Every function takes the transaction's `*store.Queries` and an already-resolved `actor`; none of them reads the pool, checks access or calls the directory.

**Files:**
- Modify: `apps/server/internal/customers/customers.go`, `legal_identity.go`, `contact_info.go`, `billing_profile.go`, `addresses.go`, `group_membership.go`, `tags.go`
- Read first (do not change): each file's handler as it stands; `apps/server/internal/customers/duplicates.go` (`errDuplicateIdentity`)

**Interfaces:**
- Produces Go (all package-private):
  - `type newCustomer struct{Name, Status, Type string; Identity *legalIdentity; Contact contactInfo}`; `(*server).insertNewCustomer(ctx, txq *store.Queries, c newCustomer, duplicateCheck, nameHolders bool, now time.Time, act actor) (store.InsertCustomerRow, *gen.CustomerConflictProblem, error)`
  - `type customerCore struct{Name, Status string; Identity *legalIdentity}`; `(*server).writeCustomerCore(ctx, txq, id int32, customerType string, before, after customerCore, expectedRevision *int32, duplicateCheck, nameHolders bool, now time.Time, act actor) (store.UpdateCustomerRow, *gen.CustomerConflictProblem, error)`
  - `writeContactInfo(ctx, txq, id int32, before, after contactInfo, expectedRevision *int32, now time.Time, act actor) (store.UpdateCustomerContactInfoRow, error)`
  - `writeBillingProfile(ctx, txq, id int32, before, after billingProfile, expectedRevision *int32, now time.Time, act actor) (store.UpdateCustomerBillingProfileRow, error)`
  - `addressCapMessage`; `insertAddress(ctx, txq, customerID int32, parsed validatedAddress, requestedPrimary bool, now time.Time, act actor) (store.CustomersCustomerAddress, error)` (may return `errAddressCapReached`); `replaceAddress(ctx, txq, customerID int32, existing store.CustomersCustomerAddress, parsed validatedAddress, requestedPrimary bool, now time.Time, act actor) (store.CustomersCustomerAddress, error)` (may return `errPrimaryTransitionRefused`); `removeAddress(ctx, txq, customerID int32, existing store.CustomersCustomerAddress, now time.Time, act actor) error` — each assumes the caller holds `LockCustomer`
  - `writeCustomerGroup(ctx, txq, id int32, before, after *groupSnapshot, expectedRevision *int32, now time.Time, act actor) (store.SetCustomerGroupRow, error)`
  - `replaceCustomerTags(ctx, txq, id int32, wanted []uuid.UUID, added, removed []tagSnapshot, now time.Time, act actor) error` — assumes the caller holds `LockCustomer`
- Wire: none.
- Consumes: nothing new.

- [ ] **Step 1: The create**

In `customers.go`, directly above `// PostCustomers Create a new customer`:

```go
// newCustomer is what PostCustomers inserts, already validated: the create
// endpoint and the CSV importer (import.go) build one from their own input and
// insert it through insertNewCustomer, so a customer created by file is the
// create endpoint's customer, number and event included.
type newCustomer struct {
	Name     string
	Status   string
	Type     string
	Identity *legalIdentity
	Contact  contactInfo
}

// insertNewCustomer is PostCustomers' transaction body: the duplicate check
// (customers foundation design D6) first when duplicateCheck says so, and
// before NextCounterValue, so a refused create burns no number; then the row
// and customer.created. A conflict returns errDuplicateIdentity with the body
// to answer it with — the transaction is rolled back by the time the caller
// sees the error, so the body travels beside it. nameHolders and act were
// resolved before the transaction opened (duplicates.go, actor.go).
func (s *server) insertNewCustomer(ctx context.Context, txq *store.Queries, c newCustomer, duplicateCheck, nameHolders bool, now time.Time, act actor) (store.InsertCustomerRow, *gen.CustomerConflictProblem, error) {
	if duplicateCheck && c.Identity != nil {
		problem, err := s.duplicateIdentityProblem(ctx, txq, *c.Identity, 0, nameHolders)
		if err != nil {
			return store.InsertCustomerRow{}, nil, err
		}
		if problem != nil {
			return store.InsertCustomerRow{}, problem, errDuplicateIdentity
		}
	}
	number, err := txq.NextCounterValue(ctx, "customer-number")
	if err != nil {
		return store.InsertCustomerRow{}, nil, err
	}
	legalCountry, legalID, legalName, legalSource, legalType := legalColumns(c.Identity)
	created, err := txq.InsertCustomer(ctx, store.InsertCustomerParams{
		CustomerNumber: number, Name: c.Name, Status: c.Status, Type: c.Type,
		LegalCountry: legalCountry, LegalID: legalID, LegalName: legalName, LegalSource: legalSource, LegalType: legalType,
		Now: now, Email: c.Contact.Email, Phone: c.Contact.Phone, Website: c.Contact.Website,
	})
	if err != nil {
		return store.InsertCustomerRow{}, nil, err
	}
	return created, nil, recordCustomerCreated(ctx, txq, now, created.ID, c.Name, c.Identity, act.Kind, act.Display, act.UserID)
}
```

In `PostCustomers`, delete the line `legalCountry, legalID, legalName, legalSource, legalType := legalColumns(identity)` and replace the `err = db.WithTx(…)` statement (the closure through `})`) with:

```go
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var err error
		created, conflict, err = s.insertNewCustomer(ctx, store.New(tx),
			newCustomer{Name: name, Status: status, Type: customerType, Identity: identity, Contact: contact},
			needsDuplicateCheck, nameHolders, now, act)
		return err
	})
```

The comment the closure carried about the duplicate check running first moves to `insertNewCustomer`'s doc comment above (already there); the `errors.Is(err, errDuplicateIdentity)` branch after it stays as it is.

- [ ] **Step 2: Name, status and identity**

In `customers.go`, directly above `// PutCustomersById Update a customer`:

```go
// customerCore is what PUT /customers/{id} writes — the name, the status and
// the legal identity — as one value, before and after.
type customerCore struct {
	Name     string
	Status   string
	Identity *legalIdentity
}

// writeCustomerCore is the transaction body PutCustomersById and
// PutCustomersByIdLegalIdentity share, and the one the CSV importer
// (import.go) writes a row's name, status and identity through: the duplicate
// check when duplicateCheck says so (a conflict returns errDuplicateIdentity
// and its body, before anything is written); the UPDATE, guarded when
// expectedRevision is set (pgx.ErrNoRows then means a stale revision); the
// registry record's invalidation when the identity moved (fix round 2, C2 —
// under the row lock the UPDATE holds); and the events — customer.updated for
// a name or identity change, customer.status_changed for a status change. The
// caller has already decided the write is not a no-op and resolved act and
// nameHolders before the transaction opened.
func (s *server) writeCustomerCore(ctx context.Context, txq *store.Queries, id int32, customerType string, before, after customerCore, expectedRevision *int32, duplicateCheck, nameHolders bool, now time.Time, act actor) (store.UpdateCustomerRow, *gen.CustomerConflictProblem, error) {
	if duplicateCheck && after.Identity != nil {
		problem, err := s.duplicateIdentityProblem(ctx, txq, *after.Identity, id, nameHolders)
		if err != nil {
			return store.UpdateCustomerRow{}, nil, err
		}
		if problem != nil {
			return store.UpdateCustomerRow{}, problem, errDuplicateIdentity
		}
	}
	legalCountry, legalID, legalName, legalSource, legalType := legalColumns(after.Identity)
	updated, err := txq.UpdateCustomer(ctx, store.UpdateCustomerParams{
		ID: id, Name: after.Name, Status: after.Status,
		LegalCountry: legalCountry, LegalID: legalID, LegalName: legalName, LegalSource: legalSource, LegalType: legalType,
		UpdatedAt: now, ExpectedRevision: expectedRevision,
	})
	if err != nil {
		return store.UpdateCustomerRow{}, nil, err
	}
	identityChanged := !identityEqual(before.Identity, after.Identity)
	if identityChanged {
		if err := invalidateRegistryRecord(ctx, txq, id, after.Identity, customerType); err != nil {
			return store.UpdateCustomerRow{}, nil, err
		}
	}
	if before.Name != after.Name || identityChanged {
		if err := recordCustomerUpdated(ctx, txq, now, id, before.Name, before.Identity, after.Name, after.Identity, act.Kind, act.Display, act.UserID); err != nil {
			return store.UpdateCustomerRow{}, nil, err
		}
	}
	if before.Status != after.Status {
		if err := recordCustomerStatusChanged(ctx, txq, now, id, before.Status, after.Status, act.Kind, act.Display, act.UserID); err != nil {
			return store.UpdateCustomerRow{}, nil, err
		}
	}
	return updated, nil, nil
}
```

In `PutCustomersById`, delete `legalCountry, legalID, legalName, legalSource, legalType := legalColumns(afterIdentity)` and replace the `err = db.WithTx(…)` statement with:

```go
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var err error
		updated, conflict, err = s.writeCustomerCore(ctx, store.New(tx), req.Id, existing.Type,
			customerCore{Name: existing.Name, Status: existing.Status, Identity: beforeIdentity},
			customerCore{Name: name, Status: finalStatus, Identity: afterIdentity},
			body.Revision, needsDuplicateCheck, nameHolders, now, act)
		return err
	})
```

The handler keeps `identityChanged`, `changed` and `statusChanged` for its no-op branch and its after-commit registry fetch; the switch after the transaction is unchanged.

In `legal_identity.go`'s `PutCustomersByIdLegalIdentity`, delete `legalCountry, legalID, legalName, legalSource, legalType := legalColumns(after)` and replace the `err = db.WithTx(…)` statement with:

```go
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		// ExpectedRevision is nil: the legal-identity sub-resource stays an
		// unconditional write (customers foundation design D5), and name and
		// status are carried over unchanged, so customer.updated is the one
		// event. The duplicate check, the invalidation and the event are
		// writeCustomerCore's, in the order this handler has always run them.
		var err error
		_, conflict, err = s.writeCustomerCore(ctx, store.New(tx), req.Id, existing.Type,
			customerCore{Name: existing.Name, Status: existing.Status, Identity: before},
			customerCore{Name: existing.Name, Status: existing.Status, Identity: after},
			nil, needsDuplicateCheck, nameHolders, now, act)
		return err
	})
```

`DeleteCustomersByIdLegalIdentity` stays as it is: it is not an import path (the importer removes an identity through `writeCustomerCore` with a nil `after.Identity`, the very statements the DELETE runs).

- [ ] **Step 3: Contact info and the billing profile**

In `contact_info.go`, directly above `// PutCustomersByIdContactInfo`:

```go
// writeContactInfo is PutCustomersByIdContactInfo's transaction body — the
// guarded UPDATE (pgx.ErrNoRows on a stale expectedRevision) and
// customer.contact_info_updated — and the one the CSV importer writes a row's
// contact info through. The caller has decided before and after differ.
func writeContactInfo(ctx context.Context, txq *store.Queries, id int32, before, after contactInfo, expectedRevision *int32, now time.Time, act actor) (store.UpdateCustomerContactInfoRow, error) {
	updated, err := txq.UpdateCustomerContactInfo(ctx, store.UpdateCustomerContactInfoParams{
		ID: id, Email: after.Email, Phone: after.Phone, Website: after.Website,
		UpdatedAt: now, ExpectedRevision: expectedRevision,
	})
	if err != nil {
		return store.UpdateCustomerContactInfoRow{}, err
	}
	return updated, recordCustomerContactInfoUpdated(ctx, txq, now, id, before, after, act.Kind, act.Display, act.UserID)
}
```

Add `"time"` to its imports, and replace the handler's `err = db.WithTx(…)` statement with:

```go
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var err error
		updated, err = writeContactInfo(ctx, store.New(tx), req.Id, before, after, body.Revision, now, act)
		return err
	})
```

In `billing_profile.go`, directly above `// PutCustomersByIdBillingProfile`:

```go
// writeBillingProfile is PutCustomersByIdBillingProfile's transaction body —
// the rate's numeric column value, the guarded full-replace UPDATE
// (pgx.ErrNoRows on a stale expectedRevision) and
// customer.billing_profile_updated — and the one the CSV importer writes a
// row's billing profile through. The caller has decided before and after
// differ. The rate's conversion fails only for a value JSON cannot carry, and
// is an error, never a NULL.
func writeBillingProfile(ctx context.Context, txq *store.Queries, id int32, before, after billingProfile, expectedRevision *int32, now time.Time, act actor) (store.UpdateCustomerBillingProfileRow, error) {
	defaultBillRate, err := numericFromFloatPtr(after.DefaultBillRate)
	if err != nil {
		return store.UpdateCustomerBillingProfileRow{}, err
	}
	updated, err := txq.UpdateCustomerBillingProfile(ctx, store.UpdateCustomerBillingProfileParams{
		ID: id, InvoiceEmail: after.InvoiceEmail, ReminderEmail: after.ReminderEmail,
		PaymentTermsDays: after.PaymentTermsDays, Currency: after.Currency, Language: after.Language,
		InvoiceDelivery: after.InvoiceDelivery, ReminderDelivery: after.ReminderDelivery,
		PeppolID: after.PeppolID, Gln: after.Gln, BuyerReference: after.BuyerReference, DefaultBillRate: defaultBillRate,
		UpdatedAt: now, ExpectedRevision: expectedRevision,
	})
	if err != nil {
		return store.UpdateCustomerBillingProfileRow{}, err
	}
	return updated, recordCustomerBillingProfileUpdated(ctx, txq, now, id, before, after, act.Kind, act.Display, act.UserID)
}
```

Add `"time"` to its imports. In the handler, delete the block from `// The rate's column value, built before the transaction` through its `if err != nil { return nil, err }` (the one reorder in this task: the conversion now runs after the actor lookup, inside the transaction — only a value JSON cannot carry fails it, and it is still a 500 either way; the commit message says so), and replace the `err = db.WithTx(…)` statement with:

```go
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var err error
		updated, err = writeBillingProfile(ctx, store.New(tx), req.Id, before, after, body.Revision, now, act)
		return err
	})
```

- [ ] **Step 4: Addresses**

In `addresses.go`, directly after `var errPrimaryTransitionRefused = …`:

```go
// addressCapMessage is D3's cap in words: the address POST's 400 on
// "addresses", and the CSV importer's row error when a row would add a 51st.
const addressCapMessage = "A customer can have at most 50 addresses"

// insertAddress is PostCustomersByIdAddresses's transaction body once the
// customer lock is held: the 50-address cap (errAddressCapReached), the
// primary bookkeeping — the first address of its type is primary whatever
// requestedPrimary says, and a primary that replaces another demotes it first
// — the insert and customer.address_added. The CSV importer adds a primary
// postal or invoice address through it.
func insertAddress(ctx context.Context, txq *store.Queries, customerID int32, parsed validatedAddress, requestedPrimary bool, now time.Time, act actor) (store.CustomersCustomerAddress, error) {
	total, err := txq.CountCustomerAddresses(ctx, customerID)
	if err != nil {
		return store.CustomersCustomerAddress{}, err
	}
	if total >= maxCustomerAddresses {
		return store.CustomersCustomerAddress{}, errAddressCapReached
	}
	countOfType, err := txq.CountCustomerAddressesOfType(ctx, store.CountCustomerAddressesOfTypeParams{CustomerID: customerID, Type: parsed.Type, ExcludeID: 0})
	if err != nil {
		return store.CustomersCustomerAddress{}, err
	}
	isPrimary := requestedPrimary
	if countOfType == 0 {
		isPrimary = true
	} else if requestedPrimary {
		if err := demoteCurrentPrimary(ctx, txq, customerID, parsed.Type, now); err != nil {
			return store.CustomersCustomerAddress{}, err
		}
	}
	created, err := txq.InsertCustomerAddress(ctx, store.InsertCustomerAddressParams{
		CustomerID: customerID, Type: parsed.Type, Label: parsed.Label, Line1: parsed.Line1, Line2: parsed.Line2,
		PostalCode: parsed.PostalCode, City: parsed.City, Region: parsed.Region, Country: parsed.Country,
		IsPrimary: isPrimary, Now: now,
	})
	if err != nil {
		return store.CustomersCustomerAddress{}, err
	}
	return created, recordCustomerAddressAdded(ctx, txq, now, customerID, created.ID, addressSnapshotFromRow(created), act.Kind, act.Display, act.UserID)
}

// replaceAddress is PutCustomersByIdAddressesByAddressId's transaction body
// once the customer lock is held and existing has been read: the primary
// bookkeeping the handler's doc comment describes (errPrimaryTransitionRefused
// for a same-type demotion of the primary), the full-replace UPDATE, the old
// type's promotion when a primary changed type, and one
// customer.address_updated. The CSV importer replaces a primary postal or
// invoice address through it.
func replaceAddress(ctx context.Context, txq *store.Queries, customerID int32, existing store.CustomersCustomerAddress, parsed validatedAddress, requestedPrimary bool, now time.Time, act actor) (store.CustomersCustomerAddress, error) {
	var isPrimary bool
	if parsed.Type == existing.Type {
		if existing.IsPrimary && !requestedPrimary {
			return store.CustomersCustomerAddress{}, errPrimaryTransitionRefused
		}
		isPrimary = requestedPrimary || existing.IsPrimary
		if isPrimary && !existing.IsPrimary {
			if err := demoteCurrentPrimary(ctx, txq, customerID, parsed.Type, now); err != nil {
				return store.CustomersCustomerAddress{}, err
			}
		}
	} else {
		countOfNewType, err := txq.CountCustomerAddressesOfType(ctx, store.CountCustomerAddressesOfTypeParams{CustomerID: customerID, Type: parsed.Type, ExcludeID: existing.ID})
		if err != nil {
			return store.CustomersCustomerAddress{}, err
		}
		if countOfNewType == 0 {
			isPrimary = true
		} else {
			isPrimary = requestedPrimary
			if requestedPrimary {
				if err := demoteCurrentPrimary(ctx, txq, customerID, parsed.Type, now); err != nil {
					return store.CustomersCustomerAddress{}, err
				}
			}
		}
	}
	updated, err := txq.UpdateCustomerAddress(ctx, store.UpdateCustomerAddressParams{
		ID: existing.ID, CustomerID: customerID,
		Type: parsed.Type, Label: parsed.Label, Line1: parsed.Line1, Line2: parsed.Line2,
		PostalCode: parsed.PostalCode, City: parsed.City, Region: parsed.Region, Country: parsed.Country,
		IsPrimary: isPrimary, UpdatedAt: now,
	})
	if err != nil {
		return store.CustomersCustomerAddress{}, err
	}
	if parsed.Type != existing.Type && existing.IsPrimary {
		if err := promoteOldestOfType(ctx, txq, customerID, existing.Type, existing.ID, now); err != nil {
			return store.CustomersCustomerAddress{}, err
		}
	}
	return updated, recordCustomerAddressUpdated(ctx, txq, now, customerID, existing.ID, addressSnapshotFromRow(existing), addressSnapshotFromRow(updated), act.Kind, act.Display, act.UserID)
}

// removeAddress is DeleteCustomersByIdAddressesByAddressId's transaction body
// once the customer lock is held and existing has been read: the delete, the
// oldest remaining address of the type promoted when a primary went, and one
// customer.address_removed. The CSV importer removes a primary postal or
// invoice address through it.
func removeAddress(ctx context.Context, txq *store.Queries, customerID int32, existing store.CustomersCustomerAddress, now time.Time, act actor) error {
	// The row itself is deleted before any promotion of a different
	// address of the same type: while the deleted row still exists with
	// is_primary=true, promoting another row of the same type would
	// transiently violate ux_customer_addresses_primary (two primaries
	// of one type at once) — deleting it first removes that row from the
	// index entirely, so the promotion below is always safe.
	if err := txq.DeleteCustomerAddress(ctx, store.DeleteCustomerAddressParams{ID: existing.ID, CustomerID: customerID}); err != nil {
		return err
	}
	if existing.IsPrimary {
		if err := promoteOldestOfType(ctx, txq, customerID, existing.Type, existing.ID, now); err != nil {
			return err
		}
	}
	return recordCustomerAddressRemoved(ctx, txq, now, customerID, existing.ID, addressSnapshotFromRow(existing), act.Kind, act.Display, act.UserID)
}
```

Then the three handlers. `PostCustomersByIdAddresses`: delete `var capProblem …`, and replace its `err = db.WithTx(…)` statement and the `switch` after it with:

```go
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		if _, err := txq.LockCustomer(ctx, req.Id); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errCustomerNotFound
			}
			return err
		}
		var err error
		created, err = insertAddress(ctx, txq, req.Id, parsed, requestedPrimary, now, act)
		return err
	})
	switch {
	case errors.Is(err, errCustomerNotFound):
		return gen.PostCustomersByIdAddresses404Response{}, nil
	case errors.Is(err, errAddressCapReached):
		return gen.PostCustomersByIdAddresses400ApplicationProblemPlusJSONResponse(
			apicommon.ValidationProblem("Invalid address", map[string][]string{"addresses": {addressCapMessage}})), nil
	case err != nil:
		return nil, fmt.Errorf("customers: create address: %w", err)
	}
```

`PutCustomersByIdAddressesByAddressId`: delete `var before, after addressSnapshot` and `var primaryProblem …`, and replace its `err = db.WithTx(…)` statement and the `switch` after it with:

```go
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		if _, err := txq.LockCustomer(ctx, req.Id); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errCustomerNotFound
			}
			return err
		}
		existing, err := txq.GetCustomerAddress(ctx, store.GetCustomerAddressParams{ID: req.AddressId, CustomerID: req.Id})
		if errors.Is(err, pgx.ErrNoRows) {
			return errCustomerNotFound
		}
		if err != nil {
			return err
		}
		updated, err = replaceAddress(ctx, txq, req.Id, existing, parsed, requestedPrimary, now, act)
		return err
	})
	switch {
	case errors.Is(err, errCustomerNotFound):
		return gen.PutCustomersByIdAddressesByAddressId404Response{}, nil
	case errors.Is(err, errPrimaryTransitionRefused):
		return primaryTransitionProblem(), nil
	case err != nil:
		return nil, fmt.Errorf("customers: update address: %w", err)
	}
```

`DeleteCustomersByIdAddressesByAddressId`: in its closure, replace everything from the comment `// The row itself is deleted before…` through `return recordCustomerAddressRemoved(…)` with `return removeAddress(ctx, txq, req.Id, existing, now, act)`.

- [ ] **Step 5: Group and tags**

In `group_membership.go`, directly above `// PutCustomersByIdGroup`:

```go
// writeCustomerGroup is PutCustomersByIdGroup's transaction body — the guarded
// group_id write (pgx.ErrNoRows when expectedRevision is stale; a foreign-key
// violation on customersGroupFK when the group was deleted in between) and
// customer.group_changed with both names snapshotted. The CSV importer moves a
// row's customer through it too, unguarded (expectedRevision nil) because it
// holds the customer's row lock, so a move by file and a move by hand are one
// event of one shape.
func writeCustomerGroup(ctx context.Context, txq *store.Queries, id int32, before, after *groupSnapshot, expectedRevision *int32, now time.Time, act actor) (store.SetCustomerGroupRow, error) {
	var groupID *uuid.UUID
	if after != nil {
		groupID = &after.GroupID
	}
	updated, err := txq.SetCustomerGroup(ctx, store.SetCustomerGroupParams{
		ID: id, GroupID: groupID, UpdatedAt: now, ExpectedRevision: expectedRevision,
	})
	if err != nil {
		return store.SetCustomerGroupRow{}, err
	}
	return updated, recordCustomerGroupChanged(ctx, txq, now, id, before, after, act.Kind, act.Display, act.UserID)
}
```

Add `"time"` (and `"github.com/google/uuid"`, if the file does not import it yet) to its imports, and replace the handler's inner `err = db.WithTx(…)` statement with:

```go
		err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
			var err error
			updated, err = writeCustomerGroup(ctx, store.New(tx), req.Id, beforeSnapshot, afterSnapshot, guard, now, *act)
			return err
		})
```

(`afterSnapshot` is non-nil exactly when `after` is, carrying the same id, so `SetCustomerGroup` receives the same `group_id` it did.)

In `tags.go`, directly above `// PutCustomersByIdTags`' doc comment:

```go
// replaceCustomerTags is PutCustomersByIdTags' transaction body once the
// customer lock is held: the set replaced — every link deleted, the wanted ones
// inserted in one statement — and customer.tags_changed with what was added and
// removed. A tag deleted in between is a foreign-key violation on
// customerTagsTagFK, the caller's to map. The CSV importer replaces a row's
// tags through it.
func replaceCustomerTags(ctx context.Context, txq *store.Queries, id int32, wanted []uuid.UUID, added, removed []tagSnapshot, now time.Time, act actor) error {
	if err := txq.DeleteCustomerTagLinks(ctx, id); err != nil {
		return err
	}
	if err := txq.InsertCustomerTagLinks(ctx, store.InsertCustomerTagLinksParams{CustomerID: id, TagIds: wanted}); err != nil {
		return err
	}
	return recordCustomerTagsChanged(ctx, txq, now, id, added, removed, act.Kind, act.Display, act.UserID)
}
```

Add `"time"` to its imports if missing, and replace the three statements after `LockCustomer` inside the handler's closure (`DeleteCustomerTagLinks`, `InsertCustomerTagLinks`, `return recordCustomerTagsChanged(…)`) with `return replaceCustomerTags(ctx, txq, req.Id, wanted, added, removed, now, act)`.

- [ ] **Step 6: Run everything the seam touches, show the old tests still bite, commit**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'
mise exec -- gofmt -w internal/customers/customers.go internal/customers/legal_identity.go internal/customers/contact_info.go \
  internal/customers/billing_profile.go internal/customers/addresses.go internal/customers/group_membership.go internal/customers/tags.go
mise exec -- gofmt -l internal && mise exec -- go vet ./internal/customers/... && mise exec -- go build ./...
taskset -c 0-3 mise exec -- go test -count=1 ./internal/customers/ 2>&1 | tail -20; echo "exit ${PIPESTATUS[0]}"
```
Expected: PASS, the concurrency tests (`*_concurrency_test.go`) included — they are the proof that the lock and guard order did not move.

No test is new, so prove instead that the existing ones cover each extracted body, restoring after each: in `writeContactInfo` return `updated, nil` instead of recording the event — `contact_info_test.go`'s event test goes red; in `writeCustomerCore` delete the `invalidateRegistryRecord` block — `registry_test.go`'s identity-change test goes red; in `insertAddress` change `countOfType == 0` to `countOfType == 1` — `addresses_test.go`'s first-is-primary test goes red; in `writeCustomerGroup` pass `nil` for `GroupID` — `group_membership_test.go` goes red; in `insertNewCustomer` move `NextCounterValue` above the duplicate check — `duplicates_test.go`'s no-number-burned test goes red. Say which test named each.

```bash
cd /home/anders/projects/vantigo/vantigo
cat > /tmp/claude-1000/msg-import-export-3.txt <<'EOF'
refactor(customers): each field group's write is one function its endpoint calls

The create, the name/status/identity update, contact info, the billing
profile, the three address writes, the group move and the tag replace are
each lifted out of their handler into a function taking the transaction's
queries and an already-resolved actor, and each handler now calls its own.
Nothing a caller sees changes: the same validation in front, the same
guarded statements, the same events, the same lock order. One reorder:
the billing profile's rate is converted to its numeric column inside the
transaction now rather than before the actor lookup — a validated rate
never fails to convert, so no answer changes. The import will be each
function's second caller, so a row written from a file is the same write
as the edit made by hand.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
PATHS="apps/server/internal/customers/customers.go apps/server/internal/customers/legal_identity.go \
 apps/server/internal/customers/contact_info.go apps/server/internal/customers/billing_profile.go \
 apps/server/internal/customers/addresses.go apps/server/internal/customers/group_membership.go \
 apps/server/internal/customers/tags.go"
git add $PATHS && git commit -F /tmp/claude-1000/msg-import-export-3.txt -- $PATHS
git show --stat HEAD && git status --short
```

---

### Task 4: The import (D3)

**Files:**
- Create: `apps/server/internal/customers/import.go`, `apps/server/internal/customers/csvimport_test.go`, `apps/server/internal/customers/csvimport_cap_test.go`
- Modify: `apps/server/internal/customers/server.go`, `module.go`, `queries/customers.sql`, `queries/groups.sql`, `openapi/customers.yaml`
- Generated: `apps/server/internal/customers/store/customers.sql.go`, `store/groups.sql.go`, `apps/server/internal/openapi/specs/customers.yaml`, `apps/server/internal/customers/gen/api.gen.go`, `apps/customers/frontend/src/api-schema.d.ts`, `openapi/COVERAGE.md`
- Read first (do not change): `apps/server/internal/expenses/attachments.go:40-90, 249-290`, `apps/server/internal/customers/duplicates.go`, `apps/server/internal/db/tx.go`

**Interfaces:**
- Produces Go: `billingManage` (server.go); `importBodyLimits`; `store.CustomerIDByNumber(ctx, customerNumber int64) (int32, error)`; `gen.CustomerImportResult`, `gen.CustomerImportError`, `gen.PostCustomersImportParams{DryRun, AllowDuplicateIdentity *string}`, `gen.PostCustomersImportRequestObject{Params; Body *multipart.Reader}`; `(*server).PostCustomersImport`.
- Wire: `POST /api/v1/customers/import` (`postCustomersImport`, `multipart/form-data` with part `file`, query `dryRun`/`allowDuplicateIdentity` as plain strings, `200 application/json CustomerImportResult`, `400 application/problem+json HttpValidationProblemDetails`, `permission:customers:create+customers:update+customers:view`); schemas `CustomerImportResult {created, dryRun, errors, failed, rows, updated}` (all required) and `CustomerImportError {row, message}` required + `column` optional.
- Consumes: Tasks 1 and 3.

- [ ] **Step 1: Write the failing tests**

Create `apps/server/internal/customers/csvimport_test.go`:

```go
package customers_test

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is the customers file on its way in (customers import/export
// design D3): the file-level refusals, then what rows do — create, update by
// customer number, each group replaced whole or left alone, the vocabularies by
// name, the duplicate guard, the dry run — and the round trip that ties the
// import to the export.

type importResultJSON struct {
	DryRun  bool              `json:"dryRun"`
	Rows    int               `json:"rows"`
	Created int               `json:"created"`
	Updated int               `json:"updated"`
	Failed  int               `json:"failed"`
	Errors  []importErrorJSON `json:"errors"`
}

// importErrorJSON's Column is a plain string: absent on the wire (a row-level
// error) decodes as "".
type importErrorJSON struct {
	Row     int    `json:"row"`
	Column  string `json:"column"`
	Message string `json:"message"`
}

// csvFileOf is a customers file written the export's way: the BOM,
// semicolons, CRLF, a cell quoted when it has to be.
func csvFileOf(rows ...[]string) []byte {
	var b strings.Builder
	b.WriteString(csvBOM)
	for _, cells := range rows {
		for i, cell := range cells {
			if i > 0 {
				b.WriteByte(';')
			}
			if strings.ContainsAny(cell, ";\"\r\n") {
				cell = `"` + strings.ReplaceAll(cell, `"`, `""`) + `"`
			}
			b.WriteString(cell)
		}
		b.WriteString("\r\n")
	}
	return []byte(b.String())
}

// multipartBody wraps data as the one part named field, the way a browser's
// FormData sends a file.
func multipartBody(t *testing.T, field string, data []byte) (string, []byte) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile(field, "customers.csv")
	if err != nil {
		t.Fatalf("create the %s part: %v", field, err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("write the %s part: %v", field, err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close the multipart writer: %v", err)
	}
	return w.FormDataContentType(), buf.Bytes()
}

// postImport POSTs data as the import's file part, query appended as given
// ("" or "?dryRun=false…").
func postImport(t *testing.T, c *modtest.Client, query string, data []byte) *modtest.Response {
	t.Helper()
	contentType, body := multipartBody(t, "file", data)
	return c.Do(http.MethodPost, "/api/v1/customers/import"+query, nil, modtest.RawBody(contentType, body))
}

// importResultOf decodes a 200, failing the test on anything else.
func importResultOf(t *testing.T, r *modtest.Response) importResultJSON {
	t.Helper()
	if r.Status != http.StatusOK {
		t.Fatalf("import: status %d body %s, want 200", r.Status, r.Body)
	}
	var result importResultJSON
	r.JSON(&result)
	return result
}

// fileRefusal is a 400's messages on "file", failing the test on anything else.
func fileRefusal(t *testing.T, r *modtest.Response) []string {
	t.Helper()
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	return problem.Errors["file"]
}

// customerIDByName is the id of the one customer called name.
func customerIDByName(t *testing.T, h *modtest.Harness, name string) int32 {
	t.Helper()
	return modtest.One[int32](t, h, `SELECT id FROM customers.customers WHERE name = $1`, name)
}

var (
	contactHeader = []string{"email", "phone", "website"}
	postalHeader  = []string{"postalLine1", "postalLine2", "postalPostalCode", "postalCity", "postalRegion", "postalCountry"}
	billingHeader = []string{"invoiceEmail", "reminderEmail", "paymentTermsDays", "currency", "language", "invoiceDelivery",
		"reminderDelivery", "peppolId", "gln", "buyerReference", "defaultBillRate"}
	legalHeader = []string{"legalCountry", "legalType", "legalId", "legalName"}
)

// cells joins groups of cells into one row.
func cells(groups ...[]string) []string {
	var out []string
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

// TestPostCustomersImport_RefusesAFileItCannotRead: every file-level refusal
// is a 400 on "file" naming what is wrong — never a result — and none of them
// writes anything.
func TestPostCustomersImport_RefusesAFileItCannotRead(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	tooMany := [][]string{{"name"}}
	for i := 0; i <= 5000; i++ {
		tooMany = append(tooMany, []string{fmt.Sprintf("Kunde %d", i)})
	}
	for _, tc := range []struct {
		name string
		data []byte
		want string
	}{
		{"not UTF-8", []byte("name\r\n\xff\xfe\r\n"), "The file is not UTF-8 text"},
		{"a bare quote", []byte("name\r\nA \"b\" c\r\n"), "The file is not a semicolon-separated CSV file"},
		{"comma-separated", []byte("name,email\r\nA,a@x.no\r\n"), "The file is comma-separated"},
		{"an unknown column", csvFileOf([]string{"name", "nmae"}, []string{"A", "B"}), "Unknown columns: 'nmae'"},
		{"a column twice", csvFileOf([]string{"name", "Name"}, []string{"A", "B"}), "The column name appears more than once"},
		{"half a group", csvFileOf([]string{"name", "email"}, []string{"A", "a@x.no"}), "The file carries some of the contact info columns but not phone, website"},
		{"more than 5000 rows", csvFileOf(tooMany...), "The file holds more than 5000 rows"},
		{"past 5 MB", bytes.Repeat([]byte("a"), 5*1024*1024+1), "A customer import is one CSV file of at most 5 MB"},
		{"an empty part", []byte{}, "A customer import is one CSV file of at most 5 MB"},
	} {
		messages := fileRefusal(t, postImport(t, c, "?dryRun=false", tc.data))
		if len(messages) == 0 || !strings.HasPrefix(messages[0], tc.want) {
			t.Errorf("%s: file = %q, want it to start %q", tc.name, messages, tc.want)
		}
	}

	contentType, body := multipartBody(t, "upload", csvFileOf([]string{"name"}, []string{"A"}))
	r := c.Do(http.MethodPost, "/api/v1/customers/import?dryRun=false", nil, modtest.RawBody(contentType, body))
	if messages := fileRefusal(t, r); len(messages) != 1 || !strings.Contains(messages[0], "the multipart part named 'file'") {
		t.Errorf("a part named upload: file = %q", messages)
	}
	if r := c.Do(http.MethodPost, "/api/v1/customers/import?dryRun=false", nil,
		modtest.RawBody("text/csv", csvFileOf([]string{"name"}, []string{"A"})),
		modtest.SkipContract("a body that is not multipart is off-contract by construction")); r.Status != http.StatusBadRequest {
		t.Errorf("not multipart: status %d, want 400", r.Status)
	}
	r = postImport(t, c, "?dryRun=yes", csvFileOf([]string{"name"}, []string{"A"}))
	var problem validationProblemJSON
	r.JSON(&problem)
	if r.Status != http.StatusBadRequest || len(problem.Errors["dryRun"]) != 1 || problem.Errors["dryRun"][0] != "'dryRun' must be one of 'true' or 'false', but was 'yes'." {
		t.Errorf("dryRun=yes: %d %+v", r.Status, problem)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers`); n != 0 {
		t.Errorf("%d customers written by refused files, want none", n)
	}
}

// TestPostCustomersImport_AColumnTheSenderMayNotWriteRefusesTheFileWhole is
// D3's rule: no import key, the file checked against the caller instead — the
// legal identity's columns against customers:legal-identity-manage, the
// billing profile's against customers:billing-manage — and a column its sender
// may not write refuses the whole file, naming the columns and the key. The
// router itself wants create, update and view together.
func TestPostCustomersImport_AColumnTheSenderMayNotWriteRefusesTheFileWhole(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	noIdentity := h.SignIn(t, "customers:view", "customers:create", "customers:update", "customers:billing-manage")
	noBilling := h.SignIn(t, "customers:view", "customers:create", "customers:update", "customers:legal-identity-manage")

	identityFile := csvFileOf(cells([]string{"name"}, legalHeader), []string{"Fjord AS", "no", "business", "923609016", "Fjord AS"})
	if messages := fileRefusal(t, postImport(t, noIdentity, "?dryRun=false", identityFile)); len(messages) != 1 ||
		!strings.Contains(messages[0], "legalCountry, legalType, legalId, legalName") || !strings.Contains(messages[0], "customers:legal-identity-manage") {
		t.Errorf("identity columns without the key: file = %q", messages)
	}
	billingFile := csvFileOf(cells([]string{"name"}, billingHeader), cells([]string{"Fjord AS"}, make([]string, 11)))
	if messages := fileRefusal(t, postImport(t, noBilling, "?dryRun=false", billingFile)); len(messages) != 1 ||
		!strings.Contains(messages[0], "invoiceEmail, reminderEmail") || !strings.Contains(messages[0], "customers:billing-manage") {
		t.Errorf("billing columns without the key: file = %q", messages)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers`); n != 0 {
		t.Errorf("%d customers written, want none", n)
	}

	for _, keys := range [][]string{
		{"customers:view", "customers:create"},
		// create and update without view: every hand write the import stands
		// in for needs view too, and row errors would describe customers the
		// caller may not see.
		{"customers:create", "customers:update"},
	} {
		if r := postImport(t, h.SignIn(t, keys...), "", csvFileOf([]string{"name"}, []string{"A"})); r.Status != http.StatusForbidden {
			t.Errorf("%v: status %d, want 403", keys, r.Status)
		}
	}
}

// TestPostCustomersImport_CreatesAndUpdatesByCustomerNumber_AsTheImporter: a
// blank number creates, a number selects the customer to update, and every
// event the rows record carries the importer — a row written by file is
// indistinguishable from the same edit made by hand.
func TestPostCustomersImport_CreatesAndUpdatesByCustomerNumber_AsTheImporter(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, userID := authenticatedClientWithID(t, h)
	existing := createCustomer(t, c, "Gammel AS")

	result := importResultOf(t, postImport(t, c, "?dryRun=false", csvFileOf(
		[]string{"customerNumber", "name", "type", "status"},
		[]string{"", "Ny Person", "person", ""},
		[]string{fmt.Sprint(existing.CustomerNumber), "Gammel og Ny AS", "", "disabled"},
	)))
	if result.DryRun || result.Rows != 2 || result.Created != 1 || result.Updated != 1 || result.Failed != 0 || len(result.Errors) != 0 {
		t.Fatalf("result = %+v, want 1 created, 1 updated", result)
	}

	created := fetchCustomerJSON(t, c, customerIDByName(t, h, "Ny Person"))
	if created.Status != "active" {
		t.Errorf("created status = %q, want active (the create default)", created.Status)
	}
	updated := fetchCustomerJSON(t, c, existing.Id)
	if updated.Name != "Gammel og Ny AS" || updated.Status != "disabled" || updated.Revision != 2 {
		t.Errorf("updated = %+v, want renamed and disabled in one write (revision 2)", updated)
	}
	for _, e := range []struct {
		customer  int32
		eventType string
	}{{created.Id, "customer.created"}, {existing.Id, "customer.updated"}, {existing.Id, "customer.status_changed"}} {
		actor := modtest.One[string](t, h, `SELECT actor_kind || '/' || actor_display || '/' || actor_user_id::text
		    FROM customers.customers_timeline_entries WHERE customer_id = $1 AND event_type = $2`, e.customer, e.eventType)
		if want := "user/" + userDisplayName(t, h, userID) + "/" + userID.String(); actor != want {
			t.Errorf("%s actor = %q, want %q", e.eventType, actor, want)
		}
	}
}

// TestPostCustomersImport_DefaultsToADryRunThatWritesNothing_AndReportsTheRealRun
// is D3's dry run: without dryRun the import only checks — no customer, no
// event, no customer number burned — and what it reports is exactly what the
// real run then does.
func TestPostCustomersImport_DefaultsToADryRunThatWritesNothing_AndReportsTheRealRun(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	existing := createCustomer(t, c, "Eksisterende AS")
	file := csvFileOf(
		cells([]string{"customerNumber", "name"}, contactHeader),
		[]string{"", "Ny Kunde AS", "post@ny.no", "", ""},
		[]string{fmt.Sprint(existing.CustomerNumber), "Eksisterende og Omdøpt AS", "", "", ""},
		[]string{"", "Feil AS", "ikke-en-adresse", "", ""},
	)
	customers := h.Count(t, `SELECT count(*) FROM customers.customers`)
	events := h.Count(t, `SELECT count(*) FROM customers.customers_timeline_entries`)
	counter := modtest.One[int64](t, h, `SELECT next_value FROM customers.counters WHERE counter_name = 'customer-number'`)

	dry := importResultOf(t, postImport(t, c, "", file))
	if !dry.DryRun || dry.Rows != 3 || dry.Created != 1 || dry.Updated != 1 || dry.Failed != 1 {
		t.Errorf("dry run = %+v, want 1 created, 1 updated, 1 failed", dry)
	}
	if h.Count(t, `SELECT count(*) FROM customers.customers`) != customers ||
		h.Count(t, `SELECT count(*) FROM customers.customers_timeline_entries`) != events ||
		modtest.One[int64](t, h, `SELECT next_value FROM customers.counters WHERE counter_name = 'customer-number'`) != counter {
		t.Errorf("the dry run wrote something")
	}
	if got := fetchCustomerJSON(t, c, existing.Id); got.Name != "Eksisterende AS" {
		t.Errorf("name after the dry run = %q, want unchanged", got.Name)
	}

	real := importResultOf(t, postImport(t, c, "?dryRun=false", file))
	if real.DryRun || real.Created != dry.Created || real.Updated != dry.Updated || real.Failed != dry.Failed ||
		fmt.Sprint(real.Errors) != fmt.Sprint(dry.Errors) {
		t.Errorf("real run = %+v, want what the dry run reported: %+v", real, dry)
	}
	if want := []importErrorJSON{{Row: 3, Column: "email", Message: "An email address must look like name@example.com, but was 'ikke-en-adresse'"}}; fmt.Sprint(real.Errors) != fmt.Sprint(want) {
		t.Errorf("errors = %+v, want %+v", real.Errors, want)
	}
}

// TestPostCustomersImport_TwoRowsCreatingOneIdentity_TheSecondIsARowErrorInBothRuns:
// a dry run's rows each roll back, so the database cannot see that row 1
// already created the identity row 2 creates — the in-file check does, before
// any row runs, and the two runs agree. allowDuplicateIdentity lets both in.
func TestPostCustomersImport_TwoRowsCreatingOneIdentity_TheSecondIsARowErrorInBothRuns(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	file := csvFileOf(cells([]string{"name"}, legalHeader),
		[]string{"Fjord AS", "no", "business", "923609016", "Fjord AS"},
		[]string{"Fjord Kopi AS", "no", "business", "923 609 016", "Fjord AS"},
	)
	want := []importErrorJSON{{Row: 2, Column: "legalId",
		Message: "Row 1 of this file already creates a customer with this legal identity. Import with allowDuplicateIdentity=true to keep both."}}
	for _, query := range []string{"", "?dryRun=false"} {
		result := importResultOf(t, postImport(t, c, query, file))
		if result.Created != 1 || result.Failed != 1 || fmt.Sprint(result.Errors) != fmt.Sprint(want) {
			t.Errorf("%q: result = %+v, want row 2 refused with %+v", query, result, want)
		}
	}
	if result := importResultOf(t, postImport(t, c, "?dryRun=false&allowDuplicateIdentity=true", file)); result.Created != 2 || result.Failed != 0 {
		t.Errorf("allowDuplicateIdentity=true: result = %+v, want both created", result)
	}
}

// TestPostCustomersImport_ACarriedGroupIsReplacedWhole_AnAbsentOneIsLeftAlone
// is D3's group rule: a group in the header is written as its endpoint writes
// it — a blank cell clears that field, all of an address's cells blank remove
// the primary address, a blank tags cell clears the tags — and a group not in
// the header is not touched.
func TestPostCustomersImport_ACarriedGroupIsReplacedWhole_AnAbsentOneIsLeftAlone(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	r := c.Do(http.MethodPost, "/api/v1/customers", map[string]any{
		"name": "Full AS", "contactInfo": map[string]any{"email": "gammel@full.no", "phone": "22 33 44 55"},
	})
	var full createdCustomerJSON
	r.JSON(&full)
	createAddress(t, c, full.Id, map[string]any{"type": "postal", "line1": "Storgata 1", "postalCode": "0155", "city": "Oslo", "country": "no"})
	if r := putBillingProfile(t, c, full.Id, map[string]any{"currency": "NOK", "paymentTermsDays": 30}); r.Status != http.StatusOK {
		t.Fatalf("billing: status %d body %s", r.Status, r.Body)
	}
	vip := createTag(t, c, map[string]any{"name": "VIP"})
	if r := putCustomerTags(t, c, full.Id, []string{vip.Id}); r.Status != http.StatusOK {
		t.Fatalf("tags: status %d body %s", r.Status, r.Body)
	}
	number := fmt.Sprint(full.CustomerNumber)

	result := importResultOf(t, postImport(t, c, "?dryRun=false", csvFileOf(
		cells([]string{"customerNumber"}, contactHeader, postalHeader, []string{"tags"}),
		cells([]string{number, "ny@full.no", "", ""}, make([]string, 6), []string{""}),
	)))
	if result.Updated != 1 || result.Failed != 0 {
		t.Fatalf("first file: result = %+v", result)
	}
	got := fetchCustomerJSON(t, c, full.Id)
	if got.ContactInfo.Email == nil || *got.ContactInfo.Email != "ny@full.no" || got.ContactInfo.Phone != nil || len(got.Tags) != 0 {
		t.Errorf("customer = %+v, want the new email, the phone cleared, no tags", got)
	}
	if addresses := listAddresses(t, c, full.Id); len(addresses.Data) != 0 {
		t.Errorf("addresses = %+v, want the primary postal address removed", addresses.Data)
	}
	if p := fetchBillingProfile(t, c, full.Id); p.Currency == nil || *p.Currency != "NOK" || p.PaymentTermsDays == nil || *p.PaymentTermsDays != 30 {
		t.Errorf("billing profile = %+v, want it untouched by a file without its columns", p)
	}

	result = importResultOf(t, postImport(t, c, "?dryRun=false", csvFileOf(
		cells([]string{"customerNumber"}, billingHeader),
		cells([]string{number}, []string{"", "", "", "EUR", "", "", "", "", "", "", "1250,50"}),
	)))
	if result.Updated != 1 || result.Failed != 0 {
		t.Fatalf("second file: result = %+v", result)
	}
	if p := fetchBillingProfile(t, c, full.Id); p.Currency == nil || *p.Currency != "EUR" || p.PaymentTermsDays != nil || p.DefaultBillRate == nil || *p.DefaultBillRate != 1250.5 {
		t.Errorf("billing profile = %+v, want EUR, the rate, and the blank terms cleared", p)
	}
}

// TestPostCustomersImport_GroupAndTagsAreNamedCaseInsensitively_AnUnknownNameIsARowError:
// a file names the vocabulary as a person reads it, whatever the case, and a
// word the vocabulary does not have is that row's error — never a word created
// behind anybody's back.
func TestPostCustomersImport_GroupAndTagsAreNamedCaseInsensitively_AnUnknownNameIsARowError(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	createGroup(t, c, map[string]any{"name": "Retail"})
	createTag(t, c, map[string]any{"name": "VIP"})
	createTag(t, c, map[string]any{"name": "Prospect"})

	result := importResultOf(t, postImport(t, c, "?dryRun=false", csvFileOf(
		[]string{"name", "group", "tags"},
		[]string{"A AS", "retail", "vip | PROSPECT"},
		[]string{"B AS", "Wholesale", ""},
		[]string{"C AS", "", "VIP|Nope"},
	)))
	want := []importErrorJSON{
		{Row: 2, Column: "group", Message: "No customer group is named 'Wholesale'"},
		{Row: 3, Column: "tags", Message: "No tag is named 'Nope'"},
	}
	if result.Created != 1 || result.Failed != 2 || fmt.Sprint(result.Errors) != fmt.Sprint(want) {
		t.Fatalf("result = %+v, want A created and %+v", result, want)
	}
	a := fetchCustomerJSON(t, c, customerIDByName(t, h, "A AS"))
	if a.Group == nil || a.Group.Name != "Retail" || len(a.Tags) != 2 || a.Tags[0].Name != "Prospect" || a.Tags[1].Name != "VIP" {
		t.Errorf("A = group %+v tags %+v, want Retail and Prospect, VIP", a.Group, a.Tags)
	}
}

// TestPostCustomersImport_ADuplicateIdentityIsARowError_UnlessAllowed is the
// create endpoint's own 409, per row, and its own flag, for the whole file.
func TestPostCustomersImport_ADuplicateIdentityIsARowError_UnlessAllowed(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	holder := createCustomerWithIdentity(t, c, "Holder AS", "no", "923609016")
	file := csvFileOf(cells([]string{"name"}, legalHeader), []string{"Tvilling AS", "no", "business", "923 609 016", "Tvilling AS"})

	result := importResultOf(t, postImport(t, c, "?dryRun=false", file))
	if result.Failed != 1 || len(result.Errors) != 1 || result.Errors[0].Column != "legalId" ||
		!strings.HasPrefix(result.Errors[0].Message, "Another customer already has this legal identity") ||
		!strings.Contains(result.Errors[0].Message, fmt.Sprintf("%d Holder AS", holder.CustomerNumber)) {
		t.Fatalf("result = %+v, want the duplicate refused on legalId, naming the holder", result)
	}
	if result := importResultOf(t, postImport(t, c, "?dryRun=false&allowDuplicateIdentity=true", file)); result.Created != 1 || result.Failed != 0 {
		t.Errorf("allowDuplicateIdentity=true: result = %+v, want it created", result)
	}
}

// TestPostCustomersImport_RowErrorsNameTheirRowAndColumn_AndNeverStopTheFile:
// rows run in file order, each on its own; a row that fails is reported by
// number and, for a field, by column, and the rows after it still run.
func TestPostCustomersImport_RowErrorsNameTheirRowAndColumn_AndNeverStopTheFile(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	existing := createCustomer(t, c, "Bedrift AS")

	result := importResultOf(t, postImport(t, c, "?dryRun=false", csvFileOf(
		cells([]string{"customerNumber", "name", "type"}, contactHeader),
		[]string{"", "Første AS", "", "", "", ""},
		[]string{"999999", "Spøkelse AS", "", "", "", ""},
		[]string{"", "Feil AS", "", "nei", "12", ""},
		[]string{"", "Kort AS"},
		[]string{fmt.Sprint(existing.CustomerNumber), "Bedrift AS", "person", "", "", ""},
		[]string{"", "Siste AS", "", "", "", ""},
	)))
	want := []importErrorJSON{
		{Row: 2, Column: "customerNumber", Message: "No customer has number 999999"},
		{Row: 3, Column: "email", Message: "An email address must look like name@example.com, but was 'nei'"},
		{Row: 3, Column: "phone", Message: "A phone number may only contain digits, spaces and + - ( ), and needs at least five digits, but was '12'"},
		{Row: 4, Message: "This row has 2 cells, but the header has 6"},
		{Row: 5, Column: "type", Message: fmt.Sprintf("A customer's type is changed on its own, never by an import; this row says 'person', but customer %d is 'business'", existing.CustomerNumber)},
	}
	if result.Rows != 6 || result.Created != 2 || result.Failed != 4 || fmt.Sprint(result.Errors) != fmt.Sprint(want) {
		t.Fatalf("result = %+v\nwant 2 created and errors %+v", result, want)
	}
	customerIDByName(t, h, "Siste AS") // the row after four failures still ran
}

// TestPostCustomersImport_ARepeatedIdentityKeepsItsSource: a file cannot say
// where an identity came from, so a new one is manual (design D1) — but a row
// repeating the identity on file is no change at all, and a Brreg pick stays a
// Brreg pick with no event recorded.
func TestPostCustomersImport_ARepeatedIdentityKeepsItsSource(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	fjord := createCustomerWithIdentity(t, c, "Fjord AS", "no", "923609016")
	h.Exec(t, `UPDATE customers.customers SET legal_source = 'brreg' WHERE id = $1`, fjord.Id)
	events := countTimelineEvents(t, h, fjord.Id, "customer.updated")

	result := importResultOf(t, postImport(t, c, "?dryRun=false", csvFileOf(
		cells([]string{"customerNumber"}, legalHeader),
		[]string{fmt.Sprint(fjord.CustomerNumber), "NO", "business", "923609016", "Fjord AS"},
	)))
	if result.Updated != 1 || result.Failed != 0 {
		t.Fatalf("result = %+v", result)
	}
	if row := fetchLegalRow(t, h, fjord.Id); row.Source != "brreg" {
		t.Errorf("source = %q, want brreg kept", row.Source)
	}
	if n := countTimelineEvents(t, h, fjord.Id, "customer.updated"); n != events {
		t.Errorf("customer.updated events = %d, want %d: nothing changed", n, events)
	}
}

// TestPostCustomersImport_IgnoresTheExportOnlyColumnsAndTheErrorColumn: an
// export re-imports without editing (D1), and so does the failed-rows file the
// browser builds, with its error column (D4).
func TestPostCustomersImport_IgnoresTheExportOnlyColumnsAndTheErrorColumn(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	result := importResultOf(t, postImport(t, c, "?dryRun=false", csvFileOf(
		[]string{"name", "id", "ownerName", "createdAt", "updatedAt", "error"},
		[]string{"Rettet AS", "1234", "Kari Nordmann", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z", "email: something was wrong"},
	)))
	if result.Created != 1 || result.Failed != 0 {
		t.Errorf("result = %+v, want the row created", result)
	}
}

// TestCustomersExportImport_ARoundTripChangesNothing: import what the export
// wrote and every customer comes back as it was — every row an update, nothing
// written, no event, and the next export byte for byte the first.
func TestCustomersExportImport_ARoundTripChangesNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, userID := authenticatedClientWithID(t, h)
	r := c.Do(http.MethodPost, "/api/v1/customers", map[string]any{
		"name":        `Fjord; "Nord" AS`,
		"identity":    map[string]any{"country": "no", "type": "business", "id": "923609016", "name": "Fjord Nord AS", "source": "manual"},
		"contactInfo": map[string]any{"email": "post@fjord.no", "phone": "+47 22 33 44 55", "website": "https://fjord.no"},
	})
	var fjord createdCustomerJSON
	r.JSON(&fjord)
	createAddress(t, c, fjord.Id, map[string]any{"type": "postal", "label": "HQ", "line1": "Storgata 1", "postalCode": "0155", "city": "Oslo", "country": "no"})
	createAddress(t, c, fjord.Id, map[string]any{"type": "invoice", "line1": "Postboks 7", "postalCode": "0101", "city": "Oslo", "country": "no"})
	if r := putBillingProfile(t, c, fjord.Id, map[string]any{
		"invoiceEmail": "faktura@fjord.no", "paymentTermsDays": 14, "currency": "NOK", "language": "nb",
		"invoiceDelivery": "ehf", "peppolId": "0192:923609016", "buyerReference": "PO-42", "defaultBillRate": 1250.5,
	}); r.Status != http.StatusOK {
		t.Fatalf("billing: status %d body %s", r.Status, r.Body)
	}
	retail := createGroup(t, c, map[string]any{"name": "Retail"})
	putCustomerGroup(t, c, fjord.Id, map[string]any{"groupId": retail.Id})
	vip := createTag(t, c, map[string]any{"name": "VIP"})
	putCustomerTags(t, c, fjord.Id, []string{vip.Id})
	putOwner(t, c, fjord.Id, map[string]any{"ownerUserId": userID.String()})
	createCustomerOfType(t, c, "Ola Nordmann", "person")
	createCustomer(t, c, "=Formel AS")

	first := exportCSV(t, c, "").Body
	events := h.Count(t, `SELECT count(*) FROM customers.customers_timeline_entries`)
	result := importResultOf(t, postImport(t, c, "?dryRun=false", first))
	if result.Rows != 3 || result.Updated != 3 || result.Created != 0 || result.Failed != 0 {
		t.Fatalf("result = %+v, want three updates", result)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers_timeline_entries`); n != events {
		t.Errorf("timeline entries = %d, want %d: a round trip writes nothing", n, events)
	}
	if second := exportCSV(t, c, "").Body; !bytes.Equal(first, second) {
		t.Errorf("the second export differs:\n%q\nfrom\n%q", second, first)
	}
}
```

Create `apps/server/internal/customers/csvimport_cap_test.go`:

```go
package customers_test

import (
	"fmt"
	"testing"
	"time"
)

// importAtTheCapBudget is what a real run of maxImportRows rows may take
// (design D3's "measure it and say what it takes"). Sixty seconds, well inside
// the ~100 s after which the proxy in front of a hosted installation
// (cloudflared) answers 524 while the rows go on committing. The run logs what
// it actually took, and that number is what docs/customers.md quotes.
const importAtTheCapBudget = 60 * time.Second

// importCapSlice is how many rows one request of this test carries. modtest's
// client gives every request a hard 30 s (modtest/client.go, clientTimeout),
// so the 5000 rows go as five files of 1000 and the five times are summed: the
// per-row cost is the same, since each row is its own transaction either way.
const importCapSlice = 1000

// TestPostCustomersImport_AtTheCap is a real run of 5000 creates, every row
// carrying contact info, a postal address, a billing profile and a tag — the
// most a first onboarding does per row. Not parallel, so the timing is this
// test's alone; skipped under -short. A dry run is the same transactions
// rolled back, so it costs the same and is not timed separately.
func TestPostCustomersImport_AtTheCap(t *testing.T) {
	if testing.Short() {
		t.Skip("imports 5000 customers")
	}
	h := newHarness(t)
	c := authenticatedClient(t, h)
	createTag(t, c, map[string]any{"name": "VIP"})
	header := cells([]string{"name"}, contactHeader, postalHeader, billingHeader, []string{"tags"})

	var took time.Duration
	created := 0
	for slice := 0; slice < 5000/importCapSlice; slice++ {
		rows := [][]string{header}
		for i := slice*importCapSlice + 1; i <= (slice+1)*importCapSlice; i++ {
			rows = append(rows, cells(
				[]string{fmt.Sprintf("Kunde %04d AS", i)},
				[]string{fmt.Sprintf("kunde%d@example.no", i), "", ""},
				[]string{fmt.Sprintf("Gate %d", i), "", "0155", "Oslo", "", "no"},
				[]string{"", "", "30", "NOK", "nb", "email", "", "", "", "", "1250,00"},
				[]string{"VIP"},
			))
		}
		start := time.Now()
		result := importResultOf(t, postImport(t, c, "?dryRun=false", csvFileOf(rows...)))
		took += time.Since(start)
		if result.Failed != 0 {
			t.Fatalf("slice %d: result = %+v, want every row created", slice, result)
		}
		created += result.Created
	}
	t.Logf("5000 rows as %d requests of %d: %s in all", 5000/importCapSlice, importCapSlice, took.Round(time.Millisecond))

	if created != 5000 {
		t.Errorf("created %d, want 5000", created)
	}
	if took > importAtTheCapBudget {
		t.Errorf("5000 rows took %s; want within %s — see Task 4 Step 6's rule for lowering maxImportRows", took, importAtTheCapBudget)
	}
}
```

- [ ] **Step 2: Run them and watch them fail**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'
mise exec -- go test -count=1 -short -run 'PostCustomersImport|CustomersExportImport' ./internal/customers/
```
Expected: FAIL — every POST answers 404 (no such operation).

- [ ] **Step 3: The query, the key and the contract**

In `queries/customers.sql`, directly after the `GetCustomer` block:

```sql
-- name: CustomerIDByNumber :one
-- CustomerIDByNumber is the CSV import's match (customers import/export design
-- D3): a row's customerNumber selects the customer it updates.
-- ux_customers_customer_number makes the answer one row or none, and
-- pgx.ErrNoRows is that row's error, not the file's.
SELECT id FROM customers.customers WHERE customer_number = @customer_number;
```

In `queries/groups.sql`'s `SetCustomerGroup` comment, replace `-- handler's two reads agreed on (the NULL arm is never taken from this` / `-- handler — a request without a revision is guarded on what it read, and` / `-- re-read on a miss), and the handler skips calling` with:

```sql
-- handler's two reads agreed on (the NULL arm is never taken from this
-- handler — a request without a revision is guarded on what it read, and
-- re-read on a miss; the CSV importer takes it, under LockCustomer, where
-- nothing can move between its read and this write), and the handler skips calling
```

In `server.go`, directly after the `legalIdentityManage` constant:

```go
// billingManage is the permission the CSV import asks for itself (customers
// import/export design D3): a file carrying the billing profile's columns is
// refused whole without it, because the importer may never write more than
// its sender could by hand, and the billing profile's own PUT wants this key.
// module.Router enforces it everywhere else, from x-vantigo-access.
const billingManage = "customers:billing-manage"
```

In `openapi/customers.yaml`, directly above `/api/v1/customers/import/template:`:

```yaml
    /api/v1/customers/import:
        post:
            description: |-
                Creates and updates customers from the customers file (customers import/export design D1, D3) — the columns GET /customers/export writes and GET /customers/import/template starts, read with the same format rules; id, ownerName, createdAt, updatedAt and a column named error are ignored, so an export and a failed-rows file re-import as they are. At most 5000 data rows and 5 MB.

                The file may only say what its sender could say by hand. There is no import permission: the operation wants customers:create, customers:update and customers:view — view because every write the import stands in for needs it by hand (PUT /customers/{id} and each sub-resource PUT are update plus view), and because a row's errors (an unknown number, a customer's type) would otherwise tell a caller about customers they may not see — and the file is then checked against the caller before any row is read — the legal-identity columns need customers:legal-identity-manage, the billing columns customers:billing-manage — and a file carrying a column its sender may not write is refused whole. So is a file with an unknown column (a misspelt header is never a silently ignored one) or with some but not all of a group's columns: the legal identity's four, contact info's three, each address's six and the billing profile's eleven come together or not at all.

                Each row, in file order: a customerNumber selects the customer to update (unknown is that row's error; no revision is sent, so the change applies regardless), a blank one creates. Each group in the header is written as its own endpoint writes it — a full replace, a blank cell clearing that field; all of an address's cells blank remove the primary address of that type; a blank tags cell clears the tags — and a group not in the header is left alone. name, type and status are each optional columns: a create needs a name and defaults to business and active; on an update an absent name column keeps the name but a blank name cell is that row's error (a name is never cleared), a blank or absent status keeps the status, and a type that differs is an error (PUT /customers/{id}/type is a deliberate act). An imported legal identity's source is manual, unless it repeats the identity on file field for field, which keeps it. group and tags name existing vocabulary, case-insensitively; an unknown name is that row's error. Each row is its own transaction through the endpoints' own validation, statements and events, the caller as actor; a row that fails writes nothing and does not stop the file.

                dryRun defaults to true: every row runs exactly as in a real run, each in its own transaction, rolled back instead of committed — no customer, no number and no event is kept, and no lock outlives its row. Two rows of the file creating one legal identity are refused on the second in both runs (a check made on the file before any row runs); otherwise a dry run cannot see an earlier row's effect on a later one, and where that matters the real run refuses the later row cleanly, as that row's error. allowDuplicateIdentity is the create endpoint's flag, applied to every row.
            operationId: postCustomersImport
            parameters:
                - description: "'true' (the default) checks the file and keeps nothing; 'false' imports it."
                  in: query
                  name: dryRun
                  schema:
                    type: string
                - description: "'true' skips the duplicate-legal-identity check for every row; 'false' (the default) makes a duplicate that row's error."
                  in: query
                  name: allowDuplicateIdentity
                  schema:
                    type: string
            requestBody:
                content:
                    multipart/form-data:
                        schema:
                            properties:
                                file:
                                    format: binary
                                    type: string
                            required:
                                - file
                            type: object
                required: true
            responses:
                "200":
                    content:
                        application/json:
                            schema:
                                $ref: '#/components/schemas/CustomerImportResult'
                    description: OK — what the rows did, or would do in a dry run.
                "400":
                    content:
                        application/problem+json:
                            schema:
                                $ref: common.yaml#/components/schemas/HttpValidationProblemDetails
                    description: Bad Request — on 'file' for the file itself (no part named file, empty, past 5 MB, not UTF-8, not a semicolon-separated CSV, more than 5000 rows, an unknown or repeated column, part of a group, or a column the caller may not write), or on 'dryRun' or 'allowDuplicateIdentity' for a value that is neither 'true' nor 'false'. A body that is not multipart at all is a bare 400.
                "401":
                    content:
                        application/json:
                            schema:
                                $ref: common.yaml#/components/schemas/AuthErrorResponse
                    description: Unauthorized
                "403":
                    content:
                        application/json:
                            schema:
                                $ref: common.yaml#/components/schemas/AuthErrorResponse
                    description: Forbidden
            summary: Import customers from CSV
            tags:
                - Customers
            x-vantigo-access: permission:customers:create+customers:update+customers:view
```

Under `components.schemas`, in alphabetical place — after the `CustomerGroup…` schemas and before `CustomerOverviewAmount`:

```yaml
        CustomerImportError:
            description: One problem with one row of an import (customers import/export design D3). row is the 1-based data row — the first row under the header is 1, and a row whose every cell is blank is skipped and takes no number. column is the header of the offending cell for a field's error, absent for a problem with the row as a whole. message is the module's own validation wording.
            properties:
                column:
                    type: string
                message:
                    type: string
                row:
                    format: int32
                    type: integer
            required:
                - message
                - row
            type: object
        CustomerImportResult:
            description: What an import did, or in a dry run would do (customers import/export design D3). rows counts the data rows read; created and updated the rows that succeeded (an update that changed nothing still counts); failed the rows that did not, each with at least one entry in errors, in row order.
            properties:
                created:
                    format: int32
                    type: integer
                dryRun:
                    type: boolean
                errors:
                    items:
                        $ref: '#/components/schemas/CustomerImportError'
                    type: array
                failed:
                    format: int32
                    type: integer
                rows:
                    format: int32
                    type: integer
                updated:
                    format: int32
                    type: integer
            required:
                - created
                - dryRun
                - errors
                - failed
                - rows
                - updated
            type: object
```

- [ ] **Step 4: Generate**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- go generate ./... && mise exec -- go generate ./... && git -C ../.. status --short
grep -n "type PostCustomersImportParams struct\|type PostCustomersImportRequestObject struct\|type CustomerImportResult struct\|type CustomerImportError struct\|func (q \*Queries) CustomerIDByNumber" -A6 internal/customers/gen/api.gen.go internal/customers/store/customers.sql.go | head -50
```
Expected: `PostCustomersImportParams{DryRun, AllowDuplicateIdentity *string}`, `PostCustomersImportRequestObject{Params PostCustomersImportParams; Body *multipart.Reader}`, `CustomerImportResult{Created int32; DryRun bool; Errors []CustomerImportError; Failed, Rows, Updated int32}`, `CustomerImportError{Column *string; Message string; Row int32}`, `CustomerIDByNumber(ctx, customerNumber int64) (int32, error)`. Red until Step 5.

- [ ] **Step 5: The importer**

Create `apps/server/internal/customers/import.go`:

```go
package customers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
	"github.com/vantigo-io/vantigo/server/internal/customers/store"
	"github.com/vantigo-io/vantigo/server/internal/db"
)

// This file is POST /customers/import (customers import/export design D3):
// the customers file (csvfile.go) read back into customers. Four things about
// it are worth saying once, because every function below follows from them:
//
//   - A file may only say what its sender could say by hand. There is no
//     import key: the router admits customers:create and customers:update
//     together, and the file's columns are then checked against the caller
//     before a row is read — the legal identity's against
//     customers:legal-identity-manage, the billing profile's against
//     customers:billing-manage. A column the sender may not write refuses the
//     file whole rather than being skipped: a skipped column is a change the
//     sender believes was made.
//   - Each row is written through the functions the endpoints themselves call
//     (insertNewCustomer, writeCustomerCore, writeContactInfo,
//     insertAddress/replaceAddress/removeAddress, writeBillingProfile,
//     writeCustomerGroup, replaceCustomerTags), behind the same validators and
//     followed by the same events, the importer as actor — so a row that
//     succeeds is indistinguishable from the same edits made by hand, and one
//     that fails leaves nothing half-written. No batch marker and no
//     customer.imported event: the granular events are the audit trail.
//   - A dry run is the real run minus the commit: each row in its own
//     transaction, rolled back when the row has run. Nothing is held past a
//     row — no lock, no counter increment, no event. What it cannot see is an
//     earlier row's effect on a later one; the one such case a file makes
//     likely — two rows creating one legal identity — is checked in memory
//     before any row runs (inFileDuplicates), and any other the real run still
//     refuses cleanly, as that row's error.
//   - Nothing leaves the database while a row's transaction is open: the actor
//     and the group and tag vocabularies are resolved once, before the first
//     row, and every access check the file needs is made on its header.

// The upload's bounds (design D3). maxImportRequestBytes is the router's cap
// for the operation (module.RouterOptions.BodyLimits): the file plus room for
// the multipart framing, expenses' receipt margin. The router's
// http.MaxBytesReader is in place before the generated wrapper hands the
// multipart reader over, so a body past it fails the part read, which
// importFilePart answers as the same 400 an oversized part gets.
const (
	maxImportFileBytes    = 5 * 1024 * 1024
	maxImportRequestBytes = maxImportFileBytes + 64*1024
	importFormField       = "file"
	// importErrorColumn is the column the browser's failed-rows file adds
	// (design D4), ignored so that file imports as it is.
	importErrorColumn = "error"
	// maxImportRows is how many data rows one import takes: the export's cap
	// (customersFileMaxRows), so a round trip always fits — unless the timing
	// test (csvimport_cap_test.go) says one request cannot carry that many in
	// time, in which case it is the largest round number that can, and
	// docs/customers.md says so.
	maxImportRows = customersFileMaxRows
)

// importBodyLimits raises the router's request-body cap for the import. Every
// other operation of this module keeps the platform default (1 MiB).
var importBodyLimits = map[string]int64{
	"postCustomersImport": maxImportRequestBytes,
}

// The file-level 400: every refusal on "file", under one title.
const (
	invalidImportFileTitle     = "Invalid import file"
	invalidImportUploadMessage = "A customer import is one CSV file of at most 5 MB, sent as the multipart part named 'file'"
)

func invalidImportFile(messages ...string) apicommon.HttpValidationProblemDetails {
	return apicommon.ValidationProblem(invalidImportFileTitle, map[string][]string{"file": messages})
}

// importFilePart reads the one multipart part named "file" — expenses'
// receiptPart for a CSV. A missing part, a second one, an empty one, one past
// maxImportFileBytes and a read that fails (a malformed body, or the router's
// cap firing) are all ok=false.
func importFilePart(mr *multipart.Reader) ([]byte, bool) {
	if mr == nil {
		return nil, false
	}
	var data []byte
	found := false
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, false
		}
		if part.FormName() != importFormField {
			_ = part.Close()
			continue
		}
		if found {
			_ = part.Close()
			return nil, false
		}
		found = true
		data, err = io.ReadAll(io.LimitReader(part, maxImportFileBytes+1))
		_ = part.Close()
		if err != nil {
			return nil, false
		}
	}
	return data, found && len(data) > 0 && len(data) <= maxImportFileBytes
}

// importFlag is one of the two query flags: absent is fallback, and anything
// but the literals 'true' and 'false' is refused in the list's own words.
func importFlag(name string, raw *string, fallback bool) (bool, string) {
	if raw == nil {
		return fallback, ""
	}
	switch *raw {
	case "true":
		return true, ""
	case "false":
		return false, ""
	}
	return fallback, fmt.Sprintf("'%s' must be one of 'true' or 'false', but was '%s'.", name, *raw)
}

// importLayout is a header read against the column table: where each column a
// row is written from sits, which groups the file carries, and how many cells
// a row must have.
type importLayout struct {
	index  map[string]int
	groups map[csvGroup]bool
	width  int
}

func (l importLayout) has(g csvGroup) bool { return l.groups[g] }

// cell is rec's value in column, "" for a column the file does not carry.
func (l importLayout) cell(rec csvRecord, column string) string {
	if i, ok := l.index[column]; ok {
		return rec.Cells[i]
	}
	return ""
}

// position orders a row's errors by where their column sits in the file, a
// row-level error first.
func (l importLayout) position(column string) int {
	if i, ok := l.index[column]; ok {
		return i
	}
	return -1
}

// importLayoutFor reads the header (design D3): every column one the table
// names, the export-only four or the error column, each at most once, matched
// without regard to case; a group's columns all together or not at all; and
// no column the caller could not write by hand. It answers every refusal at
// once, so a file is fixed in one pass rather than one error at a time.
func (s *server) importLayoutFor(ctx context.Context, header []string) (importLayout, []string) {
	if len(header) == 1 && strings.Contains(header[0], ",") {
		return importLayout{}, []string{"The file is comma-separated; the import reads semicolon-separated files, the form the export writes and a Norwegian Excel saves as CSV"}
	}
	known := make(map[string]csvColumn, len(customerCSVColumns))
	for _, c := range customerCSVColumns {
		known[strings.ToLower(c.Name)] = c
	}
	l := importLayout{index: map[string]int{}, groups: map[csvGroup]bool{}, width: len(header)}
	var refusals, unknown []string
	for i, raw := range header {
		name := strings.TrimSpace(raw)
		if name == "" {
			refusals = append(refusals, fmt.Sprintf("Column %d has no name", i+1))
			continue
		}
		if strings.EqualFold(name, importErrorColumn) {
			continue
		}
		c, ok := known[strings.ToLower(name)]
		if !ok {
			unknown = append(unknown, "'"+name+"'")
			continue
		}
		if c.Group == csvGroupExportOnly {
			continue
		}
		if _, repeated := l.index[c.Name]; repeated {
			refusals = append(refusals, fmt.Sprintf("The column %s appears more than once", c.Name))
			continue
		}
		l.index[c.Name] = i
		l.groups[c.Group] = true
	}
	if len(unknown) > 0 {
		refusals = append(refusals, fmt.Sprintf("Unknown columns: %s. A column is one the export and the template carry, and a misspelt one is refused rather than ignored", strings.Join(unknown, ", ")))
	}
	for _, g := range csvImportGroups {
		if !l.groups[g] {
			continue
		}
		var missing []string
		for _, name := range csvColumnsOf(g) {
			if _, ok := l.index[name]; !ok {
				missing = append(missing, name)
			}
		}
		if len(missing) > 0 {
			refusals = append(refusals, fmt.Sprintf("The file carries some of the %s columns but not %s; a group's columns are imported together, all of them or none", g, strings.Join(missing, ", ")))
		}
	}
	if len(l.index) == 0 && len(refusals) == 0 {
		refusals = append(refusals, "The file carries no column an import writes")
	}
	if l.groups[csvGroupIdentity] && !s.hasPermission(ctx, legalIdentityManage) {
		refusals = append(refusals, importPermissionRefusal(csvGroupIdentity, legalIdentityManage))
	}
	if l.groups[csvGroupBilling] && !s.hasPermission(ctx, billingManage) {
		refusals = append(refusals, importPermissionRefusal(csvGroupBilling, billingManage))
	}
	return l, refusals
}

// importPermissionRefusal names a group's columns and the key they need.
func importPermissionRefusal(g csvGroup, key string) string {
	return fmt.Sprintf("The columns %s need the %s permission, which you do not have: remove them from the file, or ask for the permission",
		strings.Join(csvColumnsOf(g), ", "), key)
}

// importVocabulary is the group and tag names a file may use, read once per
// file and matched without regard to case — the vocabularies' own uniqueness
// (lower(name) indexes, migrations 00024 and 00027).
type importVocabulary struct {
	groups map[string]groupSnapshot
	tags   map[string]tagSnapshot
}

func importVocabularyFor(ctx context.Context, q *store.Queries, l importLayout) (importVocabulary, error) {
	vocab := importVocabulary{groups: map[string]groupSnapshot{}, tags: map[string]tagSnapshot{}}
	if l.has(csvGroupMembership) {
		groups, err := q.ListCustomerGroups(ctx)
		if err != nil {
			return importVocabulary{}, fmt.Errorf("customers: read the group vocabulary: %w", err)
		}
		for _, g := range groups {
			vocab.groups[strings.ToLower(g.Name)] = groupSnapshot{GroupID: g.ID, Name: g.Name}
		}
	}
	if l.has(csvGroupTags) {
		tags, err := q.ListCustomerTags(ctx)
		if err != nil {
			return importVocabulary{}, fmt.Errorf("customers: read the tag vocabulary: %w", err)
		}
		for _, t := range tags {
			vocab.tags[strings.ToLower(t.Name)] = tagSnapshot{TagID: t.ID, Name: t.Name}
		}
	}
	return vocab, nil
}

// importError is one entry of CustomerImportResult.errors: Column "" is a
// problem with the row as a whole.
type importError struct {
	Row     int
	Column  string
	Message string
}

// importRowRefusal is a row's errors travelling out of its transaction as the
// error that rolls it back — errDuplicateIdentity's technique, one row wide.
type importRowRefusal struct {
	errs []importError
}

func (r *importRowRefusal) Error() string {
	return fmt.Sprintf("customers: import row %d refused", r.errs[0].Row)
}

func refuseRow(row int, column, message string) error {
	return &importRowRefusal{errs: []importError{{Row: row, Column: column, Message: message}}}
}

// importPlan is one row, validated and in the shapes its write paths take.
// Each group carries a has flag — false is "not in the file, leave it alone" —
// and inside a carried group a nil pointer is "clear it": no identity, no
// primary address of that type, no group.
type importPlan struct {
	Row            int
	CustomerNumber *int64
	HasCore        bool
	Name           string
	Type           string // "" is blank: a create takes business, an update checks nothing
	Status         string // "" is blank: a create takes active, an update keeps its own
	HasIdentity    bool
	Identity       *legalIdentity
	HasContact     bool
	Contact        contactInfo
	HasPostal      bool
	Postal         *validatedAddress
	HasInvoice     bool
	Invoice        *validatedAddress
	HasBilling     bool
	Billing        billingProfile
	HasGroup       bool
	Group          *groupSnapshot
	HasTags        bool
	Tags           []tagSnapshot
}

// identityColumns is validateLegalIdentity's field keys as the file's columns.
var identityColumns = map[string]string{"country": "legalCountry", "type": "legalType", "id": "legalId", "name": "legalName"}

// plan reads one row through the endpoints' own validators, before any
// transaction: everything wrong with the row's cells, each keyed by the column
// it came from and in the file's column order. Only what needs the database —
// the customer a number names, a duplicate identity, the type on file — is
// left for importRow.
func (l importLayout) plan(rec csvRecord, vocab importVocabulary) (importPlan, []importError) {
	p := importPlan{Row: rec.Row}
	if len(rec.Cells) != l.width {
		return p, []importError{{Row: rec.Row, Message: fmt.Sprintf("This row has %d cells, but the header has %d", len(rec.Cells), l.width)}}
	}
	var errs []importError
	fail := func(column, message string) {
		errs = append(errs, importError{Row: rec.Row, Column: column, Message: message})
	}
	failAll := func(columnOf func(string) string, fieldErrs map[string][]string) {
		for field, messages := range fieldErrs {
			for _, m := range messages {
				fail(columnOf(field), m)
			}
		}
	}
	trimmed := func(column string) string { return strings.TrimSpace(l.cell(rec, column)) }
	raw := func(column string) *string { v := l.cell(rec, column); return &v }

	if n := trimmed("customerNumber"); n != "" {
		if v, err := strconv.ParseInt(n, 10, 64); err != nil || v < 1 {
			fail("customerNumber", fmt.Sprintf("A customer number must be a whole number, but was '%s'", n))
		} else {
			p.CustomerNumber = &v
		}
	}

	if _, ok := l.index["name"]; ok {
		p.HasCore = true
		if name, msg := validateFriendlyName(l.cell(rec, "name")); msg != "" {
			fail("name", msg)
		} else {
			p.Name = name
		}
	} else if p.CustomerNumber == nil {
		fail("name", "A new customer needs a name, and this file has no name column")
	}
	if v := trimmed("type"); v != "" {
		if t, msg := validateCustomerType(v); msg != "" {
			fail("type", msg)
		} else {
			p.Type = t
		}
	}
	if v := trimmed("status"); v != "" {
		if st, msg := validateCustomerStatus(v); msg != "" {
			fail("status", msg)
		} else {
			p.Status, p.HasCore = st, true
		}
	}

	if l.has(csvGroupIdentity) {
		p.HasIdentity = true
		country, typ, id, name := trimmed("legalCountry"), trimmed("legalType"), trimmed("legalId"), trimmed("legalName")
		if country != "" || typ != "" || id != "" || name != "" {
			// A file says nothing about where an identity came from: a new one
			// is manual (design D1). importedIdentity keeps a repeated one's.
			identity, idErrs := validateLegalIdentity(country, typ, id, name, "manual")
			failAll(func(field string) string { return identityColumns[field] }, idErrs)
			if idErrs == nil {
				p.Identity = &identity
				if p.CustomerNumber == nil {
					customerType := p.Type
					if customerType == "" {
						customerType = "business"
					}
					if mismatch := identityTypeMismatch(customerType, p.Identity); mismatch != "" {
						fail("legalType", mismatch)
					}
				}
			}
		}
	}

	if l.has(csvGroupContact) {
		p.HasContact = true
		info, ciErrs := validateContactInfo(raw("email"), raw("phone"), raw("website"))
		failAll(func(field string) string { return field }, ciErrs)
		p.Contact = info
	}

	p.HasPostal, p.Postal = l.address(rec, csvGroupPostal, "postal", failAll)
	p.HasInvoice, p.Invoice = l.address(rec, csvGroupInvoice, "invoice", failAll)

	if l.has(csvGroupBilling) {
		p.HasBilling = true
		req := gen.PutCustomerBillingProfileRequest{
			InvoiceEmail: raw("invoiceEmail"), ReminderEmail: raw("reminderEmail"), Currency: raw("currency"),
			Language: raw("language"), InvoiceDelivery: raw("invoiceDelivery"), ReminderDelivery: raw("reminderDelivery"),
			PeppolId: raw("peppolId"), Gln: raw("gln"), BuyerReference: raw("buyerReference"),
		}
		if v := trimmed("paymentTermsDays"); v != "" {
			if n, err := strconv.ParseInt(v, 10, 32); err != nil {
				fail("paymentTermsDays", fmt.Sprintf("Payment terms must be a whole number of days, but was '%s'", v))
			} else {
				days := int32(n)
				req.PaymentTermsDays = &days
			}
		}
		if v := trimmed("defaultBillRate"); v != "" {
			if rate, ok := parseCSVDecimal(v); !ok {
				fail("defaultBillRate", fmt.Sprintf("A default bill rate must be a number, but was '%s'", v))
			} else {
				req.DefaultBillRate = &rate
			}
		}
		profile, bErrs := validateBillingProfile(req)
		failAll(func(field string) string { return field }, bErrs)
		p.Billing = profile
	}

	if l.has(csvGroupMembership) {
		p.HasGroup = true
		if name := trimmed("group"); name != "" {
			if g, ok := vocab.groups[strings.ToLower(name)]; ok {
				p.Group = &g
			} else {
				fail("group", fmt.Sprintf("No customer group is named '%s'", name))
			}
		}
	}

	if l.has(csvGroupTags) {
		p.HasTags = true
		seen := map[uuid.UUID]bool{}
		for _, part := range strings.Split(l.cell(rec, "tags"), csvTagSeparator) {
			name := strings.TrimSpace(part)
			if name == "" {
				continue
			}
			tag, ok := vocab.tags[strings.ToLower(name)]
			if !ok {
				fail("tags", fmt.Sprintf("No tag is named '%s'", name))
				continue
			}
			if !seen[tag.TagID] {
				seen[tag.TagID] = true
				p.Tags = append(p.Tags, tag)
			}
		}
	}

	sort.SliceStable(errs, func(i, j int) bool { return l.position(errs[i].Column) < l.position(errs[j].Column) })
	return p, errs
}

// address reads one address group (design D3): not in the file, left alone;
// every cell blank, "no primary address of this type"; otherwise
// validateAddress's request, its field errors keyed back to the file's columns
// ("line1" → "postalLine1").
func (l importLayout) address(rec csvRecord, g csvGroup, addrType string, failAll func(func(string) string, map[string][]string)) (bool, *validatedAddress) {
	if !l.has(g) {
		return false, nil
	}
	column := func(field string) string { return addrType + strings.ToUpper(field[:1]) + field[1:] }
	value := func(field string) string { return l.cell(rec, column(field)) }
	optional := func(field string) *string { v := value(field); return &v }
	if allBlank([]string{value("line1"), value("line2"), value("postalCode"), value("city"), value("region"), value("country")}) {
		return true, nil
	}
	parsed, errs := validateAddress(gen.CustomerAddressRequest{
		Type: addrType, Line1: value("line1"), Line2: optional("line2"), PostalCode: optional("postalCode"),
		City: optional("city"), Region: optional("region"), Country: value("country"),
	})
	failAll(column, errs)
	if errs != nil {
		return true, nil
	}
	return true, &parsed
}

// importOptions is what every row of one request shares, resolved before the
// first row's transaction opens.
type importOptions struct {
	act                    actor
	allowDuplicateIdentity bool
	nameHolders            bool
}

// importOutcome is what a row that succeeded did.
type importOutcome int

const (
	importCreated importOutcome = iota + 1
	importUpdated
)

// importRow writes one planned row inside the transaction txq belongs to.
// Anything wrong that only the database can tell — an unknown number, a
// duplicate identity, a type change — is an importRowRefusal, which rolls the
// row back; any other error is the request's.
func (s *server) importRow(ctx context.Context, txq *store.Queries, p importPlan, o importOptions) (importOutcome, error) {
	now := s.deps.Clock()
	if p.CustomerNumber == nil {
		id, err := s.importCreate(ctx, txq, p, o, now)
		if err != nil {
			return 0, err
		}
		return importCreated, s.importRelations(ctx, txq, id, p, o, now)
	}
	id, err := s.importUpdate(ctx, txq, p, o, now)
	if err != nil {
		return 0, err
	}
	return importUpdated, s.importRelations(ctx, txq, id, p, o, now)
}

// importCreate is POST /customers for a row: its name, type, status, identity
// and contact info in the create's own insert.
func (s *server) importCreate(ctx context.Context, txq *store.Queries, p importPlan, o importOptions, now time.Time) (int32, error) {
	customerType, status := p.Type, p.Status
	if customerType == "" {
		customerType = "business"
	}
	if status == "" {
		status = "active"
	}
	created, conflict, err := s.insertNewCustomer(ctx, txq,
		newCustomer{Name: p.Name, Status: status, Type: customerType, Identity: p.Identity, Contact: p.Contact},
		p.Identity != nil && !o.allowDuplicateIdentity, o.nameHolders, now, o.act)
	if errors.Is(err, errDuplicateIdentity) {
		return 0, refuseRow(p.Row, "legalId", duplicateIdentityRowMessage(conflict))
	}
	if err != nil {
		return 0, err
	}
	return created.ID, nil
}

// importUpdate is PUT /customers/{id} and PUT …/contact-info for a row, on the
// customer its number names, under that customer's row lock — the lock every
// address and tag write takes first, so nothing a colleague does lands between
// a read here and the write it decides.
func (s *server) importUpdate(ctx context.Context, txq *store.Queries, p importPlan, o importOptions, now time.Time) (int32, error) {
	id, err := txq.CustomerIDByNumber(ctx, *p.CustomerNumber)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, refuseRow(p.Row, "customerNumber", fmt.Sprintf("No customer has number %d", *p.CustomerNumber))
	}
	if err != nil {
		return 0, err
	}
	if _, err := txq.LockCustomer(ctx, id); err != nil {
		return 0, err
	}
	existing, err := txq.GetCustomer(ctx, id)
	if err != nil {
		return 0, err
	}
	if p.Type != "" && p.Type != existing.Type {
		return 0, refuseRow(p.Row, "type", fmt.Sprintf("A customer's type is changed on its own, never by an import; this row says '%s', but customer %d is '%s'", p.Type, existing.CustomerNumber, existing.Type))
	}

	before := customerCoreFrom(existing)
	after := before
	if p.HasCore {
		if p.Name != "" {
			after.Name = p.Name
		}
		if p.Status != "" {
			after.Status = p.Status
		}
	}
	if p.HasIdentity {
		after.Identity = importedIdentity(before.Identity, p.Identity)
		if mismatch := identityTypeMismatch(existing.Type, after.Identity); mismatch != "" {
			return 0, refuseRow(p.Row, "legalType", mismatch)
		}
	}
	if !customerCoreEqual(before, after) {
		duplicateCheck := after.Identity != nil && !identityCountryAndIDEqual(before.Identity, after.Identity) && !o.allowDuplicateIdentity
		if _, conflict, err := s.writeCustomerCore(ctx, txq, id, existing.Type, before, after, nil, duplicateCheck, o.nameHolders, now, o.act); err != nil {
			if errors.Is(err, errDuplicateIdentity) {
				return 0, refuseRow(p.Row, "legalId", duplicateIdentityRowMessage(conflict))
			}
			return 0, err
		}
	}
	if p.HasContact {
		current := contactInfoFromRow(existing.Email, existing.Phone, existing.Website)
		if !contactInfoEqual(current, p.Contact) {
			if _, err := writeContactInfo(ctx, txq, id, current, p.Contact, nil, now, o.act); err != nil {
				return 0, err
			}
		}
	}
	return id, nil
}

// importRelations is everything a create does not insert with the row, and an
// update does after it: the two primary addresses, the billing profile, the
// group and the tags — each read under the transaction, compared, and written
// through its endpoint's function only when it differs, so a row that repeats
// what is on file writes nothing.
func (s *server) importRelations(ctx context.Context, txq *store.Queries, id int32, p importPlan, o importOptions, now time.Time) error {
	if p.HasPostal {
		if err := importPrimaryAddress(ctx, txq, id, "postal", p.Postal, now, o.act); err != nil {
			return addressRefusal(p.Row, "postalLine1", err)
		}
	}
	if p.HasInvoice {
		if err := importPrimaryAddress(ctx, txq, id, "invoice", p.Invoice, now, o.act); err != nil {
			return addressRefusal(p.Row, "invoiceLine1", err)
		}
	}
	if p.HasBilling {
		row, err := txq.GetCustomerBillingProfile(ctx, id)
		if err != nil {
			return err
		}
		rate, err := floatPtrFromNumeric(row.DefaultBillRate)
		if err != nil {
			return err
		}
		current := billingProfileFromRow(row.InvoiceEmail, row.ReminderEmail, row.PaymentTermsDays, row.Currency, row.Language,
			row.InvoiceDelivery, row.ReminderDelivery, row.PeppolID, row.Gln, row.BuyerReference, rate)
		if !billingProfileEqual(current, p.Billing) {
			if _, err := writeBillingProfile(ctx, txq, id, current, p.Billing, nil, now, o.act); err != nil {
				return err
			}
		}
	}
	if p.HasGroup {
		membership, err := txq.CustomerGroupMembership(ctx, id)
		if err != nil {
			return err
		}
		var current *groupSnapshot
		if membership.GroupID != nil {
			current = &groupSnapshot{GroupID: *membership.GroupID, Name: deref(membership.GroupName)}
		}
		if !uuidPtrEqual(groupIDOf(current), groupIDOf(p.Group)) {
			if _, err := writeCustomerGroup(ctx, txq, id, current, p.Group, nil, now, o.act); err != nil {
				if db.IsForeignKeyViolation(err, customersGroupFK) {
					return refuseRow(p.Row, "group", groupNotFound(p.Group.GroupID))
				}
				return err
			}
		}
	}
	if p.HasTags {
		links, err := txq.CustomerTagsForCustomers(ctx, []int32{id})
		if err != nil {
			return err
		}
		current := make([]tagSnapshot, 0, len(links))
		for _, link := range links {
			current = append(current, tagSnapshot{TagID: link.ID, Name: link.Name})
		}
		added, removed := tagSetDiff(current, p.Tags)
		if len(added) > 0 || len(removed) > 0 {
			wanted := make([]uuid.UUID, 0, len(p.Tags))
			for _, tag := range p.Tags {
				wanted = append(wanted, tag.TagID)
			}
			if err := replaceCustomerTags(ctx, txq, id, wanted, added, removed, now, o.act); err != nil {
				if db.IsForeignKeyViolation(err, customerTagsTagFK) {
					return refuseRow(p.Row, "tags", "A tag this row names was deleted while the file was being imported")
				}
				return err
			}
		}
	}
	return nil
}

// importPrimaryAddress makes the customer's primary address of addrType the
// row's (design D3): none on file and none in the row, nothing; none on file,
// insertAddress it as primary; one on file and none in the row, removeAddress
// it — the oldest remaining of the type becomes primary, as a delete by hand
// does; one on file and one in the row, replaceAddress it unless it already
// says the same. The file has no label column, so the address keeps its own.
func importPrimaryAddress(ctx context.Context, txq *store.Queries, customerID int32, addrType string, after *validatedAddress, now time.Time, act actor) error {
	current, err := txq.PrimaryCustomerAddressOfType(ctx, store.PrimaryCustomerAddressOfTypeParams{CustomerID: customerID, Type: addrType})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		if after == nil {
			return nil
		}
		_, err := insertAddress(ctx, txq, customerID, *after, true, now, act)
		return err
	case err != nil:
		return err
	case after == nil:
		return removeAddress(ctx, txq, customerID, current, now, act)
	}
	next := *after
	next.Label = current.Label
	if addressSnapshotEqual(addressSnapshotFromRow(current), addressSnapshot{
		Type: next.Type, Label: next.Label, Line1: next.Line1, Line2: next.Line2, PostalCode: next.PostalCode,
		City: next.City, Region: next.Region, Country: next.Country, IsPrimary: true,
	}) {
		return nil
	}
	_, err = replaceAddress(ctx, txq, customerID, current, next, true, now, act)
	return err
}

// addressRefusal is the address cap as the row's error, on the group's first
// column; anything else passes through.
func addressRefusal(row int, column string, err error) error {
	if errors.Is(err, errAddressCapReached) {
		return refuseRow(row, column, addressCapMessage)
	}
	return err
}

// addressSnapshotEqual is two addresses' every field, nil included.
func addressSnapshotEqual(a, b addressSnapshot) bool {
	return a.Type == b.Type && stringPtrEqual(a.Label, b.Label) && a.Line1 == b.Line1 && stringPtrEqual(a.Line2, b.Line2) &&
		stringPtrEqual(a.PostalCode, b.PostalCode) && stringPtrEqual(a.City, b.City) && stringPtrEqual(a.Region, b.Region) &&
		a.Country == b.Country && a.IsPrimary == b.IsPrimary
}

// groupIDOf is a snapshot's group id, nil for no group.
func groupIDOf(g *groupSnapshot) *uuid.UUID {
	if g == nil {
		return nil
	}
	return &g.GroupID
}

// customerCoreFrom is a persisted row's name, status and identity.
func customerCoreFrom(c store.GetCustomerRow) customerCore {
	return customerCore{Name: c.Name, Status: c.Status, Identity: identityFromRow(c.LegalCountry, c.LegalID, c.LegalName, c.LegalSource, c.LegalType)}
}

// customerCoreEqual is the no-op rule (customers foundation design D5) for
// the three: the same name, status and identity, all five identity fields.
func customerCoreEqual(a, b customerCore) bool {
	return a.Name == b.Name && a.Status == b.Status && identityEqual(a.Identity, b.Identity)
}

// importedIdentity is the identity a row asks for. A new one is manual
// (design D1); a row repeating the identity on file in all four of the file's
// fields keeps the identity on file, source included, so re-importing an
// export never turns a Brreg pick into a manual entry.
func importedIdentity(current, row *legalIdentity) *legalIdentity {
	if row == nil || current == nil {
		return row
	}
	if current.Country == row.Country && current.Type == row.Type && current.ID == row.ID && current.Name == row.Name {
		return current
	}
	return row
}

// duplicateIdentityRowMessage is the create endpoint's 409 as a row's error:
// its detail, the holders when the caller may be told them, and the way past.
func duplicateIdentityRowMessage(problem *gen.CustomerConflictProblem) string {
	message := strings.TrimSuffix(duplicateIdentityDetail, ".")
	if problem != nil && problem.Duplicates != nil {
		holders := make([]string, 0, len(*problem.Duplicates))
		for _, d := range *problem.Duplicates {
			holders = append(holders, fmt.Sprintf("%d %s", d.CustomerNumber, d.Name))
		}
		message += " (" + strings.Join(holders, ", ") + ")"
	}
	return message + ". Import with allowDuplicateIdentity=true to keep both."
}

// importTally is what the rows did.
type importTally struct {
	rows, created, updated, failed int
	errs                           []importError
}

func (t importTally) result(dryRun bool) gen.CustomerImportResult {
	errs := make([]gen.CustomerImportError, 0, len(t.errs))
	for _, e := range t.errs {
		item := gen.CustomerImportError{Row: int32(e.Row), Message: e.Message}
		if e.Column != "" {
			column := e.Column
			item.Column = &column
		}
		errs = append(errs, item)
	}
	return gen.CustomerImportResult{
		DryRun: dryRun, Rows: int32(t.rows), Created: int32(t.created), Updated: int32(t.updated),
		Failed: int32(t.failed), Errors: errs,
	}
}

// importRows runs every row in file order. Every row is planned first — its
// cells validated, no database — and then the file's own rows are checked
// against each other (inFileDuplicates) before any row is applied, so a dry
// run, whose rows cannot see each other's writes, still refuses what the real
// run would. A refusal is that row's failure and the next row runs; any other
// error ends the request.
func importRows(file csvFile, l importLayout, vocab importVocabulary, allowDuplicateIdentity bool, apply func(importPlan) (importOutcome, error)) (importTally, error) {
	tally := importTally{rows: len(file.Rows), errs: []importError{}}
	plans := make([]importPlan, len(file.Rows))
	planErrs := make([][]importError, len(file.Rows))
	for i, rec := range file.Rows {
		plans[i], planErrs[i] = l.plan(rec, vocab)
	}
	if !allowDuplicateIdentity {
		inFileDuplicates(plans, planErrs)
	}
	for i, p := range plans {
		errs := planErrs[i]
		if len(errs) == 0 {
			outcome, err := apply(p)
			var refused *importRowRefusal
			switch {
			case errors.As(err, &refused):
				errs = refused.errs
			case err != nil:
				return importTally{}, err
			case outcome == importCreated:
				tally.created++
			default:
				tally.updated++
			}
		}
		if len(errs) > 0 {
			tally.failed++
			tally.errs = append(tally.errs, errs...)
		}
	}
	return tally, nil
}

// inFileDuplicates is the duplicate-legal-identity guard (customers foundation
// design D6) within one file (customers import/export design D3): of two rows
// that create customers with the same identity — country and id, the guard's
// own comparison — the second is refused on legalId, in both runs alike. The
// database check cannot see it in a dry run, whose rows each roll back before
// the next one runs; this check needs no database, so the two runs agree. A
// row whose cells already failed takes no identity for itself.
func inFileDuplicates(plans []importPlan, planErrs [][]importError) {
	first := map[string]int{}
	for i, p := range plans {
		if p.CustomerNumber != nil || p.Identity == nil || len(planErrs[i]) > 0 {
			continue
		}
		key := p.Identity.Country + "\x00" + p.Identity.ID
		if row, seen := first[key]; seen {
			planErrs[i] = []importError{{Row: p.Row, Column: "legalId", Message: fmt.Sprintf(
				"Row %d of this file already creates a customer with this legal identity. Import with allowDuplicateIdentity=true to keep both.", row)}}
			continue
		}
		first[key] = p.Row
	}
}

// errImportDryRun rolls a dry-run row's transaction back once the row ran.
var errImportDryRun = errors.New("customers: import dry run rolled back")

// importFile runs the file a transaction per row — the real run and the dry
// run alike, the dry run's rolled back at its end instead of committed, so a
// checked row takes the very statements, locks and events an imported one
// does, and keeps none of them: the customer-number counter's increment rolls
// back with the rest, and no lock outlives its row. Each row is retried on the
// deadlock a tag replace can lose to a tag delete (tagWriteAttempts, tags.go),
// the tags PUT's own retry, in both runs.
func (s *server) importFile(ctx context.Context, file csvFile, l importLayout, vocab importVocabulary, o importOptions, dryRun bool) (importTally, error) {
	return importRows(file, l, vocab, o.allowDuplicateIdentity, func(p importPlan) (importOutcome, error) {
		var outcome importOutcome
		err := db.RetrySerializable(ctx, tagWriteAttempts, func() error {
			return db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
				var err error
				outcome, err = s.importRow(ctx, store.New(tx), p, o)
				if err == nil && dryRun {
					return errImportDryRun
				}
				return err
			})
		})
		if errors.Is(err, errImportDryRun) {
			return outcome, nil
		}
		return outcome, err
	})
}

// PostCustomersImport Import customers from CSV
// (POST /api/v1/customers/import)
//
// The order of refusals is the order that costs least: the flags, the part,
// the file's bytes, then its header against the table and the caller — every
// one a 400 before a single row is read — and only then the rows, whose
// problems are the result rather than a refusal.
func (s *server) PostCustomersImport(ctx context.Context, req gen.PostCustomersImportRequestObject) (gen.PostCustomersImportResponseObject, error) {
	dryRun, dryRunMsg := importFlag("dryRun", req.Params.DryRun, true)
	allowDuplicateIdentity, allowMsg := importFlag("allowDuplicateIdentity", req.Params.AllowDuplicateIdentity, false)
	if paramErrs := nonEmptyMessages(map[string]string{"dryRun": dryRunMsg, "allowDuplicateIdentity": allowMsg}); len(paramErrs) > 0 {
		return gen.PostCustomersImport400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem("Invalid query parameters", paramErrs)), nil
	}

	data, ok := importFilePart(req.Body)
	if !ok {
		return gen.PostCustomersImport400ApplicationProblemPlusJSONResponse(invalidImportFile(invalidImportUploadMessage)), nil
	}
	file, refusal := readCSVFile(data, maxImportRows)
	if refusal != "" {
		return gen.PostCustomersImport400ApplicationProblemPlusJSONResponse(invalidImportFile(refusal)), nil
	}
	layout, refusals := s.importLayoutFor(ctx, file.Header)
	if len(refusals) > 0 {
		return gen.PostCustomersImport400ApplicationProblemPlusJSONResponse(invalidImportFile(refusals...)), nil
	}

	vocab, err := importVocabularyFor(ctx, store.New(s.deps.Pool), layout)
	if err != nil {
		return nil, err
	}
	tally := importTally{errs: []importError{}}
	if len(file.Rows) > 0 {
		// Resolved once, before any row's transaction, and only when there is a
		// row to write (customers foundation design D1, actor.go).
		act, err := s.actorFor(ctx, generatedFallbackActor)
		if err != nil {
			return nil, fmt.Errorf("customers: resolve actor: %w", err)
		}
		o := importOptions{
			act:                    act,
			allowDuplicateIdentity: allowDuplicateIdentity,
			// Whether a duplicate may name its holder (duplicates.go). The router
			// admits an import only with customers:view (the rule in
			// customers.yaml), so the answer is yes whenever a row could raise it.
			nameHolders: layout.has(csvGroupIdentity) && !allowDuplicateIdentity,
		}
		tally, err = s.importFile(ctx, file, layout, vocab, o, dryRun)
		if err != nil {
			return nil, fmt.Errorf("customers: import customers: %w", err)
		}
	}
	return gen.PostCustomersImport200JSONResponse(tally.result(dryRun)), nil
}

// nonEmptyMessages is a field error map of the messages that were set.
func nonEmptyMessages(messages map[string]string) map[string][]string {
	errs := map[string][]string{}
	for field, message := range messages {
		if message != "" {
			errs[field] = []string{message}
		}
	}
	return errs
}
```

Two details the code leaves to be checked against what exists: `stringPtrEqual` lives in `timeline.go` and `uuidPtrEqual` in `owner.go` (both package-level already); if `db.RetrySerializable` wraps the error it returns, `errors.As` still finds the refusal (it only wraps with `%w`, if at all).

In `module.go`, add `BodyLimits: importBodyLimits,` to `mount`'s `module.RouterOptions` literal (after `Catalog: d.Catalog,`), and replace `mount`'s doc comment with:

```go
// mount registers every contract operation on the platform router, which wraps
// each in its access rule and request-body cap before the generated wrapper
// decodes it. Every body is capped at the router's default
// (module.DefaultMaxBodyBytes) except the CSV import's, which gets
// maxImportRequestBytes (importBodyLimits, import.go). It fails when the
// router reports a problem: an operation never registered, a rule that does
// not parse, a permission missing from the catalog, or a BodyLimits entry
// naming no operation.
```

- [ ] **Step 6: Run everything, show the tests can fail, commit**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'
mise exec -- gofmt -w internal/customers/import.go internal/customers/csvimport_test.go internal/customers/csvimport_cap_test.go \
  internal/customers/server.go internal/customers/module.go && mise exec -- gofmt -l internal
mise exec -- go vet ./internal/customers/... && mise exec -- go test -count=1 ./internal/customers/ ./internal/openapi/... ./internal/module/...
taskset -c 0-3 mise exec -- go test -count=1 -v -run TestPostCustomersImport_AtTheCap ./internal/customers/ 2>&1 | grep -E "5000 rows|PASS|FAIL"
mise exec -- go run ./internal/openapi/cmd/contract coverage -corpus ../../openapi/testdata/exchanges -out ../../openapi/COVERAGE.md
cd /home/anders/projects/vantigo/vantigo && mise exec -- bun run gen:client && git status --short
```
Expected: PASS; the frozen customers corpus still validates. `COVERAGE.md` gains `postCustomersImport`; `gen:client` changes `api-schema.d.ts`.

**The timing rule.** The cap test runs on 4 CPUs (`taskset -c 0-3`, the CI runner's count) and logs the total for 5000 rows — **write it down**, Task 5 quotes it. If that total is more than 60 s (the test then fails on `importAtTheCapBudget`), do not raise the budget: lower `maxImportRows` in `import.go` to the largest round number (1000, 2000, 2500, …) whose share of the measured time — measured ÷ 5000 × n — is about 30 s or less, and then change, to that number: the `"more than 5000 rows"` case in `TestPostCustomersImport_RefusesAFileItCannotRead` (build `maxImportRows + 1` rows and expect `"The file holds more than <n> rows"`), the yaml description's "At most 5000 data rows", Task 5's `<MAX>`, and the PR body. Keep the cap test's own 5000 rows (it measures the 5000-row time either way). Record the measured time and the chosen number in the report. Near the budget but under it (over 45 s) is not a pass to keep quiet about: say so in the report.

Prove each new test can fail, restoring after each: in `importLayoutFor` delete the `billingManage` check — the permission test goes red on its 200; set the yaml's `x-vantigo-access` back to `permission:customers:create+customers:update` (and regenerate) — the permission test goes red on the create+update caller's 200; in `importFlag`'s callers pass `false` as `dryRun`'s fallback — the dry-run test goes red (the dry run writes); in `importFile` change `if err == nil && dryRun {` to `if false {` — the dry-run test goes red (it writes); delete the `inFileDuplicates(plans, planErrs)` call — the two-rows test goes red on the dry run creating 2 (the real run still refuses row 2 through the database: say what each run printed); in `importedIdentity` `return row` unconditionally — the repeated-source test goes red on `manual` (and the round trip on its event count); in `importPrimaryAddress` drop `next.Label = current.Label` — the round trip goes red; in `plan` drop `strings.ToLower` on the group lookup — the vocabulary test goes red on `retail`; delete the `len(rec.Cells) != l.width` check — the row-error test panics or goes red on row 4; set `importAtTheCapBudget` to `time.Millisecond` — the cap test goes red. Say what each printed.

```bash
cd /home/anders/projects/vantigo/vantigo
cat > /tmp/claude-1000/msg-import-export-4.txt <<'EOF'
feat(customers): customers can be created and updated from a CSV file, checked first

POST /customers/import reads the customers file back: a row with a
customer number updates that customer, a row without one creates one,
and each group of columns in the file is written as its own endpoint
writes it — whole, a blank cell clearing — while a group not in the file
is left alone. Group and tags name existing vocabulary, case-insensitively.
There is no import key: the router wants create, update and view, and a file
carrying a column its sender could not write by hand — the legal
identity's without legal-identity-manage, the billing profile's without
billing-manage — is refused whole, as is an unknown column or half a
group. Each row is its own transaction through the endpoints' own write
functions, events and all, and a failing row never stops the file.
dryRun defaults to true: every row runs as in the real run, in its own
transaction, rolled back instead of committed, so nothing outlives a row;
two rows of the file creating one legal identity are refused on the
second in both runs. The operation wants view as well as create and
update, since every write it stands in for does. 5000 rows (or the cap
the timing rule set) and 5 MB at most.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
PATHS="apps/server/internal/customers/import.go apps/server/internal/customers/csvimport_test.go \
 apps/server/internal/customers/csvimport_cap_test.go apps/server/internal/customers/server.go \
 apps/server/internal/customers/module.go apps/server/internal/customers/queries/customers.sql \
 apps/server/internal/customers/queries/groups.sql apps/server/internal/customers/store/customers.sql.go \
 apps/server/internal/customers/store/groups.sql.go openapi/customers.yaml \
 apps/server/internal/openapi/specs/customers.yaml apps/server/internal/customers/gen/api.gen.go \
 apps/customers/frontend/src/api-schema.d.ts openapi/COVERAGE.md"
git add $PATHS && git commit -F /tmp/claude-1000/msg-import-export-4.txt -- $PATHS
git show --stat HEAD && git status --short
```

---

### Task 5: The docs (D5)

**Files:**
- Modify: `docs/customers.md`, `ROADMAP.md`; commit `docs/superpowers/specs/2026-09-24-customers-import-export-design.md`, whose D3 the planner already amended in the working tree for the two rulings (dry run per row + in-file check; `customers:view`) — read it, do not rewrite it

- [ ] **Step 1: The section**

In `docs/customers.md`, directly above `## Revision and concurrency`, insert the section below. It carries three placeholders to fill from Task 4 Step 6 before committing: `<MAX>` (the `maxImportRows` the timing rule left), `<REAL>` (the cap test's logged total, rounded to the second) and `<machine>` (where it ran — the CI-like `taskset -c 0-3` run); no placeholder may be left in the committed file:

```markdown
## CSV import and export

Customers leave and arrive as one file (phase 6 delivery A, decided in
[`docs/superpowers/specs/2026-09-24-customers-import-export-design.md`](superpowers/specs/2026-09-24-customers-import-export-design.md)).
Onboarding from Tripletex, Fiken or PowerOffice is "export there, rename the
columns, import here": no competitor's layout is documented anywhere, and one
honest format beats three guessed ones.

### The file

Semicolon-separated, UTF-8 with a byte order mark, CRLF line ends, a header row,
RFC 4180 quoting, the decimal comma for money (either mark is read back, digits
only), ISO dates — `createdAt` and `updatedAt` are UTC, RFC 3339 — and every cell
beginning with `=`, `+`, `-`, `@`, a tab or a carriage return prefixed with an
apostrophe, which the import takes off again. It is the expenses payroll file's
form verbatim, the one a Norwegian Excel opens without an import dialog. The
headers are the API's JSON names, so the field tables above describe the file:

| Group | Columns | The import writes it through |
| --- | --- | --- |
| Row | `customerNumber` (blank creates), `name`, `type`, `status` | the create, or `PUT /{id}` |
| Legal identity | `legalCountry`, `legalType`, `legalId`, `legalName` | `PUT /{id}/legal-identity`'s rules; source `manual` |
| Contact info | `email`, `phone`, `website` | `PUT /{id}/contact-info`'s rules |
| Postal address | `postalLine1`, `postalLine2`, `postalPostalCode`, `postalCity`, `postalRegion`, `postalCountry` | the primary `postal` address |
| Invoice address | `invoiceLine1` … `invoiceCountry` | the primary `invoice` address |
| Billing profile | `invoiceEmail`, `reminderEmail`, `paymentTermsDays`, `currency`, `language`, `invoiceDelivery`, `reminderDelivery`, `peppolId`, `gln`, `buyerReference`, `defaultBillRate` | `PUT /{id}/billing-profile`'s rules |
| Relationship | `group` (its name), `tags` (names joined by `\|`) | `PUT /{id}/group`, `PUT /{id}/tags` |
| Export only | `id`, `ownerName`, `createdAt`, `updatedAt` | ignored |

Delivery and visiting addresses, contacts, the timeline and the owner are not in
the file — an owner is a user of this installation, which no file can name
safely. A tag whose name contains `|` cannot be named by a file. Two edge cases of
the format: a number cell of one `.` followed by exactly three digits (`1.250`) is
refused rather than guessed at — it reads as thousands to one person and as a
decimal to another — and a value that itself begins with an apostrophe followed by
one of the guarded characters (a name stored as `'=x`) comes back from a round trip
without that apostrophe, because the import cannot tell it from the guard's.

### Export

`GET /customers/export` is the list as the caller sees it: the list's own filters
(`search`, `status`, `type`, `ownerId`, `tagId`, `groupId`, `includeArchived`) and
sort, resolved by the very function the list uses, one row per customer, named
`customers-YYYY-MM-DD.csv` (UTC) and never cached. The legal identity's four columns
are **absent** — not blank — without `customers:legal-identity-view`, so such a file
re-imports without touching an identity; the billing columns are there for every
viewer, as the billing profile's own GET is. More than **5000** customers is a 400
asking for a narrower filter: a portfolio that size is exported in slices. Owners,
tags, groups, billing profiles and addresses are read in bulk — a fixed number of
queries whatever the row count. `GET /customers/import/template` answers the header
alone, every importable column. A viewer's own export carries the billing columns,
so someone without `customers:billing-manage` re-importing it removes those eleven
columns first — the import refuses a column its sender could not write.

### Import

`POST /customers/import` takes one multipart part named `file` — at most 5 MB and
**`<MAX>`** data rows. (Fill `<MAX>` from Task 4 Step 6: 5000, the export's cap, so a
round trip always fits — or the lower number the timing rule set, in which case add:
"fewer than the export's 5000, because one request must finish well inside the 100
seconds after which the proxy in front of a hosted installation gives up; a larger
export is imported in slices".)

**A file may only say what its sender could say by hand.** There is no import key:
the operation wants `customers:create`, `customers:update` and `customers:view` —
view because every write the import stands in for needs it by hand, and because a
row's errors would otherwise describe customers the caller may not see — and the header is
checked against the caller before a row is read — the legal identity's columns need
`customers:legal-identity-manage`, the billing profile's `customers:billing-manage`.
A column its sender may not write refuses the **whole file** (a 400 naming the
columns and the key), because a skipped column is a change the sender believes was
made. So does an unknown column — a misspelt header is never silently ignored — a
repeated one, and **part of a group**: the legal identity's four, contact info's
three, an address's six and the billing profile's eleven come together or not at
all. `name`, `type` and `status` are each optional. The export-only four and a
column named `error` are ignored.

**Matching.** A row with a `customerNumber` updates that customer (an unknown number
is that row's error; no revision is sent, so the change applies regardless); a
blank one creates. A create needs a name and defaults to `business` and `active`.
On an update, an absent `name` column keeps the name, but a blank `name` cell is
that row's error — a name is never cleared; a blank or absent `status` keeps the
status; and a `type` that differs from the customer's is an error — changing it is `PUT
/{id}/type`'s deliberate act.

**A group in the file is replaced whole; a group not in the file is left alone.**
Inside a group, a blank cell clears that field — all of an address's cells blank
remove the primary address of that type (the oldest remaining becomes primary, as
a delete by hand does), a blank `tags` cell clears the tags. The primary address
keeps its label, which the file does not carry. `group` and `tags` name existing
vocabulary, case-insensitively; an unknown name is that row's error, never a word
created behind anybody's back. A new legal identity is `manual`; a row repeating
the identity on file keeps its source, so re-importing an export never turns a
Brreg pick into a manual entry. `allowDuplicateIdentity=true` is the create
endpoint's own flag, for every row.

**Each row is its own transaction** through the endpoints' own write functions —
the same validation, the same guarded statements, the same events, the importer as
actor — so a row that fails leaves nothing half-written, and a row that succeeds is
indistinguishable from the same edits made by hand. A row that changes nothing
writes nothing. Rows run in file order; a failing row never stops the file. There
is no batch marker and no `customer.imported` event: the granular events are the
audit trail.

**The dry run.** `dryRun` defaults to `true`: every row runs exactly as in the real
run — each in its own transaction — and is rolled back instead of committed.
Nothing is kept: no customer, no customer number, no event, and no lock outlives its
row, so a check holds nobody up. Because each row rolls back before the next runs,
a dry run cannot see an earlier row's effect on a later one. The case a file makes
likely — two rows creating customers with the same legal identity — is checked on
the file itself before any row runs, so the second is refused in both runs alike
(unless `allowDuplicateIdentity`). Any other dependency between rows (a row naming
a customer an earlier row changed, say) may check clean and still be refused by the
real run — cleanly, as that row's error, with the other rows imported. At the cap
(5000 creates, each with contact info, a postal address, a billing profile and a
tag) a real run took `<REAL>` on `<machine>`; a dry run costs the same. (Fill
`<REAL>` and `<machine>` from the timing test's log line in Task 4 Step 6.) The
ceiling that matters is the proxy in front of a hosted installation, which gives up
after about 100 seconds while the rows go on committing.

**The result** is `CustomerImportResult` — `dryRun`, `rows`, `created`, `updated`
(an unchanged update counts), `failed`, and `errors[]` of `{row, column?,
message}`: `row` is the 1-based data row (the header is row 0; an all-blank row is
skipped and takes no number), `column` the header of a field's error, `message` the
module's own validation wording. File-level refusals are 400s on `file`, never a
result. The frontend builds the **failed-rows file** from these errors and the file
the browser still holds: the original rows that failed, as they were, with an
`error` column appended — fix them and import that file.
```

- [ ] **Step 2: The rest of D5**

In `## Permissions`, after the paragraph ending `…(`customers:billing-manage` as well on default-bearing group writes and on moves into or out of a group that carries a default).`, add:

```markdown

The [CSV import](#csv-import-and-export) adds no key either. It wants
`customers:create`, `customers:update` and `customers:view` together — view because
every write it stands in for needs view by hand — and checks the file's columns
against the caller's keys before reading a row — the legal identity's against
`customers:legal-identity-manage`, the billing profile's against
`customers:billing-manage` — refusing the whole file for a column its sender could
not write by hand. The export needs `customers:view`, and shapes its columns by
`customers:legal-identity-view` the way a customer response does.
```

In `## The frontend`, at the end of the **List** bullet (after `…says may edit.`), add:

```markdown
  [CSV import and export](#csv-import-and-export) added **Export**, which downloads
  the list's current filter and sort as the customers file (the reimbursements
  download: a plain `fetch`, the server's filename, an object URL), and **Import**,
  which opens a modal: pick a file (drop or choose, with a **Download template**
  link), **Check** — the dry run's counts and an errors table of row, column and
  problem — then **Import**, enabled once the check found a row that would succeed,
  and afterwards the counts again and, when rows failed, **Download failed rows**:
  the original rows with an `error` column, built in the browser from the file it
  still holds. The list refreshes when an import completes. The host passes two more
  props: `canExport` (`customers:view`) and `canImport` (`customers:create`,
  `customers:update` and `customers:view` — the import operation's own rule).
```

In `## API`, change `56 operations in total` to `59 operations in total`, and add after the `GET /`, `GET /{id}` row:

```markdown
| `GET /export`, `GET /import/template` | `customers:view` (the export's legal-identity columns only with `customers:legal-identity-view`) |
| `POST /import` | `customers:create` + `customers:update` + `customers:view` (plus `customers:legal-identity-manage` for a file with the legal-identity columns, `customers:billing-manage` for one with the billing columns) |
```

In `## What comes next`, directly above `Past that, the remaining gaps are exactly`, add:

```markdown
**Phase 6 delivery A** — [CSV import and export](#csv-import-and-export) — has
landed, decided in
[`docs/superpowers/specs/2026-09-24-customers-import-export-design.md`](superpowers/specs/2026-09-24-customers-import-export-design.md):
one canonical file (the payroll export's form, the API's JSON names), the list
exported as the caller sees it and capped at 5000 rows, and an import that creates
and updates by `customerNumber` through the endpoints' own write paths, a group at a
time, with a dry run by default and the failed rows handed back for a re-run. No
permission key, no event type and no migration were added. Still ahead in phase 6:
merging duplicate customers (delivery B) and GDPR handling for person customers
(delivery C).
```

and in the paragraph below it change `` `ContactsByEmail` still unused in production, no CSV import/export, no merge `` / `(phase 6)` to `` `ContactsByEmail` still unused in production, no merge and no GDPR handling `` / `(phase 6 deliveries B and C)`.

In `ROADMAP.md`, `### Phase 6 — Data operations and compliance`, after its first paragraph (ending `…never a fødselsnummer field.`), add:

```markdown

**Delivery A (done)** — decided in
[`docs/superpowers/specs/2026-09-24-customers-import-export-design.md`](docs/superpowers/specs/2026-09-24-customers-import-export-design.md):
CSV export of the list as the caller sees it and CSV import that creates and updates
through the endpoints' own write paths, with a dry run and a failed-rows file for the
re-run; one canonical format, no import key. See
[`docs/customers.md#csv-import-and-export`](docs/customers.md#csv-import-and-export).

**Still ahead in this phase:** merging duplicate customers (delivery B) and GDPR
handling for person customers (delivery C).
```

- [ ] **Step 3: Check the docs against the code, commit**

Read the new section against `csvfile.go`, `csvexport.go` and `import.go`: every message quoted, the column order, the caps, the ignored columns and the permission rule must match the code; the API table's paths against `openapi/customers.yaml`; the count 59 against `grep -c "operationId:" openapi/customers.yaml`.

```bash
cd /home/anders/projects/vantigo/vantigo
grep -c "operationId:" openapi/customers.yaml
cat > /tmp/claude-1000/msg-import-export-5.txt <<'EOF'
docs(customers): the CSV import and export, and phase 6 delivery A

docs/customers.md gains the CSV import and export section — the format,
the column table, the export's permission shape and cap, the import's
permission rule, matching, whole-group replacement, the dry run, the
caps and what the timing test measured, the result and the failed-rows
file — plus the permission note that import adds no key, the API rows,
the list page's two buttons and the phase 6 delivery A paragraph. The
roadmap marks delivery A done, with merge and GDPR ahead. The design's
D3 records two rulings made after it was written: the dry run is the
real run rolled back row by row, with an in-file duplicate-identity
check, and the import wants customers:view as well.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
PATHS="docs/customers.md ROADMAP.md docs/superpowers/specs/2026-09-24-customers-import-export-design.md"
git add $PATHS && git commit -F /tmp/claude-1000/msg-import-export-5.txt -- $PATHS
git show --stat HEAD && git status --short
```

---

### Task 6: The frontend (D4)

**Files:**
- Create: `apps/customers/frontend/src/lib/csv.ts`, `lib/csv.test.ts`, `api/import-export.ts`, `api/import-export.test.ts`, `pages/-customer-import-modal.tsx`, `pages/-customer-import-modal.test.tsx`
- Modify: `apps/customers/frontend/src/api/customers.ts`, `api/request.ts`, `pages/customers.index.tsx`, `pages/-customers.index.test.tsx`, `i18n.ts`, `apps/host/frontend/src/routes/customers/-customers-list.tsx`, `apps/host/frontend/src/routes/customers/customers-list-route.test.tsx`
- Read first (do not change): `apps/expenses/frontend/src/api/reimbursements.ts:69-160`, `apps/expenses/frontend/src/api/reimbursements.test.ts:1-60`, `apps/expenses/frontend/src/components/receipt-dropzone.tsx`, `apps/customers/frontend/src/pages/-manage-groups-modal.tsx` (a modal's shape here)

**Interfaces:**
- Produces TS: `customersSearchParams(params: CustomersQueryParams): string` (api/customers.ts); `setUnauthorizedHandler`, `setAuthStateClearer` (now wrappers) and `handleUnauthorized()` (api/request.ts); `CsvDownload`, `CustomerImportError {row; column: string | null; message}`, `CustomerImportResult`, `downloadCustomersCsv(params)`, `downloadImportTemplate()`, `saveCsv(download)`, `importCustomers(file, {dryRun, allowDuplicateIdentity})` (api/import-export.ts); `CsvRecord`, `CsvTable`, `parseCsv(text)`, `csvCell(value)`, `toCsv(rows)`, `failedRowsCsv(table, errors)` (lib/csv.ts); `CustomerImportModal({opened, onClose})`; `CustomersPage` props `canExport?`, `canImport?`.
- Wire: consumes Tasks 2 and 4.

- [ ] **Step 1: Write the failing tests**

Create `apps/customers/frontend/src/lib/csv.test.ts`:

```ts
import { describe, expect, it } from "vitest";
import { csvCell, failedRowsCsv, parseCsv } from "./csv";

describe("parseCsv", () => {
  it("reads the export's form — BOM, semicolons, CRLF, quotes — and numbers rows as the server does", () => {
    const table = parseCsv('\ufeffname;phone\r\n"Fjord; ""Nord"" AS";\'+47 22\r\n;\r\n"to\r\nlinjer";1\r\n');
    expect(table.header).toEqual(["name", "phone"]);
    // The all-blank record takes no number, exactly as readCSVFile skips it.
    expect(table.records).toEqual([
      { row: 1, cells: ['Fjord; "Nord" AS', "'+47 22"] },
      { row: 2, cells: ["to\r\nlinjer", "1"] },
    ]);
  });

  it("takes LF, no BOM, and skips empty lines", () => {
    expect(parseCsv("name\nA\n\nB").records).toEqual([
      { row: 1, cells: ["A"] },
      { row: 2, cells: ["B"] },
    ]);
  });
});

describe("csvCell", () => {
  it("guards a formula and quotes what would end the cell", () => {
    expect(csvCell("=SUM(A1)")).toBe("'=SUM(A1)");
    expect(csvCell("a;b")).toBe('"a;b"');
    expect(csvCell('say "hi"')).toBe('"say ""hi"""');
    expect(csvCell("'+47 22")).toBe("'+47 22");
    expect(csvCell("Fjord AS")).toBe("Fjord AS");
  });
});

describe("failedRowsCsv", () => {
  const table = parseCsv("\ufeffname;email\r\nOk AS;ok@x.no\r\nFeil AS;nei\r\nKort AS\r\n");

  it("keeps only the rows that failed, as they were, with an error column appended", () => {
    const csv = failedRowsCsv(table, [
      { row: 2, column: "email", message: "An email address must look like name@example.com, but was 'nei'" },
      { row: 3, column: null, message: "This row has 1 cells, but the header has 2" },
      { row: 2, column: "name", message: "Second problem" },
    ]);
    expect(csv).toBe(
      "\ufeffname;email;error\r\n" +
        "Feil AS;nei;email: An email address must look like name@example.com, but was 'nei' | name: Second problem\r\n" +
        "Kort AS;;This row has 1 cells, but the header has 2\r\n",
    );
  });

  it("overwrites the error column of a failed-rows file being re-run rather than adding a second", () => {
    const rerun = parseCsv("name;error\r\nFeil AS;old problem\r\n");
    expect(failedRowsCsv(rerun, [{ row: 1, column: "name", message: "new problem" }])).toBe(
      "\ufeffname;error\r\nFeil AS;name: new problem\r\n",
    );
  });
});
```

Create `apps/customers/frontend/src/api/import-export.test.ts`:

```ts
import { afterEach, describe, expect, it, vi } from "vitest";
import { downloadCustomersCsv, importCustomers } from "./import-export";
import { setAuthStateClearer, setUnauthorizedHandler } from "./request";

afterEach(() => {
  vi.unstubAllGlobals();
  setUnauthorizedHandler(undefined);
  setAuthStateClearer(undefined);
});

describe("downloadCustomersCsv", () => {
  it("asks for the list's filters and sort without its paging, and keeps the server's file name", async () => {
    const fetchMock = vi.fn(
      async () =>
        new Response("\ufeffname\r\n", {
          status: 200,
          headers: { "Content-Disposition": 'attachment; filename="customers-2026-09-24.csv"' },
        }),
    );
    vi.stubGlobal("fetch", fetchMock);

    const download = await downloadCustomersCsv({
      page: 3,
      pageSize: 25,
      search: "fjord",
      status: "archived",
      sortBy: "name",
      sortDirection: "desc",
      tagId: "t1",
    });

    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("/api/v1/customers/export?search=fjord&status=archived&sortBy=name&sortDirection=desc&tagId=t1");
    expect(init.credentials).toBe("include");
    expect(download.fileName).toBe("customers-2026-09-24.csv");
  });

  it("throws the refusal's own sentence", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(
        async () =>
          new Response(JSON.stringify({ title: "Too many customers to export", detail: "Narrow it with a filter." }), {
            status: 400,
            headers: { "Content-Type": "application/problem+json" },
          }),
      ),
    );
    await expect(downloadCustomersCsv({})).rejects.toThrow("Narrow it with a filter.");
  });

  it("signs the person out when the session has expired, as every other request does", async () => {
    const cleared = vi.fn();
    const unauthorized = vi.fn();
    setAuthStateClearer(cleared);
    setUnauthorizedHandler(unauthorized);
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response(null, { status: 401 })),
    );

    await expect(downloadCustomersCsv({})).rejects.toThrow();
    expect(cleared).toHaveBeenCalled();
    expect(unauthorized).toHaveBeenCalled();
  });

  it("leaves the session alone on a refusal that is about the export", async () => {
    const unauthorized = vi.fn();
    setUnauthorizedHandler(unauthorized);
    vi.stubGlobal(
      "fetch",
      vi.fn(
        async () =>
          new Response(JSON.stringify({ title: "Too many customers to export", detail: "Narrow it with a filter." }), {
            status: 400,
            headers: { "Content-Type": "application/problem+json" },
          }),
      ),
    );

    await expect(downloadCustomersCsv({})).rejects.toThrow("Narrow it with a filter.");
    expect(unauthorized).not.toHaveBeenCalled();
  });
});

describe("importCustomers", () => {
  it("sends the file as the part named file, with both flags, and fills in an omitted column with null", async () => {
    // Literally the server's body: a row-level error has no column key at all.
    const fetchMock = vi.fn(
      async () =>
        new Response(
          JSON.stringify({
            dryRun: true,
            rows: 1,
            created: 0,
            updated: 0,
            failed: 1,
            errors: [{ row: 1, message: "This row has 1 cells, but the header has 2" }],
          }),
          { status: 200, headers: { "Content-Type": "application/json" } },
        ),
    );
    vi.stubGlobal("fetch", fetchMock);
    const file = new File(["name\r\nA\r\n"], "kunder.csv", { type: "text/csv" });

    const result = await importCustomers(file, { dryRun: true, allowDuplicateIdentity: true });

    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("/api/v1/customers/import?dryRun=true&allowDuplicateIdentity=true");
    expect(init.method).toBe("POST");
    expect(((init.body as FormData).get("file") as File).name).toBe("kunder.csv");
    expect(new Headers(init.headers).has("Content-Type")).toBe(false);
    expect(result.errors).toEqual([{ row: 1, column: null, message: "This row has 1 cells, but the header has 2" }]);
  });
});
```

Create `apps/customers/frontend/src/pages/-customer-import-modal.test.tsx`:

```tsx
import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { saveCsv } from "../api/import-export";
import { CustomerImportModal } from "./-customer-import-modal";

vi.mock("../api/import-export", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../api/import-export")>()),
  saveCsv: vi.fn(),
}));

const FILE_TEXT = "\ufeffcustomerNumber;name;email\r\n;Ny Kunde AS;post@ny.no\r\n1001;Gammel AS;nope\r\n;;\r\n;Tredje AS;\r\n";
const EMAIL_ERROR = "An email address must look like name@example.com, but was 'nope'";

const checked = {
  dryRun: true,
  rows: 3,
  created: 2,
  updated: 0,
  failed: 1,
  errors: [{ row: 2, column: "email", message: EMAIL_ERROR }],
};
const imported = { ...checked, dryRun: false };
// Literally the server's body: the row-level error carries no column key.
const nothingImportable = {
  dryRun: true,
  rows: 1,
  created: 0,
  updated: 0,
  failed: 1,
  errors: [{ row: 1, message: "This row has 2 cells, but the header has 3" }],
};

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": status === 200 ? "application/json" : "application/problem+json" },
  });

const stubImport = (answers: { dry: Response; real?: Response }) => {
  const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    if (url.startsWith("/api/v1/customers/import?dryRun=true")) return answers.dry.clone();
    if (url.startsWith("/api/v1/customers/import?dryRun=false") && answers.real) return answers.real.clone();
    if (url.startsWith("/api/v1/customers/import/template")) {
      return new Response("\ufeffcustomerNumber;name\r\n", {
        status: 200,
        headers: { "Content-Disposition": 'attachment; filename="customers-import-template.csv"' },
      });
    }
    return new Response(null, { status: 404 });
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
};

const renderModal = () => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const invalidate = vi.spyOn(queryClient, "invalidateQueries");
  render(
    <MantineProvider env="test">
      <QueryClientProvider client={queryClient}>
        <CustomerImportModal opened onClose={() => {}} />
      </QueryClientProvider>
    </MantineProvider>,
  );
  return { invalidate };
};

const chooseFile = async () =>
  userEvent.upload(screen.getByLabelText("Choose CSV file"), new File([FILE_TEXT], "kunder.csv", { type: "text/csv" }));

describe("CustomerImportModal", () => {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
    vi.mocked(saveCsv).mockReset();
  });

  it("checks the file, imports it, and hands back the failed rows with an error column", async () => {
    const fetchMock = stubImport({ dry: json(checked), real: json(imported) });
    const { invalidate } = renderModal();

    await chooseFile();
    expect(screen.getByRole("button", { name: "Import" })).toBeDisabled();
    await userEvent.click(screen.getByRole("button", { name: "Check" }));

    expect(await screen.findByText("3 rows — 2 would be created, 0 would be updated, 1 have errors")).toBeInTheDocument();
    const errorRow = screen.getByText(EMAIL_ERROR).closest("tr") as HTMLElement;
    expect(within(errorRow).getByText("2")).toBeInTheDocument();
    expect(within(errorRow).getByText("email")).toBeInTheDocument();
    const dryCall = fetchMock.mock.calls.find(([input]) => String(input).includes("dryRun=true"));
    expect(String(dryCall?.[0])).toContain("allowDuplicateIdentity=false");
    expect(invalidate).not.toHaveBeenCalled();

    await userEvent.click(screen.getByRole("button", { name: "Import" }));
    expect(await screen.findByText("3 rows — 2 created, 0 updated, 1 failed")).toBeInTheDocument();
    expect(fetchMock.mock.calls.filter(([input]) => String(input).includes("dryRun=false"))).toHaveLength(1);
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ["customers"] });

    await userEvent.click(screen.getByRole("button", { name: "Download failed rows" }));
    await waitFor(() => expect(saveCsv).toHaveBeenCalled());
    const [{ blob, fileName }] = vi.mocked(saveCsv).mock.calls[0];
    expect(fileName).toBe("kunder-failed-rows.csv");
    // Blob.text() strips a leading BOM; the file must keep it, so the bytes are decoded with it.
    const text = new TextDecoder("utf-8", { ignoreBOM: true }).decode(await blob.arrayBuffer());
    expect(text).toBe(`\ufeffcustomerNumber;name;email;error\r\n1001;Gammel AS;nope;email: ${EMAIL_ERROR}\r\n`);
  });

  it("keeps Import disabled when no row of the file could be imported, and shows a row-level error without a column", async () => {
    stubImport({ dry: json(nothingImportable) });
    renderModal();

    await chooseFile();
    await userEvent.click(screen.getByRole("button", { name: "Check" }));

    expect(
      await screen.findByText("No row in this file can be imported. Fix the rows below and check again."),
    ).toBeInTheDocument();
    const errorRow = screen.getByText("This row has 2 cells, but the header has 3").closest("tr") as HTMLElement;
    expect(within(errorRow).getByText("—")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Import" })).toBeDisabled();
  });

  it("shows the server's own reasons when the file itself is refused", async () => {
    stubImport({
      dry: json(
        {
          title: "Invalid import file",
          status: 400,
          errors: { file: ["Unknown columns: 'nmae'. A column is one the export and the template carry."] },
        },
        400,
      ),
    });
    renderModal();

    await chooseFile();
    await userEvent.click(screen.getByRole("button", { name: "Check" }));

    expect(await screen.findByText("The file could not be imported")).toBeInTheDocument();
    expect(screen.getByText(/Unknown columns: 'nmae'/)).toBeInTheDocument();
  });

  it("refreshes the list and asks for a new check when a real run fails part-way", async () => {
    stubImport({ dry: json(checked), real: json({ title: "Internal Server Error", status: 500 }, 500) });
    const { invalidate } = renderModal();

    await chooseFile();
    await userEvent.click(screen.getByRole("button", { name: "Check" }));
    await screen.findByText("3 rows — 2 would be created, 0 would be updated, 1 have errors");
    await userEvent.click(screen.getByRole("button", { name: "Import" }));

    expect(await screen.findByText("The file could not be imported")).toBeInTheDocument();
    // Rows before the failure may have been committed: the list is refreshed,
    // and Import cannot be clicked again on the stale check.
    await waitFor(() => expect(invalidate).toHaveBeenCalledWith({ queryKey: ["customers"] }));
    expect(screen.getByRole("button", { name: "Import" })).toBeDisabled();
  });

  it("sends allowDuplicateIdentity when the box is ticked", async () => {
    const fetchMock = stubImport({ dry: json(checked) });
    renderModal();

    await chooseFile();
    await userEvent.click(screen.getByLabelText("Allow a customer to share a legal identity with another customer"));
    await userEvent.click(screen.getByRole("button", { name: "Check" }));

    await screen.findByText("3 rows — 2 would be created, 0 would be updated, 1 have errors");
    const dryCall = fetchMock.mock.calls.find(([input]) => String(input).includes("dryRun=true"));
    expect(String(dryCall?.[0])).toContain("allowDuplicateIdentity=true");
  });

  it("downloads the template under the server's file name", async () => {
    stubImport({ dry: json(checked) });
    renderModal();

    await userEvent.click(screen.getByRole("button", { name: "Download template" }));

    await waitFor(() => expect(saveCsv).toHaveBeenCalled());
    expect(vi.mocked(saveCsv).mock.calls[0][0].fileName).toBe("customers-import-template.csv");
  });
});
```

In `apps/customers/frontend/src/pages/-customers.index.test.tsx`: in `stubFetch`'s implementation, as its first branch after `const url = String(input);`, add:

```tsx
    if (url.startsWith("/api/v1/customers/export")) {
      return Promise.resolve(
        new Response("\ufeffcustomerNumber;name\r\n", {
          status: 200,
          headers: { "Content-Disposition": 'attachment; filename="customers-2026-09-24.csv"' },
        }),
      );
    }
```

add `onTestFinished` to the file's `vitest` import, change `renderPage`'s parameter to `(props: { canEdit?: boolean; canExport?: boolean; canImport?: boolean } = {})`, and add to the `describe`:

```tsx
  it("downloads the list it shows as the customers file", async () => {
    const fetchMock = stubFetch();
    // The two statics only — the URL constructor stays real for the render.
    const { createObjectURL, revokeObjectURL } = URL;
    URL.createObjectURL = vi.fn(() => "blob:customers");
    URL.revokeObjectURL = vi.fn();
    onTestFinished(() => {
      URL.createObjectURL = createObjectURL;
      URL.revokeObjectURL = revokeObjectURL;
    });
    const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
    router.search = { page: 2, search: "fjord", status: "archived", sortBy: "name", sortDirection: "desc" };
    renderPage({ canExport: true });

    await userEvent.click(await screen.findByRole("button", { name: "Export" }));

    await waitFor(() => expect(click).toHaveBeenCalled());
    expect((click.mock.contexts[0] as HTMLAnchorElement).download).toBe("customers-2026-09-24.csv");
    const exportUrl = String(
      fetchMock.mock.calls.find(([input]) => String(input).startsWith("/api/v1/customers/export"))?.[0],
    );
    expect(exportUrl).toBe("/api/v1/customers/export?search=fjord&status=archived&sortBy=name&sortDirection=desc");
    click.mockRestore();
  });

  it("offers Export and Import only to a caller the host says may use them", async () => {
    stubFetch();
    renderPage();
    await screen.findByText("Equinor");
    expect(screen.queryByRole("button", { name: "Export" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Import" })).not.toBeInTheDocument();
    cleanup();

    renderPage({ canExport: true, canImport: true });
    await userEvent.click(await screen.findByRole("button", { name: "Import" }));
    expect(await screen.findByRole("dialog")).toHaveTextContent("Import customers");
  });
```

In `apps/host/frontend/src/routes/customers/customers-list-route.test.tsx`, replace the `vi.mock("@vantigo/customers-ui/pages/customers.index", …)` factory with:

```tsx
vi.mock("@vantigo/customers-ui/pages/customers.index", () => ({
  CustomersPage: ({ canEdit, canExport, canImport }: { canEdit?: boolean; canExport?: boolean; canImport?: boolean }) => (
    <>
      <span data-testid="can-edit">{String(Boolean(canEdit))}</span>
      <span data-testid="can-export">{String(Boolean(canExport))}</span>
      <span data-testid="can-import">{String(Boolean(canImport))}</span>
    </>
  ),
}));
```

and add to its `describe`:

```tsx
  it("passes canImport only for customers:create, customers:update and customers:view together, and canExport from customers:view", () => {
    // The import operation's own rule: it both creates and updates, and every
    // write it stands in for needs view by hand.
    withPermissions(["customers:view", "customers:update"]);
    renderPage();
    expect(screen.getByTestId("can-import")).toHaveTextContent("false");
    expect(screen.getByTestId("can-export")).toHaveTextContent("true");
    cleanup();

    withPermissions(["customers:create", "customers:update"]);
    renderPage();
    expect(screen.getByTestId("can-import")).toHaveTextContent("false");
    cleanup();

    withPermissions(["customers:view", "customers:create", "customers:update"]);
    renderPage();
    expect(screen.getByTestId("can-import")).toHaveTextContent("true");
  });
```

(import `cleanup` from `@testing-library/react` there).

- [ ] **Step 2: Run them and watch them fail**

```bash
cd /home/anders/projects/vantigo/vantigo
mise exec -- bun run --cwd apps/customers/frontend test 2>&1 | tail -30
mise exec -- bun run --cwd apps/host/frontend test -- customers-list-route 2>&1 | tail -15
```
Expected: FAIL — `Failed to resolve import "./csv"`, `"./import-export"`, `"./-customer-import-modal"`; the index tests find no Export button; the host test finds no `can-import` true.

- [ ] **Step 3: The browser's file**

Create `apps/customers/frontend/src/lib/csv.ts`:

```ts
import type { CustomerImportError } from "../api/import-export";

/**
 * The customers file (customers import/export design D1) as the browser reads
 * and writes it — for one purpose: the failed-rows file (D4), built from the
 * file the person picked and the errors the import answered. The server is the
 * authority on what a row means; this only has to number rows exactly as the
 * server does (so an error's `row` finds its line) and write a file the server
 * reads back. Semicolons, CRLF, a byte order mark, RFC 4180 quoting, the
 * formula guard: `internal/customers/csvfile.go`'s rules.
 */
const BOM = "\ufeff";
const SEPARATOR = ";";
const LINE_END = "\r\n";
const ERROR_COLUMN = "error";

/** One data row: its 1-based number (the server's) and its cells as written. */
export interface CsvRecord {
  row: number;
  cells: string[];
}

export interface CsvTable {
  header: string[];
  records: CsvRecord[];
}

/** Splits text into records — RFC 4180 with `;`, CRLF or LF — skipping empty lines as Go's encoding/csv does. */
const splitRecords = (text: string): string[][] => {
  const records: string[][] = [];
  let cells: string[] = [];
  let cell = "";
  let quoted = false;
  let started = false;
  const endRecord = () => {
    if (started) records.push([...cells, cell]);
    cells = [];
    cell = "";
    started = false;
  };
  for (let i = 0; i < text.length; i += 1) {
    const char = text[i];
    if (quoted) {
      if (char === '"' && text[i + 1] === '"') {
        cell += '"';
        i += 1;
      } else if (char === '"') quoted = false;
      else cell += char;
      continue;
    }
    if (char === '"') {
      quoted = true;
      started = true;
    } else if (char === SEPARATOR) {
      cells.push(cell);
      cell = "";
      started = true;
    } else if (char === "\n") endRecord();
    else if (char !== "\r" || text[i + 1] !== "\n") {
      cell += char;
      started = true;
    }
  }
  endRecord();
  return records;
};

/**
 * The file as the server reads it: the header, then every record that is not
 * all blank, numbered from 1 — a record whose every cell is blank is skipped
 * and takes no number, as `readCSVFile` skips it.
 */
export const parseCsv = (text: string): CsvTable => {
  const [header = [], ...rest] = splitRecords(text.startsWith(BOM) ? text.slice(BOM.length) : text);
  const records: CsvRecord[] = [];
  for (const cells of rest) {
    if (cells.every((cell) => cell.trim() === "")) continue;
    records.push({ row: records.length + 1, cells });
  }
  return { header, records };
};

/** One cell as it goes into the file: the formula guard, then quoting — the server's `csvCell`. */
export const csvCell = (value: string): string => {
  const guarded = /^[=+\-@\t\r]/.test(value) ? `'${value}` : value;
  return /[;"\r\n]/.test(guarded) ? `"${guarded.replaceAll('"', '""')}"` : guarded;
};

/** Rows as a customers file. */
export const toCsv = (rows: string[][]): string =>
  BOM + rows.map((cells) => cells.map(csvCell).join(SEPARATOR) + LINE_END).join("");

/**
 * The failed-rows file (D4): the header and, of the records, only those an
 * error names — each as it was, with an `error` column holding every problem
 * of that row (`column: message`, or the message alone for the row as a whole).
 * A file that already carries an `error` column — a failed-rows file being
 * re-run — has it overwritten rather than a second one added. The import
 * ignores that column, so the file goes straight back in once fixed.
 */
export const failedRowsCsv = (table: CsvTable, errors: CustomerImportError[]): string => {
  const problems = new Map<number, string[]>();
  for (const error of errors) {
    const text = error.column ? `${error.column}: ${error.message}` : error.message;
    problems.set(error.row, [...(problems.get(error.row) ?? []), text]);
  }
  const existing = table.header.findIndex((name) => name.trim().toLowerCase() === ERROR_COLUMN);
  const header = existing >= 0 ? table.header : [...table.header, ERROR_COLUMN];
  const at = existing >= 0 ? existing : table.header.length;
  const rows = table.records
    .filter((record) => problems.has(record.row))
    .map((record) => {
      const cells = [...record.cells];
      while (cells.length < header.length) cells.push("");
      cells[at] = (problems.get(record.row) ?? []).join(" | ");
      return cells;
    });
  return toCsv([header, ...rows]);
};
```

- [ ] **Step 4: The api**

In `apps/customers/frontend/src/api/customers.ts`, directly above `async function fetchCustomers`, add:

```ts
/**
 * The list's filters, sort and paging as a query string — `?…`, or "" when
 * there is none. The list and the export (`import-export.ts`) both build it
 * here, so the file a person downloads is the list they are looking at.
 */
export const customersSearchParams = (params: CustomersQueryParams): string => {
  const searchParams = new URLSearchParams();
  if (params.page) searchParams.set("page", String(params.page));
  if (params.pageSize) searchParams.set("pageSize", String(params.pageSize));
  if (params.search) searchParams.set("search", params.search);
  if (params.status) searchParams.set("status", params.status);
  if (params.type) searchParams.set("type", params.type);
  if (params.sortBy) searchParams.set("sortBy", params.sortBy);
  if (params.sortDirection) searchParams.set("sortDirection", params.sortDirection);
  if (params.ownerId) searchParams.set("ownerId", params.ownerId);
  if (params.tagId) searchParams.set("tagId", params.tagId);
  if (params.groupId) searchParams.set("groupId", params.groupId);
  return searchParams.size > 0 ? `?${searchParams}` : "";
};
```

and replace `fetchCustomers`' body down to the `request` call with:

```ts
  const response = await request<PaginatedResponse<RawCustomerResponse>>(
    `/api/v1/customers${customersSearchParams(params)}`,
    { signal },
  );
  return { ...response, data: response.data.map(normalizeCustomer) };
```

Replace `apps/customers/frontend/src/api/request.ts` with the expenses package's shape (`apps/expenses/frontend/src/api/request.ts:1-39`) — the setters become wrappers that also keep the two callbacks, so the one request that cannot go through the client can still sign somebody out. `frontend-shell` has no such hook to reuse:

```ts
import { createApiClient } from "@vantigo/frontend-api-client";
import { appUrl } from "@vantigo/frontend-shell";

const client = createApiClient({ resolveUrl: appUrl });

export const { request } = client;
export type { ApiError, RequestOptions } from "@vantigo/frontend-api-client";
export { ApiConflictError, ApiValidationError, NotFoundError, readJson } from "@vantigo/frontend-api-client";

/**
 * The host's session handling, kept here as well as handed to the client.
 * Two requests in this package cannot go through `request` — the customers
 * file and the import template, whose bodies are files and whose refusals are
 * read by hand — and an expired session on either must still sign the person
 * out. Holding the two callbacks lets `handleUnauthorized` do exactly what the
 * client does.
 */
let onUnauthorized: (() => void | Promise<void>) | undefined;
let clearAuthState: (() => void | Promise<void>) | undefined;

export const setUnauthorizedHandler = (handler: (() => void | Promise<void>) | undefined) => {
  onUnauthorized = handler;
  client.setUnauthorizedHandler(handler);
};

export const setAuthStateClearer = (clearer: (() => void | Promise<void>) | undefined) => {
  clearAuthState = clearer;
  client.setAuthStateClearer(clearer);
};

/** What the shared client does on a 401: clear the session, then tell the host. */
export const handleUnauthorized = async (): Promise<void> => {
  try {
    await clearAuthState?.();
  } finally {
    await onUnauthorized?.();
  }
};
```

Create `apps/customers/frontend/src/api/import-export.ts`:

```ts
import { appUrl } from "@vantigo/frontend-shell";
import { type CustomersQueryParams, customersSearchParams } from "./customers";
import { ApiValidationError, handleUnauthorized, readJson, request } from "./request";

/** A file the server handed over, and the name it attached it under. */
export interface CsvDownload {
  blob: Blob;
  fileName: string;
}

/**
 * One problem with one row of an import (customers import/export design D3):
 * `row` is the 1-based data row, `column` the header of a field's error — null
 * for the row as a whole, which the wire says by omitting it.
 */
export interface CustomerImportError {
  row: number;
  column: string | null;
  message: string;
}

/** What an import did, or in a dry run would do. */
export interface CustomerImportResult {
  dryRun: boolean;
  rows: number;
  created: number;
  updated: number;
  failed: number;
  errors: CustomerImportError[];
}

type RawCustomerImportResult = Omit<CustomerImportResult, "errors"> & {
  errors: Array<Omit<CustomerImportError, "column"> & { column?: string }>;
};

/** The name the server attached the file under, or `fallback` when it said nothing. */
const fileNameFrom = (disposition: string | null, fallback: string): string => {
  const encoded = /filename\*=UTF-8''([^;]+)/i.exec(disposition ?? "");
  if (encoded) return decodeURIComponent(encoded[1]);
  const plain = /filename="?([^";]+)"?/i.exec(disposition ?? "");
  return plain ? plain[1] : fallback;
};

/**
 * A file, fetched rather than navigated to — the reimbursements download: a
 * plain `<a download>` would drop the person on a JSON page when the export is
 * refused, and the export's cap refusal carries no `errors` object, only a
 * detail asking for a narrower filter. Reading the body here is what lets it be
 * shown.
 */
const downloadCsv = async (path: string, fallback: string): Promise<CsvDownload> => {
  const response = await fetch(appUrl(path), { credentials: "include" });
  if (response.ok) {
    return { blob: await response.blob(), fileName: fileNameFrom(response.headers.get("Content-Disposition"), fallback) };
  }
  // The shared client is what signs somebody out; this request does not go
  // through it, so an expired session is handed over by hand rather than shown
  // as a raw problem sentence.
  if (response.status === 401) await handleUnauthorized();
  const problem = await readJson<{ title?: string; detail?: string; errors?: Record<string, string[]> }>(
    response,
  ).catch(() => null);
  if (problem?.errors) throw new ApiValidationError(problem.title ?? "Invalid export", problem.errors, response.status);
  throw Object.assign(new Error(problem?.detail ?? problem?.title ?? `Request failed (HTTP ${response.status})`), {
    status: response.status,
  });
};

/** The list, as the caller sees it, as the customers file: its filters and sort, never its paging. */
export const downloadCustomersCsv = (params: CustomersQueryParams): Promise<CsvDownload> =>
  downloadCsv(
    `/api/v1/customers/export${customersSearchParams({ ...params, page: undefined, pageSize: undefined })}`,
    "customers.csv",
  );

/** The header every import may carry. */
export const downloadImportTemplate = (): Promise<CsvDownload> =>
  downloadCsv("/api/v1/customers/import/template", "customers-import-template.csv");

/**
 * Hands the file to the browser, then lets go of the object URL — deferred,
 * because revoking in the same turn as the click is a race Chromium wins and
 * Firefox and Safari lose (the reimbursements `saveCsv`).
 */
export const saveCsv = ({ blob, fileName }: CsvDownload): void => {
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = fileName;
  document.body.append(link);
  link.click();
  setTimeout(() => {
    link.remove();
    URL.revokeObjectURL(url);
  }, 0);
};

/**
 * Sends the file to the import: one part named `file`, the `Content-Type` left
 * to the browser so the multipart boundary is its own. `dryRun: true` keeps
 * nothing. A file the server refuses as a whole is an `ApiValidationError`
 * whose `fields.file` says why.
 */
export const importCustomers = async (
  file: File,
  options: { dryRun: boolean; allowDuplicateIdentity: boolean },
): Promise<CustomerImportResult> => {
  const form = new FormData();
  form.append("file", file, file.name);
  const query = new URLSearchParams({
    dryRun: String(options.dryRun),
    allowDuplicateIdentity: String(options.allowDuplicateIdentity),
  });
  const raw = await request<RawCustomerImportResult>(`/api/v1/customers/import?${query}`, {
    method: "POST",
    body: form,
  });
  return { ...raw, errors: raw.errors.map((error) => ({ ...error, column: error.column ?? null })) };
};
```

- [ ] **Step 5: The modal**

Create `apps/customers/frontend/src/pages/-customer-import-modal.tsx`:

```tsx
import { Alert, Anchor, Box, Button, Checkbox, Group, Modal, ScrollArea, Stack, Table, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconAlertCircle } from "@tabler/icons-react";
import { useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { type ChangeEvent, type DragEvent, useState } from "react";
import { type CustomerImportResult, downloadImportTemplate, importCustomers, saveCsv } from "../api/import-export";
import { ApiValidationError } from "../api/request";
import "../i18n";
import { failedRowsCsv, parseCsv } from "../lib/csv";

/** What the failed rows of `name` are saved as: beside it, and saying what they are. */
const failedRowsFileName = (name: string) => `${name.replace(/\.csv$/i, "")}-failed-rows.csv`;

/**
 * The CSV import (customers import/export design D4), in three steps: pick a
 * file — dropped or chosen, the receipt dropzone's shape, with the template a
 * click away — then **Check**, the server's dry run, whose counts and errors are
 * shown and nothing kept; then **Import**, enabled once the check found a row
 * that would succeed, whose counts are shown again with, when rows failed,
 * **Download failed rows**: the original rows with an `error` column, built here
 * from the file the browser still holds, to be fixed and imported on their own.
 *
 * Changing the file or the duplicate flag throws the check away: a check is
 * about one file under one flag, and Import must never run on a different one.
 */
export const CustomerImportModal = ({ opened, onClose }: { opened: boolean; onClose: () => void }) => {
  const { t } = useI18n("customers");
  const queryClient = useQueryClient();
  const [file, setFile] = useState<File | null>(null);
  const [allowDuplicateIdentity, setAllowDuplicateIdentity] = useState(false);
  const [check, setCheck] = useState<CustomerImportResult | null>(null);
  const [outcome, setOutcome] = useState<CustomerImportResult | null>(null);
  const [problem, setProblem] = useState<string[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [over, setOver] = useState(false);

  const reset = () => {
    setCheck(null);
    setOutcome(null);
    setProblem(null);
  };
  const close = () => {
    setFile(null);
    setAllowDuplicateIdentity(false);
    reset();
    onClose();
  };
  const choose = (next: File | undefined) => {
    if (!next) return;
    setFile(next);
    reset();
  };

  const messagesOf = (error: unknown): string[] => {
    if (error instanceof ApiValidationError) {
      const named = error.fields.file ?? Object.values(error.fields).flat();
      if (named.length > 0) return named;
    }
    return [(error as Error).message];
  };

  const counts = (result: CustomerImportResult) => ({
    rows: String(result.rows),
    created: String(result.created),
    updated: String(result.updated),
    failed: String(result.failed),
  });

  const run = async (dryRun: boolean) => {
    if (!file) return;
    setBusy(true);
    setProblem(null);
    try {
      const result = await importCustomers(file, { dryRun, allowDuplicateIdentity });
      if (dryRun) {
        setCheck(result);
      } else {
        setOutcome(result);
        notifications.show({
          color: result.failed > 0 ? "yellow" : "teal",
          title: t("importDone"),
          message: t("importDoneCounts", counts(result)),
        });
      }
    } catch (error) {
      setProblem(messagesOf(error));
      // A real run that failed part-way has committed the rows before the
      // failure. The check no longer describes what an import would do, and a
      // second click would create those rows twice: Import waits for a new
      // Check.
      if (!dryRun) setCheck(null);
    } finally {
      setBusy(false);
      // Whatever a real run did — all of it, or the rows before a failure —
      // the list, its counts and the filters' words may have moved.
      if (!dryRun) await queryClient.invalidateQueries({ queryKey: ["customers"] });
    }
  };

  const downloadTemplate = async () => {
    try {
      saveCsv(await downloadImportTemplate());
    } catch (error) {
      setProblem(messagesOf(error));
    }
  };

  const downloadFailedRows = async () => {
    if (!file || !outcome) return;
    const csv = failedRowsCsv(parseCsv(await file.text()), outcome.errors);
    saveCsv({ blob: new Blob([csv], { type: "text/csv;charset=utf-8" }), fileName: failedRowsFileName(file.name) });
  };

  const pick = (event: ChangeEvent<HTMLInputElement>) => {
    choose(event.target.files?.[0]);
    event.target.value = "";
  };
  const drop = (event: DragEvent<HTMLDivElement>) => {
    event.preventDefault();
    setOver(false);
    choose(event.dataTransfer?.files?.[0]);
  };

  const importable = check !== null && check.created + check.updated > 0;
  const shown = outcome ?? check;

  return (
    <Modal opened={opened} onClose={close} title={t("importTitle")} size="xl">
      <Stack gap="md">
        <Text size="sm">{t("importIntro")}</Text>
        <Anchor component="button" type="button" size="sm" onClick={() => void downloadTemplate()}>
          {t("importDownloadTemplate")}
        </Anchor>

        {!outcome && (
          <Box
            p="sm"
            style={{
              border: "1px dashed var(--mantine-color-gray-4)",
              borderRadius: "var(--mantine-radius-sm)",
              background: over ? "var(--mantine-color-gray-0)" : undefined,
            }}
            onDragOver={(event) => {
              event.preventDefault();
              setOver(true);
            }}
            onDragLeave={() => setOver(false)}
            onDrop={drop}
          >
            <Stack gap="xs">
              <Text size="sm" c="dimmed">
                {t("importDropFile")}
              </Text>
              <input type="file" accept=".csv,text/csv" aria-label={t("importChooseFile")} onChange={pick} />
              {file && <Text size="sm">{t("importChosenFile", { name: file.name })}</Text>}
            </Stack>
          </Box>
        )}

        <Checkbox
          label={t("importAllowDuplicateIdentity")}
          checked={allowDuplicateIdentity}
          disabled={outcome !== null}
          onChange={(event) => {
            setAllowDuplicateIdentity(event.currentTarget.checked);
            setCheck(null);
          }}
        />

        {problem && (
          <Alert color="red" icon={<IconAlertCircle size={16} />} title={t("importCouldNotRun")}>
            {problem.map((message) => (
              <Text key={message} size="sm">
                {message}
              </Text>
            ))}
          </Alert>
        )}

        {shown && (
          <Stack gap="xs">
            <Text fw={600}>{outcome ? t("importDoneCounts", counts(outcome)) : t("importCheckCounts", counts(shown))}</Text>
            {!outcome && (
              <Text size="sm" c="dimmed">
                {importable ? t("importCheckIntro") : t("importNothingToImport")}
              </Text>
            )}
            {shown.errors.length > 0 && (
              <ScrollArea.Autosize mah={320}>
                <Table striped>
                  <Table.Thead>
                    <Table.Tr>
                      <Table.Th>{t("importErrorRow")}</Table.Th>
                      <Table.Th>{t("importErrorColumn")}</Table.Th>
                      <Table.Th>{t("importErrorMessage")}</Table.Th>
                    </Table.Tr>
                  </Table.Thead>
                  <Table.Tbody>
                    {shown.errors.map((error, index) => (
                      // A row can fail on the same column twice; the index keeps them apart.
                      <Table.Tr key={`${error.row}:${error.column ?? ""}:${index}`}>
                        <Table.Td>{error.row}</Table.Td>
                        <Table.Td>{error.column ?? "—"}</Table.Td>
                        <Table.Td>{error.message}</Table.Td>
                      </Table.Tr>
                    ))}
                  </Table.Tbody>
                </Table>
              </ScrollArea.Autosize>
            )}
          </Stack>
        )}

        {outcome && outcome.failed > 0 && (
          <Stack gap={4}>
            <Group>
              <Button variant="light" onClick={() => void downloadFailedRows()}>
                {t("importDownloadFailedRows")}
              </Button>
            </Group>
            <Text size="xs" c="dimmed">
              {t("importFailedRowsHint")}
            </Text>
          </Stack>
        )}

        <Group justify="flex-end">
          <Button variant="default" onClick={close}>
            {outcome ? t("importClose") : t("cancel")}
          </Button>
          {!outcome && (
            <>
              <Button variant="light" disabled={!file || busy} loading={busy && check === null} onClick={() => void run(true)}>
                {t("importCheck")}
              </Button>
              <Button disabled={!importable || busy} loading={busy && check !== null} onClick={() => void run(false)}>
                {t("importRun")}
              </Button>
            </>
          )}
        </Group>
      </Stack>
    </Modal>
  );
};
```

- [ ] **Step 6: The page, the host and the strings**

In `apps/customers/frontend/src/pages/customers.index.tsx`:
- add `IconDownload` and `IconUpload` to the `@tabler/icons-react` import, `import { notifications } from "@mantine/notifications";` beside the Mantine imports, `import { downloadCustomersCsv, saveCsv } from "../api/import-export";` after the `../api/groups` import, and `import { CustomerImportModal } from "./-customer-import-modal";` before `./-manage-groups-modal`;
- replace `export const CustomersPage = ({ canEdit }: { canEdit?: boolean }) => {` with:

```tsx
export const CustomersPage = ({
  canEdit,
  canExport,
  canImport,
}: {
  canEdit?: boolean;
  /** `customers:view`: download the list, as filtered and sorted, as the customers file. */
  canExport?: boolean;
  /** `customers:create`, `customers:update` and `customers:view` — the import operation's own rule. */
  canImport?: boolean;
}) => {
```

- directly after the `closeModal` function, add:

```tsx
  const [importOpened, setImportOpened] = useState(false);
  const [exporting, setExporting] = useState(false);
  // The list as it stands — its filters and sort from the URL, never its page —
  // saved as the customers file. A refusal (the 5000-row cap) is the server's
  // own sentence, shown rather than swallowed.
  const exportList = async () => {
    setExporting(true);
    try {
      saveCsv(await downloadCustomersCsv(customersListParams(listSearch)));
    } catch (error) {
      notifications.show({ color: "red", title: t("exportCouldNotBeDownloaded"), message: (error as Error).message });
    } finally {
      setExporting(false);
    }
  };
```

- in the `PageHeader`'s `actions` `Group`, directly before the Create `Button`, add:

```tsx
            {canExport && (
              <Button
                variant="default"
                leftSection={<IconDownload size={16} />}
                loading={exporting}
                onClick={() => void exportList()}
              >
                {t("exportCustomers")}
              </Button>
            )}
            {canImport && (
              <Button variant="default" leftSection={<IconUpload size={16} />} onClick={() => setImportOpened(true)}>
                {t("importCustomers")}
              </Button>
            )}
```

- directly after `<CustomerFormModal state={modalState} onClose={closeModal} />`, add `<CustomerImportModal opened={importOpened} onClose={() => setImportOpened(false)} />`.

In `apps/host/frontend/src/routes/customers/-customers-list.tsx`, replace the return statement with:

```tsx
  const permissions = authorization.data?.permissions;
  return (
    <CustomersPage
      canEdit={hasPermissions(permissions, ["customers:update"])}
      canExport={hasPermissions(permissions, ["customers:view"])}
      canImport={hasPermissions(permissions, ["customers:create", "customers:update", "customers:view"])}
    />
  );
```

and add to its doc comment, after the `canEdit` sentence: `` `canExport` (`customers:view`) puts **Export** in the header and `canImport` (`customers:create`, `customers:update` and `customers:view` together — the import operation's own rule) puts **Import** there (customers import/export design D4). ``

In `apps/customers/frontend/src/i18n.ts`, add to `en`, directly after `useRegistryPostalAddress: …,`:

```ts
  exportCustomers: "Export",
  exportCouldNotBeDownloaded: "The export could not be downloaded",
  importCustomers: "Import",
  importTitle: "Import customers",
  importIntro:
    "A semicolon-separated CSV file in UTF-8, with the columns the export and the template carry. A row with a customer number updates that customer; a row without one creates a new customer. Nothing is saved until you import.",
  importDownloadTemplate: "Download template",
  importDropFile: "Drop a CSV file here, or choose one.",
  importChooseFile: "Choose CSV file",
  importChosenFile: "Chosen: {{name}}",
  importAllowDuplicateIdentity: "Allow a customer to share a legal identity with another customer",
  importCheck: "Check",
  importCheckCounts: "{{rows}} rows — {{created}} would be created, {{updated}} would be updated, {{failed}} have errors",
  importCheckIntro: "Nothing has been saved yet: this is what importing the file would do.",
  importNothingToImport: "No row in this file can be imported. Fix the rows below and check again.",
  importErrorRow: "Row",
  importErrorColumn: "Column",
  importErrorMessage: "Problem",
  importRun: "Import",
  importDone: "Import finished",
  importDoneCounts: "{{rows}} rows — {{created}} created, {{updated}} updated, {{failed}} failed",
  importDownloadFailedRows: "Download failed rows",
  importFailedRowsHint:
    "The rows that failed, exactly as they were, with an error column added. Fix them and import that file.",
  importCouldNotRun: "The file could not be imported",
  importClose: "Close",
```

and to `nb`, directly after `useRegistryPostalAddress: …,`:

```ts
  exportCustomers: "Eksporter",
  exportCouldNotBeDownloaded: "Eksporten kunne ikke lastes ned",
  importCustomers: "Importer",
  importTitle: "Importer kunder",
  importIntro:
    "En semikolonseparert CSV-fil i UTF-8, med kolonnene eksporten og malen har. En rad med kundenummer oppdaterer den kunden; en rad uten oppretter en ny kunde. Ingenting lagres før du importerer.",
  importDownloadTemplate: "Last ned mal",
  importDropFile: "Slipp en CSV-fil her, eller velg en.",
  importChooseFile: "Velg CSV-fil",
  importChosenFile: "Valgt: {{name}}",
  importAllowDuplicateIdentity: "Tillat at en kunde har samme juridiske identitet som en annen kunde",
  importCheck: "Kontroller",
  importCheckCounts: "{{rows}} rader — {{created}} ville blitt opprettet, {{updated}} ville blitt oppdatert, {{failed}} har feil",
  importCheckIntro: "Ingenting er lagret ennå: dette er hva en import av filen ville gjort.",
  importNothingToImport: "Ingen rader i filen kan importeres. Rett radene nedenfor og kontroller på nytt.",
  importErrorRow: "Rad",
  importErrorColumn: "Kolonne",
  importErrorMessage: "Feil",
  importRun: "Importer",
  importDone: "Importen er ferdig",
  importDoneCounts: "{{rows}} rader — {{created}} opprettet, {{updated}} oppdatert, {{failed}} feilet",
  importDownloadFailedRows: "Last ned radene som feilet",
  importFailedRowsHint:
    "Radene som feilet, akkurat som de var, med en error-kolonne lagt til. Rett dem og importer den filen.",
  importCouldNotRun: "Filen kunne ikke importeres",
  importClose: "Lukk",
```

- [ ] **Step 7: Run everything, show the tests can fail, commit**

```bash
cd /home/anders/projects/vantigo/vantigo
F="apps/customers/frontend/src/lib/csv.ts apps/customers/frontend/src/lib/csv.test.ts \
 apps/customers/frontend/src/api/customers.ts apps/customers/frontend/src/api/request.ts apps/customers/frontend/src/api/import-export.ts \
 apps/customers/frontend/src/api/import-export.test.ts apps/customers/frontend/src/pages/-customer-import-modal.tsx \
 apps/customers/frontend/src/pages/-customer-import-modal.test.tsx apps/customers/frontend/src/pages/customers.index.tsx \
 apps/customers/frontend/src/pages/-customers.index.test.tsx apps/customers/frontend/src/i18n.ts \
 apps/host/frontend/src/routes/customers/-customers-list.tsx apps/host/frontend/src/routes/customers/customers-list-route.test.tsx"
mise exec -- bunx biome check --write $F
mise exec -- bun run --cwd apps/customers/frontend test && mise exec -- bun run --cwd apps/host/frontend test
mise exec -- bun run --cwd apps/customers/frontend typecheck && mise exec -- bun run --cwd apps/host/frontend typecheck
mise exec -- bun run --cwd apps/customers/frontend lint
mise exec -- bun run translations:check && mise exec -- bun run i18n:test
git status --short
```
Expected: PASS everywhere; `routeTree.gen.ts` does not move.

Prove each new test can fail, restoring after each: in `parseCsv` number every record (drop the all-blank `continue`) — the csv test and the modal's failed-rows assertion go red (row 3 becomes 4); in `failedRowsCsv` always append the column — the re-run test goes red; in `downloadCustomersCsv` drop `page: undefined, pageSize: undefined` — the api test and the index Export test go red on `page=3`/`page=2`; in `importCustomers` drop the `?? null` — the api test goes red on `column: undefined`; in the modal compute `importable` as `check !== null` — the nothing-importable test goes red; in the host wrapper pass `["customers:create", "customers:update"]` for `canImport` — the host test goes red on the create+update case; in `downloadCsv` delete the `if (response.status === 401) await handleUnauthorized();` line — the sign-out test goes red; in the modal delete `if (!dryRun) setCheck(null);` — the part-way test goes red on Import being enabled. Say what each printed.

```bash
cd /home/anders/projects/vantigo/vantigo
cat > /tmp/claude-1000/msg-import-export-6.txt <<'EOF'
feat(customers-ui): the customer list exports its view and imports a checked file

The list's header gains Export, which downloads the current filter and
sort as the customers file, and Import, which opens a modal: pick or drop
a file (with the template a click away), Check it against the server's
dry run — counts and an errors table of row, column and problem — then
Import once a row would succeed, and afterwards download the failed rows:
the original rows with an error column, built in the browser from the
file it still holds, ready to fix and import on their own. The list
refreshes when an import completes. The host passes canExport from
customers:view and canImport from customers:create and customers:update
together. en and nb.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
PATHS="$F"
git add $PATHS && git commit -F /tmp/claude-1000/msg-import-export-6.txt -- $PATHS
git show --stat HEAD && git status --short
```
If biome reformatted a file outside `PATHS`, look at it before adding it.

---

### Task 7: Verify the whole branch and open the PR

- [ ] **Step 1: The whole suite, as CI runs it**

```bash
cd /home/anders/projects/vantigo/vantigo && gh run list --branch main --limit 5   # is main already red? say so in the report if it is
cd /home/anders/projects/vantigo/vantigo/apps/server
export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'
mise exec -- gofmt -l internal && mise exec -- go vet ./... && mise exec -- go build ./...
mise exec -- golangci-lint run ./internal/customers/...   # CI's lint: an unused helper fails here, not in the pre-commit hook
taskset -c 0-3 mise exec -- go test -count=1 ./... 2>&1 | tail -40; echo "exit ${PIPESTATUS[0]}"
mise exec -- go generate ./... >/dev/null 2>&1; cd /home/anders/projects/vantigo/vantigo && git status --short   # clean but for go.mod/go.sum (and the plan file, if the controller has not committed it yet)
mise exec -- bun run gen:client && git status --short                                                         # still clean
mise exec -- bun run --cwd apps/customers/frontend test && mise exec -- bun run --cwd apps/host/frontend test
mise exec -- bun run --cwd apps/customers/frontend typecheck && mise exec -- bun run --cwd apps/host/frontend typecheck
mise exec -- bun run --cwd apps/customers/frontend lint && mise exec -- bun run --cwd apps/host/frontend lint
mise exec -- bun run translations:check && mise exec -- bun run i18n:test
mise exec -- bunx biome check apps/customers/frontend/src apps/host/frontend/src
cd apps/server && mise exec -- go run ./internal/openapi/cmd/contract coverage -corpus ../../openapi/testdata/exchanges -out ../../openapi/COVERAGE.md
cd /home/anders/projects/vantigo/vantigo && git status --short -- openapi/COVERAGE.md   # committed in Tasks 2 and 4: must print nothing
```
`taskset -c 0-3` because the race detector and 44 CPUs disagree about this database's connection limits; drop it if the suite is green without it. The full run includes `TestPostCustomersImport_AtTheCap` (not `-short`): compare its logged total with the one Task 5 wrote into the docs (and the timing rule's verdict). `main` may already be red for reasons that are not ours — if a failure is in a module this branch never touched, check it against `git log origin/main` and say so rather than fixing it here.

- [ ] **Step 2: Read the branch as a reviewer would**

```bash
cd /home/anders/projects/vantigo/vantigo
git log --oneline main..HEAD
git diff --stat main..HEAD
git diff main..HEAD -- openapi/customers.yaml
git diff main..HEAD -- apps/server/internal/customers/addresses.go apps/server/internal/customers/tags.go apps/server/internal/customers/group_membership.go
```
Check, by eye: the design and plan commits plus six task commits, each trailer `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>`; nothing under `openapi/testdata/exchanges/`; `go.mod`/`go.sum` still untracked; no existing `required:` list changed; no migration; Task 3's diff moves code without changing a statement, a lock or an event.

- [ ] **Step 3: Open the PR**

```bash
cd /home/anders/projects/vantigo/vantigo
git push -u origin feat/customers-import-export
cat > /tmp/claude-1000/pr-import-export.md <<'EOF'
## Customers CSV import and export (phase 6, delivery A)

Customers leave and arrive as one CSV file. Decided in
`docs/superpowers/specs/2026-09-24-customers-import-export-design.md` (D1–D5).

- **One file**: the expenses payroll export's form verbatim (semicolons, BOM, CRLF,
  RFC 4180, decimal comma, the formula guard), headers are the API's JSON names.
- **`GET /customers/export`** — the list as the caller sees it: its filters and
  sort, capped at 5000 (more is a 400 asking for a narrower filter), the legal
  identity's columns absent without `customers:legal-identity-view`; everything
  beyond the list's columns read in bulk. **`GET /customers/import/template`** — the
  header alone.
- **`POST /customers/import`** — multipart `file`, ≤ 5 MB and 5000 rows. A number
  updates, a blank creates; each group in the file is replaced whole through its
  endpoint's own write function (extracted, with every handler now calling its
  own), a group not in the file is left alone; group and tags by name. No import
  key: a column the sender could not write by hand refuses the file. Each row its
  own transaction; `dryRun` defaults to true and runs every row the same way,
  rolled back instead of committed; two rows of one file creating one identity are
  refused on the second in both runs. The operation wants create, update and view.
- **Frontend**: Export and Import on the list, the Import modal (Check → Import →
  Download failed rows, built client-side), `canExport`/`canImport` from the host;
  en + nb.

At the cap: see the timing recorded in docs/customers.md's dry-run paragraph (and the row cap, if the timing rule lowered it).
Contract: three operations, two new schemas; no existing schema changed, no
migration, no permission key. The frozen corpus is untouched and still validates.

Decisions on the record for review: a multi-column group comes whole or the file
is refused; the dry run is the real run rolled back row by row, with an in-file
duplicate-identity check; the import wants view as well as create and update; a
blank name on an update is an error; a repeated identity
keeps its source; the importer ignores an `error` column (the failed-rows file's);
Import is enabled once any row would succeed; a non-refusal database error is a
500 for the request.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
gh pr create --base main --head feat/customers-import-export \
  --title "Customers CSV import and export (phase 6, delivery A)" \
  --body-file /tmp/claude-1000/pr-import-export.md
```
If the timing rule lowered `maxImportRows`, add one line to the body naming the new cap and the measured time. `gh pr edit` is broken in this environment: to change the body afterwards use `gh api -X PATCH repos/:owner/:repo/pulls/<n> -F body=@/tmp/claude-1000/pr-import-export.md` (`-F`, which reads the file; `-f` would send the literal string). Do not merge — the user does that.

- [ ] **Step 4: Report**

Say: the PR's number and URL; each test shown able to fail and what the mutation printed; the cap test's total and whether the timing rule lowered `maxImportRows`; anything the generated code disagreed with this plan about (sqlc's and oapi-codegen's names in Tasks 2 and 4, whether `TestServeMuxConflictsArePinned` wanted a pin); and whether `main` was already red. Plus the places this branch decides what the spec left open, for the user's verdict:

- a multi-column group (identity, contact info, an address, billing) comes whole or the file is refused, rather than an absent column reading as blank or as "keep"; `name`/`type`/`status` are each optional;
- the dry run is the real run rolled back row by row (controller ruling; spec D3 updated), plus an in-file duplicate-identity check before any row runs; a dry run still cannot see other effects of earlier rows, which the real run refuses cleanly;
- the import wants `customers:view` as well as create and update (controller ruling), and `canImport` follows;
- a blank `name` cell on an update is a row error; an absent `name` column keeps the name;
- the row cap as the timing rule left it (5000, or the lower number and why);
- a row repeating the identity on file keeps its source (brreg stays brreg), so the round trip writes nothing;
- the importer ignores an `error` column beside D1's four, so the failed-rows file re-imports as it is;
- Import is enabled once the check found any row that would succeed, not only when every row passed;
- a database error that is not a refusal is a 500 for the whole request; rows a real run already committed stay committed.
