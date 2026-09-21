# Can This Customer Receive EHF? (delivery B) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ask the Peppol network whether a customer is a registered EHF (Peppol BIS Billing 3.0) receiver, remember the answer, warn when the billing profile contradicts it, and offer a one-click switch to EHF.

**Architecture:** A shared `internal/peppol` package (participant-host hashing, a NAPTR resolver built on `golang.org/x/net/dns/dnsmessage`, a guarded SMP client) behind a `module.Deps.PeppolLookup` seam; the forbidden-address table moves from `internal/mail` to a new `internal/netguard`; customers gets `POST /customers/{id}/peppol-lookup`, a `customer_peppol_lookups` table (off the customer row, so no `revision` bump), a `peppolLookup` summary and two warnings on the billing profile; the Billing card gets Check EHF / Use EHF.

**Tech Stack:** Go 1.27 (pgx, sqlc, oapi-codegen, goose, `golang.org/x/net/dns/dnsmessage`), PostgreSQL 18, React + Mantine + TanStack Query, vitest, bun, mise.

**Spec:** `docs/superpowers/specs/2026-09-21-customers-peppol-lookup-design.md` (D1–D6, plus "What the network looks like" — every string there was verified live and is to be used verbatim). Builds on delivery A's spec and `docs/customers.md`.

## Global Constraints

- Branch `feat/customers-peppol-lookup`. Never commit to `main`, never merge, never `--no-verify`.
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

### Task 1: One table of forbidden addresses — `internal/netguard`

**Files:**
- Create: `apps/server/internal/netguard/netguard.go`, `netguard_test.go`
- Modify: `apps/server/internal/mail/guard.go` (delegate the address classification; keep `destinationGuard`, `ErrDestinationRejected`, `allowLoopback` and every message as they are), its tests only if they reach the moved unexported functions, `.golangci.yml` only if a depguard rule would forbid `internal/mail` or `internal/peppol` importing `internal/netguard`

**Interfaces:**
- Produces: `func Disallowed(addr netip.Addr) bool` (the moved `disallowedDestination` + its IPv4/IPv6 helpers, unchanged ranges: private, loopback, link-local, CGNAT, metadata 169.254.169.254, unspecified, multicast, IPv4-mapped IPv6 unwrapped first, …); `type Resolver interface{ LookupNetIP(ctx, network, host string) ([]netip.Addr, error) }`; `var ErrDisallowed = errors.New("netguard: destination not allowed")`; `func DialContext(resolver Resolver, dial func(ctx context.Context, network, addr string) (net.Conn, error)) func(ctx context.Context, network, address string) (net.Conn, error)` — splits host:port, resolves, refuses when ANY resolved address is disallowed (or none resolve), and dials **the first checked IP literal**, never the hostname again.

- [ ] Move the range table with its table-driven tests (every existing case must survive verbatim; add IPv4-mapped IPv6 of a private address and `::ffff:169.254.169.254` if not already there).
- [ ] `DialContext` tests with a fake resolver and a fake dial func: public address → dialed as `ip:port`; one private among several → refused with `ErrDisallowed` and the dial func never called; literal IP host handled without a lookup; resolution error propagated; empty answer refused.
- [ ] `internal/mail` tests green untouched in behaviour; `go vet ./...`; `golangci-lint run` from `apps/server`.
- [ ] Commit: `refactor(netguard): one table of addresses the server will not dial`.

### Task 2: `internal/peppol` — discovery and capability lookup (D1, D2)

**Files:**
- Create: `apps/server/internal/peppol/participant.go` (+test), `naptr.go` (+test), `resolver.go` (+test), `smp.go` (+test), `lookup.go` (+test), `doc.go`
- Modify: `apps/server/go.mod`/`go.sum` (`golang.org/x/net` becomes a direct requirement — `mise exec -- go mod tidy`), `.golangci.yml` if depguard needs an allowance

