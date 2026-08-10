using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection.Extensions;

using Npgsql;

using Vantigo.Customers.Database.Customers;
using Vantigo.Customers.Endpoints;
using Vantigo.Customers.Services;
namespace Vantigo.Customers.Database;

public static class CustomerDatabaseConfiguration
{
    public static IServiceCollection AddCustomersModule(
        this IServiceCollection services,
        IConfiguration configuration)
    {
        services.TryAddSingleton<NpgsqlDataSource>(_ =>
        {
            var connectionString = configuration.GetConnectionString("vantigo") ??
                configuration.GetConnectionString("customers") ??
                configuration.GetConnectionString("Postgresql");
            if (string.IsNullOrWhiteSpace(connectionString))
            {
                throw new InvalidOperationException("ConnectionStrings:vantigo is required.");
            }

            // One application-level data source gives both EF contexts the same ADO.NET
            // pool while retaining separate DbContext lifetimes and migration histories.
            return NpgsqlDataSource.Create(connectionString);
        });
        services.AddDbContext<CustomersDbContext>((serviceProvider, options) =>
            options.UseNpgsql(
                serviceProvider.GetRequiredService<NpgsqlDataSource>(),
                npgsql => npgsql.MigrationsHistoryTable("__EFMigrationsHistory", "customers")));
        services.AddCustomerApiVersioning();
        services.AddCustomerTimeline();
        services.AddHttpClient(Vantigo.Customers.Endpoints.Lookup.BrregLookupEndpoint.HttpClientName, client =>
        {
            client.BaseAddress = new Uri(configuration["Brreg:BaseUrl"] ?? "https://data.brreg.no");
            client.Timeout = TimeSpan.FromSeconds(5);
        });

        return services;
    }

    public static async Task MigrateAsync(this IServiceProvider services)
    {
        await using var scope = services.CreateAsyncScope();
        var dbContext = scope.ServiceProvider.GetRequiredService<CustomersDbContext>();
        await dbContext.Database.MigrateAsync();
    }
}