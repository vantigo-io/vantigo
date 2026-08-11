using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.Options;

using Npgsql;
using Npgsql.EntityFrameworkCore.PostgreSQL.Infrastructure;

using Vantigo.Configuration;
using Vantigo.Identity.Database.Accounts;

namespace Vantigo.Identity.Database;

public static class IdentityDatabaseServiceCollectionExtensions
{
    public static IServiceCollection AddVantigoIdentityDatabase(this IServiceCollection services)
    {
        services.AddDbContext<AccountsDbContext>((serviceProvider, options) =>
        {
            var source = serviceProvider.GetService<NpgsqlDataSource>();
            if (source is not null)
            {
                options.UseNpgsql(source, ConfigureNpgsql);
                return;
            }

            var connectionStrings = serviceProvider.GetRequiredService<IOptions<ConnectionStringsOptions>>().Value;
            var connectionString = connectionStrings.Resolve();
            options.UseNpgsql(connectionString, ConfigureNpgsql);
        });

        return services;
    }

    private static void ConfigureNpgsql(NpgsqlDbContextOptionsBuilder options) =>
        options.MigrationsHistoryTable("__EFMigrationsHistory", "identity");
}