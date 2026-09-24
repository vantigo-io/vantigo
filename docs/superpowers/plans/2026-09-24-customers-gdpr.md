# GDPR for person customers (phase 6, delivery C) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A **person** customer's data can be handed to them in one file, and anonymised on a date a person chose — the row stays (its number, its place in projects and supply periods, the shape of its history), the person disappears from it — and the module's phase-1 promise, never a fødselsnummer, becomes a rule the validator enforces. One new permission key (`customers:personal-data`), one migration (`00030`), three operations (`GET /customers/{id}/personal-data`, `PUT`/`DELETE /customers/{id}/anonymisation`), three event types, one worker (`customers-anonymisation`), one many-provider contract slot (`contracts.CustomerPersonalData`), one new refusal (`customer_anonymised`).

**Architecture:** `validateLegalIdentity` gains the national-identity-number refusal beside the organisation-number rule it already carries, so the create body, the legal-identity PUT and the CSV import refuse it through the one validator. `contracts.CustomerPersonalData` (`ExportCustomerData` outside any transaction, `EraseCustomerData` inside the caller's) is the second sanctioned cross-module direction, `docs/module-boundaries.md` rule 9; `module.Module.CustomerPersonalData` is a many-provider slot collected — from every module given, enabled or not, each under its module's name — by `module.Compose` (for the export, which runs in a Mount) **and** by `module.Workers` (for the worker, since worker mode never composes). Communications, energy and projects implement it in their own packages over their own sqlc files; communications' erase reuses the retention worker's delete machinery and cleanup ledger through a function extracted from `CleanupBatch`. In customers, migration `00030` adds `anonymise_on`/`anonymised_at`; `personal_data.go` builds the export in one read-only snapshot plus the pool decoration and each module's section, streamed as a JSON attachment (the CSV download's shape); `anonymisation.go` schedules and cancels under the customer's lock (restoring or retyping a customer cancels its schedule in the same transaction) and holds the per-customer anonymisation — the row cleared, addresses, Peppol answer and registry record deleted, associations detached and orphan contacts deleted, every timeline entry and revision rewritten set-based in SQL, merge chains followed, each module's eraser inside the transaction, then `anonymised_at`, the revision and `customer.anonymised`; `anonymisation_worker.go` is the registry feed's lease shape running at most 50 due customers a cycle, each in its own retried transaction. The merged-away refusal generalises into a read-only refusal (`customer_merged` or `customer_anonymised`) behind the same lock-time check. `anonymisation?` reaches `SafeCustomerResponse` through the batched decoration. The frontend gains a Personal data menu on a person's page behind `canManagePersonalData`, the scheduled and anonymised banners (the latter hiding every edit, the merged-away banner's shape), the three new timeline event labels, the read-only refusal's sentence, the host prop and the admin catalog label; en + nb.

**Tech Stack:** Go 1.27 (pgx, sqlc, oapi-codegen strict server), PostgreSQL 18, React + Mantine 9 + TanStack Query, vitest, bun, mise.

**Spec:** `docs/superpowers/specs/2026-09-24-customers-gdpr-design.md` (D1–D6 + "Out of scope" + "Testing"). Read it first; it is binding. Research with file:line pointers: `.superpowers/sdd/2026-09-24-customers-gdpr/context-for-design.md`. The shapes this delivery copies: `apps/server/internal/contracts/references.go` (the holder contract and its doc voice), `apps/server/internal/module/compose.go:232-257` (the holders' collection), `apps/server/internal/modtest/modtest.go:242-251` (`WithCustomerReferenceHolders`), `apps/server/internal/customers/values.go:117-147,452-501` (`validNorwegianOrgNumber`, `validateLegalIdentity`), `merge.go` (the locked multi-table write, `lockWritableCustomer`, the coded `CustomerConflictProblem`, a refusal's body travelling out of a rolled-back transaction), `peppol_recheck_worker.go` (worker, lease, `RunCycle`), `csvexport.go:38-66` (the download response), `owner.go:72-230` (the batched decoration), `communications/retention.go:190-330` (the delete machinery and the cleanup ledger), `apps/customers/frontend/src/api/import-export.ts` (the download), `pages/customers.$customerId.tsx` (the header actions and the merged-away banner).

## Global Constraints

- Branch `feat/customers-gdpr`. Never commit to `main`, never merge, never `--no-verify`.
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
- `validateLegalID` REFUSES an eleven-digit Norwegian national identity number (fødselsnummer or D-number: both mod-11 check digits pass) for `country=no`+`type=person` — field error `legalId` "A Norwegian national identity number is never stored here"; other person identifiers stay free text. The one validator serves the identity PUT, create, and the CSV import.
- `contracts.CustomerPersonalData` is a SECOND sanctioned cross-module direction with its own rule (`docs/module-boundaries.md` rule 9), NOT a method on the merge holder: `ExportCustomerData(ctx, id) (any, error)` runs outside any transaction; `EraseCustomerData(ctx, tx, id) ([]ErasedData{Kind, Count}, error)` runs INSIDE the customers anonymisation transaction on the module's own schema (the holder's rules: own code, no own tx, no lookups). `module.Module.CustomerPersonalData` is a many-provider slot collected from EVERY module Compose is given into `Deps.CustomerPersonalData`. Implementations: communications (export conversations/messages/attachment names; erase them via the retention cleanup ledger), energy (export supply periods + metering-point address; erase no-op), projects (export code/name/status/dates; erase no-op).
- New sensitive key `customers:personal-data` (+ `customers:view`) gates `GET /customers/{id}/personal-data` (application/json download, PERSON customers only → 409 `personal_data_not_a_person`), `PUT /customers/{id}/anonymisation` `{anonymiseOn}` (person + ARCHIVED only → 409 `personal_data_customer_active`; today or later; events `customer.anonymisation_scheduled`/`_cancelled`) and `DELETE …/anonymisation`. Migration 00030: `anonymise_on date NULL`, `anonymised_at timestamptz NULL`, partial index. NO default date.
- Worker `customers-anonymisation` (`CUSTOMERS_ANONYMISATION_ENABLED` default on, `_POLL` default 24h, advisory lease like the registry feed, ≤ 50 per cycle, `RunCycle` testable): each due customer in ONE transaction, row locked: name → "Anonymised person" (number kept), identity + contact info + website cleared, billing identifiers cleared (invoiceEmail, reminderEmail, peppolId, gln, buyerReference; terms/currency/language/delivery kept), addresses + peppol lookup deleted, associations detached and orphan contacts deleted, every timeline entry KEPT with content anonymised (manual summary/note → "[anonymised]"; generated payload keys `name`, `identity`, `contactInfo`, `billingProfile`, `before`, `after`, `changes`, `absorbed`, contact names → "[anonymised]"; ids/dates/counts kept; revisions the same), merge chains handled (customers merged into this one anonymised in the same run; the survivor's `absorbed` block rewritten if this one was merged away), each module's `EraseCustomerData` inside the tx, then `anonymised_at` + revision bump + `customer.anonymised` recorded LAST (generated fallback actor). A failing module rolls that customer back; the worker moves on.
- An anonymised customer is read-only (409 `customer_anonymised` through the same lock-time check as `customer_merged`), stays archived, exports what is left, cannot be scheduled again.
- Frontend: a `canManagePersonalData` prop (`customers:personal-data`); the Personal data menu on a PERSON customer's page; banners (scheduled / anonymised); the timeline renders "[anonymised]" plainly; the admin catalog label; en + nb.
- Contract changes to EXISTING schemas stay additive/optional; new schemas may have required fields; the frozen corpus (`openapi/testdata/exchanges/customers.jsonl`) is untouched and must still validate.
- After any `queries/*.sql` or migration change: `mise exec -- go generate ./...` from `apps/server`; new migrations go in `sqlc.yaml` and `internal/db/schema_test.go`; new operations need `operationId`, `x-vantigo-access`, module-test coverage, a `KnownServeMuxConflicts` pin where they conflict, and a COVERAGE.md regeneration.
- Run `mise exec -- bunx biome check --write <files>` on touched frontend files before committing.

---

**How this plan reads the spec where it leaves a choice open.** Each is on the record for the user's verdict (Task 9 Step 4 repeats them):

1. **The refusal sits in `validateLegalIdentity`, and its key follows each door.** `validateLegalID` sees the id alone — it cannot know the country or the type — so the check goes where the organisation-number rule already is, the one place both are known. D1's "field error `legalId`" is the CSV column's name; the create/update body keys it `identity.id` and the legal-identity PUT `id`, as every other identity error already is. Spaces, hyphens and full stops are taken out before the digits are counted ("010190 12345" is a fødselsnummer as surely as eleven digits run together); only the two check digits decide — the date is not read as a date — so D-, H- and synthetic numbers are all caught, and the refusal echoes no number back.
2. **`Deps.CustomerPersonalData` is `[]contracts.CustomerPersonalDataHolder{Module, Data}`.** The export files each module's section under the module's name (D3's `modules: {communications, energy, projects}`) and the interface D2 fixes carries no name, so Compose — which knows it — pairs them. The slot is collected by Compose **and** by `module.Workers`: the worker is the one caller of `EraseCustomerData`, and in worker mode nothing composes.
3. **Energy and projects answer their kind at zero** (`energy.supplyPeriods`, `projects.projects`) rather than nothing, the merge holder's "report every kind you handle", so `customer.anonymised` says every module was asked; D2's "erase: nothing" is kept — neither writes a row.
4. **The timeline is rewritten set-based, in SQL, one statement per table** — `jsonb_each` + `jsonb_object_agg` over the listed top-level keys, applied to every payload — not per entry in Go: a customer can have thousands of entries, and one `UPDATE` inside the locked transaction is one round trip. Manual entries' `summary` and `note` become "[anonymised]" and their `sourceUrl` is cleared (a link is content too). A generated entry's **summary** is rewritten as well unless its event type is on an allow-list of summaries built from nothing personal (`customer.status_changed`, `.type_changed`, `.contact_info_updated`, `.billing_profile_updated`, `.peppol_lookup`, `.tags_changed`, `.group_changed`, `.owner_changed` and this delivery's three) — "Customer created: Kari Nordmann" would otherwise keep the name D4 removes from the payload beside it. The key list applies to every payload alike, so a status change's `before`/`after` go too; its summary keeps the story. `actor_display` is staff and stays.
5. **What takes a customer out of what anonymisation needs cancels its schedule.** Restoring it (`PUT /customers/{id}` or a CSV row to a status other than archived) or changing its type away from person drops the date in the same transaction and records `customer.anonymisation_cancelled` — otherwise re-archiving a customer months later would anonymise it the next night. A merged-away customer cannot be scheduled (409 `customer_merged`, the read-only rule), but a schedule made before its merge can still be cancelled (the one write it takes); absorbing an anonymised customer is refused 409 `customer_anonymised`.
6. **Merge chains.** Anonymising S anonymises every customer merged into it in the same transaction, each with its own row cleared and its own `customer.anonymised` event; S's whole timeline — the moved history and the `customer.merged` entries describing them — is rewritten with S's. A due customer that was merged away after it was scheduled rewrites its survivor's `customer.merged` entry for it (`absorbed` and the summary naming it), and nothing else of the survivor's: its moved history is the survivor's now, anonymised with the survivor. A chain member takes the due customer's date as its `anonymiseOn`, whatever its own schedule said — the chain is one anonymisation — so its page reads "Anonymised on". Every anonymised customer also leaves its group (the controller's ruling on the pre-flight review, amending D4): it refuses every write, so a group it still counted in could never be deleted — the merged-away precedent (#123).
7. **There is no form in the UI that writes a person's legal identity** — the create/edit form sends none for a person, and the registry card is a business's — so D5's "the identity form's `legalId` shows the new refusal" is the CSV import modal's errors table, which shows the server's sentence on `legalId`; a test pins it. No client-side mirror of the check is added.
8. **The export's timeline is every entry, deleted ones included** (their `state` says so) — a soft-deleted note is still held — and a module whose section is nil has no key under `modules`. Scheduling and cancelling answer the customer (`SafeCustomerResponse`, 200) so the page syncs its revision; the body carries no `revision`, and scheduling the date already set, or cancelling nothing, writes nothing.
9. **Counts.** `customer.anonymised`'s `erased` lists this module's four kinds first — `customers.addresses` (deleted), `customers.contactAssociations` (detached), `customers.contacts` (orphans deleted), `customers.timelineEntries` (rewritten, every state) — then each module's in Compose order. The Peppol answer and the registry record are deleted without a count, as the merge deletes them. Communications counts `conversations` and `messages` deleted, `attachments` (object keys handed to the cleanup ledger: attachments, staged uploads, raw payloads), `conversationSuggestions` cleared and `conversationCandidates` deleted — a suggestion naming the person on somebody else's conversation loses its reasoning too, which may name them.
10. **Small ones:** the partial index is `WHERE anonymise_on IS NOT NULL AND anonymised_at IS NULL` (D4's predicate plus the one that keeps every unscheduled customer out of it); orphan contacts are locked `FOR UPDATE` before the `NOT EXISTS` delete, so an attach cannot slip one back in, and the transaction is retried on a deadlock like the merge's; `anonymise_on` stays set on an anonymised customer, as the record of what was scheduled; the integration test composes customers and projects only, for the merge test's reason.

## File Structure

| File | Responsibility |
| --- | --- |
| `apps/server/internal/customers/values.go`, `values_test.go`, `export_test.go`, `national_identity_test.go` | the fødselsnummer refusal (Task 1) |
| `apps/server/internal/contracts/personal_data.go`, `references.go` | `CustomerPersonalData`, `ErasedData`, `CustomerPersonalDataHolder` (Task 2) |
| `apps/server/internal/module/module.go`, `compose.go`, `workers.go`, `compose_test.go`, `workers_test.go`, `apps/server/internal/modtest/modtest.go` | the slot, its collection, the harness seam (Task 2) |
| `docs/module-boundaries.md` | rule 9 (Task 2) |
| `apps/server/internal/{communications,energy,projects}/customer_personal_data.go`, `customer_personal_data_test.go`, `queries/customer_personal_data.sql` (+ generated `store/customer_personal_data.sql.go`), `module.go`; `communications/retention.go` | the three implementations (Task 3) |
| `docs/communications.md`, `docs/projects.md` | their paragraphs (Task 3) |
| `apps/server/internal/db/migrations/00030_customers_personal_data.sql`, `internal/customers/sqlc.yaml`, `internal/db/schema_test.go` | the two columns (Task 4) |
| `apps/server/internal/customers/queries/personal_data.sql`, `queries/addresses.sql`, `queries/merge.sql` (+ generated `store/*.go`) | every new statement (Tasks 4–6) |
| `openapi/customers.yaml` (+ generated `internal/openapi/specs/customers.yaml`, `internal/customers/gen/api.gen.go`, `apps/customers/frontend/src/api-schema.d.ts`), `openapi/COVERAGE.md`, `apps/server/internal/openapi/openapi_test.go` | three operations, five schemas, `anonymisation` (Tasks 4, 5) |
| `apps/server/internal/customers/personal_data.go`, `owner.go`, `customers.go`, `module.go`, `personal_data_test.go`, `module_test.go` | the export, the decoration, the permission (Task 4) |
| `apps/server/internal/customers/anonymisation.go`, `timeline_events.go`, `customers.go`, `customer_type.go`, `merge.go` (+ the renamed callers), `import.go`, `anonymisation_test.go` | scheduling, cancelling, and the read-only refusal (Task 5) |
| `apps/server/internal/customers/anonymisation_worker.go`, `anonymisation.go`, `anonymisation_worker_test.go`, `module.go`, `export_test.go`; `internal/config/config.go`, `config_test.go`; `cmd/vantigo/main_test.go`; `deploy/compose/vantigo.env.example`; `internal/integration/personal_data_test.go` | the anonymisation and its worker (Task 6) |
| `docs/customers.md`, `ROADMAP.md` | D6 (Task 7) |
| `apps/customers/frontend/src/api/{customers.ts,customers.test.ts,merge.test.ts,import-export.ts,personal-data.ts,personal-data.test.ts}`, `lib/customer-write-error{,.test}.ts`, `pages/{-customer-personal-data.tsx,-customer-personal-data.test.tsx,customers.$customerId.tsx,-customer-timeline.tsx,-customer-timeline.test.tsx,-customer-form-modal.tsx,-customer-form-modal.test.tsx,-customer-merge-modal.tsx,-customer-import-modal.test.tsx}`, `i18n.ts` | the menu, the banners, the timeline, the refusals, the strings (Task 8) |
| `apps/host/frontend/src/routes/customers/-customer-detail-layout.tsx`, `customer-detail-route.test.tsx`, `apps/host/frontend/src/catalogs/admin.ts`, `catalogs/admin.test.ts` | `canManagePersonalData`, the permission's labels (Task 8) |

---

### Task 1: A national identity number is refused (D1)

The promise the module has made since phase 1 becomes a rule: `no` + `person` + eleven digits whose two check digits pass is a 400, through the one validator every identity write uses.

**Files:**
- Modify: `apps/server/internal/customers/values.go`, `apps/server/internal/customers/values_test.go`, `apps/server/internal/customers/export_test.go`, `docs/customers.md`
- Create: `apps/server/internal/customers/national_identity_test.go`
- Read first (do not change): `values.go:117-147` (`validNorwegianOrgNumber`), `values.go:452-501` (`validateLegalIdentity`), `import.go:470-490` (the CSV identity path and `identityColumns`), `csvimport_test.go:40-135` (`csvFileOf`, `postImport`, `legalHeader`, `cells`)

**Interfaces:**
- Produces Go: `isNorwegianNationalID(s string) bool`, `nationalIDRefused` (the message constant), `customers.NationalIDForTest(birth string, individual int, dNumber bool) string` (test-only, export_test.go).
- Wire: a 400 on `identity.id` (create/update), `id` (`PUT …/legal-identity`) or a CSV row error on `legalId`, message "A Norwegian national identity number is never stored here".
- Consumes: nothing new.

- [ ] **Step 1: The test builder**

Every number these tests use is built, never pasted: `NationalIDForTest` does the mod-11 arithmetic itself — not through `values.go`, so a wrong weight there cannot hide behind a builder that shares it — and the tests hand it a **synthetic** birth date (the month plus 80, the range Skatteetaten keeps for test persons), so no number it builds can be anybody's.

In `apps/server/internal/customers/export_test.go`, change the import block to

```go
import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/module"
)
```

and append:

```go
// NationalIDForTest builds an eleven-digit Norwegian national identity number
// for a test (customers GDPR design D1): birth is six digits, DDMMYY, and the
// individual number is the first one from individual up whose two mod-11
// check digits both exist — a remainder that would make either one 10 skips
// that individual number, as the population register does. dNumber raises the
// first digit by four, which is all a D-number is.
//
// It is its own arithmetic, deliberately not values.go's: a test that built its
// numbers with the function under test could not catch a wrong weight in it.
// Callers pass a synthetic birth date — the month plus 80, the range
// Skatteetaten keeps for test persons — so nothing built here is a real
// person's number.
func NationalIDForTest(birth string, individual int, dNumber bool) string {
	firstWeights := []int{3, 7, 6, 1, 8, 9, 4, 5, 2}
	secondWeights := []int{5, 4, 3, 2, 7, 6, 5, 4, 3, 2}
	check := func(digits, weights []int) int {
		sum := 0
		for i, w := range weights {
			sum += digits[i] * w
		}
		// 11 - 0 is 11, which is the check digit 0; 11 - 1 is 10, which no
		// number has, and the caller skips it.
		return (11 - sum%11) % 11
	}
	for n := individual; n < 1000; n++ {
		digits := make([]int, 0, 11)
		for _, r := range fmt.Sprintf("%s%03d", birth, n) {
			digits = append(digits, int(r-'0'))
		}
		if dNumber {
			digits[0] += 4
		}
		first := check(digits, firstWeights)
		if first == 10 {
			continue
		}
		digits = append(digits, first)
		second := check(digits, secondWeights)
		if second == 10 {
			continue
		}
		digits = append(digits, second)
		var b strings.Builder
		for _, d := range digits {
			b.WriteByte(byte('0' + d))
		}
		return b.String()
	}
	panic("NationalIDForTest: no individual number from " + strconv.Itoa(individual) + " up gives two check digits for " + birth)
}
```

- [ ] **Step 2: Write the failing unit tests**

In `apps/server/internal/customers/values_test.go`, replace the whole `t.Run("person type keeps the unchanged rule", …)` sub-test at the end of `TestValidateLegalIdentity_NorwegianOrgNumberRule` with:

```go
	t.Run("person type keeps the plain rule for anything that is not a national identity number", func(t *testing.T) {
		// Eleven digits with a hyphen, but the second check digit fails (it
		// would be 3): free text, as it always was.
		got, errs := validateLegalIdentity("no", "person", "010170-12345", "Kari Nordmann", "manual")
		if errs != nil {
			t.Fatalf("validateLegalIdentity: unexpected errors %v", errs)
		}
		if got.ID != "010170-12345" {
			t.Errorf("ID = %q, want unchanged \"010170-12345\"", got.ID)
		}
	})
```

and append at the end of the file:

```go
// TestValidateLegalIdentity_RefusesANorwegianNationalIdentityNumber is customers
// GDPR design D1: for a Norwegian private person, eleven digits whose two
// mod-11 check digits pass are refused — a fødselsnummer and a D-number alike,
// run together or grouped the way people write them — while every near miss
// stays the free text a person's legal id always was. The refusal names no
// number: echoing it back would put it in a browser's error state and a log.
func TestValidateLegalIdentity_RefusesANorwegianNationalIdentityNumber(t *testing.T) {
	const refusal = "A Norwegian national identity number is never stored here"
	fnr := NationalIDForTest("018190", 100, false)
	dNumber := NationalIDForTest("018190", 100, true)
	for name, id := range map[string]string{
		"a fødselsnummer":       fnr,
		"a D-number":            dNumber,
		"grouped with a space":  fnr[:6] + " " + fnr[6:],
		"grouped with a hyphen": fnr[:6] + "-" + fnr[6:],
		"with full stops":       fnr[:2] + "." + fnr[2:4] + "." + fnr[4:],
	} {
		t.Run("refused/"+name, func(t *testing.T) {
			_, errs := validateLegalIdentity("no", "person", id, "Kari Nordmann", "manual")
			if got := errs["id"]; len(got) != 1 || got[0] != refusal {
				t.Errorf("errors[\"id\"] = %v, want [%q]", got, refusal)
			}
			if len(errs) != 1 {
				t.Errorf("errors = %v, want only id", errs)
			}
			if strings.Contains(fmt.Sprint(errs), fnr[6:]) {
				t.Errorf("errors = %v: the refusal names the number", errs)
			}
		})
	}

	wrongSecond := fnr[:10] + strconv.Itoa((int(fnr[10]-'0')+1)%10)
	for name, tc := range map[string]struct{ country, typ, id string }{
		"a second check digit that fails": {"no", "person", wrongSecond},
		"ten digits":                      {"no", "person", fnr[:10]},
		"twelve digits":                   {"no", "person", fnr + "0"},
		"a letter among the digits":       {"no", "person", fnr[:10] + "x"},
		"a passport number":               {"no", "person", "FX1234567"},
		"another country's person":        {"se", "person", fnr},
	} {
		t.Run("kept/"+name, func(t *testing.T) {
			got, errs := validateLegalIdentity(tc.country, tc.typ, tc.id, "Kari Nordmann", "manual")
			if errs != nil {
				t.Fatalf("validateLegalIdentity(%q): unexpected errors %v", tc.id, errs)
			}
			if got.ID != strings.ToLower(tc.id) {
				t.Errorf("ID = %q, want %q", got.ID, strings.ToLower(tc.id))
			}
		})
	}

	// The organisation-number rule is untouched: a business keeps its own.
	if got, errs := validateLegalIdentity("no", "business", "923609016", "Acme AS", "manual"); errs != nil || got.ID != "923609016" {
		t.Errorf("a Norwegian business = %+v, %v; want the organisation number stored", got, errs)
	}
}

// TestIsNorwegianNationalID_AnyOneDigitChangedIsNotOne: every weight is
// between 1 and 9 and 11 is prime, so changing any one digit of a valid number
// moves the check it takes part in — the rule refuses exactly the numbers
// whose check digits pass, not "eleven digits" in general.
func TestIsNorwegianNationalID_AnyOneDigitChangedIsNotOne(t *testing.T) {
	for _, valid := range []string{NationalIDForTest("298292", 400, false), NationalIDForTest("318299", 7, true)} {
		if !isNorwegianNationalID(valid) {
			t.Fatalf("isNorwegianNationalID(%q) = false, want true", valid)
		}
		for i := range valid {
			for d := byte('0'); d <= '9'; d++ {
				if d == valid[i] {
					continue
				}
				changed := valid[:i] + string(d) + valid[i+1:]
				if isNorwegianNationalID(changed) {
					t.Errorf("isNorwegianNationalID(%q) = true: digit %d changed from %c to %c", changed, i, valid[i], d)
				}
			}
		}
	}
}
```

Add `"strconv"` to `values_test.go`'s import block if it is not there (`fmt` and `strings` already are).

- [ ] **Step 3: Run them and watch them fail**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'
mise exec -- go test -count=1 -run 'NationalIdentity|NationalID|NorwegianOrgNumberRule' ./internal/customers/
```
Expected: FAIL to compile — `undefined: isNorwegianNationalID`.

- [ ] **Step 4: The check and the refusal**

In `apps/server/internal/customers/values.go`, directly after `validNorwegianOrgNumber`, add:

```go
// nationalIDRefused is customers GDPR design D1's refusal: a Norwegian
// private person's legal id is never their national identity number.
// Datatilsynet's advice for ordinary customer administration is not to hold
// one at all, and this module has promised since its foundation never to be a
// fødselsnummer field. The sentence names no number, deliberately.
const nationalIDRefused = "A Norwegian national identity number is never stored here"

// nationalIDFirstWeights and nationalIDSecondWeights are the two weight rows
// of a Norwegian national identity number's mod-11 check digits: the tenth
// digit is computed over the first nine, the eleventh over the first ten. A
// D-number is the same number with its first digit raised by four (the day
// plus 40), and its check digits are computed the same way — so one rule
// catches both, and an H-number or one of Skatteetaten's synthetic test
// numbers besides.
var (
	nationalIDFirstWeights  = []int{3, 7, 6, 1, 8, 9, 4, 5, 2}
	nationalIDSecondWeights = []int{5, 4, 3, 2, 7, 6, 5, 4, 3, 2}
)

// nationalIDCheckDigit is one mod-11 check digit over the leading digits,
// weighted, or -1 when the remainder would make it 10: no number carries that
// check digit, so digits that would need one are not a national identity
// number at all.
func nationalIDCheckDigit(digits string, weights []int) int {
	sum := 0
	for i, w := range weights {
		sum += int(digits[i]-'0') * w
	}
	switch check := 11 - sum%11; check {
	case 11:
		return 0
	case 10:
		return -1
	default:
		return check
	}
}

// isNorwegianNationalID reports whether s is a Norwegian national identity
// number (customers GDPR design D1): once whitespace, hyphens and full stops
// are taken out — "010190 12345" and "010190-12345" are how people write one —
// eleven ASCII digits whose two check digits both pass. Only the check digits
// decide; the first six digits are not read as a date, so every kind of
// number the register issues is caught by the one rule. A false positive costs
// somebody whose foreign id happens to have the shape one retyped value; a
// false negative would store the one number this module promises never to hold.
func isNorwegianNationalID(s string) bool {
	digits := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || r == '-' || r == '.' {
			return -1
		}
		return r
	}, s)
	if len(digits) != 11 {
		return false
	}
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return false
		}
	}
	if first := nationalIDCheckDigit(digits[:9], nationalIDFirstWeights); first < 0 || int(digits[9]-'0') != first {
		return false
	}
	second := nationalIDCheckDigit(digits[:10], nationalIDSecondWeights)
	return second >= 0 && int(digits[10]-'0') == second
}
```

In `validateLegalIdentity`'s doc comment, replace the last sentence — from `Every other combination,` to `keeps validateLegalID's plain non-blank/length rule.` — with:

```go
// Customers GDPR design D1 adds the mirror image for "no" with type
// "person": an id that is a Norwegian national identity number
// (isNorwegianNationalID) is refused outright rather than validated into
// legitimacy, because this module never stores one. Every other combination
// keeps validateLegalID's plain non-blank/length rule, and so does a Norwegian
// person's id that is anything else — a passport number, a foreign id, a
// customer reference.
```

and in its body, directly after the `if errs["country"] == nil && … c == "no" && t == "business" { … }` block, add:

```go
	// The check reads the value as sent, separators and all (the organisation
	// number's whitespace-stripping is for storing it; this is for refusing it),
	// and runs only once country and type are known good, exactly as the
	// business rule above does.
	if errs["country"] == nil && errs["type"] == nil && errs["id"] == nil && c == "no" && t == "person" && isNorwegianNationalID(i) {
		errs["id"] = []string{nationalIDRefused}
	}
```

`values.go` already imports `strings` and `unicode`.

- [ ] **Step 5: Write the failing HTTP test**

Create `apps/server/internal/customers/national_identity_test.go`:

```go
package customers_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/customers"
)

// TestNationalIdentityNumber_IsRefusedByEveryWriteOfAnIdentity is customers
// GDPR design D1 through the three doors an identity comes in by: the create
// body (keyed identity.id, as every nested identity error is), the
// legal-identity PUT (keyed id) and a CSV row (on its legalId column) — one
// validator behind all three, so none of them stores the number, and none of
// them says it back.
func TestNationalIdentityNumber_IsRefusedByEveryWriteOfAnIdentity(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	fnr := customers.NationalIDForTest("018190", 100, false)
	const refusal = "A Norwegian national identity number is never stored here"
	identity := map[string]any{"country": "no", "type": "person", "id": fnr, "name": "Kari Nordmann", "source": "manual"}

	r := c.Do(http.MethodPost, "/api/v1/customers", map[string]any{"name": "Kari Nordmann", "type": "person", "identity": identity})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("create: status %d body %s, want 400", r.Status, r.Body)
	}
	var created validationProblemJSON
	r.JSON(&created)
	if got := created.Errors["identity.id"]; len(got) != 1 || got[0] != refusal {
		t.Errorf("create errors = %v, want identity.id [%q]", created.Errors, refusal)
	}
	if strings.Contains(string(r.Body), fnr[6:]) {
		t.Errorf("create body %s names the number", r.Body)
	}

	person := createCustomerOfType(t, c, "Ola Nordmann", "person")
	r = c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/legal-identity", person.Id), identity)
	if r.Status != http.StatusBadRequest {
		t.Fatalf("legal-identity PUT: status %d body %s, want 400", r.Status, r.Body)
	}
	var put validationProblemJSON
	r.JSON(&put)
	if got := put.Errors["id"]; len(got) != 1 || got[0] != refusal {
		t.Errorf("legal-identity PUT errors = %v, want id [%q]", put.Errors, refusal)
	}

	result := importResultOf(t, postImport(t, c, "?dryRun=false", csvFileOf(
		cells([]string{"name", "type"}, legalHeader),
		[]string{"Kari Nordmann", "person", "no", "person", fnr[:6] + " " + fnr[6:], "Kari Nordmann"},
	)))
	want := []importErrorJSON{{Row: 1, Column: "legalId", Message: refusal}}
	if result.Failed != 1 || result.Created != 0 || fmt.Sprint(result.Errors) != fmt.Sprint(want) {
		t.Errorf("import = %+v, want only %+v", result, want)
	}

	if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE legal_id IS NOT NULL`); n != 0 {
		t.Errorf("%d customers hold a legal id, want none", n)
	}
}
```

- [ ] **Step 6: Run everything, show it can fail, update the docs, commit**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- gofmt -l internal/customers
mise exec -- go vet ./internal/customers/...
mise exec -- go test -count=1 ./internal/customers/...
```
Expected: PASS, the existing identity tests included (`010170-12345`'s second check digit would be 3, so it stays free text).

Prove the tests can fail, restoring after each: delete the new `if … t == "person" && isNorwegianNationalID(i)` block — every `refused/…` case and the HTTP test go red; change `nationalIDFirstWeights`' first weight from 3 to 4 — the refused cases go red (the builder's own arithmetic no longer agrees); drop `r == '-'` from the `strings.Map` — "grouped with a hyphen" goes red; change `return second >= 0 && …` to `return true` — "a second check digit that fails" and the one-digit test go red. Say what each printed.

In `docs/customers.md`, in `## Legal identity and its validation`, replace the `id` row of the table with:

```markdown
| `id` | Non-blank, at most 50 UTF-16 code units, trimmed and lower-cased — **except** when `country` is `no`: for `type` `business` it must be a Norwegian **organisasjonsnummer** (whitespace stripped, then exactly nine digits whose last is the mod-11 check digit — weights 3 2 7 6 5 4 3 2; a remainder that would produce check digit 10 is invalid outright — stored as the nine digits); for `type` `person` it must **not** be a Norwegian national identity number (below). |
```

and replace the paragraph that begins `**Norwegian person identities are deliberately not validated as a fødselsnummer.**` with:

```markdown
**A Norwegian national identity number is refused, never stored.** For `country` `no`
and `type` `person`, an `id` that is a fødselsnummer or a D-number — once spaces,
hyphens and full stops are taken out, eleven digits whose two mod-11 check digits pass
(weights 3 7 6 1 8 9 4 5 2 for the tenth, 5 4 3 2 7 6 5 4 3 2 for the eleventh; a
D-number is the same number with its first digit raised by four) — is a 400, "A
Norwegian national identity number is never stored here", keyed like every other
identity error: `id` on `PUT .../legal-identity`, `identity.id` in a create or update
body, the `legalId` column of a CSV row. Only the check digits decide — the date is not
read as a date — so every kind of number the register issues is caught, and the
sentence names no number back. Any other person identifier (a passport number, a
foreign id, a customer reference) stays free text under the plain rule above.
Datatilsynet's advice for ordinary customer administration is not to hold the number
at all, and this module promised from its foundation never to be a fødselsnummer
field; phase 6 delivery C made the promise a rule
([Personal data and anonymisation](#personal-data-and-anonymisation)). An identity
stored before the rule is not re-validated (below), and is cleared by an
anonymisation like every other identity.
```

```bash
cd /home/anders/projects/vantigo/vantigo
cat > /tmp/claude-1000/msg-gdpr-1.txt <<'EOF'
feat(customers): a Norwegian national identity number is refused as a person's legal id

For country no and type person, an id whose two mod-11 check digits
pass once spaces, hyphens and full stops are taken out is a 400, "A
Norwegian national identity number is never stored here" — a
fødselsnummer and a D-number alike. The check sits beside the
organisation-number rule in validateLegalIdentity, so the create body,
the legal-identity PUT and the CSV import refuse it through the one
validator, each on its own key. The tests build their numbers with
arithmetic of their own, over synthetic birth dates, so none is a real
person's.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
PATHS="apps/server/internal/customers/values.go apps/server/internal/customers/values_test.go \
 apps/server/internal/customers/export_test.go apps/server/internal/customers/national_identity_test.go docs/customers.md"
git add $PATHS && git commit -F /tmp/claude-1000/msg-gdpr-1.txt -- $PATHS
git show --stat HEAD && git status --short
```

(The docs anchor `#personal-data-and-anonymisation` resolves once Task 7 writes the section; that task's link check covers it.)

---

### Task 2: The contract and the second many-provider slot (D2)

The second write direction, with nobody calling it yet: the interface, the slot, its collection in Compose and in Workers, the harness seam, and the boundary rule that says why it is allowed.

**Files:**
- Create: `apps/server/internal/contracts/personal_data.go`
- Modify: `apps/server/internal/contracts/references.go`, `apps/server/internal/module/module.go`, `apps/server/internal/module/compose.go`, `apps/server/internal/module/workers.go`, `apps/server/internal/module/compose_test.go`, `apps/server/internal/module/workers_test.go`, `apps/server/internal/modtest/modtest.go`, `docs/module-boundaries.md`
- Read first (do not change): `compose.go:232-257` (the holders' collection this mirrors), `compose_test.go:1316-1455` (the holder tests these mirror), `workers_test.go:1-60`

**Interfaces:**
- Produces Go: `contracts.CustomerPersonalData` (`ExportCustomerData(ctx context.Context, customerID int32) (any, error)`, `EraseCustomerData(ctx context.Context, tx pgx.Tx, customerID int32) ([]contracts.ErasedData, error)`); `contracts.ErasedData{Kind string; Count int64}`; `contracts.CustomerPersonalDataHolder{Module string; Data contracts.CustomerPersonalData}`; `module.Module.CustomerPersonalData func(module.Deps) contracts.CustomerPersonalData`; `module.Deps.CustomerPersonalData []contracts.CustomerPersonalDataHolder`; `modtest.WithCustomerPersonalData(holders ...contracts.CustomerPersonalDataHolder) modtest.Option`.
- Wire: none.
- Consumes: nothing new.

- [ ] **Step 1: Write the failing Compose and Workers tests**

Append to `apps/server/internal/module/compose_test.go`:

```go
// fakePersonalData is a contracts.CustomerPersonalData with only a name, so a
// test can tell whose landed where. It is never called: Compose collects, and
// only the customers module calls.
type fakePersonalData struct{ name string }

func (*fakePersonalData) ExportCustomerData(context.Context, int32) (any, error) { return nil, nil }

func (*fakePersonalData) EraseCustomerData(context.Context, pgx.Tx, int32) ([]contracts.ErasedData, error) {
	return nil, nil
}

// Customer personal data is the second many-provider slot (customers GDPR
// design D2): every module given may declare one, Compose collects them all in
// the order given, and each is filed under its module's name — the export's
// key for that module's section. Every module's Mount sees the one list.
func TestCompose_CollectsEveryModulesCustomerPersonalDataUnderItsName(t *testing.T) {
	alpha, gamma := &fakePersonalData{name: "alpha"}, &fakePersonalData{name: "gamma"}
	var inAlpha, inBeta []contracts.CustomerPersonalDataHolder

	_, err := compose(Deps{Access: &fakeAccess{}},
		fakeLoad(map[string]string{"alpha": alphaContract, "beta": betaContract, "gamma": gammaContract}),
		Module{
			Name:                 "alpha",
			CustomerPersonalData: func(Deps) contracts.CustomerPersonalData { return alpha },
			Mount: func(d Deps) (http.Handler, error) {
				inAlpha = d.CustomerPersonalData
				return staticHandler("alpha")(d)
			},
		},
		Module{Name: "beta", Mount: func(d Deps) (http.Handler, error) {
			inBeta = d.CustomerPersonalData
			return staticHandler("beta")(d)
		}},
		Module{
			Name:                 "gamma",
			CustomerPersonalData: func(Deps) contracts.CustomerPersonalData { return gamma },
			Mount:                staticHandler("gamma"),
		},
	)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	want := []contracts.CustomerPersonalDataHolder{{Module: "alpha", Data: alpha}, {Module: "gamma", Data: gamma}}
	if !slices.Equal(inAlpha, want) {
		t.Errorf("alpha's Deps.CustomerPersonalData = %v, want alpha's then gamma's", inAlpha)
	}
	if !slices.Equal(inBeta, want) {
		t.Errorf("beta's Deps.CustomerPersonalData = %v, want alpha's then gamma's", inBeta)
	}
}

// A module MODULES leaves out still contributes, for the holders' reason: its
// schema is migrated and still holds what it held about a person, and an
// anonymisation that skipped it would leave that behind for good.
func TestCompose_DisabledModuleStillContributesItsCustomerPersonalData(t *testing.T) {
	var got []contracts.CustomerPersonalDataHolder
	beta := &fakePersonalData{name: "beta"}

	_, err := compose(
		Deps{Access: &fakeAccess{}, Config: &config.Config{Modules: []string{"alpha"}}},
		fakeLoad(map[string]string{"alpha": alphaContract, "beta": betaContract}),
		Module{Name: "alpha", Mount: func(d Deps) (http.Handler, error) {
			got = d.CustomerPersonalData
			return staticHandler("alpha")(d)
		}},
		Module{
			Name:                 "beta",
			CustomerPersonalData: func(Deps) contracts.CustomerPersonalData { return beta },
			Mount:                staticHandler("beta"),
		},
	)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if want := []contracts.CustomerPersonalDataHolder{{Module: "beta", Data: beta}}; !slices.Equal(got, want) {
		t.Errorf("Deps.CustomerPersonalData = %v, want beta's, disabled or not", got)
	}
}

// With nothing declared anywhere the slice stays nil.
func TestCompose_NoCustomerPersonalDataLeavesTheSliceNil(t *testing.T) {
	got := []contracts.CustomerPersonalDataHolder{{Module: "stale"}} // non-nil, so a no-op is caught

	_, err := compose(Deps{Access: &fakeAccess{}},
		fakeLoad(map[string]string{"alpha": alphaContract}),
		Module{Name: "alpha", Mount: func(d Deps) (http.Handler, error) {
			got = d.CustomerPersonalData
			return staticHandler("alpha")(d)
		}},
	)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if got != nil {
		t.Errorf("Deps.CustomerPersonalData = %v, want nil: no module declares any", got)
	}
}

// What a caller preset — modtest.WithCustomerPersonalData's seam — survives
// and comes first, and Compose appends to a copy.
func TestCompose_PresetCustomerPersonalDataComesFirstAndIsNotWrittenThrough(t *testing.T) {
	preset := contracts.CustomerPersonalDataHolder{Module: "preset", Data: &fakePersonalData{name: "preset"}}
	own := &fakePersonalData{name: "alpha"}
	presetList := make([]contracts.CustomerPersonalDataHolder, 1, 4)
	presetList[0] = preset
	var got []contracts.CustomerPersonalDataHolder

	_, err := compose(Deps{Access: &fakeAccess{}, CustomerPersonalData: presetList},
		fakeLoad(map[string]string{"alpha": alphaContract}),
		Module{
			Name:                 "alpha",
			CustomerPersonalData: func(Deps) contracts.CustomerPersonalData { return own },
			Mount: func(d Deps) (http.Handler, error) {
				got = d.CustomerPersonalData
				return staticHandler("alpha")(d)
			},
		},
	)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if want := []contracts.CustomerPersonalDataHolder{preset, {Module: "alpha", Data: own}}; !slices.Equal(got, want) {
		t.Errorf("Deps.CustomerPersonalData = %v, want the preset then alpha's", got)
	}
	if spare := presetList[:2]; spare[1] != (contracts.CustomerPersonalDataHolder{}) {
		t.Errorf("Compose wrote %v into the caller's own backing array", spare[1])
	}
}
```

In `apps/server/internal/module/workers_test.go`, add `"slices"` and `"github.com/vantigo-io/vantigo/server/internal/contracts"` to the imports, and append:

```go
// Worker mode never composes, and the anonymisation worker is the one caller
// of contracts.CustomerPersonalData's erase (customers GDPR design D2), so
// Workers collects the slot itself — from every module given, a disabled one
// included — before any module builds its workers.
func TestWorkers_HandTheWorkersEveryModulesCustomerPersonalData(t *testing.T) {
	beta := &fakePersonalData{name: "beta"}
	var seen []contracts.CustomerPersonalDataHolder
	Workers(Deps{Config: &config.Config{Modules: []string{"alpha"}}},
		Module{Name: "alpha", Workers: func(d Deps) []worker.Worker {
			seen = d.CustomerPersonalData
			return nil
		}},
		Module{Name: "beta", CustomerPersonalData: func(Deps) contracts.CustomerPersonalData { return beta }},
	)
	if want := []contracts.CustomerPersonalDataHolder{{Module: "beta", Data: beta}}; !slices.Equal(seen, want) {
		t.Errorf("alpha's workers saw Deps.CustomerPersonalData = %v, want beta's (disabled)", seen)
	}
}
```

- [ ] **Step 2: Run them and watch them fail**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- go test -count=1 -run 'CustomerPersonalData' ./internal/module/
```
Expected: FAIL to compile — `undefined: contracts.CustomerPersonalData`, `unknown field CustomerPersonalData in struct literal of type Module`.

- [ ] **Step 3: The contract**

Create `apps/server/internal/contracts/personal_data.go`:

```go
package contracts

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// CustomerPersonalData is what a module holds about one customer as a person
// (customers GDPR design D2): a read for the export, a write for the
// anonymisation. It is the second sanctioned cross-module direction, beside
// CustomerReferenceHolder, and deliberately not a method on it: a merge moves
// references and keeps everything, an anonymisation keeps the references and
// takes the person out of them, and a module may hold customer ids without
// holding anything about a person (docs/module-boundaries.md rule 9).
//
// ExportCustomerData answers the module's section of a private person's
// export: a JSON-serialisable value, nil when the module holds nothing for the
// id. It runs outside any transaction of the caller's — nobody holds a lock
// while it reads — on the module's own pool, in a read-only snapshot of its
// own if it needs several statements to agree.
//
// EraseCustomerData runs INSIDE the customers module's anonymisation
// transaction, which holds the customer row locked, and removes or blanks what
// the module holds about the person, on its own schema, in its own code. It
// keeps the merge holder's rules, none of which the signature shows:
//
//   - It never begins, commits or rolls back a transaction, and never touches
//     a pool: tx is the caller's, and so is the decision. An error rolls the
//     customer's whole anonymisation back, every module's part with it, and
//     the worker tries the customer again next cycle.
//   - It never reads a directory or any other contract: no in-process lookup
//     happens under a lock in this codebase (the customers module's actor.go).
//   - It reports what it removed or blanked, kind by kind — a kind it looked
//     at and had to leave alone included, at zero — so the customer.anonymised
//     event says every module was asked. A kind is "<module>.<what>" in the
//     API's camelCase, as a RepointedReferences kind is.
//   - It runs whether or not its module is enabled, for the holder's reason:
//     every schema is migrated whatever MODULES says, so a module switched off
//     still holds what it held. Its constructor must therefore need nothing a
//     disabled module's Deps lacks.
//   - Run again for an id it already erased, it finds nothing and reports
//     zeros.
type CustomerPersonalData interface {
	ExportCustomerData(ctx context.Context, customerID int32) (any, error)
	EraseCustomerData(ctx context.Context, tx pgx.Tx, customerID int32) ([]ErasedData, error)
}

// ErasedData is one kind of thing a module removed or blanked for a person,
// and how many rows of it.
type ErasedData struct {
	Kind  string
	Count int64
}

// CustomerPersonalDataHolder is one module's CustomerPersonalData under the
// module's name. The name is the export's key for the module's section
// (modules.communications, modules.energy, ...): the interface carries none,
// and Compose, which knows it, pairs the two.
type CustomerPersonalDataHolder struct {
	Module string
	Data   CustomerPersonalData
}
```

In `apps/server/internal/contracts/references.go`, replace the opening sentences of `CustomerReferenceHolder`'s doc comment

```go
// CustomerReferenceHolder is a module that stores customer ids in its own
// schema, and the one sanctioned cross-module WRITE (customers merge design
// D1). Every other contract here is a read. This one exists because merging
```

with

```go
// CustomerReferenceHolder is a module that stores customer ids in its own
// schema, and the first sanctioned cross-module WRITE (customers merge design
// D1); CustomerPersonalData (personal_data.go) is the second, with a rule of
// its own. Every other contract here is a read. This one exists because merging
```

- [ ] **Step 4: The slot and its two collections**

In `apps/server/internal/module/module.go`, add to `Deps` directly after the `CustomerReferenceHolders []contracts.CustomerReferenceHolder` field:

```go
	// CustomerPersonalData is every given module's
	// contracts.CustomerPersonalData (customers GDPR design D2), enabled or
	// not, each under its module's name, in the order the modules were given —
	// the second many-provider contract slot, collected the holders' way and
	// for their reason. Compose collects it before any Mount runs, for the
	// export; Workers (workers.go) collects it too, for the anonymisation
	// worker, because worker mode never composes. Both append to whatever the
	// caller preset here (the seam modtest.WithCustomerPersonalData fills). nil
	// when no module given holds anything about a person.
	CustomerPersonalData []contracts.CustomerPersonalDataHolder
```

and to `Module` directly after the `CustomerReferences func(Deps) contracts.CustomerReferenceHolder` field:

```go
	// CustomerPersonalData builds this module's contracts.CustomerPersonalData,
	// if it holds anything about a customer as a person (customers GDPR design
	// D2). Like CustomerReferences, any number of modules may set it, and it is
	// called for a module MODULES leaves out too, so it must need nothing of
	// Deps a disabled module would lack. Compose and Workers both call it,
	// before anything they build runs, and put the list on Deps as
	// CustomerPersonalData, each under this module's Name. A module holding
	// nothing about a person leaves it nil — time and expenses reach a customer
	// only through a project, and products not at all.
	CustomerPersonalData func(Deps) contracts.CustomerPersonalData
```

In `apps/server/internal/module/compose.go`, directly after the `if len(holders) > 0 { deps.CustomerReferenceHolders = … }` block and before `outer := http.NewServeMux()`, add:

```go
	// Customer personal data is the second many-provider slot (customers GDPR
	// design D2), collected from every module given, for the holders' reason
	// above, by the helper Workers uses too: the export reads it through a
	// Mount, the anonymisation worker through Workers.
	deps = withCustomerPersonalData(deps, given)
```

and append at the end of the file:

```go
// withCustomerPersonalData is deps with every module's
// contracts.CustomerPersonalData appended to Deps.CustomerPersonalData, each
// under its module's name, in mods order (customers GDPR design D2) — onto a
// copy of whatever the caller preset, so a harness's own slice is never
// written through. mods is every module given, enabled or not.
func withCustomerPersonalData(deps Deps, mods []Module) Deps {
	var holders []contracts.CustomerPersonalDataHolder
	for _, mod := range mods {
		if mod.CustomerPersonalData == nil {
			continue
		}
		if data := mod.CustomerPersonalData(deps); data != nil {
			holders = append(holders, contracts.CustomerPersonalDataHolder{Module: mod.Name, Data: data})
		}
	}
	if len(holders) > 0 {
		deps.CustomerPersonalData = append(slices.Clone(deps.CustomerPersonalData), holders...)
	}
	return deps
}
```

In `apps/server/internal/module/workers.go`, add the collection as `Workers`' first statement:

```go
func Workers(deps Deps, mods ...Module) []worker.Worker {
	// Every module given, enabled or not, before enablement drops any: the
	// anonymisation worker is where contracts.CustomerPersonalData's erase
	// runs (customers GDPR design D2), and worker mode never composes, so
	// nothing else would put the slot on its Deps.
	deps = withCustomerPersonalData(deps, mods)
	var out []worker.Worker
```

and in its doc comment replace `It resolves each module's contribution from deps as the caller passes it —` with `It resolves each module's contribution from deps as the caller passes it, plus Deps.CustomerPersonalData, which it collects the way Compose does —`.

- [ ] **Step 5: The harness seam**

In `apps/server/internal/modtest/modtest.go`: add `personalData []contracts.CustomerPersonalDataHolder` to `setup` directly after `holders      []contracts.CustomerReferenceHolder` (gofmt realigns the block); add the option directly after `WithCustomerReferenceHolders`:

```go
// WithCustomerPersonalData adds holders to Deps.CustomerPersonalData, for the
// module that exports and anonymises a private person (customers GDPR design
// D2) to be tested against fakes that record what they were asked, answer a
// section, or fail — to prove an anonymisation rolls back — without composing
// communications, energy or projects beside it: depguard keeps those out of
// customers' tests. module.Compose and module.Workers append the given
// modules' own after these, so a value set here survives both; given more than
// once, the holders accumulate in order.
func WithCustomerPersonalData(holders ...contracts.CustomerPersonalDataHolder) Option {
	return func(s *setup) { s.personalData = append(s.personalData, holders...) }
}
```

and in `New`'s `h.deps = module.Deps{…}` literal, directly after `CustomerReferenceHolders: s.holders,`:

```go
		CustomerPersonalData:     s.personalData,
```

- [ ] **Step 6: The boundary rule**

In `docs/module-boundaries.md`:

Rule 3's second paragraph becomes:

```markdown
   The one non-DTO type is `pgx.Tx`, in `contracts.CustomerReferenceHolder` and
   `contracts.CustomerPersonalData` — a platform type, never a store type, and the
   reason is rules 8 and 9's.
```

In rule 5, replace `Two slots are *many-provider* instead — any number of modules may fill them,` through the end of the item with:

```markdown
   Three slots are *many-provider* instead — any number of modules may fill them,
   and Compose collects them in module order: `Workers` (background work, from
   the enabled modules), `CustomerReferences`
   (`contracts.CustomerReferenceHolder`, rule 8) and `CustomerPersonalData`
   (`contracts.CustomerPersonalData`, rule 9), both from every module Compose is
   given, enabled or not — see [Turning a module off](#turning-a-module-off).
   `module.Workers` collects `CustomerPersonalData` as well, because worker mode
   never composes and the anonymisation worker is where rule 9's erase runs.
```

Rule 8's last sentence becomes `Another write direction needs a design of its own, not a second holder-shaped interface — rule 9 is that design for the second.` Then add, after rule 8:

```markdown
9. **A person's data, handed over and taken out.** The second sanctioned
   cross-module direction, made for the GDPR of private-person customers:
   `contracts.CustomerPersonalData`. A module that holds anything about a
   customer *as a person* declares `Module.CustomerPersonalData`. Its
   `ExportCustomerData` answers the module's section of that person's export,
   outside any transaction of the customers module's; its `EraseCustomerData`
   runs **inside** the customers module's anonymisation transaction, which holds
   the customer row locked, and removes or blanks what the module holds on its
   own schema, in its own package — the holder's rules exactly: it never begins
   or ends a transaction, never reads a directory, reports what it did kind by
   kind, and runs whether or not its module is enabled. It is not a method on the
   merge holder because it is not the same promise: a merge moves references and
   keeps everything, an anonymisation keeps the references and takes the person
   out of them. Today's implementations are communications (the correspondence,
   handed over and deleted), energy and projects (handed over, and kept:
   a supply period is the metering point's history, invoiced work stays)
   ([Personal data and anonymisation](customers.md#personal-data-and-anonymisation)).
```

Under "How they are enforced", after the rule-8 bullet, add:

```markdown
- **Rule 9**: the same way. `EraseCustomerData` is handed the caller's `pgx.Tx`
  and nothing else it could write with; its SQL is in its own `queries/`, so rule
  4's scan covers it; each module's package test erases real rows through a real
  transaction it rolls back first, and the customers module's anonymisation tests
  prove a module's error rolls that customer's whole anonymisation back.
```

In `## Turning a module off`, after the sentence ending `…both there whatever \`MODULES\` says.`, add: `Its \`CustomerPersonalData\` (rule 9) is collected the same way and for the same reason: a switched-off module still holds what it held about a person, and an anonymisation must still take it out.`

- [ ] **Step 7: Run them, show they can fail, commit**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- gofmt -w internal/contracts/personal_data.go internal/module internal/modtest/modtest.go && mise exec -- gofmt -l internal
mise exec -- go vet ./internal/module/... ./internal/contracts/... ./internal/modtest/... && mise exec -- go build ./...
mise exec -- go test -count=1 ./internal/module/...
```
Expected: PASS, every existing Compose and Workers test included (nothing declares the slot yet).

Prove each new test can fail, restoring after each: replace `append(slices.Clone(deps.CustomerPersonalData), holders...)` with `holders` — the preset test goes red; with `append(deps.CustomerPersonalData, holders...)` — the write-through check goes red; pass `mods` instead of `given` in composeFrom's call — the disabled test goes red; delete the `deps = withCustomerPersonalData(deps, mods)` line from `Workers` — the Workers test goes red; set `Module: "x"` instead of `mod.Name` — the name test goes red. Say what each printed.

```bash
cd /home/anders/projects/vantigo/vantigo
cat > /tmp/claude-1000/msg-gdpr-2.txt <<'EOF'
feat(contracts): modules that hold something about a person can hand it over and erase it

contracts.CustomerPersonalData is the second sanctioned cross-module
direction: ExportCustomerData answers a module's section of a private
person's export outside any transaction, and EraseCustomerData removes
what the module holds inside the customers module's anonymisation
transaction, on its own schema, under the merge holder's rules.
module.Module.CustomerPersonalData is a many-provider slot collected from
every module given, enabled or not, each under its module's name — by
Compose for the export and by Workers for the worker, since worker mode
never composes. modtest.WithCustomerPersonalData is the seam, and
docs/module-boundaries.md gains rule 9.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
PATHS="apps/server/internal/contracts/personal_data.go apps/server/internal/contracts/references.go \
 apps/server/internal/module/module.go apps/server/internal/module/compose.go apps/server/internal/module/workers.go \
 apps/server/internal/module/compose_test.go apps/server/internal/module/workers_test.go \
 apps/server/internal/modtest/modtest.go docs/module-boundaries.md"
git add $PATHS && git commit -F /tmp/claude-1000/msg-gdpr-2.txt -- $PATHS
git show --stat HEAD && git status --short
```

---

### Task 3: What communications, energy and projects hold about a person (D2)

Three implementations, each one sqlc file of its own schema and one Go file, landing before the customers module calls them. Communications' erase is the retention worker's own delete, extracted into a function both call, so a person's correspondence goes through the same ordered deletes and the same cleanup ledger as a retention batch.

**Files:**
- Create: `apps/server/internal/communications/customer_personal_data.go`, `communications/customer_personal_data_test.go`, `communications/queries/customer_personal_data.sql`; the same three in `apps/server/internal/energy/` and `apps/server/internal/projects/` (+ each generated `store/customer_personal_data.sql.go`)
- Modify: `apps/server/internal/communications/retention.go`, `communications/module.go`, `energy/module.go`, `projects/module.go`, `docs/communications.md`, `docs/projects.md`
- Read first (do not change): `communications/retention.go:190-330` (`CleanupBatch`), `communications/objects.go:100-170` (`queueObjectForDeletion`), `communications/retention_test.go:36-140` (`seedRetentionMessage`), each package's `customer_references.go` and `customer_references_test.go` (the shape these copy), `energy/stats_test.go:29` (`insertActiveSupplyPeriod`), `energy/meteringpoints_test.go:15-43`

**Interfaces:**
- Consumes: `contracts.CustomerPersonalData`, `contracts.ErasedData` (Task 2).
- Produces Go: each module's `Module().CustomerPersonalData` set; `communications.deleteMessages(ctx, q, messageIDs, now) (int64, int, error)` (package-private, shared with retention).
- Kinds: `communications.conversations`, `communications.messages`, `communications.attachments`, `communications.conversationSuggestions`, `communications.conversationCandidates`; `energy.supplyPeriods` (always 0); `projects.projects` (always 0).
- Sections (JSON): communications `{conversations: [{id, subject?, status, createdAt, lastActivityAt, messages: [{direction, subject?, occurredAt, textBody?, attachments: [fileName]}]}]}`; energy `{supplyPeriods: [{id, start, end?, status, meteringPoint: {gsrn, streetAddress, postalCode, city, countryCode}}]}`; projects `{projects: [{code, name, status, startDate?, endDate?}]}`. Each is nil — no key in the export — when the module holds nothing for the customer.

- [ ] **Step 1: The three query files**

Create `apps/server/internal/communications/queries/customer_personal_data.sql`:

```sql
-- name: CustomerConversationsForExport :many
-- CustomerConversationsForExport and the two reads after it are a private
-- person's correspondence for their export (customers GDPR design D2,
-- contracts.CustomerPersonalData): every conversation about the customer,
-- oldest first, then every message of them — internal notes included, they are
-- about the person too — and every attachment's name. customer_personal_data.go
-- reads the three in one read-only snapshot. ix_conversations_customer_id
-- finds the conversations.
SELECT id, subject, status, created_at, last_activity_at
FROM communications.conversations
WHERE customer_id = @customer_id::integer
ORDER BY created_at, id;

-- name: CustomerMessagesForExport :many
SELECT m.id, m.conversation_id, m.direction, m.subject, m.text_body, m.occurred_at
FROM communications.conversation_messages m
JOIN communications.conversations c ON c.id = m.conversation_id
WHERE c.customer_id = @customer_id::integer
ORDER BY m.occurred_at, m.id;

-- name: CustomerAttachmentNamesForExport :many
SELECT a.message_id, a.file_name
FROM communications.message_attachments a
JOIN communications.conversation_messages m ON m.id = a.message_id
JOIN communications.conversations c ON c.id = m.conversation_id
WHERE c.customer_id = @customer_id::integer
ORDER BY a.created_at, a.id;

-- name: CustomerConversationMessageIDs :many
-- CustomerConversationMessageIDs is where a person's anonymisation starts in
-- this module (customers GDPR design D2): every message of every conversation
-- about the customer, handed to deleteMessages — the retention batch's own
-- ordered deletes — inside the customers module's transaction.
SELECT m.id
FROM communications.conversation_messages m
JOIN communications.conversations c ON c.id = m.conversation_id
WHERE c.customer_id = @customer_id::integer
ORDER BY m.id;

-- name: CustomerConversationUploadKeys :many
-- The staged uploads of those conversations: their rows go with the
-- conversation (attachment_uploads cascades), so their objects are queued for
-- deletion first. An 'expired' upload's object was queued when it expired.
SELECT u.storage_key
FROM communications.attachment_uploads u
JOIN communications.conversations c ON c.id = u.conversation_id
WHERE c.customer_id = @customer_id::integer AND u.scan_status <> 'expired'
ORDER BY u.id;

-- name: DeleteCustomerConversations :execrows
-- The conversations themselves, once their messages are gone: participants'
-- links, tags, read states, idempotency records, AI interactions, staged
-- uploads and candidate rows all cascade.
DELETE FROM communications.conversations WHERE customer_id = @customer_id::integer;

-- name: ClearCustomerSuggestions :execrows
-- A suggestion naming the person on somebody else's conversation goes too, with
-- the reasoning that may name them.
UPDATE communications.conversations
SET suggested_customer_id = NULL, suggested_customer_confidence = NULL, suggested_customer_reasoning = NULL
WHERE suggested_customer_id = @customer_id::integer;

-- name: DeleteCustomerCandidates :execrows
-- And a candidate row naming the person on a conversation that is not theirs.
DELETE FROM communications.conversation_customer_candidates WHERE customer_id = @customer_id::integer;
```

Create `apps/server/internal/energy/queries/customer_personal_data.sql`:

```sql
-- name: CustomerSupplyPeriodsForExport :many
-- CustomerSupplyPeriodsForExport is a private person's supply periods for
-- their export (customers GDPR design D2, contracts.CustomerPersonalData),
-- each with the metering point's address — for a residential customer that is
-- very likely their home, which is exactly why it belongs in the file. No index
-- is keyed by customer_id (RepointSupplyPeriodsCustomer's reasoning holds: an
-- export is rare and deliberate).
SELECT sp.id, sp.start, sp."end", sp.status,
       mp.gsrn, mp.street_address, mp.postal_code, mp.city, mp.country_code
FROM energy.supply_periods sp
JOIN energy.metering_points mp ON mp.id = sp.metering_point_id
WHERE sp.customer_id = @customer_id::integer
ORDER BY sp.start, sp.id;
```

Create `apps/server/internal/projects/queries/customer_personal_data.sql`:

```sql
-- name: CustomerProjectsForExport :many
-- CustomerProjectsForExport is a private person's projects for their export
-- (customers GDPR design D2, contracts.CustomerPersonalData): what was done
-- for them, by code, name, status and dates. ix_projects_customer_id finds the
-- rows.
SELECT code, name, status, start_date, end_date
FROM projects.projects
WHERE customer_id = @customer_id::integer
ORDER BY code;
```

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server && mise exec -- go generate ./... && git status --short
```
Expected: three new `store/customer_personal_data.sql.go` files, nothing else changed. Read the generated names (`store.CustomerConversationsForExportRow` with `ID uuid.UUID`, `Subject *string`, `Status string`, `CreatedAt`, `LastActivityAt time.Time`; `store.CustomerSupplyPeriodsForExportRow` with `End *time.Time`, `StreetAddress`, `PostalCode`, `City`, `CountryCode`, `Gsrn`; `store.CustomerProjectsForExportRow` with `StartDate`, `EndDate pgtype.Date`) — if sqlc chose others, use its names in the code below.

- [ ] **Step 2: Write the failing package tests**

Create `apps/server/internal/communications/customer_personal_data_test.go`:

```go
package communications_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/communications"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// personalDataOf is the module's own contracts.CustomerPersonalData, built
// from the harness's dependencies exactly as Compose builds it.
func personalDataOf(t *testing.T, h *modtest.Harness) contracts.CustomerPersonalData {
	t.Helper()
	build := communications.Module().CustomerPersonalData
	if build == nil {
		t.Fatal("the module declares no customer personal data")
	}
	return build(h.Deps())
}

// eraseCommunicationsCustomer runs the module's erase inside a transaction the
// test owns — the customers anonymisation's position — and commits it or rolls
// it back as told.
func eraseCommunicationsCustomer(t *testing.T, h *modtest.Harness, customerID int32, commit bool) []contracts.ErasedData {
	t.Helper()
	ctx := context.Background()
	tx, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	erased, err := personalDataOf(t, h).EraseCustomerData(ctx, tx, customerID)
	if err != nil {
		t.Fatalf("EraseCustomerData(%d): %v", customerID, err)
	}
	if commit {
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit: %v", err)
		}
	}
	return erased
}

// personsConversation is seedRetentionMessage's whole shape — a delivery with
// an event on it (the RESTRICT that makes the delete order load-bearing), an
// attachment, an idempotency record, an AI interaction — made a person's: the
// conversation is about customerID, and its message says something.
func personsConversation(t *testing.T, h *modtest.Harness, customerID int32, attachmentKey string) retentionFixture {
	t.Helper()
	fx := seedRetentionMessage(t, h, retentionSeed{attachmentKey: attachmentKey, idempotency: true, ai: true})
	h.Exec(t, `UPDATE communications.conversations SET customer_id = $2 WHERE id = $1`, fx.conversationID, customerID)
	h.Exec(t, `UPDATE communications.conversation_messages SET text_body = 'Hei, strømmen er borte igjen.' WHERE id = $1`, fx.messageID)
	return fx
}

// TestCustomerPersonalData_ExportsThePersonsCorrespondence is communications'
// section of a private person's export (customers GDPR design D2): each
// conversation about them with its subject and dates, each message's
// direction, date and text, each attachment's name — and nothing of anybody
// else's. A customer with no conversation has no section at all.
func TestCustomerPersonalData_ExportsThePersonsCorrespondence(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	fx := personsConversation(t, h, 1001, "objects/"+uuid.NewString()+".txt")
	personsConversation(t, h, 1002, "")

	section, err := personalDataOf(t, h).ExportCustomerData(context.Background(), 1001)
	if err != nil {
		t.Fatalf("ExportCustomerData: %v", err)
	}
	body, err := json.Marshal(section)
	if err != nil {
		t.Fatalf("marshal the section: %v", err)
	}
	var got struct {
		Conversations []struct {
			ID       uuid.UUID `json:"id"`
			Subject  string    `json:"subject"`
			Status   string    `json:"status"`
			Messages []struct {
				Direction   string   `json:"direction"`
				TextBody    string   `json:"textBody"`
				Attachments []string `json:"attachments"`
			} `json:"messages"`
		} `json:"conversations"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	if len(got.Conversations) != 1 || got.Conversations[0].ID != fx.conversationID || got.Conversations[0].Subject != "Retention fixture" {
		t.Fatalf("conversations = %s, want customer 1001's one", body)
	}
	messages := got.Conversations[0].Messages
	if len(messages) != 1 || messages[0].Direction != "outbound" || messages[0].TextBody != "Hei, strømmen er borte igjen." ||
		!slices.Equal(messages[0].Attachments, []string{"a.txt"}) {
		t.Errorf("messages = %+v, want the one message with its text and attachment name", messages)
	}

	none, err := personalDataOf(t, h).ExportCustomerData(context.Background(), 1003)
	if err != nil || none != nil {
		t.Errorf("a customer with no conversation = %v, %v; want nil, nil", none, err)
	}
}

// TestCustomerPersonalData_ErasesThroughTheCleanupLedgerInsideTheCallersTransaction
// is the anonymisation's half (customers GDPR design D2): the person's
// conversations and every row under them go in retention's own order — the
// event before the delivery it names — the attachment's object is queued on the
// cleanup ledger, never deleted here, and a suggestion or candidate row naming
// the person on somebody else's conversation goes too. Rolled back, nothing
// went; run again, it finds nothing.
func TestCustomerPersonalData_ErasesThroughTheCleanupLedgerInsideTheCallersTransaction(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	key := "objects/" + uuid.NewString() + ".txt"
	fx := personsConversation(t, h, 1001, key)
	other := personsConversation(t, h, 1002, "")
	h.Exec(t, `UPDATE communications.conversations
	           SET suggested_customer_id = 1001, suggested_customer_confidence = 0.9, suggested_customer_reasoning = 'Kari skrev fra samme adresse'
	           WHERE id = $1`, other.conversationID)
	insertCandidate(t, h, other.conversationID.String(), 1001)

	if erased := eraseCommunicationsCustomer(t, h, 1001, false); len(erased) != 5 {
		t.Fatalf("EraseCustomerData = %+v, want the five kinds", erased)
	}
	if n := h.Count(t, `SELECT count(*) FROM communications.conversations WHERE id = $1`, fx.conversationID); n != 1 {
		t.Fatal("a rolled-back erase deleted the conversation: it wrote outside the caller's transaction")
	}

	erased := eraseCommunicationsCustomer(t, h, 1001, true)
	want := []contracts.ErasedData{
		{Kind: "communications.conversations", Count: 1},
		{Kind: "communications.messages", Count: 1},
		{Kind: "communications.attachments", Count: 1},
		{Kind: "communications.conversationSuggestions", Count: 1},
		{Kind: "communications.conversationCandidates", Count: 1},
	}
	if !slices.Equal(erased, want) {
		t.Errorf("EraseCustomerData = %+v, want %+v", erased, want)
	}
	for table, id := range map[string]uuid.UUID{
		"conversations":         fx.conversationID,
		"conversation_messages": fx.messageID,
		"message_deliveries":    fx.deliveryID,
		"message_events":        fx.eventID,
		"message_attachments":   fx.attachmentID,
	} {
		if n := h.Count(t, `SELECT count(*) FROM communications.`+pgx.Identifier{table}.Sanitize()+` WHERE id = $1`, id); n != 0 {
			t.Errorf("communications.%s still holds %s", table, id)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM communications.attachment_cleanup_records WHERE storage_key = $1 AND status = 'pending'`, key); n != 1 {
		t.Errorf("pending cleanup records for the attachment = %d, want 1: its object goes through the ledger", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM communications.conversations
	                     WHERE id = $1 AND customer_id = 1002 AND suggested_customer_id IS NULL
	                       AND suggested_customer_confidence IS NULL AND suggested_customer_reasoning IS NULL`, other.conversationID); n != 1 {
		t.Error("the other conversation lost its customer, or kept the suggestion naming the person")
	}
	if n := h.Count(t, `SELECT count(*) FROM communications.conversation_customer_candidates WHERE customer_id = 1001`); n != 0 {
		t.Errorf("%d candidate rows still name the person", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM communications.conversation_messages WHERE id = $1`, other.messageID); n != 1 {
		t.Error("the other customer's message was deleted")
	}

	for _, again := range eraseCommunicationsCustomer(t, h, 1001, true) {
		if again.Count != 0 {
			t.Errorf("a second erase %s = %d, want 0", again.Kind, again.Count)
		}
	}
}
```

Create `apps/server/internal/energy/customer_personal_data_test.go`:

```go
package energy_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/energy"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

func energyPersonalData(t *testing.T, h *modtest.Harness) contracts.CustomerPersonalData {
	t.Helper()
	build := energy.Module().CustomerPersonalData
	if build == nil {
		t.Fatal("the module declares no customer personal data")
	}
	return build(h.Deps())
}

// TestCustomerPersonalData_ExportsSupplyPeriodsWithTheirAddress is energy's
// section of a private person's export (customers GDPR design D2): every
// supply period with its metering point's address, and none of another
// customer's; nothing at all for a customer never supplied.
func TestCustomerPersonalData_ExportsSupplyPeriodsWithTheirAddress(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	point := createMeteringPoint(t, h.SignIn(t, allEnergyPermissions...))
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	insertActiveSupplyPeriod(t, h, point.Id, 1001, start, start.AddDate(0, 1, 0))
	insertActiveSupplyPeriod(t, h, point.Id, 1002, start.AddDate(0, 2, 0), start.AddDate(0, 3, 0))
	address := modtest.One[string](t, h, `SELECT street_address FROM energy.metering_points WHERE id = $1`, point.Id)

	section, err := energyPersonalData(t, h).ExportCustomerData(context.Background(), 1001)
	if err != nil {
		t.Fatalf("ExportCustomerData: %v", err)
	}
	body, _ := json.Marshal(section)
	var got struct {
		SupplyPeriods []struct {
			Start         time.Time  `json:"start"`
			End           *time.Time `json:"end"`
			Status        string     `json:"status"`
			MeteringPoint struct {
				StreetAddress string `json:"streetAddress"`
			} `json:"meteringPoint"`
		} `json:"supplyPeriods"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	if len(got.SupplyPeriods) != 1 || !got.SupplyPeriods[0].Start.Equal(start) || got.SupplyPeriods[0].Status != "Active" ||
		got.SupplyPeriods[0].MeteringPoint.StreetAddress != address {
		t.Errorf("section = %s, want customer 1001's one period at %q", body, address)
	}
	if none, err := energyPersonalData(t, h).ExportCustomerData(context.Background(), 1003); err != nil || none != nil {
		t.Errorf("a customer never supplied = %v, %v; want nil, nil", none, err)
	}
}

// Erasing keeps everything (design D2): a supply period is the metering
// point's history, and its address is the point's, not the person's; the
// period keeps pointing at the anonymised customer. It still says it was asked.
func TestCustomerPersonalData_EraseKeepsTheSupplyPeriods(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	point := createMeteringPoint(t, h.SignIn(t, allEnergyPermissions...))
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	id := insertActiveSupplyPeriod(t, h, point.Id, 1001, start, start.AddDate(0, 1, 0))

	ctx := context.Background()
	tx, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	erased, err := energyPersonalData(t, h).EraseCustomerData(ctx, tx, 1001)
	if err != nil {
		t.Fatalf("EraseCustomerData: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if want := []contracts.ErasedData{{Kind: "energy.supplyPeriods", Count: 0}}; !slices.Equal(erased, want) {
		t.Errorf("EraseCustomerData = %+v, want %+v", erased, want)
	}
	if got := modtest.One[int32](t, h, `SELECT customer_id FROM energy.supply_periods WHERE id = $1`, id); got != 1001 {
		t.Errorf("the supply period's customer_id = %d, want 1001 still", got)
	}
}
```

Create `apps/server/internal/projects/customer_personal_data_test.go`:

```go
package projects_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/projects"
)

func projectsPersonalData(t *testing.T, h *modtest.Harness) contracts.CustomerPersonalData {
	t.Helper()
	build := projects.Module().CustomerPersonalData
	if build == nil {
		t.Fatal("the module declares no customer personal data")
	}
	return build(h.Deps())
}

// TestCustomerPersonalData_ExportsThePersonsProjects is projects' section of a
// private person's export (customers GDPR design D2): code, name, status and
// dates of every project billed to them, and none of anybody else's.
func TestCustomerPersonalData_ExportsThePersonsProjects(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	person, other := int32(customerAcme), int32(customerKraftVerket)
	id := insertProjectFor(t, h, "GDPR1000", &person)
	h.Exec(t, `UPDATE projects.projects SET name = 'Varmepumpe', start_date = '2026-03-01', end_date = '2026-04-15' WHERE id = $1`, id)
	insertProjectFor(t, h, "GDPR1001", &other)

	section, err := projectsPersonalData(t, h).ExportCustomerData(context.Background(), person)
	if err != nil {
		t.Fatalf("ExportCustomerData: %v", err)
	}
	body, _ := json.Marshal(section)
	if want := `{"projects":[{"code":"GDPR1000","name":"Varmepumpe","status":"planned","startDate":"2026-03-01","endDate":"2026-04-15"}]}`; string(body) != want {
		t.Errorf("section = %s, want %s", body, want)
	}
	if none, err := projectsPersonalData(t, h).ExportCustomerData(context.Background(), int32(customerArchived)); err != nil || none != nil {
		t.Errorf("a customer with no project = %v, %v; want nil, nil", none, err)
	}
}

// Erasing keeps everything (design D2): invoiced work stays, and no customer
// name is stored here to blank — the project names the anonymised customer
// through the directory from then on. It still says it was asked.
func TestCustomerPersonalData_EraseKeepsTheProjects(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	person := int32(customerAcme)
	id := insertProjectFor(t, h, "GDPR1002", &person)
	before := modtest.One[int32](t, h, `SELECT revision FROM projects.projects WHERE id = $1`, id)

	ctx := context.Background()
	tx, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	erased, err := projectsPersonalData(t, h).EraseCustomerData(ctx, tx, person)
	if err != nil {
		t.Fatalf("EraseCustomerData: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if want := []contracts.ErasedData{{Kind: "projects.projects", Count: 0}}; !slices.Equal(erased, want) {
		t.Errorf("EraseCustomerData = %+v, want %+v", erased, want)
	}
	if got := modtest.One[int32](t, h, `SELECT revision FROM projects.projects WHERE id = $1 AND customer_id = $2`, id, person); got != before {
		t.Errorf("the project's revision = %d (was %d): an erase that keeps everything wrote the row", got, before)
	}
}
```

Run them: `mise exec -- go test -count=1 -run 'CustomerPersonalData' ./internal/communications/ ./internal/energy/ ./internal/projects/` — FAIL: `Module().CustomerPersonalData` is nil ("the module declares no customer personal data") once the build compiles, or `undefined` before Task 2 landed.

- [ ] **Step 3: Communications — retention's delete, shared**

In `apps/server/internal/communications/retention.go`, add `"github.com/google/uuid"` to the third-party imports. Add, directly before `CleanupBatch`:

```go
// deleteMessages is steps 1 to 6 of a retention batch, for messageIDs, inside
// the caller's transaction, and answers how many messages it deleted and how
// many object keys it handed to the cleanup ledger. Two callers: CleanupBatch
// below, for its batch, and a person's anonymisation
// (customer_personal_data.go), for every message of their conversations — so a
// person's correspondence leaves through exactly the deletes and the ledger
// retention's does, in the order message_events' RESTRICT allows. Step 7, the
// orphan sweeps, is each caller's own.
func deleteMessages(ctx context.Context, q *store.Queries, messageIDs []uuid.UUID, now time.Time) (int64, int, error) {
	// Step 1, then step 2. NOT the other way round: see the header.
	if err := q.DeleteMessageEventsByMessageIDs(ctx, messageIDs); err != nil {
		return 0, 0, fmt.Errorf("communications: delete message events: %w", err)
	}
	if err := q.DeleteMessageDeliveriesByMessageIDs(ctx, messageIDs); err != nil {
		return 0, 0, fmt.Errorf("communications: delete message deliveries: %w", err)
	}

	// Step 3: read every object key the batch is about to orphan, while
	// the rows that identify their owners still exist.
	attachments, err := q.ListRetentionAttachments(ctx, messageIDs)
	if err != nil {
		return 0, 0, fmt.Errorf("communications: list retention attachments: %w", err)
	}
	rawPayloads, err := q.ListRetentionRawPayloadKeys(ctx, messageIDs)
	if err != nil {
		return 0, 0, fmt.Errorf("communications: list retention raw payload keys: %w", err)
	}

	// Step 4: queue them. Step 5 in .NET is an explicit SaveChanges
	// ("Persist every reservation before deleting the rows that identify
	// its owner", `:101-102`) which forces EF's buffered inserts out
	// ahead of the ExecuteDeletes below. Here every statement is sent
	// when it is written, so that ordering is inherent rather than
	// something to arrange — and the enclosing transaction gives the same
	// all-or-nothing guarantee either way.
	queued := 0
	for _, attachment := range attachments {
		messageID := attachment.MessageID
		if err := queueObjectForDeletion(ctx, q, attachment.StorageKey, &messageID, now); err != nil {
			return 0, 0, err
		}
		queued++
	}
	// .NET dedupes the raw keys through an Ordinal HashSet (`:94`); two
	// messages sharing a raw-payload key would otherwise be queued twice,
	// and the second call would merely re-stamp the first's record.
	seen := make(map[string]bool, len(rawPayloads))
	for _, raw := range rawPayloads {
		if raw.RawPayloadStorageKey == nil || seen[*raw.RawPayloadStorageKey] {
			continue
		}
		seen[*raw.RawPayloadStorageKey] = true
		messageID := raw.ID
		if err := queueObjectForDeletion(ctx, q, *raw.RawPayloadStorageKey, &messageID, now); err != nil {
			return 0, 0, err
		}
		queued++
	}

	// Step 6.
	if err := q.DeleteMessageAttachmentsByMessageIDs(ctx, messageIDs); err != nil {
		return 0, 0, fmt.Errorf("communications: delete message attachments: %w", err)
	}
	if err := q.DeleteIdempotencyRecordsByMessageIDs(ctx, messageIDs); err != nil {
		return 0, 0, fmt.Errorf("communications: delete idempotency records: %w", err)
	}
	if err := q.DeleteOutboxJobsByMessageIDs(ctx, messageIDs); err != nil {
		return 0, 0, fmt.Errorf("communications: delete outbox jobs: %w", err)
	}
	deleted, err := q.DeleteConversationMessagesByIDs(ctx, messageIDs)
	if err != nil {
		return 0, 0, fmt.Errorf("communications: delete conversation messages: %w", err)
	}
	return deleted, queued, nil
}
```

In `CleanupBatch`, replace everything from the comment `// Step 1, then step 2. NOT the other way round: see the header.` down to and including the `deleted, err = q.DeleteConversationMessagesByIDs(ctx, messageIDs)` statement and its `if err != nil { … }` with:

```go
		// Steps 1 to 6, shared with a person's anonymisation.
		deleted, _, err = deleteMessages(ctx, q, messageIDs, now)
		if err != nil {
			return err
		}
```

leaving the step-7 sweeps and everything after them as they are.

- [ ] **Step 4: Communications — the implementation**

Create `apps/server/internal/communications/customer_personal_data.go`:

```go
package communications

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/communications/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/db"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// The five kinds a person's anonymisation reports from this module, in the
// order it erases them.
const (
	personalDataKindConversations = "communications.conversations"
	personalDataKindMessages      = "communications.messages"
	personalDataKindObjects       = "communications.attachments"
	personalDataKindSuggestions   = "communications.conversationSuggestions"
	personalDataKindCandidates    = "communications.conversationCandidates"
)

// customerPersonalData is this module's contracts.CustomerPersonalData
// (customers GDPR design D2). A person's correspondence is the most personal
// thing this installation holds about them, so it is handed over whole in the
// export and removed whole by the anonymisation: every conversation about
// them, with every message and attachment under it. A conversation that only
// suggests or lists them keeps its own customer and loses the reference.
type customerPersonalData struct {
	pool  *pgxpool.Pool
	clock func() time.Time
}

var _ contracts.CustomerPersonalData = (*customerPersonalData)(nil)

// newCustomerPersonalData is Module's CustomerPersonalData. The pool is the
// export's and the clock stamps the cleanup records the erase writes — both
// there whether or not the module is enabled.
func newCustomerPersonalData(d module.Deps) contracts.CustomerPersonalData {
	return &customerPersonalData{pool: d.Pool, clock: d.Clock}
}

// personalDataSection is this module's section of a person's export.
type personalDataSection struct {
	Conversations []personalDataConversation `json:"conversations"`
}

type personalDataConversation struct {
	ID             uuid.UUID             `json:"id"`
	Subject        *string               `json:"subject,omitempty"`
	Status         string                `json:"status"`
	CreatedAt      time.Time             `json:"createdAt"`
	LastActivityAt time.Time             `json:"lastActivityAt"`
	Messages       []personalDataMessage `json:"messages"`
}

type personalDataMessage struct {
	Direction   string    `json:"direction"`
	Subject     *string   `json:"subject,omitempty"`
	OccurredAt  time.Time `json:"occurredAt"`
	TextBody    *string   `json:"textBody,omitempty"`
	Attachments []string  `json:"attachments"`
}

// ExportCustomerData reads the three statements in one read-only snapshot, so
// a message written between them can never show without its conversation, and
// answers nil for a customer with no conversation. The HTML bodies are left
// out: the text body is the message, and the HTML is the same words dressed.
func (p *customerPersonalData) ExportCustomerData(ctx context.Context, customerID int32) (any, error) {
	var (
		conversations []store.CustomerConversationsForExportRow
		messages      []store.CustomerMessagesForExportRow
		attachments   []store.CustomerAttachmentNamesForExportRow
	)
	err := db.WithTx(ctx, p.pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		q := store.New(tx)
		var err error
		if conversations, err = q.CustomerConversationsForExport(ctx, customerID); err != nil || len(conversations) == 0 {
			return err
		}
		if messages, err = q.CustomerMessagesForExport(ctx, customerID); err != nil {
			return err
		}
		attachments, err = q.CustomerAttachmentNamesForExport(ctx, customerID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("communications: read customer %d's correspondence: %w", customerID, err)
	}
	if len(conversations) == 0 {
		return nil, nil
	}

	names := make(map[uuid.UUID][]string, len(attachments))
	for _, a := range attachments {
		names[a.MessageID] = append(names[a.MessageID], a.FileName)
	}
	byConversation := make(map[uuid.UUID][]personalDataMessage, len(conversations))
	for _, m := range messages {
		files := names[m.ID]
		if files == nil {
			files = []string{}
		}
		byConversation[m.ConversationID] = append(byConversation[m.ConversationID], personalDataMessage{
			Direction: m.Direction, Subject: m.Subject, OccurredAt: m.OccurredAt, TextBody: m.TextBody, Attachments: files,
		})
	}
	section := personalDataSection{Conversations: make([]personalDataConversation, 0, len(conversations))}
	for _, c := range conversations {
		msgs := byConversation[c.ID]
		if msgs == nil {
			msgs = []personalDataMessage{}
		}
		section.Conversations = append(section.Conversations, personalDataConversation{
			ID: c.ID, Subject: c.Subject, Status: c.Status, CreatedAt: c.CreatedAt, LastActivityAt: c.LastActivityAt, Messages: msgs,
		})
	}
	return section, nil
}

// EraseCustomerData removes the person's correspondence inside the customers
// module's transaction: every message through retention's own deletes
// (deleteMessages — the delivery events before the deliveries, each object key
// on the cleanup ledger before the rows naming it go), the staged uploads'
// objects queued the same way, then the conversations, whose remaining rows
// cascade, then the participants nobody links to any more (retention's step 7).
// A message still waiting in the outbox is deleted with its job: a mail not yet
// sent to an anonymised person is not sent. The object store is never called
// here — the cleanup worker does that after this commits, and a rollback leaves
// no record behind.
func (p *customerPersonalData) EraseCustomerData(ctx context.Context, tx pgx.Tx, customerID int32) ([]contracts.ErasedData, error) {
	q := store.New(tx)
	now := p.clock()

	messageIDs, err := q.CustomerConversationMessageIDs(ctx, customerID)
	if err != nil {
		return nil, fmt.Errorf("communications: list customer %d's messages: %w", customerID, err)
	}
	var messages int64
	queued := 0
	if len(messageIDs) > 0 {
		if messages, queued, err = deleteMessages(ctx, q, messageIDs, now); err != nil {
			return nil, err
		}
	}
	uploads, err := q.CustomerConversationUploadKeys(ctx, customerID)
	if err != nil {
		return nil, fmt.Errorf("communications: list customer %d's staged uploads: %w", customerID, err)
	}
	for _, key := range uploads {
		if err := queueObjectForDeletion(ctx, q, key, nil, now); err != nil {
			return nil, err
		}
		queued++
	}
	conversations, err := q.DeleteCustomerConversations(ctx, customerID)
	if err != nil {
		return nil, fmt.Errorf("communications: delete customer %d's conversations: %w", customerID, err)
	}
	if conversations > 0 {
		if err := q.DeleteOrphanConversationParticipants(ctx); err != nil {
			return nil, fmt.Errorf("communications: delete orphan conversation participants: %w", err)
		}
		if err := q.DeleteOrphanParticipants(ctx); err != nil {
			return nil, fmt.Errorf("communications: delete orphan participants: %w", err)
		}
	}
	suggestions, err := q.ClearCustomerSuggestions(ctx, customerID)
	if err != nil {
		return nil, fmt.Errorf("communications: clear suggestions of customer %d: %w", customerID, err)
	}
	candidates, err := q.DeleteCustomerCandidates(ctx, customerID)
	if err != nil {
		return nil, fmt.Errorf("communications: delete candidate rows of customer %d: %w", customerID, err)
	}
	return []contracts.ErasedData{
		{Kind: personalDataKindConversations, Count: conversations},
		{Kind: personalDataKindMessages, Count: messages},
		{Kind: personalDataKindObjects, Count: int64(queued)},
		{Kind: personalDataKindSuggestions, Count: suggestions},
		{Kind: personalDataKindCandidates, Count: candidates},
	}, nil
}
```

In `apps/server/internal/communications/module.go`'s `Module()`, add `CustomerPersonalData: newCustomerPersonalData,` after `CustomerReferences: newCustomerReferenceHolder,` (gofmt realigns).

- [ ] **Step 5: Energy and projects**

Create `apps/server/internal/energy/customer_personal_data.go`:

```go
package energy

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/energy/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// customerPersonalData is this module's contracts.CustomerPersonalData
// (customers GDPR design D2): a private person's supply periods, handed over
// with the address they were supplied at, and kept when the person is
// anonymised.
type customerPersonalData struct {
	pool *pgxpool.Pool
}

var _ contracts.CustomerPersonalData = customerPersonalData{}

// newCustomerPersonalData is Module's CustomerPersonalData.
func newCustomerPersonalData(d module.Deps) contracts.CustomerPersonalData {
	return customerPersonalData{pool: d.Pool}
}

type supplyPeriodsSection struct {
	SupplyPeriods []exportedSupplyPeriod `json:"supplyPeriods"`
}

type exportedSupplyPeriod struct {
	ID            int32                 `json:"id"`
	Start         time.Time             `json:"start"`
	End           *time.Time            `json:"end,omitempty"`
	Status        string                `json:"status"`
	MeteringPoint exportedMeteringPoint `json:"meteringPoint"`
}

type exportedMeteringPoint struct {
	Gsrn          string `json:"gsrn"`
	StreetAddress string `json:"streetAddress"`
	PostalCode    string `json:"postalCode"`
	City          string `json:"city"`
	CountryCode   string `json:"countryCode"`
}

// ExportCustomerData answers nil for a customer never supplied.
func (p customerPersonalData) ExportCustomerData(ctx context.Context, customerID int32) (any, error) {
	rows, err := store.New(p.pool).CustomerSupplyPeriodsForExport(ctx, customerID)
	if err != nil {
		return nil, fmt.Errorf("energy: read customer %d's supply periods: %w", customerID, err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	section := supplyPeriodsSection{SupplyPeriods: make([]exportedSupplyPeriod, 0, len(rows))}
	for _, r := range rows {
		section.SupplyPeriods = append(section.SupplyPeriods, exportedSupplyPeriod{
			ID: r.ID, Start: r.Start, End: r.End, Status: r.Status,
			MeteringPoint: exportedMeteringPoint{
				Gsrn: r.Gsrn, StreetAddress: r.StreetAddress, PostalCode: r.PostalCode, City: r.City, CountryCode: r.CountryCode,
			},
		})
	}
	return section, nil
}

// EraseCustomerData keeps everything and says so (design D2): a supply period
// is the metering point's history, the address is the point's rather than the
// person's, and the row keeps pointing at the anonymised customer, whose name
// the directory answers as "Anonymised person" from then on. Nothing here
// names the person, so there is nothing to blank.
func (customerPersonalData) EraseCustomerData(context.Context, pgx.Tx, int32) ([]contracts.ErasedData, error) {
	return []contracts.ErasedData{{Kind: customerReferenceKindSupplyPeriods, Count: 0}}, nil
}
```

Create `apps/server/internal/projects/customer_personal_data.go`:

```go
package projects

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/projects/store"
)

// customerPersonalData is this module's contracts.CustomerPersonalData
// (customers GDPR design D2): what was done for a private person, handed over,
// and kept when they are anonymised — invoiced work stays, and no customer
// name is stored here to blank.
type customerPersonalData struct {
	pool *pgxpool.Pool
}

var _ contracts.CustomerPersonalData = customerPersonalData{}

// newCustomerPersonalData is Module's CustomerPersonalData.
func newCustomerPersonalData(d module.Deps) contracts.CustomerPersonalData {
	return customerPersonalData{pool: d.Pool}
}

type projectsSection struct {
	Projects []exportedProject `json:"projects"`
}

type exportedProject struct {
	Code      string  `json:"code"`
	Name      string  `json:"name"`
	Status    string  `json:"status"`
	StartDate *string `json:"startDate,omitempty"`
	EndDate   *string `json:"endDate,omitempty"`
}

// dateOnly is a date column as the API writes one, or nil.
func dateOnly(d pgtype.Date) *string {
	if !d.Valid {
		return nil
	}
	s := d.Time.Format(time.DateOnly)
	return &s
}

// ExportCustomerData answers nil for a customer no project bills to.
func (p customerPersonalData) ExportCustomerData(ctx context.Context, customerID int32) (any, error) {
	rows, err := store.New(p.pool).CustomerProjectsForExport(ctx, customerID)
	if err != nil {
		return nil, fmt.Errorf("projects: read customer %d's projects: %w", customerID, err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	section := projectsSection{Projects: make([]exportedProject, 0, len(rows))}
	for _, r := range rows {
		section.Projects = append(section.Projects, exportedProject{
			Code: r.Code, Name: r.Name, Status: r.Status, StartDate: dateOnly(r.StartDate), EndDate: dateOnly(r.EndDate),
		})
	}
	return section, nil
}

// EraseCustomerData keeps everything and says so (design D2). A project named
// after the person is free text this delivery does not rewrite (the design's
// out-of-scope list); the project names its customer through the directory,
// which answers the anonymised name.
func (customerPersonalData) EraseCustomerData(context.Context, pgx.Tx, int32) ([]contracts.ErasedData, error) {
	return []contracts.ErasedData{{Kind: customerReferenceKindProjects, Count: 0}}, nil
}
```

If the projects package already has a helper named `dateOnly`, name this one `exportDate`. Add `CustomerPersonalData: newCustomerPersonalData,` to `energy/module.go`'s and `projects/module.go`'s `Module()` after `CustomerReferences`.

- [ ] **Step 6: Run everything, show it can fail, the docs, commit**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- gofmt -l internal && mise exec -- go vet ./internal/communications/... ./internal/energy/... ./internal/projects/...
mise exec -- go test -count=1 ./internal/communications/... ./internal/energy/... ./internal/projects/... ./internal/db/...
```
Expected: PASS — the retention suite included (its deletes are the same statements in the same order, now through `deleteMessages`), and `TestNoModuleReferencesAnotherModulesSchema` (each new query names only its own schema).

Prove the tests can fail, restoring after each: in `deleteMessages`, swap the events and deliveries deletes — the communications erase test goes red with SQLSTATE 23001 (and so does `TestRetention_DeletesEventsBeforeDeliveries`); remove the `queueObjectForDeletion` loop over `attachments` — the ledger assertion goes red; drop `ClearCustomerSuggestions` — the suggestion assertion goes red; make projects' erase `return nil, nil` — its kind assertion goes red; make energy's export skip the address — its assertion goes red. Say what each printed.

In `docs/communications.md`, after the paragraph that begins `The one write in the other direction is a customer merge:`, add:

```markdown
It also hands over and takes out what it holds about a private person —
`contracts.CustomerPersonalData` ([module boundaries rule 9](module-boundaries.md#the-rules)).
A person's export carries every conversation about them, with each message's
direction, date and text body and each attachment's name (`modules.communications`).
Their anonymisation deletes those conversations inside the customers module's
transaction, through the retention worker's own deletes: every message and the rows
under it in the order `message_events`' RESTRICT allows, each attachment, raw payload
and staged upload's object queued on the cleanup ledger before the rows naming it go —
the cleanup worker deletes the objects after the transaction commits — and a message
still waiting in the outbox with its job. A conversation that only suggests or lists
the person keeps its own customer and loses the suggestion, its reasoning and the
candidate row. See [Personal data and anonymisation](customers.md#personal-data-and-anonymisation).

What the erase does not cover, on the record: this module takes no customer lock, so a
message written into one of the person's conversations while the erase runs goes with
the conversation by cascade rather than through the ledger — or trips `message_events`'
RESTRICT, which rolls the customer back and the worker retries next cycle; nothing stops
a conversation being linked to the archived "Anonymised person" afterwards;
`communications.suppressions` can still hold the person's address; and a customer's
`customer.peppol_lookup` entries keep their `smpHost`, derived from the participant id.
```

In `docs/projects.md`, after the paragraph that begins `Projects also **holds customer references**`, add:

```markdown
It hands a private person's projects over too — `contracts.CustomerPersonalData`
([module boundaries rule 9](module-boundaries.md#the-rules)): code, name, status and
dates of every project billed to them, in their export's `modules.projects`. Their
anonymisation keeps every project: invoiced work stays, a project stores no customer
name to blank, and `customerName` reads the anonymised customer's through the
directory from then on. A project named after the person is free text this module
does not rewrite. See [Personal data and anonymisation](customers.md#personal-data-and-anonymisation).
```

```bash
cd /home/anders/projects/vantigo/vantigo
cat > /tmp/claude-1000/msg-gdpr-3.txt <<'EOF'
feat(contracts): communications, energy and projects hand over and erase what they hold about a person

Communications exports a person's conversations — each message's
direction, date and text, each attachment's name — and erases them
inside the caller's transaction through the retention worker's own
deletes, now one function both call, every object key on the cleanup
ledger; a suggestion or candidate row naming the person elsewhere goes
too. Energy exports supply periods with the metering point's address and
projects the person's projects; both keep everything on erase and report
their kind at zero.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
PATHS="apps/server/internal/communications/customer_personal_data.go apps/server/internal/communications/customer_personal_data_test.go \
 apps/server/internal/communications/queries/customer_personal_data.sql apps/server/internal/communications/store/customer_personal_data.sql.go \
 apps/server/internal/communications/retention.go apps/server/internal/communications/module.go \
 apps/server/internal/energy/customer_personal_data.go apps/server/internal/energy/customer_personal_data_test.go \
 apps/server/internal/energy/queries/customer_personal_data.sql apps/server/internal/energy/store/customer_personal_data.sql.go \
 apps/server/internal/energy/module.go \
 apps/server/internal/projects/customer_personal_data.go apps/server/internal/projects/customer_personal_data_test.go \
 apps/server/internal/projects/queries/customer_personal_data.sql apps/server/internal/projects/store/customer_personal_data.sql.go \
 apps/server/internal/projects/module.go docs/communications.md docs/projects.md"
git add $PATHS && git commit -F /tmp/claude-1000/msg-gdpr-3.txt -- $PATHS
git show --stat HEAD && git status --short
```

---

### Task 4: The migration and the export (D3, D4's columns)

The two columns, the permission key, `anonymisation?` on every customer response, and `GET /customers/{id}/personal-data` — everything this module holds about a private person, the modules' sections through the slot, streamed as a JSON attachment.

**Files:**
- Create: `apps/server/internal/db/migrations/00030_customers_personal_data.sql`, `apps/server/internal/customers/queries/personal_data.sql` (+ generated `store/personal_data.sql.go`), `apps/server/internal/customers/personal_data.go`, `apps/server/internal/customers/personal_data_test.go`
- Modify: `apps/server/internal/customers/sqlc.yaml`, `apps/server/internal/db/schema_test.go`, `openapi/customers.yaml` (+ generated `apps/server/internal/openapi/specs/customers.yaml`, `internal/customers/gen/api.gen.go`, `internal/customers/store/models.go`, `apps/customers/frontend/src/api-schema.d.ts`), `openapi/COVERAGE.md`, `apps/server/internal/openapi/openapi_test.go`, `apps/server/internal/customers/owner.go`, `customers.go`, `module.go`, `module_test.go`
- Read first (do not change): `csvexport.go:38-66` (`csvDownload`), `contacts.go:505-551` (`GetCustomersByIdContacts`), `timeline.go:412-461` (`timelineResponse`), `follow_ups.go:143-230` (`decorateFollowUpAssignees`), `owner.go:72-240` (the decoration), `merge_test.go:30-160` (the fakes and helpers this file's tests sit beside)

**Interfaces:**
- Consumes: `Deps.CustomerPersonalData` (Task 2), the three implementations' sections (Task 3).
- Produces Go: `(*server).GetCustomersByIdPersonalData`; `personalDataDownload`; `personalDataNotAPerson(number int64, name string) *gen.CustomerConflictProblem`; `customerDecoration.anonymisationOf(id) *gen.CustomerAnonymisation`; queries `AnonymisationForCustomers`, `CustomerForPersonalData`, `ListTimelineEntriesForExport`.
- Wire: `GET /api/v1/customers/{id}/personal-data` → 200 `CustomerPersonalData` as `application/json`, `Content-Disposition: attachment; filename="customer-<number>-personal-data.json"`, `Cache-Control: private, no-store`; 404; 409 `personal_data_not_a_person`. `SafeCustomerResponse.anonymisation?: {anonymiseOn, anonymisedAt?}`. New sensitive key `customers:personal-data`.

- [ ] **Step 1: The migration**

Create `apps/server/internal/db/migrations/00030_customers_personal_data.sql`:

```sql
-- +goose Up
-- A private person's anonymisation (customers GDPR design D4): the day it is
-- scheduled for, and the moment the worker ran it. Both NULL for every customer
-- nobody scheduled — which is nearly all of them, and every business.
--
-- anonymise_on is a calendar day, not an instant: a person chooses a date, and
-- the worker takes every customer whose day has come in UTC, the day every
-- other date-only field of this module is counted in. It has no default, and no
-- column says why a date was chosen: Norwegian bookkeeping rules keep accounting
-- material for years after the fiscal year, this installation invoices nothing
-- yet, and the person scheduling is the one who knows what was invoiced — so the
-- schema encodes no retention period at all (docs/customers.md, Personal data
-- and anonymisation). anonymise_on stays set once the customer is anonymised,
-- the record of what was asked for.
--
-- anonymised_at set is what makes the customer read-only (the lock-time check
-- that answers customer_anonymised) and what the worker skips. The partial
-- index is the worker's one lookup — the scheduled and not yet anonymised —
-- and holds only them.
ALTER TABLE customers.customers
    ADD COLUMN anonymise_on date,
    ADD COLUMN anonymised_at timestamptz;
CREATE INDEX ix_customers_anonymise_on ON customers.customers (anonymise_on)
    WHERE anonymise_on IS NOT NULL AND anonymised_at IS NULL;

-- +goose Down
DROP INDEX customers.ix_customers_anonymise_on;
ALTER TABLE customers.customers DROP COLUMN anonymised_at, DROP COLUMN anonymise_on;
```

In `apps/server/internal/customers/sqlc.yaml`, add `      - ../db/migrations/00030_customers_personal_data.sql` directly after the `00029_customers_merge.sql` line.

In `apps/server/internal/db/schema_test.go`'s `TestCustomersBaseline_AppliesAndIsIdempotent`, directly after the `ix_customers_merged_into` check and before the `tenantIDColumns` query, add:

```go
	// anonymise_on and anonymised_at (00030) are customers GDPR design D4's
	// schedule and its outcome: a date and an instant, both nullable — most
	// customers are never scheduled — and the worker's one lookup, the due
	// customers, has a partial index holding only the scheduled and not yet
	// anonymised. COLLATE "C": the underscore must sort as a character here, not
	// be skipped the way a linguistic collation skips it.
	var anonymisationColumns string
	if err := pool.QueryRow(ctx, `SELECT coalesce(string_agg(column_name || ':' || data_type || ':' || is_nullable, ',' ORDER BY column_name COLLATE "C"), 'MISSING')
	                              FROM information_schema.columns
	                              WHERE table_schema = 'customers' AND table_name = 'customers'
	                                AND column_name IN ('anonymise_on', 'anonymised_at')`).Scan(&anonymisationColumns); err != nil {
		t.Fatalf("read the anonymisation columns: %v", err)
	}
	if want := "anonymise_on:date:YES,anonymised_at:timestamp with time zone:YES"; anonymisationColumns != want {
		t.Errorf("anonymisation columns = %q, want %q", anonymisationColumns, want)
	}
	var anonymiseIndexDef string
	if err := pool.QueryRow(ctx, `SELECT indexdef FROM pg_indexes
	                              WHERE schemaname = 'customers' AND indexname = 'ix_customers_anonymise_on'`).Scan(&anonymiseIndexDef); err != nil {
		t.Fatalf("read ix_customers_anonymise_on definition: %v", err)
	}
	if strings.Contains(anonymiseIndexDef, "UNIQUE") ||
		!strings.Contains(anonymiseIndexDef, "WHERE ((anonymise_on IS NOT NULL) AND (anonymised_at IS NULL))") {
		t.Errorf("ix_customers_anonymise_on = %q, want a non-unique PARTIAL index on the scheduled, not yet anonymised", anonymiseIndexDef)
	}
```

- [ ] **Step 2: The queries**

Create `apps/server/internal/customers/queries/personal_data.sql`:

```sql
-- This file is every statement customers GDPR design D3 and D4 add: the
-- export's reads, the anonymisation decoration, the schedule's writes and the
-- anonymisation worker's. They are one file because each is meaningful only to
-- a person's data leaving or being taken out, and one file is one place to read
-- what an anonymisation writes — merge.sql's reasoning.

-- name: AnonymisationForCustomers :many
-- AnonymisationForCustomers is the anonymisation of a whole page of customers
-- in ONE query (design D4, SafeCustomerResponse.anonymisation), the merge
-- marker's shape (owner.go): a batched read beside the row rather than two
-- columns on the five customer row types. A customer never scheduled has no
-- row; one anonymised keeps its anonymise_on, so the one predicate covers both.
SELECT id AS customer_id, anonymise_on, anonymised_at
FROM customers.customers
WHERE id = ANY(@customer_ids::int[]) AND anonymise_on IS NOT NULL;

-- name: CustomerForPersonalData :one
-- CustomerForPersonalData is the export's read of the customer row (design D3)
-- and the scheduling's pool read before its lock (D4): the type and status the
-- refusals check, the schedule, and every column the file hands over — the
-- identity, the contact info and the billing profile's eleven columns.
SELECT id, customer_number, name, type, status, revision, merged_into_customer_id, anonymise_on, anonymised_at,
       legal_country, legal_id, legal_name, legal_source, legal_type,
       email, phone, website, owner_user_id, group_id, created_at, updated_at,
       invoice_email, reminder_email, payment_terms_days, currency, language,
       invoice_delivery, reminder_delivery, peppol_id, gln, buyer_reference, default_bill_rate
FROM customers.customers
WHERE id = @id;

-- name: ListTimelineEntriesForExport :many
-- ListTimelineEntriesForExport is every timeline entry of one customer for its
-- export (design D3): deleted ones included — a soft-deleted note is still held
-- — oldest first, the order a person reads their own history in. Revisions are
-- not part of the file.
SELECT id, customer_id, provenance, producer, event_type, occurred_on, occurred_at, summary, note,
       source_url, payload_json, payload_version, current_revision, state, actor_kind, actor_display,
       created_at, updated_at, deleted_at, actor_user_id, follow_up_on, follow_up_assignee_user_id, follow_up_done_at
FROM customers.customers_timeline_entries
WHERE customer_id = @customer_id
ORDER BY occurred_on, occurred_at NULLS FIRST, id;
```

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server && mise exec -- go generate ./... && git status --short
```
Expected: `store/models.go` (the two columns on `CustomersCustomer`: `AnonymiseOn pgtype.Date`, `AnonymisedAt *time.Time`) and `store/personal_data.sql.go`. `ListTimelineEntriesForExport` answers `[]store.CustomersCustomersTimelineEntry` (its select list is the table's, as `GetTimelineEntry`'s is); if sqlc made it a row type of its own, convert it with `store.CustomersCustomersTimelineEntry(r)` the way `fromInsertManualRow` does.

- [ ] **Step 3: The contract**

In `openapi/customers.yaml`, under `components.schemas`:

Directly after the `CustomerAddressRequest` schema, add:

```yaml
        CustomerAnonymisation:
            description: "A private person's anonymisation (customers GDPR design D4): anonymiseOn is the UTC calendar day it is scheduled for, anonymisedAt the moment the worker ran it — absent until then, omitted, never null. From anonymisedAt on the customer is read-only: every write answers 409 customer_anonymised."
            properties:
                anonymisedAt:
                    format: date-time
                    type: string
                anonymiseOn:
                    format: date
                    type: string
            required:
                - anonymiseOn
            type: object
```

Directly after the `CustomerPeppolLookup` schema, add:

```yaml
        CustomerPersonalData:
            description: "GET /customers/{id}/personal-data's file (customers GDPR design D3): everything this installation holds about a private person. exportedAt is when it was made."
            properties:
                contacts:
                    description: Every contact linked to the customer, as the contact is stored, with the association's own title, phone, email and roles.
                    items:
                        $ref: '#/components/schemas/CustomerContactResponse'
                    type: array
                customer:
                    $ref: '#/components/schemas/CustomerPersonalDataCustomer'
                exportedAt:
                    format: date-time
                    type: string
                modules:
                    additionalProperties: true
                    description: "Each other module's section, under the module's name — communications (the person's conversations: subject, dates, each message's direction, date and text body, attachment names), energy (supply periods with the metering point's address), projects (code, name, status and dates). A module holding nothing for the customer has no key."
                    type: object
                timeline:
                    description: Every timeline entry, oldest first, deleted ones included (state says which), each with its payload, actor and follow-up. Revisions are not part of the file.
                    items:
                        $ref: '#/components/schemas/TimelineResponse'
                    type: array
            required:
                - exportedAt
                - customer
                - contacts
                - timeline
                - modules
            type: object
        CustomerPersonalDataBillingProfile:
            description: The billing profile's own stored values (customers GDPR design D3) — what the customer row holds, not the resolved profile GET .../billing-profile answers with its warnings and its group's default. Unset, a field is absent.
            properties:
                buyerReference:
                    nullable: true
                    type: string
                currency:
                    nullable: true
                    type: string
                defaultBillRate:
                    format: double
                    nullable: true
                    type: number
                gln:
                    nullable: true
                    type: string
                invoiceDelivery:
                    nullable: true
                    type: string
                invoiceEmail:
                    nullable: true
                    type: string
                language:
                    nullable: true
                    type: string
                paymentTermsDays:
                    format: int32
                    nullable: true
                    type: integer
                peppolId:
                    nullable: true
                    type: string
                reminderDelivery:
                    nullable: true
                    type: string
                reminderEmail:
                    nullable: true
                    type: string
            type: object
        CustomerPersonalDataCustomer:
            description: "The customer row in a personal-data file (customers GDPR design D3): what SafeCustomerResponse carries, plus the legal identity whatever the caller's legal-identity permission — the file is shaped by customers:personal-data alone — and the addresses and billing profile's own values."
            properties:
                addresses:
                    items:
                        $ref: '#/components/schemas/CustomerAddress'
                    type: array
                anonymisation:
                    $ref: '#/components/schemas/CustomerAnonymisation'
                billingProfile:
                    $ref: '#/components/schemas/CustomerPersonalDataBillingProfile'
                contactInfo:
                    $ref: '#/components/schemas/CustomerContactInfo'
                createdAt:
                    format: date-time
                    type: string
                customerNumber:
                    format: int64
                    type: integer
                group:
                    $ref: '#/components/schemas/CustomerGroupRef'
                id:
                    format: int32
                    type: integer
                identity:
                    $ref: '#/components/schemas/LegalIdentityResponse'
                mergedInto:
                    $ref: '#/components/schemas/CustomerReference'
                name:
                    type: string
                owner:
                    $ref: '#/components/schemas/CustomerOwner'
                status:
                    type: string
                tags:
                    items:
                        $ref: '#/components/schemas/CustomerTag'
                    type: array
                type:
                    type: string
                updatedAt:
                    format: date-time
                    type: string
            required:
                - id
                - customerNumber
                - name
                - type
                - status
                - createdAt
                - updatedAt
                - contactInfo
                - addresses
                - billingProfile
                - tags
            type: object
```

In `SafeCustomerResponse.properties`, directly before `contactInfo:`, add:

```yaml
                anonymisation:
                    allOf:
                        - $ref: '#/components/schemas/CustomerAnonymisation'
                    description: "A private person's anonymisation, scheduled or done (customers GDPR design D4). Absent unless one is scheduled — omitted, never null, like owner. Once anonymisedAt is set the customer is read-only, and its name, identity and contact info are gone."
```

In `CustomerConflictProblem.description`, replace `and customer_merged, the answer a write to a customer merged away gets (the same design).` with `customer_merged, the answer a write to a customer merged away gets (the same design), and personal_data_not_a_person, personal_data_customer_active and customer_anonymised (customers GDPR design D3, D4 — the last the answer a write to an anonymised customer gets).`

Under `paths`, directly before `    /api/v1/customers/{id}/registry-record:`, add:

```yaml
    /api/v1/customers/{id}/personal-data:
        get:
            description: "Everything this installation holds about a private person, in one file (customers GDPR design D3): the customer — number, name, type, status, legal identity, contact info, addresses, the billing profile's own values, owner, group, tags, mergedInto and anonymisation — every contact linked to it as the contact is stored, with the association's title, phone, email and roles, every timeline entry (deleted ones included, revisions left out) with its payload, actor and follow-up, and each other module's section under modules, keyed by the module's name. Served as an attachment named customer-<customerNumber>-personal-data.json, and never cached. Shaped by nothing but customers:personal-data: the key means may hand this person their data, so the legal identity and the contacts are in the file without customers:legal-identity-view or customers:contacts-view. An anonymised customer's file is what is left. 404 when the customer does not exist; 409 personal_data_not_a_person for a business, which is not a data subject."
            operationId: getCustomersByIdPersonalData
            parameters:
                - in: path
                  name: id
                  required: true
                  schema:
                    format: int32
                    type: integer
            responses:
                "200":
                    content:
                        application/json:
                            schema:
                                $ref: '#/components/schemas/CustomerPersonalData'
                    description: OK — served as an attachment named customer-<customerNumber>-personal-data.json, and never cached.
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
                "404":
                    description: Not Found
                "409":
                    content:
                        application/problem+json:
                            schema:
                                $ref: '#/components/schemas/CustomerConflictProblem'
                    description: Conflict — personal_data_not_a_person
            summary: Export a private person's data
            tags:
                - Customers
            x-vantigo-access: permission:customers:personal-data+customers:view
```

In `apps/server/internal/openapi/openapi_test.go`'s `KnownServeMuxConflicts`, directly after `"GET /api/v1/customers/contacts/{id} ⟷ GET /api/v1/customers/{id}/overview",`, add:

```go
	"GET /api/v1/customers/contacts/{id} ⟷ GET /api/v1/customers/{id}/personal-data",
```

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server && mise exec -- go generate ./... && mise exec -- go generate ./... && git status --short
mise exec -- go test -count=1 ./internal/openapi/...
```
Expected: the second generate changes nothing; `TestServeMuxConflictsArePinned` passes with the one new pin (if it prints a different pair, pin exactly what it prints); the frozen corpus still validates. The generated names this plan uses: `gen.GetCustomersByIdPersonalDataRequestObject`, `gen.GetCustomersByIdPersonalDataResponseObject` (whose method is `VisitGetCustomersByIdPersonalDataResponse`), `gen.GetCustomersByIdPersonalData404Response`, `gen.GetCustomersByIdPersonalData409ApplicationProblemPlusJSONResponse`, `gen.CustomerPersonalData` (`ExportedAt`, `Customer`, `Contacts`, `Timeline`, `Modules map[string]interface{}`), `gen.CustomerPersonalDataCustomer`, `gen.CustomerPersonalDataBillingProfile`, `gen.CustomerAnonymisation` (`AnonymiseOn openapi_types.Date`, `AnonymisedAt *time.Time`), and `gen.SafeCustomerResponse.Anonymisation`. Until Step 5 the package does not compile (the server lacks the new method); that is expected.

- [ ] **Step 4: Write the failing tests**

Create `apps/server/internal/customers/personal_data_test.go`:

```go
package customers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is GET /customers/{id}/personal-data (customers GDPR design D3),
// and the fakes the anonymisation tests share: another module's
// contracts.CustomerPersonalData, faked because depguard keeps communications,
// energy and projects out of this package even in a test — each proves its own
// SQL in its own package.

// fakePersonalData stands in for another module's contracts.CustomerPersonalData.
// It answers the section it was given, records every call, and — through
// during — can look into the anonymisation's own transaction while it is open,
// or fail it.
type fakePersonalData struct {
	section any
	erased  []contracts.ErasedData
	during  func(ctx context.Context, tx pgx.Tx, customerID int32) error

	mu      sync.Mutex
	exports []int32
	erases  []int32
}

func (f *fakePersonalData) ExportCustomerData(_ context.Context, customerID int32) (any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.exports = append(f.exports, customerID)
	return f.section, nil
}

func (f *fakePersonalData) EraseCustomerData(ctx context.Context, tx pgx.Tx, customerID int32) ([]contracts.ErasedData, error) {
	f.mu.Lock()
	f.erases = append(f.erases, customerID)
	f.mu.Unlock()
	if f.during != nil {
		if err := f.during(ctx, tx, customerID); err != nil {
			return nil, err
		}
	}
	return f.erased, nil
}

func (f *fakePersonalData) erasesSoFar() []int32 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.erases)
}

// personalDataClient signs in the caller this delivery adds: its key and view,
// nothing else. The export needs nothing else — it is shaped by nothing else
// (design D3) — and the scheduling needs nothing else either (D4).
func personalDataClient(t *testing.T, h *modtest.Harness) *modtest.Client {
	t.Helper()
	return h.SignIn(t, "customers:view", "customers:personal-data")
}

func getPersonalData(t *testing.T, c *modtest.Client, id int32) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/personal-data", id), nil)
}

type anonymisationJSON struct {
	AnonymiseOn  string     `json:"anonymiseOn"`
	AnonymisedAt *time.Time `json:"anonymisedAt"`
}

// personalDataJSON decodes the file, as much of it as the tests read.
type personalDataJSON struct {
	ExportedAt time.Time `json:"exportedAt"`
	Customer   struct {
		Id             int32  `json:"id"`
		CustomerNumber int64  `json:"customerNumber"`
		Name           string `json:"name"`
		Type           string `json:"type"`
		Status         string `json:"status"`
		Identity       *struct {
			Country string `json:"country"`
			Id      string `json:"id"`
		} `json:"identity"`
		ContactInfo    contactInfoJSON `json:"contactInfo"`
		Addresses      []addressJSON   `json:"addresses"`
		BillingProfile struct {
			InvoiceEmail *string `json:"invoiceEmail"`
			Currency     *string `json:"currency"`
		} `json:"billingProfile"`
		Owner         *ownerJSON         `json:"owner"`
		Group         *groupRefJSON      `json:"group"`
		Tags          []tagJSON          `json:"tags"`
		Anonymisation *anonymisationJSON `json:"anonymisation"`
	} `json:"customer"`
	Contacts []struct {
		Contact struct {
			Id        int32   `json:"id"`
			FirstName string  `json:"firstName"`
			Email     *string `json:"email"`
		} `json:"contact"`
		Title *string `json:"title"`
	} `json:"contacts"`
	Timeline []struct {
		timelineEntryJSON
		FollowUp *struct {
			DueOn string `json:"dueOn"`
		} `json:"followUp"`
	} `json:"timeline"`
	Modules map[string]json.RawMessage `json:"modules"`
}

// setPersonIdentity gives a customer a private person's legal identity
// directly — Swedish, so the national-identity rule (Norway's) has nothing to
// say about the fixture.
func setPersonIdentity(t *testing.T, h *modtest.Harness, id int32) {
	t.Helper()
	h.Exec(t, `UPDATE customers.customers SET legal_country = 'se', legal_id = '19800101-1234', legal_name = 'Kari Nordmann',
	           legal_source = 'manual', legal_type = 'person' WHERE id = $1`, id)
}

// TestGetCustomersByIdPersonalData_HandsAPersonEverythingInOneFile is design D3:
// the customer with everything on its row and beside it — the legal identity
// for a caller who holds neither legal-identity-view nor contacts-view — its
// contacts, every timeline entry with its follow-up, and each module's section
// under its name, a module with nothing having no key. It is an attachment,
// named by the customer number, never cached.
func TestGetCustomersByIdPersonalData_HandsAPersonEverythingInOneFile(t *testing.T) {
	t.Parallel()
	alpha := &fakePersonalData{section: map[string]any{"things": []string{"one"}}}
	quiet := &fakePersonalData{}
	h := newHarness(t, modtest.WithCustomerPersonalData(
		contracts.CustomerPersonalDataHolder{Module: "alpha", Data: alpha},
		contracts.CustomerPersonalDataHolder{Module: "quiet", Data: quiet},
	))
	c, userID := authenticatedClientWithID(t, h)
	setDisplayName(t, h, userID, "Siri Saksbehandler")
	person := createCustomerOfType(t, c, "Kari Nordmann", "person")
	setPersonIdentity(t, h, person.Id)
	if r := putContactInfo(t, c, person.Id, map[string]any{"email": "kari@example.test"}); r.Status != http.StatusOK {
		t.Fatalf("contact info: status %d body %s", r.Status, r.Body)
	}
	createAddress(t, c, person.Id, fullAddressBody("postal", nil))
	if r := putBillingProfile(t, c, person.Id, map[string]any{"invoiceEmail": "faktura@example.test", "currency": "NOK"}); r.Status != http.StatusOK {
		t.Fatalf("billing profile: status %d body %s", r.Status, r.Body)
	}
	if r := putOwner(t, c, person.Id, map[string]any{"ownerUserId": userID}); r.Status != http.StatusOK {
		t.Fatalf("owner: status %d body %s", r.Status, r.Body)
	}
	group := createGroup(t, c, map[string]any{"name": "Privatkunder"})
	if r := putCustomerGroup(t, c, person.Id, map[string]any{"groupId": group.Id}); r.Status != http.StatusOK {
		t.Fatalf("group: status %d body %s", r.Status, r.Body)
	}
	tag := createTag(t, c, map[string]any{"name": "Nabo"})
	if r := putCustomerTags(t, c, person.Id, []string{tag.Id}); r.Status != http.StatusOK {
		t.Fatalf("tags: status %d body %s", r.Status, r.Body)
	}
	contact := createContact(t, c, map[string]any{"firstName": "Per", "lastName": "Nordmann", "email": "per@example.test"})
	attachContact(t, c, person.Id, contact.Id, "Ektefelle")
	entry := createWithFollowUp(t, c, person.Id, day(h, 0), "Ringte om strømavtalen", map[string]any{"dueOn": day(h, 7)})

	r := getPersonalData(t, personalDataClient(t, h), person.Id)
	if r.Status != http.StatusOK {
		t.Fatalf("personal data: status %d body %s, want 200", r.Status, r.Body)
	}
	if got := r.Header("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	if got, want := r.Header("Content-Disposition"), fmt.Sprintf(`attachment; filename="customer-%d-personal-data.json"`, person.CustomerNumber); got != want {
		t.Errorf("Content-Disposition = %q, want %q", got, want)
	}
	if got := r.Header("Cache-Control"); got != "private, no-store" {
		t.Errorf("Cache-Control = %q, want private, no-store", got)
	}

	var file personalDataJSON
	r.JSON(&file)
	got := file.Customer
	if !file.ExportedAt.Equal(h.Now()) || got.Id != person.Id || got.CustomerNumber != person.CustomerNumber || got.Name != "Kari Nordmann" || got.Type != "person" {
		t.Errorf("file = %+v, want Kari Nordmann's, made now", file)
	}
	if got.Identity == nil || got.Identity.Country != "se" || got.Identity.Id != "19800101-1234" {
		t.Errorf("identity = %+v, want hers, whatever the caller's legal-identity keys", got.Identity)
	}
	if str(got.ContactInfo.Email) != "kari@example.test" || len(got.Addresses) != 1 ||
		str(got.BillingProfile.InvoiceEmail) != "faktura@example.test" || str(got.BillingProfile.Currency) != "NOK" {
		t.Errorf("contact info %+v, addresses %+v, billing %+v", got.ContactInfo, got.Addresses, got.BillingProfile)
	}
	if got.Owner == nil || got.Owner.DisplayName != "Siri Saksbehandler" || got.Group == nil || got.Group.Name != "Privatkunder" ||
		!slices.Equal(tagNamesOf(got.Tags), []string{"Nabo"}) || got.Anonymisation != nil {
		t.Errorf("owner %+v, group %+v, tags %+v, anonymisation %+v", got.Owner, got.Group, got.Tags, got.Anonymisation)
	}
	if len(file.Contacts) != 1 || file.Contacts[0].Contact.Id != contact.Id || file.Contacts[0].Contact.FirstName != "Per" ||
		str(file.Contacts[0].Title) != "Ektefelle" {
		t.Errorf("contacts = %+v, want Per as Ektefelle", file.Contacts)
	}
	var manual bool
	for _, e := range file.Timeline {
		if e.Id == entry.Id {
			manual = str(e.Note) == "Ringte om strømavtalen" && e.FollowUp != nil && e.FollowUp.DueOn == day(h, 7)
		}
	}
	if !manual || len(file.Timeline) < 2 {
		t.Errorf("timeline = %+v, want every entry, the note with its follow-up among them", file.Timeline)
	}
	if string(file.Modules["alpha"]) != `{"things":["one"]}` {
		t.Errorf("modules.alpha = %s, want the fake's section", file.Modules["alpha"])
	}
	if _, ok := file.Modules["quiet"]; ok || len(file.Modules) != 1 {
		t.Errorf("modules = %v, want alpha's alone: a module with nothing has no key", file.Modules)
	}
	if !slices.Equal(alpha.exports, []int32{person.Id}) || !slices.Equal(quiet.exports, []int32{person.Id}) {
		t.Errorf("exports asked for = %v / %v, want the one customer each", alpha.exports, quiet.exports)
	}
}

// A business is not a data subject (design D3): its export is refused, and the
// fakes are never asked. The key, and view, are both needed; a customer that
// does not exist is a 404.
func TestGetCustomersByIdPersonalData_IsAPrivatePersonsAlone(t *testing.T) {
	t.Parallel()
	fake := &fakePersonalData{}
	h := newHarness(t, modtest.WithCustomerPersonalData(contracts.CustomerPersonalDataHolder{Module: "fake", Data: fake}))
	c := authenticatedClient(t, h)
	business := createCustomer(t, c, "Acme AS")
	person := createCustomerOfType(t, c, "Kari Nordmann", "person")

	problem := refusedWith(t, getPersonalData(t, personalDataClient(t, h), business.Id), "personal_data_not_a_person")
	if problem.Title != "Customer is not a private person" {
		t.Errorf("title = %q", problem.Title)
	}
	if len(fake.exports) != 0 {
		t.Errorf("the fake was asked about a business: %v", fake.exports)
	}
	if r := getPersonalData(t, personalDataClient(t, h), 999999); r.Status != http.StatusNotFound {
		t.Errorf("unknown customer: status %d, want 404", r.Status)
	}
	for _, keys := range [][]string{{"customers:view", "customers:legal-identity-view", "customers:contacts-view"}, {"customers:personal-data"}} {
		if r := getPersonalData(t, h.SignIn(t, keys...), person.Id); r.Status != http.StatusForbidden {
			t.Errorf("%v: status %d, want 403", keys, r.Status)
		}
	}
}
```

Run them: `mise exec -- go test -count=1 -run 'PersonalData' ./internal/customers/` — FAIL to compile: `*server does not implement gen.StrictServerInterface (missing method GetCustomersByIdPersonalData)`.

- [ ] **Step 5: The permission, the decoration, the export**

In `apps/server/internal/customers/module.go`, append to `permissions`:

```go
	{Key: "customers:personal-data", Display: "Manage personal data", Description: "Hand a private person all the data held about them, and schedule the anonymisation of an archived private person.", Category: "Customers", Sensitive: true, Delegable: true},
```

extend the doc comment above `permissions` with `customers:personal-data (customers GDPR design D3, D4) is the third: handing a person their whole file reads what legal-identity-view and contacts-view each guard, and an anonymisation removes more than any delete, so it is its own sensitive key, implied by nothing.`, and change `its fifteen permissions` to `its sixteen permissions` in `Module`'s doc comment. In `module_test.go`'s `TestModule_DeclaresItsPermissionCatalog`, change `fifteen` to `sixteen`, add `customers:personal-data (customers GDPR design D3, D4),` to the list of keys without a .NET ancestor, and append the same `contracts.Permission` literal to `want`.

In `apps/server/internal/customers/owner.go`, add `openapi_types "github.com/oapi-codegen/runtime/types"` to the imports; add to `customerDecoration`, after `mergedInto`:

```go
	// anonymisations is each customer's anonymisation, scheduled or done
	// (customers GDPR design D4); a customer never scheduled has no entry.
	anonymisations map[int32]gen.CustomerAnonymisation
```

add `anonymisations: map[int32]gen.CustomerAnonymisation{},` to `decorateKnowing`'s literal, and directly after the merge markers' loop:

```go
	// The anonymisation is one more batched query over the same ids (customers
	// GDPR design D4), for the merge marker's reason: two columns almost every
	// customer answers without, on five row types that would otherwise each
	// grow them.
	schedules, err := q.AnonymisationForCustomers(ctx, customerIDs)
	if err != nil {
		return customerDecoration{}, fmt.Errorf("customers: load anonymisations: %w", err)
	}
	for _, a := range schedules {
		dec.anonymisations[a.CustomerID] = gen.CustomerAnonymisation{
			AnonymiseOn:  openapi_types.Date{Time: a.AnonymiseOn.Time},
			AnonymisedAt: a.AnonymisedAt,
		}
	}
```

and after `merged`:

```go
// anonymisationOf is one customer's anonymisation, scheduled or done, or nil —
// absent on the wire, never null, the idiom merged beside it follows.
func (d customerDecoration) anonymisationOf(customerID int32) *gen.CustomerAnonymisation {
	if a, ok := d.anonymisations[customerID]; ok {
		return &a
	}
	return nil
}
```

Update the doc comments of `customerDecoration` and `decorate` to say five lookups: `…and the customer it was merged into, if any (customers merge design D3), and its anonymisation (customers GDPR design D4).` / `…in one directory call and four queries.`

In `apps/server/internal/customers/customers.go`'s `safeCustomerResponse`, add `Anonymisation:  dec.anonymisationOf(row.ID),` directly after `MergedInto:     dec.merged(row.ID),`, and in its doc comment after `(customers merge design D3)` add `, and its anonymisation (customers GDPR design D4)`.

Create `apps/server/internal/customers/personal_data.go`:

```go
package customers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
	"github.com/vantigo-io/vantigo/server/internal/customers/store"
	"github.com/vantigo-io/vantigo/server/internal/db"
)

// This file is GET /customers/{id}/personal-data (customers GDPR design D3): a
// private person's data, all of it, in one file — what this module holds, read
// in one snapshot, and what every other module holds, through
// contracts.CustomerPersonalData (docs/module-boundaries.md rule 9). It is
// shaped by nothing but customers:personal-data: that key means "may hand this
// person their data", so the legal identity and the contacts are in the file
// whether or not the caller could read them one by one. It is built in memory
// and streamed from the request, the CSV export's shape — no object store, no
// job: one person's data is one response.

// personalDataNotAPerson is the export's and the scheduling's refusal for a
// business (design D3): a company is not a data subject, and its contacts are
// people handled through customers of their own if they are anything here.
func personalDataNotAPerson(number int64, name string) *gen.CustomerConflictProblem {
	return mergeConflict("Customer is not a private person", "personal_data_not_a_person", fmt.Sprintf(
		"%s is a business, and personal data is a private person's. A business's contacts are handled through customers of their own, if they are customers at all.",
		customerLabel(number, name)))
}

// personalDataDownload writes the file: the generated 200 would be a bare
// application/json that names no file, and this answer has to say what to call
// it and that nobody may cache it — csvDownload's reasoning, and its headers.
// The body is marshalled before a header is written, so a failure is a 500
// rather than half a file.
type personalDataDownload struct {
	body     gen.CustomerPersonalData
	fileName string
}

func (d personalDataDownload) VisitGetCustomersByIdPersonalDataResponse(w http.ResponseWriter) error {
	body, err := json.Marshal(d.body)
	if err != nil {
		return fmt.Errorf("customers: encode the personal data file: %w", err)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", d.fileName))
	// A person's whole file: in nobody's cache, least of all a shared one.
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	_, err = w.Write(body)
	return err
}

// GetCustomersByIdPersonalData Export a private person's data
// (GET /api/v1/customers/{id}/personal-data)
//
// Order: (1) this module's own rows in one read-only REPEATABLE READ snapshot —
// the contacts list's reason: several reads answering one file must see one
// instant — its 404, and the refusal for a business, read from the same row;
// (2) the decoration and the follow-up assignees from the pool, after the
// snapshot, since both ask the user directory; (3) each module's section,
// outside any transaction of this module's (rule 9), a nil one leaving its key
// out. An anonymised customer's file is what is left of it.
func (s *server) GetCustomersByIdPersonalData(ctx context.Context, req gen.GetCustomersByIdPersonalDataRequestObject) (gen.GetCustomersByIdPersonalDataResponseObject, error) {
	var (
		row       store.CustomerForPersonalDataRow
		customer  customerRow
		addresses []store.CustomersCustomerAddress
		contacts  []store.ListContactAssociationsForCustomerRow
		roleRows  []store.ContactRolesForCustomerRow
		entries   []store.CustomersCustomersTimelineEntry
	)
	err := db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		var err error
		if row, err = txq.CustomerForPersonalData(ctx, req.Id); err != nil || row.Type != "person" {
			return err
		}
		plain, err := txq.GetCustomer(ctx, req.Id)
		if err != nil {
			return err
		}
		summary, err := txq.CustomerTimelineSummary(ctx, req.Id)
		if err != nil {
			return err
		}
		customer = fromCustomerRow(plain, summary)
		if addresses, err = txq.ListCustomerAddresses(ctx, req.Id); err != nil {
			return err
		}
		if contacts, err = txq.ListContactAssociationsForCustomer(ctx, req.Id); err != nil {
			return err
		}
		if roleRows, err = txq.ContactRolesForCustomer(ctx, req.Id); err != nil {
			return err
		}
		entries, err = txq.ListTimelineEntriesForExport(ctx, req.Id)
		return err
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return gen.GetCustomersByIdPersonalData404Response{}, nil
	case err != nil:
		return nil, fmt.Errorf("customers: read customer %d's personal data: %w", req.Id, err)
	case row.Type != "person":
		return gen.GetCustomersByIdPersonalData409ApplicationProblemPlusJSONResponse(*personalDataNotAPerson(row.CustomerNumber, row.Name)), nil
	}

	q := store.New(s.deps.Pool)
	dec, err := s.decorate(ctx, q, customer)
	if err != nil {
		return nil, err
	}
	var assignees []uuid.UUID
	for _, e := range entries {
		if e.FollowUpAssigneeUserID != nil {
			assignees = append(assignees, *e.FollowUpAssigneeUserID)
		}
	}
	followUps, err := s.decorateFollowUpAssignees(ctx, assignees)
	if err != nil {
		return nil, err
	}

	sections := map[string]interface{}{}
	for _, holder := range s.deps.CustomerPersonalData {
		section, err := holder.Data.ExportCustomerData(ctx, req.Id)
		if err != nil {
			return nil, fmt.Errorf("customers: export %s's data about customer %d: %w", holder.Module, req.Id, err)
		}
		if section != nil {
			sections[holder.Module] = section
		}
	}

	rate, err := floatPtrFromNumeric(row.DefaultBillRate)
	if err != nil {
		return nil, err
	}
	var identity *gen.LegalIdentityResponse
	if id := identityFromRow(row.LegalCountry, row.LegalID, row.LegalName, row.LegalSource, row.LegalType); id != nil {
		answer := legalIdentityResponse(*id)
		identity = &answer
	}
	exportedAddresses := make([]gen.CustomerAddress, 0, len(addresses))
	for _, a := range addresses {
		exportedAddresses = append(exportedAddresses, addressResponse(a))
	}
	// Grouped the way GetCustomersByIdContacts groups them: the rows arrive in
	// the fixed role order already.
	byContact := make(map[int32][]contactRole, len(contacts))
	for _, r := range roleRows {
		byContact[r.ContactID] = append(byContact[r.ContactID], contactRole{Role: r.Role, Primary: r.IsPrimary})
	}
	exportedContacts := make([]gen.CustomerContactResponse, 0, len(contacts))
	for _, r := range contacts {
		exportedContacts = append(exportedContacts, gen.CustomerContactResponse{
			Contact: gen.ContactResponse{
				Id: r.ID, FirstName: r.FirstName, LastName: r.LastName,
				MiddleName: r.MiddleName, Prefix: r.Prefix, Suffix: r.Suffix, Phone: r.ContactPhone, Email: r.ContactEmail,
			},
			Title: r.Title, Roles: genContactRoles(byContact[r.ID]),
			Phone: r.AssociationPhone, Email: r.AssociationEmail,
		})
	}
	exportedTimeline := make([]gen.TimelineResponse, 0, len(entries))
	for _, e := range entries {
		exportedTimeline = append(exportedTimeline, timelineResponse(e, followUps))
	}

	return personalDataDownload{
		fileName: fmt.Sprintf("customer-%d-personal-data.json", row.CustomerNumber),
		body: gen.CustomerPersonalData{
			ExportedAt: s.deps.Clock().UTC(),
			Customer: gen.CustomerPersonalDataCustomer{
				Id: row.ID, CustomerNumber: row.CustomerNumber, Name: row.Name, Type: row.Type, Status: row.Status,
				CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
				Identity:    identity,
				ContactInfo: gen.CustomerContactInfo{Email: row.Email, Phone: row.Phone, Website: row.Website},
				Addresses:   exportedAddresses,
				BillingProfile: gen.CustomerPersonalDataBillingProfile{
					InvoiceEmail: row.InvoiceEmail, ReminderEmail: row.ReminderEmail, PaymentTermsDays: row.PaymentTermsDays,
					Currency: row.Currency, Language: row.Language, InvoiceDelivery: row.InvoiceDelivery,
					ReminderDelivery: row.ReminderDelivery, PeppolId: row.PeppolID, Gln: row.Gln,
					BuyerReference: row.BuyerReference, DefaultBillRate: rate,
				},
				Owner:         dec.owner(customer.OwnerUserID),
				Group:         dec.group(row.ID),
				Tags:          dec.tagsFor(row.ID),
				MergedInto:    dec.merged(row.ID),
				Anonymisation: dec.anonymisationOf(row.ID),
			},
			Contacts: exportedContacts,
			Timeline: exportedTimeline,
			Modules:  sections,
		},
	}, nil
}
```

If the generated field for the billing profile's Peppol id is `PeppolID` rather than `PeppolId`, use the generated one (the existing `billingProfileResponse` shows which).

- [ ] **Step 6: Run everything, show it can fail, regenerate the client and the coverage, commit**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'
mise exec -- gofmt -l internal && mise exec -- go vet ./... && mise exec -- go build ./...
mise exec -- go test -count=1 ./internal/customers/... ./internal/db/... ./internal/openapi/... ./internal/module/...
cd /home/anders/projects/vantigo/vantigo && mise exec -- bun run gen:client && git status --short
cd apps/server && mise exec -- go run ./internal/openapi/cmd/contract coverage -corpus ../../openapi/testdata/exchanges -out ../../openapi/COVERAGE.md
```
Expected: PASS — the coverage gate included, `GET …/personal-data` being exercised by a 200; `TestModule_DeclaresItsPermissionCatalog` with sixteen keys.

Prove the tests can fail, restoring after each: make the export include a nil section (`sections[holder.Module] = section` unconditionally) — the `quiet` assertion goes red; drop the `Content-Disposition` header — the header assertion goes red; answer the business with the file (delete the `row.Type != "person"` case) — the refusal test goes red; filter the timeline to `state = 'active'` in `ListTimelineEntriesForExport` and delete the note first with `DELETE /customers/{id}/timeline/{entryId}` in a scratch copy of the first test — the entry is missing. Say what each printed.

```bash
cd /home/anders/projects/vantigo/vantigo
cat > /tmp/claude-1000/msg-gdpr-4.txt <<'EOF'
feat(customers): a private person's data can be handed to them in one file

GET /customers/{id}/personal-data, behind the new sensitive
customers:personal-data (+ view), answers everything held about a
person as a JSON attachment named by the customer number: the row with
its identity, contact info, addresses, billing profile, owner, group,
tags and markers; the contacts with their associations and roles; every
timeline entry with its payload and follow-up; and each other module's
section through contracts.CustomerPersonalData. A business is refused
personal_data_not_a_person. Migration 00030 adds anonymise_on and
anonymised_at with a partial index, and SafeCustomerResponse carries
anonymisation through the batched decoration.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
PATHS="apps/server/internal/db/migrations/00030_customers_personal_data.sql apps/server/internal/customers/sqlc.yaml \
 apps/server/internal/db/schema_test.go apps/server/internal/customers/queries/personal_data.sql \
 apps/server/internal/customers/store/personal_data.sql.go apps/server/internal/customers/store/models.go \
 openapi/customers.yaml apps/server/internal/openapi/specs/customers.yaml apps/server/internal/customers/gen/api.gen.go \
 apps/customers/frontend/src/api-schema.d.ts openapi/COVERAGE.md apps/server/internal/openapi/openapi_test.go \
 apps/server/internal/customers/personal_data.go apps/server/internal/customers/personal_data_test.go \
 apps/server/internal/customers/owner.go apps/server/internal/customers/customers.go \
 apps/server/internal/customers/module.go apps/server/internal/customers/module_test.go"
git add $PATHS && git commit -F /tmp/claude-1000/msg-gdpr-4.txt -- $PATHS
git show --stat HEAD && git status --short
```

Before running the commit, compare `git status --short` with `PATHS`: a generated file this list does not name (another `store/*.sql.go` whose model moved, a second `api-schema.d.ts`) goes into `PATHS` too, so the commit leaves nothing of this task behind.

---

### Task 5: Scheduling, and the read-only rule (D4)

`PUT`/`DELETE /customers/{id}/anonymisation` put a date on an archived private person and take it off; restoring or retyping a customer takes it off too. And the merged-away refusal becomes a read-only refusal with two codes, so an anonymised customer — made so directly in this task's tests, by the worker from Task 6 on — refuses every write through the same lock-time check.

**Files:**
- Create: `apps/server/internal/customers/anonymisation.go`, `apps/server/internal/customers/anonymisation_test.go`
- Modify: `openapi/customers.yaml` (+ generated `internal/openapi/specs/customers.yaml`, `internal/customers/gen/api.gen.go`, `apps/customers/frontend/src/api-schema.d.ts`), `openapi/COVERAGE.md`, `apps/server/internal/openapi/openapi_test.go`, `apps/server/internal/customers/queries/addresses.sql`, `queries/merge.sql`, `queries/personal_data.sql` (+ generated `store/addresses.sql.go`, `store/merge.sql.go`, `store/personal_data.sql.go`), `merge.go`, `import.go`, `timeline_events.go`, `customers.go`, `customer_type.go`, and — by the rename in Step 5 — `addresses.go`, `billing_profile.go`, `contact_info.go`, `contacts.go`, `follow_ups.go`, `group_membership.go`, `legal_identity.go`, `owner.go`, `peppol_lookup.go`, `peppol_recheck_worker.go`, `registry.go`, `registry_feed_worker.go`, `tags.go`, `timeline.go`
- Read first (do not change): `merge.go:55-150` (the refusal this generalises), `customers.go:749-825` (`writeCustomerCore`), `customer_type.go:85-125`, `import.go:655-675`, `merge_test.go:768-858` (`TestAMergedAwayCustomerRefusesEveryWrite`, the list of writes this task's read-only test mirrors)

**Interfaces:**
- Consumes: `CustomerForPersonalData`, `personalDataNotAPerson`, the decoration's `anonymisation` (Task 4).
- Produces Go: `(*server).PutCustomersByIdAnonymisation`, `(*server).DeleteCustomersByIdAnonymisation`, `(*server).customerAfterWrite(ctx, id) (gen.SafeCustomerResponse, error)`, `cancelAnonymisationSchedule(ctx, txq, id, locked, now, act) error`, `recordAnonymisationScheduled`, `recordAnonymisationCancelled`; the refusal renamed `customerReadOnlyError` with `isReadOnlyCustomer`, `readOnlyProblem`, `isAnonymisedRefusal`, `customerAnonymised(at time.Time)`, `refuseReadOnlyCustomer`; `store.LockCustomerRow` gains `Status`, `AnonymiseOn pgtype.Date`, `AnonymisedAt *time.Time`; `store.CustomerForMergeRow` gains `AnonymisedAt`.
- Wire: `PUT /api/v1/customers/{id}/anonymisation` `{anonymiseOn}` → 200 `SafeCustomerResponse`, 400 on `anonymiseOn` ("AnonymiseOn must be an ISO date (yyyy-MM-dd)", "An anonymisation date cannot be in the past"), 404, 409 `personal_data_not_a_person` / `personal_data_customer_active` / `customer_merged` / `customer_anonymised`; `DELETE` → 200 `SafeCustomerResponse`, 404, 409 `customer_anonymised`. Events `customer.anonymisation_scheduled` (`{customerId, anonymiseOn, previousAnonymiseOn?}`, summary "Anonymisation scheduled for 2027-01-31" or "Anonymisation moved from 2027-01-31 to 2027-03-01") and `customer.anonymisation_cancelled` (`{customerId, anonymiseOn}`, summary "Anonymisation cancelled; it was scheduled for 2027-01-31"), attributed to the caller. Every customer-scoped write of an anonymised customer answers 409 `customer_anonymised`, title "Customer was anonymised", detail "This customer's personal data was anonymised on 2027-01-31. What is left is kept for bookkeeping and takes no more changes."; a CSV row naming its number is refused on `customerNumber`, "Customer 1005 was anonymised and takes no more changes"; merging it into another customer is 409 `customer_anonymised`.

- [ ] **Step 1: The contract**

In `openapi/customers.yaml`, directly after the `CustomerAnonymisation` schema (Task 4), add:

```yaml
        CustomerAnonymisationRequest:
            description: "PUT /customers/{id}/anonymisation's body (customers GDPR design D4): anonymiseOn is the UTC calendar day to anonymise the customer on, yyyy-MM-dd, today or later — a plain string the module validates in its own words. There is no default: the caller chooses."
            properties:
                anonymiseOn:
                    type: string
            required:
                - anonymiseOn
            type: object
```

Under `paths`, directly before `    /api/v1/customers/{id}/billing-profile:`, add:

```yaml
    /api/v1/customers/{id}/anonymisation:
        delete:
            description: "Calls off a private person's scheduled anonymisation (customers GDPR design D4) and records customer.anonymisation_cancelled. With nothing scheduled it writes nothing and answers the customer as it is. It is the one write a merged-away customer takes: a schedule made before its merge would otherwise be irrevocable. 404 when the customer does not exist; 409 customer_anonymised once the anonymisation has run — there is nothing left to call off."
            operationId: deleteCustomersByIdAnonymisation
            parameters:
                - in: path
                  name: id
                  required: true
                  schema:
                    format: int32
                    type: integer
            responses:
                "200":
                    content:
                        application/json:
                            schema:
                                $ref: '#/components/schemas/SafeCustomerResponse'
                    description: OK
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
                "404":
                    description: Not Found
                "409":
                    content:
                        application/problem+json:
                            schema:
                                $ref: '#/components/schemas/CustomerConflictProblem'
                    description: Conflict — customer_anonymised
            summary: Cancel a private person's anonymisation
            tags:
                - Customers
            x-vantigo-access: permission:customers:personal-data+customers:view
        put:
            description: "Schedules a private person's anonymisation on anonymiseOn (customers GDPR design D4). On that UTC day the anonymisation worker takes the person out of the customer — the name, legal identity, contact info, the billing profile's identifiers, addresses, contacts no other customer has, the content of every timeline entry, and what other modules hold — and keeps the customer number, the dates and the shape of its history for bookkeeping. There is no default date: Norwegian bookkeeping rules keep accounting material for years after the fiscal year, and the caller, who knows what was invoiced, chooses. anonymiseOn is today or later; the date already scheduled writes nothing, another one moves it. Records customer.anonymisation_scheduled. Restoring the customer, or changing its type, calls the schedule off. 400 for a date that is malformed or past; 404 when the customer does not exist; 409 personal_data_not_a_person (a business), personal_data_customer_active (not archived: archive it first), customer_merged or customer_anonymised (it takes no more changes)."
            operationId: putCustomersByIdAnonymisation
            parameters:
                - in: path
                  name: id
                  required: true
                  schema:
                    format: int32
                    type: integer
            requestBody:
                content:
                    application/json:
                        schema:
                            $ref: '#/components/schemas/CustomerAnonymisationRequest'
                required: true
            responses:
                "200":
                    content:
                        application/json:
                            schema:
                                $ref: '#/components/schemas/SafeCustomerResponse'
                    description: OK
                "400":
                    content:
                        application/problem+json:
                            schema:
                                $ref: common.yaml#/components/schemas/HttpValidationProblemDetails
                    description: Bad Request
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
                "404":
                    description: Not Found
                "409":
                    content:
                        application/problem+json:
                            schema:
                                $ref: '#/components/schemas/CustomerConflictProblem'
                    description: Conflict
            summary: Schedule a private person's anonymisation
            tags:
                - Customers
            x-vantigo-access: permission:customers:personal-data+customers:view
```

In `apps/server/internal/openapi/openapi_test.go`'s `KnownServeMuxConflicts`, add each of these in its sorted place (every `…{id}/anonymisation` pair sorts first among its method's pairs with the same left side):

```go
	"DELETE /api/v1/customers/contacts/{id} ⟷ DELETE /api/v1/customers/{id}/anonymisation",
	"DELETE /api/v1/customers/groups/{groupId} ⟷ DELETE /api/v1/customers/{id}/anonymisation",
	"DELETE /api/v1/customers/tags/{tagId} ⟷ DELETE /api/v1/customers/{id}/anonymisation",
	"PUT /api/v1/customers/contacts/{id} ⟷ PUT /api/v1/customers/{id}/anonymisation",
	"PUT /api/v1/customers/groups/{groupId} ⟷ PUT /api/v1/customers/{id}/anonymisation",
	"PUT /api/v1/customers/tags/{tagId} ⟷ PUT /api/v1/customers/{id}/anonymisation",
```

If `TestServeMuxConflictsArePinned` prints a different set, pin exactly what it prints.

- [ ] **Step 2: The queries**

In `apps/server/internal/customers/queries/addresses.sql`, extend `LockCustomer`'s comment with

```sql
--
-- And the status and the anonymisation (customers GDPR design D4): a private
-- person's schedule is decided under this lock — only an archived person may
-- be scheduled, and a restore or a change of type that takes it out of that
-- calls the schedule off in the same transaction — and an anonymised customer
-- is read-only, which every customer-scoped write learns here, as it learns a
-- merge (lockWritableCustomer, merge.go).
```

and its select list to

```sql
SELECT id, type, status, legal_country, legal_id, legal_name, legal_source, legal_type, merged_into_customer_id,
       anonymise_on, anonymised_at
FROM customers.customers WHERE id = @id FOR NO KEY UPDATE;
```

In `queries/merge.sql`, `CustomerForMerge`: add `anonymised_at` after `merged_into_customer_id` in the select list, and to its comment `…the type, status, marker and revision the ladder checks` add `, whether the absorbed customer was anonymised (customers GDPR design D4 — an anonymised customer is read-only, and a merge would write it)`.

Append to `queries/personal_data.sql`:

```sql
-- name: SetCustomerAnonymiseOn :exec
-- SetCustomerAnonymiseOn is the schedule's write (design D4): PUT sets the
-- day, DELETE clears it (NULL). A write to the row like any other, so the
-- revision advances; both callers hold the lock and have decided the write is
-- not a no-op.
UPDATE customers.customers
SET anonymise_on = sqlc.narg(anonymise_on)::date, updated_at = @now::timestamptz, revision = revision + 1
WHERE id = @id;

-- name: DropCustomerAnonymiseOn :exec
-- DropCustomerAnonymiseOn calls a schedule off as part of another write to the
-- row — a restore, a change of type (cancelAnonymisationSchedule,
-- anonymisation.go) — whose own UPDATE has already advanced the revision in the
-- same transaction, so this one does not advance it a second time.
UPDATE customers.customers SET anonymise_on = NULL WHERE id = @id;

-- name: CustomerAnonymisedAt :one
-- CustomerAnonymisedAt is the read-only refusal read on the pool
-- (refuseReadOnlyCustomer, merge.go): when a customer was anonymised, NULL when
-- it was not. pgx.ErrNoRows is a customer that does not exist, whose 404 is the
-- caller's to give.
SELECT anonymised_at FROM customers.customers WHERE id = @id;
```

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server && mise exec -- go generate ./... && mise exec -- go generate ./... && git status --short
```

- [ ] **Step 3: Write the failing tests**

Create `apps/server/internal/customers/anonymisation_test.go`:

```go
package customers_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is PUT and DELETE /customers/{id}/anonymisation (customers GDPR
// design D4) — the schedule, its refusals, its events, what calls it off — and
// the read-only rule an anonymised customer keeps. anonymisation_worker_test.go
// carries the anonymisation itself.

func putAnonymisation(t *testing.T, c *modtest.Client, id int32, anonymiseOn string) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/anonymisation", id), map[string]any{"anonymiseOn": anonymiseOn})
}

func deleteAnonymisation(t *testing.T, c *modtest.Client, id int32) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d/anonymisation", id), nil)
}

// anonymisedCustomerJSON is customerJSON with the anonymisation (Task 4's
// field), for the tests that read it.
type anonymisedCustomerJSON struct {
	customerJSON
	Anonymisation *anonymisationJSON `json:"anonymisation"`
}

func customerAnswer(t *testing.T, r *modtest.Response) anonymisedCustomerJSON {
	t.Helper()
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var c anonymisedCustomerJSON
	r.JSON(&c)
	return c
}

// archivedPerson is a private person, archived — the one customer design D4
// lets anybody schedule.
func archivedPerson(t *testing.T, c *modtest.Client, name string) createdCustomerJSON {
	t.Helper()
	person := createCustomerOfType(t, c, name, "person")
	if r := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d", person.Id), nil); r.Status != http.StatusNoContent {
		t.Fatalf("archive %s: status %d body %s", name, r.Status, r.Body)
	}
	return person
}

// markAnonymised makes a customer anonymised directly: the read-only rule is
// this file's subject, and the worker that really does it is the next file's.
func markAnonymised(t *testing.T, h *modtest.Harness, id int32) {
	t.Helper()
	h.Exec(t, `UPDATE customers.customers SET status = 'archived', anonymise_on = $2, anonymised_at = $3 WHERE id = $1`,
		id, h.Now().UTC().Format(time.DateOnly), h.Now())
}

type scheduledPayloadJSON struct {
	CustomerId          int32   `json:"customerId"`
	AnonymiseOn         string  `json:"anonymiseOn"`
	PreviousAnonymiseOn *string `json:"previousAnonymiseOn"`
}

// TestPutCustomersByIdAnonymisation_SchedulesAnArchivedPerson is design D4's
// happy path: the day goes on the customer, on its response and on its
// timeline, attributed to the caller, and the revision advances; the same day
// again writes nothing; another day moves it, and the event says from where.
func TestPutCustomersByIdAnonymisation_SchedulesAnArchivedPerson(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	person := archivedPerson(t, c, "Kari Nordmann")
	before := fetchCustomerJSON(t, c, person.Id)
	scheduler, schedulerID := h.SignInUser(t, "customers:view", "customers:personal-data", "customers:timeline-view")
	setDisplayName(t, h, schedulerID, "Siri Saksbehandler")

	scheduled := customerAnswer(t, putAnonymisation(t, scheduler, person.Id, day(h, 30)))
	if scheduled.Anonymisation == nil || scheduled.Anonymisation.AnonymiseOn != day(h, 30) || scheduled.Anonymisation.AnonymisedAt != nil {
		t.Errorf("anonymisation = %+v, want %s and not yet run", scheduled.Anonymisation, day(h, 30))
	}
	if scheduled.Revision != before.Revision+1 {
		t.Errorf("revision = %d (was %d), want one on", scheduled.Revision, before.Revision)
	}
	var got anonymisedCustomerJSON
	c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d", person.Id), nil).JSON(&got)
	if got.Anonymisation == nil || got.Anonymisation.AnonymiseOn != day(h, 30) {
		t.Errorf("GET anonymisation = %+v, want the schedule", got.Anonymisation)
	}
	events := entriesOfType(timelineOf(t, c, person.Id), "customer.anonymisation_scheduled")
	if len(events) != 1 || str(events[0].Summary) != "Anonymisation scheduled for "+day(h, 30) || str(events[0].ActorDisplay) != "Siri Saksbehandler" {
		t.Fatalf("scheduled events = %+v, want one, by the caller", events)
	}
	var payload scheduledPayloadJSON
	if err := json.Unmarshal(events[0].Payload, &payload); err != nil || payload.CustomerId != person.Id ||
		payload.AnonymiseOn != day(h, 30) || payload.PreviousAnonymiseOn != nil {
		t.Errorf("payload = %s, want {customerId, anonymiseOn} alone", events[0].Payload)
	}

	if again := customerAnswer(t, putAnonymisation(t, scheduler, person.Id, day(h, 30))); again.Revision != scheduled.Revision {
		t.Errorf("the same day again: revision %d, want %d — a no-op writes nothing", again.Revision, scheduled.Revision)
	}
	moved := customerAnswer(t, putAnonymisation(t, scheduler, person.Id, day(h, 60)))
	if moved.Anonymisation == nil || moved.Anonymisation.AnonymiseOn != day(h, 60) {
		t.Errorf("moved = %+v, want %s", moved.Anonymisation, day(h, 60))
	}
	events = entriesOfType(timelineOf(t, c, person.Id), "customer.anonymisation_scheduled")
	if len(events) != 2 || str(events[0].Summary) != fmt.Sprintf("Anonymisation moved from %s to %s", day(h, 30), day(h, 60)) {
		t.Errorf("scheduled events = %+v, want the move on top", events)
	}
	// Today is a day like any other: the worker takes it on its next cycle.
	if today := customerAnswer(t, putAnonymisation(t, scheduler, person.Id, day(h, 0))); today.Anonymisation.AnonymiseOn != day(h, 0) {
		t.Errorf("today = %+v", today.Anonymisation)
	}
}

// TestPutCustomersByIdAnonymisation_RefusesInOrder: the date is validated
// first, then the customer is looked up, then the read-only rule, a business,
// a customer that is not archived. None of them writes anything.
func TestPutCustomersByIdAnonymisation_RefusesInOrder(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	scheduler := personalDataClient(t, h)
	person := archivedPerson(t, c, "Kari Nordmann")

	for raw, want := range map[string]string{
		"":           "AnonymiseOn must be an ISO date (yyyy-MM-dd)",
		"31.01.2027": "AnonymiseOn must be an ISO date (yyyy-MM-dd)",
		day(h, -1):   "An anonymisation date cannot be in the past",
	} {
		r := putAnonymisation(t, scheduler, person.Id, raw)
		var problem validationProblemJSON
		if r.JSON(&problem); r.Status != http.StatusBadRequest || len(problem.Errors["anonymiseOn"]) != 1 || problem.Errors["anonymiseOn"][0] != want {
			t.Errorf("anonymiseOn %q: status %d errors %v, want 400 %q", raw, r.Status, problem.Errors, want)
		}
	}
	if r := putAnonymisation(t, scheduler, 999999, day(h, 1)); r.Status != http.StatusNotFound {
		t.Errorf("unknown customer: status %d, want 404", r.Status)
	}

	business := createCustomer(t, c, "Acme AS")
	c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d", business.Id), nil)
	refusedWith(t, putAnonymisation(t, scheduler, business.Id, day(h, 1)), "personal_data_not_a_person")

	active := createCustomerOfType(t, c, "Ola Nordmann", "person")
	problem := refusedWith(t, putAnonymisation(t, scheduler, active.Id, day(h, 1)), "personal_data_customer_active")
	if problem.Title != "Customer is not archived" || !strings.Contains(problem.Detail, "Archive it before scheduling") {
		t.Errorf("problem = %+v", problem)
	}

	survivor := createCustomerOfType(t, c, "Kari N.", "person")
	mergeOK(t, mergeClient(t, h), survivor.Id, person.Id)
	refusedWith(t, putAnonymisation(t, scheduler, person.Id, day(h, 1)), "customer_merged")

	if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE anonymise_on IS NOT NULL`); n != 0 {
		t.Errorf("%d customers were scheduled by a refused request", n)
	}
	for _, keys := range [][]string{{"customers:view", "customers:update", "customers:delete"}, {"customers:personal-data"}} {
		if r := putAnonymisation(t, h.SignIn(t, keys...), survivor.Id, day(h, 1)); r.Status != http.StatusForbidden {
			t.Errorf("%v: status %d, want 403", keys, r.Status)
		}
	}
}

// TestDeleteCustomersByIdAnonymisation_CallsItOff: the schedule goes, the
// event says what it was, and a second cancel — nothing scheduled — writes
// nothing.
func TestDeleteCustomersByIdAnonymisation_CallsItOff(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	scheduler := personalDataClient(t, h)
	person := archivedPerson(t, c, "Kari Nordmann")
	scheduled := customerAnswer(t, putAnonymisation(t, scheduler, person.Id, day(h, 30)))

	cancelled := customerAnswer(t, deleteAnonymisation(t, scheduler, person.Id))
	if cancelled.Anonymisation != nil || cancelled.Revision != scheduled.Revision+1 {
		t.Errorf("cancelled = %+v at revision %d, want no anonymisation, one on from %d", cancelled.Anonymisation, cancelled.Revision, scheduled.Revision)
	}
	events := entriesOfType(timelineOf(t, c, person.Id), "customer.anonymisation_cancelled")
	if len(events) != 1 || str(events[0].Summary) != "Anonymisation cancelled; it was scheduled for "+day(h, 30) {
		t.Fatalf("cancelled events = %+v, want one naming the day", events)
	}
	if again := customerAnswer(t, deleteAnonymisation(t, scheduler, person.Id)); again.Revision != cancelled.Revision {
		t.Errorf("a second cancel: revision %d, want %d", again.Revision, cancelled.Revision)
	}
	if n := len(entriesOfType(timelineOf(t, c, person.Id), "customer.anonymisation_cancelled")); n != 1 {
		t.Errorf("cancelled events = %d, want still 1", n)
	}
	if r := deleteAnonymisation(t, scheduler, 999999); r.Status != http.StatusNotFound {
		t.Errorf("unknown customer: status %d, want 404", r.Status)
	}
}

// TestLeavingTheArchiveCallsTheScheduleOff is this plan's reading of design
// D4: an archived private person is what may be scheduled, so a restore — the
// customer PUT or a CSV row — or a change of type away from person takes the
// date off in the same transaction, recorded, rather than leaving it to fire
// the day the customer is archived again.
func TestLeavingTheArchiveCallsTheScheduleOff(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	scheduler := personalDataClient(t, h)

	restored := archivedPerson(t, c, "Kari Nordmann")
	customerAnswer(t, putAnonymisation(t, scheduler, restored.Id, day(h, 30)))
	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d", restored.Id), map[string]any{"name": "Kari Nordmann", "status": "active"})
	if got := customerAnswer(t, r); got.Status != "active" || got.Anonymisation != nil {
		t.Errorf("restored = %s with %+v, want active and nothing scheduled", got.Status, got.Anonymisation)
	}
	if n := len(entriesOfType(timelineOf(t, c, restored.Id), "customer.anonymisation_cancelled")); n != 1 {
		t.Errorf("a restore recorded %d cancellations, want 1", n)
	}

	// The CSV import restores through the same writeCustomerCore.
	imported := archivedPerson(t, c, "Per Nordmann")
	customerAnswer(t, putAnonymisation(t, scheduler, imported.Id, day(h, 30)))
	if result := importResultOf(t, postImport(t, c, "?dryRun=false", csvFileOf(
		[]string{"customerNumber", "name", "status"},
		[]string{strconv.FormatInt(imported.CustomerNumber, 10), "Per Nordmann", "active"},
	))); result.Updated != 1 || result.Failed != 0 {
		t.Fatalf("import = %+v, want the row updated", result)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE id = $1 AND status = 'active' AND anonymise_on IS NULL`, imported.Id); n != 1 {
		t.Error("a CSV restore kept the anonymisation date")
	}
	if n := len(entriesOfType(timelineOf(t, c, imported.Id), "customer.anonymisation_cancelled")); n != 1 {
		t.Errorf("a CSV restore recorded %d cancellations, want 1", n)
	}

	retyped := archivedPerson(t, c, "Ola Nordmann")
	customerAnswer(t, putAnonymisation(t, scheduler, retyped.Id, day(h, 30)))
	if r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/type", retyped.Id), map[string]any{"type": "business"}); r.Status != http.StatusOK {
		t.Fatalf("type change: status %d body %s", r.Status, r.Body)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE id = $1 AND anonymise_on IS NULL`, retyped.Id); n != 1 {
		t.Error("a business kept its anonymisation date")
	}
	if n := len(entriesOfType(timelineOf(t, c, retyped.Id), "customer.anonymisation_cancelled")); n != 1 {
		t.Errorf("a type change recorded %d cancellations, want 1", n)
	}
}

// A schedule made before the customer was merged away can still be called off:
// cancelling is the one write a merged-away customer takes.
func TestAScheduleMadeBeforeAMergeCanStillBeCalledOff(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	scheduler := personalDataClient(t, h)
	absorbed := archivedPerson(t, c, "Kari Nordmann")
	customerAnswer(t, putAnonymisation(t, scheduler, absorbed.Id, day(h, 30)))
	survivor := createCustomerOfType(t, c, "Kari N.", "person")
	mergeOK(t, mergeClient(t, h), survivor.Id, absorbed.Id)

	if got := customerAnswer(t, deleteAnonymisation(t, scheduler, absorbed.Id)); got.Anonymisation != nil || got.MergedInto == nil {
		t.Errorf("cancelled = %+v merged into %+v, want nothing scheduled and still merged away", got.Anonymisation, got.MergedInto)
	}
}

// TestAnAnonymisedCustomerIsReadOnly is design D4's read-only rule, through the
// same lock-time check a merged-away customer's refusal comes from: every write
// answers customer_anonymised — its schedule's included — a CSV row naming it
// is refused, it cannot be absorbed by a merge, archiving it again is the
// no-op it is for any archived customer, and its export still answers.
func TestAnAnonymisedCustomerIsReadOnly(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := mergeClient(t, h)
	person := archivedPerson(t, c, "Anonymised person")
	markAnonymised(t, h, person.Id)
	id := person.Id

	writes := map[string]*modtest.Response{
		"restore":          c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d", id), map[string]any{"name": "Kari Nordmann", "status": "active"}),
		"contact info":     putContactInfo(t, c, id, map[string]any{"email": "kari@example.test"}),
		"address":          c.Do(http.MethodPost, fmt.Sprintf("/api/v1/customers/%d/addresses", id), fullAddressBody("postal", nil)),
		"tags":             putCustomerTags(t, c, id, []string{createTag(t, c, map[string]any{"name": "Nabo"}).Id}),
		"timeline entry":   c.Do(http.MethodPost, fmt.Sprintf("/api/v1/customers/%d/timeline", id), map[string]any{"eventType": "note", "occurredOn": day(h, 0), "note": "Ringte"}),
		"schedule":         putAnonymisation(t, personalDataClient(t, h), id, day(h, 1)),
		"cancel":           deleteAnonymisation(t, personalDataClient(t, h), id),
		"merged elsewhere": postMerge(t, c, createCustomerOfType(t, c, "Kari N.", "person").Id, map[string]any{"sourceId": id}),
	}
	for name, r := range writes {
		problem := refusedWith(t, r, "customer_anonymised")
		if problem.Title != "Customer was anonymised" || !strings.Contains(problem.Detail, "anonymised on "+day(h, 0)) {
			t.Errorf("%s: problem = %+v", name, problem)
		}
	}

	result := importResultOf(t, postImport(t, c, "?dryRun=false", csvFileOf(
		[]string{"customerNumber", "name"},
		[]string{strconv.FormatInt(person.CustomerNumber, 10), "Kari Nordmann"},
	)))
	if len(result.Errors) != 1 || result.Errors[0].Column != "customerNumber" ||
		result.Errors[0].Message != fmt.Sprintf("Customer %d was anonymised and takes no more changes", person.CustomerNumber) {
		t.Errorf("import = %+v, want the row refused on customerNumber", result)
	}
	if r := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d", id), nil); r.Status != http.StatusNoContent {
		t.Errorf("archive again: status %d, want the 204 no-op", r.Status)
	}
	var file personalDataJSON
	if r := getPersonalData(t, personalDataClient(t, h), id); r.Status != http.StatusOK {
		t.Errorf("export: status %d body %s, want 200", r.Status, r.Body)
	} else if r.JSON(&file); file.Customer.Anonymisation == nil || file.Customer.Anonymisation.AnonymisedAt == nil {
		t.Errorf("export anonymisation = %+v, want it done", file.Customer.Anonymisation)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE id = $1 AND status = 'archived' AND email IS NULL`, id); n != 1 {
		t.Error("a refused write changed the anonymised customer")
	}
}
```

Add `"strconv"` to the imports (the CSV row). `mergeClient` holds every key the writes need (`customers:timeline-manage` for the entry, `customers:merge` for the merge, create and update for the import); the schedule and the cancel go through `personalDataClient`.

Run them: `mise exec -- go test -count=1 -run 'Anonymis|LeavingTheArchive|AScheduleMadeBeforeAMerge' ./internal/customers/` — FAIL to compile: the server lacks `PutCustomersByIdAnonymisation` and `DeleteCustomersByIdAnonymisation`.

- [ ] **Step 4: The two events**

Append to `apps/server/internal/customers/timeline_events.go`:

```go
// recordAnonymisationScheduled is PUT /customers/{id}/anonymisation's event
// (customers GDPR design D4), attributed to whoever chose the date — the worker
// that later acts on it is only the system, so this entry is where the
// decision's author is on record. previous is the day it replaces, nil for a
// first schedule. Dates only: the payload names nothing the anonymisation will
// have to take out again.
func recordAnonymisationScheduled(ctx context.Context, q *store.Queries, now time.Time, customerID int32, on time.Time, previous *time.Time, actorKind, actorDisplay string, actorUserID *uuid.UUID) error {
	day := on.Format(time.DateOnly)
	payload := map[string]any{"customerId": customerID, "anonymiseOn": day}
	summary := "Anonymisation scheduled for " + day
	if previous != nil {
		was := previous.Format(time.DateOnly)
		payload["previousAnonymiseOn"] = was
		summary = fmt.Sprintf("Anonymisation moved from %s to %s", was, day)
	}
	return recordGeneratedEvent(ctx, q, customerID, now, "customer.anonymisation_scheduled", summary, payload, 1, actorKind, actorDisplay, actorUserID)
}

// recordAnonymisationCancelled is the schedule called off (design D4): by
// DELETE /customers/{id}/anonymisation, or by a restore or a change of type
// that took the customer out of what may be anonymised
// (cancelAnonymisationSchedule). on is the day that was scheduled.
func recordAnonymisationCancelled(ctx context.Context, q *store.Queries, now time.Time, customerID int32, on time.Time, actorKind, actorDisplay string, actorUserID *uuid.UUID) error {
	day := on.Format(time.DateOnly)
	return recordGeneratedEvent(ctx, q, customerID, now, "customer.anonymisation_cancelled",
		"Anonymisation cancelled; it was scheduled for "+day, map[string]any{"customerId": customerID, "anonymiseOn": day}, 1,
		actorKind, actorDisplay, actorUserID)
}
```

- [ ] **Step 5: The read-only refusal**

In `apps/server/internal/customers/merge.go`, replace everything from the comment `// customerMergedError is a write refused because its customer was merged away` down to and including the closing brace of `refuseMergedAway` with:

```go
// customerReadOnlyError is a write refused because its customer takes no more
// changes. Two reasons: merged away (customers merge design D2) — its records
// are the survivor's now, and a write here would quietly start a second
// history nobody sees — or anonymised (customers GDPR design D4) — what is left
// is kept for bookkeeping, and a write would put a person back into it. The 409
// carries code customer_merged and names the survivor, or customer_anonymised
// and names the day. It is an error, not a return value, so that it can abort
// whichever transaction found it and travel out through the helpers in between;
// isReadOnlyCustomer and readOnlyProblem read it back.
type customerReadOnlyError struct {
	problem gen.CustomerConflictProblem
	// into is the survivor as a person reads it, "#1002 Acme AS"; empty for an
	// anonymised customer.
	into string
	// anonymised tells the two apart for the one caller whose words differ,
	// the CSV import's row error.
	anonymised bool
}

func (e *customerReadOnlyError) Error() string {
	if e.anonymised {
		return "customers: customer was anonymised"
	}
	return "customers: customer was merged away"
}

// isReadOnlyCustomer reports whether err is either refusal — customer_merged
// or customer_anonymised — and readOnlyProblem is its 409 body: the pair a
// handler's switch uses, case and answer. Every handler that refused a
// merged-away customer refuses an anonymised one the same way, with no change
// of its own.
func isReadOnlyCustomer(err error) bool {
	var readOnly *customerReadOnlyError
	return errors.As(err, &readOnly)
}

func readOnlyProblem(err error) gen.CustomerConflictProblem {
	var readOnly *customerReadOnlyError
	if errors.As(err, &readOnly) {
		return readOnly.problem
	}
	return gen.CustomerConflictProblem{}
}

// isAnonymisedRefusal reports whether err is the customer_anonymised refusal.
func isAnonymisedRefusal(err error) bool {
	var readOnly *customerReadOnlyError
	return errors.As(err, &readOnly) && readOnly.anonymised
}

// mergedAwayInto is the survivor err's refusal names, "#1002 Acme AS" — empty
// for an anonymised customer's.
func mergedAwayInto(err error) string {
	var readOnly *customerReadOnlyError
	if errors.As(err, &readOnly) {
		return readOnly.into
	}
	return ""
}

// customerMerged is the customer_merged refusal, naming the customer this one
// was merged into.
func customerMerged(intoNumber int64, intoName string) *customerReadOnlyError {
	into := customerLabel(intoNumber, intoName)
	return &customerReadOnlyError{into: into, problem: *mergeConflict("Customer was merged", "customer_merged", fmt.Sprintf(
		"This customer was merged into %s. Its records are there now, and it takes no more changes.", into))}
}

// customerAnonymised is the customer_anonymised refusal (customers GDPR design
// D4), naming the day it was anonymised and nothing else — there is nothing
// else left to name.
func customerAnonymised(at time.Time) *customerReadOnlyError {
	return &customerReadOnlyError{anonymised: true, problem: *mergeConflict("Customer was anonymised", "customer_anonymised", fmt.Sprintf(
		"This customer's personal data was anonymised on %s. What is left is kept for bookkeeping and takes no more changes.",
		at.UTC().Format(time.DateOnly)))}
}

// lockWritableCustomer is every customer-scoped write's LockCustomer: the
// customer row's FOR NO KEY UPDATE lock, then the refusal when the row it
// locked was anonymised or merged away. A merge and an anonymisation both hold
// this same lock until they commit, so a write that queued behind one reads its
// outcome here and refuses rather than writing to a customer that just stopped
// taking writes. Anonymised is asked first: a customer both merged away and
// anonymised has nothing left the survivor's name would help with.
// pgx.ErrNoRows still means the customer does not exist.
func lockWritableCustomer(ctx context.Context, txq *store.Queries, id int32) (store.LockCustomerRow, error) {
	locked, err := txq.LockCustomer(ctx, id)
	if err != nil {
		return locked, err
	}
	if locked.AnonymisedAt != nil {
		return locked, customerAnonymised(*locked.AnonymisedAt)
	}
	if locked.MergedIntoCustomerID == nil {
		return locked, nil
	}
	// Read, not locked, as the merge ladder's own merge_already_merged reads
	// it: the refusal only names it, and the marker's foreign key guarantees
	// the row.
	into, err := txq.GetCustomer(ctx, *locked.MergedIntoCustomerID)
	if err != nil {
		return locked, fmt.Errorf("read the customer %d was merged into: %w", id, err)
	}
	return locked, customerMerged(into.CustomerNumber, into.Name)
}

// refuseReadOnlyCustomer is the same refusal read on the pool, before a write
// gets as far as its lock, for the writes that would otherwise answer something
// else first or do something costly before it: an entry, an association or a
// follow-up of a merged-away customer moved with the merge, so looking it up
// answers 404; and a Peppol lookup or a registry refresh would ask the network
// first. It answers the error lockWritableCustomer would, or nil — for a
// customer that takes writes and equally for one that does not exist, whose 404
// is the caller's to give. Only the lock decides a race; this is the answer
// when there is none.
func refuseReadOnlyCustomer(ctx context.Context, q *store.Queries, id int32) error {
	at, err := q.CustomerAnonymisedAt(ctx, id)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil
	case err != nil:
		return fmt.Errorf("customers: read the anonymisation of %d: %w", id, err)
	case at != nil:
		return customerAnonymised(*at)
	}
	rows, err := q.MergedIntoForCustomers(ctx, []int32{id})
	if err != nil {
		return fmt.Errorf("customers: read merge marker of %d: %w", id, err)
	}
	if len(rows) == 0 {
		return nil
	}
	return customerMerged(rows[0].CustomerNumber, rows[0].Name)
}
```

In `mergeRefusal`, add directly after the `case survivor.Status == "archived":` case:

```go
	case absorbed.AnonymisedAt != nil:
		// An anonymised customer is read-only (customers GDPR design D4), and a
		// merge writes it; there is nothing of a person left in it to fold in.
		return &customerAnonymised(*absorbed.AnonymisedAt).problem, nil
```

and in its doc comment, after `an archived survivor,` add ` an absorbed customer that was anonymised,`.

Rename the callers in every other file of the package — the three names are the package's own, so a word-bounded rename is exact:

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server/internal/customers
ls *.go | grep -v '^merge\.go$' | xargs sed -i \
  -e 's/\bcustomerMergedError\b/customerReadOnlyError/g' \
  -e 's/\bisMergedAway\b/isReadOnlyCustomer/g' \
  -e 's/\bmergedAwayProblem\b/readOnlyProblem/g' \
  -e 's/\brefuseMergedAway\b/refuseReadOnlyCustomer/g'
grep -n 'isMergedAway\|mergedAwayProblem\|refuseMergedAway\|customerMergedError' *.go   # must print nothing
```

In `import.go`'s `importUpdate`, replace the `if _, err := lockWritableCustomer(ctx, txq, id); isReadOnlyCustomer(err) { … }` block's body with:

```go
		// A customer merged away or anonymised takes no more changes (customers
		// merge design D2, GDPR design D4) — this row would otherwise restore it,
		// write into a history nobody reads, or put a person back into what was
		// kept for bookkeeping. The row is refused; a merged-away customer's
		// survivor is the one to import it under.
		if isAnonymisedRefusal(err) {
			return 0, refuseRow(p.Row, "customerNumber", fmt.Sprintf("Customer %d was anonymised and takes no more changes", *p.CustomerNumber))
		}
		return 0, refuseRow(p.Row, "customerNumber", fmt.Sprintf("Customer %d was merged into %s and takes no more changes; use that customer's number instead",
			*p.CustomerNumber, mergedAwayInto(err)))
```

- [ ] **Step 6: Scheduling, cancelling, and what calls a schedule off**

Create `apps/server/internal/customers/anonymisation.go`:

```go
package customers

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
	"github.com/vantigo-io/vantigo/server/internal/customers/store"
	"github.com/vantigo-io/vantigo/server/internal/db"
)

// This file is a private person's anonymisation (customers GDPR design D4):
// PUT and DELETE /customers/{id}/anonymisation, which put it on a day a person
// chose and take it off again, what else takes it off, and — from
// anonymiseCustomer down, run by the worker in anonymisation_worker.go — the
// anonymisation itself. There is no default day anywhere in it: Norwegian
// bookkeeping rules keep accounting material for years after the fiscal year,
// and the person scheduling knows what was invoiced; the module does not.

// errAnonymisationRefused aborts a scheduling transaction on a refusal read
// under the lock; the body travels beside it, as errMergeRefused's does.
var errAnonymisationRefused = errors.New("customers: anonymisation refused")

// personalDataCustomerActive is the refusal for a customer that is not
// archived (design D4): an ongoing relationship is not anonymised out from
// under itself, so the relationship is ended first, deliberately, by archiving.
func personalDataCustomerActive(number int64, name, status string) *gen.CustomerConflictProblem {
	return mergeConflict("Customer is not archived", "personal_data_customer_active", fmt.Sprintf(
		"%s is %s. Archive it before scheduling its anonymisation: an ongoing relationship is not anonymised out from under it.",
		customerLabel(number, name), status))
}

// scheduleRefusal is design D4's refusals after the read-only one, in order:
// a business, then a customer that is not archived. nil lets the schedule go
// ahead.
func scheduleRefusal(number int64, name, customerType, status string) *gen.CustomerConflictProblem {
	switch {
	case customerType != "person":
		return personalDataNotAPerson(number, name)
	case status != "archived":
		return personalDataCustomerActive(number, name, status)
	}
	return nil
}

// validateAnonymiseOn is the body's one field: a yyyy-MM-dd day, parsed the way
// every date of this module is (parseISODate), today in UTC or later. Today is
// allowed — the worker takes it on its next cycle — and a day in the past is
// not: it would read as "already due" to a person who meant something else.
func validateAnonymiseOn(raw string, now time.Time) (time.Time, string) {
	on, err := parseISODate(raw)
	if err != nil {
		return time.Time{}, "AnonymiseOn must be an ISO date (yyyy-MM-dd)"
	}
	if on.Before(civilDate(now)) {
		return time.Time{}, "An anonymisation date cannot be in the past"
	}
	return on, ""
}

// customerAfterWrite is the customer as GET /customers/{id} answers it, read
// and decorated from the pool once a write has committed — never inside the
// transaction, since decorating asks the user directory.
func (s *server) customerAfterWrite(ctx context.Context, id int32) (gen.SafeCustomerResponse, error) {
	q := store.New(s.deps.Pool)
	row, err := q.GetCustomer(ctx, id)
	if err != nil {
		return gen.SafeCustomerResponse{}, fmt.Errorf("customers: read customer %d: %w", id, err)
	}
	summary, err := q.CustomerTimelineSummary(ctx, id)
	if err != nil {
		return gen.SafeCustomerResponse{}, fmt.Errorf("customers: timeline summary: %w", err)
	}
	customer := fromCustomerRow(row, summary)
	dec, err := s.decorate(ctx, q, customer)
	if err != nil {
		return gen.SafeCustomerResponse{}, err
	}
	return safeCustomerResponse(customer, s.hasPermission(ctx, legalIdentityView), dec), nil
}

// cancelAnonymisationSchedule calls a schedule off as part of another write
// that takes the customer out of what may be anonymised — a restore
// (writeCustomerCore) or a change of type away from person (customer_type.go)
// — in that write's transaction, under the lock it already holds, and records
// it (design D4, this plan's reading): left in place, a date would fire the
// night the customer was archived again, months after anybody meant it. The
// caller's own UPDATE advanced the revision; DropCustomerAnonymiseOn does not
// advance it twice. Nothing scheduled, nothing happens.
func cancelAnonymisationSchedule(ctx context.Context, txq *store.Queries, id int32, locked store.LockCustomerRow, now time.Time, act actor) error {
	if !locked.AnonymiseOn.Valid {
		return nil
	}
	if err := txq.DropCustomerAnonymiseOn(ctx, id); err != nil {
		return fmt.Errorf("call off the anonymisation of %d: %w", id, err)
	}
	return recordAnonymisationCancelled(ctx, txq, now, id, locked.AnonymiseOn.Time, act.Kind, act.Display, act.UserID)
}

// PutCustomersByIdAnonymisation Schedule a private person's anonymisation
// (PUT /api/v1/customers/{id}/anonymisation)
//
// Order: (1) the day, 400 before any database access; (2) the customer on the
// pool, its 404, and — answered there, before the actor costs a directory call
// — a business, a customer that is not archived, and the day already
// scheduled; a read-only customer's refusal is left to the lock, which words
// it; (3) the actor; (4) the transaction: the lock and the read-only refusal,
// the two refusals again under it, the no-op again, the write and the event;
// (5) the customer, read after commit.
func (s *server) PutCustomersByIdAnonymisation(ctx context.Context, req gen.PutCustomersByIdAnonymisationRequestObject) (gen.PutCustomersByIdAnonymisationResponseObject, error) {
	raw := ""
	if req.Body != nil {
		raw = req.Body.AnonymiseOn
	}
	on, msg := validateAnonymiseOn(raw, s.deps.Clock())
	if msg != "" {
		return gen.PutCustomersByIdAnonymisation400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem("Invalid anonymisation",
			map[string][]string{"anonymiseOn": {msg}})), nil
	}

	current, err := store.New(s.deps.Pool).CustomerForPersonalData(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PutCustomersByIdAnonymisation404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("customers: get customer: %w", err)
	}
	if current.AnonymisedAt == nil && current.MergedIntoCustomerID == nil {
		if problem := scheduleRefusal(current.CustomerNumber, current.Name, current.Type, current.Status); problem != nil {
			return gen.PutCustomersByIdAnonymisation409ApplicationProblemPlusJSONResponse(*problem), nil
		}
		if current.AnonymiseOn.Valid && current.AnonymiseOn.Time.Equal(on) {
			return s.putAnonymisationAnswer(ctx, req.Id)
		}
	}

	act, err := s.actorFor(ctx, generatedFallbackActor)
	if err != nil {
		return nil, fmt.Errorf("customers: resolve actor: %w", err)
	}
	now := s.deps.Clock()
	var refusal *gen.CustomerConflictProblem
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		locked, err := lockWritableCustomer(ctx, txq, req.Id)
		if err != nil {
			return err
		}
		if refusal = scheduleRefusal(current.CustomerNumber, current.Name, locked.Type, locked.Status); refusal != nil {
			return errAnonymisationRefused
		}
		var previous *time.Time
		if locked.AnonymiseOn.Valid {
			if locked.AnonymiseOn.Time.Equal(on) {
				return nil
			}
			was := locked.AnonymiseOn.Time
			previous = &was
		}
		if err := txq.SetCustomerAnonymiseOn(ctx, store.SetCustomerAnonymiseOnParams{
			ID: req.Id, AnonymiseOn: pgtype.Date{Time: on, Valid: true}, Now: now,
		}); err != nil {
			return err
		}
		return recordAnonymisationScheduled(ctx, txq, now, req.Id, on, previous, act.Kind, act.Display, act.UserID)
	})
	switch {
	case isReadOnlyCustomer(err):
		return gen.PutCustomersByIdAnonymisation409ApplicationProblemPlusJSONResponse(readOnlyProblem(err)), nil
	case errors.Is(err, errAnonymisationRefused):
		return gen.PutCustomersByIdAnonymisation409ApplicationProblemPlusJSONResponse(*refusal), nil
	case errors.Is(err, pgx.ErrNoRows):
		return gen.PutCustomersByIdAnonymisation404Response{}, nil
	case err != nil:
		return nil, fmt.Errorf("customers: schedule the anonymisation of %d: %w", req.Id, err)
	}
	return s.putAnonymisationAnswer(ctx, req.Id)
}

func (s *server) putAnonymisationAnswer(ctx context.Context, id int32) (gen.PutCustomersByIdAnonymisationResponseObject, error) {
	answer, err := s.customerAfterWrite(ctx, id)
	if err != nil {
		return nil, err
	}
	return gen.PutCustomersByIdAnonymisation200JSONResponse(answer), nil
}

// DeleteCustomersByIdAnonymisation Cancel a private person's anonymisation
// (DELETE /api/v1/customers/{id}/anonymisation)
//
// Order: (1) the customer on the pool, its 404, an anonymised one's 409, and
// nothing scheduled — answered as it is, before the actor; (2) the actor; (3)
// under the lock, the same two again, the write and the event. It takes
// LockCustomer, not lockWritableCustomer: calling a schedule off is the one
// write a merged-away customer takes, since one made before its merge would
// otherwise be irrevocable — and the merge refusal is the only thing
// lockWritableCustomer would add besides the anonymised one asked here.
func (s *server) DeleteCustomersByIdAnonymisation(ctx context.Context, req gen.DeleteCustomersByIdAnonymisationRequestObject) (gen.DeleteCustomersByIdAnonymisationResponseObject, error) {
	current, err := store.New(s.deps.Pool).CustomerForPersonalData(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.DeleteCustomersByIdAnonymisation404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("customers: get customer: %w", err)
	}
	if current.AnonymisedAt != nil {
		return gen.DeleteCustomersByIdAnonymisation409ApplicationProblemPlusJSONResponse(customerAnonymised(*current.AnonymisedAt).problem), nil
	}
	if !current.AnonymiseOn.Valid {
		return s.deleteAnonymisationAnswer(ctx, req.Id)
	}

	act, err := s.actorFor(ctx, generatedFallbackActor)
	if err != nil {
		return nil, fmt.Errorf("customers: resolve actor: %w", err)
	}
	now := s.deps.Clock()
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		locked, err := txq.LockCustomer(ctx, req.Id)
		if err != nil {
			return err
		}
		if locked.AnonymisedAt != nil {
			return customerAnonymised(*locked.AnonymisedAt)
		}
		if !locked.AnonymiseOn.Valid {
			return nil
		}
		if err := txq.SetCustomerAnonymiseOn(ctx, store.SetCustomerAnonymiseOnParams{ID: req.Id, Now: now}); err != nil {
			return err
		}
		return recordAnonymisationCancelled(ctx, txq, now, req.Id, locked.AnonymiseOn.Time, act.Kind, act.Display, act.UserID)
	})
	switch {
	case isReadOnlyCustomer(err):
		return gen.DeleteCustomersByIdAnonymisation409ApplicationProblemPlusJSONResponse(readOnlyProblem(err)), nil
	case errors.Is(err, pgx.ErrNoRows):
		return gen.DeleteCustomersByIdAnonymisation404Response{}, nil
	case err != nil:
		return nil, fmt.Errorf("customers: cancel the anonymisation of %d: %w", req.Id, err)
	}
	return s.deleteAnonymisationAnswer(ctx, req.Id)
}

func (s *server) deleteAnonymisationAnswer(ctx context.Context, id int32) (gen.DeleteCustomersByIdAnonymisationResponseObject, error) {
	answer, err := s.customerAfterWrite(ctx, id)
	if err != nil {
		return nil, err
	}
	return gen.DeleteCustomersByIdAnonymisation200JSONResponse(answer), nil
}
```

In `customers.go`'s `writeCustomerCore`, change `if _, err := lockWritableCustomer(ctx, txq, id); err != nil {` to

```go
	locked, err := lockWritableCustomer(ctx, txq, id)
	if err != nil {
```

(the closing of that `if` is unchanged; rename any later `err :=` in the function that now shadows nothing to `err =` if the compiler says so), and directly before its final `return updated, nil, nil`, add:

```go
	// A customer leaving the archive takes its anonymisation date with it
	// (customers GDPR design D4): only an archived person may be scheduled.
	if after.Status != "archived" {
		if err := cancelAnonymisationSchedule(ctx, txq, id, locked, now, act); err != nil {
			return store.UpdateCustomerRow{}, nil, err
		}
	}
```

and to its doc comment's list of what it does add `; and a restore's calling off of an anonymisation date (customers GDPR design D4)` after `customer.status_changed for a status change`.

In `customer_type.go`, change `if _, err := lockWritableCustomer(ctx, txq, req.Id); err != nil {` to `locked, err := lockWritableCustomer(ctx, txq, req.Id)` / `if err != nil {`, delete the now-redundant `var err error` below it, and directly after the `recordCustomerTypeChanged(…)` call's `if … { return err }` add:

```go
		// A business is not anonymised (customers GDPR design D4): a person
		// turned into one takes its anonymisation date with it.
		if customerType != "person" {
			if err := cancelAnonymisationSchedule(ctx, txq, req.Id, locked, now, act); err != nil {
				return err
			}
		}
```

- [ ] **Step 7: Run everything, show it can fail, regenerate, commit**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- gofmt -l internal && mise exec -- go vet ./... && mise exec -- go build ./...
mise exec -- go test -count=1 ./internal/customers/... ./internal/openapi/...
cd /home/anders/projects/vantigo/vantigo && mise exec -- bun run gen:client && git status --short
cd apps/server && mise exec -- go run ./internal/openapi/cmd/contract coverage -corpus ../../openapi/testdata/exchanges -out ../../openapi/COVERAGE.md
```
Expected: PASS — `TestAMergedAwayCustomerRefusesEveryWrite` unchanged (the rename is behaviour-free for it), the coverage gate with both new operations exercised by a 200.

Prove the tests can fail, restoring after each: delete the `locked.AnonymisedAt != nil` check in `lockWritableCustomer` — `TestAnAnonymisedCustomerIsReadOnly` goes red on the writes that learn it only under the lock (restore, contact info, address, tags, schedule, merge) — the ones that refuse on the pool first (the timeline entry) and the cancel stay green; delete the `after.Status != "archived"` block — the restore half of `TestLeavingTheArchiveCallsTheScheduleOff` goes red; make `scheduleRefusal`'s first case `customerType != "person" && status != "archived"` — the archived business is scheduled and the refusal test goes red; change `on.Before(civilDate(now))` to `!on.After(civilDate(now))` — the today case goes red; use `lockWritableCustomer` in the DELETE — `TestAScheduleMadeBeforeAMergeCanStillBeCalledOff` goes red with `customer_merged`. Say what each printed.

```bash
cd /home/anders/projects/vantigo/vantigo
cat > /tmp/claude-1000/msg-gdpr-5.txt <<'EOF'
feat(customers): an archived private person's anonymisation can be scheduled, and an anonymised customer is read-only

PUT /customers/{id}/anonymisation {anonymiseOn} puts a day — today or
later, no default — on an archived private person and records
customer.anonymisation_scheduled; DELETE calls it off and records
customer.anonymisation_cancelled, the one write a merged-away customer
takes. A business is refused personal_data_not_a_person and a customer
not archived personal_data_customer_active. Restoring a customer or
changing its type away from person calls its schedule off in the same
transaction. The merged-away refusal becomes a read-only refusal with a
second code: every write to an anonymised customer answers
customer_anonymised through the same lock-time check, a CSV row naming
it is refused, and a merge will not absorb it.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
PATHS="openapi/customers.yaml apps/server/internal/openapi/specs/customers.yaml apps/server/internal/customers/gen/api.gen.go \
 apps/customers/frontend/src/api-schema.d.ts openapi/COVERAGE.md apps/server/internal/openapi/openapi_test.go \
 apps/server/internal/customers/queries/addresses.sql apps/server/internal/customers/queries/merge.sql \
 apps/server/internal/customers/queries/personal_data.sql apps/server/internal/customers/store/addresses.sql.go \
 apps/server/internal/customers/store/merge.sql.go apps/server/internal/customers/store/personal_data.sql.go \
 apps/server/internal/customers/anonymisation.go apps/server/internal/customers/anonymisation_test.go \
 apps/server/internal/customers/merge.go apps/server/internal/customers/import.go \
 apps/server/internal/customers/timeline_events.go apps/server/internal/customers/customers.go \
 apps/server/internal/customers/customer_type.go apps/server/internal/customers/addresses.go \
 apps/server/internal/customers/billing_profile.go apps/server/internal/customers/contact_info.go \
 apps/server/internal/customers/contacts.go apps/server/internal/customers/follow_ups.go \
 apps/server/internal/customers/group_membership.go apps/server/internal/customers/legal_identity.go \
 apps/server/internal/customers/owner.go apps/server/internal/customers/peppol_lookup.go \
 apps/server/internal/customers/peppol_recheck_worker.go apps/server/internal/customers/registry.go \
 apps/server/internal/customers/registry_feed_worker.go apps/server/internal/customers/tags.go \
 apps/server/internal/customers/timeline.go"
git status --short   # every changed file of this task must be in PATHS
git add $PATHS && git commit -F /tmp/claude-1000/msg-gdpr-5.txt -- $PATHS
git show --stat HEAD && git status --short
```

---

### Task 6: The anonymisation worker (D4)

`customers-anonymisation`: every due private person, at most fifty a cycle, each in one retried transaction with its row locked — the row cleared, what hangs off it deleted or detached, every timeline entry and revision rewritten, the merge chain followed, every module's eraser inside, then the marker, the revision and `customer.anonymised`.

**Files:**
- Create: `apps/server/internal/customers/anonymisation_worker.go`, `apps/server/internal/customers/anonymisation_worker_test.go`, `apps/server/internal/integration/personal_data_test.go`
- Modify: `apps/server/internal/customers/queries/personal_data.sql` (+ generated `store/personal_data.sql.go`), `anonymisation.go`, `timeline_events.go`, `module.go`, `module_test.go`, `export_test.go`, `apps/server/internal/config/config.go`, `apps/server/internal/config/config_test.go`, `apps/server/cmd/vantigo/main_test.go`, `deploy/compose/vantigo.env.example`
- Read first (do not change): `peppol_recheck_worker.go` (the whole worker shape), `registry_feed_worker_test.go:1080-1150` (the lease tests), `queries/merge.sql` (the set-based statements and `FlattenMergedIntoChain`'s one-hop rule), `timeline_events.go` (every payload's keys), `contacts_timeline.go:34-62`, `config.go:565-580`, `module_test.go:90-175`, `internal/integration/merge_test.go`

**Interfaces:**
- Consumes: `Deps.CustomerPersonalData` (Task 2, collected by `module.Workers`), the three implementations (Task 3), the columns and `CustomerForPersonalData` (Task 4), `LockCustomer`'s new columns and the read-only refusal (Task 5).
- Produces Go: `customers.NewAnonymisationWorker(module.Deps) *customers.AnonymisationWorker` with `Name`, `Interval`, `Run`, `RunCycle(ctx) (bool, error)`; `(*server).anonymiseCustomer(ctx, id) (bool, error)`; `recordCustomerAnonymised`; `config.Config.CustomersAnonymisationEnabled bool`, `CustomersAnonymisationPoll time.Duration`; `customers.AnonymisationLeaseKeyForTest`.
- Config: `CUSTOMERS_ANONYMISATION_ENABLED` (`0`/`1`, default `1`), `CUSTOMERS_ANONYMISATION_POLL` (positive duration, default `24h`).
- Event: `customer.anonymised`, summary "Customer anonymised", payload `{customerId, erased: [{kind, count}]}`, actor `system`, recorded after the rewrite.
- Kinds this module reports first: `customers.addresses`, `customers.contactAssociations`, `customers.contacts`, `customers.timelineEntries`.

- [ ] **Step 1: The worker's statements**

Append to `apps/server/internal/customers/queries/personal_data.sql`:

```sql
-- name: DueAnonymisations :many
-- DueAnonymisations is the worker's batch (design D4): every private person
-- whose day has come (UTC), archived and not yet anonymised, oldest day first,
-- at most row_limit. ix_customers_anonymise_on holds exactly the scheduled and
-- not yet anonymised. A merged-away customer scheduled before its merge is
-- here too: it is archived, and it is a person.
SELECT id
FROM customers.customers
WHERE anonymise_on <= @today::date AND anonymised_at IS NULL AND status = 'archived' AND type = 'person'
ORDER BY anonymise_on, id
LIMIT @row_limit::int;

-- name: CustomersMergedInto :many
-- CustomersMergedInto is the rest of a merge chain (design D4: a chain is one
-- person): every customer merged into this one and not yet anonymised.
-- FlattenMergedIntoChain keeps every marker one hop long, so this one level is
-- the whole chain. ix_customers_merged_into finds them.
SELECT id
FROM customers.customers
WHERE merged_into_customer_id = @customer_id::int AND anonymised_at IS NULL
ORDER BY id;

-- name: DeleteAllCustomerAddresses :execrows
-- Every address of an anonymised customer (design D4): an address is where a
-- person lives or is reached, and none of it is bookkeeping.
DELETE FROM customers.customer_addresses WHERE customer_id = @customer_id;

-- name: DetachCustomerContacts :many
-- Every association of an anonymised customer, its role rows cascading with it
-- (design D4), answering the contacts it linked so their orphans can go next.
DELETE FROM customers.customers_contacts WHERE customer_id = @customer_id RETURNING contact_id;

-- name: LockContactsForAnonymisation :exec
-- The detached contacts, locked before the orphan delete below reads whether
-- anything else still links them: an attach key-shares the contact row, so one
-- that has not taken it yet waits here, and one that has is committed — and
-- seen — by the time the lock is granted. Ascending id, every multi-contact
-- writer's order.
SELECT id FROM customers.contacts WHERE id = ANY(@contact_ids::int[]) ORDER BY id FOR UPDATE;

-- name: DeleteOrphanedContacts :execrows
-- A contact the anonymised customer was the only one linked to existed for it
-- alone (design D4), and goes; one another customer still links is somebody
-- else's contact too, and stays.
DELETE FROM customers.contacts c
WHERE c.id = ANY(@contact_ids::int[])
  AND NOT EXISTS (SELECT 1 FROM customers.customers_contacts cc WHERE cc.contact_id = c.id);

-- name: AnonymiseTimelineEntries :execrows
-- AnonymiseTimelineEntries rewrites every timeline entry of an anonymised
-- customer in one statement (design D4): every entry stays, dated and typed,
-- with its content anonymised. A manual entry's summary and note become the
-- anonymised text and its source URL goes — a link is content too. A generated
-- entry's summary does as well, unless its event type is one whose summary is
-- built from nothing personal (kept_summary_types, anonymisation.go). Every
-- payload's top-level keys named in personal_keys become the anonymised text,
-- every other key — customerId, ids, dates, counts — is kept as it was;
-- coalesce keeps an empty object an object. Neither updated_at nor
-- current_revision moves: this is not an edit, and the revisions below are
-- rewritten the same way rather than added to.
UPDATE customers.customers_timeline_entries e
SET summary = CASE
        WHEN e.provenance = 'generated' AND e.event_type = ANY(@kept_summary_types::text[]) THEN e.summary
        ELSE @anonymised::text
    END,
    note = CASE WHEN e.note IS NULL THEN NULL ELSE @anonymised::text END,
    source_url = NULL,
    payload_json = CASE
        WHEN jsonb_typeof(e.payload_json) = 'object' THEN coalesce((
            SELECT jsonb_object_agg(p.key, CASE WHEN p.key = ANY(@personal_keys::text[]) THEN to_jsonb(@anonymised::text) ELSE p.value END)
            FROM jsonb_each(e.payload_json) AS p(key, value)), '{}'::jsonb)
        ELSE e.payload_json
    END
WHERE e.customer_id = @customer_id::int;

-- name: AnonymiseTimelineRevisions :exec
-- The same rewrite of every revision of those entries (design D4: "revisions
-- the same"), found through their entry — the revisions' own customer_id is not
-- indexed, and ux_customers_timeline_entries_revisions_entry_revision is.
UPDATE customers.customers_timeline_entries_revisions r
SET summary = CASE
        WHEN r.provenance = 'generated' AND r.event_type = ANY(@kept_summary_types::text[]) THEN r.summary
        ELSE @anonymised::text
    END,
    note = CASE WHEN r.note IS NULL THEN NULL ELSE @anonymised::text END,
    source_url = NULL,
    payload_json = CASE
        WHEN jsonb_typeof(r.payload_json) = 'object' THEN coalesce((
            SELECT jsonb_object_agg(p.key, CASE WHEN p.key = ANY(@personal_keys::text[]) THEN to_jsonb(@anonymised::text) ELSE p.value END)
            FROM jsonb_each(r.payload_json) AS p(key, value)), '{}'::jsonb)
        ELSE r.payload_json
    END
WHERE r.customer_timeline_entry_id IN (
    SELECT id FROM customers.customers_timeline_entries WHERE customer_id = @customer_id::int
);

-- name: AnonymiseAbsorbedSnapshots :exec
-- A merged-away customer anonymised on its own (design D4) takes its snapshot
-- off its survivor's timeline: the customer.merged entry that absorbed it keeps
-- its moved counts and loses the absorbed block and the summary naming it.
-- Nothing else of the survivor's is touched — its own history is its own, and
-- the history that moved to it with the merge is its now.
UPDATE customers.customers_timeline_entries
SET summary = @anonymised::text,
    payload_json = jsonb_set(payload_json, '{absorbed}', to_jsonb(@anonymised::text))
WHERE customer_id = @survivor_id::int AND event_type = 'customer.merged'
  AND jsonb_typeof(payload_json -> 'absorbed') = 'object'
  AND (payload_json -> 'absorbed' ->> 'id')::int = @absorbed_id::int;

-- name: AnonymiseAbsorbedSnapshotRevisions :exec
-- And that entry's revisions, which carry its customer_id (MoveTimelineRevisions).
UPDATE customers.customers_timeline_entries_revisions
SET summary = @anonymised::text,
    payload_json = jsonb_set(payload_json, '{absorbed}', to_jsonb(@anonymised::text))
WHERE customer_id = @survivor_id::int AND event_type = 'customer.merged'
  AND jsonb_typeof(payload_json -> 'absorbed') = 'object'
  AND (payload_json -> 'absorbed' ->> 'id')::int = @absorbed_id::int;

-- name: AnonymiseCustomerRow :exec
-- The row itself, last before the event (design D4): the name becomes the
-- anonymised name — the customer number stays, it is the bookkeeping reference
-- — the legal identity, the contact info and the billing profile's five
-- identifiers are cleared, and the terms, currency, language and delivery
-- methods stay: they say how the customer was invoiced, not who it was. Owner
-- and tags stay: staff and vocabulary. It leaves its group, though, the
-- merged-away customer's way (MarkCustomerMerged): an anonymised customer
-- refuses every write, the group PUT included, so a group it still counted in
-- could never be emptied and deleted (customers_group_id_fkey restricts); the
-- group is vocabulary, so no event needs to say which it was. anonymise_on is
-- the day that was scheduled — for a customer merged into the one scheduled,
-- that customer's day, whatever its own schedule said: the chain is one person
-- and one anonymisation. A write to the row, so the revision advances.
UPDATE customers.customers
SET name = @name::text,
    legal_country = NULL, legal_id = NULL, legal_name = NULL, legal_source = NULL, legal_type = NULL,
    email = NULL, phone = NULL, website = NULL,
    invoice_email = NULL, reminder_email = NULL, peppol_id = NULL, gln = NULL, buyer_reference = NULL,
    group_id = NULL,
    anonymise_on = @anonymise_on::date,
    anonymised_at = @now::timestamptz, updated_at = @now::timestamptz, revision = revision + 1
WHERE id = @id;
```

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server && mise exec -- go generate ./... && mise exec -- go generate ./... && git status --short
```
Read the generated parameter structs: `store.DueAnonymisationsParams{Today pgtype.Date; RowLimit int32}`, `store.AnonymiseTimelineEntriesParams{KeptSummaryTypes []string; Anonymised string; PersonalKeys []string; CustomerID int32}` (the same for revisions), `store.AnonymiseAbsorbedSnapshotsParams{Anonymised string; SurvivorID, AbsorbedID int32}`, `store.AnonymiseCustomerRowParams{Name string; AnonymiseOn pgtype.Date; Now time.Time; ID int32}`; if sqlc named a field differently, use its name.

- [ ] **Step 2: Write the failing tests**

In `apps/server/internal/customers/export_test.go`, append:

```go
// AnonymisationLeaseKeyForTest is the anonymisation worker's advisory-lease
// key, exported so a test can take the same lock from a second connection and
// prove a cycle skips — RegistryFeedLeaseKeyForTest's seam.
const AnonymisationLeaseKeyForTest = anonymisationLeaseKey
```

Create `apps/server/internal/customers/anonymisation_worker_test.go`:

```go
package customers_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/customers"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is the anonymisation worker (customers GDPR design D4), driven
// through RunCycle with h.Advance: nothing waits on a real interval. Another
// module's eraser is a fake (personal_data_test.go's fakePersonalData), for
// depguard's reason; each real one proves its SQL in its own package.

func runAnonymisation(t *testing.T, h *modtest.Harness) {
	t.Helper()
	if ran, err := customers.NewAnonymisationWorker(h.Deps()).RunCycle(context.Background()); err != nil || !ran {
		t.Fatalf("RunCycle = %v, %v; want true, nil", ran, err)
	}
}

// scheduleOn schedules an archived person's anonymisation, failing the test on
// anything but 200.
func scheduleOn(t *testing.T, h *modtest.Harness, id int32, anonymiseOn string) {
	t.Helper()
	if r := putAnonymisation(t, personalDataClient(t, h), id, anonymiseOn); r.Status != http.StatusOK {
		t.Fatalf("schedule %d on %s: status %d body %s", id, anonymiseOn, r.Status, r.Body)
	}
}

type anonymisedPayloadJSON struct {
	CustomerId int32 `json:"customerId"`
	Erased     []struct {
		Kind  string `json:"kind"`
		Count int64  `json:"count"`
	} `json:"erased"`
}

// personalTraces counts a customer's timeline rows — entries or revisions —
// whose text still says anything a fixture put there about the person.
func personalTraces(t *testing.T, h *modtest.Harness, table string, customerID int32) int {
	t.Helper()
	return h.Count(t, `SELECT count(*) FROM customers.`+pgx.Identifier{table}.Sanitize()+`
	                   WHERE customer_id = $1
	                     AND (summary || coalesce(note, '') || coalesce(source_url, '') || coalesce(payload_json::text, ''))
	                         ~* '(kari|nordmann|per@|faktura@|purring@|KARI-1|19800101)'`, customerID)
}

// TestAnonymisationWorker_TakesThePersonOutAndKeepsTheBookkeeping is design D4's
// whole list on one customer that has something in every place it names: not
// before the day; on the day, the row cleared exactly as listed and kept
// exactly as listed, addresses, Peppol answer and registry record gone,
// associations detached and the contact only this customer had deleted, every
// timeline entry and revision kept with its content anonymised and its shape
// intact, the other module's eraser called inside the transaction, and
// customer.anonymised written last, in words. Afterwards the customer is
// read-only and its export answers what is left.
func TestAnonymisationWorker_TakesThePersonOutAndKeepsTheBookkeeping(t *testing.T) {
	t.Parallel()
	var sawAddresses atomic.Int64
	var sawName atomic.Value
	fake := &fakePersonalData{
		erased: []contracts.ErasedData{{Kind: "fake.things", Count: 3}},
		during: func(ctx context.Context, tx pgx.Tx, id int32) error {
			// Read through the anonymisation's own transaction: its address
			// delete is visible here and nowhere else yet, and the row is not
			// cleared until every module has run.
			var n int64
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM customers.customer_addresses WHERE customer_id = $1`, id).Scan(&n); err != nil {
				return err
			}
			sawAddresses.Store(n)
			var name string
			if err := tx.QueryRow(ctx, `SELECT name FROM customers.customers WHERE id = $1`, id).Scan(&name); err != nil {
				return err
			}
			sawName.Store(name)
			return nil
		},
	}
	h := newHarness(t, modtest.WithCustomerPersonalData(contracts.CustomerPersonalDataHolder{Module: "fake", Data: fake}))
	c, userID := h.SignInUser(t, mergeKeys...)
	ctx := context.Background()

	person := createCustomerOfType(t, c, "Kari Nordmann", "person")
	setPersonIdentity(t, h, person.Id)
	if r := putContactInfo(t, c, person.Id, map[string]any{"email": "kari@example.test", "phone": "+47 900 00 000", "website": "https://kari.example.test"}); r.Status != http.StatusOK {
		t.Fatalf("contact info: status %d body %s", r.Status, r.Body)
	}
	createAddress(t, c, person.Id, fullAddressBody("postal", nil))
	createAddress(t, c, person.Id, fullAddressBody("invoice", nil))
	if r := putBillingProfile(t, c, person.Id, map[string]any{
		"invoiceEmail": "faktura@example.test", "reminderEmail": "purring@example.test", "buyerReference": "KARI-1",
		"currency": "NOK", "paymentTermsDays": 14, "language": "nb", "invoiceDelivery": "email",
	}); r.Status != http.StatusOK {
		t.Fatalf("billing profile: status %d body %s", r.Status, r.Body)
	}
	h.Exec(t, `UPDATE customers.customers SET peppol_id = '9908:910000000', gln = '7080000000001' WHERE id = $1`, person.Id)
	if r := putOwner(t, c, person.Id, map[string]any{"ownerUserId": userID}); r.Status != http.StatusOK {
		t.Fatalf("owner: status %d body %s", r.Status, r.Body)
	}
	group := createGroup(t, c, map[string]any{"name": "Privatkunder"})
	putCustomerGroup(t, c, person.Id, map[string]any{"groupId": group.Id})
	tag := createTag(t, c, map[string]any{"name": "Nabo"})
	putCustomerTags(t, c, person.Id, []string{tag.Id})
	orphan := createContact(t, c, map[string]any{"firstName": "Per", "lastName": "Nordmann", "email": "per@example.test"})
	attachContact(t, c, person.Id, orphan.Id, "Ektefelle")
	shared := createContact(t, c, map[string]any{"firstName": "Anne", "lastName": "Hansen"})
	attachContact(t, c, person.Id, shared.Id, "Nabo")
	neighbour := createCustomer(t, c, "Hansen Rør AS")
	attachContact(t, c, neighbour.Id, shared.Id, "Daglig leder")
	entry := createWithFollowUp(t, c, person.Id, day(h, 0), "Kari ringte om strømmen", map[string]any{"dueOn": day(h, 7)})
	putEntryWithFollowUp(t, c, person.Id, entry.Id, entry.CurrentRevision, map[string]any{"dueOn": day(h, 14)})
	h.Exec(t, `UPDATE customers.customers_timeline_entries SET source_url = 'https://kari.example.test/profil' WHERE id = $1`, entry.Id)
	if r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d", person.Id), map[string]any{"name": "Kari Nordmann Hansen", "status": "active"}); r.Status != http.StatusOK {
		t.Fatalf("rename: status %d body %s", r.Status, r.Body)
	}
	if r := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d", person.Id), nil); r.Status != http.StatusNoContent {
		t.Fatalf("archive: status %d body %s", r.Status, r.Body)
	}
	insertRegistryAndPeppol(t, h, person.Id)
	scheduleOn(t, h, person.Id, day(h, 3))
	shape := func() string {
		return modtest.One[string](t, h, `SELECT string_agg(id || ':' || event_type || ':' || occurred_on || ':' || state, ',' ORDER BY id)
		                                  FROM customers.customers_timeline_entries WHERE customer_id = $1`, person.Id)
	}
	entriesBefore := h.Count(t, `SELECT count(*) FROM customers.customers_timeline_entries WHERE customer_id = $1`, person.Id)
	shapeBefore := shape()
	revisionBefore := fetchCustomerJSON(t, c, person.Id).Revision

	// Not the day yet: nothing happens.
	runAnonymisation(t, h)
	if got := fetchCustomerJSON(t, c, person.Id); got.Name != "Kari Nordmann Hansen" || len(fake.erasesSoFar()) != 0 {
		t.Fatalf("before the day: name %q, erases %v — want nothing done", got.Name, fake.erasesSoFar())
	}

	h.Advance(72 * time.Hour)
	// Three days is past the 8-hour idle timeout (SESSION_IDLE_TIMEOUT): the
	// session from before the advance answers 401 now, so sign in again.
	c = h.SignIn(t, mergeKeys...)
	runAnonymisation(t, h)

	// The row: cleared as listed, kept as listed.
	type rowState struct {
		Name, Status                                              string
		Number                                                    int64
		LegalID, Email, Phone, Website                            *string
		InvoiceEmail, ReminderEmail, PeppolID, Gln, BuyerReference *string
		Currency, Language, InvoiceDelivery                       *string
		Terms                                                     *int32
		OwnerSet, GroupSet                                        bool
		AnonymisedAt                                              *time.Time
		AnonymiseOn                                               string
		Revision                                                  int32
	}
	var row rowState
	if err := h.Pool().QueryRow(ctx, `SELECT name, status, customer_number, legal_id, email, phone, website,
	        invoice_email, reminder_email, peppol_id, gln, buyer_reference, currency, language, invoice_delivery,
	        payment_terms_days, owner_user_id IS NOT NULL, group_id IS NOT NULL, anonymised_at, anonymise_on::text, revision
	    FROM customers.customers WHERE id = $1`, person.Id).Scan(&row.Name, &row.Status, &row.Number, &row.LegalID, &row.Email,
		&row.Phone, &row.Website, &row.InvoiceEmail, &row.ReminderEmail, &row.PeppolID, &row.Gln, &row.BuyerReference,
		&row.Currency, &row.Language, &row.InvoiceDelivery, &row.Terms, &row.OwnerSet, &row.GroupSet, &row.AnonymisedAt,
		&row.AnonymiseOn, &row.Revision); err != nil {
		t.Fatalf("read the row: %v", err)
	}
	if row.Name != "Anonymised person" || row.Status != "archived" || row.Number != person.CustomerNumber {
		t.Errorf("name/status/number = %q/%q/%d, want Anonymised person, archived, %d", row.Name, row.Status, row.Number, person.CustomerNumber)
	}
	for field, v := range map[string]*string{"legal_id": row.LegalID, "email": row.Email, "phone": row.Phone, "website": row.Website,
		"invoice_email": row.InvoiceEmail, "reminder_email": row.ReminderEmail, "peppol_id": row.PeppolID, "gln": row.Gln,
		"buyer_reference": row.BuyerReference} {
		if v != nil {
			t.Errorf("%s = %q, want cleared", field, *v)
		}
	}
	if str(row.Currency) != "NOK" || str(row.Language) != "nb" || str(row.InvoiceDelivery) != "email" || row.Terms == nil || *row.Terms != 14 ||
		!row.OwnerSet || row.GroupSet {
		t.Errorf("row = %+v, want terms, currency, language, delivery and owner kept, and the group left", row)
	}
	// Left, so the group can still be emptied and deleted: the anonymised
	// customer refuses the group PUT that would otherwise take it out.
	if r := c.Do(http.MethodDelete, "/api/v1/customers/groups/"+group.Id, nil); r.Status != http.StatusNoContent {
		t.Errorf("delete the anonymised customer's old group: status %d body %s, want 204", r.Status, r.Body)
	}
	if row.AnonymisedAt == nil || !row.AnonymisedAt.Equal(h.Now()) || row.AnonymiseOn != day(h, 0) || row.Revision != revisionBefore+1 {
		t.Errorf("anonymised at %v on %s, revision %d (was %d); want now, the scheduled day, one on", row.AnonymisedAt, row.AnonymiseOn, row.Revision, revisionBefore)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customer_tags WHERE customer_id = $1`, person.Id); n != 1 {
		t.Errorf("tags = %d, want the one kept", n)
	}

	// What hung off it.
	for table, want := range map[string]int{"customer_addresses": 0, "customer_peppol_lookups": 0, "customer_registry_records": 0, "customers_contacts": 0} {
		if n := h.Count(t, `SELECT count(*) FROM customers.`+pgx.Identifier{table}.Sanitize()+` WHERE customer_id = $1`, person.Id); n != want {
			t.Errorf("%s = %d, want %d", table, n, want)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.contacts WHERE id = $1`, orphan.Id); n != 0 {
		t.Error("the contact only this customer had is still there")
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers_contacts WHERE contact_id = $1 AND customer_id = $2`, shared.Id, neighbour.Id); n != 1 {
		t.Error("the contact another customer shares lost its other association")
	}

	// The timeline: every entry kept, typed and dated as it was; nothing of the
	// person left in any entry or revision; the kept summaries kept.
	if n := h.Count(t, `SELECT count(*) FROM customers.customers_timeline_entries WHERE customer_id = $1 AND event_type <> 'customer.anonymised'`, person.Id); n != entriesBefore {
		t.Errorf("entries = %d, want the %d there were", n, entriesBefore)
	}
	if got := modtest.One[string](t, h, `SELECT string_agg(id || ':' || event_type || ':' || occurred_on || ':' || state, ',' ORDER BY id)
	                                     FROM customers.customers_timeline_entries WHERE customer_id = $1 AND event_type <> 'customer.anonymised'`, person.Id); got != shapeBefore {
		t.Errorf("the timeline's shape moved:\n got %s\nwant %s", got, shapeBefore)
	}
	for _, table := range []string{"customers_timeline_entries", "customers_timeline_entries_revisions"} {
		if n := personalTraces(t, h, table, person.Id); n != 0 {
			t.Errorf("%s: %d rows still name the person", table, n)
		}
	}
	manual := modtest.One[string](t, h, `SELECT summary || '|' || note || '|' || coalesce(source_url, '-') || '|' || follow_up_on::text
	                                      FROM customers.customers_timeline_entries WHERE id = $1`, entry.Id)
	// The follow-up's day is the one the edit set, fourteen days on from the
	// start — eleven from the clock now that it has moved three.
	if manual != "[anonymised]|[anonymised]|-|"+day(h, 11) {
		t.Errorf("the manual entry = %q, want its words gone and its follow-up day kept", manual)
	}
	entries := timelineOf(t, c, person.Id)
	if status := entriesOfType(entries, "customer.status_changed"); len(status) == 0 || !strings.HasPrefix(str(status[0].Summary), "Customer status changed:") {
		t.Errorf("status changes = %+v, want their summary kept", status)
	}
	var created struct {
		CustomerId   int32  `json:"customerId"`
		CustomerName string `json:"customerName"`
	}
	if ev := entriesOfType(entries, "customer.created"); len(ev) != 1 || json.Unmarshal(ev[0].Payload, &created) != nil ||
		created.CustomerId != person.Id || created.CustomerName != "[anonymised]" || str(ev[0].Summary) != "[anonymised]" {
		t.Errorf("customer.created = %+v (%+v), want its id kept and its name gone", ev, created)
	}
	var attached struct {
		ContactId   int32  `json:"contactId"`
		DisplayName string `json:"displayName"`
	}
	if ev := entriesOfType(entries, "customer.contact_attached"); len(ev) != 2 || json.Unmarshal(ev[0].Payload, &attached) != nil ||
		attached.ContactId == 0 || attached.DisplayName != "[anonymised]" {
		t.Errorf("contact_attached = %+v (%+v), want the contact's id kept and its name gone", ev, attached)
	}

	// The other module, inside the transaction, before the row was cleared.
	if got := fake.erasesSoFar(); !slices.Equal(got, []int32{person.Id}) {
		t.Errorf("erases = %v, want the one customer once", got)
	}
	if sawAddresses.Load() != 0 || sawName.Load() != "Kari Nordmann Hansen" {
		t.Errorf("the eraser saw %d addresses and name %v, want 0 (inside the transaction) and the name not yet cleared", sawAddresses.Load(), sawName.Load())
	}

	// customer.anonymised, last and in words.
	last := entries[0]
	var payload anonymisedPayloadJSON
	if last.EventType != "customer.anonymised" || str(last.Summary) != "Customer anonymised" || last.ActorKind != "system" ||
		json.Unmarshal(last.Payload, &payload) != nil || payload.CustomerId != person.Id {
		t.Fatalf("newest entry = %+v, want customer.anonymised by the system", last)
	}
	want := fmt.Sprintf("[{customers.addresses 2} {customers.contactAssociations 2} {customers.contacts 1} {customers.timelineEntries %d} {fake.things 3}]", entriesBefore)
	if got := fmt.Sprint(payload.Erased); got != want {
		t.Errorf("erased = %s, want %s", got, want)
	}

	// Read-only from here on, and its export is what is left.
	refusedWith(t, putContactInfo(t, c, person.Id, map[string]any{"email": "kari@example.test"}), "customer_anonymised")
	var file personalDataJSON
	getPersonalData(t, personalDataClient(t, h), person.Id).JSON(&file)
	if file.Customer.Name != "Anonymised person" || file.Customer.Identity != nil || len(file.Contacts) != 0 ||
		file.Customer.Anonymisation == nil || file.Customer.Anonymisation.AnonymisedAt == nil {
		t.Errorf("the export afterwards = %+v, want what is left", file.Customer)
	}
}

// Not due, not archived, not a person, already anonymised: none of them is
// touched however far the clock moves.
func TestAnonymisationWorker_TakesOnlyWhatIsDue(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	later := archivedPerson(t, c, "Later Nordmann")
	scheduleOn(t, h, later.Id, day(h, 10))
	active := createCustomerOfType(t, c, "Active Nordmann", "person")
	business := createCustomer(t, c, "Acme AS")
	// Neither can be scheduled through the API; the worker must still refuse
	// them if a row ever says so.
	h.Exec(t, `UPDATE customers.customers SET anonymise_on = $2 WHERE id = ANY($1)`, []int32{active.Id, business.Id}, day(h, 0))
	c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d", business.Id), nil)

	h.Advance(48 * time.Hour)
	runAnonymisation(t, h)
	if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE anonymised_at IS NOT NULL`); n != 0 {
		t.Errorf("%d customers anonymised, want none", n)
	}
}

// A chain of merges is one person (design D4): the customer merged into the
// one that is due is anonymised in the same run, with its own event, and the
// customer.merged entry that describes it — on the due customer's own
// timeline — loses the snapshot with the rest of that timeline.
func TestAnonymisationWorker_AnonymisesTheWholeMergeChain(t *testing.T) {
	t.Parallel()
	fake := &fakePersonalData{}
	h := newHarness(t, modtest.WithCustomerPersonalData(contracts.CustomerPersonalDataHolder{Module: "fake", Data: fake}))
	c := mergeClient(t, h)
	absorbed := createCustomerOfType(t, c, "Kari Nordmann", "person")
	putContactInfo(t, c, absorbed.Id, map[string]any{"email": "kari@example.test"})
	survivor := createCustomerOfType(t, c, "Kari N.", "person")
	mergeOK(t, c, survivor.Id, absorbed.Id)
	c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d", survivor.Id), nil)
	scheduleOn(t, h, survivor.Id, day(h, 0))

	runAnonymisation(t, h)

	for _, id := range []int32{survivor.Id, absorbed.Id} {
		if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE id = $1 AND name = 'Anonymised person' AND email IS NULL
		                     AND anonymised_at IS NOT NULL AND anonymise_on = $2::date`, id, day(h, 0)); n != 1 {
			t.Errorf("customer %d is not anonymised on the chain's day", id)
		}
		if n := len(entriesOfType(timelineOf(t, c, id), "customer.anonymised")); n != 1 {
			t.Errorf("customer %d has %d customer.anonymised events, want its own one", id, n)
		}
	}
	merged := entriesOfType(timelineOf(t, c, survivor.Id), "customer.merged")
	var payload struct {
		Absorbed json.RawMessage `json:"absorbed"`
	}
	if len(merged) != 1 || json.Unmarshal(merged[0].Payload, &payload) != nil || string(payload.Absorbed) != `"[anonymised]"` {
		t.Errorf("customer.merged = %+v, want its absorbed snapshot gone", merged)
	}
	if got := fake.erasesSoFar(); !slices.Equal(got, []int32{survivor.Id, absorbed.Id}) {
		t.Errorf("erases = %v, want the survivor then the customer merged into it", got)
	}
}

// A customer scheduled and then merged away is anonymised on its day as itself
// (design D4): its row, and its snapshot on the survivor's customer.merged
// entry. Nothing else of the survivor's changes — its own name and history, and
// the history that moved to it with the merge, which is the survivor's now.
func TestAnonymisationWorker_AMergedAwayCustomerTakesItsSnapshotOffTheSurvivor(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := mergeClient(t, h)
	absorbed := archivedPerson(t, c, "Kari Nordmann")
	scheduleOn(t, h, absorbed.Id, day(h, 0))
	survivor := createCustomerOfType(t, c, "Kari N.", "person")
	putContactInfo(t, c, survivor.Id, map[string]any{"email": "kari.n@example.test"})
	mergeOK(t, c, survivor.Id, absorbed.Id)

	runAnonymisation(t, h)

	if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE id = $1 AND name = 'Anonymised person' AND anonymised_at IS NOT NULL`, absorbed.Id); n != 1 {
		t.Error("the merged-away customer was not anonymised")
	}
	got := fetchCustomerJSON(t, c, survivor.Id)
	if got.Name != "Kari N." || str(got.ContactInfo.Email) != "kari.n@example.test" {
		t.Errorf("the survivor = %+v, want it untouched", got)
	}
	entries := timelineOf(t, c, survivor.Id)
	merged := entriesOfType(entries, "customer.merged")
	var payload struct {
		Absorbed json.RawMessage `json:"absorbed"`
		Moved    []mergeMoveJSON `json:"moved"`
	}
	if len(merged) != 1 || json.Unmarshal(merged[0].Payload, &payload) != nil || string(payload.Absorbed) != `"[anonymised]"` ||
		str(merged[0].Summary) != "[anonymised]" || len(payload.Moved) == 0 {
		t.Errorf("customer.merged = %+v, want the snapshot and its summary gone and the counts kept", merged)
	}
	var movedHistory bool
	for _, e := range entriesOfType(entries, "customer.created") {
		movedHistory = movedHistory || str(e.Summary) == "Customer created: Kari Nordmann"
	}
	if !movedHistory {
		t.Error("the history that moved with the merge changed: it is the survivor's now")
	}
}

// A failing module rolls its customer back (design D4): nothing of that
// customer changed, the worker logs it and moves on to the next, and the next
// cycle tries it again.
func TestAnonymisationWorker_AFailingModuleRollsThatCustomerBack(t *testing.T) {
	t.Parallel()
	var failFor atomic.Int32
	fake := &fakePersonalData{during: func(_ context.Context, _ pgx.Tx, id int32) error {
		if id == failFor.Load() {
			return errors.New("the module is down")
		}
		return nil
	}}
	h := newHarness(t, modtest.WithCustomerPersonalData(contracts.CustomerPersonalDataHolder{Module: "fake", Data: fake}))
	c := authenticatedClient(t, h)
	first := archivedPerson(t, c, "Kari Nordmann")
	createAddress(t, c, first.Id, fullAddressBody("postal", nil))
	second := archivedPerson(t, c, "Ola Nordmann")
	scheduleOn(t, h, first.Id, day(h, 0))
	scheduleOn(t, h, second.Id, day(h, 0))
	failFor.Store(first.Id)

	runAnonymisation(t, h)

	if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE id = $1 AND name = 'Kari Nordmann' AND anonymised_at IS NULL`, first.Id); n != 1 {
		t.Error("the failed customer changed")
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customer_addresses WHERE customer_id = $1`, first.Id); n != 1 {
		t.Error("the failed customer's address went: its run was not rolled back")
	}
	if n := len(entriesOfType(timelineOf(t, c, first.Id), "customer.anonymised")); n != 0 {
		t.Errorf("the failed customer has %d customer.anonymised events", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE id = $1 AND anonymised_at IS NOT NULL`, second.Id); n != 1 {
		t.Error("the next customer was not anonymised: one failure stopped the cycle")
	}
	if logs := h.Logs(); !strings.Contains(logs, "anonymising a customer failed") || !strings.Contains(logs, fmt.Sprintf(`"customerId":%d`, first.Id)) {
		t.Errorf("logs = %s, want the failure logged with its customer", logs)
	}

	failFor.Store(0)
	runAnonymisation(t, h)
	if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE id = $1 AND anonymised_at IS NOT NULL`, first.Id); n != 1 {
		t.Error("the next cycle did not try the failed customer again")
	}
}

// A customer is anonymised once: a second cycle finds nothing, writes nothing
// and asks no module again.
func TestAnonymisationWorker_AnonymisesACustomerOnce(t *testing.T) {
	t.Parallel()
	fake := &fakePersonalData{}
	h := newHarness(t, modtest.WithCustomerPersonalData(contracts.CustomerPersonalDataHolder{Module: "fake", Data: fake}))
	c := authenticatedClient(t, h)
	person := archivedPerson(t, c, "Kari Nordmann")
	scheduleOn(t, h, person.Id, day(h, 0))

	runAnonymisation(t, h)
	revision := fetchCustomerJSON(t, c, person.Id).Revision
	h.Advance(24 * time.Hour)
	// A day is past the 8-hour idle timeout: sign in again.
	c = authenticatedClient(t, h)
	runAnonymisation(t, h)

	if got := fetchCustomerJSON(t, c, person.Id).Revision; got != revision {
		t.Errorf("revision %d after a second cycle, want %d", got, revision)
	}
	if n := len(entriesOfType(timelineOf(t, c, person.Id), "customer.anonymised")); n != 1 {
		t.Errorf("customer.anonymised events = %d, want 1", n)
	}
	if got := fake.erasesSoFar(); len(got) != 1 {
		t.Errorf("erases = %v, want one", got)
	}
}

// Fifty a cycle (design D4): the fifty-first waits for the next.
func TestAnonymisationWorker_TakesAtMostFiftyACycle(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	for i := 0; i < 51; i++ {
		id := insertCustomer(t, h, fmt.Sprintf("Person %02d", i), "archived")
		h.Exec(t, `UPDATE customers.customers SET type = 'person', anonymise_on = $2 WHERE id = $1`, id, day(h, 0))
	}
	runAnonymisation(t, h)
	if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE anonymised_at IS NOT NULL`); n != 50 {
		t.Errorf("anonymised after one cycle = %d, want 50", n)
	}
	runAnonymisation(t, h)
	if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE anonymised_at IS NOT NULL`); n != 51 {
		t.Errorf("anonymised after two = %d, want 51", n)
	}
}

// The registry feed's lease tests, on this worker's key: a second replica skips
// rather than anonymising the same customers, and every cycle lets go.
func TestAnonymisationWorker_SkipsTheCycleWhenTheLeaseIsHeld(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	ctx := context.Background()
	holder, err := h.Pool().Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire the lease holder's connection: %v", err)
	}
	defer holder.Release()
	var locked bool
	if err := holder.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, customers.AnonymisationLeaseKeyForTest).Scan(&locked); err != nil || !locked {
		t.Fatalf("take the lease: %v, %v", locked, err)
	}
	w := customers.NewAnonymisationWorker(h.Deps())
	if ran, err := w.RunCycle(ctx); err != nil || ran {
		t.Errorf("RunCycle with the lease held = %v, %v; want false, nil", ran, err)
	}
	if _, err := holder.Exec(ctx, `SELECT pg_advisory_unlock($1)`, customers.AnonymisationLeaseKeyForTest); err != nil {
		t.Fatalf("release the lease: %v", err)
	}
	if ran, err := w.RunCycle(ctx); err != nil || !ran {
		t.Errorf("RunCycle after release = %v, %v; want true, nil", ran, err)
	}
}

func TestAnonymisationWorker_ReleasesTheLeaseAfterEveryCycle(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	w := customers.NewAnonymisationWorker(h.Deps())
	for i := 0; i < 3; i++ {
		if ran, err := w.RunCycle(context.Background()); err != nil || !ran {
			t.Fatalf("RunCycle %d = %v, %v: the previous cycle did not let go", i, ran, err)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND database = (SELECT oid FROM pg_database WHERE datname = current_database())`); n != 0 {
		t.Errorf("advisory locks still held on this database = %d, want 0", n)
	}
}
```

In `apps/server/internal/customers/module_test.go`'s `TestModule_ContributesItsWorkers`, make the cases:

```go
		{name: "the default installation runs all three", want: []string{"customers-anonymisation", "customers-peppol-recheck", "customers-registry-feed"}},
		{
			name: "the feed worker turned off leaves the other two",
			env:  map[string]string{"CUSTOMERS_REGISTRY_FEED_ENABLED": "0"},
			want: []string{"customers-anonymisation", "customers-peppol-recheck"},
		},
		{
			name: "the re-check worker turned off leaves the other two",
			env:  map[string]string{"CUSTOMERS_PEPPOL_RECHECK_ENABLED": "0"},
			want: []string{"customers-anonymisation", "customers-registry-feed"},
		},
		{
			// Design D6: the re-check worker is effective only alongside the
			// lookup it uses, and "not effective" means never started.
			name: "the Peppol lookup turned off takes the re-check worker with it",
			env:  map[string]string{"PEPPOL_LOOKUP_ENABLED": "0"},
			want: []string{"customers-anonymisation", "customers-registry-feed"},
		},
		{
			name: "the anonymisation worker turned off leaves the registry workers",
			env:  map[string]string{"CUSTOMERS_ANONYMISATION_ENABLED": "0"},
			want: []string{"customers-peppol-recheck", "customers-registry-feed"},
		},
		{
			name: "all three turned off leaves none",
			env: map[string]string{
				"CUSTOMERS_REGISTRY_FEED_ENABLED":  "0",
				"CUSTOMERS_PEPPOL_RECHECK_ENABLED": "0",
				"CUSTOMERS_ANONYMISATION_ENABLED":  "0",
			},
			want: nil,
		},
```

and append:

```go
// TestModule_TheAnonymisationCadenceIsConfigured pins that the third worker's
// runner-facing cadence is the operator's too.
func TestModule_TheAnonymisationCadenceIsConfigured(t *testing.T) {
	t.Parallel()
	if got := customers.NewAnonymisationWorker(newHarness(t).Deps()).Interval(); got != 24*time.Hour {
		t.Errorf("Interval = %v, want the 24h default", got)
	}
	tuned := newHarness(t, modtest.WithEnv("CUSTOMERS_ANONYMISATION_POLL", "1h"))
	if got := customers.NewAnonymisationWorker(tuned.Deps()).Interval(); got != time.Hour {
		t.Errorf("Interval = %v, want the configured 1h", got)
	}
}
```

In `apps/server/internal/config/config_test.go`, append:

```go
// TestLoad_CustomersAnonymisationWorker pins the anonymisation worker's two
// settings (customers GDPR design D4): on unless turned off — a date a person
// chose is kept whether or not the operator remembered a switch — and a daily
// cadence, since a schedule is a day.
func TestLoad_CustomersAnonymisationWorker(t *testing.T) {
	cfg := mustLoad(t, validEnv())
	if !cfg.CustomersAnonymisationEnabled {
		t.Error("CUSTOMERS_ANONYMISATION_ENABLED unset did not default to on")
	}
	if cfg.CustomersAnonymisationPoll != 24*time.Hour {
		t.Errorf("CustomersAnonymisationPoll = %v, want 24h", cfg.CustomersAnonymisationPoll)
	}
	tuned := mustLoad(t, with(validEnv(), "CUSTOMERS_ANONYMISATION_ENABLED", "0", "CUSTOMERS_ANONYMISATION_POLL", "1h"))
	if tuned.CustomersAnonymisationEnabled || tuned.CustomersAnonymisationPoll != time.Hour {
		t.Errorf("tuned = %v/%v, want off and 1h", tuned.CustomersAnonymisationEnabled, tuned.CustomersAnonymisationPoll)
	}
	if msg := loadError(t, with(validEnv(), "CUSTOMERS_ANONYMISATION_ENABLED", "yes")); !strings.Contains(msg, `CUSTOMERS_ANONYMISATION_ENABLED: must be "0" or "1"`) {
		t.Errorf("error = %q", msg)
	}
	for _, v := range []string{"0s", "soon"} {
		if msg := loadError(t, with(validEnv(), "CUSTOMERS_ANONYMISATION_POLL", v)); !strings.Contains(msg, "CUSTOMERS_ANONYMISATION_POLL: must be a positive duration such as 30s") {
			t.Errorf("%s: error = %q", v, msg)
		}
	}
}
```

Run them: `mise exec -- go test -count=1 -run 'Anonymisation|ContributesItsWorkers' ./internal/customers/ ./internal/config/` — FAIL to compile: `undefined: customers.NewAnonymisationWorker`, `cfg.CustomersAnonymisationEnabled undefined`.

- [ ] **Step 3: The configuration**

In `apps/server/internal/config/config.go`, add to `Config` directly after `CustomersPeppolRecheckAge time.Duration`:

```go
	// CustomersAnonymisationEnabled is the on/off switch for the customers
	// module's anonymisation worker (CUSTOMERS_ANONYMISATION_ENABLED,
	// customers GDPR design D4). Default ON, like the registry workers, and for
	// a stronger reason: the worker does what a person scheduled for a date, and
	// an installation that says nothing must still keep that promise. A 0 means
	// the worker is never handed to the runner, and scheduled dates simply wait.
	CustomersAnonymisationEnabled bool
	// CustomersAnonymisationPoll is how often that worker runs a cycle
	// (CUSTOMERS_ANONYMISATION_POLL, default 24 hours): a schedule is a day, so
	// once a day is on time. Each cycle is at most fifty customers, a constant.
	CustomersAnonymisationPoll time.Duration
```

directly after `customersRegistryWorkers(&p, env, c)` in `Load`, add `customersAnonymisation(&p, env, c)`, and after the `customersRegistryWorkers` function:

```go
// customersAnonymisation loads the anonymisation worker's settings (customers
// GDPR design D4): its switch, on by default, and its cadence. The batch — at
// most fifty customers a cycle — is a constant in the worker, not a knob: each
// customer is one transaction holding its row, and a cycle is meant to finish.
func customersAnonymisation(p *problems, env map[string]string, c *Config) {
	c.CustomersAnonymisationEnabled = boolean(p, env, "CUSTOMERS_ANONYMISATION_ENABLED", true)
	c.CustomersAnonymisationPoll = duration(p, env, "CUSTOMERS_ANONYMISATION_POLL", 24*time.Hour)
}
```

In `apps/server/cmd/vantigo/main_test.go`, add `"CUSTOMERS_ANONYMISATION_ENABLED": "0",` directly after each of the two `"CUSTOMERS_PEPPOL_RECHECK_ENABLED": "0",` lines (the second test asserts that no worker started at all, and the comment above the first already says why these tests start none).

In `deploy/compose/vantigo.env.example`, directly after `# CUSTOMERS_PEPPOL_RECHECK_AGE=720h`, add:

```text
#
# The anonymisation worker carries out the anonymisations people schedule for
# private-person customers (docs/customers.md, "Personal data and
# anonymisation"): once a day it takes every archived person whose day has come,
# at most fifty a cycle, and removes the person from the customer while keeping
# its number and the shape of its history. Turned off, scheduled dates wait.
# CUSTOMERS_ANONYMISATION_ENABLED=1
# CUSTOMERS_ANONYMISATION_POLL=24h
```

- [ ] **Step 4: The anonymisation**

Append to `apps/server/internal/customers/timeline_events.go`:

```go
// erasedKind is one entry of customer.anonymised's erased list.
type erasedKind struct {
	Kind  string `json:"kind"`
	Count int64  `json:"count"`
}

// recordCustomerAnonymised is the anonymisation's event (customers GDPR design
// D4), recorded after the timeline was rewritten and so the one entry of it
// that keeps its words: what was taken out, kind by kind — this module's four
// first, then each module's in Compose order. The actor is the system: the
// worker did it, on the day a person chose, and that person is on the
// customer.anonymisation_scheduled entry.
func recordCustomerAnonymised(ctx context.Context, q *store.Queries, now time.Time, customerID int32, erased []contracts.ErasedData, actorKind, actorDisplay string, actorUserID *uuid.UUID) error {
	kinds := make([]erasedKind, 0, len(erased))
	for _, e := range erased {
		kinds = append(kinds, erasedKind{Kind: e.Kind, Count: e.Count})
	}
	payload := map[string]any{"customerId": customerID, "erased": kinds}
	return recordGeneratedEvent(ctx, q, customerID, now, "customer.anonymised", "Customer anonymised", payload, 1, actorKind, actorDisplay, actorUserID)
}
```

Append to `apps/server/internal/customers/anonymisation.go` (adding `"slices"` and `"github.com/vantigo-io/vantigo/server/internal/contracts"` to its imports):

```go
// anonymisedText is what the person's words become (design D4), and
// anonymisedName what the customer is called afterwards.
const (
	anonymisedText = "[anonymised]"
	anonymisedName = "Anonymised person"
)

// The four kinds this module reports first in customer.anonymised.
const (
	anonymiseKindAddresses           = "customers.addresses"
	anonymiseKindContactAssociations = "customers.contactAssociations"
	anonymiseKindContacts            = "customers.contacts"
	anonymiseKindTimelineEntries     = "customers.timelineEntries"
)

// personalPayloadKeys are the top-level payload keys that carry the person
// (design D4), across every event this module writes: the customer's name
// (customer.created's customerName, a snapshot's name), its identity
// (identity, legalIdentity), its contact info and billing profile, every
// before/after/changes snapshot, the merge's absorbed block and its into, and a
// contact's name, title, phone and email (contacts_timeline.go), an address's
// label and one-line display. Every other key — customerId, the ids, dates,
// counts, statuses, roles — is kept, so an entry still says what kind of thing
// happened to which record when. Applied to every payload alike: a key in this
// list that holds nothing personal on some event (a status change's before) is
// taken all the same, rather than a per-event list that a new event type could
// slip past.
var personalPayloadKeys = []string{
	"name", "customerName", "identity", "legalIdentity", "contactInfo", "billingProfile",
	"before", "after", "changes", "absorbed", "into",
	"displayName", "firstName", "middleName", "lastName", "title", "phone", "email",
	"label", "display",
}

// keptSummaryEventTypes are the generated events whose summary is built from
// nothing about the person — a status, a type, a fixed sentence, a tag or group
// name, a staff member's name, a date — and so is kept (this plan's reading of
// design D4). Every other entry's summary, a manual one's and every generated
// one not listed, becomes anonymisedText: "Customer created: Kari Nordmann"
// would otherwise keep the name the payload beside it just lost. An allow-list,
// so an event type added later is anonymised until somebody decides otherwise.
var keptSummaryEventTypes = []string{
	"customer.status_changed", "customer.type_changed", "customer.contact_info_updated",
	"customer.billing_profile_updated", "customer.peppol_lookup", "customer.tags_changed",
	"customer.group_changed", "customer.owner_changed",
	"customer.anonymisation_scheduled", "customer.anonymisation_cancelled", "customer.anonymised",
}

// anonymisationWriteAttempts is how often one customer's anonymisation runs
// before a deadlock it keeps losing is logged as that customer's failure — the
// merge's three, for the merge's reason: the anonymisation takes the customer
// row first and then locks each contact it detached, while DELETE
// /customers/contacts/{id} takes the contact first and its customers after.
// The two can cycle, and the loser runs again from a fresh snapshot.
const anonymisationWriteAttempts = 3

// anonymiseCustomer is one due customer's whole anonymisation (design D4) in
// one transaction, retried on a deadlock, answering whether it anonymised
// anything: false when, under the lock, the customer turned out not to be due
// after all. The actor is the system's, verbatim — a worker has no principal,
// and resolving one would be a directory call for nothing.
func (s *server) anonymiseCustomer(ctx context.Context, id int32) (bool, error) {
	now := s.deps.Clock()
	var done bool
	err := db.RetrySerializable(ctx, anonymisationWriteAttempts, func() error {
		return db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
			var err error
			done, err = s.anonymiseInTx(ctx, tx, id, now)
			return err
		})
	})
	return done, err
}

// anonymiseInTx is one attempt, inside tx. The customer's lock comes first and
// its state is read again under it: a cancel, a restore or a change of type
// may have landed since the batch was selected. A merge chain is one person
// (design D4), so every customer merged into this one is locked too — after
// this customer, the rest in ascending id order, so the whole is not ascending:
// a merge naming this customer and its survivor locks the other way round, and
// the deadlock that can make is the one db.RetrySerializable retries — and
// anonymised in the same transaction — its own row, its
// own timeline, its own event; its customer.merged snapshot is on this
// customer's timeline, rewritten with the rest of it. A chain cannot grow while
// this customer is archived (a merge refuses an archived survivor), so reading
// it once under the first lock is enough. A customer that was merged away after
// it was scheduled takes its snapshot off its survivor's customer.merged entry,
// and nothing else of the survivor's: the survivor is locked for that write.
func (s *server) anonymiseInTx(ctx context.Context, tx pgx.Tx, id int32, now time.Time) (bool, error) {
	txq := store.New(tx)
	locked, err := txq.LockCustomer(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("lock customer %d: %w", id, err)
	}
	due := locked.AnonymiseOn.Valid && !locked.AnonymiseOn.Time.After(civilDate(now))
	if locked.AnonymisedAt != nil || !due || locked.Type != "person" || locked.Status != "archived" {
		return false, nil
	}

	chain, err := txq.CustomersMergedInto(ctx, id)
	if err != nil {
		return false, fmt.Errorf("read the customers merged into %d: %w", id, err)
	}
	others := slices.Clone(chain)
	if locked.MergedIntoCustomerID != nil {
		others = append(others, *locked.MergedIntoCustomerID)
	}
	slices.Sort(others)
	for _, other := range others {
		if _, err := txq.LockCustomer(ctx, other); err != nil {
			return false, fmt.Errorf("lock customer %d: %w", other, err)
		}
	}

	for _, member := range append([]int32{id}, chain...) {
		if err := s.anonymiseOne(ctx, tx, txq, member, locked.AnonymiseOn, now); err != nil {
			return false, err
		}
	}
	if locked.MergedIntoCustomerID != nil {
		snapshot := store.AnonymiseAbsorbedSnapshotsParams{Anonymised: anonymisedText, SurvivorID: *locked.MergedIntoCustomerID, AbsorbedID: id}
		if err := txq.AnonymiseAbsorbedSnapshots(ctx, snapshot); err != nil {
			return false, fmt.Errorf("take customer %d's snapshot off its survivor: %w", id, err)
		}
		if err := txq.AnonymiseAbsorbedSnapshotRevisions(ctx, store.AnonymiseAbsorbedSnapshotRevisionsParams(snapshot)); err != nil {
			return false, fmt.Errorf("take customer %d's snapshot off its survivor's revisions: %w", id, err)
		}
	}
	return true, nil
}

// anonymiseOne is design D4's list for one customer, in the order the
// constraints and the event want: what hangs off the row (addresses, the
// Peppol answer, the registry record a person never has but a retyped business
// might), the contacts (associations detached, the contacts they alone held
// deleted), the timeline and its revisions rewritten, every module's eraser in
// Compose order on this transaction, then the row — cleared, marked, its
// revision advanced — and last the event, which is recorded after the rewrite
// and so keeps its words. on is the day the anonymisation was scheduled for:
// a customer merged into the one scheduled takes that day, whatever its own
// schedule said.
func (s *server) anonymiseOne(ctx context.Context, tx pgx.Tx, txq *store.Queries, id int32, on pgtype.Date, now time.Time) error {
	addresses, err := txq.DeleteAllCustomerAddresses(ctx, id)
	if err != nil {
		return fmt.Errorf("delete customer %d's addresses: %w", id, err)
	}
	if err := txq.DeleteCustomerPeppolLookup(ctx, id); err != nil {
		return fmt.Errorf("delete customer %d's Peppol answer: %w", id, err)
	}
	if err := txq.DeleteCustomerRegistryRecord(ctx, id); err != nil {
		return fmt.Errorf("delete customer %d's registry record: %w", id, err)
	}
	contactIDs, err := txq.DetachCustomerContacts(ctx, id)
	if err != nil {
		return fmt.Errorf("detach customer %d's contacts: %w", id, err)
	}
	var orphans int64
	if len(contactIDs) > 0 {
		if err := txq.LockContactsForAnonymisation(ctx, contactIDs); err != nil {
			return fmt.Errorf("lock customer %d's detached contacts: %w", id, err)
		}
		if orphans, err = txq.DeleteOrphanedContacts(ctx, contactIDs); err != nil {
			return fmt.Errorf("delete the contacts only customer %d had: %w", id, err)
		}
	}
	rewrite := store.AnonymiseTimelineEntriesParams{
		CustomerID: id, Anonymised: anonymisedText, PersonalKeys: personalPayloadKeys, KeptSummaryTypes: keptSummaryEventTypes,
	}
	entries, err := txq.AnonymiseTimelineEntries(ctx, rewrite)
	if err != nil {
		return fmt.Errorf("rewrite customer %d's timeline: %w", id, err)
	}
	if err := txq.AnonymiseTimelineRevisions(ctx, store.AnonymiseTimelineRevisionsParams(rewrite)); err != nil {
		return fmt.Errorf("rewrite customer %d's timeline revisions: %w", id, err)
	}

	erased := []contracts.ErasedData{
		{Kind: anonymiseKindAddresses, Count: addresses},
		{Kind: anonymiseKindContactAssociations, Count: int64(len(contactIDs))},
		{Kind: anonymiseKindContacts, Count: orphans},
		{Kind: anonymiseKindTimelineEntries, Count: entries},
	}
	for _, holder := range s.deps.CustomerPersonalData {
		kinds, err := holder.Data.EraseCustomerData(ctx, tx, id)
		if err != nil {
			return fmt.Errorf("erase %s's data about customer %d: %w", holder.Module, id, err)
		}
		erased = append(erased, kinds...)
	}

	if err := txq.AnonymiseCustomerRow(ctx, store.AnonymiseCustomerRowParams{ID: id, Name: anonymisedName, AnonymiseOn: on, Now: now}); err != nil {
		return fmt.Errorf("clear customer %d's row: %w", id, err)
	}
	system := generatedFallbackActor
	return recordCustomerAnonymised(ctx, txq, now, id, erased, system.Kind, system.Display, system.UserID)
}
```

If sqlc generated the entries and revisions parameter structs with their fields in different orders, the `store.AnonymiseTimelineRevisionsParams(rewrite)` conversion does not compile; build the second struct field by field instead (the same for the two absorbed-snapshot structs).

- [ ] **Step 5: The worker**

Create `apps/server/internal/customers/anonymisation_worker.go`:

```go
package customers

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vantigo-io/vantigo/server/internal/customers/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/worker"
)

// This file is the anonymisation worker (customers GDPR design D4): once a
// cycle it takes every private person whose scheduled day has come — archived,
// not yet anonymised — and runs each one's anonymisation (anonymisation.go) in
// a transaction of its own. It decides nothing: the day is a person's choice,
// made through PUT /customers/{id}/anonymisation, and the worker only keeps it.
//
// One customer failing — another module's eraser, a deadlock it kept losing —
// rolls that customer back and is logged, and the cycle moves on to the next:
// a cycle that stopped at the first failure would hold every later customer
// hostage to one module's bad day. The failed one is due again next cycle.

const (
	// anonymisationWorkerName is what the runner logs this worker as, in the
	// <module>-<worker> spelling of the two beside it.
	anonymisationWorkerName = "customers-anonymisation"

	// anonymisationLeaseKey is the ASCII string "CUSTANO1" read as a big-endian
	// 64-bit value — its own key, not the registry workers': they do unrelated
	// work, and one holding another's lease would silently stall it.
	anonymisationLeaseKey int64 = 0x43555354414E4F31

	// anonymisationBatch bounds one cycle at fifty customers (design D4). Each
	// is a transaction holding its row and every module's part; fifty keeps a
	// cycle short, and a backlog drains a day at a time.
	anonymisationBatch = 50

	// defaultAnonymisationPoll is what a worker built from a Deps with no
	// Config falls back on — config.go's own default for
	// CUSTOMERS_ANONYMISATION_POLL.
	defaultAnonymisationPoll = 24 * time.Hour
)

// AnonymisationWorker anonymises the private persons whose scheduled day has
// come. It implements worker.Worker.
type AnonymisationWorker struct {
	deps module.Deps
	srv  *server
}

var _ worker.Worker = (*AnonymisationWorker)(nil)

// NewAnonymisationWorker builds the worker over d. d.CustomerPersonalData is
// what module.Workers collected from every module given — in worker mode
// nothing composes, so that is the only place it comes from.
func NewAnonymisationWorker(d module.Deps) *AnonymisationWorker {
	return &AnonymisationWorker{deps: d, srv: newServer(d)}
}

// Name identifies this worker in the runner's logs.
func (w *AnonymisationWorker) Name() string { return anonymisationWorkerName }

// Interval is the poll cadence between cycles (CUSTOMERS_ANONYMISATION_POLL,
// default 24 hours).
func (w *AnonymisationWorker) Interval() time.Duration {
	if w.deps.Config != nil && w.deps.Config.CustomersAnonymisationPoll > 0 {
		return w.deps.Config.CustomersAnonymisationPoll
	}
	return defaultAnonymisationPoll
}

// Run is the worker loop, the registry workers' shape.
func (w *AnonymisationWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.Interval())
	defer ticker.Stop()
	for {
		if _, err := w.RunCycle(ctx); err != nil && ctx.Err() == nil {
			// Only the lease or the batch's SELECT fails a cycle, and neither
			// takes anything personal as an argument; one customer's failure is
			// logged, by id, inside the cycle.
			w.logger().Error("anonymisation cycle failed", "worker", anonymisationWorkerName, "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// RunCycle is one cycle under the advisory lease, reporting whether it ran:
// the due customers, at most anonymisationBatch, oldest day first, each in its
// own transaction.
func (w *AnonymisationWorker) RunCycle(ctx context.Context) (bool, error) {
	return w.underLease(ctx, func(ctx context.Context) error {
		today := pgtype.Date{Time: civilDate(w.now()), Valid: true}
		due, err := store.New(w.deps.Pool).DueAnonymisations(ctx, store.DueAnonymisationsParams{Today: today, RowLimit: anonymisationBatch})
		if err != nil {
			return fmt.Errorf("customers: select the customers due for anonymisation: %w", err)
		}
		anonymised, failed := 0, 0
		for _, id := range due {
			if ctx.Err() != nil {
				return nil
			}
			done, err := w.srv.anonymiseCustomer(ctx, id)
			if err != nil {
				if ctx.Err() == nil {
					w.logger().Error("customers: anonymising a customer failed; it is tried again next cycle",
						"worker", anonymisationWorkerName, "customerId", id, "error", err.Error())
				}
				failed++
				continue
			}
			if done {
				anonymised++
			}
		}
		w.logger().Info("anonymisation cycle finished", "worker", anonymisationWorkerName, "anonymised", anonymised, "failed", failed)
		return nil
	})
}

// underLease is the registry workers' lease (peppol_recheck_worker.go, and
// communications/retention.go for why the unlock runs on a context stripped of
// cancellation and why a failed unlock discards the connection), on this
// worker's own key.
func (w *AnonymisationWorker) underLease(ctx context.Context, action func(context.Context) error) (bool, error) {
	conn, err := w.deps.Pool.Acquire(ctx)
	if err != nil {
		return false, fmt.Errorf("customers: acquire a connection for the anonymisation lease: %w", err)
	}
	defer conn.Release()

	var acquired bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, anonymisationLeaseKey).Scan(&acquired); err != nil {
		return false, fmt.Errorf("customers: take the anonymisation lease: %w", err)
	}
	if !acquired {
		w.logger().Debug("the customers anonymisation lease is held by another replica; skipping this cycle",
			"worker", anonymisationWorkerName)
		return false, nil
	}
	defer func() {
		release := context.WithoutCancel(ctx)
		if _, err := conn.Exec(release, `SELECT pg_advisory_unlock($1)`, anonymisationLeaseKey); err != nil {
			w.logger().Error("releasing the anonymisation lease failed; discarding the connection",
				"worker", anonymisationWorkerName, "error", err)
			_ = conn.Conn().Close(release)
		}
	}()
	return true, action(ctx)
}

// now is the worker's clock, so tests control time exactly as they do for the
// handlers.
func (w *AnonymisationWorker) now() time.Time {
	if w.deps.Clock != nil {
		return w.deps.Clock().UTC()
	}
	return time.Now().UTC()
}

func (w *AnonymisationWorker) logger() *slog.Logger {
	if w.deps.Logger != nil {
		return w.deps.Logger
	}
	return slog.Default()
}
```

In `apps/server/internal/customers/module.go`'s `workers`, add after the re-check worker's `if`:

```go
	if d.Config.CustomersAnonymisationEnabled {
		out = append(out, NewAnonymisationWorker(d))
	}
```

and extend its doc comment: after `…the Brreg update-feed worker and the Peppol re-check worker.` add ` And, since customers GDPR design D4, the anonymisation worker, which keeps the days people schedule; it asks no network, so it has one condition, its own switch.`; change `Both workers take an advisory lease` to `All three take an advisory lease`.

- [ ] **Step 6: Customers and projects, composed for real**

Create `apps/server/internal/integration/personal_data_test.go`:

```go
package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/customers"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// TestPersonalData_TheRealProjectsModuleIsAskedAndKeepsItsProjects is customers
// GDPR design D2 end to end, against the other side itself: the export reads
// the real projects module's section through the slot Compose filled, and the
// worker — found through module.Workers, which collects the same slot in worker
// mode — asks the real projects module to erase and records that it did, while
// the project itself stays, naming the anonymised customer. Only projects is
// composed beside customers, for the merge test's reason: each module proves
// its own SQL in its own package, and what only this package can prove is that
// a real anonymisation reaches a real implementation at all.
func TestPersonalData_TheRealProjectsModuleIsAskedAndKeepsItsProjects(t *testing.T) {
	t.Parallel()
	h := newInstallation(t, modCustomers, modProjects)
	admin, _ := h.SignInUser(t,
		"customers:view", "customers:create", "customers:delete", "customers:personal-data", "customers:timeline-view",
		"projects:access", "projects:create", "projects:manage-all",
	)

	type created struct {
		Id int32 `json:"id"`
	}
	var person, project created
	okJSON(t, admin, http.MethodPost, "/api/v1/customers", map[string]any{"name": "Kari Nordmann", "type": "person"}, &person)
	okJSON(t, admin, http.MethodPost, projectsPath, map[string]any{
		"code": "KARI1000", "name": "Varmepumpe", "customerId": person.Id,
		"billingType": "time-and-materials", "currency": "NOK",
	}, &project)

	var file struct {
		Modules map[string]json.RawMessage `json:"modules"`
	}
	okJSON(t, admin, http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/personal-data", person.Id), nil, &file)
	var section struct {
		Projects []struct {
			Code string `json:"code"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(file.Modules["projects"], &section); err != nil || len(section.Projects) != 1 || section.Projects[0].Code != "KARI1000" {
		t.Fatalf("modules.projects = %s, want the real module's one project", file.Modules["projects"])
	}

	if r := admin.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d", person.Id), nil); r.Status != http.StatusNoContent {
		t.Fatalf("archive: status %d body %s", r.Status, r.Body)
	}
	okJSON(t, admin, http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/anonymisation", person.Id),
		map[string]any{"anonymiseOn": h.Now().UTC().Format("2006-01-02")}, nil)

	var worker *customers.AnonymisationWorker
	for _, w := range module.Workers(h.Deps(), customers.Module(), moduleNamed(t, modProjects)) {
		if aw, ok := w.(*customers.AnonymisationWorker); ok {
			worker = aw
		}
	}
	if worker == nil {
		t.Fatal("module.Workers built no anonymisation worker")
	}
	if ran, err := worker.RunCycle(context.Background()); err != nil || !ran {
		t.Fatalf("RunCycle = %v, %v", ran, err)
	}

	var timeline struct {
		Data []struct {
			EventType string          `json:"eventType"`
			Payload   json.RawMessage `json:"payload"`
		} `json:"data"`
	}
	okJSON(t, admin, http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/timeline", person.Id), nil, &timeline)
	var payload struct {
		Erased []struct {
			Kind  string `json:"kind"`
			Count int64  `json:"count"`
		} `json:"erased"`
	}
	if len(timeline.Data) == 0 || timeline.Data[0].EventType != "customer.anonymised" || json.Unmarshal(timeline.Data[0].Payload, &payload) != nil {
		t.Fatalf("newest entry = %+v, want customer.anonymised", timeline.Data)
	}
	asked := false
	for _, e := range payload.Erased {
		asked = asked || (e.Kind == "projects.projects" && e.Count == 0)
	}
	if !asked {
		t.Errorf("erased = %+v, want projects.projects asked and keeping its rows", payload.Erased)
	}

	var got struct {
		CustomerId   *int32  `json:"customerId"`
		CustomerName *string `json:"customerName"`
	}
	okJSON(t, admin, http.MethodGet, fmt.Sprintf("%s/%d", projectsPath, project.Id), nil, &got)
	if got.CustomerId == nil || *got.CustomerId != person.Id || got.CustomerName == nil || *got.CustomerName != "Anonymised person" {
		t.Errorf("the project names %v %v, want the anonymised customer %d", got.CustomerId, got.CustomerName, person.Id)
	}
}
```

`h.Deps()` is the harness's base `Deps`, exactly what `cmd/vantigo` hands `module.Workers` in worker mode; `moduleNamed` is the harness's own mapping.

- [ ] **Step 7: Run everything, show it can fail, commit**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'
mise exec -- gofmt -l internal cmd && mise exec -- go vet ./... && mise exec -- go build ./...
mise exec -- go test -count=1 ./internal/customers/... ./internal/config/... ./internal/integration/... ./cmd/vantigo/...
```
Expected: PASS — the cmd tests included (no worker started there: the new switch is off in both envs).

Prove the tests can fail, restoring after each: remove `"customerName"` from `personalPayloadKeys` — the matrix test's customer.created assertion and its traces check go red; add `"customer.created"` to `keptSummaryEventTypes` — the traces check goes red on the summary; drop `AnonymiseTimelineRevisions` — the revisions' traces check goes red; call the erasers after `AnonymiseCustomerRow` — the eraser's `sawName` goes red; make `anonymiseInTx` skip the chain (`chain = nil`) — the chain test goes red; delete the `LIMIT` from `DueAnonymisations` — the fifty test goes red; return the eraser error from `RunCycle` instead of logging — the failing-module test goes red on the second customer; remove the `CUSTOMERS_ANONYMISATION_ENABLED` switch check in `workers` — the module test's off case goes red. Say what each printed.

```bash
cd /home/anders/projects/vantigo/vantigo
cat > /tmp/claude-1000/msg-gdpr-6.txt <<'EOF'
feat(customers): a scheduled private person is anonymised on their day, and the bookkeeping stays

The customers-anonymisation worker (CUSTOMERS_ANONYMISATION_ENABLED, on
by default; _POLL, 24h; its own advisory lease) takes every archived
person whose day has come, at most fifty a cycle, each in one retried
transaction: the name becomes "Anonymised person" with the number kept,
the identity, contact info and billing identifiers are cleared and the
terms kept, addresses, the Peppol answer and orphan contacts go,
associations are detached, and every timeline entry and revision keeps
its date, type and ids while losing the person — set-based, in SQL. A
merge chain is one person, every module's eraser runs inside the
transaction, and customer.anonymised is recorded last. A failing module
rolls that customer back; the cycle logs it and moves on.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
PATHS="apps/server/internal/customers/queries/personal_data.sql apps/server/internal/customers/store/personal_data.sql.go \
 apps/server/internal/customers/anonymisation.go apps/server/internal/customers/anonymisation_worker.go \
 apps/server/internal/customers/anonymisation_worker_test.go apps/server/internal/customers/timeline_events.go \
 apps/server/internal/customers/module.go apps/server/internal/customers/module_test.go \
 apps/server/internal/customers/export_test.go apps/server/internal/config/config.go \
 apps/server/internal/config/config_test.go apps/server/cmd/vantigo/main_test.go \
 deploy/compose/vantigo.env.example apps/server/internal/integration/personal_data_test.go"
git add $PATHS && git commit -F /tmp/claude-1000/msg-gdpr-6.txt -- $PATHS
git show --stat HEAD && git status --short
```

---

### Task 7: The docs (D6)

**Files:**
- Modify: `docs/customers.md`, `ROADMAP.md`
- Read first (do not change): the spec's D1–D6 and "Out of scope"; `docs/customers.md` sections `## Statuses`, `## Merging duplicates`, `## Permissions`, `## The frontend`, `## API`, `## What comes next`; `ROADMAP.md` phase 6

- [ ] **Step 1: The section**

In `docs/customers.md`, directly before `## Brreg lookup`, add:

```markdown
## Personal data and anonymisation

A **private person**'s data can be handed to them in one file, and taken out of the
customer on a day somebody chose (phase 6 delivery C, decided in
[`docs/superpowers/specs/2026-09-24-customers-gdpr-design.md`](superpowers/specs/2026-09-24-customers-gdpr-design.md)).
The customer row stays — its number, its place in projects and supply periods, the
shape of its history — and the person disappears from it. A business is not a data
subject: both operations refuse one with 409 `personal_data_not_a_person`, and a
business's contacts are people handled through customers of their own, if they are
customers at all. Both need `customers:personal-data` and `customers:view`. The
[legal identity rule](#legal-identity-and-its-validation) that refuses a Norwegian
national identity number is the same delivery's.

### The export

`GET /customers/{id}/personal-data` answers everything held about the person as a
JSON attachment, `customer-<number>-personal-data.json`, never cached — built in
memory and streamed from the request, the [CSV export](#export)'s shape:

| Part | What it holds |
| --- | --- |
| `customer` | number, name, type, status, legal identity, contact info, addresses, the billing profile's own stored values (not the resolved profile), owner, group, tags, `mergedInto`, `anonymisation` |
| `contacts` | every contact linked to the customer as the contact is stored, with the association's title, phone, email and roles |
| `timeline` | every entry, oldest first, deleted ones included (their `state` says so), each with its payload, actor and follow-up; revisions are not in the file |
| `modules` | each other module's section under its name — `communications` (the person's conversations: subject, dates, each message's direction, date and text body, attachment names), `energy` (supply periods with the metering point's address), `projects` (code, name, status, dates); a module holding nothing for the customer has no key |

It is shaped by nothing but `customers:personal-data`: that key means "may hand this
person their data", so the legal identity and the contacts are in the file without
`customers:legal-identity-view` or `customers:contacts-view`. This module's parts are
read in one read-only snapshot; each module's section is its own
`contracts.CustomerPersonalData.ExportCustomerData`
([module boundaries rule 9](module-boundaries.md#the-rules)), called outside any
transaction of this module's. An anonymised customer's file is what is left.

### Scheduling

`PUT /customers/{id}/anonymisation` with `{anonymiseOn}` — a UTC calendar day,
`yyyy-MM-dd`, today or later — puts the day on the customer and records
`customer.anonymisation_scheduled` (`{customerId, anonymiseOn, previousAnonymiseOn?}`,
attributed to the caller: the worker that acts on it later is only the system, so this
entry is where the decision's author is on record). The customer must be a person and
**archived** — 409 `personal_data_customer_active` otherwise: an ongoing relationship is
not anonymised out from under itself, so it is ended first, deliberately. The day
already scheduled writes nothing; another day moves it. `DELETE …/anonymisation` calls it
off and records `customer.anonymisation_cancelled`; with nothing scheduled it writes
nothing. Both answer the customer, whose `anonymisation` is `{anonymiseOn,
anonymisedAt?}` from then on (absent when nothing is scheduled).

**There is no default day, and the field says why it exists.** Norwegian bookkeeping
rules ([bokføringsloven](https://lovdata.no/dokument/NL/lov/2004-11-19-73)) keep
accounting material for years after the end of the fiscal year, and the person
scheduling is the one who knows what was invoiced to this customer and when; this
installation invoices nothing yet, and no number is encoded here. Choose a day after
every retention period that applies to this customer has passed.

What takes a customer out of what may be anonymised calls its schedule off in the same
transaction, recorded as a cancellation: restoring it (`PUT /customers/{id}` or a CSV
row with a status other than archived) or changing its type away from person. Left in
place, a day would fire the night the customer was archived again. A merged-away
customer cannot be scheduled (409 `customer_merged` — schedule its survivor), but a
schedule made before its merge can still be called off: cancelling is the one write it
takes.

### What the worker does

The `customers-anonymisation` worker takes every archived private person whose day has
come (UTC), at most fifty a cycle, oldest day first, each in **one transaction** with
the customer row locked, retried on a deadlock:

| | |
| --- | --- |
| The row | `name` → "Anonymised person"; the **customer number stays** — it is the bookkeeping reference. The legal identity, contact info (email, phone, website) and the billing profile's identifiers (`invoiceEmail`, `reminderEmail`, `peppolId`, `gln`, `buyerReference`) are cleared; the payment terms, currency, language, delivery methods and default bill rate stay — they say how the customer was invoiced, not who it was. Owner and tags stay: staff and vocabulary. The customer leaves its group, as a merged-away one does: it refuses every write, the group PUT included, so a group it still counted in could never be deleted. Status stays archived. |
| Addresses, Peppol answer, registry record | Deleted. |
| Contacts | Every association detached, its roles with it; a contact linked to no other customer afterwards is deleted — it existed for this person alone. One another customer still links stays, theirs too. |
| Timeline | Every entry **stays**, dated and typed, with its content anonymised: a manual entry's summary and note become "[anonymised]" and its source URL goes; a generated entry's summary does too, unless its type's summary is built from nothing personal (a status or type change, a fixed sentence, a tag, group or owner change, the scheduling events); in every payload the keys that carry the person — `name`, `customerName`, `identity`, `legalIdentity`, `contactInfo`, `billingProfile`, `before`, `after`, `changes`, `absorbed`, `into`, and a contact's or address's `displayName`, `firstName`, `middleName`, `lastName`, `title`, `phone`, `email`, `label`, `display` — become "[anonymised]", and the rest (`customerId`, ids, dates, counts, statuses) is kept. Revisions the same. Follow-ups keep their day and assignee. The author of each entry (`actorDisplay`) is staff, and stays. One set-based statement per table, in SQL. |
| Other modules | Each `contracts.CustomerPersonalData.EraseCustomerData`, inside the same transaction: communications deletes the person's conversations, every message and the rows under it through the retention worker's own deletes, their objects queued on the cleanup ledger, and clears a suggestion or candidate row naming them elsewhere; energy keeps the supply periods (a period is the metering point's history, the address the point's); projects keeps the projects (invoiced work stays, no customer name is stored there). |
| Last | `anonymised_at` is set, the revision advances, and `customer.anonymised` is recorded — after the rewrite, so the one event that keeps its words: `{customerId, erased: [{kind, count}]}`, this module's four kinds first (`customers.addresses`, `customers.contactAssociations`, `customers.contacts`, `customers.timelineEntries`) and then each module's, a module that kept everything listed at zero; the actor is the system. |

A failing module rolls that customer's whole run back; the worker logs it, by id, moves
on to the next and tries it again next cycle.

**A merge chain is one person.** Customers merged into the one that is due are
anonymised in the same transaction, each with its own row, its own `customer.anonymised`
and the chain's day as its `anonymiseOn`; the `customer.merged` entries describing them
are on the due customer's timeline and go with the rest of it. A customer scheduled and
then merged away is anonymised on its day as itself, and its snapshot comes off its
survivor's `customer.merged` entry (the `absorbed` block and the summary naming it);
nothing else of the survivor's changes — the history that moved to it with the merge is
the survivor's now, and is anonymised with the survivor.

Not built, on purpose: business customers' data; deleting the customer row; a
law-derived default day; anonymising staff users (identity's concern); a bulk
"anonymise everyone archived before X"; rewriting other modules' free text (a project
named after the person); encryption at rest.

### An anonymised customer is read-only

Every write to it answers **409 `customer_anonymised`**, "Customer was anonymised",
its detail naming the day — through the same lock-time check that answers
`customer_merged` ([A merged-away customer is read-only](#a-merged-away-customer-is-read-only)),
so the list of writes is that one, its schedule's two included. A CSV row naming its
number is refused on `customerNumber`; a merge will not absorb it; `DELETE
/customers/{id}` is the archived no-op. It stays archived, and its export still answers.

**Configuration**, read once at startup by `internal/config`:

| Variable | Default | |
| --- | --- | --- |
| `CUSTOMERS_ANONYMISATION_ENABLED` | `1` | `0` → the worker is never handed to the runner, and scheduled days wait |
| `CUSTOMERS_ANONYMISATION_POLL` | `24h` | how often a cycle runs; a schedule is a day, so once a day is on time |

The batch of fifty is a constant, not a knob. The worker takes an advisory lease of its
own, so one replica at a time runs a cycle.
```

- [ ] **Step 2: The rest of customers.md**

In `## Statuses`, after the merged-away bullet, add:

```markdown
- **An anonymised customer is archived too** ([Personal data and
  anonymisation](#personal-data-and-anonymisation)): a private person whose scheduled
  day came, its response carrying `anonymisation.anonymisedAt`. It cannot be restored —
  every write answers 409 `customer_anonymised` — and restoring a customer that is only
  *scheduled* calls the schedule off.
```

In `### The refusals, in order` (Merging duplicates), add after the `merge_into_archived` row:

```markdown
| 409 `customer_anonymised` | the absorbed customer was anonymised — it takes no more writes ([Personal data and anonymisation](#personal-data-and-anonymisation)) |
```

In `### One transaction, every module`'s holder table, append to the energy row: `Its personal data (rule 9) is the supply periods with the metering point's address, handed over in the export and kept by an anonymisation.`

In `## Permissions`: `Fifteen keys` → `Sixteen keys`; add the row

```markdown
| `customers:personal-data` | Customers | Hand a private person all the data held about them, and schedule the anonymisation of an archived private person. | yes |
```

and after the `customers:merge` paragraph:

```markdown
`customers:personal-data` is the third ([Personal data and
anonymisation](#personal-data-and-anonymisation)): handing a person their whole file
reads what `customers:legal-identity-view` and `customers:contacts-view` each guard, and
an anonymisation removes more than any delete, so it is its own sensitive key, implied
by none of the others.
```

In `## API`: `60 operations in total` → `63 operations in total`; add after the merge row:

```markdown
| `GET /{id}/personal-data` | `customers:personal-data` + `customers:view` |
| `PUT /{id}/anonymisation`, `DELETE /{id}/anonymisation` | `customers:personal-data` + `customers:view` |
```

In `## The frontend`, after the **Merge…** bullet, add:

```markdown
- **Personal data** on a private person's page header, for a caller the host says may
  manage it (a new `canManagePersonalData` prop, read from `customers:personal-data`): a
  menu with **Export personal data** — the JSON file, downloaded the customers file's way
  (a plain `fetch`, the server's filename, an object URL) — and **Schedule
  anonymisation…**, a date picker that starts at today (UTC) with the retention reminder
  and the irreversibility said before the button, enabled only for an archived customer,
  the reason shown under it otherwise; once scheduled, **Change anonymisation date…** and
  **Cancel anonymisation** (through the shared confirm modal). A scheduled customer's page
  shows a yellow banner with the day; an anonymised one's shows "Anonymised on …" and, the
  merged-away banner's way, no edit action anywhere — the header hides Edit, Change type,
  Restore and Merge, and every card gets `canX && !readOnly`. The timeline labels the three
  new events and renders "[anonymised]" content as the plain text it is; an absorbed
  snapshot that was anonymised shows no details list. A write refused
  `customer_anonymised` says so in the reader's language, as `customer_merged` does. A
  person's legal identity is never entered on a form in this package, so the national
  identity refusal shows where such an id can come in — the CSV import's errors table, on
  `legalId`.
```

In `## What comes next`, replace `Still ahead in phase 6: GDPR handling for person customers (delivery C).` with `Delivery C followed it (below).`, and after that paragraph add:

```markdown
**Phase 6 delivery C** — [Personal data and anonymisation](#personal-data-and-anonymisation)
— has landed, decided in
[`docs/superpowers/specs/2026-09-24-customers-gdpr-design.md`](superpowers/specs/2026-09-24-customers-gdpr-design.md):
a Norwegian national identity number is refused as a person's legal id; a private person's
data is handed over in one file; and an archived private person is anonymised on a day a
person chose — the number and the shape of the history kept, the person taken out, every
module's part in the same transaction. It added one permission key
(`customers:personal-data`), one migration (`00030`), three operations, three event types,
one worker (`customers-anonymisation`), one refusal code (`customer_anonymised`) and the
second cross-module direction, `contracts.CustomerPersonalData`, which communications,
energy and projects implement. It is **the roadmap's last delivery**: phase 6, and the
Customers roadmap with it, is complete.
```

and in the closing paragraph replace `` `ContactsByEmail` still unused in production, no GDPR handling (phase 6 delivery
C) — itself drawn from `` with `` `ContactsByEmail` still unused in production, other modules writing to the customer
timeline (on the outbox, deferred until Orders), invoiced revenue once Invoices exists —
itself drawn from ``.

- [ ] **Step 3: The roadmap**

In `ROADMAP.md`, the heading `### Phase 6 — Data operations and compliance` becomes `### Phase 6 — Data operations and compliance (done)`, and `**Still ahead in this phase:** GDPR handling for person customers (delivery C).` becomes:

```markdown
**Delivery C (done)** — decided in
[`docs/superpowers/specs/2026-09-24-customers-gdpr-design.md`](docs/superpowers/specs/2026-09-24-customers-gdpr-design.md):
a Norwegian national identity number is refused as a person's legal id; a private
person's data is exported in one file; and an archived private person is anonymised on a
chosen day by a worker — the number, the dates and the shape of the history kept for
bookkeeping, the person taken out of the customer, its timeline and other modules
through `contracts.CustomerPersonalData` (communications, energy, projects); behind the
new `customers:personal-data`. See
[`docs/customers.md#personal-data-and-anonymisation`](docs/customers.md#personal-data-and-anonymisation).

Phase 6 is complete, and with it the Customers roadmap. Still deferred, each waiting on
another module: attachments on the timeline, and the other modules' timeline writers,
which need the outbox (Orders).
```

- [ ] **Step 4: Check the docs against the code, commit**

```bash
cd /home/anders/projects/vantigo/vantigo
grep -n 'personal-data-and-anonymisation\|module-boundaries.md#the-rules' docs/customers.md docs/module-boundaries.md docs/communications.md docs/projects.md ROADMAP.md
grep -n '^## Personal data and anonymisation' docs/customers.md
```

Read every sentence of the new section against the code it describes: the key lists against `personalPayloadKeys`/`keptSummaryEventTypes` in `anonymisation.go`, the cleared columns against `AnonymiseCustomerRow`, the kinds against the constants and each module's `EraseCustomerData`, the refusal titles and codes against `merge.go`/`anonymisation.go`/`personal_data.go`, the defaults against `config.go`, the operation count against `grep -c 'operationId:' openapi/customers.yaml` (63). Fix the docs, not the code, where they disagree — unless the code contradicts the spec, in which case stop and say so.

```bash
cat > /tmp/claude-1000/msg-gdpr-7.txt <<'EOF'
docs(customers): personal data and anonymisation, and phase 6 delivery C

docs/customers.md gains Personal data and anonymisation — the export's
contents and why one key shapes it, the scheduling rules and why there
is no default day, exactly what the worker clears, keeps and rewrites
and why the number and dates stay, merge chains, the read-only rule and
the worker's configuration — plus the identity rule's cross-reference,
the statuses, the merge refusal, the permission, the API list, the
frontend and the phase 6 delivery C paragraph. ROADMAP.md marks phase 6
complete.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
PATHS="docs/customers.md ROADMAP.md"
git add $PATHS && git commit -F /tmp/claude-1000/msg-gdpr-7.txt -- $PATHS
git show --stat HEAD && git status --short
```

---

### Task 8: The frontend (D5)

**Files:**
- Create: `apps/customers/frontend/src/api/personal-data.ts`, `api/personal-data.test.ts`, `pages/-customer-personal-data.tsx`, `pages/-customer-personal-data.test.tsx`
- Modify: `apps/customers/frontend/src/api/customers.ts`, `api/customers.test.ts`, `api/merge.test.ts`, `api/import-export.ts`, `lib/customer-write-error.ts`, `lib/customer-write-error.test.ts`, `pages/customers.$customerId.tsx`, `pages/-customer-timeline.tsx`, `pages/-customer-timeline.test.tsx`, `pages/-customer-form-modal.tsx`, `pages/-customer-form-modal.test.tsx`, `pages/-customer-merge-modal.tsx`, `pages/-customer-import-modal.test.tsx`, `i18n.ts`; `apps/host/frontend/src/routes/customers/-customer-detail-layout.tsx`, `routes/customers/customer-detail-route.test.tsx`, `apps/host/frontend/src/catalogs/admin.ts`, `catalogs/admin.test.ts`
- Read first (do not change): `pages/-customer-merge-header.test.tsx:1-140` (the header under a router), `pages/-customer-merge-modal.tsx` (refusal keys, the revision sync), `pages/-customer-timeline.tsx:60-305,540-610`, `api/import-export.ts`, `pages/-customer-import-modal.test.tsx:1-120`

**Interfaces:**
- Consumes: the three operations and `SafeCustomerResponse.anonymisation` (Tasks 4, 5), the event types (Tasks 5, 6), the refusal codes.
- Produces TS: `CustomerAnonymisation {anonymiseOn: string; anonymisedAt: string | null}`, `CustomerResponse.anonymisation: CustomerAnonymisation | null`; `downloadFile` (exported from `import-export.ts`, the former private `downloadCsv`); `downloadPersonalData(customer)`, `scheduleAnonymisation(id, anonymiseOn)`, `cancelAnonymisation(id)`; `CUSTOMER_ANONYMISED_CODE`, `isCustomerAnonymised`, `isCustomerReadOnly`; `CustomerPersonalDataMenu`; `CustomerDetailHeader`'s `canManagePersonalData?: boolean`.
- i18n (en / nb): listed in Step 5.

- [ ] **Step 1: The api layer, and the fixtures that name every field**

In `apps/customers/frontend/src/api/customers.ts`, after `CustomerContactInfo`, add:

```ts
/**
 * A private person's anonymisation (customers GDPR design D4): the UTC day it
 * is scheduled for, and — once the worker has run — when it ran. Null until
 * then; the wire omits it.
 */
export interface CustomerAnonymisation {
  anonymiseOn: string;
  anonymisedAt: string | null;
}
```

add to `CustomerResponse` after `mergedInto`:

```ts
  /** GDPR design D4. Null unless an anonymisation is scheduled or done; the wire omits the field then. */
  anonymisation: CustomerAnonymisation | null;
```

widen `RawCustomerResponse`'s `Omit<…>` list with `"anonymisation"` and its body with `anonymisation?: { anonymiseOn: string; anonymisedAt?: string } | null;`, and in `normalizeCustomer` destructure `anonymisation` beside `mergedInto` and add to `normalized`:

```ts
    anonymisation: anonymisation
      ? { anonymiseOn: anonymisation.anonymiseOn, anonymisedAt: anonymisation.anonymisedAt ?? null }
      : null,
```

(the comment's "five normalised fields" becomes "six"). Then add `anonymisation: null` beside every `mergedInto: null` in an expected `CustomerResponse`:

```bash
cd /home/anders/projects/vantigo/vantigo/apps/customers/frontend/src
sed -i 's/mergedInto: null, tags: \[\]/mergedInto: null, anonymisation: null, tags: []/' api/customers.test.ts
sed -i -E 's/^( *)mergedInto: null,$/\1mergedInto: null,\n\1anonymisation: null,/' api/customers.test.ts api/merge.test.ts pages/-customer-form-modal.test.tsx
grep -n 'anonymisation: null' api/customers.test.ts api/merge.test.ts pages/-customer-form-modal.test.tsx | wc -l   # 12
```

and in `api/customers.test.ts`, directly after the `it("carries mergedInto through, and reads its absence as null", …)` case, add:

```ts
  it("carries an anonymisation through, reading an omitted anonymisedAt as null", async () => {
    // A scheduled private person (customers GDPR design D4): the worker has not
    // run, so the server leaves anonymisedAt out.
    const scheduled = {
      id: 1005,
      name: "Kari Nordmann",
      status: "archived",
      timelineSummary: { entryCount: 1, latestOccurredOn: "2026-09-24" },
      anonymisation: { anonymiseOn: "2027-01-31" },
    };
    stubFetch(vi.fn().mockResolvedValue(jsonResponse(200, scheduled)));

    const options = customerQueryOptions(1005);
    const result = (await (options.queryFn as (context: unknown) => Promise<unknown>)({
      signal: undefined,
    })) as CustomerResponse;

    expect(result.anonymisation).toEqual({ anonymiseOn: "2027-01-31", anonymisedAt: null });
    expect(result.mergedInto).toBeNull();
  });
```

In `api/import-export.ts`, rename `downloadCsv` to `downloadFile`, export it, and update its two callers in the file; its doc comment gains `A \`CsvDownload\` is any file the server hands over — the name predates the personal-data export, which is JSON.`

Create `apps/customers/frontend/src/api/personal-data.ts`:

```ts
import { type CustomerResponse, normalizeCustomer, type RawCustomerResponse } from "./customers";
import { type CsvDownload, downloadFile } from "./import-export";
import { request } from "./request";

/**
 * A private person's whole file (customers GDPR design D3), fetched the way the
 * customers file is — a plain `fetch`, so a refusal is read and shown rather
 * than navigated to — under the name the server attached it with.
 */
export const downloadPersonalData = (customer: Pick<CustomerResponse, "id" | "customerNumber">): Promise<CsvDownload> =>
  downloadFile(
    `/api/v1/customers/${customer.id}/personal-data`,
    `customer-${customer.customerNumber}-personal-data.json`,
  );

/**
 * Puts the anonymisation on `anonymiseOn` (yyyy-MM-dd, UTC), or moves it there
 * (GDPR design D4); answers the customer as it now is, its revision included. A
 * refusal is an `ApiConflictError` whose `code` says which —
 * `personal_data_not_a_person`, `personal_data_customer_active`,
 * `customer_merged`, `customer_anonymised` — and a bad day an
 * `ApiValidationError` on `anonymiseOn`.
 */
export const scheduleAnonymisation = async (customerId: number, anonymiseOn: string): Promise<CustomerResponse> =>
  normalizeCustomer(
    await request<RawCustomerResponse>(`/api/v1/customers/${customerId}/anonymisation`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ anonymiseOn }),
    }),
  );

/** Calls the anonymisation off; answers the customer as it now is. */
export const cancelAnonymisation = async (customerId: number): Promise<CustomerResponse> =>
  normalizeCustomer(
    await request<RawCustomerResponse>(`/api/v1/customers/${customerId}/anonymisation`, { method: "DELETE" }),
  );
```

Create `apps/customers/frontend/src/api/personal-data.test.ts`:

```ts
import { afterEach, describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import { cancelAnonymisation, downloadPersonalData, scheduleAnonymisation } from "./personal-data";

const json = (body: unknown) =>
  new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } });

// Literally what the server sends once a day is scheduled: anonymisedAt is
// omitted until the worker has run, and nothing else unset is on the wire.
const scheduled = {
  id: 1005,
  customerNumber: 5,
  name: "Kari Nordmann",
  status: "archived",
  type: "person",
  createdAt: "2026-06-01T10:00:00Z",
  updatedAt: "2026-07-01T10:00:00Z",
  timelineSummary: { entryCount: 3 },
  revision: 7,
  anonymisation: { anonymiseOn: "2027-01-31" },
};

describe("personal data api", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("schedules with the day in the body and answers the normalised customer", async () => {
    const fetchMock = vi.fn((_input: RequestInfo | URL, _init?: RequestInit) => Promise.resolve(json(scheduled)));
    stubFetch(fetchMock);
    const customer = await scheduleAnonymisation(1005, "2027-01-31");
    const call = fetchMock.mock.calls.find(([url]) => String(url) === "/api/v1/customers/1005/anonymisation");
    expect(call?.[1]?.method).toBe("PUT");
    expect(JSON.parse(String(call?.[1]?.body))).toEqual({ anonymiseOn: "2027-01-31" });
    expect(customer.anonymisation).toEqual({ anonymiseOn: "2027-01-31", anonymisedAt: null });
    expect(customer.revision).toBe(7);
  });

  it("cancels with a DELETE and reads the absent anonymisation as null", async () => {
    const cancelled: Record<string, unknown> = { ...scheduled };
    delete cancelled.anonymisation;
    const fetchMock = vi.fn((_input: RequestInfo | URL, _init?: RequestInit) => Promise.resolve(json(cancelled)));
    stubFetch(fetchMock);
    expect((await cancelAnonymisation(1005)).anonymisation).toBeNull();
    expect(
      fetchMock.mock.calls.some(
        ([url, init]) => String(url) === "/api/v1/customers/1005/anonymisation" && init?.method === "DELETE",
      ),
    ).toBe(true);
  });

  it("downloads the file under the name the server gave it", async () => {
    stubFetch(
      vi.fn(() =>
        Promise.resolve(
          new Response("{}", {
            status: 200,
            headers: { "Content-Disposition": 'attachment; filename="customer-5-personal-data.json"' },
          }),
        ),
      ),
    );
    expect((await downloadPersonalData({ id: 1005, customerNumber: 5 })).fileName).toBe("customer-5-personal-data.json");
  });
});
```

- [ ] **Step 2: The read-only refusal's words**

In `apps/customers/frontend/src/lib/customer-write-error.ts`, add after `isCustomerMerged`:

```ts
/**
 * The code every customer-scoped write answers once its customer has been
 * anonymised (customers GDPR design D4): what is left is kept for bookkeeping
 * and takes no more changes.
 */
export const CUSTOMER_ANONYMISED_CODE = "customer_anonymised";

export const isCustomerAnonymised = (error: unknown): boolean =>
  error instanceof ApiConflictError && error.code === CUSTOMER_ANONYMISED_CODE;

/** Either refusal a customer that takes no more changes answers: merged away, or anonymised. */
export const isCustomerReadOnly = (error: unknown): boolean => isCustomerMerged(error) || isCustomerAnonymised(error);
```

and make `customerWriteErrorMessage`

```ts
export const customerWriteErrorMessage = (error: Error, t: (key: string) => string): string =>
  isCustomerMerged(error)
    ? t("customerMergedMessage")
    : isCustomerAnonymised(error)
      ? t("customerAnonymisedMessage")
      : error.message;
```

In `pages/-customer-form-modal.tsx` (line 191) and `pages/-customer-timeline.tsx` (lines 370 and 813), replace `isCustomerMerged(error)` with `isCustomerReadOnly(error)` and the import of `isCustomerMerged` with `isCustomerReadOnly`; update the form's comment `A merged-away customer's refusal is a 409 too` to `A merged-away or anonymised customer's refusal is a 409 too`. In `pages/-customer-merge-modal.tsx`, import `CUSTOMER_ANONYMISED_CODE` beside `CUSTOMER_MERGED_CODE` and add to `refusalKeys`:

```ts
  // The picked duplicate was anonymised (GDPR design D4): nothing of a person
  // is left in it to fold in.
  [CUSTOMER_ANONYMISED_CODE]: "customerAnonymisedMessage",
```

In `lib/customer-write-error.test.ts`, add:

```ts
// Literally the problem the server answers a write on an anonymised customer.
const anonymised = new ApiConflictError("This customer's personal data was anonymised on 2027-01-31.", {
  title: "Customer was anonymised",
  code: "customer_anonymised",
  problem: { title: "Customer was anonymised", status: 409, code: "customer_anonymised" },
});

describe("an anonymised customer's refusal", () => {
  it("is read-only, not merged, and says so in the catalog's words", () => {
    expect(isCustomerReadOnly(anonymised)).toBe(true);
    expect(isCustomerMerged(anonymised)).toBe(false);
    expect(isCustomerReadOnly(merged)).toBe(true);
    expect(customerWriteErrorMessage(anonymised, t)).toBe("t:customerAnonymisedMessage");
  });
});
```

(adding `isCustomerReadOnly` to the file's import).

- [ ] **Step 3: The menu and the schedule modal**

Create `apps/customers/frontend/src/pages/-customer-personal-data.tsx`:

```tsx
import { Alert, Button, Group, Menu, Modal, Stack, Text } from "@mantine/core";
import { DateInput } from "@mantine/dates";
import { useForm } from "@mantine/form";
import { modals } from "@mantine/modals";
import { notifications } from "@mantine/notifications";
import { IconCalendarX, IconChevronDown, IconDownload, IconShieldLock, IconUserOff } from "@tabler/icons-react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useEffect, useState } from "react";
import { ApiConflictError, ApiValidationError, type CustomerResponse, syncCustomerRevision } from "../api/customers";
import { isSessionExpired, saveCsv } from "../api/import-export";
import { cancelAnonymisation, downloadPersonalData, scheduleAnonymisation } from "../api/personal-data";
import { utcToday } from "../lib/follow-up-dates";
import { formatDateOnly } from "../lib/format-date-only";
import { CUSTOMER_ANONYMISED_CODE, CUSTOMER_MERGED_CODE, customerWriteErrorMessage } from "../lib/customer-write-error";
import "../i18n";

/** The server's scheduling refusals by code (GDPR design D4); anything else shows the server's own detail. */
const scheduleRefusalKeys: Record<string, string> = {
  personal_data_customer_active: "anonymisationRefusedActive",
  personal_data_not_a_person: "anonymisationRefusedNotAPerson",
  [CUSTOMER_ANONYMISED_CODE]: "customerAnonymisedMessage",
  [CUSTOMER_MERGED_CODE]: "customerMergedMessage",
};

/**
 * Personal data (customers GDPR design D5): a private person's export and the
 * scheduling of their anonymisation, behind the host's `canManagePersonalData`
 * (`customers:personal-data`) — the header renders it for a person only, since
 * a business is not a data subject. Export stays after the anonymisation (it
 * answers what is left); scheduling goes then, with nothing left to schedule.
 * Scheduling needs an archived customer, and a merged-away one is scheduled on
 * its survivor: the item is there but off, with the reason under it, so a
 * person knows what to do rather than wondering where it went. Once scheduled,
 * the day can be moved or the schedule called off — through the shared confirm
 * modal, as every other write that undoes something here does.
 */
export const CustomerPersonalDataMenu = ({ customer }: { customer: CustomerResponse }) => {
  const { t, formatters } = useI18n("customers");
  const queryClient = useQueryClient();
  const [scheduling, setScheduling] = useState(false);
  const anonymisation = customer.anonymisation;
  const anonymised = Boolean(anonymisation?.anonymisedAt);
  const scheduledOn = anonymisation && !anonymised ? anonymisation.anonymiseOn : null;
  const blocked = customer.mergedInto
    ? t("anonymisationMergedAway")
    : customer.status !== "archived"
      ? t("anonymisationNeedsArchive")
      : null;

  const exportFile = useMutation({
    mutationFn: () => downloadPersonalData(customer),
    onSuccess: (file) => saveCsv(file),
    onError: (error) => {
      // An expired session has already been handed to the host, which signs
      // the person out; there is nothing left to show.
      if (isSessionExpired(error)) return;
      notifications.show({ color: "red", title: t("personalDataExportFailed"), message: error.message });
    },
  });
  const cancel = useMutation({
    mutationFn: () => cancelAnonymisation(customer.id),
    onSuccess: (updated) => {
      syncCustomerRevision(queryClient, customer.id, updated.revision);
      queryClient.invalidateQueries({ queryKey: ["customers"] });
      notifications.show({
        color: "teal",
        title: t("anonymisationCancelledTitle"),
        message: t("anonymisationCancelledMessage", { name: customer.name }),
      });
    },
    onError: (error) => {
      queryClient.invalidateQueries({ queryKey: ["customers"] });
      notifications.show({ color: "red", title: t("anonymisationCouldNotBeSaved"), message: customerWriteErrorMessage(error, t) });
    },
  });
  const confirmCancel = (day: string) =>
    modals.openConfirmModal({
      title: t("anonymisationCancelTitle"),
      children: (
        <Text size="sm">
          {t("anonymisationCancelConfirm", { name: customer.name, date: formatDateOnly(formatters, day) })}
        </Text>
      ),
      labels: { confirm: t("anonymisationCancel"), cancel: t("anonymisationKeep") },
      onConfirm: () => cancel.mutate(),
    });

  return (
    <>
      <Menu position="bottom-end" withinPortal>
        <Menu.Target>
          <Button
            variant="subtle"
            color="gray"
            leftSection={<IconShieldLock size={16} />}
            rightSection={<IconChevronDown size={14} />}
            loading={exportFile.isPending}
          >
            {t("personalDataMenu")}
          </Button>
        </Menu.Target>
        <Menu.Dropdown>
          <Menu.Item leftSection={<IconDownload size={15} />} onClick={() => exportFile.mutate()}>
            {t("personalDataExport")}
          </Menu.Item>
          {!anonymised && (
            <>
              <Menu.Divider />
              <Menu.Item leftSection={<IconUserOff size={15} />} disabled={Boolean(blocked)} onClick={() => setScheduling(true)}>
                {scheduledOn ? t("anonymisationReschedule") : t("anonymisationSchedule")}
              </Menu.Item>
              {blocked && (
                <Text size="xs" c="dimmed" px="sm" pb={4} maw={280}>
                  {blocked}
                </Text>
              )}
              {scheduledOn && (
                <Menu.Item leftSection={<IconCalendarX size={15} />} color="red" onClick={() => confirmCancel(scheduledOn)}>
                  {t("anonymisationCancel")}
                </Menu.Item>
              )}
            </>
          )}
        </Menu.Dropdown>
      </Menu>
      <AnonymisationScheduleModal customer={customer} opened={scheduling} onClose={() => setScheduling(false)} />
    </>
  );
};

/**
 * The day, chosen (GDPR design D4): no default — the modal says why, and says
 * that nothing brings the data back — and nothing before today in UTC, the
 * calendar the server counts the day in. A refusal the page could not have
 * known (archived no more, anonymised meanwhile, merged away from another tab)
 * is said in the modal in words, and the page refetches.
 */
const AnonymisationScheduleModal = ({
  customer,
  opened,
  onClose,
}: {
  customer: CustomerResponse;
  opened: boolean;
  onClose: () => void;
}) => {
  const { t, formatters } = useI18n("customers");
  const queryClient = useQueryClient();
  const [refusal, setRefusal] = useState<string | null>(null);
  // A refusal left from the last time the modal was open is cleared on the
  // opening itself, adjusted during render rather than in an effect — the form
  // modal's seenState pattern (-customer-form-modal.tsx).
  const [seenOpened, setSeenOpened] = useState(opened);
  if (opened !== seenOpened) {
    setSeenOpened(opened);
    if (opened) setRefusal(null);
  }
  const form = useForm({
    initialValues: { anonymiseOn: "" },
    validate: {
      anonymiseOn: (value: string) =>
        !value ? t("anonymisationDateRequired") : value < utcToday() ? t("anonymisationDateInPast") : null,
    },
  });
  useEffect(() => {
    if (opened) {
      form.setValues({ anonymiseOn: customer.anonymisation?.anonymiseOn ?? "" });
      form.clearErrors();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [opened]);
  const mutation = useMutation({
    mutationFn: (anonymiseOn: string) => scheduleAnonymisation(customer.id, anonymiseOn),
    onSuccess: (updated, anonymiseOn) => {
      syncCustomerRevision(queryClient, customer.id, updated.revision);
      queryClient.invalidateQueries({ queryKey: ["customers"] });
      notifications.show({
        color: "teal",
        title: t("anonymisationScheduledTitle"),
        message: t("anonymisationScheduledMessage", { name: customer.name, date: formatDateOnly(formatters, anonymiseOn) }),
      });
      onClose();
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) {
        form.setErrors(error.fieldErrors);
        return;
      }
      if (error instanceof ApiConflictError && error.code && scheduleRefusalKeys[error.code]) {
        queryClient.invalidateQueries({ queryKey: ["customers"] });
        setRefusal(t(scheduleRefusalKeys[error.code]));
        return;
      }
      notifications.show({ color: "red", title: t("anonymisationCouldNotBeSaved"), message: customerWriteErrorMessage(error, t) });
    },
  });

  return (
    <Modal opened={opened} onClose={onClose} title={t("anonymisationModalTitle")}>
      <form onSubmit={form.onSubmit(({ anonymiseOn }) => mutation.mutate(anonymiseOn))}>
        <Stack gap="sm">
          <Text size="sm">{t("anonymisationModalIntro", { name: customer.name })}</Text>
          <Text size="sm">{t("anonymisationModalRetention")}</Text>
          <Text size="sm" fw={600}>
            {t("anonymisationModalIrreversible")}
          </Text>
          <DateInput
            label={t("anonymisationDate")}
            valueFormat="YYYY-MM-DD"
            minDate={utcToday()}
            withAsterisk
            {...form.getInputProps("anonymiseOn")}
          />
          {refusal && <Alert color="red">{refusal}</Alert>}
          <Group justify="flex-end">
            <Button variant="default" onClick={onClose}>
              {t("cancel")}
            </Button>
            <Button type="submit" color="red" loading={mutation.isPending}>
              {t("anonymisationConfirm")}
            </Button>
          </Group>
        </Stack>
      </form>
    </Modal>
  );
};
```

- [ ] **Step 4: The header, the banners, the read-only cards, the timeline**

In `pages/customers.$customerId.tsx`: import `IconCalendarEvent` and `IconUserOff` from `@tabler/icons-react`, `CustomerPersonalDataMenu` from `./-customer-personal-data` and `formatDateOnly` from `../lib/format-date-only`; add `canManagePersonalData?: boolean` to `CustomerDetailHeader`'s props (destructured and typed like `canMerge`), and extend its doc comment: `` `canManagePersonalData` is the host's `customers:personal-data` check (customers GDPR design D5): it puts **Personal data** beside the other actions on a private person's page. An anonymised customer, like a merged-away one, shows its banner and none of the actions that would edit it. ``

Inside the component, after `const mergedInto = customer.mergedInto;`:

```tsx
  const anonymisation = customer.anonymisation;
  const anonymisedAt = anonymisation?.anonymisedAt ?? null;
  // Merged away or anonymised: either way the customer takes no more changes
  // (merge design D2, GDPR design D4), so the page offers none.
  const readOnly = Boolean(mergedInto) || Boolean(anonymisedAt);
```

Replace the banner expression — `{mergedInto ? ( … ) : ( isArchived && ( … ) )}` — with:

```tsx
      {mergedInto ? (
        <Alert
          color="gray"
          icon={<IconArrowMerge size={16} />}
          title={t("mergedAwayBannerTitle", { number: mergedInto.customerNumber, name: mergedInto.name })}
        >
          <Stack gap={4}>
            <Text size="sm">{t("mergedAwayBannerMessage")}</Text>
            <Anchor size="sm" renderRoot={(props) => <Link to={`/customers/${mergedInto.id}` as never} {...props} />}>
              {t("mergedAwayOpenSurvivor", { number: mergedInto.customerNumber, name: mergedInto.name })}
            </Anchor>
          </Stack>
        </Alert>
      ) : anonymisedAt ? (
        <Alert
          color="gray"
          icon={<IconUserOff size={16} />}
          title={t("anonymisedBannerTitle", { date: formatters.formatDate(anonymisedAt) })}
        >
          {t("anonymisedBannerMessage")}
        </Alert>
      ) : anonymisation ? (
        <Alert
          color="yellow"
          icon={<IconCalendarEvent size={16} />}
          title={t("anonymisationScheduledBannerTitle", { date: formatDateOnly(formatters, anonymisation.anonymiseOn) })}
        >
          {t("anonymisationScheduledBannerMessage")}
        </Alert>
      ) : (
        isArchived && (
          <Alert color="gray" icon={<IconArchive size={16} />} title={t("archivedBannerTitle")}>
            {t("archivedBannerMessage")}
          </Alert>
        )
      )}
```

In the actions `Group`: change `{!mergedInto && (` (Edit / Change type) to `{!readOnly && (`, `{canMerge && !mergedInto && (` to `{canMerge && !readOnly && (`, `{canRestore && isArchived && !mergedInto && (` to `{canRestore && isArchived && !readOnly && (`, and directly before `{actions}` add:

```tsx
              {canManagePersonalData && customer.type === "person" && <CustomerPersonalDataMenu customer={customer} />}
```

In `CustomerOverview`, replace `const editable = !customer.mergedInto;` and the comment above it with:

```tsx
  // A merged-away or anonymised customer is read-only on the page (merge design
  // D4, GDPR design D5), as it is on the server: everything a merged-away one
  // had is on the survivor, and what an anonymised one has left is kept for
  // bookkeeping — anything written here would be refused.
  const editable = !customer.mergedInto && !customer.anonymisation?.anonymisedAt;
```

and in its doc comment change `A merged-away customer (merge design D4) gets every capability as false` to `A merged-away or anonymised customer (merge design D4, GDPR design D5) gets every capability as false`.

In `pages/-customer-timeline.tsx`, add to `typeKey`:

```ts
  "customer.anonymisation_scheduled": "customerAnonymisationScheduledEvent",
  "customer.anonymisation_cancelled": "customerAnonymisationCancelledEvent",
  "customer.anonymised": "customerAnonymisedEvent",
```

and above `MergedAbsorbedDetails` add to its doc comment: `Once the customer it describes has been anonymised (GDPR design D4) \`absorbed\` is the plain "[anonymised]" string, which is no record, so nothing is listed — the entry's own summary already says "[anonymised]".` Nothing else in the file changes: an anonymised summary or note is text like any other, a `changes` that is a string yields no details (`payloadDetails` reads objects only), and an anonymised contact's `displayName` is inside the summary, so the contact line stays suppressed.

- [ ] **Step 5: The strings**

In `apps/customers/frontend/src/i18n.ts`, add to `en` (after `mergedAwayOpenSurvivor`):

```ts
  personalDataMenu: "Personal data",
  personalDataExport: "Export personal data",
  personalDataExportFailed: "The personal data could not be exported",
  anonymisationSchedule: "Schedule anonymisation…",
  anonymisationReschedule: "Change anonymisation date…",
  anonymisationCancel: "Cancel anonymisation",
  anonymisationKeep: "Keep it",
  anonymisationNeedsArchive: "Archive the customer first: an ongoing relationship is not anonymised.",
  anonymisationMergedAway: "This customer was merged into another; schedule the anonymisation there.",
  anonymisationModalTitle: "Schedule anonymisation",
  anonymisationModalIntro:
    "On this day {{name}}'s name, legal identity, contact details, addresses, contacts, correspondence and the content of every timeline entry are removed. The customer number, the dates and what happened when are kept for bookkeeping.",
  anonymisationModalRetention:
    "There is no default day. Norwegian bookkeeping rules keep accounting material for years after the fiscal year, so choose a day after every retention period for what was invoiced to this customer has passed.",
  anonymisationModalIrreversible: "An anonymisation cannot be undone.",
  anonymisationDate: "Anonymise on",
  anonymisationDateRequired: "Choose a day",
  anonymisationDateInPast: "The day cannot be in the past",
  anonymisationConfirm: "Schedule anonymisation",
  anonymisationScheduledTitle: "Anonymisation scheduled",
  anonymisationScheduledMessage: "{{name}} will be anonymised on {{date}}.",
  anonymisationCancelTitle: "Cancel the anonymisation?",
  anonymisationCancelConfirm: "{{name}} will not be anonymised on {{date}}. It can be scheduled again at any time.",
  anonymisationCancelledTitle: "Anonymisation cancelled",
  anonymisationCancelledMessage: "{{name}} will not be anonymised.",
  anonymisationCouldNotBeSaved: "The anonymisation could not be saved",
  anonymisationRefusedActive: "This customer is not archived any more. Archive it before scheduling its anonymisation.",
  anonymisationRefusedNotAPerson: "Only a private person can be anonymised.",
  anonymisationScheduledBannerTitle: "Anonymisation scheduled for {{date}}",
  anonymisationScheduledBannerMessage:
    "On that day this customer's personal data is removed for good. Restoring the customer calls it off.",
  anonymisedBannerTitle: "Anonymised on {{date}}",
  anonymisedBannerMessage:
    "This private person's data has been removed. The customer number and the dates of its history are kept for bookkeeping, and nothing here can be changed.",
  customerAnonymisedMessage: "This customer was anonymised",
  customerAnonymisationScheduledEvent: "Anonymisation scheduled",
  customerAnonymisationCancelledEvent: "Anonymisation cancelled",
  customerAnonymisedEvent: "Customer anonymised",
```

and to `nb` (after its `mergedAwayOpenSurvivor`):

```ts
  personalDataMenu: "Personopplysninger",
  personalDataExport: "Eksporter personopplysninger",
  personalDataExportFailed: "Personopplysningene kunne ikke eksporteres",
  anonymisationSchedule: "Planlegg anonymisering…",
  anonymisationReschedule: "Endre anonymiseringsdato…",
  anonymisationCancel: "Avbryt anonymisering",
  anonymisationKeep: "Behold den",
  anonymisationNeedsArchive: "Arkiver kunden først: et pågående kundeforhold anonymiseres ikke.",
  anonymisationMergedAway: "Denne kunden er slått sammen med en annen; planlegg anonymiseringen der.",
  anonymisationModalTitle: "Planlegg anonymisering",
  anonymisationModalIntro:
    "Denne dagen fjernes navnet til {{name}}, den juridiske identiteten, kontaktopplysningene, adressene, kontaktene, korrespondansen og innholdet i hver tidslinjehendelse. Kundenummeret, datoene og hva som skjedde når, beholdes for regnskapet.",
  anonymisationModalRetention:
    "Det finnes ingen standarddato. Bokføringsreglene krever at regnskapsmateriale oppbevares i flere år etter regnskapsårets slutt, så velg en dag etter at hver oppbevaringsperiode for det som er fakturert denne kunden, er over.",
  anonymisationModalIrreversible: "En anonymisering kan ikke angres.",
  anonymisationDate: "Anonymiser den",
  anonymisationDateRequired: "Velg en dag",
  anonymisationDateInPast: "Dagen kan ikke være passert",
  anonymisationConfirm: "Planlegg anonymisering",
  anonymisationScheduledTitle: "Anonymisering planlagt",
  anonymisationScheduledMessage: "{{name}} anonymiseres {{date}}.",
  anonymisationCancelTitle: "Avbryte anonymiseringen?",
  anonymisationCancelConfirm: "{{name}} anonymiseres ikke {{date}}. Den kan planlegges igjen når som helst.",
  anonymisationCancelledTitle: "Anonymisering avbrutt",
  anonymisationCancelledMessage: "{{name}} anonymiseres ikke.",
  anonymisationCouldNotBeSaved: "Anonymiseringen kunne ikke lagres",
  anonymisationRefusedActive: "Denne kunden er ikke arkivert lenger. Arkiver den før anonymiseringen planlegges.",
  anonymisationRefusedNotAPerson: "Bare en privatperson kan anonymiseres.",
  anonymisationScheduledBannerTitle: "Anonymisering planlagt {{date}}",
  anonymisationScheduledBannerMessage:
    "Den dagen fjernes personopplysningene til denne kunden for godt. Gjenoppretter du kunden, avbrytes den.",
  anonymisedBannerTitle: "Anonymisert {{date}}",
  anonymisedBannerMessage:
    "Opplysningene om denne privatpersonen er fjernet. Kundenummeret og datoene i historikken beholdes for regnskapet, og ingenting her kan endres.",
  customerAnonymisedMessage: "Denne kunden er anonymisert",
  customerAnonymisationScheduledEvent: "Anonymisering planlagt",
  customerAnonymisationCancelledEvent: "Anonymisering avbrutt",
  customerAnonymisedEvent: "Kunde anonymisert",
```

- [ ] **Step 6: The tests**

Create `apps/customers/frontend/src/pages/-customer-personal-data.test.tsx`:

```tsx
import { MantineProvider } from "@mantine/core";
import { ModalsProvider } from "@mantine/modals";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRootRoute, createRouter, RouterProvider } from "@tanstack/react-router";
import { cleanup, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Suspense } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { saveCsv } from "../api/import-export";
import { stubFetch } from "../test/fetch";
import { CustomerDetailHeader } from "./customers.$customerId";

vi.mock("@mantine/notifications", () => ({ notifications: { show: vi.fn() } }));
vi.mock("../api/import-export", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../api/import-export")>()),
  saveCsv: vi.fn(),
}));

const json = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": status < 300 ? "application/json" : "application/problem+json" },
  });

// Literally the body the server sends for a private person with nothing set.
const person = (overrides: Record<string, unknown> = {}) => ({
  id: 1005,
  customerNumber: 5,
  name: "Kari Nordmann",
  status: "active",
  type: "person",
  createdAt: "2026-06-01T10:00:00Z",
  updatedAt: "2026-07-01T10:00:00Z",
  timelineSummary: { entryCount: 0 },
  revision: 4,
  ...overrides,
});

/** A day `days` from today in UTC, the calendar the server counts in. */
const utcDay = (days: number) => new Date(Date.now() + days * 86_400_000).toISOString().slice(0, 10);

type Handler = (url: string, init?: RequestInit) => Response | undefined;

const renderHeader = async (
  body: ReturnType<typeof person>,
  props: { canManagePersonalData?: boolean; canRestore?: boolean; canMerge?: boolean } = {},
  handle: Handler = () => undefined,
) => {
  const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    const answer = handle(url, init);
    if (answer) return Promise.resolve(answer);
    if (url === "/api/v1/customers/1005/legal-identity") return Promise.resolve(new Response(null, { status: 403 }));
    if (url === "/api/v1/customers/1005") return Promise.resolve(json(200, body));
    return Promise.resolve(new Response(null, { status: 404 }));
  });
  stubFetch(fetchMock);
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const rootRoute = createRootRoute({
    component: () => (
      <MantineProvider env="test">
        <ModalsProvider>
          <QueryClientProvider client={queryClient}>
            <Suspense fallback={<div>Loading…</div>}>
              <CustomerDetailHeader customerId={1005} {...props} />
            </Suspense>
          </QueryClientProvider>
        </ModalsProvider>
      </MantineProvider>
    ),
  });
  const router = createRouter({ routeTree: rootRoute, history: createMemoryHistory({ initialEntries: ["/"] }) });
  await router.load();
  render(<RouterProvider router={router} />);
  await screen.findByRole("heading", { name: body.name }, { timeout: 5000 });
  return fetchMock;
};

const callsTo = (fetchMock: ReturnType<typeof vi.fn>, method: string, url: string) =>
  fetchMock.mock.calls.filter(([u, init]) => String(u) === url && ((init as RequestInit | undefined)?.method ?? "GET") === method);

const openMenu = async () => userEvent.click(screen.getByRole("button", { name: "Personal data" }));

describe("customer detail header — personal data", () => {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
    vi.clearAllMocks();
  });

  it("offers Personal data on a private person's page only, and only when the host says so", async () => {
    await renderHeader(person());
    expect(screen.queryByRole("button", { name: "Personal data" })).not.toBeInTheDocument();
    cleanup();
    await renderHeader(person({ type: "business", name: "Acme AS" }), { canManagePersonalData: true });
    expect(screen.queryByRole("button", { name: "Personal data" })).not.toBeInTheDocument();
    cleanup();
    await renderHeader(person(), { canManagePersonalData: true });
    expect(screen.getByRole("button", { name: "Personal data" })).toBeInTheDocument();
  });

  it("downloads the file under the name the server gave it", async () => {
    const fetchMock = await renderHeader(person(), { canManagePersonalData: true }, (url) =>
      url === "/api/v1/customers/1005/personal-data"
        ? new Response("{}", {
            status: 200,
            headers: { "Content-Disposition": 'attachment; filename="customer-5-personal-data.json"' },
          })
        : undefined,
    );
    await openMenu();
    await userEvent.click(await screen.findByRole("menuitem", { name: "Export personal data" }));
    await vi.waitFor(() => expect(saveCsv).toHaveBeenCalledWith(expect.objectContaining({ fileName: "customer-5-personal-data.json" })));
    expect(callsTo(fetchMock, "GET", "/api/v1/customers/1005/personal-data")).toHaveLength(1);
  });

  it("keeps scheduling off, with the reason, while the customer is active", async () => {
    await renderHeader(person(), { canManagePersonalData: true });
    await openMenu();
    expect(await screen.findByRole("menuitem", { name: "Schedule anonymisation…" })).toBeDisabled();
    expect(screen.getByText("Archive the customer first: an ongoing relationship is not anonymised.")).toBeInTheDocument();
  });

  it("schedules an archived person on the day chosen, and sends that day", async () => {
    const day = utcDay(40);
    const fetchMock = await renderHeader(person({ status: "archived" }), { canManagePersonalData: true }, (url, init) =>
      url === "/api/v1/customers/1005/anonymisation" && init?.method === "PUT"
        ? json(200, person({ status: "archived", revision: 5, anonymisation: { anonymiseOn: day } }))
        : undefined,
    );
    await openMenu();
    await userEvent.click(await screen.findByRole("menuitem", { name: "Schedule anonymisation…" }));
    const dialog = await screen.findByRole("dialog", { name: "Schedule anonymisation" });
    expect(within(dialog).getByText("An anonymisation cannot be undone.")).toBeInTheDocument();
    await userEvent.type(within(dialog).getByRole("textbox", { name: /anonymise on/i }), day);
    await userEvent.click(within(dialog).getByRole("button", { name: "Schedule anonymisation" }));

    await vi.waitFor(() => expect(callsTo(fetchMock, "PUT", "/api/v1/customers/1005/anonymisation")).toHaveLength(1));
    const [, init] = callsTo(fetchMock, "PUT", "/api/v1/customers/1005/anonymisation")[0];
    expect(JSON.parse(String((init as RequestInit).body))).toEqual({ anonymiseOn: day });
  });

  it("says in words why the server refused a schedule", async () => {
    await renderHeader(person({ status: "archived" }), { canManagePersonalData: true }, (url, init) =>
      url === "/api/v1/customers/1005/anonymisation" && init?.method === "PUT"
        ? json(409, { title: "Customer is not archived", status: 409, code: "personal_data_customer_active", detail: "#5 Kari Nordmann is active." })
        : undefined,
    );
    await openMenu();
    await userEvent.click(await screen.findByRole("menuitem", { name: "Schedule anonymisation…" }));
    const dialog = await screen.findByRole("dialog", { name: "Schedule anonymisation" });
    await userEvent.type(within(dialog).getByRole("textbox", { name: /anonymise on/i }), utcDay(40));
    await userEvent.click(within(dialog).getByRole("button", { name: "Schedule anonymisation" }));
    expect(
      await within(dialog).findByText("This customer is not archived any more. Archive it before scheduling its anonymisation."),
    ).toBeInTheDocument();
  });

  it("shows a scheduled day in a banner, and calls the schedule off through the confirm modal", async () => {
    const fetchMock = await renderHeader(
      person({ status: "archived", anonymisation: { anonymiseOn: "2099-01-31" } }),
      { canManagePersonalData: true },
      (url, init) =>
        url === "/api/v1/customers/1005/anonymisation" && init?.method === "DELETE"
          ? json(200, person({ status: "archived", revision: 5 }))
          : undefined,
    );
    expect(screen.getByText(/^Anonymisation scheduled for /)).toBeInTheDocument();
    await openMenu();
    await userEvent.click(await screen.findByRole("menuitem", { name: "Cancel anonymisation" }));
    const confirm = await screen.findByRole("dialog", { name: "Cancel the anonymisation?" });
    await userEvent.click(within(confirm).getByRole("button", { name: "Cancel anonymisation" }));
    await vi.waitFor(() => expect(callsTo(fetchMock, "DELETE", "/api/v1/customers/1005/anonymisation")).toHaveLength(1));
  });

  it("shows an anonymised customer's banner and none of the actions that would edit it", async () => {
    await renderHeader(
      person({
        name: "Anonymised person",
        status: "archived",
        anonymisation: { anonymiseOn: "2026-09-12", anonymisedAt: "2026-09-12T02:00:00Z" },
      }),
      { canManagePersonalData: true, canRestore: true, canMerge: true },
    );
    expect(screen.getByText(/^Anonymised on /)).toBeInTheDocument();
    expect(screen.queryByText(/this customer is archived/i)).not.toBeInTheDocument();
    for (const action of ["Edit customer", "Change type", "Restore customer", "Merge…"]) {
      expect(screen.queryByRole("button", { name: action })).not.toBeInTheDocument();
    }
    await openMenu();
    expect(await screen.findByRole("menuitem", { name: "Export personal data" })).toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: /anonymis/i })).not.toBeInTheDocument();
  });
});
```

In `pages/-customer-timeline.test.tsx`, add inside `describe("the timeline's merge events", …)`, beside the other merge-event cases:

```tsx
  it("renders an anonymised customer's entries plainly, and labels the three new events", async () => {
    await renderTimeline(
      vi.fn().mockResolvedValue(
        json({
          data: [
            entry({ id: 3, provenance: "generated", eventType: "customer.anonymised", note: null, summary: "Customer anonymised",
              payload: { customerId: 42, erased: [{ kind: "customers.addresses", count: 2 }] } }),
            entry({ id: 2, provenance: "generated", eventType: "customer.merged", note: null, summary: "[anonymised]",
              payload: { customerId: 42, absorbed: "[anonymised]", moved: [] } }),
            entry({ id: 1, provenance: "generated", eventType: "customer.contact_attached", note: null, summary: "[anonymised]",
              payload: { customerId: 42, contactId: 7, displayName: "[anonymised]", roles: [] } }),
          ],
          nextCursor: null,
        }),
      ),
    );
    // The Event types filter renders every label as a <span> at once; the feed's
    // own lines are <p>, so the selector waits for the feed.
    expect(await screen.findAllByText("Customer anonymised", { selector: "p" })).toHaveLength(2); // the label and the summary
    expect(screen.getAllByText("[anonymised]")).toHaveLength(2);
    expect(screen.queryByLabelText("The duplicate's own details")).not.toBeInTheDocument();
    expect(screen.queryByText(/Contact: \[anonymised\]/)).not.toBeInTheDocument();
  });
```

(biome reflows the fixture on `--write`).

In `pages/-customer-import-modal.test.tsx`, add:

```tsx
  it("shows the server's refusal of a national identity number on its legalId row", async () => {
    const refusal = "A Norwegian national identity number is never stored here";
    stubImport({
      dry: json({ dryRun: true, rows: 1, created: 0, updated: 0, failed: 1, errors: [{ row: 1, column: "legalId", message: refusal }] }),
    });
    renderModal();
    await chooseFile();
    await userEvent.click(screen.getByRole("button", { name: "Check" }));
    await checkShown();
    const row = within(screen.getByRole("table", { name: "Problems in the file, by row" }))
      .getByText(refusal)
      .closest("tr") as HTMLElement;
    expect(within(row).getByText("legalId")).toBeInTheDocument();
  });
```

(`chooseFile`, `checkShown`, `json`, `stubImport` and `renderModal` are the file's own helpers, as the first case uses them).

In `apps/host/frontend/src/routes/customers/-customer-detail-layout.tsx`, add to `<CustomerDetailHeader …>`:

```tsx
        canManagePersonalData={hasPermissions(permissions, ["customers:personal-data"])}
```

In `routes/customers/customer-detail-route.test.tsx`, add `canManagePersonalData` to the mocked header's props and render `<span data-testid="can-manage-personal-data">{String(Boolean(canManagePersonalData))}</span>`, and add a case beside the `canMerge` one:

```tsx
  it("passes canManagePersonalData from customers:personal-data, and only from it", () => {
    vi.mocked(useQuery).mockImplementation((options) =>
      options.queryKey[0] === "authorization"
        ? ({ data: { permissions: ["customers:delete", "customers:update", "customers:merge"] }, isPending: false } as never)
        : ({ data: { user: { id: "user-1", roles: [] } } } as never),
    );
    renderCustomerDetailLayout();
    expect(screen.getByTestId("can-manage-personal-data")).toHaveTextContent("false");
    cleanup();

    vi.mocked(useQuery).mockImplementation((options) =>
      options.queryKey[0] === "authorization"
        ? ({ data: { permissions: ["customers:personal-data"] }, isPending: false } as never)
        : ({ data: { user: { id: "user-1", roles: [] } } } as never),
    );
    renderCustomerDetailLayout();
    expect(screen.getByTestId("can-manage-personal-data")).toHaveTextContent("true");
  });
```

In `apps/host/frontend/src/catalogs/admin.ts`, add after the `"customers:merge"` entry:

```ts
  "customers:personal-data": {
    moduleKey: "admin.permission.module.customers",
    categoryKey: "admin.permission.category.customers",
    displayNameKey: "admin.permission.customersPersonalData",
    descriptionKey: "admin.permission.customersPersonalDataDescription",
  },
```

to the English catalog after `admin.permission.customersMergeDescription`:

```ts
  "admin.permission.customersPersonalData": "Manage personal data",
  "admin.permission.customersPersonalDataDescription":
    "Hand a private person all the data held about them, and schedule the anonymisation of an archived private person.",
```

and to the Norwegian one after its `customersMergeDescription`:

```ts
  "admin.permission.customersPersonalData": "Håndtere personopplysninger",
  "admin.permission.customersPersonalDataDescription":
    "Gi en privatperson alle opplysningene som finnes om dem, og planlegg anonymiseringen av en arkivert privatperson.",
```

In `catalogs/admin.test.ts`, change the merge case's comment to `lists above: customers:merge and customers:personal-data are the keys the last two deliveries added.` and add beside it:

```ts
  it("names customers:personal-data in English and Norwegian, the English the server's own", () => {
    const translation = hostPermissionTranslationKeys["customers:personal-data"];
    expect(translation, "no catalog entry for customers:personal-data").toBeDefined();
    for (const lng of ["en", "nb"] as const) {
      const catalog = adminCatalog[lng] as Record<string, string>;
      expect(catalog[translation.displayNameKey], `display name (${lng})`).toBeTruthy();
      expect(catalog[translation.descriptionKey], `description (${lng})`).toBeTruthy();
    }
    const en = adminCatalog.en as Record<string, string>;
    expect(en[translation.displayNameKey]).toBe("Manage personal data");
    expect(en[translation.descriptionKey]).toBe(
      "Hand a private person all the data held about them, and schedule the anonymisation of an archived private person.",
    );
  });
```

- [ ] **Step 7: Run everything, show it can fail, commit**

```bash
cd /home/anders/projects/vantigo/vantigo
F="apps/customers/frontend/src"
H="apps/host/frontend/src"
mise exec -- bunx biome check --write $F/api/customers.ts $F/api/customers.test.ts $F/api/merge.test.ts $F/api/import-export.ts \
  $F/api/personal-data.ts $F/api/personal-data.test.ts $F/lib/customer-write-error.ts $F/lib/customer-write-error.test.ts \
  $F/pages/-customer-personal-data.tsx $F/pages/-customer-personal-data.test.tsx "$F/pages/customers.\$customerId.tsx" \
  $F/pages/-customer-timeline.tsx $F/pages/-customer-timeline.test.tsx $F/pages/-customer-form-modal.tsx \
  $F/pages/-customer-form-modal.test.tsx $F/pages/-customer-merge-modal.tsx $F/pages/-customer-import-modal.test.tsx $F/i18n.ts \
  $H/routes/customers/-customer-detail-layout.tsx $H/routes/customers/customer-detail-route.test.tsx $H/catalogs/admin.ts $H/catalogs/admin.test.ts
mise exec -- bun run --cwd apps/customers/frontend test && mise exec -- bun run --cwd apps/host/frontend test
mise exec -- bun run --cwd apps/customers/frontend typecheck && mise exec -- bun run --cwd apps/host/frontend typecheck
mise exec -- bun run --cwd apps/customers/frontend lint && mise exec -- bun run --cwd apps/host/frontend lint
mise exec -- bun run translations:check && mise exec -- bun run i18n:test
```

If a typecheck names another fixture typed as `CustomerResponse` that now lacks `anonymisation` (the host's customer tabs build theirs from wire bodies, so none is expected), add `anonymisation: null` there and put the file in `PATHS`.

Prove the tests can fail, restoring after each: drop `customer.type === "person"` from the header's condition — the business case goes red; drop `disabled={Boolean(blocked)}` — the active-customer case goes red; drop `!readOnly` from the Edit/Change type condition — the anonymised case goes red; remove `anonymisation` from `normalizeCustomer` — the api tests go red; delete `personal_data_customer_active` from `scheduleRefusalKeys` — the refusal case goes red; delete the `customer.anonymised` `typeKey` entry — the timeline case goes red ("Timeline event" instead of the label); drop the host's prop — the route case goes red. Say what each printed.

```bash
cat > /tmp/claude-1000/msg-gdpr-8.txt <<'EOF'
feat(customers-ui): a private person's page exports their data and schedules their anonymisation

A Personal data menu on a person's page header, behind the host's new
canManagePersonalData (customers:personal-data): the JSON file,
downloaded the customers file's way, and a schedule modal with no
default day, the retention reminder and the irreversibility said before
the button — enabled for an archived customer, the reason shown
otherwise — with the day moved or called off once set. A scheduled
customer shows a yellow banner; an anonymised one shows "Anonymised on"
and, like a merged-away one, no edit action anywhere. The timeline
labels the three new events, customer_anonymised is said in the
reader's words, and the admin catalog names the key. en + nb.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
PATHS="$F/api/customers.ts $F/api/customers.test.ts $F/api/merge.test.ts $F/api/import-export.ts \
 $F/api/personal-data.ts $F/api/personal-data.test.ts $F/lib/customer-write-error.ts $F/lib/customer-write-error.test.ts \
 $F/pages/-customer-personal-data.tsx $F/pages/-customer-personal-data.test.tsx $F/pages/customers.\$customerId.tsx \
 $F/pages/-customer-timeline.tsx $F/pages/-customer-timeline.test.tsx $F/pages/-customer-form-modal.tsx \
 $F/pages/-customer-form-modal.test.tsx $F/pages/-customer-merge-modal.tsx $F/pages/-customer-import-modal.test.tsx $F/i18n.ts \
 $H/routes/customers/-customer-detail-layout.tsx $H/routes/customers/customer-detail-route.test.tsx $H/catalogs/admin.ts $H/catalogs/admin.test.ts"
git add $PATHS && git commit -F /tmp/claude-1000/msg-gdpr-8.txt -- $PATHS
git show --stat HEAD && git status --short
```

---

### Task 9: Verify the whole branch and open the PR

- [ ] **Step 1: The whole suite, as CI runs it**

```bash
cd /home/anders/projects/vantigo/vantigo && gh run list --branch main --limit 5   # is main already red? say so in the report if it is
cd /home/anders/projects/vantigo/vantigo/apps/server
export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'
mise exec -- gofmt -l internal cmd && mise exec -- go vet ./... && mise exec -- go build ./...
mise exec -- golangci-lint run ./...   # depguard: no module imports another; the integration package is the one exception
taskset -c 0-3 mise exec -- go test -count=1 ./... 2>&1 | tail -40; echo "exit ${PIPESTATUS[0]}"
mise exec -- go generate ./... >/dev/null 2>&1; cd /home/anders/projects/vantigo/vantigo && git status --short   # clean but for go.mod/go.sum (and the plan file, if the controller has not committed it yet)
mise exec -- bun run gen:client && git status --short                                                         # still clean
mise exec -- bun run --cwd apps/customers/frontend test && mise exec -- bun run --cwd apps/host/frontend test
mise exec -- bun run --cwd apps/customers/frontend typecheck && mise exec -- bun run --cwd apps/host/frontend typecheck
mise exec -- bun run --cwd apps/customers/frontend lint && mise exec -- bun run --cwd apps/host/frontend lint
mise exec -- bun run translations:check && mise exec -- bun run i18n:test
mise exec -- bunx biome check apps/customers/frontend/src apps/host/frontend/src
cd apps/server && mise exec -- go run ./internal/openapi/cmd/contract coverage -corpus ../../openapi/testdata/exchanges -out ../../openapi/COVERAGE.md
cd /home/anders/projects/vantigo/vantigo && git status --short -- openapi/COVERAGE.md   # committed in Tasks 4 and 5: must print nothing
```
`taskset -c 0-3` because the race detector and 44 CPUs disagree about this database's connection limits; drop it if the suite is green without it. Run the worker's tests once more on 4 CPUs, where the CI runner's timing lives: `taskset -c 0-3 mise exec -- go test -count=10 -run 'Anonymis' ./internal/customers/` — all ten must pass. `main` may already be red for reasons that are not ours — if a failure is in a module this branch never touched, check it against `git log origin/main` and say so rather than fixing it here.

- [ ] **Step 2: Read the branch as a reviewer would**

```bash
cd /home/anders/projects/vantigo/vantigo
git log --oneline main..HEAD
git diff --stat main..HEAD
git diff main..HEAD -- openapi/customers.yaml
git diff main..HEAD -- apps/server/internal/module apps/server/internal/contracts
cd apps/server && mise exec -- go test -count=1 -run 'TestNoModuleReferencesAnotherModulesSchema|TestSqlcSchemaListsOnlyTheModulesOwnMigrations' ./internal/db/ && cd ../..
grep -rn 'fødselsnummer\|personal-data\|anonymis' docs/customers.md | head -40   # the docs say what the code does
```
Check, by eye: the design and plan commits plus eight task commits, each trailer `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>`; nothing under `openapi/testdata/exchanges/`; `go.mod`/`go.sum` still untracked; no existing `required:` list changed; one migration (`00030`); no customers file imports another module; no query names another schema; no real person's national identity number anywhere in the diff (`git diff main..HEAD | grep -E '\b[0-9]{11}\b'` finds only what the tests build or none).

- [ ] **Step 3: Open the PR**

```bash
cd /home/anders/projects/vantigo/vantigo
git push -u origin feat/customers-gdpr
cat > /tmp/claude-1000/pr-gdpr.md <<'EOF'
## GDPR for person customers (phase 6, delivery C)

A private person's data handed over in one file, and anonymised on a chosen day —
the row and its bookkeeping kept, the person taken out. Decided in
`docs/superpowers/specs/2026-09-24-customers-gdpr-design.md` (D1–D6). The Customers
roadmap's last delivery.

- **Never a fødselsnummer.** For a Norwegian private person, an id whose two mod-11
  check digits pass (fødselsnummer or D-number, separators and all) is refused — create,
  legal-identity PUT and CSV import, through the one validator.
- **One contract, a second direction.** `contracts.CustomerPersonalData` — export outside
  any transaction, erase inside the customers module's — collected from every module given,
  by Compose and by Workers (worker mode never composes). Communications hands over and
  deletes the person's correspondence through the retention worker's own deletes and
  cleanup ledger; energy and projects hand over and keep. `docs/module-boundaries.md` gains
  rule 9.
- **`GET /customers/{id}/personal-data`** behind the new sensitive `customers:personal-data`
  (+ view): the row, identity, contact info, addresses, billing profile, owner, group, tags,
  contacts, every timeline entry, and each module's section, as a JSON attachment. A business
  is refused `personal_data_not_a_person`.
- **`PUT`/`DELETE /customers/{id}/anonymisation`**: a day, today or later, no default, on an
  archived person only (`personal_data_customer_active`); restoring or retyping calls it off.
  Migration `00030`; `anonymisation?` on every customer response.
- **The `customers-anonymisation` worker** (`CUSTOMERS_ANONYMISATION_ENABLED` on, `_POLL` 24h,
  its own lease, fifty a cycle): one transaction per customer — the name becomes "Anonymised
  person" with the number kept, identity, contact info and billing identifiers cleared,
  terms kept, addresses and orphan contacts gone, every timeline entry and revision kept with
  its content anonymised in SQL, merge chains followed, each module's eraser inside, then
  `customer.anonymised`. A failing module rolls that customer back and the cycle moves on.
- **Read-only.** The merged-away refusal generalises: every write to an anonymised customer
  answers `customer_anonymised` through the same lock-time check.
- **Frontend**: a Personal data menu on a person's page (`canManagePersonalData`), the
  scheduled and anonymised banners, every edit hidden once anonymised, the new timeline
  events, en + nb.

Contract: three operations, five new schemas, one optional field on `SafeCustomerResponse`;
the frozen corpus is untouched and still validates.

Decisions on the record for review: the refusal lives in validateLegalIdentity and keys
per door; the slot pairs each implementation with its module's name and is collected by
Workers too; energy and projects report their kind at zero; the timeline is rewritten
set-based in SQL, generated summaries included unless their type is on an allow-list;
restoring or retyping calls a schedule off, a merged-away customer can be cancelled but not
scheduled, and an anonymised one cannot be absorbed; a merge chain is anonymised together,
and a merged-away customer takes only its snapshot off the survivor; the identity refusal
shows in the UI through the import's errors table, since no form enters a person's id; the
export includes deleted timeline entries.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
gh pr create --base main --head feat/customers-gdpr \
  --title "GDPR for person customers (phase 6, delivery C)" \
  --body-file /tmp/claude-1000/pr-gdpr.md
```
`gh pr edit` is broken in this environment: to change the body afterwards use `gh api -X PATCH repos/:owner/:repo/pulls/<n> -F body=@/tmp/claude-1000/pr-gdpr.md` (`-F`, which reads the file; `-f` would send the literal string). Do not merge — the user does that.

- [ ] **Step 4: Report**

Say: the PR's number and URL; each test shown able to fail and what the mutation printed; anything the generated code disagreed with this plan about (sqlc's names and parameter structs in Tasks 3–6, oapi-codegen's field names in Task 4, whether `TestServeMuxConflictsArePinned` wanted other pins); and whether `main` was already red. Plus the places this branch decides what the spec left open, for the user's verdict:

- the national-identity refusal sits in `validateLegalIdentity` beside the organisation-number rule and is keyed `id` / `identity.id` / CSV `legalId` per door; spaces, hyphens and full stops are stripped first, only the check digits decide, and the message echoes no number;
- `Deps.CustomerPersonalData` pairs each implementation with its module's name (`CustomerPersonalDataHolder`), and `module.Workers` collects the slot as well as Compose, since the worker is the eraser's one caller;
- energy and projects report `energy.supplyPeriods` / `projects.projects` at zero, so `customer.anonymised` names every module asked;
- the timeline is rewritten set-based in SQL over every payload's listed top-level keys; manual source URLs go; generated summaries are anonymised too unless their type is on an allow-list of summaries that carry nothing personal;
- restoring (API or CSV) or retyping a scheduled customer calls the schedule off, recorded; a merged-away customer cannot be scheduled but can be cancelled; an anonymised customer cannot be absorbed by a merge;
- a merge chain is anonymised in one transaction, each member with its own event and the chain's day; a due merged-away customer takes only its snapshot off the survivor, leaving the survivor's moved history as the survivor's;
- no UI form enters a person's legal identity, so D5's identity refusal is shown through the CSV import's errors table; no client-side mirror of the check;
- the export includes deleted timeline entries; a module with a nil section has no key; scheduling and cancelling answer the customer, carry no revision, and a no-op writes nothing;
- the erased counts: this module's four kinds (associations and orphan contacts counted apart), the Peppol answer and registry record deleted without a count, communications' five kinds;
- every anonymised customer leaves its group (controller ruling on the pre-flight review, D4 amended), so the group can still be deleted; a chain member takes the due customer's day unconditionally;
- the partial index adds `anonymise_on IS NOT NULL`; orphan contacts are locked before their delete and the run is retried on a deadlock; `anonymise_on` stays set after anonymisation.
