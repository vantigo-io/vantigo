using System.Net.Http.Json;

using Microsoft.AspNetCore.Hosting;
using Microsoft.AspNetCore.Identity;
using Microsoft.AspNetCore.Mvc.Testing;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.DependencyInjection.Extensions;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Http;

using Testcontainers.PostgreSql;

using Vantigo.Communications.Database.Communications;
using Vantigo.Communications.Infrastructure.Storage;
using Vantigo.Communications.Services;
using Vantigo.Contracts;
using Vantigo.Contracts.Identity;
using Vantigo.Host;
using Vantigo.Identity.Database.Accounts;
using Vantigo.Storage.Abstractions;
using Vantigo.Tenancy;
using Vantigo.Tenancy.Abstractions;

namespace Vantigo.Communications.Module.Tests.Integration;

public sealed class CommunicationsModuleFactory : WebApplicationFactory<global::Program>, IAsyncLifetime
{
    public const string BootstrapChannelAddress = "noreply@integration.test";
    private readonly PostgreSqlContainer postgres = new PostgreSqlBuilder("postgres:17-alpine").WithDatabase("vantigo").Build();
    internal CapturingEmailSender Sender { get; } = new();
    internal TestAttachmentScanner AttachmentScanner { get; } = new();
    internal InMemoryObjectStore ObjectStore { get; } = new();
    internal IObjectStore<CommunicationsStorageScope> CommunicationsStore => ObjectStore.CommunicationsStore;
    internal StubCustomerDirectory CustomerDirectory { get; } = new();
    public FailingMailgunHandler MailgunHandler { get; } = new();

    public async Task InitializeAsync()
    {
        await postgres.StartAsync();
        using var client = CreateClient(new WebApplicationFactoryClientOptions { HandleCookies = true });
        var token = await GetAntiforgeryToken(client);
        client.DefaultRequestHeaders.Add("X-XSRF-TOKEN", token);
        var response = await client.PostAsJsonAsync("/api/v1/identity/bootstrap", new { secret = "integration-bootstrap-secret", email = "owner@integration.test", displayName = "Integration Owner", password = "IntegrationPassword123" });
        response.EnsureSuccessStatusCode();
        await using var tenantScope = Services.CreateAsyncScope();
        var tenant = await tenantScope.ServiceProvider.GetRequiredService<AccountsDbContext>().Tenants
            .Where(item => item.Slug == "default").Select(item => item.Id).SingleAsync();
        tenantScope.ServiceProvider.GetRequiredService<AmbientTenantContext>();
        TestTenantContext.DefaultTenant = new TenantId(tenant);
    }

    public async Task<HttpClient> CreateAuthenticatedClientAsync() => await CreateAuthenticatedClientAsync("owner@integration.test", "IntegrationPassword123");
    public async Task<HttpClient> CreateAuthenticatedClientAsync(string email, string password)
    {
        var client = CreateClient(new WebApplicationFactoryClientOptions { HandleCookies = true });
        client.DefaultRequestHeaders.Add("X-XSRF-TOKEN", await GetAntiforgeryToken(client));
        (await client.PostAsJsonAsync("/api/v1/identity/login", new { email, password })).EnsureSuccessStatusCode();
        client.DefaultRequestHeaders.Remove("X-XSRF-TOKEN");
        client.DefaultRequestHeaders.Add("X-XSRF-TOKEN", await GetAntiforgeryToken(client));
        return client;
    }

