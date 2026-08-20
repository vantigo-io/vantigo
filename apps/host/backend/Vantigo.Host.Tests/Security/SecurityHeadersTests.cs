using System.Security.Cryptography;
using System.Text;
using System.Text.RegularExpressions;

using Vantigo.Configuration;
using Vantigo.Host.Security;

namespace Vantigo.Host.Tests.Security;

/// <summary>
/// The content security policy is the one change in this area that can break the
/// SPA silently: the page still loads, the blocked script never runs, and nothing
/// on the server notices. These tests pin the policy to the document the host
/// actually serves.
/// </summary>
public sealed partial class SecurityHeadersTests
{
    /// <summary>The published Vite document, reduced to the parts CSP cares about.</summary>
    private const string PublishedIndexHtml = """
        <!doctype html>
        <html lang="en">
          <head>
            <meta charset="UTF-8" />
            <link rel="icon" type="image/png" href="/favicon.png" />
            <title>Vantigo</title>
            <script type="module" crossorigin src="/assets/index-CWNhm0Gy.js"></script>
            <link rel="modulepreload" crossorigin href="/assets/src-RNSCzpNU.js">
            <link rel="stylesheet" crossorigin href="/assets/index-BWgsb15p.css">
          </head>
          <body>
            <div id="root"></div>
          </body>
        </html>
        """;

    [Theory]
    [InlineData(null)]
    [InlineData("/crm")]
    public void EveryInlineScriptInTheRenderedDocument_IsAllowedByTheScriptSourceHashes(string? basePath)
    {
        AppBrandingOptions branding = new()
        {
            Title = "Contoso Energi",
            LogoUrl = "https://cdn.example.com/logo.png",
            Support = new AppSupportOptions { Email = "support@example.com" },
        };

        string html = SpaIndexDocument.Render(PublishedIndexHtml, basePath, "/", branding, "Vantigo");
        string policy = SecurityHeaders.BuildContentSecurityPolicy(
            SpaIndexDocument.ComputeInlineScriptHash(basePath, branding, "Vantigo"));

        string[] inlineScripts = InlineScripts(html);
        Assert.Single(inlineScripts);
        foreach (string script in inlineScripts)
        {
            Assert.Contains(Sha256Source(script), policy, StringComparison.Ordinal);
        }
    }

    [Fact]
    public void TheDocumentInjectsTheRuntimeConfigurationTheFrontendReads()
    {
        string html = SpaIndexDocument.Render(PublishedIndexHtml, null, "/", new AppBrandingOptions(), "Vantigo");

        Assert.Contains("window.__VANTIGO_APP__=", Assert.Single(InlineScripts(html)), StringComparison.Ordinal);
    }

    [Fact]
    public void AChangedInlineScript_ChangesTheHash()
    {
        AppBrandingOptions branding = new() { Title = "Contoso" };

        Assert.NotEqual(
            SpaIndexDocument.ComputeInlineScriptHash(null, branding, "Vantigo"),
            SpaIndexDocument.ComputeInlineScriptHash(null, new AppBrandingOptions { Title = "Fabrikam" }, "Vantigo"));
    }

    [Fact]
    public void ThePublishedBundleNeedsNoUnsafeScriptSources()
    {
        string policy = SecurityHeaders.BuildContentSecurityPolicy("'sha256-abc'");

        Assert.DoesNotContain("'unsafe-eval'", policy, StringComparison.Ordinal);
        Assert.DoesNotContain("script-src 'self' 'unsafe-inline'", policy, StringComparison.Ordinal);
        Assert.Contains("script-src 'self' 'sha256-abc'", policy, StringComparison.Ordinal);
    }

    [Theory]
    [InlineData("default-src 'self'")]
    [InlineData("frame-ancestors 'none'")]
    [InlineData("object-src 'none'")]
    [InlineData("base-uri 'self'")]
    [InlineData("form-action 'self'")]
    [InlineData("connect-src 'self'")]
    // Mantine renders its theme variables and component styles as inline
    // <style> elements, so this one relaxation is load bearing.
    [InlineData("style-src 'self' 'unsafe-inline'")]
    public void ThePolicyPinsTheDirectivesThatCarryTheProtection(string directive) =>
        Assert.Contains(directive, SecurityHeaders.BuildContentSecurityPolicy("'sha256-abc'"), StringComparison.Ordinal);

    private static string[] InlineScripts(string html) =>
        [.. InlineScriptElement().Matches(html).Select(match => match.Groups["body"].Value)];

    private static string Sha256Source(string script) =>
        $"'sha256-{Convert.ToBase64String(SHA256.HashData(Encoding.UTF8.GetBytes(script)))}'";

    [GeneratedRegex(@"<script(?![^>]*\ssrc=)[^>]*>(?<body>.*?)</script>", RegexOptions.Singleline)]
    private static partial Regex InlineScriptElement();
}