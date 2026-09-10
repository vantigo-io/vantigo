using System.Net.Http.Json;

using Microsoft.AspNetCore.Hosting;
using Microsoft.AspNetCore.Mvc.Testing;
using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;

using Testcontainers.PostgreSql;

using Vantigo.Contracts;
using Vantigo.Host;
using Vantigo.Identity.Services;
using Vantigo.Tenancy;
using Vantigo.Tenancy.Abstractions;
using Vantigo.Tenancy.EntityFramework;
using Vantigo.Testing;

namespace Vantigo.Energy.Module.Tests.Integration;

public sealed class EnergyApiFactory : WebApplicationFactory<global::Program>, IAsyncLifetime
{
    private const string BootstrapSecret = "integration-test-bootstrap-secret";
    private readonly PostgreSqlContainer _postgres = new PostgreSqlBuilder("postgres:17-alpine")
        .WithDatabase("vantigo")
        .Build();

    public Guid CurrentTenantId { get; private set; }

    /// <summary>
    /// Least-privilege runtime role connection string the application connects
    /// with; migrations use the container superuser.
    /// </summary>
    internal string RuntimeConnectionString { get; private set; } = string.Empty;

    public async Task InitializeAsync()
    {
        await _postgres.StartAsync();
        RuntimeConnectionString = await TenantRuntimeRoleSql.ProvisionAsync(_postgres.GetConnectionString());
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
        await using var scope = Services.CreateAsyncScope();
        CurrentTenantId = (await scope.ServiceProvider.GetRequiredService<TenantDirectory>()
            .GetDefaultTenantAsync()).Value;
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

    public IDisposable EnterTenant(Guid tenantId) => AmbientTenantContext.Enter(new TenantId(tenantId));

    public async Task<HttpClient> CreateAuthenticatedClientAsync(string email, string password)
    {
        var client = CreateClient(new WebApplicationFactoryClientOptions { HandleCookies = true });
        var token = await client.GetFromJsonAsync<AntiforgeryToken>("/api/v1/identity/antiforgery");
        client.DefaultRequestHeaders.Add("X-XSRF-TOKEN", token!.Token);
        var login = await client.PostAsJsonAsync("/api/v1/identity/login", new { email, password });
        if (!login.IsSuccessStatusCode)
            throw new InvalidOperationException($"Integration login failed: {login.StatusCode}");
        client.DefaultRequestHeaders.Remove("X-XSRF-TOKEN");
        var refreshedToken = await client.GetFromJsonAsync<AntiforgeryToken>("/api/v1/identity/antiforgery");
        client.DefaultRequestHeaders.Add("X-XSRF-TOKEN", refreshedToken!.Token);
        return client;
    }

    protected override void ConfigureWebHost(IWebHostBuilder builder)
    {
        builder.UseEnvironment(Environments.Development);
        // Host configuration, so the module flags are in place before the host
        // composes its modules. ConfigureAppConfiguration is applied while the
        // host is built, which is after the modules have been registered.
        //
        // Energy reads customer and contact data through ICustomerDirectory, so the
        // host refuses to compose Energy without Customers. The fake below still
        // serves the reads; the module only has to be part of the combination.
        builder.UseSetting("Modules:Customers:Enabled", "true");
        builder.UseSetting("Modules:Communications:Enabled", "false");
        builder.UseSetting("Modules:Products:Enabled", "false");
        builder.UseSetting("Modules:Energy:Enabled", "true");
        builder.ConfigureAppConfiguration((_, configuration) => configuration.AddInMemoryCollection(new Dictionary<string, string?>
        {
            ["ConnectionStrings:vantigo"] = RuntimeConnectionString,
            ["ConnectionStrings:migrations"] = _postgres.GetConnectionString(),
            ["Development:Seed:Enabled"] = "false",
            ["Authentication:Bootstrap:Secret"] = BootstrapSecret,
            ["Authentication:PasswordReset:ResetUrl"] = "http://test.local/reset?email={email}&token={token}",
            ["Authentication:Invitations:AcceptUrl"] = "http://test.local/invitations?token={token}",
        }));
        builder.ConfigureServices(services =>
        {
            services.AddContractRecording();
            // Registered after the Customers module, so this is the implementation
            // the Energy endpoints resolve.
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