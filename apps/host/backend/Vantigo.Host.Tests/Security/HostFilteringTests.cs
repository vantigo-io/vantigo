using Vantigo.Host.Security;

namespace Vantigo.Host.Tests.Security;

public sealed class HostFilteringTests
{
    [Fact]
    public void TheAllowlistIsTheConfiguredOriginPlusLoopback() =>
        Assert.Equal(
            ["vantigo.example.com", "localhost", "127.0.0.1", "[::1]"],
            HostFiltering.BuildAllowedHosts("https://vantigo.example.com"));

    [Fact]
    public void ThePortIsNotPartOfTheAllowlist() =>
        Assert.Equal(
            ["vantigo.example.com", "localhost", "127.0.0.1", "[::1]"],
            HostFiltering.BuildAllowedHosts("https://vantigo.example.com:8443"));

    [Fact]
    public void ALoopbackOriginIsNotListedTwice() =>
        Assert.Equal(
            ["localhost", "127.0.0.1", "[::1]"],
            HostFiltering.BuildAllowedHosts("http://localhost:8080"));

    [Theory]
    [InlineData(null)]
    [InlineData("")]
    public void WithoutAPublicOrigin_ThereIsNothingToDerive(string? origin) =>
        Assert.Empty(HostFiltering.BuildAllowedHosts(origin));

    /// <summary>
    /// The container health probe reaches the app on loopback with the literal
    /// address in the Host header. Host filtering runs before routing and cannot
    /// exempt endpoints by metadata, so loopback has to stay in the allowlist
    /// however narrow the public origin is.
    /// </summary>
    [Theory]
    [InlineData("localhost")]
    [InlineData("127.0.0.1")]
    [InlineData("[::1]")]
    public void LoopbackProbeAddressesSurviveANarrowPublicOrigin(string loopback) =>
        Assert.Contains(loopback, HostFiltering.BuildAllowedHosts("https://vantigo.example.com"));
}