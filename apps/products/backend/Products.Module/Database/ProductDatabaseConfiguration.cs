using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection.Extensions;
using Microsoft.Extensions.Options;

using Npgsql;

using Vantigo.Configuration;
using Vantigo.Products.Database.Products;
using Vantigo.Products.Endpoints;

namespace Vantigo.Products.Database;

public static class ProductDatabaseConfiguration
{
    public static IServiceCollection AddProductsModule(this IServiceCollection services)
    {
        services.TryAddSingleton<NpgsqlDataSource>(serviceProvider =>
        {
            var connectionStrings = serviceProvider.GetRequiredService<IOptions<ConnectionStringsOptions>>().Value;
            var connectionString = connectionStrings.Resolve("products");

            // One application-level data source gives both EF contexts the same ADO.NET
            // pool while retaining separate DbContext lifetimes and migration histories.
            var dataSourceBuilder = new NpgsqlDataSourceBuilder(connectionString);
            dataSourceBuilder.EnableDynamicJson();
            return dataSourceBuilder.Build();
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

    public static async Task SeedProductsAsync(this IServiceProvider services, CancellationToken cancellationToken = default)
    {
        await using var scope = services.CreateAsyncScope();
        var seedOptions = scope.ServiceProvider.GetRequiredService<IOptions<DevelopmentSeedOptions>>().Value;
        if (seedOptions.Enabled)
            await DevelopmentSeed.DevelopmentDataSeeder.SeedProductsAsync(services, cancellationToken);
    }
}