using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Infrastructure;
using Microsoft.Extensions.DependencyInjection.Extensions;
using Microsoft.Extensions.Options;

using Npgsql;

using Vantigo.Configuration;
using Vantigo.Contracts.Authorization;
using Vantigo.Customers.Authorization;
using Vantigo.Customers.Database.Customers;
using Vantigo.Customers.Endpoints;
using Vantigo.Customers.Services;
using Vantigo.Tenancy.EntityFramework;

namespace Vantigo.Customers.Database;

public static class CustomersDatabaseConfiguration
{
    public static IServiceCollection AddCustomersModule(this IServiceCollection services)
    {
        services.AddVantigoTenancyEntityFramework();
        services.TryAddSingleton<NpgsqlDataSource>(serviceProvider =>
        {
            var connectionStrings = serviceProvider.GetRequiredService<IOptions<ConnectionStringsOptions>>().Value;
            var connectionString = connectionStrings.Resolve("customers");

            // One application-level data source gives both EF contexts the same ADO.NET
            // pool while retaining separate DbContext lifetimes and migration histories.
            return NpgsqlDataSource.Create(connectionString);
        });
        services.AddDbContext<CustomersDbContext>((serviceProvider, options) =>
            options.UseNpgsql(
                serviceProvider.GetRequiredService<NpgsqlDataSource>(),
                npgsql => npgsql.MigrationsHistoryTable("__EFMigrationsHistory", "customers"))
                .ReplaceService<IModelCacheKeyFactory, CustomersModelCacheKeyFactory>()
                .UseTenancy(serviceProvider));
        services.AddCustomerApiVersioning();
        services.AddCustomerTimeline();
        services.AddSingleton<IPermissionCatalogContributor, CustomerPermissionCatalogContributor>();
        services.AddHttpClient(Vantigo.Customers.Endpoints.Lookup.BrregLookupEndpoint.HttpClientName, (serviceProvider, client) =>
        {
            var brreg = serviceProvider.GetRequiredService<IOptions<BrregLookupOptions>>().Value;
            client.BaseAddress = new Uri(brreg.BaseUrl);
            client.Timeout = TimeSpan.FromSeconds(5);
        });

        return services;
    }

    public static async Task MigrateAsync(this IServiceProvider services)
    {
        await using var scope = services.CreateAsyncScope();
        var connectionStrings = scope.ServiceProvider.GetRequiredService<IOptions<ConnectionStringsOptions>>().Value;
        if (connectionStrings.ResolveMigrationsOverride("customers") is { } migrations)
        {
            // Migrations run as the schema-owner role while the runtime pool
            // stays on the least-privilege role; see docs/tenancy.md.
            var options = new DbContextOptionsBuilder<CustomersDbContext>()
                .UseNpgsql(migrations, npgsql => npgsql.MigrationsHistoryTable("__EFMigrationsHistory", "customers"))
                .Options;
            await using var migrationContext = new CustomersDbContext(options);
            await migrationContext.Database.MigrateAsync();
            return;
        }

        var dbContext = scope.ServiceProvider.GetRequiredService<CustomersDbContext>();
        await dbContext.Database.MigrateAsync();
    }
}