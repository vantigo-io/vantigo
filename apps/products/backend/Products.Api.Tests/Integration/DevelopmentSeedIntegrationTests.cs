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

using Vantigo.Products.Api;
using Vantigo.Products.Api.Database.Accounts;
using Vantigo.Products.Api.Database.Products;
using Vantigo.Products.Api.Domain.Products;
using Vantigo.Products.Api.Services;

namespace Vantigo.Products.Api.Tests.Integration;

public sealed class DevelopmentSeedIntegrationTests
{
    private const string AdminEmail = "admin@vantigo.local";
    private const string AdminPassword = "admin";
    private const string AdminDisplayName = "Administrator";

    private static readonly Guid AdminId = new("7f4d1e5b-8a62-4b7e-9c13-2d5f6a708194");
    private static readonly Guid OwnerRoleId = new("8e5c2f6c-9b73-4c8f-ad24-3e607b8192a5");
    private static readonly Guid UserRoleId = new("9f6d307d-ac84-4d90-be35-4f718c92a3b6");

    private static readonly string[] ExpectedSkus =
    [
        "AUR-001",
        "AUR-001-10PK",
        "FJD-100",
        "MDW-2025",
        "SRV-CONSULT",
        "SRV-INSTALL",
    ];

    [Fact]
    public async Task DefaultDevelopmentStartupSeedsFixturesOnceAndConsumesBootstrap()
    {
        await using var postgres = new PostgreSqlBuilder("postgres:17-alpine")
            .WithDatabase("products_development_seed")
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

    private static async Task AssertSeededStateAsync(IServiceProvider services)
    {
        await using var scope = services.CreateAsyncScope();
        var accounts = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        var users = scope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();
        var roles = scope.ServiceProvider.GetRequiredService<RoleManager<IdentityRole<Guid>>>();
        var products = scope.ServiceProvider.GetRequiredService<ProductsDbContext>();

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

        var skus = await products.Products.Select(product => product.Sku).ToListAsync();
        Assert.Equal(ExpectedSkus, skus.Order().ToArray());

        // The seeded catalog covers both product types and all lifecycle statuses,
        // and the campaign fixture proves multiple price rows per currency coexist.
        Assert.True(await products.Products.AnyAsync(product => product.Type == ProductType.Goods));
        Assert.True(await products.Products.AnyAsync(product => product.Type == ProductType.Service));
        Assert.True(await products.Products.AnyAsync(product => product.Status == ProductStatus.Draft));
        Assert.True(await products.Products.AnyAsync(product => product.Status == ProductStatus.Active));
        Assert.True(await products.Products.AnyAsync(product => product.Status == ProductStatus.Discontinued));

        var auroraPrices = await products.ProductPrices
            .Where(price => products.Products
                .Any(product => product.Id == price.ProductId && product.Sku == "AUR-001"))
            .ToListAsync();
        Assert.Equal(3, auroraPrices.Count);
        Assert.Contains(auroraPrices, price => price.Currency == "SEK");
        Assert.Contains(auroraPrices, price => price.Currency == "NOK" && price.ValidFrom != null);

        // The seeded category tree has two roots (Furniture, Services) with
        // Desks/Lighting as subcategories, and every product is categorised.
        var categories = await products.ProductCategories.ToListAsync();
        Assert.Equal(4, categories.Count);
        var furniture = Assert.Single(categories, category => category.Name == "Furniture");
        Assert.Null(furniture.ParentId);
        Assert.Single(categories, category => category.Name == "Desks" && category.ParentId == furniture.Id);
        Assert.Single(categories, category => category.Name == "Lighting" && category.ParentId == furniture.Id);
        Assert.Single(categories, category => category.Name == "Services" && category.ParentId == null);
        Assert.False(await products.Products.AnyAsync(product => product.CategoryId == null));

        // Physical goods carry barcodes and weights; services do not.
        Assert.False(await products.Products.AnyAsync(
            product => product.Type == ProductType.Goods && product.Barcode == null));
        Assert.False(await products.Products.AnyAsync(
            product => product.Type == ProductType.Service && product.Barcode != null));
    }

    private sealed class DevelopmentSeedApiFactory(string connectionString) : WebApplicationFactory<Program>
    {
        protected override void ConfigureWebHost(IWebHostBuilder builder)
        {
            builder.UseEnvironment(Environments.Development);
            builder.ConfigureServices(services =>
                services.AddSingleton(new ProductApiTestStartupPreparation(
                    ApplyMigrations: true,
                    SeedDevelopmentData: true)));
            builder.ConfigureAppConfiguration((_, configuration) =>
                configuration.AddInMemoryCollection(new Dictionary<string, string?>
                {
                    ["ConnectionStrings:Postgresql"] = connectionString,
                }));
        }
    }

    private sealed record AntiforgeryToken(string Token);

    private sealed record BootstrapStatus(bool Available);
}