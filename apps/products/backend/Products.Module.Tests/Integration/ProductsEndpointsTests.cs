using System.Net;
using System.Net.Http.Json;

namespace Vantigo.Products.Module.Tests.Integration;

[Collection(ProductsModuleCollection.Name)]
public sealed class ProductsEndpointsTests
{
    private readonly HttpClient _client;

    public ProductsEndpointsTests(ProductsModuleFactory factory) => _client = factory.CreateAuthenticatedClient();

    [Fact]
    public async Task CreateProduct_WithRequiredVariant_ReturnsFlattenedSingleVariant()
    {
        var response = await _client.PostAsJsonAsync("/api/v1/products", NewProduct("Nordlys Lantern", Sku("create")));

        Assert.Equal(HttpStatusCode.Created, response.StatusCode);
        var created = await response.Content.ReadFromJsonAsync<Product>();
        Assert.NotNull(created);
        Assert.True(created.Id > 0);
        Assert.Equal("Draft", created.Status);
        Assert.Equal(created.Sku, created.Variants.Single().Sku);
        Assert.Equal("pcs", created.Unit);
        Assert.Equal($"/api/v1/products/{created.Id}", response.Headers.Location?.AbsolutePath);
    }

    [Fact]
    public async Task CreateProduct_WithoutVariants_ReturnsValidationError()
    {
        var response = await _client.PostAsJsonAsync("/api/v1/products", new
        {
            name = "Missing Variant",
            type = "Goods",
            taxCategoryId = 1001,
        });

        Assert.Equal(HttpStatusCode.BadRequest, response.StatusCode);
        var problem = await response.Content.ReadFromJsonAsync<ValidationProblem>();
        Assert.Contains("variants", problem!.Errors.Keys);
    }

    [Fact]
    public async Task CreateProduct_WithPrices_ResolvesEffectivePricesOnVariantAndFlattenedResponse()
    {
        var now = DateTimeOffset.UtcNow;
        var response = await _client.PostAsJsonAsync("/api/v1/products", new
        {
            name = "Campaign Product",
            type = "Goods",
            taxCategoryId = 1001,
            variants = new object[]
            {
                new
                {
                    sku = Sku("campaign"),
                    prices = new object[]
                    {
                        new { currency = "NOK", amount = 599m },
                        new { currency = "SEK", amount = 649m },
                        new { currency = "NOK", amount = 499m, validFrom = now.AddDays(-1), validTo = now.AddDays(1) },
                    },
                },
            },
        });

        Assert.Equal(HttpStatusCode.Created, response.StatusCode);
        var created = await response.Content.ReadFromJsonAsync<Product>();
        Assert.NotNull(created);
        Assert.Equal(2, created.EffectivePrices.Count);
        Assert.Equal(499m, created.EffectivePrices.Single(price => price.Currency == "NOK").Amount);
        Assert.Equal(created.EffectivePrices, created.Variants.Single().EffectivePrices);
    }

    [Fact]
    public async Task CreateProduct_WithDuplicateSku_ReturnsConflict()
    {
        var sku = Sku("dup");
        Assert.Equal(HttpStatusCode.Created, (await _client.PostAsJsonAsync("/api/v1/products", NewProduct("First", sku))).StatusCode);
        Assert.Equal(HttpStatusCode.Conflict, (await _client.PostAsJsonAsync("/api/v1/products", NewProduct("Second", sku))).StatusCode);
    }

    [Fact]
    public async Task UpdateVariant_WithDuplicateSku_ReturnsConflict()
    {
        var first = await CreateAsync("First Variant", Sku("update-dup-first"));
        var second = await CreateAsync("Second Variant", Sku("update-dup-second"));

        var response = await _client.PutAsJsonAsync($"/api/v1/products/{second.Id}/variants/{second.Variants.Single().Id}", new
        {
            sku = first.Sku,
        });

        Assert.Equal(HttpStatusCode.Conflict, response.StatusCode);
    }

    [Fact]
    public async Task CreateProduct_WithConflictingBasePrices_ReturnsFieldError()
    {
        var response = await _client.PostAsJsonAsync("/api/v1/products", new
        {
            name = "Conflicting",
            type = "Goods",
            taxCategoryId = 1001,
            variants = new[]
            {
                new
                {
                    sku = Sku("conflict"),
                    prices = new[] { new { currency = "NOK", amount = 599m }, new { currency = "NOK", amount = 649m } },
                },
            },
        });

        Assert.Equal(HttpStatusCode.BadRequest, response.StatusCode);
        var problem = await response.Content.ReadFromJsonAsync<ValidationProblem>();
        Assert.Contains("variants[0].prices[1]", problem!.Errors.Keys);
    }

