using System.Net;
using System.Net.Http.Json;

using Microsoft.Extensions.DependencyInjection;

using Vantigo.Contracts.Authorization;

namespace Vantigo.Products.Module.Tests.Integration;

[Collection(ProductsModuleCollection.Name)]
public sealed class ProductsAuthorizationEndpointsTests(ProductsModuleFactory factory)
{
    private static readonly string[] ProductPermissionKeys =
    [
        "products:products-view",
        "products:products-manage",
        "products:variants-view",
        "products:variants-manage",
        "products:pricing-view",
        "products:pricing-manage",
        "products:categories-view",
        "products:categories-manage",
        "products:tax-categories-view",
        "products:tax-categories-manage",
    ];

    [Fact]
    public void ProductsCatalogContainsEveryEndpointPermission()
    {
        using var scope = factory.Services.CreateScope();
        var catalog = scope.ServiceProvider.GetRequiredService<IPermissionCatalog>();

        Assert.Equal(ProductPermissionKeys.OrderBy(key => key, StringComparer.Ordinal),
            catalog.Permissions.Where(permission => permission.Module == "products")
                .Select(permission => permission.Key)
                .OrderBy(key => key, StringComparer.Ordinal));
    }

    [Fact]
    public async Task ProductsEndpoints_EnforceOwnerNarrowPermissionsViewOnlyMutationAndDisabledUsers()
    {
        using var owner = factory.CreateAuthenticatedClient();
        Assert.Equal(HttpStatusCode.OK, (await owner.GetAsync("/api/v1/products")).StatusCode);
        Assert.Equal(HttpStatusCode.Created,
            (await owner.PostAsJsonAsync("/api/v1/products", NewProduct("Owner product"))).StatusCode);

        var userWithoutPermission = await factory.CreateUserWithCredentialsAsync();
        using var user = await factory.CreateAuthenticatedClientAsync(
            userWithoutPermission.Email, userWithoutPermission.Password);
        Assert.Equal(HttpStatusCode.Forbidden,
            (await user.GetAsync("/api/v1/products")).StatusCode);
        Assert.Equal(HttpStatusCode.Forbidden,
            (await user.PostAsJsonAsync("/api/v1/products", NewProduct("Denied product"))).StatusCode);

        var viewOnlyUser = await factory.CreateUserWithCredentialsAsync();
        var role = await CreateRoleAsync(owner, $"products-view-{Guid.NewGuid():N}", ["products:products-view"]);
        var assignment = await owner.PutAsJsonAsync(
            $"/api/v1/identity/access/users/{viewOnlyUser.Id}/roles",
            new
            {
                roleIds = new[] { role.Id },
                concurrencyStamp = await factory.UserConcurrencyStampAsync(viewOnlyUser.Id),
            });
        Assert.Equal(HttpStatusCode.OK, assignment.StatusCode);

        using var viewOnly = await factory.CreateAuthenticatedClientAsync(
            viewOnlyUser.Email, viewOnlyUser.Password);
        Assert.Equal(HttpStatusCode.Forbidden, (await viewOnly.GetAsync("/api/v1/products")).StatusCode);
        Assert.Equal(HttpStatusCode.Forbidden,
            (await viewOnly.PostAsJsonAsync("/api/v1/products", NewProduct("View only product"))).StatusCode);

        var disable = await owner.PostAsync($"/api/v1/identity/owner/users/{viewOnlyUser.Id}/disable", null);
        Assert.Equal(HttpStatusCode.OK, disable.StatusCode);
        Assert.Equal(HttpStatusCode.Unauthorized,
            (await viewOnly.GetAsync("/api/v1/products")).StatusCode);
    }

