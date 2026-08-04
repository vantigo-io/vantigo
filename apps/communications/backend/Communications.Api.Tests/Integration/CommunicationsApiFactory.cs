using System.Net.Http.Json;

using Microsoft.AspNetCore.Hosting;
using Microsoft.AspNetCore.Mvc.Testing;
using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.DependencyInjection.Extensions;
using Microsoft.Extensions.Hosting;

using Testcontainers.PostgreSql;

using Vantigo.Communications.Api.Services;

namespace Vantigo.Communications.Api.Tests.Integration;

public sealed class CommunicationsApiFactory : WebApplicationFactory<Program>, IAsyncLifetime
{
    private const string BootstrapSecret = "integration-bootstrap-secret";
    public const string ServiceKey = "integration-customers-service-key";
    private readonly PostgreSqlContainer postgres = new PostgreSqlBuilder("postgres:17-alpine")
        .WithDatabase("communications")
        .Build();

    public CapturingEmailSender Sender { get; } = new();

    public async Task InitializeAsync()
    {
        await postgres.StartAsync();
        using var client = CreateClient(new WebApplicationFactoryClientOptions { HandleCookies = true });
        var token = await GetAntiforgeryToken(client);
        client.DefaultRequestHeaders.Add("X-XSRF-TOKEN", token);
        var response = await client.PostAsJsonAsync("/auth/bootstrap", new
        {
            secret = BootstrapSecret,
            email = "owner@integration.test",
            displayName = "Integration Owner",
            password = "IntegrationPassword123",
        });
        Assert.Equal(System.Net.HttpStatusCode.Created, response.StatusCode);
    }

    public async Task<HttpClient> CreateAuthenticatedClientAsync()
    {
        var client = CreateClient(new WebApplicationFactoryClientOptions { HandleCookies = true });
        var token = await GetAntiforgeryToken(client);
        client.DefaultRequestHeaders.Add("X-XSRF-TOKEN", token);
        var response = await client.PostAsJsonAsync("/auth/login", new
        {
            email = "owner@integration.test",
            password = "IntegrationPassword123",
        });
        response.EnsureSuccessStatusCode();
        client.DefaultRequestHeaders.Remove("X-XSRF-TOKEN");
        client.DefaultRequestHeaders.Add("X-XSRF-TOKEN", await GetAntiforgeryToken(client));
        return client;
    }

    public async Task<string> GetAntiforgeryToken(HttpClient client)
    {
        var response = await client.GetFromJsonAsync<AntiforgeryToken>("/auth/antiforgery");
        return response!.Token;
    }

    protected override void ConfigureWebHost(IWebHostBuilder builder)
    {
        builder.UseEnvironment(Environments.Development);
        builder.ConfigureAppConfiguration((_, configuration) => configuration.AddInMemoryCollection(new Dictionary<string, string?>
        {
            ["ConnectionStrings:Postgresql"] = postgres.GetConnectionString(),
            ["Authentication:Bootstrap:Secret"] = BootstrapSecret,
            ["Customers:Enabled"] = "true",
            ["Customers:ApiKey"] = ServiceKey,
            ["Communications:BootstrapMailbox:Enabled"] = "true",
            ["Communications:BootstrapMailbox:FromAddress"] = "noreply@integration.test",
            ["Communications:BootstrapMailbox:DisplayName"] = "Integration Mailbox",
            ["Outbox:PollSeconds"] = "3600",
        }));
        builder.ConfigureServices(services =>
        {
            services.RemoveAll<IEmailSender>();
            services.AddSingleton<IEmailSender>(Sender);
        });
    }

    async Task IAsyncLifetime.DisposeAsync()
    {
        await base.DisposeAsync();
        await postgres.DisposeAsync();
    }
}

public sealed record AntiforgeryToken(string Token);

public sealed class CapturingEmailSender : IEmailSender
{
    private readonly List<EmailEnvelope> envelopes = [];
    public bool ThrowOnSend { get; set; }
    public IReadOnlyList<EmailEnvelope> Envelopes => envelopes;

    public Task SendAsync(EmailEnvelope envelope, CancellationToken cancellationToken)
    {
        if (ThrowOnSend) throw new InvalidOperationException("fake SMTP failure");
        envelopes.Add(envelope);
        return Task.CompletedTask;
    }
}

[CollectionDefinition(Name)]
public sealed class CommunicationsApiCollection : ICollectionFixture<CommunicationsApiFactory>
{
    public const string Name = "CommunicationsApi";
}
