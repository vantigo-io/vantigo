using Asp.Versioning;

using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Options;

namespace Vantigo.Configuration.Tests;

public sealed class ApiVersioningServiceCollectionExtensionsTests
{
    [Fact]
    public void AddVantigoApiVersioning_RegistersDefaultVersioningConfiguration()
    {
        var services = new ServiceCollection();

        services.AddVantigoApiVersioning();

        using var provider = services.BuildServiceProvider();
        var options = provider.GetRequiredService<IOptions<ApiVersioningOptions>>();

        Assert.Equal(new ApiVersion(1), options.Value.DefaultApiVersion);
        Assert.True(options.Value.ReportApiVersions);
        Assert.IsType<UrlSegmentApiVersionReader>(options.Value.ApiVersionReader);
    }
}