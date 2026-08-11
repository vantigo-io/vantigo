using Microsoft.Extensions.Options;

namespace Vantigo.Configuration;

/// <summary>
/// Builds absolute public URLs from the configured public origin and base path.
/// </summary>
public sealed class AppPublicUrls
{
    /// <summary>The normalized public origin, or <c>null</c> when not configured.</summary>
    public string? Origin { get; }

    /// <summary>The normalized base path, or <c>null</c> when serving from the root.</summary>
    public string? BasePath { get; }

    public AppPublicUrls(IOptions<AppPublicOriginOptions> originOptions, IOptions<AppBasePathOptions> basePathOptions)
    {
        Origin = originOptions.Value.Normalized;
        BasePath = basePathOptions.Value.Normalized;
    }

    /// <summary>
    /// The absolute public URL for an app-relative path — e.g.
    /// <c>PublicUrl("/password-reset")</c> yields
    /// <c>https://vantigo.example.com/customers/password-reset</c> — or
    /// <c>null</c> when no public origin is configured.
    /// </summary>
    public string? PublicUrl(string appRelativePathAndQuery)
    {
        if (Origin is null)
        {
            return null;
        }

        var path = appRelativePathAndQuery.StartsWith('/') ? appRelativePathAndQuery : $"/{appRelativePathAndQuery}";
        return $"{Origin}{BasePath}{path}";
    }
}