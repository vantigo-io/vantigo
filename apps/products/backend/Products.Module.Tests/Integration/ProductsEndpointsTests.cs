using System.Net;
using System.Net.Http.Json;

namespace Vantigo.Products.Module.Tests.Integration;

[Collection(ProductsModuleCollection.Name)]
public sealed class ProductsEndpointsTests
{
    private readonly HttpClient _client;

    public ProductsEndpointsTests(ProductsModuleFactory factory)
    {
        _client = factory.CreateAuthenticatedClient();
    }

    [Fact]
    public async Task CreateProduct_WithRequiredFields_ReturnsCreatedWithLocation()
    {
        var response = await _client.PostAsJsonAsync("/api/v1/products", new
        {
            name = "Nordlys Lantern",
            sku = Sku("create"),
            type = "Goods",
            vatRate = 0.25,
        });

        Assert.Equal(HttpStatusCode.Created, response.StatusCode);

        var created = await response.Content.ReadFromJsonAsync<Product>();
        Assert.NotNull(created);
        Assert.True(created.Id > 0);
        Assert.Equal("Draft", created.Status);
        Assert.Equal("pcs", created.Unit);
        Assert.Equal($"/api/v1/products/{created.Id}", response.Headers.Location?.AbsolutePath);
    }

    [Fact]
    public async Task CreateProduct_WithPrices_ResolvesEffectivePrices()
    {
        var now = DateTimeOffset.UtcNow;
        var response = await _client.PostAsJsonAsync("/api/v1/products", new
        {
            name = "Campaign Product",
            sku = Sku("campaign"),
            type = "Goods",
            vatRate = 0.25,
            prices = new object[]
            {
                new { currency = "NOK", amount = 599m },
                new { currency = "SEK", amount = 649m },
                new
                {
                    currency = "NOK",
                    amount = 499m,
                    validFrom = now.AddDays(-1),
                    validTo = now.AddDays(1),
                },
            },
        });

        Assert.Equal(HttpStatusCode.Created, response.StatusCode);
        var created = await response.Content.ReadFromJsonAsync<Product>();

        Assert.NotNull(created);
        Assert.Equal(2, created.EffectivePrices.Count);
        Assert.Equal(499m, created.EffectivePrices.Single(price => price.Currency == "NOK").Amount);
        Assert.Equal(649m, created.EffectivePrices.Single(price => price.Currency == "SEK").Amount);
    }

    [Fact]
    public async Task CreateProduct_WithDuplicateSku_ReturnsConflict()
    {
        var sku = Sku("dup");
        var first = await _client.PostAsJsonAsync("/api/v1/products", NewProduct("First", sku));
        Assert.Equal(HttpStatusCode.Created, first.StatusCode);

        var second = await _client.PostAsJsonAsync("/api/v1/products", NewProduct("Second", sku));
        Assert.Equal(HttpStatusCode.Conflict, second.StatusCode);
    }

    [Theory]
    [InlineData("", "sku")]
    [InlineData("   ", "sku")]
    public async Task CreateProduct_WithMissingSku_ReturnsBadRequestWithFieldError(string sku, string expectedField)
    {
        var response = await _client.PostAsJsonAsync("/api/v1/products", new
        {
            name = "Invalid",
            sku,
            type = "Goods",
            vatRate = 0.25,
        });

        Assert.Equal(HttpStatusCode.BadRequest, response.StatusCode);
        var problem = await response.Content.ReadFromJsonAsync<ValidationProblem>();
        Assert.Contains(expectedField, problem!.Errors.Keys);
    }

    [Fact]
    public async Task CreateProduct_WithInvalidTypeAndVatRate_ReturnsFieldErrors()
    {
        var response = await _client.PostAsJsonAsync("/api/v1/products", new
        {
            name = "Invalid",
            sku = Sku("invalid"),
            type = "Subscription",
            vatRate = 25,
        });

        Assert.Equal(HttpStatusCode.BadRequest, response.StatusCode);
        var problem = await response.Content.ReadFromJsonAsync<ValidationProblem>();
        Assert.Contains("type", problem!.Errors.Keys);
        Assert.Contains("vatRate", problem.Errors.Keys);
    }

