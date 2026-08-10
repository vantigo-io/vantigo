using System.Net;
using System.Net.Http.Json;

namespace Vantigo.Products.Module.Tests.Integration;

[Collection(ProductsModuleCollection.Name)]
public sealed class ProductCatalogFieldsTests
{
    private readonly HttpClient _client;

    public ProductCatalogFieldsTests(ProductsModuleFactory factory) => _client = factory.CreateAuthenticatedClient();

    [Fact]
    public async Task CreateProduct_WithCatalogFields_ReturnsThemOnVariantAndFlattenedResponse()
    {
        var category = await CreateCategoryAsync(Name("goods"));
        var barcode = NewGtin13();
        var response = await _client.PostAsJsonAsync("/api/v1/products", new
        {
            name = "Catalog Product",
            type = "Goods",
            taxCategoryId = 1001,
            description = "A richly described product.",
            categoryId = category.Id,
            variants = new[]
            {
                new { sku = Sku("catalog"), barcode, weightKg = 1.25, lengthCm = 30.5, widthCm = 20, heightCm = 10 },
            },
        });

        Assert.Equal(HttpStatusCode.Created, response.StatusCode);
        var created = await response.Content.ReadFromJsonAsync<Product>();
        Assert.NotNull(created);
        Assert.Equal("A richly described product.", created.Description);
        Assert.Equal(category.Id, created.Category!.Id);
        Assert.Equal(barcode, created.Barcode);
        Assert.Equal(1.25m, created.WeightKg);
        Assert.Equal(30.5m, created.Variants.Single().LengthCm);
    }

    [Fact]
    public async Task UpdateVariant_SetsAndClearsCatalogFields()
    {
        var created = await CreateProductAsync("Catalog Update", Sku("cat-upd"));
        var variant = created.Variants.Single();
        var barcode = NewGtin13();
        var update = await _client.PutAsJsonAsync($"/api/v1/products/{created.Id}/variants/{variant.Id}", new
        {
            sku = variant.Sku,
            barcode,
            weightKg = 2.5,
        });

        Assert.Equal(HttpStatusCode.OK, update.StatusCode);
        var updated = await update.Content.ReadFromJsonAsync<Variant>();
        Assert.Equal(barcode, updated!.Barcode);
        Assert.Equal(2.5m, updated.WeightKg);

        var clear = await _client.PutAsJsonAsync($"/api/v1/products/{created.Id}/variants/{variant.Id}", new { sku = variant.Sku });
        Assert.Equal(HttpStatusCode.OK, clear.StatusCode);
        var cleared = await clear.Content.ReadFromJsonAsync<Variant>();
        Assert.Null(cleared!.Barcode);
        Assert.Null(cleared.WeightKg);
    }

    [Fact]
    public async Task CreateProduct_WithInvalidGtin_ReturnsFieldError()
    {
        var response = await _client.PostAsJsonAsync("/api/v1/products", new
        {
            name = "Bad Barcode",
            type = "Goods",
            taxCategoryId = 1001,
            variants = new[] { new { sku = Sku("bad-gtin"), barcode = "4006381333932" } },
        });

        Assert.Equal(HttpStatusCode.BadRequest, response.StatusCode);
        var problem = await response.Content.ReadFromJsonAsync<ValidationProblem>();
        Assert.Contains("variants[0].barcode", problem!.Errors.Keys);
    }

    [Fact]
    public async Task CreateProduct_WithDuplicateBarcode_ReturnsConflict()
    {
        var barcode = NewGtin13();
        Assert.Equal(HttpStatusCode.Created, (await _client.PostAsJsonAsync("/api/v1/products", NewProduct("First Barcode", Sku("bar-1"), barcode))).StatusCode);
        Assert.Equal(HttpStatusCode.Conflict, (await _client.PostAsJsonAsync("/api/v1/products", NewProduct("Second Barcode", Sku("bar-2"), barcode))).StatusCode);
    }

    [Fact]
    public async Task UpdateVariant_WithBarcodeOfAnotherProduct_ReturnsConflict()
    {
        var barcode = NewGtin13();
        var first = await _client.PostAsJsonAsync("/api/v1/products", NewProduct("Barcode Owner", Sku("bar-own"), barcode));
        Assert.Equal(HttpStatusCode.Created, first.StatusCode);
        var other = await CreateProductAsync("Barcode Thief", Sku("bar-thief"));
        var update = await _client.PutAsJsonAsync($"/api/v1/products/{other.Id}/variants/{other.Variants.Single().Id}", new { sku = other.Sku, barcode });
        Assert.Equal(HttpStatusCode.Conflict, update.StatusCode);
    }

