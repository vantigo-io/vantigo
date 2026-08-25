# Tenancy and tenant isolation

Vantigo can host one tenant or many. This document states plainly what each mode
guarantees today, and where the isolation story is still incomplete.

## Status summary

| Capability | Status |
| --- | --- |
| Single-tenant mode (`Tenancy__Mode=single`, the default) | Supported |
| Multi-tenant mode (`Tenancy__Mode=multi`) | **Not production-ready**, refuses to start outside Development |
| EF Core global query filters per tenant | Enforced |
| PostgreSQL row-level security as defense in depth | Enforced when running as the least-privilege role (the Compose default) |

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

## Database-level tenant isolation

Tenant-owned tables have PostgreSQL row-level security enabled and forced, with
a policy that matches on the session-scoped `app.tenant_id` setting:

```sql
CREATE POLICY tenant_isolation ON <table>
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
```

`TenantConnectionInterceptor` applies the setting with
`set_config('app.tenant_id', …, false)` every time EF Core opens a connection,
so it covers transaction-less endpoint reads as well as transactional work.
When no tenant is resolved the interceptor explicitly writes an empty string,
which the `NULLIF` guard turns into "match no rows": a pooled physical
connection can never carry a previous request's tenant, and access without a
tenant fails closed instead of failing the uuid cast. (A pooled-connection
reset — `DISCARD ALL` — leaves a previously-set custom setting as an empty
string rather than unset, which is why the guard exists.)

Row-level security is defense in depth behind the EF Core global query filters,
and it only applies to roles without `BYPASSRLS`. The application therefore
runs as a least-privilege role by default:

| Role | Created as | Used by |
| --- | --- | --- |
| `POSTGRES_USER` (default `vantigo`) | Superuser, owns every schema and table | The `vantigo-migrate` job |
| `POSTGRES_APP_USER` (default `vantigo_app`) | `NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS`, table DML only, owns nothing | The API (`VANTIGO_DB_USER` defaults to it) |

Deployments that apply migrations from the API process (rather than a separate
migrate job) can set `ConnectionStrings__migrations` to the owner role's
connection string; the application then applies migrations over that connection
while all runtime traffic stays on the least-privilege role.

The roles are created by the PostgreSQL init script, which runs **only when the
`postgres-data` volume is first initialized**. Existing installations must apply
the same statements by hand; the SQL is reproduced in
[the Compose README](../deploy/compose/README.md).

The integration-test factories provision the same role shape
(`TenantRuntimeRoleSql`) and boot the API with it, so every integration test
exercises RLS the way production runs it.
`LeastPrivilegeDatabaseRoleIntegrationTests` in `Customers.Module.Tests`
additionally verifies the properties directly: transaction-less reads are
tenant-filtered, `IgnoreQueryFilters` and raw SQL still cannot cross tenants,
interleaved requests over the shared pool do not bleed, and an unresolved
tenant sees nothing.
