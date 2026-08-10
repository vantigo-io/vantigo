using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection.Extensions;

using Npgsql;

using Vantigo.Products.Database.Products;
using Vantigo.Products.Endpoints;

namespace Vantigo.Products.Database;

public static class ProductDatabaseConfiguration
{
    public static IServiceCollection AddProductsModule(
        this IServiceCollection services,
        IConfiguration configuration)
    {
        services.TryAddSingleton<NpgsqlDataSource>(_ =>
        {
            var connectionString = configuration.GetConnectionString("vantigo") ??
                configuration.GetConnectionString("products") ??
                configuration.GetConnectionString("Postgresql");
            if (string.IsNullOrWhiteSpace(connectionString))
            {
                throw new InvalidOperationException("ConnectionStrings:vantigo is required.");
            }

            // One application-level data source gives both EF contexts the same ADO.NET
            // pool while retaining separate DbContext lifetimes and migration histories.
            return NpgsqlDataSource.Create(connectionString);
        });
        services.AddDbContext<ProductsDbContext>((serviceProvider, options) =>
            options.UseNpgsql(serviceProvider.GetRequiredService<NpgsqlDataSource>(),
                npgsql => npgsql.MigrationsHistoryTable("__EFMigrationsHistory", "products")));
        services.AddProductApiVersioning();

        return services;
    }

    public static async Task MigrateProductsAsync(this IServiceProvider services)
    {
        await using var scope = services.CreateAsyncScope();
        var dbContext = scope.ServiceProvider.GetRequiredService<ProductsDbContext>();
        await dbContext.Database.MigrateAsync();
    }

    public static async Task SeedProductsAsync(this IServiceProvider services, IConfiguration configuration, CancellationToken cancellationToken = default)
    {
        if (configuration.GetValue("Development:Seed:Enabled", true))
            await DevelopmentSeed.DevelopmentDataSeeder.SeedProductsAsync(services, cancellationToken);
    }
}