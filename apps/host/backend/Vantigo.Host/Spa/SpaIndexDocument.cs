using System.Text.Json;
using System.Text.RegularExpressions;

using Microsoft.AspNetCore.Hosting;
using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.Options;

using Vantigo.Configuration;

namespace Vantigo.Host;

/// <summary>
/// The SPA entry point (wwwroot/index.html), templated once at startup with the
/// configured base path and whitelabeling. The frontend build embeds a default
/// path prefix in its asset URLs; this rewrites those URLs, replaces the
/// document title, and injects the runtime configuration
/// (<c>window.__VANTIGO_APP__</c>) so a single published image can serve any
/// <c>App:*</c> configuration without rebuilding the frontend.
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
        IOptions<AppBasePathOptions> basePathOptions,
        IOptions<AppBrandingOptions> brandingOptions)
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
            basePathOptions.Value.Normalized,
            BuildTimeBasePath,
            brandingOptions.Value,
            options.DefaultTitle);
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
        AppBrandingOptions brandingOptions,
        string defaultTitle)
    {
        var title = brandingOptions.GetTitle(defaultTitle);
        var effectiveBase = $"{basePath ?? string.Empty}/";
        var config = JsonSerializer.Serialize(
            new
            {
                BasePath = effectiveBase,
                title,
                brandingOptions.LogoUrl,
                Support = new
                {
                    Email = brandingOptions.Support.Email,
                    Phone = brandingOptions.Support.Phone,
                    Url = brandingOptions.Support.Url,
                },
            },
            JsonOptions);

        if (buildTimeBasePath is "" or "/")
        {
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
            $"<title>{System.Net.WebUtility.HtmlEncode(title)}</title>",
            count: 1);
    }

    [GeneratedRegex("<title>.*?</title>", RegexOptions.Singleline)]
    private static partial Regex TitleElement();
}

/// <summary>Options for <see cref="SpaIndexDocument"/>.</summary>
public sealed record SpaIndexDocumentOptions(string BuildTimeBasePath, string DefaultTitle);