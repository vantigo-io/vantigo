using System.Security.Cryptography;
using System.Text;
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

    /// <summary>
    /// The CSP <c>'sha256-...'</c> source expression for the runtime-configuration
    /// script this document injects. It is derived from the very same string the
    /// document embeds, so the content security policy cannot drift away from the
    /// script it is meant to allow.
    /// </summary>
    public string InlineScriptSha256 { get; }

    public SpaIndexDocument(
        SpaIndexDocumentOptions options,
        IWebHostEnvironment environment,
        IOptions<AppBasePathOptions> basePathOptions,
        IOptions<AppBrandingOptions> brandingOptions)
    {
        BuildTimeBasePath = options.BuildTimeBasePath;
        InlineScriptSha256 = ComputeInlineScriptHash(
            basePathOptions.Value.Normalized,
            brandingOptions.Value,
            options.DefaultTitle);

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
        var script = RuntimeConfigurationScript(effectiveBase, brandingOptions, defaultTitle);

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
                $"<head><script>{script}</script>",
                StringComparison.Ordinal);

        return TitleElement().Replace(
            html,
            $"<title>{System.Net.WebUtility.HtmlEncode(title)}</title>",
            count: 1);
    }

    /// <summary>
    /// Returns the CSP <c>'sha256-...'</c> source expression covering the script
    /// <see cref="Render"/> injects for the same inputs.
    /// </summary>
    public static string ComputeInlineScriptHash(
        string? basePath,
        AppBrandingOptions brandingOptions,
        string defaultTitle)
    {
        var script = RuntimeConfigurationScript($"{basePath ?? string.Empty}/", brandingOptions, defaultTitle);
        var digest = SHA256.HashData(Encoding.UTF8.GetBytes(script));
        return $"'sha256-{Convert.ToBase64String(digest)}'";
    }

    /// <summary>
    /// The exact text of the injected inline script. Both the rendered document
    /// and the content-security-policy hash are derived from this one method, so
    /// they cannot disagree.
    /// </summary>
    private static string RuntimeConfigurationScript(
        string effectiveBase,
        AppBrandingOptions brandingOptions,
        string defaultTitle)
    {
        var config = JsonSerializer.Serialize(
            new
            {
                BasePath = effectiveBase,
                Title = brandingOptions.GetTitle(defaultTitle),
                brandingOptions.LogoUrl,
                Support = new
                {
                    Email = brandingOptions.Support.Email,
                    Phone = brandingOptions.Support.Phone,
                    Url = brandingOptions.Support.Url,
                },
            },
            JsonOptions);

        return $"window.__VANTIGO_APP__={config};";
    }

    [GeneratedRegex("<title>.*?</title>", RegexOptions.Singleline)]
    private static partial Regex TitleElement();
}

/// <summary>Options for <see cref="SpaIndexDocument"/>.</summary>
public sealed record SpaIndexDocumentOptions(string BuildTimeBasePath, string DefaultTitle);