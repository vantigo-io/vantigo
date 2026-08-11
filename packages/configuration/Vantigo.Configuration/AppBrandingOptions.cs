using System.ComponentModel.DataAnnotations;

using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;

namespace Vantigo.Configuration;

/// <summary>
/// Whitelabeling settings read from the <c>App:*</c> configuration section.
/// </summary>
public sealed class AppBrandingOptions
{
    /// <summary>
    /// The display title for the application.
    /// </summary>
    public string? Title { get; set; }

    /// <summary>
    /// URL for the application logo.
    /// </summary>
    public string? LogoUrl { get; set; }

    /// <summary>
    /// Support contact settings.
    /// </summary>
    public AppSupportOptions Support { get; set; } = new();

    /// <summary>
    /// Resolves the effective title, falling back to
    /// <paramref name="defaultTitle"/>.
    /// </summary>
    public string GetTitle(string defaultTitle) =>
        string.IsNullOrWhiteSpace(Title) ? defaultTitle : Title.Trim();
}

/// <summary>
/// Support contact options under the <c>App:Support</c> configuration section.
/// </summary>
public sealed class AppSupportOptions
{
    public string? Email { get; set; }
    public string? Phone { get; set; }
    public string? Url { get; set; }
}

public static class AppBrandingConfigurationExtensions
{
    /// <summary>
    /// Registers <see cref="AppBrandingOptions"/> from the <c>App</c>
    /// configuration section.
    /// </summary>
    public static IServiceCollection AddAppBrandingOptions(this IServiceCollection services, IConfiguration configuration)
    {
        services.Configure<AppBrandingOptions>(configuration.GetSection("App"));
        return services;
    }
}