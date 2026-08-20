using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Options;

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

/// <summary>
/// Applies the same certificate-verified TLS requirement to the optional
/// key-ring connection-string override, so it cannot be used to reach a database
/// over plaintext once <see cref="ConnectionStringsOptions"/> is locked down.
/// </summary>
internal sealed class DataProtectionPostgreSqlOptionsValidator(
    IHostEnvironment environment,
    IOptions<TransportSecurityOptions> transportSecurity) : IValidateOptions<DataProtectionPostgreSqlOptions>
{
    public ValidateOptionsResult Validate(string? name, DataProtectionPostgreSqlOptions options)
    {
        if (environment.IsDevelopment() || transportSecurity.Value.AllowInsecureTransport)
        {
            return ValidateOptionsResult.Success;
        }

        string? failure = TransportSecurityOptions.DescribeInsecurePostgresTransport(
            options.ConnectionString, "DataProtection:PostgreSql:ConnectionString");

        return failure is null ? ValidateOptionsResult.Success : ValidateOptionsResult.Fail(failure);
    }
}

public static class DataProtectionPostgreSqlConfigurationExtensions
{
    /// <summary>
    /// Registers <see cref="DataProtectionPostgreSqlOptions"/> from the
    /// <c>DataProtection:PostgreSql</c> configuration section.
    /// </summary>
    public static IServiceCollection AddDataProtectionPostgreSqlOptions(this IServiceCollection services, IConfiguration configuration)
    {
        services.AddOptions<DataProtectionPostgreSqlOptions>()
            .Bind(configuration.GetSection("DataProtection:PostgreSql"))
            .ValidateOnStart();
        services.AddSingleton<IValidateOptions<DataProtectionPostgreSqlOptions>, DataProtectionPostgreSqlOptionsValidator>();
        return services;
    }
}