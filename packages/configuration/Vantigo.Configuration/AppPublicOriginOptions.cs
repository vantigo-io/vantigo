using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;

namespace Vantigo.Configuration;

/// <summary>
/// Configures the public origin (scheme + host) users reach the app on, via
/// <c>App:PublicOrigin</c> (e.g. <c>https://vantigo.example.com</c>).
/// </summary>
public sealed class AppPublicOriginOptions
{
    /// <summary>
    /// The configuration key holding the public origin.
    /// </summary>
    public const string ConfigurationKey = "App:PublicOrigin";

    /// <summary>
    /// The raw configured public origin.
    /// </summary>
    public string? PublicOrigin { get; set; }

    /// <summary>
    /// Returns the normalized origin (scheme + host, no trailing slash), or
    /// <c>null</c> when unset. Invalid values fail fast: generated email links
    /// silently pointing at the wrong place are worse than a startup error.
    /// </summary>
    /// <exception cref="InvalidOperationException">
    /// The value is not an absolute http(s) origin, or carries a path, query,
    /// fragment, or user info.
    /// </exception>
    public string? Normalized
    {
        get
        {
            var value = PublicOrigin?.Trim();
            if (string.IsNullOrEmpty(value))
            {
                return null;
            }

            if (!Uri.TryCreate(value, UriKind.Absolute, out var uri)
                || (uri.Scheme != Uri.UriSchemeHttp && uri.Scheme != Uri.UriSchemeHttps))
            {
                throw new InvalidOperationException(
                    $"{ConfigurationKey} must be an absolute http(s) origin like https://vantigo.example.com, but was '{PublicOrigin}'.");
            }

            if (uri.AbsolutePath != "/" || !string.IsNullOrEmpty(uri.Query)
                || !string.IsNullOrEmpty(uri.Fragment) || !string.IsNullOrEmpty(uri.UserInfo))
            {
                throw new InvalidOperationException(
                    $"{ConfigurationKey} must be an origin only (scheme + host, no path, query, fragment, or user info), but was '{PublicOrigin}'. The application base path is configured separately via {AppBasePathOptions.ConfigurationKey}.");
            }

            return uri.GetLeftPart(UriPartial.Authority);
        }
    }
}

public static class AppPublicOriginConfigurationExtensions
{
    /// <summary>
    /// Registers <see cref="AppPublicOriginOptions"/> from the
    /// <c>App:PublicOrigin</c> configuration value.
    /// </summary>
    public static IServiceCollection AddAppPublicOriginOptions(this IServiceCollection services, IConfiguration configuration)
    {
        services.Configure<AppPublicOriginOptions>(options =>
        {
            options.PublicOrigin = configuration[AppPublicOriginOptions.ConfigurationKey];
        });
        return services;
    }
}