    public async Task<CommunicationsTestUser> CreateUserAsync(string? permissionKey = null)
    {
        var email = $"user-{Guid.NewGuid():N}@integration.test"; const string password = "IntegrationUserPassword123";
        await using var scope = Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        var users = scope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();
        var roles = scope.ServiceProvider.GetRequiredService<RoleManager<IdentityRole<Guid>>>();
        var user = new ApplicationUser { UserName = email, Email = email, EmailConfirmed = true, DisplayName = "Communications Test User" };
        if (!(await users.CreateAsync(user, password)).Succeeded || !(await users.AddToRoleAsync(user, AuthRoles.User)).Succeeded) throw new InvalidOperationException("Could not create test user.");
        if (permissionKey is not null)
        {
            var role = new IdentityRole<Guid>($"communications-test-{Guid.NewGuid():N}");
            if (!(await roles.CreateAsync(role)).Succeeded) throw new InvalidOperationException("Could not create test role.");
            db.RolePermissions.Add(new RolePermission { RoleId = role.Id, PermissionKey = permissionKey }); await db.SaveChangesAsync();
            if (!(await users.AddToRoleAsync(user, role.Name!)).Succeeded) throw new InvalidOperationException("Could not assign test role.");
        }
        return new CommunicationsTestUser(user.Id, email, password);
    }

    public async Task DisableUserAsync(Guid userId)
    {
        await using var scope = Services.CreateAsyncScope();
        await scope.ServiceProvider.GetRequiredService<AccountsDbContext>().Users.Where(user => user.Id == userId).ExecuteUpdateAsync(setters => setters.SetProperty(user => user.IsDisabled, true));
    }

    public async Task<string> GetAntiforgeryToken(HttpClient client) => (await client.GetFromJsonAsync<AntiforgeryToken>("/api/v1/identity/antiforgery"))!.Token;

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
            ["Communications:BootstrapMailbox:FromAddress"] = BootstrapChannelAddress,
            ["Communications:BootstrapMailbox:DisplayName"] = "Integration Channel",
            ["Outbox:PollSeconds"] = "3600",
            ["Communications:Inbound:PollSeconds"] = "3600",
        }));
        builder.ConfigureServices(services =>
        {
            services.AddSingleton<AmbientTenantContext>();
            services.RemoveAll<ITenantContext>();
            services.AddSingleton<TestTenantContext>();
            services.AddSingleton<ITenantContext>(provider => provider.GetRequiredService<TestTenantContext>());
            services.AddSingleton(new HostTestStartupPreparation(ApplyMigrations: true, SeedDevelopmentData: false));
            services.RemoveAll<IEmailSender>(); services.AddSingleton<IEmailSender>(Sender);
            services.RemoveAll<IObjectStore<CommunicationsStorageScope>>(); services.AddSingleton<IObjectStore<CommunicationsStorageScope>>(ObjectStore.CommunicationsStore);
            services.RemoveAll<IAttachmentScanner>(); services.AddSingleton<IAttachmentScanner>(AttachmentScanner);
            services.RemoveAll<ICustomerDirectory>(); services.AddSingleton<ICustomerDirectory>(CustomerDirectory);
            services.Configure<HttpClientFactoryOptions>("mailgun", options => options.HttpMessageHandlerBuilderActions.Add(builder => builder.PrimaryHandler = MailgunHandler));
        });
    }

    public async Task ResetChannelStateAsync()
    {
        await using var scope = Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>();
        await db.MessageEvents.ExecuteDeleteAsync(); await db.MessageDeliveries.ExecuteDeleteAsync(); await db.MessageAttachments.ExecuteDeleteAsync(); await db.AttachmentUploads.ExecuteDeleteAsync(); await db.AttachmentCleanupRecords.ExecuteDeleteAsync(); await db.InboundEmailJobs.ExecuteDeleteAsync(); await db.InboundReceipts.ExecuteDeleteAsync(); await db.OutboxJobs.ExecuteDeleteAsync(); await db.IdempotencyRecords.ExecuteDeleteAsync(); await db.ConversationMessages.ExecuteDeleteAsync(); await db.ConversationTags.ExecuteDeleteAsync(); await db.ConversationCustomerCandidates.ExecuteDeleteAsync(); await db.ConversationReadStates.ExecuteDeleteAsync(); await db.Conversations.ExecuteDeleteAsync(); await db.Participants.ExecuteDeleteAsync(); await db.Tags.ExecuteDeleteAsync();
        ObjectStore.Clear();
        CustomerDirectory.FindCustomerFailure = null;
        AttachmentScanner.BeforeResult = null;
        AttachmentScanner.Result = new(AttachmentScanVerdict.Clean);
        AttachmentScanner.ScanStarted = null;
        AttachmentScanner.ContinueScan = null;
        var bootstrap = await db.Channels.SingleAsync(item => item.Address == BootstrapChannelAddress);
        await db.ChannelCredentials.Where(item => item.ChannelId != bootstrap.Id).ExecuteDeleteAsync(); await db.Channels.Where(item => item.Id != bootstrap.Id).ExecuteDeleteAsync();
        await db.ChannelCredentials.Where(item => item.ChannelId == bootstrap.Id).ExecuteDeleteAsync();
        await db.Channels.Where(item => item.Id == bootstrap.Id).ExecuteUpdateAsync(setters => setters.SetProperty(item => item.Address, BootstrapChannelAddress).SetProperty(item => item.DisplayName, "Integration Channel").SetProperty(item => item.Provider, "smtp").SetProperty(item => item.IsActive, true).SetProperty(item => item.IsDefault, true));
    }

    async Task IAsyncLifetime.DisposeAsync() { await base.DisposeAsync(); await postgres.DisposeAsync(); }
}

