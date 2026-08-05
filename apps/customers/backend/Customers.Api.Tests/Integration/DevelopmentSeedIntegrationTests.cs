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

using Vantigo.Customers.Api;
using Vantigo.Customers.Api.Database.Accounts;
using Vantigo.Customers.Api.Database.Customers;
using Vantigo.Customers.Api.Services;

namespace Vantigo.Customers.Api.Tests.Integration;

public sealed class DevelopmentSeedIntegrationTests
{
    private const string AdminEmail = "admin@vantigo.local";
    private const string AdminPassword = "admin";
    private const string AdminDisplayName = "Administrator";

    private static readonly Guid AdminId = new("7f4d1e5b-8a62-4b7e-9c13-2d5f6a708194");
    private static readonly Guid OwnerRoleId = new("8e5c2f6c-9b73-4c8f-ad24-3e607b8192a5");
    private static readonly Guid UserRoleId = new("9f6d307d-ac84-4d90-be35-4f718c92a3b6");

    [Fact]
    public async Task DefaultDevelopmentStartupSeedsFixturesOnceAndConsumesBootstrap()
    {
        await using var postgres = new PostgreSqlBuilder("postgres:17-alpine")
            .WithDatabase("customers_development_seed")
            .Build();
        await postgres.StartAsync();

        await using var first = new DevelopmentSeedApiFactory(postgres.GetConnectionString());
        using var firstClient = first.CreateClient(new WebApplicationFactoryClientOptions { HandleCookies = true });

        var configuration = first.Services.GetRequiredService<IConfiguration>();
        Assert.Equal("Development", first.Services.GetRequiredService<IHostEnvironment>().EnvironmentName);
        Assert.True(configuration.GetValue<bool>("Development:Seed:Enabled"));
        Assert.Equal(AdminEmail, configuration["Development:Seed:Admin:Email"]);
        Assert.Equal(AdminDisplayName, configuration["Development:Seed:Admin:DisplayName"]);
        Assert.Equal(AdminPassword, configuration["Development:Seed:Admin:Password"]);

        await AssertSeededStateAsync(first.Services);

        var bootstrapStatus = await firstClient.GetFromJsonAsync<BootstrapStatus>("/auth/bootstrap-status");
        Assert.NotNull(bootstrapStatus);
        Assert.False(bootstrapStatus.Available);

        var antiforgery = await firstClient.GetFromJsonAsync<AntiforgeryToken>("/auth/antiforgery");
        Assert.NotNull(antiforgery);
        firstClient.DefaultRequestHeaders.Add("X-XSRF-TOKEN", antiforgery.Token);
        var bootstrap = await firstClient.PostAsJsonAsync("/auth/bootstrap", new
        {
            secret = first.Services.GetRequiredService<BootstrapSecretProvider>().Secret,
            email = "second-admin@vantigo.local",
            displayName = "Second Owner",
            password = "AnotherOwnerPassword123",
        });
        Assert.Equal(HttpStatusCode.Conflict, bootstrap.StatusCode);

        await using var second = new DevelopmentSeedApiFactory(postgres.GetConnectionString());
        using var secondClient = second.CreateClient();

        // A separate WebApplicationFactory is pointed at the same database. The
        // exact fixture counts and fixed identity values prove startup is idempotent.
        await AssertSeededStateAsync(second.Services);
    }

    [Fact]
    public async Task ConfiguredDevelopmentStartupSeedsRequestedFixturesAndIsIdempotent()
    {
        await using var postgres = new PostgreSqlBuilder("postgres:17-alpine")
            .WithDatabase("customers_development_seed_configured")
            .Build();
        await postgres.StartAsync();

        await using var first = new DevelopmentSeedApiFactory(postgres.GetConnectionString(), 3, 2);
        _ = first.CreateClient();

        var expectedAssociations = new[]
        {
            "990000001|astrid@seed.vantigo.local|Managing Director",
            "990000003|astrid@seed.vantigo.local|Managing Director",
            "990000002|bjorn@seed.vantigo.local|Finance Lead",
        };
        await AssertSeededStateAsync(first.Services, 3, 2, expectedAssociations);

        await using var second = new DevelopmentSeedApiFactory(postgres.GetConnectionString(), 3, 2);
        _ = second.CreateClient();
        await AssertSeededStateAsync(second.Services, 3, 2, expectedAssociations);
    }

    [Fact]
    public async Task IncreasingCustomerCountConvergesExistingContactsToCanonicalGraph()
    {
        await using var postgres = new PostgreSqlBuilder("postgres:17-alpine")
            .WithDatabase("customers_development_seed_history")
            .Build();
        await postgres.StartAsync();

        await using var initial = new DevelopmentSeedApiFactory(postgres.GetConnectionString(), 1, 8);
        _ = initial.CreateClient();
        var existingAssociations = new[]
        {
            "990000001|astrid@seed.vantigo.local|Managing Director",
            "990000001|frank@seed.vantigo.local|Board Chair",
        };
        await AssertSeededStateAsync(initial.Services, 1, 8, existingAssociations);

        await using var expanded = new DevelopmentSeedApiFactory(postgres.GetConnectionString(), 8, 8);
        _ = expanded.CreateClient();
        await AssertSeededStateAsync(expanded.Services, 8, 8, new[]
        {
            "990000001|astrid@seed.vantigo.local|Managing Director",
            "990000001|frank@seed.vantigo.local|Board Chair",
            "990000002|bjorn@seed.vantigo.local|Finance Lead",
            "990000002|david@seed.vantigo.local|Technical Director",
            "990000003|astrid@seed.vantigo.local|Managing Director",
            "990000003|clara@seed.vantigo.local|Operations Manager",
            "990000004|david@seed.vantigo.local|Technical Director",
            "990000004|elin@seed.vantigo.local|Office Manager",
            "990000005|elin@seed.vantigo.local|Office Manager",
            "990000006|frank@seed.vantigo.local|Board Chair",
            "990000007|synthetic-contact-7@seed.vantigo.local|Development Contact",
            "990000008|synthetic-contact-8@seed.vantigo.local|Development Contact",
        });
    }

