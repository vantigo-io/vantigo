using System.Net;
using System.Net.Http.Json;

namespace Vantigo.Products.Api.Tests.Integration;

[Collection(ProductsApiCollection.Name)]
public sealed class ProductCatalogFieldsTests
{
    private readonly HttpClient _client;

    public ProductCatalogFieldsTests(ProductsApiFactory factory)
    {
        _client = factory.CreateAuthenticatedClient();
    }

    [Fact]
    public async Task CreateProduct_WithCatalogFields_ReturnsThemInResponse()
    {
        var category = await CreateCategoryAsync(Name("goods"));
        var barcode = NewGtin13();

        var response = await _client.PostAsJsonAsync("/api/v1/products", new
        {
            name = "Catalog Product",
            sku = Sku("catalog"),
            type = "Goods",
            vatRate = 0.25,
            description = "A richly described product.",
            categoryId = category.Id,
            barcode,
            weightKg = 1.25,
            lengthCm = 30.5,
            widthCm = 20,
            heightCm = 10,
        });

        Assert.Equal(HttpStatusCode.Created, response.StatusCode);
        var created = await response.Content.ReadFromJsonAsync<Product>();
        Assert.NotNull(created);
        Assert.Equal("A richly described product.", created.Description);
        Assert.NotNull(created.Category);
        Assert.Equal(category.Id, created.Category.Id);
        Assert.Equal(category.Name, created.Category.Name);
        Assert.Equal(barcode, created.Barcode);
        Assert.Equal(1.25m, created.WeightKg);
        Assert.Equal(30.5m, created.LengthCm);
        Assert.Equal(20m, created.WidthCm);
        Assert.Equal(10m, created.HeightCm);
    }

    [Fact]
    public async Task UpdateProduct_SetsAndClearsCatalogFields()
    {
        var category = await CreateCategoryAsync(Name("update"));
        var created = await CreateProductAsync("Catalog Update", Sku("cat-upd"));

        var barcode = NewGtin13();
        var update = await _client.PutAsJsonAsync($"/api/v1/products/{created.Id}", new
        {
            name = created.Name,
            sku = created.Sku,
            type = created.Type,
            vatRate = created.VatRate,
            description = "Updated description",
            categoryId = category.Id,
            barcode,
            weightKg = 2.5,
        });

        Assert.Equal(HttpStatusCode.OK, update.StatusCode);
        var updated = await update.Content.ReadFromJsonAsync<Product>();
        Assert.NotNull(updated);
        Assert.Equal("Updated description", updated.Description);
        Assert.Equal(category.Id, updated.Category?.Id);
        Assert.Equal(barcode, updated.Barcode);
        Assert.Equal(2.5m, updated.WeightKg);

        // Omitting the optional fields clears them again.
        var clear = await _client.PutAsJsonAsync($"/api/v1/products/{created.Id}", new
        {
            name = created.Name,
            sku = created.Sku,
            type = created.Type,
            vatRate = created.VatRate,
        });

        Assert.Equal(HttpStatusCode.OK, clear.StatusCode);
        var cleared = await clear.Content.ReadFromJsonAsync<Product>();
        Assert.NotNull(cleared);
        Assert.Null(cleared.Description);
        Assert.Null(cleared.Category);
        Assert.Null(cleared.Barcode);
        Assert.Null(cleared.WeightKg);
    }

    [Fact]
    public async Task CreateProduct_WithInvalidGtin_ReturnsFieldError()
    {
        var response = await _client.PostAsJsonAsync("/api/v1/products", new
        {
            name = "Bad Barcode",
            sku = Sku("bad-gtin"),
            type = "Goods",
            vatRate = 0.25,
            barcode = "4006381333932",
        });

        Assert.Equal(HttpStatusCode.BadRequest, response.StatusCode);
        var problem = await response.Content.ReadFromJsonAsync<ValidationProblem>();
        Assert.Contains("barcode", problem!.Errors.Keys);
    }

