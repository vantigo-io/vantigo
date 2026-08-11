using Microsoft.Extensions.Configuration;

namespace Vantigo.Configuration.Tests;

public sealed class AppBrandingOptionsTests
{
    [Fact]
    public void GetTitle_WithoutConfiguration_UsesDefaultTitle()
    {
        var options = new AppBrandingOptions();

        Assert.Equal("Customers", options.GetTitle("Customers"));
    }

    [Fact]
    public void GetTitle_ReadsTitleFromConfiguration()
    {
        var options = Load(new Dictionary<string, string?> { ["Title"] = "Acme CRM" });

        Assert.Equal("Acme CRM", options.GetTitle("Customers"));
    }

    [Fact]
    public void Bind_ReadsEveryValueFromTheAppSection()
    {
        var options = Load(new Dictionary<string, string?>
        {
            ["Title"] = "Acme CRM",
            ["LogoUrl"] = "https://cdn.acme.test/logo.svg",
            ["Support:Email"] = "help@acme.test",
            ["Support:Phone"] = "+47 123 45 678",
            ["Support:Url"] = "https://support.acme.test",
        });

        Assert.Equal("Acme CRM", options.Title);
        Assert.Equal("https://cdn.acme.test/logo.svg", options.LogoUrl);
        Assert.Equal("help@acme.test", options.Support.Email);
        Assert.Equal("+47 123 45 678", options.Support.Phone);
        Assert.Equal("https://support.acme.test", options.Support.Url);
    }

    [Theory]
    [InlineData("")]
    [InlineData("   ")]
    public void GetTitle_TreatsBlankValuesAsUnset(string blank)
    {
        var options = new AppBrandingOptions { Title = blank };

        Assert.Equal("Customers", options.GetTitle("Customers"));
    }

    private static AppBrandingOptions Load(Dictionary<string, string?> values) =>
        new ConfigurationBuilder()
            .AddInMemoryCollection(values)
            .Build()
            .Get<AppBrandingOptions>()!;
}