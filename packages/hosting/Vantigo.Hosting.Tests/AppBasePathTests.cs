using Vantigo.Hosting;

namespace Vantigo.Hosting.Tests;

public sealed class AppBasePathTests
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
    public void Normalize_ProducesLeadingSlashWithoutTrailingSlash(string? configured, string? expected)
    {
        Assert.Equal(expected, AppBasePath.Normalize(configured));
    }
}