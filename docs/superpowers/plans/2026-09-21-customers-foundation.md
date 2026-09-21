# Customers Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the Customers module's foundation gaps — timeline authorship, legal-identity validation, a list that searches/filters/sorts, a revision on the customer row, a duplicate-identity guard — and the frontend that uses them.

**Architecture:** Contract first: `openapi/customers.yaml` changes are additive and optional (the recorded corpus is frozen), then `go generate` + `bun run gen:client`. Two small migrations (`00015`, `00016`) on the `customers` schema, sqlc queries, handlers in `apps/server/internal/customers`, tests through `modtest`. Frontend in `apps/customers/frontend` with the host route/layout in `apps/host/frontend/src/routes/customers`.

**Tech Stack:** Go 1.27 (pgx, sqlc, oapi-codegen strict server, goose migrations), PostgreSQL 18, React + Mantine + TanStack Router/Query, vitest, bun, mise.

**Spec:** `docs/superpowers/specs/2026-09-21-customers-foundation-design.md` (decisions D1–D7). Read it first; this plan argues from it.

## Global Constraints

- Branch `feat/customers-foundation`. Never commit to `main`, never merge, never `--no-verify`.
- **Forbidden git commands:** `git add -A`, `git add .`, `git stash`, `git checkout -- .`, `git restore .`, `git clean`, `git reset --hard`. Commit with an explicit pathspec: `git add <files>` then `git commit -F <msgfile> -- <files>`, and check `git show --stat HEAD`. The untracked `go.mod`/`go.sum` in the repo root are not ours: never add or delete them.
- Commit messages: Conventional Commits scoped `customers` / `customers-ui` / `frontend` / `docs`, subject written as a plain sentence about behaviour (see `git log`). End every message with `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>`.
- Toolchain only through mise: `mise exec -- go …`, `mise exec -- sqlc …`, `mise exec -- bun …`. A bare `go` is "command not found" in a non-interactive shell and reads as success if piped. Capture exit codes before any pipe (`${PIPESTATUS[0]}`).
- Go tests need `export TEST_DATABASE_URL='postgres://vantigo:vantigo@127.0.0.1:55442/vantigo_test?sslmode=disable'` (port 55432 belongs to another project's container — do not touch it).
- After any `openapi/*.yaml` change: `cd apps/server && mise exec -- go generate ./... && mise exec -- go test -count=1 ./internal/openapi/...`, then from the repo root `mise exec -- bun run gen:client`; commit the generated files (`internal/openapi/specs/customers.yaml`, `internal/customers/gen/api.gen.go`, five `api-schema.d.ts` copies, `openapi/COVERAGE.md` if it moves).
- After any migration: add it to `apps/server/internal/customers/sqlc.yaml`'s `schema:` list (and wherever `internal/db/schema_test.go` expects it), then `go generate`.
- After any exported Go signature change: `cd apps/server && mise exec -- go vet ./...`.
- Contract changes are additive and optional only (frozen corpus): never add to a `required:` list of an existing schema, never remove or rename.
- Match the surrounding code: its comment density (these files explain *why*, at length), naming, error-message wording ("… must be one of 'a' or 'b', but was 'x'"), and test style. Check sibling pages/components before inventing a frontend convention: pages read `useSearch({strict:false})`/`useNavigate` themselves and never fetch permissions — the host passes capability props.
- Every new UI string goes in both catalogs in `apps/customers/frontend/src/i18n.ts` (en + nb); `mise exec -- bun run translations:check` and `mise exec -- bun run i18n:test` must pass.
- Every new test must be shown able to fail: remove the guard, see red, restore. Say so in the report.
- Frontend tests: `mise exec -- bun run --cwd apps/customers/frontend test` (not `bun --cwd X run test`, which runs nothing). Mantine popovers need `<MantineProvider env="test">` — fix the provider, never the component.

---

### Task 1: Timeline entries name their author (D1)

**Files:**
- Create: `apps/server/internal/db/migrations/00015_customers_timeline_actor.sql`
- Modify: `apps/server/internal/customers/sqlc.yaml`, `queries/timeline.sql`, `queries/customers.sql` (`InsertGeneratedTimelineEvent`), `timeline.go`, `timeline_events.go`, `contacts_timeline.go`, every caller of the `record*` helpers (`customers.go`, `customer_type.go`, `legal_identity.go`, `contacts.go`), `internal/db/schema_test.go` if it lists migrations
- Create: `apps/server/internal/customers/actor.go`, `actor_test.go` (package `customers_test`)
- Test: update `timeline_test.go`, `timeline_more_test.go`, `customers_test.go`, `contacts_test.go` expectations that pin `unattributed`/`system`

**Interfaces:**
- Produces: `type actor struct { Kind, Display string; UserID *uuid.UUID }`; `func (s *server) actorFor(ctx context.Context) (actor, error)`; constants `manualFallbackActor = actor{"unattributed","Unattributed",nil}`, `generatedFallbackActor = actor{"system","System",nil}`. Every `record*` helper and `InsertManualTimelineEntry`/`InsertTimelineRevision`/`InsertGeneratedTimelineEvent` takes the actor's three values as parameters.

Migration:

```sql
-- +goose Up
-- Who wrote a timeline entry, and who made each revision of it (customers
-- foundation design D1). NULL for everything written before this migration and
-- for a write made with no user principal: nothing can be said about those.
ALTER TABLE customers.customers_timeline_entries ADD COLUMN actor_user_id uuid;
ALTER TABLE customers.customers_timeline_entries_revisions ADD COLUMN actor_user_id uuid;

-- +goose Down
ALTER TABLE customers.customers_timeline_entries_revisions DROP COLUMN actor_user_id;
ALTER TABLE customers.customers_timeline_entries DROP COLUMN actor_user_id;
```

`actorFor`: `contracts.PrincipalFrom(ctx)`; no principal, `p.SCIM`, or `p.UserID == uuid.Nil` → caller's fallback (return a zero `actor` with `Kind == ""` and let callers substitute, or take the fallback as a parameter — pick one and use it everywhere). Otherwise `s.deps.Users.User(ctx, p.UserID)`: error → return it (the write fails; a timeline that guesses is worse than a 500); `nil` → display `"Unknown user"`; else `DisplayName`. Resolve the actor **before** opening the transaction (no directory call under a lock).

Behaviour to implement and test (through HTTP with `authenticatedClient`, reading back via the API and, for `actor_user_id`, via `modtest.One`):

- [ ] Failing test: POST a manual entry → response `actorKind == "user"`, `actorDisplay` == the signed-in user's display name; DB `actor_user_id` == that user's id. (Find how `modtest` names the signed-in user — `h.SignIn` — and what display name it gives; assert against that, not a literal.)
- [ ] Failing test: user A creates, user B (second `h.SignIn`) updates, then deletes → the entry's `actorDisplay` stays A; `GET …/revisions` shows revision 1 by A, 2 and 3 by B.
- [ ] Failing test: creating a customer, updating its name, archiving it, attaching a contact → each generated event carries the acting user, `provenance` still `generated`, `producer` still `customers.api`.
- [ ] Failing test: a principal whose user the directory does not know (use a `modtest` option or a fake `Users` if the harness offers one; otherwise unit-test `actorFor` with a stub `contracts.UserDirectory` in an internal test) → `Unknown user`.
- [ ] Implement; update pinned expectations in existing tests (they assert `unattributed`/`Unattributed`/`system`/`System` — each becomes the signed-in user; keep one test that proves the fallback when there is no principal only if such a path is reachable, otherwise document in `actor.go` that it is unreachable today).
- [ ] `go generate`, `go vet ./...`, `go test -count=1 ./internal/customers/... ./internal/db/... ./internal/openapi/...` green. Commit: `feat(customers): a timeline entry names the user who wrote it, and each revision who made it`.

### Task 2: Legal identities are validated where a rule exists (D2)

**Files:**
- Modify: `apps/server/internal/customers/values.go`, `values_test.go`, `legal_identity.go` only if validation is wired there
- Create: `apps/server/internal/customers/countries.go` (the ISO 3166-1 alpha-2 set, lower-case, `var iso3166Alpha2 = map[string]struct{}{…}` — all 249 officially assigned codes)
- Test: fix fixtures across `*_test.go` and `apps/customers/frontend` tests that use invalid org numbers (`123456789` is invalid; `923609016` (Equinor) and `974760673` (Brønnøysundregistrene) are valid)

**Interfaces:**
- Produces: `validateCountryCode` rejects unknown codes: `"A country code must be an ISO 3166-1 alpha-2 code, but was '%s'"`. `validateLegalIdentity` (existing signature) normalises and validates a Norwegian business id: `"A Norwegian organisation number must be nine digits with a valid check digit, but was '%s'"`. New pure func `validNorwegianOrgNumber(digits string) bool`.

- [ ] Table-driven failing tests in `values_test.go`: valid (`923609016`, `974 760 673` → stored `974760673`), wrong check digit (`923609017`), eight/ten digits, letters, a number whose mod-11 remainder yields check digit 10 (construct one and assert invalid), `country "xx"`/`"nor"`/`"Narnia"` rejected, `"NO"` accepted and lower-cased, `country "se"` + any id unchanged rule, `type person` + `country no` unchanged rule.
- [ ] Handler-level failing tests: POST customer with a bad org number → 400 with `errors["identity.id"]`; PUT legal-identity likewise; PUT customer *omitting* identity on a row whose stored identity is invalid (insert it with SQL) → 200.
- [ ] Implement (mod 11: weights `3,2,7,6,5,4,3,2` over the first eight digits; `r := sum % 11`; check = `0` if `r == 0`, invalid if `11-r == 10`, else `11-r`).
- [ ] Fix every fixture that now fails; do not weaken the rule to spare a fixture.
- [ ] Green as in Task 1. Commit: `feat(customers): a legal identity's country is a real country and a Norwegian organisation number checks out`.

### Task 3: The list searches, filters and sorts (D4)

**Files:**
- Modify: `openapi/customers.yaml` (`getCustomers` parameters: `status`, `type`; `sortBy` description), generated files, `queries/customers.sql` (replace `ListCustomersByID`/`ListCustomersByName` with one `ListCustomers`; `CountCustomers` same filters), `customers.go` (`GetCustomers`, `validateGetCustomersParams`, drop `fromListByNameRow`), `customers_test.go`
- Create: `apps/server/internal/customers/customers_list_test.go`

**Interfaces:**
- Produces: query params `status` (`active|disabled|archived`), `type` (`business|person`), `sortBy` ∈ `id|name|customerNumber|createdAt|updatedAt`. Error wording follows the existing `'sortBy' must be one of …, but was '%s'.` pattern.

The shared filter (write it once per query; sqlc has no fragments, so `CountCustomers` and `ListCustomers` repeat it verbatim — keep them textually identical and say so in the comment):

```sql
WHERE (
        (sqlc.narg(status)::text IS NOT NULL AND c.status = sqlc.narg(status)::text)
     OR (sqlc.narg(status)::text IS NULL AND (@include_archived::bool OR c.status <> 'archived'))
      )
  AND (sqlc.narg(customer_type)::text IS NULL OR c.type = sqlc.narg(customer_type)::text)
  AND (
        sqlc.narg(search)::text IS NULL
     OR c.name ILIKE sqlc.narg(search)::text
     OR c.customer_number::text ILIKE sqlc.narg(search_compact)::text
     OR (@search_identity::bool AND (
            c.legal_name ILIKE sqlc.narg(search)::text
         OR c.legal_id ILIKE sqlc.narg(search_compact)::text))
     OR (@search_contacts::bool AND EXISTS (
            SELECT 1
            FROM customers.customers_contacts cc
            JOIN customers.contacts ct ON ct.id = cc.contact_id
            WHERE cc.customer_id = c.id
              AND (ct.first_name ILIKE sqlc.narg(search)::text
                OR ct.last_name ILIKE sqlc.narg(search)::text
                OR (ct.first_name || ' ' || ct.last_name) ILIKE sqlc.narg(search)::text
                OR ct.email ILIKE sqlc.narg(search)::text
                OR cc.email ILIKE sqlc.narg(search)::text)))
      )
```

`search_compact` is `likePattern` of the trimmed term with all Unicode whitespace removed. `search_identity` = `hasPermission(legalIdentityView)`; `search_contacts` = `hasPermission("customers:contacts-view") && hasPermission("customers:associations-view")`. Ordering: one `CASE WHEN @sort_by = 'name' AND NOT @descending THEN c.name END ASC`-style pair per key (text keys, bigint keys and timestamptz keys each need their own CASE expressions — a CASE cannot mix types), then `c.id` in the same direction as the tie-break.

- [ ] Failing tests (each with a control row that must *not* match): by customer number; by org number with and without spaces; by legal name; by contact last name, full name, canonical email and association email; **a caller without `legal-identity-view` searching an org number gets zero rows, and a caller without `contacts-view` searching a contact email gets zero rows** (sign in with narrower permission sets); LIKE metacharacters (`%`, `_`) in the term are literal; `status=archived` alone returns only archived; `status=disabled`; `status=bogus` → 400; `type=person`; each `sortBy` both directions with an id tie-break case; `pagination.totalCount` agrees with the filters.
- [ ] The existing test that pins "search never matches legal name/id" (ported from .NET) now contradicts D4: rewrite it to the new behaviour and say why in its comment.
- [ ] Implement contract → generate → query → handler. `go vet ./...`.
- [ ] Green. Commit: `feat(customers): the list finds a customer by number, organisation number or contact, and filters by status and type`.

### Task 4: The customer row has a revision (D5)

**Files:**
- Create: `apps/server/internal/db/migrations/00016_customers_revision.sql`, `apps/server/internal/customers/customers_concurrency_test.go`
- Modify: `sqlc.yaml`, `queries/customers.sql` (every `RETURNING`/`SELECT` of the customer row gains `revision`; `UpdateCustomer` and `SetCustomerType` take `sqlc.narg(expected_revision)`; all five writes `SET revision = revision + 1`; the legal-identity writes in `queries/` too), `customers.go`, `customer_type.go`, `legal_identity.go`, `openapi/customers.yaml` (`SafeCustomerResponse.revision` optional int32; `UpdateCustomerRequest.revision` and the type-change request's `revision` optional int32; `409` on `putCustomersById` and `putCustomersByIdType` using a new `CustomerConflictProblem` schema = ProblemDetails' properties + optional `code` string + optional `duplicates` array of `{id int32, customerNumber int64, name string, status string}`), generated files

```sql
-- +goose Up
-- The customer row's optimistic-concurrency token (customers foundation design
-- D5): every write adds one, and an update that names the revision it read is
-- refused when the row has moved on.
ALTER TABLE customers.customers ADD COLUMN revision integer NOT NULL DEFAULT 1;

-- +goose Down
ALTER TABLE customers.customers DROP COLUMN revision;
```

Guarded write: `WHERE id = @id AND (sqlc.narg(expected_revision)::int IS NULL OR revision = sqlc.narg(expected_revision)::int)`; `pgx.ErrNoRows` from the `UPDATE` after a successful `GetCustomer` means a concurrent writer won → the same 409. A PUT that changes nothing still bumps nothing? **No-op rule:** keep today's behaviour that `updated_at` does not move when nothing changed, and likewise do not bump `revision` then (compute in Go, as `updatedAt` is today) — but a stale `revision` on a no-op PUT is still a 409, because the caller's picture is stale.

409 body: `title: "Customer revision conflict"`, `detail: "The customer has been changed since revision N was read; it is now at revision M."`, `status: 409`.

- [ ] Failing tests: GET shows `revision: 1`; PUT with the right revision → 200 and `revision: 2`; PUT with a stale revision → 409 and the row untouched (name, `updated_at`, no timeline event); PUT without `revision` → 200 (corpus compatibility, say so in the test comment); type change likewise; archive, legal-identity PUT and DELETE each bump it; no-op PUT does not bump.
- [ ] Concurrency test modelled on `timeline_concurrency_test.go`: N goroutines PUT with the same revision → exactly one 200, the rest 409, final `revision == 2`.
- [ ] Implement; green under `taskset -c 0-3` (or `GOMAXPROCS=4`) as well. Commit: `feat(customers): a customer carries a revision, and an update from a stale one is refused`.

### Task 5: A legal identity already in use is a conflict you can overrule (D6)

**Files:**
- Modify: `openapi/customers.yaml` (`allowDuplicateIdentity` optional boolean on `CreateCustomerRequest`, `UpdateCustomerRequest` and the legal-identity PUT body; `409` with `CustomerConflictProblem` on `postCustomers` and `putCustomersByIdLegalIdentity`; already on `putCustomersById` from Task 4), generated files, `queries/customers.sql` (`CustomersByLegalIdentity :many` — `WHERE legal_country = @country AND legal_id = @legal_id AND id <> @exclude_id ORDER BY id LIMIT 5`), `customers.go`, `legal_identity.go`
- Create: `apps/server/internal/customers/duplicates.go`, `duplicates_test.go`

**Interfaces:**
- Produces: `func (s *server) duplicateIdentityProblem(ctx, q *store.Queries, identity legalIdentity, excludeID int32) (*gen.CustomerConflictProblem, error)` — nil when there is none. Body: `title: "Duplicate legal identity"`, `code: "duplicate_legal_identity"`, `detail: "Another customer already has this legal identity."`, `status: 409`, `duplicates`.

Order of checks on each operation: permission gate → validation → 404 → revision conflict → **duplicate check** (only when the request's identity differs from the stored one, or on create) → write. The check runs inside the write's transaction.

- [ ] Failing tests: create twice with the same identity → second is 409 naming the first (`id`, `customerNumber`, `name`, `status`); `allowDuplicateIdentity: true` → 201; an **archived** holder still conflicts and is reported with `status: "archived"`; same id in another country → no conflict; PUT customer re-sending its own unchanged identity → 200; PUT customer changing identity to a taken one → 409; legal-identity PUT likewise, and with the override → 200; a caller lacking `legal-identity-manage` still gets 403 first, never the 409.
- [ ] Implement; green. Commit: `feat(customers): a legal identity another customer already has is a conflict, unless the caller says otherwise`.

### Task 6: Frontend — the list (D7, list)

**Files:**
- Modify: `apps/customers/frontend/src/api/customers.ts` (+ `customers.test.ts`), `pages/customers.index.tsx` (+ its test; create one beside the existing page tests if none exists), `i18n.ts`, `apps/host/frontend/src/routes/customers/index.tsx` (+ test if siblings have one)

**Interfaces:**
- Produces: `CustomerResponse` gains `customerNumber: number` and `revision?: number`; `CustomersQueryParams` gains `status?: "active"|"disabled"|"archived"`, `type?: CustomerType`, `sortBy?: "id"|"name"|"customerNumber"|"createdAt"|"updatedAt"`. Host `validateSearch` returns `{page, search, status, type, sortBy, sortDirection, create?}` with invalid values dropped to `undefined`; `loaderDeps`/loader pass them through so the loader and the page build the **same query key**.

- [ ] Tests first: api builds the query string; page renders customer number not id; choosing a status/type filter navigates with that search param and resets `page` to 1; clicking a sortable header toggles `sortBy`/`sortDirection`; archived rows render with the archived badge when `status=archived`.
- [ ] Implement: a status `Select` (All open — default, Active, Disabled, Archived) and a type `Select` (All, Business, Private) beside the search box; sortable headers on number, name and created (look for an existing sortable-header pattern in `packages/frontend-shell` or a sibling list page and reuse it; otherwise an `UnstyledButton` header with a chevron and `aria-sort`). Search placeholder now says what it searches.
- [ ] `mise exec -- bun run --cwd apps/customers/frontend test`, host tests, `mise exec -- bunx biome check .`, typecheck (`mise exec -- bun run --filter '*' typecheck` or the repo's script), translations checks. If the host route file changed shape, run `vite build` for the host so `routeTree.gen.ts` is regenerated, and commit it if it changed.
- [ ] Commit: `feat(customers-ui): the list shows customer numbers, filters by status and type, and sorts`.

### Task 7: Frontend — form, detail and timeline (D7, rest)

**Files:**
- Modify: `apps/customers/frontend/src/api/customers.ts`, `api/request.ts` only if a 409 body is not already surfaced (check how `ApiValidationError` is built and add a sibling `ApiConflictError { code?: string; duplicates?: … ; detail?: string }` there), `pages/-customer-form-modal.tsx`, `pages/customers.$customerId.tsx`, `pages/-customer-timeline.tsx`, `api/timeline.ts`, `i18n.ts`, host `-customer-detail-layout.tsx` / `$customerId.index.tsx` to pass `canArchive` (`customers:delete`) and `canRestore` (`customers:update`), tests beside each

- [ ] Tests first, then implement, one behaviour at a time:
  - `updateCustomer`/`changeCustomerType` send `revision`; on 409 without `code` the modal shows "This customer was changed by someone else. Reload to see the latest version." with a Reload action that refetches and re-seeds the form.
  - On 409 `duplicate_legal_identity` the modal lists the duplicates (name, customer number, status badge, link to `/customers/{id}`) and offers "Create anyway"/"Save anyway", which resubmits with `allowDuplicateIdentity: true`.
  - Similar-names hint: in create mode, a debounced (300 ms, ≥ 3 chars) `customersQueryOptions({search: name, pageSize: 3, status: undefined})` shows "Existing customers with a similar name" with links; never blocks submit.
  - Detail page: Archive button (confirmation modal following the shell's confirmation pattern — find it in `@vantigo/frontend-shell`) calling a new `archiveCustomer(id)` (`DELETE`), and Restore calling `updateCustomer(id, {name, status: "active", revision})`; an archived customer shows an `Alert` banner; both invalidate `["customers"]`.
  - Timeline: each entry card shows `actorDisplay` (falls back to the existing `unattributed` string) next to its date.
- [ ] Same checks as Task 6. Commit(s): `feat(customers-ui): …` one per behaviour group.

### Task 8: Docs

**Files:**
- Create: `docs/customers.md` (what the module is: model, statuses incl. D3's plain statement, legal identity + validation, contacts, timeline + authorship, list search and its permission rule, revision, duplicate guard, permissions table, `CustomerDirectory`, what Invoices will need — pointing at the research doc). Follow `docs/projects.md`'s shape.
- Modify: `docs/README.md` (link), `ROADMAP.md` (new `## Customers` section: Phase 1 — Foundation (done), Phase 2 — The invoice-ready customer, Phase 3 — Brreg in full, Phase 4 — Light CRM, Phase 5 — Customer 360, Phase 6 — Data operations and compliance, Later; each with a *Unblocks:* line, in the file's existing voice), `README.md` module status line if it still says "In development" and that is no longer the right word, `CONTRIBUTING.md`'s customers bullet if it enumerates behaviour.
- [ ] Commit: `docs(customers): the module's reference, and its roadmap`. The spec, this plan and the research doc ship in the same PR (`docs(customers): foundation research, design and plan`).

### Task 9: Verify and open the PR

- [ ] `cd apps/server && mise exec -- go generate ./... && git diff --exit-code` (no drift), `mise exec -- go vet ./...`, `mise exec -- golangci-lint run` **from `apps/server`** (any stderr beside `0 issues.` is a failed run), `mise exec -- go test -count=1 ./...`.
- [ ] Race run on four CPUs: `CC=<zig cc wrapper> CGO_ENABLED=1 taskset -c 0-3 mise exec -- go test -race -count=1 ./internal/customers/...` (whole suite if time allows).
- [ ] Repo root: `mise exec -- bun run gen:client && git diff --exit-code`, `mise run frontend:check`.
- [ ] `gh run list --branch main --limit 3` — if main is red, fix that in its own commit.
- [ ] Trailers: every commit in `merge-base..HEAD` ends with the Fable trailer (normalise with `git filter-branch --msg-filter` if a subagent wrote its own).
- [ ] Push, `gh pr create` against `main` with the decisions list from the spec; watch checks with the default (non-JSON) `gh pr checks` output; fix root causes on the same PR. Never merge.
