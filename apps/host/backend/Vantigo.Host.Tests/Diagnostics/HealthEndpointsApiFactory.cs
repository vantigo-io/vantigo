using Microsoft.AspNetCore.Hosting;
using Microsoft.AspNetCore.Mvc.Testing;
using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;

using Testcontainers.PostgreSql;

namespace Vantigo.Host.Tests.Diagnostics;

/// <summary>
/// Boots the full host API against a real PostgreSQL instance running in a
/// Testcontainers-managed Docker container, so <c>/health/ready</c> exercises
/// the actual <see cref="global::Program"/> composition. <see cref="StopDatabaseAsync"/>
/// lets tests simulate a PostgreSQL outage without tearing down the host.
/// </summary>
public sealed class HealthEndpointsApiFactory : WebApplicationFactory<Program>, IAsyncLifetime
{
    private const string BootstrapSecret = "health-integration-bootstrap-secret";
    private readonly PostgreSqlContainer _postgres = new PostgreSqlBuilder("postgres:17-alpine")
        .WithDatabase("vantigo")
        .Build();

    public async Task InitializeAsync()
    {
        await _postgres.StartAsync();

        // Forces the host to build (migrations, tenant bootstrap, system admin
        // bootstrap) while PostgreSQL is still reachable.
        using var client = CreateClient();
        var response = await client.GetAsync("/health/ready");
        if (!response.IsSuccessStatusCode)
        {
            throw new InvalidOperationException(
                $"Host failed to become healthy during test setup: {response.StatusCode}");
        }
    }

    public Task StopDatabaseAsync() => _postgres.StopAsync();

    protected override void ConfigureWebHost(IWebHostBuilder builder)
    {
        builder.UseEnvironment(Environments.Development);
        // Host configuration, so the module flags are in place before the host
        // composes its modules. ConfigureAppConfiguration is applied while the
        // host is built, which is after the modules have been registered.
        builder.UseSetting("Modules:Customers:Enabled", "true");
        builder.UseSetting("Modules:Communications:Enabled", "false");
        builder.UseSetting("Modules:Products:Enabled", "false");
        builder.UseSetting("Modules:Energy:Enabled", "false");
        builder.ConfigureAppConfiguration((_, configuration) => configuration.AddInMemoryCollection(new Dictionary<string, string?>
        {
            ["ConnectionStrings:vantigo"] = _postgres.GetConnectionString(),
            ["Development:Seed:Enabled"] = "false",
            ["Authentication:Bootstrap:Secret"] = BootstrapSecret,
            ["Authentication:PasswordReset:ResetUrl"] = "http://test.local/reset?email={email}&token={token}",
            ["Authentication:Invitations:AcceptUrl"] = "http://test.local/invitations?token={token}",
        }));
        builder.ConfigureServices(services =>
            services.AddSingleton(new HostTestStartupPreparation(ApplyMigrations: true, SeedDevelopmentData: false)));
    }

    async Task IAsyncLifetime.DisposeAsync()
    {
        await base.DisposeAsync();
        await _postgres.DisposeAsync();
    }
}

[CollectionDefinition(Name, DisableParallelization = true)]
public sealed class HealthEndpointsCollection : ICollectionFixture<HealthEndpointsApiFactory>
{
    public const string Name = "HealthEndpoints";
}