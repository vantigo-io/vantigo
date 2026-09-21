# The Invoice-Ready Customer (delivery A) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give a customer its own contact info, typed addresses and a billing profile, with API, UI and a `CustomerDirectory` other modules (Invoices) can read them through.

**Architecture:** Three new sub-resources under `/api/v1/customers/{id}` (contact-info, addresses, billing-profile) — never a wider `PUT /customers/{id}`. Contact info and the billing profile are nullable columns on `customers.customers` (so they share the row's `revision`); addresses are their own table with a one-primary-per-type invariant. One new sensitive permission, `customers:billing-manage`. `contracts.CustomerDirectory` gains a batch lookup and a resolved `BillingProfile`.

**Tech Stack:** Go 1.27 (pgx, sqlc, oapi-codegen strict server, goose), PostgreSQL 18, React + Mantine + TanStack Router/Query, vitest, bun, mise.

**Spec:** `docs/superpowers/specs/2026-09-21-customers-invoice-ready-design.md` (D1–D6). It builds on `docs/superpowers/specs/2026-09-21-customers-foundation-design.md` (revision rules D5, actor rule D1) and `docs/customers.md`. Read the spec first.

## Global Constraints

- Branch `feat/customers-invoice-ready`. Never commit to `main`, never merge, never `--no-verify`.
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

### Task 1: Contact info on the customer (D2, D1)

**Files:**
- Create: `apps/server/internal/db/migrations/00017_customers_contact_info.sql`, `apps/server/internal/customers/contact_info.go`, `contact_info_test.go`
- Modify: `sqlc.yaml`, `queries/customers.sql` (every SELECT/RETURNING of the row gains the three columns; new guarded `UpdateCustomerContactInfo :one`; `InsertCustomer` takes them; list/count search), `customers.go` (`customerRow`, `safeCustomerResponse`, `PostCustomers`, search params), `values.go` + `values_test.go` (`validateEmail`, `validatePhone`, `validateWebsite`), `timeline_events.go` (`recordCustomerContactInfoUpdated`, event type `customer.contact_info_updated`), `openapi/customers.yaml`, generated files

**Interfaces:**
- Produces: schema `CustomerContactInfo {email, phone, website}` (all nullable strings); `SafeCustomerResponse.contactInfo` (optional); `CreateCustomerRequest.contactInfo` (optional); `PUT /api/v1/customers/{id}/contact-info` operationId `putCustomersByIdContactInfo`, body `PutCustomerContactInfoRequest {email, phone, website, revision}` (all optional/nullable), 200 → `SafeCustomerResponse`, 400 validation problem, 404, 409 `CustomerConflictProblem`; access `permission:customers:update+customers:view`.
- Produces Go: `type contactInfo struct{ Email, Phone, Website *string }`; `validateContactInfo(email, phone, website *string) (contactInfo, map[string][]string)`; `validateEmail(raw string) (string, string)` reused by Task 3.

```sql
-- +goose Up
-- A customer's own contact details (invoice-ready design D2): what reaches the
-- customer itself rather than one of its contacts. NULL is "none given".
ALTER TABLE customers.customers
    ADD COLUMN email   varchar(255),
    ADD COLUMN phone   varchar(30),
    ADD COLUMN website varchar(2048);

-- +goose Down
ALTER TABLE customers.customers DROP COLUMN website, DROP COLUMN phone, DROP COLUMN email;
```

Rules (exact messages): email — `"An email address must look like name@example.com, but was '%s'"`, `"An email address cannot be longer than 255 characters, the given value was %d characters"`; phone — `"A phone number may only contain digits, spaces and + - ( ), and needs at least five digits, but was '%s'"`, length message in the same pattern with 30; website — reuse the timeline's `sourceUrl` validator and wording if it is generic enough, else `"A website must be an absolute http or https URL, but was '%s'"`. Blank → NULL. Errors keyed `email`, `phone`, `website` on the sub-resource and `contactInfo.email` etc. on create.

Handler order: validation 400 → 404 → revision 409 → no-op (200, nothing written) → actor → tx {guarded UPDATE bumping revision, event with before/after payload}; `pgx.ErrNoRows` from the guarded UPDATE → re-read → 409/404, as `PutCustomersById` does.

Search: add `OR c.email ILIKE search OR replace-free compact match on phone` — match `c.phone` against the compact pattern after stripping spaces from the column too: `regexp_replace(c.phone, '\s', '', 'g') ILIKE search_compact`. Both in `CountCustomers` and `ListCustomers`, kept textually identical.

- [ ] Failing tests: PUT sets all three and GET/list show them under `contactInfo`; blanks clear to null; each validation rule (table-driven unit tests + one HTTP case per field); stale `revision` → 409 and nothing written; no-op PUT bumps nothing and records no event; a real change bumps `revision` by one and records one `customer.contact_info_updated` attributed to the signed-in user with before/after in the payload; POST with `contactInfo` creates it (and a bad one is a 400 under `contactInfo.email`); caller without `customers:update` → 403; list search finds a customer by its email and by phone typed with different spacing, with control rows.
- [ ] Implement contract → generate → queries → handler. `go vet ./...`.
- [ ] Green: `go test -count=1 ./internal/customers/... ./internal/openapi/... ./internal/module/... ./internal/db/...`. Commit: `feat(customers): a customer has its own email, phone and website`.

### Task 2: Addresses (D3)

**Files:**
- Create: `apps/server/internal/db/migrations/00018_customers_addresses.sql`, `apps/server/internal/customers/queries/addresses.sql`, `addresses.go`, `addresses_test.go`, `addresses_concurrency_test.go`
- Modify: `sqlc.yaml` (schema list; `queries` is a directory so the new file is picked up), `values.go`/`values_test.go` (`validateAddress`), `timeline_events.go` (three event recorders), `openapi/customers.yaml`, generated files

**Interfaces:**
- Produces: schemas `CustomerAddress {id int32, type, label?, line1, line2?, postalCode?, city?, region?, country, isPrimary bool, createdAt, updatedAt}` and `CustomerAddressRequest {type, label, line1, line2, postalCode, city, region, country, isPrimary}` (required: `type`, `line1`, `country`); operations `getCustomersByIdAddresses` (200 → `{data: CustomerAddress[]}` ordered by type, primary first, id), `postCustomersByIdAddresses` (201 + Location), `putCustomersByIdAddressesByAddressId` (200), `deleteCustomersByIdAddressesByAddressId` (204); access per spec D1.

```sql
-- +goose Up
CREATE TABLE customers.customer_addresses (
    id          integer GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    customer_id integer      NOT NULL REFERENCES customers.customers (id) ON DELETE CASCADE,
    type        varchar(20)  NOT NULL,
    label       varchar(100),
    line1       varchar(255) NOT NULL,
    line2       varchar(255),
    postal_code varchar(20),
    city        varchar(100),
    region      varchar(100),
    country     varchar(2)   NOT NULL,
    is_primary  boolean      NOT NULL,
    created_at  timestamptz  NOT NULL,
    updated_at  timestamptz  NOT NULL
);
CREATE INDEX ix_customer_addresses_customer ON customers.customer_addresses (customer_id, type);
-- One primary per customer and type (invoice-ready design D3): the invariant
-- the handlers keep under the customer row's lock, and the database's own
-- last word should they ever not.
CREATE UNIQUE INDEX ux_customer_addresses_primary
    ON customers.customer_addresses (customer_id, type) WHERE is_primary;

-- +goose Down
DROP TABLE customers.customer_addresses;
```

**Serialisation:** every address write runs in a transaction that first locks the customer row (`SELECT id FROM customers.customers WHERE id = @id FOR NO KEY UPDATE` — a new `LockCustomer :one`; `ErrNoRows` → 404), then reads the type's addresses, decides (first-of-type → primary; `isPrimary: true` → demote the old primary first; update that changes `type` re-evaluates both the old and new type's primaries; delete of the primary → promote the oldest remaining by `created_at, id`; `isPrimary: false` on the primary → 400 `"An address that is the only or primary one of its type stays primary; make another one primary instead"` keyed `isPrimary`), then writes. Demote-before-promote order matters for the partial unique index. The customer row's `revision` is not touched.

