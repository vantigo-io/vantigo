# Brreg In Full (phase 3, delivery A) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Keep the full Brønnøysundregistrene record beside a Norwegian business customer, fetch it on a Brreg pick and on a click, report what changed as `registry.change` events, surface bankruptcy/liquidation/deletion/rename on the dashboard's attention list, and offer the registry's addresses.

**Architecture:** The Brreg client gains a single-entity fetch (pinned v2 media type, three outcomes: entity / deleted / removed). A new table `customer_registry_records` holds the record with `fetched_at`; the customer row and its `revision` are untouched. `POST /customers/{id}/registry-refresh` fetches, diffs against the stored record, upserts, and records one `registry.change` event; `GET /customers/{id}/registry-record` reads it (withheld without `legal-identity-view`); `/stats/attention` computes four item types from stored records vs the current customer. Frontend: a Registry card and "Use registry address" offers.

**Tech Stack:** Go 1.27 (pgx, sqlc, oapi-codegen, goose), PostgreSQL 18 (jsonb for addresses), React + Mantine + TanStack Query, vitest, bun, mise.

**Spec:** `docs/superpowers/specs/2026-09-22-customers-brreg-full-design.md` (D1–D5; "What the registry looks like" was verified against the live API — use its field names and status semantics verbatim).

## Global Constraints

- Branch `feat/customers-brreg-full`. Never commit to `main`, never merge, never `--no-verify`.
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

- The Brreg client's tests use `modtest.WithTransport` / an `httptest` server exactly as `brreg_test.go` does today; no test reaches data.brreg.no.
- A registry fetch triggered by a create or an identity PUT runs AFTER that write's transaction has committed and never fails the request.

---

### Task 1: The Brreg client fetches one entity (D2)

**Files:**
- Modify: `apps/server/internal/customers/brreg.go` (+ `brreg_test.go`, `brreg_internal_test.go`)
- Create: `apps/server/internal/customers/brreg_entity.go`, `brreg_entity_test.go`

**Interfaces:**
- Produces:
```go
const brregEntityMediaType = "application/vnd.brreg.enhetsregisteret.enhet.v2+json"
type brregAddress struct { Lines []string; PostalCode, City, Municipality, CountryCode string }
type brregEntityRecord struct {
    OrganisationNumber, Name, OrganisationFormCode, OrganisationForm, IndustryCode, Industry string
    Employees *int32 // nil unless harRegistrertAntallAnsatte
    VATRegistered, Bankrupt, UnderLiquidation, UnderForcedLiquidation bool
    DeletedOn, FoundedOn *time.Time // dates
    Website, Email, Phone, Mobile, ParentOrganisationNumber string
    BusinessAddress, PostalAddress *brregAddress
}
type brregEntityOutcome int // brregEntityFound, brregEntityDeleted (200 + respons_klasse SlettetEnhet: Name/OrganisationNumber/DeletedOn filled), brregEntityRemoved (410: DeletedOn from the body if present), brregEntityUnknown (404)
func (c *brregClient) entity(ctx context.Context, orgnr string) (brregEntityRecord, brregEntityOutcome, error) // error = transport/5xx-after-retries/malformed/oversized/wrong media type — "unavailable", as the search
```
Rules: request `GET {base}/enhetsregisteret/api/enheter/{orgnr}` with `Accept: brregEntityMediaType`; the response `Content-Type` must start with that media type or `application/json` (a 406 is an error naming the media type); body capped at 1 MiB; retry policy and backoff reused from the search (`brregBackoff`, `waitBackoff`, `isSuccessStatus`); 404 → unknown, 410 → removed, both never retried; `adresse` lines trimmed and empty lines dropped; `landkode` upper-cased two letters else ""; dates parsed as `2006-01-02`.

