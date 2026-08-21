using System.Net.Http.Json;

using Microsoft.AspNetCore.Hosting;
using Microsoft.AspNetCore.Identity;
using Microsoft.AspNetCore.Mvc.Testing;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.DependencyInjection.Extensions;
using Microsoft.Extensions.Hosting;

using Testcontainers.PostgreSql;

using Vantigo.Contracts.Identity;
using Vantigo.Host;
using Vantigo.Identity.Database.Accounts;
using Vantigo.Identity.Services;
using Vantigo.Products.Database.Products;
using Vantigo.Products.Domain.Products;
using Vantigo.Tenancy;
using Vantigo.Tenancy.Abstractions;

namespace Vantigo.Products.Module.Tests.Integration;

/// <summary>
/// Boots the Products API against a real PostgreSQL instance running in a
/// Testcontainers-managed Docker container. The container is shared across all
/// tests in the <see cref="ProductsModuleCollection"/> to keep the test run fast.
/// Migrations are applied by the API itself on startup.
/// </summary>
public sealed class ProductsModuleFactory : WebApplicationFactory<global::Program>, IAsyncLifetime
{
    private const string BootstrapSecret = "integration-test-bootstrap-secret";
    private readonly PostgreSqlContainer _postgres = new PostgreSqlBuilder("postgres:17-alpine")
        .WithDatabase("vantigo")
        .Build();

    public Guid DefaultTenantId { get; private set; }

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

        await using var tenantSetupScope = Services.CreateAsyncScope();
        DefaultTenantId = (await tenantSetupScope.ServiceProvider
            .GetRequiredService<TenantDirectory>().GetDefaultTenantAsync()).Value;

        using var tenantScope = EnterDefaultTenant();
        await using var scope = Services.CreateAsyncScope();
        var dbContext = scope.ServiceProvider.GetRequiredService<ProductsDbContext>();
        if (!await dbContext.TaxCategories.AnyAsync())
        {
            dbContext.TaxCategories.Add(new TaxCategory
            {
                Name = "Integration Standard 25%",
                Kind = TaxCategoryKind.Standard,
                Rate = 0.25m,
            });
            await dbContext.SaveChangesAsync();
        }
    }

    public IDisposable EnterDefaultTenant() =>
        AmbientTenantContext.Enter(new TenantId(DefaultTenantId));

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

    public async Task<(Guid Id, string Email, string Password)> CreateUserWithCredentialsAsync(
        string role = AuthRoles.User)
    {
        var email = $"user-{Guid.NewGuid():N}@integration.test";
        const string password = "IntegrationUserPassword123";
        await using var scope = Services.CreateAsyncScope();
        var users = scope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();
        var user = new ApplicationUser
        {
            UserName = email,
            Email = email,
            EmailConfirmed = true,
            DisplayName = $"Test {role}",
        };
        var create = await users.CreateAsync(user, password);
        if (!create.Succeeded || !(await users.AddToRoleAsync(user, role)).Succeeded)
            throw new InvalidOperationException($"Could not create integration {role} user {email}.");

        return (user.Id, email, password);
    }

    public async Task<string> UserConcurrencyStampAsync(Guid userId)
    {
        await using var scope = Services.CreateAsyncScope();
        var users = scope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();
        var user = await users.FindByIdAsync(userId.ToString());
        return user?.ConcurrencyStamp ?? throw new InvalidOperationException($"User {userId} was not found.");
    }

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
        builder.UseSetting("Modules:Customers:Enabled", "false");
        builder.UseSetting("Modules:Communications:Enabled", "false");
        builder.UseSetting("Modules:Products:Enabled", "true");
        builder.UseSetting("Modules:Energy:Enabled", "false");

        builder.ConfigureAppConfiguration((_, configuration) =>
        {
            configuration.AddInMemoryCollection(new Dictionary<string, string?>
            {
                ["ConnectionStrings:vantigo"] = _postgres.GetConnectionString(),
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
public sealed class ProductsModuleCollection : ICollectionFixture<ProductsModuleFactory>
{
    public const string Name = "ProductsModule";
}