    [Fact]
    public async Task UpdateProduct_ChangesOnlySharedFields()
    {
        var created = await CreateAsync("Original", Sku("update"));
        var response = await _client.PutAsJsonAsync($"/api/v1/products/{created.Id}", new
        {
            name = "Renamed",
            type = "Service",
            taxCategoryId = 1001,
            description = "Updated",
        });

        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
        var updated = await response.Content.ReadFromJsonAsync<Product>();
        Assert.NotNull(updated);
        Assert.Equal("Renamed", updated.Name);
        Assert.Equal("Service", updated.Type);
        Assert.Equal(0.25m, updated.TaxCategory.Rate);
        Assert.Equal(created.Sku, updated.Sku);
    }

    [Fact]
    public async Task UpdateVariant_SkuChangeOnDraft_IsAllowed()
    {
        var created = await CreateAsync("Draft Sku Change", Sku("draft-sku"));
        var variant = created.Variants.Single();
        var newSku = Sku("draft-sku-new");

        var response = await _client.PutAsJsonAsync($"/api/v1/products/{created.Id}/variants/{variant.Id}", new
        {
            sku = newSku,
            optionValues = new { Color = "Red" },
        });

        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
        var updated = await response.Content.ReadFromJsonAsync<Variant>();
        Assert.Equal(newSku, updated!.Sku);
        Assert.Equal("Red", updated.OptionValues["color"]);
    }

    [Fact]
    public async Task UpdateVariant_SkuChangeOnActiveProduct_ReturnsConflict()
    {
        var created = await CreateAsync("Active Sku Change", Sku("active-sku"), status: "Active");
        var variant = created.Variants.Single();
        var response = await _client.PutAsJsonAsync($"/api/v1/products/{created.Id}/variants/{variant.Id}", new { sku = Sku("active-sku-new") });
        Assert.Equal(HttpStatusCode.Conflict, response.StatusCode);
    }

    [Fact]
    public async Task VariantCrud_RejectsDeletingLastVariant()
    {
        var created = await CreateAsync("Variant CRUD", Sku("variant-crud"));
        var first = created.Variants.Single();

        var lastDelete = await _client.DeleteAsync($"/api/v1/products/{created.Id}/variants/{first.Id}");
        Assert.Equal(HttpStatusCode.Conflict, lastDelete.StatusCode);

        var add = await _client.PostAsJsonAsync($"/api/v1/products/{created.Id}/variants", new
        {
            sku = Sku("variant-two"),
            optionValues = new { Color = "Blue" },
        });
        Assert.Equal(HttpStatusCode.Created, add.StatusCode);
        var second = await add.Content.ReadFromJsonAsync<Variant>();

        Assert.Equal(HttpStatusCode.NoContent, (await _client.DeleteAsync($"/api/v1/products/{created.Id}/variants/{second!.Id}")).StatusCode);
        Assert.Equal(HttpStatusCode.Conflict, (await _client.DeleteAsync($"/api/v1/products/{created.Id}/variants/{first.Id}")).StatusCode);
    }

    [Fact]
    public async Task GetProduct_MultiVariantDoesNotFlattenSellableFields()
    {
        var response = await _client.PostAsJsonAsync("/api/v1/products", new
        {
            name = "Colour Assortment",
            type = "Goods",
            taxCategoryId = 1001,
            variants = new[]
            {
                new { sku = Sku("red"), optionValues = new { Color = "Red" } },
                new { sku = Sku("blue"), optionValues = new { Color = "Blue" } },
            },
        });
        var raw = await response.Content.ReadAsStringAsync();
        Assert.True(response.IsSuccessStatusCode, raw);
        var created = await response.Content.ReadFromJsonAsync<Product>();

        Assert.Equal(HttpStatusCode.Created, response.StatusCode);
        Assert.Null(created!.Sku);
        Assert.Null(created.Unit);
        Assert.Empty(created.EffectivePrices);
        Assert.Equal(2, created.Variants.Count);
        Assert.Equal("Red", created.Variants.Single(v => v.OptionValues["color"] == "Red").OptionValues["color"]);
    }

