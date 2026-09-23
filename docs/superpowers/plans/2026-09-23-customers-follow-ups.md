# Follow-ups (phase 4, delivery C) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a manual timeline entry carry a **follow-up** — a due date and, optionally, an assignee — that can be ticked done, so a customer's timeline answers "what happens next": due and overdue follow-ups reach `/stats/attention` for the caller, and a **Follow-ups** page answers "what is on my plate". The same branch removes the contact association's deprecated `role` alias, which the user approved now that nothing is live.

**Architecture:** Three nullable columns on `customers.customers_timeline_entries` **and** on its revisions table (migration `00026`), so a follow-up is part of the entry it belongs to and history stays point-in-time. It rides on the entry's own create/update under the entry's own `expectedRevision`; ticking it done and reopening it are two new paths that take **no** expected revision — a tick from a list must not lose a race with an edit of the note — but each is still a revision of the entry, so `current_revision` bumps and a revision row records who ticked it. Assignee display names come from `contracts.UserDirectory` in one batched call per response, outside every transaction, exactly as the owner's do. `/stats/attention` gains two caller-dependent types computed from state on every call; `GET /customers/follow-ups` is an offset-paginated list, the same shape `GET /customers` has. The frontend generalises `OwnerPicker` into a `UserPicker` (the owner card keeps working through a thin wrapper), adds a Follow-up section to the entry form, a follow-up line with Done/Reopen to each entry, a **Follow-ups** page, and a new `canManageTimeline` capability prop the host derives from `customers:timeline-manage`.

**Tech Stack:** Go 1.27 (pgx, sqlc, goose, oapi-codegen strict server), PostgreSQL 18, React + Mantine 9 + TanStack Router/Query, vitest, bun, mise.

**Spec:** `docs/superpowers/specs/2026-09-23-customers-follow-ups-design.md` (D1–D5 + "Out of scope" + "Testing"). Read it first; it is binding. **D6 — the five code-scanning alerts — is NOT part of this plan:** another agent is committing them on this same branch as their own commit. Do not touch `identity/cookies.go`, `identity/passwords.go` or `secrets/secrets.go`. The spec builds on `docs/superpowers/specs/2026-09-23-customers-contact-roles-design.md` (delivery B, whose `role` alias this plan removes) and on `docs/customers.md`.

## Global Constraints

- Branch `feat/customers-followups` (already checked out; the design commit `1d83efe` is its tip). Never commit to `main`, never merge, never `--no-verify`.
- **Forbidden git commands:** `git add -A`, `git add .`, `git stash` (even path-limited), `git checkout -- .`, `git restore .`, `git clean`, `git reset --hard`. Commit with an explicit pathspec (`git add <files>` then `git commit -F <msgfile> -- <files>`), then check `git show --stat HEAD` and that `git status --short` shows nothing of yours left. The untracked `go.mod`/`go.sum` in the repo root are not ours: never add or delete them.
- **A second agent is committing D6's GHAS fixes on this branch.** Before every commit, check `git status --short` and `git log --oneline -3`: commit only your own pathspec, and if the index already holds somebody else's file, wait rather than sweeping it in. **One implementer commits at a time.**
- Commit messages: Conventional Commits scoped `customers` / `customers-ui` / `docs`, subject a plain sentence about behaviour (see `git log`). End every message with `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>` exactly — the trailer every commit on this branch and on `main` carries. Never substitute another model name: an implementer that wrote its own once had the commit amended before push.
- Toolchain only through mise: `mise exec -- go …`, `mise exec -- bun …`. Capture exit codes before any pipe (`${PIPESTATUS[0]}`).
- Go tests need `TEST_DATABASE_URL` exported in the environment before the first `go test`. **Take the value from the environment** (the project's own test database); this plan does not name one. If it is unset, stop and ask — do not guess a port.
- **The contract break is approved for `role` and for nothing else.** The user approved removing `role` from the four contact-association schemas because the API is not live. Every other contract change in this plan is **additive**: new schemas, new optional properties, new operations, new query parameters. Nothing else is removed from any `required:` list and no existing property changes type.
- **The frozen corpus `openapi/testdata/exchanges/customers.jsonl` is never edited, for any reason.** It records `POST /customers/{id}/contacts` bodies of the shape `{"contactId": N, "role": "CEO"}` and GET lists whose items carry `role`. No schema in `openapi/customers.yaml` sets `additionalProperties: false`, so those recorded requests and responses still validate against schemas that no longer declare `role` — an unknown property is simply ignored. **`TestRecordedExchangesMatchTheContract` (`apps/server/internal/openapi/`) staying green is the proof, and it is a required step of Task 1.** If it ever goes red, the answer is to fix the schema, never to edit the corpus.
- **A follow-up exists only on a manual, active entry.** `provenance = 'manual'` and `state = 'active'`: an interaction or a note is where "call back on Friday" belongs, and a generated event never asks anyone to do anything. The create/update path cannot reach a generated entry at all (both already answer 409 `Timeline entry is immutable`); the two done paths answer the same 409 for one, and 404 when the entry carries no follow-up.
- **Done and reopen are idempotent, take no `expectedRevision`, and bump `current_revision` with a revision row when they change something.** Ticking an already-done follow-up (or reopening an open one) answers 200 with the entry unchanged and **writes nothing** — the module's own no-op rule (customers foundation design D5), which is also why no second revision row appears. The guarded `UPDATE`'s own `WHERE follow_up_done_at IS NULL` (respectively `IS NOT NULL`) is the concurrency guard: two concurrent ticks serialize on the row, the loser matches no row, re-reads and answers 200. The unique index on `(entry id, revision number)` stays the backstop it already is.
- **The actor is resolved before the transaction and only when a write will happen** (`s.actorFor(ctx, manualFallbackActor)`, `actor.go`). An idempotent no-op resolves no actor at all.
- **No directory call ever runs inside a transaction or under a lock** (`actor.go`'s rule, `owner.go`'s doc comment). The assignee is validated against `contracts.UserDirectory` **before** `db.WithTx` opens; assignee display names are resolved **after** a write commits, in one batched `Users(ids)` call per response, never one per row.
- **Dates are strict and UTC.** `followUp.dueOn` is parsed with `parseISODate` (`timeline.go`) — a four-digit-year `yyyy-MM-dd` and nothing looser — and **may be in the future**, unlike `occurredOn`. "Today" is always `civilDate(s.deps.Clock())`, midnight UTC. An attention item's `occurredAt` is its `dueOn` at midnight UTC.
- **The attention list is caller-dependent, and that is new for this module.** `GetCustomersStatsAttention` reads the principal from the context with `contracts.PrincipalFrom(ctx)`, as time's and expenses' items do; the two follow-up types only ever report follow-ups **assigned to the caller or unassigned**. Everything else about `/stats/attention` is unchanged: computed from state on every call, no dismiss state, gated on plain `customers:view`.
- **Never assert "the last fetch"** in a frontend test — debounced pickers run on their own clock and CI is slow. Filter by method and URL instead. Two different call lists exist and they are not interchangeable: a **per-route `vi.fn` spy** you wrote yourself and call from the handler has `spy.mock.calls`, whose element `[0]` is whatever you passed it; the value **`stubFetch` itself returns** (`src/test/fetch.ts`) carries `calls` (everything, session bootstrap included) and `actualCalls` (everything but the bootstrap), each an array of `[input, init]` pairs — those two are what this plan's tests read. Use `returned.actualCalls.find(([url, init]) => …)`, never a positional index and never "the last call".
- A Mantine `Select`/`MultiSelect` is queried as `getByRole("combobox", { name })`, never as a textbox; a plain `TextInput` is a textbox; a `Checkbox` is `getByRole("checkbox", { name })`; a `Switch` renders `<input type="checkbox" role="switch">` in Mantine 9.5, so it is `getByRole("switch", { name })`. Mantine popovers/modals/selects need `<MantineProvider>` (the existing tests already wrap in one). `DateInput` is a textbox.
- **Both catalogs** (`en` and `nb`) get every new string: `apps/customers/frontend/src/i18n.ts` for the package, `apps/host/frontend/src/catalogs/navigation.ts` and `catalogs/dashboard.ts` for the host. `mise exec -- bun run translations:check` and `mise exec -- bun run i18n:test` must pass.
- After any `openapi/*.yaml`, `queries/*.sql` or migration change: `cd apps/server && mise exec -- go generate ./...` (a second run must show no new diff), then from the repo root `mise exec -- bun run gen:client` for a yaml change. Commit every generated file (`apps/server/internal/openapi/specs/customers.yaml`, `internal/customers/gen/api.gen.go`, `internal/customers/store/*.go`, each changed `api-schema.d.ts`). This delivery adds three operations, so `openapi/COVERAGE.md` **does** move: regenerate it with `go run ./internal/openapi/cmd/contract coverage -corpus ../../openapi/testdata/exchanges -out ../../openapi/COVERAGE.md` from `apps/server`.
- After the migration: add it to `apps/server/internal/customers/sqlc.yaml`'s `schema:` list (`TestSqlcSchemaListsOnlyTheModulesOwnMigrations` requires the list to be exactly this module's migrations) and extend `internal/db/schema_test.go`, then `go generate`.
- **Read the generated file before writing Go against it.** After `go generate`, open `apps/server/internal/customers/store/follow_ups.sql.go`, the changed parts of `store/timeline.sql.go` and `store/models.go`, and `internal/customers/gen/api.gen.go`, and check which queries took a bare argument and which a `…Params` struct, the exact integer widths, and the spelling of every field (`FollowUpOn`, `FollowUpAssigneeUserID`, `FollowUpDoneAt`, `EntryID`, `CallerID`, `FollowUpState`, `AssigneeNone`, `RowOffset`). Where the generated code disagrees with this plan's Go, **follow the generated code** and adjust the call, not the query.
- **Every operation in the contract must be exercised by a successful exchange** (`contracttest.RequireCoverage` in `internal/customers/main_test.go`, with no allow-list). The three new operations therefore each need at least one modtest case that answers < 400 — and `pendingOperations` is not an option: `main_test.go` passes none today and must keep passing none.
- After any exported Go signature change: `mise exec -- go vet ./...` and grep every caller including tests and fakes. `contracts.CustomerDirectory` is **not** touched by this delivery.
- Match the surrounding code: comment density and voice (these files explain *why*), naming, error wording, test style. Body-level validation messages have **no** trailing period (`values.go`, `timeline.go`); query-parameter messages do (`customers.go`).
- Every new test must be shown able to fail (break the guard by hand, see red, restore by hand, see green). **Never `git stash`** — restore by hand-editing back. Say in the report what each mutation printed.
- **Every Go listing in this plan is written for readability, not to gofmt's alignment.** Run `mise exec -- gofmt -l <dir>` before each commit that touches Go, `gofmt -w` whatever it names, and commit the formatted version — `golangci-lint` fails on unformatted code. The same applies to the TypeScript listings and `bunx biome check .`.

---

## File Structure

| File | Responsibility |
| --- | --- |
| `apps/server/internal/db/migrations/00026_customers_timeline_follow_up.sql` | the three columns on the entries **and** revisions tables, and the two partial indexes the list and the attention query read through |
| `apps/server/internal/customers/queries/follow_ups.sql` | the two done/reopen guarded writes, the attention candidates, and the list's count/page pair |
| `apps/server/internal/customers/queries/timeline.sql` | the three columns in every existing timeline statement's select list, insert, update and revision snapshot |
| `apps/server/internal/customers/follow_ups.go` | the follow-up's validation, the assignee's directory checks, the response projection and the batched decoration, the two done handlers, the attention items, the list handler |
| `apps/server/internal/customers/timeline.go` | `followUp` threaded through validation, the create/update writes, and both response projections |
| `apps/server/internal/customers/stats.go` | the two new attention types merged into `/stats/attention`, with the caller read from the context |
| `openapi/customers.yaml` | `role` removed from four schemas; four new schemas; `followUp` on the manual request and both response shapes; three new operations; `assignable-users` relaxed to `customers:view` |
| `apps/customers/frontend/src/api/timeline.ts` | `followUp` on the entry, the input and the revision, the boundary normaliser, and the two done mutations |
| `apps/customers/frontend/src/api/follow-ups.ts` | the Follow-ups page's list query and its filter vocabulary |
| `apps/customers/frontend/src/components/user-picker.tsx` | `UserPicker` — the searchable directory `Select`, generalised out of `OwnerPicker` |
| `apps/customers/frontend/src/components/owner-picker.tsx` | `OwnerPicker` as a thin wrapper over `UserPicker`, so the Relationship card is untouched |
| `apps/customers/frontend/src/pages/-customer-timeline.tsx` | the Follow-up section of the form, the follow-up line, Done/Reopen, `canManageTimeline` gating, the follow-up per revision |
| `apps/customers/frontend/src/pages/follow-ups.tsx` | `FollowUpsPage`: the two filters ↔ URL, the table, the per-row Done tick |
| `apps/customers/frontend/src/pages/customers.$customerId.tsx` | `canManageTimeline` passed from `CustomerOverview` down to the timeline card |
| `apps/host/frontend/src/apps.ts`, `catalogs/navigation.ts` | the **Follow-ups** sidebar entry under Customers |
| `apps/host/frontend/src/routes/customers/follow-ups.tsx`, `routes/customers/-follow-ups-page.tsx` | the route, its search params, and the `canManageTimeline` capability prop |
| `apps/host/frontend/src/routes/customers/-customer-overview-tab.tsx` | `canManageTimeline` derived from `customers:timeline-manage` |
| `apps/host/frontend/src/routes/dashboard.tsx`, `catalogs/dashboard.ts` | the two attention sentences, en + nb |
| `docs/customers.md`, `ROADMAP.md` | the follow-ups section, the timeline/attention/permissions/API tables, the one-sentence `role` note, phase 4 delivery C done |

---

### Task 1: The approved contract break — `title` only, and `assignable-users` on `customers:view` (D5, D1)

The user approved removing the deprecated `role` alias now that the API is not live. This task removes it from the contract, from Go, from the frontend and from the docs, in one commit, and relaxes `GET /customers/assignable-users` in the same commit because both are contract changes that generation has to run over once. **The frozen corpus is not edited**, and the exchanges test staying green is this task's own proof.

**Files:**
- Modify: `openapi/customers.yaml`, `apps/server/internal/customers/values.go`, `apps/server/internal/customers/contact_roles.go`, `apps/server/internal/customers/contacts.go`, `apps/server/internal/customers/contacts_timeline.go`, `apps/server/internal/customers/owner.go`, `apps/server/internal/customers/values_test.go`, `apps/server/internal/customers/contact_roles_test.go`, `apps/server/internal/customers/contacts_test.go`, `apps/server/internal/customers/owner_test.go`, `apps/customers/frontend/src/api/contacts.ts`, `apps/customers/frontend/src/api/contacts.test.ts`, `apps/customers/frontend/src/pages/-contacts.test.tsx`, `docs/customers.md`
- Deliberately **not** modified: `openapi/testdata/exchanges/customers.jsonl` — frozen, for any reason
- Generated: `apps/server/internal/openapi/specs/customers.yaml`, `apps/server/internal/customers/gen/api.gen.go`, every changed `api-schema.d.ts`, `openapi/COVERAGE.md`
- Read first (do not change): `apps/server/internal/openapi/exchanges_test.go` (what the corpus gate actually checks), `openapi/customers.yaml:3-30, 351-421, 721-746`

**Interfaces:**
- Produces (contract): `AttachCustomerContactRequest`, `CustomerContactRequest`, `CustomerContactResponse` and `GetContactCustomersContactCustomerResponse` with **no** `role` property; `title` still optional on the requests and nullable on the responses; `role` gone from both responses' `required:` lists, leaving `[contact]` and `[customer]`. `getCustomersAssignableUsers`'s `x-vantigo-access` becomes `permission:customers:view`.
- Produces Go: `validateCustomerContactRequest(title *string, roles *[]gen.CustomerContactRoleRequest, phone, email *string, rolesWhenOmitted int) (validatedAssociation, map[string][]string)` — one parameter fewer. `validateContactRole` no longer exists. `gen.CustomerContactResponse` and `gen.GetContactCustomersContactCustomerResponse` lose their `Role string` field. `recordContactEvent`'s payload loses `"role"` and its payload version is **2**.
- Produces TypeScript: `CustomerContactResponse` and `ContactCustomerResponse` lose `role`; `titleOf` no longer exists — `normalizeCustomerContact`/`normalizeContactCustomer` read `raw.title ?? null`.

- [ ] **Step 1: Write the failing tests first — the Go side**

Three edits, all of them tests, and each fails against today's code.

1. In `apps/server/internal/customers/values_test.go`, **delete** `TestValidateContactRole_TrimsAndSucceeds`, `TestValidateContactRole_BlankIsInvalid` and `TestValidateContactRole_TooLongIsInvalid` (lines ~647-677, comments included), and replace the comment above `TestValidateContactTitle_MirrorsTheRoleRuleUnderItsOwnNoun` (lines ~1055-1061) with:

```go
// The title's rule is the rule the association's free text has always had
// (typed contact roles design D1: "Validation of the title is today's"), now
// under the only noun the contract still has. The deprecated `role` alias, and
// its own three tests, went with the follow-ups delivery's approved contract
// break (follow-ups design D5): the API is not live, so there was nobody to
// keep an alias for. associationTitleRule stays parameterised by the noun
// anyway — it costs a string and it is the seam any future second name would
// use.
```

2. In `apps/server/internal/customers/contact_roles_test.go`, replace `TestAssociationRequests_TheCorpusShapeStillWorksAndTitleWins` (lines ~400-438) entirely — the name goes with it, because the behaviour it describes is gone:

```go
// TestAssociationRequests_TitleIsTheOnlyNameForTheFreeText is the corpus-shape
// test's successor (follow-ups design D5). `role` is no longer a property of
// any association schema, so a body that still sends it is a body with an
// unknown key: nothing in openapi/customers.yaml sets
// additionalProperties: false, so it is accepted and ignored, and the
// association ends up with no title at all — which, with no roles either, is
// the title-or-roles refusal. That is the whole of the break, and it is worth
// a test precisely because the frozen corpus still sends that body: the
// corpus proves the SCHEMAS still validate it (internal/openapi's
// TestRecordedExchangesMatchTheContract), and this proves what the HANDLER now
// does with it.
func TestAssociationRequests_TitleIsTheOnlyNameForTheFreeText(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Title Only Co")
	titled := createContact(t, c, map[string]any{"firstName": "Titled", "lastName": "Personsen"})
	roleOnly := createContact(t, c, map[string]any{"firstName": "Roleonly", "lastName": "Personsen"})

	got := attachWithRoles(t, c, customer.Id, map[string]any{"contactId": titled.Id, "title": "CEO"})
	if got.Title == nil || *got.Title != "CEO" {
		t.Errorf("title = %v, want \"CEO\"", got.Title)
	}
	if len(got.Roles) != 0 {
		t.Errorf("roles = %+v, want an empty array: this request gave no typed roles", got.Roles)
	}

	// The corpus's own body, unchanged: `role` is now an unknown key, so it
	// names nothing and the request says nothing about the person.
	r := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/customers/%d/contacts", customer.Id), map[string]any{
		"contactId": roleOnly.Id, "role": "CEO",
	})
	if r.Status != http.StatusBadRequest {
		t.Fatalf("status %d body %s, want 400: role is not a field any more", r.Status, r.Body)
	}
	var problem validationProblemJSON
	r.JSON(&problem)
	if msgs := problem.Errors["title"]; len(msgs) != 1 || msgs[0] != "A contact needs a title or at least one role" {
		t.Errorf("errors[title] = %v, want the title-or-roles refusal", msgs)
	}
	if _, ok := problem.Errors["role"]; ok {
		t.Errorf("errors = %v, want no key \"role\": the field does not exist", problem.Errors)
	}

	// And a role alone still needs no title — the other half of the rule.
	roles := attachWithRoles(t, c, customer.Id, map[string]any{
		"contactId": roleOnly.Id, "roles": []any{map[string]any{"role": "billing"}}})
	if roles.Title != nil {
		t.Errorf("title = %v, want null: the request gave none", roles.Title)
	}

	// Carried over from the test this replaces: the server ALWAYS answers
	// `roles` as an array, never null, on every item of the list — a promise
	// that is optional in the yaml only because the corpus predates the field,
	// and one the `role` removal must not have disturbed.
	list := listCustomerContacts(t, c, customer.Id)
	if len(list.Data) != 2 {
		t.Fatalf("len(data) = %d, want 2", len(list.Data))
	}
	for _, item := range list.Data {
		if item.Roles == nil {
			t.Errorf("contact %d: roles is null, want an empty array (the server always answers it)", item.Contact.Id)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customer_contact_roles WHERE customer_id = $1`, customer.Id); n != 1 {
		t.Errorf("role rows = %d, want 1: only the second attach gave a typed role", n)
	}
}
```

3. In the same file, drop `Role` from the three JSON structs (lines ~25-48) — this is what makes the compiler prove no assertion still reads it:

```go
type contactRoleJSON struct {
	Role    string `json:"role"`
	Primary bool   `json:"primary"`
}

type roledContactJSON struct {
	Contact contactJSON       `json:"contact"`
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
	Title    *string                      `json:"title"`
	Roles    []contactRoleJSON            `json:"roles"`
}

type roledCustomerListJSON struct {
	Data []roledCustomerJSON `json:"data"`
}
```

(`contactRoleJSON.Role` **stays**: that is a typed role's own code, not the removed alias.) Also fix the block comment above them (lines ~17-23), which describes a distinction that no longer exists:

```go
// customerContactJSON (contacts_test.go) is the shape the association
// endpoints answered before typed roles; these types are the shape they answer
// now. Both are hand-written rather than generated, so a field the server
// stops sending shows up as a nil pointer in a test rather than as a compile
// error — which is why the assertions below check values, never mere presence.
```

Fix `TestUpdateCustomerContact_…`'s expected payload at line ~717 by deleting the `"role": "",` line — the payload no longer carries the key.

- [ ] **Step 2: Run the Go tests and watch them fail**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- go test -count=1 -run 'TestAssociationRequests_TitleIsTheOnlyNameForTheFreeText|TestValidateContactTitle' ./internal/customers/
```
Expected: FAIL. The package will not even build until Step 1's struct edits are matched by the code — `go vet ./internal/customers/` reports `undefined: validateContactRole` only after Step 4, so at this point the failure is the new test's own: `status 200 … want 400: role is not a field any more` (today `role` is still an accepted alias). If the build fails instead because an assertion in this file still reads `.Role`, fix that assertion — that is the compiler doing the work this step wants it to do.

- [ ] **Step 3: Remove `role` from the four schemas, and relax assignable-users**

In `openapi/customers.yaml`:

`AttachCustomerContactRequest` (lines 3-30) loses its `role` property:

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

`CustomerContactRequest` (lines 351-373) the same, and its `roles` description keeps every word it had:

```yaml
        CustomerContactRequest:
            properties:
                email:
                    nullable: true
                    type: string
                phone:
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

`CustomerContactResponse` (lines 374-399) loses the property **and** its place in `required:`:

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
                roles:
                    description: Every typed role this contact holds for this customer, in the fixed order billing, project, decision_maker (typed contact roles design D3). Always present on responses from this version on — an empty array when the contact holds none — and optional here only because the recorded exchange corpus predates it.
                    items:
                        $ref: '#/components/schemas/CustomerContactRole'
                    type: array
                title:
                    description: What this person is called at this customer (typed contact roles design D1). Absent when the association has no title; the roles then say who the person is.
                    nullable: true
                    type: string
            required:
                - contact
            type: object
```

`GetContactCustomersContactCustomerResponse` (lines 721-746) gets exactly the same two edits:

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
                roles:
                    description: Every typed role this contact holds for this customer, in the fixed order billing, project, decision_maker (typed contact roles design D3). Always present on responses from this version on — an empty array when the contact holds none — and optional here only because the recorded exchange corpus predates it.
                    items:
                        $ref: '#/components/schemas/CustomerContactRole'
                    type: array
                title:
                    description: What this person is called at this customer (typed contact roles design D1). Absent when the association has no title; the roles then say who the person is.
                    nullable: true
                    type: string
            required:
                - customer
            type: object
```

And `getCustomersAssignableUsers` (line ~2769) is relaxed — one line:

```yaml
            x-vantigo-access: permission:customers:view
```

Nothing else about that operation moves. Its `summary` still says "Search users assignable as a customer's owner", and that stays true: a follow-up's assignee comes from the same directory search, and widening the summary would make it vaguer rather than truer. The reason for the relaxation goes in the Go doc comment (Step 4) and in `docs/customers.md` (Task 7).

- [ ] **Step 4: Generate, read the result, then follow it through Go**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- go generate ./... && mise exec -- go generate ./... && git status --short
cd /home/anders/projects/vantigo/vantigo && mise exec -- bun run gen:client && git status --short
```

Read `apps/server/internal/customers/gen/api.gen.go` and confirm: `AttachCustomerContactRequest` and `CustomerContactRequest` have no `Role` field at all (they had `Role *string`), and `CustomerContactResponse`/`GetContactCustomersContactCustomerResponse` have no `Role string`. Then the seven Go edits, which the compiler will name for you one at a time:

`values.go` — delete `validateContactRole` (lines 337-345) entirely, and amend `associationTitleRule`'s doc comment (lines 320-326), whose "under whichever field name the caller used" is no longer true:

```go
// associationTitleRule is the free text a customer–contact association
// carries (typed contact roles design D1): non-blank when given, at most 255
// UTF-16 code units, trimmed but case-preserved. It stays parameterised by the
// noun its message names even though `title` is now the only name the contract
// has (the deprecated `role` alias went with follow-ups design D5, while
// nothing was live): the noun costs a string, and it is the seam a second name
// would use if one ever arrives.
func associationTitleRule(noun, raw string) (string, string) {
```

`contact_roles.go` — `validateCustomerContactRequest` loses the parameter and the branch (lines 124-159):

```go
// validateCustomerContactRequest is CustomerContactRequest.TryApplyTo, widened
// by design D1 and D3. Every field is validated regardless of an earlier one's
// failure and every error is reported together, keyed by the JSON field name —
// the module's all-errors-at-once shape.
//
// title is the association's free text and the only name it has (follow-ups
// design D5 removed the deprecated `role` alias). A blank string is an error,
// not an absence: a client that sends "title": "" is saying something, and
// saying it wrongly.
//
// rolesWhenOmitted is how many roles the association already holds, and it
// exists only for the title-or-role rule: an attach passes 0 (a new
// association holds none), an update passes the count it just read, because
// `roles` omitted on an update means "leave them alone" and an association
// that keeps three roles is not saying nothing about the person just because
// this request did not mention them.
func validateCustomerContactRequest(title *string, roles *[]gen.CustomerContactRoleRequest, phone, email *string, rolesWhenOmitted int) (validatedAssociation, map[string][]string) {
	errs := map[string][]string{}

	var parsedTitle *string
	if title != nil {
		t, err := validateContactTitle(*title)
		if err != "" {
			errs["title"] = []string{err}
		} else {
			parsedTitle = &t
		}
	}
```

The rest of the function — the roles loop, the title-or-role rule, phone, email — is unchanged. The rule's own comment (lines 197-200) says "The title-or-role rule (design D1)"; leave it, it is still exactly that.

`contacts.go` — six edits. Two call sites lose an argument (lines ~613 and ~750):

```go
	assoc, errs := validateCustomerContactRequest(body.Title, body.Roles, body.Phone, body.Email, 0)
```
```go
	assoc, errs := validateCustomerContactRequest(body.Title, body.Roles, body.Phone, body.Email, len(currentRoles))
```

and four response projections drop `Role:` (lines ~496, ~546, ~680, ~797):

```go
			Title: r.Title, Roles: genContactRoles(byCustomer[r.ID]),
```
```go
			Title: r.Title, Roles: genContactRoles(byContact[r.ID]),
```
```go
				Contact: contactResponse(contact), Title: assoc.Title,
```
```go
		Contact: contactResponse(contactFromAssociationRow(existing)), Title: assoc.Title,
```
(Each is one field removed from a composite literal; keep the surrounding fields and line breaks exactly as they are. `deref` stays used elsewhere in the file, so no import or helper becomes dead.)

`contacts_timeline.go` — the payload and the version (lines 34-61):

```go
// recordContactEvent is AddContact (SV/CustomerTimelineRecorder.cs:99-124),
// widened by typed contact roles design D4 and narrowed by follow-ups design
// D5: the payload carries `title` and `roles`, and `role` — the same value
// under the name the field used to have — is gone from NEW payloads. Entries
// already written keep theirs, which is why this is a payload VERSION bump (1
// → 2) rather than a silent change of shape: a reader that has to interpret an
// old entry can tell which shape it is looking at.
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
		"title":       title,
		"roles":       roles,
		"phone":       phone,
		"email":       email,
	}
	return recordGeneratedEvent(ctx, q, customerID, now, eventType, truncateUTF16(summary, 500), payload, 2, actorKind, actorDisplay, actorUserID)
}
```

`owner.go` — `GetCustomersAssignableUsers`'s doc comment (lines 341-348) has to stop claiming a permission it no longer sits behind:

```go
// It sat behind customers:update until follow-ups design D1 moved it to
// customers:view. The reason is the new caller: a timeline writer picking a
// follow-up's assignee holds customers:timeline-manage and need not hold
// customers:update at all, and what this operation answers — the display names
// of active users — is what every timeline READER already sees on every entry
// as its author. There was nothing here for customers:update to protect.
```
(Insert it in place of the existing "It sits behind customers:update rather than customers:view because it is the writer's search: who a customer COULD be given to is only useful to whoever may give it." sentence; keep the paragraph about there being nothing to exclude.)

- [ ] **Step 5: Mechanically retire `role` from `contacts_test.go`**

About twenty-six sites, in file order — the exact count depends on how the four payload maps are counted, so treat the list below as the anchors and `grep -n 'role\|Role' contacts_test.go` as the authority: nothing named `role` may remain in this file except a typed role's own code inside a `roles` array. Each entry below is an exact replacement.

Lines 51-56 and 62-67, the two JSON structs — `Role string` becomes `Title *string`, because that is the field the server answers now:

```go
type customerContactJSON struct {
	Contact contactJSON `json:"contact"`
	Title   *string     `json:"title"`
	Phone   *string     `json:"phone"`
	Email   *string     `json:"email"`
}
```
```go
type contactCustomerJSON struct {
	Customer contactCustomerReferenceJSON `json:"customer"`
	Title    *string                      `json:"title"`
	Phone    *string                      `json:"phone"`
	Email    *string                      `json:"email"`
}
```

Line ~96-101, the shared fixture — rename its parameter too, so no caller reads as if it were sending a role:

```go
// attachContact attaches contactID to customerID with title, failing the test
// on anything but 200.
func attachContact(t *testing.T, c *modtest.Client, customerID, contactID int32, title string) {
	t.Helper()
	r := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/customers/%d/contacts", customerID), map[string]any{
		"contactId": contactID, "title": title,
	})
```

Then, in every request body, `"role"` becomes `"title"` — lines ~439, ~471, ~509, ~528, ~581, ~642, ~668, ~688, ~749, ~868, ~912, ~957:

| line | becomes |
| --- | --- |
| 439 | `"contactId": contact.Id, "title": "CEO", "email": "attach@attachco.no",` |
| 471 | `"contactId": contact.Id, "title": "CTO",` |
| 509 | `"contactId": tc.contactID, "title": "CEO",` |
| 528 | `"contactId": contact.Id, "title": "", "email": "not-an-email",` |
| 581 | `"contactId": 999998, "title": "", // also nonexistent, but validation must win` |
| 642 | `"title": "Chairman", "phone": "+47 55 66 77 88",` |
| 668 | `…/contacts/999999", customer.Id), map[string]any{"title": "CEO"})` |
| 688 | `"title": "", // also invalid, but existence must win` |
| 749 | `"contactId": contact.Id, "title": "CEO", "phone": "+47 99 00 11 22",` |
| 868 | `"contactId": contact.Id, "title": "CEO", "phone": "+47 11 22 33 44",` |
| 912 | `"title": "Chairman", "email": "chair@timeline.co",` |
| 957 | `"title": "CEO", // identical to the attach above: no phone, no email either time` |

Line ~535, the field-error key set, now names the field the caller sent:

```go
	want := []string{"email", "title"}
```

Lines ~449, ~610, ~649, ~764, the four response assertions — each reads the pointer through the existing `str` helper:

```go
	if str(association.Title) != "CEO" {
		t.Errorf("Title = %q, want CEO", str(association.Title))
	}
```
```go
	if str(list.Data[0].Title) != "CEO" {
		t.Errorf("Data[0].Title = %q, want CEO", str(list.Data[0].Title))
	}
```
```go
	if str(updated.Title) != "Chairman" {
		t.Errorf("Title = %q, want Chairman", str(updated.Title))
	}
```
```go
	if str(list.Data[0].Title) != "CEO" {
		t.Errorf("Data[0].Title = %q, want CEO", str(list.Data[0].Title))
	}
```

Lines ~885, ~930, ~990, ~1024, the four expected payloads — delete the `"role"` entry from each and bump **all four** `PayloadVersion` assertions to `2`. There are four, not two: `grep -n 'PayloadVersion !=' contacts_test.go` reports lines **879, 923, 988 and 1022** (attached, relationship-updated, detached and removed), and every one of them reads the same event recorder, so all four move together:

```go
	if event.PayloadVersion != 2 {
		t.Errorf("PayloadVersion = %d, want 2 (the role key left the payload, follow-ups design D5)", event.PayloadVersion)
	}
```

and, for example, line ~885's map becomes:

```go
		"displayName": "Timeline Middleman Attachsen",
		"firstName":   "Timeline",
		"middleName":  "Middleman",
		"lastName":    "Attachsen",
		"title":       "CEO",
		"roles":       []any{},
		"phone":       "+47 11 22 33 44",
		"email":       nil,
```

with lines ~990 and ~1024 becoming, respectively:

```go
		"title": "CTO", "roles": []any{}, "phone": nil, "email": nil,
```
```go
		"title": "Custodian", "roles": []any{}, "phone": nil, "email": nil,
```

Finally, in `owner_test.go`, the assignable-users permission-gate test signs in with `customers:update`; find it (`grep -n 'assignable-users' owner_test.go`) and change the caller that must be **refused** to hold neither `customers:view` nor `customers:update`, and the caller that must be **admitted** to hold `customers:view` alone:

```go
// The search answers display names of active users, which every customer
// READER already sees (follow-ups design D1 relaxed it from customers:update
// for exactly that reason), so customers:view is the door. A caller with some
// other module's permission and nothing of this one's is still refused — the
// router's own 403, not the handler's.
func TestGetAssignableUsers_NeedsOnlyCustomersView(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	reader, _ := h.SignInUser(t, "customers:view")
	if r := reader.Do(http.MethodGet, "/api/v1/customers/assignable-users", nil); r.Status != http.StatusOK {
		t.Errorf("customers:view: status %d body %s, want 200", r.Status, r.Body)
	}
	outsider, _ := h.SignInUser(t, "customers:timeline-view")
	if r := outsider.Do(http.MethodGet, "/api/v1/customers/assignable-users", nil); r.Status != http.StatusForbidden {
		t.Errorf("timeline-view only: status %d body %s, want 403", r.Status, r.Body)
	}
}
```
If `owner_test.go` already has a differently-named test for this gate, rewrite that one in place rather than adding a second.

- [ ] **Step 6: Run the Go suite and the corpus gate**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- gofmt -l internal/customers internal/openapi
mise exec -- go vet ./... && mise exec -- go test -count=1 ./internal/customers/... ./internal/openapi/... ./internal/module/...
```
Expected: PASS, and in particular `TestRecordedExchangesMatchTheContract` green — that is the constraint's own proof that the frozen corpus still validates against schemas with no `role` in them. `RequireCoverage` is also still satisfied: no operation was added or removed by this task.

If `TestRecordedExchangesMatchTheContract` fails, **do not touch the corpus.** Read the failure: it will name an operationId and a validation message. The only plausible cause is a schema that gained `additionalProperties: false` by accident, or a `required:` list that kept `role` in one of the two responses.

- [ ] **Step 7: The frontend — `titleOf` goes**

In `apps/customers/frontend/src/api/contacts.ts`:

Drop `role` from both response interfaces (lines ~79-84 and ~101-104):

```ts
export interface CustomerContactResponse {
  contact: ContactResponse;
  /** What this person is called at this customer, or null when the association has no title. */
  title: string | null;
  /** Every typed role held for this customer, in the fixed order. */
  roles: ContactRoleAssignment[];
  phone: string | null;
  email: string | null;
}
```
```ts
export interface ContactCustomerResponse {
  customer: ContactCustomerReference;
  title: string | null;
  roles: ContactRoleAssignment[];
  phone: string | null;
  email: string | null;
}
```
(Keep whatever other fields each interface already has around those; only `role` is removed and `title` loses nothing.)

The two `Raw…` aliases (lines ~111-118) now only have to widen `roles`, since `title` is the wire's own field:

```ts
/**
 * The wire shapes, which differ from the normalised ones in one way: `roles` is
 * optional in the contract (the recorded corpus predates it) even though the
 * server always sends it, so the boundary fills it in. `title` needs no such
 * treatment — it is nullable, and null is exactly what it means.
 */
