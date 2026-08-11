using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;

namespace Vantigo.Configuration;

/// <summary>
/// Resolves the shared database connection string used by modules and Identity.
/// </summary>
public sealed class ConnectionStringsOptions
{
    public string? Vantigo { get; set; }
    public string? Postgresql { get; set; }

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
}

public static class ConnectionStringsConfigurationExtensions
{
    public static IServiceCollection AddConnectionStringsOptions(this IServiceCollection services, IConfiguration configuration)
    {
        services.Configure<ConnectionStringsOptions>(configuration.GetSection("ConnectionStrings"));
        return services;
    }
}