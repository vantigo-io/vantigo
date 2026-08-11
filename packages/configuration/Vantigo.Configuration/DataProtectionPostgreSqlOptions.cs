using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;

namespace Vantigo.Configuration;

/// <summary>
/// PostgreSQL-backed ASP.NET Core Data Protection key persistence options.
/// </summary>
public sealed class DataProtectionPostgreSqlOptions
{
    public const string DefaultSchema = "dataprotection";
    public const string DefaultTableName = "Keys";

    /// <summary>
    /// Database schema for the data protection keys table.
    /// </summary>
    public string Schema { get; set; } = DefaultSchema;

    /// <summary>
    /// Table name for the data protection keys.
    /// </summary>
    public string TableName { get; set; } = DefaultTableName;

    /// <summary>
    /// Override connection string. If not set, the effective connection string
    /// is resolved from <see cref="ConnectionStringsOptions"/> using
    /// <see cref="ConnectionStringName"/>.
    /// </summary>
    public string? ConnectionString { get; set; }

    /// <summary>
    /// Name of the connection string to look up when
    /// <see cref="ConnectionString"/> is not set. Defaults to "Vantigo".
    /// </summary>
    public string ConnectionStringName { get; set; } = "Vantigo";

    /// <summary>
    /// Application discriminator used to isolate the key ring. Defaults to the
    /// host environment application name.
    /// </summary>
    public string? ApplicationName { get; set; }
}

public static class DataProtectionPostgreSqlConfigurationExtensions
{
    /// <summary>
    /// Registers <see cref="DataProtectionPostgreSqlOptions"/> from the
    /// <c>DataProtection:PostgreSql</c> configuration section.
    /// </summary>
    public static IServiceCollection AddDataProtectionPostgreSqlOptions(this IServiceCollection services, IConfiguration configuration)
    {
        services.Configure<DataProtectionPostgreSqlOptions>(configuration.GetSection("DataProtection:PostgreSql"));
        return services;
    }
}