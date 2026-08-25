using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;

using Npgsql;

using Vantigo.Customers.Database.Customers;
using Vantigo.Customers.Domain.Customers;
using Vantigo.Tenancy;
using Vantigo.Tenancy.Abstractions;
using Vantigo.Tenancy.EntityFramework;

namespace Vantigo.Customers.Module.Tests.Integration;

/// <summary>
/// Verifies that tenant row-level security holds for the application's real
/// runtime configuration: the API connects as a least-privilege role
/// (NOSUPERUSER NOBYPASSRLS, owning nothing), the tenant setting is
/// session-scoped so transaction-less reads are covered, and pooled
/// connections never carry another request's tenant. The defense-in-depth
/// checks go through <c>IgnoreQueryFilters</c> and raw SQL so RLS is exercised
/// without the EF global query filters in front of it.
/// </summary>
[Collection(CustomersApiCollection.Name)]
public sealed class LeastPrivilegeDatabaseRoleIntegrationTests
{
    private readonly CustomersApiFactory _factory;

    public LeastPrivilegeDatabaseRoleIntegrationTests(CustomersApiFactory factory) => _factory = factory;

    [Fact]
    public async Task ApplicationRunsAsALeastPrivilegeRole()
    {
        await using var scope = _factory.Services.CreateAsyncScope();
        using var tenantScope = AmbientTenantContext.Enter(NewTenant());
        var db = scope.ServiceProvider.GetRequiredService<CustomersDbContext>();

        var privileged = await db.Database
            .SqlQueryRaw<bool>("SELECT rolsuper OR rolbypassrls AS \"Value\" FROM pg_roles WHERE rolname = current_user")
            .SingleAsync();

        Assert.False(privileged);
    }

    [Fact]
    public async Task SuperuserConnectionBypassesTenantRowLevelSecurity()
    {
        TenantId tenant = NewTenant();
        await CreateCustomerAsync(tenant, "Superuser visible");

        await using NpgsqlConnection connection = new(_factory.SuperuserConnectionString);
        await connection.OpenAsync();

        Assert.True(await CountCustomersAsync(connection, tenant) > 0);
    }

    [Fact]
    public async Task ConnectionWithoutATenantSettingSeesNoRows()
    {
        TenantId tenant = NewTenant();
        await CreateCustomerAsync(tenant, "Invisible without tenant setting");

        await using NpgsqlConnection connection = new(RawRuntimeConnectionString());
        await connection.OpenAsync();

        Assert.Equal(0L, await CountCustomersAsync(connection, tenant));
        Assert.Equal(0L, await CountCustomersAsync(connection, null));
    }

    [Fact]
    public async Task SessionScopedTenantSettingCoversTransactionLessReads()
    {
        TenantId firstTenant = NewTenant();
        TenantId secondTenant = NewTenant();
        await CreateCustomerAsync(firstTenant, "First tenant");
        await CreateCustomerAsync(secondTenant, "Second tenant");

        await using NpgsqlConnection connection = new(RawRuntimeConnectionString());
        await connection.OpenAsync();
        await using (NpgsqlCommand command = connection.CreateCommand())
        {
            command.CommandText = "SELECT set_config('app.tenant_id', @tenant_id, false);";
            command.Parameters.AddWithValue("tenant_id", firstTenant.Value.ToString());
            await command.ExecuteNonQueryAsync();
        }

        // No transaction anywhere: the session-scoped setting alone must isolate.
        Assert.Equal(1L, await CountCustomersAsync(connection, firstTenant));
        Assert.Equal(0L, await CountCustomersAsync(connection, secondTenant));
        Assert.Equal(1L, await CountCustomersAsync(connection, null));
    }

    [Fact]
    public async Task RowLevelSecurityBlocksCrossTenantRowsEvenWithoutQueryFilters()
    {
        TenantId firstTenant = NewTenant();
        TenantId secondTenant = NewTenant();
        var firstName = $"RLS first {Guid.NewGuid():N}";
        var secondName = $"RLS second {Guid.NewGuid():N}";
        await CreateCustomerAsync(firstTenant, firstName);
        await CreateCustomerAsync(secondTenant, secondName);

        await using var scope = _factory.Services.CreateAsyncScope();
        using var tenantScope = AmbientTenantContext.Enter(firstTenant);
        var db = scope.ServiceProvider.GetRequiredService<CustomersDbContext>();

        // IgnoreQueryFilters removes the EF tenant filter; only the database
        // policy separates the tenants here.
        var visible = await db.Customers.IgnoreQueryFilters()
            .Where(customer => customer.Name == firstName || customer.Name == secondName)
            .Select(customer => customer.Name)
            .ToListAsync();

        Assert.Equal([firstName], visible);
    }

