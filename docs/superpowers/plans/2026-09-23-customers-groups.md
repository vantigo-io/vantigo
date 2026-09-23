# Customer groups (phase 4, delivery D) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** An installation can define **customer groups** — "Retail", "Key accounts", "Public sector" — that a customer belongs to at most one of, and that carry a **default payment term** every member inherits unless its own billing profile decides otherwise. The vocabulary is the tags' shape (unique on `lower(name)`, unpaged, `customerCount`, 409 `group_exists`, no timeline event on its own writes); the membership is the owner's shape (one nullable column on `customers.customers`, sharing the row's `revision`, written only through its own sub-resource, recorded as `customer.group_changed`). A group with members is never deleted: 409 `group_in_use`. The default resolves in the one place resolution lives, `resolveBillingProfile`, and Products phase 4 gets the seam it will need through `contracts.CustomerEntry.Group`.

**Architecture:** Migration `00027` adds `customers.customer_groups` (uuid PK, `name varchar(100)`, `default_payment_terms_days integer CHECK (0..365)`, `created_at`, `updated_at`) with a unique index on `lower(name)`, and `customers.customers.group_id uuid NULL REFERENCES customers.customer_groups(id) ON DELETE RESTRICT` with a partial index. The vocabulary's four operations live in `groups.go` and answer `CustomerGroupSummary`; membership lives in `group_membership.go` as `PUT /customers/{id}/group`, guarded by the row's revision exactly as the owner's PUT is, and `group?: CustomerGroupRef` reaches every `SafeCustomerResponse` through `customerDecoration`'s one batched query per response — no existing customer query grows a column. The list gains `groupId=<uuid>|none`, an equality (and a NULL test) in **both** `CountCustomers` and `ListCustomers`. `resolveBillingProfile` gains its first third resolution tier, and `GET`/`PUT .../billing-profile` gain `groupDefault?`, so a card can say "inherits 30 days from Retail" without re-deriving anything. The frontend adds a Group `Select` to the Relationship card, a Manage groups modal (the first vocabulary modal that also creates), a Group filter on the list, and one sentence under the billing card's payment-terms row.

**Tech Stack:** Go 1.27 (pgx, sqlc, goose, oapi-codegen strict server), PostgreSQL 18, React + Mantine 9 + TanStack Router/Query, vitest, bun, mise.

**Spec:** `docs/superpowers/specs/2026-09-23-customers-groups-design.md` (D1–D6 + "Out of scope" + "Testing"). Read it first; it is binding, and every decision below argues from it rather than past it. Research with file:line pointers into the code this plan mirrors: `.superpowers/sdd/2026-09-23-customers-groups/context-for-design.md`. The sibling deliveries whose shape this one copies are documented in `docs/customers.md` ("Owner and tags", "Billing profile", "`contracts.CustomerDirectory`") and were planned in `docs/superpowers/plans/2026-09-23-customers-owner-tags.md` and `2026-09-23-customers-follow-ups.md`.

## Global Constraints

