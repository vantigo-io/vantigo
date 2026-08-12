using System.Net;
using System.Net.Http.Json;

using Microsoft.AspNetCore.Builder;
using Microsoft.AspNetCore.Hosting;
using Microsoft.AspNetCore.Mvc.Testing;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.DependencyInjection.Extensions;
using Microsoft.Extensions.Hosting;

using Testcontainers.PostgreSql;

using Vantigo.Contracts.Authorization;
using Vantigo.Host;
using Vantigo.Identity.Authorization;
using Vantigo.Identity.Database.Accounts;
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
    private static int nextClientAddress;
    private const string BootstrapSecret = "integration-test-bootstrap-secret";
    private readonly PostgreSqlContainer _postgres = new PostgreSqlBuilder("postgres:17-alpine")
        .WithDatabase("vantigo")
        .Build();

    public StubBrregHandler BrregHandler { get; } = new();

    internal CapturingEmailSender EmailSender { get; } = new();

    public async Task InitializeAsync()
    {
        await _postgres.StartAsync();

        using var client = CreateClientWithTestAddress(new WebApplicationFactoryClientOptions { HandleCookies = true });
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

    public HttpClient CreateClientWithTestAddress(WebApplicationFactoryClientOptions options)
    {
        var client = base.CreateClient(options);
        var octet = Interlocked.Increment(ref nextClientAddress) % 250 + 1;
        client.DefaultRequestHeaders.TryAddWithoutValidation("X-Test-Client-Address", $"10.0.0.{octet}");
        return client;
    }

    public HttpClient CreateAuthenticatedClient()
    {
        var client = CreateClientWithTestAddress(new WebApplicationFactoryClientOptions { HandleCookies = true });
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

    public async Task<HttpClient> CreateUserClientAsync(string role, IEnumerable<string>? permissions = null)
    {
        var email = $"{role.ToLowerInvariant()}-{Guid.NewGuid():N}@integration.test";
        const string password = "IntegrationPassword123";
        await using (var scope = Services.CreateAsyncScope())
        {
            var users = scope.ServiceProvider.GetRequiredService<Microsoft.AspNetCore.Identity.UserManager<ApplicationUser>>();
            var roleManager = scope.ServiceProvider.GetRequiredService<Microsoft.AspNetCore.Identity.RoleManager<Microsoft.AspNetCore.Identity.IdentityRole<Guid>>>();
            var roleName = role == "User" ? "User" : $"Test-{role}-{Guid.NewGuid():N}";
            if (await roleManager.FindByNameAsync(roleName) is null)
            {
                Assert.True((await roleManager.CreateAsync(new Microsoft.AspNetCore.Identity.IdentityRole<Guid>(roleName))).Succeeded);
            }
            var user = new ApplicationUser { UserName = email, Email = email, EmailConfirmed = true, DisplayName = role };
            Assert.True((await users.CreateAsync(user, password)).Succeeded);
            Assert.True((await users.AddToRoleAsync(user, roleName)).Succeeded);
            if (permissions is not null)
            {
                var db = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
                var roleEntity = await db.Roles.SingleAsync(item => item.Name == roleName);
                foreach (var permission in permissions)
                {
                    db.RolePermissions.Add(new RolePermission { RoleId = roleEntity.Id, PermissionKey = permission });
                }
                await db.SaveChangesAsync();
            }
        }

        var client = CreateClientWithTestAddress(new WebApplicationFactoryClientOptions { HandleCookies = true });
        var token = await client.GetFromJsonAsync<AntiforgeryToken>("/api/v1/identity/antiforgery");
        client.DefaultRequestHeaders.Add("X-XSRF-TOKEN", token!.Token);
        var login = await client.PostAsJsonAsync("/api/v1/identity/login", new { email, password });
        Assert.Equal(System.Net.HttpStatusCode.OK, login.StatusCode);
        client.DefaultRequestHeaders.Remove("X-XSRF-TOKEN");
        token = await client.GetFromJsonAsync<AntiforgeryToken>("/api/v1/identity/antiforgery");
        client.DefaultRequestHeaders.Add("X-XSRF-TOKEN", token!.Token);
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
            services.AddTransient<IStartupFilter, TestClientAddressStartupFilter>();
        });
    }

    async Task IAsyncLifetime.DisposeAsync()
    {
        await base.DisposeAsync();
        await _postgres.DisposeAsync();
    }
}

internal sealed record AntiforgeryToken(string Token);

internal sealed class TestClientAddressStartupFilter : IStartupFilter
{
    public Action<IApplicationBuilder> Configure(Action<IApplicationBuilder> next) => app =>
    {
        app.Use(async (context, continuation) =>
        {
            var address = context.Request.Headers["X-Test-Client-Address"].FirstOrDefault();
            var remoteAddress = IPAddress.TryParse(address, out var parsed)
                ? parsed
                : IPAddress.Parse("10.0.0.1");

            context.Connection.RemoteIpAddress = remoteAddress;
            await continuation(context);
        });
        next(app);
    };
}

[CollectionDefinition(Name)]
public sealed class CustomersApiCollection : ICollectionFixture<CustomersApiFactory>
{
    public const string Name = "CustomersApi";
}