**Interfaces:**
- Produces:
```go
const Scheme = "iso6523-actorid-upis"
func ParticipantHost(zone, value string) string // value e.g. "0192:923609016"; lower-cases value, sha256, base32 std upper, strip "="
type NAPTR struct{ Order, Preference uint16; Flags, Service, Regexp, Replacement string }
func decodeNAPTR(rdata []byte) (NAPTR, error)          // never panics on malformed input
func smpBaseURL(records []NAPTR) (url string, ok bool, err error) // sort by order then preference; flags "U", service "Meta:SMP" (case-insensitive); regexp delimiter is its first byte (`!.*!URL!`); ok=false when no record qualifies (registered, no SMP service); err for a qualifying record whose regexp is malformed
type Resolver struct{ Servers []string; Timeout time.Duration } // Servers empty → /etc/resolv.conf
func (r *Resolver) LookupNAPTR(ctx context.Context, host string) (records []NAPTR, found bool, err error) // NXDOMAIN → found=false, nil
type Client struct{ … }
func NewClient(opts Options) *Client // Options{Zone string; DNSServers []string; Timeout time.Duration; HTTPTransport http.RoundTripper /* nil → guarded transport */}
type Result struct{ Registered bool; SMPHost string; CanReceiveInvoice, CanReceiveCreditNote bool }
func (c *Client) Lookup(ctx context.Context, participant string) (Result, error)
```
- Consumes: `netguard.DialContext` for the default transport.

Rules (all from the spec, verbatim): hash the VALUE only; zones; NXDOMAIN = not registered; SMP URL policy (https, no userinfo, port 443 or default, else error); `strings.TrimRight(base, "/")`; request path `/<url.PathEscape("iso6523-actorid-upis::"+value)>` with `:` → `%3A` and `#` → `%23` forced; **no `Accept` header**; no redirects (`CheckRedirect` returns `http.ErrUseLastResponse` and a 3xx is an error); body capped at 1 MiB (`io.LimitReader`, over-cap = error); 404 → `Registered=false`; other non-2xx → error; parse `ServiceGroup` in namespace `http://busdox.org/serviceMetadata/publishing/1.0/`, collect `ServiceMetadataReference/@href`, take the segment after `/services/`, `url.PathUnescape` it, and compare the FULL identifier string for equality against the spec's two-entry allow-list (never a substring, prefix or wildcard match — see the spec's reminder-profile and no-invoice-doctype examples). DNS: UDP query with EDNS0 not required; on TC bit retry over TCP; 3 s per attempt; two attempts per server; SERVFAIL/REFUSED/timeouts = error; ids randomised; answers for other names/types ignored.