type RawCustomerContactResponse = Omit<CustomerContactResponse, "roles"> & {
  roles?: RawContactRole[] | null;
};
type RawContactCustomerResponse = Omit<ContactCustomerResponse, "roles"> & {
  roles?: RawContactRole[] | null;
};
```

And `titleOf` (lines ~120-128) is deleted; the two normalisers read `title` directly. Both are currently **module-private** (`const normalizeCustomerContact = …` at line ~130, `const normalizeContactCustomer = …` at line ~136); this step **exports** them, because Step 8's test calls `normalizeCustomerContact` directly and the boundary rule it pins has no other seam — `normalizeContactRoles` beside them is already exported for exactly that reason:

```ts
export const normalizeCustomerContact = (raw: RawCustomerContactResponse): CustomerContactResponse => ({
  ...raw,
  title: raw.title ?? null,
  roles: normalizeContactRoles(raw.roles),
});

export const normalizeContactCustomer = (raw: RawContactCustomerResponse): ContactCustomerResponse => ({
  ...raw,
  title: raw.title ?? null,
  roles: normalizeContactRoles(raw.roles),
});
```
(Keep the exact spread each one already uses — `grep -n 'normalizeCustomerContact\|normalizeContactCustomer' api/contacts.ts` first. `raw.title ?? null` rather than a bare spread because the property is optional on the wire type and `undefined` is not `null`. Adding `export` breaks nothing: the four in-file call sites at lines ~185, ~210, ~223 and ~229 are unchanged.)

- [ ] **Step 8: The frontend tests**

In `apps/customers/frontend/src/api/contacts.test.ts` and `apps/customers/frontend/src/pages/-contacts.test.tsx`, every fixture that carries `role` is a wire body, so each one drops the key and keeps (or gains) `title`:

```bash
cd /home/anders/projects/vantigo/vantigo/apps/customers/frontend
grep -n 'role:' src/api/contacts.test.ts src/pages/-contacts.test.tsx
```
For each hit that is the association's free text (not a `roles: [{ role: "billing" }]` element), delete `role: "…"` and make sure the fixture has `title: "…"` with the same value. Where a test's *name* or comment says "the corpus shape" or "role falls back to title", rewrite it — the fallback is gone.

Add `normalizeCustomerContact` to `contacts.test.ts`'s import list — the list is sorted, and `normalizeContactRoles` sorts **before** `normalizeCustomerContact` (`…Contact…` vs `…Customer…`, 'o' before 'u'):

```ts
import {
  attachCustomerContact,
  createContact,
  customerContactsQueryOptions,
  deleteContact,
  detachCustomerContact,
  normalizeContactRoles,
  normalizeCustomerContact,
  updateCustomerContact,
} from "./contacts";
```
then add one test that pins the new boundary rule:

```ts
it("reads the title straight off the wire and turns an absent one into null", () => {
  // The `role` alias is gone (follow-ups design D5), so there is no fallback
  // left to get wrong: a wire body with no title means the association has
  // none, and an extra `role` key a stale client still sends is ignored.
  expect(
    normalizeCustomerContact({
      contact: { id: 1, firstName: "Kari", lastName: "Nordmann" },
      title: null,
      roles: [],
      phone: null,
      email: null,
    } as never),
  ).toMatchObject({ title: null, roles: [] });
  expect(
    normalizeCustomerContact({
      contact: { id: 1, firstName: "Kari", lastName: "Nordmann" },
      title: "CEO",
      role: "IGNORED",
      phone: null,
      email: null,
    } as never),
  ).toMatchObject({ title: "CEO", roles: [] });
});
```

```bash
cd /home/anders/projects/vantigo/vantigo
mise exec -- bun run --cwd apps/customers/frontend test
bunx biome check apps/customers/frontend/src
```
Expected: PASS.

- [ ] **Step 9: One sentence in the docs**

In `docs/customers.md`, replace the whole `### The title, and why `role` is still on the wire` section (lines 160-181) with:

```markdown
### The title

`customers.customers_contacts.title` is free text and nullable. It was called
`role` until migration `00025` renamed it, and it was answered under that name on
the wire for one delivery longer; the deprecated `role` alias was **removed** with
phase 4 delivery C, while nothing was live, so `title` is now the only name the
contract has. The frozen exchange corpus still sends `{"contactId": N, "role":
"CEO"}` and still validates, because no schema here sets
`additionalProperties: false` — an unknown key is ignored. A request that sends
only `role` therefore names no title at all, and with no roles either it is
refused by the rule below.

A request with **neither a title nor at least one role** is refused (400, field
`title`, `A contact needs a title or at least one role`): an association that
says nothing about the person is not worth having. On an update, "at least one
role" counts the roles the association keeps — `roles` omitted means "leave them
alone", so changing only a phone number on an association with three roles is
not suddenly a request that says nothing.
```

Then fix the two other places that mention the alias:
- the contact-events bullet in `## The timeline` (line ~665): replace "carry `title` and `roles` (`[{role, primary}]`, never null) beside the `role` key they have always had, which is the same value as `title` and stays because entries already written speak it." with "carry `title` and `roles` (`[{role, primary}]`, never null). Payload version **2** dropped the older `role` key, which held the same value as `title`; entries written before the bump keep it, which is what the version is for."
- the API table's note, if any, and the `GET /assignable-users` row — both are Task 7's, not this task's; leave them.

- [ ] **Step 10: Regenerate COVERAGE.md and commit**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- go run ./internal/openapi/cmd/contract coverage -corpus ../../openapi/testdata/exchanges -out ../../openapi/COVERAGE.md
cd /home/anders/projects/vantigo/vantigo && git status --short
```
`COVERAGE.md` lists operations no recorded exchange exercises; this task adds no operation, so it should not move. If it does, read the diff before committing it — it means an operationId changed, which nothing here intends.

```bash
cd /home/anders/projects/vantigo/vantigo
printf '%s\n\n%s\n\n%s\n' 'feat(customers)!: the association'"'"'s free text is a title and nothing else' 'The deprecated role alias is removed from all four association schemas while nothing is live: title is the field, the title-or-roles rule keeps its wording, and the contact events'"'"' payload version is 2 without it. The frozen corpus is untouched and still validates — no schema forbids an unknown key — so a body that still sends role now names no title and is refused by the rule that already existed. GET /customers/assignable-users drops to customers:view: it answers display names every timeline reader already sees, and a follow-up'"'"'s assignee has to be pickable without customers:update.' 'Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>' > /tmp/msg-followups-task1
git add openapi/customers.yaml apps/server/internal/openapi/specs/customers.yaml apps/server/internal/customers/values.go apps/server/internal/customers/contact_roles.go apps/server/internal/customers/contacts.go apps/server/internal/customers/contacts_timeline.go apps/server/internal/customers/owner.go apps/server/internal/customers/gen apps/server/internal/customers/values_test.go apps/server/internal/customers/contact_roles_test.go apps/server/internal/customers/contacts_test.go apps/server/internal/customers/owner_test.go apps/customers/frontend/src/api/contacts.ts apps/customers/frontend/src/api/contacts.test.ts apps/customers/frontend/src/pages/-contacts.test.tsx docs/customers.md
git add $(git status --short | grep 'api-schema.d.ts' | awk '{print $2}')
git commit -F /tmp/msg-followups-task1 -- $(git diff --cached --name-only)
git show --stat HEAD && git status --short
```

- [ ] **Step 11: Show the new tests can fail**

Two mutations, by hand, restored by hand (never `git stash`):
1. Put `role` back as a property of `AttachCustomerContactRequest` in `openapi/customers.yaml`, `go generate`, re-add the `case role != nil:` branch in `validateCustomerContactRequest`, and run `go test -run TestAssociationRequests_TitleIsTheOnlyNameForTheFreeText ./internal/customers/` — expect `status 200 … want 400`. Restore both, regenerate, re-run, green.
2. Change `2` back to `1` in `recordContactEvent`'s `recordGeneratedEvent` call and run `go test -run TestAttachContact_RecordsTimelineEvent ./internal/customers/` — expect `PayloadVersion = 1, want 2`. Restore, re-run, green.

Note both in the report.

---

### Task 2: The three columns, on both tables, and every query that reads or writes them (D1, D2, D3)

**Files:**
- Create: `apps/server/internal/db/migrations/00026_customers_timeline_follow_up.sql`, `apps/server/internal/customers/queries/follow_ups.sql`
- Modify: `apps/server/internal/customers/sqlc.yaml` (the `schema:` list), `apps/server/internal/db/schema_test.go` (`TestCustomersBaseline_AppliesAndIsIdempotent`), `apps/server/internal/customers/queries/timeline.sql`
- Generated: `apps/server/internal/customers/store/follow_ups.sql.go`, `store/timeline.sql.go`, `store/models.go`
- Read first (do not change): `apps/server/internal/db/migrations/00003_customers_baseline.sql:53-95` (both tables' column order), `00015_customers_timeline_actor.sql` (the last migration that added a column to both tables at once — this one's voice), `apps/server/internal/customers/queries/registry.sql:83-116` (`RegistryAttentionCandidates`, the pre-filter-in-SQL/precedence-in-Go division this task copies), `apps/server/internal/customers/queries/customers.sql:217-360` (`CountCustomers`/`ListCustomers`, the textually-identical-WHERE rule and the offset paging this task copies)

**Interfaces:**
- Produces (SQL, and so the generated Go later tasks call):
  - `customers.customers_timeline_entries` and `customers.customers_timeline_entries_revisions` each gain `follow_up_on date`, `follow_up_assignee_user_id uuid`, `follow_up_done_at timestamptz`, all NULL-able
  - indexes `ix_customers_timeline_entries_follow_up_open` on `(follow_up_on, id)` WHERE `follow_up_on IS NOT NULL AND follow_up_done_at IS NULL AND state = 'active'`, and `ix_customers_timeline_entries_follow_up_assignee` on `(follow_up_assignee_user_id, follow_up_on, id)` WHERE `follow_up_on IS NOT NULL`
  - `store.CustomersCustomersTimelineEntry` and `store.CustomersCustomersTimelineEntriesRevision` gain `FollowUpOn pgtype.Date`, `FollowUpAssigneeUserID *uuid.UUID`, `FollowUpDoneAt *time.Time`; so do `InsertManualTimelineEntryRow`, `UpdateManualTimelineEntryParams`/`Row` and `InsertTimelineRevisionParams`
  - `SetTimelineEntryFollowUpDone :one`, `ClearTimelineEntryFollowUpDone :one`, `FollowUpAttentionCandidates :many`, `CountCustomerFollowUps :one`, `ListCustomerFollowUps :many`
- Produces Go: nothing yet. This task ends with the schema and the queries in place, the package compiling, and every existing test green — no behaviour on the wire moves until Task 4.

- [ ] **Step 1: Write the failing schema test**

In `apps/server/internal/db/schema_test.go`, inside `TestCustomersBaseline_AppliesAndIsIdempotent`, bump the migration version the test applies (line ~312) so the new migration is exercised at all:

```go
	applyUpDownUp(t, url, 3) // 00003_customers_baseline.sql
```
stays exactly as it is. `applyUpDownUp(t, url, N)` (line 274) runs **every** migration up, rolls back to `N-1`, and runs up again — `N` names the migration whose `Down` is exercised, not a ceiling — so `00026` is applied by this test as it stands and the assertions below can see its columns. This line must not change.

Then, immediately after the `customers_contacts.title` assertion (the block ending `want \"YES\" and 0`), add:

```go
	// A follow-up is part of the entry, and part of every revision of it
	// (follow-ups design D1) — so history stays point-in-time: a revision that
	// did not carry the follow-up would answer "what did this entry look like
	// then" with today's due date. The three columns are asserted on BOTH
	// tables in one query, because the failure mode this guards is adding them
	// to one and forgetting the other, which nothing else here would notice.
	var followUpColumns []string
	followUpRows, err := pool.Query(ctx, `SELECT table_name || '.' || column_name || ':' || data_type || ':' || is_nullable
	                                      FROM information_schema.columns
	                                      WHERE table_schema = 'customers'
	                                        AND table_name IN ('customers_timeline_entries', 'customers_timeline_entries_revisions')
	                                        AND column_name LIKE 'follow_up%'
	                                      ORDER BY table_name, column_name`)
	if err != nil {
		t.Fatalf("query follow-up columns: %v", err)
	}
	for followUpRows.Next() {
		var s string
		if err := followUpRows.Scan(&s); err != nil {
			t.Fatalf("scan follow-up column: %v", err)
		}
		followUpColumns = append(followUpColumns, s)
	}
	followUpRows.Close()
	if err := followUpRows.Err(); err != nil {
		t.Fatalf("iterate follow-up columns: %v", err)
	}
	wantFollowUpColumns := []string{
		"customers_timeline_entries.follow_up_assignee_user_id:uuid:YES",
		"customers_timeline_entries.follow_up_done_at:timestamp with time zone:YES",
		"customers_timeline_entries.follow_up_on:date:YES",
		"customers_timeline_entries_revisions.follow_up_assignee_user_id:uuid:YES",
		"customers_timeline_entries_revisions.follow_up_done_at:timestamp with time zone:YES",
		"customers_timeline_entries_revisions.follow_up_on:date:YES",
	}
	if !equalStrings(followUpColumns, wantFollowUpColumns) {
		t.Errorf("follow-up columns = %v, want %v", followUpColumns, wantFollowUpColumns)
	}

	// There is deliberately NO foreign key from
	// follow_up_assignee_user_id to identity.users, for migration 00024's own
	// two reasons: this module may not read identity's schema (the test below
	// and depguard both bar it), and design D1 rules that an assignee disabled
	// or removed afterwards KEEPS the follow-up. ON DELETE SET NULL would have
	// silently reassigned it to nobody.
	var assigneeForeignKeys int
	if err := pool.QueryRow(ctx, `SELECT count(*)
	                              FROM information_schema.key_column_usage k
	                              JOIN information_schema.table_constraints c
	                                ON c.constraint_name = k.constraint_name AND c.constraint_schema = k.constraint_schema
	                              WHERE k.table_schema = 'customers'
	                                AND k.column_name = 'follow_up_assignee_user_id'
	                                AND c.constraint_type = 'FOREIGN KEY'`).Scan(&assigneeForeignKeys); err != nil {
		t.Fatalf("count follow-up assignee foreign keys: %v", err)
	}
	if assigneeForeignKeys != 0 {
		t.Errorf("found %d foreign key(s) on follow_up_assignee_user_id, want 0", assigneeForeignKeys)
	}

	// Both follow-up indexes are PARTIAL, and the predicate is the load-bearing
	// half: an unpartitioned index on (follow_up_on, id) would cover every
	// timeline entry ever written, which is overwhelmingly rows with no
	// follow-up at all, to serve a list that only ever wants the few that have
	// one. indexColumns cannot see a predicate, so this reads the definitions.
	for name, want := range map[string][]string{
		"ix_customers_timeline_entries_follow_up_open":     {"UNIQUE" /* sentinel replaced below */},
		"ix_customers_timeline_entries_follow_up_assignee": {"UNIQUE"},
	} {
		_ = want
		var def string
		if err := pool.QueryRow(ctx, `SELECT indexdef FROM pg_indexes WHERE schemaname = 'customers' AND indexname = $1`, name).Scan(&def); err != nil {
			t.Fatalf("read %s definition: %v", name, err)
		}
		if !strings.Contains(def, "WHERE") || strings.Contains(def, "UNIQUE") {
			t.Errorf("%s = %q, want a non-unique PARTIAL index", name, def)
		}
	}
	var openIndexDef string
	if err := pool.QueryRow(ctx, `SELECT indexdef FROM pg_indexes
	                              WHERE schemaname = 'customers' AND indexname = 'ix_customers_timeline_entries_follow_up_open'`).Scan(&openIndexDef); err != nil {
		t.Fatalf("read ix_customers_timeline_entries_follow_up_open definition: %v", err)
	}
	for _, fragment := range []string{"follow_up_on IS NOT NULL", "follow_up_done_at IS NULL", "state)::text = 'active'"} {
		if !strings.Contains(openIndexDef, fragment) {
			t.Errorf("ix_customers_timeline_entries_follow_up_open = %q, want it to contain %q", openIndexDef, fragment)
		}
	}
```

The `for name, want := range` loop with its `_ = want` sentinel is ugly; simplify it to a slice before committing:

```go
	for _, name := range []string{
		"ix_customers_timeline_entries_follow_up_open",
		"ix_customers_timeline_entries_follow_up_assignee",
	} {
		var def string
		if err := pool.QueryRow(ctx, `SELECT indexdef FROM pg_indexes WHERE schemaname = 'customers' AND indexname = $1`, name).Scan(&def); err != nil {
			t.Fatalf("read %s definition: %v", name, err)
		}
		if !strings.Contains(def, "WHERE") || strings.Contains(def, "UNIQUE") {
			t.Errorf("%s = %q, want a non-unique PARTIAL index", name, def)
		}
	}
```
Use only this second form. (`state)::text = 'active'` is how Postgres renders a `varchar` comparison in `pg_indexes.indexdef` — the same normalisation the tags index's `lower((name)` assertion already allows for. If the actual text differs, take what `psql`/the failure prints, not what this plan guesses.)

- [ ] **Step 2: Run it and watch it fail**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- go test -count=1 -run TestCustomersBaseline_AppliesAndIsIdempotent ./internal/db/
```
Expected: FAIL — `follow-up columns = [], want [customers_timeline_entries.follow_up_assignee_user_id:uuid:YES …]`, and `read ix_customers_timeline_entries_follow_up_open definition: no rows in result set`.

- [ ] **Step 3: Write the migration**

Create `apps/server/internal/db/migrations/00026_customers_timeline_follow_up.sql`:

```sql
-- +goose Up
-- What happens next (follow-ups design D1) — phase 4's third delivery. A
-- manual timeline entry can carry a follow-up: a due date, optionally an
-- assignee, and a "done" stamp.
--
-- Three columns ON THE ENTRY, not a table of their own. A follow-up is never
-- shared, never plural and never outlives its entry: "call back on Friday"
-- belongs to the note that says why. On the entry it also shares the entry's
-- own revision, which is what lets the existing PUT set, replace and clear it
-- under the expectedRevision the caller already sends — a side table would
-- have needed a second concurrency story for one date.
--
-- follow_up_on is a date, not a timestamptz: a follow-up is due on a day, and
-- the design's overdue/due-today split is a UTC calendar comparison, not an
-- instant one. NULL means the entry carries no follow-up at all, which is the
-- overwhelming majority of entries and the reason both indexes below are
-- partial.
--
-- follow_up_assignee_user_id has deliberately NO foreign key to
-- identity.users, for migration 00024's own two reasons: this module may not
-- read identity's schema at all (internal/db/schema_test.go bars it, depguard
-- bars the import) — contracts.UserDirectory is the only sanctioned seam — and
-- design D1 rules that an assignee disabled or removed afterwards KEEPS the
-- follow-up, shown inactive. ON DELETE SET NULL would have quietly turned
-- somebody's task into nobody's. NULL means unassigned, which design D2 reads
-- as everyone's until somebody takes it.
--
-- follow_up_done_at is the stamp, not a boolean: "when" is strictly more than
-- "whether", the Follow-ups page shows it, and every other state change in
-- this module that can be undone is spelled the same way (deleted_at).
-- Clearing a follow-up clears this too — there is no done-ness without a
-- follow-up to be done.
ALTER TABLE customers.customers_timeline_entries
    ADD COLUMN follow_up_on              date,
    ADD COLUMN follow_up_assignee_user_id uuid,
    ADD COLUMN follow_up_done_at          timestamptz;

-- The revisions table mirrors the entry column-for-column (00003's own
-- comment), and that mirroring is not cosmetic here: a revision is a
-- point-in-time snapshot, so a revision that did not carry the follow-up would
-- answer "what did this entry look like then" with today's due date and
-- today's assignee. The design says history stays point-in-time; these three
-- columns are what makes that true.
ALTER TABLE customers.customers_timeline_entries_revisions
    ADD COLUMN follow_up_on              date,
    ADD COLUMN follow_up_assignee_user_id uuid,
    ADD COLUMN follow_up_done_at          timestamptz;

-- The open-follow-ups index: what /stats/attention asks (every open follow-up
-- due today or earlier) and what the Follow-ups page's default filters ask
-- (state=open or overdue, ordered by due date). Partial on all three
-- predicates, because that is exactly the set both readers want and it is a
-- tiny fraction of the table — an index over every timeline entry ever written
-- would be bytes spent on rows neither reader can ever return. id is the
-- second key column so the design's "dueOn ascending then entry id" ordering
-- is the index's own order and needs no sort.
--
-- state is in the predicate rather than the key for the same reason: a
-- soft-deleted entry's follow-up is not a follow-up any more, so those rows
-- should not be in the index at all. A DELETE of an entry therefore removes it
-- from every follow-up reader without touching a follow-up column, which is
-- why SetTimelineEntryDeleted needs no change.
CREATE INDEX ix_customers_timeline_entries_follow_up_open
    ON customers.customers_timeline_entries (follow_up_on, id)
    WHERE follow_up_on IS NOT NULL AND follow_up_done_at IS NULL AND state = 'active';

-- The assignee index: the Follow-ups page's assignee filter, which the index
-- above cannot serve because it does not carry the column, and which has to
-- work for state=done and state=all too — hence a predicate of only
-- "has a follow-up at all". Unassigned follow-ups are found by IS NULL, and a
-- b-tree index does store NULLs, so this one serves assignee=none as well as
-- assignee=<uuid>.
CREATE INDEX ix_customers_timeline_entries_follow_up_assignee
    ON customers.customers_timeline_entries (follow_up_assignee_user_id, follow_up_on, id)
    WHERE follow_up_on IS NOT NULL;

-- +goose Down
DROP INDEX customers.ix_customers_timeline_entries_follow_up_assignee;
DROP INDEX customers.ix_customers_timeline_entries_follow_up_open;
ALTER TABLE customers.customers_timeline_entries_revisions
    DROP COLUMN follow_up_done_at,
    DROP COLUMN follow_up_assignee_user_id,
    DROP COLUMN follow_up_on;
ALTER TABLE customers.customers_timeline_entries
    DROP COLUMN follow_up_done_at,
    DROP COLUMN follow_up_assignee_user_id,
    DROP COLUMN follow_up_on;
```

- [ ] **Step 4: Register it with sqlc**

In `apps/server/internal/customers/sqlc.yaml`, append to the `schema:` list, after `00025_customers_contact_roles.sql`:

```yaml
      - ../db/migrations/00026_customers_timeline_follow_up.sql
```

- [ ] **Step 5: Run the schema tests and watch them pass**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- go test -count=1 -run 'TestCustomersBaseline_AppliesAndIsIdempotent|TestSqlcSchemaListsOnlyTheModulesOwnMigrations|TestNoModuleReferencesAnotherModulesSchema' ./internal/db/
```
Expected: PASS. `applyUpDownUp` runs every migration up, down and up again, so the `Down` section above is exercised here too.

- [ ] **Step 6: Thread the three columns through `queries/timeline.sql`**

Eight edits, in file order. The three columns go **last** in every select list and every insert column list, after `actor_user_id`, matching how `00015` appended `actor_user_id` itself: a new column at the end of the list is a new field at the end of the generated struct, and reviewing the diff is then reading three added names rather than a re-ordered wall.

1. `GetTimelineEntry`'s select list (lines 6-8) → append to the third line:
```sql
       created_at, updated_at, deleted_at, actor_user_id, follow_up_on, follow_up_assignee_user_id, follow_up_done_at
```
2. `GetActiveTimelineEntry`'s select list (lines 17-19) → the identical change.
3. `ListTimelineEntries`'s select list (lines 34-36) → the identical change.
4. `InsertManualTimelineEntry` (lines 78-105) — the insert's column list, its values, its `RETURNING`, the revision's own column list and the `SELECT … FROM entry` that feeds it, and the final select. Replace the whole statement body:
```sql
WITH entry AS (
    INSERT INTO customers.customers_timeline_entries (
        customer_id, provenance, producer, event_type, occurred_on, occurred_at,
        summary, note, source_url, payload_version, current_revision, state, actor_kind, actor_display,
        actor_user_id, created_at, updated_at, follow_up_on, follow_up_assignee_user_id
    ) VALUES (
        @customer_id::int, 'manual', 'customers.api', @event_type::text, @occurred_on::date, @occurred_at,
        @summary::text, @note::text, @source_url, 1, 1, 'active', @actor_kind::text, @actor_display::text,
        @actor_user_id, @now::timestamptz, @now::timestamptz, @follow_up_on, @follow_up_assignee_user_id
    )
    RETURNING id, customer_id, provenance, producer, event_type, occurred_on, occurred_at, summary, note,
              source_url, payload_json, payload_version, current_revision, state, actor_kind, actor_display,
              created_at, updated_at, deleted_at, actor_user_id, follow_up_on, follow_up_assignee_user_id,
              follow_up_done_at
), inserted_revision AS (
    INSERT INTO customers.customers_timeline_entries_revisions (
        customer_timeline_entry_id, revision_number, customer_id, provenance, producer, event_type,
        occurred_on, occurred_at, summary, note, source_url, payload_json, payload_version, current_revision,
        state, actor_kind, actor_display, actor_user_id, created_at, updated_at,
        follow_up_on, follow_up_assignee_user_id, follow_up_done_at
    )
    SELECT id, 1, customer_id, provenance, producer, event_type, occurred_on, occurred_at, summary, note,
           source_url, payload_json, payload_version, current_revision, state, actor_kind, actor_display,
           actor_user_id, created_at, updated_at, follow_up_on, follow_up_assignee_user_id, follow_up_done_at
    FROM entry
)
SELECT id, customer_id, provenance, producer, event_type, occurred_on, occurred_at, summary, note,
       source_url, payload_json, payload_version, current_revision, state, actor_kind, actor_display,
       created_at, updated_at, deleted_at, actor_user_id, follow_up_on, follow_up_assignee_user_id,
       follow_up_done_at
FROM entry;
```
and add to its doc comment, after the `actor_user_id` paragraph:
```sql
-- follow_up_on/follow_up_assignee_user_id are the entry's own follow-up
-- (follow-ups design D1), NULL when the request carried none. follow_up_done_at
-- is deliberately NOT a parameter: a follow-up cannot be created already done,
-- and the two paths that set it are their own statements in follow_ups.sql.
```
5. `UpdateManualTimelineEntry` (lines 121-133) — the three columns join the SET list, because the PUT is a full replace: omitted means cleared, exactly as it already means for `occurred_at` and `source_url`.
```sql
UPDATE customers.customers_timeline_entries
SET event_type = @event_type::text,
    occurred_on = @occurred_on::date,
    occurred_at = @occurred_at,
    note = @note::text,
    summary = @summary::text,
    source_url = @source_url,
    follow_up_on = @follow_up_on,
    follow_up_assignee_user_id = @follow_up_assignee_user_id,
    follow_up_done_at = CASE WHEN @follow_up_on::date IS NULL THEN NULL ELSE follow_up_done_at END,
    current_revision = @new_revision::int,
    updated_at = @now::timestamptz
WHERE id = @id AND customer_id = @customer_id AND current_revision = @expected_revision::int
RETURNING id, customer_id, provenance, producer, event_type, occurred_on, occurred_at, summary, note,
          source_url, payload_json, payload_version, current_revision, state, actor_kind, actor_display,
          created_at, updated_at, deleted_at, actor_user_id, follow_up_on, follow_up_assignee_user_id,
          follow_up_done_at;
```
and add to its doc comment:
```sql
-- The follow-up is part of the entry, so a PUT replaces it the way it replaces
-- occurred_at and source_url: absent means cleared (follow-ups design D1).
-- follow_up_done_at is the one column with a CASE rather than a plain
-- assignment, and it is the design's own sentence — "clearing a follow-up also
-- clears its done state" — expressed where it cannot be forgotten: a PUT that
-- KEEPS the follow-up leaves the done stamp exactly as it was (editing the
-- note of a ticked follow-up must not un-tick it), and a PUT that clears the
-- follow-up takes the stamp with it, because done-ness without a follow-up is
-- not a state this module has.
```
6. `SetTimelineEntryDeleted` (lines 139-144) — only the `RETURNING` list grows; the SET list does **not** touch a follow-up column, and that is deliberate: every follow-up reader filters `state = 'active'`, so a soft delete removes the follow-up from the list and from attention without erasing what the entry said, and a future restore would bring it back intact.
```sql
RETURNING id, customer_id, provenance, producer, event_type, occurred_on, occurred_at, summary, note,
          source_url, payload_json, payload_version, current_revision, state, actor_kind, actor_display,
          created_at, updated_at, deleted_at, actor_user_id, follow_up_on, follow_up_assignee_user_id,
          follow_up_done_at;
```
7. `InsertTimelineRevision` (lines 164-173) — the snapshot carries them:
```sql
INSERT INTO customers.customers_timeline_entries_revisions (
    customer_timeline_entry_id, revision_number, customer_id, provenance, producer, event_type,
    occurred_on, occurred_at, summary, note, source_url, payload_json, payload_version, current_revision,
    state, actor_kind, actor_display, actor_user_id, created_at, updated_at, deleted_at,
    follow_up_on, follow_up_assignee_user_id, follow_up_done_at
) VALUES (
    @entry_id::int, @revision_number::int, @customer_id::int, @provenance::text, @producer::text, @event_type::text,
    @occurred_on::date, @occurred_at, @summary::text, @note, @source_url, @payload_json, @payload_version::int,
    @current_revision::int, @state::text, @actor_kind::text, @actor_display::text, @actor_user_id, @created_at::timestamptz,
    @updated_at::timestamptz, @deleted_at, @follow_up_on, @follow_up_assignee_user_id, @follow_up_done_at
);
```
8. `ListTimelineRevisions`'s select list (lines 179-181) → append to the third line:
```sql
       state, actor_kind, actor_display, created_at, updated_at, deleted_at, actor_user_id,
       follow_up_on, follow_up_assignee_user_id, follow_up_done_at
```

`InsertGeneratedTimelineEvent` (`queries/customers.sql:368`) is **not** touched: a generated event never carries a follow-up (design D1), and leaving it alone is what makes that true in the one place it cannot be forgotten.

- [ ] **Step 7: Write the five follow-up queries**

Create `apps/server/internal/customers/queries/follow_ups.sql`:

```sql
-- name: SetTimelineEntryFollowUpDone :one
-- SetTimelineEntryFollowUpDone is POST .../follow-up/done's guarded write
-- (follow-ups design D1). Two things about it are the design, not style.
--
-- There is NO expected_revision. A tick comes from a list — the Follow-ups
-- page, or an entry line in a feed loaded minutes ago — and must not lose a
-- race with somebody editing the note; the design says so outright. What takes
-- its place is `follow_up_done_at IS NULL` in the WHERE: two concurrent ticks
-- serialize on the row, and the second matches no row. The handler reads that
-- as "already done" and answers 200 with the entry, which is what idempotent
-- means here.
--
-- current_revision is nonetheless bumped, with `current_revision + 1` read
-- from the row rather than supplied: ticking a follow-up IS a change to the
-- entry, so a subsequent PUT holding the pre-tick revision must conflict. The
-- caller inserts the matching revision row from the returned row's own
-- current_revision, so the unique index on (entry id, revision number) stays
-- the backstop it already was.
--
-- provenance/state are in the WHERE as well as in the handler's own 409 check:
-- the handler answers the distinct "Timeline entry is immutable" problem, and
-- this repeats the condition so a mistake at the call site matches zero rows
-- instead of ticking a follow-up on a generated or deleted entry.
UPDATE customers.customers_timeline_entries
SET follow_up_done_at = @now::timestamptz,
    current_revision = current_revision + 1,
    updated_at = @now::timestamptz
WHERE id = @id AND customer_id = @customer_id
  AND provenance = 'manual' AND state = 'active'
  AND follow_up_on IS NOT NULL AND follow_up_done_at IS NULL
RETURNING id, customer_id, provenance, producer, event_type, occurred_on, occurred_at, summary, note,
          source_url, payload_json, payload_version, current_revision, state, actor_kind, actor_display,
          created_at, updated_at, deleted_at, actor_user_id, follow_up_on, follow_up_assignee_user_id,
          follow_up_done_at;

-- name: ClearTimelineEntryFollowUpDone :one
-- ClearTimelineEntryFollowUpDone is DELETE .../follow-up/done: the reopen, and
-- the exact mirror of the statement above, down to why it has no
-- expected_revision and why it still bumps current_revision. Its own guard is
-- `follow_up_done_at IS NOT NULL`, so reopening an already-open follow-up
-- matches no row and the handler answers 200 with the entry unchanged.
UPDATE customers.customers_timeline_entries
SET follow_up_done_at = NULL,
    current_revision = current_revision + 1,
    updated_at = @now::timestamptz
WHERE id = @id AND customer_id = @customer_id
  AND provenance = 'manual' AND state = 'active'
  AND follow_up_on IS NOT NULL AND follow_up_done_at IS NOT NULL
RETURNING id, customer_id, provenance, producer, event_type, occurred_on, occurred_at, summary, note,
          source_url, payload_json, payload_version, current_revision, state, actor_kind, actor_display,
          created_at, updated_at, deleted_at, actor_user_id, follow_up_on, follow_up_assignee_user_id,
          follow_up_done_at;

-- name: FollowUpAttentionCandidates :many
-- FollowUpAttentionCandidates is /stats/attention's follow-up half (follow-ups
-- design D2): every open follow-up on a non-archived customer, due today or
-- earlier, that is the caller's or nobody's. The overdue-vs-due-today split is
-- NOT made here — it is one comparison against the same `today` the caller
-- already holds, and keeping it in Go keeps this query's answer a set of facts
-- rather than a set of verdicts, the division RegistryAttentionCandidates
-- states for the same endpoint.
--
-- caller_id is nullable, and the NULL case is honest rather than defensive:
-- with no signed-in user the equality is NULL, so only unassigned follow-ups
-- come back. The router admits no unauthenticated caller here, so that case is
-- unreachable through HTTP — but "everyone's follow-ups" is the one answer a
-- caller-dependent list must never give by accident.
--
-- The ordering is the design's (due date, then entry id) so repeated calls
-- cannot reshuffle ties, even though the handler sorts the merged list itself.
SELECT e.id AS entry_id, e.customer_id, c.name AS customer_name, e.follow_up_on
FROM customers.customers_timeline_entries e
JOIN customers.customers c ON c.id = e.customer_id
WHERE e.provenance = 'manual' AND e.state = 'active'
  AND e.follow_up_on IS NOT NULL AND e.follow_up_done_at IS NULL
  AND e.follow_up_on <= @today::date
  AND c.status <> 'archived'
  AND (e.follow_up_assignee_user_id IS NULL OR e.follow_up_assignee_user_id = sqlc.narg(caller_id)::uuid)
ORDER BY e.follow_up_on, e.id;

-- name: CountCustomerFollowUps :one
-- CountCustomerFollowUps is the total GET /customers/follow-ups paginates over
-- (follow-ups design D3), the same filters ListCustomerFollowUps applies below.
-- sqlc has no query fragments, so this WHERE clause and that one must be kept
-- textually identical BY HAND — a drift between them would make
-- pagination.totalCount disagree with what the page actually shows, which is
-- the reason CountCustomers says the same thing about itself.
--
-- follow_up_state is the design's four values. 'overdue' is a subset of 'open',
-- which is why it repeats the done test rather than standing alone; 'all' means
-- every follow-up whatever its state. The archived rule rides on the same
-- parameter: archived customers' follow-ups are excluded UNLESS the caller
-- asked for done ones, because a done follow-up is a record of work finished
-- and an archived customer's finished work is still finished.
SELECT count(*)
FROM customers.customers_timeline_entries e
JOIN customers.customers c ON c.id = e.customer_id
WHERE e.provenance = 'manual' AND e.state = 'active'
  AND e.follow_up_on IS NOT NULL
  AND (@follow_up_state::text = 'all'
       OR (@follow_up_state::text = 'done' AND e.follow_up_done_at IS NOT NULL)
       OR (@follow_up_state::text = 'open' AND e.follow_up_done_at IS NULL)
       OR (@follow_up_state::text = 'overdue' AND e.follow_up_done_at IS NULL AND e.follow_up_on < @today::date))
  AND (@follow_up_state::text = 'done' OR c.status <> 'archived')
  AND (NOT @assignee_none::bool OR e.follow_up_assignee_user_id IS NULL)
  AND (sqlc.narg(assignee_id)::uuid IS NULL OR e.follow_up_assignee_user_id = sqlc.narg(assignee_id)::uuid)
  AND (sqlc.narg(customer_id)::int IS NULL OR e.customer_id = sqlc.narg(customer_id)::int);

-- name: ListCustomerFollowUps :many
-- ListCustomerFollowUps is GET /customers/follow-ups' page (design D3). The
-- WHERE clause is CountCustomerFollowUps's, kept textually identical (see its
-- comment).
--
-- OFFSET paging, not a keyset cursor, and that is the module's own precedent
-- rather than a shortcut: GET /customers and GET /customers/contacts both
-- answer `page`/`pageSize` with a PaginationMetadata block, the frontend's
-- Pagination control needs a total page count to render at all, and the design
-- asks for `page`/`pageSize` by name. The timeline's own feed is the keyset
-- one, because an infinite scroll has no page numbers to show.
--
-- The note is cut to 200 UTF-16 units by the CALLER (truncateUTF16), not here:
-- Postgres' left() counts characters, Go's count is UTF-16 code units, and the
-- two disagree on every astral character — the same reason the manual entry's
-- own 500-character summary is truncated in Go.
SELECT e.id AS entry_id, e.customer_id, c.name AS customer_name,
       e.event_type, e.occurred_on, e.note,
       e.follow_up_on, e.follow_up_assignee_user_id, e.follow_up_done_at
FROM customers.customers_timeline_entries e
JOIN customers.customers c ON c.id = e.customer_id
WHERE e.provenance = 'manual' AND e.state = 'active'
  AND e.follow_up_on IS NOT NULL
  AND (@follow_up_state::text = 'all'
       OR (@follow_up_state::text = 'done' AND e.follow_up_done_at IS NOT NULL)
       OR (@follow_up_state::text = 'open' AND e.follow_up_done_at IS NULL)
       OR (@follow_up_state::text = 'overdue' AND e.follow_up_done_at IS NULL AND e.follow_up_on < @today::date))
  AND (@follow_up_state::text = 'done' OR c.status <> 'archived')
  AND (NOT @assignee_none::bool OR e.follow_up_assignee_user_id IS NULL)
  AND (sqlc.narg(assignee_id)::uuid IS NULL OR e.follow_up_assignee_user_id = sqlc.narg(assignee_id)::uuid)
  AND (sqlc.narg(customer_id)::int IS NULL OR e.customer_id = sqlc.narg(customer_id)::int)
ORDER BY e.follow_up_on, e.id
LIMIT @page_size::int OFFSET @row_offset::int;
```

