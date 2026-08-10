using System.Text.Json;
using System.Text.RegularExpressions;

using Microsoft.AspNetCore.Hosting;
using Microsoft.Extensions.Configuration;

namespace Vantigo.Hosting;

/// <summary>
/// The SPA entry point (wwwroot/index.html), templated once at startup with the
/// configured base path and whitelabeling. The frontend build embeds a default
/// path prefix in its asset URLs; this rewrites those URLs, replaces the
/// document title, and injects the runtime configuration
/// (<c>window.__VANTIGO_APP__</c>) so a single published image can serve any
/// <c>App:*</c> configuration without rebuilding the frontend. Hashed assets
/// under wwwroot/assets are served verbatim by the static-file middleware; only
/// the entry document is templated.
/// </summary>
public sealed partial class SpaIndexDocument
{
    private static readonly JsonSerializerOptions JsonOptions = new()
    {
        PropertyNamingPolicy = JsonNamingPolicy.CamelCase,
        // The default encoder escapes "<" (\u003C), so serialized values can
        // never terminate the surrounding <script> element.
    };

    /// <summary>The base path the frontend build embeds in asset URLs.</summary>
    public string BuildTimeBasePath { get; }

    /// <summary>
    /// The templated document, or <c>null</c> when wwwroot/index.html does not
    /// exist (development and tests, where the Vite dev server serves the SPA).
    /// </summary>
    public string? Html { get; }

    public SpaIndexDocument(
        SpaIndexDocumentOptions options,
        IWebHostEnvironment environment,
        IConfiguration configuration)
    {
        BuildTimeBasePath = options.BuildTimeBasePath;

        var file = environment.WebRootFileProvider.GetFileInfo("index.html");
        if (!file.Exists)
        {
            return;
        }

        using var reader = new StreamReader(file.CreateReadStream());
        Html = Render(
            reader.ReadToEnd(),
            AppBasePath.Normalize(configuration[AppBasePath.ConfigurationKey]),
            BuildTimeBasePath,
            AppBranding.Load(configuration, options.DefaultTitle));
    }

    /// <summary>
    /// Rewrites <paramref name="buildTimeBasePath"/> to <paramref name="basePath"/>
    /// (already normalized: leading slash, no trailing slash, or <c>null</c> for
    /// root), replaces the document title, and injects the runtime configuration
    /// into the document head for the frontend to read.
    /// </summary>
    public static string Render(
        string html,
        string? basePath,
        string buildTimeBasePath,
        AppBranding branding)
    {
        var effectiveBase = $"{basePath ?? string.Empty}/";
        var config = JsonSerializer.Serialize(
            new
            {
                BasePath = effectiveBase,
                branding.Title,
                branding.LogoUrl,
                Support = new
                {
                    Email = branding.SupportEmail,
                    Phone = branding.SupportPhone,
                    Url = branding.SupportUrl,
                },
            },
            JsonOptions);

        if (buildTimeBasePath is "" or "/")
        {
            // A root Vite build emits root-relative asset URLs. Rewrite only URL
            // attributes so the host can mount that build below App:BasePath.
            if (effectiveBase != "/")
            {
                html = html
                    .Replace("src=\"/", $"src=\"{effectiveBase}", StringComparison.Ordinal)
                    .Replace("href=\"/", $"href=\"{effectiveBase}", StringComparison.Ordinal);
            }
        }
        else
        {
            html = html.Replace($"{buildTimeBasePath}/", effectiveBase, StringComparison.Ordinal);
        }

        html = html
            .Replace(
                "<head>",
                $"<head><script>window.__VANTIGO_APP__={config};</script>",
                StringComparison.Ordinal);

        return TitleElement().Replace(
            html,
            $"<title>{System.Net.WebUtility.HtmlEncode(branding.Title)}</title>",
            count: 1);
    }

    [GeneratedRegex("<title>.*?</title>", RegexOptions.Singleline)]
    private static partial Regex TitleElement();
}

/// <summary>Options for <see cref="SpaIndexDocument"/>.</summary>
/// <param name="BuildTimeBasePath">
/// The base path the frontend build embeds in its asset URLs (the app's
/// VITE_BASE_PATH default, e.g. <c>/customers</c>).
/// </param>
/// <param name="DefaultTitle">
/// The application title used when <c>App:Title</c> is not configured
/// (e.g. <c>Customers</c>).
/// </param>
public sealed record SpaIndexDocumentOptions(string BuildTimeBasePath, string DefaultTitle);