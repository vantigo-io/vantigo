# Merging duplicate customers (phase 6, delivery B) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Two customers that are the same real-world entity become one. `POST /customers/{id}/merge` with `{sourceId, revision?}`: the customer in the path survives and absorbs `sourceId` — its contacts (roles unioned, primaries resolved), addresses (primary-per-type demoted), timeline entries and revisions, tags, and every reference another module holds — in one transaction, and the absorbed customer is archived with `merged_into_customer_id` naming the survivor. The survivor keeps every field of its own row; the absorbed customer's own values are written into the `customer.merged` event. One new permission key (`customers:merge`), one migration (`00029`), two event types, one operation, one many-provider contract slot.

**Architecture:** A new contract, `contracts.CustomerReferenceHolder` (`RepointCustomer(ctx, tx, from, into) ([]RepointedReferences, error)`), is the one sanctioned cross-module write: `module.Module.CustomerReferences` is a many-provider slot like `Workers`, collected by Compose in module order into `Deps.CustomerReferenceHolders` (appended to whatever a harness preset, so `modtest.WithCustomerReferenceHolders` can hand customers fakes). Projects, energy and communications each implement one in their own package over one sqlc query file of their own schema. In customers, `merge.go` runs the whole merge inside `db.RetrySerializable` + `db.WithTx`: `LockCustomer` on both rows in ascending id order, the refusal ladder read under the locks, this module's moves (all set-based statements in a new `queries/merge.sql`), every holder with the same `pgx.Tx`, both revision bumps, the marker, the two events; the survivor is read, decorated and answered after commit. `mergedInto` reaches `SafeCustomerResponse` through the existing batched decoration (one more query per response, never a column on every row type), and `contracts.CustomerEntry.MergedInto` through the directory's two queries. The frontend gains this package's own customer picker (the projects picker's shape, over this module's list with `includeArchived`), a Merge modal on the customer page header behind a new `canMerge` prop, the merged-away banner with every card's capability gated on the customer not being merged away, and a Merge hint on the create and edit forms' duplicate-identity conflict; en + nb.

**Tech Stack:** Go 1.27 (pgx, sqlc, oapi-codegen strict server), PostgreSQL 18, React + Mantine 9 + TanStack Query, vitest, bun, mise.

**Spec:** `docs/superpowers/specs/2026-09-24-customers-merge-design.md` (D1–D5 + "Out of scope" + "Testing"). Read it first; it is binding. Research with file:line pointers: `.superpowers/sdd/2026-09-24-customers-merge/context-for-design.md`. The shapes this delivery copies: `apps/server/internal/customers/contacts.go:343-455` (the contact delete: several customers locked in ascending id order under `db.RetrySerializable`, the actor resolved first), `contacts.go:573-593` (`contactRoleWriteAttempts` and the lock-order cycle it answers), `queries/addresses.sql:1-19` (`LockCustomer`), `owner.go:80-183` (`customerDecoration`, the batched decoration), `duplicates.go` (a refusal's body travelling out of a rolled-back transaction beside a sentinel), `groups.go:80-92` (a coded `CustomerConflictProblem`), `apps/server/internal/module/workers.go` (the many-provider precedent), `apps/projects/frontend/src/components/customer-picker.tsx` (the picker's shape — copied, never imported).

## Global Constraints

