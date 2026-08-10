using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Design;

using Vantigo.Energy.Database;

namespace Vantigo.Energy.Database.Energy;

public sealed class EnergyDbContextFactory : IDesignTimeDbContextFactory<EnergyDbContext>
{
    public EnergyDbContext CreateDbContext(string[] args)
    {
        var dataSource = DesignTimeNpgsqlDataSource.Create("vantigo");
        var options = new DbContextOptionsBuilder<EnergyDbContext>()
            .UseNpgsql(dataSource, npgsql => npgsql.MigrationsHistoryTable("__EFMigrationsHistory", "energy"))
            .Options;
        return new EnergyDbContext(options);
    }
}