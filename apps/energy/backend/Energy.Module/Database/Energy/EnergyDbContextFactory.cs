using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Design;

using Vantigo.Energy.Database;
using Vantigo.Tenancy.Abstractions;

namespace Vantigo.Energy.Database.Energy;

public sealed class EnergyDbContextFactory : IDesignTimeDbContextFactory<EnergyDbContext>
{
    public EnergyDbContext CreateDbContext(string[] args)
    {
        var dataSource = DesignTimeNpgsqlDataSource.Create("vantigo");
        var options = new DbContextOptionsBuilder<EnergyDbContext>()
            .UseNpgsql(dataSource, npgsql => npgsql.MigrationsHistoryTable("__EFMigrationsHistory", "energy"))
            .Options;
        return new EnergyDbContext(options, new DesignTimeTenantContext());
    }

    private sealed class DesignTimeTenantContext : ITenantContext
    {
        public bool IsResolved => true;

        public TenantId Current => new(Guid.Parse("00000000-0000-0000-0000-000000000001"));
    }
}