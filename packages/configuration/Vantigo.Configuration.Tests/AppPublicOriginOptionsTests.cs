using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.Options;

namespace Vantigo.Configuration.Tests;

public sealed class AppPublicOriginOptionsTests
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
    public void Normalized_AcceptsValidOrigins(string? configured, string? expected)
    {
        var options = new AppPublicOriginOptions { PublicOrigin = configured };
        Assert.Equal(expected, options.Normalized);
    }

    [Theory]
    [InlineData("vantigo.example.com")] // missing scheme
    [InlineData("ftp://vantigo.example.com")] // non-http scheme
    [InlineData("https://vantigo.example.com/customers")] // path
    [InlineData("https://vantigo.example.com/?tenant=1")] // query
    [InlineData("https://vantigo.example.com/#section")] // fragment
    [InlineData("https://user:secret@vantigo.example.com")] // user info
    public void Normalized_FailsFastOnInvalidValues(string configured)
    {
        var options = new AppPublicOriginOptions { PublicOrigin = configured };
        var exception = Assert.Throws<InvalidOperationException>(() => options.Normalized);
        Assert.Contains("App:PublicOrigin", exception.Message);
    }
}

public sealed class AppPublicUrlsTests
{
    private static AppPublicUrls Create(string? origin, string? basePath = null)
    {
        var originOptions = new OptionsWrapper<AppPublicOriginOptions>(
            new AppPublicOriginOptions { PublicOrigin = origin });
        var basePathOptions = new OptionsWrapper<AppBasePathOptions>(
            new AppBasePathOptions { BasePath = basePath });
        return new AppPublicUrls(originOptions, basePathOptions);
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
        var urls = Create("https://vantigo.example.com", "/apps/vantigo/");

        Assert.Equal(
            "https://vantigo.example.com/apps/vantigo/api/v1/identity/oidc/callback",
            urls.PublicUrl("/api/v1/identity/oidc/callback"));
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
}