- Branch `feat/customers-groups`. Never commit to `main`, never merge, never `--no-verify`.
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
- A customer belongs to at most ONE group: `customers.customers.group_id` (nullable, in-module FK `ON DELETE RESTRICT`), a column that SHARES the row's `revision` — the owner's shape (`owner.go`), never the tags' join-table shape. The vocabulary (`customers.customer_groups`) copies the tags' shape: unique on `lower(name)`, NFC-normalised, unpaged list with `customerCount`, 409 `group_exists`, no timeline event on vocabulary writes.
- A group with members is never deleted: 409 `group_in_use` (the FK's RESTRICT backs it). Membership writes record `customer.group_changed` with snapshotted names.
- The default payment term resolves in ONE place, `resolveBillingProfile` (`customers/directory.go`): own value, else the group's default, else nil — and the docs' resolution table says so. No other billing field inherits.
- Permissions: NO new key — vocabulary reads on `customers:view`, vocabulary writes and membership on `customers:update` (+`customers:view` for the membership PUT, as the owner's).
- Contract changes to EXISTING schemas stay additive/optional (`group?`, `groupDefault?`); new schemas may have required fields; the frozen corpus is untouched and must still validate.
- After any `queries/*.sql` or migration change: `mise exec -- go generate ./...` from `apps/server`; new migrations go in `sqlc.yaml` and `internal/db/schema_test.go`; new operations need `operationId`, `x-vantigo-access`, module-test coverage, a `KnownServeMuxConflicts` pin where they conflict, and a COVERAGE.md regeneration.
- Run `mise exec -- bunx biome check --write <files>` on touched frontend files before committing.

---

## File Structure

| File | Responsibility |
| --- | --- |
| `apps/server/internal/db/migrations/00027_customers_groups.sql` | the vocabulary table, the membership column and its FK, the two indexes |
| `apps/server/internal/customers/queries/groups.sql` | every group statement: the vocabulary's four, the member count, the decoration batch, the membership read and the guarded membership write |
| `apps/server/internal/customers/queries/customers.sql` | `groupId`'s two SQL parameters in both list WHEREs; the group columns `DirectoryCustomer(s)`/`DirectoryBillingProfile` need |
| `apps/server/internal/customers/groups.go` | the vocabulary's four handlers, its validator and its two conflicts |
| `apps/server/internal/customers/group_membership.go` | `PUT /customers/{id}/group` and the group half of `customerDecoration` |
| `apps/server/internal/customers/owner.go` | `customerDecoration` gains a third map and one batched query |
| `apps/server/internal/customers/values.go` | `validateGroupName`; `validatePaymentTermsDays` keyed by its field |
| `apps/server/internal/customers/timeline_events.go` | `groupSnapshot` and `recordCustomerGroupChanged` |
| `apps/server/internal/customers/customers.go` | the `groupId` filter's shape check and its resolution; `group` on `safeCustomerResponse` |
| `apps/server/internal/customers/directory.go` | `resolveBillingProfile`'s third tier; `CustomerEntry.Group` |
| `apps/server/internal/customers/billing_profile.go` | `groupDefault` on the profile GET **and** PUT |
| `apps/server/internal/contracts/directory.go` | `CustomerGroupEntry` and `CustomerEntry.Group` |
| `openapi/customers.yaml` | five schemas, five operations, `group?` on `SafeCustomerResponse`, `groupDefault?` on `CustomerBillingProfile`, the `groupId` query parameter |
| `apps/customers/frontend/src/api/groups.ts` | the vocabulary's CRUD and the membership write |
| `apps/customers/frontend/src/pages/-manage-groups-modal.tsx` | the vocabulary editor: create, rename/re-default, delete with its reason |
| `apps/customers/frontend/src/pages/-customer-relationship-card.tsx` | the Group `Select` beside the owner |
| `apps/customers/frontend/src/pages/customers.index.tsx` | the Group filter ↔ URL and the Manage groups button |
| `apps/customers/frontend/src/pages/-customer-billing-card.tsx` | the inherited-term sentence under the payment-terms row |
| `apps/host/frontend/src/routes/customers/index.tsx` | `groupId` validated into the URL, the vocabulary prefetched |
| `docs/customers.md`, `ROADMAP.md`, `openapi/COVERAGE.md` | the Groups section, the two tables, the permission note, the frontend bullets, the API list, phase 4 |

---

### Task 1: The table, the membership column and every statement they need (D1)

The schema first, and nothing on the wire: this task ends with the migration applied, `sqlc` generating a `store` package that knows groups, and every existing test still green. Two indexes, and each is a decision: the unique one on `lower(name)` is what makes `group_exists` race-proof rather than merely intended, and the partial one on `group_id` is the owner's own bet — `groupId=none` is a scan by design.

**Files:**
- Create: `apps/server/internal/db/migrations/00027_customers_groups.sql`, `apps/server/internal/customers/queries/groups.sql`
- Modify: `apps/server/internal/customers/sqlc.yaml` (the `schema:` list), `apps/server/internal/db/schema_test.go` (`TestCustomersBaseline_AppliesAndIsIdempotent`)
- Generated: `apps/server/internal/customers/store/groups.sql.go`, `store/models.go`
- Read first (do not change): `apps/server/internal/db/migrations/00024_customers_owner_tags.sql` (this migration's voice, and the partial-index reasoning it states), `apps/server/internal/customers/queries/tags.sql` (`ListCustomerTags`'s correlated count, `UpdateCustomerTagRow`'s one-statement count, `DeleteCustomerTag`'s `:execrows`), `apps/server/internal/customers/queries/customers.sql:140-160` (`UpdateCustomerOwner`, the guarded write this task copies)

**Interfaces:**
- Produces (SQL): `customers.customer_groups(id uuid PK, name varchar(100) NOT NULL, default_payment_terms_days integer CHECK (BETWEEN 0 AND 365), created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL)`; `UNIQUE INDEX ux_customers_groups_name_lower ON customers.customer_groups (lower(name))`; `customers.customers.group_id uuid REFERENCES customers.customer_groups (id) ON DELETE RESTRICT`; `INDEX ix_customers_group ON customers.customers (group_id) WHERE group_id IS NOT NULL`.
- Produces Go (generated): `store.ListCustomerGroups`, `InsertCustomerGroup`, `UpdateCustomerGroupRow`, `DeleteCustomerGroup` (`:execrows` → `int64`), `GetCustomerGroup`, `CountCustomerGroupMembers`, `CustomerGroupsForCustomers`, `CustomerGroupMembership`, `SetCustomerGroup`, each with its own `…Row`/`…Params` types. The `store.CustomersCustomerGroup` model is emitted too and nothing uses it: unlike the tags' `InsertCustomerTag`, whose `RETURNING` is the whole table and therefore reuses `CustomersTag`, every statement here returns a strict subset and gets a generated row type of its own.
- Consumes: nothing. No handler, no contract, no behaviour change.

- [ ] **Step 1: Write the failing schema test**

In `apps/server/internal/db/schema_test.go`, inside `TestCustomersBaseline_AppliesAndIsIdempotent`, add `"customer_groups"` to `wantTables` (line ~321, between `customer_contact_roles` and `customer_peppol_lookups` — the list is `ORDER BY table_name`):

```go
		"customer_contact_roles",
		"customer_groups",
		"customer_peppol_lookups",
```

Then, immediately after the `ux_customers_tags_name_lower` block (the one ending `want a UNIQUE index on lower(name)`), add:

```go
	// A group is a vocabulary word too (customer groups design D1), so its
	// uniqueness is the tag vocabulary's own: an expression index on
	// lower(name), read through pg_indexes because indexColumns cannot see an
	// expression. Same cast normalisation as the tags' index above.
	var groupNameIndexDef string
	if err := pool.QueryRow(ctx, `SELECT indexdef FROM pg_indexes
	                              WHERE schemaname = 'customers' AND indexname = 'ux_customers_groups_name_lower'`).Scan(&groupNameIndexDef); err != nil {
		t.Fatalf("read ux_customers_groups_name_lower definition: %v", err)
	}
	if !strings.Contains(groupNameIndexDef, "UNIQUE") || !strings.Contains(groupNameIndexDef, "lower((name)") {
		t.Errorf("ux_customers_groups_name_lower = %q, want a UNIQUE index on lower(name)", groupNameIndexDef)
	}

	// The membership's foreign key is RESTRICT, and that is the design rather
	// than a default (design D2): a group with members is never deleted, and
	// the database is what makes that true even for a writer that raced the
	// handler's own member count. 'r' is RESTRICT; 'a' (NO ACTION), 'c'
	// (CASCADE) and 'n' (SET NULL) would each be a different promise — SET NULL
	// in particular would silently change every member's effective payment term
	// with no record on any customer.
	var groupDeleteRule string
	if err := pool.QueryRow(ctx, `
		SELECT confdeltype FROM pg_constraint c
		JOIN pg_class t ON t.oid = c.conrelid
		JOIN pg_namespace n ON n.oid = t.relnamespace
		WHERE n.nspname = 'customers' AND t.relname = 'customers' AND c.contype = 'f'
		  AND c.conname = 'customers_group_id_fkey'`).Scan(&groupDeleteRule); err != nil {
		t.Fatalf("query the group foreign key: %v", err)
	}
	if groupDeleteRule != "r" {
		t.Errorf("customers.group_id delete rule = %q, want %q (ON DELETE RESTRICT)", groupDeleteRule, "r")
	}

	// ix_customers_group is PARTIAL, for migration 00024's own reason restated:
	// groupId=none is answered by IS NULL and never by an index lookup on a
	// value, so indexing the unassigned majority would be bytes spent on rows
	// this index can never serve. The predicate is the load-bearing half, and
	// indexColumns cannot see one.
	var groupIndexDef string
	if err := pool.QueryRow(ctx, `SELECT indexdef FROM pg_indexes
	                              WHERE schemaname = 'customers' AND indexname = 'ix_customers_group'`).Scan(&groupIndexDef); err != nil {
		t.Fatalf("read ix_customers_group definition: %v", err)
	}
	if strings.Contains(groupIndexDef, "UNIQUE") || !strings.Contains(groupIndexDef, "WHERE (group_id IS NOT NULL)") {
		t.Errorf("ix_customers_group = %q, want a non-unique PARTIAL index on group_id IS NOT NULL", groupIndexDef)
	}

	// The CHECK is on the column, not only in Go: 0-365 is the billing
	// profile's own payment-terms rule (validatePaymentTermsDays,
	// billing_values.go), and a default outside it would be inherited by every
	// member of the group. The handler's field error is the message a caller
	// reads; this is the floor under it.
	var groupTermsChecks int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM pg_constraint c
		JOIN pg_class t ON t.oid = c.conrelid
		JOIN pg_namespace n ON n.oid = t.relnamespace
		WHERE n.nspname = 'customers' AND t.relname = 'customer_groups' AND c.contype = 'c'
		  AND pg_get_constraintdef(c.oid) LIKE '%default_payment_terms_days%'`).Scan(&groupTermsChecks); err != nil {
		t.Fatalf("count the group payment-terms check: %v", err)
	}
	if groupTermsChecks != 1 {
		t.Errorf("default_payment_terms_days CHECK constraints = %d, want 1", groupTermsChecks)
	}
```

`applyUpDownUp(t, url, 3)` at the top of the test must NOT change: it runs **every** migration up, rolls back to `2` and runs up again, so `00027` and its `Down` are already exercised by this test as it stands.

- [ ] **Step 2: Run it and watch it fail**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'
mise exec -- go test -count=1 -run TestCustomersBaseline_AppliesAndIsIdempotent ./internal/db/
```
Expected: FAIL — `tables = [… customer_contact_roles customer_peppol_lookups …], want [… customer_contact_roles customer_groups customer_peppol_lookups …]` (a `Fatalf` on the length, so the four later assertions do not even run yet).

- [ ] **Step 3: Write the migration**

Create `apps/server/internal/db/migrations/00027_customers_groups.sql`:

```sql
-- +goose Up
-- Customer groups that carry defaults (customer groups design D1, D2) — phase
-- 4's fourth delivery, and the first thing in this module that decides a value
-- for a customer without storing it on the customer.
--
-- The vocabulary is the tag vocabulary's shape (00024), deliberately, down to
-- the unique index on lower(name): a group is a vocabulary word, so 'Retail'
-- and 'retail' are the same word and a second one is a mistake rather than a
-- variant — an installation holding both has a filter, and a default payment
-- term, that silently split its customers in two. Names are NFC-normalised in
-- Go (validateGroupName, values.go) before they are stored or compared, for
-- the same reason the comparison ignores case.
--
-- What it does NOT copy from tags: no colour and no description. A group is a
-- policy object — its name and its default are installation policy — not a
-- label, and nothing on it is decoration. created_at/updated_at are here and
-- not on customers.tags because this row IS edited over time in a way somebody
-- may have to account for later ("when did our Retail terms change?"); the
-- module answers that from these columns rather than from a timeline it
-- deliberately does not write (design D2).
--
-- default_payment_terms_days is NULL for "this group decides nothing", and
-- CHECKed 0-365 — the billing profile's own rule (validatePaymentTermsDays,
-- billing_values.go), in the database as well as in Go because a value outside
-- it would be inherited by every member of the group. The range is a business
-- rule that has never changed and is not a UI palette (which is why tags'
-- colour has no CHECK), so a CHECK costs nothing a future migration will
-- regret.
CREATE TABLE customers.customer_groups (
    id                         uuid         PRIMARY KEY,
    name                       varchar(100) NOT NULL,
    default_payment_terms_days integer      CHECK (default_payment_terms_days BETWEEN 0 AND 365),
    created_at                 timestamptz  NOT NULL,
    updated_at                 timestamptz  NOT NULL
);
CREATE UNIQUE INDEX ux_customers_groups_name_lower ON customers.customer_groups (lower(name));

-- The membership is the OWNER's shape, not the tags' (design D1): one nullable
-- column on the customer row, so it shares the row's revision and setting it
-- is an ordinary guarded write that a concurrent edit cannot lose. NULL is "in
-- no group", which is every customer until somebody says otherwise.
--
-- Unlike owner_user_id this column DOES have a foreign key, and the difference
-- is not inconsistency: owner_user_id points across a module boundary at
-- identity's users, which this module may not reference at all
-- (internal/db/schema_test.go bars the schema, depguard bars the import), while
-- customer_groups is this module's own table two lines up. ON DELETE RESTRICT
-- because design D2 rules that a group with members is not deleted: the
-- handler counts members and answers 409 group_in_use, and this is what makes
-- that true for a writer that raced the count. ON DELETE SET NULL would have
-- changed every member's effective payment term with no record on any customer
-- — the tags' cascade is right for a label and wrong for a default.
ALTER TABLE customers.customers
    ADD COLUMN group_id uuid REFERENCES customers.customer_groups (id) ON DELETE RESTRICT;

-- The list's groupId filter is an equality on this column, and the group
-- vocabulary list's customerCount is a count over it. Partial, and the
-- consequence is the owner column's own bet restated (00024): groupId=none is a
-- SCAN, because a partial index does not serve its own WHERE's complement.
-- "Customers in no group" is a tidying-up sweep, not a daily filter, and it
-- already shares that plan with every unfiltered page of this list. The day
-- ungrouped customers are the majority of a large installation and that sweep
-- is what people run all day, the answer is a second partial index on
-- (group_id IS NULL), not widening this one.
CREATE INDEX ix_customers_group ON customers.customers (group_id)
    WHERE group_id IS NOT NULL;

-- +goose Down
ALTER TABLE customers.customers DROP COLUMN group_id;
DROP TABLE customers.customer_groups;
```

- [ ] **Step 4: Register it with sqlc**

In `apps/server/internal/customers/sqlc.yaml`, append to the `schema:` list, after `00026_customers_timeline_follow_up.sql`:

```yaml
      - ../db/migrations/00027_customers_groups.sql
```

- [ ] **Step 5: Run the schema tests and watch them pass**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- go test -count=1 -run 'TestCustomersBaseline_AppliesAndIsIdempotent|TestSqlcSchemaListsOnlyTheModulesOwnMigrations|TestNoModuleReferencesAnotherModulesSchema' ./internal/db/
```
Expected: PASS. `applyUpDownUp` runs every migration up, down and up again, so the `Down` above is exercised here too. If the `ix_customers_group` assertion fails on the predicate's exact text, take what the failure prints (Postgres normalises predicates in `pg_indexes.indexdef`) rather than what this plan guessed.

- [ ] **Step 6: Write `queries/groups.sql`**

Create `apps/server/internal/customers/queries/groups.sql`:

```sql
-- name: ListCustomerGroups :many
-- ListCustomerGroups is GET /customers/groups (customer groups design D2):
-- the whole vocabulary, name-ascending, each with how many customers belong to
-- it. Unpaged, and the count travels with the list, both for the tag
-- vocabulary's own reasons (queries/tags.sql): the delete confirmation and the
-- group picker read the one list, a vocabulary is tens of rows rather than
-- thousands, and a correlated count per row over an indexed column costs
-- nothing worth a second round trip. id is the tie-break so two groups that
-- differ only in trailing punctuation still order deterministically.
SELECT g.id, g.name, g.default_payment_terms_days,
       (SELECT count(*) FROM customers.customers c WHERE c.group_id = g.id) AS customer_count
FROM customers.customer_groups g
ORDER BY g.name, g.id;

-- name: InsertCustomerGroup :one
-- InsertCustomerGroup is POST /customers/groups. The duplicate check is this
-- statement's own unique violation rather than a SELECT first: two callers
-- creating 'Retail' at the same moment is exactly the race a check-then-insert
-- loses, and ux_customers_groups_name_lower is the only thing that can decide
-- it. The handler matches that constraint BY NAME (db.IsUniqueViolation), never
-- "some unique violation", so a uuid collision on the primary key stays a 500
-- the caller must hear about.
INSERT INTO customers.customer_groups (id, name, default_payment_terms_days, created_at, updated_at)
VALUES (@id, @name, @default_payment_terms_days, @now::timestamptz, @now::timestamptz)
RETURNING id, name, default_payment_terms_days;

-- name: UpdateCustomerGroupRow :one
-- UpdateCustomerGroupRow is PUT /customers/groups/{groupId}: a FULL REPLACE of
-- both fields, so an omitted or null default_payment_terms_days CLEARS the
-- group's default rather than leaving the one it had — the request says what
-- the group is, not what changed (design D2). Named …Row rather than
-- UpdateCustomerGroup so the generated method does not read as "update a
-- customer's group", which is SetCustomerGroup below.
--
-- No timeline write anywhere near this statement: renaming a group, or moving
-- its default, records nothing on the customers that belong to it (design D2 —
-- the group is the vocabulary, not the customer). That a new default changes
-- every member's effective payment term at once is by design, and the docs say
-- so.
--
-- customer_count comes back from this same statement, for UpdateCustomerTagRow's
-- own reason (final fix wave M3): the body this endpoint answers is what the
-- Manage groups modal keeps on screen, so the count must be the one the list
-- would report, and reading it back separately left a window in which a group
-- deleted just after the update turned a successful write into a 404.
-- pgx.ErrNoRows means the group does not exist.
UPDATE customers.customer_groups
SET name = @name, default_payment_terms_days = @default_payment_terms_days, updated_at = @now::timestamptz
WHERE id = @id
RETURNING id, name, default_payment_terms_days,
          (SELECT count(*) FROM customers.customers c WHERE c.group_id = customers.customer_groups.id) AS customer_count;

-- name: CountCustomerGroupMembers :one
-- CountCustomerGroupMembers is what DELETE /customers/groups/{groupId} answers
-- 409 group_in_use with (design D2): the number goes into the problem detail,
-- so the refusal says what has to be moved rather than only that something
-- does. It is asked BEFORE the delete because the foreign key's RESTRICT
-- cannot say how many rows it protected — the violation names a constraint,
-- not a count.
SELECT count(*) FROM customers.customers WHERE group_id = @group_id;

-- name: DeleteCustomerGroup :execrows
-- DeleteCustomerGroup is DELETE /customers/groups/{groupId} for an EMPTY group.
-- Nothing cascades: the membership column's foreign key is RESTRICT, so a group
-- somebody moved a customer into between the count above and this statement
-- raises a foreign-key violation instead of quietly detaching its members. The
-- affected row count is how the handler tells 204 from 404.
DELETE FROM customers.customer_groups WHERE id = @id;

-- name: GetCustomerGroup :one
-- GetCustomerGroup resolves the one groupId PUT /customers/{id}/group was
-- given, so an unknown one is a field error on groupId rather than a
-- foreign-key violation surfacing as a 500 — the same job CustomerTagsByIDs
-- does for tags, in the singular because this body names exactly one group and
-- never a set. It also supplies the name the event's `after` snapshot stores.
-- pgx.ErrNoRows means no such group.
SELECT id, name, default_payment_terms_days FROM customers.customer_groups WHERE id = @id;

-- name: CustomerGroupsForCustomers :many
-- CustomerGroupsForCustomers is the group of a whole page of customers in ONE
-- query (design D3), never one query per row: the list answers 25 customers and
-- a per-row read would be 25 round trips for data that is on the wire either
-- way. The single-customer reads (GET /customers/{id} and every sub-resource
-- PUT that answers SafeCustomerResponse) call it with a one-element array
-- rather than having a query of their own, so there is one shape of
-- group-on-a-response and one place it can be wrong — exactly how
-- CustomerTagsForCustomers is used.
--
-- An INNER JOIN, not a LEFT one: a customer in no group contributes no row and
-- the decoration's map simply has no entry for it, which is what "absent when
-- none, never null" means on the wire.
SELECT c.id AS customer_id, g.id, g.name
FROM customers.customers c
JOIN customers.customer_groups g ON g.id = c.group_id
WHERE c.id = ANY(@customer_ids::int[])
ORDER BY c.id;

-- name: CustomerGroupMembership :one
-- CustomerGroupMembership is one customer's group, joined: the id, the name and
-- the default, all NULL when the customer belongs to no group. Two callers, and
-- they want the same three values for different reasons — PUT
-- /customers/{id}/group needs them for its no-op check (same group, nil-safe)
-- and for the event's `before` snapshot, and GET/PUT
-- /customers/{id}/billing-profile needs them for groupDefault (design D4). One
-- query rather than two so the two can never disagree about what a customer
-- inherits.
--
-- Why this exists at all, rather than group_id being selected by GetCustomer:
-- customerRowFrom (customers.go) is shared by five sqlc row types whose queries
-- do not select it, so adding the column there means a sixth positional
-- parameter at five call sites that have nothing to pass. This is one indexed
-- lookup on a primary key, and it keeps the membership out of a struct that
-- exists to make SafeCustomerResponse's projection know one shape.
--
-- pgx.ErrNoRows means the CUSTOMER does not exist (the outer FROM is
-- customers.customers), which is the 404 both callers answer.
--
-- group_id is read from the CUSTOMER's own column rather than from g.id, and
-- that is not a stylistic choice: c.group_id is nullable on the table, so sqlc
-- types it *uuid.UUID without having to infer anything from the outer join,
-- while g.id is NOT NULL on customer_groups and would depend on that inference
-- entirely. The two are the same value by the join's own condition.
SELECT c.group_id, g.name AS group_name, g.default_payment_terms_days
FROM customers.customers c
LEFT JOIN customers.customer_groups g ON g.id = c.group_id
WHERE c.id = @id;

-- name: SetCustomerGroup :one
-- SetCustomerGroup is PUT /customers/{id}/group's write (design D3): the
-- group_id column only — name, status, type, the legal identity, the contact
-- info, the owner and the billing profile are untouched, since this
-- sub-resource never writes them. Guarded and revision-bumping exactly like
-- UpdateCustomerOwner (queries/customers.sql): sqlc.narg(expected_revision) is
-- PutCustomerGroupRequest's optional revision, and the handler skips calling
-- this entirely when the group did not actually change, so a resubmit of the
-- current group writes nothing and bumps nothing (customers foundation design
-- D5's no-op rule).
--
-- @group_id is NULL to take the customer out of every group, which is a real
-- change like any other: it bumps the revision and records
-- customer.group_changed. The RETURNING list is UpdateCustomerOwner's, column
-- for column, because fromSetCustomerGroupRow feeds the same customerRow —
-- group_id itself is deliberately NOT among them: the response's `group` comes
-- from the decoration's batched query after the transaction commits, like the
-- tags', so this list stays the one shape customerRowFrom knows.
UPDATE customers.customers
SET group_id = @group_id,
    updated_at = @updated_at::timestamptz,
    revision = revision + 1
WHERE id = @id
  AND (sqlc.narg(expected_revision)::int IS NULL OR revision = sqlc.narg(expected_revision)::int)
RETURNING id, customer_number, name, status, legal_country, legal_id, legal_name, legal_source, legal_type,
          created_at, updated_at, type, revision, email, phone, website, owner_user_id;
```

- [ ] **Step 7: Generate, and read what was generated**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- go generate ./... && mise exec -- go generate ./... && git status --short
```
The second run must add no new diff. Then open `apps/server/internal/customers/store/groups.sql.go` and the changed part of `store/models.go` and check, before writing any Go against them:

- which queries took a bare argument and which a `…Params` struct;
- the exact field spellings (`DefaultPaymentTermsDays`, `CustomerCount`, `GroupID`, `GroupName`, `CustomerID`, `ExpectedRevision`, `UpdatedAt`, `Now`);
- the integer widths (`CustomerCount` is an `int64` from `count(*)`; `DefaultPaymentTermsDays` should be `*int32`).

**This step is a gate, not a note.** `group_id` and `default_payment_terms_days` are nullable columns, so sqlc types them `*uuid.UUID`/`*int32` with no inference needed (`emit_pointers_for_null_types: true` plus the nullable-uuid override in `sqlc.yaml`). `g.name` is different: it is `NOT NULL` on `customers.customer_groups`, and it is only nil-able here because the `LEFT JOIN` may find no row — which rests entirely on sqlc inferring outer-join nullability. Three later code blocks assume it did: Task 3's `deref(current.GroupName)` (`deref` takes `*string`, `server.go`), Task 4's `deref(row.GroupName)` inside `groupEntry`, and Task 4's `gen.CustomerGroupRef{… Name: deref(row.GroupName)}`.

**If `store.CustomerGroupMembershipRow.GroupName` is not `*string`, stop and fix the query before writing any Go against it** — and the same for `DirectoryCustomerRow.GroupName` in Task 4. The fix is to make the nullability explicit rather than inferred: `nullif(g.name, '')::text AS group_name` (sqlc types `nullif` as nullable), or a `LEFT JOIN LATERAL` the generator handles the same way. Do **not** work around it in Go by treating `""` as "no group": a group cannot be named `""` (`validateGroupName` refuses it), but a string that means two things is exactly what the nil pointer exists to avoid, and the `group_id` beside it is already the honest test. Say in the report which form the generated code needed.

Where the generated code disagrees with this plan's Go about anything else — a field's spelling, a `…Params` struct where the plan passes a bare argument — **follow the generated code** and adjust the call.

- [ ] **Step 8: Prove the package still builds and nothing else moved**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- go vet ./internal/customers/... && mise exec -- gofmt -l internal/customers internal/db
mise exec -- go test -count=1 ./internal/db/ ./internal/customers/
```
Expected: PASS, and `gofmt -l` silent. Nothing in `internal/customers` reads the new queries yet, so this is the "the schema landed and broke nothing" gate.

- [ ] **Step 9: Commit**

```bash
cd /home/anders/projects/vantigo/vantigo
cat > /tmp/claude-1000/msg-groups-1.txt <<'EOF'
feat(customers): customer groups get a table, a membership column and their queries

customers.customer_groups is the tag vocabulary's shape — unique on
lower(name), NFC-normalised in Go — with no colour and no description,
because a group is a policy object rather than a label, and with a CHECKed
0-365 default payment term. customers.group_id is the owner's shape: one
nullable column on the customer row, sharing its revision, with an in-module
foreign key whose RESTRICT is what makes "a group with members is never
deleted" true even for a writer that races the handler's member count.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
PATHS="apps/server/internal/db/migrations/00027_customers_groups.sql \
 apps/server/internal/customers/queries/groups.sql \
 apps/server/internal/customers/sqlc.yaml \
 apps/server/internal/db/schema_test.go \
 apps/server/internal/customers/store"
git add $PATHS && git commit -F /tmp/claude-1000/msg-groups-1.txt -- $PATHS
git show --stat HEAD && git status --short
```
One list for `add` and `commit` — `git commit -- <paths>` commits the working tree of those paths whatever was staged, so the two must agree or the `add` is decorative. `store` is a directory because `go generate` rewrites several files in it at once.

---

### Task 2: The vocabulary — four operations, two conflicts, no timeline event (D2)

`GET/POST /customers/groups` and `PUT/DELETE /customers/groups/{groupId}`, the tags' vocabulary with two differences that are both decisions: the second field is a **payment term** rather than a colour, so it is validated by the billing profile's own rule and keyed `defaultPaymentTermsDays`; and the delete **refuses** a group with members (409 `group_in_use`) instead of cascading, because a silent detach would change every member's effective payment term with no record on any customer.

**Files:**
- Create: `apps/server/internal/customers/groups.go`, `apps/server/internal/customers/groups_test.go`
- Modify: `openapi/customers.yaml`, `apps/server/internal/customers/values.go`, `apps/server/internal/customers/billing_values.go`, `apps/server/internal/customers/billing_values_test.go` (four call sites of the validator whose signature changes — the package does not build without them), `apps/server/internal/openapi/openapi_test.go` (`KnownServeMuxConflicts`)
- Generated: `apps/server/internal/openapi/specs/customers.yaml`, `apps/server/internal/customers/gen/api.gen.go`, `apps/customers/frontend/src/api-schema.d.ts` (and any other changed `api-schema.d.ts`), `openapi/COVERAGE.md`
- Read first (do not change): `apps/server/internal/customers/tags.go:49-263` (this file's model, statement for statement), `openapi/customers.yaml:615-660, 3441-3580` (the tag schemas and paths), `apps/server/internal/openapi/openapi_test.go:167-228` (why the conflict list exists and who resolves it)

**Interfaces:**
- Produces (contract): schemas `CustomerGroupRef` (`{id, name}`, both required), `CustomerGroupRequest` (`{name, defaultPaymentTermsDays?}`, `name` required), `CustomerGroupSummary` (`{id, name, defaultPaymentTermsDays?, customerCount}`, all but the default required); operations `getCustomersGroups` (`customers:view`), `postCustomersGroups`, `putCustomersGroupsByGroupId`, `deleteCustomersGroupsByGroupId` (all three `customers:update`).
- Produces Go: `func groupSummaryOf(id uuid.UUID, name string, defaultPaymentTermsDays *int32, customerCount int64) gen.CustomerGroupSummary`; `func groupExistsConflict() gen.CustomerConflictProblem`; `func groupInUseConflict(members int64) gen.CustomerConflictProblem`; `func validateGroupRequest(name string, defaultPaymentTermsDays *int32) (string, *int32, map[string][]string)`; `func validateGroupName(raw string) (string, string)`; the four handler methods on `*server`.
- Produces Go (changed signature): `func validatePaymentTermsDays(raw *int32, field string, errs map[string][]string) *int32` — **five** existing call sites, all passing `"paymentTermsDays"`: `validateBillingProfile` (`billing_values.go:269`) and four in `billing_values_test.go` (lines 176, 180, 189, 199).
- Consumes: `store.ListCustomerGroups`, `InsertCustomerGroup`, `UpdateCustomerGroupRow`, `CountCustomerGroupMembers`, `DeleteCustomerGroup` from Task 1; `db.IsUniqueViolation`, `db.IsForeignKeyViolation`.

- [ ] **Step 1: Write the failing tests**

Create `apps/server/internal/customers/groups_test.go`:

```go
package customers_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is the vocabulary half of phase 4 delivery D (customer groups
// design D2): GET/POST /customers/groups and PUT/DELETE
// /customers/groups/{groupId}.
//
// It is the tag vocabulary's own test file (tags_test.go) with two deliberate
// differences, and each has a test here that would pass against the tags'
// version and must not: the second field is a PAYMENT TERM, so it is validated
// 0-365 and a PUT that omits it CLEARS the group's default; and a delete
// REFUSES a group with members (409 group_in_use) rather than detaching them,
// because a silent detach would change every member's effective payment term
// with no record on any customer.

// setCustomerGroup puts a customer in a group (or takes it out, with a nil
// groupID) through the column migration 00027 added. The vocabulary's own
// delete rule needs members before it can refuse a delete, and PUT
// /customers/{id}/group is the NEXT task's endpoint — calling it here would be
// a request no operation in customers.yaml matches, which the package's own
// contract recorder reports as an error on top of the 404. This is the fixture
// shortcut harness_test.go's setDisplayName/disableUser already take for
// identity's own columns.
// groupID is a group's id as the API answered it (a string) or nil to take the
// customer out of every group; the ::uuid cast is what lets an untyped nil and a
// string both reach a uuid column through the same statement.
func setCustomerGroup(t *testing.T, h *modtest.Harness, customerID int32, groupID any) {
	t.Helper()
	h.Exec(t, `UPDATE customers.customers SET group_id = $2::uuid WHERE id = $1`, customerID, groupID)
}

// groupSummaryJSON decodes CustomerGroupSummary.
type groupSummaryJSON struct {
	Id                      string `json:"id"`
	Name                    string `json:"name"`
	DefaultPaymentTermsDays *int32 `json:"defaultPaymentTermsDays"`
	CustomerCount           int32  `json:"customerCount"`
}

// listGroups GETs /customers/groups and decodes a 200.
func listGroups(t *testing.T, c *modtest.Client) []groupSummaryJSON {
	t.Helper()
	r := c.Do(http.MethodGet, "/api/v1/customers/groups", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("GET /api/v1/customers/groups: status %d body %s, want 200", r.Status, r.Body)
	}
	var groups []groupSummaryJSON
	r.JSON(&groups)
	return groups
}

// createGroup POSTs a group and fails the test on anything but 201.
func createGroup(t *testing.T, c *modtest.Client, body map[string]any) groupSummaryJSON {
	t.Helper()
	r := c.Do(http.MethodPost, "/api/v1/customers/groups", body)
	if r.Status != http.StatusCreated {
		t.Fatalf("POST /api/v1/customers/groups %v: status %d body %s, want 201", body, r.Status, r.Body)
	}
	var group groupSummaryJSON
	r.JSON(&group)
	return group
}

// TestCustomerGroups_CreateListUpdateDelete walks the vocabulary's whole life
// in one test, because each step asserts the state the previous one left: the
// list is name-ascending with a member count, a PUT is a full replace that
// keeps the id and the count, and a delete of an empty group is a 204 that is
// not idempotent.
func TestCustomerGroups_CreateListUpdateDelete(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	retail := createGroup(t, c, map[string]any{"name": "Retail", "defaultPaymentTermsDays": 30})
	key := createGroup(t, c, map[string]any{"name": "Key accounts"})
	if retail.CustomerCount != 0 {
		t.Errorf("a fresh group's customerCount = %d, want 0", retail.CustomerCount)
	}
	if retail.DefaultPaymentTermsDays == nil || *retail.DefaultPaymentTermsDays != 30 {
		t.Errorf("Retail's defaultPaymentTermsDays = %v, want 30", retail.DefaultPaymentTermsDays)
	}
	if key.DefaultPaymentTermsDays != nil {
		t.Errorf("a group created without a default has defaultPaymentTermsDays = %v, want it absent", key.DefaultPaymentTermsDays)
	}

	groups := listGroups(t, c)
	if len(groups) != 2 || groups[0].Name != "Key accounts" || groups[1].Name != "Retail" {
		t.Fatalf("groups = %+v, want Key accounts then Retail (name-ascending)", groups)
	}

	// A full replace of both fields: the new name AND a default that is gone
	// because the body did not name one (design D2 — the request says what the
	// group is, not what changed).
	r := c.Do(http.MethodPut, "/api/v1/customers/groups/"+retail.Id, map[string]any{"name": "Retail chains"})
	if r.Status != http.StatusOK {
		t.Fatalf("update: status %d body %s, want 200", r.Status, r.Body)
	}
	var updated groupSummaryJSON
	r.JSON(&updated)
	if updated.Id != retail.Id || updated.Name != "Retail chains" {
		t.Errorf("updated = %+v, want the same id and the new name", updated)
	}
	if updated.DefaultPaymentTermsDays != nil {
		t.Errorf("updated.defaultPaymentTermsDays = %v, want it cleared: the PUT is a full replace",
			updated.DefaultPaymentTermsDays)
	}

	if r := c.Do(http.MethodDelete, "/api/v1/customers/groups/"+key.Id, nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete an empty group: status %d body %s, want 204", r.Status, r.Body)
	}
	if r := c.Do(http.MethodDelete, "/api/v1/customers/groups/"+key.Id, nil); r.Status != http.StatusNotFound {
		t.Errorf("deleting it again: status %d body %s, want 404", r.Status, r.Body)
	}
	if r := c.Do(http.MethodPut, "/api/v1/customers/groups/"+uuid.NewString(), map[string]any{"name": "Nobody"}); r.Status != http.StatusNotFound {
		t.Errorf("updating an unknown group: status %d body %s, want 404", r.Status, r.Body)
	}
}

// TestCustomerGroups_NameIsUniqueIgnoringCaseAndNormalisation is the one thing
// a group vocabulary must not get wrong: 'Retail' and 'retail' are the same
// word, and so are a composed and a decomposed 'Café'. Both a create and an
// update answer 409 group_exists, and renaming a group to the name it already
// has is a plain 200 rather than a conflict with itself.
func TestCustomerGroups_NameIsUniqueIgnoringCaseAndNormalisation(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	retail := createGroup(t, c, map[string]any{"name": "Retail"})
	other := createGroup(t, c, map[string]any{"name": "Café"}) // composed é

	r := c.Do(http.MethodPost, "/api/v1/customers/groups", map[string]any{"name": "retail"})
	if r.Status != http.StatusConflict {
		t.Fatalf("create 'retail': status %d body %s, want 409", r.Status, r.Body)
	}
	var problem conflictProblemJSON
	r.JSON(&problem)
	if problem.Code == nil || *problem.Code != "group_exists" {
		t.Errorf("code = %v, want group_exists", problem.Code)
	}

	// Café is the same word decomposed: NFC-normalised before it is
	// compared, so the unique index on lower(name) sees the name it already
	// holds (values.go's validateGroupName, the tags' own rule).
	if r := c.Do(http.MethodPost, "/api/v1/customers/groups", map[string]any{"name": "Café"}); r.Status != http.StatusConflict {
		t.Errorf("create a decomposed 'Café': status %d body %s, want 409", r.Status, r.Body)
	}
	if r := c.Do(http.MethodPut, "/api/v1/customers/groups/"+other.Id, map[string]any{"name": "RETAIL"}); r.Status != http.StatusConflict {
		t.Errorf("rename to 'RETAIL': status %d body %s, want 409", r.Status, r.Body)
	}
	if r := c.Do(http.MethodPut, "/api/v1/customers/groups/"+retail.Id, map[string]any{"name": "Retail"}); r.Status != http.StatusOK {
		t.Errorf("renaming a group to its own name: status %d body %s, want 200", r.Status, r.Body)
	}
}

// TestCustomerGroups_DefaultPaymentTermsIsValidated pins the field the billing
// profile's own rule validates (design D2): 0-365 inclusive, keyed
// defaultPaymentTermsDays rather than paymentTermsDays, and 0 is a legitimate
// value ("due on receipt") rather than a missing one.
func TestCustomerGroups_DefaultPaymentTermsIsValidated(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	for _, days := range []int{-1, 366} {
		r := c.Do(http.MethodPost, "/api/v1/customers/groups", map[string]any{
			"name": fmt.Sprintf("Group %d", days), "defaultPaymentTermsDays": days,
		})
		if r.Status != http.StatusBadRequest {
			t.Fatalf("defaultPaymentTermsDays %d: status %d body %s, want 400", days, r.Status, r.Body)
		}
		var problem validationProblemJSON
		r.JSON(&problem)
		want := fmt.Sprintf("Payment terms must be between 0 and 365 days, but was %d", days)
		if msgs := problem.Errors["defaultPaymentTermsDays"]; len(msgs) != 1 || msgs[0] != want {
			t.Errorf("errors[defaultPaymentTermsDays] = %v, want [%q]", msgs, want)
		}
		if _, ok := problem.Errors["paymentTermsDays"]; ok {
			t.Errorf("errors = %v, want no key \"paymentTermsDays\": this request's field is defaultPaymentTermsDays", problem.Errors)
		}
	}

	onReceipt := createGroup(t, c, map[string]any{"name": "Cash", "defaultPaymentTermsDays": 0})
	if onReceipt.DefaultPaymentTermsDays == nil || *onReceipt.DefaultPaymentTermsDays != 0 {
		t.Errorf("defaultPaymentTermsDays = %v, want 0: due on receipt is a decision, not an absence",
			onReceipt.DefaultPaymentTermsDays)
	}

	// The name's own rule, the tags' word for word under its own noun.
	r := c.Do(http.MethodPost, "/api/v1/customers/groups", map[string]any{"name": "   "})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("a blank name: status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if msgs := problem.Errors["name"]; len(msgs) != 1 || msgs[0] != "A group name cannot be null or empty" {
		t.Errorf("errors[name] = %v, want the blank-name refusal", msgs)
	}
	long := c.Do(http.MethodPost, "/api/v1/customers/groups", map[string]any{"name": strings.Repeat("g", 101)})
	if long.Status != http.StatusBadRequest {
		t.Errorf("a 101-character name: status %d body %s, want 400", long.Status, long.Body)
	}
}

// TestCustomerGroups_DeleteRefusesAGroupInUse is the difference from tags that
// matters most (design D2): a tag's delete cascades because a label going away
// says nothing about the customer, and a group's must not, because every member
// would silently change what it inherits. The count is in the detail so the
// refusal says what has to be moved.
func TestCustomerGroups_DeleteRefusesAGroupInUse(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	retail := createGroup(t, c, map[string]any{"name": "Retail", "defaultPaymentTermsDays": 14})
	first := createCustomer(t, c, "Grouped Co")
	second := createCustomer(t, c, "Grouped Too AS")
	for _, id := range []int32{first.Id, second.Id} {
		setCustomerGroup(t, h, id, retail.Id)
	}

	r := c.Do(http.MethodDelete, "/api/v1/customers/groups/"+retail.Id, nil)
	if r.Status != http.StatusConflict {
		t.Fatalf("delete a group in use: status %d body %s, want 409", r.Status, r.Body)
	}
	var problem conflictProblemJSON
	r.JSON(&problem)
	if problem.Code == nil || *problem.Code != "group_in_use" {
		t.Fatalf("code = %v, want group_in_use", problem.Code)
	}
	if problem.Detail == nil || !strings.Contains(*problem.Detail, "2 customers") {
		t.Errorf("detail = %v, want it to name the two members", problem.Detail)
	}
	// Nothing was written: the group and both memberships stand.
	if n := h.Count(t, `SELECT count(*) FROM customers.customer_groups WHERE id = $1`, retail.Id); n != 1 {
		t.Errorf("group rows = %d after the refusal, want 1", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE group_id = $1`, retail.Id); n != 2 {
		t.Errorf("members = %d after the refusal, want 2", n)
	}

	// Moved out one at a time: the singular is worth its own assertion, since a
	// refusal that says "1 customers" is the kind of thing nobody notices until
	// a customer does.
	setCustomerGroup(t, h, second.Id, nil)
	r = c.Do(http.MethodDelete, "/api/v1/customers/groups/"+retail.Id, nil)
	r.JSON(&problem)
	if r.Status != http.StatusConflict || problem.Detail == nil || !strings.Contains(*problem.Detail, "1 customer. Move it") {
		t.Errorf("one member left: status %d detail %v, want 409 naming one customer", r.Status, problem.Detail)
	}
	setCustomerGroup(t, h, first.Id, nil)
	if r := c.Do(http.MethodDelete, "/api/v1/customers/groups/"+retail.Id, nil); r.Status != http.StatusNoContent {
		t.Errorf("delete the now-empty group: status %d body %s, want 204", r.Status, r.Body)
	}
}

// TestCustomerGroups_VocabularyWritesRecordNoTimelineEvent is design D2's other
// half: the group is the vocabulary, not the customer. Renaming a group — or
// moving its default, which changes what every member inherits — records
// nothing at all on a member's timeline, the tags' rule restated for a field
// that carries more weight than a colour.
//
// The membership is set through the column rather than through PUT
// /customers/{id}/group (setCustomerGroup above), which is what the next task
// adds. So this test says exactly what it checks and no more: a vocabulary
// write records NOTHING — not "nothing beyond the membership's own event",
// which is group_membership_test.go's to prove once that endpoint exists. The
// customer's timeline holds its one customer.created entry before and after.
func TestCustomerGroups_VocabularyWritesRecordNoTimelineEvent(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	retail := createGroup(t, c, map[string]any{"name": "Retail", "defaultPaymentTermsDays": 30})
	customer := createCustomer(t, c, "Quiet Co")
	setCustomerGroup(t, h, customer.Id, retail.Id)
	before := h.Count(t, `SELECT count(*) FROM customers.customers_timeline_entries WHERE customer_id = $1`, customer.Id)

	if r := c.Do(http.MethodPut, "/api/v1/customers/groups/"+retail.Id, map[string]any{
		"name": "Retail chains", "defaultPaymentTermsDays": 60,
	}); r.Status != http.StatusOK {
		t.Fatalf("rename and re-default: status %d body %s, want 200", r.Status, r.Body)
	}
	if n := countTimelineEvents(t, h, customer.Id, "customer.group_changed"); n != 0 {
		t.Errorf("customer.group_changed events = %d after a vocabulary write, want 0", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers_timeline_entries WHERE customer_id = $1`, customer.Id); n != before {
		t.Errorf("timeline entries = %d, want %d: a vocabulary write records nothing at all", n, before)
	}

	// And the delete of an empty group is as quiet: the group the customer is
	// in cannot be deleted at all (the test above), so this is a second group.
	spare := createGroup(t, c, map[string]any{"name": "Spare"})
	if r := c.Do(http.MethodDelete, "/api/v1/customers/groups/"+spare.Id, nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete the empty group: status %d body %s, want 204", r.Status, r.Body)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers_timeline_entries WHERE customer_id = $1`, customer.Id); n != before {
		t.Errorf("timeline entries = %d after a delete elsewhere in the vocabulary, want %d", n, before)
	}
}

// TestCustomerGroups_ReadingNeedsViewAndWritingNeedsUpdate pins the permission
// ruling (design D2): no new key. A caller with customers:view alone reads the
// vocabulary — a group's name and default are installation policy, and a
// member's INHERITED term is a policy fact rather than a negotiated one — and
// every write needs customers:update, exactly as the tag vocabulary's do.
func TestCustomerGroups_ReadingNeedsViewAndWritingNeedsUpdate(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	admin := authenticatedClient(t, h)
	group := createGroup(t, admin, map[string]any{"name": "Public sector", "defaultPaymentTermsDays": 45})

	reader, _ := h.SignInUser(t, "customers:view")
	if r := reader.Do(http.MethodGet, "/api/v1/customers/groups", nil); r.Status != http.StatusOK {
		t.Errorf("a view-only caller reading the vocabulary: status %d body %s, want 200", r.Status, r.Body)
	}
	for _, call := range []struct {
		method, path string
		body         map[string]any
	}{
		{http.MethodPost, "/api/v1/customers/groups", map[string]any{"name": "Sneaky"}},
		{http.MethodPut, "/api/v1/customers/groups/" + group.Id, map[string]any{"name": "Sneaky"}},
		{http.MethodDelete, "/api/v1/customers/groups/" + group.Id, nil},
	} {
		if r := reader.Do(call.method, call.path, call.body); r.Status != http.StatusForbidden {
			t.Errorf("%s %s as a view-only caller: status %d body %s, want 403", call.method, call.path, r.Status, r.Body)
		}
	}
}
```

Nothing in this file calls `PUT /customers/{id}/group`: that operation does not exist yet, and a request matching no operation in `customers.yaml` is reported by this package's own contract recorder (`main_test.go`'s `contracttest.NewForModule`) as an error on top of the 404. Every membership these tests need is set through `setCustomerGroup`'s `UPDATE`, above. **This task ends green.**

- [ ] **Step 2: Run them and watch them fail**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'
mise exec -- go test -count=1 -run TestCustomerGroups ./internal/customers/
```
Expected: the file **builds** — `conflictProblemJSON` (`duplicates_test.go`) and `validationProblemJSON` (`customers_test.go`) both already exist — and every case fails behaviourally: `POST /api/v1/customers/groups` answers `status 404`, and the recorder adds `contract: operation …: no operation matches` because `customers.yaml` has no groups yet. That is the right red.

- [ ] **Step 3: Add the three schemas and the two paths to the contract**

In `openapi/customers.yaml`, add the schemas in alphabetical position among `components.schemas` (after `CustomerContactRoleRequest`-ish neighbours — the generator re-sorts, so place them where `Group` sorts and let `go generate` normalise):

```yaml
        CustomerGroupRef:
            description: "The group a customer belongs to (customer groups design D3): its id and its name, and nothing else — a client that needs the group's default payment term reads it from GET /customers/groups or from the billing profile's own groupDefault, where it is already resolved against the customer."
            properties:
                id:
                    format: uuid
                    type: string
                name:
                    type: string
            required:
                - id
                - name
            type: object
        CustomerGroupRequest:
            description: "A customer group's name and default payment term (customer groups design D2). name is 1-100 characters, trimmed and NFC-normalised; a name another group already has, ignoring case, is a 409 with code group_exists. defaultPaymentTermsDays is 0-365 inclusive (the billing profile's own rule) and is the term every member inherits unless its own billing profile decides one. PUT is a FULL REPLACE of both fields: an omitted or null defaultPaymentTermsDays clears the group's default rather than leaving the one it had."
            properties:
                defaultPaymentTermsDays:
                    format: int32
                    nullable: true
                    type: integer
                name:
                    type: string
            required:
                - name
            type: object
        CustomerGroupSummary:
            description: "A group in the installation's vocabulary, with how many customers belong to it (customer groups design D2) — what the Manage groups modal needs in order to say why a delete is refused, on the same response that lists the groups. Every one of the vocabulary's three answering operations (list, create, update) answers this shape, so a client has one group type to hold."
            properties:
                customerCount:
                    format: int32
                    type: integer
                defaultPaymentTermsDays:
                    format: int32
                    nullable: true
                    type: integer
                id:
                    format: uuid
                    type: string
                name:
                    type: string
            required:
                - id
                - name
                - customerCount
            type: object
```

Then the two paths, between `/api/v1/customers/follow-ups` and `/api/v1/customers/lookup/brreg`:

```yaml
    /api/v1/customers/groups:
        get:
            operationId: getCustomersGroups
            responses:
                "200":
                    content:
                        application/json:
                            schema:
                                items:
                                    $ref: '#/components/schemas/CustomerGroupSummary'
                                type: array
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
            summary: List every customer group
            tags:
                - Customers
            x-vantigo-access: permission:customers:view
        post:
            operationId: postCustomersGroups
            requestBody:
                content:
                    application/json:
                        schema:
                            $ref: '#/components/schemas/CustomerGroupRequest'
                required: true
            responses:
                "201":
                    content:
                        application/json:
                            schema:
                                $ref: '#/components/schemas/CustomerGroupSummary'
                    description: Created
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
                "409":
                    content:
                        application/problem+json:
                            schema:
                                $ref: '#/components/schemas/CustomerConflictProblem'
                    description: Conflict — a group with this name already exists (code group_exists).
            summary: Create a customer group
            tags:
                - Customers
            x-vantigo-access: permission:customers:update
    /api/v1/customers/groups/{groupId}:
        delete:
            description: Deletes a group that no customer belongs to. A group with members is refused with 409 group_in_use and its member count in the detail: the members are moved first (the list's groupId filter finds them), because detaching them silently would change every one of their effective payment terms with no record on any customer.
            operationId: deleteCustomersGroupsByGroupId
            parameters:
                - in: path
                  name: groupId
                  required: true
                  schema:
                    format: uuid
                    type: string
            responses:
                "204":
                    description: No Content
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
                    description: Conflict — customers still belong to this group (code group_in_use).
            summary: Delete a customer group
            tags:
                - Customers
            x-vantigo-access: permission:customers:update
        put:
            operationId: putCustomersGroupsByGroupId
            parameters:
                - in: path
                  name: groupId
                  required: true
                  schema:
                    format: uuid
                    type: string
            requestBody:
                content:
                    application/json:
                        schema:
                            $ref: '#/components/schemas/CustomerGroupRequest'
                required: true
            responses:
                "200":
                    content:
                        application/json:
                            schema:
                                $ref: '#/components/schemas/CustomerGroupSummary'
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
                    description: Conflict — a group with this name already exists (code group_exists).
            summary: Rename a customer group or change its default payment term
            tags:
                - Customers
            x-vantigo-access: permission:customers:update
```

- [ ] **Step 4: Pin the new ServeMux conflicts**

`GET/POST /api/v1/customers/groups` needs **no** pin: a literal and a wildcard at the same depth (`/customers/{id}`) is precedence stdlib `ServeMux` resolves on its own — which is why `GET /api/v1/customers/tags` is not in the list either. `/customers/groups/{groupId}` is the ambiguous shape: a literal at segment 4 with a wildcard at 5, against `/customers/{id}/<literal>`. In `apps/server/internal/openapi/openapi_test.go`, add to `KnownServeMuxConflicts`, keeping the list's alphabetical order:

```go
	"DELETE /api/v1/customers/groups/{groupId} ⟷ DELETE /api/v1/customers/{id}/legal-identity",
```
(after the `contacts/{id}` line, before the `tags/{tagId}` one), and:

```go
	"PUT /api/v1/customers/groups/{groupId} ⟷ PUT /api/v1/customers/{id}/billing-profile",
	"PUT /api/v1/customers/groups/{groupId} ⟷ PUT /api/v1/customers/{id}/contact-info",
	"PUT /api/v1/customers/groups/{groupId} ⟷ PUT /api/v1/customers/{id}/legal-identity",
	"PUT /api/v1/customers/groups/{groupId} ⟷ PUT /api/v1/customers/{id}/owner",
	"PUT /api/v1/customers/groups/{groupId} ⟷ PUT /api/v1/customers/{id}/tags",
	"PUT /api/v1/customers/groups/{groupId} ⟷ PUT /api/v1/customers/{id}/type",
```
before the `PUT /api/v1/customers/tags/{tagId}` block. Task 3 adds `PUT /customers/{id}/group`, which is three more pairs (`contacts/{id}`, `groups/{groupId}`, `tags/{tagId}` against it) — **leave those to Task 3**, whose own operation creates them. `TestServeMuxConflictsArePinned` prints the exact `got`/`want` diff; if the set it computes differs from this list, take what it printed.

- [ ] **Step 5: Generate and check the contract gates**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- go generate ./... && mise exec -- go generate ./... && git status --short
mise exec -- go test -count=1 ./internal/openapi/...
cd /home/anders/projects/vantigo/vantigo && mise exec -- bun run gen:client
```
Expected: the second `go generate` adds no new diff; `internal/openapi` passes, `TestRecordedExchangesMatchTheContract` included (nothing existing changed, so the frozen corpus still validates). `openapi/COVERAGE.md` moves — this task's **four** new operations have no recorded .NET exchange, so customers' uncovered count goes 24 → 28 (Task 3's fifth takes it to 29) — so regenerate it:

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- go run ./internal/openapi/cmd/contract coverage -corpus ../../openapi/testdata/exchanges -out ../../openapi/COVERAGE.md
```

- [ ] **Step 6: The name rule and the keyed payment-terms rule**

In `apps/server/internal/customers/values.go`, immediately after `validateTagName`:

```go
// validateGroupName is the group name's rule, which is validateTagName's under
// its own noun (customer groups design D2): 1-100 characters after NFC
// normalisation, trimmed. Not shared with the tags' own function despite being
// the same three lines — the message names the thing being validated, and a
// parameterised noun for two callers would be a seam standing in for a word.
func validateGroupName(raw string) (string, string) {
	name := norm.NFC.String(raw)
	if strings.TrimSpace(name) == "" {
		return "", "A group name cannot be null or empty"
	}
	if n := utf16Length(name); n > 100 {
		return "", fmt.Sprintf("A group name cannot be longer than 100 characters, the given value was %d characters", n)
	}
	return strings.TrimSpace(name), ""
}
```

In `apps/server/internal/customers/billing_values.go`, give `validatePaymentTermsDays` the field name it keys its error under — the rule is shared by two request bodies that spell the field differently, and the rule is what design D2 says to share:

```go
// validatePaymentTermsDays is D4's payment-terms rule: 0-365 inclusive, absent
// left nil (the request never sends a "blank" integer the way a string field
// can be blank). The message is not the "but was '%s'" shape every string
// validator in this module uses — there is no raw string to quote, only the
// integer itself.
//
// field is the request's own name for the value, because the rule now has two
// callers with two spellings: the billing profile's own paymentTermsDays and a
// group's defaultPaymentTermsDays (customer groups design D2). One rule, one
// message, keyed under whichever field the caller actually sent — a second copy
// of "between 0 and 365" would be the thing that drifts the day the range moves.
func validatePaymentTermsDays(raw *int32, field string, errs map[string][]string) *int32 {
	if raw == nil {
		return nil
	}
	v := *raw
	if v < 0 || v > 365 {
		errs[field] = []string{fmt.Sprintf("Payment terms must be between 0 and 365 days, but was %d", v)}
		return nil
	}
	return &v
}
```
Then **every** call site, found first and fixed in one pass — the package does not build until all five pass a field name:

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- grep -rn 'validatePaymentTermsDays(' internal/
```
which names `billing_values.go:269` and `billing_values_test.go:176, 180, 189, 199`. In `validateBillingProfile`:

```go
		PaymentTermsDays: validatePaymentTermsDays(req.PaymentTermsDays, "paymentTermsDays", errs),
```
and in `billing_values_test.go`, all four calls take the same literal — the unit tests are about the range, not about the key, so `"paymentTermsDays"` keeps each of them asserting exactly what it asserted before:

```go
	got := validatePaymentTermsDays(&zero, "paymentTermsDays", errs)
```

- [ ] **Step 7: Write the four handlers**

Create `apps/server/internal/customers/groups.go`:

```go
package customers

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
	"github.com/vantigo-io/vantigo/server/internal/customers/store"
	"github.com/vantigo-io/vantigo/server/internal/db"
)

// This file is the vocabulary half of phase 4 delivery D (customer groups
// design D2): GET/POST /customers/groups and PUT/DELETE
// /customers/groups/{groupId}. The membership — PUT /customers/{id}/group and
// the group on every customer response — is group_membership.go.
//
// It is tags.go's vocabulary with two deliberate differences, and each is a
// decision rather than a variation:
//
//  1. **The second field is a payment term, not a colour.** It is validated by
//     the billing profile's own validatePaymentTermsDays (0-365,
//     billing_values.go) under this request's own field name, so one rule
//     covers a group's default and a customer's override and the two can never
//     disagree about what 366 days means. A CHECK on the column backs it
//     (migration 00027), because a default outside the range would be
//     inherited by every member.
//  2. **A group with members is never deleted.** The tags' cascade is right for
//     a label — a tag going away says nothing about the customer — and wrong
//     for a default: detaching the members would change every one of their
//     effective payment terms with no record on any customer. So DELETE counts
//     first and answers 409 group_in_use with that count, and the column's
//     foreign key is RESTRICT rather than SET NULL so a writer that raced the
//     count cannot get past it either.
//
// The vocabulary's own writes record no timeline event at all (design D2: the
// group is the vocabulary, not the customer), and that includes moving a
// group's default, which changes what every member inherits. That is by design
// and the docs say so — the alternative is an event on every member of a group,
// written by somebody who was editing a group rather than a customer.
//
// No new permission key: reads on customers:view, writes on customers:update
// (design D2). A group's name and default are installation policy, the same
// reasoning that put the tag vocabulary on customers:update, and a customer's
// own override stays where it is, behind customers:billing-manage.

// groupSummaryOf is CustomerGroupSummary's projection. customer_count arrives
// as a bigint from a count(*) sub-select and the contract's field is int32: a
// vocabulary with two billion members of one group is not a case worth a wider
// type, and the narrowing is here, once, rather than at each call site
// (tagSummaryOf's own reasoning).
func groupSummaryOf(id uuid.UUID, name string, defaultPaymentTermsDays *int32, customerCount int64) gen.CustomerGroupSummary {
	return gen.CustomerGroupSummary{
		Id: id, Name: name, DefaultPaymentTermsDays: defaultPaymentTermsDays, CustomerCount: int32(customerCount),
	}
}

// groupExistsConflict is the 409 both the create and the update answer for a
// name another group already holds, ignoring case. It carries code
// group_exists, the tags' tag_exists precedent, so a UI can say "you already
// have that group" and put the message under the one field the person typed in
// instead of showing a bare 409.
func groupExistsConflict() gen.CustomerConflictProblem {
	title := "Customer group already exists"
	detail := "A customer group with this name already exists. Group names are compared without regard to case."
	code := "group_exists"
	status := int32(http.StatusConflict)
	return gen.CustomerConflictProblem{Title: &title, Detail: &detail, Code: &code, Status: &status}
}

// groupInUseConflict is DELETE /customers/groups/{groupId}'s refusal for a
// group somebody still belongs to (design D2). The count is in the detail, not
// only in the code: "move them first" is only actionable if the caller learns
// how many there are, and the list's own groupId filter is where they are
// found. The singular is worth the branch — a refusal that says "1 customers"
// is the kind of thing nobody notices until a customer does.
func groupInUseConflict(members int64) gen.CustomerConflictProblem {
	title := "Customer group is in use"
	noun, pronoun := "customers", "them"
	if members == 1 {
		noun, pronoun = "customer", "it"
	}
	detail := fmt.Sprintf("This group still has %d %s. Move %s to another group, or out of every group, before deleting it.", members, noun, pronoun)
	code := "group_in_use"
	status := int32(http.StatusConflict)
	return gen.CustomerConflictProblem{Title: &title, Detail: &detail, Code: &code, Status: &status}
}

// customersGroupFK is the foreign key customers.customers.group_id carries
// (migration 00027, named by PostgreSQL's own convention). Named here because
// the delete matches it BY NAME rather than catching any 23503: this is the one
// violation that means "somebody moved a customer into the group after I
// counted", and any other foreign-key failure is a bug the caller must hear
// about as a 500.
const customersGroupFK = "customers_group_id_fkey"

// validateGroupRequest is POST/PUT /customers/groups' shared validator: both
// fields checked independently and both errors reported together, keyed by the
// request's own field names — the module's all-errors-at-once shape
// (validateTagRequest, validateBillingProfile). An absent default is nil, never
// an error; only one outside 0-365 is.
func validateGroupRequest(name string, defaultPaymentTermsDays *int32) (string, *int32, map[string][]string) {
	errs := map[string][]string{}
	normalizedName, nameErr := validateGroupName(name)
	if nameErr != "" {
		errs["name"] = []string{nameErr}
	}
	days := validatePaymentTermsDays(defaultPaymentTermsDays, "defaultPaymentTermsDays", errs)
	if len(errs) > 0 {
		return "", nil, errs
	}
	return normalizedName, days, nil
}

// GetCustomersGroups List every customer group
// (GET /api/v1/customers/groups)
//
// Unpaged, name-ascending, each group with its member count — the tag
// vocabulary's own bet, restated (design D2): tens of groups, not thousands,
// and both the picker and the Manage groups modal want the whole list. An
// installation that outgrows it gets paging as an additive contract change.
func (s *server) GetCustomersGroups(ctx context.Context, _ gen.GetCustomersGroupsRequestObject) (gen.GetCustomersGroupsResponseObject, error) {
	q := store.New(s.deps.Pool)
	rows, err := q.ListCustomerGroups(ctx)
	if err != nil {
		return nil, fmt.Errorf("customers: list customer groups: %w", err)
	}
	data := make([]gen.CustomerGroupSummary, 0, len(rows))
	for _, row := range rows {
		data = append(data, groupSummaryOf(row.ID, row.Name, row.DefaultPaymentTermsDays, row.CustomerCount))
	}
	return gen.GetCustomersGroups200JSONResponse(data), nil
}

// PostCustomersGroups Create a customer group
// (POST /api/v1/customers/groups)
//
// The duplicate check is the INSERT's own unique violation rather than a SELECT
// first (PostCustomersTags' own reasoning): two callers creating 'Retail' at
// the same moment is exactly the race a check-then-insert loses, and
// ux_customers_groups_name_lower is the only thing that can decide it.
// db.IsUniqueViolation names the constraint, so a uuid collision on the primary
// key stays a 500 rather than being reported as a duplicate name.
func (s *server) PostCustomersGroups(ctx context.Context, req gen.PostCustomersGroupsRequestObject) (gen.PostCustomersGroupsResponseObject, error) {
	body := gen.CustomerGroupRequest{}
	if req.Body != nil {
		body = *req.Body
	}
	name, days, errs := validateGroupRequest(body.Name, body.DefaultPaymentTermsDays)
	if errs != nil {
		return gen.PostCustomersGroups400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem("Invalid customer group", errs)), nil
	}

	q := store.New(s.deps.Pool)
	group, err := q.InsertCustomerGroup(ctx, store.InsertCustomerGroupParams{
		ID: uuid.New(), Name: name, DefaultPaymentTermsDays: days, Now: s.deps.Clock(),
	})
	if err != nil {
		if db.IsUniqueViolation(err, "ux_customers_groups_name_lower") {
			return gen.PostCustomersGroups409ApplicationProblemPlusJSONResponse(groupExistsConflict()), nil
		}
		return nil, fmt.Errorf("customers: create customer group: %w", err)
	}
	// A group nobody belongs to yet: the count is 0 by construction, so this
	// needs no second read.
	return gen.PostCustomersGroups201JSONResponse(groupSummaryOf(group.ID, group.Name, group.DefaultPaymentTermsDays, 0)), nil
}

// PutCustomersGroupsByGroupId Rename a customer group or change its default payment term
// (PUT /api/v1/customers/groups/{groupId})
//
// A FULL REPLACE of both fields (design D2): a body without
// defaultPaymentTermsDays CLEARS the group's default rather than leaving the one
// it had, so the request says what the group is rather than what changed —
// oapi-codegen's *int32 already collapses absent and null, which is why the
// validator never has to tell them apart.
//
// No timeline event anywhere, the vocabulary's rule, and that includes the
// default: changing it changes what every member inherits, at once, by design.
// Renaming a group to the name it already has is a plain 200, not a conflict
// with itself — the unique index compares lower(name) and the row being updated
// is the row being compared against, so the database agrees.
//
// One statement, UPDATE … RETURNING with the count (UpdateCustomerTagRow's own
// reason): the body this answers is what the Manage groups modal keeps on
// screen, so the count has to be the one the list would report, and reading it
// back separately left a window in which a group deleted just after the update
// turned a successful write into a 404.
func (s *server) PutCustomersGroupsByGroupId(ctx context.Context, req gen.PutCustomersGroupsByGroupIdRequestObject) (gen.PutCustomersGroupsByGroupIdResponseObject, error) {
	body := gen.CustomerGroupRequest{}
	if req.Body != nil {
		body = *req.Body
	}
	name, days, errs := validateGroupRequest(body.Name, body.DefaultPaymentTermsDays)
	if errs != nil {
		return gen.PutCustomersGroupsByGroupId400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem("Invalid customer group", errs)), nil
	}

	q := store.New(s.deps.Pool)
	updated, err := q.UpdateCustomerGroupRow(ctx, store.UpdateCustomerGroupRowParams{
		ID: req.GroupId, Name: name, DefaultPaymentTermsDays: days, Now: s.deps.Clock(),
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return gen.PutCustomersGroupsByGroupId404Response{}, nil
	case db.IsUniqueViolation(err, "ux_customers_groups_name_lower"):
		return gen.PutCustomersGroupsByGroupId409ApplicationProblemPlusJSONResponse(groupExistsConflict()), nil
	case err != nil:
		return nil, fmt.Errorf("customers: update customer group: %w", err)
	}
	return gen.PutCustomersGroupsByGroupId200JSONResponse(
		groupSummaryOf(updated.ID, updated.Name, updated.DefaultPaymentTermsDays, updated.CustomerCount)), nil
}

// DeleteCustomersGroupsByGroupId Delete a customer group
// (DELETE /api/v1/customers/groups/{groupId})
//
// Two statements, in this order and for this reason (design D2): the member
// count, so a refusal can say what has to be moved, then the delete. The FK's
// RESTRICT is not a second opinion but the backstop for the window between
// them — a customer moved into the group in that moment raises 23503 on
// customers_group_id_fkey, which is answered as the same 409 by counting again,
// exactly as PutCustomersByIdTags maps its own late foreign-key violation back
// to the field error the resolve would have given a moment later.
//
// Not idempotent — deleting an already-absent group is a 404, the tags' own
// asymmetry — because "it is gone" and "it was never there" are different
// answers to somebody who just clicked Delete twice. No transaction and no
// retry: nothing here shares a lock order with another write the way tags'
// cascade does with its set replace, so there is no deadlock to retry.
func (s *server) DeleteCustomersGroupsByGroupId(ctx context.Context, req gen.DeleteCustomersGroupsByGroupIdRequestObject) (gen.DeleteCustomersGroupsByGroupIdResponseObject, error) {
	q := store.New(s.deps.Pool)
	members, err := q.CountCustomerGroupMembers(ctx, req.GroupId)
	if err != nil {
		return nil, fmt.Errorf("customers: count customer group members: %w", err)
	}
	if members > 0 {
		return gen.DeleteCustomersGroupsByGroupId409ApplicationProblemPlusJSONResponse(groupInUseConflict(members)), nil
	}

	rows, err := q.DeleteCustomerGroup(ctx, req.GroupId)
	if db.IsForeignKeyViolation(err, customersGroupFK) {
		// Somebody moved a customer into the group between the count and this
		// statement. Count again and answer the refusal the count itself would
		// have given: the caller asked to delete a group that has members, which
		// is a fact about the group whichever side of the DELETE it became true
		// on.
		late, cerr := q.CountCustomerGroupMembers(ctx, req.GroupId)
		if cerr != nil {
			return nil, fmt.Errorf("customers: re-count customer group members after a foreign-key violation: %w", cerr)
		}
		if late > 0 {
			return gen.DeleteCustomersGroupsByGroupId409ApplicationProblemPlusJSONResponse(groupInUseConflict(late)), nil
		}
		// Empty again, so whoever was in it has since moved out and there is no
		// refusal to report. The delete did fail, and falling through to the 500
		// says so rather than inventing a count of zero.
	}
	if err != nil {
		return nil, fmt.Errorf("customers: delete customer group: %w", err)
	}
	if rows == 0 {
		return gen.DeleteCustomersGroupsByGroupId404Response{}, nil
	}
	return gen.DeleteCustomersGroupsByGroupId204Response{}, nil
}
```

- [ ] **Step 8: Run the tests and watch them pass**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- go test -count=1 -run TestCustomerGroups ./internal/customers/
```
Expected: PASS — every case, the in-use delete and the no-event one included, because both set their memberships through `setCustomerGroup`'s `UPDATE` rather than through an endpoint that does not exist yet. **This task ends green**, and Step 9 below runs the whole package to prove nothing else moved.

- [ ] **Step 9: Show a test can fail, then commit**

Prove `TestCustomerGroups_NameIsUniqueIgnoringCaseAndNormalisation` and `TestCustomerGroups_DefaultPaymentTermsIsValidated` can fail: change `validateGroupName` to skip `norm.NFC.String` (the decomposed 'Café' case goes red), restore it by hand; change `validatePaymentTermsDays`'s upper bound to `366` (the 366 case goes red), restore it by hand. Say in the report what each printed.

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- gofmt -l internal/customers && mise exec -- go vet ./internal/customers/...
mise exec -- go test -count=1 ./internal/customers/ ./internal/openapi/...
cd /home/anders/projects/vantigo/vantigo
cat > /tmp/claude-1000/msg-groups-2.txt <<'EOF'
feat(customers): the customer-group vocabulary has four endpoints

GET/POST /customers/groups and PUT/DELETE /customers/groups/{groupId}, the
tag vocabulary's shape with two differences that are both decisions: the
second field is a payment term, so the billing profile's own 0-365 rule
validates it under this request's own field name, and a group with members is
refused (409 group_in_use, with the count in the detail) instead of having
them detached — a silent detach would change every member's effective payment
term with no record on any customer. No new permission key, and no timeline
event on a vocabulary write.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
PATHS="openapi/customers.yaml openapi/COVERAGE.md \
 apps/server/internal/customers/groups.go apps/server/internal/customers/groups_test.go \
 apps/server/internal/customers/values.go apps/server/internal/customers/billing_values.go \
 apps/server/internal/customers/billing_values_test.go \
 apps/server/internal/openapi/openapi_test.go apps/server/internal/openapi/specs/customers.yaml \
 apps/server/internal/customers/gen apps/customers/frontend/src/api-schema.d.ts"
git add $PATHS && git commit -F /tmp/claude-1000/msg-groups-2.txt -- $PATHS
git show --stat HEAD && git status --short
```
One list, used for both — `git commit -- <paths>` commits the working tree of those paths whatever was staged, so a `git add` that names more (or fewer) than the commit's pathspec is either decorative or a way to sweep in somebody else's file. If `bun run gen:client` changed another package's `api-schema.d.ts`, add that path to `PATHS` too; `git status --short` must show nothing of yours left.

---

### Task 3: Membership — one group per customer, on the row, with its own event (D3)

`PUT /customers/{id}/group` is `PutCustomersByIdOwner` with a group instead of a user: the same five ordered steps, the same revision guard, the same no-op rule, the same 409. What is different is that the value lives in this module's own table, so the candidate is resolved with one query instead of a directory call — and that the response's `group` is decorated in a batch, like the tags', rather than selected by every customer query.

**Files:**
- Create: `apps/server/internal/customers/group_membership.go`, `apps/server/internal/customers/group_membership_test.go`, `apps/server/internal/customers/group_concurrency_test.go`
- Modify: `openapi/customers.yaml`, `apps/server/internal/openapi/openapi_test.go`, `apps/server/internal/customers/owner.go` (`customerDecoration`), `apps/server/internal/customers/customers.go` (`safeCustomerResponse`, `fromSetCustomerGroupRow`, the `groupId` filter), `apps/server/internal/customers/timeline_events.go`, `apps/server/internal/customers/queries/customers.sql` (both list WHEREs), `apps/server/internal/customers/customers_test.go` (`customerJSON.Group`)
- Generated: `apps/server/internal/openapi/specs/customers.yaml`, `internal/customers/gen/api.gen.go`, `internal/customers/store/customers.sql.go`, each changed `api-schema.d.ts`, `openapi/COVERAGE.md`
- Read first (do not change): `apps/server/internal/customers/owner.go:60-331` (the whole file — this task is its twin), `apps/server/internal/customers/timeline_events.go:456-496` (`ownerSnapshot`, `recordCustomerOwnerChanged`), `apps/server/internal/customers/customers.go:265-420` (`validateGetCustomersParams`, `GetCustomers`' resolution of `ownerId`/`tagId`), `apps/server/internal/customers/owner_concurrency_test.go` (the race harness this task's own concurrency test copies)

**Interfaces:**
- Produces (contract): schema `PutCustomerGroupRequest` (`{groupId?: uuid|null, revision?: int32}`, nothing required); `group?: CustomerGroupRef` on `SafeCustomerResponse` (additive, optional, never in `required:`); operation `putCustomersByIdGroup` (`permission:customers:update+customers:view`); query parameter `groupId` on `getCustomers`.
- Produces Go: `func (s *server) PutCustomersByIdGroup(ctx context.Context, req gen.PutCustomersByIdGroupRequestObject) (gen.PutCustomersByIdGroupResponseObject, error)`; `func groupNotFound(id uuid.UUID) string`; `func (d customerDecoration) group(customerID int32) *gen.CustomerGroupRef`; `func fromSetCustomerGroupRow(c store.SetCustomerGroupRow, ts store.CustomerTimelineSummaryRow) customerRow`; the test helper `putCustomerGroup(t, c, id, body) *modtest.Response` in `group_membership_test.go`, which Task 4's own tests also call; `type groupSnapshot struct { GroupID uuid.UUID; Name string }`; `func recordCustomerGroupChanged(ctx context.Context, q *store.Queries, now time.Time, customerID int32, before, after *groupSnapshot, actorKind, actorDisplay string, actorUserID *uuid.UUID) error`; `func validGroupFilter(raw string) bool`.
- Produces Go (changed): `customerDecoration` gains `groups map[int32]gen.CustomerGroupRef`; `CountCustomersParams`/`ListCustomersParams` gain `GroupNone bool` and `GroupID *uuid.UUID`.
- Consumes: `store.GetCustomerGroup`, `CustomerGroupMembership`, `CustomerGroupsForCustomers`, `SetCustomerGroup` from Task 1.

- [ ] **Step 1: Write the failing tests**

Create `apps/server/internal/customers/group_membership_test.go`:

```go
package customers_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is the membership half of phase 4 delivery D (customer groups
// design D3): PUT /customers/{id}/group, the group on every customer response,
// and the list's groupId filter.
//
// It is owner_test.go's matrix with the value in this module's own table
// instead of identity's directory: 404 for the customer, 409 for a stale
// revision ahead of the no-op check, a no-op that writes nothing at all, a
// field error for a group that does not exist, and one event per real change
// with the names snapshotted into it.

// putCustomerGroup PUTs /customers/{id}/group with the given body. The
// vocabulary's own tests (groups_test.go) deliberately do NOT use it — they set
// the column directly, because this operation did not exist in their task — but
// every test from here on does, this file's, group_concurrency_test.go's and
// Task 4's alike.
func putCustomerGroup(t *testing.T, c *modtest.Client, id int32, body map[string]any) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/group", id), body)
}

// TestPutCustomerGroup_SetsMovesAndClears walks the membership's whole life,
// because each step asserts the state the previous one left — and every
// response is a SafeCustomerResponse, so the group on it is asserted from the
// answer rather than from a follow-up GET.
func TestPutCustomerGroup_SetsMovesAndClears(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	retail := createGroup(t, c, map[string]any{"name": "Retail", "defaultPaymentTermsDays": 30})
	key := createGroup(t, c, map[string]any{"name": "Key accounts"})
	customer := createCustomer(t, c, "Grouped Co")

	// A customer belongs to no group until somebody says so, and the field is
	// ABSENT rather than null on the wire (design D3).
	if got := fetchCustomerJSON(t, c, customer.Id); got.Group != nil {
		t.Errorf("a fresh customer's group = %+v, want it absent", got.Group)
	}

	r := putCustomerGroup(t, c, customer.Id, map[string]any{"groupId": retail.Id, "revision": 1})
	if r.Status != http.StatusOK {
		t.Fatalf("set the group: status %d body %s, want 200", r.Status, r.Body)
	}
	var answered customerJSON
	r.JSON(&answered)
	if answered.Group == nil || answered.Group.Id != retail.Id || answered.Group.Name != "Retail" {
		t.Fatalf("answered group = %+v, want Retail: the PUT answers the whole customer", answered.Group)
	}
	if answered.Revision != 2 {
		t.Errorf("revision = %d, want 2: the group is a column on the row", answered.Revision)
	}

	// Moved, not added: one group at a time is the whole point of a column.
	moved := putCustomerGroup(t, c, customer.Id, map[string]any{"groupId": key.Id})
	if moved.Status != http.StatusOK {
		t.Fatalf("move the customer: status %d body %s, want 200", moved.Status, moved.Body)
	}
	if got := fetchCustomerJSON(t, c, customer.Id); got.Group == nil || got.Group.Name != "Key accounts" {
		t.Errorf("group after the move = %+v, want Key accounts", got.Group)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE id = $1 AND group_id = $2`, customer.Id, key.Id); n != 1 {
		t.Errorf("group_id rows = %d, want 1: the column holds exactly one group", n)
	}

	// Cleared: a real change like any other — it bumps the revision and records
	// an event, and the field goes back to being absent.
	cleared := putCustomerGroup(t, c, customer.Id, map[string]any{"groupId": nil})
	if cleared.Status != http.StatusOK {
		t.Fatalf("clear the group: status %d body %s, want 200", cleared.Status, cleared.Body)
	}
	cleared.JSON(&answered)
	if answered.Group != nil {
		t.Errorf("group after clearing = %+v, want it absent", answered.Group)
	}
	if answered.Revision != 4 {
		t.Errorf("revision = %d, want 4: set, move, clear are three writes", answered.Revision)
	}
	// An omitted groupId means the same as a null one — oapi-codegen collapses
	// the two into a nil *uuid.UUID, and clearing an already-cleared group is
	// the no-op below rather than an error.
	if r := putCustomerGroup(t, c, customer.Id, map[string]any{}); r.Status != http.StatusOK {
		t.Errorf("an empty body: status %d body %s, want 200", r.Status, r.Body)
	}
}

// TestPutCustomerGroup_TheOwnersMatrix is owner_test.go's own matrix, case for
// case: the four refusals, and the no-op that writes nothing at all.
func TestPutCustomerGroup_TheOwnersMatrix(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	retail := createGroup(t, c, map[string]any{"name": "Retail"})
	customer := createCustomer(t, c, "Matrix Co")

	// (1) The customer's own 404, ahead of everything.
	if r := putCustomerGroup(t, c, 999_999, map[string]any{"groupId": retail.Id}); r.Status != http.StatusNotFound {
		t.Errorf("an unknown customer: status %d body %s, want 404", r.Status, r.Body)
	}

	// (2) A stale revision is a 409 BEFORE the no-op check, so resubmitting the
	// current group with a stale revision is still a conflict rather than a free
	// pass (customers foundation design D5).
	if r := putCustomerGroup(t, c, customer.Id, map[string]any{"groupId": retail.Id}); r.Status != http.StatusOK {
		t.Fatalf("set the group: status %d body %s", r.Status, r.Body)
	}
	stale := putCustomerGroup(t, c, customer.Id, map[string]any{"groupId": retail.Id, "revision": 1})
	if stale.Status != http.StatusConflict {
		t.Fatalf("a stale revision on a no-op: status %d body %s, want 409", stale.Status, stale.Body)
	}
	var problem conflictProblemJSON
	stale.JSON(&problem)
	if problemTitle(problem.Title) != "Customer revision conflict" || problem.Code != nil {
		t.Errorf("conflict = title %q code %v, want the revision conflict with no code", problemTitle(problem.Title), problem.Code)
	}

	// (3) The no-op: the same group, at the current revision, writes nothing —
	// no revision bump, no updated_at move, no event.
	before := fetchCustomerJSON(t, c, customer.Id)
	events := countTimelineEvents(t, h, customer.Id, "customer.group_changed")
	noop := putCustomerGroup(t, c, customer.Id, map[string]any{"groupId": retail.Id, "revision": before.Revision})
	if noop.Status != http.StatusOK {
		t.Fatalf("the no-op: status %d body %s, want 200", noop.Status, noop.Body)
	}
	var answered customerJSON
	noop.JSON(&answered)
	if answered.Revision != before.Revision || !answered.UpdatedAt.Equal(before.UpdatedAt) {
		t.Errorf("no-op answered revision %d updatedAt %s, want %d/%s unchanged",
			answered.Revision, answered.UpdatedAt, before.Revision, before.UpdatedAt)
	}
	if answered.Group == nil || answered.Group.Id != retail.Id {
		t.Errorf("no-op answered group = %+v, want Retail: a no-op still answers the customer", answered.Group)
	}
	if got := countTimelineEvents(t, h, customer.Id, "customer.group_changed"); got != events {
		t.Errorf("customer.group_changed events = %d after a no-op, want %d", got, events)
	}
	// Clearing a customer that is in no group is the nil/nil half of the same
	// rule, and it is the case an implementation that only compares dereferenced
	// ids gets wrong.
	unassigned := createCustomer(t, c, "Never Grouped AS")
	nilNoop := putCustomerGroup(t, c, unassigned.Id, map[string]any{"groupId": nil})
	if nilNoop.Status != http.StatusOK {
		t.Fatalf("clearing an ungrouped customer: status %d body %s, want 200", nilNoop.Status, nilNoop.Body)
	}
	nilNoop.JSON(&answered)
	if answered.Revision != 1 {
		t.Errorf("revision = %d after clearing an ungrouped customer, want 1: nothing was written", answered.Revision)
	}
	if n := countTimelineEvents(t, h, unassigned.Id, "customer.group_changed"); n != 0 {
		t.Errorf("customer.group_changed events = %d, want 0", n)
	}

	// (4) A group that does not exist is a field error on groupId, not a 404:
	// the customer exists and the caller may edit it, so what is wrong is the
	// body they sent (ownerNotFound's own reasoning).
	missing := uuid.New()
	bad := putCustomerGroup(t, c, customer.Id, map[string]any{"groupId": missing.String()})
	if bad.Status != http.StatusBadRequest {
		t.Fatalf("an unknown group: status %d body %s, want 400", bad.Status, bad.Body)
	}
	var invalid validationProblemJSON
	bad.JSON(&invalid)
	want := fmt.Sprintf("Customer group %s does not exist", missing)
	if msgs := invalid.Errors["groupId"]; len(msgs) != 1 || msgs[0] != want {
		t.Errorf("errors[groupId] = %v, want [%q]", msgs, want)
	}
}

// TestPutCustomerGroup_RecordsTheEventWithSnapshottedNames pins
// customer.group_changed's summary, payload and payload version for all three
// shapes of the change, and the reason the names are IN the payload: renaming a
// group afterwards must not rewrite what the timeline says happened.
func TestPutCustomerGroup_RecordsTheEventWithSnapshottedNames(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	retail := createGroup(t, c, map[string]any{"name": "Retail"})
	key := createGroup(t, c, map[string]any{"name": "Key accounts"})
	customer := createCustomer(t, c, "Event Co")

	if r := putCustomerGroup(t, c, customer.Id, map[string]any{"groupId": retail.Id}); r.Status != http.StatusOK {
		t.Fatalf("set: status %d body %s", r.Status, r.Body)
	}
	assigned := fetchTimelineEvent(t, h, customer.Id, "customer.group_changed")
	if assigned.Summary != "Moved to group Retail" {
		t.Errorf("summary = %q, want %q", assigned.Summary, "Moved to group Retail")
	}
	if assigned.PayloadVersion != 1 {
		t.Errorf("payloadVersion = %d, want 1", assigned.PayloadVersion)
	}
	if assigned.Payload["before"] != nil {
		t.Errorf("before = %v, want null: the customer was in no group", assigned.Payload["before"])
	}
	after, ok := assigned.Payload["after"].(map[string]any)
	if !ok || after["groupId"] != retail.Id || after["name"] != "Retail" {
		t.Errorf("after = %v, want the group's id and its name at the time", assigned.Payload["after"])
	}

	if r := putCustomerGroup(t, c, customer.Id, map[string]any{"groupId": key.Id}); r.Status != http.StatusOK {
		t.Fatalf("move: status %d body %s", r.Status, r.Body)
	}
	// The vocabulary is renamed AFTER the move: the entries above must still say
	// what they said, which is the whole reason the name is snapshotted.
	if r := c.Do(http.MethodPut, "/api/v1/customers/groups/"+retail.Id, map[string]any{"name": "Retail chains"}); r.Status != http.StatusOK {
		t.Fatalf("rename: status %d body %s", r.Status, r.Body)
	}
	moved := fetchTimelineEvent(t, h, customer.Id, "customer.group_changed")
	if moved.Summary != "Moved from Retail to Key accounts" {
		t.Errorf("summary = %q, want %q — the name at the time, not today's", moved.Summary, "Moved from Retail to Key accounts")
	}
	movedBefore, ok := moved.Payload["before"].(map[string]any)
	if !ok || movedBefore["name"] != "Retail" {
		t.Errorf("before = %v, want the name the group had when the move happened", moved.Payload["before"])
	}

	if r := putCustomerGroup(t, c, customer.Id, map[string]any{"groupId": nil}); r.Status != http.StatusOK {
		t.Fatalf("clear: status %d body %s", r.Status, r.Body)
	}
	removed := fetchTimelineEvent(t, h, customer.Id, "customer.group_changed")
	if removed.Summary != "Removed from group Key accounts" {
		t.Errorf("summary = %q, want %q", removed.Summary, "Removed from group Key accounts")
	}
	if removed.Payload["after"] != nil {
		t.Errorf("after = %v, want null: the customer is in no group", removed.Payload["after"])
	}
	if n := countTimelineEvents(t, h, customer.Id, "customer.group_changed"); n != 3 {
		t.Errorf("customer.group_changed events = %d, want 3 (set, move, clear)", n)
	}
}

// TestPutCustomerGroup_OnlyTheSubResourceSetsTheGroup is the owner's rule
// restated (design D3): POST /customers and PUT /customers/{id} do not learn a
// groupId. A body that names one is a body with an unknown key — no schema in
// customers.yaml sets additionalProperties: false — so it is ignored, and the
// customer ends up in no group.
func TestPutCustomerGroup_OnlyTheSubResourceSetsTheGroup(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	retail := createGroup(t, c, map[string]any{"name": "Retail"})

	r := c.Do(http.MethodPost, "/api/v1/customers", map[string]any{"name": "Sneaky Co", "groupId": retail.Id})
	if r.Status != http.StatusCreated {
		t.Fatalf("create: status %d body %s, want 201", r.Status, r.Body)
	}
	var created createdCustomerJSON
	r.JSON(&created)
	if got := fetchCustomerJSON(t, c, created.Id); got.Group != nil {
		t.Errorf("group after a create that named one = %+v, want it absent", got.Group)
	}
	if u := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d", created.Id), map[string]any{
		"name": "Sneaky Co", "groupId": retail.Id,
	}); u.Status != http.StatusOK {
		t.Fatalf("update: status %d body %s, want 200", u.Status, u.Body)
	}
	if got := fetchCustomerJSON(t, c, created.Id); got.Group != nil {
		t.Errorf("group after an update that named one = %+v, want it absent", got.Group)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers WHERE id = $1 AND group_id IS NULL`, created.Id); n != 1 {
		t.Errorf("group_id is set on the row, want NULL: only the sub-resource writes it")
	}
}

// TestGetCustomers_GroupIdFilter pins the filter in BOTH queries: the count and
// the rows have to agree, which is the one thing a hand-kept duplicate WHERE
// clause can get wrong.
func TestGetCustomers_GroupIdFilter(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	retail := createGroup(t, c, map[string]any{"name": "Retail"})
	inGroup := createCustomer(t, c, "Filter In Group AS")
	createCustomer(t, c, "Filter No Group AS")
	if r := putCustomerGroup(t, c, inGroup.Id, map[string]any{"groupId": retail.Id}); r.Status != http.StatusOK {
		t.Fatalf("set the group: status %d body %s", r.Status, r.Body)
	}

	byGroup := getList(t, c, "groupId="+retail.Id+"&pageSize=100")
	if len(byGroup.Data) != 1 || byGroup.Data[0].Id != inGroup.Id {
		t.Fatalf("groupId=<uuid> rows = %+v, want only the member", byGroup.Data)
	}
	if byGroup.Pagination.TotalCount != 1 {
		t.Errorf("totalCount = %d, want 1: CountCustomers and ListCustomers must apply the same WHERE",
			byGroup.Pagination.TotalCount)
	}

	none := getList(t, c, "groupId=none&pageSize=100")
	if none.Pagination.TotalCount != len(none.Data) {
		t.Errorf("groupId=none: totalCount %d but %d rows", none.Pagination.TotalCount, len(none.Data))
	}
	for _, row := range none.Data {
		if row.Group != nil {
			t.Errorf("groupId=none returned %q with group %+v", row.Name, row.Group)
		}
		if row.Id == inGroup.Id {
			t.Errorf("groupId=none returned the member")
		}
	}

	// An unknown uuid matches nothing — a filter, not an error (design D3).
	unknown := getList(t, c, "groupId="+uuid.NewString())
	if unknown.Pagination.TotalCount != 0 || len(unknown.Data) != 0 {
		t.Errorf("an unknown groupId: %d rows, totalCount %d, want none", len(unknown.Data), unknown.Pagination.TotalCount)
	}

	// The shape check is the module's own query-parameter wording, with the
	// trailing period every message in validateGetCustomersParams has.
	bad := c.Do(http.MethodGet, "/api/v1/customers?groupId=nonsense", nil)
	if bad.Status != http.StatusBadRequest {
		t.Fatalf("groupId=nonsense: status %d body %s, want 400", bad.Status, bad.Body)
	}
	var problem problemDetailsJSON
	bad.JSON(&problem)
	if want := "'groupId' must be a group id or 'none', but was 'nonsense'."; problem.Detail == nil || *problem.Detail != want {
		t.Errorf("detail = %v, want %q", problem.Detail, want)
	}
}
```

Every helper this file uses already exists in the package — reuse them, never redeclare them: `fetchTimelineEvent`/`countTimelineEvents` (`contacts_test.go:817-851`), `problemTitle` and `problemDetailsJSON` (`timeline_test.go:60-70` — **not** `customers_test.go`, which holds `validationProblemJSON` and `customerJSON`), `fetchCustomerJSON` (`legal_identity_test.go:44`), and `getList(t, c, query)` (`customers_list_test.go:23`), whose query string carries **no leading `?`** and whose result is `customerListJSON` (`.Data []customerJSON`, `.Pagination.TotalCount int`).

In `apps/server/internal/customers/customers_test.go`, add the field the assertions above read, beside `Owner`:

```go
	Owner          *ownerJSON        `json:"owner"`
	Group          *groupRefJSON     `json:"group"`
	Tags           []tagJSON         `json:"tags"`
```
and, after `ownerJSON`:

```go
// groupRefJSON decodes CustomerGroupRef, a pointer on customerJSON for the same
// reason ownerJSON is one: it is genuinely absent for a customer in no group
// (customer groups design D3), never null.
type groupRefJSON struct {
	Id   string `json:"id"`
	Name string `json:"name"`
}
```

- [ ] **Step 2: Write the concurrency test**

Create `apps/server/internal/customers/group_concurrency_test.go`:

```go
package customers_test

import (
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file pins PUT /customers/{id}/group's guarded UPDATE (SetCustomerGroup,
// queries/groups.sql) — owner_concurrency_test.go's twin, for the same reason
// and with the same gate: gateCustomerLock holds FOR UPDATE on the customer
// row, and the handler's own pre-check reads through an unlocked SELECT the gate
// does not hold up, so both PUTs see the same still-current revision and only
// the database's "AND (expected_revision IS NULL OR revision = expected_revision)"
// can decide a winner once the gate releases. Dropping that clause from
// SetCustomerGroup's WHERE lets both PUTs succeed (two 200s, the revision bumped
// twice, no 409 at all), which the "exactly one winner" assertion below catches.
//
// Not parallel: awaitLockWaiters counts lock waiters across the whole database.

// TestPutCustomersByIdGroup_ConcurrentUpdatesWithSameRevision_ExactlyOneWins
// races two moves of the same customer into two DIFFERENT groups, both claiming
// revision 1. Two different groups on purpose: the same group twice is answered
// by the no-op check before any write and nothing would race at all.
func TestPutCustomersByIdGroup_ConcurrentUpdatesWithSameRevision_ExactlyOneWins(t *testing.T) {
	h := newHarness(t)
	c := authenticatedClient(t, h)
	retail := createGroup(t, c, map[string]any{"name": "Retail"})
	key := createGroup(t, c, map[string]any{"name": "Key accounts"})
	created := createCustomer(t, c, "Group Race Co")

	release := gateCustomerLock(t, h, created.Id)

	moveTo := func(groupID string) func() *modtest.Response {
		return func() *modtest.Response {
			return putCustomerGroup(t, c, created.Id, map[string]any{"groupId": groupID, "revision": 1})
		}
	}

	done := make(chan []*modtest.Response, 1)
	finished := make(chan struct{})
	go func() {
		done <- race(moveTo(retail.Id), moveTo(key.Id))
		close(finished)
	}()
	awaitLockWaiters(t, h, 2, finished)
	release()
	responses := <-done

	var winners, losers int
	for _, r := range responses {
		switch r.Status {
		case http.StatusOK:
			winners++
		case http.StatusConflict:
			losers++
			var problem conflictProblemJSON
			r.JSON(&problem)
			// The database guard's fallback answers in the same words as the
			// pre-check, with no code: one conflict, one wording, whichever
			// branch produced it (customers foundation design D5).
			if problemTitle(problem.Title) != "Customer revision conflict" || problem.Code != nil {
				t.Errorf("loser = title %q code %v, want the revision conflict with no code",
					problemTitle(problem.Title), problem.Code)
			}
		default:
			t.Errorf("status %d body %s, want 200 or 409", r.Status, r.Body)
		}
	}
	if winners != 1 || losers != 1 {
		t.Fatalf("winners=%d losers=%d, want exactly one of each", winners, losers)
	}
	if rev := h.Count(t, `SELECT revision FROM customers.customers WHERE id = $1`, created.Id); rev != 2 {
		t.Errorf("revision = %d, want 2: exactly one write landed", rev)
	}
	if n := countTimelineEvents(t, h, created.Id, "customer.group_changed"); n != 1 {
		t.Errorf("customer.group_changed events = %d, want 1: the loser wrote nothing", n)
	}
}
```

- [ ] **Step 3: Run them and watch them fail**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'
mise exec -- go test -count=1 -run 'TestPutCustomerGroup|TestGetCustomers_GroupIdFilter|TestPutCustomersByIdGroup' ./internal/customers/
```
Expected: the package **builds** — `customerJSON.Group` is a field nothing sets yet, and no test here names a generated type — and every case fails behaviourally:

- every `putCustomerGroup` reports `status 404` plus the recorder's "no operation matches" (the contract has no `PUT /customers/{id}/group` yet);
- `TestGetCustomers_GroupIdFilter` reports `groupId=<uuid> rows = 2, want only the member` and `totalCount = 2, want 1`. It does **not** fail to build and does **not** 400: oapi-codegen's std-http server ignores a query parameter the contract does not declare, so `groupId=…` reaches a handler that filters by nothing;
- the same test's shape check reports `status 200 … want 400` for `groupId=nonsense`, for the same reason.

- [ ] **Step 4: Add the operation, the request schema, the response field and the filter to the contract**

In `openapi/customers.yaml`:

`PutCustomerGroupRequest`, beside `PutCustomerOwnerRequest` (line ~944):

```yaml
        PutCustomerGroupRequest:
            description: "PUT /customers/{id}/group's own request body (customer groups design D3): the customer is put into groupId, or taken out of every group when it is absent or null. The group must exist; an unknown id is a field error on groupId. revision is optional, as PUT /customers/{id}/owner's own is — omitted, the change applies regardless; present and stale, a 409. Only this sub-resource sets a customer's group: POST /customers and PUT /customers/{id} do not take one."
            properties:
                groupId:
                    format: uuid
                    nullable: true
                    type: string
                revision:
                    description: The revision the caller read the customer at (customers foundation design D5). Optional — omitted, the change applies regardless; present and stale, a 409.
                    format: int32
                    nullable: true
                    type: integer
            type: object
```

`group` on `SafeCustomerResponse` (between `createdAt`/`customerNumber`'s neighbours, where `group` sorts — after `customerNumber`, before `id`), additive and **not** added to `required:`:

```yaml
                group:
                    allOf:
                        - $ref: '#/components/schemas/CustomerGroupRef'
                    description: The group this customer belongs to (customer groups design D3). Absent when it belongs to none — omitted, never null, like owner beside it; needs nothing beyond customers:view to read. A group's own default payment term is not here: it is read from GET /customers/groups, or already resolved on the billing profile's groupDefault.
```

The `groupId` query parameter on `getCustomers`, after `tagId` (line ~1404):

```yaml
                - in: query
                  name: groupId
                  schema:
                    description: "A group id, or the literal 'none' (customers in no group). Case-sensitive. An id no group holds simply matches nothing."
                    type: string
```

And the path, between `/api/v1/customers/{id}/contacts/{contactId}` and `/api/v1/customers/{id}/legal-identity`:

```yaml
    /api/v1/customers/{id}/group:
        put:
            operationId: putCustomersByIdGroup
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
                            $ref: '#/components/schemas/PutCustomerGroupRequest'
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
            summary: Put a customer in a group, or take it out of every group
            tags:
                - Customers
            x-vantigo-access: permission:customers:update+customers:view
```

Then the three conflict pins this operation creates, in `apps/server/internal/openapi/openapi_test.go`'s `KnownServeMuxConflicts`, in alphabetical position:

```go
	"PUT /api/v1/customers/contacts/{id} ⟷ PUT /api/v1/customers/{id}/group",
	"PUT /api/v1/customers/groups/{groupId} ⟷ PUT /api/v1/customers/{id}/group",
	"PUT /api/v1/customers/tags/{tagId} ⟷ PUT /api/v1/customers/{id}/group",
```

- [ ] **Step 5: Generate and re-run the contract gates**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- go generate ./... && mise exec -- go generate ./... && git status --short
mise exec -- go test -count=1 ./internal/openapi/...
mise exec -- go run ./internal/openapi/cmd/contract coverage -corpus ../../openapi/testdata/exchanges -out ../../openapi/COVERAGE.md
cd /home/anders/projects/vantigo/vantigo && mise exec -- bun run gen:client
```
The frozen corpus must still validate: `group` is optional and added to nothing's `required:`, so every recorded `SafeCustomerResponse` still matches.

- [ ] **Step 6: Write the membership**

In `apps/server/internal/customers/timeline_events.go`, after `recordCustomerOwnerChanged`:

```go
// groupSnapshot is customer.group_changed's before/after shape (customer groups
// design D3): which group, and what it was called at the time. The name is
// snapshotted for ownerSnapshot's own reason — a group renamed later must not
// rewrite what the timeline says happened, and a group's name is edited far more
// freely than a user's, since it is installation policy rather than a person.
type groupSnapshot struct {
	GroupID uuid.UUID `json:"groupId"`
	Name    string    `json:"name"`
}

// recordCustomerGroupChanged is PutCustomersByIdGroup's own generated event
// (design D3). Only called once the handler has confirmed the group actually
// changed, so before and after are never equal and never both nil — which is
// what lets the summary always name at least one group, and why there are
// exactly three sentences rather than a rendered "x → y" that would read
// "nobody → Retail".
//
// Like recordCustomerOwnerChanged there is no changes map: there is exactly one
// field, so before/after IS the change, and the spec names this payload shape
// exactly.
func recordCustomerGroupChanged(ctx context.Context, q *store.Queries, now time.Time, customerID int32, before, after *groupSnapshot, actorKind, actorDisplay string, actorUserID *uuid.UUID) error {
	var summary string
	switch {
	case before == nil:
		summary = fmt.Sprintf("Moved to group %s", after.Name)
	case after == nil:
		summary = fmt.Sprintf("Removed from group %s", before.Name)
	default:
		summary = fmt.Sprintf("Moved from %s to %s", before.Name, after.Name)
	}
	payload := map[string]any{
		"customerId": customerID,
		"before":     before,
		"after":      after,
	}
	return recordGeneratedEvent(ctx, q, customerID, now, "customer.group_changed", truncateUTF16(summary, 500), payload, 1, actorKind, actorDisplay, actorUserID)
}
```

In `apps/server/internal/customers/owner.go`, `customerDecoration` gains its third map — the group of a whole response in one query, never one per row:

```go
type customerDecoration struct {
	owners map[uuid.UUID]contracts.UserEntry
	tags   map[int32][]gen.CustomerTag
	groups map[int32]gen.CustomerGroupRef
}
```
in `decorateKnowing`, beside the tags' own initialisation and query:

```go
	dec := customerDecoration{
		owners: map[uuid.UUID]contracts.UserEntry{},
		tags:   map[int32][]gen.CustomerTag{},
		groups: map[int32]gen.CustomerGroupRef{},
	}
```
```go
	links, err := q.CustomerTagsForCustomers(ctx, customerIDs)
	if err != nil {
		return customerDecoration{}, fmt.Errorf("customers: load customer tags: %w", err)
	}
	for _, l := range links {
		dec.tags[l.CustomerID] = append(dec.tags[l.CustomerID], gen.CustomerTag{Id: l.ID, Name: l.Name, Color: l.Color})
	}

	// The group is one more batched query over the same customer ids (customer
	// groups design D3), and it is a query rather than a column on every
	// customer row's own SELECT for the tags' reason: one shape of
	// group-on-a-response, one place it can be wrong, and five existing row
	// types that would otherwise each have to grow a column they have no other
	// use for. A customer in no group simply has no row here.
	groups, err := q.CustomerGroupsForCustomers(ctx, customerIDs)
	if err != nil {
		return customerDecoration{}, fmt.Errorf("customers: load customer groups: %w", err)
	}
	for _, g := range groups {
		dec.groups[g.CustomerID] = gen.CustomerGroupRef{Id: g.ID, Name: g.Name}
	}
```
and, beside `tagsFor`:

```go
// group is one customer's group as the contract reports it, or nil when the
// customer belongs to none — absent on the wire, never null, the idiom owner
// beside it already follows (customer groups design D3).
func (d customerDecoration) group(customerID int32) *gen.CustomerGroupRef {
	if g, ok := d.groups[customerID]; ok {
		return &g
	}
	return nil
}
```

In `apps/server/internal/customers/customers.go`, `safeCustomerResponse` gains one line (and its doc comment one clause: "…and the customer's group, which lives in the module's own vocabulary table (design D3)"):

```go
		Owner:          dec.owner(row.OwnerUserID),
		Group:          dec.group(row.ID),
		Tags:           &tags,
```
and, after `fromUpdateCustomerOwnerRow`:

```go
// fromSetCustomerGroupRow is SetCustomerGroup's own row
// (PutCustomersByIdGroup's write, group_membership.go). Its column list is
// UpdateCustomerOwner's, which is why it needs no new parameter: the group
// itself is decorated onto the response afterwards, not read from this row.
func fromSetCustomerGroupRow(c store.SetCustomerGroupRow, ts store.CustomerTimelineSummaryRow) customerRow {
	return customerRowFrom(c.ID, c.CustomerNumber, c.Name, c.Status, c.Type, c.LegalCountry, c.LegalID, c.LegalName, c.LegalSource, c.LegalType, c.CreatedAt, c.UpdatedAt, c.Revision, c.Email, c.Phone, c.Website, c.OwnerUserID, ts)
}
```

Create `apps/server/internal/customers/group_membership.go`:

```go
package customers

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
	"github.com/vantigo-io/vantigo/server/internal/customers/store"
	"github.com/vantigo-io/vantigo/server/internal/db"
)

// This file is the membership half of phase 4 delivery D (customer groups
// design D3): PUT /customers/{id}/group. The vocabulary it writes an id from is
// groups.go.
//
// It is owner.go's PUT with one difference, and the difference is what makes it
// simpler rather than merely different: the value lives in THIS module's own
// table (customers.customer_groups, migration 00027), so the candidate is
// resolved with one query instead of a call into identity's directory — no
// out-of-process call to keep out of the transaction, and a real foreign key
// under the column, so the write cannot land on a group that does not exist
// even if this handler's own check were wrong.
//
// Everything else is owner.go's, deliberately: the ordering is (1) the customer
// lookup, 404; (2) a supplied revision that disagrees with the row just read,
// 409 — ahead of the no-op check, so resubmitting the current group with a stale
// revision is still a conflict; (3) the no-op check, nil-safe, which writes
// nothing at all (customers foundation design D5); (4) the candidate's own
// existence, 400 keyed groupId; (5) the guarded write and its timeline event in
// one transaction.
//
// Steps 3 and 4 are in that order for owner.go's own reason: the check "does
// this group exist" applies to a CHANGE of group, and a no-op resubmit must not
// be refused for the state the customer is already in. Unlike the owner's
// disabled-account case this is mostly theoretical — a group with members
// cannot be deleted (design D2) — and it is kept anyway, because the ordering
// is the module's rule and a reader comparing the two files must find the same
// shape.

// groupNotFound is the field error for a groupId no group holds, worded as
// ownerNotFound (owner.go) and tagNotFound (tags.go) word their own. A field
// error and not a 404: the customer exists and the caller may edit it, so what
// is wrong is the body they sent.
func groupNotFound(id uuid.UUID) string {
	return fmt.Sprintf("Customer group %s does not exist", id)
}

// PutCustomersByIdGroup Put a customer in a group, or take it out of every group
// (PUT /api/v1/customers/{id}/group)
func (s *server) PutCustomersByIdGroup(ctx context.Context, req gen.PutCustomersByIdGroupRequestObject) (gen.PutCustomersByIdGroupResponseObject, error) {
	body := gen.PutCustomerGroupRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	q := store.New(s.deps.Pool)
	existing, err := q.GetCustomer(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PutCustomersByIdGroup404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("customers: get customer: %w", err)
	}

	if body.Revision != nil && *body.Revision != existing.Revision {
		return gen.PutCustomersByIdGroup409ApplicationProblemPlusJSONResponse(customerRevisionConflict(*body.Revision, existing.Revision)), nil
	}

	// The customer's current group, with the name the event's before snapshot
	// needs: one query rather than a column on GetCustomer (see
	// CustomerGroupMembership's own comment in queries/groups.sql). pgx.ErrNoRows
	// would mean the customer vanished between the two reads, which is the same
	// 404 as above.
	//
	// current.GroupID is *uuid.UUID (the customer's own nullable column) and
	// current.GroupName is *string — Task 1 Step 7's gate. If the generated row
	// types say otherwise, fix the QUERY there, not this handler: uuidPtrEqual
	// below and deref() two lines down both take pointers, and a "" that means
	// "no group" would be a second spelling of the nil the column already has.
	current, err := q.CustomerGroupMembership(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PutCustomersByIdGroup404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("customers: read customer group membership: %w", err)
	}

	includeIdentity := s.hasPermission(ctx, legalIdentityView)
	before, after := current.GroupID, body.GroupId

	respond := func(row customerRow) (gen.PutCustomersByIdGroupResponseObject, error) {
		dec, err := s.decorate(ctx, q, row)
		if err != nil {
			return nil, err
		}
		return gen.PutCustomersByIdGroup200JSONResponse(safeCustomerResponse(row, includeIdentity, dec)), nil
	}

	if uuidPtrEqual(before, after) {
		// The same group, nil included: nothing written, no revision bump, no
		// event, and no actor resolved (customers foundation design D5). The
		// response is still the whole customer, decorated — which is why the
		// no-op is answered here rather than as a 304 or an empty body.
		summary, err := q.CustomerTimelineSummary(ctx, req.Id)
		if err != nil {
			return nil, fmt.Errorf("customers: timeline summary: %w", err)
		}
		return respond(fromCustomerRow(existing, summary))
	}

	var afterSnapshot *groupSnapshot
	if after != nil {
		group, err := q.GetCustomerGroup(ctx, *after)
		if errors.Is(err, pgx.ErrNoRows) {
			return gen.PutCustomersByIdGroup400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem(
				"Invalid customer group", map[string][]string{"groupId": {groupNotFound(*after)}})), nil
		}
		if err != nil {
			return nil, fmt.Errorf("customers: resolve customer group: %w", err)
		}
		afterSnapshot = &groupSnapshot{GroupID: group.ID, Name: group.Name}
	}

	var beforeSnapshot *groupSnapshot
	if before != nil {
		// The name comes from the join this handler already made, not from a
		// second read: the group cannot have been deleted while this customer
		// belonged to it (the foreign key's RESTRICT, design D2), so the name
		// read a moment ago is the name to snapshot.
		beforeSnapshot = &groupSnapshot{GroupID: *before, Name: deref(current.GroupName)}
	}

	now := s.deps.Clock()
	// Resolved before the transaction opens, and only here: by this point the
	// handler always records customer.group_changed (the no-op returned above),
	// so the actor is always needed (customers foundation design D1).
	act, err := s.actorFor(ctx, generatedFallbackActor)
	if err != nil {
		return nil, fmt.Errorf("customers: resolve actor: %w", err)
	}

	var updated store.SetCustomerGroupRow
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		var err error
		updated, err = txq.SetCustomerGroup(ctx, store.SetCustomerGroupParams{
			ID: req.Id, GroupID: after, UpdatedAt: now, ExpectedRevision: body.Revision,
		})
		if err != nil {
			return err
		}
		return recordCustomerGroupChanged(ctx, txq, now, req.Id, beforeSnapshot, afterSnapshot, act.Kind, act.Display, act.UserID)
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// The guarded UPDATE matched no row: a concurrent writer moved the
		// revision between the read above and this write, answered by re-reading
		// and reporting the row's now-current revision — the same race
		// PutCustomersByIdOwner's own guarded write answers, in the same words
		// and with no code.
		fresh, ferr := q.GetCustomer(ctx, req.Id)
		if errors.Is(ferr, pgx.ErrNoRows) {
			return gen.PutCustomersByIdGroup404Response{}, nil
		}
		if ferr != nil {
			return nil, fmt.Errorf("customers: re-read customer after conflict: %w", ferr)
		}
		return gen.PutCustomersByIdGroup409ApplicationProblemPlusJSONResponse(customerRevisionConflict(existing.Revision, fresh.Revision)), nil
	case db.IsForeignKeyViolation(err, customersGroupFK):
		// The group was deleted between the resolve above and this write — the
		// one window the resolve cannot close, since nothing here locks the
		// vocabulary (and design D2 makes it narrow: only an EMPTY group can be
		// deleted, so this is reachable only for a customer that was in no group
		// a moment ago). Answered as the field error the resolve itself would
		// have given a moment later, exactly as PutCustomersByIdTags answers its
		// own late foreign-key violation.
		return gen.PutCustomersByIdGroup400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem(
			"Invalid customer group", map[string][]string{"groupId": {groupNotFound(*after)}})), nil
	case err != nil:
		return nil, fmt.Errorf("customers: set customer group: %w", err)
	}

	summary, err := q.CustomerTimelineSummary(ctx, req.Id)
	if err != nil {
		return nil, fmt.Errorf("customers: timeline summary: %w", err)
	}
	return respond(fromSetCustomerGroupRow(updated, summary))
}
```

- [ ] **Step 7: The list filter, in both WHEREs and in Go**

In `apps/server/internal/customers/queries/customers.sql`, add the same two lines to `CountCustomers`' WHERE **and** `ListCustomers`' WHERE, after the `tag_id` block in each (the two clauses are kept textually identical by hand — that is the file's own standing warning, and pagination's `totalCount` disagreeing with the page it describes is what drift costs):

```sql
  AND (NOT @group_none::bool OR c.group_id IS NULL)
  AND (sqlc.narg(group_id)::uuid IS NULL OR c.group_id = sqlc.narg(group_id)::uuid)
