using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.Options;

using Npgsql;
using Npgsql.EntityFrameworkCore.PostgreSQL.Infrastructure;

using Vantigo.Configuration;
using Vantigo.Identity.Database.Accounts;
using Vantigo.Identity.Services;

namespace Vantigo.Identity.Database;

public static class IdentityDatabaseServiceCollectionExtensions
{
    public static IServiceCollection AddVantigoIdentityDatabase(this IServiceCollection services)
    {
        services.AddMemoryCache();
        services.AddSingleton<SessionStateCache>();
        services.AddSingleton<SessionStateInvalidationInterceptor>();
        services.AddDbContext<AccountsDbContext>((serviceProvider, options) =>
        {
            // Every account mutation goes through this context, so the interceptor is
            // where cached session state is dropped, whether the write came from
            // Identity's user store or from a direct entity update.
            options.AddInterceptors(serviceProvider.GetRequiredService<SessionStateInvalidationInterceptor>());
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