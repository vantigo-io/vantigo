using Microsoft.Extensions.Configuration;

namespace Vantigo.Hosting;

/// <summary>
/// The public origin (scheme + host) users reach the app on, configured via
/// <c>App:PublicOrigin</c> (e.g. <c>https://vantigo.example.com</c>). Combined
/// with <see cref="AppBasePath"/> it produces absolute public URLs for
/// generated links (invitation and password-reset emails, OIDC callback
/// registration). Unset means no absolute URLs can be derived and callers fall
/// back to their own defaults.
/// </summary>
public static class AppPublicOrigin
{
    /// <summary>The configuration key holding the public origin.</summary>
    public const string ConfigurationKey = "App:PublicOrigin";

    /// <summary>
    /// Returns the normalized origin (scheme + host, no trailing slash), or
    /// <c>null</c> when unset. Invalid values fail fast: generated email links
    /// silently pointing at the wrong place are worse than a startup error.
    /// </summary>
    /// <exception cref="InvalidOperationException">
    /// The value is not an absolute http(s) origin, or carries a path, query,
    /// fragment, or user info.
    /// </exception>
    public static string? Normalize(string? configured)
    {
        var value = configured?.Trim();
        if (string.IsNullOrEmpty(value))
        {
            return null;
        }

        if (!Uri.TryCreate(value, UriKind.Absolute, out var uri)
            || (uri.Scheme != Uri.UriSchemeHttp && uri.Scheme != Uri.UriSchemeHttps))
        {
            throw new InvalidOperationException(
                $"{ConfigurationKey} must be an absolute http(s) origin like https://vantigo.example.com, but was '{configured}'.");
        }

        if (uri.AbsolutePath != "/" || !string.IsNullOrEmpty(uri.Query)
            || !string.IsNullOrEmpty(uri.Fragment) || !string.IsNullOrEmpty(uri.UserInfo))
        {
            throw new InvalidOperationException(
                $"{ConfigurationKey} must be an origin only (scheme + host, no path, query, fragment, or user info), but was '{configured}'. The application base path is configured separately via {AppBasePath.ConfigurationKey}.");
        }

        return uri.GetLeftPart(UriPartial.Authority);
    }
}

/// <summary>
/// Builds absolute public URLs from the configured public origin and base path.
/// </summary>
public sealed class AppPublicUrls
{
    /// <summary>The normalized public origin, or <c>null</c> when not configured.</summary>
    public string? Origin { get; }

    /// <summary>The normalized base path, or <c>null</c> when serving from the root.</summary>
    public string? BasePath { get; }

    public AppPublicUrls(IConfiguration configuration)
    {
        Origin = AppPublicOrigin.Normalize(configuration[AppPublicOrigin.ConfigurationKey]);
        BasePath = AppBasePath.Normalize(configuration[AppBasePath.ConfigurationKey]);
    }

    /// <summary>
    /// The absolute public URL for an app-relative path — e.g.
    /// <c>PublicUrl("/password-reset")</c> yields
    /// <c>https://vantigo.example.com/customers/password-reset</c> — or
    /// <c>null</c> when no public origin is configured.
    /// </summary>
    public string? PublicUrl(string appRelativePath)
    {
        if (Origin is null)
        {
            return null;
        }

        var path = appRelativePath.StartsWith('/') ? appRelativePath : $"/{appRelativePath}";
        return $"{Origin}{BasePath}{path}";
    }
}