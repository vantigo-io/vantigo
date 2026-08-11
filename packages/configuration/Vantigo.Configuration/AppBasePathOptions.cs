using System.ComponentModel.DataAnnotations;

using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;

namespace Vantigo.Configuration;

/// <summary>
/// Configures the application base path (<c>App:BasePath</c>) used to mount an
/// app under a path prefix on a shared domain.
/// </summary>
public sealed class AppBasePathOptions
{
    /// <summary>
    /// The configuration key holding the application base path.
    /// </summary>
    public const string ConfigurationKey = "App:BasePath";

    /// <summary>
    /// The raw configured base path. Use <see cref="Normalized"/> for the
    /// canonical form.
    /// </summary>
    public string? BasePath { get; set; }

    /// <summary>
    /// Returns the normalized base path (e.g. <c>/customers</c>) with a leading
    /// slash and no trailing slash, or <c>null</c> when the value is empty or
    /// <c>/</c> (serve at the domain root).
    /// </summary>
    public string? Normalized
    {
        get
        {
            var value = BasePath?.Trim();
            if (string.IsNullOrEmpty(value))
            {
                return null;
            }

            value = "/" + value.Trim('/');
            return value == "/" ? null : value;
        }
    }
}

public static class AppBasePathConfigurationExtensions
{
    /// <summary>
    /// Registers <see cref="AppBasePathOptions"/> from the <c>App:BasePath</c>
    /// configuration value.
    /// </summary>
    public static IServiceCollection AddAppBasePathOptions(this IServiceCollection services, IConfiguration configuration)
    {
        services.Configure<AppBasePathOptions>(options =>
        {
            options.BasePath = configuration[AppBasePathOptions.ConfigurationKey];
        });
        return services;
    }
}