```
and extend `CountCustomers`' own comment where it explains `owner_none`/`owner_id`:

```sql
-- group_none/group_id are design D3's groupId filter, and they are the
-- owner's two halves for the owner's reason: 'none' is "in no group at all" (a
-- NULL test, which no equality can express) and a uuid is an equality. They are
-- never both set — the Go validation turns exactly one groupId value into
-- exactly one of them — and an equality, not tag_id's EXISTS, because a group
-- is single-valued like the owner rather than many-to-many like a tag.
```

In `apps/server/internal/customers/customers.go`, `validateGetCustomersParams` gains the shape check next to `tagId`'s:

```go
	if p.TagId != nil && !validUUIDParam(*p.TagId) {
		errs = append(errs, fmt.Sprintf("'tagId' must be a tag id, but was '%s'.", *p.TagId))
	}
	if p.GroupId != nil && !validGroupFilter(*p.GroupId) {
		errs = append(errs, fmt.Sprintf("'groupId' must be a group id or 'none', but was '%s'.", *p.GroupId))
	}
```
and, beside `validOwnerFilter`:

```go
// validGroupFilter is the groupId parameter's shape (customer groups design
// D3): a group id, or the literal 'none'. There is no 'me' to resolve, so
// unlike ownerId this needs no session at all — and 'none' is matched
// case-sensitively, as every other query parameter in this function is.
func validGroupFilter(raw string) bool {
	return raw == "none" || validUUIDParam(raw)
}
```
then the resolution in `GetCustomers`, after `tagID`'s:

```go
	// groupId's two forms resolve to the two SQL parameters the list queries
	// take (design D3). No 'me' here and nothing session-dependent: a group is a
	// bucket the installation defines, not a relationship to the caller.
	var groupID *uuid.UUID
	groupNone := false
	if req.Params.GroupId != nil {
		if *req.Params.GroupId == "none" {
			groupNone = true
		} else if id, err := uuid.Parse(*req.Params.GroupId); err == nil {
			// validateGetCustomersParams already refused anything unparseable, so
			// err is impossible here; the guard means an impossible value filters
			// nothing rather than panicking.
			groupID = &id
		}
	}
