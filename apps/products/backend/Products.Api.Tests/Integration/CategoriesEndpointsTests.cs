using System.Net;
using System.Net.Http.Json;

namespace Vantigo.Products.Api.Tests.Integration;

[Collection(ProductsApiCollection.Name)]
public sealed class CategoriesEndpointsTests
{
    private readonly HttpClient _client;

    public CategoriesEndpointsTests(ProductsApiFactory factory)
    {
        _client = factory.CreateAuthenticatedClient();
    }

    [Fact]
    public async Task CreateCategory_AsRootAndSubcategory_ReturnsCreatedWithLocation()
    {
        var root = await CreateAsync(Name("root"));
        Assert.True(root.Id > 0);
        Assert.Null(root.ParentId);

        var childResponse = await _client.PostAsJsonAsync("/api/v1/categories", new
        {
            name = Name("child"),
            parentId = root.Id,
        });

        Assert.Equal(HttpStatusCode.Created, childResponse.StatusCode);
        var child = await childResponse.Content.ReadFromJsonAsync<Category>();
        Assert.NotNull(child);
        Assert.Equal(root.Id, child.ParentId);
        Assert.Equal($"/api/v1/categories/{child.Id}", childResponse.Headers.Location?.AbsolutePath);
    }

    [Fact]
    public async Task GetCategories_ReturnsFlatListIncludingCreated()
    {
        var root = await CreateAsync(Name("list-root"));
        var child = await CreateAsync(Name("list-child"), root.Id);

        var categories = await _client.GetFromJsonAsync<List<Category>>("/api/v1/categories");

        Assert.NotNull(categories);
        Assert.Contains(categories, c => c.Id == root.Id && c.ParentId == null);
        Assert.Contains(categories, c => c.Id == child.Id && c.ParentId == root.Id);
    }

    [Fact]
    public async Task GetCategories_ReportsDirectProductCounts()
    {
        var root = await CreateAsync(Name("count-root"));
        var child = await CreateAsync(Name("count-child"), root.Id);

        // Two products directly on the child, none on the root: the counts are
        // direct assignments only, subtree totals are a client concern.
        await CreateProductAsync("Counted One", child.Id);
        await CreateProductAsync("Counted Two", child.Id);

        var categories = await _client.GetFromJsonAsync<List<Category>>("/api/v1/categories");

        Assert.NotNull(categories);
        Assert.Equal(2, categories.Single(c => c.Id == child.Id).ProductCount);
        Assert.Equal(0, categories.Single(c => c.Id == root.Id).ProductCount);
    }

    [Fact]
    public async Task CreateCategory_WithMissingName_ReturnsFieldError()
    {
        var response = await _client.PostAsJsonAsync("/api/v1/categories", new { name = "  " });

        Assert.Equal(HttpStatusCode.BadRequest, response.StatusCode);
        var problem = await response.Content.ReadFromJsonAsync<ValidationProblem>();
        Assert.Contains("name", problem!.Errors.Keys);
    }

    [Fact]
    public async Task CreateCategory_WithUnknownParent_ReturnsFieldError()
    {
        var response = await _client.PostAsJsonAsync("/api/v1/categories", new
        {
            name = Name("orphan"),
            parentId = 999999,
        });

        Assert.Equal(HttpStatusCode.BadRequest, response.StatusCode);
        var problem = await response.Content.ReadFromJsonAsync<ValidationProblem>();
        Assert.Contains("parentId", problem!.Errors.Keys);
    }

    [Fact]
    public async Task CreateCategory_WithDuplicateSiblingName_ReturnsConflict()
    {
        var name = Name("dup");
        var first = await _client.PostAsJsonAsync("/api/v1/categories", new { name });
        Assert.Equal(HttpStatusCode.Created, first.StatusCode);

        var second = await _client.PostAsJsonAsync("/api/v1/categories", new { name });
        Assert.Equal(HttpStatusCode.Conflict, second.StatusCode);
    }

    [Fact]
    public async Task UpdateCategory_RenamesAndReparents()
    {
        var root = await CreateAsync(Name("move-root"));
        var other = await CreateAsync(Name("move-other"));
        var child = await CreateAsync(Name("move-child"), root.Id);

        var newName = Name("moved");
        var response = await _client.PutAsJsonAsync($"/api/v1/categories/{child.Id}", new
        {
            name = newName,
            parentId = other.Id,
        });

        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
        var updated = await response.Content.ReadFromJsonAsync<Category>();
        Assert.NotNull(updated);
        Assert.Equal(newName, updated.Name);
        Assert.Equal(other.Id, updated.ParentId);
    }