    [Fact]
    public async Task AggregateProductEndpointsRequireNestedVariantAndPricingPermissions()
    {
        using var owner = factory.CreateAuthenticatedClient();
        var priced = await owner.PostAsJsonAsync("/api/v1/products", new
        {
            name = $"Aggregate authorization {Guid.NewGuid():N}",
            type = "Goods",
            taxCategoryId = 1001,
            variants = new[]
            {
                new
                {
                    sku = $"TST-AGG-{Guid.NewGuid():N}"[..24].ToUpperInvariant(),
                    standardCost = 42m,
                    prices = new[] { new { currency = "NOK", amount = 99m } },
                },
            },
        });
        Assert.Equal(HttpStatusCode.Created, priced.StatusCode);

        using var productsOnly = await CreateClientWithPermissionsAsync(owner,
            "products:products-view", "products:products-manage");
        Assert.Equal(HttpStatusCode.Forbidden, (await productsOnly.GetAsync("/api/v1/products")).StatusCode);
        Assert.Equal(HttpStatusCode.Forbidden,
            (await productsOnly.PostAsJsonAsync("/api/v1/products", NewProduct("Products only"))).StatusCode);

        using var variantsOnly = await CreateClientWithPermissionsAsync(owner,
            "products:products-view", "products:products-manage",
            "products:variants-view", "products:variants-manage", "products:pricing-view",
            "products:categories-view", "products:tax-categories-view");
        Assert.Equal(HttpStatusCode.OK, (await variantsOnly.GetAsync("/api/v1/products")).StatusCode);
        Assert.Equal(HttpStatusCode.Created,
            (await variantsOnly.PostAsJsonAsync("/api/v1/products", NewProduct("Variants without pricing"))).StatusCode);
        Assert.Equal(HttpStatusCode.Forbidden,
            (await variantsOnly.PostAsJsonAsync("/api/v1/products", NewPricedProduct("Pricing without permission"))).StatusCode);

        using var pricingViewOnly = await CreateClientWithPermissionsAsync(owner,
            "products:products-view", "products:products-manage",
            "products:variants-view", "products:variants-manage", "products:pricing-view",
            "products:categories-view", "products:tax-categories-view");
        Assert.Equal(HttpStatusCode.OK, (await pricingViewOnly.GetAsync("/api/v1/products")).StatusCode);
        Assert.Equal(HttpStatusCode.Forbidden,
            (await pricingViewOnly.PostAsJsonAsync("/api/v1/products", NewPricedProduct("Pricing manage missing"))).StatusCode);

        using var fullAggregate = await CreateClientWithPermissionsAsync(owner,
            "products:products-view", "products:products-manage",
            "products:variants-view", "products:variants-manage",
            "products:pricing-view", "products:pricing-manage",
            "products:categories-view", "products:tax-categories-view");
        var aggregateRead = await fullAggregate.GetAsync("/api/v1/products");
        Assert.Equal(HttpStatusCode.OK, aggregateRead.StatusCode);
        Assert.Equal(HttpStatusCode.Created,
            (await fullAggregate.PostAsJsonAsync("/api/v1/products", NewPricedProduct("Full aggregate access"))).StatusCode);
    }

    [Fact]
    public async Task ProductAggregateReadsRequireCategoryAndTaxCategoryViewPermissions()
    {
        using var owner = factory.CreateAuthenticatedClient();
        using var withoutCategoryView = await CreateClientWithPermissionsAsync(owner,
            "products:products-view", "products:variants-view", "products:pricing-view",
            "products:tax-categories-view");
        Assert.Equal(HttpStatusCode.Forbidden,
            (await withoutCategoryView.GetAsync("/api/v1/products")).StatusCode);

        using var withoutTaxCategoryView = await CreateClientWithPermissionsAsync(owner,
            "products:products-view", "products:variants-view", "products:pricing-view",
            "products:categories-view");
        Assert.Equal(HttpStatusCode.Forbidden,
            (await withoutTaxCategoryView.GetAsync("/api/v1/products")).StatusCode);
    }

    [Fact]
    public async Task CompositeMutationResponsesRequireMatchingViewPermissions()
    {
        using var owner = factory.CreateAuthenticatedClient();

        using var categoryManageOnly = await CreateClientWithPermissionsAsync(owner,
            "products:categories-manage");
        Assert.Equal(HttpStatusCode.Forbidden,
            (await categoryManageOnly.PostAsJsonAsync("/api/v1/products/categories", new { name = "Denied category" })).StatusCode);

        using var taxCategoryManageOnly = await CreateClientWithPermissionsAsync(owner,
            "products:tax-categories-manage");
        Assert.Equal(HttpStatusCode.Forbidden,
            (await taxCategoryManageOnly.PostAsJsonAsync("/api/v1/products/tax-categories", new
            {
                name = "Denied tax category",
                kind = "Standard",
                rate = 0.25m,
            })).StatusCode);

        using var pricingManageOnly = await CreateClientWithPermissionsAsync(owner,
            "products:pricing-manage");
        var product = await CreateProductWithTwoVariantsAsync(owner);
        var variant = product.Variants[0];
        Assert.Equal(HttpStatusCode.Forbidden,
            (await pricingManageOnly.PostAsJsonAsync(
                $"/api/v1/products/{product.Id}/variants/{variant.Id}/prices",
                new { currency = "NOK", amount = 199m })).StatusCode);
    }

