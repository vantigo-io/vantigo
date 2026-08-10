using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Design;

using Vantigo.Communications.Database;

namespace Vantigo.Communications.Database.Communications;

public sealed class CommunicationsDbContextFactory : IDesignTimeDbContextFactory<CommunicationsDbContext>
{
    public CommunicationsDbContext CreateDbContext(string[] args)
    {
        var dataSource = DesignTimeNpgsqlDataSource.Create("vantigo");
        return new CommunicationsDbContext(new DbContextOptionsBuilder<CommunicationsDbContext>()
            .UseNpgsql(dataSource, npgsql => npgsql.MigrationsHistoryTable("__EFMigrationsHistory", "communications"))
            .Options);
    }
}