    [Fact]
    public async Task CreateProduct_WithConflictingBasePrices_ReturnsFieldError()
    {
        var response = await _client.PostAsJsonAsync("/api/v1/products", new
        {
            name = "Conflicting",
            sku = Sku("conflict"),
            type = "Goods",
            vatRate = 0.25,
            prices = new object[]
            {
                new { currency = "NOK", amount = 599m },
                new { currency = "NOK", amount = 649m },
            },
        });

        Assert.Equal(HttpStatusCode.BadRequest, response.StatusCode);
        var problem = await response.Content.ReadFromJsonAsync<ValidationProblem>();
        Assert.Contains("prices[1]", problem!.Errors.Keys);
    }

    [Fact]
    public async Task UpdateProduct_ChangesFields()
    {
        var created = await CreateAsync("Original", Sku("update"));

        var response = await _client.PutAsJsonAsync($"/api/v1/products/{created.Id}", new
        {
            name = "Renamed",
            sku = created.Sku,
            type = "Service",
            unit = "hour",
            standardCost = 500m,
            vatRate = 0.12,
        });

        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
        var updated = await response.Content.ReadFromJsonAsync<Product>();
        Assert.NotNull(updated);
        Assert.Equal("Renamed", updated.Name);
        Assert.Equal("Service", updated.Type);
        Assert.Equal("hour", updated.Unit);
        Assert.Equal(500m, updated.StandardCost);
        Assert.Equal(0.12m, updated.VatRate);
    }

    [Fact]
    public async Task UpdateProduct_SkuChangeOnDraft_IsAllowed()
    {
        var created = await CreateAsync("Draft Sku Change", Sku("draft-sku"));
        var newSku = Sku("draft-sku-new");

        var response = await _client.PutAsJsonAsync($"/api/v1/products/{created.Id}", new
        {
            name = created.Name,
            sku = newSku,
            type = created.Type,
            vatRate = created.VatRate,
        });

        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
        var updated = await response.Content.ReadFromJsonAsync<Product>();
        Assert.NotNull(updated);
        Assert.Equal(newSku, updated.Sku);
    }

    [Fact]
    public async Task UpdateProduct_SkuChangeOnActiveProduct_ReturnsConflict()
    {
        var created = await CreateAsync("Active Sku Change", Sku("active-sku"), status: "Active");

        var response = await _client.PutAsJsonAsync($"/api/v1/products/{created.Id}", new
        {
            name = created.Name,
            sku = Sku("active-sku-new"),
            type = created.Type,
            status = "Active",
            vatRate = created.VatRate,
        });

        Assert.Equal(HttpStatusCode.Conflict, response.StatusCode);
    }

    [Fact]
    public async Task UpdateProduct_UnknownId_ReturnsNotFound()
    {
        var response = await _client.PutAsJsonAsync("/api/v1/products/999999", new
        {
            name = "Ghost",
            sku = Sku("ghost"),
            type = "Goods",
            vatRate = 0.25,
        });

        Assert.Equal(HttpStatusCode.NotFound, response.StatusCode);
    }

    [Fact]
    public async Task DeleteProduct_ArchivesInsteadOfDeleting()
    {
        var created = await CreateAsync("To Archive", Sku("archive"), status: "Active");

        var archive = await _client.DeleteAsync($"/api/v1/products/{created.Id}");
        Assert.Equal(HttpStatusCode.NoContent, archive.StatusCode);

        var fetched = await _client.GetFromJsonAsync<Product>($"/api/v1/products/{created.Id}");
        Assert.NotNull(fetched);
        Assert.Equal("Discontinued", fetched.Status);

        // Archiving is idempotent.
        var again = await _client.DeleteAsync($"/api/v1/products/{created.Id}");
        Assert.Equal(HttpStatusCode.NoContent, again.StatusCode);
    }