- Branch `feat/customers-merge`. Never commit to `main`, never merge, never `--no-verify`.
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
- ONE transaction: `POST /customers/{id}/merge` locks BOTH customer rows in ascending id order (`LockCustomer` twice), moves the customers module's own tables, then calls every `Deps.CustomerReferenceHolders[i].RepointCustomer(ctx, tx, from, into)` INSIDE the same transaction, under `db.RetrySerializable`; a holder error rolls everything back. The actor is resolved before the transaction; no directory lookup inside it.
- `contracts.CustomerReferenceHolder` is the ONE sanctioned cross-module write: a holder runs its own SQL on its own schema in its own package, inside the caller's `pgx.Tx`, never opens a transaction, never reads a directory, tolerates references already pointing at `into` (junction rows kept once), and reports `[]RepointedReferences{Kind, Count}`. `module.Module.CustomerReferences func(Deps) contracts.CustomerReferenceHolder` is a MANY-provider slot (like `Workers`); Compose collects them into `Deps.CustomerReferenceHolders`. Holders: projects, energy, communications. The schema barrier (`internal/db/schema_test.go`) and depguard stay untouched — no customers SQL names another schema.
- New sensitive permission `customers:merge` (delegable), the operation's access `customers:merge+customers:view`. The path customer SURVIVES; `sourceId` is ABSORBED; `revision` (optional) is the survivor's.
- Refusals in order: 404 either; 409 `merge_self`; 409 `merge_type_mismatch`; 409 `merge_into_archived`; 409 `merge_already_merged`. An archived source may be absorbed.
- The survivor keeps EVERY field of its own row; the absorbed customer's row values go into `customer.merged`'s payload (`absorbed: {…}`); contacts move (shared contact keeps the survivor's association, roles unioned, primaries: survivor's stay, absorbed's promoted only where the survivor has none, else demoted); addresses move (primary-per-type demotion); timeline entries + revisions move (`customer_id` rewritten on both; payloads NOT rewritten); tags unioned; the absorbed customer's registry record and Peppol lookup deleted; the absorbed customer set `archived` with `merged_into_customer_id` (migration 00029, in-module FK ON DELETE RESTRICT, partial index); both revisions bump; events `customer.merged` (survivor) and `customer.merged_away` (absorbed), no `status_changed` beside them.
- `SafeCustomerResponse.mergedInto?: {id, customerNumber, name}` (absent unless merged away); `contracts.CustomerEntry.MergedInto *int32`.
- Frontend: a `canMerge` prop (`customers:merge`) from the host; the Merge modal on the customer page header with this package's OWN customer picker (never import projects-ui); a banner on a merged-away customer; the duplicate-identity 409 hint links to the duplicate's page.
- Contract changes to EXISTING schemas stay additive/optional; new schemas may have required fields; the frozen corpus (`openapi/testdata/exchanges/customers.jsonl`) is untouched and must still validate.
- After any `queries/*.sql` or migration change: `mise exec -- go generate ./...` from `apps/server`; new migrations go in `sqlc.yaml` and `internal/db/schema_test.go`; new operations need `operationId`, `x-vantigo-access`, module-test coverage, a `KnownServeMuxConflicts` pin where they conflict, and a COVERAGE.md regeneration.
- Run `mise exec -- bunx biome check --write <files>` on touched frontend files before committing.

---

**How this plan reads the spec where it leaves a choice open.** Each is on the record for the user's verdict (Task 7 Step 4 repeats them):

1. **What a count counts.** `moved` lists every kind a merge looked at, a zero included, this module's four first (`customers.contacts`, `customers.addresses`, `customers.timelineEntries`, `customers.tags`) and then each holder's in Compose order. Each of this module's counts is *what the absorbed customer had*: `customers.contacts` is its associations, a contact the survivor already had included (it was folded, not dropped); `customers.tags` its tag links, one the survivor already carried included; `customers.timelineEntries` its **active** entries (deleted ones move too, but a person reading "14 timeline entries" should find fourteen). The summary names only non-zero kinds, and a kind this module has no word for reads "4 × kind".
2. **The race that is retried is the contact delete, not the attach.** The spec's testing section says "a merge racing a contact attach — retried". An attach takes the customer row first and the contact second, exactly as a merge does (customers first, then — moving an association — a key-share on the contact), so the two serialize and cannot cycle. `DELETE /customers/contacts/{id}` takes the contact first and the customers after, which is the one cycle `mergeWriteAttempts` exists for (the same reason `contactRoleWriteAttempts` gives). The concurrency test pins that race; the double-merge race pins `merge_already_merged`.
3. **A re-pointed project's revision advances and nothing else is written in projects.** Changing a project's customer is a change to the row, so an edit form opened before the merge — still holding the absorbed customer — answers the stale-revision 409 instead of writing it back. No project timeline entry: the merge is recorded on the survivor's customer timeline, and `customerName` reads the survivor's from the moment it commits. Energy and communications rows carry no revision and are only re-pointed.
4. **The 50-address cap guards a write, not a merge.** Every address moves (D3) even when the survivor ends past 50; its next address POST answers the cap until it is back under. Refusing the merge instead would leave two duplicates nobody could fold.
5. **`docs/energy.md` does not exist** (energy has no module doc — only its inventory spec). D5's energy paragraph goes into `docs/customers.md`'s holder table instead of a one-paragraph new file; projects and communications get theirs in their own docs.
6. **A merged-away customer stays writable on the server.** The spec names no refusal for edits of an absorbed customer, and it is an archived customer like any other (restoring one works the same way). The page shows no edit action on it anywhere (controller ruling on the pre-flight review): the header hides Edit, Change type, Archive, Restore and Merge, and every card gets `canX && !customer.mergedInto`. The marker survives any edit made through the API. The 400 for a missing or non-positive `sourceId` is the one refusal added beside the ladder; the stale revision comes after `merge_already_merged`.
7. **The Merge hint shows on both the create form's and the edit form's duplicate-identity conflict, for a caller who may merge** (D4; controller ruling on the pre-flight review), in two wordings: on an edit the fix is a merge from the duplicate's page; on a create the new customer does not exist yet, so the line says to open the duplicate instead — or create anyway and merge there. `canMerge` reaches `CustomerFormModal` from both the customer page and the list (the list opens the same form).
8. **The picker is the list endpoint with `includeArchived`, filtered in the browser.** An archived duplicate is the common case (D2), so the search must reach archived customers; this customer and merged-away ones are dropped client-side, so a page of twenty may show nineteen. No server filter was added.
9. **The integration test composes customers and projects only**, not energy and communications. Each holder's SQL is proved in its own package against the real schema through a real transaction, and Compose's collection is proved in `internal/module`; the integration test is where the real merge drives a real holder end to end, and one holder does that. Adding the other two to `moduleNamed` would need communications' storage environment and both contracts in the merged recorder to prove Compose a second time.
10. **Three holders, one task.** Each is one sqlc file, twenty lines of Go and one package test of the same shape; a reviewer reads them side by side. They land in one commit, after the contract and before the merge that calls them.
11. **This module's merge statements live in one new `queries/merge.sql`**, not spread over `contacts.sql`/`addresses.sql`/`timeline.sql`/`tags.sql`: each is set-based and meaningful only inside a merge, and one file is one place to read what a merge writes. The registry and Peppol deletes reuse the existing `DeleteCustomerRegistryRecord`/`DeleteCustomerPeppolLookup`.

Smaller readings, stated where they are implemented: a moved address's `updated_at` is the merge's; a moved timeline entry's is not (a move is not an edit, and its revision history says so); the summary's `#` is the customer number, as the duplicate-identity list shows it.

## File Structure

| File | Responsibility |
| --- | --- |
| `apps/server/internal/contracts/references.go` | `CustomerReferenceHolder`, `RepointedReferences` (Task 1) |
| `apps/server/internal/module/module.go`, `compose.go`, `compose_test.go` | the many-provider slot and its collection (Task 1) |
| `apps/server/internal/modtest/modtest.go` | `WithCustomerReferenceHolders` (Task 1) |
| `docs/module-boundaries.md` | rule 5 widened, rule 8 (Task 1) |
| `apps/server/internal/{projects,energy,communications}/customer_references.go`, `customer_references_test.go`, `queries/customer_references.sql` (+ generated `store/customer_references.sql.go`), `module.go` | the three holders (Task 2) |
| `apps/server/internal/db/migrations/00029_customers_merge.sql`, `internal/customers/sqlc.yaml`, `internal/db/schema_test.go` | the marker column (Task 3) |
| `apps/server/internal/customers/queries/merge.sql`, `queries/customers.sql` (+ generated `store/merge.sql.go`, `store/customers.sql.go`, `store/models.go`) | the moves, the marker, the decoration's read, the directory's column (Task 3) |
| `openapi/customers.yaml` (+ generated `internal/openapi/specs/customers.yaml`, `internal/customers/gen/api.gen.go`, `apps/customers/frontend/src/api-schema.d.ts`), `openapi/COVERAGE.md` | the operation, three schemas, `mergedInto` (Task 3) |
| `apps/server/internal/contracts/directory.go`, `internal/customers/directory.go` | `CustomerEntry.MergedInto` (Task 3) |
| `apps/server/internal/customers/merge.go`, `timeline_events.go`, `owner.go`, `customers.go`, `module.go` | the merge, its events, `mergedInto`, the permission (Task 3) |
| `apps/server/internal/customers/merge_test.go`, `merge_concurrency_test.go`, `merge_internal_test.go`, `module_test.go`, `customers_test.go` | its tests (Task 3) |
| `apps/server/internal/integration/merge_test.go` | customers + projects for real (Task 4) |
| `docs/customers.md`, `docs/projects.md`, `docs/communications.md`, `ROADMAP.md` | D5 (Task 5) |
| `apps/customers/frontend/src/api/customers.ts`, `api/customers.test.ts`, `api/merge.ts`, `api/merge.test.ts` | `mergedInto`, `includeArchived`, the call (Task 6) |
| `apps/customers/frontend/src/components/customer-picker.tsx`, `customer-picker.test.tsx` | this package's own picker (Task 6) |
| `apps/customers/frontend/src/pages/-customer-merge-modal.tsx`, `-customer-merge-modal.test.tsx`, `customers.$customerId.tsx`, `-customer-merge-header.test.tsx`, `-customer-contacts-card.tsx`, `-customer-form-modal.tsx`, `-customer-form-modal.test.tsx`, `customers.index.tsx`, `i18n.ts` | the modal, the header, the banner, the read-only cards, the hints, the strings (Task 6) |
| `apps/host/frontend/src/routes/customers/-customer-detail-layout.tsx`, `customer-detail-route.test.tsx`, `-customers-list.tsx`, `customers-list-route.test.tsx`, `apps/host/frontend/src/catalogs/admin.ts`, `catalogs/admin.test.ts` | `canMerge`, the permission's labels (Task 6) |

---

### Task 1: The contract and the many-provider slot (D1)

The write direction, with nobody calling it yet: the interface, the slot, Compose's collection, the harness seam and the boundary rule that says why it is allowed.

**Files:**
- Create: `apps/server/internal/contracts/references.go`
- Modify: `apps/server/internal/module/module.go`, `apps/server/internal/module/compose.go`, `apps/server/internal/module/compose_test.go`, `apps/server/internal/modtest/modtest.go`, `docs/module-boundaries.md`
- Read first (do not change): `apps/server/internal/module/workers.go` (the many-provider precedent), `compose_test.go:719-800` and `:988-1013` (the provider-slot tests and the preset-survives seam)

**Interfaces:**
- Produces Go: `contracts.CustomerReferenceHolder` (`RepointCustomer(ctx context.Context, tx pgx.Tx, from, into int32) ([]contracts.RepointedReferences, error)`); `contracts.RepointedReferences{Kind string; Count int64}`; `module.Module.CustomerReferences func(module.Deps) contracts.CustomerReferenceHolder`; `module.Deps.CustomerReferenceHolders []contracts.CustomerReferenceHolder`; `modtest.WithCustomerReferenceHolders(holders ...contracts.CustomerReferenceHolder) modtest.Option`.
- Wire: none.
- Consumes: nothing new.

- [ ] **Step 1: Write the failing Compose tests**

In `apps/server/internal/module/compose_test.go`, add `"slices"` to the standard-library imports and `"github.com/jackc/pgx/v5"` to the third-party group (beside `openapi3` and `uuid`), then append at the end of the file:

```go
// fakeReferenceHolder is a contracts.CustomerReferenceHolder with only a name,
// so a test can tell whose holder landed where. It is never called: Compose
// collects holders, and only the module that merges customers calls them.
type fakeReferenceHolder struct{ name string }

func (*fakeReferenceHolder) RepointCustomer(context.Context, pgx.Tx, int32, int32) ([]contracts.RepointedReferences, error) {
	return nil, nil
}

// Customer reference holders are the many-provider slot (customers merge design
// D1): unlike Directory, every enabled module may declare one, and Compose
// collects them all, in the order the modules were given — the order the merge
// calls them in, the same on every run. Every module's Mount sees the one list:
// a provider's own, and one that provides nothing.
func TestCompose_CollectsEveryCustomerReferenceHolderInModuleOrder(t *testing.T) {
	alpha, gamma := &fakeReferenceHolder{name: "alpha"}, &fakeReferenceHolder{name: "gamma"}
	var inAlpha, inBeta []contracts.CustomerReferenceHolder

	_, err := compose(Deps{Access: &fakeAccess{}},
		fakeLoad(map[string]string{"alpha": alphaContract, "beta": betaContract, "gamma": gammaContract}),
		Module{
			Name:               "alpha",
			CustomerReferences: func(Deps) contracts.CustomerReferenceHolder { return alpha },
			Mount: func(d Deps) (http.Handler, error) {
				inAlpha = d.CustomerReferenceHolders
				return staticHandler("alpha")(d)
			},
		},
		Module{Name: "beta", Mount: func(d Deps) (http.Handler, error) {
			inBeta = d.CustomerReferenceHolders
			return staticHandler("beta")(d)
		}},
		Module{
			Name:               "gamma",
			CustomerReferences: func(Deps) contracts.CustomerReferenceHolder { return gamma },
			Mount:              staticHandler("gamma"),
		},
	)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	want := []contracts.CustomerReferenceHolder{alpha, gamma}
	if !slices.Equal(inAlpha, want) {
		t.Errorf("alpha's Deps.CustomerReferenceHolders = %v, want alpha's then gamma's", inAlpha)
	}
	if !slices.Equal(inBeta, want) {
		t.Errorf("beta's Deps.CustomerReferenceHolders = %v, want alpha's then gamma's", inBeta)
	}
}

// A module MODULES leaves out contributes no holder even when it declares one,
// exactly as it contributes no directory — and with no holder anywhere the
// slice stays nil rather than becoming an empty one Compose invented.
func TestCompose_DisabledModuleContributesNoCustomerReferenceHolder(t *testing.T) {
	got := []contracts.CustomerReferenceHolder{&fakeReferenceHolder{}} // non-nil, so a no-op is caught

	_, err := compose(
		Deps{Access: &fakeAccess{}, Config: &config.Config{Modules: []string{"alpha"}}},
		fakeLoad(map[string]string{"alpha": alphaContract, "beta": betaContract}),
		Module{Name: "alpha", Mount: func(d Deps) (http.Handler, error) {
			got = d.CustomerReferenceHolders
			return staticHandler("alpha")(d)
		}},
		Module{
			Name:               "beta",
			CustomerReferences: func(Deps) contracts.CustomerReferenceHolder { return &fakeReferenceHolder{name: "beta"} },
			Mount:              staticHandler("beta"),
		},
	)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if got != nil {
		t.Errorf("Deps.CustomerReferenceHolders = %v, want nil: beta, the only holder, is disabled", got)
	}
}

// Holders a caller preset on Deps — modtest.WithCustomerReferenceHolders' seam —
// survive Compose and come first, and Compose appends the composed modules'
// own to a copy: the caller's backing array is never written through.
func TestCompose_PresetCustomerReferenceHoldersComeFirstAndAreNotWrittenThrough(t *testing.T) {
	preset := &fakeReferenceHolder{name: "preset"}
	own := &fakeReferenceHolder{name: "alpha"}
	presetList := make([]contracts.CustomerReferenceHolder, 1, 4)
	presetList[0] = preset
	var got []contracts.CustomerReferenceHolder

	_, err := compose(Deps{Access: &fakeAccess{}, CustomerReferenceHolders: presetList},
		fakeLoad(map[string]string{"alpha": alphaContract}),
		Module{
			Name:               "alpha",
			CustomerReferences: func(Deps) contracts.CustomerReferenceHolder { return own },
			Mount: func(d Deps) (http.Handler, error) {
				got = d.CustomerReferenceHolders
				return staticHandler("alpha")(d)
			},
		},
	)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if want := []contracts.CustomerReferenceHolder{preset, own}; !slices.Equal(got, want) {
		t.Errorf("Deps.CustomerReferenceHolders = %v, want the preset then alpha's", got)
	}
	if spare := presetList[:2]; spare[1] != nil {
		t.Errorf("Compose wrote %v into the caller's own backing array", spare[1])
	}
}
```

- [ ] **Step 2: Run them and watch them fail**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- go test -count=1 -run 'CustomerReferenceHolder' ./internal/module/
```
Expected: FAIL to compile — `undefined: contracts.CustomerReferenceHolder`, `unknown field CustomerReferences in struct literal of type Module`, `unknown field CustomerReferenceHolders in struct literal of type Deps`.

- [ ] **Step 3: The contract**

Create `apps/server/internal/contracts/references.go`:

```go
package contracts

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// CustomerReferenceHolder is a module that stores customer ids in its own
// schema, and the one sanctioned cross-module WRITE (customers merge design
// D1). Every other contract here is a read. This one exists because merging
// two customers must move every reference to the absorbed one, and every
// module shares one database and one pool, so the move can be a single
// transaction with no event bus: the customers module opens it, locks both
// customer rows, moves its own tables, and hands the same transaction to each
// holder in turn. A holder runs its own SQL, on its own schema, from its own
// package — depguard and internal/db/schema_test.go hold exactly as they do
// for a read — and an error anywhere rolls back every module's part together.
//
// tx is a platform type, not a store type, so rule 3 of
// docs/module-boundaries.md (contracts carry no store types) still holds: the
// holder builds its own store over it.
//
// RepointCustomer moves every reference from `from` to `into` inside tx and
// reports what it moved, kind by kind, for the merge's answer and its timeline
// event. The rules a holder keeps, none of which the signature shows:
//
//   - It never begins, commits or rolls back a transaction, and never touches
//     a pool: tx is the caller's, and so is the decision.
//   - It never reads a directory or any other contract. The caller holds two
//     customer rows locked, and no in-process lookup happens under a lock in
//     this codebase (the customers module's actor.go); a holder needs none —
//     both ids are known to exist.
//   - It tolerates a reference that already points at `into`: a junction row
//     that exists for both customers is kept once, never a unique violation.
//   - It reports every kind it handles, a zero count included, so the answer
//     says what was looked at as well as what moved. A kind is
//     "<module>.<what>" in the API's camelCase: "projects.projects",
//     "energy.supplyPeriods".
type CustomerReferenceHolder interface {
	RepointCustomer(ctx context.Context, tx pgx.Tx, from, into int32) ([]RepointedReferences, error)
}

// RepointedReferences is one kind of reference a holder re-pointed, and how
// many rows of it.
type RepointedReferences struct {
	Kind  string
	Count int64
}
```

- [ ] **Step 4: The slot and its collection**

In `apps/server/internal/module/module.go`, add to `Deps` directly after the `Expenses contracts.ProjectExpenses` field:

```go
	// CustomerReferenceHolders are every enabled module's
	// contracts.CustomerReferenceHolder (customers merge design D1), in the
	// order the modules were given to Compose — the one many-provider contract
	// slot. Compose collects them before any Mount runs, after the six
	// single-provider slots, and appends them to whatever the caller preset
	// here (the seam modtest.WithCustomerReferenceHolders fills), so every
	// module's Deps carries the same list. Only the module that merges
	// customers calls them, and only inside its merge transaction. nil when no
	// enabled module holds customer ids.
	CustomerReferenceHolders []contracts.CustomerReferenceHolder
```

and to `Module` directly after the `Workers func(Deps) []worker.Worker` field:

```go
	// CustomerReferences builds this module's
	// contracts.CustomerReferenceHolder, if it stores customer ids in its own
	// schema (customers merge design D1). Unlike Directory, and like Workers,
	// any number of enabled modules may set it: Compose calls every one, in
	// mods order, before any Mount runs, and puts the list on every module's
	// Deps as CustomerReferenceHolders. A module holding no customer ids leaves
	// it nil — time and expenses reach a customer only through a project.
	CustomerReferences func(Deps) contracts.CustomerReferenceHolder
```

In `apps/server/internal/module/compose.go`, insert directly after the `if expensesProvider != nil { deps.Expenses = expensesProvider.Expenses(deps) }` block, before `outer := http.NewServeMux()`:

```go
	// Customer reference holders are the one many-provider contract slot
	// (contracts.CustomerReferenceHolder, customers merge design D1): every
	// enabled module that declares one contributes it, in mods order, so the
	// merge that calls them does so in the same order on every run. They are
	// resolved last, on deps as the single slots left it, and appended to
	// whatever the caller preset — onto a copy, so a harness's own slice is
	// never written through.
	var holders []contracts.CustomerReferenceHolder
	for _, mod := range mods {
		if mod.CustomerReferences == nil {
			continue
		}
		if holder := mod.CustomerReferences(deps); holder != nil {
			holders = append(holders, holder)
		}
	}
	if len(holders) > 0 {
		deps.CustomerReferenceHolders = append(slices.Clone(deps.CustomerReferenceHolders), holders...)
	}
```

In `Compose`'s own doc comment, change `project expenses (naming both), or a nil Deps.Config:` to `project expenses (naming both) — customer reference holders, the one many-provider slot, are collected from every enabled module instead — or a nil Deps.Config:`.

- [ ] **Step 5: The harness seam**

In `apps/server/internal/modtest/modtest.go`: add `holders []contracts.CustomerReferenceHolder` to `setup` directly after `expenses     contracts.ProjectExpenses` (gofmt realigns the block); add the option directly after `WithExpenses`:

```go
// WithCustomerReferenceHolders adds holders to Deps.CustomerReferenceHolders,
// for the module that merges customers (customers merge design D1) to be
// tested against fakes that record what they were asked to re-point — or
// fail, to prove a merge rolls back — without composing projects, energy or
// communications beside it: depguard keeps those out of customers' tests.
// module.Compose appends the composed modules' own holders after these, so a
// value set here survives Compose; given more than once, the holders
// accumulate in order.
func WithCustomerReferenceHolders(holders ...contracts.CustomerReferenceHolder) Option {
	return func(s *setup) { s.holders = append(s.holders, holders...) }
}
```

and in `New`'s `h.deps = module.Deps{…}` literal, directly after `Expenses:      s.expenses,`:

```go
		CustomerReferenceHolders: s.holders,
```

- [ ] **Step 6: The boundary rule**

In `docs/module-boundaries.md`, rule 5: after the sentence ending `…so those providers' *constructors* are allowed
   to read the project directory (neither does, but a future provider could).` add, still inside item 5:

```markdown
   Two slots are *many-provider* instead — any number of enabled modules may
   fill them, and Compose collects them in module order: `Workers` (background
   work) and `CustomerReferences` (`contracts.CustomerReferenceHolder`, rule 8).
```

Rule 3: after `…so any module or a future extracted service can implement or consume them.` add, inside item 3:

```markdown
   The one non-DTO type is `pgx.Tx` in `contracts.CustomerReferenceHolder` —
   a platform type, never a store type, and the reason is rule 8's.
```

After rule 7 (the frontend rule), add:

```markdown
8. **One sanctioned cross-module write.** A module never writes another's data —
   with one exception, made for merging customers:
   `contracts.CustomerReferenceHolder`. A module that stores customer ids in its
   own schema declares `Module.CustomerReferences`, and the customers module's
   merge calls every holder **inside its own transaction**, which already holds
   both customer rows locked. The holder runs its own SQL, on its own schema,
   from its own package — so rules 1, 4 and 6 hold as they do for a read — never
   begins or ends a transaction, never reads a directory (no in-process lookup
   happens under a lock anywhere in this codebase), and tolerates a reference
   that already points at the surviving customer. Today's holders are projects,
   energy and communications ([Merging duplicates](customers.md#merging-duplicates)).
   Another write direction needs a design of its own, not a second
   holder-shaped interface.
```

Under "How they are enforced", after the rule-7 bullet, add:

```markdown
- **Rule 8**: by shape and by test. A holder is handed a `pgx.Tx` and nothing
  else it could write with; its SQL lives in its own `queries/`, so rule 4's
  scan covers it; each holder's own package test re-points real rows through a
  real transaction, and the customers module's merge tests prove that a
  holder's error rolls the whole merge back.
```

- [ ] **Step 7: Run them, show they can fail, commit**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- gofmt -w internal/contracts/references.go internal/module/module.go internal/module/compose.go internal/module/compose_test.go internal/modtest/modtest.go && mise exec -- gofmt -l internal
mise exec -- go vet ./internal/module/... ./internal/contracts/... ./internal/modtest/... && mise exec -- go build ./...
mise exec -- go test -count=1 ./internal/module/...
```
Expected: PASS, the existing Compose tests included (nothing declares the slot yet, so every other composition is unchanged).

Prove each new test can fail, restoring after each: replace `append(slices.Clone(deps.CustomerReferenceHolders), holders...)` with `holders` — the preset test goes red (the preset is gone); replace it with `append(deps.CustomerReferenceHolders, holders...)` — the preset test goes red on the write-through check; iterate `mods` backwards (`for i := len(mods) - 1; i >= 0; i--` with `mod := mods[i]`) — the order test goes red; delete `mods = enabledModules(deps, mods)` at the top of `composeFrom` — the disabled test goes red (beta's holder arrives). Say what each printed.

```bash
cd /home/anders/projects/vantigo/vantigo
cat > /tmp/claude-1000/msg-merge-1.txt <<'EOF'
feat(contracts): modules that hold customer ids can re-point them inside a merge

contracts.CustomerReferenceHolder is the one sanctioned cross-module
write: RepointCustomer moves every reference from one customer to
another inside the caller's transaction, on the holder's own schema, and
reports what it moved kind by kind. module.Module.CustomerReferences is a
many-provider slot like Workers — Compose collects every enabled module's
holder, in module order, onto Deps.CustomerReferenceHolders, appended to
a copy of whatever a harness preset — and modtest.WithCustomerReferenceHolders
is that seam. docs/module-boundaries.md gains rule 8, which says why this
write is allowed and what a holder may and may not do.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
PATHS="apps/server/internal/contracts/references.go apps/server/internal/module/module.go \
 apps/server/internal/module/compose.go apps/server/internal/module/compose_test.go \
 apps/server/internal/modtest/modtest.go docs/module-boundaries.md"
git add $PATHS && git commit -F /tmp/claude-1000/msg-merge-1.txt -- $PATHS
git show --stat HEAD && git status --short
```

---
### Task 2: The three holders (D1)

Projects, energy and communications each implement the contract over one query file of their own schema, declare it on `Module()`, and prove it in their own package: real rows, a real transaction, the counts, the rollback, and — for communications — the junction row present for both customers. Nothing calls them yet; Task 3's merge is their caller.

**Files:**
- Create: `apps/server/internal/projects/customer_references.go`, `projects/queries/customer_references.sql`, `projects/customer_references_test.go`; `apps/server/internal/energy/customer_references.go`, `energy/queries/customer_references.sql`, `energy/customer_references_test.go`; `apps/server/internal/communications/customer_references.go`, `communications/queries/customer_references.sql`, `communications/customer_references_test.go`
- Modify: `apps/server/internal/projects/module.go`, `energy/module.go`, `communications/module.go`
- Generated: `apps/server/internal/{projects,energy,communications}/store/customer_references.sql.go`
- Read first (do not change): `apps/server/internal/db/migrations/00008_projects_baseline.sql:11-31`, `00005_energy_baseline.sql:71-100`, `00006_communications_baseline.sql:87-141`; `projects/directory_test.go:15-60` (building a provider from `h.Deps()`, the capture-module trick); `energy/stats_test.go:20-33` (`insertActiveSupplyPeriod`); `communications/ai_test.go:161-171` (`insertCandidate`), `communications/conversations_test.go:113-117` (`setupChannel`)

**Interfaces:**
- Produces Go: in each of the three packages, `newCustomerReferenceHolder(module.Deps) contracts.CustomerReferenceHolder` set as `Module().CustomerReferences`; `store.RepointProjectsCustomer(ctx, store.RepointProjectsCustomerParams{IntoCustomerID, Now, FromCustomerID}) (int64, error)`; `store.RepointSupplyPeriodsCustomer(ctx, store.RepointSupplyPeriodsCustomerParams{IntoCustomerID, FromCustomerID}) (int64, error)`; `store.RepointConversationsCustomer`, `store.RepointConversationsSuggestedCustomer` (both `(int64, error)`), `store.RepointConversationCustomerCandidates(…) (store.RepointConversationCustomerCandidatesRow{Moved, Added int64}, error)`.
- Kinds: `projects.projects`; `energy.supplyPeriods`; `communications.conversations`, `communications.conversationSuggestions`, `communications.conversationCandidates`.
- Wire: none.
- Consumes: Task 1's contract and slot.

- [ ] **Step 1: Write the failing tests**

Create `apps/server/internal/projects/customer_references_test.go`:

```go
package projects_test

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/projects"
)

// repointProjectsCustomer runs the module's own holder, built from the
// harness's dependencies exactly as module.Compose builds it, inside a
// transaction the test owns — the customers merge's position — and commits it
// or rolls it back as told.
func repointProjectsCustomer(t *testing.T, h *modtest.Harness, from, into int32, commit bool) []contracts.RepointedReferences {
	t.Helper()
	build := projects.Module().CustomerReferences
	if build == nil {
		t.Fatal("the module declares no customer reference holder")
	}
	ctx := context.Background()
	tx, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	moved, err := build(h.Deps()).RepointCustomer(ctx, tx, from, into)
	if err != nil {
		t.Fatalf("RepointCustomer(%d, %d): %v", from, into, err)
	}
	if commit {
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit: %v", err)
		}
	}
	return moved
}

// insertProjectFor writes a project row directly: the holder's subject is the
// column, and the create endpoint's own rules (a customer the directory knows,
// a suggested code) are not what this file tests. customerID nil is an
// internal project.
func insertProjectFor(t *testing.T, h *modtest.Harness, code string, customerID *int32) int32 {
	t.Helper()
	return modtest.One[int32](t, h, `
		INSERT INTO projects.projects (code, name, customer_id, billing_type, created_by_user_id, created_at, updated_at)
		VALUES ($1, $1, $2, 'time-and-materials', $3, $4, $4)
		RETURNING id`, code, customerID, uuid.New(), h.Now())
}

// TestCustomerReferences_RepointsEveryProjectOfTheAbsorbedCustomer is this
// module's half of a customer merge (customers merge design D1): every project
// billed to the absorbed customer bills to the survivor, each moved row's
// revision advances as any change to it does, and nothing else moves — not the
// survivor's own project, not another customer's, not an internal one.
func TestCustomerReferences_RepointsEveryProjectOfTheAbsorbedCustomer(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	absorbed, survivor, other := int32(customerKraftVerket), int32(customerAcme), int32(customerArchived)
	first := insertProjectFor(t, h, "MRG1000", &absorbed)
	second := insertProjectFor(t, h, "MRG1001", &absorbed)
	own := insertProjectFor(t, h, "MRG1002", &survivor)
	elsewhere := insertProjectFor(t, h, "MRG1003", &other)
	internal := insertProjectFor(t, h, "MRG1004", nil)
	h.Advance(time.Hour)

	moved := repointProjectsCustomer(t, h, absorbed, survivor, true)

	if want := []contracts.RepointedReferences{{Kind: "projects.projects", Count: 2}}; !slices.Equal(moved, want) {
		t.Errorf("RepointCustomer = %+v, want %+v", moved, want)
	}
	for _, id := range []int32{first, second, own} {
		if got := modtest.One[int32](t, h, `SELECT customer_id FROM projects.projects WHERE id = $1`, id); got != survivor {
			t.Errorf("project %d customer_id = %d, want the survivor %d", id, got, survivor)
		}
	}
	if got := modtest.One[int32](t, h, `SELECT customer_id FROM projects.projects WHERE id = $1`, elsewhere); got != other {
		t.Errorf("another customer's project moved to %d", got)
	}
	if n := h.Count(t, `SELECT count(*) FROM projects.projects WHERE id = $1 AND customer_id IS NULL`, internal); n != 1 {
		t.Error("the internal project gained a customer")
	}
	if got := modtest.One[int32](t, h, `SELECT revision FROM projects.projects WHERE id = $1`, first); got != 2 {
		t.Errorf("a moved project's revision = %d, want 2", got)
	}
	if got := modtest.One[time.Time](t, h, `SELECT updated_at FROM projects.projects WHERE id = $1`, first); !got.Equal(h.Now()) {
		t.Errorf("a moved project's updated_at = %v, want the merge's %v", got, h.Now())
	}
	if got := modtest.One[int32](t, h, `SELECT revision FROM projects.projects WHERE id = $1`, own); got != 1 {
		t.Errorf("the survivor's own project was written: revision %d", got)
	}
}

// The holder writes only through the transaction it is handed: rolled back,
// nothing moved — which is what lets a merge's later failure undo it. A
// customer with no projects is a zero, not an absent kind.
func TestCustomerReferences_WritesOnlyInsideTheCallersTransaction(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	absorbed, survivor := int32(customerKraftVerket), int32(customerAcme)
	id := insertProjectFor(t, h, "MRG2000", &absorbed)

	if moved := repointProjectsCustomer(t, h, absorbed, survivor, false); len(moved) != 1 || moved[0].Count != 1 {
		t.Fatalf("RepointCustomer = %+v, want one project", moved)
	}
	if got := modtest.One[int32](t, h, `SELECT customer_id FROM projects.projects WHERE id = $1`, id); got != absorbed {
		t.Errorf("after a rollback customer_id = %d, want %d still", got, absorbed)
	}

	want := []contracts.RepointedReferences{{Kind: "projects.projects", Count: 0}}
	if moved := repointProjectsCustomer(t, h, customerUnknown, survivor, true); !slices.Equal(moved, want) {
		t.Errorf("RepointCustomer for a customer with no projects = %+v, want %+v", moved, want)
	}
}

// Compose puts projects' holder on every module's Deps once projects is
// composed — the capture module is TestModule_ComposesProjectDirectoryOntoDeps'
// own, named "customers" so a real embedded contract loads for it.
func TestModule_ComposesItsCustomerReferenceHolderOntoDeps(t *testing.T) {
	t.Parallel()
	var got []contracts.CustomerReferenceHolder
	capture := module.Module{
		Name: "customers",
		Mount: func(d module.Deps) (http.Handler, error) {
			got = d.CustomerReferenceHolders
			return http.NotFoundHandler(), nil
		},
	}

	newHarness(t, modtest.WithModule(capture))

	if len(got) != 1 {
		t.Errorf("Deps.CustomerReferenceHolders = %v, want projects' one holder", got)
	}
}
```

Create `apps/server/internal/energy/customer_references_test.go`:

```go
package energy_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/energy"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// repointEnergyCustomer runs the module's own holder inside a transaction the
// test owns — the customers merge's position — and commits it or rolls it back
// as told.
func repointEnergyCustomer(t *testing.T, h *modtest.Harness, from, into int32, commit bool) []contracts.RepointedReferences {
	t.Helper()
	build := energy.Module().CustomerReferences
	if build == nil {
		t.Fatal("the module declares no customer reference holder")
	}
	ctx := context.Background()
	tx, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	moved, err := build(h.Deps()).RepointCustomer(ctx, tx, from, into)
	if err != nil {
		t.Fatalf("RepointCustomer(%d, %d): %v", from, into, err)
	}
	if commit {
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit: %v", err)
		}
	}
	return moved
}

// TestCustomerReferences_RepointsEverySupplyPeriodOfTheAbsorbedCustomer is
// energy's half of a customer merge (customers merge design D1): every supply
// period of the absorbed customer supplies the survivor. The overlap
// constraint is per metering point, so the survivor already having a period
// on the same point is no conflict; the survivor's own period is not written.
func TestCustomerReferences_RepointsEverySupplyPeriodOfTheAbsorbedCustomer(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	point := createMeteringPoint(t, h.SignIn(t, allEnergyPermissions...))
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	first := insertActiveSupplyPeriod(t, h, point.Id, 1001, start, start.AddDate(0, 1, 0))
	second := insertActiveSupplyPeriod(t, h, point.Id, 1001, start.AddDate(0, 2, 0), start.AddDate(0, 3, 0))
	own := insertActiveSupplyPeriod(t, h, point.Id, 1002, start.AddDate(0, 4, 0), start.AddDate(0, 5, 0))

	moved := repointEnergyCustomer(t, h, 1001, 1002, true)

	if want := []contracts.RepointedReferences{{Kind: "energy.supplyPeriods", Count: 2}}; !slices.Equal(moved, want) {
		t.Errorf("RepointCustomer = %+v, want %+v", moved, want)
	}
	for _, id := range []int32{first, second, own} {
		if got := modtest.One[int32](t, h, `SELECT customer_id FROM energy.supply_periods WHERE id = $1`, id); got != 1002 {
			t.Errorf("supply period %d customer_id = %d, want 1002", id, got)
		}
	}
}

// Rolled back, nothing moved; a customer with no supply periods is a zero.
func TestCustomerReferences_WritesOnlyInsideTheCallersTransaction(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	point := createMeteringPoint(t, h.SignIn(t, allEnergyPermissions...))
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	id := insertActiveSupplyPeriod(t, h, point.Id, 1001, start, start.AddDate(0, 1, 0))

	if moved := repointEnergyCustomer(t, h, 1001, 1002, false); len(moved) != 1 || moved[0].Count != 1 {
		t.Fatalf("RepointCustomer = %+v, want one supply period", moved)
	}
	if got := modtest.One[int32](t, h, `SELECT customer_id FROM energy.supply_periods WHERE id = $1`, id); got != 1001 {
		t.Errorf("after a rollback customer_id = %d, want 1001 still", got)
	}
	want := []contracts.RepointedReferences{{Kind: "energy.supplyPeriods", Count: 0}}
	if moved := repointEnergyCustomer(t, h, 4242, 1002, true); !slices.Equal(moved, want) {
		t.Errorf("RepointCustomer for a customer with no supply periods = %+v, want %+v", moved, want)
	}
}
```

Create `apps/server/internal/communications/customer_references_test.go`:

```go
package communications_test

import (
	"context"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/communications"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// repointCommunicationsCustomer runs the module's own holder inside a
// transaction the test owns — the customers merge's position — and commits it
// or rolls it back as told.
func repointCommunicationsCustomer(t *testing.T, h *modtest.Harness, from, into int32, commit bool) []contracts.RepointedReferences {
	t.Helper()
	build := communications.Module().CustomerReferences
	if build == nil {
		t.Fatal("the module declares no customer reference holder")
	}
	ctx := context.Background()
	tx, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	moved, err := build(h.Deps()).RepointCustomer(ctx, tx, from, into)
	if err != nil {
		t.Fatalf("RepointCustomer(%d, %d): %v", from, into, err)
	}
	if commit {
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit: %v", err)
		}
	}
	return moved
}

// insertConversationFor writes a conversation row directly with the two
// customer columns the holder re-points; nil leaves one unset.
func insertConversationFor(t *testing.T, h *modtest.Harness, channelID string, customerID, suggestedID *int32) uuid.UUID {
	t.Helper()
	id := uuid.New()
	h.Exec(t, `INSERT INTO communications.conversations (id, channel_id, status, customer_id, suggested_customer_id, last_activity_at, created_at)
	           VALUES ($1, $2, 'open', $3, $4, $5, $5)`, id, uuid.MustParse(channelID), customerID, suggestedID, h.Now())
	return id
}

// TestCustomerReferences_RepointsConversationsSuggestionsAndCandidates is
// communications' half of a customer merge (customers merge design D1): both
// customer columns of a conversation, and the candidate list — where a
// conversation that already lists the survivor keeps it once instead of
// colliding with its primary key, and the absorbed customer's row is gone
// either way.
func TestCustomerReferences_RepointsConversationsSuggestionsAndCandidates(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	channel := setupChannel(t, h)
	absorbed, survivor := int32(1001), int32(1002)
	associated := insertConversationFor(t, h, channel, &absorbed, nil)
	suggested := insertConversationFor(t, h, channel, &survivor, &absorbed)
	untouched := insertConversationFor(t, h, channel, &survivor, nil)
	both := insertConversationFor(t, h, channel, nil, nil)
	insertCandidate(t, h, both.String(), absorbed)
	insertCandidate(t, h, both.String(), survivor)
	onlyAbsorbed := insertConversationFor(t, h, channel, nil, nil)
	insertCandidate(t, h, onlyAbsorbed.String(), absorbed)

	moved := repointCommunicationsCustomer(t, h, absorbed, survivor, true)

	want := []contracts.RepointedReferences{
		{Kind: "communications.conversations", Count: 1},
		{Kind: "communications.conversationSuggestions", Count: 1},
		{Kind: "communications.conversationCandidates", Count: 2},
	}
	if !slices.Equal(moved, want) {
		t.Errorf("RepointCustomer = %+v, want %+v", moved, want)
	}
	if got := modtest.One[int32](t, h, `SELECT customer_id FROM communications.conversations WHERE id = $1`, associated); got != survivor {
		t.Errorf("the associated conversation's customer_id = %d, want %d", got, survivor)
	}
	if got := modtest.One[int32](t, h, `SELECT suggested_customer_id FROM communications.conversations WHERE id = $1`, suggested); got != survivor {
		t.Errorf("the suggestion = %d, want %d", got, survivor)
	}
	if got := modtest.One[int32](t, h, `SELECT customer_id FROM communications.conversations WHERE id = $1`, untouched); got != survivor {
		t.Errorf("the survivor's own conversation moved to %d", got)
	}
	for _, conversation := range []uuid.UUID{both, onlyAbsorbed} {
		if n := h.Count(t, `SELECT count(*) FROM communications.conversation_customer_candidates WHERE conversation_id = $1 AND customer_id = $2`, conversation, survivor); n != 1 {
			t.Errorf("conversation %s lists the survivor %d times, want once", conversation, n)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM communications.conversation_customer_candidates WHERE customer_id = $1`, absorbed); n != 0 {
		t.Errorf("%d candidate rows still name the absorbed customer", n)
	}
}

// Rolled back, nothing moved: every one of the three statements ran in the
// caller's transaction.
func TestCustomerReferences_WritesOnlyInsideTheCallersTransaction(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	channel := setupChannel(t, h)
	absorbed := int32(1001)
	conversation := insertConversationFor(t, h, channel, &absorbed, &absorbed)
	insertCandidate(t, h, conversation.String(), absorbed)

	moved := repointCommunicationsCustomer(t, h, absorbed, 1002, false)
	for _, m := range moved {
		if m.Count != 1 {
			t.Errorf("%s = %d, want 1", m.Kind, m.Count)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM communications.conversations WHERE id = $1 AND customer_id = $2 AND suggested_customer_id = $2`, conversation, absorbed); n != 1 {
		t.Error("a rolled-back re-point moved the conversation")
	}
	if n := h.Count(t, `SELECT count(*) FROM communications.conversation_customer_candidates WHERE customer_id = $1`, absorbed); n != 1 {
		t.Error("a rolled-back re-point moved the candidate")
	}
}
```

- [ ] **Step 2: Run them and watch them fail**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'
mise exec -- go test -count=1 -run 'CustomerReference' ./internal/projects/ ./internal/energy/ ./internal/communications/
```
Expected: each package FAILS — `the module declares no customer reference holder` (the slot is nil), and the capture test's `want projects' one holder`.

- [ ] **Step 3: The three queries**

Create `apps/server/internal/projects/queries/customer_references.sql`:

```sql
-- name: RepointProjectsCustomer :execrows
-- RepointProjectsCustomer is this module's half of a customer merge (customers
-- merge design D1, contracts.CustomerReferenceHolder): every project billed to
-- the absorbed customer bills to the survivor, inside the customers module's
-- own transaction. There is no per-customer uniqueness to collide with —
-- ux_projects_code is global — and ix_projects_customer_id finds the rows. The
-- revision advances as it does for any change to the row, so an edit form
-- opened before the merge answers the stale-revision 409 rather than writing
-- the absorbed customer back.
UPDATE projects.projects
SET customer_id = @into_customer_id::integer,
    revision = revision + 1,
    updated_at = @now::timestamptz
WHERE customer_id = @from_customer_id::integer;
```

Create `apps/server/internal/energy/queries/customer_references.sql`:

```sql
-- name: RepointSupplyPeriodsCustomer :execrows
-- RepointSupplyPeriodsCustomer is energy's half of a customer merge (customers
-- merge design D1, contracts.CustomerReferenceHolder): every supply period of
-- the absorbed customer supplies the survivor, inside the customers module's
-- own transaction. supply_periods_no_overlap is scoped to metering_point_id,
-- so re-pointing the customer cannot violate it. No index is keyed by
-- customer_id, so this reads the table: a merge is a rare, deliberate act, and
-- an index every supply-period write pays for would be for this one statement.
UPDATE energy.supply_periods
SET customer_id = @into_customer_id::integer
WHERE customer_id = @from_customer_id::integer;
```

Create `apps/server/internal/communications/queries/customer_references.sql`:

```sql
-- name: RepointConversationsCustomer :execrows
-- RepointConversationsCustomer, RepointConversationsSuggestedCustomer and
-- RepointConversationCustomerCandidates are communications' half of a
-- customer merge (customers merge design D1, contracts.CustomerReferenceHolder),
-- all three inside the customers module's own transaction. A conversation
-- about the absorbed customer is about the survivor; how it came to be
-- associated (customer_association_source) is unchanged.
UPDATE communications.conversations
SET customer_id = @into_customer_id::integer
WHERE customer_id = @from_customer_id::integer;

-- name: RepointConversationsSuggestedCustomer :execrows
-- A suggestion naming the absorbed customer names the survivor: it was the
-- same entity all along.
UPDATE communications.conversations
SET suggested_customer_id = @into_customer_id::integer
WHERE suggested_customer_id = @from_customer_id::integer;

-- name: RepointConversationCustomerCandidates :one
-- The candidate list is the one place a re-point can collide: a conversation
-- may already list the survivor beside the absorbed customer, and
-- (conversation_id, customer_id) is the primary key. So the absorbed rows are
-- deleted and re-inserted for the survivor with ON CONFLICT DO NOTHING — one
-- statement, a conversation that listed both keeps the survivor once, and
-- each candidate keeps its created_at. moved is how many the absorbed customer
-- had; added how many of those were new to the survivor.
WITH gone AS (
    DELETE FROM communications.conversation_customer_candidates
    WHERE customer_id = @from_customer_id::integer
    RETURNING conversation_id, created_at
), kept AS (
    INSERT INTO communications.conversation_customer_candidates (conversation_id, customer_id, created_at)
    SELECT conversation_id, @into_customer_id::integer, created_at FROM gone
    ON CONFLICT (conversation_id, customer_id) DO NOTHING
    RETURNING conversation_id
)
SELECT (SELECT count(*) FROM gone)::bigint AS moved, (SELECT count(*) FROM kept)::bigint AS added;
```

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server && mise exec -- go generate ./... && git status --short
```
Expected: three new `store/customer_references.sql.go` files and nothing else. If sqlc names a parameter struct or a row field differently from the Interfaces block (it orders fields by first appearance), use its names below and say so in the report.

- [ ] **Step 4: The three holders**

Create `apps/server/internal/projects/customer_references.go`:

```go
package projects

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
	"github.com/vantigo-io/vantigo/server/internal/projects/store"
)

// customerReferenceKindProjects is the one kind of customer reference this
// module holds: projects.projects.customer_id, the customer a project bills to.
const customerReferenceKindProjects = "projects.projects"

// customerReferenceHolder is this module's contracts.CustomerReferenceHolder
// (customers merge design D1): when two customers are merged, every project of
// the absorbed one bills to the survivor from then on. Time and expenses reach
// a customer only through a project, so this one statement keeps them right
// too — neither holds a customer id of its own.
type customerReferenceHolder struct {
	clock func() time.Time
}

var _ contracts.CustomerReferenceHolder = (*customerReferenceHolder)(nil)

// newCustomerReferenceHolder is Module's CustomerReferences.
func newCustomerReferenceHolder(d module.Deps) contracts.CustomerReferenceHolder {
	return &customerReferenceHolder{clock: d.Clock}
}

// RepointCustomer moves every project of from to into, inside the caller's
// transaction (RepointProjectsCustomer). No project timeline entry is
// written: the merge is recorded on the survivor's customer timeline, and a
// project's customerName reads the survivor's the moment the merge commits.
func (h *customerReferenceHolder) RepointCustomer(ctx context.Context, tx pgx.Tx, from, into int32) ([]contracts.RepointedReferences, error) {
	n, err := store.New(tx).RepointProjectsCustomer(ctx, store.RepointProjectsCustomerParams{
		FromCustomerID: from, IntoCustomerID: into, Now: h.clock(),
	})
	if err != nil {
		return nil, fmt.Errorf("projects: re-point customer %d's projects to %d: %w", from, into, err)
	}
	return []contracts.RepointedReferences{{Kind: customerReferenceKindProjects, Count: n}}, nil
}
```

Create `apps/server/internal/energy/customer_references.go`:

```go
package energy

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/energy/store"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// customerReferenceKindSupplyPeriods is the one kind of customer reference
// this module holds: energy.supply_periods.customer_id.
const customerReferenceKindSupplyPeriods = "energy.supplyPeriods"

// customerReferenceHolder is this module's contracts.CustomerReferenceHolder
// (customers merge design D1): when two customers are merged, every metering
// point the absorbed one was supplied at is supplied to the survivor, over the
// same periods.
type customerReferenceHolder struct{}

var _ contracts.CustomerReferenceHolder = customerReferenceHolder{}

// newCustomerReferenceHolder is Module's CustomerReferences. It needs nothing
// from d: a supply period carries no timestamp or revision a move would touch.
func newCustomerReferenceHolder(module.Deps) contracts.CustomerReferenceHolder {
	return customerReferenceHolder{}
}

// RepointCustomer moves every supply period of from to into, inside the
// caller's transaction (RepointSupplyPeriodsCustomer).
func (customerReferenceHolder) RepointCustomer(ctx context.Context, tx pgx.Tx, from, into int32) ([]contracts.RepointedReferences, error) {
	n, err := store.New(tx).RepointSupplyPeriodsCustomer(ctx, store.RepointSupplyPeriodsCustomerParams{
		FromCustomerID: from, IntoCustomerID: into,
	})
	if err != nil {
		return nil, fmt.Errorf("energy: re-point customer %d's supply periods to %d: %w", from, into, err)
	}
	return []contracts.RepointedReferences{{Kind: customerReferenceKindSupplyPeriods, Count: n}}, nil
}
```

Create `apps/server/internal/communications/customer_references.go`:

```go
package communications

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/communications/store"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/module"
)

// The three kinds of customer reference this module holds: a conversation's
// customer, the customer suggested for it, and its candidate list.
const (
	customerReferenceKindConversations = "communications.conversations"
	customerReferenceKindSuggestions   = "communications.conversationSuggestions"
	customerReferenceKindCandidates    = "communications.conversationCandidates"
)

// customerReferenceHolder is this module's contracts.CustomerReferenceHolder
// (customers merge design D1): when two customers are merged, every
// conversation about, suggested for, or listing the absorbed one is about,
// suggested for, or lists the survivor.
type customerReferenceHolder struct{}

var _ contracts.CustomerReferenceHolder = customerReferenceHolder{}

// newCustomerReferenceHolder is Module's CustomerReferences.
func newCustomerReferenceHolder(module.Deps) contracts.CustomerReferenceHolder {
	return customerReferenceHolder{}
}

// RepointCustomer runs the three re-points inside the caller's transaction, in
// the order the kinds are reported.
func (customerReferenceHolder) RepointCustomer(ctx context.Context, tx pgx.Tx, from, into int32) ([]contracts.RepointedReferences, error) {
	q := store.New(tx)
	conversations, err := q.RepointConversationsCustomer(ctx, store.RepointConversationsCustomerParams{FromCustomerID: from, IntoCustomerID: into})
	if err != nil {
		return nil, fmt.Errorf("communications: re-point customer %d's conversations to %d: %w", from, into, err)
	}
	suggestions, err := q.RepointConversationsSuggestedCustomer(ctx, store.RepointConversationsSuggestedCustomerParams{FromCustomerID: from, IntoCustomerID: into})
	if err != nil {
		return nil, fmt.Errorf("communications: re-point customer %d's suggestions to %d: %w", from, into, err)
	}
	candidates, err := q.RepointConversationCustomerCandidates(ctx, store.RepointConversationCustomerCandidatesParams{FromCustomerID: from, IntoCustomerID: into})
	if err != nil {
		return nil, fmt.Errorf("communications: re-point customer %d's candidates to %d: %w", from, into, err)
	}
	return []contracts.RepointedReferences{
		{Kind: customerReferenceKindConversations, Count: conversations},
		{Kind: customerReferenceKindSuggestions, Count: suggestions},
		{Kind: customerReferenceKindCandidates, Count: candidates.Moved},
	}, nil
}
```

In each module's `Module()` literal add the slot, after the fields it already sets:

- `apps/server/internal/projects/module.go`: after `Projects:    newDirectory,` add `CustomerReferences: newCustomerReferenceHolder,` (gofmt aligns the block), and in `Module`'s doc comment change `— Time tracking first.` to `— Time tracking first — and the contracts.CustomerReferenceHolder a customer merge re-points its projects through.`
- `apps/server/internal/energy/module.go`: after `Mount:       mount,` add `CustomerReferences: newCustomerReferenceHolder,`, and at the end of `Module`'s doc comment (after `…so Directory is left nil, the same as products.`, which wraps) add: `The one thing it does provide is the contracts.CustomerReferenceHolder a customer merge re-points its supply periods through (customers merge design D1) — a write the customers module drives, not a read anyone makes.`
- `apps/server/internal/communications/module.go`: after `Workers:     workers,` add `CustomerReferences: newCustomerReferenceHolder,`, and at the end of `Module`'s doc comment (after `…so Directory is left nil.`) add: `What it does provide is the contracts.CustomerReferenceHolder a customer merge re-points its conversations, suggestions and candidates through (customers merge design D1).`

- [ ] **Step 5: Run them, show they can fail, commit**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'
mise exec -- gofmt -w internal/projects internal/energy internal/communications && mise exec -- gofmt -l internal
mise exec -- go vet ./internal/projects/... ./internal/energy/... ./internal/communications/...
mise exec -- go test -count=1 ./internal/projects/ ./internal/energy/ ./internal/communications/ ./internal/db/
```
Expected: PASS, every existing test of the three packages included; `TestNoModuleReferencesAnotherModulesSchema` still passes (each file names only its own schema — comments are stripped before the scan). `golangci-lint`'s `unused` is satisfied: each holder is reached through `Module()`.

Prove each new test can fail, restoring after each: in the projects query drop `revision = revision + 1,` (and regenerate) — the revision assertion goes red; in the communications query change `ON CONFLICT (conversation_id, customer_id) DO NOTHING` to nothing (and regenerate) — the candidates test goes red with a primary-key violation; give energy's holder the pool (`pool *pgxpool.Pool` on the struct, set from `d.Pool`) and build its store with `store.New(h.pool)` instead of `store.New(tx)` — the rollback test goes red (the period moved although the caller rolled back); remove `CustomerReferences: newCustomerReferenceHolder,` from projects' `Module()` — all three projects tests go red. Say what each printed.

```bash
cd /home/anders/projects/vantigo/vantigo
cat > /tmp/claude-1000/msg-merge-2.txt <<'EOF'
feat(contracts): projects, energy and communications re-point a merged customer's references

Each of the three modules that stores customer ids now declares a
contracts.CustomerReferenceHolder over one query file of its own schema:
projects moves the absorbed customer's projects to the survivor and
advances each moved project's revision; energy moves its supply periods;
communications moves a conversation's customer, the customer suggested
for it and its candidate list, where a conversation that already lists
the survivor keeps it once. Every holder writes only through the
transaction it is handed and reports each kind it handles, a zero
included. Nothing calls them yet: the customers merge is their caller.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
PATHS="apps/server/internal/projects/customer_references.go apps/server/internal/projects/customer_references_test.go \
 apps/server/internal/projects/queries/customer_references.sql apps/server/internal/projects/store/customer_references.sql.go \
 apps/server/internal/projects/module.go \
 apps/server/internal/energy/customer_references.go apps/server/internal/energy/customer_references_test.go \
 apps/server/internal/energy/queries/customer_references.sql apps/server/internal/energy/store/customer_references.sql.go \
 apps/server/internal/energy/module.go \
 apps/server/internal/communications/customer_references.go apps/server/internal/communications/customer_references_test.go \
 apps/server/internal/communications/queries/customer_references.sql apps/server/internal/communications/store/customer_references.sql.go \
 apps/server/internal/communications/module.go"
git add $PATHS && git commit -F /tmp/claude-1000/msg-merge-2.txt -- $PATHS
git show --stat HEAD && git status --short
```

---

### Task 3: The merge (D2, D3)

The operation itself: the marker column, the set-based moves, the contract, the permission, `mergedInto` on the response and the directory, the two events, and `merge.go` driving it all — this module's tables and every holder in one transaction.

**Files:**
- Create: `apps/server/internal/db/migrations/00029_customers_merge.sql`, `apps/server/internal/customers/queries/merge.sql`, `apps/server/internal/customers/merge.go`, `merge_test.go`, `merge_concurrency_test.go`, `merge_internal_test.go`
- Modify: `apps/server/internal/customers/sqlc.yaml`, `apps/server/internal/db/schema_test.go`, `apps/server/internal/customers/queries/customers.sql`, `openapi/customers.yaml`, `apps/server/internal/contracts/directory.go`, `apps/server/internal/customers/directory.go`, `owner.go`, `customers.go`, `timeline_events.go`, `module.go`, `module_test.go`, `customers_test.go`
- Generated: `apps/server/internal/customers/store/merge.sql.go`, `store/customers.sql.go`, `store/models.go`, `apps/server/internal/openapi/specs/customers.yaml`, `apps/server/internal/customers/gen/api.gen.go`, `apps/customers/frontend/src/api-schema.d.ts`, `openapi/COVERAGE.md`
- Read first (do not change): `contacts.go:343-455`, `:573-705` (the delete's locks, the attach, `contactRoleWriteAttempts`); `duplicates.go` (the refusal body beside the sentinel); `owner.go:80-230` (`decorateKnowing`); `timeline_events.go:67-111` (`recordGeneratedEvent`); `queries/contact_roles.sql`, `queries/tags.sql:60-92`, `queries/registry.sql:118-125`, `queries/peppol_recheck.sql:23-33`; `contacts_concurrency_test.go:45-83` (`race`, `awaitLockWaiters`), `addresses_concurrency_test.go:32-48` (`gateCustomerLock`)

**Interfaces:**
- Produces Go: `(*server).PostCustomersByIdMerge`; `mergeKindContacts`/`mergeKindAddresses`/`mergeKindTimelineEntries`/`mergeKindTags`; `mergeWriteAttempts = 3`; `mergeLockOrder(a, b int32) []int32`; `mergeSummary(mergedCustomerSnapshot, []contracts.RepointedReferences) string`; `recordCustomerMerged`, `recordCustomerMergedAway`; `customerDecoration.merged(int32) *gen.CustomerReference`; `contracts.CustomerEntry.MergedInto *int32`; store: `CustomerForMerge`, `InsertMergedAssociations`, `InsertMergedContactRoles`, `DeleteCustomerAssociations`, `MoveCustomerAddresses`, `MoveTimelineEntries`, `MoveTimelineRevisions`, `MergeCustomerTags`, `BumpCustomerRevision`, `MarkCustomerMerged`, `MergedIntoForCustomers`.
- Wire: `POST /api/v1/customers/{id}/merge` (`postCustomersByIdMerge`, body `CustomerMergeRequest {sourceId, revision?}`, `200 CustomerMergeResult {customer, moved: [CustomerMergeMove {kind, count}]}`, `400`, `404`, `409 CustomerConflictProblem` with `code` `merge_self` | `merge_type_mismatch` | `merge_into_archived` | `merge_already_merged` or none for the revision conflict, `permission:customers:merge+customers:view`); `SafeCustomerResponse.mergedInto?: CustomerReference`; permission `customers:merge`; events `customer.merged`, `customer.merged_away`.
- Consumes: Task 1's `Deps.CustomerReferenceHolders` and `modtest.WithCustomerReferenceHolders`; `LockCustomer`, `GetCustomer`, `CustomerTimelineSummary`, `DeleteCustomerRegistryRecord`, `DeleteCustomerPeppolLookup`, `decorate`, `safeCustomerResponse`, `customerRevisionConflict`, `recordGeneratedEvent`, `identitySnapshot`, `identityFromRow`, `contactInfoFromRow`, `billingProfileFromRow`, `floatPtrFromNumeric`, `truncateUTF16`.

- [ ] **Step 1: Write the failing tests**

In `apps/server/internal/customers/customers_test.go`, add to `customerJSON` after `Tags           []tagJSON         `json:"tags"``:

```go
	// MergedInto decodes SafeCustomerResponse.mergedInto (customers merge
	// design D3): a pointer, absent unless the customer was merged away.
	MergedInto *contactCustomerReferenceJSON `json:"mergedInto"`
```

(gofmt realigns the struct.) In `apps/server/internal/customers/module_test.go`, change the doc comment's `fourteen permissions` to `fifteen permissions` and `plus customers:billing-manage (invoice-ready customer design D1, D4, no .NET
// ancestor)` to `plus customers:billing-manage (invoice-ready customer design D1, D4) and
// customers:merge (customers merge design D2), neither with a .NET ancestor`, and append to `want`, after the `customers:billing-manage` entry:

```go
		{Key: "customers:merge", Display: "Merge customers", Description: "Merge a duplicate customer into another, moving its contacts, addresses, timeline, tags and other modules' references, and archiving it.", Category: "Customers", Sensitive: true, Delegable: true},
```

In `apps/server/internal/db/schema_test.go`, `TestCustomersBaseline_AppliesAndIsIdempotent`, insert directly before the `var tenantIDColumns int` block:

```go
	// merged_into_customer_id (00029) is the merge's marker: an in-module
	// foreign key, RESTRICT because a survivor is never deleted out from under
	// the customers merged into it (customers are archived, never deleted, and
	// the marker is what their page links to), and a partial index — the
	// merged-away customers are a handful among many.
	var mergedIntoDeleteRule string
	if err := pool.QueryRow(ctx, `
		SELECT confdeltype FROM pg_constraint c
		JOIN pg_class t ON t.oid = c.conrelid
		JOIN pg_namespace n ON n.oid = t.relnamespace
		WHERE n.nspname = 'customers' AND t.relname = 'customers' AND c.contype = 'f'
		  AND c.conname = 'customers_merged_into_customer_id_fkey'`).Scan(&mergedIntoDeleteRule); err != nil {
		t.Fatalf("query the merge marker's foreign key: %v", err)
	}
	if mergedIntoDeleteRule != "r" {
		t.Errorf("customers.merged_into_customer_id delete rule = %q, want %q (ON DELETE RESTRICT)", mergedIntoDeleteRule, "r")
	}
	var mergedIntoIndexDef string
	if err := pool.QueryRow(ctx, `SELECT indexdef FROM pg_indexes
	                              WHERE schemaname = 'customers' AND indexname = 'ix_customers_merged_into'`).Scan(&mergedIntoIndexDef); err != nil {
		t.Fatalf("read ix_customers_merged_into definition: %v", err)
	}
	if strings.Contains(mergedIntoIndexDef, "UNIQUE") || !strings.Contains(mergedIntoIndexDef, "WHERE (merged_into_customer_id IS NOT NULL)") {
		t.Errorf("ix_customers_merged_into = %q, want a non-unique PARTIAL index on merged_into_customer_id IS NOT NULL", mergedIntoIndexDef)
	}
```

Create `apps/server/internal/customers/merge_internal_test.go`:

```go
package customers

import (
	"slices"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
)

// TestMergeSummary_NamesWhatMovedInWords pins customer.merged's summary
// (customers merge design D3): the absorbed customer by number and name, then
// every kind that moved anything, in the answer's order, singular where there
// is one — and a kind this module has no word for still reads, by its name.
func TestMergeSummary_NamesWhatMovedInWords(t *testing.T) {
	absorbed := mergedCustomerSnapshot{CustomerNumber: 1005, Name: "Acme Norge AS"}
	moved := []contracts.RepointedReferences{
		{Kind: mergeKindContacts, Count: 3}, {Kind: mergeKindAddresses, Count: 2},
		{Kind: mergeKindTimelineEntries, Count: 14}, {Kind: mergeKindTags, Count: 0},
		{Kind: "projects.projects", Count: 2}, {Kind: "energy.supplyPeriods", Count: 1},
		{Kind: "somewhere.else", Count: 4},
	}
	want := "Absorbed #1005 Acme Norge AS: 3 contacts, 2 addresses, 14 timeline entries, 2 projects, 1 supply period, 4 × somewhere.else"
	if got := mergeSummary(absorbed, moved); got != want {
		t.Errorf("mergeSummary =\n%q\nwant\n%q", got, want)
	}
	nothing := []contracts.RepointedReferences{{Kind: mergeKindContacts, Count: 0}}
	if got := mergeSummary(absorbed, nothing); got != "Absorbed #1005 Acme Norge AS" {
		t.Errorf("mergeSummary with nothing moved = %q, want the absorbed customer alone", got)
	}
}

// TestMergeLockOrder_IsAscendingAndLocksOneRowOnce: every writer that locks
// several customer rows takes them in ascending id order (the contact
// delete's rule), which is what keeps two merges of one pair from
// deadlocking; and a merge of a customer into itself locks its one row once,
// so the ladder can answer merge_self rather than wait on itself.
func TestMergeLockOrder_IsAscendingAndLocksOneRowOnce(t *testing.T) {
	for _, tc := range []struct {
		a, b int32
		want []int32
	}{
		{1009, 1002, []int32{1002, 1009}},
		{1002, 1009, []int32{1002, 1009}},
		{1004, 1004, []int32{1004}},
	} {
		if got := mergeLockOrder(tc.a, tc.b); !slices.Equal(got, tc.want) {
			t.Errorf("mergeLockOrder(%d, %d) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}
```

Create `apps/server/internal/customers/merge_test.go`:

```go
package customers_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is POST /customers/{id}/merge (customers merge design D2, D3): the
// refusal ladder, everything that moves and everything that stays, the two
// events, and the other modules' holders — faked here, because depguard keeps
// projects, energy and communications out of this package even in a test, and
// each of them proves its own SQL in its own package. merge_concurrency_test.go
// carries the lock-forced races.

// fakeReferenceHolder stands in for another module's
// contracts.CustomerReferenceHolder. It records every call, answers the kinds
// it was given or fails, and — through during — can look into the merge's own
// transaction while it is open.
type fakeReferenceHolder struct {
	answer []contracts.RepointedReferences
	err    error
	during func(ctx context.Context, tx pgx.Tx, from, into int32) error

	mu    sync.Mutex
	calls [][2]int32
}

func (f *fakeReferenceHolder) RepointCustomer(ctx context.Context, tx pgx.Tx, from, into int32) ([]contracts.RepointedReferences, error) {
	f.mu.Lock()
	f.calls = append(f.calls, [2]int32{from, into})
	f.mu.Unlock()
	if f.during != nil {
		if err := f.during(ctx, tx, from, into); err != nil {
			return nil, err
		}
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.answer, nil
}

func (f *fakeReferenceHolder) callsSoFar() [][2]int32 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

type mergeMoveJSON struct {
	Kind  string `json:"kind"`
	Count int64  `json:"count"`
}

type mergeResultJSON struct {
	Customer customerJSON    `json:"customer"`
	Moved    []mergeMoveJSON `json:"moved"`
}

type mergeConflictJSON struct {
	Title  string  `json:"title"`
	Code   *string `json:"code"`
	Detail string  `json:"detail"`
}

// mergedEventPayloadJSON decodes customer.merged's payload (design D3).
type mergedEventPayloadJSON struct {
	CustomerId int32 `json:"customerId"`
	Absorbed   struct {
		Id             int32  `json:"id"`
		CustomerNumber int64  `json:"customerNumber"`
		Name           string `json:"name"`
		Type           string `json:"type"`
		Status         string `json:"status"`
		Identity       *struct {
			Id   string `json:"id"`
			Name string `json:"name"`
		} `json:"identity"`
		ContactInfo    contactInfoJSON    `json:"contactInfo"`
		BillingProfile billingProfileJSON `json:"billingProfile"`
		OwnerUserId    *string            `json:"ownerUserId"`
		GroupId        *string            `json:"groupId"`
	} `json:"absorbed"`
	Moved []mergeMoveJSON `json:"moved"`
}

// mergedAwayPayloadJSON decodes customer.merged_away's payload.
type mergedAwayPayloadJSON struct {
	CustomerId int32                        `json:"customerId"`
	Into       contactCustomerReferenceJSON `json:"into"`
}

// mergeClient signs in a caller who may merge and do everything the fixtures
// need: every customer key but lookup, customers:merge included.
func mergeClient(t *testing.T, h *modtest.Harness) *modtest.Client {
	t.Helper()
	return h.SignIn(t, "customers:view", "customers:create", "customers:update", "customers:delete", "customers:merge",
		"customers:legal-identity-view", "customers:legal-identity-manage",
		"customers:contacts-view", "customers:contacts-manage",
		"customers:associations-view", "customers:associations-manage",
		"customers:timeline-view", "customers:timeline-manage", "customers:billing-manage")
}

func postMerge(t *testing.T, c *modtest.Client, into int32, body map[string]any, opts ...modtest.RequestOption) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodPost, fmt.Sprintf("/api/v1/customers/%d/merge", into), body, opts...)
}

// mergeOK merges source into into and fails the test on anything but 200.
func mergeOK(t *testing.T, c *modtest.Client, into, source int32) mergeResultJSON {
	t.Helper()
	r := postMerge(t, c, into, map[string]any{"sourceId": source})
	if r.Status != http.StatusOK {
		t.Fatalf("merge %d into %d: status %d body %s, want 200", source, into, r.Status, r.Body)
	}
	var result mergeResultJSON
	r.JSON(&result)
	return result
}

// refusedWith asserts a 409 carrying code and answers its body.
func refusedWith(t *testing.T, r *modtest.Response, code string) mergeConflictJSON {
	t.Helper()
	if r.Status != http.StatusConflict {
		t.Fatalf("status %d body %s, want 409 %s", r.Status, r.Body, code)
	}
	var problem mergeConflictJSON
	r.JSON(&problem)
	if problem.Code == nil || *problem.Code != code {
		t.Errorf("code = %v, want %q (body %s)", problem.Code, code, r.Body)
	}
	return problem
}

func movedCount(result mergeResultJSON, kind string) int64 {
	for _, m := range result.Moved {
		if m.Kind == kind {
			return m.Count
		}
	}
	return -1
}

// timelineOf reads a customer's first timeline page, failing on anything but 200.
func timelineOf(t *testing.T, c *modtest.Client, customerID int32) []timelineEntryJSON {
	t.Helper()
	r := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/timeline", customerID), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("timeline of %d: status %d body %s, want 200", customerID, r.Status, r.Body)
	}
	var list timelineListJSON
	r.JSON(&list)
	return list.Data
}

func entriesOfType(entries []timelineEntryJSON, eventType string) []timelineEntryJSON {
	var out []timelineEntryJSON
	for _, e := range entries {
		if e.EventType == eventType {
			out = append(out, e)
		}
	}
	return out
}

func tagNamesOf(tags []tagJSON) []string {
	names := make([]string, 0, len(tags))
	for _, tag := range tags {
		names = append(names, tag.Name)
	}
	return names
}

// insertRegistryAndPeppol gives a customer a registry record and a stored
// Peppol answer directly: the merge's subject is which of them survive, not
// how a refresh or a lookup writes them.
func insertRegistryAndPeppol(t *testing.T, h *modtest.Harness, customerID int32) {
	t.Helper()
	h.Exec(t, `INSERT INTO customers.customer_registry_records
	               (customer_id, organisation_number, name, vat_registered, bankrupt, under_liquidation, under_forced_liquidation, fetched_at)
	           VALUES ($1, '923609016', 'ACME', true, false, false, false, $2)`, customerID, h.Now())
	h.Exec(t, `INSERT INTO customers.customer_peppol_lookups
	               (customer_id, participant_id, status, can_receive_invoice, can_receive_credit_note, checked_at)
	           VALUES ($1, '0192:923609016', 'registered', true, true, $2)`, customerID, h.Now())
}

// TestPostCustomersByIdMerge_RefusesInTheDesignsOrder walks design D2's ladder
// on one installation: 404 for either customer, then merge_self,
// merge_type_mismatch, merge_into_archived, merge_already_merged, then the
// survivor's stale revision — and the order is pinned where two refusals
// apply at once. A refused merge calls no holder: the one holder call is the
// merge that went through.
func TestPostCustomersByIdMerge_RefusesInTheDesignsOrder(t *testing.T) {
	t.Parallel()
	holder := &fakeReferenceHolder{}
	h := newHarness(t, modtest.WithCustomerReferenceHolders(holder))
	c := mergeClient(t, h)
	acme := createCustomer(t, c, "Acme AS")
	duplicate := createCustomer(t, c, "Acme Norge AS")
	person := createCustomerOfType(t, c, "Kari Nordmann", "person")
	archived := createCustomer(t, c, "Gamle Acme AS")
	if r := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d", archived.Id), nil); r.Status != http.StatusNoContent {
		t.Fatalf("archive: status %d body %s", r.Status, r.Body)
	}
	elsewhere := createCustomer(t, c, "Acme Holding AS")

	if r := postMerge(t, c, 999999, map[string]any{"sourceId": duplicate.Id}); r.Status != http.StatusNotFound {
		t.Errorf("unknown survivor: status %d body %s, want 404", r.Status, r.Body)
	}
	if r := postMerge(t, c, acme.Id, map[string]any{"sourceId": 999999}); r.Status != http.StatusNotFound {
		t.Errorf("unknown source: status %d body %s, want 404", r.Status, r.Body)
	}
	r := postMerge(t, c, acme.Id, map[string]any{"sourceId": 0})
	var invalid validationProblemJSON
	if r.Status != http.StatusBadRequest {
		t.Fatalf("sourceId 0: status %d body %s, want 400", r.Status, r.Body)
	}
	if r.JSON(&invalid); len(invalid.Errors["sourceId"]) != 1 {
		t.Errorf("sourceId 0: errors = %v, want one for sourceId", invalid.Errors)
	}

	refusedWith(t, postMerge(t, c, acme.Id, map[string]any{"sourceId": acme.Id}), "merge_self")
	mismatch := refusedWith(t, postMerge(t, c, acme.Id, map[string]any{"sourceId": person.Id}), "merge_type_mismatch")
	if !strings.Contains(mismatch.Detail, "Kari Nordmann is a private person") {
		t.Errorf("type mismatch detail = %q, want it to say what the source is", mismatch.Detail)
	}
	refusedWith(t, postMerge(t, c, archived.Id, map[string]any{"sourceId": duplicate.Id}), "merge_into_archived")
	// Both apply: the type check comes first.
	refusedWith(t, postMerge(t, c, archived.Id, map[string]any{"sourceId": person.Id}), "merge_type_mismatch")

	stale := postMerge(t, c, acme.Id, map[string]any{"sourceId": duplicate.Id, "revision": 99})
	var conflict mergeConflictJSON
	if stale.Status != http.StatusConflict {
		t.Fatalf("stale revision: status %d body %s, want 409", stale.Status, stale.Body)
	}
	if stale.JSON(&conflict); conflict.Title != "Customer revision conflict" || conflict.Code != nil {
		t.Errorf("stale revision: body %s, want the revision conflict without a code", stale.Body)
	}
	if calls := holder.callsSoFar(); len(calls) != 0 {
		t.Errorf("refused merges called the holder %v", calls)
	}

	mergeOK(t, c, acme.Id, duplicate.Id)
	already := refusedWith(t, postMerge(t, c, elsewhere.Id, map[string]any{"sourceId": duplicate.Id}), "merge_already_merged")
	if want := fmt.Sprintf("#%d Acme AS", acme.CustomerNumber); !strings.Contains(already.Detail, want) {
		t.Errorf("already-merged detail = %q, want it to name %s", already.Detail, want)
	}
	if calls := holder.callsSoFar(); !slices.Equal(calls, [][2]int32{{duplicate.Id, acme.Id}}) {
		t.Errorf("holder calls = %v, want the one merge that went through", calls)
	}
}

// The operation wants customers:merge and customers:view together (design D2):
// delete and update, which archive and edit a customer, are not enough, and
// merge alone cannot read what it answers.
func TestPostCustomersByIdMerge_WantsMergeAndView(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin := mergeClient(t, h)
	survivor := createCustomer(t, admin, "Acme AS")
	absorbed := createCustomer(t, admin, "Acme Norge AS")

	for _, keys := range [][]string{{"customers:view", "customers:update", "customers:delete"}, {"customers:merge"}} {
		r := postMerge(t, h.SignIn(t, keys...), survivor.Id, map[string]any{"sourceId": absorbed.Id})
		if r.Status != http.StatusForbidden || r.Code() != "forbidden" {
			t.Errorf("%v: status %d code %q, want 403 forbidden", keys, r.Status, r.Code())
		}
	}
	if r := postMerge(t, h.SignIn(t, "customers:merge", "customers:view"), survivor.Id, map[string]any{"sourceId": absorbed.Id}); r.Status != http.StatusOK {
		t.Errorf("merge + view: status %d body %s, want 200", r.Status, r.Body)
	}
}

// TestPostCustomersByIdMerge_MovesContactsWithTheirRolesAndPrimaries is D3's
// contact rule: every association moves; a contact linked to both keeps the
// survivor's association and title; roles are unioned; the survivor's primary
// stays, the absorbed customer's primary becomes the survivor's for a role it
// had nobody in, and every other primary flag goes — so every role ends with
// exactly one primary.
func TestPostCustomersByIdMerge_MovesContactsWithTheirRolesAndPrimaries(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := mergeClient(t, h)
	survivor := createCustomer(t, c, "Acme AS").Id
	absorbed := createCustomer(t, c, "Acme Norge AS").Id
	billing := createContact(t, c, map[string]any{"firstName": "Bea", "lastName": "Billing"}).Id
	shared := createContact(t, c, map[string]any{"firstName": "Sam", "lastName": "Shared"}).Id
	deciding := createContact(t, c, map[string]any{"firstName": "Dag", "lastName": "Decider"}).Id
	second := createContact(t, c, map[string]any{"firstName": "Siri", "lastName": "Second"}).Id

	// The survivor: Bea its primary billing contact, Sam its project contact, titled CEO.
	attachWithRoles(t, c, survivor, map[string]any{"contactId": billing, "roles": []any{map[string]any{"role": "billing"}}})
	attachWithRoles(t, c, survivor, map[string]any{"contactId": shared, "title": "CEO", "roles": []any{map[string]any{"role": "project"}}})
	// The duplicate: Sam again, as ITS primary billing contact under another
	// title; Dag its only decision maker; Siri a second billing holder.
	attachWithRoles(t, c, absorbed, map[string]any{"contactId": shared, "title": "Daglig leder", "roles": []any{map[string]any{"role": "billing"}}})
	attachWithRoles(t, c, absorbed, map[string]any{"contactId": deciding, "roles": []any{map[string]any{"role": "decision_maker"}}})
	attachWithRoles(t, c, absorbed, map[string]any{"contactId": second, "roles": []any{map[string]any{"role": "billing"}}})

	result := mergeOK(t, c, survivor, absorbed)

	if got := movedCount(result, "customers.contacts"); got != 3 {
		t.Errorf("customers.contacts = %d, want the duplicate's 3 associations, Sam's included", got)
	}
	r := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/contacts", survivor), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("survivor's contacts: status %d body %s", r.Status, r.Body)
	}
	var list roledContactListJSON
	r.JSON(&list)
	got := map[int32]roledContactJSON{}
	for _, a := range list.Data {
		got[a.Contact.Id] = a
	}
	want := map[int32][]contactRoleJSON{
		billing:  {{Role: "billing", Primary: true}},
		shared:   {{Role: "billing", Primary: false}, {Role: "project", Primary: true}},
		deciding: {{Role: "decision_maker", Primary: true}},
		second:   {{Role: "billing", Primary: false}},
	}
	if len(got) != len(want) {
		t.Fatalf("survivor has %d contacts, want %d: %+v", len(got), len(want), list.Data)
	}
	for id, roles := range want {
		if !slices.Equal(got[id].Roles, roles) {
			t.Errorf("contact %d roles = %+v, want %+v", id, got[id].Roles, roles)
		}
	}
	if str(got[shared].Title) != "CEO" {
		t.Errorf("the shared contact's title = %q, want the survivor's CEO", str(got[shared].Title))
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers_contacts WHERE customer_id = $1`, absorbed); n != 0 {
		t.Errorf("the duplicate still has %d associations", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customer_contact_roles WHERE customer_id = $1`, absorbed); n != 0 {
		t.Errorf("the duplicate still has %d role rows", n)
	}
}

// TestPostCustomersByIdMerge_MovesTheRestAndKeepsTheSurvivorsOwnRow is the rest
// of D3's table in one merge: addresses (primary-per-type demoted, labels
// kept), the timeline (entries, revisions and follow-ups, payloads untouched),
// tags unioned, the duplicate's registry record and Peppol answer gone, the
// survivor's own row untouched, the duplicate archived with the marker and
// still readable, both revisions bumped, the directory's MergedInto, and the
// two events with no status change beside them.
func TestPostCustomersByIdMerge_MovesTheRestAndKeepsTheSurvivorsOwnRow(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := mergeClient(t, h)
	ctx := context.Background()
	survivor := createCustomer(t, c, "Acme AS")
	absorbed := createCustomer(t, c, "Acme Norge AS")
	setLegalIdentity(t, h, survivor.Id, "no", "923609016", "ACME AS")
	setLegalIdentity(t, h, absorbed.Id, "no", "923609016", "ACME NORGE AS")
	for id, body := range map[int32]map[string]any{
		survivor.Id: {"email": "post@acme.no"},
		absorbed.Id: {"email": "post@acmenorge.no", "phone": "+47 22 33 44 55"},
	} {
		if r := putContactInfo(t, c, id, body); r.Status != http.StatusOK {
			t.Fatalf("contact info of %d: status %d body %s", id, r.Status, r.Body)
		}
	}
	if r := putBillingProfile(t, c, absorbed.Id, map[string]any{"currency": "EUR", "paymentTermsDays": 30}); r.Status != http.StatusOK {
		t.Fatalf("billing profile: status %d body %s", r.Status, r.Body)
	}
	keptInvoice := createAddress(t, c, survivor.Id, fullAddressBody("invoice", nil))
	oldInvoiceBody := fullAddressBody("invoice", nil)
	oldInvoiceBody["label"] = "Gammelt hovedkontor"
	demotedInvoice := createAddress(t, c, absorbed.Id, oldInvoiceBody)
	movedPostal := createAddress(t, c, absorbed.Id, fullAddressBody("postal", nil))
	vip := createTag(t, c, map[string]any{"name": "VIP"})
	nordic := createTag(t, c, map[string]any{"name": "Nordic"})
	for id, tags := range map[int32][]string{survivor.Id: {vip.Id}, absorbed.Id: {vip.Id, nordic.Id}} {
		if r := putCustomerTags(t, c, id, tags); r.Status != http.StatusOK {
			t.Fatalf("tags of %d: status %d body %s", id, r.Status, r.Body)
		}
	}
	entry := createWithFollowUp(t, c, absorbed.Id, day(h, 0), "Ring dem", map[string]any{"dueOn": day(h, 7)})
	putEntryWithFollowUp(t, c, absorbed.Id, entry.Id, entry.CurrentRevision, map[string]any{"dueOn": day(h, 14)})
	insertRegistryAndPeppol(t, h, survivor.Id)
	insertRegistryAndPeppol(t, h, absorbed.Id)
	survivorBefore := fetchCustomerJSON(t, c, survivor.Id)
	absorbedBefore := fetchCustomerJSON(t, c, absorbed.Id)

	result := mergeOK(t, c, survivor.Id, absorbed.Id)

	// The survivor's own row: every field its own, one revision on.
	after := result.Customer
	if after.Name != "Acme AS" || str(after.ContactInfo.Email) != "post@acme.no" || after.ContactInfo.Phone != nil {
		t.Errorf("survivor name/contact info = %q/%+v, want its own, untouched", after.Name, after.ContactInfo)
	}
	if after.Identity == nil || after.Identity.Id != "923609016" {
		t.Errorf("survivor identity = %+v, want its own", after.Identity)
	}
	if after.Revision != survivorBefore.Revision+1 || after.MergedInto != nil {
		t.Errorf("survivor revision %d (was %d), mergedInto %+v; want one on and no marker", after.Revision, survivorBefore.Revision, after.MergedInto)
	}
	if names := tagNamesOf(after.Tags); !slices.Equal(names, []string{"Nordic", "VIP"}) {
		t.Errorf("survivor tags = %v, want the union [Nordic VIP]", names)
	}
	if profile := fetchBillingProfile(t, c, survivor.Id); profile.Currency != nil || profile.PaymentTermsDays != nil {
		t.Errorf("survivor billing profile = %+v, want its own (empty), nothing filled in", profile)
	}

	// Addresses: the survivor's invoice primary stays, the duplicate's is
	// demoted with its label, and the duplicate's postal primary is the
	// survivor's now — it had none.
	addresses := map[int32]addressJSON{}
	for _, a := range listAddresses(t, c, survivor.Id).Data {
		addresses[a.Id] = a
	}
	if len(addresses) != 3 || !addresses[keptInvoice.Id].IsPrimary || addresses[demotedInvoice.Id].IsPrimary ||
		str(addresses[demotedInvoice.Id].Label) != "Gammelt hovedkontor" || !addresses[movedPostal.Id].IsPrimary {
		t.Errorf("survivor addresses = %+v", addresses)
	}
	if n := len(listAddresses(t, c, absorbed.Id).Data); n != 0 {
		t.Errorf("the duplicate still has %d addresses", n)
	}

	// The timeline: the entry and both its revisions under the survivor, its
	// follow-up with it, and the payloads still naming who they happened to.
	r := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/timeline/%d/revisions", survivor.Id, entry.Id), nil)
	var revisions timelineRevisionListJSON
	if r.Status != http.StatusOK {
		t.Fatalf("revisions under the survivor: status %d body %s", r.Status, r.Body)
	}
	if r.JSON(&revisions); len(revisions.Data) != 2 {
		t.Errorf("revisions = %d, want 2", len(revisions.Data))
	}
	if r := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/timeline/%d", absorbed.Id, entry.Id), nil); r.Status != http.StatusNotFound {
		t.Errorf("the entry under the duplicate: status %d, want 404", r.Status)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers_timeline_entries_revisions r
	                     JOIN customers.customers_timeline_entries e ON e.id = r.customer_timeline_entry_id
	                     WHERE r.customer_id <> e.customer_id`); n != 0 {
		t.Errorf("%d revisions disagree with their entry's customer", n)
	}
	var followUp *followUpRowJSON
	// The follow-up has no assignee, and the list's own default is assignee=me:
	// ask for the unassigned ones, on the survivor.
	for _, row := range listFollowUps(t, c, url.Values{"assignee": {"none"}, "customerId": {strconv.Itoa(int(survivor.Id))}}).Data {
		if row.EntryId == entry.Id {
			followUp = &row
		}
	}
	if followUp == nil || followUp.CustomerId != survivor.Id || followUp.CustomerName != "Acme AS" {
		t.Errorf("the follow-up = %+v, want it on the survivor", followUp)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers_timeline_entries
	                     WHERE customer_id = $1 AND (payload_json->>'customerId')::int = $2`, survivor.Id, absorbed.Id); n == 0 {
		t.Error("no moved entry still names the duplicate in its payload: payloads were rewritten")
	}

	// The registry record and the Peppol answer: the survivor's kept, the duplicate's gone.
	for _, table := range []string{"customer_registry_records", "customer_peppol_lookups"} {
		query := fmt.Sprintf(`SELECT count(*) FROM customers.%s WHERE customer_id = $1`, table)
		if h.Count(t, query, survivor.Id) != 1 || h.Count(t, query, absorbed.Id) != 0 {
			t.Errorf("%s: want the survivor's row kept and the duplicate's deleted", table)
		}
	}

	// The duplicate: archived, marked, one revision on, its own details still there.
	gone := fetchCustomerJSON(t, c, absorbed.Id)
	if gone.Status != "archived" || gone.MergedInto == nil || gone.MergedInto.Id != survivor.Id ||
		gone.MergedInto.CustomerNumber != survivor.CustomerNumber || gone.MergedInto.Name != "Acme AS" {
		t.Errorf("the duplicate = status %q mergedInto %+v, want archived and pointing at the survivor", gone.Status, gone.MergedInto)
	}
	if gone.Revision != absorbedBefore.Revision+1 || gone.Name != "Acme Norge AS" || str(gone.ContactInfo.Email) != "post@acmenorge.no" {
		t.Errorf("the duplicate = revision %d (was %d) name %q email %q", gone.Revision, absorbedBefore.Revision, gone.Name, str(gone.ContactInfo.Email))
	}
	if e, err := newDirectory(t, h).Customer(ctx, absorbed.Id); err != nil || e == nil || !e.Archived || e.MergedInto == nil || *e.MergedInto != survivor.Id {
		t.Errorf("directory Customer(duplicate) = %+v, %v; want archived with MergedInto the survivor", e, err)
	}
	if e, err := newDirectory(t, h).Customer(ctx, survivor.Id); err != nil || e == nil || e.MergedInto != nil {
		t.Errorf("directory Customer(survivor) = %+v, %v; want no marker", e, err)
	}

	// The events: customer.merged on the survivor, merged_away on the
	// duplicate, and no status change beside them.
	merged := entriesOfType(timelineOf(t, c, survivor.Id), "customer.merged")
	if len(merged) != 1 {
		t.Fatalf("customer.merged entries = %d, want 1", len(merged))
	}
	if want := fmt.Sprintf("Absorbed #%d Acme Norge AS: ", absorbed.CustomerNumber); !strings.HasPrefix(str(merged[0].Summary), want) {
		t.Errorf("summary = %q, want it to start %q", str(merged[0].Summary), want)
	}
	var payload mergedEventPayloadJSON
	if err := json.Unmarshal(merged[0].Payload, &payload); err != nil {
		t.Fatalf("decode customer.merged payload %s: %v", merged[0].Payload, err)
	}
	a := payload.Absorbed
	if payload.CustomerId != survivor.Id || a.Id != absorbed.Id || a.CustomerNumber != absorbed.CustomerNumber ||
		a.Name != "Acme Norge AS" || a.Type != "business" || a.Status != "active" ||
		a.Identity == nil || a.Identity.Name != "ACME NORGE AS" || str(a.ContactInfo.Phone) != "+47 22 33 44 55" ||
		str(a.BillingProfile.Currency) != "EUR" || a.BillingProfile.PaymentTermsDays == nil || *a.BillingProfile.PaymentTermsDays != 30 ||
		a.OwnerUserId != nil || a.GroupId != nil {
		t.Errorf("customer.merged payload = %s", merged[0].Payload)
	}
	if !slices.Equal(payload.Moved, result.Moved) {
		t.Errorf("payload moved = %+v, want the answer's %+v", payload.Moved, result.Moved)
	}
	absorbedTimeline := timelineOf(t, c, absorbed.Id)
	away := entriesOfType(absorbedTimeline, "customer.merged_away")
	if len(away) != 1 || str(away[0].Summary) != fmt.Sprintf("Merged into #%d Acme AS", survivor.CustomerNumber) {
		t.Fatalf("customer.merged_away = %+v, want one naming the survivor", away)
	}
	var awayPayload mergedAwayPayloadJSON
	if err := json.Unmarshal(away[0].Payload, &awayPayload); err != nil || awayPayload.CustomerId != absorbed.Id || awayPayload.Into.Id != survivor.Id {
		t.Errorf("customer.merged_away payload = %s (%v)", away[0].Payload, err)
	}
	if n := len(entriesOfType(absorbedTimeline, "customer.status_changed")); n != 0 {
		t.Errorf("the duplicate has %d status_changed entries, want none beside merged_away", n)
	}
}

// TestPostCustomersByIdMerge_CallsEveryHolderInsideItsTransaction is D1: each
// holder is called once, in Compose order, with (absorbed, survivor) and the
// merge's own transaction — in which this module's moves are already visible,
// uncommitted — and what each reports is in the answer after this module's
// four kinds, and in the summary.
func TestPostCustomersByIdMerge_CallsEveryHolderInsideItsTransaction(t *testing.T) {
	t.Parallel()
	var survivorAddressesSeen, absorbedAddressesSeen int
	first := &fakeReferenceHolder{
		answer: []contracts.RepointedReferences{{Kind: "projects.projects", Count: 2}},
		during: func(ctx context.Context, tx pgx.Tx, from, into int32) error {
			const q = `SELECT count(*) FROM customers.customer_addresses WHERE customer_id = $1`
			if err := tx.QueryRow(ctx, q, into).Scan(&survivorAddressesSeen); err != nil {
				return err
			}
			return tx.QueryRow(ctx, q, from).Scan(&absorbedAddressesSeen)
		},
	}
	second := &fakeReferenceHolder{answer: []contracts.RepointedReferences{
		{Kind: "energy.supplyPeriods", Count: 0}, {Kind: "somewhere.else", Count: 1},
	}}
	h := newHarness(t, modtest.WithCustomerReferenceHolders(first, second))
	c := mergeClient(t, h)
	survivor := createCustomer(t, c, "Acme AS")
	absorbed := createCustomer(t, c, "Acme Norge AS")
	createAddress(t, c, absorbed.Id, fullAddressBody("postal", nil))

	result := mergeOK(t, c, survivor.Id, absorbed.Id)

	for name, holder := range map[string]*fakeReferenceHolder{"first": first, "second": second} {
		if calls := holder.callsSoFar(); !slices.Equal(calls, [][2]int32{{absorbed.Id, survivor.Id}}) {
			t.Errorf("%s holder calls = %v, want one (absorbed, survivor)", name, calls)
		}
	}
	if survivorAddressesSeen != 1 || absorbedAddressesSeen != 0 {
		t.Errorf("inside the transaction the holder saw %d/%d addresses on survivor/duplicate, want the move made: 1/0",
			survivorAddressesSeen, absorbedAddressesSeen)
	}
	want := []mergeMoveJSON{
		{"customers.contacts", 0}, {"customers.addresses", 1}, {"customers.timelineEntries", 2}, {"customers.tags", 0},
		{"projects.projects", 2}, {"energy.supplyPeriods", 0}, {"somewhere.else", 1},
	}
	if !slices.Equal(result.Moved, want) {
		t.Errorf("moved = %+v, want %+v", result.Moved, want)
	}
	merged := entriesOfType(timelineOf(t, c, survivor.Id), "customer.merged")
	wantSummary := fmt.Sprintf("Absorbed #%d Acme Norge AS: 1 address, 2 timeline entries, 2 projects, 1 × somewhere.else", absorbed.CustomerNumber)
	if len(merged) != 1 || str(merged[0].Summary) != wantSummary {
		t.Errorf("customer.merged = %+v, want summary %q", merged, wantSummary)
	}
}

// A holder's error rolls everything back (design D1): nothing moved, no
// marker, no revision bump, no event — the merge is one transaction, and the
// holder's half cannot commit without this module's or the reverse. The error
// is not a deadlock, so it is not retried either.
func TestPostCustomersByIdMerge_AHolderErrorRollsEverythingBack(t *testing.T) {
	t.Parallel()
	failing := &fakeReferenceHolder{err: errors.New("the other module is down")}
	h := newHarness(t, modtest.WithCustomerReferenceHolders(failing))
	c := mergeClient(t, h)
	survivor := createCustomer(t, c, "Acme AS").Id
	absorbed := createCustomer(t, c, "Acme Norge AS").Id
	contact := createContact(t, c, map[string]any{"firstName": "Bea", "lastName": "Billing"}).Id
	attachWithRoles(t, c, absorbed, map[string]any{"contactId": contact, "roles": []any{map[string]any{"role": "billing"}}})
	createAddress(t, c, absorbed, fullAddressBody("postal", nil))
	tag := createTag(t, c, map[string]any{"name": "VIP"})
	if r := putCustomerTags(t, c, absorbed, []string{tag.Id}); r.Status != http.StatusOK {
		t.Fatalf("tags: status %d body %s", r.Status, r.Body)
	}
	const revisions = `SELECT revision FROM customers.customers WHERE id = $1`
	survivorRevision, absorbedRevision := modtest.One[int32](t, h, revisions, survivor), modtest.One[int32](t, h, revisions, absorbed)
	entries := h.Count(t, `SELECT count(*) FROM customers.customers_timeline_entries WHERE customer_id = $1`, absorbed)

	r := postMerge(t, c, survivor, map[string]any{"sourceId": absorbed},
		modtest.SkipContract("a holder's failure is an infrastructure 500, deliberately off-contract"))

	if r.Status != http.StatusInternalServerError {
		t.Fatalf("status %d body %s, want 500", r.Status, r.Body)
	}
	if calls := failing.callsSoFar(); len(calls) != 1 {
		t.Errorf("holder calls = %v, want exactly one: a plain error is not retried", calls)
	}
	for table, want := range map[string]int{"customers_contacts": 1, "customer_contact_roles": 1, "customer_addresses": 1, "customer_tags": 1} {
		if got := h.Count(t, fmt.Sprintf(`SELECT count(*) FROM customers.%s WHERE customer_id = $1`, table), absorbed); got != want {
			t.Errorf("%s rows on the duplicate = %d, want %d: the move was not rolled back", table, got, want)
		}
	}
	if got := h.Count(t, `SELECT count(*) FROM customers.customers_timeline_entries WHERE customer_id = $1`, absorbed); got != entries {
		t.Errorf("the duplicate's timeline entries = %d, want %d", got, entries)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE id = $1 AND status = 'active' AND merged_into_customer_id IS NULL`, absorbed); n != 1 {
		t.Error("the duplicate was archived or marked although the merge failed")
	}
	if modtest.One[int32](t, h, revisions, survivor) != survivorRevision || modtest.One[int32](t, h, revisions, absorbed) != absorbedRevision {
		t.Error("a revision moved although the merge failed")
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers_timeline_entries WHERE event_type IN ('customer.merged', 'customer.merged_away')`); n != 0 {
		t.Errorf("%d merge events were written by a failed merge", n)
	}
}

// An archived customer may be absorbed (design D2) — the usual case, the
// duplicate was archived when noticed. It stays archived, gains the marker,
// and its timeline gains merged_away but no second status change.
func TestPostCustomersByIdMerge_AbsorbsAnArchivedDuplicate(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := mergeClient(t, h)
	survivor := createCustomer(t, c, "Acme AS").Id
	absorbed := createCustomer(t, c, "Acme Norge AS").Id
	if r := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d", absorbed), nil); r.Status != http.StatusNoContent {
		t.Fatalf("archive: status %d body %s", r.Status, r.Body)
	}

	mergeOK(t, c, survivor, absorbed)

	gone := fetchCustomerJSON(t, c, absorbed)
	if gone.Status != "archived" || gone.MergedInto == nil || gone.MergedInto.Id != survivor {
		t.Errorf("the duplicate = %q %+v, want archived and marked", gone.Status, gone.MergedInto)
	}
	timeline := timelineOf(t, c, absorbed)
	if n := len(entriesOfType(timeline, "customer.status_changed")); n != 0 {
		t.Errorf("the duplicate's own status_changed stayed behind %d times; it moves with the rest of its timeline", n)
	}
	if n := len(entriesOfType(timeline, "customer.merged_away")); n != 1 {
		t.Errorf("merged_away entries = %d, want 1", n)
	}
}
```

Create `apps/server/internal/customers/merge_concurrency_test.go`:

```go
package customers_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file forces a merge's two races with a lock gate, the way
// contacts_concurrency_test.go does (race, awaitLockWaiters and
// addresses_concurrency_test.go's gateCustomerLock are reused, not declared
// again): a transaction holds a customer row, both racing requests queue
// behind it — confirmed through pg_stat_activity, never a sleep — and only then
// is the gate released.

// TestPostCustomersByIdMerge_TwoMergesOfOnePairRace_OneWinsOneIsAlreadyMerged:
// both merges queue on the lower id's lock (ascending order, design D3); the
// winner absorbs, and the other — reading the rows only once it holds both
// locks — finds the duplicate merged away and answers merge_already_merged. One
// merge event, never two, and never a 500.
func TestPostCustomersByIdMerge_TwoMergesOfOnePairRace_OneWinsOneIsAlreadyMerged(t *testing.T) {
	h := newHarness(t)
	c := mergeClient(t, h)
	survivor := createCustomer(t, c, "Acme AS").Id
	absorbed := createCustomer(t, c, "Acme Norge AS").Id
	release := gateCustomerLock(t, h, min(survivor, absorbed))

	merge := func() *modtest.Response {
		return postMerge(t, c, survivor, map[string]any{"sourceId": absorbed})
	}
	done := make(chan []*modtest.Response, 1)
	finished := make(chan struct{})
	go func() {
		done <- race(merge, merge)
		close(finished)
	}()
	awaitLockWaiters(t, h, 2, finished)
	release()
	responses := <-done

	var won, refused int
	for _, r := range responses {
		switch r.Status {
		case http.StatusOK:
			won++
		case http.StatusConflict:
			refusedWith(t, r, "merge_already_merged")
			refused++
		default:
			t.Errorf("status %d body %s, want 200 or 409", r.Status, r.Body)
		}
	}
	if won != 1 || refused != 1 {
		t.Errorf("won %d, refused %d; want exactly one of each", won, refused)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers_timeline_entries WHERE event_type = 'customer.merged'`); n != 1 {
		t.Errorf("customer.merged entries = %d, want 1", n)
	}
}

// TestPostCustomersByIdMerge_RacesTheDeleteOfAContactItMoves is the cycle
// mergeWriteAttempts exists for: a merge holds both customers and, moving the
// duplicate's association, wants a key-share on the contact; DELETE
// /customers/contacts/{id} holds that contact and wants the duplicate's row.
// PostgreSQL kills one; the retry runs it again from a fresh snapshot, so both
// requests succeed whichever order the database picked, and the contact ends
// up nowhere — never a 500, never an association left pointing at the
// duplicate.
func TestPostCustomersByIdMerge_RacesTheDeleteOfAContactItMoves(t *testing.T) {
	h := newHarness(t)
	c := mergeClient(t, h)
	survivor := createCustomer(t, c, "Acme AS").Id
	absorbed := createCustomer(t, c, "Acme Norge AS").Id
	contact := createContact(t, c, map[string]any{"firstName": "Race", "lastName": "Merger"}).Id
	attachWithRoles(t, c, absorbed, map[string]any{"contactId": contact, "roles": []any{map[string]any{"role": "billing"}}})
	release := gateCustomerLock(t, h, absorbed)

	merge := func() *modtest.Response {
		return postMerge(t, c, survivor, map[string]any{"sourceId": absorbed})
	}
	del := func() *modtest.Response {
		return c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/contacts/%d", contact), nil)
	}
	done := make(chan []*modtest.Response, 1)
	finished := make(chan struct{})
	go func() {
		done <- race(merge, del)
		close(finished)
	}()
	awaitLockWaiters(t, h, 2, finished)
	release()
	responses := <-done

	if r := responses[0]; r.Status != http.StatusOK {
		t.Errorf("merge: status %d body %s, want 200", r.Status, r.Body)
	}
	if r := responses[1]; r.Status != http.StatusNoContent {
		t.Errorf("delete: status %d body %s, want 204", r.Status, r.Body)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.contacts WHERE id = $1`, contact); n != 0 {
		t.Error("the contact survived its delete")
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers_contacts WHERE contact_id = $1`, contact); n != 0 {
		t.Errorf("%d associations of the deleted contact remain", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE id = $1 AND merged_into_customer_id = $2`, absorbed, survivor); n != 1 {
		t.Error("the duplicate is not marked merged")
	}
}
```

- [ ] **Step 2: Run them and watch them fail**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'
mise exec -- go test -count=1 -run 'Merge|DeclaresItsPermissionCatalog' ./internal/customers/
mise exec -- go test -count=1 -run TestCustomersBaseline ./internal/db/
```
Expected: the customers package FAILS to compile (`undefined: mergedCustomerSnapshot`, `mergeSummary`, `mergeLockOrder`, `mergeKindContacts`, and `e.MergedInto undefined` on `contracts.CustomerEntry`); the db test FAILS on `query the merge marker's foreign key: no rows in result set`.

- [ ] **Step 3: The marker column**

Create `apps/server/internal/db/migrations/00029_customers_merge.sql`:

```sql
-- +goose Up
-- The merge marker (customers merge design D3): which customer this one was
-- merged into, NULL for every customer that was not. A merged-away customer is
-- archived, never deleted — history stays readable and its page links to the
-- survivor through this column — so it is a pointer inside the customers
-- table, not a tombstone table of its own.
--
-- The foreign key is this module's own table, the group_id precedent (00027):
-- no module boundary is crossed. ON DELETE RESTRICT because nothing may take
-- a survivor away from under the customers merged into it; customers are not
-- deleted today, and if that ever changes this is the line that says the
-- marker has to be dealt with first. Partial, like ix_customers_group: the
-- merged-away are a handful among many, and the one lookup keyed by this
-- column (who was merged into X — the RESTRICT check itself) wants only them.
ALTER TABLE customers.customers
    ADD COLUMN merged_into_customer_id integer REFERENCES customers.customers (id) ON DELETE RESTRICT;
CREATE INDEX ix_customers_merged_into ON customers.customers (merged_into_customer_id)
    WHERE merged_into_customer_id IS NOT NULL;

-- +goose Down
ALTER TABLE customers.customers DROP COLUMN merged_into_customer_id;
```

In `apps/server/internal/customers/sqlc.yaml`, append `      - ../db/migrations/00029_customers_merge.sql` after the `00028` line.

- [ ] **Step 4: The queries**

Create `apps/server/internal/customers/queries/merge.sql`:

```sql
-- name: CustomerForMerge :one
-- CustomerForMerge is a merge's read of each of its two customers (customers
-- merge design D2, D3), made after both LockCustomer calls so the refusal
-- ladder and the absorbed customer's snapshot see the rows exactly as they
-- will be written: the type, status, marker and revision the ladder checks,
-- and every column customer.merged's absorbed payload records — the identity,
-- the contact info, the eleven billing columns, the owner and the group.
SELECT id, customer_number, name, type, status, revision, merged_into_customer_id,
       legal_country, legal_id, legal_name, legal_source, legal_type,
       email, phone, website, owner_user_id, group_id,
       invoice_email, reminder_email, payment_terms_days, currency, language,
       invoice_delivery, reminder_delivery, peppol_id, gln, buyer_reference, default_bill_rate
FROM customers.customers
WHERE id = @id;

-- name: InsertMergedAssociations :exec
-- InsertMergedAssociations is the first of a merge's three contact statements
-- (design D3): every association of the absorbed customer is copied to the
-- survivor, and a contact the survivor already has keeps the survivor's own
-- row — its title, phone and email override — through ON CONFLICT on the
-- primary key. The absorbed rows are deleted afterwards
-- (DeleteCustomerAssociations), once their roles have been copied too:
-- customer_contact_roles' composite foreign key has no ON UPDATE, so an
-- association cannot simply have its customer_id rewritten under its roles.
INSERT INTO customers.customers_contacts (customer_id, contact_id, title, phone, email)
SELECT @into_customer_id::int, contact_id, title, phone, email
FROM customers.customers_contacts
WHERE customer_id = @from_customer_id::int
ON CONFLICT (customer_id, contact_id) DO NOTHING;

-- name: InsertMergedContactRoles :exec
-- InsertMergedContactRoles unions the roles (design D3) and decides every
-- primary flag in the same statement. A role the survivor already has a
-- primary for keeps it, and every absorbed role row for it arrives
-- non-primary; a role the survivor has nobody in takes the absorbed customer's
-- primary as its own — the absorbed customer has at most one per role
-- (ux_customer_contact_roles_primary), so this can never make two. The NOT
-- EXISTS reads the statement's snapshot, which is the survivor's rows before
-- this insert. A role the survivor's association already holds for a shared
-- contact is kept as the survivor has it (the conflict target is the primary
-- key, deliberately: a primary-index conflict would be a bug to hear about, not
-- one to swallow). created_at travels with the row, so a contact's seniority in
-- a role — what decides who is promoted when a primary later steps down — is
-- the seniority it earned.
INSERT INTO customers.customer_contact_roles (customer_id, contact_id, role, is_primary, created_at)
SELECT @into_customer_id::int, r.contact_id, r.role,
       r.is_primary AND NOT EXISTS (
           SELECT 1 FROM customers.customer_contact_roles p
           WHERE p.customer_id = @into_customer_id::int AND p.role = r.role AND p.is_primary
       ),
       r.created_at
FROM customers.customer_contact_roles r
WHERE r.customer_id = @from_customer_id::int
ON CONFLICT (customer_id, contact_id, role) DO NOTHING;

-- name: DeleteCustomerAssociations :execrows
-- DeleteCustomerAssociations removes every association of a customer, and
-- with them its role rows (their foreign key cascades) — the last of the
-- merge's contact statements, once both copies above have run. Its row count
-- is the merge's customers.contacts: how many associations the absorbed
-- customer had, a contact the survivor already had included.
DELETE FROM customers.customers_contacts WHERE customer_id = @customer_id;

-- name: MoveCustomerAddresses :execrows
-- MoveCustomerAddresses moves every address of the absorbed customer to the
-- survivor (design D3), label and all, demoting the absorbed primary of a type
-- the survivor already has a primary for — the NOT EXISTS reads the
-- statement's snapshot, the survivor's own addresses — and keeping it primary
-- for a type the survivor has none of, so every type still has exactly one
-- (ux_customer_addresses_primary). The 50-address cap guards a write, not a
-- merge (docs/customers.md, Merging duplicates).
UPDATE customers.customer_addresses a
SET customer_id = @into_customer_id::int,
    is_primary = a.is_primary AND NOT EXISTS (
        SELECT 1 FROM customers.customer_addresses p
        WHERE p.customer_id = @into_customer_id::int AND p.type = a.type AND p.is_primary
    ),
    updated_at = @now::timestamptz
WHERE a.customer_id = @from_customer_id::int;

-- name: MoveTimelineEntries :one
-- MoveTimelineEntries moves every timeline entry of the absorbed customer to
-- the survivor (design D3), deleted ones and follow-ups included, and answers
-- how many of them were active — the count a person reading the summary will
-- find on the page. Payloads are not rewritten: payload_json.customerId says
-- which customer an event happened to at the time. Neither updated_at nor
-- current_revision moves: this is not an edit, and the entry's revision
-- history must not claim one.
WITH moved AS (
    UPDATE customers.customers_timeline_entries
    SET customer_id = @into_customer_id::int
    WHERE customer_id = @from_customer_id::int
    RETURNING state
)
SELECT count(*) FILTER (WHERE state = 'active')::bigint AS active_count FROM moved;

-- name: MoveTimelineRevisions :exec
-- MoveTimelineRevisions rewrites the revisions' own customer_id to match their
-- entry's (00003 mirrors it column for column). The column is not indexed, so
-- this reads the revisions table; a merge is rare, and an index every timeline
-- write would pay for is not worth it.
UPDATE customers.customers_timeline_entries_revisions
SET customer_id = @into_customer_id::int
WHERE customer_id = @from_customer_id::int;

-- name: MergeCustomerTags :one
-- MergeCustomerTags unions the tags (design D3): the absorbed customer's links
-- are deleted and re-inserted for the survivor in one statement, a tag the
-- survivor already carries kept once by the primary key. moved_count is how
-- many tags the absorbed customer had; added_count how many were new to the
-- survivor.
WITH gone AS (
    DELETE FROM customers.customer_tags
    WHERE customer_id = @from_customer_id::int
    RETURNING tag_id
), kept AS (
    INSERT INTO customers.customer_tags (customer_id, tag_id)
    SELECT @into_customer_id::int, tag_id FROM gone
    ON CONFLICT (customer_id, tag_id) DO NOTHING
    RETURNING tag_id
)
SELECT (SELECT count(*) FROM gone)::bigint AS moved_count, (SELECT count(*) FROM kept)::bigint AS added_count;

-- name: BumpCustomerRevision :execrows
-- BumpCustomerRevision is the survivor's own write in a merge (design D3): its
-- fields do not change, but what hangs off it did, and every write to the row
-- bumps revision (customers foundation design D5). Guarded like every other
-- revision-bearing write: the handler compared expected_revision under the
-- lock already, and the WHERE repeats it.
UPDATE customers.customers
SET updated_at = @now::timestamptz, revision = revision + 1
WHERE id = @id
  AND (sqlc.narg(expected_revision)::int IS NULL OR revision = sqlc.narg(expected_revision)::int);

-- name: MarkCustomerMerged :exec
-- MarkCustomerMerged is the absorbed customer's end (design D3): archived —
-- already, or now — with the marker naming the survivor, and its revision
-- advanced. Its name, number, identity, contact info and billing profile stay
-- as they were, so its page still reads.
UPDATE customers.customers
SET status = 'archived', merged_into_customer_id = @into_customer_id::int,
    updated_at = @now::timestamptz, revision = revision + 1
WHERE id = @id;

-- name: MergedIntoForCustomers :many
-- MergedIntoForCustomers is the merge marker of a whole page of customers in
-- ONE query (design D3, SafeCustomerResponse.mergedInto), the decoration's
-- shape (owner.go): a batched read beside the row rather than a column on the
-- five customer row types. A customer that was not merged away has no row.
SELECT c.id AS customer_id, t.id, t.customer_number, t.name
FROM customers.customers c
JOIN customers.customers t ON t.id = c.merged_into_customer_id
WHERE c.id = ANY(@customer_ids::int[]);
```

In `apps/server/internal/customers/queries/customers.sql`, `DirectoryCustomer` and `DirectoryCustomers` each select the marker: change both select lists from `SELECT c.id, c.name, c.status = 'archived' AS archived, c.group_id, g.name AS group_name` to `SELECT c.id, c.name, c.status = 'archived' AS archived, c.group_id, g.name AS group_name, c.merged_into_customer_id`, and add to `DirectoryCustomer`'s comment, after its last sentence: `The merge marker comes along too (customers merge design D3, contracts.CustomerEntry.MergedInto): a consumer holding the id of a customer merged away learns where it went.`

- [ ] **Step 5: The contract**

In `openapi/customers.yaml`:

(a) Directly above `    /api/v1/customers/{id}/overview:`, insert:

```yaml
    /api/v1/customers/{id}/merge:
        post:
            description: "This customer absorbs another (customers merge design D2, D3). sourceId's contacts (roles unioned, a role's primary resolved), addresses (a primary of a type this customer already has demoted), timeline entries with their revisions and follow-ups, and tags move here; every other module's reference to it — projects, supply periods, conversations — is re-pointed here in the same transaction; its registry record and Peppol answer are deleted; and it is archived with mergedInto naming this customer. This customer keeps every field of its own row: nothing is filled in from the other, whose own values are recorded in the customer.merged timeline event. Both customers' revisions advance. revision is this customer's, optional; present and stale, a 409 without a code. 404 when either customer does not exist. 409 with code merge_self (the same customer twice), merge_type_mismatch (a person and a business: a merge never changes what a customer is), merge_into_archived (this customer is archived — restore it first) or merge_already_merged (sourceId was merged away before; the detail names where). An archived sourceId may be absorbed."
            operationId: postCustomersByIdMerge
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
                            $ref: '#/components/schemas/CustomerMergeRequest'
                required: true
            responses:
                "200":
                    content:
                        application/json:
                            schema:
                                $ref: '#/components/schemas/CustomerMergeResult'
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
            summary: Merge another customer into this one
            tags:
                - Customers
            x-vantigo-access: permission:customers:merge+customers:view
```

(b) Directly above `        CustomerOverviewAmount:`, insert:

```yaml
        CustomerMergeMove:
            description: "One kind of reference a merge moved to the surviving customer, and how many (customers merge design D3). This module's four kinds come first — customers.contacts (the absorbed customer's contact associations, a contact the survivor already had included), customers.addresses, customers.timelineEntries (its active entries) and customers.tags (its tags, one the survivor already carried included) — then each other module's, in the order the installation composes them: projects.projects, energy.supplyPeriods, communications.conversations, communications.conversationSuggestions and communications.conversationCandidates. A kind is listed with count 0 when there was nothing of it; a module that is not enabled lists nothing."
            properties:
                count:
                    format: int64
                    type: integer
                kind:
                    type: string
            required:
                - kind
                - count
            type: object
        CustomerMergeRequest:
            description: "POST /customers/{id}/merge's body (customers merge design D2): sourceId is the customer to absorb into the one in the path. revision is the path customer's, optional — omitted, the merge applies regardless; present and stale, a 409. The absorbed customer needs none: it is going away."
            properties:
                revision:
                    description: The revision the caller read the surviving customer at (customers foundation design D5).
                    format: int32
                    nullable: true
                    type: integer
                sourceId:
                    format: int32
                    type: integer
            required:
                - sourceId
            type: object
        CustomerMergeResult:
            description: "What a merge did (customers merge design D3): the surviving customer as GET /customers/{id} answers it, and every kind of reference that moved to it."
            properties:
                customer:
                    $ref: '#/components/schemas/SafeCustomerResponse'
                moved:
                    items:
                        $ref: '#/components/schemas/CustomerMergeMove'
                    type: array
            required:
                - customer
                - moved
            type: object
```

(c) In `SafeCustomerResponse.properties`, directly after the `identity:` property (its `allOf` / `$ref: '#/components/schemas/SafeCustomerIdentity'` / `nullable: true` lines) and before `name:`, insert:

```yaml
                mergedInto:
                    allOf:
                        - $ref: '#/components/schemas/CustomerReference'
                    description: "The customer this one was merged into (customers merge design D3). Absent unless it was merged away — omitted, never null, like owner. A merged-away customer is archived, and everything it had is on that customer now."
```

(d) In `CustomerConflictProblem`, replace the description with: `ProblemDetails plus the customers module's own conflict detail (customers foundation design D5, D6). duplicates is populated only by the duplicate-legal-identity conflict, which also sets code; code alone (without duplicates) is also populated by the registry refresh's no_registry_identity and registry_identity_changed conflicts, the group vocabulary's group_exists and group_in_use, the tag vocabulary's tag_exists, and the merge's merge_self, merge_type_mismatch, merge_into_archived and merge_already_merged (customers merge design D2). A revision conflict carries neither.` — one line, as the file's other descriptions are (the group and tag codes were missing from it; `groups.go` and `tags.go` answer them).

- [ ] **Step 6: Generate**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server && mise exec -- go generate ./... && mise exec -- go generate ./... && git status --short
```
Expected: `store/merge.sql.go` new; `store/customers.sql.go` and `store/models.go` changed (the directory rows and `CustomersCustomer` gain `MergedIntoCustomerID *int32`); `internal/openapi/specs/customers.yaml` and `internal/customers/gen/api.gen.go` changed; the second run adds nothing. The build now fails on `*server does not implement gen.StrictServerInterface (missing method PostCustomersByIdMerge)` — Step 10 writes it. The generated names this plan uses: `gen.CustomerMergeRequest{SourceId int32; Revision *int32}`, `gen.CustomerMergeResult{Customer gen.SafeCustomerResponse; Moved []gen.CustomerMergeMove}`, `gen.CustomerMergeMove{Count int64; Kind string}`, `gen.SafeCustomerResponse.MergedInto *gen.CustomerReference`, `gen.PostCustomersByIdMerge{200JSON,400ApplicationProblemPlusJSON,404,409ApplicationProblemPlusJSON}Response`; `store.CustomerForMergeRow` (with `MergedIntoCustomerID *int32`, `GroupID *uuid.UUID`, `DefaultBillRate pgtype.Numeric`), `store.MergeCustomerTagsRow{MovedCount, AddedCount int64}`, `store.MergedIntoForCustomersRow{CustomerID, ID int32; CustomerNumber int64; Name string}`, `store.BumpCustomerRevisionParams{Now time.Time; ID int32; ExpectedRevision *int32}`, `store.MarkCustomerMergedParams{IntoCustomerID int32; Now time.Time; ID int32}`, and `{IntoCustomerID, FromCustomerID int32}` (plus `Now` for addresses) for the move params. Where sqlc or oapi-codegen named something differently, use theirs and say so in the report.

- [ ] **Step 7: The directory's marker**

In `apps/server/internal/contracts/directory.go`, add to `CustomerEntry` after `Group *CustomerGroupEntry`:

```go
	// MergedInto is the customer this one was merged into (customers merge
	// design D3), nil unless it was merged away. A merged-away customer is
	// archived and still resolves — a consumer holding a stale id learns where
	// to look. Every CustomerReferenceHolder re-points the references that
	// existed when the merge ran; one written for this id afterwards (or
	// committed while the merge ran) is what this field is the backstop for.
	MergedInto *int32
```

In `apps/server/internal/customers/directory.go`, both entry literals gain the column: in `Customer`, `Group: groupEntry(row.GroupID, row.GroupName)}, nil` becomes `Group: groupEntry(row.GroupID, row.GroupName), MergedInto: row.MergedIntoCustomerID}, nil`; in `Customers`, `Group: groupEntry(row.GroupID, row.GroupName)})` becomes `Group: groupEntry(row.GroupID, row.GroupName), MergedInto: row.MergedIntoCustomerID})`.

- [ ] **Step 8: The permission**

In `apps/server/internal/customers/module.go`, append to `permissions`, after the `customers:billing-manage` entry:

```go
	{Key: "customers:merge", Display: "Merge customers", Description: "Merge a duplicate customer into another, moving its contacts, addresses, timeline, tags and other modules' references, and archiving it.", Category: "Customers", Sensitive: true, Delegable: true},
