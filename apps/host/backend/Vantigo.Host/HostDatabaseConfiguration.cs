using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.Options;

using Npgsql;

using Vantigo.Configuration;
using Vantigo.Identity.Database;
using Vantigo.Identity.Database.Accounts;

namespace Vantigo.Host;

internal static class HostDatabaseConfiguration
{
    internal static IServiceCollection AddHostDatabases(this IServiceCollection services)
    {
        services.AddSingleton<NpgsqlDataSource>(serviceProvider =>
        {
            var connectionStrings = serviceProvider.GetRequiredService<IOptions<ConnectionStringsOptions>>().Value;
            var connectionString = connectionStrings.ResolveRuntime();
            return NpgsqlDataSource.Create(connectionString);
        });
        services.AddSingleton<ICommunicationsSchemaResetSqlExecutor, NpgsqlCommunicationsSchemaResetSqlExecutor>();
        services.AddSingleton<ICommunicationsSchemaResetter, CommunicationsSchemaResetter>();
        services.AddSingleton<ICommunicationsMigrationRunner, CommunicationsMigrationRunner>();
        services.AddVantigoIdentityDatabase();
        return services;
    }

    internal static async Task MigrateIdentityAsync(this IServiceProvider services)
    {
        await using var scope = services.CreateAsyncScope();
        var connectionStrings = scope.ServiceProvider.GetRequiredService<IOptions<ConnectionStringsOptions>>().Value;
        if (connectionStrings.ResolveMigrationsOverride() is { } migrations)
        {
            // Migrations run as the schema-owner role while the runtime pool
            // stays on the least-privilege role; see docs/tenancy.md.
            var options = new DbContextOptionsBuilder<AccountsDbContext>()
                .UseNpgsql(migrations, npgsql => npgsql.MigrationsHistoryTable("__EFMigrationsHistory", "identity"))
                .Options;
            await using var migrationContext = new AccountsDbContext(options);
            await migrationContext.Database.MigrateAsync();
            return;
        }

        var dbContext = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        await dbContext.Database.MigrateAsync();
    }
}