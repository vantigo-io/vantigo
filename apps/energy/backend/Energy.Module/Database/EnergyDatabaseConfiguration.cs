using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection.Extensions;

using Npgsql;

using Vantigo.Energy.Database.Energy;
using Vantigo.Energy.Endpoints;

namespace Vantigo.Energy.Database;

public static class EnergyDatabaseConfiguration
{
    public static IServiceCollection AddEnergyModule(this IServiceCollection services, IConfiguration configuration)
    {
        services.TryAddSingleton<NpgsqlDataSource>(_ =>
        {
            var connectionString = configuration.GetConnectionString("vantigo") ??
                configuration.GetConnectionString("energy") ?? configuration.GetConnectionString("Postgresql");
            if (string.IsNullOrWhiteSpace(connectionString))
                throw new InvalidOperationException("ConnectionStrings:vantigo is required.");
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

    public static async Task SeedEnergyAsync(this IServiceProvider services, IConfiguration configuration, CancellationToken cancellationToken = default)
    {
        if (configuration.GetValue("Development:Seed:Enabled", true))
            await DevelopmentSeed.DevelopmentDataSeeder.SeedEnergyAsync(services, cancellationToken);
    }
}