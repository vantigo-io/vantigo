# Vantigo Go port — sub-project 4: Customers, Products, Energy

Parent: `docs/superpowers/specs/2026-09-10-go-backend-port-design.md` (§10.4).
Behavioural authority for each module:

- `docs/superpowers/specs/2026-09-12-customers-inventory.md`
- `docs/superpowers/specs/2026-09-12-products-inventory.md`
- `docs/superpowers/specs/2026-09-12-energy-inventory.md`

Where this document and an inventory disagree, the inventory describes what
.NET does and this document decides what Go does.

## Goal

Port the three business modules to Go, single-tenant, behind the platform
sub-project 3 built: 75 operations (customers 29, products 26, energy 20),
222 ported tests. The SPA stays on .NET until sub-projects 6 and 7; these
modules are served by `cmd/vantigo` and exercised by tests and the smoke
test only.

One spec, one plan, one PR.

## What the .NET modules do

- **Customers.** Customers, contacts, associations between them, legal
  identity, and a timeline of entries. Brreg (the Norwegian business
  register) lookup backs legal-identity search. No rate limits, no audit
  trail, and no error-code vocabulary: refusals are bare problems or
  validation text.
- **Products.** Products, variants, categories, tax categories and prices.
  Money is `numeric` in Postgres and rounded by the column scale. Variant
  options are `jsonb`. `GET /products/stats/attention` is a permanent stub.
- **Energy.** Metering points, meters, supply periods and consumption, plus
  two aggregation endpoints. Supply periods are protected by a GiST
  exclusion constraint; consumption intervals have no overlap protection at
  all.

None of the three references another: they depend on Identity, Configuration
and Tenancy, and tenancy is dropped. Customers publishes `ICustomerDirectory`,
which Energy (and later Communications) consume in process.

## Decisions

### Platform

- **Precedence-aware routing.** `module.Router` keeps its surface
  (`HandleFunc`, `ServeHTTP`, `Err`) but dispatches through a matcher of its
  own instead of `http.ServeMux`. Within one path length, a literal segment
  wins over a parameter segment at the same position; methods are matched
  exactly, with `HEAD` falling back to `GET`. This resolves the eight pairs
  `internal/openapi.knownServeMuxConflicts` pins (customers and products),
  which .NET separated with `{id:int}`/`{id:guid}` route constraints. A true
  duplicate (same method, same pattern) is still a startup error, and every
  contract operation must still be registered. Identity's routes are
  unaffected, and its tests prove it.
- **`MODULES`.** A comma-separated list, parsed once at startup. Identity is
  always enabled and is not listed. A name the binary does not know fails
  startup naming the name and the known set, so `communications` becomes
  valid when sub-project 5 lands and not before. `energy` or
  `communications` without `customers` fails startup naming both. A disabled
  module contributes no handler, no permissions and no workers, and its paths
  answer the `/api` catch-all 404. Every schema is migrated regardless, as
  the parent spec requires.
