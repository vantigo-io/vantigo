using Microsoft.EntityFrameworkCore;

using Npgsql;

using Vantigo.Customers.Api.Database.Accounts;
using Vantigo.Customers.Api.Database.Customers;

namespace Vantigo.Customers.Api.Database;

internal static class CustomerDatabaseConfiguration
{
    internal static IServiceCollection AddCustomerDatabases(
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
        services.AddDbContext<CustomersDbContext>((serviceProvider, options) =>
            options.UseNpgsql(serviceProvider.GetRequiredService<NpgsqlDataSource>()));
        services.AddDbContext<AccountsDbContext>((serviceProvider, options) =>
            options.UseNpgsql(
                serviceProvider.GetRequiredService<NpgsqlDataSource>(),
                npgsql => npgsql.MigrationsHistoryTable("__EFMigrationsHistory", "accounts")));

        return services;
    }

    internal static async Task MigrateCustomerDatabasesAsync(this WebApplication app)
    {
        await using var scope = app.Services.CreateAsyncScope();
        var dbContext = scope.ServiceProvider.GetRequiredService<CustomersDbContext>();
        await dbContext.Database.MigrateAsync();
        var accountsDbContext = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        await accountsDbContext.Database.MigrateAsync();
    }
}