    [Fact]
    public async Task CreateProduct_WithUnknownCategory_ReturnsFieldError()
    {
        var response = await _client.PostAsJsonAsync("/api/v1/products", new
        {
            name = "Orphan",
            type = "Goods",
            taxCategoryId = 1001,
            categoryId = 999999,
            variants = new[] { new { sku = Sku("orphan") } },
        });
        Assert.Equal(HttpStatusCode.BadRequest, response.StatusCode);
        var problem = await response.Content.ReadFromJsonAsync<ValidationProblem>();
        Assert.Contains("categoryId", problem!.Errors.Keys);
    }

    [Fact]
    public async Task GetProducts_SearchByExactBarcode_ReturnsOnlyThatProduct()
    {
        var barcode = NewGtin13();
        var create = await _client.PostAsJsonAsync("/api/v1/products", NewProduct("Barcode Searchable", Sku("search-bar"), barcode));
        var created = await create.Content.ReadFromJsonAsync<Product>();
        var result = await _client.GetFromJsonAsync<ProductList>($"/api/v1/products?search={barcode}");
        Assert.Equal(created!.Id, Assert.Single(result!.Data).Id);
    }

    [Fact]
    public async Task GetProducts_SearchByVariantSku_ReturnsOnlyThatProduct()
    {
        var sku = Sku("search-variant");
        var created = await CreateProductAsync("SKU Searchable", sku);

        var result = await _client.GetFromJsonAsync<ProductList>($"/api/v1/products?search={sku}");
        Assert.Equal(created.Id, Assert.Single(result!.Data).Id);

        var unrelated = await _client.GetFromJsonAsync<ProductList>($"/api/v1/products?search={Sku("not-matching")}");
        Assert.DoesNotContain(unrelated!.Data, product => product.Id == created.Id);
    }

    [Fact]
    public async Task GetProducts_SearchMatchesDescription()
    {
        var marker = Guid.NewGuid().ToString("N")[..12];
        var create = await _client.PostAsJsonAsync("/api/v1/products", new
        {
            name = "Described Product",
            type = "Goods",
            taxCategoryId = 1001,
            description = $"Contains the marker {marker} in the text.",
            variants = new[] { new { sku = Sku("search-desc") } },
        });
        var created = await create.Content.ReadFromJsonAsync<Product>();
        var result = await _client.GetFromJsonAsync<ProductList>($"/api/v1/products?search={marker}");
        Assert.Equal(created!.Id, Assert.Single(result!.Data).Id);
    }

    private async Task<Category> CreateCategoryAsync(string name, int? parentId = null)
    {
        var response = await _client.PostAsJsonAsync("/api/v1/products/categories", new { name, parentId });
        Assert.Equal(HttpStatusCode.Created, response.StatusCode);
        return (await response.Content.ReadFromJsonAsync<Category>())!;
    }

    private async Task<Product> CreateProductAsync(string name, string sku, int? categoryId = null)
    {
        var response = await _client.PostAsJsonAsync("/api/v1/products", new
        {
            name,
            type = "Goods",
            taxCategoryId = 1001,
            categoryId,
            variants = new[] { new { sku } },
        });
        Assert.Equal(HttpStatusCode.Created, response.StatusCode);
        return (await response.Content.ReadFromJsonAsync<Product>())!;
    }

    private static object NewProduct(string name, string sku, string? barcode = null) => new
    {
        name,
        type = "Goods",
        taxCategoryId = 1001,
        variants = new[] { new { sku, barcode } },
    };

    private static string Sku(string prefix) => $"TST-{prefix}-{Guid.NewGuid():N}"[..24].ToUpperInvariant();
    private static string Name(string prefix) => $"Category {prefix} {Guid.NewGuid():N}"[..28];

    private static string NewGtin13()
    {
        var digits = new int[13];
        for (var index = 0; index < 12; index++) digits[index] = Random.Shared.Next(10);
        var sum = 0;
        for (var index = 0; index < 12; index++) sum += digits[index] * ((12 - index) % 2 == 1 ? 3 : 1);
        digits[12] = (10 - sum % 10) % 10;
        return string.Concat(digits);
    }

    private sealed record Category(int Id, string Name, int? ParentId);
    private sealed record Product(int Id, string Name, string? Sku, string Type, string Status, TaxCategory TaxCategory, string? Description, Category? Category, string? Barcode, decimal? WeightKg, decimal? LengthCm, decimal? WidthCm, decimal? HeightCm, List<Variant> Variants);
    private sealed record TaxCategory(int Id, string Name, string Kind, decimal Rate);
    private sealed record Variant(int Id, string Sku, string? Barcode, decimal? WeightKg, decimal? LengthCm, decimal? WidthCm, decimal? HeightCm);
    private sealed record ProductList(List<Product> Data);
    private sealed record ValidationProblem(Dictionary<string, string[]> Errors);
}