    [Fact]
    public async Task GetProducts_FiltersBySearchAndStatus()
    {
        var marker = Guid.NewGuid().ToString("N")[..8];
        await CreateAsync($"Searchable {marker} Active", Sku($"search-a-{marker}"), status: "Active");
        await CreateAsync($"Searchable {marker} Draft", Sku($"search-d-{marker}"));

        var page = await _client.GetFromJsonAsync<ProductList>(
            $"/api/v1/products?search={marker}&status=Active");

        Assert.NotNull(page);
        var item = Assert.Single(page.Data);
        Assert.Equal($"Searchable {marker} Active", item.Name);
    }

    [Fact]
    public async Task GetProducts_WithInvalidQuery_ReturnsBadRequest()
    {
        var response = await _client.GetAsync("/api/v1/products?page=0&status=Unknown");
        Assert.Equal(HttpStatusCode.BadRequest, response.StatusCode);
    }

    [Fact]
    public async Task PriceSubResource_SupportsAddListAndDelete()
    {
        var created = await CreateAsync("Priced", Sku("prices"));

        var add = await _client.PostAsJsonAsync($"/api/v1/products/{created.Id}/prices", new
        {
            currency = "nok",
            amount = 599m,
        });
        Assert.Equal(HttpStatusCode.Created, add.StatusCode);
        var price = await add.Content.ReadFromJsonAsync<Price>();
        Assert.NotNull(price);
        Assert.Equal("NOK", price.Currency);

        // A second open-ended NOK price would make resolution ambiguous.
        var conflicting = await _client.PostAsJsonAsync($"/api/v1/products/{created.Id}/prices", new
        {
            currency = "NOK",
            amount = 649m,
        });
        Assert.Equal(HttpStatusCode.Conflict, conflicting.StatusCode);

        // A bounded campaign price next to the base price is fine.
        var campaign = await _client.PostAsJsonAsync($"/api/v1/products/{created.Id}/prices", new
        {
            currency = "NOK",
            amount = 499m,
            validFrom = DateTimeOffset.UtcNow.AddDays(-1),
            validTo = DateTimeOffset.UtcNow.AddDays(1),
        });
        Assert.Equal(HttpStatusCode.Created, campaign.StatusCode);

        var prices = await _client.GetFromJsonAsync<List<Price>>($"/api/v1/products/{created.Id}/prices");
        Assert.Equal(2, prices!.Count);

        var delete = await _client.DeleteAsync($"/api/v1/products/{created.Id}/prices/{price.Id}");
        Assert.Equal(HttpStatusCode.NoContent, delete.StatusCode);

        prices = await _client.GetFromJsonAsync<List<Price>>($"/api/v1/products/{created.Id}/prices");
        Assert.Single(prices!);
    }

    [Fact]
    public async Task UpdatePrice_ChangesAmountAndDates()
    {
        var created = await CreateAsync("Editable Price", Sku("edit-price"));
        var add = await _client.PostAsJsonAsync($"/api/v1/products/{created.Id}/prices", new
        {
            currency = "NOK",
            amount = 599m,
        });
        Assert.Equal(HttpStatusCode.Created, add.StatusCode);
        var price = await add.Content.ReadFromJsonAsync<Price>();

        var from = DateTimeOffset.UtcNow.AddDays(-1);
        var to = DateTimeOffset.UtcNow.AddDays(1);
        var update = await _client.PutAsJsonAsync(
            $"/api/v1/products/{created.Id}/prices/{price!.Id}",
            new { currency = "NOK", amount = 549m, validFrom = from, validTo = to });

        Assert.Equal(HttpStatusCode.OK, update.StatusCode);
        var updated = await update.Content.ReadFromJsonAsync<Price>();
        Assert.Equal(549m, updated!.Amount);
        Assert.Equal(from, updated.ValidFrom);
        Assert.Equal(to, updated.ValidTo);
    }

    [Fact]
    public async Task UpdatePrice_DoesNotConflictWithItself()
    {
        var created = await CreateAsync("Self Edit", Sku("self-edit"));
        var add = await _client.PostAsJsonAsync($"/api/v1/products/{created.Id}/prices", new
        {
            currency = "NOK",
            amount = 599m,
        });
        var price = await add.Content.ReadFromJsonAsync<Price>();

        // Updating only the amount keeps the same open-ended window; the row must
        // not be rejected for overlapping itself.
        var update = await _client.PutAsJsonAsync(
            $"/api/v1/products/{created.Id}/prices/{price!.Id}",
            new { currency = "NOK", amount = 649m });

        Assert.Equal(HttpStatusCode.OK, update.StatusCode);
    }

