# Idempotency-Key — implementation plan

2026-11-19. Implements `specs/2026-11-19-idempotency-key-design.md` (I1–I32; §11 wins). Research:
`research/2026-11-19-idempotency-key.md`. `srv/` is `apps/server/internal/`, `host/` is `apps/host/frontend/src/`.

## Order and process

Docs PR (research, spec, this plan) → **A** → **B**, one PR each, each landed on `main` through the merge queue
before the next is pushed; pushed branches are never rebased. Per delivery: build in a worktree off the latest
`origin/main`, tests first; a **code review** and a **security review** in separate passes; fix; gates
(`mise run server:check`, `server:test` with `-p 2 -timeout 30m`, `frontend:check`, `docs:check`; `go generate`
in `apps/server` after docs edits); PR; green CI; **check the migration number against `main` and every open PR
before enqueueing**; merge via the merge queue. Commits are Conventional, SSH-signed, with the trailer.

## Delivery A — the engine

- `srv/db/migrations/00NNN_identity_idempotency.sql` (next free number), identity queries, sqlc.
- `srv/contracts`: `IdempotencyStore`, the reason constant; `srv/identity`: the store on `Access`
  (seal/open with `secrets.Box`, claim/lookup/complete/mark-unknown/delete-409, quota), erasure hooks (person
  erasure, customer anonymisation), the prune step (loop until short, own switch).
- `srv/config`: `IDEMPOTENCY_ENABLED`; `vantigo.env.example`.
- `srv/module`: `idempotency.go` (middleware in `wrap`'s `serve`, recorder replacing `w` before `WithRequest`,
  fingerprint with canonical JSON, the flow of I5/I23/I26/I27, replay headers), the shared predicate
  `idempotentOperation` with its test listing excluded operations and the slow-operation measurements, the
  `*string` reason holder in `keyGate.run`; export `jsonRequest`/`jsonResponse`.
- `openapi/identity.yaml` (copied): the `idempotent_replay` reason in the enum; `host/`: `refusalReasons`, labels
  en+nb; tests.
- Docs: `reference/api-introduction.md` ("Retrying safely", refusal codes), `reference/api-compatibility.md`,
  `admin/api-access.md` en+nb (Requests reason, retrying), `admin/configuration.md` en+nb, user guide Requests
  text en+nb, `admin/authentication.md`/`contributing/module-boundaries.md` as coverage demands; `go generate`.

## Delivery B — the surface

- `srv/openapi/integrator`: `Options{Idempotency}` and merging of 409/422; callers and cache keys
  (`apidocument.go`, `apimcp_index.go`); the equality test with the predicate; MCP tool descriptions and
  instructions (one key per action, reuse on retry), replay text in results; `reference/mcp.md`,
  `reference/webhooks.md`, `admin/api-access.md` en+nb; the API reference regenerates from the contract.
- Landing: one clause in the integrations note (en, nb) in `vantigo-landing`, its own PR after B.
