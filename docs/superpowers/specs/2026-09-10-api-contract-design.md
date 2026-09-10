# Vantigo Go port — sub-project 2: the API contract

Date: 2026-09-10
Status: approved in conversation (design presented and accepted before writing)
Parent: `docs/superpowers/specs/2026-09-10-go-backend-port-design.md` (§3.5, §3.6, §4, §10 step 2). The parent spec is the binding authority; this document only settles what the parent leaves open for this sub-project.

## Goal

`openapi/*.yaml` becomes the single source of truth for every API operation the Go server will serve. Both the Go server interfaces and the frontend types are generated from it, and generated code is committed and checked for drift. The shapes are the ones the .NET host serves today, so the existing frontend keeps working unchanged. No endpoint is implemented in this sub-project: the Go server keeps answering 404 on `/api` until sub-projects 3–5 add handlers.

## What the planning spike established

A throwaway harness booted the real host (all modules, single tenant) and dumped `/openapi/v1.json`:

- 212 operations: identity 108, customers 29, communications 29, products 26, energy 20; nothing outside `/api`. Identity's unversioned groups appear once `OpenApiOptions.ShouldInclude` includes every endpoint.
- .NET 10 emits OpenAPI 3.1 by default; with `OpenApiVersion = OpenApi3_0` it emits 3.0.4. oapi-codegen v2.8.0 and openapi-typescript 7.13.0 both generate from that dump without errors.
- Endpoint metadata gives the access rule for every operation: 103 permission, 74 policy (`ActiveAccount` 26, `OwnerManagement` 16, `Owner`+`OwnerManagement` 14, `AuthorizationManagement` 9, `SystemAdmin` 8, `Owner` 1), 5 authenticated-only, 1 explicit anonymous, and 29 with no authorization metadata (the public identity flows, 13 SCIM endpoints that use a bearer token, antiforgery, and the Mailgun webhook).
- Only 6 operations have an operationId.
- 142 operations have no 2xx response schema (identity 108, communications 26, customers 6, products 2), because their handlers return `Task<IResult>`. They do build their bodies from typed records (identity alone has 33 `*Response` records), so the schemas exist in code.
- 19 of 102 write operations declare no request body.

## Decisions

### Extraction (a .NET harness, deleted at cutover)

A test in `Vantigo.Host.Tests`, run only when `VANTIGO_OPENAPI_DUMP` names an output file, boots the host with every module enabled and single tenancy and writes the v1 document with:

- `OpenApiVersion = OpenApi3_0` and `ShouldInclude = _ => true`.
- `x-vantigo-access` on every operation, from endpoint metadata:
  - `IAllowAnonymous`, or no authorization metadata at all → `anonymous`
  - a SCIM path (`/api/v1/identity/scim/v2/…`) → `scim`
  - `PermissionMetadata` → `permission:<key>[+<key>…]` (every key on the endpoint, sorted, all required)
  - one or more authorization policies → `policy:<Name>[+<Name>…]` (sorted, all required)
  - `RequireAuthorization()` with no policy → `session`
