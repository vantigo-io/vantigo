# Tenancy and tenant isolation

Vantigo can host one tenant or many. This document states plainly what each mode
guarantees today, and where the isolation story is still incomplete.

## Status summary

| Capability | Status |
| --- | --- |
| Single-tenant mode (`Tenancy__Mode=single`, the default) | Supported |
| Multi-tenant mode (`Tenancy__Mode=multi`) | **Not production-ready**, refuses to start outside Development |
| EF Core global query filters per tenant | Enforced |
| PostgreSQL row-level security as defense in depth | **Not enforced at runtime** |

## Single-tenant mode is the default

`Tenancy:Mode` defaults to `single`. A single-tenant installation auto-provisions
one tenant at startup, needs no tenant routing, and is the only configuration
this project supports for real deployments today.

## Multi-tenant mode is not production-ready

`Tenancy__Mode=multi` enables tenant routing and the `/admin` tenant control
plane, but **authorization is installation-global, not tenant-scoped**:

- Role assignments are created without a tenant, and permission evaluation
  resolves role IDs without filtering by the current tenant.
- Owner user-management endpoints operate on all users in the installation.
- Invitations assign global identity roles.

The practical consequence is that an Owner invited for one tenant can enumerate,
disable, reset passwords for, and modify users belonging to *other* tenants. This
is tracked in [issue #7](https://github.com/vantigo-io/vantigo/issues/7); the
remediation (tenant-scoped roles, assignments, catalogs and invitations, plus
cross-tenant negative tests) has not been done.

Because of that, startup **fails closed**: with `Tenancy__Mode=multi` outside the
Development environment the host refuses to start and reports the reason. The
escape hatch is deliberately unpleasant to type:

```dotenv
Tenancy__Mode=multi
Tenancy__AllowUnsafeMultiTenant=true
```

Setting `Tenancy__AllowUnsafeMultiTenant=true` accepts the cross-tenant exposure
described above. Do not set it in a deployment that hosts more than one
customer's data. Development (`ASPNETCORE_ENVIRONMENT=Development`, which is what
the Aspire AppHost uses) still runs multi-tenant without the flag so the tenant
control plane stays usable locally.

## Database-level tenant isolation is not enforced

Tenant-owned tables have PostgreSQL row-level security enabled and forced, with a
policy that matches on the `app.tenant_id` setting:

```sql
CREATE POLICY tenant_isolation ON <table>
    USING (tenant_id = current_setting('app.tenant_id', true)::uuid);
```

That policy is **inert in every shipped configuration**, for two compounding
reasons:

1. **The application connects as a superuser.** Superusers bypass row-level
   security entirely, including `FORCE ROW LEVEL SECURITY`. The Docker Compose
   stack has always connected the API as `POSTGRES_USER`, which the PostgreSQL
   image creates as a superuser. The integration-test containers do the same.
2. **The tenant setting is transaction-local.** `TenantConnectionInterceptor`
   writes it with `set_config('app.tenant_id', …, true)` and only on
   `TransactionStarted`. Ordinary endpoint reads do not open an explicit
   transaction, so the setting is never applied to them.

Point 2 means the two problems cannot be fixed independently: switching the
application to a role that row-level security *does* apply to makes ordinary
reads return **zero rows** instead of leaking, because an unset `app.tenant_id`
makes the policy match nothing. This is measured, not assumed — see
`LeastPrivilegeDatabaseRoleIntegrationTests` in `Customers.Module.Tests`, which
asserts exactly that behaviour against the real migrated schema.

Tenant isolation today therefore rests entirely on the EF Core global query
filters and the manual predicates in the modules. Row-level security is intended
as defense in depth and currently provides none. Tracked in
[issue #6](https://github.com/vantigo-io/vantigo/issues/6).

### The least-privilege runtime role

The Compose stack now creates a second database role so the least-privilege
configuration exists and is testable, but it is **not the default** — enabling it
would break the application as described above.

| Role | Created as | Used by |
| --- | --- | --- |
| `POSTGRES_USER` (default `vantigo`) | Superuser, owns every schema and table | `vantigo-migrate`, and the API by default |
| `POSTGRES_APP_USER` (default `vantigo_app`) | `NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS`, table DML only, owns nothing | Nothing yet; opt in with `VANTIGO_DB_USER` |

`VANTIGO_DB_USER` / `VANTIGO_DB_PASSWORD` in `.env` select the role the API
connects as, and default to the owner role. Pointing them at
`POSTGRES_APP_USER` is only useful for reproducing issue #6.

The role is created by the PostgreSQL init script, which runs **only when the
`postgres-data` volume is first initialized**. Existing installations must apply
the same statements by hand; the SQL is reproduced in
[the Compose README](../deploy/compose/README.md).

### What the full fix requires

1. Make the tenant setting cover every query, not just transactional ones —
   either a connection-scoped `SET app.tenant_id` applied on connection open and
   reset on return to the pool, or a tenant unit-of-work that wraps all
   tenant-owned work in a transaction.
2. Move the schema-owner and migrator responsibilities onto a non-superuser role
   and run the API as `POSTGRES_APP_USER`.
3. Add integration tests that cover transaction-less reads, `IgnoreQueryFilters`,
   raw SQL, and interleaved requests for two tenants over a shared connection
   pool, all with the least-privilege role.

Until then, treat the EF query filters as the only tenant isolation mechanism,
and run one tenant per installation.
