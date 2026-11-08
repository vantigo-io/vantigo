# Virtual employees, delivery P1 — agent users and agent grants

Design: `docs/superpowers/specs/2026-11-08-virtual-employees-design.md`, V5 and V6 (on
branch `claude/virtual-employees-agent-investigation-136e89`). This is the first half of
delivery P; the second (the AI model per subject, the dispatcher for two modules, the
customer scope, the `APIRunners` hook, Knowledge's `ai_hidden`) is P2. Stacked on
`agents/l-agentloop`.

## Tasks

1. **Migrations.** `00141_identity_agent_users.sql`: `users.kind` (`person` default, `agent`,
   CHECK), no credentials on an agent (CHECK), and insert triggers that refuse an agent a
   session, login ticket, passkey, invitation, OIDC link, SCIM mapping, delegation, role
   stewardship and the Owner and SystemAdmin roles. `00142_identity_agent_grants.sql`:
   `identity.agent_grants`, a TTL CHECK of at most 10 minutes, a trigger that the user is an
   agent. Both listed in `identity/sqlc.yaml`.
2. **Contracts.** `contracts/agents.go`: `AgentAccounts`, `AgentGrants`, `IssueAgentGrant`,
   errors, `MaxAgentGrantTTL`. `contracts/grants.go`: `GrantRef`, `WithGrant`, `GrantFrom`;
   `WithAssistantGrant` and `AssistantGrantFrom` become wrappers. `Via` gains `AgentID`,
   `CustomerID`, `ApprovedBy`; `ViaAgent`. `UserEntry.Kind`, `Holder.Kind`,
   `DirectoryOption` / `IncludeAgents()` on `SearchUsers` and `PermissionHolders`.
3. **Slots.** `Module.AgentAccounts` / `Module.AgentGrants` → `Deps.AgentAccounts` /
   `Deps.AgentGrants`, resolved by `Compose` (`withAgentSlots`), single provider, identity.
4. **Identity.**
   - Queries: `agent_accounts.sql`, `agent_grants.sql`; `kind` on the user, directory, owner
     and access queries; `include_agents` on the two directory queries; sign-in, sessions and
     `Notify` exclude agents; `CountUsersByKind`.
   - `access.go`: `evaluate`, `permitted`, `policySatisfied` and the helpers take a
     `subject` (user, roles, state, MFA) instead of a session row; `Check` switches on the
     grant kind.
   - `agent_grants.go`: `checkAgentGrant` and the `AgentGrants` slot. `agent_accounts.go`: the
     `AgentAccounts` slot.
   - Refusals: `managedTarget` answers 404 for an agent (every owner operation), delegation
     validation, password recovery.
   - API: `kind` on `OwnerUserResponse` and `AccessUserResponse`; `GET /identity/owner/user-counts`.
5. **Communications.** `GET /communications/assignable-users` passes `IncludeAgents()` and
   answers `kind`.
6. **Tests.** `identity/agent_test.go` (accounts, no sign-in / reset / invitation / SCIM / OIDC,
   role and delegation refusals, listing and counts, directory default, Notify dropped, grant
   issue / check / revoke / expiry / Via / refused policies, the context kinds); the session
   path stays under the existing matrix tests; `contracts/users_test.go` pins who passes
   `IncludeAgents()`; the communications assignable-users test gains an agent; fakes of
   `UserDirectory` take the trailing option.
7. **Documentation** (both languages where the section is bilingual):
   - `admin/authentication.md` en + nb: *Agent users and agent grants*, the SCIM note, the two
     migrations in `sources`;
   - `user/workspace-administration.md` en + nb: *Agents among the users*;
   - `reference/communications.md`: the assignable-users answer;
   - `contributing/module-boundaries.md`: the two slots, `IncludeAgents`, the grant kinds;
   - `go generate ./...` in `apps/server` (OpenAPI specs copy, the embedded docs, sqlc) and
     `bun run gen:client`.

## Deviations

- `IncludeAgents` is a variadic option (`...contracts.DirectoryOption`) rather than a `bool`
  parameter, so the 20-odd existing callers keep compiling and keep the default false. Fakes
  of the interface in tests take the trailing parameter.
- `counts: {people, agents}` is `GET /identity/owner/user-counts`, not a field of
  `GET /identity/owner/users`: that response is a bare array the frontend and the recorded
  exchanges consume, and wrapping it would break both for a count.
- The database also refuses what the code refuses (triggers in 00141), because the Owner role
  passes every check and a path missed in the code would be the whole of the design's first
  lock. The triggers fire on insert only: a BEFORE UPDATE trigger on `identity.roles` makes an
  edit lock the row `FOR UPDATE`, which `TestRoles_AnAssignmentInFlightBlocksADeleteButNotAnEdit`
  forbids.
- No shared avatar component exists in `packages/frontend-shell`, so the AI badge and the
  Users page's separate section are left to delivery A (the directory and both lists already
  answer `kind`).
