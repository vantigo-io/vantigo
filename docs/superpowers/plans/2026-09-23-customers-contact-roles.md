# Typed Contact Roles (phase 4, delivery B) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Keep the free text a customer–contact association carries as a **title**, and add three **typed roles** — `billing`, `project`, `decision_maker` — with exactly one primary contact per role, so a customer can answer "who gets the invoice" and "who approves" and not only "who is this person".

**Architecture:** `customers.customers_contacts.role` is renamed to `title` and made nullable (migration `00025`) — the column already held titles (`CEO`, `CTO`), and the wire keeps answering `role` so the frozen corpus stays true. The typed roles are their own table, `customers.customer_contact_roles(customer_id, contact_id, role, is_primary, created_at)`, with the partial unique index `(customer_id, role) WHERE is_primary` — the addresses' one-primary-per-type invariant, one table over, kept by the same mechanism: every write takes the customer row's `FOR NO KEY UPDATE` lock first and does its demote-before-promote bookkeeping inside that transaction. No new paths: the roles ride on the association's own four endpoints, and the responses gain `title` and an always-present `roles` array. The frontend renames the Role input to Title, adds a Roles checkbox group with a per-role Primary switch, and shows role badges with a star on the primary one.

**Tech Stack:** Go 1.27 (pgx, sqlc, goose, oapi-codegen strict server), PostgreSQL 18, React + Mantine 9 + TanStack Router/Query, vitest, bun, mise.

**Spec:** `docs/superpowers/specs/2026-09-23-customers-contact-roles-design.md` (D1–D5 + "Out of scope" + "Testing"). Read it first; it is binding. It builds on `docs/superpowers/specs/2026-09-21-customers-invoice-ready-design.md` (D3, the typed-address primary invariant this delivery copies verbatim) and on `docs/customers.md`.

## Global Constraints

- Branch `feat/customers-contact-roles` (already checked out; the design commit `fadc4d8` is its tip). Never commit to `main`, never merge, never `--no-verify`.
- **Forbidden git commands:** `git add -A`, `git add .`, `git stash`, `git checkout -- .`, `git restore .`, `git clean`, `git reset --hard`. Commit with an explicit pathspec (`git add <files>` then `git commit -F <msgfile> -- <files>`), then check `git show --stat HEAD` and that `git status --short` shows nothing of yours left. The untracked `go.mod`/`go.sum` in the repo root are not ours: never add or delete them.
- Commit messages: Conventional Commits scoped `customers` / `customers-ui` / `docs`, subject a plain sentence about behaviour (see `git log`). End every message with `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>` exactly — the trailer every commit on this branch and on `main` carries. (If the executing session's own attribution reminder names a different model line, use that one instead, and use the *same* line on every commit of the branch.)
- Toolchain only through mise: `mise exec -- go …`, `mise exec -- bun …`. Capture exit codes before any pipe (`${PIPESTATUS[0]}`).
- Go tests need `TEST_DATABASE_URL` exported in the environment before the first `go test`. **Take the value from the environment** (the project's own test database); this plan does not name one. If it is unset, stop and ask — do not guess a port.
- **The frozen corpus** `openapi/testdata/exchanges/customers.jsonl` is never edited, for any reason. It contains `POST /customers/{id}/contacts` bodies of the shape `{"contactId": N, "role": "CEO"}` and GET lists whose items carry `role`, so:
  - **`role` stays accepted in requests and stays required in responses.** In requests it becomes an optional, deprecated alias of `title` (`title` wins when both are sent); in responses it is the title or `""`.
  - **Nothing is added to an existing `required:` list** — not `title`, not `roles`, even though the server always sends `roles`. Relaxing `role` *out of* a request schema's `required:` is allowed and is the one removal this delivery makes.
  - New schemas (`CustomerContactRole`, `CustomerContactRoleRequest`) may have required fields.
- **The role vocabulary is validated in Go, never by a yaml `enum:` and never by a database `CHECK`** — the three values are `billing`, `project`, `decision_maker`, and the message is the spec's verbatim: `A contact role must be one of 'billing', 'project' or 'decision_maker', but was '%s'`. Body-level validation messages have **no** trailing period (`values.go`); query-parameter messages do (`customers.go`). This delivery adds no query parameter.
- **The customer row is locked first for every write that touches roles** — `LockCustomer` (`queries/addresses.sql:1-19`, `FOR NO KEY UPDATE`) — which now means the association attach, update, detach *and* the contact delete. `pgx.ErrNoRows` from it is the 404.
- **The primary rule is the addresses' rule, verbatim** (`docs/customers.md` §Addresses; `addresses.go`'s `demoteCurrentPrimary`/`promoteOldestOfType`): the **first** holder of a role is its primary whatever the request says; `primary: true` on another contact demotes the current one in the same transaction; `primary: false` on the contact that is the only or the primary holder is **refused** (400, field `roles`); a contact losing a role it was primary for promotes the **longest-standing** remaining holder (`created_at`, then `contact_id`). **Demote happens before promote, and delete happens before promote** — a transient two-primaries state, even inside one transaction, violates the partial unique index. The index is the backstop, never the mechanism.
- **The timeline actor is resolved before the transaction and only when a write will happen** (`s.actorFor(ctx, generatedFallbackActor)`, `actor.go`). No directory call ever runs inside a transaction or under a lock.
- **Contact-association writes touch no `revision`** — `customers.customers_contacts` and `customers.customer_contact_roles` are off the customer row, exactly as addresses and tags are. No write here takes a `revision`, accepts one, or bumps one.
- Both catalogs (`en` and `nb`) of `apps/customers/frontend/src/i18n.ts` get every new string; `mise exec -- bun run translations:check` and `mise exec -- bun run i18n:test` must pass.
- **Never assert "the last fetch"** in a frontend test — debounced pickers run on their own clock and CI is slow. Filter by method and URL instead. Two different call lists exist and they are not interchangeable: a **per-route `vi.fn` spy** handed to `stubFetch`'s handler map has `spy.mock.calls`, where element `[0]` is the `RequestInit` (the handler's only argument); the value **`stubFetch` itself returns** (`src/test/fetch.ts`) is not a `vi.fn` wrapper and has no `.mock` — it carries plain `calls` (everything, session bootstrap included) and `actualCalls` (everything but the bootstrap) arrays of `[input, init]` pairs. Use `spy.mock.calls[0][0]` for a spied route and `returned.actualCalls.find(([url, init]) => …)` for anything else.
- A Mantine `Select`/`MultiSelect` is queried as `getByRole("combobox", { name })`, never as a textbox; a plain `TextInput` is a textbox; a `Checkbox` is `getByRole("checkbox", { name })`; a `Switch` renders `<input type="checkbox" role="switch">` in Mantine 9.5, so it is `getByRole("switch", { name })`. Mantine popovers/modals/selects need `<MantineProvider>` (the existing route tests already wrap in one).
- No new generated timeline **event type** is introduced, so `-customer-timeline.tsx`'s `typeKey` needs no entry: `customer.contact_attached`, `customer.contact_relationship_updated`, `customer.contact_detached` and `customer.contact_removed` are all already in it.
- After any `openapi/*.yaml`, `queries/*.sql` or migration change: `cd apps/server && mise exec -- go generate ./...` (a second run must show no new diff), then from the repo root `mise exec -- bun run gen:client` for a yaml change. Commit every generated file (`apps/server/internal/openapi/specs/customers.yaml`, `internal/customers/gen/api.gen.go`, `internal/customers/store/*.go`, each changed `api-schema.d.ts`). `openapi/COVERAGE.md` does **not** move — this delivery adds no operation — and `KnownServeMuxConflicts` is untouched for the same reason.
- After the migration: add it to `apps/server/internal/customers/sqlc.yaml`'s `schema:` list (`TestSqlcSchemaListsOnlyTheModulesOwnMigrations` requires the list to be exactly this module's migrations) and to `internal/db/schema_test.go`'s `wantTables`, then `go generate`.
- **Read the generated file before writing Go against it.** After `go generate`, open `apps/server/internal/customers/store/contact_roles.sql.go`, the changed parts of `store/contacts.sql.go` and `store/models.go`, and check which queries took a bare argument and which a `…Params` struct, the exact integer widths, and the spelling of every field (`IsPrimary`, `ContactID`, `ExcludeContactID`). Where sqlc disagrees with this plan's Go, **follow sqlc** and adjust the call, not the query.
- After any exported Go signature change: `mise exec -- go vet ./...` and grep every caller including tests and fakes. `contracts.CustomerDirectory` is **not** touched by this delivery (the spec parks `PrimaryContact` until Invoices asks), so no fake changes.
- Match the surrounding code: comment density and voice (these files explain *why*), naming, error wording, test style.
- Every new test must be shown able to fail (remove the guard, see red, restore). Say so in the report.
- **Every Go listing in this plan is written for readability, not to gofmt's alignment.** Run `mise exec -- gofmt -l <dir>` before each commit that touches Go, `gofmt -w` whatever it names, and commit the formatted version — `golangci-lint` fails on unformatted code. The same applies to the TypeScript listings and `bunx biome check .`.
- **One implementer commits at a time.** If two agents share the tree, the second writes and verifies but does not commit.

---

## File Structure

| File | Responsibility |
| --- | --- |
| `apps/server/internal/db/migrations/00025_customers_contact_roles.sql` | the `role` → `title` rename, the role table, its partial unique index |
| `apps/server/internal/customers/queries/contact_roles.sql` | every read and write of `customer_contact_roles`, including the demote/promote statements |
| `apps/server/internal/customers/queries/contacts.sql` | `role` → `title` in every association statement; a deterministic order for the cascade's association list |
| `apps/server/internal/customers/contact_roles.go` | the role vocabulary, `applyRoles` (the primary invariant), the promotion bookkeeping and the response projection |
| `apps/server/internal/customers/contacts.go` | the four association handlers and the contact delete, each under the customer lock, with `title`/`roles` on the wire |
| `apps/server/internal/customers/values.go` | `validateContactRole` kept as the `role` alias's validator, `validateContactTitle`, `validateAssociationRole` |
| `apps/server/internal/customers/contacts_timeline.go` | `title`/`roles` in every contact event's payload, the situational summary, the promotion event |
| `openapi/customers.yaml` | two new schemas, four additive properties on four existing schemas, `role` relaxed in the two request schemas |
| `apps/customers/frontend/src/api/contacts.ts` | `ContactRoleAssignment`, the normaliser, `title`/`roles` on the input and both response shapes |
| `apps/customers/frontend/src/lib/contact-role-label.ts` | the label for one role code, with a raw-code fallback |
| `apps/customers/frontend/src/components/contact-role-badges.tsx` | the badges, the star on the primary one and its tooltip |
| `apps/customers/frontend/src/pages/-connection.tsx` | the shared Title input + Roles group, and the edit modal that sends both |
| `apps/customers/frontend/src/pages/-customer-contacts-card.tsx` | the customer's contacts table: title under the name, badges, the attach modal's Title + Roles |
| `apps/customers/frontend/src/pages/contacts.$contactId.tsx` | the contact's customers table: the same two columns and the same attach modal |
| `docs/customers.md`, `ROADMAP.md` | the contacts section, the primary rule's prose, the event payloads, phase 4 delivery B |

---

### Task 1: The rename, the role table and every Go reader of the old column (D1, D2)

**Files:**
- Create: `apps/server/internal/db/migrations/00025_customers_contact_roles.sql`, `apps/server/internal/customers/queries/contact_roles.sql`
- Modify: `apps/server/internal/customers/sqlc.yaml` (the `schema:` list), `apps/server/internal/db/schema_test.go` (`wantTables` plus three assertions in `TestCustomersBaseline_AppliesAndIsIdempotent`), `apps/server/internal/customers/queries/contacts.sql`, `apps/server/internal/customers/contacts.go`, `apps/server/internal/customers/harness_test.go` (the `associate` fixture)
- Deliberately **not** modified: `apps/server/internal/customers/contacts_timeline.go` — its `role string` parameter is still the title and the payload's `role` key is still what the corpus recorded, so nothing in it changes until Task 3
- Generated: `apps/server/internal/customers/store/contact_roles.sql.go`, `store/contacts.sql.go`, `store/models.go`
- Read first (do not change): `apps/server/internal/db/migrations/00024_customers_owner_tags.sql` (the migration voice), `apps/server/internal/customers/queries/addresses.sql:1-19` (`LockCustomer`), `apps/server/internal/customers/queries/addresses.sql:52-90` (the three statements the primary invariant is built from)

**Interfaces:**
- Produces (SQL, and therefore the generated Go the later tasks call):
  - `customers.customers_contacts.title varchar(255) NULL` (was `role varchar(255) NOT NULL`)
  - `customers.customer_contact_roles(customer_id integer, contact_id integer, role varchar(30), is_primary boolean NOT NULL, created_at timestamptz NOT NULL, PRIMARY KEY (customer_id, contact_id, role), FOREIGN KEY (customer_id, contact_id) REFERENCES customers_contacts ON DELETE CASCADE)`, partial unique index `ux_customer_contact_roles_primary` on `(customer_id, role) WHERE is_primary`, index `ix_customer_contact_roles_contact` on `(contact_id)`
  - `ContactRolesForAssociation :many`, `ContactRolesForCustomer :many`, `ContactRolesForContact :many`, `PrimaryContactRoleHolder :one`, `CountContactRoleHolders :one`, `OldestContactRoleHolder :one`, `SetContactRolePrimary :exec`, `InsertContactRole :exec`, `DeleteContactRolesNotIn :exec`
  - `Title *string` in place of `Role string` on `store.CustomersCustomersContact`, `InsertAssociationParams`, `UpdateAssociationParams`, `GetAssociationWithContactRow`, `ListContactAssociationsForCustomerRow`, `ListCustomerAssociationsForContactRow`, `ListAssociationsForContactRow`
  - `ListAssociationsForContact` ordered by `customer_id`
- Produces Go (unchanged signatures, adapted call sites): `recordContactEvent`/`recordContact{Attached,RelationshipUpdated,Detached,Removed}` keep `role string`; the handlers pass `deref(row.Title)`.

- [ ] **Step 1: Write the failing schema test**

This task's only Go-level guard is `internal/db`'s schema test, and it is written first. In `apps/server/internal/db/schema_test.go`, inside `TestCustomersBaseline_AppliesAndIsIdempotent`, extend `wantTables` to the full alphabetical list (the query orders by `table_name`, so `customer_contact_roles` lands between `customer_addresses` and `customer_peppol_lookups`):

```go
	wantTables := []string{
		"contacts",
		"counters",
		"customer_addresses",
		"customer_contact_roles",
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

Then, immediately after the existing `customer_tags` primary-key assertion in the same function, add:

```go
	if cols := primaryKeyColumns(t, ctx, pool, "customers", "customer_contact_roles"); !equalStrings(cols, []string{"customer_id", "contact_id", "role"}) {
		t.Errorf("customer_contact_roles primary key columns = %v, want [customer_id contact_id role]", cols)
	}

	// One primary contact per customer and role (typed contact roles design
	// D2): the same partial unique index the addresses' invariant leans on
	// (ux_customer_addresses_primary), one table over. It is PARTIAL, so its
	// predicate is the load-bearing half — an index on (customer_id, role)
	// without the WHERE would forbid a second holder of a role altogether,
	// which is the opposite of what the design says — and indexColumns above
	// cannot see a predicate, so this reads the definition instead.
	var rolePrimaryIndexDef string
	if err := pool.QueryRow(ctx, `SELECT indexdef FROM pg_indexes
	                              WHERE schemaname = 'customers' AND indexname = 'ux_customer_contact_roles_primary'`).Scan(&rolePrimaryIndexDef); err != nil {
		t.Fatalf("read ux_customer_contact_roles_primary definition: %v", err)
	}
	if !strings.Contains(rolePrimaryIndexDef, "UNIQUE") || !strings.Contains(rolePrimaryIndexDef, "WHERE is_primary") {
		t.Errorf("ux_customer_contact_roles_primary = %q, want a UNIQUE index on (customer_id, role) WHERE is_primary", rolePrimaryIndexDef)
	}

	// The association's free text is a TITLE and is nullable (design D1): an
	// association that says who someone is through its roles alone needs no
	// title at all, and the old NOT NULL would have forced the empty string
	// to stand in for "none given".
	var titleNullable, roleColumns string
	if err := pool.QueryRow(ctx, `SELECT coalesce(max(is_nullable), 'MISSING'),
	                                     coalesce(count(*) FILTER (WHERE column_name = 'role'), 0)::text
	                              FROM information_schema.columns
	                              WHERE table_schema = 'customers' AND table_name = 'customers_contacts'
	                                AND column_name IN ('title', 'role')`).Scan(&titleNullable, &roleColumns); err != nil {
		t.Fatalf("read customers_contacts.title: %v", err)
	}
	if titleNullable != "YES" || roleColumns != "0" {
		t.Errorf("customers_contacts: title is_nullable = %q and %s column(s) named role, want \"YES\" and 0", titleNullable, roleColumns)
	}
```

- [ ] **Step 2: Run it and watch it fail**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- go test -count=1 -run TestCustomersBaseline_AppliesAndIsIdempotent ./internal/db/
```
Expected: FAIL — `tables = [... customer_addresses customer_peppol_lookups ...], want [... customer_addresses customer_contact_roles ...]`, and `read ux_customer_contact_roles_primary definition: no rows in result set`.

- [ ] **Step 3: Write the migration**

Create `apps/server/internal/db/migrations/00025_customers_contact_roles.sql`:

```sql
-- +goose Up
-- The free text was always a title, and the roles that answer "who gets the
-- invoice" are their own thing (typed contact roles design D1, D2) — phase 4's
-- second delivery.
--
-- The rename, not a new column beside the old one: every value already in this
-- column is a job title (the recorded corpus has CEO and CTO and nothing that
-- reads as a role), so there is nothing to migrate and nothing to interpret —
-- the data is correct under its new name. A new column would have meant
-- writing both for a release, a backfill, and a day where two columns disagree.
--
-- DROP NOT NULL, because a title is now genuinely optional: an association
-- whose contact is the billing contact and nothing else says what it needs to
-- say through customer_contact_roles below, and the alternative — the empty
-- string standing in for "none given" — is the thing this codebase spells NULL
-- everywhere else (00017's contact-info columns, 00019's billing profile).
-- The contract keeps answering `role` (the title, or "" when there is none),
-- so the frozen corpus stays true; see openapi/customers.yaml.
ALTER TABLE customers.customers_contacts RENAME COLUMN role TO title;
ALTER TABLE customers.customers_contacts ALTER COLUMN title DROP NOT NULL;

-- The typed roles. varchar(30) because the vocabulary is code-defined
-- (billing, project, decision_maker) and validated in Go, not by a CHECK: a
-- CHECK would turn the design's "a wider list is a value change for later"
-- into a migration, exactly as 00024 declined a CHECK on a tag's colour for
-- the same reason. Thirty characters leaves room for the executive-sponsor
-- kind of word the design parks without leaving room for prose.
--
-- The primary key is (customer_id, contact_id, role): a contact holds a role
-- for a customer at most once, and the pair leading the key is also the
-- "which roles does this association carry" lookup every read here makes.
--
-- The foreign key is the COMPOSITE one, to customers_contacts' own primary
-- key, rather than two separate keys to customers and contacts. That is what
-- makes a role row impossible without the association it describes, and it is
-- what makes detaching a contact — or deleting it, whose cascade removes the
-- association — take its roles with it in one statement. The handlers still
-- run the promotion the design requires before/after that cascade; the
-- cascade only guarantees no orphan survives it.
CREATE TABLE customers.customer_contact_roles (
    customer_id integer     NOT NULL,
    contact_id  integer     NOT NULL,
    role        varchar(30) NOT NULL,
    is_primary  boolean     NOT NULL,
    created_at  timestamptz NOT NULL,
    PRIMARY KEY (customer_id, contact_id, role),
    FOREIGN KEY (customer_id, contact_id)
        REFERENCES customers.customers_contacts (customer_id, contact_id) ON DELETE CASCADE
);

-- One primary per customer and role (design D2) — the invariant the handlers
-- keep under the customer row's lock, and the database's own last word should
-- they ever not. This is 00018's ux_customer_addresses_primary with (type)
-- swapped for (role) and deliberately nothing else changed: the same shape,
-- so the same reasoning about demote-before-promote applies unaltered.
CREATE UNIQUE INDEX ux_customer_contact_roles_primary
    ON customers.customer_contact_roles (customer_id, role) WHERE is_primary;

-- The primary key serves every customer-side read; this serves the other
-- direction the contact page asks in — "which roles does this contact hold,
-- at each of its customers" — which the key's leading customer_id cannot.
CREATE INDEX ix_customer_contact_roles_contact ON customers.customer_contact_roles (contact_id);

-- +goose Down
DROP TABLE customers.customer_contact_roles;
-- A title written as NULL under the new rule cannot go back under the old
-- NOT NULL, so rolling back spells it the way the old column had to: the
-- empty string. Nothing is lost that the old schema could have held.
UPDATE customers.customers_contacts SET title = '' WHERE title IS NULL;
ALTER TABLE customers.customers_contacts ALTER COLUMN title SET NOT NULL;
ALTER TABLE customers.customers_contacts RENAME COLUMN title TO role;
```

- [ ] **Step 4: Register the migration with sqlc**

In `apps/server/internal/customers/sqlc.yaml`, append to the `schema:` list, after `00024_customers_owner_tags.sql`:

```yaml
      - ../db/migrations/00025_customers_contact_roles.sql
```

- [ ] **Step 5: Run the schema tests and watch them pass**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- go test -count=1 -run 'TestCustomersBaseline_AppliesAndIsIdempotent|TestSqlcSchemaListsOnlyTheModulesOwnMigrations|TestNoModuleReferencesAnotherModulesSchema' ./internal/db/
```
Expected: PASS. (`applyUpDownUp(t, url, 3)` runs every migration up, down to 2 and up again, so the `Down` section above is exercised here too — a `Down` that cannot re-apply fails this test, which is the point of writing the `UPDATE … SET title = ''` line.)

- [ ] **Step 6: Write the role queries**

Create `apps/server/internal/customers/queries/contact_roles.sql`:

```sql
-- name: ContactRolesForAssociation :many
-- ContactRolesForAssociation is the typed roles of ONE association (typed
-- contact roles design D2, D3): what the attach/update/detach handlers read
-- under the customer row's lock before deciding anything, and what a single
-- association's response answers. The ordering is the design's fixed one,
-- billing/project/decision_maker, so the array a client receives never
-- shuffles between reads; an unknown code (there is none today — the
-- vocabulary is validated in Go — but a widened list is a value change, not a
-- migration) sorts last by name rather than vanishing.
SELECT role, is_primary, created_at
FROM customers.customer_contact_roles
WHERE customer_id = @customer_id AND contact_id = @contact_id
ORDER BY
    CASE role WHEN 'billing' THEN 0 WHEN 'project' THEN 1 WHEN 'decision_maker' THEN 2 ELSE 3 END,
    role;

-- name: ContactRolesForCustomer :many
-- ContactRolesForCustomer is every role row of one customer in ONE query
-- (design D3), for GET /customers/{id}/contacts: a per-row read would be one
-- round trip per contact for data that is on the wire either way, the same
-- reasoning CustomerTagsForCustomers states for a page of customers. Ordered
-- by contact so the caller can walk it beside the association list, then by
-- the design's fixed role order.
SELECT contact_id, role, is_primary
FROM customers.customer_contact_roles
WHERE customer_id = @customer_id
ORDER BY
    contact_id,
    CASE role WHEN 'billing' THEN 0 WHEN 'project' THEN 1 WHEN 'decision_maker' THEN 2 ELSE 3 END,
    role;

-- name: ContactRolesForContact :many
-- ContactRolesForContact is the mirror of the query above for
-- GET /customers/contacts/{id}/customers, and it is also DELETE
-- /customers/contacts/{id}'s own read: the roles a contact about to be
-- deleted holds at each of its customers, which is what decides where a
-- promotion has to follow the cascade. ix_customer_contact_roles_contact
-- (migration 00025) is what makes it a lookup rather than a scan.
SELECT customer_id, role, is_primary
FROM customers.customer_contact_roles
WHERE contact_id = @contact_id
ORDER BY
    customer_id,
    CASE role WHEN 'billing' THEN 0 WHEN 'project' THEN 1 WHEN 'decision_maker' THEN 2 ELSE 3 END,
    role;

-- name: PrimaryContactRoleHolder :one
-- PrimaryContactRoleHolder finds the contact to demote when a write makes a
-- different contact primary for the same role (design D2) — the twin of
-- addresses' PrimaryCustomerAddressOfType. At most one row can ever match:
-- that is ux_customer_contact_roles_primary's own invariant. pgx.ErrNoRows
-- means the role currently has no holder at all, which contact_roles.go reads
-- as "nothing to demote", not as an error.
SELECT contact_id
FROM customers.customer_contact_roles
WHERE customer_id = @customer_id AND role = @role AND is_primary
LIMIT 1;

-- name: CountContactRoleHolders :one
-- CountContactRoleHolders is the "is this the FIRST holder of the role" check
-- (design D2: the first holder is its primary whatever the request says) —
-- addresses' CountCustomerAddressesOfType, one table over. exclude_contact_id
-- is the contact doing the writing, always: on an attach it holds nothing yet
-- so excluding it changes no count, and on an update it must not count itself
-- as the incumbent it is about to join.
SELECT count(*)
FROM customers.customer_contact_roles
WHERE customer_id = @customer_id AND role = @role AND contact_id <> @exclude_contact_id;

-- name: OldestContactRoleHolder :one
-- OldestContactRoleHolder is the contact a lost primary promotes (design D2:
-- the LONGEST-STANDING remaining holder) — addresses'
-- OldestCustomerAddressOfType, with created_at then contact_id as the
-- tie-break so two roles given in the same transaction still promote
-- deterministically. exclude_contact_id is the contact that just gave the role
-- up or was detached. pgx.ErrNoRows means nobody else holds it: the role is
-- simply unheld now, so there is nothing to promote and the "always a primary
-- while anyone holds the role" invariant is vacuous.
SELECT contact_id
FROM customers.customer_contact_roles
WHERE customer_id = @customer_id AND role = @role AND contact_id <> @exclude_contact_id
ORDER BY created_at, contact_id
LIMIT 1;

-- name: SetContactRolePrimary :exec
-- SetContactRolePrimary flips one role row's is_primary flag alone: the
-- demote-before-promote step every write elsewhere in the same transaction
-- needs before a different contact can safely become primary for the role,
-- without transiently violating ux_customer_contact_roles_primary. Scoped to
-- customer_id and contact_id both, like every other statement in this file:
-- every triple this receives came from a row the same transaction just read
-- under that customer's lock, so the extra predicates change no row this code
-- path touches — they exist so a call-site mistake matches zero rows instead
-- of quietly flipping some other association's flag. created_at is never
-- moved: it is what "longest-standing" means, so a demotion or a promotion
-- must not reset a contact's seniority in the role.
UPDATE customers.customer_contact_roles
SET is_primary = @is_primary
WHERE customer_id = @customer_id AND contact_id = @contact_id AND role = @role;

