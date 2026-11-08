# Agents P2: the rest of the platform for a principal without a person

Design: `docs/superpowers/specs/2026-11-08-virtual-employees-design.md` §4 (V7, V8, V9b's
mechanism, V9a) and the `APIRunners` hook of V14. Stacked on P1 (`agents/p1-identity`). The
`agents` module itself is not here (A1).

## Tasks

1. **V7, a model per subject.**
   - `contracts.AITask.PerSubject`; `contracts.AIModels.SetModelFor`; `ai.TaskFor(task, subject)`
     writes `task#subject`.
   - Migration `00143_identity_ai_task_model_subjects.sql`: `subject text NOT NULL DEFAULT ''`,
     primary key `(task_key, subject)`; the identity queries and `sqlc.yaml`.
   - `identity.aiModels.ModelFor` splits on `#` (subject, task, recommendation, plan default) and
     refuses a subject on a task that is not `PerSubject`; `SetModelFor` writes or forgets one. The
     AI models endpoints keep working on subject `''`.
   - Tests: `identity/ai_models_internal_test.go`, `ai/ai_test.go`.
2. **V8, two principals on the dispatcher.**
   - `Dispatcher.SetGate(kind, gate)`; every method takes the gate from the context's grant kind
     (no grant: the assistant's). `Call` accepts either grant, `RemoteAddr` `agent-<user>`, and an
     an agent's run is read from its grant (`AgentGrants.Describe`). The Assistant installs kind `assistant`.
   - `Compose` hands `Deps.Operations` to `assistant` and `agents`. depguard `operations` allows
     `internal/agents`.
   - Tests: `operations/scope_test.go`, `module/operations_test.go`.
3. **V9b, the customer scope mechanism.**
   - `x-vantigo-customer-scope` (`param` or `resource`), parsed into `operations.Info.CustomerScope`
     at Bind and enforced for an agent's run (`operations/scope.go`); `customer_scope` error.
   - OpenAPI lint for the extension; the extension on the eight operations of Communications,
     Customers, Orders and Invoices (`openapi/*.yaml`, then `go generate` for the copies); the
     contract test `openapi/customer_scope_test.go`.
   - `modtest.Harness.AgentContext`; an integration test over the real Customers contract.
4. **APIRunners.** `Module.APIRunners`, `module.Runner`, `module.StartRunners`, `module.ComposeAPI`
   (Compose drops the runners); `cmd/vantigo` starts them in the API process on the workers' context
   and waits on shutdown; the `modtest` harness starts them too. Tests: `module/runners_test.go`.
5. **V9a, Knowledge.** `ai_hidden` applies when `Via` is set at all; the member picker passes
   `IncludeAgents()` (the pinning test in `contracts/users_test.go` allows `knowledge`). Test in
   `integration/knowledge_assistant_test.go`.
6. **Documentation (English first; `admin/configuration` also in Norwegian).**
   - `admin/configuration.md` en and nb: a model per subject, briefly.
   - `reference/knowledge.md`: `ai_hidden` for agents and the member picker.
   - `reference/customers.md`, `orders.md`, `invoices.md`, `communications.md`: the scope extension
     on the marked operations.
   - `contributing/module-boundaries.md` rule 18 (two principals, a gate per kind, the scope,
     APIRunners) and `contributing/platform-packages.md`; `reference/assistant.md` two lines.
   - `go generate ./...` in `apps/server`, committed as `chore(assistant): regenerate the embedded
     documentation`.

## Deviations

- **The run's customer is read from the grant** (review): identity's `AgentGrants.Describe` returns the
  run of a current grant of an active agent, `Options.AgentGrants` gives it to the dispatcher at
  `Bind`, and every method resolves it first (a grant that is not current is `no_grant`). The gate
  sees it through `operations.AgentRunFrom`. There is no second input.
- **No gate for the agent kind means every operation is never** (review); the assistant keeps the
  closed default.
- **`ai.Client.resolve` is unchanged**: the `task#subject` name goes to the resolver whole and
  identity, which knows the tasks, splits it and refuses a subject on a task that is not
  `PerSubject`.
- **`module.ComposeAPI`** returns the runners; `Compose` keeps its signature (many callers). The
  runners are built after the dispatcher is bound and started by the caller that owns the process
  context.
- **Shipments** (Orders) declare no scope: they name no customer, so they cannot be allowlisted for
  an agent until Orders adds one. The order (marked) carries the customer.

## Notes for the next deliveries

- A2: tool-capability of a model is not modelled (`SetModelFor` accepts any allowed model), so a run
  must surface a clear error when its model cannot call tools.
- A1 (m5): the agent gate allows only the role's allowlisted operations, and every allowlisted
  customer-data operation must carry `x-vantigo-customer-scope`; add that check to the template tests.
- Checked: the conversations list filters `customer_id` exactly (its other parameters only narrow),
  and the customer's contacts answer carries only the contacts' own details for that customer, so
  both stay in the marked set. A conversation's `candidateCustomerIds` and `suggestedCustomerId` are
  ids only.
