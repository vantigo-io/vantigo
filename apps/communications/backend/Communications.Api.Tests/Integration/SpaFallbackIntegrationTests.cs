using System.Net;

using Microsoft.AspNetCore.Hosting;
using Microsoft.Extensions.Configuration;

namespace Vantigo.Communications.Api.Tests.Integration;

/// <summary>
/// Verifies the runtime-templated SPA entry point end to end: the API rewrites
/// the frontend build's base path in wwwroot/index.html to the configured
/// App:BasePath and injects the runtime base for the frontend to read.
/// </summary>
[Collection(CommunicationsApiCollection.Name)]
public sealed class SpaFallbackIntegrationTests : IDisposable
{
    private const string BuiltIndexHtml = """
        <!doctype html>
        <html lang="en">
          <head>
            <script type="module" crossorigin src="/communications/assets/index-abc123.js"></script>
            <link rel="stylesheet" crossorigin href="/communications/assets/index-def456.css" />
          </head>
          <body><div id="root"></div></body>
        </html>
        """;

    private readonly CommunicationsApiFactory _factory;
    private readonly string _webRoot;

    public SpaFallbackIntegrationTests(CommunicationsApiFactory factory)
    {
        _factory = factory;
        _webRoot = Directory.CreateTempSubdirectory("communications-spa-test").FullName;
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
    public async Task Whitelabeling_InjectsConfiguredTitle()
    {
        using var client = CreateClient(settings: new Dictionary<string, string?>
        {
            ["App:Title"] = "Acme Mail",
        });

        var html = await client.GetStringAsync("/communications/some/deep/link");

        Assert.Contains("\"title\":\"Acme Mail\"", html);
    }

    [Fact]
    public async Task DeepLink_WithDefaultBasePath_ServesTemplatedIndex()
    {
        using var client = CreateClient();

        var response = await client.GetAsync("/communications/some/deep/link");
        var html = await response.Content.ReadAsStringAsync();

        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
        Assert.Equal("text/html", response.Content.Headers.ContentType?.MediaType);
        Assert.Equal("no-cache", response.Headers.CacheControl?.ToString());
        Assert.Contains("/communications/assets/index-abc123.js", html);
        Assert.Contains(""""basePath":"/communications/"""", html);
    }

    [Fact]
    public async Task DeepLink_WithCustomBasePath_ServesRewrittenIndex()
    {
        using var client = CreateClient(basePath: "/comms");

        var response = await client.GetAsync("/comms/some/deep/link");
        var html = await response.Content.ReadAsStringAsync();

        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
        Assert.Contains("/comms/assets/index-abc123.js", html);
        Assert.DoesNotContain("/communications/", html);
        Assert.Contains(""""basePath":"/comms/"""", html);
    }

    [Fact]
    public async Task RawIndexHtml_IsNeverServed_TemplatedDocumentWinsInstead()
    {
        using var client = CreateClient(basePath: "/comms");

        foreach (var path in new[] { "/index.html", "/comms/index.html", "/", "/comms" })
        {
            var html = await client.GetStringAsync(path);
            Assert.Contains("/comms/assets/index-abc123.js", html);
            Assert.Contains(""""basePath":"/comms/"""", html);
        }
    }

    [Fact]
    public async Task Fallback_WithoutPublishedFrontend_Returns404()
    {
        using var client = _factory.CreateClient();

        var response = await client.GetAsync("/communications/some/deep/link");

        Assert.Equal(HttpStatusCode.NotFound, response.StatusCode);
    }
}