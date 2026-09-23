# Owner and Tags (phase 4, delivery A) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give a customer one owner — a user of this installation — and a set of tags, so a list can answer "which ones are mine?" and "which ones are of this kind?", with the filters, pickers and timeline events that make both answers usable.

**Architecture:** The owner is one nullable column on `customers.customers` (`owner_user_id`), so it shares the row's `revision` and is written by a revision-guarded sub-resource PUT shaped exactly like contact info's. Tags are a two-table vocabulary of their own (`customers.tags`, `customers.customer_tags`) written by a set-replace that touches the customer row not at all. Both decorate `SafeCustomerResponse` — the owner's display name from one batched `contracts.UserDirectory.Users` call per response, the tags from one query per page — and both become a list filter (`ownerId=me|none|<uuid>`, `tagId=<uuid>`) validated in Go. The frontend gains an Owner column, tag chips and two filters on the list, an Owner row with a debounced user picker and a Tags row with a create-on-the-fly multi-select on the Overview tab, and a small Manage tags modal.

**Tech Stack:** Go 1.27 (pgx, sqlc, goose, oapi-codegen strict server), PostgreSQL 18, React + Mantine + TanStack Router/Query, vitest, bun, mise.

**Spec:** `docs/superpowers/specs/2026-09-23-customers-owner-tags-design.md` (D1–D4 + "Out of scope" + "Testing"). Read it first; it is binding. It builds on `docs/superpowers/specs/2026-09-21-customers-foundation-design.md` (the revision rules D5 and the actor rule D1) and on `docs/customers.md`.

## Global Constraints

- Branch `feat/customers-light-crm` (already checked out). Never commit to `main`, never merge, never `--no-verify`.
- **Forbidden git commands:** `git add -A`, `git add .`, `git stash`, `git checkout -- .`, `git restore .`, `git clean`, `git reset --hard`. Commit with an explicit pathspec (`git add <files>` then `git commit -F <msgfile> -- <files>`), then check `git show --stat HEAD` and that `git status --short` shows nothing of yours left. The untracked `go.mod`/`go.sum` in the repo root are not ours: never add or delete them.
- Commit messages: Conventional Commits scoped `customers` / `customers-ui` / `host` / `docs`, subject a plain sentence about behaviour (see `git log`). End every message with `Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>` exactly.
- Toolchain only through mise: `mise exec -- go …`, `mise exec -- bun …`. Capture exit codes before any pipe (`${PIPESTATUS[0]}`).
- Go tests need `TEST_DATABASE_URL` exported in the environment before the first `go test`. **Take the value from the environment** (the project's own test database); this plan does not name one. If it is unset, stop and ask — do not guess a port.
- **The frozen corpus** `openapi/testdata/exchanges/customers.jsonl` is never edited, for any reason.
- **Contract changes are additive and optional only** on schemas that already exist: a new property on `SafeCustomerResponse` is never added to its `required:` list. That includes `tags`, even though the server always sends it (`[]` when empty) — the recorded exchanges predate it. New schemas (`CustomerOwner`, `CustomerTag`, `CustomerTagSummary`, …) may have required fields. New query parameters are plain optional strings validated in Go with this module's own query-parameter wording (`'x' must be …, but was '…'.` — period-terminated, case-sensitive), never a yaml `enum:`.
- New operations need an `operationId` and an `x-vantigo-access` rule, and every operation must be exercised by this module's own tests: `internal/customers/main_test.go`'s `contracttest.RequireCoverage` fails the package otherwise.
- **`ownerId=me` resolves from the principal**, never from a request field and never from a host prop: `contracts.PrincipalFrom(ctx)` is the only source of "the caller" (`actor.go`). The spec's D4 is explicit that an unauthenticated call is already a 401 before any of this.
- **A directory call never happens inside a transaction.** `s.deps.Users.User/Users/SearchUsers` are out-of-process reads; every one of them runs before `db.WithTx` opens or after it commits, the same rule `actorFor`'s doc comment states and `refreshRegistryRecord` obeys.
- **The timeline actor is resolved before the transaction and only when a write will happen** (`s.actorFor(ctx, generatedFallbackActor)`).
- **The row's revision rules still bind:** every write to the `customers.customers` row bumps `revision`; a no-op writes nothing at all (no revision bump, no `updated_at` move, no timeline event); a guarded write repeats the revision comparison in the `UPDATE`'s own `WHERE`, and `pgx.ErrNoRows` from it means re-read and answer 409 (or 404 if the row is gone); the 409 body is `customerRevisionConflict` (title "Customer revision conflict", no `code`). The tags set-replace is **off** the row: it bumps nothing, takes no revision and accepts none.
- Both catalogs (`en` and `nb`) of `apps/customers/frontend/src/i18n.ts` get every new string; `mise exec -- bun run translations:check` and `mise exec -- bun run i18n:test` must pass.
- **Never assert "the last fetch"** in a frontend test — debounced pickers run on their own clock and CI is slow. Filter `fetchMock.mock.calls` by method and URL.
- After any `openapi/*.yaml`, `queries/*.sql` or migration change: `cd apps/server && mise exec -- go generate ./...` (a second run must show no new diff), then from the repo root `mise exec -- bun run gen:client` for a yaml change. Commit every generated file (`apps/server/internal/openapi/specs/customers.yaml`, `internal/customers/gen/api.gen.go`, `internal/customers/store/*.go`, each changed `api-schema.d.ts`, `openapi/COVERAGE.md` if it moved).
- After any migration: add it to `apps/server/internal/customers/sqlc.yaml`'s `schema:` list (`TestSqlcSchemaListsOnlyTheModulesOwnMigrations` enforces that the list is exactly this module's migrations) and to `internal/db/schema_test.go`'s `wantTables`, then `go generate`.
- **Read the generated file before writing Go against it.** After `go generate`, open `apps/server/internal/customers/store/tags.sql.go` and the changed parts of `store/customers.sql.go` and check which queries took a bare argument and which a `…Params` struct, the exact integer widths, and the spelling of every column (`LegalID` vs `LegalId`, `OwnerUserID` vs `OwnerUserId`). Where sqlc disagrees with this plan's Go, **follow sqlc** and adjust the call, not the query.
- After any exported Go signature change: `mise exec -- go vet ./...` and grep every caller including tests and fakes.
- Match the surrounding code: comment density and voice (these files explain *why*), naming, error wording, test style. Body-level validation messages have no trailing period (`values.go`); query-parameter messages do (`customers.go`'s `validateGetCustomersParams`).
- Every new test must be shown able to fail (remove the guard, see red, restore). Say so in the report.
- **One implementer commits at a time.** If two agents share the tree, the second writes and verifies but does not commit.

---

## File Structure

| File | Responsibility |
| --- | --- |
| `apps/server/internal/db/migrations/00024_customers_owner_tags.sql` | the owner column, the two tag tables, the `lower(name)` unique index |
| `apps/server/internal/customers/queries/tags.sql` | the tag vocabulary's CRUD, the set replace, the per-page tags load |
| `apps/server/internal/customers/queries/customers.sql` | `owner_user_id` on every row read, the guarded owner write, the three new list filters |
| `apps/server/internal/customers/owner.go` | `PUT /customers/{id}/owner`, `GET /customers/assignable-users`, and the owner/tags decoration every customer response goes through |
| `apps/server/internal/customers/tags.go` | the tag vocabulary's four operations and `PUT /customers/{id}/tags` |
| `apps/server/internal/customers/values.go` | `validateTagName`, `validateTagColor` beside the module's other value rules |
| `apps/server/internal/customers/customers.go` | `customerRow.OwnerUserID`, `safeCustomerResponse`'s decoration parameter, `ownerId`/`tagId` validation, `GetCustomers`'s wiring |
| `apps/server/internal/customers/timeline_events.go` | `customer.owner_changed`, `customer.tags_changed` |
| `openapi/customers.yaml` | five new operations, five new schemas, two new list parameters, two additive `SafeCustomerResponse` properties |
| `apps/customers/frontend/src/api/tags.ts` | the tag vocabulary's client, the set replace, `CustomerTag`/`CustomerTagSummary` |
| `apps/customers/frontend/src/api/owner.ts` | the owner PUT and the assignable-users search |
| `apps/customers/frontend/src/components/owner-picker.tsx` | the debounced `Select` over assignable users that keeps its current value |
| `apps/customers/frontend/src/pages/-customer-relationship-card.tsx` | the Overview tab's Owner row and Tags row |
| `apps/customers/frontend/src/pages/-manage-tags-modal.tsx` | rename, recolour and delete a tag, with its customer count |
| `apps/customers/frontend/src/pages/customers.index.tsx` | the Owner column, the tag chips, the Owner and Tag filters, Manage tags |
| `apps/host/frontend/src/routes/customers/-customers-list.tsx` | the one new host wrapper: `canEdit` for the list page |

---

### Task 1: The columns, the tables and every query that reads them (D1, D2)

**Files:**
- Create: `apps/server/internal/db/migrations/00024_customers_owner_tags.sql`, `apps/server/internal/customers/queries/tags.sql`
- Modify: `apps/server/internal/customers/sqlc.yaml` (the `schema:` list), `apps/server/internal/db/schema_test.go` (`wantTables` and two new assertions in `TestCustomersBaseline_AppliesAndIsIdempotent`), `apps/server/internal/customers/queries/customers.sql`
- Generated: `apps/server/internal/customers/store/tags.sql.go`, `apps/server/internal/customers/store/customers.sql.go`, `apps/server/internal/customers/store/models.go`
- Read first (do not change): `apps/server/internal/db/migrations/00023_customers_registry_feed.sql` (the migration voice), `apps/server/internal/customers/queries/addresses.sql:1-20` (`LockCustomer`), `apps/server/internal/customers/queries/customers.sql:183-298` (the two list queries whose WHERE clauses are kept textually identical by hand)

**Interfaces:**
- Produces (SQL, and therefore the generated Go the next tasks call):
  - `customers.customers.owner_user_id uuid NULL`, index `ix_customers_owner_user`
  - `customers.tags(id uuid PK, name varchar(100) NOT NULL, color varchar(20) NULL)`, unique index `ux_customers_tags_name_lower` on `lower(name)`
  - `customers.customer_tags(customer_id integer, tag_id uuid, PRIMARY KEY (customer_id, tag_id))`, both foreign keys `ON DELETE CASCADE`, index `ix_customer_tags_tag`
  - `UpdateCustomerOwner :one`, `CustomerExists :one` (customers.sql)
  - `ListCustomerTags :many`, `GetCustomerTag :one`, `InsertCustomerTag :one`, `UpdateCustomerTagRow :one`, `DeleteCustomerTag :execrows`, `CustomerTagsByIDs :many`, `CustomerTagsForCustomers :many`, `DeleteCustomerTagLinks :exec`, `InsertCustomerTagLinks :exec` (tags.sql)
  - `owner_user_id` on `GetCustomer`, `UpdateCustomer`, `SetCustomerType`, `UpdateCustomerContactInfo`, `ListCustomers`
  - `owner_id`, `owner_none`, `tag_id` parameters on `CountCustomers` and `ListCustomers`

- [ ] **Step 1: Write the failing schema test**

The schema test is this task's only Go-level guard, and it is written first. In `apps/server/internal/db/schema_test.go`, inside `TestCustomersBaseline_AppliesAndIsIdempotent`, extend `wantTables` to the full alphabetical list (the query orders by `table_name`, so `customer_tags` lands between `customer_registry_records` and `customers`, and `tags` sorts last):

```go
	wantTables := []string{
		"contacts",
		"counters",
		"customer_addresses",
		"customer_peppol_lookups",
		"customer_registry_records",
		"customer_tags",
		"customers",
		"customers_contacts",
		"customers_timeline_entries",
		"customers_timeline_entries_revisions",
		"registry_feed_cursor",
		"tags",
	}
```

Then, immediately after the existing `customers_contacts` primary-key assertion in the same function, add:

```go
	if cols := primaryKeyColumns(t, ctx, pool, "customers", "customer_tags"); !equalStrings(cols, []string{"customer_id", "tag_id"}) {
		t.Errorf("customer_tags primary key columns = %v, want [customer_id tag_id]", cols)
	}

	// A tag is a vocabulary, so 'VIP' and 'vip' are one word (owner and tags
	// design D2): the uniqueness is on lower(name), which is an EXPRESSION
	// index — indexColumns above joins pg_attribute on i.indkey and therefore
	// reports nothing at all for one, so this reads the definition instead.
	// The customers module's own 409 tag_exists test proves the behaviour;
	// this proves the index that makes it cheap and race-proof is really there.
	var tagNameIndexDef string
	if err := pool.QueryRow(ctx, `SELECT indexdef FROM pg_indexes
	                              WHERE schemaname = 'customers' AND indexname = 'ux_customers_tags_name_lower'`).Scan(&tagNameIndexDef); err != nil {
		t.Fatalf("read ux_customers_tags_name_lower definition: %v", err)
	}
	if !strings.Contains(tagNameIndexDef, "UNIQUE") || !strings.Contains(tagNameIndexDef, "lower(name") {
		t.Errorf("ux_customers_tags_name_lower = %q, want a UNIQUE index on lower(name)", tagNameIndexDef)
	}
```

If `strings` is not already imported in that file, add it.

- [ ] **Step 2: Run the schema test to verify it fails**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server && mise exec -- go test -count=1 -run 'TestCustomersBaseline' ./internal/db/
```
Expected: FAIL — `tables = [… registry_feed_cursor], want [… customer_tags … tags]` (the length check fires first).

- [ ] **Step 3: Write the migration**

Create `apps/server/internal/db/migrations/00024_customers_owner_tags.sql`:

```sql
-- +goose Up
-- Who owns the customer relationship, and how customers are classified
-- (owner and tags design D1, D2) — phase 4's first delivery.
--
-- owner_user_id is a SINGLE user of this installation, on the customer row
-- itself, which is the whole point: the owner shares the row's revision, so
-- setting it is an ordinary guarded write and a concurrent edit cannot lose
-- it. NULL is unowned.
--
-- There is deliberately NO foreign key to identity.users. Two reasons, and
-- both are load-bearing rather than stylistic: this module may not read
-- identity's schema at all (internal/db/schema_test.go bars it, depguard bars
-- the import) — contracts.UserDirectory is the only sanctioned seam — and a
-- FK would decide, in the database, what design D1 decides in prose: an owner
-- who is later disabled, or whose account is removed, KEEPS the customer.
-- Nothing is silently revoked; the UI shows the name with an "inactive" hint,
-- or "Unknown user" once the directory no longer knows the id at all (the
-- actorFor precedent, actor.go). ON DELETE SET NULL would have thrown that
-- record away.
ALTER TABLE customers.customers
    ADD COLUMN owner_user_id uuid;

-- The list's ownerId filter is an equality on this column, and "my customers"
-- is the one filter a sales user reaches for every day. Partial, because
-- unowned customers are found by IS NULL and never by an index lookup on a
-- value, so indexing them would be bytes spent on rows this index can never
-- serve.
CREATE INDEX ix_customers_owner_user ON customers.customers (owner_user_id)
    WHERE owner_user_id IS NOT NULL;

-- The tag vocabulary (design D2), shaped after communications' own tags
-- (00006_communications_baseline.sql) with one deliberate difference: the
-- uniqueness is on lower(name), not on name. A tag is a vocabulary word, so
-- 'VIP' and 'vip' are the same word and a second one is a mistake, not a
-- variant — and an installation that accumulates both has a filter that
-- silently splits its customers in two. The id is a uuid rather than an
-- identity integer for the same reason communications' is: a tag is created
-- from a picker, mid-edit, and the client wants a stable id it can round-trip
-- without a second read.
--
-- color is one of Mantine's named colours or NULL, validated in Go
-- (validateTagColor, values.go) rather than by a CHECK: the set is a UI
-- palette, which changes with the UI and not with the data, and a CHECK would
-- turn a palette change into a migration.
CREATE TABLE customers.tags (
    id    uuid         PRIMARY KEY,
    name  varchar(100) NOT NULL,
    color varchar(20)
);
CREATE UNIQUE INDEX ux_customers_tags_name_lower ON customers.tags (lower(name));

-- Which customers carry which tag. CASCADE both ways is what makes the two
-- destructive operations honest: deleting a tag removes it from every
-- customer (design D2's DELETE /customers/tags/{tagId} is a 204 and nothing
-- else), and archiving is not deletion so a customer's links simply survive.
CREATE TABLE customers.customer_tags (
    customer_id integer NOT NULL REFERENCES customers.customers (id) ON DELETE CASCADE,
    tag_id      uuid    NOT NULL REFERENCES customers.tags (id) ON DELETE CASCADE,
    PRIMARY KEY (customer_id, tag_id)
);