- [ ] Tests first against `httptest`: the two live bodies from the spec (Equinor: form ASA, industry 06.100, employees 21272, VAT true, business address Forusbeen 50 / 4035 STAVANGER / NO, postal Postboks 8500; Brreg: parent 912660680, email, phone, employees 487) → every field; a `SlettetEnhet` body (`respons_klasse: "SlettetEnhet"`, `slettedato: "2026-09-21"`) → deleted with name and date; a foreign address (`{"land":"Polen","landkode":"PL","poststed":"81-336 GDYNIA","adresse":["ul. Budowniczych 12"]}`) → PostalCode "", City "81-336 GDYNIA", CountryCode "PL"; `harRegistrertAntallAnsatte: false` → Employees nil; 404 empty body → unknown, no retry; 410 with `slettedato` → removed; 500 then 200 → found after retry (backoff seam at 0); 406 → error; 2 MiB body → error; malformed JSON → error; the `Accept` header asserted on every request.
- [ ] Implement; `go vet`, `golangci-lint run` from apps/server, `go test -count=1 ./internal/customers/...`. Commit: `feat(customers): the Brreg client reads one entity's full record, deleted and removed included`.

### Task 2: The record, its refresh and its event (D1, D2, D4)

**Files:**
- Create: `apps/server/internal/db/migrations/00021_customers_registry_records.sql`, `apps/server/internal/customers/queries/registry.sql`, `registry.go`, `registry_test.go`, `registry_diff.go`, `registry_diff_test.go`
- Modify: `sqlc.yaml`, `customers.go` (`PostCustomers`: fetch after commit when the identity is a Brreg-sourced Norwegian business), `legal_identity.go` (same on PUT with source brreg), `timeline_events.go` (`recordRegistryChange`, `recordRegistryRemoved`), `openapi/customers.yaml`, generated files, `openapi/COVERAGE.md`, `internal/db/schema_test.go` table list

```sql
-- +goose Up
-- The registry's own view of a Norwegian business customer (Brreg-in-full
-- design D1): a fact about the world with a timestamp, kept beside the
-- customer and never edited by hand. Its own table so a fetch never touches
-- the customer row or its revision.
CREATE TABLE customers.customer_registry_records (
    customer_id                  integer      PRIMARY KEY REFERENCES customers.customers (id) ON DELETE CASCADE,
    organisation_number          varchar(9)   NOT NULL,
    name                         varchar(255) NOT NULL,
    organisation_form_code       varchar(10),
    organisation_form            varchar(100),
    industry_code                varchar(10),
    industry                     varchar(255),
    employees                    integer,
    vat_registered               boolean      NOT NULL,
    bankrupt                     boolean      NOT NULL,
    under_liquidation            boolean      NOT NULL,
    under_forced_liquidation     boolean      NOT NULL,
    deleted_on                   date,
    founded_on                   date,
    website                      varchar(2048),
    email                        varchar(255),
    phone                        varchar(30),
    mobile                       varchar(30),
    parent_organisation_number   varchar(9),
    business_address             jsonb,
    postal_address               jsonb,
    fetched_at                   timestamptz  NOT NULL,
    registry_updated_hint        timestamptz
);

-- +goose Down
DROP TABLE customers.customer_registry_records;
```

