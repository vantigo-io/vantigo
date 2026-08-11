using Microsoft.Extensions.Configuration;

namespace Vantigo.Configuration.Tests;

public sealed class AppBasePathOptionsTests
{
    [Theory]
    [InlineData(null, null)]
    [InlineData("", null)]
    [InlineData("   ", null)]
    [InlineData("/", null)]
    [InlineData("//", null)]
    [InlineData("/customers", "/customers")]
    [InlineData("customers", "/customers")]
    [InlineData("/customers/", "/customers")]
    [InlineData(" /crm ", "/crm")]
    [InlineData("/nested/prefix/", "/nested/prefix")]
    public void Normalized_ProducesLeadingSlashWithoutTrailingSlash(string? configured, string? expected)
    {
        var options = new AppBasePathOptions { BasePath = configured };
        Assert.Equal(expected, options.Normalized);
    }
}