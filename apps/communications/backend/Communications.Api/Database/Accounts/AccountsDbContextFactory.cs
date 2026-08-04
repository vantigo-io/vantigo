using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Design;

using Vantigo.Communications.Api.Database;

namespace Vantigo.Communications.Api.Database.Accounts;

public sealed class AccountsDbContextFactory : IDesignTimeDbContextFactory<AccountsDbContext>
{
    public AccountsDbContext CreateDbContext(string[] args)
    {
        var dataSource = DesignTimeNpgsqlDataSource.Create("communications");
        var options = new DbContextOptionsBuilder<AccountsDbContext>().UseNpgsql(
            dataSource, npgsql => npgsql.MigrationsHistoryTable("__EFMigrationsHistory", "accounts")).Options;
        return new AccountsDbContext(options);
    }
}