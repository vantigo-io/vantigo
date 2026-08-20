using Vantigo.Host.Security;

namespace Vantigo.Host.Tests.Security;

public sealed class HostFilteringTests
{
    [Fact]
    public void TheAllowlistIsTheConfiguredOriginPlusLoopback() =>
        Assert.Equal(
            ["vantigo.example.com", "localhost", "127.0.0.1"],
            HostFiltering.BuildAllowedHosts("https://vantigo.example.com"));

    [Fact]
    public void ThePortIsNotPartOfTheAllowlist() =>
        Assert.Equal(
            ["vantigo.example.com", "localhost", "127.0.0.1"],
            HostFiltering.BuildAllowedHosts("https://vantigo.example.com:8443"));

    [Fact]
    public void ALoopbackOriginIsNotListedTwice() =>
        Assert.Equal(
            ["localhost", "127.0.0.1"],
            HostFiltering.BuildAllowedHosts("http://localhost:8080"));

    [Theory]
    [InlineData(null)]
    [InlineData("")]
    public void WithoutAPublicOrigin_ThereIsNothingToDerive(string? origin) =>
        Assert.Empty(HostFiltering.BuildAllowedHosts(origin));
}