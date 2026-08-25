using System.Net;

using Testcontainers.PostgreSql;

using Vantigo.Host;
using Vantigo.Tenancy.EntityFramework;

namespace Vantigo.Host.Tests.Composition;

/// <summary>
/// Two hosts migrating the same fresh database at the same time must
/// serialize on the installation-wide advisory lock: one migrates, the other
/// waits and then re-runs idempotently, and both finish healthy. Without the
/// lock this scenario corrupts a half-upgraded schema or fails one migrator.
/// </summary>
public sealed class ConcurrentMigrationTests : IAsyncLifetime
{
    private readonly PostgreSqlContainer _postgres = new PostgreSqlBuilder("postgres:17-alpine")
        .WithDatabase("vantigo")
        .Build();

    public Task InitializeAsync() => _postgres.StartAsync();

    public async Task DisposeAsync() => await _postgres.DisposeAsync();

    [Fact]
    public async Task Concurrent_migrators_serialize_and_both_hosts_become_healthy()
    {
        var runtime = await TenantRuntimeRoleSql.ProvisionAsync(_postgres.GetConnectionString());
        var activation = new ModuleActivation(customers: true, communications: false, products: false, energy: false);

        await using var first = new ModuleActivationApiFactory(runtime, _postgres.GetConnectionString(), activation);
        await using var second = new ModuleActivationApiFactory(runtime, _postgres.GetConnectionString(), activation);

        // Building each host runs the full migration pass; starting both at
        // once races them against the same empty schema.
        var clients = await Task.WhenAll(
            Task.Run(() => first.CreateClient()),
            Task.Run(() => second.CreateClient()));

        foreach (var client in clients)
        {
            using var response = await client.GetAsync("/health/ready");
            Assert.Equal(HttpStatusCode.OK, response.StatusCode);
        }
    }
}