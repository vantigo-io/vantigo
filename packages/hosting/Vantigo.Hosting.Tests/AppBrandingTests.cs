using Microsoft.Extensions.Configuration;

using Vantigo.Hosting;

namespace Vantigo.Hosting.Tests;

public sealed class AppBrandingTests
{
    private static IConfiguration Config(Dictionary<string, string?> values) =>
        new ConfigurationBuilder().AddInMemoryCollection(values).Build();

    [Fact]
    public void Load_WithoutConfiguration_UsesTheDefaultTitleAndNoBranding()
    {
        var branding = AppBranding.Load(Config([]), "Customers");

        Assert.Equal("Customers", branding.Title);
        Assert.Null(branding.LogoUrl);
        Assert.Null(branding.SupportEmail);
        Assert.Null(branding.SupportPhone);
        Assert.Null(branding.SupportUrl);
    }

    [Fact]
    public void Load_ReadsEveryValueFromTheAppSection()
    {
        var branding = AppBranding.Load(
            Config(new Dictionary<string, string?>
            {
                ["App:Title"] = "Acme CRM",
                ["App:LogoUrl"] = "https://cdn.acme.test/logo.svg",
                ["App:Support:Email"] = "help@acme.test",
                ["App:Support:Phone"] = "+47 123 45 678",
                ["App:Support:Url"] = "https://support.acme.test",
            }),
            "Customers");

        Assert.Equal("Acme CRM", branding.Title);
        Assert.Equal("https://cdn.acme.test/logo.svg", branding.LogoUrl);
        Assert.Equal("help@acme.test", branding.SupportEmail);
        Assert.Equal("+47 123 45 678", branding.SupportPhone);
        Assert.Equal("https://support.acme.test", branding.SupportUrl);
    }

    [Theory]
    [InlineData("")]
    [InlineData("   ")]
    public void Load_TreatsBlankValuesAsUnset(string blank)
    {
        var branding = AppBranding.Load(
            Config(new Dictionary<string, string?>
            {
                ["App:Title"] = blank,
                ["App:LogoUrl"] = blank,
                ["App:Support:Email"] = blank,
            }),
            "Customers");

        Assert.Equal("Customers", branding.Title);
        Assert.Null(branding.LogoUrl);
        Assert.Null(branding.SupportEmail);
    }

    [Fact]
    public void Load_TrimsWhitespace()
    {
        var branding = AppBranding.Load(
            Config(new Dictionary<string, string?> { ["App:Title"] = "  Acme CRM  " }),
            "Customers");

        Assert.Equal("Acme CRM", branding.Title);
    }
}