internal sealed class TestTenantContext(AmbientTenantContext ambient) : ITenantContext
{
    public static TenantId DefaultTenant { get; set; }

    public bool IsResolved => ambient.IsResolved || !DefaultTenant.IsEmpty;

    public TenantId Current => ambient.IsResolved ? ambient.Current :
        !DefaultTenant.IsEmpty ? DefaultTenant : throw new TenantUnresolvedException();
}

internal sealed class StubCustomerDirectory : ICustomerDirectory
{
    public ContactEmailResolution? Resolution { get; set; }
    public string? LastEmail { get; private set; }

    /// <summary>Makes customer lookups fault, so unhandled failures can be exercised end to end.</summary>
    public Exception? FindCustomerFailure { get; set; }

    public Task<CustomerDirectoryEntry?> FindCustomerAsync(int customerId, CancellationToken cancellationToken = default) =>
        FindCustomerFailure is not null
            ? Task.FromException<CustomerDirectoryEntry?>(FindCustomerFailure)
            : Task.FromResult<CustomerDirectoryEntry?>(customerId is 7 or 8 or 9 or 12 or 77 or 123
                ? new CustomerDirectoryEntry(customerId, $"Customer {customerId}")
                : null);

    public Task<ContactDirectoryEntry?> FindContactAsync(int contactId, CancellationToken cancellationToken = default) =>
        Task.FromResult<ContactDirectoryEntry?>(null);

    public Task<ContactEmailResolution?> FindByEmailAsync(string normalizedEmailAddress, CancellationToken cancellationToken = default)
    {
        LastEmail = normalizedEmailAddress;
        return Task.FromResult(Resolution);
    }
}

public sealed record CommunicationsTestUser(Guid Id, string Email, string Password);
public sealed record AntiforgeryToken(string Token);

internal sealed class CapturingEmailSender : IEmailSender
{
    private readonly List<EmailEnvelope> envelopes = [];
    public bool ThrowOnSend { get; set; }
    public IReadOnlyList<EmailEnvelope> Envelopes => envelopes;
    public Task SendAsync(EmailEnvelope envelope, Channel channel, CancellationToken cancellationToken) { if (ThrowOnSend) throw new InvalidOperationException("fake SMTP failure"); envelopes.Add(envelope); return Task.CompletedTask; }
}

internal sealed class InMemoryObjectStore
{
    private readonly Dictionary<string, byte[]> objects = new(StringComparer.Ordinal);
    private readonly object sync = new();

    internal IObjectStore<CommunicationsStorageScope> CommunicationsStore { get; }

