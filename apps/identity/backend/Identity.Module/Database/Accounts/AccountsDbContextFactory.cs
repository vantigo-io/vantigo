using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Design;

using Vantigo.Identity.Database;

namespace Vantigo.Identity.Database.Accounts;

public sealed class AccountsDbContextFactory : IDesignTimeDbContextFactory<AccountsDbContext>
{
    public AccountsDbContext CreateDbContext(string[] args)
    {
        var dataSource = DesignTimeNpgsqlDataSource.Create("vantigo");
        var options = new DbContextOptionsBuilder<AccountsDbContext>()
            .UseNpgsql(
                dataSource,
                npgsql => npgsql.MigrationsHistoryTable("__EFMigrationsHistory", "identity"))
            .Options;
        return new AccountsDbContext(options);
    }
}