    private static async Task AssertSeededStateAsync(
        IServiceProvider services,
        int customerCount = 6,
        int contactCount = 6,
        IReadOnlyCollection<string>? expectedAssociations = null)
    {
        await using var scope = services.CreateAsyncScope();
        var accounts = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        var users = scope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();
        var roles = scope.ServiceProvider.GetRequiredService<RoleManager<IdentityRole<Guid>>>();
        var customers = scope.ServiceProvider.GetRequiredService<CustomersDbContext>();

        var admin = await users.FindByEmailAsync(AdminEmail);
        Assert.NotNull(admin);
        Assert.Equal(AdminId, admin.Id);
        Assert.Equal(AdminDisplayName, admin.DisplayName);
        Assert.True(admin.EmailConfirmed);
        Assert.True(await users.CheckPasswordAsync(admin, AdminPassword));
        Assert.Equal(new[] { "Owner", "User" }, (await users.GetRolesAsync(admin)).Order());

        Assert.Equal(1, await accounts.Users.CountAsync());
        Assert.Equal(2, await accounts.Roles.CountAsync());
        Assert.Equal(2, await accounts.UserRoles.CountAsync());
        Assert.Equal(2, await roles.Roles.CountAsync());

        var roleIds = await accounts.Roles
            .ToDictionaryAsync(role => role.Name!, role => role.Id);
        Assert.Equal(OwnerRoleId, roleIds["Owner"]);
        Assert.Equal(UserRoleId, roleIds["User"]);

        Assert.Equal(1, await accounts.BootstrapStates.CountAsync());
        Assert.Equal(customerCount, await customers.Customers.CountAsync());
        Assert.Equal(contactCount, await customers.Contacts.CountAsync());
        Assert.Equal(expectedAssociations?.Count ?? 10, await customers.CustomersContacts.CountAsync());

        var customerLegalIds = await customers.Customers
            .Where(customer => customer.Identity != null)
            .Select(customer => (string)customer.Identity!.Value.Id)
            .ToListAsync();
        Assert.Equal(
            Enumerable.Range(1, customerCount)
                .Select(number => (990000000 + number).ToString())
                .ToArray(),
            customerLegalIds.Order());

        var contactEmails = await customers.Contacts
            .Where(contact => contact.Email != null)
            .Select(contact => (string)contact.Email!.Value)
            .ToListAsync();
        Assert.Equal(
            new[] { "astrid", "bjorn", "clara", "david", "elin", "frank" }
                .Take(contactCount)
                .Select(key => $"{key}@seed.vantigo.local")
                .Concat(Enumerable.Range(7, Math.Max(0, contactCount - 6))
                    .Select(number => $"synthetic-contact-{number}@seed.vantigo.local"))
                .ToArray(),
            contactEmails.Order());

        var associations = await customers.CustomersContacts
            .Select(association => new
            {
                CustomerLegalId = (string)association.Customer.Identity!.Value.Id,
                ContactEmail = (string)association.Contact.Email!.Value,
                Role = (string)association.Role,
            })
            .ToListAsync();
        Assert.Equal(
            (expectedAssociations ??
            [
                "990000001|astrid@seed.vantigo.local|Managing Director",
                "990000001|frank@seed.vantigo.local|Board Chair",
                "990000002|bjorn@seed.vantigo.local|Finance Lead",
                "990000002|david@seed.vantigo.local|Technical Director",
                "990000003|astrid@seed.vantigo.local|Managing Director",
                "990000003|clara@seed.vantigo.local|Operations Manager",
                "990000004|david@seed.vantigo.local|Technical Director",
                "990000004|elin@seed.vantigo.local|Office Manager",
                "990000005|elin@seed.vantigo.local|Office Manager",
                "990000006|frank@seed.vantigo.local|Board Chair",
            ]).Order(),
            associations
                .Select(association => $"{association.CustomerLegalId}|{association.ContactEmail}|{association.Role}")
                .Order());
    }

    private sealed class DevelopmentSeedApiFactory(
        string connectionString,
        int? customerCount = null,
        int? contactCount = null) : WebApplicationFactory<Program>
    {
        protected override void ConfigureWebHost(IWebHostBuilder builder)
        {
            builder.UseEnvironment(Environments.Development);
            builder.ConfigureServices(services =>
                services.AddSingleton(new CustomerApiTestStartupPreparation(
                    ApplyMigrations: true,
                    SeedDevelopmentData: true)));
            builder.ConfigureAppConfiguration((_, configuration) =>
            {
                var values = new Dictionary<string, string?>
                {
                    ["ConnectionStrings:Postgresql"] = connectionString,
                };
                if (customerCount is not null)
                {
                    values["Development:Seed:Data:Customers"] = customerCount.Value.ToString();
                }
                if (contactCount is not null)
                {
                    values["Development:Seed:Data:Contacts"] = contactCount.Value.ToString();
                }
                configuration.AddInMemoryCollection(values);
            });
        }
    }

    private sealed record AntiforgeryToken(string Token);

    private sealed record BootstrapStatus(bool Available);
}