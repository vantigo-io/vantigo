#pragma warning disable EF1001

using Microsoft.EntityFrameworkCore.Infrastructure;
using Microsoft.Extensions.DependencyInjection;

using Npgsql;
using Npgsql.EntityFrameworkCore.PostgreSQL.Infrastructure.Internal;

using Vantigo.Products.Api.Database.Accounts;
using Vantigo.Products.Api.Database.Products;

namespace Vantigo.Products.Api.Tests.Integration;

[Collection(ProductsApiCollection.Name)]
public sealed class DatabaseContextRegistrationTests
{
    private readonly ProductsApiFactory _factory;

    public DatabaseContextRegistrationTests(ProductsApiFactory factory) => _factory = factory;

    [Fact]
    public void ProductsAndAccountsContextsUseTheRegisteredSharedDataSource()
    {
        using var scope = _factory.Services.CreateScope();
        var dataSource = scope.ServiceProvider.GetRequiredService<NpgsqlDataSource>();
        var products = scope.ServiceProvider.GetRequiredService<ProductsDbContext>();
        var accounts = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();

        var productsOptions = products.Database.GetService<IDbContextOptions>();
        var accountsOptions = accounts.Database.GetService<IDbContextOptions>();

        Assert.Same(dataSource, productsOptions.FindExtension<NpgsqlOptionsExtension>()!.DataSource);
        Assert.Same(dataSource, accountsOptions.FindExtension<NpgsqlOptionsExtension>()!.DataSource);
    }
}

#pragma warning restore EF1001