```
and both `store.…Params` literals gain `GroupNone: groupNone, GroupID: groupID,` beside `OwnerNone`/`OwnerID`/`TagID`.

- [ ] **Step 8: Run everything and watch it pass**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- go generate ./... && mise exec -- gofmt -l internal/customers && mise exec -- go vet ./...
mise exec -- go test -count=1 ./internal/customers/
mise exec -- go test -count=1 -run 'TestPutCustomersByIdGroup_Concurrent' ./internal/customers/
```
Expected: PASS — the whole package, Task 2's vocabulary tests included (they set memberships through the column and are unaffected by this task). `go vet ./...` covers every module rather than this one: `customerDecoration` and `customerRow`'s adapters are package-private, but `contracts` is not, and Task 4 will touch it.

- [ ] **Step 9: Show a test can fail, then commit**

Prove two guards: delete the `AND (sqlc.narg(expected_revision)...)` line from `SetCustomerGroup`, regenerate, and watch the concurrency test report two winners; restore it by hand and regenerate. Then remove the `group_none`/`group_id` lines from `CountCustomers` only (leaving `ListCustomers`), regenerate, and watch `TestGetCustomers_GroupIdFilter` report `totalCount = 2, want 1` — the exact drift the duplicated WHERE risks; restore by hand. Say in the report what each printed.

