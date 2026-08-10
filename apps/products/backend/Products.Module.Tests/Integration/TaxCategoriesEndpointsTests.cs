using System.Net;
using System.Net.Http.Json;

namespace Vantigo.Products.Module.Tests.Integration;

[Collection(ProductsModuleCollection.Name)]
public sealed class TaxCategoriesEndpointsTests
{
    private readonly HttpClient _client;

    public TaxCategoriesEndpointsTests(ProductsModuleFactory factory) => _client = factory.CreateAuthenticatedClient();

    [Fact]
    public async Task TaxCategoryCrud_CreatesListsUpdatesAndDeletes()
    {
        var name = $"Test tax {Guid.NewGuid():N}";
        var create = await _client.PostAsJsonAsync("/api/v1/products/tax-categories", new
        {
            name,
            kind = "Reduced",
            rate = 0.15m,
        });

        Assert.Equal(HttpStatusCode.Created, create.StatusCode);
        var category = await create.Content.ReadFromJsonAsync<TaxCategory>();
        Assert.NotNull(category);
        Assert.Equal("Reduced", category.Kind);
        Assert.Equal(0.15m, category.Rate);

        var list = await _client.GetFromJsonAsync<List<TaxCategory>>("/api/v1/products/tax-categories");
        Assert.Contains(list!, item => item.Id == category.Id);

        var update = await _client.PutAsJsonAsync($"/api/v1/products/tax-categories/{category.Id}", new
        {
            name,
            kind = "Zero",
            rate = 0m,
        });
        Assert.Equal(HttpStatusCode.OK, update.StatusCode);
        var updated = await update.Content.ReadFromJsonAsync<TaxCategory>();
        Assert.Equal("Zero", updated!.Kind);

        Assert.Equal(HttpStatusCode.NoContent,
            (await _client.DeleteAsync($"/api/v1/products/tax-categories/{category.Id}")).StatusCode);
    }

    [Fact]
    public async Task DeleteTaxCategory_InUse_ReturnsConflict()
    {
        var category = await CreateAsync($"In use {Guid.NewGuid():N}", "Standard", 0.25m);
        var product = await _client.PostAsJsonAsync("/api/v1/products", new
        {
            name = $"Tax product {Guid.NewGuid():N}",
            type = "Goods",
            taxCategoryId = category.Id,
            variants = new[] { new { sku = $"TAX-{Guid.NewGuid():N}"[..24].ToUpperInvariant() } },
        });
        Assert.Equal(HttpStatusCode.Created, product.StatusCode);

        Assert.Equal(HttpStatusCode.Conflict,
            (await _client.DeleteAsync($"/api/v1/products/tax-categories/{category.Id}")).StatusCode);
    }

    [Fact]
    public async Task CreateProduct_WithUnknownTaxCategory_ReturnsFieldError()
    {
        var response = await _client.PostAsJsonAsync("/api/v1/products", new
        {
            name = "Unknown tax category",
            type = "Goods",
            taxCategoryId = 999999,
            variants = new[] { new { sku = $"TAX-{Guid.NewGuid():N}"[..24].ToUpperInvariant() } },
        });

        Assert.Equal(HttpStatusCode.BadRequest, response.StatusCode);
        var problem = await response.Content.ReadFromJsonAsync<ValidationProblem>();
        Assert.Contains("taxCategoryId", problem!.Errors.Keys);
    }

    [Fact]
    public async Task ProductResponse_EmbedsTaxCategoryWithRate()
    {
        var category = await CreateAsync($"Embedded {Guid.NewGuid():N}", "Reduced", 0.15m);
        var response = await _client.PostAsJsonAsync("/api/v1/products", new
        {
            name = $"Embedded tax product {Guid.NewGuid():N}",
            type = "Goods",
            taxCategoryId = category.Id,
            variants = new[] { new { sku = $"TAX-{Guid.NewGuid():N}"[..24].ToUpperInvariant() } },
        });

        Assert.Equal(HttpStatusCode.Created, response.StatusCode);
        var product = await response.Content.ReadFromJsonAsync<Product>();
        Assert.Equal(category.Id, product!.TaxCategory.Id);
        Assert.Equal("Reduced", product.TaxCategory.Kind);
        Assert.Equal(0.15m, product.TaxCategory.Rate);
    }

    private async Task<TaxCategory> CreateAsync(string name, string kind, decimal rate)
    {
        var response = await _client.PostAsJsonAsync("/api/v1/products/tax-categories", new { name, kind, rate });
        Assert.Equal(HttpStatusCode.Created, response.StatusCode);
        return (await response.Content.ReadFromJsonAsync<TaxCategory>())!;
    }

    private sealed record TaxCategory(int Id, string Name, string Kind, decimal Rate);
    private sealed record Product(int Id, TaxCategory TaxCategory);
    private sealed record ValidationProblem(Dictionary<string, string[]> Errors);
}