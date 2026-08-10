using Microsoft.EntityFrameworkCore;

using Npgsql;
using Npgsql.EntityFrameworkCore.PostgreSQL.Infrastructure;

using Vantigo.Identity.Database.Accounts;

namespace Vantigo.Identity.Database;

public static class IdentityDatabaseServiceCollectionExtensions
{
    public static IServiceCollection AddVantigoIdentityDatabase(
        this IServiceCollection services,
        IConfiguration configuration)
    {
        services.AddDbContext<AccountsDbContext>((serviceProvider, options) =>
        {
            var source = serviceProvider.GetService<NpgsqlDataSource>();
            if (source is not null)
            {
                options.UseNpgsql(source, ConfigureNpgsql);
                return;
            }

            var connectionString = configuration.GetConnectionString("vantigo") ??
                configuration.GetConnectionString("customers") ??
                configuration.GetConnectionString("Postgresql");
            if (string.IsNullOrWhiteSpace(connectionString))
            {
                throw new InvalidOperationException(
                    "ConnectionStrings:vantigo (or the transitional customers/Postgresql connection string) is required.");
            }

            options.UseNpgsql(connectionString, ConfigureNpgsql);
        });

        return services;
    }

    private static void ConfigureNpgsql(NpgsqlDbContextOptionsBuilder options) =>
        options.MigrationsHistoryTable("__EFMigrationsHistory", "identity");
}