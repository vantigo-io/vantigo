using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Design;

using Vantigo.Communications.Database;
using Vantigo.Tenancy.Abstractions;

namespace Vantigo.Communications.Database.Communications;

public sealed class CommunicationsDbContextFactory : IDesignTimeDbContextFactory<CommunicationsDbContext>
{
    public CommunicationsDbContext CreateDbContext(string[] args)
    {
        var dataSource = DesignTimeNpgsqlDataSource.Create("vantigo");
        return new CommunicationsDbContext(new DbContextOptionsBuilder<CommunicationsDbContext>()
            .UseNpgsql(dataSource, npgsql => npgsql.MigrationsHistoryTable("__EFMigrationsHistory", "communications"))
            .Options, new DesignTimeTenantContext());
    }

    private sealed class DesignTimeTenantContext : ITenantContext
    {
        public bool IsResolved => true;

        public TenantId Current => new(Guid.Parse("00000000-0000-0000-0000-000000000001"));
    }
}