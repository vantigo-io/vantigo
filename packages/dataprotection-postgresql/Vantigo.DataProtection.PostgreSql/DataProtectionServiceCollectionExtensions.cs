using Microsoft.AspNetCore.DataProtection;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Options;

using Vantigo.Azure.Identity;
using Vantigo.Configuration;

namespace Vantigo.DataProtection.PostgreSql;

/// <summary>
/// Registers PostgreSQL-backed ASP.NET Core Data Protection key persistence for
/// the Vantigo host.
/// </summary>
public static class DataProtectionServiceCollectionExtensions
{
    /// <summary>
    /// Adds data protection and persists the shared key ring to PostgreSQL using
    /// the connection string resolved from <see cref="ConnectionStringsOptions"/>
    /// or an explicit override. Throws when the key context is first used if no
    /// connection string is configured.
    /// </summary>
    public static async Task MigrateDataProtectionAsync(this IServiceProvider services)
    {
        await using var scope = services.CreateAsyncScope();
        var connectionStrings = scope.ServiceProvider.GetRequiredService<IOptions<ConnectionStringsOptions>>().Value;
        if (connectionStrings.ResolveMigrationsOverride() is { } migrations)
        {
            // Migrations run as the schema-owner role while the runtime pool
            // stays on the least-privilege role; see docs/tenancy.md.
            var dataProtectionOptions = scope.ServiceProvider.GetRequiredService<IOptions<DataProtectionPostgreSqlOptions>>().Value;
            var options = new DbContextOptionsBuilder<DataProtectionKeyDbContext>()
                .UseNpgsql(migrations, npgsql => npgsql.MigrationsHistoryTable("__EFMigrationsHistory", dataProtectionOptions.Schema))
                .Options;
            await using var migrationContext = new DataProtectionKeyDbContext(options);
            await migrationContext.Database.MigrateAsync();
            return;
        }

        await scope.ServiceProvider.GetRequiredService<DataProtectionKeyDbContext>().Database.MigrateAsync();
    }

    /// <summary>
    /// Adds data protection and persists the shared key ring to PostgreSQL using
    /// the connection string resolved from <see cref="ConnectionStringsOptions"/>
    /// or an explicit override. Throws when the key context is first used if no
    /// connection string is configured.
    /// </summary>
    /// <remarks>
    /// PostgreSQL remains the key repository (required for multi-replica
    /// deployments). When <see cref="DataProtectionKeyWrappingOptions.KeyVaultKeyUri"/> is
    /// configured, the persisted keys are additionally wrapped with that Azure
    /// Key Vault key, so a database dump alone can no longer decrypt protected
    /// payloads. See docs/data-protection-key-wrapping.md for rotation and
    /// recovery.
    /// </remarks>
    public static IServiceCollection AddVantigoDataProtection(
        this IServiceCollection services,
        IConfiguration configuration,
        IHostEnvironment environment)
    {
        services.AddDataProtectionPostgreSqlOptions(configuration);
        services.AddDataProtectionOptions(configuration);

        services.AddDbContext<DataProtectionKeyDbContext>((serviceProvider, options) =>
        {
            var dataProtectionOptions = serviceProvider.GetRequiredService<IOptions<DataProtectionPostgreSqlOptions>>().Value;
            var connectionStrings = serviceProvider.GetRequiredService<IOptions<ConnectionStringsOptions>>().Value;
            var connectionString = ResolveConnectionString(dataProtectionOptions, connectionStrings);
            options.UseNpgsql(connectionString, npgsql =>
            {
                npgsql.MigrationsHistoryTable("__EFMigrationsHistory", dataProtectionOptions.Schema);
            });
        });

        var applicationName = ResolveApplicationName(configuration, environment);

        var dataProtectionBuilder = services.AddDataProtection()
            .SetApplicationName(applicationName)
            .PersistKeysToDbContext<DataProtectionKeyDbContext>();

        var keyVaultKeyUri = ResolveKeyVaultKeyUri(configuration);
        if (!string.IsNullOrWhiteSpace(keyVaultKeyUri))
        {
            dataProtectionBuilder.ProtectKeysWithAzureKeyVault(new Uri(keyVaultKeyUri), AzureIdentityCredentialFactory.Create());
        }

        return services;
    }

    private static string ResolveConnectionString(
        DataProtectionPostgreSqlOptions options,
        ConnectionStringsOptions connectionStrings)
    {
        if (!string.IsNullOrWhiteSpace(options.ConnectionString))
        {
            return options.ConnectionString;
        }

        var configured = connectionStrings.Resolve(options.ConnectionStringName);
        if (!string.IsNullOrWhiteSpace(configured))
        {
            return configured;
        }

        throw new InvalidOperationException(
            "Data protection keys are configured to use PostgreSQL, but no connection string is available. " +
            "Configure ConnectionStrings:Vantigo or set DataProtection:PostgreSql:ConnectionString.");
    }

    private static string ResolveApplicationName(IConfiguration configuration, IHostEnvironment environment)
    {
        var options = configuration.GetSection("DataProtection:PostgreSql").Get<DataProtectionPostgreSqlOptions>();
        if (!string.IsNullOrWhiteSpace(options?.ApplicationName))
        {
            return options.ApplicationName;
        }

        return environment.ApplicationName ?? "Vantigo";
    }

    private static string? ResolveKeyVaultKeyUri(IConfiguration configuration) =>
        configuration.GetSection("DataProtection").Get<DataProtectionKeyWrappingOptions>()?.KeyVaultKeyUri;
}