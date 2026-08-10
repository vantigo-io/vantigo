using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Design;

using Vantigo.Products.Database;

namespace Vantigo.Products.Database.Products;

public sealed class ProductsDbContextFactory : IDesignTimeDbContextFactory<ProductsDbContext>
{
    public ProductsDbContext CreateDbContext(string[] args)
    {
        var dataSource = DesignTimeNpgsqlDataSource.Create("vantigo");
        var options = new DbContextOptionsBuilder<ProductsDbContext>()
            .UseNpgsql(dataSource, npgsql => npgsql.MigrationsHistoryTable("__EFMigrationsHistory", "products"))
            .Options;
        return new ProductsDbContext(options);
    }
}