-- The primary key already serves "which tags does this customer have"; this
-- index serves the other two directions the module actually asks in: the
-- list's tagId filter (which customers carry this tag) and the tag list's
-- customerCount (design D3's delete confirmation).
CREATE INDEX ix_customer_tags_tag ON customers.customer_tags (tag_id);

-- +goose Down
DROP TABLE customers.customer_tags;
DROP TABLE customers.tags;
ALTER TABLE customers.customers DROP COLUMN owner_user_id;
```

Add `      - ../db/migrations/00024_customers_owner_tags.sql` as the last entry of `schema:` in `apps/server/internal/customers/sqlc.yaml`.

- [ ] **Step 4: Run the schema test to verify it passes**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server && mise exec -- go test -count=1 -run 'TestCustomersBaseline|TestSqlcSchemaLists' ./internal/db/ ./internal/customers/
```
Expected: PASS. (`./internal/customers/` also runs the module's suite; it is green at this point because nothing in Go has changed yet.)

- [ ] **Step 5: Prove the new assertions can fail**

Comment out the `CREATE UNIQUE INDEX ux_customers_tags_name_lower` line and re-run Step 4: `TestCustomersBaseline` goes red on the index definition. Restore it. Change the `customer_tags` PRIMARY KEY to `(tag_id, customer_id)` and re-run: red on the primary-key columns. Restore.

- [ ] **Step 6: Add `owner_user_id` to every query that reads a customer row**

In `apps/server/internal/customers/queries/customers.sql`, add `owner_user_id` to the end of the column list of **each** of these five, so every generated row type carries it:

- `GetCustomer`'s `SELECT … , email, phone, website` → `… , email, phone, website, owner_user_id`
- `UpdateCustomer`'s `RETURNING … email, phone, website` → `… email, phone, website, owner_user_id`
- `SetCustomerType`'s `RETURNING …` → the same addition
- `UpdateCustomerContactInfo`'s `RETURNING …` → the same addition
- `ListCustomers`'s `SELECT c.id, … c.email, c.phone, c.website,` → `… c.email, c.phone, c.website, c.owner_user_id,` (before the two timeline sub-selects)

`InsertCustomer` is deliberately left alone: nothing creates a customer with an owner (design D1 has no create field for it), and `PostCustomers` answers `CreateCustomerResponse`, not `SafeCustomerResponse`, so its row never becomes a `customerRow`.

- [ ] **Step 7: Add the guarded owner write and the existence check**

Append to `apps/server/internal/customers/queries/customers.sql`, immediately after `UpdateCustomerContactInfo`:

```sql
-- name: UpdateCustomerOwner :one
-- UpdateCustomerOwner is PUT /customers/{id}/owner's write (owner and tags
-- design D1): the owner column only — name, status, type, the legal identity
-- and the contact info are untouched, since this sub-resource never writes
-- them. Guarded and revision-bumping exactly like UpdateCustomerContactInfo
-- above: sqlc.narg(expected_revision) is PutCustomerOwnerRequest's optional
-- revision, and the caller (owner.go) skips calling this entirely when the
-- owner did not actually change, so a resubmit of the current owner writes
-- nothing and bumps nothing (customers foundation design D5's no-op rule).
--
-- @owner_user_id is NULL to clear the owner, which is a real change like any
-- other: it bumps the revision and records customer.owner_changed.
UPDATE customers.customers
SET owner_user_id = @owner_user_id,
    updated_at = @updated_at::timestamptz,
    revision = revision + 1
WHERE id = @id
  AND (sqlc.narg(expected_revision)::int IS NULL OR revision = sqlc.narg(expected_revision)::int)
RETURNING id, customer_number, name, status, legal_country, legal_id, legal_name, legal_source, legal_type,
          created_at, updated_at, type, revision, email, phone, website, owner_user_id;

-- name: CustomerExists :one
-- CustomerExists is PUT /customers/{id}/tags's 404 check (owner and tags
-- design D2), and deliberately not LockCustomer (queries/addresses.sql): a
-- tag set-replace is off the customer row, so it takes no lock on it and no
-- revision — two concurrent replaces are last-wins, which is what replacing a
-- set means. pgx.ErrNoRows means the customer does not exist.
SELECT id FROM customers.customers WHERE id = @id;
```

- [ ] **Step 8: Add the three list filters to BOTH list queries**

`CountCustomers` and `ListCustomers` share a hand-kept identical WHERE clause (their own comments warn about it). Add the same three predicates to **both**, immediately after the `customer_type` predicate and before the big `search` predicate:

```sql
  AND (NOT @owner_none::bool OR c.owner_user_id IS NULL)
  AND (sqlc.narg(owner_id)::uuid IS NULL OR c.owner_user_id = sqlc.narg(owner_id)::uuid)
  AND (sqlc.narg(tag_id)::uuid IS NULL OR EXISTS (
        SELECT 1
        FROM customers.customer_tags cft
        WHERE cft.customer_id = c.id
          AND cft.tag_id = sqlc.narg(tag_id)::uuid))
```

and extend `CountCustomers`'s doc comment with a paragraph (`ListCustomers`'s comment already defers to it for the WHERE clause):

```sql
-- owner_none/owner_id are the two halves of design D1's ownerId filter,
-- because they are genuinely different questions: 'none' is "no owner at all"
-- (a NULL test, which no equality can express) and a uuid — including the one
-- 'me' resolved to in Go — is an equality. They are never both set: the Go
-- validation turns exactly one ownerId value into exactly one of them. tag_id
-- is design D2's single-tag filter: a customer matches when it carries that
-- tag, and multi-tag filtering is not built until someone asks.
```

- [ ] **Step 9: Generate, twice, then read what sqlc produced**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server && mise exec -- go generate ./... && mise exec -- go generate ./... && git status --short
```
Expected: `internal/customers/store/tags.sql.go` appears, `store/customers.sql.go` and `store/models.go` change, and the second run adds nothing new.

**Now open `apps/server/internal/customers/store/tags.sql.go` and the changed parts of `store/customers.sql.go` before writing a line of Go against them.** Check, and write down for Tasks 3 and 4:

- which queries took a bare argument and which a `…Params` struct. Expected bare: `ListCustomerTags(ctx)`, `GetCustomerTag(ctx, id)`, `DeleteCustomerTag(ctx, id)`, `CustomerTagsByIDs(ctx, ids)`, `CustomerTagsForCustomers(ctx, customerIDs)`, `DeleteCustomerTagLinks(ctx, customerID)`, `CustomerExists(ctx, id)`. Expected structs: `InsertCustomerTagParams{ID, Name, Color}`, `UpdateCustomerTagRowParams{Name, Color, ID}`, `InsertCustomerTagLinksParams{CustomerID, TagIds}`, `UpdateCustomerOwnerParams{OwnerUserID, UpdatedAt, ID, ExpectedRevision}`, and the three new fields on `CountCustomersParams`/`ListCustomersParams`;
- the exact spelling and type of the new fields: `OwnerUserID *uuid.UUID` (from the nullable `uuid` override) vs `OwnerUserId`; `OwnerNone bool`; `OwnerID *uuid.UUID`; `TagID *uuid.UUID`; `TagIds []uuid.UUID` vs `TagIDs`;
- `CustomerCount int64` on `ListCustomerTagsRow`/`GetCustomerTagRow` (a `count(*)` sub-select is `bigint`), so Task 4 casts to `int32` for the contract;
- `CustomerTagsForCustomersRow`'s field names (`CustomerID`, `ID`, `Name`, `Color`) and that `@customer_ids::int[]` came out `[]int32`.

Where sqlc disagrees with the code in Tasks 3 and 4, **follow sqlc**.

The query file itself, created as `apps/server/internal/customers/queries/tags.sql`:

```sql
-- name: ListCustomerTags :many
-- ListCustomerTags is GET /customers/tags (owner and tags design D2, D3):
-- the whole vocabulary, name-ascending, each with how many customers carry
-- it. The count travels with the list rather than behind a second endpoint
-- because the one place it is needed is the Manage tags modal's delete
-- confirmation, which is already showing the list — and a tag vocabulary is
-- tens of rows, not thousands, so a correlated count per row costs nothing
-- worth a second round trip. id is the tie-break so two tags that differ only
-- in case (which the unique index forbids) or in trailing punctuation still
-- order deterministically.
SELECT t.id, t.name, t.color,
       (SELECT count(*) FROM customers.customer_tags ct WHERE ct.tag_id = t.id) AS customer_count
FROM customers.tags t
ORDER BY t.name, t.id;

-- name: GetCustomerTag :one
-- GetCustomerTag is one tag with its count, for PUT /customers/tags/{tagId}'s
-- 404 check and for the body its 200 answers. pgx.ErrNoRows means the tag
-- does not exist.
SELECT t.id, t.name, t.color,
       (SELECT count(*) FROM customers.customer_tags ct WHERE ct.tag_id = t.id) AS customer_count
FROM customers.tags t
WHERE t.id = @id;

-- name: InsertCustomerTag :one
-- InsertCustomerTag is POST /customers/tags. A unique violation on
-- ux_customers_tags_name_lower is the duplicate name (409 tag_exists), which
-- the handler recognises with db.IsUniqueViolation naming that constraint
-- exactly — never a bare "some unique violation", which would also swallow a
-- uuid collision the caller must hear about as a 500.
INSERT INTO customers.tags (id, name, color)
VALUES (@id, @name, @color)
RETURNING id, name, color;

-- name: UpdateCustomerTagRow :one
-- UpdateCustomerTagRow is PUT /customers/tags/{tagId}: a rename, a recolour,
-- or both. Named …Row rather than UpdateCustomerTag so the generated method
-- does not read as "update a customer's tag", which is what the set replace
-- below does. Renaming records nothing on the customers that carry the tag
-- (design D2: the tag is the vocabulary, not the customer), so there is no
-- timeline write anywhere near this statement.
UPDATE customers.tags
SET name = @name, color = @color
WHERE id = @id
RETURNING id, name, color;

-- name: DeleteCustomerTag :execrows
-- DeleteCustomerTag is DELETE /customers/tags/{tagId}. The customer_tags
-- links go with it through the table's own ON DELETE CASCADE — there is no
-- second statement here, and that is the point of the cascade. The affected
-- row count is how the handler tells 204 from 404.
DELETE FROM customers.tags WHERE id = @id;

-- name: CustomerTagsByIDs :many
-- CustomerTagsByIDs resolves the ids PUT /customers/{id}/tags was given, in
-- one round trip, so an unknown one is a field error on tagIds rather than a
-- foreign-key violation surfacing as a 500. Duplicates in the input are
-- tolerated: = ANY() answers each tag once whatever the caller sent.
SELECT id, name, color FROM customers.tags WHERE id = ANY(@ids::uuid[]) ORDER BY name, id;

-- name: CustomerTagsForCustomers :many
-- CustomerTagsForCustomers is the tags on a whole page of customers in ONE
-- query (design D2), never one query per row: the list answers 25 customers
-- and a per-row read would be 25 round trips for data that is on the wire
-- either way. The single-customer reads (GET /customers/{id} and every
-- sub-resource PUT that answers SafeCustomerResponse) call it with a
-- one-element array rather than having a query of their own, so there is one
-- shape of tags-on-a-response and one place it can be wrong.
--
-- Ordered by customer, then tag name, so the chips come out in the same order
-- the vocabulary lists in and a caller never sees them shuffle between reads.
SELECT ct.customer_id, t.id, t.name, t.color
FROM customers.customer_tags ct
JOIN customers.tags t ON t.id = ct.tag_id
WHERE ct.customer_id = ANY(@customer_ids::int[])
ORDER BY ct.customer_id, t.name, t.id;

-- name: DeleteCustomerTagLinks :exec
-- DeleteCustomerTagLinks and InsertCustomerTagLinks are PUT
-- /customers/{id}/tags: the set is REPLACED, which is the natural write for a
-- multi-select, so it is a delete of everything followed by an insert of
-- what was asked for, both inside one transaction. A diff (delete the
-- removed, insert the added) would be the same two statements with more
-- arithmetic and the same outcome; replacing is what the endpoint means.
DELETE FROM customers.customer_tags WHERE customer_id = @customer_id;

-- name: InsertCustomerTagLinks :exec
-- unnest, not one INSERT per tag: the whole set is one statement, so a
-- transaction that dies halfway cannot leave a partial set. An empty array
-- inserts nothing, which is exactly what clearing a customer's tags means.
INSERT INTO customers.customer_tags (customer_id, tag_id)
SELECT @customer_id, unnest(@tag_ids::uuid[]);
```

- [ ] **Step 10: Verify nothing else moved, and commit**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server && mise exec -- go vet ./... && mise exec -- go test -count=1 ./internal/db/ ./internal/customers/
cd /home/anders/projects/vantigo/vantigo
printf '%s\n\n%s\n' 'feat(customers): a customer row can name an owner, and tags get their own two tables' 'Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>' > /tmp/msg-owner-task1
git add apps/server/internal/db/migrations/00024_customers_owner_tags.sql apps/server/internal/db/schema_test.go apps/server/internal/customers/sqlc.yaml apps/server/internal/customers/queries/customers.sql apps/server/internal/customers/queries/tags.sql apps/server/internal/customers/store
git commit -F /tmp/msg-owner-task1 -- apps/server/internal/db/migrations/00024_customers_owner_tags.sql apps/server/internal/db/schema_test.go apps/server/internal/customers/sqlc.yaml apps/server/internal/customers/queries/customers.sql apps/server/internal/customers/queries/tags.sql apps/server/internal/customers/store
git show --stat HEAD && git status --short
```

Expected: the module's tests still pass — the new columns reach `customerRow` only in Task 3, and the generated row types simply carry a field nothing reads yet.

---

### Task 2: The shared contract vocabulary (D1, D2, D3)

**Files:**
- Modify: `openapi/customers.yaml`
- Generated: `apps/server/internal/openapi/specs/customers.yaml`, `apps/server/internal/customers/gen/api.gen.go`, `apps/*/frontend/src/api/api-schema.d.ts` wherever it changes
- Read first (do not change): `openapi/customers.yaml:770-787` (`PutCustomerContactInfoRequest`, whose optional `revision` this copies), `:315-327` (`CustomerContactInfo`, the additive-optional precedent and its wording), `:849-892` (`SafeCustomerResponse`), `:1097-1167` (`getCustomers`'s parameters), `apps/server/internal/openapi/openapi_test.go:274-360` (`TestLintFlagsTheStructuralRules` and `accessRule` — a `permission:` list must be sorted), `:392` (`TestNullableRefsUseAllOf`)

**Why this task carries no new paths.** Every operation this delivery adds lands on `gen.StrictServerInterface`, and `server.go`'s `var _ gen.StrictServerInterface = (*server)(nil)` means the customers package stops compiling the moment a method exists in the interface and not on `*server`. So the *paths* travel with their handlers — the owner's two in Task 3, the tags' four in Task 4, each with its own `go generate`, its own `KnownServeMuxConflicts` entries and its own `COVERAGE.md` pass. What lands here is everything both halves share and neither can own alone: the eight schemas, the two additive `SafeCustomerResponse` properties, and the two new list parameters. Schemas with no operation referencing them yet generate types and break nothing (no lint forbids it), the two list parameters are optional and simply unread until Task 3, and the frontend gets its generated types one task early. This task is green on its own.

**Interfaces:**
- Produces (schemas):
```
CustomerOwner            { userId: uuid, displayName: string, active: bool }                     all required
CustomerTag              { id: uuid, name: string, color?: string|null }                        id, name required
CustomerTagSummary       { id: uuid, name: string, color?: string|null, customerCount: int32 }  id, name, customerCount required
CustomerTagRequest       { name: string, color?: string|null }                                   name required
CustomerTagsResponse     { tags: CustomerTag[] }                                                 tags required
CustomerAssignableUser   { userId: uuid, displayName: string }                                   both required
PutCustomerOwnerRequest  { ownerUserId?: uuid|null, revision?: int32|null }                      nothing required
PutCustomerTagsRequest   { tagIds: uuid[] }                                                      tagIds required
SafeCustomerResponse     gains owner?: CustomerOwner|null and tags?: CustomerTag[]               NEITHER added to required
```
- Produces (query parameters on `getCustomers`): `ownerId`, `tagId`, both plain optional strings.
- Produces (for Tasks 3 and 4 to place): the `operationId` and `x-vantigo-access` of all six operations —
```
putCustomersByIdOwner          PUT    /api/v1/customers/{id}/owner        permission:customers:update+customers:view
getCustomersAssignableUsers    GET    /api/v1/customers/assignable-users  permission:customers:update
getCustomersTags               GET    /api/v1/customers/tags              permission:customers:view
postCustomersTags              POST   /api/v1/customers/tags              permission:customers:update
putCustomersTagsByTagId        PUT    /api/v1/customers/tags/{tagId}      permission:customers:update
deleteCustomersTagsByTagId     DELETE /api/v1/customers/tags/{tagId}      permission:customers:update
putCustomersByIdTags           PUT    /api/v1/customers/{id}/tags         permission:customers:update+customers:view
```
Every `permission:` list is alphabetically sorted (`customers:update` before `customers:view`), which the contract lint requires.

- [ ] **Step 1: Add the eight schemas**

In `openapi/customers.yaml`, under `components.schemas`, insert each of these in the file's existing alphabetical order (`CustomerAssignableUser` before `CustomerConflictDuplicate`; `CustomerOwner` after `CustomerContactResponse`; the three `CustomerTag*` after the `CustomerRegistry…` schemas; `PutCustomerOwnerRequest` and `PutCustomerTagsRequest` beside `PutCustomerContactInfoRequest`):

```yaml
        CustomerAssignableUser:
            description: A user who may be made a customer's owner (owner and tags design D1) — the directory's active users, capped at 20. Enough to name them in a picker and nothing more, which is all contracts.UserDirectory publishes.
            properties:
                displayName:
                    type: string
                userId:
                    format: uuid
                    type: string
            required:
                - userId
                - displayName
            type: object
        CustomerOwner:
            description: The single user accountable for the customer relationship (owner and tags design D1). displayName is resolved from the user directory at read time, never stored on the customer; a user the directory no longer knows is reported as "Unknown user" with active false, and an owner disabled after being assigned keeps the customer and is reported with active false. Absent when the customer is unowned.
            properties:
                active:
                    type: boolean
                displayName:
                    type: string
                userId:
                    format: uuid
                    type: string
            required:
                - userId
                - displayName
                - active
            type: object
        CustomerTag:
            description: One tag as it appears on a customer (owner and tags design D2). color is one of Mantine's named colours (gray, red, pink, grape, violet, indigo, blue, cyan, teal, green, lime, yellow, orange) or null, validated by the server so a client never has to sanitise it.
            properties:
                color:
                    nullable: true
                    type: string
                id:
                    format: uuid
                    type: string
                name:
                    type: string
            required:
                - id
                - name
            type: object
        CustomerTagRequest:
            description: A tag's name and colour (owner and tags design D2). name is 1-100 characters, trimmed; a name another tag already has, ignoring case, is a 409 with code tag_exists.
            properties:
                color:
                    nullable: true
                    type: string
                name:
                    type: string
            required:
                - name
            type: object
        CustomerTagSummary:
            description: A tag in the installation's vocabulary, with how many customers carry it (owner and tags design D2, D3) — what the Manage tags modal needs in order to say what a delete will affect, on the same response that lists the tags.
            properties:
                color:
                    nullable: true
                    type: string
                customerCount:
                    format: int32
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
        CustomerTagsResponse:
            description: A customer's tags after a set replace (owner and tags design D2), name-ascending.
            properties:
                tags:
                    items:
                        $ref: '#/components/schemas/CustomerTag'
                    type: array
            required:
                - tags
            type: object
        PutCustomerOwnerRequest:
            description: "PUT /customers/{id}/owner's own request body (owner and tags design D1): the owner is set to ownerUserId, or cleared when it is absent or null. The user must exist and be active to be assigned. revision is optional, as PUT /customers/{id}/contact-info's own is."
            properties:
                ownerUserId:
                    format: uuid
                    nullable: true
                    type: string
                revision:
                    description: The revision the caller read the customer at (customers foundation design D5). Optional — omitted, the change applies regardless; present and stale, a 409.
                    format: int32
                    nullable: true
                    type: integer
            type: object
        PutCustomerTagsRequest:
            description: "PUT /customers/{id}/tags's own request body (owner and tags design D2): the customer's tag set is REPLACED by tagIds. There is no revision here and none is accepted — tags are off the customer row, so they bump nothing and two concurrent replaces are last-wins, which is what replacing a set means. An empty array clears the customer's tags. An unknown id is a field error on tagIds."
            properties:
                tagIds:
                    items:
                        format: uuid
                        type: string
                    type: array
            required:
                - tagIds
            type: object
```

- [ ] **Step 2: Add the two properties to `SafeCustomerResponse`**

Inside `SafeCustomerResponse.properties`, keeping the alphabetical order (`owner` after `name`, `tags` after `status`), and **without touching its `required:` list**:

```yaml
                owner:
                    allOf:
                        - $ref: '#/components/schemas/CustomerOwner'
                    description: The user accountable for this customer relationship (owner and tags design D1). Absent when the customer is unowned; needs nothing beyond customers:view to read.
                    nullable: true
                tags:
                    description: Every tag this customer carries, name-ascending (owner and tags design D2). Always present on responses from this version on — an empty array when the customer has none — and optional here only because the recorded exchange corpus predates it.
                    items:
                        $ref: '#/components/schemas/CustomerTag'
                    type: array
```

The `allOf` wrapper around a `nullable: true` `$ref` is not decoration: `TestNullableRefsUseAllOf` requires it, and `contactInfo` right above uses the same shape.

- [ ] **Step 3: Add the two list parameters**

In `getCustomers`'s `parameters:`, after the existing `type` parameter:

```yaml
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
```

- [ ] **Step 4: Generate, twice, and check the contract**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server && mise exec -- go generate ./... && mise exec -- go generate ./... && git status --short
cd /home/anders/projects/vantigo/vantigo/apps/server && mise exec -- go build ./... && mise exec -- go test -count=1 ./internal/openapi/... ./internal/customers/gen/ ./internal/customers/
```
Expected: `internal/openapi/specs/customers.yaml` and `internal/customers/gen/api.gen.go` change, the second generate adds nothing, **everything builds** (no interface method was added — only types and two optional params), and all three test targets are green. `openapi/COVERAGE.md` must NOT move: no operation changed.

Then read the generated types before Tasks 3 and 4 write Go against them:

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server && grep -n 'CustomerOwner\|CustomerTag\|CustomerAssignableUser\|OwnerId\|TagId\|Tags ' internal/customers/gen/api.gen.go | head -40
```
Note in particular: whether `SafeCustomerResponse.Owner` is `*CustomerOwner` and `SafeCustomerResponse.Tags` is `*[]CustomerTag` (an optional array becomes a pointer-to-slice, and Task 3 must point it at a **non-nil** slice so the wire carries `[]` rather than `null`), whether `CustomerTag.Color` is `*string`, and the spelling of `CustomerOwner.UserId` / `CustomerAssignableUser.UserId` (oapi-codegen keeps the JSON casing: `UserId`, not `UserID`).

- [ ] **Step 5: Regenerate the TypeScript client**

```bash
cd /home/anders/projects/vantigo/vantigo && mise exec -- bun run gen:client && git status --short
```
Expected: each `api-schema.d.ts` that embeds the customers contract changes. **`openapi/testdata/exchanges/customers.jsonl` must not appear in `git status`.**

- [ ] **Step 6: Commit**

```bash
cd /home/anders/projects/vantigo/vantigo
printf '%s\n\n%s\n' 'feat(customers): the contract vocabulary for an owner, tags and the two new list filters' 'Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>' > /tmp/msg-owner-task2
git add openapi/customers.yaml apps/server/internal/openapi/specs/customers.yaml apps/server/internal/customers/gen/api.gen.go
git add $(git status --porcelain | awk '/api-schema.d.ts/ {print $2}')
git commit -F /tmp/msg-owner-task2 -- openapi/customers.yaml apps/server/internal/openapi/specs/customers.yaml apps/server/internal/customers/gen/api.gen.go $(git diff --cached --name-only | grep api-schema.d.ts)
git show --stat HEAD && git status --short
```

---

### Task 3: The owner, end to end on the server (D1, D4)

**Files:**
- Create: `apps/server/internal/customers/owner.go`, `apps/server/internal/customers/owner_test.go`
- Modify: `apps/server/internal/customers/customers.go` (`customerRow.OwnerUserID`, `customerRowFrom`, the four adapters, `safeCustomerResponse`, `validateGetCustomersParams`, `GetCustomers`), `apps/server/internal/customers/timeline_events.go` (`recordCustomerOwnerChanged`), `apps/server/internal/customers/contact_info.go` and `customer_type.go` (their `safeCustomerResponse` calls), `apps/server/internal/customers/customers_test.go` (`customerJSON` gains `owner` and `tags`), `apps/server/internal/customers/harness_test.go` (three user fixtures)
- Read first (do not change): `apps/server/internal/customers/contact_info.go` (the whole handler — the owner PUT is its shape, step for step), `actor.go` (the principal, the `Unknown user` precedent, the "before the transaction" rule), `apps/server/internal/projects/people.go:30-46` (`assignableUserLimit`, `userNotFound`, `userDisabled`) and `:356-422` (`GetProjectsByIdAssignableUsers`), `apps/server/internal/contracts/users.go` (both rules in its doc comment), `apps/server/internal/customers/store/customers.sql.go` (what Task 1 generated)

**This task also builds the tags half of the decoration**, because `safeCustomerResponse` must change shape exactly once and `tags` is part of that shape: `customerDecoration` below carries both, and `decorate` loads both. Task 4 adds the tag *endpoints* and the `tagId` filter on top, and asserts tags on the list and the detail from the writing side.

**Interfaces:**
- Consumes: `store.GetCustomerRow.OwnerUserID`, `store.UpdateCustomerOwnerParams{OwnerUserID, UpdatedAt, ID, ExpectedRevision}`, `store.UpdateCustomerOwnerRow`, `store.CustomerTagsForCustomers`, `store.CountCustomersParams{…, OwnerNone, OwnerID, TagID}`, `store.ListCustomersParams{…, OwnerNone, OwnerID, TagID}` (Task 1); `gen.CustomerOwner`, `gen.CustomerTag`, `gen.CustomerAssignableUser`, `gen.PutCustomerOwnerRequest`, `gen.GetCustomersParams.OwnerId/TagId` (Task 2).
- Produces:
```go
const assignableOwnerLimit = 20
const unknownOwnerDisplay  = "Unknown user"

type customerDecoration struct { /* owners, tags */ }
func (s *server) decorate(ctx context.Context, q *store.Queries, rows ...customerRow) (customerDecoration, error)
func (d customerDecoration) owner(id *uuid.UUID) *gen.CustomerOwner
func (d customerDecoration) tagsFor(customerID int32) []gen.CustomerTag

func safeCustomerResponse(row customerRow, includeIdentity bool, dec customerDecoration) gen.SafeCustomerResponse
func ownerNotFound(id uuid.UUID) string   // "User %s does not exist"
func ownerDisabled(id uuid.UUID) string   // "User %s is disabled and cannot own a customer"
func uuidPtrEqual(a, b *uuid.UUID) bool
func fromUpdateCustomerOwnerRow(c store.UpdateCustomerOwnerRow, ts store.CustomerTimelineSummaryRow) customerRow

func (s *server) PutCustomersByIdOwner(ctx context.Context, req gen.PutCustomersByIdOwnerRequestObject) (gen.PutCustomersByIdOwnerResponseObject, error)
func (s *server) GetCustomersAssignableUsers(ctx context.Context, req gen.GetCustomersAssignableUsersRequestObject) (gen.GetCustomersAssignableUsersResponseObject, error)

// timeline_events.go
type ownerSnapshot struct { UserID uuid.UUID `json:"userId"`; DisplayName string `json:"displayName"` }
func recordCustomerOwnerChanged(ctx context.Context, q *store.Queries, now time.Time, customerID int32, before, after *ownerSnapshot, actorKind, actorDisplay string, actorUserID *uuid.UUID) error
```
- Produces (test fixtures, in `harness_test.go`, reused by Tasks 4's tests):
```go
func setDisplayName(t *testing.T, h *modtest.Harness, userID uuid.UUID, name string)
func disableUser(t *testing.T, h *modtest.Harness, userID uuid.UUID)
func forgetUser(t *testing.T, h *modtest.Harness, userID uuid.UUID)
```

- [ ] **Step 1: Add the user fixtures the tests need**

There is **no users fake** in `modtest`: the harness composes the real identity module, so `Deps.Users` reads `identity.users`, and a test shapes the directory's answers by writing that table. `h.SignInUser(t, perms…)` returns the signed-in user's id (that is the id `ownerId=me` must resolve to), and `seedUser` gives every user a generated email as its `display_name` — so a test that asserts a name sets one. Append to `apps/server/internal/customers/harness_test.go`:

```go
// setDisplayName, disableUser and forgetUser are the three states
// contracts.UserDirectory can report a customer's owner in (owner and tags
// design D1). There is no users fake to configure: modtest composes the real
// identity module, so Deps.Users reads identity.users and these three
// statements are how a test shapes what it answers — the same way projects'
// own people tests do it (internal/projects/people_test.go:96-109).
//
// setDisplayName exists because modtest seeds a user whose display_name is a
// generated email address: a test that asserts the owner's name must put a
// name there itself rather than hard-coding what modtest happens to generate.
func setDisplayName(t *testing.T, h *modtest.Harness, userID uuid.UUID, name string) {
	t.Helper()
	h.Exec(t, `UPDATE identity.users SET display_name = $2 WHERE id = $1`, userID, name)
}

// disableUser flips the flag the directory reports as Active: false. An owner
// disabled AFTER being assigned keeps the customer (design D1): nothing is
// silently revoked, so this is the fixture for "the name is still shown, with
// an inactive hint", not for a customer that lost its owner.
func disableUser(t *testing.T, h *modtest.Harness, userID uuid.UUID) {
	t.Helper()
	h.Exec(t, `UPDATE identity.users SET is_disabled = true WHERE id = $1`, userID)
}

// forgetUser removes the account entirely, which is how a stored owner_user_id
// ends up naming a user the directory returns nothing for — the case that must
// read as "Unknown user", inactive, rather than as a 500 or a vanished owner
// (the actorFor precedent, actor.go). There is deliberately no foreign key
// from customers.customers to identity.users (migration 00024), which is what
// makes this state reachable at all.
func forgetUser(t *testing.T, h *modtest.Harness, userID uuid.UUID) {
	t.Helper()
	h.Exec(t, `DELETE FROM identity.users WHERE id = $1`, userID)
}
```

And extend `customerJSON` in `customers_test.go` with the two new response fields, plus the two decoding structs (`tagJSON` is reused by Task 4):

```go
	Owner          *ownerJSON        `json:"owner"`
	Tags           []tagJSON         `json:"tags"`
```

```go
// ownerJSON decodes CustomerOwner, a pointer on customerJSON because it is
// genuinely absent for an unowned customer (owner and tags design D1) — unlike
// contactInfo, which the server always sends.
type ownerJSON struct {
	UserId      string `json:"userId"`
	DisplayName string `json:"displayName"`
	Active      bool   `json:"active"`
}

// tagJSON decodes CustomerTag. A plain slice, not a pointer: the server always
// sends tags, empty array included, so a nil here means the field was missing
// and that is a failure worth seeing as one (owner and tags design D2).
type tagJSON struct {
	Id    string  `json:"id"`
	Name  string  `json:"name"`
	Color *string `json:"color"`
}
```

- [ ] **Step 2: Put the owner's two operations on the contract**

The paths travel with their handlers (Task 2's own note says why). Add `/api/v1/customers/{id}/owner` to `openapi/customers.yaml` between `/api/v1/customers/{id}/legal-identity` and `/api/v1/customers/{id}/peppol-lookup`:

```yaml
    /api/v1/customers/{id}/owner:
        put:
            operationId: putCustomersByIdOwner
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
                            $ref: '#/components/schemas/PutCustomerOwnerRequest'
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
            summary: Set or clear a customer's owner
            tags:
                - Customers
            x-vantigo-access: permission:customers:update+customers:view
```

and `/api/v1/customers/assignable-users` between the `/api/v1/customers/{id}/…` block and `/api/v1/customers/contacts`:

```yaml
    /api/v1/customers/assignable-users:
        get:
            operationId: getCustomersAssignableUsers
            parameters:
                - in: query
                  name: query
                  schema:
                    description: Narrows the directory search by display name (and, as the directory chooses, by email). Absent or blank lists the first page of active users.
                    type: string
                - in: query
                  name: limit
                  schema:
                    description: How many users to answer, 1-20. Defaults to 20.
                    format: int32
                    type: integer
            responses:
                "200":
                    content:
                        application/json:
                            schema:
                                items:
                                    $ref: '#/components/schemas/CustomerAssignableUser'
                                type: array
                    description: OK
                "400":
                    content:
                        application/problem+json:
                            schema:
                                $ref: common.yaml#/components/schemas/ProblemDetails
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
            summary: Search users assignable as a customer's owner
            tags:
                - Customers
            x-vantigo-access: permission:customers:update
```

Then generate and pin the new route conflicts:

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server && mise exec -- go generate ./... && mise exec -- go generate ./... && git status --short
cd /home/anders/projects/vantigo/vantigo/apps/server && mise exec -- go test -count=1 -run 'TestServeMuxConflictsArePinned' ./internal/openapi/
```
Expected: FAIL, printing the pairs it found against the pinned list. **Take the pairs from that output**, not from this plan, and add the missing ones to `KnownServeMuxConflicts` in `apps/server/internal/openapi/openapi_test.go`, keeping the slice's alphabetical order. The one this step is expected to add, so a surprise is recognisable as one:

```go
	"PUT /api/v1/customers/contacts/{id} ⟷ PUT /api/v1/customers/{id}/owner",
```

…and that is the whole list: `GET /api/v1/customers/assignable-users` does **not** conflict with `GET /api/v1/customers/{id}`. ServeMux (1.22+) applies literal-over-wildcard precedence when one pattern matches a strict subset of the other, which is exactly this case — and the proof is already in the file: `/api/v1/customers/contacts` has stood beside `/api/v1/customers/{id}` since the port and has never been listed. A pair appears only when neither pattern is more specific. (`PUT /api/v1/customers/tags/{tagId}` will add its own six in Task 4, `{id}/owner` among them.)

Run the router half too — the same list is what `TestKnownServeMuxConflictsMountOnModuleRouter` (`module_router_test.go`) registers on the real precedence-aware router and requires it to route:

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server && mise exec -- go test -count=1 ./internal/openapi/... && mise exec -- go test -count=1 ./internal/customers/gen/
```

Regenerate the operation coverage table and the client:

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server && mise exec -- go run ./internal/openapi/cmd/contract coverage -corpus ../../openapi/testdata/exchanges -out ../../openapi/COVERAGE.md
cd /home/anders/projects/vantigo/vantigo && mise exec -- bun run gen:client && git status --short
```
Expected: `openapi/COVERAGE.md` gains the two new operations as uncovered by the corpus (the frozen corpus predates them — that is what frozen means, and it is not a defect), and each `api-schema.d.ts` that embeds the contract changes. `openapi/testdata/exchanges/customers.jsonl` must not appear.

From here until Step 8 the customers package does not build: `gen.StrictServerInterface` now has two methods `*server` does not. `mise exec -- go build ./internal/customers/` naming exactly `PutCustomersByIdOwner` and `GetCustomersAssignableUsers` is the expected state, and the next steps are what clear it.

- [ ] **Step 3: Write the failing owner tests**

Create `apps/server/internal/customers/owner_test.go`:

```go
package customers_test

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is the owner half of phase 4 delivery A (owner and tags design
// D1, D4): PUT /customers/{id}/owner, GET /customers/assignable-users, the
// owner on every customer response, and the list's ownerId filter. It is
// shaped after contact_info_test.go, because the owner PUT is contact info's
// PUT with one column instead of three — the happy path, the validation
// refusals, the revision guard, the no-op rule, the generated timeline event
// with its actor, the permission gate.
//
// What is new here, and has no analogue in the module so far, is that the
// value being written belongs to ANOTHER module's data: identity's users,
// reached only through contracts.UserDirectory. The three states that
// directory can report — a name, a disabled account, no account at all — are
// each a test below, and each is arranged with harness_test.go's own fixtures
// against identity.users, because modtest composes the real identity module
// rather than a fake.

// putOwner PUTs /customers/{id}/owner with body and returns the raw response,
// leaving status assertions to the caller.
func putOwner(t *testing.T, c *modtest.Client, id int32, body map[string]any) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/owner", id), body)
}

// assignableUserJSON decodes CustomerAssignableUser.
type assignableUserJSON struct {
	UserId      string `json:"userId"`
	DisplayName string `json:"displayName"`
}

// getAssignableUsers GETs /customers/assignable-users with the given query
// string (no leading '?') and decodes a 200.
func getAssignableUsers(t *testing.T, c *modtest.Client, query string) []assignableUserJSON {
	t.Helper()
	path := "/api/v1/customers/assignable-users"
	if query != "" {
		path += "?" + query
	}
	r := c.Do(http.MethodGet, path, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("GET %s: status %d body %s, want 200", path, r.Status, r.Body)
	}
	var users []assignableUserJSON
	r.JSON(&users)
	return users
}

// TestPutCustomersByIdOwner_SetsAndClears_ShowsInGetAndList pins the whole
// visible behaviour of an owner: the PUT answers the customer with the owner
// named from the DIRECTORY (never from anything stored on the customer), the
// detail read and the list row agree with it, and clearing it makes the field
// absent again rather than present-and-null.
func TestPutCustomersByIdOwner_SetsAndClears_ShowsInGetAndList(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, callerID := authenticatedClientWithID(t, h)
	setDisplayName(t, h, callerID, "Kari Nordmann")
	created := createCustomer(t, c, "Owned Co")

	r := putOwner(t, c, created.Id, map[string]any{"ownerUserId": callerID.String()})
	if r.Status != http.StatusOK {
		t.Fatalf("set: status %d body %s, want 200", r.Status, r.Body)
	}
	var owned customerJSON
	r.JSON(&owned)
	if owned.Owner == nil || owned.Owner.UserId != callerID.String() ||
		owned.Owner.DisplayName != "Kari Nordmann" || !owned.Owner.Active {
		t.Fatalf("owner = %+v, want %s/Kari Nordmann/active", owned.Owner, callerID)
	}

	if got := fetchCustomerJSON(t, c, created.Id); got.Owner == nil || got.Owner.DisplayName != "Kari Nordmann" {
		t.Errorf("GET owner = %+v, want Kari Nordmann", got.Owner)
	}
	list := getList(t, c, "search="+url.QueryEscape("Owned Co"))
	if len(list.Data) != 1 || list.Data[0].Owner == nil || list.Data[0].Owner.DisplayName != "Kari Nordmann" {
		t.Errorf("list owner = %+v, want Kari Nordmann", list.Data)
	}

	r = putOwner(t, c, created.Id, map[string]any{"ownerUserId": nil})
	if r.Status != http.StatusOK {
		t.Fatalf("clear: status %d body %s, want 200", r.Status, r.Body)
	}
	var cleared customerJSON
	r.JSON(&cleared)
	if cleared.Owner != nil {
		t.Errorf("owner = %+v after clearing, want absent", cleared.Owner)
	}
	// An absent body field means the same as null, as every full-replace body
	// in this module does (customers foundation design D1).
	if r := putOwner(t, c, created.Id, map[string]any{}); r.Status != http.StatusOK {
		t.Fatalf("clear with an empty body: status %d body %s, want 200", r.Status, r.Body)
	}
}

// TestPutCustomersByIdOwner_RefusesAUserWhoCannotOwn pins both field errors
// design D1 names, with projects' own wording: a user id nobody holds, and a
// disabled account. Both are 400s keyed ownerUserId, not 404s — the customer
// exists and the caller may edit it, so what is wrong is the body.
func TestPutCustomersByIdOwner_RefusesAUserWhoCannotOwn(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	created := createCustomer(t, c, "Refuser Co")

	missing := uuid.New()
	r := putOwner(t, c, created.Id, map[string]any{"ownerUserId": missing.String()})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("unknown user: status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	want := fmt.Sprintf("User %s does not exist", missing)
	if got := problem.Errors["ownerUserId"]; len(got) != 1 || got[0] != want {
		t.Errorf("errors[ownerUserId] = %v, want [%s]", got, want)
	}

	_, disabledID := h.SignInUser(t, "customers:view")
	disableUser(t, h, disabledID)
	r = putOwner(t, c, created.Id, map[string]any{"ownerUserId": disabledID.String()})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("disabled user: status %d body %s, want 400", r.Status, r.Body)
	}
	r.JSON(&problem)
	want = fmt.Sprintf("User %s is disabled and cannot own a customer", disabledID)
	if got := problem.Errors["ownerUserId"]; len(got) != 1 || got[0] != want {
		t.Errorf("errors[ownerUserId] = %v, want [%s]", got, want)
	}

	if got := fetchCustomerJSON(t, c, created.Id); got.Owner != nil || got.Revision != 1 {
		t.Errorf("after two refusals: owner = %+v revision = %d, want no owner and revision 1", got.Owner, got.Revision)
	}
}

// TestPutCustomersByIdOwner_AnOwnerDisabledAfterwardsKeepsTheCustomer is
// design D1's explicit ruling, and the reason the check above is on the
// WRITE alone: an account disabled later is reported inactive, and the
// customer still has an owner. Nothing revokes it behind the caller's back.
func TestPutCustomersByIdOwner_AnOwnerDisabledAfterwardsKeepsTheCustomer(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	_, ownerID := h.SignInUser(t, "customers:view")
	setDisplayName(t, h, ownerID, "Ola Nordmann")
	created := createCustomer(t, c, "Inactive Owner Co")

	if r := putOwner(t, c, created.Id, map[string]any{"ownerUserId": ownerID.String()}); r.Status != http.StatusOK {
		t.Fatalf("set: status %d body %s, want 200", r.Status, r.Body)
	}
	disableUser(t, h, ownerID)

	got := fetchCustomerJSON(t, c, created.Id)
	if got.Owner == nil || got.Owner.DisplayName != "Ola Nordmann" || got.Owner.Active {
		t.Errorf("owner = %+v, want Ola Nordmann with active false", got.Owner)
	}
}

// TestGetCustomer_AnOwnerTheDirectoryForgotIsUnknownUser pins the actorFor
// precedent (design D1): an owner whose account is gone is still an owner, and
// it reads as Unknown user with active false — not as a 500, and not as an
// unowned customer.
func TestGetCustomer_AnOwnerTheDirectoryForgotIsUnknownUser(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	_, ownerID := h.SignInUser(t, "customers:view")
	created := createCustomer(t, c, "Forgotten Owner Co")
	if r := putOwner(t, c, created.Id, map[string]any{"ownerUserId": ownerID.String()}); r.Status != http.StatusOK {
		t.Fatalf("set: status %d body %s, want 200", r.Status, r.Body)
	}
	forgetUser(t, h, ownerID)

	got := fetchCustomerJSON(t, c, created.Id)
	if got.Owner == nil || got.Owner.UserId != ownerID.String() ||
		got.Owner.DisplayName != "Unknown user" || got.Owner.Active {
		t.Errorf("owner = %+v, want %s/Unknown user/inactive", got.Owner, ownerID)
	}
}

// TestPutCustomersByIdOwner_StaleRevisionIsAConflictAndWritesNothing pins the
// revision guard ahead of the no-op check, exactly as contact info's own PUT
// orders them: resubmitting with a stale revision is a conflict, not a free
// pass.
func TestPutCustomersByIdOwner_StaleRevisionIsAConflictAndWritesNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, callerID := authenticatedClientWithID(t, h)
	created := createCustomer(t, c, "Stale Owner Co")
	if r := putOwner(t, c, created.Id, map[string]any{"ownerUserId": callerID.String(), "revision": 1}); r.Status != http.StatusOK {
		t.Fatalf("first set: status %d body %s, want 200", r.Status, r.Body)
	}

	r := putOwner(t, c, created.Id, map[string]any{"ownerUserId": nil, "revision": 1})
	if r.Status != http.StatusConflict {
		t.Fatalf("stale: status %d body %s, want 409", r.Status, r.Body)
	}
	var conflict conflictProblemJSON
	r.JSON(&conflict)
	if conflict.Title != "Customer revision conflict" || conflict.Code != nil {
		t.Errorf("conflict = %+v, want the revision conflict with no code", conflict)
	}
	got := fetchCustomerJSON(t, c, created.Id)
	if got.Owner == nil || got.Revision != 2 {
		t.Errorf("after the conflict: owner = %+v revision = %d, want the owner kept at revision 2", got.Owner, got.Revision)
	}
}

// TestPutCustomersByIdOwner_NoOpWritesNothing pins the no-op rule (customers
// foundation design D5): re-sending the owner the customer already has bumps
// no revision, moves no updated_at and records no event. Both directions are
// here, because "clear an already-unowned customer" is the case an
// implementation that only compares non-nil values gets wrong.
func TestPutCustomersByIdOwner_NoOpWritesNothing(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, callerID := authenticatedClientWithID(t, h)
	created := createCustomer(t, c, "Idempotent Owner Co")

	if r := putOwner(t, c, created.Id, map[string]any{"ownerUserId": nil}); r.Status != http.StatusOK {
		t.Fatalf("clear an unowned customer: status %d body %s, want 200", r.Status, r.Body)
	}
	if got := fetchCustomerJSON(t, c, created.Id); got.Revision != 1 {
		t.Errorf("revision = %d after clearing an unowned customer, want 1", got.Revision)
	}

	if r := putOwner(t, c, created.Id, map[string]any{"ownerUserId": callerID.String()}); r.Status != http.StatusOK {
		t.Fatalf("set: status %d body %s, want 200", r.Status, r.Body)
	}
	before := fetchCustomerJSON(t, c, created.Id)
	if r := putOwner(t, c, created.Id, map[string]any{"ownerUserId": callerID.String()}); r.Status != http.StatusOK {
		t.Fatalf("re-set: status %d body %s, want 200", r.Status, r.Body)
	}
	after := fetchCustomerJSON(t, c, created.Id)
	if after.Revision != before.Revision || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Errorf("re-setting the same owner moved revision %d→%d / updatedAt %v→%v, want neither",
			before.Revision, after.Revision, before.UpdatedAt, after.UpdatedAt)
	}
	if n := countTimelineEvents(t, h, created.Id, "customer.owner_changed"); n != 1 {
		t.Errorf("customer.owner_changed events = %d, want 1 (the no-op records nothing)", n)
	}
}

// TestPutCustomersByIdOwner_RecordsTheEventWithBothNamesAndTheActor pins
// customer.owner_changed's payload (design D1): before and after each carry
// the user id AND the display name resolved at write time, so the timeline
// still reads correctly after the account is renamed or removed — and the
// entry is attributed to whoever made the change, not to the new owner.
func TestPutCustomersByIdOwner_RecordsTheEventWithBothNamesAndTheActor(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, callerID := authenticatedClientWithID(t, h)
	setDisplayName(t, h, callerID, "Kari Nordmann")
	_, secondID := h.SignInUser(t, "customers:view")
	setDisplayName(t, h, secondID, "Ola Nordmann")
	created := createCustomer(t, c, "Event Owner Co")

	if r := putOwner(t, c, created.Id, map[string]any{"ownerUserId": callerID.String()}); r.Status != http.StatusOK {
		t.Fatalf("first set: status %d body %s, want 200", r.Status, r.Body)
	}
	if r := putOwner(t, c, created.Id, map[string]any{"ownerUserId": secondID.String()}); r.Status != http.StatusOK {
		t.Fatalf("reassign: status %d body %s, want 200", r.Status, r.Body)
	}

	if n := countTimelineEvents(t, h, created.Id, "customer.owner_changed"); n != 2 {
		t.Fatalf("customer.owner_changed events = %d, want 2", n)
	}
	event := fetchTimelineEvent(t, h, created.Id, "customer.owner_changed")
	before, _ := event.Payload["before"].(map[string]any)
	after, _ := event.Payload["after"].(map[string]any)
	if before == nil || before["displayName"] != "Kari Nordmann" || before["userId"] != callerID.String() {
		t.Errorf("before = %v, want Kari Nordmann / %s", before, callerID)
	}
	if after == nil || after["displayName"] != "Ola Nordmann" || after["userId"] != secondID.String() {
		t.Errorf("after = %v, want Ola Nordmann / %s", after, secondID)
	}
	if event.Summary != "Customer owner changed: Kari Nordmann → Ola Nordmann" {
		t.Errorf("summary = %q, want the two names", event.Summary)
	}

	gotActor := modtest.One[string](t, h, `
		SELECT actor_user_id::text FROM customers.customers_timeline_entries
		WHERE customer_id = $1 AND event_type = 'customer.owner_changed' ORDER BY id DESC LIMIT 1`, created.Id)
	if gotActor != callerID.String() {
		t.Errorf("actor_user_id = %s, want %s (whoever reassigned, not the new owner)", gotActor, callerID)
	}

	// Clearing it records a third event whose after is null, so the timeline
	// says who stopped owning the customer rather than going quiet.
	if r := putOwner(t, c, created.Id, map[string]any{"ownerUserId": nil}); r.Status != http.StatusOK {
		t.Fatalf("clear: status %d body %s, want 200", r.Status, r.Body)
	}
	cleared := fetchTimelineEvent(t, h, created.Id, "customer.owner_changed")
	if cleared.Payload["after"] != nil {
		t.Errorf("after = %v on the clearing event, want null", cleared.Payload["after"])
	}
	if cleared.Summary != "Customer owner changed: Ola Nordmann → nobody" {
		t.Errorf("summary = %q, want the clearing wording", cleared.Summary)
	}
}

// TestGetCustomers_OwnerIdFilter pins design D1's three accepted values
// against one another on one dataset, plus the refusal. 'me' is resolved from
// the SESSION — the request never names the caller — which is why the caller
// owns one of these customers and asserts it finds exactly that one.
func TestGetCustomers_OwnerIdFilter(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, callerID := authenticatedClientWithID(t, h)
	_, otherID := h.SignInUser(t, "customers:view")

	mine := createCustomer(t, c, "Filter Mine Co")
	theirs := createCustomer(t, c, "Filter Theirs Co")
	createCustomer(t, c, "Filter Nobody Co")
	if r := putOwner(t, c, mine.Id, map[string]any{"ownerUserId": callerID.String()}); r.Status != http.StatusOK {
		t.Fatalf("own mine: status %d body %s", r.Status, r.Body)
	}
	if r := putOwner(t, c, theirs.Id, map[string]any{"ownerUserId": otherID.String()}); r.Status != http.StatusOK {
		t.Fatalf("own theirs: status %d body %s", r.Status, r.Body)
	}

	names := func(query string) []string {
		list := getList(t, c, query+"&search="+url.QueryEscape("Filter "))
		out := make([]string, 0, len(list.Data))
		for _, row := range list.Data {
			out = append(out, row.Name)
		}
		return out
	}
	if got := names("ownerId=me"); len(got) != 1 || got[0] != "Filter Mine Co" {
		t.Errorf("ownerId=me = %v, want [Filter Mine Co]", got)
	}
	if got := names("ownerId=" + otherID.String()); len(got) != 1 || got[0] != "Filter Theirs Co" {
		t.Errorf("ownerId=<other> = %v, want [Filter Theirs Co]", got)
	}
	if got := names("ownerId=none"); len(got) != 1 || got[0] != "Filter Nobody Co" {
		t.Errorf("ownerId=none = %v, want [Filter Nobody Co]", got)
	}
	// The count must agree with the page: the two list queries share their
	// WHERE clause by hand, and a filter added to one and not the other is
	// exactly the drift their comments warn about.
	if list := getList(t, c, "ownerId=me&search="+url.QueryEscape("Filter ")); list.Pagination.TotalCount != 1 {
		t.Errorf("totalCount = %d for ownerId=me, want 1", list.Pagination.TotalCount)
	}

	r := c.Do(http.MethodGet, "/api/v1/customers?ownerId=someone", nil)
	if r.Status != http.StatusBadRequest {
		t.Fatalf("ownerId=someone: status %d body %s, want 400", r.Status, r.Body)
	}
	if want := "'ownerId' must be a user id, 'me' or 'none', but was 'someone'."; !strings.Contains(r.Body, want) {
		t.Errorf("body = %s, want it to contain %q", r.Body, want)
	}
}

// TestGetCustomersAssignableUsers pins design D1's picker endpoint: the
// directory's active users only, narrowed by query, capped at 20, and a limit
// outside 1-20 refused in this module's query-parameter wording.
func TestGetCustomersAssignableUsers(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, callerID := authenticatedClientWithID(t, h)
	setDisplayName(t, h, callerID, "Searchable Kari")
	_, activeID := h.SignInUser(t, "customers:view")
	setDisplayName(t, h, activeID, "Searchable Ola")
	_, disabledID := h.SignInUser(t, "customers:view")
	setDisplayName(t, h, disabledID, "Searchable Nils")
	disableUser(t, h, disabledID)

	found := getAssignableUsers(t, c, "query="+url.QueryEscape("Searchable"))
	names := make([]string, 0, len(found))
	for _, u := range found {
		names = append(names, u.DisplayName)
	}
	if len(names) != 2 || names[0] != "Searchable Kari" || names[1] != "Searchable Ola" {
		t.Errorf("assignable users = %v, want [Searchable Kari Searchable Ola] — active only, display-name order", names)
	}

	if got := getAssignableUsers(t, c, "query="+url.QueryEscape("Searchable")+"&limit=1"); len(got) != 1 {
		t.Errorf("limit=1 answered %d users, want 1", len(got))
	}
	if got := len(getAssignableUsers(t, c, "")); got > 20 {
		t.Errorf("no query answered %d users, want at most 20", got)
	}

	for _, limit := range []string{"0", "21"} {
		r := c.Do(http.MethodGet, "/api/v1/customers/assignable-users?limit="+limit, nil)
		if r.Status != http.StatusBadRequest {
			t.Errorf("limit=%s: status %d body %s, want 400", limit, r.Status, r.Body)
		}
		if want := fmt.Sprintf("'limit' must be between 1 and 20, but was %s.", limit); !strings.Contains(r.Body, want) {
			t.Errorf("limit=%s body = %s, want it to contain %q", limit, r.Body, want)
		}
	}
}

// TestOwnerPermissions pins design D4's answer: an owner is not sensitive
// data. Reading one needs nothing beyond customers:view, and writing one
// needs customers:update and no new key — the router enforces both from
// x-vantigo-access, and these two cases are what would fail if a narrower key
// were ever introduced without the catalog and the contract agreeing.
func TestOwnerPermissions(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	writer, ownerID := authenticatedClientWithID(t, h)
	setDisplayName(t, h, ownerID, "Kari Nordmann")
	created := createCustomer(t, writer, "Permission Owner Co")
	if r := putOwner(t, writer, created.Id, map[string]any{"ownerUserId": ownerID.String()}); r.Status != http.StatusOK {
		t.Fatalf("set: status %d body %s, want 200", r.Status, r.Body)
	}

	viewer := h.SignIn(t, "customers:view")
	if got := fetchCustomerJSON(t, viewer, created.Id); got.Owner == nil || got.Owner.DisplayName != "Kari Nordmann" {
		t.Errorf("a view-only caller saw owner = %+v, want Kari Nordmann", got.Owner)
	}
	if r := putOwner(t, viewer, created.Id, map[string]any{"ownerUserId": nil}); r.Status != http.StatusForbidden {
		t.Errorf("view-only PUT: status %d body %s, want 403", r.Status, r.Body)
	}
	if r := viewer.Do(http.MethodGet, "/api/v1/customers/assignable-users", nil); r.Status != http.StatusForbidden {
		t.Errorf("view-only assignable-users: status %d body %s, want 403", r.Status, r.Body)
	}
}

// TestPutCustomersByIdOwner_WhenCustomerDoesNotExist_ReturnsNotFound
func TestPutCustomersByIdOwner_WhenCustomerDoesNotExist_ReturnsNotFound(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, callerID := authenticatedClientWithID(t, h)

	r := putOwner(t, c, 999999, map[string]any{"ownerUserId": callerID.String()})
	if r.Status != http.StatusNotFound {
		t.Errorf("status %d body %s, want 404", r.Status, r.Body)
	}
}
```

Two things this file needs that may not exist yet — check before writing, and add only what is missing:

- `strings` in the import list (used by the two `strings.Contains` assertions).
- `conflictProblemJSON` with at least `Title string` and `Code *string`. Grep for it: `grep -rn 'conflictProblemJSON\|Code \*string' apps/server/internal/customers/*_test.go`. If the module already decodes `CustomerConflictProblem` under another name, use that name; if not, add it beside `validationProblemJSON` in `customers_test.go`:

```go
// conflictProblemJSON decodes CustomerConflictProblem far enough to tell a
// revision conflict (a title and no code) from a coded one such as
// tag_exists (customers foundation design D5, D6).
type conflictProblemJSON struct {
	Title string  `json:"title"`
	Code  *string `json:"code"`
}
```


- [ ] **Step 4: Run the owner tests to verify they fail**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server && mise exec -- go test -count=1 -run 'Owner|AssignableUsers' ./internal/customers/
```
Expected: a build failure — `*server` does not implement `gen.StrictServerInterface` (`PutCustomersByIdOwner`, `GetCustomersAssignableUsers` missing), `customerJSON` has no `Owner` field, `undefined: putOwner`. Once Steps 5–7 land, the failures become real assertion failures and then pass; there must be no point at which a test in this file passes without its implementation.

- [ ] **Step 5: Carry the owner column and the decoration through `customers.go`**

Five edits in `apps/server/internal/customers/customers.go`.

(a) `customerRow` gains the column, after `Website`:

```go
	OwnerUserID      *uuid.UUID
```

(b) `customerRowFrom` takes it and sets it — the one place the field mapping lives, so each adapter stays a one-line unpacking:

```go
func customerRowFrom(id int32, customerNumber int64, name, status, typ string, legalCountry, legalID, legalName, legalSource, legalType *string, createdAt, updatedAt time.Time, revision int32, email, phone, website *string, ownerUserID *uuid.UUID, ts store.CustomerTimelineSummaryRow) customerRow {
	return customerRow{
		ID: id, CustomerNumber: customerNumber, Name: name, Status: status, Type: typ,
		LegalCountry: legalCountry, LegalID: legalID, LegalName: legalName, LegalSource: legalSource, LegalType: legalType,
		CreatedAt: createdAt, UpdatedAt: updatedAt, Revision: revision,
		Email: email, Phone: phone, Website: website, OwnerUserID: ownerUserID,
		EntryCount: ts.EntryCount, LatestOccurredOn: ts.LatestOccurredOn,
	}
}
```

(c) each of the four existing adapters passes `c.OwnerUserID` in the new position (`…, c.Email, c.Phone, c.Website, c.OwnerUserID, ts)`), `fromListRow` sets `OwnerUserID: r.OwnerUserID`, and a fifth adapter joins them:

```go
// fromUpdateCustomerOwnerRow is UpdateCustomerOwner's own row
// (PutCustomersByIdOwner's write, owner.go).
func fromUpdateCustomerOwnerRow(c store.UpdateCustomerOwnerRow, ts store.CustomerTimelineSummaryRow) customerRow {
	return customerRowFrom(c.ID, c.CustomerNumber, c.Name, c.Status, c.Type, c.LegalCountry, c.LegalID, c.LegalName, c.LegalSource, c.LegalType, c.CreatedAt, c.UpdatedAt, c.Revision, c.Email, c.Phone, c.Website, c.OwnerUserID, ts)
}
```

(d) `safeCustomerResponse` takes the decoration and sets both new fields. Note `Tags` is pointed at a slice that is never nil, so a customer with no tags sends `[]` and not `null` — the contract promises an array:

```go
// safeCustomerResponse is SafeCustomerResponse's projection … [keep the
// existing comment] … dec is the part of the response that does not come from
// the customer row: the owner's display name, which lives in identity's
// directory and is resolved per response (owner and tags design D1), and the
// customer's tags, which live in their own table (D2). Both are as ungated as
// contactInfo — design D4's ruling: an owner is not sensitive data and tags
// are classification, so customers:view is the whole gate.
func safeCustomerResponse(row customerRow, includeIdentity bool, dec customerDecoration) gen.SafeCustomerResponse {
	tags := dec.tagsFor(row.ID)
	resp := gen.SafeCustomerResponse{
		Id:             row.ID,
		CustomerNumber: row.CustomerNumber,
		Name:           row.Name,
		Status:         row.Status,
		Type:           &row.Type,
		CreatedAt:      row.CreatedAt,
		UpdatedAt:      row.UpdatedAt,
		Revision:       &row.Revision,
		ContactInfo:    &gen.CustomerContactInfo{Email: row.Email, Phone: row.Phone, Website: row.Website},
		Owner:          dec.owner(row.OwnerUserID),
		Tags:           &tags,
		TimelineSummary: gen.SafeTimelineSummary{
			EntryCount:       int32(row.EntryCount),
			LatestOccurredOn: dateFromPgtype(row.LatestOccurredOn),
		},
	}
	if includeIdentity && row.LegalCountry != nil {
		resp.Identity = &gen.SafeCustomerIdentity{Country: *row.LegalCountry, Type: *row.LegalType, Id: *row.LegalID}
	}
	return resp
}
```

(e) `validateGetCustomersParams` gains the two syntax checks, at the end, before `return errs`:

```go
	// ownerId and tagId are checked for SHAPE here and resolved in
	// GetCustomers: 'me' needs the request's principal, which this function
	// deliberately does not see — it is a pure parameter check, unit-tested as
	// one, and every message it collects is joined into the one 400 detail.
	if p.OwnerId != nil && !validOwnerFilter(*p.OwnerId) {
		errs = append(errs, fmt.Sprintf("'ownerId' must be a user id, 'me' or 'none', but was '%s'.", *p.OwnerId))
	}
	if p.TagId != nil && !validUUIDParam(*p.TagId) {
		errs = append(errs, fmt.Sprintf("'tagId' must be a tag id, but was '%s'.", *p.TagId))
	}
```

with the two predicates beside it:

```go
// validOwnerFilter and validUUIDParam are the ownerId/tagId parameter shapes
// (owner and tags design D1, D2). 'me' and 'none' are matched
// case-sensitively, as every other query parameter in this function is — a
// query parameter is never normalized here, so 'Me' is rejected rather than
// silently accepted.
func validOwnerFilter(raw string) bool {
	return raw == "me" || raw == "none" || validUUIDParam(raw)
}

func validUUIDParam(raw string) bool {
	_, err := uuid.Parse(raw)
	return err == nil
}
```

(f) `GetCustomers` resolves the two filters and decorates the page. After the existing `sortBy` block and before the two `hasPermission` calls, insert:

```go
	// ownerId's three forms resolve to the two SQL parameters the list queries
	// take (owner and tags design D1): 'none' is a NULL test, and both a user
	// id and 'me' are an equality — 'me' resolved from the SESSION, never from
	// anything the request says about who the caller is. The router already
	// refused an unauthenticated call (design D4), so the missing-principal
	// branch below is unreachable in production; it is a 400 rather than a
	// silent "everyone's customers", because answering the wrong customers is
	// the one outcome a "my customers" filter must never have.
	var ownerID *uuid.UUID
	ownerNone := false
	if req.Params.OwnerId != nil {
		switch *req.Params.OwnerId {
		case "none":
			ownerNone = true
		case "me":
			p, ok := contracts.PrincipalFrom(ctx)
			if !ok || p.UserID == uuid.Nil {
				return gen.GetCustomers400ApplicationProblemPlusJSONResponse(apicommon.Problem("Invalid query parameters",
					"'ownerId' cannot be 'me' without a signed-in user.")), nil
			}
			id := p.UserID
			ownerID = &id
		default:
			// validateGetCustomersParams already refused anything unparseable,
			// so err is impossible here; the guard means an impossible value
			// filters nothing rather than panicking.
			if id, err := uuid.Parse(*req.Params.OwnerId); err == nil {
				ownerID = &id
			}
		}
	}
	var tagID *uuid.UUID
	if req.Params.TagId != nil {
		if id, err := uuid.Parse(*req.Params.TagId); err == nil {
			tagID = &id
		}
	}
```

Add `OwnerNone: ownerNone, OwnerID: ownerID, TagID: tagID` to both the `store.CountCustomersParams` and the `store.ListCustomersParams` literals — to **both**, or the total count and the page disagree, which is exactly what those queries' comments warn about.

Finally, replace the response-building loop with one that decorates the whole page in one pass:

```go
	// One directory call and one tags query for the whole page (owner and tags
	// design D1, D2), never one per row: 25 rows would otherwise be 25
	// out-of-process calls for data that is on the wire either way.
	dec, err := s.decorate(ctx, q, rows...)
	if err != nil {
		return nil, err
	}
	data := make([]gen.SafeCustomerResponse, 0, len(rows))
	for _, r := range rows {
		data = append(data, safeCustomerResponse(r, searchIdentity, dec))
	}
```

`customers.go` then imports `github.com/google/uuid` and `github.com/vantigo-io/vantigo/server/internal/contracts` if it does not already.

- [ ] **Step 6: Write `owner.go`**

```go
package customers

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
	"github.com/vantigo-io/vantigo/server/internal/customers/store"
	"github.com/vantigo-io/vantigo/server/internal/db"
)

// This file is the owner half of phase 4 delivery A (owner and tags design
// D1): PUT /customers/{id}/owner, GET /customers/assignable-users, and the
// decoration every customer response goes through on its way out.
//
// The owner is a column on customers.customers, which is the decision the
// whole file follows from: it shares the row's revision, so this PUT is
// contact_info.go's PUT with one column instead of three — same ordering, same
// guard, same no-op rule, same 409 — and a concurrent edit cannot lose it.
// What is new is that the VALUE belongs to another module: identity's users,
// reachable only through contracts.UserDirectory (Deps.Users). That has two
// consequences neither of which is optional:
//
//   - every directory call happens OUTSIDE a transaction (actor.go's rule):
//     an out-of-process call under a row lock is how an outage becomes a
//     database incident. This handler resolves the candidate, the previous
//     owner's name and the actor before db.WithTx opens, and decorates the
//     response after it commits.
//   - a stored owner_user_id can outlive the account it names. Migration
//     00024 deliberately has no foreign key, so the three states the
//     directory can report — a name, a disabled account, nothing at all —
//     are all reachable, and customerDecoration.owner below is the one place
//     each of them turns into a response.

// assignableOwnerLimit is how many candidates the owner picker's search
// answers, and the same twenty projects' own assignable-user search settled on
// (internal/projects/people.go): a picker's first page, not a report. Someone
// who cannot find a colleague in twenty rows types more of their name.
const assignableOwnerLimit = 20

// unknownOwnerDisplay is what an owner the directory no longer knows is
// called, the same words actorFor gives a vanished actor (actor.go): the
// customer still HAS an owner — design D1 is explicit that nothing is
// silently revoked — and a response that dropped the field would claim
// otherwise.
const unknownOwnerDisplay = "Unknown user"

// ownerNotFound and ownerDisabled are the two ways ownerUserId can fail,
// worded as projects words its own (people.go:39-45). Both are field errors
// rather than 404s: the customer exists and the caller may edit it, so what is
// wrong is the body they sent.
func ownerNotFound(id uuid.UUID) string {
	return fmt.Sprintf("User %s does not exist", id)
}

func ownerDisabled(id uuid.UUID) string {
	return fmt.Sprintf("User %s is disabled and cannot own a customer", id)
}

// uuidPtrEqual reports whether a and b name the same user, nil (unowned)
// included — the comparison PutCustomersByIdOwner's no-op check makes. The
// nil/nil case is the one an implementation that only compares dereferenced
// values gets wrong, and it is a real request: clearing the owner of a
// customer that has none must write nothing at all.
func uuidPtrEqual(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// customerDecoration is everything a SafeCustomerResponse carries that is not
// on the customer row: the owner's display name, borrowed from identity's
// directory for the length of one response (owner and tags design D1), and the
// customer's tags, which live in their own table (D2). It exists so
// safeCustomerResponse knows one shape whether it is rendering one customer or
// a page of twenty-five, and so the two lookups happen once per response
// rather than once per row.
type customerDecoration struct {
	owners map[uuid.UUID]contracts.UserEntry
	tags   map[int32][]gen.CustomerTag
}

// decorate resolves the owners and tags of rows in one directory call and one
// query. q must be a pool-backed store.Queries, never a transaction's: the
// directory call inside is out-of-process and must not happen under a lock, so
// every caller decorates after its write has committed.
//
// An empty rows is not an error and makes no calls at all — an empty list page
// is an ordinary answer, and asking the directory about nobody is a round trip
// for a map that will stay empty.
func (s *server) decorate(ctx context.Context, q *store.Queries, rows ...customerRow) (customerDecoration, error) {
	dec := customerDecoration{
		owners: map[uuid.UUID]contracts.UserEntry{},
		tags:   map[int32][]gen.CustomerTag{},
	}
	if len(rows) == 0 {
		return dec, nil
	}

	customerIDs := make([]int32, 0, len(rows))
	userIDs := make([]uuid.UUID, 0, len(rows))
	seen := make(map[uuid.UUID]bool, len(rows))
	for _, r := range rows {
		customerIDs = append(customerIDs, r.ID)
		if r.OwnerUserID == nil || seen[*r.OwnerUserID] {
			continue
		}
		// Distinct ids only: a page where one person owns every row must ask
		// the directory about them once.
		seen[*r.OwnerUserID] = true
		userIDs = append(userIDs, *r.OwnerUserID)
	}

	links, err := q.CustomerTagsForCustomers(ctx, customerIDs)
	if err != nil {
		return customerDecoration{}, fmt.Errorf("customers: load customer tags: %w", err)
	}
	for _, l := range links {
		dec.tags[l.CustomerID] = append(dec.tags[l.CustomerID], gen.CustomerTag{Id: l.ID, Name: l.Name, Color: l.Color})
	}

	if len(userIDs) > 0 {
		users, err := s.deps.Users.Users(ctx, userIDs)
		if err != nil {
			// A directory that cannot be reached is a 500, not a page of
			// customers with their owners quietly missing: "unowned" is a
			// claim, and this module has no business making it up.
			return customerDecoration{}, fmt.Errorf("customers: resolve customer owners: %w", err)
		}
		for _, u := range users {
			dec.owners[u.ID] = u
		}
	}
	return dec, nil
}

// owner is one customer's owner as the contract reports it, or nil when the
// customer is unowned. An id the directory answered nothing for is still an
// owner — reported as unknownOwnerDisplay and inactive, the actorFor
// precedent — because contracts.UserDirectory's absence means "no such
// account", not "the lookup failed" (its own doc comment), and design D1 keeps
// the customer's owner either way.
func (d customerDecoration) owner(id *uuid.UUID) *gen.CustomerOwner {
	if id == nil {
		return nil
	}
	if u, ok := d.owners[*id]; ok {
		return &gen.CustomerOwner{UserId: u.ID, DisplayName: u.DisplayName, Active: u.Active}
	}
	return &gen.CustomerOwner{UserId: *id, DisplayName: unknownOwnerDisplay, Active: false}
}

// tagsFor is one customer's tags, never nil: the contract promises an array,
// and a customer with no tags answers [] rather than null (owner and tags
// design D2).
func (d customerDecoration) tagsFor(customerID int32) []gen.CustomerTag {
	if tags, ok := d.tags[customerID]; ok {
		return tags
	}
	return []gen.CustomerTag{}
}

// PutCustomersByIdOwner Set or clear a customer's owner
// (PUT /api/v1/customers/{id}/owner)
//
// Ordering is PutCustomersByIdContactInfo's, step for step, because the owner
// is a column on the same row: (1) the customer lookup, 404; (2) a supplied
// revision that disagrees with the row just read, 409 — ahead of the no-op
// check, so resubmitting the current owner with a stale revision is still a
// conflict; (3) the no-op check: the same owner (nil included) writes nothing
// at all (customers foundation design D5); (4) the candidate's own validation
// against the directory, 400 keyed ownerUserId; (5) the guarded write and its
// timeline event in one transaction.
//
// Steps 3 and 4 are in that order on purpose. The check "may this user own a
// customer" applies to a CHANGE of owner, and design D1 rules that an owner
// disabled after being assigned keeps the customer — so a no-op resubmit of a
// now-disabled owner must answer 200 with that owner, not a 400 telling the
// caller their own stored state is invalid.
func (s *server) PutCustomersByIdOwner(ctx context.Context, req gen.PutCustomersByIdOwnerRequestObject) (gen.PutCustomersByIdOwnerResponseObject, error) {
	body := gen.PutCustomerOwnerRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	q := store.New(s.deps.Pool)
	existing, err := q.GetCustomer(ctx, req.Id)
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PutCustomersByIdOwner404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("customers: get customer: %w", err)
	}

	if body.Revision != nil && *body.Revision != existing.Revision {
		return gen.PutCustomersByIdOwner409ApplicationProblemPlusJSONResponse(customerRevisionConflict(*body.Revision, existing.Revision)), nil
	}

	includeIdentity := s.hasPermission(ctx, legalIdentityView)
	before, after := existing.OwnerUserID, body.OwnerUserId

	respond := func(row customerRow) (gen.PutCustomersByIdOwnerResponseObject, error) {
		dec, err := s.decorate(ctx, q, row)
		if err != nil {
			return nil, err
		}
		return gen.PutCustomersByIdOwner200JSONResponse(safeCustomerResponse(row, includeIdentity, dec)), nil
	}

	if uuidPtrEqual(before, after) {
		summary, err := q.CustomerTimelineSummary(ctx, req.Id)
		if err != nil {
			return nil, fmt.Errorf("customers: timeline summary: %w", err)
		}
		return respond(fromCustomerRow(existing, summary))
	}

	// Every directory call this handler makes happens here, before the
	// transaction: the candidate's eligibility, and the previous owner's name
	// for the event's before snapshot.
	var afterSnapshot *ownerSnapshot
	if after != nil {
		user, err := s.deps.Users.User(ctx, *after)
		if err != nil {
			return nil, fmt.Errorf("customers: resolve owner candidate: %w", err)
		}
		switch {
		case user == nil:
			return gen.PutCustomersByIdOwner400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem(
				"Invalid owner", map[string][]string{"ownerUserId": {ownerNotFound(*after)}})), nil
		case !user.Active:
			return gen.PutCustomersByIdOwner400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem(
				"Invalid owner", map[string][]string{"ownerUserId": {ownerDisabled(*after)}})), nil
		}
		afterSnapshot = &ownerSnapshot{UserID: user.ID, DisplayName: user.DisplayName}
	}

	var beforeSnapshot *ownerSnapshot
	if before != nil {
		user, err := s.deps.Users.User(ctx, *before)
		if err != nil {
			return nil, fmt.Errorf("customers: resolve previous owner: %w", err)
		}
		// The name is snapshotted into the payload rather than resolved when
		// the timeline is read: an account renamed or deleted later must not
		// rewrite what the timeline says happened.
		beforeSnapshot = &ownerSnapshot{UserID: *before, DisplayName: unknownOwnerDisplay}
		if user != nil {
			beforeSnapshot.DisplayName = user.DisplayName
		}
	}

	now := s.deps.Clock()
	// Resolved before the transaction opens, and only here: by this point the
	// handler always records customer.owner_changed (the no-op returned above),
	// so the actor is always needed (customers foundation design D1).
	act, err := s.actorFor(ctx, generatedFallbackActor)
	if err != nil {
		return nil, fmt.Errorf("customers: resolve actor: %w", err)
	}

	var updated store.UpdateCustomerOwnerRow
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		var err error
		updated, err = txq.UpdateCustomerOwner(ctx, store.UpdateCustomerOwnerParams{
			ID: req.Id, OwnerUserID: after, UpdatedAt: now, ExpectedRevision: body.Revision,
		})
		if err != nil {
			return err
		}
		return recordCustomerOwnerChanged(ctx, txq, now, req.Id, beforeSnapshot, afterSnapshot, act.Kind, act.Display, act.UserID)
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// The guarded UPDATE matched no row: a concurrent writer moved the
		// revision between the read above and this write, answered by
		// re-reading and reporting the row's now-current revision — the same
		// race PutCustomersByIdContactInfo's own guarded write answers.
		fresh, ferr := q.GetCustomer(ctx, req.Id)
		if errors.Is(ferr, pgx.ErrNoRows) {
			return gen.PutCustomersByIdOwner404Response{}, nil
		}
		if ferr != nil {
			return nil, fmt.Errorf("customers: re-read customer after conflict: %w", ferr)
		}
		return gen.PutCustomersByIdOwner409ApplicationProblemPlusJSONResponse(customerRevisionConflict(existing.Revision, fresh.Revision)), nil
	case err != nil:
		return nil, fmt.Errorf("customers: update customer owner: %w", err)
	}

	summary, err := q.CustomerTimelineSummary(ctx, req.Id)
	if err != nil {
		return nil, fmt.Errorf("customers: timeline summary: %w", err)
	}
	return respond(fromUpdateCustomerOwnerRow(updated, summary))
}

// GetCustomersAssignableUsers Search users assignable as a customer's owner
// (GET /api/v1/customers/assignable-users)
//
// The reason contracts.UserDirectory exists (its own doc comment, and
// projects' GetProjectsByIdAssignableUsers before this): the only user listing
// identity offers needs the authorization-management policy, so without this a
// salesperson who is not an administrator could not pick a colleague. It sits
// behind customers:update rather than customers:view because it is the
// writer's search: who a customer COULD be given to is only useful to whoever
// may give it.
//
// Unlike projects' version there is nothing to exclude — a customer has one
// owner, not a team, so the current owner is a legitimate result and the
// picker wants it on the list — so this is SearchUsers and a cap, and nothing
// else. SearchUsers answers active users only, so a disabled account is never
// a candidate and nothing here has to filter for that.
func (s *server) GetCustomersAssignableUsers(ctx context.Context, req gen.GetCustomersAssignableUsersRequestObject) (gen.GetCustomersAssignableUsersResponseObject, error) {
	limit := int32(assignableOwnerLimit)
	if req.Params.Limit != nil {
		if *req.Params.Limit < 1 || *req.Params.Limit > assignableOwnerLimit {
			return gen.GetCustomersAssignableUsers400ApplicationProblemPlusJSONResponse(apicommon.Problem(
				"Invalid query parameters",
				fmt.Sprintf("'limit' must be between 1 and %d, but was %d.", assignableOwnerLimit, *req.Params.Limit))), nil
		}
		limit = *req.Params.Limit
	}

	query := ""
	if req.Params.Query != nil {
		query = strings.TrimSpace(*req.Params.Query)
	}

	found, err := s.deps.Users.SearchUsers(ctx, query, int(limit))
	if err != nil {
		return nil, fmt.Errorf("customers: search assignable users: %w", err)
	}
	data := make([]gen.CustomerAssignableUser, 0, len(found))
	for _, e := range found {
		data = append(data, gen.CustomerAssignableUser{UserId: e.ID, DisplayName: e.DisplayName})
		if len(data) == int(limit) {
			// The directory clamps its own limit, but this module's cap is its
			// own promise and is enforced here rather than assumed of a
			// collaborator.
			break
		}
	}
	return gen.GetCustomersAssignableUsers200JSONResponse(data), nil
}
```

- [ ] **Step 7: Write the timeline event**

Append to `apps/server/internal/customers/timeline_events.go`:

```go
// ownerSnapshot is customer.owner_changed's before/after shape (owner and tags
// design D1): who the owner was, by id AND by the name they had at the time.
// The name is stored rather than resolved when the timeline is read, for the
// same reason actorDisplay is stored on every entry: a renamed or deleted
// account must not rewrite what the timeline says happened.
type ownerSnapshot struct {
	UserID      uuid.UUID `json:"userId"`
	DisplayName string    `json:"displayName"`
}

// ownerSnapshotName is the summary's word for one side of the change.
// "nobody" rather than "—" or an empty string: the summary is a sentence a
// person reads in the timeline, and "Customer owner changed: Kari Nordmann →
// nobody" says what happened without needing the payload.
func ownerSnapshotName(s *ownerSnapshot) string {
	if s == nil {
		return "nobody"
	}
	return s.DisplayName
}

// recordCustomerOwnerChanged is PutCustomersByIdOwner's own generated event
// (owner and tags design D1). Only called once the handler has confirmed the
// owner actually changed (owner.go's no-op rule), so before and after are
// never equal here — and either of them may be nil, which is how "assigned"
// and "cleared" are told apart.
//
// Unlike recordCustomerUpdated there is no changes map: there is exactly one
// field, so before/after IS the change, and a one-entry map repeating them
// would be noise. The spec names this payload shape exactly.
func recordCustomerOwnerChanged(ctx context.Context, q *store.Queries, now time.Time, customerID int32, before, after *ownerSnapshot, actorKind, actorDisplay string, actorUserID *uuid.UUID) error {
	summary := truncateUTF16(fmt.Sprintf("Customer owner changed: %s → %s", ownerSnapshotName(before), ownerSnapshotName(after)), 500)
	payload := map[string]any{
		"customerId": customerID,
		"before":     before,
		"after":      after,
	}
	return recordGeneratedEvent(ctx, q, customerID, now, "customer.owner_changed", summary, payload, 1, actorKind, actorDisplay, actorUserID)
}
```

- [ ] **Step 8: Update the other `safeCustomerResponse` callers**

Seven call sites outside `customers.go`'s own list loop now need a decoration. Each of them already has a pool-backed `q` and is past its transaction, so each decorates the one row it is about:

- `customers.go`'s `GetCustomer` (`:562`), `PutCustomersById`'s no-op return (`:707`) and its success return (`:818`)
- `contact_info.go`'s no-op return (`:143`) and success return (`:192`)
- `customer_type.go`'s no-op return (`:68`) and success return (`:132`)

The shape at every one of them is the same three lines — build the row, decorate it, respond:

```go
	row := fromUpdateCustomerContactInfoRow(updated, summary)
	dec, err := s.decorate(ctx, q, row)
	if err != nil {
		return nil, err
	}
	return gen.PutCustomersByIdContactInfo200JSONResponse(safeCustomerResponse(row, includeIdentity, dec)), nil
```

(substituting each site's own adapter and response type). Do not introduce a shared helper for this: the response types differ per operation, so a helper would take a constructor function and read worse than the three lines it replaced.

- [ ] **Step 9: Run the tests to verify they pass**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server && mise exec -- go build ./... && mise exec -- go test -count=1 -run 'Owner|AssignableUsers' ./internal/customers/
```
Expected: PASS, and the whole repository builds again. This task put exactly two operations on the contract and implements both, so the interface is satisfied and the package compiles — nothing here waits on Task 4.

Then the module's full suite, which includes the operation-coverage gate: the two new operations must each be exercised by a successful exchange, and they are (`TestPutCustomersByIdOwner_SetsAndClears_ShowsInGetAndList`, `TestGetCustomersAssignableUsers`).

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server && mise exec -- go test -count=1 ./internal/customers/...
```

- [ ] **Step 10: Prove the new tests can fail**

Four guards, each removed, seen red, restored:

1. In `decorate`, drop the `if u, ok := d.owners[*id]; ok` branch of `owner` so every owner reads as `Unknown user`: `TestPutCustomersByIdOwner_SetsAndClears_ShowsInGetAndList` goes red.
2. Move the candidate validation above the no-op check: `TestPutCustomersByIdOwner_AnOwnerDisabledAfterwardsKeepsTheCustomer` stays green (it never re-sends) but add a temporary re-send of the disabled owner and see the 400 — then remove both and restore the order. (Simpler variant: delete the `!user.Active` case entirely; `TestPutCustomersByIdOwner_RefusesAUserWhoCannotOwn` goes red.)
3. Add `OwnerNone`/`OwnerID` to `ListCustomersParams` but not to `CountCustomersParams`: `TestGetCustomers_OwnerIdFilter`'s `totalCount` assertion goes red — the drift those two queries' comments warn about, caught.
4. Make `uuidPtrEqual(nil, nil)` return false: `TestPutCustomersByIdOwner_NoOpWritesNothing` goes red on the revision after clearing an unowned customer.

- [ ] **Step 11: Vet, lint, commit**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server && mise exec -- go vet ./... && mise exec -- golangci-lint run ./internal/customers/... && mise exec -- go test -count=1 ./internal/customers/... ./internal/openapi/... ./internal/db/...
cd /home/anders/projects/vantigo/vantigo
printf '%s\n\n%s\n' 'feat(customers): a customer has one owner, named from the user directory and filterable as mine' 'Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>' > /tmp/msg-owner-task3
git add openapi/customers.yaml openapi/COVERAGE.md apps/server/internal/openapi/openapi_test.go apps/server/internal/openapi/specs/customers.yaml apps/server/internal/customers/gen/api.gen.go apps/server/internal/customers/owner.go apps/server/internal/customers/owner_test.go apps/server/internal/customers/customers.go apps/server/internal/customers/customers_test.go apps/server/internal/customers/harness_test.go apps/server/internal/customers/timeline_events.go apps/server/internal/customers/contact_info.go apps/server/internal/customers/customer_type.go
git add $(git status --porcelain | awk '/api-schema.d.ts/ {print $2}')
git commit -F /tmp/msg-owner-task3 -- $(git diff --cached --name-only)
git show --stat HEAD && git status --short
```

---

### Task 4: Tags, end to end on the server (D2, D3, D4)

**Files:**
- Create: `apps/server/internal/customers/tags.go`, `apps/server/internal/customers/tags_test.go`
- Modify: `openapi/customers.yaml` (four operations), `apps/server/internal/openapi/openapi_test.go` (`KnownServeMuxConflicts`), `apps/server/internal/customers/values.go` + `values_test.go` (`validateTagName`, `validateTagColor`), `apps/server/internal/customers/timeline_events.go` (`recordCustomerTagsChanged`)
- Generated: `apps/server/internal/openapi/specs/customers.yaml`, `apps/server/internal/customers/gen/api.gen.go`, `openapi/COVERAGE.md`, `api-schema.d.ts` wherever it changes
- Read first (do not change): `apps/server/internal/communications/tags.go` (the whole file — list name-asc, the `tag_exists` 409, the link/unlink asymmetry this delivery replaces with a set replace), `apps/server/internal/customers/registry.go:533-555` (`registryIdentityChangedConflict`, the coded-409 shape), `apps/server/internal/customers/values.go:28-52` and `:497-546` (the module's value-rule voice and its UTF-16 length messages), `apps/server/internal/customers/owner.go` (Task 3 — `customerDecoration` already carries tags), `apps/server/internal/customers/store/tags.sql.go` (what Task 1 generated), `apps/server/internal/db/tx.go:98-112` (`IsUniqueViolation`)

**Interfaces:**
- Consumes: every query from `queries/tags.sql` and `CustomerExists` (Task 1); `gen.CustomerTag`, `gen.CustomerTagSummary`, `gen.CustomerTagRequest`, `gen.CustomerTagsResponse`, `gen.PutCustomerTagsRequest` (Task 2); `customerDecoration`, `safeCustomerResponse`, `uuidPtrEqual` (Task 3).
- Produces:
```go
var tagColors = []string{"gray", "red", "pink", "grape", "violet", "indigo", "blue", "cyan", "teal", "green", "lime", "yellow", "orange"}
func validateTagName(raw string) (string, string)   // values.go, the module's (normalized, error) shape
func validateTagColor(raw string) (string, string)  // values.go
func validateTagRequest(name string, color *string) (string, *string, map[string][]string)
func tagExistsConflict() gen.CustomerConflictProblem
func tagNotFound(id uuid.UUID) string               // "Tag %s does not exist"

func (s *server) GetCustomersTags(ctx context.Context, req gen.GetCustomersTagsRequestObject) (gen.GetCustomersTagsResponseObject, error)
func (s *server) PostCustomersTags(ctx context.Context, req gen.PostCustomersTagsRequestObject) (gen.PostCustomersTagsResponseObject, error)
func (s *server) PutCustomersTagsByTagId(ctx context.Context, req gen.PutCustomersTagsByTagIdRequestObject) (gen.PutCustomersTagsByTagIdResponseObject, error)
func (s *server) DeleteCustomersTagsByTagId(ctx context.Context, req gen.DeleteCustomersTagsByTagIdRequestObject) (gen.DeleteCustomersTagsByTagIdResponseObject, error)
func (s *server) PutCustomersByIdTags(ctx context.Context, req gen.PutCustomersByIdTagsRequestObject) (gen.PutCustomersByIdTagsResponseObject, error)

// timeline_events.go
type tagSnapshot struct { TagID uuid.UUID `json:"tagId"`; Name string `json:"name"` }
func recordCustomerTagsChanged(ctx context.Context, q *store.Queries, now time.Time, customerID int32, added, removed []tagSnapshot, actorKind, actorDisplay string, actorUserID *uuid.UUID) error
```

- [ ] **Step 1: Put the tags' four operations on the contract**

Add `/api/v1/customers/{id}/tags` to `openapi/customers.yaml` between `/api/v1/customers/{id}/registry-refresh` and `/api/v1/customers/{id}/type`:

```yaml
    /api/v1/customers/{id}/tags:
        put:
            operationId: putCustomersByIdTags
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
                            $ref: '#/components/schemas/PutCustomerTagsRequest'
                required: true
            responses:
                "200":
                    content:
                        application/json:
                            schema:
                                $ref: '#/components/schemas/CustomerTagsResponse'
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
            summary: Replace a customer's tags
            tags:
                - Customers
            x-vantigo-access: permission:customers:update+customers:view
```

and `/api/v1/customers/tags` plus `/api/v1/customers/tags/{tagId}` after `/api/v1/customers/stats/timeseries` (the file's last path):

```yaml
    /api/v1/customers/tags:
        get:
            operationId: getCustomersTags
            responses:
                "200":
                    content:
                        application/json:
                            schema:
                                items:
                                    $ref: '#/components/schemas/CustomerTagSummary'
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
            summary: List every tag
            tags:
                - Customers
            x-vantigo-access: permission:customers:view
        post:
            operationId: postCustomersTags
            requestBody:
                content:
                    application/json:
                        schema:
                            $ref: '#/components/schemas/CustomerTagRequest'
                required: true
            responses:
                "201":
                    content:
                        application/json:
                            schema:
                                $ref: '#/components/schemas/CustomerTagSummary'
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
                    description: Conflict — a tag with this name already exists (code tag_exists).
            summary: Create a tag
            tags:
                - Customers
            x-vantigo-access: permission:customers:update
    /api/v1/customers/tags/{tagId}:
        delete:
            operationId: deleteCustomersTagsByTagId
            parameters:
                - in: path
                  name: tagId
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
            summary: Delete a tag and remove it from every customer
            tags:
                - Customers
            x-vantigo-access: permission:customers:update
        put:
            operationId: putCustomersTagsByTagId
            parameters:
                - in: path
                  name: tagId
                  required: true
                  schema:
                    format: uuid
                    type: string
            requestBody:
                content:
                    application/json:
                        schema:
                            $ref: '#/components/schemas/CustomerTagRequest'
                required: true
            responses:
                "200":
                    content:
                        application/json:
                            schema:
                                $ref: '#/components/schemas/CustomerTagSummary'
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
                    description: Conflict — another tag already has this name (code tag_exists).
            summary: Rename or recolour a tag
            tags:
                - Customers
            x-vantigo-access: permission:customers:update
```

Then generate and pin the new conflicts:

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server && mise exec -- go generate ./... && mise exec -- go generate ./... && git status --short
cd /home/anders/projects/vantigo/vantigo/apps/server && mise exec -- go test -count=1 -run 'TestServeMuxConflictsArePinned' ./internal/openapi/
```

**Take the pairs from that output** and add the missing ones to `KnownServeMuxConflicts`, in the slice's alphabetical order. The eight this step is expected to add:

```go
	"DELETE /api/v1/customers/tags/{tagId} ⟷ DELETE /api/v1/customers/{id}/legal-identity",
	"PUT /api/v1/customers/contacts/{id} ⟷ PUT /api/v1/customers/{id}/tags",
	"PUT /api/v1/customers/tags/{tagId} ⟷ PUT /api/v1/customers/{id}/billing-profile",
	"PUT /api/v1/customers/tags/{tagId} ⟷ PUT /api/v1/customers/{id}/contact-info",
	"PUT /api/v1/customers/tags/{tagId} ⟷ PUT /api/v1/customers/{id}/legal-identity",
	"PUT /api/v1/customers/tags/{tagId} ⟷ PUT /api/v1/customers/{id}/owner",
	"PUT /api/v1/customers/tags/{tagId} ⟷ PUT /api/v1/customers/{id}/tags",
	"PUT /api/v1/customers/tags/{tagId} ⟷ PUT /api/v1/customers/{id}/type",
```

`/api/v1/customers/tags/{tagId}` conflicts with every three-segment `/api/v1/customers/{id}/<literal>` of the same method, which is why `PUT` brings six of them and `DELETE` one — the same fan-out `/api/v1/customers/contacts/{id}` already has. `GET /api/v1/customers/tags` and `POST /api/v1/customers/tags` bring none: a literal second segment beats `{id}` (see Task 3's Step 2).

Then:

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server && mise exec -- go test -count=1 ./internal/openapi/... ./internal/customers/gen/
cd /home/anders/projects/vantigo/vantigo/apps/server && mise exec -- go run ./internal/openapi/cmd/contract coverage -corpus ../../openapi/testdata/exchanges -out ../../openapi/COVERAGE.md
cd /home/anders/projects/vantigo/vantigo && mise exec -- bun run gen:client && git status --short
```

- [ ] **Step 2: Write the failing value tests**

Append to `apps/server/internal/customers/values_test.go`, in the style of its neighbours (table-driven, asserting the exact message):

```go
// TestValidateTagName is the tag name rule (owner and tags design D2): 1-100
// UTF-16 units, trimmed, case preserved — validateFriendlyName's own shape,
// with 100 instead of 255.
func TestValidateTagName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr string
	}{
		{name: "an ordinary name", in: "VIP", want: "VIP"},
		{name: "case is preserved", in: "vip", want: "vip"},
		{name: "surrounding space is trimmed", in: "  Prospect  ", want: "Prospect"},
		{name: "blank is refused", in: "   ", wantErr: "A tag name cannot be null or empty"},
		{name: "empty is refused", in: "", wantErr: "A tag name cannot be null or empty"},
		{
			name:    "past 100 UTF-16 units is refused, counted in UTF-16",
			in:      strings.Repeat("😀", 51), // 51 emoji = 102 UTF-16 units
			wantErr: "A tag name cannot be longer than 100 characters, the given value was 102 characters",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := customers.ValidateTagNameForTest(tc.in)
			if got != tc.want || err != tc.wantErr {
				t.Errorf("validateTagName(%q) = (%q, %q), want (%q, %q)", tc.in, got, err, tc.want, tc.wantErr)
			}
		})
	}
}

// TestValidateTagColor is the colour rule (owner and tags design D2): one of
// Mantine's named colours, so the UI never has to sanitise what the API
// stored. Every accepted value is listed, because the point of the rule is the
// exact set.
func TestValidateTagColor(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{"gray", "red", "pink", "grape", "violet", "indigo", "blue", "cyan", "teal", "green", "lime", "yellow", "orange"} {
		if got, err := customers.ValidateTagColorForTest(ok); got != ok || err != "" {
			t.Errorf("validateTagColor(%q) = (%q, %q), want it accepted", ok, got, err)
		}
	}
	want := "A tag colour must be one of 'gray', 'red', 'pink', 'grape', 'violet', 'indigo', 'blue', 'cyan', 'teal', 'green', 'lime', 'yellow' or 'orange', but was '#ff0000'"
	if _, err := customers.ValidateTagColorForTest("#ff0000"); err != want {
		t.Errorf("validateTagColor(#ff0000) error = %q, want %q", err, want)
	}
	// Case-sensitive, like every other value rule in this file that names a
	// closed set of lowercase tokens.
	if _, err := customers.ValidateTagColorForTest("Red"); err == "" {
		t.Error("validateTagColor(Red) was accepted, want it refused: the set is lowercase")
	}
}
```

Check first how `values_test.go` reaches the package-private validators — `grep -n 'package\|ForTest' apps/server/internal/customers/values_test.go`. If that file is `package customers` (white-box), call `validateTagName`/`validateTagColor` directly and drop the `customers.` prefix and the `…ForTest` seams; if it is `package customers_test`, add the two seams to `export_test.go`:

```go
// ValidateTagNameForTest and ValidateTagColorForTest are the two tag value
// rules reached from the external test package, as this file's other seams are.
func ValidateTagNameForTest(raw string) (string, string)  { return validateTagName(raw) }
func ValidateTagColorForTest(raw string) (string, string) { return validateTagColor(raw) }
```

- [ ] **Step 3: Write the failing tag tests**

Create `apps/server/internal/customers/tags_test.go`:

```go
package customers_test

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is the tags half of phase 4 delivery A (owner and tags design D2,
// D3, D4): the vocabulary's four operations, the customer's set replace, the
// tagId list filter, and the tags that ride on every customer response.
//
// The shape is communications' own tags (internal/communications/tags.go) with
// three deliberate differences, and each of them has a test here that would
// pass against communications' version and must not: the uniqueness is
// case-insensitive; a customer's tags are REPLACED as a set rather than linked
// and unlinked one at a time; and the list carries a customerCount, because
// design D3's delete confirmation has to say what it will affect.

// tagSummaryJSON decodes CustomerTagSummary.
type tagSummaryJSON struct {
	Id            string  `json:"id"`
	Name          string  `json:"name"`
	Color         *string `json:"color"`
	CustomerCount int32   `json:"customerCount"`
}

// listTags GETs /customers/tags and decodes a 200.
func listTags(t *testing.T, c *modtest.Client) []tagSummaryJSON {
	t.Helper()
	r := c.Do(http.MethodGet, "/api/v1/customers/tags", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("GET /api/v1/customers/tags: status %d body %s, want 200", r.Status, r.Body)
	}
	var tags []tagSummaryJSON
	r.JSON(&tags)
	return tags
}

// createTag POSTs a tag and fails the test on anything but 201.
func createTag(t *testing.T, c *modtest.Client, body map[string]any) tagSummaryJSON {
	t.Helper()
	r := c.Do(http.MethodPost, "/api/v1/customers/tags", body)
	if r.Status != http.StatusCreated {
		t.Fatalf("POST /api/v1/customers/tags %v: status %d body %s, want 201", body, r.Status, r.Body)
	}
	var tag tagSummaryJSON
	r.JSON(&tag)
	return tag
}

// putCustomerTags PUTs /customers/{id}/tags with the given ids.
func putCustomerTags(t *testing.T, c *modtest.Client, id int32, tagIDs []string) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/tags", id), map[string]any{"tagIds": tagIDs})
}

// TestCustomerTags_CreateListRenameDelete walks the vocabulary's whole life in
// one test, because each step's assertion is about the state the previous one
// left: the list is name-ascending, the count is the customers carrying the
// tag, a rename keeps both the id and the count, and a delete takes the links
// with it (the table's own cascade) without touching the customers.
func TestCustomerTags_CreateListRenameDelete(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	vip := createTag(t, c, map[string]any{"name": "VIP", "color": "grape"})
	prospect := createTag(t, c, map[string]any{"name": "Prospect"})
	if vip.CustomerCount != 0 {
		t.Errorf("a fresh tag's customerCount = %d, want 0", vip.CustomerCount)
	}
	if prospect.Color != nil {
		t.Errorf("a tag created without a colour has color = %v, want null", prospect.Color)
	}

	customer := createCustomer(t, c, "Tagged Co")
	if r := putCustomerTags(t, c, customer.Id, []string{vip.Id}); r.Status != http.StatusOK {
		t.Fatalf("tag the customer: status %d body %s, want 200", r.Status, r.Body)
	}

	tags := listTags(t, c)
	if len(tags) != 2 || tags[0].Name != "Prospect" || tags[1].Name != "VIP" {
		t.Fatalf("tags = %+v, want Prospect then VIP (name-ascending)", tags)
	}
	if tags[1].CustomerCount != 1 || tags[0].CustomerCount != 0 {
		t.Errorf("customerCounts = %d/%d, want 0 for Prospect and 1 for VIP", tags[0].CustomerCount, tags[1].CustomerCount)
	}

	r := c.Do(http.MethodPut, "/api/v1/customers/tags/"+vip.Id, map[string]any{"name": "Key account", "color": "teal"})
	if r.Status != http.StatusOK {
		t.Fatalf("rename: status %d body %s, want 200", r.Status, r.Body)
	}
	var renamed tagSummaryJSON
	r.JSON(&renamed)
	if renamed.Id != vip.Id || renamed.Name != "Key account" || renamed.Color == nil || *renamed.Color != "teal" || renamed.CustomerCount != 1 {
		t.Errorf("renamed = %+v, want the same id, the new name and colour, and the count kept", renamed)
	}
	// A rename records nothing on the customers carrying the tag (design D2:
	// the tag is the vocabulary, not the customer).
	if n := countTimelineEvents(t, h, customer.Id, "customer.tags_changed"); n != 1 {
		t.Errorf("customer.tags_changed events = %d after a rename, want 1 (only the set replace)", n)
	}

	if r := c.Do(http.MethodDelete, "/api/v1/customers/tags/"+vip.Id, nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete: status %d body %s, want 204", r.Status, r.Body)
	}
	if got := fetchCustomerJSON(t, c, customer.Id); len(got.Tags) != 0 {
		t.Errorf("customer tags = %+v after deleting the tag, want none — the cascade", got.Tags)
	}
	if r := c.Do(http.MethodDelete, "/api/v1/customers/tags/"+vip.Id, nil); r.Status != http.StatusNotFound {
		t.Errorf("deleting it again: status %d body %s, want 404", r.Status, r.Body)
	}
	if r := c.Do(http.MethodPut, "/api/v1/customers/tags/"+uuid.New().String(), map[string]any{"name": "Ghost"}); r.Status != http.StatusNotFound {
		t.Errorf("renaming a tag that does not exist: status %d body %s, want 404", r.Status, r.Body)
	}
}

// TestCustomerTags_DuplicateNameIsAConflictIgnoringCase is the one difference
// from communications worth its own test: a tag is a vocabulary word, so 'VIP'
// and 'vip' are the same word. Both the create and the rename must say so, and
// both with code tag_exists, so a UI can offer "you already have that tag"
// rather than a bare 409.
func TestCustomerTags_DuplicateNameIsAConflictIgnoringCase(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	createTag(t, c, map[string]any{"name": "VIP"})
	other := createTag(t, c, map[string]any{"name": "Prospect"})

	for _, name := range []string{"VIP", "vip", "  ViP  "} {
		r := c.Do(http.MethodPost, "/api/v1/customers/tags", map[string]any{"name": name})
		if r.Status != http.StatusConflict {
			t.Fatalf("create %q: status %d body %s, want 409", name, r.Status, r.Body)
		}
		var conflict conflictProblemJSON
		r.JSON(&conflict)
		if conflict.Code == nil || *conflict.Code != "tag_exists" {
			t.Errorf("create %q conflict code = %v, want tag_exists", name, conflict.Code)
		}
	}

	r := c.Do(http.MethodPut, "/api/v1/customers/tags/"+other.Id, map[string]any{"name": "vip"})
	if r.Status != http.StatusConflict {
		t.Fatalf("rename onto an existing name: status %d body %s, want 409", r.Status, r.Body)
	}
	// Renaming a tag to the name it already has is not a conflict with itself.
	if r := c.Do(http.MethodPut, "/api/v1/customers/tags/"+other.Id, map[string]any{"name": "Prospect"}); r.Status != http.StatusOK {
		t.Errorf("renaming a tag to its own name: status %d body %s, want 200", r.Status, r.Body)
	}
	if got := listTags(t, c); len(got) != 2 {
		t.Errorf("tags = %+v, want the original two", got)
	}
}

// TestCustomerTags_RefusesABadNameOrColour pins the two validation rules over
// HTTP, keyed by the request's own field names (values_test.go carries the
// table-driven coverage of the rules themselves).
func TestCustomerTags_RefusesABadNameOrColour(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	r := c.Do(http.MethodPost, "/api/v1/customers/tags", map[string]any{"name": "  ", "color": "#ff0000"})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if got := problem.Errors["name"]; len(got) != 1 || got[0] != "A tag name cannot be null or empty" {
		t.Errorf("errors[name] = %v, want the blank-name message", got)
	}
	if got := problem.Errors["color"]; len(got) != 1 {
		t.Fatalf("errors[color] = %v, want one message", got)
	}
	// Both fields are reported together, never short-circuited on the first
	// failure — the module's all-errors-at-once convention.
	if len(problem.Errors) != 2 {
		t.Errorf("errors = %v, want exactly name and color", problem.Errors)
	}
	if len(listTags(t, c)) != 0 {
		t.Error("a refused create left a tag behind")
	}
}

// TestPutCustomersByIdTags_ReplacesTheSet pins what a set replace means: the
// answer is the customer's tags name-ascending, a second call with a different
// set replaces rather than adds, an empty array clears, and the customer row is
// untouched throughout — no revision bump, no updatedAt move (design D2: tags
// are off the row).
func TestPutCustomersByIdTags_ReplacesTheSet(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	vip := createTag(t, c, map[string]any{"name": "VIP", "color": "grape"})
	prospect := createTag(t, c, map[string]any{"name": "Prospect"})
	churned := createTag(t, c, map[string]any{"name": "Churned"})
	customer := createCustomer(t, c, "Replace Co")
	before := fetchCustomerJSON(t, c, customer.Id)

	r := putCustomerTags(t, c, customer.Id, []string{vip.Id, prospect.Id})
	if r.Status != http.StatusOK {
		t.Fatalf("first replace: status %d body %s, want 200", r.Status, r.Body)
	}
	var answered struct {
		Tags []tagJSON `json:"tags"`
	}
	r.JSON(&answered)
	if len(answered.Tags) != 2 || answered.Tags[0].Name != "Prospect" || answered.Tags[1].Name != "VIP" {
		t.Fatalf("tags = %+v, want Prospect then VIP", answered.Tags)
	}
	if answered.Tags[1].Color == nil || *answered.Tags[1].Color != "grape" {
		t.Errorf("VIP color = %v, want grape", answered.Tags[1].Color)
	}

	if r := putCustomerTags(t, c, customer.Id, []string{churned.Id}); r.Status != http.StatusOK {
		t.Fatalf("second replace: status %d body %s, want 200", r.Status, r.Body)
	}
	got := fetchCustomerJSON(t, c, customer.Id)
	if len(got.Tags) != 1 || got.Tags[0].Name != "Churned" {
		t.Errorf("tags = %+v after replacing, want only Churned — a replace is not an add", got.Tags)
	}

	if r := putCustomerTags(t, c, customer.Id, []string{}); r.Status != http.StatusOK {
		t.Fatalf("clear: status %d body %s, want 200", r.Status, r.Body)
	}
	cleared := fetchCustomerJSON(t, c, customer.Id)
	if cleared.Tags == nil || len(cleared.Tags) != 0 {
		t.Errorf("tags = %+v after clearing, want an empty array (never null)", cleared.Tags)
	}
	if cleared.Revision != before.Revision || !cleared.UpdatedAt.Equal(before.UpdatedAt) {
		t.Errorf("three set replaces moved revision %d→%d / updatedAt %v→%v, want neither: tags are off the row",
			before.Revision, cleared.Revision, before.UpdatedAt, cleared.UpdatedAt)
	}
}

// TestPutCustomersByIdTags_UnknownIdIsAFieldError pins that an id no tag holds
// is a 400 on tagIds — not a 500 from a foreign-key violation, and not a
// silently shorter set. Duplicates in the request are tolerated, because a
// set is a set.
func TestPutCustomersByIdTags_UnknownIdIsAFieldError(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	vip := createTag(t, c, map[string]any{"name": "VIP"})
	customer := createCustomer(t, c, "Unknown Tag Co")

	missing := uuid.New()
	r := putCustomerTags(t, c, customer.Id, []string{vip.Id, missing.String()})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	want := fmt.Sprintf("Tag %s does not exist", missing)
	if got := problem.Errors["tagIds"]; len(got) != 1 || got[0] != want {
		t.Errorf("errors[tagIds] = %v, want [%s]", got, want)
	}
	if got := fetchCustomerJSON(t, c, customer.Id); len(got.Tags) != 0 {
		t.Errorf("tags = %+v after the refusal, want none written", got.Tags)
	}

	if r := putCustomerTags(t, c, customer.Id, []string{vip.Id, vip.Id}); r.Status != http.StatusOK {
		t.Fatalf("duplicate ids: status %d body %s, want 200", r.Status, r.Body)
	}
	if got := fetchCustomerJSON(t, c, customer.Id); len(got.Tags) != 1 {
		t.Errorf("tags = %+v for a request naming one tag twice, want one", got.Tags)
	}

	if r := putCustomerTags(t, c, 999999, []string{vip.Id}); r.Status != http.StatusNotFound {
		t.Errorf("a customer that does not exist: status %d body %s, want 404", r.Status, r.Body)
	}
}

// TestPutCustomersByIdTags_RecordsTheEventOnlyWhenTheSetChanged pins design
// D2's event: added and removed, by id and by the name at the time, and
// nothing at all when the request names the set the customer already has —
// including the empty-to-empty case, which an implementation comparing only
// non-empty sets gets wrong.
func TestPutCustomersByIdTags_RecordsTheEventOnlyWhenTheSetChanged(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, callerID := authenticatedClientWithID(t, h)
	vip := createTag(t, c, map[string]any{"name": "VIP"})
	prospect := createTag(t, c, map[string]any{"name": "Prospect"})
	customer := createCustomer(t, c, "Event Tags Co")

	if r := putCustomerTags(t, c, customer.Id, []string{}); r.Status != http.StatusOK {
		t.Fatalf("clear an untagged customer: status %d body %s, want 200", r.Status, r.Body)
	}
	if n := countTimelineEvents(t, h, customer.Id, "customer.tags_changed"); n != 0 {
		t.Fatalf("events = %d after replacing an empty set with an empty set, want 0", n)
	}

	if r := putCustomerTags(t, c, customer.Id, []string{prospect.Id}); r.Status != http.StatusOK {
		t.Fatalf("tag: status %d body %s, want 200", r.Status, r.Body)
	}
	if r := putCustomerTags(t, c, customer.Id, []string{prospect.Id}); r.Status != http.StatusOK {
		t.Fatalf("re-send the same set: status %d body %s, want 200", r.Status, r.Body)
	}
	if n := countTimelineEvents(t, h, customer.Id, "customer.tags_changed"); n != 1 {
		t.Fatalf("events = %d, want 1 (the unchanged re-send records nothing)", n)
	}

	if r := putCustomerTags(t, c, customer.Id, []string{vip.Id}); r.Status != http.StatusOK {
		t.Fatalf("swap: status %d body %s, want 200", r.Status, r.Body)
	}
	event := fetchTimelineEvent(t, h, customer.Id, "customer.tags_changed")
	added, _ := event.Payload["added"].([]any)
	removed, _ := event.Payload["removed"].([]any)
	if len(added) != 1 || len(removed) != 1 {
		t.Fatalf("added/removed = %v/%v, want one each", added, removed)
	}
	if first, _ := added[0].(map[string]any); first["name"] != "VIP" || first["tagId"] != vip.Id {
		t.Errorf("added[0] = %v, want VIP / %s", added[0], vip.Id)
	}
	if first, _ := removed[0].(map[string]any); first["name"] != "Prospect" || first["tagId"] != prospect.Id {
		t.Errorf("removed[0] = %v, want Prospect / %s", removed[0], prospect.Id)
	}
	if event.Summary != "Customer tags changed: added VIP; removed Prospect" {
		t.Errorf("summary = %q, want both halves named", event.Summary)
	}

	gotActor := modtest.One[string](t, h, `
		SELECT actor_user_id::text FROM customers.customers_timeline_entries
		WHERE customer_id = $1 AND event_type = 'customer.tags_changed' ORDER BY id DESC LIMIT 1`, customer.Id)
	if gotActor != callerID.String() {
		t.Errorf("actor_user_id = %s, want %s (the signed-in caller)", gotActor, callerID)
	}
}

// TestGetCustomers_TagIdFilter pins design D2's single-tag filter on the list
// and the count together, plus the refusal of a value that is not a tag id.
func TestGetCustomers_TagIdFilter(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	vip := createTag(t, c, map[string]any{"name": "VIP"})
	prospect := createTag(t, c, map[string]any{"name": "Prospect"})

	tagged := createCustomer(t, c, "Tagfilter Yes Co")
	both := createCustomer(t, c, "Tagfilter Both Co")
	createCustomer(t, c, "Tagfilter No Co")
	if r := putCustomerTags(t, c, tagged.Id, []string{vip.Id}); r.Status != http.StatusOK {
		t.Fatalf("tag one: status %d body %s", r.Status, r.Body)
	}
	if r := putCustomerTags(t, c, both.Id, []string{vip.Id, prospect.Id}); r.Status != http.StatusOK {
		t.Fatalf("tag both: status %d body %s", r.Status, r.Body)
	}

	list := getList(t, c, "tagId="+vip.Id+"&search="+url.QueryEscape("Tagfilter "))
	if len(list.Data) != 2 || list.Pagination.TotalCount != 2 {
		t.Errorf("tagId=VIP = %d rows / totalCount %d, want 2 and 2", len(list.Data), list.Pagination.TotalCount)
	}
	if list = getList(t, c, "tagId="+prospect.Id+"&search="+url.QueryEscape("Tagfilter ")); len(list.Data) != 1 || list.Data[0].Name != "Tagfilter Both Co" {
		t.Errorf("tagId=Prospect = %+v, want only Tagfilter Both Co", list.Data)
	}
	// The tags ride on the list row itself, so one request answers both "which
	// customers" and "what else are they tagged with".
	if len(list.Data) == 1 && len(list.Data[0].Tags) != 2 {
		t.Errorf("list row tags = %+v, want both tags on the row", list.Data[0].Tags)
	}

	r := c.Do(http.MethodGet, "/api/v1/customers?tagId=notauuid", nil)
	if r.Status != http.StatusBadRequest {
		t.Fatalf("tagId=notauuid: status %d body %s, want 400", r.Status, r.Body)
	}
	if want := "'tagId' must be a tag id, but was 'notauuid'."; !strings.Contains(r.Body, want) {
		t.Errorf("body = %s, want it to contain %q", r.Body, want)
	}
}

// TestTagPermissions pins design D4: reading the vocabulary needs
// customers:view, everything that writes needs customers:update, and there is
// no new permission key for either.
func TestTagPermissions(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	writer := authenticatedClient(t, h)
	tag := createTag(t, writer, map[string]any{"name": "VIP"})
	customer := createCustomer(t, writer, "Permission Tags Co")

	viewer := h.SignIn(t, "customers:view")
	if got := listTags(t, viewer); len(got) != 1 {
		t.Errorf("a view-only caller listed %d tags, want 1", len(got))
	}
	for _, call := range []struct {
		name   string
		method string
		path   string
		body   map[string]any
	}{
		{name: "create", method: http.MethodPost, path: "/api/v1/customers/tags", body: map[string]any{"name": "Nope"}},
		{name: "rename", method: http.MethodPut, path: "/api/v1/customers/tags/" + tag.Id, body: map[string]any{"name": "Nope"}},
		{name: "delete", method: http.MethodDelete, path: "/api/v1/customers/tags/" + tag.Id},
		{name: "set replace", method: http.MethodPut, path: fmt.Sprintf("/api/v1/customers/%d/tags", customer.Id), body: map[string]any{"tagIds": []string{tag.Id}}},
	} {
		if r := viewer.Do(call.method, call.path, call.body); r.Status != http.StatusForbidden {
			t.Errorf("%s as a view-only caller: status %d body %s, want 403", call.name, r.Status, r.Body)
		}
	}
	// And the customer's own tags reach a view-only caller, ungated.
	if r := putCustomerTags(t, writer, customer.Id, []string{tag.Id}); r.Status != http.StatusOK {
		t.Fatalf("tag: status %d body %s", r.Status, r.Body)
	}
	if got := fetchCustomerJSON(t, viewer, customer.Id); len(got.Tags) != 1 || got.Tags[0].Name != "VIP" {
		t.Errorf("a view-only caller saw tags = %+v, want [VIP]", got.Tags)
	}
}
```

`strings` goes in the import list (the two `strings.Contains` assertions).

- [ ] **Step 4: Run both test files to verify they fail**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server && mise exec -- go test -count=1 -run 'Tag' ./internal/customers/
```
Expected: a build failure naming the missing methods and `undefined: validateTagName`. That failure resolves into real assertion failures once Steps 5–7 land.

- [ ] **Step 5: Write the two value rules**

Append to `apps/server/internal/customers/values.go`:

```go
// tagColors is the colour vocabulary a tag may use (owner and tags design
// D2): Mantine's own named colours, which is what the UI paints a chip with.
// Validating against the UI's palette in the API is deliberate and is the
// whole point of the rule — the alternative is every consumer sanitising
// whatever arrived, and a chip painted with a value a stylesheet does not know
// is an invisible chip. black and white are deliberately absent: a chip needs
// contrast against both themes.
var tagColors = []string{"gray", "red", "pink", "grape", "violet", "indigo", "blue", "cyan", "teal", "green", "lime", "yellow", "orange"}

// validateTagName is the tag name rule: non-blank, at most 100 UTF-16 code
// units, trimmed but case-preserved — validateFriendlyName's own shape with
// 100 instead of 255. Case is preserved even though uniqueness ignores it
// (migration 00024's lower(name) index): 'VIP' is how someone wrote it and is
// how it should read back, while 'vip' is not a second tag.
func validateTagName(raw string) (string, string) {
	if strings.TrimSpace(raw) == "" {
		return "", "A tag name cannot be null or empty"
	}
	if n := utf16Length(raw); n > 100 {
		return "", fmt.Sprintf("A tag name cannot be longer than 100 characters, the given value was %d characters", n)
	}
	return strings.TrimSpace(raw), ""
}

// validateTagColor is the colour rule. Case-sensitive, like every other value
// rule here that names a closed set of lowercase tokens: a query parameter or
// an enum token is never normalized in this module, only rejected.
func validateTagColor(raw string) (string, string) {
	if slices.Contains(tagColors, raw) {
		return raw, ""
	}
	quoted := make([]string, 0, len(tagColors))
	for _, c := range tagColors {
		quoted = append(quoted, "'"+c+"'")
	}
	return "", fmt.Sprintf("A tag colour must be one of %s or %s, but was '%s'",
		strings.Join(quoted[:len(quoted)-1], ", "), quoted[len(quoted)-1], raw)
}
```

`values.go` then imports `slices`. Check whether the module already builds an "one of 'a', 'b' or 'c'" list somewhere (`grep -n "or '%s'" apps/server/internal/customers/*.go`) and reuse that helper if one exists rather than adding a second way to do it.

- [ ] **Step 6: Write `tags.go`**

```go
package customers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
	"github.com/vantigo-io/vantigo/server/internal/customers/store"
	"github.com/vantigo-io/vantigo/server/internal/db"
)

// This file is the tags half of phase 4 delivery A (owner and tags design D2,
// D3): the vocabulary — GET/POST /customers/tags, PUT/DELETE
// /customers/tags/{tagId} — and PUT /customers/{id}/tags, which replaces one
// customer's set.
//
// It is communications' Tags area (internal/communications/tags.go) with three
// deliberate differences, and each one is a decision rather than a variation:
//
//  1. **Uniqueness ignores case** (migration 00024's index on lower(name)). A
//     tag is a vocabulary word; an installation holding both 'VIP' and 'vip'
//     has a filter that silently splits its customers in two.
//  2. **A customer's tags are REPLACED as a set**, not linked and unlinked one
//     at a time. The UI for tags is a multi-select, and a multi-select's write
//     is "here is the set now" — which also means there is no revision here
//     and none is accepted: tags are off the customer row (design D2), so
//     nothing bumps and two concurrent replaces are last-wins, which is what
//     replacing a set means.
//  3. **The list carries a customerCount**, because design D3's delete
//     confirmation has to say what it will affect and is already showing the
//     list.
//
// The vocabulary's own writes record no timeline event at all — renaming a tag
// says nothing about the customers carrying it (design D2: the tag is the
// vocabulary, not the customer) — while the set replace records
// customer.tags_changed on the customer whose set moved.

// tagSummaryOf is CustomerTagSummary's projection. customer_count arrives as a
// bigint from a count(*) sub-select and the contract's field is int32: a
// vocabulary with two billion uses of one tag is not a case worth a wider
// type, and the narrowing is here, once, rather than at each call site.
func tagSummaryOf(id uuid.UUID, name string, color *string, customerCount int64) gen.CustomerTagSummary {
	return gen.CustomerTagSummary{Id: id, Name: name, Color: color, CustomerCount: int32(customerCount)}
}

// tagExistsConflict is the 409 both the create and the rename answer for a
// name another tag already holds, ignoring case. It carries code tag_exists —
// communications' own code for the same refusal, and the second coded conflict
// in this module after the registry's two (registry.go) — so a UI can say "you
// already have that tag" instead of showing a bare 409.
func tagExistsConflict() gen.CustomerConflictProblem {
	title := "Tag already exists"
	detail := "A tag with this name already exists. Tag names are compared without regard to case."
	code := "tag_exists"
	status := int32(http.StatusConflict)
	return gen.CustomerConflictProblem{Title: &title, Detail: &detail, Code: &code, Status: &status}
}

// tagNotFound is the field error for an id in tagIds that no tag holds, worded
// as ownerNotFound (owner.go) words its own. A field error and not a 404: the
// customer exists and the caller may edit it, so what is wrong is the body.
func tagNotFound(id uuid.UUID) string {
	return fmt.Sprintf("Tag %s does not exist", id)
}

// validateTagRequest is POST/PUT /customers/tags' shared validator: both
// fields checked independently and both errors reported together, keyed by the
// request's own field names — the module's all-errors-at-once shape
// (validateContactInfo, validateLegalIdentity). An absent or blank colour is
// null, never an error; only a colour outside the palette is.
func validateTagRequest(name string, color *string) (string, *string, map[string][]string) {
	errs := map[string][]string{}
	normalizedName, nameErr := validateTagName(name)
	if nameErr != "" {
		errs["name"] = []string{nameErr}
	}
	var normalizedColor *string
	if color != nil && strings.TrimSpace(*color) != "" {
		c, colorErr := validateTagColor(*color)
		if colorErr != "" {
			errs["color"] = []string{colorErr}
		} else {
			normalizedColor = &c
		}
	}
	if len(errs) > 0 {
		return "", nil, errs
	}
	return normalizedName, normalizedColor, nil
}

// GetCustomersTags List every tag
// (GET /api/v1/customers/tags)
func (s *server) GetCustomersTags(ctx context.Context, _ gen.GetCustomersTagsRequestObject) (gen.GetCustomersTagsResponseObject, error) {
	q := store.New(s.deps.Pool)
	rows, err := q.ListCustomerTags(ctx)
	if err != nil {
		return nil, fmt.Errorf("customers: list tags: %w", err)
	}
	data := make([]gen.CustomerTagSummary, 0, len(rows))
	for _, row := range rows {
		data = append(data, tagSummaryOf(row.ID, row.Name, row.Color, row.CustomerCount))
	}
	return gen.GetCustomersTags200JSONResponse(data), nil
}

// PostCustomersTags Create a tag
// (POST /api/v1/customers/tags)
//
// The duplicate check is the INSERT's own unique violation rather than a
// SELECT first: two callers creating 'VIP' at the same moment is exactly the
// race a check-then-insert loses, and the index is the only thing that can
// decide it. db.IsUniqueViolation names the constraint, so a uuid collision on
// the primary key stays a 500 the caller must hear about rather than being
// reported as a duplicate name.
func (s *server) PostCustomersTags(ctx context.Context, req gen.PostCustomersTagsRequestObject) (gen.PostCustomersTagsResponseObject, error) {
	body := gen.CustomerTagRequest{}
	if req.Body != nil {
		body = *req.Body
	}
	name, color, errs := validateTagRequest(body.Name, body.Color)
	if errs != nil {
		return gen.PostCustomersTags400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem("Invalid tag", errs)), nil
	}

	q := store.New(s.deps.Pool)
	tag, err := q.InsertCustomerTag(ctx, store.InsertCustomerTagParams{ID: uuid.New(), Name: name, Color: color})
	if err != nil {
		if db.IsUniqueViolation(err, "ux_customers_tags_name_lower") {
			return gen.PostCustomersTags409ApplicationProblemPlusJSONResponse(tagExistsConflict()), nil
		}
		return nil, fmt.Errorf("customers: create tag: %w", err)
	}
	// A tag nobody carries yet: the count is 0 by construction, so this needs
	// no second read.
	return gen.PostCustomersTags201JSONResponse(tagSummaryOf(tag.ID, tag.Name, tag.Color, 0)), nil
}

// PutCustomersTagsByTagId Rename or recolour a tag
// (PUT /api/v1/customers/tags/{tagId})
//
// No timeline event anywhere: renaming a tag records nothing on the customers
// that carry it (design D2). Renaming a tag to the name it already has is a
// plain 200, not a conflict with itself — the unique index compares
// lower(name) and the row being updated is the row being compared against, so
// the database says so too.
func (s *server) PutCustomersTagsByTagId(ctx context.Context, req gen.PutCustomersTagsByTagIdRequestObject) (gen.PutCustomersTagsByTagIdResponseObject, error) {
	body := gen.CustomerTagRequest{}
	if req.Body != nil {
		body = *req.Body
	}
	name, color, errs := validateTagRequest(body.Name, body.Color)
	if errs != nil {
		return gen.PutCustomersTagsByTagId400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem("Invalid tag", errs)), nil
	}

	q := store.New(s.deps.Pool)
	updated, err := q.UpdateCustomerTagRow(ctx, store.UpdateCustomerTagRowParams{ID: req.TagId, Name: name, Color: color})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return gen.PutCustomersTagsByTagId404Response{}, nil
	case db.IsUniqueViolation(err, "ux_customers_tags_name_lower"):
		return gen.PutCustomersTagsByTagId409ApplicationProblemPlusJSONResponse(tagExistsConflict()), nil
	case err != nil:
		return nil, fmt.Errorf("customers: update tag: %w", err)
	}

	// The count is re-read rather than assumed: a rename must answer the same
	// customerCount the list would, and this endpoint's own body is what the
	// Manage tags modal keeps on screen afterwards.
	row, err := q.GetCustomerTag(ctx, updated.ID)
	if err != nil {
		return nil, fmt.Errorf("customers: read tag after update: %w", err)
	}
	return gen.PutCustomersTagsByTagId200JSONResponse(tagSummaryOf(row.ID, row.Name, row.Color, row.CustomerCount)), nil
}

// DeleteCustomersTagsByTagId Delete a tag and remove it from every customer
// (DELETE /api/v1/customers/tags/{tagId})
//
// One statement: customer_tags goes with it through the table's own ON DELETE
// CASCADE (migration 00024), which is why there is no loop here and no
// transaction. Not idempotent — deleting an already-absent tag is a 404, the
// same asymmetry communications' own tag removal has — because "it is gone"
// and "it was never there" are different answers to someone who just clicked
// Delete twice.
//
// No timeline event on the customers that lose the tag: the customers did not
// change their minds, the vocabulary did (design D2).
func (s *server) DeleteCustomersTagsByTagId(ctx context.Context, req gen.DeleteCustomersTagsByTagIdRequestObject) (gen.DeleteCustomersTagsByTagIdResponseObject, error) {
	q := store.New(s.deps.Pool)
	rows, err := q.DeleteCustomerTag(ctx, req.TagId)
	if err != nil {
		return nil, fmt.Errorf("customers: delete tag: %w", err)
	}
	if rows == 0 {
		return gen.DeleteCustomersTagsByTagId404Response{}, nil
	}
	return gen.DeleteCustomersTagsByTagId204Response{}, nil
}

// PutCustomersByIdTags Replace a customer's tags
// (PUT /api/v1/customers/{id}/tags)
//
// Ordering: (1) the customer's existence, 404 — CustomerExists, deliberately
// not LockCustomer, because this write takes no lock on the customer row
// (design D2); (2) the ids resolved in one query, 400 keyed tagIds for any the
// vocabulary does not hold; (3) the actor, resolved before the transaction and
// only when something will actually be written; (4) the replace and its event
// in one transaction.
//
// The set is compared before it is written, inside the transaction, so the
// event is recorded only when the set actually moved (design D2) — including
// the empty-to-empty case, which is a real request from a multi-select whose
// caller changed nothing.
func (s *server) PutCustomersByIdTags(ctx context.Context, req gen.PutCustomersByIdTagsRequestObject) (gen.PutCustomersByIdTagsResponseObject, error) {
	body := gen.PutCustomerTagsRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	q := store.New(s.deps.Pool)
	if _, err := q.CustomerExists(ctx, req.Id); errors.Is(err, pgx.ErrNoRows) {
		return gen.PutCustomersByIdTags404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("customers: check customer exists: %w", err)
	}

	// Distinct ids, order preserved for a deterministic field error: a request
	// naming one tag twice asks for the same set as one naming it once.
	wanted := make([]uuid.UUID, 0, len(body.TagIds))
	seen := make(map[uuid.UUID]bool, len(body.TagIds))
	for _, id := range body.TagIds {
		if seen[id] {
			continue
		}
		seen[id] = true
		wanted = append(wanted, id)
	}

	resolved, err := q.CustomerTagsByIDs(ctx, wanted)
	if err != nil {
		return nil, fmt.Errorf("customers: resolve tags: %w", err)
	}
	if len(resolved) != len(wanted) {
		known := make(map[uuid.UUID]bool, len(resolved))
		for _, t := range resolved {
			known[t.ID] = true
		}
		var messages []string
		for _, id := range wanted {
			if !known[id] {
				messages = append(messages, tagNotFound(id))
			}
		}
		return gen.PutCustomersByIdTags400ApplicationProblemPlusJSONResponse(apicommon.ValidationProblem(
			"Invalid tags", map[string][]string{"tagIds": messages})), nil
	}

	after := make([]gen.CustomerTag, 0, len(resolved))
	afterSnapshots := make([]tagSnapshot, 0, len(resolved))
	for _, t := range resolved {
		after = append(after, gen.CustomerTag{Id: t.ID, Name: t.Name, Color: t.Color})
		afterSnapshots = append(afterSnapshots, tagSnapshot{TagID: t.ID, Name: t.Name})
	}

	now := s.deps.Clock()
	// Resolved before the transaction opens (customers foundation design D1),
	// and unconditionally: whether the set changed is only known under the
	// transaction, and resolving an actor there would be a directory call
	// inside it. An actor resolved and then not used costs one cached lookup.
	act, err := s.actorFor(ctx, generatedFallbackActor)
	if err != nil {
		return nil, fmt.Errorf("customers: resolve actor: %w", err)
	}

	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		current, err := txq.CustomerTagsForCustomers(ctx, []int32{req.Id})
		if err != nil {
			return err
		}
		before := make([]tagSnapshot, 0, len(current))
		for _, l := range current {
			before = append(before, tagSnapshot{TagID: l.ID, Name: l.Name})
		}
		added, removed := tagSetDiff(before, afterSnapshots)
		if len(added) == 0 && len(removed) == 0 {
			// Nothing moved: the same no-op rule every write in this module
			// follows (customers foundation design D5), applied to a set. The
			// two statements below would be a delete and a re-insert of
			// identical rows, and the event would claim a change that did not
			// happen.
			return nil
		}
		if err := txq.DeleteCustomerTagLinks(ctx, req.Id); err != nil {
			return err
		}
		if err := txq.InsertCustomerTagLinks(ctx, store.InsertCustomerTagLinksParams{CustomerID: req.Id, TagIds: wanted}); err != nil {
			return err
		}
		return recordCustomerTagsChanged(ctx, txq, now, req.Id, added, removed, act.Kind, act.Display, act.UserID)
	})
	if err != nil {
		return nil, fmt.Errorf("customers: replace customer tags: %w", err)
	}

	return gen.PutCustomersByIdTags200JSONResponse(gen.CustomerTagsResponse{Tags: after}), nil
}
```

- [ ] **Step 7: Write the timeline event and the set diff**

Append to `apps/server/internal/customers/timeline_events.go`:

```go
// tagSnapshot is customer.tags_changed's element shape (owner and tags design
// D2): which tag, and what it was called at the time. The name is snapshotted
// for the same reason ownerSnapshot's is — renaming a tag later must not
// rewrite what the timeline says was put on the customer.
type tagSnapshot struct {
	TagID uuid.UUID `json:"tagId"`
	Name  string    `json:"name"`
}

// tagSetDiff is what a set replace actually changed: the tags in after and not
// in before, and the reverse. Both come out in after's/before's own order,
// which is name-ascending (queries/tags.sql orders both reads that way), so a
// payload and a summary read in the same order a person sees the chips in.
func tagSetDiff(before, after []tagSnapshot) (added, removed []tagSnapshot) {
	had := make(map[uuid.UUID]bool, len(before))
	for _, t := range before {
		had[t.TagID] = true
	}
	wants := make(map[uuid.UUID]bool, len(after))
	for _, t := range after {
		wants[t.TagID] = true
	}
	for _, t := range after {
		if !had[t.TagID] {
			added = append(added, t)
		}
	}
	for _, t := range before {
		if !wants[t.TagID] {
			removed = append(removed, t)
		}
	}
	return added, removed
}

// tagNameList is the summary's rendering of one side of the change.
func tagNameList(tags []tagSnapshot) string {
	names := make([]string, 0, len(tags))
	for _, t := range tags {
		names = append(names, t.Name)
	}
	return strings.Join(names, ", ")
}

// recordCustomerTagsChanged is PutCustomersByIdTags's own generated event
// (owner and tags design D2). Only called once the handler has established
// that the set actually moved, so added and removed are never both empty —
// which is why the summary can always name at least one half.
//
// Like recordCustomerOwnerChanged there is no changes map: added and removed
// ARE the change, and the spec names this payload shape exactly. Both arrays
// are always present, empty included, so a consumer never has to tell "no tags
// were added" from "the field is missing".
func recordCustomerTagsChanged(ctx context.Context, q *store.Queries, now time.Time, customerID int32, added, removed []tagSnapshot, actorKind, actorDisplay string, actorUserID *uuid.UUID) error {
	if added == nil {
		added = []tagSnapshot{}
	}
	if removed == nil {
		removed = []tagSnapshot{}
	}
	var parts []string
	if len(added) > 0 {
		parts = append(parts, "added "+tagNameList(added))
	}
	if len(removed) > 0 {
		parts = append(parts, "removed "+tagNameList(removed))
	}
	summary := truncateUTF16("Customer tags changed: "+strings.Join(parts, "; "), 500)
	payload := map[string]any{
		"customerId": customerID,
		"added":      added,
		"removed":    removed,
	}
	return recordGeneratedEvent(ctx, q, customerID, now, "customer.tags_changed", summary, payload, 1, actorKind, actorDisplay, actorUserID)
}
```

`timeline_events.go` then imports `strings`.

- [ ] **Step 8: Run everything and verify it passes**

All seven of this delivery's operations are now on the contract and implemented, so the coverage gate can be satisfied for the first time:

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server && mise exec -- go build ./... && mise exec -- go test -count=1 ./internal/customers/... ./internal/openapi/... ./internal/db/... ./internal/module/...
```
Expected: PASS, including Task 3's `Owner|AssignableUsers` tests and the module's operation-coverage gate (`TestMain`'s `RequireCoverage`). If the gate names an operation, the test that should be exercising it is answering something other than a success status.

- [ ] **Step 9: Prove the new tests can fail**

1. Drop the `len(added) == 0 && len(removed) == 0` early return: `TestPutCustomersByIdTags_RecordsTheEventOnlyWhenTheSetChanged` goes red on both the empty-to-empty count and the re-send count.
2. Change the unique index in migration 00024 to `(name)` instead of `(lower(name))` and re-apply: `TestCustomerTags_DuplicateNameIsAConflictIgnoringCase` goes red on `'vip'`. Restore.
3. Make `validateTagRequest` return on the first error instead of collecting both: `TestCustomerTags_RefusesABadNameOrColour` goes red on `len(problem.Errors) != 2`.
4. Add `TagID` to `ListCustomersParams` but not to `CountCustomersParams`: `TestGetCustomers_TagIdFilter`'s `totalCount` assertion goes red.
5. Have `PutCustomersByIdTags` skip `DeleteCustomerTagLinks`: `TestPutCustomersByIdTags_ReplacesTheSet` goes red — a replace that only adds is the bug this test exists for.

- [ ] **Step 10: Vet, lint, commit**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server && mise exec -- go vet ./... && mise exec -- golangci-lint run ./internal/customers/... && mise exec -- go test -count=1 ./internal/customers/... ./internal/openapi/... ./internal/db/...
cd /home/anders/projects/vantigo/vantigo
printf '%s\n\n%s\n' 'feat(customers): a tag vocabulary, and a customer carries any set of it' 'Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>' > /tmp/msg-owner-task4
git add openapi/customers.yaml openapi/COVERAGE.md apps/server/internal/openapi/openapi_test.go apps/server/internal/openapi/specs/customers.yaml apps/server/internal/customers/gen/api.gen.go apps/server/internal/customers/tags.go apps/server/internal/customers/tags_test.go apps/server/internal/customers/values.go apps/server/internal/customers/values_test.go apps/server/internal/customers/timeline_events.go apps/server/internal/customers/export_test.go
git add $(git status --porcelain | awk '/api-schema.d.ts/ {print $2}')
git commit -F /tmp/msg-owner-task4 -- $(git diff --cached --name-only)
git show --stat HEAD && git status --short
```

`export_test.go` is in the pathspec only if Step 2 needed the two validator seams; drop it if `values_test.go` turned out to be white-box.

---

### Task 5: The customer list — owner column, tag chips, two filters, Manage tags (D3)

**Files:**
- Create: `apps/customers/frontend/src/api/tags.ts`, `apps/customers/frontend/src/api/tags.test.ts`, `apps/customers/frontend/src/pages/-manage-tags-modal.tsx`, `apps/customers/frontend/src/pages/-manage-tags-modal.test.tsx`, `apps/host/frontend/src/routes/customers/-customers-list.tsx`
- Modify: `apps/customers/frontend/src/api/customers.ts` (`owner`/`tags` on `CustomerResponse`, `normalizeCustomer`, `CustomersQueryParams`, `CustomersListSearch`, `customersListParams`), `apps/customers/frontend/src/api/customers.test.ts`, `apps/customers/frontend/src/pages/customers.index.tsx`, `apps/customers/frontend/src/pages/-customers.index.test.tsx`, `apps/customers/frontend/src/i18n.ts` (both catalogs), `apps/host/frontend/src/routes/customers/index.tsx`
- Read first (do not change): `apps/customers/frontend/src/pages/customers.index.tsx` (the whole page — `useSearch({strict:false})`, `listSearch`, `useDebouncedListSearch`, `filterBy`, the `""` sentinel the two existing `Select`s use for "all", `showIdentity`'s conditional columns), `apps/customers/frontend/src/api/customers.ts:151-173` (`RawCustomerResponse`/`normalizeCustomer`, the omitted-to-null boundary), `apps/host/frontend/src/routes/customers/index.tsx` (`validateSearch`, `loaderDeps`, `oneOf`), `apps/host/frontend/src/routes/customers/-customer-overview-tab.tsx` (how a host wrapper computes a capability prop)

**Interfaces:**
- Produces (`src/api/tags.ts`):
```ts
export interface CustomerTag { id: string; name: string; color: string | null }
export interface CustomerTagSummary extends CustomerTag { customerCount: number }
export const customerTagsQueryOptions: () => UseQueryOptions      // ["customers", "tags"]
export const createTag: (input: { name: string; color: string | null }) => Promise<CustomerTagSummary>
export const updateTag: (id: string, input: { name: string; color: string | null }) => Promise<CustomerTagSummary>
export const deleteTag: (id: string) => Promise<void>
export const setCustomerTags: (customerId: number, tagIds: string[]) => Promise<{ tags: CustomerTag[] }>
export const TAG_COLORS: readonly string[]   // the thirteen the server accepts
```
- Produces (`src/api/customers.ts`): `CustomerOwner {userId, displayName, active}`; `CustomerResponse.owner: CustomerOwner | null` and `.tags: CustomerTag[]`; `CustomerOwnerFilter = "me" | "none"`; `CustomersQueryParams.ownerId?: string` and `.tagId?: string`; `CustomersListSearch.ownerId?: CustomerOwnerFilter | string` and `.tagId?: string`.
- Produces: `CustomersPage` takes `{ canEdit?: boolean }`; `ManageTagsModal` takes `{ opened, onClose }`.

- [ ] **Step 1: Write the failing API tests**

Create `apps/customers/frontend/src/api/tags.test.ts` in the style of `billing-profile.test.ts` (read it first for the fetch-stubbing convention this package uses):

```ts
import { afterEach, describe, expect, it, vi } from "vitest";
import { createTag, customerTagsQueryOptions, deleteTag, setCustomerTags, updateTag } from "./tags";

const jsonResponse = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const stubFetch = (respond: (url: string, init?: RequestInit) => Response) => {
  const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) =>
    Promise.resolve(respond(String(input), init)),
  );
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
};

describe("tags api", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("lists tags with their customer counts", async () => {
    stubFetch(() => jsonResponse([{ id: "t1", name: "VIP", color: "grape", customerCount: 3 }]));
    const options = customerTagsQueryOptions();
    expect(options.queryKey).toEqual(["customers", "tags"]);
    await expect(options.queryFn?.({} as never)).resolves.toEqual([
      { id: "t1", name: "VIP", color: "grape", customerCount: 3 },
    ]);
  });

  it("normalises a tag whose colour the wire omits", async () => {
    // color is nullable and omitempty on the wire, so a tag with no colour
    // arrives without the key at all. Absent and null mean the same thing, and
    // the boundary is where that is decided — every component downstream reads
    // `color: string | null`.
    stubFetch(() => jsonResponse([{ id: "t2", name: "Prospect", customerCount: 0 }]));
    const tags = (await customerTagsQueryOptions().queryFn?.({} as never)) as { color: string | null }[];
    expect(tags[0].color).toBeNull();
  });

  it("creates, renames and deletes a tag on its own URLs", async () => {
    const fetchMock = stubFetch((url, init) => {
      if (init?.method === "DELETE") return new Response(null, { status: 204 });
      return jsonResponse({ id: "t1", name: "Key account", color: "teal", customerCount: 1 });
    });
    await createTag({ name: "Key account", color: "teal" });
    await updateTag("t1", { name: "Key account", color: null });
    await deleteTag("t1");

    const calls = fetchMock.mock.calls.map(([url, init]) => `${(init as RequestInit)?.method} ${String(url)}`);
    expect(calls).toEqual([
      "POST /api/v1/customers/tags",
      "PUT /api/v1/customers/tags/t1",
      "DELETE /api/v1/customers/tags/t1",
    ]);
    const renameBody = JSON.parse(String((fetchMock.mock.calls[1][1] as RequestInit).body));
    expect(renameBody).toEqual({ name: "Key account", color: null });
  });

  it("replaces a customer's tag set in one call", async () => {
    const fetchMock = stubFetch(() => jsonResponse({ tags: [{ id: "t1", name: "VIP", color: null }] }));
    await expect(setCustomerTags(1001, ["t1"])).resolves.toEqual({ tags: [{ id: "t1", name: "VIP", color: null }] });
    const [url, init] = fetchMock.mock.calls[0];
    expect(String(url)).toBe("/api/v1/customers/1001/tags");
    expect((init as RequestInit).method).toBe("PUT");
    expect(JSON.parse(String((init as RequestInit).body))).toEqual({ tagIds: ["t1"] });
  });
});
```

Append to `apps/customers/frontend/src/api/customers.test.ts`:

```ts
describe("customers list params and normalisation, owner and tags", () => {
  it("normalises an absent owner to null and absent tags to an empty array", () => {
    // Exactly the body the server sends for an unowned, untagged customer:
    // `owner` is omitted entirely (the contract's own wording) and, for a
    // response recorded before this delivery, so is `tags`. Both mean "none",
    // and the boundary is the one place that is decided.
    const raw = {
      id: 1001,
      customerNumber: 5001,
      name: "Equinor",
      status: "active",
      type: "business",
      createdAt: "2026-06-01T10:00:00Z",
      updatedAt: "2026-07-01T10:00:00Z",
      identity: null,
    };
    const normalized = normalizeCustomerForTest(raw);
    expect(normalized.owner).toBeNull();
    expect(normalized.tags).toEqual([]);
  });

  it("keeps an owner and its tags as they arrived", () => {
    const normalized = normalizeCustomerForTest({
      id: 1001,
      customerNumber: 5001,
      name: "Equinor",
      status: "active",
      type: "business",
      createdAt: "2026-06-01T10:00:00Z",
      updatedAt: "2026-07-01T10:00:00Z",
      identity: null,
      owner: { userId: "u1", displayName: "Kari Nordmann", active: true },
      tags: [{ id: "t1", name: "VIP" }],
    });
    expect(normalized.owner).toEqual({ userId: "u1", displayName: "Kari Nordmann", active: true });
    expect(normalized.tags).toEqual([{ id: "t1", name: "VIP", color: null }]);
  });

  it("puts the two new filters on the query string and leaves them off when unset", () => {
    expect(customersListParams({ page: 1, search: "", ownerId: "me", tagId: "t1" })).toMatchObject({
      ownerId: "me",
      tagId: "t1",
    });
    const bare = customersListParams({ page: 1, search: "" });
    expect(bare.ownerId).toBeUndefined();
    expect(bare.tagId).toBeUndefined();
  });
});
```

`normalizeCustomerForTest` does not exist: export `normalizeCustomer` from `src/api/customers.ts` (it is currently module-private) and import it under that name, or add `export { normalizeCustomer as normalizeCustomerForTest }`. Check how `customers.test.ts` already reaches module-private helpers and follow it rather than inventing a second convention.

- [ ] **Step 2: Write the failing list-page tests**

Append to `apps/customers/frontend/src/pages/-customers.index.test.tsx`. Its existing `stubFetch` answers one shape for every non-stats URL, so extend it first to answer `/api/v1/customers/tags` with a tag list, then add:

```tsx
const tagRows = [
  { id: "t1", name: "VIP", color: "grape", customerCount: 2 },
  { id: "t2", name: "Prospect", color: null, customerCount: 0 },
];

// ownedRow is the wire body for a customer with an owner and one tag —
// literally what the server sends, nothing invented.
const ownedRow = {
  ...defaultRow,
  owner: { userId: "u1", displayName: "Kari Nordmann", active: true },
  tags: [{ id: "t1", name: "VIP", color: "grape" }],
};

describe("CustomersPage, owner and tags", () => {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
    router.search = { page: 1, search: "" };
    router.navigate.mockReset();
  });

  it("shows the owner's name and the tag chips on the row", async () => {
    stubFetch([ownedRow]);
    renderPage();
    await screen.findByText("Equinor");
    expect(screen.getByText("Kari Nordmann")).toBeInTheDocument();
    expect(screen.getByText("VIP")).toBeInTheDocument();
  });

  it("marks an owner the directory says is inactive", async () => {
    stubFetch([{ ...ownedRow, owner: { userId: "u1", displayName: "Kari Nordmann", active: false } }]);
    renderPage();
    await screen.findByText("Kari Nordmann");
    // The name is still shown — nothing is revoked (design D1) — with a hint
    // that the account can no longer act.
    expect(screen.getByTitle("This account is inactive")).toBeInTheDocument();
  });

  it("shows an em dash for an unowned customer", async () => {
    stubFetch([{ ...defaultRow, owner: null, tags: [] }]);
    renderPage();
    await screen.findByText("Equinor");
    expect(screen.queryByText("Kari Nordmann")).not.toBeInTheDocument();
  });

  it("drives the URL from the Owner filter and sends ownerId to the API", async () => {
    const fetchMock = stubFetch([ownedRow]);
    renderPage();
    await screen.findByText("Equinor");

    await userEvent.click(screen.getByRole("textbox", { name: "Owner" }));
    await userEvent.click(await screen.findByRole("option", { name: "Mine" }));

    expect(router.navigate).toHaveBeenCalledWith(
      expect.objectContaining({ search: expect.objectContaining({ ownerId: "me", page: 1 }) }),
    );

    // The request is found by method and URL, never by "the last call": the
    // stats and tags queries land on their own clocks.
    router.search = { page: 1, search: "", ownerId: "me" };
    cleanup();
    renderPage();
    await screen.findByText("Equinor");
    const listCalls = fetchMock.mock.calls
      .map(([url]) => String(url))
      .filter((url) => url.startsWith("/api/v1/customers?"));
    expect(listCalls.some((url) => url.includes("ownerId=me"))).toBe(true);
  });

  it("offers Unassigned as the other Owner filter", async () => {
    stubFetch([ownedRow]);
    renderPage();
    await screen.findByText("Equinor");
    await userEvent.click(screen.getByRole("textbox", { name: "Owner" }));
    await userEvent.click(await screen.findByRole("option", { name: "Unassigned" }));
    expect(router.navigate).toHaveBeenCalledWith(
      expect.objectContaining({ search: expect.objectContaining({ ownerId: "none", page: 1 }) }),
    );
  });

  it("drives the URL from the Tag filter, naming the tags the installation has", async () => {
    stubFetch([ownedRow]);
    renderPage();
    await screen.findByText("Equinor");
    await userEvent.click(screen.getByRole("textbox", { name: "Tag" }));
    await userEvent.click(await screen.findByRole("option", { name: "Prospect" }));
    expect(router.navigate).toHaveBeenCalledWith(
      expect.objectContaining({ search: expect.objectContaining({ tagId: "t2", page: 1 }) }),
    );
  });

  it("opens Manage tags only for a caller who may edit", async () => {
    stubFetch([ownedRow]);
    renderPage({ canEdit: true });
    await screen.findByText("Equinor");
    await userEvent.click(screen.getByRole("button", { name: "Manage tags" }));
    expect(await screen.findByRole("dialog")).toHaveTextContent("Manage tags");

    cleanup();
    renderPage();
    await screen.findByText("Equinor");
    expect(screen.queryByRole("button", { name: "Manage tags" })).not.toBeInTheDocument();
  });
});
```

`renderPage` currently takes no arguments: give it an optional props object (`const renderPage = (props: { canEdit?: boolean } = {}) => render(… <CustomersPage {...props} /> …)`) — the existing calls keep working.

And `-manage-tags-modal.test.tsx`:

```tsx
import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ManageTagsModal } from "./-manage-tags-modal";

const jsonResponse = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

const tagRows = [
  { id: "t1", name: "VIP", color: "grape", customerCount: 2 },
  { id: "t2", name: "Prospect", color: null, customerCount: 0 },
];

const stubFetch = () =>
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method === "DELETE") return Promise.resolve(new Response(null, { status: 204 }));
      if (init?.method === "PUT" || init?.method === "POST") {
        return Promise.resolve(jsonResponse({ id: "t1", name: "Key account", color: "teal", customerCount: 2 }));
      }
      return Promise.resolve(jsonResponse(tagRows));
    }),
  );

const renderModal = () =>
  render(
    <MantineProvider env="test">
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <ManageTagsModal opened onClose={() => {}} />
      </QueryClientProvider>
    </MantineProvider>,
  );

describe("ManageTagsModal", () => {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it("lists every tag with how many customers carry it", async () => {
    stubFetch();
    renderModal();
    await screen.findByText("VIP");
    expect(screen.getByText("2 customers")).toBeInTheDocument();
    expect(screen.getByText("No customers")).toBeInTheDocument();
  });

  it("renames a tag through its own PUT", async () => {
    stubFetch();
    renderModal();
    await userEvent.click(await screen.findByRole("button", { name: "Rename VIP" }));
    const input = screen.getByRole("textbox", { name: "Tag name" });
    await userEvent.clear(input);
    await userEvent.type(input, "Key account");
    await userEvent.click(screen.getByRole("button", { name: "Save changes" }));

    await waitFor(() => {
      const calls = (fetch as unknown as { mock: { calls: [unknown, RequestInit?][] } }).mock.calls;
      const put = calls.find(([url, init]) => init?.method === "PUT" && String(url) === "/api/v1/customers/tags/t1");
      expect(put).toBeDefined();
      expect(JSON.parse(String(put?.[1]?.body))).toEqual({ name: "Key account", color: "grape" });
    });
  });

  it("says how many customers a delete will affect, and only deletes on confirmation", async () => {
    stubFetch();
    renderModal();
    await userEvent.click(await screen.findByRole("button", { name: "Delete VIP" }));
    expect(screen.getByText("VIP is on 2 customers. Deleting it removes it from all of them.")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Delete tag" }));

    await waitFor(() => {
      const calls = (fetch as unknown as { mock: { calls: [unknown, RequestInit?][] } }).mock.calls;
      expect(calls.some(([url, init]) => init?.method === "DELETE" && String(url) === "/api/v1/customers/tags/t1")).toBe(true);
    });
  });

  it("shows the server's field error when a name is refused", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
        if (init?.method === "PUT") {
          return Promise.resolve(
            new Response(JSON.stringify({ errors: { name: ["A tag name cannot be null or empty"] } }), {
              status: 400,
              headers: { "Content-Type": "application/problem+json" },
            }),
          );
        }
        return Promise.resolve(jsonResponse(tagRows));
      }),
    );
    renderModal();
    await userEvent.click(await screen.findByRole("button", { name: "Rename VIP" }));
    await userEvent.click(screen.getByRole("button", { name: "Save changes" }));
    expect(await screen.findByText("A tag name cannot be null or empty")).toBeInTheDocument();
  });
});
```

- [ ] **Step 3: Run the frontend tests to verify they fail**

```bash
cd /home/anders/projects/vantigo/vantigo && mise exec -- bun run --cwd apps/customers/frontend test
```
Expected: FAIL — `Cannot find module './tags'`, `Cannot find module './-manage-tags-modal'`, and the list-page cases failing on the missing column, chips, filters and button.

- [ ] **Step 4: Write `src/api/tags.ts`**

```ts
import { keepPreviousData, queryOptions } from "@tanstack/react-query";
import { request } from "./request";

/**
 * The tag vocabulary (owner and tags design D2). `color` is one of Mantine's
 * named colours or null — the server validates it against exactly this list,
 * so a chip can pass it straight to Mantine's `color` prop without sanitising
 * anything.
 */
export const TAG_COLORS = [
  "gray",
  "red",
  "pink",
  "grape",
  "violet",
  "indigo",
  "blue",
  "cyan",
  "teal",
  "green",
  "lime",
  "yellow",
  "orange",
] as const;

export interface CustomerTag {
  id: string;
  name: string;
  color: string | null;
}

/** A tag with how many customers carry it — what the vocabulary list answers (design D3). */
export interface CustomerTagSummary extends CustomerTag {
  customerCount: number;
}

/**
 * `color` is nullable and omitted when unset, so a tag with no colour arrives
 * without the key at all. Absent and null mean the same thing, and this is the
 * one place that is decided — every component downstream reads
 * `color: string | null`, the same treatment `contactInfo` gets.
 */
type RawCustomerTag = Omit<CustomerTag, "color"> & { color?: string | null };
type RawCustomerTagSummary = RawCustomerTag & { customerCount: number };

export const normalizeTag = (raw: RawCustomerTag): CustomerTag => ({
  id: raw.id,
  name: raw.name,
  color: raw.color ?? null,
});

const normalizeTagSummary = (raw: RawCustomerTagSummary): CustomerTagSummary => ({
  ...normalizeTag(raw),
  customerCount: raw.customerCount,
});

/**
 * Every tag, name-ascending, with its customer count. One key for the whole
 * installation — a vocabulary is not paginated and not per-customer — and it
 * sits under the `["customers"]` prefix so every existing broad invalidation
 * refreshes it too.
 */
export const customerTagsQueryOptions = () =>
  queryOptions({
    queryKey: ["customers", "tags"],
    queryFn: async ({ signal }) =>
      (await request<RawCustomerTagSummary[]>("/api/v1/customers/tags", { signal })).map(normalizeTagSummary),
    placeholderData: keepPreviousData,
  });

export interface TagInput {
  name: string;
  color: string | null;
}

export const createTag = async (input: TagInput): Promise<CustomerTagSummary> =>
  normalizeTagSummary(
    await request<RawCustomerTagSummary>("/api/v1/customers/tags", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(input),
    }),
  );

export const updateTag = async (id: string, input: TagInput): Promise<CustomerTagSummary> =>
  normalizeTagSummary(
    await request<RawCustomerTagSummary>(`/api/v1/customers/tags/${id}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(input),
    }),
  );

export const deleteTag = (id: string) => request<void>(`/api/v1/customers/tags/${id}`, { method: "DELETE" });

/**
 * Replaces a customer's tag set (design D2). There is no revision: tags are
 * off the customer row, so this write bumps nothing and needs no optimistic
 * token — two concurrent replaces are last-wins, which is what replacing a set
 * means.
 */
export const setCustomerTags = async (customerId: number, tagIds: string[]): Promise<{ tags: CustomerTag[] }> => {
  const answered = await request<{ tags: RawCustomerTag[] }>(`/api/v1/customers/${customerId}/tags`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ tagIds }),
  });
  return { tags: answered.tags.map(normalizeTag) };
};
```

- [ ] **Step 5: Extend `src/api/customers.ts`**

```ts
/**
 * The user accountable for this customer relationship (owner and tags design
 * D1). `active` is false for an account that has been disabled since it was
 * assigned, and for one the directory no longer knows at all — which reads as
 * `displayName: "Unknown user"`. Either way the customer still has an owner:
 * nothing is revoked behind the caller's back.
 */
export interface CustomerOwner {
  userId: string;
  displayName: string;
  active: boolean;
}

/** The list's Owner filter: the caller's own customers, or the unassigned ones. */
export type CustomerOwnerFilter = "me" | "none";
```

On `CustomerResponse`, replacing nothing and adding two fields:

```ts
  /** Design D1. Null when the customer is unowned; the wire omits the field entirely in that case. */
  owner: CustomerOwner | null;
  /** Design D2. Always an array — the boundary turns an omitted field into `[]`. */
  tags: CustomerTag[];
```

`RawCustomerResponse` and `normalizeCustomer` grow the same treatment `contactInfo` already gets — and `normalizeCustomer` stops taking its early-return shortcut, because `owner` and `tags` need normalising on every response, not only when `contactInfo` is present:

```ts
type RawCustomerResponse = Omit<CustomerResponse, "contactInfo" | "owner" | "tags"> & {
  contactInfo?: Partial<CustomerContactInfo>;
  owner?: CustomerOwner | null;
  tags?: { id: string; name: string; color?: string | null }[];
};

export const normalizeCustomer = (raw: RawCustomerResponse): CustomerResponse => ({
  ...raw,
  ...(raw.contactInfo
    ? {
        contactInfo: {
          email: raw.contactInfo.email ?? null,
          phone: raw.contactInfo.phone ?? null,
          website: raw.contactInfo.website ?? null,
        },
      }
    : {}),
  owner: raw.owner ?? null,
  tags: (raw.tags ?? []).map(normalizeTag),
});
```

(import `type CustomerTag, normalizeTag` from `./tags`; the two modules must not import each other in a cycle — `tags.ts` imports only `./request`, so this direction is the safe one.)

`CustomersQueryParams` gains `ownerId?: string; tagId?: string`; `fetchCustomers` sets each when present (`if (params.ownerId) searchParams.set("ownerId", params.ownerId)`, same for `tagId`); `CustomersListSearch` gains `ownerId?: CustomerOwnerFilter | string; tagId?: string`; `customersListParams` passes both through (`ownerId: search.ownerId, tagId: search.tagId`).

- [ ] **Step 6: Write `-manage-tags-modal.tsx`**

```tsx
import {
  ActionIcon,
  Alert,
  Button,
  ColorSwatch,
  Group,
  Modal,
  Select,
  Stack,
  Table,
  Text,
  TextInput,
} from "@mantine/core";
import { useForm } from "@mantine/form";
import { notifications } from "@mantine/notifications";
import { IconPencil, IconTrash } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { EmptyState, useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { ApiValidationError } from "../api/request";
import {
  type CustomerTagSummary,
  TAG_COLORS,
  createTag,
  customerTagsQueryOptions,
  deleteTag,
  updateTag,
} from "../api/tags";
import "../i18n";

/**
 * The tag vocabulary's own editor (owner and tags design D3): rename, recolour
 * and delete, reached from the list page's Tag filter and only for a caller the
 * host says may edit (`customers:update`). It is the one place a tag is deleted
 * from, and the confirmation says how many customers carry it — which is why
 * `GET /customers/tags` answers a `customerCount` at all.
 *
 * Every mutation invalidates the broad `["customers"]` prefix rather than only
 * `["customers", "tags"]`: renaming or deleting a tag changes what the chips on
 * every customer row and every customer page say, and those live under other
 * keys.
 */
export const ManageTagsModal = ({ opened, onClose }: { opened: boolean; onClose: () => void }) => {
  const { t } = useI18n("customers");
  const { data: tags } = useQuery({ ...customerTagsQueryOptions(), enabled: opened });
  const [editing, setEditing] = useState<CustomerTagSummary | null>(null);
  const [deleting, setDeleting] = useState<CustomerTagSummary | null>(null);

  return (
    <Modal opened={opened} onClose={onClose} title={t("manageTags")} centered>
      <Stack>
        {(tags ?? []).length === 0 && <EmptyState size="sm" title={t("noTagsYet")} />}
        {(tags ?? []).length > 0 && (
          <Table>
            <Table.Tbody>
              {(tags ?? []).map((tag) => (
                <Table.Tr key={tag.id}>
                  <Table.Td>
                    <Group gap="xs" wrap="nowrap">
                      <ColorSwatch color={`var(--mantine-color-${tag.color ?? "gray"}-6)`} size={12} />
                      <Text size="sm">{tag.name}</Text>
                    </Group>
                  </Table.Td>
                  <Table.Td>
                    <Text size="sm" c="dimmed">
                      {tag.customerCount === 0 ? t("tagOnNoCustomers") : t("tagOnCustomers", { count: tag.customerCount })}
                    </Text>
                  </Table.Td>
                  <Table.Td w={80}>
                    <Group gap={4} justify="flex-end" wrap="nowrap">
                      <ActionIcon
                        variant="subtle"
                        color="gray"
                        aria-label={t("renameNamedTag", { name: tag.name })}
                        onClick={() => setEditing(tag)}
                      >
                        <IconPencil size={16} />
                      </ActionIcon>
                      <ActionIcon
                        variant="subtle"
                        color="red"
                        aria-label={t("deleteNamedTag", { name: tag.name })}
                        onClick={() => setDeleting(tag)}
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
        <TagForm tag={editing} onDone={() => setEditing(null)} />
        <DeleteTagConfirmation tag={deleting} onDone={() => setDeleting(null)} />
      </Stack>
    </Modal>
  );
};

interface TagFormValues {
  name: string;
  color: string;
}

/**
 * Creates a tag when `tag` is null and renames/recolours it otherwise — one
 * form, because the fields and the validation are identical and the only
 * difference is which request it sends. The colour `Select`'s own "no colour"
 * entry is the `""` sentinel the list page's filters use, for the same reason:
 * a Mantine `Select` needs a real string among its `data` to offer a row.
 */
const TagForm = ({ tag, onDone }: { tag: CustomerTagSummary | null; onDone: () => void }) => {
  const { t } = useI18n("customers");
  const queryClient = useQueryClient();
  const form = useForm<TagFormValues>({ initialValues: { name: tag?.name ?? "", color: tag?.color ?? "" } });
  const [seen, setSeen] = useState(tag);
  if (tag !== seen) {
    setSeen(tag);
    form.setValues({ name: tag?.name ?? "", color: tag?.color ?? "" });
    form.clearErrors();
  }

  const mutation = useMutation({
    mutationFn: (values: TagFormValues) => {
      const input = { name: values.name, color: values.color || null };
      return tag ? updateTag(tag.id, input) : createTag(input);
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["customers"] });
      onDone();
      notifications.show({ color: "teal", title: t("tagSaved"), message: t("tagSavedMessage") });
    },
    onError: (error) => {
      if (error instanceof ApiValidationError) {
        form.setErrors(error.fieldErrors);
        return;
      }
      notifications.show({ color: "red", title: t("tagCouldNotBeSaved"), message: error.message });
    },
  });

  if (!tag) return null;
  return (
    <form onSubmit={form.onSubmit((values) => mutation.mutate(values))}>
      <Stack gap="xs">
        <TextInput label={t("tagName")} data-autofocus {...form.getInputProps("name")} />
        <Select
          label={t("tagColour")}
          allowDeselect={false}
          data={[{ value: "", label: t("tagNoColour") }, ...TAG_COLORS.map((c) => ({ value: c, label: t(`tagColour_${c}`) }))]}
          {...form.getInputProps("color")}
        />
        <Group justify="flex-end">
          <Button variant="default" onClick={onDone}>
            {t("cancel")}
          </Button>
          <Button type="submit" loading={mutation.isPending}>
            {t("saveChanges")}
          </Button>
        </Group>
      </Stack>
    </form>
  );
};

/**
 * The delete confirmation, which exists to say what the delete will actually
 * do: a tag is removed from every customer carrying it (the table's cascade),
 * and `customerCount` is how many that is.
 */
const DeleteTagConfirmation = ({ tag, onDone }: { tag: CustomerTagSummary | null; onDone: () => void }) => {
  const { t } = useI18n("customers");
  const queryClient = useQueryClient();
  const mutation = useMutation({
    mutationFn: () => deleteTag(String(tag?.id)),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["customers"] });
      onDone();
      notifications.show({ color: "teal", title: t("tagDeleted"), message: t("tagDeletedMessage") });
    },
    onError: (error) => notifications.show({ color: "red", title: t("tagCouldNotBeDeleted"), message: error.message }),
  });

  if (!tag) return null;
  return (
    <Alert color="red" title={t("deleteTag")}>
      <Stack gap="xs">
        <Text size="sm">
          {tag.customerCount === 0
            ? t("deleteTagNoCustomers", { name: tag.name })
            : t("deleteTagWarning", { name: tag.name, count: tag.customerCount })}
        </Text>
        <Group justify="flex-end">
          <Button size="xs" variant="default" onClick={onDone}>
            {t("cancel")}
          </Button>
          <Button size="xs" color="red" loading={mutation.isPending} onClick={() => mutation.mutate()}>
            {t("deleteTag")}
          </Button>
        </Group>
      </Stack>
    </Alert>
  );
};
```

- [ ] **Step 7: Extend `customers.index.tsx`**

Five changes, in the page's own idiom:

(a) the props and the two new sentinels, beside `ALL_STATUSES`/`ALL_TYPES`:

```tsx
const ALL_OWNERS = "";
const ALL_TAGS = "";

const OWNER_FILTERS: CustomerOwnerFilter[] = ["me", "none"];
const isOwnerFilter = (value: string): value is CustomerOwnerFilter => (OWNER_FILTERS as string[]).includes(value);
```

(b) `CustomersPage` takes `{ canEdit }`, reads the two new search values and carries them in `listSearch`, and `filterBy` widens to them:

```tsx
export const CustomersPage = ({ canEdit }: { canEdit?: boolean }) => {
  const { t, formatters } = useI18n("customers");
  const { page, search, status, type, ownerId, tagId, sortBy, sortDirection, create } = useSearch({
    strict: false,
  }) as CustomersSearch;
  …
  const listSearch: CustomersListSearch = { page, search, status, type, ownerId, tagId, sortBy, sortDirection };
  …
  const filterBy = (next: Partial<Pick<CustomersListSearch, "status" | "type" | "ownerId" | "tagId">>) =>
    navigate({ search: { ...listSearch, ...next, page: 1 } });
  const [manageTagsOpened, setManageTagsOpened] = useState(false);
  const { data: tags } = useQuery(customerTagsQueryOptions());
```

(c) the two filters, after the existing `type` `Select` in the same `Group`. The Tag filter and Manage tags sit together, because managing the vocabulary is what a person reaches for while filtering by it (design D3):

```tsx
            <Select
              label={t("owner")}
              w={160}
              allowDeselect={false}
              data={[
                { value: ALL_OWNERS, label: t("all") },
                { value: "me", label: t("ownerMine") },
                { value: "none", label: t("ownerUnassigned") },
              ]}
              value={ownerId ?? ALL_OWNERS}
              onChange={(value) => filterBy({ ownerId: value && isOwnerFilter(value) ? value : undefined })}
            />
            <Group gap="xs" align="end">
              <Select
                label={t("tag")}
                w={160}
                allowDeselect={false}
                data={[
                  { value: ALL_TAGS, label: t("all") },
                  ...(tags ?? []).map((tag) => ({ value: tag.id, label: tag.name })),
                ]}
                value={tagId ?? ALL_TAGS}
                onChange={(value) => filterBy({ tagId: value || undefined })}
              />
              {canEdit && (
                <Button variant="subtle" size="sm" onClick={() => setManageTagsOpened(true)}>
                  {t("manageTags")}
                </Button>
              )}
            </Group>
```

plus `<ManageTagsModal opened={manageTagsOpened} onClose={() => setManageTagsOpened(false)} />` beside the existing `<CustomerFormModal …>`.

(d) the Owner column header after `{t("customerType")}` and before the identity columns, and the cell, with the chips folded into the existing name cell so a row does not grow a third text column:

```tsx
                      <Table.Th>{t("owner")}</Table.Th>
```

```tsx
                        <Table.Td>
                          <Group gap="xs" wrap="wrap">
                            <Text component="span">{customer.name}</Text>
                            {customer.tags.map((tag) => (
                              <Badge key={tag.id} variant="light" color={tag.color ?? "gray"} size="sm">
                                {tag.name}
                              </Badge>
                            ))}
                          </Group>
                        </Table.Td>
```

```tsx
                        <Table.Td>
                          {customer.owner ? (
                            <Text
                              size="sm"
                              component="span"
                              c={customer.owner.active ? undefined : "dimmed"}
                              title={customer.owner.active ? undefined : t("ownerInactiveHint")}
                            >
                              {customer.owner.displayName}
                            </Text>
                          ) : (
                            <Text c="dimmed" component="span">
                              —
                            </Text>
                          )}
                        </Table.Td>
```

(e) `Table.ScrollContainer`'s `minWidth` grows by the new column: `minWidth={showIdentity ? 1200 : 1000}`.

- [ ] **Step 8: Give the host's list route the two filters and `canEdit`**

`apps/host/frontend/src/routes/customers/index.tsx`'s `validateSearch` gains both, refusing anything that is not a shape the API accepts so the URL never carries a value the API would 400 on:

```ts
const OWNER_FILTERS = ["me", "none"] as const;
/** A tag id is a uuid; anything else is no filter rather than a 400 from the API. */
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const ownerFilterOf = (value: unknown): string | undefined => {
  if (typeof value !== "string") return undefined;
  if ((OWNER_FILTERS as readonly string[]).includes(value)) return value;
  return UUID.test(value) ? value : undefined;
};
const tagFilterOf = (value: unknown): string | undefined =>
  typeof value === "string" && UUID.test(value) ? value : undefined;
```

```ts
    ownerId: ownerFilterOf(search.ownerId),
    tagId: tagFilterOf(search.tagId),
```

The page itself needs one capability the host has not passed before, so the route gets a wrapper of its own — `apps/host/frontend/src/routes/customers/-customers-list.tsx`, the same shape `-customer-overview-tab.tsx` has:

```tsx
import { useQuery } from "@tanstack/react-query";
import { CustomersPage } from "@vantigo/customers-ui/pages/customers.index";
import { fetchSession, sessionQueryKey } from "../../api/auth";
import { getAuthorizationMe } from "../../api/authorization";
import { hasPermissions } from "../../navigation";

/**
 * The customer list, plus the one capability it now needs: `canEdit`
 * (`customers:update`) decides whether Manage tags appears beside the Tag
 * filter (owner and tags design D3). The Owner filter needs nothing from the
 * host — "Mine" is the API's own `me`, resolved from the session server-side,
 * so the host never has to tell the page who the caller is.
 *
 * Lives beside the route file rather than inside it, the same reason
 * `-customer-overview-tab.tsx` does: the route file may export nothing but its
 * `Route` without costing the bundle a code split.
 */
export const CustomersListPage = () => {
  const session = useQuery({ queryKey: sessionQueryKey, queryFn: fetchSession, staleTime: 300_000 });
  const authorization = useQuery({
    queryKey: ["authorization", "me", "none"],
    queryFn: getAuthorizationMe,
    enabled: !!session.data,
    retry: false,
    staleTime: 300_000,
  });
  return <CustomersPage canEdit={hasPermissions(authorization.data?.permissions, ["customers:update"])} />;
};
```

and `index.tsx`'s `component: CustomersPage` becomes `component: CustomersListPage` (with the import changed accordingly). Its `loader` also prefetches the vocabulary, so the Tag filter is populated on first paint:

```ts
  loader: ({ context: { queryClient }, deps }) =>
    Promise.all([
      queryClient.ensureQueryData(customersQueryOptions(deps)),
      queryClient.ensureQueryData(customerStatsQueryOptions()),
      queryClient.ensureQueryData(customerTagsQueryOptions()),
    ]),
```

- [ ] **Step 9: Both catalogs**

Add to `en` and to `nb` in `apps/customers/frontend/src/i18n.ts` (keys in the same relative position in both):

| key | en | nb |
| --- | --- | --- |
| `owner` | Owner | Eier |
| `ownerMine` | Mine | Mine |
| `ownerUnassigned` | Unassigned | Uten eier |
| `ownerInactiveHint` | This account is inactive | Denne kontoen er deaktivert |
| `tag` | Tag | Merkelapp |
| `tags` | Tags | Merkelapper |
| `manageTags` | Manage tags | Administrer merkelapper |
| `noTagsYet` | No tags yet. | Ingen merkelapper ennå. |
| `tagName` | Tag name | Navn på merkelapp |
| `tagColour` | Colour | Farge |
| `tagNoColour` | No colour | Ingen farge |
| `tagOnCustomers` | {{count}} customers | {{count}} kunder |
| `tagOnNoCustomers` | No customers | Ingen kunder |
| `renameNamedTag` | Rename {{name}} | Gi {{name}} nytt navn |
| `deleteNamedTag` | Delete {{name}} | Slett {{name}} |
| `deleteTag` | Delete tag | Slett merkelapp |
| `deleteTagWarning` | {{name}} is on {{count}} customers. Deleting it removes it from all of them. | {{name}} er satt på {{count}} kunder. Sletter du den, fjernes den fra alle. |
| `deleteTagNoCustomers` | {{name}} is not on any customer. | {{name}} er ikke satt på noen kunder. |
| `tagSaved` | Tag saved | Merkelapp lagret |
| `tagSavedMessage` | The tag was saved successfully. | Merkelappen ble lagret. |
| `tagCouldNotBeSaved` | Tag could not be saved | Kunne ikke lagre merkelappen |
| `tagDeleted` | Tag deleted | Merkelapp slettet |
| `tagDeletedMessage` | The tag was removed from every customer that carried it. | Merkelappen ble fjernet fra alle kundene som hadde den. |
| `tagCouldNotBeDeleted` | Tag could not be deleted | Kunne ikke slette merkelappen |
| `tagColour_gray` | Grey | Grå |
| `tagColour_red` | Red | Rød |
| `tagColour_pink` | Pink | Rosa |
| `tagColour_grape` | Purple | Lilla |
| `tagColour_violet` | Violet | Fiolett |
| `tagColour_indigo` | Indigo | Indigo |
| `tagColour_blue` | Blue | Blå |
| `tagColour_cyan` | Cyan | Cyan |
| `tagColour_teal` | Teal | Turkis |
| `tagColour_green` | Green | Grønn |
| `tagColour_lime` | Lime | Lime |
| `tagColour_yellow` | Yellow | Gul |
| `tagColour_orange` | Orange | Oransje |

Task 6 adds a few more; `translations:check` fails on any key present in one catalog and not the other, so add each key to both in the same edit.

- [ ] **Step 10: Run the tests to verify they pass, then prove they can fail**

```bash
cd /home/anders/projects/vantigo/vantigo && mise exec -- bun run --cwd apps/customers/frontend test && mise exec -- bun run --cwd apps/host/frontend test
cd /home/anders/projects/vantigo/vantigo && mise exec -- bun run translations:check && mise exec -- bun run i18n:test && mise run frontend:check
```

Then remove three guards, see red, restore:

1. `normalizeCustomer`'s `tags: (raw.tags ?? []).map(normalizeTag)` → `raw.tags as CustomerTag[]`: the "absent tags" case goes red (and would have been a crash on `.map` in the page).
2. The `canEdit &&` around Manage tags: the "only for a caller who may edit" case goes red on its negative half.
3. `filterBy`'s `page: 1`: no test covers it — add one? No: the existing `filterBy` behaviour is already covered for status/type, and ownerId/tagId go through the same function. Instead drop `ownerId` from `listSearch` and watch the Owner filter case go red on the navigate payload — the bug that loses a filter while typing (the Local vs CI note the page's own comment names).

- [ ] **Step 11: Lint and commit**

```bash
cd /home/anders/projects/vantigo/vantigo && bunx biome check --write apps/customers/frontend/src apps/host/frontend/src && bunx biome check .
cd /home/anders/projects/vantigo/vantigo
printf '%s\n\n%s\n' 'feat(customers-ui): the list shows who owns a customer and what it is tagged with, and filters on both' 'Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>' > /tmp/msg-owner-task5
git add apps/customers/frontend/src/api/tags.ts apps/customers/frontend/src/api/tags.test.ts apps/customers/frontend/src/api/customers.ts apps/customers/frontend/src/api/customers.test.ts apps/customers/frontend/src/pages/customers.index.tsx apps/customers/frontend/src/pages/-customers.index.test.tsx apps/customers/frontend/src/pages/-manage-tags-modal.tsx apps/customers/frontend/src/pages/-manage-tags-modal.test.tsx apps/customers/frontend/src/i18n.ts apps/host/frontend/src/routes/customers/index.tsx apps/host/frontend/src/routes/customers/-customers-list.tsx
git commit -F /tmp/msg-owner-task5 -- $(git diff --cached --name-only)
git show --stat HEAD && git status --short
```

If `apps/host/frontend/src/routeTree.gen.ts` changed, commit it too — and only if it changed.

---

### Task 6: The Overview tab — the owner picker and the tags multi-select (D3)

**Files:**
- Create: `apps/customers/frontend/src/api/owner.ts`, `apps/customers/frontend/src/api/owner.test.ts`, `apps/customers/frontend/src/components/owner-picker.tsx`, `apps/customers/frontend/src/lib/search.ts`, `apps/customers/frontend/src/pages/-customer-relationship-card.tsx`, `apps/customers/frontend/src/pages/-customer-relationship-card.test.tsx`
- Modify: `apps/customers/frontend/src/pages/customers.$customerId.tsx` (render the card), `apps/customers/frontend/src/i18n.ts` (both catalogs)
- Read first (do not change): `apps/projects/frontend/src/components/assignee-picker.tsx` (the picker this copies — the debounce, the replaced `filter`, and the rule that keeps the current value on the list), `apps/projects/frontend/src/api/people.ts:14-25` (`assignableUsersQueryOptions`' key and `keepPreviousData`), `apps/customers/frontend/src/pages/-customer-contact-card.tsx` (the card, the row, the conflict alert and `useCustomerReload`'s five callbacks), `apps/customers/frontend/src/lib/customer-reload.ts`, `apps/customers/frontend/src/api/customers.ts` (`syncCustomerRevision`'s doc comment — why the revision is written before the invalidation)

**The spec says "the summary card"; there is no summary card.** The Overview tab is `CustomerContactCard` ("Contact & addresses"), `CustomerRegistryCard`, `CustomerBillingCard`, `CustomerContactsCard` and `CustomerTimeline`; the customer's name, status and badges live in `CustomerDetailHeader`, which is a `PageHeader` and not a card. Rather than putting "who owns this relationship" inside a card titled "Contact & addresses", this task adds a **Relationship** card of its own, first in the Overview stack — one card, two rows, the owner and the tags, which is exactly the pairing the spec's D1/D2 describe. Noted here so a reviewer reads it as a decision and not a drift.

**Interfaces:**
- Produces (`src/api/owner.ts`):
```ts
export interface AssignableUser { userId: string; displayName: string }
export const assignableUsersQueryOptions: (query: string) => UseQueryOptions  // ["customers", "assignable-users", query]
export const setCustomerOwner: (id: number, ownerUserId: string | null, revision?: number) => Promise<CustomerResponse>
```
- Produces (`src/lib/search.ts`): `export const SEARCH_DEBOUNCE_MS = 300`
- Produces: `OwnerPicker({ value, selected, onChange, disabled })`; `CustomerRelationshipCard({ customerId, canEdit })`.

- [ ] **Step 1: Write the failing API tests**

Create `apps/customers/frontend/src/api/owner.test.ts`:

```ts
import { afterEach, describe, expect, it, vi } from "vitest";
import { assignableUsersQueryOptions, setCustomerOwner } from "./owner";

const jsonResponse = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

describe("owner api", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("asks for assignable users with the trimmed query, and with no query at all when it is blank", async () => {
    const fetchMock = vi.fn(() => Promise.resolve(jsonResponse([{ userId: "u1", displayName: "Kari Nordmann" }])));
    vi.stubGlobal("fetch", fetchMock);

    await assignableUsersQueryOptions("  kari  ").queryFn?.({} as never);
    await assignableUsersQueryOptions("   ").queryFn?.({} as never);

    const urls = fetchMock.mock.calls.map(([url]) => String(url));
    expect(urls).toEqual(["/api/v1/customers/assignable-users?query=kari", "/api/v1/customers/assignable-users"]);
    // The key is the trimmed term too, so "kari" and " kari " are one cache entry.
    expect(assignableUsersQueryOptions(" kari ").queryKey).toEqual(["customers", "assignable-users", "kari"]);
  });

  it("sends the owner and the revision, and null to clear", async () => {
    const fetchMock = vi.fn(() =>
      Promise.resolve(
        jsonResponse({
          id: 1001,
          customerNumber: 5001,
          name: "Equinor",
          status: "active",
          type: "business",
          createdAt: "2026-06-01T10:00:00Z",
          updatedAt: "2026-07-01T10:00:00Z",
          identity: null,
          revision: 4,
        }),
      ),
    );
    vi.stubGlobal("fetch", fetchMock);

    const saved = await setCustomerOwner(1001, "u1", 3);
    expect(saved.revision).toBe(4);
    // The response goes through the same normalisation every customer read
    // does, so a caller never has to tell an absent owner from a null one.
    expect(saved.owner).toBeNull();
    expect(saved.tags).toEqual([]);

    await setCustomerOwner(1001, null, 4);
    const bodies = fetchMock.mock.calls.map(([, init]) => JSON.parse(String((init as RequestInit).body)));
    expect(bodies).toEqual([
      { ownerUserId: "u1", revision: 3 },
      { ownerUserId: null, revision: 4 },
    ]);
    expect(fetchMock.mock.calls.every(([url]) => String(url) === "/api/v1/customers/1001/owner")).toBe(true);
  });
});
```

- [ ] **Step 2: Write the failing card tests**

Create `apps/customers/frontend/src/pages/-customer-relationship-card.test.tsx`:

```tsx
import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Suspense } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { CustomerRelationshipCard } from "./-customer-relationship-card";

const jsonResponse = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": status >= 400 ? "application/problem+json" : "application/json" },
  });

// customerBody is literally the wire body for a customer with nothing set:
// no owner key at all, no tags key at all (design D1, D2 — both are omitted
// rather than sent as null/[]), which is the shape a component must survive.
const customerBody = {
  id: 1001,
  customerNumber: 5001,
  name: "Equinor",
  status: "active",
  type: "business",
  createdAt: "2026-06-01T10:00:00Z",
  updatedAt: "2026-07-01T10:00:00Z",
  identity: null,
  revision: 3,
};

const ownedBody = {
  ...customerBody,
  owner: { userId: "u1", displayName: "Kari Nordmann", active: true },
  tags: [{ id: "t1", name: "VIP", color: "grape" }],
};

const tagRows = [
  { id: "t1", name: "VIP", color: "grape", customerCount: 2 },
  { id: "t2", name: "Prospect", color: null, customerCount: 0 },
];

const stubFetch = (options: { customer?: unknown; onWrite?: (url: string, init: RequestInit) => Response } = {}) => {
  const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    if (init?.method && options.onWrite) return Promise.resolve(options.onWrite(url, init));
    if (init?.method === "PUT" && url.endsWith("/owner")) return Promise.resolve(jsonResponse({ ...ownedBody, revision: 4 }));
    if (init?.method === "PUT" && url.endsWith("/tags")) return Promise.resolve(jsonResponse({ tags: tagRows.map(({ customerCount, ...t }) => t) }));
    if (init?.method === "POST") return Promise.resolve(jsonResponse({ id: "t3", name: "Churned", customerCount: 0 }, 201));
    if (url.startsWith("/api/v1/customers/assignable-users")) {
      return Promise.resolve(jsonResponse([{ userId: "u2", displayName: "Ola Nordmann" }]));
    }
    if (url === "/api/v1/customers/tags") return Promise.resolve(jsonResponse(tagRows));
    return Promise.resolve(jsonResponse(options.customer ?? customerBody));
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
};

const renderCard = (props: { canEdit?: boolean } = {}) =>
  render(
    <MantineProvider env="test">
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <Suspense fallback={null}>
          <CustomerRelationshipCard customerId={1001} {...props} />
        </Suspense>
      </QueryClientProvider>
    </MantineProvider>,
  );

// putsTo is every write this test made to one URL, found by method and URL —
// never "the last fetch": the tag vocabulary and the debounced user search land
// on their own clocks.
const putsTo = (fetchMock: ReturnType<typeof stubFetch>, url: string) =>
  fetchMock.mock.calls.filter(([u, init]) => String(u) === url && (init as RequestInit)?.method === "PUT");

describe("CustomerRelationshipCard", () => {
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it("renders an owner and tags, and em dashes when there are none", async () => {
    stubFetch({ customer: ownedBody });
    renderCard();
    expect(await screen.findByText("Kari Nordmann")).toBeInTheDocument();
    expect(screen.getByText("VIP")).toBeInTheDocument();

    cleanup();
    stubFetch();
    renderCard();
    await screen.findByText("Owner");
    expect(screen.queryByText("Kari Nordmann")).not.toBeInTheDocument();
    expect(screen.getAllByText("—").length).toBeGreaterThan(0);
  });

  it("says when the owner's account is inactive, and still names them", async () => {
    stubFetch({ customer: { ...ownedBody, owner: { userId: "u1", displayName: "Kari Nordmann", active: false } } });
    renderCard();
    expect(await screen.findByText("Kari Nordmann")).toBeInTheDocument();
    expect(screen.getByText("Inactive")).toBeInTheDocument();
  });

  it("shows no editing affordance without canEdit", async () => {
    stubFetch({ customer: ownedBody });
    renderCard();
    await screen.findByText("Kari Nordmann");
    expect(screen.queryByRole("textbox", { name: "Owner" })).not.toBeInTheDocument();
    expect(screen.queryByRole("textbox", { name: "Tags" })).not.toBeInTheDocument();
  });

  it("assigns an owner the debounced search found, and sends the revision it read", async () => {
    const fetchMock = stubFetch();
    renderCard({ canEdit: true });
    const picker = await screen.findByRole("textbox", { name: "Owner" });
    await userEvent.click(picker);
    await userEvent.type(picker, "Ola");
    await userEvent.click(await screen.findByRole("option", { name: "Ola Nordmann" }, { timeout: 2000 }));

    await waitFor(() => expect(putsTo(fetchMock, "/api/v1/customers/1001/owner")).toHaveLength(1));
    const [, init] = putsTo(fetchMock, "/api/v1/customers/1001/owner")[0];
    expect(JSON.parse(String((init as RequestInit).body))).toEqual({ ownerUserId: "u2", revision: 3 });
  });

  it("keeps the current owner on the list even when the search would drop them", async () => {
    // The search answers only Ola; Kari is the owner and must stay selectable,
    // or the picker blanks the name it exists to show (projects' assignee
    // picker settled this).
    stubFetch({ customer: ownedBody });
    renderCard({ canEdit: true });
    const picker = await screen.findByRole("textbox", { name: "Owner" });
    expect(picker).toHaveValue("Kari Nordmann");
    await userEvent.click(picker);
    await userEvent.type(picker, "Ola");
    expect(await screen.findByRole("option", { name: "Ola Nordmann" }, { timeout: 2000 })).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "Kari Nordmann" })).toBeInTheDocument();
  });

  it("clears the owner with null", async () => {
    const fetchMock = stubFetch({ customer: ownedBody });
    renderCard({ canEdit: true });
    await screen.findByText("VIP");
    await userEvent.click(screen.getByRole("button", { name: "Clear owner" }));

    await waitFor(() => expect(putsTo(fetchMock, "/api/v1/customers/1001/owner")).toHaveLength(1));
    const [, init] = putsTo(fetchMock, "/api/v1/customers/1001/owner")[0];
    expect(JSON.parse(String((init as RequestInit).body))).toEqual({ ownerUserId: null, revision: 3 });
  });

  it("offers Reload after a revision conflict, and re-seeds the revision it will send next", async () => {
    let revision = 3;
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (init?.method === "PUT" && url.endsWith("/owner")) {
        if (revision === 3) {
          revision = 9;
          return Promise.resolve(
            jsonResponse({ title: "Customer revision conflict", status: 409 }, 409),
          );
        }
        return Promise.resolve(jsonResponse({ ...customerBody, revision: 10 }));
      }
      if (url.startsWith("/api/v1/customers/assignable-users")) {
        return Promise.resolve(jsonResponse([{ userId: "u2", displayName: "Ola Nordmann" }]));
      }
      if (url === "/api/v1/customers/tags") return Promise.resolve(jsonResponse(tagRows));
      return Promise.resolve(jsonResponse({ ...customerBody, revision }));
    });
    vi.stubGlobal("fetch", fetchMock);

    renderCard({ canEdit: true });
    const picker = await screen.findByRole("textbox", { name: "Owner" });
    await userEvent.click(picker);
    await userEvent.click(await screen.findByRole("option", { name: "Ola Nordmann" }, { timeout: 2000 }));
    await screen.findByText("Reload");

    await userEvent.click(screen.getByRole("button", { name: "Reload" }));
    await userEvent.click(picker);
    await userEvent.click(await screen.findByRole("option", { name: "Ola Nordmann" }, { timeout: 2000 }));

    await waitFor(() => {
      const puts = fetchMock.mock.calls.filter(
        ([u, init]) => String(u) === "/api/v1/customers/1001/owner" && (init as RequestInit)?.method === "PUT",
      );
      expect(puts).toHaveLength(2);
      expect(JSON.parse(String((puts[1][1] as RequestInit).body)).revision).toBe(9);
    });
  });

  it("replaces the tag set from the multi-select, sending every id at once", async () => {
    const fetchMock = stubFetch({ customer: ownedBody });
    renderCard({ canEdit: true });
    const tagsInput = await screen.findByRole("textbox", { name: "Tags" });
    await userEvent.click(tagsInput);
    await userEvent.click(await screen.findByRole("option", { name: "Prospect" }));

    await waitFor(() => expect(putsTo(fetchMock, "/api/v1/customers/1001/tags")).toHaveLength(1));
    const [, init] = putsTo(fetchMock, "/api/v1/customers/1001/tags")[0];
    // The whole set, not a delta: VIP was already on the customer.
    expect(JSON.parse(String((init as RequestInit).body)).tagIds.sort()).toEqual(["t1", "t2"]);
  });

  it("creates a tag on the fly and then puts it on the customer", async () => {
    const fetchMock = stubFetch({ customer: ownedBody });
    renderCard({ canEdit: true });
    const tagsInput = await screen.findByRole("textbox", { name: "Tags" });
    await userEvent.click(tagsInput);
    await userEvent.type(tagsInput, "Churned");
    await userEvent.click(await screen.findByRole("option", { name: 'Create "Churned"' }));

    await waitFor(() => {
      const posts = fetchMock.mock.calls.filter(
        ([u, init]) => String(u) === "/api/v1/customers/tags" && (init as RequestInit)?.method === "POST",
      );
      expect(posts).toHaveLength(1);
      expect(JSON.parse(String((posts[0][1] as RequestInit).body))).toEqual({ name: "Churned", color: null });
    });
    await waitFor(() => {
      const [, init] = putsTo(fetchMock, "/api/v1/customers/1001/tags")[0];
      expect(JSON.parse(String((init as RequestInit).body)).tagIds).toContain("t3");
    });
  });
});
```

- [ ] **Step 3: Run the tests to verify they fail**

```bash
cd /home/anders/projects/vantigo/vantigo && mise exec -- bun run --cwd apps/customers/frontend test
```
Expected: FAIL — `Cannot find module './owner'`, `Cannot find module './-customer-relationship-card'`.

- [ ] **Step 4: Write `src/lib/search.ts` and `src/api/owner.ts`**

```ts
/**
 * How long a searchable picker waits after a keystroke before asking the API.
 * One value for every picker in the package, so they all feel the same — the
 * same constant projects' own pickers share (apps/projects/frontend/src/lib/search.ts).
 */
export const SEARCH_DEBOUNCE_MS = 300;
```

```ts
import { keepPreviousData, queryOptions } from "@tanstack/react-query";
import { type CustomerResponse, normalizeCustomer } from "./customers";
import { request } from "./request";

/**
 * A user who may be made a customer's owner (owner and tags design D1): the
 * directory's active users, at most twenty, narrowed by what the caller typed.
 * A disabled account is never offered — the API leaves it out — which is why
 * the picker keeps the CURRENT owner on its list separately: an owner disabled
 * after being assigned keeps the customer and must keep its name on screen.
 */
export interface AssignableUser {
  userId: string;
  displayName: string;
}

/**
 * `keepPreviousData` so the list does not blink empty between keystrokes, and
 * the trimmed term is both the query and the key, so " kari " and "kari" are
 * one cache entry rather than two requests for the same answer.
 */
export const assignableUsersQueryOptions = (query: string) => {
  const term = query.trim();
  return queryOptions({
    queryKey: ["customers", "assignable-users", term],
    queryFn: ({ signal }) => {
      const search = term ? `?${new URLSearchParams({ query: term })}` : "";
      return request<AssignableUser[]>(`/api/v1/customers/assignable-users${search}`, { signal });
    },
    placeholderData: keepPreviousData,
  });
};

/**
 * Sets or clears a customer's owner. It answers the whole customer, not just
 * the owner, so a caller can read the fresh revision straight off the response
 * — the same shape `updateContactInfo` has, and the reason `syncCustomerRevision`
 * can run before any invalidation.
 */
export const setCustomerOwner = async (
  id: number,
  ownerUserId: string | null,
  revision?: number,
): Promise<CustomerResponse> =>
  normalizeCustomer(
    await request(`/api/v1/customers/${id}/owner`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ ownerUserId, revision }),
    }),
  );
```

(`normalizeCustomer` is exported in Task 5's Step 5; `request`'s generic is inferred from `normalizeCustomer`'s parameter, so no explicit `RawCustomerResponse` import is needed — if the compiler disagrees, export that type too rather than casting.)

- [ ] **Step 5: Write `src/components/owner-picker.tsx`**

```tsx
import { Select } from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { useQuery } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { assignableUsersQueryOptions } from "../api/owner";
import type { CustomerOwner } from "../api/customers";
import "../i18n";
import { SEARCH_DEBOUNCE_MS } from "../lib/search";

/**
 * Who owns a customer relationship (owner and tags design D1, D3), copied from
 * projects' assignee picker (apps/projects/frontend/src/components/assignee-picker.tsx)
 * because the problem is the same one: a searchable `Select` over a directory
 * the API narrows, whose current value must survive a search that does not
 * contain it.
 *
 * Two rules that look like details and are not:
 *
 *  - `selected` is kept on the option list whatever the search returns. The API
 *    answers ACTIVE users only, and design D1 rules that an owner disabled
 *    after being assigned keeps the customer — so without this the picker would
 *    blank the very name it is meant to be showing.
 *  - Mantine's own `filter` is replaced with one that keeps every option. The
 *    list is already the answer to the search (narrowed by the API, on the
 *    user's email too, which their display name need not contain), so filtering
 *    it again client-side would drop rows the server deliberately returned.
 */
export const OwnerPicker = ({
  value,
  selected,
  onChange,
  disabled,
}: {
  value: string | null;
  /** The owner the customer already has, so a name survives a search that does not contain it. */
  selected?: CustomerOwner | null;
  onChange: (value: string | null) => void;
  disabled?: boolean;
}) => {
  const { t } = useI18n("customers");
  const [search, setSearch] = useState("");
  const [debouncedSearch] = useDebouncedValue(search, SEARCH_DEBOUNCE_MS);
  const { data: found } = useQuery(assignableUsersQueryOptions(debouncedSearch));

  const options = new Map<string, string>();
  for (const user of found ?? []) options.set(user.userId, user.displayName);
  if (value && !options.has(value) && selected) options.set(value, selected.displayName);

  return (
    <Select
      label={t("owner")}
      placeholder={t("searchOwners")}
      searchable
      clearable
      disabled={disabled}
      filter={({ options: parsed }) => parsed}
      onSearchChange={setSearch}
      nothingFoundMessage={t("noAssignableUsers")}
      data={[...options].map(([userId, displayName]) => ({ value: userId, label: displayName }))}
      value={value}
      onChange={onChange}
      clearButtonProps={{ "aria-label": t("clearOwner") }}
    />
  );
};
```

- [ ] **Step 6: Write `-customer-relationship-card.tsx`**

```tsx
import { Alert, Badge, Button, Card, Divider, Group, MultiSelect, Stack, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { IconUserStar } from "@tabler/icons-react";
import { useMutation, useQuery, useQueryClient, useSuspenseQuery } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import {
  ApiConflictError,
  customerQueryOptions,
  syncCustomerRevision,
} from "../api/customers";
import { setCustomerOwner } from "../api/owner";
import { createTag, customerTagsQueryOptions, setCustomerTags } from "../api/tags";
import { OwnerPicker } from "../components/owner-picker";
import { useCustomerReload } from "../lib/customer-reload";
import "../i18n";

/**
 * The customer page's "Relationship" card (owner and tags design D3): who owns
 * the relationship, and how the customer is classified. `canEdit` comes from
 * the host, which reads the caller's `customers:update` permission — this
 * package never fetches permissions itself.
 *
 * Both values ride on the customer read this page already made (the owner is a
 * column, the tags come decorated onto the same response), so this card issues
 * no query of its own for them — only the tag VOCABULARY, which is
 * installation-wide, and the user search, which lives inside the picker.
 *
 * The two writes reload differently, and deliberately:
 *
 *  - The owner is a column on the customer row, so its PUT is revision-guarded
 *    and answers the whole customer: `syncCustomerRevision` writes the fresh
 *    revision into every cache entry that carries it BEFORE the invalidation's
 *    refetches land, or an editor opened in that window sends the revision this
 *    save just replaced. A 409 raises the same conflict alert and Reload the
 *    other row-editing modals use (`useCustomerReload`).
 *  - The tags are off the row: no revision, nothing to sync, no conflict to
 *    handle — a plain invalidation of `["customers"]`, because the chips on the
 *    list and the counts in Manage tags both moved.
 */
export const CustomerRelationshipCard = ({ customerId, canEdit }: { customerId: number; canEdit?: boolean }) => {
  const { t } = useI18n("customers");
  const { data: customer } = useSuspenseQuery(customerQueryOptions(customerId));
  const queryClient = useQueryClient();
  const [conflict, setConflict] = useState(false);
  const [revision, setRevision] = useState(customer.revision);

  const reload = useCustomerReload({
    customerId,
    queryKey: customerQueryOptions(customerId).queryKey,
    fetchFresh: () => queryClient.fetchQuery({ ...customerQueryOptions(customerId), staleTime: 0 }),
    revisionOf: (fresh) => fresh.revision,
    seed: (fresh) => {
      setRevision(fresh.revision);
      setConflict(false);
    },
  });

  const ownerMutation = useMutation({
    mutationFn: (ownerUserId: string | null) => setCustomerOwner(customerId, ownerUserId, revision),
    onSuccess: (saved) => {
      syncCustomerRevision(queryClient, customerId, saved.revision);
      setRevision(saved.revision);
      queryClient.invalidateQueries({ queryKey: ["customers"] });
      setConflict(false);
      notifications.show({ color: "teal", title: t("ownerUpdated"), message: t("ownerUpdatedMessage") });
    },
    onError: (error) => {
      if (error instanceof ApiConflictError && !error.code) {
        setConflict(true);
        return;
      }
      notifications.show({ color: "red", title: t("ownerCouldNotBeSaved"), message: error.message });
    },
  });

  return (
    <Card withBorder padding="lg" radius="md">
      <Stack gap="lg">
        <Group gap="xs">
          <IconUserStar size={18} stroke={1.5} />
          <Text fw={600} component="h3">
            {t("relationship")}
          </Text>
        </Group>

        {conflict && (
          <Alert color="yellow" title={t("customerChangedTitle")}>
            <Stack gap="xs">
              <Text size="sm">{t("customerChangedMessage")}</Text>
              <Text size="sm">{t("customerChangesNotSaved")}</Text>
              {reload.failed && (
                <Text size="sm" c="red">
                  {t("couldNotReload")}
                </Text>
              )}
              <Group justify="flex-end">
                <Button size="xs" variant="light" color="yellow" loading={reload.reloading} onClick={reload.reload}>
                  {t("reload")}
                </Button>
              </Group>
            </Stack>
          </Alert>
        )}

        <Group gap="xs" wrap="nowrap" align="center">
          <Text size="sm" c="dimmed" miw={64}>
            {t("owner")}
          </Text>
          {customer.owner ? (
            <Group gap="xs" wrap="nowrap">
              <Text size="sm">{customer.owner.displayName}</Text>
              {!customer.owner.active && (
                <Badge variant="light" color="gray" size="sm">
                  {t("inactive")}
                </Badge>
              )}
            </Group>
          ) : (
            <Text size="sm">—</Text>
          )}
        </Group>
        {canEdit && (
          <OwnerPicker
            value={customer.owner?.userId ?? null}
            selected={customer.owner}
            disabled={ownerMutation.isPending}
            onChange={(value) => ownerMutation.mutate(value)}
          />
        )}

        <Divider />

        <Group gap="xs" wrap="wrap" align="center">
          <Text size="sm" c="dimmed" miw={64}>
            {t("tags")}
          </Text>
          {customer.tags.length === 0 && <Text size="sm">—</Text>}
          {customer.tags.map((tag) => (
            <Badge key={tag.id} variant="light" color={tag.color ?? "gray"} size="sm">
              {tag.name}
            </Badge>
          ))}
        </Group>
        {canEdit && <TagsEditor customerId={customerId} selected={customer.tags.map((tag) => tag.id)} />}
      </Stack>
    </Card>
  );
};

/**
 * The tag multi-select, with create-on-the-fly (design D3): typing a name no
 * tag has offers *Create "x"*, which POSTs the tag and then replaces the
 * customer's set including it — two calls, in that order, because the set
 * replace can only name ids that exist.
 *
 * Every change sends the WHOLE set, not a delta: that is what the endpoint
 * means, and it is why two people editing the same customer's tags are
 * last-wins rather than silently merged.
 */
const TagsEditor = ({ customerId, selected }: { customerId: number; selected: string[] }) => {
  const { t } = useI18n("customers");
  const queryClient = useQueryClient();
  const { data: vocabulary } = useQuery(customerTagsQueryOptions());
  const [search, setSearch] = useState("");

  const mutation = useMutation({
    mutationFn: (tagIds: string[]) => setCustomerTags(customerId, tagIds),
    onSuccess: () => {
      // No revision to sync: tags are off the customer row (design D2), so the
      // only thing that moved is what the chips and the counts say.
      queryClient.invalidateQueries({ queryKey: ["customers"] });
    },
    onError: (error) => notifications.show({ color: "red", title: t("tagsCouldNotBeSaved"), message: error.message }),
  });

  const createAndAttach = useMutation({
    mutationFn: async (name: string) => {
      const created = await createTag({ name, color: null });
      return setCustomerTags(customerId, [...selected, created.id]);
    },
    onSuccess: () => {
      setSearch("");
      queryClient.invalidateQueries({ queryKey: ["customers"] });
    },
    onError: (error) => notifications.show({ color: "red", title: t("tagCouldNotBeSaved"), message: error.message }),
  });

  const term = search.trim();
  const exists = (vocabulary ?? []).some((tag) => tag.name.toLowerCase() === term.toLowerCase());
  const data = [
    ...(vocabulary ?? []).map((tag) => ({ value: tag.id, label: tag.name })),
    // The create entry is an option rather than a button beside the input, so
    // one keyboard path does both: type, arrow down, enter.
    ...(term && !exists ? [{ value: `create:${term}`, label: t("createTagNamed", { name: term }) }] : []),
  ];

  return (
    <MultiSelect
      label={t("tags")}
      placeholder={t("searchTags")}
      searchable
      data={data}
      value={selected}
      searchValue={search}
      onSearchChange={setSearch}
      disabled={mutation.isPending || createAndAttach.isPending}
      onChange={(next) => {
        const creating = next.find((value) => value.startsWith("create:"));
        if (creating) {
          createAndAttach.mutate(creating.slice("create:".length));
          return;
        }
        mutation.mutate(next);
      }}
    />
  );
};
```

- [ ] **Step 7: Render it on the Overview tab**

In `apps/customers/frontend/src/pages/customers.$customerId.tsx`, import the card and put it **first** in `CustomerOverview`'s `Stack` — who owns the relationship and what kind of customer it is are what a person looks for before the contact details:

```tsx
      <CustomerRelationshipCard customerId={customerId} canEdit={canEdit} />
      <CustomerContactCard customerId={customerId} canEdit={canEdit} registryRecord={registryRecord ?? null} />
```

and extend that component's doc comment with one sentence naming the new card and why it is separate from "Contact & addresses". No host change: `canEdit` is already passed (`-customer-overview-tab.tsx`).

- [ ] **Step 8: Both catalogs**

| key | en | nb |
| --- | --- | --- |
| `relationship` | Relationship | Kunderelasjon |
| `inactive` | Inactive | Deaktivert |
| `searchOwners` | Search for a colleague… | Søk etter en kollega … |
| `noAssignableUsers` | No users found | Fant ingen brukere |
| `clearOwner` | Clear owner | Fjern eier |
| `ownerUpdated` | Owner updated | Eier oppdatert |
| `ownerUpdatedMessage` | The customer's owner was saved. | Kundens eier ble lagret. |
| `ownerCouldNotBeSaved` | Owner could not be saved | Kunne ikke lagre eier |
| `searchTags` | Search or create a tag… | Søk eller opprett en merkelapp … |
| `createTagNamed` | Create "{{name}}" | Opprett «{{name}}» |
| `tagsCouldNotBeSaved` | Tags could not be saved | Kunne ikke lagre merkelappene |

- [ ] **Step 9: Run the tests to verify they pass, then prove they can fail**

```bash
cd /home/anders/projects/vantigo/vantigo && mise exec -- bun run --cwd apps/customers/frontend test
cd /home/anders/projects/vantigo/vantigo && mise exec -- bun run translations:check && mise exec -- bun run i18n:test && mise run frontend:check
```

Then remove three guards, see red, restore:

1. The `if (value && !options.has(value) && selected)` line in `OwnerPicker`: "keeps the current owner on the list" goes red.
2. `setRevision(saved.revision)` in `ownerMutation.onSuccess` — no test covers it directly, so instead break the `seed` callback's `setRevision(fresh.revision)`: the Reload case goes red on the second PUT's revision (it sends 3 again and would 409 forever).
3. `TagsEditor`'s `[...selected, created.id]` → `[created.id]`: the create-on-the-fly case goes red on the set it puts.

- [ ] **Step 10: Lint and commit**

```bash
cd /home/anders/projects/vantigo/vantigo && bunx biome check --write apps/customers/frontend/src && bunx biome check .
cd /home/anders/projects/vantigo/vantigo
printf '%s\n\n%s\n' 'feat(customers-ui): a customer page names its owner and its tags, and lets both be changed there' 'Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>' > /tmp/msg-owner-task6
git add apps/customers/frontend/src/api/owner.ts apps/customers/frontend/src/api/owner.test.ts apps/customers/frontend/src/lib/search.ts apps/customers/frontend/src/components/owner-picker.tsx apps/customers/frontend/src/pages/-customer-relationship-card.tsx apps/customers/frontend/src/pages/-customer-relationship-card.test.tsx apps/customers/frontend/src/pages/customers.\$customerId.tsx apps/customers/frontend/src/i18n.ts
git commit -F /tmp/msg-owner-task6 -- $(git diff --cached --name-only)
git show --stat HEAD && git status --short
```

---

### Task 7: Documentation (D1–D4)

**Files:**
- Modify: `docs/customers.md`, `ROADMAP.md`
- Check and modify only if they enumerate something this delivery changed: `CONTRIBUTING.md`, `docs/module-boundaries.md`

**Interfaces:** none — this task adds no code and no test.

- [ ] **Step 1: Find every place in the docs that enumerates what this delivery changed**

```bash
cd /home/anders/projects/vantigo/vantigo
grep -rn 'customer.contact_info_updated' docs/ CONTRIBUTING.md
grep -rn '40 operations\|fourteen permissions\|fourteen keys' docs/ CONTRIBUTING.md apps/server/internal/customers/module.go
grep -rn 'UserDirectory' docs/module-boundaries.md
```
The first tells you where the timeline's generated event types are listed (two new ones must join them). The second tells you which counts are now wrong — the operation count in `docs/customers.md`'s API section moves by seven; the permission count does **not** move, because this delivery adds no key. The third tells you whether `docs/module-boundaries.md` describes who reads the user directory: customers now does, for the owner, and if that file lists the readers it gains one line.

- [ ] **Step 2: Add the new section to `docs/customers.md`**

Insert a new `## Owner and tags` section after the billing-profile section and before `## The timeline`, written in that file's own voice — full sentences explaining *why*, not a field list:

```markdown
## Owner and tags

Phase 4's first delivery, decided in
[`docs/superpowers/specs/2026-09-23-customers-owner-tags-design.md`](superpowers/specs/2026-09-23-customers-owner-tags-design.md).
Two questions a customer list has to be able to answer: *which of these are
mine?*, and *which of these are of this kind?*

### The owner

`customers.customers.owner_user_id` names **one user** of this installation — not
a team, not free text. The roadmap's "account manager" is one person accountable
for the relationship, and every system this module was benchmarked against models
it as exactly that (Salesforce's Account Owner, HubSpot's Company owner, Business
Central's Salesperson code). Because it is a column on the customer row, it shares
the row's `revision`: `PUT /customers/{id}/owner` is a revision-guarded
sub-resource write exactly like contact info's, so a concurrent edit cannot lose
it, and a request that names the owner the customer already has writes nothing at
all.

The user is named through `contracts.UserDirectory` and nowhere else — the display
name is never stored on the customer. That has three consequences worth stating,
because each is a decision rather than a side effect:

- **There is no foreign key** to `identity.users`. This module may not read
  identity's schema, and a foreign key would decide in the database what the
  design decides in prose: an owner who is later disabled, or whose account is
  removed, **keeps the customer**. Nothing is revoked behind anyone's back.
- The owner is therefore reported with an `active` flag, and an id the directory
  no longer knows reads as `Unknown user` with `active: false` — the same answer
  a timeline entry's actor gets for a vanished account.
- To *be assigned*, on the other hand, a user must exist and be active: a field
  error on `ownerUserId` otherwise (`User <id> does not exist`, `User <id> is
  disabled and cannot own a customer`). `GET /customers/assignable-users` is the
  picker's own search — the directory's active users, at most twenty — and it sits
  behind `customers:update`, because who a customer *could* be given to is only
  useful to whoever may give it.

The list filters on `ownerId`, which takes a user id, the literal `me`, or
`none`. `me` is resolved from the session, never from anything the request says
about who the caller is, which is why "My customers" in the UI needs nothing from
the host. `none` is the manager's "unassigned". Changing the owner records
`customer.owner_changed` on the timeline, with both names snapshotted at the time
of the change, and only when the owner actually changed.

### Tags

Two tables (`customers.tags`, `customers.customer_tags`), shaped after
communications' own tags with one difference that matters: the uniqueness is on
`lower(name)`. A tag is a vocabulary word, so `VIP` and `vip` are the same word —
an installation holding both has a filter that silently splits its customers in
two. Case is still preserved as it was typed.

A colour is one of Mantine's named colours (`gray red pink grape violet indigo
blue cyan teal green lime yellow orange`) or null, validated by the server. That
looks like a layering violation and is not: the alternative is every consumer
sanitising whatever arrived, and a chip painted with a value no stylesheet knows
is an invisible chip.

A customer's tags are **replaced as a set** (`PUT /customers/{id}/tags`), which is
the natural write for a multi-select. There is no `revision` and none is accepted:
tags are off the customer row, so a tag change bumps nothing and two concurrent
replaces are last-wins, which is what replacing a set means. An unknown id is a
field error on `tagIds`. Deleting a tag removes it from every customer through the
join table's own cascade, and `GET /customers/tags` answers a `customerCount` per
tag so the delete confirmation can say how many that is. Renaming a tag records
nothing on the customers carrying it — the tag is the vocabulary, not the
customer — while a set replace records `customer.tags_changed` with what was added
and what was removed, and only when the set moved.

Search does not match owner names or tag names: search stays what it is, the
customer's own fields. Everything here is readable with `customers:view` and
writable with `customers:update` — an owner is not sensitive data and tags are
classification, so a narrower key would be one more thing to configure for no
protection gained.
```

- [ ] **Step 3: Update the four places that now say something untrue**

1. **The list endpoint and search** section: add a paragraph naming the two new filters and their exact refusals (`'ownerId' must be a user id, 'me' or 'none', but was '…'.`, `'tagId' must be a tag id, but was '…'.`), and note that both are applied to the count and the page from one hand-kept WHERE clause.
2. **The API table**: seven new rows, and the operation count in the paragraph above it moves from 40 to 47 (re-count it rather than trusting this plan — `grep -c 'operationId:' openapi/customers.yaml`):

```markdown
| `PUT /{id}/owner` | `customers:update` + `customers:view` |
| `GET /assignable-users` | `customers:update` |
| `PUT /{id}/tags` | `customers:update` + `customers:view` |
| `GET /tags` | `customers:view` |
| `POST /tags` | `customers:update` |
| `PUT /tags/{tagId}`, `DELETE /tags/{tagId}` | `customers:update` |
```

3. **The Permissions section**: one sentence saying this delivery adds no key, and why — the same reasoning the design's D4 gives. Do **not** change the permission count.
4. **The frontend section**: the Owner column, the tag chips beside the name, the Owner and Tag filters, the Manage tags modal reached from the Tag filter with `canEdit`, and the Relationship card on the Overview tab with its picker and multi-select. Say that the host passes one new prop (`canEdit` on the list page, through its own `-customers-list.tsx` wrapper) and that the Owner filter needs nothing new, because `me` is resolved server-side.
5. Wherever Step 1's first grep found the generated event types listed, add `customer.owner_changed` and `customer.tags_changed` with their payload shapes.

- [ ] **Step 4: Mark the delivery in `ROADMAP.md`**

`### Phase 4 — Light CRM` keeps its intro but gains a delivery paragraph in the shape phase 3's deliveries have (`**Delivery A (done)** — …`), with a link to the spec and to `docs/customers.md#owner-and-tags`, naming what shipped (one owner, the `me`/`none` filter, the tag vocabulary with a case-insensitive name, the set replace, the two timeline events) and what is still ahead in the phase (typed contact roles with a primary contact; follow-ups; customer groups with defaults; attachments, still waiting on the storage module). Change the heading to `### Phase 4 — Light CRM (in progress)` only if the file uses that convention for a partly-delivered phase — check phase 2's heading and follow it.

- [ ] **Step 5: Commit**

```bash
cd /home/anders/projects/vantigo/vantigo
printf '%s\n\n%s\n' 'docs(customers): the owner and the tag vocabulary' 'Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>' > /tmp/msg-owner-task7
git add docs/customers.md ROADMAP.md
git commit -F /tmp/msg-owner-task7 -- $(git diff --cached --name-only)
git show --stat HEAD && git status --short
```

(If `CONTRIBUTING.md` or `docs/module-boundaries.md` needed a line, add them to both the `git add` and the pathspec.)

---

### Task 8: Verify the whole branch and open the PR

**Files:** none. This task changes nothing; it proves what the branch does and hands it over.

- [ ] **Step 1: No generation drift**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server && mise exec -- go generate ./... && git status --short
cd /home/anders/projects/vantigo/vantigo && mise exec -- bun run gen:client && git status --short
```
Expected: nothing. A file that changes here means a committed generated file was stale.

- [ ] **Step 2: The whole Go suite**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server && mise exec -- go vet ./... && mise exec -- golangci-lint run && mise exec -- go test -count=1 ./...
```
`golangci-lint run` is run **from `apps/server`**, with no package argument.

- [ ] **Step 3: The race detector, on four CPUs**

The machine has far more cores than CI, and the race detector's own scheduling is what finds a race — pin it:

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
CC="$(command -v zig >/dev/null && echo "$PWD/../../scripts/zig-cc" || echo cc)" CGO_ENABLED=1 \
  taskset -c 0-3 mise exec -- go test -race -count=1 ./internal/customers/... ./internal/db/... ./internal/openapi/... ./internal/module/...
```
If the repo has its own zig-cc wrapper under a different path, use that one (`ls scripts/`); if `go test -race` refuses for want of a C compiler, that wrapper is what supplies it. Nothing in this delivery is concurrent by design, so this is a regression check rather than a proof — the one thing it can catch is two set replaces or two owner writes racing, and `customers_concurrency_test.go`'s existing cases cover the row's own guard.

- [ ] **Step 4: The whole frontend**

```bash
cd /home/anders/projects/vantigo/vantigo
mise exec -- bun run --cwd apps/customers/frontend test
mise exec -- bun run --cwd apps/host/frontend test
mise exec -- bun run translations:check && mise exec -- bun run i18n:test
mise run frontend:check
bunx biome check .
```
Run the two test packages separately: `bun --filter` fan-out times out on this project's CI, and one package at a time is what the repo's own scripts do.

- [ ] **Step 5: Check the trailers and the branch state**

```bash
cd /home/anders/projects/vantigo/vantigo
git log --format='%h %s%n%b' origin/main..HEAD | grep -c 'Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>'
git log --oneline origin/main..HEAD
git status --short
gh run list --branch main --limit 3
```
The count must equal the number of commits on the branch. `gh run list` on `main` first, so a failure that was already there is not mistaken for one this branch caused.

- [ ] **Step 6: Push and open the PR**

```bash
cd /home/anders/projects/vantigo/vantigo
git push -u origin feat/customers-light-crm
gh pr create --base main --head feat/customers-light-crm --title 'feat(customers): an owner and tags for every customer' --body-file /tmp/pr-owner-tags.md
```

The body, written to `/tmp/pr-owner-tags.md` first, covers: what shipped (the owner column and its revision-guarded PUT, assignable users, the `ownerId=me|none|<uuid>` filter, the tag vocabulary with its case-insensitive name and colour rule, the set replace, the `tagId` filter, the two timeline events, the list column and filters, Manage tags, the Relationship card); the **decisions taken while implementing** (each with its reason): a Relationship card rather than a "summary card" that does not exist; `CustomerTagSummary` as the vocabulary's response so a rename answers the same `customerCount` the list does; `query`/`limit` as the assignable-users parameters, as the spec names them, rather than projects' `search`; one new host wrapper (`-customers-list.tsx`) to pass `canEdit` to the list page; no foreign key from `owner_user_id` to `identity.users`; and the seven route pairs added to `KnownServeMuxConflicts`. Also: every new test was shown able to fail, and how. End the body with:

```
🤖 Generated with [Claude Code](https://claude.com/claude-code)
```

- [ ] **Step 7: Watch the checks and fix root causes on the same PR**

```bash
cd /home/anders/projects/vantigo/vantigo && gh pr checks --watch
```
Use the default, non-JSON output. Fix any failure at its root on this branch — never by relaxing a test or an assertion — and push again. **Never merge**: the user merges.

If the PR description needs editing afterwards, `gh pr edit` is broken in this environment; use `gh api --method PATCH repos/{owner}/{repo}/pulls/{number} -f body=@/tmp/pr-owner-tags.md` instead.

---

## Self-review notes

Checked against the spec, section by section:

- **D1** — owner column and migration: Task 1. PUT with revision + no-op + 409: Task 3. Field errors with projects' wording: Task 3. An owner disabled afterwards keeps the customer: Task 3's own test. `assignable-users` capped at 20, active only: Task 3. `owner` on list and detail from one batched `Users` call, `Unknown user`/`active: false` for a vanished id: Task 3 (`decorate`). `ownerId=me|none|<uuid>` with the list's own wording: Task 3. `customer.owner_changed` only on change, with the acting user: Task 3. The directory unchanged (no `OwnerUserID` on `CustomerEntry`): nothing in any task touches `contracts`, deliberately.
- **D2** — two tables, unique on `lower(name)`, cascade both ways: Task 1. Colour validated in Go: Task 4. CRUD with the 409 `tag_exists`, the 1–100 trimmed name: Task 4. Set replace with the field error, no revision, last-wins: Task 4. `tags: []` always present, one query per page: Tasks 1 and 3. `tagId` filter: Tasks 1, 3 (validation) and 4 (behaviour). `customer.tags_changed` only on change; a rename records nothing: Task 4.
- **D3** — Owner column, tag chips, the two filters, Manage tags with `customerCount`: Task 5. Owner row with the debounced picker that keeps its value plus Clear, Tags row with create-on-the-fly, the two reload rules: Task 6. Both catalogs: Tasks 5 and 6.
- **D4** — no new permission key, `customers:view`/`customers:update` throughout, search unchanged: asserted in Task 3's `TestOwnerPermissions` and Task 4's `TestTagPermissions`, documented in Task 7.
- **Testing** section — every case it names has a test in Task 3, 4, 5 or 6; the frontend ones filter by method and URL rather than asserting the last fetch, and one fixture (`customerBody` in Task 6) is literally the wire body with nothing set.
- **Out of scope** — nothing in any task builds teams, several owners, auto-assignment, notifications, tag hierarchies, a `CustomerDirectory` addition, an "Assign to me" shortcut or customer groups.