    [Fact]
    public async Task RawSqlOverTheApplicationConnectionRespectsRowLevelSecurity()
    {
        TenantId firstTenant = NewTenant();
        TenantId secondTenant = NewTenant();
        var marker = $"Raw SQL {Guid.NewGuid():N}";
        await CreateCustomerAsync(firstTenant, marker);
        await CreateCustomerAsync(secondTenant, marker);

        await using var scope = _factory.Services.CreateAsyncScope();
        using var tenantScope = AmbientTenantContext.Enter(firstTenant);
        var db = scope.ServiceProvider.GetRequiredService<CustomersDbContext>();

        var count = await db.Database
            .SqlQueryRaw<long>("SELECT count(*) AS \"Value\" FROM customers.customers WHERE name = {0}", marker)
            .SingleAsync();

        Assert.Equal(1L, count);
    }

    [Fact]
    public async Task PooledConnectionsDoNotBleedTenantsAcrossInterleavedScopes()
    {
        TenantId firstTenant = NewTenant();
        TenantId secondTenant = NewTenant();
        var marker = $"Bleed probe {Guid.NewGuid():N}";
        await CreateCustomerAsync(firstTenant, marker);
        await CreateCustomerAsync(secondTenant, marker);

        // Interleave the two tenants repeatedly over the shared pool; every
        // read must see exactly its own tenant's row.
        for (var round = 0; round < 5; round++)
        {
            Assert.Equal(1L, await CountThroughApplicationAsync(firstTenant, marker));
            Assert.Equal(1L, await CountThroughApplicationAsync(secondTenant, marker));
        }
    }

    [Fact]
    public async Task UnresolvedTenantFailsClosedInsteadOfLeakingOrErroring()
    {
        TenantId tenant = NewTenant();
        var marker = $"Unresolved probe {Guid.NewGuid():N}";
        await CreateCustomerAsync(tenant, marker);

        // A resolved scope first, so the pooled connection has carried a tenant.
        Assert.Equal(1L, await CountThroughApplicationAsync(tenant, marker));

        // Then no ambient tenant: the interceptor clears the setting, and the
        // null-safe policy matches no rows rather than failing the uuid cast.
        await using var scope = _factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<CustomersDbContext>();
        var count = await db.Database
            .SqlQueryRaw<long>("SELECT count(*) AS \"Value\" FROM customers.customers WHERE name = {0}", marker)
            .SingleAsync();

        Assert.Equal(0L, count);
    }

    private async Task<long> CountThroughApplicationAsync(TenantId tenant, string name)
    {
        await using var scope = _factory.Services.CreateAsyncScope();
        using var tenantScope = AmbientTenantContext.Enter(tenant);
        var db = scope.ServiceProvider.GetRequiredService<CustomersDbContext>();
        return await db.Customers.IgnoreQueryFilters().LongCountAsync(customer => customer.Name == name);
    }

    /// <summary>
    /// The runtime role's connection string without pooling, so each test-owned
    /// connection starts as a fresh session with no tenant setting.
    /// </summary>
    private string RawRuntimeConnectionString() =>
        new NpgsqlConnectionStringBuilder(_factory.RuntimeConnectionString) { Pooling = false }.ConnectionString;

    private static async Task<long> CountCustomersAsync(NpgsqlConnection connection, TenantId? tenant)
    {
        await using NpgsqlCommand command = connection.CreateCommand();
        if (tenant is null)
        {
            command.CommandText = "SELECT count(*) FROM customers.customers;";
        }
        else
        {
            command.CommandText = "SELECT count(*) FROM customers.customers WHERE tenant_id = @tenant_id;";
            command.Parameters.AddWithValue("tenant_id", tenant.Value.Value);
        }

        return (long)(await command.ExecuteScalarAsync())!;
    }

    private async Task CreateCustomerAsync(TenantId tenant, string name)
    {
        await using AsyncServiceScope scope = _factory.Services.CreateAsyncScope();
        using AmbientTenantContext.TenantScope tenantScope = AmbientTenantContext.Enter(tenant);
        CustomersDbContext db = scope.ServiceProvider.GetRequiredService<CustomersDbContext>();
        Customer customer = new()
        {
            Name = name,
            CustomerNumber = await scope.ServiceProvider.GetRequiredService<ITenantCounterService>()
                .NextAsync(db, "customer-number"),
        };
        db.Customers.Add(customer);
        await db.SaveChangesAsync();
    }

    private static TenantId NewTenant() => new(Guid.NewGuid());
}