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
     agent's call needs `contracts.WithAgentRun`. The Assistant installs kind `assistant`.
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

- **The run's customer reaches the dispatcher on the context** (`contracts.WithAgentRun`), not from
  the grant: the dispatcher has no way to read a grant's `Via` before a call, and identity stays the
  authority of the grant. The agents module sets both from the same values. The agent call is
  refused without it.
- **`ai.Client.resolve` is unchanged**: the `task#subject` name goes to the resolver whole and
  identity, which knows the tasks, splits it and refuses a subject on a task that is not
  `PerSubject`.
- **`module.ComposeAPI`** returns the runners; `Compose` keeps its signature (many callers). The
  runners are built after the dispatcher is bound and started by the caller that owns the process
  context.
- **Shipments** (Orders) declare no scope: they name no customer, so they cannot be allowlisted for
  an agent until Orders adds one. The order (marked) carries the customer.
