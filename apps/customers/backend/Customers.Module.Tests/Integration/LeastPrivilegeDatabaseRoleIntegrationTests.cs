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
/// Characterizes what a least-privilege PostgreSQL runtime role
/// (NOSUPERUSER NOBYPASSRLS, owning nothing) actually sees today. The tenant RLS
/// policies match on the transaction-local <c>app.tenant_id</c> setting, which
/// <c>TenantConnectionInterceptor</c> only writes once a transaction has started,
/// so transaction-less reads — the normal endpoint path — see nothing at all.
/// These assertions document the defect tracked in
/// https://github.com/vantigo-io/vantigo/issues/6 and must be inverted, not
/// deleted, once the tenant setting becomes connection-scoped.
/// </summary>
[Collection(CustomersApiCollection.Name)]
public sealed class LeastPrivilegeDatabaseRoleIntegrationTests
{
    private const string RuntimeRolePassword = "least-privilege-runtime-password";

    private readonly CustomersApiFactory _factory;

    public LeastPrivilegeDatabaseRoleIntegrationTests(CustomersApiFactory factory) => _factory = factory;

    [Fact]
    public async Task SuperuserConnectionBypassesTenantRowLevelSecurity()
    {
        TenantId tenant = NewTenant();
        await CreateCustomerAsync(tenant, "Superuser visible");

        await using NpgsqlConnection connection = new(_factory.SuperuserConnectionString);
        await connection.OpenAsync();

        Assert.True(await IsSuperuserAsync(connection));
        Assert.True(await CountCustomersAsync(connection, tenant) > 0);
    }

    [Fact]
    public async Task LeastPrivilegeRoleSeesNoRowsWithoutAnExplicitTransaction()
    {
        TenantId tenant = NewTenant();
        await CreateCustomerAsync(tenant, "Runtime role read");
        string connectionString = await CreateRuntimeRoleAsync();

        await using NpgsqlConnection connection = new(connectionString);
        await connection.OpenAsync();

        Assert.False(await IsSuperuserAsync(connection));

        // The known defect: no transaction means no app.tenant_id, and the
        // policy matches no row rather than failing loudly.
        Assert.Equal(0L, await CountCustomersAsync(connection, tenant));
        Assert.Equal(0L, await CountCustomersAsync(connection, null));
    }

    [Fact]
    public async Task LeastPrivilegeRoleIsolatesTenantsInsideATransactionThatSetsTheTenantSetting()
    {
        TenantId firstTenant = NewTenant();
        TenantId secondTenant = NewTenant();
        await CreateCustomerAsync(firstTenant, "First tenant");
        await CreateCustomerAsync(secondTenant, "Second tenant");
        string connectionString = await CreateRuntimeRoleAsync();

        await using NpgsqlConnection connection = new(connectionString);
        await connection.OpenAsync();
        await using NpgsqlTransaction transaction = await connection.BeginTransactionAsync();
        await SetTenantSettingAsync(connection, transaction, firstTenant);

        Assert.Equal(1L, await CountCustomersAsync(connection, firstTenant, transaction));
        Assert.Equal(0L, await CountCustomersAsync(connection, secondTenant, transaction));
        Assert.Equal(1L, await CountCustomersAsync(connection, null, transaction));
    }

    private static async Task<bool> IsSuperuserAsync(NpgsqlConnection connection)
    {
        await using NpgsqlCommand command = connection.CreateCommand();
        command.CommandText = "SELECT rolsuper OR rolbypassrls FROM pg_roles WHERE rolname = current_user;";
        return (bool)(await command.ExecuteScalarAsync())!;
    }

    private static async Task SetTenantSettingAsync(
        NpgsqlConnection connection,
        NpgsqlTransaction transaction,
        TenantId tenant)
    {
        await using NpgsqlCommand command = connection.CreateCommand();
        command.Transaction = transaction;
        command.CommandText = "SELECT set_config('app.tenant_id', @tenant_id, true);";
        command.Parameters.AddWithValue("tenant_id", tenant.Value.ToString());
        await command.ExecuteNonQueryAsync();
    }

    private static async Task<long> CountCustomersAsync(
        NpgsqlConnection connection,
        TenantId? tenant,
        NpgsqlTransaction? transaction = null)
    {
        await using NpgsqlCommand command = connection.CreateCommand();
        command.Transaction = transaction;
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

    /// <summary>
    /// Creates the compose-equivalent runtime role: no superuser, no BYPASSRLS,
    /// table DML but no ownership. Returns a connection string for it.
    /// </summary>
    private async Task<string> CreateRuntimeRoleAsync()
    {
        NpgsqlConnectionStringBuilder builder = new(_factory.SuperuserConnectionString);
        string roleName = $"vantigo_app_{Guid.NewGuid():N}";

        await using (NpgsqlConnection connection = new(builder.ConnectionString))
        {
            await connection.OpenAsync();
            await using NpgsqlCommand command = connection.CreateCommand();
            command.CommandText = $"""
                CREATE ROLE "{roleName}" LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS NOINHERIT
                    PASSWORD '{RuntimeRolePassword}';
                GRANT CONNECT ON DATABASE "{builder.Database}" TO "{roleName}";
                GRANT USAGE ON SCHEMA customers TO "{roleName}";
                GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA customers TO "{roleName}";
                """;
            await command.ExecuteNonQueryAsync();
        }

        builder.Username = roleName;
        builder.Password = RuntimeRolePassword;
        builder.Pooling = false;
        return builder.ConnectionString;
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