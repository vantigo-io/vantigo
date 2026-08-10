using System.Net;

using Microsoft.AspNetCore.Hosting;
using Microsoft.Extensions.Configuration;

namespace Vantigo.Customers.Module.Tests.Integration;

/// <summary>
/// Verifies the runtime-templated SPA entry point end to end: the API rewrites
/// the frontend build's base path in wwwroot/index.html to the configured
/// App:BasePath and injects the runtime base for the frontend to read.
/// </summary>
[Collection(CustomersApiCollection.Name)]
public sealed class SpaFallbackIntegrationTests : IDisposable
{
    private const string BuiltIndexHtml = """
        <!doctype html>
        <html lang="en">
          <head>
            <title>Vantigo</title>
            <script type="module" crossorigin src="/assets/index-abc123.js"></script>
            <link rel="stylesheet" crossorigin href="/assets/index-def456.css" />
          </head>
          <body><div id="root"></div></body>
        </html>
        """;

    private readonly CustomersApiFactory _factory;
    private readonly string _webRoot;

    public SpaFallbackIntegrationTests(CustomersApiFactory factory)
    {
        _factory = factory;
        _webRoot = Directory.CreateTempSubdirectory("customers-spa-test").FullName;
        File.WriteAllText(Path.Combine(_webRoot, "index.html"), BuiltIndexHtml);
    }

    public void Dispose() => Directory.Delete(_webRoot, recursive: true);

    private HttpClient CreateClient(string? basePath = null, Dictionary<string, string?>? settings = null)
    {
        var factory = _factory.WithWebHostBuilder(builder =>
        {
            builder.UseWebRoot(_webRoot);
            var values = settings ?? [];
            if (basePath is not null)
            {
                values["App:BasePath"] = basePath;
            }
            if (values.Count > 0)
            {
                builder.ConfigureAppConfiguration((_, configuration) =>
                    configuration.AddInMemoryCollection(values));
            }
        });
        return factory.CreateClient();
    }

    [Fact]
    public async Task DeepLink_WithDefaultBasePath_ServesTemplatedIndex()
    {
        using var client = CreateClient();

        var response = await client.GetAsync("/some/deep/link");
        var html = await response.Content.ReadAsStringAsync();

        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
        Assert.Equal("text/html", response.Content.Headers.ContentType?.MediaType);
        Assert.Equal("no-cache", response.Headers.CacheControl?.ToString());
        Assert.Contains("/assets/index-abc123.js", html);
        Assert.Contains(""""basePath":"/"""", html);
    }

    [Fact]
    public async Task DeepLink_WithCustomBasePath_ServesRewrittenIndex()
    {
        using var client = CreateClient(basePath: "/crm");

        var response = await client.GetAsync("/crm/some/deep/link");
        var html = await response.Content.ReadAsStringAsync();

        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
        Assert.Contains("/crm/assets/index-abc123.js", html);
        Assert.Contains("/crm/assets/index-def456.css", html);
        Assert.DoesNotContain("/customers/", html);
        Assert.Contains(""""basePath":"/crm/"""", html);
    }

    [Fact]
    public async Task DeepLink_WithRootBasePath_ServesRootRelativeIndex()
    {
        using var client = CreateClient(basePath: "");

        var response = await client.GetAsync("/some/deep/link");
        var html = await response.Content.ReadAsStringAsync();

        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
        Assert.Contains("src=\"/assets/index-abc123.js\"", html);
        Assert.DoesNotContain("/customers/", html);
        Assert.Contains(""""basePath":"/"""", html);
    }

    [Fact]
    public async Task RawIndexHtml_IsNeverServed_TemplatedDocumentWinsInstead()
    {
        using var client = CreateClient(basePath: "/crm");

        // Both the prefixed and unprefixed paths must serve the templated
        // document, not the raw wwwroot file with stale build-time URLs.
        foreach (var path in new[] { "/index.html", "/crm/index.html", "/", "/crm" })
        {
            var html = await client.GetStringAsync(path);
            Assert.Contains("/crm/assets/index-abc123.js", html);
            Assert.Contains(""""basePath":"/crm/"""", html);
        }
    }

    [Fact]
    public async Task Whitelabeling_InjectsConfiguredTitleLogoAndSupport()
    {
        using var client = CreateClient(settings: new Dictionary<string, string?>
        {
            ["App:Title"] = "Acme CRM",
            ["App:LogoUrl"] = "https://cdn.acme.test/logo.svg",
            ["App:Support:Email"] = "help@acme.test",
            ["App:Support:Phone"] = "+47 123 45 678",
            ["App:Support:Url"] = "https://support.acme.test",
        });

        var html = await client.GetStringAsync("/some/deep/link");

        Assert.Contains("<title>Acme CRM</title>", html);
        Assert.DoesNotContain("<title>Vantigo</title>", html);
        Assert.Contains("\"title\":\"Acme CRM\"", html);
        Assert.Contains("\"logoUrl\":\"https://cdn.acme.test/logo.svg\"", html);
        Assert.Contains("\"email\":\"help@acme.test\"", html);
        Assert.Contains("\"url\":\"https://support.acme.test\"", html);
    }

    [Fact]
    public async Task Whitelabeling_DefaultsToTheAppNameWithoutBranding()
    {
        using var client = CreateClient();

        var html = await client.GetStringAsync("/some/deep/link");

        Assert.Contains("<title>Vantigo</title>", html);
        Assert.Contains("\"title\":\"Vantigo\"", html);
        Assert.Contains("\"logoUrl\":null", html);
        Assert.Contains("\"support\":{\"email\":null,\"phone\":null,\"url\":null}", html);
    }

    [Fact]
    public async Task ApiAndAuthGuards_StillPrecedeTheSpaFallback()
    {
        using var client = CreateClient();

        var api = await client.GetAsync("/api/unknown");
        var auth = await client.GetAsync("/api/v1/identity/unknown");

        Assert.Equal(HttpStatusCode.NotFound, api.StatusCode);
        Assert.Equal(HttpStatusCode.NotFound, auth.StatusCode);
    }

    [Fact]
    public async Task Fallback_WithoutPublishedFrontend_Returns404()
    {
        // The shared factory has no wwwroot; the fallback must not throw.
        using var client = _factory.CreateClient();

        var response = await client.GetAsync("/some/deep/link");

        Assert.Equal(HttpStatusCode.NotFound, response.StatusCode);
    }
}