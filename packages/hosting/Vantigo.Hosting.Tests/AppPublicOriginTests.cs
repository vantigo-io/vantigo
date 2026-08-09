using Microsoft.Extensions.Configuration;

using Vantigo.Hosting;

namespace Vantigo.Hosting.Tests;

public sealed class AppPublicOriginTests
{
    [Theory]
    [InlineData(null, null)]
    [InlineData("", null)]
    [InlineData("   ", null)]
    [InlineData("https://vantigo.example.com", "https://vantigo.example.com")]
    [InlineData("https://vantigo.example.com/", "https://vantigo.example.com")]
    [InlineData("  https://vantigo.example.com/  ", "https://vantigo.example.com")]
    [InlineData("http://localhost:8080", "http://localhost:8080")]
    [InlineData("https://vantigo.example.com:8443", "https://vantigo.example.com:8443")]
    public void Normalize_AcceptsValidOrigins(string? configured, string? expected)
    {
        Assert.Equal(expected, AppPublicOrigin.Normalize(configured));
    }

    [Theory]
    [InlineData("vantigo.example.com")] // missing scheme
    [InlineData("ftp://vantigo.example.com")] // non-http scheme
    [InlineData("https://vantigo.example.com/customers")] // path
    [InlineData("https://vantigo.example.com/?tenant=1")] // query
    [InlineData("https://vantigo.example.com/#section")] // fragment
    [InlineData("https://user:secret@vantigo.example.com")] // user info
    public void Normalize_FailsFastOnInvalidValues(string configured)
    {
        var exception = Assert.Throws<InvalidOperationException>(() => AppPublicOrigin.Normalize(configured));
        Assert.Contains("App:PublicOrigin", exception.Message);
    }
}

public sealed class AppPublicUrlsTests
{
    private static AppPublicUrls Create(string? origin, string? basePath = null)
    {
        var values = new Dictionary<string, string?>();
        if (origin is not null)
        {
            values["App:PublicOrigin"] = origin;
        }
        if (basePath is not null)
        {
            values["App:BasePath"] = basePath;
        }
        return new AppPublicUrls(new ConfigurationBuilder().AddInMemoryCollection(values).Build());
    }

    [Fact]
    public void PublicUrl_WithoutOrigin_ReturnsNull()
    {
        Assert.Null(Create(origin: null, basePath: "/customers").PublicUrl("/password-reset"));
    }

    [Fact]
    public void PublicUrl_CombinesOriginBasePathAndPath()
    {
        var urls = Create("https://vantigo.example.com", "/customers");

        Assert.Equal(
            "https://vantigo.example.com/customers/password-reset",
            urls.PublicUrl("/password-reset"));
    }

    [Fact]
    public void PublicUrl_WithRootBasePath_OmitsThePrefix()
    {
        var urls = Create("https://vantigo.example.com", "");

        Assert.Equal(
            "https://vantigo.example.com/password-reset",
            urls.PublicUrl("/password-reset"));
    }

    [Fact]
    public void PublicUrl_WithNestedBasePath_IncludesTheWholePrefix()
    {
        var urls = Create("https://vantigo.example.com", "/apps/customers/");

        Assert.Equal(
            "https://vantigo.example.com/apps/customers/auth/oidc/callback",
            urls.PublicUrl("/auth/oidc/callback"));
    }

    [Fact]
    public void PublicUrl_NormalizesAMissingLeadingSlash()
    {
        var urls = Create("https://vantigo.example.com", "/customers");

        Assert.Equal(
            "https://vantigo.example.com/customers/password-reset",
            urls.PublicUrl("password-reset"));
    }

    [Fact]
    public void PublicUrl_PreservesQueryTemplates()
    {
        var urls = Create("https://vantigo.example.com", "/customers");

        Assert.Equal(
            "https://vantigo.example.com/customers/invitations/accept?token={token}",
            urls.PublicUrl("/invitations/accept?token={token}"));
    }

    [Fact]
    public void Constructor_FailsFastOnAnInvalidOrigin()
    {
        Assert.Throws<InvalidOperationException>(() => Create("https://vantigo.example.com/customers"));
    }
}