    public InMemoryObjectStore() => CommunicationsStore = new TestScopedObjectStore<CommunicationsStorageScope>(this);

    internal async Task PutAsync(string key, Stream content, CancellationToken cancellationToken = default)
    {
        await using var copy = new MemoryStream();
        await content.CopyToAsync(copy, cancellationToken);
        lock (sync) objects[key] = copy.ToArray();
    }

    internal Task<Stream?> GetAsync(string key, CancellationToken cancellationToken = default)
    {
        if (GetFailure is not null) return Task.FromException<Stream?>(GetFailure);
        lock (sync) return Task.FromResult<Stream?>(objects.TryGetValue(key, out var value) ? new MemoryStream(value, writable: false) : null);
    }

    internal Task<bool> ExistsAsync(string key, CancellationToken cancellationToken = default)
    {
        lock (sync) return Task.FromResult(objects.ContainsKey(key));
    }

    internal Task DeleteAsync(string key, CancellationToken cancellationToken = default)
    {
        if (DeleteFailure is not null) return Task.FromException(DeleteFailure);
        lock (sync) objects.Remove(key);
        return Task.CompletedTask;
    }

    internal Exception? DeleteFailure { get; set; }

    internal Exception? GetFailure { get; set; }

    public void Clear()
    {
        lock (sync) objects.Clear();
        DeleteFailure = null;
        GetFailure = null;
    }

    public IReadOnlyCollection<string> PhysicalKeys
    {
        get { lock (sync) return objects.Keys.ToArray(); }
    }

    public bool ContainsPhysicalKey(string key)
    {
        lock (sync) return objects.ContainsKey(key);
    }
}

internal sealed class TestScopedObjectStore<TScope>(InMemoryObjectStore backend) : IObjectStore<TScope>
    where TScope : IStorageScope
{
    private static string PhysicalKey(string key) => $"{TScope.Name}/{key}";
    public Task PutAsync(string key, Stream content, string contentType, CancellationToken cancellationToken = default) => backend.PutAsync(PhysicalKey(key), content, cancellationToken);
    public Task<Stream?> GetAsync(string key, CancellationToken cancellationToken = default) => backend.GetAsync(PhysicalKey(key), cancellationToken);
    public Task<bool> ExistsAsync(string key, CancellationToken cancellationToken = default) => backend.ExistsAsync(PhysicalKey(key), cancellationToken);
    public Task DeleteAsync(string key, CancellationToken cancellationToken = default) => backend.DeleteAsync(PhysicalKey(key), cancellationToken);
}

internal sealed class TestAttachmentScanner : IAttachmentScanner
{
    public bool IsConfigured => true;
    public AttachmentScanResult? BeforeResult { get; set; }
    public AttachmentScanResult Result { get; set; } = new(AttachmentScanVerdict.Clean);
    public TaskCompletionSource<bool>? ScanStarted { get; set; }
    public TaskCompletionSource<bool>? ContinueScan { get; set; }

    public async Task<AttachmentScanResult> ScanAsync(Stream content, long sizeBytes, CancellationToken cancellationToken = default)
    {
        if (BeforeResult is not null) return BeforeResult;
        ScanStarted?.TrySetResult(true);
        if (ContinueScan is not null) await ContinueScan.Task.WaitAsync(cancellationToken);
        await content.CopyToAsync(Stream.Null, cancellationToken);
        return Result;
    }
}

public sealed class FailingMailgunHandler : HttpMessageHandler
{
    protected override Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken) => Task.FromResult(new HttpResponseMessage(System.Net.HttpStatusCode.BadGateway) { ReasonPhrase = "Deterministic test failure", Content = new StringContent("mailgun verification failed in test") });
}

[CollectionDefinition(Name)]
public sealed class CommunicationsModuleCollection : ICollectionFixture<CommunicationsModuleFactory>
{
    public const string Name = "CommunicationsModule";
}