    [Fact]
    public async Task UpdatePrice_CreatingAmbiguousOverlap_ReturnsConflict()
    {
        var created = await CreateAsync("Overlap Edit", Sku("overlap-edit"));
        await _client.PostAsJsonAsync($"/api/v1/products/{created.Id}/prices", new
        {
            currency = "NOK",
            amount = 599m,
        });
        var addSek = await _client.PostAsJsonAsync($"/api/v1/products/{created.Id}/prices", new
        {
            currency = "SEK",
            amount = 649m,
        });
        var sekPrice = await addSek.Content.ReadFromJsonAsync<Price>();

        // Re-pointing the SEK base row at NOK would collide with the NOK base row.
        var update = await _client.PutAsJsonAsync(
            $"/api/v1/products/{created.Id}/prices/{sekPrice!.Id}",
            new { currency = "NOK", amount = 649m });

        Assert.Equal(HttpStatusCode.Conflict, update.StatusCode);
    }

    [Fact]
    public async Task UpdatePrice_UnknownIds_ReturnNotFound()
    {
        var created = await CreateAsync("Missing Price", Sku("missing-price"));

        var unknownPrice = await _client.PutAsJsonAsync(
            $"/api/v1/products/{created.Id}/prices/999999",
            new { currency = "NOK", amount = 100m });
        Assert.Equal(HttpStatusCode.NotFound, unknownPrice.StatusCode);

        var unknownProduct = await _client.PutAsJsonAsync(
            "/api/v1/products/999999/prices/1",
            new { currency = "NOK", amount = 100m });
        Assert.Equal(HttpStatusCode.NotFound, unknownProduct.StatusCode);
    }

    [Fact]
    public async Task AddPrice_WithInvalidWindow_ReturnsFieldError()
    {
        var created = await CreateAsync("Bad Window", Sku("window"));
        var now = DateTimeOffset.UtcNow;

        var response = await _client.PostAsJsonAsync($"/api/v1/products/{created.Id}/prices", new
        {
            currency = "NOK",
            amount = 599m,
            validFrom = now,
            validTo = now.AddDays(-1),
        });

        Assert.Equal(HttpStatusCode.BadRequest, response.StatusCode);
        var problem = await response.Content.ReadFromJsonAsync<ValidationProblem>();
        Assert.Contains("validTo", problem!.Errors.Keys);
    }

    [Fact]
    public async Task GetProduct_UnknownId_ReturnsNotFound()
    {
        var response = await _client.GetAsync("/api/v1/products/999999");
        Assert.Equal(HttpStatusCode.NotFound, response.StatusCode);
    }

    private async Task<Product> CreateAsync(string name, string sku, string status = "Draft")
    {
        var response = await _client.PostAsJsonAsync("/api/v1/products", new
        {
            name,
            sku,
            type = "Goods",
            status,
            vatRate = 0.25,
        });
        Assert.Equal(HttpStatusCode.Created, response.StatusCode);
        var created = await response.Content.ReadFromJsonAsync<Product>();
        Assert.NotNull(created);
        return created;
    }

    private static object NewProduct(string name, string sku) => new
    {
        name,
        sku,
        type = "Goods",
        vatRate = 0.25,
    };

    private static string Sku(string prefix) =>
        $"TST-{prefix}-{Guid.NewGuid().ToString("N")[..8]}".ToUpperInvariant();

    private sealed record Product(
        int Id,
        string Name,
        string Sku,
        string Type,
        string Status,
        string Unit,
        decimal? StandardCost,
        decimal VatRate,
        List<Price> EffectivePrices);

    private sealed record Price(
        int Id,
        string Currency,
        decimal Amount,
        DateTimeOffset? ValidFrom,
        DateTimeOffset? ValidTo);

    private sealed record ProductList(List<Product> Data);

    private sealed record ValidationProblem(Dictionary<string, string[]> Errors);
}