- [ ] Tests first: hash vectors (`0088:123abc` → `Y7DZFXAF3D4CJZ4KCGRXTEC6TWVCGA4KY7ZWA5BOIF6MSWD4TDRQ`; `0192:923609016` → `XQK4T3FMTEZDUY5BOVJMTNAQ45N7E4TIBYHWFPGOT75BSP7UYG2A`; upper/lower-case input gives the same host); NAPTR decode of a hand-built RDATA for `100 10 "U" "Meta:SMP" "!.*!https://smp.elma-smp.no/!" .`, truncated/oversized-length inputs return errors (add a `FuzzDecodeNAPTR` seed corpus; it must never panic); `smpBaseURL` ordering, case-insensitive service, other delimiters, records without `U`/`Meta:SMP` skipped, none usable → ok=false (Lookup then reports Registered=true with no capabilities and makes no HTTP call); resolver against an in-process stub (`net.ListenPacket("udp","127.0.0.1:0")` + TCP listener on the same port): answer, NXDOMAIN, SERVFAIL, truncated-then-TCP, no reply → timeout within the ctx; SMP client against `httptest.NewTLSServer` with `Options.HTTPTransport` pointing at it: base with and without trailing slash, asserts NO `Accept` header and the exact escaped path, 404, 500, 302 not followed, 2 MiB body refused, a realistic ServiceGroup fixture (17 hrefs incl. both billing ids), a fixture with ONLY the EHF reminder profile id (`…billing:3.0#conformant#urn:fdc:anskaffelser.no:2019:ehf:reminder:3.0::2.2`) → cannot receive, a wildcard-PINT-only fixture → cannot receive, an order-profiles-only fixture → registered but cannot receive, invoice-without-credit-note; URL policy table (http://, :8443, user@, empty, garbage).
- [ ] `Lookup` end to end with the stub DNS + httptest SMP.
- [ ] One test for the DEFAULT transport: it is built on `netguard.DialContext` (dialing a name that resolves to 127.0.0.1 is refused with `netguard.ErrDisallowed`) — use an injected `netguard.Resolver` fake, not real DNS.
- [ ] `go vet`, lint, `go test -race -count=1 ./internal/peppol/... ./internal/netguard/...` (race via the zig cc wrapper if available, else plain). Commit: `feat(peppol): find a participant's SMP and what it can receive`.

### Task 3: Customers — the lookup, its memory and the warnings (D3, D4, D5)

**Files:**
- Create: `apps/server/internal/db/migrations/00020_customers_peppol_lookup.sql`, `apps/server/internal/customers/peppol_lookup.go`, `peppol_lookup_test.go`, `queries/peppol.sql`
- Modify: `internal/config/config.go` (+tests: the four variables, defaults, validation: zone non-empty hostname, DNS server `host:port`, timeout > 0), `internal/module/module.go` (`Deps.PeppolLookup`), wherever Deps is built for production and in `internal/modtest` (an option `WithPeppolLookup(fn)`), `internal/customers/server.go` (build the real client from config when the seam is nil and the feature is enabled), `billing_profile.go` + `billing_values.go` (summary + two warnings), `timeline_events.go`, `sqlc.yaml`, `openapi/customers.yaml`, generated files, `openapi/COVERAGE.md`

```sql
-- +goose Up
-- The last answer the Peppol network gave about a customer (peppol lookup
-- design D3). Its own table, not columns on customers.customers: recording an
-- answer must never bump the row's revision and conflict somebody's open form.
CREATE TABLE customers.customer_peppol_lookups (
    customer_id             integer      PRIMARY KEY REFERENCES customers.customers (id) ON DELETE CASCADE,
    participant_id          varchar(60)  NOT NULL,
    status                  varchar(20)  NOT NULL,
    can_receive_invoice     boolean      NOT NULL,
    can_receive_credit_note boolean      NOT NULL,
    smp_host                varchar(255),
    checked_at              timestamptz  NOT NULL
);

-- +goose Down
DROP TABLE customers.customer_peppol_lookups;
```

**Interfaces:**
- Produces: operation `postCustomersByIdPeppolLookup`, access `permission:customers:billing-manage+customers:view`, no request body, 200 → `CustomerPeppolLookup {status, canReceiveInvoice, canReceiveCreditNote, participantId?, smpHost?, checkedAt}` (required: `status`, `canReceiveInvoice`, `canReceiveCreditNote`, `checkedAt`; for `no_identifier` nothing is stored and `checkedAt` is now), 404, 502 and 503 `ProblemDetails`; `CustomerBillingProfile.peppolLookup` optional (same schema); warning codes `ehf_recipient_not_registered`, `ehf_available` appended AFTER the existing four in that order.
- Go: `func (s *server) lookupParticipant(profile billingProfile, identity *legalIdentity, customerType string) (participant string, derived bool)`; `billingWarnings` gains the stored lookup (nil when none or stale).

Rules: participant = explicit `peppolId` → else `derivedPeppolID` → else `no_identifier` (200, nothing stored, no network call). The network call happens OUTSIDE any transaction, under `PEPPOL_TIMEOUT`; then one transaction upserts the row and records `customer.peppol_lookup` only when status or either capability differs from the stored row (first lookup counts as changed); actor resolved before the transaction. Error from the seam → 502 `"The Peppol network could not be reached"`, nothing stored. Disabled → 503 `"Peppol lookup is disabled"`, seam never called. Stale = stored `participant_id` ≠ the participant that would be looked up now → treated as absent everywhere (summary and warnings). `participantId` omitted from both responses when `derived` and the caller lacks `customers:legal-identity-view` (`smpHost` is fine). `ehf_recipient_not_registered`: delivery `ehf` and a non-stale lookup with status `not_registered`, or `registered` with `canReceiveInvoice=false`. `ehf_available`: non-stale `registered` + `canReceiveInvoice` and delivery is not `ehf`. The customer row's `revision` never moves.

- [ ] Failing tests for every rule above (fake seam recording its calls): each outcome; explicit id wins over derived; `no_identifier` makes no call; 502 keeps the old row; 503 never calls; stale after the org number or `peppolId` changes; withholding with and without `legal-identity-view`, for derived and explicit; both warnings on and off; event only on change, attributed to the user; revision unchanged; 403 without `billing-manage`; 404; config parsing table.
- [ ] Contract → generate → gen:client → queries → handler. `go vet ./...`, full `go test ./...` (Deps changed).
- [ ] Commit: `feat(customers): ask Peppol whether a customer can receive EHF, and remember the answer`.

### Task 4: Frontend — Check EHF and Use EHF (D6)

**Files:**
- Modify: `apps/customers/frontend/src/api/billing-profile.ts` (+test: `peppolLookup` normalised — absent → `null`, inner optional fields → `null`; `checkPeppol(id)`), `src/lib/billing-labels.ts` (+test: the two new warning codes; `ehf_available` is rendered as an OFFER, not in the yellow warnings list), `src/pages/-customer-billing-card.tsx` (+test), `src/i18n.ts` (en + nb)

- [ ] Tests first: the card shows the last answer in words with its date for each status (`registered` + invoice, `registered` without invoice, `not_registered`), nothing when never checked; **Check EHF** only with `canManageBilling`; click → `POST …/peppol-lookup`, pending state, then the billing-profile query refetched (assert by filtering fetch calls, never "the last fetch"); `no_identifier` → "There is no Peppol id or Norwegian organisation number to look up"; 502 → "The Peppol network could not be reached. Try again." with the card intact; 503 → the action disappears and a dimmed "Peppol lookup is switched off on this installation" shows; `ehf_available` → a blue/teal `Alert` "This customer can receive EHF" with **Use EHF** (needs `canManageBilling`) that PUTs the full current profile with `invoiceDelivery: "ehf"` + the profile's `revision`, handles a 409 with the delivery-A conflict wording, calls `syncCustomerRevision`, and afterwards the offer is gone; `ehf_recipient_not_registered` in the yellow list; a fixture that is literally what the server sends for a never-checked profile (no `peppolLookup` key) and one with `participantId` withheld.
- [ ] Checks: customers + host tests/typecheck/lint, root biome, `translations:check`, `i18n:test`. Commit: `feat(customers-ui): check whether a customer can receive EHF, and switch to it`.

### Task 5: Docs

**Files:** `docs/customers.md` (the lookup: what is asked and of whom, NAPTR/zone facts in brief with a pointer to the spec, the guard, the table and why it is off the row, staleness, withholding, the two warnings, 502/503, config), `ROADMAP.md` (Customers phase 2 → done; note scheduled re-checks under phase 3), `docs/module-boundaries.md` (new shared packages `internal/peppol`, `internal/netguard` if it lists shared packages), `CONTRIBUTING.md` (env vars next to `BRREG_*`; operation count), `docs/customers-authentication.md` (env table rows), `deploy/compose/vantigo.env.example` (the four variables, commented like `BRREG_BASE_URL`; a note that outbound DNS and HTTPS 443 are needed), `docs/transport-security.md` only if it enumerates outbound destinations.
- [ ] Commit: `docs(customers): the Peppol lookup, its configuration and what it needs from the network`.

### Task 6: Verify and open the PR

- [ ] `go generate ./...` + `bun run gen:client` → no drift; `go vet ./...`; `golangci-lint run` from `apps/server`; `go test -count=1 ./...`; race on four CPUs for `./internal/peppol/... ./internal/netguard/... ./internal/mail/... ./internal/customers/... ./internal/module/... ./internal/config/...`; `mise run frontend:check`.
- [ ] **Check every commit's trailer BEFORE the first push** (`git log --format='%(trailers:key=Co-Authored-By,valueonly)' main..HEAD | sort | uniq -c`); normalise with `git filter-branch --msg-filter` if needed.
- [ ] One manual smoke of the real network from the dev machine, outside the test suite: a throwaway `go run` that calls `peppol.NewClient(...).Lookup` for `0192:923609016` against production (expect registered + invoice + credit note via `smp.elma-smp.no`) and for a valid-but-unregistered number (expect not registered). Report the output in the PR; do not commit the program.
- [ ] Push, `gh pr create` with the decisions list, watch checks, fix root causes on the same PR. Never merge.