    [Fact]
    public async Task CreateProduct_WithDuplicateBarcode_ReturnsConflict()
    {
        var barcode = NewGtin13();
        var first = await _client.PostAsJsonAsync(
            "/api/v1/products",
            NewProduct("First Barcode", Sku("bar-1"), barcode));
        Assert.Equal(HttpStatusCode.Created, first.StatusCode);

        var second = await _client.PostAsJsonAsync(
            "/api/v1/products",
            NewProduct("Second Barcode", Sku("bar-2"), barcode));
        Assert.Equal(HttpStatusCode.Conflict, second.StatusCode);
    }

    [Fact]
    public async Task UpdateProduct_WithBarcodeOfAnotherProduct_ReturnsConflict()
    {
        var barcode = NewGtin13();
        var first = await _client.PostAsJsonAsync(
            "/api/v1/products",
            NewProduct("Barcode Owner", Sku("bar-own"), barcode));
        Assert.Equal(HttpStatusCode.Created, first.StatusCode);

        var other = await CreateProductAsync("Barcode Thief", Sku("bar-thief"));
        var update = await _client.PutAsJsonAsync($"/api/v1/products/{other.Id}", new
        {
            name = other.Name,
            sku = other.Sku,
            type = other.Type,
            vatRate = other.VatRate,
            barcode,
        });

        Assert.Equal(HttpStatusCode.Conflict, update.StatusCode);
    }

    [Fact]
    public async Task CreateProduct_WithUnknownCategory_ReturnsFieldError()
    {
        var response = await _client.PostAsJsonAsync("/api/v1/products", new
        {
            name = "Orphan",
            sku = Sku("orphan"),
            type = "Goods",
            vatRate = 0.25,
            categoryId = 999999,
        });

        Assert.Equal(HttpStatusCode.BadRequest, response.StatusCode);
        var problem = await response.Content.ReadFromJsonAsync<ValidationProblem>();
        Assert.Contains("categoryId", problem!.Errors.Keys);
    }

    [Fact]
    public async Task GetProducts_FilteredByCategory_IncludesDescendants()
    {
        var root = await CreateCategoryAsync(Name("filter-root"));
        var child = await CreateCategoryAsync(Name("filter-child"), root.Id);
        var unrelated = await CreateCategoryAsync(Name("filter-other"));

        var inRoot = await CreateProductAsync("Root Product", Sku("f-root"), root.Id);
        var inChild = await CreateProductAsync("Child Product", Sku("f-child"), child.Id);
        var elsewhere = await CreateProductAsync("Other Product", Sku("f-other"), unrelated.Id);

        var byRoot = await _client.GetFromJsonAsync<ProductList>(
            $"/api/v1/products?categoryId={root.Id}&pageSize=100");
        Assert.NotNull(byRoot);
        Assert.Contains(byRoot.Data, p => p.Id == inRoot.Id);
        Assert.Contains(byRoot.Data, p => p.Id == inChild.Id);
        Assert.DoesNotContain(byRoot.Data, p => p.Id == elsewhere.Id);

        var byChild = await _client.GetFromJsonAsync<ProductList>(
            $"/api/v1/products?categoryId={child.Id}&pageSize=100");
        Assert.NotNull(byChild);
        Assert.Contains(byChild.Data, p => p.Id == inChild.Id);
        Assert.DoesNotContain(byChild.Data, p => p.Id == inRoot.Id);
    }

    [Fact]
    public async Task GetProducts_SearchByExactBarcode_ReturnsOnlyThatProduct()
    {
        var barcode = NewGtin13();
        var create = await _client.PostAsJsonAsync(
            "/api/v1/products",
            NewProduct("Barcode Searchable", Sku("search-bar"), barcode));
        Assert.Equal(HttpStatusCode.Created, create.StatusCode);
        var created = await create.Content.ReadFromJsonAsync<Product>();

        var result = await _client.GetFromJsonAsync<ProductList>($"/api/v1/products?search={barcode}");

        Assert.NotNull(result);
        var match = Assert.Single(result.Data);
        Assert.Equal(created!.Id, match.Id);
    }