- [ ] **Step 8: Generate, then read what was generated**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- go generate ./... && mise exec -- go generate ./... && git status --short
grep -n 'FollowUp' internal/customers/store/models.go
grep -n 'type \(SetTimelineEntryFollowUpDone\|ClearTimelineEntryFollowUpDone\|FollowUpAttentionCandidates\|CountCustomerFollowUps\|ListCustomerFollowUps\)' -A 14 internal/customers/store/follow_ups.sql.go
```

Write down for yourself, and follow the generated file wherever it disagrees with this plan:
- the exact Go types of the three columns (expected `FollowUpOn pgtype.Date`, `FollowUpAssigneeUserID *uuid.UUID`, `FollowUpDoneAt *time.Time` — the `sqlc.yaml` overrides map nullable `uuid` to `*uuid.UUID` and nullable `timestamptz` to `*time.Time`; `date` has no override, so it is `pgtype.Date` either way, and `.Valid` is how "no follow-up" is read);
- which of the five queries took a `…Params` struct (all five: each has two or more parameters) and the spelling of every field (`FollowUpState`, `AssigneeNone`, `AssigneeID`, `CustomerID`, `Today`, `PageSize`, `RowOffset`, `CallerID`, `EntryID`);
- whether `CountCustomerFollowUps` returns `int64` (it will);
- whether `ListCustomerFollowUpsRow.Note` is `*string` (it will be — the column is nullable).

If `go generate` errors on `sqlc.narg(caller_id)::uuid` inside an `OR`, the cause is sqlc's inability to infer the type of a bare narg in that position; the `::uuid` cast is already there for exactly that reason, so read the error before changing the SQL, and prefer adding a cast over changing the predicate's shape.

- [ ] **Step 9: Prove the whole package is still green**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- gofmt -l internal/customers internal/db
mise exec -- go vet ./... && mise exec -- go test -count=1 ./internal/customers/... ./internal/db/... ./internal/openapi/...
```
Expected: PASS. Nothing on the wire has moved, so every timeline test still passes unchanged — the new struct fields are simply zero. If `timeline.go` fails to compile, the cause is a `store.CustomersCustomersTimelineEntry(r)` conversion in `fromInsertManualRow`: the two struct types must still have identical fields in identical order, so `InsertManualTimelineEntryRow` and the model must have gained the three columns in the same place. If they have not, fix the `RETURNING`/`SELECT` order in `InsertManualTimelineEntry` rather than hand-writing a field-by-field conversion.

- [ ] **Step 10: Commit**

```bash
cd /home/anders/projects/vantigo/vantigo
printf '%s\n\n%s\n\n%s\n' 'feat(customers): a timeline entry has somewhere to keep a follow-up' 'Migration 00026 adds follow_up_on, follow_up_assignee_user_id and follow_up_done_at to the timeline entries table AND to its revisions table, so history stays point-in-time, with two partial indexes for the open list and the assignee filter and deliberately no foreign key to identity.users. Every timeline statement carries the three columns; a PUT replaces the follow-up and clearing it clears the done stamp. Nothing on the wire moves yet.' 'Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>' > /tmp/msg-followups-task2
git add apps/server/internal/db/migrations/00026_customers_timeline_follow_up.sql apps/server/internal/db/schema_test.go apps/server/internal/customers/sqlc.yaml apps/server/internal/customers/queries/follow_ups.sql apps/server/internal/customers/queries/timeline.sql apps/server/internal/customers/store
git commit -F /tmp/msg-followups-task2 -- $(git diff --cached --name-only)
git show --stat HEAD && git status --short
```

- [ ] **Step 11: Show the new test can fail**

Comment out the second `ALTER TABLE customers.customers_timeline_entries_revisions` block in the migration, re-run Step 5's command, and expect `follow-up columns = [customers_timeline_entries.follow_up_assignee_user_id:uuid:YES …], want [… customers_timeline_entries_revisions.follow_up_assignee_user_id:uuid:YES …]` — the exact "added to one table and not the other" mistake this assertion exists for. Restore it by hand, re-run, green. Then comment out the `WHERE` clause of `ix_customers_timeline_entries_follow_up_open` and expect `want a non-unique PARTIAL index`. Restore, re-run, green. Note both in the report.

---

### Task 3: The contract — `followUp` on the timeline, two done paths, and the Follow-ups list (D1, D3)

Every change here is **additive**: four new schemas, one new optional property on three existing schemas, three new operations, no `required:` list touched.

