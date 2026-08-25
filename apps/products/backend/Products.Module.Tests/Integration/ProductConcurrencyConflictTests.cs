using System.Net;
using System.Net.Http.Json;

namespace Vantigo.Products.Module.Tests.Integration;

/// <summary>
/// Concurrent creates race past the endpoints' friendly uniqueness pre-checks;
/// the database unique index decides the winner and the losers must surface as
/// conflicts, not server errors.
/// </summary>
[Collection(ProductsModuleCollection.Name)]
public sealed class ProductConcurrencyConflictTests
{
    private readonly HttpClient _client;

    public ProductConcurrencyConflictTests(ProductsModuleFactory factory)
    {
        _client = factory.CreateAuthenticatedClient();
    }

    [Fact]
    public async Task Concurrent_creates_with_the_same_sku_yield_one_success_and_conflicts()
    {
        var sku = $"RACE-{Guid.NewGuid().ToString("N")[..10]}".ToUpperInvariant();

        var responses = await Task.WhenAll(Enumerable.Range(0, 4).Select(index =>
            _client.PostAsJsonAsync("/api/v1/products", new
            {
                name = $"Race product {index}",
                type = "Goods",
                taxCategoryId = 1001,
                variants = new[] { new { sku } },
            })));

        Assert.Equal(1, responses.Count(response => response.StatusCode == HttpStatusCode.Created));
        Assert.All(responses.Where(response => response.StatusCode != HttpStatusCode.Created),
            response => Assert.Equal(HttpStatusCode.Conflict, response.StatusCode));
    }
}