# Vantigo documentation

Operator and contributor documentation for Vantigo:

- [Identity, authentication and deployment](customers-authentication.md) — local
  accounts, the static environment-configured OIDC provider, email, proxy trust,
  PostgreSQL Data Protection keys, and migrations.
- [Static identity operations](sso-scim-operations.md) — production static-only
  OIDC/SCIM configuration, rotation, migration, lifecycle, and recovery.
- [Transport security and browser hardening](transport-security.md) — the
  fail-closed https, PostgreSQL TLS and SMTP TLS rules and their escape hatch,
  HSTS, host filtering, the content security policy, and OpenAPI exposure.
- [Docker Compose deployment](../deploy/compose/README.md) — the pre-built image
  quick start, proxy example, upgrades, and production notes.
- [Products module](products.md) — product domain and API reference.
- [Communications module](communications.md) — communications domain and API reference.
- [Module boundaries](module-boundaries.md) — implementation ownership and module
  conventions.
- [Object storage](storage.md) — provider configuration, authentication, scopes,
  permissions, and streamed downloads.