```

and extend the catalog's doc comment: after `…deliberately narrower than customers:update.` add ` customers:merge (customers merge design D2) is the second: a merge rewrites other modules' references and archives a customer, which is more than customers:delete does, so it is its own sensitive key, never implied by delete.` In `Module`'s doc comment, change `its fourteen permissions` to `its fifteen permissions`.

- [ ] **Step 9: `mergedInto` on the response, and the two events**

Every "change X to Y" in this step and in Steps 1 and 8 quotes comment text that wraps across lines in the source (`owner.go:75-76` and `:86-87`, `customers.go:169-171`, `module_test.go:32-35`, `module.go:24-28`): match it across the line breaks, then re-wrap the comment to the file's width.

In `apps/server/internal/customers/owner.go`: add `mergedInto map[int32]gen.CustomerReference` to `customerDecoration` (after `groups`; the field is not called `merged` because the method below is), and in its doc comment change `the customer's group, whose name lives in the group vocabulary (customer groups design D3).` to `the customer's group, whose name lives in the group vocabulary (customer groups design D3), and the customer it was merged into, if any (customers merge design D3).`. Change `decorate`'s doc comment `resolves the owners, tags and groups of rows in one directory call and two queries.` to `resolves the owners, tags, groups and merge markers of rows in one directory call and three queries.` (it wraps; re-wrap it). In `decorateKnowing`, add `mergedInto: map[int32]gen.CustomerReference{},` to the `dec` literal, and directly after the groups loop (`dec.groups[g.CustomerID] = …` and its closing brace) insert:

