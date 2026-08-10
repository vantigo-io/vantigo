using Microsoft.EntityFrameworkCore;

using Npgsql;

using Vantigo.Identity.Database;
using Vantigo.Identity.Database.Accounts;

namespace Vantigo.Host;

internal static class HostDatabaseConfiguration
{
    internal static IServiceCollection AddHostDatabases(this IServiceCollection services, IConfiguration configuration)
    {
        services.AddSingleton<NpgsqlDataSource>(_ =>
        {
            var connectionString = configuration.GetConnectionString("vantigo");
            if (string.IsNullOrWhiteSpace(connectionString))
            {
                throw new InvalidOperationException("ConnectionStrings:vantigo is required.");
            }

            return NpgsqlDataSource.Create(connectionString);
        });
        services.AddVantigoIdentityDatabase(configuration);
        return services;
    }

    internal static async Task MigrateIdentityAsync(this IServiceProvider services)
    {
        await using var scope = services.CreateAsyncScope();
        await scope.ServiceProvider.GetRequiredService<AccountsDbContext>().Database.MigrateAsync();
    }
}