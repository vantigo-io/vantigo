using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Design;

using Vantigo.Customers.Database;

namespace Vantigo.Customers.Database.Customers;

public sealed class CustomersDbContextFactory : IDesignTimeDbContextFactory<CustomersDbContext>
{
    public CustomersDbContext CreateDbContext(string[] args)
    {
        var dataSource = DesignTimeNpgsqlDataSource.Create("vantigo");
        var options = new DbContextOptionsBuilder<CustomersDbContext>()
            .UseNpgsql(dataSource, npgsql => npgsql.MigrationsHistoryTable("__EFMigrationsHistory", "customers"))
            .Options;
        return new CustomersDbContext(options);
    }
}