- **Cross-module reads.** `contracts.CustomerDirectory` is the only way one
  module reads another's data:

  ```go
  type CustomerDirectory interface {
      Customer(ctx context.Context, id int32) (*CustomerEntry, error)
      Contact(ctx context.Context, id int32) (*ContactEntry, error)
      ContactsByEmail(ctx context.Context, email string) ([]ContactMatch, error)
  }
  ```

  `CustomerEntry{ID, Name, Archived}`, `ContactEntry{ID, FirstName, LastName,
  Email}` and `ContactMatch{ContactID, CandidateCustomerIDs}`, mirroring
  .NET's `ContactEmailMatch(int ContactId, IReadOnlyList<int>
  CandidateCustomerIds)`: one match names a contact and every customer it
  could belong to. Archived customers still resolve, because consumers hold
  historical references. More than one match is ambiguous and the caller must
  not choose for the user. Customers
  implements it over its own schema; `Compose` injects it into `Deps` for the
  other modules; it is nil when customers is disabled, which the enablement
  rule already prevents for its consumers. This keeps both invariants: no
  module imports another (depguard), and no module reads another's schema
  (the existing scan test).
- **Schemas and migrations.** One baseline per module on the single goose
  lineage: `00003_customers_baseline.sql`, `00004_products_baseline.sql`,
  `00005_energy_baseline.sql`, hand-written from the EF model, no `tenant_id`
  anywhere. Energy's baseline creates `btree_gist` and the GiST exclusion
  constraint on supply periods verbatim from the inventory. Column types and
  scales carry over exactly (`numeric(12,2)`, `numeric(10,3)`,
  `numeric(10,1)`, `numeric(14,3)`), as do `jsonb` option values and identity
  columns.
- **Conflicts.** `httpx.WriteError` already answers 409 for unique (23505)
  and exclusion (23P01) violations, which is what .NET's host-wide handler
  did. Foreign-key `RESTRICT` violations (23503, and 23001 for a true
  RESTRICT) additionally map to the operation's documented 409 rather than
  .NET's unmapped 500.

### Customers

- Refusals keep .NET's shapes: bare problems and validation text, with no
  `{code, message}` vocabulary. Identity's codes are not borrowed.
- Validation-versus-existence ordering is per endpoint, taken from the
  inventory's tables, not generalised.
- `GET /customers` search matches `Name` only. The doc comment and one test
  name claim legal name and legal id; the test asserts empty results. Go
  ports the behaviour and the test, and the misleading names are not copied.
- Timeline concurrency keeps all three .NET guards: the revision check, the
  concurrency token, and the unique-index backstop, all answering the same
  409.
- Brreg lookup is a port with its upstream base URL, timeout and error
  mapping from configuration; a failure answers 502. Tests use a fake
  transport, as .NET used `StubBrregHandler`; no test touches the network.

### Products

- **Conditional pricing permission.** The contract requires `pricing-view`
  on `postProducts` and `postProductsByIdVariants`. .NET additionally
  requires `products:pricing-manage` when the payload carries pricing. The
  router cannot see that, so the handler checks it through
  `Access.Check` with a permission rule and refuses with the same 403 the
  router would write.
- Money is stored in `numeric` columns and rounded by the column scale, as
  .NET relied on. The wire keeps `format: double`; the contract does not
  change in this sub-project, so the SPA's generated types do not churn.
- Variant option maps are camelCased on the wire, which ASP.NET did by
  default and Go must do explicitly.
- `PUT /products/{id}` keeps ignoring a `variants` field in the body, and
  `GET /products/stats/attention` keeps returning `[]`. Both are parity, both
  are commented as such.
- Price overlap has no database constraint in .NET and gains none here.

### Energy

- Supply-period overlap is decided by the GiST exclusion constraint, not the
  pre-check. The friendly pre-check stays for its message; the constraint
  stays authoritative under concurrency, and its loser answers 409.
- Consumption intervals keep .NET's exact-tuple dedup and gain no overlap
  protection.
- The two aggregation endpoints port the SQL from the inventory verbatim,
  including bucketing, time-zone handling, gaps and rounding.
- `from` and `to` must be full RFC 3339 timestamps. .NET parsed an
  offset-less string in the server's local time zone, which is unportable and
  makes a deployment's behaviour depend on its host; Go answers the
  operation's 400 instead. Recorded divergence.
- Consumption quantities are `numeric(14,3)` in Postgres and `float64` on the
  wire, which is exact for values of this magnitude.

## Data model

Three schemas, `customers`, `products` and `energy`, each owned by its
module, with no cross-schema references in migrations or queries — the
existing scan test already covers all five schemas. Tables, keys, indexes and
constraints come from the inventories, which cite the EF configuration each
was derived from.

## Configuration added

- `MODULES` (comma list; default `customers,products,energy` until
  communications lands).
- `BRREG_BASE_URL`, default `https://data.brreg.no`, which is .NET's
  `Brreg:BaseUrl` default.
- `BRREG_TIMEOUT`, default `15s`: .NET's total-request budget. Its named
  client also set a 20 s outer timeout, which the 15 s budget always
  preempted, so one value replaces both.

Within that budget the lookup retries a failed GET up to 3 times with a 4 s
per-attempt timeout, as .NET's standard resilience handler did. .NET also
wrapped the client in a circuit breaker (30 s sampling, library defaults);
Go does not, because the only Go equivalents are dependencies, the breaker
never appears in a ported test, and a lookup failure already degrades to a
502 rather than cascading. Recorded divergence.

Every value is validated in `internal/config` in the existing
collect-all-problems style, and secrets stay redacted.

## Testing

- **Ported tests: 222** (customers 100, products 79, energy 43), each marked
  `// Ported from <Class>.<Method>`. Tenancy-only tests are dropped.
- Each module gets a harness in the shape of identity's: a real `server.New`
  stack over `module.Compose`, its own migrated database per test, a settable
  clock, and the contract-validating transport. Every exchange is validated
  and every operation must be exercised with a successful response; the
  coverage gate has no allow-list.
- Concurrency is proved with gated tests, as identity's are: the
  supply-period GiST race and the timeline's optimistic concurrency both hold
  under forced interleaving.
- The smoke test gains one probe per module.

## Corrections to the parent spec

- §10.4 attributes a GiST constraint to this sub-project without naming the
  module: it is energy's `supply_periods`. Products has no exclusion
  constraint.
- §3.3's `MODULES` default lists `communications`, which does not exist until
  sub-project 5. The default is the set of modules the binary knows.

## Out of scope

- Communications (sub-project 5), the frontend (6) and the cutover (7).
- Contract changes: the SPA's generated types stay as they are.
- Workers: none of these modules has background work.