**Files:**
- Modify: `openapi/customers.yaml`, `apps/server/internal/openapi/openapi_test.go` (`KnownServeMuxConflicts`, only if the test says so), `apps/server/internal/customers/main_test.go` (read only — confirm `RequireCoverage` still takes no allow-list)
- Generated: `apps/server/internal/openapi/specs/customers.yaml`, `apps/server/internal/customers/gen/api.gen.go`, every changed `api-schema.d.ts`, `openapi/COVERAGE.md`
- Read first (do not change): `openapi/customers.yaml:2537-2723` (the timeline entry's own operations, whose shape the two done paths copy), `:1275-1355` (`getCustomers`, whose `page`/`pageSize` params and paginated 200 the list copies), `:655-675` (`CustomerStatsAttentionItem`, whose description gains two types)

**Interfaces:**
- Produces (contract):
  - `TimelineFollowUpRequest {dueOn (string, required), assigneeUserId (uuid, optional)}`
  - `TimelineFollowUpAssignee {userId, displayName, active}`, all three required
  - `TimelineFollowUp {dueOn (date, required), assignee (nullable), doneAt (date-time, nullable)}`
  - `CustomerFollowUp {entryId, customerId, customerName, eventType, occurredOn, note (nullable), followUp}` — everything but `note` required
  - `PaginatedResponseOfCustomerFollowUp {data, pagination}`
  - `followUp` (nullable) on `TimelineManualTimelineRequest`, `TimelineResponse` and `TimelineRevisionResponse`
  - operations `postCustomersByIdTimelineByEntryIdFollowUpDone`, `deleteCustomersByIdTimelineByEntryIdFollowUpDone` (both `permission:customers:timeline-manage+customers:timeline-view`, both answering `TimelineResponse`), `getCustomersFollowUps` (`permission:customers:timeline-view+customers:view`)
- Produces Go (oapi-codegen; **verify against the generated file**, and follow it where it differs): `gen.TimelineFollowUpRequest{AssigneeUserId *openapi_types.UUID; DueOn *string}`, `gen.TimelineFollowUpAssignee{Active bool; DisplayName string; UserId openapi_types.UUID}`, `gen.TimelineFollowUp{Assignee *TimelineFollowUpAssignee; DoneAt *time.Time; DueOn openapi_types.Date}`, `FollowUp *TimelineFollowUpRequest` on the manual request and `FollowUp *TimelineFollowUp` on both response types, `gen.CustomerFollowUp`, `gen.GetCustomersFollowUps200JSONResponse PaginatedResponseOfCustomerFollowUp`, `gen.GetCustomersFollowUpsParams{Page, PageSize *int32; Assignee, State *string; CustomerId *int32}`, and the three new `…RequestObject`/`…ResponseObject` families.

- [ ] **Step 1: Add the three timeline follow-up schemas**

In `openapi/customers.yaml`, put these three **after** `SafeTimelineSummary`'s block ends (line 1082) and **before** `TimelineListResponse` (line 1083). Indentation is eight spaces for the schema name, twelve for `properties`.

The schema block is **not** sorted — `CustomerTypeRequest` sits between `CreateCustomerRequest` and `CreateCustomerResponse`, and `CustomerStatsAttentionItem` after `CustomerTagsResponse` — so there is no ordering rule to satisfy and nothing will move a misplaced block for you. Place each addition beside the schemas it belongs with, as this plan does, and leave the rest of the file alone.

```yaml
        TimelineFollowUp:
            description: 'What happens next on this timeline entry (follow-ups design D1): a due date, optionally somebody it is assigned to, and a stamp once it is done. Absent when the entry carries no follow-up, which is most entries and every generated one. dueOn is a calendar date and may be in the future — unlike occurredOn, which records something that already happened. assignee is resolved from the user directory at read time and never stored on the entry: a user the directory no longer knows is reported as "Unknown user" with active false, and an assignee disabled after being given the follow-up keeps it and is reported with active false. It is absent when the follow-up is unassigned, which design D2 reads as everyone''s until somebody takes it. doneAt is absent while the follow-up is open.'
            properties:
                assignee:
                    allOf:
                        - $ref: '#/components/schemas/TimelineFollowUpAssignee'
                    nullable: true
                doneAt:
                    format: date-time
                    nullable: true
                    type: string
                dueOn:
                    format: date
                    type: string
            required:
                - dueOn
            type: object
        TimelineFollowUpAssignee:
            description: The user a follow-up is assigned to (follow-ups design D1), named through the user directory exactly as a customer's owner is — same three fields, same treatment of a disabled or forgotten account. A separate schema from CustomerOwner rather than a reuse of it: an owner is accountable for a customer relationship and an assignee is expected to do one thing by one date, and a shared schema would have made the two impossible to describe apart.
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
        TimelineFollowUpRequest:
            description: 'The follow-up to set on a manual timeline entry (follow-ups design D1). dueOn is a strict yyyy-MM-dd and may be in the future. assigneeUserId must name an existing, active user — a field error on followUp.assigneeUserId otherwise — and omitting it leaves the follow-up unassigned, which is everyone''s rather than nobody''s. Sending the whole object as null, or omitting it, CLEARS the entry''s follow-up on a PUT: the entry''s PUT is a full replace, as it already is for occurredAt and sourceUrl, and clearing a follow-up clears its done state with it. doneAt cannot be set here at all — POST and DELETE .../follow-up/done are the only way in and out of that state.'
            properties:
                assigneeUserId:
                    format: uuid
                    nullable: true
                    type: string
                dueOn:
                    nullable: true
                    type: string
            required:
                - dueOn
            type: object
```

`TimelineFollowUpRequest.dueOn` is `nullable: true` **and** in `required:`, which is how `TimelineManualTimelineRequest.eventType`/`occurredOn`/`note` are already declared in this file: required-but-nullable makes the generated Go field a `*string`, so the handler — not the generated binder — answers the module's own validation message for a missing value. Follow that precedent rather than tightening it, or `{"followUp": {}}` becomes a generated 400 whose body is not a `HttpValidationProblemDetails`.

- [ ] **Step 2: Add `followUp` to the manual request and both response shapes**

`TimelineManualTimelineRequest` (lines 1095-1120) gains one property; `required:` is untouched:

```yaml
                followUp:
                    allOf:
                        - $ref: '#/components/schemas/TimelineFollowUpRequest'
                    description: "The follow-up to set on this entry (follow-ups design D1). On a create, omitted means none. On an update, omitted or null CLEARS the entry's follow-up and its done state — this PUT is a full replace, as it already is for occurredAt and sourceUrl."
                    nullable: true
```
It sorts after `expectedRevision` and before `note`.

`TimelineResponse` (lines 1121-1179) gains the same key, sorted after `eventType` and before `id`:

```yaml
                followUp:
                    allOf:
                        - $ref: '#/components/schemas/TimelineFollowUp'
                    description: What happens next on this entry (follow-ups design D1). Absent when the entry carries none.
                    nullable: true
```

`TimelineRevisionResponse` (lines 1189-1246) gains it too, sorted after `eventType` and before `note`:

```yaml
                followUp:
                    allOf:
                        - $ref: '#/components/schemas/TimelineFollowUp'
                    description: The follow-up as it stood at this revision (follow-ups design D1) — a point-in-time snapshot like every other field here, so an entry whose follow-up was later moved or cleared still shows what it said then. Absent when the entry carried none at this revision.
                    nullable: true
```

- [ ] **Step 3: Add the follow-ups list schemas**

`CustomerFollowUp` goes after `CustomerContactRoleRequest`'s block ends (line 421) and before `CustomerOwner` (line 422):

```yaml
        CustomerFollowUp:
            description: One row of the Follow-ups list (follow-ups design D3) — a manual timeline entry that carries a follow-up, with just enough of the entry and of its customer to show a line and link to it. note is the entry's own note cut to its first 200 UTF-16 code units (the same unit the entry's 500-character summary is cut in), absent when the entry has none. The customer is named here rather than fetched per row: the list is read across customers, so the one thing every row needs is whose follow-up it is.
            properties:
                customerId:
                    format: int32
                    type: integer
                customerName:
                    type: string
                entryId:
                    format: int32
                    type: integer
                eventType:
                    type: string
                followUp:
                    $ref: '#/components/schemas/TimelineFollowUp'
                note:
                    nullable: true
                    type: string
                occurredOn:
                    format: date
                    type: string
            required:
                - entryId
                - customerId
                - customerName
                - eventType
                - occurredOn
                - followUp
            type: object
```
`followUp` is a bare `$ref` and is **required** here, unlike on `TimelineResponse`: every row of this list has one by definition — that is what puts it on the list.

`PaginatedResponseOfCustomerFollowUp` goes before `PaginatedResponseOfResponse` (line 981):

```yaml
        PaginatedResponseOfCustomerFollowUp:
            properties:
                data:
                    items:
                        $ref: '#/components/schemas/CustomerFollowUp'
                    type: array
                pagination:
                    $ref: common.yaml#/components/schemas/PaginationMetadata
            required:
                - data
                - pagination
            type: object
```

- [ ] **Step 4: Extend `CustomerStatsAttentionItem`'s description**

The schema's own fields do not change — the two new types reuse `id`/`type`/`title`/`occurredAt`/`entityId` exactly. Its `description` (line 656) is the contract's documentation of what the types mean, so it has to say so. Append to the end of that single-quoted string, before the closing quote (note the doubled `''` for every apostrophe, as the existing text already does):

```
 Two further types come from follow-ups (follow-ups design D2) and are the first items here that depend on WHO is asking: followUpOverdue and followUpDue report open follow-ups on non-archived customers assigned to the caller or unassigned — an unassigned follow-up is everyone''s until somebody takes it — with dueOn before today (UTC) and dueOn equal to today respectively. For those two, id is ''<type>/<entryId>'', entityId is still the CUSTOMER id (the host links a customers item to /customers/{entityId}), title is still the customer''s name, and occurredAt is dueOn at midnight UTC — so an overdue follow-up sorts by how overdue it is, not by when it was noticed. Both clear when the follow-up is ticked done, reopened onto a later date, cleared, when its entry is deleted, or when the customer is archived.
```

- [ ] **Step 5: Add the two done operations**

A new path block, placed between `/api/v1/customers/{id}/timeline/{entryId}` (which ends at line 2682) and `/api/v1/customers/{id}/timeline/{entryId}/revisions` (line 2683). Methods within a path block are alphabetical (`delete` before `get` before `post` before `put`, as the existing blocks show), so `delete` comes first:

```yaml
    /api/v1/customers/{id}/timeline/{entryId}/follow-up/done:
        delete:
            description: Reopens a follow-up that was marked done. Idempotent — reopening an open follow-up answers 200 and changes nothing — and it takes no expectedRevision, for the same reason the POST does not. 404 when the entry carries no follow-up at all; 409 when the entry is generated, deleted or voided.
            operationId: deleteCustomersByIdTimelineByEntryIdFollowUpDone
            parameters:
                - in: path
                  name: id
                  required: true
                  schema:
                    format: int32
                    type: integer
                - in: path
                  name: entryId
                  required: true
                  schema:
                    format: int32
                    type: integer
            responses:
                "200":
                    content:
                        application/json:
                            schema:
                                $ref: '#/components/schemas/TimelineResponse'
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
                "404":
                    description: Not Found
                "409":
                    content:
                        application/problem+json:
                            schema:
                                $ref: common.yaml#/components/schemas/ProblemDetails
                    description: Conflict
            summary: Reopen a timeline entry's follow-up
            tags:
                - Customers
            x-vantigo-access: permission:customers:timeline-manage+customers:timeline-view
        post:
            description: 'Marks the entry''s follow-up done. Idempotent — ticking an already-done follow-up answers 200 and changes nothing, writing no new revision — and it deliberately takes NO expectedRevision: a tick comes from a list and must not lose a race with somebody editing the note. It is still a revision of the entry when it changes something, so currentRevision bumps and the revision history records who ticked it. 404 when the entry carries no follow-up at all; 409 when the entry is generated, deleted or voided.'
            operationId: postCustomersByIdTimelineByEntryIdFollowUpDone
            parameters:
                - in: path
                  name: id
                  required: true
                  schema:
                    format: int32
                    type: integer
                - in: path
                  name: entryId
                  required: true
                  schema:
                    format: int32
                    type: integer
            responses:
                "200":
                    content:
                        application/json:
                            schema:
                                $ref: '#/components/schemas/TimelineResponse'
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
                "404":
                    description: Not Found
                "409":
                    content:
                        application/problem+json:
                            schema:
                                $ref: common.yaml#/components/schemas/ProblemDetails
                    description: Conflict
            summary: Mark a timeline entry's follow-up done
            tags:
                - Customers
            x-vantigo-access: permission:customers:timeline-manage+customers:timeline-view
```

Both answer `TimelineResponse` rather than 204, and both therefore carry `customers:timeline-view` alongside `customers:timeline-manage` — the pair the timeline's own POST and PUT carry, for the same reason: an operation that hands back an entry needs the permission to read one. (The entry DELETE is `timeline-manage` alone because it answers 204 and shows nothing.) Answering the whole entry is what lets a tick from a list update the row's `currentRevision` in place, so the next edit of that entry does not 409 on a revision the client never saw move.

- [ ] **Step 6: Add the Follow-ups list operation**

A new path block, placed between `/api/v1/customers/contacts/{id}/customers` (ends line 3007) and `/api/v1/customers/lookup/brreg` (line 3008):

```yaml
    /api/v1/customers/follow-ups:
        get:
            description: 'Every follow-up the caller asked for, across customers, oldest due date first and then by entry id. Defaults answer the question the page exists for: assignee=me and state=open, i.e. "what is on my plate". Archived customers'' follow-ups are excluded unless state=done — a done follow-up is a record of work finished, and an archived customer''s finished work is still finished.'
            operationId: getCustomersFollowUps
            parameters:
                - in: query
                  name: page
                  schema:
                    format: int32
                    type: integer
                - in: query
                  name: pageSize
                  schema:
                    format: int32
                    type: integer
                - in: query
                  name: assignee
                  schema:
                    description: "A user id, or the literal 'me' (the calling user, resolved from the session and never sent) or 'none' (follow-ups nobody has taken). Case-sensitive. Defaults to 'me'."
                    type: string
                - in: query
                  name: state
                  schema:
                    description: "'open' (not yet done), 'overdue' (open and past its due date, a subset of open), 'done' or 'all'. Case-sensitive. Defaults to 'open'."
                    type: string
                - in: query
                  name: customerId
                  schema:
                    description: Narrows the list to one customer's follow-ups.
                    format: int32
                    type: integer
            responses:
                "200":
                    content:
                        application/json:
                            schema:
                                $ref: '#/components/schemas/PaginatedResponseOfCustomerFollowUp'
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
            summary: List follow-ups across customers
            tags:
                - Customers
            x-vantigo-access: permission:customers:timeline-view+customers:view
```

The access pair is the spec's own (`customers:timeline-view` + `customers:view`): the rows are timeline data, and each one names a customer, so both doors have to be open. `sortedAndUnique` (`internal/openapi/lint.go`) requires the names in ascending order, and `customers:timeline-view` < `customers:view` because `t` < `v` — write it exactly as above. Its 400 is a plain `ProblemDetails`, not `HttpValidationProblemDetails`, matching `getCustomers`: query-parameter failures in this module are one joined `detail` sentence, never a field map.

- [ ] **Step 7: Generate both sides and read what came out**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- go generate ./... && mise exec -- go generate ./... && git status --short
cd /home/anders/projects/vantigo/vantigo && mise exec -- bun run gen:client && git status --short
```

`apps/server/internal/openapi/specs/customers.yaml` is a **byte copy** — `generate.go`'s first directive is `rm -f internal/openapi/specs/*.yaml && cp ../../openapi/*.yaml internal/openapi/specs/`, nothing more — so it reorders nothing and there is no "move it to match" step. `git diff` on it should show exactly the diff you made to `openapi/customers.yaml`, and anything else means the copy was stale before you started. Neither the schema block nor the paths block of that file is sorted (`tags` precedes `type` precedes `timeline` today), so where each addition sits is decided here and nowhere else.

Then read `apps/server/internal/customers/gen/api.gen.go` and confirm each type named in **Interfaces** above. Three things to check specifically, because the plan's later Go is written against them:
- `TimelineFollowUp.Assignee` is `*TimelineFollowUpAssignee` and `TimelineFollowUp.DueOn` is a bare `openapi_types.Date` (required), not a pointer;
- `TimelineResponse.FollowUp` and `TimelineRevisionResponse.FollowUp` are `*TimelineFollowUp`;
- `GetCustomersFollowUps200JSONResponse` is a named alias of `PaginatedResponseOfCustomerFollowUp` (so it is constructed as a struct literal with `Data` and `Pagination`), the way `GetCustomers200JSONResponse` is.

Where oapi-codegen disagrees, **follow it** and adjust Task 4's call sites.

- [ ] **Step 8: Let the router tests tell you about ServeMux conflicts**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- go test -count=1 -run 'TestServeMuxConflictsArePinned|TestKnownServeMuxConflictsMountOnModuleRouter|TestRecordedExchangesMatchTheContract' ./internal/openapi/
```

**The expectation is PASS with no edit to `KnownServeMuxConflicts`,** and the reasoning is worth checking against the result rather than assuming: Go 1.22's `ServeMux` prefers a more specific pattern over a less specific one, so `GET /api/v1/customers/follow-ups` beats `GET /api/v1/customers/{id}` the same way `GET /api/v1/customers/assignable-users`, `/tags` and `/stats` already do — none of which is pinned. The pinned pairs are all *crossings*, where each pattern has a literal where the other has a wildcard (`/customers/contacts/{id}` vs `/customers/{id}/addresses`), and the two done paths are six segments deep where no other pattern in any module reaches, so neither can cross anything.

If the test **does** report new pairs, it prints them: `got: […]` minus `want: […]`. Add exactly those strings to `KnownServeMuxConflicts` (`internal/openapi/openapi_test.go:184`), in the list's sorted position, and nothing else. Then re-run both tests — `TestKnownServeMuxConflictsMountOnModuleRouter` is what proves the precedence-aware router still mounts each new pair cleanly, and a failure **there** is a real routing problem, not a pin to add.

`TestRecordedExchangesMatchTheContract` must also be green: the corpus records the timeline's own exchanges, and `followUp` is a new optional property, so nothing recorded can fail against it.

- [ ] **Step 9: Confirm the coverage gate's shape and regenerate COVERAGE.md**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
grep -n 'RequireCoverage' internal/customers/main_test.go
mise exec -- go run ./internal/openapi/cmd/contract coverage -corpus ../../openapi/testdata/exchanges -out ../../openapi/COVERAGE.md
cd /home/anders/projects/vantigo/vantigo && git diff --stat openapi/COVERAGE.md
```
`main_test.go` must still read `os.Exit(contracttest.RequireCoverage(m, recorder))` with **no** pending list — the three new operations get real tests in Task 4, and an allow-list entry is not an option here. `COVERAGE.md`'s customers section gains the three new operations (no recorded .NET exchange exercises them — there was no .NET follow-up feature) and its heading count goes up by three.

Running the whole customers suite now will therefore **fail at the coverage gate** with `getCustomersFollowUps`, `postCustomersByIdTimelineByEntryIdFollowUpDone` and `deleteCustomersByIdTimelineByEntryIdFollowUpDone` listed as missing, and the package will not compile at all until Task 4 implements the three handlers (`var _ gen.StrictServerInterface = (*server)(nil)` in `server.go` is what makes that a compile error rather than a runtime one). **That is expected**: this task's commit is a contract commit whose Go half is Task 4, and the two are committed separately only if the tree is green in between. It is not, so **Tasks 3 and 4 share one commit**: do Step 10 below, then go straight to Task 4 and commit there.

- [ ] **Step 10: Stage, do not commit**

```bash
cd /home/anders/projects/vantigo/vantigo
git add openapi/customers.yaml apps/server/internal/openapi/specs/customers.yaml apps/server/internal/customers/gen openapi/COVERAGE.md
git add $(git status --short | grep 'api-schema.d.ts' | awk '{print $2}')
git status --short
```
Leave them staged. Task 4 adds its own files and commits both halves together with the message given there. (If `KnownServeMuxConflicts` needed a pin, `git add apps/server/internal/openapi/openapi_test.go` too.)

---

### Task 4: The server — validation, the two done paths, attention and the list (D1, D2, D3)

This task shares Task 3's commit: the contract is staged and the package does not compile until these three handlers exist.

**Files:**
- Create: `apps/server/internal/customers/follow_ups.go`, `apps/server/internal/customers/follow_ups_test.go`, `apps/server/internal/customers/follow_ups_concurrency_test.go`
- Modify: `apps/server/internal/customers/timeline.go`, `apps/server/internal/customers/stats.go`
- Deliberately **not** modified: `harness_test.go` (no fixture needs a follow-up; `follow_ups_test.go` brings its own) and `timeline_test.go` (its `timelineEntryJSON` does not declare `followUp` and `encoding/json` simply ignores the field — widening a .NET port would blur what it ports)
- Read first (do not change): `apps/server/internal/customers/owner.go:92-189` (`decorate`/`decorateKnowing`/`owner` — the batched-directory-call and unknown-user pattern this task copies), `apps/server/internal/customers/customers.go:294-370` (`GetCustomers`' parameter validation and the `ownerId=me` resolution this task copies), `apps/server/internal/customers/timeline_concurrency_test.go:1-60` (the forced-race pattern), `apps/server/internal/customers/stats_internal_test.go` (what `attentionItemsFrom` is already held to)

**Interfaces:**
- Consumes: Task 2's five queries and the three columns on every timeline row; Task 3's generated types.
- Produces Go:
  - `parsedFollowUp{DueOn time.Time; AssigneeID *uuid.UUID}`, and `parsedManualTimeline.FollowUp *parsedFollowUp`
  - `validateManualTimelineRequest(body gen.TimelineManualTimelineRequest, now time.Time) (parsedManualTimeline, map[string][]string)` — unchanged signature, one more field validated
  - `timelineResponse(e store.CustomersCustomersTimelineEntry, dec followUpDecoration) gen.TimelineResponse` and `timelineRevisionResponse(r store.CustomersCustomersTimelineEntriesRevision, dec followUpDecoration) gen.TimelineRevisionResponse` — **both gain a parameter**
  - `followUpDecoration` with `assignee(id *uuid.UUID) *gen.TimelineFollowUpAssignee`
  - `(*server).decorateFollowUpAssignees(ctx, ids []uuid.UUID) (followUpDecoration, error)`, `entryAssigneeIDs(...store.CustomersCustomersTimelineEntry) []uuid.UUID`, `revisionAssigneeIDs([]store.CustomersCustomersTimelineEntriesRevision) []uuid.UUID`
  - `attentionFollowUpOverdue = "followUpOverdue"`, `attentionFollowUpDue = "followUpDue"`, `followUpAttentionItems(rows, today) []gen.CustomerStatsAttentionItem`, `sortAttentionItems([]gen.CustomerStatsAttentionItem)`

- [ ] **Step 1: Three stubs, so the package compiles and the tests can be red for the right reason**

At the bottom of a new `apps/server/internal/customers/follow_ups.go`, with the package's imports for now just `context` and `errors`:

```go
package customers

import (
	"context"
	"errors"

	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
)

// TEMPORARY, removed in Step 4: gen.StrictServerInterface must be satisfied for
// this package to compile at all (server.go's var _ assertion), and the tests
// below have to be able to run and fail before the real implementation exists.
func (s *server) PostCustomersByIdTimelineByEntryIdFollowUpDone(ctx context.Context, req gen.PostCustomersByIdTimelineByEntryIdFollowUpDoneRequestObject) (gen.PostCustomersByIdTimelineByEntryIdFollowUpDoneResponseObject, error) {
	return nil, errors.New("customers: follow-up done not implemented")
}

func (s *server) DeleteCustomersByIdTimelineByEntryIdFollowUpDone(ctx context.Context, req gen.DeleteCustomersByIdTimelineByEntryIdFollowUpDoneRequestObject) (gen.DeleteCustomersByIdTimelineByEntryIdFollowUpDoneResponseObject, error) {
	return nil, errors.New("customers: follow-up reopen not implemented")
}

func (s *server) GetCustomersFollowUps(ctx context.Context, req gen.GetCustomersFollowUpsRequestObject) (gen.GetCustomersFollowUpsResponseObject, error) {
	return nil, errors.New("customers: follow-ups list not implemented")
}
```

Take the exact method names and request/response type names from `internal/customers/gen/api.gen.go`'s `StrictServerInterface` — `grep -n 'FollowUp' internal/customers/gen/api.gen.go | head -40` — and use those, not this plan's guess.

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server && mise exec -- go build ./internal/customers/
```
Expected: builds.

- [ ] **Step 2: Write the failing tests**

Create `apps/server/internal/customers/follow_ups_test.go`. It is long, so read it in four groups: the fixtures, create/update, done/reopen, and attention + the list.

```go
package customers_test

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// This file is follow-ups design D1, D2 and D3 end to end: the follow-up a
// manual entry carries, the two paths that tick and untick it, the two
// attention types it feeds, and the list that answers "what is on my plate".
//
// Every date here is written relative to the harness clock (h.Now()), never as
// a literal: the overdue-vs-due-today split is a UTC calendar comparison, and a
// hard-coded date makes a test that passes today and fails on a day this
// module's clock is moved.

type followUpAssigneeJSON struct {
	UserId      uuid.UUID `json:"userId"`
	DisplayName string    `json:"displayName"`
	Active      bool      `json:"active"`
}

type followUpJSON struct {
	DueOn    string                `json:"dueOn"`
	Assignee *followUpAssigneeJSON `json:"assignee"`
	DoneAt   *time.Time            `json:"doneAt"`
}

// followUpEntryJSON is timelineEntryJSON's fields this file cares about plus
// the follow-up. A separate type rather than a widening of timelineEntryJSON:
// the tests already in timeline_test.go assert an entry's whole shape, and a
// field added there would have to be asserted in every one of them.
type followUpEntryJSON struct {
	Id              int32         `json:"id"`
	EventType       string        `json:"eventType"`
	Provenance      string        `json:"provenance"`
	OccurredOn      string        `json:"occurredOn"`
	Note            *string       `json:"note"`
	CurrentRevision int32         `json:"currentRevision"`
	State           string        `json:"state"`
	FollowUp        *followUpJSON `json:"followUp"`
}

type followUpRowJSON struct {
	EntryId      int32        `json:"entryId"`
	CustomerId   int32        `json:"customerId"`
	CustomerName string       `json:"customerName"`
	EventType    string       `json:"eventType"`
	OccurredOn   string       `json:"occurredOn"`
	Note         *string      `json:"note"`
	FollowUp     followUpJSON `json:"followUp"`
}

type followUpListJSON struct {
	Data       []followUpRowJSON `json:"data"`
	Pagination struct {
		Page       int32 `json:"page"`
		PageSize   int32 `json:"pageSize"`
		TotalCount int32 `json:"totalCount"`
		TotalPages int32 `json:"totalPages"`
	} `json:"pagination"`
}

// day is the UTC calendar date `offset` days from the harness clock, formatted
// the way the contract wants it.
func day(h *modtest.Harness, offset int) string {
	return h.Now().UTC().AddDate(0, 0, offset).Format("2006-01-02")
}

// createWithFollowUp posts a manual entry carrying followUp and returns it,
// failing the test on anything but 201.
func createWithFollowUp(t *testing.T, c *modtest.Client, customerID int32, occurredOn, note string, followUp map[string]any) followUpEntryJSON {
	t.Helper()
	body := map[string]any{"eventType": "note", "occurredOn": occurredOn, "note": note}
	if followUp != nil {
		body["followUp"] = followUp
	}
	r := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/customers/%d/timeline", customerID), body)
	if r.Status != http.StatusCreated {
		t.Fatalf("create entry with %v: status %d body %s, want 201", followUp, r.Status, r.Body)
	}
	var entry followUpEntryJSON
	r.JSON(&entry)
	return entry
}

// followUpDone posts (done=true) or deletes (done=false) the entry's
// follow-up-done state and answers the raw response, so a test can assert a
// status this helper has no business deciding.
func followUpDone(t *testing.T, c *modtest.Client, customerID, entryID int32, done bool) *modtest.Response {
	t.Helper()
	path := fmt.Sprintf("/api/v1/customers/%d/timeline/%d/follow-up/done", customerID, entryID)
	if done {
		return c.Do(http.MethodPost, path, nil)
	}
	return c.Do(http.MethodDelete, path, nil)
}

// listFollowUps reads GET /customers/follow-ups with query, failing the test on
// anything but 200.
func listFollowUps(t *testing.T, c *modtest.Client, query url.Values) followUpListJSON {
	t.Helper()
	path := "/api/v1/customers/follow-ups"
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	r := c.Do(http.MethodGet, path, nil)
	if r.Status != http.StatusOK {
		t.Fatalf("list follow-ups %v: status %d body %s, want 200", query, r.Status, r.Body)
	}
	var list followUpListJSON
	r.JSON(&list)
	return list
}
```

Then the create/update group:

```go
// TestPostTimeline_CarriesAFollowUpAndAcceptsAFutureDate pins the two things
// design D1 says about dueOn that occurredOn does not: it is a strict
// yyyy-MM-dd, and it MAY be in the future — that is the whole point of a
// follow-up. The assignee comes back named from the directory, not as a bare id.
func TestPostTimeline_CarriesAFollowUpAndAcceptsAFutureDate(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, caller := authenticatedClientWithID(t, h)
	setDisplayName(t, h, caller, "Kari Nordmann")
	customer := createCustomer(t, c, "Follow Up Co")

	entry := createWithFollowUp(t, c, customer.Id, day(h, 0), "Call about the renewal", map[string]any{
		"dueOn": day(h, 14), "assigneeUserId": caller,
	})
	if entry.FollowUp == nil {
		t.Fatalf("followUp is absent, want one: %+v", entry)
	}
	if entry.FollowUp.DueOn != day(h, 14) {
		t.Errorf("dueOn = %q, want %q (a follow-up may be in the future)", entry.FollowUp.DueOn, day(h, 14))
	}
	if entry.FollowUp.DoneAt != nil {
		t.Errorf("doneAt = %v, want absent on a new follow-up", entry.FollowUp.DoneAt)
	}
	if entry.FollowUp.Assignee == nil || entry.FollowUp.Assignee.DisplayName != "Kari Nordmann" || !entry.FollowUp.Assignee.Active {
		t.Errorf("assignee = %+v, want Kari Nordmann, active", entry.FollowUp.Assignee)
	}

	// An entry with no followUp key answers no followUp at all — omitted, never
	// null, like every other optional field this API answers with.
	plain := createWithFollowUp(t, c, customer.Id, day(h, 0), "Just a note", nil)
	if plain.FollowUp != nil {
		t.Errorf("followUp = %+v, want absent when the request carried none", plain.FollowUp)
	}
}

// TestPostTimeline_RefusesAMalformedDueDateAndAnUnusableAssignee pins the two
// field errors, both keyed the way design D1 names them, and the wording of the
// assignee's — the owner's own, because it is the same claim about the same
// directory.
func TestPostTimeline_RefusesAMalformedDueDateAndAnUnusableAssignee(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Bad Follow Up Co")
	gone := seedNamedUser(t, h, "Vanished Personsen")
	forgetUser(t, h, gone)
	disabled := seedNamedUser(t, h, "Disabled Personsen")
	disableUser(t, h, disabled)

	cases := []struct {
		name     string
		followUp map[string]any
		field    string
		message  string
	}{
		{"a looser date", map[string]any{"dueOn": "2026-9-1"}, "followUp.dueOn", "FollowUp.dueOn must be an ISO date (yyyy-MM-dd)"},
		{"no date at all", map[string]any{}, "followUp.dueOn", "FollowUp.dueOn must be an ISO date (yyyy-MM-dd)"},
		{"a user who does not exist", map[string]any{"dueOn": "2026-10-01", "assigneeUserId": gone}, "followUp.assigneeUserId", fmt.Sprintf("User %s does not exist", gone)},
		{"a disabled user", map[string]any{"dueOn": "2026-10-01", "assigneeUserId": disabled}, "followUp.assigneeUserId", fmt.Sprintf("User %s is disabled and cannot be given a follow-up", disabled)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := c.Do(http.MethodPost, fmt.Sprintf("/api/v1/customers/%d/timeline", customer.Id), map[string]any{
				"eventType": "note", "occurredOn": day(h, 0), "note": "n", "followUp": tc.followUp,
			})
			if r.Status != http.StatusBadRequest {
				t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
			}
			var problem validationProblemJSON
			r.JSON(&problem)
			if msgs := problem.Errors[tc.field]; len(msgs) != 1 || msgs[0] != tc.message {
				t.Errorf("errors[%s] = %v, want [%q]", tc.field, msgs, tc.message)
			}
		})
	}
}

// TestPutTimeline_ReplacesTheFollowUpAndClearingItClearsDone is design D1's
// "set, replace or clear with the entry" and its one consequence: clearing the
// follow-up clears its done state, because done-ness without a follow-up is not
// a state this module has. The PUT is a full replace, so an omitted followUp
// clears it exactly as an omitted sourceUrl already clears that.
func TestPutTimeline_ReplacesTheFollowUpAndClearingItClearsDone(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, caller := authenticatedClientWithID(t, h)
	setDisplayName(t, h, caller, "Kari Nordmann")
	other := seedNamedUser(t, h, "Ola Nordmann")
	customer := createCustomer(t, c, "Replace Follow Up Co")
	entry := createWithFollowUp(t, c, customer.Id, day(h, 0), "original", map[string]any{
		"dueOn": day(h, 3), "assigneeUserId": caller,
	})

	if r := followUpDone(t, c, customer.Id, entry.Id, true); r.Status != http.StatusOK {
		t.Fatalf("mark done: status %d body %s, want 200", r.Status, r.Body)
	}

	// Replaced: a new date, a new assignee — and the done stamp survives,
	// because the follow-up was kept, not cleared.
	replaced := putEntryWithFollowUp(t, c, customer.Id, entry.Id, 2, map[string]any{
		"dueOn": day(h, 9), "assigneeUserId": other,
	})
	if replaced.FollowUp == nil || replaced.FollowUp.DueOn != day(h, 9) {
		t.Fatalf("followUp = %+v, want dueOn %s", replaced.FollowUp, day(h, 9))
	}
	if replaced.FollowUp.Assignee == nil || replaced.FollowUp.Assignee.DisplayName != "Ola Nordmann" {
		t.Errorf("assignee = %+v, want Ola Nordmann", replaced.FollowUp.Assignee)
	}
	if replaced.FollowUp.DoneAt == nil {
		t.Errorf("doneAt = nil, want it kept: editing a ticked follow-up must not un-tick it")
	}

	// Cleared: no followUp key at all, which this PUT reads as "none".
	cleared := putEntryWithFollowUp(t, c, customer.Id, entry.Id, replaced.CurrentRevision, nil)
	if cleared.FollowUp != nil {
		t.Errorf("followUp = %+v, want absent after a PUT that carried none", cleared.FollowUp)
	}
	var doneAt *time.Time
	if err := h.Pool().QueryRow(t.Context(),
		`SELECT follow_up_done_at FROM customers.customers_timeline_entries WHERE id = $1`, entry.Id).Scan(&doneAt); err != nil {
		t.Fatalf("read follow_up_done_at: %v", err)
	}
	if doneAt != nil {
		t.Errorf("follow_up_done_at = %v, want NULL: clearing a follow-up clears its done state", doneAt)
	}

	// An explicit null is the same instruction, and says it out loud. It cannot
	// go through putEntryWithFollowUp, which omits the KEY for a nil map — and
	// "the key is absent" and "the key is null" are exactly the two forms this
	// assertion exists to prove are one instruction. So this one builds the body
	// itself, with a literal JSON null on the wire.
	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/timeline/%d", customer.Id, entry.Id), map[string]any{
		"eventType": "note", "occurredOn": "2020-01-01", "note": "edited again",
		"expectedRevision": cleared.CurrentRevision, "followUp": nil,
	})
	if r.Status != http.StatusOK {
		t.Fatalf("explicit null followUp: status %d body %s, want 200", r.Status, r.Body)
	}
	var again followUpEntryJSON
	r.JSON(&again)
	if again.FollowUp != nil {
		t.Errorf("followUp = %+v, want absent after an explicit null", again.FollowUp)
	}
}

// putEntryWithFollowUp updates an entry, sending followUp when it is non-nil
// and omitting the key entirely when it is nil, and answers the entry.
func putEntryWithFollowUp(t *testing.T, c *modtest.Client, customerID, entryID, expectedRevision int32, followUp map[string]any) followUpEntryJSON {
	t.Helper()
	body := map[string]any{
		"eventType": "note", "occurredOn": "2020-01-01", "note": "edited", "expectedRevision": expectedRevision,
	}
	if followUp != nil {
		body["followUp"] = followUp
	}
	r := c.Do(http.MethodPut, fmt.Sprintf("/api/v1/customers/%d/timeline/%d", customerID, entryID), body)
	if r.Status != http.StatusOK {
		t.Fatalf("put entry with %v: status %d body %s, want 200", followUp, r.Status, r.Body)
	}
	var entry followUpEntryJSON
	r.JSON(&entry)
	return entry
}
```
The `"occurredOn": "2020-01-01"` literal in `putEntryWithFollowUp` is deliberate and is the one date not written from the clock: `occurredOn` may not be in the future, and a fixed past date is the safest thing to send when the test is about something else.

Then the done/reopen group:

```go
// TestFollowUpDone_IsIdempotentAndEachRealChangeIsARevision is design D1's own
// sentence, split into its four claims: the first tick bumps current_revision
// and appends a revision row naming who ticked it; the second tick answers 200
// and writes NOTHING; reopening mirrors both; and neither path takes an
// expectedRevision, so a stale client can still tick.
func TestFollowUpDone_IsIdempotentAndEachRealChangeIsARevision(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, caller := authenticatedClientWithID(t, h)
	setDisplayName(t, h, caller, "Kari Nordmann")
	customer := createCustomer(t, c, "Idempotent Co")
	entry := createWithFollowUp(t, c, customer.Id, day(h, 0), "Ring back", map[string]any{"dueOn": day(h, 1)})
	if entry.CurrentRevision != 1 {
		t.Fatalf("currentRevision = %d, want 1 on a fresh entry", entry.CurrentRevision)
	}

	first := followUpDone(t, c, customer.Id, entry.Id, true)
	if first.Status != http.StatusOK {
		t.Fatalf("first tick: status %d body %s, want 200", first.Status, first.Body)
	}
	var ticked followUpEntryJSON
	first.JSON(&ticked)
	if ticked.CurrentRevision != 2 {
		t.Errorf("currentRevision = %d, want 2: a tick is a revision of the entry", ticked.CurrentRevision)
	}
	if ticked.FollowUp == nil || ticked.FollowUp.DoneAt == nil {
		t.Fatalf("followUp = %+v, want a doneAt", ticked.FollowUp)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers_timeline_entries_revisions WHERE customer_timeline_entry_id = $1`, entry.Id); n != 2 {
		t.Errorf("revision rows = %d, want 2", n)
	}
	// The revision row records WHO ticked it, which is the reason a tick is a
	// revision at all rather than a quiet column write.
	who := modtest.One[string](t, h, `SELECT actor_display FROM customers.customers_timeline_entries_revisions
	                                  WHERE customer_timeline_entry_id = $1 ORDER BY revision_number DESC LIMIT 1`, entry.Id)
	if want := userDisplayName(t, h, caller); who != want {
		t.Errorf("revision actor = %q, want %q", who, want)
	}
	// And the revision snapshot carries the follow-up as it stood.
	doneInRevision := modtest.One[bool](t, h, `SELECT follow_up_done_at IS NOT NULL FROM customers.customers_timeline_entries_revisions
	                                           WHERE customer_timeline_entry_id = $1 ORDER BY revision_number DESC LIMIT 1`, entry.Id)
	if !doneInRevision {
		t.Errorf("revision follow_up_done_at is NULL, want the tick snapshotted into history")
	}

	second := followUpDone(t, c, customer.Id, entry.Id, true)
	if second.Status != http.StatusOK {
		t.Fatalf("second tick: status %d body %s, want 200 (idempotent)", second.Status, second.Body)
	}
	var again followUpEntryJSON
	second.JSON(&again)
	if again.CurrentRevision != 2 {
		t.Errorf("currentRevision = %d, want 2: a no-op tick writes nothing", again.CurrentRevision)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers_timeline_entries_revisions WHERE customer_timeline_entry_id = $1`, entry.Id); n != 2 {
		t.Errorf("revision rows = %d, want 2 after a no-op tick", n)
	}

	reopened := followUpDone(t, c, customer.Id, entry.Id, false)
	if reopened.Status != http.StatusOK {
		t.Fatalf("reopen: status %d body %s, want 200", reopened.Status, reopened.Body)
	}
	var open followUpEntryJSON
	reopened.JSON(&open)
	if open.CurrentRevision != 3 || open.FollowUp == nil || open.FollowUp.DoneAt != nil {
		t.Errorf("after reopen: revision %d followUp %+v, want revision 3 and no doneAt", open.CurrentRevision, open.FollowUp)
	}
	if r := followUpDone(t, c, customer.Id, entry.Id, false); r.Status != http.StatusOK {
		t.Errorf("second reopen: status %d body %s, want 200 (idempotent)", r.Status, r.Body)
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers_timeline_entries_revisions WHERE customer_timeline_entry_id = $1`, entry.Id); n != 3 {
		t.Errorf("revision rows = %d, want 3", n)
	}
}

// TestFollowUpDone_RefusesWhatHasNoFollowUpAndWhatIsNotManual pins the two
// refusals design D1 names, and that they are told apart: no follow-up is a
// 404 (the thing addressed does not exist), a generated or deleted entry is the
// timeline's own 409.
func TestFollowUpDone_RefusesWhatHasNoFollowUpAndWhatIsNotManual(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Refusal Co")

	plain := createWithFollowUp(t, c, customer.Id, day(h, 0), "no follow-up here", nil)
	if r := followUpDone(t, c, customer.Id, plain.Id, true); r.Status != http.StatusNotFound {
		t.Errorf("entry with no follow-up: status %d body %s, want 404", r.Status, r.Body)
	}
	if r := followUpDone(t, c, customer.Id, 999999, true); r.Status != http.StatusNotFound {
		t.Errorf("missing entry: status %d body %s, want 404", r.Status, r.Body)
	}

	// The customer's own creation wrote a generated entry; it has no follow-up
	// and never can, so the immutability answer must win over the 404 — a
	// caller who aimed at a generated entry needs to be told that, not told the
	// path does not exist.
	generated := modtest.One[int32](t, h, `SELECT id FROM customers.customers_timeline_entries
	                                       WHERE customer_id = $1 AND provenance = 'generated' ORDER BY id LIMIT 1`, customer.Id)
	r := followUpDone(t, c, customer.Id, generated, true)
	if r.Status != http.StatusConflict {
		t.Fatalf("generated entry: status %d body %s, want 409", r.Status, r.Body)
	}
	var problem problemDetailsJSON
	r.JSON(&problem)
	if problemTitle(problem.Title) != "Timeline entry is immutable" {
		t.Errorf("title = %q, want %q", problemTitle(problem.Title), "Timeline entry is immutable")
	}

	// A soft-deleted entry that HAD a follow-up is the same answer.
	deleted := createWithFollowUp(t, c, customer.Id, day(h, 0), "about to go", map[string]any{"dueOn": day(h, 2)})
	if r := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d/timeline/%d?expectedRevision=1", customer.Id, deleted.Id), nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete entry: status %d body %s, want 204", r.Status, r.Body)
	}
	if r := followUpDone(t, c, customer.Id, deleted.Id, true); r.Status != http.StatusConflict {
		t.Errorf("deleted entry: status %d body %s, want 409", r.Status, r.Body)
	}
}

// TestFollowUpDone_NeedsTimelineManage pins the permission pair, and that a
// reader who may see a follow-up may not tick it.
func TestFollowUpDone_NeedsTimelineManage(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	writer := authenticatedClient(t, h)
	customer := createCustomer(t, writer, "Permission Co")
	entry := createWithFollowUp(t, writer, customer.Id, day(h, 0), "Ring back", map[string]any{"dueOn": day(h, 1)})

	reader := h.SignIn(t, "customers:view", "customers:timeline-view")
	if r := followUpDone(t, reader, customer.Id, entry.Id, true); r.Status != http.StatusForbidden {
		t.Errorf("timeline-view only: status %d body %s, want 403", r.Status, r.Body)
	}
	if r := followUpDone(t, reader, customer.Id, entry.Id, false); r.Status != http.StatusForbidden {
		t.Errorf("timeline-view only, reopen: status %d body %s, want 403", r.Status, r.Body)
	}
}

// TestTimelineRevisions_CarryTheFollowUpPerRevision is design D1's "history
// stays point-in-time", read through the endpoint rather than the table: the
// revision taken before a follow-up moved still says what it said.
func TestTimelineRevisions_CarryTheFollowUpPerRevision(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, caller := authenticatedClientWithID(t, h)
	setDisplayName(t, h, caller, "Kari Nordmann")
	customer := createCustomer(t, c, "History Co")
	entry := createWithFollowUp(t, c, customer.Id, day(h, 0), "original", map[string]any{
		"dueOn": day(h, 4), "assigneeUserId": caller,
	})
	putEntryWithFollowUp(t, c, customer.Id, entry.Id, 1, map[string]any{"dueOn": day(h, 40)})

	r := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/timeline/%d/revisions", customer.Id, entry.Id), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("revisions: status %d body %s, want 200", r.Status, r.Body)
	}
	var list struct {
		Data []struct {
			Revision int32         `json:"revision"`
			FollowUp *followUpJSON `json:"followUp"`
		} `json:"data"`
	}
	r.JSON(&list)
	if len(list.Data) != 2 {
		t.Fatalf("len(data) = %d, want 2", len(list.Data))
	}
	if list.Data[0].FollowUp == nil || list.Data[0].FollowUp.DueOn != day(h, 4) {
		t.Errorf("revision 1 followUp = %+v, want dueOn %s", list.Data[0].FollowUp, day(h, 4))
	}
	if list.Data[0].FollowUp.Assignee == nil || list.Data[0].FollowUp.Assignee.DisplayName != "Kari Nordmann" {
		t.Errorf("revision 1 assignee = %+v, want Kari Nordmann", list.Data[0].FollowUp.Assignee)
	}
	if list.Data[1].FollowUp == nil || list.Data[1].FollowUp.DueOn != day(h, 40) {
		t.Errorf("revision 2 followUp = %+v, want dueOn %s", list.Data[1].FollowUp, day(h, 40))
	}
	if list.Data[1].FollowUp.Assignee != nil {
		t.Errorf("revision 2 assignee = %+v, want absent: the replace named nobody", list.Data[1].FollowUp.Assignee)
	}
}

// TestFollowUp_AnAssigneeDisabledOrForgottenAfterwardsKeepsIt is design D1's
// last sentence about the assignee, and the owner's own precedent: nothing is
// silently revoked, and a vanished account reads as "Unknown user", inactive —
// never as a 500 and never as an unassigned follow-up.
func TestFollowUp_AnAssigneeDisabledOrForgottenAfterwardsKeepsIt(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Aftermath Co")
	soonDisabled := seedNamedUser(t, h, "Soon Disabledsen")
	soonGone := seedNamedUser(t, h, "Soon Gonesen")
	disabledEntry := createWithFollowUp(t, c, customer.Id, day(h, 0), "a", map[string]any{"dueOn": day(h, 1), "assigneeUserId": soonDisabled})
	goneEntry := createWithFollowUp(t, c, customer.Id, day(h, 0), "b", map[string]any{"dueOn": day(h, 1), "assigneeUserId": soonGone})

	disableUser(t, h, soonDisabled)
	forgetUser(t, h, soonGone)

	got := fetchFollowUpEntry(t, c, customer.Id, disabledEntry.Id)
	if got.FollowUp == nil || got.FollowUp.Assignee == nil {
		t.Fatalf("followUp = %+v, want an assignee", got.FollowUp)
	}
	if got.FollowUp.Assignee.DisplayName != "Soon Disabledsen" || got.FollowUp.Assignee.Active {
		t.Errorf("assignee = %+v, want Soon Disabledsen, inactive", got.FollowUp.Assignee)
	}
	vanished := fetchFollowUpEntry(t, c, customer.Id, goneEntry.Id)
	if vanished.FollowUp == nil || vanished.FollowUp.Assignee == nil {
		t.Fatalf("followUp = %+v, want an assignee", vanished.FollowUp)
	}
	if vanished.FollowUp.Assignee.DisplayName != "Unknown user" || vanished.FollowUp.Assignee.Active {
		t.Errorf("assignee = %+v, want \"Unknown user\", inactive", vanished.FollowUp.Assignee)
	}
	if vanished.FollowUp.Assignee.UserId != soonGone {
		t.Errorf("assignee userId = %s, want %s: the id is kept even when the name cannot be", vanished.FollowUp.Assignee.UserId, soonGone)
	}
}

func fetchFollowUpEntry(t *testing.T, c *modtest.Client, customerID, entryID int32) followUpEntryJSON {
	t.Helper()
	r := c.Do(http.MethodGet, fmt.Sprintf("/api/v1/customers/%d/timeline/%d", customerID, entryID), nil)
	if r.Status != http.StatusOK {
		t.Fatalf("get entry: status %d body %s, want 200", r.Status, r.Body)
	}
	var entry followUpEntryJSON
	r.JSON(&entry)
	return entry
}
```

And the attention + list group:

```go
// TestStatsAttention_ReportsTheCallersAndUnassignedFollowUpsOnly is design D2
// in full: two types split by a UTC calendar comparison, only open follow-ups,
// only the caller's or nobody's, never an archived customer's, with the item's
// own id/entityId/title/occurredAt shape.
func TestStatsAttention_ReportsTheCallersAndUnassignedFollowUpsOnly(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, caller := authenticatedClientWithID(t, h)
	other := seedNamedUser(t, h, "Somebody Elsesen")
	mine := createCustomer(t, c, "Mine Co")
	theirs := createCustomer(t, c, "Theirs Co")
	archived := createCustomer(t, c, "Archived Co")

	overdue := createWithFollowUp(t, c, mine.Id, day(h, -10), "overdue", map[string]any{"dueOn": day(h, -3), "assigneeUserId": caller})
	dueToday := createWithFollowUp(t, c, mine.Id, day(h, -1), "due today", map[string]any{"dueOn": day(h, 0), "assigneeUserId": caller})
	unassigned := createWithFollowUp(t, c, mine.Id, day(h, -1), "nobody's", map[string]any{"dueOn": day(h, 0)})
	createWithFollowUp(t, c, mine.Id, day(h, -1), "later", map[string]any{"dueOn": day(h, 7), "assigneeUserId": caller})
	createWithFollowUp(t, c, theirs.Id, day(h, -1), "not mine", map[string]any{"dueOn": day(h, -1), "assigneeUserId": other})
	done := createWithFollowUp(t, c, mine.Id, day(h, -1), "finished", map[string]any{"dueOn": day(h, -5), "assigneeUserId": caller})
	if r := followUpDone(t, c, mine.Id, done.Id, true); r.Status != http.StatusOK {
		t.Fatalf("mark done: status %d body %s, want 200", r.Status, r.Body)
	}
	archivedEntry := createWithFollowUp(t, c, archived.Id, day(h, -1), "archived", map[string]any{"dueOn": day(h, -2), "assigneeUserId": caller})
	if r := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d", archived.Id), nil); r.Status != http.StatusNoContent {
		t.Fatalf("archive customer: status %d body %s, want 204", r.Status, r.Body)
	}
	_ = archivedEntry

	items := getAttention(t, c)
	byID := map[string]attentionItemJSON{}
	for _, item := range items {
		byID[item.Id] = item
	}

	wantOverdue := fmt.Sprintf("followUpOverdue/%d", overdue.Id)
	got, ok := byID[wantOverdue]
	if !ok {
		t.Fatalf("items = %+v, want one with id %q", items, wantOverdue)
	}
	if got.Type != "followUpOverdue" || got.Title != "Mine Co" || got.EntityId != fmt.Sprintf("%d", mine.Id) {
		t.Errorf("item = %+v, want type followUpOverdue, title \"Mine Co\", entityId %d", got, mine.Id)
	}
	// occurredAt is the DUE DATE at midnight UTC, not the moment the follow-up
	// was written: an overdue follow-up has to sort by how overdue it is.
	wantAt, err := time.Parse("2006-01-02", day(h, -3))
	if err != nil {
		t.Fatalf("parse want: %v", err)
	}
	if !got.OccurredAt.Equal(wantAt) {
		t.Errorf("occurredAt = %s, want %s (dueOn at midnight UTC)", got.OccurredAt, wantAt)
	}

	for _, id := range []string{
		fmt.Sprintf("followUpDue/%d", dueToday.Id),
		fmt.Sprintf("followUpDue/%d", unassigned.Id),
	} {
		if item, ok := byID[id]; !ok {
			t.Errorf("items = %+v, want one with id %q", items, id)
		} else if item.Type != "followUpDue" {
			t.Errorf("%s type = %q, want followUpDue", id, item.Type)
		}
	}
	for _, kind := range []string{"followUpOverdue", "followUpDue"} {
		for _, entry := range []int32{done.Id, archivedEntry.Id} {
			if _, ok := byID[fmt.Sprintf("%s/%d", kind, entry)]; ok {
				t.Errorf("items = %+v, want no %s for entry %d (done, or an archived customer's)", items, kind, entry)
			}
		}
	}
	// Another user's follow-up, and one not yet due, are nobody's business here.
	if len(items) != 3 {
		t.Errorf("len(items) = %d, want exactly 3: overdue, due today, unassigned", len(items))
	}

	// And the same list asked by somebody else answers only the unassigned one.
	stranger, _ := h.SignInUser(t, "customers:view")
	strangerItems := getAttention(t, stranger)
	if len(strangerItems) != 1 || strangerItems[0].Id != fmt.Sprintf("followUpDue/%d", unassigned.Id) {
		t.Errorf("stranger's items = %+v, want only the unassigned follow-up", strangerItems)
	}
}

// TestGetFollowUps_DefaultsToMyOpenOnes is design D3's defaults, its ordering
// and its row shape, all read off one request.
func TestGetFollowUps_DefaultsToMyOpenOnes(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, caller := authenticatedClientWithID(t, h)
	setDisplayName(t, h, caller, "Kari Nordmann")
	other := seedNamedUser(t, h, "Somebody Elsesen")
	alpha := createCustomer(t, c, "Alpha Co")
	beta := createCustomer(t, c, "Beta Co")

	late := createWithFollowUp(t, c, alpha.Id, day(h, -9), "ring Alpha", map[string]any{"dueOn": day(h, -2), "assigneeUserId": caller})
	soon := createWithFollowUp(t, c, beta.Id, day(h, -1), "ring Beta", map[string]any{"dueOn": day(h, 5), "assigneeUserId": caller})
	createWithFollowUp(t, c, beta.Id, day(h, -1), "not mine", map[string]any{"dueOn": day(h, 1), "assigneeUserId": other})
	createWithFollowUp(t, c, beta.Id, day(h, -1), "nobody's", map[string]any{"dueOn": day(h, 1)})

	list := listFollowUps(t, c, nil)
	if len(list.Data) != 2 {
		t.Fatalf("data = %+v, want 2 rows (mine, open)", list.Data)
	}
	if list.Data[0].EntryId != late.Id || list.Data[1].EntryId != soon.Id {
		t.Errorf("order = %d, %d, want %d, %d (dueOn ascending)", list.Data[0].EntryId, list.Data[1].EntryId, late.Id, soon.Id)
	}
	row := list.Data[0]
	if row.CustomerId != alpha.Id || row.CustomerName != "Alpha Co" || row.EventType != "note" {
		t.Errorf("row = %+v, want Alpha Co's note", row)
	}
	if row.Note == nil || *row.Note != "ring Alpha" {
		t.Errorf("note = %v, want \"ring Alpha\"", row.Note)
	}
	if row.OccurredOn != day(h, -9) {
		t.Errorf("occurredOn = %q, want %q", row.OccurredOn, day(h, -9))
	}
	if row.FollowUp.Assignee == nil || row.FollowUp.Assignee.DisplayName != "Kari Nordmann" {
		t.Errorf("assignee = %+v, want Kari Nordmann", row.FollowUp.Assignee)
	}
	if list.Pagination.TotalCount != 2 || list.Pagination.Page != 1 || list.Pagination.PageSize != 25 {
		t.Errorf("pagination = %+v, want page 1, pageSize 25, totalCount 2", list.Pagination)
	}
}

// TestGetFollowUps_EveryFilterAndThePageBoundary walks design D3's filters one
// at a time, plus the archived rule and the note's 200-unit cut.
func TestGetFollowUps_EveryFilterAndThePageBoundary(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, caller := authenticatedClientWithID(t, h)
	other := seedNamedUser(t, h, "Somebody Elsesen")
	alpha := createCustomer(t, c, "Alpha Co")
	beta := createCustomer(t, c, "Beta Co")
	archived := createCustomer(t, c, "Archived Co")

	overdue := createWithFollowUp(t, c, alpha.Id, day(h, -9), "overdue", map[string]any{"dueOn": day(h, -2), "assigneeUserId": caller})
	future := createWithFollowUp(t, c, alpha.Id, day(h, -1), "future", map[string]any{"dueOn": day(h, 5), "assigneeUserId": caller})
	unassigned := createWithFollowUp(t, c, beta.Id, day(h, -1), "nobody's", map[string]any{"dueOn": day(h, 1)})
	theirs := createWithFollowUp(t, c, beta.Id, day(h, -1), "theirs", map[string]any{"dueOn": day(h, 1), "assigneeUserId": other})
	done := createWithFollowUp(t, c, alpha.Id, day(h, -1), "done", map[string]any{"dueOn": day(h, -4), "assigneeUserId": caller})
	if r := followUpDone(t, c, alpha.Id, done.Id, true); r.Status != http.StatusOK {
		t.Fatalf("mark done: status %d body %s, want 200", r.Status, r.Body)
	}
	archivedOpen := createWithFollowUp(t, c, archived.Id, day(h, -1), "archived open", map[string]any{"dueOn": day(h, 1), "assigneeUserId": caller})
	archivedDone := createWithFollowUp(t, c, archived.Id, day(h, -1), "archived done", map[string]any{"dueOn": day(h, 1), "assigneeUserId": caller})
	if r := followUpDone(t, c, archived.Id, archivedDone.Id, true); r.Status != http.StatusOK {
		t.Fatalf("mark archived done: status %d body %s, want 200", r.Status, r.Body)
	}
	if r := c.Do(http.MethodDelete, fmt.Sprintf("/api/v1/customers/%d", archived.Id), nil); r.Status != http.StatusNoContent {
		t.Fatalf("archive customer: status %d body %s, want 204", r.Status, r.Body)
	}

	ids := func(list followUpListJSON) []int32 {
		out := make([]int32, 0, len(list.Data))
		for _, row := range list.Data {
			out = append(out, row.EntryId)
		}
		return out
	}
	equal := func(got, want []int32) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range got {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}

	cases := []struct {
		name  string
		query url.Values
		want  []int32
	}{
		{"state=overdue is a subset of open", url.Values{"state": {"overdue"}}, []int32{overdue.Id}},
		{"state=done, where an archived customer's follow-up survives", url.Values{"state": {"done"}}, []int32{done.Id, archivedDone.Id}},
		{"state=all, where an archived customer's OPEN one does not", url.Values{"state": {"all"}}, []int32{done.Id, overdue.Id, future.Id}},
		{"assignee=none", url.Values{"assignee": {"none"}}, []int32{unassigned.Id}},
		{"assignee=<uuid>", url.Values{"assignee": {other.String()}}, []int32{theirs.Id}},
		{"customerId narrows to one customer", url.Values{"customerId": {fmt.Sprint(alpha.Id)}}, []int32{overdue.Id, future.Id}},
		{"customerId and state together", url.Values{"customerId": {fmt.Sprint(alpha.Id)}, "state": {"done"}}, []int32{done.Id}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ids(listFollowUps(t, c, tc.query)); !equal(got, tc.want) {
				t.Errorf("entry ids = %v, want %v", got, tc.want)
			}
		})
	}
	// archivedOpen is created and never expected anywhere: state=open and
	// state=all both exclude it, which the two cases above assert by its
	// absence.
	_ = archivedOpen

	// Paging: one row per page, and the metadata that lets a control render.
	first := listFollowUps(t, c, url.Values{"state": {"all"}, "pageSize": {"1"}})
	if len(first.Data) != 1 || first.Pagination.TotalCount != 3 || first.Pagination.TotalPages != 3 {
		t.Errorf("page 1 = %+v / %+v, want 1 row of 3 across 3 pages", first.Data, first.Pagination)
	}
	second := listFollowUps(t, c, url.Values{"state": {"all"}, "pageSize": {"1"}, "page": {"2"}})
	if len(second.Data) != 1 || second.Data[0].EntryId == first.Data[0].EntryId {
		t.Errorf("page 2 = %+v, want a different single row from page 1 (%d)", second.Data, first.Data[0].EntryId)
	}
}

// TestGetFollowUps_CutsTheNoteAtTwoHundredUTF16Units is design D3's "first 200
// UTF-16 units", in the unit the design names — the same unit the entry's own
// 500-character summary is cut in, which a byte or rune count would get wrong
// for exactly the input below.
func TestGetFollowUps_CutsTheNoteAtTwoHundredUTF16Units(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c, caller := authenticatedClientWithID(t, h)
	customer := createCustomer(t, c, "Long Note Co")
	// 150 astral characters: 150 runes, 300 UTF-16 code units, 600 bytes. A cut
	// at 200 units keeps 100 of them.
	note := strings.Repeat("𝄞", 150)
	createWithFollowUp(t, c, customer.Id, day(h, 0), note, map[string]any{"dueOn": day(h, 1), "assigneeUserId": caller})

	list := listFollowUps(t, c, nil)
	if len(list.Data) != 1 || list.Data[0].Note == nil {
		t.Fatalf("data = %+v, want one row with a note", list.Data)
	}
	if got := utf16.Encode([]rune(*list.Data[0].Note)); len(got) != 200 {
		t.Errorf("note length = %d UTF-16 units, want 200", len(got))
	}
	if want := strings.Repeat("𝄞", 100); *list.Data[0].Note != want {
		t.Errorf("note = %q, want the first 100 characters", *list.Data[0].Note)
	}
}

// TestGetFollowUps_RefusesAnUnusableQuery pins the parameter messages, which
// are query-parameter messages and therefore carry a trailing period and are
// joined into one detail — the list endpoint's own shape, not a field map.
func TestGetFollowUps_RefusesAnUnusableQuery(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	c := authenticatedClient(t, h)

	cases := []struct {
		query string
		want  string
	}{
		{"page=0", "'page' must be 1 or greater, but was 0."},
		{"pageSize=101", "'pageSize' must be between 1 and 100, but was 101."},
		{"assignee=Me", "'assignee' must be a user id, 'me' or 'none', but was 'Me'."},
		{"state=OPEN", "'state' must be one of 'open', 'overdue', 'done' or 'all', but was 'OPEN'."},
		{"customerId=0", "'customerId' must be 1 or greater, but was 0."},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			r := c.Do(http.MethodGet, "/api/v1/customers/follow-ups?"+tc.query, nil)
			if r.Status != http.StatusBadRequest {
				t.Fatalf("status %d body %s, want 400", r.Status, r.Body)
			}
			var problem problemDetailsJSON
			r.JSON(&problem)
			if problem.Detail == nil || *problem.Detail != tc.want {
				t.Errorf("detail = %v, want %q", problem.Detail, tc.want)
			}
		})
	}
}

// TestGetFollowUps_NeedsBothDoors pins the access pair: the rows are timeline
// data and each names a customer, so customers:timeline-view alone is not
// enough and neither is customers:view.
func TestGetFollowUps_NeedsBothDoors(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	for _, permissions := range [][]string{{"customers:view"}, {"customers:timeline-view"}} {
		c := h.SignIn(t, permissions...)
		if r := c.Do(http.MethodGet, "/api/v1/customers/follow-ups", nil); r.Status != http.StatusForbidden {
			t.Errorf("%v: status %d body %s, want 403", permissions, r.Status, r.Body)
		}
	}
	both := h.SignIn(t, "customers:view", "customers:timeline-view")
	if r := both.Do(http.MethodGet, "/api/v1/customers/follow-ups", nil); r.Status != http.StatusOK {
		t.Errorf("both: status %d body %s, want 200", r.Status, r.Body)
	}
}
```
Add `"strings"` and `"unicode/utf16"` to the file's imports for the UTF-16 test.

- [ ] **Step 3: Run them and watch them fail**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- go test -count=1 -run 'FollowUp' ./internal/customers/
```
Expected: FAIL throughout, and each failure informative rather than a build error — the stubs make the three new paths answer 500, so `TestGetFollowUps_*` report `status 500 … want 200/400/403`, `TestFollowUpDone_*` report `status 500 … want 200`, and every create/update/revision/attention case reports `followUp is absent, want one` or `want one with id "followUpOverdue/…"`. If instead the package fails to build, the cause is a helper name this plan guessed wrong (`validationProblemJSON`, `problemDetailsJSON`, `problemTitle`, `getAttention`, `attentionItemJSON`, `createCustomer`, `createContact`) — each already exists in this package's tests; `grep -n` for it and use the real one.

- [ ] **Step 4: Write `follow_ups.go`**

Replace the stub file entirely:

```go
package customers

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/vantigo-io/vantigo/server/internal/apicommon"
	"github.com/vantigo-io/vantigo/server/internal/contracts"
	"github.com/vantigo-io/vantigo/server/internal/customers/gen"
	"github.com/vantigo-io/vantigo/server/internal/customers/store"
	"github.com/vantigo-io/vantigo/server/internal/db"
)

// This file is phase 4 delivery C (follow-ups design D1, D2, D3): the
// follow-up a manual timeline entry can carry, the two paths that tick and
// untick it, the two attention types it feeds, and GET /customers/follow-ups.
//
// A follow-up lives on the entry, which is the decision everything here
// follows from. It shares the entry's revision, so setting and replacing it is
// the timeline's existing PUT with three more columns and no new concurrency
// story. Ticking it, though, is deliberately NOT that PUT: a tick comes from a
// list and must not lose a race with somebody editing the note, so the two
// done paths take no expectedRevision at all and lean on the guarded UPDATE's
// own WHERE instead (queries/follow_ups.sql).
//
// The assignee's VALUE belongs to another module — identity's users, reachable
// only through contracts.UserDirectory — with the two consequences owner.go
// spells out and neither of which is optional: every directory call happens
// outside a transaction, and a stored id can outlive the account it names.

// followUpAssigneeDisabled is the assignee's counterpart to ownerDisabled
// (owner.go), worded for what the field is for. ownerNotFound is shared as-is:
// "User %s does not exist" is a fact about the directory, not about what the
// caller wanted the user for, and two literals of it would drift.
func followUpAssigneeDisabled(id uuid.UUID) string {
	return fmt.Sprintf("User %s is disabled and cannot be given a follow-up", id)
}

// parsedFollowUp is a validated followUp: the due date as a UTC calendar date,
// and the assignee id if one was named. nil, wherever it appears, means "this
// entry carries no follow-up" — which on a PUT means "clear it".
type parsedFollowUp struct {
	DueOn      time.Time
	AssigneeID *uuid.UUID
}

// validateFollowUp is validateManualTimelineRequest's follow-up half (design
// D1), collecting into the same errs map so a request with a bad note AND a bad
// due date hears about both — the module's all-errors-at-once shape.
//
// dueOn goes through parseISODate, the same strict yyyy-MM-dd every other date
// in this module is read with, and is deliberately NOT compared against today:
// a follow-up in the future is the normal case and the whole point, which is
// exactly where it differs from occurredOn.
//
// The assignee is only parsed here, never checked for existence: that is a
// directory call, and a directory call belongs in the handler, before any
// transaction opens (actor.go's rule). ok is returned separately from errs
// because errs is shared with the rest of the request: a request whose note is
// blank but whose follow-up is fine must still produce a usable follow-up for
// the handler to ignore.
func validateFollowUp(body *gen.TimelineFollowUpRequest, errs map[string][]string) *parsedFollowUp {
	if body == nil {
		return nil
	}
	out := parsedFollowUp{}
	ok := true
	due, err := parseISODate(deref(body.DueOn))
	if err != nil {
		errs["followUp.dueOn"] = []string{"FollowUp.dueOn must be an ISO date (yyyy-MM-dd)"}
		ok = false
	} else {
		out.DueOn = due
	}
	if body.AssigneeUserId != nil {
		id := *body.AssigneeUserId
		out.AssigneeID = &id
	}
	if !ok {
		return nil
	}
	return &out
}

// resolveFollowUpAssignee is the assignee's existence and eligibility check
// (design D1), made against the directory BEFORE the caller opens a
// transaction. It answers field errors to report, or nil when there is nothing
// to say — including when there is no follow-up or no assignee at all.
//
// An assignee disabled AFTER being given a follow-up keeps it (design D1), so
// this only ever runs on a follow-up a request is setting now: the same
// division PutCustomersByIdOwner makes between validating a CHANGE and
// re-rendering stored state.
func (s *server) resolveFollowUpAssignee(ctx context.Context, fu *parsedFollowUp) (map[string][]string, error) {
	if fu == nil || fu.AssigneeID == nil {
		return nil, nil
	}
	user, err := s.deps.Users.User(ctx, *fu.AssigneeID)
	if err != nil {
		return nil, fmt.Errorf("customers: resolve follow-up assignee: %w", err)
	}
	switch {
	case user == nil:
		return map[string][]string{"followUp.assigneeUserId": {ownerNotFound(*fu.AssigneeID)}}, nil
	case !user.Active:
		return map[string][]string{"followUp.assigneeUserId": {followUpAssigneeDisabled(*fu.AssigneeID)}}, nil
	}
	return nil, nil
}

// followUpDateParam and followUpAssigneeParam are one parsedFollowUp as the two
// columns a write sets. nil answers the zero pgtype.Date and a nil pointer,
// which is what NULL in both columns means: no follow-up.
func followUpDateParam(fu *parsedFollowUp) pgtype.Date {
	if fu == nil {
		return pgtype.Date{}
	}
	return pgtype.Date{Time: fu.DueOn, Valid: true}
}

func followUpAssigneeParam(fu *parsedFollowUp) *uuid.UUID {
	if fu == nil {
		return nil
	}
	return fu.AssigneeID
}

// followUpDecoration is the assignee display names one response needs, borrowed
// from identity's directory for the length of that response (design D1) — the
// timeline's counterpart to owner.go's customerDecoration, and the same shape
// for the same reason: one directory call per response, never one per row.
type followUpDecoration struct {
	assignees map[uuid.UUID]contracts.UserEntry
}

// decorateFollowUpAssignees resolves ids in one directory call. It must be
// called with no transaction open: the call inside is out-of-process, and an
// out-of-process call under a lock is how an outage becomes a database incident
// (owner.go's decorate says the same thing at more length).
//
// An empty ids makes no call at all — an entry with no follow-up, or a page of
// unassigned ones, is an ordinary answer, and asking the directory about nobody
// is a round trip for a map that will stay empty.
func (s *server) decorateFollowUpAssignees(ctx context.Context, ids []uuid.UUID) (followUpDecoration, error) {
	dec := followUpDecoration{assignees: map[uuid.UUID]contracts.UserEntry{}}
	distinct := make([]uuid.UUID, 0, len(ids))
	seen := make(map[uuid.UUID]bool, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		distinct = append(distinct, id)
	}
	if len(distinct) == 0 {
		return dec, nil
	}
	users, err := s.deps.Users.Users(ctx, distinct)
	if err != nil {
		// A directory that cannot be reached is a 500, not a page of follow-ups
		// with their assignees quietly missing: "unassigned" is a claim, and
		// design D2 makes it a load-bearing one — an unassigned follow-up is
		// everyone's.
		return followUpDecoration{}, fmt.Errorf("customers: resolve follow-up assignees: %w", err)
	}
	for _, u := range users {
		dec.assignees[u.ID] = u
	}
	return dec, nil
}

// entryAssigneeIDs and revisionAssigneeIDs are the assignee ids of a set of
// rows, for decorateFollowUpAssignees. Duplicates are fine — it de-duplicates
// itself — and a row with no follow-up or no assignee contributes nothing.
func entryAssigneeIDs(entries ...store.CustomersCustomersTimelineEntry) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(entries))
	for _, e := range entries {
		if e.FollowUpAssigneeUserID != nil {
			ids = append(ids, *e.FollowUpAssigneeUserID)
		}
	}
	return ids
}

func revisionAssigneeIDs(rows []store.CustomersCustomersTimelineEntriesRevision) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		if r.FollowUpAssigneeUserID != nil {
			ids = append(ids, *r.FollowUpAssigneeUserID)
		}
	}
	return ids
}

// assignee is one follow-up's assignee as the contract reports it, or nil when
// it is unassigned. An id the directory answered nothing for is still an
// assignee — reported as unknownUserDisplay and inactive, the actorFor and
// owner precedent — because contracts.UserDirectory's absence means "no such
// account", not "the lookup failed", and design D1 keeps the follow-up either
// way.
func (d followUpDecoration) assignee(id *uuid.UUID) *gen.TimelineFollowUpAssignee {
	if id == nil {
		return nil
	}
	if u, ok := d.assignees[*id]; ok {
		return &gen.TimelineFollowUpAssignee{UserId: u.ID, DisplayName: u.DisplayName, Active: u.Active}
	}
	return &gen.TimelineFollowUpAssignee{UserId: *id, DisplayName: unknownUserDisplay, Active: false}
}

// followUpResponse is the one projection every shape that answers a follow-up
// goes through — the entry, a revision of it, and a row of the Follow-ups list
// — which is why it takes three columns rather than a row type. An invalid
// (NULL) date is the whole answer: no date, no follow-up.
func followUpResponse(on pgtype.Date, assigneeID *uuid.UUID, doneAt *time.Time, dec followUpDecoration) *gen.TimelineFollowUp {
	if !on.Valid {
		return nil
	}
	return &gen.TimelineFollowUp{
		DueOn:    openapi_types.Date{Time: on.Time},
		Assignee: dec.assignee(assigneeID),
		DoneAt:   doneAt,
	}
}
```

- [ ] **Step 5: The two done handlers, in the same file**

```go
// followUpOutcome is what markFollowUp answers: exactly one of the three is
// set. It exists so the two handlers below are six lines each instead of two
// copies of the same forty — the paths differ only in which state they are
// heading for.
type followUpOutcome struct {
	Entry   *gen.TimelineResponse
	Problem *apicommon.ProblemDetails
	Missing bool
}

// markFollowUp is POST and DELETE .../follow-up/done, both of them (design D1).
//
// The ordering, and why each step is where it is:
//  1. the entry, in any state — a generated or deleted one has to be seen to
//     answer its distinct 409 rather than a 404;
//  2. manual and active, or the timeline's own "Timeline entry is immutable";
//  3. a follow-up at all, or 404 — the thing the caller addressed
//     (.../follow-up/done) genuinely does not exist, which is what 404 says,
//     and it is a different answer from "the entry cannot be edited";
//  4. ALREADY in the asked-for state: 200 with the entry and no write at all.
//     That is what idempotent means here, and it is also why no actor is
//     resolved on that path (actor.go: only when a write will happen) and why
//     no second revision row appears (customers foundation design D5's no-op
//     rule);
//  5. the actor, before the transaction opens;
//  6. the guarded write and its revision row, in one transaction.
//
// There is no expectedRevision anywhere in it. What takes its place is the
// UPDATE's own `follow_up_done_at IS NULL` (or IS NOT NULL): two concurrent
// ticks serialize on the row and the loser matches no row, which this function
// answers by re-reading and reporting what is now true. Answering 409 there
// would be telling a caller that a follow-up they can see is done is not done.
func (s *server) markFollowUp(ctx context.Context, customerID, entryID int32, done bool) (followUpOutcome, error) {
	q := store.New(s.deps.Pool)
	entry, err := q.GetTimelineEntry(ctx, store.GetTimelineEntryParams{ID: entryID, CustomerID: customerID})
	if errors.Is(err, pgx.ErrNoRows) {
		return followUpOutcome{Missing: true}, nil
	}
	if err != nil {
		return followUpOutcome{}, fmt.Errorf("customers: get timeline entry: %w", err)
	}

	if entry.Provenance != "manual" || entry.State != "active" {
		problem := timelineProblem(timelineImmutableTitle, "Generated, deleted, or voided timeline entries cannot be edited.")
		return followUpOutcome{Problem: &problem}, nil
	}
	if !entry.FollowUpOn.Valid {
		return followUpOutcome{Missing: true}, nil
	}
	if (entry.FollowUpDoneAt != nil) == done {
		return s.followUpAnswer(ctx, entry)
	}

	act, err := s.actorFor(ctx, manualFallbackActor)
	if err != nil {
		return followUpOutcome{}, fmt.Errorf("customers: resolve actor: %w", err)
	}

	now := s.deps.Clock()
	var written store.CustomersCustomersTimelineEntry
	err = db.WithTx(ctx, s.deps.Pool, pgx.TxOptions{}, func(tx pgx.Tx) error {
		txq := store.New(tx)
		var err error
		if done {
			written, err = txq.SetTimelineEntryFollowUpDone(ctx, store.SetTimelineEntryFollowUpDoneParams{
				ID: entryID, CustomerID: customerID, Now: now,
			})
		} else {
			written, err = txq.ClearTimelineEntryFollowUpDone(ctx, store.ClearTimelineEntryFollowUpDoneParams{
				ID: entryID, CustomerID: customerID, Now: now,
			})
		}
		if err != nil {
			return err
		}
		return insertTimelineRevisionFromEntry(ctx, txq, written, act)
	})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		fresh, ferr := q.GetTimelineEntry(ctx, store.GetTimelineEntryParams{ID: entryID, CustomerID: customerID})
		if errors.Is(ferr, pgx.ErrNoRows) {
			return followUpOutcome{Missing: true}, nil
		}
		if ferr != nil {
			return followUpOutcome{}, fmt.Errorf("customers: re-read timeline entry after conflict: %w", ferr)
		}
		// A concurrent writer reached this state first — or deleted the entry,
		// or cleared its follow-up, in which case what is now true is a 409 or
		// a 404 and the checks above say so on the fresh row.
		switch {
		case fresh.Provenance != "manual" || fresh.State != "active":
			problem := timelineProblem(timelineImmutableTitle, "Generated, deleted, or voided timeline entries cannot be edited.")
			return followUpOutcome{Problem: &problem}, nil
		case !fresh.FollowUpOn.Valid:
			return followUpOutcome{Missing: true}, nil
		}
		return s.followUpAnswer(ctx, fresh)
	case db.IsUniqueViolation(err, timelineRevisionUniqueConstraint):
		// The backstop, unreachable in practice: whichever writer loses the
		// guarded UPDATE never reaches the revision insert at all. Answered as
		// the timeline's own revision conflict, the same as the PUT's.
		problem := timelineProblem(timelineRevisionConflictTitle, "The timeline entry revision was concurrently changed.")
		return followUpOutcome{Problem: &problem}, nil
	case err != nil:
		return followUpOutcome{}, fmt.Errorf("customers: mark timeline follow-up: %w", err)
	}
	return s.followUpAnswer(ctx, written)
}

// followUpAnswer decorates one entry and wraps it as the 200 both done paths
// answer. The directory call inside happens after the transaction has
// committed, never within one.
func (s *server) followUpAnswer(ctx context.Context, e store.CustomersCustomersTimelineEntry) (followUpOutcome, error) {
	dec, err := s.decorateFollowUpAssignees(ctx, entryAssigneeIDs(e))
	if err != nil {
		return followUpOutcome{}, err
	}
	body := timelineResponse(e, dec)
	return followUpOutcome{Entry: &body}, nil
}

// PostCustomersByIdTimelineByEntryIdFollowUpDone Mark a timeline entry's follow-up done
// (POST /api/v1/customers/{id}/timeline/{entryId}/follow-up/done)
func (s *server) PostCustomersByIdTimelineByEntryIdFollowUpDone(ctx context.Context, req gen.PostCustomersByIdTimelineByEntryIdFollowUpDoneRequestObject) (gen.PostCustomersByIdTimelineByEntryIdFollowUpDoneResponseObject, error) {
	out, err := s.markFollowUp(ctx, req.Id, req.EntryId, true)
	switch {
	case err != nil:
		return nil, err
	case out.Missing:
		return gen.PostCustomersByIdTimelineByEntryIdFollowUpDone404Response{}, nil
	case out.Problem != nil:
		return gen.PostCustomersByIdTimelineByEntryIdFollowUpDone409ApplicationProblemPlusJSONResponse(*out.Problem), nil
	}
	return gen.PostCustomersByIdTimelineByEntryIdFollowUpDone200JSONResponse(*out.Entry), nil
}

// DeleteCustomersByIdTimelineByEntryIdFollowUpDone Reopen a timeline entry's follow-up
// (DELETE /api/v1/customers/{id}/timeline/{entryId}/follow-up/done)
func (s *server) DeleteCustomersByIdTimelineByEntryIdFollowUpDone(ctx context.Context, req gen.DeleteCustomersByIdTimelineByEntryIdFollowUpDoneRequestObject) (gen.DeleteCustomersByIdTimelineByEntryIdFollowUpDoneResponseObject, error) {
	out, err := s.markFollowUp(ctx, req.Id, req.EntryId, false)
	switch {
	case err != nil:
		return nil, err
	case out.Missing:
		return gen.DeleteCustomersByIdTimelineByEntryIdFollowUpDone404Response{}, nil
	case out.Problem != nil:
		return gen.DeleteCustomersByIdTimelineByEntryIdFollowUpDone409ApplicationProblemPlusJSONResponse(*out.Problem), nil
	}
	return gen.DeleteCustomersByIdTimelineByEntryIdFollowUpDone200JSONResponse(*out.Entry), nil
}
```

- [ ] **Step 6: The attention items and the list handler, in the same file**

```go
// The two attention types design D2 defines, computed from state on every call
// like the four registry ones — but unlike them, these depend on WHO asks. That
// is the first time this module's attention list has, and it is why
// GetCustomersStatsAttention now reads the principal.
const (
	attentionFollowUpOverdue = "followUpOverdue"
	attentionFollowUpDue     = "followUpDue"
)

// followUpAttentionItems is the follow-up half of /stats/attention's pure work
// (design D2). The query already narrowed to open follow-ups on non-archived
// customers, assigned to the caller or nobody, due today or earlier; all that
// is left is the one comparison that splits the two types.
//
// occurredAt is the DUE DATE at midnight UTC, not the moment the follow-up was
// written — projects' own rule, which stats.go's attentionOccurredAt already
// states for the registry items: the dashboard sorts by occurredAt and prints
// it as "3 days ago", so an overdue follow-up has to sort by how overdue it is.
//
// entityId is the CUSTOMER id even though the item is about an entry, because
// the host links a customers attention item to /customers/{entityId} and the
// customer's page is where the entry is. The entry id is in the item's own id.
func followUpAttentionItems(rows []store.FollowUpAttentionCandidatesRow, today time.Time) []gen.CustomerStatsAttentionItem {
	items := make([]gen.CustomerStatsAttentionItem, 0, len(rows))
	for _, r := range rows {
		due := civilDate(r.FollowUpOn.Time)
		typ := attentionFollowUpDue
		if due.Before(today) {
			typ = attentionFollowUpOverdue
		}
		items = append(items, gen.CustomerStatsAttentionItem{
			Id:         typ + "/" + strconv.FormatInt(int64(r.EntryID), 10),
			Type:       typ,
			Title:      r.CustomerName,
			OccurredAt: due,
			EntityId:   strconv.FormatInt(int64(r.CustomerID), 10),
		})
	}
	return items
}

// followUpsDefaultPageSize, followUpsMaxPageSize and followUpNoteLength are
// GET /customers/follow-ups' own numbers: the customer list's page size and cap
// (so one control behaves the same everywhere), and design D3's 200 UTF-16
// units of note.
const (
	followUpsDefaultPageSize = 25
	followUpsMaxPageSize     = 100
	followUpNoteLength       = 200
)

// followUpStates is design D3's four values, matched case-sensitively as every
// query parameter in this module is (validateGetCustomersParams says why).
var followUpStates = []string{"open", "overdue", "done", "all"}

// validateGetCustomersFollowUpsParams is validateGetCustomersParams for this
// list: every check runs regardless of the others, and every message is
// collected to be joined with a single space into one ProblemDetails.Detail.
// Query-parameter messages carry a trailing period; body-level ones do not.
//
// assignee is checked for SHAPE here and resolved in the handler: 'me' needs
// the request's principal, which this function deliberately does not see.
func validateGetCustomersFollowUpsParams(p gen.GetCustomersFollowUpsParams) []string {
	var errs []string
	if p.Page != nil && *p.Page < 1 {
		errs = append(errs, fmt.Sprintf("'page' must be 1 or greater, but was %d.", *p.Page))
	}
	if p.PageSize != nil && (*p.PageSize < 1 || *p.PageSize > followUpsMaxPageSize) {
		errs = append(errs, fmt.Sprintf("'pageSize' must be between 1 and %d, but was %d.", followUpsMaxPageSize, *p.PageSize))
	}
	if p.Assignee != nil && !validOwnerFilter(*p.Assignee) {
		errs = append(errs, fmt.Sprintf("'assignee' must be a user id, 'me' or 'none', but was '%s'.", *p.Assignee))
	}
	if p.State != nil {
		known := false
		for _, s := range followUpStates {
			if *p.State == s {
				known = true
				break
			}
		}
		if !known {
			errs = append(errs, fmt.Sprintf("'state' must be one of 'open', 'overdue', 'done' or 'all', but was '%s'.", *p.State))
		}
	}
	if p.CustomerId != nil && *p.CustomerId < 1 {
		errs = append(errs, fmt.Sprintf("'customerId' must be 1 or greater, but was %d.", *p.CustomerId))
	}
	return errs
}

// GetCustomersFollowUps List follow-ups across customers
// (GET /api/v1/customers/follow-ups)
//
// Design D3. Offset paging, not the timeline's keyset cursor: this is a page of
// a list with a page control, the same shape GET /customers answers, and the
// design asks for page/pageSize by name.
//
// 'me' is resolved from the SESSION, never from anything the request says about
// who the caller is — the same rule (and the same unreachable-in-production 400)
// GetCustomers' ownerId=me follows, for the same reason: answering somebody
// else's follow-ups is the one outcome a "what is on my plate" list must never
// have. It is the default, so an unauthenticated caller would hit it without
// asking; the router admits none, which is why that branch is a 400 rather than
// a silent everyone's-follow-ups.
func (s *server) GetCustomersFollowUps(ctx context.Context, req gen.GetCustomersFollowUpsRequestObject) (gen.GetCustomersFollowUpsResponseObject, error) {
	if msgs := validateGetCustomersFollowUpsParams(req.Params); len(msgs) > 0 {
		return gen.GetCustomersFollowUps400ApplicationProblemPlusJSONResponse(
			apicommon.Problem("Invalid query parameters", strings.Join(msgs, " "))), nil
	}

	page := int32(1)
	if req.Params.Page != nil {
		page = *req.Params.Page
	}
	pageSize := int32(followUpsDefaultPageSize)
	if req.Params.PageSize != nil {
		pageSize = *req.Params.PageSize
	}
	state := "open"
	if req.Params.State != nil {
		state = *req.Params.State
	}

	assignee := "me"
	if req.Params.Assignee != nil {
		assignee = *req.Params.Assignee
	}
	var assigneeID *uuid.UUID
	assigneeNone := false
	switch assignee {
	case "none":
		assigneeNone = true
	case "me":
		p, ok := contracts.PrincipalFrom(ctx)
		if !ok || p.UserID == uuid.Nil {
			return gen.GetCustomersFollowUps400ApplicationProblemPlusJSONResponse(apicommon.Problem(
				"Invalid query parameters", "'assignee' cannot be 'me' without a signed-in user.")), nil
		}
		id := p.UserID
		assigneeID = &id
	default:
		// validateGetCustomersFollowUpsParams already refused anything
		// unparseable, so err is impossible here; the guard means an impossible
		// value filters nothing rather than panicking.
		if id, err := uuid.Parse(assignee); err == nil {
			assigneeID = &id
		}
	}

	today := pgtype.Date{Time: civilDate(s.deps.Clock()), Valid: true}
	q := store.New(s.deps.Pool)
	total, err := q.CountCustomerFollowUps(ctx, store.CountCustomerFollowUpsParams{
		FollowUpState: state, Today: today,
		AssigneeNone: assigneeNone, AssigneeID: assigneeID, CustomerID: req.Params.CustomerId,
	})
	if err != nil {
		return nil, fmt.Errorf("customers: count follow-ups: %w", err)
	}
	rows, err := q.ListCustomerFollowUps(ctx, store.ListCustomerFollowUpsParams{
		FollowUpState: state, Today: today,
		AssigneeNone: assigneeNone, AssigneeID: assigneeID, CustomerID: req.Params.CustomerId,
		PageSize: pageSize, RowOffset: (page - 1) * pageSize,
	})
	if err != nil {
		return nil, fmt.Errorf("customers: list follow-ups: %w", err)
	}

	// One directory call for the whole page, never one per row (owner.go's
	// rule), and after the reads rather than between them.
	ids := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		if r.FollowUpAssigneeUserID != nil {
			ids = append(ids, *r.FollowUpAssigneeUserID)
		}
	}
	dec, err := s.decorateFollowUpAssignees(ctx, ids)
	if err != nil {
		return nil, err
	}

	data := make([]gen.CustomerFollowUp, 0, len(rows))
	for _, r := range rows {
		row := gen.CustomerFollowUp{
			EntryId:      r.EntryID,
			CustomerId:   r.CustomerID,
			CustomerName: r.CustomerName,
			EventType:    r.EventType,
			OccurredOn:   openapi_types.Date{Time: r.OccurredOn.Time},
		}
		if r.Note != nil {
			// Cut in UTF-16 code units, the unit design D3 names and the one
			// the entry's own summary is already cut in (truncateUTF16,
			// timeline.go): Postgres' left() counts characters, and the two
			// disagree on every astral character.
			row.Note = apicommon.Ptr(truncateUTF16(*r.Note, followUpNoteLength))
		}
		// followUp is required on this shape — carrying one is what put the row
		// on the list — so a nil here would be a bug in the query, not a row
		// with no follow-up. Dereferencing is the assertion.
		row.FollowUp = *followUpResponse(r.FollowUpOn, r.FollowUpAssigneeUserID, r.FollowUpDoneAt, dec)
		data = append(data, row)
	}

	return gen.GetCustomersFollowUps200JSONResponse{
		Data:       data,
		Pagination: apicommon.Pagination(page, pageSize, int32(total)),
	}, nil
}
```

`gen.CustomerFollowUp.FollowUp`'s Go type will be the bare `TimelineFollowUp` (it is a required, non-nullable `$ref`); if generation made it a pointer instead, drop the `*` from the assignment and delete the comment's last sentence.

- [ ] **Step 7: Thread it through `timeline.go`**

Six edits.

1. `parsedManualTimeline` (lines 102-108) gains the field:
```go
type parsedManualTimeline struct {
	EventType  string
	OccurredOn time.Time
	OccurredAt *time.Time
	Note       string
	SourceURL  *string
	// FollowUp is what happens next (follow-ups design D1), nil when the
	// request carried none — which on a PUT means "clear it", because this
	// request is a full replace.
	FollowUp *parsedFollowUp
}
```
2. `validateManualTimelineRequest` (lines 114-175) validates it and carries it out. Insert before the `if len(errs) > 0` guard:
```go
	followUp := validateFollowUp(body.FollowUp, errs)
```
and extend the success return:
```go
	return parsedManualTimeline{EventType: eventType, OccurredOn: occurredOn, OccurredAt: occurredAt, Note: note, SourceURL: sourceURL, FollowUp: followUp}, nil
```
3. `timelineResponse` (lines 420-440) takes the decoration and answers the follow-up:
```go
// timelineResponse is TimelineResponse.FromDomain (TimelineEndpoints.cs:612-632),
// plus the entry's follow-up (follow-ups design D1). dec is the assignee names
// this response's caller already resolved — in one directory call for a whole
// page, never one per entry — so this function makes no call of its own and can
// be used inside a loop.
func timelineResponse(e store.CustomersCustomersTimelineEntry, dec followUpDecoration) gen.TimelineResponse {
	return gen.TimelineResponse{
		Id:              e.ID,
		EventType:       e.EventType,
		Provenance:      e.Provenance,
		Producer:        e.Producer,
		OccurredOn:      openapi_types.Date{Time: e.OccurredOn.Time},
		OccurredAt:      e.OccurredAt,
		Summary:         apicommon.Ptr(e.Summary),
		Note:            e.Note,
		SourceUrl:       e.SourceUrl,
		Payload:         payloadElement(e.PayloadJson),
		CurrentRevision: e.CurrentRevision,
		State:           e.State,
		ActorKind:       e.ActorKind,
		ActorDisplay:    apicommon.Ptr(e.ActorDisplay),
		CreatedAt:       e.CreatedAt,
		UpdatedAt:       e.UpdatedAt,
		FollowUp:        followUpResponse(e.FollowUpOn, e.FollowUpAssigneeUserID, e.FollowUpDoneAt, dec),
	}
}
```
4. `timelineRevisionResponse` (lines 446-476) takes it too, and adds `FollowUp: followUpResponse(r.FollowUpOn, r.FollowUpAssigneeUserID, r.FollowUpDoneAt, dec)` to the returned literal, with its signature becoming `func timelineRevisionResponse(r store.CustomersCustomersTimelineEntriesRevision, dec followUpDecoration) gen.TimelineRevisionResponse` and its doc comment gaining:
```go
// The follow-up is the one it carried AT THIS REVISION (design D1): history
// stays point-in-time, so a revision taken before somebody moved the date still
// says what it said. The assignee's NAME is resolved now, though, not
// snapshotted — the same thing the entry's own response does, and the reason
// actorDisplay above IS snapshotted is that it is the only record of who acted.
```
5. `insertTimelineRevisionFromEntry` (lines 501-525) carries the three columns into the snapshot — add to the `InsertTimelineRevisionParams` literal, after `DeletedAt`:
```go
		FollowUpOn:             e.FollowUpOn,
		FollowUpAssigneeUserID: e.FollowUpAssigneeUserID,
		FollowUpDoneAt:         e.FollowUpDoneAt,
```
6. The five call sites. `GetCustomersByIdTimeline` (lines 621-625) decorates the page once:
```go
	dec, err := s.decorateFollowUpAssignees(ctx, entryAssigneeIDs(rows...))
	if err != nil {
		return nil, err
	}
	data := make([]gen.TimelineResponse, 0, len(rows))
	for _, r := range rows {
		data = append(data, timelineResponse(r, dec))
	}
```
`PostCustomersByIdTimeline` (lines 651-699) checks the assignee before the actor, passes the two columns, and decorates the answer:
```go
	// The assignee is checked against the directory here — before any database
	// access, let alone a transaction (actor.go's rule, and the same place
	// PutCustomersByIdOwner checks its own candidate).
	if fieldErrs, err := s.resolveFollowUpAssignee(ctx, parsed.FollowUp); err != nil {
		return nil, err
	} else if fieldErrs != nil {
		return gen.PostCustomersByIdTimeline400ApplicationProblemPlusJSONResponse(
			apicommon.ValidationProblem("Invalid timeline entry", fieldErrs)), nil
	}
```
goes immediately after the `validateManualTimelineRequest` guard; then the insert params gain
```go
		FollowUpOn:             followUpDateParam(parsed.FollowUp),
		FollowUpAssigneeUserID: followUpAssigneeParam(parsed.FollowUp),
```
and the return becomes
```go
	entry := fromInsertManualRow(created)
	dec, err := s.decorateFollowUpAssignees(ctx, entryAssigneeIDs(entry))
	if err != nil {
		return nil, err
	}
	return createdTimelineResponse{
		body:     timelineResponse(entry, dec),
		location: fmt.Sprintf("%s/api/v1/customers/%d/timeline/%d", s.deps.Config.BasePath, req.Id, created.ID),
	}, nil
```
`GetCustomersByIdTimelineByEntryId` (lines 707-717):
```go
	dec, err := s.decorateFollowUpAssignees(ctx, entryAssigneeIDs(entry))
	if err != nil {
		return nil, err
	}
	return gen.GetCustomersByIdTimelineByEntryId200JSONResponse(timelineResponse(entry, dec)), nil
```
`PutCustomersByIdTimelineByEntryId` (lines 739-819): the same assignee check, inserted after the 409 checks and immediately before `s.actorFor` — after, because a request aimed at an immutable entry or a stale revision must hear that rather than a field error about a user it will never write; the update params gain
```go
			FollowUpOn:             followUpDateParam(parsed.FollowUp),
			FollowUpAssigneeUserID: followUpAssigneeParam(parsed.FollowUp),
```
and the return becomes the decorated one, in the same shape as the create's.
`GetCustomersByIdTimelineByEntryIdRevisions` (lines 898-915):
```go
	dec, err := s.decorateFollowUpAssignees(ctx, revisionAssigneeIDs(rows))
	if err != nil {
		return nil, err
	}
	data := make([]gen.TimelineRevisionResponse, 0, len(rows))
	for _, r := range rows {
		data = append(data, timelineRevisionResponse(r, dec))
	}
```

- [ ] **Step 8: The two types on `/stats/attention`**

In `apps/server/internal/customers/stats.go`, `attentionItemsFrom`'s sort moves into a shared function so the merged list can use the same one (its own behaviour is unchanged, which is what keeps `stats_internal_test.go` green):

```go
// sortAttentionItems is the order every item on this endpoint answers in,
// whatever produced it: newest occurredAt first, ties broken by id — the
// dashboard reads a list, not a timeline, so "what needs looking at soonest"
// floats and a stable tiebreaker keeps repeated calls from reshuffling ties.
func sortAttentionItems(items []gen.CustomerStatsAttentionItem) {
	slices.SortStableFunc(items, func(a, b gen.CustomerStatsAttentionItem) int {
		if c := b.OccurredAt.Compare(a.OccurredAt); c != 0 {
			return c
		}
		return strings.Compare(a.Id, b.Id)
	})
}
```
and `attentionItemsFrom`'s tail becomes `sortAttentionItems(items); return items`.

Then the handler merges both halves and reads the caller:

```go
// GetCustomersStatsAttention Get customer dashboard attention items
// (GET /api/v1/customers/stats/attention)
//
// Two families of item, both computed from state each time this is asked — no
// stored list, no dismiss action: the four registry ones (Brreg in full design
// D4), and the two follow-up ones (follow-ups design D2).
//
// The follow-up half is the first item here that depends on WHO is asking: it
// reports open follow-ups assigned to the caller or unassigned, so the endpoint
// now reads the principal from the context the way time's and expenses' own
// items do. An unauthenticated caller cannot reach this (the router refuses
// first), and if one somehow did, a nil caller answers only the unassigned
// follow-ups — never everybody's.
func (s *server) GetCustomersStatsAttention(ctx context.Context, _ gen.GetCustomersStatsAttentionRequestObject) (gen.GetCustomersStatsAttentionResponseObject, error) {
	q := store.New(s.deps.Pool)
	registryRows, err := q.RegistryAttentionCandidates(ctx)
	if err != nil {
		return nil, fmt.Errorf("customers: stats attention: %w", err)
	}

	var caller *uuid.UUID
	if p, ok := contracts.PrincipalFrom(ctx); ok && p.UserID != uuid.Nil {
		id := p.UserID
		caller = &id
	}
	today := civilDate(s.deps.Clock())
	followUpRows, err := q.FollowUpAttentionCandidates(ctx, store.FollowUpAttentionCandidatesParams{
		Today: pgtype.Date{Time: today, Valid: true}, CallerID: caller,
	})
	if err != nil {
		return nil, fmt.Errorf("customers: stats attention follow-ups: %w", err)
	}

	items := append(attentionItemsFrom(registryRows), followUpAttentionItems(followUpRows, today)...)
	sortAttentionItems(items)
	return gen.GetCustomersStatsAttention200JSONResponse(items), nil
}
```
`stats.go` gains `github.com/google/uuid` and `github.com/vantigo-io/vantigo/server/internal/contracts` to its imports.

- [ ] **Step 9: The whole suite**

No existing test file changes. `harness_test.go`'s fixtures need nothing (`follow_ups_test.go` brings its own), and `timeline_test.go`'s `timelineEntryJSON` does not declare `followUp`, which `encoding/json` simply ignores — the follow-up's coverage is `follow_ups_test.go`, entirely.

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- gofmt -l internal/customers
mise exec -- go vet ./... && mise exec -- go test -count=1 ./internal/customers/... ./internal/db/... ./internal/openapi/... ./internal/module/...
```
Expected: PASS, coverage gate included — the three new operations are each exercised by a 200 above (`TestFollowUpDone_IsIdempotentAndEachRealChangeIsARevision` for both done paths, `TestGetFollowUps_DefaultsToMyOpenOnes` for the list).

If `TestGetStatsAttention_*` in `stats_test.go` now fails on a count, read it: those tests create customers, and creating a customer writes a generated entry, not a follow-up — so no follow-up item can appear in them. A failure there means the follow-up query is not filtering on `follow_up_on IS NOT NULL`.

- [ ] **Step 10: The concurrency test the revision guard needs**

Create `apps/server/internal/customers/follow_ups_concurrency_test.go`:

```go
package customers_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/vantigo-io/vantigo/server/internal/modtest"
)

// TestFollowUpDone_ConcurrentTicks_BothSucceedAndOnlyOneRevisionIsWritten is
// the guard that replaces expectedRevision on the two done paths (follow-ups
// design D1). Two ticks of the same follow-up are forced to genuinely overlap
// at the database — a gate transaction holds the entry row before either
// request starts, so both requests' Go-side "already done?" check sees the same
// still-open row and neither is short-circuited by it — and only the guarded
// UPDATE's own `follow_up_done_at IS NULL` can decide.
//
// What the assertions pin, and what each catches:
//   - both answer 200. A tick that lost a race has still achieved what it asked
//     for; answering 409 (or 404) there would be telling the caller a follow-up
//     they can see is done is not done.
//   - exactly ONE new revision row. Dropping `follow_up_done_at IS NULL` from
//     the UPDATE's WHERE would let both write, producing two revision rows and
//     current_revision 3 — or, more likely, a 23505 on
//     ux_customers_timeline_entries_revisions_entry_revision as both claim
//     revision 2, which is the backstop firing where the guard should have.
//   - current_revision is exactly 2.
//
// race and awaitLockWaiters are contacts_concurrency_test.go's, already shared
// by timeline_concurrency_test.go in this package.
func TestFollowUpDone_ConcurrentTicks_BothSucceedAndOnlyOneRevisionIsWritten(t *testing.T) {
	h := newHarness(t)
	c := authenticatedClient(t, h)
	customer := createCustomer(t, c, "Tick Race Co")
	entry := createWithFollowUp(t, c, customer.Id, day(h, 0), "ring back", map[string]any{"dueOn": day(h, 1)})

	ctx := context.Background()
	gate, err := h.Pool().Begin(ctx)
	if err != nil {
		t.Fatalf("gate: begin: %v", err)
	}
	t.Cleanup(func() { _ = gate.Rollback(ctx) })
	if _, err := gate.Exec(ctx, `SELECT 1 FROM customers.customers_timeline_entries WHERE id = $1 FOR UPDATE`, entry.Id); err != nil {
		t.Fatalf("gate: lock the entry row: %v", err)
	}

	tick := func() *modtest.Response {
		return c.Do(http.MethodPost, fmt.Sprintf("/api/v1/customers/%d/timeline/%d/follow-up/done", customer.Id, entry.Id), nil)
	}
	done := make(chan []*modtest.Response, 1)
	finished := make(chan struct{})
	go func() {
		done <- race(tick, tick)
		close(finished)
	}()
	awaitLockWaiters(t, h, 2, finished)
	if err := gate.Commit(ctx); err != nil {
		t.Fatalf("gate: release: %v", err)
	}
	responses := <-done

	for i, r := range responses {
		if r.Status != http.StatusOK {
			t.Errorf("tick %d: status %d body %s, want 200 (a tick that lost the race still got what it asked for)", i, r.Status, r.Body)
		}
	}
	if n := h.Count(t, `SELECT count(*) FROM customers.customers_timeline_entries_revisions WHERE customer_timeline_entry_id = $1`, entry.Id); n != 2 {
		t.Errorf("revision rows = %d, want 2 (the create's, and exactly one tick's)", n)
	}
	if rev := h.Count(t, `SELECT current_revision FROM customers.customers_timeline_entries WHERE id = $1`, entry.Id); rev != 2 {
		t.Errorf("current_revision = %d, want 2 (bumped exactly once)", rev)
	}
}
```

`race(fns ...func() *modtest.Response) []*modtest.Response` and `awaitLockWaiters(t, h, n, finished)` are `contacts_concurrency_test.go`'s, already shared by `timeline_concurrency_test.go`; the gate/goroutine/`awaitLockWaiters`/`Commit` dance above is copied from `TestPutCustomersByIdTimelineByEntryId_ConcurrentUpdates_ExactlyOneWins` line for line. Do **not** invent a second racing helper. The file needs `"sync"`? No — `race` owns that; it needs `context`, `fmt`, `net/http`, `testing` and `modtest`.

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- go test -count=5 -run TestFollowUpDone_ConcurrentTicks ./internal/customers/
```
Expected: PASS, five times.

- [ ] **Step 11: Commit Tasks 3 and 4 together**

```bash
cd /home/anders/projects/vantigo/vantigo
printf '%s\n\n%s\n\n%s\n' 'feat(customers): a timeline entry can say what happens next, and who is to do it' 'A manual, active entry carries a follow-up — a due date that may be in the future and an optional assignee validated against the directory before any transaction — set, replaced and cleared by the entry'"'"'s own PUT under the revision it already takes. Two new paths tick it done and reopen it: idempotent, no expectedRevision (a tick from a list must not lose a race with an edit of the note), each real change bumping current_revision with a revision row naming who ticked it. /stats/attention gains followUpOverdue and followUpDue for the caller'"'"'s and unassigned open follow-ups — the first item here that depends on who asks — and GET /customers/follow-ups answers the list, defaulting to my open ones.' 'Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>' > /tmp/msg-followups-task34
git add apps/server/internal/customers/follow_ups.go apps/server/internal/customers/follow_ups_test.go apps/server/internal/customers/follow_ups_concurrency_test.go apps/server/internal/customers/timeline.go apps/server/internal/customers/stats.go
git status --short
git commit -F /tmp/msg-followups-task34 -- $(git diff --cached --name-only)
git show --stat HEAD && git status --short
```
The yaml, the generated files and `COVERAGE.md` were staged at the end of Task 3 and go in the same commit.

- [ ] **Step 12: Show the new tests can fail**

Five mutations, by hand, each restored by hand:
1. Drop `AND follow_up_done_at IS NULL` from `SetTimelineEntryFollowUpDone`'s WHERE, `go generate`, and run `TestFollowUpDone_IsIdempotentAndEachRealChangeIsARevision` and `TestFollowUpDone_ConcurrentTicks…` — expect `currentRevision = 3, want 2` on the second tick, and a revision-row count of 3 in the race. Restore, regenerate, green.
2. Remove the `CASE WHEN @follow_up_on::date IS NULL` from `UpdateManualTimelineEntry` (make it a plain `follow_up_done_at = follow_up_done_at`), regenerate, and run `TestPutTimeline_ReplacesTheFollowUpAndClearingItClearsDone` — expect `follow_up_done_at = … want NULL`. Restore, green.
3. Change `due.Before(today)` to `due.After(today)` in `followUpAttentionItems` and run `TestStatsAttention_ReportsTheCallersAndUnassignedFollowUpsOnly` — expect `want one with id "followUpOverdue/…"`. Restore, green.
4. Drop `OR e.follow_up_assignee_user_id IS NULL` from `FollowUpAttentionCandidates`, regenerate, and run the same test — expect the stranger's list to be empty where one unassigned item is wanted. Restore, regenerate, green.
5. Change `truncateUTF16(*r.Note, followUpNoteLength)` to `(*r.Note)[:min(len(*r.Note), followUpNoteLength)]` and run `TestGetFollowUps_CutsTheNoteAtTwoHundredUTF16Units` — expect a length that is not 200 units (and possibly a broken rune). Restore, green.

Note all five in the report.

---

### Task 5: The timeline card — a Follow-up section, a follow-up line, Done/Reopen, and `canManageTimeline` (D4, D3)

**Files:**
- Create: `apps/customers/frontend/src/components/user-picker.tsx`, `apps/customers/frontend/src/components/user-picker.test.tsx`
- Modify: `apps/customers/frontend/src/components/owner-picker.tsx`, `apps/customers/frontend/src/api/timeline.ts`, `apps/customers/frontend/src/pages/-customer-timeline.tsx`, `apps/customers/frontend/src/pages/-customer-timeline.test.tsx`, `apps/customers/frontend/src/pages/customers.$customerId.tsx`, `apps/customers/frontend/src/i18n.ts`
- Read first (do not change): `apps/customers/frontend/src/components/owner-picker.tsx` (the two rules its doc comment names, which `UserPicker` inherits verbatim), `apps/customers/frontend/src/api/owner.ts:28-39` (`assignableUsersQueryOptions`, reused unchanged), `apps/customers/frontend/src/pages/-customer-relationship-card.tsx:146-153` (the one `OwnerPicker` call site, which must keep working untouched)

**Interfaces:**
- Consumes: Task 3's contract, through `TimelineEntry.followUp` and `TimelineInput.followUp`.
- Produces TypeScript:
  - `UserPicker({ label, placeholder, value, selected, onChange, disabled, clearLabel })` in `components/user-picker.tsx`
  - `OwnerPicker` unchanged in signature, implemented over `UserPicker`
  - `TimelineFollowUp {dueOn: string; assignee: TimelineAssignee | null; doneAt: string | null}`, `TimelineAssignee {userId: string; displayName: string; active: boolean}`, `TimelineFollowUpInput {dueOn: string; assigneeUserId?: string}`
  - `TimelineEntry.followUp: TimelineFollowUp | null`, `TimelineRevision.followUp: TimelineFollowUp | null`, `TimelineInput.followUp?: TimelineFollowUpInput | null`
  - `normalizeTimelineEntry`, `normalizeTimelinePage`, `markFollowUpDone(customerId, id)`, `reopenFollowUp(customerId, id)`
  - `CustomerTimeline({ customerId, canManageTimeline })`, `CustomerOverview({ …, canManageTimeline })`

- [ ] **Step 1: Write the failing tests**

Add to `apps/customers/frontend/src/pages/-customer-timeline.test.tsx`. Its `entry()` fixture gains the field first — a **wire-shaped** fixture, so the default is the shape the server actually sends for an entry with no follow-up, which is the key absent:

```tsx
const entry = (overrides: Partial<TimelineEntry> = {}): TimelineEntry => ({
  id: 7,
  provenance: "manual",
  eventType: "note",
  producer: "",
  occurredOn: "2020-07-20",
  occurredAt: null,
  note: "Original note",
  summary: null,
  sourceUrl: null,
  payload: null,
  currentRevision: 2,
  createdAt: "2026-07-20T00:00:00Z",
  updatedAt: "2026-07-20T00:00:00Z",
  actorKind: "user",
  followUp: null,
  ...overrides,
});
```

Its `renderTimeline` helper also changes **here, in this step** — every case that existed before this delivery exercises the Add button and the actions menu, so the shared helper renders a manager and the one reader case renders directly:

```tsx
const renderTimeline = async (fetchMock: ReturnType<typeof vi.fn>) => {
  stubFetch(fetchMock);
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <MantineProvider>
      <QueryClientProvider client={queryClient}>
        <CustomerTimeline customerId={42} canManageTimeline />
      </QueryClientProvider>
    </MantineProvider>,
  );
  await waitFor(() => expect(fetchMock).toHaveBeenCalled());
  return queryClient;
};
```

and six new cases (put them in their own `describe("the timeline's follow-ups", …)` at the end of the file; Step 10's fourth mutation adds a seventh, for the overdue boundary). Note that `renderTimeline` above takes no capability argument on purpose: the reader case below calls `render` itself, **without** `canManageTimeline`, so "the prop was forgotten" and "the prop was withheld" cannot be confused for one another.

```tsx
describe("the timeline's follow-ups", () => {
  // The wire shape, literally: an entry with a follow-up, as the server sends
  // it. The boundary is what turns an absent key into null, so these fixtures
  // never carry a key the server would not.
  const withFollowUp = (followUp: unknown) => ({
    id: 7,
    provenance: "manual",
    eventType: "note",
    producer: "",
    occurredOn: "2020-07-20",
    note: "Original note",
    currentRevision: 2,
    state: "active",
    actorKind: "user",
    createdAt: "2026-07-20T00:00:00Z",
    updatedAt: "2026-07-20T00:00:00Z",
    ...(followUp === undefined ? {} : { followUp }),
  });

  it("shows an overdue follow-up with its assignee, and a done one struck through", async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      if (String(input).includes("/timeline?")) {
        return Promise.resolve(
          json({
            data: [
              withFollowUp({ dueOn: "2020-01-02", assignee: { userId: "u1", displayName: "Kari Nordmann", active: true }, doneAt: null }),
              { ...withFollowUp({ dueOn: "2020-01-03", assignee: null, doneAt: "2020-01-04T09:00:00Z" }), id: 8 },
            ],
            nextCursor: null,
          }),
        );
      }
      return Promise.resolve(json({ data: [] }));
    });
    await renderTimeline(fetchMock);

    // The open, overdue line: the "Follow up <date>" wording, the assignee's
    // name, the word that says it is late, and red.
    const openLine = await screen.findByText(/Follow up /);
    expect(openLine).toHaveTextContent(/Kari Nordmann/);
    expect(openLine).toHaveTextContent(/overdue/);

    // The done line is matched by ITS OWN wording ("Followed up …"), never by
    // /done/i: the Mark done BUTTON on the other row matches that too, so a
    // /done/i assertion would pass with the done line missing entirely.
    const doneLine = screen.getByText(/Followed up /);
    expect(doneLine).not.toHaveTextContent(/overdue/);
    expect(doneLine).toHaveStyle({ textDecoration: "line-through" });
    // And it offers Reopen rather than Mark done.
    expect(screen.getByRole("button", { name: /reopen/i })).toBeInTheDocument();
  });

  it("marks a follow-up done through the entry's own path and refreshes the feed", async () => {
    const done = vi.fn(() => Promise.resolve(json(withFollowUp({ dueOn: "2020-01-02", assignee: null, doneAt: "2020-01-05T10:00:00Z" }))));
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes("/follow-up/done")) return done(input, init);
      if (url.includes("/timeline?")) {
        return Promise.resolve(json({ data: [withFollowUp({ dueOn: "2020-01-02", assignee: null, doneAt: null })], nextCursor: null }));
      }
      return Promise.resolve(json({ data: [] }));
    });
    const stub = stubFetch(fetchMock);
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <MantineProvider>
        <QueryClientProvider client={queryClient}>
          <CustomerTimeline customerId={42} canManageTimeline />
        </QueryClientProvider>
      </MantineProvider>,
    );
    await screen.findByRole("button", { name: /mark done/i });
    await userEvent.click(screen.getByRole("button", { name: /mark done/i }));

    // Never "the last fetch": find the call by method and URL.
    await waitFor(() => {
      const call = stub.actualCalls.find(
        ([url, init]) => String(url).endsWith("/api/v1/customers/42/timeline/7/follow-up/done") && init?.method === "POST",
      );
      expect(call).toBeDefined();
    });
  });

  it("reopens a done follow-up through the same path with DELETE", async () => {
    const reopen = vi.fn(() => Promise.resolve(json(withFollowUp({ dueOn: "2020-01-02", assignee: null, doneAt: null }))));
    let ticked = true;
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes("/follow-up/done")) {
        ticked = false;
        return reopen(input, init);
      }
      if (url.includes("/timeline?")) {
        return Promise.resolve(
          json({
            data: [withFollowUp({ dueOn: "2020-01-02", assignee: null, doneAt: ticked ? "2020-01-04T09:00:00Z" : null })],
            nextCursor: null,
          }),
        );
      }
      return Promise.resolve(json({ data: [] }));
    });
    const stub = stubFetch(fetchMock);
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <MantineProvider>
        <QueryClientProvider client={queryClient}>
          <CustomerTimeline customerId={42} canManageTimeline />
        </QueryClientProvider>
      </MantineProvider>,
    );
    await userEvent.click(await screen.findByRole("button", { name: /reopen/i }));

    await waitFor(() => {
      const call = stub.actualCalls.find(
        ([url, init]) => String(url).endsWith("/api/v1/customers/42/timeline/7/follow-up/done") && init?.method === "DELETE",
      );
      expect(call).toBeDefined();
    });
    // The refresh re-reads, and the line is open again: "Follow up …", no strike.
    expect(await screen.findByText(/Follow up /)).toBeInTheDocument();
    expect(screen.queryByText(/Followed up /)).not.toBeInTheDocument();
  });

  it("hides every timeline control from a reader who cannot manage the timeline", async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      if (String(input).includes("/timeline?")) {
        return Promise.resolve(json({ data: [withFollowUp({ dueOn: "2020-01-02", assignee: null, doneAt: null })], nextCursor: null }));
      }
      return Promise.resolve(json({ data: [] }));
    });
    // Rendered HERE rather than through renderTimeline, and deliberately with
    // no canManageTimeline at all: the shared helper passes it, so this is the
    // one place the withheld case is exercised.
    stubFetch(fetchMock);
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <MantineProvider>
        <QueryClientProvider client={queryClient}>
          <CustomerTimeline customerId={42} />
        </QueryClientProvider>
      </MantineProvider>,
    );

    await screen.findByText("Original note");
    expect(screen.queryByRole("button", { name: /add event/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /mark done/i })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /actions for note/i })).not.toBeInTheDocument();
    // The follow-up itself is still readable — only the control is gone.
    expect(screen.getByText(/Follow up /)).toBeInTheDocument();
  });

  it("shows each revision's own follow-up, so a ticked revision names who ticked it and when", async () => {
    const fetchMock = vi.fn((input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/revisions")) {
        return Promise.resolve(
          json({
            data: [
              {
                revision: 1,
                action: "create",
                eventType: "note",
                occurredOn: "2026-07-20",
                occurredAt: null,
                note: "Original note",
                sourceUrl: null,
                changedAt: "2026-07-20T08:00:00Z",
                actorKind: "user",
                actorDisplayName: "Kari Nordmann",
                followUp: { dueOn: "2026-08-01" },
              },
              {
                revision: 2,
                action: "update",
                eventType: "note",
                occurredOn: "2026-07-20",
                occurredAt: null,
                note: "Original note",
                sourceUrl: null,
                changedAt: "2026-07-22T09:30:00Z",
                actorKind: "user",
                actorDisplayName: "Ola Nordmann",
                followUp: { dueOn: "2026-08-01", doneAt: "2026-07-22T09:30:00Z" },
              },
            ],
          }),
        );
      }
      if (url.includes("/timeline?")) {
        return Promise.resolve(json({ data: [withFollowUp({ dueOn: "2026-08-01", assignee: null, doneAt: "2026-07-22T09:30:00Z" })], nextCursor: null }));
      }
      return Promise.resolve(json({ data: [] }));
    });
    await renderTimeline(fetchMock);
    await userEvent.click(await waitFor(actionsButton));
    await userEvent.click(await screen.findByText("Revision history"));
    const dialog = await screen.findByRole("dialog", { name: "Revision history" });

    // Revision 1 carried an open follow-up; revision 2 is the one that ticked
    // it, and the panel's own actor and changed-at line beside it is what
    // answers "who ticked it and when" (follow-ups design D4) without a doneBy
    // field on the contract.
    expect(await within(dialog).findByText(/Follow up /)).toBeInTheDocument();
    const ticked = within(dialog).getByText(/Followed up /);
    // Each revision is its own Accordion.Panel, which Mantine renders as a
    // region — so the panel holding the "Followed up" line is the panel whose
    // actor line names who ticked it. (If this version emits another role,
    // read the markup once and assert on the panel element it does emit; the
    // requirement is that the two read together, not that it is a region.)
    const panel = ticked.closest("[role='region']");
    expect(panel).toHaveTextContent(/Ola Nordmann/);
    // …and not revision 1's author, which is the whole point of reading the two
    // together rather than anywhere on the page.
    expect(panel).not.toHaveTextContent(/Kari Nordmann/);
  });

  it("sends the follow-up the form collected, and clears it when the section is emptied", async () => {
    const created = vi.fn(() => Promise.resolve(json(withFollowUp({ dueOn: "2026-12-24", assignee: null, doneAt: null }))));
    const fetchMock = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.includes("/assignable-users")) return Promise.resolve(json([{ userId: "u1", displayName: "Kari Nordmann" }]));
      if (url.endsWith("/api/v1/customers/42/timeline") && init?.method === "POST") return created(input, init);
      if (url.includes("/timeline?")) return Promise.resolve(json({ data: [], nextCursor: null }));
      return Promise.resolve(json({ data: [] }));
    });
    const stub = stubFetch(fetchMock);
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <MantineProvider>
        <QueryClientProvider client={queryClient}>
          <CustomerTimeline customerId={42} canManageTimeline />
        </QueryClientProvider>
      </MantineProvider>,
    );
    await userEvent.click(await screen.findByRole("button", { name: /add event/i }));
    await userEvent.type(screen.getByRole("textbox", { name: /description/i }), "Call back");
    await userEvent.type(screen.getByRole("textbox", { name: /follow up on/i }), "2026-12-24");
    await userEvent.click(screen.getByRole("button", { name: /^add event$/i }));

    await waitFor(() => {
      const call = stub.actualCalls.find(
        ([url, init]) => String(url).endsWith("/api/v1/customers/42/timeline") && init?.method === "POST",
      );
      expect(call).toBeDefined();
      expect(JSON.parse(String(call?.[1]?.body))).toMatchObject({ followUp: { dueOn: "2026-12-24" } });
    });
  });
});
```

And a new `apps/customers/frontend/src/components/user-picker.test.tsx`:

```tsx
import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import { UserPicker } from "./user-picker";

afterEach(() => {
  vi.unstubAllGlobals();
  vi.clearAllMocks();
});

const renderPicker = (props: Partial<Parameters<typeof UserPicker>[0]> = {}) => {
  stubFetch(vi.fn(() => Promise.resolve(new Response(JSON.stringify([]), { headers: { "Content-Type": "application/json" } }))));
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <MantineProvider>
      <QueryClientProvider client={queryClient}>
        <UserPicker label="Assignee" placeholder="Search" value={null} onChange={() => {}} clearLabel="Clear assignee" {...props} />
      </QueryClientProvider>
    </MantineProvider>,
  );
};

describe("UserPicker", () => {
  it("is a combobox under the label it was given, not a fixed one", () => {
    renderPicker();
    expect(screen.getByRole("combobox", { name: "Assignee" })).toBeInTheDocument();
  });

  // The rule OwnerPicker was built around and UserPicker inherits: the API
  // answers ACTIVE users only, and a person disabled after being chosen keeps
  // what they were given — so the current value has to survive a search that
  // does not contain it.
  it("keeps the current selection on the list whatever the search returned", () => {
    renderPicker({ value: "u9", selected: { userId: "u9", displayName: "Disabled Personsen" } });
    expect(screen.getByRole("combobox", { name: "Assignee" })).toHaveValue("Disabled Personsen");
  });
});
```

- [ ] **Step 2: Run them and watch them fail**

```bash
cd /home/anders/projects/vantigo/vantigo
mise exec -- bun run --cwd apps/customers/frontend test -- user-picker -customer-timeline
```
Expected: FAIL — `Cannot find module './user-picker'`, and in the timeline file `Unable to find an accessible element with the role "button" and name /mark done/i`, plus a type error on `followUp` not existing on `TimelineEntry` and on `canManageTimeline` not being a prop.

- [ ] **Step 3: `UserPicker`, and `OwnerPicker` over it**

Create `apps/customers/frontend/src/components/user-picker.tsx`:

```tsx
import { Select } from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { useQuery } from "@tanstack/react-query";
import { useI18n } from "@vantigo/frontend-shell";
import { useState } from "react";
import { assignableUsersQueryOptions } from "../api/owner";
import "../i18n";
import { SEARCH_DEBOUNCE_MS } from "../lib/search";

/** A user this picker can show: the two fields any of its callers has. */
export interface PickableUser {
  userId: string;
  displayName: string;
}

/**
 * A searchable `Select` over the directory `GET /customers/assignable-users`
 * answers, for any field of this module that names a colleague: the customer's
 * owner (owner and tags design D1, D3) and a timeline follow-up's assignee
 * (follow-ups design D4). It was `OwnerPicker` until the second caller arrived;
 * the only thing that was ever owner-specific about it is the words, so the
 * words are props and everything else is unchanged.
 *
 * Two rules that look like details and are not:
 *
 *  - `selected` is kept on the option list whatever the search returns. The API
 *    answers ACTIVE users only, and both designs rule that somebody disabled
 *    after being chosen keeps what they were given — so without this the picker
 *    would blank the very name it is meant to be showing.
 *  - Mantine's own `filter` is replaced with one that keeps every option. The
 *    list is already the answer to the search (narrowed by the API, on the
 *    user's email too, which their display name need not contain), so filtering
 *    it again client-side would drop rows the server deliberately returned.
 */
export const UserPicker = ({
  label,
  placeholder,
  value,
  selected,
  onChange,
  disabled,
  clearLabel,
}: {
  label: string;
  placeholder: string;
  value: string | null;
  /** The user already chosen, so a name survives a search that does not contain it. */
  selected?: PickableUser | null;
  onChange: (value: string | null) => void;
  disabled?: boolean;
  clearLabel: string;
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
      label={label}
      placeholder={placeholder}
      searchable
      clearable
      disabled={disabled}
      filter={({ options: parsed }) => parsed}
      onSearchChange={setSearch}
      nothingFoundMessage={t("noAssignableUsers")}
      data={[...options].map(([userId, displayName]) => ({ value: userId, label: displayName }))}
      value={value}
      onChange={onChange}
      // Mantine's clear button keeps its default `aria-hidden`: it is not a
      // control a screen-reader user needs, since clearing the Select from the
      // keyboard is what the empty option is for. The label is still there, so
      // a pointer test can find it.
      clearButtonProps={{ "aria-label": clearLabel }}
    />
  );
};
```

Replace `apps/customers/frontend/src/components/owner-picker.tsx` entirely:

```tsx
import { useI18n } from "@vantigo/frontend-shell";
import type { CustomerOwner } from "../api/customers";
import "../i18n";
import { UserPicker } from "./user-picker";

/**
 * Who owns a customer relationship (owner and tags design D1, D3). It is
 * `UserPicker` with this field's own words — the picker itself was generalised
 * when follow-ups needed the same control for an assignee (follow-ups design
 * D4), and every rule it followed still applies here unchanged; see its doc
 * comment. This wrapper stays rather than the Relationship card calling
 * `UserPicker` directly, so the owner's four strings live in one place instead
 * of at the call site.
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
  return (
    <UserPicker
      label={t("owner")}
      placeholder={t("searchOwners")}
      clearLabel={t("clearOwner")}
      value={value}
      selected={selected}
      onChange={onChange}
      disabled={disabled}
    />
  );
};
```
`CustomerOwner` is `{userId, displayName, active}` and `PickableUser` is `{userId, displayName}`, so it is assignable with no cast.

- [ ] **Step 4: The API boundary**

In `apps/customers/frontend/src/api/timeline.ts`, add the types, the normaliser and the two mutations.

```ts
/** A follow-up's assignee, named by the server from the user directory. */
export interface TimelineAssignee {
  userId: string;
  displayName: string;
  /** False for an account disabled or removed since it was given the follow-up. */
  active: boolean;
}

/** What happens next on an entry (follow-ups design D1). */
export interface TimelineFollowUp {
  dueOn: string;
  assignee: TimelineAssignee | null;
  doneAt: string | null;
}

/** The follow-up to send. `null`, or omitting the field, clears it. */
export interface TimelineFollowUpInput {
  dueOn: string;
  assigneeUserId?: string;
}
```
`TimelineInput` gains `followUp?: TimelineFollowUpInput | null;`, `TimelineEntry` gains `followUp: TimelineFollowUp | null;` and `TimelineRevision` gains `followUp: TimelineFollowUp | null;`.

Then the boundary itself — the server omits `followUp`, `assignee` and `doneAt` rather than sending null, so this is where an absent key becomes `null` and no component downstream ever has to know:

```ts
/**
 * The wire shape of a follow-up: every optional field is ABSENT rather than
 * null when it has no value, which is what this API does everywhere. Turning
 * that into null here is what lets every component read `entry.followUp` and
 * `followUp.assignee` without a second thought about which of the two shapes
 * answered.
 */
type RawTimelineFollowUp = { dueOn: string; assignee?: TimelineAssignee | null; doneAt?: string | null };
type RawTimelineEntry = Omit<TimelineEntry, "followUp"> & { followUp?: RawTimelineFollowUp | null };
type RawTimelineRevision = Omit<TimelineRevision, "followUp"> & { followUp?: RawTimelineFollowUp | null };

export const normalizeFollowUp = (raw?: RawTimelineFollowUp | null): TimelineFollowUp | null =>
  raw ? { dueOn: raw.dueOn, assignee: raw.assignee ?? null, doneAt: raw.doneAt ?? null } : null;

export const normalizeTimelineEntry = (raw: RawTimelineEntry): TimelineEntry => ({
  ...raw,
  followUp: normalizeFollowUp(raw.followUp),
});
```

`fetchTimeline` maps its page through it, and the three single-entry calls each map their answer:

```ts
export async function fetchTimeline(
  customerId: number,
  cursor?: string,
  signal?: AbortSignal,
  filters: TimelineFilters = defaultTimelineFilters,
): Promise<TimelinePage> {
  const params = new URLSearchParams({ limit: "25" });
  if (cursor) params.set("cursor", cursor);
  const normalized = normalizeTimelineFilters(filters);
  if (normalized.provenance !== "all") params.set("provenance", normalized.provenance);
  normalized.eventTypes.forEach((eventType) => {
    params.append("eventType", eventType);
  });
  if (normalized.occurredFrom) params.set("occurredFrom", normalized.occurredFrom);
  if (normalized.occurredTo) params.set("occurredTo", normalized.occurredTo);
  const page = await request<{ data: RawTimelineEntry[]; nextCursor: string | null }>(
    `/api/v1/customers/${customerId}/timeline?${params}`,
    { signal },
  );
  return { data: page.data.map(normalizeTimelineEntry), nextCursor: page.nextCursor ?? null };
}

export async function createTimelineEntry(customerId: number, input: TimelineInput) {
  return normalizeTimelineEntry(
    (await request(`/api/v1/customers/${customerId}/timeline`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(input),
    })) as RawTimelineEntry,
  );
}