**Interfaces:**
- Produces: schemas `CustomerRegistryAddress {lines: string[], postalCode?, city?, municipality?, countryCode}`, `CustomerRegistryRecord {organisationNumber, name, organisationFormCode?, organisationForm?, industryCode?, industry?, employees?, vatRegistered, bankrupt, underLiquidation, underForcedLiquidation, deletedOn?, foundedOn?, website?, email?, phone?, mobile?, parentOrganisationNumber?, businessAddress?, postalAddress?, fetchedAt}` (required: organisationNumber, name, the four booleans, fetchedAt), `CustomerRegistryChange {field, from?, to?}`, `CustomerRegistryRefreshResponse {status: "found"|"deleted"|"removed"|"unknown", record?, changes: CustomerRegistryChange[]}`; operations `getCustomersByIdRegistryRecord` (`permission:customers:view`; 200 record; **204** when there is none or the caller lacks `customers:legal-identity-view` — same shape as the legal-identity GET's "nothing to show"), `postCustomersByIdRegistryRefresh` (`permission:customers:legal-identity-manage+customers:view`; 200; 404; 409 `CustomerConflictProblem` with `code: "no_registry_identity"`; 502 ProblemDetails "Registry unavailable"). Truncate registry strings to their columns' widths in UTF-16 units before storing (a `navn` longer than 255 is possible).
- Go: `func registryRecordFrom(e brregEntityRecord, now time.Time) registryRecord`; `func diffRegistryRecords(before *registryRecord, legalName string, after registryRecord) []registryChange` (fields: name, organisationForm, industryCode, employees, vatRegistered, bankrupt, underLiquidation, underForcedLiquidation, deletedOn, website, email, phone, mobile, parentOrganisationNumber, businessAddress, postalAddress — addresses compared as their rendered one-line form; first fetch compares `name` against `legalName` only); `func notableRegistryChanges(changes) (bankrupt, liquidation, deleted, renamed bool)`.

Refresh handler order: 404 → identity check (Norwegian business with `validNorwegianOrgNumber`) else 409 → actor (before the network call) → `brreg.entity` OUTSIDE any transaction → error → 502, nothing stored → tx {read stored row `FOR UPDATE`; found: upsert + `registry.change` event when `len(changes) > 0` (payload `{changes: [...]}`, summary "Registry record updated: <comma-separated field names, truncated to the column>"; first fetch with no differences from the legal name records nothing); deleted: upsert with `deleted_on` set + event; removed: DELETE the row + `registry.change` event with a single change `{field: "removedFromOpenData", to: <date>}`; unknown: nothing stored, 200 `status: "unknown"` (an identity whose number the registry does not know — the user should look at it)}. The create/identity-PUT hook calls the same fetch+store function with the request's actor after commit, swallowing (logging at warn with `customerId` and an error kind, never the body) any error.

- [ ] Failing tests: create with a Brreg pick → a record row exists with the fetched fields (transport fake answers the Equinor body) and the customer response is unchanged in shape; create with the fake answering 500 → 201 anyway, no row, warn logged (assert via the harness's log capture if it has one; else assert only "no row and 201"); PUT legal-identity source brreg → row; manual source → no fetch; GET record with `legal-identity-view` → 200 fields; without → 204; no row → 204; refresh: first fetch → row + no event when the name matches, one `registry.change` with `{field:"name"…}` when it differs; second refresh with changed employees + address → one event listing both, `fetched_at` moved, `revision` unchanged; deleted → status deleted, `deletedOn` stored, event; removed → row gone, event with `removedFromOpenData`; unknown → 200 unknown, nothing stored; 409 for a person / non-Norwegian / manual-invalid identity; 502 on transport failure with the old row intact; 403 without `legal-identity-manage`; string truncation for an over-long name.
- [ ] Contract → generate → gen:client → queries → handlers; `COVERAGE.md`. `go vet`, lint, `go test -count=1 ./internal/customers/... ./internal/openapi/... ./internal/db/... ./internal/module/...`. Commit: `feat(customers): the registry's record beside the customer, refreshed on a click, its changes on the timeline`.

### Task 3: Attention items (D4)

**Files:**
- Modify: `apps/server/internal/customers/stats.go` (`GetCustomersStatsAttention`), `stats_test.go`, `queries/registry.sql` (`RegistryAttentionCandidates :many` — join records with customers, non-archived, returning what the four rules need), `openapi/customers.yaml` (the `CustomerStatsAttentionItem` description: the four types and their clearing rules; shape unchanged), generated files, `apps/host/frontend/src/routes/dashboard.tsx` (`attentionTitleKey` for the customers types) + its catalogs (en + nb) + `dashboard.test.ts`

Rules: for every non-archived customer with a record: `registryBankrupt` when `bankrupt`; `registryLiquidation` when `under_liquidation || under_forced_liquidation` (and not already bankrupt — one item per cause, bankruptcy wins); `registryDeleted` when `deleted_on` is set; `registryRenamed` when `name` ≠ the legal identity's `legal_name` (case-sensitive, trimmed) and none of the three above. `id` = `<type>/<customerId>`; `title` = the customer's name; `occurredAt` = `fetched_at`; `entityId` = customer id; ordered by `occurredAt` desc then id. The caller needs `customers:view` (the operation's existing access); the items name no organisation number.
- [ ] Failing tests: each type appears exactly once for its condition; archived customers excluded; bankruptcy suppresses the liquidation item; renamed suppressed by any status item; updating the legal name clears `registryRenamed`; archiving clears everything; ordering; a customer without a record yields nothing. Host: `attentionTitleKey` returns the four keys; sentences in en and nb ("{{name}} is registered as bankrupt" / "{{name}} er registrert konkurs", "… is under liquidation" / "… er under avvikling", "… has been deleted from the register" / "… er slettet fra registeret", "… has changed its name in the register" / "… har skiftet navn i registeret").
- [ ] Commit: `feat(customers): bankruptcy, liquidation, deletion and a rename reach the dashboard`.

### Task 4: Frontend — the Registry card and registry addresses (D3, D5)

**Files:**
- Create: `apps/customers/frontend/src/api/registry.ts` (+test), `src/pages/-customer-registry-card.tsx` (+test)
- Modify: `src/pages/customers.$customerId.tsx` (render the card on Overview when `canViewIdentity`; new prop `canManageIdentity`), `src/pages/-customer-contact-card.tsx` / `-customer-address-list.tsx` (the two "Use registry address" offers, shown when a record with that address exists and the customer has no address of that type yet; they open the existing address modal prefilled), `src/pages/-customer-address-modal.tsx` (accept initial values), `src/i18n.ts`, host `-customer-overview-tab.tsx` (+test: `canViewIdentity` from `customers:legal-identity-view`, `canManageIdentity` from `customers:legal-identity-manage`)

- [ ] Tests first: api normalises omitted fields (a fixture with only the required fields; 204 → `null`); the card renders every field with labels, the "From Brønnøysundregistrene, fetched {date}" line, red badges for bankrupt / under liquidation / deleted, the parent number as text, and nothing but a "Registry record not fetched yet" note + Refresh when there is no record; Refresh (`canManageIdentity`) → POST, pending state, the record query refetched, "N changes — see the timeline" shown once, `unknown` → "The register does not know this organisation number", 502 → inline error; the rename notice with **Update legal name** → `PUT …/legal-identity` with the registry name and the existing source/country/type/id, then the customer query invalidated; the address offers open the modal prefilled (lines joined → line1/line2, postal code, city, country lower-cased, type visiting/postal) and disappear once an address of that type exists; without `canViewIdentity` the card is absent; fixtures literally the server's wire bodies.
- [ ] Checks: customers + host tests/typecheck/lint, root biome, translations:check, i18n:test. Commit: `feat(customers-ui): the registry's record on the customer page, and its addresses one click away`.

### Task 5: Docs

`docs/customers.md` (registry record: table, fetch-on-pick/click, the three outcomes, the diff and the event, attention items and their clearing rules, address offers, permissions/withholding), `ROADMAP.md` (phase 3: delivery A done; B = feed worker + Peppol re-checks remaining), `openapi/COVERAGE.md` if not already, `CONTRIBUTING.md` operation count. Commit: `docs(customers): the registry record, and what a refresh reports`.

### Task 6: Verify and open the PR

As the previous deliveries: generate/gen:client drift, vet, lint from `apps/server`, `go test ./...`, race on 4 CPUs (customers, module, db), `mise run frontend:check`; trailers normalised BEFORE the first push; rebase onto `main` if it moved; push; PR with the decisions list. Never merge.
