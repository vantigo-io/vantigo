using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Design;

using Vantigo.Products.Api.Database;

namespace Vantigo.Products.Api.Database.Products;

public sealed class ProductsDbContextFactory : IDesignTimeDbContextFactory<ProductsDbContext>
{
    public ProductsDbContext CreateDbContext(string[] args)
    {
        var dataSource = DesignTimeNpgsqlDataSource.Create("products");
        var options = new DbContextOptionsBuilder<ProductsDbContext>()
            .UseNpgsql(dataSource)
            .Options;
        return new ProductsDbContext(options);
    }
}