    [Fact]
    public async Task PriceSubResource_IsScopedToVariant()
    {
        var created = await CreateAsync("Priced", Sku("prices"));
        var variant = created.Variants.Single();
        var basePath = $"/api/v1/products/{created.Id}/variants/{variant.Id}/prices";

        var add = await _client.PostAsJsonAsync(basePath, new { currency = "nok", amount = 599m });
        Assert.Equal(HttpStatusCode.Created, add.StatusCode);
        var price = await add.Content.ReadFromJsonAsync<Price>();
        Assert.Equal("NOK", price!.Currency);

        Assert.Equal(HttpStatusCode.Conflict,
            (await _client.PostAsJsonAsync(basePath, new { currency = "NOK", amount = 649m })).StatusCode);
        Assert.Equal(HttpStatusCode.Created,
            (await _client.PostAsJsonAsync(basePath, new { currency = "NOK", amount = 499m, validFrom = DateTimeOffset.UtcNow.AddDays(-1), validTo = DateTimeOffset.UtcNow.AddDays(1) })).StatusCode);
        Assert.Equal(2, (await _client.GetFromJsonAsync<List<Price>>(basePath))!.Count);
        Assert.Equal(HttpStatusCode.NoContent, (await _client.DeleteAsync($"{basePath}/{price.Id}")).StatusCode);
    }

    [Fact]
    public async Task AddProductPrice_WithOverlappingOpenEndedPrice_ReturnsConflict()
    {
        var created = await CreateAsync("Overlapping Prices", Sku("overlap"));
        var variant = created.Variants.Single();
        var basePath = $"/api/v1/products/{created.Id}/variants/{variant.Id}/prices";

        Assert.Equal(HttpStatusCode.Created,
            (await _client.PostAsJsonAsync(basePath, new { currency = "NOK", amount = 599m })).StatusCode);
        Assert.Equal(HttpStatusCode.Conflict,
            (await _client.PostAsJsonAsync(basePath, new { currency = "NOK", amount = 649m })).StatusCode);
    }

    [Fact]
    public async Task AddPrice_WithInvalidWindow_ReturnsFieldError()
    {
        var created = await CreateAsync("Bad Window", Sku("window"));
        var variant = created.Variants.Single();
        var response = await _client.PostAsJsonAsync($"/api/v1/products/{created.Id}/variants/{variant.Id}/prices", new
        {
            currency = "NOK",
            amount = 599m,
            validFrom = DateTimeOffset.UtcNow,
            validTo = DateTimeOffset.UtcNow.AddDays(-1),
        });

        Assert.Equal(HttpStatusCode.BadRequest, response.StatusCode);
        var problem = await response.Content.ReadFromJsonAsync<ValidationProblem>();
        Assert.Contains("validTo", problem!.Errors.Keys);
    }

    [Fact]
    public async Task GetProduct_UnknownId_ReturnsNotFound() =>
        Assert.Equal(HttpStatusCode.NotFound, (await _client.GetAsync("/api/v1/products/999999")).StatusCode);

    private async Task<Product> CreateAsync(string name, string sku, string status = "Draft")
    {
        var response = await _client.PostAsJsonAsync("/api/v1/products", NewProduct(name, sku, status));
        Assert.Equal(HttpStatusCode.Created, response.StatusCode);
        var created = await response.Content.ReadFromJsonAsync<Product>();
        Assert.NotNull(created);
        return created;
    }

    private static object NewProduct(string name, string sku, string status = "Draft") => new
    {
        name,
        type = "Goods",
        status,
        taxCategoryId = 1001,
        variants = new[] { new { sku } },
    };

    private static string Sku(string prefix) => $"TST-{prefix}-{Guid.NewGuid():N}"[..24].ToUpperInvariant();

    private sealed record Product(int Id, string Name, string? Sku, string Type, string Status, string? Unit, TaxCategory TaxCategory, List<Variant> Variants, List<Price> EffectivePrices);
    private sealed record TaxCategory(int Id, string Name, string Kind, decimal Rate);
    private sealed record Variant(int Id, string Sku, string? Barcode, string Unit, Dictionary<string, string> OptionValues, List<Price> EffectivePrices);
    private sealed record Price(int Id, string Currency, decimal Amount, DateTimeOffset? ValidFrom, DateTimeOffset? ValidTo);
    private sealed record ValidationProblem(Dictionary<string, string[]> Errors);
}