```go
	// The merge marker is one more batched query over the same ids (customers
	// merge design D3), for the group's reason: the survivor's number and name
	// live on another row, and a join on every customer row type would widen
	// five of them for a field almost every customer answers without.
	markers, err := q.MergedIntoForCustomers(ctx, customerIDs)
	if err != nil {
		return customerDecoration{}, fmt.Errorf("customers: load merge markers: %w", err)
	}
	for _, m := range markers {
		dec.mergedInto[m.CustomerID] = gen.CustomerReference{Id: m.ID, CustomerNumber: m.CustomerNumber, Name: m.Name}
	}
```

and after the `group` method add:

```go
// merged is the customer this one was merged into, or nil when it was not —
// absent on the wire, never null, the idiom group beside it follows.
func (d customerDecoration) merged(customerID int32) *gen.CustomerReference {
	if m, ok := d.mergedInto[customerID]; ok {
		return &m
	}
	return nil
}
```

In `apps/server/internal/customers/customers.go`, `safeCustomerResponse`: after `Group:          dec.group(row.ID),` add `MergedInto:     dec.merged(row.ID),`, and in its doc comment change `and the customer's group, which lives in the module's own vocabulary table (customer groups design D3). All three are` to `the customer's group, which lives in the module's own vocabulary table (customer groups design D3), and the customer it was merged into (customers merge design D3). All four are`.

