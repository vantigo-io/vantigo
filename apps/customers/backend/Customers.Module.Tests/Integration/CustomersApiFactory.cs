using System.Net.Http.Json;

using Microsoft.AspNetCore.Hosting;
using Microsoft.AspNetCore.Mvc.Testing;
using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.DependencyInjection.Extensions;
using Microsoft.Extensions.Hosting;

using Testcontainers.PostgreSql;

using Vantigo.Host;
using Vantigo.Identity.Services;

namespace Vantigo.Customers.Module.Tests.Integration;

/// <summary>
/// Boots the Customers API against a real PostgreSQL instance running in a
/// Testcontainers-managed Docker container. The container is shared across all
/// tests in the <see cref="CustomersApiCollection"/> to keep the test run fast.
/// Migrations are applied by the API itself on startup. Outbound calls to
/// Brønnøysundregisteret are routed to <see cref="BrregHandler"/> instead of the
/// real registry.
/// </summary>
public sealed class CustomersApiFactory : WebApplicationFactory<Program>, IAsyncLifetime
{
    private const string BootstrapSecret = "integration-test-bootstrap-secret";
    private readonly PostgreSqlContainer _postgres = new PostgreSqlBuilder("postgres:17-alpine")
        .WithDatabase("vantigo")
        .Build();

    public StubBrregHandler BrregHandler { get; } = new();

    internal CapturingEmailSender EmailSender { get; } = new();

    public async Task InitializeAsync()
    {
        await _postgres.StartAsync();

        using var client = CreateClient(new WebApplicationFactoryClientOptions { HandleCookies = true });
        var tokenResponse = await client.GetAsync("/api/v1/identity/antiforgery");
        var token = await tokenResponse.Content.ReadFromJsonAsync<AntiforgeryToken>();
        client.DefaultRequestHeaders.Add("X-XSRF-TOKEN", token!.Token);
        var bootstrap = await client.PostAsJsonAsync("/api/v1/identity/bootstrap", new
        {
            secret = BootstrapSecret,
            email = "owner@integration.test",
            displayName = "Integration Owner",
            password = "IntegrationPassword123",
        });
        if (bootstrap.StatusCode != System.Net.HttpStatusCode.Created)
        {
            throw new InvalidOperationException($"Fresh integration bootstrap failed: {bootstrap.StatusCode}");
        }
    }

    public HttpClient CreateAuthenticatedClient()
    {
        var client = CreateClient(new WebApplicationFactoryClientOptions { HandleCookies = true });
        var token = client.GetFromJsonAsync<AntiforgeryToken>("/api/v1/identity/antiforgery").GetAwaiter().GetResult();
        client.DefaultRequestHeaders.Add("X-XSRF-TOKEN", token!.Token);
        var login = client.PostAsJsonAsync("/api/v1/identity/login", new
        {
            email = "owner@integration.test",
            password = "IntegrationPassword123",
        }).GetAwaiter().GetResult();
        if (!login.IsSuccessStatusCode)
        {
            throw new InvalidOperationException($"Integration login failed: {login.StatusCode}");
        }

        client.DefaultRequestHeaders.Remove("X-XSRF-TOKEN");
        var refreshedToken = client.GetFromJsonAsync<AntiforgeryToken>("/api/v1/identity/antiforgery").GetAwaiter().GetResult();
        client.DefaultRequestHeaders.Add("X-XSRF-TOKEN", refreshedToken!.Token);

        return client;
    }

    protected override void ConfigureWebHost(IWebHostBuilder builder)
    {
        builder.UseEnvironment(Environments.Development);

        builder.ConfigureAppConfiguration((_, configuration) =>
        {
            configuration.AddInMemoryCollection(new Dictionary<string, string?>
            {
                ["ConnectionStrings:vantigo"] = _postgres.GetConnectionString(),
                ["Modules:Customers:Enabled"] = "true",
                ["Modules:Communications:Enabled"] = "false",
                ["Modules:Products:Enabled"] = "false",
                ["Development:Seed:Enabled"] = "false",
                ["Authentication:Bootstrap:Secret"] = BootstrapSecret,
                ["Authentication:PasswordReset:ResetUrl"] = "http://test.local/reset?email={email}&token={token}",
                ["Authentication:Invitations:AcceptUrl"] = "http://test.local/invitations?token={token}",
            });
        });

        builder.ConfigureServices(services =>
        {
            services.AddSingleton(new HostTestStartupPreparation(
                ApplyMigrations: true,
                SeedDevelopmentData: false));
            services.RemoveAll<IApplicationEmailSender>();
            services.AddSingleton<IApplicationEmailSender>(EmailSender);
            services.AddHttpClient("brreg")
                .ConfigurePrimaryHttpMessageHandler(() => BrregHandler);
        });
    }

    async Task IAsyncLifetime.DisposeAsync()
    {
        await base.DisposeAsync();
        await _postgres.DisposeAsync();
    }
}

internal sealed record AntiforgeryToken(string Token);

[CollectionDefinition(Name)]
public sealed class CustomersApiCollection : ICollectionFixture<CustomersApiFactory>
{
    public const string Name = "CustomersApi";
}