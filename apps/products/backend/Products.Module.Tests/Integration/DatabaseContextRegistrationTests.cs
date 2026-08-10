#pragma warning disable EF1001

using Microsoft.EntityFrameworkCore.Infrastructure;
using Microsoft.Extensions.DependencyInjection;

using Npgsql;
using Npgsql.EntityFrameworkCore.PostgreSQL.Infrastructure.Internal;

using Vantigo.Products.Database.Products;

namespace Vantigo.Products.Module.Tests.Integration;

[Collection(ProductsModuleCollection.Name)]
public sealed class DatabaseContextRegistrationTests
{
    private readonly ProductsModuleFactory _factory;

    public DatabaseContextRegistrationTests(ProductsModuleFactory factory) => _factory = factory;

    [Fact]
    public void ProductsContextUsesTheRegisteredSharedDataSource()
    {
        using var scope = _factory.Services.CreateScope();
        var dataSource = scope.ServiceProvider.GetRequiredService<NpgsqlDataSource>();
        var products = scope.ServiceProvider.GetRequiredService<ProductsDbContext>();
        var productsOptions = products.Database.GetService<IDbContextOptions>();

        Assert.Same(dataSource, productsOptions.FindExtension<NpgsqlOptionsExtension>()!.DataSource);
    }
}

#pragma warning restore EF1001