In `apps/server/internal/customers/timeline_events.go`, add `"github.com/vantigo-io/vantigo/server/internal/contracts"` to the imports and append:

```go
// mergedCustomerSnapshot is customer.merged's absorbed shape (customers merge
// design D3): the absorbed customer's own row as it was when it was merged.
// The survivor keeps every field of its own, so this is where a person who
// wanted the other's billing profile or identity reads it — nothing was filled
// in from it by guesswork, and nothing of it is lost either.
type mergedCustomerSnapshot struct {
	ID             int32                  `json:"id"`
	CustomerNumber int64                  `json:"customerNumber"`
	Name           string                 `json:"name"`
	Type           string                 `json:"type"`
	Status         string                 `json:"status"`
	Identity       *legalIdentitySnapshot `json:"identity"`
	ContactInfo    contactInfo            `json:"contactInfo"`
	BillingProfile billingProfile         `json:"billingProfile"`
	OwnerUserID    *uuid.UUID             `json:"ownerUserId"`
	GroupID        *uuid.UUID             `json:"groupId"`
}

// customerRefSnapshot is a customer named by id, number and name, as the
// merge's two events and the merge refusals name one.
type customerRefSnapshot struct {
	ID             int32  `json:"id"`
	CustomerNumber int64  `json:"customerNumber"`
	Name           string `json:"name"`
}

// mergeMove is one entry of customer.merged's moved list.
type mergeMove struct {
	Kind  string `json:"kind"`
	Count int64  `json:"count"`
}

// customerLabel is how the merge names a customer to a person: "#1005 Acme
// Norge AS", the customer number the duplicate-identity list shows.
func customerLabel(number int64, name string) string {
	return fmt.Sprintf("#%d %s", number, name)
}

// mergeKindNouns are the words customer.merged's summary uses for each kind a
// merge can move, singular then plural. The other modules' kinds are here as
// words only — this module never reads their data — and a kind missing from
// the table still reads, as "4 × kind", so a holder added later cannot break
// the summary.
var mergeKindNouns = map[string][2]string{
	mergeKindContacts:                        {"contact", "contacts"},
	mergeKindAddresses:                       {"address", "addresses"},
	mergeKindTimelineEntries:                 {"timeline entry", "timeline entries"},
	mergeKindTags:                            {"tag", "tags"},
	"projects.projects":                      {"project", "projects"},
	"energy.supplyPeriods":                   {"supply period", "supply periods"},
	"communications.conversations":           {"conversation", "conversations"},
	"communications.conversationSuggestions": {"conversation suggestion", "conversation suggestions"},
	"communications.conversationCandidates":  {"conversation candidate", "conversation candidates"},
}

// mergeSummary is customer.merged's summary: "Absorbed #1005 Acme Norge AS: 3
// contacts, 2 addresses, 14 timeline entries, 2 projects" — every kind that
// moved anything, in the answer's order, and the absorbed customer alone when
// nothing did.
func mergeSummary(absorbed mergedCustomerSnapshot, moved []contracts.RepointedReferences) string {
	var parts []string
	for _, m := range moved {
		if m.Count == 0 {
			continue
		}
		nouns, known := mergeKindNouns[m.Kind]
		switch {
		case !known:
			parts = append(parts, fmt.Sprintf("%d × %s", m.Count, m.Kind))
		case m.Count == 1:
			parts = append(parts, "1 "+nouns[0])
		default:
			parts = append(parts, fmt.Sprintf("%d %s", m.Count, nouns[1]))
		}
	}
	summary := "Absorbed " + customerLabel(absorbed.CustomerNumber, absorbed.Name)
	if len(parts) > 0 {
		summary += ": " + strings.Join(parts, ", ")
	}
	return truncateUTF16(summary, 500)
}

// recordCustomerMerged is POST /customers/{id}/merge's event on the survivor
// (customers merge design D3). moved is the answer's own list, zeros included,
// so the event and the response can never disagree about what moved.
func recordCustomerMerged(ctx context.Context, q *store.Queries, now time.Time, customerID int32, absorbed mergedCustomerSnapshot, moved []contracts.RepointedReferences, actorKind, actorDisplay string, actorUserID *uuid.UUID) error {
	moves := make([]mergeMove, 0, len(moved))
	for _, m := range moved {
		moves = append(moves, mergeMove{Kind: m.Kind, Count: m.Count})
	}
	payload := map[string]any{
		"customerId": customerID,
		"absorbed":   absorbed,
		"moved":      moves,
	}
	return recordGeneratedEvent(ctx, q, customerID, now, "customer.merged", mergeSummary(absorbed, moved), payload, 1, actorKind, actorDisplay, actorUserID)
}

// recordCustomerMergedAway is the merge's event on the absorbed customer: the
// one entry its own timeline gains, recorded after the rest of its timeline has
// moved, so it is the one that stays. No customer.status_changed goes beside
// it — the merge is the reason the customer is archived.
func recordCustomerMergedAway(ctx context.Context, q *store.Queries, now time.Time, customerID int32, into customerRefSnapshot, actorKind, actorDisplay string, actorUserID *uuid.UUID) error {
	payload := map[string]any{
		"customerId": customerID,
		"into":       into,
	}
	summary := truncateUTF16("Merged into "+customerLabel(into.CustomerNumber, into.Name), 500)
	return recordGeneratedEvent(ctx, q, customerID, now, "customer.merged_away", summary, payload, 1, actorKind, actorDisplay, actorUserID)
}
```

- [ ] **Step 10: The merge**

Create `apps/server/internal/customers/merge.go`:

```go
package customers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
	"github.com/vantigo-io/vantigo/server/internal/customers/store"
	"github.com/vantigo-io/vantigo/server/internal/db"
)

// This file is POST /customers/{id}/merge (customers merge design D1-D3): the
// customer in the path survives and absorbs sourceId. Everything happens in one
// transaction, because every module shares one database: both customer rows
// are locked, this module's own tables move, and then every
// contracts.CustomerReferenceHolder Compose collected re-points its own schema
// through the same transaction (docs/module-boundaries.md rule 8). An error
// anywhere — a holder's included — rolls every module's part back together.

// The four kinds of reference this module's own tables hold, reported first in
// a merge's answer, before every holder's.
const (
	mergeKindContacts        = "customers.contacts"
	mergeKindAddresses       = "customers.addresses"
	mergeKindTimelineEntries = "customers.timelineEntries"
	mergeKindTags            = "customers.tags"
)

// mergeWriteAttempts is how often a merge's transaction runs before a deadlock
// it keeps losing escapes as a 500 — contactRoleWriteAttempts' three, and for
// that constant's reason. Two writers take their locks in the other order. A
// merge takes both customer rows first and then, copying the absorbed
// customer's associations, a key-share on each contact row, while DELETE
// /customers/contacts/{id} takes the contact row first and the customers
// after. And the merge's tag union locks the absorbed customer's customer_tags
// rows and then key-shares each tag, while DELETE /customers/tags/{tagId}
// locks the tag and then, through its cascade, those same rows — tags.go's
// tagWriteAttempts shape. Either pair can cycle; PostgreSQL kills one side
// (40P01), and the loser runs again from a fresh snapshot in which the other's
// write has simply happened. An attach cannot cycle with a merge: it takes the
// customer first too. The holders' statements run inside this retry as well.
const mergeWriteAttempts = 3

var (
	// errMergeCustomerNotFound is either customer missing under its lock: 404.
	errMergeCustomerNotFound = errors.New("customers: merge customer not found")
	// errMergeRefused aborts the transaction on a refusal; the body travels
	// beside it, as errDuplicateIdentity's does (duplicates.go).
	errMergeRefused = errors.New("customers: merge refused")
)

// mergeLockOrder is the rows a merge locks, in the order it locks them:
// ascending id, every multi-customer writer's order (the contact delete's),
// so two merges of one pair never deadlock with each other — and a customer
// named twice is locked once, so merge_self is answered rather than waited on.
func mergeLockOrder(a, b int32) []int32 {
	if a == b {
		return []int32{a}
	}
	return []int32{min(a, b), max(a, b)}
}

// mergeConflict is a merge refusal's 409 body: the module's
// CustomerConflictProblem with its code.
func mergeConflict(title, code, detail string) *gen.CustomerConflictProblem {
	status := int32(http.StatusConflict)
	return &gen.CustomerConflictProblem{Title: &title, Code: &code, Detail: &detail, Status: &status}
}

// customerTypePhrase is a customer type as a refusal says it.
func customerTypePhrase(customerType string) string {
	if customerType == "person" {
		return "a private person"
	}
	return "a business"
}

// mergeRefusal is design D2's ladder after the 404s, read from the two rows
// under their locks, in its order: the same customer twice, two types, an
// archived survivor, an absorbed customer merged away before, then the
// survivor's stale revision. nil, nil lets the merge go ahead.
func mergeRefusal(ctx context.Context, txq *store.Queries, survivor, absorbed store.CustomerForMergeRow, expected *int32) (*gen.CustomerConflictProblem, error) {
	switch {
	case survivor.ID == absorbed.ID:
		return mergeConflict("Cannot merge a customer into itself", "merge_self",
			"A customer cannot absorb itself. Choose the duplicate to merge into this one."), nil
	case survivor.Type != absorbed.Type:
		return mergeConflict("Customer types differ", "merge_type_mismatch", fmt.Sprintf(
			"%s is %s and %s is %s. A merge never changes what a customer is; change one of their types first.",
			customerLabel(absorbed.CustomerNumber, absorbed.Name), customerTypePhrase(absorbed.Type),
			customerLabel(survivor.CustomerNumber, survivor.Name), customerTypePhrase(survivor.Type))), nil
	case survivor.Status == "archived":
		return mergeConflict("Customer is archived", "merge_into_archived", fmt.Sprintf(
			"%s is archived. Restore it before merging another customer into it.",
			customerLabel(survivor.CustomerNumber, survivor.Name))), nil
	case absorbed.MergedIntoCustomerID != nil:
		// The marker's foreign key guarantees the row exists; it is read, not
		// locked — the refusal only names it.
		into, err := txq.GetCustomer(ctx, *absorbed.MergedIntoCustomerID)
		if err != nil {
			return nil, fmt.Errorf("read the customer %d was merged into: %w", absorbed.ID, err)
		}
		return mergeConflict("Customer already merged", "merge_already_merged", fmt.Sprintf(
			"%s was already merged into %s.",
			customerLabel(absorbed.CustomerNumber, absorbed.Name), customerLabel(into.CustomerNumber, into.Name))), nil
	case expected != nil && *expected != survivor.Revision:
		conflict := customerRevisionConflict(*expected, survivor.Revision)
		return &conflict, nil
	}
	return nil, nil
}

// absorbedSnapshot is customer.merged's absorbed payload, from the absorbed
// customer's row as it was read under its lock.
func absorbedSnapshot(c store.CustomerForMergeRow) (mergedCustomerSnapshot, error) {
	rate, err := floatPtrFromNumeric(c.DefaultBillRate)
	if err != nil {
		return mergedCustomerSnapshot{}, err
	}
	return mergedCustomerSnapshot{
		ID: c.ID, CustomerNumber: c.CustomerNumber, Name: c.Name, Type: c.Type, Status: c.Status,
		Identity:    identitySnapshot(identityFromRow(c.LegalCountry, c.LegalID, c.LegalName, c.LegalSource, c.LegalType)),
		ContactInfo: contactInfoFromRow(c.Email, c.Phone, c.Website),
		BillingProfile: billingProfileFromRow(c.InvoiceEmail, c.ReminderEmail, c.PaymentTermsDays, c.Currency, c.Language,
			c.InvoiceDelivery, c.ReminderDelivery, c.PeppolID, c.Gln, c.BuyerReference, rate),
		OwnerUserID: c.OwnerUserID,
		GroupID:     c.GroupID,
	}, nil
}

// moveOwnRecords moves this module's tables from the absorbed customer to the
// survivor (design D3), in the one order the constraints allow, and answers
// this module's four kinds. The caller holds both rows locked.
func moveOwnRecords(ctx context.Context, txq *store.Queries, from, into int32, now time.Time) ([]contracts.RepointedReferences, error) {
	// Contacts: the associations are copied (the survivor's own row winning for
	// a shared contact), then the roles (primaries resolved in the statement),
	// and only then are the absorbed rows deleted — the roles' composite foreign
	// key cascades them away with their associations.
	if err := txq.InsertMergedAssociations(ctx, store.InsertMergedAssociationsParams{IntoCustomerID: into, FromCustomerID: from}); err != nil {
		return nil, fmt.Errorf("copy the contact associations: %w", err)
	}
	if err := txq.InsertMergedContactRoles(ctx, store.InsertMergedContactRolesParams{IntoCustomerID: into, FromCustomerID: from}); err != nil {
		return nil, fmt.Errorf("union the contact roles: %w", err)
	}
	contacts, err := txq.DeleteCustomerAssociations(ctx, from)
	if err != nil {
		return nil, fmt.Errorf("remove the absorbed associations: %w", err)
	}
	addresses, err := txq.MoveCustomerAddresses(ctx, store.MoveCustomerAddressesParams{IntoCustomerID: into, FromCustomerID: from, Now: now})
	if err != nil {
		return nil, fmt.Errorf("move the addresses: %w", err)
	}
	entries, err := txq.MoveTimelineEntries(ctx, store.MoveTimelineEntriesParams{IntoCustomerID: into, FromCustomerID: from})
	if err != nil {
		return nil, fmt.Errorf("move the timeline: %w", err)
	}
	if err := txq.MoveTimelineRevisions(ctx, store.MoveTimelineRevisionsParams{IntoCustomerID: into, FromCustomerID: from}); err != nil {
		return nil, fmt.Errorf("move the timeline revisions: %w", err)
	}
	tags, err := txq.MergeCustomerTags(ctx, store.MergeCustomerTagsParams{IntoCustomerID: into, FromCustomerID: from})
	if err != nil {
		return nil, fmt.Errorf("union the tags: %w", err)
	}
	// The registry record and the Peppol answer describe an identity the
	// survivor either shares or does not have; the survivor keeps its own, and
	// a refresh or a re-check fetches either again.
	if err := txq.DeleteCustomerRegistryRecord(ctx, from); err != nil {
		return nil, fmt.Errorf("delete the absorbed registry record: %w", err)
	}
	if err := txq.DeleteCustomerPeppolLookup(ctx, from); err != nil {
		return nil, fmt.Errorf("delete the absorbed Peppol answer: %w", err)
	}
	return []contracts.RepointedReferences{
		{Kind: mergeKindContacts, Count: contacts},
		{Kind: mergeKindAddresses, Count: addresses},
		{Kind: mergeKindTimelineEntries, Count: entries},
		{Kind: mergeKindTags, Count: tags.MovedCount},
	}, nil
}

// mergeCustomers is one attempt at the whole merge, inside tx: the locks, the
// ladder, this module's moves, every holder, both rows' writes and the two
// events. A refusal returns its body with errMergeRefused, so db.WithTx rolls
// the attempt back and the handler still has something to answer with.
func (s *server) mergeCustomers(ctx context.Context, tx pgx.Tx, into, from int32, expected *int32, now time.Time, act actor) ([]contracts.RepointedReferences, *gen.CustomerConflictProblem, error) {
	txq := store.New(tx)
	for _, id := range mergeLockOrder(into, from) {
		if _, err := txq.LockCustomer(ctx, id); errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, errMergeCustomerNotFound
		} else if err != nil {
			return nil, nil, fmt.Errorf("lock customer %d: %w", id, err)
		}
	}
	// Read only now, under both locks: a merge that queued behind another merge
	// of the same pair must see the marker the first one wrote.
	survivor, err := txq.CustomerForMerge(ctx, into)
	if err != nil {
		return nil, nil, err
	}
	absorbed := survivor
	if from != into {
		if absorbed, err = txq.CustomerForMerge(ctx, from); err != nil {
			return nil, nil, err
		}
	}
	refusal, err := mergeRefusal(ctx, txq, survivor, absorbed, expected)
	if err != nil {
		return nil, nil, err
	}
	if refusal != nil {
		return nil, refusal, errMergeRefused
	}
	snapshot, err := absorbedSnapshot(absorbed)
	if err != nil {
		return nil, nil, err
	}

	moved, err := moveOwnRecords(ctx, txq, from, into, now)
	if err != nil {
		return nil, nil, err
	}
	for _, holder := range s.deps.CustomerReferenceHolders {
		refs, err := holder.RepointCustomer(ctx, tx, from, into)
		if err != nil {
			return nil, nil, fmt.Errorf("re-point another module's references: %w", err)
		}
		moved = append(moved, refs...)
	}

	bumped, err := txq.BumpCustomerRevision(ctx, store.BumpCustomerRevisionParams{ID: into, Now: now, ExpectedRevision: expected})
	if err != nil {
		return nil, nil, err
	}
	if bumped == 0 {
		// Unreachable: the revision was compared under this transaction's own
		// lock. The WHERE repeats it all the same, and a row that answers
		// nothing here is a bug, not a conflict to report.
		return nil, nil, fmt.Errorf("customer %d changed under its own lock", into)
	}
	if err := txq.MarkCustomerMerged(ctx, store.MarkCustomerMergedParams{ID: from, IntoCustomerID: into, Now: now}); err != nil {
		return nil, nil, err
	}
	if err := recordCustomerMerged(ctx, txq, now, into, snapshot, moved, act.Kind, act.Display, act.UserID); err != nil {
		return nil, nil, err
	}
	survivorRef := customerRefSnapshot{ID: survivor.ID, CustomerNumber: survivor.CustomerNumber, Name: survivor.Name}
	if err := recordCustomerMergedAway(ctx, txq, now, from, survivorRef, act.Kind, act.Display, act.UserID); err != nil {
		return nil, nil, err
	}
	return moved, nil, nil
}

// PostCustomersByIdMerge Merge another customer into this one
// (POST /api/v1/customers/{id}/merge)
//
// Order: (1) the body's sourceId, 400 before any database access; (2) the
// actor, resolved before the transaction opens — every refusal is only
// knowable under the locks, so one wasted directory call on those paths is
// accepted, the attach's reasoning (contacts.go); (3) the transaction, retried
// on a deadlock: the locks and their 404, the ladder's 409s, the moves, the
// holders, the writes, the events; (4) after commit, the survivor read and
// decorated from the pool — never inside the transaction, since decorating
// asks the user directory.
func (s *server) PostCustomersByIdMerge(ctx context.Context, req gen.PostCustomersByIdMergeRequestObject) (gen.PostCustomersByIdMergeResponseObject, error) {
	if req.Body == nil || req.Body.SourceId <= 0 {
		return gen.PostCustomersByIdMerge400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem("Invalid merge",
			map[string][]string{"sourceId": {"sourceId must be the id of the customer to merge into this one"}})), nil
	}
	into, from := req.Id, req.Body.SourceId

	act, err := s.actorFor(ctx, generatedFallbackActor)
	if err != nil {
		return nil, fmt.Errorf("customers: resolve actor: %w", err)
	}

	now := s.deps.Clock()
	var moved []contracts.RepointedReferences
	var refusal *gen.CustomerConflictProblem
	err = db.RetrySerializable(ctx, mergeWriteAttempts, func() error {
		return db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
			var err error
			moved, refusal, err = s.mergeCustomers(ctx, tx, into, from, req.Body.Revision, now, act)
			return err
		})
	})
	switch {
	case errors.Is(err, errMergeCustomerNotFound):
		return gen.PostCustomersByIdMerge404Response{}, nil
	case errors.Is(err, errMergeRefused):
		return gen.PostCustomersByIdMerge409ApplicationProblemPlusJSONResponse(*refusal), nil
	case err != nil:
		return nil, fmt.Errorf("customers: merge customer %d into %d: %w", from, into, err)
	}

	q := store.New(s.deps.Pool)
	row, err := q.GetCustomer(ctx, into)
	if err != nil {
		return nil, fmt.Errorf("customers: read the merged customer: %w", err)
	}
	summary, err := q.CustomerTimelineSummary(ctx, into)
	if err != nil {
		return nil, fmt.Errorf("customers: timeline summary: %w", err)
	}
	customer := fromCustomerRow(row, summary)
	dec, err := s.decorate(ctx, q, customer)
	if err != nil {
		return nil, err
	}
	moves := make([]gen.CustomerMergeMove, 0, len(moved))
	for _, m := range moved {
		moves = append(moves, gen.CustomerMergeMove{Kind: m.Kind, Count: m.Count})
	}
	return gen.PostCustomersByIdMerge200JSONResponse{
		Customer: safeCustomerResponse(customer, s.hasPermission(ctx, legalIdentityView), dec),
		Moved:    moves,
	}, nil
}
```