```bash
cd /home/anders/projects/vantigo/vantigo
cat > /tmp/claude-1000/msg-groups-3.txt <<'EOF'
feat(customers): a customer belongs to at most one group

PUT /customers/{id}/group is the owner's PUT with a group: the same five
ordered steps, the same revision guard on the customer row, the same nil-safe
no-op that writes nothing, and a field error on groupId for a group that does
not exist. Every SafeCustomerResponse carries group when there is one —
decorated in one batched query per response, like the tags, so no existing
customer query grew a column — and GET /customers takes groupId=<uuid>|none in
both the count's and the page's WHERE. Each real change records
customer.group_changed with the group names as they read at the time.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
PATHS="openapi/customers.yaml openapi/COVERAGE.md \
 apps/server/internal/customers/group_membership.go \
 apps/server/internal/customers/group_membership_test.go \
 apps/server/internal/customers/group_concurrency_test.go \
 apps/server/internal/customers/owner.go apps/server/internal/customers/customers.go \
 apps/server/internal/customers/customers_test.go \
 apps/server/internal/customers/timeline_events.go \
 apps/server/internal/customers/queries/customers.sql \
 apps/server/internal/customers/store apps/server/internal/customers/gen \
 apps/server/internal/openapi/openapi_test.go apps/server/internal/openapi/specs/customers.yaml \
 apps/customers/frontend/src/api-schema.d.ts"
git add $PATHS && git commit -F /tmp/claude-1000/msg-groups-3.txt -- $PATHS
git show --stat HEAD && git status --short
```
The same list for both, and named files rather than `internal/customers` wholesale: `git commit -- <dir>` would commit whatever else happens to be dirty under it.

---

### Task 4: The default is the third resolution tier (D4)

`resolveBillingProfile` resolves `PaymentTermsDays` as **the profile's own value, else the customer's group's default, else nil** — the first field in this module with a third tier, in the same function and the same docs table every other rule lives in. The billing profile's GET **and PUT** answer `groupDefault?`, so a card can explain an inherited term without re-deriving it, and `contracts.CustomerEntry` gains the `Group` seam Products phase 4 will read.

**Files:**
- Modify: `apps/server/internal/contracts/directory.go`, `apps/server/internal/customers/directory.go`, `apps/server/internal/customers/directory_internal_test.go`, `apps/server/internal/customers/directory_test.go`, `apps/server/internal/customers/billing_profile.go`, `apps/server/internal/customers/billing_profile_test.go`, `apps/server/internal/customers/queries/customers.sql`, `openapi/customers.yaml`
- Generated: `store/customers.sql.go`, `internal/openapi/specs/customers.yaml`, `internal/customers/gen/api.gen.go`, each changed `api-schema.d.ts`
- Read first (do not change): `apps/server/internal/customers/directory.go:106-202`, `apps/server/internal/customers/directory_internal_test.go` (all of it — three tests, one of which this task extends), `apps/server/internal/customers/billing_profile.go:32-140`, `docs/customers.md:1810-1841` (the resolution table Task 5 edits)

**Interfaces:**
- Produces (contract): schema `CustomerBillingGroupDefault` (`{group: CustomerGroupRef, paymentTermsDays?: int32}`, `group` required); `groupDefault?` on `CustomerBillingProfile` (additive, optional).
- Produces Go: `contracts.CustomerGroupEntry{ID uuid.UUID; Name string}` and `contracts.CustomerEntry.Group *CustomerGroupEntry`.
- Produces Go (changed signature): `func resolveBillingProfile(id int32, customerNumber int64, name, customerType string, archived bool, identity *legalIdentity, contactEmail *string, profile billingProfile, groupDefaultPaymentTermsDays *int32, invoiceAddress *contracts.CustomerAddressEntry) *contracts.CustomerBillingProfile`; `func billingProfileResponse(p billingProfile, revision int32, warnings []string, lookup *gen.CustomerPeppolLookup, groupDefault *gen.CustomerBillingGroupDefault) gen.CustomerBillingProfile`.
- Consumes: `store.CustomerGroupMembership` (Task 1) in both billing-profile handlers; the new group columns on `DirectoryCustomer`, `DirectoryCustomers` and `DirectoryBillingProfile`.

- [ ] **Step 1: Write the failing unit cases**

In `apps/server/internal/customers/directory_internal_test.go`, add the case the spec names and update the **four** existing calls (lines 26, 68, 81, 87) to pass `nil` for the new argument — `resolveBillingProfile(42, 100042, "Acme AS", "business", false, nil, nil, profile, nil, nil)`. The compiler finds them all; the count is here so nobody stops after three:

```go
// TestResolveBillingProfile_PaymentTermsDays_GroupDefaultIsTheThirdTier pins the
// one field in this module with three resolution levels (customer groups design
// D4): the profile's own value wins, the group's default fills an unset one, and
// with neither the answer is still nil — "not decided here, whoever invoices
// uses its own default", exactly what every consumer already reads nil as.
func TestResolveBillingProfile_PaymentTermsDays_GroupDefaultIsTheThirdTier(t *testing.T) {
	own := int32(14)
	groupDefault := int32(30)

	// Own wins. A customer that negotiated 14 days is not moved to 30 by the
	// group it happens to be in — the group carries a DEFAULT, not a policy.
	got := resolveBillingProfile(1, 1, "Own Terms AS", "business", false, nil, nil,
		billingProfile{PaymentTermsDays: &own}, &groupDefault, nil)
	if got.PaymentTermsDays == nil || *got.PaymentTermsDays != 14 {
		t.Errorf("PaymentTermsDays = %v, want 14: the profile's own value wins", got.PaymentTermsDays)
	}

	// The group fills an unset one.
	got = resolveBillingProfile(1, 1, "Inherits AS", "business", false, nil, nil, billingProfile{}, &groupDefault, nil)
	if got.PaymentTermsDays == nil || *got.PaymentTermsDays != 30 {
		t.Errorf("PaymentTermsDays = %v, want 30: the group's default fills it", got.PaymentTermsDays)
	}

	// Neither: a group with no default of its own and no group at all are the
	// same argument here (both nil) and must be the same answer — nobody
	// decided, which is what every consumer already reads nil as.
	got = resolveBillingProfile(1, 1, "Neither AS", "business", false, nil, nil, billingProfile{}, nil, nil)
	if got.PaymentTermsDays != nil {
		t.Errorf("PaymentTermsDays = %v, want nil", got.PaymentTermsDays)
	}

	// 0 is a decision ("due on receipt"), not an absence — the case a
	// resolution written with a zero check instead of a nil check gets wrong,
	// in both tiers.
	zero := int32(0)
	got = resolveBillingProfile(1, 1, "Receipt AS", "business", false, nil, nil,
		billingProfile{PaymentTermsDays: &zero}, &groupDefault, nil)
	if got.PaymentTermsDays == nil || *got.PaymentTermsDays != 0 {
		t.Errorf("PaymentTermsDays = %v, want 0: the profile decided 0 and the group must not override it", got.PaymentTermsDays)
	}
	got = resolveBillingProfile(1, 1, "Receipt Group AS", "business", false, nil, nil, billingProfile{}, &zero, nil)
	if got.PaymentTermsDays == nil || *got.PaymentTermsDays != 0 {
		t.Errorf("PaymentTermsDays = %v, want 0: the group decided 0", got.PaymentTermsDays)
	}
}
```

- [ ] **Step 2: Write the failing integration and endpoint tests**

In `apps/server/internal/customers/directory_test.go` (the SQL-backed, external-package file), add:

```go
// TestDirectory_BillingProfileAndCustomersSeeTheGroup is design D4 through the
// real queries: the directory resolves an unset payment term from the group's
// default, and CustomerEntry names the group so Products phase 4 can resolve a
// group price without reading a billing profile for it.
func TestDirectory_BillingProfileAndCustomersSeeTheGroup(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	retail := createGroup(t, c, map[string]any{"name": "Retail", "defaultPaymentTermsDays": 30})
	inherits := createCustomer(t, c, "Inherits AS")
	decides := createCustomer(t, c, "Decides AS")
	for _, id := range []int32{inherits.Id, decides.Id} {
		if r := putCustomerGroup(t, c, id, map[string]any{"groupId": retail.Id}); r.Status != http.StatusOK {
			t.Fatalf("group customer %d: status %d body %s", id, r.Status, r.Body)
		}
	}
	if r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/billing-profile", decides.Id),
		map[string]any{"paymentTermsDays": 14}); r.Status != http.StatusOK {
		t.Fatalf("set own terms: status %d body %s", r.Status, r.Body)
	}

	dir := customers.Module().Directory(h.ModuleDeps())
	profile, err := dir.BillingProfile(context.Background(), inherits.Id)
	if err != nil || profile == nil {
		t.Fatalf("BillingProfile(%d) = %v, %v", inherits.Id, profile, err)
	}
	if profile.PaymentTermsDays == nil || *profile.PaymentTermsDays != 30 {
		t.Errorf("PaymentTermsDays = %v, want 30 from the group", profile.PaymentTermsDays)
	}
	own, err := dir.BillingProfile(context.Background(), decides.Id)
	if err != nil || own == nil {
		t.Fatalf("BillingProfile(%d) = %v, %v", decides.Id, own, err)
	}
	if own.PaymentTermsDays == nil || *own.PaymentTermsDays != 14 {
		t.Errorf("PaymentTermsDays = %v, want 14: the customer's own value wins", own.PaymentTermsDays)
	}

	entry, err := dir.Customer(context.Background(), inherits.Id)
	if err != nil || entry == nil {
		t.Fatalf("Customer(%d) = %v, %v", inherits.Id, entry, err)
	}
	if entry.Group == nil || entry.Group.Name != "Retail" {
		t.Errorf("Customer(…).Group = %+v, want Retail", entry.Group)
	}
	entries, err := dir.Customers(context.Background(), []int32{inherits.Id, decides.Id})
	if err != nil || len(entries) != 2 {
		t.Fatalf("Customers(…) = %v, %v", entries, err)
	}
	for _, e := range entries {
		if e.Group == nil || e.Group.ID != entry.Group.ID {
			t.Errorf("Customers(…) entry %d group = %+v, want the same group the single lookup answered", e.ID, e.Group)
		}
	}
}
```
(Use whatever this file already does to build the directory and a `module.Deps` — `mise exec -- grep -n 'Directory(' internal/customers/directory_test.go` — and follow it rather than the sketch above.)

In `apps/server/internal/customers/billing_profile_test.go`, add:

```go
// TestGetBillingProfile_GroupDefault is design D4's client-facing half: the
// profile says what the customer's group would give it, so a card can explain
// an inherited term — and can say "overridden" when the customer decided one —
// without re-deriving anything. Present whenever the customer belongs to a
// group, absent when it belongs to none, and its paymentTermsDays absent when
// the group carries no default.
func TestGetBillingProfile_GroupDefault(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	retail := createGroup(t, c, map[string]any{"name": "Retail", "defaultPaymentTermsDays": 30})
	plain := createGroup(t, c, map[string]any{"name": "No default"})
	member := createCustomer(t, c, "Member AS")
	outsider := createCustomer(t, c, "Outsider AS")

	if got := fetchBillingProfile(t, c, outsider.Id); got.GroupDefault != nil {
		t.Errorf("groupDefault = %+v for a customer in no group, want it absent", got.GroupDefault)
	}

	if r := putCustomerGroup(t, c, member.Id, map[string]any{"groupId": retail.Id}); r.Status != http.StatusOK {
		t.Fatalf("group the customer: status %d body %s", r.Status, r.Body)
	}
	got := fetchBillingProfile(t, c, member.Id)
	if got.GroupDefault == nil || got.GroupDefault.Group.Name != "Retail" {
		t.Fatalf("groupDefault = %+v, want Retail", got.GroupDefault)
	}
	if got.GroupDefault.PaymentTermsDays == nil || *got.GroupDefault.PaymentTermsDays != 30 {
		t.Errorf("groupDefault.paymentTermsDays = %v, want 30", got.GroupDefault.PaymentTermsDays)
	}
	if got.PaymentTermsDays != nil {
		t.Errorf("paymentTermsDays = %v, want it absent: the profile's own field still means \"decided here\"",
			got.PaymentTermsDays)
	}

	// The PUT answers it too, because the card writes the PUT's own body into
	// its cache: without it the sentence would vanish until the next refetch.
	r := putBillingProfile(t, c, member.Id, map[string]any{"paymentTermsDays": 14, "revision": got.Revision})
	if r.Status != http.StatusOK {
		t.Fatalf("set own terms: status %d body %s, want 200", r.Status, r.Body)
	}
	var saved billingProfileJSON
	r.JSON(&saved)
	if saved.GroupDefault == nil || saved.GroupDefault.PaymentTermsDays == nil || *saved.GroupDefault.PaymentTermsDays != 30 {
		t.Errorf("the PUT's groupDefault = %+v, want the group's 30 beside the customer's own 14", saved.GroupDefault)
	}
	if saved.PaymentTermsDays == nil || *saved.PaymentTermsDays != 14 {
		t.Errorf("paymentTermsDays = %v, want 14", saved.PaymentTermsDays)
	}

	// And the PUT's NO-OP path answers it: the same profile resubmitted writes
	// nothing (customers foundation design D5) and returns through a branch of
	// its own, which is exactly the branch a "add it where the response is
	// built" instruction forgets. The card resubmits more often than it changes
	// anything, so this is the common case, not the exotic one.
	noop := putBillingProfile(t, c, member.Id, map[string]any{"paymentTermsDays": 14, "revision": saved.Revision})
	if noop.Status != http.StatusOK {
		t.Fatalf("resubmit the same profile: status %d body %s, want 200", noop.Status, noop.Body)
	}
	var unchanged billingProfileJSON
	noop.JSON(&unchanged)
	if unchanged.Revision != saved.Revision {
		t.Errorf("revision = %d, want %d unchanged: a no-op writes nothing", unchanged.Revision, saved.Revision)
	}
	if unchanged.GroupDefault == nil || unchanged.GroupDefault.Group.Name != "Retail" {
		t.Errorf("the no-op PUT's groupDefault = %+v, want Retail", unchanged.GroupDefault)
	}

	// A group with no default of its own: the block is present (the customer IS
	// in a group) and carries no term, which is what "nothing to inherit" looks
	// like — never a 0.
	if r := putCustomerGroup(t, c, member.Id, map[string]any{"groupId": plain.Id}); r.Status != http.StatusOK {
		t.Fatalf("move the customer: status %d body %s", r.Status, r.Body)
	}
	moved := fetchBillingProfile(t, c, member.Id)
	if moved.GroupDefault == nil || moved.GroupDefault.Group.Name != "No default" {
		t.Fatalf("groupDefault = %+v, want the group with no default", moved.GroupDefault)
	}
	if moved.GroupDefault.PaymentTermsDays != nil {
		t.Errorf("groupDefault.paymentTermsDays = %v, want it absent", moved.GroupDefault.PaymentTermsDays)
	}
	// warnings learn nothing new from a group (design D4).
	if len(moved.Warnings) != len(got.Warnings) {
		t.Errorf("warnings = %v, want the same set a group-less profile raises (%v)", moved.Warnings, got.Warnings)
	}
}
```
Extend that file's `billingProfileJSON` with the field the assertions read (and add `groupDefaultJSON` beside it):