-- name: InsertContactRole :exec
-- InsertContactRole gives one association one role. is_primary is whatever
-- contact_roles.go already resolved (first-holder forced true, or the
-- request's own value once the role has a holder) — this statement never
-- decides it itself, the same division InsertCustomerAddress keeps. created_at
-- comes from the caller's Deps.Clock(), the convention every insert in this
-- module follows.
INSERT INTO customers.customer_contact_roles (customer_id, contact_id, role, is_primary, created_at)
VALUES (@customer_id, @contact_id, @role, @is_primary, @now::timestamptz);

-- name: DeleteContactRolesNotIn :exec
-- DeleteContactRolesNotIn is the removal half of an update's complete-set
-- replace (design D3: the request names the whole set to hold). It is a
-- subtraction rather than a delete-everything-and-re-insert, and that is
-- load-bearing: re-inserting a role the association already held would reset
-- its created_at, and created_at is what decides who gets promoted when a
-- primary steps down. So the rows that survive keep both their seniority and
-- their flag, and only the roles the request dropped leave.
--
-- `role <> ALL(@keep::text[])` with an empty array is TRUE for every row, so
-- an empty keep list clears the association's roles — which is exactly what
-- `roles: []` means.
DELETE FROM customers.customer_contact_roles
WHERE customer_id = @customer_id AND contact_id = @contact_id AND role <> ALL(@keep::text[]);
```

- [ ] **Step 7: Rename the column in the association statements**

In `apps/server/internal/customers/queries/contacts.sql`, replace every `cc.role` / `role` reference to the association column with `title`, and give the cascade's list a deterministic order. The five edits, in file order:

`ListContactAssociationsForCustomer`'s select list (line ~133):
```sql
       cc.title, cc.phone AS association_phone, cc.email AS association_email
```

`ListCustomerAssociationsForContact`'s select list (line ~142):
```sql
SELECT cu.id, cu.customer_number, cu.name, cc.title, cc.phone, cc.email
```

`InsertAssociation` (lines ~159-160):
```sql
INSERT INTO customers.customers_contacts (customer_id, contact_id, title, phone, email)
VALUES (@customer_id, @contact_id, @title, @phone, @email);
```

`GetAssociationWithContact`'s select list (line ~167):
```sql
SELECT cc.title, cc.phone AS association_phone, cc.email AS association_email,
```

`UpdateAssociation` (line ~180):
```sql
SET title = @title, phone = @phone, email = @email
```

`ListAssociationsForContact` (lines ~187-194) — replace the whole statement, comment included, because both the column and the ordering change:
```sql
-- name: ListAssociationsForContact :many
-- ListAssociationsForContact is DeleteContactEndpoint's cascade source
-- (:29-32): every customer this contact is attached to, for the "removed"
-- timeline event DeleteContact records against each one before the contact
-- row (and its associations, via ON DELETE CASCADE) are deleted.
--
-- Ordered by customer_id, and that is not cosmetic: DeleteContact now takes
-- each of those customers' row locks so it can promote a new primary for
-- every role this contact was primary for (typed contact roles design D2),
-- and it takes them in this list's order. Locks acquired in a deterministic
-- order across all callers is what keeps two concurrent deletes of two
-- contacts that share two customers from deadlocking with each other.
SELECT customer_id, title, phone, email
FROM customers.customers_contacts
WHERE contact_id = @contact_id
ORDER BY customer_id;
```

- [ ] **Step 8: Generate, then read what was generated**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- go generate ./... && mise exec -- go generate ./... && git status --short
```
**First, check that sqlc tracked the rename at all.** No migration in this repo has used `RENAME COLUMN` or `ALTER COLUMN … DROP NOT NULL` before (`grep -rn 'RENAME COLUMN\|DROP NOT NULL' apps/server/internal/db/migrations/` finds nothing), so sqlc's own DDL interpreter is being asked to do something this codebase has never asked it to do. Prove it did:

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
grep -n 'Title\|Role' internal/customers/store/models.go | sed -n '/CustomersCustomersContact/,$p'
grep -n 'type CustomersCustomersContact' -A 8 internal/customers/store/models.go
```
Expected: `CustomersCustomersContact` has `Title *string` and **no** `Role` field. Two failure modes to look for, and what each means:
- `Role string` still there, or `Title string` (not a pointer): sqlc applied the `RENAME` but not the `DROP NOT NULL`, or neither.
- `go generate` errors with a column-not-found on `cc.title`: sqlc did not apply the `RENAME` at all.

**If either happens, express the rename as three statements instead** and regenerate — the observable schema is identical, and this is a pure preservation of data:

```sql
ALTER TABLE customers.customers_contacts ADD COLUMN title varchar(255);
UPDATE customers.customers_contacts SET title = role;
ALTER TABLE customers.customers_contacts DROP COLUMN role;
```
with the `Down` section becoming the mirror (`ADD COLUMN role varchar(255)`, `UPDATE … SET role = coalesce(title, '')`, `ALTER COLUMN role SET NOT NULL`, `DROP COLUMN title`). Note in the report which form was used and why. Task 1 Step 1's schema test passes under either form — it asserts the columns the schema ends with, not how it got there — and that is on purpose.

Then **read** `internal/customers/store/contact_roles.sql.go` and the changed parts of `internal/customers/store/contacts.sql.go` and `store/models.go`, and write down for yourself: which of the nine new queries took a bare argument and which a `…Params` struct (`ContactRolesForCustomer` and `ContactRolesForContact` take one bare `int32`; the rest take a struct), the exact field spellings (`ExcludeContactID`, `IsPrimary`, `Keep []string`, `Now time.Time`), and that `CountContactRoleHolders` returns `int64`. Later tasks are written against those names; where they differ, **follow the generated file**.

- [ ] **Step 9: Adapt every Go reader of the renamed column**

Nothing about behaviour changes in this step — `role` on the wire is still the title, and every existing test must stay green. Six edits in `contacts.go`:

1. `GetCustomersContactsByIdCustomers` (line ~421): `Role: r.Role,` → `Role: deref(r.Title),`
2. `GetCustomersByIdContacts` (line ~448): `Role: r.Role,` → `Role: deref(r.Title),`
3. `PostCustomersByIdContacts`'s insert (line ~519): `Role: assoc.Role,` → `Title: &assoc.Role,`
4. `PutCustomersByIdContactsByContactId`'s change check (line ~581): `existing.Role != assoc.Role` → `deref(existing.Title) != assoc.Role`
5. `PutCustomersByIdContactsByContactId`'s update (line ~600): `Role: assoc.Role,` → `Title: &assoc.Role,`
6. `DeleteCustomersByIdContactsByContactId`'s event (line ~647): `existing.Role,` → `deref(existing.Title),`

and one in `DeleteCustomersContactsById` (line ~388): `a.Role,` → `deref(a.Title),`.

`contacts_timeline.go` is untouched: its `role string` parameter is still the title, and the payload's `role` key is still what the corpus recorded.

In `harness_test.go`, the `associate` fixture writes the column by name:

```go
	h.Exec(t, `INSERT INTO customers.customers_contacts (customer_id, contact_id, title, email) VALUES ($1, $2, 'Primary', $3)`,
		customerID, contactID, email)
```

- [ ] **Step 10: Prove the whole package is still green**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- go vet ./... && mise exec -- go test -count=1 ./internal/customers/... ./internal/db/... ./internal/openapi/... ./internal/module/...
```
Expected: PASS, including `TestAttachContact_RecordsTimelineEvent`'s full-payload `reflect.DeepEqual` (the payload still has exactly its nine keys) and the corpus check in `internal/openapi` (the contract has not moved yet).

- [ ] **Step 11: Commit**

```bash
cd /home/anders/projects/vantigo/vantigo
printf '%s\n\n%s\n\n%s\n' 'feat(customers): the association'"'"'s free text is a title, and roles get a table of their own' 'The column already held titles (CEO, CTO), so 00025 renames it and lets it be NULL; customer_contact_roles arrives with the addresses'"'"' partial unique index on one primary per customer and role. Nothing on the wire moves yet: role still answers the title.' 'Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>' > /tmp/msg-roles-task1
git add apps/server/internal/db/migrations/00025_customers_contact_roles.sql apps/server/internal/db/schema_test.go apps/server/internal/customers/sqlc.yaml apps/server/internal/customers/queries/contact_roles.sql apps/server/internal/customers/queries/contacts.sql apps/server/internal/customers/contacts.go apps/server/internal/customers/harness_test.go apps/server/internal/customers/store
git commit -F /tmp/msg-roles-task1 -- $(git diff --cached --name-only)
git show --stat HEAD && git status --short
```

- [ ] **Step 12: Show the new test can fail**

Comment out the `CREATE UNIQUE INDEX ux_customer_contact_roles_primary` line in the migration, re-run Step 5's command, see the `read ux_customer_contact_roles_primary definition: no rows in result set` failure, restore it, re-run, see green. Note it in the report.

---

### Task 2: The contract — `title` and `roles` on four schemas, `role` relaxed in two (D1, D3)

**Files:**
- Modify: `openapi/customers.yaml`, `apps/server/internal/customers/contacts.go` (two call sites, so the package still compiles — this task's Step 5)
- Generated: `apps/server/internal/openapi/specs/customers.yaml`, `apps/server/internal/customers/gen/api.gen.go`, `apps/customers/frontend/src/api-schema.d.ts` (and any other package's `api-schema.d.ts` that changes)
- Read first (do not change): `openapi/customers.yaml:3-18` (`AttachCustomerContactRequest`), `:340-368` (`CustomerContactRequest`, `CustomerContactResponse`), `:668-682` (`GetContactCustomersContactCustomerResponse`), `:573-591` (`CustomerTagSummary` — the voice a new schema's `description` is written in)

**Interfaces:**
- Produces (contract): schemas `CustomerContactRole {role, primary}` (both required) and `CustomerContactRoleRequest {role}` required, `primary` optional; properties `title` (nullable string) and `roles` (array) on `AttachCustomerContactRequest`, `CustomerContactRequest`, `CustomerContactResponse` and `GetContactCustomersContactCustomerResponse`; `role` removed from the two request schemas' `required:` lists and kept in the two response schemas'.
- Produces Go (oapi-codegen, verified against how `SafeCustomerResponse.Tags` generates today): `gen.CustomerContactRole{Role string; Primary bool}`, `gen.CustomerContactRoleRequest{Primary *bool; Role string}`, and on all four association types `Title *string \`json:"title,omitempty"\`` plus `Roles *[]CustomerContactRole \`json:"roles,omitempty"\`` (request side: `*[]CustomerContactRoleRequest`). A `nil` `Roles` pointer is "the property was absent"; a pointer to an empty non-nil slice marshals as `[]`, which is how the server always answers the property while the yaml keeps it optional.

- [ ] **Step 1: Add the two new schemas**

The schema block is sorted by key, and `CustomerContactResponse` sorts **before** `CustomerContactRole` (`Res` < `Rol`), so insert `CustomerContactRole` and `CustomerContactRoleRequest` **after** the `CustomerContactResponse` block ends and before the next key (`CustomerOwner`). Use the same indentation as their siblings — eight spaces for the schema name, twelve for `properties`. Generation rewrites `apps/server/internal/openapi/specs/customers.yaml` in sorted order anyway, so if the placement here is off, Step 4's second `go generate` shows it as a diff; putting it in the right place by hand keeps the source file and the generated one reading the same.

```yaml
        CustomerContactRole:
            description: One typed role a contact holds for a customer (typed contact roles design D2). role is 'billing' (who gets the invoice and the reminder), 'project' (who is spoken to day to day) or 'decision_maker' (who approves). primary marks the one contact that holds the role for this customer — there is always exactly one while anyone holds the role at all.
            properties:
                primary:
                    type: boolean
                role:
                    type: string
            required:
                - role
                - primary
            type: object
        CustomerContactRoleRequest:
            description: "One typed role to give a contact for a customer (typed contact roles design D2, D3). primary is three-valued, and the three values mean different things: omitted on a role the contact ALREADY holds leaves its primary flag exactly as it is, so a request that replaces the role set without meaning to move anybody does not have to echo every flag back; omitted on a role the contact does NOT yet hold follows the first-holder rule (primary if nobody holds the role, otherwise not). true demotes whoever holds the role now. An explicit false on the contact that is the only or the primary holder is refused with a field error on roles — there is always a primary while anyone holds the role."
            properties:
                primary:
                    nullable: true
                    type: boolean
                role:
                    type: string
            required:
                - role
            type: object
```

- [ ] **Step 2: Relax `role` and add the two properties in the two request schemas**

`AttachCustomerContactRequest` (the file's first schema) becomes — note `role` keeps its property and loses only its place in `required`:

```yaml
        AttachCustomerContactRequest:
            properties:
                contactId:
                    format: int32
                    type: integer
                email:
                    nullable: true
                    type: string
                phone:
                    nullable: true
                    type: string
                role:
                    deprecated: true
                    description: Deprecated alias of title, kept because the recorded exchange corpus sends it. title wins when both are given.
                    nullable: true
                    type: string
                roles:
                    description: The typed roles to give the contact (typed contact roles design D3). Omitted means none.
                    items:
                        $ref: '#/components/schemas/CustomerContactRoleRequest'
                    type: array
                title:
                    description: What this person is called at this customer — a job title, free text (typed contact roles design D1). At most 255 characters, trimmed. A request with neither a title nor at least one role is refused with a field error on title.
                    nullable: true
                    type: string
            required:
                - contactId
            type: object
```

`CustomerContactRequest` becomes the same minus `contactId`, and its `roles` says what a *complete set* means on an update:

```yaml
        CustomerContactRequest:
            properties:
                email:
                    nullable: true
                    type: string
                phone:
                    nullable: true
                    type: string
                role:
                    deprecated: true
                    description: Deprecated alias of title, kept because the recorded exchange corpus sends it. title wins when both are given.
                    nullable: true
                    type: string
                roles:
                    description: The complete set of typed roles the contact is to hold for this customer (typed contact roles design D3). Omitted leaves the roles unchanged; an empty array clears them. A role listed without a primary keeps the primary flag it already has, so replacing the set is not an accidental demotion.
                    items:
                        $ref: '#/components/schemas/CustomerContactRoleRequest'
                    type: array
                title:
                    description: What this person is called at this customer — a job title, free text (typed contact roles design D1). At most 255 characters, trimmed. A request that would leave the association with neither a title nor a role is refused with a field error on title.
                    nullable: true
                    type: string
            type: object
```

(`CustomerContactRequest` had `role` as its only required property, so its `required:` list goes away entirely rather than becoming empty — an empty `required: []` is not what the rest of this file writes; compare `CustomerContactInfo`, which has no `required:` key at all.)

- [ ] **Step 3: Add the two properties to the two response schemas, keeping `role` required**

`CustomerContactResponse`:

```yaml
        CustomerContactResponse:
            properties:
                contact:
                    $ref: '#/components/schemas/ContactResponse'
                email:
                    nullable: true
                    type: string
                phone:
                    nullable: true
                    type: string
                role:
                    description: The association's title, or "" when it has none. Kept required and kept under this name because the recorded exchange corpus predates title; new clients read title.
                    type: string
                roles:
                    description: Every typed role this contact holds for this customer, in the fixed order billing, project, decision_maker (typed contact roles design D3). Always present on responses from this version on — an empty array when the contact holds none — and optional here only because the recorded exchange corpus predates it.
                    items:
                        $ref: '#/components/schemas/CustomerContactRole'
                    type: array
                title:
                    description: What this person is called at this customer (typed contact roles design D1). Absent when the association has no title; role answers "" in that case.
                    nullable: true
                    type: string
            required:
                - contact
                - role
            type: object
```

`GetContactCustomersContactCustomerResponse` — the contact → customers list shape (the spec calls it `ContactCustomerResponse`; the contract's own name is this one) — gains exactly the same three edits:

```yaml
        GetContactCustomersContactCustomerResponse:
            properties:
                customer:
                    $ref: '#/components/schemas/GetContactCustomersCustomerReference'
                email:
                    nullable: true
                    type: string
                phone:
                    nullable: true
                    type: string
                role:
                    description: The association's title, or "" when it has none. Kept required and kept under this name because the recorded exchange corpus predates title; new clients read title.
                    type: string
                roles:
                    description: Every typed role this contact holds for this customer, in the fixed order billing, project, decision_maker (typed contact roles design D3). Always present on responses from this version on — an empty array when the contact holds none — and optional here only because the recorded exchange corpus predates it.
                    items:
                        $ref: '#/components/schemas/CustomerContactRole'
                    type: array
                title:
                    description: What this person is called at this customer (typed contact roles design D1). Absent when the association has no title; role answers "" in that case.
                    nullable: true
                    type: string
            required:
                - customer
                - role
            type: object
```

No operation, no `x-vantigo-access` rule and no path changes: the roles ride on `GET/POST /customers/{id}/contacts`, `PUT/DELETE /customers/{id}/contacts/{contactId}` and `GET /customers/contacts/{id}/customers` exactly as they are, with the permissions they already carry (`customers:associations-view+customers:contacts-view` to read, `customers:associations-manage+customers:contacts-view` to write, and `customers:associations-manage` alone on the DELETE).

- [ ] **Step 4: Generate both sides and read what came out**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- go generate ./... && mise exec -- go generate ./... && git status --short
cd /home/anders/projects/vantigo/vantigo && mise exec -- bun run gen:client && git status --short
```
Then read `apps/server/internal/customers/gen/api.gen.go`'s `AttachCustomerContactRequest`, `CustomerContactRequest`, `CustomerContactResponse`, `GetContactCustomersContactCustomerResponse`, `CustomerContactRole` and `CustomerContactRoleRequest`, and confirm the shapes named in **Interfaces** above. If oapi-codegen spells the array field differently (a `*[]T` vs a `[]T`), **follow it** — Task 3's Go is written against `*[]T` because that is how `SafeCustomerResponse.Tags` generates from an identical declaration, and a mismatch here means adjusting Task 3's call sites, not the yaml.

- [ ] **Step 5: Adapt the two call sites the relaxed `required:` just broke**

Dropping `role` from the two request schemas' `required:` lists changes `Role string` to `Role *string` on `gen.AttachCustomerContactRequest` and `gen.CustomerContactRequest`, so `contacts.go` no longer compiles: `validateCustomerContactRequest(body.Role, …)` at line ~478 (`PostCustomersByIdContacts`) and line ~576 (`PutCustomersByIdContactsByContactId`) now pass a pointer where a string is wanted. This task's commit has to be green on its own, so both are adapted here, minimally — Task 3 replaces these handlers wholesale and this shim disappears with them:

```go
	assoc, errs := validateCustomerContactRequest(deref(body.Role), body.Phone, body.Email)
```

at both call sites. `deref` is `server.go:132` and turns a nil `*string` into `""`, which `validateContactRole` already refuses with the message the corpus recorded — so a body that omits `role` entirely behaves exactly as `{"role": ""}` did until Task 3 introduces `title` and the title-or-role rule. Nothing else in the file needs touching: `title` and `roles` are new fields nothing reads yet, and the responses' new fields are pointers that stay nil, so the server keeps answering exactly the four keys the corpus recorded.

Confirm with the generated file which of the two fields really became a pointer before editing — if oapi-codegen kept `Role string` for either schema (it should not, but the generated file is the ground truth), leave that call site alone.

- [ ] **Step 6: Prove the package compiles and the corpus still validates**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- gofmt -l ./internal/customers && mise exec -- go vet ./... && mise exec -- go test -count=1 ./internal/openapi/... && mise exec -- go test -count=1 ./internal/customers/...
```
Expected: `gofmt -l` prints nothing, then PASS. Two things this specifically proves:
- `TestRecordedExchangesMatchTheContract` still validates the corpus line `POST /api/v1/customers/1086/contacts` with body `{"contactId":1027,"role":"","email":"not-an-email"}` and status 400. Dropping `role` from `required` cannot break it (an empty string is still a valid `string`), and the recorded 400 is checked against the generic `HttpValidationProblemDetails`, so the message inside it is not pinned by the contract — only by this module's own tests.
- Every GET list line in the corpus, whose items carry `role` and neither `title` nor `roles`, still validates, because neither new property joined a `required:` list.

- [ ] **Step 7: Commit**

```bash
cd /home/anders/projects/vantigo/vantigo
printf '%s\n\n%s\n\n%s\n' 'feat(customers): the contact-association contract learns title and typed roles' 'role stays accepted in both request bodies (now an optional alias of title) and stays required in both response bodies, so every recorded exchange still validates; title and roles are additive and optional even though the server always answers roles.' 'Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>' > /tmp/msg-roles-task2
git add openapi/customers.yaml apps/server/internal/openapi/specs/customers.yaml apps/server/internal/customers/gen/api.gen.go apps/server/internal/customers/contacts.go apps/customers/frontend/src/api-schema.d.ts
git commit -F /tmp/msg-roles-task2 -- $(git diff --cached --name-only)
git show --stat HEAD && git status --short
```
(If `gen:client` changed another package's `api-schema.d.ts` — `apps/host/frontend/src/api-schema.d.ts` is the likely one — add it to both the `git add` and the pathspec.)

---

### Task 3: The server — validation, the primary invariant, the promotions and the timeline (D1, D2, D3, D4)

**Files:**
- Create: `apps/server/internal/customers/contact_roles.go`, `apps/server/internal/customers/contact_roles_test.go`, `apps/server/internal/customers/contact_roles_concurrency_test.go`
- Modify: `apps/server/internal/customers/values.go`, `values_test.go`, `contacts.go`, `contacts_timeline.go`, `contacts_test.go`
- Read first (do not change): `apps/server/internal/customers/addresses.go:104-167` (`errPrimaryTransitionRefused`, `primaryTransitionProblem`, `demoteCurrentPrimary`, `promoteOldestOfType` — the four things this task copies), `tags.go`'s `PutCustomersByIdTags` (the `db.RetrySerializable` + `LockCustomer` + no-op-before-actor shape), `addresses_concurrency_test.go` (`gateCustomerLock`), `contacts_concurrency_test.go` (`race`, `awaitLockWaiters`)

**Interfaces:**
- Consumes: Task 1's nine queries and the `Title *string` fields; Task 2's `gen.CustomerContactRole`, `gen.CustomerContactRoleRequest`, and `Title`/`Roles` on the four association types.
- Produces Go:
  - `const contactRoleBilling = "billing"`, `contactRoleProject = "project"`, `contactRoleDecisionMaker = "decision_maker"`; `var contactRoleOrder = []string{contactRoleBilling, contactRoleProject, contactRoleDecisionMaker}`
  - `func validateAssociationRole(raw string) (string, string)` — the typed role
  - `func validateContactTitle(raw string) (string, string)` and `func validateContactRole(raw string) (string, string)` — the same rule under the two field names
  - `type contactRole struct { Role string \`json:"role"\`; Primary bool \`json:"primary"\` }`
  - `type requestedRole struct { Role string; Primary bool }`
  - `type validatedAssociation struct { Title *string; Roles []requestedRole; RolesGiven bool; Phone, Email *string }`
  - `func validateCustomerContactRequest(title, role *string, roles *[]gen.CustomerContactRoleRequest, phone, email *string, rolesWhenOmitted int) (validatedAssociation, map[string][]string)`
  - `type rolePromotion struct { ContactID int32; Role string }`
  - `func applyRoles(ctx context.Context, txq *store.Queries, customerID, contactID int32, existing []contactRole, want []requestedRole, now time.Time) ([]contactRole, []rolePromotion, error)`
  - `func releaseRoles(ctx context.Context, txq *store.Queries, customerID, contactID int32, held []contactRole) ([]rolePromotion, error)`
  - `func genContactRoles(roles []contactRole) *[]gen.CustomerContactRole`
  - `func recordContactPromoted(...)` and a `role`-aware `recordContactEvent` (see Step 8)

- [ ] **Step 1: Write the failing validation unit tests**

Append to `apps/server/internal/customers/values_test.go` (package `customers`, so the unexported functions are reachable directly — that is how every sibling test in this file works):

```go
// Not a port: the typed role vocabulary is this delivery's own (typed contact
// roles design D2), and the message is the design's verbatim — a caller who
// mistypes a role has to be told which three words are allowed.
func TestValidateAssociationRole_AcceptsTheThreeRolesAndTrims(t *testing.T) {
	for raw, want := range map[string]string{
		"billing":          "billing",
		"  project  ":      "project",
		"decision_maker":   "decision_maker",
	} {
		got, err := validateAssociationRole(raw)
		if err != "" || got != want {
			t.Errorf("validateAssociationRole(%q) = %q, %q, want %q, no error", raw, got, err, want)
		}
	}
}

func TestValidateAssociationRole_RejectsAnythingElse(t *testing.T) {
	for _, raw := range []string{"", "   ", "Billing", "BILLING", "decision-maker", "technical", "CEO"} {
		want := fmt.Sprintf("A contact role must be one of 'billing', 'project' or 'decision_maker', but was '%s'", strings.TrimSpace(raw))
		if _, err := validateAssociationRole(raw); err != want {
			t.Errorf("validateAssociationRole(%q) = error %q, want %q", raw, err, want)
		}
	}
}

// The title's rule is the role's rule that has always been there (design D1:
// "Validation of the title is today's"), so the two share one implementation
// and differ only in the noun their messages name — the field the caller
// actually sent. TestValidateContactRole_* above pins the 'role' half and is
// deliberately left untouched: the deprecated alias must keep answering
// exactly what the recorded corpus recorded.
func TestValidateContactTitle_MirrorsTheRoleRuleUnderItsOwnNoun(t *testing.T) {
	if got, err := validateContactTitle("  CEO  "); err != "" || got != "CEO" {
		t.Errorf("validateContactTitle(%q) = %q, %q, want \"CEO\", no error", "  CEO  ", got, err)
	}
	if _, err := validateContactTitle("  "); err != "A title cannot be null or empty" {
		t.Errorf("validateContactTitle(blank) = error %q, want %q", err, "A title cannot be null or empty")
	}
	want := "A title cannot be longer than 255 characters, the given value was 256 characters"
	if _, err := validateContactTitle(strings.Repeat("a", 256)); err != want {
		t.Errorf("validateContactTitle(256 chars) = error %q, want %q", err, want)
	}
}
```

- [ ] **Step 2: Run them and watch them fail**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- go test -count=1 -run 'TestValidateAssociationRole|TestValidateContactTitle' ./internal/customers/
```
Expected: FAIL to build — `undefined: validateAssociationRole`, `undefined: validateContactTitle`.

- [ ] **Step 3: Write the three value rules**

`validateAssociationRole` below reads `contactRoleOrder`, which Step 7 declares in `contact_roles.go`, so create that file now with nothing in it but the `package customers` line, the three role constants, `contactRoleOrder` and `contactRoleRank` — copy those four declarations verbatim from Step 7's listing, including their comments — and Step 7 then adds the rest of the file around them. Without this the package does not compile and Step 4 cannot run.

Then, in `apps/server/internal/customers/values.go`, replace the existing `validateContactRole` block (lines ~320-331) with:

```go
// associationTitleRule is the free text a customer–contact association
// carries, under whichever field name the caller used (typed contact roles
// design D1): non-blank when given, at most 255 UTF-16 code units, trimmed but
// case-preserved. It is one function and not two because the rule is one rule —
// only the noun the message names differs, and it names the field the caller
// actually sent, so a client is told about the property it wrote rather than
// about the column behind it.
func associationTitleRule(noun, raw string) (string, string) {
	if strings.TrimSpace(raw) == "" {
		return "", fmt.Sprintf("A %s cannot be null or empty", noun)
	}
	if n := utf16Length(raw); n > 255 {
		return "", fmt.Sprintf("A %s cannot be longer than 255 characters, the given value was %d characters", noun, n)
	}
	return strings.TrimSpace(raw), ""
}

// validateContactRole is ContactRole's Validate and constructor
// (DM/Contacts/Common/ContactRole.cs), now the DEPRECATED `role` field's own
// validator (typed contact roles design D1): the field is an alias of `title`,
// and it keeps answering exactly the messages the recorded exchange corpus
// recorded against it — a caller who has not migrated must not be told
// something new about a field they sent unchanged.
func validateContactRole(raw string) (string, string) {
	return associationTitleRule("role", raw)
}

// validateContactTitle is the same rule under the field's real name (design
// D1). Blank-when-given is still an error rather than "absent": a client that
// sends "title": "" is saying something, and saying it wrongly, which is the
// distinction every other optional field in this module draws by simply
// omitting the key.
func validateContactTitle(raw string) (string, string) {
	return associationTitleRule("title", raw)
}

// validateAssociationRole is the typed role vocabulary (design D2). Three
// values, defined in code and nowhere else: not a yaml enum (a contract enum
// would answer a 400 the module cannot word) and not a database CHECK (which
// would turn the design's "a wider list is a value change for later" into a
// migration, the same reasoning 00024 gives for a tag's colour). The message
// is the design's own, and the comparison is case-sensitive: 'Billing' is a
// mistake, not a variant, because the value is an identifier that travels into
// URLs and payloads rather than a name anyone types.
func validateAssociationRole(raw string) (string, string) {
	trimmed := strings.TrimSpace(raw)
	for _, role := range contactRoleOrder {
		if trimmed == role {
			return trimmed, ""
		}
	}
	return "", fmt.Sprintf("A contact role must be one of 'billing', 'project' or 'decision_maker', but was '%s'", trimmed)
}
```

- [ ] **Step 4: Run them and watch them pass**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- go test -count=1 -run 'TestValidateAssociationRole|TestValidateContactTitle|TestValidateContactRole' ./internal/customers/
```
Expected: PASS, the three pre-existing `TestValidateContactRole_*` included and unmodified.

- [ ] **Step 5: Write the HTTP-level tests, and watch them fail**

Create `apps/server/internal/customers/contact_roles_test.go` **before any of the implementation in Steps 7-9 exists**. It compiles against nothing new — every case drives the four association endpoints over HTTP and reads `customers.customer_contact_roles`, which Task 1 created — so it builds and fails on its assertions rather than on missing symbols, which is exactly the red this plan wants: the server currently accepts a `roles` array and ignores it, so every case fails on an absent or wrong role set.

Write the whole file:

```go
package customers_test

import (
	"fmt"
	"net/http"
	"reflect"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is typed contact roles' own tests (typed contact roles design
// D1-D4): the title-or-role rule, the vocabulary, and the one-primary-per-role
// invariant that is addresses_test.go's invariant one table over. The
// concurrency half — a forced race of two primary:true writers — is
// contact_roles_concurrency_test.go, beside the other lock-gate files.
//
// The wire shapes are declared here rather than reusing contacts_test.go's
// customerContactJSON, because that type is deliberately the SHAPE THE CORPUS
// RECORDED (contact, role, phone, email) and one test below asserts that a
// corpus-shaped request still answers exactly it. Widening it would erase the
// distinction this delivery is built on.

type contactRoleJSON struct {
	Role    string `json:"role"`
	Primary bool   `json:"primary"`
}

type roledContactJSON struct {
	Contact contactJSON       `json:"contact"`
	Role    string            `json:"role"`
	Title   *string           `json:"title"`
	Roles   []contactRoleJSON `json:"roles"`
	Phone   *string           `json:"phone"`
	Email   *string           `json:"email"`
}

type roledContactListJSON struct {
	Data []roledContactJSON `json:"data"`
}

type roledCustomerJSON struct {
	Customer contactCustomerReferenceJSON `json:"customer"`
	Role     string                       `json:"role"`
	Title    *string                      `json:"title"`
	Roles    []contactRoleJSON            `json:"roles"`
}

type roledCustomerListJSON struct {
	Data []roledCustomerJSON `json:"data"`
}

// attachWithRoles posts an attach body and returns the answered association,
// failing the test on anything but 200.
func attachWithRoles(t *testing.T, c *modtest.Client, customerID int32, body map[string]any) roledContactJSON {
	t.Helper()
	r := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/customers/%d/contacts", customerID), body)
	if r.Status != http.StatusOK {
		t.Fatalf("attach %v: status %d body %s, want 200", body, r.Status, r.Body)
	}
	var out roledContactJSON
	r.JSON(&out)
	return out
}

// putAssociation is the update, answering the response whatever the status, so
// a test can assert a refusal's body as easily as a success's.
func putAssociation(t *testing.T, c *modtest.Client, customerID, contactID int32, body map[string]any) *modtest.Response {
	t.Helper()
	return c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/contacts/%d", customerID, contactID), body)
}

// listCustomerContacts is GET /customers/{id}/contacts, in the role-aware
// shape.
func listCustomerContacts(t *testing.T, c *modtest.Client, customerID int32) roledContactListJSON {
	t.Helper()
	r := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/contacts", customerID), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("list contacts: status %d body %s, want 200", r.Status, r.Body)
	}
	var out roledContactListJSON
	r.JSON(&out)
	return out
}

// rolesOf is one association's roles as the database holds them, for the
// assertions the contract cannot make (which row is primary, and since when).
func rolesOf(t *testing.T, h *modtest.Harness, customerID, contactID int32) []contactRoleJSON {
	t.Helper()
	rows, err := h.Pool().Query(t.Context(), `
		SELECT role, is_primary FROM customers.customer_contact_roles
		WHERE customer_id = $1 AND contact_id = $2
		ORDER BY CASE role WHEN 'billing' THEN 0 WHEN 'project' THEN 1 WHEN 'decision_maker' THEN 2 ELSE 3 END, role`,
		customerID, contactID)
	if err != nil {
		t.Fatalf("rolesOf(%d, %d): %v", customerID, contactID, err)
	}
	defer rows.Close()
	var out []contactRoleJSON
	for rows.Next() {
		var r contactRoleJSON
		if err := rows.Scan(&r.Role, &r.Primary); err != nil {
			t.Fatalf("rolesOf: scan: %v", err)
		}
		out = append(out, r)
	}
	return out
}

// primaryHolderOf is the contact that holds role for customerID, or 0 when
// nobody does.
func primaryHolderOf(t *testing.T, h *modtest.Harness, customerID int32, role string) int32 {
	t.Helper()
	return int32(h.Count(t, `SELECT coalesce(max(contact_id), 0) FROM customers.customer_contact_roles
	                         WHERE customer_id = $1 AND role = $2 AND is_primary`, customerID, role))
}

// primaryCountOf is how many contacts hold role as primary — the invariant's
// own number, which must never be anything but 0 or 1.
func primaryCountOf(t *testing.T, h *modtest.Harness, customerID int32, role string) int {
	t.Helper()
	return h.Count(t, `SELECT count(*) FROM customers.customer_contact_roles
	                   WHERE customer_id = $1 AND role = $2 AND is_primary`, customerID, role)
}

func TestAttachContact_FirstHolderOfARoleIsPrimaryWhateverItAsked(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "First Holder Co")
	contact := createContact(t, c, map[string]any{"firstName": "First", "lastName": "Holdersen"})

	got := attachWithRoles(t, c, customer.Id, map[string]any{
		"contactId": contact.Id, "title": "CEO",
		"roles": []any{map[string]any{"role": "billing", "primary": false}},
	})

	want := []contactRoleJSON{{Role: "billing", Primary: true}}
	if !reflect.DeepEqual(got.Roles, want) {
		t.Errorf("roles = %+v, want %+v (the first holder is primary whatever the request says)", got.Roles, want)
	}
	if !reflect.DeepEqual(rolesOf(t, h, customer.Id, contact.Id), want) {
		t.Errorf("stored roles = %+v, want %+v", rolesOf(t, h, customer.Id, contact.Id), want)
	}
}

func TestAttachContact_PrimaryTrueDemotesTheCurrentHolder(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Demote On Attach Co")
	first := createContact(t, c, map[string]any{"firstName": "Incumbent", "lastName": "Personsen"})
	second := createContact(t, c, map[string]any{"firstName": "Usurper", "lastName": "Personsen"})

	attachWithRoles(t, c, customer.Id, map[string]any{"contactId": first.Id, "title": "CFO",
		"roles": []any{map[string]any{"role": "billing"}}})
	got := attachWithRoles(t, c, customer.Id, map[string]any{"contactId": second.Id, "title": "Controller",
		"roles": []any{map[string]any{"role": "billing", "primary": true}}})

	if !reflect.DeepEqual(got.Roles, []contactRoleJSON{{Role: "billing", Primary: true}}) {
		t.Errorf("the new contact's roles = %+v, want billing primary", got.Roles)
	}
	if !reflect.DeepEqual(rolesOf(t, h, customer.Id, first.Id), []contactRoleJSON{{Role: "billing", Primary: false}}) {
		t.Errorf("the incumbent's roles = %+v, want billing not primary (it must have been demoted)", rolesOf(t, h, customer.Id, first.Id))
	}
	if n := primaryCountOf(t, h, customer.Id, "billing"); n != 1 {
		t.Errorf("primary billing holders = %d, want exactly 1", n)
	}
}

func TestUpdateCustomerContact_ClearingTheOnlyHoldersPrimaryIsRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Refused Clear Co")
	contact := createContact(t, c, map[string]any{"firstName": "Sole", "lastName": "Holdersen"})
	attachWithRoles(t, c, customer.Id, map[string]any{"contactId": contact.Id, "title": "CEO",
		"roles": []any{map[string]any{"role": "billing"}}})

	r := putAssociation(t, c, customer.Id, contact.Id, map[string]any{"title": "CEO",
		"roles": []any{map[string]any{"role": "billing", "primary": false}}})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	want := "A contact that is the only or primary holder of the 'billing' role stays primary; make another contact primary instead"
	if msgs := problem.Errors["roles"]; len(msgs) != 1 || msgs[0] != want {
		t.Errorf("errors[roles] = %v, want [%q]", msgs, want)
	}
	if !reflect.DeepEqual(rolesOf(t, h, customer.Id, contact.Id), []contactRoleJSON{{Role: "billing", Primary: true}}) {
		t.Errorf("stored roles = %+v, want billing still primary (a refusal writes nothing)", rolesOf(t, h, customer.Id, contact.Id))
	}
}

// TestUpdateCustomerContact_OmittedPrimaryKeepsTheFlagInAReplace is the
// omitted-flag half of design D2's three-valued primary: a replace that adds a
// role and says nothing about the flag of one the contact already holds as
// primary must keep that flag, not demote it and not be refused. This is the
// rule that makes "the complete set of roles" a writable field at all — a
// client rebuilding the set from a checkbox group has no business having to
// echo every primary flag back to avoid demoting somebody.
func TestUpdateCustomerContact_OmittedPrimaryKeepsTheFlagInAReplace(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Omitted Flag Co")
	contact := createContact(t, c, map[string]any{"firstName": "Omitted", "lastName": "Flagsen"})
	other := createContact(t, c, map[string]any{"firstName": "Other", "lastName": "Flagsen"})
	// contact takes project first, so it is project's primary; other holds
	// project too, so contact is not its ONLY holder — which is the case a
	// "the only holder stays primary" reading would let through and this one
	// must not.
	attachWithRoles(t, c, customer.Id, map[string]any{"contactId": contact.Id, "title": "CEO",
		"roles": []any{map[string]any{"role": "project"}}})
	attachWithRoles(t, c, customer.Id, map[string]any{"contactId": other.Id, "title": "CTO",
		"roles": []any{map[string]any{"role": "project"}}})

	r := putAssociation(t, c, customer.Id, contact.Id, map[string]any{"title": "CEO",
		"roles": []any{map[string]any{"role": "project"}, map[string]any{"role": "billing"}}})
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200 (an omitted primary asks for nothing and cannot be refused)", r.Status, r.Body)
	}
	var updated roledContactJSON
	r.JSON(&updated)
	want := []contactRoleJSON{{Role: "billing", Primary: true}, {Role: "project", Primary: true}}
	if !reflect.DeepEqual(updated.Roles, want) {
		t.Errorf("roles = %+v, want %+v (project's flag kept, billing primary as its first holder)", updated.Roles, want)
	}
	if !reflect.DeepEqual(rolesOf(t, h, customer.Id, contact.Id), want) {
		t.Errorf("stored roles = %+v, want %+v", rolesOf(t, h, customer.Id, contact.Id), want)
	}
	if got := primaryHolderOf(t, h, customer.Id, "project"); got != contact.Id {
		t.Errorf("primary project holder = %d, want %d (unmoved)", got, contact.Id)
	}
}

// TestUpdateCustomerContact_ExplicitPrimaryFalseInAReplaceIsRefused is the other
// half: saying `primary: false` OUT LOUD about a role this contact is the
// primary holder of is the refusal (design D2), whether or not anything else in
// the request changed. The two tests together are what pins the pointer: drop it
// and make the flag a plain bool, and exactly one of them must break.
func TestUpdateCustomerContact_ExplicitPrimaryFalseInAReplaceIsRefused(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Explicit False Co")
	contact := createContact(t, c, map[string]any{"firstName": "Explicit", "lastName": "Falsesen"})
	other := createContact(t, c, map[string]any{"firstName": "Other", "lastName": "Falsesen"})
	attachWithRoles(t, c, customer.Id, map[string]any{"contactId": contact.Id, "title": "CEO",
		"roles": []any{map[string]any{"role": "project"}}})
	attachWithRoles(t, c, customer.Id, map[string]any{"contactId": other.Id, "title": "CTO",
		"roles": []any{map[string]any{"role": "project"}}})

	r := putAssociation(t, c, customer.Id, contact.Id, map[string]any{"title": "CEO",
		"roles": []any{map[string]any{"role": "project", "primary": false}, map[string]any{"role": "billing"}}})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	want := "A contact that is the only or primary holder of the 'project' role stays primary; make another contact primary instead"
	if msgs := problem.Errors["roles"]; len(msgs) != 1 || msgs[0] != want {
		t.Errorf("errors[roles] = %v, want [%q]", msgs, want)
	}
	// A refusal writes nothing at all — billing was never added.
	if got := rolesOf(t, h, customer.Id, contact.Id); !reflect.DeepEqual(got, []contactRoleJSON{{Role: "project", Primary: true}}) {
		t.Errorf("stored roles = %+v, want project primary and nothing else", got)
	}
}

func TestUpdateCustomerContact_LosingAPrimaryRolePromotesTheLongestStandingHolder(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Promote Longest Co")
	leaving := createContact(t, c, map[string]any{"firstName": "Leaving", "lastName": "Personsen"})
	lowerID := createContact(t, c, map[string]any{"firstName": "LowerId", "lastName": "Personsen"})
	higherID := createContact(t, c, map[string]any{"firstName": "HigherId", "lastName": "Personsen"})

	// created_at and contact_id are made to DISAGREE, which is the whole point:
	// contacts get ascending ids in creation order, so attaching higherID
	// before lowerID — with h.Advance moving the clock in between — makes
	// higherID the longest-standing of the two while lowerID has the smaller
	// id. An implementation that promoted by id alone would pick lowerID, and
	// only this disagreement can tell the two rules apart.
	attachWithRoles(t, c, customer.Id, map[string]any{"contactId": leaving.Id, "title": "A",
		"roles": []any{map[string]any{"role": "billing"}}})
	h.Advance(time.Hour)
	attachWithRoles(t, c, customer.Id, map[string]any{"contactId": higherID.Id, "title": "B",
		"roles": []any{map[string]any{"role": "billing"}}})
	h.Advance(time.Hour)
	attachWithRoles(t, c, customer.Id, map[string]any{"contactId": lowerID.Id, "title": "C",
		"roles": []any{map[string]any{"role": "billing"}}})

	// leaving — billing's primary, as its first holder — drops the role.
	if r := putAssociation(t, c, customer.Id, leaving.Id, map[string]any{"title": "A", "roles": []any{}}); r.Status != http.StatusOK {
		t.Fatalf("drop billing: status %d body %s, want 200", r.Status, r.Body)
	}

	if got := primaryHolderOf(t, h, customer.Id, "billing"); got != higherID.Id {
		t.Errorf("primary billing holder = %d, want %d (the longest-standing remaining holder, by created_at — not %d, which merely has the smaller id)", got, higherID.Id, lowerID.Id)
	}
	if n := primaryCountOf(t, h, customer.Id, "billing"); n != 1 {
		t.Errorf("primary billing holders = %d, want exactly 1", n)
	}
}

func TestUpdateCustomerContact_OmittedRolesAreUnchangedAndEmptyClearsThem(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Omitted Roles Co")
	contact := createContact(t, c, map[string]any{"firstName": "Omitted", "lastName": "Rolesen"})
	attachWithRoles(t, c, customer.Id, map[string]any{"contactId": contact.Id, "title": "CEO",
		"roles": []any{map[string]any{"role": "billing"}, map[string]any{"role": "project"}}})

	// No `roles` key at all: the set is left alone (design D3).
	r := putAssociation(t, c, customer.Id, contact.Id, map[string]any{"title": "CTO"})
	if r.Status != http.StatusOK {
		t.Fatalf("omit roles: status %d body %s, want 200", r.Status, r.Body)
	}
	var updated roledContactJSON
	r.JSON(&updated)
	want := []contactRoleJSON{{Role: "billing", Primary: true}, {Role: "project", Primary: true}}
	if !reflect.DeepEqual(updated.Roles, want) {
		t.Errorf("roles after omitting them = %+v, want %+v", updated.Roles, want)
	}
	if updated.Role != "CTO" || updated.Title == nil || *updated.Title != "CTO" {
		t.Errorf("role = %q, title = %v, want both \"CTO\"", updated.Role, updated.Title)
	}

	// An empty array clears them — and is allowed only because a title remains.
	if r := putAssociation(t, c, customer.Id, contact.Id, map[string]any{"title": "CTO", "roles": []any{}}); r.Status != http.StatusOK {
		t.Fatalf("clear roles: status %d body %s, want 200", r.Status, r.Body)
	}
	if got := rolesOf(t, h, customer.Id, contact.Id); len(got) != 0 {
		t.Errorf("stored roles after []= %+v, want none", got)
	}
}

func TestAssociationRequests_RefuseAnUnknownRoleADuplicateAndAnEmptyRelationship(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Refusals Co")
	contact := createContact(t, c, map[string]any{"firstName": "Refused", "lastName": "Bodysen"})

	cases := []struct {
		name  string
		body  map[string]any
		field string
		want  string
	}{
		{
			name:  "an unknown role",
			body:  map[string]any{"contactId": contact.Id, "title": "CEO", "roles": []any{map[string]any{"role": "technical"}}},
			field: "roles",
			want:  "A contact role must be one of 'billing', 'project' or 'decision_maker', but was 'technical'",
		},
		{
			name:  "a role given twice",
			body:  map[string]any{"contactId": contact.Id, "title": "CEO", "roles": []any{map[string]any{"role": "billing"}, map[string]any{"role": "billing", "primary": true}}},
			field: "roles",
			want:  "A contact role can only be given once, but 'billing' was given more than once",
		},
		{
			name:  "neither a title nor a role",
			body:  map[string]any{"contactId": contact.Id, "roles": []any{}},
			field: "title",
			want:  "A contact needs a title or at least one role",
		},
		{
			name:  "no title, no role and no roles key either",
			body:  map[string]any{"contactId": contact.Id},
			field: "title",
			want:  "A contact needs a title or at least one role",
		},
	}
	for _, tc := range cases {
		r := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/customers/%d/contacts", customer.Id), tc.body)
		if r.Status != http.StatusBadRequest {
			t.Errorf("%s: status %d body %s, want 400", tc.name, r.Status, r.Body)
			continue
		}
		var problem validationProblemJSON
		r.JSON(&problem)
		if msgs := problem.Errors[tc.field]; len(msgs) != 1 || msgs[0] != tc.want {
			t.Errorf("%s: errors[%s] = %v, want [%q]", tc.name, tc.field, msgs, tc.want)
		}
	}

	// Nothing was attached by any of them.
	if n := h.Count(t, `SELECT count(*) FROM customers.customers_contacts WHERE customer_id = $1`, customer.Id); n != 0 {
		t.Errorf("associations = %d, want 0 (every case above is a refusal)", n)
	}
}

func TestAssociationRequests_TheCorpusShapeStillWorksAndTitleWins(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Corpus Shape Co")
	corpusContact := createContact(t, c, map[string]any{"firstName": "Corpus", "lastName": "Shapesen"})
	bothContact := createContact(t, c, map[string]any{"firstName": "Both", "lastName": "Fieldsen"})

	// Literally the body the frozen corpus records (openapi/testdata/exchanges/
	// customers.jsonl): contactId and role, nothing else.
	got := attachWithRoles(t, c, customer.Id, map[string]any{"contactId": corpusContact.Id, "role": "CEO"})
	if got.Role != "CEO" || got.Title == nil || *got.Title != "CEO" {
		t.Errorf("role = %q, title = %v, want both \"CEO\" (role is an alias of title)", got.Role, got.Title)
	}
	if len(got.Roles) != 0 {
		t.Errorf("roles = %+v, want an empty array: a corpus-shaped request gives no typed roles", got.Roles)
	}

	// Both fields: title wins (design D1).
	both := attachWithRoles(t, c, customer.Id, map[string]any{"contactId": bothContact.Id, "role": "ignored", "title": "CTO"})
	if both.Role != "CTO" || both.Title == nil || *both.Title != "CTO" {
		t.Errorf("role = %q, title = %v, want both \"CTO\"", both.Role, both.Title)
	}

	// And on the list, in the shape the corpus recorded: the same four keys,
	// with the two new ones beside them.
	list := listCustomerContacts(t, c, customer.Id)
	if len(list.Data) != 2 {
		t.Fatalf("len(data) = %d, want 2", len(list.Data))
	}
	for _, item := range list.Data {
		if item.Roles == nil {
			t.Errorf("contact %d: roles is null, want an empty array (the server always answers it)", item.Contact.Id)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customer_contact_roles WHERE customer_id = $1`, customer.Id); n != 0 {
		t.Errorf("role rows = %d, want 0: a corpus-shaped request creates none", n)
	}
}

func TestGetContactCustomers_CarriesTheTitleAndTheRolesPerCustomer(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	contact := createContact(t, c, map[string]any{"firstName": "Manysided", "lastName": "Personsen"})
	alpha := createCustomer(t, c, "Alpha Roles AS")
	beta := createCustomer(t, c, "Beta Roles AS")
	attachWithRoles(t, c, alpha.Id, map[string]any{"contactId": contact.Id, "title": "CEO",
		"roles": []any{map[string]any{"role": "decision_maker"}, map[string]any{"role": "billing"}}})
	attachWithRoles(t, c, beta.Id, map[string]any{"contactId": contact.Id, "roles": []any{map[string]any{"role": "project"}}})

	r := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/contacts/%d/customers", contact.Id), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", r.Status, r.Body)
	}
	var list roledCustomerListJSON
	r.JSON(&list)
	if len(list.Data) != 2 {
		t.Fatalf("len(data) = %d, want 2", len(list.Data))
	}
	// Sorted by customer name: Alpha first.
	if got := list.Data[0].Roles; !reflect.DeepEqual(got, []contactRoleJSON{{Role: "billing", Primary: true}, {Role: "decision_maker", Primary: true}}) {
		t.Errorf("Alpha's roles = %+v, want billing then decision_maker, both primary (the fixed order, not the request's)", got)
	}
	if list.Data[0].Role != "CEO" {
		t.Errorf("Alpha's role = %q, want \"CEO\"", list.Data[0].Role)
	}
	if list.Data[1].Role != "" || list.Data[1].Title != nil {
		t.Errorf("Beta's role = %q and title = %v, want \"\" and null (an association with roles and no title)", list.Data[1].Role, list.Data[1].Title)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customer_contact_roles WHERE contact_id = $1`, contact.Id); n != 3 {
		t.Errorf("role rows for the contact = %d, want 3 (two at Alpha, one at Beta)", n)
	}
}

func TestDetachContact_PromotesAndRecordsThePromotionWithTheActingUser(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, userID := authenticatedClientWithID(t, h)
	setDisplayName(t, h, userID, "Promoting Person")
	customer := createCustomer(t, c, "Detach Promotes Co")
	leaving := createContact(t, c, map[string]any{"firstName": "Leaving", "lastName": "Detachsen"})
	staying := createContact(t, c, map[string]any{"firstName": "Staying", "lastName": "Detachsen"})
	attachWithRoles(t, c, customer.Id, map[string]any{"contactId": leaving.Id, "title": "CEO",
		"roles": []any{map[string]any{"role": "billing"}}})
	attachWithRoles(t, c, customer.Id, map[string]any{"contactId": staying.Id, "title": "CFO",
		"roles": []any{map[string]any{"role": "billing"}}})

	r := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d/contacts/%d", customer.Id, leaving.Id), nil)
	if r.Status != http.StatusNoContent {
		t.Fatalf("detach: status %d body %s, want 204", r.Status, r.Body)
	}

	if got := primaryHolderOf(t, h, customer.Id, "billing"); got != staying.Id {
		t.Errorf("primary billing holder = %d, want %d", got, staying.Id)
	}
	event := fetchTimelineEvent(t, h, customer.Id, "customer.contact_relationship_updated")
	wantSummary := fmt.Sprintf("Now the primary billing contact: Staying Detachsen (#%d)", staying.Id)
	if event.Summary != wantSummary {
		t.Errorf("summary = %q, want %q", event.Summary, wantSummary)
	}
	actorDisplay := modtest.One[string](t, h, `SELECT actor_display FROM customers.customers_timeline_entries
	                                           WHERE customer_id = $1 AND event_type = 'customer.contact_relationship_updated'
	                                           ORDER BY id DESC LIMIT 1`, customer.Id)
	if actorDisplay != userDisplayName(t, h, userID) {
		t.Errorf("actor_display = %q, want the caller who caused the promotion (%q)", actorDisplay, userDisplayName(t, h, userID))
	}
}

func TestDeleteContact_PromotesInEveryCustomerItWasPrimaryFor(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	doomed := createContact(t, c, map[string]any{"firstName": "Doomed", "lastName": "Cascadesen"})
	survivor := createContact(t, c, map[string]any{"firstName": "Survivor", "lastName": "Cascadesen"})
	alpha := createCustomer(t, c, "Alpha Cascade AS")
	beta := createCustomer(t, c, "Beta Cascade AS")
	for _, customer := range []int32{alpha.Id, beta.Id} {
		attachWithRoles(t, c, customer, map[string]any{"contactId": doomed.Id, "title": "CEO",
			"roles": []any{map[string]any{"role": "billing"}}})
		attachWithRoles(t, c, customer, map[string]any{"contactId": survivor.Id, "title": "CFO",
			"roles": []any{map[string]any{"role": "billing"}}})
	}

	if r := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/contacts/%d", doomed.Id), nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete contact: status %d body %s, want 204", r.Status, r.Body)
	}

	for _, customer := range []int32{alpha.Id, beta.Id} {
		if got := primaryHolderOf(t, h, customer, "billing"); got != survivor.Id {
			t.Errorf("customer %d: primary billing holder = %d, want %d", customer, got, survivor.Id)
		}
		if n := primaryCountOf(t, h, customer, "billing"); n != 1 {
			t.Errorf("customer %d: primary billing holders = %d, want exactly 1", customer, n)
		}
		if n := countTimelineEvents(t, h, customer, "customer.contact_removed"); n != 1 {
			t.Errorf("customer %d: contact_removed events = %d, want 1", customer, n)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customer_contact_roles WHERE contact_id = $1`, doomed.Id); n != 0 {
		t.Errorf("role rows left for the deleted contact = %d, want 0 (the cascade)", n)
	}
}

// TestUpdateCustomerContact_RecordsAnEventOnlyWhenSomethingMovedAndSaysWhat is
// also where the omitted-flag rule earns its keep. Two of its steps send a
// `roles` replace that lists a role the contact holds AS primary and says
// nothing about the flag — `[{"role":"project"}, {"role":"billing"}]` and then
// `[{"role":"project"}]`. Under a rule that read an omitted flag as false, both
// would be 400s refusing to demote the primary project contact, for requests
// that never asked to; because an omitted flag means "leave this one alone"
// (design D2, requestedRole), both are the plain role-set changes they look
// like. That is the case, not an incidental detail of the fixture.
func TestUpdateCustomerContact_RecordsAnEventOnlyWhenSomethingMovedAndSaysWhat(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Summary Co")
	contact := createContact(t, c, map[string]any{"firstName": "Summary", "lastName": "Personsen"})
	attachWithRoles(t, c, customer.Id, map[string]any{"contactId": contact.Id, "title": "CEO",
		"roles": []any{map[string]any{"role": "project"}}})

	// A resubmit of exactly what is stored: nothing at all.
	if r := putAssociation(t, c, customer.Id, contact.Id, map[string]any{"title": "CEO",
		"roles": []any{map[string]any{"role": "project", "primary": true}}}); r.Status != http.StatusOK {
		t.Fatalf("resubmit: status %d body %s, want 200", r.Status, r.Body)
	}
	if n := countTimelineEvents(t, h, customer.Id, "customer.contact_relationship_updated"); n != 0 {
		t.Fatalf("events after a resubmit = %d, want 0", n)
	}

	// Adding a role the contact becomes primary for: the summary says so.
	if r := putAssociation(t, c, customer.Id, contact.Id, map[string]any{"title": "CEO",
		"roles": []any{map[string]any{"role": "project"}, map[string]any{"role": "billing"}}}); r.Status != http.StatusOK {
		t.Fatalf("add billing: status %d body %s, want 200", r.Status, r.Body)
	}
	event := fetchTimelineEvent(t, h, customer.Id, "customer.contact_relationship_updated")
	wantSummary := fmt.Sprintf("Now the primary billing contact: Summary Personsen (#%d)", contact.Id)
	if event.Summary != wantSummary {
		t.Errorf("summary = %q, want %q", event.Summary, wantSummary)
	}
	if got := event.Payload["roles"]; !reflect.DeepEqual(got, []any{
		map[string]any{"role": "billing", "primary": true},
		map[string]any{"role": "project", "primary": true},
	}) {
		t.Errorf("payload roles = %+v, want billing then project, both primary", got)
	}
	if got := event.Payload["title"]; got != "CEO" {
		t.Errorf("payload title = %v, want \"CEO\"", got)
	}

	// Only the title moving keeps the wording it has always had.
	if r := putAssociation(t, c, customer.Id, contact.Id, map[string]any{"title": "Chairman"}); r.Status != http.StatusOK {
		t.Fatalf("retitle: status %d body %s, want 200", r.Status, r.Body)
	}
	event = fetchTimelineEvent(t, h, customer.Id, "customer.contact_relationship_updated")
	wantSummary = fmt.Sprintf("Contact relationship updated: Summary Personsen (#%d)", contact.Id)
	if event.Summary != wantSummary {
		t.Errorf("summary after a retitle = %q, want %q", event.Summary, wantSummary)
	}

	// Dropping a role it is NOT primary for anywhere else reads as "Roles
	// updated" — no new primary, but the set moved. project is the only role
	// left after billing goes, and the contact is its primary already, so no
	// "Now the primary …" applies.
	if r := putAssociation(t, c, customer.Id, contact.Id, map[string]any{"title": "Chairman",
		"roles": []any{map[string]any{"role": "project"}}}); r.Status != http.StatusOK {
		t.Fatalf("drop billing: status %d body %s, want 200", r.Status, r.Body)
	}
	event = fetchTimelineEvent(t, h, customer.Id, "customer.contact_relationship_updated")
	wantSummary = fmt.Sprintf("Roles updated: Summary Personsen (#%d)", contact.Id)
	if event.Summary != wantSummary {
		t.Errorf("summary after dropping a role = %q, want %q", event.Summary, wantSummary)
	}
}
```

Add `"time"` to the import list for `h.Advance(time.Hour)`.

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- go test -count=1 -run 'TestAttachContact_First|TestAttachContact_Primary|TestUpdateCustomerContact_(Clearing|Losing|Omitted|Explicit|RecordsAnEvent)|TestAssociationRequests|TestGetContactCustomers_Carries|TestDetachContact_Promotes|TestDeleteContact_Promotes' ./internal/customers/
```
Expected: **every case FAILs now**, and each one's message names the absent role set or the missing refusal rather than a compile error. Write down which case failed how — Step 11 re-runs this exact command and expects PASS, and a case that was green here is a case that tests nothing. If one is green, fix the test before going on.

- [ ] **Step 6: Write the forced races — two `primary: true` writers, and the crossed lock orders**

Create `apps/server/internal/customers/contact_roles_concurrency_test.go`, still before the implementation. These three also build against nothing new, and they fail now for their own reasons: the first two find no role rows at all to count primaries among, and `TestAttachAndDeleteContact_CrossedLockOrders` cannot form a cycle yet because neither handler takes the second lock. Run them and record that:

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
taskset -c 0-3 mise exec -- go test -count=1 -run 'TestPutAssociation_Concurrent|TestDetachTheOnlyHolder|TestAttachAndDeleteContact_CrossedLockOrders|TestPartialIndexIsTheBackstop' ./internal/customers/
```
Expected: FAIL. Step 12 re-runs it and expects PASS.

```go
package customers_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file pins the customer-row lock every role write takes first (typed
// contact roles design D2; queries/addresses.sql's LockCustomer): two
// association writes against the same customer serialize through that one
// FOR NO KEY UPDATE lock, so ux_customer_contact_roles_primary (migration
// 00025) is never even at risk of a transient double primary or a missing one.
// It forces the interleaving with the lock gate the addresses' own concurrency
// file uses — gateCustomerLock, race and awaitLockWaiters are all declared
// elsewhere in this package and reused here rather than written a fourth time —
// rather than trusting Go's scheduler to interleave two sequential calls
// unluckily enough.
//
// -race cannot catch what this pins: a database row lock is not a Go data race.
// Run with -count=10 or more to exercise the timing.

// TestPutAssociation_ConcurrentPrimaryTrue_ExactlyOneWinner is design D2's own
// test case ("a forced race of two primary:true writers — one wins, the other
// demotes it, never two primaries"). Both requests are serialized through the
// customer lock, so neither is rejected and both must answer 200; only the
// database's transaction ordering decides which contact ends up the primary
// billing contact, and exactly one must.
//
// A mutation dropping LockCustomer from the update handler lets both writes'
// inner queries interleave, and the result is either a 500 from an unhandled
// unique violation (applyRoles' demote/insert never expects one) or two rows
// marked primary at once — which the "exactly one primary" assertion catches
// either way.
func TestPutAssociation_ConcurrentPrimaryTrue_ExactlyOneWinner(t *testing.T) {
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Concurrent Primary Co")
	first := createContact(t, c, map[string]any{"firstName": "Racer", "lastName": "Onesen"})
	second := createContact(t, c, map[string]any{"firstName": "Racer", "lastName": "Twosen"})
	third := createContact(t, c, map[string]any{"firstName": "Incumbent", "lastName": "Threesen"})

	// third holds billing first, so it is the primary both racers try to take.
	attachWithRoles(t, c, customer.Id, map[string]any{"contactId": third.Id, "title": "A", "roles": []any{map[string]any{"role": "billing"}}})
	attachWithRoles(t, c, customer.Id, map[string]any{"contactId": first.Id, "title": "B", "roles": []any{map[string]any{"role": "billing"}}})
	attachWithRoles(t, c, customer.Id, map[string]any{"contactId": second.Id, "title": "C", "roles": []any{map[string]any{"role": "billing"}}})

	release := gateCustomerLock(t, h, customer.Id)

	makePrimary := func(contactID int32, title string) func() *modtest.Response {
		return func() *modtest.Response {
			return c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/contacts/%d", customer.Id, contactID), map[string]any{
				"title": title, "roles": []any{map[string]any{"role": "billing", "primary": true}},
			})
		}
	}

	done := make(chan []*modtest.Response, 1)
	finished := make(chan struct{})
	go func() {
		done <- race(makePrimary(first.Id, "B"), makePrimary(second.Id, "C"))
		close(finished)
	}()
	awaitLockWaiters(t, h, 2, finished)
	release()
	responses := <-done

	for i, r := range responses {
		if r.Status != http.StatusOK {
			t.Errorf("response %d: status %d body %s, want 200", i, r.Status, r.Body)
		}
	}

	if n := primaryCountOf(t, h, customer.Id, "billing"); n != 1 {
		t.Errorf("primary billing holders = %d, want exactly 1", n)
	}
	winner := primaryHolderOf(t, h, customer.Id, "billing")
	if winner != first.Id && winner != second.Id {
		t.Errorf("primary billing holder = %d, want one of the two racers (%d or %d) — the incumbent must have been demoted", winner, first.Id, second.Id)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customer_contact_roles WHERE customer_id = $1 AND role = 'billing'`, customer.Id); n != 3 {
		t.Errorf("billing role rows = %d, want 3 (a race changes who is primary, never who holds the role)", n)
	}
}

// TestDetachTheOnlyHolder_RacesAttachingANewOne pins the same lock across the
// two other role writes: detaching the role's only (and therefore primary)
// holder while a new contact is attached with that role. Two orderings exist,
// both serialized, never interleaved — the detach first, so the new contact is
// the role's first holder and primary whatever it asked; or the attach first,
// so it joins as a plain member and the detach then promotes it as the only
// remaining one. Either way exactly one contact holds billing and it is
// primary: never two primaries, and never a holder with no primary among them.
func TestDetachTheOnlyHolder_RacesAttachingANewOne(t *testing.T) {
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Detach Races Attach Co")
	leaving := createContact(t, c, map[string]any{"firstName": "Leaving", "lastName": "Racersen"})
	joining := createContact(t, c, map[string]any{"firstName": "Joining", "lastName": "Racersen"})
	attachWithRoles(t, c, customer.Id, map[string]any{"contactId": leaving.Id, "title": "A", "roles": []any{map[string]any{"role": "billing"}}})

	release := gateCustomerLock(t, h, customer.Id)

	detach := func() *modtest.Response {
		return c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d/contacts/%d", customer.Id, leaving.Id), nil)
	}
	attach := func() *modtest.Response {
		return c.Do(http.MethodPost, fmt.Sprintf("/api/v1/customers/%d/contacts", customer.Id), map[string]any{
			"contactId": joining.Id, "title": "B", "roles": []any{map[string]any{"role": "billing"}},
		})
	}

	done := make(chan []*modtest.Response, 1)
	finished := make(chan struct{})
	go func() {
		done <- race(detach, attach)
		close(finished)
	}()
	awaitLockWaiters(t, h, 2, finished)
	release()
	responses := <-done

	if responses[0].Status != http.StatusNoContent {
		t.Errorf("detach: status %d body %s, want 204", responses[0].Status, responses[0].Body)
	}
	if responses[1].Status != http.StatusOK {
		t.Errorf("attach: status %d body %s, want 200", responses[1].Status, responses[1].Body)
	}

	if n := h.Count(t, `SELECT count(*) FROM customers.customer_contact_roles WHERE customer_id = $1 AND role = 'billing'`, customer.Id); n != 1 {
		t.Fatalf("billing role rows = %d, want exactly 1 (the leaver's gone, the joiner's there)", n)
	}
	if n := primaryCountOf(t, h, customer.Id, "billing"); n != 1 {
		t.Errorf("primary billing holders = %d, want exactly 1 (never zero with a holder present)", n)
	}
	if got := primaryHolderOf(t, h, customer.Id, "billing"); got != joining.Id {
		t.Errorf("primary billing holder = %d, want %d", got, joining.Id)
	}
}

// TestAttachAndDeleteContact_CrossedLockOrders forces the one lock cycle in this
// module: an attach takes the CUSTOMER row's lock and then the CONTACT row's
// (contacts.go's PostCustomersByIdContacts), while deleting a contact takes the
// contact row's lock first — it has to, that is the ported lock's purpose — and
// only then the row of every customer it must promote a new primary for. Two
// opposite orders, so the two can cycle, and PostgreSQL breaks a cycle by
// killing one side with 40P01.
//
// The gate is what makes the interleaving reachable: while a third transaction
// holds the customer row, the delete gets the contact lock and then queues for
// the customer, and the attach queues for the customer too. Releasing the gate
// admits one of them:
//
//   - the delete wins the customer lock: it already holds the contact lock, so
//     it finishes, and the attach then finds no contact and answers 404; or
//   - the attach wins the customer lock: it now wants the contact lock the
//     delete holds, while the delete wants the customer lock the attach holds —
//     a cycle. One of the two is killed with 40P01, db.RetrySerializable
//     (contactRoleWriteAttempts) runs the loser again from a fresh snapshot, and
//     it succeeds or 404s on the second attempt.
//
// Which branch a run takes is the database's choice, so this asserts what is
// true of both: the delete always ends up done, the attach answers 200 or 404,
// and **neither ever answers 500**. That last one is the retry's whole
// observable effect — remove db.RetrySerializable from either handler and the
// 40P01 victim escapes through `fmt.Errorf("customers: attach contact: %w", err)`
// as a 500, which is what this test catches. Run it with -count=20: the cycle
// branch is likely, not certain.
func TestAttachAndDeleteContact_CrossedLockOrders(t *testing.T) {
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Crossed Locks Co")
	target := createContact(t, c, map[string]any{"firstName": "Crossed", "lastName": "Locksen"})
	// The contact is already attached elsewhere, so the delete really does have
	// a customer row to lock and a promotion to consider rather than taking a
	// short path with no customer lock at all.
	other := createCustomer(t, c, "Crossed Other Co")
	attachWithRoles(t, c, other.Id, map[string]any{"contactId": target.Id, "title": "A",
		"roles": []any{map[string]any{"role": "billing"}}})

	release := gateCustomerLock(t, h, customer.Id)

	attach := func() *modtest.Response {
		return c.Do(http.MethodPost, fmt.Sprintf("/api/v1/customers/%d/contacts", customer.Id), map[string]any{
			"contactId": target.Id, "title": "B", "roles": []any{map[string]any{"role": "project"}},
		})
	}
	del := func() *modtest.Response {
		return c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/contacts/%d", target.Id), nil)
	}

	done := make(chan []*modtest.Response, 1)
	finished := make(chan struct{})
	go func() {
		done <- race(attach, del)
		close(finished)
	}()
	awaitLockWaiters(t, h, 2, finished)
	release()
	responses := <-done
	attachResp, deleteResp := responses[0], responses[1]

	if deleteResp.Status != http.StatusNoContent {
		t.Errorf("delete: status %d body %s, want 204", deleteResp.Status, deleteResp.Body)
	}
	if attachResp.Status != http.StatusOK && attachResp.Status != http.StatusNotFound {
		t.Errorf("attach: status %d body %s, want 200 or 404 — never a 500, which is what an unretried 40P01 looks like", attachResp.Status, attachResp.Body)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.contacts WHERE id = $1`, target.Id); n != 0 {
		t.Errorf("contact rows left = %d, want 0 (the delete always wins eventually)", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customer_contact_roles WHERE contact_id = $1`, target.Id); n != 0 {
		t.Errorf("orphaned role rows = %d, want 0", n)
	}
}

// TestPartialIndexIsTheBackstop proves the database's own last word is really
// there (design D2): a second primary row for one (customer, role), inserted
// behind the handlers' backs, is refused by
// ux_customer_contact_roles_primary. Nothing in the module can reach this
// state — that is the point — so the only way to pin the index is to try it
// directly, exactly as the addresses' invariant is documented to rely on it.
func TestPartialIndexIsTheBackstop(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Backstop Co")
	one := createContact(t, c, map[string]any{"firstName": "One", "lastName": "Backstopsen"})
	two := createContact(t, c, map[string]any{"firstName": "Two", "lastName": "Backstopsen"})
	attachWithRoles(t, c, customer.Id, map[string]any{"contactId": one.Id, "title": "A", "roles": []any{map[string]any{"role": "billing"}}})
	attachWithRoles(t, c, customer.Id, map[string]any{"contactId": two.Id, "title": "B", "roles": []any{map[string]any{"role": "billing"}}})

	_, err := h.Pool().Exec(t.Context(), `UPDATE customers.customer_contact_roles SET is_primary = true
	                                      WHERE customer_id = $1 AND contact_id = $2 AND role = 'billing'`, customer.Id, two.Id)
	if err == nil {
		t.Fatal("a second primary billing holder was accepted, want ux_customer_contact_roles_primary to refuse it")
	}
}
```

- [ ] **Step 7: Write `contact_roles.go` — the vocabulary, the request validation and the invariant**

Create `apps/server/internal/customers/contact_roles.go`:

```go
package customers

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
	"github.com/vantigo-io/vantigo/server/internal/customers/store"
)

// This file is the typed contact roles (typed contact roles design D2, D3):
// the vocabulary, the validation of a request's `roles` array, and the
// one-primary-per-role invariant every association write keeps.
//
// The invariant is the ADDRESSES' invariant (invoice-ready customer design D3;
// addresses.go), deliberately unaltered, with (customer_id, type) swapped for
// (customer_id, role) and "the oldest address" for "the longest-standing
// holder". That is not laziness: the two are the same problem, the addresses'
// version has a partial unique index, a concurrency test and a paragraph of
// documentation behind it, and a second, subtly different version of the same
// rule in one module is how the two drift. Anything below that reads like
// addresses.go is meant to.
//
// Every function here assumes the caller already holds the customer row's
// FOR NO KEY UPDATE lock (queries/addresses.sql's LockCustomer) and is inside
// that same transaction. Nothing here takes the lock itself, because the
// handlers need it for their own 404 first.

// The role vocabulary (design D2). contactRoleOrder is also the order every
// response and every payload lists roles in, so a client never sees them
// shuffle, and it is the order the promotion bookkeeping walks in, so two
// roles lost in one write promote deterministically.
const (
	contactRoleBilling       = "billing"
	contactRoleProject       = "project"
	contactRoleDecisionMaker = "decision_maker"
)

var contactRoleOrder = []string{contactRoleBilling, contactRoleProject, contactRoleDecisionMaker}

// contactRoleRank is contactRoleOrder as a sort key, with an unknown code
// last. There is no unknown code today — validateAssociationRole is the only
// way one enters — but a widened vocabulary is a value change, and a sort that
// silently drops what it does not recognise is worse than one that puts it at
// the end.
func contactRoleRank(role string) int {
	if i := slices.Index(contactRoleOrder, role); i >= 0 {
		return i
	}
	return len(contactRoleOrder)
}

// contactRole is one role an association holds, exactly the pair the contract
// answers (gen.CustomerContactRole) and exactly the pair the timeline payload
// carries — the json tags let it be both, the convention addressSnapshot
// follows for the same reason.
type contactRole struct {
	Role    string `json:"role"`
	Primary bool   `json:"primary"`
}

// requestedRole is one validated element of a request's `roles` array: the
// role, and what the request asked its primary flag to be. Primary is a
// POINTER because the field is three-valued, and the three values mean
// different things (design D2, D3):
//
//   - nil (omitted) on a role the association already holds: leave the flag
//     exactly as it is. This is the case that makes a set replace safe to
//     write: a client changing which roles a contact holds should not have to
//     echo every primary flag back, and reading an omitted flag as false would
//     turn "also give this contact billing" into "and stop being the primary
//     project contact" — which is not even applied, it is refused (see phase 1
//     of applyRoles), so the request fails for something it never said.
//   - nil on a role the association does NOT hold yet: the first-holder rule
//     decides — primary if nobody holds the role, otherwise not.
//   - non-nil: what the request said. true demotes the incumbent; an explicit
//     false on the only or primary holder is the refusal.
//
// "Asked" is still the operative word for the non-nil case: the invariant may
// override it, because the first contact given a role is its primary whatever
// the request says.
type requestedRole struct {
	Role    string
	Primary *bool
}

// wantsPrimary and clearsPrimary read requestedRole.Primary's three states
// without every call site repeating the nil check — and, more to the point,
// without any of them collapsing nil into false by accident, which is the one
// mistake this pointer exists to prevent.
func (r requestedRole) wantsPrimary() bool  { return r.Primary != nil && *r.Primary }
func (r requestedRole) clearsPrimary() bool { return r.Primary != nil && !*r.Primary }

// heldPrimary indexes a role set by role, answering each one's primary flag —
// the shape applyRoles' phase 1, requestedAsHeld and the update handler's early
// refusal all need, written once.
func heldPrimary(roles []contactRole) map[string]bool {
	out := make(map[string]bool, len(roles))
	for _, r := range roles {
		out[r.Role] = r.Primary
	}
	return out
}

// validatedAssociation is the validated, normalized values of an
// AttachCustomerContactRequest or a CustomerContactRequest
// (Endpoints/Customers/Contacts/Dtos/CustomerContactRequest.cs, plus design
// D1 and D3). RolesGiven distinguishes the two things a nil Roles can mean on
// an update: `roles: []` (hold none) from an omitted `roles` (leave them
// alone). Title is nil when the association has none.
type validatedAssociation struct {
	Title        *string
	Roles        []requestedRole
	RolesGiven   bool
	Phone, Email *string
}

// validateCustomerContactRequest is CustomerContactRequest.TryApplyTo, widened
// by design D1 and D3. Every field is validated regardless of an earlier one's
// failure and every error is reported together, keyed by the JSON field name —
// the module's all-errors-at-once shape.
//
// title and role are the same value under two names (D1): role is the
// deprecated alias the recorded corpus sends, title wins when both are given,
// and each is validated under its own noun so the message names the field the
// caller wrote. A blank string in either is an error, not an absence.
//
// rolesWhenOmitted is how many roles the association already holds, and it
// exists only for the title-or-role rule: an attach passes 0 (a new
// association holds none), an update passes the count it just read, because
// `roles` omitted on an update means "leave them alone" and an association
// that keeps three roles is not saying nothing about the person just because
// this request did not mention them.
func validateCustomerContactRequest(title, role *string, roles *[]gen.CustomerContactRoleRequest, phone, email *string, rolesWhenOmitted int) (validatedAssociation, map[string][]string) {
	errs := map[string][]string{}

	var parsedTitle *string
	switch {
	case title != nil:
		t, err := validateContactTitle(*title)
		if err != "" {
			errs["title"] = []string{err}
		} else {
			parsedTitle = &t
		}
	case role != nil:
		t, err := validateContactRole(*role)
		if err != "" {
			errs["role"] = []string{err}
		} else {
			parsedTitle = &t
		}
	}

	var parsedRoles []requestedRole
	rolesGiven := roles != nil
	if rolesGiven {
		var roleErrs []string
		seen := map[string]bool{}
		for _, r := range *roles {
			name, err := validateAssociationRole(r.Role)
			if err != "" {
				roleErrs = append(roleErrs, err)
				continue
			}
			if seen[name] {
				// Not the same as sending it once: a request naming a role
				// twice with two different primary flags has asked for two
				// contradictory things, and picking one of them silently is
				// how a client learns the wrong lesson about what it sent.
				roleErrs = append(roleErrs, fmt.Sprintf("A contact role can only be given once, but '%s' was given more than once", name))
				continue
			}
			seen[name] = true
			// r.Primary travels through as the pointer it arrived as: absent,
			// true and false are three different instructions here (see
			// requestedRole), so this is the one place that must NOT normalise
			// it into a bool.
			parsedRoles = append(parsedRoles, requestedRole{Role: name, Primary: r.Primary})
		}
		if len(roleErrs) > 0 {
			errs["roles"] = roleErrs
		}
		// Answered in the design's fixed order rather than the request's, so
		// the write's bookkeeping — and therefore which contact a lost primary
		// promotes when two roles move at once — does not depend on how a
		// client happened to order its array.
		slices.SortFunc(parsedRoles, func(a, b requestedRole) int { return contactRoleRank(a.Role) - contactRoleRank(b.Role) })
	}

	// The title-or-role rule (design D1): an association that says nothing
	// about the person is not worth having. Checked only once the two halves
	// are known to be individually valid, so a request with a too-long title
	// hears about the title rather than about a rule it did not break.
	if len(errs) == 0 {
		effectiveRoles := rolesWhenOmitted
		if rolesGiven {
			effectiveRoles = len(parsedRoles)
		}
		if parsedTitle == nil && effectiveRoles == 0 {
			errs["title"] = []string{"A contact needs a title or at least one role"}
		}
	}

	p := validateOptionalPhone("phone", phone, errs)
	e := validateOptionalEmail("email", email, errs)

	if len(errs) > 0 {
		return validatedAssociation{}, errs
	}
	return validatedAssociation{Title: parsedTitle, Roles: parsedRoles, RolesGiven: rolesGiven, Phone: p, Email: e}, nil
}

// errRolePrimaryTransitionRefused is the one role refusal that is only knowable
// under the customer's lock, so it travels out of the transaction as a sentinel
// the way addresses.go's errPrimaryTransitionRefused does — and it is the same
// refusal, worded for contacts.
var errRolePrimaryTransitionRefused = errors.New("customers: primary contact role transition refused")

// rolePrimaryTransitionMessage is errRolePrimaryTransitionRefused's field
// error, keyed `roles` (design D2). It names the role, because a request
// carrying three of them should not have to guess which one was refused.
func rolePrimaryTransitionMessage(role string) string {
	return fmt.Sprintf("A contact that is the only or primary holder of the '%s' role stays primary; make another contact primary instead", role)
}

// rolePromotion is one contact that became primary for one role as a SIDE
// EFFECT of somebody else's write — a request that dropped a role it was
// primary for, a detach, or a deleted contact. The handlers record one
// timeline event per promotion, on the promoted contact, with the acting user
// who caused it (design D4), which is why this has to travel back out of the
// bookkeeping instead of being invisible inside it.
type rolePromotion struct {
	ContactID int32
	Role      string
}

// demoteRoleHolder is the demote half of demote-before-promote (design D2;
// addresses.go's demoteCurrentPrimary): the role's current primary, whoever it
// is, stops being it. A role with no holder at all is not an error — there is
// simply nothing to demote.
func demoteRoleHolder(ctx context.Context, txq *store.Queries, customerID int32, role string) error {
	current, err := txq.PrimaryContactRoleHolder(ctx, store.PrimaryContactRoleHolderParams{CustomerID: customerID, Role: role})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return txq.SetContactRolePrimary(ctx, store.SetContactRolePrimaryParams{
		CustomerID: customerID, ContactID: current, Role: role, IsPrimary: false,
	})
}

// promoteLongestStandingHolder is the promote half (design D2; addresses.go's
// promoteOldestOfType): the longest-standing remaining holder of role, other
// than excludeContactID — the contact that just gave it up — becomes its
// primary. Nobody remaining is not an error: the role is unheld now, and
// "always a primary while anyone holds the role" is vacuous when nobody does.
// It answers the promoted contact, or 0 when there was none, so the caller can
// record the event design D4 requires.
func promoteLongestStandingHolder(ctx context.Context, txq *store.Queries, customerID, excludeContactID int32, role string) (int32, error) {
	next, err := txq.OldestContactRoleHolder(ctx, store.OldestContactRoleHolderParams{
		CustomerID: customerID, Role: role, ExcludeContactID: excludeContactID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if err := txq.SetContactRolePrimary(ctx, store.SetContactRolePrimaryParams{
		CustomerID: customerID, ContactID: next, Role: role, IsPrimary: true,
	}); err != nil {
		return 0, err
	}
	return next, nil
}

// applyRoles brings one association's roles from existing to want, inside the
// caller's transaction and under the customer row's lock it already holds, and
// answers the set the association holds afterwards plus every OTHER contact
// promoted along the way (design D2).
//
// The order of the four phases is the whole correctness argument, and it is
// addresses.go's order:
//
//  1. Refuse first, write nothing. An EXPLICIT `primary: false` on a role this
//     association currently holds AS primary is the refusal — the only or the
//     primary holder stays primary — and it has to be decided before any
//     statement runs, so a request that is going to be refused leaves the
//     database untouched rather than half-applied and rolled back. An OMITTED
//     flag is not that refusal: it means "leave this one alone" (see
//     requestedRole), which is why phase 1 asks clearsPrimary() and not
//     !wantsPrimary().
//  2. Delete the dropped roles, then promote in each of them. Deleting first
//     is what makes the promotion safe: while a row with is_primary = true
//     still exists, promoting another holder of the same role would put two
//     primaries in ux_customer_contact_roles_primary at once, even if only
//     until the transaction commits.
//  3. For each kept role, demote the incumbent before this contact takes over.
//     Same index, same reason, opposite direction.
//  4. For each new role, count the holders FIRST: none means this contact is
//     the first and is primary whatever it asked for (or did not ask); some
//     means the request's own flag decides — true demotes the incumbent, false
//     or omitted joins as a plain member.
func applyRoles(ctx context.Context, txq *store.Queries, customerID, contactID int32, existing []contactRole, want []requestedRole, now time.Time) ([]contactRole, []rolePromotion, error) {
	held := heldPrimary(existing)
	wanted := make(map[string]bool, len(want))
	for _, r := range want {
		wanted[r.Role] = true
	}

	// Phase 1: refuse, before anything is written. Only an EXPLICIT false
	// refuses; an omitted flag is "leave it alone" and can never be the
	// refusal, which is what keeps a set replace from failing for something it
	// did not say.
	for _, r := range want {
		if wasPrimary, ok := held[r.Role]; ok && wasPrimary && r.clearsPrimary() {
			return nil, nil, fmt.Errorf("%w: %s", errRolePrimaryTransitionRefused, r.Role)
		}
	}

	// Phase 2: the dropped roles leave, then each of them promotes.
	keep := make([]string, 0, len(want))
	for _, r := range want {
		keep = append(keep, r.Role)
	}
	if err := txq.DeleteContactRolesNotIn(ctx, store.DeleteContactRolesNotInParams{
		CustomerID: customerID, ContactID: contactID, Keep: keep,
	}); err != nil {
		return nil, nil, err
	}
	var promotions []rolePromotion
	for _, r := range existing { // existing is already in contactRoleOrder
		if _, stillWanted := wanted[r.Role]; stillWanted || !r.Primary {
			continue
		}
		promoted, err := promoteLongestStandingHolder(ctx, txq, customerID, contactID, r.Role)
		if err != nil {
			return nil, nil, err
		}
		if promoted != 0 {
			promotions = append(promotions, rolePromotion{ContactID: promoted, Role: r.Role})
		}
	}

	// Phases 3 and 4: the roles the association is to hold, in the fixed order.
	after := make([]contactRole, 0, len(want))
	for _, r := range want {
		wasPrimary, alreadyHeld := held[r.Role]
		switch {
		case alreadyHeld:
			// Omitted keeps what it was; true promotes; explicit false on a
			// non-primary holder keeps it non-primary (false on a PRIMARY
			// holder never reaches here — phase 1 refused it).
			primary := wasPrimary || r.wantsPrimary()
			if primary && !wasPrimary {
				if err := demoteRoleHolder(ctx, txq, customerID, r.Role); err != nil {
					return nil, nil, err
				}
				if err := txq.SetContactRolePrimary(ctx, store.SetContactRolePrimaryParams{
					CustomerID: customerID, ContactID: contactID, Role: r.Role, IsPrimary: true,
				}); err != nil {
					return nil, nil, err
				}
			}
			after = append(after, contactRole{Role: r.Role, Primary: primary})
		default:
			holders, err := txq.CountContactRoleHolders(ctx, store.CountContactRoleHoldersParams{
				CustomerID: customerID, Role: r.Role, ExcludeContactID: contactID,
			})
			if err != nil {
				return nil, nil, err
			}
			// A role the association does not hold yet: the flag it asked for,
			// with omitted reading as false here and only here — the
			// first-holder rule below is what an omitted flag on a new role
			// actually means, and it is the next line.
			primary := r.wantsPrimary()
			if holders == 0 {
				primary = true
			} else if primary {
				if err := demoteRoleHolder(ctx, txq, customerID, r.Role); err != nil {
					return nil, nil, err
				}
			}
			if err := txq.InsertContactRole(ctx, store.InsertContactRoleParams{
				CustomerID: customerID, ContactID: contactID, Role: r.Role, IsPrimary: primary, Now: now,
			}); err != nil {
				return nil, nil, err
			}
			after = append(after, contactRole{Role: r.Role, Primary: primary})
		}
	}
	return after, promotions, nil
}

// releaseRoles is applyRoles' detach case (design D2): the association is gone
// — DELETE .../contacts/{contactId} removed its row, or DELETE
// /customers/contacts/{id} removed the contact and the composite foreign key's
// ON DELETE CASCADE took the role rows with it — so there is nothing to
// refuse, nothing to keep and nothing to insert, only a promotion in each role
// this association was primary for. The caller must already have deleted the
// row: promoting while a primary row still exists is the double-primary the
// partial unique index forbids, which is why this takes `held` as an argument
// rather than reading it itself.
func releaseRoles(ctx context.Context, txq *store.Queries, customerID, contactID int32, held []contactRole) ([]rolePromotion, error) {
	var promotions []rolePromotion
	for _, r := range held { // already in contactRoleOrder
		if !r.Primary {
			continue
		}
		promoted, err := promoteLongestStandingHolder(ctx, txq, customerID, contactID, r.Role)
		if err != nil {
			return nil, err
		}
		if promoted != 0 {
			promotions = append(promotions, rolePromotion{ContactID: promoted, Role: r.Role})
		}
	}
	return promotions, nil
}

// contactRolesOf reads one association's roles as the type the rest of this
// file speaks, already in the design's fixed order (the query's own ORDER BY).
func contactRolesOf(ctx context.Context, q *store.Queries, customerID, contactID int32) ([]contactRole, error) {
	rows, err := q.ContactRolesForAssociation(ctx, store.ContactRolesForAssociationParams{CustomerID: customerID, ContactID: contactID})
	if err != nil {
		return nil, err
	}
	out := make([]contactRole, 0, len(rows))
	for _, r := range rows {
		out = append(out, contactRole{Role: r.Role, Primary: r.IsPrimary})
	}
	return out, nil
}

// genContactRoles is contactRole's contract projection. It always answers a
// non-nil pointer to a non-nil slice, so `roles` is always on the wire and is
// `[]` rather than `null` for a contact that holds none — the contract keeps
// the property optional only because the recorded exchange corpus predates it
// (the same treatment SafeCustomerResponse.tags gets).
func genContactRoles(roles []contactRole) *[]gen.CustomerContactRole {
	out := make([]gen.CustomerContactRole, 0, len(roles))
	for _, r := range roles {
		out = append(out, gen.CustomerContactRole{Role: r.Role, Primary: r.Primary})
	}
	return &out
}

// rolesChanged reports whether two role sets differ at all — membership or a
// primary flag. Both sides are in contactRoleOrder, so this is an element-wise
// comparison and not a set operation.
func rolesChanged(before, after []contactRole) bool {
	return !slices.Equal(before, after)
}

// associationProblem is the 400 body every association write answers, keyed by
// field. One helper because the two writing handlers each have their own
// generated response type and would otherwise repeat the title string — and
// the title is what a UI shows above the field errors, so a typo in one of two
// copies is a visible inconsistency. The return type is whatever
// apicommon.ValidationProblem answers (HttpValidationProblemDetails); read it
// once and match it rather than guessing.
func associationProblem(errs map[string][]string) apicommon.HttpValidationProblemDetails {
	return apicommon.ValidationProblem("Invalid contact association", errs)
}

// roleErrorsFor turns errRolePrimaryTransitionRefused back into the field error
// the caller sees. The sentinel is wrapped with the role name (applyRoles), so
// this reads it back out rather than making every handler format the message.
func roleErrorsFor(err error) map[string][]string {
	role := strings.TrimSpace(strings.TrimPrefix(err.Error(), errRolePrimaryTransitionRefused.Error()+":"))
	return map[string][]string{"roles": {rolePrimaryTransitionMessage(role)}}
}
```

- [ ] **Step 8: Teach the timeline about `title`, `roles` and the two new summaries**

In `apps/server/internal/customers/contacts_timeline.go`, replace `recordContactEvent` and the four recorders with:

```go
// recordContactEvent is AddContact (SV/CustomerTimelineRecorder.cs:99-124),
// widened by typed contact roles design D4: the payload gains `title` (the
// same value `role` carries, under the name the field now has) and `roles`,
// and `role` stays exactly where it was because the recorded corpus and every
// timeline entry already written speak it.
func recordContactEvent(ctx context.Context, q *store.Queries, now time.Time, customerID int32, eventType, action string, contact store.CustomersContact, title *string, roles []contactRole, phone, email *string, actorKind, actorDisplay string, actorUserID *uuid.UUID) error {
	displayName := contactDisplayName(contact.FirstName, contact.MiddleName, contact.LastName)
	summary := fmt.Sprintf("%s: %s (#%d)", action, displayName, contact.ID)
	// Never nil: a payload that says "roles": null cannot be told apart from
	// one written before roles existed, while "roles": [] says the association
	// holds none, which is a fact.
	if roles == nil {
		roles = []contactRole{}
	}
	payload := map[string]any{
		"customerId":  customerID,
		"contactId":   contact.ID,
		"displayName": displayName,
		"firstName":   contact.FirstName,
		"middleName":  contact.MiddleName,
		"lastName":    contact.LastName,
		"role":        deref(title),
		"title":       title,
		"roles":       roles,
		"phone":       phone,
		"email":       email,
	}
	return recordGeneratedEvent(ctx, q, customerID, now, eventType, truncateUTF16(summary, 500), payload, 1, actorKind, actorDisplay, actorUserID)
}

// recordContactAttached is RecordContactAttached (SV/CustomerTimelineRecorder.cs:87-88).
func recordContactAttached(ctx context.Context, q *store.Queries, now time.Time, customerID int32, contact store.CustomersContact, title *string, roles []contactRole, phone, email *string, actorKind, actorDisplay string, actorUserID *uuid.UUID) error {
	return recordContactEvent(ctx, q, now, customerID, "customer.contact_attached", "Contact linked", contact, title, roles, phone, email, actorKind, actorDisplay, actorUserID)
}

// recordContactRelationshipUpdated is RecordContactRelationshipUpdated
// (SV/CustomerTimelineRecorder.cs:90-91): only called when the title, the
// phone, the email, the role set or a primary flag actually changed (design
// D4), and its action says which of those it was — see
// relationshipUpdateAction.
func recordContactRelationshipUpdated(ctx context.Context, q *store.Queries, now time.Time, customerID int32, contact store.CustomersContact, action string, title *string, roles []contactRole, phone, email *string, actorKind, actorDisplay string, actorUserID *uuid.UUID) error {
	return recordContactEvent(ctx, q, now, customerID, "customer.contact_relationship_updated", action, contact, title, roles, phone, email, actorKind, actorDisplay, actorUserID)
}

// recordContactDetached is RecordContactDetached (SV/CustomerTimelineRecorder.cs:93-94).
func recordContactDetached(ctx context.Context, q *store.Queries, now time.Time, customerID int32, contact store.CustomersContact, title *string, roles []contactRole, phone, email *string, actorKind, actorDisplay string, actorUserID *uuid.UUID) error {
	return recordContactEvent(ctx, q, now, customerID, "customer.contact_detached", "Contact unlinked", contact, title, roles, phone, email, actorKind, actorDisplay, actorUserID)
}

// recordContactRemoved is RecordContactRemoved (SV/CustomerTimelineRecorder.cs:96-97),
// called once per association DeleteContact cascades over, before the contact
// row itself is deleted.
func recordContactRemoved(ctx context.Context, q *store.Queries, now time.Time, customerID int32, contact store.CustomersContact, title *string, roles []contactRole, phone, email *string, actorKind, actorDisplay string, actorUserID *uuid.UUID) error {
	return recordContactEvent(ctx, q, now, customerID, "customer.contact_removed", "Contact removed", contact, title, roles, phone, email, actorKind, actorDisplay, actorUserID)
}

// relationshipUpdateActionDefault is what an update that moved only the title,
// the phone or the email has always said.
const relationshipUpdateActionDefault = "Contact relationship updated"

// relationshipUpdateAction is design D4's "its summary names what changed".
// Becoming a role's primary is the one change worth saying out loud on a
// timeline — it is the answer to "who gets the invoice" moving — so it wins
// over the plainer wordings, and a write that made this contact primary for
// more than one role names the first in the design's fixed order rather than
// listing them: the summary is a varchar(500) one-liner in a feed, and the
// payload carries the whole set for anyone who needs it.
func relationshipUpdateAction(before, after []contactRole) string {
	wasPrimary := make(map[string]bool, len(before))
	for _, r := range before {
		wasPrimary[r.Role] = r.Primary
	}
	for _, r := range after { // after is in contactRoleOrder
		if r.Primary && !wasPrimary[r.Role] {
			return fmt.Sprintf("Now the primary %s contact", contactRoleSummaryLabel(r.Role))
		}
	}
	if rolesChanged(before, after) {
		return "Roles updated"
	}
	return relationshipUpdateActionDefault
}

// contactRoleSummaryLabel is a role inside an English sentence, which is not
// the same as the code: "decision_maker" reads as a column name in a feed.
// Only the summary uses it — the payload and the API always carry the code —
// and the frontend never reads it, because the frontend has its own catalogs.
func contactRoleSummaryLabel(role string) string {
	if role == contactRoleDecisionMaker {
		return "decision-maker"
	}
	return role
}

// recordContactPromoted is design D4's promotion event: a contact that became
// a role's primary because SOMEBODY ELSE gave it up, was detached, or was
// deleted. It is a customer.contact_relationship_updated — the relationship
// did change, and inventing a type for it would be a type no timeline filter
// knows — recorded against the promoted contact, with the acting user who
// caused it rather than a system actor: a person did this, indirectly, and the
// timeline's job is to say who.
func recordContactPromoted(ctx context.Context, q *store.Queries, now time.Time, customerID int32, contact store.CustomersContact, role string, title *string, roles []contactRole, phone, email *string, actorKind, actorDisplay string, actorUserID *uuid.UUID) error {
	action := fmt.Sprintf("Now the primary %s contact", contactRoleSummaryLabel(role))
	return recordContactEvent(ctx, q, now, customerID, "customer.contact_relationship_updated", action, contact, title, roles, phone, email, actorKind, actorDisplay, actorUserID)
}
```

`truncateUTF16` (timeline.go:92) is now applied to the summary: a contact's display name is three name parts of up to 100 characters each, and the longest action above adds twenty-odd more, so the total can exceed `varchar(500)` — it could before this delivery too, and the addresses' recorders already truncate.

- [ ] **Step 9: Rewrite the four handlers in `contacts.go`**

Delete `validatedAssociation` and `validateCustomerContactRequest` from `contacts.go` (lines ~129-155) — they now live in `contact_roles.go` — and add one constant beside the two existing sentinels:

```go
// contactRoleWriteAttempts is how often an association write's transaction runs
// before the deadlock it keeps losing escapes as a 500. Three, as tags.go's
// tagWriteAttempts and identity's serializableAttempts both settled on.
//
// The deadlock is real and is between two handlers in this very file. An
// attach locks the CUSTOMER row (LockCustomer, so the role bookkeeping
// serializes) and then the CONTACT row (GetContactForUpdate, the ported lock
// that serializes attach against a concurrent delete of the same contact),
// while DELETE /customers/contacts/{id} locks the contact row first — it has
// to, that is the lock's whole purpose — and only then the rows of every
// customer it must promote a new primary for. Two opposite lock orders, so the
// two can cycle; PostgreSQL breaks it by killing one side (40P01). Being the
// victim of a lock-order cycle is not something either caller did wrong, so
// the retry runs the loser again from a fresh snapshot, in which one of the two
// writes has simply already happened.
//
// Reversing one of the orders instead was considered and rejected: the delete
// cannot lock the customers before the contact, because which customers those
// are is what reading the contact's associations tells it, and an association
// added between that read and the lock would need a retry anyway.
const contactRoleWriteAttempts = 3
```

**`PostCustomersByIdContacts`** — validation still runs before existence (`TestAttachContact_InvalidConnectionAgainstUnknownCustomer_Returns400` pins it), and `rolesWhenOmitted` is 0 because a new association holds no roles:

```go
func (s *server) PostCustomersByIdContacts(ctx context.Context, req gen.PostCustomersByIdContactsRequestObject) (gen.PostCustomersByIdContactsResponseObject, error) {
	body := gen.AttachCustomerContactRequest{}
	if req.Body != nil {
		body = *req.Body
	}

	assoc, errs := validateCustomerContactRequest(body.Title, body.Role, body.Roles, body.Phone, body.Email, 0)
	if errs != nil {
		return gen.PostCustomersByIdContacts400ApplicationProblemPlusJSONResponse(associationProblem(errs)), nil
	}

	now := s.deps.Clock()
	// Resolved before the transaction opens (customers foundation design D1,
	// actor.go): a successful write always follows past validation, and the
	// 404/409 refusals are only knowable inside the transaction, so one wasted
	// directory call on those paths is accepted rather than resolving it twice.
	act, err := s.actorFor(ctx, generatedFallbackActor)
	if err != nil {
		return nil, fmt.Errorf("customers: resolve actor: %w", err)
	}

	var response gen.CustomerContactResponse
	err = db.RetrySerializable(ctx, contactRoleWriteAttempts, func() error {
		return db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
			txq := store.New(tx)

			// The customer row's lock comes first, before the contact's: every
			// write that touches customer_contact_roles takes it (typed
			// contact roles design D2), and it is also this handler's
			// existence check, replacing the plain GetCustomer it used to make.
			customer, err := txq.LockCustomer(ctx, req.Id)
			if errors.Is(err, pgx.ErrNoRows) {
				return errAssociationTargetNotFound
			}
			if err != nil {
				return err
			}
			contact, err := txq.GetContactForUpdate(ctx, body.ContactId)
			if errors.Is(err, pgx.ErrNoRows) {
				return errAssociationTargetNotFound
			}
			if err != nil {
				return err
			}

			attached, err := txq.AssociationExists(ctx, store.AssociationExistsParams{CustomerID: req.Id, ContactID: body.ContactId})
			if err != nil {
				return err
			}
			if attached {
				return errAlreadyAttached
			}

			if err := txq.InsertAssociation(ctx, store.InsertAssociationParams{
				CustomerID: req.Id, ContactID: body.ContactId, Title: assoc.Title, Phone: assoc.Phone, Email: assoc.Email,
			}); err != nil {
				return err
			}
			// nil existing: the association was created a statement ago, so
			// every role it is given is a new one and the first-holder rule is
			// the only one that can apply.
			roles, promotions, err := applyRoles(ctx, txq, req.Id, body.ContactId, nil, assoc.Roles, now)
			if err != nil {
				return err
			}
			if err := recordContactAttached(ctx, txq, now, customer.ID, contact, assoc.Title, roles, assoc.Phone, assoc.Email, act.Kind, act.Display, act.UserID); err != nil {
				return err
			}
			if err := recordPromotions(ctx, txq, now, req.Id, promotions, act); err != nil {
				return err
			}

			response = gen.CustomerContactResponse{
				Contact: contactResponse(contact), Role: deref(assoc.Title), Title: assoc.Title,
				Roles: genContactRoles(roles), Phone: assoc.Phone, Email: assoc.Email,
			}
			return nil
		})
	})
	switch {
	case errors.Is(err, errAssociationTargetNotFound):
		return gen.PostCustomersByIdContacts404Response{}, nil
	case errors.Is(err, errAlreadyAttached):
		detail := fmt.Sprintf("Contact %d is already associated with customer %d.", body.ContactId, req.Id)
		return gen.PostCustomersByIdContacts409ApplicationProblemPlusJSONResponse(apicommon.ProblemStatus("Contact already associated", detail, http.StatusConflict)), nil
	case errors.Is(err, errRolePrimaryTransitionRefused):
		// Unreachable on an attach — nothing is held yet, so no primary can be
		// cleared — but handled rather than falling into the 500 below, because
		// "unreachable" is a property of applyRoles' phase 1 and not of this
		// call site, and a future change to either should surface as the 400 it
		// is.
		return gen.PostCustomersByIdContacts400ApplicationProblemPlusJSONResponse(associationProblem(roleErrorsFor(err))), nil
	case err != nil:
		return nil, fmt.Errorf("customers: attach contact: %w", err)
	}
	return gen.PostCustomersByIdContacts200JSONResponse(response), nil
}
```

**`PutCustomersByIdContactsByContactId`** — the association lookup still precedes validation (`TestUpdateCustomerContact_InvalidConnectionAgainstUnknownAssociation_Returns404` pins it), the no-op now answers without opening a transaction at all, and the authoritative read happens under the lock:

```go
func (s *server) PutCustomersByIdContactsByContactId(ctx context.Context, req gen.PutCustomersByIdContactsByContactIdRequestObject) (gen.PutCustomersByIdContactsByContactIdResponseObject, error) {
	q := store.New(s.deps.Pool)
	existing, err := q.GetAssociationWithContact(ctx, store.GetAssociationWithContactParams{CustomerID: req.Id, ContactID: req.ContactId})
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.PutCustomersByIdContactsByContactId404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("customers: get association: %w", err)
	}
	currentRoles, err := contactRolesOf(ctx, q, req.Id, req.ContactId)
	if err != nil {
		return nil, fmt.Errorf("customers: read association roles: %w", err)
	}

	body := gen.CustomerContactRequest{}
	if req.Body != nil {
		body = *req.Body
	}
	// rolesWhenOmitted is what the association already holds: `roles` omitted
	// means "leave them alone" (design D3), so the title-or-role rule must not
	// refuse a request that only changes a phone number on an association that
	// already has three roles.
	assoc, errs := validateCustomerContactRequest(body.Title, body.Role, body.Roles, body.Phone, body.Email, len(currentRoles))
	if errs != nil {
		return gen.PutCustomersByIdContactsByContactId400ApplicationProblemPlusJSONResponse(associationProblem(errs)), nil
	}

	// An omitted `roles` is the current set, so the rest of this handler can
	// treat "what to hold" as one thing. Every element's Primary is nil — "leave
	// this one alone" — and not the flag copied out of currentRoles: a request
	// that did not mention roles at all must be unable to move a primary flag,
	// and nil is the only value of the three that guarantees that even if the
	// set changed under us between the unlocked read and the lock.
	want := assoc.Roles
	if !assoc.RolesGiven {
		want = make([]requestedRole, 0, len(currentRoles))
		for _, r := range currentRoles {
			want = append(want, requestedRole{Role: r.Role, Primary: nil})
		}
	}

	// The refusal is decided before the no-op shortcut below, on the set the
	// unlocked read found: a request asking to clear the primary flag of a role
	// this contact is the only or the primary holder of is refused (design D2)
	// even when it changes nothing else, because answering 200 to it would tell
	// the client its `primary: false` was honoured. Only an EXPLICIT false is
	// this refusal — an omitted flag means "leave it alone" and is never
	// refused. applyRoles refuses again under the lock, and that check is the
	// authoritative one; this one only makes sure the shortcut cannot swallow it.
	currentPrimary := heldPrimary(currentRoles)
	for _, r := range want {
		if wasPrimary, ok := currentPrimary[r.Role]; ok && wasPrimary && r.clearsPrimary() {
			return gen.PutCustomersByIdContactsByContactId400ApplicationProblemPlusJSONResponse(
				associationProblem(map[string][]string{"roles": {rolePrimaryTransitionMessage(r.Role)}})), nil
		}
	}

	answer := gen.PutCustomersByIdContactsByContactId200JSONResponse{
		Contact: contactResponse(contactFromAssociationRow(existing)), Role: deref(assoc.Title), Title: assoc.Title,
		Roles: genContactRoles(currentRoles), Phone: assoc.Phone, Email: assoc.Email,
	}

	fieldsChanged := deref(existing.Title) != deref(assoc.Title) ||
		deref(existing.AssociationPhone) != deref(assoc.Phone) ||
		deref(existing.AssociationEmail) != deref(assoc.Email)
	if !fieldsChanged && !rolesChanged(currentRoles, requestedAsHeld(want, currentRoles)) {
		// Nothing moved: the no-op rule every write in this module follows
		// (customers foundation design D5), and the reason this handler no
		// longer opens a transaction for one — the UPDATE would rewrite
		// identical values, the role bookkeeping would rewrite identical rows,
		// no event would be recorded anyway, and the actor lookup below would
		// be a directory call made for a request that writes nothing. A
		// concurrent writer can make this answer stale, which is what
		// last-wins on an off-the-row resource means (tags.go says the same).
		return answer, nil
	}

	now := s.deps.Clock()
	// Resolved before the transaction opens, and only now that a write is
	// certain to follow (customers foundation design D1, actor.go).
	act, err := s.actorFor(ctx, generatedFallbackActor)
	if err != nil {
		return nil, fmt.Errorf("customers: resolve actor: %w", err)
	}

	err = db.RetrySerializable(ctx, contactRoleWriteAttempts, func() error {
		return db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
			txq := store.New(tx)
			if _, err := txq.LockCustomer(ctx, req.Id); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return errAssociationTargetNotFound
				}
				return err
			}
			// Re-read under the lock: the unlocked read above answered the
			// 404 and shaped the validation, but a concurrent detach could
			// have removed the association since, and inserting role rows for
			// an association that no longer exists is a foreign-key violation
			// rather than the 404 it really is. Detach takes this same lock,
			// so re-reading inside it is a complete answer, not a narrower
			// window.
			locked, err := txq.GetAssociationWithContact(ctx, store.GetAssociationWithContactParams{CustomerID: req.Id, ContactID: req.ContactId})
			if errors.Is(err, pgx.ErrNoRows) {
				return errAssociationTargetNotFound
			}
			if err != nil {
				return err
			}
			before, err := contactRolesOf(ctx, txq, req.Id, req.ContactId)
			if err != nil {
				return err
			}

			if err := txq.UpdateAssociation(ctx, store.UpdateAssociationParams{
				CustomerID: req.Id, ContactID: req.ContactId, Title: assoc.Title, Phone: assoc.Phone, Email: assoc.Email,
			}); err != nil {
				return err
			}
			after, promotions, err := applyRoles(ctx, txq, req.Id, req.ContactId, before, want, now)
			if err != nil {
				return err
			}
			answer.Roles = genContactRoles(after)

			// Recomputed against what the lock actually found: a concurrent
			// writer may already have made this exact change, and an event
			// claiming a change that did not happen is worse than the wasted
			// actor lookup above (the same trade addresses.go documents).
			if deref(locked.Title) != deref(assoc.Title) ||
				deref(locked.AssociationPhone) != deref(assoc.Phone) ||
				deref(locked.AssociationEmail) != deref(assoc.Email) ||
				rolesChanged(before, after) {
				if err := recordContactRelationshipUpdated(ctx, txq, now, req.Id, contactFromAssociationRow(locked),
					relationshipUpdateAction(before, after), assoc.Title, after, assoc.Phone, assoc.Email,
					act.Kind, act.Display, act.UserID); err != nil {
					return err
				}
			}
			return recordPromotions(ctx, txq, now, req.Id, promotions, act)
		})
	})
	switch {
	case errors.Is(err, errAssociationTargetNotFound):
		return gen.PutCustomersByIdContactsByContactId404Response{}, nil
	case errors.Is(err, errRolePrimaryTransitionRefused):
		return gen.PutCustomersByIdContactsByContactId400ApplicationProblemPlusJSONResponse(associationProblem(roleErrorsFor(err))), nil
	case err != nil:
		return nil, fmt.Errorf("customers: update association: %w", err)
	}

	return answer, nil
}

// requestedAsHeld predicts what applyRoles will leave the association holding,
// so the no-op check can compare like with like. It resolves the two things a
// request does not state outright (see requestedRole):
//
//   - a role the association already holds with an OMITTED flag keeps the flag
//     it has, which is the whole reason the flag is a pointer; and
//   - a role it already holds with an explicit true is primary, while an
//     explicit true on a role it does not hold yet may or may not be (the
//     first-holder rule needs a holder count this function does not have) —
//     which does not matter, because a role the association does not hold is a
//     MEMBERSHIP change and rolesChanged has already answered true whatever
//     flag is predicted for it.
func requestedAsHeld(want []requestedRole, held []contactRole) []contactRole {
	wasPrimary := heldPrimary(held)
	out := make([]contactRole, 0, len(want))
	for _, r := range want {
		primary, alreadyHeld := wasPrimary[r.Role]
		if !alreadyHeld {
			primary = r.wantsPrimary()
		} else if r.wantsPrimary() {
			primary = true
		}
		out = append(out, contactRole{Role: r.Role, Primary: primary})
	}
	return out
}
```

**`DeleteCustomersByIdContactsByContactId`** — the detach:

```go
func (s *server) DeleteCustomersByIdContactsByContactId(ctx context.Context, req gen.DeleteCustomersByIdContactsByContactIdRequestObject) (gen.DeleteCustomersByIdContactsByContactIdResponseObject, error) {
	q := store.New(s.deps.Pool)
	if _, err := q.GetAssociationWithContact(ctx, store.GetAssociationWithContactParams{CustomerID: req.Id, ContactID: req.ContactId}); errors.Is(err, pgx.ErrNoRows) {
		return gen.DeleteCustomersByIdContactsByContactId404Response{}, nil
	} else if err != nil {
		return nil, fmt.Errorf("customers: get association: %w", err)
	}

	// Resolved before the transaction opens: this handler always records a
	// "detached" event once it reaches here (the 404 case wastes one call).
	act, err := s.actorFor(ctx, generatedFallbackActor)
	if err != nil {
		return nil, fmt.Errorf("customers: resolve actor: %w", err)
	}

	now := s.deps.Clock()
	err = db.RetrySerializable(ctx, contactRoleWriteAttempts, func() error {
		return db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
			txq := store.New(tx)
			if _, err := txq.LockCustomer(ctx, req.Id); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return errAssociationTargetNotFound
				}
				return err
			}
			// Re-read under the lock, the same reason the PUT does: the 404
			// above was decided outside it.
			locked, err := txq.GetAssociationWithContact(ctx, store.GetAssociationWithContactParams{CustomerID: req.Id, ContactID: req.ContactId})
			if errors.Is(err, pgx.ErrNoRows) {
				return errAssociationTargetNotFound
			}
			if err != nil {
				return err
			}
			held, err := contactRolesOf(ctx, txq, req.Id, req.ContactId)
			if err != nil {
				return err
			}

			// The association row goes first, and its roles go with it through
			// the composite foreign key's ON DELETE CASCADE (migration 00025).
			// Only then can another holder be promoted: while this contact's
			// is_primary row still exists, promoting one would put two
			// primaries of one role in ux_customer_contact_roles_primary at
			// once — the same delete-before-promote order
			// DeleteCustomersByIdAddressesByAddressId keeps, for the same
			// index-shaped reason.
			if err := txq.DeleteAssociation(ctx, store.DeleteAssociationParams{CustomerID: req.Id, ContactID: req.ContactId}); err != nil {
				return err
			}
			promotions, err := releaseRoles(ctx, txq, req.Id, req.ContactId, held)
			if err != nil {
				return err
			}

			if err := recordContactDetached(ctx, txq, now, req.Id, contactFromAssociationRow(locked), locked.Title, held,
				locked.AssociationPhone, locked.AssociationEmail, act.Kind, act.Display, act.UserID); err != nil {
				return err
			}
			return recordPromotions(ctx, txq, now, req.Id, promotions, act)
		})
	})
	switch {
	case errors.Is(err, errAssociationTargetNotFound):
		return gen.DeleteCustomersByIdContactsByContactId404Response{}, nil
	case err != nil:
		return nil, fmt.Errorf("customers: detach contact: %w", err)
	}
	return gen.DeleteCustomersByIdContactsByContactId204Response{}, nil
}
```

**`DeleteCustomersContactsById`** — the contact delete, which cascades over every association:

```go
func (s *server) DeleteCustomersContactsById(ctx context.Context, req gen.DeleteCustomersContactsByIdRequestObject) (gen.DeleteCustomersContactsByIdResponseObject, error) {
	// Resolved before the transaction opens: the directory lookup actorFor can
	// make is an out-of-process call this module never wants to make while
	// holding a row lock (customers foundation design D1, actor.go).
	act, err := s.actorFor(ctx, generatedFallbackActor)
	if err != nil {
		return nil, fmt.Errorf("customers: resolve actor: %w", err)
	}

	now := s.deps.Clock()
	err = db.RetrySerializable(ctx, contactRoleWriteAttempts, func() error {
		return db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
			txq := store.New(tx)
			contact, err := txq.GetContactForUpdate(ctx, req.Id)
			if err != nil {
				return err
			}
			associations, err := txq.ListAssociationsForContact(ctx, req.Id)
			if err != nil {
				return err
			}
			roleRows, err := txq.ContactRolesForContact(ctx, req.Id)
			if err != nil {
				return err
			}
			rolesByCustomer := make(map[int32][]contactRole, len(associations))
			for _, r := range roleRows { // already ordered by customer, then the fixed role order
				rolesByCustomer[r.CustomerID] = append(rolesByCustomer[r.CustomerID], contactRole{Role: r.Role, Primary: r.IsPrimary})
			}

			// Every customer this contact is attached to has to be locked
			// before its roles are re-arranged (typed contact roles design
			// D2), and in the order ListAssociationsForContact answers —
			// ascending customer_id, which the query's own ORDER BY
			// guarantees. A deterministic order across all callers is what
			// keeps two concurrent deletes of two contacts that share two
			// customers from deadlocking with each other; the retry above
			// exists for the other cycle, the one against an attach.
			for _, a := range associations {
				if _, err := txq.LockCustomer(ctx, a.CustomerID); err != nil {
					return err
				}
			}
			for _, a := range associations {
				if err := recordContactRemoved(ctx, txq, now, a.CustomerID, contact, a.Title, rolesByCustomer[a.CustomerID],
					a.Phone, a.Email, act.Kind, act.Display, act.UserID); err != nil {
					return err
				}
			}

			// The contact goes, and with it every association and every role
			// row (two cascades: contacts → customers_contacts → 
			// customer_contact_roles). Only then is a promotion safe, the same
			// delete-before-promote order the detach keeps.
			if err := txq.DeleteContact(ctx, req.Id); err != nil {
				return err
			}
			for _, a := range associations {
				promotions, err := releaseRoles(ctx, txq, a.CustomerID, req.Id, rolesByCustomer[a.CustomerID])
				if err != nil {
					return err
				}
				if err := recordPromotions(ctx, txq, now, a.CustomerID, promotions, act); err != nil {
					return err
				}
			}
			return nil
		})
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return gen.DeleteCustomersContactsById404Response{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("customers: delete contact: %w", err)
	}
	return gen.DeleteCustomersContactsById204Response{}, nil
}
```

and the shared promotion recorder, in `contacts.go` beside the handlers that use it:

```go
// recordPromotions records design D4's promotion event for each contact that
// became a role's primary as a side effect of the write just made: on the
// promoted contact, with the acting user who caused it. It reads each promoted
// association back — the event's payload is that association's own title,
// phone, email and full role set, not a fragment — which is one query per
// promotion and at most three per write, since a contact can be primary for at
// most the three roles there are.
func recordPromotions(ctx context.Context, txq *store.Queries, now time.Time, customerID int32, promotions []rolePromotion, act actor) error {
	for _, p := range promotions {
		row, err := txq.GetAssociationWithContact(ctx, store.GetAssociationWithContactParams{CustomerID: customerID, ContactID: p.ContactID})
		if err != nil {
			return err
		}
		roles, err := contactRolesOf(ctx, txq, customerID, p.ContactID)
		if err != nil {
			return err
		}
		if err := recordContactPromoted(ctx, txq, now, customerID, contactFromAssociationRow(row), p.Role,
			row.Title, roles, row.AssociationPhone, row.AssociationEmail, act.Kind, act.Display, act.UserID); err != nil {
			return err
		}
	}
	return nil
}
```

Finally, the two list handlers answer `title` and `roles`, each loading every role row of the page in one query:

`GetCustomersByIdContacts` — after `rows, err := q.ListContactAssociationsForCustomer(...)`:

```go
	roleRows, err := q.ContactRolesForCustomer(ctx, req.Id)
	if err != nil {
		return nil, fmt.Errorf("customers: list customer contact roles: %w", err)
	}
	// One query for the whole list, never one per row (design D3), grouped the
	// way CustomerTagsForCustomers' answer is: the rows arrive in the fixed
	// role order already, so appending preserves it.
	byContact := make(map[int32][]contactRole, len(rows))
	for _, r := range roleRows {
		byContact[r.ContactID] = append(byContact[r.ContactID], contactRole{Role: r.Role, Primary: r.IsPrimary})
	}

	data := make([]gen.CustomerContactResponse, 0, len(rows))
	for _, r := range rows {
		data = append(data, gen.CustomerContactResponse{
			Contact: gen.ContactResponse{
				Id: r.ID, FirstName: r.FirstName, LastName: r.LastName,
				MiddleName: r.MiddleName, Prefix: r.Prefix, Suffix: r.Suffix, Phone: r.ContactPhone, Email: r.ContactEmail,
			},
			Role: deref(r.Title), Title: r.Title, Roles: genContactRoles(byContact[r.ID]),
			Phone: r.AssociationPhone, Email: r.AssociationEmail,
		})
	}
```

`GetCustomersContactsByIdCustomers` — the mirror, keyed by customer:

```go
	roleRows, err := q.ContactRolesForContact(ctx, req.Id)
	if err != nil {
		return nil, fmt.Errorf("customers: list contact roles: %w", err)
	}
	byCustomer := make(map[int32][]contactRole, len(rows))
	for _, r := range roleRows {
		byCustomer[r.CustomerID] = append(byCustomer[r.CustomerID], contactRole{Role: r.Role, Primary: r.IsPrimary})
	}

	data := make([]gen.GetContactCustomersContactCustomerResponse, 0, len(rows))
	for _, r := range rows {
		data = append(data, gen.GetContactCustomersContactCustomerResponse{
			Customer: gen.GetContactCustomersCustomerReference{Id: r.ID, CustomerNumber: r.CustomerNumber, Name: r.Name},
			Role:     deref(r.Title), Title: r.Title, Roles: genContactRoles(byCustomer[r.ID]),
			Phone:    r.Phone, Email: r.Email,
		})
	}
```

- [ ] **Step 10: Compile and fix the four pre-existing payload assertions**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- go vet ./... && mise exec -- go test -count=1 ./internal/customers/
```
Four tests in `contacts_test.go` assert the event payload with `reflect.DeepEqual` and now see two extra keys. Add them to each `wantPayload`, in the shape the recorder writes (`title` is a JSON string or `nil`; `roles` decodes to `[]any`):

- `TestAttachContact_RecordsTimelineEvent` (~line 882): add `"title": "CEO",` and `"roles": []any{},`
- `TestUpdateCustomerContact_RecordsRelationshipUpdatedEvent` (~line 924): add `"title": "Chairman",` and `"roles": []any{},`
- `TestDetachContact_RecordsTimelineEvent` and `TestDeleteContact_RecordsRemovedTimelineEvent`: the same two keys with that test's own title value and `[]any{}`.

Everything else in the file stays: `attachContact` still sends `role`, the corpus's field name, which is exactly the compatibility this delivery promises.

- [ ] **Step 11: Run Step 5's tests and watch them turn green**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- go test -count=1 -run 'TestAttachContact_First|TestAttachContact_Primary|TestUpdateCustomerContact_(Clearing|Losing|Omitted|Explicit|RecordsAnEvent)|TestAssociationRequests|TestGetContactCustomers_Carries|TestDetachContact_Promotes|TestDeleteContact_Promotes' ./internal/customers/
```
Expected: PASS, every case that Step 5 saw fail. This is the same command Step 5 ran; comparing the two runs is the proof that each case pins something the implementation does and not something it happened to do already.

- [ ] **Step 12: Run Step 6's concurrency file green, pinned to four CPUs**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
taskset -c 0-3 mise exec -- go test -count=5 -run 'TestPutAssociation_Concurrent|TestDetachTheOnlyHolder|TestAttachAndDeleteContact_CrossedLockOrders|TestPartialIndexIsTheBackstop' ./internal/customers/
```
Expected: PASS every time. The machine has far more cores than CI; `taskset -c 0-3` is what this project uses to make timing look like CI's. The three race cases are **not** `t.Parallel()` (the backstop is), because a lock gate and a parallel sibling holding the same customer row are two different experiments.

- [ ] **Step 13: The whole package green, and gofmt'd**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- gofmt -l ./internal/customers && mise exec -- go vet ./... && mise exec -- go test -count=1 ./internal/customers/... ./internal/openapi/... ./internal/module/... ./internal/db/...
```
`gofmt -l` must print nothing before the commit. **Every Go listing in this plan is written for readability, not to gofmt's exact alignment** — struct-literal field alignment and comment wrapping in particular — so run `mise exec -- gofmt -w ./internal/customers` if it names a file, and commit the formatted version. `golangci-lint` (Task 6) fails on unformatted code, so this is not a stylistic nicety.

- [ ] **Step 14: Commit**

```bash
cd /home/anders/projects/vantigo/vantigo
printf '%s\n\n%s\n\n%s\n' 'feat(customers): a customer has a primary billing, project and decision-making contact' 'Roles ride on the association'"'"'s own endpoints: the first holder of a role is its primary whatever the request said, primary:true demotes the incumbent in the same transaction, clearing the only or primary holder'"'"'s flag is refused, and losing a role promotes the longest-standing remaining holder — on an update, a detach and a deleted contact alike. Every write takes the customer row'"'"'s FOR NO KEY UPDATE lock first and retries a deadlock with the contact delete'"'"'s opposite lock order; the partial unique index stays the backstop. A promotion is recorded on the promoted contact, with the user who caused it.' 'Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>' > /tmp/msg-roles-task3
git add apps/server/internal/customers/contact_roles.go apps/server/internal/customers/contact_roles_test.go apps/server/internal/customers/contact_roles_concurrency_test.go apps/server/internal/customers/contacts.go apps/server/internal/customers/contacts_timeline.go apps/server/internal/customers/contacts_test.go apps/server/internal/customers/values.go apps/server/internal/customers/values_test.go
git commit -F /tmp/msg-roles-task3 -- $(git diff --cached --name-only)
git show --stat HEAD && git status --short
```

- [ ] **Step 15: Show five of the new tests can fail**

Each by removing exactly the guard it claims to pin, running it, seeing red, and restoring:
1. `TestAttachContact_FirstHolderOfARoleIsPrimaryWhateverItAsked` — delete `if holders == 0 { primary = true }` from `applyRoles`' new-role branch.
2. `TestUpdateCustomerContact_LosingAPrimaryRolePromotesTheLongestStandingHolder` — change `OldestContactRoleHolder`'s `ORDER BY created_at, contact_id` to `ORDER BY contact_id` and regenerate.
3. `TestPutAssociation_ConcurrentPrimaryTrue_ExactlyOneWinner` — delete the `LockCustomer` call from the update handler's transaction.
4. `TestUpdateCustomerContact_OmittedPrimaryKeepsTheFlagInAReplace` and `TestUpdateCustomerContact_ExplicitPrimaryFalseInAReplaceIsRefused` together — collapse the three-valued flag by changing `requestedRole.Primary` to a plain `bool` (`Primary: r.Primary != nil && *r.Primary` in validation, `!r.Primary` in phase 1, `r.Primary` everywhere else). Exactly one of the two must go red: the omitted case now 400s on a demotion it never asked for. Restore.
5. `TestAttachAndDeleteContact_CrossedLockOrders` — remove `db.RetrySerializable` from `PostCustomersByIdContacts` (call `db.WithTx` directly) and run it with `-count=20`; a 500 from the 40P01 victim must appear. Restore. If twenty runs never reach the cycle branch, say so in the report rather than claiming the proof — the branch is the database's choice, not the test's.
Note all five in the report, with what each failure said.

---

### Task 4: The frontend — Title, the Roles group and the badges (D5)

**Files:**
- Create: `apps/customers/frontend/src/lib/contact-role-label.ts`, `src/lib/contact-role-label.test.ts`, `src/components/contact-role-badges.tsx`, `src/components/contact-role-badges.test.tsx`
- Modify: `src/api/contacts.ts`, `src/api/contacts.test.ts`, `src/pages/-connection.tsx`, `src/pages/-customer-contacts-card.tsx`, `src/pages/contacts.$contactId.tsx`, `src/pages/-contacts.test.tsx`, `src/pages/-contact-details.test.tsx`, `src/i18n.ts`
- Read first (do not change): `src/api/tags.ts` (the `Raw…` + `normalize…` boundary convention this copies), `src/components/tag-badge.tsx` (a one-badge component's shape), `src/lib/address-type-label.ts` (a code → label helper with a raw-code fallback), `src/pages/-customer-address-modal.tsx:224-231` (a `Checkbox` that is forced on with a reason), `src/test/fetch.ts` (`stubFetch` returns the business mock)

**Interfaces:**
- Consumes: Task 2's wire shape — `title` (absent when null), `roles: [{role, primary}]` (always present), and requests carrying `title` + `roles`.
- Produces:
  - `src/api/contacts.ts`: `export const CONTACT_ROLES = ["billing", "project", "decision_maker"] as const`, `export type ContactRoleName = (typeof CONTACT_ROLES)[number]`, `export interface ContactRoleAssignment { role: string; primary: boolean }`, `export interface ContactRoleInput { role: string; primary?: boolean }`, `export const normalizeContactRoles: (raw?: RawContactRole[] | null) => ContactRoleAssignment[]`; `title: string | null` and `roles: ContactRoleAssignment[]` on `CustomerContactResponse` and `ContactCustomerResponse`; `CustomerContactInput = { title?: string; roles?: ContactRoleInput[]; phone?: string; email?: string }`
  - `src/lib/contact-role-label.ts`: `export const contactRoleLabel = (t: (key: string) => string, role: string): string`
  - `src/components/contact-role-badges.tsx`: `export const ContactRoleBadges = ({ roles }: { roles: ContactRoleAssignment[] }) => JSX.Element | null`
  - `src/pages/-connection.tsx`: `ConnectionFormValues = { title: string; roles: Record<string, boolean>; primary: Record<string, boolean>; phone: string; email: string }`, `ConnectionFields({ getInputProps, setFieldValue, values, lockedPrimary, soleRoles })`, `EditConnectionTarget` gaining `title: string | null`, `roles: ContactRoleAssignment[]`, `soleRoles: string[]`, `toRoleInputs(values)`

- [ ] **Step 1: Write the failing label and badge tests**

`src/lib/contact-role-label.test.ts`:

```ts
import { describe, expect, it } from "vitest";
import { contactRoleLabel } from "./contact-role-label";

describe("contactRoleLabel", () => {
  const t = (key: string) => `t:${key}`;

  it("maps each of the three codes to its own catalog key", () => {
    expect(contactRoleLabel(t, "billing")).toBe("t:roleBilling");
    expect(contactRoleLabel(t, "project")).toBe("t:roleProject");
    expect(contactRoleLabel(t, "decision_maker")).toBe("t:roleDecisionMaker");
  });

  it("falls back to the raw code for a role the catalog does not know", () => {
    // The vocabulary is a value change on the server, not a migration, so a
    // widened list reaches an older frontend. Showing the code is worse than
    // showing a name and far better than showing nothing.
    expect(contactRoleLabel(t, "executive_sponsor")).toBe("executive_sponsor");
  });
});
```

`src/components/contact-role-badges.test.tsx`:

```tsx
import { MantineProvider } from "@mantine/core";
import { cleanup, render, screen } from "@testing-library/react";
import { setLanguagePreference } from "@vantigo/frontend-shell";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { ContactRoleBadges } from "./contact-role-badges";
import "../i18n";

const renderBadges = (roles: { role: string; primary: boolean }[]) =>
  render(
    <MantineProvider>
      <ContactRoleBadges roles={roles} />
    </MantineProvider>,
  );

describe("ContactRoleBadges", () => {
  // The language is a module-level preference, so it is reset between cases the
  // way legal-badges.test.tsx resets it — otherwise the nb case below leaks into
  // whatever runs after it.
  beforeEach(() => setLanguagePreference("auto"));
  afterEach(cleanup);

  it("renders one badge per role with its catalog label", () => {
    renderBadges([
      { role: "billing", primary: true },
      { role: "decision_maker", primary: false },
    ]);

    expect(screen.getByText("Billing")).toBeInTheDocument();
    expect(screen.getByText("Decision maker")).toBeInTheDocument();
  });

  it("marks the primary role and says so where a reader can find it", () => {
    renderBadges([
      { role: "billing", primary: true },
      { role: "project", primary: false },
    ]);

    // The star is decoration; the sentence is the accessible answer, so it is
    // the one asserted. Two badges, one of them labelled.
    expect(screen.getByLabelText("Primary billing contact")).toBeInTheDocument();
    expect(screen.queryByLabelText("Primary project contact")).not.toBeInTheDocument();
  });

  it("localizes the labels and the primary sentence", async () => {
    // The nb catalog is not proved by translations:check, which only proves the
    // two catalogs have the same keys — this proves the Norwegian strings are
    // the ones that actually render, including the interpolated role name inside
    // the primary sentence, which is the one string a missing placeholder would
    // break silently. setLanguagePreference is frontend-shell's own seam, used
    // exactly as legal-badges.test.tsx uses it.
    setLanguagePreference("nb");
    renderBadges([
      { role: "billing", primary: true },
      { role: "decision_maker", primary: false },
    ]);

    expect(await screen.findByText("Faktura")).toBeInTheDocument();
    expect(screen.getByText("Beslutningstaker")).toBeInTheDocument();
    expect(screen.getByLabelText("Primær faktura-kontakt")).toBeInTheDocument();
  });

  it("renders no badge at all for a contact with no roles", () => {
    // Not `toBeEmptyDOMElement`: MantineProvider always injects a <style>
    // element of its own, so the container is never literally empty. What the
    // component promises is that it contributes nothing, which is "no badge".
    const { container } = renderBadges([]);
    expect(container.querySelector(".mantine-Badge-root")).toBeNull();
    expect(screen.queryByText("Billing")).not.toBeInTheDocument();
    expect(screen.queryByText("Project")).not.toBeInTheDocument();
    expect(screen.queryByText("Decision maker")).not.toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run them and watch them fail**

```bash
cd /home/anders/projects/vantigo/vantigo
mise exec -- bun run --cwd apps/customers/frontend test -- src/lib/contact-role-label.test.ts src/components/contact-role-badges.test.tsx
```
Expected: FAIL — the two modules do not exist.

- [ ] **Step 3: Write the label helper and the badges**

`src/lib/contact-role-label.ts`:

```ts
/**
 * The label for one typed contact role (typed contact roles design D2), in this
 * package's own catalog — the same shape `addressTypeLabel` has, including the
 * fallback: the role vocabulary is a value change on the server rather than a
 * migration, so a widened list can reach a frontend whose catalog predates it,
 * and showing the code is the honest answer to that.
 */
export const contactRoleLabel = (t: (key: string) => string, role: string): string =>
  role === "billing"
    ? t("roleBilling")
    : role === "project"
      ? t("roleProject")
      : role === "decision_maker"
        ? t("roleDecisionMaker")
        : role;
```

`src/components/contact-role-badges.tsx`:

```tsx
import { Badge, Group, Tooltip } from "@mantine/core";
import { IconStarFilled } from "@tabler/icons-react";
import { useI18n } from "@vantigo/frontend-shell";
import type { ContactRoleAssignment } from "../api/contacts";
import { contactRoleLabel } from "../lib/contact-role-label";
import "../i18n";

/**
 * A contact's typed roles for one customer, as chips (design D5) — the same
 * one-component treatment `TagBadge` gets, and for the same reason: the
 * customer's contacts card and the contact page's customers card show the
 * identical thing, and two copies of three props drift.
 *
 * The primary one carries a star and, more importantly, an `aria-label`
 * spelling out what the star means — "Primary billing contact". A tooltip alone
 * would leave the fact to a hover, which is not where anyone using a screen
 * reader or a phone will find it.
 *
 * The array arrives in the fixed order billing, project, decision_maker (the
 * server sorts it, and `normalizeContactRoles` sorts it again), so nothing here
 * orders anything.
 */
export const ContactRoleBadges = ({ roles }: { roles: ContactRoleAssignment[] }) => {
  const { t } = useI18n("customers");
  if (roles.length === 0) return null;

  return (
    <Group gap={4} wrap="wrap">
      {roles.map((assignment) => {
        const label = contactRoleLabel(t, assignment.role);
        if (!assignment.primary) {
          return (
            <Badge key={assignment.role} variant="light" color="gray" size="sm" tt="none" fw={500}>
              {label}
            </Badge>
          );
        }
        const primaryLabel = t("primaryRoleFor", { role: label.toLocaleLowerCase() });
        return (
          <Tooltip key={assignment.role} label={primaryLabel}>
            <Badge
              variant="light"
              color="blue"
              size="sm"
              tt="none"
              fw={500}
              aria-label={primaryLabel}
              leftSection={<IconStarFilled size={10} />}
            >
              {label}
            </Badge>
          </Tooltip>
        );
      })}
    </Group>
  );
};
```

- [ ] **Step 4: Add the catalog keys, both languages**

In `apps/customers/frontend/src/i18n.ts`, replace the three now-dead keys in the **en** block (around lines 109-113) — first confirm nothing else uses them:

```bash
cd /home/anders/projects/vantigo/vantigo
grep -rn 't("role")\|t("rolePlaceholder")\|t("roleRequired")' apps/customers/frontend/src apps/host/frontend/src packages
```
Expected after Steps 5-7: no hits. Then, in **en**, replace

```ts
  role: "Role",
  rolePlaceholder: "e.g. CEO",
  connectionPhoneDescription: "Specific to this customer connection",
  connectionEmailDescription: "Specific to this customer connection",
  roleRequired: "Role is required",
```

with

```ts
  contactTitle: "Title",
  contactTitlePlaceholder: "e.g. CEO",
  connectionPhoneDescription: "Specific to this customer connection",
  connectionEmailDescription: "Specific to this customer connection",
  titleOrRoleRequired: "Give a title or pick at least one role",
  contactRoles: "Roles",
  contactRolesDescription: "What this person does for this customer",
  roleBilling: "Billing",
  roleProject: "Project",
  roleDecisionMaker: "Decision maker",
  primaryRoleFor: "Primary {{role}} contact",
  roleOnlyHolder: "Already the only holder",
  rolePrimaryStays: "The primary holder stays primary — make another contact primary instead",
```

and the matching **nb** block (around lines 593-597):

```ts
  contactTitle: "Tittel",
  contactTitlePlaceholder: "f.eks. CEO",
  connectionPhoneDescription: "Gjelder denne kundekoblingen",
  connectionEmailDescription: "Gjelder denne kundekoblingen",
  titleOrRoleRequired: "Oppgi en tittel eller velg minst én rolle",
  contactRoles: "Roller",
  contactRolesDescription: "Hva denne personen gjør for denne kunden",
  roleBilling: "Faktura",
  roleProject: "Prosjekt",
  roleDecisionMaker: "Beslutningstaker",
  primaryRoleFor: "Primær {{role}}-kontakt",
  roleOnlyHolder: "Allerede den eneste innehaveren",
  rolePrimaryStays: "Den primære innehaveren forblir primær — gjør en annen kontakt primær i stedet",
```

`primaryBadge` ("Primary" / "Primær") already exists and is what the Primary switch is labelled with — no new key for it.

- [ ] **Step 5: Run the label and badge tests and watch them pass**

```bash
cd /home/anders/projects/vantigo/vantigo
mise exec -- bun run --cwd apps/customers/frontend test -- src/lib/contact-role-label.test.ts src/components/contact-role-badges.test.tsx
mise exec -- bun run translations:check && mise exec -- bun run i18n:test
```

- [ ] **Step 6: Write the failing api-client test, then the client**

Append to `src/api/contacts.test.ts`:

```ts
  it("reads the title out of a response that only has role, the way the corpus answers", async () => {
    // `role` is the title under its old name (design D1), so a response with no
    // `title` key must still show one — this is what keeps the contacts card's
    // title line working against the shape the recorded corpus answers.
    stubFetch(() =>
      Promise.resolve(
        new Response(
          JSON.stringify({
            data: [
              {
                contact: { id: 1001, firstName: "A", lastName: "B", middleName: null, prefix: null, suffix: null, phone: null, email: null },
                role: "CTO",
                phone: null,
                email: null,
              },
            ],
          }),
          { status: 200, headers: { "Content-Type": "application/json" } },
        ),
      ),
    );

    const answered = await customerContactsQueryOptions(2002).queryFn!({ signal: undefined } as never);
    expect(answered.data[0].title).toBe("CTO");
    expect(answered.data[0].roles).toEqual([]);
  });

  it("normalises roles: absent is empty, primary defaults to false, and the fixed order is restored", () => {
    // The server sorts, but a cached response from an older version, or any
    // future widening, must not decide what the UI's order is.
    expect(normalizeContactRoles(undefined)).toEqual([]);
    expect(normalizeContactRoles(null)).toEqual([]);
    expect(
      normalizeContactRoles([
        { role: "decision_maker", primary: true },
        { role: "billing" },
        { role: "executive_sponsor", primary: true },
        { role: "project", primary: false },
      ]),
    ).toEqual([
      { role: "billing", primary: false },
      { role: "project", primary: false },
      { role: "decision_maker", primary: true },
      { role: "executive_sponsor", primary: true },
    ]);
  });

  it("sends title and the complete roles on an attach, and reads title back as null when absent", async () => {
    const fetchMock = stubFetch(() =>
      Promise.resolve(
        new Response(
          JSON.stringify({
            contact: { id: 1001, firstName: "A", lastName: "B", middleName: null, prefix: null, suffix: null, phone: null, email: null },
            role: "",
            roles: [{ role: "billing", primary: true }],
          }),
          { status: 200, headers: { "Content-Type": "application/json" } },
        ),
      ),
    );

    const answered = await attachCustomerContact(2002, {
      contactId: 1001,
      roles: [{ role: "billing", primary: true }],
    });

    // role is "" and title is absent, so the title is genuinely none — not "".
    expect(answered.title).toBeNull();
    expect(answered.roles).toEqual([{ role: "billing", primary: true }]);
    // `actualCalls`, not `.mock.calls`: `stubFetch` (src/test/fetch.ts) returns
    // the business mock with `calls` (everything, session bootstrap included)
    // and `actualCalls` (everything but the bootstrap) as plain arrays on it —
    // it is not a vi.fn wrapper with a `.mock` of its own. Filtering by method
    // and URL rather than taking the last call, as the Global Constraints require.
    const attach = fetchMock.actualCalls.find(
      ([url, init]) => String(url) === "/api/v1/customers/2002/contacts" && init?.method === "POST",
    );
    expect(JSON.parse((attach?.[1] as RequestInit).body as string)).toEqual({
      contactId: 1001,
      roles: [{ role: "billing", primary: true }],
    });
  });
```

Add `attachCustomerContact`, `customerContactsQueryOptions` and `normalizeContactRoles` to the file's import list, and `stubFetch` if it is not already imported (read the file's existing imports first — the other cases in it already stub fetch, and this uses the same helper rather than a second one). If the existing cases call the query function differently from `queryOptions(…).queryFn!({ signal: undefined } as never)`, follow the file's own way of invoking one; the assertion is what matters, not the invocation style.

Then, in `src/api/contacts.ts`, add above `CustomerContactResponse`:

```ts
/**
 * The typed role vocabulary (typed contact roles design D2). The server
 * validates against exactly these three, so a checkbox group can be built from
 * this array and a role that arrives outside it is still rendered (see
 * `contactRoleLabel`) rather than dropped.
 */
export const CONTACT_ROLES = ["billing", "project", "decision_maker"] as const;

export type ContactRoleName = (typeof CONTACT_ROLES)[number];

/** One role a contact holds for a customer, and whether it is the primary holder. */
export interface ContactRoleAssignment {
  role: string;
  primary: boolean;
}

/** One role to give — `primary` omitted means false. */
export interface ContactRoleInput {
  role: string;
  primary?: boolean;
}

type RawContactRole = { role: string; primary?: boolean };

/**
 * `roles` is always sent by the server and `primary` is always present in it,
 * but the contract keeps both optional because the recorded exchange corpus
 * predates them — so absent and null mean "none", and a missing `primary` means
 * false. This is the one place that is decided, the same treatment `color` gets
 * in `tags.ts`.
 *
 * The order is restored rather than trusted: the API answers billing, project,
 * decision_maker, and re-sorting here means a component never has to know
 * whether what it holds came from a response, a cache or a future server.
 */
export const normalizeContactRoles = (raw?: RawContactRole[] | null): ContactRoleAssignment[] =>
  (raw ?? [])
    .map((r) => ({ role: r.role, primary: r.primary ?? false }))
    .sort((a, b) => roleRank(a.role) - roleRank(b.role));

const roleRank = (role: string) => {
  const index = (CONTACT_ROLES as readonly string[]).indexOf(role);
  return index === -1 ? CONTACT_ROLES.length : index;
};
```

Change the two response interfaces and the input, and normalise at every boundary:

```ts
export interface CustomerContactResponse {
  contact: ContactResponse;
  /** The association's title, or "" — kept for compatibility; read `title`. */
  role: string;
  /** What this person is called at this customer, or null. */
  title: string | null;
  /** Every typed role held for this customer, in the fixed order. */
  roles: ContactRoleAssignment[];
  /** Connection-specific phone, when it differs from the contact's own. */
  phone: string | null;
  /** Connection-specific email, when it differs from the contact's own. */
  email: string | null;
}

export interface CustomerContactInput {
  title?: string;
  /** The complete set of roles to hold. Omitted leaves them unchanged. */
  roles?: ContactRoleInput[];
  phone?: string;
  email?: string;
}

export interface ContactCustomerResponse {
  customer: { id: number; name: string };
  role: string;
  title: string | null;
  roles: ContactRoleAssignment[];
  phone: string | null;
  email: string | null;
}

type RawCustomerContactResponse = Omit<CustomerContactResponse, "title" | "roles"> & {
  title?: string | null;
  roles?: RawContactRole[] | null;
};
type RawContactCustomerResponse = Omit<ContactCustomerResponse, "title" | "roles"> & {
  title?: string | null;
  roles?: RawContactRole[] | null;
};

/**
 * `title` absent falls back to `role`, which is not a guess: `role` IS the title
 * on the wire (design D1), so a response from a server or a cache that predates
 * `title` carries the title under the old name. Reading it here is what lets
 * every component downstream read `title` alone and never think about which
 * version answered — and `role: ""`, which is what the server sends for an
 * association with no title, becomes null rather than an empty string.
 */
const titleOf = (raw: { title?: string | null; role: string }): string | null => raw.title ?? (raw.role || null);

const normalizeCustomerContact = (raw: RawCustomerContactResponse): CustomerContactResponse => ({
  ...raw,
  title: titleOf(raw),
  roles: normalizeContactRoles(raw.roles),
});

const normalizeContactCustomer = (raw: RawContactCustomerResponse): ContactCustomerResponse => ({
  ...raw,
  title: titleOf(raw),
  roles: normalizeContactRoles(raw.roles),
});
```

and the four call sites:

```ts
export const customerContactsQueryOptions = (customerId: number) =>
  queryOptions({
    queryKey: ["customers", customerId, "contacts"],
    queryFn: async ({ signal }) => {
      const answered = await request<{ data: RawCustomerContactResponse[] }>(
        `/api/v1/customers/${customerId}/contacts`,
        { signal },
      );
      return { data: answered.data.map(normalizeCustomerContact) };
    },
  });

export const contactCustomersQueryOptions = (contactId: number) =>
  queryOptions({
    queryKey: ["contacts", contactId, "customers"],
    queryFn: async ({ signal }) => {
      const answered = await request<{ data: RawContactCustomerResponse[] }>(
        `/api/v1/customers/contacts/${contactId}/customers`,
        { signal },
      );
      return { data: answered.data.map(normalizeContactCustomer) };
    },
  });

export const attachCustomerContact = async (customerId: number, input: CustomerContactInput & { contactId: number }) =>
  normalizeCustomerContact(
    await request<RawCustomerContactResponse>(`/api/v1/customers/${customerId}/contacts`, jsonBody("POST", input)),
  );

export const updateCustomerContact = (customerId: number, contactId: number, input: CustomerContactInput) =>
  request<RawCustomerContactResponse>(`/api/v1/customers/${customerId}/contacts/${contactId}`, jsonBody("PUT", input))
    .then(normalizeCustomerContact)
    .catch((error: unknown) => {
      if ((error as { status?: number }).status === 404)
        throw new NotFoundError("The requested contact association does not exist");
      throw error;
    });
```

- [ ] **Step 7: Extend the two route test files, and watch them fail**

`src/pages/-contacts.test.tsx`'s `describe("customer contacts card")` has four cases: `"lists associated contacts with connection values falling back to the contact's own"` (the only one whose contacts fixture carries data), `"shows an empty state when the customer has no contacts"`, `"attaches an existing contact found through the search"` and `"offers to create a new contact when the search finds nothing"`. Three of them need changing:

These are written **before** Steps 8 and 9 touch any component, so they fail on the UI as it is today: there is no Title input to type into, no `checkbox`/`switch` to click, no badge to find, and the attach body still carries `role`. That is the red this plan wants.

**(a) Adapt the two existing attach cases**, which type into a `/role/i` label that is about to stop existing and assert bodies that are about to stop matching. In `"attaches an existing contact found through the search"` (lines ~222 and ~227):

```tsx
    await userEvent.type(within(modal).getByLabelText(/^title$/i), "CEO");
    await userEvent.click(within(modal).getByRole("button", { name: /^add contact$/i }));

    await waitFor(() => expect(attachSpy).toHaveBeenCalled());
    const body = JSON.parse((attachSpy.mock.calls[0][0] as RequestInit).body as string);
    expect(body).toEqual({ contactId: 1001, title: "CEO", roles: [] });
```

and in `"offers to create a new contact when the search finds nothing"` (lines ~266 and ~276):

```tsx
    await userEvent.type(within(modal).getByLabelText(/^title$/i), "Custodian");
```
```tsx
    const attachBody = JSON.parse((attachSpy.mock.calls[0][0] as RequestInit).body as string);
    expect(attachBody).toEqual({ contactId: 1005, title: "Custodian", roles: [] });
```

Both keep their `role: "CEO"` / `role: "Custodian"` **response** fixtures: the server still answers `role`, and leaving them proves the card reads a response that carries neither `title` nor `roles`.

`"shows an empty state when the customer has no contacts"` needs nothing — its fixture is `{ data: [] }`.

**(b) Replace `"lists associated contacts with connection values falling back to the contact's own"`** with the title-and-badges case below (it keeps the connection-fallback assertions it had, and gains the two new columns), **and (c) add the four new cases** after it — one of the fixtures deliberately **without** `title`/`roles`, as the corpus-shaped body:

```tsx
  it("shows the title under the name, a starred badge for the primary role, and the connection fallbacks", async () => {
    stubFetch({
      "GET /api/v1/customers/2002": () => jsonResponse(200, customer),
      "GET /api/v1/customers/2002/billing-profile": () => jsonResponse(200, emptyBillingProfile),
      "GET /api/v1/customers/2002/addresses": () => jsonResponse(200, { data: [] }),
      "GET /api/v1/customers/2002/contacts": () =>
        jsonResponse(200, {
          data: [
            {
              contact: contact(1001, "Anders", "Refsdal", { email: "anders@personal.no" }),
              role: "CEO",
              title: "CEO",
              roles: [
                { role: "billing", primary: true },
                { role: "project", primary: false },
              ],
              phone: "+47 11 22 33 44",
              email: null,
            },
            // Literally the shape the recorded corpus answers: role and
            // nothing else. The card must render it without a title line and
            // without badges, not crash on a missing array.
            { contact: contact(1002, "Kari", "Nordmann"), role: "CTO", phone: null, email: null },
          ],
        }),
    });

    await renderRoute("/customers/2002", "Refsdal Holding");

    expect(await screen.findByText("Anders Refsdal")).toBeInTheDocument();
    expect(screen.getByText("CEO")).toBeInTheDocument();
    expect(screen.getByLabelText("Primary billing contact")).toBeInTheDocument();
    expect(screen.getByText("Project")).toBeInTheDocument();
    expect(screen.queryByLabelText("Primary project contact")).not.toBeInTheDocument();
    // The connection fallbacks this case has always pinned: the
    // connection-specific phone plainly, the inherited email dimmed.
    expect(screen.getByText("+47 11 22 33 44")).toBeInTheDocument();
    expect(screen.getByText("anders@personal.no")).toBeInTheDocument();
    // The corpus-shaped row: its title still shows (role is the title), and it
    // has no badges of its own.
    expect(screen.getByText("Kari Nordmann")).toBeInTheDocument();
    expect(screen.getByText("CTO")).toBeInTheDocument();
  });

  it("attaches with a title and the roles that were ticked, the first one primary", async () => {
    const attachSpy = vi.fn<(init?: RequestInit) => Response>(() =>
      jsonResponse(200, {
        contact: contact(1001, "Anders", "Refsdal"),
        role: "CEO",
        title: "CEO",
        roles: [{ role: "billing", primary: true }],
        phone: null,
        email: null,
      }),
    );

    stubFetch({
      "GET /api/v1/customers/2002": () => jsonResponse(200, customer),
      "GET /api/v1/customers/2002/billing-profile": () => jsonResponse(200, emptyBillingProfile),
      "GET /api/v1/customers/2002/addresses": () => jsonResponse(200, { data: [] }),
      "GET /api/v1/customers/2002/contacts": () => jsonResponse(200, { data: [] }),
      "GET /api/v1/customers/contacts": () =>
        jsonResponse(200, paginated([{ contact: contact(1001, "Anders", "Refsdal"), customerCount: 0, customer: null }])),
      "POST /api/v1/customers/2002/contacts": attachSpy,
    });

    await renderRoute("/customers/2002", "Refsdal Holding");

    await userEvent.click(await screen.findByRole("button", { name: /add contact/i }));
    const modal = await screen.findByRole("dialog");
    await userEvent.type(within(modal).getByLabelText(/search for a contact/i), "anders");
    await userEvent.click(await screen.findByText("Anders Refsdal"));

    await userEvent.type(within(modal).getByLabelText(/^title$/i), "CEO");
    await userEvent.click(within(modal).getByRole("checkbox", { name: "Billing" }));
    await userEvent.click(within(modal).getByRole("switch", { name: "Primary billing contact" }));
    await userEvent.click(within(modal).getByRole("button", { name: /^add contact$/i }));

    await waitFor(() => expect(attachSpy).toHaveBeenCalled());
    const body = JSON.parse((attachSpy.mock.calls[0][0] as RequestInit).body as string);
    expect(body).toEqual({
      contactId: 1001,
      title: "CEO",
      roles: [{ role: "billing", primary: true }],
    });
  });

  it("refuses to submit an attach with neither a title nor a role", async () => {
    const attachSpy = vi.fn<(init?: RequestInit) => Response>(() => jsonResponse(200, {}));
    stubFetch({
      "GET /api/v1/customers/2002": () => jsonResponse(200, customer),
      "GET /api/v1/customers/2002/billing-profile": () => jsonResponse(200, emptyBillingProfile),
      "GET /api/v1/customers/2002/addresses": () => jsonResponse(200, { data: [] }),
      "GET /api/v1/customers/2002/contacts": () => jsonResponse(200, { data: [] }),
      "GET /api/v1/customers/contacts": () =>
        jsonResponse(200, paginated([{ contact: contact(1001, "Anders", "Refsdal"), customerCount: 0, customer: null }])),
      "POST /api/v1/customers/2002/contacts": attachSpy,
    });

    await renderRoute("/customers/2002", "Refsdal Holding");
    await userEvent.click(await screen.findByRole("button", { name: /add contact/i }));
    const modal = await screen.findByRole("dialog");
    await userEvent.type(within(modal).getByLabelText(/search for a contact/i), "anders");
    await userEvent.click(await screen.findByText("Anders Refsdal"));
    await userEvent.click(within(modal).getByRole("button", { name: /^add contact$/i }));

    expect(await within(modal).findByText(/give a title or pick at least one role/i)).toBeInTheDocument();
    expect(attachSpy).not.toHaveBeenCalled();
  });

  it("disables the Primary switch of a role the contact is the only holder of, and says why", async () => {
    stubFetch({
      "GET /api/v1/customers/2002": () => jsonResponse(200, customer),
      "GET /api/v1/customers/2002/billing-profile": () => jsonResponse(200, emptyBillingProfile),
      "GET /api/v1/customers/2002/addresses": () => jsonResponse(200, { data: [] }),
      "GET /api/v1/customers/2002/contacts": () =>
        jsonResponse(200, {
          data: [
            {
              contact: contact(1001, "Anders", "Refsdal"),
              role: "CEO",
              title: "CEO",
              roles: [{ role: "billing", primary: true }],
              phone: null,
              email: null,
            },
            {
              contact: contact(1002, "Kari", "Nordmann"),
              role: "CTO",
              title: "CTO",
              roles: [{ role: "project", primary: true }],
              phone: null,
              email: null,
            },
          ],
        }),
    });

    await renderRoute("/customers/2002", "Refsdal Holding");
    await userEvent.click(await screen.findByRole("button", { name: "Edit connection for Anders Refsdal" }));

    const modal = await screen.findByRole("dialog");
    const billingPrimary = within(modal).getByRole("switch", { name: "Primary billing contact" });
    expect(billingPrimary).toBeChecked();
    expect(billingPrimary).toBeDisabled();
    // Nobody else holds billing, so the reason is the specific one.
    expect(within(modal).getByText("Already the only holder")).toBeInTheDocument();
    // project is held by Kari, not by this contact, so its switch is merely
    // unticked-and-therefore-disabled, with no reason shown.
    expect(within(modal).getByRole("switch", { name: "Primary project contact" })).toBeDisabled();
    expect(within(modal).queryByText(/stays primary/i)).not.toBeInTheDocument();
  });

  it("sends the title and the complete role set when the connection is saved", async () => {
    const putSpy = vi.fn<(init?: RequestInit) => Response>(() =>
      jsonResponse(200, {
        contact: contact(1001, "Anders", "Refsdal"),
        role: "Chairman",
        title: "Chairman",
        roles: [
          { role: "billing", primary: true },
          { role: "decision_maker", primary: false },
        ],
        phone: null,
        email: null,
      }),
    );

    stubFetch({
      "GET /api/v1/customers/2002": () => jsonResponse(200, customer),
      "GET /api/v1/customers/2002/billing-profile": () => jsonResponse(200, emptyBillingProfile),
      "GET /api/v1/customers/2002/addresses": () => jsonResponse(200, { data: [] }),
      "GET /api/v1/customers/2002/contacts": () =>
        jsonResponse(200, {
          data: [
            {
              contact: contact(1001, "Anders", "Refsdal"),
              role: "CEO",
              title: "CEO",
              roles: [{ role: "billing", primary: true }],
              phone: null,
              email: null,
            },
          ],
        }),
      "PUT /api/v1/customers/2002/contacts/1001": putSpy,
    });

    await renderRoute("/customers/2002", "Refsdal Holding");
    await userEvent.click(await screen.findByRole("button", { name: "Edit connection for Anders Refsdal" }));

    const modal = await screen.findByRole("dialog");
    const title = within(modal).getByLabelText(/^title$/i);
    await userEvent.clear(title);
    await userEvent.type(title, "Chairman");
    await userEvent.click(within(modal).getByRole("checkbox", { name: "Decision maker" }));
    await userEvent.click(within(modal).getByRole("button", { name: /save changes/i }));

    await waitFor(() => expect(putSpy).toHaveBeenCalled());
    const body = JSON.parse((putSpy.mock.calls[0][0] as RequestInit).body as string);
    // billing carries `primary: true` because its switch is on (it is the
    // contact's primary billing role); decision_maker carries no `primary` key
    // at all, because an omitted flag is what "I did not ask about this one"
    // means on the wire — sending `false` would be the server's one refusal.
    expect(body).toEqual({
      title: "Chairman",
      roles: [{ role: "billing", primary: true }, { role: "decision_maker" }],
    });
  });
```

In `src/pages/-contact-details.test.tsx`, the customers-list fixture and the attach body gain the same two keys, plus one case for the per-customer badges:

```tsx
  it("shows each customer's title and role badges", async () => {
    stubFetch({
      "GET /api/v1/customers/contacts/1001": () => jsonResponse(200, anders),
      "GET /api/v1/customers/contacts/1001/customers": () =>
        jsonResponse(200, {
          data: [
            {
              customer: { id: 2002, customerNumber: 42, name: "Refsdal Holding" },
              role: "CEO",
              title: "CEO",
              roles: [{ role: "decision_maker", primary: true }],
              phone: null,
              email: null,
            },
          ],
        }),
    });

    await renderRoute("/customers/contacts/1001", "Dr. Anders Refsdal");

    expect(await screen.findByText("Refsdal Holding")).toBeInTheDocument();
    expect(screen.getByText("CEO")).toBeInTheDocument();
    expect(screen.getByLabelText("Primary decision maker contact")).toBeInTheDocument();
  });
```

and the existing `expect(body).toEqual({ contactId: 1001, role: "CEO" })` (line ~154) becomes `expect(body).toEqual({ contactId: 1001, title: "CEO", roles: [] })`, with the typing step changed from `getByLabelText(/role/i)` to `getByLabelText(/^title$/i)`.

Note the label regexes: `/^title$/i` and not `/title/i`, because the modal also has a `Roles` group whose accessible name contains no "title" but whose switches' do — an unanchored `/role/i` would match three switches and the group's own label.

Run both files now and record the failures:

```bash
cd /home/anders/projects/vantigo/vantigo
mise exec -- bun run --cwd apps/customers/frontend test -- src/pages/-contacts.test.tsx src/pages/-contact-details.test.tsx
```
Expected: FAIL — "unable to find a label with the text of: /^title$/i" and the attach bodies still carrying `role`. Step 10 re-runs the whole suite and expects PASS.

- [ ] **Step 8: Rewrite the shared connection fields and the edit modal**

In `src/pages/-connection.tsx`, replace `ConnectionFormValues`, `ConnectionFields`, `EditConnectionTarget` and the modal's form wiring:

```tsx
/**
 * The association's editable state, as the form holds it (design D5). The two
 * `Record`s are keyed by role code: `roles` is which checkboxes are ticked,
 * `primary` which of the ticked ones asked to be primary. Two flat maps rather
 * than an array of objects, because that is what a checkbox group and a switch
 * bind to without a reducer in between.
 */
export interface ConnectionFormValues {
  title: string;
  roles: Record<string, boolean>;
  primary: Record<string, boolean>;
  phone: string;
  email: string;
}

/** The empty form: no title, nothing ticked. */
export const emptyConnectionValues = (): ConnectionFormValues => ({
  title: "",
  roles: {},
  primary: {},
  phone: "",
  email: "",
});

/**
 * The form's roles as the API's `roles` array (design D3: the complete set to
 * hold), in the fixed order so the request looks the same whatever order the
 * boxes were ticked in.
 *
 * `primary` is sent only when the switch is ON, and OMITTED otherwise — the API's
 * three-valued flag, used as it is meant to be. An omitted flag says "leave this
 * role's primary as it is, or let the first-holder rule decide if it is new",
 * which is exactly what an untouched switch means; sending an explicit `false`
 * instead would be the server's one refusal (`primary: false` on the primary
 * holder), so a UI that echoed every switch would turn ticking a second role into
 * a 400. There is deliberately no way to demote from here: the server refuses it,
 * and the way to move a primary is to make another contact primary instead.
 */
export const toRoleInputs = (values: ConnectionFormValues): ContactRoleInput[] =>
  CONTACT_ROLES.filter((role) => values.roles[role]).map((role) =>
    values.primary[role] ? { role, primary: true } : { role },
  );

/**
 * The title-or-role rule the server enforces (design D1), checked here too so
 * the user is told before a round trip rather than after one. The message is
 * shown on the title, the field the server keys its own refusal to.
 */
export const validateConnectionValues = (t: (key: string) => string) => ({
  title: (value: string, values: ConnectionFormValues) =>
    value.trim().length === 0 && toRoleInputs(values).length === 0 ? t("titleOrRoleRequired") : null,
});

interface ConnectionFieldsProps {
  getInputProps: (path: string) => object;
  values: ConnectionFormValues;
  setFieldValue: (path: string, value: unknown) => void;
  /**
   * Roles this association currently holds AS primary, per the last read from
   * the server: clearing one is refused (design D2), so its switch is on and
   * disabled rather than offering a change that cannot happen.
   */
  lockedPrimary: string[];
  /**
   * Of those, the ones no other contact holds at all. The two cases get
   * different reasons because only one of them is true, and the contact page
   * cannot know which — it passes an empty array and gets the general wording.
   */
  soleRoles: string[];
}

export const ConnectionFields = ({ getInputProps, values, setFieldValue, lockedPrimary, soleRoles }: ConnectionFieldsProps) => {
  const { t } = useI18n("customers");
  return (
    <>
      <TextInput
        label={t("contactTitle")}
        placeholder={t("contactTitlePlaceholder")}
        {...getInputProps("title")}
      />
      <Input.Wrapper label={t("contactRoles")} description={t("contactRolesDescription")} error={getRolesError(getInputProps)}>
        <Stack gap="xs" mt="xs">
          {CONTACT_ROLES.map((role) => {
            const checked = values.roles[role] ?? false;
            const locked = lockedPrimary.includes(role);
            const reason = soleRoles.includes(role) ? t("roleOnlyHolder") : t("rolePrimaryStays");
            return (
              <Group key={role} justify="space-between" wrap="nowrap">
                <Checkbox
                  label={contactRoleLabel(t, role)}
                  checked={checked}
                  onChange={(event) => {
                    const next = event.currentTarget.checked;
                    setFieldValue(`roles.${role}`, next);
                    // Unticking a role also drops its primary request, so a
                    // re-tick does not silently carry the old one back.
                    if (!next) setFieldValue(`primary.${role}`, false);
                  }}
                />
                <Tooltip label={reason} disabled={!locked}>
                  <Switch
                    label={t("primaryBadge")}
                    aria-label={t("primaryRoleFor", { role: contactRoleLabel(t, role).toLocaleLowerCase() })}
                    labelPosition="left"
                    size="sm"
                    checked={locked || (values.primary[role] ?? false)}
                    disabled={!checked || locked}
                    description={locked ? reason : undefined}
                    onChange={(event) => setFieldValue(`primary.${role}`, event.currentTarget.checked)}
                  />
                </Tooltip>
              </Group>
            );
          })}
        </Stack>
      </Input.Wrapper>
      <Group grow>
        <TextInput label={t("phone")} description={t("connectionPhoneDescription")} {...getInputProps("phone")} />
        <TextInput label={t("email")} description={t("connectionEmailDescription")} {...getInputProps("email")} />
      </Group>
    </>
  );
};

/**
 * The server keys its role refusals to `roles`, which is a group here and not
 * an input, so its error is read off the form and shown on the wrapper — the
 * one place a `roles` message can land where a user will see it.
 */
const getRolesError = (getInputProps: (path: string) => object) =>
  (getInputProps("roles") as { error?: ReactNode }).error;

/** Identifies the association being edited plus its current values and modal title. */
export interface EditConnectionTarget {
  customerId: number;
  contactId: number;
  /** The name of the counterpart shown in the modal title. */
  counterpartName: string;
  title: string | null;
  roles: ContactRoleAssignment[];
  /** Roles this contact is the ONLY holder of, for the disabled switch's reason. */
  soleRoles: string[];
  phone: string | null;
  email: string | null;
}
```

Imports gained: `Checkbox`, `Input`, `Switch` from `@mantine/core`; `type ReactNode` from `"react"` (the file already imports `useEffect` from it — `getRolesError`'s return type needs the type, and `React.ReactNode` is not available because nothing here imports the `React` namespace); `CONTACT_ROLES`, `type ContactRoleAssignment`, `type ContactRoleInput` from `../api/contacts`; `contactRoleLabel` from `../lib/contact-role-label`. Write `getRolesError`'s cast as `{ error?: ReactNode }`, not `{ error?: React.ReactNode }`.

The modal's form, sync and mutation:

```tsx
  const form = useForm<ConnectionFormValues>({
    initialValues: emptyConnectionValues(),
    validate: validateConnectionValues(t),
  });

  const lockedPrimary = (target?.roles ?? []).filter((r) => r.primary).map((r) => r.role);

  useEffect(() => {
    if (target) {
      form.setValues({
        title: target.title ?? "",
        roles: Object.fromEntries(target.roles.map((r) => [r.role, true])),
        primary: Object.fromEntries(target.roles.map((r) => [r.role, r.primary])),
        phone: target.phone ?? "",
        email: target.email ?? "",
      });
      form.resetDirty();
      form.clearErrors();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [target]);

  const mutation = useMutation({
    mutationFn: (values: ConnectionFormValues) => {
      if (!target) {
        throw new Error(t("editConnection"));
      }
      return updateCustomerContact(target.customerId, target.contactId, {
        title: values.title.trim() || undefined,
        roles: toRoleInputs(values),
        phone: values.phone.trim() || undefined,
        email: values.email.trim() || undefined,
      });
    },
    // Keep the existing onSuccess and onError bodies verbatim: the
    // notification, the three invalidations and the ApiValidationError branch
    // are all unaffected by this change.
  });
```

and the render passes the four props:

```tsx
          <ConnectionFields
            getInputProps={form.getInputProps}
            values={form.values}
            setFieldValue={form.setFieldValue}
            lockedPrimary={lockedPrimary}
            soleRoles={target?.soleRoles ?? []}
          />
```

- [ ] **Step 9: Rewrite the two tables and the two attach modals**

In `src/pages/-customer-contacts-card.tsx`:

1. The header cell `{t("role")}` becomes `{t("contactRoles")}`.
2. The name cell carries the title under the name, and the role cell the badges:

```tsx
                    <Table.Td>
                      <Text size="sm">{formatContactName(association.contact)}</Text>
                      {association.title && (
                        <Text size="xs" c="dimmed">
                          {association.title}
                        </Text>
                      )}
                    </Table.Td>
                    <Table.Td>
                      <ContactRoleBadges roles={association.roles} />
                    </Table.Td>
```

3. `setEditing({…})` gains the three new fields. `soleRoles` is computable *here*, because this card holds every association of the customer — which is why it is a prop of the modal rather than a guess inside it:

```tsx
                            setEditing({
                              customerId,
                              contactId: association.contact.id,
                              counterpartName: formatContactName(association.contact),
                              title: association.title,
                              roles: association.roles,
                              soleRoles: association.roles
                                .filter(
                                  (assignment) =>
                                    !associations.some(
                                      (other) =>
                                        other.contact.id !== association.contact.id &&
                                        other.roles.some((r) => r.role === assignment.role),
                                    ),
                                )
                                .map((assignment) => assignment.role),
                              phone: association.phone,
                              email: association.email,
                            })
```

4. The attach modal's form loses `role` and gains the connection shape. Its `initialValues` become the contact fields plus `...emptyConnectionValues()` with the two prefixed connection keys removed — the `mapConnectionPath` indirection existed only because `phone`/`email` clashed with the contact's own, and `title`/`roles`/`primary` do not, so only those two stay prefixed:

```tsx
  const form = useForm({
    initialValues: {
      firstName: "",
      lastName: "",
      middleName: "",
      prefix: "",
      suffix: "",
      phone: "",
      email: "",
      title: "",
      roles: {} as Record<string, boolean>,
      primary: {} as Record<string, boolean>,
      connectionPhone: "",
      connectionEmail: "",
    },
    validate: {
      title: (value, values) =>
        value.trim().length === 0 && toRoleInputs(connectionValuesOf(values)).length === 0
          ? t("titleOrRoleRequired")
          : null,
      firstName: (value) => (creatingNew && !selected && value.trim().length === 0 ? t("firstNameRequired") : null),
      lastName: (value) => (creatingNew && !selected && value.trim().length === 0 ? t("lastNameRequired") : null),
    },
  });

/**
 * The attach form holds the contact's own fields beside the connection's, with
 * the two clashing ones prefixed; this is the connection half of it, in the
 * shape ConnectionFields and toRoleInputs speak.
 */
const connectionValuesOf = (values: {
  title: string;
  roles: Record<string, boolean>;
  primary: Record<string, boolean>;
  connectionPhone: string;
  connectionEmail: string;
}): ConnectionFormValues => ({
  title: values.title,
  roles: values.roles,
  primary: values.primary,
  phone: values.connectionPhone,
  email: values.connectionEmail,
});
```

5. Both mutations send `title` and the complete `roles`, and the "create a new contact then attach" path sends them too — the roles are the point of the attach, not an afterthought:

```tsx
  const attachExisting = useMutation({
    mutationFn: (contact: ContactResponse) =>
      attachCustomerContact(customerId, {
        contactId: contact.id,
        title: form.values.title.trim() || undefined,
        roles: toRoleInputs(connectionValuesOf(form.values)),
        phone: form.values.connectionPhone.trim() || undefined,
        email: form.values.connectionEmail.trim() || undefined,
      }),
    …
  });

  const createAndAttach = useMutation({
    mutationFn: async () => {
      const contact = await createContact(toContactInput(form.values));
      try {
        await attachCustomerContact(customerId, {
          contactId: contact.id,
          title: form.values.title.trim() || undefined,
          roles: toRoleInputs(connectionValuesOf(form.values)),
        });
      } catch (error) {
        // Keep the existing catch verbatim: the "the contact exists at this
        // point" re-throw is what stops a created contact being silently
        // orphaned when only the association fails.
        throw new Error(
          `The contact "${formatContactName(contact)}" was created, but could not be added to the customer: ${
            error instanceof Error ? error.message : "unknown error"
          }`,
          { cause: error },
        );
      }
      return contact;
    },
    // onSuccess and onError are unchanged.
  });
```

6. Both branches of `showConnectionForm` now render the same `ConnectionFields` — the standalone `TextInput label={t("role")}` for the create-new branch goes away, because a new contact needs its roles exactly as much as an existing one does. `lockedPrimary` and `soleRoles` are empty: an attach holds nothing yet, so nothing can be locked.

```tsx
          {showConnectionForm && (
            <>
              <ConnectionFields
                getInputProps={(path) => form.getInputProps(mapConnectionPath(path))}
                values={connectionValuesOf(form.values)}
                setFieldValue={(path, value) => form.setFieldValue(mapConnectionPath(path), value)}
                lockedPrimary={[]}
                soleRoles={[]}
              />
              <Group justify="flex-end" mt="xs">
                <Button variant="default" onClick={close}>
                  {t("cancel")}
                </Button>
                <Button type="submit" loading={attachExisting.isPending || createAndAttach.isPending}>
                  {t("addContact")}
                </Button>
              </Group>
            </>
          )}
```

7. `mapConnectionErrors` gains nothing: the server keys its errors `title`, `roles`, `phone`, `email`, and `mapConnectionPath` already maps the last two onto the prefixed paths while leaving the first two alone.

In `src/pages/contacts.$contactId.tsx`, the same four edits: the header cell, the name/roles cells (the customer's name stays an anchor; the title goes under it), the `setEditing` call — with `soleRoles: []`, because this page lists the contact's *customers* and cannot know who else holds a role at any of them — and the add-customer modal's form, which uses `emptyConnectionValues()`, `validateConnectionValues(t)`, `ConnectionFields` with empty `lockedPrimary`/`soleRoles`, and sends `title` + `toRoleInputs(form.values)`.

- [ ] **Step 10: Run the whole customers frontend suite**

```bash
cd /home/anders/projects/vantigo/vantigo
mise exec -- bun run --cwd apps/customers/frontend test
mise exec -- bun run --cwd apps/host/frontend test
mise exec -- bun run translations:check && mise exec -- bun run i18n:test
mise run frontend:check
bunx biome check .
```
Run the two test packages separately: `bun --filter` fan-out times out on this project's CI, and one package at a time is what the repo's own scripts do. `mise run frontend:check` covers typecheck and lint.

- [ ] **Step 11: Commit**

```bash
cd /home/anders/projects/vantigo/vantigo
printf '%s\n\n%s\n\n%s\n' 'feat(customers-ui): a contact'"'"'s title and its roles at the customer' 'The Role input is a Title input, and beside it a checkbox per role with a Primary switch that is on and disabled — with the reason — for a role the contact is the primary or only holder of, because the server refuses to clear it. Both tables show the title under the name and starred badges for the roles; the contact page shows them per customer. A response with neither title nor roles, which is what the recorded corpus answers, renders as it always did.' 'Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>' > /tmp/msg-roles-task4
git add apps/customers/frontend/src/api/contacts.ts apps/customers/frontend/src/api/contacts.test.ts apps/customers/frontend/src/lib/contact-role-label.ts apps/customers/frontend/src/lib/contact-role-label.test.ts apps/customers/frontend/src/components/contact-role-badges.tsx apps/customers/frontend/src/components/contact-role-badges.test.tsx apps/customers/frontend/src/pages/-connection.tsx apps/customers/frontend/src/pages/-customer-contacts-card.tsx apps/customers/frontend/src/pages/contacts.\$contactId.tsx apps/customers/frontend/src/pages/-contacts.test.tsx apps/customers/frontend/src/pages/-contact-details.test.tsx apps/customers/frontend/src/i18n.ts
git commit -F /tmp/msg-roles-task4 -- $(git diff --cached --name-only)
git show --stat HEAD && git status --short
```

- [ ] **Step 12: Show two of the new tests can fail**

1. `"disables the Primary switch of a role the contact is the only holder of, and says why"` — change `disabled={!checked || locked}` to `disabled={!checked}` in `ConnectionFields`, see the `toBeDisabled` assertion go red, restore.
2. `"sends the title and the complete role set when the connection is saved"` — drop `roles: toRoleInputs(values)` from the edit modal's `mutationFn`, see the body comparison go red, restore.
Note both in the report.

---

### Task 5: Documentation (D1–D5)

**Files:**
- Modify: `docs/customers.md`, `ROADMAP.md`
- Check, and modify only if it enumerates something this delivery changed: `CONTRIBUTING.md`

**Interfaces:** none — this task adds no code and no test.

- [ ] **Step 1: Find every place that now says something untrue**

```bash
cd /home/anders/projects/vantigo/vantigo
grep -rn 'no primary-contact flag\|free text, e.g. "Billing"\|carrying `role`' docs/ CONTRIBUTING.md
grep -rn 'customer.contact_attached' docs/ CONTRIBUTING.md
grep -n 'operationId:' openapi/customers.yaml | wc -l
grep -rn 'typed contact roles' ROADMAP.md
```
The first grep finds the three sentences in `docs/customers.md`'s Domain model and Contacts sections that promise a primary-contact flag as future work. The second finds the timeline's generated-event list and the bullets under it. The third is the operation count in the API section — **it has not moved**, and running the count is how you prove that rather than assume it. The fourth finds the phase 4 paragraph and the "Still ahead in this phase" list.

- [ ] **Step 2: Fix the Domain model bullet**

In `docs/customers.md`, the `**Customer–contact association**` bullet (~line 37) becomes:

```markdown
- **Customer–contact association** — the many-to-many link, carrying `title`
  (free text, e.g. "CEO" — what this person is called at this customer, nullable)
  and a phone/email that **override** the contact's own for this relationship
  only. The typed roles that answer "who gets the invoice" live beside it, one
  row per role in `customers.customer_contact_roles`, with exactly one primary
  contact per role. See [Contacts and associations](#contacts-and-associations).
```

- [ ] **Step 3: Rewrite the Contacts and associations section**

Replace `docs/customers.md`'s `## Contacts and associations` section body with the following, written in that file's voice — full sentences that explain *why*, not a field list:

```markdown
## Contacts and associations

A contact is a person record independent of any customer; the many-to-many
association is what links one to a customer, carrying a `title` and an optional
phone/email that overrides the contact's own for that relationship. Deleting a
contact removes every association it has, each recorded as a
`customer.contact_removed` event against the customer it was attached to, all
inside one transaction with a row lock on the contact.

Ten operations in total: list/create/get/update/delete a contact, list a
contact's customers, list a customer's contacts, attach/update/detach an
association. See the [API](#api) table for exactly which permission gates which.

### The title, and why `role` is still on the wire

`customers.customers_contacts.title` is free text and nullable. It was called
`role` until migration `00025` renamed it, and the rename is the whole of the
change: every value the column held was a job title (`CEO`, `CTO`), which
answers "who is this person" and never "who gets the invoice".

The **contract keeps `role`**, and that is deliberate rather than legacy debt.
In a request it is an optional, deprecated alias of `title`, and `title` wins
when both are sent; in a response it is required and answers the title or `""`.
The recorded exchange corpus — frozen evidence from the retired .NET suites —
sends `{"contactId": N, "role": "CEO"}` and reads `role` back, so a contract
that dropped it would be a contract this codebase can no longer prove itself
against. `title` and `roles` are additive and optional in the yaml for the same
reason, even though the server always answers `roles`.

A request with **neither a title nor at least one role** is refused (400, field
`title`, `A contact needs a title or at least one role`): an association that
says nothing about the person is not worth having. On an update, "at least one
role" counts the roles the association keeps — `roles` omitted means "leave them
alone", so changing only a phone number on an association with three roles is
not suddenly a request that says nothing.

### Typed roles, and one primary per role

`customers.customer_contact_roles(customer_id, contact_id, role, is_primary,
created_at)` holds one row per role a contact has at a customer. The vocabulary
is three values, defined in Go and nowhere else:

| role | answers |
| --- | --- |
| `billing` | who gets the invoice (and the reminder) |
| `project` | who is spoken to day to day |
| `decision_maker` | who approves |

Not a yaml `enum:` (a contract enum answers a 400 this module cannot word) and
not a database `CHECK` (which would turn a widened list from a value change into
a migration — the same reasoning migration `00024` gives for a tag's colour).
A role outside the three is a 400 on `roles`: `A contact role must be one of
'billing', 'project' or 'decision_maker', but was '…'`. A role named twice in
one request is a 400 on `roles` too: two contradictory primary flags for one
role is not something to resolve silently.

**The primary rule is the addresses' primary rule, verbatim**, with
`(customer_id, role)` where addresses have `(customer_id, type)` — see
[Addresses](#addresses) for the mechanism, because it is the same mechanism and
not a second one:

1. **The application layer, as the mechanism.** Every write that touches roles —
   attach, update, detach, and the contact delete — locks the customer row
   `FOR NO KEY UPDATE` first (`LockCustomer`) and does everything else inside
   that transaction. Within it, **demote happens before promote, and delete
   happens before promote.**
2. **The database, as the last word.** The partial unique index
   `ux_customer_contact_roles_primary` on `(customer_id, role) WHERE is_primary`
   (migration `00025`) enforces the invariant if the ordering above were ever
   wrong. It is the backstop, not the mechanism.

The rest of the invariant's shape: the **first** contact given a role is its
primary whatever the request says. `primary: true` on another contact demotes the
current one in the same transaction. An **explicit** `primary: false` on the
contact that is the only or the primary holder is **refused** (400, field
`roles`) — there is always a primary while anyone holds the role. A contact
**losing** a role it was primary
for is not refused the way clearing the flag is: it promotes the
**longest-standing** remaining holder (`created_at`, then `contact_id`), which is
why `created_at` is never rewritten by a demotion or a promotion — seniority in a
role is what decides succession. Detaching a contact, and deleting one (whose
cascade removes the association and its role rows), run that same promotion for
every role the contact was primary for.

The contact delete is the one write here that locks more than one customer row:
it locks every customer the contact is attached to, in ascending `customer_id`
order (which is why `ListAssociationsForContact` has an `ORDER BY`), so two
concurrent deletes of two contacts sharing two customers cannot deadlock with
each other. It can still deadlock with a concurrent **attach**, which takes the
customer row's lock before the contact row's while the delete takes them the
other way round — the delete cannot reverse its order, because reading the
contact's associations is what tells it which customers to lock. Both writes
therefore retry a serialization failure or deadlock up to three times
(`contactRoleWriteAttempts`), the same treatment the tag set replace gets for the
same kind of cycle.

Roles ride on the association's **own** endpoints; there are no new paths.
`POST /customers/{id}/contacts` takes `roles` as the roles to give (none when
omitted), `PUT /customers/{id}/contacts/{contactId}` takes it as the **complete**
set to hold (omitted = unchanged, `[]` = none), and both list shapes answer
`roles` in the fixed order `billing, project, decision_maker`.

Inside a `roles` array, **`primary` is three-valued**, and the three values are
three different instructions:

| `primary` | on a role the contact already holds | on a role it does not hold yet |
| --- | --- | --- |
| omitted | leave the flag exactly as it is | the first-holder rule: primary if nobody holds the role, otherwise not |
| `true` | become the primary, demoting whoever holds it | become the primary, demoting whoever holds it |
| `false` | **refused** if this contact is the primary holder; otherwise stays non-primary | join as a plain member — unless nobody holds the role, in which case the first-holder rule still makes it primary |

The omitted case is what makes "the complete set of roles" a writable field at
all. A client rebuilding the set from a checkbox group states which roles it
wants, not who should be primary for each; reading an omitted flag as `false`
would turn "also give this contact billing" into "and stop being the primary
project contact" — and because clearing a primary flag is *refused* rather than
applied, the request would fail for something it never said. So an omitted flag
asks for nothing, and only an explicit `false` is the refusal.

The association
endpoints' existing permissions cover roles — a role is part of the association —
so this delivery adds no permission key. `GET /customers/{id}/contacts` keeps its
contact-name order: "the primary billing contact" is found by scanning the list,
which is what the UI does. A directory accessor
(`CustomerDirectory.PrimaryContact`) is one added method the day Invoices asks for
it, and not before.

**Association writes touch no `revision`.** `customers_contacts` and
`customer_contact_roles` are off the customer row, exactly as addresses and tags
are, so no write here takes a revision, accepts one or bumps one.
```

- [ ] **Step 4: Update the timeline section**

In `docs/customers.md`'s `## The timeline`, the generated-event list is unchanged — this delivery introduces no new event type, which is worth saying — and one bullet joins the others:

```markdown
- The four contact events (`customer.contact_attached`,
  `customer.contact_relationship_updated`, `customer.contact_detached`,
  `customer.contact_removed`) carry `title` and `roles` (`[{role, primary}]`,
  never null) beside the `role` key they have always had, which is the same value
  as `title` and stays because entries already written speak it.
  `customer.contact_relationship_updated` is recorded only when the title, the
  phone, the email, the role set or a primary flag actually changed, and its
  summary names which: `Now the primary billing contact: …` when the contact
  became a role's primary, `Roles updated: …` when the set moved without a new
  primary, and the older `Contact relationship updated: …` when only the title,
  phone or email did. A **promotion** caused by somebody else's write — a contact
  that dropped a role it was primary for, a detach, a deleted contact — is
  recorded on the *promoted* contact as a `customer.contact_relationship_updated`
  with the `Now the primary … contact` summary and **the acting user who caused
  it**: a person did this, indirectly, and the timeline's job is to say who.
  Typed contact roles add no new event type, so nothing new was needed in
  `-customer-timeline.tsx`'s `typeKey`.
```

- [ ] **Step 5: Update the frontend section**

In `docs/customers.md`'s `## The frontend`, add to the contacts paragraph: the customer page's contacts card and the contact page's customers card both show the title under the name and role badges with a star on the primary one (whose `aria-label` spells out "Primary billing contact", so the star is not the only carrier of the fact); the attach and edit modals share one `ConnectionFields` with a Title input and a checkbox per role, each with a Primary switch that is enabled while the box is ticked and **on and disabled** for a role the contact already holds as primary, with the reason shown — "Already the only holder" where the page can tell (the customer's contacts card holds every association, so it can), and "the primary holder stays primary — make another contact primary instead" where it cannot (the contact page lists customers, not the other contacts at each one). Saving sends `title` and the complete `roles`.

- [ ] **Step 6: Mark the delivery in `ROADMAP.md`**

`### Phase 4 — Light CRM` keeps its intro. Add a delivery paragraph in the shape delivery A's has, after it:

```markdown
**Delivery B (done)** — decided in
[`docs/superpowers/specs/2026-09-23-customers-contact-roles-design.md`](docs/superpowers/specs/2026-09-23-customers-contact-roles-design.md):
the association's free-text `role` is a `title` and stays one (migration `00025`
renames the column; the wire keeps answering `role`, because the recorded
exchange corpus sends and reads it), and three typed roles — `billing`,
`project`, `decision_maker` — live in `customers.customer_contact_roles` with
exactly one primary contact per role, on the addresses' own invariant: the first
holder is primary whatever the request said, `primary: true` demotes the
incumbent, clearing the only or primary holder's flag is refused, and losing a
role promotes the longest-standing remaining holder. The roles ride on the
association's four existing endpoints and its two existing permissions — no new
paths, no new key — and a promotion caused by somebody else's write is recorded
on the promoted contact with the user who caused it. See
[`docs/customers.md#contacts-and-associations`](docs/customers.md#contacts-and-associations).
```

and update the "Still ahead in this phase" paragraph: typed contact roles are no longer ahead, so it lists follow-ups (a timeline entry's own date and assignee, feeding `/stats/attention` and a "my follow-ups" view), customer groups that can carry defaults, and attachments once the storage module has a model for them. Keep the existing sentence about the tag vocabulary being unpaged by design, and add one in the same spirit: the role vocabulary is deliberately **three** values, and a wider list (technical, executive sponsor) is a value change rather than a migration — the free-text title carries everything else today. Also update the phase heading's parenthetical if the file uses one for a partly-delivered phase (it currently reads `(first delivery done)`; check what phase 2's heading does and follow it).

- [ ] **Step 7: Commit**

```bash
cd /home/anders/projects/vantigo/vantigo
printf '%s\n\n%s\n' 'docs(customers): the association'"'"'s title, its typed roles and the primary rule they share with addresses' 'Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>' > /tmp/msg-roles-task5
git add docs/customers.md ROADMAP.md
git commit -F /tmp/msg-roles-task5 -- $(git diff --cached --name-only)
git show --stat HEAD && git status --short
```
(If `CONTRIBUTING.md` needed a line, add it to both the `git add` and the pathspec.)

---

### Task 6: Verify the whole branch and open the PR

**Files:** none. This task changes nothing; it proves what the branch does and hands it over.

- [ ] **Step 1: No generation drift**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server && mise exec -- go generate ./... && git status --short
cd /home/anders/projects/vantigo/vantigo && mise exec -- bun run gen:client && git status --short
```
Expected: nothing (the untracked root `go.mod`/`go.sum` aside — those are not ours). A tracked file that changes here means a committed generated file was stale.

- [ ] **Step 2: The whole Go suite**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- go vet ./... && mise exec -- golangci-lint run && mise exec -- go test -count=1 ./...
```
`golangci-lint run` is run **from `apps/server`**, with no package argument.

- [ ] **Step 3: The race detector, on four CPUs**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
CC="$(command -v zig >/dev/null && echo "$PWD/../../scripts/zig-cc" || echo cc)" CGO_ENABLED=1 \
  taskset -c 0-3 mise exec -- go test -race -count=1 ./internal/customers/... ./internal/db/... ./internal/openapi/... ./internal/module/...
```
If the repo's zig-cc wrapper is at another path, use that one (`ls scripts/`); if `go test -race` refuses for want of a C compiler, that wrapper is what supplies it. Then run the three role concurrency cases harder, since a database row lock is not something `-race` can see:

```bash
taskset -c 0-3 mise exec -- go test -count=20 -run 'TestPutAssociation_Concurrent|TestDetachTheOnlyHolder|TestAttachContact_RacesDeleteContact|TestAttachAndDeleteContact_CrossedLockOrders' ./internal/customers/
```
`TestAttachContact_RacesDeleteContact_OnTheSameContactRow` is the pre-existing case and is in this list because it still has to pass, **not** because it exercises the retry: its gate is on the contact row, so the attach never reaches its own customer lock before queueing, and no cycle can form. `TestAttachAndDeleteContact_CrossedLockOrders` (Task 3, Step 6) is the one that can form it, and `-count=20` is what makes the deadlock branch likely rather than occasional.

- [ ] **Step 4: The whole frontend**

```bash
cd /home/anders/projects/vantigo/vantigo
mise exec -- bun run --cwd apps/customers/frontend test
mise exec -- bun run --cwd apps/host/frontend test
mise exec -- bun run translations:check && mise exec -- bun run i18n:test
mise run frontend:check
bunx biome check .
```

- [ ] **Step 5: Check the trailers and the branch state**

```bash
cd /home/anders/projects/vantigo/vantigo
git log --format='%h %s%n%b' origin/main..HEAD | grep -c 'Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>'
git log --oneline origin/main..HEAD
git status --short
gh run list --branch main --limit 3
```
The count must equal the number of commits on the branch (the design commit `fadc4d8` included — it already carries the trailer). `gh run list` on `main` first, so a failure that was already there is not mistaken for one this branch caused.

- [ ] **Step 6: Push and open the PR**

```bash
cd /home/anders/projects/vantigo/vantigo
git push -u origin feat/customers-contact-roles
gh pr create --base main --head feat/customers-contact-roles --title 'feat(customers): typed contact roles with a primary contact' --body-file /tmp/pr-contact-roles.md
```

Write `/tmp/pr-contact-roles.md` first. It covers:

- **What shipped:** the `role` → `title` rename and why the wire keeps `role`; `customer_contact_roles` with the partial unique index; the three-role vocabulary validated in Go; the primary invariant on attach, update, detach and contact delete; the promotion event with the acting user; `title`/`roles` on both list shapes; the Title input, the Roles checkbox group with its per-role Primary switch, the badges with the primary star; both catalogs.
- **The decisions taken while implementing**, each with its reason:
  1. `validateContactRole` was **kept** as the deprecated `role` field's validator, with its exact messages and its three existing tests untouched, and `validateContactTitle` added beside it over one shared `associationTitleRule` — the messages name the field the caller sent, and the corpus's recorded 400 under `role` must keep reading the way it reads.
  2. `validateAssociationRole` is the name of the new typed-role validator (not `validateContactRole`, which was taken).
  3. The contract schema the spec calls `ContactCustomerResponse` is actually named `GetContactCustomersContactCustomerResponse`; the contract's own name was used.
  4. A blank `title` or `role` **given** is an error, not an absence — so the corpus's `{"role": ""}` line still answers `A role cannot be null or empty` keyed `role`, and the ported `TestAttachContact_WithInvalidConnection_ReportsFieldErrors` is unchanged.
  5. **`primary` in a `roles` element is three-valued** (`*bool`), which the spec leaves implicit: omitted on a role the contact already holds means "leave the flag alone", omitted on a new role follows the first-holder rule, and only an *explicit* `false` on the only/primary holder is refused. Reading an omitted flag as `false` would make every set replace that adds a role a 400 refusing to demote a primary the request never mentioned, so "the complete set of roles" would not be a writable field at all. The yaml declares `primary` nullable with that wording, and two tests pin the two halves.
  6. The update handler answers a no-op **without opening a transaction** (the module's no-op rule, the tags set replace's precedent), and the primary-clearing refusal is checked before that shortcut so a request that changes nothing else is still refused.
  7. The update and detach **re-read the association under the customer lock** and answer 404 if it is gone, closing a window that used to answer 200 for a concurrently detached association and would now be a foreign-key 500.
  8. `contactRoleWriteAttempts = 3` retries the deadlock between the attach's customer→contact lock order and the contact delete's contact→customer one, which cannot be removed by reordering (see the constant's comment).
  9. The disabled Primary switch's reason is **two** messages, not the spec's one: "Already the only holder" where the page can prove it (the customer's contacts card holds every association) and "the primary holder stays primary — make another contact primary instead" where it cannot (the contact page). Both are true; one message would have been wrong half the time.
  10. `roles` is always answered as `[]` and never `null`, while staying optional in the yaml — the `SafeCustomerResponse.tags` precedent.
  11. `normalizeContactRoles` re-sorts into the fixed order rather than trusting the server's, so no component depends on where its array came from.
- **What was proved able to fail:** the eight mutations listed in Tasks 1, 3 and 4's final steps, and what each failure said — including whether twenty runs of `TestAttachAndDeleteContact_CrossedLockOrders` without `db.RetrySerializable` actually reached the 40P01 branch, stated honestly either way.
- Nothing in scope crept: no wider vocabulary, no roles on the customer's own contact info, no `CustomerDirectory` accessor, no use of the billing contact for invoice or reminder e-mail resolution, no contact-level permissions, no follow-ups.

End the body with:

```
🤖 Generated with [Claude Code](https://claude.com/claude-code)
```

- [ ] **Step 7: Watch the checks and fix root causes on the same PR**

```bash
cd /home/anders/projects/vantigo/vantigo && gh pr checks --watch
```
Use the default, non-JSON output. Fix any failure at its root on this branch — never by relaxing a test or an assertion — and push again. **Never merge**: the user merges.

If the PR description needs editing afterwards, `gh pr edit` is broken in this environment; use `gh api --method PATCH repos/{owner}/{repo}/pulls/{number} -f body=@/tmp/pr-contact-roles.md` instead.

---

## Self-review notes

Checked against the spec, section by section:

- **D1 — the free text is a title.** Migration `00025` renaming `role` → `title` and dropping `NOT NULL`: Task 1. `role` optional in both request schemas, required in both response schemas, `title` nullable beside it, nothing added to a `required:` list: Task 2. `title` wins when both are sent; the title-or-role refusal on field `title` with the spec's exact message; today's validation rule (non-blank when given, ≤ 255 UTF-16 units, trimmed): Task 3, Steps 3 and 5, pinned by `TestAssociationRequests_RefuseAnUnknownRole…` and `TestAssociationRequests_TheCorpusShapeStillWorksAndTitleWins`.
- **D2 — three roles, one primary each.** The table with its PK, composite cascading FK, partial unique index and `created_at`: Task 1. The vocabulary and its message: Task 3, Step 3. The primary rule's four clauses, verbatim from addresses: `applyRoles` and `releaseRoles` in Task 3, Step 7, with one test per clause in Step 5 and the promotion-on-detach and promotion-on-delete cases too. Detach and delete running the same promotion per role: Task 3, Step 9. The three-valued `primary` flag: Task 2's yaml, Task 3's `requestedRole`, and the two tests in Step 5 that pin its omitted and explicit-false halves.
- **D3 — roles ride on the association's own endpoints.** No new paths, no new permissions, `roles` as "the roles to give" on attach and "the complete set" on update with omitted = unchanged and `[]` = none, duplicates a 400 on `roles`, both response shapes answering `roles` sorted: Tasks 2 and 3. `GET /customers/{id}/contacts` keeping its contact-name order: unchanged, and stated in Task 5's docs. The customer row locked for every role write, with the partial index as backstop: Task 1's index, Task 3's handlers, Task 3 Step 6's two forced races and `TestPartialIndexIsTheBackstop`. No `CustomerDirectory` accessor is added anywhere.
- **D4 — timeline.** `title` and `roles` on the four payloads beside `role`; the update event only on change; the situational summary ("Roles updated" / "Now the primary billing contact"); the promotion recorded on the promoted contact with the acting user: Task 3, Steps 6 and 7, pinned by `TestUpdateCustomerContact_RecordsAnEventOnlyWhenSomethingMovedAndSaysWhat` and `TestDetachContact_PromotesAndRecordsThePromotionWithTheActingUser`. `contact_detached`/`_removed` carrying `title`/`roles` in the snapshot: same recorders, same step.
- **D5 — what the user sees.** Badges with the primary star and its tooltip, the title under the name, on both the contacts card and the contact page: Task 4, Steps 3 and 9. The Title input, the Roles checkbox group, the per-role Primary switch disabled with a reason: Task 4, Step 8. Both catalogs: Task 4, Step 4, with the nb rendering pinned in Step 1's badge test. No dashboard or attention change is made anywhere.
- **Testing section.** Every case it names has a test: attach with roles (first holder primary whatever was sent, `primary: true` demotes, `primary: false` on the only holder refused, duplicates 400, unknown role 400, the title-or-role rule), update replacing the set (promotion of the longest-standing holder, `[]` clears, omitted keeps), detach and contact deletion promote, the corpus's `role`-only requests answering `role` = title, `title`/`roles` on both list shapes, events only on change with the right summaries and actor, the forced race of two `primary: true` writers, the partial index as backstop — Task 3, Steps 1, 9 and 10. Frontend: the modal round trip against wire-shaped fixtures with one literally the corpus body (no `title`, no `roles`), badge rendering, the disabled Primary switch, both catalogs — Task 4, Steps 1, 6 and 9.
- **Out of scope.** Nothing in any task adds a wider vocabulary, roles on the customer's own contact info, a directory accessor, invoice/reminder e-mail resolution from the billing contact, contact-level permissions, or follow-ups.

Type consistency, checked field by field:

- `contactRole{Role string; Primary bool}` is the one Go type for a role the database holds — used by `applyRoles`, `releaseRoles`, `contactRolesOf`, `genContactRoles`, `rolesChanged`, `heldPrimary`, `requestedAsHeld`, `relationshipUpdateAction`, `recordPromotions` and all five recorders. Its `Primary` is never a pointer.
- `requestedRole{Role string; Primary *bool}` is the request side only, and its `Primary` is **always** a pointer: `wantsPrimary()` and `clearsPrimary()` are the only two readers, and neither collapses nil into false. Nothing outside `contact_roles.go` and the update handler constructs one.
- `rolePromotion{ContactID int32; Role string}` travels from `applyRoles`/`releaseRoles` to `recordPromotions`.
- `validatedAssociation{Title *string; Roles []requestedRole; RolesGiven bool; Phone, Email *string}` is what validation answers; `RolesGiven` is the only thing that distinguishes an omitted `roles` from `[]`.
- Generated names used: `gen.CustomerContactRole{Role string; Primary bool}`, `gen.CustomerContactRoleRequest{Primary *bool; Role string}`, `Title *string` / `Roles *[]…` on the four association types. `store.…Params` field spellings (`ExcludeContactID`, `IsPrimary`, `Keep`, `Now`) are to be confirmed against the generated file in Task 1 Step 8 and followed where they differ.
- On the frontend, `ContactRoleAssignment{role: string; primary: boolean}` is what every component reads (never optional, because `normalizeContactRoles` fills it), `ContactRoleInput{role: string; primary?: boolean}` is what every request sends (optional, because an omitted flag is a meaningful instruction), `contactRoleLabel` is the only role → label function, and `ContactRoleBadges` the only role → chip component. `titleOf` is the only place `role` is read as a title fallback.

