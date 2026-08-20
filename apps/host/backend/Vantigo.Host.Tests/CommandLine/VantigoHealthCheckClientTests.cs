namespace Vantigo.Host.Tests.CommandLine;

public sealed class VantigoHealthCheckClientTests
{
    [Fact]
    public async Task RunAsync_WithNothingListeningOnThePort_ReturnsFalse()
    {
        Environment.SetEnvironmentVariable("ASPNETCORE_HTTP_PORTS", "1");
        try
        {
            var healthy = await VantigoHealthCheckClient.RunAsync();

            Assert.False(healthy);
        }
        finally
        {
            Environment.SetEnvironmentVariable("ASPNETCORE_HTTP_PORTS", null);
        }
    }
}