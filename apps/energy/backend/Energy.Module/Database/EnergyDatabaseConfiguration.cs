using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection.Extensions;
using Microsoft.Extensions.Options;

using Npgsql;

using Vantigo.Configuration;
using Vantigo.Contracts.Authorization;
using Vantigo.Energy.Authorization;
using Vantigo.Energy.Database.Energy;
using Vantigo.Energy.Endpoints;
using Vantigo.Tenancy.EntityFramework;

namespace Vantigo.Energy.Database;

public static class EnergyDatabaseConfiguration
{
    public static IServiceCollection AddEnergyModule(this IServiceCollection services)
    {
        services.AddVantigoTenancyEntityFramework();
        services.AddSingleton<IPermissionCatalogContributor, EnergyPermissionCatalogContributor>();
        services.TryAddSingleton<NpgsqlDataSource>(serviceProvider =>
        {
            var connectionStrings = serviceProvider.GetRequiredService<IOptions<ConnectionStringsOptions>>().Value;
            var connectionString = connectionStrings.Resolve("energy");
            var builder = new NpgsqlDataSourceBuilder(connectionString);
            builder.EnableDynamicJson();
            return builder.Build();
        });
        services.AddDbContext<EnergyDbContext>((provider, options) =>
            options.UseNpgsql(
                    provider.GetRequiredService<NpgsqlDataSource>(),
                    npgsql => npgsql.MigrationsHistoryTable("__EFMigrationsHistory", "energy"))
                .UseTenancy(provider));
        services.AddEnergyApiVersioning();
        return services;
    }

    public static async Task MigrateEnergyAsync(this IServiceProvider services)
    {
        await using var scope = services.CreateAsyncScope();
        var connectionStrings = scope.ServiceProvider.GetRequiredService<IOptions<ConnectionStringsOptions>>().Value;
        if (connectionStrings.ResolveMigrationsOverride("energy") is { } migrations)
        {
            // Migrations run as the schema-owner role while the runtime pool
            // stays on the least-privilege role; see docs/tenancy.md.
            var dataSourceBuilder = new NpgsqlDataSourceBuilder(migrations);
            dataSourceBuilder.EnableDynamicJson();
            await using var dataSource = dataSourceBuilder.Build();
            var options = new DbContextOptionsBuilder<EnergyDbContext>()
                .UseNpgsql(dataSource, npgsql => npgsql.MigrationsHistoryTable("__EFMigrationsHistory", "energy"))
                .Options;
            await using var migrationContext = new EnergyDbContext(options, new MigrationTenantContext());
            await migrationContext.Database.MigrateAsync();
            return;
        }

        await scope.ServiceProvider.GetRequiredService<EnergyDbContext>().Database.MigrateAsync();
    }

    private sealed class MigrationTenantContext : Vantigo.Tenancy.Abstractions.ITenantContext
    {
        public bool IsResolved => false;

        public Vantigo.Tenancy.Abstractions.TenantId Current =>
            throw new InvalidOperationException("Migrations run without a tenant.");
    }

    public static async Task SeedEnergyAsync(this IServiceProvider services, CancellationToken cancellationToken = default)
    {
        await using var scope = services.CreateAsyncScope();
        var seedOptions = scope.ServiceProvider.GetRequiredService<IOptions<DevelopmentSeedOptions>>().Value;
        if (seedOptions.Enabled)
            await DevelopmentSeed.DevelopmentDataSeeder.SeedEnergyAsync(services, cancellationToken);
    }
}