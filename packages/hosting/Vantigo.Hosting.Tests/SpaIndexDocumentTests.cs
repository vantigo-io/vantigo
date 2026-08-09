using Vantigo.Hosting;

namespace Vantigo.Hosting.Tests;

public sealed class SpaIndexDocumentTests
{
    private const string BuildTimeBasePath = "/customers";

    private static readonly AppBranding DefaultBranding = new(
        Title: "Customers", LogoUrl: null, SupportEmail: null, SupportPhone: null, SupportUrl: null);

    private const string BuiltIndexHtml = """
        <!doctype html>
        <html lang="en">
          <head>
            <meta charset="UTF-8" />
            <link rel="icon" href="/customers/favicon.ico" />
            <title>Customers</title>
            <script type="module" crossorigin src="/customers/assets/index-abc123.js"></script>
            <link rel="stylesheet" crossorigin href="/customers/assets/index-def456.css" />
          </head>
          <body>
            <div id="root"></div>
          </body>
        </html>
        """;

    private static string Render(string? basePath, AppBranding? branding = null, string html = BuiltIndexHtml) =>
        SpaIndexDocument.Render(html, basePath, BuildTimeBasePath, branding ?? DefaultBranding);

    // --- Base path -----------------------------------------------------------

    [Fact]
    public void Render_WithDefaultBasePath_KeepsAssetUrlsAndInjectsBase()
    {
        var html = Render("/customers");

        Assert.Contains("src=\"/customers/assets/index-abc123.js\"", html);
        Assert.Contains("href=\"/customers/assets/index-def456.css\"", html);
        Assert.Contains("\"basePath\":\"/customers/\"", html);
    }

    [Fact]
    public void Render_WithCustomBasePath_RewritesEveryAssetUrl()
    {
        var html = Render("/crm");

        Assert.Contains("src=\"/crm/assets/index-abc123.js\"", html);
        Assert.Contains("href=\"/crm/assets/index-def456.css\"", html);
        Assert.Contains("href=\"/crm/favicon.ico\"", html);
        Assert.DoesNotContain("/customers/", html);
        Assert.Contains("\"basePath\":\"/crm/\"", html);
    }

    [Fact]
    public void Render_WithRootBasePath_RewritesToRootRelativeUrls()
    {
        var html = Render(null);

        Assert.Contains("src=\"/assets/index-abc123.js\"", html);
        Assert.Contains("href=\"/favicon.ico\"", html);
        Assert.DoesNotContain("/customers/", html);
        Assert.Contains("\"basePath\":\"/\"", html);
    }

    [Fact]
    public void Render_WithDifferentBuildTimeBasePath_UsesThatPrefix()
    {
        var html = SpaIndexDocument.Render(
            """<script src="/communications/assets/a.js"></script>""",
            "/comms",
            "/communications",
            DefaultBranding);

        Assert.Contains("/comms/assets/a.js", html);
        Assert.DoesNotContain("/communications/", html);
    }

    // --- Runtime config injection --------------------------------------------

    [Fact]
    public void Render_InjectsTheRuntimeConfigImmediatelyAfterHeadOpens()
    {
        var html = Render("/crm");

        // The config must be defined before any module script in the head executes.
        var headIndex = html.IndexOf("<head>", StringComparison.Ordinal);
        var scriptIndex = html.IndexOf("window.__VANTIGO_APP__", StringComparison.Ordinal);
        var moduleIndex = html.IndexOf("type=\"module\"", StringComparison.Ordinal);
        Assert.True(headIndex >= 0 && scriptIndex > headIndex && scriptIndex < moduleIndex);
    }

    [Fact]
    public void Render_WithDefaultBranding_InjectsNullsForUnsetValues()
    {
        var html = Render("/customers");

        Assert.Contains(
            """window.__VANTIGO_APP__={"basePath":"/customers/","title":"Customers","logoUrl":null,"support":{"email":null,"phone":null,"url":null}};""",
            html);
    }

    [Fact]
    public void Render_WithFullBranding_InjectsEveryValue()
    {
        var branding = new AppBranding(
            Title: "Acme CRM",
            LogoUrl: "https://cdn.acme.test/logo.svg",
            SupportEmail: "help@acme.test",
            SupportPhone: "+47 123 45 678",
            SupportUrl: "https://support.acme.test");

        var html = Render("/customers", branding);

        Assert.Contains("\"title\":\"Acme CRM\"", html);
        Assert.Contains("\"logoUrl\":\"https://cdn.acme.test/logo.svg\"", html);
        Assert.Contains("\"email\":\"help@acme.test\"", html);
        // The default JSON encoder escapes '+' defensively.
        Assert.Contains("\"phone\":\"\\u002B47 123 45 678\"", html);
        Assert.Contains("\"url\":\"https://support.acme.test\"", html);
    }

    // --- Title replacement ----------------------------------------------------

    [Fact]
    public void Render_ReplacesTheDocumentTitle()
    {
        var branding = DefaultBranding with { Title = "Acme CRM" };

        var html = Render("/customers", branding);

        Assert.Contains("<title>Acme CRM</title>", html);
        Assert.DoesNotContain("<title>Customers</title>", html);
    }

    [Fact]
    public void Render_WithoutTitleElement_StillInjectsTheConfig()
    {
        var html = Render(
            "/crm",
            html: """<html><head><script src="/customers/assets/a.js"></script></head></html>""");

        Assert.Contains("window.__VANTIGO_APP__", html);
        Assert.DoesNotContain("<title>", html);
    }

    // --- Escaping / injection safety ------------------------------------------

    [Fact]
    public void Render_WithHostileTitle_CannotBreakOutOfTheScriptOrTitleElements()
    {
        var branding = DefaultBranding with { Title = """</script><script>alert(1)</script>""" };

        var html = Render("/customers", branding);

        // The JSON encoder escapes "<" so the config script cannot be terminated
        // early, and the <title> content is HTML-encoded.
        Assert.DoesNotContain("<script>alert(1)</script>", html);
        Assert.Contains("\\u003C/script\\u003E", html);
        Assert.Contains("<title>&lt;/script&gt;&lt;script&gt;alert(1)&lt;/script&gt;</title>", html);
    }

    [Fact]
    public void Render_WithQuotesAndUnicodeInTitle_ProducesValidJsonAndHtml()
    {
        var branding = DefaultBranding with { Title = """Møller "Bil" & Co""" };

        var html = Render("/customers", branding);

        Assert.Contains("\"title\":\"M\\u00F8ller \\u0022Bil\\u0022 \\u0026 Co\"", html);
        Assert.Contains("<title>M&#248;ller &quot;Bil&quot; &amp; Co</title>", html);
    }

    [Fact]
    public void Render_WithHostileLogoUrl_CannotBreakOutOfTheScriptElement()
    {
        var branding = DefaultBranding with { LogoUrl = """x"};</script><script>alert(1)//""" };

        var html = Render("/customers", branding);

        Assert.DoesNotContain("</script><script>alert(1)", html);
    }

    [Fact]
    public void Render_LeavesUnrelatedContentUntouched()
    {
        var html = Render("/crm");

        Assert.Contains("<div id=\"root\"></div>", html);
    }
}