- [ ] **Step 11: Run, regenerate the client and the coverage, show the tests can fail, commit**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'
mise exec -- gofmt -w internal/customers internal/contracts internal/db && mise exec -- gofmt -l internal
mise exec -- go vet ./... && mise exec -- go build ./...
mise exec -- go test -count=1 ./internal/customers/ ./internal/db/ ./internal/openapi/... ./internal/module/...
mise exec -- go test -count=10 -run 'Merge.*Race' ./internal/customers/
mise exec -- go run ./internal/openapi/cmd/contract coverage -corpus ../../openapi/testdata/exchanges -out ../../openapi/COVERAGE.md
cd /home/anders/projects/vantigo/vantigo && mise exec -- bun run gen:client && git status --short
```
Expected: PASS, the whole customers package included — every existing response test still passes, since `mergedInto` is omitted for a customer that was not merged; the frozen customers corpus still validates; the operation-coverage gate counts `postCustomersByIdMerge`. `TestServeMuxConflictsArePinned` should need no pin (`POST /customers/{id}/merge` shares no method with the two-segment `/customers/contacts/{id}`, `/customers/groups/{groupId}` and `/customers/tags/{tagId}` patterns); if it reports a pair, add it to `KnownServeMuxConflicts` and to this commit. `COVERAGE.md` gains the operation under "uncovered, review by hand"; `gen:client` changes `apps/customers/frontend/src/api-schema.d.ts`. The `-count=10` run of the two races must pass all ten.

Prove each new test can fail, restoring after each: in `InsertMergedContactRoles` drop `AND NOT EXISTS (…)` (and regenerate) — the contacts test goes red with a 500 (`ux_customer_contact_roles_primary`); in `MoveCustomerAddresses` set `is_primary = a.is_primary` — the rest-test goes red the same way; delete the `MoveTimelineRevisions` call — the revisions-disagree assertion goes red; in `mergeRefusal` move the `survivor.Status == "archived"` case above the type case — the ladder's "both apply" line goes red; delete the `absorbed.MergedIntoCustomerID != nil` case — the ladder goes red (200 instead of 409) and the double-merge race goes red; swap `mergeLockOrder`'s `min`/`max` — the internal test goes red; move the `for _, holder := range …` loop after `db.WithTx` (outside the transaction, on a fresh `store.New(s.deps.Pool)` tx of its own) — the in-transaction test goes red on 0/1 and the rollback test on the duplicate being marked; delete `MergedInto: dec.merged(row.ID),` — the rest-test goes red on the duplicate's `mergedInto`; set `mergeWriteAttempts = 1` and run `-count=20 -run RacesTheDelete` — say how many of the twenty went red with a 500 (it is the deadlock path, so not all twenty; if none did, say so and raise the count until one does); change the permission's `Sensitive: true` to `false` — the catalog test goes red. Say what each printed.

```bash
cd /home/anders/projects/vantigo/vantigo
cat > /tmp/claude-1000/msg-merge-3.txt <<'EOF'
feat(customers): a customer can absorb its duplicate, every module's references with it

POST /customers/{id}/merge {sourceId, revision?}: the customer in the
path survives and absorbs sourceId in one transaction. Both rows are
locked in ascending id order; the refusals are read under the locks —
404, merge_self, merge_type_mismatch, merge_into_archived,
merge_already_merged, then the survivor's stale revision. Contacts move
with the survivor's association winning for a shared contact, roles
unioned and each role left with one primary; addresses move with a
clashing primary demoted; timeline entries and their revisions move,
payloads untouched; tags are unioned; the duplicate's registry record
and Peppol answer are deleted. Every CustomerReferenceHolder re-points
its own schema through the same transaction, and any error rolls it all
back. The survivor keeps every field of its own; the duplicate is
archived with merged_into_customer_id (migration 00029), and
customer.merged on the survivor records the duplicate's own values and
what moved, customer.merged_away on the duplicate names the survivor.
SafeCustomerResponse gains mergedInto and the directory's CustomerEntry
MergedInto. New sensitive key customers:merge; the operation wants it
with customers:view. Retried on the deadlock a concurrent contact delete
can cause.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
PATHS="apps/server/internal/db/migrations/00029_customers_merge.sql apps/server/internal/customers/sqlc.yaml \
 apps/server/internal/db/schema_test.go apps/server/internal/customers/queries/merge.sql \
 apps/server/internal/customers/queries/customers.sql apps/server/internal/customers/store/merge.sql.go \
 apps/server/internal/customers/store/customers.sql.go apps/server/internal/customers/store/models.go \
 openapi/customers.yaml apps/server/internal/openapi/specs/customers.yaml apps/server/internal/customers/gen/api.gen.go \
 apps/customers/frontend/src/api-schema.d.ts openapi/COVERAGE.md \
 apps/server/internal/contracts/directory.go apps/server/internal/customers/directory.go \
 apps/server/internal/customers/owner.go apps/server/internal/customers/customers.go \
 apps/server/internal/customers/timeline_events.go apps/server/internal/customers/module.go \
 apps/server/internal/customers/merge.go apps/server/internal/customers/merge_test.go \
 apps/server/internal/customers/merge_concurrency_test.go apps/server/internal/customers/merge_internal_test.go \
 apps/server/internal/customers/module_test.go apps/server/internal/customers/customers_test.go"
git add $PATHS && git commit -F /tmp/claude-1000/msg-merge-3.txt -- $PATHS
git show --stat HEAD && git status --short
```
If `go generate` touched a generated file outside `PATHS`, look at why before adding it; if `openapi/COVERAGE.md` did not move, drop it from `PATHS` and say so.

---

### Task 4: Customers and projects, composed for real (D1)

The one place the real merge drives a real holder: customers and projects composed by `module.Compose` exactly as `cmd/vantigo` composes them, a project of the duplicate re-pointed through projects' own SQL in the merge's transaction, and the survivor's overview — which reads projects through the project directory — showing it.

**Files:**
- Create: `apps/server/internal/integration/merge_test.go`
- Read first (do not change): `apps/server/internal/integration/harness_test.go` (`newInstallation`, `okJSON`, `projectsPath`), `rates_test.go:17-45` (creating a real customer and a project for it)

**Interfaces:**
- Wire: consumes `POST /api/v1/customers/{id}/merge` (Task 3), `GET /api/v1/customers/{id}/overview`, `GET /api/v1/projects/{id}`.

- [ ] **Step 1: Write the test**

Create `apps/server/internal/integration/merge_test.go`:

```go
package integration_test

import (
	"fmt"
	"net/http"
	"testing"
)