Validation messages follow the module's pattern; `type` `"An address type must be one of 'postal', 'invoice', 'delivery' or 'visiting', but was '%s'"`; Norwegian rule `"A Norwegian address needs a four-digit postal code"` (key `postalCode`) and `"A Norwegian address needs a city"` (key `city`); 51st address → 400 `"A customer can have at most 50 addresses"` (key `addresses`). Address of another customer under this customer's URL → 404.

- [ ] Failing tests for each rule above, plus: list ordering; events recorded with the acting user (`customer.address_added` / `_updated` / `_removed`, payload carrying type, label and a one-line rendering, never nothing); cascade on nothing (customers are never hard-deleted — assert an archived customer's addresses still list); 403 for a caller without `customers:update`; GET with only `customers:view` works.
- [ ] Concurrency tests (style of `customers_concurrency_test.go`, using the lock-gate helper): two simultaneous "make primary" on different addresses of one type → both 200, exactly one primary afterwards; delete-the-primary racing add-first-of-type → exactly one primary or zero addresses, never two primaries and never zero primaries with addresses present.
- [ ] Green as Task 1, also under `taskset -c 0-3` / `GOMAXPROCS=4` for the concurrency file. Commit: `feat(customers): typed addresses, one primary of each type`.

### Task 3: The billing profile and its permission (D4, D1)

**Files:**
- Create: `apps/server/internal/db/migrations/00019_customers_billing_profile.sql`, `apps/server/internal/customers/billing_profile.go`, `billing_profile_test.go`, `billing_values.go`, `billing_values_test.go`
- Modify: `sqlc.yaml`, `queries/customers.sql` (`GetCustomerBillingProfile :one` selecting the billing columns + what warnings need; guarded `UpdateCustomerBillingProfile :one` bumping revision — do NOT add the billing columns to the list/get row queries), `module.go` (14th permission: `{Key: "customers:billing-manage", Display: "Manage billing profiles", Description: "Set a customer's payment terms, invoice delivery and billing addresses for documents.", Category: "Billing", Sensitive: true, Delegable: true}` and the comments that say "thirteen"), `module_test.go`, `harness_test.go` (`authenticatedClient` gains the key), `timeline_events.go`, `openapi/customers.yaml`, generated files, `apps/host/frontend/src/catalogs/admin.ts` (+ its nb counterpart — find how permission labels/categories are keyed and add `customers:billing-manage` and the "Billing" category in en + nb), any test enumerating the permission catalog

**Interfaces:**
- Produces: schema `CustomerBillingProfile {invoiceEmail, reminderEmail, paymentTermsDays, currency, language, invoiceDelivery, reminderDelivery, peppolId, gln, buyerReference (all nullable), revision int32, warnings string[]}`; `PutCustomerBillingProfileRequest` (same fields minus warnings, `revision` optional); operations `getCustomersByIdBillingProfile` (`permission:customers:view`), `putCustomersByIdBillingProfile` (`permission:customers:billing-manage+customers:view`), 200 → `CustomerBillingProfile`, 400, 404, 409 `CustomerConflictProblem`.
- Produces Go: `type billingProfile struct{…}`; `validateBillingProfile(req) (billingProfile, map[string][]string)`; `validGLN(string) bool` (GS1 mod-10: from the right, weights 3,1,3,…); `validatePeppolID`; `billingWarnings(profile, customerType, identity *legalIdentity, contactEmail *string, hasInvoiceAddress bool) []string` returning codes in a fixed order: `ehf_without_recipient`, `email_without_address`, `efaktura_for_business`, `no_invoice_address`.

Column names: `invoice_email varchar(255)`, `reminder_email varchar(255)`, `payment_terms_days integer`, `currency varchar(3)`, `language varchar(2)`, `invoice_delivery varchar(20)`, `reminder_delivery varchar(20)`, `peppol_id varchar(60)`, `gln varchar(13)`, `buyer_reference varchar(100)`.

Exact messages: `"Payment terms must be between 0 and 365 days, but was %d"`; currency — projects' wording `"A currency must be a three-letter ISO 4217 code, but was '%s'"`; `"A document language must be one of 'nb' or 'en', but was '%s'"`; `"An invoice delivery method must be one of 'email', 'ehf', 'efaktura' or 'paper', but was '%s'"`; `"A reminder delivery method must be one of 'email' or 'paper', but was '%s'"`; `"A Peppol participant id must look like 0192:923609016 (a four-digit scheme, a colon, an identifier), but was '%s'"`; for scheme 0192 with a bad number reuse the organisation-number message; `"A GLN must be 13 digits with a valid check digit, but was '%s'"`; buyer reference length message in the module's pattern with 100.

`no_invoice_address` needs Task 2's table: a small `CustomerHasInvoiceAddress :one` (`EXISTS` primary `invoice` or primary `postal`).

Handler order for PUT: validation 400 → 404 → revision 409 → no-op → actor → tx {guarded UPDATE, `customer.billing_profile_updated` with before/after}. The PUT's 200 body carries the new `revision` and fresh `warnings`.

- [ ] Failing tests: GET on a fresh customer → all null, `revision` = the row's, `warnings` = [`no_invoice_address`] only; each validation rule (unit + one HTTP each); each warning on and off (EHF with a Norwegian business identity and no peppolId → no `ehf_without_recipient`; `email` delivery satisfied by the contact-info email; `efaktura_for_business`; adding a primary postal address clears `no_invoice_address`); stale revision → 409 nothing written; no-op; bump + event + attribution; **permission: a caller with `customers:update` but not `customers:billing-manage` → 403 on PUT and 200 on GET; the list/get customer responses never contain billing fields**; the module's permission-catalog test counts fourteen.
- [ ] Host catalogs: label + category in en and nb; run `mise exec -- bun run --cwd apps/host/frontend test`, `translations:check`, `i18n:test`.
- [ ] Green as Task 1. Commit: `feat(customers): a billing profile, behind its own permission`.

### Task 4: The directory other modules read (D5)

**Files:**
- Modify: `apps/server/internal/contracts/directory.go` (interface, `CustomerBillingProfile`, `CustomerAddressEntry`, doc comment rules), `apps/server/internal/customers/directory.go`, `directory_test.go`, `queries/customers.sql` + `queries/addresses.sql` (`DirectoryCustomers :many` with `id = ANY(@ids::int[])`, `DirectoryBillingProfile :one`, `DirectoryInvoiceAddress :one`), every fake/stub of `contracts.CustomerDirectory` (grep `ContactsByEmail(` across `apps/server`: `internal/module/compose_test.go`, `internal/integration/harness_test.go`, `internal/expenses/contractscalls_internal_test.go`, projects/energy/communications/products harnesses, `internal/modtest`), `apps/server/internal/projects/projects_list.go` (`customerNamesForPage` → one `Customers` call; keep `noteContractCall` bookkeeping via a `directoryCustomers` wrapper in `projects/contracts.go`), its tests, `docs/module-boundaries.md`

**Interfaces:**
- Produces:
```go
// Customers looks up customers by ID. An id that does not exist is simply
// absent from the result, not an error; archived customers resolve.
Customers(ctx context.Context, ids []int32) ([]CustomerEntry, error)
// BillingProfile is what an invoice needs to know about a customer, already
// resolved. It returns (nil, nil) if id does not exist.
BillingProfile(ctx context.Context, id int32) (*CustomerBillingProfile, error)

type CustomerBillingProfile struct {
    ID               int32
    CustomerNumber   int64
    Name             string
    Type             string // "business" | "person"
    Archived         bool
    LegalCountry     string // "" when the customer has no legal identity
    LegalID          string
    LegalName        string
    InvoiceAddress   *CustomerAddressEntry // primary invoice, else primary postal, else nil
    InvoiceEmail     string // billing invoice email, else the customer's own email, else ""
    ReminderEmail    string // billing reminder email, else InvoiceEmail
    PaymentTermsDays *int32
    Currency         string
    Language         string
    InvoiceDelivery  string
    ReminderDelivery string
    PeppolID         string // explicit, else 0192:<orgnr> for a Norwegian business identity, else ""
    GLN              string
    BuyerReference   string
}
type CustomerAddressEntry struct{ Label, Line1, Line2, PostalCode, City, Region, Country string }
```
- [ ] Failing directory tests (SQL-seeded, as `directory_test.go` does): batch returns found ones only, archived included, empty input → empty slice and no query error, duplicates in input tolerated; each resolution rule of `BillingProfile` both ways; missing → `(nil, nil)`.
- [ ] Projects: a test that the list page makes ONE directory call for a page with several distinct customers (the package has contract-call bookkeeping — `noteContractCall`; find the existing assertion style in `projects_list_test.go`) and still names archived and missing customers as before.
- [ ] `go vet ./...` (fakes!), full `go test -count=1 ./...`. Commit(s): `feat(contracts): the customer directory answers in batches and knows a billing profile`, `perf(projects): the list names its customers in one directory call`.

### Task 5: Frontend — contact info and addresses (D6)

**Files:**
- Create: `apps/customers/frontend/src/api/addresses.ts` (+ test), `src/pages/-customer-contact-card.tsx` (+ test), `src/pages/-customer-address-modal.tsx` (+ test) — split further if a file passes ~300 lines
- Modify: `src/api/customers.ts` (`contactInfo` on `CustomerResponse`, `updateContactInfo(id, input & {revision})`, create input), `src/pages/-customer-form-modal.tsx` (optional email + phone on create), `src/pages/customers.$customerId.tsx` (render the card on Overview; new prop `canEdit?: boolean`), `src/pages/customers.index.tsx` (search placeholder), `src/i18n.ts`, host `apps/host/frontend/src/routes/customers/` (pass `canEdit` from `customers:update` the way `canArchive` is passed; loader prefetch of addresses if siblings prefetch), tests beside each

- [ ] Tests first per behaviour: card shows email as `mailto:`, phone as `tel:`, website as an external link (`rel="noopener noreferrer"`, `target="_blank"`), em-dash placeholders when empty; edit modal sends exactly `{email, phone, website, revision}` to `PUT …/contact-info`, shows field errors from a 400 under the right inputs, and on a revision 409 shows the foundation's Reload pattern (re-seeding values AND the revision it will send next); addresses grouped by type in a fixed order (invoice, postal, delivery, visiting) with a Primary badge; add/edit modal with type select, country select (reuse a country list if `frontend-shell` or another package has one; otherwise a small ISO list with localized names via `Intl.DisplayNames`), Norwegian postal-code hint; "Make primary" sends the full address with `isPrimary: true`; delete confirms through the shell's confirmation pattern; all mutations invalidate `["customers", id, "addresses"]` (and `["customers"]` for contact info); no edit affordances without `canEdit`.
- [ ] Checks: tests, typecheck, lint for customers + host, root `bunx biome check .`, `translations:check`, `i18n:test`; regenerate and commit `routeTree.gen.ts` only if it changed.
- [ ] Commit(s): `feat(customers-ui): a customer's contact details and addresses on its page`.

### Task 6: Frontend — the billing card (D6)

**Files:**
- Create: `apps/customers/frontend/src/api/billing-profile.ts` (+ test), `src/pages/-customer-billing-card.tsx` (+ test), `src/pages/-customer-billing-modal.tsx` (+ test)
- Modify: `src/pages/customers.$customerId.tsx` (render; prop `canManageBilling?: boolean`), `src/i18n.ts`, host route (pass `canManageBilling` from `customers:billing-manage`), tests

- [ ] Tests first: read-only card renders each field with human labels (delivery methods and languages through catalog keys, payment terms as "{n} days", "Not set — the invoicing default applies" for nulls); each warning code maps to a sentence in both languages and an unknown code is ignored; the resolved hints ("Invoices go to {email}" using invoiceEmail → contact email; "EHF recipient 0192:… from the organisation number") match the server's resolution rules; edit modal sends the full profile + `revision`, maps 400 field errors, handles the revision 409 with Reload (values and revision), and is absent without `canManageBilling`; currency input upper-cases; number input bounds 0–365.
- [ ] Same checks as Task 5. Commit: `feat(customers-ui): the billing profile on the customer's page`.

### Task 7: Docs

**Files:** `docs/customers.md` (model, the three sub-resources, the primary-address invariant and how it is serialised, billing fields + warnings, the new permission → fourteen, directory additions with the resolution rules, frontend), `ROADMAP.md` (Customers phase 2: mark what is done, keep the Peppol lookup as the remaining part), `docs/module-boundaries.md` if Task 4 did not already, `CONTRIBUTING.md` customers bullet if it enumerates resources.
- [ ] Write without committing if another agent is editing the tree; otherwise commit `docs(customers): contact info, addresses and the billing profile`.

### Task 8: Verify and open the PR

- [ ] `cd apps/server && mise exec -- go generate ./... && git status --short` (no drift), `go vet ./...`, `golangci-lint run` **from `apps/server`**, `go test -count=1 ./...`.
- [ ] Race on four CPUs: `CC=<zig cc wrapper> CGO_ENABLED=1 taskset -c 0-3 mise exec -- go test -race -count=1 ./internal/customers/... ./internal/contracts/... ./internal/projects/... ./internal/db/... ./internal/module/...`.
- [ ] Repo root: `mise exec -- bun run gen:client` (no diff), `mise run frontend:check`.
- [ ] `gh run list --branch main --limit 3`; trailers on every commit; push; `gh pr create` with the decisions list; watch checks (default, non-JSON `gh pr checks` output); fix root causes on the same PR. Never merge.
