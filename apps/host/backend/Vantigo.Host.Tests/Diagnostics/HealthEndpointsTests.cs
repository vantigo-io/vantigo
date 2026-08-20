using System.Net;

namespace Vantigo.Host.Tests.Diagnostics;

/// <summary>
/// Exercises the real <c>/health/live</c> and <c>/health/ready</c> endpoints
/// against a Testcontainers-managed PostgreSQL instance, proving the exact
/// status semantics issue #8 (Azure Bicep probes) and issue #18 (CD smoke
/// tests) depend on: 200 while healthy, 503 while a required dependency is
/// unreachable, and liveness that never depends on PostgreSQL at all.
/// </summary>
[Collection(HealthEndpointsCollection.Name)]
public sealed class HealthEndpointsTests(HealthEndpointsApiFactory factory)
{
    [Fact]
    public async Task Live_always_reports_ok_and_ready_reflects_postgresql_reachability()
    {
        using var client = factory.CreateClient();

        var liveWhileHealthy = await client.GetAsync("/health/live");
        Assert.Equal(HttpStatusCode.OK, liveWhileHealthy.StatusCode);

        var readyWhileHealthy = await client.GetAsync("/health/ready");
        Assert.Equal(HttpStatusCode.OK, readyWhileHealthy.StatusCode);

        await factory.StopDatabaseAsync();

        var readyWhilePostgresIsDown = await client.GetAsync("/health/ready");
        Assert.Equal(HttpStatusCode.ServiceUnavailable, readyWhilePostgresIsDown.StatusCode);

        var liveWhilePostgresIsDown = await client.GetAsync("/health/live");
        Assert.Equal(HttpStatusCode.OK, liveWhilePostgresIsDown.StatusCode);
    }
}