```go
	GroupDefault *groupDefaultJSON `json:"groupDefault"`
```
```go
// groupDefaultJSON decodes CustomerBillingGroupDefault: which group, and what
// it would give this customer (customer groups design D4). A pointer, because
// it is absent for a customer in no group.
type groupDefaultJSON struct {
	Group            groupRefJSON `json:"group"`
	PaymentTermsDays *int32       `json:"paymentTermsDays"`
}
```

- [ ] **Step 3: Run them and watch them fail**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'
mise exec -- go test -count=1 -run 'TestResolveBillingProfile_PaymentTermsDays|TestDirectory_BillingProfileAndCustomersSeeTheGroup|TestGetBillingProfile_GroupDefault' ./internal/customers/
```
Expected: FAIL to build — `resolveBillingProfile` takes nine arguments, not ten, and `contracts.CustomerEntry` has no `Group`. That is the compiler naming every call site this task must visit. After Step 4's contract change the failures become behavioural: `PaymentTermsDays = <nil>, want 30`, `Customer(…).Group = <nil>, want Retail`, `groupDefault = <nil>, want Retail`.

- [ ] **Step 4: The contract, the contracts package and the queries**

`openapi/customers.yaml` — the new schema, and one optional property on `CustomerBillingProfile`:

```yaml
        CustomerBillingGroupDefault:
            description: "What this customer's group would give it (customer groups design D4). Present whenever the customer belongs to a group, so a client can say \"inherits 30 days from Retail\" when the profile's own paymentTermsDays is absent, and \"group default 30 days, overridden\" when it is present. paymentTermsDays is absent when the group carries no default of its own — nothing to inherit, never a 0. The profile's own paymentTermsDays keeps meaning \"decided here\": the effective value is the profile's own, else this one, else nothing, which is the rule contracts.CustomerDirectory.BillingProfile already applies for every consumer."
            properties:
                group:
                    $ref: '#/components/schemas/CustomerGroupRef'
                paymentTermsDays:
                    format: int32
                    nullable: true
                    type: integer
            required:
                - group
            type: object
```
and, inside `CustomerBillingProfile.properties` (where `groupDefault` sorts, after `gln`):

```yaml
                groupDefault:
                    allOf:
                        - $ref: '#/components/schemas/CustomerBillingGroupDefault'
                    description: The customer's group and the payment term it would give it (customer groups design D4). Absent when the customer belongs to no group.
```

`apps/server/internal/contracts/directory.go` — the seam, and the one sentence that says why it is here before its caller:

```go
// CustomerGroupEntry is the group a customer belongs to, as another module may
// reference it: an id and a name, which is what it takes to show the group and
// to look a group-specific decision up by. Products phase 4 (customer-group
// prices) is the intended reader; the id is stable and this module's own, so a
// consumer that resolves a price by group must never read a billing profile for
// it.
type CustomerGroupEntry struct {
	ID   uuid.UUID
	Name string
}
```
```go
type CustomerEntry struct {
	ID       int32
	Name     string
	Archived bool
	// Group is the group the customer belongs to, nil when it belongs to none
	// (customer groups design D4). A customer belongs to at most one.
	Group *CustomerGroupEntry
}
```
(The file gains a `github.com/google/uuid` import. `CustomerEntry` stays comparable, so `directory_test.go`'s `*got != (contracts.CustomerEntry{…})` assertions still compile — a pointer field compares by pointer, and nil == nil.)

`apps/server/internal/customers/queries/customers.sql` — three statements gain a `LEFT JOIN`, and each keeps its `:one`/`:many` shape:

```sql
-- name: DirectoryCustomer :one
SELECT c.id, c.name, c.status = 'archived' AS archived, c.group_id, g.name AS group_name
FROM customers.customers c
LEFT JOIN customers.customer_groups g ON g.id = c.group_id
WHERE c.id = @id;
```
```sql
-- name: DirectoryCustomers :many
SELECT c.id, c.name, c.status = 'archived' AS archived, c.group_id, g.name AS group_name
FROM customers.customers c
LEFT JOIN customers.customer_groups g ON g.id = c.group_id
WHERE c.id = ANY(@ids::int[])
ORDER BY c.id;
```
and `DirectoryBillingProfile` gains one column (and a clause in its comment: "…plus the group's default payment term, which resolveBillingProfile applies as the middle tier of PaymentTermsDays' resolution — a LEFT JOIN rather than a second round trip, since the group is this module's own table and a customer in no group must still answer a row"):

```sql
SELECT c.id, c.customer_number, c.name, c.type, c.status = 'archived' AS archived,
       c.legal_country, c.legal_id, c.legal_name, c.legal_source, c.legal_type, c.email,
       c.invoice_email, c.reminder_email, c.payment_terms_days, c.currency, c.language,
       c.invoice_delivery, c.reminder_delivery, c.peppol_id, c.gln, c.buyer_reference,
       g.default_payment_terms_days AS group_default_payment_terms_days
FROM customers.customers c
LEFT JOIN customers.customer_groups g ON g.id = c.group_id
WHERE c.id = @id;
```
Regenerate and **read** `store/customers.sql.go`: the three row types' new fields must be pointers (`GroupID *uuid.UUID`, `GroupName *string`, `GroupDefaultPaymentTermsDays *int32`). If sqlc did not infer the LEFT JOIN's nullability, follow the generated types and say so in the report.

- [ ] **Step 5: The resolution, in the one place it lives**

`apps/server/internal/customers/directory.go` — the function's doc comment gains the rule (and keeps its existing three bullets):

```go
//   - PaymentTermsDays: the billing profile's own paymentTermsDays, else the
//     customer's GROUP's default (customer groups design D4), else nil. The
//     first field here with a THREE-level chain, and the only one that inherits
//     from a group at all: currency, language and the delivery methods stay
//     "not decided here" until somebody asks for a default. nil out means
//     nobody decided, which is what every consumer already reads nil as — a
//     group with no default of its own and no group at all are the same answer.
```
the signature and the body:

```go
func resolveBillingProfile(id int32, customerNumber int64, name, customerType string, archived bool,
	identity *legalIdentity, contactEmail *string, profile billingProfile, groupDefaultPaymentTermsDays *int32,
	invoiceAddress *contracts.CustomerAddressEntry,
) *contracts.CustomerBillingProfile {
```
```go
	// A nil check, not a zero check: 0 days is "due on receipt", a decision the
	// group must not override.
	paymentTermsDays := profile.PaymentTermsDays
	if paymentTermsDays == nil {
		paymentTermsDays = groupDefaultPaymentTermsDays
	}
```
```go
		PaymentTermsDays: paymentTermsDays,
```
`BillingProfile`'s own call passes the new column:

```go
	return resolveBillingProfile(row.ID, row.CustomerNumber, row.Name, row.Type, row.Archived,
		identity, row.Email, profile, row.GroupDefaultPaymentTermsDays, invoiceAddress), nil
```
and `Customer`/`Customers` carry the group (one helper so the two can never disagree):

```go
// groupEntry is a directory row's group, nil when the customer belongs to none.
// Shared by Customer and Customers so the single lookup and the batch can never
// answer differently about the same customer.
func groupEntry(id *uuid.UUID, name *string) *contracts.CustomerGroupEntry {
	if id == nil {
		return nil
	}
	return &contracts.CustomerGroupEntry{ID: *id, Name: deref(name)}
}
```
```go
	return &contracts.CustomerEntry{ID: row.ID, Name: row.Name, Archived: row.Archived,
		Group: groupEntry(row.GroupID, row.GroupName)}, nil
```
```go
		entries = append(entries, contracts.CustomerEntry{ID: row.ID, Name: row.Name, Archived: row.Archived,
			Group: groupEntry(row.GroupID, row.GroupName)})
```

`apps/server/internal/customers/billing_profile.go` — `billingProfileResponse` takes the block, and both handlers read it:

```go
// groupDefault is the customer's group and the term it would give it (customer
// groups design D4) — nil for a customer in no group. Answered by the PUT as
// well as the GET: the card writes the PUT's own body into its cache, so a
// profile that came back without it would lose the inherited-term sentence
// until the next refetch.
func billingProfileResponse(p billingProfile, revision int32, warnings []string, lookup *gen.CustomerPeppolLookup, groupDefault *gen.CustomerBillingGroupDefault) gen.CustomerBillingProfile {
	return gen.CustomerBillingProfile{
		InvoiceEmail: p.InvoiceEmail, ReminderEmail: p.ReminderEmail, PaymentTermsDays: p.PaymentTermsDays,
		Currency: p.Currency, Language: p.Language, InvoiceDelivery: p.InvoiceDelivery, ReminderDelivery: p.ReminderDelivery,
		PeppolId: p.PeppolID, Gln: p.Gln, BuyerReference: p.BuyerReference,
		Revision: revision, Warnings: warnings, PeppolLookup: lookup, GroupDefault: groupDefault,
	}
}

// customerGroupDefault is GET/PUT .../billing-profile's groupDefault: one query
// (CustomerGroupMembership, queries/groups.sql — the same one PUT
// /customers/{id}/group reads its no-op check from) turned into the contract's
// block, nil when the customer belongs to no group. The group's own default may
// itself be absent: the block still stands, because "you are in Retail and
// Retail decides nothing" is a different thing to show than "you are in no
// group".
func (s *server) customerGroupDefault(ctx context.Context, q *store.Queries, customerID int32) (*gen.CustomerBillingGroupDefault, error) {
	row, err := q.CustomerGroupMembership(ctx, customerID)
	if err != nil {
		return nil, fmt.Errorf("customers: read customer group membership: %w", err)
	}
	if row.GroupID == nil {
		return nil, nil
	}
	return &gen.CustomerBillingGroupDefault{
		Group:            gen.CustomerGroupRef{Id: *row.GroupID, Name: deref(row.GroupName)},
		PaymentTermsDays: row.DefaultPaymentTermsDays,
	}, nil
}
```
In `GetCustomersByIdBillingProfile`, after the `lookup` resolution:

```go
	groupDefault, err := s.customerGroupDefault(ctx, q, req.Id)
	if err != nil {
		return nil, err
	}

	warnings := billingWarnings(profile, row.Type, identity, row.Email, hasInvoiceAddress, lookup)
	return gen.GetCustomersByIdBillingProfile200JSONResponse(billingProfileResponse(profile, row.Revision, warnings, lookup, groupDefault)), nil
```
`PutCustomersByIdBillingProfile` builds a `CustomerBillingProfile` in exactly **two** places, and both need it — the 400, 404, 409 and error returns build no profile at all:

1. the **no-op** return inside `if billingProfileEqual(before, after) { … }` (`billing_profile.go:~210`), which answers `billingProfileResponse(before, existing.Revision, warnings, lookup)`;
2. the **written** return at the end of the handler, after the transaction commits.

Resolve `groupDefault` once, on the pool, **before** the no-op branch — it is a read of the group, which neither branch changes, and putting it there means one call site rather than two:

```go
	groupDefault, err := s.customerGroupDefault(ctx, q, req.Id)
	if err != nil {
		return nil, err
	}
```
then pass `groupDefault` to both `billingProfileResponse` calls. `customerGroupDefault` runs on the **pool**, never inside `db.WithTx` — it is a read, and this module opens no transaction for a read.

`row.GroupID`/`row.GroupName` here are Task 1 Step 7's gate again: `customerGroupDefault`'s `if row.GroupID == nil` and `deref(row.GroupName)` want `*uuid.UUID`/`*string`. If the generated row disagrees, fix `CustomerGroupMembership` in `queries/groups.sql`, not this function.

- [ ] **Step 6: Run everything, then grep every consumer**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- go generate ./... && mise exec -- gofmt -l internal && mise exec -- go vet ./...
mise exec -- grep -rn 'CustomerEntry{' internal/ | grep -v '_test.go'
mise exec -- go test -count=1 ./internal/customers/ ./internal/contracts/... ./internal/projects/ ./internal/energy/ ./internal/communications/ ./internal/integration/ ./internal/products/ ./internal/module/
cd /home/anders/projects/vantigo/vantigo && mise exec -- bun run gen:client
```
`contracts.CustomerEntry` gains a field, so every fake that builds one (`internal/projects/harness_test.go`, `projects_list_internal_test.go`, `internal/energy/harness_test.go`, `internal/communications/harness_test.go`, `internal/integration/harness_test.go`, `internal/modtest`) still compiles untouched — a struct literal with named fields does. **Change none of them**: a fake that answers `Group: nil` is a fake for a consumer that does not read the group, which is every consumer today. If any of them uses a positional literal, the compiler says so and that one gains `nil`; report it.

- [ ] **Step 7: Show a test can fail, then commit**

Swap `resolveBillingProfile`'s nil check for `if paymentTermsDays == nil || *paymentTermsDays == 0` and watch the "0 is a decision" case go red; restore by hand. Say what it printed.

```bash
cd /home/anders/projects/vantigo/vantigo
cat > /tmp/claude-1000/msg-groups-4.txt <<'EOF'
feat(customers): a group's payment terms are its members' default

resolveBillingProfile gains its first third tier: the billing profile's own
paymentTermsDays, else the customer's group's default, else nil — a nil check
rather than a zero one, because 0 days is "due on receipt" and a decision the
group must not override. GET and PUT .../billing-profile answer groupDefault
so a client can explain an inherited term, and say "overridden", without
re-deriving the rule, and contracts.CustomerEntry names the group Products
phase 4 will resolve a price by. No other billing field inherits.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
PATHS="openapi/customers.yaml apps/server/internal/contracts/directory.go \
 apps/server/internal/customers/directory.go apps/server/internal/customers/directory_test.go \
 apps/server/internal/customers/directory_internal_test.go \
 apps/server/internal/customers/billing_profile.go apps/server/internal/customers/billing_profile_test.go \
 apps/server/internal/customers/queries/customers.sql \
 apps/server/internal/customers/store apps/server/internal/customers/gen \
 apps/server/internal/openapi/specs/customers.yaml apps/customers/frontend/src/api-schema.d.ts"
git add $PATHS && git commit -F /tmp/claude-1000/msg-groups-4.txt -- $PATHS
git show --stat HEAD && git status --short
```
One list for `add` and `commit`, and named files rather than `internal/customers` wholesale — this task touches six of its files and nothing else under it should ride along.

---

### Task 5: Documentation (D1–D6)

No code. `docs/customers.md` gains a **Groups** section after Owner and tags, and four existing places learn about groups; `ROADMAP.md`'s phase 4 gains delivery D and loses groups from "still ahead". Everything here must be true of the code that is now on the branch — read the handlers again rather than the plan.

It lands **before** Tasks 6–7, so this one commit documents a Group filter, a card select and a billing sentence that arrive two commits later. That is deliberate: the docs' own subject is the module's behaviour, the backend of which is complete here, and writing the frontend bullets twice (once as "not built yet") would be worse. A reviewer reading commit by commit should know; say so in the PR if it comes up.

**Files:**
- Modify: `docs/customers.md`, `ROADMAP.md`
- Read first (do not change): `docs/customers.md:517-621` (Owner and tags — the section this one follows and imitates), `440-517` (the billing profile's field table), `1694-1767` (permissions), `1810-1841` (the directory's resolution table), `1841-1989` (the frontend bullets), `1989-2042` (the API list), `2042-2118` (What comes next), `ROADMAP.md:139-215`

- [ ] **Step 1: The Groups section**

In `docs/customers.md`, after the "Owner and tags" section's last paragraph (the "Search does not match owner names or tag names…" one) and before `## The timeline`, add:

```markdown
## Groups

A **group** is a named bucket an installation defines — "Retail", "Key accounts",
"Public sector" — that a customer belongs to at most one of, and that carries a
**default payment term** every member inherits. Two halves, and they copy
opposite precedents on purpose: the vocabulary is the [tags'](#tags), and the
membership is [the owner's](#the-owner).

### The vocabulary

`customers.customer_groups` (migration `00027`) is the tag vocabulary's shape
down to the unique index on `lower(name)`: a group is a vocabulary word, so
`Retail` and `retail` are the same word, and a name is NFC-normalised before it
is stored or compared. It is **unpaged** for the same reason and with the same
bet — `GET /customers/groups` answers every group, name-ascending, each with a
`customerCount`, because the picker and the delete confirmation read the one
list and a vocabulary stays in the tens.

What it deliberately does not copy: no colour and no description. A group is a
policy object, not a label. Its second field is a **default payment term**,
validated 0–365 by the billing profile's own rule (`validatePaymentTermsDays`)
and `CHECK`ed on the column too, because a value outside the range would be
inherited by every member. `PUT /customers/groups/{groupId}` is a **full
replace** of both fields, so a body without `defaultPaymentTermsDays` **clears**
the group's default rather than leaving the one it had.

**A group with members is never deleted.** `DELETE /customers/groups/{groupId}`
counts first and answers **409 `group_in_use`** with that count in the detail;
the members are moved out first, and the list's own `groupId` filter is how they
are found. The column's foreign key is `ON DELETE RESTRICT` rather than
`SET NULL`, so a writer that races the count cannot get past it either. The tags'
cascade is right for a label — a tag going away says nothing about the customer —
and wrong for a default: detaching the members would change every one of their
effective payment terms with no record on any customer.

The vocabulary's own writes record **no timeline event**, the tags' rule (the
group is the vocabulary, not the customer), and that includes moving a group's
default: doing so changes what every member inherits, at once, by design.

### The membership

`customers.customers.group_id` is a nullable column on the customer row with an
in-module foreign key — unlike `owner_user_id`, which points across a module
boundary at identity's users and therefore has none. Being on the row is the
decision everything else follows from: the group **shares the row's `revision`**,
so `PUT /customers/{id}/group` is an ordinary guarded write that a concurrent
edit cannot lose, and it is the owner's PUT step for step — (1) the customer's
404; (2) a stale `revision`, 409, ahead of the no-op check, so resubmitting the
current group with a stale revision is still a conflict; (3) the no-op, nil-safe,
which writes nothing at all and resolves no actor; (4) an unknown `groupId`, a
400 field error on `groupId` rather than a 404, because the customer exists and
the caller may edit it; (5) the guarded write and `customer.group_changed` in one
transaction.

**Only this sub-resource sets a customer's group.** `POST /customers` and
`PUT /customers/{id}` do not take a `groupId` — the owner's own rule.

`SafeCustomerResponse.group` is `{id, name}`, absent when the customer belongs to
no group and never null, beside `owner`. It is resolved by `customerDecoration`
in one batched query per response, exactly as the tags are, rather than selected
by every customer query: one shape of group-on-a-response, one place it can be
wrong. `GET /customers?groupId=<uuid>|none` filters on the column — an equality,
or an `IS NULL` for `none`, in both `CountCustomers`' and `ListCustomers`' WHERE.
An id no group holds simply matches nothing. `groupId=none` is a scan, migration
`00024`'s own bet restated: the partial index serves the members, and "customers
in no group" is a tidying-up sweep rather than a daily filter.

`customer.group_changed` carries `{customerId, before: {groupId, name}|null,
after: {groupId, name}|null}` with the name **snapshotted**, so renaming a group
later never rewrites what the timeline says happened, and reads as "Moved to
group Retail", "Moved from Retail to Key accounts" or "Removed from group
Retail".

### The default, and where it is applied

A group's `defaultPaymentTermsDays` is resolved in exactly one place,
`resolveBillingProfile` (`customers/directory.go`): **the billing profile's own
`paymentTermsDays`, else the customer's group's default, else nothing** — the
first field in this module with a third tier, and the only one that inherits at
all. Currency, language and the delivery methods stay "not decided here" until
somebody asks for a default. The check is a nil check, not a zero one: `0` days
is "due on receipt", a decision a group must not override.

`GET`/`PUT /customers/{id}/billing-profile` answer
`groupDefault: {group: {id, name}, paymentTermsDays?}` whenever the customer
belongs to a group, so a client can say "inherits 30 days from Retail" when the
profile's own value is absent and "group default 30 days — overridden here" when
it is not. The profile's own `paymentTermsDays` keeps meaning **decided here**,
and `warnings` learn nothing new. `groupDefault.paymentTermsDays` is itself
absent when the group carries no default: being in a group that decides nothing
is a different thing to show than being in no group.

`contracts.CustomerEntry.Group` (`{ID, Name}`, nil when none) is answered by both
`Customer` and `Customers`. It has no reader yet — Products phase 4's
customer-group prices are the intended one — and it is here now because it is one
`LEFT JOIN` and one field, and because a consumer resolving a price by group must
never have to read a billing profile for it.

**No new permission key.** Reading the vocabulary is `customers:view`; creating,
editing and deleting a group, and moving a customer between groups, is
`customers:update` (plus `customers:view` for the membership PUT, as the owner's).
A group's name and default are installation policy — the same reasoning that put
the tag vocabulary on `customers:update` — and a customer's own override stays
where it is, behind `customers:billing-manage`. A `customers:view` holder
therefore learns a member's *inherited* term from the vocabulary list, which is a
policy fact rather than a negotiated one.

**Not built:** group-level prices (Products phase 4 reads `CustomerEntry.Group`
when it comes), any default beyond payment terms, bulk moves or a
move-on-delete, a group on the create form, hierarchy, paging the vocabulary, and
a group column in the list table — the filter and the relationship card carry it.
```

- [ ] **Step 2: The four existing places**

1. The billing profile's field table (line ~449): the `paymentTermsDays` row becomes

```markdown
| `paymentTermsDays` | Integer, 0–365 inclusive. NULL means "not decided here" — and, from [Groups](#groups) on, a customer whose group carries a default inherits that instead; `groupDefault` on this sub-resource says which group and what it gives, and the effective value is the profile's own, else the group's, else nothing. |
```

2. The directory's resolution table (line ~1825): move `PaymentTermsDays` out of the pass-through row into its own, above it:

```markdown
| `PaymentTermsDays` | The billing profile's own `paymentTermsDays`, else the customer's **group's** `defaultPaymentTermsDays` ([Groups](#groups)), else `nil`. The only field here with three levels, and the only one that inherits from a group: a nil check, not a zero one, so a profile (or a group) that decided `0` days — due on receipt — is not overridden by the next tier. |
| `Currency`, `Language`, `InvoiceDelivery`, `ReminderDelivery`, `GLN`, `BuyerReference` | The billing profile's own value, `nil`/`""` if never set — no further resolution. |
```
and add, to the same section's "Today's consumers" paragraph, one sentence: "`CustomerEntry.Group` is the other seam built ahead of its caller: Products phase 4's customer-group prices are the intended reader."

3. The permissions section (after the Follow-ups paragraph, line ~1758): add

```markdown
No new permission key was added for [Groups](#groups) either, and the question
was closer here than for owner and tags: a group carries a **billing** default,
which is the one kind of data this module already treats as separately sensitive
(`customers:billing-manage`). It rides on `customers:view`/`customers:update`
anyway, because a group's name and its default are installation policy rather
than one customer's negotiated terms — the same reasoning that put the tag
vocabulary on `customers:update` — and because gating one field of a
`customers:update` endpoint behind `billing-manage` would be a per-field
permission this module has never had. A customer's own override stays behind
`customers:billing-manage`, where it was.
```

4. The frontend bullets (List ~1846, Relationship card ~1875) and the API list (~1996). The List bullet gains: "[Groups](#groups) added a **Group** filter — **All**, **No group**, then each group, reflected in the URL as `groupId` — with a **Manage groups** button beside it, opening the vocabulary editor for a caller the host says may edit." (The spec's D5 writes that first row as "All groups"; the code's word is **All**, which is what the Status, Type, Owner and Tag filters beside it already say (`customers.index.tsx`'s `t("all")`), and consistency across five filters beats the spec's phrasing for one. Name the deviation in Task 8's report.) The Relationship card bullet gains: "and a **Group** `Select` beside the owner — the vocabulary's names with a *No group* row, saved through `PUT /customers/{id}/group` with the card's own revision handling, so a stale revision raises the same conflict-and-Reload alert the owner's save does." The Billing card bullet gains: "Under the payment-terms row it says where an unset term comes from — *Inherits 30 days from Retail* — or that the customer's own overrides one, from the profile's `groupDefault`." The API list's count becomes **55 operations** and gains three rows:

```markdown
| `GET /groups` | `customers:view` |
| `POST /groups`, `PUT /groups/{groupId}`, `DELETE /groups/{groupId}` | `customers:update` |
| `PUT /{id}/group` | `customers:update` + `customers:view` |
```

- [ ] **Step 3: ROADMAP**

`ROADMAP.md`: the phase 4 heading becomes `### Phase 4 — Light CRM (four deliveries done)`, and after delivery C's paragraph add:

```markdown
**Delivery D (done)** — decided in
[`docs/superpowers/specs/2026-09-23-customers-groups-design.md`](docs/superpowers/specs/2026-09-23-customers-groups-design.md):
**customer groups that carry defaults** (migration `00027`). The vocabulary is
the tags' — unique on `lower(name)`, unpaged, with a member count — and the
membership is the owner's: one nullable column on the customer row, sharing its
revision, written only through `PUT /customers/{id}/group` and recorded as
`customer.group_changed` with the group names as they read at the time. A group's
`defaultPaymentTermsDays` is the **third resolution tier** for a customer's
payment term (own value, else the group's, else nothing), applied in the one
place resolution lives, and the billing profile answers `groupDefault` so a card
can explain an inherited term without re-deriving the rule. A group with members
is never deleted — 409 `group_in_use`, with the count, and `ON DELETE RESTRICT`
under it — because detaching them would change every member's effective payment
term with no record on any customer. No new permission key, and
`contracts.CustomerEntry.Group` is the seam Products phase 4's customer-group
prices will read. See [`docs/customers.md#groups`](docs/customers.md#groups).
```
Then, in "**Still ahead in this phase:**", drop "customer groups that can carry defaults;" and add to the leftovers: "Groups have their own: no **bulk move** (a group's members are moved one at a time, which is why the delete is refused rather than cascading), no group-level prices until Products phase 4 reads the seam, no default beyond payment terms, and the vocabulary is unpaged on the same bet the tags' is."

Also update the Products phase 4 line (`ROADMAP.md:365-368`) to name the seam that now exists: "…customer-group prices (the group itself is `contracts.CustomerEntry.Group`, delivered by Customers phase 4 delivery D) and quantity breaks…".

- [ ] **Step 4: Check the links and commit**

```bash
cd /home/anders/projects/vantigo/vantigo
mise exec -- grep -n '#groups\|#the-membership\|55 operations' docs/customers.md ROADMAP.md
mise exec -- grep -c 'operationId:' openapi/customers.yaml   # must be 55
cat > /tmp/claude-1000/msg-groups-5.txt <<'EOF'
docs(customers): customer groups

A Groups section after Owner and tags — the vocabulary, the membership, the
group_in_use rule, the inheritance rule and where it is implemented — plus the
billing profile's paymentTermsDays row, the directory's resolution table (that
field now has a rule of its own), the permission table's "no new key" note and
why the question was closer here, the frontend bullets and the API list.
ROADMAP gains delivery D and loses groups from what is still ahead.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
PATHS="docs/customers.md ROADMAP.md"
git add $PATHS && git commit -F /tmp/claude-1000/msg-groups-5.txt -- $PATHS
git show --stat HEAD && git status --short
```

---

### Task 6: The frontend — a Group select, a Manage groups modal and a Group filter (D5)

**Files:**
- Create: `apps/customers/frontend/src/api/groups.ts`, `apps/customers/frontend/src/pages/-manage-groups-modal.tsx`, `apps/customers/frontend/src/pages/-manage-groups-modal.test.tsx`
- Modify: `apps/customers/frontend/src/api/customers.ts`, `apps/customers/frontend/src/pages/-customer-relationship-card.tsx`, `-customer-relationship-card.test.tsx`, `pages/customers.index.tsx`, `pages/-customers.index.test.tsx`, `src/i18n.ts`, `apps/host/frontend/src/routes/customers/index.tsx`, `apps/host/frontend/src/routes/customers/index.test.ts`
- Read first (do not change): `src/api/tags.ts` and `src/api/owner.ts` (this api file's two halves), `pages/-manage-tags-modal.tsx` (the modal's shape), `pages/-customer-relationship-card.tsx:54-153` (the owner's mutation, revision handling and conflict alert — the group's copies it), `pages/customers.index.tsx:60-72, 129-146, 265-295` (the sentinel pattern, `filterBy`, the Tag filter and its Manage button)

**Interfaces:**
- Produces TS: `interface CustomerGroup { id: string; name: string; defaultPaymentTermsDays: number | null }`; `interface CustomerGroupSummary extends CustomerGroup { customerCount: number }`; `customerGroupsQueryOptions()` on key `["customers", "groups"]`; `interface GroupInput { name: string; defaultPaymentTermsDays: number | null }`; `createGroup`, `updateGroup(id, input)`, `deleteGroup(id)`; `setCustomerGroup(id: number, groupId: string | null, revision?: number): Promise<CustomerResponse>`; `interface CustomerGroupRef { id: string; name: string }` and `CustomerResponse.group: CustomerGroupRef | null` (in `api/customers.ts`, beside `CustomerOwner`); `CustomersListSearch.groupId?: string`; `ManageGroupsModal` (`{opened, onClose, onGroupDeleted?}`).
- Consumes: Task 2 and Task 3's endpoints; `syncCustomerRevision`, `invalidateCustomersExcept`, `useCustomerReload`, `ApiConflictError`, `ApiValidationError`.

- [ ] **Step 1: Write the failing tests**

In `-customer-relationship-card.test.tsx`: add the group to the wire fixtures — `customerBody` still has **no** `group` key (the shape a component must survive), `ownedBody` gains `group: { id: "g1", name: "Retail" }` — teach `stubFetch` two routes, and add three cases:

```tsx
    if (init?.method === "PUT" && url.endsWith("/group"))
      return Promise.resolve(jsonResponse({ ...ownedBody, group: { id: "g2", name: "Key accounts" }, revision: 4 }));
    if (url === "/api/v1/customers/groups") return Promise.resolve(jsonResponse(groupRows));
```
```tsx
const groupRows = [
  { id: "g1", name: "Retail", defaultPaymentTermsDays: 30, customerCount: 2 },
  { id: "g2", name: "Key accounts", customerCount: 0 },
];
```
```tsx
  it("shows no group for a customer that belongs to none, and offers the vocabulary to a caller who may edit", async () => {
    stubFetch();
    renderCard({ canEdit: true });
    // The wire body has no `group` key at all: absent, not null (design D3) — and
    // a Mantine Select's input renders the LABEL of the option matching its
    // value, so an empty value with a "No group" row reads as "No group", never
    // as "" (the owner picker's own assertion, :202 in this file).
    await waitFor(() => expect(screen.getByRole("combobox", { name: "Group" })).toHaveValue("No group"));
    await userEvent.click(screen.getByRole("combobox", { name: "Group" }));
    expect(await screen.findByRole("option", { name: "Retail" })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "No group" })).toBeInTheDocument();
  });

  it("saves a group through its own PUT and takes the answered customer into the cache", async () => {
    const fetchMock = stubFetch({ customer: ownedBody });
    const queryClient = renderCard({ canEdit: true });
    await waitFor(() => expect(screen.getByRole("combobox", { name: "Group" })).toHaveValue("Retail"));
    await userEvent.click(screen.getByRole("combobox", { name: "Group" }));
    await userEvent.click(await screen.findByRole("option", { name: "Key accounts" }));

    await waitFor(() => expect(putsTo(fetchMock, "/api/v1/customers/1001/group")).toHaveLength(1));
    const [, init] = putsTo(fetchMock, "/api/v1/customers/1001/group")[0];
    // The revision is read off the query, never a private copy: a sibling
    // editor's save moves it under this card between renders.
    expect(JSON.parse(String((init as RequestInit).body))).toEqual({ groupId: "g2", revision: 3 });
    await waitFor(() =>
      expect((queryClient.getQueryData(["customers", 1001]) as { group: { name: string } }).group.name).toBe(
        "Key accounts",
      ),
    );
  });

  it("raises the conflict alert when the group save loses a revision race", async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (init?.method === "PUT" && url.endsWith("/group"))
        return Promise.resolve(jsonResponse({ title: "Customer revision conflict" }, 409));
      if (url === "/api/v1/customers/groups") return Promise.resolve(jsonResponse(groupRows));
      if (url === "/api/v1/customers/tags") return Promise.resolve(jsonResponse(tagRows));
      return Promise.resolve(jsonResponse(ownedBody));
    });
    vi.stubGlobal("fetch", fetchMock);
    renderCard({ canEdit: true });
    await waitFor(() => expect(screen.getByRole("combobox", { name: "Group" })).toHaveValue("Retail"));
    await userEvent.click(screen.getByRole("combobox", { name: "Group" }));
    await userEvent.click(await screen.findByRole("option", { name: "No group" }));
    expect(await screen.findByText("This customer was changed somewhere else")).toBeInTheDocument();
  });
