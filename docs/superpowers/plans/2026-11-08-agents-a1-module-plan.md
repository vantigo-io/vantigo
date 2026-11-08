# Agents A1: the module, personas, roles, levels and the gate

Design: `docs/superpowers/specs/2026-11-08-virtual-employees-design.md` §5 (V9, V9b's allowlist,
V10, V11's settings and wording, V12) and the frontend screens 1 to 3. Stacked on P2
(`agents/p2-platform`). The trigger, runs, tools, pipeline and modes in action are A2; the review
queue, calibration, activity, usage and retention are A3.

## Tasks

1. **Module registries** (AGENTS.md, Adding a module): `openapi.Modules`, `generate.go`, `cfg-agents.yaml`,
   `gen-client.ts` and its test, `moduleSchemas`, config (`agents` requires `communications`) and its
   tests, `modulesEnv`, `cmd/vantigo`, `everyModule()`, the Assistant's `appRoutes` (`promptVersion`
   bumped) and `APP_ROUTE_PREFIXES`, every depguard set (planning's lacked the Assistant; only agents
   was added there), `IncludeAgents` pinning test allows `agents`.
2. **Schema and contract.** Migration `00144_agents_baseline.sql` (`settings`, `agents`,
   `permission_levels`, `roles`, `role_channels`, `events`); `openapi/agents.yaml` (18 operations under
   `/api/v1/agents`, all `x-vantigo-assistant: never`, pinned in the integration test).
3. **Server** (`internal/agents`): settings with the company-name precondition; agents (create through
   `AgentAccounts`, persona, model through `AIModels.SetModelFor("agents.run", id)`, pause/resume,
   delete = state deleted + channels freed + `RevokeForUser` + `Disable`); avatar; roles; channels;
   levels and the setup endpoint; the front desk template in code. Raising a level, choosing or
   loosening an Auto mode and binding a channel are checked in the handler against the Owner policy
   (403 `owner_required`); lowering and unbinding are `agents:manage`.
4. **The gate** (V9), installed in `mount` with `SetGate(GrantAgent, ...)`: allowlist, levels default off,
   `agentNever`, cached by the agent's version (1 s recheck). `agents.GateFor` builds it for
   `modtest.Harness.Operations`; `modtest.Harness.AgentGrantContext` issues a grant for a real agent.
5. **Tests**: API (permissions, validation, company-name precondition, Owner-tier, delete flow, avatar,
   concurrency, coverage of every operation); the template contract tests (m5: every allowlisted
   customer-data operation carries `x-vantigo-customer-scope`, needs cover the allowlist); the gate and
   scope pinning against the real contracts and the `agentNever` family test (`internal/integration`).
6. **Frontend** `apps/agents/frontend` (`@vantigo/agents-ui`): list, agent page (Persona, Roles,
   Permissions, placeholder Usage and Activity), role page; the shared AI badge on avatars, used in
   Communications' assignee select and rows; host wiring; en and nb.
7. **Documentation** (both languages where bilingual): `user/agents.md` (new), `admin/agents.md` (new),
   `reference/agents.md` (new, English), the section overviews, `admin/configuration.md` (`MODULES`),
   `user/workspace-administration.md` (agents among users); then `go generate` and
   `chore(assistant): regenerate the embedded documentation`.

## Deviations

- **The gate's allowlist is the union of the agent's roles' templates**: a grant names the agent and the
  run, not the role, until A2 records the run's role. A2 narrows it to the run's role.
- **Channels are not checked for existence**: no contract reads Communications' channels. An id that names
  no channel never matches a conversation. A2's trigger brings a channel directory.
- **No prefill of the company name** from a workspace display name: no such setting exists to read.
- `agents:review` and `agents:activity` are declared and used by A3.
- Messenger/E-mail marking, the pipeline's tools and the hard rules' code are A2. The template lists the
  hard rules only for the role page.
- A2 must pin `OwnConversation` operations to the run's conversation, and the status/hand-off use of
  `patchCommunicationsConversationsById` to those fields (it also carries `customerId` and
  `assignedUserId`).
- The Assistant API is not in `agentNever`: it names no permission, and the dispatcher refuses it by path.