    [Fact]
    public async Task VariantDeleteRequiresPricingManageBecauseItCascadesPrices()
    {
        using var owner = factory.CreateAuthenticatedClient();
        var product = await CreateProductWithTwoVariantsAsync(owner);
        var variant = product.Variants[1];

        using var variantsManageOnly = await CreateClientWithPermissionsAsync(owner,
            "products:variants-manage");
        Assert.Equal(HttpStatusCode.Forbidden,
            (await variantsManageOnly.DeleteAsync(
                $"/api/v1/products/{product.Id}/variants/{variant.Id}")).StatusCode);

        using var fullVariantDelete = await CreateClientWithPermissionsAsync(owner,
            "products:variants-manage", "products:pricing-manage");
        Assert.Equal(HttpStatusCode.NoContent,
            (await fullVariantDelete.DeleteAsync(
                $"/api/v1/products/{product.Id}/variants/{variant.Id}")).StatusCode);
    }

    private async Task<HttpClient> CreateClientWithPermissionsAsync(HttpClient owner, params string[] permissions)
    {
        var user = await factory.CreateUserWithCredentialsAsync();
        var role = await CreateRoleAsync(owner, $"products-aggregate-{Guid.NewGuid():N}", permissions);
        var assignment = await owner.PutAsJsonAsync(
            $"/api/v1/identity/access/users/{user.Id}/roles",
            new
            {
                roleIds = new[] { role.Id },
                concurrencyStamp = await factory.UserConcurrencyStampAsync(user.Id),
            });
        Assert.Equal(HttpStatusCode.OK, assignment.StatusCode);
        return await factory.CreateAuthenticatedClientAsync(user.Email, user.Password);
    }

    private static async Task<ProductWithVariants> CreateProductWithTwoVariantsAsync(HttpClient owner)
    {
        var response = await owner.PostAsJsonAsync("/api/v1/products", new
        {
            name = $"Delete cascade {Guid.NewGuid():N}",
            type = "Goods",
            taxCategoryId = 1001,
            variants = new[]
            {
                new { sku = $"TST-DELETE-A-{Guid.NewGuid():N}"[..24].ToUpperInvariant() },
                new { sku = $"TST-DELETE-B-{Guid.NewGuid():N}"[..24].ToUpperInvariant() },
            },
        });
        Assert.Equal(HttpStatusCode.Created, response.StatusCode);
        return (await response.Content.ReadFromJsonAsync<ProductWithVariants>())!;
    }

    private static async Task<RoleResponse> CreateRoleAsync(HttpClient owner, string name, string[] permissionKeys)
    {
        var response = await owner.PostAsJsonAsync("/api/v1/identity/access/roles", new
        {
            name,
            displayName = name,
            description = "Products authorization integration role",
            permissionKeys,
        });
        Assert.Equal(HttpStatusCode.Created, response.StatusCode);
        return (await response.Content.ReadFromJsonAsync<RoleResponse>())!;
    }

    private static object NewProduct(string name) => new
    {
        name,
        type = "Goods",
        taxCategoryId = 1001,
        variants = new[] { new { sku = $"TST-AUTH-{Guid.NewGuid():N}"[..24].ToUpperInvariant() } },
    };

    private static object NewPricedProduct(string name) => new
    {
        name,
        type = "Goods",
        taxCategoryId = 1001,
        variants = new[]
        {
            new
            {
                sku = $"TST-AUTH-PRICE-{Guid.NewGuid():N}"[..24].ToUpperInvariant(),
                standardCost = 42m,
                prices = new[] { new { currency = "NOK", amount = 99m } },
            },
        },
    };

    private sealed record RoleResponse(Guid Id, string Name, string Version, IReadOnlyCollection<string> Permissions);
    private sealed record ProductWithVariants(int Id, List<Variant> Variants);
    private sealed record Variant(int Id, string Sku);
}