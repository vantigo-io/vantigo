using Microsoft.EntityFrameworkCore;

using Npgsql;

using Vantigo.Products.Api.Database.Accounts;
using Vantigo.Products.Api.Database.Products;

namespace Vantigo.Products.Api.Database;

internal static class ProductDatabaseConfiguration
{
    internal static IServiceCollection AddProductDatabases(
        this IServiceCollection services,
        IConfiguration configuration)
    {
        services.AddSingleton<NpgsqlDataSource>(_ =>
        {
            var connectionString = configuration.GetConnectionString("Postgresql");
            if (string.IsNullOrWhiteSpace(connectionString))
            {
                throw new InvalidOperationException("ConnectionStrings:Postgresql is required.");
            }

            // One application-level data source gives both EF contexts the same ADO.NET
            // pool while retaining separate DbContext lifetimes and migration histories.
            return NpgsqlDataSource.Create(connectionString);
        });
        services.AddDbContext<ProductsDbContext>((serviceProvider, options) =>
            options.UseNpgsql(serviceProvider.GetRequiredService<NpgsqlDataSource>()));
        services.AddDbContext<AccountsDbContext>((serviceProvider, options) =>
            options.UseNpgsql(
                serviceProvider.GetRequiredService<NpgsqlDataSource>(),
                npgsql => npgsql.MigrationsHistoryTable("__EFMigrationsHistory", "accounts")));

        return services;
    }

    internal static async Task MigrateProductDatabasesAsync(this WebApplication app)
    {
        await using var scope = app.Services.CreateAsyncScope();
        var dbContext = scope.ServiceProvider.GetRequiredService<ProductsDbContext>();
        await dbContext.Database.MigrateAsync();
        var accountsDbContext = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        await accountsDbContext.Database.MigrateAsync();
    }
}