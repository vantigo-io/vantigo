#pragma warning disable EF1001

using Microsoft.EntityFrameworkCore.Infrastructure;
using Microsoft.Extensions.DependencyInjection;

using Npgsql;
using Npgsql.EntityFrameworkCore.PostgreSQL.Infrastructure.Internal;

using Vantigo.Customers.Api.Database.Accounts;
using Vantigo.Customers.Api.Database.Customers;

namespace Vantigo.Customers.Api.Tests.Integration;

[Collection(CustomersApiCollection.Name)]
public sealed class DatabaseContextRegistrationTests
{
    private readonly CustomersApiFactory _factory;

    public DatabaseContextRegistrationTests(CustomersApiFactory factory) => _factory = factory;

    [Fact]
    public void CustomersAndAccountsContextsUseTheRegisteredSharedDataSource()
    {
        using var scope = _factory.Services.CreateScope();
        var dataSource = scope.ServiceProvider.GetRequiredService<NpgsqlDataSource>();
        var customers = scope.ServiceProvider.GetRequiredService<CustomersDbContext>();
        var accounts = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();

        var customersOptions = customers.Database.GetService<IDbContextOptions>();
        var accountsOptions = accounts.Database.GetService<IDbContextOptions>();

        Assert.Same(dataSource, customersOptions.FindExtension<NpgsqlOptionsExtension>()!.DataSource);
        Assert.Same(dataSource, accountsOptions.FindExtension<NpgsqlOptionsExtension>()!.DataSource);
    }
}

#pragma warning restore EF1001