    [Fact]
    public async Task UpdateCategory_MovingUnderDescendant_ReturnsConflict()
    {
        var root = await CreateAsync(Name("cycle-root"));
        var child = await CreateAsync(Name("cycle-child"), root.Id);
        var grandchild = await CreateAsync(Name("cycle-grand"), child.Id);

        var response = await _client.PutAsJsonAsync($"/api/v1/categories/{root.Id}", new
        {
            name = root.Name,
            parentId = grandchild.Id,
        });

        Assert.Equal(HttpStatusCode.Conflict, response.StatusCode);
    }

    [Fact]
    public async Task UpdateCategory_UnderItself_ReturnsFieldError()
    {
        var category = await CreateAsync(Name("self"));

        var response = await _client.PutAsJsonAsync($"/api/v1/categories/{category.Id}", new
        {
            name = category.Name,
            parentId = category.Id,
        });

        Assert.Equal(HttpStatusCode.BadRequest, response.StatusCode);
        var problem = await response.Content.ReadFromJsonAsync<ValidationProblem>();
        Assert.Contains("parentId", problem!.Errors.Keys);
    }

    [Fact]
    public async Task DeleteCategory_WithChildren_ReturnsConflict()
    {
        var root = await CreateAsync(Name("del-root"));
        await CreateAsync(Name("del-child"), root.Id);

        var response = await _client.DeleteAsync($"/api/v1/categories/{root.Id}");

        Assert.Equal(HttpStatusCode.Conflict, response.StatusCode);
    }

    [Fact]
    public async Task DeleteCategory_WithAssignedProducts_ReturnsConflict()
    {
        var category = await CreateAsync(Name("del-assigned"));
        var create = await _client.PostAsJsonAsync("/api/v1/products", new
        {
            name = "Categorised Product",
            sku = $"TST-CAT-{Guid.NewGuid().ToString("N")[..8]}".ToUpperInvariant(),
            type = "Goods",
            vatRate = 0.25,
            categoryId = category.Id,
        });
        Assert.Equal(HttpStatusCode.Created, create.StatusCode);

        var response = await _client.DeleteAsync($"/api/v1/categories/{category.Id}");

        Assert.Equal(HttpStatusCode.Conflict, response.StatusCode);
    }

    [Fact]
    public async Task DeleteCategory_WithoutChildrenOrProducts_ReturnsNoContent()
    {
        var category = await CreateAsync(Name("del-empty"));

        var response = await _client.DeleteAsync($"/api/v1/categories/{category.Id}");
        Assert.Equal(HttpStatusCode.NoContent, response.StatusCode);

        var fetch = await _client.GetAsync($"/api/v1/categories/{category.Id}");
        Assert.Equal(HttpStatusCode.NotFound, fetch.StatusCode);
    }

    [Fact]
    public async Task DeleteCategory_UnknownId_ReturnsNotFound()
    {
        var response = await _client.DeleteAsync("/api/v1/categories/999999");
        Assert.Equal(HttpStatusCode.NotFound, response.StatusCode);
    }

    private async Task<Category> CreateAsync(string name, int? parentId = null)
    {
        var response = await _client.PostAsJsonAsync("/api/v1/categories", new { name, parentId });
        Assert.Equal(HttpStatusCode.Created, response.StatusCode);
        var created = await response.Content.ReadFromJsonAsync<Category>();
        Assert.NotNull(created);
        return created;
    }

    private async Task CreateProductAsync(string name, int categoryId)
    {
        var response = await _client.PostAsJsonAsync("/api/v1/products", new
        {
            name,
            sku = $"TST-CNT-{Guid.NewGuid().ToString("N")[..8]}".ToUpperInvariant(),
            type = "Goods",
            vatRate = 0.25,
            categoryId,
        });
        Assert.Equal(HttpStatusCode.Created, response.StatusCode);
    }

    private static string Name(string prefix) =>
        $"Category {prefix} {Guid.NewGuid().ToString("N")[..8]}";

    private sealed record Category(int Id, string Name, int? ParentId, int ProductCount);

    private sealed record ValidationProblem(Dictionary<string, string[]> Errors);
}