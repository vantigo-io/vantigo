using System.Net.Http.Json;

using Microsoft.AspNetCore.Hosting;
using Microsoft.AspNetCore.Mvc.Testing;
using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;

using Testcontainers.PostgreSql;

using Vantigo.Contracts;
using Vantigo.Host;

namespace Vantigo.Energy.Module.Tests.Integration;

public sealed class EnergyApiFactory : WebApplicationFactory<global::Program>, IAsyncLifetime
{
    private const string BootstrapSecret = "integration-test-bootstrap-secret";
    private readonly PostgreSqlContainer _postgres = new PostgreSqlBuilder("postgres:17-alpine")
        .WithDatabase("vantigo")
        .Build();

    public async Task InitializeAsync()
    {
        await _postgres.StartAsync();
        using var client = CreateClient(new WebApplicationFactoryClientOptions { HandleCookies = true });
        var token = await client.GetFromJsonAsync<AntiforgeryToken>("/api/v1/identity/antiforgery");
        client.DefaultRequestHeaders.Add("X-XSRF-TOKEN", token!.Token);
        var bootstrap = await client.PostAsJsonAsync("/api/v1/identity/bootstrap", new
        {
            secret = BootstrapSecret,
            email = "owner@energy-integration.test",
            displayName = "Energy Integration Owner",
            password = "IntegrationPassword123",
        });
        if (!bootstrap.IsSuccessStatusCode)
            throw new InvalidOperationException($"Fresh integration bootstrap failed: {bootstrap.StatusCode}");
    }

    public HttpClient CreateAuthenticatedClient()
    {
        var client = CreateClient(new WebApplicationFactoryClientOptions { HandleCookies = true });
        var token = client.GetFromJsonAsync<AntiforgeryToken>("/api/v1/identity/antiforgery").GetAwaiter().GetResult();
        client.DefaultRequestHeaders.Add("X-XSRF-TOKEN", token!.Token);
        var login = client.PostAsJsonAsync("/api/v1/identity/login", new
        {
            email = "owner@energy-integration.test",
            password = "IntegrationPassword123",
        }).GetAwaiter().GetResult();
        if (!login.IsSuccessStatusCode)
            throw new InvalidOperationException($"Integration login failed: {login.StatusCode}");
        client.DefaultRequestHeaders.Remove("X-XSRF-TOKEN");
        var refreshedToken = client.GetFromJsonAsync<AntiforgeryToken>("/api/v1/identity/antiforgery").GetAwaiter().GetResult();
        client.DefaultRequestHeaders.Add("X-XSRF-TOKEN", refreshedToken!.Token);
        return client;
    }

    protected override void ConfigureWebHost(IWebHostBuilder builder)
    {
        builder.UseEnvironment(Environments.Development);
        builder.ConfigureAppConfiguration((_, configuration) => configuration.AddInMemoryCollection(new Dictionary<string, string?>
        {
            ["ConnectionStrings:vantigo"] = _postgres.GetConnectionString(),
            ["Modules:Customers:Enabled"] = "false",
            ["Modules:Communications:Enabled"] = "false",
            ["Modules:Products:Enabled"] = "false",
            ["Modules:Energy:Enabled"] = "true",
            ["Development:Seed:Enabled"] = "false",
            ["Authentication:Bootstrap:Secret"] = BootstrapSecret,
            ["Authentication:PasswordReset:ResetUrl"] = "http://test.local/reset?email={email}&token={token}",
            ["Authentication:Invitations:AcceptUrl"] = "http://test.local/invitations?token={token}",
        }));
        builder.ConfigureServices(services =>
        {
            services.AddSingleton<ICustomerDirectory, FakeCustomerDirectory>();
            services.AddSingleton(new HostTestStartupPreparation(ApplyMigrations: true, SeedDevelopmentData: false));
        });
    }

    async Task IAsyncLifetime.DisposeAsync()
    {
        await base.DisposeAsync();
        await _postgres.DisposeAsync();
    }

    private sealed class FakeCustomerDirectory : ICustomerDirectory
    {
        public Task<CustomerDirectoryEntry?> FindCustomerAsync(int customerId, CancellationToken cancellationToken = default) =>
            Task.FromResult<CustomerDirectoryEntry?>(customerId is 1001 or 1002 ? new(customerId, $"Test customer {customerId}") : null);

        public Task<ContactDirectoryEntry?> FindContactAsync(int contactId, CancellationToken cancellationToken = default) =>
            Task.FromResult<ContactDirectoryEntry?>(null);
    }
}

internal sealed record AntiforgeryToken(string Token);

[CollectionDefinition(Name)]
public sealed class EnergyModuleCollection : ICollectionFixture<EnergyApiFactory>
{
    public const string Name = "EnergyModule";
}
