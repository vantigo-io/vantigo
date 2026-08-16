using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection.Extensions;
using Microsoft.Extensions.Options;

using Npgsql;

using Vantigo.Configuration;
using Vantigo.Contracts.Authorization;
using Vantigo.Contracts.Products;
using Vantigo.Products.Authorization;
using Vantigo.Products.Database.Products;
using Vantigo.Products.Endpoints;
using Vantigo.Products.Services;
using Vantigo.Tenancy;
using Vantigo.Tenancy.Abstractions;
using Vantigo.Tenancy.EntityFramework;

namespace Vantigo.Products.Database;

public static class ProductsDatabaseConfiguration
{
    public static IServiceCollection AddProductsModule(this IServiceCollection services)
    {
        services.AddVantigoTenancyEntityFramework();
        services.AddSingleton<IPermissionCatalogContributor, ProductsPermissionCatalogContributor>();
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
        {
            options.UseTenancy(serviceProvider);
            options.UseNpgsql(serviceProvider.GetRequiredService<NpgsqlDataSource>(),
                npgsql => npgsql.MigrationsHistoryTable("__EFMigrationsHistory", "products"));
        });
        services.AddScoped<IProductCatalog, ProductCatalog>();
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
        {
            var tenantDirectory = scope.ServiceProvider.GetRequiredService<ITenantDirectory>();
            using var tenantScope = AmbientTenantContext.Enter(
                await tenantDirectory.GetDefaultTenantAsync(cancellationToken));
            await DevelopmentSeed.DevelopmentDataSeeder.SeedProductsAsync(services, cancellationToken);
        }
    }
}