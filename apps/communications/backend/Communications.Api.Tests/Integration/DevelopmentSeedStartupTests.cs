using System.Net;
using System.Net.Http.Json;

using Microsoft.AspNetCore.Hosting;
using Microsoft.AspNetCore.Identity;
using Microsoft.AspNetCore.Mvc.Testing;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;

using Testcontainers.PostgreSql;

using Vantigo.Communications.Api;
using Vantigo.Communications.Api.Database.Accounts;
using Vantigo.Communications.Api.Database.Communications;

namespace Vantigo.Communications.Api.Tests.Integration;

public sealed class DevelopmentSeedStartupTests : IAsyncLifetime
{
    private static readonly Guid DevelopmentOwnerId = Guid.Parse("0f4d7d7a-9f0b-4b0d-8e0a-000000000001");
    private static readonly Guid DevelopmentOwnerRoleId = Guid.Parse("0f4d7d7a-9f0b-4b0d-8e0a-000000000002");
    private static readonly Guid DevelopmentUserRoleId = Guid.Parse("0f4d7d7a-9f0b-4b0d-8e0a-000000000003");
    private static readonly Guid DevelopmentMailboxId = Guid.Parse("5c6e5f11-6b95-4b7d-8c4f-000000000001");

    private readonly PostgreSqlContainer postgres = new PostgreSqlBuilder("postgres:17-alpine")
        .WithDatabase("communications_development_seed")
        .Build();
    private readonly PostgreSqlContainer configurablePostgres = new PostgreSqlBuilder("postgres:17-alpine")
        .WithDatabase("communications_development_seed_volume")
        .Build();

    public async Task InitializeAsync()
    {
        await postgres.StartAsync();
        await configurablePostgres.StartAsync();
    }

    public async Task DisposeAsync()
    {
        await postgres.DisposeAsync();
        await configurablePostgres.DisposeAsync();
    }

    [Fact]
    public async Task Development_startup_seed_is_observable_and_idempotent()
    {
        SeedCounts firstCounts;
        using (var first = CreateFactory(postgres.GetConnectionString(), clearMessageCount: true))
        {
            using var client = first.CreateClient();
            var bootstrapStatus = await client.GetFromJsonAsync<BootstrapStatus>("/auth/bootstrap-status");

            Assert.NotNull(bootstrapStatus);
            Assert.False(bootstrapStatus.Available);

            await AssertSeededIdentityAsync(first.Services);
            await AssertSeededCommunicationsAsync(first.Services);
            firstCounts = await ReadCountsAsync(first.Services);
            Assert.Equal(
                new SeedCounts(
                    Users: 1,
                    Roles: 2,
                    UserRoles: 2,
                    BootstrapStates: 1,
                    Mailboxes: 1,
                    Messages: 2,
                    Deliveries: 3,
                    Events: 5,
                    ExternalLinks: 0,
                    OutboxJobs: 0,
                    IdempotencyRecords: 0),
                firstCounts);
        }

        using (var second = CreateFactory(postgres.GetConnectionString(), clearMessageCount: true))
        {
            using var client = second.CreateClient();
            var bootstrapStatus = await client.GetFromJsonAsync<BootstrapStatus>("/auth/bootstrap-status");

            Assert.NotNull(bootstrapStatus);
            Assert.False(bootstrapStatus.Available);
            Assert.Equal(firstCounts, await ReadCountsAsync(second.Services));
        }
    }

    [Fact]
    public async Task Development_seed_volume_is_deterministic_and_idempotent()
    {
        SeedCounts firstCounts;
        using (var first = CreateFactory(configurablePostgres.GetConnectionString(), 3))
        {
            using var client = first.CreateClient();
            var bootstrapStatus = await client.GetFromJsonAsync<BootstrapStatus>("/auth/bootstrap-status");

            Assert.NotNull(bootstrapStatus);
            Assert.False(bootstrapStatus.Available);
            await AssertSeededIdentityAsync(first.Services);
            await AssertDevelopmentMailboxAsync(first.Services);

            firstCounts = await ReadCountsAsync(first.Services);
            Assert.Equal(
                new SeedCounts(
                    Users: 1,
                    Roles: 2,
                    UserRoles: 2,
                    BootstrapStates: 1,
                    Mailboxes: 1,
                    Messages: 3,
                    Deliveries: 4,
                    Events: 7,
                    ExternalLinks: 0,
                    OutboxJobs: 0,
                    IdempotencyRecords: 0),
                firstCounts);
        }

        using (var second = CreateFactory(configurablePostgres.GetConnectionString(), 3))
        {
            using var client = second.CreateClient();
            var bootstrapStatus = await client.GetFromJsonAsync<BootstrapStatus>("/auth/bootstrap-status");

            Assert.NotNull(bootstrapStatus);
            Assert.False(bootstrapStatus.Available);
            Assert.Equal(firstCounts, await ReadCountsAsync(second.Services));
        }
    }

    private DevelopmentSeedApiFactory CreateFactory(
        string connectionString,
        int? messageCount = null,
        bool clearMessageCount = false) =>
        new(connectionString, messageCount, clearMessageCount);