- an `operationId` on every operation: the endpoint name when one is set, otherwise derived deterministically from the method and route (`GET /api/v1/customers/{id}` → `getCustomer…`-style camelCase built from the path segments), unique across the document.
- component schemas for every public or internal `*Request`/`*Response` type in the module assemblies (via the document transformer's schema generation), so curation can reference generated schemas rather than hand-writing them. Schema ids are unique per CLR type: a nested type's id carries its declaring types' names (`AttachCustomerContactEndpoint.Request` → `AttachCustomerContactRequest`), because the framework's bare type name would merge every endpoint-local `Request` into one schema; the harness fails if two types still claim one id.

The harness output is an input to curation, not a build artefact. After curation the YAML is hand-maintained. Routes served by authentication middleware rather than endpoints do not appear in the dump: the OpenID Connect redirect URI, `/api/v1/identity/oidc/callback` (`GET` with query parameters, `POST` with a form for the default `form_post` response mode), is added to `identity.yaml` by hand.

### Layout

```
openapi/
  common.yaml          shared components: problem documents, validation problem, pagination, shared enums
  identity.yaml
  customers.yaml
  products.yaml
  energy.yaml
  communications.yaml
```

Each module file holds that module's paths and the schemas only it uses; anything used by two or more modules lives in `common.yaml` and is referenced with a relative `$ref` (`common.yaml#/components/schemas/Problem`). OpenAPI version 3.0.x throughout.

`x-vantigo-access` is required on every operation. Allowed values: `anonymous`, `session`, `scim`, `policy:<Name>[+<Name>…]` with names from the identity policy set (`ActiveAccount`, `SystemAdmin`, `Owner`, `OwnerManagement`, `Business`, `AuthorizationManagement`), and `permission:<module>:<verb>[+<module>:<verb>…]` (every listed permission is required, sorted, joined with `+`; endpoints chain `RequirePermission`, and the calls AND together).

### What is dropped while curating

Tenancy (`/api/v1/identity/admin/tenants…`, `/api/v1/identity/tenants/current/capabilities`, `POST /api/v1/identity/session/tenant`), `GET /api/v1/identity/antiforgery`, and `POST /api/v1/communications/inbound/mailgun/{channelId}`. Attachment-scanning fields that appear in response shapes stay for now; sub-project 5 decides their fate with the rest of Communications.

### Curation and its proof

Curation fills every missing response body (2xx and the error responses the endpoint actually returns), request bodies, and parameter details, using the generated component schemas and the endpoint code as the source.

Correctness is verified, not asserted: the existing .NET integration suites (about 740 tests) record every request/response exchange they make when `VANTIGO_CONTRACT_RECORD` names a directory, and a Go test validates each recorded exchange against the curated YAML with kin-openapi (`openapi3filter` request and response validation). Exchanges against dropped operations are ignored. The recorded corpus is committed under `openapi/testdata/exchanges/` so the check runs in CI without .NET, and later sub-projects can replay it against the Go handlers.

Definition of done for curation: every operation has a success response — a 2xx with a schema, a 204, a 2xx the handler sends without a body and that is marked `x-vantigo-empty-body: true`, or, for a redirect endpoint (the OIDC flow), a 3xx declaring its `Location` header — and an `x-vantigo-access`; every recorded exchange validates; an operation with no recorded exchange is listed in a committed coverage report (`openapi/COVERAGE.md`) rather than silently trusted.

### Go

- oapi-codegen v2.8.0, per module: `models` into `internal/<module>/gen/types.gen.go`, `std-http-server` + `strict-server` into `internal/<module>/gen/server.gen.go`; `common.yaml` generates `internal/apicommon/types.gen.go`, wired to the modules through `import-mapping`.
- `internal/openapi` embeds copies of the spec files (copied by `go generate`, since `go:embed` cannot reach above the module root) and exposes a loader that resolves the relative `$ref`s from the embedded files with kin-openapi. Mounting request validation and serving `/api/openapi.json` + Scalar are sub-project 3, which introduces sessions and the first real routes.
- Tests: the embedded copies equal `openapi/*`; the specs load and validate as OpenAPI 3.0; every operation's `x-vantigo-access` is present and well-formed; the recorded-exchange validation above.
- Generated code is committed. CI regenerates it and fails on a diff.

### Frontend

`bun run gen:client` runs openapi-typescript 7.13.0 per module file into the module's frontend package: `apps/host/frontend/src/api-schema.d.ts` (identity — the host owns those screens), `apps/customers/frontend/src/api-schema.d.ts`, and likewise for products, energy and communications. Generated files are committed and excluded from biome; CI regenerates them and fails on a diff. No call sites change.

### CI and tooling

- `mise.toml` pins `go:github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen` 2.8.0 for local `go generate`; CI never runs a `go:` tool through mise — the drift jobs use `go run …@v2.8.0`, like govulncheck.
- `server-test.yml` gains the Go drift check; the frontend job in `ci.yml` gains the `gen:client` drift check (it already runs lint and tests over the frontend packages).

## Out of scope

Handlers, sessions, request-validation mounting, the spec endpoint and Scalar (sub-project 3); frontend call-site migration to a typed client; frontend de-tenanting (sub-project 6); deleting the .NET code (sub-project 7).

## Risks

- **Curation volume.** 142 operations need response schemas. The generated record schemas and the recorded-exchange validation turn this from writing into linking and checking, but it remains the bulk of the work; the plan splits it by module and, for identity, by area.
- **Recording coverage.** An operation no .NET test calls has no exchange to validate against. The coverage report makes those visible; they are reviewed by hand from the endpoint code.
- **oapi-codegen and multi-file `$ref`s.** Cross-file references need `import-mapping`; the plan proves this with `common.yaml` before any module is curated.