```
(Take the conflict alert's exact text from `i18n.ts`'s `customerChangedTitle`.)

Create `-manage-groups-modal.test.tsx` — the tags' modal test plus the two things that modal has no equivalent of, a create form and a delete that is refused:

```tsx
import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ManageGroupsModal } from "./-manage-groups-modal";

const jsonResponse = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": status >= 400 ? "application/problem+json" : "application/json" },
  });

// Literally the wire shapes: Key accounts has no defaultPaymentTermsDays key at
// all, because the server omits an unset optional field rather than sending null.
const groupRows = [
  { id: "g1", name: "Retail", defaultPaymentTermsDays: 30, customerCount: 2 },
  { id: "g2", name: "Key accounts", customerCount: 0 },
];

const stubFetch = (options: { createConflict?: boolean } = {}) => {
  const fetchMock = vi.fn((_input: RequestInfo | URL, init?: RequestInit) => {
    if (init?.method === "DELETE") return Promise.resolve(new Response(null, { status: 204 }));
    if (init?.method === "POST") {
      return Promise.resolve(
        options.createConflict
          ? jsonResponse({ title: "Customer group already exists", code: "group_exists" }, 409)
          : jsonResponse({ id: "g3", name: "Public sector", defaultPaymentTermsDays: 45, customerCount: 0 }, 201),
      );
    }
    if (init?.method === "PUT")
      return Promise.resolve(jsonResponse({ id: "g1", name: "Retail chains", customerCount: 2 }));
    return Promise.resolve(jsonResponse(groupRows));
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
};

const renderModal = () =>
  render(
    <MantineProvider env="test">
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <ManageGroupsModal opened onClose={() => {}} />
      </QueryClientProvider>
    </MantineProvider>,
  );

describe("ManageGroupsModal", () => {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it("lists every group with its default and its member count", async () => {
    stubFetch();
    renderModal();
    await screen.findByText("Retail");
    expect(screen.getByText("30 days")).toBeInTheDocument();
    expect(screen.getByText("2 customers")).toBeInTheDocument();
    // A group with no default of its own says so rather than showing a 0.
    expect(screen.getByText("No default")).toBeInTheDocument();
    expect(screen.getByText("No customers")).toBeInTheDocument();
  });

  it("creates a group through its own POST", async () => {
    const fetchMock = stubFetch();
    renderModal();
    await screen.findByText("Retail");
    await userEvent.type(screen.getByRole("textbox", { name: "Group name" }), "Public sector");
    await userEvent.type(screen.getByRole("textbox", { name: "Default payment terms (days)" }), "45");
    await userEvent.click(screen.getByRole("button", { name: "Create group" }));

    await waitFor(() => {
      const posts = fetchMock.mock.calls.filter(([, init]) => (init as RequestInit)?.method === "POST");
      expect(posts).toHaveLength(1);
      expect(JSON.parse(String((posts[0][1] as RequestInit).body))).toEqual({
        name: "Public sector",
        defaultPaymentTermsDays: 45,
      });
    });
  });

  it("puts the 409 under the name field rather than in a notification", async () => {
    stubFetch({ createConflict: true });
    renderModal();
    await screen.findByText("Retail");
    await userEvent.type(screen.getByRole("textbox", { name: "Group name" }), "retail");
    await userEvent.click(screen.getByRole("button", { name: "Create group" }));
    expect(await screen.findByText("Another group already has that name.")).toBeInTheDocument();
  });

  it("renames a group and sends both fields, so an emptied default clears it", async () => {
    const fetchMock = stubFetch();
    renderModal();
    await userEvent.click(await screen.findByRole("button", { name: "Edit Retail" }));
    const name = screen.getByRole("textbox", { name: "Group name" });
    await userEvent.clear(name);
    await userEvent.type(name, "Retail chains");
    await userEvent.clear(screen.getByRole("textbox", { name: "Default payment terms (days)" }));
    await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

    await waitFor(() => {
      const puts = fetchMock.mock.calls.filter(([, init]) => (init as RequestInit)?.method === "PUT");
      expect(puts).toHaveLength(1);
      // A full replace: the emptied field is null, not omitted-and-kept.
      expect(JSON.parse(String((puts[0][1] as RequestInit).body))).toEqual({
        name: "Retail chains",
        defaultPaymentTermsDays: null,
      });
    });
  });

  it("refuses to delete a group with members, and says why, without asking the server", async () => {
    const fetchMock = stubFetch();
    renderModal();
    await screen.findByText("Retail");
    // The server would answer 409 group_in_use; the modal already knows the
    // count, so it does not offer the click at all.
    expect(screen.getByRole("button", { name: "Delete Retail" })).toBeDisabled();
    expect(screen.getByText("2 customers belong to Retail. Move them out before deleting it.")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: "Delete Key accounts" }));
    await userEvent.click(screen.getByRole("button", { name: "Delete group" }));
    await waitFor(() =>
      expect(fetchMock.mock.calls.filter(([, init]) => (init as RequestInit)?.method === "DELETE")).toHaveLength(1),
    );
  });
});
```

In `-customers.index.test.tsx`: add the same `groupRows` fixture beside that file's own `tagRows`, teach `stubFetch` `if (url.startsWith("/api/v1/customers/groups")) return Promise.resolve(jsonResponse(groupRows));` beside the tags branch (neither prefix contains the other, so order is only for readability), add `groupId: undefined` to the "resets to the first page" test's exact `search` object — it is a `toEqual`, so it goes red without it, which is this file's first red — and add:

```tsx
  it("drives the URL from the Group filter, and offers No group as a filter of its own", async () => {
    stubFetch([ownedRow]);
    renderPage();
    await screen.findByText("Equinor");
    await userEvent.click(screen.getByRole("combobox", { name: "Group" }));
    await userEvent.click(await screen.findByRole("option", { name: "Retail" }));
    expect(router.navigate).toHaveBeenCalledWith(
      expect.objectContaining({ search: expect.objectContaining({ groupId: "g1", page: 1 }) }),
    );

    router.navigate.mockReset();
    await userEvent.click(screen.getByRole("combobox", { name: "Group" }));
    await userEvent.click(await screen.findByRole("option", { name: "No group" }));
    expect(router.navigate).toHaveBeenCalledWith(
      expect.objectContaining({ search: expect.objectContaining({ groupId: "none", page: 1 }) }),
    );
  });

  it("asks the API for the group the URL names, by method and URL rather than by call order", async () => {
    const fetchMock = stubFetch([ownedRow]);
    router.search = { page: 1, search: "", groupId: "g1" };
    renderPage();
    await screen.findByText("Equinor");
    await waitFor(() =>
      expect(
        fetchMock.mock.calls.filter(([u, init]) => String(u).includes("groupId=g1") && !(init as RequestInit)?.method),
      ).not.toHaveLength(0),
    );
  });

  it("opens Manage groups for a caller who may edit, and offers it to nobody else", async () => {
    stubFetch([ownedRow]);
    const { unmount } = renderPage({ canEdit: true });
    await screen.findByText("Equinor");
    await userEvent.click(screen.getByRole("button", { name: "Manage groups" }));
    expect(await screen.findByRole("dialog")).toHaveTextContent("Manage groups");
    unmount();

    stubFetch([ownedRow]);
    renderPage();
    await screen.findByText("Equinor");
    expect(screen.queryByRole("button", { name: "Manage groups" })).not.toBeInTheDocument();
  });
```

In `apps/host/frontend/src/routes/customers/index.test.ts`: add `groupId: undefined` to the three `toEqual` objects and one case:

```ts
  it("keeps a group id and the no-group literal, and drops anything else", () => {
    const groupId = "0191d4f8-6f1a-7c3a-9b2e-6d5f4c3b2a11";
    expect(validate({ groupId })).toMatchObject({ groupId });
    expect(validate({ groupId: "none" })).toMatchObject({ groupId: "none" });
    // 'None' is case-sensitive at the API and a bare word is neither a uuid nor
    // the literal, so the URL never carries either.
    expect(validate({ groupId: "None" })).toMatchObject({ groupId: undefined });
    expect(validate({ groupId: "notauuid" })).toMatchObject({ groupId: undefined });
  });
```

- [ ] **Step 2: Run them and watch them fail**

```bash
cd /home/anders/projects/vantigo/vantigo
mise exec -- bun run --cwd apps/customers/frontend test
mise exec -- bun run --cwd apps/host/frontend test
```
Expected: FAIL — `Cannot find module './-manage-groups-modal'`, `Unable to find an accessible element with the role "combobox" and name "Group"`, and the host's three `toEqual` objects reporting a missing `groupId` key.

- [ ] **Step 3: `api/groups.ts`, and `group` at the customers boundary**

Create `apps/customers/frontend/src/api/groups.ts`:

```ts
import { keepPreviousData, queryOptions } from "@tanstack/react-query";
import { type CustomerResponse, normalizeCustomer } from "./customers";
import { request } from "./request";

/**
 * A group in the installation's vocabulary (customer groups design D2).
 * `defaultPaymentTermsDays` is null when the group decides nothing — the wire
 * omits the field in that case, and absent and null mean the same thing, which
 * is decided here so nothing downstream has to know.
 */
export interface CustomerGroup {
  id: string;
  name: string;
  defaultPaymentTermsDays: number | null;
}

/** A group with how many customers belong to it — what the vocabulary list answers. */
export interface CustomerGroupSummary extends CustomerGroup {
  customerCount: number;
}

type RawCustomerGroupSummary = Omit<CustomerGroupSummary, "defaultPaymentTermsDays"> & {
  defaultPaymentTermsDays?: number | null;
};

const normalizeGroupSummary = (raw: RawCustomerGroupSummary): CustomerGroupSummary => ({
  id: raw.id,
  name: raw.name,
  defaultPaymentTermsDays: raw.defaultPaymentTermsDays ?? null,
  customerCount: raw.customerCount,
});

/**
 * Every group, name-ascending, with its member count. One key for the whole
 * installation — a vocabulary is not paginated and not per-customer — under the
 * `["customers"]` prefix, so every existing broad invalidation refreshes it too.
 */
export const customerGroupsQueryOptions = () =>
  queryOptions({
    queryKey: ["customers", "groups"],
    queryFn: async ({ signal }) =>
      (await request<RawCustomerGroupSummary[]>("/api/v1/customers/groups", { signal })).map(normalizeGroupSummary),
    placeholderData: keepPreviousData,
  });

/**
 * Both writes send BOTH fields, because `PUT` is a full replace: an emptied
 * default clears the group's default rather than leaving the one it had (design
 * D2), and a caller that omitted the field would be asking for the opposite of
 * what the form shows.
 */
export interface GroupInput {
  name: string;
  defaultPaymentTermsDays: number | null;
}

export const createGroup = async (input: GroupInput): Promise<CustomerGroupSummary> =>
  normalizeGroupSummary(
    await request<RawCustomerGroupSummary>("/api/v1/customers/groups", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(input),
    }),
  );

