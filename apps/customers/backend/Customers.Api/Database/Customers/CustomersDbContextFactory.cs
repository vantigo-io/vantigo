using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Design;

using Vantigo.Customers.Api.Database;

namespace Vantigo.Customers.Api.Database.Customers;

public sealed class CustomersDbContextFactory : IDesignTimeDbContextFactory<CustomersDbContext>
{
    public CustomersDbContext CreateDbContext(string[] args)
    {
        var dataSource = DesignTimeNpgsqlDataSource.Create("customers");
        var options = new DbContextOptionsBuilder<CustomersDbContext>()
            .UseNpgsql(dataSource)
            .Options;
        return new CustomersDbContext(options);
    }
}
