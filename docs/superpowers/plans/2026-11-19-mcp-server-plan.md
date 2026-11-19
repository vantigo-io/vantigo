# MCP server — implementation plan

2026-11-19. Implements `specs/2026-11-19-mcp-server-design.md` (M1–M34; §11 wins). Research:
`research/2026-11-19-mcp-server.md`. `srv/` is `apps/server/internal/`, `host/` is
`apps/host/frontend/src/`.

## Order and process

Docs PR (research, spec, this plan) → **A** → **B** → landing PR (inside B's step). One PR each,
each landed on `main` through the merge queue before the next is pushed; pushed branches are
never rebased (merge `main` in if needed). Per delivery:

1. Build in a worktree off the latest `origin/main`; tests first where a rule is testable.
2. A **code review** and a **security review** in separate passes by separate agents; fix
   their findings.
3. Gates: `mise run server:check`, `server:test` (rerun failing packages with `-p 1` for SQLSTATE
   53300/57P03; invoices needs `-timeout 30m`), `frontend:check` (the cutover-sweep timeout is
   a known flake: rerun alone), `docs:check`; `go generate` in `apps/server` after any docs edit.
4. Open the PR, green CI, **check the migration number against `main` and every open PR right
   before enqueueing**, merge through the merge queue (GraphQL `enqueuePullRequest`).
5. Commits are Conventional, SSH-signed, with the attribution trailer.

Migration: one, in A (`mcp_access`), next free number (00210 on 2026-11-19).

## Delivery A — the read-only MCP

Code:
- `srv/db/migrations/00NNN_identity_mcp_access.sql`; identity queries (`GetAPIKeyScope` joined with
  `mcp_access`, the setting's update with version bump), sqlc.
- `openapi/identity.yaml` (copied by `go generate`): `PUT …/integrations/{id}/mcp`,
  `IntegrationDetail.mcpAccess` and `mcpAvailable`, marks (`policy:Owner+OwnerManagement`,
  `x-vantigo-api: never`, `x-vantigo-assistant: never`), `api-marks.txt` golden, the
  Assistant never list, identity's `limits`, the operation-coverage test.
- `srv/identity`: the setting's service and audit `integration.mcp_changed` (M1–M3, M17);
  `contracts.MCPAccess` (`srv/contracts`) implemented by `Access`, one query for key, scope and mode
  (M22).
- `srv/config`: `MCP_ENABLED`; `vantigo.env.example`.
- `srv/module/apimcp.go` (+ `apimcp_index.go`, `apimcp_tools.go`, `apimcp_protocol.go`): the
  handler (M5–M13, M21–M33): protocol classification and conformance, authentication
  (M27), the per-IP limiter, the parsed-index LRU (M23), kind from the contract (M24), the three
  tools, the loopback (M12, M26) with result handling (M13, M32), `keyGate` `record`-only
  entry (M22), wiring in `compose.go` (M29).
- `host/`: the MCP section on the integration's Overview (mode radios saved with `If-Match`,
  URL via `appUrl`, snippet, kill-switch note), `api/integrations.ts` additions, regenerated
  `api-schema.d.ts`, `catalogs/api-access.ts` en+nb keys, tests.

Docs (en and nb where the section is bilingual): new `reference/mcp.md` (English: the setting, the
endpoint, protocol, headers, the three tools, results and limits, errors, security;
`read_write` equalled `read` until B, which removed the statement), `reference/index.md`, `reference/api-introduction.md`,
`admin/api-access.md` en+nb (the section, modes, kill switch, connecting a client),
`admin/configuration.md` en+nb. `sources` per M21. `go generate`.

Tests: spec §7 in full, except the `write` tool.

## Delivery B — write, snippet, completion

Code: the `write` tool and its listing (M8, M11, M25, M31); the connection snippet in
`-api-key-dialog.tsx`; remove "`read_write` equals `read`" from the code and docs.
Docs: `reference/mcp.md`, `admin/api-access.md` en+nb, `user/workspace-administration.md`
en+nb; the `write` tests of §7 (permission 403 as `isError`, `If-Match`, path escaping,
destructive annotation, a read-only key under `read_write`).
Landing: `../vantigo-landing` home page integrations note, en and nb, from the admin guide,
opened as its own PR (`mise run apps:check` there is stale-checkout-sensitive: run it against
this repository's current checkout).

## After the deliveries

Record the state in project memory. The manual check with a real client
(`claude mcp add --transport http …`) and the owner's overturns (spec §10) stay open.