    private static async Task AssertSeededIdentityAsync(IServiceProvider services)
    {
        await using var scope = services.CreateAsyncScope();
        var userManager = scope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();
        var roleManager = scope.ServiceProvider.GetRequiredService<RoleManager<IdentityRole<Guid>>>();
        var accounts = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();

        var user = await userManager.FindByEmailAsync("admin@vantigo.local");
        Assert.NotNull(user);
        Assert.Equal(DevelopmentOwnerId, user.Id);
        Assert.Equal("Administrator", user.DisplayName);
        Assert.True(await userManager.CheckPasswordAsync(user, "admin"));
        Assert.Equal(
            ["Owner", "User"],
            (await userManager.GetRolesAsync(user)).OrderBy(role => role).ToArray());

        var ownerRole = await roleManager.FindByNameAsync("Owner");
        var userRole = await roleManager.FindByNameAsync("User");
        Assert.NotNull(ownerRole);
        Assert.NotNull(userRole);
        Assert.Equal(DevelopmentOwnerRoleId, ownerRole.Id);
        Assert.Equal(DevelopmentUserRoleId, userRole.Id);

        var bootstrapState = await accounts.BootstrapStates.SingleAsync();
        Assert.Equal(new DateTimeOffset(2026, 1, 5, 9, 0, 0, TimeSpan.Zero), bootstrapState.CompletedAt);
    }

    private static async Task AssertSeededCommunicationsAsync(IServiceProvider services)
    {
        await AssertDevelopmentMailboxAsync(services);

        await using var scope = services.CreateAsyncScope();
        var communications = scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>();

        var messages = await communications.EmailMessages.OrderBy(message => message.Id).ToListAsync();
        Assert.Equal(
            [
                Guid.Parse("10000000-0000-0000-0000-000000000001"),
                Guid.Parse("10000000-0000-0000-0000-000000000002"),
            ],
            messages.Select(message => message.Id).ToArray());
        Assert.All(messages, message =>
        {
            Assert.Equal(DevelopmentMailboxId, message.MailboxId);
            Assert.Equal("development-seed", message.Source);
        });

        var deliveries = await communications.RecipientDeliveries.OrderBy(delivery => delivery.Id).ToListAsync();
        Assert.Equal(
            [
                Guid.Parse("20000000-0000-0000-0000-000000000001"),
                Guid.Parse("20000000-0000-0000-0000-000000000002"),
                Guid.Parse("20000000-0000-0000-0000-000000000003"),
            ],
            deliveries.Select(delivery => delivery.Id).ToArray());
        Assert.All(deliveries, delivery => Assert.Equal("relay_accepted", delivery.Status));

        var events = await communications.MessageEvents.OrderBy(messageEvent => messageEvent.Id).ToListAsync();
        Assert.Equal(
            [
                Guid.Parse("30000000-0000-0000-0000-000000000001"),
                Guid.Parse("30000000-0000-0000-0000-000000000002"),
                Guid.Parse("30000000-0000-0000-0000-000000000003"),
                Guid.Parse("30000000-0000-0000-0000-000000000004"),
                Guid.Parse("30000000-0000-0000-0000-000000000005"),
            ],
            events.Select(messageEvent => messageEvent.Id).ToArray());
        Assert.All(events, messageEvent => Assert.True(
            messageEvent.EventType is "message_queued" or "relay_accepted"));
        Assert.Equal(0, await communications.ExternalEntityLinks.CountAsync());
        Assert.Equal(0, await communications.OutboxJobs.CountAsync());
    }

    private static async Task AssertDevelopmentMailboxAsync(IServiceProvider services)
    {
        await using var scope = services.CreateAsyncScope();
        var communications = scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>();

        var mailbox = await communications.SharedMailboxes.SingleAsync();
        Assert.Equal(DevelopmentMailboxId, mailbox.Id);
        Assert.Equal("dev-mailbox@vantigo.local", mailbox.FromAddress);
        Assert.Equal("Development Mailbox", mailbox.DisplayName);
        Assert.False(mailbox.IsActive);
    }

    private static async Task<SeedCounts> ReadCountsAsync(IServiceProvider services)
    {
        await using var scope = services.CreateAsyncScope();
        var accounts = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        var communications = scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>();
        return new SeedCounts(
            await accounts.Users.CountAsync(),
            await accounts.Roles.CountAsync(),
            await accounts.UserRoles.CountAsync(),
            await accounts.BootstrapStates.CountAsync(),
            await communications.SharedMailboxes.CountAsync(),
            await communications.EmailMessages.CountAsync(),
            await communications.RecipientDeliveries.CountAsync(),
            await communications.MessageEvents.CountAsync(),
            await communications.ExternalEntityLinks.CountAsync(),
            await communications.OutboxJobs.CountAsync(),
            await communications.IdempotencyRecords.CountAsync());
    }

    private sealed class DevelopmentSeedApiFactory(
        string connectionString,
        int? messageCount,
        bool clearMessageCount) : WebApplicationFactory<Program>
    {
        protected override void ConfigureWebHost(IWebHostBuilder builder)
        {
            builder.UseEnvironment(Environments.Development);
            builder.ConfigureAppConfiguration((_, configuration) =>
            {
                var settings = new Dictionary<string, string?>
                {
                    ["ConnectionStrings:Postgresql"] = connectionString,
                    ["Authentication:Bootstrap:Secret"] = "development-seed-test-secret",
                    ["Communications:BootstrapMailbox:Enabled"] = "false",
                    ["Outbox:PollSeconds"] = "3600",
                    ["Communications:Retention:PollMinutes"] = "3600",
                };
                if (clearMessageCount)
                {
                    settings["Development:Seed:Data:Messages"] = null;
                }
                else if (messageCount is int count)
                {
                    settings["Development:Seed:Data:Messages"] = count.ToString();
                }

                configuration.AddInMemoryCollection(settings);
            });
            builder.ConfigureServices(services =>
                services.AddSingleton<CommunicationsTestStartupPreparationMarker>());
        }
    }

    private sealed record BootstrapStatus(bool Available);

    private sealed record SeedCounts(
        int Users,
        int Roles,
        int UserRoles,
        int BootstrapStates,
        int Mailboxes,
        int Messages,
        int Deliveries,
        int Events,
        int ExternalLinks,
        int OutboxJobs,
        int IdempotencyRecords);
}