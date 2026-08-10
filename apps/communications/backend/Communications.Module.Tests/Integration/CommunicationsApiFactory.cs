using System.Net.Http.Json;

using Microsoft.AspNetCore.Hosting;
using Microsoft.AspNetCore.Mvc.Testing;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.DependencyInjection.Extensions;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Http;

using Testcontainers.PostgreSql;

using Vantigo.Communications.Database.Communications;
using Vantigo.Communications.Services;
using Vantigo.Host;

namespace Vantigo.Communications.Module.Tests.Integration;

public sealed class CommunicationsModuleFactory : WebApplicationFactory<global::Program>, IAsyncLifetime
{
    public const string BootstrapMailboxAddress = "noreply@integration.test";
    private readonly PostgreSqlContainer postgres = new PostgreSqlBuilder("postgres:17-alpine")
        .WithDatabase("vantigo")
        .Build();

    public CapturingEmailSender Sender { get; } = new();
    public FailingMailgunHandler MailgunHandler { get; } = new();

    public async Task InitializeAsync()
    {
        await postgres.StartAsync();
        using var client = CreateClient(new WebApplicationFactoryClientOptions { HandleCookies = true });
        var token = await GetAntiforgeryToken(client);
        client.DefaultRequestHeaders.Add("X-XSRF-TOKEN", token);
        var response = await client.PostAsJsonAsync("/api/v1/identity/bootstrap", new
        {
            secret = "integration-bootstrap-secret",
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
        var response = await client.PostAsJsonAsync("/api/v1/identity/login", new
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
        var response = await client.GetFromJsonAsync<AntiforgeryToken>("/api/v1/identity/antiforgery");
        return response!.Token;
    }

    protected override void ConfigureWebHost(IWebHostBuilder builder)
    {
        builder.UseEnvironment(Environments.Development);
        builder.ConfigureAppConfiguration((_, configuration) => configuration.AddInMemoryCollection(new Dictionary<string, string?>
        {
            ["ConnectionStrings:vantigo"] = postgres.GetConnectionString(),
            ["Authentication:Bootstrap:Secret"] = "integration-bootstrap-secret",
            ["Development:Seed:Enabled"] = "false",
            ["Modules:Customers:Enabled"] = "true",
            ["Modules:Communications:Enabled"] = "true",
            ["Modules:Products:Enabled"] = "false",
            ["Communications:BootstrapMailbox:Enabled"] = "true",
            ["Communications:BootstrapMailbox:FromAddress"] = BootstrapMailboxAddress,
            ["Communications:BootstrapMailbox:DisplayName"] = "Integration Mailbox",
            ["Outbox:PollSeconds"] = "3600",
        }));
        builder.ConfigureServices(services =>
        {
            services.AddSingleton(new HostTestStartupPreparation(ApplyMigrations: true, SeedDevelopmentData: false));
            services.RemoveAll<IEmailSender>();
            services.AddSingleton<IEmailSender>(Sender);
            services.Configure<HttpClientFactoryOptions>("mailgun", options =>
                options.HttpMessageHandlerBuilderActions.Add(builder => builder.PrimaryHandler = MailgunHandler));
        });
    }

    public async Task ResetMailboxStateAsync()
    {
        await using var scope = Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>();
        await using var transaction = await db.Database.BeginTransactionAsync();

        var bootstrap = await db.SharedMailboxes.SingleAsync(item => item.FromAddress == BootstrapMailboxAddress);
        var extraMailboxIds = await db.SharedMailboxes
            .Where(item => item.Id != bootstrap.Id)
            .Select(item => item.Id)
            .ToArrayAsync();
        await db.IdempotencyRecords.ExecuteDeleteAsync();
        await db.EmailMessages.ExecuteDeleteAsync();
        if (extraMailboxIds.Length > 0)
        {
            await db.MailboxProviderCredentials.Where(item => extraMailboxIds.Contains(item.MailboxId)).ExecuteDeleteAsync();
            await db.SharedMailboxes.Where(item => extraMailboxIds.Contains(item.Id)).ExecuteDeleteAsync();
        }

        await db.MailboxProviderCredentials.Where(item => item.MailboxId == bootstrap.Id).ExecuteDeleteAsync();
        await db.SharedMailboxes
            .Where(item => item.Id == bootstrap.Id)
            .ExecuteUpdateAsync(setters => setters
                .SetProperty(item => item.FromAddress, BootstrapMailboxAddress)
                .SetProperty(item => item.DisplayName, "Integration Mailbox")
                .SetProperty(item => item.Provider, "smtp")
                .SetProperty(item => item.IsActive, true)
                .SetProperty(item => item.IsDefault, true));

        await transaction.CommitAsync();
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

    public Task SendAsync(EmailEnvelope envelope, Vantigo.Communications.Database.Communications.SharedMailbox mailbox, CancellationToken cancellationToken)
    {
        if (ThrowOnSend) throw new InvalidOperationException("fake SMTP failure");
        envelopes.Add(envelope);
        return Task.CompletedTask;
    }
}

public sealed class FailingMailgunHandler : HttpMessageHandler
{
    protected override Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken) =>
        Task.FromResult(new HttpResponseMessage(System.Net.HttpStatusCode.BadGateway)
        {
            ReasonPhrase = "Deterministic test failure",
            Content = new StringContent("mailgun verification failed in test"),
        });
}

[CollectionDefinition(Name)]
public sealed class CommunicationsModuleCollection : ICollectionFixture<CommunicationsModuleFactory>
{
    public const string Name = "CommunicationsModule";
}