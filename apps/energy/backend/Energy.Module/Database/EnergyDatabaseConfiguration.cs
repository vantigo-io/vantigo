using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection.Extensions;
using Microsoft.Extensions.Options;

using Npgsql;

using Vantigo.Configuration;
using Vantigo.Energy.Database.Energy;
using Vantigo.Energy.Endpoints;

namespace Vantigo.Energy.Database;

public static class EnergyDatabaseConfiguration
{
    public static IServiceCollection AddEnergyModule(this IServiceCollection services)
    {
        services.TryAddSingleton<NpgsqlDataSource>(serviceProvider =>
        {
            var connectionStrings = serviceProvider.GetRequiredService<IOptions<ConnectionStringsOptions>>().Value;
            var connectionString = connectionStrings.Resolve("energy");
            var builder = new NpgsqlDataSourceBuilder(connectionString);
            builder.EnableDynamicJson();
            return builder.Build();
        });
        services.AddDbContext<EnergyDbContext>((provider, options) => options.UseNpgsql(
            provider.GetRequiredService<NpgsqlDataSource>(),
            npgsql => npgsql.MigrationsHistoryTable("__EFMigrationsHistory", "energy")));
        services.AddEnergyApiVersioning();
        return services;
    }

    public static async Task MigrateEnergyAsync(this IServiceProvider services)
    {
        await using var scope = services.CreateAsyncScope();
        await scope.ServiceProvider.GetRequiredService<EnergyDbContext>().Database.MigrateAsync();
    }

    public static async Task SeedEnergyAsync(this IServiceProvider services, CancellationToken cancellationToken = default)
    {
        await using var scope = services.CreateAsyncScope();
        var seedOptions = scope.ServiceProvider.GetRequiredService<IOptions<DevelopmentSeedOptions>>().Value;
        if (seedOptions.Enabled)
            await DevelopmentSeed.DevelopmentDataSeeder.SeedEnergyAsync(services, cancellationToken);
    }
}