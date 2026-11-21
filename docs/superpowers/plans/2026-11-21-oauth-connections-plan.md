# OAuth connections — implementation plan

2026-11-21. Implements `specs/2026-11-21-oauth-connections-design.md` (C1–C28; **§11 wins**, A1–A24).
Research: `research/2026-11-21-oauth-connections.md`. `srv/` is `apps/server/internal/`, `host/` is
`apps/host/frontend/src/`.

## Order and process

Docs PR (research, spec, this plan) → **A** → **B** → **C** → landing PR (inside C's step). One PR
each, each landed on `main` through the merge queue before the next is pushed. Pushed branches are
never rebased **and never have `main` merged into them** (the queue lands by rebase and rejects a
merge commit): if a pushed branch conflicts, open a fresh linear branch with the same tree and a
replacement PR. Per delivery:

1. Build in a worktree off the latest `origin/main` (`bun install --frozen-lockfile` first); tests
   first where a rule is testable.
2. A **code review** and a **security review** in separate passes by separate agents; fix their
   findings.
3. Gates: `mise run server:check`, `server:test` (`go test -p 2 -timeout 30m` for a full run; rerun
   failing packages with `-p 1` for SQLSTATE 53300/57P03), `frontend:check` (read its log, the
   wrapper exits 0), `docs:check`; `go generate` in `apps/server` after any docs or contract edit.
4. Open the PR, green CI, **check the migration number against `main` and every open PR right
   before enqueueing**, merge through the merge queue (GraphQL `enqueuePullRequest`).
5. Commits are Conventional, SSH-signed, with the attribution trailer.

Migrations: one in A (next free: **00218** on 2026-11-21), one in C (registered clients, the log's
index). Renumber right before pushing.

## Delivery A — the server, switched off

Code:
- `srv/db/migrations/00NNN_identity_oauth.sql`: `oauth_connections`, `oauth_codes`, `oauth_tokens`
  (`kind`, `parent_hash`), `oauth_client_documents`; `api_requests.key_id` nullable and
  `connection_id` (no CHECK, A18). Queries and sqlc.
- `srv/config`: `OAUTH_ENABLED` (default **false** in A), the normalised issuer and resource (A12).
- `srv/identity/oauth_*.go`: client-id and redirect validation (C6, C9, A1, A4), the document fetch
  behind `oauthNet` and its cache (A2, C7), preview and approve (C13, C14, A6, A9), the token
  endpoint (C15–C17, A9–A11), `checkConnection` and `rejectConnection`, `MCP()`/`Scope()` for a
  connection, the authorizer predicate and its revocations (A15–A17), archive revoking connections,
  list and revoke operations (C18, C25), audit, the worker's pruning (A21), the request recorder
  (A18).
- `srv/contracts`: `Via.ConnectionID`, `OAuthEndpoints`, `APIRequest`/`APIKeyRefusal.ConnectionID`,
  `ErrInvalidToken`.
- `srv/module`: the token endpoint mounted ahead of the cross-origin guard (A9), `/api/mcp` for
  `vta_` (A15), the `401` challenge (C22), tool `securitySchemes` (A14).
- `srv/server`: discovery in `root` (A13); `/.well-known/*` otherwise 404.
- `openapi/identity.yaml`: `POST …/oauth/authorizations/preview`, `POST …/oauth/authorizations`,
  `GET` and `DELETE …/integrations/{id}/connections[/{connectionId}]`, `IntegrationRequest`'s new
  fields and reasons; all `x-vantigo-api: never`, `x-vantigo-assistant: never`. Registries of A22.

Tests: spec §8's unit and integration lists, with the three live client documents as fixtures (A1),
a pool of one, and the switch off.

Docs: new `reference/oauth.md` (says the feature is switched off until the screens land);
`reference/mcp.md`, `reference/api-introduction.md`; `admin/api-access.md` in `en` and `nb`
(`OAUTH_ENABLED`); `admin/installation.md` in both (routing `/.well-known/oauth-*`);
`contributing/module-boundaries.md` and `admin/authentication.md` (both languages) where their
`sources` are touched.

## Delivery B — the screens, switched on

Code: `host/routes/oauth.authorize.tsx` and its tests (C13, A6–A8), `publicPaths`, the root's
consume step, "Connected apps" on the integration Overview with Revoke and the inactive state
(C25, A17), the Requests tab's client name (A18), catalogs in `en` and `nb`; `OAUTH_ENABLED`
defaults to true.

Docs: `admin/api-access.md` in `en` and `nb` (connecting Claude and ChatGPT step by step, what the
consent page shows, Connected apps, ending a connection, what ends one by itself);
`admin/authentication.md` in both (the return after sign-in); `reference/oauth.md` and
`reference/mcp.md` lose the "switched off" note.

Check on a running instance: Claude Code against a local instance (`claude mcp add --transport
http`), the consent page in both languages, revoke.

## Delivery C — registered clients and the rest

Code: migration (`oauth_clients`, `ix_api_requests_connection`); the `oauth-clients` operations and
the "Registered clients" section (C8); `oauth_client_hosts` (C11); revalidation at refresh (A3);
`POST /api/oauth/revoke` (C18, A23) and its metadata; the Requests tab's connection filter; the MCP
section's "Connect by signing in" (C25).

Docs: the same reference and administration pages, both languages. **Landing:**
`vantigo-landing` — the API and MCP lines say a client connects by signing in (English and
Norwegian), `mise run apps:check`, its own PR linked from C's.

## After the deliveries

Memory note; a check with ChatGPT and Claude's hosted connector needs a public `https` instance —
named as open when handing over. Owner's overturns: spec §10.
