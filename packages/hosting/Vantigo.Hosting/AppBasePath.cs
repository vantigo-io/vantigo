namespace Vantigo.Hosting;

/// <summary>
/// Normalizes the configured application base path (<c>App:BasePath</c>) used to
/// mount an app under a path prefix on a shared domain.
/// </summary>
public static class AppBasePath
{
    /// <summary>The configuration key holding the application base path.</summary>
    public const string ConfigurationKey = "App:BasePath";

    /// <summary>
    /// Returns a normalized base path (e.g. <c>/customers</c>) with a leading
    /// slash and no trailing slash, or <c>null</c> when the value is empty or
    /// <c>/</c> (serve at the domain root).
    /// </summary>
    public static string? Normalize(string? configured)
    {
        var value = configured?.Trim();
        if (string.IsNullOrEmpty(value))
        {
            return null;
        }

        value = "/" + value.Trim('/');
        return value == "/" ? null : value;
    }
}