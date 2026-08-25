using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Options;

namespace Vantigo.Configuration;

/// <summary>
/// Resolves the shared database connection string used by modules and Identity.
/// </summary>
public sealed class ConnectionStringsOptions
{
    public string? Vantigo { get; set; }
    public string? Postgresql { get; set; }

    /// <summary>
    /// Optional connection string for applying schema migrations
    /// (<c>ConnectionStrings:migrations</c>). Deployments that run the API as a
    /// least-privilege role point this at the schema-owner role; when unset,
    /// migrations use the runtime connection string.
    /// </summary>
    public string? Migrations { get; set; }

    /// <summary>
    /// Returns the effective connection string. Falls back through module-specific
    /// legacy keys for transitional compatibility.
    /// </summary>
    public string Resolve(params string[] legacyKeys)
    {
        foreach (var key in new[] { Vantigo }.Concat(legacyKeys).Concat([Postgresql]))
        {
            if (!string.IsNullOrWhiteSpace(key))
            {
                return key;
            }
        }

        throw new InvalidOperationException("ConnectionStrings:vantigo is required.");
    }

    /// <summary>
    /// Returns the migrations connection string when it differs from the runtime
    /// one, or null when migrations should run over the runtime connection.
    /// </summary>
    public string? ResolveMigrationsOverride(params string[] legacyKeys) =>
        !string.IsNullOrWhiteSpace(Migrations) && Migrations != Resolve(legacyKeys)
            ? Migrations
            : null;
}

/// <summary>
/// Fails startup when a configured PostgreSQL connection string does not require
/// certificate-verified TLS outside Development.
/// </summary>
internal sealed class ConnectionStringsOptionsValidator(
    IHostEnvironment environment,
    IOptions<TransportSecurityOptions> transportSecurity) : IValidateOptions<ConnectionStringsOptions>
{
    public ValidateOptionsResult Validate(string? name, ConnectionStringsOptions options)
    {
        if (environment.IsDevelopment() || transportSecurity.Value.AllowInsecureTransport)
        {
            return ValidateOptionsResult.Success;
        }

        string? failure =
            TransportSecurityOptions.DescribeInsecurePostgresTransport(options.Vantigo, "ConnectionStrings:vantigo")
            ?? TransportSecurityOptions.DescribeInsecurePostgresTransport(options.Postgresql, "ConnectionStrings:postgresql")
            ?? TransportSecurityOptions.DescribeInsecurePostgresTransport(options.Migrations, "ConnectionStrings:migrations");

        return failure is null ? ValidateOptionsResult.Success : ValidateOptionsResult.Fail(failure);
    }
}

public static class ConnectionStringsConfigurationExtensions
{
    public static IServiceCollection AddConnectionStringsOptions(this IServiceCollection services, IConfiguration configuration)
    {
        services.AddOptions<ConnectionStringsOptions>()
            .Bind(configuration.GetSection("ConnectionStrings"))
            .ValidateOnStart();
        services.AddSingleton<IValidateOptions<ConnectionStringsOptions>, ConnectionStringsOptionsValidator>();
        return services;
    }
}