    [Fact]
    public async Task GetProducts_SearchMatchesDescription()
    {
        var marker = Guid.NewGuid().ToString("N")[..12];
        var create = await _client.PostAsJsonAsync("/api/v1/products", new
        {
            name = "Described Product",
            sku = Sku("search-desc"),
            type = "Goods",
            vatRate = 0.25,
            description = $"Contains the marker {marker} in the text.",
        });
        Assert.Equal(HttpStatusCode.Created, create.StatusCode);
        var created = await create.Content.ReadFromJsonAsync<Product>();

        var result = await _client.GetFromJsonAsync<ProductList>($"/api/v1/products?search={marker}");

        Assert.NotNull(result);
        var match = Assert.Single(result.Data);
        Assert.Equal(created!.Id, match.Id);
    }

    [Fact]
    public async Task GetProducts_FilteredByUncategorized_ReturnsOnlyProductsWithoutCategory()
    {
        var category = await CreateCategoryAsync(Name("uncat"));
        var categorised = await CreateProductAsync("Categorised", Sku("uncat-in"), category.Id);
        var uncategorised = await CreateProductAsync("Uncategorised", Sku("uncat-out"));

        var result = await _client.GetFromJsonAsync<ProductList>("/api/v1/products?uncategorized=true&pageSize=100");

        Assert.NotNull(result);
        Assert.Contains(result.Data, p => p.Id == uncategorised.Id);
        Assert.DoesNotContain(result.Data, p => p.Id == categorised.Id);
        Assert.All(result.Data, p => Assert.Null(p.Category));
    }

    [Fact]
    public async Task GetProducts_CombiningCategoryIdAndUncategorized_ReturnsBadRequest()
    {
        var response = await _client.GetAsync("/api/v1/products?categoryId=1&uncategorized=true");

        Assert.Equal(HttpStatusCode.BadRequest, response.StatusCode);
    }

    private async Task<Category> CreateCategoryAsync(string name, int? parentId = null)
    {
        var response = await _client.PostAsJsonAsync("/api/v1/categories", new { name, parentId });
        Assert.Equal(HttpStatusCode.Created, response.StatusCode);
        var created = await response.Content.ReadFromJsonAsync<Category>();
        Assert.NotNull(created);
        return created;
    }

    private async Task<Product> CreateProductAsync(string name, string sku, int? categoryId = null)
    {
        var response = await _client.PostAsJsonAsync("/api/v1/products", new
        {
            name,
            sku,
            type = "Goods",
            vatRate = 0.25,
            categoryId,
        });
        Assert.Equal(HttpStatusCode.Created, response.StatusCode);
        var created = await response.Content.ReadFromJsonAsync<Product>();
        Assert.NotNull(created);
        return created;
    }

    private static object NewProduct(string name, string sku, string barcode) => new
    {
        name,
        sku,
        type = "Goods",
        vatRate = 0.25,
        barcode,
    };

    private static string Sku(string prefix) =>
        $"TST-{prefix}-{Guid.NewGuid().ToString("N")[..8]}".ToUpperInvariant();

    private static string Name(string prefix) =>
        $"Category {prefix} {Guid.NewGuid().ToString("N")[..8]}";

    /// <summary>Generates a random, structurally valid GTIN-13 for tests.</summary>
    private static string NewGtin13()
    {
        var digits = new int[13];
        for (var index = 0; index < 12; index++)
        {
            digits[index] = Random.Shared.Next(10);
        }

        var sum = 0;
        for (var index = 0; index < 12; index++)
        {
            var weight = (12 - index) % 2 == 1 ? 3 : 1;
            sum += digits[index] * weight;
        }

        digits[12] = (10 - sum % 10) % 10;
        return string.Concat(digits);
    }

    private sealed record Category(int Id, string Name, int? ParentId);

    private sealed record CategoryRef(int Id, string Name);

    private sealed record Product(
        int Id,
        string Name,
        string Sku,
        string Type,
        string Status,
        decimal VatRate,
        string? Description,
        CategoryRef? Category,
        string? Barcode,
        decimal? WeightKg,
        decimal? LengthCm,
        decimal? WidthCm,
        decimal? HeightCm);

    private sealed record ProductList(List<Product> Data);

    private sealed record ValidationProblem(Dictionary<string, string[]> Errors);
}