export const updateGroup = async (id: string, input: GroupInput): Promise<CustomerGroupSummary> =>
  normalizeGroupSummary(
    await request<RawCustomerGroupSummary>(`/api/v1/customers/groups/${id}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(input),
    }),
  );

export const deleteGroup = (id: string) => request<void>(`/api/v1/customers/groups/${id}`, { method: "DELETE" });

/**
 * Puts a customer in a group, or takes it out of every group. It answers the
 * whole customer, not just the group, so a caller reads the fresh revision
 * straight off the response — the same shape `setCustomerOwner` has, and the
 * reason `syncCustomerRevision` can run before any invalidation.
 */
export const setCustomerGroup = async (
  id: number,
  groupId: string | null,
  revision?: number,
): Promise<CustomerResponse> =>
  normalizeCustomer(
    await request(`/api/v1/customers/${id}/group`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ groupId, revision }),
    }),
  );
```

In `apps/customers/frontend/src/api/customers.ts`, beside `CustomerOwner` (the reference type lives here, not in `groups.ts`, because `groups.ts` imports `CustomerResponse` and the reverse would be a cycle):

```ts
/**
 * The group a customer belongs to (customer groups design D3) — at most one.
 * The group's own default payment term is not here: the billing card reads it
 * from the profile's `groupDefault`, already resolved, and the Manage groups
 * modal from the vocabulary list.
 */
export interface CustomerGroupRef {
  id: string;
  name: string;
}
```
then, in the same file:

- `group: CustomerGroupRef | null;` on `CustomerResponse`, with `/** Design D3. Null when the customer belongs to no group; the wire omits the field entirely in that case. */`;
- `group?: CustomerGroupRef | null;` on `RawCustomerResponse`, and `"group"` added to its `Omit<…>`;
- `group` destructured in `normalizeCustomer`, with `group: group ?? null` beside `owner: owner ?? null`;
- `groupId?: string;` on **`CustomersQueryParams`** (`customers.ts:112`, beside `tagId`) — without this the line below does not type-check, because `customersListParams` returns that type;
- `if (params.groupId) searchParams.set("groupId", params.groupId);` in **`fetchCustomers`** (`customers.ts:~128`, after the `tagId` line) — that function sets every parameter by hand, so a field nobody adds there is silently dropped and the filter never reaches the API;
- `groupId?: string;` on `CustomersListSearch` and `groupId: search.groupId,` in `customersListParams`.

All six, or the "asks the API for the group the URL names" test cannot pass.

- [ ] **Step 4: The Group select on the Relationship card**

In `-customer-relationship-card.tsx`: import `setCustomerGroup` and `customerGroupsQueryOptions`, extend the file's doc comment ("…and which group it is in — the group is a column too, so its write is the owner's, revision and conflict alert included"), add the mutation beside `ownerMutation`:

```tsx
  // The group's save is the owner's, deliberately: it is a column on the same
  // row, so it carries the revision read off the query (never a private copy —
  // a sibling editor's save moves it under this card), answers the whole
  // customer, and a 409 raises the same conflict-and-Reload alert.
  const groupMutation = useMutation({
    mutationFn: (groupId: string | null) => setCustomerGroup(customerId, groupId, customer.revision),
    onMutate: () => {
      setConflict(false);
      reload.forget();
    },
    onSuccess: (saved) => {
      queryClient.setQueryData(customerKey, saved);
      syncCustomerRevision(queryClient, customerId, saved.revision);
      invalidateCustomersExcept(queryClient, customerKey);
      notifications.show({ color: "teal", title: t("groupUpdated"), message: t("groupUpdatedMessage") });
    },
    onError: (error) => {
      if (error instanceof ApiConflictError && !error.code) {
        setConflict(true);
        return;
      }
      notifications.show({ color: "red", title: t("groupCouldNotBeSaved"), message: error.message });
    },
  });
```
and, after the owner's `OwnerPicker` block and before the `<Divider />` that precedes the tags, the row and its editor:

```tsx
        <Group gap="xs" wrap="nowrap" align="center">
          <Text size="sm" c="dimmed" miw={64}>
            {t("group")}
          </Text>
          <Text size="sm">{customer.group?.name ?? "—"}</Text>
        </Group>
        {canEdit && (
          <GroupSelect
            group={customer.group}
            disabled={groupMutation.isPending}
            onChange={(value) => groupMutation.mutate(value)}
          />
        )}
```
with the select itself at the foot of the file:

```tsx
/**
 * The group picker: the vocabulary's names with a "No group" row, which is the
 * `NO_GROUP` sentinel for the reason every `Select` in this package has one — a
 * Mantine `Select` needs a real string among its `data` to offer a row at all,
 * and "no group" is a choice a person makes rather than a cleared field.
 *
 * The customer's OWN group seeds the option list and the vocabulary widens it,
 * for the tags editor's reason: a `Select` renders the raw value of a selected
 * option its `data` does not describe, so without this the field reads as a uuid
 * until the vocabulary lands, and for good if it fails.
 */
const NO_GROUP = "";

const GroupSelect = ({
  group,
  disabled,
  onChange,
}: {
  group: CustomerGroupRef | null;
  disabled?: boolean;
  onChange: (groupId: string | null) => void;
}) => {
  const { t } = useI18n("customers");
  const { data: vocabulary } = useQuery(customerGroupsQueryOptions());
  const options = new Map(group ? [[group.id, group.name]] : []);
  for (const g of vocabulary ?? []) options.set(g.id, g.name);

  return (
    <Select
      label={t("group")}
      allowDeselect={false}
      data={[{ value: NO_GROUP, label: t("noGroup") }, ...[...options].map(([value, label]) => ({ value, label }))]}
      value={group?.id ?? NO_GROUP}
      disabled={disabled}
      onChange={(value) => onChange(value === NO_GROUP || value === null ? null : value)}
    />
  );
};
```
(`Select` and `CustomerGroupRef` join the imports. `GroupSelect` takes no `customerId`, unlike `TagsEditor`, which needs one for `customerQueryOptions(customerId).queryKey`: the group's mutation lives in the card, so this component only reports a choice.)

- [ ] **Step 5: The Manage groups modal**

Create `apps/customers/frontend/src/pages/-manage-groups-modal.tsx`, in full — the tests above pin its accessible names, its rendered strings and its request bodies, so nothing here is left to taste:

```tsx
import { ActionIcon, Alert, Button, Group, Modal, Stack, Table, Text, TextInput } from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { IconPencil, IconTrash } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { EmptyState, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  type CustomerGroupSummary,
  createGroup,
  customerGroupsQueryOptions,
  deleteGroup,
  updateGroup,
} from "../api/groups";
import { ApiConflictError, ApiValidationError } from "../api/request";
import "../i18n";

/**
 * The group vocabulary's own editor (customer groups design D5): create,
 * rename, re-default and delete, reached from the list page's Group filter and
 * only for a caller the host says may edit (`customers:update`).
 *
 * Two differences from the Manage tags modal it is shaped after, and both are
 * the design's:
 *
 *  - It CREATES. A tag is created from the picker mid-edit, where somebody is
 *    already typing a name; a group carries a default payment term, which is a
 *    decision rather than a word, so it is made here, where the field is.
 *  - A group with members cannot be deleted (design D2), and this list already
 *    knows how many there are: the control is DISABLED with the reason beside it
 *    rather than offering a click the server answers 409 `group_in_use`. The 409
 *    is still handled — somebody can fill a group between this list and the
 *    click — but nobody is invited into it.
 *
 * Every mutation invalidates the broad `["customers"]` prefix rather than only
 * `["customers", "groups"]`: a rename changes what the Group filter, the
 * relationship card and the billing card's inherited-term sentence say, and
 * those live under other keys.
 */
export const ManageGroupsModal = ({
  opened,
  onClose,
  onGroupDeleted,
}: {
  opened: boolean;
  onClose: () => void;
  /** The id of a group that no longer exists, for a caller holding it as a filter. */
  onGroupDeleted?: (groupId: string) => void;
}) => {
  const { t } = useI18n("customers");
  const { data: groups } = useQuery({ ...customerGroupsQueryOptions(), enabled: opened });
  const [editing, setEditing] = useState<CustomerGroupSummary | null>(null);
  const [deleting, setDeleting] = useState<CustomerGroupSummary | null>(null);

  return (
    <Modal opened={opened} onClose={onClose} title={t("manageGroups")} centered>
      <Stack>
        {(groups ?? []).length === 0 && <EmptyState size="sm" title={t("noGroupsYet")} />}
        {(groups ?? []).length > 0 && (
          <Table>
            <Table.Tbody>
              {(groups ?? []).map((group) => (
                <Table.Tr key={group.id}>
                  <Table.Td>
                    <Text size="sm">{group.name}</Text>
                  </Table.Td>
                  <Table.Td>
                    <Text size="sm" c="dimmed">
                      {group.defaultPaymentTermsDays === null
                        ? t("groupNoDefault")
                        : t("paymentTermsDaysValue", { count: group.defaultPaymentTermsDays })}
                    </Text>
                  </Table.Td>
                  <Table.Td>
                    <Text size="sm" c="dimmed">
                      {group.customerCount === 0
                        ? t("groupOnNoCustomers")
                        : t("groupOnCustomers", { count: group.customerCount })}
                    </Text>
                  </Table.Td>
                  <Table.Td w={80}>
                    <Group gap={4} justify="flex-end" wrap="nowrap">
                      <ActionIcon
                        variant="subtle"
                        color="gray"
                        aria-label={t("editNamedGroup", { name: group.name })}
                        onClick={() => setEditing(group)}
                      >
                        <IconPencil size={16} />
                      </ActionIcon>
                      {/* Disabled, not hidden: the reason belongs beside a control
                          somebody can see, and the row below says what it is. */}
                      <ActionIcon
                        variant="subtle"
                        color="red"
                        aria-label={t("deleteNamedGroup", { name: group.name })}
                        disabled={group.customerCount > 0}
                        onClick={() => setDeleting(group)}
                      >
                        <IconTrash size={16} />
                      </ActionIcon>
                    </Group>
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        )}
        {/* The reasons sit under the table rather than in the count cell, so
            "2 customers" and "2 customers belong to Retail…" are two separate
            text nodes: getByText is exact-match, but a nested match would still
            be the kind of thing that breaks when somebody reformats a cell. */}
        {(groups ?? [])
          .filter((group) => group.customerCount > 0)
          .map((group) => (
            <Text key={group.id} size="xs" c="dimmed">
              {t("deleteGroupBlocked", { name: group.name, count: group.customerCount })}
            </Text>
          ))}
        {/* Keyed on the group being edited, so picking another row remounts the
            form on that group's values instead of writing them over the open
            form's — and so leaving edit mode gives a blank CREATE form back. */}
        <GroupForm key={editing?.id ?? "new"} group={editing} onDone={() => setEditing(null)} />
        {deleting && (
          <DeleteGroupConfirmation
            key={deleting.id}
            group={deleting}
            onDeleted={onGroupDeleted}
            onDone={() => setDeleting(null)}
          />
        )}
      </Stack>
    </Modal>
  );
};

interface GroupFormValues {
  name: string;
  /** A string, not a number: "" is unambiguously "no default", which `null` on the wire is. */
  days: string;
}

/**
 * One form for both create and edit — the caller keys it on the group being
 * edited, so there is never a second instance on screen and never two inputs
 * with the same accessible name. `group === null` is the create form, which is
 * the modal's resting state.
 *
 * `days` is a plain `TextInput` rather than Mantine's `NumberInput`: an emptied
 * `NumberInput` yields `""` or `NaN` depending on version, and the one thing
 * this field has to express precisely is "no default at all". It is validated
 * here before any request — a whole number 0-365, the server's own range — so a
 * typo is a message under the field rather than a round trip.
 */
const GroupForm = ({ group, onDone }: { group: CustomerGroupSummary | null; onDone: () => void }) => {
  const { t } = useI18n("customers");
  const queryClient = useQueryClient();
  const form = useForm<GroupFormValues>({
    initialValues: { name: group?.name ?? "", days: group?.defaultPaymentTermsDays?.toString() ?? "" },
  });

  const mutation = useMutation({
    mutationFn: (values: GroupFormValues) => {
      const input = {
        name: values.name.trim(),
        // Both fields, always: PUT is a full replace, so an emptied default has
        // to arrive as null rather than be left out and kept (design D2).
        defaultPaymentTermsDays: values.days.trim() === "" ? null : Number(values.days),
      };
      return group ? updateGroup(group.id, input) : createGroup(input);
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["customers"] });
      form.reset();
      onDone();
      notifications.show({ color: "teal", title: t("groupSaved"), message: t("groupSavedMessage") });
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) {
        // The server keys the term error `defaultPaymentTermsDays`; this form's
        // field is `days`, so the one key that can arrive is mapped onto it and
        // the rest are set as they came.
        const { defaultPaymentTermsDays, ...rest } = error.fieldErrors;
        form.setErrors({ ...rest, ...(defaultPaymentTermsDays ? { days: defaultPaymentTermsDays } : {}) });
        return;
      }
      // Names are unique without regard to case, so this is what creating or
      // renaming to a case variant answers. It is about the one field the person
      // just typed in, so it goes under that input rather than into a
      // notification that would leave the form looking saved.
      if (error instanceof ApiConflictError && error.code === "group_exists") {
        form.setErrors({ name: t("groupNameTaken") });
        return;
      }
      notifications.show({ color: "red", title: t("groupCouldNotBeSaved"), message: error.message });
    },
  });

  return (
    <form
      onSubmit={form.onSubmit((values) => {
        const days = values.days.trim();
        if (days !== "" && !/^\d{1,3}$/.test(days)) {
          form.setErrors({ days: t("groupDefaultPaymentTermsInvalid") });
          return;
        }
        if (days !== "" && Number(days) > 365) {
          form.setErrors({ days: t("groupDefaultPaymentTermsInvalid") });
          return;
        }
        mutation.mutate(values);
      })}
    >
      <Stack gap="xs">
        <TextInput label={t("groupName")} {...form.getInputProps("name")} />
        <TextInput
          label={t("groupDefaultPaymentTerms")}
          placeholder={t("groupNoDefault")}
          {...form.getInputProps("days")}
        />
        <Group justify="flex-end">
          {group && (
            <Button variant="default" onClick={onDone}>
              {t("cancel")}
            </Button>
          )}
          <Button type="submit" loading={mutation.isPending}>
            {group ? t("saveChanges") : t("createGroup")}
          </Button>
        </Group>
      </Stack>
    </form>
  );
};

/**
 * The delete confirmation, which only ever opens for an EMPTY group (the list's
 * own control is disabled otherwise). It still handles the 409: somebody can
 * move a customer in between this list and the click, and the server's own
 * detail already names how many, so the notification passes it straight through
 * rather than rewording it.
 */
const DeleteGroupConfirmation = ({
  group,
  onDeleted,
  onDone,
}: {
  group: CustomerGroupSummary;
  /** Told which group went, so a caller filtering by it can let go (the list page's URL). */
  onDeleted?: (groupId: string) => void;
  onDone: () => void;
}) => {
  const { t } = useI18n("customers");
  const queryClient = useQueryClient();
  const mutation = useMutation({
    mutationFn: () => deleteGroup(group.id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["customers"] });
      onDeleted?.(group.id);
      onDone();
      notifications.show({ color: "teal", title: t("groupDeleted"), message: t("groupDeletedMessage") });
    },
    onError: (error) =>
      notifications.show({ color: "red", title: t("groupCouldNotBeDeleted"), message: error.message }),
  });

  return (
    <Alert color="red" title={t("deleteGroup")}>
      <Stack gap="xs">
        <Text size="sm">{t("deleteGroupConfirmation", { name: group.name })}</Text>
        <Group justify="flex-end">
          <Button size="xs" variant="default" onClick={onDone}>
            {t("cancel")}
          </Button>
          <Button size="xs" color="red" loading={mutation.isPending} onClick={() => mutation.mutate()}>
            {t("deleteGroup")}
          </Button>
        </Group>
      </Stack>
    </Alert>
  );
};
```

- [ ] **Step 6: The list page's Group filter**

In `customers.index.tsx`: `const ALL_GROUPS = "";` beside `ALL_TAGS`; `groupId` in the `useSearch` destructuring, in `listSearch`, and in `filterBy`'s `Pick<…>`; `const { data: groups } = useQuery(customerGroupsQueryOptions());`; `const [manageGroupsOpened, setManageGroupsOpened] = useState(false);` and a `<ManageGroupsModal>` beside `<ManageTagsModal>` with the same `onGroupDeleted` repair (`if (deleted === groupId) filterBy({ groupId: undefined })`). The filter itself goes after the Tag group:

```tsx
            <Group gap="xs" align="end">
              <Select
                label={t("group")}
                w={160}
                allowDeselect={false}
                data={[
                  { value: ALL_GROUPS, label: t("all") },
                  { value: "none", label: t("noGroup") },
                  ...(groups ?? []).map((group) => ({ value: group.id, label: group.name })),
                ]}
                value={groupId ?? ALL_GROUPS}
                onChange={(value) => filterBy({ groupId: value || undefined })}
              />
              {canEdit && (
                <Button variant="subtle" size="sm" onClick={() => setManageGroupsOpened(true)}>
                  {t("manageGroups")}
                </Button>
              )}
            </Group>
```
**No `useEffect` dropping an unknown `groupId`,** unlike the Tag filter's: `none` is a legitimate value the vocabulary will never contain, so the tags' "is this id in the list" repair would throw the No-group filter away on every render. A deleted group cannot be filtered by and still have members, and `onGroupDeleted` covers the deletion this page did see. Say so in a comment where the reader will look for the missing effect.

- [ ] **Step 7: The host route**

In `apps/host/frontend/src/routes/customers/index.tsx`: import `customerGroupsQueryOptions`, add the validator and the parameter, and prefetch the vocabulary the filter reads:

```ts
/**
 * A group filter is a uuid or the literal 'none' (customer groups design D3) —
 * unlike the Owner filter's two literals, because the Group Select really does
 * offer every group by id. Anything else is no filter rather than a 400 from
 * the API.
 */
const groupFilterOf = (value: unknown): string | undefined =>
  value === "none" || (typeof value === "string" && UUID.test(value)) ? value : undefined;
```
```ts
    tagId: tagFilterOf(search.tagId),
    groupId: groupFilterOf(search.groupId),
```
```ts
      queryClient.ensureQueryData(customerTagsQueryOptions()),
      queryClient.ensureQueryData(customerGroupsQueryOptions()),
```

- [ ] **Step 8: Both catalogs**

In `apps/customers/frontend/src/i18n.ts`, after the tag keys in **en**:

```ts
  group: "Group",
  noGroup: "No group",
  groupUpdated: "Group updated",
  groupUpdatedMessage: "The customer's group was saved.",
  groupCouldNotBeSaved: "Group could not be saved",
  manageGroups: "Manage groups",
  noGroupsYet: "No groups yet.",
  groupName: "Group name",
  groupNameTaken: "Another group already has that name.",
  groupDefaultPaymentTerms: "Default payment terms (days)",
  groupDefaultPaymentTermsInvalid: "Give a whole number of days between 0 and 365, or leave it blank.",
  groupNoDefault: "No default",
  groupOnCustomers_one: "{{count}} customer",
  groupOnCustomers_other: "{{count}} customers",
  groupOnNoCustomers: "No customers",
  createGroup: "Create group",
  editNamedGroup: "Edit {{name}}",
  deleteNamedGroup: "Delete {{name}}",
  deleteGroup: "Delete group",
  deleteGroupConfirmation: "No customer belongs to {{name}}, so deleting it affects nobody.",
  deleteGroupBlocked_one: "{{count}} customer belongs to {{name}}. Move them out before deleting it.",
  deleteGroupBlocked_other: "{{count}} customers belong to {{name}}. Move them out before deleting it.",
  groupSaved: "Group saved",
  groupSavedMessage: "The group was saved successfully.",
  groupDeleted: "Group deleted",
  groupDeletedMessage: "The group was deleted.",
  groupCouldNotBeDeleted: "Group could not be deleted",
```
`groupCouldNotBeSaved` is deliberately one key for both surfaces — the card's save and the modal's — the way `tagCouldNotBeSaved` already is. And in **nb**, the same keys in the same order:

```ts
  group: "Gruppe",
  noGroup: "Ingen gruppe",
  groupUpdated: "Gruppe oppdatert",
  groupUpdatedMessage: "Kundens gruppe ble lagret.",
  groupCouldNotBeSaved: "Kunne ikke lagre gruppe",
  manageGroups: "Administrer grupper",
  noGroupsYet: "Ingen grupper ennå.",
  groupName: "Navn på gruppe",
  groupNameTaken: "En annen gruppe har allerede dette navnet.",
  groupDefaultPaymentTerms: "Standard betalingsbetingelser (dager)",
  groupDefaultPaymentTermsInvalid: "Oppgi et helt antall dager mellom 0 og 365, eller la feltet stå tomt.",
  groupNoDefault: "Ingen standard",
  groupOnCustomers_one: "{{count}} kunde",
  groupOnCustomers_other: "{{count}} kunder",
  groupOnNoCustomers: "Ingen kunder",
  createGroup: "Opprett gruppe",
  editNamedGroup: "Rediger {{name}}",
  deleteNamedGroup: "Slett {{name}}",
  deleteGroup: "Slett gruppe",
  deleteGroupConfirmation: "Ingen kunder er i {{name}}, så det påvirker ingen å slette den.",
  deleteGroupBlocked_one: "{{count}} kunde er i {{name}}. Flytt den ut før du sletter gruppen.",
  deleteGroupBlocked_other: "{{count}} kunder er i {{name}}. Flytt dem ut før du sletter gruppen.",
  groupSaved: "Gruppe lagret",
  groupSavedMessage: "Gruppen ble lagret.",
  groupDeleted: "Gruppe slettet",
  groupDeletedMessage: "Gruppen ble slettet.",
  groupCouldNotBeDeleted: "Kunne ikke slette gruppen",
```

- [ ] **Step 9: Run everything, show a test can fail, commit**

```bash
cd /home/anders/projects/vantigo/vantigo
mise exec -- bunx biome check --write apps/customers/frontend/src apps/host/frontend/src/routes/customers
mise exec -- bun run --cwd apps/customers/frontend test
mise exec -- bun run --cwd apps/host/frontend test
mise exec -- bun run translations:check && mise exec -- bun run i18n:test
mise exec -- bun run --cwd apps/customers/frontend typecheck || mise exec -- bun run typecheck
```
Prove two guards: drop `syncCustomerRevision` from `groupMutation.onSuccess` — the revision assertion in the card's save test goes red on the second write; restore by hand. Change the modal's `disabled={group.customerCount > 0}` to `disabled={false}` — the "refuses to delete a group with members" test goes red; restore by hand. Say what each printed.

```bash
cat > /tmp/claude-1000/msg-groups-6.txt <<'EOF'
feat(customers-ui): groups on the relationship card, the list filter and their own modal

The relationship card gets a Group select beside the owner, saved through PUT
/customers/{id}/group with the card's own revision handling and the same
conflict-and-Reload alert the owner's save raises. The list gets a Group filter
(all / no group / each group, reflected in the URL as groupId) with a Manage
groups button for a caller who may edit. The modal is the Manage tags modal
with the two differences the design asks for: it creates, because a group
carries a decision somebody has to type, and it disables the delete with the
reason for a group that still has members instead of offering a click the
server refuses.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
PATHS="apps/customers/frontend/src apps/host/frontend/src/routes/customers"
git add $PATHS && git commit -F /tmp/claude-1000/msg-groups-6.txt -- $PATHS
git show --stat HEAD && git status --short
```
Two directories rather than a file list, deliberately: this task touches nine files across them (three new, six edited) and nothing else in either tree is dirty by now — check `git status --short` **before** committing to be sure of that, because `git commit -- <dir>` takes whatever it finds.

---

### Task 7: The billing card says where an inherited term comes from (D5)

Three states under one field, from one already-resolved block: nothing to say, "Inherits 30 days from Retail", and "Group default 30 days — overridden here". The card derives nothing: `groupDefault` carries the group and its term, and the rule — own value wins — is the directory's own, stated once in the docs.

**Files:**
- Modify: `apps/customers/frontend/src/api/billing-profile.ts`, `apps/customers/frontend/src/pages/-customer-billing-card.tsx`, `-customer-billing-card.test.tsx`, `src/i18n.ts`
- Read first (do not change): `pages/-customer-billing-card.tsx:120-250` (`BillingRow`'s `hint` prop and the three hints already computed the same way), `api/billing-profile.ts` (the boundary normaliser this extends)

**Interfaces:**
- Produces TS: `interface CustomerBillingGroupDefault { group: CustomerGroupRef; paymentTermsDays: number | null }` — `CustomerGroupRef` imported from `./customers`, where Task 6 declares it (this file already imports from there) — and `CustomerBillingProfile.groupDefault: CustomerBillingGroupDefault | null`.

- [ ] **Step 1: Write the failing tests**

In `-customer-billing-card.test.tsx`, add `cleanup` to the `@testing-library/react` import (line 4 has `{ render, screen, waitFor, within }`; this file's `afterEach` only calls `vi.unstubAllGlobals()`, so a test that renders four times must clean up itself), then add the case. **`renderCard`'s first argument IS the profile, positionally** (`:61-64`) — never `{ profile: … }`, which would stub a body with no `revision` and no `warnings` and throw before any assertion — and the file's own fixtures `emptyProfile` (`:30-43`) and `omittedProfile` (`:52`, literally the wire body for a customer that has decided nothing) are what to build on:

```tsx
  it("says where an unset payment term comes from, that an own one overrides it, and nothing when the group decides nothing", async () => {
    // Inherited: the profile decided nothing, the group gives 30.
    renderCard({ ...emptyProfile, groupDefault: { group: { id: "g1", name: "Retail" }, paymentTermsDays: 30 } });
    expect(await screen.findByText("Inherits 30 days from Retail")).toBeInTheDocument();
    cleanup();

    // Overridden: both present, and the card says which one applies.
    renderCard({
      ...emptyProfile,
      paymentTermsDays: 14,
      groupDefault: { group: { id: "g1", name: "Retail" }, paymentTermsDays: 30 },
    });
    expect(await screen.findByText("Group default 30 days — overridden here")).toBeInTheDocument();
    cleanup();

    // In a group that decides nothing: nothing to inherit and nothing to say.
    // The block is still present on the wire, with no paymentTermsDays key at
    // all, which is the trap.
    renderCard({ ...emptyProfile, groupDefault: { group: { id: "g2", name: "Key accounts" } } });
    await screen.findByText("Payment terms");
    expect(screen.queryByText(/Inherits/)).not.toBeInTheDocument();
    expect(screen.queryByText(/Group default/)).not.toBeInTheDocument();
    cleanup();

    // In no group: no groupDefault key at all — the file's own wire fixture,
    // which must not throw.
    renderCard(omittedProfile);
    await screen.findByText("Payment terms");
    expect(screen.queryByText(/Inherits/)).not.toBeInTheDocument();
  });
```

- [ ] **Step 2: Run it and watch it fail**

```bash
cd /home/anders/projects/vantigo/vantigo
mise exec -- bun run --cwd apps/customers/frontend test -- -t "says where an unset payment term"
```
Expected: FAIL — `Unable to find an element with the text: Inherits 30 days from Retail`.

- [ ] **Step 3: The boundary and the sentence**

In `api/billing-profile.ts`:

```ts
/**
 * What this customer's group would give it (customer groups design D4).
 * Present whenever the customer belongs to a group; `paymentTermsDays` is null
 * when the group carries no default of its own, which is a different thing from
 * belonging to no group — one has nothing to inherit, the other has nowhere to
 * inherit from. The effective term is the profile's own, else this one, else
 * nothing: the rule the directory applies for every consumer, restated here
 * only so the card can say which case it is in.
 */
export interface CustomerBillingGroupDefault {
  group: CustomerGroupRef;
  paymentTermsDays: number | null;
}
```
with `import type { CustomerGroupRef } from "./customers";` — the same type the customer's own `group` field carries (Task 6 Step 3), so a card holding one can be handed the other. Then `groupDefault: CustomerBillingGroupDefault | null;` on `CustomerBillingProfile`; on `RawCustomerBillingProfile`, `groupDefault?: { group: CustomerGroupRef; paymentTermsDays?: number | null };` (the nested `group` is `required` in the contract, so it is never partial); and in `normalizeBillingProfile`:

```ts
  groupDefault: raw.groupDefault
    ? { group: raw.groupDefault.group, paymentTermsDays: raw.groupDefault.paymentTermsDays ?? null }
    : null,
```

In `-customer-billing-card.tsx`, beside the three hints it already computes the same way (and extend that block's comment: "…and the group's default, which is the only hint whose value the server resolved rather than this card"):

```tsx
  // Three states, one block: nothing to say when the customer is in no group or
  // its group decides nothing; where the term comes from when the profile
  // decided none; and which of the two applies when it did. The rule — own value
  // wins — is resolveBillingProfile's, and this card states it in words rather
  // than re-deriving it.
  const groupTerms = profile.groupDefault?.paymentTermsDays ?? null;
  const paymentTermsHint =
    groupTerms === null
      ? null
      : profile.paymentTermsDays === null
        ? t("billingInheritsTermsFromGroup", { count: groupTerms, group: profile.groupDefault?.group.name })
        : t("billingGroupTermsOverridden", { count: groupTerms });
```
and the row gains `hint={paymentTermsHint}`.

Both catalogs:

```ts
  billingInheritsTermsFromGroup_one: "Inherits {{count}} day from {{group}}",
  billingInheritsTermsFromGroup_other: "Inherits {{count}} days from {{group}}",
  billingGroupTermsOverridden_one: "Group default {{count}} day — overridden here",
  billingGroupTermsOverridden_other: "Group default {{count}} days — overridden here",
```
```ts
  billingInheritsTermsFromGroup_one: "Arver {{count}} dag fra {{group}}",
  billingInheritsTermsFromGroup_other: "Arver {{count}} dager fra {{group}}",
  billingGroupTermsOverridden_one: "Gruppens standard er {{count}} dag — overstyrt her",
  billingGroupTermsOverridden_other: "Gruppens standard er {{count}} dager — overstyrt her",
```
`0` is a real count (`billingInheritsTermsFromGroup_other` with `count: 0` reads "Inherits 0 days from Retail"): due on receipt is a decision, and the sentence must not disappear for it — which is exactly why the guard above is `groupTerms === null` and never `!groupTerms`.

- [ ] **Step 4: Run it, show it can fail, commit**

```bash
cd /home/anders/projects/vantigo/vantigo
mise exec -- bunx biome check --write apps/customers/frontend/src
mise exec -- bun run --cwd apps/customers/frontend test
mise exec -- bun run translations:check && mise exec -- bun run i18n:test
```
Change the guard to `!groupTerms` and add a case with `paymentTermsDays: 0` on the group to see the sentence vanish — then restore the `=== null` guard by hand and keep the extra assertion if it is worth keeping. Say what it printed.

```bash
cat > /tmp/claude-1000/msg-groups-7.txt <<'EOF'
feat(customers-ui): the billing card says where an inherited payment term comes from

One line under the payment-terms row, from the profile's own groupDefault:
"Inherits 30 days from Retail" when the customer decided nothing, "Group
default 30 days — overridden here" when it did, and nothing at all when it is
in no group or its group decides nothing. The card derives no rule — the
server already resolved it — and the guard is a null check, so a group whose
default is 0 days still says so.

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
EOF
PATHS="apps/customers/frontend/src/api/billing-profile.ts \
 apps/customers/frontend/src/pages/-customer-billing-card.tsx \
 apps/customers/frontend/src/pages/-customer-billing-card.test.tsx \
 apps/customers/frontend/src/i18n.ts"
git add $PATHS && git commit -F /tmp/claude-1000/msg-groups-7.txt -- $PATHS
git show --stat HEAD && git status --short
```
Four named files this time, one list for both commands: Task 6 committed the rest of that tree, so anything else dirty under it would be a leftover rather than this task's.

---

### Task 8: Verify the whole branch and open the PR

- [ ] **Step 1: The whole suite, as CI runs it**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'
mise exec -- gofmt -l internal && mise exec -- go vet ./... && mise exec -- go build ./...
taskset -c 0-3 mise exec -- go test -count=1 ./... 2>&1 | tail -40; echo "exit ${PIPESTATUS[0]}"
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- go generate ./... >/dev/null 2>&1; cd /home/anders/projects/vantigo/vantigo && git status --short   # must be clean
mise exec -- bun run --cwd apps/customers/frontend test && mise exec -- bun run --cwd apps/host/frontend test
mise exec -- bun run translations:check && mise exec -- bun run i18n:test
mise exec -- bunx biome check apps/customers/frontend/src apps/host/frontend/src
```
`taskset -c 0-3` because the race detector and 44 CPUs disagree about this database's connection limits (see the repo's own local-vs-CI notes); drop it if the suite is already green without it. `main` may already be red for reasons that are not ours — if a failure is in a module this branch never touched, check `git stash list`-free `git log origin/main` and say so in the report rather than fixing it here.

- [ ] **Step 2: Read the branch as a reviewer would**

```bash
cd /home/anders/projects/vantigo/vantigo
git log --oneline main..HEAD
git diff --stat main..HEAD
git diff main..HEAD -- openapi/customers.yaml | head -80
```
Check, by eye: every commit's trailer is `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>`; nothing in the diff touches `openapi/testdata/exchanges/`; the untracked `go.mod`/`go.sum` are still untracked; `openapi/COVERAGE.md`'s customers count moved by five.

- [ ] **Step 3: Open the PR**

```bash
cd /home/anders/projects/vantigo/vantigo
git push -u origin feat/customers-groups
cat > /tmp/claude-1000/pr-groups.md <<'EOF'
## Customer groups that carry defaults (phase 4, delivery D)

An installation can define **customer groups** — "Retail", "Key accounts",
"Public sector" — that a customer belongs to at most one of, and that carry a
**default payment term** every member inherits unless its own billing profile
decides otherwise. Decided in
`docs/superpowers/specs/2026-09-23-customers-groups-design.md` (D1–D6).

- **The vocabulary is the tags'** (migration `00027`, unique on `lower(name)`,
  NFC-normalised, unpaged, with a member count): `GET/POST /customers/groups`,
  `PUT/DELETE /customers/groups/{groupId}`, 409 `group_exists`, and no timeline
  event on a vocabulary write. No colour and no description — a group is a
  policy object, not a label.
- **The membership is the owner's**: one nullable column on the customer row,
  sharing its `revision`, written only through `PUT /customers/{id}/group`,
  answering the whole customer, with `group?` decorated onto every
  `SafeCustomerResponse` in one batched query and `groupId=<uuid>|none` on the
  list.
- **A group with members is never deleted**: 409 `group_in_use` with the count,
  and `ON DELETE RESTRICT` under it. Detaching them would change every member's
  effective payment term with no record on any customer.
- **The default is the third resolution tier**, in the one place resolution
  lives: `resolveBillingProfile` answers the profile's own value, else the
  group's default, else nothing — a nil check, so a decided `0` days survives.
  `GET`/`PUT .../billing-profile` answer `groupDefault` so the card can say
  "inherits 30 days from Retail" or "group default 30 days — overridden here".
- **`contracts.CustomerEntry.Group`** is the seam Products phase 4's
  customer-group prices will read — built ahead of its caller, as
  `BillingProfile` was.
- **No new permission key**, and the frontend gets a Group select on the
  relationship card, a Group filter with a Manage groups modal, and one sentence
  on the billing card.

Contract: five new operations (55 in total), five new schemas, `group?` and
`groupDefault?` added optionally to two existing ones. The frozen corpus is
untouched and still validates.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
gh pr create --base main --head feat/customers-groups \
  --title "Customer groups that carry defaults (phase 4, delivery D)" \
  --body-file /tmp/claude-1000/pr-groups.md
```
`gh pr edit` is broken in this environment: to change the body afterwards use `gh api -X PATCH repos/:owner/:repo/pulls/<n> -f body=@/tmp/claude-1000/pr-groups.md`. Do not merge — the user does that.

- [ ] **Step 4: Report**

Say: the PR's number and URL; each test shown able to fail and what the mutation printed; anything the generated code disagreed with this plan about (the sqlc LEFT-JOIN gate above all — which form `CustomerGroupMembership` needed); and whether `main` was already red. Plus the three places this branch goes past or around the spec's wording, each of which the controller has already approved and should still be on the record:

- the Group filter's first row says **All**, not the spec's "All groups" — the word every other filter on that page uses;
- `groupDefault` is answered by the billing-profile **PUT** as well as the GET, which D4 names only for the GET: both share `CustomerBillingProfile`, and the card writes the PUT's body into its cache;
- the vocabulary's four operations all answer `CustomerGroupSummary` and there is no separate `CustomerGroup` schema — the tags' own split, minus a shape nothing would read.