export async function updateTimelineEntry(
  customerId: number,
  id: TimelineEntry["id"],
  input: TimelineInput,
  expectedRevision: number,
) {
  return normalizeTimelineEntry(
    (await request(`/api/v1/customers/${customerId}/timeline/${id}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ ...input, expectedRevision }),
    })) as RawTimelineEntry,
  );
}

/**
 * Ticks an entry's follow-up done, or reopens it. Neither sends an
 * expectedRevision, and that is the contract's own decision (follow-ups design
 * D1): a tick comes from a list and must not conflict with somebody editing the
 * note. Both answer the whole entry, so a caller can read the fresh revision
 * straight off the response.
 */
export const markFollowUpDone = async (customerId: number, id: TimelineEntry["id"]) =>
  normalizeTimelineEntry(
    (await request(`/api/v1/customers/${customerId}/timeline/${id}/follow-up/done`, {
      method: "POST",
    })) as RawTimelineEntry,
  );

export const reopenFollowUp = async (customerId: number, id: TimelineEntry["id"]) =>
  normalizeTimelineEntry(
    (await request(`/api/v1/customers/${customerId}/timeline/${id}/follow-up/done`, {
      method: "DELETE",
    })) as RawTimelineEntry,
  );
```
and `timelineRevisionsQueryOptions`' `queryFn` maps its data: `return response.data.map((raw) => ({ ...raw, followUp: normalizeFollowUp(raw.followUp) }));` with the response typed `{ data: RawTimelineRevision[] }`.

- [ ] **Step 5: The timeline card**

In `apps/customers/frontend/src/pages/-customer-timeline.tsx`:

`CustomerTimeline` takes the new prop and threads it:

```tsx
/**
 * `canManageTimeline` comes from the host, which reads the caller's
 * `customers:timeline-manage` permission (follow-ups design D3) — this package
 * never fetches permissions itself. Until this delivery the server alone
 * enforced it and a reader saw Add, Edit and Delete buttons that answered 403;
 * now they are simply not there, and neither are the follow-up controls.
 *
 * It is optional and defaults to withheld, which is the safe direction: a call
 * site that forgets it shows a read-only card rather than buttons that fail.
 */
export const CustomerTimeline = ({
  customerId,
  canManageTimeline,
}: {
  customerId: number;
  canManageTimeline?: boolean;
}) => {
```

The Add button, the per-entry `Menu` (Edit / Revision history / Delete) and the new Done/Reopen control are each wrapped in `{canManageTimeline && …}` — except **Revision history**, which is a read: move it out of the menu only if the menu disappears entirely for a reader. It does, so keep the menu behind `canManageTimeline` and leave revision history inside it; a reader loses nothing they can act on, and the card stays one control group rather than two. Say so in a comment:

```tsx
{entry.provenance === "manual" && canManageTimeline && (
  <Menu position="bottom-end" withinPortal>
```
with, above it:
```tsx
{/* Revision history is a read and could in principle stay for a reader, but
    it lives inside this menu and a menu with one item is worse than no menu.
    A reader who needs the history has the API; a reader who needs it in the UI
    is a request this delivery has not had. */}
```

The follow-up line goes in the entry's `Stack`, after the note and before `sourceUrl`:

```tsx
{entry.followUp && (
  <Group gap="xs" align="center">
    <IconFlag size={14} aria-hidden="true" />
    <Text
      size="sm"
      c={followUpTone(entry.followUp)}
      td={entry.followUp.doneAt ? "line-through" : undefined}
    >
      {entry.followUp.doneAt
        ? t("followUpDoneOn", { date: formatDateOnly(entry.followUp.doneAt.slice(0, 10)) })
        : t("followUpDue", { date: formatDateOnly(entry.followUp.dueOn) })}
      {entry.followUp.assignee
        ? ` · ${entry.followUp.assignee.displayName}${entry.followUp.assignee.active ? "" : ` (${t("inactiveUser")})`}`
        : ` · ${t("followUpUnassigned")}`}
      {isOverdue(entry.followUp) ? ` · ${t("followUpOverdue")}` : ""}
    </Text>
    {canManageTimeline && (
      <Button
        size="compact-xs"
        variant="subtle"
        loading={followUpMutation.isPending && followUpMutation.variables?.id === entry.id}
        onClick={() => followUpMutation.mutate({ id: entry.id, done: !entry.followUp?.doneAt })}
      >
        {entry.followUp.doneAt ? t("reopen") : t("markDone")}
      </Button>
    )}
  </Group>
)}
```

with, beside `utcToday` at the top of the file:

```tsx
/** Open and past its due date, in UTC — the same calendar the server compares in. */
const isOverdue = (followUp: TimelineFollowUp) => !followUp.doneAt && followUp.dueOn < utcToday();
/** Red while overdue, grey once done, ordinary otherwise. */
const followUpTone = (followUp: TimelineFollowUp) =>
  followUp.doneAt ? "dimmed" : isOverdue(followUp) ? "red" : undefined;
```
(`followUp.dueOn < utcToday()` is a string comparison, and it is correct precisely because both sides are `yyyy-MM-dd`: that format sorts lexicographically the same way it sorts chronologically, which is why the whole codebase passes calendar dates around as strings.)

and the mutation, beside `remove`:

```tsx
const followUpMutation = useMutation({
  mutationFn: ({ id, done }: { id: TimelineEntry["id"]; done: boolean }) =>
    done ? markFollowUpDone(customerId, id) : reopenFollowUp(customerId, id),
  onSuccess: refresh,
  onError: (error: Error) => {
    // No 409 branch: neither path takes an expectedRevision, so the only
    // failures left are a vanished entry and the network.
    refresh();
    notifications.show({ color: "red", title: t("couldNotUpdateFollowUp"), message: error.message });
  },
});
```

`IconFlag` joins the `@tabler/icons-react` import; `TimelineFollowUp`, `markFollowUpDone` and `reopenFollowUp` join the `../api/timeline` import.

The form's Follow-up section goes at the end of `TimelineForm`'s `Stack`, before the buttons. `TimelineForm` takes `entry` already; it gains nothing but three form fields:

```tsx
  const form = useForm({
    initialValues: {
      eventType: "note",
      occurredOn: utcToday(),
      occurredAt: "",
      note: "",
      sourceUrl: "",
      followUpOn: "",
      followUpAssigneeUserId: "",
    },
    validate: {
      eventType: (v) => (!v ? t("typeRequired") : null),
      occurredOn: (v) => (!v ? t("dateRequired") : v > utcToday() ? t("dateFuture") : null),
      note: (v) => (!v.trim() ? t("descriptionRequired") : null),
      // No future check here, and that is the point of a follow-up: it is the
      // one date in this form that is allowed to be ahead of today.
      followUpAssigneeUserId: (value, values) =>
        value && !values.followUpOn ? t("followUpDateRequiredForAssignee") : null,
    },
  });
```
its `useEffect` seeds them from the entry:
```tsx
            followUpOn: entry.followUp?.dueOn ?? "",
            followUpAssigneeUserId: entry.followUp?.assignee?.userId ?? "",
```
(and `""`/`""` in the "new entry" branch), and `submit` maps them to the wire:
```tsx
  const submit = form.onSubmit(({ followUpOn, followUpAssigneeUserId, ...values }) =>
    mutation.mutate({
      ...values,
      occurredAt: values.occurredAt
        ? new Date(`${values.occurredOn}T${values.occurredAt}:00Z`).toISOString()
        : undefined,
      sourceUrl: values.sourceUrl || undefined,
      // No date means no follow-up, and on an update that is an instruction:
      // this PUT is a full replace, so omitting the field clears whatever the
      // entry had — including its done state.
      followUp: followUpOn
        ? { dueOn: followUpOn, assigneeUserId: followUpAssigneeUserId || undefined }
        : undefined,
    }),
  );
```
and the section itself:
```tsx
          <Divider label={t("followUp")} labelPosition="left" />
          <DateInput
            label={t("followUpOn")}
            description={t("followUpOnHint")}
            valueFormat="YYYY-MM-DD"
            clearable
            {...form.getInputProps("followUpOn")}
          />
          <UserPicker
            label={t("followUpAssignee")}
            placeholder={t("searchOwners")}
            clearLabel={t("clearFollowUpAssignee")}
            value={form.values.followUpAssigneeUserId || null}
            selected={
              entry?.followUp?.assignee
                ? { userId: entry.followUp.assignee.userId, displayName: entry.followUp.assignee.displayName }
                : null
            }
            onChange={(value) => form.setFieldValue("followUpAssigneeUserId", value ?? "")}
          />
```
`Divider` joins the `@mantine/core` import and `UserPicker` the component imports.

- [ ] **Step 6: The follow-up per revision, which is where "who ticked it" lives**

`RevisionPanel` (lines ~623-667) shows each revision's action and actor and its note. Design D4 asks for the follow-up fields per revision, and design D1's response shape has no `doneBy` — so the two together are what answer "who ticked it and when": the revision that *set* `doneAt` is the tick, and the panel already names that revision's actor. Make it name the time too, and add the follow-up line.

`const { t } = useI18n("customers");` becomes `const { t, formatters } = useI18n("customers");`, and the panel body's `Stack` becomes:

```tsx
                <Stack gap="xs">
                  <Text size="sm">
                    {revision.action} · {actorLabel(revision.actorKind, revision.actorDisplayName, t)} ·{" "}
                    {formatters.formatDate(revision.changedAt, { dateStyle: "medium", timeStyle: "short" })}
                  </Text>
                  {revision.followUp && (
                    <Text size="sm" c={revision.followUp.doneAt ? "dimmed" : undefined}>
                      {revision.followUp.doneAt
                        ? t("followUpDoneOn", {
                            date: formatters.formatDate(`${revision.followUp.doneAt.slice(0, 10)}T00:00:00Z`, {
                              dateStyle: "medium",
                              timeZone: "UTC",
                            }),
                          })
                        : t("followUpDue", {
                            date: formatters.formatDate(`${revision.followUp.dueOn}T00:00:00Z`, {
                              dateStyle: "medium",
                              timeZone: "UTC",
                            }),
                          })}
                      {revision.followUp.assignee ? ` · ${revision.followUp.assignee.displayName}` : ""}
                    </Text>
                  )}
                  <Text>{revision.note || t("noDescription")}</Text>
                </Stack>
```

The `changedAt` addition is what turns "who" into "who and when", and it is safe for the existing revision test: that one asserts `within(dialog).getByText(/Unattributed/)`, a partial regex on the same `Text`, which still matches with a timestamp appended. The follow-up line is **not** struck through here the way the entry line is — a revision is a record, not a live item, so "grey" is the whole of the styling it needs.

- [ ] **Step 7: Pass the capability down from the page**

In `apps/customers/frontend/src/pages/customers.$customerId.tsx`, `CustomerOverview` takes one more prop and hands it on:

```tsx
export const CustomerOverview = ({
  customerId,
  canEdit,
  canManageBilling,
  canViewIdentity,
  canManageIdentity,
  canManageTimeline,
}: {
  customerId: number;
  canEdit?: boolean;
  canManageBilling?: boolean;
  canViewIdentity?: boolean;
  canManageIdentity?: boolean;
  canManageTimeline?: boolean;
}) => {
```
and, at line ~371:
```tsx
      <CustomerTimeline customerId={customerId} canManageTimeline={canManageTimeline} />
```
Add to the component's doc comment:
```
 * `canManageTimeline` (follow-ups design D3) is the host's
 * `customers:timeline-manage` check, and it is the first capability here that
 * closes a gap rather than adding one: the timeline card's Add, Edit and Delete
 * controls were always server-enforced, so a reader saw buttons that answered
 * 403. It also gates the new Done/Reopen control on a follow-up.
```

- [ ] **Step 8: Both catalogs**

In `apps/customers/frontend/src/i18n.ts`, add to `en` (beside the other timeline keys, after `sourceUrl`) and the mirrored entries to `nb`:

```ts
  followUp: "Follow-up",
  followUpOn: "Follow up on",
  followUpOnHint: "May be in the future. Leave empty for no follow-up.",
  followUpAssignee: "Assigned to",
  clearFollowUpAssignee: "Clear assignee",
  followUpDateRequiredForAssignee: "Give the follow-up a date, or clear the assignee",
  followUpDue: "Follow up {{date}}",
  followUpDoneOn: "Followed up {{date}}",
  followUpOverdue: "overdue",
  followUpUnassigned: "Unassigned",
  inactiveUser: "inactive",
  markDone: "Mark done",
  reopen: "Reopen",
  couldNotUpdateFollowUp: "Could not update the follow-up",
```
```ts
  followUp: "Oppfølging",
  followUpOn: "Følg opp",
  followUpOnHint: "Kan være fram i tid. La stå tom for ingen oppfølging.",
  followUpAssignee: "Tildelt",
  clearFollowUpAssignee: "Fjern tildeling",
  followUpDateRequiredForAssignee: "Gi oppfølgingen en dato, eller fjern tildelingen",
  followUpDue: "Følg opp {{date}}",
  followUpDoneOn: "Fulgt opp {{date}}",
  followUpOverdue: "forfalt",
  followUpUnassigned: "Ikke tildelt",
  inactiveUser: "inaktiv",
  markDone: "Merk som utført",
  reopen: "Åpne igjen",
  couldNotUpdateFollowUp: "Kunne ikke oppdatere oppfølgingen",
```
(Task 6 adds the Follow-ups **page**'s own strings to the same two objects; keep these two groups separate so each task's diff reads on its own.)

- [ ] **Step 9: Run the frontend and commit**

```bash
cd /home/anders/projects/vantigo/vantigo
mise exec -- bun run --cwd apps/customers/frontend test
mise exec -- bun run translations:check && mise exec -- bun run i18n:test
bunx biome check apps/customers/frontend/src
mise run frontend:check
```
Expected: PASS, the existing `-customer-relationship-card.test.tsx` included — `OwnerPicker`'s props did not change, so its call site and its tests are untouched, which is the whole reason the wrapper exists. Every pre-existing `-customer-timeline.test.tsx` case also passes unchanged, because Step 1 already put `canManageTimeline` into the shared `renderTimeline`; if one of them still fails on a missing Add button or actions menu, that helper edit was missed.

```bash
cd /home/anders/projects/vantigo/vantigo
printf '%s\n\n%s\n\n%s\n' 'feat(customers-ui): the timeline says what happens next, and a reader stops seeing buttons that 403' 'The entry form gains a Follow-up section — a date that may be in the future and an assignee picked from the same directory search the owner uses — and each entry shows its follow-up, red while overdue and struck through once done, with a Done/Reopen control. The owner picker is generalised into a UserPicker with the owner card calling it through a thin wrapper, so nothing about the owner moved. canManageTimeline arrives as a capability prop and hides Add, Edit, Delete and the follow-up controls from a reader.' 'Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>' > /tmp/msg-followups-task5
git add apps/customers/frontend/src/components/user-picker.tsx apps/customers/frontend/src/components/user-picker.test.tsx apps/customers/frontend/src/components/owner-picker.tsx apps/customers/frontend/src/api/timeline.ts apps/customers/frontend/src/pages/-customer-timeline.tsx apps/customers/frontend/src/pages/-customer-timeline.test.tsx apps/customers/frontend/src/pages/customers.\$customerId.tsx apps/customers/frontend/src/i18n.ts
git commit -F /tmp/msg-followups-task5 -- $(git diff --cached --name-only)
git show --stat HEAD && git status --short
```

- [ ] **Step 10: Show the new tests can fail**

Four mutations, by hand:
1. Delete the follow-up `<Text>` from `RevisionPanel` and run the revision case — expect `Unable to find an element with the text: /Followed up /`. Restore, green.
2. Delete `followUp: normalizeFollowUp(raw.followUp)` from `normalizeTimelineEntry` and run the timeline tests — expect the "shows an overdue follow-up" case to find no `/Follow up /` text. Restore, green.
3. Change `{canManageTimeline && (` on the Done/Reopen button to `{true && (` and run "hides every timeline control from a reader" — expect it to find a `/mark done/i` button it should not. Restore, green.
4. Change `followUp.dueOn < utcToday()` to `<=` in `isOverdue` and run the follow-up display case — expect a due-today follow-up to read as overdue. (The fixture dates above are all in 2020, so this mutation cannot be caught by them: **add** a case whose `dueOn` is `utcToday()` and assert its line does **not** say overdue. That is a gap to close, not a mutation to skip.) Restore, green.

Note all four in the report.

---

### Task 6: The Follow-ups page, its nav entry and route, and the dashboard's two sentences (D3, D2)

**Files:**
- Create: `apps/customers/frontend/src/api/follow-ups.ts`, `apps/customers/frontend/src/pages/follow-ups.tsx`, `apps/customers/frontend/src/pages/follow-ups.test.tsx`, `apps/host/frontend/src/routes/customers/follow-ups.tsx`, `apps/host/frontend/src/routes/customers/-follow-ups-page.tsx`, `apps/host/frontend/src/routes/customers/follow-ups-route.test.tsx`
- Modify: `apps/host/frontend/src/apps.ts`, `apps/host/frontend/src/apps.test.ts`, `apps/host/frontend/src/catalogs/navigation.ts`, `apps/host/frontend/src/catalogs/dashboard.ts`, `apps/host/frontend/src/routes/dashboard.tsx`, `apps/host/frontend/src/routes/dashboard.test.ts`, `apps/host/frontend/src/routes/customers/-customer-overview-tab.tsx`, `apps/host/frontend/src/routes/customers/customer-overview-route.test.tsx`, `apps/customers/frontend/src/i18n.ts`, `apps/host/frontend/src/routeTree.gen.ts` (regenerated, never edited)
- Read first (do not change): `apps/host/frontend/src/routes/customers/contacts/index.tsx` (the thin-route precedent), `apps/host/frontend/src/routes/customers/-customer-overview-tab.tsx` (the capability-prop wrapper this task copies twice), `apps/customers/frontend/src/pages/contacts.index.tsx` (the paginated-page precedent: `useSearch`, `useNavigate`, Mantine `Pagination`)

**Interfaces:**
- Consumes: Task 3's `PaginatedResponseOfCustomerFollowUp`; Task 5's `markFollowUpDone`, `normalizeFollowUp`, `TimelineFollowUp`.
- Produces TypeScript:
  - `FollowUpAssigneeFilter = "me" | "none"`, `FollowUpState = "open" | "overdue" | "done" | "all"`, `FollowUpRow`, `followUpsQueryOptions(params)`, `followUpsListParams(search)`
  - `FollowUpsPage({ canManageTimeline })` exported from `@vantigo/customers-ui/pages/follow-ups`

- [ ] **Step 1: Write the failing tests**

`apps/customers/frontend/src/pages/follow-ups.test.tsx`:

```tsx
import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import { FollowUpsPage } from "./follow-ups";

// vi.hoisted, because vi.mock's factory is hoisted above every import and a
// plain `const` declared here would not exist when it runs — the pattern
// `-customers.index.test.tsx` already uses for the same router. The whole module
// is replaced rather than spread over the real one: the real `Link` needs a
// router context this test has no reason to build.
const router = vi.hoisted(() => ({
  search: { page: 1, assignee: "me", state: "open" } as Record<string, unknown>,
  navigate: vi.fn(),
}));

vi.mock("@tanstack/react-router", () => ({
  useSearch: () => router.search,
  useNavigate: () => router.navigate,
  Link: ({ children }: { children: ReactNode }) => <a href="#stub">{children}</a>,
}));

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

// Literally the wire body: followUp is required on a row, assignee and doneAt
// are omitted rather than null when they have no value.
const page = (rows: unknown[]) => ({
  data: rows,
  pagination: { page: 1, pageSize: 25, totalCount: rows.length, totalPages: 1, hasNextPage: false, hasPreviousPage: false },
});
const row = (overrides: Record<string, unknown> = {}) => ({
  entryId: 7,
  customerId: 42,
  customerName: "Alpha Co",
  eventType: "note",
  occurredOn: "2020-07-20",
  note: "Ring back about the renewal",
  followUp: { dueOn: "2020-08-01" },
  ...overrides,
});

const renderPage = (fetchMock: ReturnType<typeof vi.fn>, canManageTimeline = true) => {
  const stub = stubFetch(fetchMock);
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <MantineProvider>
      <QueryClientProvider client={queryClient}>
        <FollowUpsPage canManageTimeline={canManageTimeline} />
      </QueryClientProvider>
    </MantineProvider>,
  );
  return stub;
};

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.clearAllMocks();
  router.search = { page: 1, assignee: "me", state: "open" };
});

describe("the Follow-ups page", () => {
  it("asks for the caller's open follow-ups and shows a row per follow-up", async () => {
    const stub = renderPage(vi.fn(() => Promise.resolve(json(page([row()])))));

    expect(await screen.findByText("Alpha Co")).toBeInTheDocument();
    expect(screen.getByText(/Ring back about the renewal/)).toBeInTheDocument();
    // Never "the last fetch": find the call by URL.
    const call = stub.actualCalls.find(([url]) => String(url).includes("/api/v1/customers/follow-ups"));
    expect(call).toBeDefined();
    const url = new URL(String(call?.[0]), "http://test");
    expect(url.searchParams.get("assignee")).toBe("me");
    expect(url.searchParams.get("state")).toBe("open");
  });

  it("puts a filter change in the URL rather than in its own state", async () => {
    renderPage(vi.fn(() => Promise.resolve(json(page([])))));
    await screen.findByRole("combobox", { name: /state/i });

    await userEvent.click(screen.getByRole("combobox", { name: /state/i }));
    await userEvent.click(await screen.findByRole("option", { name: /overdue/i }));

    await waitFor(() => {
      expect(router.navigate).toHaveBeenCalledWith(
        expect.objectContaining({ search: expect.objectContaining({ state: "overdue", page: 1 }) }),
      );
    });
  });

  it("reads the filters back out of the URL when the URL is what changed", async () => {
    router.search = { page: 1, assignee: "none", state: "done" };
    const stub = renderPage(vi.fn(() => Promise.resolve(json(page([])))));

    await waitFor(() => {
      const call = stub.actualCalls.find(([url]) => String(url).includes("/api/v1/customers/follow-ups"));
      const url = new URL(String(call?.[0]), "http://test");
      expect(url.searchParams.get("assignee")).toBe("none");
      expect(url.searchParams.get("state")).toBe("done");
    });
  });

  it("ticks a row done through the entry's own path", async () => {
    const stub = renderPage(
      vi.fn((input: RequestInfo | URL) => {
        if (String(input).includes("/follow-up/done")) return Promise.resolve(json(row()));
        return Promise.resolve(json(page([row()])));
      }),
    );
    await userEvent.click(await screen.findByRole("button", { name: /mark done/i }));
    await waitFor(() => {
      const call = stub.actualCalls.find(
        ([url, init]) => String(url).endsWith("/api/v1/customers/42/timeline/7/follow-up/done") && init?.method === "POST",
      );
      expect(call).toBeDefined();
    });
  });

  // Its own `it`, not a second render inside the one above: two renders in one
  // test leave two copies of the table in the document, and `queryByRole` then
  // finds the first render's button and the assertion passes for the wrong
  // reason. `afterEach`'s cleanup() is what makes one render per test true.
  it("hides the tick from a reader who cannot manage the timeline", async () => {
    renderPage(vi.fn(() => Promise.resolve(json(page([row()])))), false);
    expect(await screen.findByText("Alpha Co")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /mark done/i })).not.toBeInTheDocument();
  });
});
```

`apps/host/frontend/src/routes/customers/follow-ups-route.test.tsx`:

```tsx
import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
// Sorted the way biome sorts: "-follow-ups-page" before "follow-ups" ('-' sorts
// before 'f'), and the side-effect i18n import last, which is where
// `customer-overview-route.test.tsx` already puts its own.
import { FollowUpsTab } from "./-follow-ups-page";
import { Route as FollowUpsRoute } from "./follow-ups";
import "../../i18n";

vi.mock("@tanstack/react-query", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@tanstack/react-query")>();
  return { ...actual, useQuery: vi.fn() };
});

vi.mock("@vantigo/customers-ui/pages/follow-ups", () => ({
  FollowUpsPage: ({ canManageTimeline }: { canManageTimeline?: boolean }) => (
    <span data-testid="can-manage-timeline">{String(Boolean(canManageTimeline))}</span>
  ),
}));

const renderTab = (permissions: string[]) => {
  vi.mocked(useQuery).mockImplementation((options) =>
    options.queryKey[0] === "authorization"
      ? ({ data: { permissions }, isPending: false } as never)
      : ({ data: { user: { id: "user-1", roles: [] } } } as never),
  );
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <MantineProvider>
      <QueryClientProvider client={queryClient}>
        <FollowUpsTab />
      </QueryClientProvider>
    </MantineProvider>,
  );
};

describe("the Follow-ups route's search params", () => {
  const validate = FollowUpsRoute.options.validateSearch as (search: Record<string, unknown>) => unknown;

  it("defaults to my open follow-ups on the first page", () => {
    expect(validate({})).toEqual({ page: 1, assignee: "me", state: "open", customerId: undefined });
  });

  it("keeps only the filter values the API accepts", () => {
    expect(validate({ assignee: "none", state: "overdue", page: "3", customerId: "42" })).toEqual({
      page: 3,
      assignee: "none",
      state: "overdue",
      customerId: 42,
    });
    // 'Me' is case-sensitive at the API and a uuid is not one of the page's two
    // choices, so neither reaches the URL; an unknown state falls back rather
    // than narrowing the list by something the Select cannot show.
    expect(validate({ assignee: "Me", state: "bogus", customerId: "nope" })).toEqual({
      page: 1,
      assignee: "me",
      state: "open",
      customerId: undefined,
    });
  });
});

describe("the Follow-ups route's capability prop", () => {
  it("passes canManageTimeline from customers:timeline-manage", () => {
    renderTab(["customers:timeline-manage"]);
    expect(screen.getByTestId("can-manage-timeline")).toHaveTextContent("true");
  });

  it("withholds it without the permission", () => {
    renderTab(["customers:timeline-view"]);
    expect(screen.getByTestId("can-manage-timeline")).toHaveTextContent("false");
  });
});
```

In `apps/host/frontend/src/apps.test.ts`, extend the two assertions this task moves:

```ts
    expect(paths).toEqual([
      "/customers",
      "/customers/contacts",
      "/customers/follow-ups",
      "/projects",
```
```ts
    expect(appForKey("customers").requiredPermissions).toEqual([
      "customers:view",
      "customers:contacts-view",
      "customers:associations-view",
      "customers:timeline-view",
    ]);
```

In `apps/host/frontend/src/routes/dashboard.test.ts`, add to the attention describe blocks:

```ts
  it("sends a follow-up item to its customer, whose page holds the timeline", () => {
    expect(attentionHref({ module: "customers", type: "followUpOverdue", entityId: "42" })).toBe("/customers/42");
    expect(attentionHref({ module: "customers", type: "followUpDue", entityId: "42" })).toBe("/customers/42");
  });
```
and, in the title describe block:
```ts
  it("names the two follow-up signals with the customer's own name", () => {
    expect(attentionTitleKey({ module: "customers", type: "followUpOverdue" })).toBe("dashboard.customerFollowUpOverdue");
    expect(attentionTitleKey({ module: "customers", type: "followUpDue" })).toBe("dashboard.customerFollowUpDue");
    // The local stub `t` this file already uses for the four registry cases
    // (`(key, values) => \`${key}:${values?.name}\``), not the real catalog:
    // what is under test is that the right KEY is looked up with the customer's
    // name, and asserting a translated sentence would make this test fail the
    // day somebody rewords the Norwegian.
    const t = (key: string, values?: Record<string, unknown>) => `${key}:${values?.name}`;
    expect(
      attentionTitle(
        { module: "customers", type: "followUpOverdue", entityId: "42", title: "Alpha Co" },
        t,
        formatInLosAngeles,
      ),
    ).toBe("dashboard.customerFollowUpOverdue:Alpha Co");
    expect(
      attentionTitle(
        { module: "customers", type: "followUpDue", entityId: "42", title: "Alpha Co" },
        t,
        formatInLosAngeles,
      ),
    ).toBe("dashboard.customerFollowUpDue:Alpha Co");
  });
```
`formatInLosAngeles` is this file's existing date-formatter stub, declared at line ~104 **inside** the attention `describe`, and the registry cases at lines ~196-210 pass it as `attentionTitle`'s third argument. Because it is block-scoped, put these assertions in that same `describe` — folding them into the existing registry `it` is fine, and the point is the stub `t`, not the block boundary. The file does import `i18n` at line 1 for other purposes; do not reach for it here.

In `apps/host/frontend/src/routes/customers/customer-overview-route.test.tsx`, add a fifth capability to the mock and two cases:

```tsx
vi.mock("@vantigo/customers-ui/pages/customers.$customerId", () => ({
  CustomerOverview: ({
    canEdit,
    canManageBilling,
    canViewIdentity,
    canManageIdentity,
    canManageTimeline,
  }: {
    customerId: number;
    canEdit?: boolean;
    canManageBilling?: boolean;
    canViewIdentity?: boolean;
    canManageIdentity?: boolean;
    canManageTimeline?: boolean;
  }) => (
    <>
      <span data-testid="can-edit">{String(Boolean(canEdit))}</span>
      <span data-testid="can-manage-billing">{String(Boolean(canManageBilling))}</span>
      <span data-testid="can-view-identity">{String(Boolean(canViewIdentity))}</span>
      <span data-testid="can-manage-identity">{String(Boolean(canManageIdentity))}</span>
      <span data-testid="can-manage-timeline">{String(Boolean(canManageTimeline))}</span>
    </>
  ),
}));
```
```tsx
describe("the customer overview tab's canManageTimeline capability prop", () => {
  it("passes canManageTimeline from customers:timeline-manage", () => {
    vi.mocked(useQuery).mockImplementation((options) =>
      options.queryKey[0] === "authorization"
        ? ({ data: { permissions: ["customers:timeline-manage"] }, isPending: false } as never)
        : ({ data: { user: { id: "user-1", roles: [] } } } as never),
    );
    renderTab();
    expect(screen.getByTestId("can-manage-timeline")).toHaveTextContent("true");
  });

  it("withholds it for a caller who may only read the timeline", () => {
    vi.mocked(useQuery).mockImplementation((options) =>
      options.queryKey[0] === "authorization"
        ? ({ data: { permissions: ["customers:timeline-view"] }, isPending: false } as never)
        : ({ data: { user: { id: "user-1", roles: [] } } } as never),
    );
    renderTab();
    expect(screen.getByTestId("can-manage-timeline")).toHaveTextContent("false");
  });
});
```

- [ ] **Step 2: Run them and watch them fail**

```bash
cd /home/anders/projects/vantigo/vantigo
mise exec -- bun run --cwd apps/customers/frontend test -- follow-ups
mise exec -- bun run --cwd apps/host/frontend test -- follow-ups-route apps dashboard customer-overview-route
```
Expected: FAIL — `Cannot find module './follow-ups'` in both packages, `paths` missing `/customers/follow-ups`, `attentionTitleKey(... followUpOverdue)` returning `undefined`, and `can-manage-timeline` not found.

- [ ] **Step 3: The package's API module**

Create `apps/customers/frontend/src/api/follow-ups.ts`:

```ts
import { keepPreviousData, queryOptions } from "@tanstack/react-query";
import type { PaginatedResponse } from "./customers";
import { request } from "./request";
import { normalizeFollowUp, type TimelineFollowUp } from "./timeline";

/**
 * The Follow-ups page's two filters (follow-ups design D3). The API also accepts
 * a bare user id for `assignee`; the page deliberately offers only these two,
 * the same choice the customer list's Owner filter makes — a uuid in the URL
 * would narrow the rows by something the Select cannot show.
 */
export type FollowUpAssigneeFilter = "me" | "none";
export type FollowUpState = "open" | "overdue" | "done" | "all";

export const FOLLOW_UP_ASSIGNEES: readonly FollowUpAssigneeFilter[] = ["me", "none"];
export const FOLLOW_UP_STATES: readonly FollowUpState[] = ["open", "overdue", "done", "all"];

/** One row of the list: the entry, its customer, and the follow-up itself. */
export interface FollowUpRow {
  entryId: number;
  customerId: number;
  customerName: string;
  eventType: string;
  occurredOn: string;
  note: string | null;
  followUp: TimelineFollowUp;
}

type RawFollowUpRow = Omit<FollowUpRow, "note" | "followUp"> & {
  note?: string | null;
  followUp: Parameters<typeof normalizeFollowUp>[0];
};

export interface FollowUpsQueryParams {
  page: number;
  pageSize?: number;
  assignee: FollowUpAssigneeFilter;
  state: FollowUpState;
  customerId?: number;
}

/**
 * The search a URL carries, turned into the params both the route's loader and
 * the page's own query build — one function, so a prefetch cannot key
 * differently from the read it is meant to warm (the customer list's
 * `customersListParams` is the same seam for the same reason).
 */
export const followUpsListParams = (search: {
  page?: number;
  assignee?: FollowUpAssigneeFilter;
  state?: FollowUpState;
  customerId?: number;
}): FollowUpsQueryParams => ({
  page: search.page ?? 1,
  assignee: search.assignee ?? "me",
  state: search.state ?? "open",
  customerId: search.customerId,
});

export const followUpsQueryOptions = (params: FollowUpsQueryParams) =>
  queryOptions({
    queryKey: ["customers", "follow-ups", params],
    queryFn: async ({ signal }) => {
      const query = new URLSearchParams({
        page: String(params.page),
        pageSize: String(params.pageSize ?? 25),
        assignee: params.assignee,
        state: params.state,
      });
      if (params.customerId) query.set("customerId", String(params.customerId));
      const answered = await request<PaginatedResponse<RawFollowUpRow>>(`/api/v1/customers/follow-ups?${query}`, {
        signal,
      });
      return {
        ...answered,
        data: answered.data.map((raw) => ({
          ...raw,
          note: raw.note ?? null,
          // followUp is required on this shape — carrying one is what put the
          // row on the list — so the normaliser's null branch is unreachable
          // here; it is called anyway so `assignee` and `doneAt` get the same
          // absent-to-null treatment they get on an entry.
          followUp: normalizeFollowUp(raw.followUp) as TimelineFollowUp,
        })),
      };
    },
    placeholderData: keepPreviousData,
  });
```
The query key sits under the `["customers"]` prefix on purpose: ticking a follow-up from the customer page already invalidates that prefix, so this list refreshes without a second invalidation rule to remember.

- [ ] **Step 4: The page**

Create `apps/customers/frontend/src/pages/follow-ups.tsx`:

```tsx
import { Anchor, Badge, Button, Card, Group, Pagination, Select, Stack, Table, Text } from "@mantine/core";
import { notifications } from "@mantine/notifications";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { ContentSkeleton, EmptyState, PageHeader, useI18n } from "@vantigo/frontend-shell";
import {
  FOLLOW_UP_ASSIGNEES,
  FOLLOW_UP_STATES,
  type FollowUpAssigneeFilter,
  type FollowUpRow,
  type FollowUpState,
  followUpsListParams,
  followUpsQueryOptions,
} from "../api/follow-ups";
import { markFollowUpDone, type TimelineFollowUp } from "../api/timeline";
import "../i18n";

/** Open and past its due date, in UTC — the same calendar the server compares in. */
const utcToday = () => new Date().toISOString().slice(0, 10);
const isOverdue = (followUp: TimelineFollowUp) => !followUp.doneAt && followUp.dueOn < utcToday();

interface FollowUpsSearch {
  page: number;
  assignee: FollowUpAssigneeFilter;
  state: FollowUpState;
  customerId?: number;
}

/**
 * "What is on my plate" (follow-ups design D3): every follow-up the caller
 * asked for, across customers, oldest due date first.
 *
 * Both filters live in the URL rather than in component state, which is what
 * makes a filtered list a link somebody can send — the customer list's own
 * rule. `canManageTimeline` comes from the host's `customers:timeline-manage`
 * check; without it the rows are still readable and the Done tick is not there.
 */
export const FollowUpsPage = ({ canManageTimeline }: { canManageTimeline?: boolean }) => {
  const { t, formatters } = useI18n("customers");
  const search = useSearch({ strict: false }) as FollowUpsSearch;
  const navigate = useNavigate() as (options: unknown) => void;
  const queryClient = useQueryClient();
  const params = followUpsListParams(search);
  const { data, isPending, isError } = useQuery(followUpsQueryOptions(params));

  const go = (next: Partial<FollowUpsSearch>) =>
    navigate({ search: { ...search, page: 1, ...next } });

  const tick = useMutation({
    mutationFn: (row: FollowUpRow) => markFollowUpDone(row.customerId, row.entryId),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["customers"] }),
    onError: (error: Error) =>
      notifications.show({ color: "red", title: t("couldNotUpdateFollowUp"), message: error.message }),
  });

  const formatDateOnly = (date: string) =>
    formatters.formatDate(`${date}T00:00:00Z`, { dateStyle: "medium", timeZone: "UTC" });

  return (
    <Stack gap="lg">
      <PageHeader title={t("followUps")} description={t("followUpsDescription")} />
      <Card withBorder padding="lg" radius="md">
        <Stack gap="md">
          <Group gap="sm" wrap="wrap">
            <Select
              label={t("followUpAssigneeFilter")}
              data={FOLLOW_UP_ASSIGNEES.map((value) => ({ value, label: t(`followUpAssignee_${value}`) }))}
              value={params.assignee}
              onChange={(value) => value && go({ assignee: value as FollowUpAssigneeFilter })}
              allowDeselect={false}
            />
            <Select
              label={t("followUpStateFilter")}
              data={FOLLOW_UP_STATES.map((value) => ({ value, label: t(`followUpState_${value}`) }))}
              value={params.state}
              onChange={(value) => value && go({ state: value as FollowUpState })}
              allowDeselect={false}
            />
          </Group>
          {isPending ? (
            <ContentSkeleton rows={4} />
          ) : isError ? (
            <Text c="red">{t("couldNotLoadFollowUps")}</Text>
          ) : data.data.length === 0 ? (
            <EmptyState title={t("noFollowUps")} />
          ) : (
            <Table highlightOnHover>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>{t("dueOn")}</Table.Th>
                  <Table.Th>{t("customer")}</Table.Th>
                  <Table.Th>{t("description")}</Table.Th>
                  <Table.Th>{t("followUpAssignee")}</Table.Th>
                  <Table.Th>{t("actions")}</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {data.data.map((row) => (
                  <Table.Tr key={`${row.customerId}-${row.entryId}`}>
                    <Table.Td>
                      <Group gap="xs">
                        <Text size="sm" c={row.followUp.doneAt ? "dimmed" : isOverdue(row.followUp) ? "red" : undefined}>
                          {formatDateOnly(row.followUp.dueOn)}
                        </Text>
                        {isOverdue(row.followUp) && (
                          <Badge size="xs" color="red" variant="light">
                            {t("followUpOverdue")}
                          </Badge>
                        )}
                        {row.followUp.doneAt && (
                          <Badge size="xs" color="gray" variant="light">
                            {t("followUpDone")}
                          </Badge>
                        )}
                      </Group>
                    </Table.Td>
                    <Table.Td>
                      <Anchor component={Link} to="/customers/$customerId" params={{ customerId: row.customerId }}>
                        {row.customerName}
                      </Anchor>
                    </Table.Td>
                    <Table.Td>
                      <Text size="sm" lineClamp={2}>
                        {row.note || t("noAdditionalDetails")}
                      </Text>
                    </Table.Td>
                    <Table.Td>
                      <Text size="sm" c={row.followUp.assignee ? undefined : "dimmed"}>
                        {row.followUp.assignee
                          ? `${row.followUp.assignee.displayName}${row.followUp.assignee.active ? "" : ` (${t("inactiveUser")})`}`
                          : t("followUpUnassigned")}
                      </Text>
                    </Table.Td>
                    <Table.Td>
                      {canManageTimeline && !row.followUp.doneAt && (
                        <Button
                          size="compact-xs"
                          variant="light"
                          loading={tick.isPending && tick.variables?.entryId === row.entryId}
                          onClick={() => tick.mutate(row)}
                        >
                          {t("markDone")}
                        </Button>
                      )}
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          )}
          {data && data.pagination.totalPages > 1 && (
            <Group justify="center">
              <Pagination
                total={data.pagination.totalPages}
                value={params.page}
                onChange={(page) => navigate({ search: { ...search, page } })}
              />
            </Group>
          )}
        </Stack>
      </Card>
    </Stack>
  );
};
```
The row's customer link goes to the customer's page — which is where the timeline entry is — rather than to the entry: there is no per-entry route, and inventing one for this page would be a second way to reach the same card.

- [ ] **Step 5: The host route, its search params and its capability**

Create `apps/host/frontend/src/routes/customers/follow-ups.tsx`:

```tsx
import { createFileRoute } from "@tanstack/react-router";
import { followUpsListParams, followUpsQueryOptions } from "@vantigo/customers-ui/api/follow-ups";
import { FollowUpsTab } from "./-follow-ups-page";

const assignees = ["me", "none"] as const;
const states = ["open", "overdue", "done", "all"] as const;

/**
 * Both filters are URL search params (follow-ups design D3), so a filtered list
 * is a link. Anything the API would refuse is dropped here rather than
 * forwarded: an unknown state falls back to the default rather than narrowing
 * the rows by something the page's Select cannot show, and 'Me' is not 'me'
 * because the API matches case-sensitively.
 */
export const Route = createFileRoute("/customers/follow-ups")({
  validateSearch: (search: Record<string, unknown>) => ({
    page: Math.max(1, Number(search.page) || 1),
    assignee: assignees.includes(search.assignee as (typeof assignees)[number])
      ? (search.assignee as (typeof assignees)[number])
      : ("me" as const),
    state: states.includes(search.state as (typeof states)[number])
      ? (search.state as (typeof states)[number])
      : ("open" as const),
    customerId: Number(search.customerId) > 0 ? Number(search.customerId) : undefined,
  }),
  loaderDeps: ({ search }) => followUpsListParams(search),
  loader: ({ context: { queryClient }, deps }) => queryClient.ensureQueryData(followUpsQueryOptions(deps)),
  component: FollowUpsTab,
});
```

Create `apps/host/frontend/src/routes/customers/-follow-ups-page.tsx`:

```tsx
import { useQuery } from "@tanstack/react-query";
import { FollowUpsPage } from "@vantigo/customers-ui/pages/follow-ups";
import { fetchSession, sessionQueryKey } from "../../api/auth";
import { getAuthorizationMe } from "../../api/authorization";
import { hasPermissions } from "../../navigation";

/**
 * The Follow-ups page with its one capability: `canManageTimeline`
 * (`customers:timeline-manage`, follow-ups design D3), which decides whether
 * each row carries a Done tick. Computed here, from the caller's permissions,
 * the same way `-customer-overview-tab.tsx` computes its four — the host reads
 * permissions, the package never fetches them itself.
 *
 * Lives beside the route file rather than inside it for the same reason that
 * one does: the route file may export nothing but its `Route` without costing
 * the bundle a code split.
 */
export const FollowUpsTab = () => {
  // The same keys the root layout uses, so this reads its cache.
  const session = useQuery({ queryKey: sessionQueryKey, queryFn: fetchSession, staleTime: 300_000 });
  const authorization = useQuery({
    queryKey: ["authorization", "me", "none"],
    queryFn: getAuthorizationMe,
    enabled: !!session.data,
    retry: false,
    staleTime: 300_000,
  });
  return <FollowUpsPage canManageTimeline={hasPermissions(authorization.data?.permissions, ["customers:timeline-manage"])} />;
};
```

And `-customer-overview-tab.tsx` gains the same derivation, passed to `CustomerOverview`:

```tsx
      canManageTimeline={hasPermissions(authorization.data?.permissions, ["customers:timeline-manage"])}
```
with a sentence added to its doc comment:
```
 * `canManageTimeline` (`customers:timeline-manage`, follow-ups design D3) is the
 * newest of them and the one that closes a gap rather than opening one: the
 * timeline card's Add, Edit and Delete controls were server-enforced only, so a
 * reader saw buttons that answered 403.
```

- [ ] **Step 6: The nav entry**

In `apps/host/frontend/src/apps.ts`, add a third item to the customers app, after Contacts:

```ts
    {
      // Follow-ups is offered on customers:timeline-view alone, the permission
      // the page's own API needs; the Done tick inside it asks for
      // customers:timeline-manage separately (follow-ups design D3).
      label: "navigation.followUps",
      to: "/customers/follow-ups",
      icon: IconFlag,
      requiredPermissions: ["customers:timeline-view"],
    },
```
`IconFlag` joins the `@tabler/icons-react` import at the top of the file. The entry has no `searchStrategy`: the two existing strategies reset a `page`/`search` pair, and this page's own defaults (`assignee: me`, `state: open`) come from its `validateSearch`, so a bare `/customers/follow-ups` already lands on the right list.

In `apps/host/frontend/src/catalogs/navigation.ts`, after `navigation.contacts` in both objects:

```ts
  "navigation.followUps": "Follow-ups",
```
```ts
  "navigation.followUps": "Oppfølginger",
```

- [ ] **Step 7: The dashboard's two sentences**

In `apps/host/frontend/src/catalogs/dashboard.ts`, after `dashboard.customerRegistryRenamed` in both objects:

```ts
  "dashboard.customerFollowUpOverdue": "Follow-up overdue for {{name}}",
  "dashboard.customerFollowUpDue": "Follow-up due today for {{name}}",
```
```ts
  "dashboard.customerFollowUpOverdue": "Forfalt oppfølging for {{name}}",
  "dashboard.customerFollowUpDue": "Oppfølging med frist i dag for {{name}}",
```

In `apps/host/frontend/src/routes/dashboard.tsx`, `customerAttentionTitleKeys` gains the two types:

```tsx
const customerAttentionTitleKeys: Record<string, string> = {
  registryBankrupt: "dashboard.customerRegistryBankrupt",
  registryLiquidation: "dashboard.customerRegistryLiquidation",
  registryDeleted: "dashboard.customerRegistryDeleted",
  registryRenamed: "dashboard.customerRegistryRenamed",
  followUpOverdue: "dashboard.customerFollowUpOverdue",
  followUpDue: "dashboard.customerFollowUpDue",
};
```
and its comment gains a sentence:
```tsx
// Six now: the four registry signals (Brreg in full design D4) and the two
// follow-up ones (follow-ups design D2). All six are server-built titles
// carrying the customer's own name, so all six take the same `{{name}}`
// treatment in attentionTitle below.
```
`attentionHref` needs **no** change: a follow-up item's `entityId` is the customer id, so `/customers/${entityId}` is already right — which is why design D2 chose the customer id over the entry id. Add a sentence saying so, in `attentionHref`'s own comment:
```tsx
  // Customers' six types all carry a customer id, follow-ups included: a
  // follow-up lives on a timeline entry, and the entry lives on the customer's
  // page, which is the only page that can show it.
```

- [ ] **Step 8: The package's catalogs, the route tree, and the run**

Add to `apps/customers/frontend/src/i18n.ts`, `en` then `nb`:

```ts
  followUps: "Follow-ups",
  followUpsDescription: "What you have promised to do next, across your customers.",
  followUpAssigneeFilter: "Assigned to",
  followUpStateFilter: "State",
  followUpAssignee_me: "Me",
  followUpAssignee_none: "Unassigned",
  followUpState_open: "Open",
  followUpState_overdue: "Overdue",
  followUpState_done: "Done",
  followUpState_all: "All",
  followUpDone: "Done",
  noFollowUps: "No follow-ups here.",
  couldNotLoadFollowUps: "Could not load follow-ups",
  dueOn: "Due",
```
```ts
  followUps: "Oppfølginger",
  followUpsDescription: "Det du har lovet å gjøre videre, på tvers av kundene dine.",
  followUpAssigneeFilter: "Tildelt",
  followUpStateFilter: "Status",
  followUpAssignee_me: "Meg",
  followUpAssignee_none: "Ikke tildelt",
  followUpState_open: "Åpne",
  followUpState_overdue: "Forfalt",
  followUpState_done: "Utført",
  followUpState_all: "Alle",
  followUpDone: "Utført",
  noFollowUps: "Ingen oppfølginger her.",
  couldNotLoadFollowUps: "Kunne ikke laste oppfølginger",
  dueOn: "Frist",
```
`customer`, `actions`, `description` and `noAdditionalDetails` are **already** in both catalogs and the page reuses them as they are — they are deliberately not in the listing above, because a duplicate key in the same object literal is a silent overwrite rather than an error. Before adding the block, `grep -n '^  dueOn:\|^  followUp' src/i18n.ts` to confirm none of the new keys collides either; if one does, the page reuses the existing key instead of redefining it.

```bash
cd /home/anders/projects/vantigo/vantigo
mise exec -- bun run --cwd apps/host/frontend build   # regenerates routeTree.gen.ts
mise exec -- bun run --cwd apps/customers/frontend test
mise exec -- bun run --cwd apps/host/frontend test
mise exec -- bun run translations:check && mise exec -- bun run i18n:test
bunx biome check apps/customers/frontend/src apps/host/frontend/src
mise run frontend:check
```
Run `build` twice if `tsc` fails on a stale tree the first time — the Vite plugin regenerates `routeTree.gen.ts` during `vite build`, so the first run can typecheck against the old one.

- [ ] **Step 9: Commit**

```bash
cd /home/anders/projects/vantigo/vantigo
printf '%s\n\n%s\n\n%s\n' 'feat(customers-ui): a Follow-ups page, and two dashboard signals that say a date has passed' 'GET /customers/follow-ups gets its page: both filters in the URL so a filtered list is a link, rows linking to the customer whose timeline holds the entry, and a Done tick behind customers:timeline-manage. The host adds the sidebar entry on customers:timeline-view, the route with its search validator, and the capability prop — which the customer page'"'"'s Overview tab now derives too. The dashboard names followUpOverdue and followUpDue in both languages; the link needed nothing, because the item carries the customer id.' 'Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>' > /tmp/msg-followups-task6
git add apps/customers/frontend/src/api/follow-ups.ts apps/customers/frontend/src/pages/follow-ups.tsx apps/customers/frontend/src/pages/follow-ups.test.tsx apps/customers/frontend/src/i18n.ts apps/host/frontend/src/apps.ts apps/host/frontend/src/apps.test.ts apps/host/frontend/src/catalogs/navigation.ts apps/host/frontend/src/catalogs/dashboard.ts apps/host/frontend/src/routes/dashboard.tsx apps/host/frontend/src/routes/dashboard.test.ts apps/host/frontend/src/routes/customers/follow-ups.tsx apps/host/frontend/src/routes/customers/-follow-ups-page.tsx apps/host/frontend/src/routes/customers/follow-ups-route.test.tsx apps/host/frontend/src/routes/customers/-customer-overview-tab.tsx apps/host/frontend/src/routes/customers/customer-overview-route.test.tsx apps/host/frontend/src/routeTree.gen.ts
git commit -F /tmp/msg-followups-task6 -- $(git diff --cached --name-only)
git show --stat HEAD && git status --short
```

- [ ] **Step 10: Show the new tests can fail**

Three mutations, by hand:
1. Change the page's `go` helper to `setState` on local state instead of `navigate` (comment out the `navigate` call and keep the query reading `params`), and run `follow-ups.test.tsx` — expect "puts a filter change in the URL" to fail with `navigate` never called. Restore, green.
2. Drop `"customers:timeline-view"` from the nav entry's `requiredPermissions` and run `apps.test.ts` — expect the `requiredPermissions` assertion to fail. Restore, green.
3. Remove `followUpOverdue` from `customerAttentionTitleKeys` and run `dashboard.test.ts` — expect `attentionTitleKey(...) = undefined`. Restore, green.

Note all three in the report.

---

### Task 7: Documentation (D1–D5)

**Files:**
- Modify: `docs/customers.md`, `ROADMAP.md`
- Read first (do not change): `docs/customers.md:520-620` (§Owner and tags — the voice a whole new section is written in), `:621-725` (§The timeline, which gains two paragraphs), `:1049-1097` (§Attention items, whose table gains two rows), `:1526-1577` (§Permissions), `:1765-1812` (§API)

- [ ] **Step 1: A Follow-ups section**

Insert a new `## Follow-ups` section in `docs/customers.md` immediately after `## The timeline` ends and before `## The list endpoint and search` (line ~726):

```markdown
## Follow-ups

A **manual, active** timeline entry can carry a follow-up: a due date, optionally
an assignee, and a stamp once it is done. An interaction ("called about the
renewal") or a note is exactly where "call back on Friday" belongs, and a
generated event never asks anyone to do anything — so a generated entry cannot
carry one and no endpoint offers to give it one.

It lives on the entry: `customers.customers_timeline_entries.follow_up_on`,
`.follow_up_assignee_user_id`, `.follow_up_done_at` (migration `00026`), and the
same three columns on `customers_timeline_entries_revisions`, so the history
stays point-in-time — a revision taken before somebody moved the date still says
what it said. Being on the entry is also what lets it share the entry's own
`current_revision`: setting, replacing and clearing a follow-up is the timeline's
existing `PUT` with three more columns and no second concurrency story.

There is deliberately **no foreign key** from `follow_up_assignee_user_id` to
`identity.users`, for migration `00024`'s own two reasons: this module may not
read identity's schema at all (`contracts.UserDirectory` is the only sanctioned
seam), and an assignee disabled or removed afterwards **keeps** the follow-up,
shown inactive or as `Unknown user`. Nothing is silently reassigned.

### Setting one

`POST /customers/{id}/timeline` and `PUT .../timeline/{entryId}` take
`followUp: {dueOn, assigneeUserId?}`.

- `dueOn` is a strict `yyyy-MM-dd` and **may be in the future** — which is the
  one thing it does that `occurredOn` may not, and the whole point of a
  follow-up.
- `assigneeUserId` must name an existing, **active** user, or the request is
  refused with a field error on `followUp.assigneeUserId`, worded as the owner's
  own (`User {id} does not exist` / `User {id} is disabled and cannot be given a
  follow-up`). The check is a directory call and is made **before** the
  transaction opens, like every other directory call in this module. Omitting it
  leaves the follow-up **unassigned**, which is everyone's rather than nobody's
  (see the attention list below).
- On a `PUT`, an omitted or `null` `followUp` **clears** it — that PUT is a full
  replace, as it already is for `occurredAt` and `sourceUrl` — and **clearing a
  follow-up clears its done state**, because done-ness without a follow-up is not
  a state this module has. Keeping the follow-up while editing the note leaves
  the done stamp alone: editing a ticked follow-up must not un-tick it.

### Ticking one

`POST .../timeline/{entryId}/follow-up/done` and `DELETE .../follow-up/done` mark
it done and reopen it. Both are **idempotent** — ticking an already-done
follow-up answers 200 with the entry unchanged and writes nothing at all, the
module's own no-op rule — and both answer the whole entry, so a tick from a list
can read the fresh revision straight off the response.

Neither takes an `expectedRevision`, and that is deliberate: a tick comes from a
list, or from an entry line loaded minutes ago, and must not lose a race with
somebody editing the note. What takes its place is the guarded `UPDATE`'s own
`WHERE follow_up_done_at IS NULL` (respectively `IS NOT NULL`): two concurrent
ticks serialize on the row, the loser matches no row, re-reads, and answers 200
with what is now true. Answering 409 there would be telling the caller that a
follow-up they can see is done is not done.

A tick that **changes** something is still a revision of the entry:
`current_revision` bumps and a revision row records who ticked it, which is the
reason it is a revision at all rather than a quiet column write — and it is where
"who ticked this, and when" is answered. There is no `doneBy` on the wire: the
revision that set `doneAt` **is** the tick, so the revision history's own actor
and timestamp beside it say who and when, and the entry's own response stays the
three fields `followUp` has. 404 when the
entry carries no follow-up (the thing addressed does not exist); 409 `Timeline
entry is immutable` when the entry is generated, deleted or voided — the same
problem shape the entry's own PUT and DELETE answer.

### The Follow-ups page

`GET /customers/follow-ups` (`customers:timeline-view` + `customers:view`) is
"what is on my plate": paged (`page`, `pageSize` 1–100, default 25), sorted
`dueOn` ascending then entry id, each row `{entryId, customerId, customerName,
eventType, occurredOn, note, followUp}` with the note cut to its first **200
UTF-16 code units** (the same unit the entry's own 500-character summary is cut
in).

| Parameter | Values | Default |
| --- | --- | --- |
| `assignee` | a user id, `me` (resolved from the session, never sent) or `none` | `me` |
| `state` | `open`, `overdue` (a subset of `open`), `done`, `all` | `open` |
| `customerId` | one customer's follow-ups | all |

Archived customers' follow-ups are excluded **unless `state=done`**: a done
follow-up is a record of work finished, and an archived customer's finished work
is still finished. The frontend's own page offers only `me` and `none` for
`assignee`, the same choice the customer list's Owner filter makes — a uuid in
the URL would narrow the rows by something the Select cannot show.

### What is not built

No priorities, no recurrence, no reminders by mail or notification — the
attention list and the page **are** the reminder. No follow-up without a timeline
entry, none on a generated event, and no bulk reassignment.
```

- [ ] **Step 2: Two paragraphs in §The timeline**

At the end of the manual-event-types paragraph in `## The timeline` (after "…told apart by `provenance`, never by `eventType`.", line ~695), add:

```markdown
A manual entry can also carry a **follow-up** — a due date, an assignee and a
done stamp — which rides on this same create/update under the same
`expectedRevision`, and on two paths of its own that do not. See
[Follow-ups](#follow-ups). A generated entry never carries one.
```

And at the end of `### Authorship`'s bullet list, after the "Rows written before this branch" bullet, add:

```markdown
- A revision also carries the **follow-up as it stood** at that revision, which is
  what answers "who ticked this follow-up, and when": the revision that set
  `doneAt` is the tick, and that revision's own actor and `changedAt` name the
  person and the moment. There is deliberately no `doneBy` on the entry — the
  history already holds it, and one place is better than two that can disagree.
```

- [ ] **Step 3: Two rows in §Attention items**

In `### Attention items`, change the opening "Four types" sentence to:

```markdown
Six types. Four come from the stored registry record, **at most one per
customer**, in this fixed precedence (a struck-off company's single most useful
sentence is that it is deleted, whatever else is also true of it):
```

and, after the four-row table, add:

```markdown
Two more come from [follow-ups](#follow-ups), and they are the first items here
that depend on **who is asking** — the endpoint reads the principal from the
context, as time's and expenses' own items do:

| Type | Raised when | Clears when |
| --- | --- | --- |
| `followUpOverdue` | An open follow-up on a non-archived customer, assigned to the caller **or unassigned**, whose `dueOn` is before today (UTC) | It is ticked done, reopened onto a later date, cleared, its entry is deleted, or the customer is archived |
| `followUpDue` | The same, with `dueOn` equal to today | The same |

An **unassigned** follow-up is everyone's until somebody takes it, which is why
it reaches every caller's list; somebody else's assigned follow-up reaches
nobody's but theirs. For these two the item's `id` is `"<type>/<entryId>"`, but
`entityId` is still the **customer** id — the host links a `customers` attention
item to `/customers/{entityId}`, and the customer's page is where the entry is —
and `occurredAt` is `dueOn` at midnight UTC, so an overdue follow-up sorts by how
overdue it is rather than by when it was noticed.

The dashboard's two sentences (`apps/host/frontend/src/catalogs/dashboard.ts`):
"Follow-up overdue for {{name}}" and "Follow-up due today for {{name}}", in both
English and Norwegian.
```

- [ ] **Step 4: Permissions and the API table**

In `## Permissions`, replace the `GET /stats/attention` sentence of the "No new permission key was added for Registry record" paragraph and add a paragraph after the owner-and-tags one:

```markdown
No new permission key was added for [Follow-ups](#follow-ups) either: a follow-up
is part of a timeline entry, so setting, ticking and reopening one needs
`customers:timeline-manage` and reading one needs `customers:timeline-view`,
exactly as the entry itself does. The Follow-ups list needs
`customers:timeline-view` **and** `customers:view`, because its rows are timeline
data and each one names a customer. `GET /stats/attention` stays on plain
`customers:view` for the two follow-up items too: an item carries the customer's
own name and the fact that a date has passed, nothing more.

`GET /customers/assignable-users` moved from `customers:update` to
**`customers:view`** with this delivery. It answers the display names of active
users, which every timeline reader already sees on every entry as its author, and
a timeline writer has to be able to pick an assignee without holding the
customer-edit permission. There was nothing there for `customers:update` to
protect.
```

In `## API`, change "47 operations in total" to "50 operations in total", change the assignable-users row, and add three rows after the timeline rows:

```markdown
| `GET /assignable-users` | `customers:view` |
```
```markdown
| `POST /{id}/timeline/{entryId}/follow-up/done`, `DELETE /{id}/timeline/{entryId}/follow-up/done` | `customers:timeline-manage` + `customers:timeline-view` |
| `GET /follow-ups` | `customers:timeline-view` + `customers:view` |
```
and extend the `/stats/attention` note under the table:

```markdown
`/stats/attention` is no longer a stub: it answers the four [registry attention
items](#attention-items) and the two [follow-up](#follow-ups) ones, computed live
against each customer, not stored or cached. The follow-up pair is the one part
of this API whose answer depends on the calling user.
```
Verify the operation count by counting `operationId:` in `openapi/customers.yaml` rather than trusting the arithmetic: `grep -c 'operationId:' openapi/customers.yaml`.

- [ ] **Step 5: ROADMAP**

In `ROADMAP.md`, change the phase 4 heading (line 139) and add delivery C after delivery B's paragraph (line ~177), replacing the "Still ahead in this phase" paragraph's first clause:

```markdown
### Phase 4 — Light CRM (three deliveries done)
```
```markdown
**Delivery C (done)** — decided in
[`docs/superpowers/specs/2026-09-23-customers-follow-ups-design.md`](docs/superpowers/specs/2026-09-23-customers-follow-ups-design.md):
a manual timeline entry carries a **follow-up** — a due date that may be in the
future and an optional assignee, on the entry itself (migration `00026`) and on
its revisions, so history stays point-in-time. Ticking it done and reopening it
are two paths that take no `expectedRevision`, because a tick comes from a list
and must not lose a race with an edit of the note; both are idempotent and each
real change is still a revision naming who ticked it. Due and overdue follow-ups
reach `/stats/attention` — the first items there that depend on who is asking,
reporting the caller's and unassigned ones only — and `GET /customers/follow-ups`
answers a **Follow-ups** page, defaulting to "my open ones". The timeline card
gains the section, the line and the tick, and a new `canManageTimeline`
capability prop stops a reader seeing controls that used to 403. The same
delivery removed the contact association's deprecated `role` alias while nothing
was live (`title` is the only name the contract has) and relaxed
`GET /customers/assignable-users` to `customers:view`. See
[`docs/customers.md#follow-ups`](docs/customers.md#follow-ups).

**Still ahead in this phase:** customer groups that can carry defaults;
attachments on a customer and its timeline entries, once the storage module has a
model for it. Also left for later on purpose: the tag vocabulary is **unpaged**
```
(keep the rest of that paragraph exactly as it is, from "(`GET /customers/tags` answers all of it" onward).

- [ ] **Step 6: Commit**

```bash
cd /home/anders/projects/vantigo/vantigo
printf '%s\n\n%s\n\n%s\n' 'docs(customers): follow-ups, and what the two new attention types mean' 'A Follow-ups section covering where a follow-up lives and why, what setting and clearing one does to the done state, why the two done paths take no expected revision, and the list with its filters. The attention section gains the two caller-dependent types, the permissions section explains why no key was added and why assignable-users relaxed, and the API table gains three operations. ROADMAP marks phase 4 delivery C done.' 'Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>' > /tmp/msg-followups-task7
git add docs/customers.md ROADMAP.md
git commit -F /tmp/msg-followups-task7 -- $(git diff --cached --name-only)
git show --stat HEAD && git status --short
```

---

### Task 8: Verify the whole branch and open the PR

**Files:** none. This task changes nothing; it proves what the branch does and hands it over.

- [ ] **Step 1: No generation drift**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server && mise exec -- go generate ./... && git status --short
cd /home/anders/projects/vantigo/vantigo && mise exec -- bun run gen:client && git status --short
cd /home/anders/projects/vantigo/vantigo/apps/server && mise exec -- go run ./internal/openapi/cmd/contract coverage -corpus ../../openapi/testdata/exchanges -out ../../openapi/COVERAGE.md
cd /home/anders/projects/vantigo/vantigo && git status --short
```
Expected: nothing (the untracked root `go.mod`/`go.sum` aside — those are not ours). A tracked file that changes here means a committed generated file was stale.

- [ ] **Step 2: The frozen corpus is untouched**

```bash
cd /home/anders/projects/vantigo/vantigo
git diff origin/main..HEAD --stat -- openapi/testdata/exchanges/
```
Expected: **empty output**. If it is not, the branch edited frozen evidence and the fix is to restore that file from `origin/main` and make the schema accommodate the corpus instead.

- [ ] **Step 3: The whole Go suite**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
mise exec -- gofmt -l . | head
mise exec -- go vet ./... && mise exec -- golangci-lint run && mise exec -- go test -count=1 ./...
```
`golangci-lint run` is run **from `apps/server`**, with no package argument. `gofmt -l` must print nothing.

- [ ] **Step 4: The race detector, on four CPUs**

```bash
cd /home/anders/projects/vantigo/vantigo/apps/server
CC="$(command -v zig >/dev/null && echo "$PWD/../../scripts/zig-cc" || echo cc)" CGO_ENABLED=1 \
  taskset -c 0-3 mise exec -- go test -race -count=1 ./internal/customers/... ./internal/db/... ./internal/openapi/... ./internal/module/...
```
If the repo's zig-cc wrapper is at another path, use that one (`ls scripts/`); if `go test -race` refuses for want of a C compiler, that wrapper is what supplies it. Then run the tick race harder, since a database row lock is not something `-race` can see:

```bash
taskset -c 0-3 mise exec -- go test -count=20 -run 'TestFollowUpDone_ConcurrentTicks|TestPutCustomersByIdTimelineByEntryId_ConcurrentUpdates' ./internal/customers/
```

- [ ] **Step 5: The whole frontend**

```bash
cd /home/anders/projects/vantigo/vantigo
mise exec -- bun run --cwd apps/customers/frontend test
mise exec -- bun run --cwd apps/host/frontend test
mise exec -- bun run translations:check && mise exec -- bun run i18n:test
mise run frontend:check
bunx biome check .
```

- [ ] **Step 6: Check the trailers and the branch state**

```bash
cd /home/anders/projects/vantigo/vantigo
git log --format='%h %s%n%b' origin/main..HEAD | grep -c 'Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>'
git log --oneline origin/main..HEAD
git status --short
gh run list --branch main --limit 3
```
The count must equal the number of commits on the branch — the design commit `1d83efe` and the other agent's D6 (GHAS) commit included, both of which already carry the trailer. `gh run list` on `main` first, so a failure that was already there is not mistaken for one this branch caused.

**The GHAS commit is a second agent's work on this branch.** Confirm it is present (`git log --oneline origin/main..HEAD | grep -i 'code-scanning\|cookie\|argon\|secrets'`) before opening the PR, since the spec puts it in the same PR. If it is not there yet, wait for it rather than opening a PR that claims it.

- [ ] **Step 7: Push and open the PR**

```bash
cd /home/anders/projects/vantigo/vantigo
git push -u origin feat/customers-followups
gh pr create --base main --head feat/customers-followups --title 'feat(customers): follow-ups on the timeline, and the role alias removed' --body-file /tmp/pr-followups.md
```

Write `/tmp/pr-followups.md` first. It covers:

- **What shipped:** the three columns on the entry and its revisions with their two partial indexes and no foreign key; `followUp` on the manual request, the entry response and the revision response; the two idempotent done paths and why they take no expected revision; the two caller-dependent attention types; `GET /customers/follow-ups` with its three filters and the archived rule; the `UserPicker` generalisation with `OwnerPicker` as a wrapper; the Follow-up section, the follow-up line and the Done/Reopen control; the Follow-ups page, its nav entry and its route; `canManageTimeline` on both the timeline card and the page; both catalogs everywhere; the `role` removal and the `assignable-users` relaxation.
- **The decisions taken while implementing**, each with its reason:
  1. **Idempotent means "no-op", not "write again".** A second tick answers 200 with the entry and writes nothing — no column change, no revision row, no actor resolved. The alternative (re-stamping `follow_up_done_at` and appending a revision every time) would fill a history with entries recording that nothing happened.
  2. **An omitted `followUp` on a PUT clears it**, because that PUT is already a full replace (`occurredAt` and `sourceUrl` behave the same), which is what made `null` and omitted the same instruction and avoided a three-valued field.
  3. `TimelineFollowUpAssignee` is its **own schema** rather than a reuse of `CustomerOwner`, whose three fields are identical: an owner is accountable for a relationship, an assignee is expected to do one thing by one date, and a shared schema would have made the two impossible to document apart. Reusing it would also have renamed a generated Go type that `owner.go` uses throughout.
  4. **"Who ticked it and when" is answered by the revision panel, not by a `doneBy` field.** D4 asked for it on the entry line; D1 fixes the response shape as `{dueOn, assignee?, doneAt?}`. The revision that set `doneAt` **is** the tick, so the panel showing each revision's follow-up beside its existing actor — now with its `changedAt` — says who and when without adding a field the contract does not have. D4's sentence in the spec was amended to say so.
  5. **Offset paging, not a keyset cursor**, for `GET /customers/follow-ups`: the design asks for `page`/`pageSize` by name, the frontend's Pagination control needs a total page count, and `GET /customers` and `GET /customers/contacts` both answer that shape already. The timeline's own feed stays keyset because an infinite scroll has no page numbers.
  6. **Both done paths answer `TimelineResponse` and carry `customers:timeline-view`** alongside `-manage`, rather than answering 204 on `-manage` alone like the entry DELETE: a tick from a list has to be able to update the row's `currentRevision` in place, or the next edit 409s on a revision the client never saw move.
  7. `KnownServeMuxConflicts` needed **no** new pin — state whether the test agreed, and if it did not, which pairs were added.
  8. The **revision history stays inside the timeline card's menu**, so it disappears for a reader along with Edit and Delete. A menu with one item is worse than no menu; a reader who needs the history has the API.
  9. `state=done` is the one filter that shows an **archived** customer's follow-ups, because finished work stays finished.
  10. The contact events' **payload version is 2** now that `role` left the payload, so a reader can tell the two shapes apart; entries already written keep theirs.
- **What was proved able to fail:** the mutations listed in Tasks 1, 2, 4, 5 and 6's final steps, and what each failure printed — including whether the `isOverdue` boundary mutation needed a new fixture to be caught, stated honestly either way.
- **What is NOT in this PR from me:** the five code-scanning alerts (D6) are a second agent's commit on the same branch; name it by hash and say so, rather than claiming it.
- Nothing in scope crept: no priorities, no recurrence, no e-mail or notification reminders, no follow-up without an entry, none on a generated event, no bulk reassignment, no customer groups.

End the body with:

```
🤖 Generated with [Claude Code](https://claude.com/claude-code)
```

- [ ] **Step 8: Watch the checks and fix root causes on the same PR**

```bash
cd /home/anders/projects/vantigo/vantigo && gh pr checks --watch
```
Use the default, non-JSON output. Fix any failure at its root on this branch — never by relaxing a test or an assertion — and push again. **Never merge**: the user merges.

If the PR description needs editing afterwards, `gh pr edit` is broken in this environment; use `gh api --method PATCH repos/{owner}/{repo}/pulls/{number} -f body=@/tmp/pr-followups.md` instead.

---

## Self-review notes

Checked against the spec, section by section:

- **D1 — a follow-up is part of a manual timeline entry.** The three columns on both tables, migration `00026`: Task 2. `followUp` on POST and PUT, `dueOn` strict and future-allowed, the assignee validated against the directory (field error on `followUp.assigneeUserId`, worded as the owner's), an assignee disabled afterwards keeping it, clearing also clearing done: Tasks 3 (contract) and 4 (Steps 4 and 7), pinned by `TestPostTimeline_CarriesAFollowUpAndAcceptsAFutureDate`, `TestPostTimeline_RefusesAMalformedDueDateAndAnUnusableAssignee`, `TestPutTimeline_ReplacesTheFollowUpAndClearingItClearsDone` and `TestFollowUp_AnAssigneeDisabledOrForgottenAfterwardsKeepsIt`. The two done paths, idempotent, no expected revision, each real change a revision with the ticker's name, 409 for a non-manual/non-active entry and 404 for one with no follow-up: Task 4 Steps 5 and 10, pinned by `TestFollowUpDone_IsIdempotentAndEachRealChangeIsARevision`, `TestFollowUpDone_RefusesWhatHasNoFollowUpAndWhatIsNotManual` and `TestFollowUpDone_ConcurrentTicks_BothSucceedAndOnlyOneRevisionIsWritten`. `followUp` on the entry and revision responses, assignee names from one batched directory call outside any transaction, `Unknown user` for a vanished id: Task 4 Steps 4 and 7. The permissions, and the `assignable-users` relaxation to `customers:view`: Task 3 Step 5/Step 6 and Task 1 Steps 3-4, pinned by `TestFollowUpDone_NeedsTimelineManage`, `TestGetFollowUps_NeedsBothDoors` and `TestGetAssignableUsers_NeedsOnlyCustomersView`.
- **D2 — due and overdue follow-ups are attention.** Two types, computed from state, open follow-ups on non-archived customers assigned to the caller or unassigned, the UTC split, the item's id/entityId/title/occurredAt shape, the caller read from the context: Task 4 Steps 6 and 8, pinned by `TestStatsAttention_ReportsTheCallersAndUnassignedFollowUpsOnly` including the stranger's view. The host catalog's two sentences in en and nb, and the title key mapping: Task 6 Step 7, pinned in `dashboard.test.ts`. `attentionHref` needed no change and the plan says why.
- **D3 — a Follow-ups page.** The operation with its permissions, paging, three filters, ordering, row shape and 200-unit note, and the archived rule: Tasks 3 Step 6 and 4 Step 6, pinned by `TestGetFollowUps_DefaultsToMyOpenOnes`, `TestGetFollowUps_EveryFilterAndThePageBoundary`, `TestGetFollowUps_CutsTheNoteAtTwoHundredUTF16Units` and `TestGetFollowUps_RefusesAnUnusableQuery`. The nav entry, the route with its search params, rows linking to the customer, the per-row Done tick, and `canManageTimeline` on both the page and the customer page's timeline: Task 6, pinned by `follow-ups.test.tsx`, `follow-ups-route.test.tsx`, `apps.test.ts` and `customer-overview-route.test.tsx`.
- **D4 — the timeline card.** The Follow-up section with a `DateInput` that allows a future date and a `UserPicker` fed by the same assignable-users search with the current assignee kept in the options; the follow-up line, red and "overdue" when past, grey and struck through when done; the Done/Reopen control behind `canManageTimeline`: Task 5 Steps 3, 4 and 5. The revisions view showing the follow-up per revision, which is also where "who ticked it and when" is answered (the revision that set `doneAt` is the tick, beside that revision's own actor and `changedAt`): Task 5 Step 6. Pinned by the six cases in "the timeline's follow-ups" — including the Reopen case and the revision-panel case — and by `TestTimelineRevisions_CarryTheFollowUpPerRevision` on the server side. D1's response shape stands unchanged (no `doneBy`), and D4's sentence in the spec was amended to say where the "who" comes from.
- **D5 — the contract break.** `role` removed from all four schemas, `validateContactRole` and the alias handling gone, the payload version bumped with `role` dropped from new payloads, the frontend's `titleOf` gone, the corpus not edited and its test green, the docs section replaced: Task 1 in full, pinned by `TestAssociationRequests_TitleIsTheOnlyNameForTheFreeText` and by `TestRecordedExchangesMatchTheContract`.
- **D6 — the code-scanning alerts.** Deliberately absent: another agent is committing them on this branch, and Task 8 Step 6 checks the commit is there before the PR is opened.
- **Testing section.** Every case it names has a test, listed above. The one it names that this plan places differently: "the assignable-users relaxation" is tested in `owner_test.go` (where that endpoint's other tests are) rather than in `follow_ups_test.go`.
- **Out of scope.** No task adds recurrence, priorities, e-mail or notification reminders, follow-ups on generated events, bulk reassignment, a follow-up without an entry, or customer groups.

Type consistency, checked name by name:

- `parsedFollowUp{DueOn time.Time; AssigneeID *uuid.UUID}` is the only Go type for a validated follow-up; `parsedManualTimeline.FollowUp` is `*parsedFollowUp` and nil always means "none", never "unchanged".
- `followUpDecoration{assignees map[uuid.UUID]contracts.UserEntry}` is the only decoration type; `timelineResponse`, `timelineRevisionResponse` and `followUpResponse` all take it, and `decorateFollowUpAssignees` is the only thing that builds one.
- `followUpResponse(on pgtype.Date, assigneeID *uuid.UUID, doneAt *time.Time, dec)` is the single projection, used by the entry, the revision and the list row — which is why it takes three columns rather than a row type.
- `followUpOutcome{Entry *gen.TimelineResponse; Problem *apicommon.ProblemDetails; Missing bool}` is `markFollowUp`'s only answer shape, and both handlers read exactly those three fields.
- Store names used: `SetTimelineEntryFollowUpDoneParams{ID, CustomerID, Now}`, `ClearTimelineEntryFollowUpDoneParams{ID, CustomerID, Now}`, `FollowUpAttentionCandidatesParams{Today, CallerID}`, `CountCustomerFollowUpsParams`/`ListCustomerFollowUpsParams{FollowUpState, Today, AssigneeNone, AssigneeID, CustomerID(, PageSize, RowOffset)}`, and `FollowUpOn`/`FollowUpAssigneeUserID`/`FollowUpDoneAt` on every row type — each to be confirmed against the generated file in Task 2 Step 8 and followed where it differs.
- On the frontend, `TimelineFollowUp{dueOn: string; assignee: TimelineAssignee | null; doneAt: string | null}` is what every component reads (never optional, because `normalizeFollowUp` fills it), `TimelineFollowUpInput{dueOn: string; assigneeUserId?: string}` is what every request sends, `normalizeFollowUp` is the only absent-to-null boundary, `isOverdue`/`followUpTone` are defined twice — once in `-customer-timeline.tsx` and once in `follow-ups.tsx` — deliberately, because the two render different elements from the same rule and a shared helper would have been a `lib/` module for four lines; **if a third caller appears, extract it**.
- `UserPicker` is the only directory `Select` in this package; `OwnerPicker` is its only wrapper; `assignableUsersQueryOptions` is unchanged and still the only query behind either.
- `canManageTimeline` is spelled the same in `CustomerTimeline`, `CustomerOverview`, `FollowUpsPage`, `CustomerOverviewTab` and `FollowUpsTab`, and is optional (defaulting to withheld) in all five.







