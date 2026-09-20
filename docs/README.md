# Vantigo documentation

Operator and contributor documentation for Vantigo:

- [Identity, authentication and deployment](customers-authentication.md) — local
  accounts, the static environment-configured OIDC provider, email, proxy trust,
  `APP_SECRET`-derived key material, and migrations.
- [Static identity operations](sso-scim-operations.md) — production static-only
  OIDC/SCIM configuration, rotation, migration, lifecycle, and recovery.
- [Transport security and browser hardening](transport-security.md) — the
  operator's https, PostgreSQL TLS or Unix socket, and SMTP TLS choices and what
  each costs, HSTS, host filtering, the content security policy, and the API
  contract document.
- [Docker Compose deployment](../deploy/compose/README.md) — the pre-built image
  quick start, proxy example, upgrades, and production notes.
- [Products module](products.md) — product domain and API reference.
- [Communications module](communications.md) — communications domain and API reference.
- [Projects module](projects.md) — projects, codes, roles, financial shaping, the
  optional Products dependency and the contracts later modules build on.
- [Time module](time.md) — time entries, the rate chain and its snapshots, the
  approval state machine, weekly submission, the period lock and permissions.
- [Expenses module](expenses.md) — outlays and mileage, receipts, approval, the
  reimbursed and invoiced tracks, dated rates, the payroll CSV and permissions.
- [Module boundaries](module-boundaries.md) — implementation ownership and module
  conventions.
- [Object storage](storage.md) — the filesystem-only provider, module scopes, key
  validation, and streamed downloads.
