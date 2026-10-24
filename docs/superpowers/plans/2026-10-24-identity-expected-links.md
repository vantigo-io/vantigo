# Entra phase 2, delivery A — the announced link: implementation plan

> **For agentic workers:** implement this plan task by task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** an Owner connects a user who already exists to the workforce identity provider, so that the user's next SSO sign-in or SCIM push attaches to them exactly as a regular provisioning would — and disconnects one again. The Users screen shows, per user, whether they are connected, awaiting their first sign-in, or not connected, with the action beside it.

**Architecture:** one new identity table, `identity.expected_links`, keyed on the user, holding the issuer and the two keys an arrival can carry (object id, user principal name). Two Owner endpoints under `/api/v1/identity/owner/users/{id}/sso-link` (POST announces or replaces, DELETE withdraws the announcement and unlinks). The OIDC completion and the SCIM create consult the announced link before the announced account and fulfil it by inserting the link or mapping for the named user, audited `account.linked-from-provider`. `refuseWhileLinked` counts announcements too. `OwnerUserResponse` gains `ssoLink`. The host's Users screen gains the column and a modal with the typed principal name; the Entra module's directory picker is delivery B. Branch `feat/identity-expected-links`, cut from `main` at `df9bf81d`.

**Spec:** [`docs/superpowers/specs/2026-10-24-entra-link-existing-users-design.md`](../specs/2026-10-24-entra-link-existing-users-design.md) L1–L6, delivery A, testing A.

## Tasks

- [x] **1. This plan.**
- [x] **2. Migration and queries (`srv/db/migrations`, `srv/identity/queries`, `sqlc`).**
  - `00064_identity_expected_links.sql` as L1 writes it, with the Up and Down comments the earlier migrations carry; add it to `srv/identity/sqlc.yaml`'s schema list.
  - `queries/expected_links.sql`: `UpsertExpectedLink`, `DeleteExpectedLink :execrows`, `GetExpectedLinkByUser`, `GetExpectedLinkByObjectID (issuer, object_id)`, `GetExpectedLinkByUPN`, `GetExpectedLinkTaker (issuer, object_id, upn) → user_id` (who else holds either key), `CountExpectedLinksByIssuer`.
  - `queries/oidc.sql`: `DeleteOidcLinksByUser (issuer, user_id) :execrows`; `queries/owner_users.sql`: `ListManagedUsers` LEFT JOINs the user's announced link (`expected_issuer`, `expected_object_id`, `expected_upn`, `expected_at`).
  - `sqlc generate` in `srv/identity`.
- [x] **3. Contract (`openapi/identity.yaml`, the embedded copy, `oapi-codegen`).**
  - `OwnerUserResponse.ssoLink` (L2); `POST` and `DELETE /api/v1/identity/owner/users/{id}/sso-link` with `OwnerSsoLinkRequest` and the refusals; reword the `confirmIssuerChange` and the DELETE oidc descriptions that promised relinking to a later release (L4).
  - Copy to `srv/openapi/specs/identity.yaml`; `go generate ./...` in `apps/server`.
- [x] **4. Identity (`srv/identity`).**
  - `expected_links.go`: validation (`validExpectedLink`), the refusals (`ssoNotConfigured`, `accountLinked`, `ssoLinkTaken`), `PostIdentityOwnerUsersByIdSsoLink`, `DeleteIdentityOwnerUsersByIdSsoLink`, the audits (`account.link-expected`, `account.link-unexpected`, `account.unlinked`, `account.linked-from-provider`), `oidcExpectedLink` + `fulfilExpectedOIDCLink`, `scimExpectedLink` + `fulfilExpectedScimLink`.
  - `oidc.go` `GetIdentityOidcComplete`: the announced link read after the link lookup and before the email check; fulfilled in a serializable transaction under the owner lock, then `oidcSignInLinked`. `scim_users.go` `PostIdentityScimV2Users`: `fulfilExpectedScimLink` before `adoptExpectedScimUser`.
  - `sso_settings_owner.go` `refuseWhileLinked`: links plus announcements. `users.go` `ownerUser`: `ssoLink`.
  - Tests in `expected_links_test.go` as the spec's testing A lists them; `sso_settings_owner_test.go` for the count.
- [x] **5. Frontend (`apps/host/frontend`).**
  - `bun run gen:client`; `api/users.ts`: `ssoLink`, `announceSsoLink`, `removeSsoLink`.
  - `routes/workspace/users.tsx`: the column (desktop and mobile), the connect modal, the disconnect and cancel confirmations, the refusal words; `catalogs/admin.ts` in `en` and `nb`; `users.test.tsx`.
- [x] **6. Documentation, both languages, and `ROADMAP.md`.**
  - `user/workspace-administration.md`: a *Connect an account to single sign-on* section under the users part.
  - `admin/sso-scim.md`: the announced link beside the announced account; the authority change's remedy. `admin/authentication.md`: the Owner endpoints as procedures and the refusal table; the audit events.
  - `ROADMAP.md`: Entra phase 2 — A done, B next.
- [x] **7. Verification.** `mise run server:check`, `mise run server:test`, `mise run frontend:check`, `mise run docs:check`.