// TestMerge_RepointsTheRealProjectsAndTheSurvivorsOverviewShowsThem is
// customers merge design D1 end to end, against the other side itself: the
// real customers module merges, Compose has put the real projects module's
// CustomerReferenceHolder on its Deps, and projects' own statement moves the
// duplicate's project in the merge's transaction. The project then names the
// survivor — through projects' own read of the real customer directory — and
// the survivor's overview, which reads projects through the project directory,
// counts it, while the duplicate's counts nothing.
//
// Only projects is composed beside customers, deliberately: energy's and
// communications' holders prove their SQL in their own packages, and
// internal/module proves Compose collects every enabled holder. What only this
// package can prove is that a real merge reaches a real holder at all.
func TestMerge_RepointsTheRealProjectsAndTheSurvivorsOverviewShowsThem(t *testing.T) {
	t.Parallel()
	h := newInstallation(t, modCustomers, modProjects)
	admin, _ := h.SignInUser(t,
		"customers:view", "customers:create", "customers:merge",
		"projects:access", "projects:create", "projects:manage-all",
	)

	type created struct {
		Id int32 `json:"id"`
	}
	var survivor, absorbed created
	okJSON(t, admin, http.MethodPost, "/api/v1/customers", map[string]any{"name": "Acme AS"}, &survivor)
	okJSON(t, admin, http.MethodPost, "/api/v1/customers", map[string]any{"name": "Acme Norge AS"}, &absorbed)
	var project created
	okJSON(t, admin, http.MethodPost, projectsPath, map[string]any{
		"code":        "ACME1000",
		"name":        "Acme-migrering",
		"customerId":  absorbed.Id,
		"billingType": "time-and-materials",
		"currency":    "NOK",
	}, &project)
	okJSON(t, admin, http.MethodPut, fmt.Sprintf("%s/%d/status", projectsPath, project.Id), map[string]any{"status": "active"}, nil)

	var merged struct {
		Moved []struct {
			Kind  string `json:"kind"`
			Count int64  `json:"count"`
		} `json:"moved"`
	}
	okJSON(t, admin, http.MethodPost, fmt.Sprintf("/api/v1/customers/%d/merge", survivor.Id), map[string]any{"sourceId": absorbed.Id}, &merged)
	projectsMoved := int64(-1)
	for _, m := range merged.Moved {
		if m.Kind == "projects.projects" {
			projectsMoved = m.Count
		}
	}
	if projectsMoved != 1 {
		t.Errorf("moved = %+v, want projects.projects 1 from the real holder", merged.Moved)
	}

	var got struct {
		CustomerId   *int32  `json:"customerId"`
		CustomerName *string `json:"customerName"`
	}
	okJSON(t, admin, http.MethodGet, fmt.Sprintf("%s/%d", projectsPath, project.Id), nil, &got)
	if got.CustomerId == nil || *got.CustomerId != survivor.Id || got.CustomerName == nil || *got.CustomerName != "Acme AS" {
		t.Errorf("the project names customer %v %v, want the survivor %d Acme AS", got.CustomerId, got.CustomerName, survivor.Id)
	}

	type overview struct {
		Projects *struct {
			TotalCount int32 `json:"totalCount"`
			OpenCount  int32 `json:"openCount"`
			Open       []struct {
				Id int32 `json:"id"`
			} `json:"open"`
		} `json:"projects"`
	}
	var ofSurvivor, ofAbsorbed overview
	okJSON(t, admin, http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/overview", survivor.Id), nil, &ofSurvivor)
	if p := ofSurvivor.Projects; p == nil || p.TotalCount != 1 || p.OpenCount != 1 || len(p.Open) != 1 || p.Open[0].Id != project.Id {
		t.Errorf("the survivor's overview projects = %+v, want the one moved project, open", p)
	}
	okJSON(t, admin, http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/overview", absorbed.Id), nil, &ofAbsorbed)
	if p := ofAbsorbed.Projects; p == nil || p.TotalCount != 0 {
		t.Errorf("the duplicate's overview projects = %+v, want none left", p)
	}
}
```

- [ ] **Step 2: Run it, show it can fail, commit**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'
mise exec -- gofmt -l internal && mise exec -- go vet ./internal/integration/
mise exec -- go test -count=1 ./internal/integration/
```
Expected: PASS, every existing integration test included. If the project create answers 400 on `code` (the projects module validates codes), use a code its own tests use and say which.

Prove it can fail, restoring after: remove `CustomerReferences: newCustomerReferenceHolder,` from projects' `Module()` — this test goes red on `projects.projects` and on the project still naming the duplicate; in `mergeCustomers`, `continue` past every holder — the same. Say what each printed.

```bash
cd /home/anders/projects/vantigo/vantigo
cat > /tmp/claude-1000/msg-merge-4.txt <<'EOF'
test(customers): a real merge re-points a real project and the survivor's overview shows it

The integration harness composes customers and projects as cmd/vantigo
does: a project of the duplicate is moved by projects' own holder inside
the merge's transaction, then names the survivor, and the survivor's
overview counts it while the duplicate's counts nothing. Energy and
communications prove their holders in their own packages.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
PATHS="apps/server/internal/integration/merge_test.go"
git add $PATHS && git commit -F /tmp/claude-1000/msg-merge-4.txt -- $PATHS
git show --stat HEAD && git status --short
```

---

### Task 5: The docs (D5)

**Files:**
- Modify: `docs/customers.md`, `docs/projects.md`, `docs/communications.md`, `ROADMAP.md`
- Read first: the code of Tasks 1–3 — every message, code, count and kind quoted below must match it

- [ ] **Step 1: The section**

In `docs/customers.md`, directly above `## Brreg lookup` (so it follows [The duplicate-identity guard](#the-duplicate-identity-guard), the way duplicates are found), insert:

```markdown
## Merging duplicates

Two customers that are the same real-world entity become one (phase 6 delivery B,
decided in
[`docs/superpowers/specs/2026-09-24-customers-merge-design.md`](superpowers/specs/2026-09-24-customers-merge-design.md)).
`POST /customers/{id}/merge` with `{sourceId, revision?}`: the customer in the path
**survives** and **absorbs** `sourceId`. It needs `customers:merge` and
`customers:view` — merging rewrites other modules' data and archives a customer,
which is more than `customers:delete` does. `revision` is the survivor's and
optional; stale, it is the [revision conflict](#revision-and-concurrency). The
absorbed customer needs none: it is going away.

### The refusals, in order

| Answer | When |
| --- | --- |
| 400 | `sourceId` is missing or not a positive id |
| 404 | either customer does not exist |
| 409 `merge_self` | the same customer twice |
| 409 `merge_type_mismatch` | a person and a business — a merge never changes what a customer is |
| 409 `merge_into_archived` | the survivor is archived; restore it first |
| 409 `merge_already_merged` | the absorbed customer was merged away before; the detail names where |
| 409 without a code | the survivor's `revision` is stale |

An **archived** customer may be absorbed — the usual case: the duplicate was
archived when somebody noticed it.

### What moves, what stays, what is recorded

| | |
| --- | --- |
| The survivor's own row | Kept, every field: name, type, status, legal identity, contact info, billing profile, owner, group, customer number. Nothing is filled in from the absorbed customer. |
| Contacts | Every association moves. A contact linked to both keeps the survivor's association — its title, phone and email. Roles are unioned: the survivor's primary for a role stays, the absorbed customer's primary becomes the survivor's for a role it had nobody in, and every other primary flag is dropped, so each role still has exactly one. A role keeps its `created_at`, which is what "longest-standing" means when a primary later steps down. |
| Addresses | Every address moves, label and all; the absorbed primary of a type the survivor already has a primary for is demoted. The 50-address cap guards a write, not a merge: a survivor may end past 50, and adds another only once it is under again. |
| Timeline | Every entry and every revision moves, follow-ups with them. Payloads are **not** rewritten: `payload.customerId` says which customer an event happened to at the time, and the merge event says the rest. |
| Tags | Unioned. |
| Registry record, Peppol answer | The survivor keeps its own; the absorbed customer's are deleted — they described an identity the survivor either shares or does not have, and a refresh or a re-check fetches either again. |
| Other modules | Re-pointed in the same transaction ([below](#one-transaction-every-module)). |
| The absorbed customer | Archived, with `merged_into_customer_id` naming the survivor (migration `00029`, a foreign key to this table, `ON DELETE RESTRICT`). It keeps its name, number, identity, contact info and billing profile, so its page still reads, and its response carries `mergedInto: {id, customerNumber, name}`. |

Both rows' `revision` advances. Two events are written, and no
`customer.status_changed` beside them — the merge is the reason:

- **`customer.merged`** on the survivor. Summary "Absorbed #1005 Acme Norge AS: 3
  contacts, 2 addresses, 14 timeline entries, 2 projects" — only the kinds there
  were. Payload `{customerId, absorbed: {id, customerNumber, name, type, status,
  identity, contactInfo, billingProfile, ownerUserId, groupId}, moved: [{kind,
  count}]}`: the absorbed customer's own values, so a person who wanted its billing
  profile or its identity can still read them.
- **`customer.merged_away`** on the absorbed customer. Summary "Merged into #1002
  Acme AS", payload `{customerId, into: {id, customerNumber, name}}`.

The answer is `CustomerMergeResult {customer, moved}`: the survivor as `GET` would
answer it, and every kind with its count, a zero included — this module's four first,
each counting what the absorbed customer had (`customers.contacts` its associations,
a contact the survivor already had included; `customers.addresses`;
`customers.timelineEntries` its active entries; `customers.tags` its tags), then each
other module's in the order the installation composes them. A module that is not
enabled lists nothing.

### One transaction, every module

All modules share one database, so a merge is one transaction and needs no event
bus. It locks both customer rows in ascending id order — the order every writer that
locks several customers takes ([Typed roles](#typed-roles-and-one-primary-per-role)) —
reads the refusals under the locks, moves this module's tables, and then hands the
same transaction to each `contracts.CustomerReferenceHolder` Compose collected
([module boundaries rule 8](module-boundaries.md#the-rules)). Each holder runs its
own SQL, on its own schema, from its own package:

| Holder | What it re-points (kind) |
| --- | --- |
| projects | `projects.projects.customer_id` (`projects.projects`). Each moved project's revision advances; no project timeline entry is written. Time and expenses reach a customer only through a project, so they hold nothing. |
| energy | `energy.supply_periods.customer_id` (`energy.supplyPeriods`). The overlap constraint is per metering point, so a re-point cannot violate it. Energy has no module doc of its own; this row is its paragraph. |
| communications | `conversations.customer_id` (`communications.conversations`), `conversations.suggested_customer_id` (`communications.conversationSuggestions`), and the candidate list (`communications.conversationCandidates`), where a conversation that already lists the survivor keeps it once. |

Any error, a holder's included, rolls back everything: nothing moved, no marker, no
event. The transaction runs under `db.RetrySerializable` with three attempts, for two
lock cycles: copying an association takes a key-share on the contact row while
`DELETE /customers/contacts/{id}` takes the contact first and the customers after; and
the tag union locks the absorbed customer's tag links and then key-shares each tag while
`DELETE /customers/tags/{tagId}` locks the tag and then, by its cascade, those links.
Either pair can deadlock, and the loser runs again. An attach cannot — it takes the
customer first, as a merge does.

The [directory](#contractscustomerdirectory) still answers an absorbed customer —
archived, with `CustomerEntry.MergedInto`. The merge re-points every reference that
existed when it ran; a module that accepts archived customers (projects, energy and
communications all do) can still write one for the merged-away id afterwards, or commit
one while the merge runs, and the directory's `MergedInto` tells that consumer where to
look.

A merged-away customer is an archived customer like any other: it can still be
edited or restored through the API (the page hides those actions), and the marker
stays either way. Not built: un-merging (the event payload is the record), merging
more than two at once, filling the survivor's blank fields from the absorbed
customer, rewriting historical payloads, and a "find duplicates" report — the
duplicate-identity guard and the list search are how duplicates are found today.
```

- [ ] **Step 2: The rest of D5**

In `docs/customers.md`:

**Statuses** — after the bullet beginning `- **Customers are archived, never deleted.**`, add:

```markdown
- **A merged-away customer is archived too** ([Merging duplicates](#merging-duplicates)):
  `merged_into_customer_id` names the customer that absorbed it, and its response
  carries `mergedInto`.
```

**Permissions** — `Fourteen keys, category-grouped, every one delegable.` becomes `Fifteen keys, category-grouped, every one delegable.`; after the `customers:billing-manage` row of the table add:

```markdown
| `customers:merge` | Customers | Merge a duplicate customer into another, moving its contacts, addresses, timeline, tags and other modules' references, and archiving it. | yes |
```

and after the paragraph beginning `` `customers:billing-manage` is the one key with no earlier counterpart`` add:

```markdown
`customers:merge` is the second ([Merging duplicates](#merging-duplicates)): a merge
rewrites other modules' references and archives a customer, which is more than
`customers:delete` does, so neither key implies the other.
```

**`contracts.CustomerDirectory`** — in the code block, `// {ID, Name, Archived, Group}` becomes `// {ID, Name, Archived, Group, MergedInto}`; at the end of the "Archived customers still resolve" bullet add the sentence `A merged-away customer resolves the same way, with MergedInto naming the customer that absorbed it.` (with `MergedInto` in backticks).

**The frontend** — after the **List** bullet add:

```markdown
- **Merge…** on the customer page header, for a caller the host says may merge (a
  new `canMerge` prop, read from `customers:merge`): a modal with this package's own
  customer picker (the projects picker's shape, over this module's list with
  archived customers included, leaving out this customer and any merged away), a
  plain sentence of what will happen, a warning, and the Merge button disabled, for
  two types or an archived survivor, then the counts of what moved; the page
  refreshes. A merged-away customer's page shows a banner "Merged into #1002 Acme AS"
  linking there, and shows no edit action anywhere: the header hides Edit, Change
  type, Archive, Restore and Merge, and every card — owner, tags and group, contact
  info and addresses, the registry record, the billing profile, contacts, the
  timeline — is handed its capability as `canX && !customer.mergedInto` (the contacts
  card, which had none, takes `readOnly`). The create and edit forms'
  duplicate-identity conflict adds a line suggesting Merge… on the duplicate it
  already links to, for a caller who may merge — the list page passes `canMerge`
  too, since it opens the same form.
```

**API** — `59 operations in total` becomes `60 operations in total`; after the `DELETE /{id}` (archive) row add:

```markdown
| `POST /{id}/merge` | `customers:merge` + `customers:view` |
```

**What comes next** — in the **Phase 6 delivery A** paragraph, its closing `Still ahead in phase 6: merging duplicate customers (delivery B) and GDPR handling for person customers (delivery C).` (it wraps over three lines) becomes `Still ahead in phase 6 after it: merging (delivery B, below) and GDPR handling for person customers (delivery C).`; directly after that paragraph add:

```markdown
**Phase 6 delivery B** — [Merging duplicates](#merging-duplicates) — has landed,
decided in
[`docs/superpowers/specs/2026-09-24-customers-merge-design.md`](superpowers/specs/2026-09-24-customers-merge-design.md):
`POST /customers/{id}/merge` absorbs a duplicate into the customer in the path in one
transaction — its contacts, addresses, timeline, tags and every other module's
references, the survivor keeping every field of its own and the duplicate archived
with a marker. It added one permission key (`customers:merge`), one migration
(`00029`), two event types and the first cross-module write contract,
`contracts.CustomerReferenceHolder`, which projects, energy and communications
implement. Still ahead in phase 6: GDPR handling for person customers (delivery C).
```

and in the paragraph beginning `Past that, the remaining gaps are exactly`, `no merge and no GDPR handling (phase 6 deliveries B and C)` (wrapped) becomes `no GDPR handling (phase 6 delivery C)`.

In `docs/projects.md`, directly above `### What Time tracking should build on`, add:

```markdown
Projects also **holds customer references** — `contracts.CustomerReferenceHolder`,
the one sanctioned cross-module write ([module boundaries rule 8](module-boundaries.md#the-rules)).
When the customers module merges two customers, `RepointProjectsCustomer` moves every
project of the absorbed customer to the survivor inside the merge's own transaction,
advancing each moved project's revision like any other change to the row, so an edit
form still holding the old customer answers the stale-revision 409. No project
timeline entry is written: the merge is recorded on the survivor's customer timeline,
and `customerName` reads the survivor's from then on. Time and expenses hold no
customer id of their own, so the move keeps them right too. See
[Merging duplicates](customers.md#merging-duplicates).
```

In `docs/communications.md`, at the end of `## In-process customer integration` (directly above `## SMTP`), add:

```markdown
The one write in the other direction is a customer merge: communications declares a
`contracts.CustomerReferenceHolder` ([module boundaries rule 8](module-boundaries.md#the-rules)),
and when the customers module merges two customers it re-points, inside the merge's
own transaction, every conversation's `customer_id` and `suggested_customer_id` from
the absorbed customer to the survivor, and the candidate list — where a conversation
that already lists the survivor keeps it once. See
[Merging duplicates](customers.md#merging-duplicates).
```

In `ROADMAP.md`, phase 6: replace `**Still ahead in this phase:** merging duplicate customers (delivery B) and GDPR
handling for person customers (delivery C).` with:

```markdown
**Delivery B (done)** — decided in
[`docs/superpowers/specs/2026-09-24-customers-merge-design.md`](docs/superpowers/specs/2026-09-24-customers-merge-design.md):
`POST /customers/{id}/merge` — one customer absorbs its duplicate in one transaction:
contacts (roles unioned), addresses, timeline, tags, and every other module's
references through `contracts.CustomerReferenceHolder` (projects, energy,
communications); the survivor keeps every field of its own and the duplicate is
archived with a marker; behind the new `customers:merge`. See
[`docs/customers.md#merging-duplicates`](docs/customers.md#merging-duplicates).

**Still ahead in this phase:** GDPR handling for person customers (delivery C).
```

- [ ] **Step 3: Check the docs against the code, commit**

Read the new section against `merge.go`, `timeline_events.go`, `queries/merge.sql` and the three holders: every code, title, count rule, kind name, summary shape and the retry count must match the code; the API row against `openapi/customers.yaml`; the count 60 against `grep -c "operationId:" openapi/customers.yaml`; every link target heading exists (`grep -n '^## \|^### ' docs/customers.md docs/module-boundaries.md`).

```bash
cd /home/anders/projects/vantigo/vantigo
grep -c "operationId:" openapi/customers.yaml
grep -n 'Merging duplicates\|rule 8' docs/*.md ROADMAP.md
cat > /tmp/claude-1000/msg-merge-5.txt <<'EOF'
docs(customers): merging duplicates, and phase 6 delivery B

docs/customers.md gains the Merging duplicates section — the direction,
the refusals in order, what moves, stays and is recorded, the two
events, the one transaction and its three holders, the retry, the
marker — plus the status note, the customers:merge key, the directory's
MergedInto, the frontend bullet, the API row and the phase 6 delivery B
paragraph. docs/projects.md and docs/communications.md say what each
re-points; energy, which has no module doc, is its row in the holder
table. The roadmap marks delivery B done, with GDPR ahead.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
PATHS="docs/customers.md docs/projects.md docs/communications.md ROADMAP.md"
git add $PATHS && git commit -F /tmp/claude-1000/msg-merge-5.txt -- $PATHS
git show --stat HEAD && git status --short
```

---

### Task 6: The frontend (D4)

**Files:**
- Create: `apps/customers/frontend/src/api/merge.ts`, `api/merge.test.ts`, `components/customer-picker.tsx`, `components/customer-picker.test.tsx`, `pages/-customer-merge-modal.tsx`, `pages/-customer-merge-modal.test.tsx`, `pages/-customer-merge-header.test.tsx`
- Modify: `apps/customers/frontend/src/api/customers.ts`, `api/customers.test.ts`, `pages/customers.$customerId.tsx`, `pages/-customer-form-modal.tsx`, `pages/-customer-form-modal.test.tsx`, `pages/-customer-contacts-card.tsx`, `pages/customers.index.tsx`, `i18n.ts`; `apps/host/frontend/src/routes/customers/-customer-detail-layout.tsx`, `customer-detail-route.test.tsx`, `-customers-list.tsx`, `customers-list-route.test.tsx`, `apps/host/frontend/src/catalogs/admin.ts`, `catalogs/admin.test.ts`
- Read first (do not change): `apps/projects/frontend/src/components/customer-picker.tsx` (the shape — copied, never imported), `apps/customers/frontend/src/components/user-picker.tsx` and its test (this package's picker idiom), `pages/-customer-archive.test.tsx` (rendering the header), `pages/-customer-form-modal.test.tsx:22-60` (`renderModalWithRouter`), `pages/-manage-groups-modal.tsx` (a modal's shape here)

**Interfaces:**
- Produces TS: `CustomerMergedInto {id; customerNumber; name}`, `CustomerResponse.mergedInto: CustomerMergedInto | null`, `CustomersQueryParams.includeArchived?: boolean`, `RawCustomerResponse` (now exported) (api/customers.ts); `CustomerMergeMove`, `CustomerMergeResult`, `CustomerMergeInput`, `mergeCustomer(id, input)` (api/merge.ts); `CustomerPicker({label, placeholder, value, onChange, excludeId, disabled?})`; `CustomerMergeModal({customer, opened, onClose})`; `CustomerDetailHeader` prop `canMerge?`; `CustomerOverview` gates every card on `!customer.mergedInto`; `CustomerContactsCard` prop `readOnly?`; `CustomerFormModal` prop `canMerge?`; `CustomersPage` prop `canMerge?`.
- Wire: consumes Task 3.

- [ ] **Step 1: Write the failing tests**

Create `apps/customers/frontend/src/api/merge.test.ts`:

```ts
import { afterEach, describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import { mergeCustomer } from "./merge";
import { ApiConflictError } from "./request";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": status >= 400 ? "application/problem+json" : "application/json" },
  });

// Literally the body the server sends: the survivor carries no owner, group,
// tags or mergedInto keys at all, because it omits what is unset.
const answered = {
  customer: {
    id: 1002,
    customerNumber: 2,
    name: "Acme AS",
    status: "active",
    type: "business",
    createdAt: "2026-06-01T10:00:00Z",
    updatedAt: "2026-09-24T10:00:00Z",
    timelineSummary: { entryCount: 17, latestOccurredOn: "2026-09-24" },
    revision: 5,
  },
  moved: [
    { kind: "customers.contacts", count: 3 },
    { kind: "projects.projects", count: 2 },
  ],
};

describe("mergeCustomer", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("POSTs the source and this customer's revision, and normalises the survivor it answers", async () => {
    const fetchMock = vi.fn((_url: RequestInfo | URL, _init?: RequestInit) => Promise.resolve(jsonResponse(200, answered)));
    stubFetch(fetchMock);

    const result = await mergeCustomer(1002, { sourceId: 1005, revision: 4 });

    expect(fetchMock).toHaveBeenCalledWith("/api/v1/customers/1002/merge", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ sourceId: 1005, revision: 4 }),
    });
    expect(result.customer).toMatchObject({ id: 1002, revision: 5, owner: null, group: null, mergedInto: null, tags: [] });
    expect(result.moved).toEqual(answered.moved);
  });

  it("throws a refusal as an ApiConflictError carrying its code", async () => {
    stubFetch(
      vi.fn(() =>
        Promise.resolve(
          jsonResponse(409, {
            title: "Customer already merged",
            status: 409,
            code: "merge_already_merged",
            detail: "#5 Acme Norge AS was already merged into #2 Acme AS.",
          }),
        ),
      ),
    );

    const error = await mergeCustomer(1003, { sourceId: 1005 }).catch((e: unknown) => e);

    expect(error).toBeInstanceOf(ApiConflictError);
    expect((error as ApiConflictError).code).toBe("merge_already_merged");
  });
});
```

In `apps/customers/frontend/src/api/customers.test.ts`: add `customersSearchParams,` to the `./customers` import list (after `customersQueryOptions,`); give every expected customer shape the new field — run

```bash
cd /home/anders/projects/vantigo/vantigo/apps/customers/frontend/src
sed -i 's/owner: null, group: null, tags: \[\] }/owner: null, group: null, mergedInto: null, tags: [] }/' api/customers.test.ts
sed -i 's/^\(\s*\)group: null,$/&\n\1mergedInto: null,/' api/customers.test.ts pages/-customer-form-modal.test.tsx
grep -c 'mergedInto: null' api/customers.test.ts pages/-customer-form-modal.test.tsx
```
(expect 4 and 6; run it before adding the form-modal test below, which already carries the field) — and add at the end of `describe("customerQueryOptions", …)`:

```ts
  it("carries mergedInto through, and reads its absence as null", async () => {
    // A merged-away customer (customers merge design D3); every other customer
    // arrives without the key at all, which the tests above already cover.
    const mergedAway = {
      id: 1005,
      name: "Acme Norge AS",
      status: "archived",
      timelineSummary: { entryCount: 1, latestOccurredOn: "2026-09-24" },
      mergedInto: { id: 1002, customerNumber: 2, name: "Acme AS" },
    };
    stubFetch(vi.fn().mockResolvedValue(jsonResponse(200, mergedAway)));

    const options = customerQueryOptions(1005);
    const result = (await (options.queryFn as (context: unknown) => Promise<unknown>)({
      signal: undefined,
    })) as CustomerResponse;

    expect(result.mergedInto).toEqual({ id: 1002, customerNumber: 2, name: "Acme AS" });
  });

  it("asks for archived customers only when told to", () => {
    // The merge picker's search: an archived duplicate is the usual thing to absorb.
    expect(customersSearchParams({ page: 1, pageSize: 20, includeArchived: true })).toBe(
      "?page=1&pageSize=20&includeArchived=true",
    );
    expect(customersSearchParams({ page: 1, pageSize: 20 })).toBe("?page=1&pageSize=20");
  });
```

Create `apps/customers/frontend/src/components/customer-picker.test.tsx`:

```tsx
import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import { CustomerPicker } from "./customer-picker";

const jsonResponse = (body: unknown) =>
  new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } });

// Literally what the list answers: only the merged-away one carries mergedInto.
const wire = (id: number, customerNumber: number, name: string, extra: Record<string, unknown> = {}) => ({
  id,
  customerNumber,
  name,
  status: "active",
  type: "business",
  createdAt: "2026-06-01T10:00:00Z",
  updatedAt: "2026-06-01T10:00:00Z",
  timelineSummary: { entryCount: 0 },
  ...extra,
});
const page = {
  data: [
    wire(1002, 2, "Acme AS"),
    wire(1005, 5, "Acme Norge AS", { status: "archived" }),
    wire(1007, 7, "Acme Gammel AS", { status: "archived", mergedInto: { id: 1002, customerNumber: 2, name: "Acme AS" } }),
  ],
  pagination: { page: 1, pageSize: 20, totalCount: 3, totalPages: 1, hasNextPage: false, hasPreviousPage: false },
};

afterEach(() => vi.unstubAllGlobals());

describe("CustomerPicker", () => {
  it("searches archived customers too, and offers neither the customer it is for nor one merged away", async () => {
    const fetchMock = vi.fn((_url: RequestInfo | URL, _init?: RequestInit) => Promise.resolve(jsonResponse(page)));
    stubFetch(fetchMock);
    const onChange = vi.fn();
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <MantineProvider env="test">
        <QueryClientProvider client={queryClient}>
          <CustomerPicker label="Duplicate" placeholder="Search" value={null} onChange={onChange} excludeId={1002} />
        </QueryClientProvider>
      </MantineProvider>,
    );

    await userEvent.click(screen.getByRole("combobox", { name: "Duplicate" }));
    const option = await screen.findByRole("option", { name: "#5 Acme Norge AS · Archived" });

    expect(screen.queryByRole("option", { name: "#2 Acme AS" })).not.toBeInTheDocument();
    expect(screen.queryByRole("option", { name: /Acme Gammel AS/ })).not.toBeInTheDocument();
    expect(
      fetchMock.mock.calls.some(([url]) => String(url) === "/api/v1/customers?page=1&pageSize=20&includeArchived=true"),
    ).toBe(true);

    await userEvent.click(option);
    expect(onChange).toHaveBeenCalledWith(expect.objectContaining({ id: 1005, name: "Acme Norge AS", mergedInto: null }));
  });
});
```

Create `apps/customers/frontend/src/pages/-customer-merge-modal.test.tsx`:

```tsx
import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { type CustomerResponse, normalizeCustomer, type RawCustomerResponse } from "../api/customers";
import { stubFetch } from "../test/fetch";
import { CustomerMergeModal } from "./-customer-merge-modal";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": status >= 400 ? "application/problem+json" : "application/json" },
  });

const wire = (overrides: Record<string, unknown> = {}) => ({
  id: 1002,
  customerNumber: 2,
  name: "Acme AS",
  status: "active",
  type: "business",
  createdAt: "2026-06-01T10:00:00Z",
  updatedAt: "2026-07-01T10:00:00Z",
  timelineSummary: { entryCount: 3, latestOccurredOn: null },
  revision: 4,
  ...overrides,
});
const survivor = (overrides: Record<string, unknown> = {}): CustomerResponse =>
  normalizeCustomer(wire(overrides) as RawCustomerResponse);
const listOf = (...customers: unknown[]) => ({
  data: customers,
  pagination: { page: 1, pageSize: 20, totalCount: customers.length, totalPages: 1, hasNextPage: false, hasPreviousPage: false },
});
const duplicate = wire({ id: 1005, customerNumber: 5, name: "Acme Norge AS", status: "archived", revision: 7 });

const renderModal = (customer: CustomerResponse, onRequest: (url: string, init?: RequestInit) => Promise<Response>) => {
  const fetchMock = vi.fn((url: RequestInfo | URL, init?: RequestInit) => onRequest(String(url), init));
  stubFetch(fetchMock);
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(
    <MantineProvider env="test">
      <QueryClientProvider client={queryClient}>
        <CustomerMergeModal customer={customer} opened onClose={vi.fn()} />
      </QueryClientProvider>
    </MantineProvider>,
  );
  return fetchMock;
};

/** The merge POSTs fetch saw — never "the last fetch": the picker's search runs on its own clock. */
const mergePosts = (fetchMock: ReturnType<typeof vi.fn>) =>
  fetchMock.mock.calls.filter(
    ([url, init]) => String(url) === "/api/v1/customers/1002/merge" && (init as RequestInit | undefined)?.method === "POST",
  );

const pick = async (name: string) => {
  await userEvent.click(screen.getByRole("combobox", { name: /duplicate to merge/i }));
  await userEvent.click(await screen.findByRole("option", { name }));
};

describe("CustomerMergeModal", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("says what will happen, merges with this customer's revision, and shows what moved", async () => {
    const fetchMock = renderModal(survivor(), (_url, init) =>
      Promise.resolve(
        init?.method === "POST"
          ? jsonResponse(200, {
              customer: wire({ revision: 5 }),
              moved: [
                { kind: "customers.contacts", count: 3 },
                { kind: "customers.addresses", count: 0 },
                { kind: "customers.timelineEntries", count: 14 },
                { kind: "customers.tags", count: 0 },
                { kind: "projects.projects", count: 2 },
                { kind: "somewhere.else", count: 1 },
              ],
            })
          : jsonResponse(200, listOf(duplicate)),
      ),
    );

    await pick("#5 Acme Norge AS · Archived");
    expect(screen.getByText(/^Everything on #5 Acme Norge AS — contacts, addresses, timeline, tags/)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Merge" }));

    await waitFor(() => expect(mergePosts(fetchMock)).toHaveLength(1));
    expect(mergePosts(fetchMock)[0][1]).toEqual({
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ sourceId: 1005, revision: 4 }),
    });
    expect(await screen.findByText("#5 Acme Norge AS was merged into this customer.")).toBeInTheDocument();
    for (const moved of ["3 contacts", "14 timeline entries", "2 projects", "1 × somewhere.else"]) {
      expect(screen.getByText(moved)).toBeInTheDocument();
    }
    expect(screen.queryByText(/^0 /)).not.toBeInTheDocument();
  });

  it("warns, and will not merge, a customer of the other type", async () => {
    const person = wire({ id: 1006, customerNumber: 6, name: "Kari Nordmann", type: "person" });
    const fetchMock = renderModal(survivor(), () => Promise.resolve(jsonResponse(200, listOf(person))));

    await pick("#6 Kari Nordmann");

    expect(screen.getByText(/^#6 Kari Nordmann is a private person and this customer is a business\./)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Merge" })).toBeDisabled();
    expect(mergePosts(fetchMock)).toHaveLength(0);
  });

  it("will not merge into an archived customer", async () => {
    renderModal(survivor({ status: "archived" }), () => Promise.resolve(jsonResponse(200, listOf(duplicate))));

    expect(screen.getByText("This customer is archived. Restore it before merging another customer into it.")).toBeInTheDocument();
    await pick("#5 Acme Norge AS · Archived");
    expect(screen.getByRole("button", { name: "Merge" })).toBeDisabled();
  });

  it("names a refusal the server answered", async () => {
    renderModal(survivor(), (_url, init) =>
      Promise.resolve(
        init?.method === "POST"
          ? jsonResponse(409, {
              title: "Customer already merged",
              status: 409,
              code: "merge_already_merged",
              detail: "#5 Acme Norge AS was already merged into #3 Acme Holding AS.",
            })
          : jsonResponse(200, listOf(duplicate)),
      ),
    );

    await pick("#5 Acme Norge AS · Archived");
    await userEvent.click(screen.getByRole("button", { name: "Merge" }));

    expect(await screen.findByText("That customer has already been merged into another one.")).toBeInTheDocument();
  });
});
```

Create `apps/customers/frontend/src/pages/-customer-merge-header.test.tsx`:

```tsx
import { MantineProvider } from "@mantine/core";
import { ModalsProvider } from "@mantine/modals";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRootRoute, createRouter, RouterProvider } from "@tanstack/react-router";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Suspense } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import { CustomerDetailHeader, CustomerOverview } from "./customers.$customerId";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const customer = (overrides: Record<string, unknown> = {}) => ({
  id: 1005,
  customerNumber: 5,
  name: "Acme Norge AS",
  status: "active",
  type: "business",
  createdAt: "2026-06-01T10:00:00Z",
  updatedAt: "2026-07-01T10:00:00Z",
  timelineSummary: { entryCount: 0, latestOccurredOn: null },
  revision: 4,
  ...overrides,
});

/**
 * The header under a real router, since the merged-away banner links to the
 * survivor through the package's router-Link convention (see
 * `-customer-form-modal.test.tsx`'s `renderModalWithRouter`).
 */
const renderHeader = async (
  body: ReturnType<typeof customer>,
  props: { canArchive?: boolean; canRestore?: boolean; canMerge?: boolean } = {},
) => {
  stubFetch(
    vi.fn((url: RequestInfo | URL) => {
      const path = String(url);
      if (path === "/api/v1/customers/1005/legal-identity") return Promise.resolve(new Response(null, { status: 403 }));
      if (path === "/api/v1/customers/1005") return Promise.resolve(jsonResponse(200, body));
      return Promise.resolve(new Response(null, { status: 404 }));
    }),
  );
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
};

describe("customer detail header — merge", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("offers Merge… only to a caller the host says may merge, and opens the modal", async () => {
    await renderHeader(customer());
    expect(screen.queryByRole("button", { name: "Merge…" })).not.toBeInTheDocument();
    cleanup();
    vi.unstubAllGlobals();

    await renderHeader(customer(), { canMerge: true });
    await userEvent.click(screen.getByRole("button", { name: "Merge…" }));

    expect(await screen.findByRole("dialog", { name: "Merge a duplicate into Acme Norge AS" })).toBeInTheDocument();
  });

  it("shows where a merged-away customer went, and hides every action that would edit it", async () => {
    await renderHeader(
      customer({ status: "archived", mergedInto: { id: 1002, customerNumber: 2, name: "Acme AS" } }),
      { canArchive: true, canRestore: true, canMerge: true },
    );

    expect(screen.getByText("Merged into #2 Acme AS")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Open #2 Acme AS" })).toHaveAttribute("href", "/customers/1002");
    expect(screen.queryByText(/this customer is archived/i)).not.toBeInTheDocument();
    for (const action of ["Edit customer", "Change type", "Archive customer", "Restore customer", "Merge…"]) {
      expect(screen.queryByRole("button", { name: action })).not.toBeInTheDocument();
    }
  });
});

/**
 * The page body (every card) under the same router, with every capability the
 * host could pass. Only the customer is answered; each card's own read gets a
 * 404, which is enough: the two actions asserted here sit in their cards'
 * headers and render whatever the card's data does.
 */
const renderOverview = async (body: ReturnType<typeof customer>) => {
  stubFetch(
    vi.fn((url: RequestInfo | URL) =>
      Promise.resolve(
        String(url) === "/api/v1/customers/1005" ? jsonResponse(200, body) : new Response(null, { status: 404 }),
      ),
    ),
  );
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const rootRoute = createRootRoute({
    component: () => (
      <MantineProvider env="test">
        <ModalsProvider>
          <QueryClientProvider client={queryClient}>
            <Suspense fallback={<div>Loading…</div>}>
              <CustomerOverview
                customerId={1005}
                canEdit
                canManageBilling
                canViewIdentity
                canManageIdentity
                canManageTimeline
              />
            </Suspense>
          </QueryClientProvider>
        </ModalsProvider>
      </MantineProvider>
    ),
  });
  const router = createRouter({ routeTree: rootRoute, history: createMemoryHistory({ initialEntries: ["/"] }) });
  await router.load();
  render(<RouterProvider router={router} />);
};

describe("customer page body — a merged-away customer", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("offers no edit action on any card, whatever the host allows", async () => {
    // The positive control first: the same capabilities on a customer that was
    // not merged show the cards' actions, so their absence below is the gate's.
    await renderOverview(customer());
    expect(await screen.findByRole("button", { name: "Add contact" })).toBeInTheDocument();
    expect(await screen.findByRole("button", { name: "Add timeline event" })).toBeInTheDocument();
    cleanup();
    vi.unstubAllGlobals();

    await renderOverview(customer({ status: "archived", mergedInto: { id: 1002, customerNumber: 2, name: "Acme AS" } }));
    expect((await screen.findAllByText("Contacts")).length).toBeGreaterThan(0);
    for (const action of ["Add contact", "Add timeline event", "Add address", "Edit contact details"]) {
      expect(screen.queryByRole("button", { name: action })).not.toBeInTheDocument();
    }
  });
});
```

In `apps/customers/frontend/src/pages/-customer-form-modal.test.tsx`: add `cleanup` to the `@testing-library/react` import; give `renderModalWithRouter` a third parameter — `async (state: Parameters<typeof CustomerFormModal>[0]["state"], onClose = vi.fn(), canMerge = false)` — and pass it on: `<CustomerFormModal state={state} onClose={onClose} canMerge={canMerge} />`; then add, after the test `shows the duplicate alert and its anyway button even when the conflict names nobody`:

```tsx
  it("suggests Merge… on an edit's duplicate identity, for a caller who may merge", async () => {
    // Customers merge design D4: the duplicate is already a link; the hint says
    // the fix may be a merge, from that customer's page — never a merge from
    // the form. Without canMerge the line would point at a button the caller
    // does not have.
    const editState = (): Parameters<typeof CustomerFormModal>[0]["state"] => ({
      mode: "edit",
      customer: {
        id: 1001,
        customerNumber: 5001,
        name: "Initech",
        status: "active",
        createdAt: "2026-01-01T00:00:00Z",
        updatedAt: "2026-01-01T00:00:00Z",
        type: "business",
        identity: null,
        timelineSummary: { entryCount: 0, latestOccurredOn: null },
        owner: null,
        group: null,
        mergedInto: null,
        tags: [],
        revision: 3,
      },
    });
    const conflicting = () =>
      stubFetch(
        vi.fn((_url: RequestInfo | URL, init?: RequestInit) =>
          Promise.resolve(
            init?.method === "PUT"
              ? jsonResponse(409, {
                  title: "Duplicate legal identity",
                  code: "duplicate_legal_identity",
                  detail: "Another customer already has this legal identity.",
                  status: 409,
                  duplicates: [{ id: 2002, customerNumber: 6002, name: "Acme Holding AS", status: "active" }],
                })
              : new Response(null, { status: 404 }),
          ),
        ),
      );
    const hint = /open it and use Merge… there to bring the two together/i;

    conflicting();
    await renderModalWithRouter(editState(), vi.fn(), true);
    await userEvent.click(screen.getByRole("button", { name: /save changes/i }));
    expect(await screen.findByText(hint)).toBeInTheDocument();
    cleanup();
    vi.unstubAllGlobals();

    conflicting();
    await renderModalWithRouter(editState());
    await userEvent.click(screen.getByRole("button", { name: /save changes/i }));
    expect(await screen.findByText("Acme Holding AS")).toBeInTheDocument();
    expect(screen.queryByText(hint)).not.toBeInTheDocument();
  });

  it("suggests Merge… on a create's duplicate identity too, in the create's own words", async () => {
    // D4 names both forms. A customer being created does not exist yet, so the
    // line says to open the duplicate instead — or create anyway and merge
    // there.
    stubFetch(
      vi.fn((url: RequestInfo | URL, init?: RequestInit) =>
        Promise.resolve(
          String(url) === "/api/v1/customers" && init?.method === "POST"
            ? jsonResponse(409, {
                title: "Duplicate legal identity",
                code: "duplicate_legal_identity",
                detail: "Another customer already has this legal identity.",
                status: 409,
                duplicates: [{ id: 2002, customerNumber: 6002, name: "Acme Holding AS", status: "active" }],
              })
            : new Response(null, { status: 404 }),
        ),
      ),
    );

    await renderModalWithRouter({ mode: "create" }, vi.fn(), true);
    await userEvent.type(screen.getByLabelText(/name/i), "Acme Holding");
    await userEvent.click(screen.getByRole("button", { name: /create customer/i }));

    expect(
      await screen.findByText(/open it instead — or create this one anyway and use Merge… there/i),
    ).toBeInTheDocument();
    expect(screen.queryByText(/open it and use Merge… there to bring the two together/i)).not.toBeInTheDocument();
  });
```

In `apps/host/frontend/src/routes/customers/customer-detail-route.test.tsx`: the `CustomerDetailHeader` mock takes `canMerge?: boolean` beside `canArchive`/`canRestore` and renders `<span data-testid="can-merge">{String(Boolean(canMerge))}</span>`; in `describe("the customer detail route's archive/restore capability props", …)` add:

```tsx
  it("passes canMerge from customers:merge, and only from it", () => {
    // customers:delete archives, customers:update restores; neither merges
    // (customers merge design D2).
    vi.mocked(useQuery).mockImplementation((options) =>
      options.queryKey[0] === "authorization"
        ? ({ data: { permissions: ["customers:delete", "customers:update"] }, isPending: false } as never)
        : ({ data: { user: { id: "user-1", roles: [] } } } as never),
    );
    renderCustomerDetailLayout();
    expect(screen.getByTestId("can-merge")).toHaveTextContent("false");
    cleanup();

    vi.mocked(useQuery).mockImplementation((options) =>
      options.queryKey[0] === "authorization"
        ? ({ data: { permissions: ["customers:merge"] }, isPending: false } as never)
        : ({ data: { user: { id: "user-1", roles: [] } } } as never),
    );
    renderCustomerDetailLayout();
    expect(screen.getByTestId("can-merge")).toHaveTextContent("true");
  });
```

(add `cleanup` to its `@testing-library/react` import). In `apps/host/frontend/src/routes/customers/customers-list-route.test.tsx`: the `CustomersPage` mock takes `canMerge?: boolean` and renders `<span data-testid="can-merge">{String(Boolean(canMerge))}</span>`; add to its describe:

```tsx
  it("passes canMerge from customers:merge, since the list opens the same edit form", () => {
    withPermissions(["customers:update", "customers:delete"]);
    renderPage();
    expect(screen.getByTestId("can-merge")).toHaveTextContent("false");
    cleanup();

    withPermissions(["customers:merge"]);
    renderPage();
    expect(screen.getByTestId("can-merge")).toHaveTextContent("true");
  });
```

In `apps/host/frontend/src/catalogs/admin.test.ts`, add inside `describe("the admin permission catalog", …)`:

```ts
  // Hand-kept in step with apps/server/internal/customers/module.go, like the
  // lists above: customers:merge is the one key this delivery adds.
  it("names customers:merge in English and Norwegian, the English the server's own", () => {
    const translation = hostPermissionTranslationKeys["customers:merge"];
    expect(translation, "no catalog entry for customers:merge").toBeDefined();
    for (const lng of ["en", "nb"] as const) {
      const catalog = adminCatalog[lng] as Record<string, string>;
      expect(catalog[translation.displayNameKey], `display name (${lng})`).toBeTruthy();
      expect(catalog[translation.descriptionKey], `description (${lng})`).toBeTruthy();
    }
    const en = adminCatalog.en as Record<string, string>;
    expect(en[translation.displayNameKey]).toBe("Merge customers");
    expect(en[translation.descriptionKey]).toBe(
      "Merge a duplicate customer into another, moving its contacts, addresses, timeline, tags and other modules' references, and archiving it.",
    );
  });
```

- [ ] **Step 2: Run them and watch them fail**

```bash
cd /home/anders/projects/vantigo/vantigo
mise exec -- bun run --cwd apps/customers/frontend test
mise exec -- bun run --cwd apps/host/frontend test
```
Expected: FAIL — `./merge`, `./customer-picker` and `./-customer-merge-modal` do not resolve; the customers api tests fail on `mergedInto: null` and on `includeArchived`; the header, body, form-modal and host tests fail on the missing Merge…, banner, card gates, both hints and `can-merge` (`false` where `true` is wanted); the admin catalog test fails on the missing `customers:merge` entry.

- [ ] **Step 3: The api**

In `apps/customers/frontend/src/api/customers.ts`:

- After the `CustomerGroupRef` interface add:

```ts
/**
 * The customer this one was merged into (customers merge design D3), named so
 * a page can link there. Null unless the customer was merged away — the wire
 * omits the field entirely otherwise.
 */
export interface CustomerMergedInto {
  id: number;
  customerNumber: number;
  name: string;
}
```

- In `CustomerResponse`, after `tags: CustomerTag[];` add:

```ts
  /** Merge design D3. Null unless the customer was merged away; the wire omits the field then. */
  mergedInto: CustomerMergedInto | null;
```

- In `CustomersQueryParams`, after `groupId?: string;` add:

```ts
  /**
   * Archived customers as well as the rest — the list endpoint's own
   * `includeArchived`. The merge picker's search sets it: an archived duplicate
   * is the usual thing to absorb (merge design D2).
   */
  includeArchived?: boolean;
```

- In `customersSearchParams`, after the `groupId` line add `  if (params.includeArchived) searchParams.set("includeArchived", "true");`.
- `type RawCustomerResponse = Omit<CustomerResponse, "contactInfo" | "owner" | "group" | "tags"> & {` becomes `export type RawCustomerResponse = Omit<CustomerResponse, "contactInfo" | "owner" | "group" | "tags" | "mergedInto"> & {`, and its object type gains `mergedInto?: CustomerMergedInto | null;` after `group?: CustomerGroupRef | null;`.
- In `normalizeCustomer`, destructure `mergedInto` beside `group` (`({ contactInfo, owner, group, mergedInto, tags, ...rest }: RawCustomerResponse)`), add `mergedInto: mergedInto ?? null,` after `group: group ?? null,`, and change the comment's `The four normalised fields` to `The five normalised fields`.

Create `apps/customers/frontend/src/api/merge.ts`:

```ts
import { type CustomerResponse, normalizeCustomer, type RawCustomerResponse } from "./customers";
import { request } from "./request";

/** One kind of reference a merge moved to the surviving customer, and how many (merge design D3). */
export interface CustomerMergeMove {
  kind: string;
  count: number;
}

/** What `POST /customers/{id}/merge` answers: the survivor as it is now, and every kind that moved to it. */
export interface CustomerMergeResult {
  customer: CustomerResponse;
  moved: CustomerMergeMove[];
}

export interface CustomerMergeInput {
  /** The customer to absorb into the one merged into. */
  sourceId: number;
  /** The surviving customer's revision, so a merge from a stale page is refused (design D5's rule). */
  revision?: number;
}

/**
 * The customer `id` absorbs `input.sourceId` (merge design D2). A refusal is an
 * `ApiConflictError` whose `code` says which — `merge_self`,
 * `merge_type_mismatch`, `merge_into_archived`, `merge_already_merged` — or
 * that has none, for a stale revision.
 */
export const mergeCustomer = async (id: number, input: CustomerMergeInput): Promise<CustomerMergeResult> => {
  const answered = await request<{ customer: RawCustomerResponse; moved: CustomerMergeMove[] }>(
    `/api/v1/customers/${id}/merge`,
    { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(input) },
  );
  return { customer: normalizeCustomer(answered.customer), moved: answered.moved };
};
```

- [ ] **Step 4: The picker**

Create `apps/customers/frontend/src/components/customer-picker.tsx`:

```tsx
import { Select } from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { useQuery } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { type CustomerResponse, customersQueryOptions } from "../api/customers";
import "../i18n";
import { SEARCH_DEBOUNCE_MS } from "../lib/search";

/** How many customers one search offers: the projects picker's twenty. */
const CUSTOMER_PICKER_PAGE_SIZE = 20;

/**
 * Picks another customer, searching this module's own list as the user types
 * (customers merge design D4). It is the projects package's `CustomerPicker`
 * shape, copied rather than imported — module frontends never import one
 * another (docs/module-boundaries.md rule 7) — with three differences the merge
 * needs:
 *
 *  - The value is the whole customer, not an id: the modal says what will
 *    happen to it, and checks its type, before anything is sent.
 *  - The search reaches archived customers (`includeArchived`): an archived
 *    duplicate is the usual thing to absorb.
 *  - `excludeId` (the customer being merged into) and every customer already
 *    merged away are left out — the server refuses both. They are dropped here,
 *    after the search, so a page of twenty can show nineteen.
 *
 * The chosen customer stays on the option list whatever the next search
 * returns, so its name never turns into a bare id; and Mantine's own filter is
 * replaced by one that keeps every option — the API has already searched, on
 * the customer number and more besides the name the label shows.
 */
export const CustomerPicker = ({
  label,
  placeholder,
  value,
  onChange,
  excludeId,
  disabled,
}: {
  label: string;
  placeholder: string;
  value: CustomerResponse | null;
  onChange: (customer: CustomerResponse | null) => void;
  excludeId: number;
  disabled?: boolean;
}) => {
  const { t } = useI18n("customers");
  const [search, setSearch] = useState("");
  const [debouncedSearch] = useDebouncedValue(search, SEARCH_DEBOUNCE_MS);
  const { data } = useQuery(
    customersQueryOptions({
      page: 1,
      pageSize: CUSTOMER_PICKER_PAGE_SIZE,
      search: debouncedSearch.trim() || undefined,
      includeArchived: true,
    }),
  );

  const offered = new Map<number, CustomerResponse>();
  for (const customer of data?.data ?? []) {
    if (customer.id !== excludeId && customer.mergedInto === null) offered.set(customer.id, customer);
  }
  if (value) offered.set(value.id, value);
  const optionLabel = (customer: CustomerResponse) =>
    customer.status === "archived"
      ? `#${customer.customerNumber} ${customer.name} · ${t("statusArchived")}`
      : `#${customer.customerNumber} ${customer.name}`;

  return (
    <Select
      label={label}
      placeholder={placeholder}
      disabled={disabled}
      searchable
      clearable
      filter={({ options }) => options}
      onSearchChange={setSearch}
      nothingFoundMessage={t("noCustomersFound")}
      data={[...offered.values()].map((customer) => ({ value: String(customer.id), label: optionLabel(customer) }))}
      value={value ? String(value.id) : null}
      onChange={(next) => onChange(next === null ? null : (offered.get(Number(next)) ?? null))}
    />
  );
};
```

- [ ] **Step 5: The modal**

Create `apps/customers/frontend/src/pages/-customer-merge-modal.tsx`:

```tsx
import { Alert, Button, Group, List, Modal, Stack, Text } from "@mantine/core";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { ApiConflictError, type CustomerResponse, type CustomerType, syncCustomerRevision } from "../api/customers";
import { type CustomerMergeMove, type CustomerMergeResult, mergeCustomer } from "../api/merge";
import { CustomerPicker } from "../components/customer-picker";
import "../i18n";

/** The words for each kind a merge can move (merge design D3); a kind missing here still shows, as `mergeMovedOther`. */
const moveKeys: Record<string, string> = {
  "customers.contacts": "mergeMovedContacts",
  "customers.addresses": "mergeMovedAddresses",
  "customers.timelineEntries": "mergeMovedTimelineEntries",
  "customers.tags": "mergeMovedTags",
  "projects.projects": "mergeMovedProjects",
  "energy.supplyPeriods": "mergeMovedSupplyPeriods",
  "communications.conversations": "mergeMovedConversations",
  "communications.conversationSuggestions": "mergeMovedConversationSuggestions",
  "communications.conversationCandidates": "mergeMovedConversationCandidates",
};

/** The server's refusals by code (merge design D2); anything else shows the server's own detail. */
const refusalKeys: Record<string, string> = {
  merge_self: "mergeRefusedSelf",
  merge_type_mismatch: "mergeRefusedTypeMismatch",
  merge_into_archived: "mergeRefusedIntoArchived",
  merge_already_merged: "mergeRefusedAlreadyMerged",
};

const typePhraseKey = (type: CustomerType) => (type === "person" ? "mergeTypePerson" : "mergeTypeBusiness");

/**
 * Merge… (customers merge design D4): `customer` absorbs the one picked here.
 * Everything the server would refuse that the page can already see — two
 * types, an archived survivor — is said before the button, and the button is
 * off; what only the server knows (the pick merged away meanwhile, a stale
 * revision) comes back as its 409 and is said in words. The survivor's
 * revision is the one the page holds; a 409 without a code is that revision
 * going stale, answered the way the header's other writes answer it.
 *
 * On success the answer's revision is synced before the broad invalidation —
 * the page refreshes, the timeline shows customer.merged — and the modal says
 * what moved, zeros left out.
 */
export const CustomerMergeModal = ({
  customer,
  opened,
  onClose,
}: {
  customer: CustomerResponse;
  opened: boolean;
  onClose: () => void;
}) => {
  const { t } = useI18n("customers");
  const queryClient = useQueryClient();
  const [source, setSource] = useState<CustomerResponse | null>(null);
  const [done, setDone] = useState<{ source: CustomerResponse; result: CustomerMergeResult } | null>(null);
  const [refusal, setRefusal] = useState<string | null>(null);
  const mutation = useMutation({
    mutationFn: (picked: CustomerResponse) =>
      mergeCustomer(customer.id, { sourceId: picked.id, revision: customer.revision }),
    onSuccess: (result, picked) => {
      syncCustomerRevision(queryClient, customer.id, result.customer.revision);
      queryClient.invalidateQueries({ queryKey: ["customers"] });
      setDone({ source: picked, result });
    },
    onError: (error) => {
      if (error instanceof ApiConflictError && !error.code) {
        queryClient.invalidateQueries({ queryKey: ["customers"] });
        setRefusal(t("customerChangedMessage"));
        return;
      }
      const key = error instanceof ApiConflictError && error.code ? refusalKeys[error.code] : undefined;
      setRefusal(key ? t(key) : error.message);
    },
  });
  // The component stays mounted while closed, so a pick, a refusal or a result
  // left behind would greet the next opening.
  const close = () => {
    setSource(null);
    setDone(null);
    setRefusal(null);
    mutation.reset();
    onClose();
  };
  const typeMismatch = source !== null && source.type !== customer.type;
  const intoArchived = customer.status === "archived";

  return (
    <Modal opened={opened} onClose={close} title={t("mergeCustomerTitle", { name: customer.name })} centered size="lg">
      {done ? (
        <Stack>
          <Text size="sm">{t("mergeDoneMessage", { number: done.source.customerNumber, name: done.source.name })}</Text>
          <MovedList moved={done.result.moved} />
          <Group justify="flex-end">
            <Button onClick={close}>{t("mergeClose")}</Button>
          </Group>
        </Stack>
      ) : (
        <Stack>
          <CustomerPicker
            label={t("mergeSourceLabel")}
            placeholder={t("mergeSourcePlaceholder")}
            excludeId={customer.id}
            value={source}
            onChange={(next) => {
              setSource(next);
              setRefusal(null);
            }}
            disabled={mutation.isPending}
          />
          {source && (
            <>
              <Text size="sm">{t("mergeWhatHappens", { number: source.customerNumber, name: source.name })}</Text>
              <Text size="sm" c="dimmed">
                {t("mergeKeepsOwnDetails")}
              </Text>
            </>
          )}
          {source && typeMismatch && (
            <Alert color="yellow">
              {t("mergeTypeMismatchWarning", {
                number: source.customerNumber,
                name: source.name,
                sourceType: t(typePhraseKey(source.type)),
                targetType: t(typePhraseKey(customer.type)),
              })}
            </Alert>
          )}
          {intoArchived && <Alert color="yellow">{t("mergeIntoArchivedWarning")}</Alert>}
          {refusal && (
            <Alert color="red" title={t("mergeCouldNotMerge")}>
              {refusal}
            </Alert>
          )}
          <Group justify="flex-end">
            <Button variant="default" onClick={close}>
              {t("cancel")}
            </Button>
            <Button
              color="red"
              disabled={source === null || typeMismatch || intoArchived}
              loading={mutation.isPending}
              onClick={() => source && mutation.mutate(source)}
            >
              {t("mergeConfirm")}
            </Button>
          </Group>
        </Stack>
      )}
    </Modal>
  );
};

/** What moved, in the answer's order, zeros left out — "It had nothing else to move." when all were. */
const MovedList = ({ moved }: { moved: CustomerMergeMove[] }) => {
  const { t } = useI18n("customers");
  const something = moved.filter((m) => m.count > 0);
  if (something.length === 0) {
    return (
      <Text size="sm" c="dimmed">
        {t("mergeMovedNothing")}
      </Text>
    );
  }
  return (
    <List size="sm">
      {something.map((m) => (
        <List.Item key={m.kind}>
          {moveKeys[m.kind] ? t(moveKeys[m.kind], { count: m.count }) : t("mergeMovedOther", { count: m.count, kind: m.kind })}
        </List.Item>
      ))}
    </List>
  );
};
```

- [ ] **Step 6: The header, the form, the list, the host and the strings**

In `apps/customers/frontend/src/pages/customers.$customerId.tsx`:

- Imports: add `Anchor` to the `@mantine/core` import, `IconArrowMerge` to the `@tabler/icons-react` import, `import { Link } from "@tanstack/react-router";`, and `import { CustomerMergeModal } from "./-customer-merge-modal";`.
- The header's doc comment gains, after its `canArchive`/`canRestore` sentence: `` `canMerge` is the host's `customers:merge` check (customers merge design D4): it puts **Merge…** beside the other actions, and is handed to the edit form for its duplicate-identity hint. A merged-away customer shows where it went instead of the archived banner, and none of the actions that would edit it. ``
- Props: `canMerge,` in the destructuring and `canMerge?: boolean;` in the type, after `canRestore`.
- After `const [modalState, setModalState] = useState<CustomerModalState | null>(null);` add `const [merging, setMerging] = useState(false);`, and after `const isArchived = customer.status === "archived";` add `const mergedInto = customer.mergedInto;`.
- The archived banner `{isArchived && ( <Alert … archivedBannerTitle …> … </Alert> )}` becomes:

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
      ) : (
        isArchived && (
          <Alert color="gray" icon={<IconArchive size={16} />} title={t("archivedBannerTitle")}>
            {t("archivedBannerMessage")}
          </Alert>
        )
      )}
```

- In the actions `Group`: wrap the **Edit customer** and **Change type** buttons in `{!mergedInto && ( <> … </> )}`; directly after them add

```tsx
              {canMerge && !mergedInto && (
                <Button
                  variant="subtle"
                  color="gray"
                  leftSection={<IconArrowMerge size={16} />}
                  onClick={() => setMerging(true)}
                >
                  {t("mergeCustomer")}
                </Button>
              )}
```

  and change Restore's condition `{canRestore && isArchived && (` to `{canRestore && isArchived && !mergedInto && (` (Archive's `!isArchived` already hides it on a merged-away customer).
- `<CustomerFormModal state={modalState} onClose={() => setModalState(null)} />` becomes `<CustomerFormModal state={modalState} onClose={() => setModalState(null)} canMerge={canMerge} />`, followed by `<CustomerMergeModal customer={customer} opened={merging} onClose={() => setMerging(false)} />`.

Still in `customers.$customerId.tsx`, `CustomerOverview` (controller ruling on the pre-flight review): directly after `const { data: customer } = useSuspenseQuery(customerQueryOptions(customerId));` add

```tsx
  // A merged-away customer is read-only on the page (customers merge design
  // D4): everything it had is on the survivor, and anything written here would
  // land on a customer nobody looks at. The API still accepts the writes — it is
  // an archived customer like any other — so the gate is here, on every card.
  const editable = !customer.mergedInto;
```

and hand every card its capability through it — the returned `Stack` becomes:

```tsx
    <Stack gap="lg">
      <CustomerRelationshipCard customerId={customerId} canEdit={canEdit && editable} />
      <CustomerContactCard
        customerId={customerId}
        canEdit={canEdit && editable}
        registryRecord={registryRecord ?? null}
      />
      {showRegistry && (
        <CustomerRegistryCard customerId={customerId} canManageIdentity={canManageIdentity && editable} />
      )}
      <CustomerBillingCard
        customerId={customerId}
        customer={customer}
        canManageBilling={canManageBilling && editable}
      />
      <CustomerContactsCard customerId={customerId} readOnly={!editable} />
      <CustomerTimeline customerId={customerId} canManageTimeline={canManageTimeline && editable} />
    </Stack>
```

and its doc comment gains, at the end: `` A merged-away customer (merge design D4) gets every capability as false, whatever the host passed — and the contacts card, which never took one, `readOnly`. ``

In `apps/customers/frontend/src/pages/-customer-contacts-card.tsx`: the signature becomes `export const CustomerContactsCard = ({ customerId, readOnly = false }: { customerId: number; /** A merged-away customer's card (merge design D4): the list, with nothing to add, edit or remove. */ readOnly?: boolean }) => {`; wrap the header's **Add contact** `<Button …>{t("addContact")}</Button>` in `{!readOnly && ( … )}`; and in each row's last cell wrap the `<Group gap={4} wrap="nowrap">…</Group>` holding the edit and remove `ActionIcon`s in `{!readOnly && ( … )}` (the cell stays, so the columns keep their width).

In `apps/customers/frontend/src/pages/-customer-form-modal.tsx`: the component's signature becomes

```tsx
export const CustomerFormModal = ({
  state,
  onClose,
  canMerge,
}: {
  state: CustomerModalState | null;
  onClose: () => void;
  /** `customers:merge`: a duplicate-identity conflict says a merge may be the fix (merge design D4). */
  canMerge?: boolean;
}) => {
```

and inside the duplicate alert, directly after the `<Stack gap={4}>…</Stack>` that lists the duplicates, add:

```tsx
                {canMerge && conflict.duplicates.length > 0 && (
                  // Both forms (merge design D4), in two wordings: the
                  // duplicates above are already links to where Merge… lives,
                  // and a customer being created does not exist yet, so on a
                  // create the line says to open the duplicate instead — or
                  // create anyway and merge there.
                  <Text size="xs" c="dimmed">
                    {t(isEdit ? "duplicateIdentityMergeHint" : "duplicateIdentityMergeHintCreate")}
                  </Text>
                )}
```

In `apps/customers/frontend/src/pages/customers.index.tsx`: `CustomersPage` takes `canMerge,` with a `canMerge?: boolean;` prop documented `/** customers:merge — the create and edit forms' duplicate-identity conflict suggests a merge (merge design D4). */` after `canImport`, and `<CustomerFormModal state={modalState} onClose={closeModal} />` becomes `<CustomerFormModal state={modalState} onClose={closeModal} canMerge={canMerge} />`.

In `apps/customers/frontend/src/i18n.ts`, append to `en`, after `importClose: "Close",`:

```ts
  mergeCustomer: "Merge…",
  mergeCustomerTitle: "Merge a duplicate into {{name}}",
  mergeSourceLabel: "Duplicate to merge into this customer",
  mergeSourcePlaceholder: "Search by name or number",
  mergeWhatHappens:
    "Everything on #{{number}} {{name}} — contacts, addresses, timeline, tags, and its projects, supply periods and conversations — moves here. Its own details stay readable in this customer's timeline, and it is archived.",
  mergeKeepsOwnDetails:
    "This customer keeps every detail of its own: name, legal identity, contact info, billing profile, owner and group.",
  mergeTypeBusiness: "a business",
  mergeTypePerson: "a private person",
  mergeTypeMismatchWarning:
    "#{{number}} {{name}} is {{sourceType}} and this customer is {{targetType}}. A merge never changes what a customer is: change one of their types first.",
  mergeIntoArchivedWarning: "This customer is archived. Restore it before merging another customer into it.",
  mergeConfirm: "Merge",
  mergeCouldNotMerge: "The customers could not be merged",
  mergeRefusedSelf: "A customer cannot be merged into itself.",
  mergeRefusedTypeMismatch: "The two customers are of different types. Change one of their types first.",
  mergeRefusedIntoArchived: "This customer is archived. Restore it before merging another customer into it.",
  mergeRefusedAlreadyMerged: "That customer has already been merged into another one.",
  mergeDoneMessage: "#{{number}} {{name}} was merged into this customer.",
  mergeMovedNothing: "It had nothing else to move.",
  mergeMovedContacts_one: "{{count}} contact",
  mergeMovedContacts_other: "{{count}} contacts",
  mergeMovedAddresses_one: "{{count}} address",
  mergeMovedAddresses_other: "{{count}} addresses",
  mergeMovedTimelineEntries_one: "{{count}} timeline entry",
  mergeMovedTimelineEntries_other: "{{count}} timeline entries",
  mergeMovedTags_one: "{{count}} tag",
  mergeMovedTags_other: "{{count}} tags",
  mergeMovedProjects_one: "{{count}} project",
  mergeMovedProjects_other: "{{count}} projects",
  mergeMovedSupplyPeriods_one: "{{count}} supply period",
  mergeMovedSupplyPeriods_other: "{{count}} supply periods",
  mergeMovedConversations_one: "{{count}} conversation",
  mergeMovedConversations_other: "{{count}} conversations",
  mergeMovedConversationSuggestions_one: "{{count}} conversation suggestion",
  mergeMovedConversationSuggestions_other: "{{count}} conversation suggestions",
  mergeMovedConversationCandidates_one: "{{count}} conversation candidate",
  mergeMovedConversationCandidates_other: "{{count}} conversation candidates",
  mergeMovedOther: "{{count}} × {{kind}}",
  mergeClose: "Close",
  mergedAwayBannerTitle: "Merged into #{{number}} {{name}}",
  mergedAwayBannerMessage:
    "Everything this customer had is on that customer now. What stays here is its own details, for the record.",
  mergedAwayOpenSurvivor: "Open #{{number}} {{name}}",
  duplicateIdentityMergeHint:
    "If one of these is the same customer, open it and use Merge… there to bring the two together.",
  duplicateIdentityMergeHintCreate:
    "If one of these is the same customer, open it instead — or create this one anyway and use Merge… there to bring the two together.",
```

and to `nb`, after `importClose: "Lukk",`:

```ts
  mergeCustomer: "Slå sammen…",
  mergeCustomerTitle: "Slå et duplikat sammen med {{name}}",
  mergeSourceLabel: "Duplikatet som skal slås sammen med denne kunden",
  mergeSourcePlaceholder: "Søk etter navn eller nummer",
  mergeWhatHappens:
    "Alt på #{{number}} {{name}} — kontakter, adresser, tidslinje, merkelapper, og prosjektene, leveranseperiodene og samtalene — flyttes hit. Opplysningene om den blir stående lesbare i denne kundens tidslinje, og den arkiveres.",
  mergeKeepsOwnDetails:
    "Denne kunden beholder alle sine egne opplysninger: navn, juridisk identitet, kontaktinformasjon, faktureringsprofil, ansvarlig og gruppe.",
  mergeTypeBusiness: "en bedrift",
  mergeTypePerson: "en privatperson",
  mergeTypeMismatchWarning:
    "#{{number}} {{name}} er {{sourceType}}, og denne kunden er {{targetType}}. En sammenslåing endrer aldri hva en kunde er: endre typen på en av dem først.",
  mergeIntoArchivedWarning: "Denne kunden er arkivert. Gjenopprett den før du slår en annen kunde sammen med den.",
  mergeConfirm: "Slå sammen",
  mergeCouldNotMerge: "Kundene kunne ikke slås sammen",
  mergeRefusedSelf: "En kunde kan ikke slås sammen med seg selv.",
  mergeRefusedTypeMismatch: "De to kundene er av ulik type. Endre typen på en av dem først.",
  mergeRefusedIntoArchived: "Denne kunden er arkivert. Gjenopprett den før du slår en annen kunde sammen med den.",
  mergeRefusedAlreadyMerged: "Den kunden er allerede slått sammen med en annen.",
  mergeDoneMessage: "#{{number}} {{name}} er slått sammen med denne kunden.",
  mergeMovedNothing: "Den hadde ikke noe annet som skulle flyttes.",
  mergeMovedContacts_one: "{{count}} kontakt",
  mergeMovedContacts_other: "{{count}} kontakter",
  mergeMovedAddresses_one: "{{count}} adresse",
  mergeMovedAddresses_other: "{{count}} adresser",
  mergeMovedTimelineEntries_one: "{{count}} tidslinjeoppføring",
  mergeMovedTimelineEntries_other: "{{count}} tidslinjeoppføringer",
  mergeMovedTags_one: "{{count}} merkelapp",
  mergeMovedTags_other: "{{count}} merkelapper",
  mergeMovedProjects_one: "{{count}} prosjekt",
  mergeMovedProjects_other: "{{count}} prosjekter",
  mergeMovedSupplyPeriods_one: "{{count}} leveranseperiode",
  mergeMovedSupplyPeriods_other: "{{count}} leveranseperioder",
  mergeMovedConversations_one: "{{count}} samtale",
  mergeMovedConversations_other: "{{count}} samtaler",
  mergeMovedConversationSuggestions_one: "{{count}} samtaleforslag",
  mergeMovedConversationSuggestions_other: "{{count}} samtaleforslag",
  mergeMovedConversationCandidates_one: "{{count}} kandidat på en samtale",
  mergeMovedConversationCandidates_other: "{{count}} kandidater på samtaler",
  mergeMovedOther: "{{count}} × {{kind}}",
  mergeClose: "Lukk",
  mergedAwayBannerTitle: "Slått sammen med #{{number}} {{name}}",
  mergedAwayBannerMessage:
    "Alt denne kunden hadde, ligger på den kunden nå. Det som står igjen her, er dens egne opplysninger, for ordens skyld.",
  mergedAwayOpenSurvivor: "Åpne #{{number}} {{name}}",
  duplicateIdentityMergeHint:
    "Er en av disse samme kunde, åpner du den og bruker Slå sammen… der for å samle de to.",
  duplicateIdentityMergeHintCreate:
    "Er en av disse samme kunde, åpner du den i stedet — eller oppretter denne likevel og bruker Slå sammen… der for å samle de to.",
```

In `apps/host/frontend/src/routes/customers/-customer-detail-layout.tsx`, after `canRestore={hasPermissions(permissions, ["customers:update"])}` add `canMerge={hasPermissions(permissions, ["customers:merge"])}`. In `apps/host/frontend/src/routes/customers/-customers-list.tsx`, after the `canImport=…` prop add `canMerge={hasPermissions(permissions, ["customers:merge"])}`, and extend its doc comment after the import sentence: `` `canMerge` (`customers:merge`) lets the create and edit forms' duplicate-identity conflict suggest a merge (customers merge design D4). ``

In `apps/host/frontend/src/catalogs/admin.ts`: after the `"customers:billing-manage": {…},` entry of `hostPermissionTranslationKeys` add

```ts
  "customers:merge": {
    moduleKey: "admin.permission.module.customers",
    categoryKey: "admin.permission.category.customers",
    displayNameKey: "admin.permission.customersMerge",
    descriptionKey: "admin.permission.customersMergeDescription",
  },
```

in `en`, after the two `customersBillingManage` lines:

```ts
  "admin.permission.customersMerge": "Merge customers",
  "admin.permission.customersMergeDescription":
    "Merge a duplicate customer into another, moving its contacts, addresses, timeline, tags and other modules' references, and archiving it.",
```

and in `nb`, after its two `customersBillingManage` lines:

```ts
  "admin.permission.customersMerge": "Slå sammen kunder",
  "admin.permission.customersMergeDescription":
    "Slå en duplikatkunde sammen med en annen: kontaktene, adressene, tidslinjen, merkelappene og andre modulers referanser flyttes, og duplikatet arkiveres.",
```

- [ ] **Step 7: Run everything, show the tests can fail, commit**

```bash
cd /home/anders/projects/vantigo/vantigo
F="apps/customers/frontend/src/api/customers.ts apps/customers/frontend/src/api/customers.test.ts \
 apps/customers/frontend/src/api/merge.ts apps/customers/frontend/src/api/merge.test.ts \
 apps/customers/frontend/src/components/customer-picker.tsx apps/customers/frontend/src/components/customer-picker.test.tsx \
 apps/customers/frontend/src/pages/-customer-merge-modal.tsx apps/customers/frontend/src/pages/-customer-merge-modal.test.tsx \
 apps/customers/frontend/src/pages/-customer-merge-header.test.tsx apps/customers/frontend/src/pages/customers.\$customerId.tsx \
 apps/customers/frontend/src/pages/-customer-form-modal.tsx apps/customers/frontend/src/pages/-customer-form-modal.test.tsx \
 apps/customers/frontend/src/pages/-customer-contacts-card.tsx \
 apps/customers/frontend/src/pages/customers.index.tsx apps/customers/frontend/src/i18n.ts \
 apps/host/frontend/src/routes/customers/-customer-detail-layout.tsx apps/host/frontend/src/routes/customers/customer-detail-route.test.tsx \
 apps/host/frontend/src/routes/customers/-customers-list.tsx apps/host/frontend/src/routes/customers/customers-list-route.test.tsx \
 apps/host/frontend/src/catalogs/admin.ts apps/host/frontend/src/catalogs/admin.test.ts"
mise exec -- bunx biome check --write $F
mise exec -- bun run --cwd apps/customers/frontend test && mise exec -- bun run --cwd apps/host/frontend test
mise exec -- bun run --cwd apps/customers/frontend typecheck && mise exec -- bun run --cwd apps/host/frontend typecheck
mise exec -- bun run --cwd apps/customers/frontend lint && mise exec -- bun run --cwd apps/host/frontend lint
mise exec -- bun run translations:check && mise exec -- bun run i18n:test
git status --short
```
Expected: PASS everywhere; `routeTree.gen.ts` does not move. The customers package's eslint rule 7 must not flag the picker — it imports nothing from `@vantigo/projects-ui`.

Prove each new test can fail, restoring after each: in the picker drop `customer.mergedInto === null` — the picker test goes red on `Acme Gammel AS`; drop `includeArchived: true` — it goes red on the URL; in `normalizeCustomer` drop `mergedInto: mergedInto ?? null,` — the api and merge tests go red on `mergedInto: undefined`; in the modal compute `typeMismatch` as `false` — the type test goes red on the enabled button; send `revision: undefined` from `mutationFn` — the first modal test goes red on the body; drop the `!mergedInto` around Edit and Change type — the header test goes red; drop `canMerge &&` from the hint — the edit hint test goes red on its second half; always use `"duplicateIdentityMergeHint"` — the create hint test goes red; drop `&& editable` from `CustomerTimeline`'s capability, and separately pass `readOnly={false}` to the contacts card — the body test goes red on "Add timeline event" and on "Add contact"; delete the `customers:merge` entry from `hostPermissionTranslationKeys` — the admin catalog test goes red; pass `["customers:delete"]` for `canMerge` in the host layout — the host test goes red. Say what each printed.

```bash
cd /home/anders/projects/vantigo/vantigo
cat > /tmp/claude-1000/msg-merge-6.txt <<'EOF'
feat(customers-ui): a customer page merges its duplicate in, and a merged one says where it went

The customer page header gains Merge…, for a caller the host says may
merge (canMerge, from customers:merge): a modal with this package's own
customer picker — the projects picker's shape over this module's list,
archived customers included, this customer and any merged away left
out — then a plain sentence of what will happen, a warning and no button
for two types or an archived survivor, and afterwards the counts of
what moved, with the page refreshed. A refusal is said in words. A
merged-away customer's page shows a banner linking to the customer that
absorbed it and no edit action anywhere — the header's, and every card's,
each capability gated on the customer not being merged away. The create
and edit forms' duplicate-identity conflict suggests Merge… on the
duplicate it already links to, for a caller who may merge; the list
passes canMerge too. The host's permission catalog names customers:merge.
en and nb.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
PATHS="$F"
git add $PATHS && git commit -F /tmp/claude-1000/msg-merge-6.txt -- $PATHS
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
mise exec -- golangci-lint run ./...   # CI's lint: depguard (no module imports another, the picker aside) and unused
taskset -c 0-3 mise exec -- go test -count=1 ./... 2>&1 | tail -40; echo "exit ${PIPESTATUS[0]}"
mise exec -- go generate ./... >/dev/null 2>&1; cd /home/anders/projects/vantigo/vantigo && git status --short   # clean but for go.mod/go.sum (and the plan file, if the controller has not committed it yet)
mise exec -- bun run gen:client && git status --short                                                         # still clean
mise exec -- bun run --cwd apps/customers/frontend test && mise exec -- bun run --cwd apps/host/frontend test
mise exec -- bun run --cwd apps/customers/frontend typecheck && mise exec -- bun run --cwd apps/host/frontend typecheck
mise exec -- bun run --cwd apps/customers/frontend lint && mise exec -- bun run --cwd apps/host/frontend lint
mise exec -- bun run translations:check && mise exec -- bun run i18n:test
mise exec -- bunx biome check apps/customers/frontend/src apps/host/frontend/src
cd apps/server && mise exec -- go run ./internal/openapi/cmd/contract coverage -corpus ../../openapi/testdata/exchanges -out ../../openapi/COVERAGE.md
cd /home/anders/projects/vantigo/vantigo && git status --short -- openapi/COVERAGE.md   # committed in Task 3: must print nothing
```
`taskset -c 0-3` because the race detector and 44 CPUs disagree about this database's connection limits; drop it if the suite is green without it. Run the two merge races once more on 4 CPUs, where the CI runner's timing lives: `taskset -c 0-3 mise exec -- go test -count=20 -run 'Merge.*Race' ./internal/customers/` — all twenty must pass. `main` may already be red for reasons that are not ours — if a failure is in a module this branch never touched, check it against `git log origin/main` and say so rather than fixing it here.

- [ ] **Step 2: Read the branch as a reviewer would**

```bash
cd /home/anders/projects/vantigo/vantigo
git log --oneline main..HEAD
git diff --stat main..HEAD
git diff main..HEAD -- openapi/customers.yaml
git diff main..HEAD -- apps/server/internal/module apps/server/internal/contracts
cd apps/server && mise exec -- go test -count=1 -run 'TestNoModuleReferencesAnotherModulesSchema|TestSqlcSchemaListsOnlyTheModulesOwnMigrations' ./internal/db/ && cd ../..   # no module's SQL names another schema; each sqlc.yaml lists only its own migrations
```
Check, by eye: the design and plan commits plus six task commits, each trailer `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>`; nothing under `openapi/testdata/exchanges/`; `go.mod`/`go.sum` still untracked; no existing `required:` list changed; one migration (`00029`); no customers file imports another module; no customers query names another schema; `apps/customers/frontend` imports nothing from `@vantigo/projects-ui`.

- [ ] **Step 3: Open the PR**

```bash
cd /home/anders/projects/vantigo/vantigo
git push -u origin feat/customers-merge
cat > /tmp/claude-1000/pr-merge.md <<'EOF'
## Merging duplicate customers (phase 6, delivery B)

One customer absorbs its duplicate. Decided in
`docs/superpowers/specs/2026-09-24-customers-merge-design.md` (D1–D5).

- **One transaction, one contract.** `contracts.CustomerReferenceHolder` is the one
  sanctioned cross-module write: `module.Module.CustomerReferences` is a
  many-provider slot like `Workers`, Compose collects every enabled module's holder,
  and the merge calls each inside its own transaction — own SQL, own schema, own
  package, no directory lookups. Holders: projects, energy, communications
  (candidates kept once where a conversation lists both). `docs/module-boundaries.md`
  gains rule 8.
- **`POST /customers/{id}/merge`** `{sourceId, revision?}` behind the new sensitive
  `customers:merge` (+ `customers:view`). Both rows locked in ascending id order,
  retried on deadlock; refusals 404, `merge_self`, `merge_type_mismatch`,
  `merge_into_archived`, `merge_already_merged`, stale revision. An archived
  duplicate may be absorbed.
- **What moves**: contacts (the survivor's association wins, roles unioned, one
  primary per role), addresses (a clashing primary demoted), timeline entries and
  revisions (payloads untouched), tags (unioned); the duplicate's registry record
  and Peppol answer are deleted. **What stays**: every field of the survivor's own
  row; the duplicate's values are in `customer.merged`'s payload. The duplicate is
  archived with `merged_into_customer_id` (migration `00029`) and
  `customer.merged_away`; `SafeCustomerResponse.mergedInto` and
  `CustomerEntry.MergedInto` say where it went.
- **Frontend**: Merge… on the customer page (`canMerge`), with this package's own
  picker, the sentence, the warnings and the counts; the merged-away banner; a Merge
  hint on the create and edit forms' duplicate-identity conflict; a merged-away
  customer's cards offer no edit action either. en + nb.

Contract: one operation, three new schemas, one optional field on
`SafeCustomerResponse`; the frozen corpus is untouched and still validates.

Decisions on the record for review: each of this module's counts is what the
duplicate had, every kind listed with zeros; the retried race is the contact
delete (an attach cannot cycle); a re-pointed project's revision advances and no
project timeline entry is written; the address cap does not refuse a merge; energy
has no module doc, so its paragraph is a row in customers.md; a merged-away
customer stays writable on the server while its page shows no edit action on any
card, with a 400 for a missing sourceId; the Merge hint shows on the create and edit
forms, with `canMerge` from the list page too; the picker filters in the browser; the
merge sentence names every holder module whether or not it is enabled; the integration test composes
customers and projects only.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
gh pr create --base main --head feat/customers-merge \
  --title "Merging duplicate customers (phase 6, delivery B)" \
  --body-file /tmp/claude-1000/pr-merge.md
```
`gh pr edit` is broken in this environment: to change the body afterwards use `gh api -X PATCH repos/:owner/:repo/pulls/<n> -F body=@/tmp/claude-1000/pr-merge.md` (`-F`, which reads the file; `-f` would send the literal string). Do not merge — the user does that.

- [ ] **Step 4: Report**

Say: the PR's number and URL; each test shown able to fail and what the mutation printed (including how many of the twenty `mergeWriteAttempts = 1` runs of the delete race went red); anything the generated code disagreed with this plan about (sqlc's and oapi-codegen's names in Tasks 2 and 3, whether `TestServeMuxConflictsArePinned` wanted a pin, whether a project code was refused in Task 4); and whether `main` was already red. Plus the places this branch decides what the spec left open, for the user's verdict:

- each of this module's counts is what the duplicate had (contacts and tags the survivor already had included, active timeline entries only), and every kind is listed with its zeros; the summary names the non-zero ones;
- the retried race the tests pin is the contact delete, not the attach the spec names — an attach takes the customer first, as a merge does, and cannot cycle with it;
- a re-pointed project's revision advances (an edit form holding the old customer gets the 409) and no project timeline entry is written;
- the 50-address cap guards a write, not a merge: a survivor may end past 50;
- `docs/energy.md` does not exist, so energy's paragraph is its row in customers.md's holder table rather than a new one-paragraph file;
- a merged-away customer can still be edited or restored through the API — no refusal beyond the spec's ladder — while the page hides those actions; a missing or non-positive `sourceId` is a 400;
- the Merge hint shows on the create and the edit form's duplicate-identity conflict (two wordings), only with `canMerge`, which the list page passes too; a merged-away customer's page shows no edit action on any card (controller rulings);
- `mergeWhatHappens` names projects, supply periods and conversations whether or not those modules are enabled — D4's words verbatim — while the counts afterwards list only enabled modules; unchanged unless the user wants it;
- the picker is the list endpoint with `includeArchived`, this customer and merged-away ones dropped in the browser;
- the integration test composes customers and projects only; energy and communications prove their holders in their own packages;
- the three holders landed as one task and one commit; this module's merge statements live in one new `queries/merge.sql`.
